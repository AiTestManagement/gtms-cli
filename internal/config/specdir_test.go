package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCollectSpecDirs_UsesOutputDir(t *testing.T) {
	cfg := &Config{
		Adapters: map[string]map[string]*AdapterConfig{
			"automate": {
				"playwright": {Mode: "sync", OutputDir: "tests/e2e/"},
				"cucumber":   {Mode: "sync", OutputDir: "features/"},
			},
		},
	}

	dirs := CollectSpecDirs(cfg)
	assert.Contains(t, dirs, "tests/e2e/")
	assert.Contains(t, dirs, "features/")
	assert.Len(t, dirs, 2)
}

func TestCollectSpecDirs_SpecDirNormalizedToOutputDir(t *testing.T) {
	// Simulate post-normalization state: SpecDir set, OutputDir copied from SpecDir
	cfg := &Config{
		Adapters: map[string]map[string]*AdapterConfig{
			"automate": {
				"playwright": {Mode: "sync", SpecDir: "e2e/", OutputDir: "e2e/"},
			},
		},
	}

	dirs := CollectSpecDirs(cfg)
	assert.Contains(t, dirs, "e2e/")
	assert.Len(t, dirs, 1)
}

func TestCollectSpecDirs_DefaultWhenNoOutputDir(t *testing.T) {
	cfg := &Config{
		Adapters: map[string]map[string]*AdapterConfig{
			"automate": {
				"playwright": {Mode: "sync"},
			},
		},
	}

	dirs := CollectSpecDirs(cfg)
	assert.Contains(t, dirs, "test-automation/specs/playwright/")
	assert.Len(t, dirs, 1)
}

func TestCollectSpecDirs_Deduplication(t *testing.T) {
	// Two adapters with the same OutputDir should produce one entry
	cfg := &Config{
		Adapters: map[string]map[string]*AdapterConfig{
			"automate": {
				"playwright": {Mode: "sync", OutputDir: "tests/e2e/"},
			},
			"execute": {
				"runner": {Mode: "sync", OutputDir: "tests/e2e/"},
			},
		},
	}

	dirs := CollectSpecDirs(cfg)
	assert.Equal(t, []string{"tests/e2e/"}, dirs)
}

func TestCollectSpecDirs_MixedExplicitAndDefault(t *testing.T) {
	cfg := &Config{
		Adapters: map[string]map[string]*AdapterConfig{
			"automate": {
				"playwright": {Mode: "sync", OutputDir: "tests/e2e/"},
				"cypress":    {Mode: "sync"}, // no OutputDir → default
			},
		},
	}

	dirs := CollectSpecDirs(cfg)
	assert.Contains(t, dirs, "tests/e2e/")
	assert.Contains(t, dirs, "test-automation/specs/cypress/")
	assert.Len(t, dirs, 2)
}

func TestCollectSpecDirs_EmptyConfig(t *testing.T) {
	cfg := &Config{
		Adapters: map[string]map[string]*AdapterConfig{},
	}

	dirs := CollectSpecDirs(cfg)
	assert.Empty(t, dirs)
}
