package cli

import (
	"path/filepath"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/reader"
)

// buildScopeFromArg constructs a ScopeInfo from an explicit folder argument.
// When folder is empty, scopes to the gtms/cases/ root.
func buildScopeFromArg(root string, folder string, recursive bool) *reader.ScopeInfo {
	paths := layout.Current()
	if folder == "" {
		return &reader.ScopeInfo{
			ScanDir:   layout.CasesDir(root),
			RelPath:   paths.Cases + "/",
			Recursive: recursive,
		}
	}
	return &reader.ScopeInfo{
		ScanDir:   filepath.Join(root, paths.Cases, folder),
		RelPath:   paths.Cases + "/" + folder + "/",
		Recursive: recursive,
	}
}
