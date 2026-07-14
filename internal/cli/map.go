package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/output"
	"github.com/aitestmanagement/gtms-cli/internal/reader"
)

func newMapCmd() *cobra.Command {
	var detail bool
	var jsonOut bool
	var recursive bool
	var framework string

	cmd := &cobra.Command{
		Use:   "map [test-case-id | folder]",
		Short: "Show test cases grouped by requirement",
		Long: `Show which test cases cover each requirement and their pipeline progress.

Unlike 'status' (which summarises folders and lists test cases when scoped) or
'gaps' (which finds what's missing), 'map' groups test cases by the requirement
they trace to -- answering "what am I actually testing?"

Requirements come from each test case's 'requirement:' frontmatter field. Test
cases without one appear in an UNLINKED TEST CASES section and are counted in
the summary.

  gtms map                      -- slug view, all requirements
  gtms map bug-022              -- scoped to gtms/test/cases/bug-022/
  gtms map --detail             -- full titles, all requirements
  gtms map tc-a1b2c3d4          -- full detail for one test case in its requirement group
  gtms map --json               -- machine-readable JSON output
  gtms map bug-022 -r           -- folder scope, including subdirectories
  gtms map --framework bats     -- show one framework's pipeline state`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetProjectRoot()
			cfg := GetConfig()

			// BUG-128 v2: non-fatal "did you mean" hint (stderr only).
			if hint := frameworkHint(framework, knownFrameworks(cfg, root)); hint != "" {
				fmt.Fprintln(os.Stderr, hint)
			}

			defaultFw := config.DefaultFramework(cfg)
			if framework != "" {
				defaultFw = framework
			}
			// ENH-082: explicit --framework enables strict per-TC selection;
			// config-default fallback is unchanged.
			strict := framework != ""

			if len(args) > 0 {
				arg := strings.ToLower(args[0])
				arg = normaliseTarget(arg)
				if isTestCaseID(arg) {
					// TC ID → detail view (no scope)
					return runMap(os.Stdout, root, nil, detail, arg, jsonOut, defaultFw, strict)
				}
				// Not a TC ID → treat as folder scope for overview
				folder, err := validateFolderArg(arg)
				if err != nil {
					output.Errorf(err.Error(), "")
					return output.AsDisplayed(err)
				}
				scope := buildScopeFromArg(root, folder, recursive)
				return runMap(os.Stdout, root, scope, detail, "", jsonOut, defaultFw, strict)
			}

			// No arg → full recursive scan (scope=nil)
			return runMap(os.Stdout, root, nil, detail, "", jsonOut, defaultFw, strict)
		},
	}

	cmd.Flags().BoolVar(&detail, "detail", false, "Show full titles in two-line format")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "Include test cases from subdirectories")
	cmd.Flags().StringVar(&framework, "framework", "", "Select one framework per test case (non-matching test cases show as not-set)")

	return cmd
}

// runMap displays the traceability map in the requested format.
// ENH-082: strictFramework is true when the user supplied --framework explicitly,
// causing per-TC entries to render em-dashes for TCs without a matching wiring
// record instead of falling back to a different framework.
func runMap(w io.Writer, projectRoot string, scope *reader.ScopeInfo, detail bool, detailID string, jsonOut bool, defaultFramework string, strictFramework bool) error {
	report, err := reader.Map(projectRoot, scope, defaultFramework, strictFramework)
	if err != nil {
		return err
	}

	// JSON mode: always output valid JSON, even if empty.
	// BUG-081: when a TC ID was supplied, honour it on the JSON path too --
	// mirror the text-mode group-preserving semantic from writeMapSingleTC.
	if jsonOut {
		if detailID != "" {
			return writeMapSingleTCJSON(w, report, detailID)
		}
		return writeMapJSON(w, report)
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

	// Empty project check (non-JSON modes only)
	if len(report.Groups) == 0 && len(report.Unlinked) == 0 {
		fmt.Fprintln(w, "No test cases found.")
		fmt.Fprintln(w)
		output.Dimln(w, "Next: gtms create <folder> --reference <doc>  \u2014 create your first test cases")
		return nil
	}

	// Single test case detail view
	if detailID != "" {
		return writeMapSingleTC(w, report, detailID)
	}

	// Detail view (all)
	if detail {
		return writeMapDetailAll(w, report)
	}

	// Default slug view
	return writeMapDefault(w, report)
}

// writeMapJSON outputs the MapReport as indented JSON.
func writeMapJSON(w io.Writer, report *reader.MapReport) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

// writeMapSingleTCJSON emits a TC-scoped, group-preserving filtered MapReport.
// BUG-081: mirrors the text-mode semantic of writeMapSingleTC -- when the TC
// belongs to a requirement group, the group is preserved with all sibling
// TCs intact; when the TC is unlinked, only that entry is returned. Unknown
// TC IDs error out (no silent empty payload).
func writeMapSingleTCJSON(w io.Writer, report *reader.MapReport, detailID string) error {
	for _, grp := range report.Groups {
		for _, entry := range grp.TestCases {
			if entry.TestCaseID == detailID {
				filtered := &reader.MapReport{
					Groups:   []reader.RequirementGroup{grp},
					Unlinked: []reader.MapEntry{},
					Summary: reader.MapSummary{
						TotalRequirements: 1,
						TotalTestCases:    len(grp.TestCases),
						Automated:         countAutomated(grp.TestCases),
						Executed:          countExecuted(grp.TestCases),
						UnlinkedCount:     0,
					},
				}
				return writeMapJSON(w, filtered)
			}
		}
	}
	for _, entry := range report.Unlinked {
		if entry.TestCaseID == detailID {
			single := []reader.MapEntry{entry}
			filtered := &reader.MapReport{
				Groups:   []reader.RequirementGroup{},
				Unlinked: single,
				Summary: reader.MapSummary{
					TotalRequirements: 0,
					TotalTestCases:    1,
					Automated:         countAutomated(single),
					Executed:          countExecuted(single),
					UnlinkedCount:     1,
				},
			}
			return writeMapJSON(w, filtered)
		}
	}
	// BUG-081: not-found is an argument-validation error, not render output --
	// emit to stderr so JSON consumers can `2>/dev/null | jq` cleanly, in
	// line with the rest of the GTMS CLI's argument-error convention.
	output.Errorf(fmt.Sprintf("Test case %s not found.", detailID), "")
	return output.AsDisplayed(fmt.Errorf("test case %s not found", detailID))
}

// countAutomated mirrors reader.Map summary logic exactly: an entry counts as
// automated when its AutomateStatus is "complete" or "developed". Keeping this
// in lockstep with internal/reader/map.go ensures the filtered summary block
// matches what the full-project summary would have reported for the same set.
func countAutomated(entries []reader.MapEntry) int {
	n := 0
	for _, e := range entries {
		if e.AutomateStatus == "complete" || e.AutomateStatus == "developed" {
			n++
		}
	}
	return n
}

// countExecuted mirrors reader.Map summary logic: an entry counts as executed
// when LastResult is anything other than "none" (pass / fail / error / skipped).
func countExecuted(entries []reader.MapEntry) int {
	n := 0
	for _, e := range entries {
		if e.LastResult != "none" {
			n++
		}
	}
	return n
}

// writeMapDefault renders the default slug-based view.
func writeMapDefault(w io.Writer, report *reader.MapReport) error {
	slugWidth := maxSlugWidth(report)

	fmt.Fprintln(w, "TRACEABILITY MAP")
	fmt.Fprintln(w)

	for i, grp := range report.Groups {
		fmt.Fprintf(w, "%s (%s)\n", grp.Requirement, pluralTestCases(len(grp.TestCases)))
		for _, entry := range grp.TestCases {
			writeSlugEntry(w, entry, slugWidth)
		}
		if i < len(report.Groups)-1 {
			fmt.Fprintln(w)
		}
	}

	// Unlinked section (only show when there are unlinked test cases)
	if len(report.Unlinked) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "UNLINKED TEST CASES (no requirement in frontmatter)")
		for _, entry := range report.Unlinked {
			writeSlugEntry(w, entry, slugWidth)
		}
	}

	writeKeyAndSummary(w, report)
	return nil
}

// writeSlugEntry writes one test case line in the default slug view.
func writeSlugEntry(w io.Writer, entry reader.MapEntry, slugWidth int) {
	slug := entry.Slug
	if slug == "" {
		slug = entry.TestCaseID
	}
	execIcon := formatExecuteIcon(entry)
	// BUG-079: append drift text marker when manual result has drifted.
	if entry.DriftDetected {
		execIcon += " [drift]"
	}
	fmt.Fprintf(w, "  %s  %-*s  CREATE %s  AUTOMATE %s  EXECUTE %s\n",
		entry.TestCaseID,
		slugWidth, slug,
		formatMapStageIcon(entry.CreateStatus),
		formatMapStageIcon(entry.AutomateStatus),
		execIcon,
	)
}

// maxSlugWidth computes the widest slug across all entries in the report.
func maxSlugWidth(report *reader.MapReport) int {
	w := 0
	for _, grp := range report.Groups {
		for _, e := range grp.TestCases {
			s := e.Slug
			if s == "" {
				s = e.TestCaseID
			}
			if len(s) > w {
				w = len(s)
			}
		}
	}
	for _, e := range report.Unlinked {
		s := e.Slug
		if s == "" {
			s = e.TestCaseID
		}
		if len(s) > w {
			w = len(s)
		}
	}
	return w
}

// writeMapDetailAll renders the detail view with full titles for all test cases.
func writeMapDetailAll(w io.Writer, report *reader.MapReport) error {
	fmt.Fprintln(w, "TRACEABILITY MAP")
	fmt.Fprintln(w)

	for i, grp := range report.Groups {
		fmt.Fprintf(w, "%s (%s)\n", grp.Requirement, pluralTestCases(len(grp.TestCases)))
		for _, entry := range grp.TestCases {
			writeDetailEntry(w, "  ", entry)
		}
		if i < len(report.Groups)-1 {
			fmt.Fprintln(w)
		}
	}

	// Unlinked section (only show when there are unlinked test cases)
	if len(report.Unlinked) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "UNLINKED TEST CASES (no requirement in frontmatter)")
		for _, entry := range report.Unlinked {
			writeDetailEntry(w, "  ", entry)
		}
	}

	writeKeyAndSummary(w, report)
	return nil
}

// writeMapSingleTC renders a single test case in its requirement group context.
func writeMapSingleTC(w io.Writer, report *reader.MapReport, detailID string) error {
	// Search all groups for the target test case
	for _, grp := range report.Groups {
		for _, entry := range grp.TestCases {
			if entry.TestCaseID == detailID {
				fmt.Fprintf(w, "TRACEABILITY MAP \u2014 %s\n", detailID)
				fmt.Fprintln(w)
				fmt.Fprintf(w, "%s (%s)\n", grp.Requirement, pluralTestCases(len(grp.TestCases)))
				for _, e := range grp.TestCases {
					prefix := "  "
					if e.TestCaseID == detailID {
						prefix = "\u2192 "
					}
					writeDetailEntry(w, prefix, e)
				}
				writeKey(w)
				return nil
			}
		}
	}

	// Search unlinked
	for _, entry := range report.Unlinked {
		if entry.TestCaseID == detailID {
			fmt.Fprintf(w, "TRACEABILITY MAP \u2014 %s\n", detailID)
			fmt.Fprintln(w)
			fmt.Fprintln(w, "UNLINKED TEST CASES (no requirement in frontmatter)")
			for _, e := range report.Unlinked {
				prefix := "  "
				if e.TestCaseID == detailID {
					prefix = "\u2192 "
				}
				writeDetailEntry(w, prefix, e)
			}
			writeKey(w)
			return nil
		}
	}

	// BUG-081 (extended to text mode): the not-found error was previously
	// written to the passed writer (= os.Stdout from runMap). Move it to
	// stderr so it matches the JSON branch above and the rest of the CLI's
	// argument-error convention.
	output.Errorf(fmt.Sprintf("Test case %s not found.", detailID), "")
	return output.AsDisplayed(fmt.Errorf("test case %s not found", detailID))
}

// writeDetailEntry writes a two-line detail entry for a test case.
func writeDetailEntry(w io.Writer, prefix string, entry reader.MapEntry) {
	title := entry.Title
	if title == "" {
		title = entry.Slug
	}
	fmt.Fprintf(w, "%s%s  %s\n", prefix, entry.TestCaseID, title)
	indent := strings.Repeat(" ", len(entry.TestCaseID)+4)
	execIcon := formatExecuteIcon(entry)
	// BUG-079: append drift text marker when manual result has drifted.
	if entry.DriftDetected {
		execIcon += " [drift]"
	}
	fmt.Fprintf(w, "%sCREATE %s  AUTOMATE %s  EXECUTE %s\n",
		indent,
		formatMapStageIcon(entry.CreateStatus),
		formatMapStageIcon(entry.AutomateStatus),
		execIcon,
	)
}

// writeKeyAndSummary writes the KEY and SUMMARY lines.
func writeKeyAndSummary(w io.Writer, report *reader.MapReport) {
	writeKey(w)

	s := report.Summary
	autoPct := 0
	execPct := 0
	if s.TotalTestCases > 0 {
		autoPct = s.Automated * 100 / s.TotalTestCases
		execPct = s.Executed * 100 / s.TotalTestCases
	}
	fmt.Fprintf(w, "\nSUMMARY: %s, %s, %d automated (%d%%), %d executed (%d%%), %d unlinked\n",
		pluralRequirements(s.TotalRequirements),
		pluralTestCases(s.TotalTestCases),
		s.Automated, autoPct,
		s.Executed, execPct,
		s.UnlinkedCount,
	)
}

// writeKey writes the symbol key line.
func writeKey(w io.Writer) {
	fmt.Fprintf(w, "\nKEY: %s complete  %s in progress  %s pending  \u2014 not started  %s failed  %s skipped  %s stale\n",
		output.IconComplete, output.IconInProgress, output.IconPending, output.IconError, output.IconSkipped, output.IconWarning)
}

// formatMapStageIcon returns the icon for a pipeline stage status.
// Extends formatStageStatus with "developed" → in-progress mapping.
func formatMapStageIcon(status string) string {
	switch status {
	case "complete":
		return output.IconComplete
	case "in-progress":
		return output.IconInProgress
	case "pending":
		return output.IconPending
	case "developed":
		return output.IconComplete
	case "manual":
		return "manual"
	case "none":
		return "\u2014" // em dash
	default:
		return "\u2014"
	}
}

// formatExecuteIcon returns the EXECUTE column icon, folding in the last result.
// Appends ⚠ when the artefact has been modified since the last execution.
func formatExecuteIcon(entry reader.MapEntry) string {
	// CON-023 / ENH-146 - Phase 3D fix-pass: precedence is explicit at
	// one place. Active task-derived ExecuteStatus values are checked
	// FIRST so applyTaskStatus' final overlay can never be masked by a
	// worst-of-frameworks LastResult bump:
	//
	//   1. ExecuteStatus="in-progress"  -> always wins (active run).
	//   2. ExecuteStatus="error"        -> always wins (task-derived
	//                                      error OR terminal adapter
	//                                      failure).
	//   3. ExecuteStatus="pending"
	//      AND LastResult=="none"       -> pending wins ONLY when no
	//                                      terminal result exists. The
	//                                      important contract sits in
	//                                      applyTaskStatus: a queued
	//                                      pending task must never
	//                                      hide a real terminal result.
	//                                      Expressing the gate here too
	//                                      keeps the renderer defensive
	//                                      and consistent with the data
	//                                      layer.
	//   4. LastResult terminal values   -> pass/fail/error/skipped icons.
	//   5. Default                      -> em-dash (covers
	//                                      ExecuteStatus="none" and any
	//                                      unrecognised state).
	var icon string
	switch {
	// 1-3: active task-derived states.
	case entry.ExecuteStatus == "in-progress":
		icon = output.IconInProgress
	case entry.ExecuteStatus == "error":
		icon = output.IconWarning
	case entry.ExecuteStatus == "pending" && entry.LastResult == "none":
		icon = output.IconPending
	// 4: terminal LastResult.
	case entry.LastResult == "pass":
		icon = output.IconComplete
	case entry.LastResult == "fail":
		icon = output.IconError
	case entry.LastResult == "error":
		// Defensive: legacyExecuteStatus already maps result=error to
		// ExecuteStatus=error so this branch is rarely reached. Kept
		// for direct-from-overlay paths where ExecuteStatus stayed
		// "none" but LastResult bubbled "error" up.
		icon = output.IconWarning
	case entry.LastResult == "skipped":
		// ENH-127: runtime-skipped tests render \u2298 in the EXECUTE column on
		// the traceability map, matching how `gtms status` already renders
		// them. Without this case the LastResult drops through to the
		// em-dash default and a skipped TC mis-displays as "not started".
		icon = output.IconSkipped
	// 5: not started / unrecognised.
	default:
		icon = "\u2014" // em dash
	}
	if entry.Stale {
		icon += " " + output.IconWarning
	}
	return icon
}

// pluralTestCases returns "1 test case" or "N test cases".
func pluralTestCases(n int) string {
	if n == 1 {
		return "1 test case"
	}
	return fmt.Sprintf("%d test cases", n)
}

// pluralRequirements returns "1 requirement" or "N requirements".
func pluralRequirements(n int) string {
	if n == 1 {
		return "1 requirement"
	}
	return fmt.Sprintf("%d requirements", n)
}
