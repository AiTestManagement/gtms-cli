package cli

import (
	"fmt"
	"regexp"
	"strings"
)

// maxTargetIDLength is the maximum allowed length for a target ID.
const maxTargetIDLength = 128

// targetIDPattern matches target IDs containing only safe characters:
// alphanumeric, dashes, underscores, dots, and forward slashes.
// Must start with an alphanumeric character.
var targetIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/-]*$`)

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
var folderArgPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/-]*$`)

// validateFolderArg validates and cleans a folder argument for the create command.
// Returns the cleaned folder name (trailing slashes trimmed) and any validation error.
func validateFolderArg(folder string) (string, error) {
	// Trim trailing slashes
	folder = strings.TrimRight(folder, "/")

	if folder == "" {
		return "", fmt.Errorf("folder name must not be empty")
	}

	if folder == "." || folder == ".." {
		return "", fmt.Errorf("'%s' is not a valid folder name — specify a folder within test-cases/\n    Example: gtms create bug-022", folder)
	}

	// Check for test-cases/ prefix
	if strings.HasPrefix(folder, "test-cases/") || folder == "test-cases" {
		trimmed := strings.TrimPrefix(folder, "test-cases/")
		if trimmed == "" || trimmed == folder {
			return "", fmt.Errorf("don't include the test-cases/ prefix — GTMS adds it automatically\n    Example: gtms create <folder>")
		}
		return "", fmt.Errorf("don't include the test-cases/ prefix — GTMS adds it automatically\n    Example: gtms create %s", trimmed)
	}

	if len(folder) > maxFolderArgLength {
		return "", fmt.Errorf("folder name exceeds maximum length of %d characters", maxFolderArgLength)
	}

	if !folderArgPattern.MatchString(folder) {
		return "", fmt.Errorf("folder name '%s' contains invalid characters — use only letters, numbers, dashes, underscores, and forward slashes", sanitizeForError(folder))
	}

	// Reject path traversal
	if strings.Contains(folder, "..") {
		return "", fmt.Errorf("folder name '%s' contains path traversal sequence", sanitizeForError(folder))
	}

	return folder, nil
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
