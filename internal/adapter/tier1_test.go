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
		{"safe path", "gtms/test/cases/foo.md", "gtms/test/cases/foo.md"},
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

// ---------------------------------------------------------------------------
// BUG-154: shell-quoted placeholder detection.
//
// Tier 1 substitution passes every value through ShellEscape, producing ONE
// complete POSIX token. Placeholders must therefore appear BARE in the command.
// An author who wraps one in shell quotes turns ShellEscape's quotes into literal
// characters, and the adapter silently writes to the wrong place and exits 0.
//
// These are pure string tests (no os/exec, no git), so they deliberately do NOT
// call skipIfShort and run in the smoke tier.
// ---------------------------------------------------------------------------

// bug154Vars returns the recognised-placeholder set, derived from the PRODUCTION
// map (tier1Vars) rather than restating it. A hand-copied key list would silently
// drift the moment a new placeholder was added to InvokeTier1, and the new one
// would escape both invocation-time diagnostics without any test going red.
func bug154Vars() map[string]string {
	return tier1Vars(&AdapterContext{})
}

// The two REAL shipped adapter commands, verbatim from gtms.config. These are the
// highest-value fixtures in this file: both place a bare {prompt_file} AFTER a long
// double-quoted prose segment containing ESCAPED quotes (\") and BEFORE a trailing
// empty quoted argument (--allowedTools ""). A naive quote-counting detector reads
// {prompt_file} as sitting inside quotes and false-warns on GTMS's own correct
// configuration from the very first run. These two tests fail loudly for any such
// implementation.
const (
	shippedPlaywrightScaffoldCommand = `claude -p "Read the system prompt. Generate a Playwright scaffold spec with test.fixme() for each scenario. Output using <gtms-file name=\"<filename>.spec.ts\"> tags, closed with </gtms-file>. No code fences. Raw text only." --append-system-prompt-file {prompt_file} --allowedTools ""`

	shippedCreateCommandPostMigration = `claude -p "Read the system prompt instructions. Create test cases from the source material. Output each test case using <gtms-file name=\"tc-<8-char-hex>-<short-slug>.md\"> tags, closed with </gtms-file>. YAML frontmatter then markdown body. No code fences. Raw text only." --append-system-prompt-file {prompt_file} --allowedTools ""`

	// The pre-migration create command, which BUG-154 repairs. {tc_name} sits inside
	// the double-quoted prose. Retained as a positive control: the detector must catch
	// the exact defect that was live in this repository's own gtms.config.
	shippedCreateCommandPreMigration = `claude -p "Read the system prompt instructions. Create test cases from the source material. If {tc_name} is non-empty, generate exactly one test case. Use the first ID from the pre-generated list and name the file <first-id>-{tc_name}.md. Set the title: frontmatter to a human-readable form of the name. Do not generate additional test cases. If {tc_name} is empty, generate one test case per distinct behavior using AI-chosen slugs. Output each test case using <gtms-file name=\"tc-<8-char-hex>-<short-slug>.md\"> tags, closed with </gtms-file>. YAML frontmatter then markdown body. No code fences. Raw text only." --append-system-prompt-file {prompt_file} --allowedTools ""`
)

func TestWarnQuotedPlaceholders(t *testing.T) {
	tests := []struct {
		name string
		tmpl string
		// wantNamed are placeholders the warning MUST name.
		wantNamed []string
		// wantAbsent are strings that must NOT appear anywhere in the output.
		// Used to prove a bare sibling is never blamed.
		wantAbsent []string
		// wantSilent asserts no warning at all is emitted.
		wantSilent bool
	}{
		// --- the positive matrix (tc-33745800) ---
		{name: "quoted double", tmpl: `sh probe.sh "{reference}"`, wantNamed: []string{"reference"}},
		{name: "quoted single", tmpl: `sh probe.sh '{reference}'`, wantNamed: []string{"reference"}},
		{name: "quoted suffix", tmpl: `sh probe.sh "{output_dir}/seen.txt"`, wantNamed: []string{"output_dir"}},
		{name: "quoted option assignment", tmpl: `sh probe.sh "--output={output_dir}"`, wantNamed: []string{"output_dir"}},
		{name: "quoted embedded", tmpl: `sh probe.sh "prefix-{reference}-suffix"`, wantNamed: []string{"reference"}},

		// --- bare forms: never warn ---
		{name: "bare standalone", tmpl: `sh probe.sh {reference}`, wantSilent: true},
		{name: "bare with suffix", tmpl: `mkdir -p {output_dir} && cat {testcase_file} > {output_dir}/seen.txt`, wantSilent: true},
		{name: "bare prompt_file", tmpl: `sh capture-prompt.sh {prompt_file}`, wantSilent: true},

		// --- precision guard (tc-ecb517aa): name the quoted one, never the bare sibling ---
		{
			name:       "quoted and bare sibling",
			tmpl:       `sh capture-args.sh "{reference}" {output_dir}`,
			wantNamed:  []string{"reference"},
			wantAbsent: []string{"output_dir"},
		},

		// --- diagnostic boundary (tc-7f54856a): unrecognised token is BUG-076's, not ours ---
		{name: "unrecognised token in quotes", tmpl: `sh probe.sh "{not_a_var}"`, wantSilent: true},
		{
			name:       "unrecognised quoted alongside recognised bare",
			tmpl:       `sh probe.sh "{not_a_var}" {reference}`,
			wantSilent: true,
		},

		// --- empty value is NOT exempt (tc-164a302a). The helper never sees values,
		//     so this falls out of the design; the row pins it against regression. ---
		{name: "quoted tc_name (empty at runtime)", tmpl: `sh capture-args.sh "name-is-{tc_name}-end" {prompt_file}`, wantNamed: []string{"tc_name"}},

		// --- dedup: one offending placeholder, one diagnostic ---
		{name: "same placeholder quoted twice", tmpl: `sh tool "{reference}" "{reference}"`, wantNamed: []string{"reference"}},
		// Ordering guard: a BARE occurrence before a QUOTED one must still warn. An
		// implementation that marked a name "seen" on the bare hit would miss this.
		{name: "bare occurrence before quoted occurrence", tmpl: `sh tool {reference} "{reference}"`, wantNamed: []string{"reference"}},

		// --- COMMAND SUBSTITUTION: suppressed (owner decision). A placeholder inside $()
		//     is valid and always works. ---
		{name: "command subst: matched", tmpl: `my-tool --note "$(cat {artefact_file})"`, wantSilent: true},
		{name: "command subst: nested", tmpl: `"$(echo $(basename {testcase_file}))"`, wantSilent: true},
		{name: "command subst: already closed", tmpl: `"$(date) {output_dir}"`, wantNamed: []string{"output_dir"}},
		{name: "command subst: bare outside quotes", tmpl: `$(cat {artefact_file})`, wantSilent: true},

		// --- STILL WARNS: multi-word argument and second shell. These are latently broken
		//     (they work only while the value is safe-charset). ---
		{name: "multi-word argument still warns", tmpl: `git commit -m "Automate {testcase}"`, wantNamed: []string{"testcase"}},
		{name: "nested shell still warns", tmpl: `ssh host "cd {output_dir} && bats ."`, wantNamed: []string{"output_dir"}},

		// --- ambiguity gate: unbalanced quoting stays silent, never guesses ---
		{name: "unbalanced double quote", tmpl: `sh tool "{reference}`, wantSilent: true},
		{name: "unbalanced single quote", tmpl: `sh tool '{output_dir}`, wantSilent: true},

		// --- escaping rules ---
		{name: "backslash outside quotes", tmpl: `C:\tools\my-tool {reference}`, wantSilent: true},
		{name: "apostrophe inside double-quoted prose", tmpl: `tool -p "Don't fence me in." {reference}`, wantSilent: true},
		{name: "trailing lone backslash does not panic or unbalance", tmpl: `tool {reference} \`, wantSilent: true},

		// --- multi-byte UTF-8: analyzeQuoting indexes BYTES and templateVarPattern returns
		//     BYTE offsets, so the two must stay in step. A rune-indexed scanner paired
		//     with byte offsets would desync here and blame the wrong token. ---
		{name: "bare placeholder after multi-byte quoted prose", tmpl: `tool --note "cafe unicode ✓ prose" {reference}`, wantSilent: true},
		{name: "quoted placeholder after multi-byte chars", tmpl: `tool --note "✓ {reference}"`, wantNamed: []string{"reference"}},

		// --- THE FALSE-POSITIVE KILL SWITCHES ---
		// Synthetic canary (tc-c79c123d): quoted prose + escaped quotes + empty quoted
		// arg, every placeholder bare.
		{
			name:       "quoted literals, escaped quotes, empty quoted arg, bare placeholders",
			tmpl:       `sh probe.sh --note "Emit using <gtms-file name=\"out.md\"> tags. No fences." --allowedTools "" {prompt_file} {reference}`,
			wantSilent: true,
		},
		// The real shipped adapters. If either of these warns, the fix has started
		// shouting at GTMS's own correct configuration.
		{name: "shipped playwright-scaffold automate command (tc-97b7e41c)", tmpl: shippedPlaywrightScaffoldCommand, wantSilent: true},
		{name: "shipped create command, post-migration (tc-b27c6885)", tmpl: shippedCreateCommandPostMigration, wantSilent: true},

		// Positive control: the same command BEFORE the migration must be caught.
		{
			name:       "shipped create command, pre-migration (the live defect)",
			tmpl:       shippedCreateCommandPreMigration,
			wantNamed:  []string{"tc_name"},
			wantAbsent: []string{"prompt_file"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			warnQuotedPlaceholders(&buf, tc.tmpl, bug154Vars())
			out := buf.String()

			if tc.wantSilent {
				assert.Empty(t, out, "expected no quoted-placeholder warning, got:\n%s", out)
				return
			}

			require.NotEmpty(t, out, "expected a quoted-placeholder warning, got none")
			// Exactly one diagnostic per invocation (one headline).
			assert.Equal(t, 1, strings.Count(out, "Shell-quoted template variable(s):"),
				"expected exactly one diagnostic headline")

			for _, name := range tc.wantNamed {
				assert.Contains(t, out, "{"+name+"}", "warning should name the quoted placeholder")
			}
			// wantAbsent names must not appear in the HEADLINE (the first line),
			// which is where the diagnostic names the offending placeholder. The
			// remedy body may mention any placeholder as an instructional example.
			headline := strings.SplitN(out, "\n", 2)[0]
			for _, name := range tc.wantAbsent {
				assert.NotContains(t, headline, "{"+name+"}",
					"the headline must not blame %q -- it is bare and correct", name)
			}

			// The guidance must be actionable (tc-33745800 step 3).
			assert.Contains(t, out, "complete shell token", "warning should explain GTMS already escapes the value")
			assert.Contains(t, out, "YAML", "warning should distinguish shell quotes from YAML quoting of the scalar")

			// Two-remedy text (owner decision, 2026-07-13): must NOT tell the author to
			// "keep the quotes"; must offer prompt-template and Tier 2 as the two routes.
			assert.NotContains(t, out, "Keep your quotes",
				"struck: this blessed a form that breaks silently the moment a value contains a space")
			assert.Contains(t, out, "prompt-template",
				"remedy (a) must name prompt-template for prose interpolation")
			assert.Contains(t, out, "Tier 2",
				"remedy (b) must name Tier 2 for shell-argument composition")
		})
	}
}

// TestWarnQuotedPlaceholders_DoesNotListValidVariables pins the precision guard at
// the level it is most likely to be broken. warnUnrecognizedVars prints a sorted
// "Valid variables:" line naming EVERY recognised placeholder. If warnQuotedPlaceholders
// copied that shape, its output would name output_dir even when output_dir is bare and
// correct -- failing tc-ecb517aa, which requires that no diagnostic mention the bare
// sibling.
func TestWarnQuotedPlaceholders_DoesNotListValidVariables(t *testing.T) {
	var buf bytes.Buffer
	warnQuotedPlaceholders(&buf, `sh capture-args.sh "{reference}" {output_dir}`, bug154Vars())
	out := buf.String()

	assert.NotContains(t, out, "Valid variables",
		"BUG-154 must not print the valid-variable list; that list names every bare placeholder")
	// Check headline only -- the remedy body may mention any variable as an instructional example.
	headline := strings.SplitN(out, "\n", 2)[0]
	assert.NotContains(t, headline, "{output_dir}",
		"the headline must not blame the bare sibling")
	assert.Contains(t, headline, "{reference}")
}

// TestWarnQuotedPlaceholders_NeverDoubleFiresWithBUG076 verifies the two Tier 1
// diagnostics stay disjoint on a single token (tc-7f54856a). An unrecognised token is
// never substituted, so it is never escaped, so it cannot collide with author quotes.
func TestWarnQuotedPlaceholders_NeverDoubleFiresWithBUG076(t *testing.T) {
	vars := bug154Vars()

	var quoted, unrecognized bytes.Buffer
	warnQuotedPlaceholders(&quoted, `sh probe.sh "{not_a_var}"`, vars)
	warnUnrecognizedVars(&unrecognized, `sh probe.sh "{not_a_var}"`, vars)

	assert.Empty(t, quoted.String(), "an unrecognised token must not raise the BUG-154 warning")
	assert.Contains(t, unrecognized.String(), "not_a_var", "it must still raise the BUG-076 warning")

	// And the mirror image: a recognised quoted placeholder raises BUG-154 only.
	quoted.Reset()
	unrecognized.Reset()
	warnQuotedPlaceholders(&quoted, `sh probe.sh "{reference}"`, vars)
	warnUnrecognizedVars(&unrecognized, `sh probe.sh "{reference}"`, vars)

	assert.Contains(t, quoted.String(), "{reference}", "a recognised quoted placeholder must raise BUG-154")
	assert.Empty(t, unrecognized.String(), "and must not raise the BUG-076 warning")
}

// TestWarnQuotedPlaceholders_WarningTextIsBATSSafe pins two literal constraints that
// existing acceptance tests depend on. Both are load-bearing:
//
//   - tier1-unrecognized-template-vars/tc-6d665b93 asserts
//     `grep -c 'Unrecognized template variable(s):' == 1` on stderr. If the BUG-154
//     message contained that literal, the count would become 2 and the test would go red.
//   - enh-092-create-output-lists-tcs/tc-75f84cf0 does line-POSITION arithmetic over
//     merged stdout+stderr using `grep -n` for "gtms automate" and a tc-<8hex> id.
func TestWarnQuotedPlaceholders_WarningTextIsBATSSafe(t *testing.T) {
	var buf bytes.Buffer
	warnQuotedPlaceholders(&buf, `sh probe.sh "{reference}"`, bug154Vars())
	out := buf.String()

	assert.NotContains(t, out, "Unrecognized template variable",
		"would inflate the BUG-076 warning count asserted by tc-6d665b93")
	assert.NotContains(t, out, "gtms automate",
		"would perturb the line-position arithmetic in tc-75f84cf0")
	assert.NotRegexp(t, `tc-[0-9a-f]{8}`, out,
		"would perturb the tc-id line lookup in tc-75f84cf0")
}

// TestQuoteStates_BalancedAndEscapes covers the scanner directly. The backslash rule
// inside double quotes is the load-bearing detail: without it, both shipped adapter
// commands false-warn.
func TestAnalyzeQuoting_BalancedAndEscapes(t *testing.T) {
	tests := []struct {
		name         string
		in           string
		wantBalanced bool
	}{
		{"plain text", `echo hello`, true},
		{"balanced double", `echo "hi"`, true},
		{"balanced single", `echo 'hi'`, true},
		{"empty quoted arg", `tool --allowedTools ""`, true},
		{"escaped quote inside double", `tool "name=\"x\" done"`, true},
		{"backslash outside quotes", `C:\tools\x`, true},
		{"apostrophe inside double quotes is inert", `tool "Don't"`, true},
		{"unterminated double", `echo "hi`, false},
		{"unterminated single", `echo 'hi`, false},
		{"shipped playwright command", shippedPlaywrightScaffoldCommand, true},
		{"shipped create command", shippedCreateCommandPostMigration, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, balanced := analyzeQuoting(tc.in)
			assert.Equal(t, tc.wantBalanced, balanced)
			assert.Len(t, ctx.states, len(tc.in), "one state per byte")
		})
	}
}

// TestAnalyzeQuoting_PlaceholderPositionInShippedCommands is the sharpest single assertion
// in this file. In BOTH shipped adapter commands, {prompt_file} sits AFTER a double-quoted
// prose segment that contains escaped quotes. It must be scanned as OUTSIDE quotes.
func TestAnalyzeQuoting_PlaceholderPositionInShippedCommands(t *testing.T) {
	for _, cmd := range []string{shippedPlaywrightScaffoldCommand, shippedCreateCommandPostMigration} {
		ctx, balanced := analyzeQuoting(cmd)
		require.True(t, balanced, "shipped command must scan as balanced")

		idx := strings.Index(cmd, "{prompt_file}")
		require.NotEqual(t, -1, idx, "shipped command should contain {prompt_file}")
		assert.Equal(t, quoteNone, ctx.states[idx],
			"{prompt_file} is bare in the shipped command and MUST scan as outside quotes; "+
				"if this fails, the scanner is mishandling the escaped \\\" sequences in the prose "+
				"and GTMS will warn about its own correct configuration")
	}

	// And the pre-migration create command: {tc_name} really is inside the prose.
	ctx, balanced := analyzeQuoting(shippedCreateCommandPreMigration)
	require.True(t, balanced)
	idx := strings.Index(shippedCreateCommandPreMigration, "{tc_name}")
	require.NotEqual(t, -1, idx)
	assert.Equal(t, quoteDouble, ctx.states[idx], "{tc_name} sat inside the quoted prose -- that was the defect")
}

// TestAnalyzeQuoting_CommandSubstitution covers the $(...) region tracking.
// Owner decision: a placeholder inside $(...) is valid (the inner shell parses GTMS's
// complete token correctly) and must NOT warn. The suppression must match properly
// balanced regions, handle nesting, and re-warn when $(...) has closed.
func TestAnalyzeQuoting_CommandSubstitution(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		idx       int // byte offset to check for inSubst
		wantSubst bool
	}{
		// matched: placeholder inside $(...) -- the outer quote state is still quoteDouble
		// but inSubst is true, so warnQuotedPlaceholders suppresses it.
		{"matched subst", `"$(cat {testcase_file})"`, 7, true},
		// nested: placeholder inside inner $(...)
		{"nested subst", `"$(echo $(basename {testcase_file}))"`, 18, true},
		// already closed: $() before placeholder -- back in outer context, inSubst false
		{"already closed", `"$(date) {output_dir}"`, 9, false},
		// bare subst outside quotes
		{"bare subst outside quotes", `$(cat {artefact_file})`, 6, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, balanced := analyzeQuoting(tc.in)
			require.True(t, balanced, "test input must be balanced")
			require.Less(t, tc.idx, len(ctx.inSubst), "index %d out of range for input len %d", tc.idx, len(tc.in))
			assert.Equal(t, tc.wantSubst, ctx.inSubst[tc.idx], "inSubst at byte %d", tc.idx)
		})
	}
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
