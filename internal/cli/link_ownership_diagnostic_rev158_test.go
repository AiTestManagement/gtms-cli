package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/config"
)

// REV-158 CLAUDE-006 at the CLI boundary. `gtms link` on an ambiguous id used to
// print "test case spec for tc-77777777 not found under .../gtms/test/cases"
// followed by the hint "Check the artefact path or use --force to overwrite." --
// an identity failure rendered as absence, with a hint pointing at a flag that
// cannot help and whose only effect would be to overwrite.
func TestLink_AmbiguousOwnership_TypedDiagnosticNoForceHint(t *testing.T) {
	skipIfShort(t)
	root := setupCompatProject(t)
	seedBulkSpec(t, root, "alpha", "tc-77777777", "tc-77777777-a.md")
	seedBulkSpec(t, root, "beta", "tc-77777777", "tc-77777777-b.md")

	require.NoError(t, os.MkdirAll(filepath.Join(root, "test"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "test", "a.bats"), []byte("x\n"), 0o644))

	cfg := &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {"bats-runner": {Mode: "sync", Command: "echo PASS", Framework: "bats"}},
		},
		Defaults: map[string]string{"execute": "bats-runner"},
	}
	restore := withExecuteGlobals(t, root, cfg)
	defer restore()

	out, err := runCmdCaptureStderr(t, newLinkCmd(),
		"tc-77777777", "--framework", "bats", "--artefact", "test/a.bats", "--force")
	require.Error(t, err)

	assert.Contains(t, out, "is ambiguous", "the typed ambiguity diagnostic must be shown")
	assert.Contains(t, out, "alpha", "both owners must be named")
	assert.Contains(t, out, "beta")
	assert.NotContains(t, out, "not found under",
		"an identity failure must never be rendered as absence")
	assert.NotContains(t, out, "--force to overwrite",
		"--force cannot resolve an ambiguous identity and must not be suggested")
}
