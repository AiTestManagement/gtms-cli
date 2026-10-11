package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeAllSpec(t *testing.T, root, rel, id string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte("---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: "+id+"\ntitle: t\n---\n"), 0o644))
}

// BUG-185 audit item: `gtms link --all` is the project-wide bulk repoint and
// rewrites wiring, so it requires UniqueOwner. A duplicate id anywhere in the
// project refuses the whole --all before any rewrite (mutation-checked).
func TestLink_RepointAll_ProjectWideAmbiguity_Refuses(t *testing.T) {
	skipIfShort(t)
	root := setupCompatProject(t)
	restore := withExecuteGlobals(t, root, batsLinkConfig())
	defer restore()

	writeAllSpec(t, root, "gtms/test/cases/alpha/tc-a1b2c3d4-first.md", "tc-a1b2c3d4")
	writeAllSpec(t, root, "gtms/test/cases/beta/tc-deadbeef-renamed.md", "tc-a1b2c3d4")

	out, err := runCmdCaptureStderr(t, newLinkCmd(),
		"--all", "--from-adapter", "bats-runner", "--adapter", "remote-bats")
	require.Error(t, err, "--all must refuse a project-wide duplicate id before any rewrite")
	assert.Contains(t, out, "ambiguous")
	assert.Contains(t, out, "tc-a1b2c3d4")
}

// Positive control: a project with only unique ids does not hit the ownership
// refusal on --all (it may report nothing to repoint, but never an ambiguity).
func TestLink_RepointAll_UniqueProject_NoOwnershipRefusal(t *testing.T) {
	skipIfShort(t)
	root := setupCompatProject(t)
	restore := withExecuteGlobals(t, root, batsLinkConfig())
	defer restore()

	writeAllSpec(t, root, "gtms/test/cases/alpha/tc-11111111-only.md", "tc-11111111")

	out, _ := runCmdCaptureStderr(t, newLinkCmd(),
		"--all", "--from-adapter", "bats-runner", "--adapter", "remote-bats")
	assert.NotContains(t, out, "ambiguous", "a unique project must not hit the ownership refusal")
}
