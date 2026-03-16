package reader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
)

// setupTriageProject creates a temp project with a test case, automation record,
// and completed execution (last-formal-result set).
func setupTriageProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	writeFile(t, root, "gtms.config", `project:
  name: triage-test
  repo: github.com/example/triage
`)

	// Test case file
	writeFile(t, root, filepath.Join("test-cases", "tc-007-checkout.md"), `---
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

	// Automation record with execution history
	writeFile(t, root, filepath.Join("test-automation", "records", "tc-007.automation.md"), `---
testcase: tc-007
framework: playwright
status: accepted
artefact: test-automation/specs/tc-007.spec.ts
adapter: local-claude
last-dev-result: pass
last-formal-result: fail
last-formal-run: results/junit/tc-007.xml
attempts: 2
cycle: 1
---
`)

	// Ensure tasks directory exists
	mkdirAll(t, root, "test-tasks")

	return root
}

// --- GetTriageInfo tests ---

func TestGetTriageInfo_Success(t *testing.T) {
	root := setupTriageProject(t)

	info, err := GetTriageInfo(root, "tc-007")
	require.NoError(t, err)
	require.NotNil(t, info)

	assert.Equal(t, "tc-007", info.TestCaseID)
	assert.Equal(t, "fail", info.LastResult)
	assert.Equal(t, "results/junit/tc-007.xml", info.LastRun)
	assert.NotNil(t, info.AutomationRecord)
	assert.Equal(t, "accepted", info.AutomationRecord.Status)
	assert.Equal(t, "playwright", info.AutomationRecord.Framework)
}

func TestGetTriageInfo_NoAutomationRecord(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")

	info, err := GetTriageInfo(root, "tc-nonexistent")
	assert.Nil(t, info)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no automation record found")
}

func TestGetTriageInfo_NoExecutionHistory(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")

	// Automation record without execution results
	writeFile(t, root, filepath.Join("test-automation", "records", "tc-008.automation.md"), `---
testcase: tc-008
framework: playwright
status: accepted
artefact: test-automation/specs/tc-008.spec.ts
adapter: local-claude
last-dev-result: pass
attempts: 1
cycle: 1
---
`)

	info, err := GetTriageInfo(root, "tc-008")
	assert.Nil(t, info)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no execution results found for 'tc-008'")
	assert.Contains(t, err.Error(), "gtms execute tc-008")
}

// --- RecordTriage: automation-wrong ---

func TestRecordTriage_AutomationWrong(t *testing.T) {
	root := setupTriageProject(t)

	result, err := RecordTriage(root, "tc-007", "automation-wrong", "UI redesign changed selectors", "")
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "tc-007", result.TestCaseID)
	assert.Equal(t, "automation-wrong", result.Category)
	assert.Equal(t, "UI redesign changed selectors", result.Summary)
	assert.NotEmpty(t, result.NewTaskID)
	assert.True(t, strings.HasPrefix(result.NewTaskID, "task-"))

	// Verify automation record was updated
	record, _, err := pipeline.FindAutomationRecord(root, "tc-007")
	require.NoError(t, err)
	require.NotNil(t, record)
	assert.Equal(t, "rework", record.Status)
	assert.Equal(t, 2, record.Cycle) // was 1, now 2

	// Verify new task was created in tasks/pending/
	pendingDir := filepath.Join(root, "test-tasks", "pending")
	entries, err := os.ReadDir(pendingDir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.True(t, strings.Contains(entries[0].Name(), "automate-tc-007"))
	assert.True(t, strings.HasSuffix(entries[0].Name(), ".md"))

	// Verify actions reported
	require.Len(t, result.Actions, 2)
	assert.Contains(t, result.Actions[0], "rework")
	assert.Contains(t, result.Actions[0], "cycle 2")
	assert.Contains(t, result.Actions[1], "New task created")
}

func TestRecordTriage_AutomationWrong_IncrementsCycle(t *testing.T) {
	root := setupTriageProject(t)

	// First triage
	_, err := RecordTriage(root, "tc-007", "automation-wrong", "First rework", "")
	require.NoError(t, err)

	// Simulate: re-execute and get another failure
	// Reset the record to have a formal result again so triage can be re-run
	recordPath := filepath.Join(root, "test-automation", "records", "tc-007.automation.md")
	record, err := pipeline.ReadAutomationRecord(recordPath)
	require.NoError(t, err)
	record.LastFormalResult = "fail"
	require.NoError(t, pipeline.WriteAutomationRecord(recordPath, record))

	// Second triage
	result, err := RecordTriage(root, "tc-007", "automation-wrong", "Second rework", "")
	require.NoError(t, err)

	// Verify cycle incremented to 3
	record, _, err = pipeline.FindAutomationRecord(root, "tc-007")
	require.NoError(t, err)
	assert.Equal(t, 3, record.Cycle)
	assert.Contains(t, result.Actions[0], "cycle 3")
}

// --- RecordTriage: test-wrong ---

func TestRecordTriage_TestWrong(t *testing.T) {
	root := setupTriageProject(t)

	result, err := RecordTriage(root, "tc-007", "test-wrong", "Expected result changed with new checkout flow", "")
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "tc-007", result.TestCaseID)
	assert.Equal(t, "test-wrong", result.Category)
	assert.Empty(t, result.NewTaskID) // no new task for test-wrong

	// Verify automation record was updated
	record, _, err := pipeline.FindAutomationRecord(root, "tc-007")
	require.NoError(t, err)
	require.NotNil(t, record)
	assert.Equal(t, "test-wrong", record.Status)

	// Verify test case file was updated
	tcPath := filepath.Join(root, "test-cases", "tc-007-checkout.md")
	content, err := os.ReadFile(tcPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "needs-review")

	// Verify actions reported
	require.Len(t, result.Actions, 2)
	assert.Contains(t, result.Actions[0], "test-wrong")
	assert.Contains(t, result.Actions[1], "needs-review")
}

// --- RecordTriage: app-wrong ---

func TestRecordTriage_AppWrong(t *testing.T) {
	root := setupTriageProject(t)

	result, err := RecordTriage(root, "tc-007", "app-wrong", "Payment gateway returns 500", "JIRA-789")
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "tc-007", result.TestCaseID)
	assert.Equal(t, "app-wrong", result.Category)
	assert.Equal(t, "JIRA-789", result.Defect)
	assert.Empty(t, result.NewTaskID) // no new task for app-wrong

	// Verify automation record was updated
	record, _, err := pipeline.FindAutomationRecord(root, "tc-007")
	require.NoError(t, err)
	require.NotNil(t, record)
	assert.Equal(t, "fail", record.LastFormalResult)
	assert.Equal(t, "JIRA-789", record.Defect)

	// Verify actions reported
	require.Len(t, result.Actions, 1)
	assert.Contains(t, result.Actions[0], "fail")
	assert.Contains(t, result.Actions[0], "JIRA-789")
}

func TestRecordTriage_AppWrong_NoDefect(t *testing.T) {
	root := setupTriageProject(t)

	result, err := RecordTriage(root, "tc-007", "app-wrong", "Unknown failure", "")
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify defect is not set
	record, _, err := pipeline.FindAutomationRecord(root, "tc-007")
	require.NoError(t, err)
	assert.Equal(t, "", record.Defect)

	// Actions should not mention defect
	assert.Contains(t, result.Actions[0], "fail")
	assert.NotContains(t, result.Actions[0], "defect")
}

// --- RecordTriage: validation ---

func TestRecordTriage_InvalidCategory(t *testing.T) {
	root := setupTriageProject(t)

	result, err := RecordTriage(root, "tc-007", "invalid-category", "", "")
	assert.Nil(t, result)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid triage category")
}

func TestRecordTriage_NoAutomationRecord(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")

	result, err := RecordTriage(root, "tc-none", "automation-wrong", "", "")
	assert.Nil(t, result)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no automation record found")
}

func TestRecordTriage_NoExecutionHistory(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")

	writeFile(t, root, filepath.Join("test-automation", "records", "tc-010.automation.md"), `---
testcase: tc-010
framework: playwright
status: accepted
artefact: test-automation/specs/tc-010.spec.ts
adapter: local-claude
last-dev-result: pass
attempts: 1
cycle: 1
---
`)

	result, err := RecordTriage(root, "tc-010", "automation-wrong", "", "")
	assert.Nil(t, result)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no execution results found for 'tc-010'")
}

// --- Integration test ---

func TestTriageIntegration_FullLifecycle(t *testing.T) {
	root := setupTriageProject(t)

	// Add a second test case for variety
	writeFile(t, root, filepath.Join("test-cases", "tc-008-login.md"), `---
test_case_id: tc-008
title: Login Flow
requirement: JIRA-789
status: automated
tags: [login]
---

## Steps
1. Navigate to login page
2. Enter credentials
`)

	writeFile(t, root, filepath.Join("test-automation", "records", "tc-008.automation.md"), `---
testcase: tc-008
framework: playwright
status: accepted
artefact: test-automation/specs/tc-008.spec.ts
adapter: local-claude
last-dev-result: pass
last-formal-result: fail
last-formal-run: results/junit/tc-008.xml
attempts: 1
cycle: 1
---
`)

	// 1. Triage tc-007 as automation-wrong
	result1, err := RecordTriage(root, "tc-007", "automation-wrong", "Selectors changed", "")
	require.NoError(t, err)
	require.NotNil(t, result1)
	assert.Equal(t, "automation-wrong", result1.Category)
	assert.NotEmpty(t, result1.NewTaskID)

	// 2. Triage tc-008 as app-wrong with defect
	result2, err := RecordTriage(root, "tc-008", "app-wrong", "Login endpoint 500", "BUG-123")
	require.NoError(t, err)
	require.NotNil(t, result2)
	assert.Equal(t, "app-wrong", result2.Category)

	// 3. Verify pipeline status reflects the changes
	entries, err := PipelineStatus(root, nil)
	require.NoError(t, err)
	require.Len(t, entries, 2)

	// tc-007: automation record status is "rework" after triage, visible in AUTOMATE stage
	// A pending automate task was also created, but deriveAutomateStatus returns the
	// record status ("rework") and applyTaskStatus only overrides "none" for pending tasks.
	for _, e := range entries {
		if e.TestCaseID == "tc-007" {
			assert.Equal(t, "rework", e.AutomateStatus, "tc-007 should show rework automate status")
		}
		if e.TestCaseID == "tc-008" {
			// tc-008 was triaged as app-wrong, last-formal-result = fail
			assert.Equal(t, "fail", e.LastResult)
		}
	}

	// 4. Verify gaps report reflects failing test
	report, err := Gaps(root, []string{}, nil)
	require.NoError(t, err)
	require.NotNil(t, report)

	// tc-008 should appear in currently failing
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
	writeFile(t, root, filepath.Join("test-cases", "tc-rt-001.md"), `---
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

	writeFile(t, root, filepath.Join("test-automation", "records", "tc-rt-001.automation.md"), `---
testcase: tc-rt-001
framework: bats
status: accepted
artefact: test-automation/specs/tc-rt-001.bats
adapter: local-claude
last-formal-result: fail
attempts: 1
cycle: 1
---
`)
	mkdirAll(t, root, "test-tasks")

	// Triage as test-wrong — triggers updateTestCaseStatus() roundtrip
	_, err := RecordTriage(root, "tc-rt-001", "test-wrong", "Fields should survive", "")
	require.NoError(t, err)

	// Read back the file and verify fields survived the roundtrip
	content, err := os.ReadFile(filepath.Join(root, "test-cases", "tc-rt-001.md"))
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
