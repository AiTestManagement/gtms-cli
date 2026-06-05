package reader

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// CON-023 / ENH-145 / ENH-146 reset contract change:
//
// Reset no longer mutates the automation record — wiring is read-only on the
// reader side. "Clearing execute results" now means deleting the terminal
// handoff under .gtms/results/{taskID}.handoff.yaml. The wiring record
// stays untouched (preserves identity); the local result is what gets
// cleared. ResetResult.AutomationRecordsCleared counts terminal handoffs
// removed for legacy-name compatibility, mirroring production reset.go.

// writeResetFixture creates a test case spec, a wiring record, AND a
// terminal handoff that the reset operation can clear.
func writeResetFixture(t *testing.T, root, tcID, folder string) {
	t.Helper()

	// Create test case
	tcDir := filepath.Join(root, "gtms/cases")
	if folder != "" {
		tcDir = filepath.Join(tcDir, folder)
	}
	require.NoError(t, os.MkdirAll(tcDir, 0755))
	tcContent := "---\ntest_case_id: " + tcID + "\ntitle: Test " + tcID + "\nrequirement: REQ-001\n---\n"
	require.NoError(t, os.WriteFile(filepath.Join(tcDir, tcID+"-test.md"), []byte(tcContent), 0644))

	// Wiring record (CON-023 / ENH-145) — immutable identity.
	wiringDir := filepath.Join(root, "gtms/automation", "wiring")
	require.NoError(t, os.MkdirAll(wiringDir, 0755))
	wiringContent := "testcase: " + tcID + "\n" +
		"testcase-hash: 0011223344556677\n" +
		"framework: bats\n" +
		"adapter: bats-runner\n" +
		"artefact: test/acceptance/" + tcID + ".bats\n" +
		"artefact-hash: aabbccddeeff0011\n"
	require.NoError(t, os.WriteFile(
		filepath.Join(wiringDir, tcID+"--bats.wiring.yaml"),
		[]byte(wiringContent), 0644))

	// Terminal handoff (.gtms/results/) — what reset actually clears.
	writeResetHandoff(t, root, tcID, "pass")
}

// writeResetHandoff writes a terminal handoff carrying a result outcome
// for the given TC. Reset deletes this file; the wiring record stays.
func writeResetHandoff(t *testing.T, root, tcID, result string) {
	t.Helper()
	resultDir := filepath.Join(root, ".gtms", "results")
	require.NoError(t, os.MkdirAll(resultDir, 0755))
	taskID := "task-" + strings.TrimPrefix(tcID, "tc-") + "-handoff"
	handoff := fmt.Sprintf(`task: %s
command: execute
target: %s
adapter: bats-runner
mode: sync
status: complete
result: %s
framework: bats
created: "2026-05-19T10:00:00Z"
completed: "2026-05-19T10:01:00Z"
`, taskID, tcID, result)
	require.NoError(t, os.WriteFile(
		filepath.Join(resultDir, taskID+".handoff.yaml"),
		[]byte(handoff), 0644))
}

// findHandoffsForTC returns the count of terminal handoffs targeting the given TC.
func findHandoffsForTC(t *testing.T, root, tcID string) int {
	t.Helper()
	dir := filepath.Join(root, ".gtms", "results")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("findHandoffsForTC: read %s: %v", dir, err)
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".handoff.yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		if strings.Contains(string(data), "target: "+tcID+"\n") {
			count++
		}
	}
	return count
}

// writeExecuteTaskFile creates an execute task file in the given status directory.
func writeExecuteTaskFile(t *testing.T, root, tcID, statusDir string) {
	t.Helper()
	dir := filepath.Join(root, "gtms/tasks", statusDir)
	require.NoError(t, os.MkdirAll(dir, 0755))
	content := "---\nid: task-abc1234\ntype: execute\ntarget: " + tcID + "\nadapter: bats-runner\nstatus: " + statusDir + "\ncreated: 2026-01-01T00:00:00Z\n---\n"
	filename := "task-abc1234-execute-" + tcID + ".md"
	require.NoError(t, os.WriteFile(filepath.Join(dir, filename), []byte(content), 0644))
}

func TestResetSingleTC_ClearsAutomationAndTaskFiles(t *testing.T) {
	root := t.TempDir()
	writeResetFixture(t, root, "tc-aaa1111", "")
	writeExecuteTaskFile(t, root, "tc-aaa1111", "complete")

	result, err := ResetExecuteResults(root, nil, "tc-aaa1111", false)
	require.NoError(t, err)

	assert.Equal(t, 1, result.TestCasesAffected)
	assert.Equal(t, 1, result.AutomationRecordsCleared, "reset removes the terminal handoff under .gtms/results/")
	assert.Equal(t, 1, result.TaskFilesRemoved)

	// CON-023 / ENH-145: wiring is read-only — reset must NOT touch it.
	wiringPath := filepath.Join(root, "gtms/automation", "wiring", "tc-aaa1111--bats.wiring.yaml")
	_, statErr := os.Stat(wiringPath)
	assert.NoError(t, statErr, "wiring record must survive reset")

	// Verify the terminal handoff was removed.
	assert.Equal(t, 0, findHandoffsForTC(t, root, "tc-aaa1111"),
		"reset must clear the terminal handoff for the TC")

	// Verify task file was removed
	files, _ := os.ReadDir(filepath.Join(root, "gtms/tasks", "complete"))
	assert.Empty(t, files)
}

func TestResetByScope_Shallow(t *testing.T) {
	root := t.TempDir()
	// Root-level TC
	writeResetFixture(t, root, "tc-aaa1111", "")
	// Nested TC
	writeResetFixture(t, root, "tc-bbb2222", "subfolder")
	writeExecuteTaskFile(t, root, "tc-aaa1111", "complete")
	writeExecuteTaskFile(t, root, "tc-bbb2222", "complete")

	scope := &ScopeInfo{
		ScanDir:   filepath.Join(root, "gtms/cases"),
		RelPath:   "gtms/cases/",
		Recursive: false,
	}

	result, err := ResetExecuteResults(root, scope, "", false)
	require.NoError(t, err)

	// Only root-level TC should be affected
	assert.Equal(t, 1, result.TestCasesAffected)
	assert.Equal(t, 1, result.AutomationRecordsCleared)
	assert.Equal(t, 1, result.TaskFilesRemoved)

	// Nested TC handoff should still be present
	assert.Equal(t, 1, findHandoffsForTC(t, root, "tc-bbb2222"),
		"nested-folder TC handoff must survive when shallow scope resets only root-level TCs")
}

func TestResetByScope_Recursive(t *testing.T) {
	root := t.TempDir()
	writeResetFixture(t, root, "tc-aaa1111", "")
	writeResetFixture(t, root, "tc-bbb2222", "subfolder")
	writeExecuteTaskFile(t, root, "tc-aaa1111", "complete")
	writeExecuteTaskFile(t, root, "tc-bbb2222", "error")

	scope := &ScopeInfo{
		ScanDir:   filepath.Join(root, "gtms/cases"),
		RelPath:   "gtms/cases/",
		Recursive: true,
	}

	result, err := ResetExecuteResults(root, scope, "", false)
	require.NoError(t, err)

	assert.Equal(t, 2, result.TestCasesAffected)
	assert.Equal(t, 2, result.AutomationRecordsCleared)
	assert.Equal(t, 2, result.TaskFilesRemoved)

	// Both handoffs should be cleared
	for _, tcID := range []string{"tc-aaa1111", "tc-bbb2222"} {
		assert.Equal(t, 0, findHandoffsForTC(t, root, tcID),
			"expected %s handoff to be cleared", tcID)
	}
}

func TestReset_NoAutomationRecord(t *testing.T) {
	root := t.TempDir()
	// Create test case but no automation record
	tcDir := filepath.Join(root, "gtms/cases")
	require.NoError(t, os.MkdirAll(tcDir, 0755))
	tcContent := "---\ntest_case_id: tc-ccc3333\ntitle: No Automation\nrequirement: REQ-001\n---\n"
	require.NoError(t, os.WriteFile(filepath.Join(tcDir, "tc-ccc3333-test.md"), []byte(tcContent), 0644))

	result, err := ResetExecuteResults(root, nil, "tc-ccc3333", false)
	require.NoError(t, err)

	assert.Equal(t, 0, result.TestCasesAffected)
	assert.Equal(t, 0, result.AutomationRecordsCleared)
	assert.Equal(t, 0, result.TaskFilesRemoved)
}

func TestReset_NoTaskFiles(t *testing.T) {
	root := t.TempDir()
	writeResetFixture(t, root, "tc-ddd4444", "")
	// No task files created — only the handoff exists.

	result, err := ResetExecuteResults(root, nil, "tc-ddd4444", false)
	require.NoError(t, err)

	assert.Equal(t, 1, result.TestCasesAffected)
	assert.Equal(t, 1, result.AutomationRecordsCleared, "the handoff should be cleared")
	assert.Equal(t, 0, result.TaskFilesRemoved)
}

func TestReset_DryRun(t *testing.T) {
	root := t.TempDir()
	writeResetFixture(t, root, "tc-eee5555", "")
	writeExecuteTaskFile(t, root, "tc-eee5555", "complete")

	result, err := ResetExecuteResults(root, nil, "tc-eee5555", true)
	require.NoError(t, err)

	assert.Equal(t, 1, result.TestCasesAffected)
	assert.Equal(t, 1, result.AutomationRecordsCleared)
	assert.Equal(t, 1, result.TaskFilesRemoved)

	// Verify nothing was actually deleted by the dry-run.
	assert.Equal(t, 1, findHandoffsForTC(t, root, "tc-eee5555"),
		"dry-run should not remove the terminal handoff")

	// Task file should still exist
	files, _ := os.ReadDir(filepath.Join(root, "gtms/tasks", "complete"))
	assert.Len(t, files, 1, "dry-run should not remove task files")
}

func TestReset_PendingAndInProgressTasksNotRemoved(t *testing.T) {
	root := t.TempDir()
	writeResetFixture(t, root, "tc-fff6666", "")
	writeExecuteTaskFile(t, root, "tc-fff6666", "pending")
	writeExecuteTaskFile(t, root, "tc-fff6666", "in-progress")
	writeExecuteTaskFile(t, root, "tc-fff6666", "complete")

	result, err := ResetExecuteResults(root, nil, "tc-fff6666", false)
	require.NoError(t, err)

	// Only the complete task file should be removed
	assert.Equal(t, 1, result.TaskFilesRemoved)

	// Pending and in-progress should still exist
	pendingFiles, _ := os.ReadDir(filepath.Join(root, "gtms/tasks", "pending"))
	assert.Len(t, pendingFiles, 1, "pending task file should remain")
	inProgressFiles, _ := os.ReadDir(filepath.Join(root, "gtms/tasks", "in-progress"))
	assert.Len(t, inProgressFiles, 1, "in-progress task file should remain")
}

func TestReset_ErrorTaskFilesAlsoRemoved(t *testing.T) {
	root := t.TempDir()
	writeResetFixture(t, root, "tc-ggg7777", "")
	writeExecuteTaskFile(t, root, "tc-ggg7777", "error")

	result, err := ResetExecuteResults(root, nil, "tc-ggg7777", false)
	require.NoError(t, err)

	assert.Equal(t, 1, result.TaskFilesRemoved)

	files, _ := os.ReadDir(filepath.Join(root, "gtms/tasks", "error"))
	assert.Empty(t, files)
}

func TestReset_AlreadyClear(t *testing.T) {
	root := t.TempDir()
	// CON-023 / ENH-145: "already clear" now means wiring exists (record
	// of identity) but no terminal handoff (no execute outcome yet).
	tcDir := filepath.Join(root, "gtms/cases")
	require.NoError(t, os.MkdirAll(tcDir, 0755))
	tcContent := "---\ntest_case_id: tc-hhh8888\ntitle: Already Clear\nrequirement: REQ-001\n---\n"
	require.NoError(t, os.WriteFile(filepath.Join(tcDir, "tc-hhh8888-test.md"), []byte(tcContent), 0644))

	wiringDir := filepath.Join(root, "gtms/automation", "wiring")
	require.NoError(t, os.MkdirAll(wiringDir, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(wiringDir, "tc-hhh8888--bats.wiring.yaml"),
		[]byte("testcase: tc-hhh8888\ntestcase-hash: 0011223344556677\nframework: bats\nadapter: bats-runner\nartefact: test/acceptance/tc-hhh8888.bats\nartefact-hash: aabbccddeeff0011\n"), 0644))

	result, err := ResetExecuteResults(root, nil, "tc-hhh8888", false)
	require.NoError(t, err)

	assert.Equal(t, 0, result.TestCasesAffected)
	assert.Equal(t, 0, result.AutomationRecordsCleared)
}

// TestReset_ClearsLogAndLogSpill verifies CON-023 / ENH-145: reset removes
// the terminal handoff entirely, taking its diagnostic log payload with it.
// Wiring is read-only — the on-disk source of `log:` / `notes-spill:` is
// the result contract, and clearing the result contract clears those fields
// as a side effect.
func TestReset_ClearsLogAndLogSpill(t *testing.T) {
	root := t.TempDir()

	tcDir := filepath.Join(root, "gtms/cases")
	require.NoError(t, os.MkdirAll(tcDir, 0755))
	tcContent := "---\ntest_case_id: tc-log999\ntitle: Log reset\nrequirement: ENH-077\n---\n"
	require.NoError(t, os.WriteFile(filepath.Join(tcDir, "tc-log999-test.md"), []byte(tcContent), 0644))

	// Wiring (read-only).
	wiringDir := filepath.Join(root, "gtms/automation", "wiring")
	require.NoError(t, os.MkdirAll(wiringDir, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(wiringDir, "tc-log999--bats.wiring.yaml"),
		[]byte("testcase: tc-log999\ntestcase-hash: 0011223344556677\nframework: bats\nadapter: bats-runner\nartefact: test/acceptance/tc-log999.bats\nartefact-hash: aabbccddeeff0011\n"), 0644))

	// Terminal handoff with a log payload — this is what reset clears.
	resultDir := filepath.Join(root, ".gtms", "results")
	require.NoError(t, os.MkdirAll(resultDir, 0755))
	handoff := `task: task-log999
command: execute
target: tc-log999
adapter: bats-runner
mode: sync
status: complete
result: fail
framework: bats
created: "2026-05-19T10:00:00Z"
completed: "2026-05-19T10:01:00Z"
log: |
  not ok 1 - broken assertion
`
	require.NoError(t, os.WriteFile(
		filepath.Join(resultDir, "task-log999.handoff.yaml"),
		[]byte(handoff), 0644))

	result, err := ResetExecuteResults(root, nil, "tc-log999", false)
	require.NoError(t, err)
	assert.Equal(t, 1, result.AutomationRecordsCleared,
		"reset must remove the terminal handoff carrying the log payload")

	// The handoff is gone; the wiring record survives unchanged.
	assert.Equal(t, 0, findHandoffsForTC(t, root, "tc-log999"),
		"handoff (and the log it carried) must be removed")
	_, statErr := os.Stat(filepath.Join(wiringDir, "tc-log999--bats.wiring.yaml"))
	assert.NoError(t, statErr, "wiring must survive reset")
}

// TestReset_AlreadyClear_WithOnlyLog verifies that a handoff carrying only
// a log payload (no result yet — e.g. an in-flight error mid-run) is still
// removed by reset. CON-023 / ENH-145: reset deletes any handoff matching
// the TC, regardless of which sub-fields are populated.
func TestReset_AlreadyClear_WithOnlyLog(t *testing.T) {
	root := t.TempDir()

	tcDir := filepath.Join(root, "gtms/cases")
	require.NoError(t, os.MkdirAll(tcDir, 0755))
	tcContent := "---\ntest_case_id: tc-logonly\ntitle: Log only\nrequirement: ENH-077\n---\n"
	require.NoError(t, os.WriteFile(filepath.Join(tcDir, "tc-logonly-test.md"), []byte(tcContent), 0644))

	// Wiring record (read-only).
	wiringDir := filepath.Join(root, "gtms/automation", "wiring")
	require.NoError(t, os.MkdirAll(wiringDir, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(wiringDir, "tc-logonly--bats.wiring.yaml"),
		[]byte("testcase: tc-logonly\ntestcase-hash: 0011223344556677\nframework: bats\nadapter: bats-runner\nartefact: test/acceptance/tc-logonly.bats\nartefact-hash: aabbccddeeff0011\n"), 0644))

	// Terminal handoff carrying only a log payload (status: error with no result).
	resultDir := filepath.Join(root, ".gtms", "results")
	require.NoError(t, os.MkdirAll(resultDir, 0755))
	handoff := `task: task-logonly
command: execute
target: tc-logonly
adapter: bats-runner
mode: sync
status: error
framework: bats
created: "2026-05-19T10:00:00Z"
completed: "2026-05-19T10:00:30Z"
log: |
  orphaned diagnostic text
`
	require.NoError(t, os.WriteFile(
		filepath.Join(resultDir, "task-logonly.handoff.yaml"),
		[]byte(handoff), 0644))

	result, err := ResetExecuteResults(root, nil, "tc-logonly", false)
	require.NoError(t, err)
	assert.Equal(t, 1, result.AutomationRecordsCleared,
		"orphan-log handoff should still be removed")

	assert.Equal(t, 0, findHandoffsForTC(t, root, "tc-logonly"),
		"handoff (and its log) must be gone")
}

// --- BUG-075: reset clears Summary conditionally ---

func TestReset_ClearsSummaryWhenExecuteFieldsPresent(t *testing.T) {
	// CON-023 / ENH-145: summary lives on the result contract under the
	// two-layer model. Reset removes the whole handoff, so summary goes
	// with it. The wiring record (no summary field by design) stays.
	root := t.TempDir()

	tcDir := filepath.Join(root, "gtms/cases")
	require.NoError(t, os.MkdirAll(tcDir, 0755))
	tcContent := "---\ntest_case_id: tc-sum001\ntitle: Summary reset\nrequirement: BUG-075\n---\n"
	require.NoError(t, os.WriteFile(filepath.Join(tcDir, "tc-sum001-test.md"), []byte(tcContent), 0644))

	// Wiring (read-only).
	wiringDir := filepath.Join(root, "gtms/automation", "wiring")
	require.NoError(t, os.MkdirAll(wiringDir, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(wiringDir, "tc-sum001--bats.wiring.yaml"),
		[]byte("testcase: tc-sum001\ntestcase-hash: 0011223344556677\nframework: bats\nadapter: bats-runner\nartefact: test/acceptance/tc-sum001.bats\nartefact-hash: aabbccddeeff0011\n"), 0644))

	// Terminal handoff with summary + log.
	resultDir := filepath.Join(root, ".gtms", "results")
	require.NoError(t, os.MkdirAll(resultDir, 0755))
	handoff := `task: task-sum001
command: execute
target: tc-sum001
adapter: bats-runner
mode: sync
status: complete
result: fail
framework: bats
created: "2026-05-19T10:00:00Z"
completed: "2026-05-19T10:01:00Z"
summary: "Process exited with code 1: fail detail"
log: |
  stderr output
`
	require.NoError(t, os.WriteFile(
		filepath.Join(resultDir, "task-sum001.handoff.yaml"),
		[]byte(handoff), 0644))

	result, err := ResetExecuteResults(root, nil, "tc-sum001", false)
	require.NoError(t, err)
	assert.Equal(t, 1, result.AutomationRecordsCleared)

	assert.Equal(t, 0, findHandoffsForTC(t, root, "tc-sum001"),
		"BUG-075: reset must remove the handoff (and the summary/result/log it carries)")
}

func TestReset_PreservesSummaryOnAutomateOnlyRecord(t *testing.T) {
	// CON-023 / ENH-145: an "automate-only" TC has wiring but no terminal
	// handoff yet (never executed). Reset must be a no-op for such a TC —
	// the wiring record carries no summary or result to clear, and no
	// handoff exists to remove. The wiring file itself must survive.
	root := t.TempDir()

	tcDir := filepath.Join(root, "gtms/cases")
	require.NoError(t, os.MkdirAll(tcDir, 0755))
	tcContent := "---\ntest_case_id: tc-sum002\ntitle: Automate only\nrequirement: BUG-075\n---\n"
	require.NoError(t, os.WriteFile(filepath.Join(tcDir, "tc-sum002-test.md"), []byte(tcContent), 0644))

	// Wiring exists; no handoff.
	wiringDir := filepath.Join(root, "gtms/automation", "wiring")
	require.NoError(t, os.MkdirAll(wiringDir, 0755))
	wiringPath := filepath.Join(wiringDir, "tc-sum002--bats.wiring.yaml")
	require.NoError(t, os.WriteFile(
		wiringPath,
		[]byte("testcase: tc-sum002\ntestcase-hash: 0011223344556677\nframework: bats\nadapter: bats-runner\nartefact: test/acceptance/tc-sum002.bats\nartefact-hash: aabbccddeeff0011\n"), 0644))

	result, err := ResetExecuteResults(root, nil, "tc-sum002", false)
	require.NoError(t, err)
	assert.Equal(t, 0, result.AutomationRecordsCleared,
		"BUG-075: automate-only TC (no handoff) should not be counted as cleared")

	_, statErr := os.Stat(wiringPath)
	assert.NoError(t, statErr,
		"BUG-075 / CON-023: wiring must be preserved — reset is read-only on wiring")
}

func TestReset_DryRun_CountsSummaryClearing(t *testing.T) {
	// CON-023 / ENH-145: dry-run counts what would be cleared without
	// touching disk. Under the new model, that means counting the
	// terminal handoff(s) carrying summary/result/log payload.
	root := t.TempDir()

	tcDir := filepath.Join(root, "gtms/cases")
	require.NoError(t, os.MkdirAll(tcDir, 0755))
	tcContent := "---\ntest_case_id: tc-sum003\ntitle: Dry run summary\nrequirement: BUG-075\n---\n"
	require.NoError(t, os.WriteFile(filepath.Join(tcDir, "tc-sum003-test.md"), []byte(tcContent), 0644))

	wiringDir := filepath.Join(root, "gtms/automation", "wiring")
	require.NoError(t, os.MkdirAll(wiringDir, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(wiringDir, "tc-sum003--bats.wiring.yaml"),
		[]byte("testcase: tc-sum003\ntestcase-hash: 0011223344556677\nframework: bats\nadapter: bats-runner\nartefact: test/acceptance/tc-sum003.bats\nartefact-hash: aabbccddeeff0011\n"), 0644))

	resultDir := filepath.Join(root, ".gtms", "results")
	require.NoError(t, os.MkdirAll(resultDir, 0755))
	handoff := `task: task-sum003
command: execute
target: tc-sum003
adapter: bats-runner
mode: sync
status: complete
result: fail
framework: bats
created: "2026-05-19T10:00:00Z"
completed: "2026-05-19T10:01:00Z"
summary: "Process exited with code 1: error output"
`
	require.NoError(t, os.WriteFile(
		filepath.Join(resultDir, "task-sum003.handoff.yaml"),
		[]byte(handoff), 0644))

	result, err := ResetExecuteResults(root, nil, "tc-sum003", true)
	require.NoError(t, err)
	assert.Equal(t, 1, result.AutomationRecordsCleared,
		"BUG-075: dry-run must count the handoff carrying summary as clearable")

	// Dry-run must not delete the handoff.
	assert.Equal(t, 1, findHandoffsForTC(t, root, "tc-sum003"),
		"BUG-075: dry-run must not delete the handoff")
}

func TestResetByScope_FolderScoped(t *testing.T) {
	root := t.TempDir()
	writeResetFixture(t, root, "tc-iii9999", "folder-a")
	writeResetFixture(t, root, "tc-jjj0000", "folder-b")
	writeExecuteTaskFile(t, root, "tc-iii9999", "complete")
	writeExecuteTaskFile(t, root, "tc-jjj0000", "complete")

	scope := &ScopeInfo{
		ScanDir:   filepath.Join(root, "gtms/cases", "folder-a"),
		RelPath:   "gtms/cases/folder-a/",
		Recursive: false,
	}

	result, err := ResetExecuteResults(root, scope, "", false)
	require.NoError(t, err)

	// Only folder-a TC should be affected
	assert.Equal(t, 1, result.TestCasesAffected)
	assert.Equal(t, 1, result.AutomationRecordsCleared)
	assert.Equal(t, 1, result.TaskFilesRemoved)

	// folder-b TC handoff should still be present.
	assert.Equal(t, 1, findHandoffsForTC(t, root, "tc-jjj0000"),
		"folder-b handoff must survive folder-a-scoped reset")
}
