package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadGuidanceFromFile(t *testing.T) {
	// Create a temp project root with .gtms/guidance.yaml
	dir := t.TempDir()
	gtmsDir := filepath.Join(dir, ".gtms")
	require.NoError(t, os.MkdirAll(gtmsDir, 0755))

	content := "create: |\n  custom guidance for create\n"
	require.NoError(t, os.WriteFile(filepath.Join(gtmsDir, "guidance.yaml"), []byte(content), 0644))

	cfg := LoadGuidance(dir)
	assert.Contains(t, cfg["create"], "custom guidance for create")
}

func TestLoadGuidanceFallbackWhenMissing(t *testing.T) {
	dir := t.TempDir()
	cfg := LoadGuidance(dir)

	// Should return defaults
	assert.Contains(t, cfg["create"], "gtms status")
	assert.Contains(t, cfg["automate"], "gtms execute")
	assert.Contains(t, cfg["execute"], "gtms gaps")
	assert.Contains(t, cfg["init"], "gtms create")
}

func TestLoadGuidanceFallbackWhenMalformed(t *testing.T) {
	dir := t.TempDir()
	gtmsDir := filepath.Join(dir, ".gtms")
	require.NoError(t, os.MkdirAll(gtmsDir, 0755))

	// Write invalid YAML
	require.NoError(t, os.WriteFile(filepath.Join(gtmsDir, "guidance.yaml"), []byte("{{invalid yaml"), 0644))

	cfg := LoadGuidance(dir)
	// Should fall back to defaults
	assert.Contains(t, cfg["create"], "gtms status")
}

func TestDefaultGuidanceHasAllKeys(t *testing.T) {
	cfg := DefaultGuidance()
	for _, key := range []string{"init", "create", "prime", "automate", "execute"} {
		assert.NotEmpty(t, cfg[key], "default guidance should have key %q", key)
	}
}

// --- BUG-080: prime guidance key and per-key fallback ---

func TestDefaultGuidanceHasPrimeKey(t *testing.T) {
	cfg := DefaultGuidance()
	assert.Contains(t, cfg, "prime", "DefaultGuidance must contain a 'prime' key")
	assert.Contains(t, cfg["prime"], "manual-execute",
		"prime guidance should reference manual-execute adapter")
}

func TestLoadGuidanceFallsBackPerKeyForMissingPrime(t *testing.T) {
	// A user-customised guidance.yaml with only "automate:" should still
	// return a "prime" key sourced from DefaultGuidance().
	dir := t.TempDir()
	gtmsDir := filepath.Join(dir, ".gtms")
	require.NoError(t, os.MkdirAll(gtmsDir, 0755))

	content := "automate: |\n  custom automate guidance\n"
	require.NoError(t, os.WriteFile(filepath.Join(gtmsDir, "guidance.yaml"), []byte(content), 0644))

	cfg := LoadGuidance(dir)
	// User value preserved
	assert.Contains(t, cfg["automate"], "custom automate guidance",
		"user's automate guidance must be preserved")
	// Default prime key filled in
	assert.Contains(t, cfg, "prime",
		"missing prime key must be filled from DefaultGuidance()")
	assert.Contains(t, cfg["prime"], "manual-execute",
		"fallback prime guidance should reference manual-execute")
}

func TestLoadGuidancePreservesUserKeysOverDefaults(t *testing.T) {
	// When a user file has a "prime:" key, it should NOT be overwritten.
	dir := t.TempDir()
	gtmsDir := filepath.Join(dir, ".gtms")
	require.NoError(t, os.MkdirAll(gtmsDir, 0755))

	content := "prime: |\n  my custom prime guidance\n"
	require.NoError(t, os.WriteFile(filepath.Join(gtmsDir, "guidance.yaml"), []byte(content), 0644))

	cfg := LoadGuidance(dir)
	assert.Contains(t, cfg["prime"], "my custom prime guidance",
		"user's prime guidance must win over defaults")
}
