package reader

// REV-184 regression coverage for the ENH-205 read-path migration review.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/testcase"
)

func folderScope(root, folder string) *ScopeInfo {
	return &ScopeInfo{
		ScanDir:   filepath.Join(root, "gtms", "test", "cases", folder),
		RelPath:   "gtms/test/cases/" + folder + "/",
		Recursive: false,
	}
}

// outOfScopeUnprovableTwin writes a proven owner of tc-abc12345 under a/ and an
// unreadable candidate (no test_case_id) under b/ whose FILENAME matches that id.
func outOfScopeUnprovableTwin(t *testing.T) string {
	t.Helper()
	root := idProject(t)
	writeFile(t, root, "gtms/test/cases/a/tc-abc12345-login.md",
		"---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-abc12345\ntitle: Login\nrequirement: REQ-1\n---\nbody\n")
	writeFile(t, root, "gtms/test/cases/b/tc-abc12345-copy.md",
		"---\ntitle: stray copy\n---\nbody\n")
	return root
}

// REV-184 CLAUDE-001: an in-scope owner must never be silently dropped because an
// unreadable file OUTSIDE the scope has a matching filename. ADR-024 5b: a bulk
// operation's relevance is the scope arm only, so the out-of-scope candidate does
// not affect the operation and the owner is rendered -- neither dropped nor
// reported as absence.
func TestREV184_ScopedBulkRead_OutOfScopeUnprovableTwin_OwnerRendered(t *testing.T) {
	root := outOfScopeUnprovableTwin(t)

	specs, diags, err := resolveScopedSpecs(mustSnapshot(t, root), root, folderScope(root, "a"))
	require.NoError(t, err)
	assert.Equal(t, []string{"tc-abc12345"}, specIDs(specs),
		"the proven in-scope owner must be rendered, not dropped as a silent absence")
	assert.False(t, diags.Affected(), "an out-of-scope Unprovable candidate does not affect a scoped bulk read (ADR-024 5b)")

	entries, sdiags, err := PipelineStatusWithDiagnostics(root, folderScope(root, "a"), "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "tc-abc12345", entries[0].TestCaseID)
	assert.False(t, sdiags.Affected())

	// The whole-tree read has the candidate in scope: it is diagnosed, and the
	// owner is still rendered.
	all, rdiags, err := resolveScopedSpecs(mustSnapshot(t, root), root, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"tc-abc12345"}, specIDs(all))
	assert.True(t, rdiags.Affected())
	assert.Contains(t, diagText(rdiags), "gtms/test/cases/b/tc-abc12345-copy.md")
}

// REV-184 CLAUDE-001, triage arm: ADR-024 5b point 8 resolves each triage record's
// ID with PER-ID relevance, so the same twin makes tc-abc12345 Unprovable for the
// folder triage listing. Its record is left out AND the identity is diagnosed --
// Affected() is true and the command exits 1 -- never a silent absence.
func TestREV184_FolderTriageList_PerIDUnprovable_DiagnosedNotDropped(t *testing.T) {
	root := outOfScopeUnprovableTwin(t)
	putTriageRecord(t, root, "tc-abc12345", "bats", "environment", "fail", "")

	views, diags, err := ListTriageWithDiagnostics(root, folderScope(root, "a"), "", false)
	require.NoError(t, err)
	assert.Empty(t, views, "a record whose ID is Unprovable per-id cannot be attributed and is left out")
	assert.True(t, diags.Affected(), "the left-out identity must be diagnosed, not rendered as absence")
	assert.Contains(t, diagText(diags), "tc-abc12345-copy.md")
	assert.Contains(t, diagText(diags), "tc-abc12345")
}

// REV-184 CLAUDE-001: an in-scope Unprovable candidate that is ALSO the per-id
// Unprovable resolution of a listed ID is named once, not twice.
func TestREV184_FolderTriageList_InScopeUnprovableNamedOnce(t *testing.T) {
	root := idProject(t)
	writeFile(t, root, "gtms/test/cases/a/tc-abc12345-login.md",
		"---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-abc12345\ntitle: Login\n---\nbody\n")
	writeFile(t, root, "gtms/test/cases/a/tc-abc12345-copy.md", "---\ntitle: stray\n---\nbody\n")
	putTriageRecord(t, root, "tc-abc12345", "bats", "environment", "fail", "")

	_, diags, err := ListTriageWithDiagnostics(root, folderScope(root, "a"), "", false)
	require.NoError(t, err)
	assert.Equal(t, 1, diags.count(), "one affected file, one diagnostic")
}

// REV-184 CODEX-001: map <tc-id> must decide an ambiguous sibling's group
// membership from `requirement` alone. A wrong-type unrelated field (priority) on
// the in-group owner used to make the owner unreadable, so the ambiguous ID was
// dropped from the printed group's diagnostics and the command exited 0.
func TestREV184_MapTarget_AmbiguousSiblingWrongTypePriority_Surfaced(t *testing.T) {
	root := idProject(t)
	writeFile(t, root, "gtms/test/cases/a/tc-11111111-target.md",
		"---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-11111111\ntitle: Target\nrequirement: REQ-1\n---\n")
	// Ambiguous tc-22222222: one owner in REQ-1 with a wrong-type priority, the
	// other in a different group.
	writeFile(t, root, "gtms/test/cases/a/tc-22222222-dup.md",
		"---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-22222222\ntitle: Dup\nrequirement: REQ-1\npriority: [high, low]\n---\n")
	writeFile(t, root, "gtms/test/cases/b/tc-22222222-dup.md",
		"---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-22222222\ntitle: Dup\nrequirement: REQ-2\n---\n")

	_, targetErr, diags, err := MapTargetDiagnostics(root, "tc-11111111", "", false)
	require.NoError(t, err)
	require.NoError(t, targetErr)
	assert.True(t, diags.Affected(), "an ambiguous ID with an in-group owner must be surfaced")
	assert.Contains(t, diagText(diags), "tc-22222222")
}

// REV-184 CODEX-001: an owner whose requirement itself cannot be read cannot be
// shown to be outside the printed group, so the ambiguous ID is surfaced.
func TestREV184_MapTarget_AmbiguousSiblingUnreadableRequirement_Surfaced(t *testing.T) {
	root := idProject(t)
	writeFile(t, root, "gtms/test/cases/a/tc-11111111-target.md",
		"---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-11111111\ntitle: Target\nrequirement: REQ-1\n---\n")
	writeFile(t, root, "gtms/test/cases/a/tc-22222222-dup.md",
		"---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-22222222\ntitle: Dup\nrequirement: [REQ-9]\n---\n")
	writeFile(t, root, "gtms/test/cases/b/tc-22222222-dup.md",
		"---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-22222222\ntitle: Dup\nrequirement: REQ-2\n---\n")

	_, targetErr, diags, err := MapTargetDiagnostics(root, "tc-11111111", "", false)
	require.NoError(t, err)
	require.NoError(t, targetErr)
	assert.True(t, diags.Affected())
}

func TestMapTarget_AmbiguousSiblingLaterUnreadableRequirement_Surfaced(t *testing.T) {
	root := idProject(t)
	writeFile(t, root, "gtms/test/cases/a/tc-11111111-target.md",
		"---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-11111111\ntitle: Target\nrequirement: REQ-1\n---\n")
	writeFile(t, root, "gtms/test/cases/a/tc-22222222-dup.md",
		"---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-22222222\ntitle: [Unreadable]\nrequirement: [REQ-9]\n---\n")
	writeFile(t, root, "gtms/test/cases/b/tc-22222222-dup.md",
		"---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-22222222\ntitle: Dup\nrequirement: REQ-2\n---\n")

	_, targetErr, diags, err := MapTargetDiagnostics(root, "tc-11111111", "", false)
	require.NoError(t, err)
	require.NoError(t, targetErr)
	assert.True(t, diags.Affected())
	assert.Contains(t, diagText(diags), "tc-22222222")
}

// REV-184 CODEX-001 control, updated 2026-09-27: ADR-025 section 5 makes
// wrong-type priority a whole-document schema failure. Under ADR-024 section
// 4a, section 5b's "Display-field decode failure" paragraph, and Fixed Decision
// 9, strict v2 maps it to Unprovable and surfaces this out-of-group owner where
// the legacy window did not (T15 manager ruling).
func TestREV184_MapTarget_AmbiguousOutsideGroupWrongTypeField_IDSurfacedUnderV2(t *testing.T) {
	root := idProject(t)
	writeFile(t, root, "gtms/test/cases/a/tc-11111111-target.md",
		"---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-11111111\ntitle: Target\nrequirement: REQ-1\n---\n")
	writeFile(t, root, "gtms/test/cases/a/tc-22222222-dup.md",
		"---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-22222222\ntitle: Dup\nrequirement: REQ-3\npriority: [x]\n---\n")
	writeFile(t, root, "gtms/test/cases/b/tc-22222222-dup.md",
		"---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-22222222\ntitle: Dup\nrequirement: REQ-2\n---\n")

	_, targetErr, diags, err := MapTargetDiagnostics(root, "tc-11111111", "", false)
	require.NoError(t, err)
	require.NoError(t, targetErr)
	assert.True(t, diags.Affected())
	expected := string([]byte{0xe2, 0x9c, 0x97}) + " ownership of test case ID 'tc-22222222' cannot be proved from gtms/test/cases/a/tc-22222222-dup.md: set 'priority' to its declared scalar in gtms/test/cases/a/tc-22222222-dup.md, or remove the file."
	assert.Contains(t, strings.Split(strings.TrimSuffix(diagText(diags), "\n"), "\n"), expected)
}

// REV-184 CODEX-004: every diagnostics-free bulk entry point returns the typed
// incomplete-read error -- and no rows -- when an identity is affected, instead of
// partial rows as success.
func TestREV184_DiagnosticsFreeEntryPoints_RefusePartialResults(t *testing.T) {
	root := idProject(t)
	writeFile(t, root, "gtms/test/cases/a/tc-11111111-ok.md",
		"---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-11111111\ntitle: OK\nrequirement: REQ-1\n---\n")
	writeFile(t, root, "gtms/test/cases/a/tc-22222222-x.md",
		"---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-22222222\ntitle: X\n---\n")
	writeFile(t, root, "gtms/test/cases/b/tc-22222222-y.md",
		"---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-22222222\ntitle: Y\n---\n")

	checks := map[string]func() (bool, error){
		"PipelineStatus": func() (bool, error) {
			v, err := PipelineStatus(root, nil, "", false)
			return v == nil, err
		},
		"PipelineFolderSummary": func() (bool, error) {
			v, err := PipelineFolderSummary(root, "")
			return v == nil, err
		},
		"FolderTriagedCounts": func() (bool, error) {
			v, err := FolderTriagedCounts(root, "")
			return v == nil, err
		},
		"Map": func() (bool, error) {
			v, err := Map(root, nil, "", false)
			return v == nil, err
		},
		"Gaps": func() (bool, error) {
			v, err := Gaps(root, nil, "", false)
			return v == nil, err
		},
		"GapsFolderSummary": func() (bool, error) {
			v, err := GapsFolderSummary(root, "")
			return v == nil, err
		},
		"ListTriage": func() (bool, error) {
			v, err := ListTriage(root, nil, "", false)
			return v == nil, err
		},
	}
	for name, call := range checks {
		empty, err := call()
		var incomplete *IncompleteReadError
		if assert.ErrorAsf(t, err, &incomplete, "%s must return *IncompleteReadError", name) {
			assert.Containsf(t, err.Error(), "tc-22222222", "%s error must name the affected identity", name)
		}
		assert.Truef(t, empty, "%s must not return partial rows alongside the error", name)
	}
}

// REV-184 CODEX-004: ListTriageForTC enforces single-target ownership itself --
// an Ambiguous or Unprovable bare ID is refused with the typed OwnershipError, and
// NotFound (an orphan record) still lists.
func TestREV184_ListTriageForTC_EnforcesOwnership(t *testing.T) {
	root := idProject(t)
	writeFile(t, root, "gtms/test/cases/a/tc-22222222-x.md", "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-22222222\ntitle: X\n---\n")
	writeFile(t, root, "gtms/test/cases/b/tc-22222222-y.md", "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-22222222\ntitle: Y\n---\n")
	writeFile(t, root, "gtms/test/cases/a/tc-33333333-bad.md", "---\ntitle: no id\n---\n")
	putTriageRecord(t, root, "tc-22222222", "bats", "environment", "fail", "")
	putTriageRecord(t, root, "tc-33333333", "bats", "environment", "fail", "")
	putTriageRecord(t, root, "tc-44444444", "bats", "environment", "fail", "")

	for _, id := range []string{"tc-22222222", "tc-33333333"} {
		views, err := ListTriageForTC(root, id, "", false)
		var oe *testcase.OwnershipError
		assert.ErrorAsf(t, err, &oe, "%s must be refused inside the reader", id)
		assert.Nilf(t, views, "%s refusal returns no views", id)
	}

	orphan, err := ListTriageForTC(root, "tc-44444444", "", false)
	require.NoError(t, err)
	require.Len(t, orphan, 1)
	assert.True(t, orphan[0].SpecMissing)
}

// REV-184 round 2 (manager point (a)): `map <tc-id>` keeps the SINGLE-TC tree-wide
// rule for Unprovable candidates (ADR-024 5b "Single-TC forms"; previously pinned
// only by BATS tc-a99b5902). An Unprovable candidate outside the target's folder
// and outside its printed group still makes the read Affected -- its group
// membership cannot be ruled out -- while the target's group is still returned.
// Contrast arm: an Ambiguous ID whose owners are all outside the printed group
// does NOT affect map <tc-id>.
func TestREV184_MapSingleTC_TreeWideUnprovable_AffectsButTargetGroupReturned(t *testing.T) {
	root := idProject(t)
	// Target: tc-11111111 under a/, requirement REQ-1.
	writeFile(t, root, "gtms/test/cases/a/tc-11111111-login.md",
		"---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-11111111\ntitle: Login\nrequirement: REQ-1\n---\nbody\n")
	// A proven owner in another folder and another group.
	writeFile(t, root, "gtms/test/cases/b/tc-33333333-search.md",
		"---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-33333333\ntitle: Search\nrequirement: REQ-2\n---\nbody\n")
	// Unprovable: no test_case_id, under b/, filename id unrelated to the target.
	writeFile(t, root, "gtms/test/cases/b/tc-22222222-broken.md",
		"---\ntitle: broken\n---\nbody\n")

	report, targetErr, diags, err := MapTargetDiagnosticsWithSnapshot(mustSnapshot(t, root), root, "tc-11111111", "", false)
	require.NoError(t, err)
	require.NoError(t, targetErr, "the target is a proven unique owner; it must not be refused")
	require.NotNil(t, report)

	var target *RequirementGroup
	for i := range report.Groups {
		if report.Groups[i].Requirement == "REQ-1" {
			target = &report.Groups[i]
		}
	}
	require.NotNil(t, target, "the target's group must be returned")
	var ids []string
	for _, e := range target.TestCases {
		ids = append(ids, e.TestCaseID)
	}
	assert.Equal(t, []string{"tc-11111111"}, ids)

	assert.True(t, diags.Affected(),
		"map <tc-id> judges Unprovable candidates tree-wide: one outside the target's folder and group still affects it")
	assert.Contains(t, diagText(diags), "gtms/test/cases/b/tc-22222222-broken.md")

	// Contrast: replace the Unprovable file with an Ambiguous ID outside the printed
	// group. That does not affect the single-TC read.
	writeFile(t, root, "gtms/test/cases/b/tc-22222222-broken.md",
		"---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-44444444\ntitle: Dup one\nrequirement: REQ-2\n---\nbody\n")
	writeFile(t, root, "gtms/test/cases/c/tc-44444444-dup.md",
		"---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-44444444\ntitle: Dup two\nrequirement: REQ-2\n---\nbody\n")
	_, targetErr, diags, err = MapTargetDiagnosticsWithSnapshot(mustSnapshot(t, root), root, "tc-11111111", "", false)
	require.NoError(t, err)
	require.NoError(t, targetErr)
	assert.False(t, diags.Affected(),
		"an Ambiguous ID whose owners are outside the printed group does not affect map <tc-id>; got:\n%s", diagText(diags))
}
