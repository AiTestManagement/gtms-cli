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

// MatchesSpecFile reports whether base names a test-case SPEC file whose id
// component is EXACTLY the bare tcID: a Markdown file named "<tcID>.md", or
// "<tcID>-<slug>.md" where the slug is not itself an id extension.
//
// This is the SINGLE authoritative predicate behind Snapshot.Resolve's relevance
// shortlist for Unprovable candidates (ResolveSpec and FindAllSpecs, which once
// shared it, were deleted under ENH-205 / REV-184 CODEX-003). Keeping the guard, the existence check, and the mutation on ONE predicate is load-bearing: if
// they drift (as they did in REV-121/REV-122), a prefixed non-spec file like
// "tc-x-notes.txt" can either invent a false duplicate or, worse, satisfy a
// scoped-existence check and let a qualified operation broaden to the global ID and
// delete the real spec in another folder. base must be a filename (no path); tcID
// is a bare id.
//
// REV-158 CLAUDE-003: a "<tcID>--..." filename is a LONGER id (tc-x--legacy), not
// tcID plus a slug, and must NOT match. A bare HasPrefix made an unreadable
// extended-ID sibling relevant to its shorter prefix, so a malformed
// tc-11111111--legacy.md refused every operation on the unrelated tc-11111111 --
// exactly the cross-identity coupling BUG-188 exists to remove, and which the
// wiring side of the same change already avoided (wiring.TCFromNameForScope).
func MatchesSpecFile(base, tcID string) bool {
	if !strings.HasSuffix(base, ".md") {
		return false
	}
	if base == tcID+".md" {
		return true
	}
	rest, ok := strings.CutPrefix(base, tcID+"-")
	if !ok {
		return false
	}
	// rest is "<slug>.md"; a leading '-' means the real filename was "<tcID>--",
	// i.e. an extended id, so this file belongs to a different (longer) test case.
	return !strings.HasPrefix(rest, "-")
}
