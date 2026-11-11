package adapter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	gitpkg "github.com/aitestmanagement/gtms-cli/internal/git"
	"github.com/aitestmanagement/gtms-cli/internal/id"
	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
	"github.com/aitestmanagement/gtms-cli/internal/prompt"
	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/aitestmanagement/gtms-cli/internal/task"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
)

// defaultSyncTimeout is the timeout applied to sync adapter invocations when no
// per-adapter timeout: is configured. Exported as a package-level var so tests
// can override it to avoid waiting 30 minutes (follows the lookPath pattern).
var defaultSyncTimeout = 30 * time.Minute

// defaultSyncTimeoutEnvVar names the runtime override for the default sync
// timeout. Acceptance tests (BATS) set this so they don't have to wait for
// the production 30-minute default to fire (BUG-131 spec testability note).
// Invalid or empty values are ignored and the package default is used.
const defaultSyncTimeoutEnvVar = "GTMS_DEFAULT_EXECUTE_TIMEOUT"

// effectiveDefaultSyncTimeout returns the runtime default sync timeout,
// honouring the GTMS_DEFAULT_EXECUTE_TIMEOUT env-var override when present
// and parseable. Falls back to defaultSyncTimeout on any error or absence.
func effectiveDefaultSyncTimeout() time.Duration {
	if v := strings.TrimSpace(os.Getenv(defaultSyncTimeoutEnvVar)); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return defaultSyncTimeout
}

// invocationError preserves a human-readable summary (shown to the user and
// stored on the task/result) while keeping the underlying error's unwrap chain
// intact. Without it, invoker.go's error return would flatten the summary into
// a fresh error (fmt.Errorf("%s", summary)), severing the chain so a typed
// error such as *MissingExecuteAdapterError could never be recovered via
// errors.As at the CLI boundary (BUG-171).
type invocationError struct {
	summary string
	err     error
	// started reports whether the adapter process was launched before this
	// failure (ENH-222). It lets the CLI tell a post-launch failure (timeout,
	// cancellation, stream/wait error -- "started, then failed") from a
	// pre-launch failure ("could not start") without parsing the summary.
	started bool
}

func (e *invocationError) Error() string { return e.summary }
func (e *invocationError) Unwrap() error { return e.err }

// Started reports whether the adapter process (or asynchronous dispatch) was
// launched for this invocation (ENH-222 Owner Decision 2026-10-27 #4). It is
// readable even when InvokeWithRoot returned an error, so the CLI can tell
// "accepted and ran" (Draft D) from "accepted but could not start" (Draft E)
// from a structured signal, never from error text. It inspects the result's
// Started field first, then the error chain for an *invocationError.
func Started(res *InvokeResult, err error) bool {
	if res != nil && res.Started {
		return true
	}
	var ie *invocationError
	if errors.As(err, &ie) {
		return ie.started
	}
	return false
}

// InvokeResult holds the outcome of an Invoke call, used by CLI for output formatting.
type InvokeResult struct {
	TaskID        string
	Adapter       string
	Mode          string
	Branch        string
	Status        string // pending, in-progress, complete, error (ENH-130: no longer carries test outcome)
	Result        string // pass, fail, skip, error (ENH-130: orthogonal test outcome)
	Summary       string
	Filename      string   // task filename
	ArtifactCount int      // number of files produced by adapter
	ArtifactPaths []string // relative paths of produced files
	Warnings      []string // non-fatal issues to surface to user
	Target        string   // the target argument (folder for create, tc-id for automate/execute)
	// ENH-191: the execute adapter stamped into the wiring record during a
	// sync automate. The CLI emits the verbose "Execute adapter for wiring:
	// <name>" report from this; empty for non-automate commands or when no
	// wiring was written (e.g. failed/dry-run automate).
	WiringAdapter string
	// Started reports whether the adapter process/dispatch was launched (ENH-222).
	// Threaded from InvocationResult.Started. Read via adapter.Started, which also
	// covers the error-return paths where this result is nil.
	Started bool
	// Remediation is the bound ENH-188 second line ("A Tier 2 execute adapter
	// must write a terminal status ...") for the non-terminal-execute error. It
	// is the CLI-only hint rendered under the first diagnostic line; it is NOT
	// written to the handoff. Empty on every other path, and the CLI branches on
	// it rather than on tier/mode (CLAUDE rule 2).
	Remediation string
}

// Invoke is the unified invocation orchestrator (Phase 4: Handoff).
// It performs the full sequence: generate ID, create task file, create result contract,
// build context, invoke adapter, handle result.
func Invoke(ctx context.Context, cfg *config.Config, resolved *ResolvedAdapter, target string, flags CommandFlags) (*InvokeResult, error) {
	projectRoot, err := findProjectRoot()
	if err != nil {
		return nil, err
	}

	return InvokeWithRoot(ctx, projectRoot, cfg, resolved, target, flags)
}

// CarriedOwnerError is the invocation-boundary refusal required by ADR-024 s5:
// the owner an action command carries must be present, must be the owner OF THE
// TARGET, and must still be the identity the spec on disk declares.
//
// REV-160 CODEX-002. Each of automate, execute and prime resolves ownership in
// its own Cobra wrapper, and each writes the same three-step guard. That leaves
// the LAST boundary before external code runs -- InvokeWithRoot, which generates
// the task id, writes the task file and result contract, and then invokes the
// adapter -- consuming flags.Owner without checking any of it. A caller that
// resolved the owner of tc-b and invoked target tc-a got task, result and wiring
// records attributed to tc-a while the adapter read tc-b's spec; a nil owner got
// an empty source path and a silently disabled drift gate. The bulk loops reach
// this boundary without the single-target validation step at all.
//
// Detected with errors.As. It is deliberately a hard refusal rather than a
// warning: past this point the command has already created state.
type CarriedOwnerError struct {
	// Command is the action being invoked (automate, execute, prime).
	Command string
	// Target is the id the invocation is attributed to.
	Target string
	// OwnerID is the id the carried owner actually proves; empty when no owner
	// was carried at all.
	OwnerID string
	// Reason explains which of the three conditions failed.
	Reason string
}

// Error renders with every id passed through the central identity sanitiser
// (ADR-024 s7) -- Target comes from the command line and OwnerID from a spec on
// disk, so neither is trusted terminal input.
func (e *CarriedOwnerError) Error() string {
	return fmt.Sprintf("refusing to invoke %s for '%s': %s",
		e.Command, testcase.SanitizeIdentityString(e.Target), e.Reason)
}

// requireCarriedOwner is the boundary check itself.
//
// create is exempt BY DESIGN, not by omission: it is folder-scoped because no
// test case exists yet (ADR-024 s1), so there is no owner for it to carry. Every
// other command reaching this boundary is one of the three action commands whose
// consumer-matrix row requires a uniquely proven owner before invocation.
//
// The third condition -- that the exact spec still carries the proved identity,
// schema state, and category -- reuses ValidateTestCaseOwner rather than reading
// the file again here. One implementation is the point (ADR-024 s5); a second
// copy is how two checks come to disagree. Note that it does NOT re-resolve the
// bare id: it reads the spec the carried owner already points at and carries the
// fresh normalized metadata and hash forward.
func requireCarriedOwner(projectRoot string, resolved *ResolvedAdapter, target string, flags CommandFlags) (ValidatedTestCaseOwner, error) {
	var validated ValidatedTestCaseOwner
	switch resolved.Command {
	case "automate", "execute", "prime":
	default:
		return validated, nil
	}

	owner := flags.Owner
	if owner == nil {
		return validated, &CarriedOwnerError{
			Command: resolved.Command,
			Target:  target,
			Reason:  "no resolved owner reached the invocation boundary, so nothing proves the test case exists or which spec it is",
		}
	}
	if owner.ID() != target {
		return validated, &CarriedOwnerError{
			Command: resolved.Command,
			Target:  target,
			OwnerID: owner.ID(),
			Reason: fmt.Sprintf("the carried owner proves '%s', a different test case -- the invocation would be attributed to '%s' while using another test case's spec",
				testcase.SanitizeIdentityString(owner.ID()), testcase.SanitizeIdentityString(target)),
		}
	}
	validated, violations := ValidateTestCaseOwnerFresh(projectRoot, owner)
	if len(violations) > 0 {
		return validated, &CarriedOwnerError{
			Command: resolved.Command,
			Target:  target,
			OwnerID: owner.ID(),
			Reason:  "the owning spec no longer declares that identity or schema proof -- " + FormatValidationErrors(violations),
		}
	}
	return validated, nil
}

// InvokeWithRoot is like Invoke but accepts an explicit project root (useful for testing).
func InvokeWithRoot(ctx context.Context, projectRoot string, cfg *config.Config, resolved *ResolvedAdapter, target string, flags CommandFlags) (invokeRes *InvokeResult, invokeErr error) {
	// ADR-024 s5 / REV-160 CODEX-002: the LAST boundary before any state is
	// created or any external code runs. It refuses a nil, mismatched, or stale
	// owner BEFORE the task id is generated, so a refusal leaves no task file, no
	// result contract, no wiring and no adapter process behind.
	validatedOwner, err := requireCarriedOwner(projectRoot, resolved, target, flags)
	if err != nil {
		return nil, err
	}

	// The second half of the same boundary: for every automate invocation the
	// wiring index is proved COMPLETE, and for the Tier 0 path the artefact this
	// run will write is planned HERE, from the owner just proved, refused if any
	// other identity holds it, and then RESERVED for the rest of the operation.
	// Planning before the boundary is the point -- both mutating paths below (the
	// --force sweep and the adapter's own write) happen after it, so no file is
	// removed and no artefact is overwritten on behalf of a run that was never
	// allowed to proceed.
	//
	// flags.Claims carries the operation's claim set when a bulk caller built one
	// (scanned once, before the batch mutated anything). When it is nil this
	// builds a set for this single invocation -- the same abstraction at a
	// smaller scope, not a second code path.
	claims, reservation, err := enforceArtefactOwnership(projectRoot, resolved, flags)
	if err != nil {
		return nil, err
	}
	flags.Claims = claims

	// The AUTHORISED value itself, read off the reservation the boundary just
	// took, and carried from here into the adapter context. It is deliberately
	// not re-planned downstream: the guarantee the carrier exists to give is that
	// the path the adapter writes IS the path that was collision-checked and
	// reserved, not a second computation that agrees with it. Empty when nothing
	// was reserved (not automate, not Tier 0, no planned artefact), which is the
	// carrier BuiltinAutomate refuses on.
	plannedArtefact := reservation.PlannedPath()

	// The reservation is resolved from ONE defer so that no return path can skip
	// it. Success and failure are read off exactly the two conditions the bulk
	// CLI already uses to count an item as failed -- a non-nil error, or a result
	// whose Status is "error" -- so this introduces no partial-failure policy of
	// its own; it mirrors the command's.
	//
	// Settle then overrides that verdict when DURABLE STATE disagrees with it:
	// steps run after the adapter has already written its wiring record, so an
	// invocation can fail with the project's ownership already changed on disk.
	// See ArtefactReservation.Settle.
	if reservation != nil {
		defer func() {
			succeeded := invokeErr == nil && invokeRes != nil && invokeRes.Status != "error"
			reservation.Settle(succeeded)
		}()
	}

	// Files the --force sweep DECLINED to remove are reported to the operator.
	// A silent non-deletion is indistinguishable from a sweep that ran and found
	// nothing, so a stale file that survives on purpose looks identical to a bug.
	// Collected here and attached from a defer so every return path carries them.
	var sweepWarnings []string
	defer func() {
		if invokeRes != nil && len(sweepWarnings) > 0 {
			invokeRes.Warnings = append(invokeRes.Warnings, sweepWarnings...)
		}
	}()

	// Apply timeout: per-adapter config overrides the default; sync adapters
	// always get a deadline (BUG-131). Async adapters return immediately so
	// no default is needed.
	if resolved.Config.Timeout != "" {
		d, err := time.ParseDuration(resolved.Config.Timeout)
		if err != nil {
			return nil, fmt.Errorf("adapter '%s' has invalid timeout '%s': %w", resolved.Name, resolved.Config.Timeout, err)
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d)
		defer cancel()
	} else if resolved.Mode == "sync" {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, effectiveDefaultSyncTimeout())
		defer cancel()
	}

	// 1. Generate task ID
	taskID := "task-" + id.New()
	var testCaseContent string
	var testCaseReadErr error

	// 2. Build branch name: real branch for sync adapters, constructed for async (BUG-056)
	branch := taskBranch(ctx, projectRoot, resolved, target)

	// 3. Create task file in gtms/tasks/pending/
	tf := &task.TaskFile{
		ID:          taskID,
		Type:        resolved.Command,
		Target:      target,
		Adapter:     resolved.Name,
		Status:      "pending",
		Created:     time.Now().UTC().Format(time.RFC3339),
		Branch:      branch,
		Reference:   flags.Reference,
		Environment: flags.Environment,
		ExecutedBy:  flags.ExecutedBy,
	}

	// Set framework for automate, execute, and prime commands using the precedence chain:
	// CLI flag -> config field -> adapter name
	if resolved.Command == "automate" || resolved.Command == "execute" || resolved.Command == "prime" {
		tf.Framework = ResolveFramework(resolved, flags.Framework)
	}

	// Set automate-specific task file fields
	if resolved.Command == "automate" {
		// Find the source test case file
		source := ownerSource(flags.Owner)
		if source != "" {
			tf.Reference = source
			// Read test case content for prompt injection (BUG-013)
			absPath := filepath.Join(projectRoot, source)
			if data, readErr := os.ReadFile(absPath); readErr == nil {
				testCaseContent = string(data)
			} else {
				testCaseReadErr = readErr
			}
		}
	}

	if _, createErr := task.Create(projectRoot, tf, ""); createErr != nil {
		return nil, fmt.Errorf("creating task file: %w", createErr)
	}

	// 4. Ensure .gtms/ directories exist.
	//
	// BUG-209 (tc-c5b20358 Step 4 / AC7): verify .gtms/results containment BEFORE
	// creating any .gtms subdirectory, so an escaping .gtms or .gtms/results link
	// never has results/worktrees/logs/tmp created inside its target. A refusal
	// here stops the adapter before it starts and routes the task to error/ so no
	// pending task is stranded to block the next run (AC18).
	if containErr := result.VerifyResultsDirContainment(projectRoot); containErr != nil {
		return nil, failBeforeInvocation(projectRoot, tf, containErr)
	}
	for _, subdir := range []string{"results", "worktrees", "logs", "tmp"} {
		dir := filepath.Join(projectRoot, ".gtms", subdir)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, failBeforeInvocation(projectRoot, tf, fmt.Errorf("creating .gtms/%s directory: %w", subdir, err))
		}
	}
	// BUG-209 section 4: open and verify what was created before anything uses it. A
	// swap during creation is refused here. The instant between the check above and
	// the creation stays open and is accepted for v0.4.1.
	if containErr := result.VerifyCreatedResultsDir(projectRoot); containErr != nil {
		return nil, failBeforeInvocation(projectRoot, tf, containErr)
	}

	// 5. Create result contract.
	//
	// CON-023 / ENH-146: stamp Framework, Git context, executed_by, and
	// environment on the contract at creation time so the reader overlay
	// (ENH-146 join precedence) has the data it needs without reading any
	// legacy automation record. The manual-execute path will overwrite
	// rc.Framework post-template-parse via result.Update (see below).
	rc := &result.ResultContract{
		Task:         taskID,
		Command:      resolved.Command,
		Target:       target,
		Adapter:      resolved.Name,
		Mode:         resolved.Mode,
		Created:      tf.Created,
		Status:       "pending",
		ArtefactHash: flags.ArtefactHash,
		Framework:    tf.Framework,
		ExecutedBy:   tf.ExecutedBy,
		Environment:  tf.Environment,
	}
	stampGitContext(ctx, projectRoot, rc)

	resultPath, err := result.Create(projectRoot, rc)
	if err != nil {
		// BUG-209 AC18: any pre-invocation handoff write failure (an unsafe-path
		// refusal, a Create collision, or any other) stops the adapter and must not
		// strand a pending task that blocks the next run as a duplicate active task.
		return nil, failBeforeInvocation(projectRoot, tf, fmt.Errorf("creating result contract: %w", err))
	}

	// 6. Skip worktree creation for now (would require git operations in test)
	// In production this would call git.CreateWorktree()
	workDir := projectRoot

	// 7. Read guides from guide-dir (before building context)
	guides, err := readGuides(projectRoot, resolved.Config.GuideDir)
	if err != nil {
		return nil, fmt.Errorf("reading guides: %w", err)
	}

	// 8. Build AdapterContext with command-specific fields
	ac := buildAdapterContext(projectRoot, taskID, resolved, target, flags, branch, cfg, workDir, resultPath, plannedArtefact, validatedOwner)
	ac.Guides = guides
	ac.TestCaseContent = testCaseContent
	ac.Force = flags.Force

	// CON-023 / ENH-146 (manual-execute exception): if the manual-execute
	// adapter pipeline parsed a framework out of the result template, that
	// value is the authoritative source -- wiring does not exist for
	// manual-only TCs. Update rc.Framework on disk so the reader overlay
	// sees the right framework for this terminal handoff.
	if ac.ResultFramework != "" && ac.ResultFramework != rc.Framework {
		rc.Framework = ac.ResultFramework
		// BUG-209 section 9: this is a pre-invocation write. If it fails the adapter
		// must not start, and the task must be routed to error/ so no pending task
		// is stranded.
		if updErr := result.Update(resultPath, map[string]interface{}{"framework": ac.ResultFramework}); updErr != nil {
			return nil, failBeforeInvocation(projectRoot, tf, fmt.Errorf("recording framework on handoff: %w", updErr))
		}
	}

	// 9. Assemble prompt if prompt-template is set
	if resolved.Config.PromptTemplate != "" {
		tmplPath := resolved.Config.PromptTemplate
		if !filepath.IsAbs(tmplPath) {
			tmplPath = filepath.Join(projectRoot, tmplPath)
		}

		promptVars := map[string]string{
			"reference":        ac.Reference,
			"testcase":         ac.TestCase,
			"testcase_content": ac.TestCaseContent,
			"output_dir":       ac.OutputDir,
			"output_subdir":    ac.OutputSubdir,
			"artefact_file":    ac.ArtefactFile,
			"testcase_file":    ac.TestCaseFile,
			"branch":           ac.Branch,
			"framework":        flags.Framework,
			"focus":            flags.Focus,
			"context":          ac.Context,
			"context_file":     ac.ContextFile,
			"guides":           ac.Guides,
			"environment":      ac.Environment,
			"tc_ids":           ac.TestCaseIDs,
			"tc_name":          ac.TestCaseName,
		}

		assembled, err := prompt.Assemble(tmplPath, promptVars)
		if err != nil {
			return nil, fmt.Errorf("assembling prompt: %w", err)
		}
		ac.AssembledPrompt = assembled

		// Write prompt to temp file for adapter consumption
		promptFile := filepath.Join(projectRoot, ".gtms", "tmp", taskID+"-prompt.md")
		if err := os.WriteFile(promptFile, []byte(assembled), 0644); err != nil {
			return nil, fmt.Errorf("writing prompt file: %w", err)
		}
		ac.PromptFile = promptFile
	}

	// ENH-133: Check for deferred manual-execute validation errors.
	// Go-side YAML parsing runs inside buildAdapterContext; errors are stored on
	// ac.ManualExecuteError so they can be handled here (after the handoff contract
	// is pre-populated with status: pending, before the adapter is dispatched).
	//
	// ENH-133 review-fix (round 2): persistence errors in this branch are no
	// longer silently swallowed. result.Update and buildPipelineRecords
	// failures surface as warnings on InvokeResult.Warnings, matching the
	// `"contract update failed: %v"` and `"Pipeline record could not be
	// written: %v"` shapes used by the normal sync path so callers see the
	// same warning vocabulary regardless of which branch wrote them.
	// task.Move failures escalate to a Go-error return because the task file
	// not making it into `error/` is a hard on-disk inconsistency the caller
	// must see -- the rest of the system relies on task placement.
	if ac.ManualExecuteError != nil {
		now := time.Now().UTC().Format(time.RFC3339)
		summary := ac.ManualExecuteError.Error()

		var warnings []string

		// BUG-209 section 9: an outcome that did not land writes no record.
		if updateErr := persistTerminal(resultPath, map[string]interface{}{
			"status":    "error",
			"summary":   summary,
			"completed": now,
		}, nil); updateErr != nil {
			return nil, failBeforeInvocation(projectRoot, tf, fmt.Errorf("contract update failed: %w", updateErr))
		}

		tf.Error = summary
		if moveErr := task.Move(projectRoot, tf, "error"); moveErr != nil {
			return nil, fmt.Errorf("moving task file to error/: %w", moveErr)
		}

		// Build pipeline records so the automation record reflects the error.
		rc, rcErr := result.Read(resultPath)
		if rcErr != nil {
			warnings = append(warnings, fmt.Sprintf("Pipeline record could not be written: %v", rcErr))
		} else {
			_, pWarn, pErr := buildPipelineRecords(projectRoot, cfg, tf, rc, flags.Owner)
			warnings = append(warnings, pWarn...)
			if pErr != nil {
				warnings = append(warnings, fmt.Sprintf("Pipeline record could not be written: %v", pErr))
			}
		}

		return &InvokeResult{
			TaskID:   tf.ID,
			Adapter:  resolved.Name,
			Mode:     resolved.Mode,
			Branch:   tf.Branch,
			Status:   "error",
			Summary:  summary,
			Filename: tf.Filename(),
			Target:   tf.Target,
			Warnings: warnings,
		}, nil
	}

	// 10a. Pre-adapter cleanup: remove the target's own stale output (BUG-031).
	// When --force is set, the adapter will regenerate output, so the target's own
	// previous artefacts must go first. Which files are the TARGET'S is decided by
	// wiring, never by basename -- see cleanupExistingOutputForOwner.
	//
	// The sweep is scoped to the directory of the PLANNED ARTEFACT, i.e. the exact
	// location the Tier 0 write will land in. Deriving it any other way is how the
	// sweep came to run in the wrong directory entirely: with output-dir unset,
	// ac.OutputDir is the SpecsDir default (gtms/automation/specs/<adapter>/)
	// while the framework-native write goes to test/acceptance/, so the sweep
	// walked a directory that never contained the artefact. For a non-Tier-0
	// adapter there is no planned path (the adapter owns its own output naming --
	// see the adapter-boundary limitation noted on cleanupExistingOutputForOwner)
	// and the configured output dir remains the only scope available.
	//
	// A refusal here is a hard error: an unprovable wiring index means the sweep
	// cannot know whose files these are, and guessing is what destroyed data.
	if flags.Force && resolved.Command == "automate" {
		sweepDir := filepath.Join(ac.OutputDir, ac.OutputSubdir)
		if ac.PlannedArtefact != "" {
			sweepDir = filepath.Dir(ac.PlannedArtefact)
		}
		kept, cleanupErr := cleanupExistingOutputForOwner(flags.Claims, sweepDir, target)
		sweepWarnings = append(sweepWarnings, kept...)
		if cleanupErr != nil {
			tf.Error = cleanupErr.Error()
			moveErr := moveTaskToError(projectRoot, tf)
			// BUG-209 section 9: surface the handoff-write failure alongside the
			// cleanup failure rather than discarding it.
			if updErr := result.Update(resultPath, map[string]interface{}{
				"status":    "error",
				"summary":   cleanupErr.Error(),
				"completed": time.Now().UTC().Format(time.RFC3339),
			}); updErr != nil {
				cleanupErr = fmt.Errorf("%w (and recording it on the handoff failed: %v)", cleanupErr, updErr)
			}
			if moveErr != nil {
				return nil, fmt.Errorf("%w (and %v)", cleanupErr, moveErr)
			}
			return nil, cleanupErr
		}
	}

	// 10b. Snapshot output dir before invocation (for artefact detection)
	var preInvokeFiles map[string]struct{}
	if ac.OutputDir != "" {
		preInvokeFiles = snapshotDir(ac.OutputDir)
	}

	// 11. Move task to in-progress before adapter invocation (BUG-022)
	if err := task.Move(projectRoot, tf, "in-progress"); err != nil {
		return nil, fmt.Errorf("moving task to in-progress: %w", err)
	}
	// BUG-209 section 9: this is a pre-invocation write. If it fails the adapter
	// must not start; route the now-in-progress task to error/ so it does not
	// strand and block the next run as a duplicate active task.
	if updErr := result.Update(resultPath, map[string]interface{}{
		"status": "in-progress",
	}); updErr != nil {
		return nil, failBeforeInvocation(projectRoot, tf, fmt.Errorf("recording in-progress on handoff: %w", updErr))
	}

	// 12. Invoke adapter based on tier
	var invResult *InvocationResult

	// ENH-222: thread the structured "started" signal onto every non-error
	// return (success, async, create-quarantine, handleSyncResult). Error
	// returns set invokeRes=nil and carry Started on the invocationError instead.
	defer func() {
		if invokeRes != nil && invResult != nil {
			invokeRes.Started = invResult.Started
		}
	}()

	switch resolved.Tier {
	case 1:
		invResult, err = InvokeTier1(ctx, ac, resolved.Config.Command)
	case 2:
		scriptPath := resolved.Config.Script
		if !filepath.IsAbs(scriptPath) {
			scriptPath = filepath.Join(projectRoot, scriptPath)
		}
		invResult, err = InvokeTier2(ctx, ac, scriptPath)
	case 0:
		// ENH-150: Tier 0 action adapters (create/prime/automate/execute) run
		// through the full Invoke pipeline. Visibility commands
		// (status/gaps/triage) are still dispatched via InvokeBuiltin and
		// never reach here.
		invResult, err = invokeBuiltinAction(ac, resolved, cfg)
	default:
		return nil, fmt.Errorf("unsupported adapter tier: %d", resolved.Tier)
	}

	if err != nil {
		// Adapter invocation failed at the system level
		summary := fmt.Sprintf("Adapter invocation failed: %s", err.Error())
		if ctx.Err() == context.DeadlineExceeded {
			summary = fmt.Sprintf("adapter timed out after %s", effectiveTimeoutStr(resolved))
		} else if ctx.Err() == context.Canceled {
			summary = "adapter cancelled"
		}
		tf.Error = summary
		moveErr := moveTaskToError(projectRoot, tf)
		// BUG-209 section 9: surface a handoff-write failure (keeping the error
		// routing) rather than discarding it.
		if updErr := persistTerminal(resultPath, map[string]interface{}{
			"status":    "error",
			"summary":   summary,
			"completed": time.Now().UTC().Format(time.RFC3339),
		}, nil); updErr != nil {
			summary = fmt.Sprintf("%s\n    (recording the error on the handoff failed: %v)", summary, updErr)
		}
		if moveErr != nil {
			summary = fmt.Sprintf("%s\n    (%v)", summary, moveErr)
		}
		// ENH-222: a post-launch failure (stream/wait error, or a timeout/cancel
		// that still returned a started InvocationResult) carries Started so the
		// CLI reports "started, then failed" rather than "could not start". A
		// pre-launch cmd.Start failure leaves invResult nil => Started false.
		started := invResult != nil && invResult.Started
		// BUG-171: wrap so the human-readable summary stays the surfaced
		// message (timeout/cancel/generic diagnostics unchanged) WHILE the
		// underlying error's unwrap chain survives, keeping typed errors like
		// *MissingExecuteAdapterError discoverable via errors.As at the CLI.
		return nil, &invocationError{summary: summary, err: err, started: started}
	}

	// Detect cancellation/timeout even when process exited with non-zero code
	// (process.go captures ExitError as a successful InvocationResult with exit code)
	if invResult != nil && ctx.Err() != nil {
		summary := "adapter cancelled"
		if ctx.Err() == context.DeadlineExceeded {
			summary = fmt.Sprintf("adapter timed out after %s", effectiveTimeoutStr(resolved))
		}
		tf.Error = summary
		moveErr := moveTaskToError(projectRoot, tf)
		// BUG-209 section 9: surface a handoff-write failure (keeping the error
		// routing) rather than discarding it.
		if updErr := persistTerminal(resultPath, map[string]interface{}{
			"status":    "error",
			"summary":   summary,
			"completed": time.Now().UTC().Format(time.RFC3339),
		}, nil); updErr != nil {
			summary = fmt.Sprintf("%s\n    (recording the error on the handoff failed: %v)", summary, updErr)
		}
		if moveErr != nil {
			summary = fmt.Sprintf("%s\n    (%v)", summary, moveErr)
		}
		// ENH-222: the process launched (invResult != nil && invResult.Started),
		// then the timeout/cancel fired -- "started, then failed".
		return nil, &invocationError{summary: summary, err: errors.New(summary), started: invResult.Started}
	}

	// 12a. Post-write validation for create commands (BUG-038, BUG-106)
	// Validate that every emitted spec file's frontmatter test_case_id matches
	// the filename ID, is from the pre-generated batch, and is unique.
	// Runs before handleSyncResult so it applies identically to Tier 1 and Tier 2.
	//
	// BUG-106: files with missing or unparseable frontmatter are degraded
	// (allowed through for filename-only listing) rather than hard-failed.
	// Only strict violations (parsed-but-invalid test_case_id, malformed
	// filename shape) block create.
	if resolved.Command == "create" && resolved.Mode == "sync" && invResult != nil && invResult.ExitCode == 0 {
		batchIDs := strings.Split(ac.TestCaseIDs, ",")

		// BUG-110: scope validation to files owned by this invocation.
		// When SavedFiles is populated (Tier 0, streaming Tier 1), only
		// those files enter the batch-membership check. Sibling
		// invocations' files in the same OutputDir are silently skipped.
		// When SavedFiles is empty (Tier 2 direct-write), ownedBasenames
		// is nil and the validator falls back to full dir-scan behavior.
		var ownedBasenames map[string]struct{}
		if len(invResult.SavedFiles) > 0 {
			ownedBasenames = make(map[string]struct{}, len(invResult.SavedFiles))
			for _, p := range invResult.SavedFiles {
				ownedBasenames[filepath.Base(p)] = struct{}{}
			}
		}

		valResult, valErr := ValidateCreateSpecs(ac.OutputDir, batchIDs, preInvokeFiles, ownedBasenames)
		if valErr != nil {
			// System-level error scanning output dir -- treat as warning, don't block
			// (the adapter succeeded; scanning failed for an OS reason)
		} else if len(valResult.Violations) > 0 {
			now := time.Now().UTC().Format(time.RFC3339)

			// BUG-203 AC10/AC11 (Owner Decision 4, revised 2026-10-15 quarantine,
			// 2026-10-16 ownership + honest reporting; REV-209 CODEX-001/002,
			// CLAUDE-001/004).
			//
			// Validation inspects and reports every newly appeared spec. A rejected
			// spec is QUARANTINED -- moved byte-for-byte out of gtms/test/cases/ into
			// gtms/tasks/error/<task-id>/artifacts/ (where task.List and the reader
			// scanners never look: they read only top-level *.md per status folder) --
			// ONLY when its canonical filename ID is one of the IDs GTMS generated for
			// this invocation (batchIDs), i.e. GTMS can prove this create owns it. An
			// unattributable file (any other ID, including an invented or malformed one,
			// or a concurrent sibling create's output) is NEVER moved: it is left in
			// place, named, and flagged so the user inspects concurrent create activity.
			// Valid files from this invocation stay untouched. A file counts as
			// quarantined only once the artefact exists and the source is gone; a failed
			// move is reported with the file's actual location, never as preserved.
			//
			// REV-209 R2-CODEX-001 (path containment): before moving anything, resolve
			// BOTH the source (under ac.OutputDir, which config may point outside the root
			// via a relative `..` or a parent symlink/junction) and the destination
			// through pathsafe.ResolveUnderRoot. If either cannot be proven inside the
			// physical project root, do not move -- leave the source in place and report it
			// with a DISTINCT path-containment message.
			//
			// REV-209 R2-CLAUDE-002 (no hard-coded gtms/): build the artefacts folder from
			// layout.TasksDir, which follows a renamed GTMS parent (ENH-098), so the
			// quarantine lands beside the error task and no stray gtms/ tree is created.
			batchSet := make(map[string]struct{}, len(batchIDs))
			for _, id := range batchIDs {
				if trimmed := strings.TrimSpace(id); trimmed != "" {
					batchSet[trimmed] = struct{}{}
				}
			}
			artifactsDir := filepath.Join(layout.TasksDir(projectRoot), "error", tf.ID, "artifacts")
			seenRejected := make(map[string]struct{}, len(valResult.Violations))
			var preserved []string             // project-relative quarantine paths (fully moved)
			var unattributable []string        // original paths left in place (ownership unproven)
			var notRegular []string            // original paths left in place (symlink / special file)
			var pathUnsafe []string            // original paths left in place (outside the root / unresolvable)
			var moveFailed []quarantineFailure // move attempted but failed; file still at original path
			for _, v := range valResult.Violations {
				if _, done := seenRejected[v.File]; done {
					continue // a file with several violations is handled once
				}
				seenRejected[v.File] = struct{}{}
				srcAbs := filepath.Join(ac.OutputDir, v.File)
				// REV-246 chunk-001-001: ac.OutputDir is canonical for create (BUG-214) while
				// projectRoot may be the link spelling, so a plain Rel would show
				// "../project/..." in this diagnostic. canonicalProjectRel reconciles the two
				// root spellings; on error keep the old safe fallback (forward-slashed abs).
				origRel, relErr := canonicalProjectRel(projectRoot, srcAbs)
				if relErr != nil {
					origRel = filepath.ToSlash(srcAbs)
				}
				if !ownedByBatch(v.File, batchSet) {
					// Ownership unproven: never move it (REV-209 CODEX-001/CLAUDE-001).
					unattributable = append(unattributable, origRel)
					continue
				}
				// REV-209 R3-CODEX-004: Lstat (no follow) BEFORE resolving the path. Only a
				// regular file may be quarantined -- resolving an in-project symlink first
				// would make its target the thing that gets moved, and moving a symlink
				// preserves the link, not the validated bytes. A symlink/special file is left
				// in place with an accurate diagnostic.
				if fi, lerr := os.Lstat(srcAbs); lerr != nil || !fi.Mode().IsRegular() {
					notRegular = append(notRegular, origRel)
					continue
				}
				// REV-209 R2-CODEX-001 (containment) + R3-CODEX-003: resolve BOTH source and
				// destination under the project root, and MOVE the resolved paths (not the
				// lexical ones). If either escapes or cannot resolve, leave the source in
				// place and report the distinct path-containment message.
				srcResolved, _, srcErr := pathsafe.ResolveUnderRoot(projectRoot, srcAbs)
				if srcErr != nil {
					pathUnsafe = append(pathUnsafe, origRel)
					continue
				}
				dstResolved, _, dstErr := pathsafe.ResolveUnderRoot(projectRoot, filepath.Join(artifactsDir, v.File))
				if dstErr != nil {
					pathUnsafe = append(pathUnsafe, origRel)
					continue
				}
				dest, ok, qErr := quarantineRejectedSpec(projectRoot, srcResolved, dstResolved)
				if ok {
					preserved = append(preserved, dest)
				} else {
					moveFailed = append(moveFailed, quarantineFailure{path: origRel, err: qErr})
				}
			}

			summary := quarantineCreateFailureSummary(valResult.Violations, preserved, unattributable, notRegular, pathUnsafe, moveFailed)

			// BUG-203 AC6: when the failure is a stale pre-v2 header (a v0.3.x
			// scaffolded producer that was never refreshed), name the producing
			// template or script and how to refresh it, so the user does not look at
			// the stamped spec the error otherwise names.
			if hint := staleScaffoldProducerHint(resolved, ac, projectRoot, valResult.Violations); hint != "" {
				summary = summary + "\n    " + hint
			}

			// REV-164 CODEX-001: this branch runs BEFORE handleSyncResult, so
			// it never reached the BUG-191 recovery. Update decodes the file on
			// disk before merging, so when the adapter also left the handoff
			// unusable (malformed YAML, missing, unreadable) it writes nothing
			// and fails. The error used to be discarded, which routed the task
			// to error/ but left an unreadable durable handoff -- falsifying
			// BUG-191's promise that all four synchronous commands leave a
			// READABLE error handoff for an unusable contract.
			//
			// So: stop discarding the error, and on failure hand the same
			// reconstruction path the BUG-191 recovery uses BOTH diagnostics.
			// The violations stay primary (they are why this branch exists);
			// the contract-read failure is appended, never substituted.
			prior, _ := result.CaptureHandoffState(resultPath)
			if updateErr := persistTerminal(resultPath, map[string]interface{}{
				"status":           "error",
				"completed":        now,
				"attempts":         1,
				"summary":          summary,
				"validation-error": summary,
			}, nil); updateErr != nil {
				recovered, recoverErr := recoverUnusableContract(ctx, projectRoot, tf, resultPath, resolved, invResult, prior, flags.ArtefactHash, ac.ArtefactFile, updateErr, summary)
				return recovered, withPostRunSignal(invResult, recoverErr)
			}

			tf.Error = summary
			if moveErr := moveTaskToError(projectRoot, tf); moveErr != nil {
				combined := fmt.Sprintf("%s\n    (%v)", summary, moveErr)
				return nil, &invocationError{summary: combined, err: moveErr, started: invResult.Started}
			}

			return &InvokeResult{
				TaskID:   tf.ID,
				Adapter:  resolved.Name,
				Mode:     resolved.Mode,
				Branch:   tf.Branch,
				Status:   "error",
				Summary:  summary,
				Filename: tf.Filename(),
				Target:   tf.Target,
			}, nil
		}
		// valResult.Degraded files are allowed through -- the listing code
		// in create.go:readTCFrontmatter renders them filename-only.
	}

	// 12b. Handle result based on mode
	if resolved.Mode == "sync" {
		invokeRes, invokeErr := handleSyncResult(ctx, projectRoot, cfg, tf, resultPath, resolved, invResult, ac.OutputDir, preInvokeFiles, flags.ArtefactHash, ac.ArtefactFile, flags.Owner)
		if invokeRes != nil && testCaseReadErr != nil {
			invokeRes.Warnings = append(invokeRes.Warnings, fmt.Sprintf("Test case file found but could not be read: %v", testCaseReadErr))
		}
		return invokeRes, withPostRunSignal(invResult, invokeErr)
	}

	// Async: task is already in-progress (moved before invocation), just return
	return &InvokeResult{
		TaskID:   taskID,
		Adapter:  resolved.Name,
		Mode:     resolved.Mode,
		Branch:   branch,
		Status:   "in-progress",
		Filename: tf.Filename(),
		Target:   target,
	}, nil
}

// buildAdapterContext creates the AdapterContext with command-specific field values.
//
// plannedArtefact is the absolute artefact path the invocation boundary already
// AUTHORISED and reserved (ArtefactReservation.PlannedPath). It is a parameter
// rather than something recomputed here on purpose: that is what makes the value
// the adapter writes to provably the value the ownership check inspected. Pass
// "" from any caller that took no reservation.
func buildAdapterContext(projectRoot, taskID string, resolved *ResolvedAdapter, target string, flags CommandFlags, branch string, cfg *config.Config, workDir, resultPath, plannedArtefact string, validatedOwner ValidatedTestCaseOwner) *AdapterContext {
	ctx := &AdapterContext{
		Owner:          flags.Owner,
		ValidatedOwner: validatedOwner,
		TaskID:         taskID,
		Command:        resolved.Command,
		Branch:         branch,
		Repo:           cfg.Project.Repo,
		ProjectRoot:    projectRoot,
		WorkDir:        workDir,
		ResultFile:     resultPath,
		PromptTemplate: resolved.Config.PromptTemplate,
		Focus:          flags.Focus,
		Context:        flags.Context,
		ContextFile:    flags.ContextFile,
		Environment:    flags.Environment,
	}

	// ENH-168: resolve the directory the adapter (Tier 1/2) runs in. Defaults to the
	// workDir base (project root today; the git worktree when that lands). An explicit
	// working-dir is joined on top. Both tiers read ctx.RunDir for cmd.Dir. Tier 0
	// built-ins exec nothing, so RunDir is harmless for them (and config load warns when
	// working-dir is set on a built-in). Do NOT mutate ctx.TestCaseFile / ctx.ArtefactFile
	// here -- those carriers are absolutized for the adapter at the tier1/tier2 boundary
	// only, because Tier-0 BuiltinAutomate consumes ctx.TestCaseFile as project-relative.
	ctx.RunDir = workDir
	if resolved.Config.WorkingDir != "" {
		ctx.RunDir = filepath.Join(workDir, filepath.FromSlash(resolved.Config.WorkingDir))
	}

	// Set command-specific context fields
	switch resolved.Command {
	case "create":
		ctx.Reference = flags.Reference
		ctx.TestCaseName = flags.Name
		// BUG-214 Owner Decision 7: when the create admission boundary carried the
		// containment-checked output directory, use it verbatim -- MkdirAll, the
		// built-in write, Tier 1 {output_dir} and Tier 2 GTMS_OUTPUT_DIR all read
		// ctx.OutputDir, so this is what makes the path the adapter writes provably
		// the path that was checked. The fallback below is for direct callers (tests)
		// that do not set the carrier; production create always sets it.
		if flags.ResolvedOutputDir != "" {
			ctx.OutputDir = flags.ResolvedOutputDir
		} else if resolved.Config.OutputDir != "" {
			ctx.OutputDir = filepath.Join(projectRoot, resolved.Config.OutputDir)
		} else {
			ctx.OutputDir = filepath.Join(layout.TestCasesDir(projectRoot), flags.Folder)
		}
		// Generate batch of pre-generated test case IDs for adapter consumption (ENH-042)
		tcIDs := make([]string, 20)
		for i := range tcIDs {
			tcIDs[i] = "tc-" + id.New()
		}
		ctx.TestCaseIDs = strings.Join(tcIDs, ",")
		// ENH-161: Populate template path for create adapters (role-specific).
		ctx.TemplateFile = ResolveTemplatePath(projectRoot, "create", resolved.Name)
	case "automate":
		ctx.TestCase = target
		if resolved.Config.OutputDir != "" {
			ctx.OutputDir = filepath.Join(projectRoot, resolved.Config.OutputDir)
			ctx.OutputDirConfigured = true // BUG-125: signal the Tier-0 built-in to honour it
		} else {
			ctx.OutputDir = filepath.Join(layout.SpecsDir(projectRoot), resolved.Name)
		}
		tcSource := ownerSource(flags.Owner)
		ctx.OutputSubdir = deriveOutputSubdir(tcSource)
		ctx.TestCaseFile = tcSource
		// The planned artefact path, decided by the carried owner BEFORE the
		// adapter is invoked, collision-checked and reserved there, and handed in
		// as plannedArtefact so this is the same value rather than a second
		// computation of it (see enforceArtefactOwnership,
		// ArtefactReservation.PlannedPath and BuiltinAutomate). Empty for every
		// path that has no planned Tier 0 artefact.
		ctx.PlannedArtefact = plannedArtefact
		ctx.Claims = flags.Claims

		// ENH-132: Populate manual-prime context fields when framework is manual.
		// This is framework-specific context population, not tier-specific branching.
		framework := ResolveFramework(resolved, flags.Framework)
		ctx.Framework = framework // ENH-151: available to BuiltinAutomate
		if framework == "manual" && tcSource != "" {
			populateBuiltinPrimeFields(ctx, projectRoot, target, resolved.Name, validatedOwner)
		}
	case "prime":
		// ENH-150: prime is now its own command (no longer resolves under "automate").
		// Context population mirrors the automate/manual-prime path from ENH-132.
		ctx.TestCase = target
		if resolved.Config.OutputDir != "" {
			ctx.OutputDir = filepath.Join(projectRoot, resolved.Config.OutputDir)
		} else {
			ctx.OutputDir = filepath.Join(layout.SpecsDir(projectRoot), resolved.Name)
		}
		tcSource := ownerSource(flags.Owner)
		ctx.OutputSubdir = deriveOutputSubdir(tcSource)
		ctx.TestCaseFile = tcSource

		// Populate manual-prime context fields. For Tier 2 adapters this provides
		// the GTMS_TEMPLATE_FILE, GTMS_OUTPUT_FILE, etc. env vars. For Tier 0
		// built-ins, these fields drive BuiltinPrime directly.
		framework := ResolveFramework(resolved, flags.Framework)
		if framework == "manual" && tcSource != "" {
			populateBuiltinPrimeFields(ctx, projectRoot, target, resolved.Name, validatedOwner)
		}
	case "execute":
		ctx.TestCase = target
		ctx.ArtefactFile = flags.ArtefactFile
		if resolved.Config.OutputDir != "" {
			ctx.OutputDir = filepath.Join(projectRoot, resolved.Config.OutputDir)
		} else {
			ctx.OutputDir = filepath.Join(projectRoot, "results")
		}
		tcSource := ownerSource(flags.Owner)
		ctx.OutputSubdir = deriveOutputSubdir(tcSource)
		ctx.TestCaseFile = tcSource

		// ENH-133: Populate manual-execute context fields for the manual path.
		// This is framework-specific context population, not tier-specific
		// branching.
		//
		// Adapter first, framework second: keys solely on the resolved adapter
		// via IsManualFramework -- same predicate cli/execute.go uses to defer
		// the generic artefact pre-check. Using ResolveFramework() == "manual"
		// here would diverge for ResolvedAdapter{Name: "manual-execute",
		// Config: &AdapterConfig{Framework: ""}}: the CLI defers to the
		// manual path but the invoker would skip populateManualExecuteFields,
		// leaving the Tier 2 script to fail later with missing GTMS_RESULT_*
		// env vars instead of the Go-side validation error.
		if IsManualFramework(resolved) {
			populateManualExecuteFields(ctx, projectRoot, target, flags)
		}
	default:
		ctx.Reference = flags.Reference
		if resolved.Config.OutputDir != "" {
			ctx.OutputDir = filepath.Join(projectRoot, resolved.Config.OutputDir)
		} else {
			ctx.OutputDir = layout.TestCasesDir(projectRoot)
		}
		// Generate batch of pre-generated test case IDs for unknown commands (same as create)
		defIDs := make([]string, 20)
		for i := range defIDs {
			defIDs[i] = "tc-" + id.New()
		}
		ctx.TestCaseIDs = strings.Join(defIDs, ",")
	}
	return ctx
}

// normalizeReservedMode3Runtime enforces the GTMS-owned runtime metadata on a
// reserved Mode 3 execute adapter's post-invocation handoff (BUG-210): framework
// is always "manual" and mode is always "sync". The scaffolded adapter scripts
// are user-editable, so a script may write framework: fx or mode: async into its
// own contract; admission refuses those as CONFIGURED values, but an edited
// script body is the one route admission cannot catch. It mutates rc in place and
// returns only the fields it changed, so the caller folds them into its single
// restore write. Historical evidence is not touched -- this runs only on the
// handoff for the current run.
func normalizeReservedMode3Runtime(rc *result.ResultContract) map[string]interface{} {
	updates := map[string]interface{}{}
	if rc.Framework != "manual" {
		rc.Framework = "manual"
		updates["framework"] = "manual"
	}
	if rc.Mode != "sync" {
		rc.Mode = "sync"
		updates["mode"] = "sync"
	}
	return updates
}

// canonicalReservedExecuteErrorContract builds a COMPLETE error contract for a
// reserved Mode 3 execute run entirely from GTMS-held identity (BUG-210 / REV-273):
// framework is manual and mode is sync, never the adapter's. It is the Reconstruct
// fallback source for the invalid-contract recovery, so a handoff Update cannot
// overwrite is still replaced by a readable, canonical error document that no
// reader attributes to a wired framework. summary/log are the recovery diagnostics
// and completed is the recovery timestamp.
func canonicalReservedExecuteErrorContract(ctx context.Context, projectRoot string, tf *task.TaskFile, resolved *ResolvedAdapter, summary, log, completed string) *result.ResultContract {
	rc := &result.ResultContract{
		Task:        tf.ID,
		Command:     resolved.Command,
		Target:      tf.Target,
		Adapter:     resolved.Name,
		Mode:        "sync",
		Created:     tf.Created,
		Status:      "error",
		Framework:   "manual",
		Summary:     result.CapSummary(summary),
		Log:         log,
		Attempts:    1,
		Completed:   completed,
		ExecutedBy:  tf.ExecutedBy,
		Environment: tf.Environment,
	}
	stampGitContext(ctx, projectRoot, rc)
	return rc
}

// persistReservedMode3Handoff durably writes the canonical contract for a reserved
// Mode 3 execute run, failing CLOSED (BUG-210 / REV-272 CODEX-002). It first
// applies updates with result.Update -- a field merge that re-reads and validates
// the on-disk handoff -- so the adapter's other fields survive. If that merge
// fails (for example the adapter left the file corrupt or unwritable), it rewrites
// the whole contract with result.Reconstruct, which does NOT read the old file and
// which rc fully describes (rc already carries framework: manual, mode: sync and a
// valid terminal status). It returns nil once either write persists the canonical
// fields, and an error naming BOTH failures otherwise -- the residual case the
// caller treats as the documented total-persistence limitation (BUG-218): route
// the task to error, do not complete it or write a pipeline record. With no updates
// there is nothing to persist, so it returns nil.
//
// BUG-209 section 9: this goes through the shared rule, so a refusal or a concurrency
// error is returned as it is, with no reconstruct over the newer writer's file.
func persistReservedMode3Handoff(path string, updates map[string]interface{}, rc *result.ResultContract) error {
	updateErr, reconstructErr := persistTerminalDetail(path, updates, rc)
	if updateErr == nil || blocksReconstruct(updateErr) {
		return updateErr
	}
	if reconstructErr != nil {
		return fmt.Errorf("could not persist GTMS-owned framework/mode for reserved Mode 3 adapter: update: %v; reconstruct: %w", updateErr, reconstructErr)
	}
	return nil
}

// handleSyncResult processes the result of a synchronous adapter invocation.
//
// cfg is threaded through so the automate branch can resolve the canonical
// execute adapter when writing the new wiring record (CON-023 / ENH-145).
// ctx is threaded through so Tier 2 post-overwrite Git-context re-stamping
// (CON-023 / ENH-146) can honour cancellation.
// artefactPath is the trusted, GTMS-held artefact path for this invocation
// (ac.ArtefactFile at the sole call site). It is threaded in -- rather than
// rediscovered -- so the BUG-191 unusable-contract recovery can stamp the
// overlay-join `artefact` field from state GTMS holds, never from the adapter's
// unparseable document. Empty on paths that have no artefact (e.g. automate).
func handleSyncResult(ctx context.Context, projectRoot string, cfg *config.Config, tf *task.TaskFile, resultPath string, resolved *ResolvedAdapter, invResult *InvocationResult, outputDir string, preInvokeFiles map[string]struct{}, artefactHash string, artefactPath string, owner *testcase.Owner) (res *InvokeResult, retErr error) {
	now := time.Now().UTC().Format(time.RFC3339)

	// ENH-191: capture the wiring execute adapter written during a sync
	// automate so the CLI can emit the verbose "Execute adapter for wiring:
	// <name>" report. Stamped onto whichever InvokeResult this function
	// returns; the success branches assign it below. Empty on failure /
	// non-automate paths, where no report should fire.
	var wiringAdapter string
	defer func() {
		if res != nil {
			res.WiringAdapter = wiringAdapter
		}
	}()

	// For Tier 2: check if the script updated the result contract.
	// ENH-130: enter validation for ANY status the script wrote beyond the
	// in-progress marker GTMS set before invocation. This catches legacy
	// values (fail, skipped) that Validate rejects, preventing them from
	// falling through to the exit-code handler which would silently
	// overwrite to status: complete + result: pass.
	//
	// BUG-111 round 3: an empty status (rc.Status == "") means the Tier 2
	// script's contract write failed partway -- e.g. a restricted PATH
	// truncated the result file via `>` redirect before `cat` could run.
	// For create/automate/prime this still falls through to exit-code
	// handling so the script's stderr is folded into the surfaced summary
	// (where the missing-tooling diagnostic lives). ENH-188 Option A routes
	// synchronous Tier 2 EXECUTE instead to recordNonTerminalExecuteError,
	// which preserves that same stderr folding in the summary while recording
	// status: error rather than a false exit-code pass.
	if resolved.Tier == 2 {
		// BUG-209 section 9: the state recovery may later reconstruct over must be the
		// file this read looked at, so a newer writer's file is never overwritten.
		priorState, _ := result.CaptureHandoffState(resultPath)
		rc, err := result.Read(resultPath)
		if err == nil && rc.Status != "" && rc.Status != "pending" && rc.Status != "in-progress" {
			// BUG-210 / REV-272 (CLAUDE-001, CODEX-001, CODEX-003): framework and
			// mode are GTMS-owned for a reserved Mode 3 EXECUTE adapter (command
			// scoped -- a same-named automate/prime adapter is ordinary). Every exit
			// from this terminal branch -- invalid-contract recovery below and the
			// valid-terminal restore further down -- must record framework: manual
			// and mode: sync, so a user-edited script that wrote framework: fx or
			// mode: async cannot attribute the run to a wired framework. The two
			// other Tier 2 exits are already canonical from GTMS-held state:
			// recordNonTerminalExecuteError and recoverUnusableContract both stamp
			// framework from tf.Framework (manual) and mode from resolved.Mode (sync).
			reservedMode3 := isReservedMode3Execute(resolved)

			// ENH-130: Validate script-written contracts at the Tier 2 read boundary.
			// Tier 2 adapters write contracts via heredoc, bypassing result.Update validation.
			if validateErr := result.Validate(rc); validateErr != nil {
				// Script produced a malformed contract -- treat as adapter error.
				now := time.Now().UTC().Format(time.RFC3339)
				originalContent := fmt.Sprintf("status: %s, result: %s, summary: %s", rc.Status, rc.Result, rc.Summary)
				summary := fmt.Sprintf("adapter wrote invalid contract: %v", validateErr)
				recoveryUpdate := map[string]interface{}{
					"status":    "error",
					"result":    "",
					"summary":   summary,
					"log":       originalContent,
					"completed": now,
				}
				if reservedMode3 {
					// REV-273 CLAUDE-002 / CODEX-001: framework and mode are GTMS-owned
					// and the recovery write is FAIL-CLOSED. (The earlier "already fail
					// closed" claim was wrong: a plain result.Update returned on failure
					// without attempting Reconstruct and left the task in-progress, so a
					// read-only handoff kept framework: fx and replaced a real fx pass.)
					// Canonicalise framework/mode into the merge, then persist through
					// persistReservedMode3Handoff: Update, else Reconstruct from a
					// COMPLETE canonical error contract built from GTMS-held identity.
					// Route the task to error on every outcome where possible. Only a
					// double persistence failure returns the combined error with no
					// pipeline record -- the documented BUG-218 total-persistence limit.
					recoveryUpdate["framework"] = "manual"
					recoveryUpdate["mode"] = "sync"
					canonical := canonicalReservedExecuteErrorContract(ctx, projectRoot, tf, resolved, summary, originalContent, now)
					if perr := persistReservedMode3Handoff(resultPath, recoveryUpdate, canonical); perr != nil {
						// REV-274 CLAUDE-001: mirror the valid-terminal branch's
						// combined-error handling. The task is still routed to error
						// where possible; if that move ALSO fails, the returned error
						// carries both the persistence failure and the move failure so
						// the operator is not left with a silently stranded task. No
						// pipeline record is written on either path.
						return nil, failAfterInvocation(projectRoot, tf, perr)
					}
					tf.Error = summary
					if moveErr := task.Move(projectRoot, tf, "error"); moveErr != nil {
						return nil, fmt.Errorf("moving task file: %w", moveErr)
					}
					return &InvokeResult{
						TaskID:   tf.ID,
						Adapter:  resolved.Name,
						Mode:     resolved.Mode,
						Branch:   tf.Branch,
						Status:   "error",
						Summary:  tf.Error,
						Filename: tf.Filename(),
						Target:   tf.Target,
					}, nil
				}
				// Non-reserved adapters: a hard error on write failure with NO reconstruct
				// attempt. BUG-209 section 9: the task goes to error/, so it cannot be left
				// active to block the next run.
				if updateErr := persistTerminal(resultPath, recoveryUpdate, nil); updateErr != nil {
					return nil, failAfterInvocation(projectRoot, tf, fmt.Errorf("recovery contract write failed: %w", updateErr))
				}
				tf.Error = summary
				if moveErr := task.Move(projectRoot, tf, "error"); moveErr != nil {
					return nil, fmt.Errorf("moving task file: %w", moveErr)
				}
				return &InvokeResult{
					TaskID:   tf.ID,
					Adapter:  resolved.Name,
					Mode:     resolved.Mode,
					Branch:   tf.Branch,
					Status:   "error",
					Summary:  tf.Error,
					Filename: tf.Filename(),
					Target:   tf.Target,
				}, nil
			}
			// ENH-080: hard stop for multi-file automate. Override any status
			// the Tier 2 script set -- two <gtms-file> tags for a single
			// automate TC is always wrong, even if the script reported
			// success.
			if resolved.Command == "automate" && len(invResult.SavedFiles) > 1 {
				return rejectMultiFileAutomate(projectRoot, tf, resultPath, resolved, invResult)
			}
			// BUG-029: Restore artefact hash that was lost when Tier 2 script overwrote the result contract.
			//
			// CON-023 / ENH-146: extend the same "post-Tier 2 overwrite,
			// restore the GTMS-stamped fields" treatment to Framework,
			// ExecutedBy, Environment, and Git-context. The Tier 2
			// adapter's heredoc may legitimately drop these fields (it
			// usually does); GTMS still needs them on the contract for
			// the reader overlay to do per-framework joining and CI
			// dashboards.
			restoreUpdates := map[string]interface{}{}
			if artefactHash != "" && rc.ArtefactHash != artefactHash {
				rc.ArtefactHash = artefactHash
				restoreUpdates["artefact-hash"] = artefactHash
			}
			if reservedMode3 {
				// BUG-210: framework AND mode are GTMS-owned for a reserved Mode 3
				// execute adapter. The scaffolded scripts are user-editable, so a
				// script can write framework: fx (or drop it) or mode: async into its
				// own contract; GTMS overwrites both so no reader attributes the run to
				// a wired framework and the handoff stays synchronous. Admission
				// already refused a non-manual --framework/configured framework and a
				// configured mode: async, so this normalisation only catches the one
				// route admission cannot: an edited script body. REV-272 CODEX-002
				// makes the persistence of these two fields fail closed below.
				for k, v := range normalizeReservedMode3Runtime(rc) {
					restoreUpdates[k] = v
				}
			} else if rc.Framework == "" && tf.Framework != "" {
				rc.Framework = tf.Framework
				restoreUpdates["framework"] = tf.Framework
			}
			if rc.ExecutedBy == "" && tf.ExecutedBy != "" {
				rc.ExecutedBy = tf.ExecutedBy
				restoreUpdates["executed_by"] = tf.ExecutedBy
			}
			if rc.Environment == "" && tf.Environment != "" {
				rc.Environment = tf.Environment
				restoreUpdates["environment"] = tf.Environment
			}
			// BUG-083 (Part A): Tier 2 heredocs overwrite target with the artefact path;
			// restore tf.Target so the ENH-146 reader overlay can join the handoff to wiring.
			if rc.Target != tf.Target {
				rc.Target = tf.Target
				restoreUpdates["target"] = tf.Target
			}
			// Re-stamp Git context unconditionally -- the adapter does not
			// own this metadata. Tier 2 scripts that DO set git fields
			// are overridden so the reader gets a consistent view.
			if commit, gErr := gitpkg.HeadCommit(ctx, projectRoot); gErr == nil {
				if commit != rc.GitCommit {
					rc.GitCommit = commit
					restoreUpdates["git-commit"] = commit
				}
			}
			if branch, gErr := gitpkg.CurrentBranch(ctx, projectRoot); gErr == nil {
				if branch != rc.GitBranch {
					rc.GitBranch = branch
					restoreUpdates["git-branch"] = branch
				}
			}
			if dirty, gErr := gitpkg.IsDirty(ctx, projectRoot); gErr == nil {
				if rc.GitDirty == nil || *rc.GitDirty != dirty {
					rc.GitDirty = &dirty
					restoreUpdates["git-dirty"] = dirty
				}
			}
			// BUG-084: cap oversize log payloads (>NotesSizeCapBytes) and spill
			// the full content to .gtms/logs/{task-id}.log. Tier 2 scripts emit
			// the log via heredoc; rc.Log here is the post-sanitisation content
			// from result.Read. Errors are surfaced via spillCapErr below so
			// the truncated bytes still land on the contract.
			var spillCapErr error
			if len(rc.Log) > result.NotesSizeCapBytes {
				truncated, spill, capErr := result.ApplyLogCap(projectRoot, tf.ID, rc.Log)
				restoreUpdates["log"] = truncated
				if spill != "" {
					restoreUpdates["notes-spill"] = spill
				}
				rc.Log = truncated
				rc.NotesSpill = spill
				spillCapErr = capErr
			}
			// BUG-084 (sibling cap): apply BUG-075 summary cap so the contract
			// stays bounded when a Tier 2 script ships an oversize summary
			// payload (e.g. a chatty failure assertion text).
			if len(rc.Summary) > result.SummarySizeCapBytes {
				capped := result.CapSummary(rc.Summary)
				restoreUpdates["summary"] = capped
				rc.Summary = capped
			}
			// ENH-188 (seventh owner approval): GTMS stamps `completed` on a
			// VALID TERMINAL Tier 2 EXECUTE contract only when the adapter omitted
			// it. This is reached only after terminal-contract validation
			// (result.Validate passed above), so it never rescues a missing,
			// unreadable, malformed or non-terminal contract -- those were already
			// routed to recovery or to recordNonTerminalExecuteError. Without this,
			// a conforming adapter that reports status+result but no completed
			// would never satisfy isAlreadyPassing (it reads completed), so bulk
			// execute would rerun it forever once the exit-code fallback (which
			// stamped completed) was retired. An adapter-provided completed value
			// is authoritative and left unchanged. Applies to terminal complete,
			// error and skip alike (all pass through this block).
			if resolved.Command == "execute" && rc.Completed == "" {
				rc.Completed = now
				restoreUpdates["completed"] = now
			}
			// Normalise rc.Artefact to a project-relative forward-slash path
			// so records are portable across machines and worktrees. Tier 2
			// scripts typically interpolate $GTMS_OUTPUT_FILE (an absolute
			// path) into the contract; mirror the same normalisation applied
			// to streaming-captured SavedFiles below. It is part of the one
			// restore write, so it lands or fails with the rest.
			if rc.Artefact != "" && filepath.IsAbs(rc.Artefact) {
				if relSlash, relErr := canonicalProjectRel(projectRoot, rc.Artefact); relErr == nil && !strings.HasPrefix(relSlash, "..") {
					if relSlash != rc.Artefact {
						rc.Artefact = relSlash
						restoreUpdates["artefact"] = relSlash
					}
				}
			}
			// BUG-209 section 9: the restore carries GTMS-owned fields including the
			// ENH-188 `completed` stamp, so this outcome is final only once it lands.
			// A failure routes the task to error/ before anything reports a pass, moves
			// it to complete/ or writes an execution record.
			if len(restoreUpdates) > 0 {
				if reservedMode3 {
					// REV-272 CODEX-002: for a reserved Mode 3 execute adapter the
					// GTMS-owned framework/mode (and the rest of the restored fields)
					// MUST reach disk before this run is treated as authoritative --
					// the task completed and a pipeline execution record written.
					// persistReservedMode3Handoff applies the field merge, then fails
					// CLOSED by rewriting the whole canonical contract if the merge
					// fails; it errors only if BOTH writes fail. On that residual
					// double failure -- the documented BUG-218 total-persistence limit
					// -- route the task to error and return without completing it or
					// writing a pipeline record. Every other adapter keeps the
					// warn-and-continue behaviour below.
					if perr := persistReservedMode3Handoff(resultPath, restoreUpdates, rc); perr != nil {
						return nil, failAfterInvocation(projectRoot, tf, perr)
					}
				} else if perr := persistTerminal(resultPath, restoreUpdates, rc); perr != nil {
					return nil, failAfterInvocation(projectRoot, tf, fmt.Errorf("contract restore write failed: %w", perr))
				}
			}
			// ENH-130: task movement decoupled from test outcome.
			// A clean adapter execution with a failing test moves the task to complete/.
			// Only adapter-execution errors move the task to error/.
			newStatus := "complete"
			if rc.Status == "error" {
				newStatus = "error"
				tf.Error = rc.Summary
			}

			if err := task.Move(projectRoot, tf, newStatus); err != nil {
				return nil, fmt.Errorf("moving task file: %w", err)
			}

			// Collect warnings and artifact data for Tier 2 contract-updated path
			var warnings []string

			// BUG-084: surface log spill write failure as a warning so the
			// truncated bytes still land on the contract but the operator
			// learns the full payload didn't make it to .gtms/logs/.
			if spillCapErr != nil {
				warnings = append(warnings, fmt.Sprintf("log spill write failed: %v", spillCapErr))
			}

			// ENH-096: merge adapter-injected warnings from the result contract
			if len(rc.Warnings) > 0 {
				warnings = append(warnings, rc.Warnings...)
			}

			// BUG-055: Surface adapter stderr as warnings on success path
			if stderrWarns := stderrToWarnings(invResult.Stderr); len(stderrWarns) > 0 {
				warnings = append(warnings, stderrWarns...)
			}

			// Build pipeline records for sync tasks (BUG-023: call on both success and failure
			// so that automation record's last-formal-result is updated to reflect the outcome)
			wa, pWarn, pErr := buildPipelineRecords(projectRoot, cfg, tf, rc, owner)
			wiringAdapter = wa
			warnings = append(warnings, pWarn...)
			if pErr != nil {
				warnings = append(warnings, fmt.Sprintf("Pipeline record could not be written: %v", pErr))
			}

			// S2: Empty stdout (only warn if no files were produced either)
			if strings.TrimSpace(invResult.Stdout) == "" && len(invResult.SavedFiles) == 0 {
				warnings = append(warnings, "Adapter produced no output.")
			}

			// Populate artifact data from streaming parser output (if any).
			// REV-245: relativise canonically so a linked-project output dir does not
			// produce "../" artefact paths.
			relPaths := make([]string, len(invResult.SavedFiles))
			for i, p := range invResult.SavedFiles {
				if rel, relErr := canonicalProjectRel(projectRoot, p); relErr == nil {
					relPaths[i] = rel
				} else {
					relPaths[i] = p
				}
			}

			// BUG-063: If streaming captured nothing, fall back to scanning
			// the output dir. The skeleton-style Tier 2 adapter writes the
			// file directly and reports it via the contract's `artefact:`
			// field rather than via <gtms-file> markers; without this the
			// CLI never sees ArtifactPaths and printCreatedHeadline never
			// fires. Mirrors the symmetrical fallback in the exit-code
			// branch below.
			artifactCount := len(invResult.SavedFiles)
			artifactPaths := relPaths
			if len(invResult.SavedFiles) == 0 && outputDir != "" {
				if scanned := scanOutputDir(projectRoot, outputDir, preInvokeFiles); len(scanned) > 0 {
					artifactCount = len(scanned)
					artifactPaths = scanned
				}
			}

			return &InvokeResult{
				TaskID:        tf.ID,
				Adapter:       resolved.Name,
				Mode:          resolved.Mode,
				Branch:        tf.Branch,
				Status:        rc.Status,
				Result:        rc.Result, // ENH-130: propagate test outcome
				Summary:       rc.Summary,
				Filename:      tf.Filename(),
				ArtifactCount: artifactCount,
				ArtifactPaths: artifactPaths,
				Warnings:      warnings,
				Target:        tf.Target,
			}, nil
		}
		// BUG-191: an unusable post-invocation handoff -- unparseable YAML, a
		// deleted file, or a file the adapter made unreadable -- is an
		// adapter-contract error, not a pass. result.Read surfaces all three as
		// a non-nil error (os.ReadFile failure for missing/unreadable, a decode
		// failure for malformed), so a single err != nil check catches the whole
		// class rather than special-casing fs.ErrNotExist plus YAML errors.
		//
		// This runs before exit-code handling, so it covers BOTH the exit-zero
		// and non-zero adapter-exit paths (they share this Tier 2 block); the
		// diagnostic folds in the process exit code when it is non-zero. A
		// readable but non-terminal (pending/in-progress/empty-status) handoff
		// does NOT reach here -- err is nil for it.
		if err != nil {
			return recoverUnusableContract(ctx, projectRoot, tf, resultPath, resolved, invResult, priorState, artefactHash, artefactPath, err, "")
		}
		// ENH-188 Option A: a synchronous Tier 2 EXECUTE adapter must leave a
		// VALID TERMINAL contract. A readable but non-terminal contract
		// (empty/pending/in-progress) after the adapter exits is an
		// execution-integrity error, not a pass -- exit 0 here used to fall
		// through to the exit-code handler and record status: complete, result:
		// pass (a false green; the rewritten tc-7d284f41 pins the new behaviour).
		// This prevents that false pass in normal operation. It does not cover a
		// total persistence failure, tracked by BUG-218: if both handoff repair
		// writes fail and the execution record cannot be written or inspected, a
		// later bulk run may still rely on an older pass. BUG-217 covers the
		// related stale display. The command dimension lives here, in the invoker, not
		// in internal/cli/ (CLAUDE rule 2); handleSyncResult is sync-only (its
		// sole caller gates on resolved.Mode == "sync"), so no mode check is
		// needed. create/automate/prime keep the exit-code fallback (Owner
		// Decision 1; tc-09d37e80 is the create regression control).
		if resolved.Command == "execute" {
			return recordNonTerminalExecuteError(ctx, projectRoot, cfg, tf, resultPath, resolved, invResult, rc, artefactHash, owner)
		}
		// Fall through to exit code handling if status is still non-terminal
		// (create/automate/prime only).
	}

	// For Tier 1 (or Tier 2 fallback): use exit code to determine result
	if invResult.ExitCode == 0 {
		// ENH-080: hard stop for multi-file automate. If the streaming writer
		// captured more than one <gtms-file> tag for a single automate TC,
		// fail the task and skip building the automation record -- no
		// comma-separated artefact: ever ships in a record GTMS writes.
		if resolved.Command == "automate" && len(invResult.SavedFiles) > 1 {
			return rejectMultiFileAutomate(projectRoot, tf, resultPath, resolved, invResult)
		}

		contractStatus := "complete"
		contractResult := "pass" // ENH-130: exit 0 = adapter ran successfully, test passed
		// ENH-160: Tier 0 built-in execute adapters (BuiltinExecute) carry
		// the user-authored test outcome through ResultOverride. Without
		// this, every Tier 0 manual-execute invocation would collapse to
		// `pass` regardless of the value in the manual result file.
		if invResult.ResultOverride != "" {
			contractResult = invResult.ResultOverride
		}

		updates := map[string]interface{}{
			"status":    contractStatus,
			"result":    contractResult,
			"completed": now,
			"attempts":  1,
			"summary":   invResult.Stdout,
		}

		// Convert SavedFiles to relative paths (always compute, even if empty).
		// REV-245: canonical relativisation keeps artefact paths project-relative
		// when the project root is reached through a symlink or junction.
		relPaths := make([]string, len(invResult.SavedFiles))
		for i, p := range invResult.SavedFiles {
			if rel, err := canonicalProjectRel(projectRoot, p); err == nil {
				relPaths[i] = rel
			} else {
				relPaths[i] = p
			}
		}
		if len(relPaths) > 0 {
			updates["artefact"] = strings.Join(relPaths, ",")
		}

		// BUG-016: When streaming captured files, generate a deterministic GTMS summary
		// instead of using raw adapter narration. Preserve adapter stdout in log field.
		summary := invResult.Stdout
		if len(invResult.SavedFiles) > 0 {
			summary = buildStreamingSummary(invResult.SavedFiles)
			updates["summary"] = summary
			if strings.TrimSpace(invResult.Stdout) != "" {
				updates["log"] = invResult.Stdout
			}
		}

		// If streaming didn't find files, scan output dir for directly-written files
		artifactCount := len(invResult.SavedFiles)
		artifactPaths := relPaths
		if len(invResult.SavedFiles) == 0 && outputDir != "" {
			if scanned := scanOutputDir(projectRoot, outputDir, preInvokeFiles); len(scanned) > 0 {
				updates["artefact"] = strings.Join(scanned, ",")
				artifactCount = len(scanned)
				artifactPaths = scanned
			}
		}

		// ENH-160: Tier 0 built-in adapters carry the artefact path through
		// ArtefactOverride. Without this, BuiltinExecute on the manual
		// path loses the filled manual result file from the handoff
		// contract because SavedFiles is empty and the manual pipeline
		// has no output-dir scan target. Override wins over SavedFiles
		// and the scan fallback because Tier 0 knows its artefact
		// authoritatively.
		if invResult.ArtefactOverride != "" {
			artefactRel := invResult.ArtefactOverride
			if rel, err := canonicalProjectRel(projectRoot, invResult.ArtefactOverride); err == nil {
				artefactRel = rel
			}
			updates["artefact"] = artefactRel
			artifactCount = 1
			artifactPaths = []string{artefactRel}
		}

		// Collect warnings
		var warnings []string

		// BUG-084: cap oversize log payloads to NotesSizeCapBytes and spill the
		// full content to .gtms/logs/{task-id}.log so the result contract YAML
		// stays bounded. ENH-077/ENH-092 user contract.
		if logText, ok := updates["log"].(string); ok && logText != "" {
			truncated, spill, capErr := result.ApplyLogCap(projectRoot, tf.ID, logText)
			updates["log"] = truncated
			if spill != "" {
				updates["notes-spill"] = spill
			}
			if capErr != nil {
				warnings = append(warnings, fmt.Sprintf("log spill write failed: %v", capErr))
			}
		}

		// BUG-084 (sibling cap): apply BUG-075 summary cap so oversize content
		// in the summary field can't bloat the contract. Full payload remains
		// available via log: (and the spill file when oversize).
		if summaryText, ok := updates["summary"].(string); ok && summaryText != "" {
			updates["summary"] = result.CapSummary(summaryText)
		}

		// ENH-130: check result.Update errors at terminal write sites.
		// BUG-209 section 9: Tier 1 has no other record of the outcome, so a run whose
		// terminal write did not land is never a pass, whatever the adapter's exit code,
		// whether the handoff was refused, kept being replaced or could not be written.
		if updateErr := persistTerminal(resultPath, updates, nil); updateErr != nil {
			if blocksReconstruct(updateErr) {
				return nil, failAfterInvocation(projectRoot, tf, updateErr)
			}
			return nil, failAfterInvocation(projectRoot, tf, fmt.Errorf("contract update failed: %w", updateErr))
		}

		if err := task.Move(projectRoot, tf, "complete"); err != nil {
			return nil, fmt.Errorf("moving task file: %w", err)
		}

		// S1: Zero files for commands that expect file output
		if (resolved.Command == "create" || resolved.Command == "automate") && artifactCount == 0 {
			warnings = append(warnings, "Adapter ran successfully but produced 0 files. Expected <gtms-file> delimited output or files written to the output directory.")
		}

		// S2: Empty stdout (only warn if no artifacts were found either)
		if strings.TrimSpace(invResult.Stdout) == "" && artifactCount == 0 {
			warnings = append(warnings, "Adapter produced no output.")
		}

		// S3: Build pipeline records for completed sync tasks
		rc, rcErr := result.Read(resultPath)
		if rcErr == nil {
			wa, pWarn, pErr := buildPipelineRecords(projectRoot, cfg, tf, rc, owner)
			wiringAdapter = wa
			warnings = append(warnings, pWarn...)
			if pErr != nil {
				warnings = append(warnings, fmt.Sprintf("Pipeline record could not be written: %v", pErr))
			}
			// ENH-096: merge adapter-injected warnings from the result contract
			if len(rc.Warnings) > 0 {
				warnings = append(warnings, rc.Warnings...)
			}
		}

		// BUG-055: Surface adapter stderr as warnings on success path
		if stderrWarns := stderrToWarnings(invResult.Stderr); len(stderrWarns) > 0 {
			warnings = append(warnings, stderrWarns...)
		}

		return &InvokeResult{
			TaskID:        tf.ID,
			Adapter:       resolved.Name,
			Mode:          resolved.Mode,
			Branch:        tf.Branch,
			Status:        contractStatus,
			Result:        contractResult, // ENH-130: propagate test outcome
			Summary:       summary,
			Filename:      tf.Filename(),
			ArtifactCount: artifactCount,
			ArtifactPaths: artifactPaths,
			Warnings:      warnings,
			Target:        tf.Target,
		}, nil
	}

	// Non-zero exit code = error (default), or fail (ENH-078 opt-in).
	summary := fmt.Sprintf("Process exited with code %d", invResult.ExitCode)
	if invResult.Stderr != "" {
		summary = fmt.Sprintf("%s: %s", summary, invResult.Stderr)
	}

	// ENH-078 + ENH-130: Tier 1 fail-exit-code mapping. When the adapter
	// declared `fail-exit-codes:` and the process exited with a code in
	// that list, classify as complete+fail (test ran, assertion failed)
	// rather than error (couldn't run).
	contractStatus := "error"
	contractResult := "" // unknown outcome for adapter errors
	if resolved.Tier == 1 && containsInt(resolved.Config.FailExitCodes, invResult.ExitCode) {
		contractStatus = "complete"
		contractResult = "fail"
	}

	// ENH-077: preserve the adapter's stdout + stderr in the result
	// contract's log: field so the pipeline writer can copy it into the
	// committed automation record.
	updates := map[string]interface{}{
		"status":    contractStatus,
		"completed": now,
		"attempts":  1,
		"summary":   summary,
	}
	if contractResult != "" {
		updates["result"] = contractResult
	}
	if logText := buildFailureLog(invResult.Stdout, invResult.Stderr); logText != "" {
		updates["log"] = logText
	}

	// ENH-130: check result.Update errors at terminal write sites.
	var warnings []string

	// BUG-084: cap oversize log payloads to NotesSizeCapBytes and spill the
	// full content to .gtms/logs/{task-id}.log so the result contract YAML
	// stays bounded. ENH-077 user contract.
	if logText, ok := updates["log"].(string); ok && logText != "" {
		truncated, spill, capErr := result.ApplyLogCap(projectRoot, tf.ID, logText)
		updates["log"] = truncated
		if spill != "" {
			updates["notes-spill"] = spill
		}
		if capErr != nil {
			warnings = append(warnings, fmt.Sprintf("log spill write failed: %v", capErr))
		}
	}

	// BUG-084 (sibling cap): apply BUG-075 summary cap. Tier 1 failure builds
	// summary from "Process exited with code N" + stderr; oversize stderr can
	// blow past 100 KB on a single failure if unchecked.
	if summaryText, ok := updates["summary"].(string); ok && summaryText != "" {
		updates["summary"] = result.CapSummary(summaryText)
	}

	// BUG-209 section 9: as on the exit-0 branch above, including for a fail-exit-code
	// run, which would otherwise route to complete/.
	if updateErr := persistTerminal(resultPath, updates, nil); updateErr != nil {
		if blocksReconstruct(updateErr) {
			return nil, failAfterInvocation(projectRoot, tf, updateErr)
		}
		return nil, failAfterInvocation(projectRoot, tf, fmt.Errorf("contract update failed: %w", updateErr))
	}

	// ENH-130: task movement decoupled from test outcome. A clean adapter
	// run with a failing test outcome (fail-exit-code) moves to complete/.
	// Only adapter-execution errors move to error/.
	taskStatus := "error"
	if contractStatus == "complete" {
		taskStatus = "complete"
	}
	tf.Error = summary
	if err := task.Move(projectRoot, tf, taskStatus); err != nil {
		return nil, fmt.Errorf("moving task file: %w", err)
	}

	// BUG-023: Build pipeline records on failure so automation record's
	// last-formal-result is updated to reflect the outcome (not left empty).
	rc, rcErr := result.Read(resultPath)
	if rcErr == nil {
		_, pWarn, pErr := buildPipelineRecords(projectRoot, cfg, tf, rc, owner)
		warnings = append(warnings, pWarn...)
		if pErr != nil {
			warnings = append(warnings, fmt.Sprintf("Pipeline record could not be written: %v", pErr))
		}
	}

	return &InvokeResult{
		TaskID:   tf.ID,
		Adapter:  resolved.Name,
		Mode:     resolved.Mode,
		Branch:   tf.Branch,
		Status:   contractStatus,
		Result:   contractResult, // ENH-130: propagate test outcome
		Summary:  summary,
		Filename: tf.Filename(),
		Warnings: warnings,
		Target:   tf.Target,
	}, nil
}

// asInvocationError reports whether err wraps an *invocationError.
func asInvocationError(err error, target **invocationError) bool {
	return errors.As(err, target)
}

// ENH-188 Option A: the bound remediation line shown under the first diagnostic
// line. It must not demand a result unconditionally (a terminal status: error
// contract without a result is valid), hence "a result when status is complete".
const nonTerminalExecuteRemediation = "A Tier 2 execute adapter must write a terminal status to $GTMS_RESULT_FILE before it exits, and a result when status is complete"

// recordNonTerminalExecuteError implements ENH-188 Option A for the synchronous
// Tier 2 EXECUTE path: an adapter that exited leaving a readable but
// non-terminal result contract (empty / pending / in-progress status) has not
// reported an outcome. Rather than falling through to the exit-code handler --
// which on exit 0 records a false status: complete, result: pass -- GTMS records
// status: error with no result, routes the task to error/, writes a pipeline
// record so the dashboard shows the error, and returns the bound diagnostic.
//
// It is reached only for resolved.Command == "execute" and only from
// handleSyncResult (sync mode). rc is the readable, parsed contract. The handoff
// `summary` carries the first diagnostic line; on a non-zero exit the adapter's
// stderr follows it, so missing-tooling diagnostics stay visible. The second
// (remediation) line is CLI-only, carried on InvokeResult.Remediation.
func recordNonTerminalExecuteError(ctx context.Context, projectRoot string, cfg *config.Config, tf *task.TaskFile, resultPath string, resolved *ResolvedAdapter, invResult *InvocationResult, rc *result.ResultContract, artefactHash string, owner *testcase.Owner) (*InvokeResult, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	// <status> renders the contract status, "empty" when blank (ENH-188).
	statusWord := rc.Status
	if statusWord == "" {
		statusWord = "empty"
	}

	var firstLine string
	if invResult.ExitCode == 0 {
		firstLine = fmt.Sprintf("Execution result was not recorded: the adapter exited 0 without reporting a terminal outcome (result contract status: %s)", statusWord)
	} else {
		firstLine = fmt.Sprintf("Execution result was not recorded: the adapter exited with code %d without reporting a terminal outcome (result contract status: %s)", invResult.ExitCode, statusWord)
	}

	// Handoff summary = first line; on a non-zero exit the stderr follows (as it
	// does on the exit-code error path today) so the missing-tooling diagnostic
	// is preserved (ENH-188; tc-84b2389f).
	summary := firstLine
	if invResult.ExitCode != 0 {
		if stderr := strings.TrimRight(invResult.Stderr, "\n"); stderr != "" {
			summary = firstLine + "\n" + stderr
		}
	}

	updates := map[string]interface{}{
		"status":    "error",
		"result":    "", // ENH-130: no test outcome; clears any the adapter wrote
		"summary":   summary,
		"completed": now,
		"attempts":  1,
	}
	// Preserve the adapter's stdout+stderr in the log so a diagnostic is not lost.
	if logText := buildFailureLog(invResult.Stdout, invResult.Stderr); logText != "" {
		updates["log"] = logText
	}
	// Restore GTMS-owned fields the adapter may have dropped or corrupted when it
	// overwrote the handoff, so the reader overlay can join this error to its
	// framework (mirrors the terminal branch above).
	//
	// REV-264 CODEX-001: restore the trusted tf.Framework whenever it DIFFERS, not
	// only when the adapter left it empty. A non-terminal adapter contract carrying
	// a stale or wrong non-empty framework would otherwise keep that value on the
	// durable error, and isAlreadyPassing -- which filters by framework -- would
	// drop this newest error for the selected framework and fall back to an older
	// pass. Same for target.
	if tf.Framework != "" && rc.Framework != tf.Framework {
		updates["framework"] = tf.Framework
	}
	if rc.Target != tf.Target {
		updates["target"] = tf.Target
	}
	if artefactHash != "" && rc.ArtefactHash != artefactHash {
		updates["artefact-hash"] = artefactHash
	}
	if commit, gErr := gitpkg.HeadCommit(ctx, projectRoot); gErr == nil && commit != rc.GitCommit {
		updates["git-commit"] = commit
	}
	if branch, gErr := gitpkg.CurrentBranch(ctx, projectRoot); gErr == nil && branch != rc.GitBranch {
		updates["git-branch"] = branch
	}
	if dirty, gErr := gitpkg.IsDirty(ctx, projectRoot); gErr == nil && (rc.GitDirty == nil || *rc.GitDirty != dirty) {
		updates["git-dirty"] = dirty
	}
	// CLAUDE-001 / CODEX-003 (REV-262): restore the GTMS-held identity and
	// provenance fields. A `>`-truncated or all-whitespace handoff -- the
	// empty-status case the invoker comment names -- decodes to a zero-valued
	// contract, so result.Update would otherwise write an error document with NO
	// `command: execute`. That breaks result.IsTerminalExecuteContract:
	// isAlreadyPassing (internal/cli/execute.go) would ignore this newest error
	// and skip the test case on the earlier pass -- the false green ENH-188 closes
	// -- and `gtms delete` could not attribute the handoff via
	// TargetIsTestCaseScoped. The same gap hits an adapter that overwrites the
	// handoff with a minimal pending/empty document lacking command. These fields
	// are GTMS-owned, not the adapter's, and match recoverUnusableContract.
	updates["command"] = resolved.Command
	updates["task"] = tf.ID
	updates["adapter"] = resolved.Name
	updates["mode"] = resolved.Mode
	if tf.Created != "" {
		updates["created"] = tf.Created
	}
	if tf.ExecutedBy != "" {
		updates["executed_by"] = tf.ExecutedBy
	}
	if tf.Environment != "" {
		updates["environment"] = tf.Environment
	}

	var warnings []string
	// BUG-084 caps so the contract stays bounded.
	if logText, ok := updates["log"].(string); ok && len(logText) > result.NotesSizeCapBytes {
		truncated, spill, capErr := result.ApplyLogCap(projectRoot, tf.ID, logText)
		updates["log"] = truncated
		if spill != "" {
			updates["notes-spill"] = spill
		}
		if capErr != nil {
			warnings = append(warnings, fmt.Sprintf("log spill write failed: %v", capErr))
		}
	}
	if summaryText, ok := updates["summary"].(string); ok && len(summaryText) > result.SummarySizeCapBytes {
		updates["summary"] = result.CapSummary(summaryText)
	}

	// REV-263 CLAUDE-001 / CODEX-001 fix (b): build the terminal error contract in
	// memory from GTMS-held state. It is the single source of truth for BOTH the
	// Reconstruct fallback below AND the pipeline record -- the pipeline record is
	// NEVER built by re-reading the handoff, so a rejected run can never emit
	// `outcome: pass` from the adapter's stale document (an adapter that wrote
	// `status: pending, result: pass` and then made the file unwritable).
	errRC := &result.ResultContract{
		Task:      tf.ID,
		Command:   resolved.Command,
		Target:    tf.Target,
		Adapter:   resolved.Name,
		Mode:      resolved.Mode,
		Created:   tf.Created,
		Status:    "error",
		Framework: tf.Framework,
		Attempts:  1,
		Completed: now,
	}
	if s, ok := updates["summary"].(string); ok {
		errRC.Summary = s
	}
	if l, ok := updates["log"].(string); ok {
		errRC.Log = l
	}
	if ns, ok := updates["notes-spill"].(string); ok {
		errRC.NotesSpill = ns
	}
	if tf.ExecutedBy != "" {
		errRC.ExecutedBy = tf.ExecutedBy
	}
	if tf.Environment != "" {
		errRC.Environment = tf.Environment
	}
	if artefactHash != "" {
		errRC.ArtefactHash = artefactHash
	}
	stampGitContext(ctx, projectRoot, errRC)

	// Primary write merges the error onto the readable handoff. REV-263
	// CLAUDE-001 / CODEX-001 fix (a): if that fails, the durable handoff is still
	// the adapter's non-terminal document, so fall back to result.Reconstruct --
	// the same safe temp+rename replacement recoverUnusableContract uses, which
	// replaces even a read-only target through the writable parent dir -- so the
	// failed run leaves a durable TERMINAL error whenever the file can be
	// replaced, and isAlreadyPassing sees that error rather than an older pass.
	//
	// BUG-209 section 9: through the shared rule. A refusal or a concurrency error fails
	// the run with no reconstruct and no execution record. A reconstruct refused because
	// the file changed does the same. After an ordinary failure of both writes, the
	// trusted error record below is still written: it is the fail-closed veto ENH-188
	// exists for, and it can only ever record an error.
	updateErr, reconstructErr := persistTerminalDetail(resultPath, updates, errRC)
	if blocksReconstruct(updateErr) {
		return nil, failAfterInvocation(projectRoot, tf, updateErr)
	}
	if blocksReconstruct(reconstructErr) {
		return nil, failAfterInvocation(projectRoot, tf, reconstructErr)
	}
	if updateErr != nil {
		warnings = append(warnings, fmt.Sprintf("contract update failed: %v", updateErr))
		if reconstructErr != nil {
			warnings = append(warnings, fmt.Sprintf("contract reconstruct failed: %v", reconstructErr))
		}
	}

	// CLAUDE-002 / CODEX-001 (REV-262): the task move is unconditional so a
	// readable-but-unwritable handoff never strands the task in in-progress and
	// blocks the next run.
	tf.Error = firstLine
	moveErr := task.Move(projectRoot, tf, "error")

	// REV-263 fix (b) / REV-264 CODEX-002: ALWAYS write the trusted execution
	// record from the in-memory error contract (never re-read the handoff). It
	// carries tf.Target + tf.Framework + outcome error independently of the
	// adapter's possibly-damaged handoff, so it survives even when BOTH handoff
	// writes failed (a read-only target on Windows, where rename cannot replace
	// it). isAlreadyPassing consults this record and fails closed on it, so a
	// rejected run can never be masked by an older pass even with an identity-free
	// leftover handoff. WriteExecuteResultsFile keys on tf.Type.
	_, pWarn, pErr := buildPipelineRecords(projectRoot, cfg, tf, errRC, owner)
	warnings = append(warnings, pWarn...)
	if pErr != nil {
		warnings = append(warnings, fmt.Sprintf("Pipeline record could not be written: %v", pErr))
	}

	// A hard Go error only when GTMS could leave NO durable, attributable error
	// signal: the task could not be routed to error/, OR both handoff writes AND
	// the trusted execution record failed (REV-264: "if that record also cannot
	// be written, return non-zero reporting both failures"). Otherwise return an
	// error-status InvokeResult so the CLI shows the BOUND diagnostic alongside the
	// persistence warnings and still exits non-zero -- not a raw Go error.
	//
	// REV-265 CLAUDE-002 / CODEX-002: both hard-error returns carry the COMPLETE
	// bound diagnostic -- the first line, the remediation, and every persistence
	// cause -- so single and bulk callers show it in full (the owner's eighth
	// approval). firstLine already folds in the exit code, the contract status and
	// (non-zero) the adapter's stderr.
	handoffUnwritten := updateErr != nil && reconstructErr != nil
	if moveErr != nil {
		return nil, fmt.Errorf("%s\n    %s\n    moving task file: %v (contract update: %v; reconstruct: %v; execution record: %v)",
			firstLine, nonTerminalExecuteRemediation, moveErr, updateErr, reconstructErr, pErr)
	}
	if handoffUnwritten && pErr != nil {
		return nil, fmt.Errorf("%s\n    %s\n    could not persist a durable error (contract update failed: %v; reconstruct failed: %v; execution record failed: %v)",
			firstLine, nonTerminalExecuteRemediation, updateErr, reconstructErr, pErr)
	}

	return &InvokeResult{
		TaskID:      tf.ID,
		Adapter:     resolved.Name,
		Mode:        resolved.Mode,
		Branch:      tf.Branch,
		Status:      "error",
		Result:      "",
		Summary:     summary,
		Remediation: nonTerminalExecuteRemediation,
		Filename:    tf.Filename(),
		Warnings:    warnings,
		Target:      tf.Target,
	}, nil
}

// recoverUnusableContract handles the BUG-191 fail-closed path: a synchronous
// Tier 2 adapter finished but left its result handoff unusable (unparseable,
// missing, or unreadable). It reconstructs a valid error handoff from GTMS-held
// trusted state -- never from the adapter's document -- routes the task to
// error/, and returns an error-status result.
//
// It does NOT use result.Update: that function decodes the existing file before
// merging, so it would re-hit the same read/parse error and write nothing. It
// uses result.Reconstruct, which replaces the file outright.
//
// It deliberately does NOT build pipeline records: a run whose contract could
// not be read must not write a wiring record or a favourable execution result.
//
// The return is (result, nil), not a Go error: the CLI layers decide their
// process exit from InvokeResult.Status (execute/create/prime exit non-zero on
// status error; single-target automate renders the error but exits 0). Returning
// a Go error here would change automate's shipped exit contract (tc-376157c6 /
// tc-b65b61cf).
//
// The one exception is a persistence failure of its own -- the handoff rewrite
// or the task move failing. That is returned as a Go error, and the task is
// routed to error/ first either way, so the durable state BUG-191 requires does
// not depend on the rewrite succeeding.
// validationSummary is empty for every caller except gtms create's post-write
// validation branch (REV-164 CODEX-001). When it is set, the create validation
// violations are the primary diagnostic and readErr is the secondary one: both
// are reported to the operator and both are persisted, and the violations are
// also kept under the contract's own validation-error key so the durable record
// keeps the shape BUG-038 specifies.
func recoverUnusableContract(ctx context.Context, projectRoot string, tf *task.TaskFile, resultPath string, resolved *ResolvedAdapter, invResult *InvocationResult, prior result.HandoffState, artefactHash, artefactPath string, readErr error, validationSummary string) (*InvokeResult, error) {
	// BUG-209 section 9: never reconstruct after a safety refusal or a concurrency error,
	// because that could overwrite the newer writer. No record is written.
	if blocksReconstruct(readErr) {
		return nil, failAfterInvocation(projectRoot, tf, readErr)
	}

	// readErr already carries "parsing result contract: ..." for malformed YAML
	// and "reading result contract: ..." for a missing or unreadable file, so
	// the operator- and handoff-facing diagnostic names the failure precisely.
	diag := fmt.Sprintf("adapter result contract unusable: %v", readErr)
	// Fold in the process exit code when the adapter also exited non-zero, so a
	// crash-plus-malformed run names both facts (BUG-191 tc-3940522b).
	if invResult != nil && invResult.ExitCode != 0 {
		diag = fmt.Sprintf("Process exited with code %d; %s", invResult.ExitCode, diag)
	}

	// REV-164 CODEX-001: gtms create's post-write validation branch arrives here
	// with violations already in hand. They lead the operator-facing text, since
	// rejecting the specs is why that branch exists; the unreadable handoff is
	// appended after it. Neither fact replaces the other.
	operatorDiag := diag
	if validationSummary != "" {
		operatorDiag = fmt.Sprintf("%s\n%s", validationSummary, diag)
	}

	// Preserve the adapter's stdout+stderr alongside the read error in the log
	// so a diagnostic (e.g. a missing-tooling message on stderr) is not lost.
	logText := operatorDiag
	if invResult != nil {
		if adapterLog := buildFailureLog(invResult.Stdout, invResult.Stderr); adapterLog != "" {
			logText = fmt.Sprintf("%s\n%s", adapterLog, operatorDiag)
		}
	}

	// BUG-084 caps: keep the reconstructed contract bounded. The diagnostic is
	// normally tiny, so this only bites on a very chatty adapter stderr.
	cappedSummary := result.CapSummary(operatorDiag)
	// The 1 KiB summary cap must not be what drops one of the two diagnostics.
	// A long violation list would push the contract-read failure off the end,
	// so trim the violation list instead and keep it whole in log: and
	// validation-error:. When the read error alone already fills the cap there
	// is no room for both; the newer fact wins and the violations survive in
	// the other two fields.
	if validationSummary != "" && !strings.HasSuffix(cappedSummary, diag) {
		if room := result.SummarySizeCapBytes - len(diag) - len("...\n"); room > 0 {
			head, _ := result.TruncateUTF8(validationSummary, room)
			cappedSummary = head + "...\n" + diag
		} else {
			cappedSummary = result.CapSummary(diag)
		}
	}
	cappedLog := logText
	var notesSpill string
	if len(cappedLog) > result.NotesSizeCapBytes {
		if truncated, spill, capErr := result.ApplyLogCap(projectRoot, tf.ID, cappedLog); capErr == nil {
			cappedLog = truncated
			notesSpill = spill
		} else {
			// If the spill write fails, still bound the inline log defensively.
			cappedLog = cappedLog[:result.NotesSizeCapBytes]
		}
	}

	rc := &result.ResultContract{
		Task:    tf.ID,
		Command: resolved.Command,
		Target:  tf.Target,
		Adapter: resolved.Name,
		Mode:    resolved.Mode,
		Created: tf.Created,
		Status:  "error",
		// Result is intentionally empty: an unusable document has no
		// authoritative outcome, and nothing may be salvaged from it.
		Framework: tf.Framework,
		Summary:   cappedSummary,
		Log:       cappedLog,
		// REV-164 CODEX-001: keep the BUG-038 validation-error key on the
		// durable record. The healthy-handoff path writes it through Update's
		// map merge; reconstruction marshals the struct, so without this field
		// the key would vanish exactly on the combined-failure path.
		ValidationError: result.CapSummary(validationSummary),
		// REV-164 CLAUDE-002 / CODEX-002: attempts is 1 at every other
		// terminal write site (invoker.go 615, 1165, 1323, 1723); a
		// recovered run is one attempt like any other.
		Attempts:  1,
		Completed: time.Now().UTC().Format(time.RFC3339),
	}
	// REV-164 CLAUDE-002 / CODEX-002: executed_by and environment are
	// GTMS-held trusted state, set from --executed-by / --env onto the task
	// file long before the adapter ran (invoker.go 278-279). Replacing the
	// handoff document wholesale would otherwise drop them, so the dashboard
	// row for this run would lose the operator and environment exactly when
	// an infrastructure failure makes them most useful. Nothing here comes
	// from the adapter's unusable document.
	if tf.ExecutedBy != "" {
		rc.ExecutedBy = tf.ExecutedBy
	}
	if tf.Environment != "" {
		rc.Environment = tf.Environment
	}
	// Re-stamp Git context from the repository, matching the normal terminal
	// Tier 2 path. GTMS stamped these at contract creation (invoker.go 336)
	// and owns them outright; the environment, not the adapter, is the source.
	stampGitContext(ctx, projectRoot, rc)
	if notesSpill != "" {
		rc.NotesSpill = notesSpill
	}
	if artefactHash != "" {
		rc.ArtefactHash = artefactHash
	}
	// artefactPath is GTMS-held trusted state (ac.ArtefactFile). Normalise an
	// absolute path to a project-relative forward-slash form so the dashboard
	// overlay join (resolveOverlayFramework) matches the wiring artefact path.
	if artefactPath != "" {
		rc.Artefact = artefactPath
		if filepath.IsAbs(artefactPath) {
			if rel, relErr := filepath.Rel(projectRoot, artefactPath); relErr == nil && !strings.HasPrefix(rel, "..") {
				rc.Artefact = filepath.ToSlash(rel)
			}
		}
	}

	// BUG-209 section 9: only over the file that was found unusable.
	reconstructErr := persistReconstruct(resultPath, rc, prior)

	// REV-164 CLAUDE-003 / CODEX-001: route the task to error/ even when the
	// handoff rewrite failed. BUG-191's Expected Behavior requires the task to
	// land in error/, and that must not be conditional on the rewrite
	// succeeding -- otherwise a read-only .gtms/results or a directory squatting
	// on the handoff path strands the task in in-progress/, where it also blocks
	// the next run as a duplicate active task.
	tf.Error = operatorDiag
	moveErr := task.Move(projectRoot, tf, "error")

	if reconstructErr != nil {
		if moveErr != nil {
			return nil, fmt.Errorf("reconstructing result contract: %w (and moving task file: %v)", reconstructErr, moveErr)
		}
		return nil, fmt.Errorf("reconstructing result contract: %w", reconstructErr)
	}
	if moveErr != nil {
		return nil, fmt.Errorf("moving task file: %w", moveErr)
	}

	return &InvokeResult{
		TaskID:   tf.ID,
		Adapter:  resolved.Name,
		Mode:     resolved.Mode,
		Branch:   tf.Branch,
		Status:   "error",
		Summary:  cappedSummary,
		Filename: tf.Filename(),
		Target:   tf.Target,
	}, nil
}

// effectiveTimeoutStr returns the human-readable timeout duration used for
// error messages. If the adapter has an explicit timeout: configured, that
// string is returned as-is. Otherwise the default sync timeout is formatted.
func effectiveTimeoutStr(resolved *ResolvedAdapter) string {
	if resolved.Config.Timeout != "" {
		return resolved.Config.Timeout
	}
	return effectiveDefaultSyncTimeout().String()
}

// containsInt reports whether the integer needle is present in haystack.
// Used by handleSyncResult for the ENH-078 Tier 1 fail-exit-code check.
func containsInt(haystack []int, needle int) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// stampGitContext fills rc.GitCommit, rc.GitBranch, and rc.GitDirty from
// the project's git state. CON-023 / ENH-146 requires these on every
// execute result for the reader overlay. Each helper is best-effort:
// when git is unavailable or HEAD is detached, the corresponding field
// is left unset (omitempty drops the key). GitDirty stays nil when
// unavailable so the YAML round-trip can distinguish "unavailable" from
// "clean" (PRP Task 3 *bool foot-gun).
func stampGitContext(ctx context.Context, projectRoot string, rc *result.ResultContract) {
	if commit, err := gitpkg.HeadCommit(ctx, projectRoot); err == nil {
		rc.GitCommit = commit
	}
	if branch, err := gitpkg.CurrentBranch(ctx, projectRoot); err == nil {
		rc.GitBranch = branch
	}
	if dirty, err := gitpkg.IsDirty(ctx, projectRoot); err == nil {
		rc.GitDirty = &dirty
	}
}

// buildPipelineRecords dispatches the post-invocation pipeline write based
// on command type. Returns the wiring execute adapter (automate only, "" for
// execute or when no wiring was written), any warnings (typically the
// canonical execute-adapter fallback diagnostic from WriteAutomateWiring),
// and any error so the caller can surface all three.
//
// CON-023 / ENH-145 / ENH-146 (cutover complete):
//   - automate: WriteAutomateWiring writes the six-field wiring file at
//     gtms/automation/wiring/<tc>--<framework>.wiring.yaml. The legacy
//     gtms/automation/records/*.automation.md path is retired.
//   - execute:  WriteExecuteResultsFile writes the per-test results file
//     at gtms/execution/<task>--<tc>.results.yaml (ADR-020 / CON-016).
//     Wiring is immutable on the execute path; the result contract under
//     .gtms/results/ is the canonical store of the test outcome.
func buildPipelineRecords(projectRoot string, cfg *config.Config, tf *task.TaskFile, rc *result.ResultContract, owner *testcase.Owner) (string, []string, error) {
	switch tf.Type {
	case "automate":
		// ENH-191: return the wiring's execute adapter so the sync CLI path
		// (handleSyncResult -> formatAutomateOutput) can emit the verbose
		// "Execute adapter for wiring: <name>" report. The async completion
		// path (status_common.go) and gtms link call WriteAutomateWiring
		// directly and emit their own report.
		return WriteAutomateWiring(projectRoot, cfg, tf, rc, owner)
	case "execute":
		return "", nil, WriteExecuteResultsFile(projectRoot, tf, rc)
	}
	return "", nil, nil
}

// deriveOutputSubdir extracts the subdirectory path from a test case source path.
// For "gtms/test/cases/cwd-scoping/tc-abc.md" it returns "cwd-scoping/".
// For "gtms/test/cases/tc-abc.md" (root level) or empty input it returns "".
func deriveOutputSubdir(testCaseSourcePath string) string {
	if testCaseSourcePath == "" {
		return ""
	}
	// Strip the cases directory prefix (ENH-093: routed through layout package)
	paths := layout.Current()
	trimmed := strings.TrimPrefix(testCaseSourcePath, paths.TestCases+"/")
	// Get the directory portion
	dir := filepath.Dir(trimmed)
	// Convert to forward slashes (Windows compatibility)
	dir = filepath.ToSlash(dir)
	// "." means root level -- no subdirectory
	if dir == "." {
		return ""
	}
	return dir + "/"
}

// ownerSource returns the project-relative path of the spec the command's
// orchestration already PROVED owns the target, or "" when there is no owner.
//
// ADR-024 s5: the adapter layer receives the resolved owner and reads it. It does
// not resolve the bare id. That is not an optimisation -- handing a bare id to
// the adapter layer is what let a filename merely prefixing the id
// (tc-77777777--legacy-sibling.md for tc-77777777) be selected as an adapter's
// source, the defect REV-156 recorded as keeping BUG-188 alive for five rounds.
// With the owner threaded there is no id here to mis-resolve.
func ownerSource(owner *testcase.Owner) string {
	if owner == nil {
		return ""
	}
	return owner.SpecRel()
}

// readGuides reads all .md files from the given guide directory, wrapping each
// in XML <guide name="..."> tags for clear semantic boundaries. Returns empty
// string if the directory does not exist. Only returns an error on actual read
// failures.
//
// ENH-164: when guideDir is empty (adapter has no explicit guide-dir
// configured), default to layout.Current().TestGuides (gtms/test/guides by
// default). Explicit guide-dir in adapter config continues to override.
func readGuides(projectRoot, guideDir string) (string, error) {
	if guideDir == "" {
		guideDir = layout.Current().TestGuides
	}

	absDir := guideDir
	if !filepath.IsAbs(guideDir) {
		absDir = filepath.Join(projectRoot, guideDir)
	}

	entries, err := os.ReadDir(absDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("reading guide directory %s: %w", guideDir, err)
	}

	var parts []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(absDir, entry.Name()))
		if err != nil {
			return "", fmt.Errorf("reading guide file %s: %w", entry.Name(), err)
		}
		tagged := fmt.Sprintf("<guide name=%q>\n%s\n</guide>", entry.Name(), strings.TrimSpace(string(content)))
		parts = append(parts, tagged)
	}

	return strings.Join(parts, "\n\n"), nil
}

// snapshotDir captures the set of file names in a directory before adapter invocation.
// Returns nil if the directory doesn't exist or can't be read.
func snapshotDir(dir string) map[string]struct{} {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	m := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			m[e.Name()] = struct{}{}
		}
	}
	return m
}

// scanOutputDir returns relative paths of NEW files in outputDir that were not in the exclude set.
// Skips directories, hidden files (starting with .), and files present in exclude.
// Returns nil if no new files are found.
func scanOutputDir(projectRoot, outputDir string, exclude map[string]struct{}) []string {
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return nil
	}

	var paths []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if exclude != nil {
			if _, exists := exclude[name]; exists {
				continue
			}
		}
		absPath := filepath.Join(outputDir, name)
		// REV-245: canonical relativisation so a direct write under a linked-project
		// output dir is recorded as a normal project-relative path, not "../".
		if rel, relErr := canonicalProjectRel(projectRoot, absPath); relErr == nil {
			paths = append(paths, rel)
		}
	}

	if len(paths) == 0 {
		return nil
	}
	return paths
}

// rejectMultiFileAutomate handles the ENH-080 guard: on a sync `automate`
// invocation, if the streaming writer captured more than one <gtms-file>
// tag, fail the task without writing an automation record. The streamed
// files remain on disk (mirrors existing failure semantics -- preserves
// evidence for the user). No pipeline record is written -- that's the whole
// point; we refuse to emit a comma-separated artefact: field.
//
// Returns an InvokeResult shaped like any other adapter failure so the CLI
// surface is identical to other error paths.
func rejectMultiFileAutomate(projectRoot string, tf *task.TaskFile, resultPath string, resolved *ResolvedAdapter, invResult *InvocationResult) (*InvokeResult, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	summary := multiFileAutomateSummary(invResult.SavedFiles)

	// BUG-209 section 9: surface a handoff-write failure as a warning, keeping the
	// rejection, rather than discarding it.
	var warnings []string
	if updErr := persistTerminal(resultPath, map[string]interface{}{
		"status":    "error",
		"completed": now,
		"attempts":  1,
		"summary":   summary,
	}, nil); updErr != nil {
		if blocksReconstruct(updErr) {
			return nil, failAfterInvocation(projectRoot, tf, updErr)
		}
		warnings = append(warnings, fmt.Sprintf("contract update failed: %v", updErr))
	}

	tf.Error = summary
	if err := task.Move(projectRoot, tf, "error"); err != nil {
		return nil, fmt.Errorf("moving task file: %w", err)
	}

	// Deliberately do NOT call buildPipelineRecords here -- the whole point of
	// ENH-080 is to prevent an automation record from being written when the
	// adapter emitted multiple files for a single automate TC.

	return &InvokeResult{
		TaskID:   tf.ID,
		Adapter:  resolved.Name,
		Mode:     resolved.Mode,
		Branch:   tf.Branch,
		Status:   "error",
		Summary:  summary,
		Filename: tf.Filename(),
		Target:   tf.Target,
		Warnings: warnings,
	}, nil
}

// multiFileAutomateSummary builds the human-readable error summary for the
// ENH-080 guard. Format:
//
//	automate emitted N files but exactly one is expected; captured: a, b, c
//
// Truncates at 5 displayed filenames (same shape as buildStreamingSummary).
func multiFileAutomateSummary(savedFiles []string) string {
	count := len(savedFiles)
	names := make([]string, count)
	for i, p := range savedFiles {
		names[i] = filepath.Base(p)
	}

	const maxDisplay = 5
	suffix := ""
	displayed := names
	if count > maxDisplay {
		displayed = names[:maxDisplay]
		suffix = fmt.Sprintf(", ... (%d more)", count-maxDisplay)
	}

	return fmt.Sprintf(
		"automate emitted %d files but exactly one is expected; captured: %s%s",
		count, strings.Join(displayed, ", "), suffix,
	)
}

// invokeBuiltinAction dispatches to the appropriate Tier 0 action adapter
// based on the command. Returns an InvocationResult that flows through the
// same handleSyncResult path as Tier 1 and Tier 2 results.
//
// cfg is threaded through for BuiltinAutomate, which needs it to resolve the
// canonical execute adapter for the wiring record (ENH-151).
func invokeBuiltinAction(ac *AdapterContext, resolved *ResolvedAdapter, cfg *config.Config) (*InvocationResult, error) {
	switch resolved.Command {
	case "create":
		return BuiltinCreate(ac)
	case "automate":
		return BuiltinAutomate(ac, cfg)
	case "prime":
		return BuiltinPrime(ac)
	case "execute":
		return BuiltinExecute(ac)
	default:
		return nil, fmt.Errorf("built-in adapter not available for command '%s'", resolved.Command)
	}
}

// buildStreamingSummary generates a deterministic summary from files captured via
// streaming. This replaces raw adapter narration (which may be misleading) with a
// factual description of what GTMS captured. See BUG-016.
func buildStreamingSummary(savedFiles []string) string {
	count := len(savedFiles)
	if count == 0 {
		return ""
	}

	// Extract basenames
	names := make([]string, count)
	for i, p := range savedFiles {
		names[i] = filepath.Base(p)
	}

	// Truncate if more than 5 files
	const maxDisplay = 5
	suffix := ""
	displayed := names
	if count > maxDisplay {
		displayed = names[:maxDisplay]
		suffix = fmt.Sprintf(", ... (%d more)", count-maxDisplay)
	}

	return fmt.Sprintf("Captured %d file(s): %s%s", count, strings.Join(displayed, ", "), suffix)
}

// buildFailureLog assembles a diagnostic log string from a Tier 1 / Tier 2-
// fallback adapter's captured stdout and stderr for ENH-077 post-mortem
// surfacing. Returns the empty string when both inputs are blank so the
// caller can skip writing a `log:` key in that case.
//
// When both streams carry content, stderr leads -- most test frameworks
// emit the error line to stderr and the verbose trace to stdout, so
// leading with stderr puts the assertion message at the top of the block.
func buildFailureLog(stdout, stderr string) string {
	stdout = strings.TrimRight(stdout, "\n")
	stderr = strings.TrimRight(stderr, "\n")
	if stdout == "" && stderr == "" {
		return ""
	}
	if stderr != "" && stdout != "" {
		return "stderr:\n" + stderr + "\n\nstdout:\n" + stdout
	}
	if stderr != "" {
		return stderr
	}
	return stdout
}

// stderrToWarnings converts captured adapter stderr into a slice of warning
// strings suitable for injection into InvokeResult.Warnings. It splits on
// newlines, trims whitespace from each line, and discards empty lines.
// Returns nil when stderr is empty or contains only whitespace, so callers
// can skip the append without a length check.
//
// BUG-055: adapter stderr was silently dropped on the success path. This
// helper is called from both success branches of handleSyncResult to surface
// the captured content as structured warnings the CLI already displays.
func stderrToWarnings(stderr string) []string {
	trimmed := strings.TrimSpace(stderr)
	if trimmed == "" {
		return nil
	}
	lines := strings.Split(trimmed, "\n")
	var result []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			result = append(result, line)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// taskBranch determines the branch name to record for a task (BUG-056).
//
// For sync adapters (mode: "sync"), the adapter runs in the working tree and
// does not create or check out a branch. The real current branch is recorded
// as useful audit context. If git cannot determine the branch (e.g. detached
// HEAD, non-git directory), an empty string is returned -- never a fake
// "feature/" name.
//
// For async adapters (mode: "async"), the adapter may work on a separate
// feature branch, so the constructed "feature/{command}-{target}" name is
// returned (preserving the original behavior).
func taskBranch(ctx context.Context, projectRoot string, resolved *ResolvedAdapter, target string) string {
	if resolved.Mode == "sync" {
		branch, err := gitpkg.CurrentBranch(ctx, projectRoot)
		if err != nil {
			return ""
		}
		return branch
	}
	return fmt.Sprintf("feature/%s-%s", resolved.Command, sanitizeBranchTarget(target))
}

// sanitizeBranchTarget cleans a target string for use in a git branch name.
// It replaces path separators with hyphens and strips file extensions.
func sanitizeBranchTarget(target string) string {
	// Strip file extension (e.g. ".md", ".yaml")
	ext := filepath.Ext(target)
	if ext != "" {
		target = target[:len(target)-len(ext)]
	}
	// Replace path separators with hyphens
	target = strings.ReplaceAll(target, "/", "-")
	target = strings.ReplaceAll(target, "\\", "-")
	return target
}

// removeArtefactFile is the single seam through which the automate lifecycle
// deletes a file. It exists so a test can observe WHICH PATHS an implementation
// chose to destroy, rather than only which files happened to survive: "the
// foreign artefact still exists" is satisfied by an implementation that removed
// and faithfully recreated it, and by one that never touched it, and those are
// not the same program. Production code assigns nothing to it.
var removeArtefactFile = os.Remove

// cleanupExistingOutputForOwner removes the target identity's own stale output
// from dir before a --force re-run regenerates it (BUG-031). Only files are
// removed; directories are left intact.
//
// AUTHORITY IS WIRING, AND ONLY WIRING. A file is removed if and only if a
// wiring record whose parsed body `testcase` is the target claims it (ADR-024
// s1) AND no other identity claims it too. The foreign veto is consulted FIRST,
// so a file two records both name is never removed on the strength of one of
// them; the sweep plans every file before removing any, so that refusal takes
// the whole sweep with it rather than leaving a half-swept directory. There is no basename rule of any kind, because there cannot be one: the
// TC-ID grammar admits single dashes, so "tc-aaaa-bbbb-checkout.bats" is
// indistinguishable by inspection from tc-aaaa's own output and from
// tc-aaaa-bbbb's, and the prefix rule that used to decide it deleted the wrong
// identity's file. Claims are matched by PHYSICAL FILE IDENTITY (os.SameFile),
// so an alias or a differently-cased spelling of a claimed path is still that
// claim's file.
//
// The target's CURRENT artefact needs no separate rule: it is the planned path,
// the adapter overwrites it in place, and if a previous run produced it then the
// target's own wiring already claims it.
//
// Fails CLOSED: the claim set it is handed was validated when the operation
// built it -- an unreadable or unparseable wiring record refuses the run there,
// before this is ever reached -- and any file here that cannot be examined
// refuses the sweep rather than being assumed unclaimed. The set is the
// operation's live state, so a file an EARLIER item in this same batch claimed is
// already visible to it.
//
// ADAPTER-BOUNDARY LIMITATION -- known, documented, and deliberately NOT worked
// around here. A Tier 1/2 adapter chooses its own output paths and GTMS learns
// them only after the write, so the planned-path collision refusal that guards
// the Tier 0 path has nothing to check in advance: GTMS cannot prevent an
// external adapter overwriting a file another identity claims.
//
// What it CAN do, and does: the sweep above never deletes such a file, and the
// resulting state -- two identities' wiring disagreeing about one artefact --
// surfaces afterwards as a refusal rather than as silent data loss. A refusal
// that makes an operator choose is strictly safer than a rule that picks a
// claimant and destroys the other's data, which is the failure this whole change
// exists to remove.
//
// Closing it properly needs an output RESERVATION contract with external
// adapters -- GTMS hands out an authorised destination and the adapter agrees to
// write only there -- not another special case in cleanup. Any special case here
// would have to guess which files an unknown adapter considers its own, i.e. go
// back to inferring ownership from names.
func cleanupExistingOutputForOwner(claims *ArtefactClaimSet, dir, target string) (retained []string, err error) {
	if dir == "" || target == "" || claims == nil {
		return nil, nil
	}

	// PLAN, then execute. Nothing is removed until every file has been
	// classified, because the refusal below has to be ATOMIC: a contested file
	// discovered halfway through a walk that has already deleted three others is
	// not a refusal, it is a partial sweep with an error message.
	var refusal error
	var toRemove []string
	walkErr := filepath.Walk(dir, func(path string, info os.FileInfo, walkFileErr error) error {
		if walkFileErr != nil {
			return nil // skip unreadable entries
		}
		if info.IsDir() {
			return nil
		}

		// The FOREIGN VETO IS ASKED FIRST, before the target's own claim. Order
		// is the whole finding: asking "is this mine?" first and deleting on yes
		// means a file claimed by BOTH identities is destroyed without the other
		// claim ever being consulted -- the one case where two records actively
		// disagree, resolved silently in favour of whoever happened to run.
		holder, _, foreign, holderErr := claims.foreignHolder(target, path)
		if holderErr != nil {
			refusal = holderErr
			return filepath.SkipAll
		}
		owned, ownErr := claims.claimedByOwner(target, path)
		if ownErr != nil {
			refusal = ownErr
			return filepath.SkipAll
		}

		switch {
		case foreign && owned:
			// Contested: two records name one file. Not adjudicable here.
			refusal = &ContestedArtefactError{
				Owner:    target,
				Claimant: holder,
				Artefact: claims.display(path),
			}
			return filepath.SkipAll
		case foreign:
			// Another identity's artefact. Never removed; reported when it is a
			// name this target could have produced, since that is the file the
			// operator expected the sweep to take.
			if IsOwnerDerivedArtefactName(filepath.Base(path), target) {
				retained = append(retained, PreservedArtefactWarning(claims.display(path), target, holder))
			}
			return nil
		case owned:
			toRemove = append(toRemove, path)
			return nil
		}

		// REPORTING ONLY, and the distinction is the whole point. Basename
		// resemblance decided what to DELETE before this change, and that is
		// exactly what was retired: it cannot authorise destruction. It can
		// still decide what is worth MENTIONING, because mentioning a file
		// destroys nothing and the operator's question is precisely "why is the
		// file I named still here?". Nothing below removes anything.
		//
		// The scope is the target's OWN naming rule, not a generic prefix: only a
		// file this identity could itself have produced was ever a cleanup
		// candidate, so only such a file can leave the operator wondering why it
		// survived. Listing every unrelated file in the output directory would be
		// noise, and noise is how a real warning gets ignored.
		if IsOwnerDerivedArtefactName(filepath.Base(path), target) {
			retained = append(retained, PreservedArtefactWarning(claims.display(path), target, ""))
		}
		return nil
	})
	if refusal != nil {
		return nil, refusal
	}
	if walkErr != nil {
		return nil, walkErr
	}

	// Execute. Reached only when the whole directory classified cleanly.
	for _, path := range toRemove {
		_ = removeArtefactFile(path)
	}
	return retained, nil
}

// quarantineFailure records a rejected spec whose move failed: it is still at path, and
// err explains why the quarantine could not complete (REV-209 CODEX-002/CLAUDE-004).
type quarantineFailure struct {
	path string
	err  error
}

// projectRelSlash renders abs as a project-relative, forward-slash path (the stable form
// used in every create-failure diagnostic). Falls back to the forward-slashed absolute
// path if it cannot be made relative.
func projectRelSlash(projectRoot, abs string) string {
	if rel, err := filepath.Rel(projectRoot, abs); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(abs)
}

// canonicalProjectRel renders abs as a stable, project-relative forward-slash path that
// stays inside the project even when the project root is reached through a symlink or
// junction (REV-245; BUG-214 Owner Decision 13 / AC16).
//
// BUG-214 made create's ctx.OutputDir the symlink-resolved (canonical) directory, so the
// artefact files written under it are canonical while the invocation's projectRoot is
// still the uncanonicalised link form (filepath.Abs of the cwd). A plain
// filepath.Rel(projectRoot, canonicalFile) then yields "../project/..." for a
// linked-project create.
//
// The algorithm (REV-245 delta round 2, D2-CLAUDE-001 / D2-CODEX-001):
//
//  1. Try the plain lexical filepath.Rel(projectRoot, abs). If it succeeds and the result
//     is neither absolute nor "..."-escaping, return it UNCHANGED. Every path today --
//     automate included, and in particular an output-dir that runs through an in-project
//     symlinked folder -- is preserved exactly, so the BuiltinAutomate pending-wiring
//     comparison (plain filepath.Rel at builtin_action.go) still matches and AC15 / Owner
//     Decision 11 hold.
//  2. Only when that fails or escapes (the linked-project-root case) reconcile the two
//     spellings of the root: resolve the root with pathsafe.CanonicalRoot, resolve ONLY
//     the artefact's PARENT directory with EvalSymlinks, re-attach the original base name,
//     and relativise. The final component is never resolved, so a symlink artefact in the
//     output dir is recorded as the entry that was created, not its target -- an in-project
//     target is not misidentified and an external target cannot produce a "../" path (AC16).
//
// The Rel error from the fallback is returned unchanged so callers keep their existing
// fallback behaviour.
func canonicalProjectRel(projectRoot, abs string) (string, error) {
	// REV-245 D3-CODEX-001: a real parent-traversal test, not a HasPrefix(rel, "..").
	// A valid artefact whose name merely begins with two dots (e.g. "..link-specs/x")
	// is NOT an escape and must take the plain lexical branch unchanged.
	if rel, err := filepath.Rel(projectRoot, abs); err == nil &&
		!filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(rel), nil
	}

	// Fallback: reconcile only the root spelling, never the artefact leaf.
	root := pathsafe.CanonicalRoot(projectRoot)
	parent := filepath.Dir(abs)
	if resolvedParent, err := filepath.EvalSymlinks(parent); err == nil {
		parent = resolvedParent
	}
	file := filepath.Join(parent, filepath.Base(abs))
	rel, err := filepath.Rel(root, file)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

// ownedByBatch reports whether a rejected spec's canonical filename ID is one of the IDs
// GTMS generated for this invocation (REV-209 CODEX-001/CLAUDE-001). Only a valid-shape
// filename (tc-{8hex}.md or tc-{8hex}-slug.md) yields a canonical ID; a malformed filename
// has none and is therefore never attributable, so it is never moved.
func ownedByBatch(file string, batchSet map[string]struct{}) bool {
	m := validShapePattern.FindStringSubmatch(file)
	if m == nil {
		return false
	}
	_, ok := batchSet[m[1]]
	return ok
}

// quarantineRejectedSpec moves one rejected spec out of the cases tree into the task's
// error artefacts folder (gtms/tasks/error/<task-id>/artifacts/<original-filename>),
// byte-for-byte (BUG-203 Owner Decision 4). file is a basename inside outputDir. A rename
// is preferred; a cross-device rename falls back to copy+remove.
//
// REV-209 CODEX-002/CLAUDE-004 (honest reporting): every step is checked (MkdirAll,
// rename, copy, source removal), and the quarantine is only reported as successful once
// the artefact exists AND the source is gone. It returns (destRel, true, nil) on a proven
// quarantine, or ("", false, err) when the move did not complete -- the caller then
// reports the file's actual (unchanged) location, never claiming preservation.
// REV-209 R3-CODEX-003 (partial): src and dest are the paths pathsafe.ResolveUnderRoot
// returned from the caller's containment check, and the move is performed on those checked
// resolved paths rather than re-deriving lexical ones. Fully eliminating the remaining
// parent-swap race (a directory component swapped for an escaping symlink BETWEEN the check
// and the rename/copy) would require handle-relative filesystem operations (openat/renameat
// with O_NOFOLLOW, or an os.Root-style rooted FS), which Go's stdlib does not portably
// expose here; that residual race is outside GTMS's local-user threat model and is
// documented as a dismissed remainder in REV-209.
func quarantineRejectedSpec(projectRoot, src, dest string) (string, bool, error) {
	if fi, err := os.Lstat(src); err != nil {
		return "", false, err // already gone or unreadable -- nothing to preserve
	} else if !fi.Mode().IsRegular() {
		// Defence in depth: the caller already rejected non-regular sources; never move one.
		return "", false, fmt.Errorf("source is not a regular file")
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", false, err
	}
	if err := os.Rename(src, dest); err != nil {
		// Cross-device or other rename failure: copy the bytes, then remove the source.
		data, rerr := os.ReadFile(src)
		if rerr != nil {
			return "", false, rerr
		}
		if werr := os.WriteFile(dest, data, 0o644); werr != nil {
			return "", false, werr
		}
		if rmErr := os.Remove(src); rmErr != nil {
			// Bytes were copied but the source could not be removed: NOT quarantined --
			// the invalid spec is still in the cases tree.
			return "", false, fmt.Errorf("copied to %s but could not remove the source: %w", filepath.ToSlash(dest), rmErr)
		}
	}
	// Prove the quarantine: the artefact must exist and the source must be gone.
	if _, err := os.Stat(dest); err != nil {
		return "", false, fmt.Errorf("artefact missing after move: %w", err)
	}
	if _, err := os.Stat(src); err == nil {
		return "", false, fmt.Errorf("source still present after move")
	} else if !os.IsNotExist(err) {
		return "", false, err
	}
	// REV-209 R4-CLAUDE-001 / R4-CODEX-002: report dest relative to the RESOLVED project
	// root. ResolveUnderRoot resolves both the (now existing) artefact and the root (all
	// symlinks followed, Windows 8.3 short names expanded), so the relative path is
	// layout-relative whichever spelling dest or projectRoot uses. Relativising a resolved
	// path against the unresolved root produced a spurious ../private/... or absolute path
	// in the "preserved at" line.
	if _, rel, err := pathsafe.ResolveUnderRoot(projectRoot, dest); err == nil {
		return rel, true, nil
	}
	return projectRelSlash(pathsafe.CanonicalRoot(projectRoot), dest), true, nil
}

// quarantineCreateFailureSummary builds the error summary for a create whose output
// failed validation under the quarantine + ownership contract (BUG-203 AC10/AC11): the
// fixed headline, one line per quarantined artefact, one line per unattributable file
// left in place (with the fixed inspection guidance), one line per failed move (reporting
// the file's actual location), then the established FormatValidationErrors detail block
// (kept verbatim so the "spec validation failed" preamble and the per-file reasons still
// surface for every validation failure). The AC6 producer hint is appended by the caller.
// REV-209 R3-CLAUDE-001: every path printed here is adapter-controlled (the filename the
// adapter wrote), so it is rendered through testcase.SanitizeIdentityString -- matching the
// FormatValidationErrors block below -- so a filename carrying ANSI escapes or other control
// characters (legal on Linux) cannot reach the terminal, the task Error field or the handoff
// summary unsanitised. The raw paths are used for the filesystem operations in the caller;
// only the RENDERING is sanitised.
func quarantineCreateFailureSummary(violations []SpecValidationError, preserved, unattributable, notRegular, pathUnsafe []string, moveFailed []quarantineFailure) string {
	var b strings.Builder
	b.WriteString("Created spec failed validation.")
	for _, p := range preserved {
		fmt.Fprintf(&b, "\n    Rejected output preserved at: %s", testcase.SanitizeIdentityString(p))
	}
	for _, p := range unattributable {
		fmt.Fprintf(&b, "\n    Rejected output was left in place because GTMS could not prove this create owns it: %s. Inspect concurrent create activity, then repair or remove the file if appropriate.", testcase.SanitizeIdentityString(p))
	}
	for _, p := range notRegular {
		fmt.Fprintf(&b, "\n    Rejected output was left in place because it is not a regular file: %s.", testcase.SanitizeIdentityString(p))
	}
	for _, p := range pathUnsafe {
		fmt.Fprintf(&b, "\n    Rejected output was left in place because its path is outside the project or cannot be safely resolved: %s.", testcase.SanitizeIdentityString(p))
	}
	for _, mf := range moveFailed {
		reason := ""
		if mf.err != nil {
			reason = " (" + testcase.SanitizeIdentityString(mf.err.Error()) + ")"
		}
		fmt.Fprintf(&b, "\n    Rejected output could not be preserved and remains at: %s%s", testcase.SanitizeIdentityString(mf.path), reason)
	}
	if detail := FormatValidationErrors(violations); detail != "" {
		b.WriteString("\n")
		b.WriteString(detail)
	}
	return b.String()
}

// staleHeaderSignatures are the SpecValidationError reason substrings that mark a
// pre-v2 header a stale v0.3.x scaffolded producer emitted (BUG-203 AC6). They are the
// rendered forms of the testcase schema-discriminator failures (internal/testcase
// SchemaFailureMissingSchemaDiscriminator / MalformedSchemaDiscriminator /
// UnsupportedSchemaVersion / NoFrontmatterBlock). Other create validation failures
// (duplicate / mismatched / invented id) are NOT refreshable via gtms init --force and
// get no hint.
var staleHeaderSignatures = []string{
	"missing schema discriminator",
	"malformed or empty schema discriminator",
	"unsupported schema version",
	"no frontmatter block",
}

// shippedPresetProducerSuffixes are the create producers that gtms init --force --preset
// upgrades, keyed by their path BELOW the GTMS parent folder (the Tier 0 built-in's role
// testcase templates and the standard scaffolded create scripts). A producer is recognised
// only when its first path segment is the project's ACTIVE parent folder (layout.ParentDir),
// so a renamed-parent project (ENH-098, e.g. testing/test/templates/...) is still recognised
// as shipped, while an unrelated folder that happens to hold a same-named file is not
// (REV-209 R4-CODEX-002).
var shippedPresetProducerSuffixes = map[string]struct{}{
	"test/templates/manual-testcase.template.md": {},
	"test/templates/agent-testcase.template.md":  {},
	"adapters/manual-create-script.sh":           {},
	"adapters/agent-create-script.sh":            {},
}

// shippedDemoCreateProducer is the demo create adapter, refreshed by gtms init --force --demo.
const shippedDemoCreateProducer = "_demo/adapters/create-demo.sh"

// presetRefreshSentence is the exact actionable command for a non-demo shipped producer.
// Plain `gtms init --force` is a no-op in an existing project (it lists presets and exits),
// so the hint must name the preset form (REV-209 R2-CLAUDE-001 / R2-CODEX-002). ASCII `--`.
const presetRefreshSentence = "Run gtms init --force --preset <preset>, replacing <preset> with manual, bats, or playwright -- use the preset originally used for this project."

// staleScaffoldProducerHint returns a one-line hint for a create whose output failed with a
// stale pre-v2 header (BUG-203 AC6), or "" when the failure is not a stale-header failure.
//
// REV-209 R2-CODEX-003 (correct attribution): scaffold-refresh guidance is offered ONLY
// when the producer is an exact shipped, upgradeable source -- the Tier 0 built-in's role
// template (attributed to the built-in only), the standard scaffolded create scripts, or
// the demo create script. For any other command or script adapter (custom code), the hint
// names the adapter and its script path and tells the user to fix the v2 header it emits;
// it never blames a scaffold template and never suggests init can repair custom code.
func staleScaffoldProducerHint(resolved *ResolvedAdapter, ac *AdapterContext, projectRoot string, violations []SpecValidationError) string {
	stale := false
	for _, v := range violations {
		for _, sig := range staleHeaderSignatures {
			if strings.Contains(v.Reason, sig) {
				stale = true
			}
		}
	}
	if !stale {
		return ""
	}

	// Resolve the producer. The role template is attributed ONLY to the Tier 0 built-in
	// (a configured command adapter receives a TemplateFile too but may never read it).
	// REV-209 R4-CODEX-002: relativise the template through ResolveUnderRoot, whose
	// relative return is computed against the RESOLVED project root, so recognition is
	// correct even when the root is a symlink or a Windows 8.3 short path.
	script := ""
	producer := ""
	if resolved != nil && resolved.Config != nil && resolved.Config.Script != "" {
		script = filepath.ToSlash(resolved.Config.Script)
		producer = script
	} else if resolved != nil && resolved.Tier == 0 && ac != nil && ac.TemplateFile != "" {
		if _, rel, err := pathsafe.ResolveUnderRoot(projectRoot, ac.TemplateFile); err == nil {
			producer = rel
		} else {
			producer = projectRelSlash(projectRoot, ac.TemplateFile)
		}
	}

	parent := layout.ParentDir()

	// BUG-207 Owner Decision 7: `gtms init --force --preset` is now layout-aware and
	// refreshes a renamed GTMS folder in place, so a shipped producer in a renamed
	// project gets the same normal preset-refresh guidance as the default layout.
	// isShippedPresetCreateProducer is already scoped to the active parent, so a
	// renamed producer (e.g. testing/test/templates/...) is recognised as shipped.
	// No user-facing string names BUG-207 (ENH-214).
	switch {
	case producer == shippedDemoCreateProducer:
		return fmt.Sprintf("the invalid header was produced by %s; refresh it with gtms init --force --demo.", producer)
	case isShippedPresetCreateProducer(producer, parent):
		return fmt.Sprintf("the invalid header was produced by %s. %s", producer, presetRefreshSentence)
	default:
		// Custom command or custom script adapter: do not blame a scaffold asset and do
		// not suggest init. Name the adapter and direct the user to fix its output.
		name := "the create"
		if resolved != nil && resolved.Name != "" {
			name = "the " + resolved.Name
		}
		if script != "" {
			return fmt.Sprintf("%s create adapter (script %s) wrote a spec without a valid test-case/v2 header; update the adapter so its output declares gtms_schema: \"test-case/v2\".", name, script)
		}
		return fmt.Sprintf("%s create adapter wrote a spec without a valid test-case/v2 header; update the adapter so its output declares gtms_schema: \"test-case/v2\".", name)
	}
}

// isShippedPresetCreateProducer reports whether producer (a project-relative, forward-slash
// path) is one of the preset-upgradeable shipped create producers under the active GTMS
// parent folder parent (REV-209 R4-CODEX-002).
func isShippedPresetCreateProducer(producer, parent string) bool {
	producer = strings.TrimPrefix(path.Clean(producer), "./")
	rest, ok := strings.CutPrefix(producer, parent+"/")
	if !ok {
		return false
	}
	_, ok = shippedPresetProducerSuffixes[rest]
	return ok
}

// findProjectRoot locates the project root by walking up from CWD.
func findProjectRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("could not determine working directory: %w", err)
	}

	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, "gtms.config")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no gtms.config found")
		}
		dir = parent
	}
}
