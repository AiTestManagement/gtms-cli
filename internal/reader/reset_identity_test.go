package reader

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/testcase"
)

// BUG-185 (tc-2ceabc97): reset is destructive by effect, so a folder whose
// selected id has a second owner OUTSIDE the folder refuses before removing any
// handoff or task file.
func TestReset_Folder_SecondOwnerOutsideScope_Refuses(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "tc-a1b2c3d4", "gtms/test/cases/alpha/tc-a1b2c3d4-first.md")
	writeSpec(t, root, "tc-a1b2c3d4", "gtms/test/cases/beta/tc-deadbeef-second-owner.md")
	handoff := writeHandoff(t, root, "task-77777777", "tc-a1b2c3d4")

	_, err := ResetExecuteResults(root, scopeForFolder(root, "alpha", false), "", false)
	require.Error(t, err)
	var oe *testcase.OwnershipError
	require.ErrorAs(t, err, &oe)
	assert.Equal(t, testcase.OutcomeAmbiguous, oe.Outcome)

	// No mutation: the handoff that reset would have cleared remains.
	_, statErr := os.Stat(handoff)
	assert.NoError(t, statErr, "reset must not clear state after refusing")
}

// BUG-185 (tc-2ceabc97 step 5): a unique-owner control still resets (the guard
// refuses the ambiguous id specifically, not everything).
func TestReset_Folder_UniqueOwner_Succeeds(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "tc-99999999", "gtms/test/cases/control/tc-99999999-only.md")
	writeHandoff(t, root, "task-88888888", "tc-99999999")

	res, err := ResetExecuteResults(root, scopeForFolder(root, "control", false), "", true)
	require.NoError(t, err)
	assert.Equal(t, 1, res.AutomationRecordsCleared)
}

// Reset of a folder holding an Unprovable spec refuses before any mutation.
func TestReset_Folder_Unprovable_Refuses(t *testing.T) {
	root := t.TempDir()
	spec := filepath.Join(root, "gtms/test/cases/unprovable/tc-deadbeef-missing-id.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(spec), 0o755))
	require.NoError(t, os.WriteFile(spec, []byte("---\ntitle: no id\n---\nbody\n"), 0o644))
	writeHandoff(t, root, "task-99999999", "tc-deadbeef")

	_, err := ResetExecuteResults(root, scopeForFolder(root, "unprovable", false), "", false)
	require.Error(t, err)
	var oe *testcase.OwnershipError
	require.ErrorAs(t, err, &oe)
	assert.Equal(t, testcase.OutcomeUnprovable, oe.Outcome)
}
