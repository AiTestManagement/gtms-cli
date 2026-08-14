// Package target provides the single, shared classifier that every folder-aware
// gtms command uses to interpret a positional target argument. It decides whether
// the argument names an existing folder scope (case preserved) or a test case ID
// (lowercased, folder qualifier and .md suffix stripped), and normalises Windows
// backslash separators along the way.
//
// Before BUG-167 each command inlined its own normalisation, so the seven
// folder-aware commands drifted apart from the create/gaps controls: some
// lowercased the whole positional (breaking mixed-case folders on case-sensitive
// filesystems), and some ran TC-ID validation before folder classification
// (rejecting backslash folders). Classify is the one home for the corrected
// "classify and normalise as a folder BEFORE applying TC-ID rules" order and for
// extracting a bare TC ID from a folder-qualified single-TC target.
package target

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
)

// Kind classifies how a positional target argument was interpreted.
type Kind int

const (
	// KindFolder means the argument is a folder scope (spelling preserved,
	// separators normalised). It may or may not exist on disk.
	KindFolder Kind = iota
	// KindTCID means the argument is a single test case ID. Result.Target keeps
	// the (case-preserved) folder qualifier for scoped resolution; Result.TCID is
	// the bare lowercase, .md-stripped ID for flat wiring.
	KindTCID
)

// Result is the outcome of Classify. It deliberately exposes TWO separate values
// so a caller cannot accidentally use the flat wiring key where scoped spec
// resolution is required (BUG-059 / REV-113 CODEX-001):
//
//   - Target: the QUALIFIED, case-preserved value. For KindFolder it is the
//     normalised folder path; for KindTCID it is the folder qualifier (case
//     preserved) plus the normalised bare TC ID (e.g. "FeatureA/tc-22222222").
//     Callers MUST use Target for scoped spec existence/resolution, so a
//     wrong-folder qualified target (bar/tc-x when the spec lives under foo/) is
//     rejected rather than silently resolved against the same ID elsewhere.
//   - TCID: the BARE lowercase TC ID (e.g. "tc-22222222"), empty for KindFolder.
//     Callers use it ONLY for flat wiring/task/hash lookups and mutation, which
//     are keyed on the bare ID -- and only AFTER scoped resolution via Target
//     has succeeded.
type Result struct {
	Kind   Kind
	Target string
	TCID   string
}

// Classify interprets a positional target argument as either a folder scope or a
// test case ID, applying the BUG-167 normalisation rules uniformly.
//
// Order (fail closed, then existence-first):
//  1. validateFolderSafety runs FIRST on the COMPLETE input. Any folder-safety
//     violation (path traversal, the gtms/test/cases/ prefix, an invalid
//     character, or empty) is rejected here -- Classify returns that error and
//     NEVER falls through to TC classification, so a TC-shaped final segment can
//     never launder an unsafe path (e.g. "../tc-abc12345"). Length is NOT checked
//     at this stage.
//  2. If the cleaned folder exists on disk it is a folder scope (case preserved);
//     the folder-length limit applies to the raw folder.
//  3. Otherwise, if the last path segment is TC-shaped once lowercased and its
//     .md suffix stripped, it is a bare TC ID (folder qualifier dropped); the
//     target-length limit applies to the NORMALISED qualified target.
//  4. Otherwise it is a folder scope that does not exist yet; the folder-length
//     limit applies, and each command's own downstream handles a
//     non-existent/empty folder as it did before.
//
// REV-116 CODEX-002: the length limit is applied AFTER classification, not on the
// raw argument. Before this ordering, a valid TC ID written with a ".md" suffix
// (e.g. a 126-char id -> "<id>.md" is 129 bytes) was rejected as a 129-char
// oversized folder even though the normalised TC (126 chars) is within the limit.
//
// REV-117 CODEX-001: a trailing path separator ("/" or "\") is an EXPLICIT folder
// signal. It is detected on the RAW argument before validateFolderSafety trims it;
// when present, the argument is ALWAYS a folder scope and never falls through to a
// bare TC ID. Without this, "gtms delete tc-abc12345/" trimmed to "tc-abc12345"
// and (with no such folder) became a GLOBAL TC deletion -- a data-safety
// regression across all seven folder-aware commands. Existence-first still wins:
// an existing "tc-abc12345/" directory is a folder either way.
func Classify(root, arg string) (Result, error) {
	if arg == "" {
		return Result{}, fmt.Errorf("target must not be empty")
	}

	// Capture the folder signal on the RAW argument BEFORE any trimming. A
	// trailing "/" or "\" means the user wrote a directory, not a bare TC ID.
	hadTrailingSeparator := strings.HasSuffix(arg, "/") || strings.HasSuffix(arg, "\\")

	// 1. Folder-safety gate on the COMPLETE input: backslash normalisation,
	//    reserved-prefix, traversal, and invalid-character rejection. Reject
	//    immediately on any violation -- do not fall through to TC rules. Length
	//    is deliberately excluded here (applied per classification below). A
	//    separator-only input ("/", "\") fails here as empty, NOT as a folder.
	cleaned, ferr := validateFolderSafety(arg)
	if ferr != nil {
		return Result{}, ferr
	}

	// 2. Existence-first WITH containment (REV-119 CODEX-001): an existing, in-tree
	//    directory is a folder scope (case preserved; keeps any ".md" tail -- no TC
	//    stripping). A containment/safety failure -- an out-of-tree folder symlink,
	//    or a cases-root symlink escaping the project -- is rejected HERE and NEVER
	//    silently treated as "not found" and allowed to fall through to TC /
	//    would-be-folder classification (which would let a symlinked external dir
	//    with a TC-shaped child launder into a global mutation).
	if _, ferr := testcase.ResolveFolder(root, cleaned); ferr == nil {
		if err := folderLengthOK(cleaned); err != nil {
			return Result{}, err
		}
		return Result{Kind: KindFolder, Target: cleaned}, nil
	} else if !errors.Is(ferr, testcase.ErrFolderNotFound) {
		return Result{}, ferr
	}
	// Genuine not-found (safely resolved, in-tree, absent): continue below.

	// 3. TC-ID: split off the case-preserved folder qualifier, then normalise ONLY
	//    the last segment (lowercase, strip .md). Target keeps the qualifier for
	//    scoped spec resolution; TCID is the bare flat-wiring key. The length limit
	//    applies to the normalised qualified target (pre-BUG-167 ordering).
	//
	//    Skipped entirely for an explicitly folder-shaped argument (trailing
	//    separator, REV-117 CODEX-001): such an input must never become a bare TC
	//    ID, even when the last segment is TC-shaped.
	if !hadTrailingSeparator {
		prefix := ""
		base := cleaned
		if idx := strings.LastIndex(cleaned, "/"); idx >= 0 {
			prefix = cleaned[:idx+1] // case-preserved folder qualifier, incl. trailing "/"
			base = cleaned[idx+1:]
		}
		tcID := NormaliseTarget(strings.ToLower(base))
		if IsTestCaseID(tcID) {
			qualified := prefix + tcID
			if len(qualified) > maxTargetIDLength {
				return Result{}, fmt.Errorf("target ID exceeds maximum length of %d characters", maxTargetIDLength)
			}
			return Result{Kind: KindTCID, Target: qualified, TCID: tcID}, nil
		}
	}

	// 4. Would-be folder: safe folder syntax that does not exist and is not a TC
	//    ID (or was explicitly folder-shaped). Apply the folder-length limit, then
	//    classify as a folder scope so command downstreams stay consistent with
	//    the create/gaps controls (status/map/reset yield an empty scope;
	//    execute/automate/delete report no test cases found).
	if err := folderLengthOK(cleaned); err != nil {
		return Result{}, err
	}
	return Result{Kind: KindFolder, Target: cleaned}, nil
}

// folderLengthOK enforces the folder-argument length limit. Split out so both
// Classify (per classification) and ValidateFolderArg apply the identical bound.
func folderLengthOK(folder string) error {
	if len(folder) > maxFolderArgLength {
		return fmt.Errorf("folder name exceeds maximum length of %d characters", maxFolderArgLength)
	}
	return nil
}

// segmentsHaveFoldedPrefix reports whether input begins with every segment of
// prefix, comparing each segment case-insensitively via strings.EqualFold. It is
// Unicode-safe: it never slices a byte offset derived from a case-transformed
// string, so a parent whose lowercase mapping changes byte length cannot panic it
// (REV-119 CODEX-002).
func segmentsHaveFoldedPrefix(input, prefix []string) bool {
	if len(input) < len(prefix) {
		return false
	}
	for i := range prefix {
		if !strings.EqualFold(input[i], prefix[i]) {
			return false
		}
	}
	return true
}

// reservedPrefixError builds the "don't include the <cases>/ prefix" diagnostic,
// echoing the ORIGINAL-cased suffix when one is present.
func reservedPrefixError(casesPath, suffix string) error {
	if suffix != "" {
		return fmt.Errorf("don't include the %s/ prefix -- GTMS adds it automatically\n    Example: gtms create %s", casesPath, suffix)
	}
	return fmt.Errorf("don't include the %s/ prefix -- GTMS adds it automatically\n    Example: gtms create <folder>", casesPath)
}

// maxTargetIDLength is the maximum allowed length for a target ID.
const maxTargetIDLength = 128

// targetIDPattern matches target IDs containing only safe characters:
// alphanumeric, dashes, underscores, dots, and forward slashes.
// Must start with an alphanumeric character.
var targetIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9._/-]*$`)

// ValidateTargetID checks that a target ID contains only characters safe for
// file paths, branch names, and shell usage. It rejects shell metacharacters,
// path traversal sequences, and overly long values.
//
// Allowed characters: letters, digits, dashes, underscores, dots, forward slashes.
// Forward slashes are needed for subfolder-scoped targets (e.g. "cwd-scoping/tc-abc123").
func ValidateTargetID(target string) error {
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

// ValidateFolderArg validates and cleans a FOLDER argument: it runs the shared
// folder-safety checks and then the folder-length limit. Returns the cleaned
// folder name (trailing slashes trimmed) and any validation error.
//
// Callers that classify a positional target first (target.Classify) use
// validateFolderSafety directly and apply a classification-specific length check,
// so a TC-shaped ".md" suffix is not counted against the folder limit (REV-116).
func ValidateFolderArg(folder string) (string, error) {
	cleaned, err := validateFolderSafety(folder)
	if err != nil {
		return "", err
	}
	if err := folderLengthOK(cleaned); err != nil {
		return "", err
	}
	return cleaned, nil
}

// validateFolderSafety runs every folder SAFETY check on the COMPLETE input --
// backslash/trailing-slash normalisation, reserved-prefix rejection (case
// insensitive), path-traversal, and the allowed-character set -- and returns the
// cleaned forward-slash folder. It deliberately does NOT apply the length limit:
// length is a sizing bound, not a safety gate, and (REV-116 CODEX-002) must be
// applied AFTER the caller decides whether the argument is a folder (raw length)
// or a TC ID (length of the normalised, .md-stripped ID).
func validateFolderSafety(folder string) (string, error) {
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
		return "", fmt.Errorf("'%s' is not a valid folder name -- specify a folder within %s/\n    Example: gtms create bug-022", folder, paths.TestCases)
	}

	// Reserved test-cases prefix, recognised CASE-INSENSITIVELY and SEGMENT-WISE
	// (REV-114 CODEX-001 + REV-119 CODEX-002). BUG-167 preserves case, so a
	// case-sensitive check let "GTMS/test/cases/FeatureA" or "CASES/FeatureA" slip
	// through and produce a doubled prefix. Each PATH SEGMENT is folded with
	// strings.EqualFold; the echoed remediation suffix is rebuilt from the ORIGINAL
	// input segments (user casing preserved). Crucially the suffix is NEVER sliced
	// by a byte offset taken from a differently-cased string: a Unicode renamed
	// parent whose ToLower changes UTF-8 byte length would otherwise panic that
	// slice (REV-119 CODEX-002) -- InitFromParent forbids separators, not non-ASCII.
	inputSegs := strings.Split(folder, "/")
	reservedSegs := strings.Split(paths.TestCases, "/") // e.g. ["gtms","test","cases"]

	// Full reserved prefix: input begins with every reserved segment (exact match
	// yields an empty suffix; a child yields the trailing segments).
	if segmentsHaveFoldedPrefix(inputSegs, reservedSegs) {
		return "", reservedPrefixError(paths.TestCases, strings.Join(inputSegs[len(reservedSegs):], "/"))
	}

	// Short-form prefix (e.g. "cases/foo"): input begins with just the last reserved
	// segment ("cases"). A near-match like "cases-extra/FeatureA" does NOT EqualFold
	// "cases", so it stays valid.
	shortSeg := reservedSegs[len(reservedSegs)-1]
	if len(inputSegs) > 0 && strings.EqualFold(inputSegs[0], shortSeg) {
		return "", reservedPrefixError(paths.TestCases, strings.Join(inputSegs[1:], "/"))
	}

	// Reject path traversal before regex -- gives a more specific error for "..\parent"
	// which normalises to "../parent" (BUG-035: backslash normalisation).
	if strings.Contains(folder, "..") {
		return "", fmt.Errorf("folder name '%s' contains path traversal sequence", sanitizeForError(folder))
	}

	if !folderArgPattern.MatchString(folder) {
		return "", fmt.Errorf("folder name '%s' contains invalid characters -- use only letters, numbers, dashes, underscores, and forward slashes", sanitizeForError(folder))
	}

	return folder, nil
}

// NormaliseTarget strips a trailing ".md" extension from TC-ID-shaped arguments.
// When a user tab-completes a test case filename they get "tc-abc12345.md"; this
// helper silently normalises it to "tc-abc12345" so downstream validation succeeds.
// Non-TC arguments (e.g. folder names like "my-folder.md") are returned unchanged.
// Handles subfolder-scoped targets like "cwd-scoping/tc-abc123.md".
func NormaliseTarget(target string) string {
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
		// Ensure there's actual ID content after "tc-" (not just "tc-.md" -> "tc-")
		if len(stripped) > 3 {
			return prefix + stripped
		}
	}

	return target
}

// IsTestCaseID validates that the target looks like a test case ID (e.g. tc-a1b2c3d4).
// Requires the "tc-" prefix. For subfolder-scoped targets like "cwd-scoping/tc-abc123",
// the base name after the last "/" is checked.
func IsTestCaseID(target string) bool {
	base := target
	if idx := strings.LastIndex(target, "/"); idx >= 0 {
		base = target[idx+1:]
	}
	return strings.HasPrefix(base, "tc-") && len(base) > 3
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
