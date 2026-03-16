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
