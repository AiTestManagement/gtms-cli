package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/adapter"
	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/git"
	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/output"
	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
	"github.com/aitestmanagement/gtms-cli/internal/task"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
	"github.com/spf13/cobra"
)

// newPrimeCmd builds the 'gtms prime' command.
// ENH-132: Thin command that delegates to the automate invocation path
// with defaults suited for manual test result preparation.
func newPrimeCmd() *cobra.Command {
	var adapterFlag string
	var frameworkFlag string
	var executedByFlag string
	var contextFileFlag string
	var forceFlag bool
	var updateHashFlag bool

	cmd := &cobra.Command{
		Use:   "prime <test-case-id>",
		Short: "Prepare a manual test result template",
		Long: `Stamp a blank manual result template for a test case.

The stamped file is placed at gtms/manual/records/<tc-id>--manual.result.yaml
and contains the schema directive, test case identity fields, and an empty
result: field for the tester to fill in.

Three modes:
  First prime   -- stamps a fresh template (default).
  --update-hash -- refreshes test_case_hash on an existing result file and
                  strips drift fields, preserving any filled result: value
                  and prior EXECUTE status (manual framework only).
  --force       -- destructive overwrite (loses all filled content).

Examples:
  gtms prime tc-a1b2c3d4                         -- stamp manual result template
  gtms prime tc-a1b2c3d4 --update-hash           -- refresh hash, keep result
  gtms prime tc-a1b2c3d4 --force                 -- overwrite an existing result file

Zero-AI manual testing flow:
  1. gtms init                                          -- scaffold project
  2. gtms create demo                                   -- stamp a test case skeleton, then fill it in
  3. gtms prime tc-xxxxxxxx                             -- stamp result template
  4. Edit gtms/manual/records/tc-xxxxxxxx--manual.result.yaml (set result: pass|fail|skip)
  5. gtms execute tc-xxxxxxxx --adapter manual-execute  -- record the result

Adapter execution:
  With no --adapter or default, prime uses the built-in manual-prime.
  Adapters run identically on every OS.
  See "Adapter Execution Model" in USER-GUIDE.md.`,
		Args: cobra.ExactArgs(1),
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
			if err := validatePrimeFolderStructure(root); err != nil {
				return err
			}

			target := normaliseTarget(args[0])

			// Validate: target is safe
			if err := validateTargetID(target); err != nil {
				output.Errorf(err.Error(),
					"Use only letters, numbers, dashes, underscores, dots, and forward slashes.")
				return output.AsDisplayed(err)
			}

			// Validate: target is a test case ID
			if !isTestCaseID(target) {
				msg := fmt.Sprintf("Invalid test case ID format: '%s'.", target)
				output.Errorf(msg, "Use format like tc-a1b2c3d4.")
				return output.AsDisplayed(fmt.Errorf(msg))
			}

			// Validate: test case exists
			if !testcase.Exists(root, target) {
				msg := fmt.Sprintf("Test case '%s' not found in gtms/test/cases/", target)
				output.Errorf(msg, "Create the test case first with 'gtms create'.")
				return output.AsDisplayed(fmt.Errorf(msg))
			}

			// ENH-150: Post-fill validation gate -- catches frontmatter corruption
			// before the adapter is invoked (ID mismatch, missing fields, duplicates).
			if violations := adapter.ValidateTestCasePostFill(root, target); len(violations) > 0 {
				summary := adapter.FormatValidationErrors(violations)
				output.Errorf(summary, "Fix the test case frontmatter and try again.")
				return output.AsDisplayed(fmt.Errorf(summary))
			}

			// BUG-121: Validate --framework flag if provided (mirrors execute.go:113).
			// Guard sits before framework resolution so invalid values never reach
			// the adapter or the --update-hash shortcut.
			if frameworkFlag != "" && !config.ValidateFramework(frameworkFlag) {
				msg := fmt.Sprintf("Invalid framework '%s'. Framework must contain only lowercase letters, digits, and hyphens.", frameworkFlag)
				output.Errorf(msg, "Example: --framework playwright")
				return output.AsDisplayed(fmt.Errorf(msg))
			}

			// ENH-134: --update-hash on the manual framework preserves execute
			// history. When a successful execute exists, only the manual result
			// file's test_case_hash is refreshed and drift fields are stripped.
			// The automation record's result:, executed_at:, etc. are untouched.
			{
				fw := frameworkFlag
				if fw == "" {
					fw = "manual"
				}
				if updateHashFlag && fw == "manual" {
					if err := manualUpdateHash(root, target); err != nil {
						output.Errorf(err.Error(), "Check the manual result file and try again.")
						return output.AsDisplayed(err)
					}
					return nil
				}
			}

			// Validate: no duplicate active task
			existing, err := task.FindByTarget(root, "prime", target)
			if err != nil {
				return fmt.Errorf("checking for existing tasks: %w", err)
			}
			if existing != nil {
				msg := fmt.Sprintf("A prime task for %s already exists: gtms/tasks/%s/%s-prime-%s.md",
					target, existing.Status, existing.ID, existing.Target)
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

			// Resolve executed_by
			executedBy := pipeline.ResolveExecutedBy(ctx, root, executedByFlag)

			// ENH-150: prime is its own adapter bucket. The resolver falls back
			// to the built-in manual-prime when no config entry or default exists,
			// so no CLI-level hardcode is needed for the adapter name.
			resolvedFramework := frameworkFlag
			if resolvedFramework == "" {
				resolvedFramework = "manual"
			}

			// Resolve adapter against "prime" bucket (ENH-150)
			resolved, err := adapter.Resolve(cfg, "prime", adapterFlag)
			if err != nil {
				output.Errorf(err.Error(), "Check your gtms.config file.")
				return output.AsDisplayed(err)
			}

			if IsVerbose() {
				fmt.Fprintf(os.Stderr, "Resolved adapter: %s (tier %d, mode %s)\n",
					resolved.Name, resolved.Tier, resolved.Mode)
			}

			// Build flags
			flags := adapter.CommandFlags{
				Adapter:     adapterFlag,
				Framework:   resolvedFramework,
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

			// BUG-080: dedicated prime output -- no longer reuses automate formatting
			formatPrimeOutput(result)

			// BUG-135: propagate adapter-level failures as non-zero exit code,
			// matching the contract in create.go and execute.go.
			// Use bare Summary (no "task failed:" prefix) because formatPrimeOutput
			// already prints the "Task failed:" headline.
			if result != nil && result.Status == "error" {
				return output.AsDisplayed(fmt.Errorf("%s", result.Summary))
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&adapterFlag, "adapter", "", "Adapter to use (default: manual-prime)")
	cmd.Flags().StringVar(&frameworkFlag, "framework", "", "Test framework (default: manual)")
	cmd.Flags().StringVar(&executedByFlag, "executed-by", "", "Identity to record on the executed_by field (defaults to GTMS_EXECUTED_BY, then git user.name)")
	cmd.Flags().StringVar(&contextFileFlag, "context-file", "", "Path to context file")
	cmd.Flags().BoolVar(&forceFlag, "force", false, "Overwrite existing result file")
	cmd.Flags().BoolVar(&updateHashFlag, "update-hash", false, "Refresh test_case_hash and clear drift fields, preserving execute history")

	return cmd
}

// formatPrimeOutput prints the result of a prime command.
// BUG-080: dedicated renderer -- uses "Primed" wording and reads from the
// "prime" guidance key, not the "automate" key.
func formatPrimeOutput(res *adapter.InvokeResult) {
	if res.Status == "error" {
		output.Errorf(
			fmt.Sprintf("Task failed: %s", res.Filename),
			res.Summary,
		)
		printCommandGuidance("prime", whatHappenedPrime(res))
		return
	}

	// Headline: "Primed" instead of "Automated"
	if len(res.ArtifactPaths) > 0 {
		fmt.Printf("  %s Primed %s: %s\n", output.IconComplete, res.Target, res.ArtifactPaths[0])
	} else if res.ArtifactCount > 0 {
		fmt.Printf("  %s Primed %s (%d files)\n", output.IconComplete, res.Target, res.ArtifactCount)
	} else {
		fmt.Printf("  %s Primed %s\n", output.IconComplete, res.Target)
	}

	fmt.Printf("    Adapter: %s (%s)\n", res.Adapter, res.Mode)

	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "    Task: %s\n", res.TaskID)
		fmt.Fprintf(os.Stderr, "    Branch: %s\n", res.Branch)
	}

	for _, w := range res.Warnings {
		output.Warnf(w)
	}

	printCommandGuidance("prime", whatHappenedPrime(res))
}

// validatePrimeFolderStructure checks that the required directories for prime exist.
func validatePrimeFolderStructure(root string) error {
	paths := layout.Current()
	required := []string{paths.Tasks, paths.TestCases}
	for _, dir := range required {
		path := filepath.Join(root, dir)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			msg := fmt.Sprintf("Required directory '%s' not found.", dir)
			output.Errorf(msg, "Run 'gtms init' to create the project structure.")
			return output.AsDisplayed(fmt.Errorf(msg))
		}
	}
	return nil
}

// manualUpdateHash refreshes the test_case_hash in the manual result file
// without resetting execute history. This preserves the automation record's
// result:, executed_at:, and other ENH-123 fields after a test case content
// change when the prior manual outcome remains semantically valid.
//
// ENH-134: single dispatch point for the manual-specific --update-hash
// preservation behaviour. Non-manual frameworks go through normal automate.
func manualUpdateHash(root, target string) error {
	// 1. Find the manual result file (CON-023 / ENH-145: manual TCs
	// have no wiring; the canonical manual artefact is the result file
	// under gtms/manual/records/).
	manualPaths := layout.Current()
	resultFile := fmt.Sprintf("%s--manual.result.yaml", target)
	relResultFile := filepath.ToSlash(filepath.Join(manualPaths.Manual, "records", resultFile))
	absResultFile := filepath.Join(root, manualPaths.Manual, "records", resultFile)

	if _, statErr := os.Stat(absResultFile); os.IsNotExist(statErr) {
		return fmt.Errorf("no manual result file found at %s -- run gtms prime %s --framework manual first",
			relResultFile, target)
	}
	_ = relResultFile // path retained above for the error message

	// 4. Compute the new test_case_hash
	tcSource := testcase.FindSource(root, target)
	if tcSource == "" {
		return fmt.Errorf("test case file not found for %s", target)
	}
	absTC := filepath.Join(root, tcSource)
	newHash, hashErr := pipeline.HashFile(absTC)
	if hashErr != nil {
		return fmt.Errorf("could not hash test case file: %w", hashErr)
	}

	// 5. Read the manual result file, update test_case_hash, strip drift fields
	data, readErr := os.ReadFile(absResultFile)
	if readErr != nil {
		return fmt.Errorf("could not read manual result file: %w", readErr)
	}
	content := string(data)
	content = updateTestcaseHash(content, newHash)
	content = stripDriftFields(content)

	// 6. Write back
	if writeErr := os.WriteFile(absResultFile, []byte(content), 0644); writeErr != nil {
		return fmt.Errorf("could not write manual result file: %w", writeErr)
	}

	fmt.Printf("  %s Updated test_case_hash for %s (execute history preserved)\n", output.IconComplete, target)
	return nil
}

// testcaseHashRe matches the test_case_hash: line in a YAML file.
var testcaseHashRe = regexp.MustCompile(`(?m)^test_case_hash:\s+[a-f0-9]{16}\s*$`)

// updateTestcaseHash replaces the test_case_hash value in YAML content.
func updateTestcaseHash(content, newHash string) string {
	return testcaseHashRe.ReplaceAllString(content, "test_case_hash: "+newHash)
}

// driftFieldRe matches drift diagnostic fields appended by the manual-execute adapter.
var driftFieldRe = regexp.MustCompile(`(?m)^(drift-detected|drift-detected-at|test_case_hash_at_execute):.*\n?`)

// stripDriftFields removes drift diagnostic fields from YAML content.
func stripDriftFields(content string) string {
	result := driftFieldRe.ReplaceAllString(content, "")
	// Clean up any trailing blank lines left behind
	result = strings.TrimRight(result, "\n") + "\n"
	return result
}
