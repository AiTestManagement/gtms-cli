package reader

import (
	"fmt"
	"sort"
	"strings"
)

// Gaps returns all coverage gaps across five categories:
//  1. NoTests — requirements with no test cases linked
//  2. NoAutomation — test cases with no automation record (consistent with status command)
//  3. NeverExecuted — automation records with status "developed" or "accepted" but no execution results
//  4. CurrentlyFailing — automation records where last-formal-result is "fail"
//  5. SpecButNoRecord — test cases referenced in spec files but lacking an automation record
//
// When scope is nil, all test cases are included. When scope is non-nil, only test cases
// within the scope directory are included. Automation records and spec files are always global.
func Gaps(projectRoot string, specDirs []string, scope *ScopeInfo) (*GapReport, error) {
	testCases, err := scanTestCases(projectRoot, scope)
	if err != nil {
		return nil, fmt.Errorf("scanning test cases: %w", err)
	}

	autoRecords, err := scanAutomationRecords(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("scanning automation records: %w", err)
	}

	specCoverage, err := ScanSpecFiles(projectRoot, specDirs)
	if err != nil {
		return nil, fmt.Errorf("scanning spec files: %w", err)
	}

	report := &GapReport{}

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
	// Before BUG-015, this checked spec file references via ScanSpecFiles which disagreed
	// with the status command's automation-record-based definition.
	for _, tc := range testCases {
		if _, hasRecord := autoRecords[tc.ID]; !hasRecord {
			report.NoAutomation = append(report.NoAutomation, GapEntry{
				ID:    tc.ID,
				Title: tc.Title,
			})
		}
	}

	// Category 3: Automated but never executed
	// Automation records with status "developed" or "accepted" but no last-formal-result
	for _, tc := range testCases {
		ar, hasAuto := autoRecords[tc.ID]
		if !hasAuto {
			continue
		}
		if (ar.Status == "accepted" || ar.Status == "developed") && ar.LastFormalResult == "" {
			report.NeverExecuted = append(report.NeverExecuted, GapEntry{
				ID:    tc.ID,
				Title: tc.Title,
			})
		}
	}

	// Category 4: Currently failing
	// Automation records where last-formal-result is "fail"
	for _, tc := range testCases {
		ar, hasAuto := autoRecords[tc.ID]
		if !hasAuto {
			continue
		}
		if ar.LastFormalResult == "fail" {
			report.CurrentlyFailing = append(report.CurrentlyFailing, GapEntry{
				ID:    tc.ID,
				Title: tc.Title,
			})
		}
	}

	// Category 5: Spec coverage but no automation record
	// Test cases referenced in spec files but lacking a formal automation record.
	// This is informational — the spec exists but the pipeline isn't formally tracking it.
	for _, tc := range testCases {
		_, hasSpec := specCoverage[strings.ToLower(tc.ID)]
		_, hasRecord := autoRecords[tc.ID]
		if hasSpec && !hasRecord {
			report.SpecButNoRecord = append(report.SpecButNoRecord, GapEntry{
				ID:    tc.ID,
				Title: tc.Title,
			})
		}
	}

	// Sort all categories by ID for deterministic output
	sortGapEntries(report.NoTests)
	sortGapEntries(report.NoAutomation)
	sortGapEntries(report.NeverExecuted)
	sortGapEntries(report.CurrentlyFailing)
	sortGapEntries(report.SpecButNoRecord)

	return report, nil
}

// sortGapEntries sorts gap entries by ID.
func sortGapEntries(entries []GapEntry) {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].ID < entries[j].ID
	})
}

// TotalGaps returns the total number of gaps across all categories.
func (r *GapReport) TotalGaps() int {
	return len(r.NoTests) + len(r.NoAutomation) + len(r.NeverExecuted) + len(r.CurrentlyFailing) + len(r.SpecButNoRecord)
}
