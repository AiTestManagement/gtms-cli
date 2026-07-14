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
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// setupExecuteTestProject creates a minimal project structure for execute acid tests.
// CON-023 / ENH-145: seeds a wiring record (the new canonical identity store)
// alongside the TC spec and adapter scripts. The legacy automation record is
// no longer scaffolded — production execute now reads wiring at the CLI layer
// and never mutates it from the invoker.
func setupExecuteTestProject(t *testing.T) (string, *config.Config) {
	t.Helper()
	root := t.TempDir()

	// Create required directories
	for _, dir := range []string{
		"gtms/tasks/pending", "gtms/tasks/complete", "gtms/tasks/error",
		"gtms/tasks/in-progress", "gtms/tasks/in-review",
		"gtms/test/cases", "gtms/automation/wiring", "gtms/automation/specs",
		"gtms/execution",
		"results",
		".gtms/results", ".gtms/worktrees", ".gtms/logs",
		"testdata",
	} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0755))
	}

	// Create a test case file that automate/execute reference
	testCasePath := filepath.Join(root, "gtms/test/cases", "tc-acid-checkout.md")
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
	require.NoError(t, os.WriteFile(testCasePath, []byte(testCaseContent), 0644))

	// Create the mock spec file (the artefact) BEFORE seeding wiring so
	// the hashes in the wiring record match real on-disk content. The
	// reader's hash-currency picker would otherwise see this as stale.
	artefactPath := filepath.Join(root, "gtms/automation", "specs", "tc-acid-checkout.spec.ts")
	require.NoError(t, os.WriteFile(artefactPath, []byte("// mock spec file\n"), 0644))

	// Compute the real hashes from the files just written.
	testCaseHash, err := pipeline.HashFile(testCasePath)
	require.NoError(t, err)
	artefactHash, err := pipeline.HashFile(artefactPath)
	require.NoError(t, err)

	// Seed the wiring record (CON-023 / ENH-145) using the production
	// writer with the disk-derived hashes so the wiring lands in
	// TierCurrent.
	if _, err := wiring.Write(root, &wiring.WiringRecord{
		TestCase:     "tc-acid",
		TestCaseHash: testCaseHash,
		Framework:    "playwright",
		Adapter:      "mock-runner",
		Artefact:     "gtms/automation/specs/tc-acid-checkout.spec.ts",
		ArtefactHash: artefactHash,
	}); err != nil {
		require.NoError(t, err)
	}

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
result: pass
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
      framework: playwright
      command: 'echo "PASS: {testcase}"'
    mock-async:
      mode: async
      framework: playwright
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
				ArtefactFile: "gtms/automation/specs/tc-acid-checkout.spec.ts",
			}
			res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-acid", flags)
			require.NoError(t, err)

			// Verify outcome
			assert.Equal(t, "complete", res.Status, "Tier 1 sync should complete immediately")
			assert.Equal(t, "mock-runner", res.Adapter)
			assert.Equal(t, "sync", res.Mode)
			// BUG-056: sync adapter in non-git temp dir → empty branch
			assert.Equal(t, "", res.Branch)
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

			// CON-023 / ENH-146: assert pass-outcome on the result
			// contract (canonical store). The legacy automation-record
			// update is retired on the execute path.
			assert.Equal(t, "pass", rc.Result,
				"Result contract should show pass after successful sync execute")
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
				ArtefactFile: "gtms/automation/specs/tc-acid-checkout.spec.ts",
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

			// Read the updated result contract — this is the new canonical
			// store of execute outcome under CON-023 / ENH-146.
			rc, err := result.Read(resultPath)
			require.NoError(t, err)
			assert.Equal(t, "complete", rc.Status, "Status script should report complete")
			assert.Equal(t, "pass", rc.Result, "Result contract carries the pass outcome")
			assert.Equal(t, "results/mock-output.xml", rc.Artefact)

			// Snapshot wiring before post-poll processing so we can assert
			// byte-stability: the execute path must NEVER mutate wiring.
			wiringPath := filepath.Join(asyncRoot, "gtms", "automation", "wiring", "tc-acid--playwright.wiring.yaml")
			wiringBefore, err := os.ReadFile(wiringPath)
			require.NoError(t, err)

			// Process the async completion: move task to complete and write
			// the per-test results file (ADR-020 / CON-016). CON-023 /
			// ENH-146: WriteExecuteResultsFile is the new post-execute
			// helper — the legacy pipeline.UpdateExecutionResult's
			// automation-record-mutating half is retired.
			err = task.Move(asyncRoot, asyncTask, "complete")
			require.NoError(t, err)
			// Adapter is sourced from the resolved wiring under the new
			// model; stamp it on the contract so the per-test results
			// file carries it correctly.
			rc.Framework = "playwright"
			if rc.Adapter == "" {
				rc.Adapter = "mock-async"
			}
			err = WriteExecuteResultsFile(asyncRoot, asyncTask, rc)
			require.NoError(t, err)

			// Verify task moved to complete.
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

			// CON-023 / ENH-146 verification of the new state stores:
			//
			//   1. Result contract carries the terminal outcome (already
			//      asserted above).
			//   2. gtms/execution/<task>--<tc>.results.yaml has been
			//      written with the pass outcome.
			//   3. Wiring file is byte-for-byte unchanged.
			executionPath := filepath.Join(asyncRoot, "gtms", "execution",
				asyncTask.ID+"--tc-acid.results.yaml")
			_, err = os.Stat(executionPath)
			assert.NoError(t, err,
				"per-test results file (gtms/execution/) must be written on async completion")

			wiringAfter, err := os.ReadFile(wiringPath)
			require.NoError(t, err)
			assert.Equal(t, wiringBefore, wiringAfter,
				"execute path must NEVER mutate wiring (CON-023 / ENH-146)")
		})
	})
}

// TestExecuteAcidTest_SameCodePath verifies that the execute command's code path
// contains no tier-specific branching. Both tiers use the same InvokeWithRoot function.
func TestExecuteAcidTest_SameCodePath(t *testing.T) {
	skipIfShort(t)
	root, cfg := setupExecuteTestProject(t)

	flags := CommandFlags{
		ArtefactFile: "gtms/automation/specs/tc-acid-checkout.spec.ts",
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
		ArtefactFile: "gtms/automation/specs/tc-acid-checkout.spec.ts",
	}

	resolved := &ResolvedAdapter{
		Command: "execute", Name: "ctx-test",
		Config: &config.AdapterConfig{Mode: "sync", Command: `echo "spec={artefact_file} tc={testcase}"`},
		Tier: 1, Mode: "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, target, flags)
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)

	// The command template should have {testcase} and {artefact_file} substituted
	assert.Contains(t, res.Summary, "spec=gtms/automation/specs/tc-acid-checkout.spec.ts")
	assert.Contains(t, res.Summary, "tc=tc-acid")
}

// TestExecuteAcidTest_AutomationRecordUpdated verifies the per-execute
// canonical store is updated correctly after sync execute. CON-023 /
// ENH-146: that store is now the result contract + the per-test
// gtms/execution/*.results.yaml file; the legacy automation record
// is no longer touched on the execute path.
func TestExecuteAcidTest_AutomationRecordUpdated(t *testing.T) {
	skipIfShort(t)
	t.Run("sync_updates_record", func(t *testing.T) {
		root, cfg := setupExecuteTestProject(t)

		resolved, err := Resolve(cfg, "execute", "mock-runner")
		require.NoError(t, err)

		flags := CommandFlags{ArtefactFile: "gtms/automation/specs/tc-acid-checkout.spec.ts", Framework: "playwright"}
		res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-acid", flags)
		require.NoError(t, err)

		rc, err := result.Read(result.ResultPath(root, res.TaskID))
		require.NoError(t, err)
		assert.Equal(t, "complete", rc.Status)
		assert.Equal(t, "pass", rc.Result)
		assert.Equal(t, "tc-acid", rc.Target)
		assert.Equal(t, "playwright", rc.Framework)
	})
}

// TestExecuteAcidTest_TaskFileFields verifies that task files are created
// correctly for execute commands.
func TestExecuteAcidTest_TaskFileFields(t *testing.T) {
	skipIfShort(t)
	root, cfg := setupExecuteTestProject(t)

	resolved, err := Resolve(cfg, "execute", "mock-runner")
	require.NoError(t, err)

	flags := CommandFlags{ArtefactFile: "gtms/automation/specs/tc-acid-checkout.spec.ts"}
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
	// BUG-056: sync adapter in non-git temp dir → empty branch
	assert.Equal(t, "", execTask.Branch)
	assert.NotEmpty(t, execTask.Created)
}

// TestExecuteAcidTest_ResultContractFields verifies result contracts for both tiers.
func TestExecuteAcidTest_ResultContractFields(t *testing.T) {
	skipIfShort(t)
	t.Run("tier1_result_contract", func(t *testing.T) {
		root, cfg := setupExecuteTestProject(t)

		resolved, err := Resolve(cfg, "execute", "mock-runner")
		require.NoError(t, err)

		flags := CommandFlags{ArtefactFile: "gtms/automation/specs/tc-acid-checkout.spec.ts"}
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

		flags := CommandFlags{ArtefactFile: "gtms/automation/specs/tc-acid-checkout.spec.ts"}
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
	flags := CommandFlags{ArtefactFile: "gtms/automation/specs/tc-acid-checkout.spec.ts"}

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

// TestBUG023_FailedExecuteUpdatesPipelineRecord verifies that when a Tier 1
// execute adapter fails (non-zero exit), the automation record's last-formal-result
// is set to "fail" so that gaps and map report accurate state.
func TestBUG023_FailedExecuteUpdatesPipelineRecord(t *testing.T) {
	skipIfShort(t)
	root, cfg := setupExecuteTestProject(t)

	// Create a failing Tier 1 adapter
	resolved := &ResolvedAdapter{
		Command: "execute",
		Name:    "mock-fail-runner",
		Config:  &config.AdapterConfig{Mode: "sync", Command: "exit 1", Framework: "playwright"},
		Tier:    1,
		Mode:    "sync",
	}

	flags := CommandFlags{ArtefactFile: "gtms/automation/specs/tc-acid-checkout.spec.ts", Framework: "playwright"}
	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-acid", flags)
	require.NoError(t, err, "InvokeWithRoot should not return error for non-zero exit")
	assert.Equal(t, "error", res.Status, "status should be error for failed adapter")

	// CON-023 / ENH-146: assert on the result contract (the new canonical
	// store of execute outcome). The legacy automation-record update is
	// retired on the execute path.
	rc, err := result.Read(result.ResultPath(root, res.TaskID))
	require.NoError(t, err)
	assert.Equal(t, "error", rc.Status, "result contract status should be error after failed adapter")
	assert.Equal(t, "tc-acid", rc.Target)
	assert.Equal(t, "playwright", rc.Framework, "framework should be stamped on result contract from task")
}

// TestBUG023_FailedTier2ExecuteUpdatesPipelineRecord verifies that when a Tier 2
// execute adapter script sets the result contract to error status, the automation
// record's last-formal-result is set to "error" (ENH-040: was "fail" before).
func TestBUG023_FailedTier2ExecuteUpdatesPipelineRecord(t *testing.T) {
	skipIfShort(t)
	root, _ := setupExecuteTestProject(t)

	// Create a Tier 2 script that updates result contract to error
	failScript := `#!/bin/bash
# Update result contract to error status
cat > "${GTMS_RESULT_FILE}" <<EOF
task: ${GTMS_TASK_ID}
command: ${GTMS_COMMAND}
target: ${GTMS_TESTCASE}
adapter: mock-fail-tier2
mode: sync
status: error
attempts: 1
summary: "Test assertion failed"
completed: 2025-02-14T12:00:00Z
EOF
exit 0
`
	scriptPath := filepath.Join(root, "testdata", "mock-fail-execute.sh")
	require.NoError(t, os.WriteFile(scriptPath, []byte(failScript), 0755))

	cfg, err := config.LoadFromFile(filepath.Join(root, "gtms.config"))
	require.NoError(t, err)

	resolved := &ResolvedAdapter{
		Command: "execute",
		Name:    "mock-fail-tier2",
		Config:  &config.AdapterConfig{Mode: "sync", Script: scriptPath, Framework: "playwright"},
		Tier:    2,
		Mode:    "sync",
	}

	flags := CommandFlags{ArtefactFile: "gtms/automation/specs/tc-acid-checkout.spec.ts", Framework: "playwright"}
	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-acid", flags)
	require.NoError(t, err)
	assert.Equal(t, "error", res.Status, "status should be error for failed Tier 2 adapter")

	// CON-023 / ENH-146: result contract is the canonical store.
	rc, err := result.Read(result.ResultPath(root, res.TaskID))
	require.NoError(t, err)
	assert.Equal(t, "error", rc.Status)
	assert.Equal(t, "playwright", rc.Framework)
}

// TestBUG023_FailedThenPassedExecute verifies the full lifecycle: a failed execute
// sets last-formal-result to "fail", then a subsequent successful execute sets it to "pass".
func TestBUG023_FailedThenPassedExecute(t *testing.T) {
	skipIfShort(t)
	root, cfg := setupExecuteTestProject(t)

	// First: run a failing adapter
	failResolved := &ResolvedAdapter{
		Command: "execute",
		Name:    "mock-fail-runner",
		Config:  &config.AdapterConfig{Mode: "sync", Command: "exit 1", Framework: "playwright"},
		Tier:    1,
		Mode:    "sync",
	}

	flags := CommandFlags{ArtefactFile: "gtms/automation/specs/tc-acid-checkout.spec.ts", Framework: "playwright"}
	res, err := InvokeWithRoot(context.Background(), root, cfg, failResolved, "tc-acid", flags)
	require.NoError(t, err)
	assert.Equal(t, "error", res.Status)

	// CON-023 / ENH-146: assert the result contract for the failed run.
	failRC, err := result.Read(result.ResultPath(root, res.TaskID))
	require.NoError(t, err)
	assert.Equal(t, "error", failRC.Status, "first run: result contract status = error")

	// Second: run a passing adapter
	passResolved, err := Resolve(cfg, "execute", "mock-runner")
	require.NoError(t, err)

	res, err = InvokeWithRoot(context.Background(), root, cfg, passResolved, "tc-acid", flags)
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)

	passRC, err := result.Read(result.ResultPath(root, res.TaskID))
	require.NoError(t, err)
	assert.Equal(t, "complete", passRC.Status, "second run: result contract status = complete")
	assert.Equal(t, "pass", passRC.Result, "second run: result contract result = pass")
}

// TestBUG029_Tier2ExecutePersistsArtefactHash verifies that the artefact hash
// from CommandFlags.ArtefactHash survives a Tier 2 adapter's result contract
// overwrite and is persisted on the result contract. CON-023 / ENH-146: the
// hash now lives on the result contract (the canonical execute store);
// wiring is read-only on the execute path.
func TestBUG029_Tier2ExecutePersistsArtefactHash(t *testing.T) {
	skipIfShort(t)

	root := t.TempDir()

	// Create required directories
	for _, dir := range []string{
		"gtms/tasks/pending", "gtms/tasks/complete", "gtms/tasks/error",
		"gtms/tasks/in-progress", "gtms/tasks/in-review",
		"gtms/test/cases", "gtms/automation/wiring", "gtms/automation/specs",
		"gtms/execution",
		".gtms/results", ".gtms/worktrees", ".gtms/logs",
	} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0755))
	}

	// Create test case
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "gtms/test/cases", "tc-bug029.md"),
		[]byte("---\nid: tc-bug029\ntitle: BUG-029 hash test\n---\n\n## Steps\n1. Test\n"), 0644,
	))

	// Create spec file with known content
	specContent := []byte("#!/usr/bin/env bats\n@test \"example\" { true; }\n")
	specPath := filepath.Join(root, "gtms/automation", "specs", "tc-bug029.bats")
	require.NoError(t, os.WriteFile(specPath, specContent, 0644))

	// Compute expected hash (used by the adapter context to stamp the
	// result contract — see CommandFlags.ArtefactHash below).
	expectedHash, err := pipeline.HashFile(specPath)
	require.NoError(t, err)
	require.NotEmpty(t, expectedHash)

	// Seed wiring with a deliberately stale artefact-hash to prove the
	// execute path stamps the contract from CommandFlags.ArtefactHash
	// (computed at invocation time) and never reads back from wiring.
	if _, err := wiring.Write(root, &wiring.WiringRecord{
		TestCase:     "tc-bug029",
		TestCaseHash: "0011223344556677",
		Framework:    "bats",
		Adapter:      "mock-tier2-hash",
		Artefact:     "gtms/automation/specs/tc-bug029.bats",
		ArtefactHash: "deadbeefdeadbeef",
	}); err != nil {
		require.NoError(t, err)
	}

	// Create Tier 2 adapter script that overwrites result contract WITHOUT artefact-hash
	scriptContent := `#!/bin/sh
cat > "$GTMS_RESULT_FILE" <<EOF
task: ${GTMS_TASK_ID}
command: execute
target: ${GTMS_ARTEFACT_FILE}
adapter: mock-tier2-hash
mode: sync
status: complete
result: pass
summary: "Tests passed"
completed: 2026-01-01T00:00:00Z
EOF
`
	scriptPath := filepath.Join(root, "mock-tier2-hash.sh")
	require.NoError(t, os.WriteFile(scriptPath, []byte(scriptContent), 0755))

	// Create gtms.config with the Tier 2 adapter
	cfgContent := `project:
  name: BUG-029 Test
  repo: org/bug029
adapters:
  execute:
    mock-tier2-hash:
      mode: sync
      framework: bats
      script: mock-tier2-hash.sh
defaults:
  execute: mock-tier2-hash
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "gtms.config"), []byte(cfgContent), 0644))

	cfg, err := config.LoadFromFile(filepath.Join(root, "gtms.config"))
	require.NoError(t, err)

	resolved, err := Resolve(cfg, "execute", "mock-tier2-hash")
	require.NoError(t, err)
	assert.Equal(t, 2, resolved.Tier, "should be Tier 2 adapter")

	flags := CommandFlags{
		ArtefactFile: "gtms/automation/specs/tc-bug029.bats",
		ArtefactHash: expectedHash,
		Framework:    "bats",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-bug029", flags)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "complete", res.Status)

	// CON-023 / ENH-146: the execute path no longer mutates the legacy
	// automation record. The locally-computed artefact hash now lives on
	// the result contract — that's what the reader overlay reads.
	rc, err := result.Read(result.ResultPath(root, res.TaskID))
	require.NoError(t, err)
	assert.Equal(t, expectedHash, rc.ArtefactHash,
		"artefact hash should be carried on the result contract from CommandFlags.ArtefactHash")
	assert.Equal(t, "pass", rc.Result)
	assert.Equal(t, "bats", rc.Framework, "framework should be stamped from task")

	// Wiring byte-stability — execute must not have mutated it even
	// though wiring carried a deliberately stale artefact-hash.
	wiringPath := filepath.Join(root, "gtms", "automation", "wiring", "tc-bug029--bats.wiring.yaml")
	wiringAfter, err := os.ReadFile(wiringPath)
	require.NoError(t, err)
	assert.Contains(t, string(wiringAfter), "deadbeefdeadbeef",
		"wiring's stale artefact-hash must remain untouched on the execute path")
}

// TestBUG083_InvokerRestoresTargetAfterTier2Overwrite verifies that the CLI
// target (tf.Target) is restored on the result contract after a Tier 2 adapter
// overwrites the handoff via heredoc with the artefact path. Without this
// restore, the reader overlay in internal/reader/wiring_scan.go cannot join
// the handoff to wiring (it keys on rc.Target), so gtms status/map/gaps
// silently drop the latest execute result.
func TestBUG083_InvokerRestoresTargetAfterTier2Overwrite(t *testing.T) {
	skipIfShort(t)

	root := t.TempDir()

	for _, dir := range []string{
		"gtms/tasks/pending", "gtms/tasks/complete", "gtms/tasks/error",
		"gtms/tasks/in-progress", "gtms/tasks/in-review",
		"gtms/test/cases", "gtms/automation/wiring", "gtms/automation/specs",
		"gtms/execution",
		".gtms/results", ".gtms/worktrees", ".gtms/logs",
	} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0755))
	}

	require.NoError(t, os.WriteFile(
		filepath.Join(root, "gtms/test/cases", "tc-bug083.md"),
		[]byte("---\nid: tc-bug083\ntitle: BUG-083 target restore test\n---\n\n## Steps\n1. Test\n"), 0644,
	))

	specContent := []byte("#!/usr/bin/env bats\n@test \"example\" { true; }\n")
	specPath := filepath.Join(root, "gtms/automation", "specs", "tc-bug083.bats")
	require.NoError(t, os.WriteFile(specPath, specContent, 0644))

	specHash, err := pipeline.HashFile(specPath)
	require.NoError(t, err)
	require.NotEmpty(t, specHash)

	if _, err := wiring.Write(root, &wiring.WiringRecord{
		TestCase:     "tc-bug083",
		TestCaseHash: "0011223344556677",
		Framework:    "bats",
		Adapter:      "mock-tier2-target-overwriter",
		Artefact:     "gtms/automation/specs/tc-bug083.bats",
		ArtefactHash: specHash,
	}); err != nil {
		require.NoError(t, err)
	}

	// Mock Tier 2 adapter that mirrors the real bats-runner heredoc: writes
	// target: ${GTMS_ARTEFACT_FILE} instead of the CLI target.
	scriptContent := `#!/bin/sh
cat > "$GTMS_RESULT_FILE" <<EOF
task: ${GTMS_TASK_ID}
command: execute
target: ${GTMS_ARTEFACT_FILE}
adapter: mock-tier2-target-overwriter
mode: sync
status: complete
result: pass
summary: "Tests passed"
completed: 2026-01-01T00:00:00Z
EOF
`
	scriptPath := filepath.Join(root, "mock-tier2-target-overwriter.sh")
	require.NoError(t, os.WriteFile(scriptPath, []byte(scriptContent), 0755))

	cfgContent := `project:
  name: BUG-083 Test
  repo: org/bug083
adapters:
  execute:
    mock-tier2-target-overwriter:
      mode: sync
      framework: bats
      script: mock-tier2-target-overwriter.sh
defaults:
  execute: mock-tier2-target-overwriter
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "gtms.config"), []byte(cfgContent), 0644))

	cfg, err := config.LoadFromFile(filepath.Join(root, "gtms.config"))
	require.NoError(t, err)

	resolved, err := Resolve(cfg, "execute", "mock-tier2-target-overwriter")
	require.NoError(t, err)
	assert.Equal(t, 2, resolved.Tier, "should be Tier 2 adapter")

	flags := CommandFlags{
		ArtefactFile: "gtms/automation/specs/tc-bug083.bats",
		ArtefactHash: specHash,
		Framework:    "bats",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-bug083", flags)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "complete", res.Status)

	rc, err := result.Read(result.ResultPath(root, res.TaskID))
	require.NoError(t, err)
	assert.Equal(t, "tc-bug083", rc.Target,
		"target should be restored to the CLI-supplied tf.Target after Tier 2 overwrite")
	assert.NotContains(t, rc.Target, ".bats",
		"target must not carry the artefact path after restore")
	assert.Equal(t, "pass", rc.Result)
	assert.Equal(t, "bats", rc.Framework, "framework should be stamped from task")
}
