package scaffold

// Source-shape tests verify compile-time / code-structure invariants that were
// previously asserted by BATS acceptance tests grepping Go source files.
// These are architectural guardrails, not behaviour tests.
// See ENH-088 for the full audit and migration rationale.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Audit #13: promptCreateStandard references {tc_ids} and ID contract ---

func TestSourceShape_PromptCreateStandardReferencesTcIds(t *testing.T) {
	src, err := os.ReadFile("templates.go")
	require.NoError(t, err)
	content := string(src)

	assert.Contains(t, content, "promptCreateStandard",
		"templates.go must define promptCreateStandard constant")
	assert.Contains(t, content, "{tc_ids}",
		"promptCreateStandard must reference {tc_ids} variable for pre-generated IDs")
	assert.Contains(t, content, "Do NOT invent your own IDs",
		"promptCreateStandard must instruct adapters not to invent their own IDs")
}

// --- Audit #20: no stale 7-char refs, 8-char present, all tc-IDs 8 hex ---

func TestSourceShape_ScaffoldTemplatesNo7CharRefs(t *testing.T) {
	src, err := os.ReadFile("templates.go")
	require.NoError(t, err)
	content := string(src)

	stalePattern := regexp.MustCompile(`(?i)7-char|7 char|7hex`)
	assert.False(t, stalePattern.MatchString(content),
		"templates.go must not contain stale 7-char/7hex references")
}

func TestSourceShape_ScaffoldTemplatesHas8CharRef(t *testing.T) {
	src, err := os.ReadFile("templates.go")
	require.NoError(t, err)
	content := string(src)

	assert.True(t, strings.Contains(content, "8-char"),
		"templates.go must reference 8-char hex format")
}

func TestSourceShape_ScaffoldTemplatesAllTcIDsHave8HexChars(t *testing.T) {
	src, err := os.ReadFile("templates.go")
	require.NoError(t, err)
	content := string(src)

	// Find all tc-<hex> patterns and verify each has exactly 8 hex chars
	tcIDPattern := regexp.MustCompile(`tc-[0-9a-fA-F]+`)
	matches := tcIDPattern.FindAllString(content, -1)
	require.NotEmpty(t, matches, "templates.go should contain at least one tc-<hex> ID")

	exactPattern := regexp.MustCompile(`^tc-[0-9a-fA-F]{8}$`)
	for _, id := range matches {
		assert.True(t, exactPattern.MatchString(id),
			"tc-ID %q must have exactly 8 hex characters", id)
	}
}

// --- BUG-100: starterGuideContent and the checked-in dogfood mirror must match ---

// TestSourceShape_StarterGuideMatchesDogfoodMirror guards the embedded-template
// parity rule: the Go-source constant that `gtms init` stamps into fresh
// projects, and the checked-in copy at gtms/cases/guides/test-case-template.md
// that this repo dogfoods, must stay byte-for-byte identical. BUG-099 inverted
// the skeleton frontmatter contract; BUG-100 swept the documented shape across
// both files. Without this guard a future edit to one without the other
// silently re-introduces the drift.
func TestSourceShape_StarterGuideMatchesDogfoodMirror(t *testing.T) {
	mirror, err := os.ReadFile("../../gtms/cases/guides/test-case-template.md")
	require.NoError(t, err)
	// Normalise: the Go raw string literal uses LF; the on-disk mirror may be
	// CRLF on Windows checkouts. Strip trailing newlines/CR from both sides so
	// the comparison is invariant to platform line-ending style.
	want := strings.TrimRight(starterGuideContent, "\n")
	got := strings.TrimRight(strings.ReplaceAll(string(mirror), "\r\n", "\n"), "\n")
	assert.Equal(t, want, got,
		"starterGuideContent and gtms/cases/guides/test-case-template.md have drifted; "+
			"edit both in lockstep")
}

// TestSourceShape_StarterGuideDocumentsLeanContract verifies that the documented
// skeleton frontmatter matches the post-BUG-099 lean contract that
// BuiltinCreate emits, and that the retired keys (status:, name:) do not
// appear as part of the documented contract.
func TestSourceShape_StarterGuideDocumentsLeanContract(t *testing.T) {
	// Required keys the documented skeleton must call out.
	for _, key := range []string{"test_case_id:", "title:", "priority: Medium", "type: Functional", "created:"} {
		assert.Contains(t, starterGuideContent, key,
			"starter guide must document skeleton key %q (post-BUG-099 contract)", key)
	}

	// Retired keys must not appear in the documented skeleton example.
	// We check for the YAML-block forms (`name: "..."`, `status: draft`) rather
	// than the bare token, because "name" and "status" both legitimately appear
	// elsewhere in the guide prose.
	retiredPatterns := []string{
		`name: "<slug`,
		"status: draft",
	}
	for _, pat := range retiredPatterns {
		assert.NotContains(t, starterGuideContent, pat,
			"starter guide must not document retired skeleton key pattern %q (BUG-099)", pat)
	}
}

// --- ENH-128: templates.go must not contain compiled-in adapter YAML ---

func TestSourceShape_TemplatesGoNoCompiledAdapterYAML(t *testing.T) {
	src, err := os.ReadFile("templates.go")
	require.NoError(t, err)

	// Strip doc comments to allow framework names in documentation
	var codeLines []string
	for _, line := range strings.Split(string(src), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		codeLines = append(codeLines, line)
	}
	code := strings.Join(codeLines, "\n")

	// ENH-128 / BUG-111: adapter YAML config that should NOT be compiled into
	// Go string literals. Adapter script content (e.g. playwright-runner.sh
	// calling "npx playwright test") is allowed; only YAML config-style entries
	// like "framework: playwright" are forbidden because those belong in preset
	// YAML files, not compiled Go.
	forbidden := []string{
		"framework: playwright",
		"output-dir: gtms/scripts/playwright/",
		"--reporter=junit --output=results/junit/",
	}

	for _, lit := range forbidden {
		assert.False(t, strings.Contains(code, lit),
			"templates.go non-comment code must not contain compiled adapter YAML %q", lit)
	}

	// configMinimal, configClaude, configGitHub functions should not exist
	removedFuncs := []string{
		"func configMinimal(",
		"func configClaude(",
		"func configGitHub(",
	}
	for _, fn := range removedFuncs {
		assert.False(t, strings.Contains(code, fn),
			"templates.go must not contain removed function %q (presets are now embedded .yaml files)", fn)
	}
}

// --- BUG-111: scaffold.go must not write BATS assets unconditionally ---

func TestSourceShape_ScaffoldGoNoBatsAssetOutsidePreset(t *testing.T) {
	src, err := os.ReadFile("scaffold.go")
	require.NoError(t, err)
	content := string(src)

	// The shared Init() path must not contain unconditional bats-runner.sh writes.
	// BATS assets are installed only via installPresetAssets for the bats preset.
	// Guard: if someone re-adds unconditional bats-runner or bats-tap writes to
	// scaffold.go outside the installPresetAssets function, this test fails.
	assert.NotContains(t, content, `"bats-runner.sh"`,
		"scaffold.go must not reference bats-runner.sh as a literal string (use PresetAssets registry)")
	assert.NotContains(t, content, `"bats-tap.sh"`,
		"scaffold.go must not reference bats-tap.sh as a literal string (use PresetAssets registry)")
}

// --- BUG-103: .claude/commands/tests-execute.md advertises the skipped bucket ---

// testsExecuteCommandPath resolves the path to the /tests-execute slash-command
// definition relative to this test's package directory. Go tests run with the
// package directory as the working directory, so walking back two levels lands
// at the project root.
func testsExecuteCommandPath() string {
	return filepath.Join("..", "..", ".claude", "commands", "tests-execute.md")
}

// TestTestsExecuteCommand_AdvertisesSkippedBucket guards that the
// /tests-execute slash-command definition continues to instruct callers to
// surface the "skipped" count in the report template. Previously enforced by
// tc-83c92aba and tc-b347faf3 BATS, which pinned section-heading anchors that
// CON-023 invalidated. The invariant is the literal `{S} skipped` token; the
// anchor shape is incidental and shifts as the slash command evolves.
func TestTestsExecuteCommand_AdvertisesSkippedBucket(t *testing.T) {
	content, err := os.ReadFile(testsExecuteCommandPath())
	require.NoError(t, err,
		"reading .claude/commands/tests-execute.md (path is relative to internal/scaffold/)")

	assert.Contains(t, string(content), "{S} skipped",
		"tests-execute.md must advertise the {S} skipped token in its report template")
}

// TestTestsExecuteCommand_PassedFailedSkippedTriple guards that the report
// template surfaces the three-way passed/failed/skipped count in a single
// line, in order. The pattern is intentionally loose: it tolerates arbitrary
// whitespace and punctuation between the three buckets so it survives future
// punctuation tweaks to the template.
func TestTestsExecuteCommand_PassedFailedSkippedTriple(t *testing.T) {
	content, err := os.ReadFile(testsExecuteCommandPath())
	require.NoError(t, err)

	tripleLine := regexp.MustCompile(`(?m)^.*passed.*failed.*skipped.*$`)
	assert.True(t, tripleLine.Match(content),
		"tests-execute.md must include a line listing passed/failed/skipped together")
}
