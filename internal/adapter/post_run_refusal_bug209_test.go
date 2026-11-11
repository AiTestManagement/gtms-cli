package adapter

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/aitestmanagement/gtms-cli/internal/task"
)

func tier1Create(command string, failOn ...int) *ResolvedAdapter {
	return &ResolvedAdapter{
		Command: "create",
		Name:    "post-run-tier1",
		Config:  &config.AdapterConfig{Mode: "sync", Command: command, FailExitCodes: failOn},
		Tier:    1,
		Mode:    "sync",
	}
}

func postRunConfig() *config.Config {
	return &config.Config{Project: config.ProjectConfig{Name: "Test", Repo: "org/test"}}
}

// AC33 (REV-277 CLAUDE-001): a refusal after the adapter ran says so, on both terminal
// sites, and it still matches result.IsRefusal through the wrapper.
func TestPostRunRefusal_Tier1ReportsStarted(t *testing.T) {
	skipIfShort(t)
	for name, resolved := range map[string]*ResolvedAdapter{
		"exit 0":         tier1Create(`rm -f {result_file} && mkdir {result_file} && exit 0`),
		"fail exit code": tier1Create(`rm -f {result_file} && mkdir {result_file} && exit 3`, 3),
	} {
		t.Run(name, func(t *testing.T) {
			root := setupTestProject(t)
			res, err := InvokeWithRoot(context.Background(), root, postRunConfig(), resolved, "JIRA-456", CommandFlags{})
			require.Error(t, err)
			assert.True(t, result.IsRefusal(err), "the wrapper must keep the refusal reachable: %v", err)
			assert.True(t, Started(res, err), "the adapter ran, so this must not read as could not start")
			assert.Contains(t, err.Error(), "refused handoff .gtms/results/task-")
			assert.Contains(t, err.Error(), "is not a regular file")
		})
	}
}

// AC33 (REV-277 CLAUDE-001): recoverUnusableContract's refusal is covered too. A Tier 2
// adapter reaches it by replacing its own handoff.
func TestPostRunRefusal_Tier2RecoveryReportsStarted(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)
	script := filepath.Join(root, "swap.sh")
	require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\nrm -f \"$GTMS_RESULT_FILE\"\nmkdir \"$GTMS_RESULT_FILE\"\nexit 0\n"), 0o755))
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "post-run-tier2",
		Config:  &config.AdapterConfig{Mode: "sync", Script: script},
		Tier:    2,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, postRunConfig(), resolved, "JIRA-456", CommandFlags{})
	require.Error(t, err)
	assert.True(t, result.IsRefusal(err), "got %v", err)
	assert.True(t, Started(res, err), "the adapter ran, so this must not read as could not start")
	errored, lerr := task.List(root, "error")
	require.NoError(t, lerr)
	assert.Len(t, errored, 1, "the task must be filed under error/")
}

// A refusal before the adapter runs still reads as could not start.
func TestPreRunRefusal_ReportsNotStarted(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)
	results := filepath.Join(root, ".gtms", "results")
	require.NoError(t, os.RemoveAll(results))
	require.NoError(t, os.WriteFile(results, []byte("a file"), 0o644))

	res, err := InvokeWithRoot(context.Background(), root, postRunConfig(), tier1Create(`echo ran > ran.txt`), "JIRA-456", CommandFlags{})
	require.Error(t, err)
	assert.False(t, Started(res, err), "the adapter never ran")
	assert.NoFileExists(t, filepath.Join(root, "ran.txt"))
}

// REV-277 CLAUDE-004, as the coordinator asked to state: a Tier 1 terminal write that
// exhausts its retries does not record a pass, because its outcome never landed. The task
// goes to error/, the exit is non-zero and the adapter is reported as having run. The
// message is the concurrency error, with no unsafe-file remediation.
func TestTier1_TerminalConcurrencyErrorIsNotAPassAndNotARefusal(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)
	replaced := 0
	result.InjectPublishFailureForTest(func(stage string, data []byte) error {
		// Only the terminal write, which is the one recording a complete status.
		if stage != "recheck" || !bytes.Contains(data, []byte("status: complete")) {
			return nil
		}
		matches, err := filepath.Glob(filepath.Join(root, ".gtms", "results", "*.handoff.yaml"))
		require.NoError(t, err)
		require.Len(t, matches, 1)
		replaced++
		require.NoError(t, os.Remove(matches[0]))
		// Each replacement differs in size. A filesystem such as ext4 can reuse the inode
		// at once, and two writes in one clock tick share a modification time, so an
		// identical file could look unchanged.
		other := "status: in-progress\nsummary: another writer" + strings.Repeat("x", replaced) + "\n"
		require.NoError(t, os.WriteFile(matches[0], []byte(other), 0o644))
		return nil
	})
	t.Cleanup(func() { result.InjectPublishFailureForTest(nil) })

	res, err := InvokeWithRoot(context.Background(), root, postRunConfig(), tier1Create(`exit 0`), "JIRA-456", CommandFlags{})

	require.Error(t, err)
	assert.Equal(t, 4, replaced, "one initial attempt and three retries")
	assert.True(t, result.IsConcurrentUpdate(err), "got %v", err)
	assert.False(t, result.IsRefusal(err), "a healthy file must not be reported as unsafe")
	assert.NotContains(t, err.Error(), "remove it by hand")
	assert.True(t, Started(res, err), "the adapter ran")

	complete, lerr := task.List(root, "complete")
	require.NoError(t, lerr)
	assert.Empty(t, complete, "no pass may be recorded")
	errored, lerr := task.List(root, "error")
	require.NoError(t, lerr)
	assert.Len(t, errored, 1)
}

// REV-277 CODEX-004: when the task cannot be moved to error/, the command says so
// alongside the original failure. A regular file at the error directory blocks the move.
func TestFailBeforeInvocation_MoveFailureIsCombined(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)
	results := filepath.Join(root, ".gtms", "results")
	require.NoError(t, os.RemoveAll(results))
	require.NoError(t, os.WriteFile(results, []byte("a file"), 0o644))
	errDir := filepath.Join(root, "gtms", "tasks", "error")
	require.NoError(t, os.RemoveAll(errDir))
	require.NoError(t, os.WriteFile(errDir, []byte("a file"), 0o644))

	_, err := InvokeWithRoot(context.Background(), root, postRunConfig(), tier1Create(`exit 0`), "JIRA-456", CommandFlags{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not move task to error", "the routing failure must be returned")
	assert.Contains(t, err.Error(), ".gtms/results", "the original failure must stay")
}

// REV-277 CODEX-004: no routing failure can be discarded.
func TestSourceShape_NoDiscardedTaskMoveInInvoker(t *testing.T) {
	src, err := os.ReadFile("invoker.go")
	require.NoError(t, err)
	assert.NotContains(t, string(src), `_ = task.Move(`, "check the move to error/, and combine and return its failure")
}
