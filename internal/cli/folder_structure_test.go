package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
)

// BUG-170: the create/automate/prime folder-structure validators must NOT
// require the disposable gtms/tasks/ directory (ADR-011 derived state), but
// must still hard-error on absent committed source dirs (gtms/test/cases/,
// gtms/automation/). execute reuses validateAutomateFolderStructure, so its
// behaviour is pinned by the automate cases here.
//
// The stable assertion contract (BUG-170): specs and these tests key on the
// "Required directory '<dir>' not found." message line only; hints are free.

// mkTree creates each of the given layout-relative directories under root.
func mkTree(t *testing.T, root string, dirs ...string) {
	t.Helper()
	for _, dir := range dirs {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0o755))
	}
}

// TestValidateFolderStructure_ToleratesMissingTasksDir_BUG170 verifies all
// three validators pass when gtms/tasks/ is absent but the committed source
// dirs are present.
func TestValidateFolderStructure_ToleratesMissingTasksDir_BUG170(t *testing.T) {
	paths := layout.Current()

	t.Run("create", func(t *testing.T) {
		root := t.TempDir()
		mkTree(t, root, paths.TestCases) // no gtms/tasks
		assert.NoError(t, validateFolderStructure(root))
		assertAbsent(t, root, paths.Tasks)
	})

	t.Run("automate", func(t *testing.T) {
		root := t.TempDir()
		mkTree(t, root, paths.TestCases, paths.Automation) // no gtms/tasks
		assert.NoError(t, validateAutomateFolderStructure(root))
		assertAbsent(t, root, paths.Tasks)
	})

	t.Run("prime", func(t *testing.T) {
		root := t.TempDir()
		mkTree(t, root, paths.TestCases) // no gtms/tasks
		assert.NoError(t, validatePrimeFolderStructure(root))
		assertAbsent(t, root, paths.Tasks)
	})
}

// TestValidateFolderStructure_MissingTestCasesStillErrors_BUG170 is the
// negative control: absence of the committed gtms/test/cases/ source dir must
// still fail every validator with the named-directory message.
func TestValidateFolderStructure_MissingTestCasesStillErrors_BUG170(t *testing.T) {
	paths := layout.Current()
	wantMsg := "Required directory '" + paths.TestCases + "' not found."

	t.Run("create", func(t *testing.T) {
		root := t.TempDir()
		mkTree(t, root, paths.Tasks) // tasks present, test/cases absent
		var err error
		stderr := captureStderr(t, func() { err = validateFolderStructure(root) })
		assert.Error(t, err)
		assert.Contains(t, stderr, wantMsg)
	})

	t.Run("automate", func(t *testing.T) {
		root := t.TempDir()
		mkTree(t, root, paths.Tasks, paths.Automation) // test/cases absent
		var err error
		stderr := captureStderr(t, func() { err = validateAutomateFolderStructure(root) })
		assert.Error(t, err)
		assert.Contains(t, stderr, wantMsg)
	})

	t.Run("prime", func(t *testing.T) {
		root := t.TempDir()
		mkTree(t, root, paths.Tasks) // test/cases absent
		var err error
		stderr := captureStderr(t, func() { err = validatePrimeFolderStructure(root) })
		assert.Error(t, err)
		assert.Contains(t, stderr, wantMsg)
	})
}

// TestValidateAutomateFolderStructure_MissingAutomationStillErrors_BUG170 is
// the second negative control: gtms/automation/ is committed source and its
// absence must still fail the automate/execute validator. Easy to drop by
// accident when editing the shared required list.
func TestValidateAutomateFolderStructure_MissingAutomationStillErrors_BUG170(t *testing.T) {
	paths := layout.Current()
	root := t.TempDir()
	mkTree(t, root, paths.TestCases) // automation absent, tasks absent
	var err error
	stderr := captureStderr(t, func() { err = validateAutomateFolderStructure(root) })
	assert.Error(t, err)
	assert.Contains(t, stderr, "Required directory '"+paths.Automation+"' not found.")
}

// TestValidateFolderStructure_PreflightCreatesNothing_BUG170 verifies the
// tolerant preflight does not mutate the filesystem: after a passing call with
// gtms/tasks/ absent, the directory is still absent (no eager creation).
func TestValidateFolderStructure_PreflightCreatesNothing_BUG170(t *testing.T) {
	paths := layout.Current()
	root := t.TempDir()
	mkTree(t, root, paths.TestCases, paths.Automation)

	require.NoError(t, validateFolderStructure(root))
	require.NoError(t, validateAutomateFolderStructure(root))
	require.NoError(t, validatePrimeFolderStructure(root))

	assertAbsent(t, root, paths.Tasks)
}

// assertAbsent fails if the layout-relative dir exists under root.
func assertAbsent(t *testing.T, root, dir string) {
	t.Helper()
	_, err := os.Stat(filepath.Join(root, dir))
	assert.True(t, os.IsNotExist(err), "expected %s to be absent, stat err = %v", dir, err)
}
