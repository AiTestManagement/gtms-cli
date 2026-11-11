package reader

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BUG-209 section 8: a refused entry cannot be attributed to a test case, so a read
// of ONE test case must report it too, not only a folder read. These write a hard
// link, which needs no privilege on either platform.

const singleTCRefusalID = "tc-11111111"

func singleTCRefusalProject(t *testing.T) (root, handoff string) {
	t.Helper()
	root = t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "gtms.config"), []byte("project:\n  name: t\n  repo: r\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "gtms", ".gtms-root"), nil, 0o644))

	cases := filepath.Join(root, "gtms", "test", "cases", "fixture")
	require.NoError(t, os.MkdirAll(cases, 0o755))
	spec := "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: " + singleTCRefusalID +
		"\ntitle: \"Fixture\"\nrequirement: \"BUG-209\"\npriority: Medium\ncreated: 2026-11-06\n---\n\n## Test Objective\n\nFixture.\n"
	require.NoError(t, os.WriteFile(filepath.Join(cases, singleTCRefusalID+"-x.md"), []byte(spec), 0o644))

	results := filepath.Join(root, ".gtms", "results")
	require.NoError(t, os.MkdirAll(results, 0o755))
	handoff = filepath.Join(results, "task-99999999.handoff.yaml")
	require.NoError(t, os.WriteFile(handoff, []byte("task: task-99999999\nstatus: complete\nresult: pass\n"), 0o644))
	return root, handoff
}

func TestBUG209_PipelineDetailReportsRefusal(t *testing.T) {
	root, handoff := singleTCRefusalProject(t)
	snap, err := testcase.NewSnapshot(root)
	require.NoError(t, err)

	// Control: a single-link handoff is not a refusal.
	_, diags, err := PipelineDetailWithSnapshot(snap, root, singleTCRefusalID, "", false)
	require.NoError(t, err)
	assert.False(t, diags.Affected(), "a single-link handoff must not be refused")

	require.NoError(t, os.Link(handoff, filepath.Join(t.TempDir(), "other-link.yaml")))
	detail, diags, err := PipelineDetailWithSnapshot(snap, root, singleTCRefusalID, "", false)
	require.NoError(t, err)
	require.NotNil(t, detail, "the safe detail must still be returned")
	assert.True(t, diags.Affected(), "a hard-linked handoff must be reported by a single test case read")

	var b strings.Builder
	diags.Render(&b)
	assert.Contains(t, b.String(), "refused handoff .gtms/results/task-99999999.handoff.yaml: has more than one hard link")

	// A caller with no stderr channel gets the typed error, not a clean answer.
	_, err = PipelineDetail(root, singleTCRefusalID, "", false)
	var incomplete *IncompleteReadError
	assert.True(t, errors.As(err, &incomplete), "PipelineDetail must return IncompleteReadError, got %v", err)
}

func TestBUG209_TriageSingleTestCaseReadsReportRefusal(t *testing.T) {
	root, handoff := singleTCRefusalProject(t)
	putTriageRecord(t, root, singleTCRefusalID, "bats", "environment", "fail", "")
	snap, err := testcase.NewSnapshot(root)
	require.NoError(t, err)

	// Control: a single-link handoff is not a refusal.
	view, err := ShowTriage(root, singleTCRefusalID, "bats")
	require.NoError(t, err)
	assert.False(t, view.Diagnostics.Affected(), "a single-link handoff must not be refused")

	require.NoError(t, os.Link(handoff, filepath.Join(t.TempDir(), "other-link.yaml")))

	view, err = ShowTriage(root, singleTCRefusalID, "bats")
	require.NoError(t, err)
	assert.True(t, view.Diagnostics.Affected(), "triage show must report the refusal")

	views, diags, err := ListTriageForTCWithSnapshot(snap, root, singleTCRefusalID, "", false)
	require.NoError(t, err)
	assert.Len(t, views, 1, "the safe record must still be listed")
	assert.True(t, diags.Affected(), "triage --list <tc> must report the refusal")
}

// BUG-209 AC31: an unexpected failure scanning .gtms/results is reported by every
// report command, never shown as no results.
func TestBUG209_ScanFailureIsReportedByEveryReader(t *testing.T) {
	root, _ := singleTCRefusalProject(t)
	putTriageRecord(t, root, singleTCRefusalID, "bats", "environment", "fail", "")
	snap, err := testcase.NewSnapshot(root)
	require.NoError(t, err)

	result.InjectScanReadFailureForTest(errors.New("injected list failure"))
	t.Cleanup(func() { result.InjectScanReadFailureForTest(nil) })

	const want = "refused results directory .gtms/results: could not be inspected: injected list failure"
	assertReported := func(name string, diags IdentityDiagnostics, err error) {
		t.Helper()
		require.NoError(t, err, name)
		var b strings.Builder
		diags.Render(&b)
		assert.Contains(t, b.String(), want, name)
	}

	_, diags, err := PipelineStatusWithDiagnostics(root, nil, "", false)
	assertReported("status", diags, err)
	_, diags, err = PipelineDetailWithSnapshot(snap, root, singleTCRefusalID, "", false)
	assertReported("status <tc>", diags, err)
	_, diags, err = PipelineFolderSummaryWithDiagnostics(root, "", false)
	assertReported("status folder summary", diags, err)
	_, diags, err = GapsWithDiagnostics(root, nil, "", false)
	assertReported("gaps", diags, err)
	_, diags, err = MapWithDiagnostics(root, nil, "", false)
	assertReported("map", diags, err)
	_, diags, err = ListTriageWithDiagnostics(root, nil, "", false)
	assertReported("triage --list", diags, err)
	view, err := ShowTriage(root, singleTCRefusalID, "bats")
	require.NoError(t, err)
	assertReported("triage <tc>", view.Diagnostics, nil)
}
