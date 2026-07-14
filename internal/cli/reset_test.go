package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupResetFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	writeTestFile(t, root, filepath.Join("gtms/test/cases", "tc-aaa1111-login-happy.md"), `---
test_case_id: tc-aaa1111
title: Login Happy Path
requirement: REQ-A
---
`)
	writeTestFile(t, root, filepath.Join("gtms/test/cases", "tc-bbb2222-checkout-flow.md"), `---
test_case_id: tc-bbb2222
title: Checkout Flow
requirement: REQ-B
---
`)

	// tc-aaa1111 has wiring + terminal handoff (pass).
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-aaa1111",
		Framework: "bats",
		Result:    "pass",
	})

	// tc-bbb2222 has wiring but no terminal handoff (no execute outcome).
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-bbb2222",
		Framework: "bats",
	})

	// Execute task file for tc-aaa1111
	writeTestFile(t, root, filepath.Join("gtms/tasks", "complete", "task-abc1234-execute-tc-aaa1111.md"), `---
id: task-abc1234
type: execute
target: tc-aaa1111
adapter: bats-runner
status: complete
created: 2026-01-01T00:00:00Z
---
`)

	return root
}

func TestRunReset_Summary(t *testing.T) {
	root := setupResetFixture(t)
	var buf bytes.Buffer

	err := runReset(&buf, root, nil, "tc-aaa1111", false)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "Cleared execute results for 1 test case(s)")
	assert.Contains(t, out, "1 automation records updated")
	assert.Contains(t, out, "1 task files removed")
}

func TestRunReset_DryRun(t *testing.T) {
	root := setupResetFixture(t)
	var buf bytes.Buffer

	err := runReset(&buf, root, nil, "tc-aaa1111", true)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "[dry-run]")
	assert.Contains(t, out, "Would clear execute results for 1 test case(s)")

	// Verify nothing was modified
	taskDir := filepath.Join(root, "gtms/tasks", "complete")
	files, err := os.ReadDir(taskDir)
	require.NoError(t, err)
	assert.Len(t, files, 1, "dry-run should not remove task files")
}

func TestRunReset_NothingToClear(t *testing.T) {
	root := setupResetFixture(t)
	var buf bytes.Buffer

	// tc-bbb2222 has no execute results
	err := runReset(&buf, root, nil, "tc-bbb2222", false)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "Nothing to clear.")
}

func TestRunReset_DryRun_NothingToClear(t *testing.T) {
	root := setupResetFixture(t)
	var buf bytes.Buffer

	err := runReset(&buf, root, nil, "tc-bbb2222", true)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "[dry-run] Nothing to clear.")
}

// TestRunReset_FolderWithHyphen is a regression test for BUG-025.
// Folder names containing hyphens (e.g. "folder-a") must be routed to the
// folder-scoped reset path, not treated as a test case ID.
func TestRunReset_FolderWithHyphen(t *testing.T) {
	root := t.TempDir()

	// Create a TC in a folder with a hyphen
	writeTestFile(t, root, filepath.Join("gtms/test/cases", "folder-a", "tc-aaa00010-sample.md"), `---
test_case_id: tc-aaa00010
title: Sample in folder-a
requirement: REQ-001
---
`)

	// Wiring + terminal handoff so reset has something to clear.
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-aaa00010",
		Framework: "bats",
		Artefact:  "test/acceptance/sample.bats",
		Result:    "pass",
	})

	// Verify isTestCaseID rejects folder names with hyphens (BUG-025 root cause)
	assert.False(t, isTestCaseID("folder-a"), "folder name with hyphen must not be treated as TC ID")

	// Call runReset with folder scope -- this is the path that was broken before BUG-025 fix
	scope := buildScopeFromArg(root, "folder-a", false)
	var buf bytes.Buffer
	err := runReset(&buf, root, scope, "", false)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "Cleared execute results for 1 test case(s)")
	assert.Contains(t, out, "1 automation records updated")
}
