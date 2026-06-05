package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/git"
	"github.com/aitestmanagement/gtms-cli/internal/link"
	"github.com/aitestmanagement/gtms-cli/internal/output"
	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
)

// newLinkCmd builds the 'gtms link' command with its flags.
func newLinkCmd() *cobra.Command {
	var frameworkFlag string
	var artefactFlag string
	var environmentFlag string
	var executedByFlag string
	var checkFlag bool
	var forceFlag bool
	var strictFlag bool

	cmd := &cobra.Command{
		Use:   "link <tc-id>",
		Short: "Register a pre-existing test as an automation record",
		Long: `Link a pre-existing test to a test case by creating an automation record.

The user asserts that the test file follows the TC-ID naming convention.
GTMS checks the artefact file exists (filesystem only) and writes the record.
No framework CLI is invoked.

Examples:
  gtms link tc-a1b2c3d4 --framework playwright --artefact tests/login.spec.ts
  gtms link tc-a1b2c3d4 --framework playwright --artefact tests/login.spec.ts --force
  gtms link tc-a1b2c3d4 --framework playwright --artefact tests/login.spec.ts --check
  gtms link tc-a1b2c3d4 --framework playwright --check   (re-check existing link)`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetProjectRoot()
			target := strings.ToLower(args[0])
			target = normaliseTarget(target)

			// BUG-058: validate target ID safety (path traversal, shell metacharacters)
			// before the isTestCaseID format check. Other commands (automate, execute,
			// delete, triage) already call validateTargetID; link was the only gap.
			if err := validateTargetID(target); err != nil {
				msg := fmt.Sprintf("Invalid test case ID format: '%s'.", target)
				output.Errorf(msg, "Use format like tc-a1b2c3d4.")
				return output.AsDisplayed(fmt.Errorf(msg))
			}

			// Validate target format
			if !isTestCaseID(target) {
				msg := fmt.Sprintf("Invalid test case ID format: '%s'.", target)
				output.Errorf(msg, "Use format like tc-a1b2c3d4.")
				return output.AsDisplayed(fmt.Errorf(msg))
			}

			// Validate --framework is provided and valid
			if frameworkFlag == "" {
				msg := "--framework is required for gtms link."
				output.Errorf(msg, "Example: gtms link tc-a1b2c3d4 --framework playwright --artefact tests/login.spec.ts")
				return output.AsDisplayed(fmt.Errorf(msg))
			}
			if !config.ValidateFramework(frameworkFlag) {
				msg := fmt.Sprintf("Invalid framework '%s'. Framework must contain only lowercase letters, digits, and hyphens.", frameworkFlag)
				output.Errorf(msg, "Example: --framework playwright")
				return output.AsDisplayed(fmt.Errorf(msg))
			}

			// --check mode: validate without writing
			if checkFlag {
				result, err := link.CheckLink(root, target, frameworkFlag, artefactFlag, strictFlag)
				if err != nil {
					output.Errorf(err.Error(), "Check the artefact path and try again.")
					return output.AsDisplayed(err)
				}
				formatCheckOutput(result)
				return nil
			}

			// Write mode: --artefact is required
			if artefactFlag == "" {
				msg := "--artefact is required for gtms link."
				output.Errorf(msg, "Example: gtms link tc-a1b2c3d4 --framework playwright --artefact tests/login.spec.ts")
				return output.AsDisplayed(fmt.Errorf(msg))
			}

			// Get current branch (best-effort)
			branch, _ := git.CurrentBranch(cmd.Context(), root)

			// ENH-125: resolve executed_by (flag → env var → git user.name).
			executedBy := pipeline.ResolveExecutedBy(cmd.Context(), root, executedByFlag)

			// Delegate to core link package. cfg is required to resolve the
			// canonical execute adapter for wiring records (CON-023 / ENH-145).
			cfg := GetConfig()
			warnings, err := link.LinkRecord(root, cfg, target, frameworkFlag, artefactFlag, branch, environmentFlag, executedBy, forceFlag, strictFlag)
			if err != nil {
				output.Errorf(err.Error(), "Check the artefact path or use --force to overwrite.")
				return output.AsDisplayed(err)
			}

			fmt.Printf("  %s Linked: %s (%s)\n", output.IconComplete, target, frameworkFlag)
			for _, w := range warnings {
				output.Warnf(w)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&frameworkFlag, "framework", "", "Test framework (e.g., playwright, cypress)")
	cmd.Flags().StringVar(&artefactFlag, "artefact", "", "Path to the test artefact file")
	cmd.Flags().StringVar(&environmentFlag, "env", "", "Target environment (e.g., staging, production)")
	cmd.Flags().StringVar(&executedByFlag, "executed-by", "", "Identity to record on the executed_by field (defaults to git user.name)")
	cmd.Flags().BoolVar(&checkFlag, "check", false, "Validate without writing (health check mode)")
	cmd.Flags().BoolVar(&forceFlag, "force", false, "Overwrite existing record")
	cmd.Flags().BoolVar(&strictFlag, "strict", false, "Reject phantom TC IDs that have no spec under gtms/cases/")

	return cmd
}

// formatCheckOutput prints the result of a --check validation.
func formatCheckOutput(result link.CheckResult) {
	fmt.Printf("  %s Check passed: %s (%s)\n", output.IconComplete, result.TestCase, result.Framework)
	fmt.Printf("    Artefact: %s\n", result.Artefact)
	if result.RecordExists {
		fmt.Printf("    Record: exists\n")
	} else {
		fmt.Printf("    Record: not yet created\n")
	}
}
