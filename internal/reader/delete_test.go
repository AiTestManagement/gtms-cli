package reader

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeDeleteFixture creates a full artifact chain for a test case.
// It creates: test case spec, wiring record (with artefact field pointing to a test script),
// the test script file, an execute task file, and a result contract.
//
// CON-023 / ENH-145: the automation record (gtms/automation/records/...automation.md)
// is retired. Identity now lives at gtms/automation/wiring/{tc}--{fw}.wiring.yaml as
// pure YAML carrying the six identity fields. The delete code scans wiring for
// `artefact:` (test script) and follows result contracts by `target` substring.
func writeDeleteFixture(t *testing.T, root, tcID, folder string) {
	t.Helper()

	// Derive a unique task ID from the TC ID to avoid collisions in multi-TC tests.
	// Strip "tc-" prefix and use as task suffix: tc-aaa11110 -> task-aaa11110
	taskID := "task-" + strings.TrimPrefix(tcID, "tc-")

	// Test case spec
	tcDir := filepath.Join(root, "gtms/test/cases")
	if folder != "" {
		tcDir = filepath.Join(tcDir, folder)
	}
	require.NoError(t, os.MkdirAll(tcDir, 0755))
	tcContent := "---\ntest_case_id: " + tcID + "\ntitle: Test " + tcID + "\nrequirement: REQ-001\n---\n"
	require.NoError(t, os.WriteFile(filepath.Join(tcDir, tcID+"-test.md"), []byte(tcContent), 0644))

	// Test script file (the artefact the wiring record points to)
	scriptDir := filepath.Join(root, "test", "acceptance")
	if folder != "" {
		scriptDir = filepath.Join(scriptDir, folder)
	}
	require.NoError(t, os.MkdirAll(scriptDir, 0755))
	scriptRelPath := "test/acceptance/"
	if folder != "" {
		scriptRelPath += folder + "/"
	}
	scriptRelPath += tcID + "-test.bats"
	require.NoError(t, os.WriteFile(filepath.Join(root, scriptRelPath), []byte("@test \"placeholder\" { true; }"), 0644))

	// Wiring record (CON-023 / ENH-145) — pure YAML, six identity fields.
	wiringDir := filepath.Join(root, "gtms", "automation", "wiring")
	require.NoError(t, os.MkdirAll(wiringDir, 0755))
	wiringContent := "testcase: " + tcID + "\n" +
		"testcase-hash: 0011223344556677\n" +
		"framework: bats\n" +
		"adapter: bats-runner\n" +
		"artefact: " + scriptRelPath + "\n" +
		"artefact-hash: aabbccddeeff0011\n"
	require.NoError(t, os.WriteFile(
		filepath.Join(wiringDir, tcID+"--bats.wiring.yaml"),
		[]byte(wiringContent), 0644))

	// Execute task file (complete)
	taskDir := filepath.Join(root, "gtms/tasks", "complete")
	require.NoError(t, os.MkdirAll(taskDir, 0755))
	taskContent := "---\nid: " + taskID + "\ntype: execute\ntarget: " + tcID + "\nadapter: bats-runner\nstatus: complete\ncreated: 2026-01-01T00:00:00Z\n---\n"
	require.NoError(t, os.WriteFile(filepath.Join(taskDir, taskID+"-execute-"+tcID+".md"), []byte(taskContent), 0644))

	// Result contract (.gtms/results/) — target is the TC ID so findResultContracts
	// matches via substring search.
	resultsDir := filepath.Join(root, ".gtms", "results")
	require.NoError(t, os.MkdirAll(resultsDir, 0755))
	resultContent := "task: " + taskID + "\ncommand: execute\ntarget: " + tcID + "\nadapter: bats-runner\nmode: sync\nstatus: complete\n"
	require.NoError(t, os.WriteFile(filepath.Join(resultsDir, taskID+".handoff.yaml"), []byte(resultContent), 0644))
}

// writeTaskFile creates a task file in the specified status directory with the specified command type.
func writeTaskFile(t *testing.T, root, tcID, statusDir, command, taskID string) {
	t.Helper()
	dir := filepath.Join(root, "gtms/tasks", statusDir)
	require.NoError(t, os.MkdirAll(dir, 0755))
	content := "---\nid: " + taskID + "\ntype: " + command + "\ntarget: " + tcID + "\nadapter: test-adapter\nstatus: " + statusDir + "\ncreated: 2026-01-01T00:00:00Z\n---\n"
	filename := taskID + "-" + command + "-" + tcID + ".md"
	require.NoError(t, os.WriteFile(filepath.Join(dir, filename), []byte(content), 0644))
}

func TestDeleteSingleTC(t *testing.T) {
	root := t.TempDir()
	writeDeleteFixture(t, root, "tc-aaa11110", "myfolder")

	result, err := DeleteArtifacts(root, nil, "tc-aaa11110", false, false)
	require.NoError(t, err)

	assert.Equal(t, 1, result.TestCasesProcessed)
	assert.Equal(t, 1, result.TestCaseSpecsRemoved)
	assert.Equal(t, 1, result.AutomationRecords)
	assert.Equal(t, 1, result.TestScripts)
	assert.Equal(t, 1, result.TaskFiles)
	assert.Equal(t, 1, result.ResultContracts)
	assert.Equal(t, 5, result.TotalFiles())
	assert.Len(t, result.FilesDeleted, 5)

	// Verify files are actually gone
	specFiles, _ := filepath.Glob(filepath.Join(root, "gtms/test/cases", "myfolder", "tc-aaa11110-*.md"))
	assert.Empty(t, specFiles)

	wiringFiles, _ := filepath.Glob(filepath.Join(root, "gtms/automation", "wiring", "tc-aaa11110--*.wiring.yaml"))
	assert.Empty(t, wiringFiles)

	scriptFiles, _ := filepath.Glob(filepath.Join(root, "test", "acceptance", "myfolder", "tc-aaa11110-*"))
	assert.Empty(t, scriptFiles)

	taskFiles, _ := os.ReadDir(filepath.Join(root, "gtms/tasks", "complete"))
	assert.Empty(t, taskFiles)

	resultFiles, _ := filepath.Glob(filepath.Join(root, ".gtms", "results", "*.handoff.yaml"))
	assert.Empty(t, resultFiles)
}

func TestDeleteSingleTC_KeepSpec(t *testing.T) {
	root := t.TempDir()
	writeDeleteFixture(t, root, "tc-bbb22220", "myfolder")

	result, err := DeleteArtifacts(root, nil, "tc-bbb22220", true, false)
	require.NoError(t, err)

	assert.Equal(t, 1, result.TestCasesProcessed)
	assert.Equal(t, 0, result.TestCaseSpecsRemoved, "spec should be preserved")
	assert.Equal(t, 1, result.AutomationRecords)
	assert.Equal(t, 1, result.TestScripts)
	assert.Equal(t, 1, result.TaskFiles)
	assert.Equal(t, 1, result.ResultContracts, "keep-spec should still delete result contracts")

	// Verify spec file still exists
	specFiles, _ := filepath.Glob(filepath.Join(root, "gtms/test/cases", "myfolder", "tc-bbb22220-*.md"))
	assert.Len(t, specFiles, 1, "spec file should still exist")

	// Verify other artifacts are gone
	wiringFiles, _ := filepath.Glob(filepath.Join(root, "gtms/automation", "wiring", "tc-bbb22220--*.wiring.yaml"))
	assert.Empty(t, wiringFiles)

	// Verify result contracts are gone
	resultFiles, _ := filepath.Glob(filepath.Join(root, ".gtms", "results", "*.handoff.yaml"))
	assert.Empty(t, resultFiles)
}

func TestDeleteFolder(t *testing.T) {
	root := t.TempDir()
	writeDeleteFixture(t, root, "tc-ccc33330", "cleanup")
	writeDeleteFixture(t, root, "tc-ddd44440", "cleanup")

	scope := &ScopeInfo{
		ScanDir:   filepath.Join(root, "gtms/test/cases", "cleanup"),
		RelPath:   "gtms/test/cases/cleanup/",
		Recursive: false,
	}

	result, err := DeleteArtifacts(root, scope, "", false, false)
	require.NoError(t, err)

	assert.Equal(t, 2, result.TestCasesProcessed)
	assert.Equal(t, 2, result.TestCaseSpecsRemoved)
	assert.Equal(t, 2, result.AutomationRecords)
	assert.Equal(t, 2, result.TestScripts)
	assert.Equal(t, 2, result.TaskFiles)
	assert.Equal(t, 2, result.ResultContracts)
}

func TestDeleteFolder_KeepSpec(t *testing.T) {
	root := t.TempDir()
	writeDeleteFixture(t, root, "tc-eee55550", "keep-folder")
	writeDeleteFixture(t, root, "tc-fff66660", "keep-folder")

	scope := &ScopeInfo{
		ScanDir:   filepath.Join(root, "gtms/test/cases", "keep-folder"),
		RelPath:   "gtms/test/cases/keep-folder/",
		Recursive: false,
	}

	result, err := DeleteArtifacts(root, scope, "", true, false)
	require.NoError(t, err)

	assert.Equal(t, 2, result.TestCasesProcessed)
	assert.Equal(t, 0, result.TestCaseSpecsRemoved, "specs should be preserved")
	assert.Equal(t, 2, result.AutomationRecords)
	assert.Equal(t, 2, result.ResultContracts, "keep-spec should still delete result contracts")

	// Verify spec files still exist
	specFiles, _ := filepath.Glob(filepath.Join(root, "gtms/test/cases", "keep-folder", "tc-*.md"))
	assert.Len(t, specFiles, 2, "both spec files should still exist")
}

func TestDelete_DryRun(t *testing.T) {
	root := t.TempDir()
	writeDeleteFixture(t, root, "tc-ggg77770", "dryfolder")

	result, err := DeleteArtifacts(root, nil, "tc-ggg77770", false, true)
	require.NoError(t, err)

	assert.Equal(t, 1, result.TestCasesProcessed)
	assert.Equal(t, 1, result.TestCaseSpecsRemoved)
	assert.Equal(t, 1, result.AutomationRecords)
	assert.Equal(t, 1, result.TestScripts)
	assert.Equal(t, 1, result.TaskFiles)
	assert.Equal(t, 1, result.ResultContracts)
	assert.Len(t, result.FilesDeleted, 5)

	// Verify files still exist (dry-run should not delete)
	specFiles, _ := filepath.Glob(filepath.Join(root, "gtms/test/cases", "dryfolder", "tc-ggg77770-*.md"))
	assert.Len(t, specFiles, 1, "dry-run should not remove spec file")

	wiringFiles, _ := filepath.Glob(filepath.Join(root, "gtms/automation", "wiring", "tc-ggg77770--*.wiring.yaml"))
	assert.Len(t, wiringFiles, 1, "dry-run should not remove wiring record")

	scriptFiles, _ := filepath.Glob(filepath.Join(root, "test", "acceptance", "dryfolder", "tc-ggg77770-*"))
	assert.Len(t, scriptFiles, 1, "dry-run should not remove test script")

	taskFiles, _ := os.ReadDir(filepath.Join(root, "gtms/tasks", "complete"))
	assert.Len(t, taskFiles, 1, "dry-run should not remove task file")

	resultFiles, _ := filepath.Glob(filepath.Join(root, ".gtms", "results", "*.handoff.yaml"))
	assert.Len(t, resultFiles, 1, "dry-run should not remove result contract")
}

func TestDelete_MissingArtifacts(t *testing.T) {
	root := t.TempDir()

	// Create only a test case spec — no automation, no scripts, no tasks
	tcDir := filepath.Join(root, "gtms/test/cases")
	require.NoError(t, os.MkdirAll(tcDir, 0755))
	tcContent := "---\ntest_case_id: tc-hhh88880\ntitle: Partial\nrequirement: REQ-001\n---\n"
	require.NoError(t, os.WriteFile(filepath.Join(tcDir, "tc-hhh88880-partial.md"), []byte(tcContent), 0644))

	result, err := DeleteArtifacts(root, nil, "tc-hhh88880", false, false)
	require.NoError(t, err)

	assert.Equal(t, 1, result.TestCasesProcessed)
	assert.Equal(t, 1, result.TestCaseSpecsRemoved)
	assert.Equal(t, 0, result.AutomationRecords)
	assert.Equal(t, 0, result.TestScripts)
	assert.Equal(t, 0, result.TaskFiles)
	assert.Equal(t, 0, result.ResultFiles)
}

func TestDelete_NonexistentTC(t *testing.T) {
	root := t.TempDir()
	// Create empty cases dir so the walk doesn't fail
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/test/cases"), 0755))

	result, err := DeleteArtifacts(root, nil, "tc-nonexist0", false, false)
	require.NoError(t, err)

	assert.Equal(t, 1, result.TestCasesProcessed)
	assert.Equal(t, 0, result.TotalFiles())
}

func TestDelete_MultipleAutomationRecords(t *testing.T) {
	root := t.TempDir()
	tcID := "tc-iii99990"

	// Create test case
	tcDir := filepath.Join(root, "gtms/test/cases")
	require.NoError(t, os.MkdirAll(tcDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tcDir, tcID+"-multi.md"),
		[]byte("---\ntest_case_id: "+tcID+"\ntitle: Multi\nrequirement: REQ-001\n---\n"), 0644))

	// Create multiple wiring records (bats + playwright) with artefact fields
	wiringDir := filepath.Join(root, "gtms/automation", "wiring")
	require.NoError(t, os.MkdirAll(wiringDir, 0755))

	// Create script files that the records point to
	require.NoError(t, os.MkdirAll(filepath.Join(root, "test", "acceptance"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "test", "acceptance", tcID+"-test.bats"), []byte("# stub"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms", "scripts", "playwright"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "gtms", "scripts", "playwright", tcID+"-test.spec.ts"), []byte("// stub"), 0644))

	for _, fw := range []string{"bats", "playwright"} {
		var artefact string
		if fw == "bats" {
			artefact = "test/acceptance/" + tcID + "-test.bats"
		} else {
			artefact = "gtms/scripts/playwright/" + tcID + "-test.spec.ts"
		}
		content := "testcase: " + tcID + "\n" +
			"testcase-hash: 0011223344556677\n" +
			"framework: " + fw + "\n" +
			"adapter: " + fw + "-runner\n" +
			"artefact: " + artefact + "\n" +
			"artefact-hash: aabbccddeeff0011\n"
		require.NoError(t, os.WriteFile(filepath.Join(wiringDir, tcID+"--"+fw+".wiring.yaml"), []byte(content), 0644))
	}

	result, err := DeleteArtifacts(root, nil, tcID, false, false)
	require.NoError(t, err)

	assert.Equal(t, 2, result.AutomationRecords, "should delete both framework wiring records")
	assert.Equal(t, 2, result.TestScripts, "should delete both test scripts")

	// Verify both wiring records are gone
	remaining, _ := filepath.Glob(filepath.Join(wiringDir, tcID+"--*.wiring.yaml"))
	assert.Empty(t, remaining)
}

func TestDelete_AllTaskStatuses(t *testing.T) {
	root := t.TempDir()
	tcID := "tc-jjj00000"

	// Create test case
	tcDir := filepath.Join(root, "gtms/test/cases")
	require.NoError(t, os.MkdirAll(tcDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tcDir, tcID+"-statuses.md"),
		[]byte("---\ntest_case_id: "+tcID+"\ntitle: Statuses\nrequirement: REQ-001\n---\n"), 0644))

	// Create task files in all 5 status directories
	statuses := []string{"pending", "in-progress", "in-review", "complete", "error"}
	for i, status := range statuses {
		taskID := fmt.Sprintf("task-%08d", i)
		writeTaskFile(t, root, tcID, status, "execute", taskID)
	}

	result, err := DeleteArtifacts(root, nil, tcID, false, false)
	require.NoError(t, err)

	assert.Equal(t, 5, result.TaskFiles, "should find tasks in all 5 status directories")

	// Verify all are gone
	for _, status := range statuses {
		entries, _ := os.ReadDir(filepath.Join(root, "gtms/tasks", status))
		assert.Empty(t, entries, "status dir %s should be empty", status)
	}
}

func TestDelete_AllTaskTypes(t *testing.T) {
	root := t.TempDir()
	tcID := "tc-kkk11110"

	// Create test case
	tcDir := filepath.Join(root, "gtms/test/cases")
	require.NoError(t, os.MkdirAll(tcDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tcDir, tcID+"-types.md"),
		[]byte("---\ntest_case_id: "+tcID+"\ntitle: Types\nrequirement: REQ-001\n---\n"), 0644))

	// Create task files for all 3 command types in complete
	writeTaskFile(t, root, tcID, "complete", "create", "task-00000001")
	writeTaskFile(t, root, tcID, "complete", "automate", "task-00000002")
	writeTaskFile(t, root, tcID, "complete", "execute", "task-00000003")

	result, err := DeleteArtifacts(root, nil, tcID, false, false)
	require.NoError(t, err)

	assert.Equal(t, 3, result.TaskFiles, "should find all 3 command-type task files")

	entries, _ := os.ReadDir(filepath.Join(root, "gtms/tasks", "complete"))
	assert.Empty(t, entries)
}

func TestDelete_RecordDrivenResultFiles(t *testing.T) {
	// CON-023 / ENH-145: the legacy `executed_artefact:` field that drove
	// the original "delete the JUnit XML this record produced" path lived
	// on the retired .automation.md schema. The new six-field wiring
	// schema has no equivalent — the per-run artefact path now lives on
	// the result contract under `artefact:`.
	//
	// Production delete.go still reads `executed_artefact:` from any
	// manual record that carries it, so the legacy field is preserved for
	// manual-pipeline TCs (where the manual result file IS the artefact).
	// Test that path here instead — keeps the behavioural assertion alive
	// against the surface that still owns it.
	root := t.TempDir()
	tcID := "tc-lll22220"

	// Create test case spec
	tcDir := filepath.Join(root, "gtms/test/cases")
	require.NoError(t, os.MkdirAll(tcDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tcDir, tcID+"-results.md"),
		[]byte("---\ntest_case_id: "+tcID+"\ntitle: Result files\nrequirement: REQ-001\n---\n"), 0644))

	// Create a result file that the manual record's executed_artefact points to
	resultDir := filepath.Join(root, "results", "junit")
	require.NoError(t, os.MkdirAll(resultDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(resultDir, tcID+"-results.xml"),
		[]byte("<testsuites/>"), 0644))

	// Manual record carries executed_artefact pointing at the result file.
	// This is the only surface where executed_artefact still exists
	// post-CON-023 (manual records are the manual-pipeline artefact root).
	// The manual record itself does NOT declare its own `artefact:` field
	// — leaving it empty avoids a double-delete on the record file.
	manualDir := filepath.Join(root, "gtms", "manual", "records")
	require.NoError(t, os.MkdirAll(manualDir, 0755))
	manualContent := "test_case_id: " + tcID +
		"\nframework: manual" +
		"\nexecuted_artefact: results/junit/" + tcID + "-results.xml\n" +
		"result: pass\n"
	require.NoError(t, os.WriteFile(
		filepath.Join(manualDir, tcID+"--manual.result.yaml"),
		[]byte(manualContent), 0644))

	result, err := DeleteArtifacts(root, nil, tcID, false, false)
	require.NoError(t, err)

	assert.Equal(t, 1, result.ResultFiles, "should find result file from manual record executed_artefact field")

	remaining, _ := filepath.Glob(filepath.Join(resultDir, "*"+tcID+"*"))
	assert.Empty(t, remaining)
}

func TestDelete_NoSlugFilename(t *testing.T) {
	root := t.TempDir()
	tcID := "tc-mmm33330"

	// Create test case with no slug suffix (just tcID.md)
	tcDir := filepath.Join(root, "gtms/test/cases")
	require.NoError(t, os.MkdirAll(tcDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tcDir, tcID+".md"),
		[]byte("---\ntest_case_id: "+tcID+"\ntitle: No Slug\nrequirement: REQ-001\n---\n"), 0644))

	result, err := DeleteArtifacts(root, nil, tcID, false, false)
	require.NoError(t, err)

	assert.Equal(t, 1, result.TestCaseSpecsRemoved, "should match no-slug filename")
}

func TestDelete_ResultContracts_MultipleCommands(t *testing.T) {
	root := t.TempDir()
	tcID := "tc-nnn44440"

	// Create test case spec so the delete has something to process
	tcDir := filepath.Join(root, "gtms/test/cases")
	require.NoError(t, os.MkdirAll(tcDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tcDir, tcID+"-multi.md"),
		[]byte("---\ntest_case_id: "+tcID+"\ntitle: Multi\nrequirement: REQ-001\n---\n"), 0644))

	// Create result contracts for 3 different command types, all referencing this TC
	resultsDir := filepath.Join(root, ".gtms", "results")
	require.NoError(t, os.MkdirAll(resultsDir, 0755))

	// execute: target is a file path containing the TC ID
	require.NoError(t, os.WriteFile(filepath.Join(resultsDir, "task-11111111.handoff.yaml"),
		[]byte("task: task-11111111\ncommand: execute\ntarget: test/acceptance/folder/"+tcID+"-multi.bats\nstatus: complete\n"), 0644))

	// automate: target is the source test case path
	require.NoError(t, os.WriteFile(filepath.Join(resultsDir, "task-22222222.handoff.yaml"),
		[]byte("task: task-22222222\ncommand: automate\ntarget: gtms/test/cases/folder/"+tcID+"-multi.md\nstatus: complete\n"), 0644))

	// create: target is a requirement (contains TC ID in this scenario)
	require.NoError(t, os.WriteFile(filepath.Join(resultsDir, "task-33333333.handoff.yaml"),
		[]byte("task: task-33333333\ncommand: create\ntarget: "+tcID+"\nstatus: complete\n"), 0644))

	// Unrelated contract for a DIFFERENT TC (should survive)
	require.NoError(t, os.WriteFile(filepath.Join(resultsDir, "task-99999999.handoff.yaml"),
		[]byte("task: task-99999999\ncommand: execute\ntarget: test/acceptance/other/tc-zzz99990-other.bats\nstatus: complete\n"), 0644))

	result, err := DeleteArtifacts(root, nil, tcID, false, false)
	require.NoError(t, err)

	assert.Equal(t, 3, result.ResultContracts, "should delete all 3 contracts referencing this TC")

	// Verify the unrelated contract survived
	remaining, _ := filepath.Glob(filepath.Join(resultsDir, "*.handoff.yaml"))
	assert.Len(t, remaining, 1, "unrelated contract should survive")
	assert.Contains(t, remaining[0], "task-99999999")
}

func TestDelete_ResultContracts_MissingDir(t *testing.T) {
	root := t.TempDir()

	// No .gtms/results/ directory at all
	result, err := DeleteArtifacts(root, nil, "tc-ooo55550", false, false)
	require.NoError(t, err)

	assert.Equal(t, 0, result.ResultContracts, "should handle missing .gtms/results/ gracefully")
}

func TestDelete_ResultContracts_MalformedYAML(t *testing.T) {
	root := t.TempDir()
	tcID := "tc-ppp66660"

	// Create test case spec
	tcDir := filepath.Join(root, "gtms/test/cases")
	require.NoError(t, os.MkdirAll(tcDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tcDir, tcID+"-bad.md"),
		[]byte("---\ntest_case_id: "+tcID+"\ntitle: Bad YAML\nrequirement: REQ-001\n---\n"), 0644))

	// Create results dir with a malformed YAML file and a valid one
	resultsDir := filepath.Join(root, ".gtms", "results")
	require.NoError(t, os.MkdirAll(resultsDir, 0755))

	// Malformed YAML
	require.NoError(t, os.WriteFile(filepath.Join(resultsDir, "task-badyaml1.handoff.yaml"),
		[]byte("this is not valid yaml: [unclosed"), 0644))

	// Valid contract referencing our TC
	require.NoError(t, os.WriteFile(filepath.Join(resultsDir, "task-goodone1.handoff.yaml"),
		[]byte("task: task-goodone1\ncommand: execute\ntarget: test/acceptance/"+tcID+"-bad.bats\nstatus: complete\n"), 0644))

	result, err := DeleteArtifacts(root, nil, tcID, false, false)
	require.NoError(t, err)

	assert.Equal(t, 1, result.ResultContracts, "should skip malformed YAML and still find valid contract")

	// Verify the malformed file survived (was skipped, not deleted)
	remaining, _ := filepath.Glob(filepath.Join(resultsDir, "*.handoff.yaml"))
	assert.Len(t, remaining, 1, "malformed YAML file should survive")
	assert.Contains(t, remaining[0], "task-badyaml1")
}

// --- ENH-128: Path safety tests ---
//
// AC #5 contract: a record-declared artefact path that resolves outside the
// project-owned allowlist must (a) cause DeleteArtifacts to return a
// *PathSafetyError naming the offending path, and (b) abort the entire
// operation atomically -- the spec, the record, and any "safe sibling"
// artefact must all remain on disk. No partial-deletion permitted.

func TestDelete_PathSafety_ParentTraversal(t *testing.T) {
	root := t.TempDir()
	tcID := "tc-safepath1"

	// Create test case spec
	tcDir := filepath.Join(root, "gtms/test/cases")
	require.NoError(t, os.MkdirAll(tcDir, 0755))
	specPath := filepath.Join(tcDir, tcID+"-safe.md")
	require.NoError(t, os.WriteFile(specPath,
		[]byte("---\ntest_case_id: "+tcID+"\ntitle: Path safety\nrequirement: REQ-001\n---\n"), 0644))

	// Create wiring record with a ".." artefact path (path traversal attempt).
	// CON-023 / ENH-145: the unsafe-artefact check fires on the new wiring
	// surface — the legacy .automation.md path is retired.
	recordDir := filepath.Join(root, "gtms/automation", "wiring")
	require.NoError(t, os.MkdirAll(recordDir, 0755))
	recordPath := filepath.Join(recordDir, tcID+"--bats.wiring.yaml")
	recordContent := "testcase: " + tcID +
		"\ntestcase-hash: 0011223344556677" +
		"\nframework: bats" +
		"\nadapter: bats-runner" +
		"\nartefact: ../../etc/passwd" +
		"\nartefact-hash: aabbccddeeff0011\n"
	require.NoError(t, os.WriteFile(recordPath, []byte(recordContent), 0644))

	_, err := DeleteArtifacts(root, nil, tcID, false, false)
	require.Error(t, err, "unsafe artefact path must produce an error, not silent success")
	assert.True(t, IsPathSafetyError(err), "error should be a *PathSafetyError")
	assert.Contains(t, err.Error(), "../../etc/passwd",
		"refusal message should name the offending path")

	// Atomicity: nothing should have been deleted.
	_, statErr := os.Stat(specPath)
	assert.NoError(t, statErr, "spec must NOT be deleted when an unsafe path is declared")
	_, statErr = os.Stat(recordPath)
	assert.NoError(t, statErr, "record must NOT be deleted when an unsafe path is declared")
}

func TestDelete_PathSafety_AbsolutePathOutside(t *testing.T) {
	root := t.TempDir()
	tcID := "tc-safepath2"

	// Create test case spec
	tcDir := filepath.Join(root, "gtms/test/cases")
	require.NoError(t, os.MkdirAll(tcDir, 0755))
	specPath := filepath.Join(tcDir, tcID+"-abs.md")
	require.NoError(t, os.WriteFile(specPath,
		[]byte("---\ntest_case_id: "+tcID+"\ntitle: Abs path\nrequirement: REQ-001\n---\n"), 0644))

	// Build an absolute path that is GUARANTEED to be outside the project root
	// across platforms: a sibling temp directory.
	outsideAbs := filepath.Join(t.TempDir(), "evil", "file.bats")
	// Normalise to forward slashes so YAML parsing on Windows doesn't choke
	// on backslashes and to keep the test source readable.
	outsideForRecord := filepath.ToSlash(outsideAbs)

	// Create wiring record with an absolute path outside project root.
	// CON-023 / ENH-145: unsafe-artefact check fires on wiring; legacy
	// .automation.md is retired.
	recordDir := filepath.Join(root, "gtms/automation", "wiring")
	require.NoError(t, os.MkdirAll(recordDir, 0755))
	recordPath := filepath.Join(recordDir, tcID+"--bats.wiring.yaml")
	recordContent := "testcase: " + tcID +
		"\ntestcase-hash: 0011223344556677" +
		"\nframework: bats" +
		"\nadapter: bats-runner" +
		"\nartefact: " + outsideForRecord +
		"\nartefact-hash: aabbccddeeff0011\n"
	require.NoError(t, os.WriteFile(recordPath, []byte(recordContent), 0644))

	_, err := DeleteArtifacts(root, nil, tcID, false, false)
	require.Error(t, err, "absolute path outside root must be refused")
	assert.True(t, IsPathSafetyError(err), "error should be a *PathSafetyError")

	// Atomicity: spec and record must both survive the refusal.
	_, statErr := os.Stat(specPath)
	assert.NoError(t, statErr, "spec must NOT be deleted when an unsafe path is declared")
	_, statErr = os.Stat(recordPath)
	assert.NoError(t, statErr, "record must NOT be deleted when an unsafe path is declared")
}

// TestDelete_PathSafety_AtomicAbort_MixedSafeUnsafe is the ENH-128 AC #5 + AC #6
// joint check, mirroring tc-d4e1a7f2: when a record contains a mix of safe and
// unsafe artefact paths, the entire delete must abort BEFORE any os.Remove
// runs. The "safe sibling" artefact must NOT be deleted -- proving that
// validation is a whole-set precheck, not a per-path-as-deletion-progresses
// check.
func TestDelete_PathSafety_AtomicAbort_MixedSafeUnsafe(t *testing.T) {
	root := t.TempDir()
	tcID := "tc-mmmmmmmm"

	// Spec
	tcDir := filepath.Join(root, "gtms/test/cases", "feat")
	require.NoError(t, os.MkdirAll(tcDir, 0755))
	specPath := filepath.Join(tcDir, tcID+"-mixed.md")
	require.NoError(t, os.WriteFile(specPath,
		[]byte("---\ntest_case_id: "+tcID+"\ntitle: Mixed\nrequirement: ENH-128\n---\n"), 0644))

	// Safe in-project artefact
	scriptsDir := filepath.Join(root, "scripts")
	require.NoError(t, os.MkdirAll(scriptsDir, 0755))
	safeScriptPath := filepath.Join(scriptsDir, tcID+"-safe.sh")
	require.NoError(t, os.WriteFile(safeScriptPath, []byte("echo safe"), 0644))

	// Manual record declaring one safe artefact and one unsafe executed_artefact.
	// CON-023 / ENH-145: the wiring schema retires `executed_artefact:`,
	// so the legacy "mixed safe + unsafe per record" case only persists on
	// the manual-record surface (delete.go reads both fields from manual
	// records via the non-strict parseRecordArtefactFields fallback).
	manualDir := filepath.Join(root, "gtms", "manual", "records")
	require.NoError(t, os.MkdirAll(manualDir, 0755))
	recordPath := filepath.Join(manualDir, tcID+"--manual.result.yaml")
	recordContent := "test_case_id: " + tcID +
		"\nframework: manual" +
		"\nartefact: scripts/" + tcID + "-safe.sh" +
		"\nexecuted_artefact: ../outside/payload.txt" +
		"\nresult: pass\n"
	require.NoError(t, os.WriteFile(recordPath, []byte(recordContent), 0644))

	_, err := DeleteArtifacts(root, nil, tcID, false, false)
	require.Error(t, err, "mixed safe+unsafe record must be refused, not partially executed")
	assert.True(t, IsPathSafetyError(err), "error should be a *PathSafetyError")

	// CORE ATOMICITY ASSERTION: safe sibling must still exist with original content.
	content, statErr := os.ReadFile(safeScriptPath)
	require.NoError(t, statErr, "safe artefact must NOT be deleted -- atomicity violated otherwise")
	assert.Equal(t, "echo safe", string(content), "safe artefact content must be unchanged")

	// Spec and record must also survive.
	_, statErr = os.Stat(specPath)
	assert.NoError(t, statErr, "spec must survive atomic abort")
	_, statErr = os.Stat(recordPath)
	assert.NoError(t, statErr, "record must survive atomic abort")
}

func TestDelete_Deduplication(t *testing.T) {
	root := t.TempDir()
	tcID := "tc-dedup001"

	// Create test case spec
	tcDir := filepath.Join(root, "gtms/test/cases")
	require.NoError(t, os.MkdirAll(tcDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tcDir, tcID+"-dedup.md"),
		[]byte("---\ntest_case_id: "+tcID+"\ntitle: Dedup\nrequirement: REQ-001\n---\n"), 0644))

	// Create a single test script
	require.NoError(t, os.MkdirAll(filepath.Join(root, "test", "acceptance"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "test", "acceptance", tcID+"-test.bats"), []byte("# stub"), 0644))

	// Create TWO wiring records that both point to the SAME artefact file
	wiringDir := filepath.Join(root, "gtms/automation", "wiring")
	require.NoError(t, os.MkdirAll(wiringDir, 0755))
	for _, fw := range []string{"bats", "playwright"} {
		content := "testcase: " + tcID +
			"\ntestcase-hash: 0011223344556677" +
			"\nframework: " + fw +
			"\nadapter: " + fw + "-runner" +
			"\nartefact: test/acceptance/" + tcID + "-test.bats" +
			"\nartefact-hash: aabbccddeeff0011\n"
		require.NoError(t, os.WriteFile(filepath.Join(wiringDir, tcID+"--"+fw+".wiring.yaml"), []byte(content), 0644))
	}

	result, err := DeleteArtifacts(root, nil, tcID, false, false)
	require.NoError(t, err)

	assert.Equal(t, 1, result.TestScripts, "duplicate paths should be deduplicated — only delete once")
	assert.Equal(t, 2, result.AutomationRecords, "both wiring records should be deleted")
}

// BUG-073: cross-field duplicate — artefact: and executed_artefact: resolve to
// the same canonical path via different relative forms. The file must be deleted
// once, counted under TestScripts, and not counted under ResultFiles.
func TestDelete_DedupesArtefactAndExecutedArtefactSamePath(t *testing.T) {
	root := t.TempDir()
	tcID := "tc-crossdup1"

	// Create test case spec
	tcDir := filepath.Join(root, "gtms/test/cases")
	require.NoError(t, os.MkdirAll(tcDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tcDir, tcID+"-crossdup.md"),
		[]byte("---\ntest_case_id: "+tcID+"\ntitle: Cross-field dedup\nrequirement: BUG-073\n---\n"), 0644))

	// Create a single physical file that both fields will reference
	scriptsDir := filepath.Join(root, "scripts")
	require.NoError(t, os.MkdirAll(scriptsDir, 0755))
	scriptPath := filepath.Join(scriptsDir, tcID+".sh")
	require.NoError(t, os.WriteFile(scriptPath, []byte("#!/bin/bash\necho test"), 0644))

	// CON-023 / ENH-145: the legacy cross-field dedup case (a single
	// automation record carrying BOTH `artefact:` and `executed_artefact:`)
	// only persists on the manual-record surface post-cutover — wiring
	// has no `executed_artefact:` field. Test cross-field dedup against
	// the manual record, the one surface where it still applies.
	manualDir := filepath.Join(root, "gtms", "manual", "records")
	require.NoError(t, os.MkdirAll(manualDir, 0755))
	recordContent := "test_case_id: " + tcID +
		"\nframework: manual" +
		"\nartefact: scripts/" + tcID + ".sh" +
		"\nexecuted_artefact: ./scripts/" + tcID + ".sh" +
		"\nresult: pass\n"
	require.NoError(t, os.WriteFile(
		filepath.Join(manualDir, tcID+"--manual.result.yaml"),
		[]byte(recordContent), 0644))

	result, err := DeleteArtifacts(root, nil, tcID, false, false)
	require.NoError(t, err, "delete must not fail on cross-field duplicate")

	assert.Equal(t, 1, result.TestScripts, "cross-field duplicate: scripts wins priority, counted once")
	assert.Equal(t, 0, result.ResultFiles, "cross-field duplicate: filtered out of ResultFiles")
	assert.Equal(t, 1, result.AutomationRecords, "the manual record should be deleted")
	assert.Equal(t, 1, result.TestCaseSpecsRemoved, "the spec should be deleted")

	// The file must actually be gone from disk
	_, statErr := os.Stat(scriptPath)
	assert.True(t, os.IsNotExist(statErr), "the physical file must be removed from disk")
}

func TestDelete_MissingArtefactFile(t *testing.T) {
	root := t.TempDir()
	tcID := "tc-missing01"

	// Create test case spec
	tcDir := filepath.Join(root, "gtms/test/cases")
	require.NoError(t, os.MkdirAll(tcDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tcDir, tcID+"-missing.md"),
		[]byte("---\ntest_case_id: "+tcID+"\ntitle: Missing file\nrequirement: REQ-001\n---\n"), 0644))

	// Create wiring record pointing to a non-existent file
	wiringDir := filepath.Join(root, "gtms/automation", "wiring")
	require.NoError(t, os.MkdirAll(wiringDir, 0755))
	recordContent := "testcase: " + tcID +
		"\ntestcase-hash: 0011223344556677" +
		"\nframework: bats" +
		"\nadapter: bats-runner" +
		"\nartefact: test/acceptance/" + tcID + "-test.bats" +
		"\nartefact-hash: aabbccddeeff0011\n"
	require.NoError(t, os.WriteFile(filepath.Join(wiringDir, tcID+"--bats.wiring.yaml"), []byte(recordContent), 0644))

	// The artefact file does NOT exist — delete should not error
	result, err := DeleteArtifacts(root, nil, tcID, false, false)
	require.NoError(t, err)

	assert.Equal(t, 0, result.TestScripts, "missing artefact file should be silently skipped")
	assert.Equal(t, 1, result.AutomationRecords, "the wiring record should still be deleted")
}

func TestDelete_NoRecords_CleanState(t *testing.T) {
	root := t.TempDir()
	tcID := "tc-clean001"

	// Create test case spec only — no automation records, no scripts, no tasks, no contracts
	tcDir := filepath.Join(root, "gtms/test/cases")
	require.NoError(t, os.MkdirAll(tcDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tcDir, tcID+"-clean.md"),
		[]byte("---\ntest_case_id: "+tcID+"\ntitle: Clean\nrequirement: REQ-001\n---\n"), 0644))

	result, err := DeleteArtifacts(root, nil, tcID, false, false)
	require.NoError(t, err)

	assert.Equal(t, 1, result.TestCasesProcessed)
	assert.Equal(t, 1, result.TestCaseSpecsRemoved)
	assert.Equal(t, 0, result.AutomationRecords)
	assert.Equal(t, 0, result.TestScripts)
	assert.Equal(t, 0, result.ResultFiles)
	assert.Equal(t, 0, result.TaskFiles)
	assert.Equal(t, 0, result.ResultContracts)
}

// --- Path safety unit tests ---
// BUG-057: these now call pathsafe.ResolveUnderRoot (lifted from the former
// local safeResolvePath) and pathsafe.IsWithinRoot.

func TestSafeResolvePath_WithinRoot(t *testing.T) {
	root := t.TempDir()

	// Create a file within root
	require.NoError(t, os.MkdirAll(filepath.Join(root, "test"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "test", "file.txt"), []byte("ok"), 0644))

	resolved, _, err := pathsafe.ResolveUnderRoot(root, "test/file.txt")
	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(resolved))
}

func TestSafeResolvePath_ParentTraversal(t *testing.T) {
	root := t.TempDir()

	_, _, err := pathsafe.ResolveUnderRoot(root, "../../../etc/passwd")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "resolves outside project root")
}

func TestSafeResolvePath_EmptyPath(t *testing.T) {
	root := t.TempDir()

	_, _, err := pathsafe.ResolveUnderRoot(root, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "empty artefact path")
}

func TestIsWithinRoot(t *testing.T) {
	sep := string(filepath.Separator)
	root := sep + "a" + sep + "b"
	assert.True(t, pathsafe.IsWithinRoot(root+sep+"c", root))
	assert.True(t, pathsafe.IsWithinRoot(root, root))
	assert.False(t, pathsafe.IsWithinRoot(sep+"a"+sep+"bc", root))
	assert.False(t, pathsafe.IsWithinRoot(sep+"other"+sep+"path", root))
}
