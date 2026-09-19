package reader

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
)

func skipIfNoSymlinks(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs elevation on Windows")
	}
	dir := t.TempDir()
	if err := os.Symlink(filepath.Join(dir, "target"), filepath.Join(dir, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

// rootScope mirrors internal/cli/scope.go's no-argument scope: ScanDir is built
// by joining the RAW project root, which is what makes the symlinked-root case
// reachable.
func rootScope(root string, recursive bool) *ScopeInfo {
	return &ScopeInfo{
		ScanDir:   layout.TestCasesDir(root),
		RelPath:   layout.Current().TestCases + "/",
		Recursive: recursive,
	}
}

// REV-158 CLAUDE-004: root-scope reset silently cleared nothing when the project
// root was reached through a symlink. ResetExecuteResults canonicalises the root
// but relativised the RAW ScanDir against it, so filepath.Rel produced a
// "../.."-prefixed string, nothing matched, and reset reported success having
// selected no owners. The old resetByScope walked ScanDir directly and was
// unaffected, so this was a silent regression with a zero exit code.
//
// This is reachable on macOS (/var and /tmp are symlinks), from a symlinked home
// or work directory, and from a Windows 8.3 short path.
func TestReset_RootScopeThroughSymlinkedRoot_SelectsOwners(t *testing.T) {
	skipIfNoSymlinks(t)

	realRoot := t.TempDir()
	writeSpec(t, realRoot, "tc-55555555", "gtms/test/cases/tc-55555555-x.md")
	writeHandoff(t, realRoot, "task-55555555", "tc-55555555")

	// A symlink that reaches the same project.
	linkParent := t.TempDir()
	linkedRoot := filepath.Join(linkParent, "project-link")
	require.NoError(t, os.Symlink(realRoot, linkedRoot))

	// Baseline: through the real path the owner is selected.
	direct, err := ResetExecuteResults(realRoot, rootScope(realRoot, true), "", true)
	require.NoError(t, err)
	require.Equal(t, 1, direct.TestCasesAffected, "baseline: the real path must select the owner")

	// Through the symlink it must select the same owner, not silently nothing.
	viaLink, err := ResetExecuteResults(linkedRoot, rootScope(linkedRoot, true), "", true)
	require.NoError(t, err)
	assert.Equal(t, 1, viaLink.TestCasesAffected,
		"root-scope reset through a symlinked project root selected nothing and reported success")
	assert.Equal(t, direct.AutomationRecordsCleared, viaLink.AutomationRecordsCleared)
}

// delete shares the same scope-relativisation path and must agree.
func TestDelete_RootScopeThroughSymlinkedRoot_SelectsOwners(t *testing.T) {
	skipIfNoSymlinks(t)

	realRoot := t.TempDir()
	writeSpec(t, realRoot, "tc-66666666", "gtms/test/cases/tc-66666666-x.md")

	linkParent := t.TempDir()
	linkedRoot := filepath.Join(linkParent, "project-link")
	require.NoError(t, os.Symlink(realRoot, linkedRoot))

	res, err := DeleteArtifacts(linkedRoot, rootScope(linkedRoot, true), "", false, false, true)
	require.NoError(t, err)
	assert.Equal(t, 1, res.TestCasesProcessed,
		"root-scope delete through a symlinked project root selected nothing")
}
