//go:build !windows

package adapter

import (
	"os/exec"
	"syscall"
)

// configureProcGroup places the child process in its own process group so
// that killProcessTree can signal the entire tree. Must be called before
// cmd.Start().
func configureProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// captureChildPgid returns two closures:
//
//   - killFn:    kills the entire process group rooted at the child.
//   - cleanupFn: no-op on Unix (no OS handles are retained; pgid is just an
//     int). Present to keep the caller symmetric with the Windows path,
//     where handle cleanup is meaningful.
//
// The pgid is captured immediately after cmd.Start() to avoid a race with
// child exit -- syscall.Getpgid called at kill time can fail with ESRCH if
// the immediate child has already exited while descendants still hold pipes
// open. Because configureProcGroup sets Setpgid: true, the child's PID IS
// its pgid (it's the group leader).
//
// Returns two no-op closures if cmd.Process is nil (Start failed).
func captureChildPgid(cmd *exec.Cmd) (killFn func() error, cleanupFn func()) {
	if cmd.Process == nil {
		return func() error { return nil }, func() {}
	}
	// Setpgid: true makes the child its own process-group leader, so the
	// child's PID equals its pgid. No Getpgid syscall needed -- and no race
	// against the child exiting before we look it up.
	pgid := cmd.Process.Pid
	killFn = func() error {
		// SIGKILL the whole group. -pgid in kill(2) means "signal every
		// process in this group." Falls back to Process.Kill() only if the
		// group-kill itself errors (e.g. group already gone).
		if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	// Unix cleanup: no OS handles retained. No-op keeps the caller
	// symmetric with the Windows implementation.
	cleanupFn = func() {}
	return killFn, cleanupFn
}

// killProcessTree is retained for callers that don't have a captured pgid
// closure to hand. It does the live Getpgid lookup and races with child exit
// -- prefer captureChildPgid for new code. Falls back to killing the
// immediate process if the pgid lookup fails (the process already exited).
func killProcessTree(cmd *exec.Cmd) error {
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		return cmd.Process.Kill()
	}
	return syscall.Kill(-pgid, syscall.SIGKILL)
}
