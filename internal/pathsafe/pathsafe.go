// Package pathsafe provides validation and containment helpers for filesystem
// paths that originate from caller-supplied identifiers, configuration, or
// automation records.
//
// Two complementary concerns live here:
//
//  1. ValidateFilenameComponent — rejects values that are unsafe for use as a
//     single filename component (path separators, traversal sequences, control
//     characters). Used at the boundaries where caller-supplied IDs (test case
//     IDs, framework names, task IDs) are embedded in filepath.Join calls.
//     filepath.Join cleans ".." lexically but does NOT prevent the result from
//     escaping the intended directory; this function is the missing guard.
//     Added in BUG-058.
//
//  2. ResolveUnderRoot / IsWithinRoot / PathSafetyError — canonicalise a path
//     and verify it resolves to a location within a project root boundary.
//     Used at the boundaries where stored artefact paths from automation
//     records are about to be opened or written. Lifted from
//     internal/reader/delete.go (ENH-128) to a neutral package so both
//     internal/pipeline and internal/reader can consume the same
//     implementation without inverting the package layering. Added in BUG-057.
//
// Both surfaces are intentionally simple — string operations and standard
// library path resolution. Callers remain responsible for any
// format-specific validation (e.g. tc-{hex} pattern); this package only
// enforces filesystem safety and root containment.
package pathsafe

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ValidateFilenameComponent rejects values that are unsafe for use as a single
// filename component. The label parameter names the value in error messages
// (e.g. "test case ID", "framework", "task ID").
//
// A safe filename component:
//   - is not empty
//   - contains no forward slash ("/")
//   - contains no backslash ("\")
//   - does not contain ".." (path traversal)
//   - is not "." (current directory)
//   - contains no control characters (< 0x20 or 0x7F)
//   - does not start with "/" or a Windows drive letter ("C:")
//
// These checks are deliberately simple string operations rather than regexps.
// The caller is responsible for any format-specific validation (e.g. tc-{hex}
// pattern). This function only enforces filesystem safety.
func ValidateFilenameComponent(value, label string) error {
	if value == "" {
		return fmt.Errorf("invalid %s: must not be empty", label)
	}

	if value == "." || value == ".." {
		return fmt.Errorf("invalid %s: must not be '.' or '..'", label)
	}

	if strings.Contains(value, "/") {
		return fmt.Errorf("invalid %s: contains path separator", label)
	}

	if strings.Contains(value, "\\") {
		return fmt.Errorf("invalid %s: contains path separator", label)
	}

	if strings.Contains(value, "..") {
		return fmt.Errorf("invalid %s: contains path traversal sequence", label)
	}

	// Check for control characters
	for _, r := range value {
		if r < 0x20 || r == 0x7F {
			return fmt.Errorf("invalid %s: contains control character", label)
		}
	}

	return nil
}

// PathSafetyError signals that a path resolved outside the project-owned
// allowlist. Callers MUST treat this as a refusal: the operation must abort
// and the CLI must exit non-zero with a clear message identifying the
// offending path.
type PathSafetyError struct {
	Path  string // the offending path, as declared in the record
	Cause error  // underlying error from ResolveUnderRoot
}

func (e *PathSafetyError) Error() string {
	return fmt.Sprintf("artefact path %q resolves outside the project-owned allowlist: %v", e.Path, e.Cause)
}

func (e *PathSafetyError) Unwrap() error { return e.Cause }

// IsPathSafetyError reports whether err (or any error it wraps) is a
// *PathSafetyError.
func IsPathSafetyError(err error) bool {
	var pse *PathSafetyError
	return errors.As(err, &pse)
}

// ResolveUnderRoot canonicalises inputPath against projectRoot and verifies
// it resolves to a location within the project root. Returns the canonical
// absolute path, a filepath.ToSlash-normalised project-relative path, or a
// *PathSafetyError if the path escapes the project root.
//
// inputPath may be absolute or relative. Relative paths are joined to
// projectRoot. Symlinks are evaluated where the target exists; for
// non-existent paths the cleaned absolute form is used. Both the final
// absolute path and the relative form are returned so callers that need
// os.Stat (absolute) and callers that need the storage form (relative) are
// both served.
//
// Non-existent paths are resolved without EvalSymlinks (the parent must
// still be within root).
func ResolveUnderRoot(projectRoot, inputPath string) (absPath, relPath string, err error) {
	if inputPath == "" {
		return "", "", &PathSafetyError{
			Path:  inputPath,
			Cause: fmt.Errorf("empty artefact path"),
		}
	}

	// Resolve projectRoot to canonical form (handles Windows 8.3 short paths).
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return "", "", &PathSafetyError{
			Path:  inputPath,
			Cause: fmt.Errorf("resolving project root: %w", err),
		}
	}
	evalRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", "", &PathSafetyError{
			Path:  inputPath,
			Cause: fmt.Errorf("evaluating project root symlinks: %w", err),
		}
	}

	// Build the candidate absolute path.
	var candidate string
	if filepath.IsAbs(inputPath) {
		candidate = filepath.Clean(inputPath)
	} else {
		candidate = filepath.Join(evalRoot, inputPath)
	}

	// Try to evaluate symlinks on the resolved path.
	evalPath, evalErr := filepath.EvalSymlinks(candidate)
	if evalErr != nil {
		// File might not exist yet -- use the cleaned absolute path.
		evalPath = filepath.Clean(candidate)
	}

	// Containment check: evalPath must be under evalRoot.
	if !IsWithinRoot(evalPath, evalRoot) {
		return "", "", &PathSafetyError{
			Path:  inputPath,
			Cause: fmt.Errorf("path %q resolves outside project root", inputPath),
		}
	}

	// Compute the normalised relative path.
	rel, err := filepath.Rel(evalRoot, evalPath)
	if err != nil {
		return "", "", &PathSafetyError{
			Path:  inputPath,
			Cause: fmt.Errorf("computing relative path: %w", err),
		}
	}

	return evalPath, filepath.ToSlash(rel), nil
}

// IsWithinRoot checks if absPath is contained within absRoot.
// Both paths must be absolute and already canonicalised.
func IsWithinRoot(absPath, absRoot string) bool {
	// Exact match is allowed (path IS the root).
	if absPath == absRoot {
		return true
	}
	// Must have the root as a prefix followed by a path separator.
	return strings.HasPrefix(absPath, absRoot+string(filepath.Separator))
}
