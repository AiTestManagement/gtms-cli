package cli

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
)

// maxTargetIDLength is the maximum allowed length for a target ID.
const maxTargetIDLength = 128

// targetIDPattern matches target IDs containing only safe characters:
// alphanumeric, dashes, underscores, dots, and forward slashes.
// Must start with an alphanumeric character.
var targetIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9._/-]*$`)

// validateTargetID checks that a target ID contains only characters safe for
// file paths, branch names, and shell usage. It rejects shell metacharacters,
// path traversal sequences, and overly long values.
//
// Allowed characters: letters, digits, dashes, underscores, dots, forward slashes.
// Forward slashes are needed for subfolder-scoped targets (e.g. "cwd-scoping/tc-abc123").
func validateTargetID(target string) error {
	if target == "" {
		return fmt.Errorf("target ID must not be empty")
	}

	if len(target) > maxTargetIDLength {
		return fmt.Errorf("target ID exceeds maximum length of %d characters", maxTargetIDLength)
	}

	if !targetIDPattern.MatchString(target) {
		return fmt.Errorf("target ID '%s' contains unsafe characters", sanitizeForError(target))
	}

	// Reject path traversal even though individual characters are allowed
	if strings.Contains(target, "..") {
		return fmt.Errorf("target ID '%s' contains path traversal sequence", sanitizeForError(target))
	}

	return nil
}

// maxFolderArgLength is the maximum allowed length for a folder argument.
const maxFolderArgLength = 128

// folderArgPattern matches folder names containing only safe characters:
// alphanumeric, dashes, underscores, dots, and forward slashes.
// Must start with an alphanumeric character. Dots are allowed in
// non-leading positions (e.g. "v2.1", "sprint-2.0"); "." and ".."
// are caught by the explicit check before this regex runs.
var folderArgPattern = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9._/-]*$`)

// validateFolderArg validates and cleans a folder argument for the create command.
// Returns the cleaned folder name (trailing slashes trimmed) and any validation error.
func validateFolderArg(folder string) (string, error) {
	// Trim trailing slashes
	folder = strings.TrimRight(folder, "/")

	// Normalise Windows-style backslash separators to forward slashes (BUG-035).
	// PowerShell tab-completion produces backslash paths; users shouldn't have to
	// translate them. Done before regex, traversal, and length checks so those
	// checks run against the canonical forward-slash form. A trailing backslash
	// becomes a trailing slash, so re-trim afterwards.
	folder = strings.ReplaceAll(folder, "\\", "/")
	folder = strings.TrimRight(folder, "/")

	if folder == "" {
		return "", fmt.Errorf("folder name must not be empty")
	}

	paths := layout.Current()
	if folder == "." || folder == ".." {
		return "", fmt.Errorf("'%s' is not a valid folder name — specify a folder within %s/\n    Example: gtms create bug-022", folder, paths.Cases)
	}

	// Check for full prefix (e.g. "gtms/cases/foo") — ENH-093: routed through layout package
	casesPrefix := paths.Cases + "/"
	if strings.HasPrefix(folder, casesPrefix) || folder == paths.Cases {
		trimmed := strings.TrimPrefix(folder, casesPrefix)
		if trimmed == "" || trimmed == folder {
			return "", fmt.Errorf("don't include the %s/ prefix — GTMS adds it automatically\n    Example: gtms create <folder>", paths.Cases)
		}
		return "", fmt.Errorf("don't include the %s/ prefix — GTMS adds it automatically\n    Example: gtms create %s", paths.Cases, trimmed)
	}

	// Check for short-form prefix (e.g. "cases/foo") — common user mistake
	shortCasesDir := filepath.Base(paths.Cases)
	shortPrefix := shortCasesDir + "/"
	if strings.HasPrefix(folder, shortPrefix) || folder == shortCasesDir {
		trimmed := strings.TrimPrefix(folder, shortPrefix)
		if trimmed == "" || trimmed == folder {
			return "", fmt.Errorf("don't include the %s/ prefix — GTMS adds it automatically\n    Example: gtms create <folder>", paths.Cases)
		}
		return "", fmt.Errorf("don't include the %s/ prefix — GTMS adds it automatically\n    Example: gtms create %s", paths.Cases, trimmed)
	}

	if len(folder) > maxFolderArgLength {
		return "", fmt.Errorf("folder name exceeds maximum length of %d characters", maxFolderArgLength)
	}

	// Reject path traversal before regex — gives a more specific error for "..\parent"
	// which normalises to "../parent" (BUG-035: backslash normalisation).
	if strings.Contains(folder, "..") {
		return "", fmt.Errorf("folder name '%s' contains path traversal sequence", sanitizeForError(folder))
	}

	if !folderArgPattern.MatchString(folder) {
		return "", fmt.Errorf("folder name '%s' contains invalid characters — use only letters, numbers, dashes, underscores, and forward slashes", sanitizeForError(folder))
	}

	return folder, nil
}

// filePathExtensions is the set of file extensions that suggest a value is a file path.
var filePathExtensions = map[string]bool{
	".md": true, ".txt": true, ".yaml": true, ".yml": true,
	".json": true, ".xml": true, ".csv": true, ".html": true,
	".go": true, ".py": true, ".js": true, ".ts": true,
}

// LooksLikeFilePath returns true if value looks like it could be a file path.
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

// normaliseTarget strips a trailing ".md" extension from TC-ID-shaped arguments.
// When a user tab-completes a test case filename they get "tc-abc12345.md"; this
// helper silently normalises it to "tc-abc12345" so downstream validation succeeds.
// Non-TC arguments (e.g. folder names like "my-folder.md") are returned unchanged.
// Handles subfolder-scoped targets like "cwd-scoping/tc-abc123.md".
func normaliseTarget(target string) string {
	// Extract base name (part after last "/") to check prefix
	base := target
	prefix := ""
	if idx := strings.LastIndex(target, "/"); idx >= 0 {
		prefix = target[:idx+1]
		base = target[idx+1:]
	}

	// Only strip .md if the base starts with "tc-" and has content between "tc-" and ".md"
	if strings.HasPrefix(base, "tc-") && strings.HasSuffix(base, ".md") {
		stripped := strings.TrimSuffix(base, ".md")
		// Ensure there's actual ID content after "tc-" (not just "tc-.md" → "tc-")
		if len(stripped) > 3 {
			return prefix + stripped
		}
	}

	return target
}

// sanitizeForError truncates and cleans a target string for safe inclusion in error messages.
// Prevents the error message itself from being an injection vector.
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
