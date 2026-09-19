package reader

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/testcase"
)

// BUG-185/BUG-188 (tc-5b0666ce, tc-81893058 step 17): direct single-TC read
// refuses an Ambiguous id and names its owners, and surfaces Unprovable as its
// own outcome -- it never presents one arbitrary owner as fact.
func TestPipelineDetail_AmbiguousID_Refuses(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "tc-a1b2c3d4", "gtms/test/cases/alpha/tc-a1b2c3d4-first.md")
	writeSpec(t, root, "tc-a1b2c3d4", "gtms/test/cases/beta/tc-a1b2c3d4-second.md")

	_, err := PipelineDetail(root, "tc-a1b2c3d4", "", false)
	require.Error(t, err)
	var oe *testcase.OwnershipError
	require.ErrorAs(t, err, &oe)
	assert.Equal(t, testcase.OutcomeAmbiguous, oe.Outcome)
	assert.Len(t, oe.Owners, 2)
}

func TestPipelineDetail_UnprovableID_Refuses(t *testing.T) {
	root := t.TempDir()
	spec := filepath.Join(root, "gtms/test/cases/unprovable/tc-deadbeef-missing-id.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(spec), 0o755))
	require.NoError(t, os.WriteFile(spec, []byte("---\ntitle: no id\n---\nbody\n"), 0o644))

	_, err := PipelineDetail(root, "tc-deadbeef", "", false)
	require.Error(t, err)
	var oe *testcase.OwnershipError
	require.ErrorAs(t, err, &oe)
	assert.Equal(t, testcase.OutcomeUnprovable, oe.Outcome)
	assert.Equal(t, testcase.ReasonMissingID, oe.Reason)
}

// Prefix sibling still resolves for status (positive control): tc-77777777
// reads its own owner even though tc-77777777--legacy is present.
func TestPipelineDetail_PrefixSibling_ResolvesOwner(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "tc-77777777", "gtms/test/cases/checkout/tc-77777777-owner.md")
	writeSpec(t, root, "tc-77777777--legacy", "gtms/test/cases/checkout/tc-77777777--legacy-sibling.md")

	d, err := PipelineDetail(root, "tc-77777777", "", false)
	require.NoError(t, err)
	assert.Equal(t, "tc-77777777", d.TestCaseID)
}
