package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ENH-168: working-dir validation. Mirrors TestAbsoluteOutputDirError -- build a
// programmatic gtms.config, LoadFromFile, assert on the error / warnings.

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "gtms.config")
	require.NoError(t, os.WriteFile(path, []byte(body), 0644))
	return path
}

func TestWorkingDir_RelativeAccepted(t *testing.T) {
	cfgContent := "project:\n  name: Test\n  repo: org/test\nadapters:\n  execute:\n    runner:\n      mode: sync\n      working-dir: PLAYWRIGHT-TESTS\n      command: 'echo run'\ndefaults:\n  execute: runner\n"
	cfg, err := LoadFromFile(writeConfig(t, cfgContent))
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, "PLAYWRIGHT-TESTS", cfg.Adapters["execute"]["runner"].WorkingDir)
	assert.Empty(t, cfg.Warnings, "a relative working-dir on a Tier-1 adapter is clean")
}

func TestWorkingDir_AbsoluteRejected(t *testing.T) {
	// filepath.IsAbs("/foo") is false on Windows, so build a real absolute path.
	abs := filepath.ToSlash(filepath.Join(t.TempDir(), "harness"))
	cfgContent := "project:\n  name: Test\n  repo: org/test\nadapters:\n  execute:\n    runner:\n      mode: sync\n      working-dir: " + abs + "\n      command: 'echo run'\ndefaults:\n  execute: runner\n"
	_, err := LoadFromFile(writeConfig(t, cfgContent))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "working-dir")
	assert.Contains(t, err.Error(), "Must be a relative path")
}

func TestWorkingDir_DotDotRejected(t *testing.T) {
	cfgContent := "project:\n  name: Test\n  repo: org/test\nadapters:\n  execute:\n    runner:\n      mode: sync\n      working-dir: ../outside\n      command: 'echo run'\ndefaults:\n  execute: runner\n"
	_, err := LoadFromFile(writeConfig(t, cfgContent))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "working-dir")
	assert.Contains(t, err.Error(), "inside the project root")
}

func TestWorkingDir_Tier0Warning(t *testing.T) {
	// A built-in (Tier 0) adapter: no command/script/module. working-dir has no
	// effect there, so config load must WARN (not error) and the cfg must load.
	cfgContent := "project:\n  name: Test\n  repo: org/test\nadapters:\n  create:\n    agent-create:\n      mode: sync\n      working-dir: harness\ndefaults:\n  create: agent-create\n"
	cfg, err := LoadFromFile(writeConfig(t, cfgContent))
	require.NoError(t, err, "Tier-0 working-dir must be a warning, not a hard error")
	require.NotNil(t, cfg)

	found := false
	for _, w := range cfg.Warnings {
		if strings.Contains(w, "working-dir") && strings.Contains(w, "Tier 0") {
			found = true
		}
	}
	assert.True(t, found, "expected a Tier-0 working-dir warning, got: %v", cfg.Warnings)
}
