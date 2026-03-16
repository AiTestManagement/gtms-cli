package task

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateAndRead(t *testing.T) {
	root := t.TempDir()

	tf := &TaskFile{
		ID:      "task-abc1234",
		Type:    "create",
		Target:  "JIRA-456",
		Adapter: "local-claude",
		Status:  "pending",
		Created: "2025-02-14T10:00:00Z",
		Branch:  "feature/create-JIRA-456",
	}

	path, err := Create(root, tf, "Task body content\n")
	require.NoError(t, err)
	assert.FileExists(t, path)
	assert.Contains(t, path, filepath.Join("test-tasks", "pending"))
	assert.Contains(t, path, "task-abc1234-create-JIRA-456.md")

	// Read it back
	readTF, err := Read(path)
	require.NoError(t, err)
	assert.Equal(t, "task-abc1234", readTF.ID)
	assert.Equal(t, "create", readTF.Type)
	assert.Equal(t, "JIRA-456", readTF.Target)
	assert.Equal(t, "local-claude", readTF.Adapter)
	assert.Equal(t, "pending", readTF.Status)
	assert.Equal(t, "2025-02-14T10:00:00Z", readTF.Created)
	assert.Equal(t, "feature/create-JIRA-456", readTF.Branch)
}

func TestCreate_DefaultStatus(t *testing.T) {
	root := t.TempDir()

	tf := &TaskFile{
		ID:     "task-def5678",
		Type:   "automate",
		Target: "tc-001",
	}

	path, err := Create(root, tf, "")
	require.NoError(t, err)
	assert.Contains(t, path, filepath.Join("test-tasks", "pending"))
	assert.Equal(t, "pending", tf.Status)
	assert.NotEmpty(t, tf.Created) // should have been set
}

func TestCreate_MissingFields(t *testing.T) {
	root := t.TempDir()

	_, err := Create(root, &TaskFile{Type: "create", Target: "X"}, "")
	assert.Error(t, err, "missing ID should error")

	_, err = Create(root, &TaskFile{ID: "task-123", Target: "X"}, "")
	assert.Error(t, err, "missing type should error")

	_, err = Create(root, &TaskFile{ID: "task-123", Type: "create"}, "")
	assert.Error(t, err, "missing target should error")
}

func TestMove(t *testing.T) {
	root := t.TempDir()

	tf := &TaskFile{
		ID:      "task-mov1234",
		Type:    "create",
		Target:  "JIRA-789",
		Adapter: "test-adapter",
		Status:  "pending",
		Created: "2025-02-14T10:00:00Z",
		Branch:  "feature/create-JIRA-789",
	}

	path, err := Create(root, tf, "Move test body\n")
	require.NoError(t, err)
	assert.FileExists(t, path)

	// Move to complete
	err = Move(root, tf, "complete")
	require.NoError(t, err)
	assert.Equal(t, "complete", tf.Status)

	// Old file should be gone
	assert.NoFileExists(t, path)

	// New file should exist
	newPath := filepath.Join(root, "test-tasks", "complete", "task-mov1234-create-JIRA-789.md")
	assert.FileExists(t, newPath)

	// Read moved file and verify status updated in frontmatter
	readTF, err := Read(newPath)
	require.NoError(t, err)
	assert.Equal(t, "complete", readTF.Status)
	assert.Equal(t, "task-mov1234", readTF.ID)
}

func TestMove_InvalidStatus(t *testing.T) {
	root := t.TempDir()

	tf := &TaskFile{
		ID:     "task-inv1234",
		Type:   "create",
		Target: "X",
		Status: "pending",
	}

	err := Move(root, tf, "invalid-status")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid status")
}

func TestMove_SameStatus(t *testing.T) {
	root := t.TempDir()

	tf := &TaskFile{
		ID:      "task-same123",
		Type:    "create",
		Target:  "X",
		Adapter: "test",
		Status:  "pending",
		Created: "2025-02-14T10:00:00Z",
	}

	_, err := Create(root, tf, "")
	require.NoError(t, err)

	// Move to same status should be a no-op
	err = Move(root, tf, "pending")
	require.NoError(t, err)
}

func TestList(t *testing.T) {
	root := t.TempDir()

	// Create tasks in different statuses
	tf1 := &TaskFile{
		ID: "task-list001", Type: "create", Target: "JIRA-1",
		Adapter: "test", Status: "pending", Created: "2025-02-14T10:00:00Z",
	}
	tf2 := &TaskFile{
		ID: "task-list002", Type: "create", Target: "JIRA-2",
		Adapter: "test", Status: "pending", Created: "2025-02-14T10:00:00Z",
	}
	_, err := Create(root, tf1, "")
	require.NoError(t, err)
	_, err = Create(root, tf2, "")
	require.NoError(t, err)

	// Create a task in complete status
	tf3Complete := &TaskFile{
		ID: "task-list003", Type: "automate", Target: "tc-001",
		Adapter: "test", Status: "complete", Created: "2025-02-14T10:00:00Z",
	}
	_, err = Create(root, tf3Complete, "")
	require.NoError(t, err)

	// List pending only
	pending, err := List(root, "pending")
	require.NoError(t, err)
	assert.Len(t, pending, 2)

	// List complete only
	complete, err := List(root, "complete")
	require.NoError(t, err)
	assert.Len(t, complete, 1)
	assert.Equal(t, "tc-001", complete[0].Target)

	// List all
	all, err := List(root)
	require.NoError(t, err)
	assert.Len(t, all, 3)
}

func TestList_EmptyDir(t *testing.T) {
	root := t.TempDir()

	tasks, err := List(root, "pending")
	require.NoError(t, err)
	assert.Empty(t, tasks)
}

func TestFindByTarget(t *testing.T) {
	root := t.TempDir()

	tf1 := &TaskFile{
		ID: "task-find001", Type: "create", Target: "JIRA-100",
		Adapter: "test", Status: "pending", Created: "2025-02-14T10:00:00Z",
	}
	tf2 := &TaskFile{
		ID: "task-find002", Type: "automate", Target: "tc-050",
		Adapter: "test", Status: "in-progress", Created: "2025-02-14T10:00:00Z",
	}

	_, err := Create(root, tf1, "")
	require.NoError(t, err)

	// Create in-progress directory manually for tf2
	tf2.Status = "in-progress"
	_, err = Create(root, tf2, "")
	require.NoError(t, err)

	// Find create task for JIRA-100
	found, err := FindByTarget(root, "create", "JIRA-100")
	require.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, "task-find001", found.ID)

	// Find automate task for tc-050
	found, err = FindByTarget(root, "automate", "tc-050")
	require.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, "task-find002", found.ID)

	// Not found
	found, err = FindByTarget(root, "create", "NONEXISTENT")
	require.NoError(t, err)
	assert.Nil(t, found, "should return nil when not found")
}

func TestFindByTarget_CustomStatuses(t *testing.T) {
	root := t.TempDir()

	tf := &TaskFile{
		ID: "task-cust001", Type: "create", Target: "JIRA-200",
		Adapter: "test", Status: "complete", Created: "2025-02-14T10:00:00Z",
	}

	_, err := Create(root, tf, "")
	require.NoError(t, err)

	// Should not find in default statuses (pending, in-progress)
	found, err := FindByTarget(root, "create", "JIRA-200")
	require.NoError(t, err)
	assert.Nil(t, found)

	// Should find in complete
	found, err = FindByTarget(root, "create", "JIRA-200", "complete")
	require.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, "task-cust001", found.ID)
}

func TestCreateWithOptionalFields(t *testing.T) {
	root := t.TempDir()

	tf := &TaskFile{
		ID:        "task-opt1234",
		Type:      "automate",
		Target:    "tc-001",
		Adapter:   "test",
		Status:    "pending",
		Created:   "2025-02-14T10:00:00Z",
		Branch:    "feature/automate-tc-001",
		Reference: "JIRA-100",
		Framework: "playwright",
	}

	path, err := Create(root, tf, "")
	require.NoError(t, err)

	readTF, err := Read(path)
	require.NoError(t, err)
	assert.Equal(t, "JIRA-100", readTF.Reference)
	assert.Equal(t, "playwright", readTF.Framework)
}

func TestTaskFileNaming(t *testing.T) {
	root := t.TempDir()

	tf := &TaskFile{
		ID:      "task-abc1234",
		Type:    "execute",
		Target:  "tc-007",
		Adapter: "test",
		Status:  "pending",
		Created: "2025-02-14T10:00:00Z",
	}

	path, err := Create(root, tf, "")
	require.NoError(t, err)

	filename := filepath.Base(path)
	assert.Equal(t, "task-abc1234-execute-tc-007.md", filename)

	// Verify directory structure
	relPath, err := filepath.Rel(root, path)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join("test-tasks", "pending", "task-abc1234-execute-tc-007.md"), relPath)
}

func TestFilename_SanitizesTarget(t *testing.T) {
	tests := []struct {
		name     string
		target   string
		expected string
	}{
		{
			name:     "normal ID",
			target:   "JIRA-456",
			expected: "task-abc1234-create-JIRA-456.md",
		},
		{
			name:     "file path with .md extension",
			target:   "reference/gtms-implementation.md",
			expected: "task-abc1234-create-reference-gtms-implementation.md",
		},
		{
			name:     "bare .md extension",
			target:   "some-doc.md",
			expected: "task-abc1234-create-some-doc.md",
		},
		{
			name:     "nested path preserves components",
			target:   "path/to/something",
			expected: "task-abc1234-create-path-to-something.md",
		},
		{
			name:     "backslash path preserves components",
			target:   "path\\to\\file.md",
			expected: "task-abc1234-create-path-to-file.md",
		},
		{
			name:     "nested folder for create (ENH-049)",
			target:   "payments/checkout",
			expected: "task-abc1234-create-payments-checkout.md",
		},
		{
			name:     "test case ID unchanged",
			target:   "tc-007",
			expected: "task-abc1234-create-tc-007.md",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tf := &TaskFile{
				ID:     "task-abc1234",
				Type:   "create",
				Target: tt.target,
			}
			assert.Equal(t, tt.expected, tf.Filename())
		})
	}
}

func TestCreateAndMove_WithPathTarget(t *testing.T) {
	root := t.TempDir()

	tf := &TaskFile{
		ID:      "task-path123",
		Type:    "create",
		Target:  "reference/gtms-implementation.md",
		Adapter: "test",
		Status:  "pending",
		Created: "2025-02-14T10:00:00Z",
		Branch:  "feature/create-test",
	}

	path, err := Create(root, tf, "")
	require.NoError(t, err)

	// Filename should not have double .md, and should preserve directory components
	filename := filepath.Base(path)
	assert.Equal(t, "task-path123-create-reference-gtms-implementation.md", filename)
	assert.NotContains(t, filename, ".md.md")

	// Move should work correctly
	err = Move(root, tf, "complete")
	require.NoError(t, err)

	newPath := filepath.Join(root, "test-tasks", "complete", "task-path123-create-reference-gtms-implementation.md")
	assert.FileExists(t, newPath)
}

func TestMove_PreservesBody(t *testing.T) {
	root := t.TempDir()

	tf := &TaskFile{
		ID:      "task-body123",
		Type:    "create",
		Target:  "JIRA-999",
		Adapter: "test",
		Status:  "pending",
		Created: "2025-02-14T10:00:00Z",
	}

	body := "# Task Body\n\nThis is the task description.\n"
	_, err := Create(root, tf, body)
	require.NoError(t, err)

	err = Move(root, tf, "complete")
	require.NoError(t, err)

	newPath := filepath.Join(root, "test-tasks", "complete", "task-body123-create-JIRA-999.md")
	content, err := os.ReadFile(newPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "# Task Body")
	assert.Contains(t, string(content), "This is the task description.")
}
