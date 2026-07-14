package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/adapter"
	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/git"
	"github.com/aitestmanagement/gtms-cli/internal/output"
	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/aitestmanagement/gtms-cli/internal/task"
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
  gtms execute tc-a1b2c3d4 --adapter bats-runner    -- confirm the wiring record's adapter explicitly
  gtms execute tc-a1b2c3d4 --env staging            -- run against staging environment

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

			// Validate: git repo exists
			if !git.IsRepo(ctx, root) {
				output.Errorf("Not a git repository.", "Initialize a git repo with 'git init'.")
				return output.AsDisplayed(fmt.Errorf("not a git repository"))
			}

			// Validate: folder structure exists
			if err := validateAutomateFolderStructure(root); err != nil {
				return err
			}

			// ENH-125: resolve executed_by once at command entry (flag → env → git user.name).
			executedBy := pipeline.ResolveExecutedBy(ctx, root, executedByFlag)

			// No argument = run all test cases from root (recursive implied)
			if len(args) == 0 {
				recursiveFlag = true
				return runBulkExecute(cmd, root, "", adapterFlag, environmentFlag, executedBy, frameworkFlag, forceFlag, failFastFlag, recursiveFlag, allowStaleFlag)
			}

			target := strings.ToLower(args[0])
			target = normaliseTarget(target)

			// Validate: target is safe (rejects shell metacharacters, path traversal)
			if err := validateTargetID(target); err != nil {
				output.Errorf(err.Error(),
					"Use only letters, numbers, dashes, underscores, dots, and forward slashes.")
				return output.AsDisplayed(err)
			}

			// Disambiguate: folder (bulk mode) vs single test case ID
			if IsBulkFolder(root, target) {
				return runBulkExecute(cmd, root, target, adapterFlag, environmentFlag, executedBy, frameworkFlag, forceFlag, failFastFlag, recursiveFlag, allowStaleFlag)
			}

			// --- Single TC mode (existing behavior) ---

			// Validate: target format (test case ID)
			if !isTestCaseID(target) {
				msg := fmt.Sprintf("Invalid test case ID format: '%s'.", target)
				output.Errorf(msg, "Use format like tc-a1b2c3d4.")
				return output.AsDisplayed(fmt.Errorf(msg))
			}

			// ENH-150: Post-fill validation gate -- catches frontmatter corruption
			// before the adapter is invoked.
			if violations := adapter.ValidateTestCasePostFill(root, target); len(violations) > 0 {
				summary := adapter.FormatValidationErrors(violations)
				output.Errorf(summary, "Fix the test case frontmatter and try again.")
				return output.AsDisplayed(fmt.Errorf(summary))
			}

			// Validate --framework flag if provided
			if frameworkFlag != "" && !config.ValidateFramework(frameworkFlag) {
				msg := fmt.Sprintf("Invalid framework '%s'. Framework must contain only lowercase letters, digits, and hyphens.", frameworkFlag)
				output.Errorf(msg, "Example: --framework playwright")
				return output.AsDisplayed(fmt.Errorf(msg))
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
				// Framework is sourced from the result template inside the
				// manual-execute pipeline; CommandFlags carries the user's
				// explicit flag value only.
				framework = frameworkFlag
			} else {
				// Wiring lookup. With --framework: exactly one wiring or fail.
				// Without --framework: exactly-one shortcut, otherwise
				// disambiguation error.
				var wiringRec *wiring.WiringRecord
				if frameworkFlag != "" {
					var wErr error
					wiringRec, _, wErr = wiring.Find(root, target, frameworkFlag)
					if wErr != nil {
						output.Errorf(fmt.Sprintf("Reading wiring for %s--%s: %v", target, frameworkFlag, wErr),
							"Check the wiring file for parse errors.")
						return output.AsDisplayed(wErr)
					}
					if wiringRec == nil {
						msg := fmt.Sprintf("No wiring record found for '%s' (framework: %s).", target, frameworkFlag)
						output.Errorf(msg, fmt.Sprintf("Run 'gtms automate %s --framework %s' (or 'gtms link') to wire it.", target, frameworkFlag))
						return output.AsDisplayed(fmt.Errorf(msg))
					}
				} else {
					recs, wErr := wiring.FindAllForTC(root, target)
					if wErr != nil {
						output.Errorf(fmt.Sprintf("Scanning wiring for %s: %v", target, wErr),
							"Check gtms/automation/wiring/ for parse errors.")
						return output.AsDisplayed(wErr)
					}
					if len(recs) == 0 {
						msg := fmt.Sprintf("No wiring records found for '%s'.", target)
						output.Errorf(msg, fmt.Sprintf("Run 'gtms automate %s' (or 'gtms link') to wire it.", target))
						return output.AsDisplayed(fmt.Errorf(msg))
					}
					if len(recs) > 1 {
						frameworks := make([]string, len(recs))
						for i, r := range recs {
							frameworks[i] = r.Framework
						}
						msg := fmt.Sprintf("'%s' has multiple wiring records (%s).", target, strings.Join(frameworks, ", "))
						output.Errorf(msg, "Re-run with --framework <name> to choose one.")
						return output.AsDisplayed(fmt.Errorf(msg))
					}
					wiringRec = recs[0]
				}

				framework = wiringRec.Framework

				// Conflict rule: explicit --adapter must agree with the
				// selected wiring record's adapter. Wiring is authoritative;
				// flags may select or confirm, never silently override.
				if adapterFlag != "" && adapterFlag != wiringRec.Adapter {
					msg := fmt.Sprintf(
						"--adapter %q conflicts with wiring %s--%s (adapter: %s).",
						adapterFlag, target, framework, wiringRec.Adapter)
					output.Errorf(msg, fmt.Sprintf("Drop --adapter or pass --adapter %s.", wiringRec.Adapter))
					return output.AsDisplayed(fmt.Errorf(msg))
				}

				// Resolve via the wiring record's adapter, not the project default.
				var rErr error
				resolved, rErr = adapter.Resolve(cfg, "execute", wiringRec.Adapter)
				if rErr != nil {
					output.Errorf(fmt.Sprintf("Wiring adapter %q not configured: %v", wiringRec.Adapter, rErr),
						"Restore the adapter in gtms.config or re-run automate after fixing it.")
					return output.AsDisplayed(rErr)
				}

				// BUG-057: containment check on the wiring's artefact path
				// before drift/hash/adapter invocation. A tampered or
				// hand-edited wiring file pointing outside projectRoot must
				// fail here, not silently run. Use the canonical absolute
				// path for hashing and the normalised relative form for
				// adapter inputs. Wiring is NOT rewritten on execute -- with
				// one exception: the pending → real hash bootstrap (ENH-151).
				absArtefact, safeArtefact, safeErr := pathsafe.ResolveUnderRoot(root, wiringRec.Artefact)
				if safeErr != nil {
					msg := fmt.Sprintf("Unsafe wiring artefact path for %s--%s: %v", target, framework, safeErr)
					output.Errorf(msg, fmt.Sprintf("Edit gtms/automation/wiring/%s--%s.wiring.yaml or re-run 'gtms link %s --framework %s --artefact <path inside project root> --force'.", target, framework, target, framework))
					return output.AsDisplayed(fmt.Errorf(msg))
				}
				resolvedArtefact = safeArtefact

				// ENH-151: bootstrap pending → real hash before drift check.
				// This runs unconditionally -- --allow-stale does not bypass it.
				if err := bootstrapPendingWiring(root, wiringRec, absArtefact); err != nil {
					msg := fmt.Sprintf("Cannot bootstrap wiring for %s--%s: %v", target, framework, err)
					output.Errorf(msg, "Ensure the artefact file exists and is readable, then re-run.")
					return output.AsDisplayed(fmt.Errorf(msg))
				}

				// Drift preflight: testcase-hash + artefact-hash currency.
				// No task is created, no result file is written, wiring is
				// unchanged.
				if !allowStaleFlag {
					if diag := checkWiringDrift(root, wiringRec, resolvedArtefact); diag != "" {
						// ENH-156: remediation hint leads with safest-first order.
						hint := fmt.Sprintf(
							"To refresh wiring (preserves artefact): gtms link --refresh %s\n"+
								"    To bypass for this run only: --allow-stale\n"+
								"    To regenerate the artefact (overwrites): gtms automate %s --framework %s --force",
							target, target, framework)
						output.Errorf(diag, hint)
						return output.AsDisplayed(fmt.Errorf(diag))
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
				return output.AsDisplayed(fmt.Errorf(msg))
			}

			// Invoke adapter with the wiring-resolved artefact / framework.
			flags := adapter.CommandFlags{
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

			if err != nil {
				return err
			}

			// Format output
			formatExecuteOutput(result)

			// ENH-130: status: error = adapter/infrastructure failure → non-zero exit.
			// status: complete (any result) = clean adapter run → exit 0.
			if result.Status == "error" {
				return output.AsDisplayed(fmt.Errorf("task failed: %s", result.Summary))
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&adapterFlag, "adapter", "", "Adapter to use (must agree with the wiring record's adapter)")
	cmd.Flags().StringVar(&environmentFlag, "env", "", "Target environment (e.g., staging, production)")
	cmd.Flags().StringVar(&executedByFlag, "executed-by", "", "Identity to record on the executed_by field (defaults to GTMS_EXECUTED_BY, then git user.name)")
	cmd.Flags().StringVar(&frameworkFlag, "framework", "", "Framework to select among a TC's wiring records")
	cmd.Flags().BoolVar(&forceFlag, "force", false, "Re-run already-passing test cases in bulk mode (wiring and drift skips still apply)")
	cmd.Flags().BoolVar(&failFastFlag, "fail-fast", false, "Stop on first failure or error in bulk mode")
	cmd.Flags().BoolVarP(&recursiveFlag, "recursive", "r", false, "Include test cases from subdirectories")
	cmd.Flags().BoolVar(&allowStaleFlag, "allow-stale", false, "Skip the wiring drift check for this run (does NOT update wiring)")

	// Add execute status subcommand
	cmd.AddCommand(newExecuteStatusCmd())

	return cmd
}

// bootstrapPendingWiring performs the one-way pending → <real hash>
// transition on the wiring record's artefact-hash field (ENH-151). If the
// wiring's artefact-hash is not PendingArtefactHash, this is a no-op.
//
// On success, wiringRec.ArtefactHash is updated in-place and the wiring file
// on disk is rewritten with the real hash. On failure (missing artefact,
// write-back error), the wiring file is NOT mutated and the error is returned
// so the caller can abort before invoking the adapter.
//
// execute may only transition pending → <real hash>. It never overwrites a
// non-pending hash.
func bootstrapPendingWiring(root string, wiringRec *wiring.WiringRecord, absArtefact string) error {
	if !wiring.IsPendingArtefactHash(wiringRec.ArtefactHash) {
		return nil
	}

	realHash, err := pipeline.HashFile(absArtefact)
	if err != nil {
		return fmt.Errorf("artefact %s is not readable: %w", wiringRec.Artefact, err)
	}

	// Write back the updated wiring with the real hash. All other fields
	// are preserved verbatim.
	updated := *wiringRec
	updated.ArtefactHash = realHash
	if _, err := wiring.Write(root, &updated); err != nil {
		return fmt.Errorf("writing updated wiring: %w", err)
	}

	// Update the in-memory record so the drift check below uses the real hash.
	wiringRec.ArtefactHash = realHash
	return nil
}

// checkWiringDrift recomputes testcase-hash and artefact-hash against
// current file content and returns a diagnostic naming the stale fields
// + a repair command. Empty string when no drift is detected.
//
// CON-023 / ENH-145: drift is a CLI preflight error -- no task is created,
// no result file is written, and the wiring file is not touched. Pass
// --allow-stale at the CLI to bypass this check for a single run.
func checkWiringDrift(root string, w *wiring.WiringRecord, resolvedArtefact string) string {
	var staleFields []string
	var expectedTC, currentTC, expectedArt, currentArt string

	specPath, specErr := pipeline.ResolveTestCaseSpec(root, w.TestCase)
	if specErr == nil {
		if h, hErr := pipeline.HashFile(filepath.Join(root, filepath.FromSlash(specPath))); hErr == nil {
			currentTC = h
			expectedTC = w.TestCaseHash
			if h != w.TestCaseHash {
				staleFields = append(staleFields, "testcase-hash")
			}
		}
	}

	artefactAbs := pipeline.AbsArtefactPath(root, resolvedArtefact)
	if _, statErr := os.Stat(artefactAbs); statErr != nil {
		return fmt.Sprintf("Missing artefact for %s--%s: %s does not resolve on disk.", w.TestCase, w.Framework, w.Artefact)
	}
	if h, hErr := pipeline.HashFile(artefactAbs); hErr == nil {
		currentArt = h
		expectedArt = w.ArtefactHash
		if h != w.ArtefactHash {
			staleFields = append(staleFields, "artefact-hash")
		}
	}

	if len(staleFields) == 0 {
		return ""
	}
	return fmt.Sprintf(
		"Stale wiring for %s--%s on %s. Expected testcase-hash=%s, current=%s. Expected artefact-hash=%s, current=%s.",
		w.TestCase, w.Framework, strings.Join(staleFields, ", "),
		expectedTC, currentTC, expectedArt, currentArt,
	)
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

	tcIDs, err := DiscoverTestCases(root, folder, recursive)
	if err != nil {
		output.Errorf(err.Error(), "Check that gtms/test/cases/"+folder+"/ contains tc-*.md files.")
		return output.AsDisplayed(err)
	}

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
		if IsVerbose() {
			fmt.Fprintf(os.Stderr, "Resolved manual adapter: %s (tier %d, mode %s)\n",
				manualResolved.Name, manualResolved.Tier, manualResolved.Mode)
		}
	}

	total := len(tcIDs)
	recursiveLabel := ""
	if recursive {
		recursiveLabel = " (recursive)"
	}
	scope := "gtms/test/cases/"
	if folder != "" {
		scope = fmt.Sprintf("gtms/test/cases/%s/", folder)
	}
	fmt.Printf("Processing %d test cases in %s%s...\n", total, scope, recursiveLabel)

	passed := 0
	skipped := 0
	staleSkipped := 0 // ENH-156: stale-wiring skips tracked for summary hint
	failed := 0       // assertion failures (ENH-130: result.Result == "fail")
	errored := 0      // infrastructure errors (result.Status == "error" or result.Result == "error")

	for i, tcID := range tcIDs {
		idx := i + 1

		var resolved *adapter.ResolvedAdapter
		var framework string
		var resolvedArtefact string
		var artefactHash string

		if isManualPath {
			resolved = manualResolved
			framework = frameworkFlag
		} else {
			// Wiring selection for this TC.
			wiringRec, skipReason := selectWiringForBulk(root, tcID, frameworkFlag)
			if skipReason != "" {
				skipped++
				fmt.Fprintf(os.Stderr, "  %s %-16s skipped (%s)  (%d/%d)\n",
					skipIcon(skipReason), tcID, skipReason, idx, total)
				continue
			}

			// Explicit --adapter must agree with wiring's adapter.
			if adapterFlag != "" && adapterFlag != wiringRec.Adapter {
				skipped++
				reason := fmt.Sprintf("--adapter conflicts with wiring (wants %s)", wiringRec.Adapter)
				fmt.Fprintf(os.Stderr, "  %s %-16s skipped (%s)  (%d/%d)\n",
					skipIcon(reason), tcID, reason, idx, total)
				continue
			}

			// Resolve via the wiring record's adapter, not the project default.
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

			// ENH-151: bootstrap pending → real hash before drift check.
			if bErr := bootstrapPendingWiring(root, wiringRec, absArtefact); bErr != nil {
				errored++
				fmt.Fprintf(os.Stderr, "  %s %-16s error: %s  (%d/%d)\n",
					output.IconError, tcID, truncateReason("bootstrap wiring: "+bErr.Error(), 40), idx, total)
				if failFast {
					break
				}
				continue
			}

			// Drift preflight (bulk: skip rather than abort).
			if !allowStale {
				if diag := checkWiringDrift(root, wiringRec, resolvedArtefact); diag != "" {
					reason := "stale wiring"
					if strings.Contains(diag, "Missing artefact") {
						reason = "missing artefact"
					}
					skipped++
					if reason == "stale wiring" {
						staleSkipped++ // ENH-156: track for summary hint
					}
					fmt.Fprintf(os.Stderr, "  %s %-16s skipped (%s)  (%d/%d)\n",
						skipIcon(reason), tcID, reason, idx, total)
					continue
				}
			}

			// Skip already-passing TCs unless --force. CON-023 reads "is
			// it stale?" off wiring; we already passed that gate above --
			// so any prior pass is by definition against the current
			// content and a re-run would be wasted work.
			if !force {
				if reason := shouldSkipExecute(root, tcID, resolved, framework); reason != "" {
					skipped++
					fmt.Fprintf(os.Stderr, "  %s %-16s skipped (%s)  (%d/%d)\n",
						skipIcon(reason), tcID, reason, idx, total)
					continue
				}
			}

			artefactHash, _ = pipeline.HashFile(absArtefact)
		}

		flags := adapter.CommandFlags{
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

		if invokeErr != nil {
			errored++
			errMsg := invokeErr.Error()
			fmt.Fprintf(os.Stderr, "  %s %-16s error: %s  (%d/%d)\n",
				output.IconError, tcID, truncateReason(errMsg, 40), idx, total)
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
			if failFast {
				break
			}
			continue
		}

		if result != nil && result.Result == "fail" {
			failed++
			fmt.Fprintf(os.Stderr, "  %s %-16s fail: %s  (%d/%d)\n",
				output.IconError, tcID, truncateReason(result.Summary, 40), idx, total)
			if failFast {
				break
			}
			continue
		}

		if result != nil && result.Result == "error" {
			errored++
			fmt.Fprintf(os.Stderr, "  %s %-16s error: %s  (%d/%d)\n",
				output.IconError, tcID, truncateReason(result.Summary, 40), idx, total)
			if failFast {
				break
			}
			continue
		}

		if result != nil && result.Result == "skip" {
			skipped++
			fmt.Fprintf(os.Stderr, "  %s %-16s skipped  (%d/%d)\n",
				skipIcon("test skipped"), tcID, idx, total)
			continue
		}

		passed++
		fmt.Fprintf(os.Stderr, "  %s %-16s pass       (%d/%d)   \n",
			output.IconComplete, tcID, idx, total)
	}

	// Print summary
	fmt.Printf("\n  %d passed, %d skipped, %d failed, %d errored\n", passed, skipped, failed, errored)

	// BUG-078: result-tied key legend (per ADR-021, gtms execute is result-tied).
	fmt.Printf("\n  Key: %s = passed  %s = failed/error  %s = skipped  %s = warning\n",
		output.IconComplete, output.IconError, output.IconSkipped, output.IconWarning)

	// ENH-156: emit one summary hint when TCs were skipped for stale wiring.
	// Folder scope points directly at `gtms link --refresh <folder>`. Root
	// scope redirects to `gtms status -r` to identify the affected folders
	// first, because `gtms link --refresh` requires a positional folder
	// target and there is no single folder to point at.
	if staleSkipped > 0 {
		if folder != "" {
			fmt.Fprintf(os.Stderr, "\n  Hint: to refresh stale wiring, run: gtms link --refresh %s\n", folder)
		} else {
			fmt.Fprintf(os.Stderr, "\n  Hint: stale wiring found in root-scope bulk execute. Run 'gtms status -r' to identify folders, then 'gtms link --refresh <folder>'.\n")
		}
	}

	// Print guidance once after summary
	printCommandGuidance("execute", whatHappenedBulkExecute(folder, passed, skipped, failed, errored))

	if failed > 0 || errored > 0 {
		msg := fmt.Sprintf("%d failed, %d errored out of %d test cases", failed, errored, total)
		return output.AsDisplayed(fmt.Errorf(msg))
	}

	return nil
}

// isMode3ExecuteAdapterName reports whether the given execute adapter
// name is one of the Mode 3 execute adapters that read a filled result
// template instead of an automation artefact. CON-023 wiring-
// authoritative execute requires every other adapter to go through
// wiring lookup; Mode 3 execute adapters legitimately have no wiring
// record and must bypass that gate before any wiring resolution.
//
// The closed set covers both Tier 0 built-ins (manual-execute,
// agent-execute) and their Tier 2 script variants introduced by
// ENH-160 (manual-execute-script, agent-execute-script).
//
// This predicate is also used to check cfg.Defaults["execute"] so
// that a no-flag `gtms execute tc-X` on a manual-preset project
// bypasses wiring when the configured default is a Mode 3 name
// (ENH-163).
//
// This is intentionally a name-based predicate, distinct from
// adapter.IsManualFramework. The wiring-bypass decision happens before
// the adapter is resolved, so the CLI cannot ask the resolved adapter
// what it is. If a future enhancement adds another Mode 3 execute
// adapter, update this list alongside the adapter registration --
// otherwise the new adapter will fall through to wiring lookup and
// fail with "No wiring records found".
func isMode3ExecuteAdapterName(name string) bool {
	return name == "manual-execute" ||
		name == "agent-execute" ||
		name == "manual-execute-script" ||
		name == "agent-execute-script"
}

// selectWiringForBulk picks a wiring record for one TC in the bulk path.
// Returns the chosen record on success, or an empty record and a
// non-empty skip reason on any miss (no wiring, ambiguous frameworks,
// scan error). Reasons are short labels suitable for the bulk-loop
// "skipped (...)" rendering. CON-023 / ENH-145.
func selectWiringForBulk(root, tcID, frameworkFlag string) (*wiring.WiringRecord, string) {
	if frameworkFlag != "" {
		rec, _, err := wiring.Find(root, tcID, frameworkFlag)
		if err != nil {
			return nil, "wiring parse error"
		}
		if rec == nil {
			return nil, "no " + frameworkFlag + " wiring"
		}
		return rec, ""
	}
	recs, err := wiring.FindAllForTC(root, tcID)
	if err != nil {
		return nil, "wiring scan error"
	}
	switch len(recs) {
	case 0:
		return nil, "not wired"
	case 1:
		return recs[0], ""
	default:
		return nil, "multiple frameworks -- specify --framework"
	}
}

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
func shouldSkipExecute(root, tcID string, resolved *adapter.ResolvedAdapter, framework string) string {
	if adapter.IsManualFramework(resolved) {
		return ""
	}

	// Active task in flight: skip the redundant invocation.
	if existing, err := task.FindByTarget(root, "execute", tcID); err == nil && existing != nil {
		return "active task exists"
	}

	// Already-passing fast path, scoped to the selected framework so a
	// passing result under one framework cannot silently skip a run on
	// another framework wired to the same TC. Drift was already gated
	// above, so a stale pass would have surfaced as "stale wiring"
	// upstream.
	if isAlreadyPassing(root, tcID, framework) {
		return "already passing"
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
func isAlreadyPassing(root, tcID, framework string) bool {
	resultsDir := filepath.Join(root, ".gtms", "results")
	entries, err := os.ReadDir(resultsDir)
	if err != nil {
		return false
	}
	var latestStamp, latestStatus, latestResult string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".handoff.yaml") {
			continue
		}
		rc, rErr := result.Read(filepath.Join(resultsDir, e.Name()))
		if rErr != nil || rc == nil {
			continue
		}
		if rc.Target != tcID {
			continue
		}
		// BUG-130 structural guard: only terminal execute contracts
		// count as prior execute passes. Non-execute commands
		// (automate/create/prime) also write terminal handoffs but
		// must never satisfy this check (BUG-124, BUG-129).
		if !result.IsTerminalExecuteContract(rc) {
			continue
		}
		if framework != "" && rc.Framework != framework {
			continue
		}
		stamp := rc.Completed
		if stamp == "" {
			stamp = rc.Created
		}
		if stamp > latestStamp {
			latestStamp = stamp
			latestStatus = rc.Status
			latestResult = rc.Result
		}
	}
	return latestStatus == "complete" && latestResult == "pass"
}

// formatExecuteOutput prints the result of an execute command.
// ENH-120: headline surfaces outcome + TC ID, not internal task filename.
// ENH-130: reads both Status and Result for result-aware rendering.
func formatExecuteOutput(res *adapter.InvokeResult) {
	// ENH-130: adapter-execution errors -- the adapter itself broke.
	if res.Status == "error" {
		output.Errorf(
			fmt.Sprintf("Task failed: %s", res.Filename),
			res.Summary,
		)
		printCommandGuidance("execute", whatHappenedExecute(res))
		return
	}

	// ENH-130: clean adapter run with failing test outcome.
	if res.Status == "complete" && res.Result == "fail" {
		output.Errorf(
			fmt.Sprintf("Test failed: %s", res.Target),
			res.Summary,
		)
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

	for _, w := range res.Warnings {
		output.Warnf(w)
	}

	printCommandGuidance("execute", whatHappenedExecute(res))
}
