package adapter

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
	"time"
)

// runAdapterProcess executes an already-configured exec.Cmd, streams its stdout
// through the file-delimiter parser, and returns the invocation result.
// Both Tier 1 and Tier 2 adapters delegate here after building their command.
func runAdapterProcess(cmd *exec.Cmd, outputDir string, force bool) (*InvocationResult, error) {
	// BUG-131: Place the child in its own process group (Unix) so that
	// the captured-pgid kill closure can signal the entire tree on
	// cancellation. Must be set BEFORE cmd.Start(). No-op on Windows
	// (Job Object setup happens AFTER Start in captureChildPgid because
	// AssignProcessToJobObject needs the child handle).
	configureProcGroup(cmd)

	// cmd.Cancel must be set BEFORE Start(). We close over a holder that
	// the post-Start logic fills in once the child PID (Unix) or Job
	// Object (Windows) is captured. Capturing at Start time avoids two
	// races BUG-131 round-1 hit: Unix Getpgid racing child exit, and
	// Windows taskkill failing to traverse descendants that outlive the
	// immediate parent shell.
	var killFn func() error
	var cleanupFn func()
	cmd.Cancel = func() error {
		if killFn != nil {
			return killFn()
		}
		// Fallback path -- Start failed or the closure wasn't installed.
		return killProcessTree(cmd)
	}
	cmd.WaitDelay = 5 * time.Second

	// Set up streaming stdout pipe and buffered stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	// Start the process
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	// Capture pgid (Unix) / set up Job Object (Windows) immediately after
	// Start so cmd.Cancel kills the entire descendant tree atomically,
	// including descendants that survive the immediate parent shell. On
	// Windows the child is created suspended (see configureProcGroup) and
	// captureChildPgid resumes it after Job Object assignment, so no user
	// code runs in the child until it is bound to the job.
	killFn, cleanupFn = captureChildPgid(cmd)
	// cleanupFn is idempotent on Windows (once-guarded) and a no-op on Unix.
	// It closes the retained Job Object + process handles on normal
	// adapter completion so repeated execute runs do not leak OS handles.
	// Kill implicitly cleans up; cleanup after kill is a no-op.
	defer cleanupFn()

	// Stream and parse stdout
	streamRes, parseErr := parseStreamingOutput(stdout, outputDir, force)

	// Wait for process to complete (AFTER reading all stdout)
	waitErr := cmd.Wait()

	// Handle parse errors
	if parseErr != nil {
		return nil, parseErr
	}

	// Handle ErrWaitDelay — pipes were slow to close, treat as success.
	// Only relevant on Windows where child processes inherit pipe handles.
	if waitErr != nil && errors.Is(waitErr, exec.ErrWaitDelay) {
		waitErr = nil
	}

	result := &InvocationResult{
		ExitCode:   0,
		Stderr:     strings.TrimSpace(stderrBuf.String()),
		SavedFiles: streamRes.SavedFiles,
	}

	// Set Stdout: summary from streaming parser
	result.Stdout = streamRes.Summary

	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			return nil, waitErr
		}
	}

	return result, nil
}
