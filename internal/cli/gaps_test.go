package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/output"
	"github.com/aitestmanagement/gtms-cli/internal/reader"
)

// setupGapsFixture creates a fixture with gaps: test case but no spec file.
func setupGapsFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	// Test case with no automation spec → NoAutomation gap
	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-aaa1111-login-happy.md"), `---
test_case_id: tc-aaa1111
title: Login Happy Path
requirement: REQ-A
---
`)

	return root
}

// setupGapsNoGapsFixture creates a fixture with full coverage (no gaps).
func setupGapsNoGapsFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-aaa1111-login-happy.md"), `---
test_case_id: tc-aaa1111
title: Login Happy Path
requirement: REQ-A
---
`)

	// Spec file referencing tc-aaa1111 → covers NoAutomation
	writeTestFile(t, root, filepath.Join("gtms/automation", "specs", "tc-aaa1111-login.spec.ts"), `// tc-aaa1111
`)

	// Wiring + passing handoff for tc-aaa1111 (CON-023 / ENH-145).
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-aaa1111",
		Framework: "playwright",
		Result:    "pass",
		Attempts:  1,
	})

	return root
}

func TestRunGaps_JSON(t *testing.T) {
	root := setupGapsFixture(t)
	var buf bytes.Buffer

	err := runGaps(&buf, root, nil, true, "", false)
	require.NoError(t, err)

	out := buf.String()

	// Should be valid JSON
	var report reader.GapReport
	err = json.Unmarshal([]byte(out), &report)
	require.NoError(t, err, "Output should be valid JSON")

	// Should have gap in NoAutomation
	assert.NotEmpty(t, report.NoAutomation)
	assert.Equal(t, "tc-aaa1111", report.NoAutomation[0].ID)

	// No human-readable decorations
	assert.NotContains(t, out, "GAPS REPORT")
	assert.NotContains(t, out, "No coverage gaps found.")
}

func TestRunGaps_JSON_NoGaps(t *testing.T) {
	root := setupGapsNoGapsFixture(t)
	var buf bytes.Buffer

	err := runGaps(&buf, root, nil, true, "", false)
	require.NoError(t, err)

	out := buf.String()

	// Must be valid JSON, NOT "No coverage gaps found."
	assert.NotContains(t, out, "No coverage gaps found.")

	var report reader.GapReport
	err = json.Unmarshal([]byte(out), &report)
	require.NoError(t, err, "No-gaps JSON should still be valid JSON")

	assert.Empty(t, report.NoTests)
	assert.Empty(t, report.NoAutomation)
	assert.Empty(t, report.CurrentlyFailing)
	// CON-023 / ENH-146 wiring-aware categories must also be empty in a
	// fully-covered fixture.
	assert.Empty(t, report.StaleTestCaseHash)
	assert.Empty(t, report.StaleArtefactHash)
	assert.Empty(t, report.MissingArtefact)
}

func TestRunGaps_JSON_EmptyArrays(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer

	err := runGaps(&buf, root, nil, true, "", false)
	require.NoError(t, err)

	out := buf.String()

	// Nil slices must serialize as [] not null. CON-023 / ENH-146 retired
	// "never_executed" and "spec_but_no_record" (hidden via json:"-"); the
	// new wiring-aware categories take their place in the JSON shape.
	assert.Contains(t, out, `"no_tests": []`)
	assert.Contains(t, out, `"no_automation": []`)
	assert.Contains(t, out, `"currently_failing": []`)
	assert.Contains(t, out, `"stale_testcase_hash": []`)
	assert.Contains(t, out, `"stale_artefact_hash": []`)
	assert.Contains(t, out, `"missing_artefact": []`)
}

// --- Scope feedback tests (ENH-036) ---

func TestRunGaps_ScopeFeedback(t *testing.T) {
	root := setupGapsFixture(t)
	var buf bytes.Buffer

	scope := &reader.ScopeInfo{
		ScanDir:   filepath.Join(root, "gtms/cases"),
		RelPath:   "gtms/cases/login/",
		Recursive: false,
	}
	err := runGaps(&buf, root, scope, false, "", false)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "Scope: gtms/cases/login/")
	assert.Contains(t, out, "use -r for recursive")
}

func TestRunGaps_ScopeFeedbackRecursive(t *testing.T) {
	root := setupGapsFixture(t)
	var buf bytes.Buffer

	scope := &reader.ScopeInfo{
		ScanDir:   filepath.Join(root, "gtms/cases"),
		RelPath:   "gtms/cases/login/",
		Recursive: true,
	}
	err := runGaps(&buf, root, scope, false, "", false)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "Scope: gtms/cases/login/")
	assert.NotContains(t, out, "use -r for recursive")
}

// --- Folder summary tests (ENH-066) ---

func TestRunGapsFolderSummary_Table(t *testing.T) {
	root := t.TempDir()

	writeTestFile(t, root, filepath.Join("gtms/cases", "login", "tc-aaa1111-login.md"), `---
test_case_id: tc-aaa1111
title: Login Test
---
`)
	writeTestFile(t, root, filepath.Join("gtms/cases", "checkout", "tc-bbb1111-checkout.md"), `---
test_case_id: tc-bbb1111
title: Checkout Test
---
`)

	var buf bytes.Buffer
	err := runGapsFolderSummary(&buf, root, false, "")
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "FOLDER")
	assert.Contains(t, out, "NOT AUTOMATED")
	assert.Contains(t, out, "FAILING")
	assert.Contains(t, out, "login")
	assert.Contains(t, out, "checkout")
	// CON-023 / ENH-146: "Not run here" is not a gap. The NOT EXECUTED
	// column was retired from the folder-summary human surface.
	assert.NotContains(t, out, "NOT EXECUTED")
}

func TestRunGapsFolderSummary_JSON(t *testing.T) {
	root := t.TempDir()

	writeTestFile(t, root, filepath.Join("gtms/cases", "login", "tc-aaa1111-login.md"), `---
test_case_id: tc-aaa1111
title: Login Test
---
`)

	var buf bytes.Buffer
	err := runGapsFolderSummary(&buf, root, true, "")
	require.NoError(t, err)

	var entries []reader.GapsFolderSummaryEntry
	err = json.Unmarshal(buf.Bytes(), &entries)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}

func TestRunGapsFolderSummary_Empty(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer

	err := runGapsFolderSummary(&buf, root, false, "")
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "No test cases found.")
	assert.Contains(t, out, "gtms create")
}

func TestRunGaps_EmptyProjectMessage(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer

	scope := &reader.ScopeInfo{
		ScanDir:   filepath.Join(root, "gtms/cases"),
		RelPath:   "gtms/cases/",
		Recursive: true,
	}
	err := runGaps(&buf, root, scope, false, "", false)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "No test cases found.")
	assert.NotContains(t, out, "No coverage gaps found.")
}

func TestRunGaps_FullCoverageMessage(t *testing.T) {
	root := setupGapsNoGapsFixture(t)
	var buf bytes.Buffer

	scope := &reader.ScopeInfo{
		ScanDir:   filepath.Join(root, "gtms/cases"),
		RelPath:   "gtms/cases/",
		Recursive: true,
	}
	err := runGaps(&buf, root, scope, false, "", false)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "No coverage gaps found.")
	assert.NotContains(t, out, "No test cases found.")
}

// --- Recursive flat list tests (gtms gaps -r) ---

func setupGapsFolderFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	// Folder "login" — 2 TCs, 1 with automation
	writeTestFile(t, root, filepath.Join("gtms/cases", "login", "tc-aaa1111-login-happy.md"), `---
test_case_id: tc-aaa1111
title: Login Happy Path
requirement: REQ-A
---
`)
	writeTestFile(t, root, filepath.Join("gtms/cases", "login", "tc-aaa2222-login-error.md"), `---
test_case_id: tc-aaa2222
title: Login Error
requirement: REQ-A
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-aaa1111",
		Framework: "bats",
		Result:    "pass",
		Attempts:  1,
	})

	// Folder "checkout" — 1 TC, no automation
	writeTestFile(t, root, filepath.Join("gtms/cases", "checkout", "tc-bbb1111-checkout.md"), `---
test_case_id: tc-bbb1111
title: Checkout Flow
requirement: REQ-B
---
`)

	return root
}

func TestRunGaps_RecursiveFlatList(t *testing.T) {
	// Test that passing a recursive scope to runGaps shows individual gap entries
	// from subdirectories (the "gtms gaps -r" path).
	root := setupGapsFolderFixture(t)
	var buf bytes.Buffer

	scope := buildScopeFromArg(root, "", true)
	err := runGaps(&buf, root, scope, false, "", false)
	require.NoError(t, err)

	out := buf.String()
	// Should show individual gap entries, not folder summary
	assert.Contains(t, out, "GAPS REPORT")
	assert.NotContains(t, out, "FOLDER")
	// tc-aaa2222 and tc-bbb1111 have no automation — should appear as gaps
	assert.Contains(t, out, "tc-aaa2222")
	assert.Contains(t, out, "tc-bbb1111")
}

func TestRunGaps_RecursiveFlatList_JSON(t *testing.T) {
	root := setupGapsFolderFixture(t)
	var buf bytes.Buffer

	scope := buildScopeFromArg(root, "", true)
	err := runGaps(&buf, root, scope, true, "", false)
	require.NoError(t, err)

	var report reader.GapReport
	err = json.Unmarshal(buf.Bytes(), &report)
	require.NoError(t, err, "Output should be valid JSON")
	assert.Equal(t, 3, report.TotalTestCases, "Should include all TCs from all subfolders")
	assert.Len(t, report.NoAutomation, 2, "Two TCs without automation")
}

// --- ENH-094: RuntimeSkipped gap category ---

func TestRunGaps_RuntimeSkippedCategory(t *testing.T) {
	root := t.TempDir()

	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-skip01-skipped-test.md"), `---
test_case_id: tc-skip01
title: Skipped Test
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-skip01",
		Framework: "bats",
		Result:    "skipped",
	})

	var buf bytes.Buffer
	scope := buildScopeFromArg(root, "", true)
	err := runGaps(&buf, root, scope, false, "", false)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "Runtime-skipped tests: 1", "Must show runtime-skipped category with count")
	assert.Contains(t, out, "tc-skip01", "Must list the skipped TC")
}

func TestRunGaps_RuntimeSkippedJSON(t *testing.T) {
	root := t.TempDir()

	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-skip01-skipped-test.md"), `---
test_case_id: tc-skip01
title: Skipped Test
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-skip01",
		Framework: "bats",
		Result:    "skipped",
	})

	var buf bytes.Buffer
	scope := buildScopeFromArg(root, "", true)
	err := runGaps(&buf, root, scope, true, "", false)
	require.NoError(t, err)

	var report reader.GapReport
	err = json.Unmarshal(buf.Bytes(), &report)
	require.NoError(t, err)
	require.Len(t, report.RuntimeSkipped, 1, "JSON must include runtime_skipped category")
	assert.Equal(t, "tc-skip01", report.RuntimeSkipped[0].ID)
}

func TestRunGaps_RuntimeSkippedCountsInTotalGaps(t *testing.T) {
	root := t.TempDir()

	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-skip01-skipped-test.md"), `---
test_case_id: tc-skip01
title: Skipped Test
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-skip01",
		Framework: "bats",
		Result:    "skipped",
	})

	var buf bytes.Buffer
	scope := buildScopeFromArg(root, "", true)
	err := runGaps(&buf, root, scope, false, "", false)
	require.NoError(t, err)

	// Should NOT say "No coverage gaps found" since there IS a runtime-skipped gap
	out := buf.String()
	assert.NotContains(t, out, "No coverage gaps found.", "Runtime-skipped TC must count as a gap")
}

// --- CON-023 / ENH-146 review-fix-pass coverage ---

// TestRunGaps_AdapterError_SurfacesAsExecutionError covers the fix where
// terminal handoffs with status: error and an empty result still classify
// into ExecutionErrors. ENH-130 / CON-023 keeps the orthogonal status/result
// split on the result contract itself; the legacy carrier in the gaps
// classifier needs the error signal synthesised at overlay time.
func TestRunGaps_AdapterError_SurfacesAsExecutionError(t *testing.T) {
	root := t.TempDir()

	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-ae0001-adapter-error.md"), `---
test_case_id: tc-ae0001
title: Adapter error
requirement: REQ-E
---
`)
	// AdapterError=true seeds a result contract with status: error and no
	// result — the regression shape this fix guards against.
	seedLegacyRecord(t, root, legacyRecord{
		TC:           "tc-ae0001",
		Framework:    "bats",
		AdapterError: true,
	})

	var buf bytes.Buffer
	scope := buildScopeFromArg(root, "", true)
	err := runGaps(&buf, root, scope, true, "", false)
	require.NoError(t, err)

	var report reader.GapReport
	require.NoError(t, json.Unmarshal(buf.Bytes(), &report))
	require.Len(t, report.ExecutionErrors, 1,
		"adapter-error executions must surface in ExecutionErrors")
	assert.Equal(t, "tc-ae0001", report.ExecutionErrors[0].ID)
	// Adapter-error executions must NOT be double-classified as failing.
	assert.Empty(t, report.CurrentlyFailing,
		"adapter-error executions must not appear in CurrentlyFailing")
}

// TestRunGaps_MultiWiring_StaleAndCurrent ensures a TC with one current
// wiring record and one stale wiring record for a different framework still
// surfaces under the stale gap categories. Picker-only classification (the
// previous bug) hid the stale framework behind the current one.
func TestRunGaps_MultiWiring_StaleAndCurrent(t *testing.T) {
	root := t.TempDir()

	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-mw0001-multi.md"), `---
test_case_id: tc-mw0001
title: Multi-framework wiring
requirement: REQ-M
---
`)
	// Current bats wiring.
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-mw0001",
		Framework: "bats",
	})
	// Stale playwright wiring (wrong testcase-hash) using a distinct
	// artefact path so the two records do not clobber each other on disk.
	seedLegacyRecord(t, root, legacyRecord{
		TC:           "tc-mw0001",
		Framework:    "playwright",
		Artefact:     "test/specs/tc-mw0001.spec.ts",
		TestCaseHash: "deadbeefcafef00d",
	})

	// Default mode (no --framework): both wiring records considered; the
	// stale framework must still surface.
	var buf bytes.Buffer
	scope := buildScopeFromArg(root, "", true)
	err := runGaps(&buf, root, scope, true, "", false)
	require.NoError(t, err)
	var report reader.GapReport
	require.NoError(t, json.Unmarshal(buf.Bytes(), &report))
	require.Len(t, report.StaleTestCaseHash, 1,
		"stale playwright wiring must still surface even when bats is current")
	assert.Equal(t, "tc-mw0001", report.StaleTestCaseHash[0].ID)

	// Strict --framework bats suppresses the stale playwright gap.
	buf.Reset()
	err = runGaps(&buf, root, scope, true, "bats", true)
	require.NoError(t, err)
	report = reader.GapReport{}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &report))
	assert.Empty(t, report.StaleTestCaseHash,
		"strict --framework bats must filter out the playwright gap")

	// Strict --framework playwright reports the stale gap.
	buf.Reset()
	err = runGaps(&buf, root, scope, true, "playwright", true)
	require.NoError(t, err)
	report = reader.GapReport{}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &report))
	require.Len(t, report.StaleTestCaseHash, 1,
		"strict --framework playwright must report the stale gap")
	assert.Equal(t, "tc-mw0001", report.StaleTestCaseHash[0].ID)
}

// TestRunGaps_MultiWiring_PassAndFail verifies that a passing primary
// framework does not hide a failing sibling framework. CurrentlyFailing is a
// wiring-unit category per GapReport doc and ENH-146 §"Counting unit
// discipline".
func TestRunGaps_MultiWiring_PassAndFail(t *testing.T) {
	root := t.TempDir()

	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-mw0003-pass-fail.md"), `---
test_case_id: tc-mw0003
title: Multi-framework pass + fail
requirement: REQ-M
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-mw0003",
		Framework: "bats",
		Result:    "pass",
		Attempts:  1,
	})
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-mw0003",
		Framework: "playwright",
		Artefact:  "test/specs/tc-mw0003.spec.ts",
		Result:    "fail",
		Attempts:  1,
	})

	var buf bytes.Buffer
	scope := buildScopeFromArg(root, "", true)
	err := runGaps(&buf, root, scope, true, "", false)
	require.NoError(t, err)
	var report reader.GapReport
	require.NoError(t, json.Unmarshal(buf.Bytes(), &report))
	require.Len(t, report.CurrentlyFailing, 1,
		"failing playwright wiring must still surface even when bats is passing")
	assert.Equal(t, "tc-mw0003", report.CurrentlyFailing[0].ID)

	// Strict --framework bats suppresses the playwright fail.
	buf.Reset()
	err = runGaps(&buf, root, scope, true, "bats", true)
	require.NoError(t, err)
	report = reader.GapReport{}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &report))
	assert.Empty(t, report.CurrentlyFailing,
		"strict --framework bats must filter out the playwright fail")

	// Strict --framework playwright reports the fail.
	buf.Reset()
	err = runGaps(&buf, root, scope, true, "playwright", true)
	require.NoError(t, err)
	report = reader.GapReport{}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &report))
	require.Len(t, report.CurrentlyFailing, 1,
		"strict --framework playwright must report the fail")
	assert.Equal(t, "tc-mw0003", report.CurrentlyFailing[0].ID)
}

// TestRunGaps_MultiWiring_PassAndAdapterError verifies that an adapter-error
// sibling framework surfaces in ExecutionErrors even when the primary
// framework passes. Combined coverage with the adapter-error→ExecutionErrors
// synthesis in scanAutomationRecords.
func TestRunGaps_MultiWiring_PassAndAdapterError(t *testing.T) {
	root := t.TempDir()

	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-mw0004-pass-adapter-error.md"), `---
test_case_id: tc-mw0004
title: Multi-framework pass + adapter error
requirement: REQ-M
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-mw0004",
		Framework: "bats",
		Result:    "pass",
		Attempts:  1,
	})
	seedLegacyRecord(t, root, legacyRecord{
		TC:           "tc-mw0004",
		Framework:    "playwright",
		Artefact:     "test/specs/tc-mw0004.spec.ts",
		AdapterError: true,
	})

	var buf bytes.Buffer
	scope := buildScopeFromArg(root, "", true)
	err := runGaps(&buf, root, scope, true, "", false)
	require.NoError(t, err)
	var report reader.GapReport
	require.NoError(t, json.Unmarshal(buf.Bytes(), &report))
	require.Len(t, report.ExecutionErrors, 1,
		"adapter-error playwright wiring must still surface even when bats is passing")
	assert.Equal(t, "tc-mw0004", report.ExecutionErrors[0].ID)
	assert.Empty(t, report.CurrentlyFailing,
		"adapter-error executions must not appear in CurrentlyFailing")

	// Strict --framework bats hides the playwright adapter error.
	buf.Reset()
	err = runGaps(&buf, root, scope, true, "bats", true)
	require.NoError(t, err)
	report = reader.GapReport{}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &report))
	assert.Empty(t, report.ExecutionErrors,
		"strict --framework bats must filter out the playwright adapter error")
}

// TestRunGaps_MultiWiring_PassAndSkipped verifies that a runtime-skipped
// sibling framework surfaces in RuntimeSkipped even when the primary
// framework passes.
func TestRunGaps_MultiWiring_PassAndSkipped(t *testing.T) {
	root := t.TempDir()

	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-mw0005-pass-skip.md"), `---
test_case_id: tc-mw0005
title: Multi-framework pass + skip
requirement: REQ-M
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-mw0005",
		Framework: "bats",
		Result:    "pass",
		Attempts:  1,
	})
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-mw0005",
		Framework: "playwright",
		Artefact:  "test/specs/tc-mw0005.spec.ts",
		Result:    "skipped",
	})

	var buf bytes.Buffer
	scope := buildScopeFromArg(root, "", true)
	err := runGaps(&buf, root, scope, true, "", false)
	require.NoError(t, err)
	var report reader.GapReport
	require.NoError(t, json.Unmarshal(buf.Bytes(), &report))
	require.Len(t, report.RuntimeSkipped, 1,
		"runtime-skipped playwright wiring must still surface even when bats is passing")
	assert.Equal(t, "tc-mw0005", report.RuntimeSkipped[0].ID)

	// Strict --framework bats hides the playwright skip.
	buf.Reset()
	err = runGaps(&buf, root, scope, true, "bats", true)
	require.NoError(t, err)
	report = reader.GapReport{}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &report))
	assert.Empty(t, report.RuntimeSkipped,
		"strict --framework bats must filter out the playwright skip")
}

// TestRunGaps_MultiWiring_MissingAndCurrent guards the same multi-record
// rule for the MissingArtefact category.
func TestRunGaps_MultiWiring_MissingAndCurrent(t *testing.T) {
	root := t.TempDir()

	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-mw0002-multi-miss.md"), `---
test_case_id: tc-mw0002
title: Multi-framework wiring with missing artefact
requirement: REQ-M
---
`)
	// Current bats wiring.
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-mw0002",
		Framework: "bats",
	})
	// Playwright wiring with no artefact file on disk.
	seedLegacyRecord(t, root, legacyRecord{
		TC:               "tc-mw0002",
		Framework:        "playwright",
		Artefact:         "test/specs/tc-mw0002.spec.ts",
		SkipArtefactFile: true,
	})

	var buf bytes.Buffer
	scope := buildScopeFromArg(root, "", true)
	err := runGaps(&buf, root, scope, true, "", false)
	require.NoError(t, err)
	var report reader.GapReport
	require.NoError(t, json.Unmarshal(buf.Bytes(), &report))
	require.Len(t, report.MissingArtefact, 1,
		"missing-artefact playwright wiring must still surface even when bats is current")
	assert.Equal(t, "tc-mw0002", report.MissingArtefact[0].ID)
	// ClassifyWiring suppresses StaleArtefactHash on missing artefact —
	// make sure we don't double-count.
	assert.Empty(t, report.StaleArtefactHash,
		"missing artefact must not be double-counted as stale artefact-hash")
}

// --- CON-023 / ENH-146 wiring-aware gap categories ---

// TestRunGaps_WiringCategories_Human asserts the three wiring-derived gap
// categories print under their ENH-146 vocabulary labels and that the
// retired human-output labels do not appear.
func TestRunGaps_WiringCategories_Human(t *testing.T) {
	root := t.TempDir()

	// TC #1: stale testcase-hash (wiring hash differs from current spec content).
	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-st0001-stale-tc.md"), `---
test_case_id: tc-st0001
title: Stale testcase
requirement: REQ-S
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:           "tc-st0001",
		Framework:    "bats",
		TestCaseHash: "deadbeefcafef00d", // intentionally wrong → stale testcase-hash
	})

	// TC #2: stale artefact-hash (artefact-hash wrong; spec hash matches).
	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-st0002-stale-art.md"), `---
test_case_id: tc-st0002
title: Stale artefact
requirement: REQ-S
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:           "tc-st0002",
		Framework:    "bats",
		ArtefactHash: "deadbeefcafef00d", // intentionally wrong → stale artefact-hash
	})

	// TC #3: missing artefact (wiring points to a path that doesn't exist).
	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-mi0001-missing.md"), `---
test_case_id: tc-mi0001
title: Missing artefact
requirement: REQ-M
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:               "tc-mi0001",
		Framework:        "bats",
		SkipArtefactFile: true, // do not create the file on disk → MissingArtefact
	})

	var buf bytes.Buffer
	scope := buildScopeFromArg(root, "", true)
	err := runGaps(&buf, root, scope, false, "", false)
	require.NoError(t, err)

	out := buf.String()

	// New wiring categories appear as headers with counts.
	assert.Contains(t, out, "Stale wiring (testcase): 1")
	assert.Contains(t, out, "Stale wiring (artefact): 1")
	assert.Contains(t, out, "Missing artefacts: 1")

	// And the specific TCs are listed.
	assert.Contains(t, out, "tc-st0001")
	assert.Contains(t, out, "tc-st0002")
	assert.Contains(t, out, "tc-mi0001")

	// Retired human-output labels must not appear.
	assert.NotContains(t, out, "Automated but never executed",
		"retired category must not appear in human output")
	assert.NotContains(t, out, "Spec coverage but no automation record",
		"retired category must not appear in human output")
	assert.NotContains(t, out, "Stale execution results",
		"retired category must not appear in human output")
}

// TestRunGaps_WiringCategories_JSON asserts the wiring-aware JSON keys are
// emitted and populated.
func TestRunGaps_WiringCategories_JSON(t *testing.T) {
	root := t.TempDir()

	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-mi0002-missing.md"), `---
test_case_id: tc-mi0002
title: Missing artefact JSON
requirement: REQ-M
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:               "tc-mi0002",
		Framework:        "bats",
		SkipArtefactFile: true,
	})

	var buf bytes.Buffer
	scope := buildScopeFromArg(root, "", true)
	err := runGaps(&buf, root, scope, true, "", false)
	require.NoError(t, err)

	out := buf.String()
	// All three wiring-aware keys present in the JSON shape.
	assert.Contains(t, out, `"stale_testcase_hash":`)
	assert.Contains(t, out, `"stale_artefact_hash":`)
	assert.Contains(t, out, `"missing_artefact":`)
	// Retired JSON keys must not appear.
	assert.NotContains(t, out, `"never_executed"`,
		"retired JSON key must not appear")
	assert.NotContains(t, out, `"spec_but_no_record"`,
		"retired JSON key must not appear")
	assert.NotContains(t, out, `"stale_execution"`,
		"retired JSON key must not appear")

	var report reader.GapReport
	require.NoError(t, json.Unmarshal(buf.Bytes(), &report))
	require.Len(t, report.MissingArtefact, 1, "missing-artefact wiring must populate MissingArtefact")
	assert.Equal(t, "tc-mi0002", report.MissingArtefact[0].ID)
}

// TestRunGaps_NotRunHereIsNotAGap: a wired TC with no terminal result file
// must not surface as a gap. ENH-146 §"Decisions Inherited": "Not run here"
// is the expected state on a fresh clone, not a gap.
func TestRunGaps_NotRunHereIsNotAGap(t *testing.T) {
	root := t.TempDir()

	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-nr0001-not-run.md"), `---
test_case_id: tc-nr0001
title: Wired but never executed
requirement: REQ-N
---
`)
	// Wiring with current hashes and an existing artefact, but no terminal
	// result contract.
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-nr0001",
		Framework: "bats",
	})

	// Sanity: no .gtms/results/ exists.
	_, statErr := os.Stat(filepath.Join(root, ".gtms", "results"))
	require.True(t, os.IsNotExist(statErr), "fixture must not have terminal results")

	var buf bytes.Buffer
	scope := buildScopeFromArg(root, "", true)
	err := runGaps(&buf, root, scope, false, "", false)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "No coverage gaps found.",
		"wired-but-not-run-here TC must not surface as any gap")
}

// --- CON-023 / ENH-146 review-fix-pass-3 coverage ---

// TestGapsFolderSummary_AllRecords_SiblingFailing covers the same
// picker-only bug at the folder-summary surface: a passing primary
// framework must not hide a failing sibling framework on the same TC.
func TestGapsFolderSummary_AllRecords_SiblingFailing(t *testing.T) {
	root := t.TempDir()

	writeTestFile(t, root, filepath.Join("gtms/cases", "feature", "tc-fs0001-multi.md"), `---
test_case_id: tc-fs0001
title: Multi-framework folder summary
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-fs0001",
		Framework: "bats",
		Result:    "pass",
		Attempts:  1,
	})
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-fs0001",
		Framework: "playwright",
		Artefact:  "test/specs/tc-fs0001.spec.ts",
		Result:    "fail",
		Attempts:  1,
	})

	entries, err := reader.GapsFolderSummary(root, "")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, 1, entries[0].Failing,
		"folder summary FAILING must surface the playwright fail even when bats passes")
}

// TestRunGaps_ManualFail_NotInCurrentlyFailing covers Finding #2: manual
// rows are excluded from result-based wiring-unit categories. A manual-only
// TC with result: fail surfaces via NoAutomation.ManualCoverage="recorded",
// not via CurrentlyFailing.
func TestRunGaps_ManualFail_NotInCurrentlyFailing(t *testing.T) {
	root := t.TempDir()

	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-mf0001-manual-fail.md"), `---
test_case_id: tc-mf0001
title: Manual fail
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-mf0001",
		Framework: "manual",
		Result:    "fail",
	})

	var buf bytes.Buffer
	scope := buildScopeFromArg(root, "", true)
	err := runGaps(&buf, root, scope, true, "", false)
	require.NoError(t, err)
	var report reader.GapReport
	require.NoError(t, json.Unmarshal(buf.Bytes(), &report))

	assert.Empty(t, report.CurrentlyFailing,
		"manual-only fail must not surface in CurrentlyFailing (wiring-unit category)")
	require.Len(t, report.NoAutomation, 1,
		"manual-only TC must still surface in NoAutomation")
	assert.Equal(t, "recorded", report.NoAutomation[0].ManualCoverage,
		"manual fail is captured as ManualCoverage:recorded")
}

// TestRunGaps_ManualSkip_NotInRuntimeSkipped: same exclusion for skip.
func TestRunGaps_ManualSkip_NotInRuntimeSkipped(t *testing.T) {
	root := t.TempDir()

	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-ms0001-manual-skip.md"), `---
test_case_id: tc-ms0001
title: Manual skip
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-ms0001",
		Framework: "manual",
		Result:    "skipped",
	})

	var buf bytes.Buffer
	scope := buildScopeFromArg(root, "", true)
	err := runGaps(&buf, root, scope, true, "", false)
	require.NoError(t, err)
	var report reader.GapReport
	require.NoError(t, json.Unmarshal(buf.Bytes(), &report))

	assert.Empty(t, report.RuntimeSkipped,
		"manual-only skip must not surface in RuntimeSkipped (wiring-unit category)")
}

// TestRunGaps_BatsFailPlusManualPass_StillFails: a TC with both bats wiring
// (fail) and a manual record (pass) must still surface in CurrentlyFailing
// via the bats row. Verifies manual exclusion didn't accidentally suppress
// real wiring failures on dual-coverage TCs.
func TestRunGaps_BatsFailPlusManualPass_StillFails(t *testing.T) {
	root := t.TempDir()

	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-bm0001-bats-fail-manual-pass.md"), `---
test_case_id: tc-bm0001
title: Bats fail + manual pass
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-bm0001",
		Framework: "bats",
		Result:    "fail",
		Attempts:  1,
	})
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-bm0001",
		Framework: "manual",
		Result:    "pass",
	})

	var buf bytes.Buffer
	scope := buildScopeFromArg(root, "", true)
	err := runGaps(&buf, root, scope, true, "", false)
	require.NoError(t, err)
	var report reader.GapReport
	require.NoError(t, json.Unmarshal(buf.Bytes(), &report))

	require.Len(t, report.CurrentlyFailing, 1,
		"bats fail must surface even when a manual pass coexists")
	assert.Equal(t, "tc-bm0001", report.CurrentlyFailing[0].ID)
}

// TestRunGaps_ManualDrift_SelectedRecordRule validates that drift counting
// follows the BUG-079 / BUG-086 selected-record rule: drift surfaces only
// when the selected automation record is manual. A sibling manual record's
// drift does NOT leak onto a bats-selected TC.
func TestRunGaps_ManualDrift_SelectedRecordRule(t *testing.T) {
	root := t.TempDir()

	tcID := "tc-md0001"
	writeTestFile(t, root, filepath.Join("gtms/cases", tcID+"-manual-drift.md"), `---
test_case_id: tc-md0001
title: Manual drift under framework filter
---
`)
	// Bats wiring with a passing result — primary automation surface.
	seedLegacyRecord(t, root, legacyRecord{
		TC:        tcID,
		Framework: "bats",
		Result:    "pass",
		Attempts:  1,
	})
	// A manual result file that flags drift-detected against the spec hash.
	manualDir := filepath.Join(root, "gtms", "manual", "records")
	require.NoError(t, os.MkdirAll(manualDir, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(manualDir, tcID+"--manual.result.yaml"),
		[]byte(`test_case_id: tc-md0001
test_case_hash: 0011223344556677
framework: manual
result: pass
drift-detected: true
drift-detected-at: "2026-05-15T07:43:25Z"
test_case_hash_at_execute: "deadbeefcafef00d"
`), 0644))

	// BUG-086: Non-strict, no framework — bats wins default selection
	// (non-manual preferred on equal cycle). Drift does NOT surface because
	// the selected record is bats, not manual.
	var buf bytes.Buffer
	scope := buildScopeFromArg(root, "", true)
	err := runGaps(&buf, root, scope, true, "", false)
	require.NoError(t, err)
	var report reader.GapReport
	require.NoError(t, json.Unmarshal(buf.Bytes(), &report))
	require.Len(t, report.DriftDetected, 0,
		"BUG-086: drift must NOT surface when bats is the selected record")

	// Strict --framework bats: bats is selected, drift does NOT surface.
	buf.Reset()
	err = runGaps(&buf, root, scope, true, "bats", true)
	require.NoError(t, err)
	report = reader.GapReport{}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &report))
	require.Len(t, report.DriftDetected, 0,
		"BUG-086: drift must NOT surface under --framework bats")

	// Strict --framework manual: manual is selected, drift DOES surface.
	buf.Reset()
	err = runGaps(&buf, root, scope, true, "manual", true)
	require.NoError(t, err)
	report = reader.GapReport{}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &report))
	require.Len(t, report.DriftDetected, 1,
		"BUG-086: drift must surface when manual is explicitly selected")
	assert.Equal(t, tcID, report.DriftDetected[0].ID)
}

// --- BUG-081: TC-ID rejection on `gtms gaps` with existence-first guard ---

// withGapsGlobals overrides the package-level projectRoot and appConfig used by
// the gaps RunE closure, then returns a restore function. Tests use this to
// invoke the cobra RunE directly without needing PersistentPreRunE to fire.
func withGapsGlobals(t *testing.T, root string, cfg *config.Config) func() {
	t.Helper()
	savedRoot := projectRoot
	savedCfg := appConfig
	projectRoot = root
	appConfig = cfg
	return func() {
		projectRoot = savedRoot
		appConfig = savedCfg
	}
}

func TestBUG081_GapsRejectsTcIdWhenFolderAbsent(t *testing.T) {
	root := t.TempDir()
	// Create a TC file (flat under gtms/cases/) so the TC exists as a test
	// case but NOT as a folder name. The existence-first guard must reject.
	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-aaa1111-login.md"), `---
test_case_id: tc-aaa1111
title: Login
requirement: REQ-A
---
`)

	restore := withGapsGlobals(t, root, &config.Config{})
	defer restore()

	cmd := newGapsCmd()
	err := cmd.RunE(cmd, []string{"tc-aaa1111"})
	require.Error(t, err)
	assert.True(t, output.IsDisplayed(err), "rejection error should be marked as displayed")
	assert.Contains(t, err.Error(), "argument must be a folder, not a TC ID")
}

func TestBUG081_GapsRejectsTcIdJSONFlagAlsoRejects(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-aaa1111-login.md"), `---
test_case_id: tc-aaa1111
title: Login
requirement: REQ-A
---
`)

	restore := withGapsGlobals(t, root, &config.Config{})
	defer restore()

	cmd := newGapsCmd()
	require.NoError(t, cmd.Flags().Set("json", "true"))
	err := cmd.RunE(cmd, []string{"tc-aaa1111"})
	require.Error(t, err)
	assert.True(t, output.IsDisplayed(err))
	assert.Contains(t, err.Error(), "argument must be a folder, not a TC ID")
}

func TestBUG081_GapsTcShapedFolderStillScopes(t *testing.T) {
	// Existence-first regression: a real folder named tc-regression must
	// still be accepted as a scope, even though isTestCaseID matches the
	// "tc-" prefix. This guards against a blanket TC-shape rejection.
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-regression", "tc-xyz12345-edge.md"), `---
test_case_id: tc-xyz12345
title: Edge case
requirement: REQ-A
---
`)

	restore := withGapsGlobals(t, root, &config.Config{})
	defer restore()

	cmd := newGapsCmd()
	err := cmd.RunE(cmd, []string{"tc-regression"})
	require.NoError(t, err, "tc-shaped folder name that exists must fall through to the normal scope path")
}
