package cli

// CON-023 / ENH-145 / ENH-146 — Phase 3D map CLI contract.
//
// Pins the JSON wire shape emitted by `gtms map --json` for the wiring
// cutover. Mirrors what status_enh146_test.go does for `gtms status`.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/output"
	"github.com/aitestmanagement/gtms-cli/internal/reader"
)

// TestMapPhase3D_CLIJSON_WiredEntryShape pins that a wired TC's
// per-entry JSON carries the new ENH-146 wiring fields alongside the
// preserved legacy compact fields.
func TestMapPhase3D_CLIJSON_WiredEntryShape(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/test/cases", "tc-aaa1111-wired.md"), `---
test_case_id: tc-aaa1111
title: Wired TC
requirement: REQ-A
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC: "tc-aaa1111", Framework: "bats", Result: "pass",
		ExecutedAt: "2026-05-19T10:01:00Z",
	})

	var buf bytes.Buffer
	require.NoError(t, runMap(&buf, root, nil, false, "", true, "", false))

	var report map[string]interface{}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &report))
	require.Contains(t, report, "groups")

	groups := report["groups"].([]interface{})
	require.Len(t, groups, 1)
	group := groups[0].(map[string]interface{})
	tcs := group["test_cases"].([]interface{})
	require.Len(t, tcs, 1)
	entry := tcs[0].(map[string]interface{})

	// New wiring fields.
	assert.Equal(t, true, entry["wired"])
	assert.Equal(t, false, entry["manual_ready"])
	assert.Equal(t, "bats", entry["selected_framework"],
		"selected_framework must serialise as the framework name when wired")

	frameworks := entry["frameworks"].([]interface{})
	require.Len(t, frameworks, 1)
	fe := frameworks[0].(map[string]interface{})
	assert.Equal(t, "bats", fe["framework"])
	assert.Equal(t, true, fe["wired"])
	assert.Equal(t, true, fe["artefact_present"])
	assert.Equal(t, "complete", fe["last_status_here"])
	assert.Equal(t, "pass", fe["last_result_here"])

	// Preserved compact fields the existing CLI consumers depend on.
	assert.Equal(t, "complete", entry["automate_status"])
	assert.Equal(t, "complete", entry["execute_status"])
	assert.Equal(t, "pass", entry["last_result"])
}

// TestMapPhase3D_CLIJSON_NotWiredSerialisesSelectedFrameworkAsNull pins
// the JSON null contract for selected_framework when no wiring exists.
func TestMapPhase3D_CLIJSON_NotWiredSerialisesSelectedFrameworkAsNull(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/test/cases", "tc-bbb1111-bare.md"), `---
test_case_id: tc-bbb1111
title: Bare TC
requirement: REQ-B
---
`)

	var buf bytes.Buffer
	require.NoError(t, runMap(&buf, root, nil, false, "", true, "", false))

	var report map[string]interface{}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &report))
	groups := report["groups"].([]interface{})
	require.Len(t, groups, 1)
	tcs := groups[0].(map[string]interface{})["test_cases"].([]interface{})
	require.Len(t, tcs, 1)
	entry := tcs[0].(map[string]interface{})

	assert.Equal(t, false, entry["wired"])
	require.Contains(t, entry, "selected_framework")
	assert.Nil(t, entry["selected_framework"],
		"selected_framework must serialise as JSON null when no wiring framework qualifies")
	assert.Equal(t, []interface{}{}, entry["frameworks"],
		"frameworks must serialise as [] (not null) when there is no wiring")
}

// TestMapPhase3D_CLIJSON_MultiFrameworkWorstOfRule pins that the CLI
// JSON wire shape carries the worst-of-frameworks compact LastResult so
// a sibling-framework failure cannot hide behind the picker's pass on
// the table view.
func TestMapPhase3D_CLIJSON_MultiFrameworkWorstOfRule(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/test/cases", "tc-ccc1111-multifw.md"), `---
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

	var buf bytes.Buffer
	require.NoError(t, runMap(&buf, root, nil, false, "", true, "", false))

	var report map[string]interface{}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &report))
	groups := report["groups"].([]interface{})
	tcs := groups[0].(map[string]interface{})["test_cases"].([]interface{})
	entry := tcs[0].(map[string]interface{})

	// Every wired framework must be present in the JSON.
	frameworks := entry["frameworks"].([]interface{})
	require.Len(t, frameworks, 2,
		"both wired frameworks must appear in frameworks[]")

	// Compact human row must not lie about the TC's worst outcome.
	assert.Equal(t, "fail", entry["last_result"],
		"compact last_result must surface the sibling framework's fail")
}

// TestMapPhase3D_CLIText_MultiFrameworkRendersFailIcon pins that the
// text renderer's worst-of compact icon surfaces the sibling-framework
// failure rather than the picker-selected pass.
func TestMapPhase3D_CLIText_MultiFrameworkRendersFailIcon(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/test/cases", "tc-ddd1111-mixed.md"), `---
test_case_id: tc-ddd1111
title: Mixed outcome TC
requirement: REQ-D
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC: "tc-ddd1111", Framework: "bats", Result: "pass",
	})
	seedLegacyRecord(t, root, legacyRecord{
		TC: "tc-ddd1111", Framework: "playwright", Result: "fail",
	})

	var buf bytes.Buffer
	require.NoError(t, runMap(&buf, root, nil, false, "", false, "", false))

	out := buf.String()
	// The fail icon (output.IconError) is "✗" — the EXECUTE column
	// must render fail (not the picker-selected pass).
	assert.Contains(t, out, "EXECUTE ✗",
		"EXECUTE column must surface the sibling fail, not the picker's pass")
}

// TestMapPhase3D_CLIJSON_OrphanResultDoesNotLeak pins that a terminal
// handoff for a TC with no wiring does not surface on the map JSON as
// wired/passing.
func TestMapPhase3D_CLIJSON_OrphanResultDoesNotLeak(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/test/cases", "tc-eee1111-orphan.md"), `---
test_case_id: tc-eee1111
title: Orphan result TC
requirement: REQ-O
---
`)
	resultsDir := filepath.Join(root, ".gtms", "results")
	require.NoError(t, os.MkdirAll(resultsDir, 0o755))
	handoff := `task: task-orphan-1
command: execute
target: tc-eee1111
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

	var buf bytes.Buffer
	require.NoError(t, runMap(&buf, root, nil, false, "", true, "", false))

	var report map[string]interface{}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &report))
	groups := report["groups"].([]interface{})
	require.Len(t, groups, 1)
	tcs := groups[0].(map[string]interface{})["test_cases"].([]interface{})
	require.Len(t, tcs, 1)
	entry := tcs[0].(map[string]interface{})

	assert.Equal(t, false, entry["wired"],
		"orphan result must not flip wired=true on the JSON contract")
	assert.Nil(t, entry["selected_framework"])
	assert.Equal(t, "none", entry["last_result"],
		"orphan result must not leak through to compact last_result")
}

// --- Phase 3D fix-pass: formatExecuteIcon precedence ---

// TestMapPhase3D_CLIText_InProgressTaskOverridesSiblingFailIcon pins
// the second fix-pass decision: the EXECUTE column icon surfaces an
// active in-progress execute task even when worst-of-frameworks has
// bumped LastResult to a sibling framework's terminal fail.
// applyTaskStatus is the authoritative final overlay for ExecuteStatus,
// and the icon renderer must respect that — otherwise the visual lies
// about the TC's most recent state.
func TestMapPhase3D_CLIText_InProgressTaskOverridesSiblingFailIcon(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/test/cases", "tc-iii1111-inflight.md"), `---
test_case_id: tc-iii1111
title: In-flight with sibling fail
requirement: REQ-I
---
`)
	// Sibling-framework terminal fail (would dominate LastResult
	// post-worst-of without the renderer fix).
	seedLegacyRecord(t, root, legacyRecord{
		TC: "tc-iii1111", Framework: "playwright", Result: "fail",
	})
	// Bats wiring with no terminal overlay — picker target for the task.
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-iii1111", Framework: "bats"})

	// Active in-progress execute task on bats.
	require.NoError(t, os.MkdirAll(
		filepath.Join(root, "gtms", "tasks", "in-progress"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "gtms", "tasks", "in-progress",
			"task-iiiijjjj-execute-tc-iii1111.md"),
		[]byte(`---
id: task-iiiijjjj
type: execute
target: tc-iii1111
adapter: bats-runner
status: in-progress
created: 2026-05-19T11:00:00Z
branch: feature/exec
---
`), 0o644))

	var buf bytes.Buffer
	require.NoError(t, runMap(&buf, root, nil, false, "", false, "", false))

	out := buf.String()
	// IconInProgress = "●". Must be in the EXECUTE column.
	assert.Contains(t, out, "EXECUTE ●",
		"EXECUTE column must render the in-progress icon when the "+
			"task is active, not the sibling framework's fail icon")
	// IconError = "✗". Must NOT be in the EXECUTE column (it would
	// indicate the sibling fail is winning over the task state).
	assert.NotContains(t, out, "EXECUTE ✗",
		"EXECUTE column must not render the sibling fail icon when "+
			"an in-progress execute task is active on the TC")
}

// TestMapPhase3D_CLIText_TaskErrorOverridesSiblingFailIcon pins that a
// newer execute-error task surfaces as the EXECUTE column icon (⚠)
// even when worst-of-frameworks has bumped LastResult to a sibling
// framework's fail. Same rationale as the in-progress case.
func TestMapPhase3D_CLIText_TaskErrorOverridesSiblingFailIcon(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/test/cases", "tc-iii2222-taskerr.md"), `---
test_case_id: tc-iii2222
title: Task error with older sibling fail
requirement: REQ-I
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC: "tc-iii2222", Framework: "playwright",
		Result:     "fail",
		ExecutedAt: "2026-05-19T08:00:00Z",
	})
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-iii2222", Framework: "bats"})

	require.NoError(t, os.MkdirAll(
		filepath.Join(root, "gtms", "tasks", "error"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "gtms", "tasks", "error",
			"task-kkkkllll-execute-tc-iii2222.md"),
		[]byte(`---
id: task-kkkkllll
type: execute
target: tc-iii2222
adapter: bats-runner
status: error
created: 2026-05-19T12:00:00Z
branch: feature/exec
---
`), 0o644))

	var buf bytes.Buffer
	require.NoError(t, runMap(&buf, root, nil, false, "", false, "", false))

	out := buf.String()
	// IconWarning = "⚠".
	assert.Contains(t, out, "EXECUTE ⚠",
		"EXECUTE column must render the warning icon for a newer "+
			"execute-error task, not the sibling fail icon")
	assert.NotContains(t, out, "EXECUTE ✗",
		"EXECUTE column must not render the sibling fail icon when "+
			"a newer execute-error task overrides it")
}

// TestMapPhase3D_CLIText_TaskErrorWithNoOverlayRendersWarningIcon pins
// the third reviewer-requested case: a wired TC with NO terminal
// overlay anywhere (so worst-of contributes nothing) and an
// execute-error task on the picker's framework must render the
// warning icon (⚠) on the EXECUTE column, not the em-dash that
// pre-fix code would have rendered (LastResult="none" had no matching
// branch and ExecuteStatus="error" wasn't checked before LastResult).
func TestMapPhase3D_CLIText_TaskErrorWithNoOverlayRendersWarningIcon(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/test/cases", "tc-iii3333-noverlay-err.md"), `---
test_case_id: tc-iii3333
title: Task error, no overlay anywhere
requirement: REQ-I
---
`)
	// Wiring exists but NO terminal handoff for any framework.
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-iii3333", Framework: "bats"})

	// Execute-error task on bats — applyTaskStatus's record-supersedes
	// check finds LastResult="none" and lets the task error through.
	require.NoError(t, os.MkdirAll(
		filepath.Join(root, "gtms", "tasks", "error"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "gtms", "tasks", "error",
			"task-mmmnnnoo-execute-tc-iii3333.md"),
		[]byte(`---
id: task-mmmnnnoo
type: execute
target: tc-iii3333
adapter: bats-runner
status: error
created: 2026-05-19T12:00:00Z
branch: feature/exec
---
`), 0o644))

	var buf bytes.Buffer
	require.NoError(t, runMap(&buf, root, nil, false, "", false, "", false))

	out := buf.String()
	// IconWarning = "⚠". Must be in the EXECUTE column.
	assert.Contains(t, out, "EXECUTE ⚠",
		"EXECUTE column must render the warning icon for a task-derived "+
			"execute-error even when no terminal overlay exists on any framework")
	// Em-dash "—" must NOT be the EXECUTE column glyph here — that
	// would mean the renderer fell through to the default for
	// ExecuteStatus=error with LastResult=none (the pre-fix bug).
	assert.NotContains(t, out, "EXECUTE —",
		"EXECUTE column must not render em-dash when an execute-error "+
			"task is the most recent state on the TC")
}

// TestFormatExecuteIcon_TaskInProgressOverridesLastResult is a focused
// unit test on the icon precedence rule: in-progress beats every
// LastResult value.
func TestFormatExecuteIcon_TaskInProgressOverridesLastResult(t *testing.T) {
	for _, last := range []string{"pass", "fail", "error", "skipped", "none", ""} {
		got := formatExecuteIcon(reader.MapEntry{
			ExecuteStatus: "in-progress",
			LastResult:    last,
		})
		assert.Equalf(t, output.IconInProgress, got,
			"in-progress must dominate LastResult=%q", last)
	}
}

// TestFormatExecuteIcon_TaskErrorOverridesLastResult is a focused unit
// test on the icon precedence rule: ExecuteStatus="error" (task-derived
// or terminal adapter-error) beats LastResult fail/skipped/pass.
func TestFormatExecuteIcon_TaskErrorOverridesLastResult(t *testing.T) {
	for _, last := range []string{"pass", "fail", "skipped", "none", ""} {
		got := formatExecuteIcon(reader.MapEntry{
			ExecuteStatus: "error",
			LastResult:    last,
		})
		assert.Equalf(t, output.IconWarning, got,
			"ExecuteStatus=error must dominate LastResult=%q", last)
	}
}

// TestFormatExecuteIcon_PendingStillSilencedByTerminalLastResult pins
// the intentional preserved pending semantic: a pending execute task
// stays silenced once any terminal LastResult exists. Matches the
// single-framework and gtms-status conventions.
func TestFormatExecuteIcon_PendingStillSilencedByTerminalLastResult(t *testing.T) {
	cases := []struct {
		last string
		want string
	}{
		{"pass", output.IconComplete},
		{"fail", output.IconError},
		{"error", output.IconWarning},
		{"skipped", output.IconSkipped},
	}
	for _, c := range cases {
		got := formatExecuteIcon(reader.MapEntry{
			ExecuteStatus: "pending",
			LastResult:    c.last,
		})
		assert.Equalf(t, c.want, got,
			"pending must stay silenced by terminal LastResult=%q", c.last)
	}
}
