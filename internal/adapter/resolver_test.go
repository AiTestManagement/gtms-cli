package adapter

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/aitestmanagement/gtms-cli/internal/config"
)

// testConfig returns a config for testing resolver logic.
func testConfig() *config.Config {
	return &config.Config{
		Project: config.ProjectConfig{
			Name: "Test Project",
			Repo: "org/test",
		},
		Adapters: map[string]map[string]*config.AdapterConfig{
			"create": {
				"local-claude": {
					Mode:    "sync",
					Command: `claude -p {prompt}`,
				},
				"github-create": {
					Mode:   "async",
					Script: "adapters/github-create.sh",
				},
			},
			"automate": {
				"local-claude": {
					Mode:    "sync",
					Command: `claude -p {prompt}`,
				},
			},
			"execute": {
				"local-runner": {
					Mode:    "sync",
					Command: "npx playwright test",
				},
				"builtin-runner": {
					Mode: "sync",
					// No command, script, or module = built-in (tier 0)
				},
			},
		},
		Defaults: map[string]string{
			"create":  "local-claude",
			"automate": "local-claude",
			"execute": "local-runner",
		},
	}
}

func TestResolve_DefaultAdapter(t *testing.T) {
	cfg := testConfig()

	ra, err := Resolve(cfg, "create", "")
	require.NoError(t, err)
	assert.Equal(t, "create", ra.Command)
	assert.Equal(t, "local-claude", ra.Name)
	assert.Equal(t, "sync", ra.Mode)
	assert.Equal(t, 1, ra.Tier) // has Command set
}

func TestResolve_FlagOverride(t *testing.T) {
	cfg := testConfig()

	ra, err := Resolve(cfg, "create", "github-create")
	require.NoError(t, err)
	assert.Equal(t, "github-create", ra.Name)
	assert.Equal(t, "async", ra.Mode)
	assert.Equal(t, 2, ra.Tier) // has Script set
}

func TestResolve_MissingAdapter(t *testing.T) {
	cfg := testConfig()

	_, err := Resolve(cfg, "create", "nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "No adapter 'nonexistent' registered for 'create'")
	assert.Contains(t, err.Error(), "Available adapters:")
}

func TestResolve_VisibilityCommandFallback(t *testing.T) {
	cfg := testConfig()

	// status has no adapter config, should fall back to built-in
	ra, err := Resolve(cfg, "status", "")
	require.NoError(t, err)
	assert.Equal(t, "built-in", ra.Name)
	assert.Equal(t, 0, ra.Tier)
	assert.Equal(t, "sync", ra.Mode)
}

func TestResolve_GapsCommand(t *testing.T) {
	cfg := testConfig()

	ra, err := Resolve(cfg, "gaps", "")
	require.NoError(t, err)
	assert.Equal(t, "built-in", ra.Name)
	assert.Equal(t, 0, ra.Tier)
}

func TestResolve_TriageCommand(t *testing.T) {
	cfg := testConfig()

	ra, err := Resolve(cfg, "triage", "")
	require.NoError(t, err)
	assert.Equal(t, "built-in", ra.Name)
	assert.Equal(t, 0, ra.Tier)
}

func TestResolve_NoDefaultNoFlag(t *testing.T) {
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
		Adapters: map[string]map[string]*config.AdapterConfig{
			"create": {
				"some-adapter": {Mode: "sync", Command: "echo"},
			},
		},
		Defaults: map[string]string{},
	}

	_, err := Resolve(cfg, "create", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "No default adapter configured for 'create'")
}

// TestResolve_AutomateFromPresetDefault verifies BUG-123: an automate adapter
// entry that carries a framework but no command/script/module resolves as a
// Tier 0 built-in via the config default, and ResolveFramework yields the
// preset framework (no --framework flag needed). This is the resolution path
// the bats/playwright presets rely on for bare `gtms automate <tc>`.
func TestResolve_AutomateFromPresetDefault(t *testing.T) {
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
		Adapters: map[string]map[string]*config.AdapterConfig{
			"automate": {
				"agent-automate": {Mode: "sync", Framework: "bats"},
			},
		},
		Defaults: map[string]string{"automate": "agent-automate"},
	}

	ra, err := Resolve(cfg, "automate", "")
	require.NoError(t, err)
	assert.Equal(t, "automate", ra.Command)
	assert.Equal(t, "agent-automate", ra.Name)
	assert.Equal(t, "sync", ra.Mode)
	assert.Equal(t, 0, ra.Tier, "built-in entry (no command/script/module) is Tier 0")

	// No --framework flag: framework comes from the adapter config entry.
	assert.Equal(t, "bats", ResolveFramework(ra, ""), "framework resolves from the adapter config entry")
}

// TestResolve_ManualAutomateResolvesManualFramework verifies BUG-123: the manual
// preset's automate default resolves framework "manual", which BuiltinAutomate
// uses to surface the prime/execute diagnostic instead of the generic
// no-default-adapter error.
func TestResolve_ManualAutomateResolvesManualFramework(t *testing.T) {
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
		Adapters: map[string]map[string]*config.AdapterConfig{
			"automate": {
				"manual-automate": {Mode: "sync", Framework: "manual"},
			},
		},
		Defaults: map[string]string{"automate": "manual-automate"},
	}

	ra, err := Resolve(cfg, "automate", "")
	require.NoError(t, err)
	assert.Equal(t, "manual-automate", ra.Name)
	assert.Equal(t, 0, ra.Tier)
	assert.Equal(t, "manual", ResolveFramework(ra, ""))
}

func TestComputeTier_Command(t *testing.T) {
	ac := &config.AdapterConfig{Command: "echo hello"}
	assert.Equal(t, 1, computeTier(ac))
}

func TestComputeTier_Script(t *testing.T) {
	ac := &config.AdapterConfig{Script: "run.sh"}
	assert.Equal(t, 2, computeTier(ac))
}

func TestComputeTier_Module(t *testing.T) {
	ac := &config.AdapterConfig{Module: "github.com/some/module"}
	assert.Equal(t, 3, computeTier(ac))
}

func TestComputeTier_Builtin(t *testing.T) {
	ac := &config.AdapterConfig{}
	assert.Equal(t, 0, computeTier(ac))
}

func TestResolve_BuiltinTier(t *testing.T) {
	cfg := testConfig()

	ra, err := Resolve(cfg, "execute", "builtin-runner")
	require.NoError(t, err)
	assert.Equal(t, "builtin-runner", ra.Name)
	assert.Equal(t, 0, ra.Tier) // no command, script, or module
}

func TestResolve_NoAdaptersForCommand(t *testing.T) {
	cfg := &config.Config{
		Project:  config.ProjectConfig{Name: "Test", Repo: "org/test"},
		Adapters: map[string]map[string]*config.AdapterConfig{},
		Defaults: map[string]string{
			"create": "missing",
		},
	}

	_, err := Resolve(cfg, "create", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "No adapter 'missing' registered for 'create'")
}

// ENH-150: Built-in action adapter fallback tests

func TestResolve_BuiltinActionFallback_AgentCreate(t *testing.T) {
	// Empty config — no adapters registered at all
	cfg := &config.Config{
		Project:  config.ProjectConfig{Name: "Test", Repo: "org/test"},
		Adapters: map[string]map[string]*config.AdapterConfig{},
		Defaults: map[string]string{},
	}

	ra, err := Resolve(cfg, "create", "agent-create")
	require.NoError(t, err)
	assert.Equal(t, "agent-create", ra.Name)
	assert.Equal(t, 0, ra.Tier)
	assert.Equal(t, "sync", ra.Mode)
}

func TestResolve_BuiltinActionFallback_ManualCreate(t *testing.T) {
	cfg := &config.Config{
		Project:  config.ProjectConfig{Name: "Test", Repo: "org/test"},
		Adapters: map[string]map[string]*config.AdapterConfig{},
		Defaults: map[string]string{},
	}

	ra, err := Resolve(cfg, "create", "manual-create")
	require.NoError(t, err)
	assert.Equal(t, "manual-create", ra.Name)
	assert.Equal(t, 0, ra.Tier)
}

func TestResolve_BuiltinActionFallback_AgentPrime(t *testing.T) {
	cfg := &config.Config{
		Project:  config.ProjectConfig{Name: "Test", Repo: "org/test"},
		Adapters: map[string]map[string]*config.AdapterConfig{},
		Defaults: map[string]string{},
	}

	ra, err := Resolve(cfg, "prime", "agent-prime")
	require.NoError(t, err)
	assert.Equal(t, "agent-prime", ra.Name)
	assert.Equal(t, 0, ra.Tier)
}

func TestResolve_BuiltinActionFallback_ManualPrime(t *testing.T) {
	cfg := &config.Config{
		Project:  config.ProjectConfig{Name: "Test", Repo: "org/test"},
		Adapters: map[string]map[string]*config.AdapterConfig{},
		Defaults: map[string]string{},
	}

	ra, err := Resolve(cfg, "prime", "manual-prime")
	require.NoError(t, err)
	assert.Equal(t, "manual-prime", ra.Name)
	assert.Equal(t, 0, ra.Tier)
}

func TestResolve_BuiltinActionFallback_AgentExecute(t *testing.T) {
	cfg := &config.Config{
		Project:  config.ProjectConfig{Name: "Test", Repo: "org/test"},
		Adapters: map[string]map[string]*config.AdapterConfig{},
		Defaults: map[string]string{},
	}

	ra, err := Resolve(cfg, "execute", "agent-execute")
	require.NoError(t, err)
	assert.Equal(t, "agent-execute", ra.Name)
	assert.Equal(t, 0, ra.Tier)
}

func TestResolve_BuiltinActionFallback_ManualExecute(t *testing.T) {
	cfg := &config.Config{
		Project:  config.ProjectConfig{Name: "Test", Repo: "org/test"},
		Adapters: map[string]map[string]*config.AdapterConfig{},
		Defaults: map[string]string{},
	}

	ra, err := Resolve(cfg, "execute", "manual-execute")
	require.NoError(t, err)
	assert.Equal(t, "manual-execute", ra.Name)
	assert.Equal(t, 0, ra.Tier)
}

func TestResolve_ConfigTakesPrecedenceOverBuiltin(t *testing.T) {
	// Config defines agent-create with a script — should be Tier 2, not Tier 0
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
		Adapters: map[string]map[string]*config.AdapterConfig{
			"create": {
				"agent-create": {
					Mode:   "sync",
					Script: "my-custom-agent-create.sh",
				},
			},
		},
		Defaults: map[string]string{},
	}

	ra, err := Resolve(cfg, "create", "agent-create")
	require.NoError(t, err)
	assert.Equal(t, "agent-create", ra.Name)
	assert.Equal(t, 2, ra.Tier) // Script set = Tier 2
	assert.Equal(t, "my-custom-agent-create.sh", ra.Config.Script)
}

func TestResolve_UnknownNameStillErrors(t *testing.T) {
	cfg := &config.Config{
		Project:  config.ProjectConfig{Name: "Test", Repo: "org/test"},
		Adapters: map[string]map[string]*config.AdapterConfig{},
		Defaults: map[string]string{},
	}

	_, err := Resolve(cfg, "create", "totally-unknown")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "No adapter 'totally-unknown' registered for 'create'")
}

func TestResolve_PrimeDefaultsToManualPrime(t *testing.T) {
	// No flag, no config default — prime should fall back to built-in manual-prime
	cfg := &config.Config{
		Project:  config.ProjectConfig{Name: "Test", Repo: "org/test"},
		Adapters: map[string]map[string]*config.AdapterConfig{},
		Defaults: map[string]string{},
	}

	ra, err := Resolve(cfg, "prime", "")
	require.NoError(t, err)
	assert.Equal(t, "manual-prime", ra.Name)
	assert.Equal(t, 0, ra.Tier)
}

func TestResolve_PrimeConfigDefaultOverridesBuiltin(t *testing.T) {
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
		Adapters: map[string]map[string]*config.AdapterConfig{
			"prime": {
				"custom-prime": {
					Mode:   "sync",
					Script: "custom-prime.sh",
				},
			},
		},
		Defaults: map[string]string{
			"prime": "custom-prime",
		},
	}

	ra, err := Resolve(cfg, "prime", "")
	require.NoError(t, err)
	assert.Equal(t, "custom-prime", ra.Name)
	assert.Equal(t, 2, ra.Tier) // Script = Tier 2
}

func TestIsBuiltinActionAdapter(t *testing.T) {
	assert.True(t, IsBuiltinActionAdapter("create", "agent-create"))
	assert.True(t, IsBuiltinActionAdapter("create", "manual-create"))
	assert.True(t, IsBuiltinActionAdapter("prime", "agent-prime"))
	assert.True(t, IsBuiltinActionAdapter("prime", "manual-prime"))
	assert.True(t, IsBuiltinActionAdapter("execute", "agent-execute"))
	assert.True(t, IsBuiltinActionAdapter("execute", "manual-execute"))
	assert.True(t, IsBuiltinActionAdapter("automate", "agent-automate"))
	assert.True(t, IsBuiltinActionAdapter("automate", "manual-automate"))
	assert.False(t, IsBuiltinActionAdapter("create", "unknown"))
	assert.False(t, IsBuiltinActionAdapter("automate", "unknown"))
	assert.False(t, IsBuiltinActionAdapter("status", "built-in"))
}
