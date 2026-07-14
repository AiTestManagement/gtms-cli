package onboarding

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Nudge behaviour tests ---

func TestCheckAgentInstructions_NudgesWhenFileExistsNoMention(t *testing.T) {
	root := t.TempDir()
	// Create CLAUDE.md without any gtms mention.
	require.NoError(t, os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("# My Project\nSome instructions here.\n"), 0644))
	// Create the snippet file so the nudge references it.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "gtms", "AGENTS-SNIPPET.md"), []byte("snippet"), 0644))

	var buf bytes.Buffer
	CheckAgentInstructions(root, &buf)

	assert.Contains(t, buf.String(), "AGENTS-SNIPPET.md",
		"nudge should reference the snippet file")
	assert.Contains(t, buf.String(), "Tip:",
		"nudge should start with Tip:")
}

func TestCheckAgentInstructions_SilentWhenFileMentionsGTMS(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("# My Project\nUse gtms create to generate test cases.\n"), 0644))

	var buf bytes.Buffer
	CheckAgentInstructions(root, &buf)

	assert.Empty(t, buf.String(), "should print nothing when file mentions gtms")
}

func TestCheckAgentInstructions_CaseInsensitiveMatch(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("# My Project\nGTMS is a test management tool.\n"), 0644))

	var buf bytes.Buffer
	CheckAgentInstructions(root, &buf)

	assert.Empty(t, buf.String(), "should print nothing -- GTMS mention is case-insensitive")
}

func TestCheckAgentInstructions_SilentWhenNoInstructionFile(t *testing.T) {
	root := t.TempDir()
	// No recognised instruction files at all.

	var buf bytes.Buffer
	CheckAgentInstructions(root, &buf)

	assert.Empty(t, buf.String(), "should print nothing when no instruction file exists")

	// Verify no files were created.
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	assert.Empty(t, entries, "should not create any files")
}

func TestCheckAgentInstructions_DegradeWhenSnippetAbsent(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("# Project\nNo mention here.\n"), 0644))
	// Deliberately do NOT create gtms/AGENTS-SNIPPET.md.

	var buf bytes.Buffer
	CheckAgentInstructions(root, &buf)

	out := buf.String()
	assert.Contains(t, out, "gtms agent",
		"nudge should reference gtms agent when snippet is absent")
	assert.NotContains(t, out, "AGENTS-SNIPPET.md",
		"nudge should NOT reference snippet file when it does not exist")
}

func TestCheckAgentInstructions_ShortCircuitsWhenAnyFileMentions(t *testing.T) {
	root := t.TempDir()
	// CLAUDE.md has no mention.
	require.NoError(t, os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("# Project\nNo mention here.\n"), 0644))
	// AGENTS.md mentions gtms.
	require.NoError(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("# Agents\nUse gtms to manage tests.\n"), 0644))

	var buf bytes.Buffer
	CheckAgentInstructions(root, &buf)

	assert.Empty(t, buf.String(), "should print nothing when any recognised file mentions gtms")
}

func TestCheckAgentInstructions_ReadOnly(t *testing.T) {
	root := t.TempDir()
	claudePath := filepath.Join(root, "CLAUDE.md")
	content := []byte("# Project\nNo mention of the tool.\n")
	require.NoError(t, os.WriteFile(claudePath, content, 0644))

	// Capture directory listing before.
	entriesBefore, err := os.ReadDir(root)
	require.NoError(t, err)
	namesBefore := dirEntryNames(entriesBefore)

	var buf bytes.Buffer
	CheckAgentInstructions(root, &buf)

	// Nudge should fire (proves the read path ran).
	assert.NotEmpty(t, buf.String(), "nudge should fire to prove check ran")

	// Directory listing after should be unchanged.
	entriesAfter, err := os.ReadDir(root)
	require.NoError(t, err)
	namesAfter := dirEntryNames(entriesAfter)
	assert.Equal(t, namesBefore, namesAfter, "no files should be created or deleted")

	// File content should be unchanged.
	afterContent, err := os.ReadFile(claudePath)
	require.NoError(t, err)
	assert.Equal(t, content, afterContent, "instruction file should not be modified")
}

func TestCheckAgentInstructions_CursorRulesDir_MentionsGTMS(t *testing.T) {
	root := t.TempDir()
	cursorRulesDir := filepath.Join(root, ".cursor", "rules")
	require.NoError(t, os.MkdirAll(cursorRulesDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(cursorRulesDir, "main.md"), []byte("Use gtms for test management.\n"), 0644))

	var buf bytes.Buffer
	CheckAgentInstructions(root, &buf)

	assert.Empty(t, buf.String(), "should print nothing when .cursor/rules/ contains gtms mention")
}

func TestCheckAgentInstructions_CursorRulesDir_NoMention(t *testing.T) {
	root := t.TempDir()
	cursorRulesDir := filepath.Join(root, ".cursor", "rules")
	require.NoError(t, os.MkdirAll(cursorRulesDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(cursorRulesDir, "main.md"), []byte("Some cursor rules.\n"), 0644))
	// Create snippet so nudge references it.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "gtms", "AGENTS-SNIPPET.md"), []byte("snippet"), 0644))

	var buf bytes.Buffer
	CheckAgentInstructions(root, &buf)

	assert.Contains(t, buf.String(), "Tip:",
		"nudge should fire when .cursor/rules/ exists but has no gtms mention")
}

func TestCheckAgentInstructions_CopilotInstructions_MentionsGTMS(t *testing.T) {
	root := t.TempDir()
	ghDir := filepath.Join(root, ".github")
	require.NoError(t, os.MkdirAll(ghDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(ghDir, "copilot-instructions.md"), []byte("Run gtms create first.\n"), 0644))

	var buf bytes.Buffer
	CheckAgentInstructions(root, &buf)

	assert.Empty(t, buf.String(), "should print nothing when copilot-instructions.md mentions gtms")
}

func TestCheckAgentInstructions_InvalidProjectRoot(t *testing.T) {
	// Non-existent project root should not panic or print anything.
	var buf bytes.Buffer
	CheckAgentInstructions(filepath.Join(t.TempDir(), "nonexistent"), &buf)

	assert.Empty(t, buf.String(), "should print nothing for invalid project root")
}

// --- Nudge text validation ---

func TestNudgeText_ASCIIOnly(t *testing.T) {
	for _, text := range []string{NudgeWithSnippet, NudgeWithoutSnippet} {
		for i, b := range []byte(text) {
			if b == '\n' {
				continue
			}
			assert.True(t, b >= 0x20 && b <= 0x7E,
				"byte %d (%q) at position %d is not printable ASCII", b, string(b), i)
		}
	}
}

func TestNudgeText_ContainsExpectedReferences(t *testing.T) {
	assert.Contains(t, NudgeWithSnippet, "AGENTS-SNIPPET.md",
		"snippet nudge must reference AGENTS-SNIPPET.md")
	assert.Contains(t, NudgeWithoutSnippet, "gtms agent",
		"fallback nudge must reference gtms agent")
	assert.NotContains(t, NudgeWithoutSnippet, "AGENTS-SNIPPET.md",
		"fallback nudge must not reference AGENTS-SNIPPET.md")
}

func TestNudgeText_MixedCaseMention(t *testing.T) {
	// Verify detection works with various cases of "gtms" in the file.
	cases := []string{"gtms", "GTMS", "Gtms", "gTmS"}
	for _, mention := range cases {
		t.Run(mention, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.WriteFile(
				filepath.Join(root, "CLAUDE.md"),
				[]byte("# Instructions\nUse "+mention+" for tests.\n"),
				0644))

			var buf bytes.Buffer
			CheckAgentInstructions(root, &buf)

			assert.Empty(t, buf.String(),
				"should be silent when file contains %q", mention)
		})
	}
}

func TestNudgeText_AGENTSmdFile(t *testing.T) {
	root := t.TempDir()
	// Only AGENTS.md exists, no gtms mention.
	require.NoError(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("# Agents\nSome instructions.\n"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "gtms", "AGENTS-SNIPPET.md"), []byte("snippet"), 0644))

	var buf bytes.Buffer
	CheckAgentInstructions(root, &buf)

	assert.Contains(t, buf.String(), "Tip:",
		"nudge should fire when AGENTS.md exists without gtms mention")
}

// --- Recognised instruction files list ---

func TestRecognisedInstructionFiles_ExpectedSet(t *testing.T) {
	// Guard against accidental additions/removals.
	expected := []string{
		"CLAUDE.md",
		"AGENTS.md",
		filepath.Join(".github", "copilot-instructions.md"),
		filepath.Join(".cursor", "rules"),
	}
	assert.Equal(t, expected, recognisedInstructionFiles,
		"recognised instruction files must match the frozen spec set")
}

// --- helpers ---

func dirEntryNames(entries []os.DirEntry) []string {
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	return names
}

