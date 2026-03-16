package adapter

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// InvokeTier2 executes a Tier 2 adapter script with GTMS_ environment variables.
// The script can update the result contract file directly. If it doesn't,
// GTMS falls back to exit code handling (same as Tier 1).
func InvokeTier2(ctx context.Context, ac *AdapterContext, scriptPath string) (*InvocationResult, error) {
	// Check script exists
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("adapter script not found: %s", scriptPath)
	}

	// Build environment variables — minimal allowlist + GTMS_ vars
	env := minimalEnv()
	env = append(env,
		"GTMS_TASK_ID="+ac.TaskID,
		"GTMS_COMMAND="+ac.Command,
		"GTMS_REFERENCE="+ac.Reference,
		"GTMS_TESTCASE="+ac.TestCase,
		"GTMS_TESTCASE_CONTENT="+ac.TestCaseContent,
		"GTMS_OUTPUT_DIR="+ac.OutputDir,
		"GTMS_OUTPUT_SUBDIR="+ac.OutputSubdir,
		"GTMS_SPEC_FILE="+ac.SpecFile,
		"GTMS_PROMPT_TEMPLATE="+ac.PromptTemplate,
		"GTMS_BRANCH="+ac.Branch,
		"GTMS_REPO="+ac.Repo,
		"GTMS_PROJECT_ROOT="+ac.ProjectRoot,
		"GTMS_WORK_DIR="+ac.WorkDir,
		"GTMS_RESULT_FILE="+ac.ResultFile,
		"GTMS_FOCUS="+ac.Focus,
		"GTMS_CONTEXT="+ac.Context,
		"GTMS_CONTEXT_FILE="+ac.ContextFile,
		"GTMS_GUIDES="+ac.Guides,
		"GTMS_PROMPT_FILE="+ac.PromptFile,
		"GTMS_ENVIRONMENT="+ac.Environment,
		"GTMS_TC_IDS="+ac.TestCaseIDs,
	)

	// Verify sh is available; Tier 2 scripts require a POSIX shell (ENH-009)
	if _, shErr := lookPath("sh"); shErr != nil {
		if runtime.GOOS == "windows" {
			return nil, fmt.Errorf("adapter script requires a POSIX shell (sh/bash) which is not available on this system; install Git Bash or WSL to run Tier 2 adapter scripts on Windows")
		}
		return nil, fmt.Errorf("POSIX shell (sh) not found on PATH; Tier 2 adapter scripts require sh")
	}

	// Execute script via sh
	cmd := exec.CommandContext(ctx, "sh", scriptPath)
	cmd.Env = env
	if ac.ProjectRoot != "" {
		cmd.Dir = ac.ProjectRoot
	}

	// Pipe assembled prompt via stdin for tools that read from stdin.
	if ac.AssembledPrompt != "" {
		cmd.Stdin = strings.NewReader(ac.AssembledPrompt)
	}

	return runAdapterProcess(cmd, filepath.Join(ac.OutputDir, ac.OutputSubdir))
}

// minimalEnv returns a minimal set of environment variables for Tier 2 script execution.
// Only essential system vars (PATH, HOME, etc.) and platform-specific vars are included.
// GTMS_ vars are appended by the caller. This prevents leaking secrets from the parent process.
func minimalEnv() []string {
	allowed := []string{"PATH", "HOME", "TMPDIR", "USER", "SHELL", "LANG", "LC_ALL"}
	if runtime.GOOS == "windows" {
		allowed = append(allowed, "USERPROFILE", "SYSTEMROOT", "COMSPEC", "PATHEXT", "TEMP", "TMP")
	}
	var env []string
	for _, key := range allowed {
		if val := os.Getenv(key); val != "" {
			env = append(env, key+"="+val)
		}
	}
	return env
}
