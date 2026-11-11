//go:build unix

package cli

import "syscall"

// unixMkfifo creates a FIFO (named pipe) at path. Unix-only; used by the REV-265
// "special entries must not be opened" test to prove isAlreadyPassing never
// blocks on a device-like entry in gtms/execution.
func unixMkfifo(path string) error {
	return syscall.Mkfifo(path, 0o644)
}
