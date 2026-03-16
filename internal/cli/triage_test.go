package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/aitestmanagement/gtms-cli/internal/reader"
)

func TestWriteTriageJSON(t *testing.T) {
	result := &reader.TriageResult{
		TestCaseID: "tc-aaa1111",
		Category:   "automation-wrong",
		Summary:    "Selectors changed after redesign",
		Actions:    []string{"Automation record: status -> rework, cycle 2", "New task created: task-abc1234-automate-tc-aaa1111"},
		NewTaskID:  "task-abc1234",
	}

	var buf bytes.Buffer
	err := writeTriageJSON(&buf, result)
	require.NoError(t, err)

	out := buf.String()

	// Should be valid JSON
	var roundtrip reader.TriageResult
	err = json.Unmarshal([]byte(out), &roundtrip)
	require.NoError(t, err, "Output should be valid JSON")

	assert.Equal(t, "tc-aaa1111", roundtrip.TestCaseID)
	assert.Equal(t, "automation-wrong", roundtrip.Category)
	assert.Equal(t, "Selectors changed after redesign", roundtrip.Summary)
	assert.Equal(t, "task-abc1234", roundtrip.NewTaskID)
	assert.Len(t, roundtrip.Actions, 2)

	// No human-readable decorations
	assert.NotContains(t, out, "Triage recorded")
}

func TestWriteTriageJSON_EmptyActions(t *testing.T) {
	result := &reader.TriageResult{
		TestCaseID: "tc-bbb2222",
		Category:   "app-wrong",
		Summary:    "Server 500",
		Defect:     "JIRA-789",
	}

	var buf bytes.Buffer
	err := writeTriageJSON(&buf, result)
	require.NoError(t, err)

	out := buf.String()

	// Actions should be [] not null
	assert.Contains(t, out, `"actions": []`)

	// Defect should be present
	assert.Contains(t, out, `"defect": "JIRA-789"`)
}

func TestWriteTriageJSON_OmitsEmptyOptionalFields(t *testing.T) {
	result := &reader.TriageResult{
		TestCaseID: "tc-ccc3333",
		Category:   "test-wrong",
		Summary:    "Expected changed",
		Actions:    []string{"Test case updated"},
	}

	var buf bytes.Buffer
	err := writeTriageJSON(&buf, result)
	require.NoError(t, err)

	out := buf.String()

	// Defect and NewTaskID should be omitted (omitempty)
	assert.NotContains(t, out, `"defect"`)
	assert.NotContains(t, out, `"new_task_id"`)
}

// setupTriageFixture creates a fixture with the files RecordTriage needs.
func setupTriageFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	// Test case
	writeTestFile(t, root, filepath.Join("test-cases", "tc-aaa1111-login-happy.md"), `---
test_case_id: tc-aaa1111
title: Login Happy Path
requirement: REQ-A
---
`)

	// Automation record with fail result (required for triage)
	writeTestFile(t, root, filepath.Join("test-automation", "records", "tc-aaa1111.automation.md"), `---
testcase: tc-aaa1111
framework: playwright
status: accepted
last-formal-result: fail
attempts: 1
cycle: 1
---
`)

	// test-tasks/pending/ must exist for task creation
	writeTestFile(t, root, filepath.Join("test-tasks", "pending", ".gitkeep"), "")

	return root
}

func TestTriageCommand_JSON_AutomationWrong(t *testing.T) {
	root := setupTriageFixture(t)
	var buf bytes.Buffer

	// Call RecordTriage directly then write JSON (same as the CLI path)
	result, err := reader.RecordTriage(root, "tc-aaa1111", "automation-wrong", "Selectors broke", "")
	require.NoError(t, err)

	err = writeTriageJSON(&buf, result)
	require.NoError(t, err)

	out := buf.String()

	// Should be valid JSON
	var roundtrip reader.TriageResult
	err = json.Unmarshal([]byte(out), &roundtrip)
	require.NoError(t, err, "Output should be valid JSON")

	assert.Equal(t, "tc-aaa1111", roundtrip.TestCaseID)
	assert.Equal(t, "automation-wrong", roundtrip.Category)
	assert.NotEmpty(t, roundtrip.Actions)
	assert.NotEmpty(t, roundtrip.NewTaskID)

	// No human-readable decorations
	assert.NotContains(t, out, "Triage recorded")
	assert.NotContains(t, out, "Run 'gtms automate status'")
}
