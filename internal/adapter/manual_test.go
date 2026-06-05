package adapter

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/aitestmanagement/gtms-cli/internal/task"
)

// --- parseManualResultFile tests ---

func TestParseManualResultFile_ValidPass(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.yaml")
	content := `test_case_id: tc-12345678
test_case_hash: abcdef0123456789
framework: manual
result: pass
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	mrf, err := parseManualResultFile(path)
	require.NoError(t, err)
	assert.Equal(t, "tc-12345678", mrf.TestCase)
	assert.Equal(t, "abcdef0123456789", mrf.TestCaseHash)
	assert.Equal(t, "manual", mrf.Framework)
	assert.Equal(t, "pass", mrf.Result)
}

func TestParseManualResultFile_ValidFail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.yaml")
	content := `test_case_id: tc-aabbccdd
test_case_hash: 1234567890abcdef
framework: manual
result: fail
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	mrf, err := parseManualResultFile(path)
	require.NoError(t, err)
	assert.Equal(t, "fail", mrf.Result)
}

func TestParseManualResultFile_ValidSkip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.yaml")
	content := `test_case_id: tc-aabbccdd
test_case_hash: 1234567890abcdef
framework: manual
result: skip
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	mrf, err := parseManualResultFile(path)
	require.NoError(t, err)
	assert.Equal(t, "skip", mrf.Result)
}

func TestParseManualResultFile_MalformedYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.yaml")
	content := `test_case_id: tc-12345678
  broken: indentation: wrong
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	_, err := parseManualResultFile(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not parse")
	assert.Contains(t, err.Error(), "result.yaml")
}

func TestParseManualResultFile_FileNotFound(t *testing.T) {
	_, err := parseManualResultFile("/nonexistent/path/result.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not read")
}

func TestParseManualResultFile_WithSchemaDirective(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.yaml")
	content := `# yaml-language-server: $schema=../../schemas/manual-result.schema.json
test_case_id: tc-12345678
test_case_hash: abcdef0123456789
framework: manual
result: pass
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	mrf, err := parseManualResultFile(path)
	require.NoError(t, err)
	assert.Equal(t, "pass", mrf.Result)
}

func TestParseManualResultFile_AdditionalFieldsIgnored(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.yaml")
	content := `test_case_id: tc-12345678
test_case_hash: abcdef0123456789
framework: manual
result: pass
branch: main
executed_by: alice
custom_field: some-value
`
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	mrf, err := parseManualResultFile(path)
	require.NoError(t, err)
	assert.Equal(t, "pass", mrf.Result)
}

// --- YAML null form tests ---

func TestParseManualResultFile_NullForms(t *testing.T) {
	// All YAML null forms should normalise to empty string for `result:`
	nullForms := []struct {
		name    string
		content string
	}{
		{"bare_empty", "test_case_id: tc-12345678\ntest_case_hash: abcdef0123456789\nframework: manual\nresult:\n"},
		{"explicit_null", "test_case_id: tc-12345678\ntest_case_hash: abcdef0123456789\nframework: manual\nresult: null\n"},
		{"tilde_null", "test_case_id: tc-12345678\ntest_case_hash: abcdef0123456789\nframework: manual\nresult: ~\n"},
		{"empty_double_quotes", "test_case_id: tc-12345678\ntest_case_hash: abcdef0123456789\nframework: manual\nresult: \"\"\n"},
		{"empty_single_quotes", "test_case_id: tc-12345678\ntest_case_hash: abcdef0123456789\nframework: manual\nresult: ''\n"},
	}

	for _, tc := range nullForms {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "result.yaml")
			require.NoError(t, os.WriteFile(path, []byte(tc.content), 0644))

			mrf, err := parseManualResultFile(path)
			require.NoError(t, err)
			assert.Empty(t, mrf.Result, "YAML null form %q should normalise to empty string", tc.name)
		})
	}
}

// --- validateManualResultFile tests ---

func TestValidateManualResultFile_ValidPass(t *testing.T) {
	mrf := &ManualResultFile{
		TestCase:     "tc-12345678",
		TestCaseHash: "abcdef0123456789",
		Framework:    "manual",
		Result:       "pass",
	}
	err := validateManualResultFile(mrf, "tc-12345678", "manual")
	assert.NoError(t, err)
}

func TestValidateManualResultFile_ValidFail(t *testing.T) {
	mrf := &ManualResultFile{
		TestCase:     "tc-12345678",
		TestCaseHash: "abcdef0123456789",
		Framework:    "manual",
		Result:       "fail",
	}
	err := validateManualResultFile(mrf, "tc-12345678", "manual")
	assert.NoError(t, err)
}

func TestValidateManualResultFile_ValidSkip(t *testing.T) {
	mrf := &ManualResultFile{
		TestCase:     "tc-12345678",
		TestCaseHash: "abcdef0123456789",
		Framework:    "manual",
		Result:       "skip",
	}
	err := validateManualResultFile(mrf, "tc-12345678", "manual")
	assert.NoError(t, err)
}

func TestValidateManualResultFile_MissingTestcase(t *testing.T) {
	mrf := &ManualResultFile{
		TestCaseHash: "abcdef0123456789",
		Framework:    "manual",
		Result:       "pass",
	}
	err := validateManualResultFile(mrf, "tc-12345678", "manual")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "required field missing: test_case_id")
}

func TestValidateManualResultFile_TestcaseMismatch(t *testing.T) {
	mrf := &ManualResultFile{
		TestCase:     "tc-aaaaaaaa",
		TestCaseHash: "abcdef0123456789",
		Framework:    "manual",
		Result:       "pass",
	}
	err := validateManualResultFile(mrf, "tc-12345678", "manual")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "test_case_id mismatch")
}

func TestValidateManualResultFile_MissingTestcaseHash(t *testing.T) {
	mrf := &ManualResultFile{
		TestCase:  "tc-12345678",
		Framework: "manual",
		Result:    "pass",
	}
	err := validateManualResultFile(mrf, "tc-12345678", "manual")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "required field missing: test_case_hash")
}

func TestValidateManualResultFile_InvalidTestcaseHashFormat(t *testing.T) {
	mrf := &ManualResultFile{
		TestCase:     "tc-12345678",
		TestCaseHash: "TOOSHORT",
		Framework:    "manual",
		Result:       "pass",
	}
	err := validateManualResultFile(mrf, "tc-12345678", "manual")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid test_case_hash format")
}

func TestValidateManualResultFile_MissingFramework(t *testing.T) {
	mrf := &ManualResultFile{
		TestCase:     "tc-12345678",
		TestCaseHash: "abcdef0123456789",
		Result:       "pass",
	}
	err := validateManualResultFile(mrf, "tc-12345678", "manual")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "required field missing: framework")
}

func TestValidateManualResultFile_FrameworkMismatch(t *testing.T) {
	mrf := &ManualResultFile{
		TestCase:     "tc-12345678",
		TestCaseHash: "abcdef0123456789",
		Framework:    "bats",
		Result:       "pass",
	}
	err := validateManualResultFile(mrf, "tc-12345678", "manual")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "framework mismatch")
}

func TestValidateManualResultFile_EmptyResult(t *testing.T) {
	mrf := &ManualResultFile{
		TestCase:     "tc-12345678",
		TestCaseHash: "abcdef0123456789",
		Framework:    "manual",
		Result:       "",
	}
	err := validateManualResultFile(mrf, "tc-12345678", "manual")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "result field is empty")
}

func TestValidateManualResultFile_InvalidResultValue(t *testing.T) {
	invalidValues := []string{"paas", "Pass", "passed", "PASS", "error", "skipped", "1", "true"}
	for _, val := range invalidValues {
		t.Run(val, func(t *testing.T) {
			mrf := &ManualResultFile{
				TestCase:     "tc-12345678",
				TestCaseHash: "abcdef0123456789",
				Framework:    "manual",
				Result:       val,
			}
			err := validateManualResultFile(mrf, "tc-12345678", "manual")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "invalid result value")
			assert.Contains(t, err.Error(), val)
			assert.Contains(t, err.Error(), "pass, fail, or skip")
		})
	}
}

func TestValidateManualResultFile_ResultErrorRejected(t *testing.T) {
	// The user-authored file does NOT allow result: error.
	// That asymmetry is by design (CON-020 Decision 5/8).
	mrf := &ManualResultFile{
		TestCase:     "tc-12345678",
		TestCaseHash: "abcdef0123456789",
		Framework:    "manual",
		Result:       "error",
	}
	err := validateManualResultFile(mrf, "tc-12345678", "manual")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid result value")
}

// --- testcaseHashPattern tests ---

func TestTestcaseHashPattern_Valid(t *testing.T) {
	assert.True(t, testcaseHashPattern.MatchString("abcdef0123456789"))
	assert.True(t, testcaseHashPattern.MatchString("0000000000000000"))
	assert.True(t, testcaseHashPattern.MatchString("ffffffffffffffff"))
}

func TestTestcaseHashPattern_Invalid(t *testing.T) {
	assert.False(t, testcaseHashPattern.MatchString("abc"))                  // too short
	assert.False(t, testcaseHashPattern.MatchString("ABCDEF0123456789"))     // uppercase
	assert.False(t, testcaseHashPattern.MatchString("abcdef01234567890"))    // too long (17)
	assert.False(t, testcaseHashPattern.MatchString("abcdef012345678"))      // too short (15)
	assert.False(t, testcaseHashPattern.MatchString("ghijkl0123456789"))     // non-hex
}

// --- populateManualExecuteFields tests (ENH-133 review-fix) ---

// TestPopulateManualExecuteFields_MissingResultFile_DirectsToPrime is the
// review-fix regression test for ENH-133: when the expected manual result
// file is absent on disk, the deferred error message must name `gtms prime`
// and `--framework manual` so the user has an actionable next step. This is
// the ONLY missing-artefact path the manual framework must take — the
// generic pre-check in cli/execute.go must no longer fire and shadow this
// hint.
//
// CON-023 / ENH-145: populateManualExecuteFields no longer consults the
// legacy automation record; it looks directly at
// gtms/manual/records/<tc>--manual.result.yaml. The test omits that file
// to exercise the missing-result-file branch.
func TestPopulateManualExecuteFields_MissingResultFile_DirectsToPrime(t *testing.T) {
	root := t.TempDir()
	target := "tc-deadbeef"
	framework := "manual"

	// No gtms/manual/records/<tc>--manual.result.yaml on disk — that's
	// the trigger for the deferred error.

	ctx := &AdapterContext{}
	populateManualExecuteFields(ctx, root, target, CommandFlags{Framework: framework})

	require.Error(t, ctx.ManualExecuteError, "missing manual result file must produce a deferred error")
	msg := ctx.ManualExecuteError.Error()

	// The actionable hint must name the corrective command AND the framework
	// flag, plus the missing path so the user knows which file is gone.
	assert.Contains(t, msg, "gtms prime", "error must name `gtms prime` as the corrective command")
	assert.Contains(t, msg, "--framework manual", "error must direct user to re-prime with --framework manual")
	assert.Contains(t, msg, target+"--manual.result.yaml", "error must reference the missing file path")
}

// TestPopulateManualExecuteFields_NoAutomationRecord_DirectsToPrime covers the
// upstream branch of the same hint: when there is no manual automation record
// at all (record path missing), the deferred error must still direct the user
// to `gtms prime --framework manual`.
func TestPopulateManualExecuteFields_NoAutomationRecord_DirectsToPrime(t *testing.T) {
	root := t.TempDir()
	target := "tc-cafef00d"

	// No automation record on disk.
	ctx := &AdapterContext{}
	populateManualExecuteFields(ctx, root, target, CommandFlags{Framework: "manual"})

	require.Error(t, ctx.ManualExecuteError)
	msg := ctx.ManualExecuteError.Error()
	assert.Contains(t, msg, "gtms prime")
	assert.Contains(t, msg, "--framework manual")
}

// --- Invoker deferred-error persistence path (ENH-133 review-fix round 2) ---

// TestInvoke_ManualDeferredError_PersistsToFailed verifies the happy-path
// persistence sequence in the manual-execute deferred-error branch:
//
//   - The result handoff is updated to status: error with the deferred summary.
//   - The task file is moved to gtms/tasks/error/.
//   - The InvokeResult carries Status: "error" and the same summary.
//   - When all three persistence operations succeed, no warnings are emitted.
//
// This is the baseline for the review-fix change. It exercises the "all
// writes succeeded" path so a regression that re-introduces silent
// swallowing would still surface (no warnings expected here, but the test
// catches changes that break the persistence chain itself).
func TestInvoke_ManualDeferredError_PersistsToFailed(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)
	target := "tc-deadbeef"

	// CON-023 / ENH-145: populateManualExecuteFields reads directly from
	// gtms/manual/records/<tc>--manual.result.yaml — the legacy automation
	// record is no longer consulted. Omitting the manual result file is
	// the trigger for ManualExecuteError.

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "execute",
		Name:    "manual-execute",
		Config: &config.AdapterConfig{
			Mode:      "sync",
			Script:    "gtms/adapters/manual-execute.sh",
			Framework: "manual",
		},
		Tier: 2,
		Mode: "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, target, CommandFlags{})
	require.NoError(t, err, "deferred manual error should be returned via InvokeResult, not as a Go error")
	require.NotNil(t, res)

	assert.Equal(t, "error", res.Status, "InvokeResult.Status must reflect the deferred error")
	assert.Contains(t, res.Summary, "manual result file not found", "summary must carry the actionable hint")
	assert.Contains(t, res.Summary, "gtms prime")
	assert.Empty(t, res.Warnings, "no persistence warnings expected when all writes succeed")

	// Task file must be in error/, not pending/ or in-progress/.
	errorTasks, err := task.List(root, "error")
	require.NoError(t, err)
	require.Len(t, errorTasks, 1, "task file must move to error/")
	assert.Equal(t, target, errorTasks[0].Target)
	assert.Equal(t, "execute", errorTasks[0].Type)
	assert.Contains(t, errorTasks[0].Error, "manual result file not found")

	pendingTasks, err := task.List(root, "pending")
	require.NoError(t, err)
	assert.Empty(t, pendingTasks, "no pending task should remain")

	// Result handoff must reflect status: error and carry the same summary.
	rcPath := result.ResultPath(root, res.TaskID)
	rc, err := result.Read(rcPath)
	require.NoError(t, err)
	assert.Equal(t, "error", rc.Status)
	assert.Contains(t, rc.Summary, "manual result file not found")
	assert.NotEmpty(t, rc.Completed, "completed timestamp must be set")
}

// --- buildAdapterContext predicate symmetry (ENH-133 review-fix round 4) ---

// TestBuildAdapterContext_ManualExecute_AdapterNameOnly_PopulatesManualFields
// pins the predicate symmetry between cli/execute.go and adapter/invoker.go:
// both must key on IsManualFramework(resolved), not on the framework string
// returned by ResolveFramework.
//
// Regression case: ResolvedAdapter{Name: "manual-execute",
// Config: &AdapterConfig{Framework: ""}}. The CLI defers the generic
// artefact pre-check because IsManualFramework returns true. Pre-fix the
// invoker used `ResolveFramework() == "manual"`, which falls through to the
// adapter-name fallback "manual-execute" — not "manual" — and therefore
// skipped populateManualExecuteFields. The Tier 2 manual-execute script
// would then run with no GTMS_RESULT_TEMPLATE / GTMS_RESULT_VALUE / etc.
// and fail late with a cryptic missing-env-var error instead of the
// Go-side validation error that names `gtms prime --framework manual`.
//
// Assertion: with no automation record on disk, populateManualExecuteFields
// sets ctx.ManualExecuteError to the actionable hint. A non-nil
// ManualExecuteError is sufficient evidence that the manual-fields path
// was entered — the manual_test.go suite above already covers what
// populateManualExecuteFields does once it's called.
func TestBuildAdapterContext_ManualExecute_AdapterNameOnly_PopulatesManualFields(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}
	resolved := &ResolvedAdapter{
		Command: "execute",
		Name:    "manual-execute",
		Config:  &config.AdapterConfig{Mode: "sync", Script: "gtms/adapters/manual-execute.sh"},
		Tier:    2,
		Mode:    "sync",
	}

	ac := buildAdapterContext(root, "task-test001", resolved, "tc-cafef00d", CommandFlags{}, "feature/test", cfg, root, "/tmp/result.yaml")

	require.NotNil(t, ac, "buildAdapterContext must not return nil")
	require.Error(t, ac.ManualExecuteError,
		"adapter-name-only manual-execute path must enter populateManualExecuteFields and defer the missing-record error")
	msg := ac.ManualExecuteError.Error()
	assert.Contains(t, msg, "gtms prime", "deferred error must name `gtms prime`")
	assert.Contains(t, msg, "--framework manual", "deferred error must direct user to --framework manual")
}

// TestBuildAdapterContext_NonManualExecute_DoesNotPopulateManualFields is the
// negative complement: a non-manual adapter must not enter the manual path
// even when the target has no automation record.
func TestBuildAdapterContext_NonManualExecute_DoesNotPopulateManualFields(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}
	resolved := &ResolvedAdapter{
		Command: "execute",
		Name:    "bats-runner",
		Config:  &config.AdapterConfig{Mode: "sync", Command: "bats {artefact}", Framework: "bats"},
		Tier:    1,
		Mode:    "sync",
	}

	ac := buildAdapterContext(root, "task-test002", resolved, "tc-cafef00d", CommandFlags{}, "feature/test", cfg, root, "/tmp/result.yaml")

	require.NotNil(t, ac)
	assert.NoError(t, ac.ManualExecuteError,
		"non-manual adapter must not enter populateManualExecuteFields")
	assert.Empty(t, ac.ResultTemplate, "manual-only context fields must remain empty for non-manual adapters")
	assert.Empty(t, ac.ResultValue)
}

