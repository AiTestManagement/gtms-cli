package adapter

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/prompt"
)

// lookPath is a package-level variable wrapping exec.LookPath.
// Tests can override it to simulate sh being absent on PATH.
var lookPath = exec.LookPath

// osStat is a package-level variable wrapping os.Stat.
// Tests can override it to simulate the presence or absence of candidate
// sh binaries (e.g. Git for Windows fallback paths) without touching the
// real filesystem.
var osStat = os.Stat

// gitForWindowsShPaths returns candidate absolute paths for sh.exe on Windows
// when it is not on PATH. Ordered with %ProgramFiles%-derived paths first so
// custom install drives are honored, then hardcoded C: paths as a last resort.
func gitForWindowsShPaths() []string {
	var paths []string
	for _, envKey := range []string{"ProgramFiles", "ProgramW6432", "ProgramFiles(x86)"} {
		if base := os.Getenv(envKey); base != "" {
			paths = append(paths,
				filepath.Join(base, "Git", "usr", "bin", "sh.exe"),
				filepath.Join(base, "Git", "bin", "sh.exe"),
			)
		}
	}
	paths = append(paths,
		`C:\Program Files\Git\usr\bin\sh.exe`,
		`C:\Program Files\Git\bin\sh.exe`,
		`C:\Program Files (x86)\Git\usr\bin\sh.exe`,
		`C:\Program Files (x86)\Git\bin\sh.exe`,
	)
	return paths
}

// resolveSh returns an absolute path to an sh executable, or an error if none
// can be located. It first consults PATH via lookPath. On Windows, if PATH
// lookup fails, it probes known Git for Windows install locations — Git for
// Windows ships sh.exe but does not add its usr/bin directory to PATH, so
// PowerShell and cmd.exe invocations of gtms cannot find it without this
// fallback (BUG-030).
//
// The returned path MUST be used verbatim when constructing an exec.Cmd — do
// not pass the bare string "sh" to exec.CommandContext after calling this,
// because os/exec re-runs PATH lookup at spawn time and will still fail.
func resolveSh() (string, error) {
	if p, err := lookPath("sh"); err == nil {
		return p, nil
	}
	if runtime.GOOS == "windows" {
		for _, candidate := range gitForWindowsShPaths() {
			fi, err := osStat(candidate)
			if err == nil && !fi.IsDir() {
				return candidate, nil
			}
		}
	} else {
		// BUG-111: when PATH does not surface sh (e.g. a deliberately
		// restricted PATH set up by a Tier 2 missing-tooling simulation),
		// probe the canonical POSIX shell locations so GTMS itself stays
		// usable while letting the adapter script discover whatever it
		// needs (npx, node, etc.) on the supplied PATH. Mirrors the
		// Git for Windows fallback that exists for the Windows arm.
		for _, candidate := range []string{"/bin/sh", "/usr/bin/sh"} {
			fi, err := osStat(candidate)
			if err == nil && !fi.IsDir() {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("POSIX shell (sh) not found on PATH")
}

// gitBashDirsFromShPath derives the Git for Windows directories whose contents
// must be on PATH for a resolved sh.exe to function. sh.exe alone is useless
// without its sibling coreutils (mkdir, cat, cp, rm, ...) which live in the
// same usr\bin directory, and many scripts also depend on mingw64\bin utilities.
//
// Given a path like "C:\Program Files\Git\usr\bin\sh.exe", returns
//
//	["C:\Program Files\Git\usr\bin",
//	 "C:\Program Files\Git\mingw64\bin",
//	 "C:\Program Files\Git\bin"]
//
// Non-Git-for-Windows shPaths (e.g. WSL or a bare sh on PATH in Git Bash)
// still get at least the containing directory returned.
func gitBashDirsFromShPath(shPath string) []string {
	shDir := filepath.Dir(shPath)
	dirs := []string{shDir}
	// Git-for-Windows layout only applies on Windows. On other platforms a
	// bare /usr/bin/sh path must NOT be expanded to fictitious mingw64 dirs.
	if runtime.GOOS != "windows" {
		return dirs
	}
	// Walk up looking for a Git-for-Windows-shaped layout:
	//   <GitRoot>\usr\bin\sh.exe
	//   <GitRoot>\bin\sh.exe
	parent := filepath.Dir(shDir)
	gitRoot := ""
	if strings.EqualFold(filepath.Base(shDir), "bin") {
		candidate := parent
		if strings.EqualFold(filepath.Base(parent), "usr") {
			candidate = filepath.Dir(parent)
		}
		// Only expand if the candidate install root is named "Git". This keeps
		// the expansion targeted at Git for Windows and avoids inventing
		// subdirectories under unrelated "\foo\bin" paths.
		if strings.EqualFold(filepath.Base(candidate), "Git") {
			gitRoot = candidate
		}
	}
	if gitRoot != "" {
		for _, sub := range []string{
			filepath.Join(gitRoot, "mingw64", "bin"),
			filepath.Join(gitRoot, "bin"),
			filepath.Join(gitRoot, "usr", "bin"),
		} {
			if sub == shDir {
				continue
			}
			dirs = append(dirs, sub)
		}
	}
	return dirs
}

// prependPathEntries returns env with the given directories prepended to the
// PATH variable (case-insensitive match on Windows). If no PATH entry exists,
// one is appended. Duplicates are not deduplicated — the OS resolves left-to-right
// and extra entries are harmless.
func prependPathEntries(env []string, dirs []string) []string {
	if len(dirs) == 0 {
		return env
	}
	sep := string(os.PathListSeparator)
	prefix := strings.Join(dirs, sep)
	result := make([]string, len(env))
	copy(result, env)
	for i, e := range result {
		if eq := strings.IndexByte(e, '='); eq > 0 {
			if strings.EqualFold(e[:eq], "PATH") {
				result[i] = "PATH=" + prefix + sep + e[eq+1:]
				return result
			}
		}
	}
	return append(result, "PATH="+prefix)
}

// resolveShell determines which shell to use for Tier 1 command execution.
// Returns the shell binary (absolute path when resolved via resolveSh) and
// its flag for passing a command string.
// When sh is resolvable (PATH or Git for Windows fallback), returns (shPath, "-c", nil).
// When sh cannot be resolved on Windows, falls back to ("cmd", "/c", nil).
// When sh cannot be resolved on non-Windows, returns an error.
func resolveShell() (string, string, error) {
	if shPath, err := resolveSh(); err == nil {
		return shPath, "-c", nil
	}
	if runtime.GOOS == "windows" {
		return "cmd", "/c", nil
	}
	return "", "", fmt.Errorf("POSIX shell (sh) not found on PATH")
}

// ensureMSYSPath converts Windows-style PATH entries to MSYS-style (e.g. C:\foo → /c/foo)
// and switches the delimiter from ; to :. This enables MSYS bash to resolve commands
// when launched from PowerShell or cmd where PATH uses native Windows format.
func ensureMSYSPath(env []string) []string {
	result := make([]string, len(env))
	copy(result, env)
	for i, e := range result {
		if strings.HasPrefix(strings.ToUpper(e), "PATH=") {
			winPath := e[5:]
			entries := strings.Split(winPath, ";")
			var msysEntries []string
			for _, entry := range entries {
				entry = strings.TrimSpace(entry)
				if entry == "" {
					continue
				}
				// Convert C:\foo\bar to /c/foo/bar
				entry = strings.ReplaceAll(entry, "\\", "/")
				if len(entry) >= 2 && entry[1] == ':' {
					entry = "/" + strings.ToLower(entry[:1]) + entry[2:]
				}
				msysEntries = append(msysEntries, entry)
			}
			result[i] = "PATH=" + strings.Join(msysEntries, ":")
			break
		}
	}
	return result
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

// templateVarPattern matches {variable} placeholders in a command template.
// Uses a broad [^}]+ match to catch any brace-delimited token, not just [a-z_]+,
// so that typos with unexpected characters are also detected.
var templateVarPattern = regexp.MustCompile(`\{([^}]+)\}`)

// warnUnrecognizedVars scans commandTemplate for {variable} placeholders that
// do not appear as keys in the vars map. If any are found, a warning is written
// to w naming the unrecognized variable(s) and listing valid alternatives.
// This catches typos such as {artefact} (should be {artefact_file}) at
// invocation time instead of producing opaque downstream failures (BUG-076).
func warnUnrecognizedVars(w io.Writer, commandTemplate string, vars map[string]string) {
	matches := templateVarPattern.FindAllStringSubmatch(commandTemplate, -1)
	if len(matches) == 0 {
		return
	}

	// Collect unrecognized variable names (deduplicated, in order of appearance).
	seen := make(map[string]bool)
	var unrecognized []string
	for _, m := range matches {
		name := m[1]
		if _, known := vars[name]; known {
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		unrecognized = append(unrecognized, name)
	}

	if len(unrecognized) == 0 {
		return
	}

	// Build sorted list of valid variable names from the vars map.
	valid := make([]string, 0, len(vars))
	for k := range vars {
		valid = append(valid, k)
	}
	sort.Strings(valid)

	// Format unrecognized names as {var1}, {var2} for clarity.
	braced := make([]string, len(unrecognized))
	for i, name := range unrecognized {
		braced[i] = "{" + name + "}"
	}

	fmt.Fprintf(w, "⚠ Unrecognized template variable(s): %s\n", strings.Join(braced, ", "))
	fmt.Fprintf(w, "    Valid variables: %s\n", strings.Join(valid, ", "))
}

// InvokeTier1 executes a Tier 1 adapter by substituting variables in the command
// template and running it via the shell. GTMS handles the result contract for Tier 1.
func InvokeTier1(ctx context.Context, ac *AdapterContext, commandTemplate string) (*InvocationResult, error) {
	// Substitute variables in command template
	vars := map[string]string{
		"prompt":           ac.AssembledPrompt,
		"prompt_file":      ac.PromptFile,
		"artefact_file":    ac.ArtefactFile,
		"testcase_file":    ac.TestCaseFile,
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
		"tc_name":          ac.TestCaseName,
	}

	// Warn about unrecognized {variable} tokens in the command template (BUG-076).
	// Runs on the original commandTemplate BEFORE substitution so that literal
	// {foo} text in substituted values does not trigger false-positive warnings.
	warnUnrecognizedVars(os.Stderr, commandTemplate, vars)

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

	// On Windows when using sh (PATH-resolved or fallback), fix up the child
	// environment so scripts can actually do work:
	//   1. Prepend Git for Windows dirs (usr\bin, mingw64\bin, bin) to PATH so
	//      mkdir/cat/cp/etc. are discoverable (BUG-030 part 2).
	//   2. Convert PATH to MSYS style (C:\foo → /c/foo, ; → :) so bash's
	//      internal command lookup works from PowerShell-inherited envs.
	// Skipped for cmd /c fallback since those tools aren't POSIX.
	if runtime.GOOS == "windows" && strings.EqualFold(filepath.Base(shell), "sh.exe") {
		env := prependPathEntries(os.Environ(), gitBashDirsFromShPath(shell))
		cmd.Env = ensureMSYSPath(env)
	}

	if ac.WorkDir != "" {
		cmd.Dir = ac.WorkDir
	}

	// Pipe assembled prompt via stdin for tools that read from stdin.
	if ac.AssembledPrompt != "" {
		cmd.Stdin = strings.NewReader(ac.AssembledPrompt)
	}

	return runAdapterProcess(cmd, filepath.Join(ac.OutputDir, ac.OutputSubdir), ac.Force)
}
