//go:build !windows

package result

// BUG-209 killable child-process tests: a FIFO at a handoff path must be REFUSED
// without blocking by every writer and by Read (AC6), proved by a real subprocess
// the parent can kill if it hangs; and process death before publication leaves the
// destination holding the complete OLD document (AC2), proved by killing a child
// blocked at the rename boundary.

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain dispatches the child-process modes. With no GTMS_CHILD_MODE set it runs
// the package tests normally.
func TestMain(m *testing.M) {
	switch os.Getenv("GTMS_CHILD_MODE") {
	case "":
		os.Exit(m.Run())
	case "fifo-read":
		_, err := Read(os.Getenv("GTMS_CHILD_PATH"))
		os.Exit(refusalExitCode(err))
	case "fifo-update":
		err := Update(os.Getenv("GTMS_CHILD_PATH"), map[string]interface{}{"summary": "x"})
		os.Exit(refusalExitCode(err))
	case "fifo-reconstruct":
		err := Reconstruct(os.Getenv("GTMS_CHILD_PATH"), childErrorContract())
		os.Exit(refusalExitCode(err))
	case "fifo-results-dir":
		scan := OpenScan(os.Getenv("GTMS_CHILD_PATH"))
		defer scan.Close()
		if scan.DirRefusal == nil {
			os.Exit(3)
		}
		os.Exit(0)
	case "block-rename":
		// Block at the rename boundary, after the staged file is written, so the
		// parent can kill the process before publication and prove the old file is
		// intact. Announce readiness first.
		publishStagedHook = func(stage string, _ []byte) error {
			if stage == "rename" {
				os.Stdout.WriteString("BLOCKED\n")
				_ = os.Stdout.Sync()
				select {} // hang until killed
			}
			return nil
		}
		_ = Update(os.Getenv("GTMS_CHILD_PATH"), map[string]interface{}{"summary": "never-published"})
		os.Exit(0) // unreachable while the hook blocks
	default:
		os.Exit(2)
	}
}

// refusalExitCode maps a child op result to an exit code: 0 when it refused
// (correct), 3 when it returned a non-refusal error or none (wrong).
func refusalExitCode(err error) int {
	if err != nil && IsRefusal(err) {
		return 0
	}
	return 3
}

func childErrorContract() *ResultContract {
	return &ResultContract{
		Task: "task-child", Command: "execute", Target: "tc-11111111",
		Adapter: "a", Mode: "sync", Status: "error",
	}
}

// AC6: a FIFO at the handoff path is refused WITHOUT blocking by Read, Update and
// Reconstruct, each proved by a subprocess that the parent kills if it hangs. The
// subprocess must exit on its own (exit 0 = refused) well within the deadline.
func TestSafeHandoff_FifoChildProcessNeverHangs(t *testing.T) {
	if testing.Short() {
		t.Skip("skips child-process FIFO checks under -short")
	}
	for _, mode := range []string{"fifo-read", "fifo-update", "fifo-reconstruct"} {
		t.Run(mode, func(t *testing.T) {
			_, path := canonicalResults(t, "task-fifochild")
			if err := syscall.Mkfifo(path, 0o644); err != nil {
				t.Skipf("cannot create a FIFO here: %v", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0])
			cmd.Env = append(os.Environ(), "GTMS_CHILD_MODE="+mode, "GTMS_CHILD_PATH="+path)
			err := cmd.Run()

			require.NotEqual(t, context.DeadlineExceeded, ctx.Err(),
				"%s blocked on the FIFO -- it must refuse without opening it (the child had to be killed)", mode)
			require.NoError(t, err, "%s must exit 0 (refused the FIFO), not hang or mis-handle it", mode)
		})
	}
}

// AC2: a process killed before publication leaves the destination holding the
// complete OLD document. The child blocks at the rename boundary with the staged
// file already written; the parent kills it and verifies the handoff is unchanged.
func TestSafeHandoff_ProcessDeathBeforePublishLeavesOld(t *testing.T) {
	if testing.Short() {
		t.Skip("skips child-process crash simulation under -short")
	}
	_, path := canonicalResults(t, "task-kill")
	seedValidHandoff(t, path, "task-kill")
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "GTMS_CHILD_MODE=block-rename", "GTMS_CHILD_PATH="+path)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())

	// Wait for the child to reach the rename boundary (staged file written).
	ready := make(chan bool, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			if sc.Text() == "BLOCKED" {
				ready <- true
				return
			}
		}
		ready <- false
	}()
	select {
	case ok := <-ready:
		require.True(t, ok, "child did not reach the rename boundary")
	case <-time.After(20 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("child never reached the rename boundary")
	}

	// Kill the process at the boundary, before publication.
	require.NoError(t, cmd.Process.Kill())
	_ = cmd.Wait()

	// The destination must still hold the complete OLD document.
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after, "a process killed before publication must leave the old handoff intact")
	rc, err := Read(path)
	require.NoError(t, err, "the old handoff must still parse")
	assert.Equal(t, "task-kill", rc.Task)
}
