package cli

import (
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
)

// IsBulkFolder reports whether arg names an existing, in-tree test-case folder
// (bulk processing). It delegates to the authoritative testcase.ResolveFolder, so
// an out-of-tree folder symlink is treated as "not a bulk folder" (err != nil)
// rather than followed via a raw os.Stat (REV-119 CODEX-001).
func IsBulkFolder(root, arg string) bool {
	_, err := testcase.ResolveFolder(root, arg)
	return err == nil
}
