package reader

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
)

// ResetResult describes what a reset operation did (or would do in dry-run mode).
type ResetResult struct {
	TestCasesAffected        int
	AutomationRecordsCleared int
	TaskFilesRemoved         int
}

// ResetExecuteResults clears execute results for test cases matching the given
// scope or single TC ID. When dryRun is true, it counts affected items without
// modifying any files.
//
// If tcID is non-empty, only that single test case is reset (scope is ignored).
// If tcID is empty, scope determines which test cases are reset.
func ResetExecuteResults(projectRoot string, scope *ScopeInfo, tcID string, dryRun bool) (*ResetResult, error) {
	result := &ResetResult{}

	// ADR-024: reset is destructive by EFFECT (it removes handoffs and task
	// files), so it requires proven UniqueOwner ownership before any mutation.
	// One project-wide snapshot backs the preflight; Ambiguous or Unprovable
	// ownership for any selected id refuses the WHOLE reset (all-or-nothing),
	// visibly, before a single file is removed. A NotFound id proceeds so orphaned
	// execute state keyed by exact id can still be cleared.
	projectRoot = pathsafe.CanonicalRoot(projectRoot)
	snap, err := testcase.NewSnapshot(projectRoot)
	if err != nil {
		return nil, err
	}

	var selection []string
	if tcID != "" {
		// ADR-024 s3 / REV-158 CLAUDE-001: a folder-qualified single-TC reset
		// requires MUTATION AUTHORITY under that folder, not mere reachability --
		// reset clears state attributed to the owner, so a spec symlink must not
		// make an out-of-scope test case resettable through it.
		if scope != nil {
			if err := snap.RequireMutationAuthorityUnderScope(scopeRelFor(projectRoot, scope), tcID); err != nil {
				return nil, err
			}
		}
		selection = []string{tcID}
	} else {
		scopeRel := deriveResetScopeRel(projectRoot, scope)
		// REV-160 CLAUDE-002 / CODEX-003: a nil scope means "the whole cases
		// tree", so it must select RECURSIVELY. deriveResetScopeRel already
		// returns the cases root for nil, but pairing that root with a shallow
		// scan selected only specs sitting directly under gtms/test/cases/ and
		// reported success, silently skipping every nested spec. At b524c9b5 the
		// same input went through scanTestCases(projectRoot, nil), which walked
		// the whole tree. ResetExecuteResults is exported, so an out-of-CLI
		// caller that used to clear the whole project would have been told it
		// succeeded after clearing only the root level.
		ids, inScopeUnprovable := snap.SelectFolder(scopeRel, scope == nil || scope.Recursive)
		if len(inScopeUnprovable) > 0 {
			return nil, testcase.UnprovableInScope(scopeRel, inScopeUnprovable[0])
		}
		selection = ids
	}
	for _, id := range selection {
		res := snap.Resolve(id)
		if res.Outcome == testcase.OutcomeAmbiguous || res.Outcome == testcase.OutcomeUnprovable {
			return nil, testcase.OwnershipErrorFor(res)
		}
	}

	if tcID != "" {
		return resetSingleTC(projectRoot, tcID, dryRun, result)
	}
	return resetSelection(projectRoot, selection, dryRun, result)
}

// deriveResetScopeRel returns the canonical project-relative scope directory for
// a reset scope (the whole cases tree when scope is nil).
func deriveResetScopeRel(projectRoot string, scope *ScopeInfo) string {
	if scope == nil {
		return filepath.ToSlash(layout.Current().TestCases)
	}
	return scopeRelFor(projectRoot, scope)
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

// resetSelection clears execute results for the snapshot-selected owner ids.
// Selection and the ownership preflight already ran in ResetExecuteResults, so
// every id here is a proven unique (or not-found) owner.
func resetSelection(projectRoot string, selection []string, dryRun bool, result *ResetResult) (*ResetResult, error) {
	for _, id := range selection {
		cleared, err := clearAutomationRecords(projectRoot, id, dryRun)
		if err != nil {
			return nil, err
		}
		result.AutomationRecordsCleared += cleared

		removed, err := removeExecuteTaskFiles(projectRoot, id, dryRun)
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

// clearAutomationRecords clears all GTMS handoff history (create,
// automate, execute, prime) for the given TC under .gtms/results/.
// CON-023 / ENH-146: wiring is read-only -- reset must not mutate
// wiring. The full clear is intentional: reset purges all stage
// results so the TC can be re-run from scratch. Counts handoffs
// removed (or that would be removed in dry-run mode).
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
