package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/output"
	"github.com/aitestmanagement/gtms-cli/internal/reader"
)

func newGapsCmd() *cobra.Command {
	var jsonOut bool
	var recursive bool
	var framework string

	cmd := &cobra.Command{
		Use:   "gaps [folder]",
		Short: "Show test coverage gaps",
		Long: `Analyse the project for test coverage gaps across the wiring-aware
categories: missing automation, currently failing,
execution errors, runtime-skipped, stale wiring (testcase / artefact),
missing artefacts, and manual test cases with spec drift.

  gtms gaps              -- folder summary table (per-folder gap counts)
  gtms gaps bug-022      -- scoped to gtms/test/cases/bug-022/ (full category breakdown)
  gtms gaps tc-a1b2c3d4  -- error (positional arg is a folder; use 'gtms status tc-...' for per-TC views)
  gtms gaps --json       -- machine-readable JSON output
  gtms gaps -r           -- include test cases from subdirectories
  gtms gaps --framework bats  -- show one framework's coverage`,
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

			if len(args) == 0 {
				// No arg: -r → recursive flat list; default → folder summary
				if recursive {
					scope := buildScopeFromArg(root, "", true)
					return runGaps(os.Stdout, root, scope, jsonOut, defaultFw, strict)
				}
				// BUG-082: pass the explicit --framework value, not the
				// config default. When no flag is given, framework == ""
				// and GapsFolderSummary skips framework filtering,
				// counting ALL non-manual wiring as automated.
				return runGapsFolderSummary(os.Stdout, root, jsonOut, framework)
			}

			// BUG-081: existence-first guard against TC-ID-shaped args.
			// isTestCaseID is a loose prefix check, so a legitimate folder named
			// e.g. "tc-regression" must still scope normally. Only reject when
			// the arg is TC-shaped AND no folder of that name exists.
			normalised := normaliseTarget(args[0])
			if isTestCaseID(normalised) {
				folderPath := filepath.Join(layout.TestCasesDir(root), normalised)
				if info, statErr := os.Stat(folderPath); statErr != nil || !info.IsDir() {
					output.Errorf("argument must be a folder, not a TC ID",
						"Use 'gtms status "+normalised+"' for per-TC detail.")
					return output.AsDisplayed(fmt.Errorf("argument must be a folder, not a TC ID"))
				}
			}

			folder, err := validateFolderArg(args[0])
			if err != nil {
				output.Errorf(err.Error(), "")
				return output.AsDisplayed(err)
			}
			scope := buildScopeFromArg(root, folder, recursive)
			return runGaps(os.Stdout, root, scope, jsonOut, defaultFw, strict)
		},
	}

	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "Include test cases from subdirectories")
	cmd.Flags().StringVar(&framework, "framework", "", "Select one framework per test case (non-matching test cases show as not-set)")

	return cmd
}

// runGapsFolderSummary displays the folder summary view for gaps (no-arg default).
func runGapsFolderSummary(w io.Writer, projectRoot string, jsonOut bool, defaultFramework string) error {
	entries, err := reader.GapsFolderSummary(projectRoot, defaultFramework)
	if err != nil {
		return err
	}

	if jsonOut {
		return writeGapsFolderJSON(w, entries)
	}

	if len(entries) == 0 {
		fmt.Fprintln(w, "No test cases found.")
		fmt.Fprintln(w)
		output.Dimln(w, "Next: gtms create <folder> --reference <doc>  \u2014 create your first test cases")
		return nil
	}

	// CON-023 / ENH-146: "Not run here" is not a gap -- fresh clones legitimately
	// have all wiring un-run locally. The NOT EXECUTED column is therefore
	// dropped from the folder-summary human surface. The `not_executed`
	// JSON field is kept on GapsFolderSummaryEntry for shape stability but
	// is no longer populated.
	tbl := output.NewTable("FOLDER", "CREATED", "NOT AUTOMATED", "FAILING")
	for _, e := range entries {
		tbl.AddRow(
			e.Folder,
			fmt.Sprintf("%d", e.Created),
			fmt.Sprintf("%d", e.NotAutomated),
			fmt.Sprintf("%d", e.Failing),
		)
	}
	tbl.Render(w)

	fmt.Fprintln(w)
	output.Dimln(w, "Next: gtms gaps <folder>  \u2014 see detailed gap breakdown")
	return nil
}

// writeGapsFolderJSON outputs the gaps folder summary as indented JSON.
func writeGapsFolderJSON(w io.Writer, entries []reader.GapsFolderSummaryEntry) error {
	if entries == nil {
		entries = []reader.GapsFolderSummaryEntry{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(entries)
}

// runGaps displays the gap report for test cases within the scope.
// ENH-082: strictFramework is true when the user supplied --framework explicitly,
// causing per-TC categorisation to skip TCs without a matching framework record.
func runGaps(w io.Writer, projectRoot string, scope *reader.ScopeInfo, jsonOut bool, defaultFramework string, strictFramework bool) error {
	report, err := reader.Gaps(projectRoot, scope, defaultFramework, strictFramework)
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

	// Distinguish empty project from fully covered
	if report.TotalGaps() == 0 {
		if report.TotalTestCases == 0 {
			fmt.Fprintln(w, "No test cases found.")
			fmt.Fprintln(w)
			output.Dimln(w, "Next: gtms create <folder> --reference <doc>  \u2014 create your first test cases")
		} else {
			fmt.Fprintln(w, "No coverage gaps found.")
		}
		return nil
	}

	fmt.Fprintln(w, "GAPS REPORT")
	fmt.Fprintln(w, "\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500")
	fmt.Fprintln(w)

	// CON-023 / ENH-146 wiring-aware category set. Retired prints
	// ("Automated but never executed", "Spec coverage but no automation
	// record", "Stale execution results") removed -- the underlying GapReport
	// fields are Go-internal carriers (json:"-") with no populated data.
	// BUG-138: "Requirements without test cases" retired -- the NoTests
	// category was never populated (no requirements-inventory source).
	// The JSON "no_tests" key is kept for shape stability (see writeGapsJSON).
	printGapCategory(w, "Test cases without automation", report.NoAutomation)
	printGapCategoryWithSince(w, "Currently failing", report.CurrentlyFailing)
	printGapCategory(w, "Execution errors", report.ExecutionErrors)
	printGapCategory(w, "Runtime-skipped tests", report.RuntimeSkipped)
	printGapCategory(w, "Stale wiring (testcase)", report.StaleTestCaseHash)
	printGapCategory(w, "Stale wiring (artefact)", report.StaleArtefactHash)
	printGapCategory(w, "Missing artefacts", report.MissingArtefact)
	printGapCategory(w, "Manual results with TC drift", report.DriftDetected)

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
// CON-023 / ENH-146 retired SpecButNoRecord / NeverExecuted / StaleExecution
// (json:"-" carriers -- not normalized here).
func writeGapsJSON(w io.Writer, report *reader.GapReport) error {
	// BUG-138: NoTests retired from the human surface but kept in JSON
	// for shape stability (always []).
	if report.NoTests == nil {
		report.NoTests = []reader.GapEntry{}
	}
	if report.NoAutomation == nil {
		report.NoAutomation = []reader.GapEntry{}
	}
	if report.CurrentlyFailing == nil {
		report.CurrentlyFailing = []reader.GapEntry{}
	}
	if report.ExecutionErrors == nil {
		report.ExecutionErrors = []reader.GapEntry{}
	}
	if report.RuntimeSkipped == nil {
		report.RuntimeSkipped = []reader.GapEntry{}
	}
	if report.StaleTestCaseHash == nil {
		report.StaleTestCaseHash = []reader.GapEntry{}
	}
	if report.StaleArtefactHash == nil {
		report.StaleArtefactHash = []reader.GapEntry{}
	}
	if report.MissingArtefact == nil {
		report.MissingArtefact = []reader.GapEntry{}
	}
	if report.DriftDetected == nil {
		report.DriftDetected = []reader.GapEntry{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}
