package reader

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- deriveManualCoverage unit tests ---

func TestDeriveManualCoverage_NoManualRecord(t *testing.T) {
	records := []automationFrontmatter{
		{Framework: "bats", Result: "pass"},
	}
	assert.Equal(t, "", deriveManualCoverage(records))
}

func TestDeriveManualCoverage_NoRecords(t *testing.T) {
	assert.Equal(t, "", deriveManualCoverage(nil))
	assert.Equal(t, "", deriveManualCoverage([]automationFrontmatter{}))
}

func TestDeriveManualCoverage_Prepared(t *testing.T) {
	records := []automationFrontmatter{
		{Framework: "manual", Result: ""},
	}
	assert.Equal(t, "prepared", deriveManualCoverage(records))
}

func TestDeriveManualCoverage_RecordedPass(t *testing.T) {
	records := []automationFrontmatter{
		{Framework: "manual", Result: "pass"},
	}
	assert.Equal(t, "recorded", deriveManualCoverage(records))
}

func TestDeriveManualCoverage_RecordedFail(t *testing.T) {
	records := []automationFrontmatter{
		{Framework: "manual", Result: "fail"},
	}
	assert.Equal(t, "recorded", deriveManualCoverage(records))
}

func TestDeriveManualCoverage_RecordedSkipped(t *testing.T) {
	records := []automationFrontmatter{
		{Framework: "manual", Result: "skipped"},
	}
	assert.Equal(t, "recorded", deriveManualCoverage(records))
}

func TestDeriveManualCoverage_ManualAndNonManual(t *testing.T) {
	// TC has both a manual record (prepared) and a bats record.
	// ManualCoverage should be "prepared" because the manual record is empty.
	records := []automationFrontmatter{
		{Framework: "bats", Result: "pass"},
		{Framework: "manual", Result: ""},
	}
	assert.Equal(t, "prepared", deriveManualCoverage(records))
}

func TestDeriveManualCoverage_ManualRecordedAndNonManual(t *testing.T) {
	// TC has both a manual record (recorded) and a bats record.
	// ManualCoverage should be "recorded" regardless of the bats record.
	records := []automationFrontmatter{
		{Framework: "bats", Result: "pass"},
		{Framework: "manual", Result: "pass"},
	}
	assert.Equal(t, "recorded", deriveManualCoverage(records))
}

// --- PipelineStatus integration tests for ManualCoverage ---

func TestPipelineStatus_ManualCoverage_Prepared(t *testing.T) {
	root := t.TempDir()
	setupMinimalProject(t, root)

	// Create a test case
	createTestCase(t, root, "tc-mc000001", "Manual prepared TC")

	// Create a manual automation record with empty result (prepared)
	writeAutomationRecord(t, root, "tc-mc000001", "manual", "", "accepted")

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "prepared", entries[0].ManualCoverage)
}

func TestPipelineStatus_ManualCoverage_Recorded(t *testing.T) {
	root := t.TempDir()
	setupMinimalProject(t, root)

	createTestCase(t, root, "tc-mc000002", "Manual recorded TC")
	writeAutomationRecord(t, root, "tc-mc000002", "manual", "pass", "accepted")

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "recorded", entries[0].ManualCoverage)
}

func TestPipelineStatus_ManualCoverage_NoCoverage(t *testing.T) {
	root := t.TempDir()
	setupMinimalProject(t, root)

	createTestCase(t, root, "tc-mc000003", "No coverage TC")

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "", entries[0].ManualCoverage)
}

func TestPipelineStatus_ManualCoverage_NonManualOnly(t *testing.T) {
	root := t.TempDir()
	setupMinimalProject(t, root)

	createTestCase(t, root, "tc-mc000004", "Bats only TC")
	writeAutomationRecord(t, root, "tc-mc000004", "bats", "pass", "accepted")

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "", entries[0].ManualCoverage)
}

func TestPipelineStatus_ManualCoverage_PreservesNoAutomation(t *testing.T) {
	// A TC with only a manual record (prepared) should still count as not-automated
	// for the existing pipeline semantics. ManualCoverage is the disambiguator.
	root := t.TempDir()
	setupMinimalProject(t, root)

	createTestCase(t, root, "tc-mc000005", "Manual only TC")
	writeAutomationRecord(t, root, "tc-mc000005", "manual", "", "accepted")

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	// AutomateStatus should be "manual" (not "complete") per deriveAutomateStatus
	assert.Equal(t, "manual", entries[0].AutomateStatus)
	assert.Equal(t, "prepared", entries[0].ManualCoverage)
}

// --- Folder summary tests ---

func TestPipelineFolderSummary_ManualCounts(t *testing.T) {
	root := t.TempDir()
	setupMinimalProject(t, root)

	// Create 3 TCs in a folder: one prepared, one recorded, one no-manual
	folder := filepath.Join(root, "gtms", "cases", "myfolder")
	require.NoError(t, os.MkdirAll(folder, 0755))

	createTestCaseInDir(t, folder, "tc-mf000001", "Prepared manual")
	createTestCaseInDir(t, folder, "tc-mf000002", "Recorded manual")
	createTestCaseInDir(t, folder, "tc-mf000003", "Non-manual")

	writeAutomationRecord(t, root, "tc-mf000001", "manual", "", "accepted")
	writeAutomationRecord(t, root, "tc-mf000002", "manual", "pass", "accepted")
	writeAutomationRecord(t, root, "tc-mf000003", "bats", "pass", "accepted")

	summaries, err := PipelineFolderSummary(root, "")
	require.NoError(t, err)
	require.Len(t, summaries, 1)

	s := summaries[0]
	assert.Equal(t, 3, s.Created)
	assert.Equal(t, 1, s.ManualPrepared)
	assert.Equal(t, 1, s.ManualRecorded)
	// Existing counts: only non-manual records count as "automated"
	assert.Equal(t, 1, s.Automated)
}

// --- Gaps tests ---

func TestGaps_NoAutomation_ManualCoverage(t *testing.T) {
	root := t.TempDir()
	setupMinimalProject(t, root)

	// TC with only a manual record → NoAutomation with ManualCoverage="prepared"
	createTestCase(t, root, "tc-gp000001", "Manual prepared gap")
	writeAutomationRecord(t, root, "tc-gp000001", "manual", "", "accepted")

	// TC with manual record with result → NoAutomation with ManualCoverage="recorded"
	createTestCase(t, root, "tc-gp000002", "Manual recorded gap")
	writeAutomationRecord(t, root, "tc-gp000002", "manual", "pass", "accepted")

	// TC with no record at all → NoAutomation with ManualCoverage=""
	createTestCase(t, root, "tc-gp000003", "No coverage gap")

	// TC with non-manual record → NOT in NoAutomation
	createTestCase(t, root, "tc-gp000004", "Automated TC")
	writeAutomationRecord(t, root, "tc-gp000004", "bats", "pass", "accepted")

	report, err := Gaps(root, nil, "", false)
	require.NoError(t, err)

	// 3 TCs in NoAutomation (manual-only and no-record)
	require.Len(t, report.NoAutomation, 3)

	// Build map by ID for assertion
	noAutoMap := make(map[string]GapEntry)
	for _, e := range report.NoAutomation {
		noAutoMap[e.ID] = e
	}

	assert.Equal(t, "prepared", noAutoMap["tc-gp000001"].ManualCoverage)
	assert.Equal(t, "recorded", noAutoMap["tc-gp000002"].ManualCoverage)
	assert.Equal(t, "", noAutoMap["tc-gp000003"].ManualCoverage)

	// tc-gp000004 (bats) should NOT be in NoAutomation
	_, hasBats := noAutoMap["tc-gp000004"]
	assert.False(t, hasBats)
}

func TestGapsFolderSummary_ManualCounts(t *testing.T) {
	root := t.TempDir()
	setupMinimalProject(t, root)

	folder := filepath.Join(root, "gtms", "cases", "gapfolder")
	require.NoError(t, os.MkdirAll(folder, 0755))

	createTestCaseInDir(t, folder, "tc-gf000001", "Prepared")
	createTestCaseInDir(t, folder, "tc-gf000002", "Recorded")
	createTestCaseInDir(t, folder, "tc-gf000003", "No manual")

	writeAutomationRecord(t, root, "tc-gf000001", "manual", "", "accepted")
	writeAutomationRecord(t, root, "tc-gf000002", "manual", "pass", "accepted")
	// tc-gf000003 has no records at all

	summaries, err := GapsFolderSummary(root, "")
	require.NoError(t, err)
	require.Len(t, summaries, 1)

	s := summaries[0]
	assert.Equal(t, 3, s.Created)
	assert.Equal(t, 1, s.ManualPrepared)
	assert.Equal(t, 1, s.ManualRecorded)
	// All 3 should be NotAutomated (manual is on-ramp, no-record is no-automation)
	assert.Equal(t, 3, s.NotAutomated)
}

// --- Map tests ---

func TestMap_ManualCoverage(t *testing.T) {
	root := t.TempDir()
	setupMinimalProject(t, root)

	createTestCase(t, root, "tc-mp000001", "Map prepared")
	writeAutomationRecord(t, root, "tc-mp000001", "manual", "", "accepted")

	createTestCase(t, root, "tc-mp000002", "Map recorded")
	writeAutomationRecord(t, root, "tc-mp000002", "manual", "pass", "accepted")

	report, err := Map(root, nil, "", false)
	require.NoError(t, err)

	// Build map of all entries (unlinked since no requirement)
	entryMap := make(map[string]MapEntry)
	for _, e := range report.Unlinked {
		entryMap[e.TestCaseID] = e
	}

	assert.Equal(t, "prepared", entryMap["tc-mp000001"].ManualCoverage)
	assert.Equal(t, "recorded", entryMap["tc-mp000002"].ManualCoverage)
}

// --- Helper functions ---

func setupMinimalProject(t *testing.T, root string) {
	t.Helper()
	dirs := []string{
		filepath.Join(root, "gtms", "cases"),
		filepath.Join(root, "gtms", "automation", "records"),
		filepath.Join(root, "gtms", "tasks", "pending"),
		filepath.Join(root, "gtms", "tasks", "in-progress"),
		filepath.Join(root, "gtms", "tasks", "complete"),
		filepath.Join(root, "gtms", "tasks", "error"),
	}
	for _, dir := range dirs {
		require.NoError(t, os.MkdirAll(dir, 0755))
	}
}

func createTestCase(t *testing.T, root, id, title string) {
	t.Helper()
	dir := filepath.Join(root, "gtms", "cases")
	createTestCaseInDir(t, dir, id, title)
}

func createTestCaseInDir(t *testing.T, dir, id, title string) {
	t.Helper()
	content := fmt.Sprintf(`---
test_case_id: %s
title: %s
requirement: ""
---
# %s
`, id, title, title)
	path := filepath.Join(dir, id+"-test.md")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
}

// writeAutomationRecord seeds the per-(tc, framework) state the new
// wiring-aware reader expects. CON-023 / ENH-145 / ENH-146:
//
//   - framework == "manual": no wiring record is created (manual-only
//     TCs do not get wiring per CON-023 Q#12); a manual result file is
//     written at gtms/manual/records/{tc}--manual.result.yaml.
//   - framework != "manual": writes a wiring record via wiring.Write at
//     gtms/automation/wiring/{tc}--{framework}.wiring.yaml. If a result
//     argument is supplied, also writes a matching terminal handoff via
//     result.Create under .gtms/results/ so the reader's overlay join
//     populates the execute fields.
//
// Implementation: this helper is a thin wrapper around seedLegacyRecord
// so all wiring / handoff / manual-record writes route through the
// production writers (wiring.Write, result.Create) and the shared
// hand-built manual YAML in the seed helper. The status parameter is
// retained for back-compatibility — wiring has no lifecycle so it is
// dropped silently. The legacy "accepted" vs "developed" distinction
// is retired (wiring is identity-only).
//
// Result vocabulary: "" stays "" (i.e. wiring exists but no terminal
// result joined → reader sees Result == ""). Non-empty values pass
// through unchanged.
func writeAutomationRecord(t *testing.T, root, tcID, framework, result, status string) {
	t.Helper()
	_ = status // wiring has no lifecycle; retained for back-compat with callers
	seedLegacyRecord(t, root, legacyRecord{
		TC:        tcID,
		Framework: framework,
		Result:    result,
	})
}
