package adapter

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/aitestmanagement/gtms-cli/internal/task"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// setupAutomateTestProject creates a project structure for automate tests.
//
// CON-023 / ENH-145: post-cutover, automate writes wiring records under
// gtms/automation/wiring/ and requires (a) a test case spec on disk so
// testcase-hash can be computed, (b) the produced artefact on disk so
// artefact-hash can be computed, and (c) at least one execute adapter
// configured for the relevant framework so wiring.adapter resolves.
// The fixture below provides all three.
func setupAutomateTestProject(t *testing.T) (string, *config.Config) {
	t.Helper()
	root := t.TempDir()

	for _, dir := range []string{
		"gtms/tasks/pending", "gtms/tasks/complete", "gtms/tasks/error",
		"gtms/tasks/in-progress", "gtms/tasks/in-review",
		"gtms/cases", "gtms/automation/wiring", "gtms/automation/specs",
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
		filepath.Join(root, "gtms/cases", "tc-auto-test.md"),
		[]byte(testCaseContent), 0644,
	))

	// Create mock Tier 2 automate script. It writes a real artefact file so
	// the post-automate wiring write can hash it.
	scriptContent := `#!/bin/bash
mkdir -p "$(dirname gtms/automation/specs/tc-auto.spec.ts)"
echo "generated spec body" > gtms/automation/specs/tc-auto.spec.ts
cat > "${GTMS_RESULT_FILE}" <<EOF
task: ${GTMS_TASK_ID}
command: automate
target: ${GTMS_TESTCASE}
adapter: mock-automate-tier2
mode: sync
status: complete
result: pass
artefact: gtms/automation/specs/tc-auto.spec.ts
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
      command: 'printf ''automation generated for {testcase}\n<gtms-file name="tc-auto.spec.ts">\nspec body\n</gtms-file>\n'''
      framework: playwright
      output-dir: gtms/automation/specs
    mock-tier2:
      mode: sync
      script: testdata/mock-automate.sh
      framework: playwright
  execute:
    playwright-runner:
      mode: sync
      command: 'echo execute'
      framework: playwright
    cypress-runner:
      mode: sync
      command: 'echo execute'
      framework: cypress
defaults:
  automate: mock-tier1
  execute: playwright-runner
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
	// CON-023 / ENH-145: streaming captured one artefact via the <gtms-file>
	// marker; the summary describes the capture rather than echoing stdout.
	assert.Contains(t, res.Summary, "Captured 1 file(s)")
	assert.Contains(t, res.Summary, "tc-auto.spec.ts")

	// Verify task file
	completeTasks, err := task.List(root, "complete")
	require.NoError(t, err)
	require.Len(t, completeTasks, 1)

	tf := completeTasks[0]
	assert.Equal(t, "automate", tf.Type)
	assert.Equal(t, "tc-auto", tf.Target)
	assert.Equal(t, "playwright", tf.Framework)
	assert.Equal(t, "gtms/cases/tc-auto-test.md", tf.Reference)

	// Verify wiring record was created (CON-023 / ENH-145).
	rec, _, err := wiring.Find(root, "tc-auto", "playwright")
	require.NoError(t, err)
	require.NotNil(t, rec, "wiring record should be created after sync automate")
	assert.Equal(t, "tc-auto", rec.TestCase)
	assert.Equal(t, "playwright", rec.Framework)
	assert.Equal(t, "playwright-runner", rec.Adapter,
		"wiring.adapter is the canonical EXECUTE adapter, not the automate adapter")
	assert.NotEmpty(t, rec.TestCaseHash)
	assert.NotEmpty(t, rec.ArtefactHash)
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
	assert.Equal(t, "gtms/automation/specs/tc-auto.spec.ts", rc.Artefact)

	// Verify wiring record (CON-023 / ENH-145).
	rec, _, err := wiring.Find(root, "tc-auto", "playwright")
	require.NoError(t, err)
	require.NotNil(t, rec)
	assert.Equal(t, "gtms/automation/specs/tc-auto.spec.ts", rec.Artefact)
	assert.Equal(t, "playwright-runner", rec.Adapter)
}

func TestAutomate_ContextFile_Tier1(t *testing.T) {
	skipIfShort(t)
	root, cfg := setupAutomateTestProject(t)

	// Create a context file with known content
	contextContent := "Use async/await, not callbacks. Follow REST conventions."
	contextFile := filepath.Join(root, "testdata", "coding-standards.md")
	require.NoError(t, os.WriteFile(contextFile, []byte(contextContent), 0644))

	// Use a Tier 1 adapter that echoes {context} in output
	resolved := &ResolvedAdapter{
		Command: "automate", Name: "ctx-file-test",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `echo "context={context}"`},
		Tier:    1, Mode: "sync",
	}

	flags := CommandFlags{
		Framework:   "playwright",
		ContextFile: contextFile,
		Context:     contextContent,
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-auto", flags)
	require.NoError(t, err)

	assert.Equal(t, "complete", res.Status)
	assert.Contains(t, res.Summary, "Use async/await, not callbacks. Follow REST conventions.")
}

func TestAutomate_ContextFile_Tier2Env(t *testing.T) {
	skipIfShort(t)
	root := t.TempDir()

	for _, dir := range []string{
		"gtms/tasks/pending", "gtms/tasks/complete", "gtms/tasks/error",
		"gtms/tasks/in-progress", "gtms/tasks/in-review",
		"gtms/cases", "gtms/automation/wiring", "gtms/automation/specs",
		".gtms/results", ".gtms/worktrees", ".gtms/logs",
		"testdata",
	} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0755))
	}

	// Create test case file
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "gtms/cases", "tc-auto-test.md"),
		[]byte("---\nid: tc-auto\ntitle: Test\n---\n"), 0644,
	))

	// Create context file
	contextContent := "CONTEXT_MARKER_FOR_TEST"
	contextFile := filepath.Join(root, "testdata", "context.md")
	require.NoError(t, os.WriteFile(contextFile, []byte(contextContent), 0644))

	// Create Tier 2 script that echoes GTMS_CONTEXT into result summary
	scriptContent := `#!/bin/bash
cat > "${GTMS_RESULT_FILE}" <<EOF
task: ${GTMS_TASK_ID}
command: automate
target: ${GTMS_TESTCASE}
adapter: mock-ctx-tier2
mode: sync
status: complete
result: pass
artefact: gtms/automation/specs/tc-auto.spec.ts
attempts: 1
summary: "context=${GTMS_CONTEXT}"
completed: "2025-02-14T12:00:00Z"
EOF
`
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "testdata", "mock-ctx-automate.sh"),
		[]byte(scriptContent), 0755,
	))

	cfgContent := `project:
  name: Context Test
  repo: org/ctx-test
adapters:
  automate:
    mock-ctx-tier2:
      mode: sync
      script: testdata/mock-ctx-automate.sh
defaults:
  automate: mock-ctx-tier2
`
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "gtms.config"),
		[]byte(cfgContent), 0644,
	))

	cfg, err := config.LoadFromFile(filepath.Join(root, "gtms.config"))
	require.NoError(t, err)

	resolved, err := Resolve(cfg, "automate", "mock-ctx-tier2")
	require.NoError(t, err)

	flags := CommandFlags{
		Framework:   "playwright",
		ContextFile: contextFile,
		Context:     contextContent,
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-auto", flags)
	require.NoError(t, err)

	assert.Equal(t, "complete", res.Status)
	assert.Contains(t, res.Summary, "context=CONTEXT_MARKER_FOR_TEST")
}

func TestAutomate_NoContextFile_EmptyContext(t *testing.T) {
	skipIfShort(t)
	root, cfg := setupAutomateTestProject(t)

	// Use Tier 1 adapter that echoes {context}
	resolved := &ResolvedAdapter{
		Command: "automate", Name: "no-ctx-test",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `echo "context={context} end"`},
		Tier:    1, Mode: "sync",
	}

	// No ContextFile or Context set
	flags := CommandFlags{Framework: "playwright"}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-auto", flags)
	require.NoError(t, err)

	assert.Equal(t, "complete", res.Status)
	// {context} should be replaced with empty string — shell may add quotes around empty value
	assert.Contains(t, res.Summary, "end")
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

// --- CON-023 / ENH-145 canonical-adapter fallback diagnostic ---

// TestWriteAutomateWiring_FallbackDiagnostic_NamesCompetingAdapters pins
// the contract that when multiple execute adapters match the wiring
// framework and no default selects one, WriteAutomateWiring still writes
// the wiring file but returns a one-line warning naming the chosen
// adapter and the competing matches. The caller (invoker handleSyncResult
// or async status_common.go) is expected to thread these into
// InvokeResult.Warnings so the user sees them in CLI output.
//
// This is the safety net against "wiring silently nailed a
// lexically-first execute adapter that wasn't what the user expected."
func TestWriteAutomateWiring_FallbackDiagnostic_NamesCompetingAdapters(t *testing.T) {
	root := t.TempDir()

	// Seed a TC spec so the writer can compute testcase-hash.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/cases"), 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "gtms/cases", "tc-fb01-spec.md"),
		[]byte("---\nid: tc-fb01\n---\nbody\n"), 0644))

	// Seed a produced artefact so artefact-hash can be computed.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "test/acceptance"), 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "test/acceptance/tc-fb01.bats"),
		[]byte("# bats artefact"), 0644))

	// Three execute adapters all matching framework=bats, no default —
	// triggers the lexically-first fallback.
	cfg := &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"bats-runner":      {Framework: "bats", Mode: "sync", Command: "echo ok"},
				"remote-bats":      {Framework: "bats", Mode: "sync", Command: "echo ok"},
				"remote-bats-lean": {Framework: "bats", Mode: "sync", Command: "echo ok"},
			},
		},
		Defaults: map[string]string{},
	}

	tf := &task.TaskFile{
		ID: "task-fb01", Type: "automate", Target: "tc-fb01",
		Status: "complete", Framework: "bats",
	}
	rc := &result.ResultContract{
		Task: "task-fb01", Command: "automate", Target: "tc-fb01",
		Adapter: "some-automate-adapter", Mode: "sync", Status: "complete",
		Result: "pass", Artefact: "test/acceptance/tc-fb01.bats",
	}

	warnings, err := WriteAutomateWiring(root, cfg, tf, rc)
	require.NoError(t, err)
	require.Len(t, warnings, 1, "exactly one fallback-diagnostic warning")

	w := warnings[0]
	assert.Contains(t, w, "bats", "warning must name the framework")
	assert.Contains(t, w, "bats-runner", "warning must name the chosen adapter (lexically first)")
	assert.Contains(t, w, "remote-bats", "warning must name the competing adapter")
	assert.Contains(t, w, "remote-bats-lean", "warning must name the competing adapter")
	assert.Contains(t, w, "defaults.execute",
		"warning must point the user at the gtms.config knob that suppresses it")

	// Wiring must still be written using the chosen adapter — the
	// warning is a hint, not a refusal.
	rec, _, _ := wiring.Find(root, "tc-fb01", "bats")
	require.NotNil(t, rec)
	assert.Equal(t, "bats-runner", rec.Adapter)
}

// TestWriteAutomateWiring_DefaultSelectsAdapter_NoWarning: when
// defaults.execute pins one of the matching adapters, the resolver takes
// the single-default fast path and no warning is emitted.
func TestWriteAutomateWiring_DefaultSelectsAdapter_NoWarning(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/cases"), 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "gtms/cases", "tc-fb02-spec.md"),
		[]byte("---\nid: tc-fb02\n---\nbody\n"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "test/acceptance"), 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "test/acceptance/tc-fb02.bats"),
		[]byte("# bats artefact"), 0644))

	cfg := &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"bats-runner": {Framework: "bats", Mode: "sync", Command: "echo ok"},
				"remote-bats": {Framework: "bats", Mode: "sync", Command: "echo ok"},
			},
		},
		Defaults: map[string]string{"execute": "remote-bats"},
	}

	tf := &task.TaskFile{
		ID: "task-fb02", Type: "automate", Target: "tc-fb02",
		Status: "complete", Framework: "bats",
	}
	rc := &result.ResultContract{
		Task: "task-fb02", Command: "automate", Target: "tc-fb02",
		Adapter: "some-automate-adapter", Mode: "sync", Status: "complete",
		Result: "pass", Artefact: "test/acceptance/tc-fb02.bats",
	}

	warnings, err := WriteAutomateWiring(root, cfg, tf, rc)
	require.NoError(t, err)
	assert.Empty(t, warnings,
		"single-default fast path must not emit a fallback warning")

	rec, _, _ := wiring.Find(root, "tc-fb02", "bats")
	require.NotNil(t, rec)
	assert.Equal(t, "remote-bats", rec.Adapter)
}

// --- BUG-057 path-safety tests on the automate write side ---

// minimalBatsConfig produces a one-adapter execute config for the BUG-057
// automate tests below. The fallback-diagnostic suite already exercises
// multi-match adapters; here we only need a deterministic single-match.
func minimalBatsConfig() *config.Config {
	return &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"bats-runner": {Framework: "bats", Mode: "sync", Command: "echo ok"},
			},
		},
		Defaults: map[string]string{"execute": "bats-runner"},
	}
}

// TestWriteAutomateWiring_RejectsRelativeTraversalOutsideRoot pins that
// an adapter-produced artefact path with `..` segments escaping
// projectRoot is refused at the write-side; no wiring file is created.
func TestWriteAutomateWiring_RejectsRelativeTraversalOutsideRoot(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/cases"), 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "gtms/cases", "tc-ps01-spec.md"),
		[]byte("---\nid: tc-ps01\n---\nbody\n"), 0644))

	// File outside root that `..` would land on.
	parent := filepath.Dir(root)
	escape := filepath.Join(parent, "automate-traversal-target.txt")
	require.NoError(t, os.WriteFile(escape, []byte("outside"), 0644))
	t.Cleanup(func() { _ = os.Remove(escape) })

	tf := &task.TaskFile{
		ID: "task-ps01", Type: "automate", Target: "tc-ps01",
		Status: "complete", Framework: "bats",
	}
	rc := &result.ResultContract{
		Task: "task-ps01", Command: "automate", Target: "tc-ps01",
		Adapter: "some-automate-adapter", Mode: "sync", Status: "complete",
		Result: "pass", Artefact: "../automate-traversal-target.txt",
	}

	warnings, err := WriteAutomateWiring(root, minimalBatsConfig(), tf, rc)
	require.Error(t, err)
	assert.True(t, pathsafe.IsPathSafetyError(err),
		"adapter-produced traversal artefact must yield *pathsafe.PathSafetyError")
	assert.Empty(t, warnings)

	rec, _, _ := wiring.Find(root, "tc-ps01", "bats")
	assert.Nil(t, rec, "no wiring should be written for an unsafe adapter artefact")
}

// TestWriteAutomateWiring_RejectsAbsoluteOutsideRoot pins that an
// absolute artefact path outside the project root is refused.
func TestWriteAutomateWiring_RejectsAbsoluteOutsideRoot(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/cases"), 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "gtms/cases", "tc-ps02-spec.md"),
		[]byte("---\nid: tc-ps02\n---\nbody\n"), 0644))

	outside := t.TempDir()
	outsidePath := filepath.Join(outside, "outside-target.txt")
	require.NoError(t, os.WriteFile(outsidePath, []byte("outside"), 0644))

	tf := &task.TaskFile{
		ID: "task-ps02", Type: "automate", Target: "tc-ps02",
		Status: "complete", Framework: "bats",
	}
	rc := &result.ResultContract{
		Task: "task-ps02", Command: "automate", Target: "tc-ps02",
		Adapter: "some-automate-adapter", Mode: "sync", Status: "complete",
		Result: "pass", Artefact: outsidePath,
	}

	_, err := WriteAutomateWiring(root, minimalBatsConfig(), tf, rc)
	require.Error(t, err)
	assert.True(t, pathsafe.IsPathSafetyError(err),
		"absolute outside-root artefact must yield *pathsafe.PathSafetyError")

	rec, _, _ := wiring.Find(root, "tc-ps02", "bats")
	assert.Nil(t, rec)
}

// TestWriteAutomateWiring_AbsoluteInsideRootNormalisesToRelative pins
// that wiring never stores an absolute path verbatim — an
// absolute-inside-root artefact is normalised to project-relative
// slash form.
func TestWriteAutomateWiring_AbsoluteInsideRootNormalisesToRelative(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/cases"), 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "gtms/cases", "tc-ps03-spec.md"),
		[]byte("---\nid: tc-ps03\n---\nbody\n"), 0644))

	require.NoError(t, os.MkdirAll(filepath.Join(root, "test/acceptance"), 0755))
	absArtefact := filepath.Join(root, "test", "acceptance", "tc-ps03.bats")
	require.NoError(t, os.WriteFile(absArtefact, []byte("# bats"), 0644))

	tf := &task.TaskFile{
		ID: "task-ps03", Type: "automate", Target: "tc-ps03",
		Status: "complete", Framework: "bats",
	}
	rc := &result.ResultContract{
		Task: "task-ps03", Command: "automate", Target: "tc-ps03",
		Adapter: "some-automate-adapter", Mode: "sync", Status: "complete",
		Result: "pass", Artefact: absArtefact,
	}

	_, err := WriteAutomateWiring(root, minimalBatsConfig(), tf, rc)
	require.NoError(t, err)

	rec, _, _ := wiring.Find(root, "tc-ps03", "bats")
	require.NotNil(t, rec)
	assert.Equal(t, "test/acceptance/tc-ps03.bats", rec.Artefact,
		"absolute inside-root artefact must be normalised to project-relative slash form")
}
