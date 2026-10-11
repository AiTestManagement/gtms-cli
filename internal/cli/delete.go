package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/output"
	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
	"github.com/aitestmanagement/gtms-cli/internal/reader"
	targetpkg "github.com/aitestmanagement/gtms-cli/internal/target"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
	"github.com/spf13/cobra"
)

func newDeleteCmd() *cobra.Command {
	var keepSpec bool
	var recursive bool
	var localDryRun bool
	var forceArtefactDeletion bool

	cmd := &cobra.Command{
		Use:   "delete <test-case-id | folder>",
		Short: "Delete test case artefacts",
		Long: `Delete pipeline artefacts for a test case or folder of test cases.

Single test case:
  gtms delete tc-a1b2c3d0                          -- delete GTMS-owned state, preserve referenced artefacts
  gtms delete tc-a1b2c3d0 --keep-spec              -- also keep the spec
  gtms delete tc-a1b2c3d0 --force-artefact-deletion -- also delete referenced artefacts (see below)
  gtms delete tc-a1b2c3d0 --dry-run                -- preview what would be deleted

Folder (bulk mode):
  gtms delete my-feature                  -- delete all test cases in gtms/test/cases/my-feature/
  gtms delete my-feature -r               -- include test cases from subdirectories
  gtms delete my-feature --keep-spec      -- keep specs, delete pipeline artefacts only

GTMS-owned state deleted (always):
  - Test case specs (gtms/test/cases/**/tc-{id}-*.md or tc-{id}.md), unless --keep-spec
  - Wiring records (gtms/automation/wiring/tc-{id}--*.wiring.yaml)
  - Manual records (gtms/manual/records/tc-{id}--manual.result.yaml)
  - Result files (discovered from record executed_artefact fields)
  - Task files (gtms/tasks/*/task-*-{command}-tc-{id}.md)
  - Result contracts (.gtms/results/*.handoff.yaml)

Referenced test artefacts (from wiring and manual record artefact fields) are
PRESERVED by default and named in the summary -- a wiring reference is not a
claim of exclusive ownership. Pass --force-artefact-deletion to delete them
too; even then, an artefact referenced by a test case OUTSIDE the deletion set
is preserved and the surviving test case is named in a warning. That check
reads each record's declared testcase: identity, so a same-ID test case in
another folder, or a longer tc-xxxxxxxx--variant sibling, counts as an outside
reference. A record that cannot be parsed, or whose declared identity
disagrees with its filename, makes the forced delete refuse rather than risk
removing a file that is still shared.

A test case ID that exists in more than one folder is refused, not guessed:
delete names the candidates and exits non-zero. Qualify the target as
folder/tc-xxxxxxxx to choose one. Naming a folder grants authority only over
specs that physically live in it, so a spec symlinked in from another folder
is not deletable through the link.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := GetProjectRoot()
			// BUG-113: honour both root-position (gtms --dry-run delete)
			// and local (gtms delete --dry-run) flag sources.
			dry := IsDryRun() || localDryRun

			// BUG-167: shared classifier -- folder case preserved (existence
			// first), only confirmed TC IDs lowercased/stripped.
			res, err := targetpkg.Classify(root, args[0])
			if err != nil {
				output.Errorf(err.Error(),
					"Use only letters, numbers, dashes, underscores, dots, and forward slashes.")
				return output.AsDisplayed(err)
			}

			// Disambiguate: TC ID vs folder
			if res.Kind == targetpkg.KindTCID {
				// BUG-059 (REV-113): a folder-qualified TC must resolve scoped
				// before this mutating command deletes state for the bare ID --
				// otherwise delete bar/tc-x would remove a same-ID TC under foo/.
				if err := requireScopedSpec(root, res); err != nil {
					output.Errorf(err.Error(), "")
					return output.AsDisplayed(err)
				}
				// ADR-024 s3 / REV-158 CLAUDE-001: carry the folder qualifier into the
				// core so the destructive plan can require MUTATION AUTHORITY under it,
				// not just reachability. requireScopedSpec above stays as the earlier
				// diagnostic; the refusal that protects data lives in the plan.
				qualifiedScope, qerr := qualifiedTargetScope(root, res)
				if qerr != nil {
					output.Errorf(qerr.Error(), "")
					return output.AsDisplayed(qerr)
				}
				return runDelete(os.Stdout, root, qualifiedScope, res.TCID, keepSpec, forceArtefactDeletion, dry)
			}

			// Folder mode
			folder := res.Target

			if !IsBulkFolder(root, folder) {
				msg := fmt.Sprintf("No test cases folder found at gtms/test/cases/%s/.", folder)
				output.Errorf(msg, "Check the folder name and try again.")
				return output.AsDisplayed(errors.New(msg))
			}

			scope, serr := buildScopeFromArg(root, folder, recursive)
			if serr != nil {
				output.Errorf(serr.Error(), "")
				return output.AsDisplayed(serr)
			}
			return runDelete(os.Stdout, root, scope, "", keepSpec, forceArtefactDeletion, dry)
		},
	}

	cmd.Flags().BoolVar(&keepSpec, "keep-spec", false, "Preserve test case specs, delete only pipeline artefacts")
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "Include test cases from subdirectories")
	cmd.Flags().BoolVar(&localDryRun, "dry-run", false, "Preview what would be deleted without executing")
	cmd.Flags().BoolVar(&forceArtefactDeletion, "force-artefact-deletion", false, "Physically delete referenced test artefacts when no test case outside the deletion set references them")

	return cmd
}

// runDelete executes the delete operation and prints the result summary.
func runDelete(w io.Writer, projectRoot string, scope *reader.ScopeInfo, tcID string, keepSpec, force, dryRun bool) error {
	result, err := reader.DeleteArtifacts(projectRoot, scope, tcID, keepSpec, force, dryRun)
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
		// ADR-024 / BUG-185: ownership is not uniquely proven for a selected id
		// (ambiguous or unprovable). Refuse the whole plan visibly -- non-zero exit
		// AND an actionable diagnostic on stderr -- before any filesystem mutation.
		var oe *testcase.OwnershipError
		if errors.As(err, &oe) {
			output.Errorf(oe.Error(), "")
			return output.AsDisplayed(err)
		}
		// ADR-024 s3 / REV-158 CLAUDE-001: the target was reachable through a link
		// but its spec lives outside the named folder, so the qualifier does not
		// grant authority to remove it.
		var sae *testcase.ScopeAuthorityError
		if errors.As(err, &sae) {
			output.Errorf(sae.Error(), "")
			return output.AsDisplayed(err)
		}
		// ADR-024 s1 / F2, REV-160 CLAUDE-007: a wiring record attributed to a
		// selected id is corrupt or declares a foreign identity. It used to fall
		// through to the bare `return err` below, so it exited non-zero but
		// printed without the established error shape the refusals above use.
		var cwe *reader.CorruptWiringError
		if errors.As(err, &cwe) {
			output.Errorf(cwe.Error(), "Fix or remove the wiring record, then retry.")
			return output.AsDisplayed(err)
		}
		return err
	}

	formatDeleteOutput(w, result, projectRoot, dryRun)
	return nil
}

// formatDeleteOutput prints the delete result, grouped by artefact type.
func formatDeleteOutput(w io.Writer, result *reader.DeleteResult, projectRoot string, dryRun bool) {
	total := result.TotalFiles()

	if total == 0 && len(result.PreservedArtefacts) == 0 {
		if dryRun {
			fmt.Fprintln(w, "[dry-run] Nothing to delete.")
		} else {
			fmt.Fprintln(w, "Nothing to delete.")
		}
		return
	}

	if total > 0 {
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
			fmt.Fprintf(w, "  Test artefacts:      %d\n", result.TestScripts)
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
	}

	// BUG-173: name preserved referenced artefacts. Safe-default preserves
	// (no surviving TCs) are grouped under a heading; artefacts kept because a
	// forced deletion was blocked by an external reference emit an individual
	// warning naming the surviving test case(s).
	formatPreservedArtefacts(w, result.PreservedArtefacts)

	// In verbose or dry-run mode, list individual deleted files. Relativise
	// against the canonical root: the planner resolves artefact paths through
	// pathsafe (canonical form), so a raw root would produce a spurious "../../"
	// escape on a Windows 8.3 short path (BUG-173 Windows CI).
	if (dryRun || IsVerbose()) && len(result.FilesDeleted) > 0 {
		fmt.Fprintln(w)
		canonRoot := pathsafe.CanonicalRoot(projectRoot)
		for _, f := range result.FilesDeleted {
			rel, err := filepath.Rel(canonRoot, f)
			if err != nil {
				rel = f
			}
			rel = filepath.ToSlash(rel)
			fmt.Fprintf(w, "  %s\n", rel)
		}
	}
}

// formatPreservedArtefacts prints the BUG-173 preserved-artefact section.
func formatPreservedArtefacts(w io.Writer, preserved []reader.PreservedArtefact) {
	if len(preserved) == 0 {
		return
	}

	var defaults []reader.PreservedArtefact
	var shared []reader.PreservedArtefact
	for _, p := range preserved {
		if len(p.SurvivingTCs) > 0 {
			shared = append(shared, p)
		} else {
			defaults = append(defaults, p)
		}
	}

	fmt.Fprintln(w)
	if len(defaults) > 0 {
		fmt.Fprintln(w, "Preserved referenced artefacts (not deleted):")
		for _, p := range defaults {
			fmt.Fprintf(w, "  %s\n", p.Path)
		}
	}
	for _, p := range shared {
		fmt.Fprintf(w, "⚠ Preserved %s -- still referenced by %s\n", p.Path, strings.Join(p.SurvivingTCs, ", "))
	}
}
