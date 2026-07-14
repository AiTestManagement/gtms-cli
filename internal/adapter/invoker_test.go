package adapter

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/prompt"
	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/aitestmanagement/gtms-cli/internal/task"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// setupTestProject creates a minimal project structure for invoker tests.
func setupTestProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	// Create required directories
	for _, dir := range []string{
		"gtms/tasks/pending", "gtms/tasks/complete", "gtms/tasks/error",
		"gtms/tasks/in-progress", "gtms/tasks/in-review",
		"gtms/test/cases", "gtms/automation",
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
	// BUG-056: sync adapter in non-git temp dir → empty branch (no fake feature/ name)
	assert.Equal(t, "", res.Branch)

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
// from in-progress to error (not from pending to error), confirming the in-progress
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
// moves the task from in-progress to error (not directly from pending).
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

	// Verify task ended in error
	failedTasks, err := task.List(root, "error")
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
result: pass
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

// BUG-063: A Tier 2 create adapter that writes its file directly to
// GTMS_OUTPUT_DIR and reports it via the contract's artefact: field
// (not via <gtms-file> streaming markers) must still surface the file
// in InvokeResult.ArtifactPaths so the CLI's printCreatedHeadline can
// fire. Mirrors the symmetrical fallback in the exit-code branch.
func TestBUG063_Tier2ContractUpdate_ScansOutputDirWhenStreamingEmpty(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	// Mock skeleton-style Tier 2: write file directly under gtms/test/cases/<folder>/,
	// no <gtms-file> markers in stdout, contract updated to status: complete.
	scriptPath := filepath.Join(root, "testdata", "mock-skeleton.sh")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "testdata"), 0755))

	// Note: script intentionally does not write the `artefact:` line into
	// the contract. The whole point of the BUG-063 fix is that GTMS must
	// discover the file by scanning GTMS_OUTPUT_DIR even when the contract
	// doesn't name it. Quoting an absolute Windows path with `C:` into
	// YAML's `artefact:` field would also fail to parse, but the fix is
	// independent of that.
	// Use the first ID from the pre-generated batch (ENH-042) so the
	// BUG-038 post-write spec validator accepts the file.
	script := `#!/bin/sh
mkdir -p "${GTMS_OUTPUT_DIR}"
ID=$(echo "${GTMS_TC_IDS}" | cut -d',' -f1)
OUTFILE="${GTMS_OUTPUT_DIR}/${ID}-direct-write.md"
cat > "${OUTFILE}" <<TCEOF
---
test_case_id: ${ID}
name: "direct-write"
---
TCEOF
cat > "${GTMS_RESULT_FILE}" <<EOF
task: ${GTMS_TASK_ID}
command: create
target: ${GTMS_REFERENCE}
adapter: mock-skeleton
mode: sync
status: complete
result: pass
attempts: 1
summary: "Mock skeleton wrote one file directly"
completed: "2026-05-03T10:00:00Z"
EOF
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0755))

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock-skeleton",
		Config:  &config.AdapterConfig{Mode: "sync", Script: "testdata/mock-skeleton.sh"},
		Tier:    2,
		Mode:    "sync",
	}

	flags := CommandFlags{Folder: "demo-folder"}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "demo-folder", flags)
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)

	// The fix: ArtifactPaths must be populated even though SavedFiles was empty.
	require.NotEmpty(t, res.ArtifactPaths,
		"BUG-063: ArtifactPaths should be populated via scanOutputDir fallback when streaming captured nothing")
	assert.Equal(t, 1, res.ArtifactCount)

	// The single discovered path should match the file the mock wrote.
	require.Len(t, res.ArtifactPaths, 1)
	assert.Contains(t, res.ArtifactPaths[0], "-direct-write.md",
		"discovered path should include the slug from the script")
	// Forward-slash relative path under gtms/test/cases/.
	assert.True(t, strings.HasPrefix(res.ArtifactPaths[0], "gtms/test/cases/"),
		"path should be relative and forward-slashed: got %q", res.ArtifactPaths[0])
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

	// Verify task moved to error
	failedTasks, err := task.List(root, "error")
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
	// BUG-056: sync adapter in non-git temp dir → empty branch
	assert.Equal(t, "", tf.Branch)
	assert.NotEmpty(t, tf.Created)
}

func TestInvokeWithRoot_GuidesFlow(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	// Create guide-dir with a guide file
	guideDir := filepath.Join(root, "gtms/test", "guides")
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
		Config:  &config.AdapterConfig{Mode: "sync", Command: `echo "ok"`, GuideDir: "gtms/test/guides/"},
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

// --- BUG-056: taskBranch mode-aware branch resolution ---

// TestBUG056_SyncAdapterRecordsEmptyBranchInNonGitDir verifies that sync adapters
// in a non-git temp directory record an empty branch (not a fake feature/ name).
func TestBUG056_SyncAdapterRecordsEmptyBranchInNonGitDir(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock-tier1",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `echo "output"`},
		Tier:    1,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "BUG056-SYNC", CommandFlags{})
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)
	// Non-git temp dir → empty branch for sync adapter
	assert.Equal(t, "", res.Branch, "sync adapter in non-git dir should have empty branch")

	// Verify task file also has empty branch
	completeTasks, err := task.List(root, "complete")
	require.NoError(t, err)
	require.Len(t, completeTasks, 1)
	assert.Equal(t, "", completeTasks[0].Branch, "task file branch should be empty for sync in non-git dir")
}

// TestBUG056_SyncAdapterRecordsRealBranchInGitRepo verifies that sync adapters
// in a real git repository record the actual current branch name.
func TestBUG056_SyncAdapterRecordsRealBranchInGitRepo(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	// Initialize a git repo with an initial commit so HEAD is valid
	gitInit(t, root)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock-tier1",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `echo "output"`},
		Tier:    1,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "BUG056-GIT", CommandFlags{})
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)
	// In a git repo, sync adapter should record the real branch (not feature/)
	assert.NotEqual(t, "", res.Branch, "sync adapter in git repo should have non-empty branch")
	assert.NotContains(t, res.Branch, "feature/", "sync adapter should NOT have constructed feature/ branch")

	// Verify task file has the same real branch
	completeTasks, err := task.List(root, "complete")
	require.NoError(t, err)
	require.Len(t, completeTasks, 1)
	assert.Equal(t, res.Branch, completeTasks[0].Branch, "task file branch should match InvokeResult branch")
}

// TestBUG056_AsyncAdapterRetainsConstructedBranch verifies that async adapters
// still get the constructed feature/{command}-{target} branch name.
func TestBUG056_AsyncAdapterRetainsConstructedBranch(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock-async",
		Config:  &config.AdapterConfig{Mode: "async", Command: `echo "async output"`},
		Tier:    1,
		Mode:    "async",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "BUG056-ASYNC", CommandFlags{})
	require.NoError(t, err)
	assert.Equal(t, "in-progress", res.Status)
	// Async adapter should retain constructed branch
	assert.Equal(t, "feature/create-BUG056-ASYNC", res.Branch, "async adapter should have constructed feature/ branch")
}

// TestBUG056_TaskBranchHelper_UnitTests exercises the taskBranch helper directly.
func TestBUG056_TaskBranchHelper_UnitTests(t *testing.T) {
	t.Run("sync_mode_non_git_dir", func(t *testing.T) {
		resolved := &ResolvedAdapter{Command: "create", Mode: "sync"}
		branch := taskBranch(context.Background(), t.TempDir(), resolved, "TARGET-1")
		assert.Equal(t, "", branch, "sync in non-git dir → empty")
	})

	t.Run("async_mode_constructs_feature_branch", func(t *testing.T) {
		resolved := &ResolvedAdapter{Command: "automate", Mode: "async"}
		branch := taskBranch(context.Background(), t.TempDir(), resolved, "tc-abc123")
		assert.Equal(t, "feature/automate-tc-abc123", branch)
	})

	t.Run("async_mode_sanitizes_target", func(t *testing.T) {
		resolved := &ResolvedAdapter{Command: "create", Mode: "async"}
		branch := taskBranch(context.Background(), t.TempDir(), resolved, "path/to/file.md")
		assert.Equal(t, "feature/create-path-to-file", branch)
	})
}

// gitInit initializes a git repo with an initial commit in the given directory.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	cmds := []struct {
		args []string
	}{
		{[]string{"init"}},
		{[]string{"config", "user.email", "test@test.com"}},
		{[]string{"config", "user.name", "Test"}},
		{[]string{"add", "."}},
		{[]string{"commit", "--allow-empty", "-m", "initial commit"}},
	}
	for _, c := range cmds {
		cmd := exec.Command("git", c.args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v failed: %s", c.args, string(out))
	}
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

	// Verify task file moved to error
	failedTasks, listErr := task.List(root, "error")
	require.NoError(t, listErr)
	assert.Len(t, failedTasks, 1)
	assert.Equal(t, "JIRA-TIMEOUT", failedTasks[0].Target)

	// Verify no task left in pending
	pendingTasks, listErr := task.List(root, "pending")
	require.NoError(t, listErr)
	assert.Len(t, pendingTasks, 0)

	// Verify result contract status is error
	// Find the task ID from the error task
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
	// Both paths correctly move the task to "error".
	if err != nil {
		// Path A: cancellation detected by invoker
		assert.Contains(t, err.Error(), "cancelled")
	} else {
		// Path B: normal error handling caught the killed process
		require.NotNil(t, res)
		assert.Equal(t, "error", res.Status)
	}

	// Key invariant: task file is in "error", not left in "pending"
	failedTasks, listErr := task.List(root, "error")
	require.NoError(t, listErr)
	assert.Len(t, failedTasks, 1)
	assert.Equal(t, "JIRA-CANCEL", failedTasks[0].Target)

	pendingTasks, listErr := task.List(root, "pending")
	require.NoError(t, listErr)
	assert.Len(t, pendingTasks, 0)
}

// TestInvokeWithRoot_Tier1_FailExitCodes covers ENH-078: an opt-in
// fail-exit-codes list on a Tier 1 adapter maps listed non-zero exit codes
// to status: fail. Codes outside the list — and any non-zero exit when the
// list is unset — still produce status: error. Exit 0 is unaffected.
func TestInvokeWithRoot_Tier1_FailExitCodes(t *testing.T) {
	skipIfShort(t)

	cases := []struct {
		name           string
		failExitCodes  []int
		exitCode       int
		expectStatus   string // result-contract status field
		expectResult   string // result-contract result field (ENH-130)
		expectInvoke   string // InvokeResult.Status
		expectTaskDir  string // "complete" or "error"
	}{
		{
			name:          "unset list — exit 1 still produces error",
			failExitCodes: nil,
			exitCode:      1,
			expectStatus:  "error",
			expectResult:  "",
			expectInvoke:  "error",
			expectTaskDir: "error",
		},
		{
			name:          "list [1] — exit 1 produces complete+fail",
			failExitCodes: []int{1},
			exitCode:      1,
			expectStatus:  "complete",
			expectResult:  "fail",
			expectInvoke:  "complete",
			expectTaskDir: "complete", // ENH-130: clean adapter run, task to complete/
		},
		{
			name:          "list [1] — exit 127 produces error",
			failExitCodes: []int{1},
			exitCode:      127,
			expectStatus:  "error",
			expectResult:  "",
			expectInvoke:  "error",
			expectTaskDir: "error",
		},
		{
			name:          "list [1] — exit 0 produces complete+pass",
			failExitCodes: []int{1},
			exitCode:      0,
			expectStatus:  "complete",
			expectResult:  "pass",
			expectInvoke:  "complete",
			expectTaskDir: "complete",
		},
		{
			name:          "list [1, 2, 3] — exit 2 produces complete+fail",
			failExitCodes: []int{1, 2, 3},
			exitCode:      2,
			expectStatus:  "complete",
			expectResult:  "fail",
			expectInvoke:  "complete",
			expectTaskDir: "complete", // ENH-130: clean adapter run, task to complete/
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := setupTestProject(t)

			cfg := &config.Config{
				Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
			}

			// Use sh -c so the test controls the exit code precisely. The
			// invoker wraps Tier 1 commands in sh -c on Unix / cmd /c on
			// Windows; either resolves "exit N" the same way.
			resolved := &ResolvedAdapter{
				Command: "create",
				Name:    "mock-tier1",
				Config: &config.AdapterConfig{
					Mode:          "sync",
					Command:       fmt.Sprintf("exit %d", tc.exitCode),
					FailExitCodes: tc.failExitCodes,
				},
				Tier: 1,
				Mode: "sync",
			}

			res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "ENH-078", CommandFlags{})
			require.NoError(t, err)
			require.NotNil(t, res)

			// Verify InvokeResult.Status reflects the contract status
			assert.Equal(t, tc.expectInvoke, res.Status,
				"InvokeResult.Status should match contract status for exit code %d", tc.exitCode)

			// Verify result-contract status and result fields on disk
			rcPath := result.ResultPath(root, res.TaskID)
			rc, rcErr := result.Read(rcPath)
			require.NoError(t, rcErr)
			assert.Equal(t, tc.expectStatus, rc.Status,
				"contract status field should be %s for exit code %d with fail-exit-codes=%v",
				tc.expectStatus, tc.exitCode, tc.failExitCodes)
			assert.Equal(t, tc.expectResult, rc.Result,
				"contract result field should be %q for exit code %d with fail-exit-codes=%v",
				tc.expectResult, tc.exitCode, tc.failExitCodes)

			// Verify task moved to expected directory
			tasks, listErr := task.List(root, tc.expectTaskDir)
			require.NoError(t, listErr)
			assert.Len(t, tasks, 1, "expected exactly one task in %s/", tc.expectTaskDir)
			if len(tasks) > 0 {
				assert.Equal(t, "ENH-078", tasks[0].Target)
			}
		})
	}
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
	assert.Contains(t, res.Summary, filepath.Join(root, "gtms/test/cases"))
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
	assert.Contains(t, res.Summary, filepath.Join(root, "gtms/automation", "specs", "my-auto"))
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
	tcDir := filepath.Join(root, "gtms/test/cases")
	require.NoError(t, os.MkdirAll(tcDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tcDir, "tc-a1b2c3d-description.md"), []byte("test"), 0644))

	result := findTestCaseSource(root, "tc-a1b2c3d")
	assert.Equal(t, "gtms/test/cases/tc-a1b2c3d-description.md", result)
}

func TestFindTestCaseSource_NoPartialMatch(t *testing.T) {
	root := t.TempDir()
	tcDir := filepath.Join(root, "gtms/test/cases")
	require.NoError(t, os.MkdirAll(tcDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tcDir, "tc-a1b2c3d-description.md"), []byte("test"), 0644))

	// tc-a1b should NOT match tc-a1b2c3d-description.md
	result := findTestCaseSource(root, "tc-a1b")
	assert.Equal(t, "", result)
}

func TestFindTestCaseSource_DotExtension(t *testing.T) {
	root := t.TempDir()
	tcDir := filepath.Join(root, "gtms/test/cases")
	require.NoError(t, os.MkdirAll(tcDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tcDir, "tc-a1b2c3d.md"), []byte("test"), 0644))

	result := findTestCaseSource(root, "tc-a1b2c3d")
	assert.Equal(t, "gtms/test/cases/tc-a1b2c3d.md", result)
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

	// CON-023 / ENH-145: block the wiring directory (was records/). We
	// create gtms/automation/wiring as a regular FILE so MkdirAll fails
	// inside WriteAutomateWiring, exercising the same "pipeline write
	// failure produces a warning, doesn't block the task" path.
	wiringPath := filepath.Join(root, "gtms/automation", "wiring")
	os.RemoveAll(wiringPath)
	require.NoError(t, os.WriteFile(wiringPath, []byte("blocker"), 0644))

	// Seed a TC spec so the wiring writer gets past the spec-resolution
	// step and reaches the MkdirAll on the (blocked) wiring directory.
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "gtms/test/cases/tc-s3test-spec.md"),
		[]byte("---\nid: tc-s3test\n---\nbody\n"),
		0644,
	))

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"playwright-runner": {Framework: "playwright", Mode: "sync", Command: "echo ok"},
			},
		},
		Defaults: map[string]string{"execute": "playwright-runner"},
	}

	// Automate command. Framework is explicit so the wiring writer doesn't
	// short-circuit on missing framework — we want it to fail at MkdirAll.
	resolved := &ResolvedAdapter{
		Command: "automate",
		Name:    "mock-tier1",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `printf '<gtms-file name="tc-s3test.spec.js">\ntest()\n</gtms-file>\n'`, Framework: "playwright"},
		Tier:    1,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-s3test", CommandFlags{Framework: "playwright"})
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
result: pass
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

	// Folder-based output dir should be gtms/test/cases/<folder>
	expectedDir := filepath.Join(root, "gtms/test/cases", "login")
	assert.Contains(t, res.Summary, expectedDir, "Folder-based output dir should be gtms/test/cases/<folder>")
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

	// Empty folder falls back to gtms/test/cases/
	expectedDir := filepath.Join(root, "gtms/test/cases")
	assert.Contains(t, res.Summary, expectedDir, "Default should be gtms/test/cases/ when folder is empty")
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
	result := deriveOutputSubdir("gtms/test/cases/tc-abc123.md")
	assert.Equal(t, "", result)
}

func TestDeriveOutputSubdir_SingleSubdir(t *testing.T) {
	result := deriveOutputSubdir("gtms/test/cases/cwd-scoping/tc-abc123.md")
	assert.Equal(t, "cwd-scoping/", result)
}

func TestDeriveOutputSubdir_NestedSubdir(t *testing.T) {
	result := deriveOutputSubdir("gtms/test/cases/auth/login/tc-abc123.md")
	assert.Equal(t, "auth/login/", result)
}

func TestDeriveOutputSubdir_DeeplyNested(t *testing.T) {
	result := deriveOutputSubdir("gtms/test/cases/a/b/c/tc-xyz.md")
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

	// Each ID should match tc-{8hex} pattern
	for _, id := range ids {
		assert.Regexp(t, `^tc-[0-9a-f]{8}$`, id, "each ID should match tc-{8hex} pattern")
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

// --- BUG-023: Failed execute results propagated to automation records ---
// TestBUG023_FailedExecuteUpdatesPipelineRecord is in execute_acid_test.go

func TestBUG023_PassAfterFailUpdatesPipelineRecord(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	// First: execute that fails
	resolved := &ResolvedAdapter{
		Command: "execute",
		Name:    "fail-runner",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `sh -c "exit 1"`, Framework: "bats"},
		Tier:    1,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-bug023b", CommandFlags{})
	require.NoError(t, err)
	assert.Equal(t, "error", res.Status)

	// CON-023 / ENH-146: assert against the result contract — the legacy
	// .automation.md update is retired on the execute path.
	failRC, err := result.Read(result.ResultPath(root, res.TaskID))
	require.NoError(t, err)
	assert.Equal(t, "error", failRC.Status)

	// Second: execute that succeeds
	resolved.Config.Command = `echo "all tests passed"`
	res, err = InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-bug023b", CommandFlags{})
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)

	passRC, err := result.Read(result.ResultPath(root, res.TaskID))
	require.NoError(t, err)
	assert.Equal(t, "complete", passRC.Status)
	assert.Equal(t, "pass", passRC.Result, "successful execute after failure should set rc.Result to pass")
}

// --- ENH-080: Multi-file automate output must be rejected at automate time ---

// TestENH080_AutomateMultiFileFails_Tier1 verifies that a Tier 1 sync automate
// adapter emitting two <gtms-file> tags for a single TC fails the task,
// writes NO automation record, and surfaces a recognisable error summary.
func TestENH080_AutomateMultiFileFails_Tier1(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	// Mock Tier 1 adapter emitting TWO <gtms-file> tags for one automate run.
	// This matches the AI-defect pattern from tc-4abe0420 / tc-4043bc4f.
	multiFileCmd := `printf '<gtms-file name="tc-enh080-primary.bats">\n#!/usr/bin/env bats\n@test "main" { true; }\n</gtms-file>\n<gtms-file name="tc-enh080-extra.bats">\n#!/usr/bin/env bats\n@test "extra" { true; }\n</gtms-file>\n'`

	resolved := &ResolvedAdapter{
		Command: "automate",
		Name:    "multi-file-mock",
		Config:  &config.AdapterConfig{Mode: "sync", Command: multiFileCmd, Framework: "bats"},
		Tier:    1,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-enh080", CommandFlags{})
	require.NoError(t, err)

	// Task result: error status (not complete)
	assert.Equal(t, "error", res.Status, "multi-file automate must be rejected as error")
	assert.Contains(t, res.Summary, "automate emitted 2 files",
		"summary should name the command and file count")
	assert.Contains(t, res.Summary, "exactly one is expected",
		"summary should state the expectation")
	assert.Contains(t, res.Summary, "tc-enh080-primary.bats",
		"summary should list the captured filenames for user diagnosis")
	assert.Contains(t, res.Summary, "tc-enh080-extra.bats")

	// Task file must live in gtms/tasks/error/
	failedTasks, err := task.List(root, "error")
	require.NoError(t, err)
	assert.Len(t, failedTasks, 1, "task file should be in gtms/tasks/error/")
	assert.Equal(t, "tc-enh080", failedTasks[0].Target)

	// Result contract: status=error, summary matches
	rcPath := result.ResultPath(root, res.TaskID)
	rc, err := result.Read(rcPath)
	require.NoError(t, err)
	assert.Equal(t, "error", rc.Status)
	assert.Contains(t, rc.Summary, "exactly one is expected")

	// NO wiring record should have been written. This is the whole point
	// of ENH-080 — a comma-separated artefact: field never ships in a record
	// GTMS writes. (CON-023 retargeted records → wiring; the guard fires
	// before buildPipelineRecords either way.)
	wiringDir := filepath.Join(root, "gtms/automation", "wiring")
	if entries, statErr := os.ReadDir(wiringDir); statErr == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.Contains(e.Name(), "tc-enh080") {
				t.Errorf("wiring record should not have been written: %s", e.Name())
			}
		}
	}
}

// TestENH080_AutomateMultiFileFails_Tier2ScriptContract verifies the guard
// also fires on the Tier 2 script-updated-contract branch. Even if the Tier 2
// script reports status=complete in its result contract, GTMS must override
// to error when the streaming writer captured multiple files.
func TestENH080_AutomateMultiFileFails_Tier2ScriptContract(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	// Build a Tier 2 mock script that emits two <gtms-file> tags AND writes
	// status=complete to the result contract. The guard must override.
	scriptPath := filepath.Join(root, "mock-tier2-multifile.sh")
	script := `#!/bin/bash
printf '<gtms-file name="tc-enh080b-primary.bats">\n#!/usr/bin/env bats\n@test "main" { true; }\n</gtms-file>\n'
printf '<gtms-file name="tc-enh080b-extra.bats">\n#!/usr/bin/env bats\n@test "extra" { true; }\n</gtms-file>\n'
cat > "${GTMS_RESULT_FILE}" <<EOF
task: ${GTMS_TASK_ID}
command: ${GTMS_COMMAND}
target: ${GTMS_TESTCASE}
adapter: multi-file-tier2-mock
mode: sync
created: "2026-04-16T00:00:00Z"
status: complete
result: pass
artefact: tc-enh080b-primary.bats,tc-enh080b-extra.bats
attempts: 1
summary: "adapter says all good"
completed: "2026-04-16T00:00:00Z"
EOF
exit 0
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0755))

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "automate",
		Name:    "multi-file-tier2-mock",
		Config:  &config.AdapterConfig{Mode: "sync", Script: "mock-tier2-multifile.sh", Framework: "bats"},
		Tier:    2,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-enh080b", CommandFlags{})
	require.NoError(t, err)

	// Guard must override the script's status=complete.
	assert.Equal(t, "error", res.Status, "guard must override tier 2 script status=complete")
	assert.Contains(t, res.Summary, "automate emitted 2 files")

	// Task moved to error
	failedTasks, err := task.List(root, "error")
	require.NoError(t, err)
	assert.Len(t, failedTasks, 1)
	assert.Equal(t, "tc-enh080b", failedTasks[0].Target)

	// Result contract status must be error (not complete)
	rcPath := result.ResultPath(root, res.TaskID)
	rc, err := result.Read(rcPath)
	require.NoError(t, err)
	assert.Equal(t, "error", rc.Status)

	// No wiring record (CON-023).
	wiringDir := filepath.Join(root, "gtms/automation", "wiring")
	if entries, statErr := os.ReadDir(wiringDir); statErr == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.Contains(e.Name(), "tc-enh080b") {
				t.Errorf("wiring record should not have been written: %s", e.Name())
			}
		}
	}
}

// TestENH080_AutomateSingleFileStillWorks is the regression check that the
// guard fires ONLY for multi-file output. A single <gtms-file> tag must
// still produce a clean complete + automation record.
func TestENH080_AutomateSingleFileStillWorks(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	// Seed a TC spec so testcase-hash can be computed.
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "gtms/test/cases/tc-enh080c-spec.md"),
		[]byte("---\nid: tc-enh080c\n---\nbody\n"),
		0644,
	))

	// CON-023 / ENH-145: cfg must have a bats execute adapter so the
	// wiring writer can resolve the canonical execute adapter.
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"bats-runner": {Framework: "bats", Mode: "sync", Script: "ignored"},
			},
		},
		Defaults: map[string]string{"execute": "bats-runner"},
	}

	singleFileCmd := `printf '<gtms-file name="tc-enh080c-ok.bats">\n#!/usr/bin/env bats\n@test "ok" { true; }\n</gtms-file>\n'`

	resolved := &ResolvedAdapter{
		Command: "automate",
		Name:    "single-file-mock",
		Config:  &config.AdapterConfig{Mode: "sync", Command: singleFileCmd, Framework: "bats"},
		Tier:    1,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-enh080c", CommandFlags{})
	require.NoError(t, err)

	assert.Equal(t, "complete", res.Status, "single-file automate must still succeed")
	assert.Equal(t, 1, res.ArtifactCount)

	// Wiring record should exist (CON-023 cutover; single-file path is unchanged).
	rec, _, err := wiring.Find(root, "tc-enh080c", "bats")
	require.NoError(t, err)
	require.NotNil(t, rec, "wiring record should be written for single-file automate")
	assert.Equal(t, "bats-runner", rec.Adapter)
}

// TestENH080_CreateMultiFileStillWorks verifies that the guard is automate-only.
// `create` legitimately emits many <gtms-file> tags (one per test case) and
// must continue to work unchanged.
func TestENH080_CreateMultiFileStillWorks(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	multiFileCmd := `printf '<gtms-file name="tc-enh080d-one.md">\none\n</gtms-file>\n<gtms-file name="tc-enh080d-two.md">\ntwo\n</gtms-file>\n<gtms-file name="tc-enh080d-three.md">\nthree\n</gtms-file>\n'`

	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "create-many-mock",
		Config:  &config.AdapterConfig{Mode: "sync", Command: multiFileCmd},
		Tier:    1,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "ENH-REF", CommandFlags{Folder: "enh-ref"})
	require.NoError(t, err)

	assert.Equal(t, "complete", res.Status, "create with many files must still succeed — guard is automate-only")
	assert.Equal(t, 3, res.ArtifactCount, "all three streamed files must be captured")
}

// --- ENH-077: buildFailureLog + Tier 1 log-carry tests ---

func TestBuildFailureLog_BothEmpty(t *testing.T) {
	assert.Equal(t, "", buildFailureLog("", ""))
	assert.Equal(t, "", buildFailureLog("\n\n", "\n"))
}

func TestBuildFailureLog_StdoutOnly(t *testing.T) {
	got := buildFailureLog("line 1\nline 2\n", "")
	assert.Equal(t, "line 1\nline 2", got,
		"trailing newline must be trimmed but internal newlines preserved")
}

func TestBuildFailureLog_StderrOnly(t *testing.T) {
	got := buildFailureLog("", "error on line 3\n")
	assert.Equal(t, "error on line 3", got)
}

func TestBuildFailureLog_BothStreams_StderrLeads(t *testing.T) {
	got := buildFailureLog("stdout body\n", "stderr body\n")
	assert.Equal(t, "stderr:\nstderr body\n\nstdout:\nstdout body", got,
		"when both streams have content, stderr should lead so the error line appears first")
}

// TestInvokeWithRoot_Tier1_FailureCarriesLog covers ENH-077: when a Tier 1
// execute adapter exits non-zero, the captured stdout / stderr should land
// in the result contract's log: field. Previously the invoker only wrote
// log: on the success-with-streaming-files branch, so a failed run left
// the field empty and diagnostic context was lost on `rm -rf .gtms/`.
func TestInvokeWithRoot_Tier1_FailureCarriesLog(t *testing.T) {
	skipIfShort(t)

	cases := []struct {
		name          string
		failExitCodes []int
		exitCode      int
		expectStatus  string
	}{
		{
			name:         "exit 1 no fail-exit-codes — error carries log",
			exitCode:     1,
			expectStatus: "error",
		},
		{
			name:          "exit 1 with fail-exit-codes [1] — complete+fail carries log",
			failExitCodes: []int{1},
			exitCode:      1,
			expectStatus:  "complete", // ENH-130: fail-exit-code → complete
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := setupTestProject(t)
			cfg := &config.Config{
				Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
			}

			// The command emits recognisable strings to both streams before
			// exiting with the target code.
			cmd := fmt.Sprintf(`echo "ENH077_STDOUT_LINE"; echo "ENH077_STDERR_LINE" >&2; exit %d`, tc.exitCode)
			resolved := &ResolvedAdapter{
				Command: "create",
				Name:    "mock-tier1-fail",
				Config: &config.AdapterConfig{
					Mode:          "sync",
					Command:       cmd,
					FailExitCodes: tc.failExitCodes,
				},
				Tier: 1,
				Mode: "sync",
			}

			res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "ENH-077", CommandFlags{})
			require.NoError(t, err)
			require.NotNil(t, res)

			rc, rcErr := result.Read(result.ResultPath(root, res.TaskID))
			require.NoError(t, rcErr)
			assert.Equal(t, tc.expectStatus, rc.Status)

			// Both streams must appear in the contract log, with stderr
			// leading per buildFailureLog's contract.
			assert.Contains(t, rc.Log, "ENH077_STDOUT_LINE",
				"stdout must be preserved in result contract log on %s", tc.expectStatus)
			assert.Contains(t, rc.Log, "ENH077_STDERR_LINE",
				"stderr must be preserved in result contract log on %s", tc.expectStatus)
			stderrPos := strings.Index(rc.Log, "ENH077_STDERR_LINE")
			stdoutPos := strings.Index(rc.Log, "ENH077_STDOUT_LINE")
			assert.Less(t, stderrPos, stdoutPos,
				"stderr should appear before stdout in failure log")
		})
	}
}

// TestInvokeWithRoot_Tier1_SilentFailureOmitsLogKey verifies ENH-077: when
// an adapter exits non-zero but produced no output on either stream, the
// result contract does NOT grow an empty `log:` key. Keeping the key out
// avoids cluttering committed automation records with empty scalars.
func TestInvokeWithRoot_Tier1_SilentFailureOmitsLogKey(t *testing.T) {
	skipIfShort(t)

	root := setupTestProject(t)
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock-silent-fail",
		Config:  &config.AdapterConfig{Mode: "sync", Command: "exit 1"},
		Tier:    1,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "ENH-077", CommandFlags{})
	require.NoError(t, err)

	// The summary will always be populated (from exit code); log should
	// not be written because both streams were empty.
	rc, rcErr := result.Read(result.ResultPath(root, res.TaskID))
	require.NoError(t, rcErr)
	assert.Equal(t, "error", rc.Status)
	assert.Empty(t, rc.Log, "silent failure must not grow an empty log key in the contract")
}

// --- BUG-055: Adapter stderr surfaced as warnings on success path ---

// TestStderrToWarnings validates the helper that converts captured adapter
// stderr into a slice of warning strings for InvokeResult.Warnings.
func TestStderrToWarnings(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		expect []string
	}{
		{"empty string", "", nil},
		{"whitespace only", "   \n  \n  ", nil},
		{"single line", "WARN: something happened", []string{"WARN: something happened"}},
		{"multi line", "line one\nline two\nline three", []string{"line one", "line two", "line three"}},
		{"blank lines filtered", "first\n\n\nsecond\n\nthird", []string{"first", "second", "third"}},
		{"trailing newlines trimmed", "hello\n\n\n", []string{"hello"}},
		{"leading newlines trimmed", "\n\nhello", []string{"hello"}},
		{"lines with internal whitespace", "  WARN: padded  \n  INFO: also padded  ", []string{"WARN: padded", "INFO: also padded"}},
		{"windows crlf", "line one\r\nline two\r\n", []string{"line one", "line two"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stderrToWarnings(tc.input)
			assert.Equal(t, tc.expect, got)
		})
	}
}

// TestBUG055_Tier1StderrSurfacedOnSuccess verifies that adapter stderr content
// flows into InvokeResult.Warnings when a Tier 1 adapter exits 0.
func TestBUG055_Tier1StderrSurfacedOnSuccess(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	// Tier 1 command that emits to stderr AND exits 0.
	// The `echo ... >&2` writes to stderr; `echo ok` writes to stdout.
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "stderr-tier1",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `echo "WARN: from tier1" >&2 && echo "ok"`},
		Tier:    1,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "BUG055-T1", CommandFlags{})
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)

	// Stderr should appear in warnings
	found := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "WARN: from tier1") {
			found = true
			break
		}
	}
	assert.True(t, found, "expected stderr to appear in Warnings, got: %v", res.Warnings)
}

// TestBUG055_Tier2StderrSurfacedOnSuccess verifies that adapter stderr content
// flows into InvokeResult.Warnings when a Tier 2 adapter writes a complete
// result contract and exits 0.
func TestBUG055_Tier2StderrSurfacedOnSuccess(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	// Create mock Tier 2 script that writes stderr AND updates the result contract
	scriptDir := filepath.Join(root, "testdata")
	require.NoError(t, os.MkdirAll(scriptDir, 0755))
	script := `#!/bin/bash
printf 'WARN: noteworthy thing\n' >&2
cat > "${GTMS_RESULT_FILE}" <<EOF
task: ${GTMS_TASK_ID}
command: ${GTMS_COMMAND}
target: ${GTMS_REFERENCE}
adapter: stderr-tier2
mode: sync
status: complete
result: pass
artefact: test-output.md
attempts: 1
summary: "Tier 2 completed"
completed: "2026-05-02T10:00:00Z"
EOF
`
	require.NoError(t, os.WriteFile(filepath.Join(scriptDir, "stderr-adapter.sh"), []byte(script), 0755))

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "stderr-tier2",
		Config:  &config.AdapterConfig{Mode: "sync", Script: "testdata/stderr-adapter.sh"},
		Tier:    2,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "BUG055-T2", CommandFlags{})
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)

	// Stderr should appear in warnings
	found := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "WARN: noteworthy thing") {
			found = true
			break
		}
	}
	assert.True(t, found, "expected stderr to appear in Warnings, got: %v", res.Warnings)
}

// TestBUG055_ErrorPathStderrNotDuplicated verifies that the error path still
// surfaces stderr in the summary string (existing behaviour) and does NOT
// also inject it into Warnings (which would cause double-surfacing).
func TestBUG055_ErrorPathStderrNotDuplicated(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "fail-stderr",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `echo "ERR: something broke" >&2 && exit 1`},
		Tier:    1,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "BUG055-ERR", CommandFlags{})
	require.NoError(t, err)
	assert.Equal(t, "error", res.Status)

	// Existing behaviour: stderr is in the summary
	assert.Contains(t, res.Summary, "ERR: something broke")

	// BUG-055 guard: stderr must NOT appear in Warnings on the error path
	for _, w := range res.Warnings {
		assert.NotContains(t, w, "ERR: something broke",
			"error-path stderr should be in Summary, not duplicated in Warnings")
	}
}

// TestBUG055_StderrAndContractWarningsCoexist verifies that stderr warnings
// and contract-injected warnings (ENH-096) both appear in InvokeResult.Warnings
// without collision or duplication.
func TestBUG055_StderrAndContractWarningsCoexist(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	// Create mock Tier 2 script that writes stderr AND populates warnings in the contract
	scriptDir := filepath.Join(root, "testdata")
	require.NoError(t, os.MkdirAll(scriptDir, 0755))
	script := `#!/bin/bash
printf 'STDERR-WARN: from stderr channel\n' >&2
cat > "${GTMS_RESULT_FILE}" <<EOF
task: ${GTMS_TASK_ID}
command: ${GTMS_COMMAND}
target: ${GTMS_REFERENCE}
adapter: coexist-tier2
mode: sync
status: complete
result: pass
artefact: test-output.md
attempts: 1
summary: "Tier 2 completed with both channels"
warnings:
  - "CONTRACT-WARN: from contract channel"
completed: "2026-05-02T10:00:00Z"
EOF
`
	require.NoError(t, os.WriteFile(filepath.Join(scriptDir, "coexist-adapter.sh"), []byte(script), 0755))

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "coexist-tier2",
		Config:  &config.AdapterConfig{Mode: "sync", Script: "testdata/coexist-adapter.sh"},
		Tier:    2,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "BUG055-COEX", CommandFlags{})
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)

	// Both channels should be present in Warnings
	hasContract := false
	hasStderr := false
	for _, w := range res.Warnings {
		if strings.Contains(w, "CONTRACT-WARN: from contract channel") {
			hasContract = true
		}
		if strings.Contains(w, "STDERR-WARN: from stderr channel") {
			hasStderr = true
		}
	}
	assert.True(t, hasContract, "expected contract warning in Warnings, got: %v", res.Warnings)
	assert.True(t, hasStderr, "expected stderr warning in Warnings, got: %v", res.Warnings)

	// Contract warnings should come before stderr warnings in the slice
	contractIdx := -1
	stderrIdx := -1
	for i, w := range res.Warnings {
		if strings.Contains(w, "CONTRACT-WARN") && contractIdx == -1 {
			contractIdx = i
		}
		if strings.Contains(w, "STDERR-WARN") && stderrIdx == -1 {
			stderrIdx = i
		}
	}
	assert.Less(t, contractIdx, stderrIdx,
		"contract warnings should come before stderr warnings; contract=%d stderr=%d warnings=%v",
		contractIdx, stderrIdx, res.Warnings)
}

// TestBUG055_EmptyStderrNoWarning verifies that when an adapter exits 0 with
// no stderr output, no spurious empty warning is produced.
func TestBUG055_EmptyStderrNoWarning(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	// Adapter that only writes to stdout, no stderr
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "quiet-tier1",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `echo "clean output"`},
		Tier:    1,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "BUG055-QUIET", CommandFlags{})
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)

	// Only the expected S1 zero-files warning should be present, not any stderr warnings
	for _, w := range res.Warnings {
		assert.NotContains(t, w, "WARN:", "no stderr warning expected for silent adapter")
	}
}

// --- BUG-131: Default sync adapter timeout ---

func TestDefaultSyncTimeout_AppliedWhenUnset(t *testing.T) {
	skipIfShort(t)

	// Override default to a short duration for testing
	orig := defaultSyncTimeout
	defaultSyncTimeout = 500 * time.Millisecond
	defer func() { defaultSyncTimeout = orig }()

	root := setupTestProject(t)
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "hanging-adapter",
		Config: &config.AdapterConfig{
			Mode:    "sync",
			Command: "exec sleep 30",
			// No Timeout configured -- default should apply
		},
		Tier: 1,
		Mode: "sync",
	}

	start := time.Now()
	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "BUG131-DEFAULT", CommandFlags{})
	elapsed := time.Since(start)

	// Should have been killed by the default timeout, not run for 30 seconds
	assert.Less(t, elapsed, 15*time.Second, "should have been killed by default timeout")

	// Invoker returns nil result and error on timeout
	assert.Nil(t, res)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")

	// Verify task file moved to error
	errorTasks, listErr := task.List(root, "error")
	require.NoError(t, listErr)
	assert.Len(t, errorTasks, 1)

	// Verify result contract status is error
	taskID := errorTasks[0].ID
	rcPath := result.ResultPath(root, taskID)
	rc, rcErr := result.Read(rcPath)
	require.NoError(t, rcErr)
	assert.Equal(t, "error", rc.Status)
	assert.Contains(t, rc.Summary, "timed out")
}

func TestDefaultSyncTimeout_NotAppliedToAsync(t *testing.T) {
	// Verify that async mode does NOT get a default timeout applied.
	// Async adapters run then return "in-progress" status. We use a fast
	// command to keep the test quick.
	skipIfShort(t)

	root := setupTestProject(t)
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "async-adapter",
		Config: &config.AdapterConfig{
			Mode:    "async",
			Command: `echo "async done"`,
			// No Timeout configured
		},
		Tier: 1,
		Mode: "async",
	}

	start := time.Now()
	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "BUG131-ASYNC", CommandFlags{})
	elapsed := time.Since(start)

	// Async should return quickly (well under 5s)
	assert.Less(t, elapsed, 5*time.Second, "async adapter should return quickly")
	require.NoError(t, err)
	assert.Equal(t, "in-progress", res.Status)
}

func TestEffectiveTimeoutStr_ReturnsConfigured(t *testing.T) {
	resolved := &ResolvedAdapter{
		Config: &config.AdapterConfig{Timeout: "5m"},
	}
	assert.Equal(t, "5m", effectiveTimeoutStr(resolved))
}

func TestEffectiveTimeoutStr_ReturnsDefault(t *testing.T) {
	resolved := &ResolvedAdapter{
		Config: &config.AdapterConfig{},
	}
	assert.Equal(t, defaultSyncTimeout.String(), effectiveTimeoutStr(resolved))
}

func TestEffectiveDefaultSyncTimeout_EnvOverride(t *testing.T) {
	// BUG-131 testability hook: GTMS_DEFAULT_EXECUTE_TIMEOUT overrides the
	// package default so acceptance tests don't have to wait 30 minutes.
	t.Setenv(defaultSyncTimeoutEnvVar, "5s")
	assert.Equal(t, 5*time.Second, effectiveDefaultSyncTimeout())
}

func TestEffectiveDefaultSyncTimeout_EnvUnsetUsesDefault(t *testing.T) {
	t.Setenv(defaultSyncTimeoutEnvVar, "")
	assert.Equal(t, defaultSyncTimeout, effectiveDefaultSyncTimeout())
}

func TestEffectiveDefaultSyncTimeout_InvalidEnvFallsBack(t *testing.T) {
	// Garbage in the env var must not crash; fall back to the package default.
	t.Setenv(defaultSyncTimeoutEnvVar, "not-a-duration")
	assert.Equal(t, defaultSyncTimeout, effectiveDefaultSyncTimeout())
}

func TestEffectiveDefaultSyncTimeout_ZeroEnvFallsBack(t *testing.T) {
	// A non-positive override would disable the timeout entirely, which is
	// the very behaviour BUG-131 was filed to prevent. Reject and fall back.
	t.Setenv(defaultSyncTimeoutEnvVar, "0s")
	assert.Equal(t, defaultSyncTimeout, effectiveDefaultSyncTimeout())
}

func TestEffectiveTimeoutStr_HonoursEnvOverride(t *testing.T) {
	// Error messages should report the operative timeout, including the
	// env-override path used by acceptance tests.
	t.Setenv(defaultSyncTimeoutEnvVar, "7s")
	resolved := &ResolvedAdapter{
		Config: &config.AdapterConfig{},
	}
	assert.Equal(t, (7 * time.Second).String(), effectiveTimeoutStr(resolved))
}

func TestProcessTreeKill_GrandchildReaped(t *testing.T) {
	skipIfShort(t)

	// Override default to a short duration
	orig := defaultSyncTimeout
	defaultSyncTimeout = 1 * time.Second
	defer func() { defaultSyncTimeout = orig }()

	root := setupTestProject(t)
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	// This adapter spawns a grandchild (sh -c "sleep 60") and then waits.
	// The grandchild should be killed when the process group is terminated.
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "grandchild-adapter",
		Config: &config.AdapterConfig{
			Mode:    "sync",
			Command: `sh -c 'sh -c "sleep 60" & wait'`,
		},
		Tier: 1,
		Mode: "sync",
	}

	start := time.Now()
	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "BUG131-TREE", CommandFlags{})
	elapsed := time.Since(start)

	// Should have been killed by the timeout + WaitDelay, not run for 60 seconds
	assert.Less(t, elapsed, 15*time.Second, "should have been killed by timeout, not waited for grandchild")

	assert.Nil(t, res)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")
}

func TestConfiguredTimeout_OverridesDefault(t *testing.T) {
	skipIfShort(t)

	// Set a very long default that would definitely cause the test to hang
	// if the configured timeout doesn't override it.
	orig := defaultSyncTimeout
	defaultSyncTimeout = 30 * time.Minute
	defer func() { defaultSyncTimeout = orig }()

	root := setupTestProject(t)
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "override-adapter",
		Config: &config.AdapterConfig{
			Mode:    "sync",
			Command: "exec sleep 30",
			Timeout: "200ms", // This should override the 30m default
		},
		Tier: 1,
		Mode: "sync",
	}

	start := time.Now()
	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "BUG131-OVERRIDE", CommandFlags{})
	elapsed := time.Since(start)

	assert.Less(t, elapsed, 15*time.Second, "configured timeout should override default")
	assert.Nil(t, res)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")
	assert.Contains(t, err.Error(), "200ms", "error should reference the configured timeout, not the default")
}

// TestStreamingParser_UnblocksWhenShellExits covers BUG-131 round-2 gap 3:
// the existing TestProcessTreeKill_GrandchildReaped keeps the adapter parent
// alive with `wait`, which doesn't pin the actual Playwright failure mode --
// the immediate shell exits while a descendant retains inherited stdout. If
// the streaming parser blocks on Read against a pipe the dead parent never
// closes (because a live descendant still holds the write end), gtms hangs.
// On Windows the only containment that survives the immediate-parent exit is
// Job Object membership; on Unix the descendant inherits the parent's
// process group (Setpgid: true on the parent), so the group kill reaches it.
//
// This test asserts: even when the adapter parent exits IMMEDIATELY, the
// configured timeout cancels the context, the captured pgid / Job Object
// reaps the descendant, the pipe gets closed, and gtms returns within a
// bounded wall-clock window.
func TestStreamingParser_UnblocksWhenShellExits(t *testing.T) {
	skipIfShort(t)

	// Short override so we don't wait for the production 30m default.
	orig := defaultSyncTimeout
	defaultSyncTimeout = 1 * time.Second
	defer func() { defaultSyncTimeout = orig }()

	root := setupTestProject(t)
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	// Adapter command: spawn a backgrounded grandchild that inherits stdout
	// via sleep (no redirection of the inherited descriptors), then EXIT
	// immediately. The grandchild keeps the write end of the pipe open even
	// though the immediate shell is gone -- this is the precise scenario
	// taskkill /T fails on (no live parent to traverse from) and that the
	// process_unix.Getpgid lookup races (immediate child already exited).
	//
	// On Unix: setpgid + cached pgid + kill(-pgid) hits the grandchild.
	// On Windows: Job Object membership + TerminateJobObject hits it.
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "shell-exits-adapter",
		Config: &config.AdapterConfig{
			Mode: "sync",
			// `sleep 60 &` -- grandchild inherits stdout, runs in
			// background. Parent shell exits immediately after the
			// backgrounded launch (no `wait`).
			Command: `sh -c 'sleep 60 &'`,
		},
		Tier: 1,
		Mode: "sync",
	}

	start := time.Now()
	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "BUG131-SHELL-EXIT", CommandFlags{})
	elapsed := time.Since(start)

	// The streaming parser must return on cancel even though the immediate
	// shell has long since exited. Generous ceiling accounts for the
	// 1s default + 5s WaitDelay + slack. A regression hangs forever (and
	// the test framework times out at its own boundary).
	assert.Less(t, elapsed, 15*time.Second,
		"streaming parser must unblock when shell exits and descendant retains pipe")

	// BUG-131 round-3 tightening: the stated purpose of this test is to
	// PROVE the timeout-cancellation kill path fires (context deadline ->
	// cmd.Cancel -> captured-pgid killFn / Job Object TerminateJobObject).
	// A nil-error completion means the WaitDelay pipe-close path masked
	// the containment behaviour and we get no signal on whether the
	// descendant was actually reaped by the intended mechanism. Reject it
	// so the coverage is unambiguous.
	require.Error(t, err,
		"streaming parser must surface the timeout error -- nil err means the "+
			"WaitDelay path masked the Job Object / pgid kill path we are proving")
	assert.Contains(t, err.Error(), "timed out",
		"error must indicate timeout / deadline (proves context cancellation drove the kill)")
	assert.Nil(t, res, "timeout must return nil result")
}

// --- BUG-136: prompt assembly substitutes tc_name ---

// TestBUG136_PromptAssemblySubstitutesTcName verifies that the promptVars map
// in invoker.go includes tc_name and that the assembled prompt file contains
// the substituted name value, not the literal {tc_name} token.
func TestBUG136_PromptAssemblySubstitutesTcName(t *testing.T) {
	skipIfShort(t)

	t.Run("name_supplied", func(t *testing.T) {
		root := setupTestProject(t)

		// Write a prompt template that contains {tc_name}
		tmplDir := filepath.Join(root, "gtms", "test", "prompts")
		require.NoError(t, os.MkdirAll(tmplDir, 0755))
		tmplPath := filepath.Join(tmplDir, "probe.md")
		require.NoError(t, os.WriteFile(tmplPath, []byte("Name: {tc_name}\nIDs: {tc_ids}\n"), 0644))

		cfg := &config.Config{
			Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
		}

		resolved := &ResolvedAdapter{
			Command: "create",
			Name:    "mock-prompt",
			Config: &config.AdapterConfig{
				Mode:           "sync",
				Command:        `echo "ok"`,
				PromptTemplate: filepath.Join("gtms", "test", "prompts", "probe.md"),
			},
			Tier: 1,
			Mode: "sync",
		}

		flags := CommandFlags{
			Name:   "user-can-login",
			Folder: "login",
		}

		res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "REQ-001", flags)
		require.NoError(t, err)
		assert.Equal(t, "complete", res.Status)

		// Read the assembled prompt from .gtms/tmp/
		promptFile := filepath.Join(root, ".gtms", "tmp", res.TaskID+"-prompt.md")
		data, err := os.ReadFile(promptFile)
		require.NoError(t, err)
		content := string(data)

		assert.Contains(t, content, "Name: user-can-login",
			"assembled prompt must contain the substituted name")
		assert.NotContains(t, content, "{tc_name}",
			"assembled prompt must not contain the literal {tc_name} token")
	})

	t.Run("name_omitted", func(t *testing.T) {
		root := setupTestProject(t)

		tmplDir := filepath.Join(root, "gtms", "test", "prompts")
		require.NoError(t, os.MkdirAll(tmplDir, 0755))
		tmplPath := filepath.Join(tmplDir, "probe.md")
		require.NoError(t, os.WriteFile(tmplPath, []byte("Name: {tc_name}\nIDs: {tc_ids}\n"), 0644))

		cfg := &config.Config{
			Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
		}

		resolved := &ResolvedAdapter{
			Command: "create",
			Name:    "mock-prompt",
			Config: &config.AdapterConfig{
				Mode:           "sync",
				Command:        `echo "ok"`,
				PromptTemplate: filepath.Join("gtms", "test", "prompts", "probe.md"),
			},
			Tier: 1,
			Mode: "sync",
		}

		flags := CommandFlags{
			Folder: "login",
			// Name intentionally omitted
		}

		res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "REQ-002", flags)
		require.NoError(t, err)
		assert.Equal(t, "complete", res.Status)

		promptFile := filepath.Join(root, ".gtms", "tmp", res.TaskID+"-prompt.md")
		data, err := os.ReadFile(promptFile)
		require.NoError(t, err)
		content := string(data)

		assert.Contains(t, content, "Name: \n",
			"assembled prompt must resolve {tc_name} to empty when name is omitted")
		assert.NotContains(t, content, "{tc_name}",
			"assembled prompt must not contain the literal {tc_name} token")
	})
}

// TestBUG136_PromptCreateStandardResolvesCleanly is the ENH-176 guard.
// It feeds the promptCreateStandard const from internal/scaffold through
// prompt.AssembleString and asserts no unresolved {placeholder} survives
// in the <output_rules> section.
func TestBUG136_PromptCreateStandardResolvesCleanly(t *testing.T) {
	// promptCreateStandard is not exported, so read it from the source file.
	// This avoids creating a cross-package dependency on an unexported const.
	src, err := os.ReadFile("../scaffold/templates.go")
	require.NoError(t, err)

	// Extract the promptCreateStandard const body.
	content := string(src)
	marker := "promptCreateStandard = `"
	startIdx := strings.Index(content, marker)
	require.NotEqual(t, -1, startIdx, "promptCreateStandard const not found in templates.go")

	body := content[startIdx+len(marker):]
	endIdx := strings.Index(body, "`")
	require.NotEqual(t, -1, endIdx, "closing backtick not found for promptCreateStandard")
	template := body[:endIdx]

	// Build a complete vars map matching the 16-key promptVars set.
	vars := map[string]string{
		"artefact_file":    "",
		"branch":           "main",
		"context":          "test context",
		"context_file":     "",
		"environment":      "",
		"focus":            "",
		"framework":        "",
		"guides":           "test guides",
		"output_dir":       "/tmp/out",
		"output_subdir":    "",
		"reference":        "REQ-001",
		"tc_ids":           "tc-aabbccdd,tc-11223344",
		"tc_name":          "user-can-login",
		"testcase":         "",
		"testcase_content": "",
		"testcase_file":    "",
	}

	assembled := prompt.AssembleString(template, vars)

	// Extract <output_rules> section.
	rulesStart := strings.Index(assembled, "<output_rules>")
	rulesEnd := strings.Index(assembled, "</output_rules>")
	require.NotEqual(t, -1, rulesStart, "<output_rules> section not found")
	require.NotEqual(t, -1, rulesEnd, "</output_rules> section not found")
	rules := assembled[rulesStart:rulesEnd]

	assert.NotContains(t, rules, "{tc_name}",
		"<output_rules> must not contain unresolved {tc_name} after assembly")
	assert.NotContains(t, rules, "{tc_ids}",
		"<output_rules> must not contain unresolved {tc_ids} after assembly")
}
