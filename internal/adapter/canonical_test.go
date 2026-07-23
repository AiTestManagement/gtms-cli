package adapter

import (
	"testing"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ENH-191: ResolveCanonicalExecuteAdapter tests for the inherent-framework
// extension and Mode 3 exclusion.

func TestResolveCanonical_MatchingDefault(t *testing.T) {
	cfg := &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"runner-a": {Framework: "playwright", Script: "run.sh", Mode: "sync"},
				"runner-b": {Framework: "playwright", Script: "run2.sh", Mode: "sync"},
			},
		},
		Defaults: map[string]string{"execute": "runner-a"},
	}
	name, _, err := ResolveCanonicalExecuteAdapter(cfg, "playwright")
	require.NoError(t, err)
	assert.Equal(t, "runner-a", name)
}

func TestResolveCanonical_SoleCandidate(t *testing.T) {
	cfg := &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"runner-a": {Framework: "playwright", Script: "run.sh", Mode: "sync"},
				"bats-runner": {Framework: "bats", Script: "bats.sh", Mode: "sync"},
			},
		},
	}
	name, _, err := ResolveCanonicalExecuteAdapter(cfg, "playwright")
	require.NoError(t, err)
	assert.Equal(t, "runner-a", name)
}

func TestResolveCanonical_LexicalFallback(t *testing.T) {
	cfg := &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"beta-runner": {Framework: "playwright", Script: "b.sh", Mode: "sync"},
				"alpha-runner": {Framework: "playwright", Script: "a.sh", Mode: "sync"},
			},
		},
	}
	name, matches, err := ResolveCanonicalExecuteAdapter(cfg, "playwright")
	require.NoError(t, err)
	assert.Equal(t, "alpha-runner", name, "lexically first should win")
	assert.Len(t, matches, 2, "both candidates should be listed")
}

func TestResolveCanonical_NameFallback(t *testing.T) {
	// ENH-191: adapter with empty framework: should match when its name equals
	// the requested framework.
	cfg := &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"runner-a": {Framework: "", Script: "run.sh", Mode: "sync"},
			},
		},
	}
	name, _, err := ResolveCanonicalExecuteAdapter(cfg, "runner-a")
	require.NoError(t, err)
	assert.Equal(t, "runner-a", name)
}

func TestResolveCanonical_NameFallbackDefault(t *testing.T) {
	// ENH-191: default with empty framework: should match via name fallback.
	cfg := &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"runner-a": {Framework: "", Script: "run.sh", Mode: "sync"},
			},
		},
		Defaults: map[string]string{"execute": "runner-a"},
	}
	name, _, err := ResolveCanonicalExecuteAdapter(cfg, "runner-a")
	require.NoError(t, err)
	assert.Equal(t, "runner-a", name)
}

func TestResolveCanonical_ScriptVariantsExcludedFromManual(t *testing.T) {
	// BUG-165 / Option A: both -script variants are Mode 3 reserved names.
	// They are dispatched before wiring lookup and must never be canonical
	// wiring runners. When they are the only framework:"manual" execute
	// adapters, canonical resolution must error -- not return either of them.
	cfg := &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"manual-execute-script": {Framework: "manual", Script: "gtms/adapters/manual-execute-script.sh", Mode: "sync"},
				"agent-execute-script":  {Framework: "manual", Script: "gtms/adapters/agent-execute-script.sh", Mode: "sync"},
			},
		},
		Defaults: map[string]string{"execute": "manual-execute-script"},
	}
	_, _, err := ResolveCanonicalExecuteAdapter(cfg, "manual")
	require.Error(t, err, "both -script variants must be excluded from canonical selection")
	assert.Contains(t, err.Error(), "no execute adapter configured for framework")
}

func TestResolveCanonical_ScriptVariantsExcludedEvenWithoutDefault(t *testing.T) {
	// BUG-165 / Option A: same exclusion applies even without a project default.
	cfg := &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"manual-execute-script": {Framework: "manual", Script: "gtms/adapters/manual-execute-script.sh", Mode: "sync"},
				"agent-execute-script":  {Framework: "manual", Script: "gtms/adapters/agent-execute-script.sh", Mode: "sync"},
			},
		},
	}
	_, _, err := ResolveCanonicalExecuteAdapter(cfg, "manual")
	require.Error(t, err, "-script variants must be excluded even without a default")
	assert.Contains(t, err.Error(), "no execute adapter configured for framework")
}

func TestResolveCanonical_Mode3Excluded(t *testing.T) {
	// All four Mode 3 reserved names are excluded from canonical selection,
	// even when their framework matches; a real runner is preferred over any
	// Mode 3 adapter. (BUG-165: covers Tier-0 pair; -script pair covered
	// by TestResolveCanonical_ScriptVariantsExcludedFromManual.)
	cfg := &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"agent-execute": {Framework: "manual", Mode: "sync"},
				"real-runner":   {Framework: "manual", Script: "run.sh", Mode: "sync"},
			},
		},
	}
	name, _, err := ResolveCanonicalExecuteAdapter(cfg, "manual")
	require.NoError(t, err)
	assert.Equal(t, "real-runner", name, "Mode 3 should be excluded from candidates")
}

func TestResolveCanonical_Mode3DefaultExcluded(t *testing.T) {
	// A Mode 3 reserved name (manual-execute) set as defaults.execute must
	// be skipped by the fast path even if its framework matches.
	cfg := &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"manual-execute": {Framework: "manual", Mode: "sync"},
				"real-runner":    {Framework: "manual", Script: "run.sh", Mode: "sync"},
			},
		},
		Defaults: map[string]string{"execute": "manual-execute"},
	}
	name, _, err := ResolveCanonicalExecuteAdapter(cfg, "manual")
	require.NoError(t, err)
	assert.Equal(t, "real-runner", name, "Mode 3 default should be skipped")
}

func TestResolveCanonical_NoMatch(t *testing.T) {
	cfg := &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"runner-a": {Framework: "playwright", Script: "run.sh", Mode: "sync"},
			},
		},
	}
	_, _, err := ResolveCanonicalExecuteAdapter(cfg, "bats")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no execute adapter configured for framework")
}

func TestResolveCanonical_NilConfig(t *testing.T) {
	_, _, err := ResolveCanonicalExecuteAdapter(nil, "bats")
	require.Error(t, err)
}

func TestResolveCanonical_EmptyFramework(t *testing.T) {
	cfg := &config.Config{}
	_, _, err := ResolveCanonicalExecuteAdapter(cfg, "")
	require.Error(t, err)
}
