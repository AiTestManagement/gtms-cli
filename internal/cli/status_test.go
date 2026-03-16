package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/aitestmanagement/gtms-cli/internal/reader"
)

// setupStatusFixture creates a minimal fixture project for status CLI tests.
func setupStatusFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	writeTestFile(t, root, filepath.Join("test-cases", "tc-aaa1111-login-happy.md"), `---
test_case_id: tc-aaa1111
title: Login Happy Path
requirement: REQ-A
---
`)
	writeTestFile(t, root, filepath.Join("test-cases", "tc-bbb1111-checkout-flow.md"), `---
test_case_id: tc-bbb1111
title: Checkout Flow
requirement: REQ-B
---
`)

	// tc-aaa1111 has automation with pass result
	writeTestFile(t, root, filepath.Join("test-automation", "records", "tc-aaa1111.automation.md"), `---
testcase: tc-aaa1111
framework: playwright
status: accepted
last-formal-result: pass
attempts: 1
cycle: 1
---
`)

	return root
}

func TestRunStatusOverview_Table(t *testing.T) {
	root := setupStatusFixture(t)
	var buf bytes.Buffer

	err := runStatusOverview(&buf, root, nil, false)
	require.NoError(t, err)

	out := buf.String()

	// Table should include slug alongside test case ID
	assert.Contains(t, out, "tc-aaa1111  login-happy")
	assert.Contains(t, out, "tc-bbb1111  checkout-flow")

	// Table header should be present
	assert.Contains(t, out, "TEST CASE")
}

func TestRunStatusOverview_JSON(t *testing.T) {
	root := setupStatusFixture(t)
	var buf bytes.Buffer

	err := runStatusOverview(&buf, root, nil, true)
	require.NoError(t, err)

	out := buf.String()

	// Should be valid JSON
	var entries []reader.PipelineEntry
	err = json.Unmarshal([]byte(out), &entries)
	require.NoError(t, err, "Output should be valid JSON")

	// Should have two entries
	assert.Len(t, entries, 2)

	// Slug should be present in JSON
	assert.Equal(t, "login-happy", entries[0].Slug)
	assert.Equal(t, "checkout-flow", entries[1].Slug)

	// No human-readable decorations (table headers, not JSON keys)
	assert.NotContains(t, out, "TEST CASE")
	assert.NotContains(t, out, "LAST RESULT")
	assert.NotContains(t, out, "No test cases found.")
}

func TestRunStatusOverview_JSON_Empty(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer

	err := runStatusOverview(&buf, root, nil, true)
	require.NoError(t, err)

	out := buf.String()

	// Must be valid JSON, NOT "No test cases found."
	assert.NotContains(t, out, "No test cases found.")

	var entries []reader.PipelineEntry
	err = json.Unmarshal([]byte(out), &entries)
	require.NoError(t, err, "Empty project JSON should still be valid JSON")

	assert.Len(t, entries, 0)

	// Must be [] not null
	assert.Contains(t, out, "[]")
}

func TestRunStatusDetail_JSON(t *testing.T) {
	root := setupStatusFixture(t)
	var buf bytes.Buffer

	err := runStatusDetail(&buf, root, "tc-aaa1111", true)
	require.NoError(t, err)

	out := buf.String()

	// Should be valid JSON
	var detail reader.PipelineDetailEntry
	err = json.Unmarshal([]byte(out), &detail)
	require.NoError(t, err, "Output should be valid JSON")

	assert.Equal(t, "tc-aaa1111", detail.TestCaseID)
	assert.Equal(t, "login-happy", detail.Slug)
	assert.Equal(t, "Login Happy Path", detail.Title)
	assert.Equal(t, "REQ-A", detail.Requirement)

	// No human-readable decorations
	assert.NotContains(t, out, "CREATE:")
	assert.NotContains(t, out, "AUTOMATE:")
	assert.NotContains(t, out, "\u2500") // horizontal line
}

func TestRunStatusDetail_ShowsSlugInHeader(t *testing.T) {
	root := setupStatusFixture(t)
	var buf bytes.Buffer

	err := runStatusDetail(&buf, root, "tc-aaa1111", false)
	require.NoError(t, err)

	out := buf.String()

	// Header should include slug alongside test case ID
	assert.Contains(t, out, "tc-aaa1111  login-happy: Login Happy Path")
}

func TestRunStatusDetail_JSON_NotFound(t *testing.T) {
	root := setupStatusFixture(t)
	var buf bytes.Buffer

	err := runStatusDetail(&buf, root, "tc-nonexistent", true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestFormatTestCaseColumn(t *testing.T) {
	assert.Equal(t, "tc-001  login-happy", formatTestCaseColumn("tc-001", "login-happy"))
	assert.Equal(t, "tc-001", formatTestCaseColumn("tc-001", ""))
}

// --- BUG-017 regression tests ---

func TestFormatStageStatus_Failed(t *testing.T) {
	result := formatStageStatus("failed")
	assert.Equal(t, "\u2717", result, "Failed status should display X mark icon")
}

func TestRunStatusOverview_StatusKey(t *testing.T) {
	root := setupStatusFixture(t)
	var buf bytes.Buffer

	err := runStatusOverview(&buf, root, nil, false)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "Key:")
	assert.Contains(t, out, "complete/pass")
	assert.Contains(t, out, "failed/error")
	assert.Contains(t, out, "not yet attempted")
}

func TestRunStatusOverview_NoKeyInJSON(t *testing.T) {
	root := setupStatusFixture(t)
	var buf bytes.Buffer

	err := runStatusOverview(&buf, root, nil, true)
	require.NoError(t, err)

	out := buf.String()
	assert.NotContains(t, out, "Key:")
}

func TestBUG017_FailedExecuteShowsXInTable(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("test-cases", "tc-aaa1111-checkout.md"), `---
test_case_id: tc-aaa1111
title: Checkout Test
requirement: REQ-A
---
`)
	writeTestFile(t, root, filepath.Join("test-automation", "records", "tc-aaa1111.automation.md"), `---
testcase: tc-aaa1111
framework: bats
status: accepted
artefact: test-automation/specs/tc-aaa1111.bats
adapter: bats-runner
attempts: 1
cycle: 1
---
`)
	writeTestFile(t, root, filepath.Join("test-tasks", "failed", "task-abc1234-execute-tc-aaa1111.md"), `---
id: task-abc1234
type: execute
target: tc-aaa1111
adapter: bats-runner
status: failed
created: 2026-03-07T10:00:00Z
branch: main
---
`)

	var buf bytes.Buffer
	err := runStatusOverview(&buf, root, nil, false)
	require.NoError(t, err)

	out := buf.String()
	// The EXECUTE column should show the X mark (failed icon), not em dash
	assert.Contains(t, out, "\u2717", "Failed execute should show X mark icon")
	assert.Contains(t, out, "tc-aaa1111")
}

func TestBUG017_FailedExecuteInJSON(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("test-cases", "tc-aaa1111-checkout.md"), `---
test_case_id: tc-aaa1111
title: Checkout Test
requirement: REQ-A
---
`)
	writeTestFile(t, root, filepath.Join("test-automation", "records", "tc-aaa1111.automation.md"), `---
testcase: tc-aaa1111
framework: bats
status: accepted
artefact: test-automation/specs/tc-aaa1111.bats
adapter: bats-runner
attempts: 1
cycle: 1
---
`)
	writeTestFile(t, root, filepath.Join("test-tasks", "failed", "task-abc1234-execute-tc-aaa1111.md"), `---
id: task-abc1234
type: execute
target: tc-aaa1111
adapter: bats-runner
status: failed
created: 2026-03-07T10:00:00Z
branch: main
---
`)

	var buf bytes.Buffer
	err := runStatusOverview(&buf, root, nil, true)
	require.NoError(t, err)

	var entries []reader.PipelineEntry
	err = json.Unmarshal(buf.Bytes(), &entries)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	assert.Equal(t, "failed", entries[0].ExecuteStatus, "JSON output should show execute_status as 'failed'")
}

func TestBUG017_FailedExecuteDetailView(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("test-cases", "tc-aaa1111-checkout.md"), `---
test_case_id: tc-aaa1111
title: Checkout Test
requirement: REQ-A
---
`)
	writeTestFile(t, root, filepath.Join("test-automation", "records", "tc-aaa1111.automation.md"), `---
testcase: tc-aaa1111
framework: bats
status: accepted
artefact: test-automation/specs/tc-aaa1111.bats
adapter: bats-runner
attempts: 1
cycle: 1
---
`)
	writeTestFile(t, root, filepath.Join("test-tasks", "failed", "task-abc1234-execute-tc-aaa1111.md"), `---
id: task-abc1234
type: execute
target: tc-aaa1111
adapter: bats-runner
status: failed
created: 2026-03-07T10:00:00Z
branch: main
---
`)

	var buf bytes.Buffer
	err := runStatusDetail(&buf, root, "tc-aaa1111", false)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "\u2717", "Detail view should show X mark for failed execute")
	assert.Contains(t, out, "Failed", "Detail view should show 'Failed' label")
}

// --- Scope feedback tests (ENH-036) ---

func TestRunStatusOverview_ScopeFeedback(t *testing.T) {
	root := setupStatusFixture(t)
	var buf bytes.Buffer

	scope := &reader.ScopeInfo{
		ScanDir:   filepath.Join(root, "test-cases"),
		RelPath:   "test-cases/login/",
		Recursive: false,
	}
	err := runStatusOverview(&buf, root, scope, false)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "Scope: test-cases/login/")
	assert.Contains(t, out, "use -r for recursive")
}

func TestRunStatusOverview_ScopeFeedbackRecursive(t *testing.T) {
	root := setupStatusFixture(t)
	var buf bytes.Buffer

	scope := &reader.ScopeInfo{
		ScanDir:   filepath.Join(root, "test-cases"),
		RelPath:   "test-cases/login/",
		Recursive: true,
	}
	err := runStatusOverview(&buf, root, scope, false)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "Scope: test-cases/login/")
	assert.NotContains(t, out, "use -r for recursive")
}

func TestRunStatusOverview_ScopeFeedbackBeforeEmpty(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer

	scope := &reader.ScopeInfo{
		ScanDir:   filepath.Join(root, "test-cases", "empty"),
		RelPath:   "test-cases/empty/",
		Recursive: false,
	}
	err := runStatusOverview(&buf, root, scope, false)
	require.NoError(t, err)

	out := buf.String()
	// Scope feedback should appear even when no test cases are found
	assert.Contains(t, out, "Scope: test-cases/empty/")
	assert.Contains(t, out, "No test cases found.")
}

func TestRunStatusOverview_NilScopeNoFeedback(t *testing.T) {
	root := setupStatusFixture(t)
	var buf bytes.Buffer

	err := runStatusOverview(&buf, root, nil, false)
	require.NoError(t, err)

	out := buf.String()
	assert.NotContains(t, out, "Scope:")
}
