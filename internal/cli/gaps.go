package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/output"
	"github.com/aitestmanagement/gtms-cli/internal/reader"
)

func newGapsCmd() *cobra.Command {
	var jsonOut bool
	var recursive bool

	cmd := &cobra.Command{
		Use:   "gaps [folder]",
		Short: "Show test coverage gaps",
		Long: `Analyze the project for test coverage gaps across four categories: missing tests, missing automation, never executed, and currently failing.

  gtms gaps              — text output (all test cases)
  gtms gaps bug-022      — scoped to test-cases/bug-022/
  gtms gaps --json       — machine-readable JSON output
  gtms gaps -r           — include test cases from subdirectories`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetProjectRoot()
			cfg := GetConfig()
			specDirs := config.CollectSpecDirs(cfg)

			var folder string
			if len(args) > 0 {
				var err error
				folder, err = validateFolderArg(args[0])
				if err != nil {
					output.Errorf(err.Error(), "")
					return output.AsDisplayed(err)
				}
			}
			scope := buildScopeFromArg(root, folder, recursive)
			return runGaps(os.Stdout, root, specDirs, scope, jsonOut)
		},
	}

	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "Include test cases from subdirectories")

	return cmd
}

// runGaps displays the gap report for test cases within the scope.
func runGaps(w io.Writer, projectRoot string, specDirs []string, scope *reader.ScopeInfo, jsonOut bool) error {
	report, err := reader.Gaps(projectRoot, specDirs, scope)
	if err != nil {
		return err
	}

	if jsonOut {
		return writeGapsJSON(w, report)
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

	if report.TotalGaps() == 0 {
		fmt.Fprintln(w, "No coverage gaps found.")
		return nil
	}

	fmt.Fprintln(w, "GAPS REPORT")
	fmt.Fprintln(w, "\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500")
	fmt.Fprintln(w)

	printGapCategory(w, "Requirements without test cases", report.NoTests)
	printGapCategory(w, "Test cases without automation", report.NoAutomation)
	printGapCategory(w, "Automated but never executed", report.NeverExecuted)
	printGapCategoryWithSince(w, "Currently failing", report.CurrentlyFailing)
	printGapCategory(w, "Spec coverage but no automation record", report.SpecButNoRecord)

	return nil
}

// printGapCategory prints a single gap category with its entries.
func printGapCategory(w io.Writer, title string, entries []reader.GapEntry) {
	fmt.Fprintf(w, "%s: %d\n", title, len(entries))
	for _, e := range entries {
		fmt.Fprintf(w, "  %s  %s\n", e.ID, e.Title)
	}
	fmt.Fprintln(w)
}

// printGapCategoryWithSince prints a gap category where entries may have a Since date.
func printGapCategoryWithSince(w io.Writer, title string, entries []reader.GapEntry) {
	fmt.Fprintf(w, "%s: %d\n", title, len(entries))
	for _, e := range entries {
		if e.Since != "" {
			fmt.Fprintf(w, "  %s  %s (fail since %s)\n", e.ID, e.Title, e.Since)
		} else {
			fmt.Fprintf(w, "  %s  %s\n", e.ID, e.Title)
		}
	}
	fmt.Fprintln(w)
}

// writeGapsJSON outputs the gap report as indented JSON.
// Nil slices are initialized to empty slices so they serialize as [] not null.
func writeGapsJSON(w io.Writer, report *reader.GapReport) error {
	if report.NoTests == nil {
		report.NoTests = []reader.GapEntry{}
	}
	if report.NoAutomation == nil {
		report.NoAutomation = []reader.GapEntry{}
	}
	if report.NeverExecuted == nil {
		report.NeverExecuted = []reader.GapEntry{}
	}
	if report.CurrentlyFailing == nil {
		report.CurrentlyFailing = []reader.GapEntry{}
	}
	if report.SpecButNoRecord == nil {
		report.SpecButNoRecord = []reader.GapEntry{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}
