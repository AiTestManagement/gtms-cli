package cli

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// REV-184 round 7 (human approval): "Extend failWithIdentityDiagnostics(diags,
// err) to the identified error paths in those five functions, with one focused
// regression test per function."
//
// Each of these bulk read commands used to return a failed output write without
// rendering the identity diagnostics, so the command failed naming only the
// write error while an affected (ambiguous) identity went unreported. Each test
// forces the JSON write to fail on a project holding an ambiguous ID and pins:
// the write error itself is returned (non-zero exit), and the ambiguous ID is
// still named on stderr, exactly once.

// seedAmbiguousReadProject: a unique tc-11111111 plus tc-22222222 owned by two
// specs in different folders (ambiguous project-wide).
func seedAmbiguousReadProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeTestFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeTestFile(t, root, "gtms/test/cases/a/tc-11111111-one.md",
		"---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-11111111\ntitle: One\nrequirement: REQ-1\n---\n")
	writeTestFile(t, root, "gtms/test/cases/a/tc-22222222-dup.md",
		"---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-22222222\ntitle: Dup\nrequirement: REQ-1\n---\n")
	writeTestFile(t, root, "gtms/test/cases/b/tc-22222222-dup.md",
		"---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-22222222\ntitle: Dup\nrequirement: REQ-2\n---\n")
	return root
}

// assertWriteErrorKeepsDiagnostics runs fn with stderr captured and checks the
// write error is returned unchanged and the ambiguous ID is diagnosed once.
func assertWriteErrorKeepsDiagnostics(t *testing.T, fn func() error) {
	t.Helper()
	var err error
	stderr := captureStderr(t, func() { err = fn() })
	require.Error(t, err, "a failed output write must still exit non-zero")
	assert.ErrorIs(t, err, errMapWriteBoom, "the write error itself must be returned")
	assert.Contains(t, stderr, "tc-22222222", "the identity diagnostics must survive the write error")
	assert.Equal(t, 1, strings.Count(stderr, "is ambiguous"), "the diagnostic is rendered once")
}

func TestRunGaps_WriteError_StillEmitsIdentityDiagnostics(t *testing.T) {
	root := seedAmbiguousReadProject(t)
	assertWriteErrorKeepsDiagnostics(t, func() error {
		return runGaps(failingMapWriter{}, root, nil, true, "", false)
	})
}

func TestRunGapsFolderSummary_WriteError_StillEmitsIdentityDiagnostics(t *testing.T) {
	root := seedAmbiguousReadProject(t)
	assertWriteErrorKeepsDiagnostics(t, func() error {
		return runGapsFolderSummary(failingMapWriter{}, root, true, "")
	})
}

func TestRunMap_WriteError_StillEmitsIdentityDiagnostics(t *testing.T) {
	root := seedAmbiguousReadProject(t)
	assertWriteErrorKeepsDiagnostics(t, func() error {
		return runMap(failingMapWriter{}, root, nil, false, true, "", false)
	})
}

func TestRunStatusOverview_WriteError_StillEmitsIdentityDiagnostics(t *testing.T) {
	root := seedAmbiguousReadProject(t)
	assertWriteErrorKeepsDiagnostics(t, func() error {
		return runStatusOverview(failingMapWriter{}, root, nil, true, "", false, false, "", nil)
	})
}

func TestRunStatusFolderSummary_WriteError_StillEmitsIdentityDiagnostics(t *testing.T) {
	root := seedAmbiguousReadProject(t)
	assertWriteErrorKeepsDiagnostics(t, func() error {
		return runStatusFolderSummary(failingMapWriter{}, root, true, "", false, nil)
	})
}
