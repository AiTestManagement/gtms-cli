package reader

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/result"
)

// BUG-193: triage can classify a completed manual-framework execution.
//
// These are the Go unit-level counterparts of the canonical acceptance specs
// under gtms/test/cases/triage-manual-framework-classification/. They exercise
// the reader mechanism (the proven-manual overlay claim, framework inference,
// the artefact proof, and the shared read-back surfaces) directly. The BATS
// acceptance layer is authored separately by the orchestrator.

func manualRecordPath(root, tc string) string {
	return filepath.Join(root, "gtms", "manual", "records", tc+"--manual.result.yaml")
}

// writeManualResultBody stamps the canonical manual result file for a pair with
// the given overall result. framework: manual and test_case_id identify the pair.
func writeManualResultBody(t *testing.T, root, tc, outcome string) {
	t.Helper()
	dir := filepath.Join(root, "gtms", "manual", "records")
	require.NoError(t, os.MkdirAll(dir, 0755))
	body := "test_case_id: " + tc + "\nframework: manual\nresult: " + outcome + "\n"
	require.NoError(t, os.WriteFile(manualRecordPath(root, tc), []byte(body), 0644))
}

// seedManualExecution writes the manual result file plus a terminal manual
// execute handoff whose artefact is the canonical manual result path -- the
// exact shape `gtms execute --adapter manual-execute` produces (framework:
// manual, no artefact-hash). Returns the handoff task id. completed lets a
// caller order two executions.
func seedManualExecution(t *testing.T, root, tc, outcome, task, completed string) string {
	t.Helper()
	writeManualResultBody(t, root, tc, outcome)
	canonical := filepath.ToSlash(filepath.Join("gtms", "manual", "records", tc+"--manual.result.yaml"))
	rc := &result.ResultContract{
		Task:      task,
		Command:   "execute",
		Target:    tc,
		Adapter:   "manual-execute",
		Mode:      "sync",
		Created:   completed,
		Framework: "manual",
		Completed: completed,
		Artefact:  canonical,
		Status:    "complete",
		Result:    outcome,
	}
	_, err := result.Create(root, rc)
	require.NoError(t, err)
	return task
}

func newManualProject(t *testing.T, tc string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeTriageSpec(t, root, tc)
	return root
}

// tc-d181c0f3 / tc-844cdebf: manual-only classification succeeds and the
// snapshot copies the selected execute handoff's provenance verbatim.
func TestClassifyTriage_BUG193_ManualOnlySucceeds(t *testing.T) {
	root := newManualProject(t, "tc-11111111")
	task := seedManualExecution(t, root, "tc-11111111", "fail", "task-manual-1", "2026-09-10T16:34:00Z")

	rec, err := ClassifyTriage(root, "tc-11111111", "environment", "does not match original", "", "manual", triageTestValues())
	require.NoError(t, err)
	assert.Equal(t, "manual", rec.Framework)
	assert.Equal(t, "fail", rec.Snapshot.Result)
	assert.Equal(t, task, rec.Snapshot.Task)
	assert.Equal(t, "2026-09-10T16:34:00Z", rec.Snapshot.CompletedAt)
	assert.Equal(t, "gtms/manual/records/tc-11111111--manual.result.yaml", rec.Snapshot.Artefact)
	assert.Empty(t, rec.Snapshot.ArtefactHash, "manual-execute handoff carries no artefact hash; snapshot omits it")
	assert.NotEmpty(t, rec.Snapshot.TestcaseHashAtTriage)

	// Exactly one record, under the manual pair.
	assert.FileExists(t, triageRecordPath(root, "tc-11111111", "manual"))
	assert.NoFileExists(t, filepath.Join(root, "gtms", "automation", "wiring", "tc-11111111--manual.wiring.yaml"))
}

// tc-f8197375 item 7: a primed manual file with no terminal execute handoff is
// refused, even with a non-empty user-edited result.
func TestClassifyTriage_BUG193_PrimedOnlyRefused(t *testing.T) {
	root := newManualProject(t, "tc-11111111")
	writeManualResultBody(t, root, "tc-11111111", "fail") // primed + edited, never executed

	_, err := ClassifyTriage(root, "tc-11111111", "environment", "", "", "manual", triageTestValues())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no execution result is available")
	assert.NoFileExists(t, triageRecordPath(root, "tc-11111111", "manual"))
}

// tc-f8197375 item 16: with .gtms/results/ removed after execution, the durable
// execution file alone does not authorise classification.
func TestClassifyTriage_BUG193_ResultsDirRemovedRefused(t *testing.T) {
	root := newManualProject(t, "tc-11111111")
	seedManualExecution(t, root, "tc-11111111", "fail", "task-manual-1", "2026-09-10T16:34:00Z")
	require.NoError(t, os.RemoveAll(filepath.Join(root, ".gtms", "results")))

	_, err := ClassifyTriage(root, "tc-11111111", "environment", "", "", "manual", triageTestValues())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no execution result is available")
	assert.NoFileExists(t, triageRecordPath(root, "tc-11111111", "manual"))
}

// tc-24ccb516: --framework manual-execute is not aliased to the manual
// framework; it is refused and writes no record. The same fixture classifies
// under the real name manual.
func TestClassifyTriage_BUG193_AdapterNameNotFrameworkAlias(t *testing.T) {
	root := newManualProject(t, "tc-11111111")
	seedManualExecution(t, root, "tc-11111111", "fail", "task-manual-1", "2026-09-10T16:34:00Z")

	_, err := ClassifyTriage(root, "tc-11111111", "environment", "", "", "manual-execute", triageTestValues())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "manual-execute")
	assert.NoFileExists(t, triageRecordPath(root, "tc-11111111", "manual-execute"))
	assert.NoFileExists(t, triageRecordPath(root, "tc-11111111", "manual"))

	// The task-field mismatch is non-causal: classification never reads the task
	// file, and the real framework name classifies the same fixture.
	_, err = ClassifyTriage(root, "tc-11111111", "environment", "", "", "manual", triageTestValues())
	require.NoError(t, err)
	assert.FileExists(t, triageRecordPath(root, "tc-11111111", "manual"))
}

// tc-0f8e5fc0: inference resolves manual on a manual-only executed case.
func TestResolveTriageFramework_BUG193_ManualOnlyInferred(t *testing.T) {
	root := newManualProject(t, "tc-11111111")
	seedManualExecution(t, root, "tc-11111111", "fail", "task-manual-1", "2026-09-10T16:34:00Z")

	rec, err := ClassifyTriage(root, "tc-11111111", "environment", "", "", "", triageTestValues())
	require.NoError(t, err)
	assert.Equal(t, "manual", rec.Framework)
	assert.FileExists(t, triageRecordPath(root, "tc-11111111", "manual"))
}

// tc-0f8e5fc0 negative: inference has no candidate on a primed-only case.
func TestResolveTriageFramework_BUG193_PrimedOnlyNoCandidate(t *testing.T) {
	root := newManualProject(t, "tc-11111111")
	writeManualResultBody(t, root, "tc-11111111", "fail")

	_, err := ClassifyTriage(root, "tc-11111111", "environment", "", "", "", triageTestValues())
	require.Error(t, err)
	assert.NoFileExists(t, triageRecordPath(root, "tc-11111111", "manual"))
}

// tc-a1a2bf5d: manual + wired reqnroll is ambiguous without --framework, naming
// both candidates deterministically.
func TestResolveTriageFramework_BUG193_MixedAmbiguity(t *testing.T) {
	root := newManualProject(t, "tc-22222222")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-22222222", Framework: "reqnroll", Result: "pass", ExecutedAt: "2026-09-10T15:14:00Z"})
	seedManualExecution(t, root, "tc-22222222", "fail", "task-manual-2", "2026-09-10T16:34:00Z")

	_, err := ClassifyTriage(root, "tc-22222222", "environment", "", "", "", triageTestValues())
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "Specify --framework")
	assert.Contains(t, msg, "manual")
	assert.Contains(t, msg, "reqnroll")
	assert.NoFileExists(t, triageRecordPath(root, "tc-22222222", "manual"))
	assert.NoFileExists(t, triageRecordPath(root, "tc-22222222", "reqnroll"))
}

// tc-7505c358: explicit --framework manual selects the newer manual failure on a
// reqnroll-wired case; reqnroll still classifies from its own handoff; wiring
// is unchanged.
func TestClassifyTriage_BUG193_ExplicitManualOnWiredCase(t *testing.T) {
	root := newManualProject(t, "tc-22222222")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-22222222", Framework: "reqnroll", Result: "pass", ExecutedAt: "2026-09-10T15:14:00Z"})
	seedManualExecution(t, root, "tc-22222222", "fail", "task-manual-2", "2026-09-10T16:34:00Z")

	recM, err := ClassifyTriage(root, "tc-22222222", "environment", "", "", "manual", triageTestValues())
	require.NoError(t, err)
	assert.Equal(t, "fail", recM.Snapshot.Result)
	assert.Equal(t, "task-manual-2", recM.Snapshot.Task)
	assert.Equal(t, "gtms/manual/records/tc-22222222--manual.result.yaml", recM.Snapshot.Artefact)

	recR, err := ClassifyTriage(root, "tc-22222222", "environment", "", "", "reqnroll", triageTestValues())
	require.NoError(t, err)
	assert.Equal(t, "pass", recR.Snapshot.Result)

	// Wiring set is byte-identical: exactly the reqnroll record, no manual wiring.
	assert.FileExists(t, filepath.Join(root, "gtms", "automation", "wiring", "tc-22222222--reqnroll.wiring.yaml"))
	assert.NoFileExists(t, filepath.Join(root, "gtms", "automation", "wiring", "tc-22222222--manual.wiring.yaml"))
}

// tc-7246e425: a wiring-free non-manual terminal handoff stays an overlay orphan
// -- proven as a counterfactual by adding wiring, after which the identical
// handoff classifies.
func TestClassifyTriage_BUG193_NonManualOrphanStaysOrphan(t *testing.T) {
	root := newManualProject(t, "tc-33333333")
	// Hand-seed a reqnroll execute handoff with NO reqnroll wiring.
	rc := &result.ResultContract{
		Task: "task-aaaa0003", Command: "execute", Target: "tc-33333333",
		Adapter: "reqnroll-runner", Mode: "sync", Created: "2026-09-11T09:00:00Z",
		Framework: "reqnroll", Completed: "2026-09-11T09:00:00Z",
		Artefact: "gtms/test/specs/reqnroll/tc-33333333.feature",
		Status:   "complete", Result: "fail",
	}
	_, err := result.Create(root, rc)
	require.NoError(t, err)

	_, err = ClassifyTriage(root, "tc-33333333", "environment", "", "", "reqnroll", triageTestValues())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no execution result is available")
	assert.NoFileExists(t, triageRecordPath(root, "tc-33333333", "reqnroll"))

	// Counterfactual: add the matching wiring and the SAME handoff classifies.
	seedWiringWithRealArtefactHash(t, root, "tc-33333333", "reqnroll", "gtms/test/specs/reqnroll/tc-33333333.feature", "Feature: x\n")
	// seedWiring... only writes the artefact; write the wiring record itself.
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-33333333", Framework: "reqnroll", Artefact: "gtms/test/specs/reqnroll/tc-33333333.feature", SkipArtefactFile: true})

	rec, err := ClassifyTriage(root, "tc-33333333", "environment", "", "", "reqnroll", triageTestValues())
	require.NoError(t, err)
	assert.Equal(t, "fail", rec.Snapshot.Result)
	assert.Equal(t, "task-aaaa0003", rec.Snapshot.Task)
}

// tc-c78c0008: unprovable artefact variants are each refused; the untampered
// state classifies.
func TestClassifyTriage_BUG193_UnprovableArtefactRefused(t *testing.T) {
	canonicalRel := "gtms/manual/records/tc-11111111--manual.result.yaml"
	handoffArtefact := func(t *testing.T, root, artefact string) {
		// Rewrite the execute handoff's artefact line to the given value.
		rc := &result.ResultContract{
			Task: "task-manual-1", Command: "execute", Target: "tc-11111111",
			Adapter: "manual-execute", Mode: "sync", Created: "2026-09-10T16:34:00Z",
			Framework: "manual", Completed: "2026-09-10T16:34:00Z",
			Artefact: artefact, Status: "complete", Result: "fail",
		}
		require.NoError(t, os.RemoveAll(filepath.Join(root, ".gtms", "results")))
		_, err := result.Create(root, rc)
		require.NoError(t, err)
	}

	refused := func(t *testing.T, setup func(root string)) {
		root := newManualProject(t, "tc-11111111")
		writeManualResultBody(t, root, "tc-11111111", "fail")
		// Baseline valid handoff.
		handoffArtefact(t, root, canonicalRel)
		setup(root)
		_, err := ClassifyTriage(root, "tc-11111111", "environment", "", "", "manual", triageTestValues())
		require.Error(t, err)
		assert.NoFileExists(t, triageRecordPath(root, "tc-11111111", "manual"))
	}

	// Variant A: out-of-project artefact path.
	refused(t, func(root string) { handoffArtefact(t, root, "../outside/tc-11111111--manual.result.yaml") })
	// Variant B: in-project path naming a different test case.
	refused(t, func(root string) { handoffArtefact(t, root, "gtms/manual/records/tc-99999999--manual.result.yaml") })
	// Variant C: artefact is a directory.
	refused(t, func(root string) { handoffArtefact(t, root, "gtms/manual/records") })
	// Variant F: byte-identical copy at a non-canonical in-project path.
	refused(t, func(root string) { handoffArtefact(t, root, "gtms/manual/tc-11111111--manual.result.yaml") })
	// Variant D: canonical file body is malformed YAML.
	refused(t, func(root string) {
		require.NoError(t, os.WriteFile(manualRecordPath(root, "tc-11111111"), []byte(": : [not yaml\n"), 0644))
	})
	// Variant E: canonical file body names a non-manual framework.
	refused(t, func(root string) {
		body := "test_case_id: tc-11111111\nframework: reqnroll\nresult: fail\n"
		require.NoError(t, os.WriteFile(manualRecordPath(root, "tc-11111111"), []byte(body), 0644))
	})
	// Symlinked artefact (Linux only): a link at the canonical path is refused.
	if runtime.GOOS != "windows" {
		root := newManualProject(t, "tc-11111111")
		writeManualResultBody(t, root, "tc-11111111", "fail")
		handoffArtefact(t, root, canonicalRel)
		// Replace the canonical file with a symlink to a copy outside the tree.
		outside := filepath.Join(root, "..", "outside-tc-11111111.yaml")
		require.NoError(t, os.WriteFile(outside, []byte("test_case_id: tc-11111111\nframework: manual\nresult: fail\n"), 0644))
		require.NoError(t, os.Remove(manualRecordPath(root, "tc-11111111")))
		require.NoError(t, os.Symlink(outside, manualRecordPath(root, "tc-11111111")))
		_, err := ClassifyTriage(root, "tc-11111111", "environment", "", "", "manual", triageTestValues())
		require.Error(t, err)
		assert.NoFileExists(t, triageRecordPath(root, "tc-11111111", "manual"))
	}

	// Untampered control: the canonical pair classifies.
	root := newManualProject(t, "tc-11111111")
	seedManualExecution(t, root, "tc-11111111", "fail", "task-manual-1", "2026-09-10T16:34:00Z")
	_, err := ClassifyTriage(root, "tc-11111111", "environment", "", "", "manual", triageTestValues())
	require.NoError(t, err)
	assert.FileExists(t, triageRecordPath(root, "tc-11111111", "manual"))
}

// tc-ac5e07ff: after classifying a manual failure, a later manual pass shows as
// changed since triage through the shared overlay (--show/--list surfaces).
func TestShowListTriage_BUG193_LaterManualPassChanged(t *testing.T) {
	root := newManualProject(t, "tc-11111111")
	seedManualExecution(t, root, "tc-11111111", "fail", "task-manual-1", "2026-09-10T16:34:00Z")
	_, err := ClassifyTriage(root, "tc-11111111", "environment", "", "", "manual", triageTestValues())
	require.NoError(t, err)

	// Later manual pass (newer completed).
	seedManualExecution(t, root, "tc-11111111", "pass", "task-manual-2", "2026-09-10T17:00:00Z")

	view, err := ShowTriage(root, "tc-11111111", "manual")
	require.NoError(t, err)
	require.NotNil(t, view.ResultChanged)
	assert.True(t, *view.ResultChanged, "snapshot fail vs latest pass must read as changed")
	assert.Equal(t, "fail", view.Record.Snapshot.Result)

	views, err := ListTriageForTC(root, "tc-11111111", "", true)
	require.NoError(t, err)
	require.Len(t, views, 1)
	require.NotNil(t, views[0].ResultChanged)
	assert.True(t, *views[0].ResultChanged)
}

// tc-d6234b6f: editing the manual file to pass without re-executing does not
// invalidate the claim; triage classifies the executed fail.
func TestClassifyTriage_BUG193_PostExecutionEditKeepsExecutedResult(t *testing.T) {
	root := newManualProject(t, "tc-11111111")
	seedManualExecution(t, root, "tc-11111111", "fail", "task-manual-1", "2026-09-10T16:34:00Z")
	// Edit the file to pass WITHOUT executing again (handoff still says fail).
	writeManualResultBody(t, root, "tc-11111111", "pass")

	rec, err := ClassifyTriage(root, "tc-11111111", "environment", "", "", "manual", triageTestValues())
	require.NoError(t, err)
	assert.Equal(t, "fail", rec.Snapshot.Result, "claim binds on identity, not on file content")
	assert.Equal(t, "gtms/manual/records/tc-11111111--manual.result.yaml", rec.Snapshot.Artefact)

	view, err := ShowTriage(root, "tc-11111111", "manual")
	require.NoError(t, err)
	require.NotNil(t, view.ResultChanged)
	assert.False(t, *view.ResultChanged, "latest handoff is still the executed fail")
}

// BUG-193 item 13: the task-field mismatch is non-causal. The manual claim reads
// the handoff's framework: manual, never the task file, so classification is
// independent of what the execute task recorded as its framework.
func TestClassifyTriage_BUG193_TaskFrameworkMismatchNonCausal(t *testing.T) {
	root := newManualProject(t, "tc-11111111")
	// The manual claim is admitted purely from the terminal execute handoff; no
	// task file is written here, and classification still succeeds.
	seedManualExecution(t, root, "tc-11111111", "fail", "task-manual-1", "2026-09-10T16:34:00Z")
	_, hit := manualClaimForTC(root, "tc-11111111")
	assert.True(t, hit, "the proven manual claim comes from the handoff, not the task file")

	rec, err := ClassifyTriage(root, "tc-11111111", "environment", "", "", "manual", triageTestValues())
	require.NoError(t, err)
	assert.Equal(t, "manual", rec.Framework)
}

// tc-ac5e07ff item 9 status JSON contract: on a MANUAL-ONLY case the manual
// triage is attached at the test-case level and frameworks[] stays [] (the
// ENH-146 shape the map_phase3d tests pin), never nested under a framework entry.
func TestAttachTriage_BUG193_ManualOnlyCarriesTopLevelTriage(t *testing.T) {
	root := newManualProject(t, "tc-11111111")
	seedManualExecution(t, root, "tc-11111111", "fail", "task-manual-1", "2026-09-10T16:34:00Z")
	_, err := ClassifyTriage(root, "tc-11111111", "environment", "", "", "manual", triageTestValues())
	require.NoError(t, err)

	// Detail (single-TC) view: strict --framework manual, as `gtms status <tc>
	// --framework manual --json` uses.
	detail, err := PipelineDetail(root, "tc-11111111", "manual", true)
	require.NoError(t, err)
	require.NoError(t, AttachTriageToDetail(root, detail, triageTestValues(), "", ""))
	assert.Empty(t, detail.Frameworks, "manual-only frameworks[] stays []")
	require.NotNil(t, detail.Triage, "manual triage attaches at the test-case level")
	assert.Equal(t, "environment", detail.Triage.Value)
	assert.Equal(t, "fail", detail.Triage.Snapshot.Result)

	// Overview (folder) view: the same entry-level attachment.
	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.NoError(t, AttachTriageToEntries(root, entries, triageTestValues(), "", ""))
	var found *PipelineEntry
	for i := range entries {
		if entries[i].TestCaseID == "tc-11111111" {
			found = &entries[i]
		}
	}
	require.NotNil(t, found)
	assert.Empty(t, found.Frameworks)
	require.NotNil(t, found.Triage)
	assert.Equal(t, "environment", found.Triage.Value)
}

// Guard the merge rule directly: a proven manual claim never displaces a value
// the wiring join already produced (add-if-absent).
func TestMergeManualClaims_BUG193_AddIfAbsent(t *testing.T) {
	existing := map[string]overlayHit{overlayKey("tc-1", "manual"): {RawResult: "pass", Task: "wired"}}
	manual := map[string]overlayHit{
		overlayKey("tc-1", "manual"): {RawResult: "fail", Task: "claim"},
		overlayKey("tc-2", "manual"): {RawResult: "fail", Task: "claim2"},
	}
	out := mergeManualClaims(existing, manual)
	assert.Equal(t, "wired", out[overlayKey("tc-1", "manual")].Task, "wiring join wins")
	assert.Equal(t, "claim2", out[overlayKey("tc-2", "manual")].Task, "absent key filled")
}

// BUG-193 worktree review finding (2026-09-13): the entry-level triage carrier is exclusively the
// manual-only carrier. On a WIRED case, classify the manual execution and then
// remove the editable manual artefact: the BUG-127 synthesised manual row
// disappears, but the historical manual triage record remains. It must NOT be
// attached at the test-case level (where the selected wired framework could
// inherit it), while staying reachable through the triage read surface.
func TestAttachTriage_BUG193_WiredCaseNeverCarriesTopLevelTriage(t *testing.T) {
	root := newManualProject(t, "tc-22222222")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-22222222", Framework: "reqnroll", Result: "pass", ExecutedAt: "2026-09-10T15:14:00Z"})
	seedManualExecution(t, root, "tc-22222222", "fail", "task-manual-2", "2026-09-10T16:34:00Z")
	_, err := ClassifyTriage(root, "tc-22222222", "environment", "", "", "manual", triageTestValues())
	require.NoError(t, err)

	// Remove the editable manual artefact after classification.
	require.NoError(t, os.Remove(manualRecordPath(root, "tc-22222222")))

	// Detail view with the picker selecting the wired framework.
	detail, err := PipelineDetail(root, "tc-22222222", "", false)
	require.NoError(t, err)
	require.NoError(t, AttachTriageToDetail(root, detail, triageTestValues(), "", ""))
	assert.True(t, detail.Wired)
	assert.Nil(t, detail.Triage, "a wired case never carries the entry-level manual triage")
	for _, fe := range detail.Frameworks {
		assert.NotEqual(t, "manual", fe.Framework, "the synthesised manual row is gone with its artefact")
		assert.Nil(t, fe.Triage, "the manual classification must not land on framework %q", fe.Framework)
	}

	// Overview view: same isolation.
	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.NoError(t, AttachTriageToEntries(root, entries, triageTestValues(), "", ""))
	for i := range entries {
		if entries[i].TestCaseID != "tc-22222222" {
			continue
		}
		assert.True(t, entries[i].Wired)
		assert.Nil(t, entries[i].Triage)
		for _, fe := range entries[i].Frameworks {
			assert.Nil(t, fe.Triage)
		}
	}

	// The record itself is not lost: the triage read surface still finds it.
	view, err := ShowTriage(root, "tc-22222222", "manual")
	require.NoError(t, err)
	assert.Equal(t, "environment", view.Record.Value)
}

// writeRawManualHandoff writes a manual execute handoff with arbitrary field
// values under the given file name, bypassing result.Create so the REV-170
// CODEX-002 admission gate can be exercised with malformed provenance.
func writeRawManualHandoff(t *testing.T, root, fileName, tc, task, outcome, completed, status string) {
	t.Helper()
	dir := filepath.Join(root, ".gtms", "results")
	require.NoError(t, os.MkdirAll(dir, 0755))
	canonical := "gtms/manual/records/" + tc + "--manual.result.yaml"
	body := "task: " + task + "\ncommand: execute\ntarget: " + tc + "\nadapter: manual-execute\nmode: sync\nframework: manual\nartefact: " + canonical + "\ncreated: \"2026-09-10T16:34:00Z\"\n"
	if completed != "" {
		body += "completed: \"" + completed + "\"\n"
	}
	body += "status: " + status + "\n"
	if outcome != "" {
		body += "result: " + outcome + "\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, fileName), []byte(body), 0644))
}

// REV-170 CODEX-001: an explicit non-manual --framework is a strict selection.
// On a manual-only classified case it must suppress the entry-level manual
// carrier for JSON attachment (and therefore rendering and filtering), while
// no explicit framework or an explicit "manual" still attaches it.
func TestAttachTriage_REV170_StrictNonManualFrameworkSuppressesManualCarrier(t *testing.T) {
	root := newManualProject(t, "tc-11111111")
	seedManualExecution(t, root, "tc-11111111", "fail", "task-manual-1", "2026-09-10T16:34:00Z")
	_, err := ClassifyTriage(root, "tc-11111111", "environment", "", "", "manual", triageTestValues())
	require.NoError(t, err)

	for _, tc := range []struct {
		requested string
		attached  bool
	}{
		{"bats", false},
		{"reqnroll", false},
		{"manual", true},
		{"", true},
	} {
		detail, err := PipelineDetail(root, "tc-11111111", tc.requested, tc.requested != "")
		require.NoError(t, err)
		require.NoError(t, AttachTriageToDetail(root, detail, triageTestValues(), "", tc.requested))
		assert.Equal(t, tc.attached, detail.Triage != nil, "detail, requested=%q", tc.requested)

		entries, err := PipelineStatus(root, nil, tc.requested, tc.requested != "")
		require.NoError(t, err)
		require.NoError(t, AttachTriageToEntries(root, entries, triageTestValues(), "", tc.requested))
		for i := range entries {
			if entries[i].TestCaseID == "tc-11111111" {
				assert.Equal(t, tc.attached, entries[i].Triage != nil, "entries, requested=%q", tc.requested)
			}
		}
	}
}

// REV-170 CODEX-002: the manual-claim admission gate. Each malformed handoff
// must be refused BEFORE a triage record is written, so no unreadable record
// can be produced; the well-formed control proves the fixture is otherwise
// classifiable.
func TestManualClaim_REV170_ProvenanceGateRefusesMalformedHandoffs(t *testing.T) {
	cases := []struct {
		name     string
		fileName string
		task     string
		outcome  string
		complete string
		status   string
	}{
		{"missing task", "task-aaaa0001.handoff.yaml", "", "fail", "2026-09-10T16:34:00Z", "complete"},
		// result.Read strips control characters from the task at the read
		// boundary, so a task that is only well-formed AFTER stripping and then
		// disagrees with the filename is refused by the filename check.
		{"control chars in task leave it disagreeing with filename", "task-aaaa0001.handoff.yaml", "task-bbbb\x1b[31m0002", "fail", "2026-09-10T16:34:00Z", "complete"},
		{"task disagrees with filename", "task-aaaa0001.handoff.yaml", "task-bbbb0002", "fail", "2026-09-10T16:34:00Z", "complete"},
		{"error handoff without a result", "task-aaaa0001.handoff.yaml", "task-aaaa0001", "", "2026-09-10T16:34:00Z", "error"},
		{"result outside the vocabulary", "task-aaaa0001.handoff.yaml", "task-aaaa0001", "passed", "2026-09-10T16:34:00Z", "complete"},
		{"completed not RFC3339", "task-aaaa0001.handoff.yaml", "task-aaaa0001", "fail", "yesterday\x07", "complete"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := newManualProject(t, "tc-11111111")
			writeManualResultBody(t, root, "tc-11111111", "fail")
			writeRawManualHandoff(t, root, c.fileName, "tc-11111111", c.task, c.outcome, c.complete, c.status)
			_, err := ClassifyTriage(root, "tc-11111111", "environment", "", "", "manual", triageTestValues())
			require.Error(t, err)
			assert.Contains(t, err.Error(), "no execution result is available")
			assert.NoFileExists(t, filepath.Join(root, "gtms", "triage", "tc-11111111--manual.triage.yaml"))
		})
	}
	t.Run("well-formed control", func(t *testing.T) {
		root := newManualProject(t, "tc-11111111")
		writeManualResultBody(t, root, "tc-11111111", "fail")
		writeRawManualHandoff(t, root, "task-aaaa0001.handoff.yaml", "tc-11111111", "task-aaaa0001", "fail", "2026-09-10T16:34:00Z", "complete")
		rec, err := ClassifyTriage(root, "tc-11111111", "environment", "", "", "manual", triageTestValues())
		require.NoError(t, err)
		assert.Equal(t, "task-aaaa0001", rec.Snapshot.Task)
	})
	t.Run("control chars never reach the stored task", func(t *testing.T) {
		// The read boundary strips them; the stored provenance must be the
		// clean identity that matches the filename, never the raw bytes.
		root := newManualProject(t, "tc-11111111")
		writeManualResultBody(t, root, "tc-11111111", "fail")
		writeRawManualHandoff(t, root, "task-aaaa0001.handoff.yaml", "tc-11111111", "task-aaaa0001\x07", "fail", "2026-09-10T16:34:00Z", "complete")
		rec, err := ClassifyTriage(root, "tc-11111111", "environment", "", "", "manual", triageTestValues())
		require.NoError(t, err)
		assert.Equal(t, "task-aaaa0001", rec.Snapshot.Task)
		for _, r := range rec.Snapshot.Task {
			assert.False(t, r < 0x20 || r == 0x7f, "control character reached the stored task")
		}
	})
}

// REV-170 CLAUDE-003: a handoff target carrying dot segments or separators is
// refused before any path is built, even when the artefact string is spelled
// to match the path the target would have produced.
func TestManualArtefactProven_REV170_TraversalTargetRefused(t *testing.T) {
	root := t.TempDir()
	for _, target := range []string{"../../../etc/evil", "..", "tc-1111/../tc-2222", "tc-1111" + string(rune(92)) + "x", "tc-1111\x00"} {
		rc := &result.ResultContract{
			Target:   target,
			Artefact: filepath.ToSlash(filepath.Join("gtms", "manual", "records", target+"--manual.result.yaml")),
		}
		assert.False(t, manualArtefactProven(root, rc), "target %q must be refused", target)
	}
}

// REV-170 CODEX-003: an ancestor symlink under the canonical path (here
// manual/records itself) pointing outside the project passes a leaf Lstat but
// must fail the containment resolution. Skipped where symlinks cannot be
// created (Windows without developer mode); the leaf-symlink case is covered
// on Linux by the acceptance suite.
func TestManualArtefactProven_REV170_AncestorSymlinkRefused(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "project")
	outside := filepath.Join(base, "outside")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms", "manual"), 0755))
	require.NoError(t, os.MkdirAll(outside, 0755))
	body := "test_case_id: tc-11111111\nframework: manual\nresult: fail\n"
	require.NoError(t, os.WriteFile(filepath.Join(outside, "tc-11111111--manual.result.yaml"), []byte(body), 0644))
	if err := os.Symlink(outside, filepath.Join(root, "gtms", "manual", "records")); err != nil {
		t.Skipf("symlink creation not permitted on this host: %v", err)
	}
	rc := &result.ResultContract{
		Target:   "tc-11111111",
		Artefact: "gtms/manual/records/tc-11111111--manual.result.yaml",
	}
	assert.False(t, manualArtefactProven(root, rc), "an ancestor symlink to an out-of-project directory must not prove the artefact")

	// Positive control: the same layout with a real directory proves.
	require.NoError(t, os.Remove(filepath.Join(root, "gtms", "manual", "records")))
	writeManualResultBody(t, root, "tc-11111111", "fail")
	assert.True(t, manualArtefactProven(root, rc))
}
