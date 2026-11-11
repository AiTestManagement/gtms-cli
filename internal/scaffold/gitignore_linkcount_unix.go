//go:build !windows

package scaffold

import (
	"fmt"
	"os"
	"syscall"
)

// hardLinkCount returns the number of hard links to the file described by info
// (obtained via os.Lstat on path). BUG-208 D6: ensureGitignore uses this to
// refuse a .gitignore whose link count is greater than one, because os.Lstat
// reports a hard-linked regular file as an ordinary regular file. A link count
// that cannot be determined is an inspection error -- GTMS never assumes a
// count of one.
//
// On unix the count is the Nlink field of the stat_t that backs the FileInfo.
func hardLinkCount(path string, info os.FileInfo) (uint64, error) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("cannot determine link count for %s: unexpected FileInfo backing %T", path, info.Sys())
	}
	return uint64(st.Nlink), nil
}
