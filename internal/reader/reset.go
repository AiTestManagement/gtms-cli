package reader

import (
	"errors"
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

	// BUG-209: a refused .gtms/results entry (an unsafe leaf) is left untouched
	// and named; a refused results directory (an escaping link) stops all handoff
	// cleanup. Either makes the cleanup incomplete, so the CLI names it and exits
	// non-zero while completing every unrelated safe removal.
	HandoffRefusals []*result.RefusedHandoffError
	DirRefusal      *result.RefusedDirError
}

// Incomplete reports whether a BUG-209 refusal left the reset cleanup incomplete.
func (r *ResetResult) Incomplete() bool {
	return len(r.HandoffRefusals) > 0 || r.DirRefusal != nil
}

// ResetExecuteResults clears execute results for test cases matching the given
// scope or single TC ID. When dryRun is true, it counts affected items without
// modifying any files.
//
// If tcID is non-empty, only that single test case is reset (scope is ignored).
// If tcID is empty, scope determines which test cases are reset.
func ResetExecuteResults(projectRoot string, scope *ScopeInfo, tcID string, dryRun bool) (*ResetResult, error) {
	res := &ResetResult{}

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
		r := snap.Resolve(id)
		if r.Outcome == testcase.OutcomeAmbiguous || r.Outcome == testcase.OutcomeUnprovable {
			return nil, testcase.OwnershipErrorFor(r)
		}
	}

	// BUG-209: open the shared results-directory scan ONCE. An escaping results
	// directory stops all handoff cleanup (never followed or removed through); each
	// unsafe leaf is recorded and left untouched. Task-file cleanup (under
	// gtms/tasks, not .gtms/results) proceeds regardless, so the safe part of the
	// reset still completes.
	scan := result.OpenScan(projectRoot)
	defer scan.Close()
	res.DirRefusal = scan.DirRefusal
	for _, ref := range scan.Refusals {
		res.HandoffRefusals = append(res.HandoffRefusals, ref.Err)
	}

	if tcID != "" {
		return resetSingleTC(projectRoot, scan, tcID, dryRun, res)
	}
	return resetSelection(projectRoot, scan, selection, dryRun, res)
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
func resetSingleTC(projectRoot string, scan *result.Scan, tcID string, dryRun bool, res *ResetResult) (*ResetResult, error) {
	cleared, err := clearAutomationRecords(scan, tcID, dryRun, res)
	if err != nil {
		return nil, err
	}
	res.AutomationRecordsCleared = cleared

	removed, err := removeExecuteTaskFiles(projectRoot, tcID, dryRun)
	if err != nil {
		return nil, err
	}
	res.TaskFilesRemoved = removed

	if cleared > 0 || removed > 0 {
		res.TestCasesAffected = 1
	}

	return res, nil
}

// resetSelection clears execute results for the snapshot-selected owner ids.
// Selection and the ownership preflight already ran in ResetExecuteResults, so
// every id here is a proven unique (or not-found) owner.
func resetSelection(projectRoot string, scan *result.Scan, selection []string, dryRun bool, res *ResetResult) (*ResetResult, error) {
	for _, id := range selection {
		cleared, err := clearAutomationRecords(scan, id, dryRun, res)
		if err != nil {
			return nil, err
		}
		res.AutomationRecordsCleared += cleared

		removed, err := removeExecuteTaskFiles(projectRoot, id, dryRun)
		if err != nil {
			return nil, err
		}
		res.TaskFilesRemoved += removed

		if cleared > 0 || removed > 0 {
			res.TestCasesAffected++
		}
	}

	return res, nil
}

// clearAutomationRecords clears all GTMS handoff history (create, automate,
// execute, prime) for the given TC under .gtms/results/, through the shared
// BUG-209 scanner: it removes each safe handoff whose target matches tcID AND its
// leftover staged files, never follows or removes through an unsafe leaf or an
// escaping results directory (those were recorded as refusals in
// ResetExecuteResults), and leaves wiring untouched (CON-023 / ENH-146). Counts
// handoffs removed (or that would be removed in dry-run mode).
func clearAutomationRecords(scan *result.Scan, tcID string, dryRun bool, res *ResetResult) (int, error) {
	if scan.Absent || scan.DirRefusal != nil {
		return 0, nil
	}
	cleared := 0
	for _, e := range scan.Entries {
		if e.RC == nil || e.RC.Target != tcID {
			continue
		}
		if dryRun {
			// A preview names what a real run would leave (AC35).
			_, refused := scan.LeftoversFor(e.Base)
			res.addHandoffRefusals(refused)
			cleared++
			continue
		}
		refused, err := scan.Remove(e.Base)
		res.addHandoffRefusals(refused)
		if err != nil {
			// BUG-209 section 8: an entry that became unsafe after the scan read it is
			// left untouched and reported like any other refused entry, which makes the
			// exit non-zero, and the rest of the cleanup continues. delete does the same.
			var rh *result.RefusedHandoffError
			var rd *result.RefusedDirError
			switch {
			case errors.As(err, &rh):
				res.addHandoffRefusals([]*result.RefusedHandoffError{rh})
				continue
			case errors.As(err, &rd):
				if res.DirRefusal == nil {
					res.DirRefusal = rd
				}
				return cleared, nil
			}
			return cleared, fmt.Errorf("removing handoff %s: %w", e.Base, err)
		}
		cleared++
	}
	return cleared, nil
}

// addHandoffRefusals records refusals once per path.
func (r *ResetResult) addHandoffRefusals(refs []*result.RefusedHandoffError) {
	for _, ref := range refs {
		seen := false
		for _, have := range r.HandoffRefusals {
			if have.Rel == ref.Rel {
				seen = true
				break
			}
		}
		if !seen {
			r.HandoffRefusals = append(r.HandoffRefusals, ref)
		}
	}
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
