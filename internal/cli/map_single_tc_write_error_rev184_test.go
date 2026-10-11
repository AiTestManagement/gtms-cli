package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// REV-184 round 7 (human decision): "Fix runMapSingleTC so diagnostics survive
// every output-write-error path."
//
// runMapSingleTC used to return a failed output write without rendering the
// identity diagnostics, so `map <tc-id>` failed naming only the write error
// while an in-group ambiguous identity went unreported.

var errMapWriteBoom = errors.New("map write boom")

type failingMapWriter struct{}

func (failingMapWriter) Write([]byte) (int, error) { return 0, errMapWriteBoom }

// seedMapTargetWithAmbiguousSibling: target tc-11111111 in REQ-1, and an
// ambiguous tc-22222222 with one owner in REQ-1, so the target's printed group
// is affected and the diagnostics are non-empty.
func seedMapTargetWithAmbiguousSibling(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeTestFile(t, root, "gtms/test/cases/a/tc-11111111-target.md",
		"---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-11111111\ntitle: Target\nrequirement: REQ-1\n---\n")
	writeTestFile(t, root, "gtms/test/cases/a/tc-22222222-dup.md",
		"---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-22222222\ntitle: Dup\nrequirement: REQ-1\n---\n")
	writeTestFile(t, root, "gtms/test/cases/b/tc-22222222-dup.md",
		"---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-22222222\ntitle: Dup\nrequirement: REQ-2\n---\n")
	return root
}

func TestMapSingleTC_WriteError_StillEmitsIdentityDiagnostics(t *testing.T) {
	root := seedMapTargetWithAmbiguousSibling(t)
	snap := mapTestSnap(t, root)

	var err error
	stderr := captureStderr(t, func() {
		err = runMapSingleTC(failingMapWriter{}, snap, root, "tc-11111111", true, "", false)
	})

	require.Error(t, err, "a failed output write must still exit non-zero")
	assert.ErrorIs(t, err, errMapWriteBoom, "the write error itself must be returned")
	assert.Contains(t, stderr, "tc-22222222", "the identity diagnostics must survive the write error")
	assert.Equal(t, 1, strings.Count(stderr, "is ambiguous"), "the diagnostic is rendered once")
}

// Control: on the success path the diagnostics are rendered exactly once, by
// finishWithIdentityDiagnostics, and the command still exits non-zero.
func TestMapSingleTC_Success_IdentityDiagnosticsRenderedOnce(t *testing.T) {
	root := seedMapTargetWithAmbiguousSibling(t)
	snap := mapTestSnap(t, root)

	var buf bytes.Buffer
	var err error
	stderr := captureStderr(t, func() {
		err = runMapSingleTC(&buf, snap, root, "tc-11111111", true, "", false)
	})

	require.Error(t, err, "an affected identity makes the read incomplete")
	assert.NotErrorIs(t, err, errMapWriteBoom)
	assert.Contains(t, buf.String(), "tc-11111111", "the map output is written")
	assert.Equal(t, 1, strings.Count(stderr, "is ambiguous"), "no double reporting on the success path")
}
