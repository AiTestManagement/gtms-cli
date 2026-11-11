//go:build windows

package scaffold

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// hardLinkCount returns the number of hard links to the file at path. BUG-208
// D6: ensureGitignore uses this to refuse a .gitignore whose link count is
// greater than one, because os.Lstat reports a hard-linked regular file as an
// ordinary regular file. A link count that cannot be determined is an
// inspection error -- GTMS never assumes a count of one.
//
// Windows has no Nlink in the FileInfo, so the file is opened for metadata only
// (no access rights requested) and GetFileInformationByHandle is queried for
// NumberOfLinks. os.Link needs no special Windows rights, so this path is
// exercised on Windows as well as unix (BUG-208 AC10). The info argument is
// unused on Windows but kept for a single cross-platform signature.
func hardLinkCount(path string, _ os.FileInfo) (uint64, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, fmt.Errorf("cannot determine link count for %s: %w", path, err)
	}
	// Access 0 (query attributes only); share everything so an open handle
	// elsewhere does not make this fail; FILE_FLAG_BACKUP_SEMANTICS lets the
	// handle open regardless of the object type.
	handle, err := windows.CreateFile(
		p,
		0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return 0, fmt.Errorf("cannot determine link count for %s: %w", path, err)
	}
	defer windows.CloseHandle(handle)

	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return 0, fmt.Errorf("cannot determine link count for %s: %w", path, err)
	}
	return uint64(info.NumberOfLinks), nil
}
