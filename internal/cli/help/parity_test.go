package help

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParity_AgentGuideMirrorsCanonical guards the embedded-template parity
// rule: the package-local mirror that go:embed snapshots into the binary, and
// the canonical authored file at reference/ai-coding-assistant-guide.md, must
// stay byte-for-byte identical. Without this guard a future edit to one
// without the other silently introduces drift.
//
// Pattern: same as internal/scaffold/source_shape_test.go
// TestSourceShape_StarterGuideMatchesDogfoodMirror.
func TestParity_AgentGuideMirrorsCanonical(t *testing.T) {
	canonical, err := os.ReadFile(findCanonicalGuide(t))
	require.NoError(t, err, "reading canonical reference/ai-coding-assistant-guide.md")

	// Normalise: the Go embed uses LF; the on-disk file may be CRLF on
	// Windows checkouts. Strip trailing newlines from both sides so the
	// comparison is invariant to platform line-ending style.
	want := strings.TrimRight(strings.ReplaceAll(string(canonical), "\r\n", "\n"), "\n")
	got := strings.TrimRight(strings.ReplaceAll(AgentGuide, "\r\n", "\n"), "\n")

	assert.Equal(t, want, got,
		"AgentGuide (internal/cli/help/ai-coding-assistant-guide.md) and "+
			"reference/ai-coding-assistant-guide.md have drifted; copy the canonical "+
			"file to the mirror to fix")
}

// TestParity_AgentGuideNonEmpty ensures the embed is not accidentally empty.
func TestParity_AgentGuideNonEmpty(t *testing.T) {
	require.NotEmpty(t, AgentGuide,
		"AgentGuide must not be empty -- go:embed did not find the file")
	// Sanity: the guide is ~360 lines / ~20KB.
	assert.Greater(t, len(AgentGuide), 10000,
		"AgentGuide is suspiciously short -- expected ~20KB, got %d bytes", len(AgentGuide))
}

// TestParity_AgentGuideNoRelativeLinks guards against relative markdown links
// that render as terminal noise when gtms agent prints the guide. The canonical
// file was reworded (ENH-179) to replace [text](../path) links with plain text
// directing readers to gtms --help for version-pinned URLs.
func TestParity_AgentGuideNoRelativeLinks(t *testing.T) {
	assert.NotContains(t, AgentGuide, "../USER-GUIDE.md",
		"AgentGuide should not contain the relative '../USER-GUIDE.md' link "+
			"(renders as terminal noise when printed by gtms agent)")
	assert.NotContains(t, AgentGuide, "](adapter-guide.md)",
		"AgentGuide should not contain the relative '](adapter-guide.md)' link "+
			"(renders as terminal noise when printed by gtms agent)")
}

// findCanonicalGuide walks up from the test's CWD looking for gtms.config
// (project root marker), then returns the path to the canonical guide file.
// Tests in this package run with CWD = internal/cli/help, so the walk is short.
func findCanonicalGuide(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "gtms.config")); err == nil {
			return filepath.Join(dir, "reference", "ai-coding-assistant-guide.md")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("project root not found: no gtms.config in any ancestor of %s", dir)
		}
		dir = parent
	}
}
