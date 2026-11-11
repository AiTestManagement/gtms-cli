package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/aitestmanagement/gtms-cli/internal/task"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
)

// AC33 (REV-277 CLAUDE-003): a refusal found during a bulk run prints its complete path,
// reason and remediation, once, beside the clipped per-case line.
func TestBUG209_BulkRefusalDuringRunPrintsItInFull(t *testing.T) {
	skipIfShort(t)
	root := t.TempDir()
	enh231Spec(t, root, "bulk", "tc-11111111")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-11111111", Framework: "bats", Adapter: "bats-runner", Result: "fail"})
	cfg := batsExecuteConfig()
	cfg.Adapters["execute"]["bats-runner"].Command = `rm -f {result_file} && mkdir {result_file}`

	out, err := runBulk(t, root, "bulk", cfg, true)

	require.Error(t, err, "the run errored, so the exit is non-zero")
	assert.Equal(t, 1, strings.Count(out, ": is not a regular file"), "the full reason, once: %s", out)
	assert.Contains(t, out, "refused handoff .gtms/results/task-")
	assert.Equal(t, 1, strings.Count(out, "remove it by hand, or replace it with an ordinary single-link regular file under .gtms/results, and retry"), out)
}

func TestPrintBulkRefusal_OncePerPathAndOnlyForRefusals(t *testing.T) {
	refusal := &result.RefusedHandoffError{Rel: ".gtms/results/task-a.handoff.yaml", Reason: "is a link"}
	reported := map[string]bool{}
	stderr := captureStderr(t, func() {
		printBulkRefusal(refusal, reported)
		printBulkRefusal(refusal, reported)
		printBulkRefusal(errors.New("not a refusal"), reported)
	})
	assert.Equal(t, 1, strings.Count(stderr, "refused handoff .gtms/results/task-a.handoff.yaml: is a link"), stderr)
	assert.NotContains(t, stderr, "not a refusal")
}

// REV-277 CODEX-003: a list inspects every listed task's handoff, in any state and
// whether or not it can be polled. A refused entry belonging to no listed task is not
// this list's to report.
func TestAsyncStatusList_ReportsFinishedAndUnpollableTasks(t *testing.T) {
	skipIfShort(t)
	for _, form := range statusForms() {
		for _, state := range []string{"complete", "error", "in-progress"} {
			t.Run(form.kind+"/"+state, func(t *testing.T) {
				root := t.TempDir()
				writeTestFile(t, root, "gtms.config", "project:\n  name: t\n  repo: r\n")
				seedStatusTask(t, root, form.kind, "task-aaaaaaaa", "tc-aaaaaaaa", "complete")
				seedStatusTask(t, root, form.kind, "task-bbbbbbbb", "tc-bbbbbbbb", state)
				makeHandoffADirectory(t, root, "task-bbbbbbbb")

				// No adapter or status script is configured, so nothing can be polled.
				cfg := &config.Config{Project: config.ProjectConfig{Name: "t", Repo: "r"}}
				var out bytes.Buffer
				var err error
				stderr := captureStderr(t, func() { err = form.list(context.Background(), &out, root, cfg) })

				require.Error(t, err, "the exit must be non-zero")
				assert.Equal(t, 1, strings.Count(stderr, statusRefusalLine), stderr)
				assert.Contains(t, out.String(), "tc-aaaaaaaa", "the other task must still be listed")
				assert.Contains(t, out.String(), "tc-bbbbbbbb")
			})
		}
	}
}

func TestListedHandoffRefusals_OnlyListedTasks(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "gtms.config", "project:\n  name: t\n  repo: r\n")
	path := result.ResultPath(root, "task-99999999")
	require.NoError(t, os.MkdirAll(path, 0o755))

	refusals := listedHandoffRefusals(root, []*task.TaskFile{{ID: "task-aaaaaaaa"}})
	assert.Empty(t, refusals, "that entry belongs to no listed task")
	assert.Len(t, listedHandoffRefusals(root, []*task.TaskFile{{ID: "task-99999999"}}), 1)
}

// REV-277 CODEX-006: gtms status with its triage decoration scans once and reports each
// refusal once, in the JSON and the detail forms.
func TestBUG209_StatusCommandScansOnceAndReportsOnce(t *testing.T) {
	skipIfShort(t)
	root := t.TempDir()
	enh231Spec(t, root, "scan", "tc-11111111")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-11111111", Framework: "bats", Adapter: "bats-runner", Result: "pass"})
	require.NoError(t, os.MkdirAll(result.ResultPath(root, "task-99999999"), 0o755))
	const line = "refused handoff .gtms/results/task-99999999.handoff.yaml: is not a regular file"
	count := 0
	result.CountScansForTest(&count)
	t.Cleanup(func() { result.CountScansForTest(nil) })

	var err error
	stderr := captureStderr(t, func() {
		err = runStatusOverview(io.Discard, root, nil, true, "", false, false, "", map[string]string{})
	})
	require.Error(t, err)
	assert.Equal(t, 1, count, "the overview must scan once")
	assert.Equal(t, 1, strings.Count(stderr, line), stderr)

	snap, serr := testcase.NewSnapshot(root)
	require.NoError(t, serr)
	count = 0
	stderr = captureStderr(t, func() {
		err = runStatusDetail(io.Discard, snap, root, "tc-11111111", true, "", false, false, "", map[string]string{})
	})
	require.Error(t, err)
	assert.Equal(t, 1, count, "the detail must scan once")
	assert.Equal(t, 1, strings.Count(stderr, line), stderr)
}
