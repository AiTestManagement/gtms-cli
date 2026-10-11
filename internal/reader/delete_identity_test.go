package reader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/testcase"
)

// writeHandoff seeds a terminal handoff whose target is the bare TC id.
func writeHandoff(t *testing.T, root, taskID, tcID string) string {
	t.Helper()
	dir := filepath.Join(root, ".gtms", "results")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, taskID+".handoff.yaml")
	body := "task: " + taskID + "\ncommand: execute\ntarget: " + tcID + "\nstatus: complete\nresult: pass\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}

func scopeForFolder(root, folder string, recursive bool) *ScopeInfo {
	return &ScopeInfo{
		ScanDir:   filepath.Join(root, "gtms", "test", "cases", folder),
		RelPath:   "gtms/test/cases/" + folder + "/",
		Recursive: recursive,
	}
}

// BUG-185 (tc-81893058, bare form): a renamed second owner whose filename does
// not shortlist the id is still found by body identity, so a bare-id delete
// refuses as Ambiguous and mutates nothing.
func TestDelete_BareID_RenamedDuplicateOwner_Refuses(t *testing.T) {
	root := t.TempDir()
	a := writeSpec(t, root, "tc-a1b2c3d4", "gtms/test/cases/alpha/tc-a1b2c3d4-first.md")
	b := writeSpec(t, root, "tc-a1b2c3d4", "gtms/test/cases/beta/tc-deadbeef-renamed-owner.md")

	_, err := DeleteArtifacts(root, nil, "tc-a1b2c3d4", false, false, false)
	require.Error(t, err)
	var oe *testcase.OwnershipError
	require.ErrorAs(t, err, &oe)
	assert.Equal(t, testcase.OutcomeAmbiguous, oe.Outcome)
	assert.Len(t, oe.Owners, 2)
	// Renamed owner named despite its filename not shortlisting the id.
	assert.Contains(t, strings.Join(oe.Owners, "|"), "tc-deadbeef-renamed-owner.md")
	assertPresent(t, a, b)
}

// BUG-185 (tc-81893058 step 3, headline): deleting a FOLDER refuses because a
// selected id has a second owner OUTSIDE the folder -- only a project-wide lookup
// can see it.
func TestDelete_Folder_SecondOwnerOutsideScope_Refuses(t *testing.T) {
	root := t.TempDir()
	a := writeSpec(t, root, "tc-a1b2c3d4", "gtms/test/cases/alpha/tc-a1b2c3d4-first.md")
	b := writeSpec(t, root, "tc-a1b2c3d4", "gtms/test/cases/beta/tc-deadbeef-renamed-owner.md")

	_, err := DeleteArtifacts(root, scopeForFolder(root, "alpha", false), "", false, false, false)
	require.Error(t, err)
	var oe *testcase.OwnershipError
	require.ErrorAs(t, err, &oe)
	assert.Equal(t, testcase.OutcomeAmbiguous, oe.Outcome)
	assertPresent(t, a, b)
}

// BUG-185: a project-wide-unique id in the same shape still deletes (positive
// control -- the guard refuses the ambiguous id specifically, not everything).
func TestDelete_Folder_UniqueOwner_Succeeds(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "tc-99999999", "gtms/test/cases/solo/tc-99999999-only.md")

	res, err := DeleteArtifacts(root, scopeForFolder(root, "solo", false), "", false, false, true)
	require.NoError(t, err)
	assert.Equal(t, 1, res.TestCaseSpecsRemoved)
}

// BUG-188 (tc-f7d8f7c1 in Go): a delete plan for the shorter id contains only its
// own spec, wiring and handoff -- never the `--legacy` sibling's, discovered
// through the old filename glob and target substring.
func TestDelete_PrefixSibling_PlanExcludesSibling(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "tc-77777777", "gtms/test/cases/checkout/tc-77777777-owner.md")
	writeSpec(t, root, "tc-77777777--legacy", "gtms/test/cases/checkout/tc-77777777--legacy-sibling.md")
	writeWiring(t, root, "tc-77777777", "test/acceptance/checkout/tc-77777777-owner.bats")
	writeWiring(t, root, "tc-77777777--legacy", "test/acceptance/checkout/tc-77777777--legacy-sibling.bats")
	writeHandoff(t, root, "task-11111111", "tc-77777777")
	writeHandoff(t, root, "task-22222222", "tc-77777777--legacy")

	res, err := DeleteArtifacts(root, nil, "tc-77777777", false, false, true)
	require.NoError(t, err)

	assert.Equal(t, 1, res.TestCaseSpecsRemoved, "only the owner spec")
	assert.Equal(t, 1, res.AutomationRecords, "only the owner wiring")
	assert.Equal(t, 1, res.ResultContracts, "only the owner handoff")
	for _, f := range res.FilesDeleted {
		assert.NotContains(t, f, "--legacy", "the sibling's files must never enter the plan")
	}
}

// BUG-185 Unprovable (tc-81893058 step 12): a folder containing a spec with no
// test_case_id refuses (Unprovable) before any mutation.
func TestDelete_Folder_UnprovableCandidate_Refuses(t *testing.T) {
	root := t.TempDir()
	spec := filepath.Join(root, "gtms/test/cases/unprovable/tc-deadbeef-missing-id.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(spec), 0o755))
	require.NoError(t, os.WriteFile(spec, []byte("---\ntitle: no id\nrequirement: X\n---\nbody\n"), 0o644))

	_, err := DeleteArtifacts(root, scopeForFolder(root, "unprovable", false), "", false, false, false)
	require.Error(t, err)
	var oe *testcase.OwnershipError
	require.ErrorAs(t, err, &oe)
	assert.Equal(t, testcase.OutcomeUnprovable, oe.Outcome)
	assert.Equal(t, testcase.ReasonMissingID, oe.Reason)
	assertPresent(t, spec)
}

func assertPresent(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		_, err := os.Stat(p)
		assert.NoError(t, err, "must remain after a refusal: %s", p)
	}
}

// Outcome 3 (coordinator): a relevant spec whose identity cannot be established
// (missing test_case_id) must REACH the ownership decision and CAUSE the refusal
// -- it is never omitted as an out-of-scope record. This asserts the causal chain,
// not a diagnostic string: the operation returns a typed Unprovable OwnershipError
// whose named candidate is THAT exact record's path, and repairing that one record
// makes the same operation succeed. Mutation-checked (see the SelectFolder guard).
func TestDelete_Folder_UnprovableSpec_CausalChain(t *testing.T) {
	root := t.TempDir()
	specRel := "gtms/test/cases/unprovable/tc-b2c3d4e5-no-identity.md"
	specAbs := filepath.Join(root, filepath.FromSlash(specRel))
	require.NoError(t, os.MkdirAll(filepath.Dir(specAbs), 0o755))
	require.NoError(t, os.WriteFile(specAbs, []byte("---\ntitle: no id\nrequirement: X\n---\nbody\n"), 0o644))

	_, err := DeleteArtifacts(root, scopeForFolder(root, "unprovable", false), "", false, false, true)
	require.Error(t, err, "the unprovable spec must stop the operation, not be silently dropped")

	var oe *testcase.OwnershipError
	require.ErrorAs(t, err, &oe)
	assert.Equal(t, testcase.OutcomeUnprovable, oe.Outcome, "the refusal is caused by unprovable ownership")
	assert.Equal(t, testcase.ReasonMissingID, oe.Reason)
	assert.Contains(t, oe.Path, "tc-b2c3d4e5-no-identity.md",
		"the named candidate is THAT exact record -- proving it was seen and could not be attributed")

	// Repair that one record: the SAME operation now succeeds. This is the other
	// half of the causal chain -- the refusal was caused by this record, not by an
	// unrelated failure.
	require.NoError(t, os.WriteFile(specAbs, []byte("---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-b2c3d4e5\ntitle: fixed\n---\nbody\n"), 0o644))
	_, err = DeleteArtifacts(root, scopeForFolder(root, "unprovable", false), "", false, false, true)
	require.NoError(t, err, "repairing the one unprovable record makes the operation succeed")
}

// BUG-188 Gap 1 (tc-f7d8f7c1 step 6): deleting the EXTENDED id plans its own 3
// files -- spec, its own wiring record (tc-x--legacy--bats.wiring.yaml), and its
// handoff -- which the first-"--" filename split previously dropped.
func TestDelete_ExtendedID_IncludesOwnWiring(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "tc-77777777", "gtms/test/cases/checkout/tc-77777777-owner.md")
	writeSpec(t, root, "tc-77777777--legacy", "gtms/test/cases/checkout/tc-77777777--legacy-sibling.md")
	writeWiring(t, root, "tc-77777777", "test/acceptance/checkout/a.bats")
	writeWiring(t, root, "tc-77777777--legacy", "test/acceptance/checkout/b.bats")
	writeHandoff(t, root, "task-11111111", "tc-77777777")
	writeHandoff(t, root, "task-22222222", "tc-77777777--legacy")

	res, err := DeleteArtifacts(root, nil, "tc-77777777--legacy", false, false, true)
	require.NoError(t, err)
	assert.Equal(t, 1, res.TestCaseSpecsRemoved, "the extended id's own spec")
	assert.Equal(t, 1, res.AutomationRecords, "the extended id's OWN wiring record")
	assert.Equal(t, 1, res.ResultContracts, "the extended id's own handoff")
	for _, f := range res.FilesDeleted {
		assert.NotContains(t, f, "tc-77777777-owner", "the shorter sibling's files must not be planned")
		assert.NotContains(t, f, "/a.bats", "the shorter sibling's wiring must not be planned")
	}
}

// BUG-188 Gap 1 (tc-85334613): with both siblings referencing one shared artefact
// via distinct wiring records, a forced delete of the shorter id no longer aborts
// on an unparseable extended-sibling wiring record, and preserves the artefact
// (the extended sibling still references it, outside the deletion set).
func TestDelete_ExtendedSiblingSharedArtefact_ForcePreserves(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "tc-77777777", "gtms/test/cases/checkout/tc-77777777-owner.md")
	writeSpec(t, root, "tc-77777777--legacy", "gtms/test/cases/checkout/tc-77777777--legacy-sibling.md")
	shared := "test/acceptance/shared/identity-shared.bats"
	sharedAbs := filepath.Join(root, filepath.FromSlash(shared))
	require.NoError(t, os.MkdirAll(filepath.Dir(sharedAbs), 0o755))
	require.NoError(t, os.WriteFile(sharedAbs, []byte("# shared"), 0o644))
	writeWiring(t, root, "tc-77777777", shared)
	writeWiring(t, root, "tc-77777777--legacy", shared)

	res, err := DeleteArtifacts(root, nil, "tc-77777777", false, true, true)
	require.NoError(t, err, "the extended sibling's wiring is parseable now; force must not abort")
	var preserved bool
	for _, p := range res.PreservedArtefacts {
		if strings.Contains(p.Path, "identity-shared.bats") {
			preserved = true
		}
	}
	assert.True(t, preserved, "the shared artefact is referenced by the extended sibling, so it is preserved")
}
