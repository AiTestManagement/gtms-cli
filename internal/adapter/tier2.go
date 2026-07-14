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
		"GTMS_ARTEFACT_FILE="+adapterFacingPath(ac, ac.ArtefactFile),
		"GTMS_TESTCASE_FILE="+adapterFacingPath(ac, ac.TestCaseFile),
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
		"GTMS_TC_NAME="+ac.TestCaseName,
		"GTMS_TESTCASE_HASH="+ac.TestCaseHash,
		"GTMS_TC_TITLE="+ac.TCTitle,
		"GTMS_TC_REQUIREMENT="+ac.TCRequirement,
		"GTMS_TC_PRIORITY="+ac.TCPriority,
		"GTMS_TC_TYPE="+ac.TCType,
		"GTMS_TEMPLATE_FILE="+ac.TemplateFile,
		"GTMS_OUTPUT_FILE="+ac.OutputFile,
		"GTMS_RESULT_TEMPLATE="+ac.ResultTemplate,
		"GTMS_RESULT_VALUE="+ac.ResultValue,
		"GTMS_RESULT_TESTCASE="+ac.ResultTestCase,
		"GTMS_RESULT_TESTCASE_HASH="+ac.ResultTestCaseHash,
		"GTMS_RESULT_FRAMEWORK="+ac.ResultFramework,
	)

	// ENH-132: Export GTMS_FORCE for manual-prime overwrite safety
	if ac.Force {
		env = append(env, "GTMS_FORCE=true")
	}

	// Resolve sh: use PATH first, fall back to Git for Windows install locations
	// on Windows (BUG-030). The resolved absolute path is threaded through to
	// exec.CommandContext — passing the bare string "sh" would re-trigger PATH
	// lookup at spawn time and defeat the fallback.
	shPath, shErr := resolveSh()
	if shErr != nil {
		if runtime.GOOS == "windows" {
			return nil, fmt.Errorf("adapter script requires a POSIX shell (sh/bash) which is not available on this system; install Git for Windows (standard location) or add sh to PATH to run Tier 2 adapter scripts on Windows")
		}
		return nil, fmt.Errorf("POSIX shell (sh) not found on PATH; Tier 2 adapter scripts require sh")
	}

	// On Windows, prepend Git for Windows coreutils directories (usr\bin,
	// mingw64\bin, bin) to the child's PATH. Without this, sh.exe runs but
	// cannot find mkdir, cat, cp, rm etc. when GTMS was invoked from
	// PowerShell or cmd.exe (BUG-030 part 2). Safe no-op if these dirs are
	// already on PATH — exec.Command resolves left-to-right so duplicates
	// are harmless.
	if runtime.GOOS == "windows" {
		env = prependPathEntries(env, gitBashDirsFromShPath(shPath))
	}

	// Execute script via sh
	cmd := exec.CommandContext(ctx, shPath, scriptPath)
	cmd.Env = env
	if ac.RunDir != "" {
		cmd.Dir = ac.RunDir
	}

	// Pipe assembled prompt via stdin for tools that read from stdin.
	if ac.AssembledPrompt != "" {
		cmd.Stdin = strings.NewReader(ac.AssembledPrompt)
	}

	return runAdapterProcess(cmd, filepath.Join(ac.OutputDir, ac.OutputSubdir), ac.Force)
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
