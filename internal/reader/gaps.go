package reader

import (
	"fmt"
	"sort"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
)

// Gaps returns all coverage gaps. Categories surfaced post-CON-023 / ENH-146:
//
//  1. NoTests           — requirements with no test cases linked
//  2. NoAutomation      — test cases with no wiring records (manual-only TCs included)
//  3. CurrentlyFailing  — wiring units whose latest terminal result is "fail"
//  4. ExecutionErrors   — wiring units whose latest terminal handoff has
//                         status: error (adapter failure, disjoint from
//                         CurrentlyFailing per ENH-130)
//  5. RuntimeSkipped    — wiring units whose latest terminal result is "skipped"
//  6. StaleTestCaseHash — wiring units where testcase-hash differs from current spec content
//  7. StaleArtefactHash — wiring units where artefact-hash differs from current artefact content
//  8. MissingArtefact   — wiring units whose artefact path does not resolve on disk
//  9. DriftDetected     — manual TCs with drift-detected: true in their result file
//
// Retired per CON-023 / ENH-146: SpecButNoRecord, NeverExecuted, StaleExecution.
// "Not run here" is not a gap — it's the expected state on a fresh clone.
//
// When scope is nil, all test cases are included. When scope is non-nil, only test cases
// within the scope directory are included. Wiring records are always global.
// strictFramework (ENH-082) honours an explicit --framework flag: when true and
// defaultFramework is non-empty, TCs without a matching wiring record for that
// framework are not classified into the per-wiring gap categories
// (CurrentlyFailing / ExecutionErrors / RuntimeSkipped / Stale* / MissingArtefact).
// NoAutomation classification is unchanged (driven by hasNonManualRecord).
func Gaps(projectRoot string, scope *ScopeInfo, defaultFramework string, strictFramework bool) (*GapReport, error) {
	testCases, err := scanTestCases(projectRoot, scope)
	if err != nil {
		return nil, fmt.Errorf("scanning test cases: %w", err)
	}

	autoRecords, err := scanAutomationRecords(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("scanning automation records: %w", err)
	}

	report := &GapReport{
		TotalTestCases: len(testCases),
	}

	// Build a set of requirements that have test cases
	reqsWithTests := make(map[string]bool)
	// Build a set of all known requirements (from test cases)
	allReqs := make(map[string]string) // req ID -> first test case title that references it

	for _, tc := range testCases {
		if tc.Requirement != "" {
			reqsWithTests[tc.Requirement] = true
			if _, ok := allReqs[tc.Requirement]; !ok {
				allReqs[tc.Requirement] = tc.Title
			}
		}
	}

	// Category 1: Requirements without test cases
	// Note: We can only detect requirements referenced by existing test cases.
	// If a requirement has zero test cases, we won't know about it unless
	// we scan another source. For now, this category stays empty unless
	// there's an external requirements list.
	// The gap is detected in the "no automation" and other categories instead.

	// Category 2: Test cases without automation
	// Test cases that exist but have no automation record (consistent with status command).
	// Manual-only records (framework: manual) count as "not automated" — manual testing
	// is an on-ramp, not a substitute for real automation.
	// ENH-134: populate ManualCoverage sub-state on each NoAutomation entry so
	// consumers can disambiguate no-coverage from prepared-as-manual from recorded-as-manual.
	for _, tc := range testCases {
		if !hasNonManualRecord(autoRecords[tc.ID]) {
			report.NoAutomation = append(report.NoAutomation, GapEntry{
				ID:             tc.ID,
				Title:          tc.Title,
				ManualCoverage: deriveManualCoverage(autoRecords[tc.ID]),
			})
		}
	}

	// NeverExecuted is retired per CON-023 / ENH-146.
	// "Not run here" is a status, not a gap — fresh clones legitimately
	// have all wiring un-run locally.

	// Categories 4 / 7 / 8 / 9 — result-based per-wiring-unit gaps + manual drift.
	// CurrentlyFailing, ExecutionErrors, and RuntimeSkipped are wiring-unit
	// categories per the GapReport doc and ENH-146 §"Counting unit
	// discipline": every relevant wiring record on a TC contributes, so a
	// passing primary framework cannot hide a failing/erroring/skipped
	// sibling. Manual-only TCs have no wiring; manual rows (synthesised by
	// scanAutomationRecords from gtms/manual/records/) therefore do NOT
	// contribute to these three categories. Manual coverage surfaces via
	// NoAutomation.ManualCoverage and the Manual-ready status view; manual
	// fail/skip is captured in the manual result file and visible in the
	// status detail view.
	//
	// DriftDetected follows the BUG-079 selected-record rule (BUG-086 fix):
	// drift is checked ONLY on the selected automation record, not across
	// every row. If the selected record is non-manual (e.g. bats), drift
	// does NOT count even if a sibling manual result file has drift fields.
	// This matches the status surfaces' behaviour and prevents over-counting
	// in multi-framework TCs.
	// BUG-079: a manual-only TC can appear in both NoAutomation and
	// DriftDetected.
	//
	// Output remains one TC per category (deduped via the local saw*
	// booleans). Strict --framework filtering (ENH-082) restricts result-
	// based classification to records of that framework; in non-strict mode
	// every wiring record is considered.
	//
	// Adapter-failure executions surface as ExecutionErrors via the legacy
	// carrier's status→result synthesis in status.go::scanAutomationRecords.
	// Categories SpecButNoRecord and StaleExecution are retired.
	for _, tc := range testCases {
		records, hasAuto := autoRecords[tc.ID]
		if !hasAuto {
			continue
		}
		var sawFail, sawError, sawSkipped, sawDrift bool

		// BUG-086: drift uses the selected-record rule, not the
		// all-records approach used by wiring-unit categories.
		selected := selectAutomationRecord(records, defaultFramework, strictFramework)
		if selected.Framework == "manual" {
			if readManualDriftDiagnostics(projectRoot, selected).DriftDetected {
				sawDrift = true
			}
		}

		for _, ar := range records {
			if strictFramework && defaultFramework != "" && ar.Framework != defaultFramework {
				continue
			}
			// Result-based wiring-unit categories: manual rows excluded.
			if ar.Framework != "manual" {
				switch ar.Result {
				case "fail":
					sawFail = true
				case "error":
					sawError = true
				case "skipped":
					sawSkipped = true
				}
			}
		}
		if sawFail {
			report.CurrentlyFailing = append(report.CurrentlyFailing, GapEntry{ID: tc.ID, Title: tc.Title})
		}
		if sawError {
			report.ExecutionErrors = append(report.ExecutionErrors, GapEntry{ID: tc.ID, Title: tc.Title})
		}
		if sawSkipped {
			report.RuntimeSkipped = append(report.RuntimeSkipped, GapEntry{ID: tc.ID, Title: tc.Title})
		}
		if sawDrift {
			report.DriftDetected = append(report.DriftDetected, GapEntry{ID: tc.ID, Title: tc.Title})
		}
	}

	// Categories 10-12: wiring drift / missing artefact (CON-023 / ENH-146).
	// These are wiring-unit categories — every relevant wiring record on the
	// TC contributes. ENH-146 §"Counting unit discipline" calls these out as
	// per-wiring counts so multi-framework TCs do not hide a stale/missing
	// framework behind a current one (picker-only classification was the
	// bug). Output remains one line per TC: a TC enters each category at
	// most once even when several of its wiring records share the gap.
	//
	// Strict-framework filtering (ENH-082): explicit --framework restricts
	// classification to that framework's wiring record. Without --framework
	// every wiring record is considered.
	wiringByTC, _ := wiringScan(projectRoot)
	for _, tc := range testCases {
		recs := wiringByTC[tc.ID]
		if len(recs) == 0 {
			continue
		}
		var (
			sawMissingArtefact bool
			sawStaleTestcase   bool
			sawStaleArtefact   bool
		)
		for _, w := range recs {
			if strictFramework && defaultFramework != "" && w.Framework != defaultFramework {
				continue
			}
			c := ClassifyWiring(projectRoot, w)
			// ClassifyWiring suppresses StaleArtefactHash whenever the artefact
			// is missing — the hash cannot be computed and we never claim
			// artefact drift in that state.
			if c.MissingArtefact {
				sawMissingArtefact = true
			}
			if c.StaleTestcaseHash {
				sawStaleTestcase = true
			}
			if c.StaleArtefactHash {
				sawStaleArtefact = true
			}
		}
		if sawMissingArtefact {
			report.MissingArtefact = append(report.MissingArtefact, GapEntry{ID: tc.ID, Title: tc.Title})
		}
		if sawStaleTestcase {
			report.StaleTestCaseHash = append(report.StaleTestCaseHash, GapEntry{ID: tc.ID, Title: tc.Title})
		}
		if sawStaleArtefact {
			report.StaleArtefactHash = append(report.StaleArtefactHash, GapEntry{ID: tc.ID, Title: tc.Title})
		}
	}

	// Sort all categories by ID for deterministic output
	sortGapEntries(report.NoTests)
	sortGapEntries(report.NoAutomation)
	sortGapEntries(report.CurrentlyFailing)
	sortGapEntries(report.ExecutionErrors)
	sortGapEntries(report.RuntimeSkipped)
	sortGapEntries(report.StaleTestCaseHash)
	sortGapEntries(report.StaleArtefactHash)
	sortGapEntries(report.MissingArtefact)
	sortGapEntries(report.DriftDetected)

	return report, nil
}

// hasFrameworkRecord returns true if the slice contains at least one automation record
// matching the given framework exactly. An empty or nil slice returns false.
func hasFrameworkRecord(records []automationFrontmatter, framework string) bool {
	for _, r := range records {
		if r.Framework == framework {
			return true
		}
	}
	return false
}

// hasNonManualRecord returns true if the slice contains at least one automation record
// with a framework other than "manual". An empty or nil slice returns false.
func hasNonManualRecord(records []automationFrontmatter) bool {
	for _, r := range records {
		if r.Framework != "manual" {
			return true
		}
	}
	return false
}

// sortGapEntries sorts gap entries by ID.
func sortGapEntries(entries []GapEntry) {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].ID < entries[j].ID
	})
}

// TotalGaps returns the total number of gaps across all categories.
func (r *GapReport) TotalGaps() int {
	return len(r.NoTests) + len(r.NoAutomation) + len(r.CurrentlyFailing) +
		len(r.ExecutionErrors) + len(r.RuntimeSkipped) +
		len(r.StaleTestCaseHash) + len(r.StaleArtefactHash) + len(r.MissingArtefact) +
		len(r.DriftDetected)
}

// GapsFolderSummary returns aggregate gap counts per folder.
// It scans all test cases recursively and groups by the immediate subfolder under gtms/test/cases/.
// CON-023 / ENH-146: spec-file scanning was retired alongside the SpecButNoRecord
// category, and "not run here" is no longer counted as a gap. Folder summary
// counts come exclusively from wiring records overlaid with terminal result
// contracts.
func GapsFolderSummary(projectRoot string, defaultFramework string) ([]GapsFolderSummaryEntry, error) {
	testCases, err := scanTestCases(projectRoot, nil)
	if err != nil {
		return nil, fmt.Errorf("scanning test cases: %w", err)
	}

	autoRecords, err := scanAutomationRecords(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("scanning automation records: %w", err)
	}

	tcDir := layout.TestCasesDir(projectRoot)

	type folderAccum struct {
		created      int
		notAutomated int
		failing      int
		skipped      int
		// BUG-043: TCs with records under other frameworks but not the requested one.
		frameworkMismatch int
		// ENH-134: manual-coverage sub-state counts.
		manualPrepared int
		manualRecorded int
		// ENH-117: count of TCs with stale testcase-hash.
		staleTestCaseHash int
		// BUG-079: count of TCs with drift-detected: true in their manual result file.
		driftDetected int
	}
	folders := make(map[string]*folderAccum)

	for _, tc := range testCases {
		folder := deriveFolderName(tcDir, tc.SourceFile)

		acc, ok := folders[folder]
		if !ok {
			acc = &folderAccum{}
			folders[folder] = acc
		}

		acc.created++

		records := autoRecords[tc.ID]

		// ENH-134: manual-coverage sub-state counts derived from ALL records,
		// before framework filtering.
		mc := deriveManualCoverage(records)
		switch mc {
		case "prepared":
			acc.manualPrepared++
		case "recorded":
			acc.manualRecorded++
		}

		// BUG-086: drift uses the selected-record rule, not the all-records
		// approach used by wiring-unit categories. Compute the selected
		// record once; check drift only when the selected record is manual.
		// GapsFolderSummary does not support strict --framework, so pass
		// false. When defaultFramework is set, selectAutomationRecord prefers
		// the matching framework; otherwise it uses the highest-cycle /
		// non-manual-preference fallback.
		selected := selectAutomationRecord(records, defaultFramework, false)
		driftOnSelected := selected.Framework == "manual" &&
			readManualDriftDiagnostics(projectRoot, selected).DriftDetected

		// Framework gating.
		// With --framework: skip the TC entirely if no record matches that
		// framework, and only consider matching records below. Without
		// --framework: any non-manual record is enough to clear NotAutomated;
		// otherwise mark as not-automated and still check manual drift.
		if defaultFramework != "" {
			if !hasFrameworkRecord(records, defaultFramework) {
				acc.notAutomated++
				// BUG-043: if records exist under other frameworks, count as
				// framework mismatch so --json consumers can distinguish from
				// "not automated at all".
				if len(records) > 0 {
					acc.frameworkMismatch++
				}
				// BUG-086: drift follows the selected-record rule.
				if driftOnSelected {
					acc.driftDetected++
				}
				continue
			}
		} else if !hasNonManualRecord(records) {
			acc.notAutomated++
			// BUG-086: manual-only TCs still need drift check via
			// the selected record (which is manual in this branch).
			if driftOnSelected {
				acc.driftDetected++
			}
			continue
		}

		// All-records aggregation (CON-023 / ENH-146 §"Counting unit
		// discipline"). A passing primary framework cannot hide a failing,
		// skipped, or stale sibling framework on the same TC. Each counter
		// increments at most once per TC.
		//
		// - Result-based wiring-unit categories (failing, skipped,
		//   staleTestCaseHash) iterate non-manual records; manual rows are
		//   excluded because manual-only TCs are not wiring units.
		// - driftDetected uses the selected-record rule (BUG-086), computed
		//   above as driftOnSelected.
		// - With --framework, restrict to matching wiring records.
		// CON-023 / ENH-146: "Not run here" is not a gap, so there is no
		// notExecuted increment here.
		var sawFail, sawSkipped, sawStaleTC bool
		for _, ar := range records {
			if defaultFramework != "" && ar.Framework != defaultFramework {
				continue
			}
			if ar.Framework == "manual" {
				continue
			}
			switch ar.Result {
			case "fail":
				sawFail = true
			case "skipped":
				sawSkipped = true
			}
			if isStaleTestCaseHash(projectRoot, ar) {
				sawStaleTC = true
			}
		}
		if sawFail {
			acc.failing++
		}
		if sawSkipped {
			acc.skipped++
		}
		if sawStaleTC {
			acc.staleTestCaseHash++
		}
		if driftOnSelected {
			acc.driftDetected++
		}
	}

	// Convert to sorted slice
	entries := make([]GapsFolderSummaryEntry, 0, len(folders))
	for name, acc := range folders {
		entries = append(entries, GapsFolderSummaryEntry{
			Folder:            name,
			Created:           acc.created,
			NotAutomated:      acc.notAutomated,
			// NotExecuted retired per CON-023 / ENH-146; field kept on the
			// struct for JSON shape stability but always zero.
			Failing:           acc.failing,
			Skipped:           acc.skipped,
			FrameworkMismatch: acc.frameworkMismatch,
			ManualPrepared:    acc.manualPrepared,
			ManualRecorded:    acc.manualRecorded,
			StaleTestCaseHash: acc.staleTestCaseHash,
			DriftDetected:     acc.driftDetected,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Folder < entries[j].Folder
	})

	return entries, nil
}
