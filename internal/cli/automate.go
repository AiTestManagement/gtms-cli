package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/aitestmanagement/gtms-cli/internal/adapter"
	"github.com/aitestmanagement/gtms-cli/internal/git"
	"github.com/aitestmanagement/gtms-cli/internal/output"
	"github.com/aitestmanagement/gtms-cli/internal/task"
)

// newAutomateCmd builds the 'gtms automate' command with its flags and subcommands.
func newAutomateCmd() *cobra.Command {
	var adapterFlag string
	var frameworkFlag string
	var environmentFlag string

	cmd := &cobra.Command{
		Use:   "automate <test-case-id>",
		Short: "Automate test cases into executable scripts",
		Long: `Automate a test case by delegating to a configured adapter.

  gtms automate tc-007                         — use default adapter
  gtms automate tc-007 --adapter local-claude  — use specific adapter
  gtms automate tc-007 --framework playwright  — specify test framework`,
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

			// Validate: target format (test case ID, e.g. tc-007)
			if !isTestCaseID(target) {
				msg := fmt.Sprintf("Invalid test case ID format: '%s'.", target)
				output.Errorf(msg, "Use format like tc-007.")
				return output.AsDisplayed(fmt.Errorf(msg))
			}

			// Validate: test case file exists
			if !testCaseExists(root, target) {
				msg := fmt.Sprintf("Test case '%s' not found in test-cases/", target)
				output.Errorf(msg, "Create the test case first with 'gtms create'.")
				return output.AsDisplayed(fmt.Errorf(msg))
			}

			// Validate: no duplicate active task for this target
			existing, err := task.FindByTarget(root, "automate", target)
			if err != nil {
				return fmt.Errorf("checking for existing tasks: %w", err)
			}
			if existing != nil {
				msg := fmt.Sprintf("An automate task for %s already exists: test-tasks/%s/%s-automate-%s.md",
					target, existing.Status, existing.ID, existing.Target)
				output.Errorf(msg, "Wait for the existing task to complete or remove it.")
				return output.AsDisplayed(fmt.Errorf(msg))
			}

			// Resolve adapter
			resolved, err := adapter.Resolve(cfg, "automate", adapterFlag)
			if err != nil {
				output.Errorf(err.Error(), "Check your gtms.config file.")
				return output.AsDisplayed(err)
			}

			if IsVerbose() {
				fmt.Fprintf(os.Stderr, "Resolved adapter: %s (tier %d, mode %s)\n",
					resolved.Name, resolved.Tier, resolved.Mode)
			}

			// Invoke adapter
			flags := adapter.CommandFlags{
				Adapter:     adapterFlag,
				Framework:   frameworkFlag,
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
			formatAutomateOutput(result)

			return nil
		},
	}

	cmd.Flags().StringVar(&adapterFlag, "adapter", "", "Adapter to use (overrides default)")
	cmd.Flags().StringVar(&frameworkFlag, "framework", "", "Test framework (e.g., playwright, cypress)")
	cmd.Flags().StringVar(&environmentFlag, "env", "", "Target environment (e.g., staging, production)")

	// Add automate status subcommand
	cmd.AddCommand(newAutomateStatusCmd())

	return cmd
}

// validateAutomateFolderStructure checks that the required directories for automate exist.
func validateAutomateFolderStructure(root string) error {
	required := []string{"test-tasks", "test-cases", "test-automation"}
	for _, dir := range required {
		path := filepath.Join(root, dir)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			msg := fmt.Sprintf("Required directory '%s' not found.", dir)
			output.Errorf(msg, "Create the GTMS folder structure: test-tasks/, test-cases/, test-automation/")
			return output.AsDisplayed(fmt.Errorf(msg))
		}
	}
	return nil
}

// isTestCaseID validates that the target looks like a test case ID (e.g. tc-007).
func isTestCaseID(target string) bool {
	// Accept formats like tc-007, tc-1, tc-42, etc.
	// Also accept other ID formats with a dash
	parts := strings.SplitN(target, "-", 2)
	if len(parts) < 2 {
		return false
	}
	return len(parts[0]) > 0 && len(parts[1]) > 0
}

// testCaseExists checks if a test case file exists for the given target.
func testCaseExists(root, target string) bool {
	testCasesDir := filepath.Join(root, "test-cases")

	// Walk looking for a file starting with the target ID
	found := false
	_ = filepath.Walk(testCasesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if strings.HasPrefix(base, target+"-") || strings.HasPrefix(base, target+".") {
			found = true
			return filepath.SkipAll
		}
		return nil
	})

	return found
}

// formatAutomateOutput prints the result of an automate command.
func formatAutomateOutput(res *adapter.InvokeResult) {
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
		fmt.Println("    Check progress: gtms automate status")
	}

	for _, w := range res.Warnings {
		output.Warnf(w)
	}
}
