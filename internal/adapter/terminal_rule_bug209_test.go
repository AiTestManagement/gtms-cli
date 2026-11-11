package adapter

// BUG-209 section 9, the shared terminal persistence rule (AC34).

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/aitestmanagement/gtms-cli/internal/task"
)

// injectWriters replaces the writers. reconstructCalls counts every reconstruct attempt.
func injectWriters(t *testing.T, updateErr error) *int {
	t.Helper()
	reconstructCalls := 0
	origUpdate, origReconstruct := persistUpdate, persistReconstruct
	persistUpdate = func(string, map[string]interface{}) error { return updateErr }
	persistReconstruct = func(string, *result.ResultContract, result.HandoffState) error {
		reconstructCalls++
		return nil
	}
	t.Cleanup(func() { persistUpdate, persistReconstruct = origUpdate, origReconstruct })
	return &reconstructCalls
}

func blockingErrors() map[string]error {
	return map[string]error{
		"refusal":     &result.RefusedHandoffError{Rel: ".gtms/results/task-a.handoff.yaml", Reason: "is a link"},
		"dir refusal": &result.RefusedDirError{Rel: ".gtms/results", Reason: "resolves outside the project"},
		"concurrency": &result.ConcurrentUpdateError{Rel: ".gtms/results/task-a.handoff.yaml", Retries: 3},
	}
}

func canonicalTestContract() *result.ResultContract {
	return &result.ResultContract{Task: "task-a", Command: "execute", Target: "tc-11111111", Adapter: "a", Mode: "sync", Status: "error"}
}

// Rule: Reconstruct is never called after a refusal or a concurrency error.
func TestPersistTerminal_NeverReconstructsAfterRefusalOrConcurrency(t *testing.T) {
	for name, cause := range blockingErrors() {
		t.Run(name, func(t *testing.T) {
			calls := injectWriters(t, cause)
			err := PersistTerminal(filepath.Join(t.TempDir(), "task-a.handoff.yaml"), map[string]interface{}{"summary": "x"}, canonicalTestContract())
			assert.ErrorIs(t, err, cause)
			assert.Zero(t, *calls, "a reconstruct could overwrite the newer writer")
		})
	}
}

// Rule: Reconstruct stays available for an ordinary failure with a canonical contract.
func TestPersistTerminal_OrdinaryFailureReconstructsOnlyWithCanonical(t *testing.T) {
	path := filepath.Join(t.TempDir(), "task-a.handoff.yaml")
	calls := injectWriters(t, errors.New("disk full"))
	require.NoError(t, PersistTerminal(path, map[string]interface{}{"summary": "x"}, canonicalTestContract()))
	assert.Equal(t, 1, *calls)

	err := PersistTerminal(path, map[string]interface{}{"summary": "x"}, nil)
	require.Error(t, err, "with no canonical contract the outcome did not land")
	assert.Equal(t, 1, *calls, "no reconstruct without a canonical contract")
}

// Rule: the reconstruct is against the state captured before the write, so a real file
// replaced after that capture is refused and kept.
func TestPersistTerminal_ReconstructRefusedIfTheFileChangedAfterTheCapture(t *testing.T) {
	root := setupTestProject(t)
	path := result.ResultPath(root, "task-a")
	require.NoError(t, os.WriteFile(path, []byte("not: [valid\n"), 0o644))
	const newer = "task: task-a\nstatus: in-progress\nsummary: newer writer\n"
	orig := persistUpdate
	persistUpdate = func(string, map[string]interface{}) error {
		require.NoError(t, os.Remove(path))
		require.NoError(t, os.WriteFile(path, []byte(newer), 0o644))
		return errors.New("ordinary failure")
	}
	t.Cleanup(func() { persistUpdate = orig })

	err := PersistTerminal(path, map[string]interface{}{"summary": "x"}, canonicalTestContract())
	require.Error(t, err)
	got, rerr := os.ReadFile(path)
	require.NoError(t, rerr)
	assert.Equal(t, newer, string(got), "the newer writer's file must survive")
}

// tier2Run runs a Tier 2 adapter whose script is body, for a given command.
func tier2Run(t *testing.T, root, command, name, body string) (*InvokeResult, error) {
	t.Helper()
	script := filepath.Join(root, name+".sh")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\n"+body+"\n"), 0o755))
	resolved := &ResolvedAdapter{
		Command: command,
		Name:    name,
		Config:  &config.AdapterConfig{Mode: "sync", Script: script, Framework: "fx"},
		Tier:    2,
		Mode:    "sync",
	}
	return InvokeWithRoot(context.Background(), root, postRunConfig(), resolved, "JIRA-456", CommandFlags{})
}

// terminalContractScript writes a valid terminal contract and exits 0. Nothing in it
// sets the GTMS-owned target, so GTMS must write a restore.
const terminalContractScript = `printf 'status: complete\nresult: pass\nsummary: "1 passed"\n' > "$GTMS_RESULT_FILE"
exit 0`

// No record and no complete/ for a run: every task under error/, none active.
func assertErrorRouted(t *testing.T, root string) {
	t.Helper()
	for _, state := range []string{"complete", "pending", "in-progress"} {
		tasks, err := task.List(root, state)
		require.NoError(t, err)
		assert.Empty(t, tasks, "no task may be in %s/", state)
	}
	errored, err := task.List(root, "error")
	require.NoError(t, err)
	assert.Len(t, errored, 1, "the task must be under error/")
}

// Rule: Tier 2 reports no pass and moves nothing to complete/ unless its restore landed.
func TestTier2_RestoreFailureIsNotAPass(t *testing.T) {
	skipIfShort(t)
	cases := map[string]error{
		"refusal":     &result.RefusedHandoffError{Rel: ".gtms/results/task-a.handoff.yaml", Reason: "is a link"},
		"concurrency": &result.ConcurrentUpdateError{Rel: ".gtms/results/task-a.handoff.yaml", Retries: 3},
	}
	for name, cause := range cases {
		t.Run(name, func(t *testing.T) {
			root := setupTestProject(t)
			calls := injectWriters(t, cause)
			res, err := tier2Run(t, root, "create", "restore", terminalContractScript)
			require.Error(t, err)
			assert.ErrorIs(t, err, cause)
			assert.True(t, Started(res, err), "the adapter ran")
			assert.Zero(t, *calls, "no reconstruct")
			assertErrorRouted(t, root)
		})
	}
}

// Rule: an ordinary failure with no surviving write also fails the run.
func TestTier2_RestoreOrdinaryDoubleFailureIsNotAPass(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)
	origUpdate, origReconstruct := persistUpdate, persistReconstruct
	persistUpdate = func(string, map[string]interface{}) error { return errors.New("injected update failure") }
	persistReconstruct = func(string, *result.ResultContract, result.HandoffState) error {
		return errors.New("injected reconstruct failure")
	}
	t.Cleanup(func() { persistUpdate, persistReconstruct = origUpdate, origReconstruct })

	res, err := tier2Run(t, root, "create", "restore", terminalContractScript)
	require.Error(t, err)
	assert.True(t, Started(res, err))
	assert.Contains(t, err.Error(), "contract restore write failed")
	assertErrorRouted(t, root)
}

// Rule: stranded-task prevention on the invalid-contract recovery. The unsafe target
// leaves no active task.
func TestTier2_InvalidContractRecoveryFailureLeavesNoActiveTask(t *testing.T) {
	skipIfShort(t)
	for name, cause := range blockingErrors() {
		t.Run(name, func(t *testing.T) {
			root := setupTestProject(t)
			calls := injectWriters(t, cause)
			_, err := tier2Run(t, root, "create", "invalid", `printf 'status: complete\n' > "$GTMS_RESULT_FILE"
exit 0`)
			require.Error(t, err)
			assert.ErrorIs(t, err, cause)
			assert.Zero(t, *calls, "no reconstruct")
			assertErrorRouted(t, root)
		})
	}
}

// Rule: a refused read goes to error/ without a reconstruct being attempted.
func TestTier2_RefusedReadDoesNotReconstruct(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)
	calls := 0
	orig := persistReconstruct
	persistReconstruct = func(string, *result.ResultContract, result.HandoffState) error { calls++; return nil }
	t.Cleanup(func() { persistReconstruct = orig })

	res, err := tier2Run(t, root, "create", "swap", `rm -f "$GTMS_RESULT_FILE"
mkdir "$GTMS_RESULT_FILE"
exit 0`)
	require.Error(t, err)
	assert.True(t, result.IsRefusal(err), "got %v", err)
	assert.True(t, Started(res, err))
	assert.Zero(t, calls, "Reconstruct must not be called after a safety refusal")
	assertErrorRouted(t, root)
}

// Rule: an ordinary unusable contract is still recovered (BUG-191 stays intact).
func TestTier2_MalformedContractIsStillRecovered(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)
	res, err := tier2Run(t, root, "create", "malformed", `printf 'not: [valid\n' > "$GTMS_RESULT_FILE"
exit 0`)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "error", res.Status)
	rc, rerr := result.Read(result.ResultPath(root, res.TaskID))
	require.NoError(t, rerr, "the reconstructed handoff must be readable")
	assert.Equal(t, "error", rc.Status)
}

// Rule: the reserved-adapter path keeps framework: manual on an ordinary failure, and does
// not reconstruct after a refusal or a concurrency error.
func TestReservedMode3_RefusalAndConcurrencyDoNotReconstruct(t *testing.T) {
	for name, cause := range blockingErrors() {
		t.Run(name, func(t *testing.T) {
			calls := injectWriters(t, cause)
			err := persistReservedMode3Handoff(filepath.Join(t.TempDir(), "task-a.handoff.yaml"),
				map[string]interface{}{"framework": "manual"}, canonicalTestContract())
			assert.ErrorIs(t, err, cause)
			assert.Zero(t, *calls)
		})
	}
}

func TestReservedMode3_OrdinaryFailureStillReconstructs(t *testing.T) {
	calls := injectWriters(t, errors.New("corrupt"))
	require.NoError(t, persistReservedMode3Handoff(filepath.Join(t.TempDir(), "task-a.handoff.yaml"),
		map[string]interface{}{"framework": "manual"}, canonicalTestContract()))
	assert.Equal(t, 1, *calls)
}

// Rule: the real start state, for every post-invocation failure, with no flags involved.
func TestPostRunSignal_EveryFailureCarriesTheRealState(t *testing.T) {
	plain := errors.New("moving task file: disk full")
	err := withPostRunSignal(&InvocationResult{Started: true}, plain)
	assert.True(t, Started(nil, err))
	assert.ErrorIs(t, err, plain, "the cause stays reachable")
	assert.Equal(t, plain.Error(), err.Error(), "the message is unchanged")

	assert.False(t, Started(nil, withPostRunSignal(&InvocationResult{Started: false}, plain)))
	assert.NoError(t, withPostRunSignal(&InvocationResult{Started: true}, nil))
}

// Exit-path inventory: no handoff write outside the shared rule. The three direct writes
// left are pre-invocation, where no outcome exists yet and a failure stops the adapter.
func TestSourceShape_TerminalWritesUseTheSharedRule(t *testing.T) {
	src, err := os.ReadFile("invoker.go")
	require.NoError(t, err)
	code := string(src)

	var direct []string
	lines := strings.Split(strings.ReplaceAll(code, "\r\n", "\n"), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		if regexp.MustCompile(`result\.Update\(|persistUpdate\(`).MatchString(line) {
			// The key the write sets identifies which one it is.
			direct = append(direct, strings.Join(lines[i:min(i+2, len(lines))], " "))
		}
		assert.NotRegexp(t, `result\.Reconstruct\(`, line, "use the shared rule: invoker.go:%d", i+1)
	}
	require.Len(t, direct, 3, "direct writes in invoker.go: %q", direct)
	for i, want := range []string{`"framework": ac.ResultFramework`, `"status":    "error"`, `"status": "in-progress"`} {
		assert.Contains(t, direct[i], want, "pre-invocation write %d", i)
	}

	status, err := os.ReadFile("../cli/status_common.go")
	require.NoError(t, err)
	assert.NotContains(t, string(status), "result.Update(", "async finalisation must use adapter.PersistTerminal")
	assert.NotContains(t, string(status), "result.Reconstruct(")
}
