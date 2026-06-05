package pipeline

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrateAutomationRecords_RenamesOldFields(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "gtms/automation/records")
	require.NoError(t, os.MkdirAll(dir, 0755))

	// Write a legacy-format record with old field names
	legacy := `---
testcase: tc-mig001
framework: bats
status: accepted
artefact: test/acceptance/tc-mig001.bats
adapter: bats-runner
last-dev-result: pass
last-formal-result: fail
last-formal-run: results/tc-mig001.xml
last-formal-run-at: "2026-04-16T14:32:11Z"
artefact-hash: abc123def456
log: |
  not ok 1 - expected pass
log-spill: .gtms/logs/task-old.log
attempts: 2
summary: 2 of 5 failed
cycle: 3
defect: BUG-042
results-file: gtms/execution/task-x--tc-mig001.results.yaml
---
`
	recordPath := filepath.Join(dir, "tc-mig001--bats.automation.md")
	require.NoError(t, os.WriteFile(recordPath, []byte(legacy), 0644))

	count, err := MigrateAutomationRecords(root)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// Read back with new struct
	record, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)

	assert.Equal(t, "tc-mig001", record.TestCase)
	assert.Equal(t, "bats", record.Framework)
	assert.Equal(t, "accepted", record.Status)
	assert.Equal(t, "fail", record.Result, "last-formal-result must map to result")
	assert.Equal(t, "results/tc-mig001.xml", record.ExecutedArtefact, "last-formal-run must map to executed_artefact")
	assert.Equal(t, "2026-04-16T14:32:11Z", record.ExecutedAt, "last-formal-run-at must map to executed_at")
	assert.Contains(t, record.Notes, "not ok 1 - expected pass", "log must map to notes")
	assert.Equal(t, ".gtms/logs/task-old.log", record.NotesSpill, "log-spill must map to notes-spill")
	assert.Equal(t, []string{"BUG-042"}, record.Defect, "defect string must convert to []string")
	assert.Equal(t, "abc123def456", record.ArtefactHash)
	assert.Equal(t, "pass", record.LastDevResult)
	assert.Equal(t, 2, record.Attempts)
	assert.Equal(t, "2 of 5 failed", record.Summary)
	assert.Equal(t, 3, record.Cycle)
	assert.Equal(t, "gtms/execution/task-x--tc-mig001.results.yaml", record.ResultsFile)
	assert.Equal(t, "bats-runner", record.Adapter)
	assert.Equal(t, "test/acceptance/tc-mig001.bats", record.Artefact)

	// New fields should be empty
	assert.Empty(t, record.ExecutedBy)
	assert.Empty(t, record.Environment)
}

func TestMigrateAutomationRecords_AlreadyNewFormat(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "gtms/automation/records")
	require.NoError(t, os.MkdirAll(dir, 0755))

	// Write a record already in new format
	record := &AutomationRecord{
		RecordCommon: RecordCommon{
			TestCase:  "tc-mig002",
			Framework: "playwright",
			Status:    "developed",
			Result:    "pass",
			ExecutedAt: "2026-05-01T10:00:00Z",
		},
		Artefact:         "tests/tc-mig002.spec.ts",
		Adapter:          "claude-playwright",
		Cycle:            1,
		ExecutedArtefact: "results/tc-mig002.xml",
	}
	recordPath := filepath.Join(dir, "tc-mig002--playwright.automation.md")
	require.NoError(t, WriteAutomationRecord(recordPath, record))

	count, err := MigrateAutomationRecords(root)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "already-migrated records should be skipped")
}

func TestMigrateAutomationRecords_EmptyDefectStaysEmpty(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "gtms/automation/records")
	require.NoError(t, os.MkdirAll(dir, 0755))

	legacy := `---
testcase: tc-mig003
framework: bats
status: developed
last-formal-result: pass
cycle: 1
---
`
	recordPath := filepath.Join(dir, "tc-mig003--bats.automation.md")
	require.NoError(t, os.WriteFile(recordPath, []byte(legacy), 0644))

	count, err := MigrateAutomationRecords(root)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	record, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)
	assert.Empty(t, record.Defect, "empty defect must stay empty (not []string{''})")
}

func TestMigrateAutomationRecords_PreservesAllFields(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "gtms/automation/records")
	require.NoError(t, os.MkdirAll(dir, 0755))

	legacy := `---
testcase: tc-mig004
framework: pester
status: accepted
artefact: gtms/automation/specs/tc-mig004.tests.ps1
branch: feature/mig-test
adapter: remote-pester-lean
last-dev-result: pass
last-formal-result: error
last-formal-run: results/tc-mig004.xml
last-formal-run-at: "2026-04-20T12:00:00Z"
artefact-hash: hash123
attempts: 3
summary: All tests passed
cycle: 2
---
`
	recordPath := filepath.Join(dir, "tc-mig004--pester.automation.md")
	require.NoError(t, os.WriteFile(recordPath, []byte(legacy), 0644))

	count, err := MigrateAutomationRecords(root)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	record, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)
	assert.Equal(t, "tc-mig004", record.TestCase)
	assert.Equal(t, "pester", record.Framework)
	assert.Equal(t, "accepted", record.Status)
	assert.Equal(t, "feature/mig-test", record.Branch)
	assert.Equal(t, "pass", record.LastDevResult)
	assert.Equal(t, "error", record.Result)
	assert.Equal(t, "results/tc-mig004.xml", record.ExecutedArtefact)
	assert.Equal(t, "2026-04-20T12:00:00Z", record.ExecutedAt)
	assert.Equal(t, "hash123", record.ArtefactHash)
	assert.Equal(t, 3, record.Attempts)
	assert.Equal(t, "All tests passed", record.Summary)
	assert.Equal(t, 2, record.Cycle)
}

func TestMigrateAutomationRecords_NoDirectory(t *testing.T) {
	root := t.TempDir()
	// No gtms/automation/records/ directory

	count, err := MigrateAutomationRecords(root)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestMigrateAutomationRecords_Idempotent(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "gtms/automation/records")
	require.NoError(t, os.MkdirAll(dir, 0755))

	legacy := `---
testcase: tc-mig005
framework: bats
status: developed
last-formal-result: pass
cycle: 1
---
`
	recordPath := filepath.Join(dir, "tc-mig005--bats.automation.md")
	require.NoError(t, os.WriteFile(recordPath, []byte(legacy), 0644))

	// First migration
	count1, err := MigrateAutomationRecords(root)
	require.NoError(t, err)
	assert.Equal(t, 1, count1)

	// Second migration — should be a no-op
	count2, err := MigrateAutomationRecords(root)
	require.NoError(t, err)
	assert.Equal(t, 0, count2, "second migration should be a no-op")
}
