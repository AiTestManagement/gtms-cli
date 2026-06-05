package reader

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/result"
)

// ResetResult describes what a reset operation did (or would do in dry-run mode).
type ResetResult struct {
	TestCasesAffected      int
	AutomationRecordsCleared int
	TaskFilesRemoved       int
}

// ResetExecuteResults clears execute results for test cases matching the given
// scope or single TC ID. When dryRun is true, it counts affected items without
// modifying any files.
//
// If tcID is non-empty, only that single test case is reset (scope is ignored).
// If tcID is empty, scope determines which test cases are reset.
func ResetExecuteResults(projectRoot string, scope *ScopeInfo, tcID string, dryRun bool) (*ResetResult, error) {
	result := &ResetResult{}

	if tcID != "" {
		return resetSingleTC(projectRoot, tcID, dryRun, result)
	}

	return resetByScope(projectRoot, scope, dryRun, result)
}

// resetSingleTC clears execute results for a single test case by ID.
func resetSingleTC(projectRoot, tcID string, dryRun bool, result *ResetResult) (*ResetResult, error) {
	cleared, err := clearAutomationRecords(projectRoot, tcID, dryRun)
	if err != nil {
		return nil, err
	}
	result.AutomationRecordsCleared = cleared

	removed, err := removeExecuteTaskFiles(projectRoot, tcID, dryRun)
	if err != nil {
		return nil, err
	}
	result.TaskFilesRemoved = removed

	if cleared > 0 || removed > 0 {
		result.TestCasesAffected = 1
	}

	return result, nil
}

// resetByScope clears execute results for all test cases within the given scope.
func resetByScope(projectRoot string, scope *ScopeInfo, dryRun bool, result *ResetResult) (*ResetResult, error) {
	testCases, err := scanTestCases(projectRoot, scope)
	if err != nil {
		return nil, fmt.Errorf("scanning test cases: %w", err)
	}

	for _, tc := range testCases {
		cleared, err := clearAutomationRecords(projectRoot, tc.ID, dryRun)
		if err != nil {
			return nil, err
		}
		result.AutomationRecordsCleared += cleared

		removed, err := removeExecuteTaskFiles(projectRoot, tc.ID, dryRun)
		if err != nil {
			return nil, err
		}
		result.TaskFilesRemoved += removed

		if cleared > 0 || removed > 0 {
			result.TestCasesAffected++
		}
	}

	return result, nil
}

// clearAutomationRecords removes the terminal result handoffs under
// .gtms/results/ that belong to a given TC. CON-023 / ENH-146: wiring
// is read-only — reset must not mutate wiring. The execute outcome
// lives on the result contract, so clearing it is the right semantics
// for "reset this TC's last run." Counts handoffs removed (or that
// would be removed in dry-run mode).
func clearAutomationRecords(projectRoot, tcID string, dryRun bool) (int, error) {
	dir := filepath.Join(projectRoot, ".gtms", "results")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("reading .gtms/results for %s: %w", tcID, err)
	}

	cleared := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".handoff.yaml") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		rc, err := readContractTarget(path)
		if err != nil || rc != tcID {
			continue
		}
		if dryRun {
			cleared++
			continue
		}
		if err := os.Remove(path); err != nil {
			return cleared, fmt.Errorf("removing handoff %s: %w", path, err)
		}
		cleared++
	}
	return cleared, nil
}

// readContractTarget peeks at a handoff file and returns its target TC.
// Best-effort: parse errors return ("", err).
func readContractTarget(path string) (string, error) {
	rc, err := result.Read(path)
	if err != nil || rc == nil {
		return "", err
	}
	return rc.Target, nil
}

// removeExecuteTaskFiles removes completed and error execute task files for the
// given test case ID. Returns the count of files removed (or that would be removed
// in dry-run mode).
func removeExecuteTaskFiles(projectRoot, tcID string, dryRun bool) (int, error) {
	count := 0
	suffix := fmt.Sprintf("-execute-%s.md", tcID)

	for _, statusDir := range []string{"complete", "error"} {
		dir := filepath.Join(layout.TasksDir(projectRoot), statusDir)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}

		entries, err := os.ReadDir(dir)
		if err != nil {
			return count, fmt.Errorf("reading %s: %w", dir, err)
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			if strings.HasSuffix(entry.Name(), suffix) {
				count++
				if !dryRun {
					path := filepath.Join(dir, entry.Name())
					if err := os.Remove(path); err != nil {
						return count, fmt.Errorf("removing task file %s: %w", path, err)
					}
				}
			}
		}
	}

	return count, nil
}
