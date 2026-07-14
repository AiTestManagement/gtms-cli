package cli

// CON-023 / ENH-146 Phase 3C — Status Summary/Detail Contract.
//
// These tests pin the ENH-146 JSON shape on both gtms status --json
// (overview) and gtms status <tc> --json (detail), and the overlay
// terminal-handoff discipline (pending/in-progress excluded; status:error
// renders as execution error, not pass/fail).
//
// They run alongside the existing status_test.go to keep the contract
// visible at a glance — anything that breaks here is an ENH-146 contract
// regression, not an incidental test-fixture detail.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/reader"
)

// --- Overview --json shape: per-TC scalars + nested frameworks[] ---

// TestENH146_Overview_NormalWiredHasPinnedShape verifies a wired TC with a
// passing terminal handoff emits the pinned per-TC ENH-146 shape and the
// per-framework overlay.
func TestENH146_Overview_NormalWiredHasPinnedShape(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/test/cases", "tc-eee0001-x.md"),
		"---\ntest_case_id: tc-eee0001\ntitle: Wired pass\n---\n")
	seedLegacyRecord(t, root, legacyRecord{
		TC: "tc-eee0001", Framework: "bats", Result: "pass",
		ExecutedAt: "2026-05-19T10:01:00Z",
	})

	var buf bytes.Buffer
	require.NoError(t, runStatusOverview(&buf, root, nil, true, "", false))

	var entries []reader.PipelineEntry
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entries))
	require.Len(t, entries, 1)

	e := entries[0]
	assert.Equal(t, "tc-eee0001", e.TestCaseID)
	assert.True(t, e.Wired)
	assert.False(t, e.ManualReady)
	assert.Equal(t, "bats", e.SelectedFramework)
	require.Len(t, e.Frameworks, 1)

	fe := e.Frameworks[0]
	assert.Equal(t, "bats", fe.Framework)
	assert.True(t, fe.Wired)
	assert.Empty(t, fe.WiringDrift)
	assert.True(t, fe.ArtefactPresent)
	assert.NotEmpty(t, fe.Artefact)
	assert.Equal(t, "complete", fe.LastStatusHere)
	assert.Equal(t, "pass", fe.LastResultHere)
	assert.Equal(t, "2026-05-19T10:01:00Z", fe.LastExecutedHere)

	// Legacy carriers must not appear in the JSON contract.
	raw := buf.String()
	assertNoLegacyCarriersInOverviewJSON(t, raw)
}

// TestENH146_Overview_NotRunHere verifies a wired TC with no terminal
// handoff renders as the "wired, not run here" state — no overlay fields,
// no false pass/fail signal.
func TestENH146_Overview_NotRunHere(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/test/cases", "tc-eee0002-x.md"),
		"---\ntest_case_id: tc-eee0002\ntitle: Wired not run\n---\n")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-eee0002", Framework: "bats"})

	var buf bytes.Buffer
	require.NoError(t, runStatusOverview(&buf, root, nil, true, "", false))

	var entries []reader.PipelineEntry
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entries))
	require.Len(t, entries, 1)
	e := entries[0]

	assert.True(t, e.Wired)
	assert.Equal(t, "bats", e.SelectedFramework)
	require.Len(t, e.Frameworks, 1)
	fe := e.Frameworks[0]
	assert.True(t, fe.ArtefactPresent)
	assert.Empty(t, fe.LastStatusHere, "no terminal handoff → no last_status_here")
	assert.Empty(t, fe.LastResultHere, "no terminal handoff → no last_result_here")
	assert.Empty(t, fe.LastExecutedHere)
}

// TestENH146_Overview_StatusErrorRendersExecutionError verifies that a
// terminal handoff with status: error and no result renders as
// last_status_here:"error" / last_result_here unset — never as pass/fail.
func TestENH146_Overview_StatusErrorRendersExecutionError(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/test/cases", "tc-eee0003-x.md"),
		"---\ntest_case_id: tc-eee0003\ntitle: Adapter error\n---\n")
	seedLegacyRecord(t, root, legacyRecord{
		TC: "tc-eee0003", Framework: "bats", AdapterError: true,
		ExecutedAt: "2026-05-19T10:01:00Z",
	})

	var buf bytes.Buffer
	require.NoError(t, runStatusOverview(&buf, root, nil, true, "", false))
	var entries []reader.PipelineEntry
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entries))
	require.Len(t, entries, 1)
	require.Len(t, entries[0].Frameworks, 1)
	fe := entries[0].Frameworks[0]

	assert.Equal(t, "error", fe.LastStatusHere, "adapter failure → last_status_here:error")
	assert.Empty(t, fe.LastResultHere, "adapter failure has no test outcome")
	assert.NotEqual(t, "pass", fe.LastResultHere)
	assert.NotEqual(t, "fail", fe.LastResultHere)
}

// TestENH146_Overview_NonTerminalHandoffsExcluded hand-places pending and
// in-progress result files and verifies they are NOT consumed by the
// overlay (ENH-146 terminal-handoff discipline).
func TestENH146_Overview_NonTerminalHandoffsExcluded(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/test/cases", "tc-eee0004-x.md"),
		"---\ntest_case_id: tc-eee0004\ntitle: Pending only\n---\n")
	// Wiring only, no terminal handoff via the seed helper.
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-eee0004", Framework: "bats"})

	// Hand-place a pending handoff and an in-progress handoff. result.Validate
	// rejects status:pending / in-progress with non-empty result, so we write
	// the YAML directly (bypassing result.Create) — same shape an aborted
	// adapter run would leave on disk.
	resultsDir := filepath.Join(root, ".gtms", "results")
	require.NoError(t, os.MkdirAll(resultsDir, 0o755))
	pendingYAML := `task: task-eee0004-pending
command: execute
target: tc-eee0004
adapter: bats-runner
mode: sync
created: "2026-05-19T10:00:00Z"
status: pending
framework: bats
`
	require.NoError(t, os.WriteFile(filepath.Join(resultsDir, "task-eee0004-pending.handoff.yaml"),
		[]byte(pendingYAML), 0o644))
	inProgressYAML := `task: task-eee0004-inprog
command: execute
target: tc-eee0004
adapter: bats-runner
mode: sync
created: "2026-05-19T10:00:01Z"
status: in-progress
framework: bats
`
	require.NoError(t, os.WriteFile(filepath.Join(resultsDir, "task-eee0004-inprog.handoff.yaml"),
		[]byte(inProgressYAML), 0o644))

	var buf bytes.Buffer
	require.NoError(t, runStatusOverview(&buf, root, nil, true, "", false))
	var entries []reader.PipelineEntry
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entries))
	require.Len(t, entries, 1)
	require.Len(t, entries[0].Frameworks, 1)
	fe := entries[0].Frameworks[0]

	assert.Empty(t, fe.LastStatusHere,
		"non-terminal handoffs (pending/in-progress) must not contribute to overlay")
	assert.Empty(t, fe.LastResultHere)
	assert.Empty(t, fe.LastExecutedHere)
}

// TestENH146_Overview_MultiFrameworkPicker pins picker semantics:
// current-bats beats current-playwright on lexical tie-break (bats < playwright)
// when both are current+automated, and both records still appear in frameworks[].
func TestENH146_Overview_MultiFrameworkPicker(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/test/cases", "tc-eee0005-x.md"),
		"---\ntest_case_id: tc-eee0005\ntitle: Multi-fw\n---\n")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-eee0005", Framework: "bats", Result: "pass"})
	seedLegacyRecord(t, root, legacyRecord{
		TC: "tc-eee0005", Framework: "playwright",
		Artefact: "test/acceptance/tc-eee0005-pw.spec.ts",
		// No result — not-run.
	})

	var buf bytes.Buffer
	require.NoError(t, runStatusOverview(&buf, root, nil, true, "", false))
	var entries []reader.PipelineEntry
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entries))
	require.Len(t, entries, 1)
	e := entries[0]

	assert.Equal(t, "bats", e.SelectedFramework,
		"current automated bats outranks current automated playwright via lexical tie-break")
	require.Len(t, e.Frameworks, 2)
	// Frameworks[] sorted lexically.
	assert.Equal(t, "bats", e.Frameworks[0].Framework)
	assert.Equal(t, "playwright", e.Frameworks[1].Framework)
	assert.Equal(t, "pass", e.Frameworks[0].LastResultHere)
	assert.Empty(t, e.Frameworks[1].LastResultHere, "playwright has no terminal handoff")
}

// TestENH146_Overview_StaleTestcaseHash flips a TC spec after wiring is
// written and verifies wiring_drift:"testcase" surfaces in --json.
func TestENH146_Overview_StaleTestcaseHash(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/test/cases", "tc-eee0006-x.md"),
		"---\ntest_case_id: tc-eee0006\ntitle: original\n---\n")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-eee0006", Framework: "bats"})

	// Mutate the spec after wiring is written so testcase-hash diverges.
	writeTestFile(t, root, filepath.Join("gtms/test/cases", "tc-eee0006-x.md"),
		"---\ntest_case_id: tc-eee0006\ntitle: mutated\n---\n")

	var buf bytes.Buffer
	require.NoError(t, runStatusOverview(&buf, root, nil, true, "", false))
	var entries []reader.PipelineEntry
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entries))
	require.Len(t, entries, 1)
	require.Len(t, entries[0].Frameworks, 1)
	fe := entries[0].Frameworks[0]

	assert.Equal(t, "testcase", fe.WiringDrift, "spec content changed → testcase-hash drift")
	assert.True(t, fe.ArtefactPresent, "artefact still on disk")
}

// TestENH146_Overview_StaleArtefactHash flips the artefact after wiring is
// written and verifies wiring_drift:"artefact" surfaces in --json.
func TestENH146_Overview_StaleArtefactHash(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/test/cases", "tc-eee0007-x.md"),
		"---\ntest_case_id: tc-eee0007\ntitle: x\n---\n")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-eee0007", Framework: "bats"})

	// Mutate the artefact after wiring is written.
	artefact := filepath.Join(root, "test/acceptance/tc-eee0007.bats")
	require.NoError(t, os.WriteFile(artefact, []byte("# mutated\n"), 0o644))

	var buf bytes.Buffer
	require.NoError(t, runStatusOverview(&buf, root, nil, true, "", false))
	var entries []reader.PipelineEntry
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entries))
	require.Len(t, entries, 1)
	require.Len(t, entries[0].Frameworks, 1)
	fe := entries[0].Frameworks[0]

	assert.Equal(t, "artefact", fe.WiringDrift, "artefact content changed → artefact-hash drift")
	assert.True(t, fe.ArtefactPresent)
}

// TestENH146_Overview_MissingArtefact verifies the missing-artefact tier
// surfaces in --json with artefact_present:false and wiring_drift never
// claiming an artefact-hash mismatch when the artefact cannot be hashed.
func TestENH146_Overview_MissingArtefact(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/test/cases", "tc-eee0008-x.md"),
		"---\ntest_case_id: tc-eee0008\ntitle: missing\n---\n")
	seedLegacyRecord(t, root, legacyRecord{
		TC: "tc-eee0008", Framework: "bats",
		Artefact:         "test/acceptance/tc-eee0008-gone.bats",
		SkipArtefactFile: true,
	})

	var buf bytes.Buffer
	require.NoError(t, runStatusOverview(&buf, root, nil, true, "", false))
	var entries []reader.PipelineEntry
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entries))
	require.Len(t, entries, 1)
	require.Len(t, entries[0].Frameworks, 1)
	fe := entries[0].Frameworks[0]

	assert.False(t, fe.ArtefactPresent, "artefact path doesn't resolve")
	assert.NotEqual(t, "artefact", fe.WiringDrift,
		"missing artefact must not claim artefact-hash drift")
	assert.NotEqual(t, "both", fe.WiringDrift,
		"missing artefact must not claim 'both' drift")
}

// TestENH146_Overview_ManualOnlyTC verifies that manual-only TCs render
// as wired:false / manual_ready:true / frameworks:[] with no wiring row
// pretending to cover the TC. JSON contract: selected_framework
// serialises as `null` (asserted separately in the SelectedFrameworkIsNull
// raw-JSON tests); on the Go struct after json.Unmarshal it round-trips
// back to the empty string, which assert.Empty satisfies.
func TestENH146_Overview_ManualOnlyTC(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/test/cases", "tc-eee0009-x.md"),
		"---\ntest_case_id: tc-eee0009\ntitle: manual only\n---\n")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-eee0009", Framework: "manual", Result: "pass"})

	var buf bytes.Buffer
	require.NoError(t, runStatusOverview(&buf, root, nil, true, "", false))
	var entries []reader.PipelineEntry
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entries))
	require.Len(t, entries, 1)
	e := entries[0]

	assert.False(t, e.Wired, "manual-only TC: wired:false")
	assert.True(t, e.ManualReady, "manual-only TC: manual_ready:true")
	assert.Empty(t, e.SelectedFramework, "manual-only TC has no wiring → selected_framework empty")
	assert.Len(t, e.Frameworks, 0, "no wiring → frameworks[] is empty (manual row must NOT appear)")

	// No wiring directory should have been created either.
	wiringDir := filepath.Join(root, "gtms", "automation", "wiring")
	if _, err := os.Stat(wiringDir); err == nil {
		entries, _ := os.ReadDir(wiringDir)
		assert.Len(t, entries, 0,
			"manual-only TCs must not produce wiring files under %s", wiringDir)
	}
}

// TestENH146_Overview_OrphanResultFileIgnored verifies that a result file
// without a matching wiring record does not affect status output
// (ENH-146 Edge Case 5).
func TestENH146_Overview_OrphanResultFileIgnored(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/test/cases", "tc-eee0010-x.md"),
		"---\ntest_case_id: tc-eee0010\ntitle: orphan results\n---\n")

	// Hand-place a result file but no wiring.
	resultsDir := filepath.Join(root, ".gtms", "results")
	require.NoError(t, os.MkdirAll(resultsDir, 0o755))
	orphan := `task: task-eee0010-orphan
command: execute
target: tc-eee0010
adapter: bats-runner
mode: sync
created: "2026-05-19T10:00:00Z"
status: complete
result: pass
framework: bats
completed: "2026-05-19T10:01:00Z"
`
	require.NoError(t, os.WriteFile(filepath.Join(resultsDir, "task-eee0010-orphan.handoff.yaml"),
		[]byte(orphan), 0o644))

	var buf bytes.Buffer
	require.NoError(t, runStatusOverview(&buf, root, nil, true, "", false))
	var entries []reader.PipelineEntry
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entries))
	require.Len(t, entries, 1)
	e := entries[0]

	assert.False(t, e.Wired)
	assert.False(t, e.ManualReady)
	assert.Empty(t, e.SelectedFramework)
	assert.Len(t, e.Frameworks, 0,
		"orphan result file (no wiring) must not surface as a framework row")
}

// --- Detail --json shape: per-TC scalars + frameworks[] + result-contract fields ---

// TestENH146_Detail_PreservesResultContractFields verifies that a wired TC
// with a terminal handoff carrying summary / log / notes-spill / executed_by
// / environment surfaces those fields in the detail --json contract.
// (Git context is also surfaced when populated.)
func TestENH146_Detail_PreservesResultContractFields(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/test/cases", "tc-eee0011-x.md"),
		"---\ntest_case_id: tc-eee0011\ntitle: Detail probe\nrequirement: REQ-D\n---\n")

	// Use a hand-built terminal handoff so we can stamp git-* without
	// invoking the real git helpers in tests.
	wiringDir := filepath.Join(root, "gtms/automation/wiring")
	require.NoError(t, os.MkdirAll(wiringDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(wiringDir, "tc-eee0011--bats.wiring.yaml"),
		[]byte("testcase: tc-eee0011\ntestcase-hash: 0011223344556677\nframework: bats\nadapter: bats-runner\nartefact: test/acceptance/tc-eee0011.bats\nartefact-hash: aabbccddeeff0011\n"),
		0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "test/acceptance"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "test/acceptance/tc-eee0011.bats"),
		[]byte("# fixture\n"), 0o644))

	resultsDir := filepath.Join(root, ".gtms/results")
	require.NoError(t, os.MkdirAll(resultsDir, 0o755))
	handoff := `task: task-eee0011
command: execute
target: tc-eee0011
adapter: bats-runner
mode: sync
created: "2026-05-19T10:00:00Z"
status: complete
result: fail
framework: bats
completed: "2026-05-19T10:01:00Z"
summary: "1 of 3 tests failed"
log: |
  not ok 1 - login flow
  # in tc-eee0011.bats line 12
notes-spill: ".gtms/logs/task-eee0011.log"
executed_by: alice
environment: staging
git-commit: deadbeefcafebabe
git-branch: feature/foo
git-dirty: false
`
	require.NoError(t, os.WriteFile(filepath.Join(resultsDir, "task-eee0011.handoff.yaml"),
		[]byte(handoff), 0o644))

	var buf bytes.Buffer
	require.NoError(t, runStatusDetail(&buf, root, "tc-eee0011", true, "", false))

	var detail reader.PipelineDetailEntry
	require.NoError(t, json.Unmarshal(buf.Bytes(), &detail))

	// ENH-146 per-TC shape on detail.
	assert.True(t, detail.Wired)
	assert.False(t, detail.ManualReady)
	assert.Equal(t, "bats", detail.SelectedFramework)
	require.Len(t, detail.Frameworks, 1)
	fe := detail.Frameworks[0]

	// Result-contract fields preserved on the selected framework's entry.
	assert.Equal(t, "1 of 3 tests failed", fe.Summary, "summary lifted from result contract")
	assert.Contains(t, fe.LogExcerpt, "not ok 1 - login flow", "log lifted from result contract")
	assert.Equal(t, "alice", fe.ExecutedBy, "executed_by lifted from result contract")
	assert.Equal(t, "staging", fe.Environment, "environment lifted from result contract")
	assert.Equal(t, "deadbeefcafebabe", fe.GitCommit)
	assert.Equal(t, "feature/foo", fe.GitBranch)
	require.NotNil(t, fe.GitDirty, "git-dirty:false should preserve as non-nil pointer")
	assert.False(t, *fe.GitDirty)
	assert.Equal(t, "complete", fe.LastStatusHere)
	assert.Equal(t, "fail", fe.LastResultHere)
	assert.Equal(t, "2026-05-19T10:01:00Z", fe.LastExecutedHere)

	// notes / notes-spill stay at the detail top level (CLI text renderer
	// prints the block under the file-paths section). Notes mirrors the
	// log payload from the selected framework's overlay.
	assert.Contains(t, detail.Notes, "not ok 1 - login flow")
	assert.Equal(t, ".gtms/logs/task-eee0011.log", detail.NotesSpill)

	// Legacy carriers must not leak.
	raw := buf.String()
	assertNoLegacyCarriersInDetailJSON(t, raw)
}

// TestENH146_Detail_ManualOnlyHasNoFrameworksRow verifies a manual-only TC's
// detail view renders the per-TC ENH-146 shape (wired:false / manual_ready:true)
// and an empty frameworks[].
func TestENH146_Detail_ManualOnlyHasNoFrameworksRow(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/test/cases", "tc-eee0012-x.md"),
		"---\ntest_case_id: tc-eee0012\ntitle: manual only\n---\n")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-eee0012", Framework: "manual", Result: "pass"})

	var buf bytes.Buffer
	require.NoError(t, runStatusDetail(&buf, root, "tc-eee0012", true, "", false))

	var detail reader.PipelineDetailEntry
	require.NoError(t, json.Unmarshal(buf.Bytes(), &detail))

	assert.False(t, detail.Wired)
	assert.True(t, detail.ManualReady)
	assert.Empty(t, detail.SelectedFramework)
	assert.Len(t, detail.Frameworks, 0)
}

// TestENH146_Detail_LegacyCarriersAbsent verifies the named legacy carriers
// are NOT present in the detail --json output (they remain on the Go struct
// as table-renderer carriers, but are json:"-").
func TestENH146_Detail_LegacyCarriersAbsent(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/test/cases", "tc-eee0013-x.md"),
		"---\ntest_case_id: tc-eee0013\ntitle: t\nrequirement: R\n---\n")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-eee0013", Framework: "bats", Result: "pass"})

	var buf bytes.Buffer
	require.NoError(t, runStatusDetail(&buf, root, "tc-eee0013", true, "", false))
	assertNoLegacyCarriersInDetailJSON(t, buf.String())
}

// --- ENH-146 contract: testcase key + selected_framework null semantics ---

// TestENH146_Detail_TestcaseKeyPresent_TestCaseIdAbsent pins the JSON key
// rename from the legacy `test_case_id` to the ENH-146 pinned `testcase`.
// Raw assertions guard against the JSON-tag drift the struct-decode tests
// can't see (Go's encoder/decoder happily silently maps either key).
func TestENH146_Detail_TestcaseKeyPresent_TestCaseIdAbsent(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/test/cases", "tc-eee0014-x.md"),
		"---\ntest_case_id: tc-eee0014\ntitle: keytest\nrequirement: R\n---\n")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-eee0014", Framework: "bats", Result: "pass"})

	var buf bytes.Buffer
	require.NoError(t, runStatusDetail(&buf, root, "tc-eee0014", true, "", false))

	top := decodeJSONObject(t, buf.Bytes())
	assert.Contains(t, top, "testcase",
		"detail --json must emit pinned ENH-146 key `testcase`")
	assert.Equal(t, "tc-eee0014", top["testcase"])
	assert.NotContains(t, top, "test_case_id",
		"detail --json must not emit legacy key `test_case_id`")
}

// TestENH146_Overview_SelectedFrameworkIsNullForManualOnly verifies the
// JSON contract emits `selected_framework: null` (not `""`) for a TC
// with no qualifying wiring framework.
func TestENH146_Overview_SelectedFrameworkIsNullForManualOnly(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/test/cases", "tc-eee0015-x.md"),
		"---\ntest_case_id: tc-eee0015\ntitle: manual only\n---\n")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-eee0015", Framework: "manual", Result: "pass"})

	var buf bytes.Buffer
	require.NoError(t, runStatusOverview(&buf, root, nil, true, "", false))

	arr := decodeJSONArray(t, buf.Bytes())
	require.Len(t, arr, 1)
	assertSelectedFrameworkNull(t, arr[0])
}

// TestENH146_Overview_SelectedFrameworkIsNullForOrphan verifies that a
// stray result-only TC (no wiring) emits selected_framework:null.
func TestENH146_Overview_SelectedFrameworkIsNullForOrphan(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/test/cases", "tc-eee0016-x.md"),
		"---\ntest_case_id: tc-eee0016\ntitle: orphan\n---\n")

	resultsDir := filepath.Join(root, ".gtms", "results")
	require.NoError(t, os.MkdirAll(resultsDir, 0o755))
	orphan := `task: task-eee0016-orphan
command: execute
target: tc-eee0016
adapter: bats-runner
mode: sync
created: "2026-05-19T10:00:00Z"
status: complete
result: pass
framework: bats
completed: "2026-05-19T10:01:00Z"
`
	require.NoError(t, os.WriteFile(filepath.Join(resultsDir, "task-eee0016-orphan.handoff.yaml"),
		[]byte(orphan), 0o644))

	var buf bytes.Buffer
	require.NoError(t, runStatusOverview(&buf, root, nil, true, "", false))
	arr := decodeJSONArray(t, buf.Bytes())
	require.Len(t, arr, 1)
	assertSelectedFrameworkNull(t, arr[0])
}

// TestENH146_Overview_SelectedFrameworkIsNullForBareTC pins null behavior
// for a TC that has neither wiring nor any manual record (the most common
// "fresh draft" case).
func TestENH146_Overview_SelectedFrameworkIsNullForBareTC(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/test/cases", "tc-eee0017-x.md"),
		"---\ntest_case_id: tc-eee0017\ntitle: bare draft\n---\n")

	var buf bytes.Buffer
	require.NoError(t, runStatusOverview(&buf, root, nil, true, "", false))
	arr := decodeJSONArray(t, buf.Bytes())
	require.Len(t, arr, 1)
	assertSelectedFrameworkNull(t, arr[0])
}

// TestENH146_Detail_SelectedFrameworkIsNullForManualOnly pins detail JSON
// also emits `selected_framework: null` for the manual-only case.
func TestENH146_Detail_SelectedFrameworkIsNullForManualOnly(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/test/cases", "tc-eee0018-x.md"),
		"---\ntest_case_id: tc-eee0018\ntitle: manual only\n---\n")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-eee0018", Framework: "manual", Result: "pass"})

	var buf bytes.Buffer
	require.NoError(t, runStatusDetail(&buf, root, "tc-eee0018", true, "", false))
	top := decodeJSONObject(t, buf.Bytes())
	assertSelectedFrameworkNull(t, top)
}

// TestENH146_Detail_SelectedFrameworkIsStringWhenWired verifies the
// positive case — selected_framework serialises as a JSON string (not
// null) when wiring exists.
func TestENH146_Detail_SelectedFrameworkIsStringWhenWired(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/test/cases", "tc-eee0019-x.md"),
		"---\ntest_case_id: tc-eee0019\ntitle: wired\nrequirement: R\n---\n")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-eee0019", Framework: "bats", Result: "pass"})

	var buf bytes.Buffer
	require.NoError(t, runStatusDetail(&buf, root, "tc-eee0019", true, "", false))
	top := decodeJSONObject(t, buf.Bytes())
	require.Contains(t, top, "selected_framework")
	assert.Equal(t, "bats", top["selected_framework"],
		"wired TC: selected_framework must serialise as the string framework name")
}

// --- Helpers ---

// legacyOverviewCarriers — the carriers explicitly listed by ENH-146 / the
// Phase 3C task as MUST-NOT-LEAK on the overview --json contract. Keys are
// the literal JSON key string the legacy struct used.
var legacyOverviewCarriers = []string{
	"automate_status",
	"execute_status",
	"last_result",
	"last_result_date",
}

// legacyDetailCarriers — same set as overview plus the detail-only top-
// level carriers (stale / manual_coverage / artefact_path / ...). `framework`
// is handled separately because the literal key also lives inside the
// frameworks[] nested array.
var legacyDetailCarriers = []string{
	"automate_status",
	"execute_status",
	"last_result",
	"last_result_date",
	"stale",
	"stale_testcase_hash",
	"manual_coverage",
	"available_frameworks",
	"artefact_path",
	"last_run_path",
	"last_run_at",
	// Legacy ID key retired by the ENH-146 testcase-key rename.
	"test_case_id",
}

// decodeJSONObject decodes a top-level JSON object into a map[string]interface{}.
// Use this to assert ABSENCE of legacy keys on the top-level detail JSON
// without false-positives from values inside the nested frameworks[] array.
func decodeJSONObject(t *testing.T, raw []byte) map[string]interface{} {
	t.Helper()
	var top map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &top), "expected top-level JSON object")
	return top
}

// decodeJSONArray decodes a top-level JSON array of objects.
func decodeJSONArray(t *testing.T, raw []byte) []map[string]interface{} {
	t.Helper()
	var arr []map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &arr), "expected top-level JSON array")
	return arr
}

// assertSelectedFrameworkNull asserts that the JSON `selected_framework`
// field on the supplied decoded object is explicitly null — present in the
// map, and the value is JSON-null (Go-nil after unmarshal). An empty
// string fails this assertion.
func assertSelectedFrameworkNull(t *testing.T, m map[string]interface{}) {
	t.Helper()
	require.Contains(t, m, "selected_framework",
		"selected_framework must be present in the JSON contract")
	assert.Nil(t, m["selected_framework"],
		"selected_framework must serialise as JSON null when no framework is selected (got %v)",
		m["selected_framework"])
}

func assertNoLegacyCarriersInOverviewJSON(t *testing.T, raw string) {
	t.Helper()
	arr := decodeJSONArray(t, []byte(raw))
	for i, m := range arr {
		for _, key := range legacyOverviewCarriers {
			assert.NotContainsf(t, m, key,
				"overview --json entry %d: must not emit legacy carrier %q", i, key)
		}
	}
}

// assertNoLegacyCarriersInDetailJSON inspects the TOP-LEVEL keys of the
// detail JSON object via json.Unmarshal so the absence assertion is
// scoped to the detail surface itself — values inside the nested
// frameworks[] array (which legitimately uses `framework`) are not
// considered. This replaces the previous string-scan helper that could
// not distinguish top-level from nested keys.
func assertNoLegacyCarriersInDetailJSON(t *testing.T, raw string) {
	t.Helper()
	top := decodeJSONObject(t, []byte(raw))
	for _, key := range legacyDetailCarriers {
		assert.NotContainsf(t, top, key,
			"detail --json top level: must not emit legacy carrier %q", key)
	}
	// Top-level `framework` must be absent. The same key is allowed
	// inside frameworks[] elements, which the top-level map check above
	// correctly ignores.
	assert.NotContains(t, top, "framework",
		"detail --json top level: `framework` must surface only inside frameworks[], "+
			"never at the top level (it lived there pre-CON-023)")
}
