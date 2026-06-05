package adapter

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeFileInfo is a minimal os.FileInfo used by tests that stub osStat to
// report a (non-directory) file exists at a given path.
type fakeFileInfo struct{}

func (fakeFileInfo) Name() string       { return "sh.exe" }
func (fakeFileInfo) Size() int64        { return 0 }
func (fakeFileInfo) Mode() os.FileMode  { return 0 }
func (fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (fakeFileInfo) IsDir() bool        { return false }
func (fakeFileInfo) Sys() interface{}   { return nil }

// stubMissingSh overrides lookPath and osStat to simulate a system on which
// sh is neither on PATH nor present at any Git for Windows fallback location.
// Returns a cleanup func that restores the originals.
func stubMissingSh() func() {
	origLookPath := lookPath
	origOsStat := osStat
	lookPath = func(name string) (string, error) {
		return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
	}
	osStat = func(name string) (os.FileInfo, error) {
		return nil, os.ErrNotExist
	}
	return func() {
		lookPath = origLookPath
		osStat = origOsStat
	}
}

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
		{"safe path", "gtms/cases/foo.md", "gtms/cases/foo.md"},
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
	// On PATH, resolveShell now returns the absolute path (via resolveSh), not bare "sh".
	assert.Equal(t, "-c", flag)
	assert.NotEmpty(t, shell)
}

func TestResolveShell_ShMissing_Windows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only test")
	}

	// Simulate sh not on PATH AND no Git for Windows install → cmd /c fallback.
	defer stubMissingSh()()

	shell, flag, err := resolveShell()
	require.NoError(t, err)
	assert.Equal(t, "cmd", shell)
	assert.Equal(t, "/c", flag)
}

func TestResolveShell_ShViaGitForWindowsFallback_Windows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only test")
	}

	// sh not on PATH, but Git for Windows sh.exe present at a known location.
	// resolveShell must return that absolute path (not "sh", not "cmd").
	origLookPath := lookPath
	origOsStat := osStat
	lookPath = func(name string) (string, error) {
		return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
	}
	candidates := gitForWindowsShPaths()
	require.NotEmpty(t, candidates)
	target := candidates[0]
	osStat = func(name string) (os.FileInfo, error) {
		if name == target {
			return fakeFileInfo{}, nil
		}
		return nil, os.ErrNotExist
	}
	defer func() {
		lookPath = origLookPath
		osStat = origOsStat
	}()

	shell, flag, err := resolveShell()
	require.NoError(t, err)
	assert.Equal(t, target, shell)
	assert.Equal(t, "-c", flag)
}

func TestResolveShell_ShMissing_NonWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("non-Windows-only test")
	}

	defer stubMissingSh()()

	_, _, err := resolveShell()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "POSIX shell (sh) not found")
}

func TestInvokeTier1_ShMissing_ReturnsError_NonWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("non-Windows-only test")
	}

	origLookPath := lookPath
	origOsStat := osStat
	lookPath = func(name string) (string, error) {
		return "", errors.New("not found")
	}
	osStat = func(name string) (os.FileInfo, error) {
		return nil, os.ErrNotExist
	}
	defer func() {
		lookPath = origLookPath
		osStat = origOsStat
	}()

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

// TestGitBashDirsFromShPath_GitForWindowsLayout verifies that a shPath under a
// standard Git for Windows install expands to usr\bin + mingw64\bin + bin so
// coreutils are all reachable from the spawned shell (BUG-030 part 2).
func TestGitBashDirsFromShPath_GitForWindowsLayout(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only: backslash paths and Git-for-Windows layout are Windows concepts")
	}
	dirs := gitBashDirsFromShPath(`C:\Program Files\Git\usr\bin\sh.exe`)
	assert.Contains(t, dirs, `C:\Program Files\Git\usr\bin`)
	assert.Contains(t, dirs, `C:\Program Files\Git\mingw64\bin`)
	assert.Contains(t, dirs, `C:\Program Files\Git\bin`)
	assert.Equal(t, `C:\Program Files\Git\usr\bin`, dirs[0], "containing dir must come first")
}

// TestGitBashDirsFromShPath_NonGitLayout verifies that a non-Git-for-Windows
// shPath still yields at least the containing directory (no panics, no junk).
func TestGitBashDirsFromShPath_NonGitLayout(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only: test asserts on Windows-specific expansion behaviour")
	}
	// Use a path whose parent-of-bin is NOT "Git" — this must NOT expand into
	// fictitious mingw64/usr/bin siblings.
	shPath := filepath.Join("C:", "tools", "shbin", "bin", "sh.exe")
	dirs := gitBashDirsFromShPath(shPath)
	require.NotEmpty(t, dirs)
	assert.Equal(t, filepath.Dir(shPath), dirs[0])
	for _, d := range dirs[1:] {
		assert.NotContains(t, d, "mingw64", "non-Git layouts must not invent Git subdirs")
	}
}

// TestPrependPathEntries_PrependsToExistingPath verifies that new directories
// land at the front of an existing PATH entry, case-insensitively matched.
func TestPrependPathEntries_PrependsToExistingPath(t *testing.T) {
	env := []string{"FOO=bar", "Path=C:\\existing", "BAZ=qux"}
	out := prependPathEntries(env, []string{`C:\Program Files\Git\usr\bin`})
	sep := string(os.PathListSeparator)
	expected := "PATH=C:\\Program Files\\Git\\usr\\bin" + sep + "C:\\existing"
	found := false
	for _, e := range out {
		if e == expected {
			found = true
			break
		}
	}
	assert.True(t, found, "expected %q in env, got %v", expected, out)
	assert.Len(t, out, 3, "should not add a duplicate PATH entry")
}

// TestPrependPathEntries_AppendsWhenMissing verifies behaviour when the env
// has no PATH entry at all — rare but possible with stripped minimalEnv.
func TestPrependPathEntries_AppendsWhenMissing(t *testing.T) {
	env := []string{"FOO=bar"}
	out := prependPathEntries(env, []string{`/x`, `/y`})
	sep := string(os.PathListSeparator)
	assert.Contains(t, out, "PATH=/x"+sep+"/y")
}

// TestPrependPathEntries_EmptyDirsIsNoOp verifies callers on non-Windows paths
// pay no cost.
func TestPrependPathEntries_EmptyDirsIsNoOp(t *testing.T) {
	env := []string{"PATH=/usr/bin", "FOO=bar"}
	out := prependPathEntries(env, nil)
	assert.Equal(t, env, out)
}

// --- BUG-076: Unrecognized template variable warning tests ---

// TestWarnUnrecognizedVars_EmitsWarning verifies that a command template
// containing an unrecognized {variable} triggers a warning (AC1).
func TestWarnUnrecognizedVars_EmitsWarning(t *testing.T) {
	vars := map[string]string{
		"reference": "JIRA-123",
		"branch":    "feature/test",
	}
	var buf bytes.Buffer
	warnUnrecognizedVars(&buf, `echo {reference} {typo_var}`, vars)

	output := buf.String()
	assert.Contains(t, output, "typo_var", "warning should name the unrecognized variable")
	assert.Contains(t, output, "Unrecognized template variable", "warning should use standard preamble")
}

// TestWarnUnrecognizedVars_NamesVarsAndListsAlternatives verifies the warning
// includes both the unrecognized name(s) and the sorted list of valid
// alternatives (AC2).
func TestWarnUnrecognizedVars_NamesVarsAndListsAlternatives(t *testing.T) {
	vars := map[string]string{
		"artefact_file": "/path/to/file",
		"branch":        "main",
		"reference":     "REQ-1",
	}
	var buf bytes.Buffer
	warnUnrecognizedVars(&buf, `bats {artefact}`, vars)

	output := buf.String()
	// Should name the unrecognized variable in braces.
	assert.Contains(t, output, "{artefact}")
	// Should list valid alternatives in sorted order.
	assert.Contains(t, output, "artefact_file")
	assert.Contains(t, output, "branch")
	assert.Contains(t, output, "reference")
	// Verify sorted order: artefact_file, branch, reference.
	assert.Contains(t, output, "artefact_file, branch, reference")
}

// TestInvokeTier1_UnrecognizedVarWarnsButProceeds verifies that execution
// continues normally after the warning is emitted (AC3). The command succeeds
// even though it contains an unrecognized variable.
func TestInvokeTier1_UnrecognizedVarWarnsButProceeds(t *testing.T) {
	skipIfShort(t)
	ac := &AdapterContext{
		TaskID:      "task-bug076",
		Command:     "create",
		Reference:   "JIRA-BUG076",
		ProjectRoot: t.TempDir(),
		WorkDir:     t.TempDir(),
	}

	// {bogus_var} is not a known variable — it will be left as literal text.
	// The command should still execute and produce output.
	result, err := InvokeTier1(context.Background(), ac, `echo "ref={reference} bogus={bogus_var}"`)
	require.NoError(t, err, "InvokeTier1 should not return an error for unrecognized vars")
	assert.Equal(t, 0, result.ExitCode, "command should succeed despite unrecognized variable")
	assert.Contains(t, result.Stdout, "ref=JIRA-BUG076", "recognized variable should be substituted")
}

// TestWarnUnrecognizedVars_NoWarningWhenAllRecognized verifies no warning is
// emitted when every {variable} in the template matches a known key (AC4).
func TestWarnUnrecognizedVars_NoWarningWhenAllRecognized(t *testing.T) {
	vars := map[string]string{
		"reference": "JIRA-123",
		"branch":    "feature/test",
		"task_id":   "task-abc123",
	}
	var buf bytes.Buffer
	warnUnrecognizedVars(&buf, `echo {reference} {branch} {task_id}`, vars)

	assert.Empty(t, buf.String(), "no warning should be emitted when all variables are recognized")
}

// TestWarnUnrecognizedVars_ScansOriginalTemplate verifies that detection runs on
// the original commandTemplate string, not on the post-substitution result (AC5).
// If a substituted value contains literal {fake_var} text, it must NOT trigger
// a false-positive warning.
func TestWarnUnrecognizedVars_ScansOriginalTemplate(t *testing.T) {
	vars := map[string]string{
		"reference": "a value containing {fake_var} literal text",
		"branch":    "main",
	}
	var buf bytes.Buffer
	// The template only uses {reference} and {branch} — both are known.
	// The value of "reference" contains "{fake_var}" but since we scan the
	// TEMPLATE (not the substituted result), this must not trigger a warning.
	warnUnrecognizedVars(&buf, `echo {reference} {branch}`, vars)

	assert.Empty(t, buf.String(), "should not warn about {fake_var} in a substituted value")
}

// TestWarnUnrecognizedVars_AlternativesFromVarsMap verifies that the list of
// valid alternatives in the warning comes from the vars map keys at runtime,
// not a hard-coded list (AC6).
func TestWarnUnrecognizedVars_AlternativesFromVarsMap(t *testing.T) {
	// Use a small, custom vars map — NOT the full InvokeTier1 vars.
	// This proves the alternatives are derived from whatever map is passed.
	vars := map[string]string{
		"zebra":   "z",
		"apple":   "a",
		"mango":   "m",
	}
	var buf bytes.Buffer
	warnUnrecognizedVars(&buf, `echo {unknown}`, vars)

	output := buf.String()
	require.NotEmpty(t, output, "should emit a warning for {unknown}")
	// Alternatives should be alpha-sorted and match exactly the map keys.
	assert.Contains(t, output, "apple, mango, zebra")
	// Must NOT contain the unrecognized var in the alternatives list.
	// The word "unknown" appears in the unrecognized section but not in Valid variables.
	lines := strings.Split(strings.TrimSpace(output), "\n")
	require.Len(t, lines, 2, "warning should be exactly two lines")
	assert.Contains(t, lines[0], "{unknown}", "first line names the unrecognized variable")
	assert.NotContains(t, lines[1], "unknown", "alternatives line must not include unrecognized var")
	assert.Contains(t, lines[1], "apple, mango, zebra", "alternatives should be sorted vars map keys")
}
