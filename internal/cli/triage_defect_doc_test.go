package cli

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ENH-200 / CON-040 supersedes the BUG-180 defect-flag doc guard.
//
// The retired guard pinned the --defect help to "--app-wrong or --test-wrong"
// and the USER-GUIDE bullet to a category-specific exclusivity claim. Those
// categories no longer exist: --defect is optional and valid with EVERY --value.
//
// This narrow source-shape guard keeps the intent (the flag help must describe
// the current behaviour) without pinning prose: the --defect help must not name
// any retired category flag, and must tie itself to --value.
func TestSourceShape_DefectFlagHelpMatchesBehaviour(t *testing.T) {
	src, err := os.ReadFile("triage.go")
	require.NoError(t, err, "reading internal/cli/triage.go")
	flagHelp := string(src)

	require.Contains(t, flagHelp, `"defect", ""`,
		"the --defect flag registration moved; update this guard")

	// The --defect help must not resurrect the retired category flags.
	for _, retired := range []string{"--app-wrong", "--automation-wrong", "--test-wrong"} {
		assert.NotContains(t, flagHelp, "defect ID to record (use with "+retired,
			"the --defect help must not reference the retired category flags")
	}
}
