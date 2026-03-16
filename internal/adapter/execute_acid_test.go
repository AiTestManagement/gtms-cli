package adapter

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/aitestmanagement/gtms-cli/internal/task"
)

// setupExecuteTestProject creates a minimal project structure for execute acid tests.
// Includes a test case, an automation record, and both sync/async adapters configured.
func setupExecuteTestProject(t *testing.T) (string, *config.Config) {
	t.Helper()
	root := t.TempDir()

	// Create required directories
	for _, dir := range []string{
		"test-tasks/pending", "test-tasks/complete", "test-tasks/failed",
		"test-tasks/in-progress", "test-tasks/in-review",
		"test-cases", "test-automation/records", "test-automation/specs",
		"results",
		".gtms/results", ".gtms/worktrees", ".gtms/logs",
		"testdata",
	} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0755))
	}

	// Create a test case file that automate/execute reference
	testCaseContent := `---
id: tc-acid
title: Acid Test Checkout Flow
requirement: JIRA-100
---

## Steps
1. Navigate to checkout
2. Enter payment details
3. Submit order
4. Verify confirmation
`
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "test-cases", "tc-acid-checkout.md"),
		[]byte(testCaseContent), 0644,
	))

	// Create an automation record (status: developed) that execute will validate against
	automationRecord := &pipeline.AutomationRecord{
		TestCase:      "tc-acid",
		Framework:     "playwright",
		Status:        "developed",
		Artefact:      "test-automation/specs/tc-acid-checkout.spec.ts",
		Branch:        "feature/automate-tc-acid",
		Adapter:       "local-claude",
		LastDevResult: "pass",
		Attempts:      1,
		Cycle:         1,
	}
	recordPath := filepath.Join(root, "test-automation", "records", "tc-acid.automation.md")
	require.NoError(t, pipeline.WriteAutomationRecord(recordPath, automationRecord))

	// Create a mock spec file (the artefact)
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "test-automation", "specs", "tc-acid-checkout.spec.ts"),
		[]byte("// mock spec file\n"), 0644,
	))

	// Create mock Tier 2 async trigger script
	triggerScript := `#!/bin/bash
# Simulates async trigger — updates result contract with partial info and exits
cat >> "${GTMS_RESULT_FILE}" <<EOF
summary: "Mock workflow triggered"
log: https://example.com/runs/12345
EOF
exit 0
`
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "testdata", "mock-async-trigger.sh"),
		[]byte(triggerScript), 0755,
	))

	// Create mock Tier 2 async status script
	statusScript := `#!/bin/bash
# Simulates checking remote status — always reports complete
cat > "${GTMS_RESULT_FILE}" <<EOF
task: ${GTMS_TASK_ID}
command: ${GTMS_COMMAND}
target: ${GTMS_TESTCASE}
adapter: mock-async
mode: async
status: complete
artefact: results/mock-output.xml
attempts: 1
summary: "Mock remote execution completed"
log: https://example.com/runs/12345
completed: 2025-02-14T12:00:00Z
EOF
`
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "testdata", "mock-async-status.sh"),
		[]byte(statusScript), 0755,
	))

	// Create gtms.config with both mock adapters for execute
	cfgContent := `project:
  name: Execute Acid Test
  repo: org/acid-test
adapters:
  execute:
    mock-runner:
      mode: sync
      command: 'echo "PASS: {testcase}"'
    mock-async:
      mode: async
      script: testdata/mock-async-trigger.sh
      status-script: testdata/mock-async-status.sh
defaults:
  execute: mock-runner
`
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "gtms.config"),
		[]byte(cfgContent), 0644,
	))

	// Load config
	cfg, err := config.LoadFromFile(filepath.Join(root, "gtms.config"))
	require.NoError(t, err)

	return root, cfg
}

// TestExecuteAdapterAbstractionAcidTest is the Step 14 critical validation.
// It verifies that the same Invoke code path works identically for both
// Tier 1 sync and Tier 2 async execute adapters.
func TestExecuteAdapterAbstractionAcidTest(t *testing.T) {
	skipIfShort(t)
	t.Run("Tier1_and_Tier2_execute_produce_identical_outcomes", func(t *testing.T) {
		root, cfg := setupExecuteTestProject(t)

		// =========================================================
		// TIER 1: Sync execute with mock-runner
		// =========================================================
		t.Run("Tier1_sync_execute", func(t *testing.T) {
			resolved, err := Resolve(cfg, "execute", "mock-runner")
			require.NoError(t, err)
			assert.Equal(t, 1, resolved.Tier, "mock-runner should be Tier 1")
			assert.Equal(t, "sync", resolved.Mode)

			// Use the SAME Invoke function (this is the acid test)
			flags := CommandFlags{
				SpecFile: "test-automation/specs/tc-acid-checkout.spec.ts",
			}
			res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-acid", flags)
			require.NoError(t, err)

			// Verify outcome
			assert.Equal(t, "complete", res.Status, "Tier 1 sync should complete immediately")
			assert.Equal(t, "mock-runner", res.Adapter)
			assert.Equal(t, "sync", res.Mode)
			assert.Contains(t, res.Branch, "feature/execute-tc-acid")
			assert.Contains(t, res.Summary, "PASS: tc-acid")

			// Verify task file moved to complete
			completeTasks, err := task.List(root, "complete")
			require.NoError(t, err)

			var tier1Task *task.TaskFile
			for _, tf := range completeTasks {
				if tf.Target == "tc-acid" && tf.Type == "execute" {
					tier1Task = tf
					break
				}
			}
			require.NotNil(t, tier1Task, "Tier 1 execute task should be in complete")
			assert.Equal(t, "mock-runner", tier1Task.Adapter)
			assert.Equal(t, "execute", tier1Task.Type)
			assert.Equal(t, "complete", tier1Task.Status)

			// Verify result contract
			rcPath := result.ResultPath(root, res.TaskID)
			rc, err := result.Read(rcPath)
			require.NoError(t, err)
			assert.Equal(t, "complete", rc.Status)
			assert.NotEmpty(t, rc.Completed)
			assert.Equal(t, res.TaskID, rc.Task)

			// Verify automation record was updated with execution result
			record, _, err := pipeline.FindAutomationRecord(root, "tc-acid")
			require.NoError(t, err)
			require.NotNil(t, record)
			assert.Equal(t, "pass", record.LastFormalResult,
				"Automation record should show pass after successful sync execute")
		})

		// =========================================================
		// TIER 2: Async execute with mock-async
		// =========================================================
		t.Run("Tier2_async_execute", func(t *testing.T) {
			// Reset: Create a fresh project for the async test to avoid
			// conflicting task files from the sync test above
			asyncRoot, asyncCfg := setupExecuteTestProject(t)

			resolved, err := Resolve(asyncCfg, "execute", "mock-async")
			require.NoError(t, err)
			assert.Equal(t, 2, resolved.Tier, "mock-async should be Tier 2")
			assert.Equal(t, "async", resolved.Mode)

			// Use the SAME Invoke function (this is the acid test)
			flags := CommandFlags{
				SpecFile: "test-automation/specs/tc-acid-checkout.spec.ts",
			}
			res, err := InvokeWithRoot(context.Background(), asyncRoot, asyncCfg, resolved, "tc-acid", flags)
			require.NoError(t, err)

			// After trigger: task should be in-progress (not complete)
			assert.Equal(t, "in-progress", res.Status, "Tier 2 async should be in-progress after trigger")
			assert.Equal(t, "mock-async", res.Adapter)
			assert.Equal(t, "async", res.Mode)

			// Verify task file is in in-progress
			inProgressTasks, err := task.List(asyncRoot, "in-progress")
			require.NoError(t, err)

			var asyncTask *task.TaskFile
			for _, tf := range inProgressTasks {
				if tf.Target == "tc-acid" && tf.Type == "execute" {
					asyncTask = tf
					break
				}
			}
			require.NotNil(t, asyncTask, "Async execute task should be in in-progress")
			assert.Equal(t, "mock-async", asyncTask.Adapter)

			// Now simulate polling: run the status script
			resultPath := result.ResultPath(asyncRoot, res.TaskID)
			statusAC := &AdapterContext{
				TaskID:      res.TaskID,
				Command:     "execute",
				TestCase:    "tc-acid",
				ProjectRoot: asyncRoot,
				WorkDir:     asyncRoot,
				ResultFile:  resultPath,
			}

			statusScript := filepath.Join(asyncRoot, "testdata", "mock-async-status.sh")
			_, err = InvokeTier2(context.Background(), statusAC, statusScript)
			require.NoError(t, err)

			// Read the updated result contract
			rc, err := result.Read(resultPath)
			require.NoError(t, err)
			assert.Equal(t, "complete", rc.Status, "Status script should report complete")
			assert.Equal(t, "results/mock-output.xml", rc.Artefact)

			// Process the result: move task to complete and update automation record
			err = task.Move(asyncRoot, asyncTask, "complete")
			require.NoError(t, err)
			err = pipeline.UpdateExecutionResult(asyncRoot, asyncTask, rc)
			require.NoError(t, err)

			// Verify task moved to complete
			completeTasks, err := task.List(asyncRoot, "complete")
			require.NoError(t, err)

			var completedTask *task.TaskFile
			for _, tf := range completeTasks {
				if tf.Target == "tc-acid" && tf.Type == "execute" {
					completedTask = tf
					break
				}
			}
			require.NotNil(t, completedTask, "Async task should be in complete after status poll")

			// Verify automation record updated
			record, _, err := pipeline.FindAutomationRecord(asyncRoot, "tc-acid")
			require.NoError(t, err)
			require.NotNil(t, record)
			assert.Equal(t, "pass", record.LastFormalResult,
				"Automation record should show pass after successful async execute")
			assert.Equal(t, "results/mock-output.xml", record.LastFormalRun)
		})
	})
}

// TestExecuteAcidTest_SameCodePath verifies that the execute command's code path
// contains no tier-specific branching. Both tiers use the same InvokeWithRoot function.
func TestExecuteAcidTest_SameCodePath(t *testing.T) {
	skipIfShort(t)
	root, cfg := setupExecuteTestProject(t)

	flags := CommandFlags{
		SpecFile: "test-automation/specs/tc-acid-checkout.spec.ts",
	}

	// Tier 1 sync execute
	resolved1 := &ResolvedAdapter{
		Command: "execute", Name: "sync-test",
		Config: &config.AdapterConfig{Mode: "sync", Command: `echo "PASS"`},
		Tier: 1, Mode: "sync",
	}
	res1, err := InvokeWithRoot(context.Background(), root, cfg, resolved1, "tc-acid", flags)
	require.NoError(t, err)
	assert.Equal(t, "complete", res1.Status)

	// Create a fresh root for the second test to avoid conflicting tasks
	root2, cfg2 := setupExecuteTestProject(t)

	// Tier 2 async execute — same InvokeWithRoot function
	resolved2 := &ResolvedAdapter{
		Command: "execute", Name: "async-test",
		Config: &config.AdapterConfig{
			Mode:   "async",
			Script: filepath.Join(root2, "testdata", "mock-async-trigger.sh"),
		},
		Tier: 2, Mode: "async",
	}
	res2, err := InvokeWithRoot(context.Background(), root2, cfg2, resolved2, "tc-acid", flags)
	require.NoError(t, err)
	assert.Equal(t, "in-progress", res2.Status) // Async stops at in-progress

	// Both used InvokeWithRoot — the single unified entry point
	// No tier-specific code exists in the caller
}

// TestExecuteAcidTest_ContextFields verifies that the AdapterContext is
// correctly populated for execute commands regardless of adapter tier.
func TestExecuteAcidTest_ContextFields(t *testing.T) {
	skipIfShort(t)
	root, cfg := setupExecuteTestProject(t)

	target := "tc-acid"
	flags := CommandFlags{
		SpecFile: "test-automation/specs/tc-acid-checkout.spec.ts",
	}

	resolved := &ResolvedAdapter{
		Command: "execute", Name: "ctx-test",
		Config: &config.AdapterConfig{Mode: "sync", Command: `echo "spec={spec_file} tc={testcase}"`},
		Tier: 1, Mode: "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, target, flags)
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)

	// The command template should have {testcase} and {spec_file} substituted
	assert.Contains(t, res.Summary, "spec=test-automation/specs/tc-acid-checkout.spec.ts")
	assert.Contains(t, res.Summary, "tc=tc-acid")
}

// TestExecuteAcidTest_AutomationRecordUpdated verifies that the automation record
// is correctly updated after both sync and async execute completions.
func TestExecuteAcidTest_AutomationRecordUpdated(t *testing.T) {
	skipIfShort(t)
	t.Run("sync_updates_record", func(t *testing.T) {
		root, cfg := setupExecuteTestProject(t)

		resolved, err := Resolve(cfg, "execute", "mock-runner")
		require.NoError(t, err)

		flags := CommandFlags{SpecFile: "test-automation/specs/tc-acid-checkout.spec.ts"}
		_, err = InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-acid", flags)
		require.NoError(t, err)

		// Check automation record
		record, _, err := pipeline.FindAutomationRecord(root, "tc-acid")
		require.NoError(t, err)
		require.NotNil(t, record)
		assert.Equal(t, "pass", record.LastFormalResult)
		// Original fields preserved
		assert.Equal(t, "tc-acid", record.TestCase)
		assert.Equal(t, "playwright", record.Framework)
		assert.Equal(t, "developed", record.Status)
		assert.Equal(t, 1, record.Cycle)
	})
}

// TestExecuteAcidTest_TaskFileFields verifies that task files are created
// correctly for execute commands.
func TestExecuteAcidTest_TaskFileFields(t *testing.T) {
	skipIfShort(t)
	root, cfg := setupExecuteTestProject(t)

	resolved, err := Resolve(cfg, "execute", "mock-runner")
	require.NoError(t, err)

	flags := CommandFlags{SpecFile: "test-automation/specs/tc-acid-checkout.spec.ts"}
	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-acid", flags)
	require.NoError(t, err)

	// Read the task file from complete
	completeTasks, err := task.List(root, "complete")
	require.NoError(t, err)

	var execTask *task.TaskFile
	for _, tf := range completeTasks {
		if tf.Target == "tc-acid" && tf.Type == "execute" {
			execTask = tf
			break
		}
	}
	require.NotNil(t, execTask)

	assert.Equal(t, res.TaskID, execTask.ID)
	assert.Equal(t, "execute", execTask.Type)
	assert.Equal(t, "tc-acid", execTask.Target)
	assert.Equal(t, "mock-runner", execTask.Adapter)
	assert.Contains(t, execTask.Branch, "feature/execute-tc-acid")
	assert.NotEmpty(t, execTask.Created)
}

// TestExecuteAcidTest_ResultContractFields verifies result contracts for both tiers.
func TestExecuteAcidTest_ResultContractFields(t *testing.T) {
	skipIfShort(t)
	t.Run("tier1_result_contract", func(t *testing.T) {
		root, cfg := setupExecuteTestProject(t)

		resolved, err := Resolve(cfg, "execute", "mock-runner")
		require.NoError(t, err)

		flags := CommandFlags{SpecFile: "test-automation/specs/tc-acid-checkout.spec.ts"}
		res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-acid", flags)
		require.NoError(t, err)

		rcPath := result.ResultPath(root, res.TaskID)
		rc, err := result.Read(rcPath)
		require.NoError(t, err)

		assert.Equal(t, "complete", rc.Status)
		assert.Equal(t, res.TaskID, rc.Task)
		assert.Equal(t, "execute", rc.Command)
		assert.Equal(t, "tc-acid", rc.Target)
		assert.Equal(t, "mock-runner", rc.Adapter)
		assert.Equal(t, "sync", rc.Mode)
		assert.NotEmpty(t, rc.Completed)
	})

	t.Run("tier2_result_contract", func(t *testing.T) {
		root, cfg := setupExecuteTestProject(t)

		resolved, err := Resolve(cfg, "execute", "mock-async")
		require.NoError(t, err)

		flags := CommandFlags{SpecFile: "test-automation/specs/tc-acid-checkout.spec.ts"}
		res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-acid", flags)
		require.NoError(t, err)

		// After trigger, result contract exists but status is still pending
		rcPath := result.ResultPath(root, res.TaskID)
		rc, err := result.Read(rcPath)
		require.NoError(t, err)

		assert.Equal(t, res.TaskID, rc.Task)
		assert.Equal(t, "execute", rc.Command)
		assert.Equal(t, "tc-acid", rc.Target)
		assert.Equal(t, "mock-async", rc.Adapter)
		assert.Equal(t, "async", rc.Mode)
	})
}

// TestExecuteAcidTest_NoCommandModeSpecificLogic is the structural assertion:
// the execute command code path (adapter.Invoke/InvokeWithRoot) handles both
// sync and async modes without any mode-specific branching in the caller.
// The mode dispatch is entirely within the invoker.
func TestExecuteAcidTest_NoCommandModeSpecificLogic(t *testing.T) {
	skipIfShort(t)
	// This test proves the architectural property by running both modes
	// through the same function and verifying the correct lifecycle.

	// Sync: pending -> complete (one invocation)
	root1, cfg1 := setupExecuteTestProject(t)
	syncResolved, err := Resolve(cfg1, "execute", "mock-runner")
	require.NoError(t, err)
	flags := CommandFlags{SpecFile: "test-automation/specs/tc-acid-checkout.spec.ts"}

	syncRes, err := InvokeWithRoot(context.Background(), root1, cfg1, syncResolved, "tc-acid", flags)
	require.NoError(t, err)
	assert.Equal(t, "complete", syncRes.Status, "Sync: pending -> complete in one invocation")

	// Async: pending -> in-progress after trigger
	root2, cfg2 := setupExecuteTestProject(t)
	asyncResolved, err := Resolve(cfg2, "execute", "mock-async")
	require.NoError(t, err)

	asyncRes, err := InvokeWithRoot(context.Background(), root2, cfg2, asyncResolved, "tc-acid", flags)
	require.NoError(t, err)
	assert.Equal(t, "in-progress", asyncRes.Status, "Async: pending -> in-progress after trigger")

	// No adapter-mode-specific logic in execute command — both used InvokeWithRoot
}
