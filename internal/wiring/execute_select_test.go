package wiring

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
)

// seedExecSelectWiring writes a valid wiring record file for (tc, framework).
func seedExecSelectWiring(t *testing.T, root, tc, framework string) {
	t.Helper()
	_, err := Write(root, &WiringRecord{
		TestCase:     tc,
		TestCaseHash: "0011223344556677",
		Framework:    framework,
		Adapter:      framework + "-runner",
		Artefact:     "tests/" + tc + ".bats",
		ArtefactHash: "aabbccddeeff0011",
	})
	require.NoError(t, err)
}

// writeRawWiringBytes writes arbitrary bytes to the canonical wiring path, bypassing
// the schema writer, to model a malformed/unreadable file on disk.
func writeRawWiringBytes(t *testing.T, root, name, content string) {
	t.Helper()
	dir := layout.WiringDir(root)
	require.NoError(t, os.MkdirAll(dir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0644))
}

func TestSelectForExecute_NotWired(t *testing.T) {
	root := t.TempDir()
	sel, err := SelectForExecute(root, "tc-11111111", "")
	require.NoError(t, err)
	assert.Equal(t, ExecuteSkip, sel.Outcome)
	assert.Equal(t, "not wired", sel.Reason)
}

func TestSelectForExecute_NoFrameworkWiring(t *testing.T) {
	root := t.TempDir()
	seedExecSelectWiring(t, root, "tc-11111111", "bats")
	sel, err := SelectForExecute(root, "tc-11111111", "playwright")
	require.NoError(t, err)
	assert.Equal(t, ExecuteSkip, sel.Outcome)
	assert.Contains(t, sel.Reason, "playwright")
}

func TestSelectForExecute_SingleRecordFound(t *testing.T) {
	root := t.TempDir()
	seedExecSelectWiring(t, root, "tc-11111111", "bats")

	// Discovery.
	sel, err := SelectForExecute(root, "tc-11111111", "")
	require.NoError(t, err)
	require.Equal(t, ExecuteFound, sel.Outcome)
	assert.Equal(t, "bats", sel.Selection.Framework)
	assert.Equal(t, "tc-11111111", sel.Selection.TestCase)

	// Explicit framework.
	sel, err = SelectForExecute(root, "tc-11111111", "bats")
	require.NoError(t, err)
	assert.Equal(t, ExecuteFound, sel.Outcome)
}

func TestSelectForExecute_MultipleFrameworksNotRun(t *testing.T) {
	root := t.TempDir()
	seedExecSelectWiring(t, root, "tc-11111111", "bats")
	seedExecSelectWiring(t, root, "tc-11111111", "playwright")
	sel, err := SelectForExecute(root, "tc-11111111", "")
	require.NoError(t, err)
	assert.Equal(t, ExecuteNotRun, sel.Outcome)
	assert.Contains(t, sel.Reason, "multiple frameworks")
}

// No-framework discovery is exactly the generic scanner
// (FindAllForTCSelections): a malformed file next to a valid record is warned
// about and dropped, and the valid record is selected. BUG-200 scope cut
// (2026-10-25): BUG-212 owns changing this.
func TestSelectForExecute_MalformedDiscovery_MatchesGenericScanner(t *testing.T) {
	root := t.TempDir()
	seedExecSelectWiring(t, root, "tc-11111111", "bats")
	writeRawWiringBytes(t, root, "tc-11111111--playwright.wiring.yaml", "this is: [not valid yaml")

	want, err := FindAllForTCSelections(root, "tc-11111111")
	require.NoError(t, err)
	require.Len(t, want, 1)

	sel, err := SelectForExecute(root, "tc-11111111", "")
	require.NoError(t, err)
	require.Equal(t, ExecuteFound, sel.Outcome)
	assert.Equal(t, want[0].Path, sel.Selection.Path)
	assert.Equal(t, "bats", sel.Selection.Framework)
}

func TestSelectForExecute_MalformedApplicable_ExplicitFramework(t *testing.T) {
	root := t.TempDir()
	writeRawWiringBytes(t, root, "tc-11111111--bats.wiring.yaml", "this is: [not valid yaml")
	sel, err := SelectForExecute(root, "tc-11111111", "bats")
	require.NoError(t, err)
	assert.Equal(t, ExecuteError, sel.Outcome)
}

func TestSelectForExecute_UnrelatedMalformed_Ignored(t *testing.T) {
	root := t.TempDir()
	seedExecSelectWiring(t, root, "tc-11111111", "bats")
	// A malformed file for a DIFFERENT, out-of-scope test case.
	writeRawWiringBytes(t, root, "tc-99999999--bats.wiring.yaml", "this is: [not valid yaml")
	sel, err := SelectForExecute(root, "tc-11111111", "")
	require.NoError(t, err)
	assert.Equal(t, ExecuteFound, sel.Outcome, "an unrelated malformed file must not affect this TC")
}

// A readable record whose body framework disagrees with its filename is NOT an
// ExecuteError here; it is returned as Found so the caller's VerifySelection
// reports the identity mismatch (BUG-211). Same for a body testcase mismatch on
// the explicit-framework path.
func TestSelectForExecute_ReadableIdentityMismatch_NotError(t *testing.T) {
	root := t.TempDir()
	writeRawWiringBytes(t, root, "tc-11111111--bats.wiring.yaml",
		"testcase: tc-11111111\ntestcase-hash: 0011223344556677\nframework: playwright\nadapter: bats-runner\nartefact: tests/x.bats\nartefact-hash: aabbccddeeff0011\n")
	sel, err := SelectForExecute(root, "tc-11111111", "bats")
	require.NoError(t, err)
	assert.Equal(t, ExecuteFound, sel.Outcome)
}

// Explicit framework selects only the exact file tc--framework.wiring.yaml: a
// missing one is the ordinary miss even when another framework's file exists.
func TestSelectForExecute_ExplicitFramework_MissingIsMiss(t *testing.T) {
	root := t.TempDir()
	writeRawWiringBytes(t, root, "tc-11111111--playwright.wiring.yaml", "this is: [not valid yaml")
	sel, err := SelectForExecute(root, "tc-11111111", "bats")
	require.NoError(t, err)
	assert.Equal(t, ExecuteSkip, sel.Outcome)
	assert.Equal(t, "no bats wiring", sel.Reason)
}

// DCODEX-001 guard: a valid record whose framework contains "--" (tc-77777777,
// framework legacy--bats) is selected for tc-77777777 in discovery, by its
// body, even though its filename also reads as (tc-77777777--legacy, bats).
func TestSelectForExecute_DoubleDashFrameworkSelectedInDiscovery(t *testing.T) {
	root := t.TempDir()
	seedExecSelectWiring(t, root, "tc-77777777", "legacy--bats")

	sel, err := SelectForExecute(root, "tc-77777777", "")
	require.NoError(t, err)
	require.Equal(t, ExecuteFound, sel.Outcome)
	assert.Equal(t, "tc-77777777", sel.Selection.TestCase)
	assert.Equal(t, "legacy--bats", sel.Selection.Framework)

	// The extended id does not claim it: its body names tc-77777777.
	sel, err = SelectForExecute(root, "tc-77777777--legacy", "")
	require.NoError(t, err)
	assert.Equal(t, ExecuteSkip, sel.Outcome)
}
