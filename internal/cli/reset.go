package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/aitestmanagement/gtms-cli/internal/output"
	"github.com/aitestmanagement/gtms-cli/internal/reader"
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
				arg := strings.ToLower(args[0])
				arg = normaliseTarget(arg)
				if isTestCaseID(arg) {
					return runReset(os.Stdout, root, nil, arg, dry)
				}
				folder, err := validateFolderArg(arg)
				if err != nil {
					output.Errorf(err.Error(), "")
					return output.AsDisplayed(err)
				}
				scope := buildScopeFromArg(root, folder, recursive)
				return runReset(os.Stdout, root, scope, "", dry)
			}

			scope := buildScopeFromArg(root, "", recursive)
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
