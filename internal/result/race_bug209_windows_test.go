//go:build windows

package result

// BUG-209 round 5 (REV-275), Windows: the verified directory is rechecked before each
// path-based use, guaranteed metadata fails closed, the read-only attribute is kept
// through the retry wait, and an unexpected open error is surfaced.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// repointBoundDir makes the recheck see a different directory at the bound path, which
// is what a swap looks like to it. This does not depend on whether Windows lets a
// directory be renamed while a handle on it is open.
func repointBoundDir(t *testing.T, b *boundDir) {
	t.Helper()
	b.canonicalProject = ""
	b.absPath = t.TempDir()
}

// CLAUDE-004, CODEX-001: each of the three uses refuses a changed directory.
func TestWindowsRecheck_RefusesPublishOpenAndDelete(t *testing.T) {
	assertChanged := func(t *testing.T, err error) {
		t.Helper()
		var rd *RefusedDirError
		require.True(t, errors.As(err, &rd), "got %v", err)
		assert.Equal(t, "could not be inspected: changed during the check", rd.Reason)
	}

	t.Run("publish", func(t *testing.T) {
		_, path := canonicalResults(t, "task-pub")
		seedValidHandoff(t, path, "task-pub")
		before, err := os.ReadFile(path)
		require.NoError(t, err)
		b, _, err := openResultsDirForPath(path)
		require.NoError(t, err)
		defer b.Close()
		repointBoundDir(t, b)

		assertChanged(t, publishStaged(b, filepath.Base(path), []byte("task: x\nstatus: pending\n"), true, false, nil))
		after, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, before, after, "the destination must be unchanged")
	})
	t.Run("open", func(t *testing.T) {
		_, path := canonicalResults(t, "task-open")
		seedValidHandoff(t, path, "task-open")
		b, _, err := openResultsDirForPath(path)
		require.NoError(t, err)
		defer b.Close()
		repointBoundDir(t, b)

		_, rerr := readLeaf(b, filepath.Base(path))
		assertChanged(t, rerr)
	})
	t.Run("delete", func(t *testing.T) {
		projectRoot, path := canonicalResults(t, "task-del")
		seedValidHandoff(t, path, "task-del")
		scan := OpenScan(projectRoot)
		defer scan.Close()
		require.Nil(t, scan.DirRefusal)
		repointBoundDir(t, scan.root)

		assertChanged(t, scan.RemoveEntry(filepath.Base(path)))
		assert.FileExists(t, path, "the entry must be left in place")
	})
}

// Section 4: a recheck failing during a scan makes the whole scan a refused directory,
// so nothing from it is believed. This needs a real rename of the open directory, which
// Windows may not allow. If it refuses, the swap cannot happen at all, so the test skips
// naming that reason; the three uses above still prove the refusal itself.
func TestWindowsScan_RecheckFailureRefusesTheDirectory(t *testing.T) {
	projectRoot, path := canonicalResults(t, "task-scan")
	seedValidHandoff(t, path, "task-scan")
	results := filepath.Dir(path)
	var renameErr error
	withRaceHook(t, func(stage string) {
		if stage == "dir-open-entry" && renameErr == nil {
			if renameErr = os.Rename(results, results+"-moved"); renameErr == nil {
				require.NoError(t, os.Mkdir(results, 0o755))
			}
		}
	})
	scan := OpenScan(projectRoot)
	defer scan.Close()
	if renameErr != nil {
		t.Skipf("Windows refused to rename the directory while it was open, so this swap cannot occur: %v", renameErr)
	}
	require.NotNil(t, scan.DirRefusal, "a directory changed during the scan must refuse it")
	assert.Empty(t, scan.Entries)
}

// CLAUDE-010, CODEX-005: every guaranteed item that cannot be inspected refuses the
// write, leaving the destination unchanged and no staged file behind.
func TestWindowsCapture_FailsClosed(t *testing.T) {
	for _, stage := range []string{"open", "info", "sd", "control", "owner", "group", "dacl"} {
		t.Run(stage, func(t *testing.T) {
			_, path := canonicalResults(t, "task-cap")
			seedValidHandoff(t, path, "task-cap")
			before, err := os.ReadFile(path)
			require.NoError(t, err)
			withPublishHook(t, func(s string, _ []byte) error {
				if s == "capture-"+stage {
					return assertInjected
				}
				return nil
			})

			uerr := Update(path, map[string]interface{}{"summary": "x"})
			var rh *RefusedHandoffError
			require.True(t, errors.As(uerr, &rh), "got %v", uerr)
			assert.Contains(t, rh.Reason, "could not be inspected: ")
			publishStagedHook = nil

			after, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, before, after, "the destination must be unchanged")
			leftovers, err := filepath.Glob(path + ".tmp-*")
			require.NoError(t, err)
			assert.Empty(t, leftovers, "a refused write must not leave a staged file")
		})
	}
}

// holdExclusively opens path with no sharing at all, which blocks both a replacement and
// another reader until the returned function runs.
func holdExclusively(t *testing.T, path string) func() {
	t.Helper()
	return holdSharing(t, path, 0)
}

// holdSharing opens path with the given sharing mode until the returned function runs.
func holdSharing(t *testing.T, path string, share uint32) func() {
	t.Helper()
	p, err := windows.UTF16PtrFromString(path)
	require.NoError(t, err)
	h, err := windows.CreateFile(p, windows.GENERIC_READ, share, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	require.NoError(t, err)
	released := false
	release := func() {
		if !released {
			released = true
			windows.CloseHandle(h)
		}
	}
	t.Cleanup(release)
	return release
}

// CLAUDE-013: a read-only destination stays read-only through the retry wait, not only
// after it. The "recheck" stage runs before each attempt clears the attribute, so from
// the second attempt on it shows the state during the wait.
func TestWindowsReadonly_RetainedThroughRetryWait(t *testing.T) {
	_, path := canonicalResults(t, "task-ro")
	seedValidHandoff(t, path, "task-ro")
	require.NoError(t, setFileReadonly(path, true))
	t.Cleanup(func() { _ = setFileReadonly(path, false) })
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	// A reader that shares reading only: the merge can still read, the replace cannot.
	holdSharing(t, path, windows.FILE_SHARE_READ)

	attempts := 0
	withPublishHook(t, func(stage string, _ []byte) error {
		if stage == "recheck" {
			attempts++
			ro, roErr := getFileReadonly(path)
			require.NoError(t, roErr)
			assert.True(t, ro, "attempt %d: the attribute must be set while waiting", attempts)
		}
		return nil
	})

	uerr := Update(path, map[string]interface{}{"summary": "x"})
	require.Error(t, uerr, "the held destination cannot be replaced")
	publishStagedHook = nil
	assert.Greater(t, attempts, 1, "the bounded retry must have run")

	ro, err := getFileReadonly(path)
	require.NoError(t, err)
	assert.True(t, ro, "the attribute must still be set after failing")
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after, "the destination must be unchanged")
}

// CLAUDE-011: an open error that outlasts the retry is surfaced, not treated as no result.
func TestWindowsScan_UnexpectedOpenErrorIsSurfaced(t *testing.T) {
	projectRoot, path := canonicalResults(t, "task-held")
	seedValidHandoff(t, path, "task-held")
	release := holdExclusively(t, path)
	defer release()
	// win-runner-1 lets a plain read succeed under an exclusive hold, so there is no
	// sharing violation there to surface, and the scan correctly returns the entry.
	if _, err := os.ReadFile(path); err == nil {
		t.Skip("this host does not enforce the exclusive hold, so no sharing violation can occur")
	}

	scan := OpenScan(projectRoot)
	defer scan.Close()
	require.Nil(t, scan.DirRefusal)
	require.Len(t, scan.Refusals, 1, "a sharing violation must be surfaced")
	assert.Contains(t, scan.Refusals[0].Err.Reason, "could not be inspected: ")
	assert.Empty(t, scan.Entries)
}

// CLAUDE-011: a transient hold shorter than the retry bound is invisible to a reader.
func TestWindowsRead_RetriesPastTransientHold(t *testing.T) {
	_, path := canonicalResults(t, "task-transient")
	seedValidHandoff(t, path, "task-transient")
	release := holdExclusively(t, path)
	withRaceHook(t, func(stage string) {
		if stage == "leaf-open" {
			go func() {
				// Released from inside the bound, on the first look at the file.
				release()
			}()
		}
	})

	rc, err := Read(path)
	require.NoError(t, err)
	assert.Equal(t, "task-transient", rc.Task)
}

// AC13: the staged file over an existing handoff is protected owner-only from the first
// instant, so it never takes a wider ACL from its directory.
func TestWindowsStagedFile_ProtectedOwnerOnly(t *testing.T) {
	_, path := canonicalResults(t, "task-staged")
	seedValidHandoff(t, path, "task-staged")

	checked := false
	withPublishHook(t, func(stage string, _ []byte) error {
		if stage != "write" {
			return nil
		}
		matches, err := filepath.Glob(path + ".tmp-*")
		require.NoError(t, err)
		require.Len(t, matches, 1, "the staged file should exist at this point")
		_, _, protected := readSD(t, matches[0])
		assert.True(t, protected, "the staged file's DACL must be protected, so it inherits nothing")

		sd, err := windows.GetNamedSecurityInfo(matches[0], windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		require.NoError(t, err)
		dacl, _, err := sd.DACL()
		require.NoError(t, err)
		require.NotNil(t, dacl)
		assert.Equal(t, uint16(1), dacl.AceCount, "the staged file must have exactly one ACE, for its owner")
		checked = true
		return nil
	})

	require.NoError(t, Update(path, map[string]interface{}{"summary": "x"}))
	assert.True(t, checked, "the check must have run")
}

// Section 4 (CODEX-001): staging rechecks immediately before the create, so a directory
// changed after the earlier check gets no staged file.
func TestWindowsRecheck_RefusesStagingAfterALateChange(t *testing.T) {
	_, path := canonicalResults(t, "task-stage")
	seedValidHandoff(t, path, "task-stage")
	b, _, err := openResultsDirForPath(path)
	require.NoError(t, err)
	defer b.Close()
	other := t.TempDir()
	withRaceHook(t, func(stage string) {
		if stage == "dir-stage" {
			b.canonicalProject = ""
			b.absPath = other
		}
	})

	perr := publishStaged(b, filepath.Base(path), []byte("task: x\nstatus: pending\n"), true, false, nil)
	var rd *RefusedDirError
	require.True(t, errors.As(perr, &rd), "got %v", perr)
	leftovers, gerr := filepath.Glob(path + ".tmp-*")
	require.NoError(t, gerr)
	assert.Empty(t, leftovers, "nothing may be staged after the recheck refuses")
}

// Section 4 (CODEX-001): cleanup rechecks too. A staged path that may no longer reach the
// verified directory is left alone rather than removed through a swap.
func TestWindowsRecheck_RefusesCleanupAfterALateChange(t *testing.T) {
	_, path := canonicalResults(t, "task-clean")
	seedValidHandoff(t, path, "task-clean")
	b, _, err := openResultsDirForPath(path)
	require.NoError(t, err)
	defer b.Close()
	other := t.TempDir()
	withRaceHook(t, func(stage string) {
		if stage == "dir-cleanup" {
			b.canonicalProject = ""
			b.absPath = other
		}
	})
	withPublishHook(t, func(stage string, _ []byte) error {
		if stage == "write" {
			return assertInjected
		}
		return nil
	})

	perr := publishStaged(b, filepath.Base(path), []byte("task: x\nstatus: pending\n"), true, false, nil)
	require.Error(t, perr)
	leftovers, gerr := filepath.Glob(path + ".tmp-*")
	require.NoError(t, gerr)
	assert.Len(t, leftovers, 1, "the staged file must be left, not removed through a changed path")
}

// Section 4 (CLAUDE-002): the recheck verifies the physical path the operations use, not
// only the logical spelling. A junction is what Windows offers without a privilege.
func TestWindowsVerify_PhysicalPath(t *testing.T) {
	if testing.Short() {
		t.Skip("skips the cmd.exe mklink junction check under -short")
	}
	projectRoot, path := canonicalResults(t, "task-phys")
	seedValidHandoff(t, path, "task-phys")
	results := filepath.Dir(path)
	b, _, err := openResultsDirCanonical(projectRoot)
	require.NoError(t, err)
	defer b.Close()
	require.NoError(t, b.verify(), "control: the bound directory verifies")
	boundPath, boundPhysical := b.absPath, b.physicalAtBind

	mklink := func(link, target string) {
		out, cerr := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", link, target).CombinedOutput()
		require.NoError(t, cerr, string(out))
		t.Cleanup(func() { _ = exec.Command("cmd.exe", "/d", "/c", "rmdir", link).Run() })
	}

	// A physical path that is now a junction to somewhere outside.
	outsideJunction := filepath.Join(t.TempDir(), "to-outside")
	mklink(outsideJunction, t.TempDir())
	b.absPath = outsideJunction
	verr := b.verify()
	var rd *RefusedDirError
	require.True(t, errors.As(verr, &rd), "got %v", verr)
	assert.Equal(t, reasonOutside, rd.Reason)

	// The same directory reached through a new junction spelling: same identity, but no
	// longer the physical path that was verified.
	sameDirJunction := filepath.Join(filepath.Dir(results), "alias")
	mklink(sameDirJunction, results)
	b.absPath = sameDirJunction
	b.physicalAtBind = sameDirJunction
	verr = b.verify()
	require.True(t, errors.As(verr, &rd), "got %v", verr)
	assert.Equal(t, "could not be inspected: changed during the check", rd.Reason)

	b.absPath, b.physicalAtBind = boundPath, boundPhysical
	require.NoError(t, b.verify(), "control: restored, it verifies again")
}
