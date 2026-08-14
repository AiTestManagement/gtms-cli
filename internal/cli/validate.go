package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/target"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
)

// requireScopedSpec enforces the qualified-to-flat identity boundary for a
// folder-qualified single-TC target. It is the single guard every folder-aware
// command runs BEFORE reducing res.Target to the bare res.TCID for a flat
// wiring/task lookup or mutation, so strengthening it here protects every caller.
//
// For a QUALIFIED target (res.Target contains "/") it requires that the bare ID
// resolves to EXACTLY ONE project-wide spec, and that spec is under the requested
// folder:
//   - REV-113 CODEX-001: the spec must exist under the requested folder (else a
//     wrong-folder target would silently operate on the same-ID TC elsewhere).
//   - REV-120 CODEX-001: the bare ID must be UNIQUE project-wide. Cross-folder
//     duplicate IDs are not prevented today (tree-wide uniqueness ENH-100/ENH-101
//     are open), and the downstream mutation is flat/bare-ID -- so
//     `delete foo/tc-x` would also delete `bar/tc-x`. Abort BEFORE any mutation and
//     name every conflicting spec.
//
// Unqualified (bare) targets skip both checks -- the global/brownfield path is
// unchanged. Non-TC kinds are not applicable.
func requireScopedSpec(root string, res target.Result) error {
	if res.Kind != target.KindTCID {
		return nil
	}
	if !strings.Contains(res.Target, "/") {
		return nil // unqualified: global lookup, no scoping
	}

	// Ambiguity guard: a qualified target must resolve to a UNIQUE project-wide
	// spec, or the flat bare-ID operation would affect same-ID specs in other
	// folders (REV-120 CODEX-001).
	matches, err := testcase.FindAllSpecs(root, res.TCID)
	if err != nil {
		return err
	}
	if len(matches) > 1 {
		// REV-123 CODEX-001: the conflicting paths are filesystem-derived (checked-out
		// directory names), so sanitize each for display before echoing -- a control
		// or ANSI-escape character in a path must not manipulate the terminal.
		display := make([]string, len(matches))
		for i, m := range matches {
			display[i] = sanitizeControlChars(m)
		}
		return fmt.Errorf("test case ID '%s' is ambiguous -- it exists in %d places:\n    %s\n    Resolve the duplicate IDs (or use a form that targets a single spec) before targeting '%s'.",
			res.TCID, len(matches), strings.Join(display, "\n    "), res.Target)
	}

	// Scoped existence: the (unique) spec must live under the requested folder.
	if testcase.Exists(root, res.Target) {
		return nil
	}
	return fmt.Errorf("test case '%s' not found in %s/", res.Target, layout.Current().TestCases)
}

// invalidTargetFormatError builds the historical "Invalid test case ID format"
// compatibility response for a positional target that the shared classifier
// (target.Classify) rejected. The pre-BUG-167 commands overrode the raw
// validator error with this generic message, and ~15 frozen acceptance specs
// (bug-058-pathsafe-validation, tc-id-md-suffix-normalisation, etc.) assert the
// "Invalid test case ID format" substring. This keeps that substring while also
// surfacing the classifier's specific cause and the TC-format hint. Returns
// (message, hint) for output.Errorf; the hint carries its own 4-space indent for
// the second line since FprintError only indents the first.
func invalidTargetFormatError(rawArg string, cause error) (string, string) {
	msg := fmt.Sprintf("Invalid test case ID format: '%s'.", sanitizeForError(rawArg))
	hint := cause.Error() + "\n    Use format like tc-a1b2c3d4."
	return msg, hint
}

// The positional-target validators/normalisers below are thin delegations to the
// shared internal/target package (BUG-167). The authoritative implementations
// live there so every folder-aware command classifies and normalises a target
// identically. These unexported wrappers keep the existing cli call sites and
// white-box tests (validate_test.go, discover_test.go, gaps_test.go) unchanged.

// validateTargetID delegates to target.ValidateTargetID.
func validateTargetID(t string) error {
	return target.ValidateTargetID(t)
}

// validateFolderArg delegates to target.ValidateFolderArg.
func validateFolderArg(folder string) (string, error) {
	return target.ValidateFolderArg(folder)
}

// normaliseTarget delegates to target.NormaliseTarget.
func normaliseTarget(t string) string {
	return target.NormaliseTarget(t)
}

// filePathExtensions is the set of file extensions that suggest a value is a file path.
var filePathExtensions = map[string]bool{
	".md": true, ".txt": true, ".yaml": true, ".yml": true,
	".json": true, ".xml": true, ".csv": true, ".html": true,
	".go": true, ".py": true, ".js": true, ".ts": true,
}

// looksLikeFilePath returns true if value looks like it could be a file path.
// A value looks like a file path if it contains path separators (/ or \)
// or ends with a common file extension.
func looksLikeFilePath(value string) bool {
	if value == "" {
		return false
	}
	if strings.ContainsAny(value, "/\\") {
		return true
	}
	ext := strings.ToLower(filepath.Ext(value))
	return filePathExtensions[ext]
}

// sanitizeForError truncates and cleans a target string for safe inclusion in error messages.
// Prevents the error message itself from being an injection vector. Retained in cli
// (not delegated) because create.go and framework_hint.go call it directly.
func sanitizeForError(s string) string {
	runes := []rune(s)
	if len(runes) > 40 {
		s = string(runes[:40]) + "..."
	}
	// Replace control characters and common shell metacharacters for display
	var b strings.Builder
	for _, r := range s {
		if r < 32 || r == 127 {
			b.WriteRune('?')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// sanitizeControlChars replaces terminal control characters (including the ANSI
// ESC 0x1b, all C0 controls, and DEL 0x7F) with '?' so a filesystem-derived string
// echoed in a diagnostic cannot manipulate the terminal. Unlike sanitizeForError it
// does NOT truncate -- a legitimate spec path can be long and must remain readable
// (REV-123 CODEX-001).
func sanitizeControlChars(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r == 0x7F {
			b.WriteRune('?')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
