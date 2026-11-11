package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/aitestmanagement/gtms-cli/internal/adapter"
	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/execution"
	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/link"
	"github.com/aitestmanagement/gtms-cli/internal/output"
	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
	"github.com/aitestmanagement/gtms-cli/internal/result"
	targetpkg "github.com/aitestmanagement/gtms-cli/internal/target"
	"github.com/aitestmanagement/gtms-cli/internal/task"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
	"github.com/spf13/cobra"
)

// newExecuteCmd builds the 'gtms execute' command with its flags and subcommands.
func newExecuteCmd() *cobra.Command {
	var adapterFlag string
	var environmentFlag string
	var executedByFlag string
	var frameworkFlag string
	var forceFlag bool
	var failFastFlag bool
	var recursiveFlag bool
	var allowStaleFlag bool
	cmd := &cobra.Command{
		Use:   "execute [test-case-id | folder]",
		Short: "Execute test cases and record results",
		Long: `Execute a test case by delegating to a configured adapter. For automated
test cases, GTMS runs the automation artefact produced by 'gtms automate'
and records the result. For manual test cases, GTMS records the result from
the primed result file.

Single test case:
  gtms execute tc-a1b2c3d4                          -- adapter comes from the wiring record
  gtms execute tc-a1b2c3d4 --adapter runner-b       -- override with a same-framework adapter
  gtms execute tc-a1b2c3d4 --env staging            -- record staging as the environment label and pass it to the adapter

Folder (bulk mode):
  gtms execute my-feature                      -- execute all test cases in gtms/test/cases/my-feature/
  gtms execute my-feature -r                   -- include test cases from subdirectories
  gtms execute my-feature --force              -- re-run test cases skipped as already passing
  gtms execute my-feature --fail-fast          -- stop on first failure or error

All test cases:
  gtms execute -r                              -- execute all test cases across all folders

Manual execute via the prime pipeline:
  gtms execute tc-a1b2c3d4                             -- record from primed result file (manual-preset default)
  gtms execute tc-a1b2c3d4 --adapter manual-execute    -- explicit adapter selection

Adapter execution:
  Runs the adapter from --adapter or the gtms.config default
  (built-in options: agent-execute, manual-execute).
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

			// ENH-125: resolve executed_by once at command entry (flag -> env -> git user.name).
			executedBy := pipeline.ResolveExecutedBy(ctx, root, executedByFlag)

			// No argument = run all test cases from root (recursive implied)
			if len(args) == 0 {
				recursiveFlag = true
				return runBulkExecute(cmd, root, "", adapterFlag, environmentFlag, executedBy, frameworkFlag, forceFlag, failFastFlag, recursiveFlag, allowStaleFlag)
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
					return runBulkExecute(cmd, root, res.Target, adapterFlag, environmentFlag, executedBy, frameworkFlag, forceFlag, failFastFlag, recursiveFlag, allowStaleFlag)
				}
				msg := fmt.Sprintf("Invalid test case ID format: '%s'.", sanitizeForError(res.Target))
				output.Errorf(msg, "Use format like tc-a1b2c3d4.")
				return output.AsDisplayed(errors.New(msg))
			}

			// ADR-024 s5: resolve identity ONCE and pass the resolved owner forward.
			// The scope guard (BUG-059 / REV-113) and the ownership refusal (BUG-185 /
			// BUG-188) run inside it against one snapshot.
			//
			// REV-159 CODEX-001: NotFound is a REFUSAL here, as it already is for
			// automate and prime. It used to be ignored, on the reasoning that a
			// missing spec would surface through the wiring lookup below -- but that is
			// not true on every branch. A Mode-3 execute adapter (manual-execute,
			// agent-execute, ...) BYPASSES wiring lookup entirely, so an orphan id
			// reached adapter resolution with a nil owner, created task and result
			// state, and invoked external code for a test case that does not exist.
			// Even on the wiring branch the nil owner silently disabled the
			// testcase-hash half of the drift preflight, since checkWiringDrift skips
			// the comparison when it has no spec to hash.
			//
			// ADR-024's consumer matrix is explicit for this row: Automate / execute /
			// prime refuse before mutation or invocation on Not found, and adapter
			// invocation requires a uniquely proven owner. Execute was the only one of
			// the three not doing so. The wording matches automate's.
			owner, hint, err := resolveTargetOwner(root, res, "Check the folder qualifier, or drop it to match the TC anywhere.")
			if err != nil {
				if errors.Is(err, testcase.ErrOwnerNotFound) {
					msg := fmt.Sprintf("Test case '%s' not found in gtms/test/cases/", res.Target)
					output.Errorf(msg, "Create the test case first with 'gtms create'.")
					return output.AsDisplayed(errors.New(msg))
				}
				output.Errorf(err.Error(), hint)
				return output.AsDisplayed(err)
			}

			// --- Single TC mode (existing behavior) -- flat wiring key ---
			target := res.TCID

			// ENH-150: Post-fill validation gate -- catches frontmatter corruption
			// before the adapter is invoked. It validates the RESOLVED OWNER.
			if violations := adapter.ValidateTestCaseOwner(root, owner); len(violations) > 0 {
				summary := adapter.FormatValidationErrors(violations)
				output.Errorf(summary, "Fix the test case frontmatter and try again.")
				return output.AsDisplayed(errors.New(summary))
			}

			// Validate --framework flag if provided
			if frameworkFlag != "" && !config.ValidateFramework(frameworkFlag) {
				msg := fmt.Sprintf("Invalid framework '%s'. Framework must contain only lowercase letters, digits, and hyphens.", frameworkFlag)
				output.Errorf(msg, "Example: --framework playwright")
				return output.AsDisplayed(errors.New(msg))
			}

			// CON-023 / ENH-145 / ENH-146 / ENH-163:
			//
			//   - Mode 3 execute adapters (manual-execute, agent-execute,
			//     manual-execute-script, agent-execute-script) bypass
			//     wiring lookup because manual-only TCs have no wiring
			//     records (CON-023 Q#12).
			//   - The bypass fires when either:
			//     (a) --adapter names a Mode 3 adapter, OR
			//     (b) no --adapter flag is set and cfg.Defaults["execute"]
			//         names a Mode 3 adapter (ENH-163).
			//   - Every other path reads the wiring record first to pick the
			//     framework + execute adapter + artefact. The wiring file is
			//     immutable on the execute path -- no auto-heal, no glob
			//     fallback, no ENH-136 auto-create.
			var resolved *adapter.ResolvedAdapter
			var framework string
			var resolvedArtefact string
			var artefactHash string
			// ENH-231: set when a script-only change is to be accepted. The
			// acceptance write happens after every CLI gate, immediately before
			// invocation (Owner Decision 2). acceptSel carries the examined
			// wiring record, the exact file it was read from, and the identity
			// it was selected for (REV-232 CODEX-001) out to the acceptance
			// point. acceptArtefactAbs is the script's absolute path, re-hashed
			// immediately before invocation (REV-232 DCODEX-002).
			var acceptScriptDrift bool
			var acceptSel wiring.Selection
			var acceptArtefactAbs string

			// ENH-163: derive the effective adapter name for Mode 3 bypass.
			// Flag takes precedence; when absent, check defaults.execute.
			effectiveAdapter := adapterFlag
			if effectiveAdapter == "" {
				if defaultName, ok := cfg.Defaults["execute"]; ok && isMode3ExecuteAdapterName(defaultName) {
					effectiveAdapter = defaultName
				}
			}

			isManualPath := isMode3ExecuteAdapterName(effectiveAdapter)
			if isManualPath {
				var rErr error
				resolved, rErr = adapter.Resolve(cfg, "execute", effectiveAdapter)
				if rErr != nil {
					output.Errorf(rErr.Error(), "Check your gtms.config file.")
					return output.AsDisplayed(rErr)
				}
				// BUG-210: a reserved Mode 3 adapter is manual-only and
				// synchronous-only. Refuse a non-manual --framework, a non-manual
				// configured framework, or a configured mode: async here -- before
				// any task, handoff or execution record is created and before the
				// adapter runs. The manual path never does a wiring lookup, so an
				// unwired TC gets this refusal rather than "No wiring record found".
				if msg, hint, refused := adapter.ValidateReservedMode3Framework(resolved.Name, frameworkFlag, resolved.Config.Framework, resolved.Config.Mode); refused {
					output.Errorf(msg, hint)
					return output.AsDisplayed(errors.New(msg))
				}
				// Framework is sourced from the result template inside the
				// manual-execute pipeline; CommandFlags carries the user's
				// explicit flag value only.
				framework = frameworkFlag
			} else {
				// ENH-191: when --adapter is a wiring-driven override, validate
				// it up front and use its implied framework as the wiring selector.
				var overrideResolved *adapter.ResolvedAdapter
				var impliedFramework string
				if adapterFlag != "" {
					var oErr error
					overrideResolved, impliedFramework, oErr = adapter.ResolveWiringExecuteAdapter(cfg, adapterFlag, frameworkFlag)
					if oErr != nil {
						msg := fmt.Sprintf("Cannot use --adapter %q: %v", adapterFlag, oErr)
						output.Errorf(msg, "Check gtms.config adapters.execute for valid same-framework adapters.")
						return output.AsDisplayed(errors.New(msg))
					}
				}

				// Wiring lookup. With override: implied framework selects wiring.
				// With --framework (no override): explicit framework selects.
				// Neither: exactly-one shortcut or disambiguation error.
				var wiringRec *wiring.WiringRecord
				var wiringSel wiring.Selection
				wiringFramework := frameworkFlag
				if impliedFramework != "" {
					wiringFramework = impliedFramework
				}

				if wiringFramework != "" {
					var wErr error
					var wiringPath string
					wiringRec, wiringPath, wErr = wiring.Find(root, target, wiringFramework)
					if wErr != nil {
						output.Errorf(fmt.Sprintf("Reading wiring for %s--%s: %v", target, wiringFramework, wErr),
							"Check the wiring file for parse errors.")
						return output.AsDisplayed(wErr)
					}
					if wiringRec == nil {
						msg := fmt.Sprintf("No wiring record found for '%s' (framework: %s).", target, wiringFramework)
						output.Errorf(msg, fmt.Sprintf("Run 'gtms automate %s --framework %s' (or 'gtms link') to wire it.", target, wiringFramework))
						return output.AsDisplayed(errors.New(msg))
					}
					wiringSel = wiring.Selection{Record: wiringRec, Path: wiringPath, TestCase: target, Framework: wiringFramework}
				} else {
					recs, wErr := wiring.FindAllForTCSelections(root, target)
					if wErr != nil {
						output.Errorf(fmt.Sprintf("Scanning wiring for %s: %v", target, wErr),
							"Check gtms/automation/wiring/ for parse errors.")
						return output.AsDisplayed(wErr)
					}
					if len(recs) == 0 {
						msg := fmt.Sprintf("No wiring records found for '%s'.", target)
						output.Errorf(msg, fmt.Sprintf("Run 'gtms automate %s' (or 'gtms link') to wire it.", target))
						return output.AsDisplayed(errors.New(msg))
					}
					if len(recs) > 1 {
						frameworks := make([]string, len(recs))
						for i, r := range recs {
							frameworks[i] = r.Record.Framework
						}
						msg := fmt.Sprintf("'%s' has multiple wiring records (%s).", target, strings.Join(frameworks, ", "))
						output.Errorf(msg, "Re-run with --framework <name> to choose one.")
						return output.AsDisplayed(errors.New(msg))
					}
					wiringSel = recs[0]
					wiringRec = wiringSel.Record
				}

				// BUG-211: verify the selection the instant it is established,
				// before anything trusts the body's framework, adapter or
				// artefact (adapter resolution, path-safety, the pending
				// bootstrap, hashing, the drift check, or any write). A wiring
				// file whose body declares a different test case or framework
				// than the file it was selected from is a data-integrity error,
				// not a run. VerifySelection covers both reachable mismatches:
				// the explicit --framework test-case/framework mismatch, and the
				// scan-branch framework mismatch (the scan already drops a
				// test-case mismatch by body identity).
				if verr := wiring.VerifySelection(root, wiringSel); verr != nil {
					msg := fmt.Sprintf("Wiring identity mismatch for %s--%s: %v", target, wiringSel.Framework, verr)
					output.Errorf(msg, "The wiring file's body disagrees with its name. Re-create it with 'gtms link'.")
					return output.AsDisplayed(errors.New(msg))
				}

				framework = wiringRec.Framework

				// ENH-191: same-framework override -- resolve the override
				// adapter instead of the wired adapter. The wiring record's
				// adapter field is never mutated.
				if overrideResolved != nil {
					resolved = overrideResolved
				} else {
					// No override -- resolve via the wiring record's adapter.
					var rErr error
					resolved, rErr = adapter.Resolve(cfg, "execute", wiringRec.Adapter)
					if rErr != nil {
						output.Errorf(fmt.Sprintf("Wiring adapter %q not configured: %v", wiringRec.Adapter, rErr),
							"Restore the adapter in gtms.config or re-run automate after fixing it.")
						return output.AsDisplayed(rErr)
					}
				}

				// BUG-057: containment check on the wiring's artefact path
				// before drift/hash/adapter invocation. A tampered or
				// hand-edited wiring file pointing outside projectRoot must
				// fail here, not silently run. Use the canonical absolute
				// path for hashing and the normalised relative form for
				// adapter inputs. Wiring is NOT rewritten on execute -- with
				// one exception: the pending -> real hash bootstrap (ENH-151).
				absArtefact, safeArtefact, safeErr := pathsafe.ResolveUnderRoot(root, wiringRec.Artefact)
				if safeErr != nil {
					msg := fmt.Sprintf("Unsafe wiring artefact path for %s--%s: %v", target, framework, safeErr)
					output.Errorf(msg, fmt.Sprintf("Edit gtms/automation/wiring/%s--%s.wiring.yaml or re-run 'gtms link %s --framework %s --artefact <path inside project root> --force'.", target, framework, target, framework))
					return output.AsDisplayed(errors.New(msg))
				}
				resolvedArtefact = safeArtefact

				// BUG-200: artefact availability is checked regardless of
				// --allow-stale and BEFORE the pending-hash bootstrap. A missing
				// or unreadable artefact refuses here (non-zero) and never
				// invokes the adapter, in every hash/flag state -- single/bulk
				// parity (ENH-151). --allow-stale bypasses hash mismatches only,
				// never artefact availability.
				switch classifyArtefactAvailability(absArtefact) {
				case artefactMissing:
					// ENH-222 Draft I: name the expected file; never a refresh hint.
					printScriptMissingSingle(target, framework, resolvedArtefact)
					return output.AsDisplayed(fmt.Errorf("%s was not run because its automation script is missing", target))
				case artefactUnreadable:
					// ENH-222 Draft J: name the file; never a refresh or regenerate hint.
					printScriptUnreadableSingle(target, resolvedArtefact)
					return output.AsDisplayed(fmt.Errorf("%s was not run because its automation script could not be read", target))
				}

				// ENH-151: bootstrap pending -> real hash before drift check.
				// This runs unconditionally -- --allow-stale does not bypass it.
				// BUG-211: the bootstrap takes the verified selection and writes
				// only the selected file; a concurrent change between hashing and
				// the write is refused without overwriting the newer wiring.
				if _, bErr := bootstrapPendingWiring(root, wiringSel, absArtefact); bErr != nil {
					if errors.Is(bErr, link.ErrConcurrentWiringChange) {
						// ENH-222 Draft F: the saved automation file changed
						// concurrently; the newer version was kept.
						printConcurrentChangeSingle(target, wiringRelPath(root, wiringSel.Path))
						return output.AsDisplayed(fmt.Errorf("%s was not started because its saved automation file changed", target))
					}
					msg := fmt.Sprintf("Cannot bootstrap wiring for %s--%s: %v", target, framework, bErr)
					output.Errorf(msg, "Ensure the artefact file exists and is readable, then re-run.")
					return output.AsDisplayed(errors.New(msg))
				}

				// Drift preflight: testcase-hash + artefact-hash currency.
				// No task is created, no result file is written, wiring is
				// unchanged here.
				//
				// ENH-231: in relaxed mode (execute.on-script-change: accept,
				// the default) a script-ONLY change (artefact-hash drift, spec
				// current, artefact present) is accepted automatically. The
				// acceptance write is deferred until after every remaining CLI
				// gate -- in particular the active-task check below -- so a
				// refusal there still leaves wiring unchanged (Owner Decision
				// 2). Specification drift, a missing artefact, and strict mode
				// all still refuse here.
				if !allowStaleFlag {
					runExecuteTestHook("pre-drift")
					dr := checkWiringDrift(root, wiringRec, resolvedArtefact, owner)
					if dr.diag != "" {
						// ENH-222 message precedence: spec drift leads, then an
						// unverifiable spec next to a script change (both policies),
						// then an accepted script-only change, then a refused script
						// change.
						switch {
						case dr.specStale:
							// Draft A: a changed test case spec (both modes).
							printSpecChangedSingle(target, framework, dr)
							return output.AsDisplayed(fmt.Errorf("%s was not run because its test case spec changed", target))
						case dr.unverifiedScriptChange():
							// Draft K: the script changed but the spec could not be
							// checked (both policies). No refresh can repair it.
							printSpecUnavailableSingle(target, owner.SpecRel())
							return output.AsDisplayed(fmt.Errorf("%s was not run because GTMS could not check its test case spec", target))
						case dr.scriptOnly() && cfg.OnScriptChange() == config.OnScriptChangeAccept:
							acceptScriptDrift = true
							acceptSel = wiringSel
							acceptArtefactAbs = absArtefact
						default:
							// Draft B: a changed automation script under
							// execute.on-script-change: refuse.
							printScriptChangedRefuseSingle(target, framework, dr)
							return output.AsDisplayed(fmt.Errorf("%s was not run because its automation script changed", target))
						}
					}
				}

				artefactHash, _ = pipeline.HashFile(absArtefact)
			}

			if IsVerbose() {
				fmt.Fprintf(os.Stderr, "Resolved adapter: %s (tier %d, mode %s) -- wiring framework: %s\n",
					resolved.Name, resolved.Tier, resolved.Mode, framework)
			}

			// Validate: no duplicate active task for this target
			existing, err := task.FindByTarget(root, "execute", target)
			if err != nil {
				return fmt.Errorf("checking for existing tasks: %w", err)
			}
			if existing != nil {
				msg := fmt.Sprintf("An execute task for %s already exists: gtms/tasks/%s/%s-execute-%s.md",
					target, existing.Status, existing.ID, existing.Target)
				output.Errorf(msg, "Wait for the existing task to complete or remove it.")
				return output.AsDisplayed(errors.New(msg))
			}

			// ENH-231: accept the script edit now that every CLI gate has
			// passed (adapter + wiring resolved, path safe, artefact present,
			// spec current, no active execute task). Owner Decision 2 places
			// acceptance immediately before invocation; Decision 11 re-reads the
			// record on disk and refuses a concurrent change. A failure here is
			// a pre-invocation refusal, so no task/result is created.
			//
			// ENH-222: the acceptance NOTICE is deferred until after invocation so
			// it can report whether the test actually started (Draft D vs E). Only
			// the file names are known here.
			var acceptScriptRel, acceptWiringRel string
			if acceptScriptDrift {
				acceptScriptRel = filepath.ToSlash(resolvedArtefact)
				acceptWiringRel = wiringRelPath(root, acceptSel.Path)
				runExecuteTestHook("pre-accept")
				acceptedHash, aerr := link.AcceptScriptDrift(root, acceptSel)
				if aerr != nil {
					if errors.Is(aerr, link.ErrConcurrentWiringChange) {
						// ENH-222 Draft F: the saved automation file changed
						// concurrently; the newer version was kept.
						printConcurrentChangeSingle(target, acceptWiringRel)
						return output.AsDisplayed(fmt.Errorf("%s was not started because its saved automation file changed", target))
					}
					msg := fmt.Sprintf("Cannot accept script change for %s--%s: %v", target, framework, aerr)
					// ENH-222 (2026-10-28): keep the error line unchanged; the
					// recovery block names the narrow, framework-scoped confirm
					// command per the safety rule. The explanatory line sits at the
					// error printer's 4-space hint indent; the command is four
					// spaces further (relative layout the owner pinned).
					output.Errorf(msg, "")
					printAcceptFailureConfirmHint(os.Stderr, "    ", target, framework)
					return output.AsDisplayed(errors.New(msg))
				}
				// REV-232 CODEX-002: the invocation carries the exact hash the
				// wiring now records, not the pre-acceptance preflight hash.
				artefactHash = acceptedHash
				runExecuteTestHook("post-accept")
				// REV-232 DCODEX-002: the invocation must carry the hash of the
				// script it runs. If the script changed again since acceptance,
				// refuse without invoking and without updating wiring again.
				if cerr := link.ConfirmAcceptedArtefact(acceptArtefactAbs, acceptedHash); cerr != nil {
					// ENH-222 Draft H: the script changed again while GTMS was
					// preparing the test; name the file already updated.
					printChangedAgainSingle(target, framework, acceptScriptRel, acceptWiringRel)
					return output.AsDisplayed(fmt.Errorf("%s was not run because %s changed again", target, acceptScriptRel))
				}
			}

			// Invoke adapter with the wiring-resolved artefact / framework.
			flags := adapter.CommandFlags{
				Owner:        owner,
				Adapter:      resolved.Name,
				ArtefactFile: resolvedArtefact,
				Environment:  environmentFlag,
				ExecutedBy:   executedBy,
				ArtefactHash: artefactHash,
				Framework:    framework,
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

			// ENH-222 Owner Decision 4: the structured start signal, readable even
			// when InvokeWithRoot returned an error.
			started := adapter.Started(result, err)

			if acceptScriptDrift {
				if started {
					// Draft D: accepted and ran (only after dispatch is proven).
					printAcceptedRanSingle(acceptScriptRel, target, acceptWiringRel)
				} else {
					// Draft E: accepted but the test could not start.
					reason := "the adapter could not be started"
					if err != nil {
						reason = err.Error()
					}
					printAcceptedCouldNotStartSingle(acceptScriptRel, target, reason, acceptWiringRel)
					return err
				}
			}

			// ENH-222 Draft G: --allow-stale says the checks were bypassed for
			// this run only, and only for a test that actually started.
			if allowStaleFlag && started {
				printAllowStaleRanLine(target)
				printAllowStaleFutureLine()
			}

			if err != nil {
				// BUG-209 AC33: say the test ran, whatever flags were used, so an operator
				// can tell a run with side effects from one that never started. The two
				// branches above have already said so.
				if started && !acceptScriptDrift && !allowStaleFlag {
					printStartedThenFailed(target)
				}
				return err
			}

			// Format output
			formatExecuteOutput(result)

			// ENH-130: status: error = adapter/infrastructure failure -> non-zero exit.
			// status: complete (any result) = clean adapter run -> exit 0.
			if result.Status == "error" {
				return output.AsDisplayed(fmt.Errorf("task failed: %s", result.Summary))
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&adapterFlag, "adapter", "", "Adapter to use (same-framework override or Mode 3 prime-path selection)")
	cmd.Flags().StringVar(&environmentFlag, "env", "", "Environment label recorded and passed to the adapter; GTMS does not route on it")
	cmd.Flags().StringVar(&executedByFlag, "executed-by", "", "Identity to record on the executed_by field (defaults to GTMS_EXECUTED_BY, then git user.name)")
	cmd.Flags().StringVar(&frameworkFlag, "framework", "", "Framework to select among a TC's wiring records")
	cmd.Flags().BoolVar(&forceFlag, "force", false, "Re-run already-passing test cases in bulk mode (change checks still apply; an automated test case with an active execute task is still skipped; manual and agent execution unchanged)")
	cmd.Flags().BoolVar(&failFastFlag, "fail-fast", false, "Stop on first failure or error in bulk mode")
	cmd.Flags().BoolVarP(&recursiveFlag, "recursive", "r", false, "Include test cases from subdirectories")
	cmd.Flags().BoolVar(&allowStaleFlag, "allow-stale", false, "Bypass the spec and script change checks for this run only (accepts no change for later runs and does NOT update wiring)")

	// Add execute status subcommand
	cmd.AddCommand(newExecuteStatusCmd())

	return cmd
}

// bootstrapPendingWiring performs the one-way pending -> <real hash>
// transition on the selected wiring record's artefact-hash field (ENH-151).
// If sel.Record's artefact-hash is not PendingArtefactHash, this is a no-op
// and the existing hash is returned.
//
// BUG-211: the bootstrap takes the wiring.Selection so identity comes from the
// file execute selected, never from the record body. It verifies the selection
// before hashing (self-safety; the caller already verified it under constraint
// 1), hashes the artefact, then -- immediately before writing -- re-reads the
// same selected file and refuses with link.ErrConcurrentWiringChange if any of
// the six fields or the identity changed since selection (the initial verify
// cannot catch a change that happens during hashing; REV-232 DCODEX-001). It
// writes only the selected record, with identity taken from the verified
// selection, and confirms the write target is the selected file.
//
// Success / failure contract: on success it returns the exact hash it wrote and
// updates sel.Record.ArtefactHash in memory ONLY after the disk write succeeds,
// so the downstream drift check sees the real hash. On any failure (identity
// mismatch, concurrent change, unreadable artefact, write error) both the
// on-disk file and the in-memory record are left unchanged, still pending, and
// the error is returned so the caller can abort before invoking the adapter.
//
// execute may only transition pending -> <real hash>. It never overwrites a
// non-pending hash.
func bootstrapPendingWiring(root string, sel wiring.Selection, absArtefact string) (string, error) {
	rec := sel.Record
	if rec == nil {
		return "", errors.New("wiring record is required")
	}
	if !wiring.IsPendingArtefactHash(rec.ArtefactHash) {
		return rec.ArtefactHash, nil
	}

	// Self-safety: the caller verified the selection under constraint 1, but
	// keep the function correct on its own.
	if err := wiring.VerifySelection(root, sel); err != nil {
		return "", err
	}

	realHash, err := pipeline.HashFile(absArtefact)
	if err != nil {
		return "", fmt.Errorf("artefact %s is not readable: %w", rec.Artefact, err)
	}

	// Concurrent-change window: a test can model a competing writer here.
	runExecuteTestHook("post-bootstrap-hash")

	// Re-read the selected file immediately before the write and refuse if it
	// no longer matches what the preflight examined (REV-232 DCODEX-001).
	if err := recheckBootstrapWiring(sel); err != nil {
		return "", err
	}

	// Identity comes from the verified selection, not the body; only the
	// artefact-hash changes. Every other field is preserved verbatim.
	updated := &wiring.WiringRecord{
		TestCase:     sel.TestCase,
		TestCaseHash: rec.TestCaseHash,
		Framework:    sel.Framework,
		Adapter:      rec.Adapter,
		Artefact:     rec.Artefact,
		ArtefactHash: realHash,
	}

	// The writer derives its target from updated's identity. Confirm, before
	// writing, that this is the selected file (VerifySelection already proved
	// it; this keeps a future change to wiring.Path from redirecting the write).
	target, err := wiring.Path(root, updated.TestCase, updated.Framework)
	if err != nil {
		return "", fmt.Errorf("writing updated wiring: %w", err)
	}
	if filepath.Clean(target) != filepath.Clean(sel.Path) {
		return "", fmt.Errorf("%w: bootstrap would write to %s, selected %s", wiring.ErrIdentityMismatch, target, sel.Path)
	}

	if _, err := wiring.Write(root, updated); err != nil {
		return "", fmt.Errorf("writing updated wiring: %w", err)
	}

	// Update the in-memory record only after the disk write succeeds, so the
	// drift check below uses the real hash and any failure leaves it pending.
	rec.ArtefactHash = realHash
	return realHash, nil
}

// recheckBootstrapWiring re-reads sel.Path and refuses with
// link.ErrConcurrentWiringChange unless it still declares the expected identity
// and still matches sel.Record in all six fields. It is the bootstrap's
// equivalent of internal/link.recheckSelectedWiring (ENH-231 Owner Decision 11),
// kept local so the one allowed execute-path wiring write stays in this file.
func recheckBootstrapWiring(sel wiring.Selection) error {
	onDisk, err := wiring.Read(sel.Path)
	if err != nil {
		return fmt.Errorf("re-reading wiring before bootstrap: %w", err)
	}
	// The selection was identity-verified before this re-read, so an identity
	// mismatch here means the file changed underneath us: report it as a
	// concurrent change (retry), keeping the identity error in the chain.
	if err := wiring.VerifyIdentity(onDisk, sel.TestCase, sel.Framework); err != nil {
		return fmt.Errorf("%w: %w", link.ErrConcurrentWiringChange, err)
	}
	if !sameBootstrapRecord(onDisk, sel.Record) {
		return link.ErrConcurrentWiringChange
	}
	return nil
}

// sameBootstrapRecord reports whether the on-disk record still matches the
// state the bootstrap selected. All six fields must be identical; any
// difference means a concurrent writer changed the record.
func sameBootstrapRecord(a, b *wiring.WiringRecord) bool {
	return a.TestCase == b.TestCase &&
		a.TestCaseHash == b.TestCaseHash &&
		a.Framework == b.Framework &&
		a.Adapter == b.Adapter &&
		a.Artefact == b.Artefact &&
		a.ArtefactHash == b.ArtefactHash
}

// wiringDrift is the classified outcome of the execute drift preflight. diag is
// the user-facing diagnostic (empty when the wiring is current); the booleans
// let the caller apply the ENH-231 policy, which treats specification drift and
// a missing artefact differently from a script-only change.
type wiringDrift struct {
	diag          string // non-empty => drift or missing; execution must not proceed under strict handling
	specStale     bool   // testcase-hash differs from the resolved spec
	artefactStale bool   // artefact-hash differs from the current script content
	missing       bool   // the artefact does not resolve on disk
	unreadable    bool   // the artefact exists but cannot be read, so it cannot be fingerprinted
	// specUnverified: no spec path resolved, or the spec could not be hashed
	// (missing, unreadable, raced away). That is "spec unverified", never
	// "spec current" (REV-232 CLAUDE-001). specProblem says why.
	specUnverified bool
	specProblem    string
	// ENH-222: the recorded and current fingerprints, carried so the Draft A/B
	// printers can show them under -v without re-hashing. Empty when not computed.
	expectedTestcaseHash string
	currentTestcaseHash  string
	expectedArtefactHash string
	currentArtefactHash  string
}

// unverifiedScriptChange reports a script change next to a spec that could
// not be verified. ENH-222: in BOTH policies this is refused as "spec
// unavailable" (Draft K) -- a refresh cannot fix a missing or unreadable spec.
func (d wiringDrift) unverifiedScriptChange() bool {
	return d.artefactStale && !d.specStale && d.specUnverified && !d.missing && !d.unreadable
}

// scriptOnly reports a change the ENH-231 policy may accept automatically:
// the script changed, the spec was checked and is current, and the script is
// present and readable. An unverified spec is never script-only.
func (d wiringDrift) scriptOnly() bool {
	return d.artefactStale && !d.specStale && !d.specUnverified && !d.missing && !d.unreadable
}

// ENH-222 plain-language execute messages. Every helper writes to stderr and
// renders file paths with filepath.ToSlash so the wording is identical on every
// OS. The drafts are the owner-agreed wording recorded verbatim in ENH-222; only
// wrapping, indentation and glyph placement may vary. tcRel/wiringRel/specRel are
// forward-slash, project-relative paths.

// wiringRelPath returns the project-relative, forward-slash path of a wiring file.
func wiringRelPath(root, absPath string) string {
	if rel, err := filepath.Rel(root, absPath); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(absPath)
}

// printSpecChangedSingle is Draft A: a changed test case spec (both modes). Spec
// drift leads over script drift. Fingerprints appear only under -v.
func printSpecChangedSingle(tcID, framework string, dr wiringDrift) {
	fmt.Fprintf(os.Stderr, "%s was not run because its test case spec changed.\n\n", tcID)
	fmt.Fprintln(os.Stderr, "Check that the automation script still tests the current spec, then confirm it with:")
	fmt.Fprintf(os.Stderr, "gtms link --refresh %s --framework %s\n", tcID, framework)
	printDriftFingerprintsVerbose(dr)
}

// printScriptChangedRefuseSingle is Draft B: a changed automation script under
// execute.on-script-change: refuse. Only framework-scoped recovery is offered.
func printScriptChangedRefuseSingle(tcID, framework string, dr wiringDrift) {
	fmt.Fprintf(os.Stderr, "%s was not run because its automation script changed.\n\n", tcID)
	fmt.Fprintln(os.Stderr, "This project is configured to stop when an automation script changes.")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Review the changed script. If the change is expected, mark this version as current with:")
	fmt.Fprintf(os.Stderr, "gtms link --refresh %s --framework %s\n\n", tcID, framework)
	fmt.Fprintln(os.Stderr, "To run it once without marking it as current:")
	fmt.Fprintf(os.Stderr, "gtms execute %s --framework %s --allow-stale\n", tcID, framework)
	printDriftFingerprintsVerbose(dr)
}

// printSpecUnavailableSingle is Draft K: the script changed but the spec could
// not be checked (both policies). No refresh hint -- a refresh cannot repair it.
func printSpecUnavailableSingle(tcID, specRel string) {
	fmt.Fprintf(os.Stderr, "%s was not run because GTMS could not check its test case spec:\n", tcID)
	fmt.Fprintf(os.Stderr, "%s\n\n", filepath.ToSlash(specRel))
	fmt.Fprintln(os.Stderr, "Restore the file or make it readable, then retry.")
}

// printScriptMissingSingle is Draft I: a missing automation script (single).
func printScriptMissingSingle(tcID, framework, scriptRel string) {
	fmt.Fprintf(os.Stderr, "%s was not run because its automation script is missing.\n\n", tcID)
	fmt.Fprintln(os.Stderr, "Expected file:")
	fmt.Fprintf(os.Stderr, "%s\n\n", filepath.ToSlash(scriptRel))
	fmt.Fprintln(os.Stderr, "Restore the file, or regenerate it:")
	fmt.Fprintf(os.Stderr, "gtms automate %s --framework %s --force\n", tcID, framework)
}

// printScriptUnreadableSingle is Draft J: an unreadable automation script (single).
func printScriptUnreadableSingle(tcID, scriptRel string) {
	fmt.Fprintf(os.Stderr, "%s was not run because its automation script could not be read.\n\n", tcID)
	fmt.Fprintln(os.Stderr, "File:")
	fmt.Fprintf(os.Stderr, "%s\n\n", filepath.ToSlash(scriptRel))
	fmt.Fprintln(os.Stderr, "Check that the path is a readable file, then retry.")
}

// printAcceptedRanSingle is Draft D: default mode, script change accepted and the
// test actually started. Printed only after dispatch is proven.
func printAcceptedRanSingle(scriptRel, tcID, wiringRel string) {
	fmt.Fprintf(os.Stderr, "Script changed: %s\n", filepath.ToSlash(scriptRel))
	fmt.Fprintf(os.Stderr, "GTMS accepted the change and ran %s.\n\n", tcID)
	fmt.Fprintln(os.Stderr, "File updated:")
	fmt.Fprintf(os.Stderr, "%s\n", filepath.ToSlash(wiringRel))
}

// printAcceptedCouldNotStartSingle is Draft E: the script change was accepted but
// the test could not start. Never uses the successful "ran" form.
func printAcceptedCouldNotStartSingle(scriptRel, tcID, reason, wiringRel string) {
	fmt.Fprintf(os.Stderr, "Script change accepted: %s\n", filepath.ToSlash(scriptRel))
	fmt.Fprintf(os.Stderr, "GTMS could not start %s: %s\n\n", tcID, reason)
	fmt.Fprintln(os.Stderr, "File updated:")
	fmt.Fprintf(os.Stderr, "%s\n", filepath.ToSlash(wiringRel))
}

// printChangedAgainSingle is Draft H: the script changed again after acceptance
// while GTMS was preparing the test. The retry keeps the run's --framework.
func printChangedAgainSingle(tcID, framework, scriptRel, wiringRel string) {
	fmt.Fprintf(os.Stderr, "%s was not run because %s changed again while GTMS was preparing the test.\n\n", tcID, filepath.ToSlash(scriptRel))
	fmt.Fprintln(os.Stderr, "File updated before the script changed again:")
	fmt.Fprintf(os.Stderr, "%s\n\n", filepath.ToSlash(wiringRel))
	fmt.Fprintln(os.Stderr, "Finish editing the script, then retry:")
	fmt.Fprintf(os.Stderr, "gtms execute %s --framework %s\n", tcID, framework)
}

// printConcurrentChangeSingle is Draft F: the saved automation file changed
// concurrently; GTMS kept the newer version and did not overwrite it.
func printConcurrentChangeSingle(tcID, wiringRel string) {
	fmt.Fprintf(os.Stderr, "%s was not started because its saved automation file changed while GTMS was preparing the run.\n\n", tcID)
	fmt.Fprintln(os.Stderr, "File:")
	fmt.Fprintf(os.Stderr, "%s\n\n", filepath.ToSlash(wiringRel))
	fmt.Fprintln(os.Stderr, "GTMS kept the newer version and did not overwrite it. Retry the command.")
}

// printAcceptFailureConfirmHint writes the ENH-222 (2026-10-28) accept-failure
// recovery block to w: an explanatory line at baseIndent, then the
// framework-scoped confirm command indented exactly four spaces more than the
// explanatory line. The owner pinned the RELATIVE layout (command four spaces
// beyond the explanatory line); the absolute indent differs between the single
// path (baseIndent "    ", under the error printer's hint indent) and the bulk
// path (baseIndent "", directly under the per-test-case error line). The command
// always carries --framework per the Refresh-command safety rule.
func printAcceptFailureConfirmHint(w *os.File, baseIndent, tcID, framework string) {
	fmt.Fprintf(w, "%sAfter fixing the problem above, confirm this script with:\n", baseIndent)
	fmt.Fprintf(w, "%s    gtms link --refresh %s --framework %s\n", baseIndent, tcID, framework)
}

// printAllowStaleRanLine is Draft G's first line: printed only for a test case
// that actually started under --allow-stale.
func printAllowStaleRanLine(tcID string) {
	fmt.Fprintf(os.Stderr, "Ran %s with change checks bypassed for this run only.\n", tcID)
}

// printAllowStaleFutureLine is Draft G's second line: change checks bypassed for
// this run only; no spec or script change was accepted for future runs.
func printAllowStaleFutureLine() {
	fmt.Fprintln(os.Stderr, "GTMS did not accept any spec or script changes for future runs.")
}

// printDriftFingerprintsVerbose shows the recorded and current fingerprints, but
// only under -v (ENH-222: fingerprints are verbose-only).
func printDriftFingerprintsVerbose(dr wiringDrift) {
	if !IsVerbose() {
		return
	}
	fmt.Fprintf(os.Stderr, "    Expected testcase-hash=%s, current=%s.\n", dr.expectedTestcaseHash, dr.currentTestcaseHash)
	fmt.Fprintf(os.Stderr, "    Expected artefact-hash=%s, current=%s.\n", dr.expectedArtefactHash, dr.currentArtefactHash)
}

// executeTestHook, when set by a test, is called at named points of the
// wiring execute path ("post-bootstrap-hash" between hashing and the pending
// bootstrap write, "pre-drift" before the drift preflight, "pre-accept"
// immediately before automatic script acceptance, "post-accept" right after
// it) so tests can model a concurrent script or wiring edit in those windows.
// Always nil in production.
var executeTestHook func(stage string)

func runExecuteTestHook(stage string) {
	if executeTestHook != nil {
		executeTestHook(stage)
	}
}

// checkWiringDrift recomputes testcase-hash and artefact-hash against
// current file content and classifies the result: a diagnostic naming the
// stale fields plus which fields drifted (ENH-231). diag is empty when no
// drift is detected.
//
// CON-023 / ENH-145: drift is a CLI preflight error -- no task is created,
// no result file is written, and the wiring file is not touched. Pass
// --allow-stale at the CLI to bypass this check for a single run.
//
// ENH-231: the caller decides, from specStale/artefactStale/missing and the
// execute.on-script-change policy, whether a script-only change is accepted
// automatically. A missing artefact and specification drift always refuse.
func checkWiringDrift(root string, w *wiring.WiringRecord, resolvedArtefact string, owner *testcase.Owner) wiringDrift {
	var staleFields []string
	var expectedTC, currentTC, expectedArt, currentArt string
	var specStale, artefactStale, specUnverified bool
	var specProblem string

	// ADR-024 s5: drift compares the wiring's recorded hash against the spec the
	// command RESOLVED. Re-deriving the spec here would let drift be judged against
	// a different file than the one execute is about to run.
	//
	// REV-232 CLAUDE-001: an empty spec path or a spec that cannot be hashed is
	// "spec unverified". On its own it does not refuse (current wiring keeps its
	// pre-ENH-231 behaviour), but it can never make a script change look
	// script-only, so such a change is never accepted automatically.
	if specAbs := testcase.OwnerAbsFor(root, owner); specAbs == "" {
		specUnverified = true
		specProblem = "no specification path was resolved"
	} else if h, hErr := pipeline.HashFile(specAbs); hErr != nil {
		specUnverified = true
		specProblem = fmt.Sprintf("specification cannot be read: %v", hErr)
	} else {
		currentTC = h
		expectedTC = w.TestCaseHash
		if h != w.TestCaseHash {
			staleFields = append(staleFields, "testcase-hash")
			specStale = true
		}
	}

	artefactAbs := pipeline.AbsArtefactPath(root, resolvedArtefact)
	if _, statErr := os.Stat(artefactAbs); statErr != nil {
		return wiringDrift{
			diag:    fmt.Sprintf("Missing artefact for %s--%s: %s does not resolve on disk.", w.TestCase, w.Framework, w.Artefact),
			missing: true,
		}
	}
	// ENH-231 Owner Decision 6: an artefact that exists but cannot be read is
	// refused like a missing one. Before ENH-231 a hash error was swallowed
	// here, the drift check reported "current", and the adapter ran.
	h, hErr := pipeline.HashFile(artefactAbs)
	if hErr != nil {
		return wiringDrift{
			diag:       fmt.Sprintf("Unreadable artefact for %s--%s: %s cannot be read: %v", w.TestCase, w.Framework, w.Artefact, hErr),
			unreadable: true,
		}
	}
	currentArt = h
	expectedArt = w.ArtefactHash
	if h != w.ArtefactHash {
		staleFields = append(staleFields, "artefact-hash")
		artefactStale = true
	}

	if len(staleFields) == 0 {
		return wiringDrift{specUnverified: specUnverified, specProblem: specProblem}
	}
	diag := fmt.Sprintf(
		"Stale wiring for %s--%s on %s. Expected testcase-hash=%s, current=%s. Expected artefact-hash=%s, current=%s.",
		w.TestCase, w.Framework, strings.Join(staleFields, ", "),
		expectedTC, currentTC, expectedArt, currentArt,
	)
	// REV-232 DCLAUDE-001: diag stays the stale-wiring diagnostic. Whether an
	// unverified spec turns it into a "spec unverified" refusal depends on the
	// execute.on-script-change policy, which the caller applies.
	return wiringDrift{
		diag:                 diag,
		specStale:            specStale,
		artefactStale:        artefactStale,
		specUnverified:       specUnverified,
		specProblem:          specProblem,
		expectedTestcaseHash: expectedTC,
		currentTestcaseHash:  currentTC,
		expectedArtefactHash: expectedArt,
		currentArtefactHash:  currentArt,
	}
}

// artefactState classifies whether a resolved artefact can be run.
type artefactState int

const (
	artefactPresent    artefactState = iota // exists and is readable as a file
	artefactMissing                         // does not resolve on disk
	artefactUnreadable                      // exists but cannot be read (e.g. a directory at the path)
)

// classifyArtefactAvailability reports whether absArtefact is present, missing,
// or unreadable. BUG-200: availability is checked regardless of --allow-stale
// and BEFORE the pending-hash bootstrap, so a missing or unreadable artefact is
// "not run" in every hash/flag state on both the single and bulk paths. A
// directory placed at the artefact path reads as unreadable (os.ReadFile fails)
// rather than missing (os.Stat succeeds), which is the cross-platform way to
// express "exists but cannot be read as a file" without relying on permissions.
func classifyArtefactAvailability(absArtefact string) artefactState {
	if _, err := os.Stat(absArtefact); err != nil {
		return artefactMissing
	}
	if _, err := pipeline.HashFile(absArtefact); err != nil {
		return artefactUnreadable
	}
	return artefactPresent
}

// skipIcon returns the appropriate status icon for a bulk-execute skip reason.
// "already passing" is a healthy state (complete icon); "test skipped" is a
// runtime skip outcome (skipped icon); all others indicate a problem (warning icon).
func skipIcon(reason string) string {
	if reason == "already passing" {
		return output.IconComplete
	}
	if reason == "test skipped" {
		return output.IconSkipped
	}
	return output.IconWarning
}

func firstLine(text string) string {
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		return text[:idx]
	}
	return text
}

// printIndentedDetail writes the continuation lines of a multi-line error to w
// under a uniform 4-space indent. Any leading whitespace embedded in the source
// string (e.g. discover.go's `\n  ` candidate-list indent) is stripped first so
// every continuation line lands at the same column.
func printIndentedDetail(w *os.File, text string) {
	lines := strings.Split(text, "\n")
	for _, line := range lines[1:] {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" {
			continue
		}
		fmt.Fprintf(w, "    %s\n", trimmed)
	}
}

// runBulkExecute handles the bulk (folder) path for the execute command.
//
// CON-023 / ENH-145 / ENH-146 / ENH-163:
//   - Mode 3 execute adapters bypass wiring and run every TC
//     unconditionally -- manual TCs have no wiring records.
//   - The bypass fires when --adapter names a Mode 3 adapter OR when
//     no flag is set and cfg.Defaults["execute"] names one (ENH-163).
//   - Every other path resolves per-TC through wiring: pick the wiring
//     record (filtered by --framework or by single-match shortcut), check
//     hash currency (skip on drift unless --allow-stale), then invoke
//     through the wiring's adapter + framework + artefact path.
func runBulkExecute(cmd *cobra.Command, root string, folder string, adapterFlag, environmentFlag, executedBy, frameworkFlag string, force, failFast, recursive, allowStale bool) error {
	ctx := cmd.Context()
	cfg := GetConfig()

	// REV-234 CLAUDE-001: validate --framework exactly as the single-test path
	// does. Bulk returns before that check, so on a case-insensitive filesystem
	// --framework BATS opened tc--bats.wiring.yaml and was then refused as a
	// misleading wiring identity mismatch.
	if frameworkFlag != "" && !config.ValidateFramework(frameworkFlag) {
		msg := fmt.Sprintf("Invalid framework '%s'. Framework must contain only lowercase letters, digits, and hyphens.", frameworkFlag)
		output.Errorf(msg, "Example: --framework playwright")
		return output.AsDisplayed(errors.New(msg))
	}

	// ADR-024 / BUG-185 (tc-dbc64e99) + BUG-188: the work-set is the project-wide
	// body-identity OWNERS under the folder; the whole run refuses before any
	// adapter is invoked on an Ambiguous or Unprovable id. Body-id owners (not the
	// filename-truncated DiscoverTestCases set) keep an extended-id sibling from
	// being truncated, skipped, and the wrong id invoked against an adapter.
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

	// BUG-209 AC24 / tc-ce73503b: a refused .gtms/results entry (an unsafe leaf)
	// or an escaping results directory cannot be attributed and may be the newest
	// evidence, so it vetoes EVERY already-passing skip for this run -- the tests
	// run rather than being skipped. It is warned ONCE here (per path), and it does
	// NOT by itself make a completed run fail: the exit follows the tests actually
	// run (section 8). The active-task guard is unaffected.
	var vetoAlreadyPassing bool
	var passCandidates map[string][]executePassCandidate
	// Refused paths already shown in full this run, so each is named once (section 8).
	reportedRefusals := make(map[string]bool)
	scan := result.OpenScan(root)
	if len(scan.Refusals) > 0 || scan.DirRefusal != nil {
		vetoAlreadyPassing = true
		refs := make([]*result.RefusedHandoffError, 0, len(scan.Refusals))
		for _, r := range scan.Refusals {
			refs = append(refs, r.Err)
		}
		renderHandoffRefusals(scan.DirRefusal, refs)
		for _, r := range refs {
			reportedRefusals[r.Rel] = true
		}
		if scan.DirRefusal != nil {
			reportedRefusals[scan.DirRefusal.Rel] = true
		}
	}
	// Build the already-passing Source-1 candidates from the SAME scan, so the
	// whole bulk run scans .gtms/results exactly once (not once per test case).
	passCandidates = buildExecutePassCandidates(scan)
	scan.Close()

	// ENH-163: derive effective adapter for Mode 3 bypass (same logic as
	// single-TC path). Flag takes precedence; when absent, check defaults.
	effectiveAdapter := adapterFlag
	if effectiveAdapter == "" {
		if defaultName, ok := cfg.Defaults["execute"]; ok && isMode3ExecuteAdapterName(defaultName) {
			effectiveAdapter = defaultName
		}
	}

	// Manual-execute path is single-adapter, single-framework. Resolve once.
	isManualPath := isMode3ExecuteAdapterName(effectiveAdapter)
	var manualResolved *adapter.ResolvedAdapter
	if isManualPath {
		manualResolved, err = adapter.Resolve(cfg, "execute", effectiveAdapter)
		if err != nil {
			output.Errorf(err.Error(), "Check your gtms.config file.")
			return output.AsDisplayed(err)
		}
		// BUG-210: a reserved Mode 3 adapter is manual-only and synchronous-only.
		// Refuse a non-manual --framework, a non-manual configured framework, or a
		// configured mode: async for the WHOLE bulk run, before the "Processing ..."
		// line and before any test case is processed or the adapter is invoked
		// (criteria 6, 2b).
		if msg, hint, refused := adapter.ValidateReservedMode3Framework(manualResolved.Name, frameworkFlag, manualResolved.Config.Framework, manualResolved.Config.Mode); refused {
			output.Errorf(msg, hint)
			return output.AsDisplayed(errors.New(msg))
		}
		if IsVerbose() {
			fmt.Fprintf(os.Stderr, "Resolved manual adapter: %s (tier %d, mode %s)\n",
				manualResolved.Name, manualResolved.Tier, manualResolved.Mode)
		}
	}

	// ENH-191: when --adapter is a wiring-driven override, validate it once
	// up front (before any TC is processed) and derive the implied framework.
	var overrideResolved *adapter.ResolvedAdapter
	var impliedFramework string
	if !isManualPath && adapterFlag != "" {
		overrideResolved, impliedFramework, err = adapter.ResolveWiringExecuteAdapter(cfg, adapterFlag, frameworkFlag)
		if err != nil {
			msg := fmt.Sprintf("Cannot use --adapter %q: %v", adapterFlag, err)
			output.Errorf(msg, "Check gtms.config adapters.execute for valid same-framework adapters.")
			return output.AsDisplayed(errors.New(msg))
		}
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

	// ENH-222: the run-level framework filter used by the safe-hint logic.
	runFramework := frameworkFlag
	if impliedFramework != "" {
		runFramework = impliedFramework
	}
	bg := newBulkGuidance(root, folder, runFramework, recursive)

	passed := 0
	skipped := 0
	notRun := 0 // BUG-200: refusals (not run) -- non-zero exit, do NOT stop --fail-fast
	failed := 0 // assertion failures (ENH-130: result.Result == "fail")
	errored := 0 // infrastructure errors (result.Status == "error" or result.Result == "error")

	for i, owner := range owners {
		tcID := owner.ID()
		idx := i + 1

		var resolved *adapter.ResolvedAdapter
		var framework string
		var resolvedArtefact string
		var artefactHash string
		// ENH-222: accept-notice state, carried out of the wiring branch so the
		// post-invoke section can report Draft D2 (ran) vs Draft E (could not
		// start) once the start signal is known.
		acceptScriptDrift := false
		var acceptScriptRel, acceptWiringRel string

		if isManualPath {
			resolved = manualResolved
			framework = frameworkFlag
		} else {
			// ENH-191: when override is active, use the implied framework
			// as the wiring selector. Otherwise use the explicit --framework.
			bulkFramework := frameworkFlag
			if impliedFramework != "" {
				bulkFramework = impliedFramework
			}

			// Wiring selection for this TC. BUG-200: SelectForExecute separates
			// an ordinary miss (skip, exit 0) from a refusal (not run, non-zero)
			// and from malformed-applicable wiring (error, stops --fail-fast).
			sel, selErr := wiring.SelectForExecute(root, tcID, bulkFramework)
			if selErr != nil {
				// A genuine directory-scan failure is an error, as before.
				errored++
				fmt.Fprintf(os.Stderr, "  %s %-16s error: %s  (%d/%d)\n",
					output.IconError, tcID, truncateReason("wiring scan error: "+selErr.Error(), 40), idx, total)
				if failFast {
					break
				}
				continue
			}
			// Map the classified selection. Written as an if-chain (not a
			// switch) so that `break` targets the bulk FOR loop for --fail-fast,
			// matching every other error site in this loop.
			if sel.Outcome == wiring.ExecuteError {
				// Malformed/unreadable wiring that applies to this TC: an error
				// on every selection path, no fallback (BUG-200 Unreadable
				// wiring attribution). Stops --fail-fast like other errors.
				// ENH-222 Draft M: name the exact saved automation file (full
				// path, not truncated); its unprovable state suppresses any
				// broader mutating hint (it never enters an accept/refuse group).
				errored++
				wiringRel := tcID + "--" + bulkFramework + wiring.FileSuffix
				if abs, perr := wiring.Path(root, tcID, bulkFramework); perr == nil {
					wiringRel = wiringRelPath(root, abs)
				}
				fmt.Fprintf(os.Stderr, "  %s %-16s error: saved automation file is invalid: %s  (%d/%d)\n",
					output.IconError, tcID, wiringRel, idx, total)
				if failFast {
					break
				}
				continue
			}
			if sel.Outcome == wiring.ExecuteNotRun {
				// Ambiguous multi-framework wiring with no --framework: GTMS
				// cannot tell which script to run. Not run (non-zero), does NOT
				// stop --fail-fast. ENH-222 Draft L: translate the internal
				// "multiple frameworks" reason to plain language at the CLI
				// boundary (the wiring package keeps its internal reason).
				notRun++
				fmt.Fprintf(os.Stderr, "  %s %-16s not run (choose a framework)  (%d/%d)\n",
					output.IconWarning, tcID, idx, total)
				printChooseFrameworkBulkBlock(root, tcID)
				continue
			}
			if sel.Outcome == wiring.ExecuteSkip {
				skipReason := sel.Reason
				// ENH-191: when override is active and no wiring matched, name
				// the override-framework mismatch. Still an ordinary skip.
				if overrideResolved != nil {
					skipReason = fmt.Sprintf("no %s wiring (override adapter: %s)", impliedFramework, adapterFlag)
				}
				skipped++
				fmt.Fprintf(os.Stderr, "  %s %-16s skipped (%s)  (%d/%d)\n",
					skipIcon(skipReason), tcID, skipReason, idx, total)
				continue
			}
			wiringSel := sel.Selection

			// BUG-211: verify the selection immediately after selection
			// returns a real record, before the body's framework, adapter or
			// artefact is trusted. A filename/body identity mismatch is an ERROR
			// (not a skip), counted and subject to --fail-fast, so a corrupt
			// record cannot redirect the bootstrap or run under the wrong
			// framework.
			if verr := wiring.VerifySelection(root, wiringSel); verr != nil {
				errored++
				fmt.Fprintf(os.Stderr, "  %s %-16s error: %s  (%d/%d)\n",
					output.IconError, tcID, truncateReason("wiring identity mismatch: "+verr.Error(), 40), idx, total)
				if failFast {
					break
				}
				continue
			}
			wiringRec := wiringSel.Record

			// ENH-191: same-framework override -- use the pre-resolved
			// override adapter instead of the wired adapter.
			if overrideResolved != nil {
				resolved = overrideResolved
			} else {
				// No override -- resolve via the wiring record's adapter.
				var rErr error
				resolved, rErr = adapter.Resolve(cfg, "execute", wiringRec.Adapter)
				if rErr != nil {
					errored++
					fmt.Fprintf(os.Stderr, "  %s %-16s error: wiring adapter %q not configured  (%d/%d)\n",
						output.IconError, tcID, wiringRec.Adapter, idx, total)
					if failFast {
						break
					}
					continue
				}
			}

			framework = wiringRec.Framework

			// BUG-057: containment check on the wiring's artefact path.
			// Path-safety violations are an execution error (not a skip),
			// distinct from missing-artefact drift, and honor --fail-fast.
			absArtefact, safeArtefact, safeErr := pathsafe.ResolveUnderRoot(root, wiringRec.Artefact)
			if safeErr != nil {
				errored++
				fmt.Fprintf(os.Stderr, "  %s %-16s error: %s  (%d/%d)\n",
					output.IconError, tcID, truncateReason("unsafe artefact path: "+safeErr.Error(), 40), idx, total)
				if failFast {
					break
				}
				continue
			}
			resolvedArtefact = safeArtefact

			// BUG-200: artefact availability is checked regardless of
			// --allow-stale and BEFORE the pending-hash bootstrap, so a missing
			// or unreadable artefact is "not run" in every hash/flag state
			// (real or pending hash, with or without --allow-stale). Not run is
			// non-zero but does NOT stop --fail-fast. The adapter never runs.
			if state := classifyArtefactAvailability(absArtefact); state != artefactPresent {
				// ENH-222: plain-language reasons + inline block (Draft I2 / J2).
				if state == artefactUnreadable {
					notRun++
					fmt.Fprintf(os.Stderr, "  %s %-16s not run (script unreadable)  (%d/%d)\n",
						output.IconWarning, tcID, idx, total)
					printScriptUnreadableBulkBlock(resolvedArtefact)
				} else {
					notRun++
					fmt.Fprintf(os.Stderr, "  %s %-16s not run (script missing)  (%d/%d)\n",
						output.IconWarning, tcID, idx, total)
					printScriptMissingBulkBlock(tcID, framework, resolvedArtefact)
				}
				continue
			}

			// ENH-151: bootstrap pending -> real hash before drift check.
			// BUG-211: pass the verified selection so the write lands only on the
			// selected file; a concurrent change surfaces here as an error.
			if _, bErr := bootstrapPendingWiring(root, wiringSel, absArtefact); bErr != nil {
				errored++
				fmt.Fprintf(os.Stderr, "  %s %-16s error: %s  (%d/%d)\n",
					output.IconError, tcID, truncateReason("bootstrap wiring: "+bErr.Error(), 40), idx, total)
				if failFast {
					break
				}
				continue
			}

			// Drift preflight (bulk: refuse this TC rather than abort the run).
			//
			// BUG-200: a drift refusal is "not run" (non-zero exit, does NOT
			// stop --fail-fast), counted apart from a normal skip. ENH-231: a
			// script-ONLY change (artefact-hash drift, spec current) is accepted
			// automatically in relaxed mode; the acceptance write is deferred
			// until after the active-task gate below (Owner Decisions 2 and 3).
			// A missing/unreadable artefact is already handled by the
			// availability pre-check above; the branches here stay as a
			// defensive net against a race between the pre-check and the drift
			// hash, classified identically as not run.
			if !allowStale {
				runExecuteTestHook("pre-drift")
				dr := checkWiringDrift(root, wiringRec, resolvedArtefact, owner)
				if dr.diag != "" {
					// ENH-222 message precedence mirrors the single path.
					switch {
					case dr.missing:
						notRun++
						fmt.Fprintf(os.Stderr, "  %s %-16s not run (script missing)  (%d/%d)\n",
							output.IconWarning, tcID, idx, total)
						printScriptMissingBulkBlock(tcID, framework, resolvedArtefact)
						continue
					case dr.unreadable:
						notRun++
						fmt.Fprintf(os.Stderr, "  %s %-16s not run (script unreadable)  (%d/%d)\n",
							output.IconWarning, tcID, idx, total)
						printScriptUnreadableBulkBlock(resolvedArtefact)
						continue
					case dr.specStale:
						// Draft A per test case (inline, never grouped). Spec
						// drift leads over a simultaneous script change.
						notRun++
						bg.specDriftRefusals++
						fmt.Fprintf(os.Stderr, "  %s %-16s not run (spec changed)  (%d/%d)\n",
							output.IconWarning, tcID, idx, total)
						printSpecChangedSingle(tcID, framework, dr)
						continue
					case dr.unverifiedScriptChange():
						// Draft K in BOTH policies: the script changed but the
						// spec could not be checked. No refresh can repair it.
						notRun++
						fmt.Fprintf(os.Stderr, "  %s %-16s not run (spec unavailable)  (%d/%d)\n",
							output.IconWarning, tcID, idx, total)
						fmt.Fprintln(os.Stderr)
						printSpecUnavailableSingle(tcID, owner.SpecRel())
						continue
					case dr.scriptOnly() && cfg.OnScriptChange() == config.OnScriptChangeAccept:
						acceptScriptDrift = true
					default:
						// Script change in refuse mode: Draft C grouped refusal.
						notRun++
						fmt.Fprintf(os.Stderr, "  %s %-16s not run (script changed)  (%d/%d)\n",
							output.IconWarning, tcID, idx, total)
						bg.addRefusedScriptChange(tcID, framework, filepath.ToSlash(resolvedArtefact))
						continue
					}
				}
			}

			// ENH-231 Owner Decision 3: the active-task check applies to wired
			// automated execution on EVERY bulk run, including --force and the
			// script-drift acceptance run. --force (and acceptance) bypass only
			// the already-passing optimization, never the active-task guard.
			// Manual/agent frameworks keep today's bypass (activeExecuteTaskSkip
			// and shouldSkipExecute both no-op for them).
			if force || acceptScriptDrift || vetoAlreadyPassing {
				// --force, script-drift acceptance, and a BUG-209 refusal veto all
				// bypass the already-passing optimization while keeping the
				// active-task guard.
				if reason := activeExecuteTaskSkip(root, tcID, resolved); reason != "" {
					skipped++
					fmt.Fprintf(os.Stderr, "  %s %-16s skipped (%s)  (%d/%d)\n",
						skipIcon(reason), tcID, reason, idx, total)
					continue
				}
			} else {
				// Normal run: active-task AND already-passing skips both apply.
				// CON-023 reads "is it stale?" off wiring; we already passed that
				// gate above, so any prior pass is against the current content
				// and a re-run would be wasted work.
				if reason := shouldSkipExecute(root, tcID, resolved, framework, passCandidates[tcID]); reason != "" {
					skipped++
					fmt.Fprintf(os.Stderr, "  %s %-16s skipped (%s)  (%d/%d)\n",
						skipIcon(reason), tcID, reason, idx, total)
					continue
				}
			}

			// ENH-231: accept the script edit now that the active-task gate has
			// passed, immediately before invocation (Owner Decisions 2, 11).
			if acceptScriptDrift {
				acceptScriptRel = filepath.ToSlash(resolvedArtefact)
				acceptWiringRel = wiringRelPath(root, wiringSel.Path)
				runExecuteTestHook("pre-accept")
				acceptedHash, aerr := link.AcceptScriptDrift(root, wiringSel)
				if aerr != nil {
					errored++
					if errors.Is(aerr, link.ErrConcurrentWiringChange) {
						// ENH-222 Draft F: the saved automation file changed
						// concurrently; the newer version was kept.
						fmt.Fprintf(os.Stderr, "  %s %-16s error: saved automation file changed during execution  (%d/%d)\n",
							output.IconError, tcID, idx, total)
						printConcurrentChangeSingle(tcID, acceptWiringRel)
					} else {
						fmt.Fprintf(os.Stderr, "  %s %-16s error: %s  (%d/%d)\n",
							output.IconError, tcID, truncateReason("accept script change: "+aerr.Error(), 40), idx, total)
						// ENH-222 (2026-10-28): per-test-case accept-failure
						// recovery block, never grouped, using this test case's
						// validated framework. Counts/classification/exit code
						// are unchanged (still errored++).
						printAcceptFailureConfirmHint(os.Stderr, "", tcID, framework)
					}
					if failFast {
						break
					}
					continue
				}
				// REV-232 CODEX-002: carry the exact accepted hash; no second
				// hash of the script after acceptance.
				artefactHash = acceptedHash
				runExecuteTestHook("post-accept")
				// REV-232 DCODEX-002: same invocation-time hash check as the
				// single path; refuse this test case, do not re-accept.
				// ENH-222 Draft H: name the file already updated.
				if cerr := link.ConfirmAcceptedArtefact(absArtefact, acceptedHash); cerr != nil {
					errored++
					fmt.Fprintf(os.Stderr, "  %s %-16s error: script changed again after acceptance; retry  (%d/%d)\n",
						output.IconError, tcID, idx, total)
					printChangedAgainSingle(tcID, framework, acceptScriptRel, acceptWiringRel)
					if failFast {
						break
					}
					continue
				}
			} else {
				artefactHash, _ = pipeline.HashFile(absArtefact)
			}
		}

		// REV-158 CODEX-002: Owner is REQUIRED here. The loop resolves an owner
		// and uses it for the drift check, but the flags used to omit it, so
		// InvokeWithRoot exposed an empty TestCaseFile and OutputSubdir to execute
		// adapters and manual execution could not compute its current test-case
		// hash. Bulk and single-TC execution gave different, incorrect adapter
		// contracts for the same test case (ADR-024 s5).
		flags := adapter.CommandFlags{
			Owner:        owner,
			Adapter:      resolved.Name,
			ArtefactFile: resolvedArtefact,
			Environment:  environmentFlag,
			ExecutedBy:   executedBy,
			ArtefactHash: artefactHash,
			Framework:    framework,
		}

		// Progress indicator instead of spinner (TTY only -- \r doesn't work in pipes)
		if output.IsTTY(os.Stderr) {
			fmt.Fprintf(os.Stderr, "  %s %-16s running...  (%d/%d)\r",
				output.IconInProgress, tcID, idx, total)
		}

		result, invokeErr := adapter.InvokeWithRoot(ctx, root, cfg, resolved, tcID, flags)

		// ENH-222: read the structured start signal before classification, so an
		// accepted change is reported as "ran" (Draft D2) once dispatch is proven
		// -- even for a process that started then exited non-zero -- and a test
		// that could not start is reported separately (Draft E).
		started := adapter.Started(result, invokeErr)
		if acceptScriptDrift {
			reason := "the adapter could not be started"
			if invokeErr != nil {
				reason = invokeErr.Error()
			}
			bg.addAccepted(tcID, acceptScriptRel, acceptWiringRel, started, reason)
		}
		// ENH-222 Draft G: --allow-stale prints the "Ran ..." line only for a
		// test case that actually started.
		if allowStale && started {
			printAllowStaleRanLine(tcID)
		}

		if invokeErr != nil {
			errored++
			errMsg := invokeErr.Error()
			fmt.Fprintf(os.Stderr, "  %s %-16s error: %s  (%d/%d)\n",
				output.IconError, tcID, truncateReason(errMsg, 40), idx, total)
			// BUG-209 section 10 (CLAUDE-003): the per-case reason is clipped, so a
			// refusal found during the run gets its complete path and reason here.
			printBulkRefusal(invokeErr, reportedRefusals)
			// BUG-209 AC33: the accepted and allow-stale forms have already said it ran.
			if started && !acceptScriptDrift && !allowStale {
				fmt.Fprintf(os.Stderr, "    started, then failed\n")
			}
			// REV-265 CLAUDE-002 / CODEX-002: never clip the bound non-terminal
			// diagnostic. When the hard error carries it (all persistence writes
			// failed), print the full message -- the bound first line, remediation
			// and every persistence cause -- in an adjacent block rather than the
			// 40-char per-case reason.
			if strings.Contains(errMsg, "Execution result was not recorded") {
				for _, ln := range strings.Split(errMsg, "\n") {
					if trimmed := strings.TrimLeft(ln, " "); trimmed != "" {
						fmt.Fprintf(os.Stderr, "    %s\n", trimmed)
					}
				}
			}
			if failFast {
				break
			}
			continue
		}

		// ENH-130: classify on InvokeResult.Result (orthogonal contract).
		if result != nil && result.Status == "error" {
			errored++
			fmt.Fprintf(os.Stderr, "  %s %-16s error: %s  (%d/%d)\n",
				output.IconError, tcID, truncateReason(result.Summary, 40), idx, total)
			// ENH-188: the 40-char per-case line clips the bound diagnostic, so
			// print it in full in an adjacent block when the invoker set the
			// remediation hint (non-terminal Tier 2 execute). Open Question 1
			// leaves this layout to the PRP; assert only the bound phrase.
			if result.Remediation != "" {
				output.FprintError(os.Stderr, firstLine(result.Summary), result.Remediation)
			}
			// REV-263 CLAUDE-002 / CODEX-002: bulk never rendered result.Warnings,
			// hiding a failed handoff write. Surface the contract-write class here.
			renderBulkContractWarnings(result.Warnings)
			if failFast {
				break
			}
			continue
		}

		if result != nil && result.Result == "fail" {
			failed++
			fmt.Fprintf(os.Stderr, "  %s %-16s fail: %s  (%d/%d)\n",
				output.IconError, tcID, truncateReason(result.Summary, 40), idx, total)
			renderBulkContractWarnings(result.Warnings)
			if failFast {
				break
			}
			continue
		}

		if result != nil && result.Result == "error" {
			errored++
			fmt.Fprintf(os.Stderr, "  %s %-16s error: %s  (%d/%d)\n",
				output.IconError, tcID, truncateReason(result.Summary, 40), idx, total)
			renderBulkContractWarnings(result.Warnings)
			if failFast {
				break
			}
			continue
		}

		if result != nil && result.Result == "skip" {
			skipped++
			fmt.Fprintf(os.Stderr, "  %s %-16s skipped  (%d/%d)\n",
				skipIcon("test skipped"), tcID, idx, total)
			renderBulkContractWarnings(result.Warnings)
			continue
		}

		passed++
		fmt.Fprintf(os.Stderr, "  %s %-16s pass       (%d/%d)   \n",
			output.IconComplete, tcID, idx, total)
		if result != nil {
			renderBulkContractWarnings(result.Warnings)
		}
	}

	// ENH-222: grouped notices (Draft D2 acceptances, Draft C refusals with
	// safe hints, Draft E could-not-start) go after the per-test-case lines and
	// before the summary.
	bg.emit()

	// Print summary. BUG-200: five buckets in the fixed order
	// passed, skipped, not run, failed, errored. "not run" counts test cases
	// GTMS refused to run (apart from normal skips) and drives a non-zero exit.
	fmt.Printf("\n  %d passed, %d skipped, %d not run, %d failed, %d errored\n", passed, skipped, notRun, failed, errored)

	// BUG-078: result-tied key legend (per ADR-021, gtms execute is result-tied).
	fmt.Printf("\n  Key: %s = passed  %s = failed/error  %s = skipped  %s = warning\n",
		output.IconComplete, output.IconError, output.IconSkipped, output.IconWarning)

	// ENH-222 (2026-10-27 owner decision): a changed spec is confirmed one test
	// case at a time, so NO folder-level refresh is ever offered for spec
	// changes. Each spec-changed test case already printed its own narrow
	// `gtms link --refresh <tc> --framework <fw>` inline; at root scope this
	// run-level hint only points the user at gtms status.
	bg.specDriftRunHint()

	// ENH-222 Draft G: after --allow-stale, say once that change checks were
	// bypassed for this run only and no change was accepted for future runs.
	if allowStale {
		printAllowStaleFutureLine()
	}

	// Print guidance once after summary
	printCommandGuidance("execute", whatHappenedBulkExecute(folder, passed, skipped, notRun, failed, errored))

	// BUG-200: a refused test case (not run) makes the bulk command exit
	// non-zero, alongside failures and errors, so CI never reads a run that
	// executed nothing as success.
	if failed > 0 || errored > 0 || notRun > 0 {
		msg := fmt.Sprintf("%d failed, %d errored, %d not run out of %d test cases", failed, errored, notRun, total)
		return output.AsDisplayed(errors.New(msg))
	}

	return nil
}

// isMode3ExecuteAdapterName delegates to adapter.IsMode3ExecuteAdapterName.
// ENH-191: the authoritative list now lives in internal/adapter/mode3.go so
// both the CLI dispatch and the adapter layer (resolver exclusion, override
// validation) share one definition.
func isMode3ExecuteAdapterName(name string) bool {
	return adapter.IsMode3ExecuteAdapterName(name)
}

// BUG-200: bulk wiring selection moved to wiring.SelectForExecute, which
// distinguishes an ordinary miss (skip) from an ambiguous multi-framework
// refusal (not run) and from malformed-applicable wiring (error). The former
// selectWiringForBulk collapsed all of these into a single skip reason.

// shouldSkipExecute returns a skip reason when the bulk execute loop
// should not re-invoke the adapter for tcID under the given framework.
//
// CON-023 / ENH-145 / ENH-146: the wiring + drift check upstream already
// decided "this TC is current" -- so the only remaining reasons to skip
// are:
//   - the manual-execute adapter (never -- manual bulk re-evaluates every
//     TC because the user's template edit is the re-evaluation signal),
//   - the latest terminal result *for this framework* is already a clean
//     pass,
//   - another execute task for this TC is already in flight.
//
// ENH-134: the manual-adapter branch keys on the *resolved adapter*, not
// on any framework string. Wiring is read-only here.
//
// framework is the wiring-selected framework name; isAlreadyPassing uses
// it to ensure a pass under a different framework (e.g. Playwright)
// cannot suppress execution of a different framework's run on the same
// TC. Counting unit is `(testcase, framework)` per ENH-146.
func shouldSkipExecute(root, tcID string, resolved *adapter.ResolvedAdapter, framework string, source1 []executePassCandidate) string {
	if adapter.IsManualFramework(resolved) {
		return ""
	}

	// Active task in flight: skip the redundant invocation.
	if reason := activeExecuteTaskSkip(root, tcID, resolved); reason != "" {
		return reason
	}

	// Already-passing fast path, scoped to the selected framework so a
	// passing result under one framework cannot silently skip a run on
	// another framework wired to the same TC. Drift was already gated
	// above, so a stale pass would have surfaced as "stale wiring"
	// upstream.
	if isAlreadyPassing(root, tcID, framework, source1) {
		return "already passing"
	}

	return ""
}

// activeExecuteTaskSkip returns "active task exists" when a WIRED AUTOMATED
// execute task is already in flight for tcID, and "" otherwise.
//
// ENH-231 Owner Decision 3: this guard is the one skip that --force (and the
// script-drift acceptance run) must NOT bypass, so it is factored out of
// shouldSkipExecute to be callable on the --force path as well. Manual and
// agent frameworks are exempt -- they keep today's early-bypass behaviour and
// never gain an active-task check -- which is why the IsManualFramework guard
// lives here, shared by both callers.
func activeExecuteTaskSkip(root, tcID string, resolved *adapter.ResolvedAdapter) string {
	if adapter.IsManualFramework(resolved) {
		return ""
	}
	if existing, err := task.FindByTarget(root, "execute", tcID); err == nil && existing != nil {
		return "active task exists"
	}
	return ""
}

// isAlreadyPassing peeks at the latest terminal handoff under
// .gtms/results/ for (tcID, framework) and reports whether it was a
// clean pass. When framework is non-empty, results stamped with a
// different framework are ignored -- a Playwright pass cannot mark a
// BATS run as "already passing". When framework is empty (legacy
// callers), the filter is permissive (any framework).
//
// Best-effort: any I/O or parse failure returns false so the TC runs.
//
// REV-262 round 3: the `completed` stamp has one-second precision, so a pass and
// a later error that finish in the same second carry equal stamps. Selecting with
// `stamp > latest` alone then let whichever file os.ReadDir returned first win,
// and if the pass sorted first the newest run (the error) was masked and bulk
// skipped the test case as already passing -- a false green (reproduced on Linux
// by tc-fd93cc4b). The tie predates ENH-188; ENH-188's new error outcomes only
// made it observable. Selection now matches the dashboard reader
// (internal/reader/wiring_scan.go): newer stamp wins; on a stamp tie the newer
// handoff file mtime wins; on a full (stamp AND mtime) tie a non-pass outranks a
// pass -- fail closed, so the test case reruns. Worst case is one extra run,
// never a false green, and the result does not depend on directory read order.
// A coarse-mtime filesystem only widens the full-tie case, which the fail-closed
// rule already covers.
//
// REV-270 synthesis-004: stamps are compared as instants, not as text. Compared
// as text, a custom adapter's "2026-11-06T11:00:00+01:00" pass sorted after a
// rejection stamped "2026-11-06T10:30:00Z", although the rejection happened 30
// minutes later, and the next bulk run skipped the test case. A stamp that is
// present but is not valid RFC3339 cannot be placed in time, so one on any
// applicable candidate rules out a skip and the test case runs.
//
// REV-264 CODEX-002: selection also consumes the trusted per-test execution
// records under gtms/execution/. Those are written from GTMS's in-memory
// execution context (tf.Target + tf.Framework + outcome), independently of the
// adapter's handoff, so a rejected run leaves a non-pass signal even when BOTH
// handoff writes failed and the leftover handoff is identity-free (a read-only
// target on Windows). A newer non-pass execution record therefore outranks an
// older pass handoff, so a failed run can never be masked as already passing.
// printStartedThenFailed says a test ran and then failed (BUG-209 AC33).
func printStartedThenFailed(tcID string) {
	fmt.Fprintf(os.Stderr, "%s started, then failed.\n", tcID)
}

// printBulkRefusal prints the complete refusal or concurrency error an invocation failed
// with, in the block beside its clipped per-case line, once per path for the run. A
// concurrency error carries no remediation, because the file is healthy.
func printBulkRefusal(err error, reported map[string]bool) {
	var rh *result.RefusedHandoffError
	var rd *result.RefusedDirError
	var ce *result.ConcurrentUpdateError
	var first, remediation, key string
	switch {
	case errors.As(err, &rh):
		first, remediation, key = rh.FirstLine(), rh.Remediation(), rh.Rel
	case errors.As(err, &rd):
		first, remediation, key = rd.FirstLine(), rd.Remediation(), rd.Rel
	case errors.As(err, &ce):
		first, key = ce.Error(), "concurrent:"+ce.Rel
	default:
		return
	}
	if reported[key] {
		return
	}
	reported[key] = true
	fmt.Fprintf(os.Stderr, "    %s\n", first)
	if remediation != "" {
		fmt.Fprintf(os.Stderr, "    %s\n", remediation)
	}
}

// executePassCandidate is one execute handoff's pass-selection data, extracted
// from the SINGLE per-bulk-run results-directory scan (BUG-209 AC22 / "once per
// scan"): isAlreadyPassing no longer enumerates .gtms/results itself, so a bulk
// run does not re-scan every handoff per test case (the REV-270 O(TC x handoffs)
// trap the coordinator flagged).
type executePassCandidate struct {
	stamp     string
	mtime     time.Time
	isPass    bool
	framework string
}

// buildExecutePassCandidates groups the execute handoffs from one scan by target,
// keeping only the fields isAlreadyPassing's Source 1 needs. Non-execute contracts
// are excluded by the BUG-130 structural guard (result.IsExecuteContract).
func buildExecutePassCandidates(scan *result.Scan) map[string][]executePassCandidate {
	if scan == nil {
		return nil
	}
	out := make(map[string][]executePassCandidate)
	for _, e := range scan.Entries {
		rc := e.RC
		if rc == nil || !result.IsExecuteContract(rc) {
			continue
		}
		stamp := rc.Completed
		if stamp == "" {
			stamp = rc.Created
		}
		out[rc.Target] = append(out[rc.Target], executePassCandidate{
			stamp:     stamp,
			mtime:     time.Unix(0, e.MTime),
			isPass:    rc.Status == "complete" && rc.Result == "pass",
			framework: rc.Framework,
		})
	}
	return out
}

// isAlreadyPassing reports whether the latest applicable evidence for (tcID,
// framework) is a clean pass. Source 1 (local .gtms/results handoffs) is supplied
// pre-scanned in source1 so a bulk run scans the directory once; Source 2 (the
// committed gtms/execution records) is read here per test case, name-filtered, and
// stays VETO-ONLY.
func isAlreadyPassing(root, tcID, framework string, source1 []executePassCandidate) bool {
	var (
		haveBest     bool
		sawMalformed bool
		latestStamp  time.Time
		latestMtime  time.Time
		latestPass   bool
	)
	// consider folds one candidate (an execute handoff or an execution record)
	// into the running selection: the newer instant wins; on an equal instant the
	// newer file mtime wins; on a full tie a non-pass outranks a pass (fail closed,
	// and independent of directory read order). An empty stamp never ranks. A
	// malformed stamp never ranks either, and it also rules out a skip (see
	// sawMalformed at the return below).
	consider := func(rawStamp string, mtime time.Time, isPass bool) {
		if rawStamp == "" {
			return
		}
		stamp, pErr := time.Parse(time.RFC3339, rawStamp)
		if pErr != nil {
			sawMalformed = true
			return
		}
		if !haveBest {
			haveBest, latestStamp, latestMtime, latestPass = true, stamp, mtime, isPass
			return
		}
		switch {
		case stamp.After(latestStamp):
		case stamp.Equal(latestStamp) && mtime.After(latestMtime):
		case stamp.Equal(latestStamp) && mtime.Equal(latestMtime) && latestPass && !isPass:
		default:
			return
		}
		latestStamp, latestMtime, latestPass = stamp, mtime, isPass
	}

	// Source 1: execute handoffs under .gtms/results/, pre-scanned once per bulk
	// run (BUG-130 guard applied during the build via result.IsExecuteContract; an
	// unsafe leaf never reaches here because the scanner refused it). The framework
	// filter keeps a pass under one framework from skipping another's run.
	for _, c := range source1 {
		if framework != "" && c.framework != framework {
			continue
		}
		consider(c.stamp, c.mtime, c.isPass)
	}

	// Source 2: trusted execution records under gtms/execution/ (REV-264 CODEX-002),
	// VETO-ONLY (REV-265 CLAUDE-001). These records are committed/durable (they
	// survive a fresh clone, unlike the gitignored .gtms/results/ handoffs), so a
	// PASS record must never establish or strengthen a skip -- only a local handoff
	// (Source 1) can positively make a test "already passing". A newer applicable
	// NON-pass record may veto that skip, so a rejected run whose durable handoff
	// could not be written is still not masked.
	//
	// REV-265 CODEX-001: the directory and every entry are link-safe. A linked
	// execution directory (symlink, or a Windows junction/reparse point under
	// godebug winsymlink=0) fails OPEN -- isAlreadyPassing returns false, never a
	// skip -- so a redirected directory cannot suppress a run. Entries are Lstat'd
	// and only regular files are opened; symlinks, FIFOs and other special entries
	// are skipped, so execution.Read never follows a link or blocks on a device.
	//
	// REV-270 chunk-004-001: records only accumulate and this runs once per test
	// case in a bulk run, so a file named for another test case is ruled out from
	// its name, before it is stat'd or parsed. The decoded tc_id below stays the
	// authoritative check on the files that pass.
	execDir := layout.ExecutionDir(root)
	recordSuffix := execution.ResultsFileSuffix(tcID)
	if linked, exists := executionDirLinkedOrMissing(execDir); linked {
		return false
	} else if exists {
		if entries, err := os.ReadDir(execDir); err == nil {
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), recordSuffix) {
					continue
				}
				info, iErr := e.Info() // DirEntry.Info is Lstat-like: it does not follow a link
				if iErr != nil || !info.Mode().IsRegular() {
					continue
				}
				rf, rErr := execution.Read(filepath.Join(execDir, e.Name()))
				if rErr != nil || rf == nil || len(rf.Results) == 0 {
					continue
				}
				r0 := rf.Results[0]
				if r0.TCID != tcID {
					continue
				}
				if framework != "" && rf.Framework != framework {
					continue
				}
				if r0.Outcome == "pass" {
					continue // veto-only: a pass record never establishes/strengthens a skip
				}
				consider(rf.CompletedAt, info.ModTime(), false)
			}
		}
	}

	return haveBest && latestPass && !sawMalformed
}

// executionDirLinkedOrMissing classifies gtms/execution for the already-passing
// scan (REV-265 CODEX-001). It resolves the PARENT first so a symlinked project
// root -- common on macOS and CI temp dirs -- is not mistaken for a linked
// execution directory, then inspects only the leaf. linked is true when the leaf
// is a symlink, a Windows junction/reparse point, or otherwise does not resolve
// to itself (callers fail open); exists is false when the directory is simply
// absent (callers skip the trusted source and keep the local-handoff result).
func executionDirLinkedOrMissing(execDir string) (linked, exists bool) {
	resolvedParent, err := filepath.EvalSymlinks(filepath.Dir(execDir))
	if err != nil {
		return false, false
	}
	leaf := filepath.Join(resolvedParent, filepath.Base(execDir))
	info, err := os.Lstat(leaf)
	if err != nil {
		return false, false
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return true, true
	}
	resolved, err := filepath.EvalSymlinks(leaf)
	if err != nil || !sameFilesystemPath(resolved, leaf) {
		// A junction/reparse leaf (resolved elsewhere) or an unresolvable leaf.
		return true, true
	}
	return false, true
}

// sameFilesystemPath compares two cleaned paths, case-insensitively on Windows.
func sameFilesystemPath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// formatExecuteOutput prints the result of an execute command.
// ENH-120: headline surfaces outcome + TC ID, not internal task filename.
// ENH-130: reads both Status and Result for result-aware rendering.
func formatExecuteOutput(res *adapter.InvokeResult) {
	// ENH-130: adapter-execution errors -- the adapter itself broke.
	if res.Status == "error" {
		if res.Remediation != "" {
			// ENH-188: the summary's first line is the bound diagnostic; render
			// it in the CLAUDE.md error format with the remediation as the hint.
			// On the non-zero path the adapter's stderr follows the first line in
			// the summary, so surface those continuation lines under the same
			// 4-space indent. Branching on Remediation (an ENH-188-only signal
			// set in the invoker), never on tier/mode, keeps CLAUDE rule 2.
			output.Errorf(firstLine(res.Summary), res.Remediation)
			printIndentedDetail(os.Stderr, res.Summary)
		} else {
			output.Errorf(
				fmt.Sprintf("Task failed: %s", res.Filename),
				res.Summary,
			)
		}
		// REV-263 CLAUDE-002 / CODEX-002: surface res.Warnings on the error path
		// too (e.g. "contract update failed" / "contract restore write failed"),
		// which this early return previously hid. The bound diagnostic above is
		// unchanged; these are additional warning lines.
		renderExecuteWarnings(res.Warnings)
		printCommandGuidance("execute", whatHappenedExecute(res))
		return
	}

	// ENH-130: clean adapter run with failing test outcome.
	if res.Status == "complete" && res.Result == "fail" {
		output.Errorf(
			fmt.Sprintf("Test failed: %s", res.Target),
			res.Summary,
		)
		renderExecuteWarnings(res.Warnings)
		printCommandGuidance("execute", whatHappenedExecute(res))
		return
	}

	// ENH-130: clean adapter run with skipped test outcome.
	if res.Status == "complete" && res.Result == "skip" {
		fmt.Printf("  %s Test skipped: %s\n", output.IconSkipped, res.Target)
		fmt.Printf("    Adapter: %s (%s)\n", res.Adapter, res.Mode)
		if res.Summary != "" {
			fmt.Printf("    %s\n", res.Summary)
		}
		renderExecuteWarnings(res.Warnings)
		printCommandGuidance("execute", whatHappenedExecute(res))
		return
	}

	// ENH-130: clean adapter run with error test outcome (test broke, not adapter).
	if res.Status == "complete" && res.Result == "error" {
		fmt.Printf("  %s Test outcome unknown: %s\n", output.IconWarning, res.Target)
		fmt.Printf("    Adapter: %s (%s)\n", res.Adapter, res.Mode)
		if res.Summary != "" {
			fmt.Printf("    %s\n", res.Summary)
		}
		renderExecuteWarnings(res.Warnings)
		printCommandGuidance("execute", whatHappenedExecute(res))
		return
	}

	// ENH-130: derive outcome from Result field (orthogonal contract).
	outcome := res.Result
	if outcome == "" {
		outcome = "pass"
	}

	if len(res.Warnings) > 0 && res.ArtifactCount == 0 {
		fmt.Printf("  %s Recorded result for %s: %s (with warnings)\n", output.IconWarning, res.Target, outcome)
	} else {
		fmt.Printf("  %s Recorded result for %s: %s\n", output.IconComplete, res.Target, outcome)
	}

	fmt.Printf("    Adapter: %s (%s)\n", res.Adapter, res.Mode)

	// ENH-120: task ID and branch demoted to verbose-only output
	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "    Task: %s\n", res.TaskID)
		fmt.Fprintf(os.Stderr, "    Branch: %s\n", res.Branch)
	}

	if res.Mode == "async" {
		fmt.Println("    Check progress: gtms execute status")
	}

	renderExecuteWarnings(res.Warnings)

	printCommandGuidance("execute", whatHappenedExecute(res))
}

// renderExecuteWarnings prints each warning in the repo warning format. Shared by
// the formatExecuteOutput branches so the contract-write warnings REV-262/REV-263
// added ("contract update failed", "contract restore write failed", "contract
// reconstruct failed") are not hidden on the error/fail/skip paths that return
// early before the success-path loop.
func renderExecuteWarnings(warnings []string) {
	for _, w := range warnings {
		output.Warnf(w)
	}
}

// renderBulkContractWarnings prints only contract-write failure warnings for a
// bulk per-case result (REV-263 CLAUDE-002 / CODEX-002). Bulk output never
// rendered result.Warnings, so an operator could not tell that GTMS failed to
// persist the handoff. It is scoped to the contract-write class to avoid
// flooding the per-case bulk lines with every advisory warning.
func renderBulkContractWarnings(warnings []string) {
	for _, w := range warnings {
		if strings.Contains(w, "contract update failed") ||
			strings.Contains(w, "contract reconstruct failed") ||
			strings.Contains(w, "contract restore write failed") ||
			// REV-265 CODEX-003: the trusted execution record is the independently
			// attributable failure signal; bulk must report when it could not be
			// written, even if the handoff itself remained durable.
			strings.Contains(w, "Pipeline record could not be written") {
			output.Warnf(w)
		}
	}
}
