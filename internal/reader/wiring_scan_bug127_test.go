package reader

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// BUG-127: a recorded manual/prime result must be reachable through status
// (and map) even when the test case is ALSO wired to a framework. The fix
// models the manual result as a first-class FrameworkEntry on a wired case,
// decouples ManualReady from the un-wired gate, and resolves an explicit
// --framework manual selection in buildPipelineEntry (pickWiring stays
// wiring-only). These tests pin all three, plus the dup/precedence guards for
// the manual-wiring + manual-result both-exist case and the drift side-effect.

// TestBUG127_ManualResultSurfacesWhenAlsoWired: on a bats-wired TC that also
// carries a recorded manual result, --framework manual surfaces the manual
// result (pass/fail/skipped), --json exposes it (ManualReady + a manual
// frameworks[] entry), and the default view keeps the wired framework selected.
func TestBUG127_ManualResultSurfacesWhenAlsoWired(t *testing.T) {
	for _, r := range []string{"pass", "fail", "skipped"} {
		r := r
		t.Run(r, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, root, filepath.Join("gtms/test/cases", "tc-bug127-wired.md"), `---
test_case_id: tc-bug127
title: Wired TC with a manual result
---
`)
			// bats wiring + terminal pass, AND a manual result file on the SAME TC.
			seedLegacyRecord(t, root, legacyRecord{TC: "tc-bug127", Framework: "bats", Result: "pass"})
			seedLegacyRecord(t, root, legacyRecord{TC: "tc-bug127", Framework: "manual", Result: r})

			// Explicit --framework manual surfaces the recorded manual result.
			strict, err := PipelineStatus(root, nil, "manual", true)
			require.NoError(t, err)
			require.Len(t, strict, 1)
			e := strict[0]
			assert.Equal(t, "manual", e.SelectedFramework)
			assert.True(t, e.ManualReady)
			assert.Equal(t, r, e.LastResult)
			if r == "skipped" {
				assert.Equal(t, "skipped", e.ExecuteStatus)
			} else {
				assert.Equal(t, "complete", e.ExecuteStatus)
			}
			manualEntries := frameworksWithName(e.Frameworks, "manual")
			require.Len(t, manualEntries, 1, "exactly one manual frameworks[] entry")
			assert.Equal(t, r, manualEntries[0].LastResultHere)
			if r == "skipped" {
				assert.Equal(t, "skipped", manualEntries[0].LastStatusHere)
			} else {
				assert.Equal(t, "complete", manualEntries[0].LastStatusHere)
			}

			// Default view (Option A): manual is visible in frameworks[]/ManualReady
			// but the wired framework stays selected.
			def, err := PipelineStatus(root, nil, "", false)
			require.NoError(t, err)
			require.Len(t, def, 1)
			assert.True(t, def[0].ManualReady)
			assert.Len(t, frameworksWithName(def[0].Frameworks, "manual"), 1)
			assert.Equal(t, "bats", def[0].SelectedFramework)

			// Explicit --framework bats is unchanged.
			batsView, err := PipelineStatus(root, nil, "bats", true)
			require.NoError(t, err)
			require.Len(t, batsView, 1)
			assert.Equal(t, "bats", batsView[0].SelectedFramework)
		})
	}
}

// TestBUG127_NoDuplicateManualEntry_WhenManualWiringAndResultBothExist: a TC
// with BOTH a manual WIRING record (CON-023 Edge Case 1) and a manual RESULT
// file must yield exactly one Framework=="manual" entry (dup guard), and the
// picker's wiring-derived selection must win (precedence guard).
func TestBUG127_NoDuplicateManualEntry_WhenManualWiringAndResultBothExist(t *testing.T) {
	root := t.TempDir()
	specRel := "gtms/test/cases/tc-bug127dup-mixed.md"
	writeFile(t, root, filepath.FromSlash(specRel), `---
test_case_id: tc-bug127dup
title: Manual wiring AND manual result file
---
`)
	// bats wiring (TierCurrent).
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-bug127dup", Framework: "bats", Result: "pass"})

	// Manual WIRING record -- committed manual runbook (CON-023 Edge Case 1).
	manualArtefact := "test/manual/tc-bug127dup.md"
	manualAbs := filepath.Join(root, manualArtefact)
	require.NoError(t, os.MkdirAll(filepath.Dir(manualAbs), 0755))
	require.NoError(t, os.WriteFile(manualAbs, []byte("# manual runbook\n"), 0644))
	specHash, err := pipeline.HashFile(filepath.Join(root, filepath.FromSlash(specRel)))
	require.NoError(t, err)
	manualHash, err := pipeline.HashFile(manualAbs)
	require.NoError(t, err)
	_, err = wiring.Write(root, &wiring.WiringRecord{
		TestCase: "tc-bug127dup", TestCaseHash: specHash,
		Framework: "manual", Adapter: "manual-execute",
		Artefact: manualArtefact, ArtefactHash: manualHash,
	})
	require.NoError(t, err)

	// Manual RESULT file on the same TC.
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-bug127dup", Framework: "manual", Result: "pass"})

	strict, err := PipelineStatus(root, nil, "manual", true)
	require.NoError(t, err)
	require.Len(t, strict, 1)
	e := strict[0]

	// Dup guard: exactly one manual entry, and it is the wiring-derived one
	// (Wired==true), not the synthesized result-file entry (Wired==false).
	manualEntries := frameworksWithName(e.Frameworks, "manual")
	require.Len(t, manualEntries, 1, "no duplicate manual frameworks[] entry")
	assert.True(t, manualEntries[0].Wired, "the surviving manual entry is the wiring-derived one")

	// Precedence guard: the picker's manual-wiring selection wins.
	assert.Equal(t, "manual", e.SelectedFramework)
}

// TestBUG127_ManualDriftSurfacesUnderFrameworkManual: pins the deliberate
// side-effect that setting SelectedFramework=="manual" (before the drift block)
// makes manual drift surface under --framework manual on a wired case.
func TestBUG127_ManualDriftSurfacesUnderFrameworkManual(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-bug127drift.md"), `---
test_case_id: tc-bug127drift
title: Wired TC with a drifted manual result
---
`)
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-bug127drift", Framework: "bats", Result: "pass"})
	writeManualResultFile(t, root, "tc-bug127drift", true) // drifted manual record

	strict, err := PipelineStatus(root, nil, "manual", true)
	require.NoError(t, err)
	require.Len(t, strict, 1)
	assert.Equal(t, "manual", strict[0].SelectedFramework)
	assert.True(t, strict[0].DriftDetected,
		"manual drift must surface under --framework manual on a wired case")
}

// TestBUG127_MapManualResultRespectsOptionA: gtms map shares buildPipelineEntry
// but applies a worst-of-frameworks override (map.go) AFTER it. The synthesized
// result-file manual entry (Wired==false) must NOT bleed into the compact default
// map row -- per Option A it surfaces only under explicit --framework manual. This
// pins AC6 for map (the map-specific override the PRP flagged for verification).
func TestBUG127_MapManualResultRespectsOptionA(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-bug127map.md"), `---
test_case_id: tc-bug127map
title: Wired bats-pass TC with a recorded manual FAIL
requirement: REQ-BUG127
---
`)
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-bug127map", Framework: "bats", Result: "pass"})
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-bug127map", Framework: "manual", Result: "fail"})

	// Default map view (Option A): the manual fail must NOT worsen the compact row.
	def, err := Map(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, def.Groups, 1)
	require.Len(t, def.Groups[0].TestCases, 1)
	e := def.Groups[0].TestCases[0]
	assert.Equal(t, "bats", e.SelectedFramework)
	assert.Equal(t, "pass", e.LastResult,
		"manual fail must not bleed into the compact default map row (Option A)")
	assert.True(t, e.ManualReady)
	require.Len(t, frameworksWithName(e.Frameworks, "manual"), 1)

	// Explicit --framework manual: the recorded manual result surfaces.
	strict, err := Map(root, nil, "manual", true)
	require.NoError(t, err)
	s := strict.Groups[0].TestCases[0]
	assert.Equal(t, "manual", s.SelectedFramework)
	assert.Equal(t, "fail", s.LastResult,
		"explicit --framework manual surfaces the recorded manual result")
}
