package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
	"github.com/aitestmanagement/gtms-cli/internal/task"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// enh231AcceptConfig is batsExecuteConfig with an explicit on-script-change
// policy. Empty policy == absent block == accept default.
func enh231Config(t *testing.T, policy string) *config.Config {
	t.Helper()
	cfg := batsExecuteConfig()
	if policy != "" {
		cfg.Execute = &config.ExecuteConfig{OnScriptChange: policy}
	}
	return cfg
}

func enh231Spec(t *testing.T, root, folder, tcID string) {
	t.Helper()
	writeTestFile(t, root, filepath.Join("gtms/test/cases", folder, tcID+"-x.md"), `---
gtms_schema: "test-case/v2"
type: Test Case Spec
test_type: Functional
test_case_id: `+tcID+`
title: ENH-231 fixture
---
`)
}

func wiringArtefactHash(t *testing.T, root, tcID string) string {
	t.Helper()
	rec, _, err := wiring.Find(root, tcID, "bats")
	require.NoError(t, err)
	require.NotNil(t, rec)
	return rec.ArtefactHash
}

func countHandoffs(t *testing.T, root string) int {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(root, ".gtms", "results", "*.handoff.yaml"))
	return len(m)
}

func runBulk(t *testing.T, root, folder string, cfg *config.Config, force bool) (string, error) {
	t.Helper()
	restore := withExecuteGlobals(t, root, cfg)
	defer restore()
	cmd := &cobra.Command{Use: "execute"}
	cmd.SetContext(context.Background())
	var err error
	out := captureStderr(t, func() {
		err = runBulkExecute(cmd, root, folder, "", "", "", "", force, false, false, false)
	})
	return out, err
}

// tc-f1e5a367 (Go-level): a script-only edit overrides the already-passing skip
// for the one accepting run; the accepted wiring takes the new hash.
func TestENH231_BulkScriptDriftOverridesAlreadyPassing(t *testing.T) {
	skipIfShort(t)
	root := t.TempDir()
	enh231Spec(t, root, "drift", "tc-11111111")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-11111111", Framework: "bats", Adapter: "bats-runner", Result: "pass"})

	artefact := filepath.Join(root, "test/acceptance/tc-11111111.bats")
	require.NoError(t, os.WriteFile(artefact, []byte("# fixture artefact for tc-11111111\n# edited for ENH-231\n"), 0644))
	newArt, err := pipeline.HashFile(artefact)
	require.NoError(t, err)
	require.NotEqual(t, newArt, wiringArtefactHash(t, root, "tc-11111111"))

	_, err = runBulk(t, root, "drift", enh231Config(t, ""), false)
	require.NoError(t, err)

	assert.Equal(t, newArt, wiringArtefactHash(t, root, "tc-11111111"),
		"accept mode must refresh the artefact-hash for the accepting run")
}

// tc-5fc0af74 (Go-level): refuse mode neither runs nor re-wires a script-only
// change in bulk; a wiring file stays byte-for-byte.
func TestENH231_BulkRefuseDoesNotAcceptOrRun(t *testing.T) {
	root := t.TempDir()
	enh231Spec(t, root, "drift", "tc-11111111")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-11111111", Framework: "bats", Adapter: "bats-runner", Result: "pass"})
	before := wiringArtefactHash(t, root, "tc-11111111")
	handoffsBefore := countHandoffs(t, root)

	artefact := filepath.Join(root, "test/acceptance/tc-11111111.bats")
	require.NoError(t, os.WriteFile(artefact, []byte("# fixture artefact for tc-11111111\n# edited\n"), 0644))

	out, err := runBulk(t, root, "drift", enh231Config(t, config.OnScriptChangeRefuse), false)
	// BUG-200: a refuse-mode script change is now "not run" and exits non-zero.
	// ENH-222: the plain-language reason is "script changed".
	require.Error(t, err, "BUG-200: a bulk refusal must exit non-zero")
	assert.Contains(t, out, "not run (script changed)")
	assert.Equal(t, before, wiringArtefactHash(t, root, "tc-11111111"), "refuse mode must not re-wire")
	assert.Equal(t, handoffsBefore, countHandoffs(t, root), "refuse mode must not dispatch a run")
}

// tc-828245de (Go-level): with both spec and script drift in accept mode, the
// spec refusal wins and the wiring is not re-written.
func TestENH231_BulkSpecAndScriptDriftSpecRefusalWins(t *testing.T) {
	root := t.TempDir()
	enh231Spec(t, root, "drift", "tc-11111111")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-11111111", Framework: "bats", Adapter: "bats-runner", Result: "pass"})
	before := wiringArtefactHash(t, root, "tc-11111111")

	// Drift BOTH the spec and the artefact.
	spec := filepath.Join(root, "gtms/test/cases/drift/tc-11111111-x.md")
	f, err := os.OpenFile(spec, os.O_APPEND|os.O_WRONLY, 0644)
	require.NoError(t, err)
	_, _ = f.WriteString("\nAppended step.\n")
	require.NoError(t, f.Close())
	artefact := filepath.Join(root, "test/acceptance/tc-11111111.bats")
	require.NoError(t, os.WriteFile(artefact, []byte("# fixture artefact for tc-11111111\n# edited\n"), 0644))

	out, err := runBulk(t, root, "drift", enh231Config(t, ""), false)
	// BUG-200: spec drift is now "not run" and exits non-zero.
	// ENH-222: the plain-language reason is "spec changed"; spec drift leads.
	require.Error(t, err, "BUG-200: spec drift refusal must exit non-zero")
	assert.Contains(t, out, "not run (spec changed)")
	assert.Equal(t, before, wiringArtefactHash(t, root, "tc-11111111"),
		"spec drift must block any artefact-hash update")
}

// tc-75c214bd (Go-level): bulk --force with current wiring and an active wired
// automated execute task skips with "active task exists" and does not dispatch.
func TestENH231_BulkForceRespectsActiveTask(t *testing.T) {
	root := t.TempDir()
	enh231Spec(t, root, "drift", "tc-44444444")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-44444444", Framework: "bats", Adapter: "bats-runner", Result: "pass"})
	before := wiringArtefactHash(t, root, "tc-44444444")

	// Seed an active (in-progress) execute task for the TC.
	_, err := task.Create(root, &task.TaskFile{
		ID:      "task-active01",
		Type:    "execute",
		Target:  "tc-44444444",
		Adapter: "bats-runner",
		Status:  "in-progress",
		Created: "2026-10-23T00:00:00Z",
	}, "active task fixture")
	require.NoError(t, err)
	handoffsBefore := countHandoffs(t, root)

	out, err := runBulk(t, root, "drift", enh231Config(t, ""), true /* force */)
	require.NoError(t, err)
	assert.Contains(t, out, "active task exists", "--force must not bypass the active-task guard")
	assert.Equal(t, before, wiringArtefactHash(t, root, "tc-44444444"))
	assert.Equal(t, handoffsBefore, countHandoffs(t, root), "no second run may be dispatched")
}
