package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/output"
	"github.com/aitestmanagement/gtms-cli/internal/reader"
)

func newStatusCmd() *cobra.Command {
	var jsonOut bool
	var recursive bool
	var framework string

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

			if len(args) > 0 {
				arg := strings.ToLower(args[0])
				arg = normaliseTarget(arg)
				if isTestCaseID(arg) {
					// TC ID → detail view
					return runStatusDetail(os.Stdout, root, arg, jsonOut, defaultFw, strict)
				}
				// Not a TC ID → treat as folder scope for overview
				folder, err := validateFolderArg(arg)
				if err != nil {
					output.Errorf(err.Error(), "")
					return output.AsDisplayed(err)
				}
				scope := buildScopeFromArg(root, folder, recursive)
				return runStatusOverview(os.Stdout, root, scope, jsonOut, defaultFw, strict)
			}

			// No arg: -r → recursive flat list; default → folder summary
			if recursive {
				scope := buildScopeFromArg(root, "", true)
				return runStatusOverview(os.Stdout, root, scope, jsonOut, defaultFw, strict)
			}
			// BUG-082: pass the explicit --framework value, not the
			// config default. When no flag is given, framework == ""
			// and PipelineFolderSummary skips framework filtering,
			// counting ALL non-manual wiring as automated.
			return runStatusFolderSummary(os.Stdout, root, jsonOut, framework)
		},
	}

	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "Include test cases from subdirectories; with no folder, show the flat per test case view")
	cmd.Flags().StringVar(&framework, "framework", "", "Select one framework per test case (non-matching test cases show as not-set)")

	return cmd
}

// runStatusOverview displays the pipeline overview table for test cases within the scope.
// ENH-082: strictFramework is set by the caller when --framework was supplied
// explicitly so per-TC selection skips fallback to a different framework.
func runStatusOverview(w io.Writer, projectRoot string, scope *reader.ScopeInfo, jsonOut bool, defaultFramework string, strictFramework bool) error {
	entries, err := reader.PipelineStatus(projectRoot, scope, defaultFramework, strictFramework)
	if err != nil {
		return err
	}

	if jsonOut {
		return writeStatusJSON(w, entries)
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

	if len(entries) == 0 {
		fmt.Fprintln(w, "No test cases found.")
		return nil
	}

	tbl := output.NewTable("TEST CASE", "CREATE", "AUTOMATE", "EXECUTE", "LAST RESULT")
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
		tbl.AddRow(
			formatTestCaseColumn(e.TestCaseID, e.Slug),
			formatStageStatus(e.CreateStatus),
			formatStageStatus(e.AutomateStatus),
			execCol,
			resultCol,
		)
	}

	tbl.Render(w)
	fmt.Fprintf(w, "\nKey:\n")
	fmt.Fprintf(w, "  %s = complete  %s = error  %s = stale  %s = skipped\n",
		output.IconComplete, output.IconError, output.IconWarning, output.IconSkipped)
	fmt.Fprintf(w, "  %s = in progress    %s = pending  \u2014 = not yet attempted\n",
		output.IconInProgress, output.IconPending)

	if hint := statusHint(entries); hint != "" {
		fmt.Fprintln(w)
		output.Dimln(w, "Next: "+hint)
	}
	return nil
}

// runStatusFolderSummary displays the folder summary view (no-arg default).
//
// ENH-089: cells use icon-forward rendering (✓ ● ○ ✗) with fraction suffix
// only on non-✓ cells. CREATE column always shows ✓ (a TC existing in
// gtms/test/cases/ IS the creation). AUTOMATE/EXECUTE columns apply the
// documented priority rule via the formatFolder*Cell helpers below.
func runStatusFolderSummary(w io.Writer, projectRoot string, jsonOut bool, defaultFramework string) error {
	entries, err := reader.PipelineFolderSummary(projectRoot, defaultFramework)
	if err != nil {
		return err
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
	tbl := output.NewTable("FOLDER", "TC", "CREATE", "AUTOMATE", "EXECUTE", "SKIP")
	for _, e := range entries {
		// TC column carries the raw count plus the optional draft annotation
		// (ENH-066). Draft annotation does NOT duplicate into CREATE.
		tcCol := fmt.Sprintf("%d", e.Created)
		if e.DraftCount == 1 {
			tcCol = fmt.Sprintf("%d (1 draft)", e.Created)
		} else if e.DraftCount > 1 {
			tcCol = fmt.Sprintf("%d (%d drafts)", e.Created, e.DraftCount)
		}
		tbl.AddRow(
			e.Folder,
			tcCol,
			output.IconComplete, // CREATE always ✓
			formatFolderAutomateCell(e),
			formatFolderExecuteCell(e),
			formatFolderSkipCell(e),
		)
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
//         AND no fails / errors.
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
func runStatusDetail(w io.Writer, projectRoot, testCaseID string, jsonOut bool, defaultFramework string, strictFramework bool) error {
	detail, err := reader.PipelineDetail(projectRoot, testCaseID, defaultFramework, strictFramework)
	if err != nil {
		return err
	}

	if jsonOut {
		return writeStatusDetailJSON(w, detail)
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
	execIcon := formatStageStatus(detail.ExecuteStatus)
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
