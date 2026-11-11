//go:build windows

package testcase

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
)

// BUG-205 (Windows only): the test case folder resolver must refuse a folder
// that is a directory junction pointing outside the project, exactly as v0.3.3
// did, and must still accept an in-tree junction alias. These are the Windows
// analogues of the Linux symlink matrix in testcase_symlink_test.go. They build
// REAL junctions with `cmd.exe /d /c mklink /J` (a directory symlink needs
// Developer Mode and does not reproduce the bug) and exercise the go.mod
// `winsymlink=0` default -- do NOT set GODEBUG in the environment.
//
// AC8: a junction that cannot be created FAILS the test, never skips.

// skipIfShortJunction skips shell-dependent junction tests in -short smoke runs.
func skipIfShortJunction(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping shell-dependent junction test in -short mode")
	}
}

// mustJunction creates link -> target with mklink /J and FAILS (never skips) if
// creation fails. It removes the junction itself with `cmd.exe /d /c rmdir`
// before any recursive t.TempDir cleanup, so a recursive delete can never follow
// the junction into the target's contents.
func mustJunction(t *testing.T, link, target string) {
	t.Helper()
	out, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", link, target).CombinedOutput()
	if err != nil {
		t.Fatalf("mklink /J %q %q must succeed (BUG-205 AC8, must not skip): %v\n%s", link, target, err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("cmd.exe", "/d", "/c", "rmdir", link).Run()
	})
}

// A test case folder that is a junction to a folder OUTSIDE the project must be a
// containment failure (a *pathsafe.PathSafetyError), never silently
// ErrFolderNotFound. (The Windows counterpart of
// TestResolveFolder_OutOfTreeSymlink_Rejected.)
func TestResolveFolder_OutOfTreeJunction_Rejected(t *testing.T) {
	skipIfShortJunction(t)
	root := t.TempDir()
	external := t.TempDir() // outside the project
	require(t, os.MkdirAll(filepath.Join(external, "tc-scope"), 0o755))
	cases := filepath.Join(root, "gtms", "test", "cases")
	require(t, os.MkdirAll(cases, 0o755))
	mustJunction(t, filepath.Join(cases, "escape"), external)

	_, err := ResolveFolder(root, "escape/tc-scope")
	assert.Error(t, err)
	assert.False(t, errors.Is(err, ErrFolderNotFound),
		"an out-of-tree junction folder must NOT be reported as not-found")
	assert.True(t, pathsafe.IsPathSafetyError(err),
		"an out-of-tree junction folder must be a PathSafetyError, got: %v", err)

	// The escaping junction used directly as the folder is also rejected.
	_, err = ResolveFolder(root, "escape")
	assert.True(t, pathsafe.IsPathSafetyError(err),
		"an out-of-tree folder junction must be rejected, got: %v", err)
}

// A junction resolving back INSIDE the cases tree is a legitimate alias and must
// be accepted (the over-strict-fix guard, matching spec tc-f7b316e8). The
// Windows counterpart of TestResolveFolder_InTreeAlias_Allowed.
func TestResolveFolder_InTreeJunctionAlias_Allowed(t *testing.T) {
	skipIfShortJunction(t)
	root := t.TempDir()
	cases := filepath.Join(root, "gtms", "test", "cases")
	realDir := filepath.Join(cases, "real")
	require(t, os.MkdirAll(realDir, 0o755))
	mustJunction(t, filepath.Join(cases, "alias"), realDir)

	_, err := ResolveFolder(root, "alias")
	assert.NoError(t, err, "an in-tree folder junction alias must resolve")
}
