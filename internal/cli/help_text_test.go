package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/cli/help"
	"github.com/aitestmanagement/gtms-cli/internal/onboarding"
	"github.com/aitestmanagement/gtms-cli/internal/scaffold"
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
		"link":     newLinkCmd,
		"list":     newListCmd,
		"reset":    newResetCmd,
		"init":     newInitCmd,
		"prime":    newPrimeCmd,
		"version":  newVersionCmd,
		"agent":           newAgentCmd,
		"skills":          newSkillsCmd,
		"getting-started": newGettingStartedCmd,
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
// BUG-115: delete removed from this list -- it now uses <test-case-id | folder>
// (required positional) because Args is cobra.ExactArgs(1).
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

	// Exempt the agent topic: its Long is the full embedded cheat sheet
	// (reference doc), not typical help prose. The cheat sheet legitimately
	// uses "TCs" as a compact abbreviation throughout.
	exempt := map[string]bool{"agent": true}

	commands := allUserCommands()
	for name, buildCmd := range commands {
		if exempt[name] {
			continue
		}
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

// BUG-113: --dry-run flag visibility tests.
//
// After the BUG-113 fix, --dry-run is hidden from the root persistent flags
// (no longer appears in any --help global flags block) but registered as a
// local flag on delete and reset only.

// TestHelpText_DryRunHiddenFromRootHelp verifies --dry-run does not appear
// in the root command's help output after MarkHidden.
func TestHelpText_DryRunHiddenFromRootHelp(t *testing.T) {
	usage := rootCmd.UsageString()
	assert.NotContains(t, usage, "--dry-run",
		"rootCmd --help should not show --dry-run (hidden via MarkHidden)")
}

// TestHelpText_DryRunHiddenFromAllSubcommands verifies --dry-run does not
// appear in any subcommand's Global Flags block. Walks the entire tree
// including nested commands (create status, automate status, etc.).
func TestHelpText_DryRunHiddenFromAllSubcommands(t *testing.T) {
	// Exceptions: delete and reset register --dry-run as a local flag,
	// so it will appear in their help -- but in the Flags section, not
	// Global Flags.
	exceptions := map[string]bool{"delete": true, "reset": true}

	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		if exceptions[cmd.Name()] {
			return
		}
		usage := cmd.UsageString()
		// Extract only the Global Flags section
		globalSection := extractSection(usage, "Global Flags:")
		assert.NotContains(t, globalSection, "--dry-run",
			"%s --help Global Flags should not show --dry-run", cmd.CommandPath())
		for _, sub := range cmd.Commands() {
			walk(sub)
		}
	}

	for _, sub := range rootCmd.Commands() {
		walk(sub)
	}
}

// TestHelpText_DryRunVisibleOnDelete verifies --dry-run appears in the
// local Flags block of delete --help.
func TestHelpText_DryRunVisibleOnDelete(t *testing.T) {
	cmd := newDeleteCmd()
	usage := cmd.UsageString()
	localFlags := extractSection(usage, "Flags:")
	assert.Contains(t, localFlags, "--dry-run",
		"delete --help local Flags should show --dry-run")
}

// TestHelpText_DryRunVisibleOnReset verifies --dry-run appears in the
// local Flags block of reset --help.
func TestHelpText_DryRunVisibleOnReset(t *testing.T) {
	cmd := newResetCmd()
	usage := cmd.UsageString()
	localFlags := extractSection(usage, "Flags:")
	assert.Contains(t, localFlags, "--dry-run",
		"reset --help local Flags should show --dry-run")
}

// ENH-179: root help Windows note (moved to footer).

// TestHelpText_RootWindowsNote verifies the root help Long string
// names the Windows auto-detection guarantee in its footer.
func TestHelpText_RootWindowsNote(t *testing.T) {
	long := rootCmd.Long

	assert.Contains(t, long, "Git for Windows",
		"root Long should name the Windows prerequisite")
	assert.Contains(t, long, "script adapters",
		"root Long should scope the guarantee to script adapters")
}

// ENH-179: root help tagline, signpost, and guide URL tests.

// TestHelpText_RootTaglineAndSignpost verifies the root help Long string opens
// with the tagline and contains the AI signpost and guide permalink.
func TestHelpText_RootTaglineAndSignpost(t *testing.T) {
	long := rootCmd.Long

	// Tagline
	assert.True(t, strings.HasPrefix(long, "GTMS tracks your testing the way Git tracks your code."),
		"root Long should open with the tagline")

	// AI signpost
	assert.Contains(t, long, "IF YOU ARE AN AI CODING ASSISTANT",
		"root Long should contain the AI signpost")

	// Version-agnostic guide permalink (ref is "main" on dev, tag on release)
	assert.Contains(t, long, "github.com/aitestmanagement/gtms-cli/blob/",
		"root Long should contain a guide permalink with the repo blob prefix")
	assert.Contains(t, long, "USER-GUIDE.md",
		"root Long should reference USER-GUIDE.md")

	// De-anchored: no #adapter-execution-model fragment
	assert.NotContains(t, long, "#adapter-execution-model",
		"root Long Full-guide link should not contain the adapter-execution-model anchor")
}

// TestHelpText_PipelineAdapterExecutionFooter verifies that the four pipeline
// commands carry a uniform "Adapter execution:" footer with a name-only
// reference to the USER-GUIDE section and NO raw URLs.
func TestHelpText_PipelineAdapterExecutionFooter(t *testing.T) {
	commands := map[string]func() *cobra.Command{
		"create":   newCreateCmd,
		"prime":    newPrimeCmd,
		"automate": newAutomateCmd,
		"execute":  newExecuteCmd,
	}
	for name, buildCmd := range commands {
		t.Run(name, func(t *testing.T) {
			cmd := buildCmd()
			long := cmd.Long

			assert.Contains(t, long, "Adapter execution:",
				"%s Long should contain the 'Adapter execution:' footer label", name)
			assert.Contains(t, long, "Adapter Execution Model",
				"%s Long should reference the USER-GUIDE section by name", name)
			assert.Contains(t, long, "Adapters run identically on every OS",
				"%s Long should state the cross-platform guarantee", name)

			// Subcommand footers must NOT carry raw URLs -- permalinks live in root only.
			assert.NotContains(t, long, "https://",
				"%s Long should not contain raw URLs (those belong in root help only)", name)
		})
	}
}

// TestHelpText_NoFalseNoConfigFallback verifies that create, automate, and
// execute footers do not claim a no-config fallback (only prime has one via
// manual-prime -- resolver.go:42-44).
func TestHelpText_NoFalseNoConfigFallback(t *testing.T) {
	noFallbackCmds := map[string]func() *cobra.Command{
		"create":   newCreateCmd,
		"automate": newAutomateCmd,
		"execute":  newExecuteCmd,
	}
	for name, buildCmd := range noFallbackCmds {
		t.Run(name, func(t *testing.T) {
			long := buildCmd().Long
			assert.NotContains(t, long, "with none configured",
				"%s footer must not imply a no-config fallback", name)
			assert.NotContains(t, long, "no adapter configured",
				"%s footer must not imply a no-config fallback", name)
		})
	}

	// prime DOES have an implicit default
	t.Run("prime has manual-prime default", func(t *testing.T) {
		long := newPrimeCmd().Long
		assert.Contains(t, long, "manual-prime",
			"prime footer should name its built-in default (manual-prime)")
	})
}

// TestHelpText_CreateOldPointerRemoved verifies the old mid-paragraph
// USER-GUIDE/adapter-guide pointer was reconciled into the footer.
func TestHelpText_CreateOldPointerRemoved(t *testing.T) {
	long := newCreateCmd().Long
	assert.NotContains(t, long, "See USER-GUIDE.md for the create workflow",
		"create Long should not contain the old mid-paragraph doc pointer")
	assert.NotContains(t, long, "reference/adapter-guide.md for prompt-template",
		"create Long should not reference adapter-guide.md directly (moved to root)")
}

// --- ENH-173: Embedded AI coding-assistant cheat sheet tests ---

// TestENH173_HelpTopicPrintsEmbeddedCheatSheet verifies that running the
// agent command prints the full embedded cheat sheet with known anchors.
func TestENH173_HelpTopicPrintsEmbeddedCheatSheet(t *testing.T) {
	cmd := newAgentCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	require.NoError(t, err, "agent command should succeed")

	output := buf.String()
	require.NotEmpty(t, output, "agent command should produce output")

	// Assert known section-heading anchors from the cheat sheet.
	anchors := []string{
		"Workflow Sequences",
		"Mode 3",
		"Status Icons",
		"AI-Specific Gotchas",
		"Integration Principles",
		"Debugging Failures",
		"Further Reading",
	}
	for _, anchor := range anchors {
		assert.Contains(t, output, anchor,
			"agent output must contain cheat-sheet anchor %q", anchor)
	}
}

// TestENH173_AgentTopicDiscoverable verifies the agent topic appears in the
// root command's subcommand list (and therefore in "gtms help" output).
func TestENH173_AgentTopicDiscoverable(t *testing.T) {
	var found bool
	for _, sub := range rootCmd.Commands() {
		if sub.Name() == "agent" {
			found = true
			assert.NotEmpty(t, sub.Short,
				"agent command must have a Short description for discoverability")
			break
		}
	}
	assert.True(t, found, "agent command must be registered on rootCmd")
}

// TestENH173_CheatSheetCommandsExistInCobraTree parses fenced command blocks
// from the embedded guide and asserts every gtms command token resolves
// against the assembled cobra command tree. This is the drift guard -- it
// fails when a command is renamed or removed but the cheat sheet still
// references the old name.
//
// Scope: fenced blocks only (```sh/```bash ... ```). Prose tables contain
// retired flags as negative examples and are exempt by construction.
func TestENH173_CheatSheetCommandsExistInCobraTree(t *testing.T) {
	// Extract fenced code blocks from the guide.
	fencedContent := extractFencedBlocks(help.AgentGuide)
	require.NotEmpty(t, fencedContent,
		"AgentGuide must contain fenced command blocks")

	// Parse gtms command tokens from the fenced blocks.
	commands := extractGTMSCommands(fencedContent)
	require.NotEmpty(t, commands,
		"fenced blocks must contain gtms command references")

	// Parse flag tokens from the fenced blocks.
	flags := extractGTMSFlags(fencedContent)
	require.NotEmpty(t, flags,
		"fenced blocks must contain flag references")

	// Verify each command exists in the cobra tree.
	for _, cmdName := range commands {
		t.Run("cmd/"+cmdName, func(t *testing.T) {
			found := findCobraCommand(rootCmd, cmdName)
			assert.NotNil(t, found,
				"cheat sheet references 'gtms %s' in a fenced block but no such command exists in the cobra tree", cmdName)
		})
	}

	// Verify each flag exists on at least one command.
	for _, flagName := range flags {
		t.Run("flag/"+flagName, func(t *testing.T) {
			found := flagExistsOnAnyCommand(rootCmd, flagName)
			assert.True(t, found,
				"cheat sheet references '--%s' in a fenced block but no command accepts that flag", flagName)
		})
	}
}

// TestENH173_StructuralLiveness_AllVisibleCommandsHelpExitsZero recursively
// walks the assembled root command's visible (non-hidden) subcommands and
// asserts that each one's help renders without error and includes its Use
// line. This is the structural-liveness guard left unfiled by CLI help audit
// v1's Step 5 (AC from ENH-173).
func TestENH173_StructuralLiveness_AllVisibleCommandsHelpExitsZero(t *testing.T) {
	// Cobra internals to exclude -- these are hidden or framework-generated.
	cobraInternals := map[string]bool{
		"completion": true,
		"help":       true,
	}

	var checked int
	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		if cmd.Hidden || cobraInternals[cmd.Name()] {
			return
		}
		t.Run(cmd.CommandPath(), func(t *testing.T) {
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			err := cmd.Help()
			assert.NoError(t, err,
				"%s help should not error", cmd.CommandPath())

			output := buf.String()
			// Use cmd.Name() rather than cmd.Use because cobra overrides
			// the rendered Use line for commands with subcommands (e.g.
			// "list <adapters|frameworks|all>" becomes "gtms list [command]").
			// For hyphenated topic names (e.g. "getting-started"), also
			// accept the space-separated form case-insensitively since the
			// topic content may use "Getting Started" instead of the
			// hyphenated command name.
			nameFound := strings.Contains(output, cmd.Name()) ||
				strings.Contains(strings.ToLower(output), strings.ReplaceAll(cmd.Name(), "-", " "))
			assert.True(t, nameFound,
				"%s help output should contain its command name (or space-separated equivalent)", cmd.CommandPath())
			checked++
		})
		for _, sub := range cmd.Commands() {
			walk(sub)
		}
	}

	for _, sub := range rootCmd.Commands() {
		walk(sub)
	}

	// Sanity: we should have checked a reasonable number of commands.
	// As of ENH-173 there are 15+ user-facing commands including nested ones.
	assert.GreaterOrEqual(t, checked, 15,
		"structural-liveness walk should check at least 15 commands, got %d", checked)
}

// --- ENH-179: agent topic presentation and signpost tests ---

// TestENH179_SignpostRootOnly verifies the AI signpost appears only in root
// help and is not repeated on any subcommand. Walks the same tree as
// TestHelpText_DryRunHiddenFromAllSubcommands.
func TestENH179_SignpostRootOnly(t *testing.T) {
	signpost := "IF YOU ARE AN AI CODING ASSISTANT"

	// Positive: signpost IS in root Long
	assert.Contains(t, rootCmd.Long, signpost,
		"root Long should contain the AI signpost")

	// Negative: no subcommand Long contains the signpost
	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		if cmd.Name() == "help" || cmd.Name() == "completion" {
			return
		}
		assert.NotContains(t, cmd.Long, signpost,
			"%s Long should not contain the AI signpost (root only)", cmd.CommandPath())
		for _, sub := range cmd.Commands() {
			walk(sub)
		}
	}

	for _, sub := range rootCmd.Commands() {
		walk(sub)
	}
}

// TestENH179_AgentBucketedAsHelpTopic verifies that the rendered root help
// lists "agent" under "Additional help topics:" and not under "Available
// Commands:". Asserts on rendered help sections, not cobra-tree membership.
func TestENH179_AgentBucketedAsHelpTopic(t *testing.T) {
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	err := rootCmd.Help()
	require.NoError(t, err, "root help should not error")

	output := buf.String()

	// Output must contain both sections
	require.Contains(t, output, "Available Commands:",
		"root help must have 'Available Commands:' section")
	require.Contains(t, output, "Additional help topics:",
		"root help must have 'Additional help topics:' section")

	// Split at "Additional help topics:" to get the two sections
	parts := strings.SplitN(output, "Additional help topics:", 2)
	require.Len(t, parts, 2, "should split into exactly two parts")

	commandsSection := parts[0] // everything before "Additional help topics:"
	topicsSection := parts[1]   // everything after

	// "agent" must be in the topics section, not in the commands section
	// (after the "Available Commands:" header)
	commandsPart := ""
	if idx := strings.Index(commandsSection, "Available Commands:"); idx >= 0 {
		commandsPart = commandsSection[idx:]
	}

	assert.NotRegexp(t, `(?m)^\s+agent\s`, commandsPart,
		"'agent' should NOT appear as a command row under 'Available Commands:'")
	assert.Contains(t, topicsSection, "agent",
		"'agent' should appear under 'Additional help topics:'")
}

// TestENH179_AgentHelpClean verifies that both "gtms agent" (via Execute) and
// "gtms help agent" (via Help) print only the cheat sheet with no Usage/Flags
// noise.
func TestENH179_AgentHelpClean(t *testing.T) {
	cmd := newAgentCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Help()
	require.NoError(t, err, "agent help should not error")

	output := buf.String()
	require.NotEmpty(t, output, "agent help should produce output")

	assert.NotContains(t, output, "Usage:",
		"agent help should not contain 'Usage:' section")
	assert.NotContains(t, output, "Flags:",
		"agent help should not contain 'Flags:' section")
	assert.NotContains(t, output, "Global Flags:",
		"agent help should not contain 'Global Flags:' section")
}

// --- ENH-173 drift-test helpers ---

// extractFencedBlocks returns the concatenated content of all fenced code
// blocks (```sh, ```bash, or bare ```) in the given markdown text.
func extractFencedBlocks(markdown string) string {
	var blocks []string
	lines := strings.Split(markdown, "\n")
	var inFence bool
	var current []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !inFence {
			if strings.HasPrefix(trimmed, "```") {
				inFence = true
				current = nil
				continue
			}
		} else {
			if trimmed == "```" {
				blocks = append(blocks, strings.Join(current, "\n"))
				inFence = false
				continue
			}
			current = append(current, line)
		}
	}
	return strings.Join(blocks, "\n")
}

// extractGTMSCommands parses "gtms <subcommand>" tokens from fenced block
// text and returns the unique subcommand names.
func extractGTMSCommands(text string) []string {
	// Match "gtms <word>" where word is a subcommand name.
	pattern := regexp.MustCompile(`\bgtms\s+([a-z][-a-z]*)`)
	matches := pattern.FindAllStringSubmatch(text, -1)

	seen := make(map[string]bool)
	var commands []string
	for _, m := range matches {
		cmd := m[1]
		// Skip "gtms help" -- help is a cobra internal, not a user command.
		if cmd == "help" {
			continue
		}
		if !seen[cmd] {
			seen[cmd] = true
			commands = append(commands, cmd)
		}
	}
	return commands
}

// extractGTMSFlags parses "--flag" tokens from fenced block text and returns
// the unique flag names (without the -- prefix). Only matches flags preceded
// by whitespace or start-of-line to avoid false positives from file paths
// like "<tc-id>--manual.result.yaml".
func extractGTMSFlags(text string) []string {
	pattern := regexp.MustCompile(`(?:^|\s)--([a-z][-a-z]*)`)
	matches := pattern.FindAllStringSubmatch(text, -1)

	seen := make(map[string]bool)
	var flags []string
	for _, m := range matches {
		flag := m[1]
		// Skip --help (cobra built-in, not on all commands as a local flag).
		if flag == "help" {
			continue
		}
		if !seen[flag] {
			seen[flag] = true
			flags = append(flags, flag)
		}
	}
	return flags
}

// keepGTMSLines narrows fenced-block text to the gtms invocations within it,
// discarding the rest.
//
// Why this exists: extractGTMSFlags matches every "--word" token in the text it
// is given and asserts each one is a real gtms flag. That is correct for fenced
// blocks made entirely of gtms invocations, which is what the skills used to
// contain. The automate skill now also carries BATS authoring examples, whose
// fenced blocks legitimately contain non-gtms flags (--separate-stderr,
// --partial, and flags of whatever command is under test). Feeding those to the
// flag resolver would fail the drift guard on correct content.
//
// A gtms invocation may span several lines via trailing backslashes, and the
// continuation lines do not themselves name gtms:
//
//	gtms automate tc-12345678 \
//	  --adapter agent-automate
//
// Keeping only lines that literally contain "gtms" would drop that --adapter
// line, so a flag that no longer exists could be added there and sail past the
// drift guard. We therefore follow each gtms line through its continuations.
func keepGTMSLines(text string) string {
	var kept []string
	continuing := false
	for _, line := range strings.Split(text, "\n") {
		keep := continuing || strings.Contains(line, "gtms")
		if keep {
			kept = append(kept, line)
		}
		// A trailing backslash means the command continues on the next line.
		continuing = keep && strings.HasSuffix(strings.TrimRight(line, " \t"), `\`)
	}
	return strings.Join(kept, "\n")
}

// TestKeepGTMSLinesFollowsContinuations pins the two things keepGTMSLines has to
// get right at once: it must drop non-gtms example lines (so BATS authoring
// examples do not fail the drift guard), while still following a gtms invocation
// across backslash continuations (so a flag on a continuation line is still
// checked). Getting the first right by simply filtering on "gtms" silently loses
// the second, which is the hole this test exists to close.
func TestKeepGTMSLinesFollowsContinuations(t *testing.T) {
	input := strings.Join([]string{
		`gtms automate tc-12345678 \`,
		`  --adapter agent-automate`,
		`run --separate-stderr my-cli build --target release`,
		`gtms status --json`,
	}, "\n")

	got := keepGTMSLines(input)

	// The continuation line carries a gtms flag and must survive.
	if !strings.Contains(got, "--adapter") {
		t.Errorf("continuation line was dropped; a bogus flag there would evade the drift guard.\ngot:\n%s", got)
	}
	// The BATS example line carries non-gtms flags and must not.
	if strings.Contains(got, "--separate-stderr") || strings.Contains(got, "--target") {
		t.Errorf("non-gtms example line was kept; its flags would fail the drift guard.\ngot:\n%s", got)
	}
	if !strings.Contains(got, "--json") {
		t.Errorf("plain gtms line was dropped.\ngot:\n%s", got)
	}
}

// findCobraCommand looks up a command by name in the cobra tree. Searches
// one level deep (direct subcommands of root).
func findCobraCommand(root *cobra.Command, name string) *cobra.Command {
	for _, sub := range root.Commands() {
		if sub.Name() == name {
			return sub
		}
	}
	return nil
}

// flagExistsOnAnyCommand checks if a flag name is accepted by any command
// in the cobra tree (including persistent/inherited flags).
func flagExistsOnAnyCommand(root *cobra.Command, flagName string) bool {
	// Check root persistent flags first.
	if root.PersistentFlags().Lookup(flagName) != nil {
		return true
	}

	var walk func(cmd *cobra.Command) bool
	walk = func(cmd *cobra.Command) bool {
		if cmd.Flags().Lookup(flagName) != nil {
			return true
		}
		if cmd.PersistentFlags().Lookup(flagName) != nil {
			return true
		}
		for _, sub := range cmd.Commands() {
			if walk(sub) {
				return true
			}
		}
		return false
	}

	for _, sub := range root.Commands() {
		if walk(sub) {
			return true
		}
	}
	return false
}

// extractSection returns the text between a header line and the next blank line
// (or another section header). Used to isolate "Flags:" from "Global Flags:".
func extractSection(text, header string) string {
	lines := strings.Split(text, "\n")
	var capturing bool
	var section []string
	for _, line := range lines {
		if strings.HasPrefix(line, header) {
			capturing = true
			continue
		}
		if capturing {
			trimmed := strings.TrimSpace(line)
			// Stop at the next section header or empty line
			if trimmed == "" || (len(trimmed) > 0 && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && strings.HasSuffix(trimmed, ":")) {
				break
			}
			section = append(section, line)
		}
	}
	return strings.Join(section, "\n")
}

// --- ENH-180: Starter Agent Skills topic and drift tests ---

// TestENH180_SkillsTopicPrintsOverview verifies that the skills topic prints
// the overview with known anchors: all five skill names, the catalog path,
// and at least one discovery directory in the install instruction.
func TestENH180_SkillsTopicPrintsOverview(t *testing.T) {
	cmd := newSkillsCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	require.NoError(t, err, "skills command should succeed")

	output := buf.String()
	require.NotEmpty(t, output, "skills command should produce output")

	anchors := []string{
		"gtms-tests-create",
		"gtms-tests-automate",
		"gtms-tests-execute",
		"gtms-tests-prime",
		"gtms-tests-verify-intent",
		"gtms/skills/",
		".claude/skills/",
	}
	for _, anchor := range anchors {
		assert.Contains(t, output, anchor,
			"skills output must contain anchor %q", anchor)
	}
}

// TestENH180_SkillsTopicDiscoverable verifies the skills topic appears in the
// root command's subcommand list (and therefore in "gtms help" output).
func TestENH180_SkillsTopicDiscoverable(t *testing.T) {
	var found bool
	for _, sub := range rootCmd.Commands() {
		if sub.Name() == "skills" {
			found = true
			assert.NotEmpty(t, sub.Short,
				"skills command must have a Short description for discoverability")
			break
		}
	}
	assert.True(t, found, "skills command must be registered on rootCmd")
}

// TestENH180_SkillsBucketedAsHelpTopic verifies that the rendered root help
// lists "skills" under "Additional help topics:" and not under "Available
// Commands:".
func TestENH180_SkillsBucketedAsHelpTopic(t *testing.T) {
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	err := rootCmd.Help()
	require.NoError(t, err, "root help should not error")

	output := buf.String()

	require.Contains(t, output, "Additional help topics:",
		"root help must have 'Additional help topics:' section")

	parts := strings.SplitN(output, "Additional help topics:", 2)
	require.Len(t, parts, 2, "should split into exactly two parts")

	commandsSection := parts[0]
	topicsSection := parts[1]

	commandsPart := ""
	if idx := strings.Index(commandsSection, "Available Commands:"); idx >= 0 {
		commandsPart = commandsSection[idx:]
	}

	assert.NotRegexp(t, `(?m)^\s+skills\s`, commandsPart,
		"'skills' should NOT appear as a command row under 'Available Commands:'")
	assert.Contains(t, topicsSection, "skills",
		"'skills' should appear under 'Additional help topics:'")
}

// TestENH180_SkillsHelpClean verifies that "gtms skills" and "gtms help skills"
// print only the overview content with no Usage/Flags noise.
func TestENH180_SkillsHelpClean(t *testing.T) {
	cmd := newSkillsCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Help()
	require.NoError(t, err, "skills help should not error")

	output := buf.String()
	require.NotEmpty(t, output, "skills help should produce output")

	assert.NotContains(t, output, "Usage:",
		"skills help should not contain 'Usage:' section")
	assert.NotContains(t, output, "Flags:",
		"skills help should not contain 'Flags:' section")
	assert.NotContains(t, output, "Global Flags:",
		"skills help should not contain 'Global Flags:' section")
}

// TestENH180_SkillFencedBlocksResolveAgainstCobraTree extracts fenced command
// blocks from all embedded skill files and asserts every gtms command/flag
// token resolves against the assembled cobra tree. This is the drift guard
// for the starter skills -- analogous to TestENH173 for the agent cheat sheet.
func TestENH180_SkillFencedBlocksResolveAgainstCobraTree(t *testing.T) {
	skillFiles := []struct {
		name    string
		content string
	}{
		{"gtms-tests-create", readEmbeddedSkill(t, "skills/gtms-tests-create/SKILL.md")},
		{"gtms-tests-automate", readEmbeddedSkill(t, "skills/gtms-tests-automate/SKILL.md")},
		{"gtms-tests-execute", readEmbeddedSkill(t, "skills/gtms-tests-execute/SKILL.md")},
		{"gtms-tests-prime", readEmbeddedSkill(t, "skills/gtms-tests-prime/SKILL.md")},
		{"gtms-tests-verify-intent", readEmbeddedSkill(t, "skills/gtms-tests-verify-intent/SKILL.md")},
	}

	for _, sf := range skillFiles {
		fencedContent := keepGTMSLines(extractFencedBlocks(sf.content))
		if fencedContent == "" {
			continue
		}

		commands := extractGTMSCommands(fencedContent)
		for _, cmdName := range commands {
			t.Run(sf.name+"/cmd/"+cmdName, func(t *testing.T) {
				found := findCobraCommand(rootCmd, cmdName)
				assert.NotNil(t, found,
					"skill %s references 'gtms %s' in a fenced block but no such command exists in the cobra tree",
					sf.name, cmdName)
			})
		}

		flags := extractGTMSFlags(fencedContent)
		for _, flagName := range flags {
			t.Run(sf.name+"/flag/"+flagName, func(t *testing.T) {
				found := flagExistsOnAnyCommand(rootCmd, flagName)
				assert.True(t, found,
					"skill %s references '--%s' in a fenced block but no command accepts that flag",
					sf.name, flagName)
			})
		}
	}
}

// TestENH180_SkillsOverviewFencedBlocksResolve applies the same drift test
// to the skills overview help topic content.
func TestENH180_SkillsOverviewFencedBlocksResolve(t *testing.T) {
	fencedContent := extractFencedBlocks(help.SkillsOverview)
	if fencedContent == "" {
		// Overview has no fenced blocks -- that is acceptable.
		return
	}

	commands := extractGTMSCommands(fencedContent)
	for _, cmdName := range commands {
		t.Run("cmd/"+cmdName, func(t *testing.T) {
			found := findCobraCommand(rootCmd, cmdName)
			assert.NotNil(t, found,
				"skills overview references 'gtms %s' in a fenced block but no such command exists", cmdName)
		})
	}
}

// readEmbeddedSkill reads a skill file from the scaffold package's embedded FS.
func readEmbeddedSkill(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("..", "scaffold", path))
	require.NoError(t, err, "reading embedded skill %s from disk", path)
	return string(content)
}

// --- ENH-184: Getting Started help topic tests ---

// TestENH184_GettingStartedTopicPrintsQuickStart verifies that the
// getting-started topic prints the quick start with known anchors: the
// title line, the four numbered steps, the brownfield section, and the
// FURTHER READING footer.
func TestENH184_GettingStartedTopicPrintsQuickStart(t *testing.T) {
	cmd := newGettingStartedCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	require.NoError(t, err, "getting-started command should succeed")

	output := buf.String()
	require.NotEmpty(t, output, "getting-started command should produce output")

	anchors := []string{
		"Getting Started with GTMS",
		"STEP 1 -- READ THE OPERATING REFERENCE",
		"STEP 2 -- INITIALISE",
		"STEP 3 -- INSTALL THE STARTER SKILLS",
		"STEP 4 -- RUN ONE TEST CASE THROUGH THE PIPELINE",
		"ALREADY HAVE TESTS? (BROWNFIELD)",
		"FURTHER READING",
	}
	for _, anchor := range anchors {
		assert.Contains(t, output, anchor,
			"getting-started output must contain anchor %q", anchor)
	}

	// Verify section order: each anchor appears after the previous one.
	for i := 1; i < len(anchors); i++ {
		prev := strings.Index(output, anchors[i-1])
		curr := strings.Index(output, anchors[i])
		assert.Greater(t, curr, prev,
			"anchor %q must appear after %q", anchors[i], anchors[i-1])
	}
}

// TestENH184_GettingStartedTopicDiscoverable verifies the getting-started
// topic appears in the root command's subcommand list (and therefore in
// "gtms help" output).
func TestENH184_GettingStartedTopicDiscoverable(t *testing.T) {
	var found bool
	for _, sub := range rootCmd.Commands() {
		if sub.Name() == "getting-started" {
			found = true
			assert.NotEmpty(t, sub.Short,
				"getting-started command must have a Short description for discoverability")
			break
		}
	}
	assert.True(t, found, "getting-started command must be registered on rootCmd")
}

// TestENH184_GettingStartedBucketedAsHelpTopic verifies that the rendered
// root help lists "getting-started" under "Additional help topics:" and not
// under "Available Commands:".
func TestENH184_GettingStartedBucketedAsHelpTopic(t *testing.T) {
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	err := rootCmd.Help()
	require.NoError(t, err, "root help should not error")

	output := buf.String()

	require.Contains(t, output, "Additional help topics:",
		"root help must have 'Additional help topics:' section")

	parts := strings.SplitN(output, "Additional help topics:", 2)
	require.Len(t, parts, 2, "should split into exactly two parts")

	commandsSection := parts[0]
	topicsSection := parts[1]

	commandsPart := ""
	if idx := strings.Index(commandsSection, "Available Commands:"); idx >= 0 {
		commandsPart = commandsSection[idx:]
	}

	assert.NotRegexp(t, `(?m)^\s+getting-started\s`, commandsPart,
		"'getting-started' should NOT appear as a command row under 'Available Commands:'")
	assert.Contains(t, topicsSection, "getting-started",
		"'getting-started' should appear under 'Additional help topics:'")
}

// TestENH184_GettingStartedHelpClean verifies that "gtms getting-started"
// and "gtms help getting-started" print only the overview content with no
// Usage/Flags noise.
func TestENH184_GettingStartedHelpClean(t *testing.T) {
	cmd := newGettingStartedCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Help()
	require.NoError(t, err, "getting-started help should not error")

	output := buf.String()
	require.NotEmpty(t, output, "getting-started help should produce output")

	assert.NotContains(t, output, "Usage:",
		"getting-started help should not contain 'Usage:' section")
	assert.NotContains(t, output, "Flags:",
		"getting-started help should not contain 'Flags:' section")
	assert.NotContains(t, output, "Global Flags:",
		"getting-started help should not contain 'Global Flags:' section")
}

// TestENH184_GettingStartedFencedBlocksResolve applies the drift test to
// the getting-started topic content. The pinned draft uses indented code
// blocks (4 spaces) rather than triple-backtick fences, so this test
// extracts gtms command/flag tokens from indented lines in addition to
// any fenced blocks.
func TestENH184_GettingStartedFencedBlocksResolve(t *testing.T) {
	content := help.GettingStarted

	// Extract from fenced blocks (standard path).
	fencedContent := extractFencedBlocks(content)

	// Also extract from indented code blocks (4+ leading spaces) since
	// the getting-started content uses that format for command examples.
	indentedContent := extractIndentedBlocks(content)

	combined := fencedContent + "\n" + indentedContent
	if strings.TrimSpace(combined) == "" {
		// No code blocks at all -- acceptable but unexpected for this topic.
		return
	}

	commands := extractGTMSCommands(combined)
	for _, cmdName := range commands {
		t.Run("cmd/"+cmdName, func(t *testing.T) {
			found := findCobraCommand(rootCmd, cmdName)
			assert.NotNil(t, found,
				"getting-started references 'gtms %s' in a code block but no such command exists", cmdName)
		})
	}

	flags := extractGTMSFlags(combined)
	for _, flagName := range flags {
		t.Run("flag/"+flagName, func(t *testing.T) {
			found := flagExistsOnAnyCommand(rootCmd, flagName)
			assert.True(t, found,
				"getting-started references '--%s' in a code block but no command accepts that flag", flagName)
		})
	}
}

// TestENH184_NoColdStartPointer verifies the getting-started topic does
// not contain a pointer to the cold-start guide (ENH-174 unshipped).
func TestENH184_NoColdStartPointer(t *testing.T) {
	content := strings.ToLower(help.GettingStarted)
	assert.NotContains(t, content, "cold-start",
		"getting-started content must not reference cold-start guide (ENH-174 unshipped)")
}

// extractIndentedBlocks returns the concatenated content of all lines
// indented by 4+ spaces in the given text (code blocks in plain-text
// help topics that do not use triple-backtick fences).
func extractIndentedBlocks(text string) string {
	var blocks []string
	for _, line := range strings.Split(text, "\n") {
		if len(line) >= 4 && line[:4] == "    " {
			blocks = append(blocks, line)
		}
	}
	return strings.Join(blocks, "\n")
}

// --- ENH-183: Agent-instructions snippet drift guard ---

// TestENH183_SnippetCommandsExistInCobraTree verifies every gtms command
// referenced in the agent-instructions snippet resolves against the cobra
// tree. This is the drift guard for the snippet -- analogous to TestENH173
// for the agent cheat sheet and TestENH180 for the starter skills.
//
// The snippet uses inline backtick code (not fenced blocks), so we search
// the raw content in addition to any fenced/indented blocks.
func TestENH183_SnippetCommandsExistInCobraTree(t *testing.T) {
	content := scaffold.AgentsSnippetMD

	// Combine fenced, indented, and raw content to catch inline backtick refs.
	fencedContent := extractFencedBlocks(content)
	indentedContent := extractIndentedBlocks(content)
	combined := fencedContent + "\n" + indentedContent + "\n" + content

	commands := extractGTMSCommands(combined)
	require.NotEmpty(t, commands,
		"AgentsSnippetMD must contain gtms command references")

	for _, cmdName := range commands {
		t.Run("cmd/"+cmdName, func(t *testing.T) {
			found := findCobraCommand(rootCmd, cmdName)
			assert.NotNil(t, found,
				"snippet references 'gtms %s' but no such command exists in the cobra tree", cmdName)
		})
	}
}

// --- ENH-185: Agent-instructions nudge drift guard ---

// TestENH185_NudgeTextCommandsResolveAgainstCobraTree verifies that every
// gtms command referenced in the nudge text constants resolves against the
// assembled cobra tree. This is the drift guard for the nudge wording --
// analogous to TestENH173 for the agent cheat sheet.
func TestENH185_NudgeTextCommandsResolveAgainstCobraTree(t *testing.T) {
	nudgeTexts := []struct {
		name    string
		content string
	}{
		{"NudgeWithSnippet", onboarding.NudgeWithSnippet},
		{"NudgeWithoutSnippet", onboarding.NudgeWithoutSnippet},
	}

	for _, nt := range nudgeTexts {
		commands := extractGTMSCommands(nt.content)
		for _, cmdName := range commands {
			t.Run(nt.name+"/cmd/"+cmdName, func(t *testing.T) {
				found := findCobraCommand(rootCmd, cmdName)
				assert.NotNil(t, found,
					"nudge text %s references 'gtms %s' but no such command exists in the cobra tree",
					nt.name, cmdName)
			})
		}
	}
}

// TestENH185_NudgeTextASCIIOnly verifies that both nudge text constants
// contain only printable ASCII characters (0x20-0x7E) plus newlines.
// This guards the project typography rule (no em-dashes, smart quotes,
// or other non-ASCII).
func TestENH185_NudgeTextASCIIOnly(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{"NudgeWithSnippet", onboarding.NudgeWithSnippet},
		{"NudgeWithoutSnippet", onboarding.NudgeWithoutSnippet},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for i, b := range []byte(tc.text) {
				if b == '\n' {
					continue
				}
				assert.True(t, b >= 0x20 && b <= 0x7E,
					"byte 0x%02x (%q) at position %d is not printable ASCII",
					b, string(b), i)
			}
		})
	}
}
