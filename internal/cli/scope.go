package cli

import (
	"errors"
	"path/filepath"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/reader"
	"github.com/aitestmanagement/gtms-cli/internal/target"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
)

// buildScopeFromArg constructs a ScopeInfo from an explicit folder argument.
// When folder is empty, scopes to the gtms/test/cases/ root.
//
// REV-119 CODEX-001: for a named folder the ScanDir is the CANONICAL, contained
// path from testcase.ResolveFolder -- the read/reset/delete-scope paths must not
// validate safely and then scan a raw symlink path. A safely-resolved but ABSENT
// in-tree folder is a legitimate empty scope (nil error); an out-of-tree symlink
// (or an escaping cases root) is rejected with an error rather than treated as an
// empty scope.
func buildScopeFromArg(root string, folder string, recursive bool) (*reader.ScopeInfo, error) {
	paths := layout.Current()
	if folder == "" {
		return &reader.ScopeInfo{
			ScanDir:   layout.TestCasesDir(root),
			RelPath:   paths.TestCases + "/",
			Recursive: recursive,
		}, nil
	}

	canonical, err := testcase.ResolveFolder(root, folder)
	if err != nil {
		if errors.Is(err, testcase.ErrFolderNotFound) {
			// Absent but safely-resolved in-tree folder: an empty scope. The joined
			// path is proven in-tree (ResolveFolder validated containment); the
			// reader simply finds nothing to scan.
			return &reader.ScopeInfo{
				ScanDir:   filepath.Join(root, paths.TestCases, folder),
				RelPath:   paths.TestCases + "/" + folder + "/",
				Recursive: recursive,
			}, nil
		}
		return nil, err // containment/safety failure
	}

	return &reader.ScopeInfo{
		ScanDir:   canonical,
		RelPath:   paths.TestCases + "/" + folder + "/",
		Recursive: recursive,
	}, nil
}

// qualifiedTargetScope builds the ScopeInfo a folder-QUALIFIED single-TC target
// implies, or nil for a bare target.
//
// REV-158 CLAUDE-001: delete and reset used to drop the folder qualifier after
// requireScopedSpec and call the core with a nil scope, so the destructive plan
// had no idea which folder the user had named and could only check project-wide
// ownership. Carrying the qualifier down lets the mutating package enforce
// authority itself (ADR-024 s3), with the CLI check remaining as the earlier,
// friendlier diagnostic.
//
// The scope is recursive: a qualified target has always matched a spec nested
// below the named folder, and this changes only WHOSE entry may grant authority,
// never how deep the folder reaches.
func qualifiedTargetScope(root string, res target.Result) (*reader.ScopeInfo, error) {
	folder, _ := testcase.SplitQualifiedTarget(res.Target)
	if folder == "" {
		return nil, nil
	}
	return buildScopeFromArg(root, folder, true)
}
