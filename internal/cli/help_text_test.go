package cli

import (
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

// allUserCommands returns cobra command constructors for all user-facing commands.
func allUserCommands() map[string]func() *cobra.Command {
	return map[string]func() *cobra.Command{
		"create":   newCreateCmd,
		"automate": newAutomateCmd,
		"execute":  newExecuteCmd,
		"status":   newStatusCmd,
		"gaps":     newGapsCmd,
		"map":      newMapCmd,
		"triage":   newTriageCmd,
		"delete":   newDeleteCmd,
		"list":     newListCmd,
		"reset":    newResetCmd,
		"init":     newInitCmd,
	}
}

// TestHelpText_GapsNoFourCategories verifies the stale "four categories" claim was removed.
func TestHelpText_GapsNoFourCategories(t *testing.T) {
	cmd := newGapsCmd()
	long := cmd.Long
	assert.NotContains(t, long, "four categories",
		"gaps Long should not mention 'four categories' (actually seven)")
}

// TestHelpText_MapNoMapReport verifies the internal Go type name was removed from --json.
func TestHelpText_MapNoMapReport(t *testing.T) {
	cmd := newMapCmd()
	usage := cmd.UsageString()
	assert.NotContains(t, usage, "MapReport",
		"map --help should not leak the internal Go type name 'MapReport'")
}

// TestHelpText_CreateLineCount verifies create --help stays within the 50-line cap.
func TestHelpText_CreateLineCount(t *testing.T) {
	cmd := newCreateCmd()
	// Count lines in Long + UsageString combined (full --help output)
	full := cmd.Long + "\n" + cmd.UsageString()
	lines := strings.Split(full, "\n")
	assert.LessOrEqual(t, len(lines), 50,
		"create --help should be at most 50 lines, got %d", len(lines))
}

// TestHelpText_CreateNoInputLayers verifies tutorial content was removed from create.
func TestHelpText_CreateNoInputLayers(t *testing.T) {
	cmd := newCreateCmd()
	long := cmd.Long
	assert.NotContains(t, long, "INPUT LAYERS",
		"create Long should not contain INPUT LAYERS section")
	assert.NotContains(t, long, "DATA FLOW",
		"create Long should not contain DATA FLOW section")
}

// TestHelpText_ExecuteNoLegacyManualResult verifies execute --help no longer shows
// the legacy --result bypass (ENH-138: bypass removed).
func TestHelpText_ExecuteNoLegacyManualResult(t *testing.T) {
	cmd := newExecuteCmd()
	long := cmd.Long
	assert.NotContains(t, long, "--result pass",
		"execute Long should not contain --result pass (legacy bypass removed)")
	assert.NotContains(t, long, "--result fail",
		"execute Long should not contain --result fail (legacy bypass removed)")
	assert.NotContains(t, long, "Manual result (no adapter runs",
		"execute Long should not contain the legacy manual result help block")
}

// TestHelpText_ExecuteEnvExample verifies execute --help shows a --env example.
func TestHelpText_ExecuteEnvExample(t *testing.T) {
	cmd := newExecuteCmd()
	long := cmd.Long
	assert.Contains(t, long, "--env staging",
		"execute Long should contain a --env example")
}

// TestHelpText_UsageArgConsistency verifies commands that accept both a tc-id and a folder
// use the standard "[test-case-id | folder]" syntax in their Usage line.
func TestHelpText_UsageArgConsistency(t *testing.T) {
	commandsWithBothArgs := []struct {
		name   string
		newCmd func() *cobra.Command
	}{
		{"status", newStatusCmd},
		{"map", newMapCmd},
		{"reset", newResetCmd},
		{"automate", newAutomateCmd},
		{"execute", newExecuteCmd},
		{"delete", newDeleteCmd},
	}

	for _, tc := range commandsWithBothArgs {
		t.Run(tc.name, func(t *testing.T) {
			cmd := tc.newCmd()
			useLine := cmd.UseLine()
			assert.Contains(t, useLine, "[test-case-id | folder]",
				"%s Usage line should contain '[test-case-id | folder]', got: %s", tc.name, useLine)
			assert.NotContains(t, useLine, "[folder-or-tc-id]",
				"%s Usage line should not contain the old '[folder-or-tc-id]' form", tc.name)
		})
	}
}

// TestHelpText_NoTCsAbbreviation verifies no command uses the "TCs" abbreviation in help text.
func TestHelpText_NoTCsAbbreviation(t *testing.T) {
	// Match whole-word "TCs" (not part of words like "TCSettings" etc.)
	tcsPattern := regexp.MustCompile(`\bTCs\b`)

	commands := allUserCommands()
	for name, buildCmd := range commands {
		t.Run(name, func(t *testing.T) {
			cmd := buildCmd()
			// Check both the Long description and the Usage/flags output
			combined := cmd.Long + "\n" + cmd.UsageString()
			assert.False(t, tcsPattern.MatchString(combined),
				"%s --help should not contain the abbreviation 'TCs'; use 'test cases' instead", name)
		})
	}
}

// TestHelpText_TriageLowercaseUsage verifies triage uses lowercase <test-case-id>.
func TestHelpText_TriageLowercaseUsage(t *testing.T) {
	cmd := newTriageCmd()
	useLine := cmd.UseLine()
	assert.Contains(t, useLine, "<test-case-id>",
		"triage Usage should use lowercase '<test-case-id>', got: %s", useLine)
	assert.NotContains(t, useLine, "<test-case-ID>",
		"triage Usage should not use uppercase '<test-case-ID>'")
}

// TestHelpText_TriageExactlyOneConstraint verifies triage help mentions the constraint.
func TestHelpText_TriageExactlyOneConstraint(t *testing.T) {
	cmd := newTriageCmd()
	long := cmd.Long
	assert.Contains(t, long, "exactly one of",
		"triage Long should note that exactly one category flag is required")
}

// TestHelpText_StatusFolderSummaryCaption verifies the default status caption is accurate.
func TestHelpText_StatusFolderSummaryCaption(t *testing.T) {
	cmd := newStatusCmd()
	long := cmd.Long
	assert.Contains(t, long, "folder summary",
		"status Long should describe the default as 'folder summary'")
}

// TestHelpText_DeleteNoBATS verifies delete help is framework-neutral.
func TestHelpText_DeleteNoBATS(t *testing.T) {
	cmd := newDeleteCmd()
	usage := cmd.UsageString()
	assert.NotContains(t, usage, "BATS scripts",
		"delete --help should not name BATS as the sole acceptance script format")
}
