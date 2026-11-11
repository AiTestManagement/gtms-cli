package reader

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// BUG-206: a manual-ONLY (unwired) test case with a manual result record must
// surface one first-class `manual` frameworks[] row across status (single-TC,
// folder, recursive) and map JSON, exactly as the binding JSON state table
// requires -- replacing ENH-146's "empty frameworks[] whenever wired:false"
// rule. These tests pin every row of that table for the manual-only path.
// The wired-plus-manual and manual-wiring-dedup paths stay covered by the
// BUG-127 tests (extended there).

func bug206WriteSpec(t *testing.T, root, tcID string) {
	t.Helper()
	writeFile(t, root, filepath.Join("gtms/test/cases", tcID+"-manual.md"),
		"---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: "+tcID+"\ntitle: manual only\nrequirement: REQ-BUG206\n---\n")
}

// manualRow returns the single frameworks[] entry named "manual", failing if
// there is not exactly one.
func bug206ManualRow(t *testing.T, fws []FrameworkEntry) FrameworkEntry {
	t.Helper()
	rows := frameworksWithName(fws, "manual")
	require.Len(t, rows, 1, "expected exactly one manual row")
	return rows[0]
}

// TestBUG206_ManualOnly_PreparedRow: a primed-but-not-recorded manual record
// yields one result-free manual row (state table "Prepared" row).
func TestBUG206_ManualOnly_PreparedRow(t *testing.T) {
	root := t.TempDir()
	bug206WriteSpec(t, root, "tc-aaaa0001")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-aaaa0001", Framework: "manual"}) // no result -> prepared

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	e := entries[0]

	assert.False(t, e.Wired)
	assert.True(t, e.ManualReady)
	assert.Empty(t, e.SelectedFramework, "default view -> selected_framework null")

	row := bug206ManualRow(t, e.Frameworks)
	assert.False(t, row.Wired)
	assert.Equal(t, filepath.ToSlash("gtms/manual/records/tc-aaaa0001--manual.result.yaml"), row.Artefact)
	assert.True(t, row.ArtefactPresent)
	assert.Empty(t, row.WiringDrift)
	assert.Empty(t, row.WiringBootstrap)
	assert.Empty(t, row.LastResultHere, "prepared row has no result")
	assert.Empty(t, row.LastStatusHere)
	assert.Empty(t, row.LastExecutedHere)
	assert.Empty(t, row.WiredAdapter)
	assert.Empty(t, row.LastRunAdapter)

	// JSON contract: frameworks[] is a one-element array and selected_framework
	// serialises as null.
	raw, err := json.Marshal(e)
	require.NoError(t, err)
	var obj map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &obj))
	assert.JSONEq(t, "null", string(obj["selected_framework"]))
	var fws []map[string]any
	require.NoError(t, json.Unmarshal(obj["frameworks"], &fws))
	require.Len(t, fws, 1)
	assert.Equal(t, "manual", fws[0]["framework"])
	assert.Equal(t, false, fws[0]["wired"])
	_, hasResult := fws[0]["last_result_here"]
	assert.False(t, hasResult, "prepared row omits last_result_here")
	_, hasExec := fws[0]["last_executed_here"]
	assert.False(t, hasExec, "manual row omits last_executed_here")
}

// TestBUG206_ManualOnly_RecordedOutcomes: pass/fail/skip normalisation on the
// manual row across detail, folder, recursive and map JSON (state table
// "Recorded" rows). skip -> skipped/skipped; pass/fail -> complete.
func TestBUG206_ManualOnly_RecordedOutcomes(t *testing.T) {
	cases := []struct {
		id         string
		seed       string // value written to the manual record
		wantResult string
		wantStatus string
	}{
		{"tc-aaaa0002", "pass", "pass", "complete"},
		{"tc-aaaa0003", "fail", "fail", "complete"},
		{"tc-aaaa0004", "skip", "skipped", "skipped"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.seed, func(t *testing.T) {
			root := t.TempDir()
			bug206WriteSpec(t, root, c.id)
			seedLegacyRecord(t, root, legacyRecord{TC: c.id, Framework: "manual", Result: c.seed})

			// Detail (single-TC).
			detail, err := PipelineDetail(root, c.id, "", false)
			require.NoError(t, err)
			assert.False(t, detail.Wired)
			assert.True(t, detail.ManualReady)
			assert.Empty(t, detail.SelectedFramework)
			drow := bug206ManualRow(t, detail.Frameworks)
			assert.Equal(t, c.wantResult, drow.LastResultHere)
			assert.Equal(t, c.wantStatus, drow.LastStatusHere)
			assert.Empty(t, drow.LastExecutedHere)

			// Folder.
			folder, err := PipelineStatus(root, nil, "", false)
			require.NoError(t, err)
			require.Len(t, folder, 1)
			frow := bug206ManualRow(t, folder[0].Frameworks)
			assert.Equal(t, c.wantResult, frow.LastResultHere)
			assert.Equal(t, c.wantStatus, frow.LastStatusHere)

			// Map: row present, compact carriers reflect the manual outcome
			// (manual-only has no wired worst-of).
			m, err := Map(root, nil, "", false)
			require.NoError(t, err)
			require.Len(t, m.Groups, 1)
			require.Len(t, m.Groups[0].TestCases, 1)
			me := m.Groups[0].TestCases[0]
			mrow := bug206ManualRow(t, me.Frameworks)
			assert.Equal(t, c.wantResult, mrow.LastResultHere)
			assert.Equal(t, c.wantStatus, mrow.LastStatusHere)
			assert.Equal(t, "recorded", me.ManualCoverage)
			assert.Equal(t, c.wantResult, me.LastResult)
			assert.Empty(t, me.SelectedFramework)
		})
	}
}

// TestBUG206_ManualOnly_FrameworkSelection: default -> null; --framework manual
// -> "manual"; --framework bats -> null with the manual row still present and
// never promoted (state table selection rows).
func TestBUG206_ManualOnly_FrameworkSelection(t *testing.T) {
	root := t.TempDir()
	bug206WriteSpec(t, root, "tc-aaaa0005")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-aaaa0005", Framework: "manual", Result: "pass"})

	// Default: null, row present with its pass outcome.
	def, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, def, 1)
	assert.Empty(t, def[0].SelectedFramework)
	assert.Equal(t, "pass", bug206ManualRow(t, def[0].Frameworks).LastResultHere)

	// --framework manual: selected_framework "manual", row present.
	man, err := PipelineStatus(root, nil, "manual", true)
	require.NoError(t, err)
	require.Len(t, man, 1)
	assert.Equal(t, "manual", man[0].SelectedFramework)
	assert.Equal(t, "pass", bug206ManualRow(t, man[0].Frameworks).LastResultHere)

	// --framework bats: null, row still present, never promoted to bats.
	bats, err := PipelineStatus(root, nil, "bats", true)
	require.NoError(t, err)
	require.Len(t, bats, 1)
	assert.Empty(t, bats[0].SelectedFramework, "non-manual strict view never selects the manual result")
	assert.Equal(t, "pass", bug206ManualRow(t, bats[0].Frameworks).LastResultHere)

	// Map honours the same --framework manual selection.
	mm, err := Map(root, nil, "manual", true)
	require.NoError(t, err)
	require.Len(t, mm.Groups, 1)
	require.Len(t, mm.Groups[0].TestCases, 1)
	assert.Equal(t, "manual", mm.Groups[0].TestCases[0].SelectedFramework)
}

// TestBUG206_NoManualRecord_NoRow: the negative control -- an unwired TC with no
// manual record keeps an empty frameworks[] (first row of the state table).
func TestBUG206_NoManualRecord_NoRow(t *testing.T) {
	root := t.TempDir()
	bug206WriteSpec(t, root, "tc-aaaa0006")

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.False(t, entries[0].Wired)
	assert.False(t, entries[0].ManualReady)
	assert.Empty(t, entries[0].SelectedFramework)
	assert.Len(t, entries[0].Frameworks, 0, "no manual record -> no synthesized row")
}

// TestBUG206_ManualWiringAndResult_OneWiringDerivedRow: when a manual WIRING
// record and a manual result file coexist (no other framework), the BUG-127
// duplicate guard keeps exactly one `manual` row, sourced from the wiring
// record (wired:true), with no second synthesized row (state table last row;
// tc-56a063f1).
func TestBUG206_ManualWiringAndResult_OneWiringDerivedRow(t *testing.T) {
	root := t.TempDir()
	specRel := "gtms/test/cases/tc-aaaa0007-manual.md"
	writeFile(t, root, filepath.FromSlash(specRel),
		"---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-aaaa0007\ntitle: manual wiring and result\nrequirement: REQ-BUG206\n---\n")

	// Manual WIRING record (CON-023 Edge Case 1), pointing at the manual result.
	artefact := "gtms/manual/records/tc-aaaa0007--manual.result.yaml"
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-aaaa0007", Framework: "manual", Result: "pass"}) // writes the manual result file
	specHash, err := pipeline.HashFile(filepath.Join(root, filepath.FromSlash(specRel)))
	require.NoError(t, err)
	artefactHash, err := pipeline.HashFile(filepath.Join(root, filepath.FromSlash(artefact)))
	require.NoError(t, err)
	_, err = wiring.Write(root, &wiring.WiringRecord{
		TestCase: "tc-aaaa0007", TestCaseHash: specHash,
		Framework: "manual", Adapter: "manual-execute",
		Artefact: artefact, ArtefactHash: artefactHash,
	})
	require.NoError(t, err)

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	e := entries[0]
	assert.True(t, e.Wired, "a manual wiring record makes the TC wired, not manual-only")
	row := bug206ManualRow(t, e.Frameworks) // asserts exactly one manual row
	assert.True(t, row.Wired, "the surviving manual row is wiring-derived")
	assert.Equal(t, "manual-execute", row.WiredAdapter)
	assert.Equal(t, filepath.ToSlash(artefact), row.Artefact)
}
