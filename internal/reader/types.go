// Package reader implements the built-in local-reader adapter.
// It scans the filesystem for test cases, automation records, and task files
// to provide pipeline visibility without invoking external adapters.
package reader

import "encoding/json"

// PipelineEntry represents the pipeline status of a single test case.
//
// CON-023 / ENH-146 pinned per-TC JSON shape:
//
//	testcase           — TC ID
//	wired              — at least one wiring record exists for this TC
//	manual_ready       — manual-only TC (no wiring) with a primed manual
//	                     result file at gtms/manual/records/<tc>--manual.result.yaml
//	selected_framework — framework chosen by the hash-currency picker, or
//	                     null when no wiring framework qualifies (wired:false,
//	                     strict-framework miss, or no records)
//	frameworks         — array of per-framework entries (see FrameworkEntry)
//
// Legacy fields are retained as Go-internal carriers (json:"-") so the
// existing CLI formatters can render the table view during the
// transition window. JSON consumers see only the ENH-146 shape.
//
// SelectedFramework on the Go struct stays a `string` (empty when none was
// chosen) so internal comparisons remain straightforward. The JSON contract
// emits `null` for the empty case via a custom (Un)MarshalJSON — see
// PipelineEntry.MarshalJSON / UnmarshalJSON below.
type PipelineEntry struct {
	TestCaseID        string           `json:"testcase"`
	Slug              string           `json:"slug,omitempty"`
	Title             string           `json:"title,omitempty"`
	CreateStatus      string           `json:"create_status,omitempty"`
	Wired             bool             `json:"wired"`
	ManualReady       bool             `json:"manual_ready"`
	SelectedFramework string           `json:"-"` // emitted via (Un)MarshalJSON as `selected_framework: null|string`
	Frameworks        []FrameworkEntry `json:"frameworks"`

	// --- Legacy Go-internal carriers (json:"-") ---
	// These power the existing TTY table renderer in internal/cli/.
	// JSON consumers see only the ENH-146 shape above.
	AutomateStatus        string   `json:"-"`
	ExecuteStatus         string   `json:"-"`
	LastResult            string   `json:"-"`
	LastResultDate        string   `json:"-"`
	LastRunAt             string   `json:"-"`
	Framework             string   `json:"-"`
	Stale                 bool     `json:"-"`
	StaleTestCaseHash     bool     `json:"-"`
	ManualCoverage        string   `json:"-"`
	AvailableFrameworks   []string `json:"-"`
	DriftDetected         bool     `json:"-"`
	DriftDetectedAt       string   `json:"-"`
	TestCaseHashAtExecute string   `json:"-"`
}

// MarshalJSON emits the ENH-146 per-TC contract. SelectedFramework
// serialises as `null` when empty (no qualifying wiring) and as the
// literal framework name otherwise.
func (e PipelineEntry) MarshalJSON() ([]byte, error) {
	type pe PipelineEntry
	var sf *string
	if e.SelectedFramework != "" {
		s := e.SelectedFramework
		sf = &s
	}
	return json.Marshal(struct {
		pe
		SelectedFramework *string `json:"selected_framework"`
	}{
		pe:                pe(e),
		SelectedFramework: sf,
	})
}

// UnmarshalJSON parses the ENH-146 contract, accepting either `null` or
// a string for `selected_framework`. `null` (and missing) round-trip back
// to the empty string on the Go struct — including when unmarshalling
// into a reused (non-zero) struct, so a prior populated value never
// leaks past a `null` decode.
func (e *PipelineEntry) UnmarshalJSON(data []byte) error {
	type pe PipelineEntry
	aux := struct {
		*pe
		SelectedFramework *string `json:"selected_framework"`
	}{
		pe: (*pe)(e),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	// Explicitly reset before re-assignment so `null` and missing keys
	// both clear any prior value on a reused struct.
	e.SelectedFramework = ""
	if aux.SelectedFramework != nil {
		e.SelectedFramework = *aux.SelectedFramework
	}
	return nil
}

// FrameworkEntry is one per-framework row inside a PipelineEntry's
// frameworks[] array. CON-023 / ENH-146.
type FrameworkEntry struct {
	Framework       string `json:"framework"`
	Wired           bool   `json:"wired"` // true for wiring-derived entries; false for the synthesized `manual` result-file entry (BUG-127)
	WiringDrift     string `json:"wiring_drift"` // "" | "testcase" | "artefact" | "both"
	ArtefactPresent bool   `json:"artefact_present"`
	Artefact        string `json:"artefact"`
	// BUG-102: bootstrap state of the wiring record's artefact-hash.
	// "pending" when artefact-hash is the uninitialised sentinel
	// (wiring.PendingArtefactHash), "ready" when it is a real hash.
	WiringBootstrap string `json:"wiring_bootstrap"`
	// Overlay fields — joined from the latest terminal handoff under
	// .gtms/results/ for (testcase, framework). nil/empty when no
	// terminal result is joined.
	LastExecutedHere string `json:"last_executed_here,omitempty"`
	LastStatusHere   string `json:"last_status_here,omitempty"` // "complete" | "error" | ""
	LastResultHere   string `json:"last_result_here,omitempty"` // "pass" | "fail" | "skip" | "error" | ""
	GitCommit        string `json:"git_commit,omitempty"`
	GitBranch        string `json:"git_branch,omitempty"`
	GitDirty         *bool  `json:"git_dirty,omitempty"`
	// Detail-view fields surfaced from the latest terminal result.
	Summary     string `json:"summary,omitempty"`
	LogExcerpt  string `json:"log_excerpt,omitempty"`
	ExecutedBy  string `json:"executed_by,omitempty"`
	Environment string `json:"environment,omitempty"`
}

// PipelineDetailEntry provides detailed pipeline information for a single test case.
//
// CON-023 / ENH-146: the JSON-visible shape mirrors the per-TC shape from
// PipelineEntry (testcase / wired / manual_ready / selected_framework /
// frameworks[]) plus detail-only metadata (tags, per-stage dates).
//
// Result-contract carve-outs:
//   - summary / log_excerpt / executed_by / environment / git_commit /
//     git_branch / git_dirty surface inside the selected framework's
//     entry under frameworks[] (per FrameworkEntry).
//   - notes / notes_spill remain top-level detail fields because the CLI
//     text renderer prints the diagnostic `Notes:` block under the
//     file-paths section. Source: the latest terminal result contract's
//     `log` and `notes-spill` for the selected framework.
//
// Legacy table-renderer carriers (automate_status / execute_status /
// last_result / last_result_date / framework / stale / manual_coverage /
// available_frameworks / artefact_path / last_run_path / last_run_at)
// are retained on the Go struct (json:"-") so the CLI text formatter
// compiles, but are no longer emitted in --json.
//
// SelectedFramework on the Go struct stays a `string`; the JSON contract
// emits `null` for the empty case via (Un)MarshalJSON below.
type PipelineDetailEntry struct {
	TestCaseID        string           `json:"testcase"`
	Slug              string           `json:"slug,omitempty"`
	Title             string           `json:"title,omitempty"`
	Requirement       string           `json:"requirement,omitempty"`
	CreateStatus      string           `json:"create_status,omitempty"`
	Wired             bool             `json:"wired"`
	ManualReady       bool             `json:"manual_ready"`
	SelectedFramework string           `json:"-"` // emitted via (Un)MarshalJSON as `selected_framework: null|string`
	Frameworks        []FrameworkEntry `json:"frameworks"`

	Tags []string `json:"tags,omitempty"`
	// Per-stage timestamps for the detail view.
	// CreateDate:   date-only, sourced from test-case frontmatter `created:`.
	// AutomateDate: date-only, derived from the LOCAL mtime of the selected
	//               wiring-record file. Reflects "last regenerated on this
	//               machine" — on a fresh clone (git checkout rewrites mtimes
	//               on MINGW64 and most Linux distros) it will equal checkout
	//               date, not commit date.
	CreateDate   string `json:"create_date,omitempty"`
	AutomateDate string `json:"automate_date,omitempty"`
	// ENH-077/ENH-123: diagnostic notes payload kept TOP-LEVEL on the
	// detail entry (not inside frameworks[]) so the CLI text renderer
	// prints the `Notes:` block under the file-paths section without
	// reaching into the nested array. Sourced from the latest terminal
	// result contract's `log:` / `notes-spill:` for the selected framework.
	// (Per-framework summary / log_excerpt / executed_by / environment /
	// git_* live in frameworks[] via FrameworkEntry.)
	Notes      string `json:"notes,omitempty"`
	NotesSpill string `json:"notes_spill,omitempty"`
	// BUG-079: manual-drift diagnostic fields (audit/diagnostic; raw RFC3339
	// timestamps per the audit-field rendering rule).
	DriftDetected         bool   `json:"drift_detected,omitempty"`
	DriftDetectedAt       string `json:"drift_detected_at,omitempty"`
	TestCaseHashAtExecute string `json:"test_case_hash_at_execute,omitempty"`

	// --- Legacy Go-internal carriers (json:"-") ---
	// These power the CLI text renderer in internal/cli/status.go's
	// runStatusDetail. JSON consumers see only the ENH-146 shape above
	// (plus the diagnostic-notes / manual-drift carve-outs).
	AutomateStatus      string   `json:"-"`
	ExecuteStatus       string   `json:"-"`
	LastResult          string   `json:"-"`
	LastResultDate      string   `json:"-"`
	Framework           string   `json:"-"`
	ArtefactPath        string   `json:"-"`
	LastRunPath         string   `json:"-"`
	LastRunAt           string   `json:"-"`
	Stale               bool     `json:"-"`
	StaleTestCaseHash   bool     `json:"-"`
	ManualCoverage      string   `json:"-"`
	AvailableFrameworks []string `json:"-"`
}

// MarshalJSON emits the ENH-146 per-TC detail contract. SelectedFramework
// serialises as `null` when empty.
func (d PipelineDetailEntry) MarshalJSON() ([]byte, error) {
	type pd PipelineDetailEntry
	var sf *string
	if d.SelectedFramework != "" {
		s := d.SelectedFramework
		sf = &s
	}
	return json.Marshal(struct {
		pd
		SelectedFramework *string `json:"selected_framework"`
	}{
		pd:                pd(d),
		SelectedFramework: sf,
	})
}

// UnmarshalJSON parses the ENH-146 detail contract, accepting either
// `null` or a string for `selected_framework`. `null` and missing keys
// both clear any prior value when decoding into a reused struct.
func (d *PipelineDetailEntry) UnmarshalJSON(data []byte) error {
	type pd PipelineDetailEntry
	aux := struct {
		*pd
		SelectedFramework *string `json:"selected_framework"`
	}{
		pd: (*pd)(d),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	d.SelectedFramework = ""
	if aux.SelectedFramework != nil {
		d.SelectedFramework = *aux.SelectedFramework
	}
	return nil
}

// GapReport contains all coverage gap categories.
//
// CON-023 / ENH-146: gap categories rebuilt on the wiring model.
//
//	NoTests           — requirements with no TCs linked (unchanged)
//	NoAutomation      — TCs with no wiring records (manual TCs included)
//	CurrentlyFailing  — wiring units whose latest terminal result is "fail"
//	                    (manual rows excluded — manual fail surfaces via
//	                    NoAutomation.ManualCoverage and the status detail view)
//	ExecutionErrors   — wiring units whose latest terminal handoff has
//	                    status: error (adapter failure, disjoint from
//	                    CurrentlyFailing per ENH-130; manual rows excluded)
//	RuntimeSkipped    — wiring units whose latest terminal result is "skipped"
//	                    (manual rows excluded)
//	StaleTestCaseHash — wiring units where testcase-hash differs (ENH-117)
//	StaleArtefactHash — wiring units where artefact-hash differs (NEW)
//	MissingArtefact   — wiring units whose artefact path does not resolve (NEW)
//	DriftDetected     — manual TCs with drift-detected: true in result file
//	                    (the one category that IS manual-row-driven; checked
//	                    regardless of --framework filter, since spec drift
//	                    against a manual record is a maintenance signal
//	                    independent of the user's chosen wiring framework)
//
// Counting discipline: each gap category produces at most one entry per TC.
// Multiple matching wiring records on the same TC are deduped — the TC
// appears once per category. Strict --framework filtering (ENH-082)
// restricts the result-based wiring-unit categories (CurrentlyFailing,
// ExecutionErrors, RuntimeSkipped, StaleTestCaseHash, StaleArtefactHash,
// MissingArtefact) to wiring records of the requested framework.
// DriftDetected is excluded from --framework filtering.
//
// Retired (no longer surfaced): SpecButNoRecord, NeverExecuted, StaleExecution.
// "Not run here" is not a gap — it's the expected state on a fresh clone.
type GapReport struct {
	TotalTestCases    int        `json:"total_test_cases"`
	NoTests           []GapEntry `json:"no_tests"`
	NoAutomation      []GapEntry `json:"no_automation"`
	CurrentlyFailing  []GapEntry `json:"currently_failing"`
	ExecutionErrors   []GapEntry `json:"execution_errors"`
	RuntimeSkipped    []GapEntry `json:"runtime_skipped"`
	StaleTestCaseHash []GapEntry `json:"stale_testcase_hash"`
	StaleArtefactHash []GapEntry `json:"stale_artefact_hash"`
	MissingArtefact   []GapEntry `json:"missing_artefact"`
	DriftDetected     []GapEntry `json:"drift_detected"`

	// Legacy Go-internal carriers (json:"-") — kept temporarily so
	// existing CLI formatters compile during the transition. Always nil.
	NeverExecuted   []GapEntry `json:"-"`
	SpecButNoRecord []GapEntry `json:"-"`
	StaleExecution  []GapEntry `json:"-"`
}

// GapEntry represents a single item in a gap category.
type GapEntry struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Since string `json:"since,omitempty"`
	// ENH-134: manual-coverage sub-state. Populated on NoAutomation entries
	// to disambiguate no-coverage from prepared-as-manual from recorded-as-manual.
	ManualCoverage string `json:"manual_coverage,omitempty"`
}

// FolderSummaryEntry represents aggregate pipeline counts for one folder.
//
// ENH-089 added Passing/Failing/Errored/InFlight to drive icon selection in
// the renderer (priority rule: ✗ > ● > ○ > ✓). Existing fields
// (Created/DraftCount/Automated/Executed) retain the same names and JSON
// keys for backward compatibility with existing JSON consumers and BATS
// assertions.
type FolderSummaryEntry struct {
	Folder     string `json:"folder"`
	Created    int    `json:"created"`
	DraftCount int    `json:"draft_count"`
	Automated  int    `json:"automated"`
	Executed   int    `json:"executed"`
	// ENH-089: per-stage outcome breakdown for icon-forward summary.
	// Passing, Failing, Errored count TCs whose selected automation record
	// has the matching last-formal-result. InFlight counts TCs with an
	// active execute task in gtms/tasks/in-progress/.
	Passing  int `json:"passing"`
	Failing  int `json:"failing"`
	Errored  int `json:"errored"`
	Skipped  int `json:"skipped"`
	InFlight int `json:"in_flight"`
	// BUG-043: count of TCs that have automation records under other frameworks
	// but not the requested --framework. Zero when no framework filter is active.
	FrameworkMismatch int `json:"framework_mismatch"`
	// ENH-134: manual-coverage sub-state counts.
	ManualPrepared int `json:"manual_prepared,omitempty"`
	ManualRecorded int `json:"manual_recorded,omitempty"`
}

// GapsFolderSummaryEntry represents aggregate gap counts for one folder.
//
// NotExecuted is retained on the JSON shape for compatibility but is no
// longer populated. CON-023 / ENH-146 retired "not run here" as a gap;
// counts always render as zero. The field will be removed in a later ENH.
type GapsFolderSummaryEntry struct {
	Folder       string `json:"folder"`
	Created      int    `json:"created"`
	NotAutomated int    `json:"not_automated"`
	NotExecuted  int    `json:"not_executed"`
	Failing      int    `json:"failing"`
	Skipped      int    `json:"skipped"`
	// BUG-043: count of TCs that have automation records under other frameworks
	// but not the requested --framework. Zero when no framework filter is active.
	FrameworkMismatch int `json:"framework_mismatch"`
	// ENH-134: manual-coverage sub-state counts.
	ManualPrepared int `json:"manual_prepared,omitempty"`
	ManualRecorded int `json:"manual_recorded,omitempty"`
	// ENH-117: count of TCs with stale testcase-hash.
	StaleTestCaseHash int `json:"stale_testcase_hash,omitempty"`
	// BUG-079: count of TCs with drift-detected: true in their manual result file.
	DriftDetected int `json:"drift_detected,omitempty"`
}

// TriageInfo holds the current state of a test case for triage decision-making.
type TriageInfo struct {
	TestCaseID       string
	AutomationRecord *automationFrontmatter // current automation state
	LastResult       string                 // pass or fail
	LastRun          string                 // path or URL to results
	Stale            bool                   // true when artefact hash differs from stored hash
	FailureHistory   []TriageEntry          // previous triage decisions
}

// TriageEntry represents a single triage decision in the history.
type TriageEntry struct {
	Date     string // ISO 8601
	Category string // automation-wrong, test-wrong, app-wrong
	Summary  string
	Defect   string // optional defect link
}

// TriageResult describes what was done by a triage operation.
type TriageResult struct {
	TestCaseID string   `json:"test_case_id"`
	Category   string   `json:"category"`
	Summary    string   `json:"summary"`
	Defect     string   `json:"defect,omitempty"`
	Actions    []string `json:"actions"`
	NewTaskID  string   `json:"new_task_id,omitempty"`
}

// testCaseFrontmatter represents the YAML frontmatter of a test case file.
type testCaseFrontmatter struct {
	ID          string   `yaml:"test_case_id"`
	Title       string   `yaml:"title"`
	Requirement string   `yaml:"requirement"`
	Priority    string   `yaml:"priority,omitempty"`
	Type        string   `yaml:"type,omitempty"`
	Status      string   `yaml:"status,omitempty"`
	Tags        []string `yaml:"tags,omitempty"`
	Created     string   `yaml:"created,omitempty"`
	SourceFile  string   `yaml:"-"` // populated by scanner, not from YAML
}

// automationFrontmatter represents the YAML frontmatter of an automation record.
// ENH-123: field names align with pipeline.RecordCommon + pipeline.AutomationRecord.
//
// CON-023 / ENH-145 / ENH-146 (transitional carrier):
//
// Post-cutover, automationFrontmatter is no longer parsed from any
// .automation.md file — production reader paths route through wiring +
// the terminal-result overlay. scanAutomationRecords (status.go) still
// builds this struct from wiring + overlay so PipelineFolderSummary and a
// few legacy gap classifiers keep compiling. The fields below are NOT a
// wire schema; they are an in-memory adapter between the new wiring layer
// and the not-yet-migrated folder-summary code.
//
// TODO(CON-023): retire LastDevResult, Cycle, Defect when the legacy
// migration bridge is removed. These fields are dropped from the wiring
// schema (CON-023 Q #20) and the new picker never consults them; they
// linger only so the transitional carrier survives the worktree window.
// LastDevResult was the historical "last development outcome" stamp,
// Cycle was the bumped-by-`automate --force` counter the legacy picker
// used as a tie-breaker (now replaced by hash-currency + framework
// precedence), and Defect was the per-record defect link list. All three
// are pinned to zero-values by scanAutomationRecords today.
type automationFrontmatter struct {
	TestCase  string `yaml:"testcase"`
	Framework string `yaml:"framework"`
	Status    string `yaml:"status"`
	Artefact  string `yaml:"artefact"`
	Adapter   string `yaml:"adapter"`
	// LastDevResult: retired field — TODO(CON-023): remove when the
	// legacy migration bridge is retired (see scanAutomationRecords).
	LastDevResult    string `yaml:"last-dev-result"`
	Result           string `yaml:"result"`            // was last-formal-result
	ExecutedArtefact string `yaml:"executed_artefact"` // was last-formal-run
	ExecutedAt       string `yaml:"executed_at"`       // RFC3339 UTC timestamp (was last-formal-run-at)
	ExecutedBy       string `yaml:"executed_by"`       // CI runner identity or tester name
	Environment      string `yaml:"environment"`       // target environment (staging, production, etc.)
	ArtefactHash     string `yaml:"artefact-hash"`
	TestCaseHash     string `yaml:"testcase-hash"` // ENH-117: spec content hash at record-write time
	Notes            string `yaml:"notes"`         // ENH-077/ENH-123: diagnostic output (was log)
	NotesSpill       string `yaml:"notes-spill"`   // ENH-077/ENH-123: relative path to full notes (was log-spill)
	Summary          string `yaml:"summary"`       // CON-023: lifted from the retired automation record onto the result-contract overlay
	Attempts         int    `yaml:"attempts"`
	// Cycle: retired field — TODO(CON-023): remove when the legacy
	// migration bridge is retired. Picker uses hash-currency +
	// framework precedence; no counter survives.
	Cycle int `yaml:"cycle"`
	// Defect: retired field — TODO(CON-023): remove when the legacy
	// migration bridge is retired. Triage app-wrong appends to
	// gtms/triage-history/<tc>.md instead.
	Defect []string `yaml:"defect"` // ENH-123: array of defect IDs (was string)
}

// taskFrontmatter represents the YAML frontmatter of a task file.
type taskFrontmatter struct {
	ID      string `yaml:"id"`
	Type    string `yaml:"type"`
	Target  string `yaml:"target"`
	Adapter string `yaml:"adapter"`
	Status  string `yaml:"status"`
	Created string `yaml:"created"`
	Branch  string `yaml:"branch"`
}

// MapReport contains the full traceability map grouped by requirement.
type MapReport struct {
	Groups   []RequirementGroup `json:"groups"`
	Unlinked []MapEntry         `json:"unlinked"`
	Summary  MapSummary         `json:"summary"`
}

// RequirementGroup represents one requirement and all its test cases.
type RequirementGroup struct {
	Requirement string     `json:"requirement"`
	TestCases   []MapEntry `json:"test_cases"`
}

// MapEntry represents one test case in the traceability map.
//
// CON-023 / ENH-145 / ENH-146 — Phase 3D wiring cutover:
//
// The compact carriers (AutomateStatus / ExecuteStatus / LastResult /
// ArtefactPath / Stale / ManualCoverage / AvailableFrameworks / drift
// fields) remain on the JSON contract because they are pipeline-stage
// labels the existing human and JSON consumers depend on — not legacy
// automation-record lifecycle fields. They are now derived from wiring
// + the latest terminal result overlay rather than from the retired
// .automation.md surface.
//
// The new fields (Wired / ManualReady / SelectedFramework / Frameworks[])
// mirror PipelineEntry's ENH-146 shape so map JSON consumers see the
// same wiring-unit identity surface as `gtms status --json`.
//
// Multi-framework discipline: Frameworks[] enumerates every wired
// framework for the TC; the compact LastResult / ExecuteStatus carry
// the worst-of-frameworks outcome so a picker-selected pass cannot
// hide a sibling framework's fail / error / skipped on the human row.
//
// SelectedFramework stays a Go `string` (empty when none qualified)
// for ergonomic internal use; the JSON contract emits `null` for the
// empty case via MapEntry.MarshalJSON / UnmarshalJSON.
type MapEntry struct {
	TestCaseID     string `json:"test_case_id"`
	Slug           string `json:"slug"`
	Title          string `json:"title"`
	CreateStatus   string `json:"create_status"`
	AutomateStatus string `json:"automate_status"`
	ExecuteStatus  string `json:"execute_status"`
	LastResult     string `json:"last_result"`
	ArtefactPath   string `json:"artefact_path,omitempty"`
	Stale          bool   `json:"stale,omitempty"`
	// ENH-134: manual-coverage sub-state (same semantics as PipelineEntry).
	ManualCoverage string `json:"manual_coverage,omitempty"`
	// BUG-043: frameworks available across all automation records for this TC.
	AvailableFrameworks []string `json:"available_frameworks,omitempty"`
	// BUG-079: drift diagnostic fields (same semantics as PipelineEntry).
	DriftDetected         bool   `json:"drift_detected,omitempty"`
	DriftDetectedAt       string `json:"drift_detected_at,omitempty"`
	TestCaseHashAtExecute string `json:"test_case_hash_at_execute,omitempty"`

	// CON-023 / ENH-146 wiring shape — mirrors PipelineEntry.
	Wired             bool             `json:"wired"`
	ManualReady       bool             `json:"manual_ready"`
	SelectedFramework string           `json:"-"` // emitted via (Un)MarshalJSON as `selected_framework: null|string`
	Frameworks        []FrameworkEntry `json:"frameworks"`
}

// MarshalJSON emits the ENH-146 per-TC map entry contract.
// SelectedFramework serialises as `null` when empty (no qualifying
// wiring) and as the literal framework name otherwise. Mirrors
// PipelineEntry.MarshalJSON.
func (e MapEntry) MarshalJSON() ([]byte, error) {
	type me MapEntry
	var sf *string
	if e.SelectedFramework != "" {
		s := e.SelectedFramework
		sf = &s
	}
	return json.Marshal(struct {
		me
		SelectedFramework *string `json:"selected_framework"`
	}{
		me:                me(e),
		SelectedFramework: sf,
	})
}

// UnmarshalJSON parses the ENH-146 map entry contract, accepting either
// `null` or a string for `selected_framework`. `null` and missing keys
// both clear any prior value on a reused struct. Mirrors
// PipelineEntry.UnmarshalJSON.
func (e *MapEntry) UnmarshalJSON(data []byte) error {
	type me MapEntry
	aux := struct {
		*me
		SelectedFramework *string `json:"selected_framework"`
	}{
		me: (*me)(e),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	e.SelectedFramework = ""
	if aux.SelectedFramework != nil {
		e.SelectedFramework = *aux.SelectedFramework
	}
	return nil
}

// MapSummary provides aggregate statistics for the traceability map.
type MapSummary struct {
	TotalRequirements int `json:"total_requirements"`
	TotalTestCases    int `json:"total_test_cases"`
	Automated         int `json:"automated"`
	Executed          int `json:"executed"`
	UnlinkedCount     int `json:"unlinked_count"`
}
