package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/link"
	"github.com/aitestmanagement/gtms-cli/internal/output"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// newLinkCmd builds the 'gtms link' command with its flags.
func newLinkCmd() *cobra.Command {
	var frameworkFlag string
	var artefactFlag string
	var checkFlag bool
	var forceFlag bool
	var strictFlag bool
	var refreshFlag bool
	var recursiveFlag bool
	var adapterFlag string
	var fromAdapterFlag string
	var allFlag bool
	var localDryRun bool

	cmd := &cobra.Command{
		Use:   "link [tc-id | folder]",
		Short: "Register a pre-existing test by writing a wiring record",
		Long: `Link a pre-existing test to a test case by writing a wiring record.

The user asserts that the test file follows the TC-ID naming convention.
GTMS checks the artefact file and the test case spec exist (filesystem only),
resolves the execute adapter configured for the framework, and writes the
wiring record with fresh hashes. No framework CLI is invoked.

Examples:
  gtms link tc-a1b2c3d4 --framework playwright --artefact tests/login.spec.ts
  gtms link tc-a1b2c3d4 --framework playwright --artefact tests/login.spec.ts --force
  gtms link tc-a1b2c3d4 --framework playwright --artefact tests/login.spec.ts --check
  gtms link tc-a1b2c3d4 --framework playwright --check   (re-check existing link)

Refresh mode (recompute hashes from current spec and artefact):
  gtms link --refresh tc-a1b2c3d4                     (refresh all frameworks for this TC)
  gtms link --refresh tc-a1b2c3d4 --framework bats    (refresh only the bats record)
  gtms link --refresh my-feature                      (refresh stale records in folder)
  gtms link --refresh my-feature -r                   (include subdirectories)

Repoint mode (reassign the stored execute adapter):
  gtms link tc-a1b2c3d4 --adapter runner-b                                    (single TC)
  gtms link tc-a1b2c3d4 --from-adapter runner-a --adapter runner-b            (with precondition)
  gtms link my-feature --from-adapter runner-a --adapter runner-b             (folder)
  gtms link my-feature -r --from-adapter runner-a --adapter runner-b          (recursive)
  gtms link --all --from-adapter runner-a --adapter runner-b                  (project-wide)
  gtms link my-feature --from-adapter runner-a --adapter runner-b --dry-run   (preview)`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetProjectRoot()

			// BUG-113: combine root-position and local-position --dry-run.
			dry := IsDryRun() || localDryRun

			// --- Repoint mode (ENH-192) ---
			if adapterFlag != "" {
				// Reject flags incompatible with repoint (ENH-192 flag-permutation table).
				if artefactFlag != "" {
					msg := "--artefact is not valid with --adapter repoint. Repoint changes only the adapter field."
					output.Errorf(msg, "Drop --artefact and retry.")
					return output.AsDisplayed(fmt.Errorf(msg))
				}
				if refreshFlag {
					msg := "--refresh is not valid with --adapter repoint. These are separate modes."
					output.Errorf(msg, "Use --refresh alone to recompute hashes, or --adapter alone to repoint.")
					return output.AsDisplayed(fmt.Errorf(msg))
				}
				if forceFlag {
					msg := "--force is not valid with --adapter repoint."
					output.Errorf(msg, "Repoint is idempotent -- rerun to update remaining records.")
					return output.AsDisplayed(fmt.Errorf(msg))
				}
				if checkFlag {
					msg := "--check is not valid with --adapter repoint. Use --dry-run to preview."
					output.Errorf(msg, "")
					return output.AsDisplayed(fmt.Errorf(msg))
				}
				if strictFlag {
					msg := "--strict is not valid with --adapter repoint."
					output.Errorf(msg, "")
					return output.AsDisplayed(fmt.Errorf(msg))
				}
				var target string
				if len(args) > 0 {
					target = strings.ToLower(args[0])
					target = normaliseTarget(target)
				}
				return runRepoint(root, target, adapterFlag, fromAdapterFlag, frameworkFlag, allFlag, recursiveFlag, dry)
			}

			// --from-adapter without --adapter is invalid.
			if fromAdapterFlag != "" {
				msg := "--from-adapter requires --adapter."
				output.Errorf(msg, "Example: gtms link tc-x --from-adapter old --adapter new")
				return output.AsDisplayed(fmt.Errorf(msg))
			}

			// --all without --adapter is invalid.
			if allFlag {
				msg := "--all requires --adapter for repoint mode."
				output.Errorf(msg, "Example: gtms link --all --from-adapter old --adapter new")
				return output.AsDisplayed(fmt.Errorf(msg))
			}

			// --dry-run outside repoint mode is rejected (BUG-146).
			if dry {
				msg := "--dry-run is not yet supported for non-repoint link operations."
				output.Errorf(msg, "Use --adapter to enter repoint mode; write, check, and refresh modes do not support --dry-run.")
				return output.AsDisplayed(fmt.Errorf(msg))
			}

			// Non-repoint modes require a positional argument.
			if len(args) == 0 {
				msg := "A test case ID or folder is required."
				output.Errorf(msg, "Example: gtms link tc-a1b2c3d4 --framework playwright --artefact tests/login.spec.ts")
				return output.AsDisplayed(fmt.Errorf(msg))
			}

			target := strings.ToLower(args[0])
			target = normaliseTarget(target)

			// --- Refresh mode (ENH-156) ---
			if refreshFlag {
				return runRefresh(root, target, frameworkFlag, artefactFlag, checkFlag, forceFlag, strictFlag, recursiveFlag)
			}

			// --- Standard link mode (original behavior) ---

			if err := validateTargetID(target); err != nil {
				msg := fmt.Sprintf("Invalid test case ID format: '%s'.", target)
				output.Errorf(msg, "Use format like tc-a1b2c3d4.")
				return output.AsDisplayed(fmt.Errorf(msg))
			}

			if !isTestCaseID(target) {
				msg := fmt.Sprintf("Invalid test case ID format: '%s'.", target)
				output.Errorf(msg, "Use format like tc-a1b2c3d4.")
				return output.AsDisplayed(fmt.Errorf(msg))
			}

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

			if checkFlag {
				result, err := link.CheckLink(root, target, frameworkFlag, artefactFlag, strictFlag)
				if err != nil {
					output.Errorf(err.Error(), "Check the artefact path and try again.")
					return output.AsDisplayed(err)
				}
				formatCheckOutput(result)
				return nil
			}

			if artefactFlag == "" {
				msg := "--artefact is required for gtms link."
				output.Errorf(msg, "Example: gtms link tc-a1b2c3d4 --framework playwright --artefact tests/login.spec.ts")
				return output.AsDisplayed(fmt.Errorf(msg))
			}

			cfg := GetConfig()
			wiringAdapter, warnings, err := link.LinkRecord(root, cfg, target, frameworkFlag, artefactFlag, forceFlag, strictFlag)
			if err != nil {
				output.Errorf(err.Error(), "Check the artefact path or use --force to overwrite.")
				return output.AsDisplayed(err)
			}

			fmt.Printf("  %s Linked: %s (%s)\n", output.IconComplete, target, frameworkFlag)
			for _, w := range warnings {
				output.Warnf(w)
			}
			if wiringAdapter != "" && IsVerbose() {
				fmt.Fprintf(os.Stderr, "Execute adapter for wiring: %s\n", wiringAdapter)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&frameworkFlag, "framework", "", "Test framework (e.g., playwright, bats)")
	cmd.Flags().StringVar(&artefactFlag, "artefact", "", "Path to the test artefact file")
	cmd.Flags().BoolVar(&checkFlag, "check", false, "Validate without writing (health check mode)")
	cmd.Flags().BoolVar(&forceFlag, "force", false, "Overwrite existing record")
	cmd.Flags().BoolVar(&strictFlag, "strict", false, "Reject phantom TC IDs that have no spec under gtms/test/cases/")
	cmd.Flags().BoolVar(&refreshFlag, "refresh", false, "Refresh stale wiring hashes in-place without retyping artefact paths")
	cmd.Flags().BoolVarP(&recursiveFlag, "recursive", "r", false, "Include subdirectories in folder or repoint scope")
	cmd.Flags().StringVar(&adapterFlag, "adapter", "", "Repoint: new execute adapter name")
	cmd.Flags().StringVar(&fromAdapterFlag, "from-adapter", "", "Repoint: filter by current adapter (mandatory for bulk)")
	cmd.Flags().BoolVar(&allFlag, "all", false, "Repoint: project-wide scope (requires --from-adapter)")
	cmd.Flags().BoolVar(&localDryRun, "dry-run", false, "Preview repoint without writing")

	return cmd
}

// runRefresh handles the --refresh path for the link command (ENH-156).
//
// Reject incompatible flags first, then dispatch to single-TC or folder mode.
func runRefresh(root, target, frameworkFlag, artefactFlag string, checkFlag, forceFlag, strictFlag, recursive bool) error {
	// Reject incompatible flags.
	if artefactFlag != "" {
		msg := "--artefact is not valid with --refresh. Refresh sources the artefact from the existing wiring record."
		output.Errorf(msg, fmt.Sprintf("To change the artefact path, use: gtms link %s --framework <fw> --artefact <path> --force", target))
		return output.AsDisplayed(fmt.Errorf(msg))
	}
	if checkFlag {
		msg := "--check is not valid with --refresh. To inspect stale wiring, use: gtms status"
		output.Errorf(msg, "")
		return output.AsDisplayed(fmt.Errorf(msg))
	}
	if forceFlag {
		msg := "--force is not valid with --refresh. Refresh is itself the explicit acknowledgement."
		output.Errorf(msg, "")
		return output.AsDisplayed(fmt.Errorf(msg))
	}
	if strictFlag {
		msg := "--strict is not valid with --refresh."
		output.Errorf(msg, "")
		return output.AsDisplayed(fmt.Errorf(msg))
	}

	// Validate target safety.
	if err := validateTargetID(target); err != nil {
		output.Errorf(err.Error(),
			"Use only letters, numbers, dashes, underscores, dots, and forward slashes.")
		return output.AsDisplayed(err)
	}

	// Validate --framework if provided.
	if frameworkFlag != "" && !config.ValidateFramework(frameworkFlag) {
		msg := fmt.Sprintf("Invalid framework '%s'. Framework must contain only lowercase letters, digits, and hyphens.", frameworkFlag)
		output.Errorf(msg, "Example: --framework playwright")
		return output.AsDisplayed(fmt.Errorf(msg))
	}

	// Dispatch: folder (bulk) vs single-TC.
	if IsBulkFolder(root, target) {
		return runFolderRefresh(root, target, frameworkFlag, recursive)
	}

	// Single-TC mode: validate TC ID format.
	if !isTestCaseID(target) {
		msg := fmt.Sprintf("'%s' is not a valid test case ID or an existing folder under gtms/test/cases/.", target)
		output.Errorf(msg, "Use format like tc-a1b2c3d4 or provide a folder name.")
		return output.AsDisplayed(fmt.Errorf(msg))
	}

	return runSingleTCRefresh(root, target, frameworkFlag)
}

// runSingleTCRefresh handles --refresh for a single TC ID (ENH-156).
func runSingleTCRefresh(root, tcID, frameworkFlag string) error {
	var records []*wiring.WiringRecord

	if frameworkFlag != "" {
		rec, _, err := wiring.Find(root, tcID, frameworkFlag)
		if err != nil {
			msg := fmt.Sprintf("Reading wiring for %s--%s: %v", tcID, frameworkFlag, err)
			output.Errorf(msg, "Check the wiring file for parse errors.")
			return output.AsDisplayed(fmt.Errorf(msg))
		}
		if rec == nil {
			msg := fmt.Sprintf("No wiring record found for '%s' (framework: %s).", tcID, frameworkFlag)
			output.Errorf(msg, fmt.Sprintf("Run 'gtms link %s --framework %s --artefact <path>' to create it.", tcID, frameworkFlag))
			return output.AsDisplayed(fmt.Errorf(msg))
		}
		records = []*wiring.WiringRecord{rec}
	} else {
		recs, err := wiring.FindAllForTC(root, tcID)
		if err != nil {
			msg := fmt.Sprintf("Scanning wiring for %s: %v", tcID, err)
			output.Errorf(msg, "Check gtms/automation/wiring/ for parse errors.")
			return output.AsDisplayed(fmt.Errorf(msg))
		}
		if len(recs) == 0 {
			msg := fmt.Sprintf("No wiring records found for '%s'.", tcID)
			output.Errorf(msg, fmt.Sprintf("Run 'gtms link %s --framework <fw> --artefact <path>' to create one.", tcID))
			return output.AsDisplayed(fmt.Errorf(msg))
		}
		records = recs
	}

	failCount := 0
	for _, rec := range records {
		refreshed, err := link.RefreshRecord(root, rec)
		if err != nil {
			failCount++
			fmt.Printf("  %s %s (%s): %s\n", output.IconError, tcID, rec.Framework, err.Error())
			continue
		}
		if refreshed {
			fmt.Printf("  %s Refreshed: %s (%s)\n", output.IconComplete, tcID, rec.Framework)
		} else {
			fmt.Printf("  %s Current: %s (%s)\n", output.IconSkipped, tcID, rec.Framework)
		}
	}

	if failCount > 0 {
		msg := fmt.Sprintf("%d of %d wiring records failed to refresh for %s", failCount, len(records), tcID)
		return output.AsDisplayed(fmt.Errorf(msg))
	}
	return nil
}

// runFolderRefresh handles --refresh for a folder target (ENH-156).
//
// Spec-first selection: discover TC IDs under the folder, enumerate wiring
// for each, filter to stale records, refresh those, report current as no-ops.
func runFolderRefresh(root, folder, frameworkFlag string, recursive bool) error {
	tcIDs, err := DiscoverTestCases(root, folder, recursive)
	if err != nil {
		output.Errorf(err.Error(), "Check that gtms/test/cases/"+folder+"/ contains tc-*.md files.")
		return output.AsDisplayed(err)
	}

	failCount := 0
	refreshedCount := 0
	currentCount := 0
	noWiringCount := 0

	for _, tcID := range tcIDs {
		var records []*wiring.WiringRecord

		if frameworkFlag != "" {
			rec, _, findErr := wiring.Find(root, tcID, frameworkFlag)
			if findErr != nil {
				failCount++
				fmt.Printf("  %s %s (%s): %s\n", output.IconError, tcID, frameworkFlag, findErr.Error())
				continue
			}
			if rec == nil {
				noWiringCount++
				continue
			}
			records = []*wiring.WiringRecord{rec}
		} else {
			recs, findErr := wiring.FindAllForTC(root, tcID)
			if findErr != nil {
				failCount++
				fmt.Printf("  %s %s: %s\n", output.IconError, tcID, findErr.Error())
				continue
			}
			if len(recs) == 0 {
				noWiringCount++
				continue
			}
			records = recs
		}

		for _, rec := range records {
			refreshed, refErr := link.RefreshRecord(root, rec)
			if refErr != nil {
				failCount++
				fmt.Printf("  %s %s (%s): %s\n", output.IconError, tcID, rec.Framework, refErr.Error())
				continue
			}
			if refreshed {
				refreshedCount++
				fmt.Printf("  %s Refreshed: %s (%s)\n", output.IconComplete, tcID, rec.Framework)
			} else {
				currentCount++
				fmt.Printf("  %s Current: %s (%s)\n", output.IconSkipped, tcID, rec.Framework)
			}
		}
	}

	// Summary
	fmt.Printf("\n  %d refreshed, %d current, %d failed\n", refreshedCount, currentCount, failCount)

	if failCount > 0 {
		msg := fmt.Sprintf("%d wiring records failed to refresh in %s/", failCount, folder)
		return output.AsDisplayed(fmt.Errorf(msg))
	}
	return nil
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

// runRepoint handles the --adapter repoint path (ENH-192).
func runRepoint(root, target, adapterFlag, fromAdapterFlag, frameworkFlag string, allFlag, recursive, dryRun bool) error {
	// Flag-combination rejections.
	if err := validateRepointFlags(target, adapterFlag, fromAdapterFlag, allFlag, recursive); err != nil {
		return err
	}

	// CODEX-010: -r is a folder-scope modifier. Reject it for a single-TC target
	// before any adapter validation or mutation. (--all already rejects -r in
	// validateRepointFlags.)
	if recursive && !allFlag && target != "" && !IsBulkFolder(root, target) {
		msg := "-r is only valid for a folder repoint, not a single test case."
		output.Errorf(msg, "Drop -r, or target a folder under gtms/test/cases/.")
		return output.AsDisplayed(fmt.Errorf(msg))
	}

	// The new adapter (existence + Mode-3 exclusion + framework derivation) is
	// validated INSIDE the core repoint operation, so the CLI only builds options
	// and dispatches by scope (CODEX-016).
	opts := link.RepointOptions{
		Config:        GetConfig(),
		FromAdapter:   fromAdapterFlag,
		NewAdapter:    adapterFlag,
		FrameworkFlag: frameworkFlag,
		DryRun:        dryRun,
	}

	// Determine scope.
	if allFlag {
		return runRepointAll(root, opts)
	}

	// Validate target safety.
	if err := validateTargetID(target); err != nil {
		output.Errorf(err.Error(),
			"Use only letters, numbers, dashes, underscores, dots, and forward slashes.")
		return output.AsDisplayed(err)
	}

	// Folder (bulk) vs single-TC.
	if IsBulkFolder(root, target) {
		if fromAdapterFlag == "" {
			msg := "--from-adapter is required for folder repoint."
			output.Errorf(msg, fmt.Sprintf("Example: gtms link %s --from-adapter old --adapter %s", target, adapterFlag))
			return output.AsDisplayed(fmt.Errorf(msg))
		}
		return runRepointFolder(root, target, recursive, opts)
	}

	// Single-TC mode.
	if !isTestCaseID(target) {
		msg := fmt.Sprintf("'%s' is not a valid test case ID or an existing folder under gtms/test/cases/.", target)
		output.Errorf(msg, "Use format like tc-a1b2c3d4 or provide a folder name.")
		return output.AsDisplayed(fmt.Errorf(msg))
	}

	return runRepointSingleTC(root, target, opts)
}

// validateRepointFlags rejects invalid flag combinations for repoint mode.
func validateRepointFlags(target, adapterFlag, fromAdapterFlag string, allFlag, recursive bool) error {
	// --all exclusivity.
	if allFlag {
		if target != "" {
			msg := "--all rejects a positional target."
			output.Errorf(msg, "Use: gtms link --all --from-adapter old --adapter new")
			return output.AsDisplayed(fmt.Errorf(msg))
		}
		if fromAdapterFlag == "" {
			msg := "--from-adapter is required with --all."
			output.Errorf(msg, "Example: gtms link --all --from-adapter old --adapter new")
			return output.AsDisplayed(fmt.Errorf(msg))
		}
		if recursive {
			msg := "-r is redundant with --all (--all already covers the entire project)."
			output.Errorf(msg, "Use: gtms link --all --from-adapter old --adapter new")
			return output.AsDisplayed(fmt.Errorf(msg))
		}
	}

	// --adapter without a target or --all is invalid.
	if !allFlag && target == "" {
		msg := "A test case ID or folder is required for repoint, or use --all."
		output.Errorf(msg, "Example: gtms link tc-x --adapter new")
		return output.AsDisplayed(fmt.Errorf(msg))
	}

	return nil
}

// runRepointSingleTC delegates single-TC repoint to the core link package and
// renders the result. Adapter resolution and the four-case --from-adapter
// precondition live in link.RepointSingle (CODEX-008/CODEX-016).
func runRepointSingleTC(root, tcID string, opts link.RepointOptions) error {
	res, err := link.RepointSingle(root, tcID, opts)
	if err != nil {
		output.Errorf(err.Error(), "Check gtms.config adapters.execute, the current wiring ('gtms status'), or run 'gtms automate' to create it.")
		return output.AsDisplayed(err)
	}
	if res.Status == "already-current" {
		fmt.Printf("  %s Already current: %s (%s) adapter %s\n",
			output.IconSkipped, res.TestCase, res.Framework, res.NewAdapter)
		return nil
	}
	formatRepointResult(res, opts.DryRun)
	if res.Status == "error" {
		return output.AsDisplayed(res.Error)
	}
	return nil
}

// runRepointFolder delegates folder/recursive repoint to the core link package
// and renders the result (CODEX-008/CODEX-016).
func runRepointFolder(root, folder string, recursive bool, opts link.RepointOptions) error {
	summary, err := link.RepointBulk(root, folder, recursive, opts)
	if err != nil {
		output.Errorf(err.Error(), "Check gtms.config adapters.execute, or resolve the scope/ambiguity issue before repointing.")
		return output.AsDisplayed(err)
	}
	formatRepointSummary(summary, opts.FromAdapter, opts.DryRun)
	if summary.Errors > 0 {
		return output.AsDisplayed(fmt.Errorf("%d repoint errors in %s/", summary.Errors, folder))
	}
	return nil
}

// runRepointAll delegates project-wide repoint to the core link package and
// renders the result (CODEX-008/CODEX-016).
func runRepointAll(root string, opts link.RepointOptions) error {
	summary, err := link.RepointAll(root, opts)
	if err != nil {
		output.Errorf(err.Error(), "Check gtms.config adapters.execute or gtms/automation/wiring/ for issues.")
		return output.AsDisplayed(err)
	}
	formatRepointSummary(summary, opts.FromAdapter, opts.DryRun)
	if summary.Errors > 0 {
		return output.AsDisplayed(fmt.Errorf("%d repoint errors", summary.Errors))
	}
	return nil
}

// formatRepointResult prints a single repoint outcome.
func formatRepointResult(res link.RepointResult, dryRun bool) {
	prefix := ""
	if dryRun {
		prefix = "[dry-run] "
	}
	switch res.Status {
	case "repointed":
		fmt.Printf("  %s %sRepointed: %s (%s) %s -> %s\n",
			output.IconComplete, prefix, res.TestCase, res.Framework, res.OldAdapter, res.NewAdapter)
	case "error":
		fmt.Printf("  %s %s (%s): %s\n",
			output.IconError, res.TestCase, res.Framework, res.Error)
	}
	if res.Warning != "" {
		output.Warnf(res.Warning)
	}
}

// formatRepointSummary prints all results and the summary line.
func formatRepointSummary(summary link.RepointSummary, fromAdapter string, dryRun bool) {
	for _, res := range summary.Results {
		formatRepointResult(res, dryRun)
	}

	if summary.Repointed == 0 && summary.Errors == 0 {
		fmt.Printf("\n  0 repointed, no records matched adapter %q\n", fromAdapter)
		return
	}

	prefix := ""
	if dryRun {
		prefix = "[dry-run] "
	}
	fmt.Printf("\n  %s%d repointed, %d skipped, %d warnings, %d errors\n",
		prefix, summary.Repointed, summary.Skipped, summary.Warnings, summary.Errors)
}
