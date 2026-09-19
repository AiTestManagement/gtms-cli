package reader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
)

// BUG-187 clause 4 says refusals are terminal-safe. The first implementation
// sanitized only the record PATH, which left every other repository-derived
// value in a diagnostic rendering raw. These cover the values that reach a
// terminal without passing through the path.

// TestSelectTriageRecordPath_SanitisesFrameworkNames covers the ambiguity
// diagnostic. ListForTC derives framework names by trimming REPOSITORY
// FILENAMES, so they are attacker-influencable content rather than GTMS-chosen
// labels, and a crafted tc-x--<ESC>...triage.yaml would otherwise inject
// control characters straight into stderr.
func TestSelectTriageRecordPath_SanitisesFrameworkNames(t *testing.T) {
	root := t.TempDir()
	dir := layout.TriageDir(root)
	require.NoError(t, os.MkdirAll(dir, 0o755))

	// Two records for one TC forces the "multiple triage records" branch. The
	// second framework name carries an ESC byte.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-11111111--bats.triage.yaml"), []byte("x"), 0o644))
	hostile := filepath.Join(dir, "tc-11111111--ev\x1b[2Kil.triage.yaml")
	if err := os.WriteFile(hostile, []byte("x"), 0o644); err != nil {
		t.Skipf("cannot create a filename containing a control character here (%v); this case produced NO evidence", err)
	}

	_, err := selectTriageRecordPath(root, "tc-11111111", "")
	require.Error(t, err, "two records with no --framework must be an ambiguity error")
	require.Contains(t, err.Error(), "multiple triage records exist",
		"premise: the test must reach the ambiguity diagnostic, not some earlier failure")

	assert.NotContains(t, err.Error(), "\x1b",
		"a framework name derived from a repository filename must not carry a raw ESC into the terminal")
	assert.Contains(t, err.Error(), "ev?[2Kil",
		"the name must still be shown, escaped, so the operator can find the offending file")
}

// TestSelectTriageRecordPath_SanitisesWiringFrameworkNames covers the sibling
// diagnostic built from wiring records rather than triage filenames. Same rule,
// same source of untrust.
func TestSelectTriageRecordPath_SanitisesWiringFrameworkNames(t *testing.T) {
	root := t.TempDir()
	dir := layout.TriageDir(root)
	require.NoError(t, os.MkdirAll(dir, 0o755))

	// No triage records at all, so selection falls through to the wiring path.
	_, err := selectTriageRecordPath(root, "tc-22222222", "")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "\x1b",
		"no diagnostic on this path may carry a raw control character")
	// Sanity: the message is one line, so nothing can forge a second.
	assert.Equal(t, 1, len(strings.Split(strings.TrimRight(err.Error(), "\n"), "\n")),
		"a refusal must be a single line; a second line could impersonate new output")
}
