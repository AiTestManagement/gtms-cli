package reader

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// REV-160 CLAUDE-002 / CODEX-003 -- a nil scope means "the whole cases tree".
//
// This branch replaced scanTestCases(projectRoot, nil) -- which walked the tree
// recursively -- with snap.SelectFolder(casesRoot, scope != nil && scope.Recursive).
// For a nil scope that second argument is false, so selection became a SHALLOW
// scan of gtms/test/cases/ and every nested spec was silently skipped while the
// call still reported success.
//
// The pre-existing nil-scope test only asserted "does not panic", which is why
// the narrowing went undetected. This one seeds a NESTED spec with resettable
// state and asserts it is selected and cleared, so restoring the shallow flag
// fails the test.
func TestResetExecuteResults_NilScopeSelectsNestedSpecs(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "tc-11111111", "gtms/test/cases/tc-11111111-at-root.md")
	writeSpec(t, root, "tc-22222222", "gtms/test/cases/deep/tc-22222222-one-level.md")
	writeSpec(t, root, "tc-33333333", "gtms/test/cases/deep/deeper/tc-33333333-two-levels.md")

	rootHandoff := writeHandoff(t, root, "task-aaaaaaaa", "tc-11111111")
	deepHandoff := writeHandoff(t, root, "task-bbbbbbbb", "tc-22222222")
	deeperHandoff := writeHandoff(t, root, "task-cccccccc", "tc-33333333")

	// Dry run first: the COUNTS alone prove the selection breadth.
	dry, err := ResetExecuteResults(root, nil, "", true)
	require.NoError(t, err)
	assert.Equal(t, 3, dry.TestCasesAffected,
		"a nil scope must select the whole cases tree; a shallow scan of gtms/test/cases/ finds only the root-level spec")
	assert.Equal(t, 3, dry.AutomationRecordsCleared)

	// And the real run must actually clear the nested state.
	real, err := ResetExecuteResults(root, nil, "", false)
	require.NoError(t, err)
	assert.Equal(t, 3, real.TestCasesAffected)

	for _, h := range []string{rootHandoff, deepHandoff, deeperHandoff} {
		_, statErr := os.Stat(h)
		assert.True(t, os.IsNotExist(statErr),
			"handoff %s survived a whole-tree reset: %v", h, statErr)
	}
}
