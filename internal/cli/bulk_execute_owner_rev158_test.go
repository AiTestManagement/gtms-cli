package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// REV-158 CODEX-002: bulk execute resolves an owner and uses it for the drift
// check, then omitted it from CommandFlags. InvokeWithRoot consequently exposed
// an empty TestCaseFile and OutputSubdir to execute adapters, so bulk and
// single-TC execution gave different, incorrect adapter contracts for the same
// test case, and manual execution could not compute its current test-case hash.
func TestRunBulkExecute_PassesOwningSpecToAdapter(t *testing.T) {
	skipIfShort(t)
	root := setupCompatProject(t)
	spec := seedBulkSpec(t, root, "bulkexec", "tc-bbbb2222", "tc-bbbb2222-login.md")

	artefactDir := filepath.Join(root, "test", "acceptance")
	require.NoError(t, os.MkdirAll(artefactDir, 0o755))
	artefact := filepath.Join(artefactDir, "tc-bbbb2222.bats")
	require.NoError(t, os.WriteFile(artefact, []byte("@test \"x\" { true; }\n"), 0o644))

	artefactHash, herr := pipeline.HashFile(artefact)
	require.NoError(t, herr)
	specHash, serr := pipeline.HashFile(spec)
	require.NoError(t, serr)
	_, werr := wiring.Write(root, &wiring.WiringRecord{
		TestCase: "tc-bbbb2222", TestCaseHash: specHash, Framework: "bats",
		Adapter: "bats-runner", Artefact: "test/acceptance/tc-bbbb2222.bats", ArtefactHash: artefactHash,
	})
	require.NoError(t, werr)

	marker := filepath.ToSlash(filepath.Join(root, "exec-owner-source.txt"))
	writeMockScript(t, root, "mock-bulk-execute.sh", `#!/bin/bash
printf '%s' "${GTMS_TESTCASE_FILE}" > "`+marker+`"
{
  echo "task: ${GTMS_TASK_ID}"
  echo "command: execute"
  echo "target: ${GTMS_TESTCASE}"
  echo "adapter: bats-runner"
  echo "mode: sync"
  echo "status: complete"
  echo "result: pass"
  echo "attempts: 1"
  echo "summary: \"ran\""
  echo "completed: \"2026-01-01T00:00:00Z\""
} > "${GTMS_RESULT_FILE}"
`)

	cfg := &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"bats-runner": {Mode: "sync", Script: "testdata/mock-bulk-execute.sh", Framework: "bats"},
			},
		},
		Defaults: map[string]string{"execute": "bats-runner"},
	}
	restore := withExecuteGlobals(t, root, cfg)
	defer restore()

	out, err := runCmdCaptureStderr(t, newExecuteCmd(), "bulkexec")
	require.NoError(t, err, "bulk execute failed: %s", out)

	got, readErr := os.ReadFile(filepath.Join(root, "exec-owner-source.txt"))
	require.NoError(t, readErr, "the execute adapter did not run")
	source := strings.TrimSpace(string(got))
	require.NotEmpty(t, source, "bulk execute handed the adapter an EMPTY test-case source")
	assert.Equal(t, "gtms/test/cases/bulkexec/tc-bbbb2222-login.md", filepath.ToSlash(source),
		"bulk execute must give the adapter the same owning spec the single-TC path does")
}
