package cli

// ADR-024 5b "Qualified targets resolve once, too": a qualified <folder>/<tc-id>
// status, map or triage --list target must build EXACTLY ONE project-wide snapshot,
// shared by the scoped-owner check and the reader -- not one for each. ENH-205 fixed
// the pre-existing double build.
//
// REV-184 R2-CODEX-002 / R2-CLAUDE-001: these tests drive the REAL cobra commands
// (newStatusCmd / newMapCmd / newTriageCmd via cmd.Execute) so the counter is
// measured around the complete CLI operation, as 5b requires ("rooted at the
// complete CLI operation, not at the reader entry point"). A RunE that builds a
// second snapshot -- e.g. by calling the snapshot-building requireScopedSpec(root,
// res) or a non-WithSnapshot reader wrapper -- fails them.
//
// Each command is also driven with a WRONG-folder qualification, which must be
// refused: that proves the qualified target actually reached the scoped-owner
// check inside RunE, so the count of 1 is not the count of a path that skipped it.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
)

// oneSnapshotProject seeds tc-11111111 under checkout/ and tc-22222222 under
// other/, so "other/tc-11111111" is a well-formed qualified target whose folder
// exists but does not own the ID.
func oneSnapshotProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "gtms.config"), []byte("project:\n  name: x\n  repo: x\n"), 0o644))
	for _, s := range []struct{ folder, file, id string }{
		{"checkout", "tc-11111111-login.md", "tc-11111111"},
		{"other", "tc-22222222-logout.md", "tc-22222222"},
	} {
		dir := filepath.Join(root, "gtms", "test", "cases", s.folder)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, s.file),
			[]byte("---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: "+s.id+"\ntitle: T\nrequirement: REQ-1\n---\n"), 0o644))
	}
	return root
}

type qualifiedCmdCase struct {
	name   string
	newCmd func() *cobra.Command
	args   func(target string) []string
}

var qualifiedCmdCases = []qualifiedCmdCase{
	{"status", newStatusCmd, func(tg string) []string { return []string{tg} }},
	{"map", newMapCmd, func(tg string) []string { return []string{tg} }},
	{"triage --list", newTriageCmd, func(tg string) []string { return []string{"--list", tg} }},
}

// runQualified drives one fresh command instance through cobra's Execute with
// stdout and stderr captured, and returns the snapshot-build delta around it.
func runQualified(t *testing.T, c qualifiedCmdCase, target string) (int64, string, error) {
	t.Helper()
	cmd := c.newCmd()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	before := testcase.SnapshotBuildsForTest()
	var out string
	var err error
	_ = captureStderr(t, func() {
		out, err = runCmdCaptureStdout(t, cmd, c.args(target)...)
	})
	return testcase.SnapshotBuildsForTest() - before, out, err
}

// TestADR024_QualifiedTargetBuildsOneSnapshot_RealCommand asserts, per command,
// that the complete `gtms <cmd> checkout/tc-11111111` invocation builds exactly
// one project-wide snapshot.
func TestADR024_QualifiedTargetBuildsOneSnapshot_RealCommand(t *testing.T) {
	root := oneSnapshotProject(t)
	restore := withStatusGlobals(t, root, &config.Config{})
	defer restore()

	for _, c := range qualifiedCmdCases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			delta, out, err := runQualified(t, c, "checkout/tc-11111111")
			require.NoError(t, err, "qualified %s must succeed for an in-folder owner", c.name)
			if c.name != "triage --list" { // no triage records seeded: the list is legitimately empty
				assert.Contains(t, out, "tc-11111111", "%s must render the qualified test case", c.name)
			}
			assert.Equal(t, int64(1), delta,
				"qualified %s <folder>/<tc-id> must build exactly one snapshot across the whole "+
					"CLI operation (ADR-024 5b): the scoped-owner check and the read share it", c.name)
		})
	}
}

// TestADR024_QualifiedTargetWrongFolderRefused_RealCommand proves the qualified
// branch really runs the scoped-owner check inside RunE: tc-11111111 exists, but
// not under other/, so every command must refuse it.
func TestADR024_QualifiedTargetWrongFolderRefused_RealCommand(t *testing.T) {
	root := oneSnapshotProject(t)
	restore := withStatusGlobals(t, root, &config.Config{})
	defer restore()

	for _, c := range qualifiedCmdCases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			delta, _, err := runQualified(t, c, "other/tc-11111111")
			require.Error(t, err, "%s other/tc-11111111 must be refused: the owner is under checkout/", c.name)
			assert.Equal(t, int64(1), delta,
				"a refused qualified %s must still build exactly one snapshot", c.name)
		})
	}
}
