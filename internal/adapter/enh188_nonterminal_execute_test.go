package adapter

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/aitestmanagement/gtms-cli/internal/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failDurableErrorWrites installs the BUG-209 injection seam so the ENH-188
// recovery writes (whose bytes carry "status: error") fail, while the
// pre-invocation pending/in-progress writes succeed. BUG-209 made read-only
// handoffs replaceable, so this seam replaces the old chmod 0444 mechanism the
// record flagged under "Interaction With ENH-188".
func failDurableErrorWrites(t *testing.T) {
	t.Helper()
	// The "write" stage only: this is an ordinary write failure. An inspection failure,
	// which an earlier stage would be on Windows, is a refusal, and routes differently.
	result.InjectPublishFailureForTest(func(stage string, data []byte) error {
		if stage == "write" && bytes.Contains(data, []byte("status: error")) {
			return errors.New("injected durable-write failure")
		}
		return nil
	})
	t.Cleanup(func() { result.InjectPublishFailureForTest(nil) })
}

// runWritablePendingExecute runs an execute adapter that writes a non-terminal
// (pending) contract and exits 0, leaving the handoff writable (the write failure
// is injected by the test, not forced with a read-only file).
func runWritablePendingExecute(t *testing.T, root string, cfg *config.Config) (*InvokeResult, error) {
	t.Helper()
	scriptPath := writeTier2Script(t, root, "pending-writable.sh", "{ printf 'status: pending\\n'; } > \"${GTMS_RESULT_FILE}\"\nexit 0\n")
	resolved := &ResolvedAdapter{
		Command: "execute",
		Name:    "nonterminal-tier2",
		Config:  &config.AdapterConfig{Mode: "sync", Script: scriptPath, Framework: "playwright"},
		Tier:    2,
		Mode:    "sync",
	}
	flags := CommandFlags{ArtefactFile: "gtms/automation/specs/tc-acid-checkout.spec.ts", Framework: "playwright"}
	return InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-acid", withOwner(t, root, "tc-acid", flags))
}

// ENH-188 Option A: a synchronous Tier 2 EXECUTE adapter that exits leaving a
// readable but non-terminal result contract (empty / pending / in-progress)
// must be recorded as status: error with no result -- never the retired
// exit-code fallback pass. This is the six-combination Go matrix the record
// requires (status x exit code), plus a command-gate control proving the
// fallback survives for non-execute commands.

// writeTier2Script writes a POSIX-sh Tier 2 adapter script to root/testdata and
// returns its absolute path. body is the script after the shebang.
func writeTier2Script(t *testing.T, root, name, body string) string {
	t.Helper()
	p := filepath.Join(root, "testdata", name)
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755))
	return p
}

// nonTerminalExecuteCase drives one matrix cell through InvokeWithRoot and
// returns the InvokeResult plus the on-disk contract read back.
func runNonTerminalExecute(t *testing.T, root string, cfg *config.Config, scriptPath string) (*InvokeResult, *result.ResultContract) {
	t.Helper()
	resolved := &ResolvedAdapter{
		Command: "execute",
		Name:    "nonterminal-tier2",
		Config:  &config.AdapterConfig{Mode: "sync", Script: scriptPath, Framework: "playwright"},
		Tier:    2,
		Mode:    "sync",
	}
	flags := CommandFlags{ArtefactFile: "gtms/automation/specs/tc-acid-checkout.spec.ts", Framework: "playwright"}
	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-acid", withOwner(t, root, "tc-acid", flags))
	require.NoError(t, err)
	require.NotNil(t, res)
	rc, rcErr := result.Read(result.ResultPath(root, res.TaskID))
	require.NoError(t, rcErr)
	return res, rc
}

func TestENH188_NonTerminalExecute_Matrix(t *testing.T) {
	skipIfShort(t)

	// contractBody: how the adapter leaves its contract. Empty string means the
	// adapter never touches $GTMS_RESULT_FILE, so GTMS's pre-invocation
	// in-progress stamp stands.
	cases := []struct {
		name       string
		statusWord string // expected <status> rendering in the diagnostic
		writeBody  string // heredoc write (empty => leave handoff untouched)
	}{
		{
			name:       "empty",
			statusWord: "empty",
			writeBody: `{
  printf 'task: %s\n'    "${GTMS_TASK_ID}"
  printf 'command: %s\n' "${GTMS_COMMAND}"
  printf 'target: %s\n'  "${GTMS_TESTCASE}"
  printf 'adapter: nonterminal-tier2\n'
  printf 'mode: sync\n'
  printf 'status: ""\n'
} > "${GTMS_RESULT_FILE}"
`,
		},
		{
			name:       "pending",
			statusWord: "pending",
			writeBody: `{
  printf 'task: %s\n'    "${GTMS_TASK_ID}"
  printf 'command: %s\n' "${GTMS_COMMAND}"
  printf 'target: %s\n'  "${GTMS_TESTCASE}"
  printf 'adapter: nonterminal-tier2\n'
  printf 'mode: sync\n'
  printf 'status: pending\n'
} > "${GTMS_RESULT_FILE}"
`,
		},
		{
			name:       "in-progress",
			statusWord: "in-progress",
			writeBody:  "", // leave the pre-stamped in-progress handoff untouched
		},
	}

	for _, c := range cases {
		for _, exit := range []int{0, 3} {
			c, exit := c, exit
			label := c.name
			if exit == 0 {
				label += "_exit0"
			} else {
				label += "_exitN"
			}
			t.Run(label, func(t *testing.T) {
				root, cfg := setupExecuteTestProject(t)

				body := c.writeBody
				const stderrMarker = "fixture-runner: required tool 'frobnicate' not found on PATH"
				if exit != 0 {
					body += "echo \"" + stderrMarker + "\" >&2\nexit 3\n"
				} else {
					body += "exit 0\n"
				}
				scriptPath := writeTier2Script(t, root, "nonterminal.sh", body)

				res, rc := runNonTerminalExecute(t, root, cfg, scriptPath)

				// Core contract: error, no result, bound remediation.
				assert.Equal(t, "error", res.Status, "status must be error, never a fallback pass")
				assert.Equal(t, "", res.Result, "no test result for an unreported run")
				assert.Equal(t, nonTerminalExecuteRemediation, res.Remediation)
				assert.Equal(t, "error", rc.Status)
				assert.Empty(t, rc.Result, "handoff must carry no result")

				// Diagnostic first line (ENH-188 bound phrases).
				assert.Contains(t, res.Summary, "Execution result was not recorded")
				assert.Contains(t, res.Summary, "result contract status: "+c.statusWord)
				if exit == 0 {
					assert.Contains(t, res.Summary, "the adapter exited 0 without reporting a terminal outcome")
					assert.NotContains(t, res.Summary, "exited with code")
				} else {
					assert.Contains(t, res.Summary, "the adapter exited with code 3 without reporting a terminal outcome")
					assert.NotContains(t, res.Summary, "exited 0 without reporting")
					// Non-zero: the adapter's stderr is retained in the summary so
					// a missing-tooling diagnostic stays visible.
					assert.Contains(t, res.Summary, stderrMarker)
					assert.Contains(t, rc.Summary, stderrMarker)
				}

				// The handoff summary never claims a terminal pass.
				assert.NotContains(t, res.Summary, "result: pass")

				// Task moved to error/, not complete/.
				errTasks, err := task.List(root, "error")
				require.NoError(t, err)
				assert.Len(t, errTasks, 1)
				completeTasks, err := task.List(root, "complete")
				require.NoError(t, err)
				assert.Len(t, completeTasks, 0)
			})
		}
	}
}

// TestENH188_RemediationNeverDemandsResultUnconditionally pins the corrected
// second line: a terminal status: error contract without a result is valid, so
// the remediation must say "a result when status is complete", not demand one
// unconditionally.
func TestENH188_RemediationWording(t *testing.T) {
	assert.Contains(t, nonTerminalExecuteRemediation, "write a terminal status to $GTMS_RESULT_FILE before it exits")
	assert.Contains(t, nonTerminalExecuteRemediation, "a result when status is complete")
}

// TestENH188_CommandGate_FallbackPreservedForNonExecute proves the new branch is
// gated on command == "execute": a NON-execute (prime) Tier 2 adapter that exits
// 0 leaving a non-terminal contract still falls through to the exit-code handler
// and is recorded as complete/pass. This mirrors the create control
// tc-09d37e80, which stays green unchanged.
func TestENH188_CommandGate_FallbackPreservedForNonExecute(t *testing.T) {
	skipIfShort(t)
	root, cfg := setupExecuteTestProject(t)

	// A Tier 2 adapter that exits 0 without writing a terminal contract.
	scriptPath := writeTier2Script(t, root, "prime-nonterminal.sh", "exit 0\n")

	resolved := &ResolvedAdapter{
		Command: "prime",
		Name:    "prime-nonterminal",
		Config:  &config.AdapterConfig{Mode: "sync", Script: scriptPath, Framework: "playwright"},
		Tier:    2,
		Mode:    "sync",
	}
	flags := CommandFlags{ArtefactFile: "gtms/automation/specs/tc-acid-checkout.spec.ts", Framework: "playwright"}
	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-acid", withOwner(t, root, "tc-acid", flags))
	require.NoError(t, err)
	require.NotNil(t, res)

	assert.Equal(t, "complete", res.Status, "non-execute keeps the exit-code fallback")
	assert.Equal(t, "", res.Remediation, "no ENH-188 diagnostic for non-execute commands")
	assert.NotContains(t, res.Summary, "Execution result was not recorded")
	// The exit-code fallback records a pass for a non-execute command.
	rc, rcErr := result.Read(result.ResultPath(root, res.TaskID))
	require.NoError(t, rcErr)
	assert.Equal(t, "complete", rc.Status)
	assert.Equal(t, "pass", rc.Result)
}

// TestENH188_CompletedStamp pins the seventh owner approval: GTMS stamps
// `completed` on a VALID TERMINAL Tier 2 execute contract only when the adapter
// omitted it; an adapter-provided value is authoritative; it applies to
// complete/pass, complete/skip and error-without-result; and it never rescues a
// non-terminal contract.
func TestENH188_CompletedStamp(t *testing.T) {
	skipIfShort(t)

	// terminalScript builds a Tier 2 execute adapter that writes the given
	// status/result and, when completedLine is non-empty, a completed: line.
	terminalScript := func(status, resultLine, completedLine string) string {
		b := `{
  printf 'task: %s\n'    "${GTMS_TASK_ID}"
  printf 'command: %s\n' "${GTMS_COMMAND}"
  printf 'target: %s\n'  "${GTMS_TESTCASE}"
  printf 'adapter: terminal-tier2\n'
  printf 'mode: sync\n'
  printf 'status: ` + status + `\n'
`
		if resultLine != "" {
			b += "  printf 'result: " + resultLine + "\\n'\n"
		}
		b += "  printf 'summary: \"done\"\\n'\n"
		if completedLine != "" {
			b += "  printf 'completed: " + completedLine + "\\n'\n"
		}
		b += `} > "${GTMS_RESULT_FILE}"
exit 0
`
		return b
	}

	run := func(t *testing.T, scriptBody string) *result.ResultContract {
		t.Helper()
		root, cfg := setupExecuteTestProject(t)
		scriptPath := writeTier2Script(t, root, "terminal.sh", scriptBody)
		resolved := &ResolvedAdapter{
			Command: "execute",
			Name:    "terminal-tier2",
			Config:  &config.AdapterConfig{Mode: "sync", Script: scriptPath, Framework: "playwright"},
			Tier:    2,
			Mode:    "sync",
		}
		flags := CommandFlags{ArtefactFile: "gtms/automation/specs/tc-acid-checkout.spec.ts", Framework: "playwright"}
		res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-acid", withOwner(t, root, "tc-acid", flags))
		require.NoError(t, err)
		require.NotNil(t, res)
		rc, rcErr := result.Read(result.ResultPath(root, res.TaskID))
		require.NoError(t, rcErr)
		return rc
	}

	t.Run("complete_pass_absent_completed_is_stamped", func(t *testing.T) {
		rc := run(t, terminalScript("complete", "pass", ""))
		assert.Equal(t, "complete", rc.Status)
		assert.Equal(t, "pass", rc.Result)
		assert.NotEmpty(t, rc.Completed, "GTMS must stamp completed when the adapter omits it")
	})

	t.Run("complete_skip_absent_completed_is_stamped", func(t *testing.T) {
		rc := run(t, terminalScript("complete", "skip", ""))
		assert.Equal(t, "complete", rc.Status)
		assert.Equal(t, "skip", rc.Result)
		assert.NotEmpty(t, rc.Completed)
	})

	t.Run("error_without_result_absent_completed_is_stamped", func(t *testing.T) {
		rc := run(t, terminalScript("error", "", ""))
		assert.Equal(t, "error", rc.Status)
		assert.Empty(t, rc.Result)
		assert.NotEmpty(t, rc.Completed)
	})

	t.Run("adapter_supplied_completed_is_authoritative", func(t *testing.T) {
		const supplied = "2020-01-02T03:04:05Z"
		rc := run(t, terminalScript("complete", "pass", supplied))
		assert.Equal(t, supplied, rc.Completed, "an adapter-provided completed must be left unchanged")
	})

	t.Run("non_terminal_contract_is_not_rescued", func(t *testing.T) {
		// A pending contract on exit 0 must still be an ENH-188 error, not a
		// completed terminal contract. (The stamp lives inside the validated
		// terminal branch and never reaches this path.)
		root, cfg := setupExecuteTestProject(t)
		body := `{
  printf 'task: %s\n'    "${GTMS_TASK_ID}"
  printf 'command: %s\n' "${GTMS_COMMAND}"
  printf 'target: %s\n'  "${GTMS_TESTCASE}"
  printf 'adapter: nonterminal-tier2\n'
  printf 'mode: sync\n'
  printf 'status: pending\n'
} > "${GTMS_RESULT_FILE}"
exit 0
`
		scriptPath := writeTier2Script(t, root, "nonterminal2.sh", body)
		res, rc := runNonTerminalExecute(t, root, cfg, scriptPath)
		assert.Equal(t, "error", res.Status)
		assert.Equal(t, "", rc.Result)
		assert.Contains(t, res.Summary, "Execution result was not recorded")
	})
}

// requireReadOnlyEnforced skips the test when the platform does not enforce a
// read-only file permission (e.g. the suite runs as root), so the read-only
// handoff cases are exercised only where they are meaningful (REV-262).
func requireReadOnlyEnforced(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "probe")
	require.NoError(t, os.WriteFile(p, []byte("x"), 0o644))
	require.NoError(t, os.Chmod(p, 0o444))
	if err := os.WriteFile(p, []byte("y"), 0o644); err == nil {
		_ = os.Chmod(p, 0o644)
		t.Skip("platform does not enforce read-only file permissions here; read-only handoff case not exercised")
	}
	_ = os.Chmod(p, 0o644)
}

// TestENH188_EmptyHandoffRestoresExecuteIdentity pins CLAUDE-001 / CODEX-003: a
// genuinely truncated (0-byte) handoff after exit 0 is recorded as an error whose
// contract still carries the GTMS-held execute identity (command/task/adapter/
// mode/created), so result.IsTerminalExecuteContract recognises it (and thus
// isAlreadyPassing will not skip the test case on an earlier pass) and
// TargetIsTestCaseScoped can attribute it for delete.
func TestENH188_EmptyHandoffRestoresExecuteIdentity(t *testing.T) {
	skipIfShort(t)
	root, cfg := setupExecuteTestProject(t)
	// `: > "$GTMS_RESULT_FILE"` truncates the pre-created handoff to zero bytes.
	scriptPath := writeTier2Script(t, root, "truncate.sh", ": > \"${GTMS_RESULT_FILE}\"\nexit 0\n")
	res, rc := runNonTerminalExecute(t, root, cfg, scriptPath)

	assert.Equal(t, "error", res.Status)
	assert.Contains(t, res.Summary, "result contract status: empty")
	// Identity/provenance restored onto the truncated document.
	assert.Equal(t, "execute", rc.Command, "command must be restored so IsTerminalExecuteContract recognises the error")
	assert.NotEmpty(t, rc.Task, "task id must be restored")
	assert.Equal(t, "nonterminal-tier2", rc.Adapter)
	assert.Equal(t, "sync", rc.Mode)
	assert.NotEmpty(t, rc.Completed)
	assert.True(t, result.IsTerminalExecuteContract(rc), "the restored error contract must be a terminal execute contract")
	assert.True(t, result.TargetIsTestCaseScoped(rc), "delete must be able to attribute the handoff")
}

// renameOverReadOnlyWorks reports whether this platform lets a temp file be
// renamed over a read-only target (POSIX: yes, via the writable parent dir;
// Windows: no, MoveFileEx refuses a read-only target). It decides which of the
// replace-succeeds / replace-fails unwritable-handoff paths this platform can
// exercise (REV-263).
func renameOverReadOnlyWorks(t *testing.T) bool {
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, "t")
	require.NoError(t, os.WriteFile(target, []byte("old"), 0o644))
	require.NoError(t, os.Chmod(target, 0o444))
	tmp := filepath.Join(dir, "t.new")
	require.NoError(t, os.WriteFile(tmp, []byte("new"), 0o644))
	err := os.Rename(tmp, target)
	_ = os.Chmod(target, 0o644)
	return err == nil
}

// restoreResultsWritable chmods every handoff back to 0644 so t.TempDir cleanup
// can remove a read-only handoff on Windows.
func restoreResultsWritable(root string) {
	resultsDir := filepath.Join(root, ".gtms", "results")
	_ = filepath.WalkDir(resultsDir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			_ = os.Chmod(p, 0o644)
		}
		return nil
	})
}

// runReadOnlyPendingExecute runs an execute adapter that writes a non-terminal
// (pending) contract, makes the handoff read-only, and exits 0.
func runReadOnlyPendingExecute(t *testing.T, root string, cfg *config.Config) (*InvokeResult, error) {
	t.Helper()
	body := `{
  printf 'status: pending\n'
} > "${GTMS_RESULT_FILE}"
chmod 0444 "${GTMS_RESULT_FILE}"
exit 0
`
	scriptPath := writeTier2Script(t, root, "readonly.sh", body)
	resolved := &ResolvedAdapter{
		Command: "execute",
		Name:    "nonterminal-tier2",
		Config:  &config.AdapterConfig{Mode: "sync", Script: scriptPath, Framework: "playwright"},
		Tier:    2,
		Mode:    "sync",
	}
	flags := CommandFlags{ArtefactFile: "gtms/automation/specs/tc-acid-checkout.spec.ts", Framework: "playwright"}
	return InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-acid", withOwner(t, root, "tc-acid", flags))
}

// TestENH188_UnwritableHandoffStillMovesToErrorAndRerunNotBlocked pins
// CLAUDE-002 / CODEX-001: a readable but unwritable handoff must not strand the
// task in in-progress. The task lands in error/ and a second run is not refused
// as a duplicate active task -- on BOTH the replace-succeeds platform (no hard
// error, Reconstruct wrote a durable terminal error) and the replace-fails
// platform (hard Go error after the move).
func TestENH188_UnwritableHandoffStillMovesToErrorAndRerunNotBlocked(t *testing.T) {
	skipIfShort(t)
	root, cfg := setupExecuteTestProject(t)
	defer restoreResultsWritable(root)

	// BUG-209: a read-only handoff is now replaceable, so force the durable-write
	// failure with the injection seam. Both the recovery Update and the Reconstruct
	// fallback (their bytes carry "status: error") fail; the trusted execution
	// record still persists, so this is an error-status result (not a hard error)
	// carrying both failure warnings.
	failDurableErrorWrites(t)

	res, err := runWritablePendingExecute(t, root, cfg)

	errTasks, lerr := task.List(root, "error")
	require.NoError(t, lerr)
	assert.Len(t, errTasks, 1, "task must land in error/, not stay in in-progress")
	inProgress, lerr := task.List(root, "in-progress")
	require.NoError(t, lerr)
	assert.Len(t, inProgress, 0)

	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "error", res.Status)
	joined := strings.Join(res.Warnings, "\n")
	assert.Contains(t, joined, "contract update failed")
	assert.Contains(t, joined, "contract reconstruct failed")

	// A rerun is not blocked by the duplicate active-task guard.
	existing, ferr := task.FindByTarget(root, "execute", "tc-acid")
	require.NoError(t, ferr)
	assert.Nil(t, existing, "no active execute task must remain to block a rerun")
}

// TestENH188_UnwritableHandoff_ReplaceSucceeds pins REV-263 CLAUDE-001/CODEX-001
// fix (a)+(c) on a platform that can replace a read-only target: result.Update
// fails, result.Reconstruct writes a durable TERMINAL error, no hard Go error is
// returned, and the on-disk handoff is a terminal execute error (so
// isAlreadyPassing cannot fall back to an older pass).
func TestENH188_UnwritableHandoff_ReplaceSucceeds(t *testing.T) {
	skipIfShort(t)
	requireReadOnlyEnforced(t)
	if !renameOverReadOnlyWorks(t) {
		t.Skip("platform cannot replace a read-only target via rename; replace-succeeds path not exercised here")
	}
	root, cfg := setupExecuteTestProject(t)
	defer restoreResultsWritable(root)

	res, err := runReadOnlyPendingExecute(t, root, cfg)
	require.NoError(t, err, "Reconstruct should replace the read-only handoff, so no hard error")
	require.NotNil(t, res)
	assert.Equal(t, "error", res.Status)

	rc, rcErr := result.Read(result.ResultPath(root, res.TaskID))
	require.NoError(t, rcErr)
	assert.Equal(t, "error", rc.Status, "the durable handoff must be a terminal error, not the adapter's pending doc")
	assert.Empty(t, rc.Result)
	assert.Equal(t, "execute", rc.Command)
	assert.True(t, result.IsTerminalExecuteContract(rc))
}

// TestENH188_RejectedRunNeverRecordsOutcomePass pins REV-263 CLAUDE-001 fix (b):
// the pipeline record is built from an in-memory error contract, never by
// re-reading the handoff, so an adapter that wrote status: pending, result: pass
// and then made the handoff unwritable can never leave an execution record with
// outcome: pass.
func TestENH188_RejectedRunNeverRecordsOutcomePass(t *testing.T) {
	skipIfShort(t)
	requireReadOnlyEnforced(t)
	root, cfg := setupExecuteTestProject(t)
	defer restoreResultsWritable(root)

	body := `{
  printf 'status: pending\n'
  printf 'result: pass\n'
} > "${GTMS_RESULT_FILE}"
chmod 0444 "${GTMS_RESULT_FILE}"
exit 0
`
	scriptPath := writeTier2Script(t, root, "readonly-pass.sh", body)
	resolved := &ResolvedAdapter{
		Command: "execute",
		Name:    "nonterminal-tier2",
		Config:  &config.AdapterConfig{Mode: "sync", Script: scriptPath, Framework: "playwright"},
		Tier:    2,
		Mode:    "sync",
	}
	flags := CommandFlags{ArtefactFile: "gtms/automation/specs/tc-acid-checkout.spec.ts", Framework: "playwright"}
	_, _ = InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-acid", withOwner(t, root, "tc-acid", flags))

	// No execution results file for this run may record outcome: pass.
	execDir := filepath.Join(root, "gtms", "execution")
	entries, _ := os.ReadDir(execDir)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, rErr := os.ReadFile(filepath.Join(execDir, e.Name()))
		if rErr != nil {
			continue
		}
		assert.NotContains(t, string(data), "outcome: pass", "a rejected run must never record outcome: pass")
	}
}

// TestENH188_UnwritableOmittedCompletedFailsTheRun: when a valid terminal contract omits
// completed and the restore write (which carries the completed stamp) fails, the run is
// not a pass. BUG-209 section 9 replaced the earlier warning: no Tier 2 path reports a
// pass or moves a task to complete/ unless its final outcome was durably persisted.
func TestENH188_UnwritableOmittedCompletedFailsTheRun(t *testing.T) {
	skipIfShort(t)
	root, cfg := setupExecuteTestProject(t)
	// BUG-209: force the restore write (which stamps completed onto the terminal
	// contract, so its bytes carry "status: complete") to fail via injection,
	// replacing the old read-only (chmod 0444) mechanism.
	result.InjectPublishFailureForTest(func(_ string, data []byte) error {
		if bytes.Contains(data, []byte("status: complete")) {
			return errors.New("injected restore-write failure")
		}
		return nil
	})
	defer result.InjectPublishFailureForTest(nil)
	body := `{
  printf 'task: %s\n'    "${GTMS_TASK_ID}"
  printf 'command: %s\n' "${GTMS_COMMAND}"
  printf 'target: %s\n'  "${GTMS_TESTCASE}"
  printf 'adapter: terminal-tier2\n'
  printf 'mode: sync\n'
  printf 'status: complete\n'
  printf 'result: pass\n'
  printf 'summary: "done"\n'
} > "${GTMS_RESULT_FILE}"
exit 0
`
	scriptPath := writeTier2Script(t, root, "terminal-writable.sh", body)
	resolved := &ResolvedAdapter{
		Command: "execute",
		Name:    "terminal-tier2",
		Config:  &config.AdapterConfig{Mode: "sync", Script: scriptPath, Framework: "playwright"},
		Tier:    2,
		Mode:    "sync",
	}
	flags := CommandFlags{ArtefactFile: "gtms/automation/specs/tc-acid-checkout.spec.ts", Framework: "playwright"}
	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-acid", withOwner(t, root, "tc-acid", flags))
	require.Error(t, err, "the outcome did not land, so this is not a pass")
	assert.Nil(t, res)
	assert.Contains(t, err.Error(), "contract restore write failed")
	assert.True(t, Started(res, err), "the adapter ran")
	done, lerr := task.List(root, "complete")
	require.NoError(t, lerr)
	assert.Empty(t, done, "no task may reach complete/")
	errored, lerr := task.List(root, "error")
	require.NoError(t, lerr)
	assert.Len(t, errored, 1, "the task must be under error/")
}

// TestENH188_RejectedRunRestoresTrustedFramework pins REV-264 CODEX-001: a
// rejected run whose adapter wrote a WRONG non-empty framework must have the
// trusted tf.Framework restored on both the durable handoff and the execution
// record, so isAlreadyPassing (which filters by framework) cannot drop this
// newest error and fall back to an older pass.
func TestENH188_RejectedRunRestoresTrustedFramework(t *testing.T) {
	skipIfShort(t)
	root, cfg := setupExecuteTestProject(t) // wiring framework is "playwright"
	// Adapter writes a non-terminal contract carrying a WRONG framework, then
	// exits 0 (the handoff stays writable, so result.Update succeeds).
	body := `{
  printf 'status: pending\n'
  printf 'framework: wrong-fw\n'
} > "${GTMS_RESULT_FILE}"
exit 0
`
	scriptPath := writeTier2Script(t, root, "wrongfw.sh", body)
	resolved := &ResolvedAdapter{
		Command: "execute",
		Name:    "nonterminal-tier2",
		Config:  &config.AdapterConfig{Mode: "sync", Script: scriptPath, Framework: "playwright"},
		Tier:    2,
		Mode:    "sync",
	}
	flags := CommandFlags{ArtefactFile: "gtms/automation/specs/tc-acid-checkout.spec.ts", Framework: "playwright"}
	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-acid", withOwner(t, root, "tc-acid", flags))
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "error", res.Status)

	rc, rcErr := result.Read(result.ResultPath(root, res.TaskID))
	require.NoError(t, rcErr)
	assert.Equal(t, "error", rc.Status)
	assert.Equal(t, "playwright", rc.Framework, "the trusted framework must overwrite the adapter's wrong value")

	// The trusted execution record also carries the correct framework and error.
	execDir := filepath.Join(root, "gtms", "execution")
	entries, _ := os.ReadDir(execDir)
	foundTrusted := false
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, rErr := os.ReadFile(filepath.Join(execDir, e.Name()))
		if rErr != nil {
			continue
		}
		s := string(data)
		if strings.Contains(s, "framework: playwright") {
			foundTrusted = true
			assert.NotContains(t, s, "framework: wrong-fw")
			assert.NotContains(t, s, "outcome: pass")
		}
	}
	assert.True(t, foundTrusted, "a trusted execution record with the correct framework must be written")
}

// blockExecutionDir replaces gtms/execution with a regular FILE so
// execution.Write's MkdirAll fails -- the trusted record cannot be persisted.
func blockExecutionDir(t *testing.T, root string) {
	t.Helper()
	execDir := filepath.Join(root, "gtms", "execution")
	require.NoError(t, os.RemoveAll(execDir))
	require.NoError(t, os.WriteFile(execDir, []byte("blocker"), 0o644))
}

// TestENH188_AllWritesFailReportsBoundDiagnostic pins REV-265 CLAUDE-002/CODEX-002:
// when both handoff writes AND the trusted execution record all fail, the hard
// Go error carries the COMPLETE bound diagnostic (first line + remediation) and
// every persistence cause. Runs only where a read-only handoff cannot be replaced
// by rename (so Reconstruct also fails); skips elsewhere with a reason.
func TestENH188_AllWritesFailReportsBoundDiagnostic(t *testing.T) {
	skipIfShort(t)
	root, cfg := setupExecuteTestProject(t)
	defer restoreResultsWritable(root)
	// BUG-209: both handoff writes fail via injection (their bytes carry
	// "status: error"), replacing the old read-only mechanism; the execution
	// record fails because its directory is blocked. All three persistence writes
	// fail -> hard error with the complete bound diagnostic.
	failDurableErrorWrites(t)
	blockExecutionDir(t, root)

	_, err := runWritablePendingExecute(t, root, cfg)
	require.Error(t, err, "all three persistence writes failed -> hard error")
	assert.Contains(t, err.Error(), "Execution result was not recorded", "the bound first line must be in the hard error")
	assert.Contains(t, err.Error(), "A Tier 2 execute adapter must write a terminal status", "the remediation must be in the hard error")
	assert.Contains(t, err.Error(), "could not persist a durable error", "the persistence causes must be reported")
}

// TestENH188_WritableHandoffUnwritableExecutionWarns pins REV-265 CODEX-003: when
// the handoff is durable but the trusted execution record cannot be written, the
// run returns an error-status result (not a hard error) carrying the
// "Pipeline record could not be written" warning, so bulk can surface it.
func TestENH188_WritableHandoffUnwritableExecutionWarns(t *testing.T) {
	skipIfShort(t)
	root, cfg := setupExecuteTestProject(t)
	blockExecutionDir(t, root)

	scriptPath := writeTier2Script(t, root, "pending-writable.sh", "{ printf 'status: pending\\n'; } > \"${GTMS_RESULT_FILE}\"\nexit 0\n")
	resolved := &ResolvedAdapter{
		Command: "execute",
		Name:    "nonterminal-tier2",
		Config:  &config.AdapterConfig{Mode: "sync", Script: scriptPath, Framework: "playwright"},
		Tier:    2,
		Mode:    "sync",
	}
	flags := CommandFlags{ArtefactFile: "gtms/automation/specs/tc-acid-checkout.spec.ts", Framework: "playwright"}
	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-acid", withOwner(t, root, "tc-acid", flags))
	require.NoError(t, err, "a durable handoff -> error-status result, not a hard error")
	require.NotNil(t, res)
	assert.Equal(t, "error", res.Status)
	assert.Contains(t, strings.Join(res.Warnings, "\n"), "Pipeline record could not be written", "the execution-record failure must be surfaced as a warning")
}

// sanity: ensure the stderr retention path does not accidentally swallow a
// trailing newline-only stderr into the summary.
func TestENH188_Exit0SummaryHasNoStderrSuffix(t *testing.T) {
	skipIfShort(t)
	root, cfg := setupExecuteTestProject(t)
	// exit 0, pending, stdout noise but no stderr.
	body := `echo "1 passed"
{
  printf 'task: %s\n'    "${GTMS_TASK_ID}"
  printf 'command: %s\n' "${GTMS_COMMAND}"
  printf 'target: %s\n'  "${GTMS_TESTCASE}"
  printf 'adapter: nonterminal-tier2\n'
  printf 'mode: sync\n'
  printf 'status: pending\n'
} > "${GTMS_RESULT_FILE}"
exit 0
`
	scriptPath := writeTier2Script(t, root, "exit0-stdout.sh", body)
	res, _ := runNonTerminalExecute(t, root, cfg, scriptPath)
	// The summary is exactly the first diagnostic line (single line) for exit 0.
	assert.False(t, strings.Contains(res.Summary, "\n"), "exit-0 summary must be the single diagnostic line")
	assert.Contains(t, res.Summary, "result contract status: pending")
}
