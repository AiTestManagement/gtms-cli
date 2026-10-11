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

// seedBulkSpec writes one spec into a folder under gtms/test/cases/.
func seedBulkSpec(t *testing.T, root, folder, tcID, filename string) string {
	t.Helper()
	dir := filepath.Join(root, "gtms", "test", "cases", folder)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	abs := filepath.Join(dir, filename)
	body := "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: " + tcID + "\ntitle: " + tcID + "\n---\n\n## Steps\n1. do\n"
	require.NoError(t, os.WriteFile(abs, []byte(body), 0o644))
	return abs
}

// writeMockScript writes an executable Tier 2 adapter script.
func writeMockScript(t *testing.T, root, name, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "testdata"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "testdata", name), []byte(body), 0o755))
}

// REV-158 CODEX-001: bulk automate iterates resolved owners but used to drop the
// owner when building CommandFlags. The adapter therefore received an empty
// source path and empty test-case content, and the post-invocation
// WriteAutomateWiring failed with "no resolved owner" -- a failure downgraded to
// a warning, so bulk automate reported success while writing NO wiring record.
//
// Both halves the reviewer named are asserted: the adapter is handed a non-empty
// owning spec, and the run writes wiring carrying that spec's hash.
func TestRunBulkAutomate_PassesResolvedOwnerAndWritesWiring(t *testing.T) {
	skipIfShort(t)
	root := setupCompatProject(t)
	spec := seedBulkSpec(t, root, "bulkauto", "tc-aaaa1111", "tc-aaaa1111-login.md")

	marker := filepath.ToSlash(filepath.Join(root, "owner-source.txt"))
	writeMockScript(t, root, "mock-bulk-automate.sh", `#!/bin/bash
printf '%s' "${GTMS_TESTCASE_FILE}" > "`+marker+`"
mkdir -p gtms/automation/specs
echo "spec body" > "gtms/automation/specs/${GTMS_TESTCASE}.bats"
{
  echo "task: ${GTMS_TASK_ID}"
  echo "command: automate"
  echo "target: ${GTMS_TESTCASE}"
  echo "adapter: mock-bulk-automate"
  echo "mode: sync"
  echo "status: complete"
  echo "result: pass"
  echo "artefact: gtms/automation/specs/${GTMS_TESTCASE}.bats"
  echo "attempts: 1"
  echo "summary: \"generated\""
  echo "completed: \"2026-01-01T00:00:00Z\""
} > "${GTMS_RESULT_FILE}"
`)

	cfg := &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"automate": {
				"mock-bulk-automate": {Mode: "sync", Script: "testdata/mock-bulk-automate.sh", Framework: "bats"},
			},
			"execute": {
				"bats-runner": {Mode: "sync", Command: "echo PASS", Framework: "bats"},
			},
		},
		Defaults: map[string]string{"automate": "mock-bulk-automate", "execute": "bats-runner"},
	}
	restore := withExecuteGlobals(t, root, cfg)
	defer restore()

	out, err := runCmdCaptureStderr(t, newAutomateCmd(), "bulkauto")
	require.NoError(t, err, "bulk automate failed: %s", out)

	// 1. The adapter received the OWNING spec, not an empty source.
	got, readErr := os.ReadFile(filepath.Join(root, "owner-source.txt"))
	require.NoError(t, readErr, "the adapter did not run")
	source := strings.TrimSpace(string(got))
	require.NotEmpty(t, source, "bulk automate handed the adapter an EMPTY test-case source")
	assert.Equal(t, "gtms/test/cases/bulkauto/tc-aaaa1111-login.md", filepath.ToSlash(source))

	// 2. Wiring was written, carrying the resolved owner's hash.
	rec, _, ferr := wiring.Find(root, "tc-aaaa1111", "bats")
	require.NoError(t, ferr)
	require.NotNil(t, rec, "bulk automate reported success but wrote no wiring record")
	wantHash, herr := pipeline.HashFile(spec)
	require.NoError(t, herr)
	assert.Equal(t, wantHash, rec.TestCaseHash,
		"wiring testcase-hash must be the hash of the spec this run resolved as the owner")
}
