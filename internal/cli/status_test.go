package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/output"
	"github.com/aitestmanagement/gtms-cli/internal/reader"
)

// setupStatusFixture creates a minimal fixture project for status CLI tests.
func setupStatusFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-aaa1111-login-happy.md"), `---
test_case_id: tc-aaa1111
title: Login Happy Path
requirement: REQ-A
---
`)
	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-bbb1111-checkout-flow.md"), `---
test_case_id: tc-bbb1111
title: Checkout Flow
requirement: REQ-B
---
`)

	// tc-aaa1111 has wiring + passing handoff (CON-023 / ENH-145).
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-aaa1111",
		Framework: "playwright",
		Result:    "pass",
		Attempts:  1,
	})

	return root
}

func TestRunStatusOverview_Table(t *testing.T) {
	root := setupStatusFixture(t)
	var buf bytes.Buffer

	err := runStatusOverview(&buf, root, nil, false, "", false)
	require.NoError(t, err)

	out := buf.String()

	// Table should include slug alongside test case ID
	assert.Contains(t, out, "tc-aaa1111  login-happy")
	assert.Contains(t, out, "tc-bbb1111  checkout-flow")

	// Table header should be present
	assert.Contains(t, out, "TEST CASE")
}

func TestRunStatusOverview_JSON(t *testing.T) {
	root := setupStatusFixture(t)
	var buf bytes.Buffer

	err := runStatusOverview(&buf, root, nil, true, "", false)
	require.NoError(t, err)

	out := buf.String()

	// Should be valid JSON
	var entries []reader.PipelineEntry
	err = json.Unmarshal([]byte(out), &entries)
	require.NoError(t, err, "Output should be valid JSON")

	// Should have two entries
	assert.Len(t, entries, 2)

	// Slug should be present in JSON
	assert.Equal(t, "login-happy", entries[0].Slug)
	assert.Equal(t, "checkout-flow", entries[1].Slug)

	// No human-readable decorations (table headers, not JSON keys)
	assert.NotContains(t, out, "TEST CASE")
	assert.NotContains(t, out, "LAST RESULT")
	assert.NotContains(t, out, "No test cases found.")
}

func TestRunStatusOverview_JSON_Empty(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer

	err := runStatusOverview(&buf, root, nil, true, "", false)
	require.NoError(t, err)

	out := buf.String()

	// Must be valid JSON, NOT "No test cases found."
	assert.NotContains(t, out, "No test cases found.")

	var entries []reader.PipelineEntry
	err = json.Unmarshal([]byte(out), &entries)
	require.NoError(t, err, "Empty project JSON should still be valid JSON")

	assert.Len(t, entries, 0)

	// Must be [] not null
	assert.Contains(t, out, "[]")
}

func TestRunStatusDetail_JSON(t *testing.T) {
	root := setupStatusFixture(t)
	var buf bytes.Buffer

	err := runStatusDetail(&buf, root, "tc-aaa1111", true, "", false)
	require.NoError(t, err)

	out := buf.String()

	// Should be valid JSON
	var detail reader.PipelineDetailEntry
	err = json.Unmarshal([]byte(out), &detail)
	require.NoError(t, err, "Output should be valid JSON")

	assert.Equal(t, "tc-aaa1111", detail.TestCaseID)
	assert.Equal(t, "login-happy", detail.Slug)
	assert.Equal(t, "Login Happy Path", detail.Title)
	assert.Equal(t, "REQ-A", detail.Requirement)

	// No human-readable decorations
	assert.NotContains(t, out, "CREATE:")
	assert.NotContains(t, out, "AUTOMATE:")
	assert.NotContains(t, out, "\u2500") // horizontal line
}

func TestRunStatusDetail_ShowsSlugInHeader(t *testing.T) {
	root := setupStatusFixture(t)
	var buf bytes.Buffer

	err := runStatusDetail(&buf, root, "tc-aaa1111", false, "", false)
	require.NoError(t, err)

	out := buf.String()

	// Header should include slug alongside test case ID
	assert.Contains(t, out, "tc-aaa1111  login-happy: Login Happy Path")
}

func TestRunStatusDetail_JSON_NotFound(t *testing.T) {
	root := setupStatusFixture(t)
	var buf bytes.Buffer

	err := runStatusDetail(&buf, root, "tc-nonexistent", true, "", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestFormatTestCaseColumn(t *testing.T) {
	assert.Equal(t, "tc-001  login-happy", formatTestCaseColumn("tc-001", "login-happy"))
	assert.Equal(t, "tc-001", formatTestCaseColumn("tc-001", ""))
}

// --- BUG-017 regression tests ---

func TestFormatStageStatus_Error(t *testing.T) {
	result := formatStageStatus("error")
	assert.Equal(t, "\u2717", result, "Error status should display X mark icon")
}

func TestFormatStageStatus_FailedIsUnknown(t *testing.T) {
	result := formatStageStatus("failed")
	assert.Equal(t, "\u2014", result, "Retired 'failed' status should display em dash (no longer valid)")
}

func TestRunStatusOverview_StatusKey(t *testing.T) {
	root := setupStatusFixture(t)
	var buf bytes.Buffer

	err := runStatusOverview(&buf, root, nil, false, "", false)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "Key:")
	// BUG-078: updated legend wording per ENH-130 vocabulary.
	assert.Contains(t, out, "= complete")
	assert.Contains(t, out, "= error")
	assert.Contains(t, out, "= stale")
	assert.Contains(t, out, "= skipped")
	assert.Contains(t, out, "not yet attempted")
	// Stale wordings must be gone.
	assert.NotContains(t, out, "complete/pass")
	assert.NotContains(t, out, "= failed")
	assert.NotContains(t, out, "error/stale")
}

func TestRunStatusOverview_NoKeyInJSON(t *testing.T) {
	root := setupStatusFixture(t)
	var buf bytes.Buffer

	err := runStatusOverview(&buf, root, nil, true, "", false)
	require.NoError(t, err)

	out := buf.String()
	assert.NotContains(t, out, "Key:")
}

func TestBUG017_FailedExecuteShowsXInTable(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-aaa1111-checkout.md"), `---
test_case_id: tc-aaa1111
title: Checkout Test
requirement: REQ-A
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-aaa1111",
		Framework: "bats",
		Adapter:   "bats-runner",
		Artefact:  "gtms/automation/specs/tc-aaa1111.bats",
		Attempts:  1,
	})
	writeTestFile(t, root, filepath.Join("gtms/tasks", "error", "task-abc1234-execute-tc-aaa1111.md"), `---
id: task-abc1234
type: execute
target: tc-aaa1111
adapter: bats-runner
status: error
created: 2026-03-07T10:00:00Z
branch: main
---
`)

	var buf bytes.Buffer
	err := runStatusOverview(&buf, root, nil, false, "", false)
	require.NoError(t, err)

	out := buf.String()
	// The EXECUTE column should show the X mark (failed icon), not em dash
	assert.Contains(t, out, "\u2717", "Failed execute should show X mark icon")
	assert.Contains(t, out, "tc-aaa1111")
}

func TestBUG017_FailedExecuteInJSON(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-aaa1111-checkout.md"), `---
test_case_id: tc-aaa1111
title: Checkout Test
requirement: REQ-A
---
`)
	// Wiring exists but no terminal handoff — only an error task.
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-aaa1111",
		Framework: "bats",
		Adapter:   "bats-runner",
		Artefact:  "gtms/automation/specs/tc-aaa1111.bats",
		Attempts:  1,
	})
	writeTestFile(t, root, filepath.Join("gtms/tasks", "error", "task-abc1234-execute-tc-aaa1111.md"), `---
id: task-abc1234
type: execute
target: tc-aaa1111
adapter: bats-runner
status: error
created: 2026-03-07T10:00:00Z
branch: main
---
`)

	var buf bytes.Buffer
	err := runStatusOverview(&buf, root, nil, true, "", false)
	require.NoError(t, err)

	// CON-023 / ENH-146 retired the legacy AutomateStatus/ExecuteStatus JSON
	// fields (now json:"-"). The new shape exposes wired + frameworks[]; an
	// error task without a terminal handoff appears as a wired TC whose
	// framework entry carries no overlay result. The task-state-only "error"
	// signal lives on the internal carrier consumed by the table renderer
	// (see TestBUG017_FailedExecuteShowsXInTable) and is not part of the
	// per-TC JSON contract.
	var entries []reader.PipelineEntry
	err = json.Unmarshal(buf.Bytes(), &entries)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	assert.True(t, entries[0].Wired, "wiring should still be visible in JSON")
	require.Len(t, entries[0].Frameworks, 1)
	assert.Empty(t, entries[0].Frameworks[0].LastStatusHere,
		"no terminal handoff → overlay last_status_here stays empty")
}

func TestBUG017_FailedExecuteDetailView(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-aaa1111-checkout.md"), `---
test_case_id: tc-aaa1111
title: Checkout Test
requirement: REQ-A
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-aaa1111",
		Framework: "bats",
		Adapter:   "bats-runner",
		Artefact:  "gtms/automation/specs/tc-aaa1111.bats",
		Attempts:  1,
	})
	writeTestFile(t, root, filepath.Join("gtms/tasks", "error", "task-abc1234-execute-tc-aaa1111.md"), `---
id: task-abc1234
type: execute
target: tc-aaa1111
adapter: bats-runner
status: error
created: 2026-03-07T10:00:00Z
branch: main
---
`)

	var buf bytes.Buffer
	err := runStatusDetail(&buf, root, "tc-aaa1111", false, "", false)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "\u2717", "Detail view should show X mark for error execute")
	assert.Contains(t, out, "Error", "Detail view should show 'Error' label")
}

// --- Scope feedback tests (ENH-036) ---

func TestRunStatusOverview_ScopeFeedback(t *testing.T) {
	root := setupStatusFixture(t)
	var buf bytes.Buffer

	scope := &reader.ScopeInfo{
		ScanDir:   filepath.Join(root, "gtms/cases"),
		RelPath:   "gtms/cases/login/",
		Recursive: false,
	}
	err := runStatusOverview(&buf, root, scope, false, "", false)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "Scope: gtms/cases/login/")
	assert.Contains(t, out, "use -r for recursive")
}

func TestRunStatusOverview_ScopeFeedbackRecursive(t *testing.T) {
	root := setupStatusFixture(t)
	var buf bytes.Buffer

	scope := &reader.ScopeInfo{
		ScanDir:   filepath.Join(root, "gtms/cases"),
		RelPath:   "gtms/cases/login/",
		Recursive: true,
	}
	err := runStatusOverview(&buf, root, scope, false, "", false)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "Scope: gtms/cases/login/")
	assert.NotContains(t, out, "use -r for recursive")
}

func TestRunStatusOverview_ScopeFeedbackBeforeEmpty(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer

	scope := &reader.ScopeInfo{
		ScanDir:   filepath.Join(root, "gtms/cases", "empty"),
		RelPath:   "gtms/cases/empty/",
		Recursive: false,
	}
	err := runStatusOverview(&buf, root, scope, false, "", false)
	require.NoError(t, err)

	out := buf.String()
	// Scope feedback should appear even when no test cases are found
	assert.Contains(t, out, "Scope: gtms/cases/empty/")
	assert.Contains(t, out, "No test cases found.")
}

func TestRunStatusOverview_NilScopeNoFeedback(t *testing.T) {
	root := setupStatusFixture(t)
	var buf bytes.Buffer

	err := runStatusOverview(&buf, root, nil, false, "", false)
	require.NoError(t, err)

	out := buf.String()
	assert.NotContains(t, out, "Scope:")
}

// --- Folder summary tests (ENH-066) ---

func setupFolderSummaryFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	// Folder "login" — 2 TCs, 1 automated with pass
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

func TestRunStatusFolderSummary_Table(t *testing.T) {
	root := setupFolderSummaryFixture(t)
	var buf bytes.Buffer

	err := runStatusFolderSummary(&buf, root, false, "")
	require.NoError(t, err)

	out := buf.String()
	// ENH-089: column headers renamed to imperative-stage form, with new TC column.
	assert.Contains(t, out, "FOLDER")
	assert.Contains(t, out, "TC")
	assert.Contains(t, out, "CREATE")
	assert.Contains(t, out, "AUTOMATE")
	assert.Contains(t, out, "EXECUTE")
	// Old column names must be gone.
	assert.NotContains(t, out, "CREATED")
	assert.NotContains(t, out, "AUTOMATED")
	assert.NotContains(t, out, "EXECUTED")
	assert.Contains(t, out, "login")
	assert.Contains(t, out, "checkout")
	// login: 1/2 passing — substring match still holds (icon prefix doesn't break it).
	assert.Contains(t, out, "1/2")
}

func TestRunStatusFolderSummary_DraftAnnotation(t *testing.T) {
	root := t.TempDir()

	writeTestFile(t, root, filepath.Join("gtms/cases", "feature", "tc-aaa1111-draft.md"), `---
test_case_id: tc-aaa1111
title: Draft Test
status: draft
---
`)
	writeTestFile(t, root, filepath.Join("gtms/cases", "feature", "tc-aaa2222-ready.md"), `---
test_case_id: tc-aaa2222
title: Ready Test
---
`)

	var buf bytes.Buffer
	err := runStatusFolderSummary(&buf, root, false, "")
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "2 (1 draft)")
}

func TestRunStatusFolderSummary_DraftAnnotationPlural(t *testing.T) {
	root := t.TempDir()

	writeTestFile(t, root, filepath.Join("gtms/cases", "feature", "tc-aaa1111-draft1.md"), `---
test_case_id: tc-aaa1111
title: Draft Test 1
status: draft
---
`)
	writeTestFile(t, root, filepath.Join("gtms/cases", "feature", "tc-aaa2222-draft2.md"), `---
test_case_id: tc-aaa2222
title: Draft Test 2
status: draft
---
`)
	writeTestFile(t, root, filepath.Join("gtms/cases", "feature", "tc-aaa3333-ready.md"), `---
test_case_id: tc-aaa3333
title: Ready Test
---
`)

	var buf bytes.Buffer
	err := runStatusFolderSummary(&buf, root, false, "")
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "3 (2 drafts)")
}

func TestRunStatusFolderSummary_JSON(t *testing.T) {
	root := setupFolderSummaryFixture(t)
	var buf bytes.Buffer

	err := runStatusFolderSummary(&buf, root, true, "")
	require.NoError(t, err)

	out := buf.String()
	var entries []reader.FolderSummaryEntry
	err = json.Unmarshal([]byte(out), &entries)
	require.NoError(t, err, "Output should be valid JSON")
	assert.Len(t, entries, 2)
}

func TestRunStatusFolderSummary_JSON_Empty(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer

	err := runStatusFolderSummary(&buf, root, true, "")
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "[]")
	assert.NotContains(t, out, "No test cases found.")
}

func TestRunStatusFolderSummary_Empty(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer

	err := runStatusFolderSummary(&buf, root, false, "")
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "No test cases found.")
	assert.Contains(t, out, "gtms create")
}

func TestRunStatusFolderSummary_NoScopeLine(t *testing.T) {
	root := setupFolderSummaryFixture(t)
	var buf bytes.Buffer

	err := runStatusFolderSummary(&buf, root, false, "")
	require.NoError(t, err)

	out := buf.String()
	assert.NotContains(t, out, "Scope:")
}

// --- Recursive flat list tests (gtms status -r) ---

func TestRunStatusOverview_RecursiveFlatList(t *testing.T) {
	// Test that passing a recursive scope to runStatusOverview shows individual TCs
	// from subdirectories (the "gtms status -r" path).
	root := setupFolderSummaryFixture(t)
	var buf bytes.Buffer

	scope := buildScopeFromArg(root, "", true)
	err := runStatusOverview(&buf, root, scope, false, "", false)
	require.NoError(t, err)

	out := buf.String()
	// Should show individual TCs from both folders
	assert.Contains(t, out, "tc-aaa1111")
	assert.Contains(t, out, "tc-aaa2222")
	assert.Contains(t, out, "tc-bbb1111")
	// Should show the individual TC table, not folder summary
	assert.Contains(t, out, "TEST CASE")
	assert.NotContains(t, out, "FOLDER")
}

// --- Manual framework display tests (ENH-068) ---

func TestFormatStageStatus_Manual(t *testing.T) {
	result := formatStageStatus("manual")
	assert.Equal(t, "manual", result, "Manual status should display the text 'manual'")
}

func TestFormatDetailLabel_Manual(t *testing.T) {
	result := formatDetailLabel("manual", "")
	assert.Equal(t, "Manual testing", result)
}

func TestRunStatusOverview_ManualInAutomateColumn(t *testing.T) {
	root := t.TempDir()

	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-aaa1111-manual.md"), `---
test_case_id: tc-aaa1111
title: Manual Test
---
`)
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-aaa1111", Framework: "manual", Result: "pass"})

	var buf bytes.Buffer
	err := runStatusOverview(&buf, root, nil, false, "", false)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "manual", "AUTOMATE column should show 'manual' for manual records")
	assert.Contains(t, out, "pass", "LAST RESULT should show 'pass'")
}

func TestRunStatusOverview_ManualInJSON(t *testing.T) {
	root := t.TempDir()

	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-aaa1111-manual.md"), `---
test_case_id: tc-aaa1111
title: Manual Test
---
`)
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-aaa1111", Framework: "manual", Result: "pass"})

	var buf bytes.Buffer
	err := runStatusOverview(&buf, root, nil, true, "", false)
	require.NoError(t, err)

	var entries []reader.PipelineEntry
	err = json.Unmarshal(buf.Bytes(), &entries)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	// CON-023 / ENH-146: manual-only TCs have no wiring, so the new JSON
	// shape exposes manual-readiness via the per-TC manual_ready flag.
	// AutomateStatus / ExecuteStatus / LastResult were retired from JSON
	// (json:"-") — they remain on the internal Go-side carrier consumed
	// by the table renderer.
	assert.False(t, entries[0].Wired, "manual-only TC: wired:false")
	assert.True(t, entries[0].ManualReady, "manual-only TC: manual_ready:true")
	assert.Empty(t, entries[0].SelectedFramework, "no wiring → selected_framework empty")
}

func TestRunStatusFolderSummary_ManualExcludedFromAutomated(t *testing.T) {
	root := t.TempDir()

	// 2 TCs: one manual, one automated
	writeTestFile(t, root, filepath.Join("gtms/cases", "feature", "tc-aaa1111-manual.md"), `---
test_case_id: tc-aaa1111
title: Manual Test
---
`)
	writeTestFile(t, root, filepath.Join("gtms/cases", "feature", "tc-bbb2222-auto.md"), `---
test_case_id: tc-bbb2222
title: Automated Test
---
`)
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-aaa1111", Framework: "manual", Result: "pass"})
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-bbb2222", Framework: "bats", Result: "pass"})

	var buf bytes.Buffer
	err := runStatusFolderSummary(&buf, root, false, "")
	require.NoError(t, err)

	out := buf.String()
	// ENH-089: AUTOMATE column shows ○ 1/2 (only bats counts).
	// EXECUTE column shows ✓ (both pass — including the manual record).
	assert.Contains(t, out, "1/2", "Only automated TC should count in AUTOMATE column")
	assert.Contains(t, out, output.IconComplete, "EXECUTE should render ✓ when all results pass")
}

func TestRunStatusOverview_RecursiveFlatList_JSON(t *testing.T) {
	root := setupFolderSummaryFixture(t)
	var buf bytes.Buffer

	scope := buildScopeFromArg(root, "", true)
	err := runStatusOverview(&buf, root, scope, true, "", false)
	require.NoError(t, err)

	var entries []reader.PipelineEntry
	err = json.Unmarshal(buf.Bytes(), &entries)
	require.NoError(t, err, "Output should be valid JSON")
	assert.Len(t, entries, 3, "Should include all TCs from all subfolders")
}

// --- ENH-072: Framework annotation in LAST RESULT ---

func TestFormatLastResult_WithFramework(t *testing.T) {
	result := formatLastResult("pass", "", "bats")
	assert.Equal(t, "pass [bats]", result)
}

func TestFormatLastResult_NoFramework(t *testing.T) {
	result := formatLastResult("pass", "", "")
	assert.Equal(t, "pass", result)
}

func TestFormatLastResult_EmDashNoAnnotation(t *testing.T) {
	result := formatLastResult("none", "", "bats")
	assert.Equal(t, "\u2014", result, "Em dash should have no framework annotation")

	result2 := formatLastResult("", "", "bats")
	assert.Equal(t, "\u2014", result2, "Empty result should have no framework annotation")
}

func TestFormatLastResult_DateAndFramework(t *testing.T) {
	result := formatLastResult("pass", "2026-04-01", "bats")
	assert.Equal(t, "pass (2026-04-01) [bats]", result)
}

func TestRunStatusOverview_FrameworkInLastResult(t *testing.T) {
	root := setupStatusFixture(t)
	var buf bytes.Buffer

	err := runStatusOverview(&buf, root, nil, false, "", false)
	require.NoError(t, err)

	out := buf.String()
	// tc-aaa1111 has framework: playwright and result: pass.
	// CON-023 / ENH-145: the result handoff always carries a completed
	// timestamp now, so the LAST RESULT cell looks like
	// `pass (<RFC3339>) [playwright]`. Assert the substrings separately
	// so the test remains robust against the timestamp format.
	assert.Contains(t, out, "pass ", "LAST RESULT should show 'pass'")
	assert.Contains(t, out, "[playwright]", "LAST RESULT should include framework annotation")
}

func TestRunStatusOverview_JSON_FrameworkField(t *testing.T) {
	root := setupStatusFixture(t)
	var buf bytes.Buffer

	err := runStatusOverview(&buf, root, nil, true, "", false)
	require.NoError(t, err)

	var entries []reader.PipelineEntry
	err = json.Unmarshal(buf.Bytes(), &entries)
	require.NoError(t, err)

	// CON-023 / ENH-146: top-level `framework` was retired (json:"-"). The
	// new shape exposes the picker's choice via `selected_framework` plus
	// per-framework state under `frameworks[].framework`.
	assert.Equal(t, "playwright", entries[0].SelectedFramework, "JSON should expose selected_framework")
	require.Len(t, entries[0].Frameworks, 1)
	assert.Equal(t, "playwright", entries[0].Frameworks[0].Framework)

	// tc-bbb1111 has no automation
	assert.False(t, entries[1].Wired, "TC without wiring should have wired:false")
	assert.Empty(t, entries[1].SelectedFramework)
}

func TestRunStatusDetail_FrameworkOnExecuteLine(t *testing.T) {
	root := setupStatusFixture(t)
	var buf bytes.Buffer

	err := runStatusDetail(&buf, root, "tc-aaa1111", false, "", false)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "[playwright]", "Detail EXECUTE line should show framework annotation")
}

func TestRunStatusDetail_NoFrameworkWhenNoResult(t *testing.T) {
	root := setupStatusFixture(t)
	var buf bytes.Buffer

	// tc-bbb1111 has no automation record, so no result and no framework
	err := runStatusDetail(&buf, root, "tc-bbb1111", false, "", false)
	require.NoError(t, err)

	out := buf.String()
	assert.NotContains(t, out, "[", "Detail view should not show framework brackets when no result")
}

// BUG-085: the wiring overlay used to populate both LastResultDate and
// LastRunAt from the result-contract `completed:` value, producing a doubled
// timestamp on the EXECUTE detail line — once via formatExecuteLabel's
// inner-paren RFC3339 wrap, once via appendDate(formatRunAt(LastRunAt)).
// The fix drops the LastResultDate write on the wiring overlay branch so
// LastRunAt is the sole stage-time carrier and the line renders once in the
// ENH-085 "Pass, YYYY-MM-DD HH:MM UTC [bats]" shape.
func TestBUG085_StatusDetailDoesNotDoubleRenderExecuteTimestamp(t *testing.T) {
	root := t.TempDir()

	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-bug085-sample.md"), `---
test_case_id: tc-bug085
title: BUG-085 fixture
requirement: REQ-X
---
`)

	seedLegacyRecord(t, root, legacyRecord{
		TC:         "tc-bug085",
		Framework:  "bats",
		Result:     "pass",
		ExecutedAt: "2026-05-26T10:25:57Z",
		Attempts:   1,
	})

	var buf bytes.Buffer
	err := runStatusDetail(&buf, root, "tc-bug085", false, "", false)
	require.NoError(t, err)
	out := buf.String()

	// Single formatted UTC timestamp present.
	assert.Contains(t, out, "Pass, 2026-05-26 10:25 UTC",
		"EXECUTE line must carry the single formatted UTC stamp")

	// No inner-paren RFC3339 wrap (the BUG-085 leak shape).
	assert.NotContains(t, out, "Pass (2026-05-26T10:25:57Z)",
		"inner-paren RFC3339 wrap must not appear after the fix")
	assert.NotContains(t, out, "T10:25:57Z",
		"raw RFC3339 must not leak onto rendered EXECUTE line")

	// Order on the EXECUTE line: Pass → UTC stamp → [framework].
	var executeLine string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "EXECUTE:") {
			executeLine = line
			break
		}
	}
	require.NotEmpty(t, executeLine, "EXECUTE line must be rendered")
	assert.Regexp(t, `Pass,.*UTC.*\[bats\]`, executeLine,
		"EXECUTE line order: Pass → UTC → [framework]")
	assert.NotRegexp(t, `\[bats\].*UTC`, executeLine,
		"framework bracket must not appear before the UTC stamp")
}

// --- ENH-089: icon-forward folder summary cell helpers + renderer ---

func TestFormatFolderExecuteCell_AllPass(t *testing.T) {
	e := reader.FolderSummaryEntry{Created: 5, Passing: 5}
	assert.Equal(t, output.IconComplete, formatFolderExecuteCell(e),
		"All passing → ✓ only, no fraction")
}

func TestFormatFolderExecuteCell_OneFail(t *testing.T) {
	e := reader.FolderSummaryEntry{Created: 5, Passing: 4, Failing: 1}
	assert.Equal(t, output.IconError+" 4/5", formatFolderExecuteCell(e),
		"Any fail → ✗ N/M (passing/total)")
}

func TestFormatFolderExecuteCell_OneInFlight(t *testing.T) {
	e := reader.FolderSummaryEntry{Created: 5, Passing: 4, InFlight: 1}
	assert.Equal(t, output.IconInProgress+" 4/5", formatFolderExecuteCell(e),
		"In-flight (no fails) → ● N/M")
}

func TestFormatFolderExecuteCell_FailBeatsInFlight(t *testing.T) {
	// Worst-news-wins: a fail outranks an in-flight task on a different TC.
	e := reader.FolderSummaryEntry{Created: 5, Passing: 3, Failing: 1, InFlight: 1}
	assert.Equal(t, output.IconError+" 3/5", formatFolderExecuteCell(e),
		"Fail must outrank in-flight (priority ✗ > ●)")
}

func TestFormatFolderExecuteCell_ErrorBeatsInFlight(t *testing.T) {
	// Errored collapses to ✗ alongside Failing.
	e := reader.FolderSummaryEntry{Created: 5, Passing: 3, Errored: 1, InFlight: 1}
	assert.Equal(t, output.IconError+" 3/5", formatFolderExecuteCell(e),
		"Errored must collapse to ✗ and outrank in-flight")
}

func TestFormatFolderExecuteCell_PartialPending(t *testing.T) {
	e := reader.FolderSummaryEntry{Created: 5, Passing: 2}
	assert.Equal(t, output.IconPending+" 2/5", formatFolderExecuteCell(e),
		"No fail / no in-flight / partial coverage → ○ N/M")
}

func TestFormatFolderExecuteCell_EmptyFolder(t *testing.T) {
	e := reader.FolderSummaryEntry{Created: 0}
	assert.Equal(t, output.IconComplete, formatFolderExecuteCell(e),
		"Empty folder defensively renders ✓ (no division by zero)")
}

func TestFormatFolderAutomateCell_AllAutomated(t *testing.T) {
	e := reader.FolderSummaryEntry{Created: 5, Automated: 5}
	assert.Equal(t, output.IconComplete, formatFolderAutomateCell(e),
		"All automated → ✓ only")
}

func TestFormatFolderAutomateCell_Partial(t *testing.T) {
	e := reader.FolderSummaryEntry{Created: 5, Automated: 3}
	assert.Equal(t, output.IconPending+" 3/5", formatFolderAutomateCell(e),
		"Partial automation → ○ N/M (automated/total)")
}

func TestFormatFolderAutomateCell_NoneAutomated(t *testing.T) {
	e := reader.FolderSummaryEntry{Created: 5, Automated: 0}
	assert.Equal(t, output.IconPending+" 0/5", formatFolderAutomateCell(e),
		"Zero automation → ○ 0/N")
}

// --- BUG-042: SKIP column surfaces runtime-skipped count as a digit ---

func TestFormatFolderSkipCell_NoSkips(t *testing.T) {
	e := reader.FolderSummaryEntry{Created: 5, Passing: 5}
	assert.Equal(t, "\u2014", formatFolderSkipCell(e),
		"No skips → em-dash (column is not applicable)")
}

func TestFormatFolderSkipCell_WithSkips(t *testing.T) {
	// BUG-042 canonical case: 2 skipped + 1 pass in a folder of 3. The literal
	// count "2" must appear paired with the ⊘ glyph.
	e := reader.FolderSummaryEntry{Created: 3, Passing: 1, Skipped: 2}
	assert.Equal(t, output.IconSkipped+" 2", formatFolderSkipCell(e),
		"Skipped=2 → '⊘ 2' (glyph + digit) so users don't have to subtract from total")
}

func TestRunStatusFolderSummary_SkipColumnHeaderPresent(t *testing.T) {
	// BUG-042 AC: the text column header reflects the new cell. The
	// tc-a024501b BATS fixture checks `grep -qi 'skip'` against the full
	// output, so any case of the word "skip" in the header or key satisfies it.
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/cases", "feature", "tc-aaa1111-pass.md"), `---
test_case_id: tc-aaa1111
title: Pass
---
`)

	var buf bytes.Buffer
	err := runStatusFolderSummary(&buf, root, false, "")
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "SKIP", "Folder summary must expose a SKIP column header (BUG-042)")
}

func TestRunStatusFolderSummary_SkipCountRenderedAsDigit(t *testing.T) {
	// BUG-042 canonical fixture: tc-a024501b-folder-summary-skipped-column.
	// 2 skipped + 1 pass in foldera — the literal digit "2" must appear in
	// the foldera row.
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/cases", "foldera", "tc-10101010-skipped.md"), `---
test_case_id: tc-10101010
title: Skipped one
---
`)
	writeTestFile(t, root, filepath.Join("gtms/cases", "foldera", "tc-20202020-skipped.md"), `---
test_case_id: tc-20202020
title: Skipped two
---
`)
	writeTestFile(t, root, filepath.Join("gtms/cases", "foldera", "tc-30303030-pass.md"), `---
test_case_id: tc-30303030
title: Passing
---
`)
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-10101010", Framework: "bats", Result: "skipped"})
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-20202020", Framework: "bats", Result: "skipped"})
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-30303030", Framework: "bats", Result: "pass"})

	var buf bytes.Buffer
	err := runStatusFolderSummary(&buf, root, false, "")
	require.NoError(t, err)

	out := buf.String()

	// Isolate the foldera row — it's the only data row.
	var folderaRow string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "foldera") {
			folderaRow = line
			break
		}
	}
	require.NotEmpty(t, folderaRow, "foldera row must be present")

	// Must contain the skip count digit.
	assert.Contains(t, folderaRow, "2",
		"foldera row must carry the literal skipped count '2' (BUG-042)")
	// And must not collapse into the all-pass rendering the BATS fixture refutes.
	assert.NotContains(t, folderaRow, "3/3",
		"skip count must not be folded into the pass denominator")
	assert.NotContains(t, folderaRow, output.IconComplete+" 3",
		"skip count must not be rendered as '✓ 3'")
}

func TestRunStatusFolderSummary_HeadersRenamed(t *testing.T) {
	root := setupFolderSummaryFixture(t)
	var buf bytes.Buffer

	err := runStatusFolderSummary(&buf, root, false, "")
	require.NoError(t, err)

	out := buf.String()
	// New imperative-stage headers (match detail view vocabulary).
	assert.Contains(t, out, "TC")
	assert.Contains(t, out, "CREATE")
	assert.Contains(t, out, "AUTOMATE")
	assert.Contains(t, out, "EXECUTE")
	// Old past-tense headers must be gone — no substring leak.
	assert.NotContains(t, out, "CREATED")
	assert.NotContains(t, out, "AUTOMATED")
	assert.NotContains(t, out, "EXECUTED")
}

func TestRunStatusFolderSummary_KeyFooter(t *testing.T) {
	root := setupFolderSummaryFixture(t)
	var buf bytes.Buffer

	err := runStatusFolderSummary(&buf, root, false, "")
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "Key:", "Folder summary should include a key footer for the icons")
	assert.Contains(t, out, "all pass", "Key should describe ✓")
	assert.Contains(t, out, "some failing", "Key should describe ✗")
	assert.Contains(t, out, "in progress", "Key should describe ●")
	assert.Contains(t, out, "not yet attempted", "Key should describe ○")
}

func TestRunStatusFolderSummary_CreateColumnAlwaysCheck(t *testing.T) {
	// Even with no automation and no execution, CREATE must show ✓ —
	// a TC existing in gtms/cases/ IS the creation.
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/cases", "feature", "tc-aaa1111-fresh.md"), `---
test_case_id: tc-aaa1111
title: Fresh test case
---
`)

	var buf bytes.Buffer
	err := runStatusFolderSummary(&buf, root, false, "")
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, output.IconComplete, "CREATE column must render ✓ for a folder with TCs")
	// No ✗ / ● / ○ should appear in CREATE — the only ○ that can appear is in
	// AUTOMATE / EXECUTE for this fresh-folder case.
}

func TestRunStatusFolderSummary_TCColumnHasDraftAnnotation(t *testing.T) {
	// ENH-066's draft annotation moves to the TC column under ENH-089.
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/cases", "feature", "tc-aaa1111-draft.md"), `---
test_case_id: tc-aaa1111
title: Draft Test
status: draft
---
`)
	writeTestFile(t, root, filepath.Join("gtms/cases", "feature", "tc-aaa2222-ready.md"), `---
test_case_id: tc-aaa2222
title: Ready Test
---
`)

	var buf bytes.Buffer
	err := runStatusFolderSummary(&buf, root, false, "")
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "2 (1 draft)", "Draft annotation belongs to TC column under ENH-089")
}

func TestRunStatusFolderSummary_FailureRendersXMark(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/cases", "checkout", "tc-aaa1111-checkout.md"), `---
test_case_id: tc-aaa1111
title: Checkout
---
`)
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-aaa1111", Framework: "bats", Result: "fail"})

	var buf bytes.Buffer
	err := runStatusFolderSummary(&buf, root, false, "")
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, output.IconError, "EXECUTE column should render ✗ for a failing folder")
	assert.Contains(t, out, "0/1", "Fraction should reflect 0 passing of 1 total")
}

func TestRunStatusFolderSummary_InFlightRendersFilledCircle(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/cases", "feature", "tc-aaa1111-test.md"), `---
test_case_id: tc-aaa1111
title: Test
---
`)
	// In-flight execute task — no automation record needed for in-flight detection
	writeTestFile(t, root, filepath.Join("gtms/tasks", "in-progress", "task-abc1234-execute-tc-aaa1111.md"), `---
id: task-abc1234
type: execute
target: tc-aaa1111
adapter: bats-runner
status: in-progress
created: 2026-04-16T12:00:00Z
branch: main
---
`)

	var buf bytes.Buffer
	err := runStatusFolderSummary(&buf, root, false, "")
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, output.IconInProgress,
		"EXECUTE column should render ● when an execute task is in-progress")
}

func TestRunStatusFolderSummary_JSONHasNewFields(t *testing.T) {
	// Backward-compatible additive change to JSON.
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/cases", "feature", "tc-aaa1111-pass.md"), `---
test_case_id: tc-aaa1111
title: Pass
---
`)
	writeTestFile(t, root, filepath.Join("gtms/cases", "feature", "tc-aaa2222-fail.md"), `---
test_case_id: tc-aaa2222
title: Fail
---
`)
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-aaa1111", Framework: "bats", Result: "pass"})
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-aaa2222", Framework: "bats", Result: "fail"})

	var buf bytes.Buffer
	err := runStatusFolderSummary(&buf, root, true, "")
	require.NoError(t, err)

	var entries []reader.FolderSummaryEntry
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entries))
	require.Len(t, entries, 1)

	// Existing fields preserved
	assert.Equal(t, 2, entries[0].Created)
	assert.Equal(t, 2, entries[0].Automated)
	assert.Equal(t, 2, entries[0].Executed)
	// New fields populated
	assert.Equal(t, 1, entries[0].Passing)
	assert.Equal(t, 1, entries[0].Failing)
	assert.Equal(t, 0, entries[0].Errored)
	assert.Equal(t, 0, entries[0].InFlight)

	// Confirm the JSON literal contains the new keys
	out := buf.String()
	assert.Contains(t, out, `"passing"`)
	assert.Contains(t, out, `"failing"`)
	assert.Contains(t, out, `"errored"`)
	assert.Contains(t, out, `"in_flight"`)
}

// --- ENH-077: diagnostic log block in detail view ---

// setupLogDetailFixture creates a TC with wiring + a terminal handoff
// carrying the log payload. CON-023 / ENH-145: notes / log lives on
// the result contract under the two-layer model, not on wiring (which
// is identity-only).
func setupLogDetailFixture(t *testing.T, lastResult, logBody, logSpill string) string {
	t.Helper()
	root := t.TempDir()

	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-logui01-failure.md"), `---
test_case_id: tc-logui01
title: Failing thing
requirement: ENH-077
---
`)
	writeTestFile(t, root, filepath.Join("gtms/automation", "specs", "tc-logui01.bats"), "# stub")

	// Wiring record (immutable identity).
	wDir := filepath.Join(root, "gtms", "automation", "wiring")
	require.NoError(t, os.MkdirAll(wDir, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(wDir, "tc-logui01--bats.wiring.yaml"),
		[]byte("testcase: tc-logui01\ntestcase-hash: 0011223344556677\nframework: bats\nadapter: bats-runner\nartefact: gtms/automation/specs/tc-logui01.bats\nartefact-hash: aabbccddeeff0011\n"), 0644))

	// Terminal handoff carries result + log payload.
	rDir := filepath.Join(root, ".gtms", "results")
	require.NoError(t, os.MkdirAll(rDir, 0755))
	var sb strings.Builder
	sb.WriteString("task: task-logui01\ncommand: execute\ntarget: tc-logui01\nadapter: bats-runner\nmode: sync\n")
	sb.WriteString("created: \"2026-05-19T10:00:00Z\"\n")
	sb.WriteString("status: complete\n")
	sb.WriteString("result: " + lastResult + "\n")
	sb.WriteString("framework: bats\n")
	sb.WriteString("completed: \"2026-05-19T10:01:00Z\"\n")
	if logBody != "" {
		sb.WriteString("log: |\n")
		for _, line := range strings.Split(strings.TrimRight(logBody, "\n"), "\n") {
			sb.WriteString("  " + line + "\n")
		}
	}
	if logSpill != "" {
		// CON-023: notes-spill lifted onto the result contract (alongside log).
		sb.WriteString("notes-spill: " + logSpill + "\n")
	}
	require.NoError(t, os.WriteFile(
		filepath.Join(rDir, "task-logui01.handoff.yaml"),
		[]byte(sb.String()), 0644))

	return root
}

func TestRunStatusDetail_ShowsLogBlockOnFail(t *testing.T) {
	root := setupLogDetailFixture(t, "fail",
		"not ok 1 - expected 1 got 0\n# in tc-logui01.bats line 12\n", "")

	var buf bytes.Buffer
	require.NoError(t, runStatusDetail(&buf, root, "tc-logui01", false, "", false))
	out := buf.String()

	assert.Contains(t, out, "Notes:", "detail view must render Notes: header on fail")
	assert.Contains(t, out, "  not ok 1 - expected 1 got 0",
		"log body must be indented two spaces")
	assert.Contains(t, out, "  # in tc-logui01.bats line 12")
}

func TestRunStatusDetail_ShowsLogBlockOnError(t *testing.T) {
	root := setupLogDetailFixture(t, "error", "pwsh: command not found\n", "")

	var buf bytes.Buffer
	require.NoError(t, runStatusDetail(&buf, root, "tc-logui01", false, "", false))
	out := buf.String()

	assert.Contains(t, out, "Notes:", "detail view must render Notes: header on error")
	assert.Contains(t, out, "  pwsh: command not found")
}

func TestRunStatusDetail_HidesLogBlockOnPass(t *testing.T) {
	// Pathological stale scenario: a record with pass-result still carrying
	// a log. The renderer must guard against this and hide the log.
	root := setupLogDetailFixture(t, "pass", "stale failure text\n", "")

	var buf bytes.Buffer
	require.NoError(t, runStatusDetail(&buf, root, "tc-logui01", false, "", false))
	out := buf.String()

	assert.NotContains(t, out, "Notes:",
		"pass result must not render a Log: block even when record has stale log")
	assert.NotContains(t, out, "stale failure text")
}

func TestRunStatusDetail_HidesLogBlockWhenLogEmpty(t *testing.T) {
	root := setupLogDetailFixture(t, "fail", "", "")

	var buf bytes.Buffer
	require.NoError(t, runStatusDetail(&buf, root, "tc-logui01", false, "", false))
	out := buf.String()

	assert.NotContains(t, out, "Notes:",
		"fail result with empty log must not render a Log: block")
}

func TestRunStatusDetail_TruncatedLogHeaderShowsSpillPath(t *testing.T) {
	root := setupLogDetailFixture(t, "fail",
		"partial head of a 128 KB log\n",
		".gtms/logs/task-huge01.log")

	var buf bytes.Buffer
	require.NoError(t, runStatusDetail(&buf, root, "tc-logui01", false, "", false))
	out := buf.String()

	assert.Contains(t, out, "Notes:  (truncated to 64 KB",
		"truncated header must include the 64 KB notice")
	assert.Contains(t, out, ".gtms/logs/task-huge01.log",
		"truncated header must point to the spill file")
}

func TestRunStatusDetail_JSONIncludesLogFieldsOnFail(t *testing.T) {
	root := setupLogDetailFixture(t, "fail", "not ok 1 - oops\n", ".gtms/logs/task-x.log")

	var buf bytes.Buffer
	require.NoError(t, runStatusDetail(&buf, root, "tc-logui01", true, "", false))

	var detail reader.PipelineDetailEntry
	require.NoError(t, json.Unmarshal(buf.Bytes(), &detail))
	assert.Contains(t, detail.Notes, "not ok 1 - oops")
	assert.Equal(t, ".gtms/logs/task-x.log", detail.NotesSpill)
}

func TestRunStatusDetail_JSONOmitsEmptyLogFields(t *testing.T) {
	root := setupLogDetailFixture(t, "fail", "", "")

	var buf bytes.Buffer
	require.NoError(t, runStatusDetail(&buf, root, "tc-logui01", true, "", false))

	// Raw JSON should not contain "log":"" or "log_spill":"" because the
	// fields have omitempty tags.
	raw := buf.String()
	assert.NotContains(t, raw, `"log":`, "empty log field should be omitted from JSON")
	assert.NotContains(t, raw, `"log_spill":`, "empty log_spill should be omitted from JSON")
}

// --- ENH-094: skipped status rendering ---

func TestFormatStageStatus_Skipped(t *testing.T) {
	result := formatStageStatus("skipped")
	assert.Equal(t, output.IconSkipped, result, "Skipped status must display ⊘ glyph")
}

func TestFormatLastResult_Skipped(t *testing.T) {
	result := formatLastResult("skipped", "", "bats")
	assert.Equal(t, "skipped [bats]", result, "Skipped result must render as 'skipped [framework]'")
}

func TestFormatExecuteLabel_Skipped(t *testing.T) {
	label := formatExecuteLabel("skipped", "skipped", "")
	assert.Equal(t, "Skipped", label, "Skipped execute label must capitalize")
}

func TestRunStatusOverview_SkippedRendering(t *testing.T) {
	root := t.TempDir()

	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-skip01-test.md"), `---
test_case_id: tc-skip01
title: Skipped Test
---
`)
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-skip01", Framework: "bats", Result: "skipped"})

	var buf bytes.Buffer
	err := runStatusOverview(&buf, root, nil, false, "", false)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, output.IconSkipped, "EXECUTE column must show ⊘ for skipped result")
	// CON-023 / ENH-145: handoff carries a completed timestamp so the cell
	// renders `skipped (<RFC3339>) [bats]`. Assert substrings separately.
	assert.Contains(t, out, "skipped ", "LAST RESULT must show 'skipped'")
	assert.Contains(t, out, "[bats]", "LAST RESULT must include framework annotation")
}

func TestRunStatusOverview_SkippedInJSON(t *testing.T) {
	root := t.TempDir()

	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-skip01-test.md"), `---
test_case_id: tc-skip01
title: Skipped Test
---
`)
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-skip01", Framework: "bats", Result: "skipped"})

	var buf bytes.Buffer
	err := runStatusOverview(&buf, root, nil, true, "", false)
	require.NoError(t, err)

	var entries []reader.PipelineEntry
	err = json.Unmarshal(buf.Bytes(), &entries)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	// CON-023 / ENH-146: LastResult / ExecuteStatus retired from JSON
	// (json:"-"). The skipped outcome surfaces via frameworks[].last_result_here.
	// The reader uses its legacy "skipped" vocabulary here (result-contract
	// vocab is "skip"); aligning the JSON field with the result-contract
	// vocabulary is a Phase 3C concern.
	require.Len(t, entries[0].Frameworks, 1)
	assert.Equal(t, "skipped", entries[0].Frameworks[0].LastResultHere,
		"JSON frameworks[].last_result_here must carry the skipped outcome")
	assert.Equal(t, "complete", entries[0].Frameworks[0].LastStatusHere,
		"adapter ran to completion → status:complete in the handoff")
}

func TestRunStatusOverview_KeyIncludesSkipped(t *testing.T) {
	root := setupStatusFixture(t)
	var buf bytes.Buffer

	err := runStatusOverview(&buf, root, nil, false, "", false)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "skipped", "Key footer must include 'skipped' description")
}

func TestFormatFolderExecuteCell_Skipped(t *testing.T) {
	e := reader.FolderSummaryEntry{Created: 3, Passing: 2, Skipped: 1}
	assert.Equal(t, output.IconSkipped+" 2/3", formatFolderExecuteCell(e),
		"Some skipped (no fails) → ⊘ N/M")
}

func TestFormatFolderExecuteCell_FailBeatsSkipped(t *testing.T) {
	e := reader.FolderSummaryEntry{Created: 3, Passing: 1, Failing: 1, Skipped: 1}
	assert.Equal(t, output.IconError+" 1/3", formatFolderExecuteCell(e),
		"Fail must outrank skipped (priority ✗ > ⊘)")
}

// --- BUG-078: status icon consistency tests ---

func TestBUG078_FormatStageStatus_ErrorReturnsErrorIcon(t *testing.T) {
	// BUG-078: ENH-130 "error" status must render as IconError (not IconWarning).
	// This collapses the display of legacy "failed" and ENH-130 "error" to the
	// same icon. The model-layer distinction is preserved for ENH-141.
	result := formatStageStatus("error")
	assert.Equal(t, output.IconError, result,
		"ENH-130 'error' status must render as IconError (✗), not IconWarning (⚠)")
}

func TestENH141_FormatStageStatus_FailedIsRetired(t *testing.T) {
	// ENH-141: "failed" is no longer a valid task lifecycle status.
	// It falls through to the default em-dash, same as any unknown value.
	result := formatStageStatus("failed")
	assert.Equal(t, "—", result,
		"Retired 'failed' status must render as em-dash (unknown)")
}

// --- BUG-080: statusHint rewrites to gtms prime when adapter is manual-prime ---

func TestStatusHint_ManualPrimeRewrite(t *testing.T) {
	// When the only registered automate adapter is manual-prime (and no default
	// is set), statusHint should suggest 'gtms prime' not 'gtms automate'.
	saved := appConfig
	defer func() { appConfig = saved }()

	appConfig = &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"automate": {
				"manual-prime": {Mode: "sync"},
			},
		},
	}

	entries := []reader.PipelineEntry{
		{TestCaseID: "tc-aaa1111", AutomateStatus: "none"},
	}

	hint := statusHint(entries)
	assert.Contains(t, hint, "gtms prime",
		"hint must use gtms prime when adapter is manual-prime")
	assert.Contains(t, hint, "--framework manual",
		"hint must include --framework manual flag")
	assert.NotContains(t, hint, "gtms automate",
		"hint must NOT suggest gtms automate when adapter is manual-prime")
}

func TestStatusHint_ManualPrimeDefaultRewrite(t *testing.T) {
	// Even when manual-prime is the configured DEFAULT (adapterHint returns ""),
	// statusHint must still rewrite to 'gtms prime'.
	saved := appConfig
	defer func() { appConfig = saved }()

	appConfig = &config.Config{
		Defaults: map[string]string{"automate": "manual-prime"},
		Adapters: map[string]map[string]*config.AdapterConfig{
			"automate": {
				"manual-prime": {Mode: "sync"},
			},
		},
	}

	entries := []reader.PipelineEntry{
		{TestCaseID: "tc-aaa1111", AutomateStatus: "none"},
	}

	hint := statusHint(entries)
	assert.Contains(t, hint, "gtms prime",
		"hint must use gtms prime even when manual-prime is the configured default")
	assert.NotContains(t, hint, "gtms automate",
		"hint must NOT suggest gtms automate when default is manual-prime")
}

func TestStatusHint_NonManualUnchanged(t *testing.T) {
	// When the automate adapter is bats-runner (non-manual), statusHint
	// should suggest 'gtms automate' as before.
	saved := appConfig
	defer func() { appConfig = saved }()

	appConfig = &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"automate": {
				"bats-runner": {Mode: "sync"},
			},
		},
	}

	entries := []reader.PipelineEntry{
		{TestCaseID: "tc-aaa1111", AutomateStatus: "none"},
	}

	hint := statusHint(entries)
	assert.Contains(t, hint, "gtms automate",
		"hint must use gtms automate for non-manual adapters")
	assert.NotContains(t, hint, "gtms prime",
		"hint must NOT suggest gtms prime for non-manual adapters")
}

func TestStatusHint_NoConfigNoPanic(t *testing.T) {
	// When appConfig is nil, statusHint must not panic.
	saved := appConfig
	defer func() { appConfig = saved }()

	appConfig = nil

	entries := []reader.PipelineEntry{
		{TestCaseID: "tc-aaa1111", AutomateStatus: "none"},
	}

	hint := statusHint(entries)
	assert.Contains(t, hint, "gtms automate",
		"nil config should fall through to default automate hint")
}

// --- BUG-097: ENH-150 prime-bucket shape also triggers the prime hint ---

func TestStatusHint_ENH150_DefaultsPrimeManualPrime(t *testing.T) {
	// ENH-150: when defaults.prime == "manual-prime" (the shape the minimal
	// preset ships under the peer adapters.prime bucket), the status hint
	// must rewrite to gtms prime even though manual-prime no longer lives
	// under adapters.automate.
	saved := appConfig
	defer func() { appConfig = saved }()

	appConfig = &config.Config{
		Defaults: map[string]string{
			"prime":   "manual-prime",
			"execute": "bats-runner",
		},
		Adapters: map[string]map[string]*config.AdapterConfig{
			"prime": {
				"manual-prime": {Mode: "sync"},
			},
			"execute": {
				"bats-runner": {Mode: "sync"},
			},
		},
	}

	entries := []reader.PipelineEntry{
		{TestCaseID: "tc-aaa1111", AutomateStatus: "none"},
	}

	hint := statusHint(entries)
	assert.Contains(t, hint, "gtms prime",
		"ENH-150: defaults.prime=manual-prime must route to gtms prime hint")
	assert.Contains(t, hint, "--framework manual",
		"hint must include --framework manual flag")
	assert.NotContains(t, hint, "gtms automate",
		"hint must NOT leak the legacy gtms automate wording")
}

func TestStatusHint_ENH150_SinglePrimeAdapterManualPrime(t *testing.T) {
	// ENH-150: when no defaults.automate is set AND adapters.prime has
	// exactly one entry (manual-prime), the status hint must rewrite to
	// gtms prime. This is the no-default minimal-preset shape exercised by
	// tc-d51d135d.
	saved := appConfig
	defer func() { appConfig = saved }()

	appConfig = &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"prime": {
				"manual-prime": {Mode: "sync"},
			},
		},
	}

	entries := []reader.PipelineEntry{
		{TestCaseID: "tc-aaa1111", AutomateStatus: "none"},
	}

	hint := statusHint(entries)
	assert.Contains(t, hint, "gtms prime",
		"ENH-150 single prime-bucket manual-prime must route to gtms prime hint")
	assert.Contains(t, hint, "--framework manual",
		"hint must include --framework manual flag")
}

func TestStatusHint_ENH150_NonManualAutomateDefaultSuppressesPrimeRewrite(t *testing.T) {
	// Mirror tc-5eab92a8: even if adapters.prime contains manual-prime, a
	// non-manual defaults.automate (e.g. local-claude) must suppress the
	// prime rewrite — the user has signalled they want an agent-driven
	// automate path.
	saved := appConfig
	defer func() { appConfig = saved }()

	appConfig = &config.Config{
		Defaults: map[string]string{
			"automate": "local-claude",
		},
		Adapters: map[string]map[string]*config.AdapterConfig{
			"automate": {
				"local-claude": {Mode: "sync"},
			},
			"prime": {
				"manual-prime": {Mode: "sync"},
			},
		},
	}

	entries := []reader.PipelineEntry{
		{TestCaseID: "tc-aaa1111", AutomateStatus: "none"},
	}

	hint := statusHint(entries)
	assert.Contains(t, hint, "gtms automate",
		"non-manual defaults.automate must keep the gtms automate hint")
	assert.NotContains(t, hint, "gtms prime",
		"non-manual defaults.automate must NOT rewrite to gtms prime")
}

func TestStatusHint_AmbiguousMultiAdapterNoDefault(t *testing.T) {
	// BUG-080 round-2: when no default is configured AND multiple automate
	// adapters are registered (one of which is manual-prime), the project is
	// ambiguous — not manual-only. The real adapter resolver
	// (internal/adapter/resolver.go) errors out in this configuration rather
	// than picking a first-registered, so the status hint must not assume
	// manual-prime either. Map iteration over cfg.Adapters["automate"] is
	// non-deterministic; without this guard the hint would flicker between
	// gtms prime and gtms automate across runs.
	saved := appConfig
	defer func() { appConfig = saved }()

	appConfig = &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"automate": {
				"manual-prime": {Mode: "sync"},
				"bats-runner":  {Mode: "sync"},
			},
		},
	}

	entries := []reader.PipelineEntry{
		{TestCaseID: "tc-aaa1111", AutomateStatus: "none"},
	}

	hint := statusHint(entries)
	assert.Contains(t, hint, "gtms automate",
		"ambiguous multi-adapter projects must fall through to gtms automate")
	assert.NotContains(t, hint, "gtms prime",
		"must not assume manual-only when multiple adapters are registered without a default")
}

func TestBUG078_StatusLegendUsesENH130Vocabulary(t *testing.T) {
	root := setupStatusFixture(t)
	var buf bytes.Buffer

	err := runStatusOverview(&buf, root, nil, false, "", false)
	require.NoError(t, err)

	out := buf.String()

	// New legend wording (BUG-078 / ENH-130 vocabulary).
	assert.Contains(t, out, "= complete")
	assert.Contains(t, out, "= error")
	assert.Contains(t, out, "= stale")
	assert.Contains(t, out, "= skipped")

	// Stale legend wording must be absent.
	assert.NotContains(t, out, "complete/pass",
		"Legend must not contain '/pass' — ✓ is status-tied (stage completed), not result-tied")
	assert.NotContains(t, out, "= failed",
		"Legend must use ENH-130 vocabulary 'error', not legacy 'failed'")
	assert.NotContains(t, out, "error/stale",
		"Warning icon is stale-only now that error has its own icon")
}

// --- BUG-082: CLI-level regression test ---
// These tests exercise the newStatusCmd / newGapsCmd RunE paths with a config
// where DefaultFramework returns "bats", proving that the CLI passes the
// explicit framework variable (empty string when no flag given) to the folder
// summary, NOT the config default. If status.go were reverted to pass
// defaultFw, the Pester-only folder would show as not automated and these
// tests would fail.

// withStatusGlobals overrides the package-level projectRoot and appConfig
// used by the status/gaps RunE closures, then returns a restore function.
func withStatusGlobals(t *testing.T, root string, cfg *config.Config) func() {
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

// batsDefaultConfig returns a config where DefaultFramework returns "bats"
// (via the default automate adapter having framework: bats).
func batsDefaultConfig() *config.Config {
	return &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"automate": {
				"bats-runner": {
					Framework: "bats", Mode: "sync",
					Command: "echo PASS",
				},
			},
		},
		Defaults: map[string]string{"automate": "bats-runner"},
	}
}

func TestBUG082_CLIStatusNoFramework_PesterOnlyCountsAsAutomated(t *testing.T) {
	// Fixture: config default framework is "bats", one TC with Pester-only wiring.
	// The no-arg status path must pass "" (not "bats") to PipelineFolderSummary
	// so the Pester folder shows as automated.
	root := t.TempDir()

	writeTestFile(t, root, filepath.Join("gtms/cases", "pester-only", "tc-aaa1111-pester.md"), `---
test_case_id: tc-aaa1111
title: Pester Test
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-aaa1111",
		Framework: "pester",
		Result:    "pass",
	})

	cfg := batsDefaultConfig()
	restore := withStatusGlobals(t, root, cfg)
	defer restore()

	// Invoke the status RunE with no args, no flags (the no-arg folder summary path).
	// The RunE writes to os.Stdout, so we capture it.
	cmd := newStatusCmd()

	var runErr error
	out := captureStdout(t, func() {
		runErr = cmd.RunE(cmd, []string{})
	})
	require.NoError(t, runErr)

	// The AUTOMATE column must show all-automated (check mark), not ○ 0/1.
	assert.NotContains(t, out, "0/1",
		"BUG-082 regression: Pester-only folder must NOT show 0/1 automated when config default is bats")
	assert.Contains(t, out, output.IconComplete,
		"BUG-082 regression: Pester-only folder AUTOMATE column must show check mark")
}

func TestBUG082_CLIStatusNoFramework_JSON_PesterOnlyCountsAsAutomated(t *testing.T) {
	root := t.TempDir()

	writeTestFile(t, root, filepath.Join("gtms/cases", "pester-only", "tc-aaa1111-pester.md"), `---
test_case_id: tc-aaa1111
title: Pester Test
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-aaa1111",
		Framework: "pester",
		Result:    "pass",
	})

	cfg := batsDefaultConfig()
	restore := withStatusGlobals(t, root, cfg)
	defer restore()

	cmd := newStatusCmd()
	require.NoError(t, cmd.Flags().Set("json", "true"))

	var runErr error
	out := captureStdout(t, func() {
		runErr = cmd.RunE(cmd, []string{})
	})
	require.NoError(t, runErr)

	var entries []reader.FolderSummaryEntry
	require.NoError(t, json.Unmarshal([]byte(out), &entries))
	require.Len(t, entries, 1)

	assert.Equal(t, "pester-only", entries[0].Folder)
	assert.Equal(t, 1, entries[0].Automated,
		"BUG-082 regression: Pester-only TC must be counted as automated in JSON when no --framework flag")
	assert.Equal(t, 0, entries[0].FrameworkMismatch,
		"BUG-082 regression: no framework filter means no mismatches")
}

func TestBUG082_CLIGapsNoFramework_PesterOnlyCountsAsAutomated(t *testing.T) {
	root := t.TempDir()

	writeTestFile(t, root, filepath.Join("gtms/cases", "pester-only", "tc-aaa1111-pester.md"), `---
test_case_id: tc-aaa1111
title: Pester Test
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-aaa1111",
		Framework: "pester",
		Result:    "pass",
	})

	cfg := batsDefaultConfig()
	restore := withStatusGlobals(t, root, cfg)
	defer restore()

	cmd := newGapsCmd()
	require.NoError(t, cmd.Flags().Set("json", "true"))

	var runErr error
	out := captureStdout(t, func() {
		runErr = cmd.RunE(cmd, []string{})
	})
	require.NoError(t, runErr)

	var entries []reader.GapsFolderSummaryEntry
	require.NoError(t, json.Unmarshal([]byte(out), &entries))
	require.Len(t, entries, 1)

	assert.Equal(t, "pester-only", entries[0].Folder)
	assert.Equal(t, 0, entries[0].NotAutomated,
		"BUG-082 regression: Pester-only TC must NOT be counted as not-automated when no --framework flag")
	assert.Equal(t, 0, entries[0].FrameworkMismatch,
		"BUG-082 regression: no framework filter means no mismatches")
}
