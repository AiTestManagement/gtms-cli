package testcase

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExists_UnqualifiedID_FileExists(t *testing.T) {
	root := t.TempDir()
	casesDir := filepath.Join(root, "gtms", "test", "cases", "login")
	require(t, os.MkdirAll(casesDir, 0755))
	require(t, os.WriteFile(filepath.Join(casesDir, "tc-abc12345-login-test.md"), []byte("spec"), 0644))

	assert.True(t, Exists(root, "tc-abc12345"))
}

func TestExists_UnqualifiedID_NoFile(t *testing.T) {
	root := t.TempDir()
	casesDir := filepath.Join(root, "gtms", "test", "cases")
	require(t, os.MkdirAll(casesDir, 0755))

	assert.False(t, Exists(root, "tc-deadbeef"))
}

func TestExists_FolderQualified_CorrectFolder(t *testing.T) {
	root := t.TempDir()
	casesDir := filepath.Join(root, "gtms", "test", "cases", "login")
	require(t, os.MkdirAll(casesDir, 0755))
	require(t, os.WriteFile(filepath.Join(casesDir, "tc-abc12345-login-test.md"), []byte("spec"), 0644))

	assert.True(t, Exists(root, "login/tc-abc12345"))
}

func TestExists_FolderQualified_WrongFolder(t *testing.T) {
	root := t.TempDir()
	casesDir := filepath.Join(root, "gtms", "test", "cases", "login")
	require(t, os.MkdirAll(casesDir, 0755))
	require(t, os.WriteFile(filepath.Join(casesDir, "tc-abc12345-login-test.md"), []byte("spec"), 0644))

	// File exists under login/, but we ask for checkout/
	assert.False(t, Exists(root, "checkout/tc-abc12345"))
}

func TestExists_UnqualifiedFindsInSubfolder(t *testing.T) {
	root := t.TempDir()
	casesDir := filepath.Join(root, "gtms", "test", "cases", "deep", "nested")
	require(t, os.MkdirAll(casesDir, 0755))
	require(t, os.WriteFile(filepath.Join(casesDir, "tc-abc12345-nested-test.md"), []byte("spec"), 0644))

	// Unqualified should find it anywhere under gtms/test/cases/
	assert.True(t, Exists(root, "tc-abc12345"))
}

func TestExists_CasesDirDoesNotExist(t *testing.T) {
	root := t.TempDir()
	// No gtms/test/cases/ directory at all
	assert.False(t, Exists(root, "tc-abc12345"))
}

func TestExists_MultipleFiles_OneMatches(t *testing.T) {
	root := t.TempDir()
	casesDir := filepath.Join(root, "gtms", "test", "cases")
	require(t, os.MkdirAll(casesDir, 0755))
	require(t, os.WriteFile(filepath.Join(casesDir, "tc-11111111-first.md"), []byte("spec"), 0644))
	require(t, os.WriteFile(filepath.Join(casesDir, "tc-22222222-second.md"), []byte("spec"), 0644))

	assert.True(t, Exists(root, "tc-22222222"))
	assert.False(t, Exists(root, "tc-33333333"))
}

func TestExists_FileMatchesDotExtension(t *testing.T) {
	root := t.TempDir()
	casesDir := filepath.Join(root, "gtms", "test", "cases")
	require(t, os.MkdirAll(casesDir, 0755))
	require(t, os.WriteFile(filepath.Join(casesDir, "tc-abc12345.md"), []byte("spec"), 0644))

	assert.True(t, Exists(root, "tc-abc12345"))
}

func TestExists_PartialIDDoesNotMatch(t *testing.T) {
	root := t.TempDir()
	casesDir := filepath.Join(root, "gtms", "test", "cases")
	require(t, os.MkdirAll(casesDir, 0755))
	require(t, os.WriteFile(filepath.Join(casesDir, "tc-abc12345-test.md"), []byte("spec"), 0644))

	// "tc-abc1234" is a prefix of "tc-abc12345" but should NOT match
	// because the file base is "tc-abc12345-test.md" and HasPrefix("tc-abc12345-test.md", "tc-abc1234-")
	// is false (the 5th hex char differs).
	// Actually "tc-abc1234-" IS a prefix of "tc-abc12345-test.md" -- let's verify this is the intended behaviour.
	// The existing helpers match on HasPrefix(base, target+"-"), so "tc-abc1234" would match "tc-abc12345-test.md"
	// because "tc-abc12345-test.md" starts with "tc-abc1234" + "-" would be "tc-abc1234-" and
	// "tc-abc12345-test.md" does NOT start with "tc-abc1234-".
	// It starts with "tc-abc12345" not "tc-abc1234-". So this correctly does not match.
	assert.False(t, Exists(root, "tc-abc1234"))
}

func TestExists_FolderQualifiedSubfolderDoesNotExist(t *testing.T) {
	root := t.TempDir()
	casesDir := filepath.Join(root, "gtms", "test", "cases")
	require(t, os.MkdirAll(casesDir, 0755))
	// The "nonexistent" subfolder doesn't exist
	assert.False(t, Exists(root, "nonexistent/tc-abc12345"))
}

// require is a test helper that fails the test immediately on error.
func require(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
