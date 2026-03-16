package adapter

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func skipIfShort(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping: requires shell execution")
	}
}

func TestInvokeTier1_EchoCommand(t *testing.T) {
	skipIfShort(t)
	ac := &AdapterContext{
		TaskID:      "task-t1test1",
		Command:     "create",
		Reference:   "JIRA-456",
		ProjectRoot: t.TempDir(),
		WorkDir:     t.TempDir(),
	}

	result, err := InvokeTier1(context.Background(), ac, `echo "test output"`)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, "test output", result.Stdout)
}

func TestInvokeTier1_VariableSubstitution(t *testing.T) {
	skipIfShort(t)
	ac := &AdapterContext{
		TaskID:      "task-t1test2",
		Command:     "create",
		Reference:   "JIRA-789",
		Branch:      "feature/create-JIRA-789",
		ProjectRoot: t.TempDir(),
		WorkDir:     t.TempDir(),
	}

	result, err := InvokeTier1(context.Background(), ac, `echo "src={reference} branch={branch}"`)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "src=JIRA-789")
	assert.Contains(t, result.Stdout, "branch=feature/create-JIRA-789")
}

func TestInvokeTier1_NonZeroExitCode(t *testing.T) {
	skipIfShort(t)
	ac := &AdapterContext{
		TaskID:      "task-t1test3",
		Command:     "create",
		ProjectRoot: t.TempDir(),
		WorkDir:     t.TempDir(),
	}

	result, err := InvokeTier1(context.Background(), ac, `exit 1`)
	require.NoError(t, err) // error is captured in exit code, not returned
	assert.Equal(t, 1, result.ExitCode)
}

func TestInvokeTier1_StderrCapture(t *testing.T) {
	skipIfShort(t)
	ac := &AdapterContext{
		TaskID:      "task-t1test4",
		Command:     "create",
		ProjectRoot: t.TempDir(),
		WorkDir:     t.TempDir(),
	}

	result, err := InvokeTier1(context.Background(), ac, `echo "stderr msg" >&2 && exit 2`)
	require.NoError(t, err)
	assert.Equal(t, 2, result.ExitCode)
	assert.Equal(t, "stderr msg", result.Stderr)
}

func TestInvokeTier1_ContextSubstitution(t *testing.T) {
	skipIfShort(t)
	ac := &AdapterContext{
		TaskID:      "task-t1ctx",
		Command:     "create",
		Reference:   "JIRA-CTX",
		Context:     "test context content",
		ContextFile: "/path/to/notes.md",
		ProjectRoot: t.TempDir(),
		WorkDir:     t.TempDir(),
	}

	// Template uses unquoted placeholders — ShellEscape handles quoting
	result, err := InvokeTier1(context.Background(), ac, `echo ctx={context} file={context_file}`)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "ctx=test context content")
	assert.Contains(t, result.Stdout, "file=/path/to/notes.md")
}

func TestInvokeTier1_GuidesSubstitution(t *testing.T) {
	skipIfShort(t)
	ac := &AdapterContext{
		TaskID:      "task-t1guides",
		Command:     "create",
		Reference:   "JIRA-GUIDES",
		Guides:      "## Template\nUse markdown format\n",
		ProjectRoot: t.TempDir(),
		WorkDir:     t.TempDir(),
	}

	result, err := InvokeTier1(context.Background(), ac, `echo {guides}`)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "## Template")
	assert.Contains(t, result.Stdout, "Use markdown format")
}

func TestShellEscape(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty string", "", "''"},
		{"safe value", "JIRA-456", "JIRA-456"},
		{"safe path", "test-cases/foo.md", "test-cases/foo.md"},
		{"safe with numbers", "task-a3f72b1", "task-a3f72b1"},
		{"safe with equals", "key=value", "key=value"},
		{"spaces", "hello world", "'hello world'"},
		{"semicolon injection", "JIRA-123; rm -rf /", "'JIRA-123; rm -rf /'"},
		{"pipe injection", "a | b", "'a | b'"},
		{"ampersand injection", "a && b", "'a && b'"},
		{"dollar expansion", "$(whoami)", "'$(whoami)'"},
		{"backtick expansion", "`whoami`", "'`whoami`'"},
		{"double quotes", `say "hello"`, `'say "hello"'`},
		{"single quotes", "it's here", "'it'\\''s here'"},
		{"newline", "line1\nline2", "'line1\nline2'"},
		{"exclamation", "hello!", "'hello!'"},
		{"hash", "comment # here", "'comment # here'"},
		{"redirect", "file > /dev/null", "'file > /dev/null'"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ShellEscape(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestInvokeTier1_CommandInjectionPrevented(t *testing.T) {
	skipIfShort(t)
	ac := &AdapterContext{
		TaskID:      "task-inject",
		Command:     "create",
		Reference:   "JIRA-123; echo INJECTED",
		ProjectRoot: t.TempDir(),
		WorkDir:     t.TempDir(),
	}

	// The semicolon in the source should NOT cause a second command to execute
	result, err := InvokeTier1(context.Background(), ac, `echo src={reference}`)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	// The full string including the semicolon should appear as literal text
	assert.Contains(t, result.Stdout, "JIRA-123; echo INJECTED")
}

func TestInvokeTier1_DoubleQuotesInPrompt(t *testing.T) {
	skipIfShort(t)
	ac := &AdapterContext{
		TaskID:          "task-dquote",
		Command:         "create",
		AssembledPrompt: `Generate test cases for "checkout flow" with "guest user" scenario.`,
		ProjectRoot:     t.TempDir(),
		WorkDir:         t.TempDir(),
	}

	// Double quotes in the prompt should not break shell parsing
	result, err := InvokeTier1(context.Background(), ac, `echo {prompt}`)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "checkout flow")
	assert.Contains(t, result.Stdout, "guest user")
}

func TestInvokeTier1_MultiLinePrompt(t *testing.T) {
	skipIfShort(t)
	ac := &AdapterContext{
		TaskID:          "task-multiline",
		Command:         "create",
		AssembledPrompt: "line one\nline two\nline three",
		ProjectRoot:     t.TempDir(),
		WorkDir:         t.TempDir(),
	}

	// Multi-line prompt content should be passed safely
	result, err := InvokeTier1(context.Background(), ac, `echo {prompt}`)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "line one")
}

func TestInvokeTier1_CommandInjectionPipePrevented(t *testing.T) {
	skipIfShort(t)
	ac := &AdapterContext{
		TaskID:      "task-pipe",
		Command:     "create",
		Reference:   "REQ-1 | cat /etc/passwd",
		ProjectRoot: t.TempDir(),
		WorkDir:     t.TempDir(),
	}

	result, err := InvokeTier1(context.Background(), ac, `echo src={reference}`)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "REQ-1 | cat /etc/passwd")
	// The pipe should NOT have executed — output must not contain /etc/passwd contents
	assert.NotContains(t, result.Stdout, "root:")
}

func TestInvokeTier1_CommandInjectionBacktickPrevented(t *testing.T) {
	skipIfShort(t)
	ac := &AdapterContext{
		TaskID:      "task-backtick",
		Command:     "create",
		Reference:   "REQ-`whoami`",
		ProjectRoot: t.TempDir(),
		WorkDir:     t.TempDir(),
	}

	result, err := InvokeTier1(context.Background(), ac, `echo src={reference}`)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	// The backticks should be literal, not executed
	assert.Contains(t, result.Stdout, "REQ-`whoami`")
}

func TestInvokeTier1_CommandInjectionDollarPrevented(t *testing.T) {
	skipIfShort(t)
	ac := &AdapterContext{
		TaskID:      "task-dollar",
		Command:     "create",
		Reference:   "REQ-$(echo PWNED)",
		ProjectRoot: t.TempDir(),
		WorkDir:     t.TempDir(),
	}

	result, err := InvokeTier1(context.Background(), ac, `echo src={reference}`)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "REQ-$(echo PWNED)")
	// The subshell should NOT have executed
	assert.NotEqual(t, "src=REQ-PWNED", result.Stdout)
}

func TestInvokeTier1_SafeValuesUnchangedWithQuotedTemplate(t *testing.T) {
	skipIfShort(t)
	// Test the "legacy" double-quoted template pattern from existing adapter configs.
	// Safe values (no metacharacters) pass through ShellEscape unchanged,
	// so double-quoted templates continue to work identically.
	ac := &AdapterContext{
		TaskID:      "task-legacy",
		Command:     "create",
		Reference:   "JIRA-456",
		ProjectRoot: t.TempDir(),
		WorkDir:     t.TempDir(),
	}

	result, err := InvokeTier1(context.Background(), ac, `echo "src={reference}"`)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, "src=JIRA-456", result.Stdout)
}

func TestInvokeTier1_MockTier1Output(t *testing.T) {
	skipIfShort(t)
	ac := &AdapterContext{
		TaskID:      "task-t1mock",
		Command:     "create",
		Reference:   "JIRA-456",
		ProjectRoot: t.TempDir(),
		WorkDir:     t.TempDir(),
	}

	result, err := InvokeTier1(context.Background(), ac, `echo "mock tier1 output"`)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, "mock tier1 output", result.Stdout)
}

func TestInvokeTier1_ContextTimeout(t *testing.T) {
	skipIfShort(t)
	ac := &AdapterContext{
		TaskID:      "task-t1timeout",
		Command:     "create",
		Reference:   "JIRA-TIMEOUT",
		ProjectRoot: t.TempDir(),
		WorkDir:     t.TempDir(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	// Use "exec sleep" so sleep replaces the shell process — killing the process
	// kills sleep directly (no orphaned child keeping the pipe open on Windows).
	result, err := InvokeTier1(ctx, ac, "exec sleep 30")
	elapsed := time.Since(start)

	// Process should be killed well before the 30s sleep completes.
	assert.Less(t, elapsed, 15*time.Second, "should have been killed by context timeout, not run full 30s")

	// Either an error is returned or result has non-zero exit code
	if err != nil {
		// System-level error from context cancellation is acceptable
		return
	}
	assert.NotEqual(t, 0, result.ExitCode, "should have non-zero exit code after timeout")
}

func TestInvokeTier1_PromptFileSubstitution(t *testing.T) {
	skipIfShort(t)
	ac := &AdapterContext{
		TaskID:      "task-t1pf",
		Command:     "create",
		Reference:   "JIRA-PF",
		PromptFile:  "/tmp/test-prompt.md",
		ProjectRoot: t.TempDir(),
		WorkDir:     t.TempDir(),
	}

	result, err := InvokeTier1(context.Background(), ac, `echo "file={prompt_file}"`)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "/tmp/test-prompt.md")
}

func TestInvokeTier1_StdinDelivery(t *testing.T) {
	skipIfShort(t)
	ac := &AdapterContext{
		TaskID:          "task-stdin1",
		Command:         "create",
		AssembledPrompt: "prompt content via stdin",
		ProjectRoot:     t.TempDir(),
		WorkDir:         t.TempDir(),
	}

	// 'cat' reads from stdin and outputs to stdout
	result, err := InvokeTier1(context.Background(), ac, "cat")
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "prompt content via stdin")
}

func TestBUG005_LargePromptDoesNotExceedCmdLimit(t *testing.T) {
	skipIfShort(t)
	largePrompt := strings.Repeat("x", 40000)

	ac := &AdapterContext{
		TaskID:          "task-bug005",
		Command:         "create",
		AssembledPrompt: largePrompt,
		PromptFile:      "/tmp/large-prompt.md",
		ProjectRoot:     t.TempDir(),
		WorkDir:         t.TempDir(),
	}

	// 'wc -c' counts bytes from stdin — verifies full content received
	result, err := InvokeTier1(context.Background(), ac, "wc -c")
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "40000")
}

func TestInvokeTier1_PromptDeprecatedStillWorks(t *testing.T) {
	skipIfShort(t)
	ac := &AdapterContext{
		TaskID:          "task-deprecated",
		Command:         "create",
		AssembledPrompt: "short prompt",
		ProjectRoot:     t.TempDir(),
		WorkDir:         t.TempDir(),
	}

	result, err := InvokeTier1(context.Background(), ac, `echo {prompt}`)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "short prompt")
}

func TestInvokeTier1_NoStdinWhenPromptEmpty(t *testing.T) {
	skipIfShort(t)
	ac := &AdapterContext{
		TaskID:      "task-nostdin",
		Command:     "create",
		Reference:   "JIRA-NOSTDIN",
		ProjectRoot: t.TempDir(),
		WorkDir:     t.TempDir(),
	}

	// echo should work fine without stdin - no assembled prompt means no stdin piping
	result, err := InvokeTier1(context.Background(), ac, `echo "no prompt"`)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, "no prompt", result.Stdout)
}

func TestInvokeTier1_EnvironmentSubstitution(t *testing.T) {
	skipIfShort(t)
	ac := &AdapterContext{
		TaskID:      "task-t1env",
		Command:     "automate",
		TestCase:    "tc-001",
		Environment: "staging",
		ProjectRoot: t.TempDir(),
		WorkDir:     t.TempDir(),
	}

	result, err := InvokeTier1(context.Background(), ac, `echo "env={environment}"`)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "env=staging")
}

func TestInvokeTier1_OutputSubdirSubstitution(t *testing.T) {
	skipIfShort(t)
	ac := &AdapterContext{
		TaskID:       "task-t1subdir",
		Command:      "automate",
		TestCase:     "tc-001",
		OutputSubdir: "cwd-scoping/",
		ProjectRoot:  t.TempDir(),
		WorkDir:      t.TempDir(),
	}

	result, err := InvokeTier1(context.Background(), ac, `echo "subdir={output_subdir}"`)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "subdir=cwd-scoping/")
}

func TestInvokeTier1_OutputSubdirEmpty(t *testing.T) {
	skipIfShort(t)
	ac := &AdapterContext{
		TaskID:       "task-t1subdirE",
		Command:      "automate",
		TestCase:     "tc-001",
		OutputSubdir: "",
		ProjectRoot:  t.TempDir(),
		WorkDir:      t.TempDir(),
	}

	// Empty string is shell-escaped to '' by ShellEscape, so the substituted
	// command becomes: echo "subdir=''". This is correct Tier 1 behavior.
	// In practice, prompt templates (not command templates) use {output_subdir}.
	result, err := InvokeTier1(context.Background(), ac, `echo "subdir={output_subdir}"`)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "subdir=")
}

func TestInvokeTier1_TCIDsSubstitution(t *testing.T) {
	skipIfShort(t)
	ac := &AdapterContext{
		TaskID:      "task-t1tcids",
		Command:     "create",
		Reference:   "JIRA-TCIDS",
		TestCaseIDs: "tc-a1b2c3d,tc-e4f5a6b,tc-c7d8e9f",
		ProjectRoot: t.TempDir(),
		WorkDir:     t.TempDir(),
	}

	result, err := InvokeTier1(context.Background(), ac, `echo "ids={tc_ids}"`)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "ids=tc-a1b2c3d,tc-e4f5a6b,tc-c7d8e9f")
}

func TestInvokeTier1_ContextCancellation(t *testing.T) {
	skipIfShort(t)
	ac := &AdapterContext{
		TaskID:      "task-t1cancel",
		Command:     "create",
		Reference:   "JIRA-CANCEL",
		ProjectRoot: t.TempDir(),
		WorkDir:     t.TempDir(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)

	start := time.Now()
	// Use "exec sleep" so sleep replaces the shell process — killing the process
	// kills sleep directly (no orphaned child keeping the pipe open on Windows).
	result, err := InvokeTier1(ctx, ac, "exec sleep 30")
	elapsed := time.Since(start)

	// Process should be killed well before the 30s sleep completes.
	assert.Less(t, elapsed, 15*time.Second, "should have been killed by context cancellation, not run full 30s")

	// Either an error is returned or result has non-zero exit code
	if err != nil {
		return
	}
	assert.NotEqual(t, 0, result.ExitCode, "should have non-zero exit code after cancellation")
}

// --- ENH-009: Shell detection tests ---

func TestResolveShell_ShAvailable(t *testing.T) {
	// On this system sh is available; resolveShell should return sh/-c
	shell, flag, err := resolveShell()
	require.NoError(t, err)
	assert.Equal(t, "sh", shell)
	assert.Equal(t, "-c", flag)
}

func TestResolveShell_ShMissing_Windows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only test")
	}

	// Override lookPath to simulate sh not being on PATH
	origLookPath := lookPath
	lookPath = func(name string) (string, error) {
		return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
	}
	defer func() { lookPath = origLookPath }()

	shell, flag, err := resolveShell()
	require.NoError(t, err)
	assert.Equal(t, "cmd", shell)
	assert.Equal(t, "/c", flag)
}

func TestResolveShell_ShMissing_NonWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("non-Windows-only test")
	}

	origLookPath := lookPath
	lookPath = func(name string) (string, error) {
		return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
	}
	defer func() { lookPath = origLookPath }()

	_, _, err := resolveShell()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "POSIX shell (sh) not found")
}

func TestInvokeTier1_ShMissing_ReturnsError_NonWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("non-Windows-only test")
	}

	origLookPath := lookPath
	lookPath = func(name string) (string, error) {
		return "", errors.New("not found")
	}
	defer func() { lookPath = origLookPath }()

	ac := &AdapterContext{
		TaskID:      "task-nosh",
		Command:     "create",
		Reference:   "REQ-1",
		ProjectRoot: t.TempDir(),
		WorkDir:     t.TempDir(),
	}

	_, err := InvokeTier1(context.Background(), ac, `echo "hello"`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot execute Tier 1 adapter")
}
