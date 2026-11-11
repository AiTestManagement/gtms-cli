package cli

// ENH-222 deterministic Go coverage:
//
//   - the structured "started" signal across the adapter boundary
//     (adapter.Started), which the default-mode acceptance notice uses to tell
//     Draft D ("accepted and ran") from Draft E ("accepted but could not
//     start"). Covered with crafted adapters, not race-based BATS.
//   - Draft F: a saved automation file that changes concurrently while GTMS is
//     preparing the run, driven by the execute test hook.
//
// Drafts H (script changed again after acceptance) and K (spec unavailable) are
// covered by rev232_execute_test.go.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/adapter"
	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// TestENH222_StartedSignalMatrix drives adapter.InvokeWithRoot directly and
// asserts adapter.Started for each pre-launch and post-launch outcome. The
// signal must come from the adapter boundary, never from task creation, a
// result contract, or error text (ENH-222 Owner Decision 2026-10-27 #4).
func TestENH222_StartedSignalMatrix(t *testing.T) {
	cases := []struct {
		name        string
		entry       *config.AdapterConfig
		wantStarted bool
	}{
		// Pre-launch failures: the process is never launched.
		{name: "prompt assembly failure -> did not start",
			entry:       &config.AdapterConfig{Framework: "bats", Mode: "sync", Command: "echo PASS", PromptTemplate: "gtms/no-such-template.md"},
			wantStarted: false},
		{name: "invalid timeout -> did not start",
			entry:       &config.AdapterConfig{Framework: "bats", Mode: "sync", Command: "echo PASS", Timeout: "not-a-duration"},
			wantStarted: false},
		{name: "cmd.Start failure (missing working dir) -> did not start",
			entry:       &config.AdapterConfig{Framework: "bats", Mode: "sync", Command: "echo PASS", WorkingDir: "missing/adapter-workdir"},
			wantStarted: false},
		// Post-launch outcomes: the process launched, even when it then failed.
		{name: "non-zero exit -> started, then failed",
			entry:       &config.AdapterConfig{Framework: "bats", Mode: "sync", Command: "exit 3"},
			wantStarted: true},
		{name: "successful dispatch -> started",
			entry:       &config.AdapterConfig{Framework: "bats", Mode: "sync", Command: "echo PASS"},
			wantStarted: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			skipIfShort(t)
			root := enh231SingleProject(t, "")
			artefact := enh231ArtefactPath(root)
			hash, err := pipeline.HashFile(artefact)
			require.NoError(t, err)

			owner, err := testcase.ResolveOwner(root, enh231TC)
			require.NoError(t, err)

			cfg := enh231Config(t, "")
			cfg.Adapters["execute"]["bats-runner"] = tc.entry
			resolved, rerr := adapter.Resolve(cfg, "execute", "bats-runner")
			require.NoError(t, rerr)

			flags := adapter.CommandFlags{
				Owner:        owner,
				Adapter:      "bats-runner",
				ArtefactFile: "test/acceptance/" + enh231TC + ".bats",
				Framework:    "bats",
				ArtefactHash: hash,
			}
			res, invokeErr := adapter.InvokeWithRoot(context.Background(), root, cfg, resolved, enh231TC, flags)
			assert.Equal(t, tc.wantStarted, adapter.Started(res, invokeErr),
				"adapter.Started mismatch (res=%v, err=%v)", res, invokeErr)
		})
	}
}

// TestENH222_CancellationAfterLaunch_StartedDraftD proves the CANCELLATION-
// after-launch row of Owner Decision 4 deterministically: a synchronisation
// point (a marker file the adapter writes as its first action) proves the
// adapter process launched BEFORE the run is cancelled -- it does not rely on a
// short wall-clock timeout racing process startup. This exercises the
// context.Canceled branch of the invoker. The timeout
// (context.DeadlineExceeded) row is covered by
// TestENH222_TimeoutAfterLaunch_StartedDraftD. In both cases the started signal
// must be true, so the default-mode acceptance uses the started form
// (Draft D "accepted the change and ran"), never Draft E "could not start",
// even though the run then fails.
func TestENH222_CancellationAfterLaunch_StartedDraftD(t *testing.T) {
	skipIfShort(t)
	root := enh231SingleProject(t, "")
	enh231EditScript(t, root) // script-only drift, accept mode

	// The adapter writes a marker (proving the process ran) then blocks. It runs
	// with cmd.Dir = project root, so the relative marker lands at root/.
	marker := filepath.Join(root, "started.marker")
	cfg := enh231Config(t, "")
	cfg.Adapters["execute"]["bats-runner"] = &config.AdapterConfig{
		Framework: "bats", Mode: "sync",
		Command: "printf started > started.marker; sleep 30",
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Cancel the run ONLY after the adapter process has demonstrably started
	// (its marker exists). A bounded safety loop cancels anyway, so a process
	// that never starts fails the marker assertion rather than hanging.
	go func() {
		for i := 0; i < 1000; i++ {
			if _, err := os.Stat(marker); err == nil {
				cancel()
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		cancel()
	}()

	restore := withExecuteGlobals(t, root, cfg)
	defer restore()
	cmd := newExecuteCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{enh231TC})
	cmd.SetContext(ctx)
	var err error
	out := captureStderr(t, func() { err = cmd.Execute() })

	require.Error(t, err, "a cancelled run must exit non-zero: %s", out)
	assert.Contains(t, err.Error(), "cancelled",
		"the started-then-cancelled run must surface the cancellation failure")
	_, statErr := os.Stat(marker)
	require.NoError(t, statErr, "the adapter process must have started (marker written)")
	// Draft D only prints when adapter.Started returned true, so its presence
	// end-to-end proves the started flag was true for a started-then-failed run.
	assert.Contains(t, out, "GTMS accepted the change and ran "+enh231TC,
		"a started-then-cancelled run must use the started form (Draft D)")
	assert.NotContains(t, out, "GTMS could not start",
		"a started-then-cancelled run must never use Draft E")
}

// TestENH222_AcceptFailureHint_NamesFrameworkConfirmCommand pins the complete
// two-line recovery hint on the accept-failure path. The pre-accept test hook
// makes the artefact unreadable after the drift gate passed but before the
// acceptance write, so link.AcceptScriptDrift fails with a non-concurrent error
// and execute takes the "Cannot accept script change" path deterministically.
func TestENH222_AcceptFailureHint_NamesFrameworkConfirmCommand(t *testing.T) {
	skipIfShort(t)
	root := enh231SingleProject(t, "")
	enh231EditScript(t, root) // script-only drift, accept mode
	artefact := enh231ArtefactPath(root)
	setExecuteTestHook(t, func(stage string) {
		if stage == "pre-accept" {
			// Replace the artefact file with a directory so the acceptance
			// write cannot hash it -- a deterministic non-concurrent failure.
			_ = os.Remove(artefact)
			_ = os.MkdirAll(artefact, 0o755)
		}
	})

	out, err := runSingle(t, root, enh231Config(t, ""), enh231TC)
	require.Error(t, err, "an accept failure must exit non-zero: %s", out)
	assert.Contains(t, out, "Cannot accept script change for "+enh231TC+"--bats")
	// The complete contiguous two-line recovery block, immediately following the
	// "Cannot accept script change ..." error line, with the command indented
	// exactly four spaces more than the explanatory line (relative layout).
	assertAcceptFailureBlock(t, out, enh231TC, "bats", "Cannot accept script change")
	assert.NotContains(t, out, "Refresh wiring explicitly", "the old hint wording is retired")
}

// assertAcceptFailureBlock asserts out contains the ENH-222 (2026-10-28)
// accept-failure recovery block as one contiguous block attached to THIS test
// case's error line: the error line (identified by tcID + errKind), IMMEDIATELY
// followed by the explanatory line, IMMEDIATELY followed by the
// `gtms link --refresh <tc> --framework <fw>` command indented exactly four
// spaces more than the explanatory line. Pinning the preceding error line is what
// enforces "never grouped" -- a grouped or deferred print (blocks emitted after
// all error lines / the summary) breaks the error-line -> explanatory adjacency.
// The owner pinned the RELATIVE layout; the absolute indent may differ between
// single and bulk output, so the expected command line is built from the
// explanatory line's own leading whitespace plus four spaces.
func assertAcceptFailureBlock(t *testing.T, out, tcID, framework, errKind string) {
	t.Helper()
	const explan = "After fixing the problem above, confirm this script with:"
	cmd := "gtms link --refresh " + tcID + " --framework " + framework
	lines := strings.Split(out, "\n")
	for i, raw := range lines {
		ln := strings.TrimRight(raw, "\r")
		if !strings.HasSuffix(ln, explan) || i+1 >= len(lines) {
			continue
		}
		next := strings.TrimRight(lines[i+1], "\r")
		if !strings.HasSuffix(next, cmd) {
			continue // an explanatory line for a different test case's block
		}
		// Adjacency: the explanatory line must IMMEDIATELY follow this test
		// case's own error line (proves the block is attached per test case and
		// not grouped/deferred after the summary).
		require.GreaterOrEqual(t, i, 1, "the explanatory line must be preceded by this test case's error line")
		prev := strings.TrimRight(lines[i-1], "\r")
		require.Contains(t, prev, tcID,
			"the explanatory line must immediately follow %s's own error line (not grouped); preceding line was %q", tcID, prev)
		require.Contains(t, prev, errKind,
			"the preceding line must be the accept-failure error line (not grouped); preceding line was %q", prev)
		lead := ln[:len(ln)-len(explan)]
		require.Empty(t, strings.TrimSpace(lead), "the explanatory line must have only leading whitespace before it")
		want := lead + "    " + cmd
		require.Equal(t, want, next,
			"the command must be indented exactly four spaces more than the explanatory line (relative layout)")
		return
	}
	t.Fatalf("accept-failure block for %s--%s not found in output:\n%s", tcID, framework, out)
}

// TestENH222_BulkAcceptFailureHint_PerTestCase pins that, in bulk, an accept
// failure that is not a concurrent change prints the two-line recovery block
// once per affected test case (never grouped), using that test case's framework,
// without changing counts, classification or exit code. Two affected test cases
// are used; each is driven deterministically by the pre-accept hook (its
// artefact is made unreadable just before its own acceptance write, so earlier
// availability and drift checks for that test case already passed).
func TestENH222_BulkAcceptFailureHint_PerTestCase(t *testing.T) {
	skipIfShort(t)
	root := t.TempDir()
	const tc1, tc2 = "tc-11111111", "tc-22222222"
	enh231Spec(t, root, "driftbulk", tc1)
	enh231Spec(t, root, "driftbulk", tc2)
	seedLegacyRecord(t, root, legacyRecord{TC: tc1, Framework: "bats", Adapter: "bats-runner"})
	seedLegacyRecord(t, root, legacyRecord{TC: tc2, Framework: "bats", Adapter: "bats-runner"})
	art1 := filepath.Join(root, "test/acceptance", tc1+".bats")
	art2 := filepath.Join(root, "test/acceptance", tc2+".bats")
	// Script-only drift for both (accept mode default).
	require.NoError(t, os.WriteFile(art1, []byte("# edited 1\n"), 0o644))
	require.NoError(t, os.WriteFile(art2, []byte("# edited 2\n"), 0o644))

	// The bulk loop processes owners in sorted id order (tc1 then tc2). At each
	// pre-accept, spoil that test case's artefact just-in-time so its acceptance
	// write fails with a non-concurrent error while the other's earlier checks
	// still see an intact artefact.
	spoilOrder := []string{art1, art2}
	calls := 0
	setExecuteTestHook(t, func(stage string) {
		if stage != "pre-accept" {
			return
		}
		if calls < len(spoilOrder) {
			a := spoilOrder[calls]
			_ = os.Remove(a)
			_ = os.MkdirAll(a, 0o755)
		}
		calls++
	})

	out, err := runBulk(t, root, "driftbulk", enh231Config(t, ""), false)
	require.Error(t, err, "bulk accept failures must exit non-zero: %s", out)

	// One recovery block per affected test case, never grouped.
	assert.Equal(t, 2, strings.Count(out, "After fixing the problem above, confirm this script with:"),
		"exactly one accept-failure block per affected test case")
	// Each block must sit immediately under its own test case's error line
	// (never grouped / deferred).
	assertAcceptFailureBlock(t, out, tc1, "bats", "error: accept script change")
	assertAcceptFailureBlock(t, out, tc2, "bats", "error: accept script change")

	// Counts, classification and exit behaviour unchanged versus today: both are
	// errored, nothing ran, and no grouped acceptance notice is printed. The
	// non-zero exit error encodes the bucket counts and the total.
	assert.Contains(t, err.Error(), "0 failed, 2 errored, 0 not run out of 2 test cases")
	assert.NotContains(t, out, "GTMS accepted the change and ran")
	assert.NotContains(t, out, "Refresh wiring explicitly", "the old hint wording is retired")
}

// TestENH222_RootScopeRefuseHint_NoEmptyTarget pins that a root-scope refuse-mode
// shared-script refusal offers `gtms status` as the inspection route and never a
// `gtms link --refresh` line with an empty target or any `--dry-run` line. It
// fails if the `g.folder != ""` guard in execute_guidance.go is removed (the
// unguarded path would format `gtms link --refresh  --dry-run` with an empty
// folder).
func TestENH222_RootScopeRefuseHint_NoEmptyTarget(t *testing.T) {
	bg := newBulkGuidance(t.TempDir(), "" /* root scope */, "" /* no --framework */, false)
	grp := &bulkRefuseGroup{
		scriptRel: "test/acceptance/shared-script.bats",
		tcs: []bulkRefuseTC{
			{tcID: "tc-11111111", framework: "bats"},
			{tcID: "tc-22222222", framework: "bats"},
		},
	}
	lines := bg.scriptChangeRefreshHint(grp)
	joined := strings.Join(lines, "\n")

	assert.Contains(t, joined, "gtms status", "root scope must offer gtms status as the inspection route")
	assert.Contains(t, joined, "gtms link --refresh tc-11111111 --framework bats", "each refused test case keeps its narrow confirm command")
	assert.NotContains(t, joined, "--dry-run", "root scope must not offer a folder --dry-run (there is no folder to name)")
	for _, ln := range lines {
		assert.NotContains(t, ln, "gtms link --refresh  ", "no refresh line may have an empty target")
		if strings.Contains(ln, "gtms link --refresh") {
			assert.Regexp(t, `gtms link --refresh \S`, ln, "every refresh line names a concrete target")
		}
	}
}

// markerTimeoutContext is a test-only context that models a deadline that fires
// AFTER the adapter process has demonstrably started. Its Done() channel closes
// once the marker file exists, and Err() then returns context.DeadlineExceeded.
// The invoker's context.WithTimeout child inherits the parent's error on
// propagation, so the invoker sees ctx.Err() == DeadlineExceeded and reports the
// run as timed out -- deterministically, without a wall-clock race. It covers
// the timeout row of Owner Decision 4 (timeout after launch -> started).
type markerTimeoutContext struct {
	done chan struct{}
}

func newMarkerTimeoutContext(marker string) *markerTimeoutContext {
	c := &markerTimeoutContext{done: make(chan struct{})}
	go func() {
		for i := 0; i < 1000; i++ {
			if _, err := os.Stat(marker); err == nil {
				close(c.done)
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		close(c.done) // safety: a process that never starts fails the assertions
	}()
	return c
}

func (c *markerTimeoutContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *markerTimeoutContext) Done() <-chan struct{}       { return c.done }
func (c *markerTimeoutContext) Err() error {
	select {
	case <-c.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}
func (c *markerTimeoutContext) Value(interface{}) interface{} { return nil }

// TestENH222_TimeoutAfterLaunch_StartedDraftD proves the TIMEOUT-after-launch row
// of Owner Decision 4 deterministically. A test-only context whose Err() returns
// context.DeadlineExceeded once the adapter's marker file exists drives the
// invoker's timeout branch (not the cancellation branch) without a wall-clock
// race. The started signal must be true, so the run surfaces the "timed out"
// failure AND the started form (Draft D "accepted the change and ran"), never
// Draft E "could not start".
func TestENH222_TimeoutAfterLaunch_StartedDraftD(t *testing.T) {
	skipIfShort(t)
	root := enh231SingleProject(t, "")
	enh231EditScript(t, root) // script-only drift, accept mode

	marker := filepath.Join(root, "started.marker")
	cfg := enh231Config(t, "")
	cfg.Adapters["execute"]["bats-runner"] = &config.AdapterConfig{
		Framework: "bats", Mode: "sync",
		Command: "printf started > started.marker; sleep 30",
	}

	restore := withExecuteGlobals(t, root, cfg)
	defer restore()
	cmd := newExecuteCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{enh231TC})
	cmd.SetContext(newMarkerTimeoutContext(marker))
	var err error
	out := captureStderr(t, func() { err = cmd.Execute() })

	require.Error(t, err, "a timed-out run must exit non-zero: %s", out)
	assert.Contains(t, err.Error(), "timed out",
		"the started-then-failed run must surface the timeout failure")
	_, statErr := os.Stat(marker)
	require.NoError(t, statErr, "the adapter process must have started (marker written)")
	assert.Contains(t, out, "GTMS accepted the change and ran "+enh231TC,
		"a started-then-timed-out run must use the started form (Draft D)")
	assert.NotContains(t, out, "GTMS could not start",
		"a started-then-timed-out run must never use Draft E")
}

// TestENH222_ConcurrentWiringChange_DraftF drives the bootstrap concurrent-change
// path: the selected (pending) wiring file is rewritten while GTMS is preparing
// the run, so recheckBootstrapWiring refuses with ErrConcurrentWiringChange. The
// single path must print Draft F -- naming the saved automation file and saying
// the newer version was kept -- and must not overwrite it.
func TestENH222_ConcurrentWiringChange_DraftF(t *testing.T) {
	skipIfShort(t)
	root := enh231SingleProject(t, "")
	// Make the wiring pending so bootstrapPendingWiring runs and its
	// post-bootstrap-hash hook fires.
	wpath, err := wiring.Path(root, enh231TC, "bats")
	require.NoError(t, err)
	rec, err := wiring.Read(wpath)
	require.NoError(t, err)
	rec.ArtefactHash = wiring.PendingArtefactHash
	_, err = wiring.Write(root, rec)
	require.NoError(t, err)

	before, err := os.ReadFile(wpath)
	require.NoError(t, err)

	// Concurrent writer: change a field of the selected wiring file between the
	// bootstrap hash and its re-read, so the re-read no longer matches.
	mutated := false
	setExecuteTestHook(t, func(stage string) {
		if stage == "post-bootstrap-hash" && !mutated {
			mutated = true
			onDisk, rerr := wiring.Read(wpath)
			if rerr != nil {
				return
			}
			onDisk.Adapter = "other-runner"
			_, _ = wiring.Write(root, onDisk)
		}
	})

	out, err := runSingle(t, root, enh231Config(t, ""), enh231TC)
	require.Error(t, err, "a concurrent wiring change must refuse: %s", out)
	assert.Contains(t, out, enh231TC+" was not started because its saved automation file changed while GTMS was preparing the run")
	assert.Contains(t, out, "GTMS kept the newer version and did not overwrite it. Retry the command.")
	assert.Contains(t, out, filepath.ToSlash("gtms/automation/wiring/"+enh231TC+"--bats.wiring.yaml"))
	assert.Equal(t, 0, countHandoffs(t, root), "no adapter invocation")

	// The newer version (the concurrent writer's) survives; GTMS did not
	// overwrite it back to the pending bytes.
	after, err := os.ReadFile(wpath)
	require.NoError(t, err)
	assert.NotEqual(t, string(before), string(after), "the concurrent writer's version must survive")
	assert.Contains(t, string(after), "other-runner")
}
