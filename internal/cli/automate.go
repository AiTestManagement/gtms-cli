package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/adapter"
	"github.com/aitestmanagement/gtms-cli/internal/git"
	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/output"
	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
	"github.com/aitestmanagement/gtms-cli/internal/task"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
	"github.com/spf13/cobra"
)

// newAutomateCmd builds the 'gtms automate' command with its flags and subcommands.
func newAutomateCmd() *cobra.Command {
	var adapterFlag string
	var frameworkFlag string
	var environmentFlag string
	var executedByFlag string
	var contextFileFlag string
	var forceFlag bool
	var failFastFlag bool
	var recursiveFlag bool

	cmd := &cobra.Command{
		Use:   "automate [test-case-id | folder]",
		Short: "Automate test cases into executable scripts",
		Long: `Automate a test case by delegating to a configured adapter.

Single test case:
  gtms automate tc-a1b2c3d4                         — use default adapter
  gtms automate tc-a1b2c3d4 --adapter local-claude  — use specific adapter
  gtms automate tc-a1b2c3d4 --framework playwright  — specify test framework
  gtms automate tc-a1b2c3d4 --context-file docs/standards.md — pass supplementary context

Folder (bulk mode):
  gtms automate my-feature                     — automate all test cases in gtms/cases/my-feature/
  gtms automate my-feature -r                  — include test cases from subdirectories
  gtms automate my-feature --force             — reprocess all test cases (ignore skip logic)
  gtms automate my-feature --fail-fast         — stop on first error

All test cases:
  gtms automate -r                             — automate all test cases across all folders`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
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

			// Read context file if provided (before bulk/single branch)
			var contextContent string
			var absContextPath string
			if contextFileFlag != "" {
				var err error
				absContextPath, err = filepath.Abs(contextFileFlag)
				if err != nil {
					output.Errorf(fmt.Sprintf("Invalid context file path: %s", contextFileFlag),
						"Provide a valid file path.")
					return output.AsDisplayed(fmt.Errorf("invalid context file path: %w", err))
				}
				data, err := os.ReadFile(absContextPath)
				if err != nil {
					output.Errorf(fmt.Sprintf("Cannot read context file: %s", contextFileFlag),
						"Check that the file exists and is readable.")
					return output.AsDisplayed(fmt.Errorf("reading context file: %w", err))
				}
				contextContent = string(data)
			}

			// ENH-125: resolve executed_by once at command entry (flag → env → git user.name).
			executedBy := pipeline.ResolveExecutedBy(ctx, root, executedByFlag)

			// No argument = run all test cases from root (recursive implied)
			if len(args) == 0 {
				recursiveFlag = true
				return runBulkAutomate(cmd, root, "", adapterFlag, frameworkFlag, environmentFlag, executedBy, forceFlag, failFastFlag, recursiveFlag, absContextPath, contextContent)
			}

			target := strings.ToLower(args[0])
			target = normaliseTarget(target)

			// Validate: target is safe (rejects shell metacharacters, path traversal)
			if err := validateTargetID(target); err != nil {
				output.Errorf(err.Error(),
					"Use only letters, numbers, dashes, underscores, dots, and forward slashes.")
				return output.AsDisplayed(err)
			}

			// Disambiguate: folder (bulk mode) vs single test case ID
			if IsBulkFolder(root, target) {
				return runBulkAutomate(cmd, root, target, adapterFlag, frameworkFlag, environmentFlag, executedBy, forceFlag, failFastFlag, recursiveFlag, absContextPath, contextContent)
			}

			// --- Single TC mode (existing behavior) ---

			// Validate: target format (test case ID, e.g. tc-007)
			if !isTestCaseID(target) {
				msg := fmt.Sprintf("Invalid test case ID format: '%s'.", target)
				output.Errorf(msg, "Use format like tc-a1b2c3d4.")
				return output.AsDisplayed(fmt.Errorf(msg))
			}

			// BUG-059: use shared testcase.Exists helper (replaces private testCaseExists).
			if !testcase.Exists(root, target) {
				msg := fmt.Sprintf("Test case '%s' not found in gtms/cases/", target)
				output.Errorf(msg, "Create the test case first with 'gtms create'.")
				return output.AsDisplayed(fmt.Errorf(msg))
			}

			// ENH-150: Post-fill validation gate — catches frontmatter corruption
			// before the adapter is invoked.
			if violations := adapter.ValidateTestCasePostFill(root, target); len(violations) > 0 {
				summary := adapter.FormatValidationErrors(violations)
				output.Errorf(summary, "Fix the test case frontmatter and try again.")
				return output.AsDisplayed(fmt.Errorf(summary))
			}

			// Validate: no duplicate active task for this target
			existing, err := task.FindByTarget(root, "automate", target)
			if err != nil {
				return fmt.Errorf("checking for existing tasks: %w", err)
			}
			if existing != nil {
				msg := fmt.Sprintf("An automate task for %s already exists: gtms/tasks/%s/%s-automate-%s.md",
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
				ExecutedBy:  executedBy,
				ContextFile: absContextPath,
				Context:     contextContent,
				Force:       forceFlag,
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
	cmd.Flags().StringVar(&executedByFlag, "executed-by", "", "Identity to record on the executed_by field (defaults to git user.name)")
	cmd.Flags().StringVar(&contextFileFlag, "context-file", "", "Path to context file (coding standards, API docs, etc.)")
	cmd.Flags().BoolVar(&forceFlag, "force", false, "Reprocess all test cases (ignore skip logic)")
	cmd.Flags().BoolVar(&failFastFlag, "fail-fast", false, "Stop on first error in bulk mode")
	cmd.Flags().BoolVarP(&recursiveFlag, "recursive", "r", false, "Include test cases from subdirectories")

	// Add automate status subcommand
	cmd.AddCommand(newAutomateStatusCmd())

	return cmd
}

// runBulkAutomate handles the bulk (folder) path for the automate command.
func runBulkAutomate(cmd *cobra.Command, root string, folder string, adapterFlag, frameworkFlag, environmentFlag, executedBy string, force, failFast, recursive bool, contextFilePath, contextContent string) error {
	ctx := cmd.Context()
	cfg := GetConfig()

	// Discover test cases in the folder
	tcIDs, err := DiscoverTestCases(root, folder, recursive)
	if err != nil {
		output.Errorf(err.Error(), "Check that gtms/cases/"+folder+"/ contains tc-*.md files.")
		return output.AsDisplayed(err)
	}

	// Resolve adapter once
	resolved, err := adapter.Resolve(cfg, "automate", adapterFlag)
	if err != nil {
		output.Errorf(err.Error(), "Check your gtms.config file.")
		return output.AsDisplayed(err)
	}

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "Resolved adapter: %s (tier %d, mode %s)\n",
			resolved.Name, resolved.Tier, resolved.Mode)
	}

	// Resolve framework from adapter config using precedence chain
	framework := adapter.ResolveFramework(resolved, frameworkFlag)

	total := len(tcIDs)
	recursiveLabel := ""
	if recursive {
		recursiveLabel = " (recursive)"
	}
	scope := "gtms/cases/"
	if folder != "" {
		scope = fmt.Sprintf("gtms/cases/%s/", folder)
	}
	fmt.Printf("Processing %d test cases in %s%s...\n", total, scope, recursiveLabel)

	succeeded := 0
	skipped := 0
	failed := 0

	for i, tcID := range tcIDs {
		idx := i + 1

		// Skip check (unless --force)
		if !force {
			if reason := shouldSkipAutomate(root, tcID, framework); reason != "" {
				skipped++
				fmt.Fprintf(os.Stderr, "  %s %-16s skipped (%s)  (%d/%d)\n",
					output.IconWarning, tcID, reason, idx, total)
				continue
			}
		}

		// Build flags for this TC
		flags := adapter.CommandFlags{
			Adapter:     adapterFlag,
			Framework:   frameworkFlag,
			Environment: environmentFlag,
			ExecutedBy:  executedBy,
			ContextFile: contextFilePath,
			Context:     contextContent,
			Force:       force,
		}

		// Progress indicator instead of spinner (TTY only — \r doesn't work in pipes)
		if output.IsTTY(os.Stderr) {
			fmt.Fprintf(os.Stderr, "  %s %-16s running...  (%d/%d)\r",
				output.IconInProgress, tcID, idx, total)
		}

		result, invokeErr := adapter.InvokeWithRoot(ctx, root, cfg, resolved, tcID, flags)

		if invokeErr != nil {
			failed++
			errMsg := invokeErr.Error()
			fmt.Fprintf(os.Stderr, "  %s %-16s failed: %s  (%d/%d)\n",
				output.IconError, tcID, truncateReason(errMsg, 40), idx, total)
			if failFast {
				break
			}
			continue
		}

		if result != nil && result.Status == "error" {
			failed++
			fmt.Fprintf(os.Stderr, "  %s %-16s failed: %s  (%d/%d)\n",
				output.IconError, tcID, truncateReason(result.Summary, 40), idx, total)
			if failFast {
				break
			}
			continue
		}

		succeeded++
		fmt.Fprintf(os.Stderr, "  %s %-16s automated  (%d/%d)   \n",
			output.IconComplete, tcID, idx, total)
	}

	// Print summary
	fmt.Printf("\n  %d automated, %d skipped, %d failed\n", succeeded, skipped, failed)

	// Print guidance once after summary
	printCommandGuidance("automate", whatHappenedBulkAutomate(folder, succeeded, skipped, failed))

	if failed > 0 {
		msg := fmt.Sprintf("%d of %d test cases failed", failed, total)
		return output.AsDisplayed(fmt.Errorf(msg))
	}

	return nil
}

// shouldSkipAutomate checks whether a TC should be skipped in bulk automate mode.
// Returns a skip reason string, or empty if the TC should be processed.
//
// CON-023 / ENH-145: presence of a wiring record means this TC × framework
// is already automated. The legacy automation-record lookup is retired —
// after the cutover, wiring is the only source of automation identity.
func shouldSkipAutomate(root, tcID, framework string) string {
	// Check if wiring record already exists for this framework.
	rec, _, err := wiring.Find(root, tcID, framework)
	if err == nil && rec != nil {
		return "already automated"
	}

	// Check for active task
	existing, err := task.FindByTarget(root, "automate", tcID)
	if err == nil && existing != nil {
		return "active task exists"
	}

	return ""
}

// validateAutomateFolderStructure checks that the required directories for automate exist.
func validateAutomateFolderStructure(root string) error {
	paths := layout.Current()
	required := []string{paths.Tasks, paths.Cases, paths.Automation}
	for _, dir := range required {
		path := filepath.Join(root, dir)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			msg := fmt.Sprintf("Required directory '%s' not found.", dir)
			output.Errorf(msg, fmt.Sprintf("Create the GTMS folder structure: %s/, %s/, %s/", paths.Tasks, paths.Cases, paths.Automation))
			return output.AsDisplayed(fmt.Errorf(msg))
		}
	}
	return nil
}

// isTestCaseID validates that the target looks like a test case ID (e.g. tc-a1b2c3d4).
// Requires the "tc-" prefix. For subfolder-scoped targets like "cwd-scoping/tc-abc123",
// the base name after the last "/" is checked.
func isTestCaseID(target string) bool {
	base := target
	if idx := strings.LastIndex(target, "/"); idx >= 0 {
		base = target[idx+1:]
	}
	return strings.HasPrefix(base, "tc-") && len(base) > 3
}

// truncateReason shortens a reason string for display in progress output.
func truncateReason(reason string, maxLen int) string {
	if len(reason) <= maxLen {
		return reason
	}
	return reason[:maxLen-3] + "..."
}

// formatAutomateOutput prints the result of an automate command.
// ENH-120: headline surfaces artefact path, not internal task filename.
func formatAutomateOutput(res *adapter.InvokeResult) {
	if res.Status == "error" {
		output.Errorf(
			fmt.Sprintf("Task failed: %s", res.Filename),
			res.Summary,
		)
		printCommandGuidance("automate", whatHappenedAutomate(res))
		return
	}

	// ENH-120: artefact-focused headline — surface the produced artefact path.
	if len(res.ArtifactPaths) > 0 {
		fmt.Printf("  %s Automated %s: %s\n", output.IconComplete, res.Target, res.ArtifactPaths[0])
	} else if len(res.Warnings) > 0 && res.ArtifactCount == 0 {
		fmt.Printf("  %s Completed with warnings for %s\n", output.IconWarning, res.Target)
	} else if res.ArtifactCount > 0 {
		fmt.Printf("  %s Automated %s (%d files)\n", output.IconComplete, res.Target, res.ArtifactCount)
	} else {
		fmt.Printf("  %s Automated %s\n", output.IconComplete, res.Target)
	}

	fmt.Printf("    Adapter: %s (%s)\n", res.Adapter, res.Mode)

	// ENH-120: task ID and branch demoted to verbose-only output
	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "    Task: %s\n", res.TaskID)
		fmt.Fprintf(os.Stderr, "    Branch: %s\n", res.Branch)
	}

	if res.Mode == "async" {
		fmt.Println("    Check progress: gtms automate status")
	}

	for _, w := range res.Warnings {
		output.Warnf(w)
	}

	printCommandGuidance("automate", whatHappenedAutomate(res))
}
