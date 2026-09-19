package cli

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/aitestmanagement/gtms-cli/internal/output"
	"github.com/aitestmanagement/gtms-cli/internal/reader"
	targetpkg "github.com/aitestmanagement/gtms-cli/internal/target"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
	"github.com/spf13/cobra"
)

func newResetCmd() *cobra.Command {
	var recursive bool
	var localDryRun bool

	cmd := &cobra.Command{
		Use:   "reset [test-case-id | folder]",
		Short: "Clear recorded results so test cases can be re-run",
		Long: `Clear recorded results so the pipeline dashboard returns to a clean state.
For each matched test case this removes all handoff history under
.gtms/results/ (create, automate, execute, prime) and its finished execute
task files. Wiring records and test case specs are untouched.

  gtms reset                    -- clear root-level test case results
  gtms reset bug-022            -- clear results in gtms/test/cases/bug-022/
  gtms reset bug-022 -r         -- clear results in bug-022/ and subdirectories
  gtms reset -r                 -- clear all results across all folders
  gtms reset tc-a1b2c3d0        -- clear result for a single test case
  gtms reset --dry-run -r       -- show what would be cleared without modifying`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetProjectRoot()
			// BUG-113: honour both root-position (gtms --dry-run reset)
			// and local (gtms reset --dry-run) flag sources.
			dry := IsDryRun() || localDryRun

			if len(args) > 0 {
				// BUG-167: shared classifier -- folder case preserved (existence
				// first), only confirmed TC IDs lowercased/stripped.
				res, err := targetpkg.Classify(root, args[0])
				if err != nil {
					output.Errorf(err.Error(), "")
					return output.AsDisplayed(err)
				}
				if res.Kind == targetpkg.KindTCID {
					// BUG-059 (REV-113): a folder-qualified TC must resolve scoped
					// before this mutating command clears state for the bare ID.
					if err := requireScopedSpec(root, res); err != nil {
						output.Errorf(err.Error(), "")
						return output.AsDisplayed(err)
					}
					// ADR-024 s3 / REV-158 CLAUDE-001: carry the folder qualifier into
					// the core so reset can require MUTATION AUTHORITY under it.
					qualifiedScope, qerr := qualifiedTargetScope(root, res)
					if qerr != nil {
						output.Errorf(qerr.Error(), "")
						return output.AsDisplayed(qerr)
					}
					return runReset(os.Stdout, root, qualifiedScope, res.TCID, dry)
				}
				scope, serr := buildScopeFromArg(root, res.Target, recursive)
				if serr != nil {
					output.Errorf(serr.Error(), "")
					return output.AsDisplayed(serr)
				}
				return runReset(os.Stdout, root, scope, "", dry)
			}

			scope, serr := buildScopeFromArg(root, "", recursive)
			if serr != nil {
				output.Errorf(serr.Error(), "")
				return output.AsDisplayed(serr)
			}
			return runReset(os.Stdout, root, scope, "", dry)
		},
	}

	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "Include test cases from subdirectories")
	cmd.Flags().BoolVar(&localDryRun, "dry-run", false, "Preview what would be cleared without executing")

	return cmd
}

// runReset executes the reset operation and prints the result summary.
func runReset(w io.Writer, projectRoot string, scope *reader.ScopeInfo, tcID string, dryRun bool) error {
	result, err := reader.ResetExecuteResults(projectRoot, scope, tcID, dryRun)
	if err != nil {
		// ADR-024 / BUG-185 (tc-2ceabc97): reset is destructive by effect. An
		// ambiguous or unprovable selected id refuses before any mutation, and the
		// refusal MUST be visible -- non-zero exit AND an actionable diagnostic on
		// stderr, never a silent non-zero exit (REV-156 CLAUDE-003).
		var oe *testcase.OwnershipError
		if errors.As(err, &oe) {
			output.Errorf(oe.Error(), "")
			return output.AsDisplayed(err)
		}
		// ADR-024 s3 / REV-158 CLAUDE-001: reached through a link, but the spec
		// lives outside the named folder.
		var sae *testcase.ScopeAuthorityError
		if errors.As(err, &sae) {
			output.Errorf(sae.Error(), "")
			return output.AsDisplayed(err)
		}
		return err
	}

	if dryRun {
		if result.TestCasesAffected == 0 {
			fmt.Fprintln(w, "[dry-run] Nothing to clear.")
			return nil
		}
		fmt.Fprintf(w, "[dry-run] Would clear execute results for %d test case(s):\n", result.TestCasesAffected)
		fmt.Fprintf(w, "  Automation records: %d\n", result.AutomationRecordsCleared)
		fmt.Fprintf(w, "  Task files: %d\n", result.TaskFilesRemoved)
		return nil
	}

	if result.TestCasesAffected == 0 {
		fmt.Fprintln(w, "Nothing to clear.")
		return nil
	}

	fmt.Fprintf(w, "Cleared execute results for %d test case(s) (%d automation records updated, %d task files removed)\n",
		result.TestCasesAffected, result.AutomationRecordsCleared, result.TaskFilesRemoved)
	return nil
}
