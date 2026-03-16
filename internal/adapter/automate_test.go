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

// setupAutomateTestProject creates a project structure for automate tests.
func setupAutomateTestProject(t *testing.T) (string, *config.Config) {
	t.Helper()
	root := t.TempDir()

	for _, dir := range []string{
		"test-tasks/pending", "test-tasks/complete", "test-tasks/failed",
		"test-tasks/in-progress", "test-tasks/in-review",
		"test-cases", "test-automation/records", "test-automation/specs",
		".gtms/results", ".gtms/worktrees", ".gtms/logs",
		"testdata",
	} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0755))
	}

	// Create a test case file
	testCaseContent := `---
id: tc-auto
title: Automate Test Case
---

## Steps
1. Do something
`
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "test-cases", "tc-auto-test.md"),
		[]byte(testCaseContent), 0644,
	))

	// Create mock Tier 2 automate script
	scriptContent := `#!/bin/bash
cat > "${GTMS_RESULT_FILE}" <<EOF
task: ${GTMS_TASK_ID}
command: automate
target: ${GTMS_TESTCASE}
adapter: mock-automate-tier2
mode: sync
status: complete
artefact: test-automation/specs/tc-auto.spec.ts
attempts: 1
summary: "Spec file generated"
completed: "2025-02-14T12:00:00Z"
EOF
`
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "testdata", "mock-automate.sh"),
		[]byte(scriptContent), 0755,
	))

	cfgContent := `project:
  name: Automate Test
  repo: org/auto-test
adapters:
  automate:
    mock-tier1:
      mode: sync
      command: 'echo "automation generated for {testcase}"'
    mock-tier2:
      mode: sync
      script: testdata/mock-automate.sh
defaults:
  automate: mock-tier1
`
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "gtms.config"),
		[]byte(cfgContent), 0644,
	))

	cfg, err := config.LoadFromFile(filepath.Join(root, "gtms.config"))
	require.NoError(t, err)

	return root, cfg
}

func TestAutomate_Tier1Sync(t *testing.T) {
	skipIfShort(t)
	root, cfg := setupAutomateTestProject(t)

	resolved, err := Resolve(cfg, "automate", "mock-tier1")
	require.NoError(t, err)
	assert.Equal(t, 1, resolved.Tier)

	flags := CommandFlags{Framework: "playwright"}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-auto", flags)
	require.NoError(t, err)

	assert.Equal(t, "complete", res.Status)
	assert.Equal(t, "mock-tier1", res.Adapter)
	assert.Contains(t, res.Summary, "automation generated for tc-auto")

	// Verify task file
	completeTasks, err := task.List(root, "complete")
	require.NoError(t, err)
	require.Len(t, completeTasks, 1)

	tf := completeTasks[0]
	assert.Equal(t, "automate", tf.Type)
	assert.Equal(t, "tc-auto", tf.Target)
	assert.Equal(t, "playwright", tf.Framework)
	assert.Equal(t, "test-cases/tc-auto-test.md", tf.Reference)

	// Verify automation record was created
	record, _, err := pipeline.FindAutomationRecord(root, "tc-auto")
	require.NoError(t, err)
	require.NotNil(t, record, "Automation record should be created after sync automate")
	assert.Equal(t, "tc-auto", record.TestCase)
	assert.Equal(t, "playwright", record.Framework)
	assert.Equal(t, "developed", record.Status)
	assert.Equal(t, "pass", record.LastDevResult)
	assert.Equal(t, 1, record.Cycle)
}

func TestAutomate_Tier2Sync(t *testing.T) {
	skipIfShort(t)
	root, cfg := setupAutomateTestProject(t)

	resolved, err := Resolve(cfg, "automate", "mock-tier2")
	require.NoError(t, err)
	assert.Equal(t, 2, resolved.Tier)

	flags := CommandFlags{Framework: "playwright"}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-auto", flags)
	require.NoError(t, err)

	assert.Equal(t, "complete", res.Status)
	assert.Equal(t, "mock-tier2", res.Adapter)

	// Verify result contract
	rcPath := result.ResultPath(root, res.TaskID)
	rc, err := result.Read(rcPath)
	require.NoError(t, err)
	assert.Equal(t, "complete", rc.Status)
	assert.Equal(t, "test-automation/specs/tc-auto.spec.ts", rc.Artefact)

	// Verify automation record
	record, _, err := pipeline.FindAutomationRecord(root, "tc-auto")
	require.NoError(t, err)
	require.NotNil(t, record)
	assert.Equal(t, "developed", record.Status)
	assert.Equal(t, "test-automation/specs/tc-auto.spec.ts", record.Artefact)
}

func TestAutomate_ContextFields(t *testing.T) {
	skipIfShort(t)
	root, cfg := setupAutomateTestProject(t)

	resolved := &ResolvedAdapter{
		Command: "automate", Name: "ctx-test",
		Config: &config.AdapterConfig{Mode: "sync", Command: `echo "tc={testcase} out={output_dir}"`},
		Tier: 1, Mode: "sync",
	}

	flags := CommandFlags{Framework: "cypress"}
	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-auto", flags)
	require.NoError(t, err)

	assert.Equal(t, "complete", res.Status)
	assert.Contains(t, res.Summary, "tc=tc-auto")
	// OutputDir for automate should be automation/specs
	assert.Contains(t, res.Summary, "automation")
}
