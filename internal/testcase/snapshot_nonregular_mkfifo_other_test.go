//go:build !unix

package testcase

import "errors"

// mkfifoForTest has no portable implementation off unix (Windows has no
// mkfifo), so the FIFO row skips there.
func mkfifoForTest(path string) error {
	return errors.New("mkfifo is not available on this platform")
}
