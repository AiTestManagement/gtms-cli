package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ENH-210 CLI-boundary coverage: the triage --list positional is classified
// with the shared existence-first classifier, so a trailing separator forces
// folder interpretation, -r is rejected for a test-case target, and the
// broadened positional-count diagnostic no longer names a folder alone.
//
// The trailing-BACKSLASH form lives here rather than in the BATS acceptance
// suite: shells strip or reinterpret a trailing backslash inconsistently, so
// only a Go command-boundary test can prove triage actually reuses the
// classifier's trailing-separator rule (see tc-cb6db9d7 Notes).

func withTriageProjectRoot(t *testing.T, root string) {
	t.Helper()
	savedRoot := projectRoot
	savedCfg := appConfig
	projectRoot = root
	appConfig = nil // human list output needs no config; JSON label is best-effort
	t.Cleanup(func() {
		projectRoot = savedRoot
		appConfig = savedCfg
	})
}

func writeTriageProjectFile(t *testing.T, root, rel, content string) {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.WriteFile(abs, []byte(content), 0o644))
}

func seedTC99999999(t *testing.T, root string) {
	t.Helper()
	writeTriageProjectFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeTriageProjectFile(t, root, "gtms/test/cases/search/tc-99999999.md",
		"---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-99999999\ntitle: t\n---\nbody\n")
	writeTriageProjectFile(t, root, "gtms/triage/tc-99999999--bats.triage.yaml",
		"version: 1\ntest_case_id: tc-99999999\nframework: bats\nstatus: active\nvalue: environment\nclassified_at: 2026-09-01T09:00:00Z\nsnapshot:\n  task: t\n  result: fail\n")
}

// runTriageCapStdout runs the triage command capturing os.Stdout (where the
// list rows are printed), returning the stdout and the command error.
func runTriageCapStdout(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var err error
	out := captureStdout(t, func() { _, err = runTriage(args...) })
	return out, err
}

func TestTriageList_TrailingBackslashClassifiesAsFolder(t *testing.T) {
	root := t.TempDir()
	seedTC99999999(t, root)
	withTriageProjectRoot(t, root)

	// Bare ID (no directory of that name) is a test-case target: the record lists.
	out, err := runTriageCapStdout(t, "--list", "tc-99999999")
	require.NoError(t, err)
	assert.Contains(t, out, "tc-99999999 [bats] active -- environment",
		"a bare test-case ID must list its record")

	// Trailing backslash is an explicit folder signal: the absent folder is an
	// empty scope, so the record must NOT be listed even though the ID matches.
	outBS, err := runTriageCapStdout(t, "--list", `tc-99999999\`)
	require.NoError(t, err)
	assert.NotContains(t, outBS, "-- environment",
		"a trailing backslash must force folder interpretation, not a test-case listing")
	assert.Contains(t, outBS, "No triage records.")
}

func TestTriageList_RecursiveRejectedForTestCaseTarget(t *testing.T) {
	root := t.TempDir()
	seedTC99999999(t, root)
	withTriageProjectRoot(t, root)

	// The rejection message is on the returned error; the folder-scope hint is on
	// os.Stderr via output.Errorf.
	stderr := captureStderr(t, func() {
		out, err := runTriageCapStdout(t, "--list", "tc-99999999", "-r")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not valid with a test-case target")
		assert.NotContains(t, out, "-- environment", "nothing is listed on rejection")
	})
	assert.Contains(t, strings.ToLower(stderr), "folder list scopes",
		"the diagnostic must explain that recursion applies to folder list scopes")

	// -r stays valid for a folder target (decided by classification).
	outFolder, err := runTriageCapStdout(t, "--list", "search", "-r")
	require.NoError(t, err)
	assert.Contains(t, outFolder, "tc-99999999 [bats] active -- environment")
}

func TestTriageList_BroadenedPositionalCountDiagnostic(t *testing.T) {
	out, err := runTriage("--list", "tc-11111111", "tc-22222222")
	require.Error(t, err)
	assert.Contains(t, out, "The --list command accepts at most one folder or test-case ID.")
}

func TestTriageList_HelpDescribesFolderOrTestCasePositional(t *testing.T) {
	out, err := runTriage("--help")
	require.NoError(t, err)
	assert.Contains(t, out, "[folder|test-case-id]",
		"help must describe the --list positional as a folder or a test-case ID")
}
