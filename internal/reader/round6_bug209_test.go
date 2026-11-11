package reader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
)

func countScans(t *testing.T) *int {
	t.Helper()
	count := 0
	result.CountScansForTest(&count)
	t.Cleanup(func() { result.CountScansForTest(nil) })
	return &count
}

// REV-277 CODEX-005: a refused entry that appeared after planning, even one this delete
// was never going to remove, leaves the cleanup incomplete.
func TestBUG209_DeleteFinalScanRefusalForcesNonZero(t *testing.T) {
	root, unrelated, safe := removalRaceProject(t)
	plan := &deletePlan{projectRoot: root, resultContracts: []string{safe}}
	require.NoError(t, os.Link(unrelated, filepath.Join(t.TempDir(), "other-link")))

	res := &DeleteResult{}
	require.NoError(t, removeResultContracts(plan, res, false))

	assert.NoFileExists(t, safe, "the planned removal must still happen")
	assert.FileExists(t, unrelated, "the refused entry must be left untouched")
	require.Len(t, res.HandoffRefusals, 1)
	assert.Equal(t, "refused handoff .gtms/results/task-aaaaaaaa.handoff.yaml: has more than one hard link", res.HandoffRefusals[0].FirstLine())
	assert.True(t, res.Incomplete(), "the exit must be non-zero")
}

// REV-277 CODEX-006: status and its triage decoration share one scan, in both forms.
func TestBUG209_StatusWithTriageScansOnce(t *testing.T) {
	root, _ := singleTCRefusalProject(t)
	putTriageRecord(t, root, singleTCRefusalID, "bats", "environment", "fail", "")

	t.Run("overview", func(t *testing.T) {
		count := countScans(t)
		entries, diags, err := PipelineStatusWithDiagnostics(root, nil, "", false)
		require.NoError(t, err)
		require.NoError(t, AttachTriageToEntriesFromScan(root, entries, diags, newTriageValues(), "", ""))
		assert.Equal(t, 1, *count)
	})
	t.Run("detail", func(t *testing.T) {
		snap, err := testcase.NewSnapshot(root)
		require.NoError(t, err)
		count := countScans(t)
		detail, diags, err := PipelineDetailWithSnapshot(snap, root, singleTCRefusalID, "", false)
		require.NoError(t, err)
		require.NoError(t, AttachTriageToDetailFromScan(root, detail, diags, newTriageValues(), "", ""))
		assert.Equal(t, 1, *count)
	})
}

// REV-277 CODEX-006: a caller with no scan of its own cannot lose a refusal.
func TestBUG209_StandaloneTriageAttachReportsRefusal(t *testing.T) {
	root, handoff := singleTCRefusalProject(t)
	require.NoError(t, os.Link(handoff, filepath.Join(t.TempDir(), "other-link")))
	err := AttachTriageToEntries(root, nil, newTriageValues(), "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has more than one hard link")
}

// REV-277 CODEX-006: gtms triage --value checks, resolves and snapshots from one scan.
func TestBUG209_TriageValueScansOnce(t *testing.T) {
	root := triageValueProject(t)
	count := countScans(t)
	_, err := ClassifyTriage(root, "tc-11111111", "environment", "", "", "", triageTestValues())
	require.NoError(t, err)
	assert.Equal(t, 1, *count)
}

// Section 8: two entries whose names look alike once escaped are still two paths, so each
// is named.
func TestBUG209_RefusalsDedupedByRawPath(t *testing.T) {
	var d IdentityDiagnostics
	d.addResultsRefusals(resultsRefusals{handoffs: []*result.RefusedHandoffError{
		{Rel: ".gtms/results/a\x01.handoff.yaml", Reason: "is a link"},
		{Rel: ".gtms/results/a\x02.handoff.yaml", Reason: "is a link"},
		{Rel: ".gtms/results/a\x02.handoff.yaml", Reason: "is a link"},
	}})
	var b strings.Builder
	d.Render(&b)
	assert.Equal(t, 2, strings.Count(b.String(), "refused handoff .gtms/results/a?.handoff.yaml: is a link"))
}
