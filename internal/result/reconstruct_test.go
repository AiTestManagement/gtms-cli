package result

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errorContract is the shape BUG-191 recovery reconstructs: a valid error
// handoff carrying identity and join fields but no test outcome.
func errorContract(task string) *ResultContract {
	return &ResultContract{
		Task:         task,
		Command:      "execute",
		Target:       "tc-11111111",
		Adapter:      "unparseable-tier2",
		Mode:         "sync",
		Created:      "2026-04-01T10:00:00Z",
		Status:       "error",
		Framework:    "bats",
		Artefact:     "test/acceptance/unparseable-contract/tc-11111111-unparseable-subject.bats",
		ArtefactHash: "0123456789abcdef",
		Summary:      "adapter result contract unusable: parsing result contract: yaml: line 11: did not find expected key",
		Completed:    "2026-04-01T10:00:01Z",
	}
}

// BUG-191: Reconstruct must overwrite a malformed file with a valid contract
// that Read can parse -- Update cannot, because it decodes the malformed bytes
// first.
func TestReconstruct_OverwritesMalformedFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "task-recon01.handoff.yaml")

	// A document Read cannot parse (the exact BUG-191 variable-indent shape).
	malformed := "log: |\n    Determining projects to restore...\n  Error Message:\n    Stack Trace:\n"
	require.NoError(t, os.WriteFile(path, []byte(malformed), 0644))

	_, readErr := Read(path)
	require.Error(t, readErr, "precondition: the seeded document must be unparseable")

	require.NoError(t, Reconstruct(path, errorContract("task-recon01")))

	rc, err := Read(path)
	require.NoError(t, err, "reconstructed handoff must be readable")
	assert.Equal(t, "error", rc.Status)
	assert.Empty(t, rc.Result)
	assert.Equal(t, "bats", rc.Framework)
	assert.Equal(t, "0123456789abcdef", rc.ArtefactHash)
	assert.Contains(t, rc.Summary, "parsing result contract")
}

// BUG-191 tc-969e199b: the recovery must CREATE the handoff when the adapter
// deleted it, not merely rewrite an existing one.
func TestReconstruct_CreatesWhenMissing(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "nested", "task-recon02.handoff.yaml")

	require.NoFileExists(t, path)
	require.NoError(t, Reconstruct(path, errorContract("task-recon02")))

	rc, err := Read(path)
	require.NoError(t, err)
	assert.Equal(t, "error", rc.Status)
}

// REV-164 CLAUDE-003: Reconstruct refuses a directory at the target path
// instead of deleting it. The old TestReconstruct_ReplacesDirectoryInPlace
// seeded an EMPTY directory, so it passed without exercising anything -- and
// the behaviour it advertised (clear the directory, write in its place) was
// only ever true for an empty one. The honest contract is: report the
// directory, leave every byte of it alone, and let the caller route the task to
// error/ (see TestBUG191_DirectoryAtHandoffPath_* in internal/adapter).
func TestReconstruct_RefusesDirectoryAtTargetPath(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sentinel string
	}{
		{name: "empty directory"},
		{name: "non-empty directory", sentinel: "do-not-delete-me.txt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "task-recon03.handoff.yaml")
			require.NoError(t, os.Mkdir(path, 0755))

			sentinelPath := filepath.Join(path, tc.sentinel)
			const sentinelBody = "adapter working state that must survive recovery\n"
			if tc.sentinel != "" {
				require.NoError(t, os.WriteFile(sentinelPath, []byte(sentinelBody), 0644))
			}

			err := Reconstruct(path, errorContract("task-recon03"))
			require.Error(t, err, "a directory at the handoff path must be reported, not deleted")
			assert.Contains(t, err.Error(), "is a directory")

			info, statErr := os.Stat(path)
			require.NoError(t, statErr, "the directory must still be there")
			assert.True(t, info.IsDir(), "the directory must not have been replaced by a file")

			if tc.sentinel != "" {
				body, readErr := os.ReadFile(sentinelPath)
				require.NoError(t, readErr, "the directory's contents must survive untouched")
				assert.Equal(t, sentinelBody, string(body))
			}

			assertNoTempLitter(t, root)
		})
	}
}

// REV-164 CODEX-001: a failure anywhere in Reconstruct must leave the previous
// handoff exactly as it was. The validation gate runs before any disk access,
// so this pins the earliest failure point; the rename ordering pins the rest.
func TestReconstruct_ValidationFailureLeavesExistingFileIntact(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "task-recon05.handoff.yaml")
	const existing = "task: task-recon05\nstatus: error\nsummary: the earlier handoff\n"
	require.NoError(t, os.WriteFile(path, []byte(existing), 0644))

	invalid := errorContract("task-recon05")
	invalid.Status = "complete" // complete without a result fails Validate

	require.Error(t, Reconstruct(path, invalid))

	body, err := os.ReadFile(path)
	require.NoError(t, err, "the existing handoff must survive a failed reconstruction")
	assert.Equal(t, existing, string(body))
	assertNoTempLitter(t, root)
}

// REV-164 CODEX-001: the same non-destructive guarantee at the WRITE stage.
// A read-only results directory makes the temp write fail after validation and
// marshalling have both succeeded -- exactly the ENOSPC / read-only-filesystem
// shape. The old handoff must still be readable afterwards.
func TestReconstruct_WriteFailureLeavesExistingFileIntact(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "results")
	require.NoError(t, os.Mkdir(dir, 0755))
	path := filepath.Join(dir, "task-recon06.handoff.yaml")
	const existing = "task: task-recon06\nstatus: error\nsummary: the earlier handoff\n"
	require.NoError(t, os.WriteFile(path, []byte(existing), 0644))

	require.NoError(t, os.Chmod(dir, 0555))
	t.Cleanup(func() { _ = os.Chmod(dir, 0755) })
	// Root, and filesystems that do not enforce directory write permission
	// (notably Windows), cannot create this state -- skip rather than pass
	// vacuously.
	probe := filepath.Join(dir, "probe.tmp")
	if probeErr := os.WriteFile(probe, []byte("x"), 0644); probeErr == nil {
		_ = os.Remove(probe)
		t.Skip("cannot make a directory unwritable to this process (root, or a filesystem without POSIX write denial)")
	}

	require.Error(t, Reconstruct(path, errorContract("task-recon06")))

	body, err := os.ReadFile(path)
	require.NoError(t, err, "the existing handoff must survive a failed write")
	assert.Equal(t, existing, string(body))
}

// assertNoTempLitter fails when a failed reconstruction has left a temporary
// file behind in dir.
func assertNoTempLitter(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotContains(t, e.Name(), ".tmp-", "failed reconstruction left a temporary file behind: %s", e.Name())
	}
}

// Reconstruct must refuse to write a contract that would fail validation, so a
// future miswrite cannot persist an invalid document.
func TestReconstruct_RejectsInvalidContract(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "task-recon04.handoff.yaml")

	// status: complete requires a result -- Validate rejects this.
	invalid := &ResultContract{
		Task:    "task-recon04",
		Command: "execute",
		Target:  "tc-11111111",
		Adapter: "x",
		Mode:    "sync",
		Status:  "complete",
	}
	err := Reconstruct(path, invalid)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "validation failed")
	assert.NoFileExists(t, path, "an invalid contract must not be written")
	assertNoTempLitter(t, root)
}
