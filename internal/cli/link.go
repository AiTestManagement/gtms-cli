package cli

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aitestmanagement/gtms-cli/internal/adapter"
	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/link"
	"github.com/aitestmanagement/gtms-cli/internal/output"
	targetpkg "github.com/aitestmanagement/gtms-cli/internal/target"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
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
  gtms link --refresh my-feature --dry-run            (preview the refresh without writing)

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
					return output.AsDisplayed(errors.New(msg))
				}
				if refreshFlag {
					msg := "--refresh is not valid with --adapter repoint. These are separate modes."
					output.Errorf(msg, "Use --refresh alone to recompute hashes, or --adapter alone to repoint.")
					return output.AsDisplayed(errors.New(msg))
				}
				if forceFlag {
					msg := "--force is not valid with --adapter repoint."
					output.Errorf(msg, "Repoint is idempotent -- rerun to update remaining records.")
					return output.AsDisplayed(errors.New(msg))
				}
				if checkFlag {
					msg := "--check is not valid with --adapter repoint. Use --dry-run to preview."
					output.Errorf(msg, "")
					return output.AsDisplayed(errors.New(msg))
				}
				if strictFlag {
					msg := "--strict is not valid with --adapter repoint."
					output.Errorf(msg, "")
					return output.AsDisplayed(errors.New(msg))
				}
				// BUG-167 / REV-118: the classifier's decision crosses the
				// orchestration boundary as a TYPED result. runRepoint switches on
				// Result.Kind and NEVER re-derives folder-vs-TC from the string --
				// so a trailing-separator folder target (tc-x/) can't be reclassified
				// into a single-TC repoint on a global wiring. classified is nil only
				// for --all (project-wide, no positional target).
				var classified *targetpkg.Result
				if len(args) > 0 {
					res, cerr := targetpkg.Classify(root, args[0])
					if cerr != nil {
						output.Errorf(cerr.Error(),
							"Use only letters, numbers, dashes, underscores, dots, and forward slashes.")
						return output.AsDisplayed(cerr)
					}
					classified = &res
				}
				return runRepoint(root, classified, adapterFlag, fromAdapterFlag, frameworkFlag, allFlag, recursiveFlag, dry)
			}

			// --from-adapter without --adapter is invalid.
			if fromAdapterFlag != "" {
				msg := "--from-adapter requires --adapter."
				output.Errorf(msg, "Example: gtms link tc-x --from-adapter old --adapter new")
				return output.AsDisplayed(errors.New(msg))
			}

			// --all without --adapter is invalid.
			if allFlag {
				msg := "--all requires --adapter for repoint mode."
				output.Errorf(msg, "Example: gtms link --all --from-adapter old --adapter new")
				return output.AsDisplayed(errors.New(msg))
			}

			// BUG-146: refresh mode now honours --dry-run (preview). Write and
			// check modes still reject it -- so the rejection is gated on
			// !refreshFlag. The refresh preview is dispatched via runRefresh below.
			if dry && !refreshFlag {
				msg := "--dry-run is not yet supported for non-repoint link operations."
				output.Errorf(msg, "Use --adapter to enter repoint mode, or --refresh --dry-run to preview a refresh; write and check modes do not support --dry-run.")
				return output.AsDisplayed(errors.New(msg))
			}

			// Non-repoint modes require a positional argument.
			if len(args) == 0 {
				msg := "A test case ID or folder is required."
				output.Errorf(msg, "Example: gtms link tc-a1b2c3d4 --framework playwright --artefact tests/login.spec.ts")
				return output.AsDisplayed(errors.New(msg))
			}

			// BUG-167: shared classifier -- preserve folder case for --refresh,
			// normalise separators, lowercase/strip only confirmed TC IDs.
			res, cerr := targetpkg.Classify(root, args[0])
			if cerr != nil {
				// --refresh accepts a folder OR a TC scope, so it keeps the raw
				// classifier error -- the generic "Invalid test case ID format"
				// override would mislabel a rejected folder as a bad TC ID.
				if refreshFlag {
					output.Errorf(cerr.Error(),
						"Use only letters, numbers, dashes, underscores, dots, and forward slashes.")
					return output.AsDisplayed(cerr)
				}
				// Standard link is single-TC only: retain the historical generic
				// message (BUG-058 frozen specs) plus the classifier's specific cause.
				msg, hint := invalidTargetFormatError(args[0], cerr)
				output.Errorf(msg, hint)
				return output.AsDisplayed(cerr)
			}

			// --- Refresh mode (ENH-156) -- accepts a folder scope or a TC ID ---
			// BUG-167 / REV-118: pass the TYPED classifier result into runRefresh,
			// which switches on Result.Kind and never re-derives folder-vs-TC from
			// the string (scoped validation of a qualified TC happens inside).
			if refreshFlag {
				return runRefresh(root, res, frameworkFlag, artefactFlag, checkFlag, forceFlag, strictFlag, recursiveFlag, dry)
			}

			// --- Standard link mode (original behavior) -- single TC only ---
			if res.Kind != targetpkg.KindTCID {
				msg := fmt.Sprintf("Invalid test case ID format: '%s'.", sanitizeForError(args[0]))
				output.Errorf(msg, "Use format like tc-a1b2c3d4.")
				return output.AsDisplayed(errors.New(msg))
			}
			// BUG-059 (REV-114 CLAUDE-001): scope-validate a folder-qualified target
			// BEFORE the check/link split, regardless of --strict. LinkRecord's own
			// scoped check is gated on --strict (default off), so without this a
			// non-strict `gtms link bar/tc-x` would resolve globally and write wiring
			// to the wrong folder. requireScopedSpec only enforces when a qualifier is
			// present, so a bare TC ID keeps the ENH-111 non-strict/global behaviour.
			if err := requireScopedSpec(root, res); err != nil {
				output.Errorf(err.Error(), "Check the folder qualifier, or drop it to match the TC anywhere.")
				return output.AsDisplayed(err)
			}
			// Pass the QUALIFIED target to LinkRecord/CheckLink; they strip to the
			// bare ID internally for the flat wiring key.
			target := res.Target

			if frameworkFlag == "" {
				msg := "--framework is required for gtms link."
				output.Errorf(msg, "Example: gtms link tc-a1b2c3d4 --framework playwright --artefact tests/login.spec.ts")
				return output.AsDisplayed(errors.New(msg))
			}
			if !config.ValidateFramework(frameworkFlag) {
				msg := fmt.Sprintf("Invalid framework '%s'. Framework must contain only lowercase letters, digits, and hyphens.", frameworkFlag)
				output.Errorf(msg, "Example: --framework playwright")
				return output.AsDisplayed(errors.New(msg))
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
				return output.AsDisplayed(errors.New(msg))
			}

			cfg := GetConfig()
			wiringAdapter, warnings, err := link.LinkRecord(root, cfg, target, frameworkFlag, artefactFlag, forceFlag, strictFlag)
			if err != nil {
				// BUG-171: a missing execute adapter for the framework is a
				// link/configuration problem, not an artefact problem. Give
				// link-appropriate guidance and do NOT inherit automate's
				// manual prime-path advice (the shared resolver error must stay
				// command-specific at the CLI boundary).
				var mae *adapter.MissingExecuteAdapterError
				if errors.As(err, &mae) {
					output.Errorf(err.Error(),
						"Configure a compatible execute adapter in gtms.config (adapters.execute with a matching framework), or correct the link's --framework/--artefact.")
					return output.AsDisplayed(err)
				}
				// tc-d1c39672: a foreign-identity wiring record cannot be forced, so
				// the generic "use --force to overwrite" hint would send the user to a
				// dead end. Point them at the record instead.
				var wme *link.WiringIdentityMismatchError
				if errors.As(err, &wme) {
					output.Errorf(err.Error(),
						"Fix or remove the mismatched wiring record; --force cannot overwrite a foreign-identity record.")
					return output.AsDisplayed(err)
				}
				// ADR-024 s7 / REV-158 CLAUDE-006: an ambiguous or unprovable identity
				// is not an artefact problem and --force cannot resolve it. Render the
				// typed diagnostic, which names both owners, with no --force hint.
				var oe *testcase.OwnershipError
				if errors.As(err, &oe) {
					output.Errorf(oe.Error(), "")
					return output.AsDisplayed(err)
				}
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
	cmd.Flags().BoolVar(&localDryRun, "dry-run", false, "Preview a refresh or repoint without writing")

	return cmd
}

// runRefresh handles the --refresh path for the link command (ENH-156).
//
// BUG-167 / REV-118: runRefresh consumes the CLASSIFIED target.Result. It rejects
// incompatible flags first, then dispatches by classified.Kind. It NEVER calls
// IsBulkFolder or isTestCaseID to determine the target kind, and never infers kind
// from the string, slashes, or filesystem existence -- so a trailing-separator
// folder target (KindFolder) can't be re-derived into a single-TC refresh on a
// global wiring, even when the folder is absent or TC-shaped.
func runRefresh(root string, classified targetpkg.Result, frameworkFlag, artefactFlag string, checkFlag, forceFlag, strictFlag, recursive, dry bool) error {
	// F3: refresh resolves ownership once and reuses that snapshot across the
	// ownership guard and the spec-hash path (one project-wide enumeration per
	// operation). refresh reads specs and writes only wiring -- never a spec -- so
	// the cached snapshot cannot go stale mid-operation.

	// Reject incompatible flags. classified.Target is used only for the example
	// hint text (never to determine kind).
	if artefactFlag != "" {
		msg := "--artefact is not valid with --refresh. Refresh sources the artefact from the existing wiring record."
		output.Errorf(msg, fmt.Sprintf("To change the artefact path, use: gtms link %s --framework <fw> --artefact <path> --force", classified.Target))
		return output.AsDisplayed(errors.New(msg))
	}
	if checkFlag {
		msg := "--check is not valid with --refresh. To preview a refresh without writing, use: gtms link " + classified.Target + " --refresh --dry-run"
		output.Errorf(msg, "")
		return output.AsDisplayed(errors.New(msg))
	}
	if forceFlag {
		msg := "--force is not valid with --refresh. Refresh is itself the explicit acknowledgement."
		output.Errorf(msg, "")
		return output.AsDisplayed(errors.New(msg))
	}
	if strictFlag {
		msg := "--strict is not valid with --refresh."
		output.Errorf(msg, "")
		return output.AsDisplayed(errors.New(msg))
	}

	// Validate --framework if provided.
	if frameworkFlag != "" && !config.ValidateFramework(frameworkFlag) {
		msg := fmt.Sprintf("Invalid framework '%s'. Framework must contain only lowercase letters, digits, and hyphens.", frameworkFlag)
		output.Errorf(msg, "Example: --framework playwright")
		return output.AsDisplayed(errors.New(msg))
	}

	// Dispatch strictly by classified kind (target safety was already enforced by
	// target.Classify -- validateFolderSafety ran on the complete input).
	switch classified.Kind {
	case targetpkg.KindFolder:
		return runFolderRefresh(root, classified.Target, frameworkFlag, recursive, dry)
	case targetpkg.KindTCID:
		// BUG-059 (REV-113): scope-validate a qualified TC, then refresh the flat
		// wiring keyed on the bare ID.
		if err := requireScopedSpec(root, classified); err != nil {
			output.Errorf(err.Error(), "Check the folder qualifier, or drop it to match the TC anywhere.")
			return output.AsDisplayed(err)
		}
		return runSingleTCRefresh(root, classified.TCID, frameworkFlag, dry)
	default:
		return output.AsDisplayed(fmt.Errorf("internal error: unclassified refresh target"))
	}
}

// dryMarker returns the "[dry-run] " prefix in preview mode and "" otherwise.
//
// Every line a preview emits carries it -- successes, per-record failures,
// discovery failures, and the operation-level errors below (REV-128
// CODEX-128-002, extended at REV-129 CODEX-129-001 to the directory-level
// scan errors). The rule is that a user who asked for a preview must never be
// shown output they could mistake for a real refresh, and drawing the line
// anywhere short of "everything the operation prints" creates a distinction
// they cannot see.
//
// The boundary is the operation, not the process: the flag-compatibility
// rejections in runRefresh (--check/--force/--strict with --refresh, an invalid
// --framework) stay unlabelled. Those reject the command line before a mode is
// even dispatched, they say nothing about wiring, and they are identical whether
// or not --dry-run was passed -- there is no real-refresh reading to confuse
// them with.
func dryMarker(dry bool) string {
	if dry {
		return "[dry-run] "
	}
	return ""
}

// refreshOutcome classifies the result of processing one wiring record during a
// refresh (preview or real).
type refreshOutcome int

const (
	refreshFailed refreshOutcome = iota
	refreshChanged
	refreshCurrent
)

// refreshDryFieldList formats the changed hash field tokens for a dry-run
// preview line. Only the fields that would actually change are listed (BUG-146
// AC3), so a pending record previews "testcase-hash" alone.
func refreshDryFieldList(plan link.RefreshPlan) string {
	var fields []string
	if plan.TestCaseHashChanged {
		fields = append(fields, "testcase-hash")
	}
	if plan.ArtefactHashChanged {
		fields = append(fields, "artefact-hash")
	}
	return strings.Join(fields, ", ")
}

// processRefreshRecord previews (dry) or applies (real) a single wiring record's
// refresh, prints the per-record line, and reports the outcome. The dry path
// calls link.PlanRefresh (no write); the real path calls link.RefreshRecord.
// Both share link.PlanRefresh's validation, so the preview surfaces exactly the
// errors and field changes the real refresh would (BUG-146 parity).
func processRefreshRecord(root string, snap *testcase.Snapshot, tcLabel string, rec *wiring.WiringRecord, dry bool) refreshOutcome {
	if dry {
		plan, err := link.PlanRefresh(root, snap, rec)
		if err != nil {
			// BUG-146 CODEX-128-002 / AC2: a preview failure must carry the
			// [dry-run] label so an all-failures preview is never mistaken for a
			// real refresh failure. The cause substring is left intact (specs grep
			// it); only the marker is prefixed, matched to the success line above.
			fmt.Printf("  %s [dry-run] %s (%s): %s\n", output.IconError, tcLabel, rec.Framework, err.Error())
			return refreshFailed
		}
		if plan.Changed {
			fmt.Printf("  %s [dry-run] Would refresh: %s (%s) -- %s\n",
				output.IconComplete, tcLabel, rec.Framework, refreshDryFieldList(plan))
			return refreshChanged
		}
		fmt.Printf("  %s [dry-run] Current: %s (%s)\n", output.IconSkipped, tcLabel, rec.Framework)
		return refreshCurrent
	}

	refreshed, err := link.RefreshRecord(root, snap, rec)
	if err != nil {
		fmt.Printf("  %s %s (%s): %s\n", output.IconError, tcLabel, rec.Framework, err.Error())
		return refreshFailed
	}
	if refreshed {
		fmt.Printf("  %s Refreshed: %s (%s)\n", output.IconComplete, tcLabel, rec.Framework)
		return refreshChanged
	}
	fmt.Printf("  %s Current: %s (%s)\n", output.IconSkipped, tcLabel, rec.Framework)
	return refreshCurrent
}

// processRefreshTarget renders one discovered refresh target: a discovery failure
// (malformed/unreadable file, or a filename/content identity mismatch) prints the
// per-record error line and counts as a failure; a valid record is previewed or
// applied via processRefreshRecord. Routing failures here -- instead of dropping
// them at discovery -- keeps the dry and real failure sets identical (CODEX-002).
func processRefreshTarget(root string, snap *testcase.Snapshot, tgt link.RefreshTarget, dry bool) refreshOutcome {
	if tgt.Err != nil {
		// BUG-146 CODEX-128-002 / AC2: a preview discovery failure (malformed /
		// identity-mismatched wiring) carries the [dry-run] label; the real path
		// renders the same line without it. The cause substring is unchanged.
		fmt.Printf("  %s %s%s (%s): %s\n", output.IconError, dryMarker(dry), tgt.TCID, tgt.Framework, tgt.Err.Error())
		return refreshFailed
	}
	return processRefreshRecord(root, snap, tgt.TCID, tgt.Record, dry)
}

// runSingleTCRefresh handles --refresh for a single TC ID (ENH-156). With dry
// set it renders a non-mutating preview instead of writing (BUG-146).
//
// Discovery goes through link.DiscoverRefreshTargets (identity-validating,
// diagnostic-preserving) rather than wiring.Find/FindAllForTC, so a wiring file
// whose YAML identity disagrees with its filename cannot be selected as this TC
// and then written to a foreign identity (CODEX-001), and a malformed in-scope
// file is a counted failure rather than a silent skip (CODEX-002).
func runSingleTCRefresh(root, tcID, frameworkFlag string, dry bool) error {
	// ADR-024 / BUG-185: refresh writes wiring, so a bare id whose ownership is
	// Ambiguous or Unprovable is held back (for a single id, the whole refresh)
	// rather than writing a hash from an unproven owner. A prefix/-- sibling does
	// NOT trigger this -- it is a different, unique owner (tc-31d05fff).
	// F4: a snapshot build failure (enumeration/containment/read error) must NOT
	// skip the ownership guard and proceed to rewrite wiring -- that is
	// error-to-permission conversion on a mutating consumer. Surface it and exit
	// non-zero.
	snap, serr := testcase.NewSnapshot(root)
	if serr != nil {
		output.Errorf(dryMarker(dry)+serr.Error(), "Check gtms/test/cases/ is readable, then retry.")
		return output.AsDisplayed(serr)
	}
	if oerr := snap.RequireOwnership(tcID); oerr != nil {
		output.Errorf(dryMarker(dry)+oerr.Error(), "")
		return output.AsDisplayed(oerr)
	}
	targets, err := link.DiscoverRefreshTargets(root, []string{tcID}, frameworkFlag)
	if err != nil {
		msg := fmt.Sprintf("%sScanning wiring for %s: %v", dryMarker(dry), tcID, err)
		output.Errorf(msg, "Check gtms/automation/wiring/ for parse errors.")
		return output.AsDisplayed(errors.New(msg))
	}
	if len(targets) == 0 {
		// BUG-146 CODEX-128-002 / AC2: a preview labels every line it emits,
		// including the no-record outcome. Nothing was planned and nothing would be
		// written either way, but the user asked for a preview and must be able to
		// see that a preview is what ran -- an unlabelled line here is the only
		// output of the command and reads as a real refresh failure. The cause
		// substring, the hint, and the exit status are unchanged; only the marker
		// is prefixed. (The `gtms execute` path emits the same two sentences from
		// execute.go and is deliberately untouched -- it has no preview mode.)
		marker := dryMarker(dry)
		if frameworkFlag != "" {
			msg := fmt.Sprintf("%sNo wiring record found for '%s' (framework: %s).", marker, tcID, frameworkFlag)
			output.Errorf(msg, fmt.Sprintf("Run 'gtms link %s --framework %s --artefact <path>' to create it.", tcID, frameworkFlag))
			return output.AsDisplayed(errors.New(msg))
		}
		msg := fmt.Sprintf("%sNo wiring records found for '%s'.", marker, tcID)
		output.Errorf(msg, fmt.Sprintf("Run 'gtms link %s --framework <fw> --artefact <path>' to create one.", tcID))
		return output.AsDisplayed(errors.New(msg))
	}

	failCount := 0
	for _, tgt := range targets {
		if processRefreshTarget(root, snap, tgt, dry) == refreshFailed {
			failCount++
		}
	}

	if failCount > 0 {
		msg := fmt.Sprintf("%d of %d wiring records failed to refresh for %s", failCount, len(targets), tcID)
		return output.AsDisplayed(errors.New(msg))
	}
	return nil
}

// folderNotFoundMessage renders "this folder is not there" for every scope-taking
// link path. REV-160 mechanism C: refresh and repoint reached the SAME condition
// -- a scope folder that does not exist -- and rendered it two different ways.
// Refresh said "folder '<cases>/<name>/' does not exist"; repoint surfaced
// testcase.ErrFolderNotFound as "test case folder not found: <name>", with
// neither the phrase a caller scripts against nor the normalised trailing
// separator. One condition, one formatter, so the next scope-taking path cannot
// invent a third wording.
//
// The trailing "/" is load-bearing, not decoration: it is the NORMALISED folder
// token, and it is what tells a reader the argument was taken as a folder rather
// than a test case id -- which is the whole subject of the
// folder-target-normalisation suite.
func folderNotFoundMessage(prefix, folder string) string {
	return fmt.Sprintf("%sfolder '%s/%s/' does not exist", prefix, layout.Current().TestCases, folder)
}

// runFolderRefresh handles --refresh for a folder target (ENH-156).
//
// Spec-first selection: discover TC IDs under the folder, enumerate wiring
// for each, filter to stale records, refresh those, report current as no-ops.
// With dry set it renders a non-mutating preview instead of writing (BUG-146),
// using the same per-record continuation policy so the preview's success/failure
// set matches the real refresh exactly.
func runFolderRefresh(root, folder, frameworkFlag string, recursive, dry bool) error {
	// ADR-024 / BUG-185 (tc-31d05fff) + BUG-188: refresh selection is by PARSED
	// body identity via the project-wide snapshot, so a prefix/-- sibling is a
	// different, unique owner and no longer creates FALSE ambiguity (the old
	// filename-based PartitionTCIDsByAmbiguity + DiscoverTestCases collapsed the
	// pair to one id and held both back). Refresh WRITES wiring, so it needs
	// project-wide UniqueOwner per id -- but its policy is per-ID HOLD-BACK, not
	// whole-operation refusal: an Ambiguous or Unprovable id is held back as a
	// counted failure, named, with no wiring written, while every unambiguous
	// record still refreshes. It exits non-zero when anything was held back.
	snap, serr := testcase.NewSnapshot(root)
	if serr != nil {
		return serr
	}
	scopeRel, ok := folderScopeRel(root, folder)
	if !ok {
		// REV-160 mechanism C. Folder-not-found must keep the wording this path
		// produced at b524c9b5 via DiscoverTestCases, which is also what the
		// sibling resolveBulkOwners still emits for the identical condition
		// (internal/cli/validate.go). The CLAUDE-005 rewrite restored the
		// BEHAVIOUR here but not the PHRASING, so a caller scripting against the
		// message broke, and two helpers disagreed about the same failure.
		//
		// Distinct from the empty-folder case below: this is "the folder is not
		// there", that one is "the folder is there and holds no test cases".
		msg := folderNotFoundMessage(dryMarker(dry), folder)
		output.Errorf(msg, "Check the folder name and try again.")
		return output.AsDisplayed(errors.New(msg))
	}
	ownerIDs, inScopeUnprovable := snap.SelectFolder(scopeRel, recursive)
	// inScopeUnprovable is tested first so it is read on every path (REV-184 round 6).
	if len(inScopeUnprovable) == 0 && len(ownerIDs) == 0 {
		// REV-160 CLAUDE-005: an existing but EMPTY folder is a "No test cases
		// found" error, not a silent zero-work success. At b524c9b5 this branch
		// went through DiscoverTestCases, which returned exactly this message with
		// a non-zero exit; replacing it with snap.SelectFolder dropped the check,
		// so `gtms link --refresh <emptyfolder>` printed "0 refreshed, 0 current,
		// 0 failed" and exited 0 -- a scripted `gtms link --refresh <f> && ...`
		// chain then proceeded against a folder holding nothing. The sibling bulk
		// helper resolveBulkOwners (internal/cli/validate.go) keeps the error for
		// exactly this case; refresh must not be the odd one out.
		msg := fmt.Sprintf("%sNo test cases found in %s/%s/", dryMarker(dry), layout.Current().TestCases, folder)
		output.Errorf(msg, fmt.Sprintf("Check that %s/%s/ contains tc-*.md files.", layout.Current().TestCases, folder))
		return output.AsDisplayed(errors.New(msg))
	}

	// Partition the folder's owner ids by PROJECT-WIDE ownership. A duplicate
	// whose second physical owner is outside the folder is held back too -- a
	// scope-local scan could not see it (tc-31d05fff step 8).
	held := map[string]error{}
	var heldIDs []string
	var okIDs []string
	for _, id := range ownerIDs {
		if oerr := snap.RequireOwnership(id); oerr != nil {
			held[id] = oerr
			heldIDs = append(heldIDs, id)
		} else {
			okIDs = append(okIDs, id)
		}
	}
	sort.Strings(heldIDs)

	// Single identity-validating discovery path for both dry and real refresh: an
	// identity-mismatched record cannot be written to a foreign TC (CODEX-001) and
	// a malformed in-scope record is a counted failure, not a silent drop
	// (CODEX-002). Only proven unique owners reach discovery/refresh.
	targets, err := link.DiscoverRefreshTargets(root, okIDs, frameworkFlag)
	if err != nil {
		// REV-184 round 6: a wiring-scan failure must not hide the in-scope
		// unprovable specs. Report them first, then keep the scan error and the
		// non-zero exit.
		printInScopeUnprovable(scopeRel, inScopeUnprovable, dry)
		msg := fmt.Sprintf("%sScanning wiring for %s/: %v", dryMarker(dry), folder, err)
		output.Errorf(msg, "Check gtms/automation/wiring/ for parse errors.")
		return output.AsDisplayed(errors.New(msg))
	}

	failCount := 0
	refreshedCount := 0
	currentCount := 0

	// Held-back ids (ambiguous/unprovable ownership): counted failures, named,
	// with the remedy on the line -- no wiring is written for them.
	for _, id := range heldIDs {
		fmt.Printf("  %s %s%s: %v\n", output.IconError, dryMarker(dry), id, held[id])
		failCount++
	}
	// In-scope unprovable specs have no body id and no wiring to refresh; report
	// each with the repair for its specific failure.
	failCount += printInScopeUnprovable(scopeRel, inScopeUnprovable, dry)

	for _, tgt := range targets {
		switch processRefreshTarget(root, snap, tgt, dry) {
		case refreshFailed:
			failCount++
		case refreshChanged:
			refreshedCount++
		case refreshCurrent:
			currentCount++
		}
	}

	// Summary. In dry mode the counts describe the plan ("would refresh") rather
	// than a completed mutation, and an entirely-current scope gets an explicit
	// nothing-to-refresh line (BUG-146 AC2 empty case).
	if dry {
		if refreshedCount == 0 && failCount == 0 {
			// REV-129 CLAUDE-002: "all wiring is current" is only true when there
			// was wiring to find. With currentCount also zero the scope holds no
			// wiring at all -- telling the user their wiring is current is the
			// opposite diagnosis, and sends them away from the actual remedy
			// (gtms link ... --artefact). Exit status stays 0 either way (AC2).
			if currentCount > 0 {
				fmt.Printf("\n  [dry-run] Nothing to refresh -- all wiring is current.\n")
			} else {
				fmt.Printf("\n  [dry-run] No wiring records in scope.\n")
			}
		}
		fmt.Printf("\n  [dry-run] %d would refresh, %d current, %d failed\n", refreshedCount, currentCount, failCount)
	} else {
		fmt.Printf("\n  %d refreshed, %d current, %d failed\n", refreshedCount, currentCount, failCount)
	}

	if failCount > 0 {
		msg := fmt.Sprintf("%d wiring records failed to refresh in %s/", failCount, folder)
		return output.AsDisplayed(errors.New(msg))
	}
	return nil
}

// printInScopeUnprovable prints one failure line per in-scope unprovable spec,
// each with the repair for its specific failure, and returns how many it printed.
func printInScopeUnprovable(scopeRel string, unprovable []testcase.Candidate, dry bool) int {
	for _, c := range unprovable {
		fmt.Printf("  %s %s%v\n", output.IconError, dryMarker(dry), testcase.UnprovableInScope(scopeRel, c))
	}
	return len(unprovable)
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
// runRepoint handles the --adapter repoint path for the link command.
//
// BUG-167 / REV-118: runRepoint consumes the CLASSIFIED target.Result (nil only
// for --all, the project-wide mode). It dispatches by classified.Kind and NEVER
// calls IsBulkFolder or isTestCaseID to determine the target kind -- so a
// trailing-separator folder target (KindFolder) can't be re-derived into a
// single-TC repoint on a global wiring. Flag validation order and contracts are
// preserved; classified.Target is used only for validation predicates/messages,
// never to determine kind.
func runRepoint(root string, classified *targetpkg.Result, adapterFlag, fromAdapterFlag, frameworkFlag string, allFlag, recursive, dryRun bool) error {
	// targetStr participates ONLY in flag validation and message text (the
	// "target present" predicate), never in kind determination.
	targetStr := ""
	if classified != nil {
		targetStr = classified.Target
	}

	// Flag-combination rejections (unchanged order/contract).
	if err := validateRepointFlags(targetStr, adapterFlag, fromAdapterFlag, allFlag, recursive); err != nil {
		return err
	}

	// CODEX-010 / REV-118: -r is a folder-scope modifier. Reject it for a single-TC
	// target -- keyed on classified.Kind, NOT filesystem/string reclassification.
	// (--all already rejects -r in validateRepointFlags.) A KindFolder target keeps
	// folder intent for -r even when the folder is absent or TC-shaped.
	if recursive && !allFlag && classified != nil && classified.Kind == targetpkg.KindTCID {
		msg := "-r is only valid for a folder repoint, not a single test case."
		output.Errorf(msg, "Drop -r, or target a folder under gtms/test/cases/.")
		return output.AsDisplayed(errors.New(msg))
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

	// ADR-024 s5 / REV-160 CLAUDE-003: ONE project-wide enumeration for the whole
	// repoint, whatever its scope. Every guard below and the write itself answer
	// against THIS snapshot, and the resulting ownership decision is carried in
	// opts.Authority rather than re-derived. A qualified single-TC repoint used to
	// run three independent enumerations and a folder or --all repoint two, so the
	// state the record was written under was not necessarily the state the
	// refusals were made against.
	snap, serr := testcase.NewSnapshot(root)
	if serr != nil {
		output.Errorf(serr.Error(), "Check that gtms/test/cases/ is readable.")
		return output.AsDisplayed(serr)
	}

	// --all: project-wide scope (no positional target).
	if allFlag {
		return runRepointAll(root, snap, opts)
	}

	// Non-all: validateRepointFlags already rejected an empty target, so classified
	// is non-nil here.
	if classified == nil {
		return output.AsDisplayed(fmt.Errorf("internal error: repoint target not classified"))
	}

	// Dispatch strictly by classified kind (target safety already enforced by
	// target.Classify -- validateFolderSafety ran on the complete input).
	switch classified.Kind {
	case targetpkg.KindFolder:
		if fromAdapterFlag == "" {
			msg := "--from-adapter is required for folder repoint."
			output.Errorf(msg, fmt.Sprintf("Example: gtms link %s --from-adapter old --adapter %s", classified.Target, adapterFlag))
			return output.AsDisplayed(errors.New(msg))
		}
		return runRepointFolder(root, snap, classified.Target, recursive, opts)
	case targetpkg.KindTCID:
		// BUG-059 (REV-113): scope-validate a qualified TC, then repoint the flat
		// wiring keyed on the bare ID -- against the operation's snapshot.
		if err := requireScopedSpecIn(snap, root, *classified); err != nil {
			output.Errorf(err.Error(), "Check the folder qualifier, or drop it to match the TC anywhere.")
			return output.AsDisplayed(err)
		}
		// The ONE ownership decision for this repoint. It replaces the separate
		// refuseUnownedTarget call that stood here, which asked the same question
		// against a second snapshot and then threw the answer away, leaving
		// RepointSingle to build a third and resolve the id all over again. The
		// decision is now carried to the write (REV-160 CLAUDE-003). NotFound stays
		// permitted -- see link.AuthoriseSingleRepoint for the orphan-repair policy
		// and its three controls.
		auth, aerr := link.AuthoriseSingleRepoint(snap, classified.TCID)
		if aerr != nil {
			output.Errorf(aerr.Error(), "")
			return output.AsDisplayed(aerr)
		}
		opts.Authority = auth
		return runRepointSingleTC(root, classified.TCID, opts)
	default:
		return output.AsDisplayed(fmt.Errorf("internal error: unclassified repoint target"))
	}
}

// validateRepointFlags rejects invalid flag combinations for repoint mode.
func validateRepointFlags(target, adapterFlag, fromAdapterFlag string, allFlag, recursive bool) error {
	// --all exclusivity.
	if allFlag {
		if target != "" {
			msg := "--all rejects a positional target."
			output.Errorf(msg, "Use: gtms link --all --from-adapter old --adapter new")
			return output.AsDisplayed(errors.New(msg))
		}
		if fromAdapterFlag == "" {
			msg := "--from-adapter is required with --all."
			output.Errorf(msg, "Example: gtms link --all --from-adapter old --adapter new")
			return output.AsDisplayed(errors.New(msg))
		}
		if recursive {
			msg := "-r is redundant with --all (--all already covers the entire project)."
			output.Errorf(msg, "Use: gtms link --all --from-adapter old --adapter new")
			return output.AsDisplayed(errors.New(msg))
		}
	}

	// --adapter without a target or --all is invalid.
	if !allFlag && target == "" {
		msg := "A test case ID or folder is required for repoint, or use --all."
		output.Errorf(msg, "Example: gtms link tc-x --adapter new")
		return output.AsDisplayed(errors.New(msg))
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
func runRepointFolder(root string, snap *testcase.Snapshot, folder string, recursive bool, opts link.RepointOptions) error {
	// ADR-024 / BUG-185 (tc-f8fdee34): folder and recursive repoint WRITE wiring,
	// so they require proven UniqueOwner project-wide before any rewrite. Unlike
	// refresh, repoint has no per-ID hold-back policy: an Ambiguous or Unprovable
	// id refuses the WHOLE operation and leaves every wiring record byte-identical.
	//
	// REV-160 CLAUDE-003: the refusal and the work set now come from ONE decision
	// against the operation's snapshot, carried into RepointBulk. It used to be a
	// CLI-level refusal against one snapshot followed by RepointBulk building
	// another and asking again.
	auth, aerr := link.AuthoriseScopeRepoint(root, snap, folder, recursive)
	if aerr != nil {
		// REV-160 mechanism C, second half. A missing scope folder is the SAME
		// condition refresh renders, so it renders the same way -- through
		// folderNotFoundMessage, not through the raw testcase.ErrFolderNotFound
		// text, which carries neither the phrase callers script against nor the
		// normalised trailing separator.
		if errors.Is(aerr, testcase.ErrFolderNotFound) {
			msg := folderNotFoundMessage("", folder)
			output.Errorf(msg, "Check the folder name and try again.")
			return output.AsDisplayed(errors.New(msg))
		}
		output.Errorf(aerr.Error(), "")
		return output.AsDisplayed(aerr)
	}
	opts.Authority = auth
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
func runRepointAll(root string, snap *testcase.Snapshot, opts link.RepointOptions) error {
	// ADR-024 / BUG-185: --all is the project-wide bulk repoint and rewrites wiring,
	// so it is a mutating consumer requiring UniqueOwner. Like folder/recursive
	// repoint it has NO per-id hold-back, so ANY Ambiguous or Unprovable owner
	// anywhere in the project refuses the whole operation before any rewrite. The
	// whole project is the recursive scope of the cases root (folder "").
	//
	// REV-160 CLAUDE-003: one decision, carried into RepointAll.
	auth, aerr := link.AuthoriseScopeRepoint(root, snap, "", true)
	if aerr != nil {
		output.Errorf(aerr.Error(), "")
		return output.AsDisplayed(aerr)
	}
	opts.Authority = auth
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
