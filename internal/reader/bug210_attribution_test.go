package reader

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BUG-210 AC14a: focused reader tests proving a reserved-adapter (manual)
// execute error does not override a selected non-manual framework view, while
// staying visible for the manual view and for unwired/manual-only test cases.
// The mechanism under test is applyTaskStatus's error-override gate.

// manualErrorTask is a terminal execute error task produced by a reserved Mode 3
// (manual) adapter.
func manualErrorTask(target, created string) taskFrontmatter {
	return taskFrontmatter{
		ID:      "task-manualerr",
		Type:    "execute",
		Target:  target,
		Adapter: "manual-execute-script",
		Status:  "error",
		Created: created,
	}
}

// Scenario (a)+(b): an fx pass followed by a later reserved-adapter manual error.
// The explicit fx view must remain pass/complete -- the manual error is excluded.
func TestApplyTaskStatus_ManualErrorDoesNotOverrideFxView(t *testing.T) {
	entry := &PipelineEntry{
		TestCaseID:        "tc-11111111",
		SelectedFramework: "fx",
		ExecuteStatus:     "complete",
		LastResult:        "pass",
		// LastRunAt intentionally empty so recordSupersedes cannot be what
		// keeps the pass -- the exclusion of the reserved-adapter error is.
	}
	applyTaskStatus(entry, []taskFrontmatter{manualErrorTask("tc-11111111", "2026-05-19T12:00:00Z")})
	assert.Equal(t, "complete", entry.ExecuteStatus,
		"a reserved-adapter manual error must not mark the fx view errored")
}

// Control: a real fx-runner execute error on the fx view MUST still win. This
// proves the gate is narrow (reserved adapters only), not a blanket suppression.
func TestApplyTaskStatus_RealFxErrorStillOverridesFxView(t *testing.T) {
	entry := &PipelineEntry{
		TestCaseID:        "tc-11111111",
		SelectedFramework: "fx",
		ExecuteStatus:     "complete",
		LastResult:        "pass",
	}
	realFxError := taskFrontmatter{
		ID: "task-fxerr", Type: "execute", Target: "tc-11111111",
		Adapter: "fx-runner", Status: "error", Created: "2026-05-19T12:00:00Z",
	}
	applyTaskStatus(entry, []taskFrontmatter{realFxError})
	assert.Equal(t, "error", entry.ExecuteStatus,
		"a real automated-adapter error must still override the fx view")
}

// Scenario (c): the explicit manual view must show the manual error.
func TestApplyTaskStatus_ManualErrorShowsInManualView(t *testing.T) {
	entry := &PipelineEntry{
		TestCaseID:        "tc-11111111",
		SelectedFramework: "manual",
		ExecuteStatus:     "complete",
	}
	applyTaskStatus(entry, []taskFrontmatter{manualErrorTask("tc-11111111", "2026-05-19T12:00:00Z")})
	assert.Equal(t, "error", entry.ExecuteStatus,
		"the manual view must keep the manual error visible")
}

// Scenario (d): a truly unwired / manual-only test case (not wired, no selected
// framework) must keep its manual error visible.
func TestApplyTaskStatus_ManualErrorVisibleForUnwiredTC(t *testing.T) {
	entry := &PipelineEntry{
		TestCaseID:        "tc-33333333",
		SelectedFramework: "", // unwired / manual-only
		Wired:             false,
		ExecuteStatus:     "complete",
	}
	applyTaskStatus(entry, []taskFrontmatter{manualErrorTask("tc-33333333", "2026-05-19T12:00:00Z")})
	assert.Equal(t, "error", entry.ExecuteStatus,
		"an unwired/manual-only test case must keep its manual error visible")
}

// REV-272 CLAUDE-002: a WIRED test case viewed under a non-manual framework it has
// no wiring for leaves SelectedFramework empty (pickWiring returns nil), but it is
// still a non-manual view, not an unwired/manual-only case. The manual error must
// be hidden. Fails on the previous code, whose nonManualView required a non-empty
// SelectedFramework and so showed the error here.
func TestApplyTaskStatus_ManualErrorHiddenForWiredUnmatchedFrameworkView(t *testing.T) {
	entry := &PipelineEntry{
		TestCaseID:         "tc-11111111",
		RequestedFramework: "fy", // explicit --framework fy, which the TC has no wiring for
		SelectedFramework:  "",   // so nothing was selected
		Wired:              true,
		ExecuteStatus:      "complete",
	}
	applyTaskStatus(entry, []taskFrontmatter{manualErrorTask("tc-11111111", "2026-05-19T12:00:00Z")})
	assert.Equal(t, "complete", entry.ExecuteStatus,
		"a wired test case under an unmatched non-manual framework must hide the manual error")
}

// REV-273 CLAUDE-001 / CODEX-002: a strict --framework manual view of a wired test
// case whose manual record is missing or unparseable leaves SelectedFramework
// empty, but RequestedFramework is "manual", so it is a manual view and the
// reserved-adapter error MUST stay visible. Fails on the REV-272 code, whose
// nonManualView (SelectedFramework!="manual" && (SelectedFramework!="" || Wired))
// treated this as a non-manual view and hid the error.
func TestApplyTaskStatus_ManualViewShowsErrorWithoutSelectedFramework(t *testing.T) {
	entry := &PipelineEntry{
		TestCaseID:         "tc-11111111",
		RequestedFramework: "manual", // explicit --framework manual
		SelectedFramework:  "",        // no parseable manual record -> nothing selected
		Wired:              true,
		ExecuteStatus:      "complete",
	}
	applyTaskStatus(entry, []taskFrontmatter{manualErrorTask("tc-11111111", "2026-05-19T12:00:00Z")})
	assert.Equal(t, "error", entry.ExecuteStatus,
		"a strict --framework manual view must show the manual error even with no selected manual row")
}

// Scenario (e): end-to-end through the readers. For an fx-wired test case with a
// real fx pass and a later reserved-adapter manual error task, the map compact
// state and the per-framework fx row must agree on pass, while the explicit
// manual view surfaces the error.
func TestReader_FxPassSurvivesManualErrorAcrossMapAndStatus(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-11111111-fixture.md"), `---
gtms_schema: "test-case/v2"
type: Test Case Spec
test_type: Functional
test_case_id: tc-11111111
title: Fx pass survives a manual error
requirement: BUG-210-FIXTURE
---
`)
	// Real fx pass.
	seedLegacyRecord(t, root, legacyRecord{
		TC: "tc-11111111", Framework: "fx", Adapter: "fx-runner",
		Result: "pass", ExecutedAt: "2026-05-19T10:00:00Z",
	})
	// Manual record (recorded fail) so the --framework manual view resolves.
	seedLegacyRecord(t, root, legacyRecord{
		TC: "tc-11111111", Framework: "manual", Result: "fail",
	})
	// A later reserved-adapter manual execute error task.
	mkdirAll(t, root, filepath.Join("gtms/tasks", "error"))
	writeFile(t, root, filepath.Join("gtms/tasks", "error",
		"task-manualerr-execute-tc-11111111.md"), `---
id: task-manualerr
type: execute
target: tc-11111111
adapter: manual-execute-script
status: error
created: 2026-05-19T12:00:00Z
branch: feature/exec
---
`)

	// Map compact state (default view) must stay complete, not error.
	report, err := Map(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, report.Groups, 1)
	mapEntry := report.Groups[0].TestCases[0]
	assert.Equal(t, "complete", mapEntry.ExecuteStatus,
		"map compact execute_status must remain complete (the fx pass), not the manual error")

	// The per-framework fx row must still be a pass.
	var fxRow *FrameworkEntry
	for i := range mapEntry.Frameworks {
		if mapEntry.Frameworks[i].Framework == "fx" {
			fxRow = &mapEntry.Frameworks[i]
		}
	}
	require.NotNil(t, fxRow, "fx framework row must exist")
	assert.Equal(t, "pass", fxRow.LastResultHere, "the fx row must still report pass")

	// Status default (fx) view: ExecuteStatus complete.
	detailFx, err := PipelineDetail(root, "tc-11111111", "", false)
	require.NoError(t, err)
	assert.Equal(t, "complete", detailFx.ExecuteStatus,
		"status default view must remain complete")

	// Explicit manual view: the manual error is visible.
	detailManual, err := PipelineDetail(root, "tc-11111111", "manual", true)
	require.NoError(t, err)
	assert.Equal(t, "error", detailManual.ExecuteStatus,
		"the --framework manual view must surface the manual error")
}

// REV-272 CLAUDE-002: end-to-end through status and map. An fx-wired test case with
// a reserved-adapter manual error, viewed strictly under a non-manual framework it
// has no wiring for (fy), must not surface the manual error -- it is a wired,
// non-manual view, not an unwired/manual-only case.
func TestReader_WiredUnmatchedFrameworkViewHidesManualError(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-11111111-fixture.md"), `---
gtms_schema: "test-case/v2"
type: Test Case Spec
test_type: Functional
test_case_id: tc-11111111
title: Wired case under an unmatched framework
requirement: BUG-210-FIXTURE
---
`)
	// Wired to fx only (no runtime result needed).
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-11111111", Framework: "fx", Adapter: "fx-runner"})
	// A reserved-adapter manual execute error task.
	mkdirAll(t, root, filepath.Join("gtms/tasks", "error"))
	writeFile(t, root, filepath.Join("gtms/tasks", "error",
		"task-manualerr-execute-tc-11111111.md"), `---
id: task-manualerr
type: execute
target: tc-11111111
adapter: manual-execute-script
status: error
created: 2026-05-19T12:00:00Z
branch: feature/exec
---
`)

	// Strict status view of framework fy (the TC has no fy wiring): error hidden.
	detail, err := PipelineDetail(root, "tc-11111111", "fy", true)
	require.NoError(t, err)
	assert.NotEqual(t, "error", detail.ExecuteStatus,
		"a wired TC viewed under an unmatched non-manual framework must not show the manual error in status")

	// Map under the same strict framework filter: error hidden on the compact state.
	report, err := Map(root, nil, "fy", true)
	require.NoError(t, err)
	require.Len(t, report.Groups, 1)
	assert.NotEqual(t, "error", report.Groups[0].TestCases[0].ExecuteStatus,
		"map under an unmatched non-manual framework must not show the manual error")
}

// REV-273 CLAUDE-001 / CODEX-002 end-to-end: a wired test case with a
// reserved-adapter manual error, viewed strictly under --framework manual with NO
// manual record (and separately with an UNPARSEABLE one), must keep the error
// visible in status. Fails on the REV-272 reader, which hid it.
func TestReader_WiredManualViewShowsErrorWithMissingOrUnparseableRecord(t *testing.T) {
	seedWiredTCWithManualError := func(t *testing.T, root string) {
		writeFile(t, root, filepath.Join("gtms/test/cases", "tc-11111111-fixture.md"), `---
gtms_schema: "test-case/v2"
type: Test Case Spec
test_type: Functional
test_case_id: tc-11111111
title: Wired case, manual view, no usable manual record
requirement: BUG-210-FIXTURE
---
`)
		seedLegacyRecord(t, root, legacyRecord{TC: "tc-11111111", Framework: "fx", Adapter: "fx-runner"})
		mkdirAll(t, root, filepath.Join("gtms/tasks", "error"))
		writeFile(t, root, filepath.Join("gtms/tasks", "error",
			"task-manualerr-execute-tc-11111111.md"), `---
id: task-manualerr
type: execute
target: tc-11111111
adapter: manual-execute-script
status: error
created: 2026-05-19T12:00:00Z
branch: feature/exec
---
`)
	}

	t.Run("missing manual record", func(t *testing.T) {
		root := t.TempDir()
		seedWiredTCWithManualError(t, root)
		// No manual record written at all.
		detail, err := PipelineDetail(root, "tc-11111111", "manual", true)
		require.NoError(t, err)
		assert.Equal(t, "error", detail.ExecuteStatus,
			"--framework manual with a missing manual record must still show the manual error")
	})

	t.Run("unparseable manual record", func(t *testing.T) {
		root := t.TempDir()
		seedWiredTCWithManualError(t, root)
		mkdirAll(t, root, filepath.Join("gtms/manual", "records"))
		writeFile(t, root, filepath.Join("gtms/manual", "records",
			"tc-11111111--manual.result.yaml"), "- this is not a mapping\n")
		detail, err := PipelineDetail(root, "tc-11111111", "manual", true)
		require.NoError(t, err)
		assert.Equal(t, "error", detail.ExecuteStatus,
			"--framework manual with an unparseable manual record must still show the manual error")
	})
}

// REV-273 implementation note: RequestedFramework carries only internal view
// context and must change no JSON output. It is json:"-" and absent from
// PipelineEntry.MarshalJSON, so marshalling an entry is identical whether or not
// RequestedFramework is set, and the serialised bytes never name it.
func TestPipelineEntry_RequestedFrameworkNotSerialised(t *testing.T) {
	base := PipelineEntry{TestCaseID: "tc-11111111", Wired: true, SelectedFramework: "fx"}
	withReq := base
	withReq.RequestedFramework = "manual"

	a, err := json.Marshal(base)
	require.NoError(t, err)
	b, err := json.Marshal(withReq)
	require.NoError(t, err)

	assert.JSONEq(t, string(a), string(b),
		"RequestedFramework must not change serialised PipelineEntry JSON")
	assert.False(t, strings.Contains(strings.ToLower(string(b)), "requested"),
		"serialised JSON must not name the requested-framework field")
}
