package cli

import (
	"path/filepath"

	"github.com/aitestmanagement/gtms-cli/internal/reader"
)

// buildScopeFromArg constructs a ScopeInfo from an explicit folder argument.
// When folder is empty, scopes to the test-cases/ root.
func buildScopeFromArg(root string, folder string, recursive bool) *reader.ScopeInfo {
	if folder == "" {
		return &reader.ScopeInfo{
			ScanDir:   filepath.Join(root, "test-cases"),
			RelPath:   "test-cases/",
			Recursive: recursive,
		}
	}
	return &reader.ScopeInfo{
		ScanDir:   filepath.Join(root, "test-cases", folder),
		RelPath:   "test-cases/" + folder + "/",
		Recursive: recursive,
	}
}
