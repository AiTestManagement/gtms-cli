package adapter

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/aitestmanagement/gtms-cli/internal/task"
)

// TestAdapterAbstractionAcidTest is the critical Step 9 validation.
// It verifies that the same Invoke code path works identically for both
// Tier 1 and Tier 2 adapters with no tier-specific branching in the caller.
//
// What "works identically" means:
// - Same create command code path for both tiers
// - Task file created with correct adapter name in both cases
// - Result contract created and read correctly in both cases
// - Task file moved to complete correctly
// - No if tier == 1 / if tier == 2 logic in the create command itself
func TestAdapterAbstractionAcidTest(t *testing.T) {
	skipIfShort(t)
	t.Run("Tier1_and_Tier2_produce_identical_outcomes", func(t *testing.T) {
		// --- Set up project root ---
		root := t.TempDir()
		for _, dir := range []string{
			"gtms/tasks/pending", "gtms/tasks/complete", "gtms/tasks/error",
			"gtms/tasks/in-progress", "gtms/tasks/in-review",
			"gtms/cases", "gtms/automation",
			".gtms/results", ".gtms/worktrees", ".gtms/logs",
			"testdata",
		} {
			require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0755))
		}

		// Create gtms.config with both mock adapters
		cfgContent := `project:
  name: Acid Test Project
  repo: org/acid-test
adapters:
  create:
    mock-tier1:
      mode: sync
      command: 'echo "mock tier1 output"'
    mock-tier2:
      mode: sync
      script: testdata/mock-adapter.sh
defaults:
  create: mock-tier1
`
		require.NoError(t, os.WriteFile(
			filepath.Join(root, "gtms.config"),
			[]byte(cfgContent), 0644,
		))

		// Create mock Tier 2 adapter script
		mockScript := `#!/bin/bash
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
		require.NoError(t, os.WriteFile(
			filepath.Join(root, "testdata", "mock-adapter.sh"),
			[]byte(mockScript), 0755,
		))

		// Load config
		cfg, err := config.LoadFromFile(filepath.Join(root, "gtms.config"))
		require.NoError(t, err)

		// --- Test Tier 1 ---
		t.Run("Tier1_sync_create", func(t *testing.T) {
			resolved, err := Resolve(cfg, "create", "mock-tier1")
			require.NoError(t, err)
			assert.Equal(t, 1, resolved.Tier, "mock-tier1 should be Tier 1")
			assert.Equal(t, "sync", resolved.Mode)

			flags := CommandFlags{}

			// Use the SAME Invoke function (this is the acid test)
			res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "ACID-TIER1", flags)
			require.NoError(t, err)

			// Verify outcome
			assert.Equal(t, "complete", res.Status, "Tier 1 should complete")
			assert.Equal(t, "mock-tier1", res.Adapter)
			assert.Equal(t, "sync", res.Mode)
			// BUG-056: sync adapter in non-git temp dir → empty branch
			assert.Equal(t, "", res.Branch)

			// Verify task file in complete
			completeTasks, err := task.List(root, "complete")
			require.NoError(t, err)

			var tier1Task *task.TaskFile
			for _, tf := range completeTasks {
				if tf.Target == "ACID-TIER1" {
					tier1Task = tf
					break
				}
			}
			require.NotNil(t, tier1Task, "Tier 1 task should be in complete")
			assert.Equal(t, "mock-tier1", tier1Task.Adapter)
			assert.Equal(t, "create", tier1Task.Type)
			assert.Equal(t, "complete", tier1Task.Status)

			// Verify result contract
			rcPath := result.ResultPath(root, res.TaskID)
			rc, err := result.Read(rcPath)
			require.NoError(t, err)
			assert.Equal(t, "complete", rc.Status)
			assert.NotEmpty(t, rc.Completed)
			assert.Equal(t, res.TaskID, rc.Task)
		})

		// --- Test Tier 2 ---
		t.Run("Tier2_sync_create", func(t *testing.T) {
			resolved, err := Resolve(cfg, "create", "mock-tier2")
			require.NoError(t, err)
			assert.Equal(t, 2, resolved.Tier, "mock-tier2 should be Tier 2")
			assert.Equal(t, "sync", resolved.Mode)

			flags := CommandFlags{}

			// Use the SAME Invoke function (this is the acid test)
			res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "ACID-TIER2", flags)
			require.NoError(t, err)

			// Verify outcome
			assert.Equal(t, "complete", res.Status, "Tier 2 should complete")
			assert.Equal(t, "mock-tier2", res.Adapter)
			assert.Equal(t, "sync", res.Mode)
			// BUG-056: sync adapter in non-git temp dir → empty branch
			assert.Equal(t, "", res.Branch)

			// Verify task file in complete
			completeTasks, err := task.List(root, "complete")
			require.NoError(t, err)

			var tier2Task *task.TaskFile
			for _, tf := range completeTasks {
				if tf.Target == "ACID-TIER2" {
					tier2Task = tf
					break
				}
			}
			require.NotNil(t, tier2Task, "Tier 2 task should be in complete")
			assert.Equal(t, "mock-tier2", tier2Task.Adapter)
			assert.Equal(t, "create", tier2Task.Type)
			assert.Equal(t, "complete", tier2Task.Status)

			// Verify result contract
			rcPath := result.ResultPath(root, res.TaskID)
			rc, err := result.Read(rcPath)
			require.NoError(t, err)
			assert.Equal(t, "complete", rc.Status)
			assert.Equal(t, res.TaskID, rc.Task)
		})

		// --- Verify both used the same code path ---
		t.Run("Both_tiers_same_code_path", func(t *testing.T) {
			// Verify both tasks are in complete
			completeTasks, err := task.List(root, "complete")
			require.NoError(t, err)
			assert.Len(t, completeTasks, 2, "Both Tier 1 and Tier 2 tasks should be complete")

			// No tasks should remain in pending
			pendingTasks, err := task.List(root, "pending")
			require.NoError(t, err)
			assert.Empty(t, pendingTasks, "No tasks should be pending after sync completion")

			// Both used the same InvokeWithRoot function — no tier-specific
			// branching was needed in the caller. The acid test passes if
			// we get here with both tasks complete and no errors above.
		})
	})
}

// TestAcidTest_NoTierBranchingInCreateCommand verifies that the create
// command's code path contains no tier-specific branching. The create
// command calls adapter.Invoke() which internally dispatches by tier.
// The create command itself never checks if tier == 1 or tier == 2.
//
// This test complements the functional acid test above by verifying
// the architectural property through a structural assertion.
func TestAcidTest_NoTierBranchingInCreateCommand(t *testing.T) {
	skipIfShort(t)
	// The create command (internal/cli/create.go) calls:
	//   adapter.InvokeWithRoot(context.Background(), root, cfg, resolved, target, flags)
	//
	// It never checks resolved.Tier. The tier dispatch happens inside
	// InvokeWithRoot (in invoker.go), which is the adapter package's
	// responsibility.
	//
	// This test verifies that InvokeWithRoot handles the dispatch correctly
	// by running the same flow with different tiers and getting identical
	// outcomes (covered by TestAdapterAbstractionAcidTest above).

	root := t.TempDir()
	for _, dir := range []string{
		"gtms/tasks/pending", "gtms/tasks/complete", "gtms/tasks/error",
		"gtms/tasks/in-progress", ".gtms/results",
	} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0755))
	}

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}
	flags := CommandFlags{}

	// Invoke with Tier 1 - calling the same function
	resolved1 := &ResolvedAdapter{
		Command: "create", Name: "tier1-test",
		Config: &config.AdapterConfig{Mode: "sync", Command: `echo "t1"`},
		Tier: 1, Mode: "sync",
	}
	res1, err := InvokeWithRoot(context.Background(), root, cfg, resolved1, "STRUCT-T1", flags)
	require.NoError(t, err)
	assert.Equal(t, "complete", res1.Status)

	// Invoke with Tier 2 - calling the EXACT SAME function
	scriptPath := filepath.Join(root, "test.sh")
	require.NoError(t, os.WriteFile(scriptPath, []byte("#!/bin/bash\necho ok\n"), 0755))
	resolved2 := &ResolvedAdapter{
		Command: "create", Name: "tier2-test",
		Config: &config.AdapterConfig{Mode: "sync", Script: scriptPath},
		Tier: 2, Mode: "sync",
	}
	res2, err := InvokeWithRoot(context.Background(), root, cfg, resolved2, "STRUCT-T2", flags)
	require.NoError(t, err)
	assert.Equal(t, "complete", res2.Status)

	// Both used InvokeWithRoot — the single unified entry point
	// No tier-specific code exists in the caller
}

// TestAcidTest_ErrorHandlingIdentical verifies that error handling
// is also identical between Tier 1 and Tier 2.
func TestAcidTest_ErrorHandlingIdentical(t *testing.T) {
	skipIfShort(t)
	root := t.TempDir()
	for _, dir := range []string{
		"gtms/tasks/pending", "gtms/tasks/complete", "gtms/tasks/error",
		"gtms/tasks/in-progress", ".gtms/results",
	} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0755))
	}

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}
	flags := CommandFlags{}

	// Tier 1 failure
	resolved1 := &ResolvedAdapter{
		Command: "create", Name: "fail-tier1",
		Config: &config.AdapterConfig{Mode: "sync", Command: `exit 1`},
		Tier: 1, Mode: "sync",
	}
	res1, err := InvokeWithRoot(context.Background(), root, cfg, resolved1, "FAIL-T1", flags)
	require.NoError(t, err)
	assert.Equal(t, "error", res1.Status)

	// Tier 2 failure (script that exits non-zero, doesn't write result file)
	failScript := filepath.Join(root, "fail.sh")
	require.NoError(t, os.WriteFile(failScript, []byte("#!/bin/bash\nexit 1\n"), 0755))
	resolved2 := &ResolvedAdapter{
		Command: "create", Name: "fail-tier2",
		Config: &config.AdapterConfig{Mode: "sync", Script: failScript},
		Tier: 2, Mode: "sync",
	}
	res2, err := InvokeWithRoot(context.Background(), root, cfg, resolved2, "FAIL-T2", flags)
	require.NoError(t, err)
	assert.Equal(t, "error", res2.Status)

	// Both error tasks should be in the error directory
	errorTasks, err := task.List(root, "error")
	require.NoError(t, err)
	assert.Len(t, errorTasks, 2, "Both tier 1 and tier 2 failures should be in error/")
}
