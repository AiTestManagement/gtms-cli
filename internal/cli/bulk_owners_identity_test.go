package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/testcase"
)

func writeBulkSpec(t *testing.T, root, rel, id string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte("---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: "+id+"\ntitle: t\n---\n"), 0o644))
}

// BUG-188 / C1: the bulk automate/execute work-set is the body-identity owners,
// so an extended-id sibling (tc-x--legacy) is NOT truncated to tc-x and dropped.
// The old DiscoverTestCases/extractTestCaseID collapsed both to tc-x.
func TestResolveBulkOwners_ExtendedSiblingNotTruncated(t *testing.T) {
	root := t.TempDir()
	writeBulkSpec(t, root, "gtms/test/cases/checkout/tc-77777777-owner.md", "tc-77777777")
	writeBulkSpec(t, root, "gtms/test/cases/checkout/tc-77777777--legacy-sibling.md", "tc-77777777--legacy")

	owners, err := resolveBulkOwners(root, "checkout", false)
	require.NoError(t, err)
	ids := make([]string, len(owners))
	specs := make([]string, len(owners))
	for i, o := range owners {
		ids[i] = o.ID()
		specs[i] = o.SpecRel()
	}
	assert.ElementsMatch(t, []string{"tc-77777777", "tc-77777777--legacy"}, ids,
		"both distinct body-identity owners must be in the work-set, not one truncated id")
	// ADR-024 s5: the work-set carries each id's RESOLVED SPEC, so the bulk loop
	// hands the adapter a proven owner per iteration instead of a bare id it would
	// have to resolve again -- which is where the extended sibling was previously
	// collapsed onto its prefix.
	assert.ElementsMatch(t, []string{
		"gtms/test/cases/checkout/tc-77777777-owner.md",
		"gtms/test/cases/checkout/tc-77777777--legacy-sibling.md",
	}, specs, "each work-set entry must carry its own owning spec")
}

// C1: bulk automate/execute over a folder with a duplicate owner (second owner
// renamed, outside the folder) refuses before returning any work-set.
func TestResolveBulkOwners_RenamedDuplicateRefuses(t *testing.T) {
	root := t.TempDir()
	writeBulkSpec(t, root, "gtms/test/cases/alpha/tc-11111111-owner.md", "tc-11111111")
	writeBulkSpec(t, root, "gtms/test/cases/beta/tc-22222222-renamed.md", "tc-11111111")

	_, err := resolveBulkOwners(root, "alpha", false)
	require.Error(t, err)
	var oe *testcase.OwnershipError
	require.ErrorAs(t, err, &oe)
	assert.Equal(t, testcase.OutcomeAmbiguous, oe.Outcome)
}
