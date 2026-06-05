package reader

// CON-023 / ENH-146 — result-overlay join precedence ladder.
//
// The reader's overlay scan (scanTerminalResults) is required to use the
// ENH-146 §"Pin the join precedence" ladder when joining terminal result
// files to currently existing wiring records:
//
//	1. Explicit framework — verified against currently existing wiring;
//	   excluded as orphan if no matching wiring (Edge Case 5).
//	2. Unique target+artefact-hash match.
//	3. Unique target+artefact path match.
//	4. Unambiguous adapter mapping for the TC.
//	5. Otherwise excluded — the reader never guesses.
//
// These tests cover each rung in isolation plus the orphan path. They
// also pin the "manual-only TC remains Manual-ready after rung-1
// verification lands" preservation (the explicit-framework rung must not
// accidentally surface manual results through the wiring overlay; manual
// state stays sourced from gtms/manual/records/).

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// joinLadderCase pins the ladder inputs and the expected outcome.
type joinLadderCase struct {
	name      string
	rc        *result.ResultContract
	tcWiring  []*wiring.WiringRecord
	wantFW    string
	wantEmpty bool // true when the join must exclude the result entirely
}

// TestScanTerminalResults_JoinLadderRungs exercises each rung in
// isolation and the orphan/excluded paths via the helper directly. The
// helper is the canonical implementation of the ladder; testing it
// without going through the filesystem keeps the cases focused.
func TestScanTerminalResults_JoinLadderRungs(t *testing.T) {
	bats := &wiring.WiringRecord{
		TestCase: "tc-aaa11111", TestCaseHash: "spec-h", Framework: "bats",
		Adapter: "bats-runner", Artefact: "test/acceptance/tc-aaa11111.bats", ArtefactHash: "bats-art-h",
	}
	playwright := &wiring.WiringRecord{
		TestCase: "tc-aaa11111", TestCaseHash: "spec-h", Framework: "playwright",
		Adapter: "playwright-runner", Artefact: "test/playwright/tc-aaa11111.spec.ts", ArtefactHash: "pw-art-h",
	}
	bothFrameworks := []*wiring.WiringRecord{bats, playwright}

	cases := []joinLadderCase{
		{
			name: "Rung1_Join_ExplicitFrameworkResolves",
			rc: &result.ResultContract{
				Target: "tc-aaa11111", Framework: "bats", Status: "complete", Result: "pass",
			},
			tcWiring: bothFrameworks,
			wantFW:   "bats",
		},
		{
			name: "Rung1_Join_ExplicitFrameworkOrphanExcluded",
			rc: &result.ResultContract{
				Target: "tc-aaa11111", Framework: "playwright", Status: "complete", Result: "pass",
			},
			tcWiring:  []*wiring.WiringRecord{bats},
			wantEmpty: true,
		},
		{
			name: "Rung1_Join_ExplicitFrameworkNoFallthroughOnMiss",
			rc: &result.ResultContract{
				Target: "tc-aaa11111", Framework: "playwright",
				ArtefactHash: "bats-art-h", // would match bats via rung 2 — must NOT be used
				Status:       "complete", Result: "pass",
			},
			tcWiring:  []*wiring.WiringRecord{bats},
			wantEmpty: true,
		},
		{
			name: "Rung2_Join_ByArtefactHashUnique",
			rc: &result.ResultContract{
				Target: "tc-aaa11111", ArtefactHash: "bats-art-h",
				Status: "complete", Result: "pass",
			},
			tcWiring: bothFrameworks,
			wantFW:   "bats",
		},
		{
			name: "Rung2_Join_ByArtefactHashAmbiguousFallsThrough",
			rc: &result.ResultContract{
				Target: "tc-aaa11111", ArtefactHash: "shared-h",
				Artefact: "test/acceptance/tc-aaa11111.bats", // disambiguates via rung 3
				Status:   "complete", Result: "pass",
			},
			tcWiring: []*wiring.WiringRecord{
				{TestCase: "tc-aaa11111", TestCaseHash: "spec-h", Framework: "bats",
					Adapter: "bats-runner", Artefact: "test/acceptance/tc-aaa11111.bats", ArtefactHash: "shared-h"},
				{TestCase: "tc-aaa11111", TestCaseHash: "spec-h", Framework: "playwright",
					Adapter: "playwright-runner", Artefact: "test/playwright/tc-aaa11111.spec.ts", ArtefactHash: "shared-h"},
			},
			wantFW: "bats",
		},
		{
			name: "Rung3_Join_ByArtefactPathUnique",
			rc: &result.ResultContract{
				Target: "tc-aaa11111", Artefact: "test/playwright/tc-aaa11111.spec.ts",
				Status: "complete", Result: "pass",
			},
			tcWiring: bothFrameworks,
			wantFW:   "playwright",
		},
		{
			name: "Rung4_Join_ByAdapterUnique",
			rc: &result.ResultContract{
				Target: "tc-aaa11111", Adapter: "playwright-runner",
				Status: "complete", Result: "pass",
			},
			tcWiring: bothFrameworks,
			wantFW:   "playwright",
		},
		{
			name: "Rung5_Join_ExcludeWhenAdapterAmbiguous",
			rc: &result.ResultContract{
				Target: "tc-aaa11111", Adapter: "shared-runner",
				Status: "complete", Result: "pass",
			},
			tcWiring: []*wiring.WiringRecord{
				{TestCase: "tc-aaa11111", TestCaseHash: "s1", Framework: "bats",
					Adapter: "shared-runner", Artefact: "a1", ArtefactHash: "h1"},
				{TestCase: "tc-aaa11111", TestCaseHash: "s1", Framework: "playwright",
					Adapter: "shared-runner", Artefact: "a2", ArtefactHash: "h2"},
			},
			wantEmpty: true,
		},
		{
			name: "Rung5_Join_ExcludeWhenNoSignal",
			rc: &result.ResultContract{
				Target: "tc-aaa11111", Status: "complete", Result: "pass",
			},
			tcWiring:  bothFrameworks,
			wantEmpty: true,
		},
		{
			name: "Join_NoWiringForTCExcludesAllResults",
			rc: &result.ResultContract{
				Target: "tc-zzz99999", Framework: "bats", Status: "complete", Result: "pass",
			},
			tcWiring:  nil,
			wantEmpty: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := resolveOverlayFramework(tc.rc, tc.tcWiring)
			if tc.wantEmpty {
				assert.Equal(t, "", got, "expected ladder to exclude result")
				return
			}
			assert.Equal(t, tc.wantFW, got)
		})
	}
}

// TestScanTerminalResults_Join_ExplicitFrameworkVerifiedAgainstWiring
// integrates the ladder through scanTerminalResults itself. A result
// with explicit framework="bats" surfaces under the matching wiring; a
// sibling result with explicit framework="playwright" but no playwright
// wiring is excluded as orphan (Edge Case 5).
func TestScanTerminalResults_Join_ExplicitFrameworkVerifiedAgainstWiring(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join("gtms/cases", "tc-aaa22222-orphan.md"), `---
test_case_id: tc-aaa22222
title: Explicit framework join TC
---
`)
	// Only bats wiring exists for the TC.
	seedLegacyRecord(t, root, legacyRecord{
		TC: "tc-aaa22222", Framework: "bats", Result: "pass",
		ExecutedAt: "2026-05-20T10:00:00Z",
	})

	// Hand-author a second result file with framework: playwright. No
	// playwright wiring exists → rung 1 must exclude it as orphan.
	rcOrphan := &result.ResultContract{
		Task: "task-orphan-pw", Command: "execute", Target: "tc-aaa22222",
		Adapter: "playwright-runner", Mode: "sync", Created: "2026-05-20T09:59:00Z",
		Status: "complete", Result: "fail", Framework: "playwright",
		Completed: "2026-05-20T11:00:00Z",
	}
	_, err := result.Create(root, rcOrphan)
	require.NoError(t, err)

	wiringByTC, err := wiringScan(root)
	require.NoError(t, err)
	overlay := scanTerminalResults(root, wiringByTC)

	hitBats, okBats := overlay[overlayKey("tc-aaa22222", "bats")]
	require.True(t, okBats, "bats result must overlay via explicit-framework rung")
	assert.Equal(t, "pass", hitBats.Result)

	_, okPW := overlay[overlayKey("tc-aaa22222", "playwright")]
	assert.False(t, okPW, "playwright result must be excluded — no playwright wiring exists for this TC")
}

// TestScanTerminalResults_Join_AdapterMappingAmbiguousExcluded pins
// rung 4's "exactly one match" rule end-to-end: when two wiring
// records on the same TC share an adapter, a frameworkless/hashless/
// pathless result joins via rung 4 only if exactly one matches.
func TestScanTerminalResults_Join_AdapterMappingAmbiguousExcluded(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join("gtms/cases", "tc-aaa33333-ambig.md"), `---
test_case_id: tc-aaa33333
title: Ambiguous adapter mapping
---
`)
	// Two wiring records on the same TC sharing the same adapter name.
	seedLegacyRecord(t, root, legacyRecord{
		TC: "tc-aaa33333", Framework: "bats", Adapter: "shared-runner",
		Artefact: "test/acceptance/tc-aaa33333.bats",
	})
	seedLegacyRecord(t, root, legacyRecord{
		TC: "tc-aaa33333", Framework: "playwright", Adapter: "shared-runner",
		Artefact: "test/playwright/tc-aaa33333.spec.ts",
	})

	rc := &result.ResultContract{
		Task: "task-rung4", Command: "execute", Target: "tc-aaa33333",
		Adapter: "shared-runner", Mode: "sync", Created: "2026-05-20T09:59:00Z",
		Status: "complete", Result: "pass", Completed: "2026-05-20T11:00:00Z",
	}
	_, err := result.Create(root, rc)
	require.NoError(t, err)

	wiringByTC, err := wiringScan(root)
	require.NoError(t, err)
	overlay := scanTerminalResults(root, wiringByTC)

	_, okBats := overlay[overlayKey("tc-aaa33333", "bats")]
	_, okPW := overlay[overlayKey("tc-aaa33333", "playwright")]
	assert.False(t, okBats, "ambiguous adapter must not silently overlay against bats")
	assert.False(t, okPW, "ambiguous adapter must not silently overlay against playwright")
}

// TestPicker_DefaultFrameworkDoesNotOverrideFrameworkPrecedence pins the
// ENH-146 §"Decisions Inherited" rule that framework precedence is the
// picker tie-breaker after hash currency, and per-project configurability
// is rejected for v1. Specifically: when a TC has current bats AND
// current manual wiring (the rare-but-supported "committed manual
// artefact" case per CON-023 Edge Case 1), the picker must select bats
// (the framework-precedence winner at the same currency tier) regardless
// of the `defaultFramework` value, except in strict-framework mode.
//
// Before the fix, defaultFramework="manual" silently flipped the picker
// to manual within the top tier — that bypassed the pinned precedence.
func TestPicker_DefaultFrameworkDoesNotOverrideFrameworkPrecedence(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms/cases/tc-pp01-mixed.md", `---
test_case_id: tc-pp01
title: Mixed wiring TC
---
`)
	// Current bats wiring via the helper.
	seedLegacyRecord(t, root, legacyRecord{
		TC: "tc-pp01", Framework: "bats", Result: "pass",
	})
	// Current manual wiring written directly (seedLegacyRecord routes
	// framework=manual to gtms/manual/records/, but CON-023 Edge Case 1
	// allows manual-framework wiring when the artefact is a committed
	// manual runbook / procedure file). Hash mirrors bats so both wiring
	// records land at TierCurrent.
	manualArtefact := "test/manual/tc-pp01.md"
	manualAbs := filepath.Join(root, manualArtefact)
	require.NoError(t, os.MkdirAll(filepath.Dir(manualAbs), 0755))
	require.NoError(t, os.WriteFile(manualAbs, []byte("# manual runbook\n"), 0644))
	specAbs := filepath.Join(root, "gtms/cases/tc-pp01-mixed.md")
	specHash, err := pipeline.HashFile(specAbs)
	require.NoError(t, err)
	manualHash, err := pipeline.HashFile(manualAbs)
	require.NoError(t, err)
	_, err = wiring.Write(root, &wiring.WiringRecord{
		TestCase: "tc-pp01", TestCaseHash: specHash,
		Framework: "manual", Adapter: "manual-execute",
		Artefact: manualArtefact, ArtefactHash: manualHash,
	})
	require.NoError(t, err)

	// Non-strict mode with defaultFramework="manual": framework
	// precedence still wins — bats outranks manual at the same currency
	// tier per ENH-146 §"Decisions Inherited".
	entries, err := PipelineStatus(root, nil, "manual", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "bats", entries[0].SelectedFramework,
		"non-strict defaultFramework=manual must NOT override the bats > manual precedence")

	// Strict mode with defaultFramework="manual": the user has
	// explicitly asked for manual only — that filter is honoured per
	// the existing strict-framework contract.
	strict, err := PipelineStatus(root, nil, "manual", true)
	require.NoError(t, err)
	require.Len(t, strict, 1)
	assert.Equal(t, "manual", strict[0].SelectedFramework,
		"strict --framework=manual must select the manual wiring")
}

// TestScanTerminalResults_SameSecondCompletedTiebreaksByMtime pins the
// rule that when two terminal handoffs for the same (target, framework)
// share an RFC3339-second Completed stamp, the file with the later mtime
// wins. Without this tiebreaker, `os.ReadDir` iteration order picks the
// winner — non-deterministic on ext4, so a re-execute that completes in
// the same wall-clock second as the original error can fail to clear the
// dashboard. See tc-e3b7c650 in test/acceptance/stale-failed-override/.
func TestScanTerminalResults_SameSecondCompletedTiebreaksByMtime(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join("gtms/cases", "tc-tie01-reexec.md"), `---
test_case_id: tc-tie01
title: Same-second completed tiebreaker
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC: "tc-tie01", Framework: "bats", Adapter: "bats-runner",
		Artefact: "test/acceptance/tc-tie01.bats",
	})

	tied := "2026-05-28T17:30:45Z"

	rcErr := &result.ResultContract{
		Task: "task-older-err", Command: "execute", Target: "tc-tie01",
		Adapter: "bats-runner", Mode: "sync", Framework: "bats",
		Created: "2026-05-28T17:30:44Z", Status: "error",
		Completed: tied, Summary: "first run failed",
	}
	_, err := result.Create(root, rcErr)
	require.NoError(t, err)

	time.Sleep(20 * time.Millisecond)
	rcOK := &result.ResultContract{
		Task: "task-newer-ok", Command: "execute", Target: "tc-tie01",
		Adapter: "bats-runner", Mode: "sync", Framework: "bats",
		Created: "2026-05-28T17:30:45Z", Status: "complete", Result: "pass",
		Completed: tied, Summary: "re-execute passed",
	}
	_, err = result.Create(root, rcOK)
	require.NoError(t, err)

	wiringByTC, err := wiringScan(root)
	require.NoError(t, err)
	overlay := scanTerminalResults(root, wiringByTC)

	hit, ok := overlay[overlayKey("tc-tie01", "bats")]
	require.True(t, ok, "overlay must surface a hit for (tc-tie01, bats)")
	assert.Equal(t, "complete", hit.Status, "later-mtime handoff must win on Completed-stamp tie")
	assert.Equal(t, "pass", hit.Result, "result must reflect the later-mtime (complete) handoff")
}

// TestPipelineStatus_Join_ManualReadyPreserved pins the manual-ready
// preservation rule: a manual-only TC with a primed result template
// still surfaces as Manual-ready even though the overlay's rung-1
// verification now excludes orphan results.
//
// The signal source is gtms/manual/records/, scanned by scanManualByTC
// — independent of the wiring overlay. This test makes that explicit so
// a future change to the ladder cannot accidentally regress the manual
// pillar.
func TestPipelineStatus_Join_ManualReadyPreserved(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join("gtms/cases", "tc-aaa44444-manual.md"), `---
test_case_id: tc-aaa44444
title: Manual-only TC
---
`)
	// Manual TC: no wiring (Edge Case 1). Primed manual record under
	// gtms/manual/records/. Empty Result means "primed but not run."
	seedLegacyRecord(t, root, legacyRecord{
		TC: "tc-aaa44444", Framework: "manual", Result: "",
	})

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	entry := entries[0]

	assert.False(t, entry.Wired, "manual-only TC must not surface as wired")
	assert.True(t, entry.ManualReady, "manual-only TC with primed template must surface as Manual-ready")
	assert.Equal(t, "prepared", entry.ManualCoverage)
}
