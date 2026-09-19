package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/output"
	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
	"github.com/aitestmanagement/gtms-cli/internal/reader"
	targetpkg "github.com/aitestmanagement/gtms-cli/internal/target"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	var jsonOut bool
	var recursive bool
	var framework string
	var showTriage bool
	var triageValue string

	cmd := &cobra.Command{
		Use:   "status [test-case-id | folder]",
		Short: "Show pipeline overview status",
		Long: `Show the pipeline dashboard. With no arguments, displays a folder summary.
Use 'gaps' for coverage analysis or 'map' for requirement traceability.

  gtms status              -- folder summary (per-folder pipeline rollup)
  gtms status bug-022      -- scoped to gtms/test/cases/bug-022/
  gtms status tc-a1b2c3d4       -- detail view for one test case
  gtms status --json       -- machine-readable JSON output
  gtms status -r           -- flat per test case list (all folders)
  gtms status --framework bats  -- show one framework's pipeline state`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetProjectRoot()
			cfg := GetConfig()

			// BUG-128 v2: non-fatal "did you mean" hint for near-typo
			// framework names. Fires only when --framework is unrecognised
			// AND within edit distance <= 2 of a known name. Never errors,
			// never changes stdout/JSON/exit code.
			if hint := frameworkHint(framework, knownFrameworks(cfg, root)); hint != "" {
				fmt.Fprintln(os.Stderr, hint)
			}

			defaultFw := config.DefaultFramework(cfg)
			if framework != "" {
				defaultFw = framework
			}
			// ENH-082: an explicit --framework flag enables strict per-TC
			// filtering -- TCs without a matching framework record render as
			// em-dashes instead of falling back to a different framework.
			// A config-level default keeps its current fallback behaviour.
			strict := framework != ""

			// ENH-202: --triage implies triage presentation and requires a
			// scope. An unscoped root invocation (no folder, no TC, no -r) is
			// refused BEFORE any scan so it never expands the whole project
			// (item 2 / tc-cd5d4d75 step 9). --show-triage at root is allowed;
			// only the filter needs a scope.
			triageFilter := triageValue
			if triageFilter != "" && len(args) == 0 && !recursive {
				msg := "--triage requires a folder, test-case ID, or --recursive scope."
				output.Errorf(msg, "Try 'gtms status -r --triage environment'.")
				return output.AsDisplayed(fmt.Errorf(msg))
			}
			// The filter implies presentation for the scoped views.
			showTriageEff := showTriage || triageFilter != ""

			// Best-effort triage labels for JSON; a config problem never blocks
			// a read (the pre-run hook already enforced gtms.config loaded).
			triageValues := map[string]string{}
			if cfg != nil {
				if v, verr := cfg.ResolveTriageValues(); verr == nil {
					triageValues = v
				}
			}

			if len(args) > 0 {
				// BUG-167: shared classifier -- folder case preserved (existence
				// first), only confirmed TC IDs lowercased/stripped.
				res, err := targetpkg.Classify(root, args[0])
				if err != nil {
					output.Errorf(err.Error(), "")
					return output.AsDisplayed(err)
				}
				if res.Kind == targetpkg.KindTCID {
					// BUG-059 (REV-113): a folder-qualified TC must resolve scoped.
					if err := requireScopedSpec(root, res); err != nil {
						output.Errorf(err.Error(), "")
						return output.AsDisplayed(err)
					}
					// TC ID -> detail view (flat lookup on the bare ID)
					return runStatusDetail(os.Stdout, root, res.TCID, jsonOut, defaultFw, strict, showTriageEff, triageFilter, triageValues)
				}
				// Folder scope for overview
				scope, serr := buildScopeFromArg(root, res.Target, recursive)
				if serr != nil {
					output.Errorf(serr.Error(), "")
					return output.AsDisplayed(serr)
				}
				return runStatusOverview(os.Stdout, root, scope, jsonOut, defaultFw, strict, showTriageEff, triageFilter, triageValues)
			}

			// No arg: -r → recursive flat list; default → folder summary
			if recursive {
				scope, serr := buildScopeFromArg(root, "", true)
				if serr != nil {
					output.Errorf(serr.Error(), "")
					return output.AsDisplayed(serr)
				}
				return runStatusOverview(os.Stdout, root, scope, jsonOut, defaultFw, strict, showTriageEff, triageFilter, triageValues)
			}
			// BUG-082: pass the explicit --framework value, not the
			// config default. When no flag is given, framework == ""
			// and PipelineFolderSummary skips framework filtering,
			// counting ALL non-manual wiring as automated.
			// ENH-202: the root summary gains a per-folder TRIAGED count only
			// under --show-triage (the filter is refused at root above).
			return runStatusFolderSummary(os.Stdout, root, jsonOut, framework, showTriage, triageValues)
		},
	}

	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "Include test cases from subdirectories; with no folder, show the flat per test case view")
	cmd.Flags().StringVar(&framework, "framework", "", "Select one framework per test case (non-matching test cases show as not-set)")
	cmd.Flags().BoolVar(&showTriage, "show-triage", false, "Show triage classification (per-folder TRIAGED count, or a per-test-case TRIAGE column/section)")
	cmd.Flags().StringVar(&triageValue, "triage", "", "Filter to test cases carrying this triage value (implies --show-triage; needs a folder, test-case ID, or -r)")

	return cmd
}

// runStatusOverview displays the pipeline overview table for test cases within the scope.
// ENH-082: strictFramework is set by the caller when --framework was supplied
// explicitly so per-TC selection skips fallback to a different framework.
//
// ENH-202: per-test-case JSON ALWAYS carries the nested triage object (no
// presentation flag), so the JSON path always attaches triage. Human output
// gains a TRIAGE column only under showTriage (which the caller already ORs
// with an active --triage filter). triageFilter, when set, restricts the view
// to test cases whose selected framework carries that active value; an empty
// match is a successful, empty result.
func runStatusOverview(w io.Writer, projectRoot string, scope *reader.ScopeInfo, jsonOut bool, defaultFramework string, strictFramework bool, showTriage bool, triageFilter string, triageValues map[string]string) error {
	entries, err := reader.PipelineStatus(projectRoot, scope, defaultFramework, strictFramework)
	if err != nil {
		return err
	}
	// BUG-193 / REV-170 CODEX-001: only an EXPLICIT --framework (strict) gates
	// the manual-only entry-level triage carrier; a config default does not.
	explicitFramework := ""
	if strictFramework {
		explicitFramework = defaultFramework
	}

	if jsonOut {
		// Per-TC JSON always exposes triage; the filter, when set, narrows both
		// which rows appear and which triage objects attach.
		if err := reader.AttachTriageToEntries(projectRoot, entries, triageValues, triageFilter, explicitFramework); err != nil {
			return err
		}
		if triageFilter != "" {
			entries = filterEntriesByTriage(entries)
		}
		return writeStatusJSON(w, entries)
	}

	// Human output. Attach triage only when it will be shown (showTriage is the
	// caller's showTriage||filter), so default output stays byte-identical.
	if showTriage {
		if err := reader.AttachTriageToEntries(projectRoot, entries, triageValues, triageFilter, explicitFramework); err != nil {
			return err
		}
		if triageFilter != "" {
			entries = filterEntriesByTriage(entries)
		}
	}

	// Print scope feedback BEFORE the empty-state check
	if scope != nil {
		fmt.Fprintf(w, "Scope: %s", scope.RelPath)
		if !scope.Recursive {
			fmt.Fprint(w, " (use -r for recursive)")
		}
		fmt.Fprintln(w)
		fmt.Fprintln(w)
	}

	// ENH-202: an empty --triage match is a successful, empty result with a
	// diagnostic naming the value (tc-cd5d4d75 steps 4-5). Exit status alone
	// separates it from the refused root invocation.
	if triageFilter != "" && len(entries) == 0 {
		fmt.Fprintf(w, "No test cases match triage value '%s'.\n", triageFilter)
		return nil
	}

	if len(entries) == 0 {
		fmt.Fprintln(w, "No test cases found.")
		return nil
	}

	var tbl *output.Table
	if showTriage {
		tbl = output.NewTable("TEST CASE", "CREATE", "AUTOMATE", "EXECUTE", "LAST RESULT", "TRIAGE")
	} else {
		tbl = output.NewTable("TEST CASE", "CREATE", "AUTOMATE", "EXECUTE", "LAST RESULT")
	}
	anyChanged := false
	for _, e := range entries {
		execCol := formatStageStatus(e.ExecuteStatus)
		if e.Stale {
			execCol += " " + output.IconWarning
		}
		resultCol := formatLastResult(e.LastResult, e.LastResultDate, e.Framework)
		// BUG-079: append drift text marker when manual result has drifted.
		if e.DriftDetected {
			resultCol += " [drift]"
		}
		if showTriage {
			triageCol, changed := formatTriageCell(selectedFrameworkTriage(e))
			if changed {
				anyChanged = true
			}
			tbl.AddRow(
				formatTestCaseColumn(e.TestCaseID, e.Slug),
				formatStageStatus(e.CreateStatus),
				formatStageStatus(e.AutomateStatus),
				execCol,
				resultCol,
				triageCol,
			)
		} else {
			tbl.AddRow(
				formatTestCaseColumn(e.TestCaseID, e.Slug),
				formatStageStatus(e.CreateStatus),
				formatStageStatus(e.AutomateStatus),
				execCol,
				resultCol,
			)
		}
	}

	tbl.Render(w)
	fmt.Fprintf(w, "\nKey:\n")
	fmt.Fprintf(w, "  %s = complete  %s = error  %s = stale  %s = skipped\n",
		output.IconComplete, output.IconError, output.IconWarning, output.IconSkipped)
	fmt.Fprintf(w, "  %s = in progress    %s = pending  \u2014 = not yet attempted\n",
		output.IconInProgress, output.IconPending)
	// ENH-202: explain the triage warning glyph when a row carries it, so the
	// reader can learn what it means from the same output (tc-8ed5c09e step 3).
	if showTriage && anyChanged {
		fmt.Fprintf(w, "  %s = result changed since triage\n", output.IconWarning)
	}

	if hint := statusHint(entries); hint != "" {
		fmt.Fprintln(w)
		output.Dimln(w, "Next: "+hint)
	}
	return nil
}

// selectedFrameworkTriage returns the triage object attached to a test case's
// SELECTED framework entry, or nil when the test case has no selected framework
// or no attached triage there. Human presentation and filtering both key off
// the selected framework so a classification on another framework never renders
// or matches (ENH-202 / tc-82eebc80).
func selectedFrameworkTriage(e reader.PipelineEntry) *reader.FrameworkTriage {
	if e.SelectedFramework != "" {
		// A selected (wired) framework answers for itself: its own triage or
		// nil. Never fall through to the entry-level carrier here, or a manual
		// classification could render or filter as the wired framework's
		// (BUG-193 worktree review, 2026-09-13).
		for i := range e.Frameworks {
			if e.Frameworks[i].Framework == e.SelectedFramework {
				return e.Frameworks[i].Triage
			}
		}
		return nil
	}
	// BUG-193: a manual-only test case has no selected framework and an empty
	// frameworks[], so its manual triage is carried at the test-case level.
	return e.Triage
}

// filterEntriesByTriage keeps only entries whose selected framework carries an
// attached triage object. AttachTriage has already restricted attachment to the
// active records matching the --triage value, so a non-nil selected triage means
// the row matched the filter.
func filterEntriesByTriage(entries []reader.PipelineEntry) []reader.PipelineEntry {
	kept := make([]reader.PipelineEntry, 0, len(entries))
	for _, e := range entries {
		if selectedFrameworkTriage(e) != nil {
			kept = append(kept, e)
		}
	}
	return kept
}

// formatTriageCell renders the per-test-case TRIAGE column: the stable stored
// value key (never the friendly label), with the warning glyph appended when
// the latest execution result has changed since triage. Returns "\u2014" when
// no active classification applies. The second return reports whether the cell
// carries the changed-result warning so the caller can emit the legend.
func formatTriageCell(t *reader.FrameworkTriage) (string, bool) {
	if t == nil || t.Status != "active" {
		return "\u2014", false
	}
	changed := t.ResultChangedSinceTriage != nil && *t.ResultChangedSinceTriage
	if changed {
		return t.Value + " " + output.IconWarning, true
	}
	return t.Value, false
}

// runStatusFolderSummary displays the folder summary view (no-arg default).
//
// ENH-089: cells use icon-forward rendering (✓ ● ○ ✗) with fraction suffix
// only on non-✓ cells. CREATE column always shows ✓ (a TC existing in
// gtms/test/cases/ IS the creation). AUTOMATE/EXECUTE columns apply the
// documented priority rule via the formatFolder*Cell helpers below.
func runStatusFolderSummary(w io.Writer, projectRoot string, jsonOut bool, defaultFramework string, showTriage bool, triageValues map[string]string) error {
	entries, err := reader.PipelineFolderSummary(projectRoot, defaultFramework)
	if err != nil {
		return err
	}

	// ENH-202: the root summary gains a per-folder TRIAGED count only under
	// --show-triage. It stays an aggregate: one count per folder, never a
	// per-test-case expansion (tc-db0624fc). Without the flag the count is not
	// computed and not emitted (default output byte-unchanged), so the *int
	// Triaged pointer stays nil and its JSON key is omitted (tc-db0624fc step 8).
	if showTriage {
		counts, cErr := reader.FolderTriagedCounts(projectRoot, defaultFramework)
		if cErr != nil {
			return cErr
		}
		for i := range entries {
			c := counts[entries[i].Folder]
			entries[i].Triaged = &c
		}
	}

	if jsonOut {
		return writeStatusFolderJSON(w, entries)
	}

	if len(entries) == 0 {
		fmt.Fprintln(w, "No test cases found.")
		fmt.Fprintln(w)
		output.Dimln(w, "Next:")
		output.Dimln(w, "    gtms create <folder>                    \u2014 create your first test case")
		output.Dimln(w, "  or")
		output.Dimln(w, "    gtms create <folder> --reference <doc>  \u2014 create with a linked reference")
		return nil
	}

	fmt.Fprintln(w)
	// BUG-042: dedicated SKIP column surfaces the runtime-skipped count as a
	// digit alongside the existing ⊘ glyph. The JSON folder summary already
	// emits a numeric `skipped` field (ENH-094); the text renderer now
	// matches it so users can read skip counts without subtracting.
	var tbl *output.Table
	if showTriage {
		tbl = output.NewTable("FOLDER", "TC", "CREATE", "AUTOMATE", "EXECUTE", "SKIP", "TRIAGED")
	} else {
		tbl = output.NewTable("FOLDER", "TC", "CREATE", "AUTOMATE", "EXECUTE", "SKIP")
	}
	for _, e := range entries {
		// TC column carries the raw count plus the optional draft annotation
		// (ENH-066). Draft annotation does NOT duplicate into CREATE.
		tcCol := fmt.Sprintf("%d", e.Created)
		if e.DraftCount == 1 {
			tcCol = fmt.Sprintf("%d (1 draft)", e.Created)
		} else if e.DraftCount > 1 {
			tcCol = fmt.Sprintf("%d (%d drafts)", e.Created, e.DraftCount)
		}
		if showTriage {
			triaged := 0
			if e.Triaged != nil {
				triaged = *e.Triaged
			}
			tbl.AddRow(
				e.Folder,
				tcCol,
				output.IconComplete, // CREATE always ✓
				formatFolderAutomateCell(e),
				formatFolderExecuteCell(e),
				formatFolderSkipCell(e),
				fmt.Sprintf("%d", triaged),
			)
		} else {
			tbl.AddRow(
				e.Folder,
				tcCol,
				output.IconComplete, // CREATE always ✓
				formatFolderAutomateCell(e),
				formatFolderExecuteCell(e),
				formatFolderSkipCell(e),
			)
		}
	}
	tbl.Render(w)

	fmt.Fprintf(w, "\nKey: %s = all pass  %s = some failing  %s = in progress  %s = not yet attempted  %s = skipped\n",
		output.IconComplete, output.IconError, output.IconInProgress, output.IconPending, output.IconSkipped)

	fmt.Fprintln(w)
	output.Dimln(w, "Next: gtms status <folder>  \u2014 see individual test cases")
	return nil
}

// formatFolderAutomateCell returns the AUTOMATE-column text for a folder summary row.
// ENH-089: ✓ when fully automated, ○ N/M (automated/total) otherwise.
// Manual-framework records are excluded from the Automated count by the reader
// (ENH-068), so a folder with only manual records correctly shows ○ 0/N here.
func formatFolderAutomateCell(e reader.FolderSummaryEntry) string {
	if e.Created == 0 || e.Automated >= e.Created {
		return output.IconComplete
	}
	return output.IconPending + " " + fmt.Sprintf("%d/%d", e.Automated, e.Created)
}

// formatFolderExecuteCell returns the EXECUTE-column text for a folder summary row.
// ENH-089 priority rule (worst-news-wins): ✗ > ● > ○ > ✓.
//   - ✗ : at least one TC has last-formal-result fail or error.
//   - ● : at least one TC has an active execute task in gtms/tasks/in-progress/
//     AND no fails / errors.
//   - ○ : not all TCs have a passing result yet AND nothing in flight AND no fails.
//   - ✓ : all TCs in the folder have last-formal-result: pass.
//
// The fraction is always passing/total, regardless of icon. The icon tells you
// WHY the fraction isn't full (fail vs in-flight vs unrun) without overloading
// the number.
//
// BUG-042: the explicit skipped COUNT lives in the dedicated SKIP column
// (formatFolderSkipCell). EXECUTE keeps the ⊘ glyph + pass-ratio when nothing
// worse is happening in the folder so the at-a-glance severity ordering is
// preserved and the user still sees why the ratio isn't 3/3.
func formatFolderExecuteCell(e reader.FolderSummaryEntry) string {
	if e.Created == 0 {
		// No TCs in folder -- treat as ✓ (vacuously all-pass). Defensive only.
		return output.IconComplete
	}
	fraction := fmt.Sprintf("%d/%d", e.Passing, e.Created)
	switch {
	case e.Failing+e.Errored > 0:
		return output.IconError + " " + fraction
	case e.InFlight > 0:
		return output.IconInProgress + " " + fraction
	case e.Skipped > 0:
		return output.IconSkipped + " " + fraction
	case e.Passing < e.Created:
		return output.IconPending + " " + fraction
	default:
		return output.IconComplete
	}
}

// formatFolderSkipCell returns the SKIP-column text for a folder summary row.
//
// BUG-042: the existing EXECUTE column collapses pass/skipped into a single
// `⊘ 1/3` glyph + ratio, so the literal skipped count (e.g. 2) never appears
// anywhere in the row. This helper surfaces the count as a digit so users
// can read it without subtracting from the denominator. The JSON folder
// summary already emits this number via FolderSummaryEntry.Skipped.
//
// Rendering:
//   - No skips -> "--" (em-dash, same "not applicable" glyph the other columns use).
//   - Skips present -> "⊘ N" so the glyph and the numeric count stay together
//     even if future layout changes split columns.
func formatFolderSkipCell(e reader.FolderSummaryEntry) string {
	if e.Skipped == 0 {
		return "\u2014"
	}
	return fmt.Sprintf("%s %d", output.IconSkipped, e.Skipped)
}

// writeStatusFolderJSON outputs the folder summary as indented JSON.
func writeStatusFolderJSON(w io.Writer, entries []reader.FolderSummaryEntry) error {
	if entries == nil {
		entries = []reader.FolderSummaryEntry{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(entries)
}

// statusHint inspects pipeline entries and suggests the next logical action.
// If no default adapter is configured for the suggested command, the hint
// includes --adapter <name> so the user isn't left guessing.
func statusHint(entries []reader.PipelineEntry) string {
	needsAutomate := false
	needsExecute := false
	for _, e := range entries {
		if e.AutomateStatus == "none" {
			needsAutomate = true
		}
		if e.ExecuteStatus == "none" && e.AutomateStatus != "none" {
			needsExecute = true
		}
	}
	if needsAutomate {
		// BUG-080: when the project is unambiguously manual-prime-oriented,
		// surface the first-class gtms prime command instead of gtms automate.
		if shouldRewriteToPrime() {
			return "gtms prime <tc-id> --framework manual  -- stamp a manual result template"
		}
		return "gtms automate " + adapterHint("automate") + "<tc-id>  -- automate a test case"
	}
	if needsExecute {
		return "gtms execute " + adapterHint("execute") + "<tc-id>   -- run an automated test"
	}
	return ""
}

// shouldRewriteToPrime returns true when the status hint should be rewritten
// to the gtms prime command for an un-automated test case.
//
// BUG-080 round-2: the rewrite only fires when the project is unambiguously
// manual-prime-oriented. The real adapter resolver
// (internal/adapter/resolver.go) does not pick a "first registered" adapter
// when no default is set -- it errors out -- so a status hint that picks a
// first-registered would propose a command (gtms prime) the user hadn't
// signalled they wanted in an ambiguous multi-adapter project.
//
// BUG-097: ENH-150 moved manual-prime from adapters.automate to a peer
// adapters.prime bucket (the minimal preset now registers manual-prime under
// prime:). Both shapes must route to the prime hint to preserve ENH-150's
// promised backward compatibility while still picking up the new shape.
//
// Rewrite when ANY of:
//   - defaults.automate == "manual-prime" (legacy ENH-111 explicit), OR
//   - no defaults.automate AND exactly one automate adapter is registered
//     AND it is "manual-prime" (legacy ENH-111 single-adapter shape), OR
//   - defaults.prime == "manual-prime" (ENH-150 explicit prime default; the
//     shape the minimal preset ships), OR
//   - no defaults.automate AND exactly one prime adapter is registered AND it
//     is "manual-prime" (ENH-150 single-prime shape).
//
// The "no defaults.automate" guard on the single-adapter branches stops the
// non-manual case (e.g. defaults.automate: local-claude alongside a prime
// bucket) from accidentally promoting to prime.
//
// Otherwise fall through to the generic gtms automate hint.
func shouldRewriteToPrime() bool {
	cfg := GetConfig()
	if cfg == nil {
		return false
	}
	// Legacy ENH-111 shape: manual-prime under adapters.automate
	if cfg.Defaults != nil && cfg.Defaults["automate"] == "manual-prime" {
		return true
	}
	if cfg.Defaults == nil || cfg.Defaults["automate"] == "" {
		if adapters, ok := cfg.Adapters["automate"]; ok && len(adapters) == 1 {
			if _, isManualPrime := adapters["manual-prime"]; isManualPrime {
				return true
			}
		}
	}
	// ENH-150 shape: manual-prime under peer adapters.prime bucket
	if cfg.Defaults != nil && cfg.Defaults["prime"] == "manual-prime" {
		return true
	}
	if cfg.Defaults == nil || cfg.Defaults["automate"] == "" {
		if adapters, ok := cfg.Adapters["prime"]; ok && len(adapters) == 1 {
			if _, isManualPrime := adapters["manual-prime"]; isManualPrime {
				return true
			}
		}
	}
	return false
}

// adapterHint returns "--adapter <name> " if no default is configured for the
// given command, showing the first available adapter name. Returns "" if a
// default is already set.
func adapterHint(command string) string {
	cfg := GetConfig()
	if cfg == nil {
		return ""
	}
	if cfg.Defaults != nil && cfg.Defaults[command] != "" {
		return ""
	}
	if cfg.Adapters != nil {
		if adapters, ok := cfg.Adapters[command]; ok {
			for name := range adapters {
				return "--adapter " + name + " "
			}
		}
	}
	return ""
}

// runStatusDetail displays detailed status for a single test case.
// ENH-082: strictFramework is true when the user supplied --framework on the
// command line, so the detail view shows em-dashes for AUTOMATE/EXECUTE
// instead of falling back to a different framework's record.
func runStatusDetail(w io.Writer, projectRoot, testCaseID string, jsonOut bool, defaultFramework string, strictFramework bool, showTriage bool, triageFilter string, triageValues map[string]string) error {
	// F3: direct single-TC read resolves ownership once and reuses that snapshot
	// for the rest of the read (one project-wide enumeration per operation).

	detail, err := reader.PipelineDetail(projectRoot, testCaseID, defaultFramework, strictFramework)
	if err != nil {
		// ADR-024 / BUG-185 / BUG-188: direct single-TC read refuses an ambiguous id
		// (naming its owners) or an unprovable candidate (with its repair), visibly
		// on stderr with a non-zero exit, rather than presenting one arbitrary owner.
		var oe *testcase.OwnershipError
		if errors.As(err, &oe) {
			output.Errorf(oe.Error(), "")
			return output.AsDisplayed(err)
		}
		return err
	}
	// BUG-193 / REV-170 CODEX-001: see runStatusOverview.
	explicitFramework := ""
	if strictFramework {
		explicitFramework = defaultFramework
	}

	if jsonOut {
		// Per-TC JSON always exposes triage (no presentation flag). A --triage
		// filter narrows attachment to matching active records; a non-matching
		// single-test-case view returns the object WITHOUT a triage member
		// rather than an empty collection (tc-cd5d4d75 step 7).
		if err := reader.AttachTriageToDetail(projectRoot, detail, triageValues, triageFilter, explicitFramework); err != nil {
			return err
		}
		return writeStatusDetailJSON(w, detail)
	}

	// Human output. Attach triage only when it will be shown (showTriage already
	// ORs an active --triage filter), so default detail output is byte-identical.
	if showTriage {
		if err := reader.AttachTriageToDetail(projectRoot, detail, triageValues, triageFilter, explicitFramework); err != nil {
			return err
		}
	}
	// ENH-202: a --triage filter that matches nothing is a successful, empty
	// result -- print the diagnostic and stop before rendering the record body
	// (tc-cd5d4d75 step 4).
	if triageFilter != "" && selectedDetailTriage(detail) == nil {
		fmt.Fprintf(w, "No test cases match triage value '%s'.\n", triageFilter)
		return nil
	}

	// Header
	header := formatTestCaseColumn(detail.TestCaseID, detail.Slug) + ": " + detail.Title
	fmt.Fprintln(w, header)
	fmt.Fprintln(w, strings.Repeat("\u2500", len(header)))

	// Stage statuses -- per-stage timestamps are appended inside the paren
	// clause via appendDate (which skips on em-dash / empty inputs).
	createLabel := appendDate(formatDetailLabel(detail.CreateStatus, detail.Requirement), detail.CreateDate)
	fmt.Fprintf(w, "CREATE:     %s (%s)\n",
		formatStageStatus(detail.CreateStatus),
		createLabel)

	autoLabel := appendDate(formatDetailLabel(detail.AutomateStatus, detail.Framework), detail.AutomateDate)
	// ENH-134: append manual-coverage sub-state to the AUTOMATE label so the
	// detail view shows "Manual testing (prepared)" or "Manual testing (recorded)".
	if detail.ManualCoverage != "" && detail.AutomateStatus == "manual" {
		autoLabel += " (" + detail.ManualCoverage + ")"
	}
	fmt.Fprintf(w, "AUTOMATE:   %s (%s)\n",
		formatStageStatus(detail.AutomateStatus),
		autoLabel)

	execLabel := formatExecuteLabel(detail.ExecuteStatus, detail.LastResult, detail.LastResultDate)
	// Timestamp goes BEFORE the framework-bracket append so the output
	// reads "Pass, 2026-04-16 14:32 UTC [bats]" -- substring matches on
	// "[bats]" / "fail [playwright]" stay intact.
	execLabel = appendDate(execLabel, formatRunAt(detail.LastRunAt))
	if detail.Framework != "" && detail.LastResult != "none" && detail.LastResult != "" {
		execLabel += " [" + detail.Framework + "]"
	}
	// BUG-162 Symptom A: the per-TC detail EXECUTE line is RESULT-AWARE (ADR-021
	// amendment). A cleanly completed run whose test outcome is `fail` must render
	// ✗ beside the "Fail" label, not ✓ -- folding LastResult the same way `gtms map`
	// does, via the shared output.ExecuteResultIcon. The orthogonal ENH-130 data is
	// untouched (ExecuteStatus stays "complete" for a fail). The status TABLE/overview
	// (runStatusOverview) stays STATUS-TIED per BUG-078 and is NOT changed.
	execIcon := formatExecuteStageIcon(detail.ExecuteStatus, detail.LastResult)
	if detail.Stale {
		execIcon += " " + output.IconWarning
		execLabel += " -- script modified since last run"
	}
	fmt.Fprintf(w, "EXECUTE:    %s (%s)\n", execIcon, execLabel)

	// BUG-079: drift detection line under EXECUTE when present.
	// Raw RFC3339 timestamp (not formatRunAt) so it stays byte-comparable
	// with drift_detected_at in JSON and the manual result file.
	if detail.DriftDetected {
		driftTime := detail.DriftDetectedAt
		if driftTime == "" {
			driftTime = "unknown"
		}
		fmt.Fprintf(w, "  Drift detected: %s\n", driftTime)
	}

	// ENH-191: Runner provenance line for wiring-derived entries with an
	// execution result. Shows the adapter that actually ran, and appends the
	// wired default when they differ.
	//
	// REV-105 CLAUDE-001: scope this to the framework the EXECUTE line above
	// represents (detail.Framework). detail.Frameworks holds one entry per
	// wiring record -- all frameworks, unfiltered -- so on a multi-wired TC
	// viewed with a --framework selector the unscoped loop rendered a second,
	// unlabeled Runner line for the OTHER framework's runner directly under
	// the selected framework's EXECUTE result, misattributing provenance. The
	// EXECUTE line is already scoped to detail.Framework; the Runner line must
	// match it so exactly one runner is shown, for the result on display.
	for _, fe := range detail.Frameworks {
		if fe.Framework != detail.Framework {
			continue
		}
		if !fe.Wired || fe.LastRunAdapter == "" {
			continue
		}
		if fe.WiredAdapter != "" && fe.WiredAdapter != fe.LastRunAdapter {
			fmt.Fprintf(w, "  Runner: %s (wired default: %s)\n", fe.LastRunAdapter, fe.WiredAdapter)
		} else {
			fmt.Fprintf(w, "  Runner: %s\n", fe.LastRunAdapter)
		}
	}

	// ENH-202: triage block beneath EXECUTE, under --show-triage / --triage.
	if showTriage {
		renderTriageDetail(w, selectedDetailTriage(detail))
	}

	fmt.Fprintln(w)

	// File paths
	if detail.ArtefactPath != "" {
		fmt.Fprintf(w, "Automation: %s\n", detail.ArtefactPath)
	}
	if detail.LastRunPath != "" {
		fmt.Fprintf(w, "Last run:   %s\n", detail.LastRunPath)
	}

	// ENH-077/ENH-123: diagnostic notes block rendered under the file-paths
	// section when the most recent execute outcome is fail or error AND the
	// committed automation record carries a non-empty notes payload. Passing
	// runs, never-run TCs, and records without notes content render nothing.
	if detail.Notes != "" && (detail.LastResult == "fail" || detail.LastResult == "error") {
		fmt.Fprintln(w)
		header := "Notes:"
		if detail.NotesSpill != "" {
			header = fmt.Sprintf("Notes:  (truncated to 64 KB \u2014 full output at %s)", detail.NotesSpill)
		}
		fmt.Fprintln(w, header)
		for _, line := range strings.Split(strings.TrimRight(detail.Notes, "\n"), "\n") {
			fmt.Fprintf(w, "  %s\n", line)
		}
	}

	return nil
}

// selectedDetailTriage returns the triage object attached to the detail's
// SELECTED framework entry, or nil. Detail presentation and filtering key off
// the selected framework so a sibling framework's classification never renders
// or matches (ENH-202 / tc-82eebc80).
func selectedDetailTriage(detail *reader.PipelineDetailEntry) *reader.FrameworkTriage {
	if detail == nil {
		return nil
	}
	if detail.SelectedFramework != "" {
		// Same rule as selectedFrameworkTriage: a selected framework answers
		// for itself and never inherits the entry-level carrier (BUG-193 worktree review, 2026-09-13).
		for i := range detail.Frameworks {
			if detail.Frameworks[i].Framework == detail.SelectedFramework {
				return detail.Frameworks[i].Triage
			}
		}
		return nil
	}
	// BUG-193: manual-only detail carries the manual triage at the test-case level.
	return detail.Triage
}

// renderTriageDetail prints the single-test-case TRIAGE block beneath EXECUTE.
// It shows the stable value (never the label), the optional summary and defect
// (each omitted independently when empty), and a self-explanatory changed-result
// line. A resolved record is treated as untriaged in the human detail and shows
// nothing. tc-23ab0b6b / tc-8ed5c09e.
func renderTriageDetail(w io.Writer, t *reader.FrameworkTriage) {
	if t == nil || t.Status != "active" {
		return
	}
	changed := t.ResultChangedSinceTriage != nil && *t.ResultChangedSinceTriage
	line := "TRIAGE:     " + t.Value
	if changed {
		line += " " + output.IconWarning
	}
	fmt.Fprintln(w, line)
	// ENH-209: summary and defect are persisted record content reaching the
	// terminal at read time, so harden this reference renderer with the same
	// shared control-character sanitiser the triage --show/--list surfaces use.
	// Ordinary prose is unchanged (the sanitiser is a no-op on clean text);
	// status JSON is untouched.
	if t.Summary != "" {
		fmt.Fprintf(w, "  Summary:  %s\n", pathsafe.SanitizeControlChars(t.Summary))
	}
	if t.Defect != "" {
		fmt.Fprintf(w, "  Defect:   %s\n", pathsafe.SanitizeControlChars(t.Defect))
	}
	switch {
	case changed:
		// Name both results so the warning is self-explanatory in place.
		fmt.Fprintf(w, "  Snapshot: %s; latest result: %s\n", t.Snapshot.Result, t.LatestResult)
	case t.ResultChangedSinceTriage == nil:
		// No latest terminal result to compare -- state it rather than implying
		// the result is unchanged.
		fmt.Fprintf(w, "  Snapshot: %s; latest result: unavailable\n", t.Snapshot.Result)
	}
	if changed {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "%s = result changed since triage\n", output.IconWarning)
	}
}

// formatTestCaseColumn returns the test case ID with its slug appended if available.
// e.g. "tc-a1b2c3d  login-happy" or just "tc-a1b2c3d" when slug is empty.
func formatTestCaseColumn(id, slug string) string {
	if slug == "" {
		return id
	}
	return id + "  " + slug
}

// formatStageStatus returns the status icon for a pipeline stage.
func formatStageStatus(status string) string {
	switch status {
	case "complete":
		return output.IconComplete
	case "in-progress":
		return output.IconInProgress
	case "pending":
		return output.IconPending
	case "developed":
		return output.IconComplete
	case "error":
		return output.IconError
	case "skipped":
		return output.IconSkipped
	case "manual":
		return "manual"
	case "none":
		return "\u2014" // em dash
	default:
		return "\u2014"
	}
}

// formatExecuteStageIcon returns the EXECUTE icon for the per-TC detail view,
// folding the orthogonal ENH-130 test result into the glyph (BUG-162 Symptom A,
// ADR-021 amendment). Lifecycle states keep status-detail's own front-half:
// in-progress -> ●, pending(+no result) -> ○, and lifecycle error -> ✗
// (status detail's error glyph; `gtms map` uses ⚠ for error and that divergence
// is intentional and out of scope). Terminal results (pass/fail/skipped) route
// through the SHARED output.ExecuteResultIcon so detail and map cannot diverge.
//
// This is the detail-view derivation ONLY. The status table/overview
// (runStatusOverview) deliberately stays status-tied via formatStageStatus
// (BUG-078) and must not call this.
func formatExecuteStageIcon(executeStatus, lastResult string) string {
	switch {
	case executeStatus == "in-progress":
		return output.IconInProgress
	case executeStatus == "error":
		return output.IconError
	case executeStatus == "pending" && (lastResult == "none" || lastResult == ""):
		return output.IconPending
	default:
		return output.ExecuteResultIcon(lastResult)
	}
}

// formatLastResult formats the last result column.
// When framework is non-empty, it appends [framework] after the result.
func formatLastResult(result, date, framework string) string {
	if result == "none" || result == "" {
		return "\u2014" // em dash
	}
	// "error", "pass", "fail" all display as their text value
	s := result
	if date != "" {
		s = fmt.Sprintf("%s (%s)", result, date)
	}
	if framework != "" {
		s += " [" + framework + "]"
	}
	return s
}

// formatDetailLabel returns a context label for a stage in detail view.
func formatDetailLabel(status, info string) string {
	switch status {
	case "complete", "developed":
		label := "Complete"
		if info != "" {
			label = info
		}
		return label
	case "in-progress":
		return "In progress"
	case "pending":
		return "Pending"
	case "error":
		return "Error"
	case "manual":
		return "Manual testing"
	default:
		return "\u2014"
	}
}

// formatExecuteLabel returns the execute stage label for detail view.
func formatExecuteLabel(executeStatus, result, date string) string {
	// Error execute status shows "Error" (infrastructure/automation problem)
	if executeStatus == "error" || result == "error" {
		return "Error"
	}
	// Runtime-skipped shows "Skipped"
	if result == "skipped" {
		return "Skipped"
	}
	if result == "none" || result == "" {
		return "\u2014"
	}
	label := capitalize(result)
	if date != "" {
		label = fmt.Sprintf("%s (%s)", label, date)
	}
	return label
}

// capitalize returns the string with its first letter upper-cased.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// appendDate appends a timestamp/date to an existing detail-view label
// (the text inside the parens of a CREATE / AUTOMATE / EXECUTE line),
// rendered as ", <value>" to keep the view compact.
//
// Skip rules:
//   - If the label is empty or an em-dash ("--"), do not append -- the stage
//     has never run and a trailing date would be misleading.
//   - If the date is empty, do not append.
func appendDate(label, date string) string {
	if label == "" || label == "\u2014" || date == "" {
		return label
	}
	return label + ", " + date
}

// formatRunAt renders an ISO 8601 (RFC3339) UTC timestamp as a compact
// human-readable form for the EXECUTE line: "YYYY-MM-DD HH:MM UTC".
// Returns "" for empty input or parse failure (appendDate will skip the
// append in that case).
func formatRunAt(iso string) string {
	if iso == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return ""
	}
	return t.UTC().Format("2006-01-02 15:04 UTC")
}

// writeStatusJSON outputs the pipeline entries as indented JSON.
func writeStatusJSON(w io.Writer, entries []reader.PipelineEntry) error {
	if entries == nil {
		entries = []reader.PipelineEntry{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(entries)
}

// writeStatusDetailJSON outputs a single pipeline detail entry as indented JSON.
func writeStatusDetailJSON(w io.Writer, detail *reader.PipelineDetailEntry) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(detail)
}
