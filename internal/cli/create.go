package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/aitestmanagement/gtms-cli/internal/adapter"
	"github.com/aitestmanagement/gtms-cli/internal/git"
	"github.com/aitestmanagement/gtms-cli/internal/output"
	"github.com/aitestmanagement/gtms-cli/internal/task"
)

// newCreateCmd builds the 'gtms create' command with its flags and subcommands.
func newCreateCmd() *cobra.Command {
	var adapterFlag string
	var focusFlag string
	var contextFileFlag string
	var referenceFlag string

	cmd := &cobra.Command{
		Use:   "create <folder>",
		Short: "Create test cases from requirements",
		Long: `Create test cases by delegating to a configured adapter.

The <folder> argument specifies where test case files will be created,
relative to the test-cases/ directory. GTMS creates the folder automatically.

INPUT LAYERS:

  Config (gtms.config → adapters.create.<name>):
    prompt-template  Path to the prompt template file. Uses {reference},
                     {focus}, {context}, {guides} placeholders that GTMS
                     fills before passing to the adapter.
    guide-dir        Directory of .md files. GTMS reads all .md files
                     (sorted alphabetically), concatenates them, and
                     injects the content as {guides} in the prompt
                     template. Use for test case standards, templates,
                     and writing conventions.
    command/script   How the adapter runs. Tier 1: command template with
                     {variable} substitution. Tier 2: script receiving
                     GTMS_* environment variables.

  CLI Flags:
    <folder>         Target folder under test-cases/ where files will be
                     created. Examples: bug-022, payments/checkout.
    --reference      Optional reference identifier (e.g. REQ-001, BUG-022).
                     Becomes {reference} in templates and $GTMS_REFERENCE
                     for Tier 2 scripts.
    --focus          Narrows scope within the source. Becomes {focus}
                     in templates and $GTMS_FOCUS for scripts.
    --context-file   Path to a context file. GTMS reads the file and
                     injects its content as {context} in templates and
                     $GTMS_CONTEXT for scripts. Essential when using
                     --allowedTools "" with local requirement files.
    --adapter        Override the default adapter for this invocation.

  Guides (from guide-dir):
    All .md files in the configured guide-dir are automatically embedded
    into every create invocation. This ensures consistent quality
    standards without relying on the AI to discover reference files.

DATA FLOW:

  1. GTMS reads guide-dir .md files → concatenated as {guides}
  2. GTMS reads prompt-template → fills {reference}, {focus}, {context}, {guides}
  3. Assembled prompt passed to adapter:
     Tier 1: substituted into command template as {prompt}
     Tier 2: exported as GTMS_* environment variables
  4. Adapter generates test case files in test-cases/<folder>/
  5. GTMS creates task file, result contract, and reports outcome

Examples:
  gtms create bug-022 --context-file PRPs/bugs/BUG-022.md --reference BUG-022
  gtms create payments/checkout --reference REQ-123 --focus "guest checkout"
  gtms create sprint-14 --adapter github-create`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cfg := GetConfig()
			root := GetProjectRoot()

			// Validate and clean folder argument
			folder, err := validateFolderArg(args[0])
			if err != nil {
				output.Errorf(err.Error(), "")
				return output.AsDisplayed(err)
			}

			// Validate: git repo exists
			if !git.IsRepo(ctx, root) {
				output.Errorf("Not a git repository.", "Initialize a git repo with 'git init'.")
				return output.AsDisplayed(fmt.Errorf("not a git repository"))
			}

			// Validate: folder structure exists
			if err := validateFolderStructure(root); err != nil {
				return err
			}

			// Validate: no duplicate active task for this folder
			existing, err := task.FindByTarget(root, "create", folder)
			if err != nil {
				return fmt.Errorf("checking for existing tasks: %w", err)
			}
			if existing != nil {
				msg := fmt.Sprintf("A create task for %s already exists: test-tasks/%s/%s-create-%s.md",
					folder, existing.Status, existing.ID, existing.Target)
				output.Errorf(msg, "Wait for the existing task to complete or remove it.")
				return output.AsDisplayed(fmt.Errorf(msg))
			}

			// Read context file if provided
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

			// Resolve adapter
			resolved, err := adapter.Resolve(cfg, "create", adapterFlag)
			if err != nil {
				output.Errorf(err.Error(), "Check your gtms.config file.")
				return output.AsDisplayed(err)
			}

			if IsVerbose() {
				fmt.Fprintf(os.Stderr, "Resolved adapter: %s (tier %d, mode %s)\n",
					resolved.Name, resolved.Tier, resolved.Mode)
			}

			// Auto-create the target folder under test-cases/ (only when adapter doesn't override output-dir)
			if resolved.Config.OutputDir == "" {
				outputDir := filepath.Join(root, "test-cases", folder)
				if err := os.MkdirAll(outputDir, 0755); err != nil {
					return fmt.Errorf("creating output directory: %w", err)
				}
				fmt.Fprintf(os.Stderr, "  → Target folder: test-cases/%s/\n", folder)
			} else {
				fmt.Fprintf(os.Stderr, "  → Output directory: %s (from adapter config)\n", resolved.Config.OutputDir)
			}

			// Invoke adapter
			flags := adapter.CommandFlags{
				Adapter:     adapterFlag,
				Focus:       focusFlag,
				ContextFile: absContextPath,
				Context:     contextContent,
				Folder:      folder,
				Reference:   referenceFlag,
			}

			// Start spinner for sync adapters
			var spinner *output.Spinner
			if resolved.Mode == "sync" {
				spinner = output.NewSpinner(os.Stderr, fmt.Sprintf("Running %s...", resolved.Name))
				spinner.Start()
			}

			result, err := adapter.InvokeWithRoot(ctx, root, cfg, resolved, folder, flags)

			// Stop spinner BEFORE any output
			if spinner != nil {
				spinner.Stop()
			}

			if err != nil {
				return err
			}

			// Format output
			formatCreateOutput(result)

			return nil
		},
	}

	cmd.Flags().StringVar(&adapterFlag, "adapter", "", "Adapter to use (overrides default)")
	cmd.Flags().StringVar(&focusFlag, "focus", "", "Focus area within the source document")
	cmd.Flags().StringVar(&contextFileFlag, "context-file", "", "Path to context file (essential for file-based requirements)")
	cmd.Flags().StringVar(&referenceFlag, "reference", "", "Reference identifier (e.g. REQ-001, BUG-022)")

	// Add create status subcommand
	cmd.AddCommand(newCreateStatusCmd())

	return cmd
}

// validateFolderStructure checks that the required GTMS directories exist.
func validateFolderStructure(root string) error {
	required := []string{"test-tasks", "test-cases"}
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

// formatCreateOutput prints the result of a create command.
func formatCreateOutput(res *adapter.InvokeResult) {
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
		fmt.Println("    Check progress: gtms create status")
	}

	for _, w := range res.Warnings {
		output.Warnf(w)
	}
}
