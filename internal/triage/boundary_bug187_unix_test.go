//go:build unix

package triage

import (
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BUG-187 entry-type cases that need POSIX facilities (FIFO, socket). They live
// in a unix-only file so the portable table compiles on Windows. Each announces
// a skip as non-evidence when the facility is unavailable.

func TestRead_RefusesFIFO_WithoutBlocking(t *testing.T) {
	root := t.TempDir()
	dir := realTriageDir(t, root)
	fifo := filepath.Join(dir, "tc-11111111--bats.triage.yaml")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("mkfifo unavailable here (%v); this case produced NO evidence", err)
	}

	path, err := Path(root, "tc-11111111", "bats")
	require.NoError(t, err)

	// Opening a FIFO with no writer blocks forever, so the guard MUST refuse it
	// on Lstat before opening. Drive Read under a deadline: a block shows up as a
	// timeout rather than a refusal.
	done := make(chan error, 1)
	go func() {
		_, rerr := Read(root, path)
		done <- rerr
	}()
	select {
	case rerr := <-done:
		assertEntryTypeError(t, rerr, "named pipe")
	case <-time.After(10 * time.Second):
		t.Fatal("Read blocked on a FIFO; the entry-type gate must refuse it before opening")
	}

	// The FIFO is untouched.
	fi, lerr := os.Lstat(fifo)
	require.NoError(t, lerr)
	assert.NotZero(t, fi.Mode()&os.ModeNamedPipe, "the FIFO must survive the refusal")
}

func TestRead_RefusesSocket(t *testing.T) {
	root := t.TempDir()
	dir := realTriageDir(t, root)
	sockPath := filepath.Join(dir, "tc-11111111--bats.triage.yaml")
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Skipf("unix socket unavailable here (%v); this case produced NO evidence", err)
	}
	defer l.Close()

	path, err := Path(root, "tc-11111111", "bats")
	require.NoError(t, err)
	_, err = Read(root, path)
	assertEntryTypeError(t, err, "socket")
}
