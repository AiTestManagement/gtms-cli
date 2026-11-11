//go:build unix

package scaffold

// BUG-213 Owner Decision 3: a scaffold destination that is a special file (a
// FIFO here) is refused as non-regular and never written through. safeCreateOnly
// classifies with Lstat only -- it never opens the FIFO -- so, unlike the
// pre-BUG-208 .gitignore read, this cannot block. Unix-only (FIFO creation needs
// syscall.Mkfifo).

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBUG213_FIFOCreateOnlyRefused(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "gtms", "adapters"), 0o755))
	fifoRel := "gtms/adapters/manual-execute-script.sh"
	fifoPath := filepath.Join(dir, filepath.FromSlash(fifoRel))
	require.NoError(t, syscall.Mkfifo(fifoPath, 0o644), "creating FIFO")

	outcome, warn, err := safeCreateOnly(dir, fifoRel, []byte("#!/bin/sh\n"), 0o644)
	require.NoError(t, err, "classifying a FIFO must not error or block")
	assert.Equal(t, writeRefused, outcome, "a special file must be refused")
	assert.Contains(t, warn, "is not a regular file")
	assert.Contains(t, warn, "left untouched")

	// The FIFO is still a FIFO and was not written through.
	info, lerr := os.Lstat(fifoPath)
	require.NoError(t, lerr)
	assert.NotZero(t, info.Mode()&os.ModeNamedPipe, "the FIFO must be left untouched")
}
