//go:build unix

package scaffold

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The FIFO row of the BUG-208 file-state contract (AC4) needs a harness that
// can be killed. Before the fix, ensureGitignore called os.ReadFile on the
// FIFO, which blocks in open() until a writer appears -- forever in CI. A
// goroutine cannot be cancelled out of that call, so the operation runs in a
// child test process (this test binary re-executed) under exec.CommandContext
// with a fixed deadline. On timeout the child is killed and reaped before the
// FIFO is removed, so a regression fails within the ceiling and leaves nothing
// blocked behind it.

const (
	bug208FIFODirEnv  = "GTMS_BUG208_FIFO_DIR"
	bug208FIFOModeEnv = "GTMS_BUG208_FIFO_MODE" // "ensure" (the code under test) or "block" (harness self-check)
	bug208FIFOMarker  = "BUG208_FIFO_RESULT:"
)

type bug208FIFOChildResult struct {
	Action   string   `json:"action"`
	Warnings []string `json:"warnings"`
	Err      string   `json:"err"`
}

// TestBUG208_FIFOChild is the child half of the FIFO harness. It does nothing
// unless the parent test sets bug208FIFODirEnv.
func TestBUG208_FIFOChild(t *testing.T) {
	dir := os.Getenv(bug208FIFODirEnv)
	if dir == "" {
		t.Skip("helper process for TestBUG208_FIFODoesNotHang and TestBUG208_FIFOHarnessKillsBlockingChild")
	}
	if os.Getenv(bug208FIFOModeEnv) == "block" {
		// Deliberately blocking stand-in for the pre-fix read: open() on a FIFO
		// with no writer never returns. The parent must kill this process.
		_, _ = os.ReadFile(filepath.Join(dir, ".gitignore"))
		return
	}
	r := &Result{}
	action, err := ensureGitignore(dir, r)
	res := bug208FIFOChildResult{Action: string(action), Warnings: r.Warnings}
	if err != nil {
		res.Err = err.Error()
	}
	b, mErr := json.Marshal(res)
	if mErr != nil {
		t.Fatalf("marshal child result: %v", mErr)
	}
	fmt.Println(bug208FIFOMarker + string(b))
}

// runBUG208FIFOChild re-executes this test binary as the child, killing it if
// it is still running at the deadline. Run returns only after the child has
// been reaped (WaitDelay bounds the wait for its output pipes too).
func runBUG208FIFOChild(t *testing.T, dir, mode string, deadline time.Duration) (output string, timedOut bool, runErr error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBUG208_FIFOChild$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), bug208FIFODirEnv+"="+dir, bug208FIFOModeEnv+"="+mode)
	cmd.WaitDelay = 5 * time.Second
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	runErr = cmd.Run()
	return out.String(), errors.Is(ctx.Err(), context.DeadlineExceeded), runErr
}

func makeBUG208FIFO(t *testing.T) (dir, fifo string) {
	t.Helper()
	dir = t.TempDir()
	fifo = filepath.Join(dir, ".gitignore")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("FIFO unsupported on this filesystem: %v", err)
	}
	return dir, fifo
}

// TestBUG208_FIFODoesNotHang: the fixed ensureGitignore classifies the FIFO
// with Lstat, never opens it, and returns normally with the warning.
func TestBUG208_FIFODoesNotHang(t *testing.T) {
	skipIfShort(t)
	dir, fifo := makeBUG208FIFO(t)

	output, timedOut, runErr := runBUG208FIFOChild(t, dir, "ensure", 15*time.Second)
	// The child has been reaped (killed first on timeout) before the FIFO goes.
	defer os.Remove(fifo)
	require.False(t, timedOut, "ensureGitignore blocked on a FIFO (BUG-208 regression): child killed at the 15s deadline\n%s", output)
	require.NoError(t, runErr, "child process failed:\n%s", output)

	var res bug208FIFOChildResult
	found := false
	for _, line := range strings.Split(output, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), bug208FIFOMarker); ok {
			require.NoError(t, json.Unmarshal([]byte(rest), &res))
			found = true
		}
	}
	require.True(t, found, "child printed no result line:\n%s", output)

	assert.Empty(t, res.Err)
	assert.Equal(t, string(GitignoreUnchanged), res.Action)
	warnings := strings.Join(res.Warnings, "\n")
	assert.Equal(t, 1, strings.Count(warnings, "skipped updating .gitignore"))
	assert.Contains(t, warnings, "special file", "a FIFO is a special file")
	for _, entry := range gitignoreEntries {
		assert.Contains(t, warnings, entry)
	}
	for _, forbidden := range []string{"updating .gitignore: read", "Created .gitignore"} {
		assert.NotContains(t, warnings, forbidden)
	}
	fi, lerr := os.Lstat(fifo)
	require.NoError(t, lerr)
	assert.NotZero(t, fi.Mode()&os.ModeNamedPipe, ".gitignore must still be the FIFO")
}

// TestBUG208_FIFOHarnessKillsBlockingChild proves the harness itself: a child
// that blocks on the FIFO is killed at the deadline and reaped, so a regression
// in ensureGitignore cannot hang the test run.
func TestBUG208_FIFOHarnessKillsBlockingChild(t *testing.T) {
	skipIfShort(t)
	dir, fifo := makeBUG208FIFO(t)

	const deadline = 2 * time.Second
	start := time.Now()
	output, timedOut, runErr := runBUG208FIFOChild(t, dir, "block", deadline)
	elapsed := time.Since(start)
	defer os.Remove(fifo)

	require.True(t, timedOut, "the blocking child was expected to hit the deadline\n%s", output)
	require.Error(t, runErr, "a killed child must report a non-nil error")
	assert.Less(t, elapsed, deadline+10*time.Second, "the killed child was not reaped promptly")
}
