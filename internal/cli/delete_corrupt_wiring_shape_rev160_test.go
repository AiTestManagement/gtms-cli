package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/output"
)

// REV-160 CLAUDE-007(b) -- the F2 corrupt-wiring refusal bypassed output.Errorf.
// runDelete fell through to a bare `return err`, so it exited non-zero (that half
// was always fine) but printed without the established
// "<icon> <message>\n    <hint>" shape every other refusal in the same function
// uses. ADR-024 s7 requires a refusal to reach the user with an actionable
// diagnostic on the documented error stream.
func TestRunDelete_CorruptWiringRefusalUsesTheStandardErrorShape(t *testing.T) {
	root := t.TempDir()

	cases := filepath.Join(root, "gtms", "test", "cases", "checkout")
	require.NoError(t, os.MkdirAll(cases, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cases, "tc-77777777-owner.md"),
		[]byte("---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-77777777\ntitle: owner\n---\n"), 0o644))

	wdir := filepath.Join(root, "gtms", "automation", "wiring")
	require.NoError(t, os.MkdirAll(wdir, 0o755))
	// Named for tc-77777777 but declaring tc-99999999: a filename/body identity
	// mismatch, which ADR-024 s1 makes a data-integrity error, never absence.
	require.NoError(t, os.WriteFile(filepath.Join(wdir, "tc-77777777--bats.wiring.yaml"),
		[]byte("testcase: tc-99999999\nframework: bats\nadapter: b\nartefact: x\n"), 0o644))

	var err error
	stderr := captureStderr(t, func() {
		err = runDelete(io.Discard, root, nil, "tc-77777777", false, false, true)
	})

	require.Error(t, err, "a corrupt wiring record must refuse the whole plan")
	assert.True(t, output.IsDisplayed(err),
		"the refusal must be marked as already displayed, or main.go prints it a second time in a different shape")

	lines := strings.Split(strings.TrimRight(stderr, "\n"), "\n")
	require.GreaterOrEqual(t, len(lines), 2,
		"expected a message line and a hint line, got %q", stderr)
	assert.True(t, strings.HasPrefix(lines[0], output.IconError+" "),
		"the message line must carry the standard error icon, got %q", lines[0])
	assert.Contains(t, lines[0], "corrupt or its declared identity disagrees with its filename")
	assert.True(t, strings.HasPrefix(lines[1], "    "),
		"the hint must be indented four spaces like every other refusal, got %q", lines[1])
	assert.Contains(t, lines[1], "Fix or remove the wiring record")
}
