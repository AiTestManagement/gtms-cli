package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aitestmanagement/gtms-cli/internal/adapter"
	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/output"
	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
	targetpkg "github.com/aitestmanagement/gtms-cli/internal/target"
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
		Short: "Automate test cases",
		Long: `Automate a test case by delegating to a configured adapter.

Single test case:
  gtms automate tc-a1b2c3d4                         -- use default adapter
  gtms automate tc-a1b2c3d4 --adapter agent-automate -- use specific adapter
  gtms automate tc-a1b2c3d4 --framework playwright  -- specify test framework
  gtms automate tc-a1b2c3d4 --context-file docs/standards.md -- pass supplementary context

Folder (bulk mode):
  gtms automate my-feature                     -- automate all test cases in gtms/test/cases/my-feature/
  gtms automate my-feature -r                  -- include test cases from subdirectories
  gtms automate my-feature --force             -- reprocess all test cases (ignore skip logic)
  gtms automate my-feature --fail-fast         -- stop on first error

All test cases:
  gtms automate -r                             -- automate all test cases across all folders
  gtms automate                                -- same; recursion is implied when no folder is given

Adapter execution:
  Runs the adapter from --adapter or the gtms.config default
  (built-in options: agent-automate, manual-automate).
  Adapters run identically on every OS.
  See "Adapter Execution Model" in USER-GUIDE.md.`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cfg := GetConfig()
			root := GetProjectRoot()

			// Validate: git repo exists and git can be run (ENH-189)
			if err := requireGitRepo(ctx, root, notARepoHintDefault); err != nil {
				return err
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

			// BUG-121: Validate --framework flag if provided (mirrors execute.go:113).
			// Guard sits before the bulk/single branch so both paths are covered.
			if frameworkFlag != "" && !config.ValidateFramework(frameworkFlag) {
				msg := fmt.Sprintf("Invalid framework '%s'. Framework must contain only lowercase letters, digits, and hyphens.", frameworkFlag)
				output.Errorf(msg, "Example: --framework playwright")
				return output.AsDisplayed(fmt.Errorf(msg))
			}

			// No argument = run all test cases from root (recursive implied)
			if len(args) == 0 {
				recursiveFlag = true
				return runBulkAutomate(cmd, root, "", adapterFlag, frameworkFlag, environmentFlag, executedBy, forceFlag, failFastFlag, recursiveFlag, absContextPath, contextContent)
			}

			// BUG-167: shared classifier -- preserves folder case, normalises
			// separators, and lowercases/strips only confirmed TC IDs.
			res, err := targetpkg.Classify(root, args[0])
			if err != nil {
				// BUG-167 follow-up (BUG-058 compatibility): retain the historical
				// generic "Invalid test case ID format" message AND surface the
				// classifier's specific cause (traversal/prefix/invalid chars).
				msg, hint := invalidTargetFormatError(args[0], err)
				output.Errorf(msg, hint)
				return output.AsDisplayed(err)
			}

			// Disambiguate: folder (bulk mode) vs single test case ID.
			// BUG-167 follow-up: enter bulk mode ONLY for an EXISTING folder. A
			// safe-but-nonexistent folder-shaped arg falls through to the historical
			// single-TC "Invalid test case ID format" response (frozen tc-13dbade0,
			// ENH-052 contract). Real mixed-case/backslash folders exist, so
			// IsBulkFolder is true and they still enter bulk mode correctly.
			if res.Kind == targetpkg.KindFolder {
				if IsBulkFolder(root, res.Target) {
					return runBulkAutomate(cmd, root, res.Target, adapterFlag, frameworkFlag, environmentFlag, executedBy, forceFlag, failFastFlag, recursiveFlag, absContextPath, contextContent)
				}
				msg := fmt.Sprintf("Invalid test case ID format: '%s'.", sanitizeForError(res.Target))
				output.Errorf(msg, "Use format like tc-a1b2c3d4.")
				return output.AsDisplayed(fmt.Errorf(msg))
			}

			// --- Single TC mode (existing behavior) ---
			// ADR-024 s5: resolve identity ONCE here and pass the resolved owner
			// forward. Everything downstream -- the adapter's source file, post-fill
			// validation, hashing, the wiring write -- consumes `owner` rather than
			// re-deriving the id for itself. The scope guard (BUG-059 / REV-113), the
			// project-wide ambiguity guard (REV-120), and the ownership refusal
			// (BUG-185 / BUG-188) all run inside it, in that order, against one snapshot.
			owner, hint, err := resolveTargetOwner(root, res, "Check the folder qualifier, resolve any duplicate IDs, or drop the qualifier.")
			if err != nil {
				if errors.Is(err, testcase.ErrOwnerNotFound) {
					msg := fmt.Sprintf("Test case '%s' not found in gtms/test/cases/", res.Target)
					output.Errorf(msg, "Create the test case first with 'gtms create'.")
					return output.AsDisplayed(fmt.Errorf(msg))
				}
				output.Errorf(err.Error(), hint)
				return output.AsDisplayed(err)
			}

			// Flat wiring/task/invoke key (bare ID) for the rest of the path.
			target := res.TCID

			// ENH-150: Post-fill validation gate -- catches frontmatter corruption
			// before the adapter is invoked. It validates the RESOLVED OWNER; it is
			// not handed the id to go looking for a file itself.
			if violations := adapter.ValidateTestCaseOwner(root, owner); len(violations) > 0 {
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
				Owner:       owner,
				Adapter:     adapterFlag,
				Framework:   frameworkFlag,
				Environment: environmentFlag,
				ExecutedBy:  executedBy,
				ContextFile: absContextPath,
				Context:     contextContent,
				Force:       forceFlag,
			}

			// BUG-171 / REV-125: when the built-in automate path will fail its
			// pre-run prerequisites (unsupported framework, or supported but no
			// configured runner), the invocation fails fast with a `✗`
			// diagnostic. Suppress the progress spinner in that case so its
			// static non-TTY line does not precede the headline on stderr. This
			// reuses the SAME adapter-layer predicate as the bulk preflight -- it
			// only decides whether to show the spinner and adds no diagnostic
			// semantics. All tier/support/runner knowledge stays behind
			// CheckTier0AutomatePrereqs; the CLI never branches on tier.
			fw := adapter.ResolveFramework(resolved, frameworkFlag)
			prereqFails := adapter.CheckTier0AutomatePrereqs(cfg, resolved, fw) != nil

			// Start spinner for sync adapters
			var spinner *output.Spinner
			if resolved.Mode == "sync" && !prereqFails {
				spinner = output.NewSpinner(os.Stderr, fmt.Sprintf("Running %s...", resolved.Name))
				spinner.Start()
			}

			result, err := adapter.InvokeWithRoot(ctx, root, cfg, resolved, target, flags)

			// Stop spinner BEFORE any output
			if spinner != nil {
				spinner.Stop()
			}

			if err != nil {
				// BUG-171: render command-specific guidance when the framework
				// has no configured execute adapter, instead of leaking the raw
				// internal invocation chain to Cobra. The error task + result
				// contract are already produced by the invoker (error lifecycle
				// preserved); we only own the presentation here.
				var mae *adapter.MissingExecuteAdapterError
				if errors.As(err, &mae) {
					renderMissingExecuteAdapter(mae, target)
					return output.AsDisplayed(err)
				}
				return err
			}

			// Format output
			formatAutomateOutput(result)

			return nil
		},
	}

	cmd.Flags().StringVar(&adapterFlag, "adapter", "", "Adapter to use (overrides default)")
	cmd.Flags().StringVar(&frameworkFlag, "framework", "", "Test framework label (e.g., playwright, bats); overrides the adapter's configured framework")
	cmd.Flags().StringVar(&environmentFlag, "env", "", "Environment label recorded and passed to the adapter; GTMS does not route on it")
	cmd.Flags().StringVar(&executedByFlag, "executed-by", "", "Identity to record on the executed_by field (defaults to GTMS_EXECUTED_BY, then git user.name)")
	cmd.Flags().StringVar(&contextFileFlag, "context-file", "", "Path to context file (coding standards, API docs, etc.)")
	cmd.Flags().BoolVar(&forceFlag, "force", false, "Reprocess even if already automated (bulk skip checks bypassed; GTMS replaces only output files this test case provably owns, and ownership is required with or without --force; an external adapter's own writes are outside that guarantee)")
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

	// ADR-024 / BUG-185 (tc-dbc64e99) + BUG-188: the work-set is the project-wide
	// body-identity OWNERS under the folder, and the whole run refuses before any
	// adapter is invoked if any is Ambiguous (incl. a renamed owner outside the
	// folder) or Unprovable. Deriving the ids from resolved body identity -- not
	// the filename-truncated DiscoverTestCases set -- is what keeps an extended-id
	// sibling (tc-x--legacy) from being truncated to tc-x, skipped, and the wrong
	// id invoked against an adapter.
	owners, err := resolveBulkOwners(root, folder, recursive)
	if err != nil {
		var oe *testcase.OwnershipError
		if errors.As(err, &oe) {
			output.Errorf(oe.Error(), "")
			return output.AsDisplayed(err)
		}
		output.Errorf(err.Error(), "Check that gtms/test/cases/"+folder+"/ contains tc-*.md files.")
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

	// BUG-171 / REV-125 (settled contract): for the Tier 0 built-in automate
	// path, a failed pre-run prerequisite is a batch-wide failure, so we
	// validate ONCE before the loop, emit one diagnostic, and create no per-TC
	// task/result/artefact/wiring state. CheckTier0AutomatePrereqs centralises
	// the ordering (framework support FIRST, then canonical runner) and the
	// tier gate, so the CLI never branches on tier and never diverges from what
	// the single-TC invoke would report:
	//   - unsupported framework (e.g. cypress) -> the established
	//     unsupported-framework diagnostic (adding a runner cannot help);
	//   - supported framework with no runner   -> BUG-171's missing-runner
	//     guidance (renderMissingExecuteAdapter).
	// Not applicable for Tier 1/2 adapters or for framework ""/"manual" (the
	// helper returns nil), so a Tier 1 bulk run still iterates.
	if prereqErr := adapter.CheckTier0AutomatePrereqs(cfg, resolved, framework); prereqErr != nil {
		var unsupported *adapter.UnsupportedAutomateFrameworkError
		var mae *adapter.MissingExecuteAdapterError
		switch {
		case errors.As(prereqErr, &unsupported):
			output.Errorf(prereqErr.Error(), "")
			return output.AsDisplayed(prereqErr)
		case errors.As(prereqErr, &mae):
			renderMissingExecuteAdapter(mae, "<tc-id>")
			return output.AsDisplayed(prereqErr)
		}
	}

	// ADR-024 artefact ownership: scan and validate every wiring record ONCE,
	// here, BEFORE the batch mutates anything. Three things follow from doing it
	// here rather than per item.
	//
	// Completeness is a batch-wide precondition, and it is enforced with the same
	// policy the prerequisite check above already uses: one diagnostic, no
	// per-TC task/result/artefact/wiring state, nothing invoked. A corrupt wiring
	// record cannot prove who owns the artefacts this batch is about to write
	// over, and discovering that at item 40 -- after 39 items have already
	// changed the tree -- is not a refusal, it is a partial run.
	//
	// Reads stay O(wiring records) for the whole batch instead of
	// O(test cases x wiring records), which is the REV-159 regression shape.
	//
	// And the set is MUTABLE operation state, not a frozen snapshot: each
	// invocation reserves its planned path before its adapter runs and commits it
	// once its wiring is written, so item 40 is judged against what items 1..39
	// actually claimed. A snapshot taken here and never updated would be stale in
	// exactly the way that lets two test cases write the same artefact.
	claims, claimsErr := adapter.NewArtefactClaimSet(root)
	if claimsErr != nil {
		output.Errorf(claimsErr.Error(), "")
		return output.AsDisplayed(claimsErr)
	}

	total := len(owners)
	recursiveLabel := ""
	if recursive {
		recursiveLabel = " (recursive)"
	}
	scope := "gtms/test/cases/"
	if folder != "" {
		scope = fmt.Sprintf("gtms/test/cases/%s/", folder)
	}
	fmt.Printf("Processing %d test cases in %s%s...\n", total, scope, recursiveLabel)

	succeeded := 0
	skipped := 0
	failed := 0

	for i, owner := range owners {
		tcID := owner.ID()
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

		// Build flags for this TC.
		//
		// REV-158 CODEX-001: Owner is REQUIRED here. The loop already iterates
		// resolved owners, but the flags used to omit the owner, so the adapter
		// received an empty source path and empty test-case content, and the
		// post-invocation WriteAutomateWiring failed with "no resolved owner" --
		// a failure downgraded to a warning, so bulk automate reported success
		// while writing no wiring record at all (ADR-024 s5).
		flags := adapter.CommandFlags{
			Owner: owner,
			// The batch's live ownership state, scanned once above and updated by
			// each invocation. Passing it is what keeps wiring reads O(N) and what
			// lets this item see the artefacts earlier items claimed.
			Claims:      claims,
			Adapter:     adapterFlag,
			Framework:   frameworkFlag,
			Environment: environmentFlag,
			ExecutedBy:  executedBy,
			ContextFile: contextFilePath,
			Context:     contextContent,
			Force:       force,
		}

		// Progress indicator instead of spinner (TTY only -- \r doesn't work in pipes)
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
// is already automated. The legacy automation-record lookup is retired --
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
	// BUG-170: gtms/tasks/ is disposable, regenerable derived state (ADR-011).
	// The task layer creates the status directory it writes into, so we must not
	// require gtms/tasks/ to pre-exist. Only committed source dirs are required.
	// This validator also serves execute (execute.go calls it), so execute
	// inherits the same tolerance and the same gtms/automation/ requirement.
	required := []string{paths.TestCases, paths.Automation}
	for _, dir := range required {
		path := filepath.Join(root, dir)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			msg := fmt.Sprintf("Required directory '%s' not found.", dir)
			output.Errorf(msg, fmt.Sprintf("Create the GTMS folder structure: %s/, %s/", paths.TestCases, paths.Automation))
			return output.AsDisplayed(fmt.Errorf(msg))
		}
	}
	return nil
}

// isTestCaseID delegates to target.IsTestCaseID (BUG-167). Retained as an
// unexported cli wrapper for the control commands (gaps) and link's internal
// TC-vs-folder branches that still call it directly.
func isTestCaseID(id string) bool {
	return targetpkg.IsTestCaseID(id)
}

// truncateReason shortens a reason string for display in progress output.
func truncateReason(reason string, maxLen int) string {
	if len(reason) <= maxLen {
		return reason
	}
	return reason[:maxLen-3] + "..."
}

// renderMissingExecuteAdapter prints BUG-171's command-specific automate
// guidance in the repo `✗ {what} / {what-to-do}` format. The capitalized
// headline is built from the typed error's Framework field (never echoing the
// raw lowercase adapter message), and the hint names BOTH valid recoveries for
// an already-initialised project -- configure an adapters.execute runner, or
// stay on the manual prime path -- while keeping the configured-adapter
// inventory. primeTarget is the TC ID in single mode, or a "<tc-id>"
// placeholder in bulk mode where no single target applies.
func renderMissingExecuteAdapter(mae *adapter.MissingExecuteAdapterError, primeTarget string) {
	headline := fmt.Sprintf("No execute adapter configured for framework %q", mae.Framework)
	hint := fmt.Sprintf(
		"Configured execute adapters: %s.\n"+
			"    To automate %s test cases, add an execute adapter for framework %q under adapters.execute (framework: %s) in gtms.config,\n"+
			"    or stay on the manual prime path: gtms prime %s --framework manual",
		mae.ConfiguredAdapters(), mae.Framework, mae.Framework, mae.Framework, primeTarget)
	output.Errorf(headline, hint)
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

	// ENH-120: artefact-focused headline -- surface the produced artefact path.
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

	// ENH-191 wiring-time report (AC7): emit the selected execute adapter on
	// the sync automate path under -v. The async completion path
	// (status_common.go) and gtms link emit the same line on their own paths.
	if res.WiringAdapter != "" && IsVerbose() {
		fmt.Fprintf(os.Stderr, "Execute adapter for wiring: %s\n", res.WiringAdapter)
	}

	if res.Mode == "async" {
		fmt.Println("    Check progress: gtms automate status")
	}

	for _, w := range res.Warnings {
		output.Warnf(w)
	}

	printCommandGuidance("automate", whatHappenedAutomate(res))
}
