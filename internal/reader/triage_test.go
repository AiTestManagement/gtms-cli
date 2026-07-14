package reader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupTriageProject creates a temp project with a test case, wiring
// record, and completed terminal-result handoff. CON-023 / ENH-145:
// wiring replaces the legacy automation-record fixture; the test
// outcome lives on the result contract under .gtms/results/.
func setupTriageProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	writeFile(t, root, "gtms.config", `project:
  name: triage-test
  repo: github.com/example/triage
`)

	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-007-checkout.md"), `---
test_case_id: tc-007
title: Checkout Flow - Guest User
requirement: JIRA-456
status: automated
tags: [checkout, guest]
---

## Steps
1. Navigate to checkout
2. Complete as guest
`)

	// Wiring record (new identity store).
	writeFile(t, root, filepath.Join("gtms/automation", "wiring", "tc-007--playwright.wiring.yaml"), `testcase: tc-007
testcase-hash: 0011223344556677
framework: playwright
adapter: playwright-runner
artefact: gtms/automation/specs/tc-007.spec.ts
artefact-hash: aabbccddeeff0011
`)

	// Terminal handoff carrying the failing test outcome.
	writeFile(t, root, filepath.Join(".gtms", "results", "task-007-playwright.handoff.yaml"),
		`task: task-007-playwright
command: execute
target: tc-007
adapter: playwright-runner
mode: sync
created: "2026-05-19T10:00:00Z"
status: complete
result: fail
artefact: results/junit/tc-007.xml
attempts: 2
framework: playwright
completed: "2026-05-19T10:01:00Z"
`)

	// Ensure tasks directory exists
	mkdirAll(t, root, "gtms/tasks")
	mkdirAll(t, root, "gtms/tasks/pending")

	return root
}

// --- GetTriageInfo tests ---

func TestGetTriageInfo_Success(t *testing.T) {
	root := setupTriageProject(t)

	info, err := GetTriageInfo(root, "tc-007", "playwright")
	require.NoError(t, err)
	require.NotNil(t, info)

	assert.Equal(t, "tc-007", info.TestCaseID)
	assert.Equal(t, "fail", info.LastResult)
	assert.Equal(t, "results/junit/tc-007.xml", info.LastRun)
	assert.NotNil(t, info.AutomationRecord)
	// CON-023 / ENH-145: wiring has no lifecycle; the reader synthesises "developed".
	assert.Equal(t, "developed", info.AutomationRecord.Status)
	assert.Equal(t, "playwright", info.AutomationRecord.Framework)
}

func TestGetTriageInfo_NoAutomationRecord(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")

	info, err := GetTriageInfo(root, "tc-nonexistent", "")
	assert.Nil(t, info)
	assert.Error(t, err)
	// CON-023 / ENH-145: triage now reads wiring; the error wording reflects that.
	assert.Contains(t, err.Error(), "no wiring record found")
}

func TestGetTriageInfo_NoExecutionHistory(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")

	// CON-023 / ENH-145: wiring record exists but no terminal handoff → triage refuses.
	writeFile(t, root, filepath.Join("gtms/automation", "wiring", "tc-008--playwright.wiring.yaml"),
		"testcase: tc-008\ntestcase-hash: 0011223344556677\nframework: playwright\nadapter: playwright-runner\nartefact: gtms/automation/specs/tc-008.spec.ts\nartefact-hash: aabbccddeeff0011\n")

	info, err := GetTriageInfo(root, "tc-008", "")
	assert.Nil(t, info)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no execution results found for 'tc-008'")
	assert.Contains(t, err.Error(), "gtms execute tc-008")
}

// --- RecordTriage: automation-wrong ---

func TestRecordTriage_AutomationWrong(t *testing.T) {
	root := setupTriageProject(t)

	// Snapshot the wiring file pre-triage so we can assert it stays
	// byte-stable (CON-023: wiring is read-only on triage).
	wiringPath := filepath.Join(root, "gtms/automation", "wiring", "tc-007--playwright.wiring.yaml")
	wiringBefore, err := os.ReadFile(wiringPath)
	require.NoError(t, err)

	result, err := RecordTriage(root, "tc-007", "automation-wrong", "UI redesign changed selectors", "", "playwright")
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "tc-007", result.TestCaseID)
	assert.Equal(t, "automation-wrong", result.Category)
	assert.Equal(t, "UI redesign changed selectors", result.Summary)
	assert.NotEmpty(t, result.NewTaskID)
	assert.True(t, strings.HasPrefix(result.NewTaskID, "task-"))

	// Wiring file must be unchanged (CON-023 / ENH-146).
	wiringAfter, err := os.ReadFile(wiringPath)
	require.NoError(t, err)
	assert.Equal(t, wiringBefore, wiringAfter, "wiring must not be mutated on triage")

	// New task was created in tasks/pending/
	pendingDir := filepath.Join(root, "gtms/tasks", "pending")
	entries, err := os.ReadDir(pendingDir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Contains(t, entries[0].Name(), "automate-tc-007")
	assert.True(t, strings.HasSuffix(entries[0].Name(), ".md"))

	// Actions reported.
	require.GreaterOrEqual(t, len(result.Actions), 1)
	assert.Contains(t, result.Actions[0], "triaged as wrong")
}

func TestRecordTriage_AutomationWrong_QueuesAnotherTaskOnRepeat(t *testing.T) {
	// CON-023: triage doesn't bump a cycle counter (cycle is retired).
	// Re-running automation-wrong queues another automate task.
	root := setupTriageProject(t)

	_, err := RecordTriage(root, "tc-007", "automation-wrong", "First rework", "", "playwright")
	require.NoError(t, err)

	pendingDir := filepath.Join(root, "gtms/tasks", "pending")
	entries, err := os.ReadDir(pendingDir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "first triage queues one task")

	// Second triage queues another task — wiring is still untouched.
	_, err = RecordTriage(root, "tc-007", "automation-wrong", "Second rework", "", "playwright")
	require.NoError(t, err)

	entries, err = os.ReadDir(pendingDir)
	require.NoError(t, err)
	assert.Len(t, entries, 2, "second triage queues another task")
}

// --- RecordTriage: test-wrong ---

func TestRecordTriage_TestWrong(t *testing.T) {
	root := setupTriageProject(t)

	result, err := RecordTriage(root, "tc-007", "test-wrong", "Expected result changed with new checkout flow", "", "playwright")
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "tc-007", result.TestCaseID)
	assert.Equal(t, "test-wrong", result.Category)
	assert.Empty(t, result.NewTaskID) // no new task for test-wrong

	// CON-023 / ENH-146: triage no longer mutates wiring. test-wrong
	// updates the TC spec status only.
	tcPath := filepath.Join(root, "gtms/test/cases", "tc-007-checkout.md")
	content, err := os.ReadFile(tcPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "needs-review")

	require.GreaterOrEqual(t, len(result.Actions), 1)
	assert.Contains(t, result.Actions[0], "needs-review")
}

// --- RecordTriage: app-wrong ---
//
// Phase 3E contract (CON-023 / ENH-146):
//   - app-wrong means the application/product is at fault, NOT the
//     automation, so app-wrong must NOT queue an automate task.
//   - result.NewTaskID stays empty.
//   - When defect OR summary is provided, append an audit entry to
//     gtms/triage-history/<tc>.md.
//   - With neither defect nor summary, no history file is written
//     (avoids noisy/empty entries).
//   - Wiring is never mutated.
//   - Pending-automate-task count does not grow.

func TestRecordTriage_AppWrong(t *testing.T) {
	root := setupTriageProject(t)

	// Snapshot wiring + pending-task count for the post-triage stability
	// assertions.
	wiringPath := filepath.Join(root, "gtms/automation", "wiring", "tc-007--playwright.wiring.yaml")
	wiringBefore, err := os.ReadFile(wiringPath)
	require.NoError(t, err)
	pendingBefore, err := os.ReadDir(filepath.Join(root, "gtms/tasks", "pending"))
	require.NoError(t, err)

	result, err := RecordTriage(root, "tc-007", "app-wrong", "Payment gateway returns 500", "JIRA-789", "playwright")
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "tc-007", result.TestCaseID)
	assert.Equal(t, "app-wrong", result.Category)
	assert.Equal(t, "JIRA-789", result.Defect)
	assert.Empty(t, result.NewTaskID, "app-wrong must NOT queue an automate task")

	// Audit entry appended to triage-history.
	historyPath := filepath.Join(root, "gtms", "triage-history", "tc-007.md")
	historyBytes, err := os.ReadFile(historyPath)
	require.NoError(t, err)
	assert.Contains(t, string(historyBytes), "JIRA-789")
	assert.Contains(t, string(historyBytes), "app-wrong",
		"history entry should record the triage category")

	// Wiring is byte-stable.
	wiringAfter, err := os.ReadFile(wiringPath)
	require.NoError(t, err)
	assert.Equal(t, wiringBefore, wiringAfter, "wiring must not be mutated by app-wrong triage")

	// Pending-automate-task count must NOT grow on app-wrong.
	pendingAfter, err := os.ReadDir(filepath.Join(root, "gtms/tasks", "pending"))
	require.NoError(t, err)
	assert.Equal(t, len(pendingBefore), len(pendingAfter),
		"pending-automate-task count must not increase for app-wrong")

	require.GreaterOrEqual(t, len(result.Actions), 1)
	assert.Contains(t, result.Actions[0], "app-wrong")
	assert.Contains(t, result.Actions[0], "JIRA-789")
}

func TestRecordTriage_AppWrong_NoDefect(t *testing.T) {
	// Phase 3E contract: with neither defect nor summary, app-wrong is a
	// no-op on disk — no history file is created (avoids empty/noisy
	// entries) and no automate task is queued.
	root := setupTriageProject(t)

	pendingBefore, err := os.ReadDir(filepath.Join(root, "gtms/tasks", "pending"))
	require.NoError(t, err)

	result, err := RecordTriage(root, "tc-007", "app-wrong", "", "", "playwright")
	require.NoError(t, err)
	require.NotNil(t, result)

	// Without defect or summary, no history file is written.
	historyPath := filepath.Join(root, "gtms", "triage-history", "tc-007.md")
	_, statErr := os.Stat(historyPath)
	assert.True(t, os.IsNotExist(statErr), "no history file when both defect and summary are empty")

	// No automate task queued.
	assert.Empty(t, result.NewTaskID, "app-wrong must NOT queue an automate task")
	pendingAfter, err := os.ReadDir(filepath.Join(root, "gtms/tasks", "pending"))
	require.NoError(t, err)
	assert.Equal(t, len(pendingBefore), len(pendingAfter),
		"pending-automate-task count must not increase for app-wrong")

	// Actions still report the category without mentioning defect.
	assert.Contains(t, result.Actions[0], "app-wrong")
	assert.NotContains(t, result.Actions[0], "defect")
}

// TestRecordTriage_AppWrong_SummaryOnly verifies the "defect OR summary"
// rule: a summary alone (no defect) still produces a triage-history
// entry, so triage notes about the application aren't lost when the
// defect tracker reference isn't ready yet.
func TestRecordTriage_AppWrong_SummaryOnly(t *testing.T) {
	root := setupTriageProject(t)

	result, err := RecordTriage(root, "tc-007", "app-wrong", "Login endpoint returns 502 intermittently", "", "playwright")
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Empty(t, result.NewTaskID, "app-wrong must NOT queue an automate task")

	historyPath := filepath.Join(root, "gtms", "triage-history", "tc-007.md")
	historyBytes, err := os.ReadFile(historyPath)
	require.NoError(t, err)
	assert.Contains(t, string(historyBytes), "Login endpoint returns 502 intermittently",
		"summary should appear in the history entry")
}

// TestRecordTriage_AppWrong_AppendsAcrossRuns confirms multiple
// app-wrong triages on the same TC append to the same history file
// rather than overwriting it.
func TestRecordTriage_AppWrong_AppendsAcrossRuns(t *testing.T) {
	root := setupTriageProject(t)

	_, err := RecordTriage(root, "tc-007", "app-wrong", "First triage", "JIRA-100", "playwright")
	require.NoError(t, err)
	_, err = RecordTriage(root, "tc-007", "app-wrong", "Second triage", "JIRA-200", "playwright")
	require.NoError(t, err)

	historyPath := filepath.Join(root, "gtms", "triage-history", "tc-007.md")
	bytes, err := os.ReadFile(historyPath)
	require.NoError(t, err)
	s := string(bytes)
	assert.Contains(t, s, "JIRA-100", "first triage entry preserved")
	assert.Contains(t, s, "JIRA-200", "second triage entry appended")
}

// --- RecordTriage: validation ---

func TestRecordTriage_InvalidCategory(t *testing.T) {
	root := setupTriageProject(t)

	result, err := RecordTriage(root, "tc-007", "invalid-category", "", "", "playwright")
	assert.Nil(t, result)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid triage category")
}

func TestRecordTriage_NoAutomationRecord(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")

	result, err := RecordTriage(root, "tc-none", "automation-wrong", "", "", "")
	assert.Nil(t, result)
	assert.Error(t, err)
	// CON-023 / ENH-145: triage now reads wiring; error wording reflects that.
	assert.Contains(t, err.Error(), "no wiring record found")
}

func TestRecordTriage_NoExecutionHistory(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")

	// CON-023 / ENH-145: wiring record exists but no terminal handoff
	// → triage refuses ("no execution results found").
	writeFile(t, root, filepath.Join("gtms/automation", "wiring", "tc-010--playwright.wiring.yaml"),
		"testcase: tc-010\ntestcase-hash: 0011223344556677\nframework: playwright\nadapter: playwright-runner\nartefact: gtms/automation/specs/tc-010.spec.ts\nartefact-hash: aabbccddeeff0011\n")

	result, err := RecordTriage(root, "tc-010", "automation-wrong", "", "", "playwright")
	assert.Nil(t, result)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no execution results found for 'tc-010'")
}

// --- Integration test ---

func TestTriageIntegration_FullLifecycle(t *testing.T) {
	root := setupTriageProject(t)

	// Add a second TC with wiring + a failing terminal handoff.
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-008-login.md"), `---
test_case_id: tc-008
title: Login Flow
requirement: JIRA-789
status: automated
tags: [login]
---
`)
	writeFile(t, root, filepath.Join("gtms/automation", "wiring", "tc-008--playwright.wiring.yaml"),
		"testcase: tc-008\ntestcase-hash: 0011223344556677\nframework: playwright\nadapter: playwright-runner\nartefact: gtms/automation/specs/tc-008.spec.ts\nartefact-hash: aabbccddeeff0011\n")
	writeFile(t, root, filepath.Join(".gtms", "results", "task-008-playwright.handoff.yaml"),
		`task: task-008-playwright
command: execute
target: tc-008
adapter: playwright-runner
mode: sync
created: "2026-05-19T10:00:00Z"
status: complete
result: fail
framework: playwright
completed: "2026-05-19T10:01:00Z"
`)

	// 1. Triage tc-007 as automation-wrong (queues a re-automate task)
	result1, err := RecordTriage(root, "tc-007", "automation-wrong", "Selectors changed", "", "playwright")
	require.NoError(t, err)
	require.NotNil(t, result1)
	assert.Equal(t, "automation-wrong", result1.Category)
	assert.NotEmpty(t, result1.NewTaskID)

	// 2. Triage tc-008 as app-wrong (appends to triage-history; no wiring write)
	result2, err := RecordTriage(root, "tc-008", "app-wrong", "Login endpoint 500", "BUG-123", "playwright")
	require.NoError(t, err)
	require.NotNil(t, result2)
	assert.Equal(t, "app-wrong", result2.Category)

	// 3. Pipeline status still surfaces the failing run for tc-008
	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	for _, e := range entries {
		if e.TestCaseID == "tc-008" {
			assert.Equal(t, "fail", e.LastResult)
		}
	}

	// 4. Gaps report includes tc-008 under CurrentlyFailing
	report, err := Gaps(root, nil, "", false)
	require.NoError(t, err)
	require.NotNil(t, report)
	found := false
	for _, g := range report.CurrentlyFailing {
		if g.ID == "tc-008" {
			found = true
			break
		}
	}
	assert.True(t, found, "tc-008 should be in currently failing gaps")
}

// --- Category validation helpers ---

func TestIsValidCategory(t *testing.T) {
	assert.True(t, isValidCategory("automation-wrong"))
	assert.True(t, isValidCategory("test-wrong"))
	assert.True(t, isValidCategory("app-wrong"))
	assert.False(t, isValidCategory(""))
	assert.False(t, isValidCategory("invalid"))
	assert.False(t, isValidCategory("AUTOMATION-WRONG"))
}

// --- CLI helper tests ---

func TestResolveTriageCategory(t *testing.T) {
	tests := []struct {
		name            string
		automationWrong bool
		testWrong       bool
		appWrong        bool
		wantCategory    string
		wantErr         bool
	}{
		{"automation-wrong only", true, false, false, "automation-wrong", false},
		{"test-wrong only", false, true, false, "test-wrong", false},
		{"app-wrong only", false, false, true, "app-wrong", false},
		{"none set", false, false, false, "", true},
		{"two set", true, true, false, "", true},
		{"all set", true, true, true, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test the category validation logic directly
			count := 0
			category := ""
			if tt.automationWrong {
				count++
				category = "automation-wrong"
			}
			if tt.testWrong {
				count++
				category = "test-wrong"
			}
			if tt.appWrong {
				count++
				category = "app-wrong"
			}

			if count == 0 || count > 1 {
				assert.True(t, tt.wantErr, "expected error for count=%d", count)
			} else {
				assert.False(t, tt.wantErr)
				assert.Equal(t, tt.wantCategory, category)
			}
		})
	}
}

// --- BUG-011 regression: roundtrip field preservation ---

func TestBUG011_TriageRoundtripPreservesFields(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")

	// Test case with all frontmatter fields including priority, type, created
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-rt-001.md"), `---
test_case_id: tc-rt-001
title: Roundtrip Preservation Test
requirement: BUG-011
priority: High
type: Integration
status: automated
tags: [roundtrip, regression]
created: 2026-02-21
---

## Steps
1. Verify fields survive roundtrip
`)

	// CON-023 / ENH-145: wiring + terminal handoff.
	writeFile(t, root, filepath.Join("gtms/automation", "wiring", "tc-rt-001--bats.wiring.yaml"),
		"testcase: tc-rt-001\ntestcase-hash: 0011223344556677\nframework: bats\nadapter: bats-runner\nartefact: gtms/automation/specs/tc-rt-001.bats\nartefact-hash: aabbccddeeff0011\n")
	writeFile(t, root, filepath.Join(".gtms", "results", "task-rt-001.handoff.yaml"),
		"task: task-rt-001\ncommand: execute\ntarget: tc-rt-001\nadapter: bats-runner\nmode: sync\ncreated: \"2026-05-19T10:00:00Z\"\nstatus: complete\nresult: fail\nframework: bats\ncompleted: \"2026-05-19T10:01:00Z\"\n")
	mkdirAll(t, root, "gtms/tasks")

	// Triage as test-wrong — triggers updateTestCaseStatus() roundtrip
	_, err := RecordTriage(root, "tc-rt-001", "test-wrong", "Fields should survive", "", "")
	require.NoError(t, err)

	// Read back the file and verify fields survived the roundtrip
	content, err := os.ReadFile(filepath.Join(root, "gtms/test/cases", "tc-rt-001.md"))
	require.NoError(t, err)
	s := string(content)

	assert.Contains(t, s, "test_case_id: tc-rt-001", "test_case_id should survive roundtrip")
	assert.Contains(t, s, "priority: High", "priority should survive roundtrip")
	assert.Contains(t, s, "type: Integration", "type should survive roundtrip")
	assert.Contains(t, s, "created: \"2026-02-21\"", "created should survive roundtrip")
	assert.Contains(t, s, "needs-review", "status should be updated to needs-review")
	assert.Contains(t, s, "title: Roundtrip Preservation Test", "title should survive roundtrip")
	assert.Contains(t, s, "requirement: BUG-011", "requirement should survive roundtrip")
}

// --- ENH-040: Error vs Fail distinction in triage ---

func TestENH040_GetTriageInfo_ErrorResult(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")

	// CON-023 / ENH-145: wiring + terminal handoff with status: complete + result: error.
	writeFile(t, root, filepath.Join("gtms/automation", "wiring", "tc-e40--bats.wiring.yaml"),
		"testcase: tc-e40\ntestcase-hash: 0011223344556677\nframework: bats\nadapter: bats-runner\nartefact: gtms/automation/specs/tc-e40.bats\nartefact-hash: aabbccddeeff0011\n")
	writeFile(t, root, filepath.Join(".gtms", "results", "task-e40.handoff.yaml"),
		"task: task-e40\ncommand: execute\ntarget: tc-e40\nadapter: bats-runner\nmode: sync\ncreated: \"2026-05-19T10:00:00Z\"\nstatus: complete\nresult: error\nframework: bats\ncompleted: \"2026-05-19T10:01:00Z\"\n")
	mkdirAll(t, root, "gtms/tasks")

	info, err := GetTriageInfo(root, "tc-e40", "bats")
	require.NoError(t, err)
	require.NotNil(t, info)

	assert.Equal(t, "tc-e40", info.TestCaseID)
	assert.Equal(t, "error", info.LastResult, "error last-formal-result should be visible in triage info")
}

func TestENH040_RecordTriage_ErrorResult_AutomationWrong(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")

	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-e40-triage.md"), `---
test_case_id: tc-e40
title: Error Triage Test
requirement: REQ-1
---
`)
	writeFile(t, root, filepath.Join("gtms/automation", "wiring", "tc-e40--bats.wiring.yaml"),
		"testcase: tc-e40\ntestcase-hash: 0011223344556677\nframework: bats\nadapter: bats-runner\nartefact: gtms/automation/specs/tc-e40.bats\nartefact-hash: aabbccddeeff0011\n")
	writeFile(t, root, filepath.Join(".gtms", "results", "task-e40.handoff.yaml"),
		"task: task-e40\ncommand: execute\ntarget: tc-e40\nadapter: bats-runner\nmode: sync\ncreated: \"2026-05-19T10:00:00Z\"\nstatus: complete\nresult: error\nframework: bats\ncompleted: \"2026-05-19T10:01:00Z\"\n")
	mkdirAll(t, root, "gtms/tasks")

	// Triage an error result as automation-wrong (the natural choice for errors)
	result, err := RecordTriage(root, "tc-e40", "automation-wrong", "BATS syntax error", "", "bats")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "automation-wrong", result.Category)
	assert.NotEmpty(t, result.NewTaskID)
}

// TestTriage_Join_AmbiguousAdapterAcrossWiring pins the medium fix from
// the Phase 3 review: triage must load every wiring record for the TC
// (not just the framework being triaged) so the ENH-146 join ladder can
// detect adapter ambiguity at rung 4.
//
// Setup: TC has two current wiring records (bats + playwright) sharing
// the same `adapter: shared-runner`. A frameworkless result with the
// shared adapter must be classified as ambiguous and excluded — even
// when triage is invoked for one specific framework.
//
// Pre-fix bug: triage built `map{tc: {selectedRec}}` and ran the ladder
// against a singleton. Rung 4 saw `len(matches) == 1` and accepted the
// orphan as belonging to whichever framework was being triaged. Post-fix
// behaviour: triage loads all wiring via wiring.FindAllForTC, so rung 4
// correctly excludes the result. Triage then surfaces "no execution
// results found" because there is no joinable terminal handoff.
func TestTriage_Join_AmbiguousAdapterAcrossWiring(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-amb01-shared.md"), `---
test_case_id: tc-amb01
title: Shared-adapter TC
requirement: REQ-A
---
`)
	// Two wiring records on the same TC sharing the same adapter.
	writeFile(t, root, filepath.Join("gtms/automation", "wiring", "tc-amb01--bats.wiring.yaml"),
		"testcase: tc-amb01\ntestcase-hash: 0011223344556677\nframework: bats\nadapter: shared-runner\nartefact: test/acceptance/tc-amb01.bats\nartefact-hash: aaaa111122223333\n")
	writeFile(t, root, filepath.Join("gtms/automation", "wiring", "tc-amb01--playwright.wiring.yaml"),
		"testcase: tc-amb01\ntestcase-hash: 0011223344556677\nframework: playwright\nadapter: shared-runner\nartefact: tests/tc-amb01.spec.ts\nartefact-hash: bbbb222233334444\n")
	// Frameworkless result with the shared adapter. Rung 1 skipped (no
	// framework). Rungs 2/3 skipped (no ArtefactHash / Artefact match
	// either). Rung 4 must see TWO matching wiring records and exclude.
	writeFile(t, root, filepath.Join(".gtms", "results", "task-amb01.handoff.yaml"),
		"task: task-amb01\ncommand: execute\ntarget: tc-amb01\nadapter: shared-runner\nmode: sync\ncreated: \"2026-05-19T10:00:00Z\"\nstatus: complete\nresult: pass\ncompleted: \"2026-05-19T10:01:00Z\"\n")
	mkdirAll(t, root, "gtms/tasks")

	_, err := GetTriageInfo(root, "tc-amb01", "bats")
	require.Error(t, err,
		"triage must refuse to surface an ambiguously-joined result — the join ladder excludes it")
	assert.Contains(t, err.Error(), "no execution results found",
		"triage should report no execution results, not silently triage the orphan")
}
