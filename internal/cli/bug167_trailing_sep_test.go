package cli

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/config"
)

// runCmdCaptureStdout runs a cobra command with os.Stdout redirected to a pipe and
// returns what it wrote. status/map render their overview/detail views to os.Stdout
// directly (not the cobra writer), so this is how their output is asserted.
func runCmdCaptureStdout(t *testing.T, cmd *cobra.Command, args ...string) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	cmd.SetArgs(args)
	execErr := cmd.Execute()
	_ = w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	return string(out), execErr
}

// REV-117 CODEX-001: a trailing path separator makes a target an explicit folder,
// so "gtms <cmd> tc-abc12345/" must be treated as a (non-existent) folder scope and
// must NEVER operate on the GLOBAL bare id tc-abc12345 that lives under foo/.
// setupScopedProject seeds tc-abc12345 only under foo/ (see bug059_scoped_test.go).

// assertGlobalTCUntouched fails if the global tc-abc12345 spec under foo/ was
// deleted, or if any wiring/task/result was written for the bare id.
func assertGlobalTCUntouched(t *testing.T, root string) {
	t.Helper()
	foo := filepath.Join(root, "gtms/test/cases/foo/tc-abc12345-real.md")
	_, err := os.Stat(foo)
	assert.NoError(t, err, "the global tc-abc12345 spec under foo/ must survive a folder-shaped target")
	wiring, _ := filepath.Glob(filepath.Join(root, "gtms/automation/wiring", "tc-abc12345*"))
	assert.Empty(t, wiring, "no wiring may be written for the global bare id")
	assertNoPipelineMutation(t, root)
}

// TestTrailingSeparator_NoGlobalTCOperation drives every mutating folder-aware
// surface with "tc-abc12345/". With the fix the input classifies as a folder
// scope (the folder tc-abc12345/ does not exist), so no command may reach the
// global bare-id path. Without the fix delete/execute/automate/reset/link would
// broaden to the global tc-abc12345 under foo/.
func TestTrailingSeparator_NoGlobalTCOperation(t *testing.T) {
	skipIfShort(t)

	const art = "test/acceptance/foo/real-test.bats"
	cases := []struct {
		name   string
		newCmd func() *cobra.Command
		args   func(root string) []string
		cfg    func() *config.Config
	}{
		{"delete", newDeleteCmd,
			func(r string) []string { return []string{"tc-abc12345/"} }, batsExecuteConfig},
		// Windows-capable backslash probe: a trailing "\" is the native Windows
		// folder signal and must also block the global deletion. Portable -- runs
		// on Windows CI (pure classification, no symlink).
		{"delete-backslash", newDeleteCmd,
			func(r string) []string { return []string{`tc-abc12345\`} }, batsExecuteConfig},
		{"execute", newExecuteCmd,
			func(r string) []string { return []string{"tc-abc12345/"} }, batsExecuteConfig},
		{"automate", newAutomateCmd,
			func(r string) []string { return []string{"tc-abc12345/"} }, batsExecuteConfig},
		{"reset", newResetCmd,
			func(r string) []string { return []string{"tc-abc12345/"} }, batsExecuteConfig},
		{"link-standard", newLinkCmd,
			func(r string) []string {
				return []string{"tc-abc12345/", "--artefact", filepath.Join(r, art), "--framework", "bats"}
			}, batsLinkConfig},
		{"link-check", newLinkCmd,
			func(r string) []string {
				return []string{"tc-abc12345/", "--check", "--artefact", filepath.Join(r, art), "--framework", "bats"}
			}, batsLinkConfig},
		{"link-refresh", newLinkCmd,
			func(r string) []string { return []string{"tc-abc12345/", "--refresh"} }, batsLinkConfig},
		{"link-repoint", newLinkCmd,
			func(r string) []string {
				return []string{"tc-abc12345/", "--adapter", "bats-runner", "--from-adapter", "bats-runner"}
			}, batsLinkConfig},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := setupScopedProject(t)
			restore := withExecuteGlobals(t, root, tc.cfg())
			defer restore()

			// Exit status varies by command (folder-not-found error vs no-op); the
			// invariant is that NONE operate on the global bare id.
			_, _ = runCmdCaptureStderr(t, tc.newCmd(), tc.args(root)...)
			assertGlobalTCUntouched(t, root)
		})
	}
}

// TestTrailingSeparator_ReadCommands_FolderOverview confirms status and map take
// folder/overview semantics for "tc-abc12345/" rather than rendering the global
// single-TC detail. The foo/ spec's title is the differential: it appears in the
// bare-id TC detail (positive control) but not in the empty-folder overview.
func TestTrailingSeparator_ReadCommands_FolderOverview(t *testing.T) {
	skipIfShort(t)

	for _, name := range []string{"status", "map"} {
		t.Run(name, func(t *testing.T) {
			newCmd := newStatusCmd
			if name == "map" {
				newCmd = newMapCmd
			}

			root := setupScopedProject(t)
			restore := withStatusGlobals(t, root, batsExecuteConfig())
			defer restore()

			// Positive control: the bare id renders the global TC detail (title shown).
			bareOut, _ := runCmdCaptureStdout(t, newCmd(), "tc-abc12345")
			require.Contains(t, bareOut, "Real spec under foo",
				"%s <bare-id> must render the global TC detail (positive control)", name)

			// Folder-shaped: the empty tc-abc12345/ scope must NOT render that TC.
			restore2 := withStatusGlobals(t, root, batsExecuteConfig())
			defer restore2()
			folderOut, _ := runCmdCaptureStdout(t, newCmd(), "tc-abc12345/")
			assert.NotContains(t, folderOut, "Real spec under foo",
				"%s tc-abc12345/ must take folder overview, not global TC detail", name)
		})
	}
}
