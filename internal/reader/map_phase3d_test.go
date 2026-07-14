package reader

// CON-023 / ENH-145 / ENH-146 — Phase 3D map command contract.
//
// These tests pin the wiring-aware map contract:
//
//   - One MapEntry per TC-to-requirement link with per-framework details
//     surfaced in Frameworks[] (counting unit).
//   - Multi-framework wiring is fully represented in JSON; the compact
//     human row must not hide a sibling framework's failure.
//   - Manual-only TCs are not wiring units — Wired=false, ManualReady=true,
//     Frameworks=[], with the manual-record signal preserved on the
//     existing compact AUTOMATE / EXECUTE / LAST RESULT carriers.
//   - Adapter-error (status:error, empty result) stays distinct from
//     test-outcome fail.
//   - Orphan terminal handoffs (no matching wiring) are ignored.
//   - Pending / in-progress handoffs are ignored by the terminal overlay.
//   - Stale/missing wiring is reflected via the existing Stale flag — no
//     new gap categories. Authoritative gap reporting stays in `gtms gaps`.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMapPhase3D_WiredJSONExposesENH146Shape pins the new top-level
// wiring fields on MapEntry: Wired, ManualReady, SelectedFramework,
// Frameworks[]. The legacy compact fields stay populated so the existing
// human renderer continues to work.
func TestMapPhase3D_WiredJSONExposesENH146Shape(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-aaa1111-wired.md"), `---
test_case_id: tc-aaa1111
title: Wired TC
requirement: REQ-A
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC: "tc-aaa1111", Framework: "bats", Result: "pass",
		ExecutedAt: "2026-05-19T10:01:00Z",
	})

	report, err := Map(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, report.Groups, 1)
	require.Len(t, report.Groups[0].TestCases, 1)
	entry := report.Groups[0].TestCases[0]

	assert.True(t, entry.Wired, "wired TC must report Wired=true")
	assert.False(t, entry.ManualReady, "wired non-manual TC is not manual-ready")
	assert.Equal(t, "bats", entry.SelectedFramework,
		"picker must select the single wired framework")

	require.Len(t, entry.Frameworks, 1,
		"single-framework wired TC: one entry in Frameworks[]")
	fe := entry.Frameworks[0]
	assert.Equal(t, "bats", fe.Framework)
	assert.True(t, fe.Wired)
	assert.Empty(t, fe.WiringDrift, "fresh wiring => no drift")
	assert.True(t, fe.ArtefactPresent, "auto-seeded artefact present")
	assert.NotEmpty(t, fe.Artefact)
	assert.Equal(t, "complete", fe.LastStatusHere)
	assert.Equal(t, "pass", fe.LastResultHere)
	assert.Equal(t, "2026-05-19T10:01:00Z", fe.LastExecutedHere)

	// Legacy compact carriers remain populated for the human renderer.
	assert.Equal(t, "complete", entry.AutomateStatus)
	assert.Equal(t, "complete", entry.ExecuteStatus)
	assert.Equal(t, "pass", entry.LastResult)

	// Round-trip JSON: SelectedFramework must serialise as the literal
	// framework name (not null) when one is selected.
	raw, err := json.Marshal(entry)
	require.NoError(t, err)
	var obj map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &obj))
	require.Contains(t, obj, "selected_framework")
	assert.Equal(t, "bats", obj["selected_framework"])
}

// TestMapPhase3D_NotWiredJSONShape pins the JSON shape for a TC with no
// wiring at all: Wired=false, ManualReady=false, SelectedFramework=null,
// Frameworks=[].
func TestMapPhase3D_NotWiredJSONShape(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-bbb1111-bare.md"), `---
test_case_id: tc-bbb1111
title: Bare TC
requirement: REQ-A
---
`)

	report, err := Map(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, report.Groups, 1)
	require.Len(t, report.Groups[0].TestCases, 1)
	entry := report.Groups[0].TestCases[0]

	assert.False(t, entry.Wired)
	assert.False(t, entry.ManualReady)
	assert.Empty(t, entry.SelectedFramework)
	require.NotNil(t, entry.Frameworks,
		"Frameworks must be a non-nil empty slice, not null in JSON")
	assert.Len(t, entry.Frameworks, 0)

	raw, err := json.Marshal(entry)
	require.NoError(t, err)
	var obj map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &obj))
	require.Contains(t, obj, "selected_framework")
	assert.Nil(t, obj["selected_framework"],
		"selected_framework must serialise as JSON null when no framework is selected")
}

// TestMapPhase3D_MultiFrameworkJSONListsAllFrameworks pins the
// counting-unit rule: a TC wired to multiple frameworks must surface
// every framework in Frameworks[] so a picker-selected single record
// cannot mask sibling wiring.
func TestMapPhase3D_MultiFrameworkJSONListsAllFrameworks(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-ccc1111-multifw.md"), `---
test_case_id: tc-ccc1111
title: Multi-framework TC
requirement: REQ-M
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC: "tc-ccc1111", Framework: "bats", Result: "pass",
	})
	seedLegacyRecord(t, root, legacyRecord{
		TC: "tc-ccc1111", Framework: "playwright", Result: "fail",
	})

	report, err := Map(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, report.Groups, 1)
	require.Len(t, report.Groups[0].TestCases, 1)
	entry := report.Groups[0].TestCases[0]

	assert.True(t, entry.Wired)
	require.Len(t, entry.Frameworks, 2,
		"both wired frameworks must appear in Frameworks[]")

	// Frameworks sorted lexically — bats first, playwright second.
	assert.Equal(t, "bats", entry.Frameworks[0].Framework)
	assert.Equal(t, "pass", entry.Frameworks[0].LastResultHere)
	assert.Equal(t, "playwright", entry.Frameworks[1].Framework)
	assert.Equal(t, "fail", entry.Frameworks[1].LastResultHere)

	// AvailableFrameworks legacy carrier mirrors the wiring frameworks.
	assert.ElementsMatch(t, []string{"bats", "playwright"}, entry.AvailableFrameworks)
}

// TestMapPhase3D_MultiFrameworkCompactRowDoesNotHideFailure pins the
// "human output may stay compact, but must not report the TC as passing
// solely because the selected framework passes" rule. With one
// framework passing and another failing, the compact LastResult /
// ExecuteStatus must surface the failure.
func TestMapPhase3D_MultiFrameworkCompactRowDoesNotHideFailure(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-ddd1111-mixed.md"), `---
test_case_id: tc-ddd1111
title: Mixed outcome TC
requirement: REQ-M
---
`)
	// bats is lexically first → picker default selection in non-strict mode.
	seedLegacyRecord(t, root, legacyRecord{
		TC: "tc-ddd1111", Framework: "bats", Result: "pass",
	})
	seedLegacyRecord(t, root, legacyRecord{
		TC: "tc-ddd1111", Framework: "playwright", Result: "fail",
	})

	report, err := Map(root, nil, "", false)
	require.NoError(t, err)
	entry := report.Groups[0].TestCases[0]

	assert.Equal(t, "fail", entry.LastResult,
		"compact LastResult must surface the sibling framework's fail, not the picker's pass")
	assert.Equal(t, "complete", entry.ExecuteStatus,
		"ExecuteStatus stays complete; the failure is signalled via LastResult")
}

// TestMapPhase3D_MultiFrameworkCompactRowSurfacesError pins the
// adapter-error variant of the worst-of rule. status:error from any
// wired framework must override a sibling pass in the compact row.
func TestMapPhase3D_MultiFrameworkCompactRowSurfacesError(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-ddd2222-mixed-err.md"), `---
test_case_id: tc-ddd2222
title: Mixed pass+adapter-error TC
requirement: REQ-M
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC: "tc-ddd2222", Framework: "bats", Result: "pass",
	})
	seedLegacyRecord(t, root, legacyRecord{
		TC: "tc-ddd2222", Framework: "playwright", AdapterError: true,
	})

	report, err := Map(root, nil, "", false)
	require.NoError(t, err)
	entry := report.Groups[0].TestCases[0]

	assert.Equal(t, "error", entry.LastResult,
		"adapter-error sibling must surface on the compact row")
	assert.Equal(t, "error", entry.ExecuteStatus,
		"ExecuteStatus reflects the worst-of-framework adapter failure")
}

// TestMapPhase3D_AdapterErrorDistinctFromFail pins the ENH-130 rule that
// status:error with empty result (adapter failure) is distinct from
// status:complete with result:fail (test outcome failure). Map's compact
// LastResult must read "error" for the first and "fail" for the second.
func TestMapPhase3D_AdapterErrorDistinctFromFail(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-eee1111-adaptererr.md"), `---
test_case_id: tc-eee1111
title: Adapter error TC
requirement: REQ-E
---
`)
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-eee2222-outcomefail.md"), `---
test_case_id: tc-eee2222
title: Outcome fail TC
requirement: REQ-E
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC: "tc-eee1111", Framework: "bats", AdapterError: true,
	})
	seedLegacyRecord(t, root, legacyRecord{
		TC: "tc-eee2222", Framework: "bats", Result: "fail",
	})

	report, err := Map(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, report.Groups, 1)
	require.Len(t, report.Groups[0].TestCases, 2)

	byID := map[string]MapEntry{}
	for _, e := range report.Groups[0].TestCases {
		byID[e.TestCaseID] = e
	}

	adapterErr := byID["tc-eee1111"]
	require.Len(t, adapterErr.Frameworks, 1)
	assert.Equal(t, "error", adapterErr.Frameworks[0].LastStatusHere,
		"adapter failure → frameworks[].last_status_here:error")
	assert.Empty(t, adapterErr.Frameworks[0].LastResultHere,
		"adapter failure has no test outcome → last_result_here unset")
	assert.Equal(t, "error", adapterErr.LastResult,
		"compact LastResult signals adapter error distinctly from fail")
	assert.Equal(t, "error", adapterErr.ExecuteStatus)

	outcomeFail := byID["tc-eee2222"]
	require.Len(t, outcomeFail.Frameworks, 1)
	assert.Equal(t, "complete", outcomeFail.Frameworks[0].LastStatusHere)
	assert.Equal(t, "fail", outcomeFail.Frameworks[0].LastResultHere)
	assert.Equal(t, "fail", outcomeFail.LastResult,
		"test outcome failure surfaces as LastResult=fail")
	assert.Equal(t, "complete", outcomeFail.ExecuteStatus,
		"outcome failure keeps ExecuteStatus=complete")
}

// TestMapPhase3D_ManualOnlySignal pins the manual-only TC contract:
//   - Wired = false  (manual TCs are not wiring units)
//   - ManualReady = true when a primed manual result file exists
//   - Frameworks = [] (no framework row pretends to be wiring)
//   - Legacy carriers: AutomateStatus="manual", LastResult=<manual result>
func TestMapPhase3D_ManualOnlySignal(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-fff1111-manual-pass.md"), `---
test_case_id: tc-fff1111
title: Manual pass TC
requirement: REQ-MAN
---
`)
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-fff2222-manual-prepared.md"), `---
test_case_id: tc-fff2222
title: Manual prepared TC
requirement: REQ-MAN
---
`)
	// Recorded manual result.
	seedLegacyRecord(t, root, legacyRecord{
		TC: "tc-fff1111", Framework: "manual", Result: "pass",
	})
	// Primed manual record (no result yet).
	seedLegacyRecord(t, root, legacyRecord{
		TC: "tc-fff2222", Framework: "manual",
	})

	report, err := Map(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, report.Groups, 1)
	require.Len(t, report.Groups[0].TestCases, 2)

	byID := map[string]MapEntry{}
	for _, e := range report.Groups[0].TestCases {
		byID[e.TestCaseID] = e
	}

	recorded := byID["tc-fff1111"]
	assert.False(t, recorded.Wired,
		"manual-only TC must not report Wired=true")
	assert.True(t, recorded.ManualReady)
	assert.Empty(t, recorded.SelectedFramework,
		"manual-only TC has no selected wiring framework")
	require.NotNil(t, recorded.Frameworks)
	assert.Len(t, recorded.Frameworks, 0,
		"manual-only TC must not surface a framework row in Frameworks[]")
	assert.Equal(t, "manual", recorded.AutomateStatus)
	assert.Equal(t, "pass", recorded.LastResult)
	assert.Equal(t, "complete", recorded.ExecuteStatus)
	assert.Equal(t, "recorded", recorded.ManualCoverage)

	prepared := byID["tc-fff2222"]
	assert.False(t, prepared.Wired)
	assert.True(t, prepared.ManualReady)
	assert.Empty(t, prepared.SelectedFramework)
	assert.Len(t, prepared.Frameworks, 0)
	assert.Equal(t, "manual", prepared.AutomateStatus)
	assert.Equal(t, "none", prepared.LastResult,
		"primed-but-not-recorded manual surfaces LastResult=none")
	assert.Equal(t, "prepared", prepared.ManualCoverage)
}

// TestMapPhase3D_OrphanResultIgnored pins the "orphan result contracts
// without matching wiring are ignored" carry-forward rule. A terminal
// handoff for a TC with no wiring must not surface as wired/passing on
// the map.
func TestMapPhase3D_OrphanResultIgnored(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-ggg1111-orphan-result.md"), `---
test_case_id: tc-ggg1111
title: Orphan-result TC
requirement: REQ-O
---
`)
	// Hand-write a terminal handoff without any wiring file.
	resultsDir := filepath.Join(root, ".gtms", "results")
	require.NoError(t, os.MkdirAll(resultsDir, 0o755))
	handoff := `task: task-orphan-1
command: execute
target: tc-ggg1111
adapter: bats-runner
mode: sync
created: 2026-05-19T09:59:00Z
completed: 2026-05-19T10:00:00Z
framework: bats
status: complete
result: pass
`
	require.NoError(t, os.WriteFile(
		filepath.Join(resultsDir, "task-orphan-1.handoff.yaml"),
		[]byte(handoff), 0o644))

	report, err := Map(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, report.Groups, 1)
	require.Len(t, report.Groups[0].TestCases, 1)
	entry := report.Groups[0].TestCases[0]

	assert.False(t, entry.Wired,
		"orphan result with no wiring must not flip the TC to Wired=true")
	assert.False(t, entry.ManualReady)
	assert.Empty(t, entry.SelectedFramework)
	assert.Len(t, entry.Frameworks, 0)
	assert.Equal(t, "none", entry.AutomateStatus,
		"orphan result does not synthesise an AUTOMATE column")
	assert.Equal(t, "none", entry.LastResult,
		"orphan result is ignored — LastResult stays none")
}

// TestMapPhase3D_NonTerminalHandoffIgnored pins the terminal-handoff
// discipline: pending / in-progress result files are excluded from the
// overlay even when they match wiring.
func TestMapPhase3D_NonTerminalHandoffIgnored(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-hhh1111-inflight.md"), `---
test_case_id: tc-hhh1111
title: In-flight TC
requirement: REQ-IF
---
`)
	// Wiring exists but no terminal result.
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-hhh1111", Framework: "bats"})

	// Hand-write a non-terminal (in-progress) handoff for the SAME TC.
	resultsDir := filepath.Join(root, ".gtms", "results")
	require.NoError(t, os.MkdirAll(resultsDir, 0o755))
	inProgress := `task: task-inflight-1
command: execute
target: tc-hhh1111
adapter: bats-runner
mode: sync
created: 2026-05-19T10:00:00Z
framework: bats
status: in-progress
`
	require.NoError(t, os.WriteFile(
		filepath.Join(resultsDir, "task-inflight-1.handoff.yaml"),
		[]byte(inProgress), 0o644))

	report, err := Map(root, nil, "", false)
	require.NoError(t, err)
	entry := report.Groups[0].TestCases[0]

	assert.True(t, entry.Wired, "wiring still makes the TC wired")
	require.Len(t, entry.Frameworks, 1)
	assert.Empty(t, entry.Frameworks[0].LastStatusHere,
		"in-progress handoff must not surface as a terminal overlay")
	assert.Empty(t, entry.Frameworks[0].LastResultHere)
	assert.Equal(t, "none", entry.LastResult,
		"non-terminal handoff is ignored — TC remains wired/not run here")
	assert.Equal(t, "none", entry.ExecuteStatus)
}

// TestMapPhase3D_StaleArtefactFlagsStale pins that wiring-classifier
// drift surfaces via the existing Stale flag. The map must not invent
// new gap categories — authoritative reporting stays in `gtms gaps`.
func TestMapPhase3D_StaleArtefactFlagsStale(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-iii1111-stale.md"), `---
test_case_id: tc-iii1111
title: Stale artefact TC
requirement: REQ-S
---
`)
	// Wiring with a stale artefact hash (the helper seeds the real hash
	// when ArtefactHash is empty — we override to a known-mismatched
	// value so the classifier flags drift).
	seedLegacyRecord(t, root, legacyRecord{
		TC: "tc-iii1111", Framework: "bats",
		ArtefactHash: "0000000000000000", // deliberately mismatched to trigger stale
	})

	report, err := Map(root, nil, "", false)
	require.NoError(t, err)
	entry := report.Groups[0].TestCases[0]

	assert.True(t, entry.Wired)
	assert.True(t, entry.Stale,
		"stale artefact hash on the picked wiring → MapEntry.Stale=true")
	require.Len(t, entry.Frameworks, 1)
	assert.Equal(t, "artefact", entry.Frameworks[0].WiringDrift,
		"wiring_drift carries the per-framework drift label")
}

// --- Phase 3D fix-pass: task overlay must outrank worst-of-frameworks ---

// TestMapPhase3D_InProgressExecuteTaskNotHiddenByWorstOfFrameworks pins
// Finding 1 (medium): an in-progress execute task must remain visible
// on the compact ExecuteStatus column even when a sibling-framework
// terminal result has a worse outcome (fail). The fix-pass reorders
// worst-of-frameworks to run BEFORE applyTaskStatus so the task-derived
// state is the final overlay.
func TestMapPhase3D_InProgressExecuteTaskNotHiddenByWorstOfFrameworks(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-ttt1111-inflight.md"), `---
test_case_id: tc-ttt1111
title: In-flight with sibling fail
requirement: REQ-T
---
`)
	// Sibling-framework terminal fail (would dominate worst-of pre-fix).
	seedLegacyRecord(t, root, legacyRecord{
		TC: "tc-ttt1111", Framework: "playwright", Result: "fail",
	})
	// Bats wiring with no overlay — the picker will select this.
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-ttt1111", Framework: "bats"})

	// Active in-progress execute task on bats.
	mkdirAll(t, root, filepath.Join("gtms/tasks", "in-progress"))
	writeFile(t, root, filepath.Join("gtms/tasks", "in-progress",
		"task-aabbccdd-execute-tc-ttt1111.md"), `---
id: task-aabbccdd
type: execute
target: tc-ttt1111
adapter: bats-runner
status: in-progress
created: 2026-05-19T11:00:00Z
branch: feature/exec
---
`)

	report, err := Map(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, report.Groups, 1)
	entry := report.Groups[0].TestCases[0]

	assert.Equal(t, "in-progress", entry.ExecuteStatus,
		"in-progress execute task must remain the final overlay "+
			"even when a sibling framework has terminal fail")
	// The fail signal stays visible inside the per-framework array.
	assert.Equal(t, "fail", entry.LastResult,
		"LastResult still surfaces the sibling fail; "+
			"ExecuteStatus carries the in-progress signal")
	require.Len(t, entry.Frameworks, 2,
		"both frameworks remain in the per-framework array")
}

// TestMapPhase3D_NewExecuteErrorTaskNotHiddenByWorstOfFrameworks pins
// that a newer execute task with status error overrides the
// worst-of-frameworks terminal label. The applyTaskStatus
// record-supersedes gate keys off LastResult in {pass, skipped}, so a
// worst-of bump to "fail" or "error" must not stop the task error from
// winning.
func TestMapPhase3D_NewExecuteErrorTaskNotHiddenByWorstOfFrameworks(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-ttt2222-taskerr.md"), `---
test_case_id: tc-ttt2222
title: Newer task error + sibling fail
requirement: REQ-T
---
`)
	// Sibling-framework terminal fail (older than the task error).
	seedLegacyRecord(t, root, legacyRecord{
		TC: "tc-ttt2222", Framework: "playwright",
		Result:     "fail",
		ExecutedAt: "2026-05-19T08:00:00Z",
	})
	// Bats wiring with no overlay — the picker will select this.
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-ttt2222", Framework: "bats"})

	// Errored execute task on bats, created after the sibling fail.
	mkdirAll(t, root, filepath.Join("gtms/tasks", "error"))
	writeFile(t, root, filepath.Join("gtms/tasks", "error",
		"task-eeeeffff-execute-tc-ttt2222.md"), `---
id: task-eeeeffff
type: execute
target: tc-ttt2222
adapter: bats-runner
status: error
created: 2026-05-19T12:00:00Z
branch: feature/exec
---
`)

	report, err := Map(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, report.Groups, 1)
	entry := report.Groups[0].TestCases[0]

	assert.Equal(t, "error", entry.ExecuteStatus,
		"newer execute-error task must win over a sibling-framework "+
			"terminal fail — the task is the final overlay")
}

// TestMapPhase3D_PassRecordSupersedesStaleExecuteErrorTask pins that
// the applyTaskStatus record-supersedes path still functions after the
// worst-of reorder: when only one framework is wired and its terminal
// result is "pass" (newer than a stale execute-error task), the pass
// record supersedes the task error and ExecuteStatus stays "complete".
// This is the inverse guarantee — we did not silently regress the
// existing single-framework supersede behaviour.
func TestMapPhase3D_PassRecordSupersedesStaleExecuteErrorTask(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-ttt3333-passwin.md"), `---
test_case_id: tc-ttt3333
title: Pass record supersedes stale task error
requirement: REQ-T
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC: "tc-ttt3333", Framework: "bats",
		Result:     "pass",
		ExecutedAt: "2026-05-19T13:00:00Z",
	})

	// Older execute-error task — should be superseded by the passing record.
	mkdirAll(t, root, filepath.Join("gtms/tasks", "error"))
	writeFile(t, root, filepath.Join("gtms/tasks", "error",
		"task-99998888-execute-tc-ttt3333.md"), `---
id: task-99998888
type: execute
target: tc-ttt3333
adapter: bats-runner
status: error
created: 2026-05-19T11:00:00Z
branch: feature/exec
---
`)

	report, err := Map(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, report.Groups, 1)
	entry := report.Groups[0].TestCases[0]

	assert.Equal(t, "pass", entry.LastResult)
	assert.Equal(t, "complete", entry.ExecuteStatus,
		"newer pass record must still supersede an older execute-error task")
}

// TestMapPhase3D_PendingExecuteTaskVisibleWhenNoOverlay confirms the
// pre-existing semantic survives the reorder: when no wired framework
// has a terminal overlay (and there's no sibling failure to amplify),
// a pending execute task remains visible. (Pending overrides only when
// ExecuteStatus was "none" — that gate is unchanged; we just ensure the
// reorder didn't accidentally flip it.)
func TestMapPhase3D_PendingExecuteTaskVisibleWhenNoOverlay(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-ttt4444-pending.md"), `---
test_case_id: tc-ttt4444
title: Pending execute, no overlay anywhere
requirement: REQ-T
---
`)
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-ttt4444", Framework: "bats"})

	mkdirAll(t, root, filepath.Join("gtms/tasks", "pending"))
	writeFile(t, root, filepath.Join("gtms/tasks", "pending",
		"task-77776666-execute-tc-ttt4444.md"), `---
id: task-77776666
type: execute
target: tc-ttt4444
adapter: bats-runner
status: pending
created: 2026-05-19T14:00:00Z
branch: feature/exec
---
`)

	report, err := Map(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, report.Groups, 1)
	entry := report.Groups[0].TestCases[0]

	assert.Equal(t, "pending", entry.ExecuteStatus,
		"pending execute task must surface on a wired TC with no overlay")
}

// TestMapPhase3D_MissingArtefactDoesNotInventGapCategory pins that a
// missing-artefact wiring record surfaces via Frameworks[].ArtefactPresent
// and the existing Stale flag (when relevant) — it does NOT add a new
// top-level gap category to the map.
func TestMapPhase3D_MissingArtefactDoesNotInventGapCategory(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-jjj1111-missing.md"), `---
test_case_id: tc-jjj1111
title: Missing artefact TC
requirement: REQ-MA
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC: "tc-jjj1111", Framework: "bats",
		Artefact:         "test/acceptance/tc-jjj1111-not-on-disk.bats",
		ArtefactHash:     "aaaaaaaaaaaaaaaa",
		SkipArtefactFile: true,
	})

	report, err := Map(root, nil, "", false)
	require.NoError(t, err)
	entry := report.Groups[0].TestCases[0]

	assert.True(t, entry.Wired,
		"missing artefact does not retract wired state")
	require.Len(t, entry.Frameworks, 1)
	assert.False(t, entry.Frameworks[0].ArtefactPresent,
		"frameworks[].artefact_present:false signals missing artefact")
}
