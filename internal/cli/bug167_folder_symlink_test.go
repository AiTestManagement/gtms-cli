package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// REV-119 CODEX-001: the folder-classification path must not follow an out-of-tree
// symlink. An escaping symlink `gtms/test/cases/escape` -> external dir that
// contains a TC-shaped CHILD DIRECTORY (escape/tc-scope) whose spec carries an
// in-project TC ID must NOT let a folder-scoped command import that external ID
// and mutate matching in-project state. Every folder-aware command must reject the
// escaping scope with zero in-project mutation.

// addEscapeFolderWithTCChild creates an out-of-tree directory holding a TC-shaped
// child dir escape/tc-scope/ with a spec whose ID (tc-abc12345) collides with the
// in-project TC seeded by setupScopedProject, then symlinks it under the cases dir.
func addEscapeFolderWithTCChild(t *testing.T, root string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privilege on Windows")
	}
	external := t.TempDir() // sibling temp dir, outside the project root
	child := filepath.Join(external, "tc-scope")
	require.NoError(t, os.MkdirAll(child, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(child, "tc-abc12345-external.md"),
		[]byte("---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-abc12345\ntitle: External collider\n---\n"), 0o644))
	if err := os.Symlink(external, filepath.Join(root, "gtms/test/cases/escape")); err != nil {
		t.Skipf("symlink unsupported in this environment: %v", err)
	}
}

func TestFolderSymlinkEscape_NoInProjectMutation(t *testing.T) {
	skipIfShort(t)

	root := setupScopedProject(t) // tc-abc12345 lives ONLY under foo/
	restore := withExecuteGlobals(t, root, twoAdapterLinkConfig())
	defer restore()

	// Seed real wiring for the in-project tc-abc12345 so a broadened repoint/refresh
	// would have something to corrupt.
	artefact := filepath.Join(root, "test/acceptance/foo/real-test.bats")
	_, err := runCmdCaptureStderr(t, newLinkCmd(), "tc-abc12345", "--framework", "bats", "--artefact", artefact)
	require.NoError(t, err)
	wiringFile := filepath.Join(root, "gtms/automation/wiring/tc-abc12345--bats.wiring.yaml")
	inProjectSpec := filepath.Join(root, "gtms/test/cases/foo/tc-abc12345-real.md")

	addEscapeFolderWithTCChild(t, root) // may t.Skip on platforms without symlinks

	wiringBefore, err := os.ReadFile(wiringFile)
	require.NoError(t, err)

	assertNoInProjectMutation := func(t *testing.T, label string) {
		t.Helper()
		assert.FileExists(t, inProjectSpec, "%s: in-project spec must survive", label)
		after, rerr := os.ReadFile(wiringFile)
		require.NoError(t, rerr)
		assert.Equal(t, string(wiringBefore), string(after), "%s: in-project wiring must be unchanged", label)
		assert.NotContains(t, string(after), "new-runner", "%s: wiring must not be repointed", label)
		assertNoPipelineMutation(t, root)
	}

	// Every folder-aware command, driven through the escaping scope.
	cases := []struct {
		name   string
		newCmd func() *cobra.Command
		args   []string
	}{
		{"delete", newDeleteCmd, []string{"escape/tc-scope"}},
		{"reset", newResetCmd, []string{"escape/tc-scope"}},
		{"status", newStatusCmd, []string{"escape/tc-scope"}},
		{"map", newMapCmd, []string{"escape/tc-scope"}},
		{"execute", newExecuteCmd, []string{"escape/tc-scope"}},
		{"automate", newAutomateCmd, []string{"escape/tc-scope"}},
		{"link-refresh", newLinkCmd, []string{"escape/tc-scope", "--refresh"}},
		{"link-repoint", newLinkCmd, []string{"escape/tc-scope", "--adapter", "new-runner", "--from-adapter", "bats-runner"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _ = runCmdCaptureStderr(t, tc.newCmd(), tc.args...)
			assertNoInProjectMutation(t, tc.name)
		})
	}
}

// TestFolderSymlink_InTreeAlias_StillWorks confirms the hardening does not break a
// legitimate in-tree folder alias (a symlink resolving back inside the cases tree).
func TestFolderSymlink_InTreeAlias_StillWorks(t *testing.T) {
	skipIfShort(t)
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privilege on Windows")
	}
	root := setupScopedProject(t) // tc-abc12345 under foo/
	restore := withStatusGlobals(t, root, batsExecuteConfig())
	defer restore()

	// gtms/test/cases/alias -> gtms/test/cases/foo (both in-tree).
	if err := os.Symlink(filepath.Join(root, "gtms/test/cases/foo"),
		filepath.Join(root, "gtms/test/cases/alias")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	out, err := runCmdCaptureStdout(t, newStatusCmd(), "alias")
	require.NoError(t, err, "an in-tree folder alias must resolve, not error")
	assert.Contains(t, out, "tc-abc12345", "the alias must scope to foo/'s test case")
}
