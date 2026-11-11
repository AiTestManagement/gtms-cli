package scaffold

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- REV-249 BUG-207 round-3 independent-review findings ---------------------

// CODEX-001: the demo cleanup commands shell-quote an unsafe renamed parent while
// keeping today's exact unquoted form for a demonstrably safe name.
func TestBUG207_CODEX001_DemoCleanupShellQuoting(t *testing.T) {
	// Safe name: unchanged, exactly as tc-80910ae1 pins it.
	safe := demoBridgeGuideFor("testing")
	assert.Contains(t, safe, "rm -rf testing/test/cases/_demo/")
	assert.Contains(t, safe, "rm -rf testing/automation/specs/demo-sh/")
	assert.Contains(t, safe, "rm -rf testing/automation/specs/demo-cmd/")
	assert.Contains(t, safe, "rm -rf _demo/", "the project-root command stays canonical")

	// Name with a space: single-quoted as one argument so it cannot split.
	spaced := demoBridgeGuideFor("qa suite")
	assert.Contains(t, spaced, "rm -rf 'qa suite/test/cases/_demo/'")
	assert.NotContains(t, spaced, "rm -rf qa suite/test/cases/_demo/", "an unquoted space would split the path")

	// shellQuotePOSIX directly: safe stays bare; shell syntax is neutralised.
	assert.Equal(t, "testing/test/cases/_demo/", shellQuotePOSIX("testing/test/cases/_demo/"))
	assert.Equal(t, "'qa suite'", shellQuotePOSIX("qa suite"))
	assert.Equal(t, `'a'\''b'`, shellQuotePOSIX("a'b"), "embedded single quote is POSIX-escaped")
	for _, bad := range []string{"a;rm -rf /", "a$(id)", "a`id`", "a|b", "a&b", "a>b", "a*b"} {
		q := shellQuotePOSIX(bad)
		assert.True(t, strings.HasPrefix(q, "'") && strings.HasSuffix(q, "'"),
			"%q must be single-quoted, got %q", bad, q)
	}
}

// CODEX-002: existing-project demo seeding fails closed on invalid discovery.
// DemoSeed resolves the active parent strictly before any mutation and returns the
// discovery error instead of defaulting to gtms and seeding _demo.
func TestBUG207_CODEX002_DemoSeedRefusesInvalidDiscovery(t *testing.T) {
	skipIfShort(t)
	dir := t.TempDir()
	const cfgBytes = "project:\n  name: x\n  repo: org/x\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gtms.config"), []byte(cfgBytes), 0o644))
	// Two sentinel-bearing folders: discovery is ambiguous (invalid layout).
	for _, p := range []string{"testing", "other"} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, p), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, p, ".gtms-root"), nil, 0o644))
	}

	res, err := DemoSeed(dir, false)
	require.Error(t, err, "DemoSeed must refuse an ambiguous layout instead of defaulting to gtms")
	assert.Nil(t, res)

	// No mutation: no _demo tree, no gtms/ tree, config unchanged.
	_, statErr := os.Stat(filepath.Join(dir, "_demo"))
	assert.True(t, os.IsNotExist(statErr), "_demo must not be created on refusal")
	_, statErr = os.Stat(filepath.Join(dir, "gtms"))
	assert.True(t, os.IsNotExist(statErr), "no canonical gtms/ tree may appear")
	after, rerr := os.ReadFile(filepath.Join(dir, "gtms.config"))
	require.NoError(t, rerr)
	assert.Equal(t, cfgBytes, string(after), "gtms.config must be byte-for-byte unchanged")
}

// CODEX-003: a top-level symlink to a GTMS-shaped tree is refused before mutation
// even when gtms.config is absent (the fresh-project row), not mis-classified as a
// fresh project. POSIX symlink variant; the Windows junction variant is Pester.
func TestBUG207_CODEX003_LinkedTopLevelRefusedWhenNoConfig(t *testing.T) {
	skipIfShort(t)
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlink variant; Windows junction is covered by Pester (Owner Decision 12)")
	}
	dir := t.TempDir()
	// External GTMS-shaped tree with a real sentinel.
	ext := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ext, "tasks"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(ext, "test", "cases"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ext, ".gtms-root"), nil, 0o644))
	// Top-level symlink into it; NO gtms.config in the project (fresh-project row).
	require.NoError(t, os.Symlink(ext, filepath.Join(dir, "testing")))

	parent, err := resolveActiveParent(Options{ProjectRoot: dir, Preset: PresetManual})
	require.Error(t, err, "a linked GTMS parent must be refused, not treated as a fresh project")
	assert.Empty(t, parent)
	assert.Contains(t, err.Error(), "testing")
	// The external tree is untouched.
	_, statErr := os.Stat(filepath.Join(ext, ".gtms-root"))
	assert.NoError(t, statErr, "the external sentinel must still exist")
}

// CODEX-004 / CLAUDE-002: the renamed parent is escaped as a literal gitignore
// pattern, so comment/negation/glob/backslash names still ignore the execution
// paths. The same escaped set backs the BUG-208 unsafe-leaf warning.
func TestBUG207_CODEX004_CLAUDE002_GitignorePatternEscaping(t *testing.T) {
	// Safe name: unchanged (AC6/AC11 default behaviour and tc-481f4021 "testing").
	assert.Equal(t,
		[]string{".gtms/", "testing/execution/attachments/", "testing/execution/logs/", "/gtms.config.bak*"},
		gitignoreEntriesFor("testing"))

	cases := map[string]string{
		"#qa":  `\#qa/execution/attachments/`,
		"!qa":  `\!qa/execution/attachments/`,
		"[qa]": `\[qa]/execution/attachments/`,
		"a*b":  `a\*b/execution/attachments/`,
		"a?b":  `a\?b/execution/attachments/`,
		`a\b`:  `a\\b/execution/attachments/`,
	}
	for parent, wantAttach := range cases {
		entries := gitignoreEntriesFor(parent)
		require.Len(t, entries, 4)
		assert.Equal(t, wantAttach, entries[1], "attachments rule for parent %q", parent)
		// The unsafe-leaf warning must list the SAME escaped entry set.
		warn := gitignoreSkipWarning(gitignoreState{kind: kindDirectory, mode: os.ModeDir}, parent)
		assert.Contains(t, warn, wantAttach, "BUG-208 warning must list the escaped entry for %q", parent)
	}
	// A leading '#'/'!' is only escaped at the segment start, not mid-name.
	assert.Equal(t, `q#a/execution/logs/`, gitignoreEntriesFor("q#a")[2])
}

// CODEX-005: VS Code settings are serialized with encoding/json, so a parent with
// JSON-special characters still produces valid JSON pointing at the right path.
func TestBUG207_CODEX005_VscodeSettingsJSONSafe(t *testing.T) {
	// Default layout is byte-for-byte the shipped template (AC6).
	assert.Equal(t, vscodeSettingsTemplate, vscodeSettingsFor("gtms"))

	for _, parent := range []string{"testing", `q"x`, `a\b`, "qa suite", "q<a>&b"} {
		out := vscodeSettingsFor(parent)
		assert.True(t, json.Valid([]byte(out)), "output for parent %q must be valid JSON: %s", parent, out)

		var doc map[string]map[string]string
		require.NoError(t, json.Unmarshal([]byte(out), &doc), "parent %q", parent)
		schemas := doc["yaml.schemas"]
		require.Len(t, schemas, 1, "parent %q", parent)
		for k, v := range schemas {
			assert.Equal(t, parent+"/schemas/manual-result.schema.json", k, "schema key for %q", parent)
			assert.Equal(t, parent+"/manual/records/*.result.yaml", v, "records glob for %q", parent)
		}
	}
}
