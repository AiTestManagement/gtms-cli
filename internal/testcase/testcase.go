// Package testcase provides shared, path-safe test-case spec resolution.
//
// BUG-059: extracted from duplicated private helpers in pipeline and cli
// packages to provide a single canonical check used by all write paths
// (link, automate, execute).
//
// REV-115: the folder-qualified lookup, and every candidate spec match, are
// resolved under the canonical cases directory via pathsafe.ResolveUnderRoot.
// A symlink under gtms/test/cases/ that resolves OUTSIDE the cases directory --
// whether as the scope directory (root or intermediate component) or as an
// individual spec file -- is rejected, so no caller can be tricked into
// operating on an out-of-tree spec. Symlinks that resolve back INSIDE the cases
// directory (legitimate in-tree aliases) remain supported.
//
// REV-116: the cases directory itself is validated under the project root before
// it is trusted as that containment boundary. If gtms/test/cases is a symlink
// resolving outside the project, resolution fails closed; an in-project alias is
// canonicalised to its in-tree target and still works.
package testcase

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
)

// ErrFolderNotFound signals that a folder was safely resolved to an in-tree
// location but does not exist (or is not a directory). It is DISTINCT from a
// containment/safety failure (a *pathsafe.PathSafetyError), so callers can
// continue classification on a genuine miss while STILL rejecting an out-of-tree
// symlink instead of silently treating it as "empty". Test with errors.Is.
var ErrFolderNotFound = errors.New("test case folder not found")

// ResolveFolder resolves a cases-relative folder to its canonical absolute path
// under the project-owned test-cases directory, or returns an error. It is the
// single authoritative folder resolver -- folderExists (classifier), IsBulkFolder,
// DiscoverTestCases, and the read/scope paths all delegate here so no folder
// consumer scans a raw, unchecked, symlink-followable path (REV-119 CODEX-001).
//
// Order:
//  1. Resolve the cases root beneath the (canonical) project root; reject an
//     out-of-project cases-root symlink.
//  2. Resolve the candidate folder beneath the canonical cases root; reject an
//     out-of-tree folder symlink (root or intermediate component).
//  3. Verify the resolved path exists and is a directory.
//
// Returns:
//   - (canonicalAbsPath, nil) for an existing, in-tree, contained directory;
//   - ("", ErrFolderNotFound-wrapped) for a safely-resolved but absent folder;
//   - ("", containment error) for any out-of-tree / unsafe resolution.
func ResolveFolder(projectRoot, relativeFolder string) (string, error) {
	casesRoot, _, err := pathsafe.ResolveUnderRoot(projectRoot, layout.Current().TestCases)
	if err != nil {
		return "", fmt.Errorf("test cases directory resolves outside the project root: %w", err)
	}

	scope := casesRoot
	if relativeFolder != "" {
		abs, _, rerr := pathsafe.ResolveUnderRoot(casesRoot, relativeFolder)
		if rerr != nil {
			return "", fmt.Errorf("test case folder %q resolves outside the test cases directory: %w", relativeFolder, rerr)
		}
		scope = abs
	}

	info, statErr := os.Stat(scope)
	if statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return "", fmt.Errorf("%w: %s", ErrFolderNotFound, relativeFolder)
		}
		return "", fmt.Errorf("checking test case folder %q: %w", relativeFolder, statErr)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: %s (not a directory)", ErrFolderNotFound, relativeFolder)
	}
	return scope, nil
}

// MatchesSpecFile reports whether base names a test-case SPEC file for the bare
// tcID: a Markdown file named exactly "<tcID>.md" or "<tcID>-*.md".
//
// This is the SINGLE authoritative predicate shared by ResolveSpec
// (existence/resolution), FindAllSpecs (the qualified-target uniqueness guard), and
// reader.findTestCaseSpecs (the delete/reset mutation). Keeping the guard, the
// existence check, and the mutation on ONE predicate is load-bearing: if they drift
// (as they did in REV-121/REV-122), a prefixed non-spec file like "tc-x-notes.txt"
// can either invent a false duplicate or, worse, satisfy a scoped-existence check
// and let a qualified operation broaden to the global ID and delete the real spec in
// another folder. base must be a filename (no path); tcID is a bare id.
func MatchesSpecFile(base, tcID string) bool {
	if !strings.HasSuffix(base, ".md") {
		return false
	}
	return base == tcID+".md" || strings.HasPrefix(base, tcID+"-")
}

// ResolveSpec resolves the spec file for a target and returns its project-relative
// (forward-slash) path, or an error. The error distinguishes "not found" from an
// out-of-tree containment violation for callers that want the security diagnostic;
// Exists/FindSource wrap it for the common boolean/string cases.
//
// Target shapes:
//   - Unqualified: "tc-abc123" -- searched anywhere under gtms/test/cases/.
//   - Folder-qualified: "folder/tc-abc123" -- searched only under
//     gtms/test/cases/folder/, with folder resolved for containment.
//
// A match is a real spec file per MatchesSpecFile ("<tcID>.md" or "<tcID>-*.md") --
// REV-122: a prefixed non-.md decoy must NOT satisfy the scoped existence check.
// Every match is resolved under the canonical cases directory; an out-of-tree spec
// symlink is skipped.
func ResolveSpec(projectRoot, target string) (string, error) {
	// REV-116 CODEX-001: validate the cases directory ITSELF under the project root
	// before trusting it as the containment boundary. layout.TestCasesDir merely
	// joins "gtms/test/cases" onto projectRoot -- if that path is a symlink pointing
	// OUTSIDE the project, using it raw as the ResolveUnderRoot root would promote
	// the external target to the trusted root, so a matching external spec would
	// pass. Resolving the cases dir under the project root instead rejects an
	// out-of-project cases symlink and canonicalises an in-project alias to its
	// in-tree target. All folder + candidate resolution below uses this canonical
	// cases root.
	casesRoot, _, err := pathsafe.ResolveUnderRoot(projectRoot, layout.Current().TestCases)
	if err != nil {
		return "", fmt.Errorf("test cases directory resolves outside the project root: %w", err)
	}

	folder := ""
	tcID := target
	if idx := strings.LastIndex(target, "/"); idx >= 0 {
		folder = target[:idx]
		tcID = target[idx+1:]
	}

	// Resolve the search directory under the (canonical) cases root. For a
	// folder-qualified target this rejects an out-of-tree directory symlink (root
	// OR intermediate component, via EvalSymlinks on the whole path); an in-tree
	// alias resolves to its canonical in-tree directory.
	searchDir := casesRoot
	if folder != "" {
		abs, _, err := pathsafe.ResolveUnderRoot(casesRoot, folder)
		if err != nil {
			return "", fmt.Errorf("test case folder %q resolves outside the test cases directory: %w", folder, err)
		}
		searchDir = abs
	}

	if info, err := os.Stat(searchDir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("test case %q not found under %s/", target, layout.Current().TestCases)
	}

	var found string
	_ = filepath.Walk(searchDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if !MatchesSpecFile(base, tcID) {
			return nil
		}
		// Containment on the candidate spec file itself: reject an out-of-tree spec
		// SYMLINK even when it sits inside a safe directory. A symlink that resolves
		// to another IN-tree spec is allowed (consistent with resolved containment).
		rel, relErr := filepath.Rel(casesRoot, path)
		if relErr != nil {
			return nil
		}
		if _, _, resErr := pathsafe.ResolveUnderRoot(casesRoot, rel); resErr != nil {
			return nil // out-of-tree candidate: skip it, keep searching
		}
		// REV-123 CODEX-002: relativise against the CANONICAL project root. path is
		// canonical (from ResolveUnderRoot/Walk); a raw projectRoot (e.g. a Windows
		// 8.3 short path) would make filepath.Rel emit a spurious "../.." instead of
		// "gtms/test/cases/...".
		if projRel, e := filepath.Rel(pathsafe.CanonicalRoot(projectRoot), path); e == nil {
			found = filepath.ToSlash(projRel)
		} else {
			found = filepath.ToSlash(path)
		}
		return filepath.SkipAll
	})

	if found == "" {
		return "", fmt.Errorf("test case %q not found under %s/", target, layout.Current().TestCases)
	}
	return found, nil
}

// FindAllSpecs returns the project-relative (forward-slash) paths of EVERY in-tree
// test case SPEC for the bare TC ID, sorted. It is the project-wide identity check
// behind the qualified-target ambiguity guard (REV-120 CODEX-001): a folder-
// qualified target is only safe to reduce to its bare ID for a flat mutation when
// exactly one spec carries that ID.
//
// tcID must be a BARE id (no folder qualifier). Matching mirrors the delete/reset
// mutation resolver (reader.findTestCaseSpecs) EXACTLY -- a `.md` file named
// "<tcID>.md" or "<tcID>-*.md" -- so the guard counts precisely what a mutation
// would touch (REV-121 CODEX-002): a prefixed non-spec file (tc-x-notes.txt) is not
// a duplicate. Every candidate is containment-checked under the canonical cases
// root, so an out-of-tree spec symlink is never counted.
//
// It FAILS CLOSED (REV-121 CODEX-001): a filesystem walk error means the count may
// be incomplete, so it returns an error and the qualified operation aborts rather
// than silently undercounting and mutating shared flat state. A missing cases
// directory yields an empty result, not an error.
func FindAllSpecs(projectRoot, tcID string) ([]string, error) {
	casesRoot, _, err := pathsafe.ResolveUnderRoot(projectRoot, layout.Current().TestCases)
	if err != nil {
		return nil, fmt.Errorf("test cases directory resolves outside the project root: %w", err)
	}
	if info, statErr := os.Stat(casesRoot); statErr != nil || !info.IsDir() {
		return nil, nil
	}

	var found []string
	walkErr := filepath.Walk(casesRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// REV-121 CODEX-001: a scan error means the project-wide count may be
			// INCOMPLETE. Fail closed -- propagate so the qualified operation aborts
			// instead of undercounting a duplicate and mutating shared flat state.
			return fmt.Errorf("scanning test cases under %s: %w", layout.Current().TestCases, err)
		}
		if info.IsDir() {
			return nil
		}
		// REV-121/122: the ONE shared spec predicate -- identical to ResolveSpec and
		// the delete/reset mutation resolver, so a prefixed non-spec file is never a
		// duplicate.
		if !MatchesSpecFile(filepath.Base(path), tcID) {
			return nil
		}
		// Containment on the candidate spec: reject an out-of-tree spec symlink even
		// inside a safe directory (mirrors ResolveSpec).
		rel, relErr := filepath.Rel(casesRoot, path)
		if relErr != nil {
			return nil
		}
		if _, _, resErr := pathsafe.ResolveUnderRoot(casesRoot, rel); resErr != nil {
			return nil
		}
		// REV-123 CODEX-002: relativise against the CANONICAL project root (see
		// ResolveSpec) so a non-canonical (Windows 8.3) root cannot produce a
		// spurious "../.." project-relative path.
		if projRel, e := filepath.Rel(pathsafe.CanonicalRoot(projectRoot), path); e == nil {
			found = append(found, filepath.ToSlash(projRel))
		} else {
			found = append(found, filepath.ToSlash(path))
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.Strings(found)
	return found, nil
}

// Exists reports whether a path-safe, in-tree test case spec file exists for the
// given target. It maps any containment violation or missing spec to false.
func Exists(projectRoot, target string) bool {
	_, err := ResolveSpec(projectRoot, target)
	return err == nil
}

// FindSource returns the project-relative (forward-slash) path to the test case
// spec file for the given target, or "" if it is missing or resolves out-of-tree.
//
// ENH-134: used by manualUpdateHash which needs the TC file path for hashing.
func FindSource(projectRoot, target string) string {
	path, err := ResolveSpec(projectRoot, target)
	if err != nil {
		return ""
	}
	return path
}
