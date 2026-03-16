package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/aitestmanagement/gtms-cli/internal/output"
	"github.com/aitestmanagement/gtms-cli/internal/reader"
)

// writeTestFile creates a file with the given content, creating parent dirs as needed.
func writeTestFile(t *testing.T, root, relPath, content string) {
	t.Helper()
	fullPath := filepath.Join(root, relPath)
	dir := filepath.Dir(fullPath)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(fullPath, []byte(content), 0o644))
}

// setupMapFixture creates a minimal fixture project for map CLI tests.
func setupMapFixture(t *testing.T) string {
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

func TestRunMap_DefaultSlugView(t *testing.T) {
	root := setupMapFixture(t)
	var buf bytes.Buffer

	err := runMap(&buf, root, nil, false, "", false)
	require.NoError(t, err)

	out := buf.String()

	// Header
	assert.Contains(t, out, "TRACEABILITY MAP")

	// Slugs should appear (not full titles in default view)
	assert.Contains(t, out, "login-happy")
	assert.Contains(t, out, "checkout-flow")

	// KEY and SUMMARY lines
	assert.Contains(t, out, "KEY:")
	assert.Contains(t, out, "SUMMARY:")

	// Summary should have requirement count
	assert.Contains(t, out, "2 requirements")
	assert.Contains(t, out, "2 test cases")
}

func TestRunMap_DetailAll(t *testing.T) {
	root := setupMapFixture(t)
	var buf bytes.Buffer

	err := runMap(&buf, root, nil, true, "", false)
	require.NoError(t, err)

	out := buf.String()

	// Full titles should appear
	assert.Contains(t, out, "Login Happy Path")
	assert.Contains(t, out, "Checkout Flow")

	// Two-line format: pipeline on indented second line
	// The title line has the tc-id, then the next line has CREATE/AUTOMATE/EXECUTE indented
	lines := strings.Split(out, "\n")
	foundTwoLine := false
	for i, line := range lines {
		if strings.Contains(line, "tc-aaa1111") && strings.Contains(line, "Login Happy Path") {
			// Next line should have pipeline status indented
			if i+1 < len(lines) && strings.Contains(lines[i+1], "CREATE") {
				foundTwoLine = true
			}
		}
	}
	assert.True(t, foundTwoLine, "Expected two-line format with pipeline on indented second line")
}

func TestRunMap_DetailSingleTC(t *testing.T) {
	root := setupMapFixture(t)
	var buf bytes.Buffer

	err := runMap(&buf, root, nil, false, "tc-aaa1111", false)
	require.NoError(t, err)

	out := buf.String()

	// Header with tc-id
	assert.Contains(t, out, "TRACEABILITY MAP")
	assert.Contains(t, out, "tc-aaa1111")

	// Arrow marker on target test case
	assert.Contains(t, out, "\u2192 tc-aaa1111")

	// Should show only REQ-A group (tc-aaa1111's requirement)
	assert.Contains(t, out, "REQ-A")
	// REQ-B should NOT appear
	assert.NotContains(t, out, "REQ-B")

	// KEY line present, no SUMMARY (single group context)
	assert.Contains(t, out, "KEY:")
	assert.NotContains(t, out, "SUMMARY:")
}

func TestRunMap_DetailSingleTC_NotFound(t *testing.T) {
	root := setupMapFixture(t)
	var buf bytes.Buffer

	err := runMap(&buf, root, nil, false, "tc-nonexistent", false)
	require.Error(t, err)
	assert.True(t, output.IsDisplayed(err), "error should be marked as displayed")

	out := buf.String()
	assert.Contains(t, out, "Test case tc-nonexistent not found.")
}

func TestRunMap_JSON(t *testing.T) {
	root := setupMapFixture(t)
	var buf bytes.Buffer

	err := runMap(&buf, root, nil, false, "", true)
	require.NoError(t, err)

	out := buf.String()

	// Should be valid JSON
	var report reader.MapReport
	err = json.Unmarshal([]byte(out), &report)
	require.NoError(t, err, "Output should be valid JSON")

	// Fields should be populated
	assert.Len(t, report.Groups, 2)
	assert.Equal(t, 2, report.Summary.TotalTestCases)

	// No human-readable decorations
	assert.NotContains(t, out, "KEY:")
	assert.NotContains(t, out, "SUMMARY:")
}

func TestRunMap_EmptyProject(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer

	err := runMap(&buf, root, nil, false, "", false)
	require.NoError(t, err)

	assert.Equal(t, "No test cases found.\n", buf.String())
}

func TestRunMap_JSON_EmptyProject(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer

	err := runMap(&buf, root, nil, false, "", true)
	require.NoError(t, err)

	out := buf.String()

	// Must be valid JSON, NOT "No test cases found."
	assert.NotContains(t, out, "No test cases found.")

	var report reader.MapReport
	err = json.Unmarshal([]byte(out), &report)
	require.NoError(t, err, "Empty project JSON should still be valid JSON")

	assert.Equal(t, 0, report.Summary.TotalTestCases)
	assert.Equal(t, 0, report.Summary.TotalRequirements)

	// Nil slices serialize as null — verify we get [] instead
	assert.Contains(t, out, `"groups": []`)
	assert.Contains(t, out, `"unlinked": []`)
}

func TestRunMap_DetailFlagIgnoredWithDetailID(t *testing.T) {
	root := setupMapFixture(t)

	// Without --detail flag
	var buf1 bytes.Buffer
	err := runMap(&buf1, root, nil, false, "tc-aaa1111", false)
	require.NoError(t, err)

	// With --detail flag
	var buf2 bytes.Buffer
	err = runMap(&buf2, root, nil, true, "tc-aaa1111", false)
	require.NoError(t, err)

	// Both should produce identical output (--detail is ignored when detailID is set)
	assert.Equal(t, buf1.String(), buf2.String())
}

func TestRunMap_JSONIgnoresDetailID(t *testing.T) {
	root := setupMapFixture(t)
	var buf bytes.Buffer

	// JSON mode with a specific TC ID — JSON always outputs full report
	err := runMap(&buf, root, nil, false, "tc-aaa1111", true)
	require.NoError(t, err)

	out := buf.String()

	// JSON should still contain ALL groups, not just tc-aaa1111's group
	var report reader.MapReport
	err = json.Unmarshal([]byte(out), &report)
	require.NoError(t, err)

	assert.Len(t, report.Groups, 2, "JSON output should include all groups regardless of detailID")
	assert.Equal(t, 2, report.Summary.TotalTestCases)

	// No human-readable decorations
	assert.NotContains(t, out, "KEY:")
	assert.NotContains(t, out, "SUMMARY:")
}

func TestRunMap_DetailSingleTC_Unlinked(t *testing.T) {
	root := setupMapFixture(t)

	// Add an unlinked test case
	writeTestFile(t, root, filepath.Join("test-cases", "tc-ccc1111-orphan.md"), `---
test_case_id: tc-ccc1111
title: Orphan Test Case
requirement: ""
---
`)

	var buf bytes.Buffer
	err := runMap(&buf, root, nil, false, "tc-ccc1111", false)
	require.NoError(t, err)

	out := buf.String()

	// Header with tc-id
	assert.Contains(t, out, "TRACEABILITY MAP")
	assert.Contains(t, out, "tc-ccc1111")

	// Arrow marker on target test case
	assert.Contains(t, out, "\u2192 tc-ccc1111")

	// Should show UNLINKED section, not a requirement group
	assert.Contains(t, out, "UNLINKED TEST CASES")
	assert.NotContains(t, out, "REQ-A")
	assert.NotContains(t, out, "REQ-B")

	// KEY present, no SUMMARY
	assert.Contains(t, out, "KEY:")
	assert.NotContains(t, out, "SUMMARY:")
}

func TestFormatMapStageIcon(t *testing.T) {
	assert.Equal(t, output.IconComplete, formatMapStageIcon("complete"))
	assert.Equal(t, output.IconInProgress, formatMapStageIcon("in-progress"))
	assert.Equal(t, output.IconPending, formatMapStageIcon("pending"))
	assert.Equal(t, output.IconComplete, formatMapStageIcon("developed"))
	assert.Equal(t, "\u2014", formatMapStageIcon("none"))
	assert.Equal(t, "\u2014", formatMapStageIcon("unknown"))
}

func TestFormatExecuteIcon(t *testing.T) {
	assert.Equal(t, output.IconComplete, formatExecuteIcon(reader.MapEntry{LastResult: "pass"}))
	assert.Equal(t, output.IconError, formatExecuteIcon(reader.MapEntry{LastResult: "fail"}))
	assert.Equal(t, output.IconInProgress, formatExecuteIcon(reader.MapEntry{ExecuteStatus: "in-progress", LastResult: "none"}))
	assert.Equal(t, output.IconPending, formatExecuteIcon(reader.MapEntry{ExecuteStatus: "pending", LastResult: "none"}))
	assert.Equal(t, "\u2014", formatExecuteIcon(reader.MapEntry{ExecuteStatus: "none", LastResult: "none"}))
}

// --- Scope feedback tests (ENH-036) ---

func TestRunMap_ScopeFeedback(t *testing.T) {
	root := setupMapFixture(t)
	var buf bytes.Buffer

	scope := &reader.ScopeInfo{
		ScanDir:   filepath.Join(root, "test-cases"),
		RelPath:   "test-cases/login/",
		Recursive: false,
	}
	err := runMap(&buf, root, scope, false, "", false)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "Scope: test-cases/login/")
	assert.Contains(t, out, "use -r for recursive")
}

func TestRunMap_ScopeFeedbackRecursive(t *testing.T) {
	root := setupMapFixture(t)
	var buf bytes.Buffer

	scope := &reader.ScopeInfo{
		ScanDir:   filepath.Join(root, "test-cases"),
		RelPath:   "test-cases/login/",
		Recursive: true,
	}
	err := runMap(&buf, root, scope, false, "", false)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "Scope: test-cases/login/")
	assert.NotContains(t, out, "use -r for recursive")
}
