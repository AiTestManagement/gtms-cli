package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	targetpkg "github.com/aitestmanagement/gtms-cli/internal/target"
)

// REV-184 round 8 (human decision): runTriageList returned a failed render /
// write of the triage list without emitting the identity diagnostics; passing
// diags.Affected() to the renderer does not render or preserve them. The fix
// routes that error through failWithIdentityDiagnostics.
//
// renderTriageList writes to os.Stdout, so the test swaps os.Stdout for a file
// opened read-only: every write to it fails, and the JSON encoder returns that
// error.
func TestRunTriageList_WriteError_StillEmitsIdentityDiagnostics(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeTestFile(t, root, "gtms/test/cases/a/tc-e2e20001-ok.md",
		"---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-e2e20001\ntitle: One\n---\nbody\n")
	// An in-scope spec with no test_case_id: an unprovable identity that the
	// root triage list must diagnose.
	writeTestFile(t, root, "gtms/test/cases/a/tc-e2e20002-missing-id.md",
		"---\ntitle: stray\n---\nbody\n")

	roPath := filepath.Join(t.TempDir(), "stdout-readonly")
	require.NoError(t, os.WriteFile(roPath, nil, 0o644))
	ro, err := os.Open(roPath)
	require.NoError(t, err)
	defer ro.Close()

	var runErr error
	stderr := captureStderr(t, func() {
		orig := os.Stdout
		os.Stdout = ro
		defer func() { os.Stdout = orig }()
		runErr = runTriageList(root, false, targetpkg.Result{}, "", false, false, true)
	})

	require.Error(t, runErr, "a failed list write must still exit non-zero")
	var pathErr *os.PathError
	assert.ErrorAs(t, runErr, &pathErr, "the write error itself must be returned, not replaced")
	assert.Contains(t, stderr, "tc-e2e20002-missing-id.md", "the identity diagnostics must survive the write error")
	assert.Equal(t, 1, strings.Count(stderr, "cannot be proved from gtms/test/cases/a/tc-e2e20002-missing-id.md"),
		"the diagnostic is rendered once")
}
