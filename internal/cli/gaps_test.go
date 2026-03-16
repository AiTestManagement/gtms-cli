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

// setupGapsFixture creates a fixture with gaps: test case but no spec file.
func setupGapsFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	// Test case with no automation spec → NoAutomation gap
	writeTestFile(t, root, filepath.Join("test-cases", "tc-aaa1111-login-happy.md"), `---
test_case_id: tc-aaa1111
title: Login Happy Path
requirement: REQ-A
---
`)

	return root
}

// setupGapsNoGapsFixture creates a fixture with full coverage (no gaps).
func setupGapsNoGapsFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	writeTestFile(t, root, filepath.Join("test-cases", "tc-aaa1111-login-happy.md"), `---
test_case_id: tc-aaa1111
title: Login Happy Path
requirement: REQ-A
---
`)

	// Spec file referencing tc-aaa1111 → covers NoAutomation
	writeTestFile(t, root, filepath.Join("test-automation", "specs", "tc-aaa1111-login.spec.ts"), `// tc-aaa1111
`)

	// Automation record with pass result → covers NeverExecuted and CurrentlyFailing
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

func TestRunGaps_JSON(t *testing.T) {
	root := setupGapsFixture(t)
	var buf bytes.Buffer

	err := runGaps(&buf, root, nil, nil, true)
	require.NoError(t, err)

	out := buf.String()

	// Should be valid JSON
	var report reader.GapReport
	err = json.Unmarshal([]byte(out), &report)
	require.NoError(t, err, "Output should be valid JSON")

	// Should have gap in NoAutomation
	assert.NotEmpty(t, report.NoAutomation)
	assert.Equal(t, "tc-aaa1111", report.NoAutomation[0].ID)

	// No human-readable decorations
	assert.NotContains(t, out, "GAPS REPORT")
	assert.NotContains(t, out, "No coverage gaps found.")
}

func TestRunGaps_JSON_NoGaps(t *testing.T) {
	root := setupGapsNoGapsFixture(t)
	var buf bytes.Buffer

	err := runGaps(&buf, root, []string{"test-automation/specs"}, nil, true)
	require.NoError(t, err)

	out := buf.String()

	// Must be valid JSON, NOT "No coverage gaps found."
	assert.NotContains(t, out, "No coverage gaps found.")

	var report reader.GapReport
	err = json.Unmarshal([]byte(out), &report)
	require.NoError(t, err, "No-gaps JSON should still be valid JSON")

	assert.Empty(t, report.NoTests)
	assert.Empty(t, report.NoAutomation)
	assert.Empty(t, report.NeverExecuted)
	assert.Empty(t, report.CurrentlyFailing)
}

func TestRunGaps_JSON_EmptyArrays(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer

	err := runGaps(&buf, root, nil, nil, true)
	require.NoError(t, err)

	out := buf.String()

	// Nil slices must serialize as [] not null
	assert.Contains(t, out, `"no_tests": []`)
	assert.Contains(t, out, `"no_automation": []`)
	assert.Contains(t, out, `"never_executed": []`)
	assert.Contains(t, out, `"currently_failing": []`)
	assert.Contains(t, out, `"spec_but_no_record": []`)
}

// --- Scope feedback tests (ENH-036) ---

func TestRunGaps_ScopeFeedback(t *testing.T) {
	root := setupGapsFixture(t)
	var buf bytes.Buffer

	scope := &reader.ScopeInfo{
		ScanDir:   filepath.Join(root, "test-cases"),
		RelPath:   "test-cases/login/",
		Recursive: false,
	}
	err := runGaps(&buf, root, nil, scope, false)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "Scope: test-cases/login/")
	assert.Contains(t, out, "use -r for recursive")
}

func TestRunGaps_ScopeFeedbackRecursive(t *testing.T) {
	root := setupGapsFixture(t)
	var buf bytes.Buffer

	scope := &reader.ScopeInfo{
		ScanDir:   filepath.Join(root, "test-cases"),
		RelPath:   "test-cases/login/",
		Recursive: true,
	}
	err := runGaps(&buf, root, nil, scope, false)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "Scope: test-cases/login/")
	assert.NotContains(t, out, "use -r for recursive")
}
