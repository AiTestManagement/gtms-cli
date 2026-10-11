package reader

// Unit coverage for the ENH-205 / ADR-024 5b shared identity helper. These prove
// the OWNERSHIP OUTCOMES and every display-field variant at the resolver layer, as
// the ADR verification strategy requires; the per-command user-visible policy (e)
// behaviour is proven by the acceptance specs.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/testcase"
)

func idProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	return root
}

func mustSnapshot(t *testing.T, root string) *testcase.Snapshot {
	t.Helper()
	snap, err := testcase.NewSnapshot(root)
	require.NoError(t, err)
	return snap
}

func specIDs(specs []testCaseFrontmatter) []string {
	out := make([]string, 0, len(specs))
	for _, s := range specs {
		out = append(out, s.ID)
	}
	return out
}

// diagText renders all diagnostics to a string for substring assertions, exactly
// as the CLI would surface them on stderr.
func diagText(d IdentityDiagnostics) string {
	var b strings.Builder
	d.Render(&b)
	return b.String()
}

func TestResolveScopedSpecs_CleanTreeAllProven(t *testing.T) {
	root := idProject(t)
	writeFile(t, root, "gtms/test/cases/checkout/tc-11111111-a.md", "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-11111111\ntitle: A\n---\n")
	writeFile(t, root, "gtms/test/cases/checkout/tc-22222222-b.md", "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-22222222\ntitle: B\n---\n")
	writeFile(t, root, "gtms/test/cases/search/tc-33333333-c.md", "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-33333333\ntitle: C\n---\n")

	specs, diags, err := resolveScopedSpecs(mustSnapshot(t, root), root, nil)
	require.NoError(t, err)
	assert.False(t, diags.Affected(), "a clean tree has no identity diagnostics")
	assert.Equal(t, []string{"tc-11111111", "tc-22222222", "tc-33333333"}, specIDs(specs))
}

func TestResolveScopedSpecs_DuplicateIsAmbiguousNamesEveryOwner(t *testing.T) {
	root := idProject(t)
	writeFile(t, root, "gtms/test/cases/checkout/tc-33333333-a.md", "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-33333333\ntitle: A\n---\n")
	writeFile(t, root, "gtms/test/cases/search/tc-33333333-b.md", "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-33333333\ntitle: B\n---\n")
	writeFile(t, root, "gtms/test/cases/checkout/tc-11111111-ok.md", "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-11111111\ntitle: OK\n---\n")

	// Scoped to checkout: the duplicate has an owner in scope, so it is affected
	// even though its other owner is elsewhere, and BOTH owners are named.
	scope := &ScopeInfo{ScanDir: filepath.Join(root, "gtms/test/cases/checkout"), RelPath: "gtms/test/cases/checkout/", Recursive: false}
	specs, diags, err := resolveScopedSpecs(mustSnapshot(t, root), root, scope)
	require.NoError(t, err)
	assert.Equal(t, []string{"tc-11111111"}, specIDs(specs), "the ambiguous ID is never resolved to one owner")
	require.Len(t, diags.ambiguous, 1)
	assert.Equal(t, "tc-33333333", diags.ambiguous[0].id)
	txt := diagText(diags)
	assert.Contains(t, txt, "test case ID 'tc-33333333' is ambiguous")
	assert.Contains(t, txt, "tc-33333333-a.md")
	assert.Contains(t, txt, "tc-33333333-b.md", "the out-of-scope owner is named too")
}

func TestResolveScopedSpecs_MissingIDIsUnprovableInScopeOnly(t *testing.T) {
	root := idProject(t)
	writeFile(t, root, "gtms/test/cases/checkout/tc-11111111-ok.md", "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-11111111\ntitle: OK\n---\n")
	writeFile(t, root, "gtms/test/cases/checkout/tc-55555555-no-id.md", "---\ntitle: No id\n---\n")
	writeFile(t, root, "gtms/test/cases/search/tc-88888888-no-id.md", "---\ntitle: Also no id\n---\n")

	scope := &ScopeInfo{ScanDir: filepath.Join(root, "gtms/test/cases/checkout"), RelPath: "gtms/test/cases/checkout/", Recursive: false}
	specs, diags, err := resolveScopedSpecs(mustSnapshot(t, root), root, scope)
	require.NoError(t, err)
	assert.Equal(t, []string{"tc-11111111"}, specIDs(specs))
	txt := diagText(diags)
	assert.Contains(t, txt, "cannot be proved from")
	assert.Contains(t, txt, "tc-55555555-no-id.md")
	assert.NotContains(t, txt, "tc-88888888-no-id.md", "an out-of-scope unprovable candidate does not affect a folder read")
	assert.NotContains(t, txt, "not found", "an unprovable spec is never reported as not found")
}

func TestResolveScopedSpecs_RenamedOwnerVisibleAndCounted(t *testing.T) {
	root := idProject(t)
	writeFile(t, root, "gtms/test/cases/checkout/renamed.md", "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-99999999\ntitle: Renamed\n---\n")
	writeFile(t, root, "gtms/test/cases/checkout/tc-11111111-ok.md", "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-11111111\ntitle: OK\n---\n")

	specs, diags, err := resolveScopedSpecs(mustSnapshot(t, root), root, nil)
	require.NoError(t, err)
	assert.False(t, diags.Affected())
	assert.Equal(t, []string{"tc-11111111", "tc-99999999"}, specIDs(specs), "a renamed body owner is listed under its body ID")

	// A same-ID spec elsewhere makes the renamed owner count as a second owner.
	writeFile(t, root, "gtms/test/cases/search/tc-99999999-orig.md", "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-99999999\ntitle: Orig\n---\n")
	specs2, diags2, err := resolveScopedSpecs(mustSnapshot(t, root), root, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"tc-11111111"}, specIDs(specs2))
	require.Len(t, diags2.ambiguous, 1)
	assert.Contains(t, diagText(diags2), "renamed.md")
}

func TestNormalizedDisplayFields_NamesTheOffendingField(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		field string // "" means success
	}{
		{"good", "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-11111111\ntitle: Good\nrequirement: REQ-1\ntags:\n  - a\n---\n", ""},
		{"bad-title", "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-aaaaaaaa\ntitle:\n  - one\n  - two\n---\n", "title"},
		{"bad-requirement", "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-ffffffff\nrequirement:\n  id: REQ-1\n---\n", "requirement"},
		{"bad-tags", "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-abababab\ntags:\n  area: checkout\n---\n", "tags"},
		// The by-mechanism widening: a non-title/requirement/tags field also counts.
		{"bad-priority", "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-bcbcbcbc\npriority:\n  level: high\n---\n", "priority"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			decoded := testcase.DecodeCandidate([]byte(c.body))
			tc, field := testCaseFromNormalized(decoded.Metadata, decoded.Failure)
			if c.field == "" {
				require.NotNil(t, tc)
				assert.Equal(t, "", field)
			} else {
				assert.Nil(t, tc)
				assert.Equal(t, c.field, field)
			}
		})
	}
}

func TestIdentityDiagnostics_RenderOrderAndClosingLine(t *testing.T) {
	d := IdentityDiagnostics{
		ambiguous: []ambiguousDiag{{id: "tc-33333333", owners: []string{"a/x.md", "b/x.md"}}},
		pathIssues: []pathIssue{
			{path: "z/bad.md", message: "test case z/bad.md cannot be read: field 'title' has the wrong type"},
			{path: "a/missing.md", message: "ownership of a candidate spec under a/ cannot be proved from a/missing.md: add a test_case_id"},
		},
	}
	txt := diagText(d)
	// Ambiguous first, then path issues sorted by path (a/missing before z/bad).
	ambIdx := strings.Index(txt, "is ambiguous")
	aIdx := strings.Index(txt, "a/missing.md")
	zIdx := strings.Index(txt, "z/bad.md")
	require.True(t, ambIdx >= 0 && aIdx >= 0 && zIdx >= 0)
	assert.Less(t, ambIdx, aIdx, "ambiguous is rendered before path issues")
	assert.Less(t, aIdx, zIdx, "path issues are sorted by path")
	assert.Contains(t, txt, "3 test cases could not be read", "closing line counts every affected entry")
}
