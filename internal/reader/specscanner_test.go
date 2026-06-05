package reader

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScanSpecFiles_FindsIDs(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join("specs", "checkout.spec.ts"),
		`@test "tc-a1b2c3d0: some test" { echo ok }`)

	result, err := ScanSpecFiles(root, []string{"specs/"})
	require.NoError(t, err)

	assert.Contains(t, result, "tc-a1b2c3d0")
	require.Len(t, result["tc-a1b2c3d0"], 1)
	assert.Equal(t, "specs/checkout.spec.ts", result["tc-a1b2c3d0"][0])
}

func TestScanSpecFiles_CaseInsensitive(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join("specs", "upper.spec.ts"),
		`test("TC-A1B2C3D0: upper case")`)

	result, err := ScanSpecFiles(root, []string{"specs/"})
	require.NoError(t, err)

	// Should normalise to lowercase
	assert.Contains(t, result, "tc-a1b2c3d0")
	assert.NotContains(t, result, "TC-A1B2C3D0")
}

func TestScanSpecFiles_MultipleIDsPerFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join("specs", "multi.spec.ts"),
		"test('tc-001: first');\ntest('tc-002: second');")

	result, err := ScanSpecFiles(root, []string{"specs/"})
	require.NoError(t, err)

	assert.Contains(t, result, "tc-001")
	assert.Contains(t, result, "tc-002")
}

func TestScanSpecFiles_MultipleFilesPerID(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join("specs", "a.spec.ts"), "test('tc-007: in file a')")
	writeFile(t, root, filepath.Join("specs", "b.spec.ts"), "test('tc-007: in file b')")

	result, err := ScanSpecFiles(root, []string{"specs/"})
	require.NoError(t, err)

	require.Contains(t, result, "tc-007")
	assert.Len(t, result["tc-007"], 2)
}

func TestScanSpecFiles_SkipsMissingDirs(t *testing.T) {
	root := t.TempDir()

	result, err := ScanSpecFiles(root, []string{"nonexistent-dir/"})
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestScanSpecFiles_WordBoundaryRejectsLongerHex(t *testing.T) {
	root := t.TempDir()
	// TC-a1b2c3d4e5 is too long (10 hex chars) — should NOT match as TC-a1b2c3d4
	writeFile(t, root, filepath.Join("specs", "long.spec.ts"),
		`test("tc-a1b2c3d4e5: overly long hex id")`)

	result, err := ScanSpecFiles(root, []string{"specs/"})
	require.NoError(t, err)
	assert.Empty(t, result, "should not match partial prefix of a longer hex string")
}

func TestScanSpecFiles_NoMatches(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join("specs", "noref.ts"),
		"const x = 42; // no test case references here")

	result, err := ScanSpecFiles(root, []string{"specs/"})
	require.NoError(t, err)
	assert.Empty(t, result)
}
