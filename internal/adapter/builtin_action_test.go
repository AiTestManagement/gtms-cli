package adapter

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrg/frontmatter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/scaffold"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

func TestBuiltinCreate_EmitsCanonicalV2(t *testing.T) {
	dir := t.TempDir()

	ctx := &AdapterContext{
		OutputDir:    dir,
		TestCaseIDs:  "tc-aabbccdd,tc-11223344",
		TestCaseName: "",
		Reference:    "",
	}

	result, err := BuiltinCreate(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Len(t, result.SavedFiles, 1)

	// File should exist
	outPath := result.SavedFiles[0]
	assert.FileExists(t, outPath)
	assert.Equal(t, filepath.Join(dir, "tc-aabbccdd.md"), outPath)

	// Read and verify content
	data, err := os.ReadFile(outPath)
	require.NoError(t, err)
	content := string(data)
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	canonicalPrefix := []string{
		`gtms_schema: "test-case/v2"`,
		"type: Test Case Spec",
		"test_type: Functional",
		"test_case_id: tc-aabbccdd",
		`title: ""`,
		"requirement: ",
		"priority: Medium",
	}
	require.GreaterOrEqual(t, len(lines), len(canonicalPrefix)+1)
	assert.Equal(t, canonicalPrefix, lines[1:1+len(canonicalPrefix)])
	assert.Regexp(t, `^created: \d{4}-\d{2}-\d{2}$`, lines[1+len(canonicalPrefix)])
	assert.NotContains(t, content, "status: draft")
	assert.NotContains(t, content, "name:")
	assert.Contains(t, content, "## Test Objective")
	assert.Contains(t, content, "## Test Steps")
}

func TestBuiltinCreate_WithName(t *testing.T) {
	dir := t.TempDir()

	ctx := &AdapterContext{
		OutputDir:    dir,
		TestCaseIDs:  "tc-aabbccdd",
		TestCaseName: "user-can-login",
	}

	result, err := BuiltinCreate(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, filepath.Join(dir, "tc-aabbccdd-user-can-login.md"), result.SavedFiles[0])

	data, err := os.ReadFile(result.SavedFiles[0])
	require.NoError(t, err)
	assert.Contains(t, string(data), `title: "user-can-login"`)
}

func TestBuiltinCreate_WithReference(t *testing.T) {
	dir := t.TempDir()

	ctx := &AdapterContext{
		OutputDir:   dir,
		TestCaseIDs: "tc-aabbccdd",
		Reference:   "REQ-001",
	}

	result, err := BuiltinCreate(ctx)
	require.NoError(t, err)

	data, err := os.ReadFile(result.SavedFiles[0])
	require.NoError(t, err)
	// ENH-161 substitution wraps non-empty --reference in a YAML
	// double-quoted scalar (see yamlQuotedScalarOrEmpty) so values
	// containing `: `, leading `-`, etc. round-trip cleanly.
	assert.Contains(t, string(data), `requirement: "REQ-001"`)
}

func TestBuiltinCreate_NoOutputDir(t *testing.T) {
	ctx := &AdapterContext{
		TestCaseIDs: "tc-aabbccdd",
	}

	_, err := BuiltinCreate(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "output directory not set")
}

func TestBuiltinCreate_NoTestCaseIDs(t *testing.T) {
	dir := t.TempDir()
	ctx := &AdapterContext{
		OutputDir: dir,
	}

	_, err := BuiltinCreate(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no test case IDs available")
}

func TestBuiltinPrime_StampsTemplate(t *testing.T) {
	dir := t.TempDir()

	// Create template file
	tmplPath := filepath.Join(dir, "template.yaml")
	tmplContent := `test_case_id: ${TESTCASE}
test_case_hash: ${TESTCASE_HASH}
framework: manual
result:
title: "${TC_TITLE}"
branch: ${BRANCH}
`
	require.NoError(t, os.WriteFile(tmplPath, []byte(tmplContent), 0644))

	outPath := filepath.Join(dir, "tc-aabbccdd--manual.result.yaml")
	ctx := &AdapterContext{
		TemplateFile: tmplPath,
		OutputFile:   outPath,
		TestCase:     "tc-aabbccdd",
		TestCaseHash: "abcdef0123456789",
		Branch:       "main",
		TCTitle:      "Login test",
		Force:        false,
	}

	result, err := BuiltinPrime(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.FileExists(t, outPath)

	data, err := os.ReadFile(outPath)
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "test_case_id: tc-aabbccdd")
	assert.Contains(t, content, "test_case_hash: abcdef0123456789")
	assert.Contains(t, content, "branch: main")
	assert.Contains(t, content, `title: "Login test"`)
}

func TestBuiltinPrime_RefusesOverwriteWithoutForce(t *testing.T) {
	dir := t.TempDir()

	tmplPath := filepath.Join(dir, "template.yaml")
	require.NoError(t, os.WriteFile(tmplPath, []byte("template"), 0644))

	outPath := filepath.Join(dir, "existing.yaml")
	require.NoError(t, os.WriteFile(outPath, []byte("existing content"), 0644))

	ctx := &AdapterContext{
		TemplateFile: tmplPath,
		OutputFile:   outPath,
		Force:        false,
	}

	result, err := BuiltinPrime(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, result.ExitCode)
	assert.Contains(t, result.Stderr, "already exists")
}

func TestBuiltinPrime_OverwritesWithForce(t *testing.T) {
	dir := t.TempDir()

	tmplPath := filepath.Join(dir, "template.yaml")
	require.NoError(t, os.WriteFile(tmplPath, []byte("test_case_id: ${TESTCASE}"), 0644))

	outPath := filepath.Join(dir, "existing.yaml")
	require.NoError(t, os.WriteFile(outPath, []byte("old content"), 0644))

	ctx := &AdapterContext{
		TemplateFile: tmplPath,
		OutputFile:   outPath,
		TestCase:     "tc-aabbccdd",
		Force:        true,
	}

	result, err := BuiltinPrime(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)

	data, err := os.ReadFile(outPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "tc-aabbccdd")
}

func TestBuiltinExecute_RecordsVerdict(t *testing.T) {
	dir := t.TempDir()

	resultFilePath := filepath.Join(dir, "result.yaml")
	require.NoError(t, os.WriteFile(resultFilePath, []byte("test_case_id: tc-aabbccdd\ntest_case_hash: abcdef0123456789\nresult: pass\n"), 0644))

	ctx := &AdapterContext{
		ResultValue:        "pass",
		TestCase:           "tc-aabbccdd",
		TestCaseHash:       "abcdef0123456789",
		ResultTestCaseHash: "abcdef0123456789",
		ResultTemplate:     resultFilePath,
	}

	result, err := BuiltinExecute(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "tc-aabbccdd -> pass")
	assert.Empty(t, result.Stderr) // no drift
}

func TestBuiltinExecute_DetectsDrift(t *testing.T) {
	dir := t.TempDir()

	resultFilePath := filepath.Join(dir, "result.yaml")
	require.NoError(t, os.WriteFile(resultFilePath, []byte("test_case_id: tc-aabbccdd\ntest_case_hash: abcdef0123456789\nresult: pass\n"), 0644))

	ctx := &AdapterContext{
		ResultValue:        "pass",
		TestCase:           "tc-aabbccdd",
		TestCaseHash:       "1111111111111111", // different from prime-time
		ResultTestCaseHash: "abcdef0123456789",
		ResultTemplate:     resultFilePath,
	}

	result, err := BuiltinExecute(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stderr, "drift")

	// Drift fields should be appended to the result file
	data, err := os.ReadFile(resultFilePath)
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "drift-detected: true")
	assert.Contains(t, content, "test_case_hash_at_execute: 1111111111111111")
}

// --- ENH-150 post-fill validation, ADR-024 s5 ---
//
// The gate now validates the RESOLVED OWNER rather than walking the cases tree
// for a filename that claims the target. Three of these fixtures no longer have
// an owner to validate, because the resolution upstream now REFUSES them
// outright -- earlier, and more strictly, than the old walk did. Each test below
// therefore asserts both halves: that the fixture is refused at resolution, and
// that the gate itself is correct for whatever it is handed.

func TestValidateTestCaseOwner_ValidFile(t *testing.T) {
	dir := t.TempDir()

	casesDir := filepath.Join(dir, "gtms", "test", "cases", "test-folder")
	require.NoError(t, os.MkdirAll(casesDir, 0755))
	gtmsDir := filepath.Join(dir, "gtms")
	require.NoError(t, os.WriteFile(filepath.Join(gtmsDir, ".gtms-root"), []byte(""), 0644))

	tcContent := "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-aabbccdd\ntitle: \"Test case\"\nstatus: draft\n---\n## Test Objective\n"
	require.NoError(t, os.WriteFile(filepath.Join(casesDir, "tc-aabbccdd-test.md"), []byte(tcContent), 0644))

	owner, err := testcase.ResolveOwner(dir, "tc-aabbccdd")
	require.NoError(t, err, "a valid unique spec must resolve to an owner")
	violations := ValidateTestCaseOwner(dir, owner)
	assert.Empty(t, violations)
}

// Filename/body ID mismatch. The old gate walked to tc-aabbccdd-test.md and
// reported "does not match filename ID". Under ADR-024 s1 that file declares
// tc-99999999 and therefore does not OWN tc-aabbccdd at all, so the id resolves
// to NotFound and the command refuses before the gate. The refusal is preserved;
// what changed is which layer states it, and that the reason is now accurate.
func TestValidateTestCaseOwner_IDMismatchHasNoOwner(t *testing.T) {
	dir := t.TempDir()
	casesDir := filepath.Join(dir, "gtms", "test", "cases", "test-folder")
	require.NoError(t, os.MkdirAll(casesDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gtms", ".gtms-root"), []byte(""), 0644))

	tcContent := "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-99999999\ntitle: \"Mismatched ID\"\n---\n"
	require.NoError(t, os.WriteFile(filepath.Join(casesDir, "tc-aabbccdd-test.md"), []byte(tcContent), 0644))

	_, err := testcase.ResolveOwner(dir, "tc-aabbccdd")
	require.Error(t, err, "a spec declaring another id must not own tc-aabbccdd")
	assert.ErrorIs(t, err, testcase.ErrOwnerNotFound)

	// The file that DOES own tc-99999999 validates cleanly -- a filename that
	// disagrees with a valid body id is not an integrity error for a spec
	// (ADR-024 s1/s4: renamed owners are expressly permitted).
	owner, ownErr := testcase.ResolveOwner(dir, "tc-99999999")
	require.NoError(t, ownErr)
	violations := ValidateTestCaseOwner(dir, owner)
	assert.Empty(t, violations)
}

// Missing test_case_id. The old gate walked to the file and reported the missing
// field. Now the candidate is Unprovable, which refuses the whole operation --
// a strictly stronger outcome than a violation list, and it names the same file.
func TestValidateTestCaseOwner_MissingFrontmatterIsUnprovable(t *testing.T) {
	dir := t.TempDir()
	casesDir := filepath.Join(dir, "gtms", "test", "cases", "test-folder")
	require.NoError(t, os.MkdirAll(casesDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gtms", ".gtms-root"), []byte(""), 0644))

	tcContent := "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntitle: \"No test_case_id\"\n---\n"
	require.NoError(t, os.WriteFile(filepath.Join(casesDir, "tc-aabbccdd-test.md"), []byte(tcContent), 0644))

	_, err := testcase.ResolveOwner(dir, "tc-aabbccdd")
	require.Error(t, err)
	var oe *testcase.OwnershipError
	require.ErrorAs(t, err, &oe)
	assert.Equal(t, testcase.OutcomeUnprovable, oe.Outcome)
	assert.Equal(t, testcase.ReasonMissingID, oe.Reason)
	assert.Contains(t, oe.Error(), "tc-aabbccdd-test.md", "the refusal must name the offending file")
}

// Duplicate test_case_id. The old gate scanned the TC's own folder and reported
// a duplicate. Now two specs declaring one id are two physical owners, which
// resolves Ambiguous and refuses -- and the check is project-wide, so it also
// catches the cross-folder duplicate the folder-scoped scan could not see.
func TestValidateTestCaseOwner_DuplicateIDsAreAmbiguous(t *testing.T) {
	dir := t.TempDir()
	casesDir := filepath.Join(dir, "gtms", "test", "cases", "test-folder")
	require.NoError(t, os.MkdirAll(casesDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gtms", ".gtms-root"), []byte(""), 0644))

	tc1 := "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-aabbccdd\ntitle: \"First\"\n---\n"
	tc2 := "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-aabbccdd\ntitle: \"Duplicate\"\n---\n"
	require.NoError(t, os.WriteFile(filepath.Join(casesDir, "tc-aabbccdd-first.md"), []byte(tc1), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(casesDir, "tc-aabbccdd-duplicate.md"), []byte(tc2), 0644))

	_, err := testcase.ResolveOwner(dir, "tc-aabbccdd")
	require.Error(t, err, "a duplicated id must refuse, not pick one")
	var oe *testcase.OwnershipError
	require.ErrorAs(t, err, &oe)
	assert.Equal(t, testcase.OutcomeAmbiguous, oe.Outcome)
	assert.Len(t, oe.Owners, 2, "both owners must be named")
}

// Cross-folder duplicate: the shape the old FOLDER-scoped duplicate scan missed
// entirely. It is now refused for the same reason as the same-folder case.
func TestValidateTestCaseOwner_CrossFolderDuplicateIsAmbiguous(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "gtms"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gtms", ".gtms-root"), []byte(""), 0644))
	for _, folder := range []string{"alpha", "beta"} {
		d := filepath.Join(dir, "gtms", "test", "cases", folder)
		require.NoError(t, os.MkdirAll(d, 0755))
		body := "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-aabbccdd\ntitle: " + folder + "\n---\n"
		require.NoError(t, os.WriteFile(filepath.Join(d, "tc-aabbccdd-spec.md"), []byte(body), 0644))
	}

	_, err := testcase.ResolveOwner(dir, "tc-aabbccdd")
	require.Error(t, err)
	var oe *testcase.OwnershipError
	require.ErrorAs(t, err, &oe)
	assert.Equal(t, testcase.OutcomeAmbiguous, oe.Outcome)
}

// The gate's own remaining job: an owner whose spec became unreadable between
// resolution and validation must produce a violation, not a silent pass. This is
// what stops the gate degrading into a no-op now that it trusts a resolved owner.
func TestValidateTestCaseOwner_ReportsUnparseableOwner(t *testing.T) {
	dir := t.TempDir()
	casesDir := filepath.Join(dir, "gtms", "test", "cases", "test-folder")
	require.NoError(t, os.MkdirAll(casesDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gtms", ".gtms-root"), []byte(""), 0644))
	spec := filepath.Join(casesDir, "tc-aabbccdd-test.md")
	require.NoError(t, os.WriteFile(spec, []byte("---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-aabbccdd\n---\nok\n"), 0644))

	owner, err := testcase.ResolveOwner(dir, "tc-aabbccdd")
	require.NoError(t, err)

	// The spec loses its identity after the owner was resolved.
	require.NoError(t, os.WriteFile(spec, []byte("---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntitle: gone\n---\n"), 0644))

	violations := ValidateTestCaseOwner(dir, owner)
	require.NotEmpty(t, violations, "the gate must read the owner, not trust the resolution")
	assert.Contains(t, violations[0].Reason, "missing required field")
}

// A nil owner is not a violation: the command owns the not-found wording.
func TestValidateTestCaseOwner_NilOwnerIsNotAViolation(t *testing.T) {
	violations := ValidateTestCaseOwner(t.TempDir(), nil)
	assert.Empty(t, violations)
}

func TestYamlEscape(t *testing.T) {
	assert.Equal(t, "hello", yamlEscape("hello"))
	// Backslash is doubled, quote is escaped
	assert.Equal(t, `say \\\"hi\\\"`, yamlEscape("say \\\"hi\\\""))
	// Newline becomes literal \n
	assert.Equal(t, `line1\nline2`, yamlEscape("line1\nline2"))
	// Tab becomes literal \t
	assert.Equal(t, `tab\there`, yamlEscape("tab\there"))
}

// --- ENH-151: BuiltinAutomate tests ---

// setupAutomateFixture creates a minimal project root with a slugged TC spec
// file and a gtms.config that has a bats-runner execute adapter. BUG-107:
// uses a slugged filename (tc-auto0001-test-skeleton.md) so BuiltinAutomate
// exercises the slug-preserving code path.
func setupAutomateFixture(t *testing.T) (root string) {
	t.Helper()
	root = t.TempDir()

	// TC spec (slugged filename per BUG-107)
	tcDir := filepath.Join(root, "gtms", "test", "cases", "my-feature")
	require.NoError(t, os.MkdirAll(tcDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tcDir, "tc-auto0001-test-skeleton.md"), []byte(`---
gtms_schema: "test-case/v2"
type: Test Case Spec
test_type: Functional
test_case_id: tc-auto0001
title: Test automation skeleton
requirement: REQ-A
priority: Medium
---

## Test Objective

Test something.
`), 0644))

	// gtms.config with bats-runner execute adapter
	require.NoError(t, os.WriteFile(filepath.Join(root, "gtms.config"), []byte(`project:
  name: test-project
  repo: org/test
adapters:
  execute:
    bats-runner:
      mode: sync
      script: run.sh
      framework: bats
defaults:
  execute: bats-runner
`), 0644))

	// Required dirs
	for _, dir := range []string{
		"gtms/tasks/pending",
		"gtms/tasks/in-progress",
		"gtms/tasks/complete",
		"gtms/tasks/error",
		"gtms/automation/wiring",
		"test/acceptance/my-feature",
	} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0755))
	}

	return root
}

// setupBareAutomateFixture creates a minimal project root with a bare TC spec
// file (no slug). Used to test the fallback path where the artefact filename
// uses only the test case ID. BUG-107.
func setupBareAutomateFixture(t *testing.T) (root string) {
	t.Helper()
	root = t.TempDir()

	// TC spec (bare filename -- no slug)
	tcDir := filepath.Join(root, "gtms", "test", "cases", "my-feature")
	require.NoError(t, os.MkdirAll(tcDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tcDir, "tc-auto0001.md"), []byte(`---
gtms_schema: "test-case/v2"
type: Test Case Spec
test_type: Functional
test_case_id: tc-auto0001
title: Bare test case
requirement: REQ-B
priority: Medium
---

## Test Objective

Bare spec test.
`), 0644))

	// Same gtms.config
	require.NoError(t, os.WriteFile(filepath.Join(root, "gtms.config"), []byte(`project:
  name: test-project
  repo: org/test
adapters:
  execute:
    bats-runner:
      mode: sync
      script: run.sh
      framework: bats
defaults:
  execute: bats-runner
`), 0644))

	for _, dir := range []string{
		"gtms/tasks/pending",
		"gtms/tasks/in-progress",
		"gtms/tasks/complete",
		"gtms/tasks/error",
		"gtms/automation/wiring",
		"test/acceptance/my-feature",
	} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0755))
	}

	return root
}

func TestBuiltinAutomate_StampsBATSSkeleton(t *testing.T) {
	root := setupAutomateFixture(t)
	cfg, err := loadTestConfig(root)
	require.NoError(t, err)

	ctx := &AdapterContext{
		ProjectRoot:  root,
		TestCase:     "tc-auto0001",
		Framework:    "bats",
		TestCaseFile: "gtms/test/cases/my-feature/tc-auto0001-test-skeleton.md",
		OutputSubdir: "my-feature/",
	}

	result, err := BuiltinAutomate(planFor(ctx), cfg)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	require.Len(t, result.SavedFiles, 1)

	// BUG-107: Skeleton file should preserve the source spec slug
	outPath := result.SavedFiles[0]
	assert.FileExists(t, outPath)
	assert.True(t, strings.HasSuffix(outPath, "tc-auto0001-test-skeleton.bats"),
		"skeleton filename should be tc-auto0001-test-skeleton.bats, got %s", outPath)

	// Read and verify skeleton content
	data, err := os.ReadFile(outPath)
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "setup_file()")
	assert.Contains(t, content, "_common_setup")
	assert.Contains(t, content, "@test")
	assert.Contains(t, content, "tc-auto0001")
	assert.Contains(t, content, "teardown()")
	assert.Contains(t, content, "common-setup.bash")
}

func TestBuiltinAutomate_WritesWiringWithPendingHash(t *testing.T) {
	root := setupAutomateFixture(t)
	cfg, err := loadTestConfig(root)
	require.NoError(t, err)

	ctx := &AdapterContext{
		ProjectRoot:  root,
		TestCase:     "tc-auto0001",
		Framework:    "bats",
		TestCaseFile: "gtms/test/cases/my-feature/tc-auto0001-test-skeleton.md",
		OutputSubdir: "my-feature/",
	}

	_, err = BuiltinAutomate(planFor(ctx), cfg)
	require.NoError(t, err)

	// Read wiring record and verify
	rec, wiringPath, err := wiring.Find(root, "tc-auto0001", "bats")
	require.NoError(t, err)
	require.NotNil(t, rec, "wiring record should exist")
	assert.NotEmpty(t, wiringPath)
	assert.Equal(t, "tc-auto0001", rec.TestCase)
	assert.Equal(t, "bats", rec.Framework)
	assert.Equal(t, "bats-runner", rec.Adapter, "wiring should carry the canonical execute adapter")
	assert.Equal(t, wiring.PendingArtefactHash, rec.ArtefactHash,
		"artefact-hash should be the pending sentinel")
	assert.True(t, wiring.IsRealArtefactHash(rec.TestCaseHash),
		"testcase-hash should be a real hash, got %q", rec.TestCaseHash)
	// BUG-107: wiring artefact should carry the slugged filename
	assert.Contains(t, rec.Artefact, "tc-auto0001-test-skeleton.bats",
		"artefact path should reference the slugged skeleton")
}

// TestBuiltinAutomate_HonorsConfiguredOutputDir is the BUG-125 regression: when
// output-dir is explicitly configured (OutputDirConfigured == true), the built-in
// stamps the spec under that dir (joined with the work-item subdir) and records that
// location in the wiring artefact -- NOT under the framework-native default.
func TestBuiltinAutomate_HonorsConfiguredOutputDir(t *testing.T) {
	root := setupAutomateFixture(t)
	cfg, err := loadTestConfig(root)
	require.NoError(t, err)

	// ctx.OutputDir is absolute in production (invoker joins it with projectRoot);
	// mirror that here with a brownfield-style harness path inside the project.
	customDir := filepath.Join(root, "PLAYWRIGHT-TESTS", "tests", "gtms")
	ctx := &AdapterContext{
		ProjectRoot:         root,
		TestCase:            "tc-auto0001",
		Framework:           "bats",
		TestCaseFile:        "gtms/test/cases/my-feature/tc-auto0001-test-skeleton.md",
		OutputSubdir:        "my-feature/",
		OutputDir:           customDir,
		OutputDirConfigured: true,
	}

	result, err := BuiltinAutomate(planFor(ctx), cfg)
	require.NoError(t, err)
	require.Len(t, result.SavedFiles, 1)

	outPath := result.SavedFiles[0]
	assert.FileExists(t, outPath)
	wantDir := filepath.Join(customDir, "my-feature")
	assert.Equal(t, filepath.Join(wantDir, "tc-auto0001-test-skeleton.bats"), outPath,
		"spec should be stamped under the configured output-dir/subdir")
	assert.NotContains(t, filepath.ToSlash(outPath), "test/acceptance",
		"configured output-dir must override the framework default")

	// Wiring artefact must record the configured location (project-relative, forward slashes).
	rec, _, err := wiring.Find(root, "tc-auto0001", "bats")
	require.NoError(t, err)
	require.NotNil(t, rec)
	assert.Contains(t, rec.Artefact, "PLAYWRIGHT-TESTS/tests/gtms/my-feature/tc-auto0001-test-skeleton.bats",
		"wiring artefact should point at the configured output-dir")
}

// TestBuiltinAutomate_UnsetOutputDirUsesFrameworkDefault locks the fallback: with
// output-dir NOT configured (OutputDirConfigured == false), the spec lands in the
// framework-native default (test/acceptance for BATS) -- the greenfield no-regression case.
func TestBuiltinAutomate_UnsetOutputDirUsesFrameworkDefault(t *testing.T) {
	root := setupAutomateFixture(t)
	cfg, err := loadTestConfig(root)
	require.NoError(t, err)

	ctx := &AdapterContext{
		ProjectRoot:  root,
		TestCase:     "tc-auto0001",
		Framework:    "bats",
		TestCaseFile: "gtms/test/cases/my-feature/tc-auto0001-test-skeleton.md",
		OutputSubdir: "my-feature/",
		// OutputDir / OutputDirConfigured deliberately left zero.
	}

	result, err := BuiltinAutomate(planFor(ctx), cfg)
	require.NoError(t, err)
	require.Len(t, result.SavedFiles, 1)

	outPath := filepath.ToSlash(result.SavedFiles[0])
	assert.Contains(t, outPath, "test/acceptance/my-feature/tc-auto0001-test-skeleton.bats",
		"unset output-dir should fall back to the BATS framework default")
}

func TestBuiltinAutomate_RejectsUnsupportedFramework(t *testing.T) {
	root := setupAutomateFixture(t)
	cfg, err := loadTestConfig(root)
	require.NoError(t, err)

	// BUG-111: "playwright" was added to the framework registry alongside
	// BATS. Remaining unregistered frameworks still error with "no automate
	// support found"; ENH-112 will add richer Playwright integration.
	for _, fw := range []string{"pester", "cypress"} {
		ctx := &AdapterContext{
			ProjectRoot:  root,
			TestCase:     "tc-auto0001",
			Framework:    fw,
			TestCaseFile: "gtms/test/cases/my-feature/tc-auto0001-test-skeleton.md",
			OutputSubdir: "my-feature/",
		}
		_, err = BuiltinAutomate(planFor(ctx), cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no automate support found")
		assert.Contains(t, err.Error(), fw)
	}
}

func TestBuiltinAutomate_RejectsNoFramework(t *testing.T) {
	root := setupAutomateFixture(t)
	cfg, err := loadTestConfig(root)
	require.NoError(t, err)

	ctx := &AdapterContext{
		ProjectRoot:  root,
		TestCase:     "tc-auto0001",
		Framework:    "",
		TestCaseFile: "gtms/test/cases/my-feature/tc-auto0001-test-skeleton.md",
	}

	_, err = BuiltinAutomate(planFor(ctx), cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no --framework specified")
}

// --- BUG-120: BuiltinAutomate manual-framework diagnostic test ---

func TestBuiltinAutomate_RejectsManualFramework(t *testing.T) {
	root := setupAutomateFixture(t)
	cfg, err := loadTestConfig(root)
	require.NoError(t, err)

	ctx := &AdapterContext{
		ProjectRoot:  root,
		TestCase:     "tc-auto0001",
		Framework:    "manual",
		TestCaseFile: "gtms/test/cases/my-feature/tc-auto0001-test-skeleton.md",
		OutputSubdir: "my-feature/",
	}

	_, err = BuiltinAutomate(planFor(ctx), cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not require automate")
	assert.Contains(t, err.Error(), "gtms prime")
	assert.Contains(t, err.Error(), "--adapter manual-execute")
	assert.Contains(t, err.Error(), "tc-auto0001")
}

// --- BUG-107: DeriveArtefactBasename tests ---

func TestDeriveArtefactBasename_SluggedPath(t *testing.T) {
	got := DeriveArtefactBasename("gtms/test/cases/my-feature/tc-aaa-login-happy.md", "tc-aaa")
	assert.Equal(t, "tc-aaa-login-happy", got)
}

func TestDeriveArtefactBasename_BarePath(t *testing.T) {
	got := DeriveArtefactBasename("gtms/test/cases/my-feature/tc-aaa.md", "tc-aaa")
	assert.Equal(t, "tc-aaa", got)
}

func TestDeriveArtefactBasename_EmptyPath(t *testing.T) {
	got := DeriveArtefactBasename("", "tc-fallback")
	assert.Equal(t, "tc-fallback", got)
}

func TestDeriveArtefactBasename_NestedPath(t *testing.T) {
	got := DeriveArtefactBasename("gtms/test/cases/a/b/tc-xxx-deep-slug.md", "tc-xxx")
	assert.Equal(t, "tc-xxx-deep-slug", got)
}

func TestDeriveArtefactBasename_NoExtension(t *testing.T) {
	got := DeriveArtefactBasename("gtms/test/cases/tc-aaa-slug", "tc-aaa")
	assert.Equal(t, "tc-aaa-slug", got)
}

// --- BUG-107: BuiltinAutomate bare spec fallback test ---

func TestBuiltinAutomate_BareSpecFallback(t *testing.T) {
	root := setupBareAutomateFixture(t)
	cfg, err := loadTestConfig(root)
	require.NoError(t, err)

	ctx := &AdapterContext{
		ProjectRoot:  root,
		TestCase:     "tc-auto0001",
		Framework:    "bats",
		TestCaseFile: "gtms/test/cases/my-feature/tc-auto0001.md",
		OutputSubdir: "my-feature/",
	}

	result, err := BuiltinAutomate(planFor(ctx), cfg)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	require.Len(t, result.SavedFiles, 1)

	// Bare spec: artefact filename should use bare TC ID
	outPath := result.SavedFiles[0]
	assert.FileExists(t, outPath)
	assert.True(t, strings.HasSuffix(outPath, "tc-auto0001.bats"),
		"bare spec should produce tc-auto0001.bats, got %s", outPath)

	// Wiring artefact should also be bare
	rec, _, err := wiring.Find(root, "tc-auto0001", "bats")
	require.NoError(t, err)
	require.NotNil(t, rec)
	assert.Contains(t, rec.Artefact, "tc-auto0001.bats",
		"wiring artefact should use bare TC ID for bare spec")
}

// loadTestConfig loads a gtms.config from the given root directory.
// planFor supplies the planned artefact path that the invocation boundary
// carries in production. BuiltinAutomate refuses to run without it (the carrier
// is load-bearing, not a hint), so a test driving the adapter directly has to
// plan the same way Invoke does -- through the one shared planner, never by
// spelling a path of its own.
//
// A framework with no registered support plans nothing; BuiltinAutomate then
// still reports the unsupported-framework failure first, as it always has.
func planFor(ctx *AdapterContext) *AdapterContext {
	support := LookupFrameworkSupport(ctx.Framework)
	ctx.PlannedArtefact = PlanArtefactPath(ctx.ProjectRoot, support, ctx.OutputDir,
		ctx.OutputDirConfigured, ctx.OutputSubdir, ctx.TestCaseFile, ctx.TestCase)
	return ctx
}

func loadTestConfig(root string) (*config.Config, error) {
	return config.LoadFromFile(filepath.Join(root, "gtms.config"))
}

// --- ENH-161: template-driven create + prime tests ---

// TestYamlQuotedScalarOrEmpty covers the substitution helper that converts a
// user-controlled value into either a complete YAML double-quoted scalar
// (carrying its own quotes) or the empty string. This is the contract the
// template line `requirement: ${REQUIREMENT}` relies on: the placeholder
// either expands to a properly-quoted scalar OR vanishes, never to a bare
// unquoted scalar carrying YAML-special characters.
func TestYamlQuotedScalarOrEmpty(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"plain", "REQ-001", `"REQ-001"`},
		// Bug regression: `--reference='REQ: value'` previously substituted
		// unquoted, yielding `requirement: REQ: value` (two `: ` mapping
		// starts) which broke YAML parsing.
		{"colon space", "REQ: value", `"REQ: value"`},
		{"leading dash", "-not-a-sequence", `"-not-a-sequence"`},
		{"hash comment", "REQ #42", `"REQ #42"`},
		{"backslash", `dir\path`, `"dir\\path"`},
		{"double quote", `say "hi"`, `"say \"hi\""`},
		{"newline", "line1\nline2", `"line1\nline2"`},
		{"tab", "a\tb", `"a\tb"`},
		{"all risk chars", `/&\":'#*?` + "-prefix", `"/&\\\":'#*?-prefix"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, yamlQuotedScalarOrEmpty(tc.in))
		})
	}
}

// TestBuiltinCreate_ReadsFromTemplateFile verifies the template-read code
// path: BuiltinCreate substitutes placeholders into content sourced from
// ctx.TemplateFile rather than the inline fallback.
func TestBuiltinCreate_ReadsFromTemplateFile(t *testing.T) {
	dir := t.TempDir()
	tmplDir := t.TempDir()
	tmplPath := filepath.Join(tmplDir, "manual-testcase.template.md")
	// Custom template content -- includes a marker not in the fallback so we
	// can prove the on-disk template was used.
	require.NoError(t, os.WriteFile(tmplPath, []byte(`---
gtms_schema: "test-case/v2"
type: Test Case Spec
test_type: Functional
test_case_id: ${TESTCASE_ID}
title: "${TITLE}"
requirement: ${REQUIREMENT}
priority: High
created: ${CREATED}
---

## Custom Marker From Template File

`), 0644))

	ctx := &AdapterContext{
		OutputDir:    dir,
		TemplateFile: tmplPath,
		TestCaseIDs:  "tc-aabbccdd",
		Reference:    "REQ-fromTemplate",
	}
	result, err := BuiltinCreate(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Empty(t, result.Stderr, "no fallback warning when template exists")

	data, err := os.ReadFile(result.SavedFiles[0])
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "## Custom Marker From Template File")
	assert.Contains(t, content, `requirement: "REQ-fromTemplate"`)
	assert.Contains(t, content, "test_case_id: tc-aabbccdd")
	// Frontmatter must be parseable.
	var fm struct {
		TestCaseID  string `yaml:"test_case_id"`
		Requirement string `yaml:"requirement"`
	}
	_, parseErr := frontmatter.Parse(strings.NewReader(content), &fm)
	require.NoError(t, parseErr)
	assert.Equal(t, "tc-aabbccdd", fm.TestCaseID)
	assert.Equal(t, "REQ-fromTemplate", fm.Requirement)
}

// TestBuiltinCreate_FallbackOnMissingTemplate verifies the missing-template
// fallback per AC #15: stderr warning naming the expected path, fallback
// shape, exit 0.
func TestBuiltinCreate_FallbackOnMissingTemplate(t *testing.T) {
	dir := t.TempDir()
	missingPath := filepath.Join(t.TempDir(), "does-not-exist.template.md")

	ctx := &AdapterContext{
		OutputDir:    dir,
		TemplateFile: missingPath,
		TestCaseIDs:  "tc-aabbccdd",
	}
	result, err := BuiltinCreate(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode, "missing template is non-fatal per AC #15")
	assert.Contains(t, result.Stderr, "warning:")
	assert.Contains(t, result.Stderr, missingPath, "stderr names the expected path")

	data, err := os.ReadFile(result.SavedFiles[0])
	require.NoError(t, err)
	// Fallback shape contains the same headings as TestcaseTemplateMD.
	assert.Contains(t, string(data), "## Test Objective")
	assert.Contains(t, string(data), "## Test Steps")
}

// TestBuiltinCreate_NonMissingReadErrorSurfaces verifies that read errors
// other than ENOENT (e.g. the template path points at a directory, or the
// file is unreadable) surface as a real error rather than silently
// triggering the fallback. The fallback is reserved for the genuinely
// "template not found" case; everything else is operator-actionable.
func TestBuiltinCreate_NonMissingReadErrorSurfaces(t *testing.T) {
	dir := t.TempDir()
	// Point TemplateFile at a directory -- os.ReadFile returns an "is a
	// directory" error, which is portable between Linux and Windows and
	// is NOT errors.Is(err, os.ErrNotExist).
	dirAsTemplate := filepath.Join(dir, "template-as-dir")
	require.NoError(t, os.Mkdir(dirAsTemplate, 0755))

	ctx := &AdapterContext{
		OutputDir:    dir,
		TemplateFile: dirAsTemplate,
		TestCaseIDs:  "tc-aabbccdd",
	}
	_, err := BuiltinCreate(ctx)
	require.Error(t, err, "non-missing read error must surface")
	assert.Contains(t, err.Error(), "reading create template")
	assert.Contains(t, err.Error(), dirAsTemplate)
}

// TestBuiltinPrime_NonMissingReadErrorSurfaces mirrors the create-side check
// for BuiltinPrime: non-ENOENT read errors must surface, not silently fall
// back.
func TestBuiltinPrime_NonMissingReadErrorSurfaces(t *testing.T) {
	dir := t.TempDir()
	dirAsTemplate := filepath.Join(dir, "template-as-dir")
	require.NoError(t, os.Mkdir(dirAsTemplate, 0755))

	ctx := &AdapterContext{
		TemplateFile: dirAsTemplate,
		OutputFile:   filepath.Join(dir, "out.yaml"),
		TestCase:     "tc-aabbccdd",
	}
	_, err := BuiltinPrime(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading prime template")
	assert.Contains(t, err.Error(), dirAsTemplate)
}

// TestBuiltinCreate_ReferenceWithColon is the regression test for the bug
// where `--reference='REQ: value'` produced unparseable frontmatter
// (`requirement: REQ: value`). After the fix, the value must round-trip
// through YAML parsing intact.
func TestBuiltinCreate_ReferenceWithColon(t *testing.T) {
	dir := t.TempDir()
	ctx := &AdapterContext{
		OutputDir:   dir,
		TestCaseIDs: "tc-aabbccdd",
		Reference:   "REQ: value",
	}
	result, err := BuiltinCreate(ctx)
	require.NoError(t, err)

	data, err := os.ReadFile(result.SavedFiles[0])
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, `requirement: "REQ: value"`, "colon-bearing reference is quoted")

	// Frontmatter must parse cleanly; this is what the bug broke.
	var fm struct {
		Requirement string `yaml:"requirement"`
	}
	_, parseErr := frontmatter.Parse(strings.NewReader(content), &fm)
	require.NoError(t, parseErr, "frontmatter must be parseable YAML")
	assert.Equal(t, "REQ: value", fm.Requirement, "value round-trips through YAML")
}

// TestBuiltinCreate_ReferenceWithRiskSurfaceChars covers AC #25's worst-case
// character coverage for ${REQUIREMENT}: `/`, `&`, `\`, `"`, `'`, leading
// `-`, and YAML-sensitive `:`, `#`, `*`, `?`.
func TestBuiltinCreate_ReferenceWithRiskSurfaceChars(t *testing.T) {
	dir := t.TempDir()
	worstCase := `-REQ/path&branch\quote:"value"'apos'#star*question?`

	ctx := &AdapterContext{
		OutputDir:   dir,
		TestCaseIDs: "tc-aabbccdd",
		Reference:   worstCase,
	}
	result, err := BuiltinCreate(ctx)
	require.NoError(t, err)

	data, err := os.ReadFile(result.SavedFiles[0])
	require.NoError(t, err)
	content := string(data)

	var fm struct {
		Requirement string `yaml:"requirement"`
	}
	_, parseErr := frontmatter.Parse(strings.NewReader(content), &fm)
	require.NoError(t, parseErr, "risk-surface chars must not break YAML")
	assert.Equal(t, worstCase, fm.Requirement, "worst-case value round-trips intact")
}

// TestBuiltinCreate_EmptyReferenceRendersBare covers AC #14: with no
// `--reference`, ${REQUIREMENT} substitutes to the empty string and the
// rendered line is `requirement: ` (bare key, parseable YAML, null value).
func TestBuiltinCreate_EmptyReferenceRendersBare(t *testing.T) {
	dir := t.TempDir()
	ctx := &AdapterContext{
		OutputDir:   dir,
		TestCaseIDs: "tc-aabbccdd",
		// Reference intentionally empty.
	}
	result, err := BuiltinCreate(ctx)
	require.NoError(t, err)

	data, err := os.ReadFile(result.SavedFiles[0])
	require.NoError(t, err)
	content := string(data)

	// Bare key with no value (no quotes inserted). Parseable as null.
	assert.Regexp(t, `(?m)^requirement: *$`, content, "empty reference yields bare key line")

	var fm struct {
		Requirement string `yaml:"requirement"`
	}
	_, parseErr := frontmatter.Parse(strings.NewReader(content), &fm)
	require.NoError(t, parseErr)
	assert.Empty(t, fm.Requirement, "empty value parses as empty string")
}

// TestBuiltinCreate_FallbackIsByteIdenticalToScaffold enforces the AC #15
// invariant that the missing-template fallback shape (adapter-side Go const)
// matches the scaffolded template content (scaffold-side Go const)
// byte-for-byte. Drift between the two would cause Tier 0 fallback output to
// differ from the on-disk template's day-one output, breaking handoff parity.
//
// Both constants are compiled by the same Go toolchain so line-ending and
// encoding normalisation is identical -- the comparison is a true byte
// equality check, not a source-text scan.
func TestBuiltinCreate_FallbackIsByteIdenticalToScaffold(t *testing.T) {
	assert.Equal(t,
		testcaseTemplateFallback,
		scaffold.TestcaseTemplateMD,
		"fallback shape must match scaffold.TestcaseTemplateMD byte-for-byte")
}

// TestBuiltinPrime_FallbackOnMissingTemplate verifies the prime-side missing
// template fallback (AC #15 mirror for prime).
func TestBuiltinPrime_FallbackOnMissingTemplate(t *testing.T) {
	dir := t.TempDir()
	missingPath := filepath.Join(t.TempDir(), "no-such-template.yaml")
	outPath := filepath.Join(dir, "tc-aabbccdd--manual.result.yaml")

	ctx := &AdapterContext{
		TemplateFile: missingPath,
		OutputFile:   outPath,
		TestCase:     "tc-aabbccdd",
		TestCaseHash: "abcdef0123456789",
		Branch:       "main",
	}
	result, err := BuiltinPrime(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stderr, "warning:")
	assert.Contains(t, result.Stderr, missingPath, "stderr names the expected path")

	data, err := os.ReadFile(outPath)
	require.NoError(t, err)
	// Fallback shape contains the GTMS contract block.
	assert.Contains(t, string(data), "test_case_id: tc-aabbccdd")
	assert.Contains(t, string(data), "framework: manual")
}

// TestBuiltinPrime_TCSnapshotFieldsWithRiskChars covers AC #25's prime-stage
// risk surface: TC frontmatter fields snapshotted at prime time may carry
// quotes, colons, backslashes, etc. via operator edits to the TC file. The
// stamped result must remain parseable YAML.
func TestBuiltinPrime_TCSnapshotFieldsWithRiskChars(t *testing.T) {
	dir := t.TempDir()
	tcPath := filepath.Join(dir, "gtms", "test", "cases", "tc-aabbccdd-risk.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(tcPath), 0o755))
	doc := `---
gtms_schema: "test-case/v2"
type: Test Case Spec
test_type: 'Functional #1'
test_case_id: tc-aabbccdd
title: 'Login: error "msg" with \ slash'
requirement: 'REQ: value-with-colon'
priority: '-Medium'
created: 2026-09-26
---
`
	require.NoError(t, os.WriteFile(tcPath, []byte(doc), 0o644))
	owner, err := testcase.ResolveOwner(dir, "tc-aabbccdd")
	require.NoError(t, err)
	fresh, violations := ValidateTestCaseOwnerFresh(dir, owner)
	require.Empty(t, violations)

	tmplPath := filepath.Join(dir, "manual-result.template.yaml")
	require.NoError(t, os.WriteFile(tmplPath, []byte(`test_case_id: ${TESTCASE}
test_case_hash: ${TESTCASE_HASH}
framework: manual
result:
title: "${TC_TITLE}"
requirement: "${TC_REQUIREMENT}"
priority: "${TC_PRIORITY}"
test_type: "${TEST_TYPE}"
branch: ${BRANCH}
`), 0644))

	outPath := filepath.Join(dir, "tc-aabbccdd--manual.result.yaml")
	ctx := &AdapterContext{
		Owner:        owner,
		TemplateFile: tmplPath,
		OutputFile:   outPath,
		TestCase:     "tc-aabbccdd",
		Branch:       "main",
		Force:        false,
	}
	populateBuiltinPrimeFields(ctx, dir, "tc-aabbccdd", "manual-prime", fresh)
	outPath = ctx.OutputFile
	result, err := BuiltinPrime(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)

	data, err := os.ReadFile(outPath)
	require.NoError(t, err)
	var fm struct {
		Title       string `yaml:"title"`
		Requirement string `yaml:"requirement"`
		Priority    string `yaml:"priority"`
		TestType    string `yaml:"test_type"`
	}
	require.NoError(t, yaml.Unmarshal(data, &fm), "stamped result must be parseable YAML")
	assert.Equal(t, ctx.TCTitle, fm.Title)
	assert.Equal(t, ctx.TCRequirement, fm.Requirement)
	assert.Equal(t, ctx.TCPriority, fm.Priority)
	assert.Equal(t, ctx.TestType, fm.TestType)
}

func TestPopulateBuiltinPrimeFields_UsesFreshNormalizedTestType(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "gtms", "test", "cases", "tc-aabbccdd-security.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.WriteFile(abs, strictOwnedSpec("tc-aabbccdd", "Fresh title", "ENH-219", "High", "Security", "\n", false), 0o644))
	owner, err := testcase.ResolveOwner(root, "tc-aabbccdd")
	require.NoError(t, err)
	fresh, violations := ValidateTestCaseOwnerFresh(root, owner)
	require.Empty(t, violations)

	ctx := &AdapterContext{Owner: owner}
	populateBuiltinPrimeFields(ctx, root, "tc-aabbccdd", "manual-prime", fresh)
	assert.Equal(t, fresh.Hash, ctx.TestCaseHash)
	assert.Equal(t, "Fresh title", ctx.TCTitle)
	assert.Equal(t, "ENH-219", ctx.TCRequirement)
	assert.Equal(t, "High", ctx.TCPriority)
	assert.Equal(t, "Security", ctx.TestType)
}

func TestBuiltinPrime_SubstitutesTEST_TYPE(t *testing.T) {
	dir := t.TempDir()
	templatePath := filepath.Join(dir, "manual-result.template.yaml")
	outputPath := filepath.Join(dir, "result.yaml")
	require.NoError(t, os.WriteFile(templatePath, []byte("test_type: \"${TEST_TYPE}\"\n"), 0o644))
	ctx := &AdapterContext{
		TemplateFile: templatePath,
		OutputFile:   outputPath,
		TestType:     "Security",
	}
	result, err := BuiltinPrime(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, result.ExitCode)
	data, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	assert.Equal(t, "test_type: \"Security\"\n", string(data))
	assert.NotContains(t, string(data), "${TEST_TYPE}")
}

func TestPrimeCategoryPropagation_BuiltinAndScripted(t *testing.T) {
	skipIfShort(t)
	dir := t.TempDir()
	templatePath := filepath.Join(dir, "manual-result.template.yaml")
	builtinPath := filepath.Join(dir, "builtin-prime.yaml")
	require.NoError(t, os.WriteFile(templatePath, []byte("test_type: \"${TEST_TYPE}\"\n"), 0o644))
	builtinCtx := &AdapterContext{TemplateFile: templatePath, OutputFile: builtinPath, TestType: "Security"}
	result, err := BuiltinPrime(builtinCtx)
	require.NoError(t, err)
	require.Equal(t, 0, result.ExitCode)
	builtinData, err := os.ReadFile(builtinPath)
	require.NoError(t, err)

	scriptPath := filepath.Join(dir, "prime.sh")
	scriptedPath := filepath.Join(dir, "scripted-prime.yaml")
	script := `#!/bin/sh
printf 'test_type: "%s"\n' "$GTMS_TEST_TYPE" > "$GTMS_OUTPUT_FILE"
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o755))
	scriptedCtx := &AdapterContext{TestType: "Security", OutputFile: scriptedPath, OutputDir: dir, ProjectRoot: dir, WorkDir: dir}
	scriptedResult, err := InvokeTier2(context.Background(), scriptedCtx, scriptPath)
	require.NoError(t, err)
	require.Equal(t, 0, scriptedResult.ExitCode)
	scriptedData, err := os.ReadFile(scriptedPath)
	require.NoError(t, err)

	assert.Equal(t, "test_type: \"Security\"\n", string(builtinData))
	assert.Equal(t, builtinData, scriptedData)
	assert.NotContains(t, string(scriptedData), "Test Case Spec")
}

// --- ENH-162: BuiltinAutomate template-orchestration tests ---

func TestBuiltinAutomate_ReadsTemplateFromDisk(t *testing.T) {
	root := setupAutomateFixture(t)
	cfg, err := loadTestConfig(root)
	require.NoError(t, err)

	// Write a custom BATS template with a sentinel
	tmplDir := filepath.Join(root, "gtms", "automation", "templates")
	require.NoError(t, os.MkdirAll(tmplDir, 0755))
	customTmpl := `#!/usr/bin/env bats
# CUSTOM TEMPLATE SENTINEL
# Test case: ${TESTCASE_ID}
setup_file() {
    export PROJECT_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/${PROJECT_ROOT_DEPTH}" && pwd)"
    load "$PROJECT_ROOT/test/test_helper/common-setup.bash"
}
setup() { _common_setup; }
@test "${TESTCASE_ID}: custom" { skip "skeleton"; }
teardown() { :; }
`
	require.NoError(t, os.WriteFile(filepath.Join(tmplDir, "bats.template.bats"), []byte(customTmpl), 0644))

	ctx := &AdapterContext{
		ProjectRoot:  root,
		TestCase:     "tc-auto0001",
		Framework:    "bats",
		TestCaseFile: "gtms/test/cases/my-feature/tc-auto0001-test-skeleton.md",
		OutputSubdir: "my-feature/",
	}

	result, err := BuiltinAutomate(planFor(ctx), cfg)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Empty(t, result.Stderr, "no warning when template exists")

	data, err := os.ReadFile(result.SavedFiles[0])
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "CUSTOM TEMPLATE SENTINEL", "should read from disk template")
	assert.Contains(t, content, "tc-auto0001", "should substitute TC ID")
	assert.NotContains(t, content, "${TESTCASE_ID}", "placeholder should be substituted")
}

func TestBuiltinAutomate_FallbackOnMissingTemplate(t *testing.T) {
	root := setupAutomateFixture(t)
	cfg, err := loadTestConfig(root)
	require.NoError(t, err)

	// Do NOT create the template file -- it should be missing
	ctx := &AdapterContext{
		ProjectRoot:  root,
		TestCase:     "tc-auto0001",
		Framework:    "bats",
		TestCaseFile: "gtms/test/cases/my-feature/tc-auto0001-test-skeleton.md",
		OutputSubdir: "my-feature/",
	}

	result, err := BuiltinAutomate(planFor(ctx), cfg)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode, "missing template is non-fatal")
	assert.Contains(t, result.Stderr, "warning:")
	assert.Contains(t, result.Stderr, "bats.template.bats")
	assert.Contains(t, result.Stderr, "using built-in default")

	// Output should still be a valid BATS skeleton
	data, err := os.ReadFile(result.SavedFiles[0])
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "#!/usr/bin/env bats")
	assert.Contains(t, content, "tc-auto0001")
	assert.Contains(t, content, "_common_setup")
}

func TestBuiltinAutomate_NonMissingReadErrorSurfaces(t *testing.T) {
	root := setupAutomateFixture(t)
	cfg, err := loadTestConfig(root)
	require.NoError(t, err)

	// Create a directory at the template path
	tmplDir := filepath.Join(root, "gtms", "automation", "templates")
	require.NoError(t, os.MkdirAll(tmplDir, 0755))
	tmplPath := filepath.Join(tmplDir, "bats.template.bats")
	require.NoError(t, os.MkdirAll(tmplPath, 0755)) // directory, not a file

	ctx := &AdapterContext{
		ProjectRoot:  root,
		TestCase:     "tc-auto0001",
		Framework:    "bats",
		TestCaseFile: "gtms/test/cases/my-feature/tc-auto0001-test-skeleton.md",
		OutputSubdir: "my-feature/",
	}

	_, err = BuiltinAutomate(planFor(ctx), cfg)
	require.Error(t, err, "directory-at-template-path must error")
	assert.Contains(t, err.Error(), "reading bats automate template")
}

func TestBuiltinAutomate_FallbackByteIdenticalToTemplateRead(t *testing.T) {
	// Run BuiltinAutomate twice: once without template (fallback), once with
	// the template file containing the exact same const. The output artefacts
	// must be byte-identical.
	root1 := setupAutomateFixture(t)
	root2 := setupAutomateFixture(t)
	cfg1, err := loadTestConfig(root1)
	require.NoError(t, err)
	cfg2, err := loadTestConfig(root2)
	require.NoError(t, err)

	ctx1 := &AdapterContext{
		ProjectRoot:  root1,
		TestCase:     "tc-auto0001",
		Framework:    "bats",
		TestCaseFile: "gtms/test/cases/my-feature/tc-auto0001-test-skeleton.md",
		OutputSubdir: "my-feature/",
	}

	// root1: no template -> fallback path
	result1, err := BuiltinAutomate(planFor(ctx1), cfg1)
	require.NoError(t, err)
	assert.Contains(t, result1.Stderr, "warning:", "fallback path should warn")

	// root2: template from the const -> template-read path
	tmplDir := filepath.Join(root2, "gtms", "automation", "templates")
	require.NoError(t, os.MkdirAll(tmplDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tmplDir, "bats.template.bats"),
		[]byte(scaffold.BATSAutomateTemplate), 0644))

	ctx2 := &AdapterContext{
		ProjectRoot:  root2,
		TestCase:     "tc-auto0001",
		Framework:    "bats",
		TestCaseFile: "gtms/test/cases/my-feature/tc-auto0001-test-skeleton.md",
		OutputSubdir: "my-feature/",
	}
	result2, err := BuiltinAutomate(planFor(ctx2), cfg2)
	require.NoError(t, err)
	assert.Empty(t, result2.Stderr, "template-read path should not warn")

	data1, err := os.ReadFile(result1.SavedFiles[0])
	require.NoError(t, err)
	data2, err := os.ReadFile(result2.SavedFiles[0])
	require.NoError(t, err)

	assert.Equal(t, string(data1), string(data2),
		"fallback output must be byte-identical to template-read output when template matches const")
}
