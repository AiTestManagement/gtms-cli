package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/output"
)

// REV-184 round 6 (human decision): "Report the in-scope unprovable specs before
// returning the wiring-scan error, while preserving that scan error and the
// non-zero exit."
//
// runFolderRefresh used to return the wiring-scan error before the loop that
// reports in-scope unprovable specs, so a folder holding an unprovable spec AND
// an unreadable wiring directory failed naming only the scan error.

// seedUnprovableRefreshFolder writes one provable spec (so the folder has an
// owner id and reaches the wiring scan) and one spec with no test_case_id (an
// in-scope unprovable candidate).
func seedUnprovableRefreshFolder(t *testing.T, root, folder string) {
	t.Helper()
	dir := filepath.Join(root, "gtms/test/cases", folder)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-e1e10001-ok.md"),
		[]byte("---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-e1e10001\ntitle: t\n---\n# body\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-e1e10002-missing-id.md"),
		[]byte("---\ntitle: t\nrequirement: X\n---\n# body\n"), 0o644))
}

// runLinkRefreshCaptureBoth returns stdout (where refresh prints per-record
// lines) and stderr (where output.Errorf writes) from one run.
func runLinkRefreshCaptureBoth(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	stderr = captureStderr(t, func() {
		stdout, err = runCmdCaptureStdout(t, newLinkCmd(), args...)
	})
	return stdout, stderr, err
}

func TestLinkRefresh_WiringScanError_StillReportsInScopeUnprovable(t *testing.T) {
	skipIfShort(t)
	for _, dry := range []bool{false, true} {
		root := setupCompatProject(t)
		restore := withExecuteGlobals(t, root, batsLinkConfig())

		folder := "scanfail"
		seedUnprovableRefreshFolder(t, root, folder)
		// A regular file where the wiring directory belongs: the scan's
		// directory read fails, which DiscoverRefreshTargets returns as an error.
		require.NoError(t, os.WriteFile(filepath.Join(root, "gtms/automation/wiring"), []byte("x"), 0o644))

		args := []string{folder, "--refresh"}
		if dry {
			args = append(args, "--dry-run")
		}
		stdout, stderr, err := runLinkRefreshCaptureBoth(t, args...)
		restore()

		require.Error(t, err, "dry=%v: the wiring-scan failure must still exit non-zero", dry)
		assert.True(t, output.IsDisplayed(err), "dry=%v: the scan error must be rendered through output.Errorf", dry)
		assert.Contains(t, stderr, "Scanning wiring for "+folder+"/", "dry=%v: the scan error must be preserved", dry)
		assert.Contains(t, stdout, "tc-e1e10002-missing-id.md",
			"dry=%v: the in-scope unprovable spec must be reported before the scan error returns", dry)
		assert.Equal(t, 1, strings.Count(stdout, "ownership of a candidate spec under"), "dry=%v: reported exactly once", dry)
	}
}

// Control: on the normal path (wiring scan succeeds) the unprovable spec is
// reported exactly once -- the early report is only on the scan-error path.
func TestLinkRefresh_InScopeUnprovable_ReportedOnceOnNormalPath(t *testing.T) {
	skipIfShort(t)
	root := setupCompatProject(t)
	restore := withExecuteGlobals(t, root, batsLinkConfig())
	defer restore()

	seedUnprovableRefreshFolder(t, root, "scanok")

	stdout, stderr, err := runLinkRefreshCaptureBoth(t, "scanok", "--refresh")
	require.Error(t, err, "an unprovable in-scope spec is a counted failure")
	assert.NotContains(t, stderr, "Scanning wiring for", "the wiring scan must have succeeded in the control")
	assert.Equal(t, 1, strings.Count(stdout, "ownership of a candidate spec under"), "no duplicate report on the normal path")
	assert.Contains(t, stdout, "0 refreshed, 0 current, 1 failed")
}
