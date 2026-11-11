//go:build !windows

package result

// BUG-209 round 5 (REV-275), Unix: operations stay on the verified directory's handle
// when its path is swapped, leaves are never followed, and ownership and durability
// failures are not hidden. Every swap happens at a fixed seam.

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// swapResultsDirForEmptyOne moves .gtms/results aside and puts a new empty directory
// at its path, returning both.
func swapResultsDirForEmptyOne(t *testing.T, projectRoot string) (moved, replacement string) {
	t.Helper()
	replacement = filepath.Join(projectRoot, ".gtms", "results")
	moved = filepath.Join(projectRoot, ".gtms", "results-moved")
	require.NoError(t, os.Rename(replacement, moved))
	require.NoError(t, os.Mkdir(replacement, 0o755))
	return moved, replacement
}

func entryNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// CLAUDE-003: a swap between opening and verifying is refused, naming what it found.
func TestBind_SwapToOutsideLinkRefusedAsOutside(t *testing.T) {
	projectRoot, _ := canonicalResults(t, "task-bind")
	outside := t.TempDir()
	results := filepath.Join(projectRoot, ".gtms", "results")
	withRaceHook(t, func(stage string) {
		if stage == "dir-bind" {
			require.NoError(t, os.Rename(results, results+"-moved"))
			require.NoError(t, os.Symlink(outside, results))
		}
	})

	scan := OpenScan(projectRoot)
	defer scan.Close()
	require.NotNil(t, scan.DirRefusal)
	assert.Equal(t, reasonOutside, scan.DirRefusal.Reason)
}

func TestBind_SwapToAnotherDirectoryRefused(t *testing.T) {
	projectRoot, _ := canonicalResults(t, "task-bind")
	withRaceHook(t, func(stage string) {
		if stage == "dir-bind" {
			swapResultsDirForEmptyOne(t, projectRoot)
		}
	})

	scan := OpenScan(projectRoot)
	defer scan.Close()
	require.NotNil(t, scan.DirRefusal)
	assert.Equal(t, "could not be inspected: changed during the check", scan.DirRefusal.Reason)
}

// Section 4: after a missing directory is created it is opened and verified. A swap to
// an outside link right after creation is refused, with nothing written through it.
func TestCreate_DirectorySwappedAfterCreationRefused(t *testing.T) {
	projectRoot := t.TempDir()
	outside := t.TempDir()
	results := filepath.Join(projectRoot, ".gtms", "results")
	withRaceHook(t, func(stage string) {
		if stage == "dir-created" {
			require.NoError(t, os.Remove(results))
			require.NoError(t, os.Symlink(outside, results))
		}
	})

	rc := &ResultContract{Task: "task-new", Command: "execute", Target: "tc-11111111", Adapter: "a", Mode: "sync", Status: "pending"}
	_, err := Create(projectRoot, rc)
	require.Error(t, err)
	assert.True(t, IsRefusal(err), "got %v", err)
	assert.Empty(t, entryNames(t, outside), "nothing may land outside")
}

// Section 4: publication and the directory sync stay on the verified directory, and
// nothing reaches what was swapped in at its path.
func TestPublish_StaysOnVerifiedDirectoryAfterPathSwap(t *testing.T) {
	projectRoot, path := canonicalResults(t, "task-pub")
	seedValidHandoff(t, path, "task-pub")
	var moved, replacement string
	withPublishHook(t, func(stage string, _ []byte) error {
		if stage == "write" {
			moved, replacement = swapResultsDirForEmptyOne(t, projectRoot)
		}
		return nil
	})

	require.NoError(t, Update(path, map[string]interface{}{"summary": "landed-on-the-handle"}))

	assert.Empty(t, entryNames(t, replacement), "nothing may reach the swapped-in directory")
	got, err := os.ReadFile(filepath.Join(moved, filepath.Base(path)))
	require.NoError(t, err)
	assert.Contains(t, string(got), "landed-on-the-handle")
}

// CODEX-007, CLAUDE-004: enumeration reads the verified directory, not its path.
func TestScan_EnumeratesVerifiedDirectoryAfterPathSwap(t *testing.T) {
	projectRoot, path := canonicalResults(t, "task-enum")
	seedValidHandoff(t, path, "task-enum")
	withRaceHook(t, func(stage string) {
		if stage == "dir-list" {
			swapResultsDirForEmptyOne(t, projectRoot)
		}
	})

	scan := OpenScan(projectRoot)
	defer scan.Close()
	require.Nil(t, scan.DirRefusal)
	require.Len(t, scan.Entries, 1, "the entry in the verified directory must be seen")
	assert.Equal(t, "task-enum", scan.Entries[0].RC.Task)
}

// CODEX-004: deletion goes through the verified handle, so it cannot reach a file with
// the same name in a directory swapped in at the path.
func TestScan_RemoveStaysOnVerifiedDirectoryAfterPathSwap(t *testing.T) {
	projectRoot, path := canonicalResults(t, "task-del")
	seedValidHandoff(t, path, "task-del")
	scan := OpenScan(projectRoot)
	defer scan.Close()

	moved, replacement := swapResultsDirForEmptyOne(t, projectRoot)
	decoy := filepath.Join(replacement, filepath.Base(path))
	require.NoError(t, os.WriteFile(decoy, []byte("decoy"), 0o644))

	_, rerr := scan.Remove(filepath.Base(path))
	require.NoError(t, rerr)
	assert.FileExists(t, decoy, "the file at the swapped-in path must survive")
	assert.NoFileExists(t, filepath.Join(moved, filepath.Base(path)))
}

// CODEX-002, CLAUDE-005: a leaf swapped after inspection is never followed, and a FIFO
// swapped in never blocks.
func TestRead_LeafSwappedBeforeOpen(t *testing.T) {
	cases := []struct {
		name   string
		swap   func(t *testing.T, path string)
		reason string
	}{
		{"in-root symlink", func(t *testing.T, path string) {
			target := path + ".target"
			require.NoError(t, os.Rename(path, target))
			require.NoError(t, os.Symlink(filepath.Base(target), path))
		}, reasonIsLink},
		{"hard link", func(t *testing.T, path string) {
			require.NoError(t, os.Link(path, path+".other"))
		}, reasonHardLinked},
		{"fifo", func(t *testing.T, path string) {
			require.NoError(t, os.Remove(path))
			require.NoError(t, syscall.Mkfifo(path, 0o644))
		}, reasonNotRegular},
		{"directory", func(t *testing.T, path string) {
			require.NoError(t, os.Remove(path))
			require.NoError(t, os.Mkdir(path, 0o755))
		}, reasonNotRegular},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, path := canonicalResults(t, "task-leaf")
			seedValidHandoff(t, path, "task-leaf")
			withRaceHook(t, func(stage string) {
				if stage == "leaf-open" {
					tc.swap(t, path)
				}
			})

			done := make(chan error, 1)
			go func() {
				_, err := Read(path)
				done <- err
			}()
			select {
			case err := <-done:
				var rh *RefusedHandoffError
				require.True(t, errors.As(err, &rh), "got %v", err)
				assert.Equal(t, tc.reason, rh.Reason)
			case <-time.After(10 * time.Second):
				t.Fatal("Read blocked on a leaf swapped in before the open")
			}
		})
	}
}

// AC13: at every moment the staged file over an existing handoff is owner-only, and the
// published file then has its final bits.
func TestStagedFile_NeverWiderThanFinal(t *testing.T) {
	_, path := canonicalResults(t, "task-staged")
	seedValidHandoff(t, path, "task-staged")
	require.NoError(t, os.Chmod(path, 0o640))

	checked := false
	withPublishHook(t, func(stage string, _ []byte) error {
		if stage != "write" {
			return nil
		}
		matches, err := filepath.Glob(path + ".tmp-*")
		require.NoError(t, err)
		require.Len(t, matches, 1, "the staged file should exist at this point")
		info, err := os.Lstat(matches[0])
		require.NoError(t, err)
		assert.Zero(t, info.Mode().Perm()&0o077, "the staged file gives group or other access: %o", info.Mode().Perm())
		checked = true
		return nil
	})

	require.NoError(t, Update(path, map[string]interface{}{"summary": "x"}))
	assert.True(t, checked, "the check must have run")
	assert.Equal(t, os.FileMode(0o640), statPerm(t, path))
}

// CLAUDE-009: the decision compares the staged file's owner, not the process's.
func TestStagedNeedsChown(t *testing.T) {
	assert.False(t, stagedNeedsChown(1, 2, 1, 2))
	assert.True(t, stagedNeedsChown(1, 2, 1, 3), "a different group must be applied")
	assert.True(t, stagedNeedsChown(1, 2, 9, 2), "a different owner must be applied")
}

// CLAUDE-009: in a setgid directory a new file takes the directory's group. A handoff
// in another of this user's groups must keep that group across replacement. This needs a
// user in two groups, which is not guaranteed, so it skips naming that reason.
func TestUpdate_GroupSurvivesInSetgidDirectory(t *testing.T) {
	groups, err := os.Getgroups()
	require.NoError(t, err)
	egid := os.Getegid()
	other := -1
	for _, g := range groups {
		if g != egid {
			other = g
			break
		}
	}
	if other == -1 {
		t.Skip("this user belongs to only one group, so the real setgid case cannot be set up")
	}

	_, path := canonicalResults(t, "task-group")
	seedValidHandoff(t, path, "task-group")
	dir := filepath.Dir(path)
	require.NoError(t, os.Chown(dir, -1, other))
	require.NoError(t, os.Chmod(dir, 0o755|os.ModeSetgid))
	// The old file keeps the process's group while new files take the directory's.
	require.NoError(t, os.Chown(path, -1, egid))

	require.NoError(t, Update(path, map[string]interface{}{"summary": "x"}))

	info, err := os.Lstat(path)
	require.NoError(t, err)
	assert.Equal(t, uint32(egid), info.Sys().(*syscall.Stat_t).Gid, "the handoff's group must survive")
}

// CLAUDE-008, CODEX-006: a failure syncing the directory is returned, with the new file
// in place. Closing the bound descriptor makes the real sync call fail.
func TestPublish_DirectorySyncFailureIsReturned(t *testing.T) {
	_, path := canonicalResults(t, "task-sync")
	seedValidHandoff(t, path, "task-sync")
	b, absent, err := openResultsDirForPath(path)
	require.NoError(t, err)
	require.False(t, absent)
	defer b.Root.Close()
	require.NoError(t, b.dirFile.Close())

	serr := publishStaged(b, filepath.Base(path), []byte("task: task-sync\nstatus: pending\nsummary: published\n"), true, false, nil)
	require.Error(t, serr, "a failed directory sync must not be reported as success")
	assert.Contains(t, serr.Error(), "syncing results directory")
	got, rerr := os.ReadFile(path)
	require.NoError(t, rerr)
	assert.Contains(t, string(got), "published", "the new file stays in place after the failure")
}

// CLAUDE-012: only a real escape is reported as outside.
func TestVerifyContainment_ReportsTheRealReason(t *testing.T) {
	projectRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projectRoot, ".gtms"), []byte("a file"), 0o644))
	err := VerifyResultsDirContainment(projectRoot)
	var rd *RefusedDirError
	require.True(t, errors.As(err, &rd), "got %v", err)
	assert.Contains(t, rd.Reason, "could not be inspected: ")

	linked := t.TempDir()
	require.NoError(t, os.Symlink(t.TempDir(), filepath.Join(linked, ".gtms")))
	err = VerifyResultsDirContainment(linked)
	require.True(t, errors.As(err, &rd), "got %v", err)
	assert.Equal(t, reasonOutside, rd.Reason)
}

// Section 10 (CLAUDE-005, CODEX-002): a real entry named with a newline and an escape is
// refused with an escaped first line, and removal still uses the raw name.
func TestScan_ControlCharactersInARealEntryName(t *testing.T) {
	projectRoot, path := canonicalResults(t, "task-plain")
	seedValidHandoff(t, path, "task-plain")
	name := "task-\x1b[31m\nforged.handoff.yaml"
	stray := filepath.Join(filepath.Dir(path), name)
	require.NoError(t, os.Symlink(filepath.Join(t.TempDir(), "nowhere"), stray))

	scan := OpenScan(projectRoot)
	defer scan.Close()
	require.Len(t, scan.Refusals, 1)
	assert.Equal(t, name, scan.Refusals[0].Base, "the raw name must be kept")
	first := scan.Refusals[0].Err.FirstLine()
	assert.NotContains(t, first, "\x1b")
	assert.NotContains(t, first, "\n")
	assert.Contains(t, first, "is a link")
}

// Section 4 (CLAUDE-002): retargeting a link to keep the logical path correct, while
// something else is put at the physical path, is refused. This is the exact sequence the
// review describes, run against the check itself.
func TestVerify_PhysicalPathSwapBehindAnInProjectLink(t *testing.T) {
	projectRoot := t.TempDir()
	gtmsDir := filepath.Join(projectRoot, ".gtms")
	real := filepath.Join(gtmsDir, "real")
	require.NoError(t, os.MkdirAll(real, 0o755))
	results := filepath.Join(gtmsDir, "results")
	require.NoError(t, os.Symlink("real", results))

	b, absent, err := openResultsDirCanonical(projectRoot)
	require.NoError(t, err)
	require.False(t, absent)
	defer b.Close()
	require.NoError(t, b.verify(), "control: an unchanged in-project link verifies")

	outside := t.TempDir()
	require.NoError(t, os.Rename(real, real+"2"))
	require.NoError(t, os.Symlink(outside, real))
	require.NoError(t, os.Remove(results))
	require.NoError(t, os.Symlink("real2", results))

	verr := b.verify()
	var rd *RefusedDirError
	require.True(t, errors.As(verr, &rd), "the logical path still reaches the right directory, so only the physical check can catch this: %v", verr)
	assert.Equal(t, reasonOutside, rd.Reason)
}

// AC35: a FIFO at .gtms/results is refused rather than opened, proved in a child process
// the parent can kill if it hangs.
func TestFifoResultsDir_RefusedWithoutBlocking(t *testing.T) {
	if testing.Short() {
		t.Skip("skips child-process FIFO checks under -short")
	}
	projectRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, ".gtms"), 0o755))
	require.NoError(t, syscall.Mkfifo(filepath.Join(projectRoot, ".gtms", "results"), 0o644))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0])
	cmd.Env = append(os.Environ(), "GTMS_CHILD_MODE=fifo-results-dir", "GTMS_CHILD_PATH="+projectRoot)
	err := cmd.Run()
	require.NoError(t, ctx.Err(), "the command blocked on a FIFO at .gtms/results")
	require.NoError(t, err, "the child must refuse the directory")
}

// CLAUDE-007: a metadata failure is a refusal on Unix, as it is on Windows.
func TestMetadataFailure_IsARefusal(t *testing.T) {
	_, path := canonicalResults(t, "task-meta")
	seedValidHandoff(t, path, "task-meta")
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	withPublishHook(t, func(stage string, _ []byte) error {
		if stage == "chown" {
			return assertInjected
		}
		return nil
	})

	uerr := Update(path, map[string]interface{}{"summary": "x"})
	require.Error(t, uerr)
	assert.True(t, IsRefusal(uerr), "got %v", uerr)
	assert.Contains(t, uerr.Error(), "could not be inspected: preserving handoff ownership")
	publishStagedHook = nil
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after, "the destination must be unchanged")
}
