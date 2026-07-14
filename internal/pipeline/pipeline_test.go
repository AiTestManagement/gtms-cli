package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/aitestmanagement/gtms-cli/internal/execution"
	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/aitestmanagement/gtms-cli/internal/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildAutomationRecord(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation", "records"), 0755))

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
		Result:  "pass",
		Artefact:  "gtms/automation/specs/tc-007-checkout-guest.spec.ts",
		Attempts:  2,
		Summary:   "Spec file generated after 2 attempts",
		Log:       ".gtms/logs/task-abc1234/",
		Completed: "2025-02-12T11:05:00Z",
	}

	err := BuildAutomationRecord(root, tf, rc)
	require.NoError(t, err)

	// Read back the record — now at framework-qualified path
	recordPath := filepath.Join(root, "gtms/automation", "records", "tc-007--playwright.automation.md")
	record, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)

	assert.Equal(t, "tc-007", record.TestCase)
	assert.Equal(t, "playwright", record.Framework)
	assert.Equal(t, "developed", record.Status)
	assert.Equal(t, "gtms/automation/specs/tc-007-checkout-guest.spec.ts", record.Artefact)
	assert.Equal(t, "feature/automate-tc-007", record.Branch)
	assert.Equal(t, "local-claude", record.Adapter)
	assert.Equal(t, "pass", record.LastDevResult)
	assert.Equal(t, 2, record.Attempts)
	assert.Equal(t, "Spec file generated after 2 attempts", record.Summary)
	assert.Equal(t, 1, record.Cycle)
}

func TestBuildAutomationRecord_CycleIncrement(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation", "records"), 0755))

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
		Result:  "pass",
		Artefact: "gtms/automation/specs/tc-007.spec.ts",
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

	recordPath := filepath.Join(root, "gtms/automation", "records", "tc-007--playwright.automation.md")
	record, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)
	assert.Equal(t, 2, record.Cycle)
}

func TestBuildAutomationRecord_CorruptedExistingRecordErrors(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation", "records"), 0755))

	recordPath := filepath.Join(root, "gtms/automation", "records", "tc-007--playwright.automation.md")
	original := []byte("---\ntestcase: tc-007\nframework: playwright\ncycle: [not-a-number]\n---\n")
	require.NoError(t, os.WriteFile(recordPath, original, 0644))

	tf := &task.TaskFile{
		ID:        "task-corrupt",
		Type:      "automate",
		Target:    "tc-007",
		Adapter:   "local-claude",
		Framework: "playwright",
	}

	rc := &result.ResultContract{
		Task:     "task-corrupt",
		Command:  "automate",
		Target:   "tc-007",
		Status:   "complete",
		Result:  "pass",
		Artefact: "gtms/automation/specs/tc-007.spec.ts",
		Attempts: 1,
	}

	err := BuildAutomationRecord(root, tf, rc)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unreadable")
	assert.Contains(t, err.Error(), recordPath)

	after, readErr := os.ReadFile(recordPath)
	require.NoError(t, readErr)
	assert.Equal(t, original, after)
}

func TestBuildAutomationRecord_ErrorStatus(t *testing.T) {
	root := t.TempDir()

	tf := &task.TaskFile{
		ID:        "task-err",
		Type:      "automate",
		Target:    "tc-008",
		Framework: "mock",
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

	recordPath := filepath.Join(root, "gtms/automation", "records", "tc-008--mock.automation.md")
	record, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)
	assert.Equal(t, "error", record.LastDevResult)
}

func TestBuildAutomationRecord_ExplicitFail(t *testing.T) {
	root := t.TempDir()

	tf := &task.TaskFile{
		ID:        "task-fail",
		Type:      "automate",
		Target:    "tc-008",
		Framework: "mock",
	}

	rc := &result.ResultContract{
		Task:    "task-fail",
		Command: "automate",
		Target:  "tc-008",
		Status:  "complete",
		Result:  "fail",
		Summary: "Tests ran but failed",
	}

	err := BuildAutomationRecord(root, tf, rc)
	require.NoError(t, err)

	recordPath := filepath.Join(root, "gtms/automation", "records", "tc-008--mock.automation.md")
	record, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)
	assert.Equal(t, "fail", record.LastDevResult)
}

func TestUpdateExecutionResult(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation", "records"), 0755))

	// First create an automation record at framework-qualified path
	record := &AutomationRecord{
		RecordCommon: RecordCommon{
			TestCase:  "tc-007",
			Framework: "playwright",
			Status:    "developed",
			Branch:    "feature/automate-tc-007",
		},
		Artefact:      "gtms/automation/specs/tc-007.spec.ts",
		Adapter:       "local-claude",
		LastDevResult: "pass",
		Cycle:         1,
	}

	recordPath := filepath.Join(root, "gtms/automation", "records", "tc-007--playwright.automation.md")
	require.NoError(t, WriteAutomationRecord(recordPath, record))

	// Now update with execution result — tf must carry framework
	tf := &task.TaskFile{
		ID:        "task-exec123",
		Type:      "execute",
		Target:    "tc-007",
		Adapter:   "local-runner",
		Framework: "playwright",
	}

	rc := &result.ResultContract{
		Task:      "task-exec123",
		Command:   "execute",
		Target:    "tc-007",
		Adapter:   "local-runner",
		Status:    "complete",
		Result:  "pass",
		Artefact:  "results/junit/tc-007.xml",
		Completed: "2026-04-16T14:32:11Z",
	}

	err := UpdateExecutionResult(root, tf, rc)
	require.NoError(t, err)

	// Read back and verify
	updated, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)
	assert.Equal(t, "pass", updated.Result)
	assert.Equal(t, "results/junit/tc-007.xml", updated.ExecutedArtefact)
	assert.Equal(t, "2026-04-16T14:32:11Z", updated.ExecutedAt)
	// Original fields should be preserved
	assert.Equal(t, "tc-007", updated.TestCase)
	assert.Equal(t, "playwright", updated.Framework)
	assert.Equal(t, "developed", updated.Status)
	assert.Equal(t, 1, updated.Cycle)
}

// TestUpdateExecutionResult_NoCompletedFallsBackToNow verifies that when a
// pathological adapter does not set `completed:` on the result contract,
// UpdateExecutionResult still writes a valid RFC3339 timestamp to
// last-formal-run-at (falling back to time.Now).
func TestUpdateExecutionResult_NoCompletedFallsBackToNow(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation", "records"), 0755))

	record := &AutomationRecord{
		RecordCommon: RecordCommon{
			TestCase:  "tc-010",
			Framework: "bats",
			Status:    "developed",
		},
		Adapter: "bats-runner",
		Cycle:   1,
	}
	recordPath := filepath.Join(root, "gtms/automation", "records", "tc-010--bats.automation.md")
	require.NoError(t, WriteAutomationRecord(recordPath, record))

	tf := &task.TaskFile{
		ID:        "task-nocompleted",
		Type:      "execute",
		Target:    "tc-010",
		Framework: "bats",
	}
	rc := &result.ResultContract{
		Task:    "task-nocompleted",
		Command: "execute",
		Target:  "tc-010",
		Status:  "complete",
		Result:  "pass",
		// Completed deliberately empty — simulating a misbehaving Tier 2 script.
	}

	require.NoError(t, UpdateExecutionResult(root, tf, rc))

	updated, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)
	require.NotEmpty(t, updated.ExecutedAt, "fallback timestamp should be set when rc.Completed is empty")
	// Must be parseable as RFC3339.
	_, perr := time.Parse(time.RFC3339, updated.ExecutedAt)
	assert.NoError(t, perr, "last-formal-run-at must be RFC3339: %s", updated.ExecutedAt)
}

func TestUpdateExecutionResult_ErrorStatus(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation", "records"), 0755))

	// Create an existing automation record
	record := &AutomationRecord{
		RecordCommon: RecordCommon{
			TestCase:  "tc-009",
			Framework: "bats",
			Status:    "developed",
			Branch:    "feature/automate-tc-009",
		},
		Artefact:      "gtms/automation/specs/tc-009.bats",
		Adapter:       "local-claude",
		LastDevResult: "pass",
		Cycle:         1,
	}

	recordPath := filepath.Join(root, "gtms/automation", "records", "tc-009--bats.automation.md")
	require.NoError(t, WriteAutomationRecord(recordPath, record))

	// Update with an error execution result (non-zero exit code -> status "error")
	tf := &task.TaskFile{
		ID:        "task-fail123",
		Type:      "execute",
		Target:    "tc-009",
		Adapter:   "bats-runner",
		Framework: "bats",
	}

	rc := &result.ResultContract{
		Task:    "task-fail123",
		Command: "execute",
		Target:  "tc-009",
		Adapter: "bats-runner",
		Status:  "error",
		Summary: "Process exited with code 1",
	}

	err := UpdateExecutionResult(root, tf, rc)
	require.NoError(t, err)

	// Read back and verify last-formal-result is "error" (ENH-040: was "fail" before)
	updated, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)
	assert.Equal(t, "error", updated.Result)
	// Original fields should be preserved
	assert.Equal(t, "tc-009", updated.TestCase)
	assert.Equal(t, "bats", updated.Framework)
	assert.Equal(t, "developed", updated.Status)
	assert.Equal(t, 1, updated.Cycle)
}

func TestUpdateExecutionResult_ExplicitFail(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation", "records"), 0755))

	// Create an existing automation record
	record := &AutomationRecord{
		RecordCommon: RecordCommon{
			TestCase:  "tc-009",
			Framework: "bats",
			Status:    "developed",
		},
		Artefact:      "gtms/automation/specs/tc-009.bats",
		Adapter:       "local-claude",
		LastDevResult: "pass",
		Cycle:         1,
	}

	recordPath := filepath.Join(root, "gtms/automation", "records", "tc-009--bats.automation.md")
	require.NoError(t, WriteAutomationRecord(recordPath, record))

	// Update with explicit fail (Tier 2 adapter wrote status: "fail" to contract)
	tf := &task.TaskFile{
		ID:        "task-fail456",
		Type:      "execute",
		Target:    "tc-009",
		Adapter:   "bats-runner",
		Framework: "bats",
	}

	rc := &result.ResultContract{
		Task:    "task-fail456",
		Command: "execute",
		Target:  "tc-009",
		Adapter: "bats-runner",
		Status:  "complete",
		Result:  "fail",
		Summary: "3 of 10 tests failed",
	}

	err := UpdateExecutionResult(root, tf, rc)
	require.NoError(t, err)

	// Read back and verify last-formal-result is "fail"
	updated, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)
	assert.Equal(t, "fail", updated.Result)
}

func TestUpdateExecutionResult_PassAfterError(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation", "records"), 0755))

	// Create an existing automation record
	record := &AutomationRecord{
		RecordCommon: RecordCommon{
			TestCase:  "tc-010",
			Framework: "bats",
			Status:    "developed",
		},
		Artefact:      "gtms/automation/specs/tc-010.bats",
		Adapter:       "bats-runner",
		LastDevResult: "pass",
		Cycle:         1,
	}

	recordPath := filepath.Join(root, "gtms/automation", "records", "tc-010--bats.automation.md")
	require.NoError(t, WriteAutomationRecord(recordPath, record))

	// First: error execution (non-zero exit code -> rc.Status "error")
	tf := &task.TaskFile{
		ID:        "task-fail456",
		Type:      "execute",
		Target:    "tc-010",
		Adapter:   "bats-runner",
		Framework: "bats",
	}
	rc := &result.ResultContract{
		Task:    "task-fail456",
		Command: "execute",
		Target:  "tc-010",
		Adapter: "bats-runner",
		Status:  "error",
		Summary: "Process exited with code 1",
	}

	err := UpdateExecutionResult(root, tf, rc)
	require.NoError(t, err)

	updated, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)
	assert.Equal(t, "error", updated.Result)

	// Second: successful execution
	tf.ID = "task-pass789"
	rc.Task = "task-pass789"
	rc.Status = "complete"
	rc.Result = "pass" // ENH-130: complete requires result
	rc.Summary = "All tests passed"
	rc.Artefact = "results/tc-010.xml"

	err = UpdateExecutionResult(root, tf, rc)
	require.NoError(t, err)

	updated, err = ReadAutomationRecord(recordPath)
	require.NoError(t, err)
	assert.Equal(t, "pass", updated.Result)
	assert.Equal(t, "results/tc-010.xml", updated.ExecutedArtefact)
}

func TestUpdateExecutionResult_ArtefactHash(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation", "records"), 0755))

	record := &AutomationRecord{
		RecordCommon: RecordCommon{
			TestCase:  "tc-029",
			Framework: "bats",
			Status:    "developed",
		},
		Artefact:     "gtms/automation/specs/tc-029.bats",
		Adapter:      "bats-runner",
		ArtefactHash: "oldhash000000000",
		Cycle:        1,
	}

	recordPath := filepath.Join(root, "gtms/automation", "records", "tc-029--bats.automation.md")
	require.NoError(t, WriteAutomationRecord(recordPath, record))

	tf := &task.TaskFile{
		ID:        "task-hash001",
		Type:      "execute",
		Target:    "tc-029",
		Adapter:   "bats-runner",
		Framework: "bats",
	}
	rc := &result.ResultContract{
		Task:         "task-hash001",
		Command:      "execute",
		Target:       "tc-029",
		Adapter:      "bats-runner",
		Status:       "complete",
		Result:  "pass",
		ArtefactHash: "newhash123456789",
	}

	err := UpdateExecutionResult(root, tf, rc)
	require.NoError(t, err)

	updated, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)
	assert.Equal(t, "newhash123456789", updated.ArtefactHash)
	assert.Equal(t, "pass", updated.Result)
}

func TestUpdateExecutionResult_EmptyHashPreservesExisting(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation", "records"), 0755))

	record := &AutomationRecord{
		RecordCommon: RecordCommon{
			TestCase:  "tc-030",
			Framework: "bats",
			Status:    "developed",
		},
		Artefact:     "gtms/automation/specs/tc-030.bats",
		Adapter:      "bats-runner",
		ArtefactHash: "oldhash000000000",
		Cycle:        1,
	}

	recordPath := filepath.Join(root, "gtms/automation", "records", "tc-030--bats.automation.md")
	require.NoError(t, WriteAutomationRecord(recordPath, record))

	tf := &task.TaskFile{
		ID:        "task-hash002",
		Type:      "execute",
		Target:    "tc-030",
		Adapter:   "bats-runner",
		Framework: "bats",
	}
	rc := &result.ResultContract{
		Task:         "task-hash002",
		Command:      "execute",
		Target:       "tc-030",
		Adapter:      "bats-runner",
		Status:       "complete",
		Result:  "pass",
		ArtefactHash: "", // empty — should NOT clear existing hash
	}

	err := UpdateExecutionResult(root, tf, rc)
	require.NoError(t, err)

	updated, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)
	assert.Equal(t, "oldhash000000000", updated.ArtefactHash, "existing hash should be preserved when contract has empty hash")
	assert.Equal(t, "pass", updated.Result)
}

// --- BUG-070: summary propagation from execute to automation record ---

func TestUpdateExecutionResult_SummaryOverwrite(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation", "records"), 0755))

	// Seed a record with a stale automate-time summary
	record := &AutomationRecord{
		RecordCommon: RecordCommon{
			TestCase:  "tc-bug070a",
			Framework: "bats",
			Status:    "developed",
		},
		Artefact: "test/acceptance/tc-bug070a.bats",
		Adapter:  "bats-runner",
		Summary:  "old automate summary",
		Cycle:    1,
	}
	recordPath := filepath.Join(root, "gtms/automation", "records", "tc-bug070a--bats.automation.md")
	require.NoError(t, WriteAutomationRecord(recordPath, record))

	tf := &task.TaskFile{
		ID:        "task-bug070a1",
		Type:      "execute",
		Target:    "tc-bug070a",
		Adapter:   "bats-runner",
		Framework: "bats",
	}
	rc := &result.ResultContract{
		Task:      "task-bug070a1",
		Command:   "execute",
		Target:    "tc-bug070a",
		Adapter:   "bats-runner",
		Status:    "complete",
		Result:  "pass",
		Summary:   "2 passed, 1 skipped",
		Completed: "2026-05-06T10:00:00Z",
	}

	err := UpdateExecutionResult(root, tf, rc)
	require.NoError(t, err)

	updated, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)
	assert.Equal(t, "2 passed, 1 skipped", updated.Summary,
		"BUG-070: execute must overwrite stale automate-time summary with runtime summary")
}

func TestUpdateExecutionResult_EmptySummaryClearsStale(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation", "records"), 0755))

	// Seed a record with a stale automate-time summary
	record := &AutomationRecord{
		RecordCommon: RecordCommon{
			TestCase:  "tc-bug070b",
			Framework: "bats",
			Status:    "developed",
		},
		Artefact: "test/acceptance/tc-bug070b.bats",
		Adapter:  "bats-runner",
		Summary:  "old automate summary",
		Cycle:    1,
	}
	recordPath := filepath.Join(root, "gtms/automation", "records", "tc-bug070b--bats.automation.md")
	require.NoError(t, WriteAutomationRecord(recordPath, record))

	tf := &task.TaskFile{
		ID:        "task-bug070b1",
		Type:      "execute",
		Target:    "tc-bug070b",
		Adapter:   "bats-runner",
		Framework: "bats",
	}
	rc := &result.ResultContract{
		Task:      "task-bug070b1",
		Command:   "execute",
		Target:    "tc-bug070b",
		Adapter:   "bats-runner",
		Status:    "complete",
		Result:  "pass",
		Summary:   "", // empty — must clear stale summary
		Completed: "2026-05-06T10:00:00Z",
	}

	err := UpdateExecutionResult(root, tf, rc)
	require.NoError(t, err)

	updated, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)
	assert.Empty(t, updated.Summary,
		"BUG-070: empty rc.Summary must clear stale automate-time summary, not preserve it")
}

// --- BUG-075: summary size cap ---

func TestUpdateExecutionResult_TruncatesOversizeSummary(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation", "records"), 0755))

	record := &AutomationRecord{
		RecordCommon: RecordCommon{
			TestCase:  "tc-bug075a",
			Framework: "bats",
			Status:    "developed",
		},
		Artefact: "test/acceptance/tc-bug075a.bats",
		Adapter:  "bats-runner",
		Cycle:    1,
	}
	recordPath := filepath.Join(root, "gtms/automation", "records", "tc-bug075a--bats.automation.md")
	require.NoError(t, WriteAutomationRecord(recordPath, record))

	bigSummary := strings.Repeat("X", 200*1024)
	tf := &task.TaskFile{
		ID:        "task-bug075a1",
		Type:      "execute",
		Target:    "tc-bug075a",
		Adapter:   "bats-runner",
		Framework: "bats",
	}
	rc := &result.ResultContract{
		Task:      "task-bug075a1",
		Command:   "execute",
		Target:    "tc-bug075a",
		Adapter:   "bats-runner",
		Status:    "complete",
		Result:    "fail",
		Summary:   bigSummary,
		Completed: "2026-05-07T10:00:00Z",
	}

	err := UpdateExecutionResult(root, tf, rc)
	require.NoError(t, err)

	updated, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)

	marker := " … (truncated; see notes:)"
	assert.LessOrEqual(t, len(updated.Summary), summarySizeCapBytes+len(marker),
		"BUG-075: truncated summary must not exceed cap + marker")
	assert.Contains(t, updated.Summary, marker,
		"BUG-075: truncated summary must contain the truncation marker")
	assert.True(t, utf8.ValidString(updated.Summary),
		"BUG-075: truncated summary must be valid UTF-8")
}

func TestUpdateExecutionResult_ShortSummaryUnchanged(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation", "records"), 0755))

	record := &AutomationRecord{
		RecordCommon: RecordCommon{
			TestCase:  "tc-bug075b",
			Framework: "bats",
			Status:    "developed",
		},
		Artefact: "test/acceptance/tc-bug075b.bats",
		Adapter:  "bats-runner",
		Cycle:    1,
	}
	recordPath := filepath.Join(root, "gtms/automation", "records", "tc-bug075b--bats.automation.md")
	require.NoError(t, WriteAutomationRecord(recordPath, record))

	shortSummary := "3 tests passed"
	tf := &task.TaskFile{
		ID:        "task-bug075b1",
		Type:      "execute",
		Target:    "tc-bug075b",
		Adapter:   "bats-runner",
		Framework: "bats",
	}
	rc := &result.ResultContract{
		Task:      "task-bug075b1",
		Command:   "execute",
		Target:    "tc-bug075b",
		Adapter:   "bats-runner",
		Status:    "complete",
		Result:  "pass",
		Summary:   shortSummary,
		Completed: "2026-05-07T10:00:00Z",
	}

	err := UpdateExecutionResult(root, tf, rc)
	require.NoError(t, err)

	updated, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)
	assert.Equal(t, shortSummary, updated.Summary,
		"BUG-075: short summary must not be truncated")
}

func TestFindAutomationRecord(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation", "records"), 0755))

	// No record yet
	record, path, err := FindAutomationRecord(root, "tc-007", "bats")
	require.NoError(t, err)
	assert.Nil(t, record)
	assert.Empty(t, path)

	// Create a record at framework-qualified path
	rec := &AutomationRecord{
		RecordCommon: RecordCommon{
			TestCase:  "tc-007",
			Framework: "bats",
			Status:    "developed",
		},
		Artefact: "gtms/automation/specs/tc-007.spec.ts",
		Cycle:    1,
	}
	recordPath := filepath.Join(root, "gtms/automation", "records", "tc-007--bats.automation.md")
	require.NoError(t, WriteAutomationRecord(recordPath, rec))

	// Now find it
	record, path, err = FindAutomationRecord(root, "tc-007", "bats")
	require.NoError(t, err)
	require.NotNil(t, record)
	assert.Equal(t, "tc-007", record.TestCase)
	assert.Equal(t, "developed", record.Status)
	assert.NotEmpty(t, path)
}

func TestFindAutomationRecord_WrongFramework(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation", "records"), 0755))

	// Create a bats record
	rec := &AutomationRecord{
		RecordCommon: RecordCommon{
			TestCase:  "tc-007",
			Framework: "bats",
			Status:    "developed",
		},
		Cycle: 1,
	}
	recordPath := filepath.Join(root, "gtms/automation", "records", "tc-007--bats.automation.md")
	require.NoError(t, WriteAutomationRecord(recordPath, rec))

	// Looking for pester should return nil
	record, path, err := FindAutomationRecord(root, "tc-007", "pester")
	require.NoError(t, err)
	assert.Nil(t, record)
	assert.Empty(t, path)
}

func TestBuildAutomationRecord_TwoFrameworks(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation", "records"), 0755))

	rc := &result.ResultContract{
		Task:     "task-1",
		Command:  "automate",
		Target:   "tc-007",
		Adapter:  "claude-bats",
		Status:   "complete",
		Result:  "pass",
		Artefact: "specs/tc-007.bats",
		Attempts: 1,
	}

	// Build bats record
	tfBats := &task.TaskFile{
		ID: "task-1", Type: "automate", Target: "tc-007",
		Adapter: "claude-bats", Framework: "bats",
	}
	require.NoError(t, BuildAutomationRecord(root, tfBats, rc))

	// Build pester record
	tfPester := &task.TaskFile{
		ID: "task-2", Type: "automate", Target: "tc-007",
		Adapter: "claude-pester", Framework: "pester",
	}
	rc.Task = "task-2"
	rc.Adapter = "claude-pester"
	rc.Artefact = "specs/tc-007.tests.ps1"
	require.NoError(t, BuildAutomationRecord(root, tfPester, rc))

	// Both records should exist independently
	batsPath := filepath.Join(root, "gtms/automation", "records", "tc-007--bats.automation.md")
	pesterPath := filepath.Join(root, "gtms/automation", "records", "tc-007--pester.automation.md")

	batsRec, err := ReadAutomationRecord(batsPath)
	require.NoError(t, err)
	assert.Equal(t, "bats", batsRec.Framework)
	assert.Equal(t, "specs/tc-007.bats", batsRec.Artefact)

	pesterRec, err := ReadAutomationRecord(pesterPath)
	require.NoError(t, err)
	assert.Equal(t, "pester", pesterRec.Framework)
	assert.Equal(t, "specs/tc-007.tests.ps1", pesterRec.Artefact)
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

// --- BUG-036: Pipeline integration with log containing YAML separator ---

func TestUpdateExecutionResult_LogWithYAMLSeparator(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation", "records"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".gtms", "results"), 0755))

	// Create an existing automation record
	record := &AutomationRecord{
		RecordCommon: RecordCommon{
			TestCase:  "tc-7b4f45c7",
			Framework: "pester",
			Status:    "developed",
		},
		Artefact:      "gtms/automation/specs/tc-7b4f45c7.tests.ps1",
		Adapter:       "remote-pester-lean",
		LastDevResult: "pass",
		Cycle:         1,
	}

	recordPath := filepath.Join(root, "gtms/automation", "records", "tc-7b4f45c7--pester.automation.md")
	require.NoError(t, WriteAutomationRecord(recordPath, record))

	// Write a result contract with --- in the log field (simulating Pester output)
	contractYAML := `task: task-pester001
command: execute
target: tc-7b4f45c7
adapter: remote-pester-lean
mode: sync
created: "2026-04-12T13:00:00Z"
status: complete
result: pass
summary: "All 5 tests passed"
log: |
  Pester v5.7.1
  ---
  Tests completed: 5 passed, 0 failed
completed: "2026-04-12T13:02:29Z"
`
	contractPath := filepath.Join(root, ".gtms", "results", "task-pester001.handoff.yaml")
	require.NoError(t, os.WriteFile(contractPath, []byte(contractYAML), 0644))

	// Read the contract (this is where BUG-036 would truncate)
	rc, err := result.Read(contractPath)
	require.NoError(t, err)
	require.Equal(t, "complete", rc.Status, "status must be parsed correctly despite --- in log")

	tf := &task.TaskFile{
		ID:        "task-pester001",
		Type:      "execute",
		Target:    "tc-7b4f45c7",
		Adapter:   "remote-pester-lean",
		Framework: "pester",
	}

	err = UpdateExecutionResult(root, tf, rc)
	require.NoError(t, err)

	// Verify: last-formal-result should be "pass" (not stuck on previous value)
	updated, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)
	assert.Equal(t, "pass", updated.Result, "BUG-036: last-formal-result must be 'pass' when contract has --- in log")
}

// --- ENH-077: diagnostic log field tests ---
//
// BUG-084: helper-only tests for truncateUTF8 / writeLogSpill moved to
// internal/result/spill_test.go when the helpers were lifted to the result
// package. End-to-end tests below continue to exercise the helpers through
// BuildAutomationRecord and UpdateExecutionResult via the logSizeCapBytes
// alias.

func TestUpdateExecutionResult_CopiesShortLogVerbatim(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation", "records"), 0755))

	record := &AutomationRecord{
		RecordCommon: RecordCommon{
			TestCase:  "tc-log001",
			Framework: "bats",
			Status:    "developed",
		},
		Artefact: "test/acceptance/tc-log001.bats",
		Adapter:  "bats-runner",
		Cycle:    1,
	}
	recordPath := filepath.Join(root, "gtms/automation", "records", "tc-log001--bats.automation.md")
	require.NoError(t, WriteAutomationRecord(recordPath, record))

	logBody := "not ok 1 - tc-log001\n# expected 1 got 0\n"
	rc := &result.ResultContract{
		Task:    "task-log001",
		Command: "execute",
		Target:  "tc-log001",
		Status:  "complete",
		Result:  "fail",
		Log:     logBody,
	}
	tf := &task.TaskFile{
		ID:        "task-log001",
		Type:      "execute",
		Target:    "tc-log001",
		Framework: "bats",
	}

	require.NoError(t, UpdateExecutionResult(root, tf, rc))

	updated, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)
	assert.Equal(t, logBody, updated.Notes)
	assert.Empty(t, updated.NotesSpill, "short log should not produce a spill path")

	// No spill file should have been created.
	_, statErr := os.Stat(filepath.Join(root, ".gtms", "logs", "task-log001.log"))
	assert.True(t, os.IsNotExist(statErr), "no spill file for short logs")
}

func TestUpdateExecutionResult_ClearsLogOnPass(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation", "records"), 0755))

	// Seed the record with stale fail-state values as if a previous run had
	// populated them.
	record := &AutomationRecord{
		RecordCommon: RecordCommon{
			TestCase:  "tc-log002",
			Framework: "bats",
			Status:    "developed",
			Result:    "fail",
			Notes:     "not ok 1 - broken\n",
		},
		Adapter:    "bats-runner",
		Cycle:      1,
		NotesSpill: ".gtms/logs/task-stale.log",
	}
	recordPath := filepath.Join(root, "gtms/automation", "records", "tc-log002--bats.automation.md")
	require.NoError(t, WriteAutomationRecord(recordPath, record))

	// A passing run overwrites with an empty log — ENH-077 requires the
	// renderer to hide stale failure output on green runs, so the writer
	// MUST clear both fields.
	rc := &result.ResultContract{
		Task:    "task-pass001",
		Command: "execute",
		Target:  "tc-log002",
		Status:  "complete",
		Result:  "pass",
		Log:     "",
	}
	tf := &task.TaskFile{
		ID:        "task-pass001",
		Type:      "execute",
		Target:    "tc-log002",
		Framework: "bats",
	}

	require.NoError(t, UpdateExecutionResult(root, tf, rc))

	updated, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)
	assert.Equal(t, "pass", updated.Result)
	assert.Empty(t, updated.Notes, "passing run must clear stale failure log")
	assert.Empty(t, updated.NotesSpill, "passing run must clear stale spill pointer")
}

func TestUpdateExecutionResult_TruncatesOversizeLogAndSpills(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation", "records"), 0755))

	record := &AutomationRecord{
		RecordCommon: RecordCommon{
			TestCase:  "tc-log003",
			Framework: "bats",
			Status:    "developed",
		},
		Adapter: "bats-runner",
		Cycle:   1,
	}
	recordPath := filepath.Join(root, "gtms/automation", "records", "tc-log003--bats.automation.md")
	require.NoError(t, WriteAutomationRecord(recordPath, record))

	// 128 KB of output — twice the cap. Use a line pattern so truncation
	// is easy to reason about.
	bigLog := strings.Repeat("verbose framework output line\n", 4500) // ~130 KB
	require.Greater(t, len(bigLog), logSizeCapBytes)

	rc := &result.ResultContract{
		Task:    "task-log003",
		Command: "execute",
		Target:  "tc-log003",
		Status:  "complete",
		Result:  "fail",
		Log:     bigLog,
	}
	tf := &task.TaskFile{
		ID:        "task-log003",
		Type:      "execute",
		Target:    "tc-log003",
		Framework: "bats",
	}

	require.NoError(t, UpdateExecutionResult(root, tf, rc))

	updated, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(updated.Notes), logSizeCapBytes,
		"truncated log must not exceed cap")
	assert.NotEmpty(t, updated.NotesSpill, "oversize log must produce a spill path")
	assert.Equal(t, ".gtms/logs/task-log003.log", updated.NotesSpill)

	// Full content must exist at the spill path.
	spillAbs := filepath.Join(root, ".gtms", "logs", "task-log003.log")
	spillData, readErr := os.ReadFile(spillAbs)
	require.NoError(t, readErr)
	assert.Equal(t, bigLog, string(spillData), "spill file must carry the full untruncated log")
}

// --- ENH-092: automate-path log truncation ---

func TestBuildAutomationRecord_TruncatesOversizeLogAndSpills(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation", "records"), 0755))

	// 128 KB of output — twice the cap.
	bigLog := strings.Repeat("chatty compiler output line\n", 4500)
	require.Greater(t, len(bigLog), logSizeCapBytes)

	tf := &task.TaskFile{
		ID:        "task-biglog01",
		Type:      "automate",
		Target:    "tc-automate01",
		Adapter:   "local-claude",
		Framework: "bats",
	}

	rc := &result.ResultContract{
		Task:     "task-biglog01",
		Command:  "automate",
		Target:   "tc-automate01",
		Adapter:  "local-claude",
		Status:   "complete",
		Result:  "pass",
		Artefact: "test/acceptance/tc-automate01.bats",
		Attempts: 1,
		Log:      bigLog,
	}

	err := BuildAutomationRecord(root, tf, rc)
	require.NoError(t, err)

	recordPath := filepath.Join(root, "gtms/automation", "records", "tc-automate01--bats.automation.md")
	record, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)

	assert.LessOrEqual(t, len(record.Notes), logSizeCapBytes,
		"automate-path truncated log must not exceed cap")
	assert.NotEmpty(t, record.NotesSpill, "oversize automate log must produce a spill path")
	assert.Equal(t, ".gtms/logs/task-biglog01.log", record.NotesSpill)

	// Full content must exist at the spill path.
	spillAbs := filepath.Join(root, ".gtms", "logs", "task-biglog01.log")
	spillData, readErr := os.ReadFile(spillAbs)
	require.NoError(t, readErr)
	assert.Equal(t, bigLog, string(spillData), "spill file must carry the full untruncated log")
}

func TestBuildAutomationRecord_ShortLogNoSpill(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation", "records"), 0755))

	shortLog := "All tests passed\n"

	tf := &task.TaskFile{
		ID:        "task-shortlog",
		Type:      "automate",
		Target:    "tc-automate02",
		Adapter:   "local-claude",
		Framework: "bats",
	}

	rc := &result.ResultContract{
		Task:     "task-shortlog",
		Command:  "automate",
		Target:   "tc-automate02",
		Adapter:  "local-claude",
		Status:   "complete",
		Result:  "pass",
		Artefact: "test/acceptance/tc-automate02.bats",
		Attempts: 1,
		Log:      shortLog,
	}

	err := BuildAutomationRecord(root, tf, rc)
	require.NoError(t, err)

	recordPath := filepath.Join(root, "gtms/automation", "records", "tc-automate02--bats.automation.md")
	record, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)

	assert.Equal(t, shortLog, record.Notes)
	assert.Empty(t, record.NotesSpill, "short log should not produce a spill path")

	// No spill file should be created
	_, statErr := os.Stat(filepath.Join(root, ".gtms", "logs", "task-shortlog.log"))
	assert.True(t, os.IsNotExist(statErr), "no spill file for short automate logs")
}

func TestUpdateExecutionResult_MultibyteBoundaryStaysValidUTF8(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation", "records"), 0755))

	record := &AutomationRecord{
		RecordCommon: RecordCommon{
			TestCase:  "tc-log004",
			Framework: "bats",
			Status:    "developed",
		},
		Adapter: "bats-runner",
		Cycle:   1,
	}
	recordPath := filepath.Join(root, "gtms/automation", "records", "tc-log004--bats.automation.md")
	require.NoError(t, WriteAutomationRecord(recordPath, record))

	// Pad with ASCII then append multibyte runes so the cap lands mid-rune.
	// Ensure the total exceeds logSizeCapBytes.
	padLen := logSizeCapBytes - 1
	log := strings.Repeat("a", padLen) + strings.Repeat("é", 100) // 2 bytes each
	require.Greater(t, len(log), logSizeCapBytes)

	rc := &result.ResultContract{
		Task:    "task-log004",
		Command: "execute",
		Target:  "tc-log004",
		Status:  "complete",
		Result:  "fail",
		Log:     log,
	}
	tf := &task.TaskFile{
		ID:        "task-log004",
		Type:      "execute",
		Target:    "tc-log004",
		Framework: "bats",
	}

	require.NoError(t, UpdateExecutionResult(root, tf, rc))

	updated, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)
	assert.True(t, utf8.ValidString(updated.Notes),
		"truncated log must remain valid UTF-8 even when cap lands mid-rune")
	assert.NotEmpty(t, updated.NotesSpill)
}

// --- ENH-094: skipped status in pipeline ---

func TestUpdateExecutionResult_SkippedStatus(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation", "records"), 0755))

	// Create an existing automation record
	record := &AutomationRecord{
		RecordCommon: RecordCommon{
			TestCase:  "tc-skip001",
			Framework: "bats",
			Status:    "developed",
		},
		Artefact: "gtms/automation/specs/tc-skip001.bats",
		Adapter:  "bats-runner",
		Cycle:    1,
	}
	recordPath := filepath.Join(root, "gtms/automation", "records", "tc-skip001--bats.automation.md")
	require.NoError(t, WriteAutomationRecord(recordPath, record))

	rc := &result.ResultContract{
		Task:    "task-skip001",
		Command: "execute",
		Target:  "tc-skip001",
		Status:  "complete",
		Result:  "skip",
	}
	tf := &task.TaskFile{
		ID:        "task-skip001",
		Type:      "execute",
		Target:    "tc-skip001",
		Framework: "bats",
	}

	require.NoError(t, UpdateExecutionResult(root, tf, rc))

	updated, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)
	assert.Equal(t, "skipped", updated.Result, "ENH-094: skipped contract status must map to last-formal-result: skipped")
}

func TestBuildAutomationRecord_SkippedStatus(t *testing.T) {
	root := t.TempDir()

	rc := &result.ResultContract{
		Task:    "task-skip002",
		Command: "automate",
		Target:  "tc-skip002",
		Status:  "complete",
		Result:  "skip",
	}
	tf := &task.TaskFile{
		ID:        "task-skip002",
		Type:      "automate",
		Target:    "tc-skip002",
		Framework: "bats",
	}

	require.NoError(t, BuildAutomationRecord(root, tf, rc))

	recordPath := filepath.Join(root, "gtms/automation", "records", "tc-skip002--bats.automation.md")
	record, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)
	assert.Equal(t, "skipped", record.LastDevResult, "ENH-094: skipped contract status must map to last-dev-result: skipped")
}

// --- ENH-109: results-file field on AutomationRecord ---

func TestReadAutomationRecord_WithoutResultsFile(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation", "records"), 0755))

	// Write an automation record without the results-file field (legacy format)
	content := "---\ntestcase: tc-legacy01\nframework: bats\nstatus: developed\nartefact: test.bats\ncycle: 1\n---\n"
	recordPath := filepath.Join(root, "gtms/automation", "records", "tc-legacy01--bats.automation.md")
	require.NoError(t, os.WriteFile(recordPath, []byte(content), 0644))

	record, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)
	assert.Equal(t, "tc-legacy01", record.TestCase)
	assert.Equal(t, "bats", record.Framework)
	assert.Empty(t, record.ResultsFile, "old record without results-file must read as empty string")
}

func TestWriteReadAutomationRecord_WithResultsFile(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation", "records"), 0755))

	record := &AutomationRecord{
		RecordCommon: RecordCommon{
			TestCase:  "tc-rf001",
			Framework: "playwright",
			Status:    "developed",
		},
		Artefact:    "tests/login.spec.ts",
		Adapter:     "claude-playwright",
		Cycle:       1,
		ResultsFile: "gtms/execution/task-bf9cd4d3--tc-rf001.results.yaml",
	}
	recordPath := filepath.Join(root, "gtms/automation", "records", "tc-rf001--playwright.automation.md")
	require.NoError(t, WriteAutomationRecord(recordPath, record))

	readBack, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)
	assert.Equal(t, "gtms/execution/task-bf9cd4d3--tc-rf001.results.yaml", readBack.ResultsFile)
	assert.Equal(t, "tc-rf001", readBack.TestCase)
	assert.Equal(t, "playwright", readBack.Framework)
}

func TestWriteAutomationRecord_EmptyResultsFileOmitted(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation", "records"), 0755))

	record := &AutomationRecord{
		RecordCommon: RecordCommon{
			TestCase:  "tc-rf002",
			Framework: "bats",
			Status:    "developed",
		},
		Cycle: 1,
		// ResultsFile intentionally left empty
	}
	recordPath := filepath.Join(root, "gtms/automation", "records", "tc-rf002--bats.automation.md")
	require.NoError(t, WriteAutomationRecord(recordPath, record))

	// Read the raw file and check that results-file key is not present
	data, err := os.ReadFile(recordPath)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "results-file:", "empty results-file must be omitted from YAML output")
}

// --- BUG-064: execution.Write wiring in UpdateExecutionResult ---

func TestUpdateExecutionResult_WritesResultsFile(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation", "records"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms", "execution"), 0755))

	// Seed an automation record
	record := &AutomationRecord{
		RecordCommon: RecordCommon{
			TestCase:  "tc-b064001",
			Framework: "bats",
			Status:    "developed",
		},
		Artefact: "test/acceptance/b064/tc-b064001.bats",
		Adapter:  "bats-runner",
		Cycle:    1,
	}
	recordPath := filepath.Join(root, "gtms/automation", "records", "tc-b064001--bats.automation.md")
	require.NoError(t, WriteAutomationRecord(recordPath, record))

	tf := &task.TaskFile{
		ID:        "task-b064aaa1",
		Type:      "execute",
		Target:    "tc-b064001",
		Adapter:   "bats-runner",
		Framework: "bats",
		Created:   "2026-05-04T10:00:00Z",
	}
	rc := &result.ResultContract{
		Task:      "task-b064aaa1",
		Command:   "execute",
		Target:    "tc-b064001",
		Adapter:   "bats-runner",
		Status:    "complete",
		Result:  "pass",
		Artefact:  "test/acceptance/b064/tc-b064001.bats",
		Completed: "2026-05-04T10:00:05Z",
		Summary:   "All tests passed",
	}

	err := UpdateExecutionResult(root, tf, rc)
	require.NoError(t, err)

	// Assert: results file exists with correct filename
	expectedFile := filepath.Join(root, "gtms", "execution", "task-b064aaa1--tc-b064001.results.yaml")
	assert.FileExists(t, expectedFile)

	// Parse and validate the results file
	rf, readErr := execution.Read(expectedFile)
	require.NoError(t, readErr)
	assert.Equal(t, "0.1", rf.SchemaVersion)
	assert.Equal(t, "task-b064aaa1", rf.TaskID)
	assert.Equal(t, "bats", rf.Framework)
	assert.Equal(t, "bats-runner", rf.Adapter)
	assert.Equal(t, "2026-05-04T10:00:00Z", rf.StartedAt)
	assert.Equal(t, "2026-05-04T10:00:05Z", rf.CompletedAt)
	assert.Equal(t, "test/acceptance/b064/tc-b064001.bats", rf.Artefact)
	require.Len(t, rf.Results, 1)
	assert.Equal(t, "tc-b064001", rf.Results[0].TCID)
	assert.Equal(t, "pass", rf.Results[0].Outcome)
	assert.Equal(t, "All tests passed", rf.Results[0].Message)

	// Assert: automation record's results-file field is populated
	updated, readRecErr := ReadAutomationRecord(recordPath)
	require.NoError(t, readRecErr)
	assert.Equal(t, "gtms/execution/task-b064aaa1--tc-b064001.results.yaml", updated.ResultsFile)
}

func TestUpdateExecutionResult_WritesResultsFile_FailStatus(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation", "records"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms", "execution"), 0755))

	record := &AutomationRecord{
		RecordCommon: RecordCommon{
			TestCase:  "tc-b064002",
			Framework: "bats",
			Status:    "developed",
		},
		Adapter: "bats-runner",
		Cycle:   1,
	}
	recordPath := filepath.Join(root, "gtms/automation", "records", "tc-b064002--bats.automation.md")
	require.NoError(t, WriteAutomationRecord(recordPath, record))

	tf := &task.TaskFile{
		ID:        "task-b064bbb2",
		Type:      "execute",
		Target:    "tc-b064002",
		Adapter:   "bats-runner",
		Framework: "bats",
		Created:   "2026-05-04T11:00:00Z",
	}
	rc := &result.ResultContract{
		Task:      "task-b064bbb2",
		Command:   "execute",
		Target:    "tc-b064002",
		Adapter:   "bats-runner",
		Status:    "complete",
		Result:    "fail",
		Completed: "2026-05-04T11:00:03Z",
		Summary:   "2 tests failed",
	}

	err := UpdateExecutionResult(root, tf, rc)
	require.NoError(t, err)

	expectedFile := filepath.Join(root, "gtms", "execution", "task-b064bbb2--tc-b064002.results.yaml")
	assert.FileExists(t, expectedFile)

	rf, readErr := execution.Read(expectedFile)
	require.NoError(t, readErr)
	require.Len(t, rf.Results, 1)
	assert.Equal(t, "tc-b064002", rf.Results[0].TCID)
	assert.Equal(t, "fail", rf.Results[0].Outcome)
	assert.Equal(t, "2 tests failed", rf.Results[0].Message)
}

func TestUpdateExecutionResult_WritesResultsFile_SkippedStatus(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation", "records"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms", "execution"), 0755))

	record := &AutomationRecord{
		RecordCommon: RecordCommon{
			TestCase:  "tc-b064003",
			Framework: "bats",
			Status:    "developed",
		},
		Adapter: "bats-runner",
		Cycle:   1,
	}
	recordPath := filepath.Join(root, "gtms/automation", "records", "tc-b064003--bats.automation.md")
	require.NoError(t, WriteAutomationRecord(recordPath, record))

	tf := &task.TaskFile{
		ID:        "task-b064ccc3",
		Type:      "execute",
		Target:    "tc-b064003",
		Adapter:   "bats-runner",
		Framework: "bats",
		Created:   "2026-05-04T12:00:00Z",
	}
	rc := &result.ResultContract{
		Task:      "task-b064ccc3",
		Command:   "execute",
		Target:    "tc-b064003",
		Adapter:   "bats-runner",
		Status:    "complete",
		Result:    "skip",
		Completed: "2026-05-04T12:00:01Z",
		Summary:   "All tests skipped",
	}

	err := UpdateExecutionResult(root, tf, rc)
	require.NoError(t, err)

	expectedFile := filepath.Join(root, "gtms", "execution", "task-b064ccc3--tc-b064003.results.yaml")
	assert.FileExists(t, expectedFile)

	rf, readErr := execution.Read(expectedFile)
	require.NoError(t, readErr)
	require.Len(t, rf.Results, 1)
	assert.Equal(t, "tc-b064003", rf.Results[0].TCID)
	assert.Equal(t, "skip", rf.Results[0].Outcome, "pipeline 'skipped' must map to results.go 'skip'")

	// Verify automation record's Result field still uses pipeline vocabulary "skipped"
	updated, readRecErr := ReadAutomationRecord(recordPath)
	require.NoError(t, readRecErr)
	assert.Equal(t, "skipped", updated.Result, "automation record must use pipeline vocabulary 'skipped'")
}

// --- ENH-130: contractOutcome and recordResultFromContract helper tests ---

func TestContractOutcome(t *testing.T) {
	tests := []struct {
		name     string
		status   string
		result   string
		expected string
	}{
		{"complete/pass", "complete", "pass", "pass"},
		{"complete/fail", "complete", "fail", "fail"},
		{"complete/skip", "complete", "skip", "skip"},
		{"complete/error", "complete", "error", "error"},
		{"error with empty result", "error", "", "error"},
		{"error with fail result (Q6)", "error", "fail", "fail"},
		{"error with pass result (Q6)", "error", "pass", "pass"},
		{"pending empty", "pending", "", "error"},
		{"unknown empty", "unknown", "", "error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc := &result.ResultContract{Status: tt.status, Result: tt.result}
			assert.Equal(t, tt.expected, contractOutcome(rc))
		})
	}
}

func TestRecordResultFromContract(t *testing.T) {
	tests := []struct {
		name     string
		status   string
		result   string
		expected string
	}{
		{"pass unchanged", "complete", "pass", "pass"},
		{"fail unchanged", "complete", "fail", "fail"},
		{"skip maps to skipped (Q10)", "complete", "skip", "skipped"},
		{"error unchanged", "complete", "error", "error"},
		{"adapter crash (error empty)", "error", "", "error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc := &result.ResultContract{Status: tt.status, Result: tt.result}
			assert.Equal(t, tt.expected, recordResultFromContract(rc))
		})
	}
}

// --- BUG-058: path separator sanitization at package boundary ---

func TestBuildAutomationRecord_RejectsPathTraversal(t *testing.T) {
	root := t.TempDir()

	tests := []struct {
		name      string
		target    string
		framework string
	}{
		{"slash in target", "x/y", "bats"},
		{"backslash in target", "x\\y", "bats"},
		{"dotdot in target", "../escape", "bats"},
		{"empty target", "", "bats"},
		{"slash in framework", "tc-007", "x/y"},
		{"backslash in framework", "tc-007", "x\\y"},
		{"dotdot in framework", "tc-007", "../../escape"},
		{"empty framework", "tc-007", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tf := &task.TaskFile{
				ID:        "task-bad001",
				Type:      "automate",
				Target:    tt.target,
				Adapter:   "local-claude",
				Framework: tt.framework,
			}
			rc := &result.ResultContract{
				Task:    "task-bad001",
				Command: "automate",
				Status:  "complete",
				Result:  "pass",
			}

			err := BuildAutomationRecord(root, tf, rc)
			require.Error(t, err, "expected rejection for %s", tt.name)
		})
	}
}

func TestUpdateExecutionResult_RejectsPathTraversal(t *testing.T) {
	root := t.TempDir()

	tests := []struct {
		name      string
		target    string
		framework string
	}{
		{"slash in target", "../escape", "bats"},
		{"backslash in target", "x\\y", "bats"},
		{"empty target", "", "bats"},
		{"slash in framework", "tc-007", "../../escape"},
		{"empty framework", "tc-007", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tf := &task.TaskFile{
				ID:        "task-bad002",
				Type:      "execute",
				Target:    tt.target,
				Adapter:   "bats-runner",
				Framework: tt.framework,
			}
			rc := &result.ResultContract{
				Task:    "task-bad002",
				Command: "execute",
				Status:  "complete",
				Result:  "pass",
			}

			err := UpdateExecutionResult(root, tf, rc)
			require.Error(t, err, "expected rejection for %s", tt.name)
		})
	}
}

func TestFindAutomationRecord_RejectsPathTraversal(t *testing.T) {
	root := t.TempDir()

	tests := []struct {
		name       string
		testCaseID string
		framework  string
	}{
		{"slash in testCaseID", "../escape", "bats"},
		{"backslash in testCaseID", "x\\y", "bats"},
		{"empty testCaseID", "", "bats"},
		{"slash in framework", "tc-007", "../../escape"},
		{"empty framework", "tc-007", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := FindAutomationRecord(root, tt.testCaseID, tt.framework)
			require.Error(t, err, "expected rejection for %s", tt.name)
		})
	}
}

// BUG-084: TestWriteLogSpill_RejectsPathTraversal lifted to
// internal/result/spill_test.go alongside the lifted WriteLogSpill helper.

// --- ENH-117: testcase-hash tests ---

func TestBuildAutomationRecord_WritesTestCaseHash(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation", "records"), 0755))

	// Create a test case spec so the hash can be computed
	casesDir := filepath.Join(root, "gtms", "test", "cases")
	require.NoError(t, os.MkdirAll(casesDir, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(casesDir, "tc-hash01-test.md"),
		[]byte("---\ntest_case_id: tc-hash01\ntitle: hash test\n---\nSome spec content\n"), 0644))

	tf := &task.TaskFile{
		ID:        "task-hash01",
		Type:      "automate",
		Target:    "tc-hash01",
		Adapter:   "test-adapter",
		Framework: "bats",
	}
	rc := &result.ResultContract{
		Task:     "task-hash01",
		Command:  "automate",
		Target:   "tc-hash01",
		Status:   "complete",
		Result:   "pass",
		Artefact: "test/tc-hash01.bats",
		Attempts: 1,
	}

	err := BuildAutomationRecord(root, tf, rc)
	require.NoError(t, err)

	recordPath := filepath.Join(root, "gtms/automation/records/tc-hash01--bats.automation.md")
	record, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)

	assert.NotEmpty(t, record.TestCaseHash, "testcase-hash should be populated")
	assert.Len(t, record.TestCaseHash, 16, "testcase-hash should be 16 hex chars")
	assert.Regexp(t, `^[0-9a-f]{16}$`, record.TestCaseHash)
}

func TestBuildAutomationRecord_MissingSpecLeavesHashEmpty(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation", "records"), 0755))
	// No test case spec exists — hash should be empty (best-effort)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms", "test", "cases"), 0755))

	tf := &task.TaskFile{
		ID:        "task-nohash",
		Type:      "automate",
		Target:    "tc-nospec",
		Adapter:   "test-adapter",
		Framework: "bats",
	}
	rc := &result.ResultContract{
		Task:     "task-nohash",
		Command:  "automate",
		Target:   "tc-nospec",
		Status:   "complete",
		Result:   "pass",
		Artefact: "test/tc-nospec.bats",
		Attempts: 1,
	}

	err := BuildAutomationRecord(root, tf, rc)
	require.NoError(t, err)

	recordPath := filepath.Join(root, "gtms/automation/records/tc-nospec--bats.automation.md")
	record, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)
	assert.Empty(t, record.TestCaseHash, "testcase-hash should be empty when spec is missing")
}

func TestUpdateExecutionResult_PreservesTestCaseHash(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation", "records"), 0755))

	// Write a record with a known testcase-hash
	recordPath := filepath.Join(root, "gtms/automation/records/tc-preserve--bats.automation.md")
	record := &AutomationRecord{
		RecordCommon: RecordCommon{
			TestCase:  "tc-preserve",
			Framework: "bats",
			Status:    "developed",
		},
		Artefact:     "test/tc-preserve.bats",
		ArtefactHash: "abcdef0123456789",
		TestCaseHash: "fedcba9876543210",
		Cycle:        1,
	}
	require.NoError(t, WriteAutomationRecord(recordPath, record))

	// Create the execution results dir
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms", "execution"), 0755))

	tf := &task.TaskFile{
		ID:        "task-exec01",
		Type:      "execute",
		Target:    "tc-preserve",
		Adapter:   "bats-runner",
		Framework: "bats",
		Created:   "2026-05-19T10:00:00Z",
	}
	rc := &result.ResultContract{
		Task:         "task-exec01",
		Command:      "execute",
		Target:       "tc-preserve",
		Adapter:      "bats-runner",
		Status:       "complete",
		Result:       "pass",
		Artefact:     "test/tc-preserve.bats",
		ArtefactHash: "abcdef0123456789",
		Completed:    "2026-05-19T10:01:00Z",
	}

	err := UpdateExecutionResult(root, tf, rc)
	require.NoError(t, err)

	updated, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)
	assert.Equal(t, "fedcba9876543210", updated.TestCaseHash,
		"testcase-hash must survive the UpdateExecutionResult read-modify-write cycle")
}
