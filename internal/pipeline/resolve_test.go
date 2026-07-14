package pipeline

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveArtefact_UsesStoredPathWhenValid(t *testing.T) {
	dir := t.TempDir()

	// Create a file at the stored path
	artefactDir := filepath.Join(dir, "test", "acceptance")
	require.NoError(t, os.MkdirAll(artefactDir, 0755))
	artefactPath := filepath.Join(artefactDir, "tc-abc1234-my-test.bats")
	require.NoError(t, os.WriteFile(artefactPath, []byte("test content"), 0644))

	relPath := "test/acceptance/tc-abc1234-my-test.bats"
	result, err := ResolveArtefact(dir, relPath, "tc-abc1234")

	require.NoError(t, err)
	assert.Equal(t, relPath, result)
}

// ENH-110: stored path exists on disk and its basename does NOT contain the
// TC ID. The resolver must trust the stored path (the basename check was dropped
// to support shared-file frameworks like Playwright grouped tests).
func TestResolveArtefact_StoredPathWins_BasenameHasNoTCID(t *testing.T) {
	dir := t.TempDir()

	// Create a file at the stored path whose basename is unrelated to the TC ID.
	// This simulates a shared-file artefact like tests/login.spec.ts.
	sharedDir := filepath.Join(dir, "tests")
	require.NoError(t, os.MkdirAll(sharedDir, 0755))
	sharedFile := filepath.Join(sharedDir, "login.spec.ts")
	require.NoError(t, os.WriteFile(sharedFile, []byte("shared test"), 0644))

	// Stored path points at the shared file — resolver must use it directly.
	result, err := ResolveArtefact(dir, "tests/login.spec.ts", "tc-abc1234")

	require.NoError(t, err)
	assert.Equal(t, "tests/login.spec.ts", result)
}

// ENH-110: stored path exists on disk AND a competing file with the TC ID in
// its basename also exists. The stored path must win (no glob runs).
func TestResolveArtefact_StoredPathWins_OverCompetingGlob(t *testing.T) {
	dir := t.TempDir()

	// Create a shared-file artefact at the stored path (basename has no TC ID).
	sharedDir := filepath.Join(dir, "tests", "shared")
	require.NoError(t, os.MkdirAll(sharedDir, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(sharedDir, "login.spec.ts"),
		[]byte("shared"), 0644))

	// Create a competing file whose name DOES contain the TC ID.
	otherDir := filepath.Join(dir, "tests", "other")
	require.NoError(t, os.MkdirAll(otherDir, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(otherDir, "tc-abc1234-other.spec.ts"),
		[]byte("other"), 0644))

	// Stored path points at the shared file — must win over the competing glob candidate.
	result, err := ResolveArtefact(dir, "tests/shared/login.spec.ts", "tc-abc1234")

	require.NoError(t, err)
	assert.Equal(t, "tests/shared/login.spec.ts", result)
}

func TestResolveArtefact_FindsArtefactAfterDirectoryRename(t *testing.T) {
	skipIfShort(t)

	dir := t.TempDir()

	// Create the file in a NEW location (simulating directory rename)
	newDir := filepath.Join(dir, "test", "acceptance", "new-folder")
	require.NoError(t, os.MkdirAll(newDir, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(newDir, "tc-abc1234-my-test.bats"),
		[]byte("test content"), 0644))

	// Stored path points to OLD location
	oldPath := "test/acceptance/old-folder/tc-abc1234-my-test.bats"
	result, err := ResolveArtefact(dir, oldPath, "tc-abc1234")

	require.NoError(t, err)
	assert.Equal(t, "test/acceptance/new-folder/tc-abc1234-my-test.bats", result)
}

func TestResolveArtefact_ErrorsOnZeroMatches_WithStoredPath(t *testing.T) {
	skipIfShort(t)

	dir := t.TempDir()

	// ENH-110: SPEC §2.5 error format includes stored path and "no glob match".
	_, err := ResolveArtefact(dir, "nonexistent/path.bats", "tc-abc1234")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "artefact for tc-abc1234 not found at nonexistent/path.bats and no glob match")
}

func TestResolveArtefact_ErrorsOnZeroMatches_NoStoredPath(t *testing.T) {
	skipIfShort(t)

	dir := t.TempDir()

	// ENH-110: when stored path is empty, a different message variant is used.
	_, err := ResolveArtefact(dir, "", "tc-abc1234")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "artefact for tc-abc1234 not found (no stored path and no glob match)")
}

func TestResolveArtefact_ErrorsOnAmbiguousMatch(t *testing.T) {
	skipIfShort(t)

	dir := t.TempDir()

	// Create two files matching the same TC ID in different directories
	dir1 := filepath.Join(dir, "test", "dir1")
	dir2 := filepath.Join(dir, "test", "dir2")
	require.NoError(t, os.MkdirAll(dir1, 0755))
	require.NoError(t, os.MkdirAll(dir2, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir1, "tc-abc1234-test.bats"), []byte("v1"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir2, "tc-abc1234-test.bats"), []byte("v2"), 0644))

	_, err := ResolveArtefact(dir, "nonexistent.bats", "tc-abc1234")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "multiple artefact files found")
}

func TestResolveArtefact_SkipsGitAndGtmsDirs(t *testing.T) {
	skipIfShort(t)

	dir := t.TempDir()

	// Put a matching file ONLY inside .git/ and .gtms/ — should not be found
	gitDir := filepath.Join(dir, ".git", "objects")
	gtmsDir := filepath.Join(dir, ".gtms", "tmp")
	require.NoError(t, os.MkdirAll(gitDir, 0755))
	require.NoError(t, os.MkdirAll(gtmsDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "tc-abc1234-data"), []byte("x"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(gtmsDir, "tc-abc1234-prompt.md"), []byte("x"), 0644))

	_, err := ResolveArtefact(dir, "", "tc-abc1234")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "artefact for tc-abc1234 not found")
}

func TestResolveArtefact_EmptyStoredPathTriggersSearch(t *testing.T) {
	skipIfShort(t)

	dir := t.TempDir()

	testDir := filepath.Join(dir, "test")
	require.NoError(t, os.MkdirAll(testDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(testDir, "tc-abc1234-test.bats"), []byte("content"), 0644))

	result, err := ResolveArtefact(dir, "", "tc-abc1234")

	require.NoError(t, err)
	assert.Equal(t, "test/tc-abc1234-test.bats", result)
}

// BUG-048: parent-dir exclusion must be root-anchored, not depth-insensitive.

func TestResolveArtefact_SkipsSentinelParent_Default(t *testing.T) {
	dir := t.TempDir()

	// Place a matching file inside the default parent dir ("gtms/test/cases/...").
	// The resolver must exclude the top-level "gtms/" directory.
	target := filepath.Join(dir, "gtms", "test", "cases", "foo")
	require.NoError(t, os.MkdirAll(target, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(target, "tc-abc12345-test.bats"),
		[]byte("content"), 0644))

	_, err := ResolveArtefact(dir, "", "tc-abc12345")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "artefact for tc-abc12345 not found")
}

func TestResolveArtefact_SkipsSentinelParent_Renamed(t *testing.T) {
	dir := t.TempDir()

	// Simulate a renamed parent directory ("testing" instead of "gtms").
	orig := layout.Current()
	layout.InitFromParent("testing")
	t.Cleanup(func() {
		layout.InitFromParent(orig.Parent)
	})

	// Place a matching file inside the renamed parent dir.
	target := filepath.Join(dir, "testing", "test", "cases", "foo")
	require.NoError(t, os.MkdirAll(target, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(target, "tc-abc12345-test.bats"),
		[]byte("content"), 0644))

	_, err := ResolveArtefact(dir, "", "tc-abc12345")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "artefact for tc-abc12345 not found")
}

func TestResolveArtefact_FindsNestedGtmsSubdir(t *testing.T) {
	dir := t.TempDir()

	// Place a matching file inside a NESTED "gtms/" directory (not at root).
	// The resolver must NOT exclude this — only the top-level parent is skipped.
	nested := filepath.Join(dir, "docs", "gtms", "examples")
	require.NoError(t, os.MkdirAll(nested, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(nested, "tc-abc12345-test.bats"),
		[]byte("content"), 0644))

	result, err := ResolveArtefact(dir, "", "tc-abc12345")

	require.NoError(t, err)
	assert.Equal(t, "docs/gtms/examples/tc-abc12345-test.bats", result)
}

// BUG-057: path-safety tests for ResolveArtefact fast-path.

func TestResolveArtefact_RejectsAbsolutePathOutsideProject(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	// Create a real file outside the project root so os.Stat would succeed
	// if the containment check were missing.
	outsidePath := filepath.Join(outside, "evil.txt")
	require.NoError(t, os.WriteFile(outsidePath, []byte("bad"), 0644))

	_, err := ResolveArtefact(root, outsidePath, "tc-abc1234")

	require.Error(t, err)
	assert.True(t, pathsafe.IsPathSafetyError(err),
		"absolute path outside project should produce *pathsafe.PathSafetyError")
}

func TestResolveArtefact_RejectsRelativeTraversal(t *testing.T) {
	root := t.TempDir()

	// Create a file outside root that a relative ".." traversal would reach.
	parent := filepath.Dir(root)
	escapePath := filepath.Join(parent, "traversal-target.txt")
	require.NoError(t, os.WriteFile(escapePath, []byte("escaped"), 0644))
	t.Cleanup(func() { os.Remove(escapePath) })

	_, err := ResolveArtefact(root, "../traversal-target.txt", "tc-abc1234")

	require.Error(t, err)
	assert.True(t, pathsafe.IsPathSafetyError(err),
		"relative traversal outside project should produce *pathsafe.PathSafetyError")
}

func TestResolveArtefact_AbsoluteInsideRootNormalisedToRelative(t *testing.T) {
	root := t.TempDir()

	// Create a file inside the project root using an absolute path.
	sub := filepath.Join(root, "test", "acceptance")
	require.NoError(t, os.MkdirAll(sub, 0755))
	absFile := filepath.Join(sub, "tc-abc1234-test.bats")
	require.NoError(t, os.WriteFile(absFile, []byte("content"), 0644))

	result, err := ResolveArtefact(root, absFile, "tc-abc1234")

	require.NoError(t, err)
	assert.Equal(t, "test/acceptance/tc-abc1234-test.bats", result,
		"absolute path inside root should be normalised to relative slash form")
}

func TestResolveArtefact_FastPathAndGlobReturnSameShape(t *testing.T) {
	skipIfShort(t)

	root := t.TempDir()

	// Create a file that matches both the stored path and the glob.
	sub := filepath.Join(root, "test", "acceptance")
	require.NoError(t, os.MkdirAll(sub, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(sub, "tc-abc1234-test.bats"),
		[]byte("content"), 0644))

	relStored := "test/acceptance/tc-abc1234-test.bats"

	// Fast-path: stored path exists on disk.
	fastResult, err := ResolveArtefact(root, relStored, "tc-abc1234")
	require.NoError(t, err)

	// Glob fallback: force glob by providing a non-existent stored path.
	globResult, err := ResolveArtefact(root, "nonexistent/path.bats", "tc-abc1234")
	require.NoError(t, err)

	assert.Equal(t, fastResult, globResult,
		"fast-path and glob fallback must return the same relative slash-normalised shape")
}

func TestResolveArtefact_SymlinkInsideTargetingOutside(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on Windows")
	}
	skipIfShort(t)

	root := t.TempDir()
	outside := t.TempDir()

	// Create a real file outside the root.
	outsideFile := filepath.Join(outside, "secret.txt")
	require.NoError(t, os.WriteFile(outsideFile, []byte("secret"), 0644))

	// Create a symlink inside the root pointing outside.
	linkPath := filepath.Join(root, "evil-link.txt")
	require.NoError(t, os.Symlink(outsideFile, linkPath))

	_, err := ResolveArtefact(root, "evil-link.txt", "tc-sym1234")

	require.Error(t, err, "symlink targeting outside root must be rejected")
	assert.True(t, pathsafe.IsPathSafetyError(err),
		"symlink escape should produce *pathsafe.PathSafetyError")
}

func TestHashFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	require.NoError(t, os.WriteFile(path, []byte("hello world"), 0644))

	hash, err := HashFile(path)

	require.NoError(t, err)
	assert.Len(t, hash, 16, "hash should be 16 hex chars")
}

func TestHashFile_DifferentContentDifferentHash(t *testing.T) {
	dir := t.TempDir()

	path1 := filepath.Join(dir, "file1.txt")
	path2 := filepath.Join(dir, "file2.txt")
	require.NoError(t, os.WriteFile(path1, []byte("content A"), 0644))
	require.NoError(t, os.WriteFile(path2, []byte("content B"), 0644))

	hash1, err := HashFile(path1)
	require.NoError(t, err)

	hash2, err := HashFile(path2)
	require.NoError(t, err)

	assert.NotEqual(t, hash1, hash2)
}

func TestHashFile_SameContentSameHash(t *testing.T) {
	dir := t.TempDir()

	path1 := filepath.Join(dir, "file1.txt")
	path2 := filepath.Join(dir, "file2.txt")
	require.NoError(t, os.WriteFile(path1, []byte("same content"), 0644))
	require.NoError(t, os.WriteFile(path2, []byte("same content"), 0644))

	hash1, err := HashFile(path1)
	require.NoError(t, err)

	hash2, err := HashFile(path2)
	require.NoError(t, err)

	assert.Equal(t, hash1, hash2)
}

func TestHashFile_CRLFAndLFProduceSameHash(t *testing.T) {
	dir := t.TempDir()

	lfFile := filepath.Join(dir, "lf.txt")
	crlfFile := filepath.Join(dir, "crlf.txt")
	require.NoError(t, os.WriteFile(lfFile, []byte("line1\nline2\nline3\n"), 0644))
	require.NoError(t, os.WriteFile(crlfFile, []byte("line1\r\nline2\r\nline3\r\n"), 0644))

	lfHash, err := HashFile(lfFile)
	require.NoError(t, err)

	crlfHash, err := HashFile(crlfFile)
	require.NoError(t, err)

	assert.Equal(t, lfHash, crlfHash, "Same content with different line endings should produce the same hash")
}

func TestHashFile_ErrorOnMissing(t *testing.T) {
	_, err := HashFile("/nonexistent/path/file.txt")
	require.Error(t, err)
}

// --- ENH-117: ResolveTestCaseSpec tests ---

func TestResolveTestCaseSpec_TopLevel(t *testing.T) {
	root := t.TempDir()
	casesDir := filepath.Join(root, "gtms", "test", "cases")
	require.NoError(t, os.MkdirAll(casesDir, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(casesDir, "tc-aaa11111-some-test.md"),
		[]byte("---\ntest_case_id: tc-aaa11111\ntitle: test\n---\n"), 0644))

	path, err := ResolveTestCaseSpec(root, "tc-aaa11111")
	require.NoError(t, err)
	assert.Equal(t, "gtms/test/cases/tc-aaa11111-some-test.md", path)
}

func TestResolveTestCaseSpec_Subfolder(t *testing.T) {
	root := t.TempDir()
	subDir := filepath.Join(root, "gtms", "test", "cases", "my-feature")
	require.NoError(t, os.MkdirAll(subDir, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(subDir, "tc-bbb22222-another-test.md"),
		[]byte("---\ntest_case_id: tc-bbb22222\ntitle: test\n---\n"), 0644))

	path, err := ResolveTestCaseSpec(root, "tc-bbb22222")
	require.NoError(t, err)
	assert.Equal(t, "gtms/test/cases/my-feature/tc-bbb22222-another-test.md", path)
}

func TestResolveTestCaseSpec_NotFound(t *testing.T) {
	root := t.TempDir()
	casesDir := filepath.Join(root, "gtms", "test", "cases")
	require.NoError(t, os.MkdirAll(casesDir, 0755))

	_, err := ResolveTestCaseSpec(root, "tc-nonexist")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tc-nonexist")
	assert.Contains(t, err.Error(), "not found")
}

func skipIfShort(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}
}
