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

// newTriageCmd builds the 'gtms triage' command with its flags.
func newTriageCmd() *cobra.Command {
	var (
		automationWrong bool
		testWrong       bool
		appWrong        bool
		summary         string
		defect          string
		jsonOut         bool
	)

	cmd := &cobra.Command{
		Use:   "triage <test-case-ID>",
		Short: "Classify and triage test failures",
		Long: `Classify a failed test execution and trigger follow-on actions.

  gtms triage tc-007 --automation-wrong --summary "Selectors changed"
  gtms triage tc-007 --test-wrong --summary "Expected result changed"
  gtms triage tc-007 --app-wrong --defect JIRA-789 --summary "Payment gateway 500"
  gtms triage tc-007 --app-wrong --summary "bug" --json   — machine-readable JSON output`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := strings.ToLower(args[0])
			root := GetProjectRoot()

			// Validate: target ID is safe (rejects shell metacharacters, path traversal)
			if err := validateTargetID(target); err != nil {
				output.Errorf(err.Error(),
					"Use only letters, numbers, dashes, underscores, dots, and forward slashes.")
				return output.AsDisplayed(err)
			}

			// Validate: target format (test case ID)
			if !isTestCaseID(target) {
				msg := fmt.Sprintf("Invalid test case ID format: '%s'.", target)
				output.Errorf(msg, "Use format like tc-007.")
				return output.AsDisplayed(fmt.Errorf(msg))
			}

			// Validate: exactly one category flag
			category, err := resolveTriageCategory(automationWrong, testWrong, appWrong)
			if err != nil {
				output.Errorf(err.Error(), "")
				return output.AsDisplayed(err)
			}

			// Call reader.RecordTriage
			result, err := reader.RecordTriage(root, target, category, summary, defect)
			if err != nil {
				output.Errorf(err.Error(), "")
				return output.AsDisplayed(err)
			}

			// Format output
			if jsonOut {
				return writeTriageJSON(os.Stdout, result)
			}
			formatTriageOutput(os.Stdout, result)
			return nil
		},
	}

	cmd.Flags().BoolVar(&automationWrong, "automation-wrong", false, "Automation code is broken and needs rework")
	cmd.Flags().BoolVar(&testWrong, "test-wrong", false, "Test case itself is wrong and needs review")
	cmd.Flags().BoolVar(&appWrong, "app-wrong", false, "Application has a bug (raise defect)")
	cmd.Flags().StringVar(&summary, "summary", "", "Summary of the triage decision")
	cmd.Flags().StringVar(&defect, "defect", "", "Defect ID to link (used with --app-wrong)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")

	return cmd
}

// resolveTriageCategory validates that exactly one category flag is set and returns it.
func resolveTriageCategory(automationWrong, testWrong, appWrong bool) (string, error) {
	count := 0
	category := ""

	if automationWrong {
		count++
		category = "automation-wrong"
	}
	if testWrong {
		count++
		category = "test-wrong"
	}
	if appWrong {
		count++
		category = "app-wrong"
	}

	if count == 0 || count > 1 {
		return "", fmt.Errorf("Specify exactly one of --automation-wrong, --test-wrong, or --app-wrong")
	}

	return category, nil
}

// writeTriageJSON outputs the triage result as indented JSON.
func writeTriageJSON(w io.Writer, result *reader.TriageResult) error {
	if result.Actions == nil {
		result.Actions = []string{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

// formatTriageOutput prints the result of a triage command.
func formatTriageOutput(w io.Writer, result *reader.TriageResult) {
	fmt.Fprintf(w, "  %s Triage recorded for %s: %s\n",
		output.IconComplete, result.TestCaseID, result.Category)

	for _, action := range result.Actions {
		fmt.Fprintf(w, "    %s\n", action)
	}

	// Category-specific follow-up messages
	switch result.Category {
	case "automation-wrong":
		fmt.Fprintln(w, "    Run 'gtms automate status' to track progress")
	case "test-wrong":
		fmt.Fprintln(w, "    Action: Update the test case, then re-automate")
	case "app-wrong":
		if result.Defect != "" {
			fmt.Fprintf(w, "    Defect linked: %s\n", result.Defect)
		}
	}
}
