//go:build !windows

package result

// BUG-209 Unix Go tests: permission-bit and uid/gid preservation across Update
// and Reconstruct (AC10), the mode-000 rules (AC11), and the FIFO/symlink/hard-
// link refusals including a killable no-hang proof for a FIFO (AC6). These run on
// the Linux VPS, where Go green on Windows is not enough (Linux-only guards skip).

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func statPerm(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Lstat(path)
	require.NoError(t, err)
	return info.Mode().Perm()
}

// AC10: Update and Reconstruct preserve permission bits, including read-only ones.
func TestSafeHandoff_UnixModesSurvive(t *testing.T) {
	for _, mode := range []os.FileMode{0o600, 0o640, 0o444, 0o400} {
		t.Run(mode.String(), func(t *testing.T) {
			_, path := canonicalResults(t, "task-mode")
			seedValidHandoff(t, path, "task-mode")
			require.NoError(t, os.Chmod(path, mode))

			// Reconstruct preserves the mode exactly (not 000).
			require.NoError(t, Reconstruct(path, &ResultContract{
				Task: "task-mode", Command: "execute", Target: "tc-11111111",
				Adapter: "a", Mode: "sync", Status: "error",
			}))
			assert.Equal(t, mode, statPerm(t, path), "Reconstruct must keep mode %v", mode)

			// Update preserves the mode exactly (the file is readable at 0400+).
			require.NoError(t, Update(path, map[string]interface{}{"summary": "x"}))
			assert.Equal(t, mode, statPerm(t, path), "Update must keep mode %v", mode)
		})
	}
}

// AC11: mode 000 -- Update fails with the destination unchanged; Reconstruct
// produces 0600.
func TestSafeHandoff_Mode000(t *testing.T) {
	t.Run("update fails, destination unchanged", func(t *testing.T) {
		_, path := canonicalResults(t, "task-z-upd")
		seedValidHandoff(t, path, "task-z-upd")
		before, err := os.ReadFile(path)
		require.NoError(t, err)
		require.NoError(t, os.Chmod(path, 0o000))

		err = Update(path, map[string]interface{}{"summary": "x"})
		require.Error(t, err, "Update must fail on a mode-000 handoff")

		require.NoError(t, os.Chmod(path, 0o600))
		after, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, before, after, "the mode-000 handoff must be unchanged after a failed Update")
	})

	t.Run("reconstruct yields 0600", func(t *testing.T) {
		_, path := canonicalResults(t, "task-z-rec")
		seedValidHandoff(t, path, "task-z-rec")
		require.NoError(t, os.Chmod(path, 0o000))

		require.NoError(t, Reconstruct(path, &ResultContract{
			Task: "task-z-rec", Command: "execute", Target: "tc-11111111",
			Adapter: "a", Mode: "sync", Status: "error",
		}))
		assert.Equal(t, os.FileMode(0o600), statPerm(t, path))
		rc, err := Read(path)
		require.NoError(t, err)
		assert.Equal(t, "error", rc.Status)
	})
}

// AC5/AC6: a symlink or hard-link leaf is refused by every writer and by Read.
func TestSafeHandoff_UnsafeLeavesRefused(t *testing.T) {
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "o.yaml")
	require.NoError(t, os.WriteFile(outsideFile, []byte("task: o\nstatus: error\n"), 0o644))

	t.Run("symlink", func(t *testing.T) {
		_, path := canonicalResults(t, "task-sym")
		require.NoError(t, os.Symlink(outsideFile, path))
		assertRefused(t, path, reasonIsLink)
	})

	t.Run("hard link", func(t *testing.T) {
		projectRoot, path := canonicalResults(t, "task-hl")
		// Hard link must be on the same filesystem; make the target in-project.
		target := filepath.Join(projectRoot, "target.yaml")
		require.NoError(t, os.WriteFile(target, []byte("task: t\nstatus: error\n"), 0o644))
		require.NoError(t, os.Link(target, path))
		assertRefused(t, path, reasonHardLinked)
	})
}

func assertRefused(t *testing.T, path, reason string) {
	t.Helper()
	_, readErr := Read(path)
	require.Error(t, readErr)
	assert.True(t, IsRefusal(readErr), "Read must refuse")
	assert.Contains(t, readErr.Error(), reason)

	upErr := Update(path, map[string]interface{}{"summary": "x"})
	require.Error(t, upErr)
	assert.True(t, IsRefusal(upErr), "Update must refuse")

	recErr := Reconstruct(path, &ResultContract{
		Task: "t", Command: "execute", Target: "tc-11111111", Adapter: "a", Mode: "sync", Status: "error",
	})
	require.Error(t, recErr)
	assert.True(t, IsRefusal(recErr), "Reconstruct must refuse")
}

// AC6: a FIFO at the handoff path is refused WITHOUT blocking. The classify step
// is Lstat-only (no open), so the operation must return promptly; a watchdog
// fails the test rather than letting the suite hang on a regression.
func TestSafeHandoff_FifoNeverHangs(t *testing.T) {
	_, path := canonicalResults(t, "task-fifo")
	if err := syscall.Mkfifo(path, 0o644); err != nil {
		t.Skipf("cannot create a FIFO here: %v", err)
	}

	for name, op := range map[string]func() error{
		"read":        func() error { _, e := Read(path); return e },
		"update":      func() error { return Update(path, map[string]interface{}{"summary": "x"}) },
		"reconstruct": func() error { return Reconstruct(path, &ResultContract{Task: "t", Command: "execute", Target: "tc-11111111", Adapter: "a", Mode: "sync", Status: "error"}) },
	} {
		done := make(chan error, 1)
		go func() { done <- op() }()
		select {
		case err := <-done:
			require.Error(t, err, "%s on a FIFO must error", name)
			assert.True(t, IsRefusal(err), "%s on a FIFO must refuse, got %v", name, err)
			assert.Contains(t, err.Error(), reasonNotRegular)
		case <-time.After(10 * time.Second):
			t.Fatalf("%s on a FIFO blocked -- it must refuse without opening the FIFO", name)
		}
	}
}

// AC3: a durability failure AFTER publication (the directory fsync) returns an
// error even though the NEW file is already in place. Driven through the "dirsync"
// seam.
func TestSafeHandoff_PostPublicationSyncFailureSurfaces(t *testing.T) {
	_, path := canonicalResults(t, "task-dirsync")
	seedValidHandoff(t, path, "task-dirsync")

	withPublishHook(t, func(stage string, _ []byte) error {
		if stage == "dirsync" {
			return assertInjected
		}
		return nil
	})

	err := Update(path, map[string]interface{}{"summary": "published-then-sync-failed"})
	require.Error(t, err, "a post-publication sync failure must be surfaced")
	assert.Contains(t, err.Error(), "syncing results directory")

	// The NEW document is in place (publication already happened).
	publishStagedHook = nil
	rc, rErr := Read(path)
	require.NoError(t, rErr)
	assert.Equal(t, "published-then-sync-failed", rc.Summary, "the new file must be in place after a post-publication failure")
}

// AC9: a leaf swapped between inspection and the final re-check is refused, with
// the destination unchanged. The "recheck" seam performs the swap in-hook just
// before the identity re-check.
func TestSafeHandoff_LeafSwapBeforeRecheckRefused(t *testing.T) {
	projectRoot, path := canonicalResults(t, "task-swap")
	seedValidHandoff(t, path, "task-swap")
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	swapped := false
	withPublishHook(t, func(stage string, _ []byte) error {
		if stage == "recheck" && !swapped {
			swapped = true
			// Replace the leaf with a different inode (remove + recreate).
			_ = os.Remove(path)
			_ = os.WriteFile(path, []byte("task: task-swap\nstatus: error\nsummary: swapped-in\n"), 0o644)
		}
		return nil
	})

	// Update retries a safe replacement (section 6), so this uses Reconstruct, which
	// writes without reading and is refused.
	err = Reconstruct(path, &ResultContract{Task: "task-swap", Command: "execute", Target: "tc-11111111", Adapter: "a", Mode: "sync", Status: "error", Summary: "should-be-refused"})
	require.Error(t, err, "a leaf swapped before the re-check must be refused")
	assert.True(t, IsRefusal(err))

	publishStagedHook = nil
	// The swapped-in leaf is left as the swap put it; our staged write did not land.
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotEqual(t, before, after, "the swapped-in content is what remains")
	assert.Contains(t, string(after), "swapped-in", "the write must not have published over the swapped leaf")

	entries, err := os.ReadDir(filepath.Join(projectRoot, ".gtms", "results"))
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotContains(t, e.Name(), ".tmp-", "a refused swap left a staged file: %s", e.Name())
	}
}

// AC10 (uid/gid): when the captured uid/gid cannot be applied, the write is
// refused with the destination unchanged. There is no second user in CI, so the
// chown-failure branch is driven through the "chown" seam.
func TestSafeHandoff_UidGidApplyFailureRefuses(t *testing.T) {
	_, path := canonicalResults(t, "task-chown")
	seedValidHandoff(t, path, "task-chown")
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	withPublishHook(t, func(stage string, _ []byte) error {
		if stage == "chown" {
			return assertInjected
		}
		return nil
	})

	err = Update(path, map[string]interface{}{"summary": "x"})
	require.Error(t, err, "a uid/gid apply failure must refuse the write")
	assert.Contains(t, err.Error(), "preserving handoff ownership")

	publishStagedHook = nil
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after, "the destination must be unchanged after a refused uid/gid apply")
}

// AC10 (uid/gid): when uid/gid match the staging process, the write succeeds and
// keeps them; the differing-owner refusal needs a second user and is covered by
// the record's platform notes.
func TestSafeHandoff_UidGidPreservedWhenSame(t *testing.T) {
	_, path := canonicalResults(t, "task-own")
	seedValidHandoff(t, path, "task-own")
	before, err := os.Lstat(path)
	require.NoError(t, err)
	st := before.Sys().(*syscall.Stat_t)

	require.NoError(t, Update(path, map[string]interface{}{"summary": "x"}))

	after, err := os.Lstat(path)
	require.NoError(t, err)
	st2 := after.Sys().(*syscall.Stat_t)
	assert.Equal(t, st.Uid, st2.Uid)
	assert.Equal(t, st.Gid, st2.Gid)
}
