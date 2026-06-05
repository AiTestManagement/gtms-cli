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
)

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

// InvokeWithRoot is like Invoke but accepts an explicit project root (useful for testing).
func InvokeWithRoot(ctx context.Context, projectRoot string, cfg *config.Config, resolved *ResolvedAdapter, target string, flags CommandFlags) (*InvokeResult, error) {
	// Apply timeout from adapter config
	if resolved.Config.Timeout != "" {
		d, err := time.ParseDuration(resolved.Config.Timeout)
		if err != nil {
			return nil, fmt.Errorf("adapter '%s' has invalid timeout '%s': %w", resolved.Name, resolved.Config.Timeout, err)
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d)
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
		source := findTestCaseSource(projectRoot, target)
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

	_, err := task.Create(projectRoot, tf, "")
	if err != nil {
		return nil, fmt.Errorf("creating task file: %w", err)
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
	ac := buildAdapterContext(projectRoot, taskID, resolved, target, flags, branch, cfg, workDir, resultPath)
	ac.Guides = guides
	ac.TestCaseContent = testCaseContent
	ac.Force = flags.Force

	// CON-023 / ENH-146 (manual-execute exception): if the manual-execute
	// adapter pipeline parsed a framework out of the result template, that
	// value is the authoritative source — wiring does not exist for
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
	// must see — the rest of the system relies on task placement.
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
			pWarn, pErr := buildPipelineRecords(projectRoot, cfg, tf, rc)
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

	// 10a. Pre-adapter cleanup: remove existing output files by TC ID prefix (BUG-031)
	// When --force is set, the adapter will regenerate output. Old files must be removed
	// first to avoid duplicates when the adapter produces a different filename slug.
	if flags.Force && (resolved.Command == "automate" || resolved.Command == "create") {
		effectiveDir := filepath.Join(ac.OutputDir, ac.OutputSubdir)
		_ = cleanupExistingOutputByTCID(effectiveDir, target)
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
			summary = fmt.Sprintf("adapter timed out after %s", resolved.Config.Timeout)
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
		return nil, fmt.Errorf("%s", summary)
	}

	// Detect cancellation/timeout even when process exited with non-zero code
	// (process.go captures ExitError as a successful InvocationResult with exit code)
	if invResult != nil && ctx.Err() != nil {
		summary := "adapter cancelled"
		if ctx.Err() == context.DeadlineExceeded {
			summary = fmt.Sprintf("adapter timed out after %s", resolved.Config.Timeout)
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

			_ = result.Update(resultPath, map[string]interface{}{
				"status":           "error",
				"completed":        now,
				"attempts":         1,
				"summary":          summary,
				"validation-error": summary,
			})

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
		invokeRes, invokeErr := handleSyncResult(ctx, projectRoot, cfg, tf, resultPath, resolved, invResult, ac.OutputDir, preInvokeFiles, flags.ArtefactHash)
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
func buildAdapterContext(projectRoot, taskID string, resolved *ResolvedAdapter, target string, flags CommandFlags, branch string, cfg *config.Config, workDir, resultPath string) *AdapterContext {
	ctx := &AdapterContext{
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

	// Set command-specific context fields
	switch resolved.Command {
	case "create":
		ctx.Reference = flags.Reference
		ctx.TestCaseName = flags.Name
		if resolved.Config.OutputDir != "" {
			ctx.OutputDir = filepath.Join(projectRoot, resolved.Config.OutputDir)
		} else {
			ctx.OutputDir = filepath.Join(layout.CasesDir(projectRoot), flags.Folder)
		}
		// Generate batch of pre-generated test case IDs for adapter consumption (ENH-042)
		tcIDs := make([]string, 20)
		for i := range tcIDs {
			tcIDs[i] = "tc-" + id.New()
		}
		ctx.TestCaseIDs = strings.Join(tcIDs, ",")
	case "automate":
		ctx.TestCase = target
		if resolved.Config.OutputDir != "" {
			ctx.OutputDir = filepath.Join(projectRoot, resolved.Config.OutputDir)
		} else {
			ctx.OutputDir = filepath.Join(layout.SpecsDir(projectRoot), resolved.Name)
		}
		tcSource := findTestCaseSource(projectRoot, target)
		ctx.OutputSubdir = deriveOutputSubdir(tcSource)
		ctx.TestCaseFile = tcSource

		// ENH-132: Populate manual-prime context fields when framework is manual.
		// This is framework-specific context population, not tier-specific branching.
		framework := ResolveFramework(resolved, flags.Framework)
		ctx.Framework = framework // ENH-151: available to BuiltinAutomate
		if framework == "manual" && tcSource != "" {
			absTC := filepath.Join(projectRoot, tcSource)
			if hash, hashErr := pipeline.HashFile(absTC); hashErr == nil {
				ctx.TestCaseHash = hash
			}
			ctx.TemplateFile = filepath.Join(layout.ManualTemplatesDir(projectRoot), "manual-result.template.yaml")
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
		tcSource := findTestCaseSource(projectRoot, target)
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
			ctx.TemplateFile = filepath.Join(layout.ManualTemplatesDir(projectRoot), "manual-result.template.yaml")
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
		tcSource := findTestCaseSource(projectRoot, target)
		ctx.OutputSubdir = deriveOutputSubdir(tcSource)
		ctx.TestCaseFile = tcSource

		// ENH-133: Populate manual-execute context fields for the manual path.
		// This is framework-specific context population, not tier-specific
		// branching.
		//
		// Adapter first, framework second: keys solely on the resolved adapter
		// via IsManualFramework — same predicate cli/execute.go uses to defer
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
			ctx.OutputDir = layout.CasesDir(projectRoot)
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
func handleSyncResult(ctx context.Context, projectRoot string, cfg *config.Config, tf *task.TaskFile, resultPath string, resolved *ResolvedAdapter, invResult *InvocationResult, outputDir string, preInvokeFiles map[string]struct{}, artefactHash string) (*InvokeResult, error) {
	now := time.Now().UTC().Format(time.RFC3339)

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
				// Script produced a malformed contract — treat as adapter error.
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
			// the Tier 2 script set — two <gtms-file> tags for a single
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
			// Re-stamp Git context unconditionally — the adapter does not
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
			pWarn, pErr := buildPipelineRecords(projectRoot, cfg, tf, rc)
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
		// Fall through to exit code handling if status is still pending
	}

	// For Tier 1 (or Tier 2 fallback): use exit code to determine result
	if invResult.ExitCode == 0 {
		// ENH-080: hard stop for multi-file automate. If the streaming writer
		// captured more than one <gtms-file> tag for a single automate TC,
		// fail the task and skip building the automation record — no
		// comma-separated artefact: ever ships in a record GTMS writes.
		if resolved.Command == "automate" && len(invResult.SavedFiles) > 1 {
			return rejectMultiFileAutomate(projectRoot, tf, resultPath, resolved, invResult)
		}

		contractStatus := "complete"
		contractResult := "pass" // ENH-130: exit 0 = adapter ran successfully, test passed

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
			pWarn, pErr := buildPipelineRecords(projectRoot, cfg, tf, rc)
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
		pWarn, pErr := buildPipelineRecords(projectRoot, cfg, tf, rc)
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
// on command type. Returns any warnings (typically the canonical
// execute-adapter fallback diagnostic from WriteAutomateWiring) plus any
// error so the caller can surface both on the result contract.
//
// CON-023 / ENH-145 / ENH-146 (cutover complete):
//   - automate: WriteAutomateWiring writes the six-field wiring file at
//     gtms/automation/wiring/<tc>--<framework>.wiring.yaml. The legacy
//     gtms/automation/records/*.automation.md path is retired.
//   - execute:  WriteExecuteResultsFile writes the per-test results file
//     at gtms/execution/<task>--<tc>.results.yaml (ADR-020 / CON-016).
//     Wiring is immutable on the execute path; the result contract under
//     .gtms/results/ is the canonical store of the test outcome.
func buildPipelineRecords(projectRoot string, cfg *config.Config, tf *task.TaskFile, rc *result.ResultContract) ([]string, error) {
	switch tf.Type {
	case "automate":
		return WriteAutomateWiring(projectRoot, cfg, tf, rc)
	case "execute":
		return nil, WriteExecuteResultsFile(projectRoot, tf, rc)
	}
	return nil, nil
}

// deriveOutputSubdir extracts the subdirectory path from a test case source path.
// For "gtms/cases/cwd-scoping/tc-abc.md" it returns "cwd-scoping/".
// For "gtms/cases/tc-abc.md" (root level) or empty input it returns "".
func deriveOutputSubdir(testCaseSourcePath string) string {
	if testCaseSourcePath == "" {
		return ""
	}
	// Strip the cases directory prefix (ENH-093: routed through layout package)
	paths := layout.Current()
	trimmed := strings.TrimPrefix(testCaseSourcePath, paths.Cases+"/")
	// Get the directory portion
	dir := filepath.Dir(trimmed)
	// Convert to forward slashes (Windows compatibility)
	dir = filepath.ToSlash(dir)
	// "." means root level — no subdirectory
	if dir == "." {
		return ""
	}
	return dir + "/"
}

// findTestCaseSource searches for a test case file in the cases directory tree.
func findTestCaseSource(projectRoot, target string) string {
	testCasesDir := layout.CasesDir(projectRoot)

	// Walk the cases directory looking for a file matching the target
	var found string
	_ = filepath.Walk(testCasesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		// Check if the filename starts with the target ID followed by a delimiter
		base := filepath.Base(path)
		if strings.HasPrefix(base, target+"-") || strings.HasPrefix(base, target+".") {
			rel, relErr := filepath.Rel(projectRoot, path)
			if relErr == nil {
				found = filepath.ToSlash(rel)
			} else {
				found = path
			}
			return filepath.SkipAll
		}
		return nil
	})

	return found
}

// readGuides reads all .md files from the given guide directory, wrapping each
// in XML <guide name="..."> tags for clear semantic boundaries. Returns empty
// string if guideDir is empty or the directory does not exist. Only returns an
// error on actual read failures.
func readGuides(projectRoot, guideDir string) (string, error) {
	if guideDir == "" {
		return "", nil
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
// files remain on disk (mirrors existing failure semantics — preserves
// evidence for the user). No pipeline record is written — that's the whole
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

	// Deliberately do NOT call buildPipelineRecords here — the whole point of
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
// When both streams carry content, stderr leads — most test frameworks
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
// HEAD, non-git directory), an empty string is returned — never a fake
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

// cleanupExistingOutputByTCID removes files in dir whose basename starts with
// the target TC ID prefix (e.g. "tc-a1b2c3d4-"). This handles the "different slug"
// scenario where --force re-runs an adapter that generates a different filename
// for the same test case (BUG-031). Only files are removed; directories are left intact.
func cleanupExistingOutputByTCID(dir, target string) error {
	if dir == "" || target == "" {
		return nil
	}

	prefix := target + "-"
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if info.IsDir() {
			return nil
		}
		if strings.HasPrefix(filepath.Base(path), prefix) {
			_ = os.Remove(path)
		}
		return nil
	})
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
