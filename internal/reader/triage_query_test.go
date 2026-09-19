package reader

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/triage"
)

// ENH-201 read-surface tests: ShowTriage and ListTriage. Records are written
// directly via triage.Write so a test can control the snapshot result
// independently of the terminal handoff, which is what the raw-vs-raw
// comparison hinges on.

// putTriageRecord writes a valid triage record with the given snapshot result.
// resolvedAt != "" makes it a resolved record.
func putTriageRecord(t *testing.T, root, tc, fw, value, snapResult, resolvedAt string) {
	t.Helper()
	status := triage.StatusActive
	if resolvedAt != "" {
		status = triage.StatusResolved
	}
	_, err := triage.Write(root, &triage.Record{
		Version:      triage.SchemaVersion,
		TestCaseID:   tc,
		Framework:    fw,
		Status:       status,
		Value:        value,
		ClassifiedAt: "2026-08-27T09:00:00Z",
		ResolvedAt:   resolvedAt,
		Snapshot: triage.Snapshot{
			Task:   "task-" + tc,
			Result: snapResult,
		},
	})
	require.NoError(t, err)
}

func TestShowTriage_ComparisonAcrossHandoff(t *testing.T) {
	cases := []struct {
		name       string
		snapResult string
		latest     string // "" => no handoff seeded
		want       *bool
	}{
		{"unchanged fail", "fail", "fail", boolPtr(false)},
		{"changed fail to pass", "fail", "pass", boolPtr(true)},
		{"raw skip unchanged", "skip", "skip", boolPtr(false)},
		{"raw skip to pass", "skip", "pass", boolPtr(true)},
		{"no handoff is nil", "error", "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
			writeTriageSpec(t, root, "tc-11111111")
			if c.latest != "" {
				seedLegacyRecord(t, root, legacyRecord{TC: "tc-11111111", Framework: "bats", Result: c.latest})
			}
			putTriageRecord(t, root, "tc-11111111", "bats", "environment", c.snapResult, "")

			view, err := ShowTriage(root, "tc-11111111", "bats")
			require.NoError(t, err)
			assert.Equal(t, c.snapResult, view.Record.Snapshot.Result, "snapshot result must stay raw")
			if c.want == nil {
				assert.Nil(t, view.ResultChanged, "comparison must be nil when no handoff exists")
			} else {
				require.NotNil(t, view.ResultChanged)
				assert.Equal(t, *c.want, *view.ResultChanged)
			}
		})
	}
}

func TestShowTriage_Selection(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")

	// Sole record: framework omitted resolves it.
	putTriageRecord(t, root, "tc-11111111", "bats", "environment", "fail", "")
	sole, err := ShowTriage(root, "tc-11111111", "")
	require.NoError(t, err)
	assert.Equal(t, "bats", sole.Record.Framework)

	// Ambiguity: two records, framework omitted -> error naming both.
	putTriageRecord(t, root, "tc-22222222", "bats", "environment", "fail", "")
	putTriageRecord(t, root, "tc-22222222", "playwright", "test-wrong", "pass", "2026-08-27T10:00:00Z")
	_, err = ShowTriage(root, "tc-22222222", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bats")
	assert.Contains(t, err.Error(), "playwright")

	// Resolved record returned directly, no lifecycle flag.
	resolved, err := ShowTriage(root, "tc-22222222", "playwright")
	require.NoError(t, err)
	assert.Equal(t, triage.StatusResolved, resolved.Record.Status)
	assert.NotEmpty(t, resolved.Record.ResolvedAt)

	// Missing pair: named no-record error naming both components.
	_, err = ShowTriage(root, "tc-33333333", "bats")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tc-33333333")
	assert.Contains(t, err.Error(), "bats")
}

func TestListTriage_OrderingAndFilters(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")

	// Written in deliberately non-lexical order.
	putTriageRecord(t, root, "tc-22222222", "playwright", "environment", "fail", "")
	putTriageRecord(t, root, "tc-11111111", "playwright", "retired-value", "fail", "")
	putTriageRecord(t, root, "tc-11111111", "bats", "environment", "fail", "")
	putTriageRecord(t, root, "tc-33333333", "bats", "app-wrong", "fail", "")
	putTriageRecord(t, root, "tc-22222222", "bats", "environment", "fail", "2026-08-27T10:00:00Z") // resolved
	putTriageRecord(t, root, "tc-44444444", "bats", "retired-value", "fail", "2026-08-27T10:00:00Z")

	pairs := func(views []TriageView) []string {
		out := make([]string, 0, len(views))
		for _, v := range views {
			out = append(out, v.Record.TestCaseID+"/"+v.Record.Framework)
		}
		return out
	}

	// Active-only default, stable lexical order.
	active, err := ListTriage(root, nil, "", false)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"tc-11111111/bats", "tc-11111111/playwright", "tc-22222222/playwright", "tc-33333333/bats",
	}, pairs(active))

	// Include resolved -> all six, still ordered.
	all, err := ListTriage(root, nil, "", true)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"tc-11111111/bats", "tc-11111111/playwright", "tc-22222222/bats",
		"tc-22222222/playwright", "tc-33333333/bats", "tc-44444444/bats",
	}, pairs(all))

	// Value filter retains an unconfigured stored key without consulting config.
	retired, err := ListTriage(root, nil, "retired-value", false)
	require.NoError(t, err)
	assert.Equal(t, []string{"tc-11111111/playwright"}, pairs(retired))

	// Value + lifecycle compose.
	envResolved, err := ListTriage(root, nil, "environment", true)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"tc-11111111/bats", "tc-22222222/bats", "tc-22222222/playwright",
	}, pairs(envResolved))
}

func TestListTriage_RootIncludesOrphanFolderExcludes(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")

	// tc-11111111 has a spec under checkout/; tc-44444444 has no spec anywhere.
	writeFile(t, root, filepath.Join("gtms", "test", "cases", "checkout", "tc-11111111.md"),
		"---\ntest_case_id: tc-11111111\ntitle: t\n---\nbody\n")
	putTriageRecord(t, root, "tc-11111111", "bats", "environment", "fail", "")
	putTriageRecord(t, root, "tc-44444444", "bats", "environment", "fail", "")

	// Root: both, orphan flagged.
	rootList, err := ListTriage(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, rootList, 2)
	byID := map[string]TriageView{}
	for _, v := range rootList {
		byID[v.Record.TestCaseID] = v
	}
	assert.False(t, byID["tc-11111111"].SpecMissing)
	assert.True(t, byID["tc-44444444"].SpecMissing, "orphan must be flagged in the root list")

	// Folder checkout: only tc-11111111; orphan absent.
	scope := &ScopeInfo{
		ScanDir:   filepath.Join(root, "gtms", "test", "cases", "checkout"),
		RelPath:   "gtms/test/cases/checkout/",
		Recursive: false,
	}
	folderList, err := ListTriage(root, scope, "", false)
	require.NoError(t, err)
	require.Len(t, folderList, 1)
	assert.Equal(t, "tc-11111111", folderList[0].Record.TestCaseID)
}

func TestListTriage_FailClosedScopeIsolation(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")

	// clean folder + valid record; broken folder + malformed record.
	writeFile(t, root, filepath.Join("gtms", "test", "cases", "clean", "tc-11111111.md"),
		"---\ntest_case_id: tc-11111111\ntitle: t\n---\nbody\n")
	writeFile(t, root, filepath.Join("gtms", "test", "cases", "broken", "tc-55555555.md"),
		"---\ntest_case_id: tc-55555555\ntitle: t\n---\nbody\n")
	putTriageRecord(t, root, "tc-11111111", "bats", "environment", "fail", "")
	// Malformed (bad status) written raw so it decodes but fails semantic validation.
	writeFile(t, root, filepath.Join("gtms", "triage", "tc-55555555--bats.triage.yaml"),
		"version: 1\ntest_case_id: tc-55555555\nframework: bats\nstatus: banana\nvalue: environment\nclassified_at: 2026-08-27T09:00:00Z\nsnapshot:\n  task: t\n  result: fail\n")

	// clean folder: the out-of-scope malformed record does not poison it.
	cleanScope := &ScopeInfo{
		ScanDir: filepath.Join(root, "gtms", "test", "cases", "clean"),
		RelPath: "gtms/test/cases/clean/",
	}
	cleanList, err := ListTriage(root, cleanScope, "", false)
	require.NoError(t, err)
	require.Len(t, cleanList, 1)

	// broken folder: malformed in-scope record fails the whole list, path named.
	brokenScope := &ScopeInfo{
		ScanDir: filepath.Join(root, "gtms", "test", "cases", "broken"),
		RelPath: "gtms/test/cases/broken/",
	}
	_, err = ListTriage(root, brokenScope, "", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tc-55555555--bats.triage.yaml")

	// root: every record is in scope, so the malformed one fails it too.
	_, err = ListTriage(root, nil, "", false)
	require.Error(t, err)

	// The malformed file was never repaired.
	data, _ := os.ReadFile(filepath.Join(root, "gtms", "triage", "tc-55555555--bats.triage.yaml"))
	assert.Contains(t, string(data), "status: banana")
}

func boolPtr(b bool) *bool { return &b }
