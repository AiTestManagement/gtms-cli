package cli

import (
	"fmt"
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

	cmd := &cobra.Command{
		Use:   "link <tc-id | folder>",
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
  gtms link --refresh my-feature -r                   (include subdirectories)`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetProjectRoot()
			target := strings.ToLower(args[0])
			target = normaliseTarget(target)

			// --- Refresh mode (ENH-156) ---
			if refreshFlag {
				return runRefresh(root, target, frameworkFlag, artefactFlag, checkFlag, forceFlag, strictFlag, recursiveFlag)
			}

			// --- Standard link mode (original behavior) ---

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

			// Delegate to core link package. cfg is required to resolve the
			// canonical execute adapter for wiring records (CON-023 / ENH-145).
			cfg := GetConfig()
			warnings, err := link.LinkRecord(root, cfg, target, frameworkFlag, artefactFlag, forceFlag, strictFlag)
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

	cmd.Flags().StringVar(&frameworkFlag, "framework", "", "Test framework (e.g., playwright, bats)")
	cmd.Flags().StringVar(&artefactFlag, "artefact", "", "Path to the test artefact file")
	cmd.Flags().BoolVar(&checkFlag, "check", false, "Validate without writing (health check mode)")
	cmd.Flags().BoolVar(&forceFlag, "force", false, "Overwrite existing record")
	cmd.Flags().BoolVar(&strictFlag, "strict", false, "Reject phantom TC IDs that have no spec under gtms/test/cases/")
	cmd.Flags().BoolVar(&refreshFlag, "refresh", false, "Refresh stale wiring hashes in-place without retyping artefact paths")
	cmd.Flags().BoolVarP(&recursiveFlag, "recursive", "r", false, "Include subdirectories in folder refresh mode")

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
