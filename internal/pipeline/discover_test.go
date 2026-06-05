package pipeline

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- DiscoverArtefact tests ---

func TestDiscoverArtefact_HappyPath(t *testing.T) {
	root := t.TempDir()
	setupSentinel(t, root)

	// Create a single artefact file
	dir := filepath.Join(root, "test", "acceptance", "my-feature")
	require.NoError(t, os.MkdirAll(dir, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "tc-abc12345-some-test.bats"),
		[]byte("#!/usr/bin/env bats"), 0644,
	))

	result, err := DiscoverArtefact(root, "test/acceptance/**/{testcase}*.bats", "tc-abc12345")
	require.NoError(t, err)
	assert.Equal(t, "test/acceptance/my-feature/tc-abc12345-some-test.bats", result)
}

func TestDiscoverArtefact_EmptyPattern(t *testing.T) {
	root := t.TempDir()

	result, err := DiscoverArtefact(root, "", "tc-abc12345")
	assert.NoError(t, err)
	assert.Empty(t, result)
}

func TestDiscoverArtefact_NoMatches(t *testing.T) {
	root := t.TempDir()
	setupSentinel(t, root)

	// Create a file that does NOT match the pattern
	dir := filepath.Join(root, "test", "acceptance")
	require.NoError(t, os.MkdirAll(dir, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "tc-OTHER00-something.bats"),
		[]byte("test"), 0644,
	))

	_, err := DiscoverArtefact(root, "test/acceptance/**/{testcase}*.bats", "tc-abc12345")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "No artefact found")
	assert.Contains(t, err.Error(), "tc-abc12345")
	// Error surfaces the post-substitution pattern so users see the concrete
	// path shape that was searched, not the unsubstituted {testcase} template.
	assert.Contains(t, err.Error(), "test/acceptance/**/tc-abc12345*.bats")
}

func TestDiscoverArtefact_MultipleMatches(t *testing.T) {
	root := t.TempDir()
	setupSentinel(t, root)

	// Create two matching artefact files
	dir1 := filepath.Join(root, "test", "acceptance", "folder-a")
	dir2 := filepath.Join(root, "test", "acceptance", "folder-b")
	require.NoError(t, os.MkdirAll(dir1, 0755))
	require.NoError(t, os.MkdirAll(dir2, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir1, "tc-abc12345-first.bats"),
		[]byte("test1"), 0644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir2, "tc-abc12345-second.bats"),
		[]byte("test2"), 0644,
	))

	_, err := DiscoverArtefact(root, "test/acceptance/**/{testcase}*.bats", "tc-abc12345")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Multiple artefacts found")
	assert.Contains(t, err.Error(), "tc-abc12345")
	assert.Contains(t, err.Error(), "gtms link")
}

func TestDiscoverArtefact_SubstitutesTestcase(t *testing.T) {
	root := t.TempDir()
	setupSentinel(t, root)

	// Create artefact with specific TC ID
	dir := filepath.Join(root, "scripts")
	require.NoError(t, os.MkdirAll(dir, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "tc-ff001122.spec.ts"),
		[]byte("test"), 0644,
	))

	result, err := DiscoverArtefact(root, "scripts/{testcase}*.spec.ts", "tc-ff001122")
	require.NoError(t, err)
	assert.Equal(t, "scripts/tc-ff001122.spec.ts", result)
}

func TestDiscoverArtefact_DoublestarRecursive(t *testing.T) {
	root := t.TempDir()
	setupSentinel(t, root)

	// Create artefact deeply nested
	dir := filepath.Join(root, "test", "acceptance", "deep", "nested", "feature")
	require.NoError(t, os.MkdirAll(dir, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "tc-deep0001-nested-test.bats"),
		[]byte("test"), 0644,
	))

	result, err := DiscoverArtefact(root, "test/acceptance/**/{testcase}*.bats", "tc-deep0001")
	require.NoError(t, err)
	assert.Equal(t, "test/acceptance/deep/nested/feature/tc-deep0001-nested-test.bats", result)
}

func TestDiscoverArtefact_DoublestarZeroLevels(t *testing.T) {
	root := t.TempDir()
	setupSentinel(t, root)

	// Create artefact directly in test/acceptance (** matches zero levels)
	dir := filepath.Join(root, "test", "acceptance")
	require.NoError(t, os.MkdirAll(dir, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "tc-zero0001-direct.bats"),
		[]byte("test"), 0644,
	))

	result, err := DiscoverArtefact(root, "test/acceptance/**/{testcase}*.bats", "tc-zero0001")
	require.NoError(t, err)
	assert.Equal(t, "test/acceptance/tc-zero0001-direct.bats", result)
}

func TestDiscoverArtefact_SkipsGitAndGtmsDirs(t *testing.T) {
	root := t.TempDir()
	setupSentinel(t, root)

	// Create matching files inside .git and .gtms — they must NOT be found
	gitDir := filepath.Join(root, ".git", "hooks")
	gtmsDir := filepath.Join(root, ".gtms", "tmp")
	require.NoError(t, os.MkdirAll(gitDir, 0755))
	require.NoError(t, os.MkdirAll(gtmsDir, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(gitDir, "tc-skip0001-git.bats"),
		[]byte("test"), 0644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(gtmsDir, "tc-skip0001-gtms.bats"),
		[]byte("test"), 0644,
	))

	_, err := DiscoverArtefact(root, "**/{testcase}*.bats", "tc-skip0001")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "No artefact found")
}

func TestDiscoverArtefact_SkipsSentinelParent(t *testing.T) {
	root := t.TempDir()
	setupSentinel(t, root)

	// Create matching file inside the sentinel parent (gtms/)
	dir := filepath.Join(root, "gtms", "adapters")
	require.NoError(t, os.MkdirAll(dir, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "tc-sent0001-adapter.bats"),
		[]byte("test"), 0644,
	))

	_, err := DiscoverArtefact(root, "**/{testcase}*.bats", "tc-sent0001")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "No artefact found")
}

func TestDiscoverArtefact_UnsafeSymlinkOnly_ReturnsPathSafetyError(t *testing.T) {
	trySymlink(t)
	root := t.TempDir()
	setupSentinel(t, root)

	// The real artefact lives OUTSIDE the project root.
	external := t.TempDir()
	externalArtefact := filepath.Join(external, "tc-esc00001.bats")
	require.NoError(t, os.WriteFile(externalArtefact, []byte("external"), 0644))

	// The only in-tree glob match is a symlink that escapes the project root.
	inTreeDir := filepath.Join(root, "test", "acceptance", "escape")
	require.NoError(t, os.MkdirAll(inTreeDir, 0755))
	require.NoError(t, os.Symlink(externalArtefact, filepath.Join(inTreeDir, "tc-esc00001.bats")))

	_, err := DiscoverArtefact(root, "test/acceptance/**/{testcase}*.bats", "tc-esc00001")
	require.Error(t, err)
	// The escaping symlink must be reported as a path-safety failure — not
	// degraded to the generic "No artefact found", which would mislead the
	// user into creating an artefact that already exists outside the root.
	assert.Contains(t, err.Error(), "path safety")
	assert.Contains(t, err.Error(), "outside the project root")
	assert.NotContains(t, err.Error(), "No artefact found")
}

func TestDiscoverArtefact_SafeMatchWinsOverUnsafeSymlink(t *testing.T) {
	trySymlink(t)
	root := t.TempDir()
	setupSentinel(t, root)

	// A safe, in-tree artefact for the TC.
	safeDir := filepath.Join(root, "test", "acceptance", "real")
	require.NoError(t, os.MkdirAll(safeDir, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(safeDir, "tc-esc00002.bats"), []byte("safe"), 0644))

	// An unsafe symlink for the SAME TC elsewhere in the tree. Discovery must
	// use the safe artefact and ignore the unsafe candidate — an unsafe path
	// is only an error when it explains why nothing actionable was found.
	external := t.TempDir()
	externalArtefact := filepath.Join(external, "tc-esc00002.bats")
	require.NoError(t, os.WriteFile(externalArtefact, []byte("external"), 0644))
	escapeDir := filepath.Join(root, "test", "acceptance", "escape")
	require.NoError(t, os.MkdirAll(escapeDir, 0755))
	require.NoError(t, os.Symlink(externalArtefact, filepath.Join(escapeDir, "tc-esc00002.bats")))

	result, err := DiscoverArtefact(root, "test/acceptance/**/{testcase}*.bats", "tc-esc00002")
	require.NoError(t, err)
	assert.Equal(t, "test/acceptance/real/tc-esc00002.bats", result)
}

func TestDiscoverArtefact_PatternWithoutDoublestar(t *testing.T) {
	root := t.TempDir()
	setupSentinel(t, root)

	// Create artefact at exact path (no ** in pattern)
	dir := filepath.Join(root, "test", "acceptance")
	require.NoError(t, os.MkdirAll(dir, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "tc-exact001-test.bats"),
		[]byte("test"), 0644,
	))

	result, err := DiscoverArtefact(root, "test/acceptance/{testcase}*.bats", "tc-exact001")
	require.NoError(t, err)
	assert.Equal(t, "test/acceptance/tc-exact001-test.bats", result)
}

// --- matchDoublestar tests ---

func TestMatchDoublestar_ExactMatch(t *testing.T) {
	assert.True(t, matchDoublestar(
		[]string{"test", "acceptance", "foo.bats"},
		[]string{"test", "acceptance", "foo.bats"},
	))
}

func TestMatchDoublestar_WildcardMatch(t *testing.T) {
	assert.True(t, matchDoublestar(
		[]string{"test", "acceptance", "*.bats"},
		[]string{"test", "acceptance", "foo.bats"},
	))
}

func TestMatchDoublestar_DoublestarMiddle(t *testing.T) {
	assert.True(t, matchDoublestar(
		[]string{"test", "**", "*.bats"},
		[]string{"test", "acceptance", "feature", "foo.bats"},
	))
}

func TestMatchDoublestar_DoublestarZeroLevels(t *testing.T) {
	assert.True(t, matchDoublestar(
		[]string{"test", "**", "*.bats"},
		[]string{"test", "foo.bats"},
	))
}

func TestMatchDoublestar_DoublestarEnd(t *testing.T) {
	assert.True(t, matchDoublestar(
		[]string{"test", "**"},
		[]string{"test", "acceptance", "feature", "foo.bats"},
	))
}

func TestMatchDoublestar_NoMatch(t *testing.T) {
	assert.False(t, matchDoublestar(
		[]string{"test", "acceptance", "*.bats"},
		[]string{"test", "acceptance", "foo.sh"},
	))
}

func TestMatchDoublestar_PathTooShort(t *testing.T) {
	assert.False(t, matchDoublestar(
		[]string{"test", "acceptance", "*.bats"},
		[]string{"test", "acceptance"},
	))
}

func TestMatchDoublestar_PatternTooLong(t *testing.T) {
	assert.False(t, matchDoublestar(
		[]string{"test", "acceptance", "extra", "*.bats"},
		[]string{"test", "acceptance", "foo.bats"},
	))
}

// --- TryAutoCreateRecord tests ---

func TestTryAutoCreateRecord_HappyPath(t *testing.T) {
	root := t.TempDir()
	setupSentinel(t, root)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation/records"), 0755))

	// Create artefact file
	dir := filepath.Join(root, "test", "acceptance", "my-feature")
	require.NoError(t, os.MkdirAll(dir, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "tc-auto0001-test.bats"),
		[]byte("test content"), 0644,
	))

	opts := AutoCreateOptions{
		TestCaseID:   "tc-auto0001",
		Framework:    "bats",
		AdapterName:  "bats-runner",
		Branch:       "main",
		ArtefactGlob: "test/acceptance/**/{testcase}*.bats",
	}

	record, recordPath, err := TryAutoCreateRecord(root, opts)
	require.NoError(t, err)
	require.NotNil(t, record)
	assert.NotEmpty(t, recordPath)

	// Verify record fields
	assert.Equal(t, "tc-auto0001", record.TestCase)
	assert.Equal(t, "bats", record.Framework)
	assert.Equal(t, "developed", record.Status)
	assert.Equal(t, "bats-runner", record.Adapter)
	assert.Equal(t, "linked", record.LastDevResult)
	assert.Equal(t, "test/acceptance/my-feature/tc-auto0001-test.bats", record.Artefact)
	assert.NotEmpty(t, record.ArtefactHash)
	assert.Equal(t, 1, record.Cycle)
}

func TestTryAutoCreateRecord_NoGlob(t *testing.T) {
	root := t.TempDir()

	opts := AutoCreateOptions{
		TestCaseID:   "tc-abc12345",
		Framework:    "bats",
		AdapterName:  "runner",
		ArtefactGlob: "", // no glob configured
	}

	record, recordPath, err := TryAutoCreateRecord(root, opts)
	assert.NoError(t, err)
	assert.Nil(t, record)
	assert.Empty(t, recordPath)
}

func TestTryAutoCreateRecord_ExistingRecord(t *testing.T) {
	root := t.TempDir()
	setupSentinel(t, root)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation/records"), 0755))

	// Create artefact file
	dir := filepath.Join(root, "test", "acceptance")
	require.NoError(t, os.MkdirAll(dir, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "tc-exist001-test.bats"),
		[]byte("test"), 0644,
	))

	// Create existing record first
	createErr := CreateAutomationRecord(root, RecordOptions{
		TestCase:      "tc-exist001",
		Framework:     "bats",
		Artefact:      "test/acceptance/tc-exist001-test.bats",
		Adapter:       "other-adapter",
		LastDevResult: "pass",
	})
	require.NoError(t, createErr)

	// Try auto-create — should fail because record exists and Force=false
	opts := AutoCreateOptions{
		TestCaseID:   "tc-exist001",
		Framework:    "bats",
		AdapterName:  "bats-runner",
		ArtefactGlob: "test/acceptance/**/{testcase}*.bats",
	}

	_, _, err := TryAutoCreateRecord(root, opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestTryAutoCreateRecord_NoMatch(t *testing.T) {
	root := t.TempDir()
	setupSentinel(t, root)

	// No artefact file exists
	require.NoError(t, os.MkdirAll(filepath.Join(root, "test", "acceptance"), 0755))

	opts := AutoCreateOptions{
		TestCaseID:   "tc-nomatch1",
		Framework:    "bats",
		AdapterName:  "bats-runner",
		ArtefactGlob: "test/acceptance/**/{testcase}*.bats",
	}

	_, _, err := TryAutoCreateRecord(root, opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "No artefact found")
}

// --- helpers ---

// setupSentinel creates the gtms/.gtms-root sentinel so layout.ParentDir()
// can resolve the parent directory during testing. Without this, the walk
// exclusion of the sentinel parent would not work correctly.
func setupSentinel(t *testing.T, root string) {
	t.Helper()
	gtmsDir := filepath.Join(root, "gtms")
	require.NoError(t, os.MkdirAll(gtmsDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(gtmsDir, ".gtms-root"), []byte(""), 0644))
}

// trySymlink skips the calling test when the platform/user cannot create real
// symbolic links. Windows without developer mode / admin cannot — the BATS
// sibling tc-f58d27b3 skips for the same reason.
func trySymlink(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, "probe-target")
	link := filepath.Join(dir, "probe-link")
	require.NoError(t, os.WriteFile(target, []byte("x"), 0644))
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks not supported by current user/platform")
	}
	fi, err := os.Lstat(link)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Skip("os.Symlink did not produce a real symlink on this platform")
	}
}
