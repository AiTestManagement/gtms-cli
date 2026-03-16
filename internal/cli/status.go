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

func newStatusCmd() *cobra.Command {
	var jsonOut bool
	var recursive bool

	cmd := &cobra.Command{
		Use:   "status [folder-or-tc-id]",
		Short: "Show pipeline overview status",
		Long: `Show the pipeline status of all test cases, or detailed status for a single test case.

  gtms status              — overview of all test cases
  gtms status bug-022      — scoped to test-cases/bug-022/
  gtms status tc-007       — detail view for one test case
  gtms status --json       — machine-readable JSON output
  gtms status -r           — include test cases from subdirectories`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetProjectRoot()

			if len(args) > 0 {
				arg := strings.ToLower(args[0])
				if isTestCaseID(arg) && strings.HasPrefix(arg, "tc-") {
					// TC ID → detail view
					return runStatusDetail(os.Stdout, root, arg, jsonOut)
				}
				// Not a TC ID → treat as folder scope for overview
				folder, err := validateFolderArg(arg)
				if err != nil {
					output.Errorf(err.Error(), "")
					return output.AsDisplayed(err)
				}
				scope := buildScopeFromArg(root, folder, recursive)
				return runStatusOverview(os.Stdout, root, scope, jsonOut)
			}

			// No arg → root-level scope
			scope := buildScopeFromArg(root, "", recursive)
			return runStatusOverview(os.Stdout, root, scope, jsonOut)
		},
	}

	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "Include test cases from subdirectories")

	return cmd
}

// runStatusOverview displays the pipeline overview table for test cases within the scope.
func runStatusOverview(w io.Writer, projectRoot string, scope *reader.ScopeInfo, jsonOut bool) error {
	entries, err := reader.PipelineStatus(projectRoot, scope)
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
		tbl.AddRow(
			formatTestCaseColumn(e.TestCaseID, e.Slug),
			formatStageStatus(e.CreateStatus),
			formatStageStatus(e.AutomateStatus),
			formatStageStatus(e.ExecuteStatus),
			formatLastResult(e.LastResult, e.LastResultDate),
		)
	}

	tbl.Render(w)
	fmt.Fprintf(w, "\nKey: %s = complete/pass  %s = failed/error  %s = in progress  %s = pending  \u2014 = not yet attempted\n",
		output.IconComplete, output.IconError, output.IconInProgress, output.IconPending)
	return nil
}

// runStatusDetail displays detailed status for a single test case.
func runStatusDetail(w io.Writer, projectRoot, testCaseID string, jsonOut bool) error {
	detail, err := reader.PipelineDetail(projectRoot, testCaseID)
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

	// Stage statuses
	fmt.Fprintf(w, "CREATE:     %s (%s)\n",
		formatStageStatus(detail.CreateStatus),
		formatDetailLabel(detail.CreateStatus, detail.Requirement))

	autoLabel := formatDetailLabel(detail.AutomateStatus, detail.Framework)
	fmt.Fprintf(w, "AUTOMATE:   %s (%s)\n",
		formatStageStatus(detail.AutomateStatus),
		autoLabel)

	execLabel := formatExecuteLabel(detail.ExecuteStatus, detail.LastResult, detail.LastResultDate)
	fmt.Fprintf(w, "EXECUTE:    %s (%s)\n",
		formatStageStatus(detail.ExecuteStatus),
		execLabel)

	fmt.Fprintln(w)

	// File paths
	if detail.ArtefactPath != "" {
		fmt.Fprintf(w, "Automation: %s\n", detail.ArtefactPath)
	}
	if detail.LastRunPath != "" {
		fmt.Fprintf(w, "Last run:   %s\n", detail.LastRunPath)
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
	case "failed":
		return output.IconError
	case "none":
		return "\u2014" // em dash
	default:
		return "\u2014"
	}
}

// formatLastResult formats the last result column.
func formatLastResult(result, date string) string {
	if result == "none" || result == "" {
		return "\u2014" // em dash
	}
	if date != "" {
		return fmt.Sprintf("%s (%s)", result, date)
	}
	return result
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
	case "failed":
		return "Failed"
	default:
		return "\u2014"
	}
}

// formatExecuteLabel returns the execute stage label for detail view.
func formatExecuteLabel(executeStatus, result, date string) string {
	// Failed execute task always shows "Failed" — regardless of historical result
	if executeStatus == "failed" {
		return "Failed"
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
