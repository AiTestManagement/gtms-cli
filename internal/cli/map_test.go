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

	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-aaa1111-login-happy.md"), `---
test_case_id: tc-aaa1111
title: Login Happy Path
requirement: REQ-A
---
`)
	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-bbb1111-checkout-flow.md"), `---
test_case_id: tc-bbb1111
title: Checkout Flow
requirement: REQ-B
---
`)

	// tc-aaa1111 has wiring + passing handoff (CON-023 / ENH-145).
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-aaa1111",
		Framework: "playwright",
		Result:    "pass",
		Attempts:  1,
	})

	return root
}

func TestRunMap_DefaultSlugView(t *testing.T) {
	root := setupMapFixture(t)
	var buf bytes.Buffer

	err := runMap(&buf, root, nil, false, "", false, "", false)
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

	err := runMap(&buf, root, nil, true, "", false, "", false)
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

	err := runMap(&buf, root, nil, false, "tc-aaa1111", false, "", false)
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

	err := runMap(&buf, root, nil, false, "tc-nonexistent", false, "", false)
	require.Error(t, err)
	assert.True(t, output.IsDisplayed(err), "error should be marked as displayed")
	assert.Contains(t, err.Error(), "tc-nonexistent",
		"the returned error must reference the unknown TC ID")

	// BUG-081: the not-found message now goes to stderr (output.Errorf), not
	// the runMap writer, so the writer buffer must NOT carry it. The BATS
	// suite covers the stderr stream end-to-end.
	assert.NotContains(t, buf.String(), "Test case tc-nonexistent not found.",
		"not-found message must not leak into the writer (it goes to stderr)")
}

func TestRunMap_JSON(t *testing.T) {
	root := setupMapFixture(t)
	var buf bytes.Buffer

	err := runMap(&buf, root, nil, false, "", true, "", false)
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

	err := runMap(&buf, root, nil, false, "", false, "", false)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "No test cases found.")
	assert.Contains(t, out, "gtms create")
}

func TestRunMap_JSON_EmptyProject(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer

	err := runMap(&buf, root, nil, false, "", true, "", false)
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
	err := runMap(&buf1, root, nil, false, "tc-aaa1111", false, "", false)
	require.NoError(t, err)

	// With --detail flag
	var buf2 bytes.Buffer
	err = runMap(&buf2, root, nil, true, "tc-aaa1111", false, "", false)
	require.NoError(t, err)

	// Both should produce identical output (--detail is ignored when detailID is set)
	assert.Equal(t, buf1.String(), buf2.String())
}

// BUG-081: the JSON branch of runMap now honours detailID and emits a
// group-preserving filtered shape that mirrors the text-mode semantic of
// writeMapSingleTC. The tests below replace the obsolete
// TestRunMap_JSONIgnoresDetailID which asserted the bug behaviour.

func TestBUG081_MapTcIdJSONLinkedPreservesGroupContext(t *testing.T) {
	root := setupMapFixture(t)
	var buf bytes.Buffer

	err := runMap(&buf, root, nil, false, "tc-aaa1111", true, "", false)
	require.NoError(t, err)

	var report reader.MapReport
	require.NoError(t, json.Unmarshal(buf.Bytes(), &report))

	// Filtered to the one group hosting tc-aaa1111 (REQ-A); REQ-B is dropped.
	require.Len(t, report.Groups, 1)
	assert.Equal(t, "REQ-A", report.Groups[0].Requirement)
	assert.Len(t, report.Unlinked, 0)

	// Summary reflects the requirement-scoped set.
	assert.Equal(t, 1, report.Summary.TotalRequirements)
	assert.Equal(t, 1, report.Summary.TotalTestCases)
	assert.Equal(t, 0, report.Summary.UnlinkedCount)

	// No human-readable decorations.
	out := buf.String()
	assert.NotContains(t, out, "KEY:")
	assert.NotContains(t, out, "SUMMARY:")
}

func TestBUG081_MapTcIdJSONLinkedSiblingsPreserved(t *testing.T) {
	root := setupMapFixture(t)

	// Add a sibling TC under the same requirement (REQ-A) so we can assert
	// that the filtered JSON keeps the whole group, not just the requested TC.
	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-aaa2222-login-locked.md"), `---
test_case_id: tc-aaa2222
title: Login Locked Account
requirement: REQ-A
---
`)

	var buf bytes.Buffer
	err := runMap(&buf, root, nil, false, "tc-aaa1111", true, "", false)
	require.NoError(t, err)

	var report reader.MapReport
	require.NoError(t, json.Unmarshal(buf.Bytes(), &report))

	require.Len(t, report.Groups, 1)
	require.Len(t, report.Groups[0].TestCases, 2, "sibling TCs in the same requirement must be preserved")

	got := map[string]bool{}
	for _, tc := range report.Groups[0].TestCases {
		got[tc.TestCaseID] = true
	}
	assert.True(t, got["tc-aaa1111"], "requested TC must be present")
	assert.True(t, got["tc-aaa2222"], "sibling TC must be preserved alongside the requested TC")

	assert.Equal(t, 1, report.Summary.TotalRequirements)
	assert.Equal(t, 2, report.Summary.TotalTestCases)
}

func TestBUG081_MapTcIdJSONUnlinkedShape(t *testing.T) {
	root := setupMapFixture(t)
	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-ccc1111-orphan.md"), `---
test_case_id: tc-ccc1111
title: Orphan Test Case
requirement: ""
---
`)

	var buf bytes.Buffer
	err := runMap(&buf, root, nil, false, "tc-ccc1111", true, "", false)
	require.NoError(t, err)

	var report reader.MapReport
	require.NoError(t, json.Unmarshal(buf.Bytes(), &report))

	assert.Len(t, report.Groups, 0)
	require.Len(t, report.Unlinked, 1)
	assert.Equal(t, "tc-ccc1111", report.Unlinked[0].TestCaseID)

	assert.Equal(t, 0, report.Summary.TotalRequirements)
	assert.Equal(t, 1, report.Summary.TotalTestCases)
	assert.Equal(t, 1, report.Summary.UnlinkedCount)
}

func TestBUG081_MapTcIdJSONUnknownTcErrors(t *testing.T) {
	root := setupMapFixture(t)
	var buf bytes.Buffer

	err := runMap(&buf, root, nil, false, "tc-nonexistent", true, "", false)
	require.Error(t, err)
	assert.True(t, output.IsDisplayed(err), "error should be marked as displayed")
	assert.Contains(t, err.Error(), "tc-nonexistent",
		"the returned error must reference the unknown TC ID")

	// Stdout (the runMap writer) must NOT carry the not-found message — that
	// goes to stderr via output.Errorf — and must NOT carry a misleading empty
	// JSON envelope either. The BATS suite asserts the stderr stream directly.
	out := buf.String()
	assert.NotContains(t, out, `"groups"`, "no JSON envelope on stdout for unknown TC")
	assert.NotContains(t, out, `"unlinked"`, "no JSON envelope on stdout for unknown TC")
	assert.NotContains(t, out, "Test case tc-nonexistent not found.",
		"not-found message must not leak into stdout (it goes to stderr)")
}

func TestRunMap_DetailSingleTC_Unlinked(t *testing.T) {
	root := setupMapFixture(t)

	// Add an unlinked test case
	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-ccc1111-orphan.md"), `---
test_case_id: tc-ccc1111
title: Orphan Test Case
requirement: ""
---
`)

	var buf bytes.Buffer
	err := runMap(&buf, root, nil, false, "tc-ccc1111", false, "", false)
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
	assert.Equal(t, "manual", formatMapStageIcon("manual"))
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
		ScanDir:   filepath.Join(root, "gtms/cases"),
		RelPath:   "gtms/cases/login/",
		Recursive: false,
	}
	err := runMap(&buf, root, scope, false, "", false, "", false)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "Scope: gtms/cases/login/")
	assert.Contains(t, out, "use -r for recursive")
}

func TestRunMap_ScopeFeedbackRecursive(t *testing.T) {
	root := setupMapFixture(t)
	var buf bytes.Buffer

	scope := &reader.ScopeInfo{
		ScanDir:   filepath.Join(root, "gtms/cases"),
		RelPath:   "gtms/cases/login/",
		Recursive: true,
	}
	err := runMap(&buf, root, scope, false, "", false, "", false)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "Scope: gtms/cases/login/")
	assert.NotContains(t, out, "use -r for recursive")
}

// --- ENH-066: No-arg recursive and empty hint tests ---

func TestRunMap_NoArgRecursive(t *testing.T) {
	root := t.TempDir()

	// TCs in subfolders — scope=nil should find them
	writeTestFile(t, root, filepath.Join("gtms/cases", "login", "tc-aaa1111-login.md"), `---
test_case_id: tc-aaa1111
title: Login Test
requirement: REQ-A
---
`)
	writeTestFile(t, root, filepath.Join("gtms/cases", "checkout", "tc-bbb1111-checkout.md"), `---
test_case_id: tc-bbb1111
title: Checkout Test
requirement: REQ-B
---
`)

	var buf bytes.Buffer
	// scope=nil triggers full recursive scan
	err := runMap(&buf, root, nil, false, "", false, "", false)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "tc-aaa1111")
	assert.Contains(t, out, "tc-bbb1111")
	assert.NotContains(t, out, "Scope:")
}

// TestRunMap_ReRecordUpdatesResult verifies that re-recording a manual result from
// fail to pass is reflected in the map output (BUG-027, Finding 10).
func TestRunMap_ReRecordUpdatesResult(t *testing.T) {
	root := t.TempDir()

	// Create a test case
	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-fff1111-rerecord.md"), `---
test_case_id: tc-fff1111
title: Re-record Test
requirement: REQ-X
---
`)

	// CON-023 / ENH-145: manual-only TCs surface via a manual result file.
	// Initial state: fail.
	manualDir := filepath.Join(root, "gtms", "manual", "records")
	require.NoError(t, os.MkdirAll(manualDir, 0755))
	manualPath := filepath.Join(manualDir, "tc-fff1111--manual.result.yaml")
	require.NoError(t, os.WriteFile(manualPath,
		[]byte("test_case_id: tc-fff1111\nframework: manual\nresult: fail\n"), 0644))

	// Read map — should show fail
	var buf1 bytes.Buffer
	err := runMap(&buf1, root, nil, false, "", true, "", false)
	require.NoError(t, err)
	var report1 reader.MapReport
	require.NoError(t, json.Unmarshal(buf1.Bytes(), &report1))
	require.Len(t, report1.Groups, 1)
	assert.Equal(t, "fail", report1.Groups[0].TestCases[0].LastResult, "initial result should be fail")

	// Overwrite the manual record with pass result (simulating re-recording).
	require.NoError(t, os.WriteFile(manualPath,
		[]byte("test_case_id: tc-fff1111\nframework: manual\nresult: pass\n"), 0644))

	// Read map again — should now show pass
	var buf2 bytes.Buffer
	err = runMap(&buf2, root, nil, false, "", true, "", false)
	require.NoError(t, err)
	var report2 reader.MapReport
	require.NoError(t, json.Unmarshal(buf2.Bytes(), &report2))
	require.Len(t, report2.Groups, 1)
	assert.Equal(t, "pass", report2.Groups[0].TestCases[0].LastResult, "re-recorded result should be pass")
}

// TestRunMap_ManualAutomateShowsManual verifies that map displays "manual" in AUTOMATE column
// for test cases with manual framework automation records (BUG-027, Finding 9).
func TestRunMap_ManualAutomateShowsManual(t *testing.T) {
	root := t.TempDir()

	writeTestFile(t, root, filepath.Join("gtms/cases", "tc-eee1111-manual-test.md"), `---
test_case_id: tc-eee1111
title: Manual Test
requirement: REQ-Y
---
`)

	// CON-023 / ENH-145: manual-only TCs live in gtms/manual/records/.
	manualDir := filepath.Join(root, "gtms", "manual", "records")
	require.NoError(t, os.MkdirAll(manualDir, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(manualDir, "tc-eee1111--manual.result.yaml"),
		[]byte("test_case_id: tc-eee1111\nframework: manual\nresult: pass\n"), 0644))

	var buf bytes.Buffer
	err := runMap(&buf, root, nil, false, "", false, "", false)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "AUTOMATE manual", "AUTOMATE column should show 'manual' for manual framework")
}

func TestRunMap_EmptyWithHint(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer

	err := runMap(&buf, root, nil, false, "", false, "", false)
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "No test cases found.")
	assert.Contains(t, out, "gtms create")
}
