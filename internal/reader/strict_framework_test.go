package reader

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests for ENH-082 — strict --framework filtering in per-TC views.
//
// Strict mode is enabled by the CLI when the user passes --framework
// explicitly. In strict mode, selectAutomationRecord returns the empty
// automationFrontmatter when no record matches the requested framework —
// no fallback to a different framework's record.
//
// Non-strict mode preserves the existing fallback rules (single-record
// short-circuit, defaultFramework match, then highest-cycle).

// --- Unit tests for selectAutomationRecord strict mode ---

func TestSelectAutomationRecord_StrictNoMatch(t *testing.T) {
	records := []automationFrontmatter{
		{Framework: "bats", Status: "accepted", Cycle: 2, TestCase: "tc-x"},
		{Framework: "playwright", Status: "accepted", Cycle: 1, TestCase: "tc-x"},
	}
	selected := selectAutomationRecord(records, "pester", true)
	assert.Equal(t, "", selected.Framework, "Strict mode with no matching framework should return empty")
	assert.Equal(t, "", selected.TestCase, "Empty record should have no test case")
	assert.Equal(t, "", selected.Status, "Empty record should have no status")
}

func TestSelectAutomationRecord_StrictWithMatch(t *testing.T) {
	records := []automationFrontmatter{
		{Framework: "bats", Status: "accepted", Cycle: 2},
		{Framework: "pester", Status: "accepted", Cycle: 1},
	}
	selected := selectAutomationRecord(records, "pester", true)
	assert.Equal(t, "pester", selected.Framework, "Strict mode with matching framework should return that record")
}

func TestSelectAutomationRecord_StrictSingleRecordNoMatch(t *testing.T) {
	// REGRESSION: the single-record short-circuit must NOT win in strict mode
	// when the framework doesn't match. Without strict bypass, this would
	// return the bats record incorrectly.
	records := []automationFrontmatter{
		{Framework: "bats", Status: "accepted", Cycle: 1},
	}
	selected := selectAutomationRecord(records, "pester", true)
	assert.Equal(t, "", selected.Framework, "Strict mode must bypass single-record short-circuit when framework doesn't match")
}

func TestSelectAutomationRecord_StrictSingleRecordMatch(t *testing.T) {
	records := []automationFrontmatter{
		{Framework: "bats", Status: "accepted", Cycle: 1},
	}
	selected := selectAutomationRecord(records, "bats", true)
	assert.Equal(t, "bats", selected.Framework, "Strict mode with matching single record should return it")
}

func TestSelectAutomationRecord_StrictEmptyFramework(t *testing.T) {
	// Strict mode with an empty defaultFramework is equivalent to non-strict —
	// no framework was actually specified, so fallback rules apply.
	records := []automationFrontmatter{
		{Framework: "bats", Status: "accepted", Cycle: 2},
		{Framework: "playwright", Status: "accepted", Cycle: 1},
	}
	selected := selectAutomationRecord(records, "", true)
	assert.Equal(t, "bats", selected.Framework, "Strict mode with empty framework falls back to highest cycle")
}

func TestSelectAutomationRecord_NonStrictFallback(t *testing.T) {
	// REGRESSION: the no-flag (config-default) path must still fall back
	// to the highest-cycle record when defaultFramework doesn't match.
	records := []automationFrontmatter{
		{Framework: "bats", Status: "accepted", Cycle: 2},
		{Framework: "playwright", Status: "accepted", Cycle: 1},
	}
	selected := selectAutomationRecord(records, "pester", false)
	assert.Equal(t, "bats", selected.Framework, "Non-strict mode should fall back to highest cycle")
}

func TestSelectAutomationRecord_StrictAndNonStrictEmptyRecords(t *testing.T) {
	// Edge: no records at all — both modes return the zero value.
	for _, strict := range []bool{true, false} {
		selected := selectAutomationRecord(nil, "pester", strict)
		assert.Equal(t, automationFrontmatter{}, selected, "No records should return empty regardless of strict")
	}
}

// --- Integration tests for PipelineStatus / PipelineDetail strict mode ---

func TestPipelineStatus_StrictFrameworkOmitsNonMatching(t *testing.T) {
	root := t.TempDir()

	// tc-aaa1111: both bats and pester records (pester passes)
	writeFile(t, root, filepath.Join("gtms/test/cases", "dual", "tc-aaa1111-both.md"), `---
test_case_id: tc-aaa1111
title: Both Frameworks
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-aaa1111",
		Framework: "bats",
		Result:    "fail",
	})
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-aaa1111",
		Framework: "pester",
		Result:    "pass",
	})

	// tc-bbb2222: only bats record
	writeFile(t, root, filepath.Join("gtms/test/cases", "dual", "tc-bbb2222-bats-only.md"), `---
test_case_id: tc-bbb2222
title: Bats Only
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-bbb2222",
		Framework: "bats",
		Result:    "pass",
	})

	// Strict mode with --framework pester: tc-aaa1111 selects pester record;
	// tc-bbb2222 selects nothing (em-dash).
	entries, err := PipelineStatus(root, nil, "pester", true)
	require.NoError(t, err)
	require.Len(t, entries, 2)

	// Entries are sorted by ID, so aaa first then bbb.
	aaa := entries[0]
	assert.Equal(t, "tc-aaa1111", aaa.TestCaseID)
	assert.Equal(t, "complete", aaa.AutomateStatus, "tc-aaa1111 has pester record → automation complete")
	assert.Equal(t, "pester", aaa.Framework, "tc-aaa1111 framework should be pester (not the bats fallback)")
	assert.Equal(t, "pass", aaa.LastResult, "tc-aaa1111 last result should be the pester pass, not the bats fail")
	assert.Equal(t, "complete", aaa.ExecuteStatus)

	bbb := entries[1]
	assert.Equal(t, "tc-bbb2222", bbb.TestCaseID)
	assert.Equal(t, "none", bbb.AutomateStatus, "tc-bbb2222 has no pester record → automate em-dash")
	assert.Equal(t, "", bbb.Framework, "tc-bbb2222 should have no framework selected")
	assert.Equal(t, "none", bbb.LastResult, "tc-bbb2222 should have no last result")
	assert.Equal(t, "none", bbb.ExecuteStatus, "tc-bbb2222 should have no execute status")
}

func TestPipelineStatus_NonStrictFrameworkFallsBack(t *testing.T) {
	// REGRESSION: same fixture as strict-omits, but with strict=false
	// (i.e. config-default path) — tc-bbb2222 should fall back to its bats
	// record, preserving today's behaviour.
	root := t.TempDir()
	writeFile(t, root, filepath.Join("gtms/test/cases", "dual", "tc-bbb2222-bats-only.md"), `---
test_case_id: tc-bbb2222
title: Bats Only
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-bbb2222",
		Framework: "bats",
		Result:    "pass",
	})

	entries, err := PipelineStatus(root, nil, "pester", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	bbb := entries[0]
	assert.Equal(t, "tc-bbb2222", bbb.TestCaseID)
	assert.Equal(t, "complete", bbb.AutomateStatus, "Non-strict mode should still select the bats record")
	assert.Equal(t, "bats", bbb.Framework, "Non-strict mode should fall back to bats")
	assert.Equal(t, "pass", bbb.LastResult)
}

func TestPipelineDetail_StrictFrameworkEmpty(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-bbb2222-bats-only.md"), `---
test_case_id: tc-bbb2222
title: Bats Only
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-bbb2222",
		Framework: "bats",
		Artefact:  "test/acceptance/foo.bats",
		Result:    "pass",
	})

	detail, err := PipelineDetail(root, "tc-bbb2222", "pester", true)
	require.NoError(t, err)
	require.NotNil(t, detail)

	// All per-record fields should be empty/none under strict mode.
	assert.Equal(t, "none", detail.AutomateStatus, "Strict + non-matching → AUTOMATE em-dash")
	assert.Equal(t, "", detail.Framework, "Strict + non-matching → no framework label")
	assert.Equal(t, "", detail.ArtefactPath, "Strict + non-matching → no artefact path")
	assert.Equal(t, "none", detail.LastResult, "Strict + non-matching → LAST RESULT em-dash")
	assert.Equal(t, "none", detail.ExecuteStatus)
	assert.False(t, detail.Stale, "Strict + empty record → no stale warning")
}

func TestPipelineDetail_NonStrictFrameworkFallsBack(t *testing.T) {
	// REGRESSION: detail view in non-strict mode keeps falling back.
	root := t.TempDir()
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-bbb2222-bats-only.md"), `---
test_case_id: tc-bbb2222
title: Bats Only
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-bbb2222",
		Framework: "bats",
		Result:    "pass",
	})

	detail, err := PipelineDetail(root, "tc-bbb2222", "pester", false)
	require.NoError(t, err)
	require.NotNil(t, detail)

	assert.Equal(t, "complete", detail.AutomateStatus, "Non-strict still selects bats")
	assert.Equal(t, "bats", detail.Framework, "Non-strict still falls back")
}

// --- Integration tests for Gaps strict mode ---

func TestGaps_StrictFrameworkExcludesNonMatching(t *testing.T) {
	root := t.TempDir()

	// tc-bbb2222: only bats record, currently failing
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-bbb2222-bats-only.md"), `---
test_case_id: tc-bbb2222
title: Bats Only Failing
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-bbb2222",
		Framework: "bats",
		Result:    "fail",
	})

	// Strict --framework pester: tc-bbb2222 should NOT appear in
	// CurrentlyFailing because the empty (no-pester) record has no
	// LastFormalResult to test against.
	report, err := Gaps(root, nil, "pester", true)
	require.NoError(t, err)
	assert.Empty(t, report.CurrentlyFailing, "Strict mode → TC without pester record not in CurrentlyFailing")
	// NoAutomation classification is independent of strict (driven by
	// hasNonManualRecord), so tc-bbb2222 is still automated — just not
	// currently failing under the strict pester filter.
	assert.Empty(t, report.NoAutomation, "tc-bbb2222 is automated (has a non-manual record), strict-or-not")
}

func TestGaps_NonStrictFrameworkIncludesFallback(t *testing.T) {
	// REGRESSION: same fixture, non-strict mode → tc-bbb2222 falls back to
	// its bats record and DOES appear in CurrentlyFailing.
	root := t.TempDir()
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-bbb2222-bats-only.md"), `---
test_case_id: tc-bbb2222
title: Bats Only Failing
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-bbb2222",
		Framework: "bats",
		Result:    "fail",
	})

	report, err := Gaps(root, nil, "pester", false)
	require.NoError(t, err)
	require.Len(t, report.CurrentlyFailing, 1, "Non-strict mode falls back to bats record")
	assert.Equal(t, "tc-bbb2222", report.CurrentlyFailing[0].ID)
}

// --- Integration tests for Map strict mode ---

func TestMap_StrictFrameworkOmitsNonMatching(t *testing.T) {
	root := t.TempDir()

	// tc-aaa1111 has both bats and pester records
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-aaa1111-both.md"), `---
test_case_id: tc-aaa1111
title: Both Frameworks
requirement: REQ-A
---
`)
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-aaa1111", Framework: "bats", Result: "fail"})
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-aaa1111",
		Framework: "pester",
		Result:    "pass",
		Artefact:  "test/pester/tc-aaa1111-both.Tests.ps1",
	})

	// tc-bbb2222 has only bats
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-bbb2222-bats-only.md"), `---
test_case_id: tc-bbb2222
title: Bats Only
requirement: REQ-A
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-bbb2222",
		Framework: "bats",
		Result:    "pass",
		Artefact:  "test/acceptance/tc-bbb2222.bats",
	})

	report, err := Map(root, nil, "pester", true)
	require.NoError(t, err)
	require.Len(t, report.Groups, 1)
	require.Len(t, report.Groups[0].TestCases, 2)

	// Find each TC in the report.
	var aaa, bbb *MapEntry
	for i := range report.Groups[0].TestCases {
		entry := &report.Groups[0].TestCases[i]
		switch entry.TestCaseID {
		case "tc-aaa1111":
			aaa = entry
		case "tc-bbb2222":
			bbb = entry
		}
	}
	require.NotNil(t, aaa, "tc-aaa1111 should appear")
	require.NotNil(t, bbb, "tc-bbb2222 should appear (filter does not hide TCs from map)")

	// tc-aaa1111: pester record selected, columns reflect pester.
	assert.Equal(t, "complete", aaa.AutomateStatus, "tc-aaa1111: pester record → automation complete")
	assert.Equal(t, "pass", aaa.LastResult, "tc-aaa1111: pester result is pass (not the bats fail)")
	assert.Equal(t, "complete", aaa.ExecuteStatus)

	// tc-bbb2222: no pester record → empty columns.
	assert.Equal(t, "none", bbb.AutomateStatus, "tc-bbb2222: strict pester → automate em-dash")
	assert.Equal(t, "none", bbb.LastResult, "tc-bbb2222: strict pester → no result")
	assert.Equal(t, "none", bbb.ExecuteStatus)
	assert.Equal(t, "", bbb.ArtefactPath, "tc-bbb2222: strict pester → no artefact path")
}

func TestMap_NonStrictFrameworkFallsBack(t *testing.T) {
	// REGRESSION: non-strict map keeps falling back to the bats record.
	root := t.TempDir()
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-bbb2222-bats-only.md"), `---
test_case_id: tc-bbb2222
title: Bats Only
requirement: REQ-A
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-bbb2222",
		Framework: "bats",
		Result:    "pass",
		Artefact:  "test/acceptance/tc-bbb2222.bats",
	})

	report, err := Map(root, nil, "pester", false)
	require.NoError(t, err)
	require.Len(t, report.Groups, 1)
	require.Len(t, report.Groups[0].TestCases, 1)

	bbb := report.Groups[0].TestCases[0]
	assert.Equal(t, "tc-bbb2222", bbb.TestCaseID)
	assert.Equal(t, "complete", bbb.AutomateStatus, "Non-strict falls back to bats")
	assert.Equal(t, "pass", bbb.LastResult)
}

// --- BUG-043: Cross-framework signal tests ---
//
// These tests verify that the AvailableFrameworks field on per-TC structs
// and the FrameworkMismatch count on folder-summary structs correctly
// distinguish "no records at all" from "records exist under other frameworks".

func TestAvailableFrameworks_Helper(t *testing.T) {
	t.Run("nil records", func(t *testing.T) {
		assert.Nil(t, availableFrameworks(nil))
	})
	t.Run("empty slice", func(t *testing.T) {
		assert.Nil(t, availableFrameworks([]automationFrontmatter{}))
	})
	t.Run("single framework", func(t *testing.T) {
		records := []automationFrontmatter{{Framework: "bats"}}
		assert.Equal(t, []string{"bats"}, availableFrameworks(records))
	})
	t.Run("multiple frameworks sorted", func(t *testing.T) {
		records := []automationFrontmatter{
			{Framework: "pester"},
			{Framework: "bats"},
			{Framework: "playwright"},
		}
		assert.Equal(t, []string{"bats", "pester", "playwright"}, availableFrameworks(records))
	})
	t.Run("duplicate frameworks deduplicated", func(t *testing.T) {
		records := []automationFrontmatter{
			{Framework: "bats", Cycle: 1},
			{Framework: "bats", Cycle: 2},
		}
		assert.Equal(t, []string{"bats"}, availableFrameworks(records))
	})
	t.Run("empty framework strings ignored", func(t *testing.T) {
		records := []automationFrontmatter{
			{Framework: ""},
			{Framework: "bats"},
		}
		assert.Equal(t, []string{"bats"}, availableFrameworks(records))
	})
	t.Run("all empty framework strings returns nil", func(t *testing.T) {
		records := []automationFrontmatter{{Framework: ""}}
		assert.Nil(t, availableFrameworks(records))
	})
}

func TestPipelineStatus_AvailableFrameworks(t *testing.T) {
	root := t.TempDir()

	// tc-aaa1111: bats + pester records
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-aaa1111-both.md"), `---
test_case_id: tc-aaa1111
title: Both Frameworks
---
`)
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-aaa1111", Framework: "bats", Result: "pass"})
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-aaa1111", Framework: "pester", Result: "pass"})

	// tc-bbb2222: pester only
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-bbb2222-pester-only.md"), `---
test_case_id: tc-bbb2222
title: Pester Only
---
`)
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-bbb2222", Framework: "pester", Result: "pass"})

	// tc-ccc3333: no records at all
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-ccc3333-no-records.md"), `---
test_case_id: tc-ccc3333
title: No Records
---
`)

	entries, err := PipelineStatus(root, nil, "bats", true)
	require.NoError(t, err)
	require.Len(t, entries, 3)

	// Sorted by ID: aaa, bbb, ccc
	aaa := entries[0]
	assert.Equal(t, "tc-aaa1111", aaa.TestCaseID)
	assert.Equal(t, []string{"bats", "pester"}, aaa.AvailableFrameworks,
		"TC with both records should list both frameworks")

	bbb := entries[1]
	assert.Equal(t, "tc-bbb2222", bbb.TestCaseID)
	assert.Equal(t, []string{"pester"}, bbb.AvailableFrameworks,
		"TC with pester-only record should list pester — distinguishable from no-records TC")
	assert.Equal(t, "none", bbb.AutomateStatus,
		"Strict --framework bats with no bats record → automate em-dash")

	ccc := entries[2]
	assert.Equal(t, "tc-ccc3333", ccc.TestCaseID)
	assert.Nil(t, ccc.AvailableFrameworks,
		"TC with no records should have nil AvailableFrameworks (omitted in JSON)")
	assert.Equal(t, "none", ccc.AutomateStatus)
}

func TestPipelineDetail_AvailableFrameworks(t *testing.T) {
	root := t.TempDir()

	// TC with pester-only record
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-bbb2222-pester-only.md"), `---
test_case_id: tc-bbb2222
title: Pester Only
---
`)
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-bbb2222", Framework: "pester", Result: "pass"})

	detail, err := PipelineDetail(root, "tc-bbb2222", "bats", true)
	require.NoError(t, err)
	require.NotNil(t, detail)

	assert.Equal(t, []string{"pester"}, detail.AvailableFrameworks,
		"Detail view should carry AvailableFrameworks from all records")
	assert.Equal(t, "none", detail.AutomateStatus,
		"Strict bats + no bats record → em-dash")
	assert.Equal(t, "", detail.Framework,
		"No framework selected under strict mode")
}

func TestPipelineDetail_AvailableFrameworks_NoRecords(t *testing.T) {
	root := t.TempDir()

	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-ccc3333-no-records.md"), `---
test_case_id: tc-ccc3333
title: No Records
---
`)

	detail, err := PipelineDetail(root, "tc-ccc3333", "bats", true)
	require.NoError(t, err)
	require.NotNil(t, detail)

	assert.Nil(t, detail.AvailableFrameworks,
		"TC with no records should have nil AvailableFrameworks")
}

func TestMap_AvailableFrameworks(t *testing.T) {
	root := t.TempDir()

	// TC with pester-only record
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-bbb2222-pester-only.md"), `---
test_case_id: tc-bbb2222
title: Pester Only
requirement: REQ-A
---
`)
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-bbb2222", Framework: "pester", Result: "pass"})

	// TC with no records
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-ccc3333-no-records.md"), `---
test_case_id: tc-ccc3333
title: No Records
requirement: REQ-A
---
`)

	report, err := Map(root, nil, "bats", true)
	require.NoError(t, err)
	require.Len(t, report.Groups, 1)
	require.Len(t, report.Groups[0].TestCases, 2)

	var bbb, ccc *MapEntry
	for i := range report.Groups[0].TestCases {
		e := &report.Groups[0].TestCases[i]
		switch e.TestCaseID {
		case "tc-bbb2222":
			bbb = e
		case "tc-ccc3333":
			ccc = e
		}
	}
	require.NotNil(t, bbb)
	require.NotNil(t, ccc)

	assert.Equal(t, []string{"pester"}, bbb.AvailableFrameworks,
		"Map entry with pester record should list pester")
	assert.Nil(t, ccc.AvailableFrameworks,
		"Map entry with no records should have nil AvailableFrameworks")
}

func TestPipelineFolderSummary_FrameworkMismatch(t *testing.T) {
	root := t.TempDir()

	// tc-aaa1111: bats record (matches --framework bats)
	writeFile(t, root, filepath.Join("gtms/test/cases", "folder-a", "tc-aaa1111-bats.md"), `---
test_case_id: tc-aaa1111
title: Has Bats
---
`)
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-aaa1111", Framework: "bats", Result: "pass"})

	// tc-bbb2222: pester only (mismatch when --framework bats)
	writeFile(t, root, filepath.Join("gtms/test/cases", "folder-a", "tc-bbb2222-pester-only.md"), `---
test_case_id: tc-bbb2222
title: Pester Only
---
`)
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-bbb2222", Framework: "pester", Result: "pass"})

	// tc-ccc3333: no records at all (not a mismatch, just not automated)
	writeFile(t, root, filepath.Join("gtms/test/cases", "folder-a", "tc-ccc3333-no-records.md"), `---
test_case_id: tc-ccc3333
title: No Records
---
`)

	entries, err := PipelineFolderSummary(root, "bats")
	require.NoError(t, err)
	require.Len(t, entries, 1)

	folderA := entries[0]
	assert.Equal(t, "folder-a", folderA.Folder)
	assert.Equal(t, 3, folderA.Created)
	assert.Equal(t, 1, folderA.Automated, "Only tc-aaa1111 has a bats record")
	assert.Equal(t, 1, folderA.FrameworkMismatch,
		"tc-bbb2222 has pester but not bats → 1 mismatch; tc-ccc3333 has no records → not a mismatch")
}

func TestPipelineFolderSummary_FrameworkMismatch_NoFilter(t *testing.T) {
	root := t.TempDir()

	writeFile(t, root, filepath.Join("gtms/test/cases", "folder-a", "tc-aaa1111-bats.md"), `---
test_case_id: tc-aaa1111
title: Has Bats
---
`)
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-aaa1111", Framework: "bats"})

	entries, err := PipelineFolderSummary(root, "")
	require.NoError(t, err)
	require.Len(t, entries, 1)

	assert.Equal(t, 0, entries[0].FrameworkMismatch,
		"No framework filter → FrameworkMismatch always 0")
}

func TestGapsFolderSummary_FrameworkMismatch(t *testing.T) {
	root := t.TempDir()

	// tc-aaa1111: bats record (matches --framework bats)
	writeFile(t, root, filepath.Join("gtms/test/cases", "folder-a", "tc-aaa1111-bats.md"), `---
test_case_id: tc-aaa1111
title: Has Bats
---
`)
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-aaa1111", Framework: "bats", Result: "pass"})

	// tc-bbb2222: pester only (mismatch when --framework bats)
	writeFile(t, root, filepath.Join("gtms/test/cases", "folder-a", "tc-bbb2222-pester-only.md"), `---
test_case_id: tc-bbb2222
title: Pester Only
---
`)
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-bbb2222", Framework: "pester", Result: "pass"})

	// tc-ccc3333: no records (not a mismatch)
	writeFile(t, root, filepath.Join("gtms/test/cases", "folder-a", "tc-ccc3333-no-records.md"), `---
test_case_id: tc-ccc3333
title: No Records
---
`)

	entries, err := GapsFolderSummary(root, "bats")
	require.NoError(t, err)
	require.Len(t, entries, 1)

	folderA := entries[0]
	assert.Equal(t, "folder-a", folderA.Folder)
	assert.Equal(t, 2, folderA.NotAutomated,
		"Both tc-bbb2222 (pester only) and tc-ccc3333 (no records) are not-automated under bats")
	assert.Equal(t, 1, folderA.FrameworkMismatch,
		"Only tc-bbb2222 is a framework mismatch (has pester); tc-ccc3333 has no records at all")
}

func TestGapsFolderSummary_FrameworkMismatch_NoFilter(t *testing.T) {
	root := t.TempDir()

	writeFile(t, root, filepath.Join("gtms/test/cases", "folder-a", "tc-aaa1111-bats.md"), `---
test_case_id: tc-aaa1111
title: Has Bats
---
`)
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-aaa1111", Framework: "bats"})

	entries, err := GapsFolderSummary(root, "")
	require.NoError(t, err)
	require.Len(t, entries, 1)

	assert.Equal(t, 0, entries[0].FrameworkMismatch,
		"No framework filter → FrameworkMismatch always 0")
}
