package reader

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// Map returns a traceability report grouping test cases by their requirement field.
// When scope is nil, all test cases are included. When scope is non-nil, only test cases
// within the scope directory are included. Wiring records and tasks are always global.
// strictFramework (ENH-082) honours an explicit --framework flag in per-TC entries:
// when true and defaultFramework is non-empty, TCs without a matching wiring record
// render with no SelectedFramework and em-dashes for AUTOMATE/EXECUTE (no fallback).
// CON-023 / ENH-145 / ENH-146 — Phase 3D:
// Automation identity comes from wiring (gtms/automation/wiring/);
// execution state comes from the latest terminal handoff in .gtms/results/;
// manual-only TCs surface via gtms/manual/records/. The map shares the
// Phase 3C wiring-aware reader path (buildPipelineEntry) with status, so
// the picker, terminal-overlay discipline, manual-ready signal, and
// per-framework entries are computed once. The map then applies a
// worst-of-frameworks override on the compact LastResult / ExecuteStatus
// carriers so a sibling-framework failure cannot hide behind a
// picker-selected pass on the human row.
func Map(projectRoot string, scope *ScopeInfo, defaultFramework string, strictFramework bool) (*MapReport, error) {
	testCases, err := scanTestCases(projectRoot, scope)
	if err != nil {
		return nil, fmt.Errorf("scanning test cases: %w", err)
	}

	wiringByTC, err := wiringScan(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("scanning wiring records: %w", err)
	}
	overlay := scanTerminalResults(projectRoot, wiringByTC)
	manuals := scanManualByTC(projectRoot)
	tasks, err := scanTasks(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("scanning tasks: %w", err)
	}

	// Group test cases by requirement.
	groups := make(map[string]*RequirementGroup)
	unlinked := []MapEntry{}

	for _, tc := range testCases {
		entry := buildMapEntry(projectRoot, tc, wiringByTC[tc.ID], overlay, manuals[tc.ID], tasks, defaultFramework, strictFramework)

		if tc.Requirement == "" {
			unlinked = append(unlinked, entry)
			continue
		}

		grp, ok := groups[tc.Requirement]
		if !ok {
			grp = &RequirementGroup{Requirement: tc.Requirement}
			groups[tc.Requirement] = grp
		}
		grp.TestCases = append(grp.TestCases, entry)
	}

	// Convert map to sorted slice.
	sortedGroups := make([]RequirementGroup, 0, len(groups))
	for _, grp := range groups {
		// Sort test cases within group by ID.
		sort.Slice(grp.TestCases, func(i, j int) bool {
			return grp.TestCases[i].TestCaseID < grp.TestCases[j].TestCaseID
		})
		sortedGroups = append(sortedGroups, *grp)
	}

	// Sort groups alphabetically by requirement.
	sort.Slice(sortedGroups, func(i, j int) bool {
		return sortedGroups[i].Requirement < sortedGroups[j].Requirement
	})

	// Sort unlinked by ID.
	sort.Slice(unlinked, func(i, j int) bool {
		return unlinked[i].TestCaseID < unlinked[j].TestCaseID
	})

	// Build summary.
	totalTC := 0
	automated := 0
	executed := 0
	for _, grp := range sortedGroups {
		totalTC += len(grp.TestCases)
		for _, e := range grp.TestCases {
			if e.AutomateStatus == "complete" || e.AutomateStatus == "developed" {
				automated++
			}
			if e.LastResult != "none" {
				executed++
			}
		}
	}
	for _, e := range unlinked {
		totalTC++
		if e.AutomateStatus == "complete" || e.AutomateStatus == "developed" {
			automated++
		}
		if e.LastResult != "none" {
			executed++
		}
	}

	summary := MapSummary{
		TotalRequirements: len(sortedGroups),
		TotalTestCases:    totalTC,
		Automated:         automated,
		Executed:          executed,
		UnlinkedCount:     len(unlinked),
	}

	return &MapReport{
		Groups:   sortedGroups,
		Unlinked: unlinked,
		Summary:  summary,
	}, nil
}

// buildMapEntry constructs a MapEntry from a test case, its wiring
// records, the global terminal-result overlay, the manual record (if
// any), and active tasks. Routes through buildPipelineEntry so the map
// shares the Phase 3C wiring-aware path with status, then applies the
// map-specific worst-of-frameworks override on the compact carriers
// BEFORE active-task overlays so task-derived execute state always has
// the final say.
//
// Phase 3D fix-pass (review Finding 1): worst-of runs on the PipelineEntry
// before applyTaskStatus. Reason: applyTaskStatus is the final, authoritative
// overlay for pending/in-progress/task-error state. If worst-of runs after
// it, a sibling-framework terminal fail can clobber a task-derived in-progress
// or task-error label and the user loses visibility of the active run.
// Running worst-of first lets applyTaskStatus take the bumped LastResult
// into account when deciding whether a stale execute-error task is
// superseded by a passing record (it correctly says "no" when the
// worst-of bump made LastResult=fail/error/skipped), so task-error and
// in-progress state still win.
//
// CON-023 / ENH-146: orphan terminal handoffs (no matching wiring) are
// ignored — buildPipelineEntry only joins overlay entries on (TC,
// framework) keys taken from wiring. Non-terminal handoffs are
// pre-filtered in scanTerminalResults.
func buildMapEntry(
	projectRoot string,
	tc testCaseFrontmatter,
	wiringRecs []*wiring.WiringRecord,
	overlay map[string]overlayHit,
	manual *manualRecord,
	tasks []taskFrontmatter,
	defaultFramework string,
	strictFramework bool,
) MapEntry {
	pe := buildPipelineEntry(projectRoot, tc, wiringRecs, overlay, manual, defaultFramework, strictFramework)

	// Worst-of-frameworks compact override: pin the Phase 3D rule that
	// "Human output may stay compact, but it must not report the TC as
	// unwired or passing solely because the selected framework passes
	// while a sibling wired framework has terminal fail/error/skip state."
	//
	// Scoping rules:
	//   - Non-strict mode: every wired framework contributes — a sibling
	//     failure must not hide behind a picker-selected pass.
	//   - Strict mode with --framework X: the user has explicitly asked
	//     for X only, so sibling frameworks are out of scope. Restrict
	//     worst-of to the selected framework, which collapses to "use
	//     the picker's overlay" — matches the existing strict-framework
	//     contract pinned by TestMap_StrictFrameworkOmitsNonMatching.
	//   - Strict-mode miss (no matching wiring): no relevant framework,
	//     so worst-of contributes nothing and the em-dash carries through.
	//   - Manual-only TCs are skipped — they're not wiring units, and
	//     the manual record drives the compact LastResult upstream.
	//
	// Applied on PipelineEntry before applyTaskStatus (see function-level
	// comment for the ordering rationale).
	if pe.Wired {
		// Worst-of considers only WIRED frameworks (real wiring units). The
		// synthesized result-file `manual` entry (Wired==false, BUG-127) must NOT
		// bleed into the compact default-view result -- per Option A it surfaces
		// only under explicit --framework manual (resolved in buildPipelineEntry).
		relevant := make([]FrameworkEntry, 0, len(pe.Frameworks))
		for _, fe := range pe.Frameworks {
			if fe.Wired {
				relevant = append(relevant, fe)
			}
		}
		if strictFramework && defaultFramework != "" {
			relevant = frameworksWithName(relevant, defaultFramework)
		}
		worstResult, worstExec := worstFrameworkOutcome(relevant)
		if outcomePriority(worstResult) > outcomePriority(pe.LastResult) {
			pe.LastResult = worstResult
			pe.ExecuteStatus = worstExec
		}
	}

	applyTaskStatus(&pe, tasks)

	entry := MapEntry{
		TestCaseID:            pe.TestCaseID,
		Slug:                  pe.Slug,
		Title:                 pe.Title,
		CreateStatus:          pe.CreateStatus,
		AutomateStatus:        pe.AutomateStatus,
		ExecuteStatus:         pe.ExecuteStatus,
		LastResult:            pe.LastResult,
		Stale:                 pe.Stale,
		ManualCoverage:        pe.ManualCoverage,
		AvailableFrameworks:   pe.AvailableFrameworks,
		DriftDetected:         pe.DriftDetected,
		DriftDetectedAt:       pe.DriftDetectedAt,
		TestCaseHashAtExecute: pe.TestCaseHashAtExecute,
		Wired:                 pe.Wired,
		ManualReady:           pe.ManualReady,
		SelectedFramework:     pe.SelectedFramework,
		Frameworks:            pe.Frameworks,
	}

	// Default to "none" for the CLI label vocabulary when nothing was
	// populated by the wiring/overlay path (matches the pre-cutover
	// renderer behaviour for unwired TCs).
	if entry.AutomateStatus == "" {
		entry.AutomateStatus = "none"
	}
	if entry.ExecuteStatus == "" {
		entry.ExecuteStatus = "none"
	}
	if entry.LastResult == "" {
		entry.LastResult = "none"
	}

	// ArtefactPath: when a framework is selected, surface its wiring
	// artefact verbatim on the top-level carrier so the existing slug
	// view / detail view CLI consumers keep working. The same value is
	// also present inside Frameworks[<selected>].Artefact.
	if pe.SelectedFramework != "" {
		for _, fe := range pe.Frameworks {
			if fe.Framework == pe.SelectedFramework {
				entry.ArtefactPath = fe.Artefact
				break
			}
		}
	}

	return entry
}

// deriveSlug extracts the slug portion from a test case filename.
// e.g. "tc-a1b2c3d-tier1-sync-happy-path.md" → "tier1-sync-happy-path"
func deriveSlug(tc testCaseFrontmatter) string {
	if tc.SourceFile == "" {
		return ""
	}
	base := filepath.Base(tc.SourceFile)
	noExt := strings.TrimSuffix(base, ".md")
	if len(noExt) > len(tc.ID)+1 {
		return noExt[len(tc.ID)+1:]
	}
	return ""
}

// outcomePriority orders compact LastResult values from "lowest signal"
// to "highest signal" for the worst-of override. Higher wins.
//
//	none < pass < skipped < error < fail
//
// fail beats error so a confirmed test outcome failure dominates an
// adapter failure on the compact display. error beats skipped so an
// adapter failure is more visible than a deliberate skip. skipped
// beats pass so any non-pass outcome surfaces over a sibling pass.
func outcomePriority(result string) int {
	switch result {
	case "fail":
		return 4
	case "error":
		return 3
	case "skipped":
		return 2
	case "pass":
		return 1
	default:
		return 0
	}
}

// worstFrameworkOutcome walks Frameworks[] and returns the worst
// (LastResult, ExecuteStatus) pair seen across the per-framework overlay
// hits. Frameworks without a terminal overlay contribute nothing.
func worstFrameworkOutcome(frameworks []FrameworkEntry) (string, string) {
	worstResult := "none"
	worstExec := "none"
	for _, fe := range frameworks {
		// Adapter failure: status:error with no result.
		if fe.LastStatusHere == "error" {
			candidate := "error"
			if outcomePriority(candidate) > outcomePriority(worstResult) {
				worstResult = candidate
				worstExec = "error"
			}
			continue
		}
		if fe.LastResultHere == "" {
			continue
		}
		candidate := fe.LastResultHere
		if outcomePriority(candidate) > outcomePriority(worstResult) {
			worstResult = candidate
			worstExec = legacyExecuteStatusForResult(candidate)
		}
	}
	return worstResult, worstExec
}

// frameworksWithName returns the subset of Frameworks[] whose framework
// name matches `name`. Returns an empty slice if no match. Used by the
// worst-of override to restrict the scan in strict-framework mode.
func frameworksWithName(frameworks []FrameworkEntry, name string) []FrameworkEntry {
	out := make([]FrameworkEntry, 0, 1)
	for _, fe := range frameworks {
		if fe.Framework == name {
			out = append(out, fe)
		}
	}
	return out
}

// legacyExecuteStatusForResult maps a compact LastResult value to the
// legacy ExecuteStatus column vocabulary. Mirrors legacyExecuteStatus
// in wiring_scan.go but accepts the post-overlay normalised result
// label directly.
func legacyExecuteStatusForResult(result string) string {
	switch result {
	case "error":
		return "error"
	case "skipped":
		return "skipped"
	case "pass", "fail":
		return "complete"
	default:
		return "none"
	}
}
