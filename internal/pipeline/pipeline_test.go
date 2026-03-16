package pipeline

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/aitestmanagement/gtms-cli/internal/task"
)

func TestBuildAutomationRecord(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "test-automation", "records"), 0755))

	tf := &task.TaskFile{
		ID:        "task-abc1234",
		Type:      "automate",
		Target:    "tc-007",
		Adapter:   "local-claude",
		Status:    "complete",
		Created:   "2025-02-12T11:00:00Z",
		Branch:    "feature/automate-tc-007",
		Framework: "playwright",
	}

	rc := &result.ResultContract{
		Task:      "task-abc1234",
		Command:   "automate",
		Target:    "tc-007",
		Adapter:   "local-claude",
		Mode:      "sync",
		Status:    "complete",
		Artefact:  "test-automation/specs/tc-007-checkout-guest.spec.ts",
		Attempts:  2,
		Summary:   "Spec file generated after 2 attempts",
		Log:       ".gtms/logs/task-abc1234/",
		Completed: "2025-02-12T11:05:00Z",
	}

	err := BuildAutomationRecord(root, tf, rc)
	require.NoError(t, err)

	// Read back the record
	recordPath := filepath.Join(root, "test-automation", "records", "tc-007.automation.md")
	record, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)

	assert.Equal(t, "tc-007", record.TestCase)
	assert.Equal(t, "playwright", record.Framework)
	assert.Equal(t, "developed", record.Status)
	assert.Equal(t, "test-automation/specs/tc-007-checkout-guest.spec.ts", record.Artefact)
	assert.Equal(t, "feature/automate-tc-007", record.Branch)
	assert.Equal(t, "local-claude", record.Adapter)
	assert.Equal(t, "pass", record.LastDevResult)
	assert.Equal(t, 2, record.Attempts)
	assert.Equal(t, "Spec file generated after 2 attempts", record.Summary)
	assert.Equal(t, 1, record.Cycle)
}

func TestBuildAutomationRecord_CycleIncrement(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "test-automation", "records"), 0755))

	tf := &task.TaskFile{
		ID:        "task-first",
		Type:      "automate",
		Target:    "tc-007",
		Adapter:   "local-claude",
		Framework: "playwright",
	}

	rc := &result.ResultContract{
		Task:     "task-first",
		Command:  "automate",
		Target:   "tc-007",
		Adapter:  "local-claude",
		Status:   "complete",
		Artefact: "test-automation/specs/tc-007.spec.ts",
		Attempts: 1,
	}

	// First build - cycle 1
	err := BuildAutomationRecord(root, tf, rc)
	require.NoError(t, err)

	// Second build - cycle should increment to 2
	tf.ID = "task-second"
	rc.Task = "task-second"
	err = BuildAutomationRecord(root, tf, rc)
	require.NoError(t, err)

	recordPath := filepath.Join(root, "test-automation", "records", "tc-007.automation.md")
	record, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)
	assert.Equal(t, 2, record.Cycle)
}

func TestBuildAutomationRecord_ErrorStatus(t *testing.T) {
	root := t.TempDir()

	tf := &task.TaskFile{
		ID:     "task-err",
		Type:   "automate",
		Target: "tc-008",
	}

	rc := &result.ResultContract{
		Task:    "task-err",
		Command: "automate",
		Target:  "tc-008",
		Status:  "error",
		Summary: "Generation failed",
	}

	err := BuildAutomationRecord(root, tf, rc)
	require.NoError(t, err)

	recordPath := filepath.Join(root, "test-automation", "records", "tc-008.automation.md")
	record, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)
	assert.Equal(t, "fail", record.LastDevResult)
}

func TestUpdateExecutionResult(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "test-automation", "records"), 0755))

	// First create an automation record
	record := &AutomationRecord{
		TestCase:      "tc-007",
		Framework:     "playwright",
		Status:        "developed",
		Artefact:      "test-automation/specs/tc-007.spec.ts",
		Branch:        "feature/automate-tc-007",
		Adapter:       "local-claude",
		LastDevResult: "pass",
		Cycle:         1,
	}

	recordPath := filepath.Join(root, "test-automation", "records", "tc-007.automation.md")
	require.NoError(t, WriteAutomationRecord(recordPath, record))

	// Now update with execution result
	tf := &task.TaskFile{
		ID:      "task-exec123",
		Type:    "execute",
		Target:  "tc-007",
		Adapter: "local-runner",
	}

	rc := &result.ResultContract{
		Task:     "task-exec123",
		Command:  "execute",
		Target:   "tc-007",
		Adapter:  "local-runner",
		Status:   "complete",
		Artefact: "results/junit/tc-007.xml",
	}

	err := UpdateExecutionResult(root, tf, rc)
	require.NoError(t, err)

	// Read back and verify
	updated, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)
	assert.Equal(t, "pass", updated.LastFormalResult)
	assert.Equal(t, "results/junit/tc-007.xml", updated.LastFormalRun)
	// Original fields should be preserved
	assert.Equal(t, "tc-007", updated.TestCase)
	assert.Equal(t, "playwright", updated.Framework)
	assert.Equal(t, "developed", updated.Status)
	assert.Equal(t, 1, updated.Cycle)
}

func TestFindAutomationRecord(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "test-automation", "records"), 0755))

	// No record yet
	record, path, err := FindAutomationRecord(root, "tc-007")
	require.NoError(t, err)
	assert.Nil(t, record)
	assert.Empty(t, path)

	// Create a record
	rec := &AutomationRecord{
		TestCase: "tc-007",
		Status:   "developed",
		Artefact: "test-automation/specs/tc-007.spec.ts",
		Cycle:    1,
	}
	recordPath := filepath.Join(root, "test-automation", "records", "tc-007.automation.md")
	require.NoError(t, WriteAutomationRecord(recordPath, rec))

	// Now find it
	record, path, err = FindAutomationRecord(root, "tc-007")
	require.NoError(t, err)
	require.NotNil(t, record)
	assert.Equal(t, "tc-007", record.TestCase)
	assert.Equal(t, "developed", record.Status)
	assert.NotEmpty(t, path)
}

func TestBuildAutomationRecord_NilInputs(t *testing.T) {
	err := BuildAutomationRecord("/tmp", nil, &result.ResultContract{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "task file is required")

	err = BuildAutomationRecord("/tmp", &task.TaskFile{}, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "result contract is required")
}

func TestUpdateExecutionResult_NilInputs(t *testing.T) {
	err := UpdateExecutionResult("/tmp", nil, &result.ResultContract{})
	assert.Error(t, err)

	err = UpdateExecutionResult("/tmp", &task.TaskFile{}, nil)
	assert.Error(t, err)
}
