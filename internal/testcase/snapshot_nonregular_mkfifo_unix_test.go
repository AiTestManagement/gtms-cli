//go:build unix

package testcase

import "syscall"

// mkfifoForTest creates a FIFO, or returns the reason it could not. Some
// filesystems (notably a Windows drive mounted under WSL) refuse them; the
// caller skips that ROW and says so.
func mkfifoForTest(path string) error {
	return syscall.Mkfifo(path, 0o600)
}
