package adapter

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/prompt"
)

// lookPath is a package-level variable wrapping exec.LookPath.
// Tests can override it to simulate sh being absent on PATH.
var lookPath = exec.LookPath

// resolveShell determines which shell to use for Tier 1 command execution.
// Returns the shell binary name and its flag for passing a command string.
// When sh is available, returns ("sh", "-c", nil).
// When sh is absent on Windows, falls back to ("cmd", "/c", nil).
// When sh is absent on non-Windows, returns an error.
func resolveShell() (string, string, error) {
	_, err := lookPath("sh")
	if err == nil {
		return "sh", "-c", nil
	}
	if runtime.GOOS == "windows" {
		return "cmd", "/c", nil
	}
	return "", "", fmt.Errorf("POSIX shell (sh) not found on PATH")
}

// unsafePattern matches any character that is not safe for unquoted use in a POSIX shell.
var unsafePattern = regexp.MustCompile(`[^\w@%+=:,./-]`)

// ShellEscape returns a shell-escaped version of s, safe for use as a single
// token in a POSIX shell command line. Empty strings return ''. Strings
// containing only safe characters are returned unchanged. All other strings
// are wrapped in single quotes with internal single quotes escaped as '\''.
func ShellEscape(s string) string {
	if len(s) == 0 {
		return "''"
	}
	if !unsafePattern.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// InvokeTier1 executes a Tier 1 adapter by substituting variables in the command
// template and running it via the shell. GTMS handles the result contract for Tier 1.
func InvokeTier1(ctx context.Context, ac *AdapterContext, commandTemplate string) (*InvocationResult, error) {
	// Substitute variables in command template
	vars := map[string]string{
		"prompt":           ac.AssembledPrompt,
		"prompt_file":      ac.PromptFile,
		"spec_file":        ac.SpecFile,
		"output_dir":       ac.OutputDir,
		"output_subdir":    ac.OutputSubdir,
		"reference":        ac.Reference,
		"testcase":         ac.TestCase,
		"testcase_content": ac.TestCaseContent,
		"branch":           ac.Branch,
		"prompt_template":  ac.PromptTemplate,
		"repo":             ac.Repo,
		"task_id":          ac.TaskID,
		"result_file":      ac.ResultFile,
		"project_root":     ac.ProjectRoot,
		"work_dir":         ac.WorkDir,
		"focus":            ac.Focus,
		"context":          ac.Context,
		"context_file":     ac.ContextFile,
		"guides":           ac.Guides,
		"environment":      ac.Environment,
		"tc_ids":           ac.TestCaseIDs,
	}

	// Shell-escape all values to prevent command injection (BUG-001).
	// This ensures metacharacters in user-supplied values are treated as
	// literal strings when passed to sh -c.
	for key, value := range vars {
		vars[key] = ShellEscape(value)
	}

	command := prompt.AssembleString(commandTemplate, vars)

	// Resolve shell: use sh if available, fall back to cmd /c on Windows (ENH-009).
	// NOTE: ShellEscape above applies POSIX quoting; values with special chars may not
	// work correctly under cmd /c. Tracked by ENH-010. Safe for typical alphanumeric targets.
	shell, flag, err := resolveShell()
	if err != nil {
		return nil, fmt.Errorf("cannot execute Tier 1 adapter: %w", err)
	}

	cmd := exec.CommandContext(ctx, shell, flag, command)

	if ac.WorkDir != "" {
		cmd.Dir = ac.WorkDir
	}

	// Pipe assembled prompt via stdin for tools that read from stdin.
	if ac.AssembledPrompt != "" {
		cmd.Stdin = strings.NewReader(ac.AssembledPrompt)
	}

	return runAdapterProcess(cmd, filepath.Join(ac.OutputDir, ac.OutputSubdir))
}
