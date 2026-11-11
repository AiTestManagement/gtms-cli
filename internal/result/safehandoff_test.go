package result

// BUG-209 cross-platform Go tests for the shared safe-handoff core: failure
// injection at every pre-publication boundary (AC1), the old-or-new boundary
// invariant (AC2), and the Read refusal/accept matrix (section 8 / AC6). The
// platform-specific metadata and no-hang tests live in the _unix/_windows files.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// assertInjected is the sentinel the failure-injection hook returns.
var assertInjected = errors.New("injected failure")

// symlinkSupported reports whether this process can create a symlink here
// (Windows without the privilege cannot). Used to skip the symlink forms, which
// are also covered by Pester junction cases on Windows.
func symlinkSupported(t *testing.T) bool {
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, "t")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		return false
	}
	return os.Symlink(target, filepath.Join(dir, "l")) == nil
}

// canonicalResults returns a project root whose .gtms/results directory exists,
// plus the handoff path for task. It exercises the real canonical layout so the
// containment + directory diagnostics are on the tested path.
func canonicalResults(t *testing.T, task string) (projectRoot, path string) {
	t.Helper()
	projectRoot = t.TempDir()
	dir := filepath.Join(projectRoot, ".gtms", "results")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	return projectRoot, filepath.Join(dir, task+".handoff.yaml")
}

func seedValidHandoff(t *testing.T, path, task string) {
	t.Helper()
	rc := &ResultContract{Task: task, Command: "execute", Target: "tc-11111111", Adapter: "a", Mode: "sync", Status: "error"}
	require.NoError(t, Reconstruct(path, rc))
}

// withPublishHook installs the failure-injection seam for one test.
func withPublishHook(t *testing.T, fn func(stage string, data []byte) error) {
	t.Helper()
	publishStagedHook = fn
	t.Cleanup(func() { publishStagedHook = nil })
}

// AC1 + AC2: a failure injected at each pre-publication boundary leaves the old
// file byte-identical, removes the staged file, and returns an error.
func TestSafeHandoff_InjectedFailureLeavesOldFileIntact(t *testing.T) {
	for _, stage := range []string{"write", "meta", "sync", "rename"} {
		t.Run(stage, func(t *testing.T) {
			projectRoot, path := canonicalResults(t, "task-inj"+stage)
			seedValidHandoff(t, path, "task-inj"+stage)
			before, err := os.ReadFile(path)
			require.NoError(t, err)

			withPublishHook(t, func(s string, _ []byte) error {
				if s == stage {
					return assertInjected
				}
				return nil
			})

			upErr := Update(path, map[string]interface{}{"summary": "new text"})
			require.Error(t, upErr, "injected failure at %q must surface", stage)

			after, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, before, after, "the old handoff must be byte-identical after a failed %q", stage)

			// No staged litter.
			entries, err := os.ReadDir(filepath.Join(projectRoot, ".gtms", "results"))
			require.NoError(t, err)
			for _, e := range entries {
				assert.NotContains(t, e.Name(), ".tmp-", "a failed write left a staged file: %s", e.Name())
			}
		})
	}
}

// AC6 / section 8: Read refuses an unsafe leaf and accepts a regular one.
func TestSafeHandoff_ReadMatrix(t *testing.T) {
	t.Run("regular leaf is read", func(t *testing.T) {
		_, path := canonicalResults(t, "task-rok")
		seedValidHandoff(t, path, "task-rok")
		rc, err := Read(path)
		require.NoError(t, err)
		assert.Equal(t, "error", rc.Status)
	})

	t.Run("absent is not found", func(t *testing.T) {
		_, path := canonicalResults(t, "task-rabsent")
		_, err := Read(path)
		require.Error(t, err)
		assert.False(t, IsRefusal(err), "absent is a not-found read error, not a refusal")
	})

	t.Run("directory leaf is refused", func(t *testing.T) {
		_, path := canonicalResults(t, "task-rdir")
		require.NoError(t, os.Mkdir(path, 0o755))
		_, err := Read(path)
		require.Error(t, err)
		assert.True(t, IsRefusal(err))
		assert.Contains(t, err.Error(), reasonNotRegular)
	})
}

// AC4: repeated GTMS-owned replacements with concurrent readers -- every read
// sees a COMPLETE old or new document, never a partial/empty/torn one, and no read
// returns an error: a transient open race on Windows is retried inside the shared read
// path, so a caller never sees it (CLAUDE-011). Runs on Windows and Linux.
func TestSafeHandoff_ConcurrentReadsSeeCompleteDocs(t *testing.T) {
	_, path := canonicalResults(t, "task-conc")
	seedValidHandoff(t, path, "task-conc")

	var wg sync.WaitGroup
	stop := make(chan struct{})
	var mu sync.Mutex
	var completeReads int
	var readErr error

	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				rc, err := Read(path)
				if err != nil {
					mu.Lock()
					readErr = err
					mu.Unlock()
					return
				}
				if rc.Task != "task-conc" || rc.Command != "execute" {
					mu.Lock()
					readErr = fmt.Errorf("torn read: task=%q command=%q", rc.Task, rc.Command)
					mu.Unlock()
					return
				}
				mu.Lock()
				completeReads++
				mu.Unlock()
			}
		}()
	}

	for i := 0; i < 300; i++ {
		require.NoError(t, Update(path, map[string]interface{}{"summary": fmt.Sprintf("rev-%d", i)}))
	}
	close(stop)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	require.NoError(t, readErr, "a concurrent reader saw a torn/partial document or a spurious refusal")
	assert.Greater(t, completeReads, 0, "readers must have observed at least one complete document")
}

// A results directory resolving outside the project is refused with the exact
// section-10 directory diagnostic (symlink form; the junction form is Pester).
func TestSafeHandoff_OutsideResultsDirRefused(t *testing.T) {
	if !symlinkSupported(t) {
		t.Skip("symlinks not creatable here (privilege); junction form is covered by Pester")
	}
	projectRoot := t.TempDir()
	outside := t.TempDir()
	gtmsDir := filepath.Join(projectRoot, ".gtms")
	require.NoError(t, os.MkdirAll(gtmsDir, 0o755))
	link := filepath.Join(gtmsDir, "results")
	require.NoError(t, os.Symlink(outside, link))

	path := filepath.Join(projectRoot, ".gtms", "results", "task-out.handoff.yaml")
	_, err := Read(path)
	require.Error(t, err)
	var rd *RefusedDirError
	require.ErrorAs(t, err, &rd)
	assert.Contains(t, err.Error(), "refused results directory .gtms/results: resolves outside the project")
	assert.Contains(t, err.Error(), dirRemediation)
}
