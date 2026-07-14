package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscoverTestCases_FindsTCsAlphabetically(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "gtms/test/cases", "my-feature")
	require.NoError(t, os.MkdirAll(dir, 0755))

	// Create files in non-alphabetical order
	for _, name := range []string{
		"tc-c5d7e9f-third.md",
		"tc-a1b2c3d-first.md",
		"tc-b3c4d5e-second.md",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("# test"), 0644))
	}

	ids, err := DiscoverTestCases(root, "my-feature", false)
	require.NoError(t, err)
	assert.Equal(t, []string{"tc-a1b2c3d", "tc-b3c4d5e", "tc-c5d7e9f"}, ids)
}

func TestDiscoverTestCases_EmptyFolder(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "gtms/test/cases", "empty-feature")
	require.NoError(t, os.MkdirAll(dir, 0755))

	_, err := DiscoverTestCases(root, "empty-feature", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "No test cases found")
}

func TestDiscoverTestCases_IgnoresNonTCFiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "gtms/test/cases", "mixed")
	require.NoError(t, os.MkdirAll(dir, 0755))

	// tc-*.md files
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-001-test.md"), []byte("# test"), 0644))
	// Non-TC files
	require.NoError(t, os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# readme"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("notes"), 0644))
	// Subdirectory (should be ignored)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "subdir"), 0755))

	ids, err := DiscoverTestCases(root, "mixed", false)
	require.NoError(t, err)
	assert.Equal(t, []string{"tc-001"}, ids)
}

func TestDiscoverTestCases_FolderNotExist(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/test/cases"), 0755))

	_, err := DiscoverTestCases(root, "nonexistent", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist")
}

func TestDiscoverTestCases_NestedFolder(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "gtms/test/cases", "payments", "checkout")
	require.NoError(t, os.MkdirAll(dir, 0755))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-abc1234-cart.md"), []byte("# test"), 0644))

	ids, err := DiscoverTestCases(root, "payments/checkout", false)
	require.NoError(t, err)
	assert.Equal(t, []string{"tc-abc1234"}, ids)
}

func TestDiscoverTestCases_OnlyNonTCFiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "gtms/test/cases", "no-tcs")
	require.NoError(t, os.MkdirAll(dir, 0755))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# readme"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("notes"), 0644))

	_, err := DiscoverTestCases(root, "no-tcs", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "No test cases found")
}

func TestIsBulkFolder_DirectoryExists(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "gtms/test/cases", "my-feature")
	require.NoError(t, os.MkdirAll(dir, 0755))

	assert.True(t, IsBulkFolder(root, "my-feature"))
}

func TestIsBulkFolder_NotDirectory(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/test/cases"), 0755))

	assert.False(t, IsBulkFolder(root, "nonexistent"))
}

func TestIsBulkFolder_FileNotDir(t *testing.T) {
	root := t.TempDir()
	tcDir := filepath.Join(root, "gtms/test/cases")
	require.NoError(t, os.MkdirAll(tcDir, 0755))
	// Create a file with the same name (not a directory)
	require.NoError(t, os.WriteFile(filepath.Join(tcDir, "my-feature"), []byte("file"), 0644))

	assert.False(t, IsBulkFolder(root, "my-feature"))
}

func TestIsBulkFolder_NestedDirectory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "gtms/test/cases", "payments", "checkout")
	require.NoError(t, os.MkdirAll(dir, 0755))

	assert.True(t, IsBulkFolder(root, "payments/checkout"))
	assert.False(t, IsBulkFolder(root, "payments/nonexistent"))
}

func TestDiscoverTestCases_RecursiveFindsSubdirs(t *testing.T) {
	root := t.TempDir()
	topDir := filepath.Join(root, "gtms/test/cases", "feature")
	subDir := filepath.Join(topDir, "login")
	require.NoError(t, os.MkdirAll(topDir, 0755))
	require.NoError(t, os.MkdirAll(subDir, 0755))

	// Top-level TC
	require.NoError(t, os.WriteFile(filepath.Join(topDir, "tc-001-top.md"), []byte("# test"), 0644))
	// Subdirectory TC
	require.NoError(t, os.WriteFile(filepath.Join(subDir, "tc-002-nested.md"), []byte("# test"), 0644))

	ids, err := DiscoverTestCases(root, "feature", true)
	require.NoError(t, err)
	assert.Equal(t, []string{"tc-001", "tc-002"}, ids)
}

func TestDiscoverTestCases_RecursiveFalseIgnoresSubdirs(t *testing.T) {
	root := t.TempDir()
	topDir := filepath.Join(root, "gtms/test/cases", "feature")
	subDir := filepath.Join(topDir, "login")
	require.NoError(t, os.MkdirAll(topDir, 0755))
	require.NoError(t, os.MkdirAll(subDir, 0755))

	// Top-level TC
	require.NoError(t, os.WriteFile(filepath.Join(topDir, "tc-001-top.md"), []byte("# test"), 0644))
	// Subdirectory TC (should be ignored)
	require.NoError(t, os.WriteFile(filepath.Join(subDir, "tc-002-nested.md"), []byte("# test"), 0644))

	ids, err := DiscoverTestCases(root, "feature", false)
	require.NoError(t, err)
	assert.Equal(t, []string{"tc-001"}, ids)
}

func TestDiscoverTestCases_RecursiveEmptySubdirs(t *testing.T) {
	root := t.TempDir()
	topDir := filepath.Join(root, "gtms/test/cases", "feature")
	subDir := filepath.Join(topDir, "empty-sub")
	require.NoError(t, os.MkdirAll(topDir, 0755))
	require.NoError(t, os.MkdirAll(subDir, 0755))

	// Only top-level TC, subdirectory is empty
	require.NoError(t, os.WriteFile(filepath.Join(topDir, "tc-001-only.md"), []byte("# test"), 0644))

	ids, err := DiscoverTestCases(root, "feature", true)
	require.NoError(t, err)
	assert.Equal(t, []string{"tc-001"}, ids)
}

func TestDiscoverTestCases_RecursiveSortedAcrossDepths(t *testing.T) {
	root := t.TempDir()
	topDir := filepath.Join(root, "gtms/test/cases", "feature")
	subA := filepath.Join(topDir, "alpha")
	subB := filepath.Join(topDir, "beta")
	require.NoError(t, os.MkdirAll(topDir, 0755))
	require.NoError(t, os.MkdirAll(subA, 0755))
	require.NoError(t, os.MkdirAll(subB, 0755))

	// Create TCs across depths in non-alphabetical order
	require.NoError(t, os.WriteFile(filepath.Join(subB, "tc-zzz0001-last.md"), []byte("# test"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(topDir, "tc-mmm0001-middle.md"), []byte("# test"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(subA, "tc-aaa0001-first.md"), []byte("# test"), 0644))

	ids, err := DiscoverTestCases(root, "feature", true)
	require.NoError(t, err)
	assert.Equal(t, []string{"tc-aaa0001", "tc-mmm0001", "tc-zzz0001"}, ids)
}

func TestDiscoverTestCases_RecursiveDeepNesting(t *testing.T) {
	root := t.TempDir()
	topDir := filepath.Join(root, "gtms/test/cases", "feature")
	level2 := filepath.Join(topDir, "sub1")
	level3 := filepath.Join(level2, "sub2")
	require.NoError(t, os.MkdirAll(level3, 0755))

	require.NoError(t, os.WriteFile(filepath.Join(topDir, "tc-001-top.md"), []byte("# test"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(level2, "tc-002-mid.md"), []byte("# test"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(level3, "tc-003-deep.md"), []byte("# test"), 0644))

	ids, err := DiscoverTestCases(root, "feature", true)
	require.NoError(t, err)
	assert.Equal(t, []string{"tc-001", "tc-002", "tc-003"}, ids)
}

func TestDiscoverTestCases_RecursiveNoTCsAnywhere(t *testing.T) {
	root := t.TempDir()
	topDir := filepath.Join(root, "gtms/test/cases", "feature")
	subDir := filepath.Join(topDir, "sub")
	require.NoError(t, os.MkdirAll(subDir, 0755))

	// Non-TC files only
	require.NoError(t, os.WriteFile(filepath.Join(topDir, "readme.md"), []byte("# readme"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(subDir, "notes.txt"), []byte("notes"), 0644))

	_, err := DiscoverTestCases(root, "feature", true)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "No test cases found")
}

func TestExtractTestCaseID(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"tc-a1b2c3d-login-happy.md", "tc-a1b2c3d"},
		{"tc-007-simple.md", "tc-007"},
		{"tc-abc1234.md", "tc-abc1234"},
		{"tc-a1b2c3d-multi-dash-name.md", "tc-a1b2c3d"},
		{"readme.md", ""},
		{"tc-.md", ""},
		{"tc-abc.txt", ""},
		{"not-a-tc.md", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := extractTestCaseID(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
