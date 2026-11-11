package adapter

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/aitestmanagement/gtms-cli/internal/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BUG-210 / REV-273: these tests drive the invalid-contract recovery branch of
// handleSyncResult itself -- the code REV-273 found broken -- not just the
// persistReservedMode3Handoff helper. A reserved Tier 2 execute adapter leaves a
// READABLE but invalid terminal contract carrying framework: fx and mode: async
// (so result.Validate fails and the recovery branch runs). The write failures are
// induced through the persistUpdate/persistReconstruct seams so the fail-closed
// branches are exercised deterministically on every platform (no read-only-file
// permission games, which differ on Windows).

// invalidReservedHandoff writes a readable, Validate-rejected terminal contract
// (status complete + an invalid result value) carrying framework: fx, mode: async.
func invalidReservedHandoff(taskID string) string {
	return "task: " + taskID + "\n" +
		"command: execute\n" +
		"target: tc-11111111\n" +
		"adapter: manual-execute-script\n" +
		"mode: async\n" +
		"framework: fx\n" +
		"status: complete\n" +
		"result: bogus\n" +
		"created: 2026-11-07T10:00:00Z\n"
}

// seedRecoveryCase creates the in-progress execute task and the invalid handoff on
// disk and returns the task file and the handoff path.
func seedRecoveryCase(t *testing.T, root, adapterName string) (*task.TaskFile, string) {
	t.Helper()
	tf := &task.TaskFile{
		ID:      "task-recov001",
		Type:    "execute",
		Target:  "tc-11111111",
		Adapter: adapterName,
		Status:  "in-progress",
		Created: "2026-11-07T10:00:00Z",
	}
	_, err := task.Create(root, tf, "")
	require.NoError(t, err)
	resultPath := result.ResultPath(root, tf.ID)
	require.NoError(t, os.WriteFile(resultPath, []byte(invalidReservedHandoff(tf.ID)), 0644))
	return tf, resultPath
}

func reservedExecuteResolved() *ResolvedAdapter {
	return &ResolvedAdapter{
		Command: "execute",
		Name:    "manual-execute-script",
		Config:  &config.AdapterConfig{Mode: "sync", Script: "gtms/adapters/manual-execute-script.sh"},
		Tier:    2,
		Mode:    "sync",
	}
}

func assertNoExecutionRecord(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "gtms", "execution"))
	if err != nil {
		return // directory absent -> no record
	}
	assert.Empty(t, entries, "the recovery path must not write a pipeline/execution record")
}

// Update fails, Reconstruct succeeds: the handoff is rewritten canonical
// (status error, framework manual, mode sync, full GTMS-held identity), the task
// is routed to error/, and no pipeline/execution record is written.
func TestHandleSyncResult_ReservedRecovery_UpdateFailsReconstructSucceeds(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)
	tf, resultPath := seedRecoveryCase(t, root, "manual-execute-script")

	origUpdate := persistUpdate
	persistUpdate = func(string, map[string]interface{}) error { return errors.New("injected update failure") }
	defer func() { persistUpdate = origUpdate }()

	cfg := &config.Config{Project: config.ProjectConfig{Name: "Test", Repo: "org/test"}}
	res, err := handleSyncResult(context.Background(), root, cfg, tf, resultPath,
		reservedExecuteResolved(), &InvocationResult{ExitCode: 0}, "", nil, "", "", nil)

	require.NoError(t, err, "a successful Reconstruct fallback is not a Go error")
	require.NotNil(t, res)
	assert.Equal(t, "error", res.Status)

	rc, readErr := result.Read(resultPath)
	require.NoError(t, readErr, "the reconstructed handoff must be readable")
	assert.Equal(t, "error", rc.Status)
	assert.Empty(t, rc.Result)
	assert.Equal(t, "manual", rc.Framework, "framework must be canonicalised to manual")
	assert.Equal(t, "sync", rc.Mode, "mode must be canonicalised to sync")
	assert.Equal(t, "execute", rc.Command)
	assert.Equal(t, "tc-11111111", rc.Target)
	assert.Equal(t, "manual-execute-script", rc.Adapter)
	assert.Equal(t, tf.ID, rc.Task)
	assert.Equal(t, tf.Created, rc.Created, "created comes from GTMS-held task state")

	errorTasks, err := task.List(root, "error")
	require.NoError(t, err)
	require.Len(t, errorTasks, 1, "the task must be routed to error/")
	inProgress, err := task.List(root, "in-progress")
	require.NoError(t, err)
	assert.Empty(t, inProgress, "the task must not be stranded in in-progress/")
	assertNoExecutionRecord(t, root)
}

// Both writes fail: a hard error naming both failures, the task routed to error/
// where possible, and no pipeline/execution record. The original contradictory
// handoff survives on disk (the accepted BUG-218 total-persistence boundary).
func TestHandleSyncResult_ReservedRecovery_BothWritesFail(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)
	tf, resultPath := seedRecoveryCase(t, root, "manual-execute-script")

	origUpdate, origReconstruct := persistUpdate, persistReconstruct
	persistUpdate = func(string, map[string]interface{}) error { return errors.New("injected update failure") }
	persistReconstruct = func(string, *result.ResultContract, result.HandoffState) error { return errors.New("injected reconstruct failure") }
	defer func() { persistUpdate, persistReconstruct = origUpdate, origReconstruct }()

	cfg := &config.Config{Project: config.ProjectConfig{Name: "Test", Repo: "org/test"}}
	res, err := handleSyncResult(context.Background(), root, cfg, tf, resultPath,
		reservedExecuteResolved(), &InvocationResult{ExitCode: 0}, "", nil, "", "", nil)

	require.Error(t, err, "a double persistence failure is a hard error")
	assert.Nil(t, res)
	assert.Contains(t, err.Error(), "injected update failure")
	assert.Contains(t, err.Error(), "injected reconstruct failure")

	errorTasks, err := task.List(root, "error")
	require.NoError(t, err)
	assert.Len(t, errorTasks, 1, "the task is routed to error/ where possible, even on double failure")
	assertNoExecutionRecord(t, root)

	// BUG-218 boundary: with both writes failed, the original contradictory file
	// survives and a reader could still join it to fx. This is the accepted limit.
	rc, readErr := result.Read(resultPath)
	require.NoError(t, readErr)
	assert.Equal(t, "fx", rc.Framework, "the original contradictory handoff survives (BUG-218 boundary)")
}

// Triple failure (REV-274 CLAUDE-001): Update, Reconstruct AND task.Move all fail.
// The returned error must carry both the persistence failure and the task-move
// failure (mirroring the valid-terminal branch), and no execution record is
// written. The move failure is induced deterministically on every platform WITHOUT
// a seam: the task file is never created on disk, so task.Move's os.Open of the
// source path fails ("opening task file for move").
func TestHandleSyncResult_ReservedRecovery_TripleFailure(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	// Build the task frontmatter and the invalid handoff, but DO NOT create the
	// task file on disk, so the later task.Move cannot find its source.
	tf := &task.TaskFile{
		ID:      "task-recov003",
		Type:    "execute",
		Target:  "tc-11111111",
		Adapter: "manual-execute-script",
		Status:  "in-progress",
		Created: "2026-11-07T10:00:00Z",
	}
	resultPath := result.ResultPath(root, tf.ID)
	require.NoError(t, os.WriteFile(resultPath, []byte(invalidReservedHandoff(tf.ID)), 0644))

	origUpdate, origReconstruct := persistUpdate, persistReconstruct
	persistUpdate = func(string, map[string]interface{}) error { return errors.New("injected update failure") }
	persistReconstruct = func(string, *result.ResultContract, result.HandoffState) error { return errors.New("injected reconstruct failure") }
	defer func() { persistUpdate, persistReconstruct = origUpdate, origReconstruct }()

	cfg := &config.Config{Project: config.ProjectConfig{Name: "Test", Repo: "org/test"}}
	res, err := handleSyncResult(context.Background(), root, cfg, tf, resultPath,
		reservedExecuteResolved(), &InvocationResult{ExitCode: 0}, "", nil, "", "", nil)

	require.Error(t, err, "a triple failure is a hard error")
	assert.Nil(t, res)
	assert.Contains(t, err.Error(), "injected update failure")
	assert.Contains(t, err.Error(), "injected reconstruct failure")
	assert.Contains(t, err.Error(), "could not move task to error",
		"the task-move failure must be reported alongside the persistence failure")
	assertNoExecutionRecord(t, root)
}

// Non-reserved adapters keep today's exact recovery behaviour: a failing Update is
// a hard error with NO Reconstruct attempt, the task goes to error/, and the adapter's
// framework/mode are not rewritten.
func TestHandleSyncResult_NonReservedRecovery_UpdateFailsNoReconstruct(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)
	tf, resultPath := seedRecoveryCase(t, root, "ordinary-tier2")

	reconstructCalled := false
	origUpdate, origReconstruct := persistUpdate, persistReconstruct
	persistUpdate = func(string, map[string]interface{}) error { return errors.New("injected update failure") }
	persistReconstruct = func(string, *result.ResultContract, result.HandoffState) error { reconstructCalled = true; return nil }
	defer func() { persistUpdate, persistReconstruct = origUpdate, origReconstruct }()

	resolved := &ResolvedAdapter{
		Command: "execute",
		Name:    "ordinary-tier2",
		Config:  &config.AdapterConfig{Mode: "sync", Script: "testdata/ordinary.sh", Framework: "bats"},
		Tier:    2,
		Mode:    "sync",
	}
	cfg := &config.Config{Project: config.ProjectConfig{Name: "Test", Repo: "org/test"}}
	res, err := handleSyncResult(context.Background(), root, cfg, tf, resultPath,
		resolved, &InvocationResult{ExitCode: 0}, "", nil, "", "", nil)

	require.Error(t, err, "a non-reserved recovery Update failure is a hard error")
	assert.Nil(t, res)
	assert.Contains(t, err.Error(), "recovery contract write failed")
	assert.False(t, reconstructCalled, "non-reserved recovery must not attempt Reconstruct")

	// BUG-209 section 9 (REV-278 CODEX-004): the task goes to error/, so it cannot be left
	// active to block the next run. It was left in-progress before that decision.
	inProgress, err := task.List(root, "in-progress")
	require.NoError(t, err)
	assert.Empty(t, inProgress, "no task may be left active")
	errorTasks, err := task.List(root, "error")
	require.NoError(t, err)
	assert.Len(t, errorTasks, 1)

	// Framework/mode not rewritten: the original (failed) write left the file as-is.
	rc, readErr := result.Read(resultPath)
	require.NoError(t, readErr)
	assert.Equal(t, "fx", rc.Framework, "non-reserved recovery must not rewrite framework")
	assert.Equal(t, "async", rc.Mode, "non-reserved recovery must not rewrite mode")
	assertNoExecutionRecord(t, root)
}
