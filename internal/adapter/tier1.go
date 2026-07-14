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

// quoteState describes the POSIX shell quoting context at a byte position in a
// Tier 1 command template.
type quoteState int

const (
	quoteNone quoteState = iota
	quoteSingle
	quoteDouble
)

// quoteStates returns the POSIX shell quoting state at every byte index of s,
// and reports whether s ends outside any quote (balanced).
//
// It models the three rules that matter for BUG-154 detection:
//
//   - Outside quotes, a backslash escapes the next character, so \" is a literal
//     quote character and NOT a state change.
//   - Inside single quotes, backslash is NOT special (POSIX). Only ' closes.
//   - Inside double quotes, a backslash escapes the next character, so \" stays
//     inside. Only an unescaped " closes.
//
// The backslash rule is load-bearing. GTMS's own shipped adapter commands embed
// escaped quotes inside a double-quoted prose segment (<gtms-file name=\"...\">)
// and then place a BARE {prompt_file} after it. A scanner that treated each \"
// as a state change would read that trailing placeholder as "inside quotes" and
// warn on GTMS's own correct configuration.
//
// balanced == false means the template ends inside an unterminated quote. The
// caller MUST then stay silent: the shape is malformed or exotic, the scanner
// cannot tell where the placeholder really sits, and BUG-154 requires the
// heuristic to under-warn rather than over-warn.
// quoteContext holds per-byte analysis of a command template: which quoting context
// each byte sits in, and whether it is inside a command substitution ($(...)).
type quoteContext struct {
	states  []quoteState
	inSubst []bool
}

func analyzeQuoting(s string) (ctx quoteContext, balanced bool) {
	n := len(s)
	ctx.states = make([]quoteState, n)
	ctx.inSubst = make([]bool, n)
	st := quoteNone

	// substDepth tracks nested $(...) regions. A positive depth means we are
	// inside at least one command substitution. Each $( pushes; each ) at
	// positive depth pops. This is tracked separately from the quote-state
	// machine because $(...) resets the quoting context for its interior while
	// the outer quote state suspends and resumes when the substitution closes.
	//
	// We track it as a simple counter because we only need "is this byte inside
	// a substitution, yes/no" -- we do not need to know whether the *inner*
	// quoting is balanced (the outer shell handles that).
	substDepth := 0

	for i := 0; i < n; i++ {
		ctx.states[i] = st
		ctx.inSubst[i] = substDepth > 0

		switch st {
		case quoteNone:
			switch s[i] {
			case '\\':
				if i+1 < n {
					i++
					ctx.states[i] = st
					ctx.inSubst[i] = substDepth > 0
				}
			case '\'':
				st = quoteSingle
			case '"':
				st = quoteDouble
			case '$':
				if i+1 < n && s[i+1] == '(' {
					substDepth++
					i++
					ctx.states[i] = st
					ctx.inSubst[i] = substDepth > 0
				}
			case ')':
				if substDepth > 0 {
					substDepth--
				}
			}
		case quoteSingle:
			// POSIX: no escape processing inside single quotes.
			if s[i] == '\'' {
				st = quoteNone
			}
		case quoteDouble:
			switch s[i] {
			case '\\':
				if i+1 < n {
					i++
					ctx.states[i] = st
					ctx.inSubst[i] = substDepth > 0
				}
			case '"':
				st = quoteNone
			case '$':
				if i+1 < n && s[i+1] == '(' {
					substDepth++
					i++
					ctx.states[i] = st
					ctx.inSubst[i] = substDepth > 0
				}
			case ')':
				if substDepth > 0 {
					substDepth--
				}
			}
		}
	}
	return ctx, st == quoteNone
}

// warnQuotedPlaceholders scans commandTemplate for RECOGNISED {placeholder} tokens
// that sit inside a shell-quoted segment, and warns that GTMS already escapes each
// value as a complete shell token (BUG-154).
//
// Tier 1 substitution passes every value through ShellEscape, which yields ONE
// complete POSIX token (bare when safe, single-quoted when not, '' when empty).
// The only composition that works is therefore a bare placeholder. When an author
// wraps one in shell quotes, ShellEscape's quotes become literal characters inside
// the author's word: the adapter writes to a bogus location, exits 0, and GTMS
// records a false pass.
//
// Deliberate constraints, each pinned by a canonical test case:
//
//   - Only RECOGNISED placeholders warn. An unrecognised token is never substituted,
//     so it is never escaped, so it cannot collide with the author's quotes -- that
//     is warnUnrecognizedVars' business (BUG-076), not ours. The two diagnostics
//     never double-fire on the same token.
//   - Warnings are deduplicated by name, so one offending placeholder yields exactly
//     one diagnostic.
//   - A bare placeholder is NEVER named, even when a quoted sibling in the same
//     template is. Telling an author to unquote something already bare sends them to
//     fix working code. This is also why we do not print a "valid variables" list the
//     way warnUnrecognizedVars does -- that list would name every bare placeholder.
//   - Ambiguous (unbalanced) quoting stays silent. See analyzeQuoting.
//   - A placeholder inside a $(...) command substitution is suppressed. The placeholder's
//     real quoting context is the inner shell of the substitution, not the outer double
//     quotes, and GTMS's escaping is correct there. The suppression is region-based: if
//     $(...) has already closed before the placeholder, the placeholder is back in the
//     outer context and DOES warn.
//
// Inspection is limited to the Tier 1 command: template. It must NEVER scan a
// prompt-template file or the assembled prompt: those use the same {placeholder}
// syntax but follow the deliberately unescaped textual-substitution contract, so
// quoted prose there is valid.
//
// The remedy text offers two routes:
//   - Prose interpolation -> move it to a prompt-template file, consumed as bare {prompt_file}.
//   - Shell argument composition or second-shell command -> use a Tier 2 adapter or wrapper
//     script, where the value arrives as a GTMS_ env var and normal shell quoting applies.
//
// Under the complete-token contract there is no way to build a multi-word argument
// containing an interpolated value in a Tier 1 command, because a bare {x} is its own
// token. That gap is why the dogfood config originally quoted {tc_name} inside prose.
// The remedy must name it honestly rather than blessing the quoted form.
func warnQuotedPlaceholders(w io.Writer, commandTemplate string, vars map[string]string) {
	ctx, balanced := analyzeQuoting(commandTemplate)
	if !balanced {
		// Cannot tell where the placeholder sits. Stay silent (BUG-154).
		return
	}

	seen := make(map[string]bool)
	var quoted []string
	for _, m := range templateVarPattern.FindAllStringSubmatchIndex(commandTemplate, -1) {
		open := m[0]
		name := commandTemplate[m[2]:m[3]]
		if _, known := vars[name]; !known {
			continue // unrecognised: BUG-076's business, never ours
		}
		if seen[name] {
			continue
		}
		// A placeholder is only warned about when:
		//   (a) it sits inside quotes (not quoteNone), AND
		//   (b) it is NOT inside a $(...) command substitution.
		if ctx.states[open] == quoteNone || ctx.inSubst[open] {
			continue
		}
		seen[name] = true
		quoted = append(quoted, name)
	}

	if len(quoted) == 0 {
		return
	}

	braced := make([]string, len(quoted))
	for i, name := range quoted {
		braced[i] = "{" + name + "}"
	}

	fmt.Fprintf(w, "⚠ Shell-quoted template variable(s): %s\n", strings.Join(braced, ", "))
	fmt.Fprintf(w, "    GTMS already shell-escapes each Tier 1 value as one complete shell token,\n")
	fmt.Fprintf(w, "    so shell quotes around a placeholder can end up inside the value as literal\n")
	fmt.Fprintf(w, "    quote characters. The adapter then writes to the wrong place and still exits 0.\n")
	fmt.Fprintf(w, "    Under the complete-token contract there is no Tier 1 way to interpolate a value\n")
	fmt.Fprintf(w, "    into a multi-word argument, because a bare placeholder is always its own token.\n")
	fmt.Fprintf(w, "    Two remedies, depending on what you are doing:\n")
	fmt.Fprintf(w, "    (a) Interpolating into prose: move it to a prompt-template file (textual,\n")
	fmt.Fprintf(w, "        unescaped substitution) and consume the result as bare {prompt_file}.\n")
	fmt.Fprintf(w, "    (b) Composing a shell argument or building a command for a second shell:\n")
	fmt.Fprintf(w, "        use a Tier 2 adapter or wrapper script, where the value arrives as a\n")
	fmt.Fprintf(w, "        GTMS_ env var and normal shell quoting applies.\n")
	fmt.Fprintf(w, "    Quoting the whole \"command:\" scalar in gtms.config is YAML syntax, and is fine.\n")
}

// adapterFacingPath prepares a project-relative path carrier for the external
// adapter. When ENH-168 working-dir has moved the run cwd off the project root,
// the relative carrier is absolutized so a runner in a subdir can still resolve
// the input it is handed.
//
// The result is ALWAYS forward-slash (BUG-126): the value is handed to a POSIX
// shell (sh -c / Tier-2 script) and on to CLIs (e.g. `npx playwright test`) that
// reject backslash paths on Windows. filepath.Join emits OS-native separators, so
// the join is normalized with filepath.ToSlash for the shell consumer. cmd.Dir
// (the cwd) is set separately and rightly stays OS-native -- only the carrier
// STRINGS need POSIX separators.
//
// Applied ONLY to the values handed to the adapter (Tier 1 {testcase_file} /
// {artefact_file} and the Tier 2 GTMS_ equivalents), never to the ctx fields
// themselves -- Tier-0 BuiltinAutomate consumes ctx.TestCaseFile as project-relative
// (filepath.Join(ProjectRoot, TestCaseFile)) and would break if it were absolutized.
//
// The RunDir == "" arm is defensive (RunDir is always set today). The live
// absolutization condition is RunDir != ProjectRoot -- i.e. working-dir is active.
// When it is not, a relative carrier is already forward-slash and ToSlash is a
// byte-for-byte no-op (no regression).
func adapterFacingPath(ac *AdapterContext, p string) string {
	if p == "" {
		return p
	}
	// Absolutize only when working-dir is active (RunDir != ProjectRoot). RunDir is
	// always set today; the live condition is RunDir != ProjectRoot.
	if !filepath.IsAbs(p) && ac.RunDir != "" && ac.RunDir != ac.ProjectRoot {
		p = filepath.Join(ac.ProjectRoot, filepath.FromSlash(p))
	}
	// BUG-126: normalize separators for the shell consumer. No-op on Unix.
	return filepath.ToSlash(p)
}

// tier1Vars builds the {placeholder} -> value map for Tier 1 substitution.
//
// It is the single definition of which placeholders are RECOGNISED. Both
// invocation-time diagnostics key off this map: warnUnrecognizedVars warns about
// tokens absent from it (BUG-076), and warnQuotedPlaceholders warns only about
// tokens present in it (BUG-154). Tests derive the recognised set from here rather
// than restating it, so a newly added placeholder cannot silently escape either
// diagnostic.
//
// Values here are RAW. ShellEscape is applied by the caller, after the diagnostics
// have run against the original template.
func tier1Vars(ac *AdapterContext) map[string]string {
	return map[string]string{
		"prompt":           ac.AssembledPrompt,
		"prompt_file":      ac.PromptFile,
		"artefact_file":    adapterFacingPath(ac, ac.ArtefactFile),
		"testcase_file":    adapterFacingPath(ac, ac.TestCaseFile),
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
}

// InvokeTier1 executes a Tier 1 adapter by substituting variables in the command
// template and running it via the shell. GTMS handles the result contract for Tier 1.
func InvokeTier1(ctx context.Context, ac *AdapterContext, commandTemplate string) (*InvocationResult, error) {
	vars := tier1Vars(ac)

	// Warn about unrecognized {variable} tokens in the command template (BUG-076).
	// Runs on the original commandTemplate BEFORE substitution so that literal
	// {foo} text in substituted values does not trigger false-positive warnings.
	warnUnrecognizedVars(os.Stderr, commandTemplate, vars)

	// Warn about recognised placeholders the author wrapped in shell quotes (BUG-154).
	// Also runs on the ORIGINAL template, before the ShellEscape loop below: quote
	// characters arriving inside a substituted VALUE are data, not author quoting, and
	// must never trigger this. Inspects the command: template only -- never a
	// prompt-template file or the assembled prompt.
	warnQuotedPlaceholders(os.Stderr, commandTemplate, vars)

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

	if ac.RunDir != "" {
		cmd.Dir = ac.RunDir
	}

	// Pipe assembled prompt via stdin for tools that read from stdin.
	if ac.AssembledPrompt != "" {
		cmd.Stdin = strings.NewReader(ac.AssembledPrompt)
	}

	return runAdapterProcess(cmd, filepath.Join(ac.OutputDir, ac.OutputSubdir), ac.Force)
}
