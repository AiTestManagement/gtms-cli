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
	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-aaa1111-login-happy.md"), `---
test_case_id: tc-aaa1111
title: Login Happy Path
requirement: REQ-A
---
`)

	// CON-023 / ENH-145: wiring record + terminal handoff with result: fail.
	writeTestFile(t, root, filepath.Join("gtms/automation", "wiring", "tc-aaa1111--playwright.wiring.yaml"),
		"testcase: tc-aaa1111\ntestcase-hash: 0011223344556677\nframework: playwright\nadapter: playwright-runner\nartefact: test/sample.spec.ts\nartefact-hash: aabbccddeeff0011\n")
	writeTestFile(t, root, filepath.Join(".gtms", "results", "task-aaa1111.handoff.yaml"),
		"task: task-aaa1111\ncommand: execute\ntarget: tc-aaa1111\nadapter: playwright-runner\nmode: sync\ncreated: \"2026-05-19T10:00:00Z\"\nstatus: complete\nresult: fail\nframework: playwright\ncompleted: \"2026-05-19T10:01:00Z\"\n")

	// gtms/tasks/pending/ must exist for task creation
	writeTestFile(t, root, filepath.Join("gtms/tasks", "pending", ".gitkeep"), "")

	return root
}

func TestTriageCommand_JSON_AutomationWrong(t *testing.T) {
	root := setupTriageFixture(t)
	var buf bytes.Buffer

	// Call RecordTriage directly then write JSON (same as the CLI path)
	result, err := reader.RecordTriage(root, "tc-aaa1111", "automation-wrong", "Selectors broke", "", "")
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
