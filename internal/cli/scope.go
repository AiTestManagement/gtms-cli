package cli

import (
	"path/filepath"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/reader"
)

// buildScopeFromArg constructs a ScopeInfo from an explicit folder argument.
// When folder is empty, scopes to the gtms/test/cases/ root.
func buildScopeFromArg(root string, folder string, recursive bool) *reader.ScopeInfo {
	paths := layout.Current()
	if folder == "" {
		return &reader.ScopeInfo{
			ScanDir:   layout.TestCasesDir(root),
			RelPath:   paths.TestCases + "/",
			Recursive: recursive,
		}
	}
	return &reader.ScopeInfo{
		ScanDir:   filepath.Join(root, paths.TestCases, folder),
		RelPath:   paths.TestCases + "/" + folder + "/",
		Recursive: recursive,
	}
}
