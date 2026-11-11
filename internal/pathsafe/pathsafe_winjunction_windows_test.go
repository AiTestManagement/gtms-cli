//go:build windows

package pathsafe

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BUG-205 (Windows only): a directory junction that points OUTSIDE the project
// must once again be recognised as a link and resolved to its target, so the
// pathsafe containment checks refuse it. These tests build REAL junctions with
// `cmd.exe /d /c mklink /J` -- a directory SYMLINK (mklink /D) needs Developer
// Mode and does not reproduce the bug. They exercise the go.mod-embedded
// `winsymlink=0` default: do NOT set GODEBUG in the environment when running
// them (an explicit GODEBUG=winsymlink=1 overrides the default and is
// unsupported for junction safety -- BUG-205 Decision 2).
//
// AC8: if a junction cannot be created the test FAILS, never skips -- mklink /J
// needs no administrator rights, so a failure is a genuine environment problem.

// skipIfShort skips a test that shells out (to cmd.exe) in -short smoke runs.
// These junction tests are the Windows analogues of the Linux-only symlink
// tests in pathsafe_test.go and run in the full suite (go test ./...), which is
// what the Windows CI runner uses (BUG-205 AC8).
func skipIfShort(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping shell-dependent junction test in -short mode")
	}
}

// mustJunction creates a directory junction link -> target with mklink /J and
// FAILS the test (never skips -- AC8) if creation fails. It registers a cleanup
// that removes the JUNCTION ITSELF with `cmd.exe /d /c rmdir` before any
// recursive t.TempDir removal, so a recursive delete can never follow the
// junction into the target's contents.
func mustJunction(t *testing.T, link, target string) {
	t.Helper()
	out, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", link, target).CombinedOutput()
	if err != nil {
		t.Fatalf("mklink /J %q %q must succeed (BUG-205 AC8, must not skip): %v\n%s", link, target, err, out)
	}
	t.Cleanup(func() {
		// Remove the junction as a link only; never recurse through it.
		_ = exec.Command("cmd.exe", "/d", "/c", "rmdir", link).Run()
	})
}

// AC1: under winsymlink=0, a junction is reported as a symlink by os.Lstat and
// filepath.EvalSymlinks resolves it to its target (not to the junction itself).
func TestWinJunction_ReportedAsSymlinkAndResolvesTarget(t *testing.T) {
	skipIfShort(t)
	root := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "file.txt"), []byte("x"), 0o644))

	link := filepath.Join(root, "jx")
	mustJunction(t, link, outside)

	info, err := os.Lstat(link)
	require.NoError(t, err)
	require.NotZero(t, info.Mode()&os.ModeSymlink,
		"under winsymlink=0 a directory junction must report as a symlink (got mode %v); "+
			"the go.mod godebug default is not in effect", info.Mode())

	resolved, err := filepath.EvalSymlinks(link)
	require.NoError(t, err)
	wantTarget, err := filepath.EvalSymlinks(outside)
	require.NoError(t, err)
	assert.Equal(t, wantTarget, resolved,
		"EvalSymlinks must resolve the junction to its target, not to the junction's own path")
}

// AC2: ResolveUnderRoot refuses an EXISTING path reached through a junction that
// points outside the root.
func TestWinJunction_ResolveUnderRoot_ExistingOutsideRefused(t *testing.T) {
	skipIfShort(t)
	root := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644))

	mustJunction(t, filepath.Join(root, "escape"), outside)

	_, _, err := ResolveUnderRoot(root, "escape/secret.txt")
	require.Error(t, err, "an existing file reached through an out-of-root junction must be refused")
	assert.True(t, IsPathSafetyError(err), "must be a *PathSafetyError, got: %v", err)
}

// AC3: ResolveUnderRoot refuses a NOT-YET-CREATED leaf beneath such a junction.
func TestWinJunction_ResolveUnderRoot_MissingLeafOutsideRefused(t *testing.T) {
	skipIfShort(t)
	root := t.TempDir()
	outside := t.TempDir() // left empty

	mustJunction(t, filepath.Join(root, "escape"), outside)

	_, _, err := ResolveUnderRoot(root, "escape/missing.txt")
	require.Error(t, err, "a missing leaf under an out-of-root junction must be refused")
	assert.True(t, IsPathSafetyError(err), "must be a *PathSafetyError, got: %v", err)
}

// AC6 (over-strict-fix guard, matches tc-f7b316e8 / tc-167059d1): a junction that
// resolves back INSIDE the root is a legitimate in-tree alias and must be
// accepted. The containment check must resolve root and candidate the same way,
// so a junction is never mistaken for an escape when it stays in-tree.
func TestWinJunction_ResolveUnderRoot_InTreeAliasAccepted(t *testing.T) {
	skipIfShort(t)
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	require.NoError(t, os.MkdirAll(realDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(realDir, "file.txt"), []byte("x"), 0o644))

	mustJunction(t, filepath.Join(root, "alias"), realDir)

	absPath, relPath, err := ResolveUnderRoot(root, "alias/file.txt")
	require.NoError(t, err, "an in-tree junction alias must be accepted")
	assert.True(t, filepath.IsAbs(absPath))
	assert.Equal(t, "real/file.txt", relPath,
		"an in-tree alias must resolve to its real in-root location")
}
