package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The TestDiscoverTestCases_* and TestExtractTestCaseID suites tested
// cli.DiscoverTestCases and extractTestCaseID, the folder-walk work-set builder
// ENH-205 deleted (dead since BUG-185/188; the bulk work-set is built by
// cli.resolveBulkOwners via the snapshot's SelectFolder). Only IsBulkFolder
// remains in discover.go, and its tests are retained below.

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
