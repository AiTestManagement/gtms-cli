package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/config"
)

// BUG-167 follow-up: the shared classifier must not lose the historical
// "Invalid test case ID format" response that ~15 frozen acceptance specs
// (bug-058-pathsafe-validation, folder-level-bulk-execution tc-13dbade0, etc.)
// assert on. Two regressions are locked here:
//   1. execute/automate/standard-link emit the generic "Invalid test case ID
//      format" message AND the classifier's specific cause on a rejected target.
//   2. execute/automate enter bulk mode ONLY for an EXISTING folder; a safe but
//      nonexistent folder-shaped arg falls through to the generic invalid-format
//      response (ENH-052 contract, tc-13dbade0), while a real mixed-case folder
//      still enters bulk mode.

// setupCompatProject creates a git repo with the minimal GTMS folder structure
// that execute/automate validate before dispatch.
func setupCompatProject(t *testing.T) string {
	t.Helper()
	root := initGitRepo(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/test/cases"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation"), 0o755))
	return root
}

// runCmdCaptureStderr drives a cobra command via its RunE and returns the
// stderr text (where output.Errorf writes) plus the command error.
func runCmdCaptureStderr(t *testing.T, cmd *cobra.Command, args ...string) (string, error) {
	t.Helper()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs(args)
	cmd.SetContext(context.Background())
	var err error
	out := captureStderr(t, func() { err = cmd.Execute() })
	return out, err
}

// --- Regression 1: message composition helper (shared by all three commands) ---

func TestInvalidTargetFormatError_KeepsSubstringAndCause(t *testing.T) {
	cause := fmt.Errorf("folder name 'C:/tc-abc12345' contains invalid characters -- use only letters")
	msg, hint := invalidTargetFormatError(`C:\tc-abc12345`, cause)

	assert.Contains(t, msg, "Invalid test case ID format", "must keep the BUG-058 compatibility substring")
	assert.Contains(t, msg, `C:\tc-abc12345`, "must echo the raw target")
	assert.Contains(t, hint, "contains invalid characters", "must surface the classifier's specific cause")
	assert.Contains(t, hint, "Use format like tc-a1b2c3d4.", "must keep the TC-format hint")
}

// --- Regression 1: execute / automate / standard-link on a rejected target ---

func TestExecute_UnsafeTarget_InvalidFormatWithCause(t *testing.T) {
	skipIfShort(t)
	root := setupCompatProject(t)
	restore := withExecuteGlobals(t, root, &config.Config{})
	defer restore()

	out, err := runCmdCaptureStderr(t, newExecuteCmd(), `C:\tc-abc12345`)
	require.Error(t, err)
	assert.Contains(t, out, "Invalid test case ID format")
	assert.Contains(t, out, "invalid characters", "the specific classifier cause must also appear")
}

func TestAutomate_UnsafeTarget_InvalidFormatWithCause(t *testing.T) {
	skipIfShort(t)
	root := setupCompatProject(t)
	restore := withExecuteGlobals(t, root, &config.Config{})
	defer restore()

	out, err := runCmdCaptureStderr(t, newAutomateCmd(), `tc-abc/../../etc/passwd`)
	require.Error(t, err)
	assert.Contains(t, out, "Invalid test case ID format")
	assert.Contains(t, out, "path traversal", "the specific classifier cause must also appear")
}

func TestLink_StandardMode_UnsafeTarget_InvalidFormatWithCause(t *testing.T) {
	// Standard link (no --refresh / --adapter) is single-TC only, so it keeps the
	// historical generic message plus the classifier cause. No git/FS needed --
	// the traversal target is rejected at classification.
	restore := withExecuteGlobals(t, t.TempDir(), &config.Config{})
	defer restore()

	out, err := runCmdCaptureStderr(t, newLinkCmd(), `..\..\tc-abc12345`)
	require.Error(t, err)
	assert.Contains(t, out, "Invalid test case ID format")
	assert.Contains(t, out, "path traversal", "the specific classifier cause must also appear")
}

// --- Regression 2: nonexistent folder-shaped arg -> generic invalid-format ---

func TestExecute_NonexistentFolder_InvalidFormat(t *testing.T) {
	skipIfShort(t)
	root := setupCompatProject(t)
	restore := withExecuteGlobals(t, root, batsExecuteConfig())
	defer restore()

	out, err := runCmdCaptureStderr(t, newExecuteCmd(), "nonexistent-folder")
	require.Error(t, err)
	assert.Contains(t, out, "Invalid test case ID format", "nonexistent folder-shaped arg must fall through to single-TC (tc-13dbade0)")
	assert.NotContains(t, out, "does not exist", "must NOT enter bulk mode and report a missing folder")
}

func TestAutomate_NonexistentFolder_InvalidFormat(t *testing.T) {
	skipIfShort(t)
	root := setupCompatProject(t)
	restore := withExecuteGlobals(t, root, batsExecuteConfig())
	defer restore()

	out, err := runCmdCaptureStderr(t, newAutomateCmd(), "nonexistent-folder")
	require.Error(t, err)
	assert.Contains(t, out, "Invalid test case ID format")
	assert.NotContains(t, out, "does not exist")
}

// --- Regression 2 positive: an EXISTING mixed-case folder still enters bulk ---

func TestExecute_ExistingMixedCaseFolder_EntersBulk(t *testing.T) {
	skipIfShort(t)
	root := setupCompatProject(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/test/cases/FeatureA"), 0o755))
	restore := withExecuteGlobals(t, root, batsExecuteConfig())
	defer restore()

	// Empty folder -> bulk discovery reports "No test cases found", proving the
	// command entered bulk mode (case preserved) rather than the invalid-format path.
	out, err := runCmdCaptureStderr(t, newExecuteCmd(), "FeatureA")
	require.Error(t, err)
	assert.NotContains(t, out, "Invalid test case ID format")
	assert.Contains(t, out, "No test cases found", "an existing mixed-case folder must enter bulk mode")
}

func TestAutomate_ExistingMixedCaseFolder_EntersBulk(t *testing.T) {
	skipIfShort(t)
	root := setupCompatProject(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/test/cases/FeatureA"), 0o755))
	restore := withExecuteGlobals(t, root, batsExecuteConfig())
	defer restore()

	out, err := runCmdCaptureStderr(t, newAutomateCmd(), "FeatureA")
	require.Error(t, err)
	assert.NotContains(t, out, "Invalid test case ID format")
	assert.Contains(t, out, "No test cases found", "an existing mixed-case folder must enter bulk mode")
}
