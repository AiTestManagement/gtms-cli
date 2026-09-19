package adapter

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adrg/frontmatter"
	"github.com/aitestmanagement/gtms-cli/internal/config"
	gitpkg "github.com/aitestmanagement/gtms-cli/internal/git"
	"github.com/aitestmanagement/gtms-cli/internal/id"
	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
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
}

func (e *invocationError) Error() string { return e.summary }
func (e *invocationError) Unwrap() error { return e.err }

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
// The third condition -- that the spec still declares the identity -- reuses
// ValidateTestCaseOwner rather than reading the file again here. One identity
// implementation is the point (ADR-024 s5); a second copy is how two checks come
// to disagree. Note that it does NOT re-resolve the bare id: it reads the spec
// the carried owner already points at.
func requireCarriedOwner(projectRoot string, resolved *ResolvedAdapter, target string, flags CommandFlags) error {
	switch resolved.Command {
	case "automate", "execute", "prime":
	default:
		return nil
	}

	owner := flags.Owner
	if owner == nil {
		return &CarriedOwnerError{
			Command: resolved.Command,
			Target:  target,
			Reason:  "no resolved owner reached the invocation boundary, so nothing proves the test case exists or which spec it is",
		}
	}
	if owner.ID() != target {
		return &CarriedOwnerError{
			Command: resolved.Command,
			Target:  target,
			OwnerID: owner.ID(),
			Reason: fmt.Sprintf("the carried owner proves '%s', a different test case -- the invocation would be attributed to '%s' while using another test case's spec",
				testcase.SanitizeIdentityString(owner.ID()), testcase.SanitizeIdentityString(target)),
		}
	}
	if violations := ValidateTestCaseOwner(projectRoot, owner); len(violations) > 0 {
		return &CarriedOwnerError{
			Command: resolved.Command,
			Target:  target,
			OwnerID: owner.ID(),
			Reason:  "the owning spec no longer declares that identity -- " + FormatValidationErrors(violations),
		}
	}
	return nil
}

// InvokeWithRoot is like Invoke but accepts an explicit project root (useful for testing).
func InvokeWithRoot(ctx context.Context, projectRoot string, cfg *config.Config, resolved *ResolvedAdapter, target string, flags CommandFlags) (invokeRes *InvokeResult, invokeErr error) {
	// ADR-024 s5 / REV-160 CODEX-002: the LAST boundary before any state is
	// created or any external code runs. It refuses a nil, mismatched, or stale
	// owner BEFORE the task id is generated, so a refusal leaves no task file, no
	// result contract, no wiring and no adapter process behind.
	if err := requireCarriedOwner(projectRoot, resolved, target, flags); err != nil {
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

	// 4. Ensure .gtms/ directories exist
	for _, subdir := range []string{"results", "worktrees", "logs", "tmp"} {
		dir := filepath.Join(projectRoot, ".gtms", subdir)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("creating .gtms/%s directory: %w", subdir, err)
		}
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
		return nil, fmt.Errorf("creating result contract: %w", err)
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
	ac := buildAdapterContext(projectRoot, taskID, resolved, target, flags, branch, cfg, workDir, resultPath, plannedArtefact)
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
		_ = result.Update(resultPath, map[string]interface{}{"framework": ac.ResultFramework})
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

		if updateErr := result.Update(resultPath, map[string]interface{}{
			"status":    "error",
			"summary":   summary,
			"completed": now,
		}); updateErr != nil {
			warnings = append(warnings, fmt.Sprintf("contract update failed: %v", updateErr))
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
			_ = task.Move(projectRoot, tf, "error")
			_ = result.Update(resultPath, map[string]interface{}{
				"status":    "error",
				"summary":   cleanupErr.Error(),
				"completed": time.Now().UTC().Format(time.RFC3339),
			})
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
	_ = result.Update(resultPath, map[string]interface{}{
		"status": "in-progress",
	})

	// 12. Invoke adapter based on tier
	var invResult *InvocationResult

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
		_ = task.Move(projectRoot, tf, "error")
		_ = result.Update(resultPath, map[string]interface{}{
			"status":    "error",
			"summary":   summary,
			"completed": time.Now().UTC().Format(time.RFC3339),
		})
		// BUG-171: wrap so the human-readable summary stays the surfaced
		// message (timeout/cancel/generic diagnostics unchanged) WHILE the
		// underlying error's unwrap chain survives, keeping typed errors like
		// *MissingExecuteAdapterError discoverable via errors.As at the CLI.
		return nil, &invocationError{summary: summary, err: err}
	}

	// Detect cancellation/timeout even when process exited with non-zero code
	// (process.go captures ExitError as a successful InvocationResult with exit code)
	if invResult != nil && ctx.Err() != nil {
		summary := "adapter cancelled"
		if ctx.Err() == context.DeadlineExceeded {
			summary = fmt.Sprintf("adapter timed out after %s", effectiveTimeoutStr(resolved))
		}
		tf.Error = summary
		_ = task.Move(projectRoot, tf, "error")
		_ = result.Update(resultPath, map[string]interface{}{
			"status":    "error",
			"summary":   summary,
			"completed": time.Now().UTC().Format(time.RFC3339),
		})
		return nil, fmt.Errorf("%s", summary)
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
			summary := FormatValidationErrors(valResult.Violations)

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
			if updateErr := result.Update(resultPath, map[string]interface{}{
				"status":           "error",
				"completed":        now,
				"attempts":         1,
				"summary":          summary,
				"validation-error": summary,
			}); updateErr != nil {
				return recoverUnusableContract(ctx, projectRoot, tf, resultPath, resolved, invResult, flags.ArtefactHash, ac.ArtefactFile, updateErr, summary)
			}

			tf.Error = summary
			_ = task.Move(projectRoot, tf, "error")

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
		return invokeRes, invokeErr
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
func buildAdapterContext(projectRoot, taskID string, resolved *ResolvedAdapter, target string, flags CommandFlags, branch string, cfg *config.Config, workDir, resultPath, plannedArtefact string) *AdapterContext {
	ctx := &AdapterContext{
		Owner:          flags.Owner,
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
		if resolved.Config.OutputDir != "" {
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
			absTC := filepath.Join(projectRoot, tcSource)
			if hash, hashErr := pipeline.HashFile(absTC); hashErr == nil {
				ctx.TestCaseHash = hash
			}
			// ENH-161: role-specific template path derived from adapter name.
			ctx.TemplateFile = ResolveTemplatePath(projectRoot, "prime", resolved.Name)
			ctx.OutputFile = filepath.Join(layout.ManualRecordsDir(projectRoot), target+"--manual.result.yaml")
			// Manual-prime writes to gtms/manual/records/, not the BATS/automate
			// output dir. Point the post-run scanner there so the CLI headline
			// surfaces the stamped result file path.
			ctx.OutputDir = layout.ManualRecordsDir(projectRoot)
			ctx.OutputSubdir = ""

			// ENH-142: Parse TC frontmatter to populate snapshot fields.
			// Snapshot values are copied to the result file at prime time for
			// self-contained review. Parse errors are non-fatal (empty fields).
			if tcData, readErr := os.ReadFile(absTC); readErr == nil {
				var tcFM struct {
					Title       string `yaml:"title"`
					Requirement string `yaml:"requirement"`
					Priority    string `yaml:"priority"`
					Type        string `yaml:"type"`
				}
				if _, parseErr := frontmatter.Parse(bytes.NewReader(tcData), &tcFM); parseErr == nil {
					ctx.TCTitle = tcFM.Title
					ctx.TCRequirement = tcFM.Requirement
					ctx.TCPriority = tcFM.Priority
					ctx.TCType = tcFM.Type
				}
			}
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
			absTC := filepath.Join(projectRoot, tcSource)
			if hash, hashErr := pipeline.HashFile(absTC); hashErr == nil {
				ctx.TestCaseHash = hash
			}
			// ENH-161: role-specific template path derived from adapter name.
			ctx.TemplateFile = ResolveTemplatePath(projectRoot, "prime", resolved.Name)
			ctx.OutputFile = filepath.Join(layout.ManualRecordsDir(projectRoot), target+"--manual.result.yaml")
			ctx.OutputDir = layout.ManualRecordsDir(projectRoot)
			ctx.OutputSubdir = ""

			// ENH-142: Parse TC frontmatter to populate snapshot fields.
			if tcData, readErr := os.ReadFile(absTC); readErr == nil {
				var tcFM struct {
					Title       string `yaml:"title"`
					Requirement string `yaml:"requirement"`
					Priority    string `yaml:"priority"`
					Type        string `yaml:"type"`
				}
				if _, parseErr := frontmatter.Parse(bytes.NewReader(tcData), &tcFM); parseErr == nil {
					ctx.TCTitle = tcFM.Title
					ctx.TCRequirement = tcFM.Requirement
					ctx.TCPriority = tcFM.Priority
					ctx.TCType = tcFM.Type
				}
			}
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
	// Fall through to exit-code handling so the script's stderr is folded
	// into the surfaced summary (where the missing-tooling diagnostic
	// lives) instead of getting buried by a generic "invalid contract"
	// recovery message.
	if resolved.Tier == 2 {
		rc, err := result.Read(resultPath)
		if err == nil && rc.Status != "" && rc.Status != "pending" && rc.Status != "in-progress" {
			// ENH-130: Validate script-written contracts at the Tier 2 read boundary.
			// Tier 2 adapters write contracts via heredoc, bypassing result.Update validation.
			if validateErr := result.Validate(rc); validateErr != nil {
				// Script produced a malformed contract -- treat as adapter error.
				now := time.Now().UTC().Format(time.RFC3339)
				originalContent := fmt.Sprintf("status: %s, result: %s, summary: %s", rc.Status, rc.Result, rc.Summary)
				recoveryUpdate := map[string]interface{}{
					"status":    "error",
					"result":    "",
					"summary":   fmt.Sprintf("adapter wrote invalid contract: %v", validateErr),
					"log":       originalContent,
					"completed": now,
				}
				if updateErr := result.Update(resultPath, recoveryUpdate); updateErr != nil {
					return nil, fmt.Errorf("recovery contract write failed: %w", updateErr)
				}
				tf.Error = fmt.Sprintf("adapter wrote invalid contract: %v", validateErr)
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
			if rc.Framework == "" && tf.Framework != "" {
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
			if len(restoreUpdates) > 0 {
				_ = result.Update(resultPath, restoreUpdates)
			}
			// Normalise rc.Artefact to a project-relative forward-slash path
			// so records are portable across machines and worktrees. Tier 2
			// scripts typically interpolate $GTMS_OUTPUT_FILE (an absolute
			// path) into the contract; mirror the same normalisation applied
			// to streaming-captured SavedFiles below.
			if rc.Artefact != "" && filepath.IsAbs(rc.Artefact) {
				if rel, relErr := filepath.Rel(projectRoot, rc.Artefact); relErr == nil && !strings.HasPrefix(rel, "..") {
					relSlash := filepath.ToSlash(rel)
					if relSlash != rc.Artefact {
						rc.Artefact = relSlash
						_ = result.Update(resultPath, map[string]interface{}{"artefact": relSlash})
					}
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

			// Populate artifact data from streaming parser output (if any)
			relPaths := make([]string, len(invResult.SavedFiles))
			for i, p := range invResult.SavedFiles {
				if rel, relErr := filepath.Rel(projectRoot, p); relErr == nil {
					relPaths[i] = filepath.ToSlash(rel)
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
		// does NOT reach here -- err is nil for it -- so it keeps falling through
		// to exit-code handling (tc-7d284f41 and the BUG-111 empty-status
		// diagnostic are preserved).
		if err != nil {
			return recoverUnusableContract(ctx, projectRoot, tf, resultPath, resolved, invResult, artefactHash, artefactPath, err, "")
		}
		// Fall through to exit code handling if status is still pending
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

		// Convert SavedFiles to relative paths (always compute, even if empty)
		relPaths := make([]string, len(invResult.SavedFiles))
		for i, p := range invResult.SavedFiles {
			if rel, err := filepath.Rel(projectRoot, p); err == nil {
				relPaths[i] = filepath.ToSlash(rel)
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
			if rel, err := filepath.Rel(projectRoot, invResult.ArtefactOverride); err == nil {
				artefactRel = filepath.ToSlash(rel)
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
		if updateErr := result.Update(resultPath, updates); updateErr != nil {
			warnings = append(warnings, fmt.Sprintf("contract update failed: %v", updateErr))
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

	if updateErr := result.Update(resultPath, updates); updateErr != nil {
		warnings = append(warnings, fmt.Sprintf("contract update failed: %v", updateErr))
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
func recoverUnusableContract(ctx context.Context, projectRoot string, tf *task.TaskFile, resultPath string, resolved *ResolvedAdapter, invResult *InvocationResult, artefactHash, artefactPath string, readErr error, validationSummary string) (*InvokeResult, error) {
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

	reconstructErr := result.Reconstruct(resultPath, rc)

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
		if rel, relErr := filepath.Rel(projectRoot, absPath); relErr == nil {
			paths = append(paths, filepath.ToSlash(rel))
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

	_ = result.Update(resultPath, map[string]interface{}{
		"status":    "error",
		"completed": now,
		"attempts":  1,
		"summary":   summary,
	})

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
