package reader

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- readManualDriftDiagnostics unit tests ---

func TestReadManualDriftDiagnostics_DriftedFile(t *testing.T) {
	root := t.TempDir()
	setupMinimalProject(t, root)

	tcID := "tc-dr000001"
	writeManualResultFile(t, root, tcID, true)

	ar := automationFrontmatter{
		Framework: "manual",
		Artefact:  fmt.Sprintf("gtms/manual/records/%s--manual.result.yaml", tcID),
	}

	drift := readManualDriftDiagnostics(root, ar)
	assert.True(t, drift.DriftDetected)
	assert.Equal(t, "2026-05-15T07:43:25Z", drift.DriftDetectedAt)
	assert.Equal(t, "9839fb78189f3f25", drift.TestCaseHashAtExecute)
}

func TestReadManualDriftDiagnostics_NonDriftedFile(t *testing.T) {
	root := t.TempDir()
	setupMinimalProject(t, root)

	tcID := "tc-dr000002"
	writeManualResultFile(t, root, tcID, false)

	ar := automationFrontmatter{
		Framework: "manual",
		Artefact:  fmt.Sprintf("gtms/manual/records/%s--manual.result.yaml", tcID),
	}

	drift := readManualDriftDiagnostics(root, ar)
	assert.False(t, drift.DriftDetected)
	assert.Empty(t, drift.DriftDetectedAt)
	assert.Empty(t, drift.TestCaseHashAtExecute)
}

func TestReadManualDriftDiagnostics_NonManualFramework(t *testing.T) {
	root := t.TempDir()
	setupMinimalProject(t, root)

	tcID := "tc-dr000003"
	writeManualResultFile(t, root, tcID, true)

	ar := automationFrontmatter{
		Framework: "bats",
		Artefact:  fmt.Sprintf("gtms/manual/records/%s--manual.result.yaml", tcID),
	}

	drift := readManualDriftDiagnostics(root, ar)
	assert.False(t, drift.DriftDetected)
}

func TestReadManualDriftDiagnostics_MissingFile(t *testing.T) {
	root := t.TempDir()
	setupMinimalProject(t, root)

	ar := automationFrontmatter{
		Framework: "manual",
		Artefact:  "gtms/manual/records/tc-nonexist--manual.result.yaml",
	}

	drift := readManualDriftDiagnostics(root, ar)
	assert.False(t, drift.DriftDetected)
}

func TestReadManualDriftDiagnostics_EmptyArtefact(t *testing.T) {
	root := t.TempDir()
	ar := automationFrontmatter{
		Framework: "manual",
		Artefact:  "",
	}

	drift := readManualDriftDiagnostics(root, ar)
	assert.False(t, drift.DriftDetected)
}

// --- PipelineStatus integration tests for drift ---

func TestPipelineStatus_DriftDetected(t *testing.T) {
	root := t.TempDir()
	setupMinimalProject(t, root)

	tcID := "tc-dr000010"
	createTestCase(t, root, tcID, "Drifted TC")
	writeAutomationRecord(t, root, tcID, "manual", "pass", "accepted")
	writeManualResultFile(t, root, tcID, true)

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	assert.True(t, entries[0].DriftDetected)
	assert.Equal(t, "2026-05-15T07:43:25Z", entries[0].DriftDetectedAt)
	assert.Equal(t, "9839fb78189f3f25", entries[0].TestCaseHashAtExecute)
}

func TestPipelineStatus_DriftNotDetected(t *testing.T) {
	root := t.TempDir()
	setupMinimalProject(t, root)

	tcID := "tc-dr000011"
	createTestCase(t, root, tcID, "Non-drifted TC")
	writeAutomationRecord(t, root, tcID, "manual", "pass", "accepted")
	writeManualResultFile(t, root, tcID, false)

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	assert.False(t, entries[0].DriftDetected)
	assert.Empty(t, entries[0].DriftDetectedAt)
	assert.Empty(t, entries[0].TestCaseHashAtExecute)
}

func TestPipelineDetail_DriftDetected(t *testing.T) {
	root := t.TempDir()
	setupMinimalProject(t, root)

	tcID := "tc-dr000012"
	createTestCase(t, root, tcID, "Detail drifted TC")
	writeAutomationRecord(t, root, tcID, "manual", "pass", "accepted")
	writeManualResultFile(t, root, tcID, true)

	detail, err := PipelineDetail(root, tcID, "", false)
	require.NoError(t, err)

	assert.True(t, detail.DriftDetected)
	assert.Equal(t, "2026-05-15T07:43:25Z", detail.DriftDetectedAt)
	assert.Equal(t, "9839fb78189f3f25", detail.TestCaseHashAtExecute)
}

func TestMap_DriftDetected(t *testing.T) {
	root := t.TempDir()
	setupMinimalProject(t, root)

	tcID := "tc-dr000013"
	createTestCase(t, root, tcID, "Map drifted TC")
	writeAutomationRecord(t, root, tcID, "manual", "pass", "accepted")
	writeManualResultFile(t, root, tcID, true)

	report, err := Map(root, nil, "", false)
	require.NoError(t, err)

	// TC has no requirement → unlinked
	require.Len(t, report.Unlinked, 1)
	assert.True(t, report.Unlinked[0].DriftDetected)
	assert.Equal(t, "2026-05-15T07:43:25Z", report.Unlinked[0].DriftDetectedAt)
	assert.Equal(t, "9839fb78189f3f25", report.Unlinked[0].TestCaseHashAtExecute)
}

func TestGaps_DriftDetected_Category(t *testing.T) {
	root := t.TempDir()
	setupMinimalProject(t, root)

	tcID := "tc-dr000014"
	createTestCase(t, root, tcID, "Gaps drifted TC")
	writeAutomationRecord(t, root, tcID, "manual", "pass", "accepted")
	writeManualResultFile(t, root, tcID, true)

	report, err := Gaps(root, nil, "", false)
	require.NoError(t, err)

	require.Len(t, report.DriftDetected, 1)
	assert.Equal(t, tcID, report.DriftDetected[0].ID)
	// TotalGaps includes DriftDetected
	assert.Greater(t, report.TotalGaps(), 0)
}

func TestGaps_DriftDetected_DoubleCounts(t *testing.T) {
	// A TC with only a manual record (NoAutomation) that is also drifted
	// should appear in both NoAutomation and DriftDetected.
	root := t.TempDir()
	setupMinimalProject(t, root)

	tcID := "tc-dr000015"
	createTestCase(t, root, tcID, "Double-counted TC")
	writeAutomationRecord(t, root, tcID, "manual", "pass", "accepted")
	writeManualResultFile(t, root, tcID, true)

	report, err := Gaps(root, nil, "", false)
	require.NoError(t, err)

	// Should be in NoAutomation (manual-only counts as not-automated)
	noAutoIDs := make(map[string]bool)
	for _, e := range report.NoAutomation {
		noAutoIDs[e.ID] = true
	}
	assert.True(t, noAutoIDs[tcID], "TC should appear in NoAutomation")

	// Should also be in DriftDetected
	driftIDs := make(map[string]bool)
	for _, e := range report.DriftDetected {
		driftIDs[e.ID] = true
	}
	assert.True(t, driftIDs[tcID], "TC should appear in DriftDetected")

	// TotalGaps should count both
	assert.Equal(t, len(report.NoAutomation)+len(report.DriftDetected), report.TotalGaps())
}

func TestGapsFolderSummary_DriftCount(t *testing.T) {
	root := t.TempDir()
	setupMinimalProject(t, root)

	folder := filepath.Join(root, "gtms", "test", "cases", "driftfolder")
	require.NoError(t, os.MkdirAll(folder, 0755))

	tcID := "tc-dr000016"
	createTestCaseInDir(t, folder, tcID, "Drifted in folder")
	writeAutomationRecord(t, root, tcID, "manual", "pass", "accepted")
	writeManualResultFile(t, root, tcID, true)

	// Also a non-drifted TC in the same folder
	tcID2 := "tc-dr000017"
	createTestCaseInDir(t, folder, tcID2, "Non-drifted in folder")
	writeAutomationRecord(t, root, tcID2, "manual", "pass", "accepted")
	writeManualResultFile(t, root, tcID2, false)

	summaries, err := GapsFolderSummary(root, "")
	require.NoError(t, err)
	require.Len(t, summaries, 1)

	assert.Equal(t, 1, summaries[0].DriftDetected)
}

func TestSelectedRecordRule_NonManualSelected(t *testing.T) {
	// TC has both a manual record (drifted) and a bats record.
	// When bats is selected (via default framework), drift must NOT appear.
	root := t.TempDir()
	setupMinimalProject(t, root)

	tcID := "tc-dr000018"
	createTestCase(t, root, tcID, "Dual-framework TC")
	writeAutomationRecord(t, root, tcID, "manual", "pass", "accepted")
	writeAutomationRecord(t, root, tcID, "bats", "pass", "accepted")
	writeManualResultFile(t, root, tcID, true)

	// Request bats framework → bats record is selected → no drift
	entries, err := PipelineStatus(root, nil, "bats", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	assert.False(t, entries[0].DriftDetected)
	assert.Empty(t, entries[0].DriftDetectedAt)
}

// --- ENH-117: isStaleTestCaseHash unit tests ---

func TestIsStaleTestCaseHash_MatchingHash(t *testing.T) {
	root := t.TempDir()
	setupMinimalProject(t, root)

	tcID := "tc-hs000001"
	createTestCase(t, root, tcID, "Matching hash TC")

	// Compute the actual hash of the test case file
	casesDir := filepath.Join(root, "gtms", "test", "cases")
	entries, _ := os.ReadDir(casesDir)
	var tcFile string
	for _, e := range entries {
		if !e.IsDir() && len(e.Name()) > len(tcID) && e.Name()[:len(tcID)] == tcID {
			tcFile = filepath.Join(casesDir, e.Name())
			break
		}
	}
	require.NotEmpty(t, tcFile, "test case file should exist")

	// Use pipeline.HashFile to get the correct hash
	hash := computeTestCaseHash(t, tcFile)

	ar := automationFrontmatter{
		TestCase:     tcID,
		Framework:    "bats",
		TestCaseHash: hash,
	}

	assert.False(t, isStaleTestCaseHash(root, ar), "hash matches — should not be stale")
}

func TestIsStaleTestCaseHash_DifferentHash(t *testing.T) {
	root := t.TempDir()
	setupMinimalProject(t, root)

	tcID := "tc-hs000002"
	createTestCase(t, root, tcID, "Stale hash TC")

	ar := automationFrontmatter{
		TestCase:     tcID,
		Framework:    "bats",
		TestCaseHash: "0000000000000000", // definitely wrong
	}

	assert.True(t, isStaleTestCaseHash(root, ar), "hash differs — should be stale")
}

func TestIsStaleTestCaseHash_EmptyHash_Legacy(t *testing.T) {
	root := t.TempDir()
	setupMinimalProject(t, root)

	tcID := "tc-hs000003"
	createTestCase(t, root, tcID, "Legacy TC no hash")

	ar := automationFrontmatter{
		TestCase:     tcID,
		Framework:    "bats",
		TestCaseHash: "", // legacy record — no hash stored
	}

	assert.False(t, isStaleTestCaseHash(root, ar), "empty hash (legacy) — should not be stale")
}

func TestIsStaleTestCaseHash_MissingSpec(t *testing.T) {
	root := t.TempDir()
	setupMinimalProject(t, root)

	// TC spec does not exist — only the automation record has a hash
	ar := automationFrontmatter{
		TestCase:     "tc-hs000004",
		Framework:    "bats",
		TestCaseHash: "abcdef1234567890",
	}

	assert.False(t, isStaleTestCaseHash(root, ar), "missing spec — should not be stale")
}

func TestPipelineStatus_StaleTestCaseHash(t *testing.T) {
	root := t.TempDir()
	setupMinimalProject(t, root)

	tcID := "tc-hs000010"
	createTestCase(t, root, tcID, "Status stale hash TC")
	writeAutomationRecordWithTestCaseHash(t, root, tcID, "bats", "0000000000000000")

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	assert.True(t, entries[0].StaleTestCaseHash, "status should report stale testcase-hash")
}

func TestPipelineStatus_NoStaleTestCaseHash_WhenMatches(t *testing.T) {
	root := t.TempDir()
	setupMinimalProject(t, root)

	tcID := "tc-hs000011"
	createTestCase(t, root, tcID, "Status clean hash TC")

	// Get the actual hash
	casesDir := filepath.Join(root, "gtms", "test", "cases")
	var tcFile string
	entries2, _ := os.ReadDir(casesDir)
	for _, e := range entries2 {
		if !e.IsDir() && len(e.Name()) > len(tcID) && e.Name()[:len(tcID)] == tcID {
			tcFile = filepath.Join(casesDir, e.Name())
			break
		}
	}
	hash := computeTestCaseHash(t, tcFile)
	writeAutomationRecordWithTestCaseHash(t, root, tcID, "bats", hash)

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	assert.False(t, entries[0].StaleTestCaseHash, "status should not report stale when hash matches")
}

func TestGaps_StaleTestCaseHash_Category(t *testing.T) {
	root := t.TempDir()
	setupMinimalProject(t, root)

	tcID := "tc-hs000020"
	createTestCase(t, root, tcID, "Gaps stale hash TC")
	writeAutomationRecordWithTestCaseHash(t, root, tcID, "bats", "0000000000000000")

	report, err := Gaps(root, nil, "", false)
	require.NoError(t, err)

	require.Len(t, report.StaleTestCaseHash, 1)
	assert.Equal(t, tcID, report.StaleTestCaseHash[0].ID)
}

// --- Helper ---

// computeTestCaseHash computes the pipeline hash for a test case file.
func computeTestCaseHash(t *testing.T, path string) string {
	t.Helper()
	hash, err := pipeline.HashFile(path)
	require.NoError(t, err)
	return hash
}

// writeAutomationRecordWithTestCaseHash seeds a wiring record for the
// given TC × framework with the supplied testcase-hash. CON-023 / ENH-145:
// the legacy gtms/automation/records/*.automation.md file is retired —
// the wiring record at gtms/automation/wiring/ is now the source.
func writeAutomationRecordWithTestCaseHash(t *testing.T, root, tcID, framework, testCaseHash string) {
	t.Helper()
	wDir := filepath.Join(root, "gtms", "automation", "wiring")
	require.NoError(t, os.MkdirAll(wDir, 0755))

	path := filepath.Join(wDir, fmt.Sprintf("%s--%s.wiring.yaml", tcID, framework))
	content := fmt.Sprintf(`testcase: %s
testcase-hash: %s
framework: %s
adapter: %s-runner
artefact: test/acceptance/%s.bats
artefact-hash: aabbccddeeff0011
`, tcID, testCaseHash, framework, framework, tcID)
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
}

// writeManualResultFile creates a manual result YAML file with or without drift fields.
func writeManualResultFile(t *testing.T, root, tcID string, drifted bool) {
	t.Helper()

	manualDir := filepath.Join(root, "gtms", "manual", "records")
	require.NoError(t, os.MkdirAll(manualDir, 0755))

	filename := fmt.Sprintf("%s--manual.result.yaml", tcID)
	path := filepath.Join(manualDir, filename)

	content := fmt.Sprintf(`test_case_id: %s
test_case_hash: abcdef0123456789
framework: manual
result: pass
branch: main
`, tcID)

	if drifted {
		content += `
drift-detected: true
drift-detected-at: 2026-05-15T07:43:25Z
test_case_hash_at_execute: 9839fb78189f3f25
`
	}

	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
}
