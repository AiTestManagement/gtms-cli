package link

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// seedTwoOwners creates tc-77777777 owned by two specs in different folders,
// plus an artefact to link.
func seedTwoOwners(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	createTCSpecAt(t, root, "alpha", "tc-77777777")
	createTCSpecAt(t, root, "beta", "tc-77777777")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "test"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "test", "a.bats"), []byte("x\n"), 0o644))
	return root
}

// REV-158 CLAUDE-006: LinkRecord hashed through pipeline.ResolveTestCaseSpec ->
// testcase.FindSource, which maps every error -- including a typed
// *OwnershipError -- to "". An ambiguous id was therefore reported as
// "test case spec for tc-77777777 not found under .../gtms/test/cases", an
// identity failure rendered as absence, and the CLI attached the hint
// "Check the artefact path or use --force to overwrite" -- a flag that cannot
// help. ADR-024 s7 requires the typed diagnostic naming both owners.
func TestLinkRecord_AmbiguousOwnership_TypedDiagnosticNotAbsence(t *testing.T) {
	root := seedTwoOwners(t)

	err := linkRecordErr(root, playwrightConfig(), "tc-77777777", "playwright", "test/a.bats", true, false)
	require.Error(t, err)

	var oe *testcase.OwnershipError
	require.ErrorAs(t, err, &oe, "link must return the typed ownership error, not a not-found error")
	assert.Equal(t, testcase.OutcomeAmbiguous, oe.Outcome)
	require.Len(t, oe.Owners, 2, "both owners must be named")
	assert.Contains(t, strings.Join(oe.Owners, "|"), "alpha")
	assert.Contains(t, strings.Join(oe.Owners, "|"), "beta")

	assert.NotContains(t, err.Error(), "not found",
		"an identity failure must never be rendered as absence")
	assert.NotContains(t, err.Error(), "--force",
		"--force cannot resolve an ambiguous identity and must not be suggested")

	rec, _, _ := wiring.Find(root, "tc-77777777", "playwright")
	assert.Nil(t, rec, "nothing may be written")
}

// The same holds for --strict, which used to go through testcase.Exists and
// produce the same absence wording.
func TestLinkRecord_AmbiguousOwnershipStrict_TypedDiagnostic(t *testing.T) {
	root := seedTwoOwners(t)

	err := linkRecordErr(root, playwrightConfig(), "tc-77777777", "playwright", "test/a.bats", false, true)
	require.Error(t, err)
	var oe *testcase.OwnershipError
	require.ErrorAs(t, err, &oe)
	assert.Equal(t, testcase.OutcomeAmbiguous, oe.Outcome)
}

// An Unprovable identity is likewise a refusal with a repair sentence, not
// absence.
func TestLinkRecord_UnprovableOwnership_TypedDiagnostic(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(layout.TestCasesDir(root), "feat")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-77777777-broken.md"),
		[]byte("---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: [unclosed\n---\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "test"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "test", "a.bats"), []byte("x\n"), 0o644))

	err := linkRecordErr(root, playwrightConfig(), "tc-77777777", "playwright", "test/a.bats", true, false)
	require.Error(t, err)
	var oe *testcase.OwnershipError
	require.ErrorAs(t, err, &oe)
	assert.Equal(t, testcase.OutcomeUnprovable, oe.Outcome)
	assert.Contains(t, err.Error(), "cannot be proved")
}

// --check is a direct single-TC read and must refuse an ambiguous id too,
// rather than presenting one arbitrary owner as fact.
func TestCheckLink_AmbiguousOwnership_TypedDiagnostic(t *testing.T) {
	root := seedTwoOwners(t)

	_, err := CheckLink(root, "tc-77777777", "playwright", "test/a.bats", false)
	require.Error(t, err)
	var oe *testcase.OwnershipError
	require.ErrorAs(t, err, &oe)
	assert.Equal(t, testcase.OutcomeAmbiguous, oe.Outcome)
}

// --refresh is a wiring write on the same package and must keep the typed error
// rather than converting it to "spec not found for ...".
func TestPlanRefresh_AmbiguousOwnership_TypedDiagnostic(t *testing.T) {
	root := seedTwoOwners(t)

	_, err := PlanRefresh(root, mustOpSnapshot(t, root), &wiring.WiringRecord{
		TestCase: "tc-77777777", TestCaseHash: "1111111111111111", Framework: "playwright",
		Adapter: "playwright-runner", Artefact: "test/a.bats", ArtefactHash: "2222222222222222",
	})
	require.Error(t, err)
	var oe *testcase.OwnershipError
	require.ErrorAs(t, err, &oe)
	assert.NotContains(t, err.Error(), "spec not found")
}

// Positive control: a genuinely absent test case keeps link's established
// not-found wording, which frozen acceptance specs assert.
func TestLinkRecord_PhantomStillReportsNotFound(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(layout.TestCasesDir(root), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "test"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "test", "a.bats"), []byte("x\n"), 0o644))

	err := linkRecordErr(root, playwrightConfig(), "tc-deadbeef", "playwright", "test/a.bats", false, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found in gtms/test/cases")
	var oe *testcase.OwnershipError
	assert.NotErrorAs(t, err, &oe, "genuine absence must NOT be a typed ownership error")
}
