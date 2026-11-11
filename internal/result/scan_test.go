package result

// BUG-209 section 8 scanner tests. Hard links work on Windows, so the unsafe-leaf
// case runs on every platform; the outside-directory (symlink) case skips where
// symlinks need a privilege and is covered by Pester there.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScan_EntriesRefusalsAndLeftovers(t *testing.T) {
	projectRoot, h := canonicalResults(t, "task-aaaa1111")
	seedValidHandoff(t, h, "task-aaaa1111")

	// A leftover staged file must be ignored (never an entry, never a refusal).
	require.NoError(t, os.WriteFile(h+".tmp-0123456789abcdef", []byte("status: error\n"), 0o644))

	// A hard-linked handoff is an unsafe leaf -> a refusal, not an entry.
	target := filepath.Join(projectRoot, "target.yaml")
	require.NoError(t, os.WriteFile(target, []byte("task: t\nstatus: error\n"), 0o644))
	hl := filepath.Join(projectRoot, ".gtms", "results", "task-bbbb2222.handoff.yaml")
	require.NoError(t, os.Link(target, hl))

	scan := OpenScan(projectRoot)
	defer scan.Close()

	require.False(t, scan.Absent)
	require.Nil(t, scan.DirRefusal)
	require.Len(t, scan.Entries, 1, "only the valid single-link handoff is an entry")
	assert.Equal(t, "task-aaaa1111.handoff.yaml", scan.Entries[0].Base)
	require.Len(t, scan.Refusals, 1, "the hard-linked handoff is refused")
	assert.Contains(t, scan.Refusals[0].Err.Error(), reasonHardLinked)

	// Remove removes the handoff AND its leftover staged file through the handle.
	_, removeErr := scan.Remove("task-aaaa1111.handoff.yaml")
	require.NoError(t, removeErr)
	assert.NoFileExists(t, h)
	assert.NoFileExists(t, h+".tmp-0123456789abcdef")
}

func TestScan_AbsentIsSilent(t *testing.T) {
	projectRoot := t.TempDir() // no .gtms/results
	scan := OpenScan(projectRoot)
	defer scan.Close()
	assert.True(t, scan.Absent)
	assert.Nil(t, scan.DirRefusal)
	assert.Empty(t, scan.Entries)
	assert.Empty(t, scan.Refusals)
}

func TestScan_OutsideDirRefused(t *testing.T) {
	if !symlinkSupported(t) {
		t.Skip("symlinks not creatable here (privilege); junction form is covered by Pester")
	}
	projectRoot := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, ".gtms"), 0o755))
	require.NoError(t, os.Symlink(outside, filepath.Join(projectRoot, ".gtms", "results")))

	scan := OpenScan(projectRoot)
	defer scan.Close()
	require.NotNil(t, scan.DirRefusal, "an outside results directory must be refused even when empty")
	assert.Contains(t, scan.DirRefusal.Error(), "resolves outside the project")
	assert.Empty(t, scan.Entries)
}

// BUG-209 AC31: an unexpected failure listing the directory is refused, with its
// cause, and the scan holds no results. It is never shown as an empty directory.
func TestScan_ListFailureIsRefusedNotNoResults(t *testing.T) {
	projectRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, ".gtms", "results"), 0o755))
	InjectScanReadFailureForTest(errors.New("injected list failure"))
	t.Cleanup(func() { InjectScanReadFailureForTest(nil) })

	scan := OpenScan(projectRoot)
	defer scan.Close()
	require.NotNil(t, scan.DirRefusal, "a list failure must be refused")
	assert.Equal(t, "refused results directory .gtms/results: could not be inspected: injected list failure", scan.DirRefusal.FirstLine())
	assert.False(t, scan.Absent, "a list failure is not an absent directory")
	assert.Empty(t, scan.Entries)
}

// BUG-209 AC31: something that cannot be opened as a directory is refused. Using a
// regular file at the path needs no privilege, so this runs on every platform.
func TestScan_OpenFailureIsRefusedNotNoResults(t *testing.T) {
	projectRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, ".gtms"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(projectRoot, ".gtms", "results"), []byte("not a directory"), 0o644))

	scan := OpenScan(projectRoot)
	defer scan.Close()
	require.NotNil(t, scan.DirRefusal, "a results path that cannot be opened must be refused")
	assert.Contains(t, scan.DirRefusal.FirstLine(), "refused results directory .gtms/results: could not be inspected: ")
	assert.False(t, scan.Absent)
}
