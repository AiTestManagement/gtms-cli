package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/aitestmanagement/gtms-cli/internal/output"
	"github.com/aitestmanagement/gtms-cli/internal/reader"
)

func newMapCmd() *cobra.Command {
	var detail bool
	var jsonOut bool
	var recursive bool

	cmd := &cobra.Command{
		Use:   "map [folder-or-tc-id]",
		Short: "Show test cases grouped by requirement",
		Long: `Show which test cases cover each requirement and their pipeline progress.

Unlike 'status' (which lists every test case individually) or 'gaps' (which
finds what's missing), 'map' groups test cases by the requirement they trace
to — answering "what am I actually testing?"

  gtms map                      — slug view, all requirements
  gtms map bug-022              — scoped to test-cases/bug-022/
  gtms map --detail             — full titles, all requirements
  gtms map tc-a1b2c3d           — full detail for one test case in its requirement group
  gtms map --json               — machine-readable JSON output
  gtms map -r                   — include test cases from subdirectories`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetProjectRoot()

			if len(args) > 0 {
				arg := strings.ToLower(args[0])
				if isTestCaseID(arg) && strings.HasPrefix(arg, "tc-") {
					// TC ID → detail view (no scope)
					return runMap(os.Stdout, root, nil, detail, arg, jsonOut)
				}
				// Not a TC ID → treat as folder scope for overview
				folder, err := validateFolderArg(arg)
				if err != nil {
					output.Errorf(err.Error(), "")
					return output.AsDisplayed(err)
				}
				scope := buildScopeFromArg(root, folder, recursive)
				return runMap(os.Stdout, root, scope, detail, "", jsonOut)
			}

			// No arg → root-level scope
			scope := buildScopeFromArg(root, "", recursive)
			return runMap(os.Stdout, root, scope, detail, "", jsonOut)
		},
	}

	cmd.Flags().BoolVar(&detail, "detail", false, "Show full titles in two-line format")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output raw MapReport as JSON")
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "Include test cases from subdirectories")

	return cmd
}

// runMap displays the traceability map in the requested format.
func runMap(w io.Writer, projectRoot string, scope *reader.ScopeInfo, detail bool, detailID string, jsonOut bool) error {
	report, err := reader.Map(projectRoot, scope)
	if err != nil {
		return err
	}

	// JSON mode: always output valid JSON, even if empty
	if jsonOut {
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
	fmt.Fprintf(w, "  %s  %-*s  CREATE %s  AUTOMATE %s  EXECUTE %s\n",
		entry.TestCaseID,
		slugWidth, slug,
		formatMapStageIcon(entry.CreateStatus),
		formatMapStageIcon(entry.AutomateStatus),
		formatExecuteIcon(entry),
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

	output.FprintError(w, fmt.Sprintf("Test case %s not found.", detailID), "")
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
	fmt.Fprintf(w, "%sCREATE %s  AUTOMATE %s  EXECUTE %s\n",
		indent,
		formatMapStageIcon(entry.CreateStatus),
		formatMapStageIcon(entry.AutomateStatus),
		formatExecuteIcon(entry),
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
	fmt.Fprintf(w, "\nKEY: %s complete  %s in progress  %s pending  \u2014 not started  %s failed\n",
		output.IconComplete, output.IconInProgress, output.IconPending, output.IconError)
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
	case "none":
		return "\u2014" // em dash
	default:
		return "\u2014"
	}
}

// formatExecuteIcon returns the EXECUTE column icon, folding in the last result.
func formatExecuteIcon(entry reader.MapEntry) string {
	switch {
	case entry.LastResult == "pass":
		return output.IconComplete
	case entry.LastResult == "fail":
		return output.IconError
	case entry.ExecuteStatus == "in-progress":
		return output.IconInProgress
	case entry.ExecuteStatus == "pending":
		return output.IconPending
	case entry.ExecuteStatus == "none":
		return "\u2014" // em dash
	default:
		return "\u2014"
	}
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
