package reader

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/adrg/frontmatter"
	"gopkg.in/yaml.v3"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// PipelineStatus returns the pipeline status of test cases in the project.
// When scope is nil, it scans all test cases (backward compatible).
// When scope is non-nil, it scans only test cases within the scope directory.
// defaultFramework selects which wiring record to display when a TC has
// multiple. strictFramework (ENH-082) honours an explicit --framework
// filter: when true and defaultFramework is non-empty and pickWiring
// finds no wiring record for that framework, the entry has no selected
// framework — SelectedFramework is empty (JSON-encoded as null) and the
// legacy Framework / AutomateStatus / ExecuteStatus / LastResult carriers
// remain at their "none"/"" defaults. Wired remains true whenever any
// wiring record exists for the TC, regardless of strict-framework
// selection: "wired" means "at least one wiring record exists for this
// TC", not "the requested framework is wired".
// CON-023 / ENH-146: identity comes from wiring (gtms/automation/wiring/);
// runtime overlay from terminal handoffs (.gtms/results/, status: complete
// or status: error only); manual-ready from gtms/manual/records/.
func PipelineStatus(projectRoot string, scope *ScopeInfo, defaultFramework string, strictFramework bool) ([]PipelineEntry, error) {
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

	entries := make([]PipelineEntry, 0, len(testCases))
	for _, tc := range testCases {
		entry := buildPipelineEntry(projectRoot, tc, wiringByTC[tc.ID], overlay, manuals[tc.ID], defaultFramework, strictFramework)
		applyTaskStatus(&entry, tasks)
		entries = append(entries, entry)
	}

	// Sort by test case ID for deterministic output
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].TestCaseID < entries[j].TestCaseID
	})

	return entries, nil
}

// PipelineFolderSummary returns aggregate pipeline counts per folder.
// It scans all test cases recursively and groups by the immediate subfolder under gtms/test/cases/.
//
// ENH-089: also populates Passing/Failing/Errored/InFlight for the icon-forward
// renderer. InFlight is sourced from active execute task files (gtms/tasks/in-progress/);
// outcome counts are sourced from automation-record result (was last-formal-result). The framework
// filter (ENH-075) continues to scope which automation records contribute outcome counts;
// in-flight tasks have no framework field so they count for any --framework value.
//
// TODO(CON-023 / ENH-146): PipelineFolderSummary still routes through the
// transitional automationFrontmatter carrier (scanAutomationRecords +
// selectAutomationRecord). It is the last reader that has not been
// retargeted onto the wiring-aware picker (buildPipelineEntry +
// pickWiring). The carrier synthesises Status="developed" and Cycle=0 to
// keep this path compiling, which means: (a) folder-summary results
// remain deterministic per commit, (b) but folder-summary counts use the
// transitional carrier semantics, not the ENH-146 picker semantics. When
// folder-summary is converted to consume PipelineEntry / Frameworks[]
// directly, scanAutomationRecords and selectAutomationRecord can be
// retired alongside the automationFrontmatter struct fields marked with
// the related retirement TODO in types.go.
func PipelineFolderSummary(projectRoot string, defaultFramework string) ([]FolderSummaryEntry, error) {
	testCases, err := scanTestCases(projectRoot, nil)
	if err != nil {
		return nil, fmt.Errorf("scanning test cases: %w", err)
	}

	autoRecords, err := scanAutomationRecords(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("scanning automation records: %w", err)
	}

	// ENH-089: load active tasks once and build a TC-id → in-flight set so
	// the per-TC loop is O(1) per check rather than O(tasks).
	tasks, err := scanTasks(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("scanning tasks: %w", err)
	}
	inFlightTCs := make(map[string]bool)
	for _, t := range tasks {
		if t.Type == "execute" && (t.Status == "in-progress" || t.Status == "pending") {
			inFlightTCs[t.Target] = true
		}
	}

	tcDir := layout.TestCasesDir(projectRoot)

	// Group test cases by folder
	type folderAccum struct {
		created   int
		drafts    int
		automated int
		executed  int
		// ENH-089: outcome counts (drive icon selection in the renderer)
		passing  int
		failing  int
		errored  int
		skipped  int
		inFlight int
		// BUG-043: TCs with records under other frameworks but not the requested one.
		frameworkMismatch int
		// ENH-134: manual-coverage sub-state counts.
		manualPrepared int
		manualRecorded int
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
		if strings.EqualFold(tc.Status, "draft") {
			acc.drafts++
		}

		// ENH-089: in-flight counts increment BEFORE the framework-filter
		// `continue` below so a TC with an active execute task is visible
		// even if its selected automation record doesn't match --framework.
		if inFlightTCs[tc.ID] {
			acc.inFlight++
		}

		// ENH-134: manual-coverage sub-state counts derived from ALL records,
		// before framework filtering.
		if records, ok := autoRecords[tc.ID]; ok {
			mc := deriveManualCoverage(records)
			switch mc {
			case "prepared":
				acc.manualPrepared++
			case "recorded":
				acc.manualRecorded++
			}
		}

		if records, ok := autoRecords[tc.ID]; ok {
			// PipelineFolderSummary already gates on framework match below
			// (ENH-075), so non-strict selection is sufficient here. Strict
			// mode (ENH-082) applies to per-TC views, not folder aggregation.
			ar := selectAutomationRecord(records, defaultFramework, false)
			// When a framework filter is active, skip TCs that don't have
			// a matching record for that framework (ENH-075).
			if defaultFramework != "" && ar.Framework != defaultFramework {
				// BUG-043: this TC has records under other frameworks but not the
				// requested one — count as framework mismatch so --json consumers
				// can distinguish from "not automated at all".
				acc.frameworkMismatch++
				continue
			}
			autoStatus := deriveAutomateStatus(ar)
			if autoStatus == "complete" || autoStatus == "developed" {
				acc.automated++
			}
			result, _ := deriveExecuteResult(ar)
			if result != "" && result != "none" {
				acc.executed++
			}
			// ENH-089/094: outcome breakdown — drives ✓ / ○ / ● / ✗ / ⊘ in EXECUTE column.
			switch result {
			case "pass":
				acc.passing++
			case "fail":
				acc.failing++
			case "error":
				acc.errored++
			case "skipped":
				acc.skipped++
			}
		}
	}

	// Convert to sorted slice
	entries := make([]FolderSummaryEntry, 0, len(folders))
	for name, acc := range folders {
		entries = append(entries, FolderSummaryEntry{
			Folder:            name,
			Created:           acc.created,
			DraftCount:        acc.drafts,
			Automated:         acc.automated,
			Executed:          acc.executed,
			Passing:           acc.passing,
			Failing:           acc.failing,
			Errored:           acc.errored,
			Skipped:           acc.skipped,
			InFlight:          acc.inFlight,
			FrameworkMismatch: acc.frameworkMismatch,
			ManualPrepared:    acc.manualPrepared,
			ManualRecorded:    acc.manualRecorded,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Folder < entries[j].Folder
	})

	return entries, nil
}

// deriveFolderName extracts the immediate subfolder name under gtms/test/cases/.
// Returns "(root)" for test cases directly in gtms/test/cases/ with no subfolder.
func deriveFolderName(tcDir, sourceFile string) string {
	dir := filepath.Dir(sourceFile)
	rel, err := filepath.Rel(tcDir, dir)
	if err != nil || rel == "." {
		return "(root)"
	}
	// Use the first path component only
	parts := strings.SplitN(filepath.ToSlash(rel), "/", 2)
	if len(parts) > 0 && parts[0] != "" {
		return parts[0]
	}
	return "(root)"
}

// PipelineDetail returns detailed pipeline information for a single test case.
// It always performs an unscoped (global) scan — Convention 4: ID-based lookups are global.
// defaultFramework selects which wiring record to display when a TC has multiple.
// strictFramework (ENH-082) honours an explicit --framework flag: when true and
// defaultFramework is non-empty, a TC without a matching wiring record yields
// an empty selection, so the detail view shows em-dashes for AUTOMATE/EXECUTE
// instead of pretending another framework's record applies.
//
// CON-023 / ENH-146: detail builds its per-TC view through the same
// wiring-aware path as PipelineStatus (buildPipelineEntry → frameworks[]) so
// the --json shape is the per-TC ENH-146 contract with the selected
// framework's overlay fields surfaced inside frameworks[].
func PipelineDetail(projectRoot, testCaseID, defaultFramework string, strictFramework bool) (*PipelineDetailEntry, error) {
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

	pe := buildPipelineEntry(projectRoot, *tc, wiringByTC[tc.ID], overlay, manuals[tc.ID], defaultFramework, strictFramework)
	applyTaskStatus(&pe, tasks)

	detail := &PipelineDetailEntry{
		TestCaseID:        pe.TestCaseID,
		Slug:              pe.Slug,
		Title:              pe.Title,
		Requirement:       tc.Requirement,
		CreateStatus:      pe.CreateStatus,
		Wired:             pe.Wired,
		ManualReady:       pe.ManualReady,
		SelectedFramework: pe.SelectedFramework,
		Frameworks:        pe.Frameworks,

		Tags:       tc.Tags,
		CreateDate: tc.Created,

		// Legacy carriers driving the CLI text renderer (json:"-").
		AutomateStatus:      pe.AutomateStatus,
		ExecuteStatus:       pe.ExecuteStatus,
		LastResult:          pe.LastResult,
		LastResultDate:      pe.LastResultDate,
		Framework:           pe.Framework,
		LastRunAt:           pe.LastRunAt,
		Stale:               pe.Stale,
		StaleTestCaseHash:   pe.StaleTestCaseHash,
		ManualCoverage:      pe.ManualCoverage,
		AvailableFrameworks: pe.AvailableFrameworks,
		DriftDetected:       pe.DriftDetected,
		DriftDetectedAt:     pe.DriftDetectedAt,
		TestCaseHashAtExecute: pe.TestCaseHashAtExecute,
	}
	// Default to "none" for the CLI label vocabulary when nothing has been
	// populated by the wiring/overlay path (preserves the pre-cutover detail
	// renderer behaviour: em-dash for unwired TCs).
	if detail.AutomateStatus == "" {
		detail.AutomateStatus = "none"
	}
	if detail.ExecuteStatus == "" {
		detail.ExecuteStatus = "none"
	}
	if detail.LastResult == "" {
		detail.LastResult = "none"
	}

	// File-path carriers for the CLI renderer ("Automation:" / "Last run:")
	// and the notes payload come from the SELECTED framework's overlay /
	// wiring record. These stay flat at the detail top-level because the
	// text renderer reads them directly. They are json:"-" except for
	// Notes / NotesSpill, which the user-task pins as preserved on the
	// detail surface (ENH-077 / ENH-123 contract).
	if pe.SelectedFramework != "" {
		for _, fe := range pe.Frameworks {
			if fe.Framework == pe.SelectedFramework {
				detail.ArtefactPath = fe.Artefact
				detail.Notes = fe.LogExcerpt
				break
			}
		}
		if hit, ok := overlay[overlayKey(tc.ID, pe.SelectedFramework)]; ok {
			detail.LastRunPath = hit.Artefact
			detail.NotesSpill = hit.NotesSpill
		}
		// AutomateDate is the local mtime of the selected wiring file.
		detail.AutomateDate = automationRecordMtime(projectRoot, tc.ID, pe.SelectedFramework)
	}

	return detail, nil
}

// scanTestCases reads test case markdown files.
// When scope is nil, it recursively scans all of gtms/test/cases/ (backward compatible).
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

// scanTestCasesAll recursively scans all test case files under gtms/test/cases/.
func scanTestCasesAll(projectRoot string) ([]testCaseFrontmatter, error) {
	tcDir := layout.TestCasesDir(projectRoot)
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

// scanAutomationRecords reads the canonical automation identity from
// tracked wiring records (CON-023 / ENH-145) and overlays the latest
// matching terminal result contract from .gtms/results/. Manual-only
// TCs (no wiring) surface as framework="manual" rows sourced from
// gtms/manual/records/.
//
// The returned shape is automationFrontmatter — a transitional carrier
// kept while internal/reader/gaps.go and internal/reader/map.go are
// refactored onto the new picker. No production read path touches the
// legacy gtms/automation/records/*.automation.md directory.
//
// Synthesised fields:
//   - Status = "developed"   (wiring has no lifecycle — synthetic stamp)
//   - Cycle  = 0             (cycle is retired)
func scanAutomationRecords(projectRoot string) (map[string][]automationFrontmatter, error) {
	recs, err := wiringScan(projectRoot)
	if err != nil {
		return nil, err
	}
	overlay := scanTerminalResults(projectRoot, recs)
	manuals := scanManualByTC(projectRoot)

	out := make(map[string][]automationFrontmatter)
	for tcID, wRecs := range recs {
		for _, w := range wRecs {
			af := automationFrontmatter{
				TestCase:     w.TestCase,
				Framework:    w.Framework,
				Adapter:      w.Adapter,
				Artefact:     w.Artefact,
				ArtefactHash: w.ArtefactHash,
				TestCaseHash: w.TestCaseHash,
				Status:       "developed",
			}
			if hit, ok := overlay[overlayKey(w.TestCase, w.Framework)]; ok {
				af.Result = hit.Result
				// Adapter-failure terminal handoffs (ENH-130: status: error,
				// no result) lose the error signal in the transitional carrier
				// unless we synthesise it here, because downstream gap
				// classifiers key off af.Result. CON-023 / ENH-146 keeps the
				// orthogonal status/result split on the result contract itself;
				// this synthesis is local to the legacy carrier only.
				if af.Result == "" && hit.Status == "error" {
					af.Result = "error"
				}
				af.ExecutedAt = hit.ExecutedAt
				af.ExecutedBy = hit.ExecutedBy
				af.Environment = hit.Environment
				af.Notes = hit.Notes
				af.NotesSpill = hit.NotesSpill
				af.ExecutedArtefact = hit.Artefact
				af.Attempts = hit.Attempts
				af.Summary = hit.Summary
			}
			out[tcID] = append(out[tcID], af)
		}
	}

	// CON-023 / Edge Case 1: manual-only TCs surface via the manual
	// result file. They have no wiring record but the gaps/map code
	// paths still need them visible. Add a framework="manual" row.
	for tcID, mr := range manuals {
		af := automationFrontmatter{
			TestCase:  tcID,
			Framework: "manual",
			Status:    "developed",
			Artefact:  mr.ArtefactPath,
			Result:    mr.Result,
		}
		out[tcID] = append(out[tcID], af)
	}

	return out, nil
}

// wiringScanFn is a package-level seam so test fixtures can stub the
// wiring scan without touching the filesystem. Defaults to the real
// implementation; tests that want to inject can swap it.
var wiringScanFn = defaultWiringScan

// selectAutomationRecord picks one automation record from a slice for display.
// Selection rule (in priority order):
//  1. If no records -> return empty.
//  2. If strictFramework && defaultFramework != "" (ENH-082):
//     - Return the first record matching defaultFramework, or empty if no match.
//     - Bypasses the single-record short-circuit so a single non-matching
//       record still yields empty (caller wants strict per-TC filtering).
//  3. If only one record -> use it (no framework check).
//  4. If defaultFramework matches a record -> use it.
//  5. Fallback: pick the record with the highest cycle count, with manual
//     deprioritised on ties.
//
// strictFramework should be true only when the caller wants an explicit
// --framework filter to suppress fallback (per-TC views in status / gaps /
// map). It should be false when the caller just wants a sensible default
// pick (config-default fallback, or folder-summary aggregation that has its
// own framework gating).
//
// TODO(CON-023 / ENH-146): selectAutomationRecord is the picker for the
// transitional automationFrontmatter carrier — it still consults the
// retired Cycle field on step 5. Its only remaining caller is
// PipelineFolderSummary; PipelineStatus, PipelineDetail, Map, and Triage
// route through pickWiring (internal/reader/picker.go) instead. Retire
// this function (and the synthesised Cycle/Status fields it reads) when
// folder-summary is migrated onto the wiring-aware picker.
func selectAutomationRecord(records []automationFrontmatter, defaultFramework string, strictFramework bool) automationFrontmatter {
	if len(records) == 0 {
		return automationFrontmatter{}
	}

	// Strict mode (ENH-082): explicit --framework filter rules out fallback.
	// Must come BEFORE the single-record short-circuit so that one record
	// with the wrong framework still yields empty.
	if strictFramework && defaultFramework != "" {
		for _, r := range records {
			if r.Framework == defaultFramework {
				return r
			}
		}
		return automationFrontmatter{}
	}

	if len(records) == 1 {
		return records[0]
	}

	// Try to match defaultFramework
	if defaultFramework != "" {
		for _, r := range records {
			if r.Framework == defaultFramework {
				return r
			}
		}
	}

	// Fallback: highest cycle count, with manual deprioritised on ties.
	best := records[0]
	for _, r := range records[1:] {
		if r.Cycle > best.Cycle {
			best = r
		} else if r.Cycle == best.Cycle && best.Framework == "manual" && r.Framework != "manual" {
			// Prefer non-manual over manual when cycles are tied
			best = r
		}
	}
	return best
}

// availableFrameworks returns a sorted, deduplicated list of framework names
// from the given automation records. Returns nil (not empty slice) when no
// records exist or none carry a framework name, so JSON omitempty correctly
// omits the field for TCs with no automation at all (BUG-043).
func availableFrameworks(records []automationFrontmatter) []string {
	if len(records) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(records))
	var fws []string
	for _, r := range records {
		if r.Framework != "" && !seen[r.Framework] {
			seen[r.Framework] = true
			fws = append(fws, r.Framework)
		}
	}
	sort.Strings(fws)
	if len(fws) == 0 {
		return nil
	}
	return fws
}

// scanTasks reads all task files from active status directories (pending, in-progress).
func scanTasks(projectRoot string) ([]taskFrontmatter, error) {
	tasksDir := layout.TasksDir(projectRoot)
	if _, err := os.Stat(tasksDir); os.IsNotExist(err) {
		return nil, nil
	}

	var results []taskFrontmatter

	// Scan all status subdirectories
	statusDirs := []string{"pending", "in-progress", "in-review", "complete", "error"}
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

// deriveManualCoverage classifies the manual-coverage sub-state from ALL
// automation records for a test case (not just the selected one).
// Returns "" if no manual record exists, "prepared" if a manual record
// exists with an empty Result (prime ran, execute hasn't), or "recorded"
// if a manual record exists with a non-empty Result (post-execute coverage).
//
// ENH-134: this is additive — it does not alter the NoAutomation classification
// (hasNonManualRecord semantics unchanged). A TC in NoAutomation can carry
// ManualCoverage = "prepared" or "recorded" to disambiguate from no-coverage.
func deriveManualCoverage(records []automationFrontmatter) string {
	for _, r := range records {
		if r.Framework == "manual" {
			if r.Result == "" {
				return "prepared"
			}
			return "recorded"
		}
	}
	return ""
}

// manualDriftDiagnostics holds the drift fields extracted from a manual result file.
type manualDriftDiagnostics struct {
	DriftDetected         bool
	DriftDetectedAt       string
	TestCaseHashAtExecute string
}

// manualResultDriftFields mirrors the three drift diagnostic fields in the
// manual result YAML. The field names use the exact casing written by the
// manual-execute adapter script (kebab-case for the first two, snake_case for
// the third — matching driftFieldRe in internal/cli/prime.go:342).
type manualResultDriftFields struct {
	DriftDetected         bool   `yaml:"drift-detected"`
	DriftDetectedAt       string `yaml:"drift-detected-at"`
	TestCaseHashAtExecute string `yaml:"test_case_hash_at_execute"`
}

// readManualDriftDiagnostics opens the manual result file for the given
// automation record and extracts the drift diagnostic fields.
//
// Returns the zero value when:
//   - ar.Framework is not "manual"
//   - ar.Artefact is empty
//   - the file does not exist or cannot be read
//   - the file does not contain drift-detected: true
//
// BUG-079: this is the hook between the adapter-written diagnostics and the
// reader pipeline. It is called after each selectAutomationRecord() call,
// gated on the selected record's framework.
func readManualDriftDiagnostics(projectRoot string, ar automationFrontmatter) manualDriftDiagnostics {
	if ar.Framework != "manual" || ar.Artefact == "" {
		return manualDriftDiagnostics{}
	}

	absPath := filepath.Join(projectRoot, filepath.FromSlash(ar.Artefact))
	data, err := os.ReadFile(absPath)
	if err != nil {
		return manualDriftDiagnostics{}
	}

	var fields manualResultDriftFields
	if err := yaml.Unmarshal(data, &fields); err != nil {
		return manualDriftDiagnostics{}
	}

	if !fields.DriftDetected {
		return manualDriftDiagnostics{}
	}

	return manualDriftDiagnostics{
		DriftDetected:         true,
		DriftDetectedAt:       fields.DriftDetectedAt,
		TestCaseHashAtExecute: fields.TestCaseHashAtExecute,
	}
}

// deriveCreateStatus determines the CREATE stage status from a test case.
// If the test case file exists, creation is complete.
func deriveCreateStatus(tc testCaseFrontmatter) string {
	// The existence of a test case file means it was created
	return "complete"
}

// deriveAutomateStatus determines the AUTOMATE stage status from an automation record.
// Manual framework records return "manual" regardless of their status value,
// so the AUTOMATE column honestly shows "manual" instead of a checkmark.
//
// CON-023 / ENH-145: wiring records carry no lifecycle. scanAutomationRecords
// synthesises Status = "developed" on every wiring-backed row to keep the
// legacy carrier alive for downstream consumers. Under the new contract
// "developed" and "accepted" both mean "wiring exists for this TC × framework"
// — i.e. complete — so the two values collapse here.
func deriveAutomateStatus(ar automationFrontmatter) string {
	if ar.Framework == "manual" {
		return "manual"
	}
	switch ar.Status {
	case "accepted", "developed":
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
	if ar.Result == "" {
		return "none", ""
	}
	return ar.Result, "" // date would come from the result file if available
}

// applyTaskStatus checks active tasks and updates pipeline entry statuses.
// For execute tasks, it compares timestamps of error vs completed tasks
// so the most recent execution state wins (BUG-018).
func applyTaskStatus(entry *PipelineEntry, tasks []taskFrontmatter) {
	// Track newest error and completed execute task timestamps.
	// ISO 8601 strings are lexicographically sortable, so string comparison works.
	var newestErrorCreated string
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

		// Track newest error and completed execute tasks for timestamp comparison
		if task.Type == "execute" {
			if task.Status == "error" && task.Created > newestErrorCreated {
				newestErrorCreated = task.Created
			}
			if task.Status == "complete" && task.Created > newestCompleteCreated {
				newestCompleteCreated = task.Created
			}
		}
	}

	// Apply execute status based on which is newer: error or completed.
	// If a completed task is the same age or newer than the error task,
	// the error is stale and should not override.
	//
	// BUG-044: also compare against the automation record's executed_at (was last-formal-run-at).
	// When the record carries a pass or skipped result whose run-at timestamp
	// is at least as recent as the error task, the error task is an
	// infrastructure artefact (e.g. adapter cancelled) and must not override
	// the authoritative record. The allow-list is strict: only pass/skipped
	// records supersede. If the record's own result is fail or error, the
	// error task is consistent with the record and the override proceeds —
	// otherwise the detail-view label renders "Fail"/"Error" instead of the
	// "Error" that formatExecuteLabel emits when ExecuteStatus=="error".
	if newestErrorCreated != "" && newestErrorCreated > newestCompleteCreated {
		recordSupersedes := entry.LastRunAt != "" &&
			entry.LastRunAt >= newestErrorCreated &&
			(entry.LastResult == "pass" || entry.LastResult == "skipped")
		if !recordSupersedes {
			entry.ExecuteStatus = "error"
		}
	}
}

// isStaleTestCaseHash checks if the test case spec has been modified since the
// automation record was written. Returns false if no hash stored (legacy records)
// or if the spec cannot be resolved/hashed (graceful degradation).
//
// ENH-117: mirrors isStaleArtefact but compares the spec hash, not the artefact hash.
func isStaleTestCaseHash(projectRoot string, ar automationFrontmatter) bool {
	if ar.TestCaseHash == "" {
		return false
	}
	specPath, err := pipeline.ResolveTestCaseSpec(projectRoot, ar.TestCase)
	if err != nil {
		return false // spec missing — surfaces in gaps as a different issue
	}
	currentHash, err := pipeline.HashFile(pipeline.AbsArtefactPath(projectRoot, specPath))
	if err != nil {
		return false
	}
	return currentHash != ar.TestCaseHash
}

// isStaleArtefact checks if the wiring-bound artefact has been modified
// since wiring was written. CON-023 / ENH-145: read artefact directly
// from ar.Artefact (the wiring path) — the legacy ResolveArtefact glob
// fallback is retired.
func isStaleArtefact(projectRoot string, ar automationFrontmatter) bool {
	if ar.ArtefactHash == "" || ar.Artefact == "" {
		return false
	}
	// ENH-151: pending artefact-hash is never stale — pre-bootstrap drift
	// is not meaningful. The sentinel means "awaiting first execute."
	if wiring.IsPendingArtefactHash(ar.ArtefactHash) {
		return false
	}
	currentHash, err := pipeline.HashFile(pipeline.AbsArtefactPath(projectRoot, ar.Artefact))
	if err != nil {
		return false
	}
	return currentHash != ar.ArtefactHash
}

// resolveArtefactPath returns the wiring-bound artefact path verbatim.
// CON-023 / ENH-145 retires the legacy glob fallback — wiring's
// artefact field is authoritative.
func resolveArtefactPath(ar automationFrontmatter) string {
	return ar.Artefact
}

// automationRecordMtime returns the date-only (YYYY-MM-DD) local mtime
// of the wiring record file backing (tcID, framework). CON-023 / ENH-145:
// the file is gtms/automation/wiring/{tc}--{framework}.wiring.yaml.
func automationRecordMtime(projectRoot, tcID, framework string) string {
	if framework == "" || tcID == "" {
		return ""
	}
	path := filepath.Join(layout.WiringDir(projectRoot),
		fmt.Sprintf("%s--%s.wiring.yaml", tcID, framework))
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return fi.ModTime().UTC().Format("2006-01-02")
}
