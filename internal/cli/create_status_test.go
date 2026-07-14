package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/aitestmanagement/gtms-cli/internal/task"
)

// setupCreateStatusFixture creates a minimal GTMS project with a completed create task,
// a result contract pointing to TC files, and the TC files themselves.
func setupCreateStatusFixture(t *testing.T, taskID, target string, tcFiles []struct{ id, title string }, warnings []string) string {
	t.Helper()
	root := t.TempDir()

	// Create directories
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/tasks", "complete"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".gtms", "results"), 0755))

	tcDir := filepath.Join(root, "gtms/test/cases", target)
	require.NoError(t, os.MkdirAll(tcDir, 0755))

	// Create TC fixture files and build artefact path list
	var artefactPaths []string
	for _, tc := range tcFiles {
		filename := fmt.Sprintf("%s-slug.md", tc.id)
		content := fmt.Sprintf("---\ntest_case_id: %s\ntitle: %s\n---\n\n# %s\n", tc.id, tc.title, tc.title)
		require.NoError(t, os.WriteFile(filepath.Join(tcDir, filename), []byte(content), 0644))
		artefactPaths = append(artefactPaths, fmt.Sprintf("gtms/test/cases/%s/%s", target, filename))
	}

	// Create completed task file
	tf := &task.TaskFile{
		ID:      taskID,
		Type:    "create",
		Target:  target,
		Adapter: "mock-adapter",
		Status:  "complete",
		Created: "2026-04-19T10:00:00Z",
		Branch:  "feature/create-" + target,
	}
	_, err := task.Create(root, tf, "")
	require.NoError(t, err)

	// Create result contract
	artefact := ""
	for i, p := range artefactPaths {
		if i > 0 {
			artefact += ","
		}
		artefact += p
	}

	rc := &result.ResultContract{
		Task:      taskID,
		Command:   "create",
		Target:    target,
		Adapter:   "mock-adapter",
		Mode:      "sync",
		Created:   "2026-04-19T10:00:00Z",
		Status:    "complete",
		Result:    "pass", // ENH-130: complete requires result
		Artefact:  artefact,
		Completed: "2026-04-19T10:05:00Z",
		Warnings:  warnings,
	}
	_, err = result.Create(root, rc)
	require.NoError(t, err)

	return root
}

func TestRunCreateStatusDetail_ListsTCsOnCompletion(t *testing.T) {
	root := setupCreateStatusFixture(t, "task-stat001", "my-feature",
		[]struct{ id, title string }{
			{"tc-abc00001", "Login happy path"},
			{"tc-abc00002", "Login error path"},
		},
		nil,
	)

	var buf bytes.Buffer
	err := runCreateStatusDetail(context.Background(), &buf, root, nil, "my-feature")
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "tc-abc00001")
	assert.Contains(t, out, "Login happy path")
	assert.Contains(t, out, "tc-abc00002")
	assert.Contains(t, out, "Login error path")
	assert.Contains(t, out, "Created 2 test cases:")
}

func TestRunCreateStatusDetail_ListsSingleTC(t *testing.T) {
	root := setupCreateStatusFixture(t, "task-stat002", "single-tc",
		[]struct{ id, title string }{
			{"tc-abc00001", "Only test case"},
		},
		nil,
	)

	var buf bytes.Buffer
	err := runCreateStatusDetail(context.Background(), &buf, root, nil, "single-tc")
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "tc-abc00001")
	assert.Contains(t, out, "Only test case")
	assert.Contains(t, out, "Created 1 test case:")
}

func TestRunCreateStatusDetail_SurfacesWarnings(t *testing.T) {
	root := setupCreateStatusFixture(t, "task-stat003", "warn-feature",
		[]struct{ id, title string }{
			{"tc-abc00001", "Warning test"},
		},
		[]string{"prompt template missing guides section"},
	)

	var buf bytes.Buffer
	err := runCreateStatusDetail(context.Background(), &buf, root, nil, "warn-feature")
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "prompt template missing guides section")
}

func TestRunCreateStatusDetail_NoTCsWhenArtefactEmpty(t *testing.T) {
	root := t.TempDir()

	// Create directories
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/tasks", "complete"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".gtms", "results"), 0755))

	// Task with no artefact
	tf := &task.TaskFile{
		ID:      "task-stat004",
		Type:    "create",
		Target:  "empty-target",
		Adapter: "mock",
		Status:  "complete",
		Created: "2026-04-19T10:00:00Z",
		Branch:  "feature/create-empty-target",
	}
	_, err := task.Create(root, tf, "")
	require.NoError(t, err)

	rc := &result.ResultContract{
		Task:      "task-stat004",
		Command:   "create",
		Target:    "empty-target",
		Adapter:   "mock",
		Mode:      "sync",
		Created:   "2026-04-19T10:00:00Z",
		Status:    "complete",
		Result:    "pass", // ENH-130: complete requires result
		Completed: "2026-04-19T10:05:00Z",
	}
	_, err = result.Create(root, rc)
	require.NoError(t, err)

	var buf bytes.Buffer
	err = runCreateStatusDetail(context.Background(), &buf, root, nil, "empty-target")
	require.NoError(t, err)

	out := buf.String()
	assert.NotContains(t, out, "Created 0")
	assert.NotContains(t, out, "test case")
}
