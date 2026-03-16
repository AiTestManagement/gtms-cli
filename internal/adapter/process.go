package adapter

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// runAdapterProcess executes an already-configured exec.Cmd, streams its stdout
// through the file-delimiter parser, and returns the invocation result.
// Both Tier 1 and Tier 2 adapters delegate here after building their command.
func runAdapterProcess(cmd *exec.Cmd, outputDir string) (*InvocationResult, error) {
	// Configure graceful cancellation behaviour.
	// Must be set BEFORE cmd.Start().
	cmd.Cancel = func() error {
		if runtime.GOOS == "windows" {
			return cmd.Process.Kill()
		}
		return cmd.Process.Signal(os.Interrupt)
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

	// Stream and parse stdout
	streamRes, parseErr := parseStreamingOutput(stdout, outputDir)

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
