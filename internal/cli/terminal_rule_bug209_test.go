package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/aitestmanagement/gtms-cli/internal/task"
)

// asyncTerminalProject has one in-progress execute task whose status script writes an
// oversized terminal contract, so finalising it needs a cap write.
func asyncTerminalProject(t *testing.T) (root string, tf *task.TaskFile) {
	t.Helper()
	root = t.TempDir()
	writeTestFile(t, root, "gtms.config", "project:\n  name: t\n  repo: r\n")
	body := "#!/bin/sh\nprintf 'task: task-bbbbbbbb\\ncommand: execute\\ntarget: tc-bbbbbbbb\\nadapter: asyncfx\\nmode: async\\nframework: fx\\nstatus: complete\\nresult: pass\\nsummary: \"' > \"$GTMS_RESULT_FILE\"\n" +
		"i=0\nwhile [ $i -lt 3000 ]; do printf 'x' >> \"$GTMS_RESULT_FILE\"; i=$((i+1)); done\n" +
		"printf '\"\\n' >> \"$GTMS_RESULT_FILE\"\nexit 0\n"
	writeTestFile(t, root, "status.sh", body)
	seedStatusTask(t, root, "execute", "task-bbbbbbbb", "tc-bbbbbbbb", "in-progress")
	found, err := task.FindByTarget(root, "execute", "tc-bbbbbbbb", task.ValidStatuses...)
	require.NoError(t, err)
	require.NotNil(t, found)
	return root, found
}

func executeRecordCount(t *testing.T, root string) int {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(root, "gtms", "execution", "*"))
	require.NoError(t, err)
	return len(m)
}

// Rule: async finalisation records nothing unless its final document landed. A hook
// replaces the handoff before every publish of the cap write, so the retries run out.
func TestAsyncFinalisation_ConcurrencyErrorFailsWithNoRecord(t *testing.T) {
	skipIfShort(t)
	root, tf := asyncTerminalProject(t)
	path := result.ResultPath(root, tf.ID)
	n := 0
	result.InjectPublishFailureForTest(func(stage string, _ []byte) error {
		if stage == "recheck" {
			n++
			_ = os.Remove(path)
			_ = os.WriteFile(path, []byte("status: in-progress\nsummary: other"+strings.Repeat("x", n)+"\n"), 0o644)
		}
		return nil
	})
	t.Cleanup(func() { result.InjectPublishFailureForTest(nil) })

	err := checkAsyncStatus(context.Background(), root, asyncStatusConfig("execute", "status.sh"), tf, "execute")

	require.Error(t, err)
	assert.True(t, result.IsConcurrentUpdate(err), "got %v", err)
	assert.Equal(t, 4, n, "one attempt and three retries")
	assert.Zero(t, executeRecordCount(t, root), "no execution record may be written")
	done, lerr := task.List(root, "complete")
	require.NoError(t, lerr)
	assert.Empty(t, done, "no task may reach complete/")
	errored, lerr := task.List(root, "error")
	require.NoError(t, lerr)
	assert.Len(t, errored, 1, "the task must be under error/")
}

// REV-279 CLAUDE-002: a failed cap write on a handoff that is still running is only a
// warning. The task stays pollable, the adapter's file is not overwritten, no record is
// written, and a later poll finalises the terminal result.
func TestAsyncCap_FailureOnRunningHandoffStaysPollableThenFinalises(t *testing.T) {
	skipIfShort(t)
	failures := map[string]func(path string) func(string, []byte) error{
		"ordinary write failure": func(string) func(string, []byte) error {
			return func(stage string, _ []byte) error {
				if stage == "write" {
					return errors.New("injected write failure")
				}
				return nil
			}
		},
		"concurrent replacement": func(path string) func(string, []byte) error {
			n := 0
			return func(stage string, _ []byte) error {
				if stage == "recheck" {
					n++
					_ = os.Remove(path)
					_ = os.WriteFile(path, []byte("status: in-progress\nsummary: other"+strings.Repeat("x", n)+"\n"), 0o644)
				}
				return nil
			}
		},
	}
	for name, hook := range failures {
		t.Run(name, func(t *testing.T) {
			root, tf := asyncTerminalProject(t)
			path := result.ResultPath(root, tf.ID)
			running := "#!/bin/sh\nprintf 'task: task-bbbbbbbb\\ncommand: execute\\ntarget: tc-bbbbbbbb\\nadapter: asyncfx\\nmode: async\\nframework: fx\\nstatus: in-progress\\nsummary: \"' > \"$GTMS_RESULT_FILE\"\n" +
				"i=0\nwhile [ $i -lt 3000 ]; do printf 'x' >> \"$GTMS_RESULT_FILE\"; i=$((i+1)); done\n" +
				"printf '\"\\n' >> \"$GTMS_RESULT_FILE\"\nexit 0\n"
			writeTestFile(t, root, "status.sh", running)

			result.InjectPublishFailureForTest(hook(path))
			t.Cleanup(func() { result.InjectPublishFailureForTest(nil) })
			var pollErr error
			stderr := captureStderr(t, func() {
				pollErr = checkAsyncStatus(context.Background(), root, asyncStatusConfig("execute", "status.sh"), tf, "execute")
			})
			result.InjectPublishFailureForTest(nil)

			require.NoError(t, pollErr, "a failed cap on a running handoff is not a failure of the run")
			assert.Contains(t, stderr, "Spill cap write failed", "the failure is still a visible warning")
			assert.Zero(t, executeRecordCount(t, root), "no execution record may be written")
			for status, want := range map[string]int{"in-progress": 1, "error": 0, "complete": 0} {
				got, lerr := task.List(root, status)
				require.NoError(t, lerr)
				assert.Len(t, got, want, "tasks in %s/", status)
			}

			// A later poll, with the adapter finished, finalises the result.
			writeTestFile(t, root, "status.sh", "#!/bin/sh\nprintf 'task: task-bbbbbbbb\\ncommand: execute\\ntarget: tc-bbbbbbbb\\nadapter: asyncfx\\nmode: async\\nframework: fx\\nstatus: complete\\nresult: pass\\nsummary: \"done\"\\n' > \"$GTMS_RESULT_FILE\"\nexit 0\n")
			found, ferr := task.FindByTarget(root, "execute", "tc-bbbbbbbb", task.ValidStatuses...)
			require.NoError(t, ferr)
			require.NotNil(t, found)
			require.NoError(t, checkAsyncStatus(context.Background(), root, asyncStatusConfig("execute", "status.sh"), found, "execute"))
			done, lerr := task.List(root, "complete")
			require.NoError(t, lerr)
			assert.Len(t, done, 1, "the later poll finalises the task")
			errored, lerr := task.List(root, "error")
			require.NoError(t, lerr)
			assert.Empty(t, errored)
			rc, rerr := result.Read(path)
			require.NoError(t, rerr)
			assert.Equal(t, "complete", rc.Status)
			assert.Equal(t, "pass", rc.Result)
		})
	}
}

// The running-handoff fallback must not touch the adapter's file: with no canonical
// contract there is nothing to reconstruct from.
func TestAsyncCap_FailureOnRunningHandoffDoesNotOverwriteIt(t *testing.T) {
	skipIfShort(t)
	root, tf := asyncTerminalProject(t)
	path := result.ResultPath(root, tf.ID)
	writeTestFile(t, root, "status.sh", "#!/bin/sh\nprintf 'task: task-bbbbbbbb\\ncommand: execute\\ntarget: tc-bbbbbbbb\\nadapter: asyncfx\\nmode: async\\nframework: fx\\nstatus: in-progress\\nsummary: \"' > \"$GTMS_RESULT_FILE\"\n"+
		"i=0\nwhile [ $i -lt 3000 ]; do printf 'x' >> \"$GTMS_RESULT_FILE\"; i=$((i+1)); done\n"+
		"printf '\"\\n' >> \"$GTMS_RESULT_FILE\"\nexit 0\n")
	// Only the first write fails. A reconstruct after it would land the capped document,
	// and the assertion below would catch that.
	writes := 0
	result.InjectPublishFailureForTest(func(stage string, _ []byte) error {
		if stage == "write" {
			writes++
			if writes == 1 {
				return errors.New("injected write failure")
			}
		}
		return nil
	})
	t.Cleanup(func() { result.InjectPublishFailureForTest(nil) })
	captureStderr(t, func() {
		require.NoError(t, checkAsyncStatus(context.Background(), root, asyncStatusConfig("execute", "status.sh"), tf, "execute"))
	})
	result.InjectPublishFailureForTest(nil)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), strings.Repeat("x", 3000), "the adapter's uncapped document is untouched")
	assert.Equal(t, 1, writes, "nothing may try to write again over the running handoff")
}

// Rule: a concurrency error reaches the operator and the exit code, with no remediation.
func TestSurfaceAsyncRefusals_ConcurrencyErrorIsReported(t *testing.T) {
	cause := &result.ConcurrentUpdateError{Rel: ".gtms/results/task-a.handoff.yaml", Retries: 3}
	var err error
	stderr := captureStderr(t, func() { err = surfaceAsyncRefusals([]error{cause, cause}) })
	require.Error(t, err)
	assert.Equal(t, 1, strings.Count(stderr, "kept changing during the update"), stderr)
	assert.NotContains(t, stderr, "remove it by hand")
}

// CLAUDE-004: the concurrency error is printed in full beside the clipped line, once.
func TestPrintBulkRefusal_ConcurrencyErrorInFull(t *testing.T) {
	cause := &result.ConcurrentUpdateError{Rel: ".gtms/results/task-a.handoff.yaml", Retries: 3}
	reported := map[string]bool{}
	stderr := captureStderr(t, func() {
		printBulkRefusal(cause, reported)
		printBulkRefusal(cause, reported)
	})
	assert.Equal(t, 1, strings.Count(stderr, cause.Error()), stderr)
	assert.NotContains(t, stderr, "remove it by hand")
}

// CLAUDE-005: a refused results directory is named once in a bulk run, not again on the
// first test case.
func TestBUG209_BulkNamesARefusedResultsDirOnce(t *testing.T) {
	skipIfShort(t)
	root := t.TempDir()
	enh231Spec(t, root, "bulk", "tc-11111111")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-11111111", Framework: "bats", Adapter: "bats-runner", Result: "pass"})
	results := filepath.Join(root, ".gtms", "results")
	require.NoError(t, os.RemoveAll(results))
	require.NoError(t, os.WriteFile(results, []byte("a file"), 0o644))

	out, err := runBulk(t, root, "bulk", enh231Config(t, ""), true)
	require.Error(t, err)
	assert.Equal(t, 1, strings.Count(out, "refused results directory .gtms/results: could not be inspected: not a directory"), out)
}

// AC33 (CLAUDE-003): an ordinary bulk run says a test started and then failed, with no
// script-drift or allow-stale flag involved.
func TestBUG209_OrdinaryBulkReportsStartedThenFailed(t *testing.T) {
	skipIfShort(t)
	root := t.TempDir()
	enh231Spec(t, root, "bulk", "tc-11111111")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-11111111", Framework: "bats", Adapter: "bats-runner", Result: "fail"})
	cfg := batsExecuteConfig()
	cfg.Adapters["execute"]["bats-runner"].Command = `rm -f {result_file} && mkdir {result_file}`

	out, err := runBulk(t, root, "bulk", cfg, true)
	require.Error(t, err)
	assert.Contains(t, out, "started, then failed", out)
}

func TestPrintStartedThenFailed(t *testing.T) {
	stderr := captureStderr(t, func() { printStartedThenFailed("tc-11111111") })
	assert.Contains(t, stderr, "tc-11111111 started, then failed.")
}
