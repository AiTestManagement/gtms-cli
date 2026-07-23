package adapter

import (
	"testing"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveWiringExecuteAdapter_ConfiguredFramework(t *testing.T) {
	cfg := &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"runner-a": {Framework: "playwright", Script: "run.sh", Mode: "sync"},
			},
		},
	}
	resolved, fw, err := ResolveWiringExecuteAdapter(cfg, "runner-a", "")
	require.NoError(t, err)
	assert.Equal(t, "runner-a", resolved.Name)
	assert.Equal(t, "playwright", fw)
}

func TestResolveWiringExecuteAdapter_NameFallback(t *testing.T) {
	cfg := &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"runner-a": {Framework: "", Script: "run.sh", Mode: "sync"},
			},
		},
	}
	resolved, fw, err := ResolveWiringExecuteAdapter(cfg, "runner-a", "")
	require.NoError(t, err)
	assert.Equal(t, "runner-a", resolved.Name)
	assert.Equal(t, "runner-a", fw, "empty framework should fall back to adapter name")
}

func TestResolveWiringExecuteAdapter_ExplicitFrameworkAgreement(t *testing.T) {
	cfg := &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"runner-b": {Framework: "playwright", Script: "run.sh", Mode: "sync"},
			},
		},
	}
	resolved, fw, err := ResolveWiringExecuteAdapter(cfg, "runner-b", "playwright")
	require.NoError(t, err)
	assert.Equal(t, "runner-b", resolved.Name)
	assert.Equal(t, "playwright", fw)
}

func TestResolveWiringExecuteAdapter_ExplicitFrameworkMismatch(t *testing.T) {
	cfg := &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"runner-b": {Framework: "playwright", Script: "run.sh", Mode: "sync"},
			},
		},
	}
	_, _, err := ResolveWiringExecuteAdapter(cfg, "runner-b", "bats")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--framework")
	assert.Contains(t, err.Error(), "does not match")
}

func TestResolveWiringExecuteAdapter_MissingAdapter(t *testing.T) {
	cfg := &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"runner-a": {Framework: "playwright", Script: "run.sh", Mode: "sync"},
			},
		},
	}
	_, _, err := ResolveWiringExecuteAdapter(cfg, "no-such-runner", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
}

func TestResolveWiringExecuteAdapter_Mode3Reserved(t *testing.T) {
	cfg := &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"manual-execute": {Mode: "sync"},
			},
		},
	}
	for _, name := range []string{"manual-execute", "agent-execute", "manual-execute-script", "agent-execute-script"} {
		_, _, err := ResolveWiringExecuteAdapter(cfg, name, "")
		require.Error(t, err, "Mode 3 name %q should be rejected", name)
		assert.Contains(t, err.Error(), "Mode 3")
	}
}

func TestResolveWiringExecuteAdapter_EmptyName(t *testing.T) {
	cfg := &config.Config{}
	_, _, err := ResolveWiringExecuteAdapter(cfg, "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "required")
}

// The two key cases from ENH-191 acceptance criteria:

func TestResolveWiringExecuteAdapter_PlaywrightAdapterExplicitBats_Errors(t *testing.T) {
	// config framework:playwright + explicit --framework bats -> error
	cfg := &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"runner-b": {Framework: "playwright", Script: "run.sh", Mode: "sync"},
			},
		},
	}
	_, _, err := ResolveWiringExecuteAdapter(cfg, "runner-b", "bats")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not match")
}

func TestResolveWiringExecuteAdapter_NameFallbackOverride(t *testing.T) {
	// adapter name runner-a + config framework:"" + wiring framework runner-a -> valid
	cfg := &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"runner-a": {Framework: "", Script: "run.sh", Mode: "sync"},
			},
		},
	}
	resolved, fw, err := ResolveWiringExecuteAdapter(cfg, "runner-a", "")
	require.NoError(t, err)
	assert.Equal(t, "runner-a", resolved.Name)
	assert.Equal(t, "runner-a", fw)
}
