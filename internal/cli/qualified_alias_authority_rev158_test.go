package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sha256Of(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// REV-158 CLAUDE-001 at the CLI boundary. delete and reset used to drop the
// folder qualifier after requireScopedSpec and call the core with a nil scope,
// so the destructive plan could only check project-wide ownership. With
// beta/tc-22222222-beta.md real and alpha/tc-22222222-linked.md a symlink to it,
// `gtms delete alpha/tc-22222222` exited 0 and deleted the file in BETA.
//
// This pins the CLI half: the qualifier reaches the core, the command exits
// non-zero, and the out-of-scope target is byte-identical afterwards.
func TestDeleteAndReset_QualifiedThroughSpecAlias_RefuseAtCLI(t *testing.T) {
	skipIfShort(t)
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs elevation on Windows")
	}
	root := setupCompatProject(t)
	beta := seedBulkSpec(t, root, "beta", "tc-22222222", "tc-22222222-beta.md")

	alpha := filepath.Join(root, "gtms", "test", "cases", "alpha")
	require.NoError(t, os.MkdirAll(alpha, 0o755))
	link := filepath.Join(alpha, "tc-22222222-linked.md")
	if err := os.Symlink(beta, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	before := sha256Of(t, beta)

	restore := withExecuteGlobals(t, root, nil)
	defer restore()

	out, err := runCmdCaptureStderr(t, newDeleteCmd(), "alpha/tc-22222222")
	require.Error(t, err, "gtms delete alpha/tc-22222222 must refuse")
	assert.Contains(t, out, "does not grant authority")
	assert.Equal(t, before, sha256Of(t, beta), "the out-of-scope target must be byte-identical")

	out, err = runCmdCaptureStderr(t, newResetCmd(), "alpha/tc-22222222")
	require.Error(t, err, "gtms reset alpha/tc-22222222 must refuse")
	assert.Contains(t, out, "does not grant authority")
	assert.Equal(t, before, sha256Of(t, beta), "the out-of-scope target must be byte-identical")

	_, lerr := os.Lstat(link)
	assert.NoError(t, lerr, "the link itself must be untouched by a refusal")
}
