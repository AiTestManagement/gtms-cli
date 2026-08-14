package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BUG-180 / REV-141 round 4 (CODEX-003, CLAUDE-001).
//
// --test-wrong now persists --defect into the triage history entry. Two
// user-facing surfaces describe that flag: the Cobra flag usage string
// and the USER-GUIDE flag list. When the behaviour changed, the flag help
// was updated and the guide was not, so the canonical reference actively
// contradicted `gtms triage --help`.
//
// This is a deliberately NARROW guard, not prose pinning (see BUG-159 for
// why pinning documentation wholesale is a trap). It asserts two things
// only: the flag help names both categories that persist the value, and
// the guide no longer carries the exclusivity claim that the change
// falsified. Rewording either surface is free as long as neither reverts
// to "only --app-wrong".
func TestSourceShape_DefectFlagDocsAgreeWithBehaviour(t *testing.T) {
	src, err := os.ReadFile("triage.go")
	require.NoError(t, err, "reading internal/cli/triage.go")
	flagHelp := string(src)

	require.Contains(t, flagHelp, `"defect", ""`,
		"the --defect flag registration moved; update this guard")
	assert.Contains(t, flagHelp, "--app-wrong or --test-wrong",
		"flag help must name both categories that persist a defect")

	guidePath := findUserGuide(t)
	guideBytes, err := os.ReadFile(guidePath)
	require.NoError(t, err, "reading USER-GUIDE.md")
	guide := string(guideBytes)

	defectLine := ""
	for _, line := range strings.Split(guide, "\n") {
		if strings.HasPrefix(line, "- `--defect <id>`") {
			defectLine = line
			break
		}
	}
	require.NotEmpty(t, defectLine,
		"USER-GUIDE no longer has a `--defect <id>` flag bullet; update this guard")

	assert.NotContains(t, defectLine, "only meaningful with `--app-wrong`",
		"the guide still claims --defect is app-wrong only, which --test-wrong now falsifies")
	assert.Contains(t, defectLine, "--test-wrong",
		"the guide's --defect bullet must mention --test-wrong")
}

// findUserGuide walks up to the project root and returns USER-GUIDE.md.
// Same shape as findCanonicalGuide in internal/cli/help/parity_test.go.
func findUserGuide(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "gtms.config")); err == nil {
			return filepath.Join(dir, "USER-GUIDE.md")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("project root not found: no gtms.config in any ancestor of %s", dir)
		}
		dir = parent
	}
}
