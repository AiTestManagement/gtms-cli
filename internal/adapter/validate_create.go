package adapter

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/adrg/frontmatter"
)

// specFrontmatter holds the minimal frontmatter fields needed for validation.
type specFrontmatter struct {
	TestCaseID string `yaml:"test_case_id"`
}

// SpecValidationError represents a single validation failure for a spec file.
type SpecValidationError struct {
	File   string // relative path or basename of the offending file
	Reason string // human-readable description of the violation
}

// DegradedFile represents a spec file that could not be strictly validated
// because its frontmatter is missing or unparseable. These files degrade to
// filename-only listing (ENH-092) instead of hard-failing create (BUG-106).
type DegradedFile struct {
	File   string // basename of the degraded file
	Reason string // why it degraded (e.g. "could not parse frontmatter")
}

// ValidateCreateResult bundles hard validation failures (Violations) and
// soft-pass degraded files (Degraded). The invoker hard-fails only on
// Violations; Degraded files are allowed through to the listing code,
// which renders them filename-only.
//
// BUG-106 precedence rule: the boundary is whether test_case_id is parseable.
//   - Parseable test_case_id: strict BUG-038/BUG-104 validation applies.
//   - Unparseable test_case_id (missing/malformed frontmatter): ENH-092
//     degraded listing applies -- no hard failure.
//   - Malformed filename shape: always a hard failure regardless of frontmatter.
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
// with tc-{8hex} (the GTMS gate pattern), then classifies each file into
// one of three categories:
//
//   - Hard violation (ValidateCreateResult.Violations): the file has a
//     parseable test_case_id that fails strict checks, or the filename
//     shape is malformed. These block the create command.
//   - Degraded (ValidateCreateResult.Degraded): the file's frontmatter is
//     missing or unparseable, so test_case_id cannot be determined. These
//     are allowed through and render as filename-only in the TC listing
//     (ENH-092 degradation). BUG-106 precedence rule.
//   - Valid: the file passes all checks. No entry in either list.
//
// Strict checks (applied only when test_case_id is parseable):
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

		// Parse frontmatter
		f, openErr := os.Open(path)
		if openErr != nil {
			continue // skip unreadable files
		}

		var fm specFrontmatter
		_, parseErr := frontmatter.Parse(f, &fm)
		f.Close()

		// BUG-106 degradation: if frontmatter cannot be parsed, the file
		// degrades to filename-only listing instead of hard-failing.
		if parseErr != nil {
			result.Degraded = append(result.Degraded, DegradedFile{
				File:   base,
				Reason: fmt.Sprintf("could not parse frontmatter: %v", parseErr),
			})
			continue
		}

		// BUG-106 degradation: if test_case_id is missing from parseable
		// frontmatter, the file degrades to filename-only listing.
		if fm.TestCaseID == "" {
			result.Degraded = append(result.Degraded, DegradedFile{
				File:   base,
				Reason: "frontmatter is missing required field 'test_case_id'",
			})
			continue
		}

		// From here, test_case_id is parseable -- strict validation applies.

		// Check 2: test_case_id matches expected format
		if !tcIDFormatPattern.MatchString(fm.TestCaseID) {
			result.Violations = append(result.Violations, SpecValidationError{
				File:   base,
				Reason: fmt.Sprintf("frontmatter test_case_id '%s' does not match expected format tc-{8hex}", fm.TestCaseID),
			})
			continue
		}

		// Check 3: test_case_id matches filename ID
		if fm.TestCaseID != filenameID {
			result.Violations = append(result.Violations, SpecValidationError{
				File:   base,
				Reason: fmt.Sprintf("frontmatter test_case_id '%s' does not match filename ID '%s'", fm.TestCaseID, filenameID),
			})
		}

		// Check 4: test_case_id is in the pre-generated batch
		if len(batchSet) > 0 {
			if _, ok := batchSet[fm.TestCaseID]; !ok {
				result.Violations = append(result.Violations, SpecValidationError{
					File:   base,
					Reason: fmt.Sprintf("frontmatter test_case_id '%s' is not in the pre-generated batch", fm.TestCaseID),
				})
			}
		}

		// Track for duplicate detection
		if firstFile, seen := seenIDs[fm.TestCaseID]; seen {
			result.Violations = append(result.Violations, SpecValidationError{
				File:   base,
				Reason: fmt.Sprintf("duplicate test_case_id '%s' (also used by %s)", fm.TestCaseID, firstFile),
			})
		} else {
			seenIDs[fm.TestCaseID] = base
		}
	}

	return result, nil
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
		fmt.Fprintf(&b, "\n    %s: %s", v.File, v.Reason)
	}
	return b.String()
}
