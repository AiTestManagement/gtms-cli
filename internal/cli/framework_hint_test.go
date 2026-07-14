package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// ---------------------------------------------------------------------------
// editDistance
// ---------------------------------------------------------------------------

func TestEditDistance(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"", "abc", 3},
		{"abc", "abc", 0},
		{"manual", "manuald", 1},  // trailing char
		{"bats", "cat", 2},        // substitution + deletion
		{"bats", "pester", 5},     // very different
		{"playwright", "playwrit", 2}, // missing 'g' and 'h' from 'wright'
		{"bats", "bats", 0},
		{"a", "b", 1},
	}
	for _, tt := range tests {
		t.Run(tt.a+"_vs_"+tt.b, func(t *testing.T) {
			got := editDistance(tt.a, tt.b)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ---------------------------------------------------------------------------
// frameworkHint
// ---------------------------------------------------------------------------

func TestFrameworkHint_NearTypo(t *testing.T) {
	known := []string{"bats", "manual", "playwright"}

	hint := frameworkHint("manuald", known)
	assert.NotEmpty(t, hint, "manuald is 1 edit from manual -- should fire")
	assert.Contains(t, hint, "manual")
	assert.Contains(t, hint, "manuald")
	assert.Contains(t, hint, "Did you mean")
}

func TestFrameworkHint_Recognised(t *testing.T) {
	known := []string{"bats", "manual", "playwright"}

	// Recognised frameworks must never trigger a hint, even if absent from
	// the current scope -- this is the v1-regression pin.
	for _, fw := range known {
		t.Run(fw, func(t *testing.T) {
			assert.Empty(t, frameworkHint(fw, known))
		})
	}
}

func TestFrameworkHint_Empty(t *testing.T) {
	known := []string{"bats", "manual", "playwright"}
	assert.Empty(t, frameworkHint("", known))
}

func TestFrameworkHint_DistinctUnknown(t *testing.T) {
	known := []string{"bats", "manual", "playwright"}

	// These are unrecognised but NOT near-typos (distance > 2).
	for _, fw := range []string{"pester", "nonexistent-fw", "foobar", "cypress"} {
		t.Run(fw, func(t *testing.T) {
			assert.Empty(t, frameworkHint(fw, known),
				"%s should not trigger a hint (distance > 2 from all known)", fw)
		})
	}
}

func TestFrameworkHint_EmptyKnownSet(t *testing.T) {
	// Edge case: no known frameworks at all. Should return "" (no hint).
	assert.Empty(t, frameworkHint("anything", nil))
	assert.Empty(t, frameworkHint("anything", []string{}))
}

func TestFrameworkHint_NoFrameworkMismatchKeyword(t *testing.T) {
	known := []string{"bats", "manual", "playwright"}
	hint := frameworkHint("manuald", known)
	assert.NotContains(t, hint, "framework_mismatch",
		"hint must not contain the literal 'framework_mismatch' keyword")
}

func TestFrameworkHint_NotePrefix(t *testing.T) {
	known := []string{"bats", "manual", "playwright"}
	hint := frameworkHint("manuald", known)
	assert.Contains(t, hint, "Note:")
}

// ---------------------------------------------------------------------------
// knownFrameworks
// ---------------------------------------------------------------------------

func TestKnownFrameworks_ShippedAlwaysPresent(t *testing.T) {
	// Even with an empty config and no wiring, the shipped set is present.
	cfg := &config.Config{}
	root := t.TempDir()

	known := knownFrameworks(cfg, root)
	assert.Contains(t, known, "bats")
	assert.Contains(t, known, "playwright")
	assert.Contains(t, known, "manual")
}

func TestKnownFrameworks_ExcludesNone(t *testing.T) {
	// Config with an adapter that has no framework field -> "(none)" bucket;
	// that must NOT appear in the known set.
	cfg := &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"create": {
				"mock": {Mode: "sync", Command: "echo ok"},
			},
		},
	}
	root := t.TempDir()

	known := knownFrameworks(cfg, root)
	for _, fw := range known {
		assert.NotEqual(t, "(none)", fw)
	}
}

func TestKnownFrameworks_ConfigFrameworkIncluded(t *testing.T) {
	cfg := &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"automate": {
				"pester-runner": {
					Mode: "sync", Command: "echo ok", Framework: "pester",
				},
			},
		},
	}
	root := t.TempDir()

	known := knownFrameworks(cfg, root)
	assert.Contains(t, known, "pester")
}

func TestKnownFrameworks_WiringFrameworkIncluded(t *testing.T) {
	cfg := &config.Config{}
	root := t.TempDir()

	// Seed a wiring record for "cypress" -- no config adapter for it.
	setupWiringDir(t, root)
	rec := &wiring.WiringRecord{
		TestCase:     "tc-aabbccdd",
		TestCaseHash: "0123456789abcdef",
		Framework:    "cypress",
		Adapter:      "cypress-runner",
		Artefact:     "test/cypress/tc-aabbccdd.spec.js",
		ArtefactHash: "fedcba9876543210",
	}
	_, err := wiring.Write(root, rec)
	require.NoError(t, err)

	known := knownFrameworks(cfg, root)
	assert.Contains(t, known, "cypress",
		"a framework present only in wiring records must be recognised")
}

func TestKnownFrameworks_Sorted(t *testing.T) {
	cfg := &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"automate": {
				"z-runner": {
					Mode: "sync", Command: "echo ok", Framework: "ztest",
				},
			},
		},
	}
	root := t.TempDir()

	known := knownFrameworks(cfg, root)
	for i := 1; i < len(known); i++ {
		assert.True(t, known[i-1] <= known[i],
			"knownFrameworks must be sorted: %q should come before %q", known[i-1], known[i])
	}
}

// setupWiringDir creates the parent directories needed for wiring.Write.
func setupWiringDir(t *testing.T, root string) {
	t.Helper()
	// wiring.Write creates the directory on demand, but we need gtms.config
	// and .gtms-root for layout discovery to work.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms", "test", "cases"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "gtms.config"), []byte("project:\n  name: test\n  repo: test/repo\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gtms-root"), []byte(""), 0644))
}
