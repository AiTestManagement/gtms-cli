package result

// BUG-209 round 5 (REV-275): deterministic checks that hold on every platform. They
// use a seam or a fixed file layout, never timing.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withRaceHook(t *testing.T, fn func(stage string)) {
	t.Helper()
	raceHookFn = fn
	t.Cleanup(func() { raceHookFn = nil })
}

// CLAUDE-002: a directory at a handoff path is a refused entry, not something to skip.
func TestScan_DirectoryAtHandoffPathRefused(t *testing.T) {
	projectRoot, path := canonicalResults(t, "task-dirleaf")
	require.NoError(t, os.Mkdir(path, 0o755))
	plain := filepath.Join(filepath.Dir(path), "not-a-handoff-dir")
	require.NoError(t, os.Mkdir(plain, 0o755))

	scan := OpenScan(projectRoot)
	defer scan.Close()
	require.Nil(t, scan.DirRefusal)
	require.Len(t, scan.Refusals, 1, "only the directory with the handoff suffix is an entry")
	assert.Equal(t, "refused handoff .gtms/results/task-dirleaf.handoff.yaml: is not a regular file", scan.Refusals[0].Err.FirstLine())
}

// CODEX-008: only the exact generated form counts as a staged leftover.
func TestIsStagedNameFor(t *testing.T) {
	const base = "task-a.handoff.yaml"
	generated, err := stagedName(base)
	require.NoError(t, err)
	assert.True(t, isStagedNameFor(base, generated), "every generated name must match")
	for _, name := range []string{
		base + ".tmp-not-a-stage",
		base + ".tmp-0123456789abcde",   // one short
		base + ".tmp-0123456789abcdef0", // one long
		base + ".tmp-0123456789ABCDEF",  // upper case is never generated
		base + ".tmp-",
		"task-b.handoff.yaml.tmp-0123456789abcdef",
		base,
	} {
		assert.False(t, isStagedNameFor(base, name), name)
	}
}

// CODEX-008: Remove takes the handoff and its real leftover, and keeps everything else.
func TestScan_RemoveKeepsNonGeneratedNames(t *testing.T) {
	projectRoot, path := canonicalResults(t, "task-keep")
	seedValidHandoff(t, path, "task-keep")
	real := path + ".tmp-0123456789abcdef"
	keptFile := path + ".tmp-not-a-stage"
	keptDir := path + ".tmp-fedcba9876543210"
	require.NoError(t, os.WriteFile(real, []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(keptFile, []byte("x"), 0o644))
	require.NoError(t, os.Mkdir(keptDir, 0o755))

	scan := OpenScan(projectRoot)
	defer scan.Close()
	refused, err := scan.Remove(filepath.Base(path))
	require.NoError(t, err)

	assert.NoFileExists(t, path)
	assert.NoFileExists(t, real)
	assert.FileExists(t, keptFile, "a file that merely shares the prefix must survive")
	assert.DirExists(t, keptDir, "a directory with a staged-looking name must survive")
	require.Len(t, refused, 1, "and it is named, because it has the generated form (AC35)")
}

// RemoveEntry refuses an entry that became unsafe after the scan read it.
func TestScan_RemoveEntryRefusesEntryChangedSinceScan(t *testing.T) {
	projectRoot, path := canonicalResults(t, "task-changed")
	seedValidHandoff(t, path, "task-changed")
	scan := OpenScan(projectRoot)
	defer scan.Close()
	require.Len(t, scan.Entries, 1)

	require.NoError(t, os.Link(path, filepath.Join(t.TempDir(), "other-link")))

	err := scan.RemoveEntry(filepath.Base(path))
	require.Error(t, err)
	assert.True(t, IsRefusal(err), "got %v", err)
	assert.FileExists(t, path)
}

// CLAUDE-003: a directory replaced between opening and verifying is refused. This
// drives the check directly, so that it holds whether or not the platform lets a
// directory be renamed while a handle on it is open.
func TestBoundDir_VerifyRefusesDifferentDirectory(t *testing.T) {
	projectRoot, _ := canonicalResults(t, "task-bind")
	b, absent, err := openResultsDirCanonical(projectRoot)
	require.NoError(t, err)
	require.False(t, absent)
	defer b.Close()
	require.NoError(t, b.verify(), "control: the directory it was bound to verifies")

	b.canonicalProject = ""
	b.absPath = t.TempDir()
	verr := b.verify()
	require.Error(t, verr)
	var rd *RefusedDirError
	require.True(t, errors.As(verr, &rd))
	assert.Equal(t, "could not be inspected: changed during the check", rd.Reason)
}

// CLAUDE-005: the file replaced is the file merged. A file swapped in after the read
// is never overwritten by bytes merged from the old one: the merge runs again against
// it (REV-277 CLAUDE-004).
func TestUpdate_FileSwappedAfterReadIsReMerged(t *testing.T) {
	_, path := canonicalResults(t, "task-after-read")
	seedValidHandoff(t, path, "task-after-read")
	const swappedIn = "task: task-after-read\nstatus: error\nmarker: kept-from-the-swapped-file\n"

	swapped := false
	withRaceHook(t, func(stage string) {
		if stage == "after-read" && !swapped {
			swapped = true
			require.NoError(t, os.Remove(path))
			require.NoError(t, os.WriteFile(path, []byte(swappedIn), 0o644))
		}
	})
	require.NoError(t, Update(path, map[string]interface{}{"summary": "merged"}))
	raceHookFn = nil

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(got), "kept-from-the-swapped-file", "the merge must be against the swapped-in file")
	assert.Contains(t, string(got), "summary: merged")
}

// CLAUDE-005: a file appearing at an absent destination is not silently replaced.
func TestCreateAndReconstruct_DestinationAppearingIsRefused(t *testing.T) {
	const appeared = "appeared during the write\n"
	projectRoot, path := canonicalResults(t, "task-appear")
	withPublishHook(t, func(stage string, _ []byte) error {
		if stage == "recheck" {
			if _, err := os.Lstat(path); os.IsNotExist(err) {
				require.NoError(t, os.WriteFile(path, []byte(appeared), 0o644))
			}
		}
		return nil
	})

	rc := &ResultContract{Task: "task-appear", Command: "execute", Target: "tc-11111111", Adapter: "a", Mode: "sync", Status: "error"}
	_, err := Create(projectRoot, rc)
	require.Error(t, err)
	assert.True(t, IsRefusal(err), "got %v", err)
	got, rerr := os.ReadFile(path)
	require.NoError(t, rerr)
	assert.Equal(t, appeared, string(got))

	require.NoError(t, os.Remove(path))
	err = Reconstruct(path, rc)
	require.Error(t, err)
	assert.True(t, IsRefusal(err), "got %v", err)
	got, rerr = os.ReadFile(path)
	require.NoError(t, rerr)
	assert.Equal(t, appeared, string(got))
}
