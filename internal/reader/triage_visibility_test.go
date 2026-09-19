package reader

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ENH-202 reader tests: the triage-visibility join for status and gaps.
// These exercise the shared helpers (activeTriageByTC, AttachTriage*,
// FolderTriagedCounts) and the folder/failing partitions in gaps, using the
// same fixture helpers the ENH-200/201 reader tests use.

// writeSpecInFolder writes a minimal test-case spec under a named folder so
// folder-summary and gaps aggregations group it under that folder.
func writeSpecInFolder(t *testing.T, root, folder, tc string) {
	t.Helper()
	body := "---\n" +
		"test_case_id: " + tc + "\n" +
		"title: \"" + tc + " subject\"\n" +
		"requirement: ENH-202\n" +
		"created: 2026-08-28\n" +
		"---\n\n## Test Objective\nx\n"
	writeFile(t, root, filepath.Join("gtms", "test", "cases", folder, tc+".md"), body)
}

func newTriageValues() map[string]string {
	return map[string]string{
		"environment": "Shared environment problem",
		"app-wrong":   "",
	}
}

// --- activeTriageByTC ---

func TestActiveTriageByTC_ExcludesResolvedAndKeysByFramework(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")

	putTriageRecord(t, root, "tc-11111111", "bats", "environment", "fail", "")             // active
	putTriageRecord(t, root, "tc-22222222", "bats", "app-wrong", "fail", "2026-08-28T00:00:00Z") // resolved

	byTC, err := activeTriageByTC(root, []string{"tc-11111111", "tc-22222222", "tc-33333333"})
	require.NoError(t, err)

	// Active record is present, keyed by framework.
	require.NotNil(t, byTC["tc-11111111"])
	assert.NotNil(t, byTC["tc-11111111"]["bats"])
	// Resolved record is excluded from the active index.
	assert.Nil(t, byTC["tc-22222222"], "resolved records must not appear in the active index")
	// Absent record.
	assert.Nil(t, byTC["tc-33333333"])
}

func TestTcIsTriaged_FrameworkAware(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	putTriageRecord(t, root, "tc-11111111", "playwright", "app-wrong", "fail", "")

	byTC, err := activeTriageByTC(root, []string{"tc-11111111"})
	require.NoError(t, err)

	assert.True(t, tcIsTriaged(byTC["tc-11111111"], ""), "any active record triages when no framework is selected")
	assert.True(t, tcIsTriaged(byTC["tc-11111111"], "playwright"))
	assert.False(t, tcIsTriaged(byTC["tc-11111111"], "bats"), "a classification on another framework must not count")
}

// --- AttachTriageToEntries ---

func TestAttachTriage_NestsUnderFrameworkWithChangeAndLabel(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeSpecInFolder(t, root, "checkout", "tc-11111111")
	writeSpecInFolder(t, root, "checkout", "tc-22222222")

	// tc-11111111: classified fail, latest still fail -> unchanged (false), labelled.
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-11111111", Framework: "bats", Result: "fail"})
	putTriageRecord(t, root, "tc-11111111", "bats", "environment", "fail", "")
	// tc-22222222: classified fail, latest pass -> changed (true), empty label.
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-22222222", Framework: "bats", Result: "pass"})
	putTriageRecord(t, root, "tc-22222222", "bats", "app-wrong", "fail", "")

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.NoError(t, AttachTriageToEntries(root, entries, newTriageValues(), "", ""))

	byID := map[string]PipelineEntry{}
	for _, e := range entries {
		byID[e.TestCaseID] = e
	}

	t1 := frameworkTriageForTest(t, byID["tc-11111111"], "bats")
	assert.Equal(t, "environment", t1.Value)
	assert.Equal(t, "Shared environment problem", t1.Label)
	require.NotNil(t, t1.ResultChangedSinceTriage)
	assert.False(t, *t1.ResultChangedSinceTriage)

	t2 := frameworkTriageForTest(t, byID["tc-22222222"], "bats")
	assert.Equal(t, "app-wrong", t2.Value)
	assert.Empty(t, t2.Label, "an empty configured label is omitted")
	require.NotNil(t, t2.ResultChangedSinceTriage)
	assert.True(t, *t2.ResultChangedSinceTriage)
}

func TestAttachTriage_NullComparisonWhenNoHandoff(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeSpecInFolder(t, root, "checkout", "tc-44444444")
	// Wiring but no terminal handoff -> no latest result to compare.
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-44444444", Framework: "bats", Result: ""})
	putTriageRecord(t, root, "tc-44444444", "bats", "environment", "fail", "")

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.NoError(t, AttachTriageToEntries(root, entries, newTriageValues(), "", ""))

	tv := frameworkTriageForTest(t, entries[0], "bats")
	assert.Nil(t, tv.ResultChangedSinceTriage, "no handoff -> nil (comparison unavailable)")
	assert.Empty(t, tv.LatestResult)
}

func TestAttachTriage_ResolvedVisibleWithoutFilterHiddenWithFilter(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeSpecInFolder(t, root, "checkout", "tc-33333333")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-33333333", Framework: "bats", Result: "fail"})
	putTriageRecord(t, root, "tc-33333333", "bats", "test-wrong", "fail", "2026-08-28T00:00:00Z") // resolved

	// filter == "" attaches the resolved record (JSON exposes lifecycle).
	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.NoError(t, AttachTriageToEntries(root, entries, nil, "", ""))
	tv := frameworkTriageForTest(t, entries[0], "bats")
	assert.Equal(t, "resolved", tv.Status)

	// A value filter attaches only ACTIVE matching records -> nothing here.
	entries2, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.NoError(t, AttachTriageToEntries(root, entries2, nil, "test-wrong", ""))
	assert.Nil(t, frameworkEntryTriage(entries2[0], "bats"), "a filter must not attach a resolved record")
}

func TestAttachTriage_NoCrossFrameworkAttribution(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeSpecInFolder(t, root, "checkout", "tc-66666666")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-66666666", Framework: "bats", Result: "fail"})
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-66666666", Framework: "playwright", Adapter: "playwright-runner", Artefact: "test/acceptance/tc-66666666.spec.ts", Result: "fail"})
	putTriageRecord(t, root, "tc-66666666", "playwright", "app-wrong", "fail", "")

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.NoError(t, AttachTriageToEntries(root, entries, nil, "", ""))

	assert.Nil(t, frameworkEntryTriage(entries[0], "bats"), "bats entry must carry no triage")
	pw := frameworkEntryTriage(entries[0], "playwright")
	require.NotNil(t, pw)
	assert.Equal(t, "app-wrong", pw.Value)
}

// --- FolderTriagedCounts ---

func TestFolderTriagedCounts_DedupesMultiFramework(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeSpecInFolder(t, root, "checkout", "tc-66666666")
	writeSpecInFolder(t, root, "checkout", "tc-11111111")
	// tc-66666666 classified on BOTH frameworks -> still counts once.
	putTriageRecord(t, root, "tc-66666666", "bats", "environment", "fail", "")
	putTriageRecord(t, root, "tc-66666666", "playwright", "app-wrong", "fail", "")
	putTriageRecord(t, root, "tc-11111111", "bats", "environment", "fail", "")

	counts, err := FolderTriagedCounts(root, "")
	require.NoError(t, err)
	assert.Equal(t, 2, counts["checkout"], "a multi-framework TC counts once")

	// With --framework bats, only bats classifications count -> both TCs.
	countsBats, err := FolderTriagedCounts(root, "bats")
	require.NoError(t, err)
	assert.Equal(t, 2, countsBats["checkout"])
	// With --framework playwright, only tc-66666666 qualifies.
	countsPw, err := FolderTriagedCounts(root, "playwright")
	require.NoError(t, err)
	assert.Equal(t, 1, countsPw["checkout"])
}

// --- gaps partitions ---

func TestGapsFolderSummary_TriagedPartitionEqualsCreated(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	// Every result state; one resolved (counts as untriaged); one never executed.
	writeSpecInFolder(t, root, "checkout", "tc-11111111")
	writeSpecInFolder(t, root, "checkout", "tc-22222222")
	writeSpecInFolder(t, root, "checkout", "tc-33333333")
	writeSpecInFolder(t, root, "checkout", "tc-44444444")
	writeSpecInFolder(t, root, "checkout", "tc-55555555")
	writeSpecInFolder(t, root, "checkout", "tc-66666666")
	writeSpecInFolder(t, root, "checkout", "tc-77777777")

	seedLegacyRecord(t, root, legacyRecord{TC: "tc-11111111", Framework: "bats", Result: "fail"})
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-22222222", Framework: "bats", Result: "pass"})
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-33333333", Framework: "bats", Result: "skip"})
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-44444444", Framework: "bats", AdapterError: true})
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-55555555", Framework: "bats", Result: "fail"})
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-66666666", Framework: "bats", Result: ""}) // never executed
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-77777777", Framework: "bats", Result: "fail"})

	putTriageRecord(t, root, "tc-11111111", "bats", "environment", "fail", "")
	putTriageRecord(t, root, "tc-22222222", "bats", "test-wrong", "pass", "")
	putTriageRecord(t, root, "tc-33333333", "bats", "intermittent", "skip", "")
	putTriageRecord(t, root, "tc-44444444", "bats", "app-wrong", "error", "")
	putTriageRecord(t, root, "tc-77777777", "bats", "app-wrong", "fail", "2026-08-28T00:00:00Z") // resolved

	entries, err := GapsFolderSummary(root, "")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	e := entries[0]
	assert.Equal(t, 7, e.Created)
	assert.Equal(t, 4, e.Triaged, "pass/skip/error classifications count; resolved does not")
	assert.Equal(t, 3, e.Untriaged)
	assert.Equal(t, e.Created, e.Triaged+e.Untriaged, "partition invariant")
}

func TestGaps_CurrentlyFailingSubsetPartition(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeSpecInFolder(t, root, "checkout", "tc-11111111")
	writeSpecInFolder(t, root, "checkout", "tc-22222222")
	writeSpecInFolder(t, root, "checkout", "tc-33333333")
	writeSpecInFolder(t, root, "checkout", "tc-44444444")
	writeSpecInFolder(t, root, "checkout", "tc-55555555")

	seedLegacyRecord(t, root, legacyRecord{TC: "tc-11111111", Framework: "bats", Result: "fail"})
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-22222222", Framework: "bats", Result: "fail"})
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-33333333", Framework: "bats", Result: "fail"})
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-44444444", Framework: "bats", Result: "fail"})
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-55555555", Framework: "bats", Result: "pass"})

	putTriageRecord(t, root, "tc-11111111", "bats", "environment", "fail", "")
	putTriageRecord(t, root, "tc-22222222", "bats", "app-wrong", "fail", "")
	// tc-33333333 unclassified. tc-44444444 resolved -> untriaged failing.
	putTriageRecord(t, root, "tc-44444444", "bats", "app-wrong", "fail", "2026-08-28T00:00:00Z")
	putTriageRecord(t, root, "tc-55555555", "bats", "test-wrong", "pass", "") // not failing

	scope := &ScopeInfo{ScanDir: filepath.Join(root, "gtms", "test", "cases", "checkout"), RelPath: "gtms/test/cases/checkout/"}
	report, err := Gaps(root, scope, "", false)
	require.NoError(t, err)
	require.Len(t, report.CurrentlyFailing, 4)
	assert.Equal(t, 2, report.CurrentlyFailingTriaged)
	assert.Equal(t, 2, report.CurrentlyFailingUntriaged)
	assert.Equal(t, len(report.CurrentlyFailing), report.CurrentlyFailingTriaged+report.CurrentlyFailingUntriaged)
}

// --- test helpers ---

func frameworkEntryTriage(e PipelineEntry, fw string) *FrameworkTriage {
	for i := range e.Frameworks {
		if e.Frameworks[i].Framework == fw {
			return e.Frameworks[i].Triage
		}
	}
	return nil
}

func frameworkTriageForTest(t *testing.T, e PipelineEntry, fw string) *FrameworkTriage {
	t.Helper()
	tv := frameworkEntryTriage(e, fw)
	require.NotNilf(t, tv, "expected triage on %s framework %s", e.TestCaseID, fw)
	return tv
}
