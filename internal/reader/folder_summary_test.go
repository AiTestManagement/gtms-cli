package reader

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPipelineFolderSummary_MultiFolder(t *testing.T) {
	root := t.TempDir()

	// Folder "login" — 2 TCs, 1 automated + executed
	writeFile(t, root, filepath.Join("gtms/cases", "login", "tc-aaa1111-login-happy.md"), `---
test_case_id: tc-aaa1111
title: Login Happy Path
requirement: REQ-A
---
`)
	writeFile(t, root, filepath.Join("gtms/cases", "login", "tc-aaa2222-login-error.md"), `---
test_case_id: tc-aaa2222
title: Login Error
requirement: REQ-A
---
`)
	// CON-023 / ENH-145: wiring record at the new path + matching
	// terminal handoff so the reader overlay surfaces a pass.
	writeFile(t, root, filepath.Join("gtms/automation", "wiring", "tc-aaa1111--bats.wiring.yaml"), `testcase: tc-aaa1111
testcase-hash: 0011223344556677
framework: bats
adapter: bats-runner
artefact: test/acceptance/tc-aaa1111.bats
artefact-hash: aabbccddeeff0011
`)
	writeFile(t, root, filepath.Join(".gtms", "results", "task-aaa1111-bats.handoff.yaml"), `task: task-aaa1111-bats
command: execute
target: tc-aaa1111
adapter: bats-runner
mode: sync
created: "2026-05-19T10:00:00Z"
status: complete
result: pass
framework: bats
completed: "2026-05-19T10:01:00Z"
`)

	// Folder "checkout" — 1 TC, no automation
	writeFile(t, root, filepath.Join("gtms/cases", "checkout", "tc-bbb1111-checkout.md"), `---
test_case_id: tc-bbb1111
title: Checkout Flow
requirement: REQ-B
---
`)

	entries, err := PipelineFolderSummary(root, "")
	require.NoError(t, err)
	require.Len(t, entries, 2)

	// Sorted alphabetically: checkout, login
	assert.Equal(t, "checkout", entries[0].Folder)
	assert.Equal(t, 1, entries[0].Created)
	assert.Equal(t, 0, entries[0].Automated)
	assert.Equal(t, 0, entries[0].Executed)

	assert.Equal(t, "login", entries[1].Folder)
	assert.Equal(t, 2, entries[1].Created)
	assert.Equal(t, 1, entries[1].Automated)
	assert.Equal(t, 1, entries[1].Executed)
}

func TestPipelineFolderSummary_DraftCount(t *testing.T) {
	root := t.TempDir()

	// 3 TCs in "feature": 2 draft, 1 ready
	writeFile(t, root, filepath.Join("gtms/cases", "feature", "tc-aaa1111-draft1.md"), `---
test_case_id: tc-aaa1111
title: Draft One
status: draft
---
`)
	writeFile(t, root, filepath.Join("gtms/cases", "feature", "tc-aaa2222-draft2.md"), `---
test_case_id: tc-aaa2222
title: Draft Two
status: Draft
---
`)
	writeFile(t, root, filepath.Join("gtms/cases", "feature", "tc-aaa3333-ready.md"), `---
test_case_id: tc-aaa3333
title: Ready One
status: ready
---
`)

	entries, err := PipelineFolderSummary(root, "")
	require.NoError(t, err)
	require.Len(t, entries, 1)

	assert.Equal(t, "feature", entries[0].Folder)
	assert.Equal(t, 3, entries[0].Created)
	assert.Equal(t, 2, entries[0].DraftCount)
}

func TestPipelineFolderSummary_NoDraftWhenZero(t *testing.T) {
	root := t.TempDir()

	writeFile(t, root, filepath.Join("gtms/cases", "feature", "tc-aaa1111-ready.md"), `---
test_case_id: tc-aaa1111
title: Ready One
---
`)

	entries, err := PipelineFolderSummary(root, "")
	require.NoError(t, err)
	require.Len(t, entries, 1)

	assert.Equal(t, 0, entries[0].DraftCount)
}

func TestPipelineFolderSummary_EmptyProject(t *testing.T) {
	root := t.TempDir()

	entries, err := PipelineFolderSummary(root, "")
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestPipelineFolderSummary_RootFolder(t *testing.T) {
	root := t.TempDir()

	// TC directly in gtms/cases/ (no subfolder)
	writeFile(t, root, filepath.Join("gtms/cases", "tc-aaa1111-root.md"), `---
test_case_id: tc-aaa1111
title: Root TC
---
`)

	entries, err := PipelineFolderSummary(root, "")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "(root)", entries[0].Folder)
}

func TestGapsFolderSummary_MultiFolders(t *testing.T) {
	root := t.TempDir()

	// Folder "login" — 2 TCs, 1 automated (no execution), 1 not automated
	writeFile(t, root, filepath.Join("gtms/cases", "login", "tc-aaa1111-login-happy.md"), `---
test_case_id: tc-aaa1111
title: Login Happy Path
---
`)
	writeFile(t, root, filepath.Join("gtms/cases", "login", "tc-aaa2222-login-error.md"), `---
test_case_id: tc-aaa2222
title: Login Error
---
`)
	// CON-023 / ENH-145: wiring record (no terminal handoff → "never executed").
	writeFile(t, root, filepath.Join("gtms/automation", "wiring", "tc-aaa1111--bats.wiring.yaml"), `testcase: tc-aaa1111
testcase-hash: 0011223344556677
framework: bats
adapter: bats-runner
artefact: test/acceptance/tc-aaa1111.bats
artefact-hash: aabbccddeeff0011
`)

	// Folder "checkout" — 1 TC, automated and failing
	writeFile(t, root, filepath.Join("gtms/cases", "checkout", "tc-bbb1111-checkout.md"), `---
test_case_id: tc-bbb1111
title: Checkout Flow
---
`)
	writeFile(t, root, filepath.Join("gtms/automation", "wiring", "tc-bbb1111--bats.wiring.yaml"), `testcase: tc-bbb1111
testcase-hash: 0011223344556677
framework: bats
adapter: bats-runner
artefact: test/acceptance/tc-bbb1111.bats
artefact-hash: aabbccddeeff0011
`)
	writeFile(t, root, filepath.Join(".gtms", "results", "task-bbb1111.handoff.yaml"), `task: task-bbb1111
command: execute
target: tc-bbb1111
adapter: bats-runner
mode: sync
created: "2026-05-19T10:00:00Z"
status: complete
result: fail
framework: bats
completed: "2026-05-19T10:01:00Z"
`)

	entries, err := GapsFolderSummary(root, "")
	require.NoError(t, err)
	require.Len(t, entries, 2)

	// checkout: 1 created, 0 not-automated, 0 not-executed (has result), 1 failing
	assert.Equal(t, "checkout", entries[0].Folder)
	assert.Equal(t, 1, entries[0].Created)
	assert.Equal(t, 0, entries[0].NotAutomated)
	assert.Equal(t, 0, entries[0].NotExecuted)
	assert.Equal(t, 1, entries[0].Failing)

	// login: 2 created, 1 not-automated, 0 failing.
	// CON-023 / ENH-146: NotExecuted is retired ("not run here" is not a
	// gap); the field is no longer populated and renders as zero.
	assert.Equal(t, "login", entries[1].Folder)
	assert.Equal(t, 2, entries[1].Created)
	assert.Equal(t, 1, entries[1].NotAutomated)
	assert.Equal(t, 0, entries[1].NotExecuted, "NotExecuted retired post-ENH-146")
	assert.Equal(t, 0, entries[1].Failing)
}

func TestGapsFolderSummary_EmptyProject(t *testing.T) {
	root := t.TempDir()

	entries, err := GapsFolderSummary(root, "")
	require.NoError(t, err)
	assert.Empty(t, entries)
}

// --- Manual framework tests (ENH-068) ---

func TestDeriveAutomateStatus_Manual(t *testing.T) {
	ar := automationFrontmatter{
		Framework: "manual",
		Status:    "accepted",
	}
	assert.Equal(t, "manual", deriveAutomateStatus(ar))
}

func TestDeriveAutomateStatus_NonManual(t *testing.T) {
	ar := automationFrontmatter{
		Framework: "bats",
		Status:    "accepted",
	}
	assert.Equal(t, "complete", deriveAutomateStatus(ar))
}

func TestSelectAutomationRecord_PrefersNonManualOnTie(t *testing.T) {
	records := []automationFrontmatter{
		{Framework: "manual", Status: "accepted", Cycle: 1},
		{Framework: "bats", Status: "accepted", Cycle: 1},
	}
	selected := selectAutomationRecord(records, "", false)
	assert.Equal(t, "bats", selected.Framework, "Should prefer non-manual when cycles tied")
}

func TestSelectAutomationRecord_ManualWinsHigherCycle(t *testing.T) {
	records := []automationFrontmatter{
		{Framework: "bats", Status: "accepted", Cycle: 1},
		{Framework: "manual", Status: "accepted", Cycle: 2},
	}
	selected := selectAutomationRecord(records, "", false)
	assert.Equal(t, "manual", selected.Framework, "Higher cycle should win regardless of framework")
}

func TestSelectAutomationRecord_DefaultFrameworkOverridesManual(t *testing.T) {
	records := []automationFrontmatter{
		{Framework: "manual", Status: "accepted", Cycle: 2},
		{Framework: "bats", Status: "accepted", Cycle: 1},
	}
	selected := selectAutomationRecord(records, "bats", false)
	assert.Equal(t, "bats", selected.Framework, "defaultFramework should override manual")
}

func TestPipelineFolderSummary_ManualExcludedFromAutomated(t *testing.T) {
	root := t.TempDir()

	// 2 TCs: one with manual record + result, one with bats record + result
	writeFile(t, root, filepath.Join("gtms/cases", "feature", "tc-aaa1111-manual.md"), `---
test_case_id: tc-aaa1111
title: Manual Test
---
`)
	writeFile(t, root, filepath.Join("gtms/cases", "feature", "tc-bbb2222-automated.md"), `---
test_case_id: tc-bbb2222
title: Automated Test
---
`)
	// CON-023 / ENH-145: manual TC → manual result file. Bats TC → wiring + handoff.
	writeFile(t, root, filepath.Join("gtms/manual", "records", "tc-aaa1111--manual.result.yaml"),
		"test_case_id: tc-aaa1111\nframework: manual\nresult: pass\n")
	writeFile(t, root, filepath.Join("gtms/automation", "wiring", "tc-bbb2222--bats.wiring.yaml"),
		"testcase: tc-bbb2222\ntestcase-hash: 0011223344556677\nframework: bats\nadapter: bats-runner\nartefact: test/acceptance/tc-bbb2222.bats\nartefact-hash: aabbccddeeff0011\n")
	writeFile(t, root, filepath.Join(".gtms", "results", "task-bbb2222.handoff.yaml"),
		`task: task-bbb2222
command: execute
target: tc-bbb2222
adapter: bats-runner
mode: sync
created: "2026-05-19T10:00:00Z"
status: complete
result: pass
framework: bats
completed: "2026-05-19T10:01:00Z"
`)

	entries, err := PipelineFolderSummary(root, "")
	require.NoError(t, err)
	require.Len(t, entries, 1)

	// Only bats TC counts as automated; both count as executed
	assert.Equal(t, 1, entries[0].Automated, "Manual record should NOT count as automated")
	assert.Equal(t, 2, entries[0].Executed, "Manual result should count as executed")
}

func TestGapsFolderSummary_ManualCountsAsNotAutomated(t *testing.T) {
	root := t.TempDir()

	writeFile(t, root, filepath.Join("gtms/cases", "feature", "tc-aaa1111-manual.md"), `---
test_case_id: tc-aaa1111
title: Manual Only Test
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-aaa1111",
		Framework: "manual",
		Adapter:   "manual",
		Result:    "pass",
	})

	entries, err := GapsFolderSummary(root, "")
	require.NoError(t, err)
	require.Len(t, entries, 1)

	assert.Equal(t, 1, entries[0].NotAutomated, "Manual-only TC should count as not automated")
}

// TestGaps_ManualOnlyInNoAutomation: a manual record without a framework
// (framework: manual, filename `--manual.automation.md`) is the "no real
// automation yet" state — the TC still needs an automation record under a
// real framework, so gaps keeps it in NoAutomation.
//
// BUG-041 counterpart: a manual record that WAS routed to a real framework
// (e.g. `gtms execute --result skip --framework bats` writes a `--bats`
// record with framework: bats) must NOT be double-classified as NoAutomation
// on top of the framework-qualified categorisation. See
// TestGaps_FrameworkQualifiedManualNotInNoAutomation below.
func TestGaps_ManualOnlyInNoAutomation(t *testing.T) {
	root := t.TempDir()

	writeFile(t, root, filepath.Join("gtms/cases", "tc-aaa1111-manual.md"), `---
test_case_id: tc-aaa1111
title: Manual Only Test
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-aaa1111",
		Framework: "manual",
		Adapter:   "manual",
		Result:    "pass",
	})

	report, err := Gaps(root, nil, "", false)
	require.NoError(t, err)

	assert.Len(t, report.NoAutomation, 1, "Manual-only TC should appear in NoAutomation")
	assert.Equal(t, "tc-aaa1111", report.NoAutomation[0].ID)
}

// TestGaps_FrameworkQualifiedManualNotInNoAutomation: BUG-041. A manual
// record produced by `--result <x> --framework <fw>` has framework set to
// the real framework (e.g. bats) even though the adapter remains "manual".
// It must NOT be double-counted into NoAutomation — the record is the TC's
// automation for that framework, even though a human produced it.
func TestGaps_FrameworkQualifiedManualNotInNoAutomation(t *testing.T) {
	root := t.TempDir()

	writeFile(t, root, filepath.Join("gtms/cases", "tc-bbb2222-manual-bats.md"), `---
test_case_id: tc-bbb2222
title: Manually recorded skip against bats framework
---
`)
	writeFile(t, root, filepath.Join("gtms/automation", "wiring", "tc-bbb2222--bats.wiring.yaml"),
		"testcase: tc-bbb2222\ntestcase-hash: 0011223344556677\nframework: bats\nadapter: bats-runner\nartefact: test/acceptance/tc-bbb2222.bats\nartefact-hash: aabbccddeeff0011\n")
	writeFile(t, root, filepath.Join(".gtms", "results", "task-bbb2222.handoff.yaml"),
		`task: task-bbb2222
command: execute
target: tc-bbb2222
adapter: bats-runner
mode: sync
created: "2026-05-19T10:00:00Z"
status: complete
result: skip
framework: bats
completed: "2026-05-19T10:01:00Z"
`)

	report, err := Gaps(root, nil, "", false)
	require.NoError(t, err)

	assert.Empty(t, report.NoAutomation,
		"BUG-041: TC with a framework-qualified manual record must NOT be double-counted in NoAutomation")
	assert.Len(t, report.RuntimeSkipped, 1,
		"BUG-041: TC with framework-qualified manual skip must appear in RuntimeSkipped")
	assert.Equal(t, "tc-bbb2222", report.RuntimeSkipped[0].ID)
}

// --- Framework filter tests (ENH-075) ---

func TestPipelineFolderSummary_FrameworkFilter(t *testing.T) {
	root := t.TempDir()

	// TC-A has both bats and pester records (both automated + executed)
	writeFile(t, root, filepath.Join("gtms/cases", "feature", "tc-aaa1111-both.md"), `---
test_case_id: tc-aaa1111
title: Both Frameworks
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-aaa1111",
		Framework: "bats",
		Result:    "pass",
		Attempts:  1,
	})
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-aaa1111",
		Framework: "pester",
		Result:    "pass",
		Attempts:  1,
	})

	// TC-B has only a bats record
	writeFile(t, root, filepath.Join("gtms/cases", "feature", "tc-bbb2222-bats-only.md"), `---
test_case_id: tc-bbb2222
title: Bats Only
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-bbb2222",
		Framework: "bats",
		Result:    "pass",
		Attempts:  1,
	})

	// Filter by pester: only TC-A matches
	entries, err := PipelineFolderSummary(root, "pester")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, 2, entries[0].Created, "Created should count all TCs regardless of framework")
	assert.Equal(t, 1, entries[0].Automated, "Only TC-A has a pester record")
	assert.Equal(t, 1, entries[0].Executed, "Only TC-A has a pester execution result")

	// Filter by bats: both TCs match
	entries, err = PipelineFolderSummary(root, "bats")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, 2, entries[0].Created)
	assert.Equal(t, 2, entries[0].Automated, "Both TCs have bats records")
	assert.Equal(t, 2, entries[0].Executed, "Both TCs have bats execution results")

	// No filter: both TCs match (backward compatible)
	entries, err = PipelineFolderSummary(root, "")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, 2, entries[0].Created)
	assert.Equal(t, 2, entries[0].Automated)
	assert.Equal(t, 2, entries[0].Executed)
}

func TestGapsFolderSummary_FrameworkFilter(t *testing.T) {
	root := t.TempDir()

	// TC-A has both bats and pester records
	writeFile(t, root, filepath.Join("gtms/cases", "feature", "tc-aaa1111-both.md"), `---
test_case_id: tc-aaa1111
title: Both Frameworks
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-aaa1111",
		Framework: "bats",
		Result:    "pass",
		Attempts:  1,
	})
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-aaa1111",
		Framework: "pester",
		Result:    "pass",
		Attempts:  1,
	})

	// TC-B has only a bats record
	writeFile(t, root, filepath.Join("gtms/cases", "feature", "tc-bbb2222-bats-only.md"), `---
test_case_id: tc-bbb2222
title: Bats Only
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-bbb2222",
		Framework: "bats",
		Result:    "pass",
		Attempts:  1,
	})

	// Filter by pester: TC-B should be "not automated" for pester
	entries, err := GapsFolderSummary(root, "pester")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, 2, entries[0].Created)
	assert.Equal(t, 1, entries[0].NotAutomated, "TC-B has no pester record")
	assert.Equal(t, 0, entries[0].NotExecuted)
	assert.Equal(t, 0, entries[0].Failing)

	// Filter by bats: both TCs automated for bats
	entries, err = GapsFolderSummary(root, "bats")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, 0, entries[0].NotAutomated, "Both TCs have bats records")

	// No filter: both TCs automated (backward compatible)
	entries, err = GapsFolderSummary(root, "")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, 0, entries[0].NotAutomated)
}

func TestDeriveFolderName(t *testing.T) {
	tcDir := filepath.Join("root", "gtms/cases")

	assert.Equal(t, "(root)", deriveFolderName(tcDir, filepath.Join("root", "gtms/cases", "tc-001.md")))
	assert.Equal(t, "login", deriveFolderName(tcDir, filepath.Join("root", "gtms/cases", "login", "tc-001.md")))
	assert.Equal(t, "login", deriveFolderName(tcDir, filepath.Join("root", "gtms/cases", "login", "sub", "tc-001.md")))
}

// --- ENH-089: outcome breakdown for icon-forward folder summary ---

func TestPipelineFolderSummary_PassingCount(t *testing.T) {
	root := t.TempDir()

	writeFile(t, root, filepath.Join("gtms/cases", "feature", "tc-aaa1111.md"), `---
test_case_id: tc-aaa1111
title: Test One
---
`)
	writeFile(t, root, filepath.Join("gtms/cases", "feature", "tc-aaa2222.md"), `---
test_case_id: tc-aaa2222
title: Test Two
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-aaa1111",
		Framework: "bats",
		Result:    "pass",
	})
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-aaa2222",
		Framework: "bats",
		Result:    "pass",
	})

	entries, err := PipelineFolderSummary(root, "")
	require.NoError(t, err)
	require.Len(t, entries, 1)

	assert.Equal(t, 2, entries[0].Created)
	assert.Equal(t, 2, entries[0].Passing)
	assert.Equal(t, 0, entries[0].Failing)
	assert.Equal(t, 0, entries[0].Errored)
	assert.Equal(t, 0, entries[0].InFlight)
}

func TestPipelineFolderSummary_FailingCount(t *testing.T) {
	root := t.TempDir()

	writeFile(t, root, filepath.Join("gtms/cases", "feature", "tc-aaa1111.md"), `---
test_case_id: tc-aaa1111
title: Test One
---
`)
	writeFile(t, root, filepath.Join("gtms/cases", "feature", "tc-aaa2222.md"), `---
test_case_id: tc-aaa2222
title: Test Two
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-aaa1111",
		Framework: "bats",
		Result:    "pass",
	})
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-aaa2222",
		Framework: "bats",
		Result:    "fail",
	})

	entries, err := PipelineFolderSummary(root, "")
	require.NoError(t, err)
	require.Len(t, entries, 1)

	assert.Equal(t, 1, entries[0].Passing)
	assert.Equal(t, 1, entries[0].Failing)
	assert.Equal(t, 0, entries[0].Errored)
}

func TestPipelineFolderSummary_ErrorCount(t *testing.T) {
	root := t.TempDir()

	writeFile(t, root, filepath.Join("gtms/cases", "feature", "tc-aaa1111.md"), `---
test_case_id: tc-aaa1111
title: Test One
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-aaa1111",
		Framework: "bats",
		Result:    "error",
	})

	entries, err := PipelineFolderSummary(root, "")
	require.NoError(t, err)
	require.Len(t, entries, 1)

	assert.Equal(t, 0, entries[0].Passing)
	assert.Equal(t, 0, entries[0].Failing)
	assert.Equal(t, 1, entries[0].Errored,
		"result: error must populate Errored, not Failing")
}

func TestPipelineFolderSummary_InFlightFromTaskFile(t *testing.T) {
	root := t.TempDir()

	writeFile(t, root, filepath.Join("gtms/cases", "feature", "tc-aaa1111.md"), `---
test_case_id: tc-aaa1111
title: Test One
---
`)
	writeFile(t, root, filepath.Join("gtms/cases", "feature", "tc-aaa2222.md"), `---
test_case_id: tc-aaa2222
title: Test Two
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-aaa1111",
		Framework: "bats",
		Result:    "pass",
	})
	// In-flight execute task on tc-aaa2222 — no automation record needed
	// for in-flight detection.
	writeFile(t, root, filepath.Join("gtms/tasks", "in-progress", "task-abc1234-execute-tc-aaa2222.md"), `---
id: task-abc1234
type: execute
target: tc-aaa2222
adapter: bats-runner
status: in-progress
created: 2026-04-16T12:00:00Z
branch: main
---
`)

	entries, err := PipelineFolderSummary(root, "")
	require.NoError(t, err)
	require.Len(t, entries, 1)

	assert.Equal(t, 1, entries[0].InFlight,
		"TC with execute task in gtms/tasks/in-progress/ must increment InFlight")
	assert.Equal(t, 1, entries[0].Passing,
		"TC with passing automation record still counts as passing alongside in-flight on a different TC")
}

func TestPipelineFolderSummary_FrameworkFilterScopesOutcomeCounts(t *testing.T) {
	root := t.TempDir()

	// TC-A has bats result=pass, pester result=fail
	writeFile(t, root, filepath.Join("gtms/cases", "feature", "tc-aaa1111.md"), `---
test_case_id: tc-aaa1111
title: Both Frameworks
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-aaa1111",
		Framework: "bats",
		Result:    "pass",
	})
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-aaa1111",
		Framework: "pester",
		Result:    "fail",
	})

	// Filter by bats: pass count = 1, fail count = 0
	entries, err := PipelineFolderSummary(root, "bats")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, 1, entries[0].Passing, "bats record passes")
	assert.Equal(t, 0, entries[0].Failing, "pester fail must NOT leak into bats filter")

	// Filter by pester: pass count = 0, fail count = 1
	entries, err = PipelineFolderSummary(root, "pester")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, 0, entries[0].Passing, "bats pass must NOT leak into pester filter")
	assert.Equal(t, 1, entries[0].Failing, "pester record fails")
}

func TestPipelineFolderSummary_NoOutcomeWhenNoAutomation(t *testing.T) {
	root := t.TempDir()

	writeFile(t, root, filepath.Join("gtms/cases", "feature", "tc-aaa1111.md"), `---
test_case_id: tc-aaa1111
title: Test One
---
`)

	entries, err := PipelineFolderSummary(root, "")
	require.NoError(t, err)
	require.Len(t, entries, 1)

	assert.Equal(t, 1, entries[0].Created)
	assert.Equal(t, 0, entries[0].Passing)
	assert.Equal(t, 0, entries[0].Failing)
	assert.Equal(t, 0, entries[0].Errored)
	assert.Equal(t, 0, entries[0].InFlight)
}

// --- BUG-082: folder summary must not hide non-default framework wiring ---

func TestBUG082_FolderSummary_NoFrameworkCountsAllWiring(t *testing.T) {
	root := t.TempDir()

	// Folder "bats-folder" — 1 TC with BATS wiring
	writeFile(t, root, filepath.Join("gtms/cases", "bats-folder", "tc-aaa1111-bats.md"), `---
test_case_id: tc-aaa1111
title: BATS Test
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-aaa1111",
		Framework: "bats",
		Result:    "pass",
	})

	// Folder "pester-folder" — 1 TC with Pester wiring only
	writeFile(t, root, filepath.Join("gtms/cases", "pester-folder", "tc-bbb2222-pester.md"), `---
test_case_id: tc-bbb2222
title: Pester Test
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-bbb2222",
		Framework: "pester",
		Result:    "pass",
	})

	// BUG-082: No framework filter (empty string) — both folders must
	// count as automated regardless of any config default.
	entries, err := PipelineFolderSummary(root, "")
	require.NoError(t, err)
	require.Len(t, entries, 2)

	// Sorted alphabetically: bats-folder, pester-folder
	assert.Equal(t, "bats-folder", entries[0].Folder)
	assert.Equal(t, 1, entries[0].Automated, "BATS folder must be automated with no framework filter")
	assert.Equal(t, 1, entries[0].Passing)

	assert.Equal(t, "pester-folder", entries[1].Folder)
	assert.Equal(t, 1, entries[1].Automated, "BUG-082: Pester folder must be automated with no framework filter")
	assert.Equal(t, 1, entries[1].Passing)
	assert.Equal(t, 0, entries[1].FrameworkMismatch, "No framework filter means no mismatches")
}

func TestBUG082_FolderSummary_ExplicitFrameworkStillScopes(t *testing.T) {
	root := t.TempDir()

	// Folder "mixed" — 1 BATS TC, 1 Pester TC
	writeFile(t, root, filepath.Join("gtms/cases", "mixed", "tc-aaa1111-bats.md"), `---
test_case_id: tc-aaa1111
title: BATS Test
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-aaa1111",
		Framework: "bats",
		Result:    "pass",
	})

	writeFile(t, root, filepath.Join("gtms/cases", "mixed", "tc-bbb2222-pester.md"), `---
test_case_id: tc-bbb2222
title: Pester Test
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-bbb2222",
		Framework: "pester",
		Result:    "pass",
	})

	// Explicit --framework bats: only BATS TC counts as automated
	entries, err := PipelineFolderSummary(root, "bats")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, 2, entries[0].Created, "Created counts all TCs")
	assert.Equal(t, 1, entries[0].Automated, "Only BATS TC automated under bats filter")
	assert.Equal(t, 1, entries[0].FrameworkMismatch, "Pester-only TC is a framework mismatch under bats filter")

	// Explicit --framework pester: only Pester TC counts as automated
	entries, err = PipelineFolderSummary(root, "pester")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, 2, entries[0].Created)
	assert.Equal(t, 1, entries[0].Automated, "Only Pester TC automated under pester filter")
	assert.Equal(t, 1, entries[0].FrameworkMismatch, "BATS-only TC is a framework mismatch under pester filter")
}

func TestBUG082_GapsFolderSummary_NoFrameworkCountsAllWiring(t *testing.T) {
	root := t.TempDir()

	// 1 BATS-wired TC, 1 Pester-wired TC in same folder
	writeFile(t, root, filepath.Join("gtms/cases", "multi", "tc-aaa1111-bats.md"), `---
test_case_id: tc-aaa1111
title: BATS Test
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-aaa1111",
		Framework: "bats",
		Result:    "pass",
	})

	writeFile(t, root, filepath.Join("gtms/cases", "multi", "tc-bbb2222-pester.md"), `---
test_case_id: tc-bbb2222
title: Pester Test
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-bbb2222",
		Framework: "pester",
		Result:    "pass",
	})

	// BUG-082: No framework filter — neither TC should be "not automated"
	entries, err := GapsFolderSummary(root, "")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, 2, entries[0].Created)
	assert.Equal(t, 0, entries[0].NotAutomated, "BUG-082: Both TCs automated with no framework filter")
	assert.Equal(t, 0, entries[0].FrameworkMismatch, "No framework filter means no mismatches")
}

func TestBUG082_GapsFolderSummary_ExplicitFrameworkStillScopes(t *testing.T) {
	root := t.TempDir()

	// 1 BATS-wired TC, 1 Pester-wired TC in same folder
	writeFile(t, root, filepath.Join("gtms/cases", "multi", "tc-aaa1111-bats.md"), `---
test_case_id: tc-aaa1111
title: BATS Test
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-aaa1111",
		Framework: "bats",
		Result:    "pass",
	})

	writeFile(t, root, filepath.Join("gtms/cases", "multi", "tc-bbb2222-pester.md"), `---
test_case_id: tc-bbb2222
title: Pester Test
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-bbb2222",
		Framework: "pester",
		Result:    "pass",
	})

	// Explicit --framework bats: Pester TC is not-automated for bats
	entries, err := GapsFolderSummary(root, "bats")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, 1, entries[0].NotAutomated, "Pester TC not automated under bats filter")
	assert.Equal(t, 1, entries[0].FrameworkMismatch, "Pester TC is framework mismatch under bats")

	// Explicit --framework pester: BATS TC is not-automated for pester
	entries, err = GapsFolderSummary(root, "pester")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, 1, entries[0].NotAutomated, "BATS TC not automated under pester filter")
	assert.Equal(t, 1, entries[0].FrameworkMismatch, "BATS TC is framework mismatch under pester")
}
