package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/output"
)

// REV-160 CLAUDE-005 -- `gtms link --refresh <emptyfolder>` must error, not exit 0.
//
// At b524c9b5 runFolderRefresh went through DiscoverTestCases, which returned
// "No test cases found in gtms/test/cases/<folder>/" for an existing but empty
// folder: output.Errorf with a hint, and a non-zero exit. This branch replaced it
// with snap.SelectFolder and never checked for an empty owner set, so refresh
// printed "0 refreshed, 0 current, 0 failed" and succeeded -- which makes a
// scripted `gtms link --refresh <folder> && ...` chain proceed against a folder
// holding nothing. The sibling bulk helper (resolveBulkOwners) kept the error for
// exactly this case.
func TestLinkRefresh_EmptyFolder_ErrorsInsteadOfSucceeding(t *testing.T) {
	skipIfShort(t)
	root := setupCompatProject(t)
	restore := withExecuteGlobals(t, root, batsLinkConfig())
	defer restore()

	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/test/cases/emptyfolder"), 0o755))

	stderr, err := runCmdCaptureStderr(t, newLinkCmd(), "emptyfolder", "--refresh")

	require.Error(t, err, "an existing but empty folder must not be a silent zero-work success")
	assert.True(t, output.IsDisplayed(err), "the refusal must be rendered through output.Errorf")
	assert.Contains(t, stderr, "No test cases found in gtms/test/cases/emptyfolder/",
		"the b524c9b5 diagnostic must be preserved")
	assert.Contains(t, stderr, "contains tc-*.md files", "the actionable hint must be preserved")
}

// The dry-run preview must refuse identically -- it is the same selection, and a
// preview that reports "nothing to do" for a folder that holds nothing is the
// same silent success one flag away.
func TestLinkRefresh_EmptyFolder_DryRunAlsoErrors(t *testing.T) {
	skipIfShort(t)
	root := setupCompatProject(t)
	restore := withExecuteGlobals(t, root, batsLinkConfig())
	defer restore()

	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/test/cases/emptyfolder"), 0o755))

	stderr, err := runCmdCaptureStderr(t, newLinkCmd(), "emptyfolder", "--refresh", "--dry-run")

	require.Error(t, err)
	assert.Contains(t, stderr, "No test cases found in gtms/test/cases/emptyfolder/")
}

// Control: a folder that DOES hold a spec still refreshes and exits 0, so the new
// guard cannot be satisfied by refusing every folder.
func TestLinkRefresh_NonEmptyFolder_StillSucceeds(t *testing.T) {
	skipIfShort(t)
	root := setupCompatProject(t)
	restore := withExecuteGlobals(t, root, batsLinkConfig())
	defer restore()

	_ = seedStaleLinkedTC(t, root, "checkout", "tc-77777777")

	_, err := runCmdCaptureStdout(t, newLinkCmd(), "checkout", "--refresh")
	require.NoError(t, err, "a folder with a wired spec must still refresh cleanly")
}
