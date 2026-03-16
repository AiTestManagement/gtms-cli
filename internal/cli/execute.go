package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/aitestmanagement/gtms-cli/internal/adapter"
	"github.com/aitestmanagement/gtms-cli/internal/git"
	"github.com/aitestmanagement/gtms-cli/internal/output"
	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
	"github.com/aitestmanagement/gtms-cli/internal/task"
)

// newExecuteCmd builds the 'gtms execute' command with its flags and subcommands.
func newExecuteCmd() *cobra.Command {
	var adapterFlag string
	var environmentFlag string

	cmd := &cobra.Command{
		Use:   "execute <test-case-id>",
		Short: "Execute automated test cases",
		Long: `Execute an automated test case by delegating to a configured adapter.

  gtms execute tc-007                          — use default adapter
  gtms execute tc-007 --adapter mock-runner    — use specific adapter`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			target := strings.ToLower(args[0])
			cfg := GetConfig()
			root := GetProjectRoot()

			// Validate: git repo exists
			if !git.IsRepo(ctx, root) {
				output.Errorf("Not a git repository.", "Initialize a git repo with 'git init'.")
				return output.AsDisplayed(fmt.Errorf("not a git repository"))
			}

			// Validate: folder structure exists
			if err := validateAutomateFolderStructure(root); err != nil {
				return err
			}

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

			// Validate: automation record exists with status accepted or developed
			record, _, err := pipeline.FindAutomationRecord(root, target)
			if err != nil {
				return fmt.Errorf("checking automation record: %w", err)
			}
			if record == nil {
				msg := fmt.Sprintf("No automation record found for '%s'. Run 'gtms automate %s' first.", target, target)
				output.Errorf(msg, fmt.Sprintf("Run 'gtms automate %s' to create automation.", target))
				return output.AsDisplayed(fmt.Errorf(msg))
			}
			if record.Status != "accepted" && record.Status != "developed" {
				msg := fmt.Sprintf("Automation record for '%s' has status '%s'. Must be 'accepted' or 'developed'.", target, record.Status)
				output.Errorf(msg, "Wait for automation to complete or re-run 'gtms automate'.")
				return output.AsDisplayed(fmt.Errorf(msg))
			}

			// Validate: no duplicate active task for this target
			existing, err := task.FindByTarget(root, "execute", target)
			if err != nil {
				return fmt.Errorf("checking for existing tasks: %w", err)
			}
			if existing != nil {
				msg := fmt.Sprintf("An execute task for %s already exists: test-tasks/%s/%s-execute-%s.md",
					target, existing.Status, existing.ID, existing.Target)
				output.Errorf(msg, "Wait for the existing task to complete or remove it.")
				return output.AsDisplayed(fmt.Errorf(msg))
			}

			// Resolve adapter
			resolved, err := adapter.Resolve(cfg, "execute", adapterFlag)
			if err != nil {
				output.Errorf(err.Error(), "Check your gtms.config file.")
				return output.AsDisplayed(err)
			}

			if IsVerbose() {
				fmt.Fprintf(os.Stderr, "Resolved adapter: %s (tier %d, mode %s)\n",
					resolved.Name, resolved.Tier, resolved.Mode)
			}

			// Invoke adapter with spec file from automation record
			flags := adapter.CommandFlags{
				Adapter:     adapterFlag,
				SpecFile:    record.Artefact,
				Environment: environmentFlag,
			}

			// Start spinner for sync adapters
			var spinner *output.Spinner
			if resolved.Mode == "sync" {
				spinner = output.NewSpinner(os.Stderr, fmt.Sprintf("Running %s...", resolved.Name))
				spinner.Start()
			}

			result, err := adapter.InvokeWithRoot(ctx, root, cfg, resolved, target, flags)

			// Stop spinner BEFORE any output
			if spinner != nil {
				spinner.Stop()
			}

			if err != nil {
				return err
			}

			// Format output
			formatExecuteOutput(result)

			return nil
		},
	}

	cmd.Flags().StringVar(&adapterFlag, "adapter", "", "Adapter to use (overrides default)")
	cmd.Flags().StringVar(&environmentFlag, "env", "", "Target environment (e.g., staging, production)")

	// Add execute status subcommand
	cmd.AddCommand(newExecuteStatusCmd())

	return cmd
}

// formatExecuteOutput prints the result of an execute command.
func formatExecuteOutput(res *adapter.InvokeResult) {
	if res.Status == "error" {
		output.Errorf(
			fmt.Sprintf("Task failed: %s", res.Filename),
			res.Summary,
		)
		return
	}

	if len(res.Warnings) > 0 && res.ArtifactCount == 0 {
		fmt.Fprintf(os.Stderr, "  %s Task completed with warnings: %s\n", output.IconWarning, res.Filename)
	} else if res.ArtifactCount > 0 {
		fmt.Printf("  %s Task created: %s (%d files)\n", output.IconComplete, res.Filename, res.ArtifactCount)
	} else {
		fmt.Printf("  %s Task created: %s\n", output.IconComplete, res.Filename)
	}
	fmt.Printf("    Adapter: %s (%s)\n", res.Adapter, res.Mode)
	fmt.Printf("    Branch: %s\n", res.Branch)

	if res.Mode == "async" {
		fmt.Println("    Check progress: gtms execute status")
	}

	for _, w := range res.Warnings {
		output.Warnf(w)
	}
}
