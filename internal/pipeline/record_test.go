package pipeline

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateAutomationRecord_NewRecord(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation/records"), 0755))

	// Create artefact file for hash computation
	artefactDir := filepath.Join(root, "tests")
	require.NoError(t, os.MkdirAll(artefactDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(artefactDir, "sample.spec.ts"), []byte("test content"), 0644))

	opts := RecordOptions{
		TestCase:      "tc-abc12345",
		Framework:     "playwright",
		Artefact:      "tests/sample.spec.ts",
		Adapter:       "manual-link",
		LastDevResult: "linked",
		Branch:        "feature/link",
	}

	err := CreateAutomationRecord(root, opts)
	require.NoError(t, err)

	// Read back the record
	recordPath := filepath.Join(root, "gtms/automation/records", "tc-abc12345--playwright.automation.md")
	record, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)

	assert.Equal(t, "tc-abc12345", record.TestCase)
	assert.Equal(t, "playwright", record.Framework)
	assert.Equal(t, "developed", record.Status)
	assert.Equal(t, "tests/sample.spec.ts", record.Artefact)
	assert.Equal(t, "manual-link", record.Adapter)
	assert.Equal(t, "linked", record.LastDevResult)
	assert.Equal(t, 1, record.Attempts)
	assert.Equal(t, 1, record.Cycle)
	assert.Equal(t, "feature/link", record.Branch)
	assert.NotEmpty(t, record.ArtefactHash, "artefact hash should be computed")

	// Execute fields should be empty (zero values)
	assert.Empty(t, record.Result)
	assert.Empty(t, record.ExecutedArtefact)
	assert.Empty(t, record.ExecutedAt)
	assert.Empty(t, record.ResultsFile)
	assert.Empty(t, record.Notes)
	assert.Empty(t, record.NotesSpill)
	assert.Empty(t, record.Defect)
}

func TestCreateAutomationRecord_ExistingWithoutForce(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation/records"), 0755))

	// Create artefact file
	require.NoError(t, os.MkdirAll(filepath.Join(root, "tests"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "tests/sample.spec.ts"), []byte("test"), 0644))

	opts := RecordOptions{
		TestCase:      "tc-abc12345",
		Framework:     "playwright",
		Artefact:      "tests/sample.spec.ts",
		Adapter:       "manual-link",
		LastDevResult: "linked",
	}

	// First create succeeds
	err := CreateAutomationRecord(root, opts)
	require.NoError(t, err)

	// Second create without Force should fail
	err = CreateAutomationRecord(root, opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
	assert.Contains(t, err.Error(), "--force")
}

func TestCreateAutomationRecord_CorruptedExistingRecordWithoutForceErrors(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation/records"), 0755))

	require.NoError(t, os.MkdirAll(filepath.Join(root, "tests"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "tests/sample.spec.ts"), []byte("test"), 0644))

	recordPath := filepath.Join(root, "gtms/automation/records", "tc-abc12345--playwright.automation.md")
	original := []byte("---\ntestcase: tc-abc12345\nframework: playwright\ncycle: [not-a-number]\n---\n")
	require.NoError(t, os.WriteFile(recordPath, original, 0644))

	opts := RecordOptions{
		TestCase:      "tc-abc12345",
		Framework:     "playwright",
		Artefact:      "tests/sample.spec.ts",
		Adapter:       "manual-link",
		LastDevResult: "linked",
	}

	err := CreateAutomationRecord(root, opts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unreadable")
	assert.Contains(t, err.Error(), recordPath)

	after, readErr := os.ReadFile(recordPath)
	require.NoError(t, readErr)
	assert.Equal(t, original, after)
}

func TestCreateAutomationRecord_ForceOverwrite(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation/records"), 0755))

	// Create artefact file
	require.NoError(t, os.MkdirAll(filepath.Join(root, "tests"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "tests/sample.spec.ts"), []byte("test"), 0644))

	// Seed an existing record with execute-stage fields populated
	recordPath := filepath.Join(root, "gtms/automation/records", "tc-abc12345--playwright.automation.md")
	existing := &AutomationRecord{
		RecordCommon: RecordCommon{
			TestCase:   "tc-abc12345",
			Framework:  "playwright",
			Status:     "developed",
			Result:     "pass",
			ExecutedAt: "2026-04-20T10:00:00Z",
			Notes:      "old log",
			Defect:     []string{"BUG-999"},
		},
		Artefact:         "tests/old.spec.ts",
		Adapter:          "old-adapter",
		LastDevResult:    "pass",
		Cycle:            1,
		ExecutedArtefact: "task-old",
		ResultsFile:      ".gtms/results/old.handoff.yaml",
		NotesSpill:       ".gtms/logs/spill.txt",
		Summary:          "old summary",
	}
	require.NoError(t, WriteAutomationRecord(recordPath, existing))

	// Force overwrite
	opts := RecordOptions{
		TestCase:      "tc-abc12345",
		Framework:     "playwright",
		Artefact:      "tests/sample.spec.ts",
		Adapter:       "manual-link",
		LastDevResult: "linked",
		Force:         true,
	}

	err := CreateAutomationRecord(root, opts)
	require.NoError(t, err)

	// Read back and verify
	record, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)

	assert.Equal(t, "manual-link", record.Adapter)
	assert.Equal(t, "linked", record.LastDevResult)
	assert.Equal(t, "tests/sample.spec.ts", record.Artefact)
	assert.Equal(t, 2, record.Cycle, "cycle should increment from 1 to 2")

	// All stale execute fields should be cleared
	assert.Empty(t, record.Result)
	assert.Empty(t, record.ExecutedArtefact)
	assert.Empty(t, record.ExecutedAt)
	assert.Empty(t, record.ResultsFile)
	assert.Empty(t, record.Notes)
	assert.Empty(t, record.NotesSpill)
	assert.Empty(t, record.Summary)
	assert.Empty(t, record.Defect)
}

func TestCreateAutomationRecord_CorruptedExistingRecordWithForceOverwrites(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation/records"), 0755))

	require.NoError(t, os.MkdirAll(filepath.Join(root, "tests"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "tests/sample.spec.ts"), []byte("test"), 0644))

	recordPath := filepath.Join(root, "gtms/automation/records", "tc-abc12345--playwright.automation.md")
	original := []byte("---\ntestcase: tc-abc12345\nframework: playwright\ncycle: [not-a-number]\n---\n")
	require.NoError(t, os.WriteFile(recordPath, original, 0644))

	opts := RecordOptions{
		TestCase:      "tc-abc12345",
		Framework:     "playwright",
		Artefact:      "tests/sample.spec.ts",
		Adapter:       "manual-link",
		LastDevResult: "linked",
		Force:         true,
	}

	err := CreateAutomationRecord(root, opts)
	require.NoError(t, err)

	after, readErr := os.ReadFile(recordPath)
	require.NoError(t, readErr)
	assert.NotEqual(t, original, after)

	record, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)
	assert.Equal(t, 1, record.Cycle)
	assert.Equal(t, "manual-link", record.Adapter)
	assert.Equal(t, "linked", record.LastDevResult)
}

func TestCreateAutomationRecord_CycleIncrement(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation/records"), 0755))

	// Create artefact file
	require.NoError(t, os.MkdirAll(filepath.Join(root, "tests"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "tests/sample.spec.ts"), []byte("test"), 0644))

	opts := RecordOptions{
		TestCase:      "tc-abc12345",
		Framework:     "playwright",
		Artefact:      "tests/sample.spec.ts",
		Adapter:       "manual-link",
		LastDevResult: "linked",
		Force:         true,
	}

	// First create: cycle=1
	err := CreateAutomationRecord(root, opts)
	require.NoError(t, err)

	recordPath := filepath.Join(root, "gtms/automation/records", "tc-abc12345--playwright.automation.md")
	record, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)
	assert.Equal(t, 1, record.Cycle)

	// Second create (force): cycle=2
	err = CreateAutomationRecord(root, opts)
	require.NoError(t, err)
	record, err = ReadAutomationRecord(recordPath)
	require.NoError(t, err)
	assert.Equal(t, 2, record.Cycle)

	// Third create (force): cycle=3
	err = CreateAutomationRecord(root, opts)
	require.NoError(t, err)
	record, err = ReadAutomationRecord(recordPath)
	require.NoError(t, err)
	assert.Equal(t, 3, record.Cycle)
}

func TestCreateAutomationRecord_ArtefactPathNormalised(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation/records"), 0755))

	// Create artefact file at a path with backslashes on Windows
	require.NoError(t, os.MkdirAll(filepath.Join(root, "tests", "sub"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "tests", "sub", "sample.spec.ts"), []byte("test"), 0644))

	opts := RecordOptions{
		TestCase:      "tc-abc12345",
		Framework:     "playwright",
		Artefact:      filepath.Join("tests", "sub", "sample.spec.ts"), // OS-native separators
		Adapter:       "manual-link",
		LastDevResult: "linked",
	}

	err := CreateAutomationRecord(root, opts)
	require.NoError(t, err)

	recordPath := filepath.Join(root, "gtms/automation/records", "tc-abc12345--playwright.automation.md")
	record, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)

	// Artefact path should always use forward slashes
	assert.Equal(t, "tests/sub/sample.spec.ts", record.Artefact)
}

func TestCreateAutomationRecord_ArtefactHash(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation/records"), 0755))

	// Create artefact file with known content
	require.NoError(t, os.MkdirAll(filepath.Join(root, "tests"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "tests/sample.spec.ts"), []byte("deterministic content"), 0644))

	opts := RecordOptions{
		TestCase:      "tc-abc12345",
		Framework:     "playwright",
		Artefact:      "tests/sample.spec.ts",
		Adapter:       "manual-link",
		LastDevResult: "linked",
	}

	err := CreateAutomationRecord(root, opts)
	require.NoError(t, err)

	recordPath := filepath.Join(root, "gtms/automation/records", "tc-abc12345--playwright.automation.md")
	record, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)

	assert.NotEmpty(t, record.ArtefactHash)
	assert.Len(t, record.ArtefactHash, 16, "hash should be 16 hex chars (8 bytes)")
}

func TestCreateAutomationRecord_FieldDefaults(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation/records"), 0755))

	require.NoError(t, os.MkdirAll(filepath.Join(root, "tests"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "tests/sample.spec.ts"), []byte("test"), 0644))

	opts := RecordOptions{
		TestCase:      "tc-abc12345",
		Framework:     "playwright",
		Artefact:      "tests/sample.spec.ts",
		Adapter:       "manual-link",
		LastDevResult: "linked",
	}

	err := CreateAutomationRecord(root, opts)
	require.NoError(t, err)

	recordPath := filepath.Join(root, "gtms/automation/records", "tc-abc12345--playwright.automation.md")
	record, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)

	assert.Equal(t, "developed", record.Status)
	assert.Equal(t, 1, record.Attempts)
	assert.Equal(t, 1, record.Cycle)
}

func TestCreateAutomationRecord_CreatesDirectory(t *testing.T) {
	root := t.TempDir()
	// Do NOT pre-create the records directory

	require.NoError(t, os.MkdirAll(filepath.Join(root, "tests"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "tests/sample.spec.ts"), []byte("test"), 0644))

	opts := RecordOptions{
		TestCase:      "tc-abc12345",
		Framework:     "playwright",
		Artefact:      "tests/sample.spec.ts",
		Adapter:       "manual-link",
		LastDevResult: "linked",
	}

	err := CreateAutomationRecord(root, opts)
	require.NoError(t, err)

	// Record should be created even though directory didn't exist
	recordPath := filepath.Join(root, "gtms/automation/records", "tc-abc12345--playwright.automation.md")
	_, err = os.Stat(recordPath)
	assert.NoError(t, err, "record file should be created")
}

// BUG-058: Verify CreateAutomationRecord rejects identifiers containing path
// separators, traversal sequences, or other unsafe characters.
func TestCreateAutomationRecord_RejectsPathTraversal(t *testing.T) {
	root := t.TempDir()

	badTestCases := []struct {
		name      string
		testCase  string
		framework string
	}{
		{"forward slash in test case", "x/y", "bats"},
		{"backslash in test case", "x\\y", "bats"},
		{"dotdot in test case", "../escape", "bats"},
		{"embedded dotdot in test case", "tc-..abc", "bats"},
		{"empty test case", "", "bats"},
		{"forward slash in framework", "tc-abc12345", "x/y"},
		{"backslash in framework", "tc-abc12345", "x\\y"},
		{"dotdot in framework", "tc-abc12345", "../../escape"},
		{"empty framework", "tc-abc12345", ""},
	}

	for _, tt := range badTestCases {
		t.Run(tt.name, func(t *testing.T) {
			opts := RecordOptions{
				TestCase:      tt.testCase,
				Framework:     tt.framework,
				Artefact:      "tests/sample.spec.ts",
				Adapter:       "manual-link",
				LastDevResult: "linked",
			}

			err := CreateAutomationRecord(root, opts)
			require.Error(t, err, "expected rejection for %s", tt.name)

			// Verify no file was written outside the intended directory
			recordsDir := filepath.Join(root, "gtms", "automation", "records")
			entries, _ := os.ReadDir(recordsDir)
			assert.Empty(t, entries, "no record should be created for unsafe input")
		})
	}
}

// --- ENH-117: testcase-hash on CreateAutomationRecord ---

func TestCreateAutomationRecord_WritesTestCaseHash(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation/records"), 0755))

	// Create a test case spec
	casesDir := filepath.Join(root, "gtms", "cases")
	require.NoError(t, os.MkdirAll(casesDir, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(casesDir, "tc-linkhash-test.md"),
		[]byte("---\ntest_case_id: tc-linkhash\ntitle: test\n---\nContent for hashing\n"), 0644))

	// Create artefact
	artefactDir := filepath.Join(root, "tests")
	require.NoError(t, os.MkdirAll(artefactDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(artefactDir, "tc-linkhash.bats"), []byte("test"), 0644))

	opts := RecordOptions{
		TestCase:      "tc-linkhash",
		Framework:     "bats",
		Artefact:      "tests/tc-linkhash.bats",
		Adapter:       "manual-link",
		LastDevResult: "linked",
	}

	err := CreateAutomationRecord(root, opts)
	require.NoError(t, err)

	recordPath := filepath.Join(root, "gtms/automation/records/tc-linkhash--bats.automation.md")
	record, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)

	assert.NotEmpty(t, record.TestCaseHash, "testcase-hash should be populated via CreateAutomationRecord")
	assert.Len(t, record.TestCaseHash, 16, "testcase-hash should be 16 hex chars")
	assert.Regexp(t, `^[0-9a-f]{16}$`, record.TestCaseHash)
}

func TestCreateAutomationRecord_SubfolderSpec_WritesTestCaseHash(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/automation/records"), 0755))

	// Create a test case spec under a subfolder
	subDir := filepath.Join(root, "gtms", "cases", "my-feature")
	require.NoError(t, os.MkdirAll(subDir, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(subDir, "tc-subfold-test.md"),
		[]byte("---\ntest_case_id: tc-subfold\ntitle: subfolder test\n---\nSubfolder content\n"), 0644))

	// Create artefact
	artefactDir := filepath.Join(root, "tests")
	require.NoError(t, os.MkdirAll(artefactDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(artefactDir, "tc-subfold.bats"), []byte("test"), 0644))

	opts := RecordOptions{
		TestCase:      "tc-subfold",
		Framework:     "bats",
		Artefact:      "tests/tc-subfold.bats",
		Adapter:       "manual-link",
		LastDevResult: "linked",
	}

	err := CreateAutomationRecord(root, opts)
	require.NoError(t, err)

	recordPath := filepath.Join(root, "gtms/automation/records/tc-subfold--bats.automation.md")
	record, err := ReadAutomationRecord(recordPath)
	require.NoError(t, err)

	assert.NotEmpty(t, record.TestCaseHash, "testcase-hash should be populated for subfolder-scoped spec")
	assert.Len(t, record.TestCaseHash, 16)
}
