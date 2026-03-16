package adapter

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/aitestmanagement/gtms-cli/internal/task"
)

// setupTestProject creates a minimal project structure for invoker tests.
func setupTestProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	// Create required directories
	for _, dir := range []string{
		"test-tasks/pending", "test-tasks/complete", "test-tasks/failed",
		"test-tasks/in-progress", "test-tasks/in-review",
		"test-cases", "test-automation",
		".gtms/results", ".gtms/worktrees", ".gtms/logs",
	} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0755))
	}

	// Create gtms.config
	cfgContent := `project:
  name: Test Project
  repo: org/test
adapters:
  create:
    mock-tier1:
      mode: sync
      command: 'echo "mock tier1 output"'
defaults:
  create: mock-tier1
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "gtms.config"), []byte(cfgContent), 0644))

	return root
}

func TestInvokeWithRoot_Tier1Sync(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock-tier1",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `echo "mock tier1 output"`},
		Tier:    1,
		Mode:    "sync",
	}

	flags := CommandFlags{}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "JIRA-456", flags)
	require.NoError(t, err)
	assert.Equal(t, "mock-tier1", res.Adapter)
	assert.Equal(t, "sync", res.Mode)
	assert.Equal(t, "complete", res.Status)
	assert.Contains(t, res.TaskID, "task-")
	assert.Contains(t, res.Branch, "feature/create-JIRA-456")

	// Verify task file moved to complete
	completeTasks, err := task.List(root, "complete")
	require.NoError(t, err)
	assert.Len(t, completeTasks, 1)
	assert.Equal(t, "JIRA-456", completeTasks[0].Target)
	assert.Equal(t, "mock-tier1", completeTasks[0].Adapter)

	// Verify result contract
	rcPath := result.ResultPath(root, res.TaskID)
	rc, err := result.Read(rcPath)
	require.NoError(t, err)
	assert.Equal(t, "complete", rc.Status)
	assert.NotEmpty(t, rc.Completed)
}

// TestBUG022_SyncTaskTransitionsToInProgress verifies that sync adapters move the task
// to in-progress before invocation. We use a failing adapter to verify the task transitions
// from in-progress to failed (not from pending to failed), confirming the in-progress
// transition happened.
func TestBUG022_SyncTaskTransitionsToInProgress(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	// Use a successful sync adapter
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock-tier1",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `echo "output"`},
		Tier:    1,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "BUG022-TEST", CommandFlags{})
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)

	// Verify no task remains in pending (it should have moved through in-progress to complete)
	pendingTasks, err := task.List(root, "pending")
	require.NoError(t, err)
	assert.Empty(t, pendingTasks, "no tasks should remain in pending after sync execution")

	// Verify task ended in complete
	completeTasks, err := task.List(root, "complete")
	require.NoError(t, err)
	assert.Len(t, completeTasks, 1)

	// Verify result contract was updated through in-progress
	// (final status is complete, but the contract should show complete, not pending)
	rcPath := result.ResultPath(root, res.TaskID)
	rc, err := result.Read(rcPath)
	require.NoError(t, err)
	assert.Equal(t, "complete", rc.Status)
}

// TestBUG022_SyncFailedTaskTransitionsFromInProgress verifies that a failing sync adapter
// moves the task from in-progress to failed (not directly from pending).
func TestBUG022_SyncFailedTaskTransitionsFromInProgress(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock-tier1",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `exit 1`},
		Tier:    1,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "BUG022-FAIL", CommandFlags{})
	require.NoError(t, err)
	assert.Equal(t, "error", res.Status)

	// Verify no task in pending (moved through in-progress before failing)
	pendingTasks, err := task.List(root, "pending")
	require.NoError(t, err)
	assert.Empty(t, pendingTasks, "no tasks should remain in pending after sync failure")

	// Verify task ended in failed
	failedTasks, err := task.List(root, "failed")
	require.NoError(t, err)
	assert.Len(t, failedTasks, 1)
}

func TestInvokeWithRoot_ContextFileFlow(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock-tier1",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `echo "{context}"`},
		Tier:    1,
		Mode:    "sync",
	}

	flags := CommandFlags{
		ContextFile: "/tmp/notes.md",
		Context:     "supplementary context from file",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "JIRA-CTX", flags)
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)
	assert.Contains(t, res.Summary, "supplementary context from file")
}

func TestInvokeWithRoot_ContextFileEmpty(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock-tier1",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `echo "no context"`},
		Tier:    1,
		Mode:    "sync",
	}

	flags := CommandFlags{}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "JIRA-NOCTX", flags)
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)
	assert.Contains(t, res.Summary, "no context")
}

func TestInvokeWithRoot_Tier2Sync(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	// Create mock tier2 script
	scriptPath := filepath.Join(root, "testdata", "mock-adapter.sh")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "testdata"), 0755))

	script := `#!/bin/bash
cat > "${GTMS_RESULT_FILE}" <<EOF
task: ${GTMS_TASK_ID}
command: ${GTMS_COMMAND}
target: ${GTMS_REFERENCE}
adapter: mock-tier2
mode: sync
status: complete
artefact: test-output.md
attempts: 1
summary: "Mock tier2 completed"
completed: "2025-02-14T10:05:00Z"
EOF
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0755))

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock-tier2",
		Config:  &config.AdapterConfig{Mode: "sync", Script: "testdata/mock-adapter.sh"},
		Tier:    2,
		Mode:    "sync",
	}

	flags := CommandFlags{}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "JIRA-789", flags)
	require.NoError(t, err)
	assert.Equal(t, "mock-tier2", res.Adapter)
	assert.Equal(t, "sync", res.Mode)
	assert.Equal(t, "complete", res.Status)

	// Verify task file moved to complete
	completeTasks, err := task.List(root, "complete")
	require.NoError(t, err)
	assert.Len(t, completeTasks, 1)
	assert.Equal(t, "JIRA-789", completeTasks[0].Target)
	assert.Equal(t, "mock-tier2", completeTasks[0].Adapter)
}

func TestInvokeWithRoot_Tier1Error(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "failing-adapter",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `echo "error msg" >&2 && exit 1`},
		Tier:    1,
		Mode:    "sync",
	}

	flags := CommandFlags{}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "JIRA-FAIL", flags)
	require.NoError(t, err) // Invoke returns result even on adapter error
	assert.Equal(t, "error", res.Status)
	assert.Contains(t, res.Summary, "Process exited with code 1")

	// Verify task moved to failed
	failedTasks, err := task.List(root, "failed")
	require.NoError(t, err)
	assert.Len(t, failedTasks, 1)
	assert.Equal(t, "JIRA-FAIL", failedTasks[0].Target)
}

func TestInvokeWithRoot_Async(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "async-adapter",
		Config:  &config.AdapterConfig{Mode: "async", Command: `echo "async started"`},
		Tier:    1,
		Mode:    "async",
	}

	flags := CommandFlags{}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "JIRA-ASYNC", flags)
	require.NoError(t, err)
	assert.Equal(t, "in-progress", res.Status)
	assert.Equal(t, "async", res.Mode)

	// Verify task moved to in-progress
	inProgressTasks, err := task.List(root, "in-progress")
	require.NoError(t, err)
	assert.Len(t, inProgressTasks, 1)
	assert.Equal(t, "JIRA-ASYNC", inProgressTasks[0].Target)
}

func TestInvokeWithRoot_TaskFileCreatedCorrectly(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "test-adapter",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `echo "ok"`},
		Tier:    1,
		Mode:    "sync",
	}

	flags := CommandFlags{}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "JIRA-CHECK", flags)
	require.NoError(t, err)

	// Read the task file from complete
	completeTasks, err := task.List(root, "complete")
	require.NoError(t, err)
	require.Len(t, completeTasks, 1)

	tf := completeTasks[0]
	assert.Equal(t, res.TaskID, tf.ID)
	assert.Equal(t, "create", tf.Type)
	assert.Equal(t, "JIRA-CHECK", tf.Target)
	assert.Equal(t, "test-adapter", tf.Adapter)
	assert.Contains(t, tf.Branch, "feature/create-JIRA-CHECK")
	assert.NotEmpty(t, tf.Created)
}

func TestInvokeWithRoot_GuidesFlow(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	// Create guide-dir with a guide file
	guideDir := filepath.Join(root, "test-cases", "guides")
	require.NoError(t, os.MkdirAll(guideDir, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(guideDir, "template.md"),
		[]byte("# Test Template\nUse this format.\n"), 0644,
	))

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock-tier1",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `echo "ok"`, GuideDir: "test-cases/guides/"},
		Tier:    1,
		Mode:    "sync",
	}

	flags := CommandFlags{}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "JIRA-GUIDES", flags)
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)
}

func TestInvokeWithRoot_PromptFileCreated(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	// Create prompt template file on disk
	tmplDir := filepath.Join(root, "prompts")
	require.NoError(t, os.MkdirAll(tmplDir, 0755))
	tmpl := "Generate tests for {reference}\n\nContext: {context}"
	require.NoError(t, os.WriteFile(filepath.Join(tmplDir, "create.md"), []byte(tmpl), 0644))

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock-tier1",
		Config: &config.AdapterConfig{
			Mode:           "sync",
			Command:        `echo "ok"`,
			PromptTemplate: "prompts/create.md",
		},
		Tier: 1,
		Mode: "sync",
	}

	flags := CommandFlags{Context: "test context", Folder: "jira-999", Reference: "JIRA-999"}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "jira-999", flags)
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)

	// Verify prompt file exists in .gtms/tmp/
	matches, _ := filepath.Glob(filepath.Join(root, ".gtms", "tmp", "*-prompt.md"))
	require.Len(t, matches, 1, "expected one prompt file in .gtms/tmp/")

	content, err := os.ReadFile(matches[0])
	require.NoError(t, err)
	assert.Contains(t, string(content), "JIRA-999")
	assert.Contains(t, string(content), "test context")
}

func TestInvokeWithRoot_PromptFileCreatedTier2(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	// Create prompt template file on disk
	tmplDir := filepath.Join(root, "prompts")
	require.NoError(t, os.MkdirAll(tmplDir, 0755))
	tmpl := "Generate tests for {reference}"
	require.NoError(t, os.WriteFile(filepath.Join(tmplDir, "create.md"), []byte(tmpl), 0644))

	// Create mock tier2 script that reads prompt file
	scriptDir := filepath.Join(root, "testdata")
	require.NoError(t, os.MkdirAll(scriptDir, 0755))
	script := "#!/bin/bash\ncat \"$GTMS_PROMPT_FILE\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(scriptDir, "prompt-reader.sh"), []byte(script), 0755))

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock-tier2",
		Config: &config.AdapterConfig{
			Mode:           "sync",
			Script:         "testdata/prompt-reader.sh",
			PromptTemplate: "prompts/create.md",
		},
		Tier: 2,
		Mode: "sync",
	}

	flags := CommandFlags{Folder: "jira-t2pf", Reference: "JIRA-T2PF"}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "jira-t2pf", flags)
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)
	// Tier 2 script outputs the prompt file content as summary
	assert.Contains(t, res.Summary, "JIRA-T2PF")

	// Verify prompt file exists in .gtms/tmp/
	matches, _ := filepath.Glob(filepath.Join(root, ".gtms", "tmp", "*-prompt.md"))
	require.Len(t, matches, 1, "expected one prompt file in .gtms/tmp/")
}

// --- sanitizeBranchTarget tests (BUG-008) ---

func TestSanitizeBranchTarget_FilePath(t *testing.T) {
	assert.Equal(t, "reference-adapter-guide", sanitizeBranchTarget("reference/adapter-guide.md"))
}

func TestSanitizeBranchTarget_NestedPath(t *testing.T) {
	assert.Equal(t, "some-path-to-file", sanitizeBranchTarget("some/path/to/file.yaml"))
}

func TestSanitizeBranchTarget_SimpleID(t *testing.T) {
	assert.Equal(t, "REQ-123", sanitizeBranchTarget("REQ-123"))
}

func TestSanitizeBranchTarget_BackslashPath(t *testing.T) {
	assert.Equal(t, "dir-subdir-file", sanitizeBranchTarget("dir\\subdir\\file.md"))
}

func TestSanitizeBranchTarget_NoExtension(t *testing.T) {
	assert.Equal(t, "path-to-target", sanitizeBranchTarget("path/to/target"))
}

// --- readGuides tests ---

func TestReadGuides_HappyPath(t *testing.T) {
	dir := t.TempDir()
	guideDir := filepath.Join(dir, "guides")
	require.NoError(t, os.MkdirAll(guideDir, 0755))

	require.NoError(t, os.WriteFile(filepath.Join(guideDir, "01-template.md"), []byte("# Template\nUse this.\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(guideDir, "02-principles.md"), []byte("# Principles\nBe precise.\n"), 0644))

	result, err := readGuides(dir, "guides")
	require.NoError(t, err)
	assert.Contains(t, result, "# Template")
	assert.Contains(t, result, "# Principles")
	// Guides are wrapped in XML tags with the filename as attribute
	assert.Contains(t, result, `<guide name="01-template.md">`)
	assert.Contains(t, result, `<guide name="02-principles.md">`)
	assert.Contains(t, result, "</guide>")
}

func TestReadGuides_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	guideDir := filepath.Join(dir, "guides")
	require.NoError(t, os.MkdirAll(guideDir, 0755))

	result, err := readGuides(dir, "guides")
	require.NoError(t, err)
	assert.Equal(t, "", result)
}

func TestReadGuides_MissingDir(t *testing.T) {
	dir := t.TempDir()

	result, err := readGuides(dir, "nonexistent-guides")
	require.NoError(t, err)
	assert.Equal(t, "", result)
}

func TestReadGuides_EmptyGuideDir(t *testing.T) {
	dir := t.TempDir()

	result, err := readGuides(dir, "")
	require.NoError(t, err)
	assert.Equal(t, "", result)
}

func TestReadGuides_SortedOrder(t *testing.T) {
	dir := t.TempDir()
	guideDir := filepath.Join(dir, "guides")
	require.NoError(t, os.MkdirAll(guideDir, 0755))

	// Write files in reverse alphabetical order
	require.NoError(t, os.WriteFile(filepath.Join(guideDir, "z-last.md"), []byte("LAST"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(guideDir, "a-first.md"), []byte("FIRST"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(guideDir, "m-middle.md"), []byte("MIDDLE"), 0644))

	result, err := readGuides(dir, "guides")
	require.NoError(t, err)

	// Verify sorted order: a-first appears before m-middle, which appears before z-last
	firstIdx := strings.Index(result, "FIRST")
	middleIdx := strings.Index(result, "MIDDLE")
	lastIdx := strings.Index(result, "LAST")
	require.NotEqual(t, -1, firstIdx, "FIRST must appear in output")
	require.NotEqual(t, -1, middleIdx, "MIDDLE must appear in output")
	require.NotEqual(t, -1, lastIdx, "LAST must appear in output")
	assert.Less(t, firstIdx, middleIdx, "FIRST must come before MIDDLE")
	assert.Less(t, middleIdx, lastIdx, "MIDDLE must come before LAST")
}

func TestReadGuides_SkipsNonMd(t *testing.T) {
	dir := t.TempDir()
	guideDir := filepath.Join(dir, "guides")
	require.NoError(t, os.MkdirAll(guideDir, 0755))

	require.NoError(t, os.WriteFile(filepath.Join(guideDir, "guide.md"), []byte("GUIDE CONTENT"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(guideDir, "notes.txt"), []byte("TEXT CONTENT"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(guideDir, "data.json"), []byte("{}"), 0644))

	result, err := readGuides(dir, "guides")
	require.NoError(t, err)
	assert.Contains(t, result, "GUIDE CONTENT")
	assert.NotContains(t, result, "TEXT CONTENT")
	assert.NotContains(t, result, "{}")
}

func TestInvokeWithRoot_TimeoutFromConfig(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "slow-adapter",
		Config: &config.AdapterConfig{
			Mode:    "sync",
			Command: "exec sleep 30",
			Timeout: "200ms",
		},
		Tier: 1,
		Mode: "sync",
	}

	flags := CommandFlags{}

	start := time.Now()
	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "JIRA-TIMEOUT", flags)
	elapsed := time.Since(start)

	// Should have been killed by timeout, not run for 30 seconds
	assert.Less(t, elapsed, 15*time.Second, "should have been killed by timeout")

	// Invoker returns nil result and error on cancellation/timeout
	assert.Nil(t, res)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")

	// Verify task file moved to failed
	failedTasks, listErr := task.List(root, "failed")
	require.NoError(t, listErr)
	assert.Len(t, failedTasks, 1)
	assert.Equal(t, "JIRA-TIMEOUT", failedTasks[0].Target)

	// Verify no task left in pending
	pendingTasks, listErr := task.List(root, "pending")
	require.NoError(t, listErr)
	assert.Len(t, pendingTasks, 0)

	// Verify result contract status is error
	// Find the task ID from the failed task
	taskID := failedTasks[0].ID
	rcPath := result.ResultPath(root, taskID)
	rc, rcErr := result.Read(rcPath)
	require.NoError(t, rcErr)
	assert.Equal(t, "error", rc.Status)
	assert.Contains(t, rc.Summary, "timed out")
}

func TestInvokeWithRoot_CancellationCleansUpTaskFile(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "slow-adapter",
		Config:  &config.AdapterConfig{Mode: "sync", Command: "exec sleep 30"},
		Tier:    1,
		Mode:    "sync",
	}

	flags := CommandFlags{}

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)

	start := time.Now()
	res, err := InvokeWithRoot(ctx, root, cfg, resolved, "JIRA-CANCEL", flags)
	elapsed := time.Since(start)

	// Should have been killed by cancellation, not run for 30 seconds
	assert.Less(t, elapsed, 15*time.Second, "should have been killed by cancellation")

	// Two valid outcomes depending on timing:
	// Path A: InvokeTier1 returns error → invoker detects ctx.Err(), returns (nil, error with "cancelled")
	// Path B: InvokeTier1 captures exit code → handleSyncResult processes it as non-zero exit → returns (result with status "error", nil)
	// Both paths correctly move the task to "failed".
	if err != nil {
		// Path A: cancellation detected by invoker
		assert.Contains(t, err.Error(), "cancelled")
	} else {
		// Path B: normal error handling caught the killed process
		require.NotNil(t, res)
		assert.Equal(t, "error", res.Status)
	}

	// Key invariant: task file is in "failed", not left in "pending"
	failedTasks, listErr := task.List(root, "failed")
	require.NoError(t, listErr)
	assert.Len(t, failedTasks, 1)
	assert.Equal(t, "JIRA-CANCEL", failedTasks[0].Target)

	pendingTasks, listErr := task.List(root, "pending")
	require.NoError(t, listErr)
	assert.Len(t, pendingTasks, 0)
}

// --- OutputDir tests ---

func TestInvokeWithRoot_CreateWithOutputDir(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock-tier1",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `echo "{output_dir}"`, OutputDir: "custom/tests/"},
		Tier:    1,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "REQ-OUT1", CommandFlags{})
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)
	assert.Contains(t, res.Summary, filepath.Join(root, "custom/tests/"))
}

func TestInvokeWithRoot_CreateWithoutOutputDir(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock-tier1",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `echo "{output_dir}"`},
		Tier:    1,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "REQ-OUT2", CommandFlags{})
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)
	assert.Contains(t, res.Summary, filepath.Join(root, "test-cases"))
}

func TestInvokeWithRoot_AutomateWithOutputDir(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "automate",
		Name:    "my-auto",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `echo "{output_dir}"`, OutputDir: "tests/e2e/"},
		Tier:    1,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-001", CommandFlags{})
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)
	assert.Contains(t, res.Summary, filepath.Join(root, "tests/e2e/"))
}

func TestInvokeWithRoot_AutomateWithoutOutputDir(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "automate",
		Name:    "my-auto",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `echo "{output_dir}"`},
		Tier:    1,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-002", CommandFlags{})
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)
	assert.Contains(t, res.Summary, filepath.Join(root, "test-automation", "specs", "my-auto"))
}

func TestInvokeWithRoot_ExecuteWithoutOutputDir(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "execute",
		Name:    "my-runner",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `echo "{output_dir}"`},
		Tier:    1,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-003", CommandFlags{})
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)
	assert.Contains(t, res.Summary, filepath.Join(root, "results"))
}

func TestInvokeWithRoot_ExecuteWithOutputDir(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "execute",
		Name:    "my-runner",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `echo "{output_dir}"`, OutputDir: "test-results/bats/"},
		Tier:    1,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-001", CommandFlags{})
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)
	assert.Contains(t, res.Summary, filepath.Join(root, "test-results/bats/"))
}

// --- Environment prompt template substitution test (ENH-014 Item 3) ---

func TestEnvironment_PromptTemplateSubstitution(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	// Create prompt template with {environment} placeholder
	tmplDir := filepath.Join(root, "prompts")
	require.NoError(t, os.MkdirAll(tmplDir, 0755))
	tmpl := "Run tests for {testcase} in environment: {environment}"
	require.NoError(t, os.WriteFile(filepath.Join(tmplDir, "auto.md"), []byte(tmpl), 0644))

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "automate",
		Name:    "env-test",
		Config: &config.AdapterConfig{
			Mode:           "sync",
			Command:        `cat`,
			PromptTemplate: "prompts/auto.md",
		},
		Tier: 1,
		Mode: "sync",
	}

	flags := CommandFlags{Environment: "staging"}
	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-envtest", flags)
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)
	// The assembled prompt (piped to cat via stdin) should contain the substituted value
	assert.Contains(t, res.Summary, "staging")
	assert.NotContains(t, res.Summary, "{environment}")
}

// --- snapshotDir and scanOutputDir tests (ENH-014 Item 1) ---

func TestSnapshotDir_CapturesFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.md"), []byte("a"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.md"), []byte("b"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "subdir"), 0755))

	snap := snapshotDir(dir)
	assert.Len(t, snap, 2)
	_, hasA := snap["a.md"]
	_, hasB := snap["b.md"]
	assert.True(t, hasA)
	assert.True(t, hasB)
	// Directories should not be captured
	_, hasSub := snap["subdir"]
	assert.False(t, hasSub)
}

func TestSnapshotDir_NonExistentDir(t *testing.T) {
	snap := snapshotDir("/nonexistent/dir/12345")
	assert.Nil(t, snap)
}

func TestScanOutputDir_ReturnsRelativePaths(t *testing.T) {
	root := t.TempDir()
	outDir := filepath.Join(root, "output")
	require.NoError(t, os.MkdirAll(outDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(outDir, "result.md"), []byte("result"), 0644))

	paths := scanOutputDir(root, outDir, nil)
	require.Len(t, paths, 1)
	assert.Equal(t, "output/result.md", paths[0])
}

func TestScanOutputDir_EmptyDir(t *testing.T) {
	root := t.TempDir()
	outDir := filepath.Join(root, "output")
	require.NoError(t, os.MkdirAll(outDir, 0755))

	paths := scanOutputDir(root, outDir, nil)
	assert.Nil(t, paths)
}

func TestScanOutputDir_SkipsHiddenFiles(t *testing.T) {
	root := t.TempDir()
	outDir := filepath.Join(root, "output")
	require.NoError(t, os.MkdirAll(outDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(outDir, ".hidden"), []byte("h"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(outDir, "visible.md"), []byte("v"), 0644))

	paths := scanOutputDir(root, outDir, nil)
	require.Len(t, paths, 1)
	assert.Equal(t, "output/visible.md", paths[0])
}

func TestScanOutputDir_NonExistentDir(t *testing.T) {
	paths := scanOutputDir("/tmp", "/nonexistent/dir/12345", nil)
	assert.Nil(t, paths)
}

func TestScanOutputDir_ExcludesFilteredFiles(t *testing.T) {
	root := t.TempDir()
	outDir := filepath.Join(root, "output")
	require.NoError(t, os.MkdirAll(outDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(outDir, "old.md"), []byte("old"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(outDir, "new.md"), []byte("new"), 0644))

	exclude := map[string]struct{}{"old.md": {}}
	paths := scanOutputDir(root, outDir, exclude)
	require.Len(t, paths, 1)
	assert.Equal(t, "output/new.md", paths[0])
}

func TestTier1_ArtefactPopulatedFromOutputDir(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	// Create output dir
	outDir := filepath.Join(root, "custom-output")
	require.NoError(t, os.MkdirAll(outDir, 0755))

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	// Tier 1 adapter that writes a file directly to the output dir (no streaming delimiters)
	cmd := fmt.Sprintf(`echo "hello" > %s/test-output.md`, filepath.ToSlash(outDir))
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "file-writer",
		Config:  &config.AdapterConfig{Mode: "sync", Command: cmd, OutputDir: "custom-output"},
		Tier:    1,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "JIRA-ART", CommandFlags{})
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)
	assert.Equal(t, 1, res.ArtifactCount)
	require.Len(t, res.ArtifactPaths, 1)
	assert.Equal(t, "custom-output/test-output.md", res.ArtifactPaths[0])

	// Verify artefact field in result contract
	rcPath := result.ResultPath(root, res.TaskID)
	rc, err := result.Read(rcPath)
	require.NoError(t, err)
	assert.Contains(t, rc.Artefact, "custom-output/test-output.md")
}

func TestScanOutputDir_ExcludesPreExistingFiles(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	// Create output dir with a pre-existing file
	outDir := filepath.Join(root, "custom-output")
	require.NoError(t, os.MkdirAll(outDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(outDir, "pre-existing.md"), []byte("old"), 0644))

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	// Tier 1 adapter that writes one new file to the output dir
	cmd := fmt.Sprintf(`echo "new content" > %s/new-file.md`, filepath.ToSlash(outDir))
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "file-writer",
		Config:  &config.AdapterConfig{Mode: "sync", Command: cmd, OutputDir: "custom-output"},
		Tier:    1,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "JIRA-PRE", CommandFlags{})
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)
	assert.Equal(t, 1, res.ArtifactCount, "should only count the new file")
	require.Len(t, res.ArtifactPaths, 1)
	assert.Equal(t, "custom-output/new-file.md", res.ArtifactPaths[0])
}

// --- Source/TestCase redundancy tests (ENH-014 Item 2) ---

func TestBuildAdapterContext_SourceOnlyForCreate(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	// Create a Tier 2 script that echoes GTMS_REFERENCE and GTMS_TESTCASE
	scriptDir := filepath.Join(root, "testdata")
	require.NoError(t, os.MkdirAll(scriptDir, 0755))
	script := `#!/bin/bash
echo "SOURCE=${GTMS_REFERENCE} TESTCASE=${GTMS_TESTCASE}"
`
	require.NoError(t, os.WriteFile(filepath.Join(scriptDir, "source-check.sh"), []byte(script), 0755))

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	// Automate command: Reference should be empty, TestCase should be set
	resolved := &ResolvedAdapter{
		Command: "automate",
		Name:    "source-test",
		Config:  &config.AdapterConfig{Mode: "sync", Script: "testdata/source-check.sh"},
		Tier:    2,
		Mode:    "sync",
	}
	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-test1", CommandFlags{})
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)
	assert.Contains(t, res.Summary, "SOURCE= ")
	assert.Contains(t, res.Summary, "TESTCASE=tc-test1")
}

// --- BUG-006 Tests ---

// --- findTestCaseSource prefix matching tests (ENH-014 Item 5) ---

func TestFindTestCaseSource_ExactMatch(t *testing.T) {
	root := t.TempDir()
	tcDir := filepath.Join(root, "test-cases")
	require.NoError(t, os.MkdirAll(tcDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tcDir, "tc-a1b2c3d-description.md"), []byte("test"), 0644))

	result := findTestCaseSource(root, "tc-a1b2c3d")
	assert.Equal(t, "test-cases/tc-a1b2c3d-description.md", result)
}

func TestFindTestCaseSource_NoPartialMatch(t *testing.T) {
	root := t.TempDir()
	tcDir := filepath.Join(root, "test-cases")
	require.NoError(t, os.MkdirAll(tcDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tcDir, "tc-a1b2c3d-description.md"), []byte("test"), 0644))

	// tc-a1b should NOT match tc-a1b2c3d-description.md
	result := findTestCaseSource(root, "tc-a1b")
	assert.Equal(t, "", result)
}

func TestFindTestCaseSource_DotExtension(t *testing.T) {
	root := t.TempDir()
	tcDir := filepath.Join(root, "test-cases")
	require.NoError(t, os.MkdirAll(tcDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tcDir, "tc-a1b2c3d.md"), []byte("test"), 0644))

	result := findTestCaseSource(root, "tc-a1b2c3d")
	assert.Equal(t, "test-cases/tc-a1b2c3d.md", result)
}

func TestBUG006_S1_SyncZeroArtifactsWarns(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	// Adapter exits 0 with text but no <gtms-file> delimiters
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock-tier1",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `echo "just a summary, no file delimiters"`},
		Tier:    1,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "JIRA-S1", CommandFlags{})
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)
	assert.Equal(t, 0, res.ArtifactCount)
	require.True(t, len(res.Warnings) > 0, "expected warnings for zero artifacts")
	assert.Contains(t, res.Warnings[0], "0 files")
}

func TestBUG006_S1_SyncWithArtifactsNoWarning(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	// Adapter produces a file via streaming delimiter
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock-tier1",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `printf '<gtms-file name="tc-test.md">\ntest content\n</gtms-file>\n'`},
		Tier:    1,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "JIRA-S1OK", CommandFlags{})
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)
	assert.Equal(t, 1, res.ArtifactCount)
	assert.Len(t, res.ArtifactPaths, 1)
	// S1 warning should NOT be present
	for _, w := range res.Warnings {
		assert.NotContains(t, w, "0 files")
	}
}

func TestBUG006_S2_SyncEmptyStdoutWarns(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	// Adapter exits 0 with zero output
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock-tier1",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `true`},
		Tier:    1,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "JIRA-S2", CommandFlags{})
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)

	// Should have warnings for both empty stdout and zero files
	hasNoOutput := false
	for _, w := range res.Warnings {
		if assert.ObjectsAreEqual("Adapter produced no output.", w) {
			hasNoOutput = true
		}
	}
	assert.True(t, hasNoOutput, "expected 'no output' warning, got: %v", res.Warnings)
}

func TestBUG006_S3_PipelineRecordFailureWarns(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	// Create test-automation/records as a regular FILE to block MkdirAll
	recordsPath := filepath.Join(root, "test-automation", "records")
	// First remove the directory that setupTestProject may have created
	os.RemoveAll(recordsPath)
	// Write a regular file at that path so MkdirAll fails
	require.NoError(t, os.WriteFile(recordsPath, []byte("blocker"), 0644))

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	// Use automate command so buildPipelineRecords calls BuildAutomationRecord
	resolved := &ResolvedAdapter{
		Command: "automate",
		Name:    "mock-tier1",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `printf '<gtms-file name="tc-s3.spec.js">\ntest()\n</gtms-file>\n'`},
		Tier:    1,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-s3test", CommandFlags{})
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status, "task should still complete despite pipeline failure")

	hasPipelineWarning := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "Pipeline record could not be written") {
			hasPipelineWarning = true
		}
	}
	assert.True(t, hasPipelineWarning, "expected pipeline warning, got: %v", res.Warnings)
}

func TestBUG006_S7_ArtifactCountPopulated(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	// Adapter produces preamble text + 3 files via streaming delimiters
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock-tier1",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `printf 'Processing...\n<gtms-file name="a.md">\nA\n</gtms-file>\n<gtms-file name="b.md">\nB\n</gtms-file>\n<gtms-file name="c.md">\nC\n</gtms-file>\n'`},
		Tier:    1,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "JIRA-S7", CommandFlags{})
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)
	assert.Equal(t, 3, res.ArtifactCount)
	assert.Len(t, res.ArtifactPaths, 3)

	// Paths should be relative (no absolute paths)
	for _, p := range res.ArtifactPaths {
		assert.False(t, filepath.IsAbs(p), "path should be relative: %s", p)
	}

	// No S1 zero-file warning
	for _, w := range res.Warnings {
		assert.NotContains(t, w, "0 files")
	}
	// No S2 empty-output warning (preamble text provides Stdout)
	for _, w := range res.Warnings {
		assert.NotContains(t, w, "no output")
	}
}

func TestBUG006_Tier2ContractUpdatedPathPopulatesFields(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	// Create mock tier2 script that updates result file and also outputs streaming files
	scriptDir := filepath.Join(root, "testdata")
	require.NoError(t, os.MkdirAll(scriptDir, 0755))

	script := `#!/bin/bash
# Update result contract
cat > "${GTMS_RESULT_FILE}" <<EOF
task: ${GTMS_TASK_ID}
command: ${GTMS_COMMAND}
target: ${GTMS_REFERENCE}
adapter: mock-tier2
mode: sync
status: complete
artefact: test-output.md
attempts: 1
summary: "Tier2 completed with contract update"
completed: "2025-02-14T10:05:00Z"
EOF

# Also output streaming file
printf '<gtms-file name="tc-t2.md">\nTier2 content\n</gtms-file>\n'
`
	scriptPath := filepath.Join(scriptDir, "t2-stream.sh")
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0755))

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock-tier2",
		Config:  &config.AdapterConfig{Mode: "sync", Script: "testdata/t2-stream.sh"},
		Tier:    2,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "JIRA-T2F", CommandFlags{})
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)
	assert.Equal(t, 1, res.ArtifactCount)
	assert.Len(t, res.ArtifactPaths, 1)

	// S1 zero-files warning should NOT be present (Tier 2 contract path exempt)
	for _, w := range res.Warnings {
		assert.NotContains(t, w, "0 files")
	}
}

// --- BUG-013 Tests: TestCaseContent injection ---

func TestAutomate_TestCaseContentInjected(t *testing.T) {
	skipIfShort(t)
	root, cfg := setupAutomateTestProject(t)

	// Create prompt template that uses {testcase_content}
	tmplDir := filepath.Join(root, "prompts")
	require.NoError(t, os.MkdirAll(tmplDir, 0755))
	tmpl := "Automate this test case:\n\n{testcase_content}\n\nFramework: {framework}"
	require.NoError(t, os.WriteFile(filepath.Join(tmplDir, "auto.md"), []byte(tmpl), 0644))

	resolved := &ResolvedAdapter{
		Command: "automate",
		Name:    "content-test",
		Config: &config.AdapterConfig{
			Mode:           "sync",
			Command:        `echo "done"`,
			PromptTemplate: "prompts/auto.md",
		},
		Tier: 1,
		Mode: "sync",
	}

	flags := CommandFlags{Framework: "playwright"}
	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-auto", flags)
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)

	// Read the assembled prompt file from .gtms/tmp/
	matches, _ := filepath.Glob(filepath.Join(root, ".gtms", "tmp", "*-prompt.md"))
	require.Len(t, matches, 1, "expected one prompt file in .gtms/tmp/")

	content, err := os.ReadFile(matches[0])
	require.NoError(t, err)

	promptContent := string(content)
	// The test case file created by setupAutomateTestProject has "## Steps" and "Do something"
	assert.Contains(t, promptContent, "## Steps")
	assert.Contains(t, promptContent, "Do something")
	assert.Contains(t, promptContent, "Automate Test Case") // title from frontmatter
	assert.NotContains(t, promptContent, "{testcase_content}") // placeholder must be resolved
	assert.Contains(t, promptContent, "playwright") // framework substituted too
}

func TestAutomate_MissingTestCaseContentIsEmpty(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	// Create prompt template that uses {testcase_content}
	tmplDir := filepath.Join(root, "prompts")
	require.NoError(t, os.MkdirAll(tmplDir, 0755))
	tmpl := "Content: [{testcase_content}]\nID: {testcase}"
	require.NoError(t, os.WriteFile(filepath.Join(tmplDir, "auto.md"), []byte(tmpl), 0644))

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "automate",
		Name:    "content-test",
		Config: &config.AdapterConfig{
			Mode:           "sync",
			Command:        `echo "done"`,
			PromptTemplate: "prompts/auto.md",
		},
		Tier: 1,
		Mode: "sync",
	}

	// Use a test case ID with no matching file
	flags := CommandFlags{Framework: "playwright"}
	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-nonexistent", flags)
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status) // Should not fail

	// Verify prompt file has empty content (placeholder resolved to "")
	matches, _ := filepath.Glob(filepath.Join(root, ".gtms", "tmp", "*-prompt.md"))
	require.Len(t, matches, 1)

	content, err := os.ReadFile(matches[0])
	require.NoError(t, err)

	promptContent := string(content)
	assert.Contains(t, promptContent, "Content: []") // empty content
	assert.Contains(t, promptContent, "ID: tc-nonexistent") // {testcase} still holds ID
}

func TestAutomate_Tier2TestCaseContentEnvVar(t *testing.T) {
	skipIfShort(t)
	root, cfg := setupAutomateTestProject(t)

	// Create a Tier 2 script that echoes GTMS_TESTCASE_CONTENT
	scriptDir := filepath.Join(root, "testdata")
	script := `#!/bin/bash
echo "CONTENT_START=${GTMS_TESTCASE_CONTENT}CONTENT_END"
`
	require.NoError(t, os.WriteFile(filepath.Join(scriptDir, "content-check.sh"), []byte(script), 0755))

	resolved := &ResolvedAdapter{
		Command: "automate",
		Name:    "content-tier2",
		Config:  &config.AdapterConfig{Mode: "sync", Script: "testdata/content-check.sh"},
		Tier:    2,
		Mode:    "sync",
	}

	flags := CommandFlags{Framework: "playwright"}
	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-auto", flags)
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)

	// The test case file from setupAutomateTestProject contains "## Steps" and "Do something"
	assert.Contains(t, res.Summary, "CONTENT_START=")
	assert.Contains(t, res.Summary, "## Steps")
	assert.Contains(t, res.Summary, "Do something")
	assert.Contains(t, res.Summary, "CONTENT_END")
}

// --- Folder-based output dir tests (ENH-049) ---

func TestInvokeWithRoot_CreateFolderOutputDir(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock-tier1",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `echo "{output_dir}"`},
		Tier:    1,
		Mode:    "sync",
	}

	flags := CommandFlags{Folder: "login"}
	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "login", flags)
	require.NoError(t, err)

	// Folder-based output dir should be test-cases/<folder>
	expectedDir := filepath.Join(root, "test-cases", "login")
	assert.Contains(t, res.Summary, expectedDir, "Folder-based output dir should be test-cases/<folder>")
}

func TestInvokeWithRoot_CreateConfigOutputDirOverridesFolder(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock-tier1",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `echo "{output_dir}"`, OutputDir: "custom/output"},
		Tier:    1,
		Mode:    "sync",
	}

	flags := CommandFlags{Folder: "login"}
	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "login", flags)
	require.NoError(t, err)

	// Config OutputDir should take precedence over folder
	expectedDir := filepath.Join(root, "custom", "output")
	assert.Contains(t, res.Summary, expectedDir, "Config OutputDir should override folder-based output dir")
}

func TestInvokeWithRoot_CreateEmptyFolder(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock-tier1",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `echo "{output_dir}"`},
		Tier:    1,
		Mode:    "sync",
	}

	flags := CommandFlags{Folder: ""}
	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "test-target", flags)
	require.NoError(t, err)

	// Empty folder falls back to test-cases/
	expectedDir := filepath.Join(root, "test-cases")
	assert.Contains(t, res.Summary, expectedDir, "Default should be test-cases/ when folder is empty")
}

func TestInvokeWithRoot_CreateReferenceFlag(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock-tier1",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `echo "{reference}"`},
		Tier:    1,
		Mode:    "sync",
	}

	flags := CommandFlags{Folder: "bug-022", Reference: "BUG-022"}
	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "bug-022", flags)
	require.NoError(t, err)

	assert.Contains(t, res.Summary, "BUG-022", "Reference flag value should be passed to adapter")
}

// --- BUG-016: Streaming summary tests ---

func TestBUG016_StreamingSummaryGTMSGenerated(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	// Adapter outputs misleading preamble + files via streaming (simulates --allowedTools "" behaviour)
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock-tier1",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `printf 'The file write was denied. Here is the generated test output:\n<gtms-file name="tc-login.md">\nLogin test\n</gtms-file>\n<gtms-file name="tc-logout.md">\nLogout test\n</gtms-file>\n'`},
		Tier:    1,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "JIRA-BUG16", CommandFlags{})
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)

	// Summary should be GTMS-generated, NOT the misleading adapter narration
	assert.Contains(t, res.Summary, "Captured 2 file(s)")
	assert.Contains(t, res.Summary, "tc-login.md")
	assert.Contains(t, res.Summary, "tc-logout.md")
	assert.NotContains(t, res.Summary, "file write was denied")

	// Verify result contract has GTMS summary and adapter narration in log
	rc, rcErr := result.Read(result.ResultPath(root, res.TaskID))
	require.NoError(t, rcErr)
	assert.Contains(t, rc.Summary, "Captured 2 file(s)")
	assert.Contains(t, rc.Log, "file write was denied")
}

func TestBUG016_NonStreamingSummaryUnchanged(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	// Adapter outputs plain text (no streaming delimiters)
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock-tier1",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `echo "plain adapter output, no files"`},
		Tier:    1,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "JIRA-BUG16B", CommandFlags{})
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)

	// Summary should still contain the raw adapter output (unchanged behaviour)
	assert.Contains(t, res.Summary, "plain adapter output")
	assert.NotContains(t, res.Summary, "Captured")
}

func TestBUG016_StreamingEmptyPreambleNoLog(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	// Adapter outputs only streaming files (no preamble/narration)
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock-tier1",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `printf '<gtms-file name="tc-only.md">\ntest\n</gtms-file>\n'`},
		Tier:    1,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "JIRA-BUG16C", CommandFlags{})
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)
	assert.Contains(t, res.Summary, "Captured 1 file(s)")

	// Log should be empty when there's no preamble text
	rc, rcErr := result.Read(result.ResultPath(root, res.TaskID))
	require.NoError(t, rcErr)
	assert.Empty(t, rc.Log)
}

// --- buildStreamingSummary unit tests (pure logic, no shell needed) ---

func TestBuildStreamingSummary_SingleFile(t *testing.T) {
	result := buildStreamingSummary([]string{"/tmp/output/tc-login.md"})
	assert.Equal(t, "Captured 1 file(s): tc-login.md", result)
}

func TestBuildStreamingSummary_MultipleFiles(t *testing.T) {
	result := buildStreamingSummary([]string{
		"/tmp/output/tc-login.md",
		"/tmp/output/tc-logout.md",
		"/tmp/output/tc-signup.md",
	})
	assert.Equal(t, "Captured 3 file(s): tc-login.md, tc-logout.md, tc-signup.md", result)
}

func TestBuildStreamingSummary_TruncatesAfterFive(t *testing.T) {
	files := []string{
		"/tmp/a.md", "/tmp/b.md", "/tmp/c.md",
		"/tmp/d.md", "/tmp/e.md", "/tmp/f.md",
		"/tmp/g.md", "/tmp/h.md",
	}
	result := buildStreamingSummary(files)
	assert.Contains(t, result, "Captured 8 file(s)")
	assert.Contains(t, result, "a.md, b.md, c.md, d.md, e.md")
	assert.Contains(t, result, "... (3 more)")
	assert.NotContains(t, result, "f.md")
}

func TestBuildStreamingSummary_Empty(t *testing.T) {
	result := buildStreamingSummary(nil)
	assert.Equal(t, "", result)
}

// --- deriveOutputSubdir tests (pure logic, no shell needed) ---

func TestDeriveOutputSubdir_EmptyString(t *testing.T) {
	result := deriveOutputSubdir("")
	assert.Equal(t, "", result)
}

func TestDeriveOutputSubdir_RootLevel(t *testing.T) {
	result := deriveOutputSubdir("test-cases/tc-abc123.md")
	assert.Equal(t, "", result)
}

func TestDeriveOutputSubdir_SingleSubdir(t *testing.T) {
	result := deriveOutputSubdir("test-cases/cwd-scoping/tc-abc123.md")
	assert.Equal(t, "cwd-scoping/", result)
}

func TestDeriveOutputSubdir_NestedSubdir(t *testing.T) {
	result := deriveOutputSubdir("test-cases/auth/login/tc-abc123.md")
	assert.Equal(t, "auth/login/", result)
}

func TestDeriveOutputSubdir_DeeplyNested(t *testing.T) {
	result := deriveOutputSubdir("test-cases/a/b/c/tc-xyz.md")
	assert.Equal(t, "a/b/c/", result)
}

// --- ENH-042: Test case ID generation tests ---

func TestBuildAdapterContext_SetsTestCaseIDs_ForCreate(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock",
		Config:  &config.AdapterConfig{Mode: "sync", Command: "echo ok"},
		Tier:    1,
		Mode:    "sync",
	}
	flags := CommandFlags{}

	ac := buildAdapterContext(root, "task-test123", resolved, "REQ-001", flags, "feature/test", cfg, root, "/tmp/result.yaml")

	// TestCaseIDs should be populated
	require.NotEmpty(t, ac.TestCaseIDs, "TestCaseIDs should be set for create command")

	ids := strings.Split(ac.TestCaseIDs, ",")
	assert.Len(t, ids, 20, "should generate batch of 20 IDs")

	// Each ID should match tc-{7hex} pattern
	for _, id := range ids {
		assert.Regexp(t, `^tc-[0-9a-f]{7,8}$`, id, "each ID should match tc-{7hex} pattern")
	}

	// All IDs should be unique
	seen := make(map[string]bool)
	for _, id := range ids {
		assert.False(t, seen[id], "duplicate ID found: %s", id)
		seen[id] = true
	}
}

func TestBuildAdapterContext_NoTestCaseIDs_ForAutomate(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}
	resolved := &ResolvedAdapter{
		Command: "automate",
		Name:    "mock",
		Config:  &config.AdapterConfig{Mode: "sync", Command: "echo ok"},
		Tier:    1,
		Mode:    "sync",
	}
	flags := CommandFlags{}

	ac := buildAdapterContext(root, "task-test456", resolved, "tc-abc1234", flags, "feature/test", cfg, root, "/tmp/result.yaml")
	assert.Empty(t, ac.TestCaseIDs, "TestCaseIDs should be empty for automate command")
}

func TestBuildAdapterContext_NoTestCaseIDs_ForExecute(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}
	resolved := &ResolvedAdapter{
		Command: "execute",
		Name:    "mock",
		Config:  &config.AdapterConfig{Mode: "sync", Command: "echo ok"},
		Tier:    1,
		Mode:    "sync",
	}
	flags := CommandFlags{}

	ac := buildAdapterContext(root, "task-test789", resolved, "tc-def5678", flags, "feature/test", cfg, root, "/tmp/result.yaml")
	assert.Empty(t, ac.TestCaseIDs, "TestCaseIDs should be empty for execute command")
}
