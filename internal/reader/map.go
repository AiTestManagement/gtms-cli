package reader

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Map returns a traceability report grouping test cases by their requirement field.
// When scope is nil, all test cases are included. When scope is non-nil, only test cases
// within the scope directory are included. Automation records and tasks are always global.
func Map(projectRoot string, scope *ScopeInfo) (*MapReport, error) {
	testCases, err := scanTestCases(projectRoot, scope)
	if err != nil {
		return nil, fmt.Errorf("scanning test cases: %w", err)
	}

	autoRecords, err := scanAutomationRecords(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("scanning automation records: %w", err)
	}

	tasks, err := scanTasks(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("scanning tasks: %w", err)
	}

	// Group test cases by requirement
	groups := make(map[string]*RequirementGroup)
	unlinked := []MapEntry{}

	for _, tc := range testCases {
		entry := buildMapEntry(tc, autoRecords, tasks)

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

	// Convert map to sorted slice
	sortedGroups := make([]RequirementGroup, 0, len(groups))
	for _, grp := range groups {
		// Sort test cases within group by ID
		sort.Slice(grp.TestCases, func(i, j int) bool {
			return grp.TestCases[i].TestCaseID < grp.TestCases[j].TestCaseID
		})
		sortedGroups = append(sortedGroups, *grp)
	}

	// Sort groups alphabetically by requirement
	sort.Slice(sortedGroups, func(i, j int) bool {
		return sortedGroups[i].Requirement < sortedGroups[j].Requirement
	})

	// Sort unlinked by ID
	sort.Slice(unlinked, func(i, j int) bool {
		return unlinked[i].TestCaseID < unlinked[j].TestCaseID
	})

	// Build summary
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

// buildMapEntry constructs a MapEntry from a test case, automation records, and active tasks.
func buildMapEntry(tc testCaseFrontmatter, autoRecords map[string]automationFrontmatter, tasks []taskFrontmatter) MapEntry {
	entry := MapEntry{
		TestCaseID:     tc.ID,
		Slug:           deriveSlug(tc),
		Title:          tc.Title,
		CreateStatus:   "complete", // file exists = created
		AutomateStatus: "none",
		ExecuteStatus:  "none",
		LastResult:     "none",
	}

	// Check automation status
	if ar, ok := autoRecords[tc.ID]; ok {
		entry.AutomateStatus = deriveAutomateStatus(ar)
		entry.LastResult, _ = deriveExecuteResult(ar)
		if entry.LastResult != "none" {
			entry.ExecuteStatus = "complete"
		}
	}

	// Apply active task overrides via PipelineEntry adapter
	pipeEntry := PipelineEntry{
		TestCaseID:     entry.TestCaseID,
		CreateStatus:   entry.CreateStatus,
		AutomateStatus: entry.AutomateStatus,
		ExecuteStatus:  entry.ExecuteStatus,
		LastResult:     entry.LastResult,
	}
	applyTaskStatus(&pipeEntry, tasks)
	entry.CreateStatus = pipeEntry.CreateStatus
	entry.AutomateStatus = pipeEntry.AutomateStatus
	entry.ExecuteStatus = pipeEntry.ExecuteStatus

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
