package cli

// ENH-231 Go coverage for the single-test-case behaviour matrix, unreadable
// scripts, post-admission failures, and the manual/agent bulk bypass of the
// active-task check. Companion to enh231_execute_test.go (bulk matrix) and
// internal/link/accept_script_drift_test.go (the acceptance helper).
//
// Timeout and cancellation (Owner Decision 4): timeout is covered on the single
// path through the adapter's own timeout setting, which is the deadline the
// invoker applies once the invocation is admitted. Cancellation is covered on
// the bulk path with an already-cancelled command context. It cannot be covered
// on the single path that way: the single path's first gate (requireGitRepo)
// shells out to git under the same context, so a cancelled context is refused
// there -- before admission -- and never reaches the acceptance point.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/adapter"
	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
	"github.com/aitestmanagement/gtms-cli/internal/task"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

const enh231TC = "tc-11111111"

// enh231SingleProject builds a git-backed project with one spec under drift/
// and a current bats wiring record for it (adapter bats-runner unless given).
func enh231SingleProject(t *testing.T, wiredAdapter string) string {
	t.Helper()
	root := setupCompatProject(t)
	enh231Spec(t, root, "drift", enh231TC)
	if wiredAdapter == "" {
		wiredAdapter = "bats-runner"
	}
	seedLegacyRecord(t, root, legacyRecord{TC: enh231TC, Framework: "bats", Adapter: wiredAdapter})
	return root
}

func enh231ArtefactPath(root string) string {
	return filepath.Join(root, "test/acceptance", enh231TC+".bats")
}

// enh231EditScript changes only the script and returns its new hash.
func enh231EditScript(t *testing.T, root string) string {
	t.Helper()
	p := enh231ArtefactPath(root)
	require.NoError(t, os.WriteFile(p, []byte("# fixture artefact for "+enh231TC+"\n# edited for ENH-231\n"), 0644))
	h, err := pipeline.HashFile(p)
	require.NoError(t, err)
	return h
}

func enh231EditSpec(t *testing.T, root string) {
	t.Helper()
	spec := filepath.Join(root, "gtms/test/cases/drift", enh231TC+"-x.md")
	f, err := os.OpenFile(spec, os.O_APPEND|os.O_WRONLY, 0644)
	require.NoError(t, err)
	_, _ = f.WriteString("\nAppended step.\n")
	require.NoError(t, f.Close())
}

func wiringBytes(t *testing.T, root, tcID string) []byte {
	t.Helper()
	p, err := wiring.Path(root, tcID, "bats")
	require.NoError(t, err)
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	return b
}

func countExecuteTasks(t *testing.T, root string) int {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(root, "gtms", "tasks", "*", "task-*-execute-*.md"))
	return len(m)
}

func runSingle(t *testing.T, root string, cfg *config.Config, args ...string) (string, error) {
	t.Helper()
	restore := withExecuteGlobals(t, root, cfg)
	defer restore()
	return runCmdCaptureStderr(t, newExecuteCmd(), args...)
}

// enh231AssertNoMutation asserts a single-path refusal left the wiring
// byte-for-byte unchanged and dispatched nothing.
func enh231AssertNoMutation(t *testing.T, root string, wiringBefore []byte, handoffsBefore, tasksBefore int) {
	t.Helper()
	assert.Equal(t, string(wiringBefore), string(wiringBytes(t, root, enh231TC)), "wiring must be byte-for-byte unchanged")
	assert.Equal(t, handoffsBefore, countHandoffs(t, root), "no result contract may be created")
	assert.Equal(t, tasksBefore, countExecuteTasks(t, root), "no execute task may be created")
}

// --- 1. Single-test-case behaviour matrix ---

// Script-only drift, absent policy: the wiring takes the new artefact-hash,
// keeps its testcase-hash, and the run is dispatched.
func TestENH231_Single_ScriptOnlyDriftAcceptedAndDispatched(t *testing.T) {
	skipIfShort(t)
	root := enh231SingleProject(t, "")
	recBefore, _, err := wiring.Find(root, enh231TC, "bats")
	require.NoError(t, err)
	newArt := enh231EditScript(t, root)
	require.NotEqual(t, recBefore.ArtefactHash, newArt)

	out, err := runSingle(t, root, enh231Config(t, ""), enh231TC)
	require.NoError(t, err, out)

	rec, _, err := wiring.Find(root, enh231TC, "bats")
	require.NoError(t, err)
	assert.Equal(t, newArt, rec.ArtefactHash, "accept mode must refresh the artefact-hash")
	assert.Equal(t, recBefore.TestCaseHash, rec.TestCaseHash, "testcase-hash must be preserved")
	// ENH-222 Draft D: the notice is printed after dispatch is proven and says it ran.
	assert.Contains(t, out, "GTMS accepted the change and ran "+enh231TC)
	assert.NotContains(t, out, "wiring updated automatically")
	assert.Equal(t, 1, countHandoffs(t, root), "the accepted run must be dispatched")
}

// The four single-path refusals: each exits non-zero, leaves the wiring
// byte-for-byte unchanged and dispatches nothing.
func TestENH231_Single_RefusalsLeaveWiringUnchanged(t *testing.T) {
	cases := []struct {
		name         string
		wiredAdapter string
		policy       string
		prepare      func(t *testing.T, root string)
		wantOut      string
	}{
		{
			name:    "refuse mode",
			policy:  config.OnScriptChangeRefuse,
			// ENH-222 Draft B: a changed script under refuse mode.
			wantOut: "was not run because its automation script changed",
		},
		{
			name: "spec and script drift",
			prepare: func(t *testing.T, root string) {
				enh231EditSpec(t, root)
			},
			// ENH-222 Draft A: spec drift leads over a simultaneous script change.
			wantOut: "was not run because its test case spec changed",
		},
		{
			name: "active execute task",
			prepare: func(t *testing.T, root string) {
				_, err := task.Create(root, &task.TaskFile{
					ID: "task-active01", Type: "execute", Target: enh231TC,
					Adapter: "bats-runner", Status: "in-progress", Created: "2026-10-23T00:00:00Z",
				}, "active task fixture")
				require.NoError(t, err)
			},
			wantOut: "already exists",
		},
		{
			name:         "unconfigured wiring adapter",
			wiredAdapter: "ghost-runner",
			wantOut:      "not configured",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			skipIfShort(t)
			root := enh231SingleProject(t, tc.wiredAdapter)
			if tc.prepare != nil {
				tc.prepare(t, root)
			}
			wiringBefore := wiringBytes(t, root, enh231TC)
			handoffsBefore := countHandoffs(t, root)
			tasksBefore := countExecuteTasks(t, root)
			enh231EditScript(t, root)

			out, err := runSingle(t, root, enh231Config(t, tc.policy), enh231TC)
			require.Error(t, err, "single-path refusal must exit non-zero")
			assert.Contains(t, out, tc.wantOut)
			assert.NotContains(t, out, "wiring updated automatically")
			enh231AssertNoMutation(t, root, wiringBefore, handoffsBefore, tasksBefore)
		})
	}
}

// --- 2. Unreadable script ---

// makeUnreadable removes read access to path, or skips when this platform or
// user cannot enforce it (Windows without icacls, or a Unix root user).
func makeUnreadable(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		user := os.Getenv("USERNAME")
		if user == "" {
			t.Skip("cannot determine the Windows user to deny read access")
		}
		if out, err := exec.Command("icacls", path, "/deny", user+":(R)").CombinedOutput(); err != nil {
			t.Skipf("icacls cannot deny read access here: %v: %s", err, out)
		}
		t.Cleanup(func() { _ = exec.Command("icacls", path, "/remove:d", user).Run() })
	} else {
		require.NoError(t, os.Chmod(path, 0))
		t.Cleanup(func() { _ = os.Chmod(path, 0644) })
	}
	if _, err := os.ReadFile(path); err == nil {
		t.Skip("permission bits are not enforced for this user; cannot make the script unreadable")
	}
}

// An edited script that cannot be read is never accepted and never runs
// (ENH-231 Owner Decision 6): the single path refuses with an "Unreadable
// artefact" error, the bulk path skips it, and the wiring is not rewritten.
func TestENH231_UnreadableScriptNeverAccepted(t *testing.T) {
	skipIfShort(t)
	root := enh231SingleProject(t, "")
	enh231EditScript(t, root)
	makeUnreadable(t, enh231ArtefactPath(root))
	wiringBefore := wiringBytes(t, root, enh231TC)

	out, err := runSingle(t, root, enh231Config(t, ""), enh231TC)
	require.Error(t, err, "single path must refuse an unreadable script: %s", out)
	// ENH-222 Draft J: name the file; tell the user to make it readable.
	assert.Contains(t, out, enh231TC+" was not run because its automation script could not be read")
	assert.Contains(t, out, "Check that the path is a readable file, then retry.")
	assert.NotContains(t, out, "Recorded result", "an unreadable script must never run")
	assert.NotContains(t, out, "wiring updated automatically")
	assert.Equal(t, string(wiringBefore), string(wiringBytes(t, root, enh231TC)), "single path must not accept an unreadable script")

	out, _ = runBulk(t, root, "drift", enh231Config(t, ""), false)
	// BUG-200: an unreadable script is "not run" (non-zero exit), not a skip.
	// ENH-222: the plain-language reason is "script unreadable".
	assert.Contains(t, out, "not run (script unreadable)", "bulk must report an unreadable script as not run, never execute it")
	assert.NotContains(t, out, "wiring updated automatically")
	assert.Equal(t, string(wiringBefore), string(wiringBytes(t, root, enh231TC)), "bulk path must not accept an unreadable script")
}

// --- 3. Post-admission failures keep the accepted hash ---

// Each case is admitted (script-only drift, all CLI gates pass), so the hash
// is accepted; the later failure must not roll it back.
func TestENH231_Single_PostAdmissionFailuresKeepAcceptedHash(t *testing.T) {
	cases := []struct {
		name    string
		entry   *config.AdapterConfig
		wantErr string
		// ENH-222: the acceptance notice depends on whether the process started.
		// A started-then-failed run (non-zero exit, post-launch timeout) says it
		// ran (Draft D); a pre-launch failure (prompt assembly, invalid timeout)
		// says the change was accepted but the test could not start (Draft E).
		wantNotice string
		started    bool
	}{
		// Adapter process exits non-zero.
		{name: "adapter non-zero exit", entry: &config.AdapterConfig{Framework: "bats", Mode: "sync", Command: "exit 3"}, wantErr: "exited with code 3", wantNotice: "GTMS accepted the change and ran " + enh231TC, started: true},
		// Invocation setup fails after the task and result contract exist:
		// the prompt template does not exist, so prompt assembly fails.
		{name: "prompt assembly failure", entry: &config.AdapterConfig{Framework: "bats", Mode: "sync", Command: "echo PASS", PromptTemplate: "gtms/no-such-template.md"}, wantErr: "assembling prompt", wantNotice: "GTMS could not start " + enh231TC, started: false},
		// Invocation setup fails before any task is created: the adapter
		// timeout cannot be parsed.
		{name: "invalid timeout setup failure", entry: &config.AdapterConfig{Framework: "bats", Mode: "sync", Command: "echo PASS", Timeout: "not-a-duration"}, wantErr: "invalid timeout", wantNotice: "GTMS could not start " + enh231TC, started: false},
		// The adapter exceeds its timeout. Whether the process launched before a
		// 100ms deadline is environment-dependent (on a slow Windows host it can
		// be killed during suspended-child setup), so this case asserts only that
		// acceptance happened. The deterministic started/did-not-start cases are
		// in TestENH222_StartedSignalMatrix, and the timeout/cancellation-after-
		// launch (started-then-failed -> Draft D) case is covered deterministically
		// by TestENH222_TimeoutAfterLaunch_StartedDraftD.
		{name: "adapter timeout", entry: &config.AdapterConfig{Framework: "bats", Mode: "sync", Command: "sleep 5", Timeout: "100ms"}, wantErr: "timed out", wantNotice: "accepted", started: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			skipIfShort(t)
			root := enh231SingleProject(t, "")
			newArt := enh231EditScript(t, root)
			cfg := enh231Config(t, "")
			cfg.Adapters["execute"]["bats-runner"] = tc.entry

			out, err := runSingle(t, root, cfg, enh231TC)
			require.Error(t, err, "the post-admission failure must surface: %s", out)
			assert.Contains(t, err.Error(), tc.wantErr)
			// Acceptance happened before the failure either way; the notice wording
			// reflects whether the process started.
			assert.Contains(t, out, tc.wantNotice, "acceptance must have happened before the failure")
			assert.NotContains(t, out, "wiring updated automatically", "ENH-222 retired the old notice")
			if !tc.started {
				assert.NotContains(t, out, "GTMS accepted the change and ran", "a pre-launch failure must not claim the test ran")
			}
			assert.Equal(t, newArt, wiringArtefactHash(t, root, enh231TC),
				"a failure after admission must not roll back the accepted hash")
		})
	}
}

// Bulk: an adapter failure and a cancelled context after admission both keep
// the accepted hash.
func TestENH231_Bulk_PostAdmissionFailuresKeepAcceptedHash(t *testing.T) {
	t.Run("adapter non-zero exit", func(t *testing.T) {
		skipIfShort(t)
		root := t.TempDir()
		enh231Spec(t, root, "drift", enh231TC)
		seedLegacyRecord(t, root, legacyRecord{TC: enh231TC, Framework: "bats", Adapter: "bats-runner"})
		newArt := enh231EditScript(t, root)
		cfg := enh231Config(t, "")
		cfg.Adapters["execute"]["bats-runner"] = &config.AdapterConfig{Framework: "bats", Mode: "sync", Command: "exit 3"}

		_, err := runBulk(t, root, "drift", cfg, false)
		require.Error(t, err, "bulk must report the errored run")
		assert.Equal(t, newArt, wiringArtefactHash(t, root, enh231TC))
	})

	t.Run("cancelled context", func(t *testing.T) {
		skipIfShort(t)
		root := t.TempDir()
		enh231Spec(t, root, "drift", enh231TC)
		seedLegacyRecord(t, root, legacyRecord{TC: enh231TC, Framework: "bats", Adapter: "bats-runner"})
		newArt := enh231EditScript(t, root)

		restore := withExecuteGlobals(t, root, enh231Config(t, ""))
		defer restore()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		cmd := &cobra.Command{Use: "execute"}
		cmd.SetContext(ctx)
		_ = captureStderr(t, func() {
			_ = runBulkExecute(cmd, root, "drift", "", "", "", "", false, false, false, false)
		})
		assert.Equal(t, newArt, wiringArtefactHash(t, root, enh231TC),
			"cancellation after admission must not roll back the accepted hash")
	})
}

// --- 4. Manual and agent bulk execution do not gain the active-task check ---

// The skip helpers exempt every IsManualFramework adapter from the active-task
// check, while a wired automated adapter is still caught.
func TestENH231_ActiveTaskSkip_ExemptsManualAndAgent(t *testing.T) {
	root := t.TempDir()
	_, err := task.Create(root, &task.TaskFile{
		ID: "task-active01", Type: "execute", Target: enh231TC,
		Adapter: "manual-execute", Status: "in-progress", Created: "2026-10-23T00:00:00Z",
	}, "active task fixture")
	require.NoError(t, err)

	exempt := []*adapter.ResolvedAdapter{
		{Command: "execute", Name: "manual-execute"},
		{Command: "execute", Name: "agent-execute"},
		{Command: "execute", Name: "my-manual", Config: &config.AdapterConfig{Framework: "manual", Mode: "sync", Command: "echo ok"}},
	}
	for _, r := range exempt {
		require.True(t, adapter.IsManualFramework(r), r.Name)
		assert.Empty(t, activeExecuteTaskSkip(root, enh231TC, r), "%s must not gain the active-task check (--force path)", r.Name)
		assert.Empty(t, shouldSkipExecute(root, enh231TC, r, "manual", nil), "%s must not gain the active-task check (normal path)", r.Name)
	}

	automated := &adapter.ResolvedAdapter{Command: "execute", Name: "bats-runner",
		Config: &config.AdapterConfig{Framework: "bats", Mode: "sync", Command: "echo ok"}}
	require.False(t, adapter.IsManualFramework(automated))
	assert.Equal(t, "active task exists", activeExecuteTaskSkip(root, enh231TC, automated))
}

// End to end through runBulkExecute: manual-execute and agent-execute bulk runs
// with an active execute task never print the active-task skip, with and
// without --force, and still attempt the invocation.
func TestENH231_Bulk_ManualAndAgentKeepEarlyBypass(t *testing.T) {
	for _, name := range []string{"manual-execute", "agent-execute"} {
		for _, force := range []bool{false, true} {
			name, force := name, force
			t.Run(name+map[bool]string{false: "", true: " --force"}[force], func(t *testing.T) {
				skipIfShort(t)
				root := t.TempDir()
				enh231Spec(t, root, "drift", enh231TC)
				_, err := task.Create(root, &task.TaskFile{
					ID: "task-active01", Type: "execute", Target: enh231TC,
					Adapter: name, Status: "in-progress", Created: "2026-10-23T00:00:00Z",
				}, "active task fixture")
				require.NoError(t, err)
				handoffsBefore := countHandoffs(t, root)

				restore := withExecuteGlobals(t, root, &config.Config{})
				defer restore()
				cmd := &cobra.Command{Use: "execute"}
				cmd.SetContext(context.Background())
				out := captureStderr(t, func() {
					_ = runBulkExecute(cmd, root, "drift", name, "", "", "", force, false, false, false)
				})
				assert.NotContains(t, out, "active task exists", "%s bulk must not gain the active-task check", name)
				assert.Greater(t, countHandoffs(t, root), handoffsBefore, "%s bulk must still attempt the invocation", name)
			})
		}
	}
}
