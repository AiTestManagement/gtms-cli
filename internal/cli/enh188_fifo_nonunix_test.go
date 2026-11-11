//go:build !unix

package cli

import "errors"

// unixMkfifo is unsupported off Unix; the FIFO test skips before calling it.
func unixMkfifo(path string) error {
	return errors.New("mkfifo not supported on this platform")
}
