package reader

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ENH-210 reader coverage: ListTriageForTC (the bare/qualified test-case-target
// list route) and the folder-list prefix-leak correction on ListTriage. These
// are the mutation/boundary controls ENH-210 asks for: exact body identity on
// both paths, orphan [spec missing], the empty-result success, and the
// relevant/unprovable fail-closed rule for prefix-compatible malformed records.

func writeSpecUnder(t *testing.T, root, folder, tc string) {
	t.Helper()
	writeFile(t, root, filepath.Join("gtms", "test", "cases", folder, tc+".md"),
		"---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: "+tc+"\ntitle: t\n---\nbody\n")
}

func writeMalformedTriage(t *testing.T, root, filename string) {
	t.Helper()
	writeFile(t, root, filepath.Join("gtms", "triage", filename),
		"version: 1\nseverity: high\n")
}

func TestListTriageForTC_ListsEveryFramework(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeSpecUnder(t, root, "checkout", "tc-11111111")
	writeSpecUnder(t, root, "search", "tc-99999999")
	putTriageRecord(t, root, "tc-11111111", "bats", "environment", "fail", "")
	putTriageRecord(t, root, "tc-11111111", "playwright", "app-wrong", "fail", "")
	putTriageRecord(t, root, "tc-99999999", "bats", "environment", "fail", "")

	// Two frameworks, lexical order, no --framework required.
	multi, err := ListTriageForTC(root, "tc-11111111", "", false)
	require.NoError(t, err)
	require.Len(t, multi, 2)
	assert.Equal(t, "bats", multi[0].Record.Framework)
	assert.Equal(t, "playwright", multi[1].Record.Framework)
	assert.False(t, multi[0].SpecMissing)

	// One framework lists exactly one, and nothing from the other test case.
	single, err := ListTriageForTC(root, "tc-99999999", "", false)
	require.NoError(t, err)
	require.Len(t, single, 1)
	assert.Equal(t, "tc-99999999", single[0].Record.TestCaseID)
}

func TestListTriageForTC_OrphanMarkedSpecMissing(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeSpecUnder(t, root, "checkout", "tc-11111111")
	putTriageRecord(t, root, "tc-11111111", "bats", "environment", "fail", "")
	// tc-44444444 has a record but no spec anywhere: an orphan.
	putTriageRecord(t, root, "tc-44444444", "bats", "environment", "fail", "")

	orphan, err := ListTriageForTC(root, "tc-44444444", "", false)
	require.NoError(t, err)
	require.Len(t, orphan, 1)
	assert.True(t, orphan[0].SpecMissing, "an orphan record queried by bare ID must be marked spec-missing")

	linked, err := ListTriageForTC(root, "tc-11111111", "", false)
	require.NoError(t, err)
	require.Len(t, linked, 1)
	assert.False(t, linked[0].SpecMissing, "a linked record must not be marked spec-missing")
}

func TestListTriageForTC_NoRecordIsEmptySuccess(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeSpecUnder(t, root, "search", "tc-33333333") // spec, no record
	putTriageRecord(t, root, "tc-11111111", "bats", "environment", "fail", "")

	empty, err := ListTriageForTC(root, "tc-33333333", "", false)
	require.NoError(t, err)
	assert.Empty(t, empty, "a test case with a spec but no triage record lists nothing")

	// Widening the lifecycle filter does not conjure a record.
	empty2, err := ListTriageForTC(root, "tc-33333333", "", true)
	require.NoError(t, err)
	assert.Empty(t, empty2)
}

func TestListTriage_ExactBodyIdentityBothPaths(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeSpecUnder(t, root, "cx", "tc-77777777")
	writeSpecUnder(t, root, "legacy", "tc-77777777--legacy")
	putTriageRecord(t, root, "tc-77777777", "bats", "environment", "fail", "")
	putTriageRecord(t, root, "tc-77777777--legacy", "bats", "app-wrong", "fail", "")

	// Bare TC path, both directions: neither attributes the sibling's record.
	shorter, err := ListTriageForTC(root, "tc-77777777", "", false)
	require.NoError(t, err)
	require.Len(t, shorter, 1)
	assert.Equal(t, "environment", shorter[0].Record.Value)

	longer, err := ListTriageForTC(root, "tc-77777777--legacy", "", false)
	require.NoError(t, err)
	require.Len(t, longer, 1)
	assert.Equal(t, "app-wrong", longer[0].Record.Value)

	// Folder path: cx owns only tc-77777777; the prefix sibling must not leak in.
	cxScope := &ScopeInfo{ScanDir: filepath.Join(root, "gtms", "test", "cases", "cx"), RelPath: "gtms/test/cases/cx/"}
	cx, err := ListTriage(root, cxScope, "", false)
	require.NoError(t, err)
	require.Len(t, cx, 1)
	assert.Equal(t, "tc-77777777", cx[0].Record.TestCaseID)
	assert.Equal(t, "environment", cx[0].Record.Value)
}

func TestListTriageForTC_MalformedRelevanceBoundary(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeSpecUnder(t, root, "checkout", "tc-11111111")
	writeSpecUnder(t, root, "search", "tc-99999999")
	putTriageRecord(t, root, "tc-11111111", "bats", "environment", "fail", "")
	putTriageRecord(t, root, "tc-99999999", "bats", "environment", "fail", "")
	// Definitively unrelated malformed record: its filename cannot represent tc-11111111.
	writeMalformedTriage(t, root, "tc-55555555--bats.triage.yaml")

	// tc-11111111 is clean: the unrelated malformed file is never opened.
	ok, err := ListTriageForTC(root, "tc-11111111", "", false)
	require.NoError(t, err)
	require.Len(t, ok, 1)

	// A malformed record RELEVANT to the target (its own filename) fails closed.
	_, err = ListTriageForTC(root, "tc-55555555", "", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tc-55555555--bats.triage.yaml")

	// Prefix-compatible malformed: could belong to tc-11111111 (framework
	// extra--bats) or tc-11111111--extra (framework bats). Relevant/unprovable:
	// the query fails closed naming the path.
	writeMalformedTriage(t, root, "tc-11111111--extra--bats.triage.yaml")
	_, err = ListTriageForTC(root, "tc-11111111", "", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tc-11111111--extra--bats.triage.yaml")

	_, err = ListTriageForTC(root, "tc-11111111--extra", "", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tc-11111111--extra--bats.triage.yaml")

	// tc-99999999 is unaffected: neither malformed file can represent it.
	other, err := ListTriageForTC(root, "tc-99999999", "", false)
	require.NoError(t, err)
	require.Len(t, other, 1)

	// No malformed file was repaired by a failed read.
	data, _ := os.ReadFile(filepath.Join(root, "gtms", "triage", "tc-11111111--extra--bats.triage.yaml"))
	assert.Contains(t, string(data), "severity: high")
}

func TestListTriageForTC_FiltersApply(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeSpecUnder(t, root, "checkout", "tc-22222222")
	putTriageRecord(t, root, "tc-22222222", "bats", "environment", "fail", "2026-09-01T10:00:00Z") // resolved
	putTriageRecord(t, root, "tc-22222222", "playwright", "app-wrong", "fail", "")                 // active

	// Default active-only: only the playwright record.
	active, err := ListTriageForTC(root, "tc-22222222", "", false)
	require.NoError(t, err)
	require.Len(t, active, 1)
	assert.Equal(t, "playwright", active[0].Record.Framework)

	// --include-resolved: both, bats first.
	both, err := ListTriageForTC(root, "tc-22222222", "", true)
	require.NoError(t, err)
	require.Len(t, both, 2)

	// --value environment with active-only: the environment record is resolved,
	// so the filter yields nothing.
	filtered, err := ListTriageForTC(root, "tc-22222222", "environment", false)
	require.NoError(t, err)
	assert.Empty(t, filtered)

	// --value environment with --include-resolved: the resolved bats record.
	filteredIncl, err := ListTriageForTC(root, "tc-22222222", "environment", true)
	require.NoError(t, err)
	require.Len(t, filteredIncl, 1)
	assert.Equal(t, "bats", filteredIncl[0].Record.Framework)
}
