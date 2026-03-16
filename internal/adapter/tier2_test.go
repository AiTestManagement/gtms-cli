package adapter

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInvokeTier2_ScriptExecution(t *testing.T) {
	skipIfShort(t)
	dir := t.TempDir()

	// Create a simple mock script
	scriptPath := filepath.Join(dir, "mock-adapter.sh")
	script := `#!/bin/bash
echo "tier2 output"
`
	err := os.WriteFile(scriptPath, []byte(script), 0755)
	require.NoError(t, err)

	ac := &AdapterContext{
		TaskID:      "task-t2test1",
		Command:     "create",
		Reference:   "JIRA-456",
		ProjectRoot: dir,
		WorkDir:     dir,
		ResultFile:  filepath.Join(dir, "result.yaml"),
	}

	result, err := InvokeTier2(context.Background(), ac, scriptPath)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, "tier2 output", result.Stdout)
}

func TestInvokeTier2_EnvVarsPassed(t *testing.T) {
	skipIfShort(t)
	dir := t.TempDir()

	// Script that echoes env vars
	scriptPath := filepath.Join(dir, "env-check.sh")
	script := `#!/bin/bash
echo "TASK=$GTMS_TASK_ID CMD=$GTMS_COMMAND SRC=$GTMS_REFERENCE"
`
	err := os.WriteFile(scriptPath, []byte(script), 0755)
	require.NoError(t, err)

	ac := &AdapterContext{
		TaskID:      "task-t2env",
		Command:     "create",
		Reference:   "JIRA-789",
		TestCase:    "tc-001",
		OutputDir:   "/output",
		SpecFile:    "spec.md",
		Branch:      "feature/test",
		Repo:        "org/repo",
		ProjectRoot: dir,
		WorkDir:     dir,
		ResultFile:  filepath.Join(dir, "result.yaml"),
	}

	result, err := InvokeTier2(context.Background(), ac, scriptPath)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "TASK=task-t2env")
	assert.Contains(t, result.Stdout, "CMD=create")
	assert.Contains(t, result.Stdout, "SRC=JIRA-789")
}

func TestInvokeTier2_GuidesEnvVar(t *testing.T) {
	skipIfShort(t)
	dir := t.TempDir()

	scriptPath := filepath.Join(dir, "guides-check.sh")
	script := `#!/bin/bash
printf '%s' "$GTMS_GUIDES"
`
	err := os.WriteFile(scriptPath, []byte(script), 0755)
	require.NoError(t, err)

	ac := &AdapterContext{
		TaskID:      "task-t2guides",
		Command:     "create",
		Reference:   "JIRA-GUIDES",
		Guides:      "guide content here",
		ProjectRoot: dir,
		WorkDir:     dir,
		ResultFile:  filepath.Join(dir, "result.yaml"),
	}

	result, err := InvokeTier2(context.Background(), ac, scriptPath)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "guide content here")
}

func TestInvokeTier2_ContextEnvVars(t *testing.T) {
	skipIfShort(t)
	dir := t.TempDir()

	scriptPath := filepath.Join(dir, "ctx-check.sh")
	script := `#!/bin/bash
echo "CTX=$GTMS_CONTEXT FILE=$GTMS_CONTEXT_FILE"
`
	err := os.WriteFile(scriptPath, []byte(script), 0755)
	require.NoError(t, err)

	ac := &AdapterContext{
		TaskID:      "task-t2ctx",
		Command:     "create",
		Reference:   "JIRA-CTX",
		Context:     "context content here",
		ContextFile: "/tmp/notes.md",
		ProjectRoot: dir,
		WorkDir:     dir,
		ResultFile:  filepath.Join(dir, "result.yaml"),
	}

	result, err := InvokeTier2(context.Background(), ac, scriptPath)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "CTX=context content here")
	assert.Contains(t, result.Stdout, "FILE=/tmp/notes.md")
}

func TestInvokeTier2_ContextWithSpecialChars(t *testing.T) {
	skipIfShort(t)
	dir := t.TempDir()

	// Script prints GTMS_CONTEXT using printf %s to avoid shell interpretation
	scriptPath := filepath.Join(dir, "special-check.sh")
	script := `#!/bin/bash
printf '%s' "$GTMS_CONTEXT"
`
	err := os.WriteFile(scriptPath, []byte(script), 0755)
	require.NoError(t, err)

	// Context with shell metacharacters that would break naive shell expansion
	specialContent := `user said $HOME is "important" and used 'quotes' & backticks`

	ac := &AdapterContext{
		TaskID:      "task-t2special",
		Command:     "create",
		Reference:   "JIRA-SPECIAL",
		Context:     specialContent,
		ContextFile: "/tmp/notes.md",
		ProjectRoot: dir,
		WorkDir:     dir,
		ResultFile:  filepath.Join(dir, "result.yaml"),
	}

	result, err := InvokeTier2(context.Background(), ac, scriptPath)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, `$HOME`)
	assert.Contains(t, result.Stdout, `"important"`)
	assert.Contains(t, result.Stdout, `'quotes'`)
}

func TestInvokeTier2_WritesToResultFile(t *testing.T) {
	skipIfShort(t)
	dir := t.TempDir()
	resultFile := filepath.Join(dir, "result.yaml")

	// Script that writes to GTMS_RESULT_FILE
	scriptPath := filepath.Join(dir, "write-result.sh")
	script := `#!/bin/bash
cat > "${GTMS_RESULT_FILE}" <<EOF
task: ${GTMS_TASK_ID}
command: ${GTMS_COMMAND}
target: ${GTMS_REFERENCE}
adapter: mock-tier2
mode: sync
status: complete
artefact: test-output.md
attempts: 1
summary: "Mock tier2 completed"
completed: "2025-02-14T10:05:00Z"
EOF
`
	err := os.WriteFile(scriptPath, []byte(script), 0755)
	require.NoError(t, err)

	ac := &AdapterContext{
		TaskID:      "task-t2write",
		Command:     "create",
		Reference:   "JIRA-111",
		ProjectRoot: dir,
		WorkDir:     dir,
		ResultFile:  resultFile,
	}

	result, err := InvokeTier2(context.Background(), ac, scriptPath)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)

	// Verify result file was written
	assert.FileExists(t, resultFile)
	data, err := os.ReadFile(resultFile)
	require.NoError(t, err)
	assert.Contains(t, string(data), "status: complete")
	assert.Contains(t, string(data), "task: task-t2write")
}

func TestInvokeTier2_ExitCodeFallback(t *testing.T) {
	skipIfShort(t)
	dir := t.TempDir()

	scriptPath := filepath.Join(dir, "fail.sh")
	script := `#!/bin/bash
echo "error message" >&2
exit 1
`
	err := os.WriteFile(scriptPath, []byte(script), 0755)
	require.NoError(t, err)

	ac := &AdapterContext{
		TaskID:      "task-t2fail",
		Command:     "create",
		ProjectRoot: dir,
		WorkDir:     dir,
		ResultFile:  filepath.Join(dir, "result.yaml"),
	}

	result, err := InvokeTier2(context.Background(), ac, scriptPath)
	require.NoError(t, err)
	assert.Equal(t, 1, result.ExitCode)
	assert.Equal(t, "error message", result.Stderr)
}

func TestInvokeTier2_ScriptNotFound(t *testing.T) {
	ac := &AdapterContext{
		TaskID:      "task-t2nf",
		ProjectRoot: t.TempDir(),
	}

	_, err := InvokeTier2(context.Background(), ac, "/nonexistent/script.sh")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "adapter script not found")
}

func TestInvokeTier2_PromptFileEnvVar(t *testing.T) {
	skipIfShort(t)
	dir := t.TempDir()

	scriptPath := filepath.Join(dir, "prompt-file-check.sh")
	script := "#!/bin/bash\nprintf '%s' \"$GTMS_PROMPT_FILE\"\n"
	err := os.WriteFile(scriptPath, []byte(script), 0755)
	require.NoError(t, err)

	ac := &AdapterContext{
		TaskID:      "task-t2pf",
		Command:     "create",
		Reference:   "JIRA-PF",
		PromptFile:  "/tmp/test-prompt.md",
		ProjectRoot: dir,
		WorkDir:     dir,
		ResultFile:  filepath.Join(dir, "result.yaml"),
	}

	result, err := InvokeTier2(context.Background(), ac, scriptPath)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, "/tmp/test-prompt.md", result.Stdout)
}

func TestInvokeTier2_StdinDelivery(t *testing.T) {
	skipIfShort(t)
	dir := t.TempDir()

	scriptPath := filepath.Join(dir, "stdin-check.sh")
	script := "#!/bin/bash\ncat\n"
	err := os.WriteFile(scriptPath, []byte(script), 0755)
	require.NoError(t, err)

	ac := &AdapterContext{
		TaskID:          "task-t2stdin",
		Command:         "create",
		Reference:       "JIRA-STDIN",
		AssembledPrompt: "prompt via stdin",
		ProjectRoot:     dir,
		WorkDir:         dir,
		ResultFile:      filepath.Join(dir, "result.yaml"),
	}

	result, err := InvokeTier2(context.Background(), ac, scriptPath)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "prompt via stdin")
}

func TestTier2_MinimalEnv(t *testing.T) {
	skipIfShort(t)
	dir := t.TempDir()

	// Set a non-GTMS env var in parent process
	t.Setenv("SECRET_KEY_FOR_TEST", "hunter2")

	scriptPath := filepath.Join(dir, "env-leak-check.sh")
	script := `#!/bin/bash
printf '%s' "$SECRET_KEY_FOR_TEST"
`
	err := os.WriteFile(scriptPath, []byte(script), 0755)
	require.NoError(t, err)

	ac := &AdapterContext{
		TaskID:      "task-t2minimal",
		Command:     "create",
		Reference:   "JIRA-MINIMAL",
		ProjectRoot: dir,
		WorkDir:     dir,
		ResultFile:  filepath.Join(dir, "result.yaml"),
	}

	result, err := InvokeTier2(context.Background(), ac, scriptPath)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	// SECRET_KEY_FOR_TEST should NOT be visible to the script
	assert.Empty(t, result.Stdout, "non-GTMS env var should not leak to Tier 2 scripts")
}

func TestTier2_PathPreserved(t *testing.T) {
	skipIfShort(t)
	dir := t.TempDir()

	scriptPath := filepath.Join(dir, "path-check.sh")
	script := `#!/bin/bash
printf '%s' "$PATH"
`
	err := os.WriteFile(scriptPath, []byte(script), 0755)
	require.NoError(t, err)

	ac := &AdapterContext{
		TaskID:      "task-t2path",
		Command:     "create",
		Reference:   "JIRA-PATH",
		ProjectRoot: dir,
		WorkDir:     dir,
		ResultFile:  filepath.Join(dir, "result.yaml"),
	}

	result, err := InvokeTier2(context.Background(), ac, scriptPath)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.NotEmpty(t, result.Stdout, "PATH should be preserved in Tier 2 env")
}

func TestInvokeTier2_EnvironmentEnvVar(t *testing.T) {
	skipIfShort(t)
	dir := t.TempDir()

	scriptPath := filepath.Join(dir, "env-check.sh")
	script := `#!/bin/bash
printf '%s' "$GTMS_ENVIRONMENT"
`
	err := os.WriteFile(scriptPath, []byte(script), 0755)
	require.NoError(t, err)

	ac := &AdapterContext{
		TaskID:      "task-t2envflag",
		Command:     "automate",
		TestCase:    "tc-001",
		Environment: "production",
		ProjectRoot: dir,
		WorkDir:     dir,
		ResultFile:  filepath.Join(dir, "result.yaml"),
	}

	result, err := InvokeTier2(context.Background(), ac, scriptPath)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, "production", result.Stdout)
}

func TestInvokeTier2_OutputSubdirEnvVar(t *testing.T) {
	skipIfShort(t)
	dir := t.TempDir()

	scriptPath := filepath.Join(dir, "subdir-check.sh")
	script := `#!/bin/bash
printf '%s' "$GTMS_OUTPUT_SUBDIR"
`
	err := os.WriteFile(scriptPath, []byte(script), 0755)
	require.NoError(t, err)

	ac := &AdapterContext{
		TaskID:       "task-t2subdir",
		Command:      "automate",
		TestCase:     "tc-001",
		OutputSubdir: "cwd-scoping/",
		ProjectRoot:  dir,
		WorkDir:      dir,
		ResultFile:   filepath.Join(dir, "result.yaml"),
	}

	result, err := InvokeTier2(context.Background(), ac, scriptPath)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, "cwd-scoping/", result.Stdout)
}

func TestInvokeTier2_OutputSubdirEmptyEnvVar(t *testing.T) {
	skipIfShort(t)
	dir := t.TempDir()

	scriptPath := filepath.Join(dir, "subdir-empty-check.sh")
	script := `#!/bin/bash
printf 'pre%send' "$GTMS_OUTPUT_SUBDIR"
`
	err := os.WriteFile(scriptPath, []byte(script), 0755)
	require.NoError(t, err)

	ac := &AdapterContext{
		TaskID:       "task-t2subdirE",
		Command:      "automate",
		TestCase:     "tc-001",
		OutputSubdir: "",
		ProjectRoot:  dir,
		WorkDir:      dir,
		ResultFile:   filepath.Join(dir, "result.yaml"),
	}

	result, err := InvokeTier2(context.Background(), ac, scriptPath)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, "preend", result.Stdout)
}

func TestInvokeTier2_TCIDsEnvVar(t *testing.T) {
	skipIfShort(t)
	dir := t.TempDir()

	scriptPath := filepath.Join(dir, "tcids-check.sh")
	script := "#!/bin/bash\nprintf '%s' \"$GTMS_TC_IDS\"\n"
	err := os.WriteFile(scriptPath, []byte(script), 0755)
	require.NoError(t, err)

	ac := &AdapterContext{
		TaskID:      "task-t2tcids",
		Command:     "create",
		Reference:   "JIRA-TCIDS",
		TestCaseIDs: "tc-a1b2c3d,tc-e4f5a6b,tc-c7d8e9f",
		ProjectRoot: dir,
		WorkDir:     dir,
		ResultFile:  filepath.Join(dir, "result.yaml"),
	}

	result, err := InvokeTier2(context.Background(), ac, scriptPath)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, "tc-a1b2c3d,tc-e4f5a6b,tc-c7d8e9f", result.Stdout)
}

func TestInvokeTier2_ContextTimeout(t *testing.T) {
	skipIfShort(t)
	dir := t.TempDir()

	// Create a script that uses exec to replace the shell with sleep,
	// so killing the process kills sleep directly (no orphaned child on Windows).
	scriptPath := filepath.Join(dir, "slow-adapter.sh")
	script := "#!/bin/bash\nexec sleep 30\n"
	err := os.WriteFile(scriptPath, []byte(script), 0755)
	require.NoError(t, err)

	ac := &AdapterContext{
		TaskID:      "task-t2timeout",
		Command:     "create",
		Reference:   "JIRA-TIMEOUT",
		ProjectRoot: dir,
		WorkDir:     dir,
		ResultFile:  filepath.Join(dir, "result.yaml"),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	result, err := InvokeTier2(ctx, ac, scriptPath)
	elapsed := time.Since(start)

	// Process should be killed well before the 30s sleep completes.
	assert.Less(t, elapsed, 15*time.Second, "should have been killed by context timeout, not run full 30s")

	// Either an error is returned or result has non-zero exit code
	if err != nil {
		return
	}
	assert.NotEqual(t, 0, result.ExitCode, "should have non-zero exit code after timeout")
}

// --- ENH-009: Tier 2 shell detection tests ---

func TestInvokeTier2_ShNotFound_Windows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only test")
	}

	dir := t.TempDir()

	// Create a real script file so os.Stat passes
	scriptPath := filepath.Join(dir, "test-adapter.sh")
	err := os.WriteFile(scriptPath, []byte("#!/bin/bash\necho hello\n"), 0755)
	require.NoError(t, err)

	// Override lookPath to simulate sh not found
	origLookPath := lookPath
	lookPath = func(name string) (string, error) {
		return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
	}
	defer func() { lookPath = origLookPath }()

	ac := &AdapterContext{
		TaskID:      "task-t2nosh-win",
		Command:     "create",
		Reference:   "REQ-1",
		ProjectRoot: dir,
		WorkDir:     dir,
		ResultFile:  filepath.Join(dir, "result.yaml"),
	}

	_, err = InvokeTier2(context.Background(), ac, scriptPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "POSIX shell")
	assert.Contains(t, err.Error(), "Git Bash or WSL")
}

func TestInvokeTier2_ShNotFound_NonWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("non-Windows-only test")
	}

	dir := t.TempDir()

	scriptPath := filepath.Join(dir, "test-adapter.sh")
	err := os.WriteFile(scriptPath, []byte("#!/bin/bash\necho hello\n"), 0755)
	require.NoError(t, err)

	origLookPath := lookPath
	lookPath = func(name string) (string, error) {
		return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
	}
	defer func() { lookPath = origLookPath }()

	ac := &AdapterContext{
		TaskID:      "task-t2nosh-nix",
		Command:     "create",
		Reference:   "REQ-1",
		ProjectRoot: dir,
		WorkDir:     dir,
		ResultFile:  filepath.Join(dir, "result.yaml"),
	}

	_, err = InvokeTier2(context.Background(), ac, scriptPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "POSIX shell (sh) not found")
}
