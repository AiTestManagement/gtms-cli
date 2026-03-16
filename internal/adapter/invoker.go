package adapter

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/id"
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
	Status        string   // complete, error, in-progress
	Summary       string
	Filename      string   // task filename
	ArtifactCount int      // number of files produced by adapter
	ArtifactPaths []string // relative paths of produced files
	Warnings      []string // non-fatal issues to surface to user
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

	// 2. Build branch name (sanitise target for git compatibility)
	branch := fmt.Sprintf("feature/%s-%s", resolved.Command, sanitizeBranchTarget(target))

	// 3. Create task file in test-tasks/pending/
	tf := &task.TaskFile{
		ID:        taskID,
		Type:      resolved.Command,
		Target:    target,
		Adapter:   resolved.Name,
		Status:    "pending",
		Created:   time.Now().UTC().Format(time.RFC3339),
		Branch:    branch,
		Reference: flags.Reference,
	}

	// Set automate-specific task file fields
	if resolved.Command == "automate" {
		tf.Framework = flags.Framework
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

	// 5. Create result contract
	rc := &result.ResultContract{
		Task:    taskID,
		Command: resolved.Command,
		Target:  target,
		Adapter: resolved.Name,
		Mode:    resolved.Mode,
		Created: tf.Created,
		Status:  "pending",
	}

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
			"spec_file":        ac.SpecFile,
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

	// 10. Snapshot output dir before invocation (for artefact detection)
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
		// Built-in: handled separately, not via Invoke
		return nil, fmt.Errorf("built-in adapters should not be invoked via Invoke()")
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
		_ = task.Move(projectRoot, tf, "failed")
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
		_ = task.Move(projectRoot, tf, "failed")
		_ = result.Update(resultPath, map[string]interface{}{
			"status":    "error",
			"summary":   summary,
			"completed": time.Now().UTC().Format(time.RFC3339),
		})
		return nil, fmt.Errorf("%s", summary)
	}

	// 12. Handle result based on mode
	if resolved.Mode == "sync" {
		invokeRes, invokeErr := handleSyncResult(projectRoot, tf, resultPath, resolved, invResult, ac.OutputDir, preInvokeFiles)
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
		if resolved.Config.OutputDir != "" {
			ctx.OutputDir = filepath.Join(projectRoot, resolved.Config.OutputDir)
		} else {
			ctx.OutputDir = filepath.Join(projectRoot, "test-cases", flags.Folder)
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
			ctx.OutputDir = filepath.Join(projectRoot, "test-automation", "specs", resolved.Name)
		}
		ctx.OutputSubdir = deriveOutputSubdir(findTestCaseSource(projectRoot, target))
	case "execute":
		ctx.TestCase = target
		ctx.SpecFile = flags.SpecFile
		if resolved.Config.OutputDir != "" {
			ctx.OutputDir = filepath.Join(projectRoot, resolved.Config.OutputDir)
		} else {
			ctx.OutputDir = filepath.Join(projectRoot, "results")
		}
		ctx.OutputSubdir = deriveOutputSubdir(findTestCaseSource(projectRoot, target))
	default:
		ctx.Reference = flags.Reference
		if resolved.Config.OutputDir != "" {
			ctx.OutputDir = filepath.Join(projectRoot, resolved.Config.OutputDir)
		} else {
			ctx.OutputDir = filepath.Join(projectRoot, "test-cases")
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
func handleSyncResult(projectRoot string, tf *task.TaskFile, resultPath string, resolved *ResolvedAdapter, invResult *InvocationResult, outputDir string, preInvokeFiles map[string]struct{}) (*InvokeResult, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	// For Tier 2: check if the script updated the result contract
	// We check for terminal statuses (complete/error) rather than != "pending"
	// because the contract is now set to "in-progress" before invocation (BUG-022).
	if resolved.Tier == 2 {
		rc, err := result.Read(resultPath)
		if err == nil && (rc.Status == "complete" || rc.Status == "error") {
			// Script updated the result contract to a terminal status
			newStatus := "complete"
			if rc.Status == "error" {
				newStatus = "failed"
				tf.Error = rc.Summary
			}

			if err := task.Move(projectRoot, tf, newStatus); err != nil {
				return nil, fmt.Errorf("moving task file: %w", err)
			}

			// Collect warnings and artifact data for Tier 2 contract-updated path
			var warnings []string

			// Build pipeline records for completed sync tasks
			if newStatus == "complete" {
				if pErr := buildPipelineRecords(projectRoot, tf, rc); pErr != nil {
					warnings = append(warnings, fmt.Sprintf("Pipeline record could not be written: %v", pErr))
				}
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

			return &InvokeResult{
				TaskID:        tf.ID,
				Adapter:       resolved.Name,
				Mode:          resolved.Mode,
				Branch:        tf.Branch,
				Status:        rc.Status,
				Summary:       rc.Summary,
				Filename:      tf.Filename(),
				ArtifactCount: len(invResult.SavedFiles),
				ArtifactPaths: relPaths,
				Warnings:      warnings,
			}, nil
		}
		// Fall through to exit code handling if status is still pending
	}

	// For Tier 1 (or Tier 2 fallback): use exit code to determine result
	if invResult.ExitCode == 0 {
		updates := map[string]interface{}{
			"status":    "complete",
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

		_ = result.Update(resultPath, updates)

		if err := task.Move(projectRoot, tf, "complete"); err != nil {
			return nil, fmt.Errorf("moving task file: %w", err)
		}

		// Collect warnings
		var warnings []string

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
			if pErr := buildPipelineRecords(projectRoot, tf, rc); pErr != nil {
				warnings = append(warnings, fmt.Sprintf("Pipeline record could not be written: %v", pErr))
			}
		}

		return &InvokeResult{
			TaskID:        tf.ID,
			Adapter:       resolved.Name,
			Mode:          resolved.Mode,
			Branch:        tf.Branch,
			Status:        "complete",
			Summary:       summary,
			Filename:      tf.Filename(),
			ArtifactCount: artifactCount,
			ArtifactPaths: artifactPaths,
			Warnings:      warnings,
		}, nil
	}

	// Non-zero exit code = error
	summary := fmt.Sprintf("Process exited with code %d", invResult.ExitCode)
	if invResult.Stderr != "" {
		summary = fmt.Sprintf("%s: %s", summary, invResult.Stderr)
	}

	_ = result.Update(resultPath, map[string]interface{}{
		"status":    "error",
		"completed": now,
		"attempts":  1,
		"summary":   summary,
	})

	tf.Error = summary
	if err := task.Move(projectRoot, tf, "failed"); err != nil {
		return nil, fmt.Errorf("moving task file: %w", err)
	}

	return &InvokeResult{
		TaskID:   tf.ID,
		Adapter:  resolved.Name,
		Mode:     resolved.Mode,
		Branch:   tf.Branch,
		Status:   "error",
		Summary:  summary,
		Filename: tf.Filename(),
	}, nil
}

// buildPipelineRecords calls the appropriate pipeline function based on the command type.
// Returns any error from the pipeline function so callers can surface it as a warning.
func buildPipelineRecords(projectRoot string, tf *task.TaskFile, rc *result.ResultContract) error {
	switch tf.Type {
	case "automate":
		return pipeline.BuildAutomationRecord(projectRoot, tf, rc)
	case "execute":
		return pipeline.UpdateExecutionResult(projectRoot, tf, rc)
	}
	return nil
}

// deriveOutputSubdir extracts the subdirectory path from a test case source path.
// For "test-cases/cwd-scoping/tc-abc.md" it returns "cwd-scoping/".
// For "test-cases/tc-abc.md" (root level) or empty input it returns "".
func deriveOutputSubdir(testCaseSourcePath string) string {
	if testCaseSourcePath == "" {
		return ""
	}
	// Strip the "test-cases/" prefix
	trimmed := strings.TrimPrefix(testCaseSourcePath, "test-cases/")
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

// findTestCaseSource searches for a test case file in the test-cases directory tree.
func findTestCaseSource(projectRoot, target string) string {
	testCasesDir := filepath.Join(projectRoot, "test-cases")

	// Walk the test-cases directory looking for a file matching the target
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
