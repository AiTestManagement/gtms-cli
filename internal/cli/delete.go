package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/aitestmanagement/gtms-cli/internal/output"
	"github.com/aitestmanagement/gtms-cli/internal/reader"
)

func newDeleteCmd() *cobra.Command {
	var keepSpec bool
	var recursive bool

	cmd := &cobra.Command{
		Use:   "delete [test-case-id | folder]",
		Short: "Delete test case artifacts",
		Long: `Delete all pipeline artifacts for a test case or folder of test cases.

Single test case:
  gtms delete tc-a1b2c3d0                — delete all artifacts for this test case
  gtms delete tc-a1b2c3d0 --keep-spec    — keep the spec, delete pipeline artifacts only
  gtms delete tc-a1b2c3d0 --dry-run      — preview what would be deleted

Folder (bulk mode):
  gtms delete my-feature                  — delete all test cases in gtms/cases/my-feature/
  gtms delete my-feature -r               — include test cases from subdirectories
  gtms delete my-feature --keep-spec      — keep specs, delete pipeline artifacts only

Artifact types deleted:
  - Test case specs (gtms/cases/**/tc-{id}-*.md)
  - Wiring records (gtms/automation/wiring/tc-{id}--*.wiring.yaml)
  - Manual records (gtms/manual/records/tc-{id}--manual.result.yaml)
  - Test scripts (discovered from wiring record artefact fields)
  - Task files (gtms/tasks/*/task-*-{command}-tc-{id}.md)
  - Result contracts (.gtms/results/*.handoff.yaml)`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetProjectRoot()
			dry := IsDryRun()

			arg := strings.ToLower(args[0])
			arg = normaliseTarget(arg)

			// Validate: arg is safe
			if err := validateTargetID(arg); err != nil {
				output.Errorf(err.Error(),
					"Use only letters, numbers, dashes, underscores, dots, and forward slashes.")
				return output.AsDisplayed(err)
			}

			// Disambiguate: TC ID vs folder
			if isTestCaseID(arg) {
				return runDelete(os.Stdout, root, nil, arg, keepSpec, dry)
			}

			// Folder mode
			folder, err := validateFolderArg(arg)
			if err != nil {
				output.Errorf(err.Error(), "")
				return output.AsDisplayed(err)
			}

			if !IsBulkFolder(root, folder) {
				msg := fmt.Sprintf("No test cases folder found at gtms/cases/%s/.", folder)
				output.Errorf(msg, "Check the folder name and try again.")
				return output.AsDisplayed(fmt.Errorf(msg))
			}

			scope := buildScopeFromArg(root, folder, recursive)
			return runDelete(os.Stdout, root, scope, "", keepSpec, dry)
		},
	}

	cmd.Flags().BoolVar(&keepSpec, "keep-spec", false, "Preserve test case specs, delete only pipeline artifacts")
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "Include test cases from subdirectories")

	return cmd
}

// runDelete executes the delete operation and prints the result summary.
func runDelete(w io.Writer, projectRoot string, scope *reader.ScopeInfo, tcID string, keepSpec, dryRun bool) error {
	result, err := reader.DeleteArtifacts(projectRoot, scope, tcID, keepSpec, dryRun)
	if err != nil {
		// ENH-128 AC #5: a record-declared artefact path resolved outside the
		// project-owned allowlist. Refuse atomically with a clear stderr
		// message and a non-zero exit. No partial deletion has occurred.
		var pse *reader.PathSafetyError
		if errors.As(err, &pse) {
			msg := fmt.Sprintf("Refusing to delete: artefact path %q resolves outside the project-owned allowlist.", pse.Path)
			hint := "Fix the offending path in the automation record before retrying, or remove the record manually."
			output.Errorf(msg, hint)
			return output.AsDisplayed(err)
		}
		return err
	}

	formatDeleteOutput(w, result, projectRoot, dryRun)
	return nil
}

// formatDeleteOutput prints the delete result, grouped by artifact type.
func formatDeleteOutput(w io.Writer, result *reader.DeleteResult, projectRoot string, dryRun bool) {
	total := result.TotalFiles()

	if total == 0 {
		if dryRun {
			fmt.Fprintln(w, "[dry-run] Nothing to delete.")
		} else {
			fmt.Fprintln(w, "Nothing to delete.")
		}
		return
	}

	if dryRun {
		fmt.Fprintf(w, "[dry-run] Would delete %d file(s) for %d test case(s):\n", total, result.TestCasesProcessed)
	} else {
		fmt.Fprintf(w, "Deleted %d file(s) for %d test case(s):\n", total, result.TestCasesProcessed)
	}

	if result.TestCaseSpecsRemoved > 0 {
		fmt.Fprintf(w, "  Test case specs:     %d\n", result.TestCaseSpecsRemoved)
	}
	if result.AutomationRecords > 0 {
		fmt.Fprintf(w, "  Automation records:  %d\n", result.AutomationRecords)
	}
	if result.TestScripts > 0 {
		fmt.Fprintf(w, "  Test scripts:        %d\n", result.TestScripts)
	}
	if result.TaskFiles > 0 {
		fmt.Fprintf(w, "  Task files:          %d\n", result.TaskFiles)
	}
	if result.ResultFiles > 0 {
		fmt.Fprintf(w, "  Result files:        %d\n", result.ResultFiles)
	}
	if result.ResultContracts > 0 {
		fmt.Fprintf(w, "  Result contracts:    %d\n", result.ResultContracts)
	}

	// In verbose or dry-run mode, list individual files
	if (dryRun || IsVerbose()) && len(result.FilesDeleted) > 0 {
		fmt.Fprintln(w)
		for _, f := range result.FilesDeleted {
			rel, err := filepath.Rel(projectRoot, f)
			if err != nil {
				rel = f
			}
			rel = filepath.ToSlash(rel)
			fmt.Fprintf(w, "  %s\n", rel)
		}
	}
}
