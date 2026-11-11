package cli

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/output"
	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/aitestmanagement/gtms-cli/internal/task"
)

// BUG-209 section 8 (CLAUDE-006, CLAUDE-007): every async status command reports a
// refused handoff and exits non-zero, in its list and detail forms, for a task in any
// state, and one refusal neither hides nor stops polling the other tasks. A directory
// at the handoff path is the unsafe leaf because it needs no privilege.

const statusRefusalLine = "refused handoff .gtms/results/task-bbbbbbbb.handoff.yaml: is not a regular file"

type statusForm struct {
	kind   string
	list   func(context.Context, io.Writer, string, *config.Config) error
	detail func(context.Context, io.Writer, string, *config.Config, string) error
}

func statusForms() []statusForm {
	return []statusForm{
		{"create", runCreateStatusList, runCreateStatusDetail},
		{"automate", runAutomateStatusList, runAutomateStatusDetail},
		{"execute", runExecuteStatusList, runExecuteStatusDetail},
	}
}

func seedStatusTask(t *testing.T, root, kind, id, target, status string) {
	t.Helper()
	tf := &task.TaskFile{ID: id, Type: kind, Target: target, Adapter: "asyncfx", Status: status}
	_, err := task.Create(root, tf, "")
	require.NoError(t, err)
	_, err = result.Create(root, &result.ResultContract{
		Task: id, Command: kind, Target: target, Adapter: "asyncfx", Mode: "async", Status: "in-progress",
	})
	require.NoError(t, err)
}

func makeHandoffADirectory(t *testing.T, root, id string) {
	t.Helper()
	path := result.ResultPath(root, id)
	require.NoError(t, os.Remove(path))
	require.NoError(t, os.Mkdir(path, 0o755))
}

func asyncStatusConfig(kind, statusScript string) *config.Config {
	return &config.Config{
		Project: config.ProjectConfig{Name: "t", Repo: "r"},
		Adapters: map[string]map[string]*config.AdapterConfig{
			kind: {"asyncfx": {Mode: "async", Script: "fx.sh", StatusScript: statusScript}},
		},
	}
}

func TestAsyncStatusList_RefusalReportedAfterEveryTaskIsListed(t *testing.T) {
	skipIfShort(t)
	for _, form := range statusForms() {
		t.Run(form.kind, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "gtms.config", "project:\n  name: t\n  repo: r\n")
			writeTestFile(t, root, "status.sh", "#!/bin/sh\nexit 0\n")
			seedStatusTask(t, root, form.kind, "task-aaaaaaaa", "tc-aaaaaaaa", "in-progress")
			seedStatusTask(t, root, form.kind, "task-bbbbbbbb", "tc-bbbbbbbb", "in-progress")
			seedStatusTask(t, root, form.kind, "task-cccccccc", "tc-cccccccc", "in-progress")
			makeHandoffADirectory(t, root, "task-bbbbbbbb")

			var out strings.Builder
			var err error
			stderr := captureStderr(t, func() {
				err = form.list(context.Background(), &out, root, asyncStatusConfig(form.kind, "status.sh"))
			})

			require.Error(t, err, "the aggregate exit must be non-zero")
			assert.True(t, output.IsDisplayed(err), "the refusal has already been shown")
			assert.Equal(t, 1, strings.Count(stderr, statusRefusalLine), "reported once: %s", stderr)
			for _, target := range []string{"tc-aaaaaaaa", "tc-bbbbbbbb", "tc-cccccccc"} {
				assert.Contains(t, out.String(), target, "every task must still be listed")
			}
		})
	}
}

func TestAsyncStatusDetail_RefusalInAnyTaskState(t *testing.T) {
	skipIfShort(t)
	for _, form := range statusForms() {
		for _, state := range []string{"in-progress", "complete"} {
			t.Run(form.kind+"/"+state, func(t *testing.T) {
				root := t.TempDir()
				writeTestFile(t, root, "gtms.config", "project:\n  name: t\n  repo: r\n")
				writeTestFile(t, root, "status.sh", "#!/bin/sh\nexit 0\n")
				seedStatusTask(t, root, form.kind, "task-bbbbbbbb", "tc-bbbbbbbb", state)
				makeHandoffADirectory(t, root, "task-bbbbbbbb")

				var out strings.Builder
				var err error
				stderr := captureStderr(t, func() {
					err = form.detail(context.Background(), &out, root, asyncStatusConfig(form.kind, "status.sh"), "tc-bbbbbbbb")
				})

				require.Error(t, err, "the exit must be non-zero")
				assert.Equal(t, 1, strings.Count(stderr, statusRefusalLine), "reported once: %s", stderr)
				assert.Contains(t, out.String(), "task-bbbbbbbb", "the safe detail must still be shown")
			})
		}
	}
}

// CLAUDE-006: a status script failing does not hide a refused handoff.
func TestCheckAsyncStatus_FailingScriptStillReportsRefusal(t *testing.T) {
	skipIfShort(t)
	root := t.TempDir()
	writeTestFile(t, root, "gtms.config", "project:\n  name: t\n  repo: r\n")
	writeTestFile(t, root, "status.sh", "#!/bin/sh\nrm -rf \"$GTMS_RESULT_FILE\"\nmkdir \"$GTMS_RESULT_FILE\"\nexit 1\n")
	seedStatusTask(t, root, "execute", "task-bbbbbbbb", "tc-bbbbbbbb", "in-progress")
	tf, err := task.FindByTarget(root, "execute", "tc-bbbbbbbb", task.ValidStatuses...)
	require.NoError(t, err)
	require.NotNil(t, tf)

	cerr := checkAsyncStatus(context.Background(), root, asyncStatusConfig("execute", "status.sh"), tf, "execute")
	require.Error(t, cerr, "the refusal must be returned")
	assert.True(t, result.IsRefusal(cerr), "got %v", cerr)
}

// Section 8: the status script is not run against an unsafe leaf, because it is handed
// that path and would write through it.
func TestCheckAsyncStatus_ScriptNotRunOnRefusedHandoff(t *testing.T) {
	skipIfShort(t)
	root := t.TempDir()
	writeTestFile(t, root, "gtms.config", "project:\n  name: t\n  repo: r\n")
	writeTestFile(t, root, "status.sh", "#!/bin/sh\necho ran > \"$GTMS_PROJECT_ROOT/script-ran\"\nexit 0\n")
	seedStatusTask(t, root, "execute", "task-bbbbbbbb", "tc-bbbbbbbb", "in-progress")
	makeHandoffADirectory(t, root, "task-bbbbbbbb")
	tf, err := task.FindByTarget(root, "execute", "tc-bbbbbbbb", task.ValidStatuses...)
	require.NoError(t, err)
	require.NotNil(t, tf)

	cerr := checkAsyncStatus(context.Background(), root, asyncStatusConfig("execute", "status.sh"), tf, "execute")
	require.Error(t, cerr)
	assert.NoFileExists(t, root+"/script-ran", "the status script must not run")
}
