package reader

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeDoc drops an ordinary Markdown document (no test_case_id) into a cases
// folder -- the shape a brownfield project routinely has.
func writeDoc(t *testing.T, root, relPath, body string) string {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(relPath))
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.WriteFile(abs, []byte(body), 0o644))
	return abs
}

// REV-158 CLAUDE-002 at the consumer boundary: a README.md beside a valid spec
// must not refuse folder-scoped delete or reset. Before the fix every *.md under
// gtms/test/cases/ was admitted as a spec candidate, so a README parsed cleanly,
// declared no test_case_id, became Unprovable, and made the whole folder
// operation exit 1 with "ownership of test case ID '.../README.md' cannot be
// proved". There was no flag and no opt-out.
func TestFolderMutations_OrdinaryDocumentationDoesNotRefuse(t *testing.T) {
	for _, doc := range []string{"README.md", "NOTES.md"} {
		t.Run(doc, func(t *testing.T) {
			root := t.TempDir()
			spec := writeSpec(t, root, "tc-11111111", "gtms/test/cases/feat/tc-11111111-login.md")
			readme := writeDoc(t, root, "gtms/test/cases/feat/"+doc, "# Feat\n\nHow this folder works.\n")

			// reset (destructive by effect) must plan, not refuse.
			_, rerr := ResetExecuteResults(root, scopeForFolder(root, "feat", false), "", true)
			require.NoError(t, rerr, "%s must not refuse a folder reset", doc)

			// delete must plan and remove exactly the spec, leaving the document.
			res, derr := DeleteArtifacts(root, scopeForFolder(root, "feat", false), "", false, false, false)
			require.NoError(t, derr, "%s must not refuse a folder delete", doc)
			assert.Equal(t, 1, res.TestCasesProcessed)

			_, err := os.Stat(spec)
			assert.Error(t, err, "the spec should have been deleted")
			assertPresent(t, readme)
		})
	}
}

// The positive control for the same rule: a spec-shaped file with no identity
// still refuses the folder operation. Narrowing admission must not make
// malformed specs invisible (ADR-024's accepted brownfield cost).
func TestFolderMutations_SpecShapedFileWithoutIDStillRefuses(t *testing.T) {
	root := t.TempDir()
	spec := writeSpec(t, root, "tc-11111111", "gtms/test/cases/feat/tc-11111111-login.md")
	broken := writeDoc(t, root, "gtms/test/cases/feat/tc-22222222-broken.md", "---\ntitle: t\n---\nbody\n")

	_, err := DeleteArtifacts(root, scopeForFolder(root, "feat", false), "", false, false, false)
	require.Error(t, err, "a tc-named spec declaring no id must refuse the whole plan")
	assertPresent(t, spec, broken)
}
