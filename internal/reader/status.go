package reader

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/adrg/frontmatter"
)

// PipelineStatus returns the pipeline status of test cases in the project.
// When scope is nil, it scans all test cases (backward compatible).
// When scope is non-nil, it scans only test cases within the scope directory.
func PipelineStatus(projectRoot string, scope *ScopeInfo) ([]PipelineEntry, error) {
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

	// Build pipeline entries from test cases
	entries := make([]PipelineEntry, 0, len(testCases))
	for _, tc := range testCases {
		entry := PipelineEntry{
			TestCaseID:     tc.ID,
			Slug:           deriveSlug(tc),
			Title:          tc.Title,
			CreateStatus:   deriveCreateStatus(tc),
			AutomateStatus: "none",
			ExecuteStatus:  "none",
			LastResult:     "none",
		}

		// Check automation status
		if ar, ok := autoRecords[tc.ID]; ok {
			entry.AutomateStatus = deriveAutomateStatus(ar)
			entry.LastResult, entry.LastResultDate = deriveExecuteResult(ar)
			if entry.LastResult != "none" {
				entry.ExecuteStatus = "complete"
			}
		}

		// Check for active tasks that might override status
		applyTaskStatus(&entry, tasks)

		entries = append(entries, entry)
	}

	// Sort by test case ID for deterministic output
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].TestCaseID < entries[j].TestCaseID
	})

	return entries, nil
}

// PipelineDetail returns detailed pipeline information for a single test case.
// It always performs an unscoped (global) scan — Convention 4: ID-based lookups are global.
func PipelineDetail(projectRoot, testCaseID string) (*PipelineDetailEntry, error) {
	testCases, err := scanTestCases(projectRoot, nil)
	if err != nil {
		return nil, fmt.Errorf("scanning test cases: %w", err)
	}

	// Find the requested test case
	var tc *testCaseFrontmatter
	for _, candidate := range testCases {
		if candidate.ID == testCaseID {
			c := candidate
			tc = &c
			break
		}
	}
	if tc == nil {
		return nil, fmt.Errorf("test case '%s' not found", testCaseID)
	}

	autoRecords, err := scanAutomationRecords(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("scanning automation records: %w", err)
	}

	tasks, err := scanTasks(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("scanning tasks: %w", err)
	}

	detail := &PipelineDetailEntry{
		TestCaseID:     tc.ID,
		Slug:           deriveSlug(*tc),
		Title:          tc.Title,
		Requirement:    tc.Requirement,
		CreateStatus:   deriveCreateStatus(*tc),
		AutomateStatus: "none",
		ExecuteStatus:  "none",
		LastResult:     "none",
		Tags:           tc.Tags,
	}

	if ar, ok := autoRecords[tc.ID]; ok {
		detail.AutomateStatus = deriveAutomateStatus(ar)
		detail.Framework = ar.Framework
		detail.ArtefactPath = ar.Artefact
		detail.LastRunPath = ar.LastFormalRun
		detail.LastResult, detail.LastResultDate = deriveExecuteResult(ar)
		if detail.LastResult != "none" {
			detail.ExecuteStatus = "complete"
		}
	}

	// Apply active task overrides
	pipeEntry := PipelineEntry{
		TestCaseID:     detail.TestCaseID,
		CreateStatus:   detail.CreateStatus,
		AutomateStatus: detail.AutomateStatus,
		ExecuteStatus:  detail.ExecuteStatus,
		LastResult:     detail.LastResult,
		LastResultDate: detail.LastResultDate,
	}
	applyTaskStatus(&pipeEntry, tasks)
	detail.CreateStatus = pipeEntry.CreateStatus
	detail.AutomateStatus = pipeEntry.AutomateStatus
	detail.ExecuteStatus = pipeEntry.ExecuteStatus

	return detail, nil
}

// scanTestCases reads test case markdown files.
// When scope is nil, it recursively scans all of test-cases/ (backward compatible).
// When scope is non-nil and Recursive is true, it uses filepath.Walk from scope.ScanDir.
// When scope is non-nil and Recursive is false, it uses os.ReadDir for shallow scanning.
func scanTestCases(projectRoot string, scope *ScopeInfo) ([]testCaseFrontmatter, error) {
	if scope == nil {
		// Backward compatible: scan all test cases recursively
		return scanTestCasesAll(projectRoot)
	}

	if scope.Recursive {
		return scanTestCasesRecursive(scope.ScanDir)
	}

	return scanTestCasesShallow(scope.ScanDir)
}

// scanTestCasesAll recursively scans all test case files under test-cases/.
func scanTestCasesAll(projectRoot string) ([]testCaseFrontmatter, error) {
	tcDir := filepath.Join(projectRoot, "test-cases")
	return scanTestCasesRecursive(tcDir)
}

// scanTestCasesRecursive uses filepath.Walk to scan a directory and all subdirectories.
func scanTestCasesRecursive(scanDir string) ([]testCaseFrontmatter, error) {
	if _, err := os.Stat(scanDir); os.IsNotExist(err) {
		return nil, nil // directory doesn't exist is fine
	}

	var results []testCaseFrontmatter

	err := filepath.Walk(scanDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".md") {
			return nil
		}

		tc, err := parseTestCaseFile(path)
		if err != nil {
			// Skip malformed files with a warning to stderr
			fmt.Fprintf(os.Stderr, "warning: skipping %s: %v\n", path, err)
			return nil
		}
		if tc.ID != "" {
			tc.SourceFile = path
			results = append(results, *tc)
		}
		return nil
	})

	return results, err
}

// scanTestCasesShallow reads only the .md files directly in the given directory,
// without descending into subdirectories.
func scanTestCasesShallow(scanDir string) ([]testCaseFrontmatter, error) {
	if _, err := os.Stat(scanDir); os.IsNotExist(err) {
		return nil, nil // directory doesn't exist is fine
	}

	entries, err := os.ReadDir(scanDir)
	if err != nil {
		return nil, err
	}

	var results []testCaseFrontmatter
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		path := filepath.Join(scanDir, entry.Name())
		tc, err := parseTestCaseFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: skipping %s: %v\n", path, err)
			continue
		}
		if tc.ID != "" {
			tc.SourceFile = path
			results = append(results, *tc)
		}
	}

	return results, nil
}

// parseTestCaseFile reads and parses frontmatter from a test case file.
func parseTestCaseFile(path string) (*testCaseFrontmatter, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var tc testCaseFrontmatter
	_, err = frontmatter.Parse(f, &tc)
	if err != nil {
		return nil, fmt.Errorf("parsing frontmatter: %w", err)
	}
	tc.ID = strings.ToLower(tc.ID)
	return &tc, nil
}

// scanAutomationRecords reads all automation record files.
// Returns a map keyed by test case ID.
func scanAutomationRecords(projectRoot string) (map[string]automationFrontmatter, error) {
	recordDir := filepath.Join(projectRoot, "test-automation", "records")
	if _, err := os.Stat(recordDir); os.IsNotExist(err) {
		return nil, nil // no automation records directory is fine
	}

	results := make(map[string]automationFrontmatter)

	entries, err := os.ReadDir(recordDir)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".automation.md") {
			continue
		}

		path := filepath.Join(recordDir, entry.Name())
		ar, err := parseAutomationFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: skipping %s: %v\n", path, err)
			continue
		}
		if ar.TestCase != "" {
			results[ar.TestCase] = *ar
		}
	}

	return results, nil
}

// parseAutomationFile reads and parses frontmatter from an automation record.
func parseAutomationFile(path string) (*automationFrontmatter, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var ar automationFrontmatter
	_, err = frontmatter.Parse(f, &ar)
	if err != nil {
		return nil, fmt.Errorf("parsing frontmatter: %w", err)
	}
	return &ar, nil
}

// scanTasks reads all task files from active status directories (pending, in-progress).
func scanTasks(projectRoot string) ([]taskFrontmatter, error) {
	tasksDir := filepath.Join(projectRoot, "test-tasks")
	if _, err := os.Stat(tasksDir); os.IsNotExist(err) {
		return nil, nil
	}

	var results []taskFrontmatter

	// Scan all status subdirectories
	statusDirs := []string{"pending", "in-progress", "in-review", "complete", "failed"}
	for _, status := range statusDirs {
		dir := filepath.Join(tasksDir, status)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}

		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}

			path := filepath.Join(dir, entry.Name())
			task, err := parseTaskFile(path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: skipping %s: %v\n", path, err)
				continue
			}
			// Use the directory name as the canonical status
			task.Status = status
			results = append(results, *task)
		}
	}

	return results, nil
}

// parseTaskFile reads and parses frontmatter from a task file.
func parseTaskFile(path string) (*taskFrontmatter, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var task taskFrontmatter
	_, err = frontmatter.Parse(f, &task)
	if err != nil {
		return nil, fmt.Errorf("parsing frontmatter: %w", err)
	}
	return &task, nil
}

// deriveCreateStatus determines the CREATE stage status from a test case.
// If the test case file exists, creation is complete.
func deriveCreateStatus(tc testCaseFrontmatter) string {
	// The existence of a test case file means it was created
	return "complete"
}

// deriveAutomateStatus determines the AUTOMATE stage status from an automation record.
func deriveAutomateStatus(ar automationFrontmatter) string {
	switch ar.Status {
	case "accepted":
		return "complete"
	case "in-progress", "pending":
		return ar.Status
	default:
		if ar.Status != "" {
			return ar.Status
		}
		return "none"
	}
}

// deriveExecuteResult determines the last execution result from an automation record.
func deriveExecuteResult(ar automationFrontmatter) (result, date string) {
	if ar.LastFormalResult == "" {
		return "none", ""
	}
	return ar.LastFormalResult, "" // date would come from the result file if available
}

// applyTaskStatus checks active tasks and updates pipeline entry statuses.
// For execute tasks, it compares timestamps of failed vs completed tasks
// so the most recent execution state wins (BUG-018).
func applyTaskStatus(entry *PipelineEntry, tasks []taskFrontmatter) {
	// Track newest failed and completed execute task timestamps.
	// ISO 8601 strings are lexicographically sortable, so string comparison works.
	var newestFailedCreated string
	var newestCompleteCreated string

	for _, task := range tasks {
		if task.Target != entry.TestCaseID {
			continue
		}

		// Active tasks (pending/in-progress) affect all stage statuses
		if task.Status == "pending" || task.Status == "in-progress" {
			switch task.Type {
			case "create":
				if entry.CreateStatus == "none" || task.Status == "in-progress" {
					entry.CreateStatus = task.Status
				}
			case "automate":
				if entry.AutomateStatus == "none" || task.Status == "in-progress" {
					entry.AutomateStatus = task.Status
				}
			case "execute":
				if entry.ExecuteStatus == "none" || task.Status == "in-progress" {
					entry.ExecuteStatus = task.Status
				}
			}
			continue
		}

		// Track newest failed and completed execute tasks for timestamp comparison
		if task.Type == "execute" {
			if task.Status == "failed" && task.Created > newestFailedCreated {
				newestFailedCreated = task.Created
			}
			if task.Status == "complete" && task.Created > newestCompleteCreated {
				newestCompleteCreated = task.Created
			}
		}
	}

	// Apply execute status based on which is newer: failed or completed.
	// If a completed task is the same age or newer than the failed task,
	// the failure is stale and should not override.
	if newestFailedCreated != "" && newestFailedCreated > newestCompleteCreated {
		entry.ExecuteStatus = "failed"
	}
}
