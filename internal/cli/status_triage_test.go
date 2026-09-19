package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/output"
	"github.com/aitestmanagement/gtms-cli/internal/reader"
	"github.com/aitestmanagement/gtms-cli/internal/triage"
)

// ENH-202 CLI tests: flag composition/validation (Verification Scope #1) and
// the root-summary JSON gating regression (VS #7 for the root summary).

func writeCliTriage(t *testing.T, root, tc, fw, value, snapResult string) {
	t.Helper()
	_, err := triage.Write(root, &triage.Record{
		Version:      triage.SchemaVersion,
		TestCaseID:   tc,
		Framework:    fw,
		Status:       triage.StatusActive,
		Value:        value,
		ClassifiedAt: "2026-08-28T09:00:00Z",
		Snapshot:     triage.Snapshot{Task: "task-" + tc, Result: snapResult},
	})
	require.NoError(t, err)
}

func writeCliSpec(t *testing.T, root, folder, tc string) {
	t.Helper()
	writeTestFile(t, root, filepath.Join("gtms/test/cases", folder, tc+".md"),
		"---\ntest_case_id: "+tc+"\ntitle: "+tc+"\nrequirement: ENH-202\n---\n# body\n")
}

// TestStatus_RootTriageFilterRefused pins tc-cd5d4d75 step 9: an unscoped root
// --triage invocation is refused with a scope diagnostic before any scan.
func TestStatus_RootTriageFilterRefused(t *testing.T) {
	root := t.TempDir()
	writeCliSpec(t, root, "checkout", "tc-11111111")
	restore := withGapsGlobals(t, root, &config.Config{})
	defer restore()

	cmd := newStatusCmd()
	require.NoError(t, cmd.Flags().Set("triage", "environment"))
	var err error
	out := captureStdout(t, func() { err = cmd.RunE(cmd, []string{}) })

	require.Error(t, err)
	assert.True(t, output.IsDisplayed(err), "refusal should be a displayed error")
	assert.Contains(t, err.Error(), "--triage requires a folder, test-case ID, or --recursive scope.")
	// No project expansion: no folder row, no test case ID in stdout.
	assert.NotContains(t, out, "tc-11111111")
	assert.NotContains(t, out, "checkout")
}

// TestStatus_RootRecursiveTriageNotRefused pins tc-cd5d4d75 step 11: explicit
// -r is the supported project-wide path, so it must NOT be refused.
func TestStatus_RootRecursiveTriageNotRefused(t *testing.T) {
	root := t.TempDir()
	writeCliSpec(t, root, "checkout", "tc-11111111")
	restore := withGapsGlobals(t, root, &config.Config{})
	defer restore()

	cmd := newStatusCmd()
	require.NoError(t, cmd.Flags().Set("recursive", "true"))
	require.NoError(t, cmd.Flags().Set("triage", "environment"))
	var err error
	_ = captureStdout(t, func() { err = cmd.RunE(cmd, []string{}) })
	require.NoError(t, err, "-r --triage is the supported project-wide filter and must not be refused")
}

// TestStatus_RootSummaryJSONGatesTriaged pins tc-db0624fc step 8: default root
// status JSON carries no `triaged`; only --show-triage adds it.
func TestStatus_RootSummaryJSONGatesTriaged(t *testing.T) {
	root := t.TempDir()
	writeCliSpec(t, root, "checkout", "tc-11111111")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-11111111", Framework: "bats", Result: "fail"})
	writeCliTriage(t, root, "tc-11111111", "bats", "environment", "fail")
	restore := withGapsGlobals(t, root, &config.Config{})
	defer restore()

	// Default: no --show-triage -> no triaged key.
	cmd := newStatusCmd()
	require.NoError(t, cmd.Flags().Set("json", "true"))
	def := captureStdout(t, func() { require.NoError(t, cmd.RunE(cmd, []string{})) })
	assert.NotContains(t, def, "triaged", "default root status JSON must not carry triaged")

	// With --show-triage -> triaged key present.
	cmd2 := newStatusCmd()
	require.NoError(t, cmd2.Flags().Set("json", "true"))
	require.NoError(t, cmd2.Flags().Set("show-triage", "true"))
	shown := captureStdout(t, func() { require.NoError(t, cmd2.RunE(cmd2, []string{})) })
	assert.Contains(t, shown, "\"triaged\": 1", "--show-triage adds the per-folder triaged count")
}

// TestStatus_PerTCJSONAlwaysHasTriage pins tc-a81c1961 / tc-17551503: per-test-case
// JSON (the folder overview) nests triage under its framework WITHOUT any
// presentation flag.
func TestStatus_PerTCJSONAlwaysHasTriage(t *testing.T) {
	root := t.TempDir()
	writeCliSpec(t, root, "checkout", "tc-11111111")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-11111111", Framework: "bats", Result: "fail"})
	writeCliTriage(t, root, "tc-11111111", "bats", "environment", "fail")
	restore := withGapsGlobals(t, root, &config.Config{})
	defer restore()

	cmd := newStatusCmd()
	require.NoError(t, cmd.Flags().Set("json", "true"))
	out := captureStdout(t, func() { require.NoError(t, cmd.RunE(cmd, []string{"checkout"})) })

	assert.Contains(t, out, "\"triage\"", "per-TC JSON must nest a triage object without --show-triage")
	assert.Contains(t, out, "\"value\": \"environment\"")
	// The nested object rides under the framework entry, not the TC top level.
	idxFrameworks := strings.Index(out, "\"frameworks\"")
	idxTriage := strings.Index(out, "\"triage\"")
	require.Positive(t, idxFrameworks)
	assert.Greater(t, idxTriage, idxFrameworks, "triage must appear inside frameworks[], not at top level")
}

// TestStatus_DetailTriageBlockHuman pins the single-test-case detail rendering
// (tc-23ab0b6b / tc-8ed5c09e): value + optional summary/defect, the changed
// result explained in place, and the unavailable comparison stated in words.
func TestStatus_DetailTriageBlockHuman(t *testing.T) {
	root := t.TempDir()
	writeCliSpec(t, root, "checkout", "tc-11111111")
	writeCliSpec(t, root, "checkout", "tc-22222222")
	writeCliSpec(t, root, "checkout", "tc-33333333")
	values := map[string]string{"environment": "Shared environment problem", "app-wrong": ""}

	// Unchanged: fail classified, latest fail; summary + defect shown; no warning.
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-11111111", Framework: "bats", Result: "fail"})
	_, err := triage.Write(root, &triage.Record{Version: triage.SchemaVersion, TestCaseID: "tc-11111111", Framework: "bats", Status: triage.StatusActive, Value: "environment", Summary: "creds expired", Defect: "JIRA-789", ClassifiedAt: "2026-08-28T09:00:00Z", Snapshot: triage.Snapshot{Task: "task-1", Result: "fail"}})
	require.NoError(t, err)

	// Changed: fail classified, latest pass; no summary/defect; warning + legend.
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-22222222", Framework: "bats", Result: "pass"})
	_, err = triage.Write(root, &triage.Record{Version: triage.SchemaVersion, TestCaseID: "tc-22222222", Framework: "bats", Status: triage.StatusActive, Value: "app-wrong", ClassifiedAt: "2026-08-28T09:00:00Z", Snapshot: triage.Snapshot{Task: "task-2", Result: "fail"}})
	require.NoError(t, err)

	// Unavailable: classified, no handoff at all.
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-33333333", Framework: "bats", Result: ""})
	_, err = triage.Write(root, &triage.Record{Version: triage.SchemaVersion, TestCaseID: "tc-33333333", Framework: "bats", Status: triage.StatusActive, Value: "environment", ClassifiedAt: "2026-08-28T09:00:00Z", Snapshot: triage.Snapshot{Task: "task-3", Result: "fail"}})
	require.NoError(t, err)

	var buf bytes.Buffer
	require.NoError(t, runStatusDetail(&buf, root, "tc-11111111", false, "bats", true, true, "", values))
	unchanged := buf.String()
	assert.Contains(t, unchanged, "TRIAGE:     environment")
	assert.Contains(t, unchanged, "Summary:  creds expired")
	assert.Contains(t, unchanged, "Defect:   JIRA-789")
	assert.NotContains(t, unchanged, output.IconWarning)
	assert.NotContains(t, unchanged, "result changed since triage")
	assert.NotContains(t, unchanged, "Shared environment problem", "human detail shows the key, never the label")

	buf.Reset()
	require.NoError(t, runStatusDetail(&buf, root, "tc-22222222", false, "bats", true, true, "", values))
	changed := buf.String()
	assert.Contains(t, changed, "TRIAGE:     app-wrong "+output.IconWarning)
	assert.Contains(t, changed, "Snapshot: fail; latest result: pass")
	assert.Contains(t, changed, "result changed since triage")
	assert.NotContains(t, changed, "Summary:")

	buf.Reset()
	require.NoError(t, runStatusDetail(&buf, root, "tc-33333333", false, "bats", true, true, "", values))
	unavail := buf.String()
	assert.Contains(t, unavail, "Snapshot: fail; latest result: unavailable")
	assert.NotContains(t, unavail, output.IconWarning)
}

// TestGaps_HumanTriageColumnsAndHeading pins the gaps text surfaces: TRIAGED /
// UNTRIAGED columns in the root folder summary, and the currently-failing
// heading split in the detailed report (tc-dcdbd94b / tc-716af7e0).
func TestGaps_HumanTriageColumnsAndHeading(t *testing.T) {
	root := t.TempDir()
	writeCliSpec(t, root, "checkout", "tc-11111111")
	writeCliSpec(t, root, "checkout", "tc-22222222")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-11111111", Framework: "bats", Result: "fail"})
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-22222222", Framework: "bats", Result: "fail"})
	writeCliTriage(t, root, "tc-11111111", "bats", "environment", "fail")

	// Root folder summary text.
	var buf bytes.Buffer
	require.NoError(t, runGapsFolderSummary(&buf, root, false, ""))
	summary := buf.String()
	assert.Contains(t, summary, "TRIAGED")
	assert.Contains(t, summary, "UNTRIAGED")

	// Detailed report heading carries the split.
	buf.Reset()
	scope := &reader.ScopeInfo{ScanDir: filepath.Join(root, "gtms", "test", "cases", "checkout"), RelPath: "gtms/test/cases/checkout/"}
	require.NoError(t, runGaps(&buf, root, scope, false, "", false))
	detailed := buf.String()
	assert.Contains(t, detailed, "Currently failing: 2 (1 triaged, 1 untriaged)")
}
