package cli

import (
	"errors"
	"path/filepath"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/reader"
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
