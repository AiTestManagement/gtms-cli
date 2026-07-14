//go:build windows

package adapter

import (
	"fmt"
	"io"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// configureProcGroup on Windows requests the child process be created with
// its primary thread suspended (CREATE_SUSPENDED). Job Object assignment
// then happens after Start (when the child handle exists) but BEFORE the
// primary thread is resumed, so no user code runs in the child until it is
// bound to the job. This closes the immediate-exit race that the BUG-131
// round-2 review flagged: without CREATE_SUSPENDED the child's shell can
// spawn a descendant and exit before AssignProcessToJobObject fires,
// leaving the descendant unbound and untracked.
func configureProcGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
}

// captureChildPgid attaches the freshly-started (and currently suspended)
// child to a Job Object with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE, then resumes
// the primary thread. Returns:
//
//   - killFn:    terminates the entire job (tree kill). Idempotent.
//   - cleanupFn: closes the retained Windows handles. Idempotent. Safe to
//     call after killFn (no-op) or without killFn (handles closed cleanly on
//     normal completion so we don't leak OS handles across repeated runs).
//
// The child is ALWAYS resumed before this function returns, even on failure
// of any intermediate step -- leaving the child suspended would wedge the
// adapter forever. If Job Object setup fails at any point, killFn falls back
// to taskkill /T /F /PID (documented last resort, less robust against
// detached descendants but always available on Windows).
//
// This ordering closes the BUG-131 round-2 race: the child cannot execute a
// single instruction until AssignProcessToJobObject has bound it (and
// therefore any descendants it later spawns) to the job.
//
// Returns two no-op closures if cmd.Process is nil (Start failed).
func captureChildPgid(cmd *exec.Cmd) (killFn func() error, cleanupFn func()) {
	if cmd.Process == nil {
		return func() error { return nil }, func() {}
	}

	pid := cmd.Process.Pid

	// Fallback path: taskkill closure + resume-only cleanup. Used if any
	// step of Job Object setup fails. Even on the fallback path, the child
	// must be resumed or it will hang forever.
	fallback := func() (func() error, func()) {
		_ = resumeChildThreads(uint32(pid))
		return func() error { return killViaTaskkill(pid) }, func() {}
	}

	// CreateJobObject(NULL, NULL) -> unnamed, default security.
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fallback()
	}

	// Configure JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE so closing the job handle
	// (or calling TerminateJobObject) terminates every process assigned to
	// the job, including descendants that inherit job membership.
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return fallback()
	}

	// Open the child's process handle with the access rights needed to
	// assign it to the job.
	procHandle, err := windows.OpenProcess(
		windows.PROCESS_TERMINATE|windows.PROCESS_SET_QUOTA,
		false,
		uint32(pid),
	)
	if err != nil {
		_ = windows.CloseHandle(job)
		return fallback()
	}

	if err := windows.AssignProcessToJobObject(job, procHandle); err != nil {
		_ = windows.CloseHandle(procHandle)
		_ = windows.CloseHandle(job)
		return fallback()
	}

	// Child is bound to the job. Resume its primary thread so user code
	// runs. Any descendants spawned from here on inherit job membership.
	if err := resumeChildThreads(uint32(pid)); err != nil {
		// Resume failed. The child is bound to the job but will never run.
		// Terminate the job to unwedge and fall back to taskkill (which
		// will now be a no-op since the child was never running, but keeps
		// the closure contract simple).
		_ = windows.TerminateJobObject(job, 1)
		_ = windows.CloseHandle(procHandle)
		_ = windows.CloseHandle(job)
		return func() error { return killViaTaskkill(pid) }, func() {}
	}

	// Both handles are retained until either killFn or cleanupFn closes
	// them. A once guard ensures either function can be invoked in any
	// order (kill-then-cleanup, cleanup-alone, kill-alone) without
	// double-close or use-after-close.
	var closeOnce sync.Once
	closeHandles := func() {
		closeOnce.Do(func() {
			_ = windows.CloseHandle(procHandle)
			_ = windows.CloseHandle(job)
		})
	}

	killFn = func() error {
		// TerminateJobObject kills every process in the job atomically.
		// Exit code 1 mirrors the SIGKILL semantics on Unix.
		termErr := windows.TerminateJobObject(job, 1)
		closeHandles()
		if termErr != nil {
			// Fallback: taskkill the immediate child tree.
			return killViaTaskkill(pid)
		}
		return nil
	}

	cleanupFn = closeHandles

	return killFn, cleanupFn
}

// resumeChildThreads enumerates all threads owned by the given PID via a
// Toolhelp snapshot and resumes each one. Called after AssignProcessToJobObject
// (normal path) or from the fallback path (job setup failed but we must not
// leave the child suspended).
//
// Windows CreateProcess creates a new process with a single primary thread,
// so in practice this iterates once, but the enumeration keeps the logic
// robust against edge cases (e.g. a suspended process with additional
// pre-spawned threads).
func resumeChildThreads(pid uint32) error {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return fmt.Errorf("CreateToolhelp32Snapshot: %w", err)
	}
	defer windows.CloseHandle(snap)

	var entry windows.ThreadEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))

	if err := windows.Thread32First(snap, &entry); err != nil {
		return fmt.Errorf("Thread32First: %w", err)
	}

	var resumed int
	for {
		if entry.OwnerProcessID == pid {
			th, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
			if err == nil {
				_, _ = windows.ResumeThread(th)
				_ = windows.CloseHandle(th)
				resumed++
			}
		}
		if err := windows.Thread32Next(snap, &entry); err != nil {
			// ERROR_NO_MORE_FILES marks end-of-enumeration; treat as success.
			break
		}
	}

	if resumed == 0 {
		return fmt.Errorf("no threads found for pid %d", pid)
	}
	return nil
}

// killViaTaskkill is the documented fallback when Job Object setup failed.
// Less robust against detached descendants but available on every Windows
// host without any prerequisite syscall support.
func killViaTaskkill(pid int) error {
	kill := exec.Command("taskkill", "/T", "/F", "/PID", fmt.Sprintf("%d", pid))
	kill.Stdout = io.Discard
	kill.Stderr = io.Discard
	return kill.Run()
}

// killProcessTree is retained for callers that don't have a captured-pgid
// closure. Prefer captureChildPgid for new code -- Job Objects survive the
// immediate parent shell exiting in a way taskkill alone does not.
func killProcessTree(cmd *exec.Cmd) error {
	return killViaTaskkill(cmd.Process.Pid)
}
