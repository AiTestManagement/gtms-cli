package result

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateAndRead(t *testing.T) {
	root := t.TempDir()

	rc := &ResultContract{
		Task:    "task-abc1234",
		Command: "create",
		Target:  "JIRA-456",
		Adapter: "local-claude",
		Mode:    "sync",
		Created: "2025-02-14T10:00:00Z",
		Status:  "pending",
	}

	path, err := Create(root, rc)
	require.NoError(t, err)
	assert.FileExists(t, path)
	assert.Contains(t, path, filepath.Join(".gtms", "results"))
	assert.Contains(t, path, "task-abc1234.result.yaml")

	// Read back
	readRC, err := Read(path)
	require.NoError(t, err)
	assert.Equal(t, "task-abc1234", readRC.Task)
	assert.Equal(t, "create", readRC.Command)
	assert.Equal(t, "JIRA-456", readRC.Target)
	assert.Equal(t, "local-claude", readRC.Adapter)
	assert.Equal(t, "sync", readRC.Mode)
	assert.Equal(t, "2025-02-14T10:00:00Z", readRC.Created)
	assert.Equal(t, "pending", readRC.Status)
}

func TestCreate_DefaultStatus(t *testing.T) {
	root := t.TempDir()

	rc := &ResultContract{
		Task:    "task-def5678",
		Command: "automate",
		Target:  "tc-001",
		Adapter: "test",
		Mode:    "sync",
		Created: "2025-02-14T10:00:00Z",
	}

	path, err := Create(root, rc)
	require.NoError(t, err)

	readRC, err := Read(path)
	require.NoError(t, err)
	assert.Equal(t, "pending", readRC.Status)
}

func TestCreate_MissingTask(t *testing.T) {
	root := t.TempDir()

	_, err := Create(root, &ResultContract{Command: "create"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "task ID is required")
}

func TestUpdate(t *testing.T) {
	root := t.TempDir()

	rc := &ResultContract{
		Task:    "task-upd1234",
		Command: "create",
		Target:  "JIRA-789",
		Adapter: "test-adapter",
		Mode:    "sync",
		Created: "2025-02-14T10:00:00Z",
		Status:  "pending",
	}

	path, err := Create(root, rc)
	require.NoError(t, err)

	// Update specific fields
	err = Update(path, map[string]interface{}{
		"status":    "complete",
		"artefact":  "test-cases/tc-001-checkout.md",
		"attempts":  1,
		"summary":   "Successfully created test case",
		"completed": "2025-02-14T10:05:00Z",
	})
	require.NoError(t, err)

	// Read and verify
	readRC, err := Read(path)
	require.NoError(t, err)
	assert.Equal(t, "complete", readRC.Status)
	assert.Equal(t, "test-cases/tc-001-checkout.md", readRC.Artefact)
	assert.Equal(t, 1, readRC.Attempts)
	assert.Equal(t, "Successfully created test case", readRC.Summary)
	assert.Equal(t, "2025-02-14T10:05:00Z", readRC.Completed)

	// Original fields preserved
	assert.Equal(t, "task-upd1234", readRC.Task)
	assert.Equal(t, "create", readRC.Command)
	assert.Equal(t, "JIRA-789", readRC.Target)
	assert.Equal(t, "test-adapter", readRC.Adapter)
}

func TestUpdate_ErrorStatus(t *testing.T) {
	root := t.TempDir()

	rc := &ResultContract{
		Task:    "task-err1234",
		Command: "create",
		Target:  "JIRA-111",
		Adapter: "test",
		Mode:    "sync",
		Created: "2025-02-14T10:00:00Z",
		Status:  "pending",
	}

	path, err := Create(root, rc)
	require.NoError(t, err)

	err = Update(path, map[string]interface{}{
		"status":    "error",
		"summary":   "Process exited with code 1",
		"completed": "2025-02-14T10:01:00Z",
	})
	require.NoError(t, err)

	readRC, err := Read(path)
	require.NoError(t, err)
	assert.Equal(t, "error", readRC.Status)
	assert.Equal(t, "Process exited with code 1", readRC.Summary)
}

func TestResultPath(t *testing.T) {
	path := ResultPath("/projects/myapp", "task-abc1234")
	assert.Equal(t, filepath.Join("/projects/myapp", ".gtms", "results", "task-abc1234.result.yaml"), path)
}

func TestCreateWithAllFields(t *testing.T) {
	root := t.TempDir()

	rc := &ResultContract{
		Task:      "task-full123",
		Command:   "create",
		Target:    "JIRA-999",
		Adapter:   "full-adapter",
		Mode:      "async",
		Created:   "2025-02-14T10:00:00Z",
		Status:    "complete",
		Artefact:  "test-cases/tc-099.md",
		Attempts:  3,
		Summary:   "Completed after retries",
		Log:       "attempt 1 failed\nattempt 2 failed\nattempt 3 succeeded",
		Completed: "2025-02-14T10:30:00Z",
	}

	path, err := Create(root, rc)
	require.NoError(t, err)

	readRC, err := Read(path)
	require.NoError(t, err)
	assert.Equal(t, "complete", readRC.Status)
	assert.Equal(t, "test-cases/tc-099.md", readRC.Artefact)
	assert.Equal(t, 3, readRC.Attempts)
	assert.Equal(t, "Completed after retries", readRC.Summary)
	assert.Contains(t, readRC.Log, "attempt 1 failed")
	assert.Equal(t, "2025-02-14T10:30:00Z", readRC.Completed)
}
