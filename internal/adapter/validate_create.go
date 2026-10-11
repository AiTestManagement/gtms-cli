package adapter

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/testcase"
)

// SpecValidationError represents a single validation failure for a spec file.
type SpecValidationError struct {
	File   string // relative path or basename of the offending file
	Reason string // human-readable description of the violation
}

// DegradedFile is retained in the presentation DTO for compatibility. Strict v2
// create validation no longer adds entries to this slice: every supplied
// testcase candidate with an invalid document is a violation.
type DegradedFile struct {
	File   string // basename of the degraded file
	Reason string // why it degraded (e.g. "could not parse frontmatter")
}

// ValidateCreateResult retains its established presentation shape. Under strict
// v2 validation supplied testcase candidates populate Violations or pass;
// Degraded remains empty.
type ValidateCreateResult struct {
	Violations []SpecValidationError
	Degraded   []DegradedFile
}

// gatePattern matches any .md filename that starts with tc-{8hex}. Files
// matching this pattern are considered GTMS-looking output and enter
// validation. Both bare (tc-{8hex}.md) and slugged (tc-{8hex}-slug.md)
// forms pass the gate, as do malformed shapes like tc-{8hex}foo.md -- the
// shape check below distinguishes valid from malformed.
var gatePattern = regexp.MustCompile(`^tc-[0-9a-f]{8}`)

// validShapePattern matches the two legal spec filename forms:
//   - bare:    tc-{8hex}.md
//   - slugged: tc-{8hex}-{slug}.md  (slug starts with alnum, may contain alnum/hyphen/underscore)
//
// Group 1 captures the tc-{8hex} ID for use in frontmatter-match checks.
var validShapePattern = regexp.MustCompile(`^(tc-[0-9a-f]{8})(-[A-Za-z0-9][A-Za-z0-9_-]*)?\.md$`)

// tcIDFormatPattern validates that a test_case_id value matches the expected format.
var tcIDFormatPattern = regexp.MustCompile(`^tc-[0-9a-f]{8}$`)

// ValidateCreateSpecs scans outputDir for .md spec files whose names start
// with tc-{8hex} (the GTMS gate pattern), then classifies each file:
//
//   - Hard violation (ValidateCreateResult.Violations): the filename shape,
//     strict v2 schema, or identity checks fail. These block create.
//   - Valid: the file passes all checks. No entry in either list.
//
// Strict checks:
//
//  1. The filename is a valid shape: bare tc-{8hex}.md or slugged tc-{8hex}-slug.md.
//  2. The test_case_id matches the expected format (tc-{8hex}).
//  3. The test_case_id equals the filename ID portion.
//  4. The test_case_id is one of the pre-generated batchIDs.
//  5. No two files share the same test_case_id.
//
// The preExisting parameter, when non-nil, is a set of filenames (basename only)
// that existed in outputDir before the current adapter invocation. Files present
// in this set are silently skipped regardless of shape -- they belong to prior
// invocations and their IDs are not expected to be in the current batch (BUG-040).
// Pass nil to validate all files (backward-compatible with pre-BUG-040 callers).
//
// The ownedFiles parameter, when non-nil, is a set of filenames (basename only)
// that the current invocation claims ownership of. Only files whose basename is
// in this set enter the validation loop -- sibling invocations' files are silently
// skipped. When nil, all files (after preExisting filtering) are inspected,
// preserving backward compatibility. This prevents concurrent same-folder creates
// from cross-validating each other's output (BUG-110, Option A).
//
// A non-nil error indicates a system-level failure (e.g. directory unreadable),
// not a validation failure.
//
// Files that do not start with tc-{8hex} are silently skipped -- the validator
// only inspects files that look like GTMS spec output. Zero matching files is
// not an error (handled elsewhere as a warning).
//
// The scan is top-level only (flat), matching the depth of snapshotDir and
// scanOutputDir -- files in subdirectories of outputDir are not inspected.
// GTMS create outputs land flat in OutputDir by contract; deeper files
// belong to sub-folder invocations with their own OutputDir scope and must
// not be conflated with the current invocation (BUG-040 re-opening).
func ValidateCreateSpecs(outputDir string, batchIDs []string, preExisting map[string]struct{}, ownedFiles map[string]struct{}) (ValidateCreateResult, error) {
	var result ValidateCreateResult

	if outputDir == "" {
		return result, nil
	}

	entries, err := os.ReadDir(outputDir)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return result, fmt.Errorf("reading output directory: %w", err)
	}

	// Build batch lookup set
	batchSet := make(map[string]struct{}, len(batchIDs))
	for _, id := range batchIDs {
		batchSet[id] = struct{}{}
	}

	// Track seen test_case_id values for duplicate detection
	seenIDs := make(map[string]string) // test_case_id -> first filename that used it

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		base := entry.Name()
		if testcase.IsReservedCompanion(base) {
			continue
		}

		// Only inspect .md files whose name starts with tc-{8hex}
		if !strings.HasSuffix(base, ".md") {
			continue
		}
		if !gatePattern.MatchString(base) {
			continue // not a GTMS-looking spec file, skip
		}

		// Skip files that existed before the current invocation (BUG-040).
		// Pre-existing files are silently skipped regardless of shape.
		if preExisting != nil {
			if _, ok := preExisting[base]; ok {
				continue
			}
		}

		// Skip files not owned by the current invocation (BUG-110).
		// When ownedFiles is non-nil, only files in that set enter
		// validation. Sibling invocations' files are silently skipped.
		if ownedFiles != nil {
			if _, ok := ownedFiles[base]; !ok {
				continue
			}
		}

		// Check 1 (filename shape): must be bare tc-{8hex}.md or slugged tc-{8hex}-slug.md.
		// Shape failures are always hard violations regardless of frontmatter.
		shapeMatch := validShapePattern.FindStringSubmatch(base)
		if shapeMatch == nil {
			result.Violations = append(result.Violations, SpecValidationError{
				File:   base,
				Reason: "filename matches tc-{8hex} prefix but is not a valid shape (expected tc-{8hex}.md or tc-{8hex}-slug.md)",
			})
			continue
		}

		filenameID := shapeMatch[1]
		path := filepath.Join(outputDir, base)

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			continue // skip unreadable files
		}
		decoded := testcase.DecodeCandidate(data)
		if decoded.Failure != nil {
			result.Violations = append(result.Violations, SpecValidationError{
				File:   base,
				Reason: createSchemaFailureReason(decoded.Failure),
			})
			continue
		}

		bodyID := decoded.ParsedIdentity
		if bodyID == "" {
			result.Violations = append(result.Violations, SpecValidationError{
				File:   base,
				Reason: "frontmatter is missing required field 'test_case_id'",
			})
			continue
		}

		// From here, test_case_id is parseable -- strict validation applies.

		// Check 2: test_case_id matches expected format
		if !tcIDFormatPattern.MatchString(bodyID) {
			result.Violations = append(result.Violations, SpecValidationError{
				File:   base,
				Reason: fmt.Sprintf("frontmatter test_case_id '%s' does not match expected format tc-{8hex}", bodyID),
			})
			continue
		}

		// Check 3: test_case_id matches filename ID
		if bodyID != filenameID {
			result.Violations = append(result.Violations, SpecValidationError{
				File:   base,
				Reason: fmt.Sprintf("frontmatter test_case_id '%s' does not match filename ID '%s'", bodyID, filenameID),
			})
		}

		// Check 4: test_case_id is in the pre-generated batch
		if len(batchSet) > 0 {
			if _, ok := batchSet[bodyID]; !ok {
				result.Violations = append(result.Violations, SpecValidationError{
					File:   base,
					Reason: fmt.Sprintf("frontmatter test_case_id '%s' is not in the pre-generated batch", bodyID),
				})
			}
		}

		// Track for duplicate detection
		if firstFile, seen := seenIDs[bodyID]; seen {
			result.Violations = append(result.Violations, SpecValidationError{
				File:   base,
				Reason: fmt.Sprintf("duplicate test_case_id '%s' (also used by %s)", bodyID, firstFile),
			})
		} else {
			seenIDs[bodyID] = base
		}
	}

	return result, nil
}

func createSchemaFailureReason(failure *testcase.SchemaFailure) string {
	if failure.Kind == testcase.SchemaFailureMalformedYAML && failure.Key == "" {
		return "could not parse frontmatter: " + testcase.SanitizeIdentityString(failure.Detail)
	}
	return failure.Error()
}

// FormatValidationErrors builds a human-readable summary from a slice of
// SpecValidationError values. The format follows GTMS error conventions.
func FormatValidationErrors(violations []SpecValidationError) string {
	if len(violations) == 0 {
		return ""
	}

	var b strings.Builder
	fmt.Fprintf(&b, "spec validation failed: %d violation(s) in adapter output", len(violations))
	for _, v := range violations {
		fmt.Fprintf(&b, "\n    %s: %s", testcase.SanitizeIdentityString(v.File), sanitizeValidationReason(v.Reason))
	}
	return b.String()
}

func sanitizeValidationReason(reason string) string {
	// A reason can contain a nested FormatValidationErrors list. Preserve its
	// structural newlines while sanitizing every rendered line independently.
	lines := strings.Split(reason, "\n")
	for i := range lines {
		lines[i] = testcase.SanitizeIdentityString(lines[i])
	}
	return strings.Join(lines, "\n")
}
