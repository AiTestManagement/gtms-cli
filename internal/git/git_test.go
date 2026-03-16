package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func skipIfShort(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping: requires git")
	}
}

// initTempRepo creates a temporary directory, initialises a git repo,
// and returns the path. The caller should defer os.RemoveAll(path).
func initTempRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// git init
	cmd := exec.Command("git", "init", dir)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git init failed: %s", string(out))

	// Configure user for commits
	cmd = exec.Command("git", "-C", dir, "config", "user.email", "test@test.com")
	require.NoError(t, cmd.Run())
	cmd = exec.Command("git", "-C", dir, "config", "user.name", "Test")
	require.NoError(t, cmd.Run())

	// Create an initial commit so HEAD exists
	dummyFile := filepath.Join(dir, "README.md")
	require.NoError(t, os.WriteFile(dummyFile, []byte("# Test\n"), 0644))
	cmd = exec.Command("git", "-C", dir, "add", ".")
	require.NoError(t, cmd.Run())
	cmd = exec.Command("git", "-C", dir, "commit", "-m", "initial commit")
	out, err = cmd.CombinedOutput()
	require.NoError(t, err, "git commit failed: %s", string(out))

	return dir
}

func TestIsRepo_True(t *testing.T) {
	skipIfShort(t)
	dir := initTempRepo(t)
	assert.True(t, IsRepo(context.Background(), dir))
}

func TestIsRepo_False(t *testing.T) {
	skipIfShort(t)
	dir := t.TempDir()
	assert.False(t, IsRepo(context.Background(), dir))
}

func TestProjectRoot(t *testing.T) {
	skipIfShort(t)
	dir := initTempRepo(t)

	// Create a subdirectory
	sub := filepath.Join(dir, "sub", "deep")
	require.NoError(t, os.MkdirAll(sub, 0755))

	root, err := ProjectRoot(context.Background(), sub)
	require.NoError(t, err)

	// On Windows, t.TempDir() may return a short (8.3) path while git
	// returns the long path. Resolve both via EvalSymlinks for comparison.
	expected, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	actual, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	assert.Equal(t, filepath.Clean(expected), filepath.Clean(actual))
}

func TestProjectRoot_NotRepo(t *testing.T) {
	skipIfShort(t)
	dir := t.TempDir()
	_, err := ProjectRoot(context.Background(), dir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not a git repository")
}

func TestCurrentBranch(t *testing.T) {
	skipIfShort(t)
	dir := initTempRepo(t)

	branch, err := CurrentBranch(context.Background(), dir)
	require.NoError(t, err)
	// Default branch could be "main" or "master" depending on git config
	assert.NotEmpty(t, branch)
}

func TestFileExists_Tracked(t *testing.T) {
	skipIfShort(t)
	dir := initTempRepo(t)
	assert.True(t, FileExists(context.Background(), dir, "README.md"))
}

func TestFileExists_Untracked(t *testing.T) {
	skipIfShort(t)
	dir := initTempRepo(t)

	// Create an untracked file
	require.NoError(t, os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("x"), 0644))
	assert.False(t, FileExists(context.Background(), dir, "untracked.txt"))
}

func TestFileExists_Missing(t *testing.T) {
	skipIfShort(t)
	dir := initTempRepo(t)
	assert.False(t, FileExists(context.Background(), dir, "nonexistent.txt"))
}

func TestListFiles(t *testing.T) {
	skipIfShort(t)
	dir := initTempRepo(t)

	files, err := ListFiles(context.Background(), dir, "", "")
	require.NoError(t, err)
	assert.Contains(t, files, "README.md")
}

func TestCreateAndRemoveWorktree(t *testing.T) {
	skipIfShort(t)
	dir := initTempRepo(t)

	wtPath := filepath.Join(t.TempDir(), "worktree-test")
	err := CreateWorktree(context.Background(), dir, wtPath, "test-branch")
	require.NoError(t, err)

	// The worktree directory should exist
	_, err = os.Stat(wtPath)
	assert.NoError(t, err)

	// List worktrees should mention the new path
	out, err := ListWorktrees(context.Background(), dir)
	require.NoError(t, err)
	assert.Contains(t, out, "test-branch")

	// Remove worktree
	err = RemoveWorktree(context.Background(), dir, wtPath)
	assert.NoError(t, err)
}
