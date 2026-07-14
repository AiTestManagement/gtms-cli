package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/aitestmanagement/gtms-cli/internal/config"
)

// fixtureConfig returns a test config with create, automate, and execute adapters.
func fixtureConfig() *config.Config {
	return &config.Config{
		Project: config.ProjectConfig{Name: "test-project", Repo: "test/repo"},
		Adapters: map[string]map[string]*config.AdapterConfig{
			"create": {
				"local-claude": {
					Mode:    "sync",
					Command: "claude -p create",
				},
			},
			"automate": {
				"bats": {
					Mode:      "sync",
					Command:   "claude -p automate-bats --append-system-prompt-file {prompt_file}",
					Framework: "bats",
				},
				"local-claude": {
					Mode:    "sync",
					Command: "claude -p automate",
				},
			},
			"execute": {
				"bats-runner": {
					Mode:      "sync",
					Command:   "bats {artefact_file}",
					Framework: "bats",
				},
				"local-runner": {
					Mode:    "sync",
					Command: "npx playwright test {artefact_file}",
				},
				"remote-bats": {
					Mode:      "sync",
					Script:    "adapters/remote-bats.sh",
					Framework: "bats",
				},
			},
		},
		Defaults: map[string]string{
			"create":   "local-claude",
			"automate": "bats",
			"execute":  "bats-runner",
		},
	}
}

func TestBuildAdapterEntries_SortOrder(t *testing.T) {
	cfg := fixtureConfig()
	entries := buildAdapterEntries(cfg)

	// Collect command order
	var commands []string
	for _, e := range entries {
		if len(commands) == 0 || commands[len(commands)-1] != e.Command {
			commands = append(commands, e.Command)
		}
	}

	// create must come before automate, automate before execute, then built-ins
	createIdx := indexOf(commands, "create")
	automateIdx := indexOf(commands, "automate")
	executeIdx := indexOf(commands, "execute")
	require.NotEqual(t, -1, createIdx, "create bucket must exist")
	require.NotEqual(t, -1, automateIdx, "automate bucket must exist")
	require.NotEqual(t, -1, executeIdx, "execute bucket must exist")
	assert.Less(t, createIdx, automateIdx, "create before automate")
	assert.Less(t, automateIdx, executeIdx, "automate before execute")
}

func TestBuildAdapterEntries_AlphabeticalWithinBucket(t *testing.T) {
	cfg := fixtureConfig()
	entries := buildAdapterEntries(cfg)

	// Check automate bucket is sorted alphabetically.
	// Config adapters: bats, local-claude. Built-ins: agent-automate, manual-automate.
	var automateNames []string
	for _, e := range entries {
		if e.Command == "automate" {
			automateNames = append(automateNames, e.Name)
		}
	}
	require.Len(t, automateNames, 4)
	assert.Equal(t, "agent-automate", automateNames[0])
	assert.Equal(t, "bats", automateNames[1])
	assert.Equal(t, "local-claude", automateNames[2])
	assert.Equal(t, "manual-automate", automateNames[3])
}

func TestBuildAdapterEntries_BuiltinsIncluded(t *testing.T) {
	cfg := fixtureConfig()
	entries := buildAdapterEntries(cfg)

	builtinNames := make(map[string]bool)
	for _, e := range entries {
		if e.Tier == 0 {
			builtinNames[e.Command] = true
		}
	}
	assert.True(t, builtinNames["gaps"], "gaps built-in present")
	assert.True(t, builtinNames["map"], "map built-in present")
	assert.True(t, builtinNames["status"], "status built-in present")
	assert.True(t, builtinNames["triage"], "triage built-in present")
}

func TestBuildAdapterEntries_TierDerivation(t *testing.T) {
	cfg := fixtureConfig()
	entries := buildAdapterEntries(cfg)

	tierMap := make(map[string]int)
	for _, e := range entries {
		tierMap[e.Command+":"+e.Name] = e.Tier
	}

	assert.Equal(t, 1, tierMap["create:local-claude"], "command → tier 1")
	assert.Equal(t, 2, tierMap["execute:remote-bats"], "script → tier 2")
	assert.Equal(t, 0, tierMap["status:built-in"], "built-in → tier 0")
}

func TestBuildAdapterEntries_DefaultMarker(t *testing.T) {
	cfg := fixtureConfig()
	entries := buildAdapterEntries(cfg)

	defaults := make(map[string]bool)
	for _, e := range entries {
		if e.Default {
			defaults[e.Command+":"+e.Name] = true
		}
	}

	assert.True(t, defaults["create:local-claude"])
	assert.True(t, defaults["automate:bats"])
	assert.True(t, defaults["execute:bats-runner"])
	// fixtureConfig has no defaults.prime; the resolver's implicit default
	// (manual-prime) must be marked as well.
	assert.True(t, defaults["prime:manual-prime"], "implicit prime default must be marked")
	assert.Len(t, defaults, 4, "three config defaults + one implicit built-in default")
}

func TestBuildAdapterEntries_ToolValue(t *testing.T) {
	cfg := fixtureConfig()
	entries := buildAdapterEntries(cfg)

	toolMap := make(map[string]string)
	for _, e := range entries {
		toolMap[e.Command+":"+e.Name] = e.Tool
	}

	assert.Equal(t, "claude -p create", toolMap["create:local-claude"])
	assert.Equal(t, "adapters/remote-bats.sh", toolMap["execute:remote-bats"])
	assert.Equal(t, "(built-in)", toolMap["status:built-in"])
}

func TestBuildFrameworkEntries_GroupsByFramework(t *testing.T) {
	cfg := fixtureConfig()
	entries := buildAdapterEntries(cfg)
	fwEntries := buildFrameworkEntries(entries)

	fwMap := make(map[string][]string)
	for _, e := range fwEntries {
		fwMap[e.Framework] = e.Adapters
	}

	assert.Contains(t, fwMap, "bats")
	assert.Contains(t, fwMap, "(none)")

	// bats should include automate:bats, execute:bats-runner, execute:remote-bats
	batsAdapters := fwMap["bats"]
	assert.Contains(t, batsAdapters, "automate:bats")
	assert.Contains(t, batsAdapters, "execute:bats-runner")
	assert.Contains(t, batsAdapters, "execute:remote-bats")
}

func TestBuildFrameworkEntries_NoneGroupLast(t *testing.T) {
	cfg := fixtureConfig()
	entries := buildAdapterEntries(cfg)
	fwEntries := buildFrameworkEntries(entries)

	// (none) must be last
	last := fwEntries[len(fwEntries)-1]
	assert.Equal(t, "(none)", last.Framework)
}

func TestBuildFrameworkEntries_AdaptersSorted(t *testing.T) {
	cfg := fixtureConfig()
	entries := buildAdapterEntries(cfg)
	fwEntries := buildFrameworkEntries(entries)

	for _, e := range fwEntries {
		sorted := make([]string, len(e.Adapters))
		copy(sorted, e.Adapters)
		assert.Equal(t, sorted, e.Adapters, "adapters should be sorted for framework %s", e.Framework)
	}
}

func TestRenderAdaptersTable_NoShowTools(t *testing.T) {
	cfg := fixtureConfig()
	entries := buildAdapterEntries(cfg)

	var buf strings.Builder
	renderAdaptersTable(&buf, entries, false)
	out := buf.String()

	// Header should NOT contain TOOL
	lines := strings.Split(out, "\n")
	assert.Contains(t, lines[0], "COMMAND")
	assert.Contains(t, lines[0], "NAME")
	assert.Contains(t, lines[0], "TIER")
	assert.Contains(t, lines[0], "FRAMEWORK")
	assert.Contains(t, lines[0], "MODE")
	assert.Contains(t, lines[0], "DEFAULT")
	assert.NotContains(t, lines[0], "TOOL")

	// Check that adapter names appear
	assert.Contains(t, out, "local-claude")
	assert.Contains(t, out, "bats-runner")
}

func TestRenderAdaptersTable_ShowTools(t *testing.T) {
	cfg := fixtureConfig()
	entries := buildAdapterEntries(cfg)

	var buf strings.Builder
	renderAdaptersTable(&buf, entries, true)
	out := buf.String()

	lines := strings.Split(out, "\n")
	assert.Contains(t, lines[0], "TOOL")
}

func TestRenderAdaptersTable_EmDashForNoFramework(t *testing.T) {
	cfg := fixtureConfig()
	entries := buildAdapterEntries(cfg)

	var buf strings.Builder
	renderAdaptersTable(&buf, entries, false)
	out := buf.String()

	assert.Contains(t, out, "\u2014", "em-dash should appear for framework-less adapters")
}

func TestRenderAdaptersTable_DefaultMarkerStar(t *testing.T) {
	cfg := fixtureConfig()
	entries := buildAdapterEntries(cfg)

	var buf strings.Builder
	renderAdaptersTable(&buf, entries, false)
	out := buf.String()

	assert.Contains(t, out, "*", "star should mark default adapters")
}

func TestRunListAdapters_JSON(t *testing.T) {
	cfg := fixtureConfig()
	entries := buildAdapterEntries(cfg)

	var buf strings.Builder
	err := runListAdapters(&buf, entries, true, false)
	require.NoError(t, err)

	var result []adapterEntry
	err = json.Unmarshal([]byte(buf.String()), &result)
	require.NoError(t, err)
	assert.NotEmpty(t, result)

	// Verify JSON always includes tool field
	for _, e := range result {
		assert.NotEmpty(t, e.Tool, "tool field must be present for %s:%s", e.Command, e.Name)
	}

	// Verify schema fields present
	first := result[0]
	assert.NotEmpty(t, first.Command)
	assert.NotEmpty(t, first.Name)
	assert.NotEmpty(t, first.Mode)
}

func TestRunListFrameworks_JSON(t *testing.T) {
	cfg := fixtureConfig()
	entries := buildAdapterEntries(cfg)
	fwEntries := buildFrameworkEntries(entries)

	var buf strings.Builder
	err := runListFrameworks(&buf, fwEntries, true)
	require.NoError(t, err)

	var result []frameworkEntry
	err = json.Unmarshal([]byte(buf.String()), &result)
	require.NoError(t, err)
	assert.NotEmpty(t, result)

	// Verify bats framework exists
	found := false
	for _, e := range result {
		if e.Framework == "bats" {
			found = true
			assert.NotEmpty(t, e.Adapters)
		}
	}
	assert.True(t, found, "bats framework should exist in JSON output")
}

func TestRunListAll_BothSections(t *testing.T) {
	cfg := fixtureConfig()
	entries := buildAdapterEntries(cfg)
	fwEntries := buildFrameworkEntries(entries)

	var buf strings.Builder
	err := runListAll(&buf, entries, fwEntries, false, false)
	require.NoError(t, err)
	out := buf.String()

	assert.Contains(t, out, "ADAPTERS")
	assert.Contains(t, out, "========")
	assert.Contains(t, out, "FRAMEWORKS")
	assert.Contains(t, out, "==========")
}

func TestRunListAll_JSON(t *testing.T) {
	cfg := fixtureConfig()
	entries := buildAdapterEntries(cfg)
	fwEntries := buildFrameworkEntries(entries)

	var buf strings.Builder
	err := runListAll(&buf, entries, fwEntries, true, false)
	require.NoError(t, err)

	var result listAllJSON
	err = json.Unmarshal([]byte(buf.String()), &result)
	require.NoError(t, err)
	assert.NotEmpty(t, result.Adapters)
	assert.NotEmpty(t, result.Frameworks)
}

func TestTruncateTool(t *testing.T) {
	short := "bats {artefact_file}"
	assert.Equal(t, short, truncateTool(short, 45))

	long := "claude -p automate-bats --append-system-prompt-file {prompt_file} --verbose"
	result := truncateTool(long, 45)
	assert.Len(t, result, 45)
	assert.True(t, strings.HasSuffix(result, "..."))
}

func TestRenderFrameworksTable(t *testing.T) {
	cfg := fixtureConfig()
	entries := buildAdapterEntries(cfg)
	fwEntries := buildFrameworkEntries(entries)

	var buf strings.Builder
	renderFrameworksTable(&buf, fwEntries)
	out := buf.String()

	assert.Contains(t, out, "FRAMEWORK")
	assert.Contains(t, out, "ADAPTERS")
	assert.Contains(t, out, "bats")
	assert.Contains(t, out, "(none)")
}

func TestBuildAdapterEntries_EmptyConfig(t *testing.T) {
	cfg := &config.Config{
		Project:  config.ProjectConfig{Name: "empty", Repo: "empty/repo"},
		Adapters: map[string]map[string]*config.AdapterConfig{},
		Defaults: map[string]string{},
	}
	entries := buildAdapterEntries(cfg)

	// Should contain one hint row per empty pipeline bucket plus all built-ins.
	// 3 hint rows (create/automate/execute) + 4 reader built-ins + 8 action built-ins = 15.
	assert.Len(t, entries, 15, "3 empty-bucket hints + 12 built-in entries for empty config")

	// Verify hints are present for each pipeline bucket.
	hintBuckets := make(map[string]bool)
	var builtinCount int
	for _, e := range entries {
		if e.Tier == hintTierSentinel {
			hintBuckets[e.Command] = true
			assert.Contains(t, e.Name, "no adapters", "hint name should mention 'no adapters'")
			assert.Contains(t, e.Name, "gtms init", "hint name should reference 'gtms init'")
			continue
		}
		assert.Equal(t, 0, e.Tier, "non-hint entries should be tier 0 (built-ins) for empty config")
		builtinCount++
	}
	assert.True(t, hintBuckets["create"], "hint row for create bucket")
	assert.True(t, hintBuckets["automate"], "hint row for automate bucket")
	assert.True(t, hintBuckets["execute"], "hint row for execute bucket")
	assert.Equal(t, 12, builtinCount, "exactly 12 built-in entries (4 readers + 8 action)")
}

func TestBuildAdapterEntries_EmptyBucketHint(t *testing.T) {
	// Config with execute empty but create + automate populated — verify only
	// the empty bucket gets a hint row.
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "partial", Repo: "test/repo"},
		Adapters: map[string]map[string]*config.AdapterConfig{
			"create": {
				"local": {Mode: "sync", Command: "echo create"},
			},
			"automate": {
				"local": {Mode: "sync", Command: "echo automate"},
			},
			"execute": {},
		},
		Defaults: map[string]string{},
	}
	entries := buildAdapterEntries(cfg)

	var hintCommands []string
	for _, e := range entries {
		if e.Tier == hintTierSentinel {
			hintCommands = append(hintCommands, e.Command)
		}
	}
	assert.Equal(t, []string{"execute"}, hintCommands, "only execute should have a hint row")
}

func TestBuildAdapterEntries_ActionBuiltinsIncluded(t *testing.T) {
	cfg := fixtureConfig()
	entries := buildAdapterEntries(cfg)

	// All 8 action built-ins must appear.
	actionBuiltins := map[string]bool{
		"create:agent-create":     false,
		"create:manual-create":    false,
		"automate:agent-automate": false,
		"automate:manual-automate": false,
		"prime:agent-prime":       false,
		"prime:manual-prime":      false,
		"execute:agent-execute":   false,
		"execute:manual-execute":  false,
	}
	for _, e := range entries {
		key := e.Command + ":" + e.Name
		if _, want := actionBuiltins[key]; want {
			actionBuiltins[key] = true
			assert.Equal(t, 0, e.Tier, "action built-in %s should be tier 0", key)
			assert.Equal(t, "(built-in)", e.Tool, "action built-in %s should have tool (built-in)", key)
			assert.Equal(t, "sync", e.Mode, "action built-in %s should have mode sync", key)
		}
	}
	for key, found := range actionBuiltins {
		assert.True(t, found, "action built-in %s must be present in entries", key)
	}
}

func TestBuildAdapterEntries_PrimeOrdering(t *testing.T) {
	cfg := fixtureConfig()
	entries := buildAdapterEntries(cfg)

	// Collect unique command order as they appear in sorted entries.
	var commands []string
	for _, e := range entries {
		if len(commands) == 0 || commands[len(commands)-1] != e.Command {
			commands = append(commands, e.Command)
		}
	}

	executeIdx := indexOf(commands, "execute")
	primeIdx := indexOf(commands, "prime")
	gapsIdx := indexOf(commands, "gaps")
	require.NotEqual(t, -1, primeIdx, "prime bucket must exist")
	require.NotEqual(t, -1, gapsIdx, "gaps bucket must exist")
	assert.Less(t, executeIdx, primeIdx, "execute before prime")
	assert.Less(t, primeIdx, gapsIdx, "prime before gaps")
}

func TestBuildAdapterEntries_ConfigShadowsBuiltin(t *testing.T) {
	// Config defines agent-create with a script -- should produce one Tier 2 row,
	// not a duplicate Tier 0 built-in row.
	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "shadow", Repo: "test/repo"},
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
	entries := buildAdapterEntries(cfg)

	// Count entries with command:name == "create:agent-create".
	var matches []adapterEntry
	for _, e := range entries {
		if e.Command == "create" && e.Name == "agent-create" {
			matches = append(matches, e)
		}
	}
	require.Len(t, matches, 1, "exactly one row for create:agent-create")
	assert.Equal(t, 2, matches[0].Tier, "config-defined entry should be tier 2 (script)")
	assert.Equal(t, "my-custom-agent-create.sh", matches[0].Tool)
}

func TestBuildAdapterEntries_BuiltinDefaultMarker(t *testing.T) {
	t.Run("implicit_prime_default_marks_manual_prime", func(t *testing.T) {
		// No cfg.Defaults.prime, no adapters.prime entries -> resolver falls
		// back to manual-prime. The Default marker must follow the resolver.
		cfg := &config.Config{
			Project:  config.ProjectConfig{Name: "p", Repo: "r"},
			Adapters: map[string]map[string]*config.AdapterConfig{},
			Defaults: map[string]string{},
		}
		entries := buildAdapterEntries(cfg)

		var primeDefaults []string
		for _, e := range entries {
			if e.Command == "prime" && e.Default {
				primeDefaults = append(primeDefaults, e.Name)
			}
		}
		assert.Equal(t, []string{"manual-prime"}, primeDefaults,
			"manual-prime must carry the DEFAULT * when no defaults.prime is set")
	})

	t.Run("explicit_defaults_prime_marks_builtin_row_without_adapters_entry", func(t *testing.T) {
		// cfg.Defaults.prime = manual-prime but no adapters.prime.manual-prime
		// entry -- the built-in row must carry the *.
		cfg := &config.Config{
			Project:  config.ProjectConfig{Name: "p", Repo: "r"},
			Adapters: map[string]map[string]*config.AdapterConfig{},
			Defaults: map[string]string{"prime": "manual-prime"},
		}
		entries := buildAdapterEntries(cfg)

		var marked *adapterEntry
		for i := range entries {
			if entries[i].Command == "prime" && entries[i].Name == "manual-prime" {
				marked = &entries[i]
			}
		}
		require.NotNil(t, marked, "manual-prime row must be present")
		assert.True(t, marked.Default, "built-in manual-prime row must be marked default")
		assert.Equal(t, 0, marked.Tier, "row should still be a Tier 0 built-in")
	})

	t.Run("explicit_defaults_prime_to_agent_marks_agent_not_manual", func(t *testing.T) {
		// cfg.Defaults.prime = agent-prime -- the agent-prime built-in row
		// must carry the *, and manual-prime must not.
		cfg := &config.Config{
			Project:  config.ProjectConfig{Name: "p", Repo: "r"},
			Adapters: map[string]map[string]*config.AdapterConfig{},
			Defaults: map[string]string{"prime": "agent-prime"},
		}
		entries := buildAdapterEntries(cfg)

		for _, e := range entries {
			if e.Command != "prime" {
				continue
			}
			switch e.Name {
			case "agent-prime":
				assert.True(t, e.Default, "agent-prime must be marked default")
			case "manual-prime":
				assert.False(t, e.Default, "manual-prime must NOT be marked default when agent-prime is")
			}
		}
	})

	t.Run("config_shadow_keeps_default_on_config_row_not_builtin", func(t *testing.T) {
		// adapters.prime.manual-prime defined + defaults.prime = manual-prime
		// -- only the config row exists (built-in deduped) and carries the *.
		cfg := &config.Config{
			Project: config.ProjectConfig{Name: "p", Repo: "r"},
			Adapters: map[string]map[string]*config.AdapterConfig{
				"prime": {
					"manual-prime": {
						Mode:   "sync",
						Script: "my-shadow.sh",
					},
				},
			},
			Defaults: map[string]string{"prime": "manual-prime"},
		}
		entries := buildAdapterEntries(cfg)

		var rows []adapterEntry
		for _, e := range entries {
			if e.Command == "prime" && e.Name == "manual-prime" {
				rows = append(rows, e)
			}
		}
		require.Len(t, rows, 1, "expected exactly one manual-prime row (config shadows builtin)")
		assert.Equal(t, 2, rows[0].Tier, "row must be the config Tier 2 entry, not the builtin")
		assert.True(t, rows[0].Default, "config-shadowed default must still carry the *")
	})
}

// ENH-163: When defaults.execute names a Mode 3 adapter, it is now a true
// runtime default (Default=true) because no-flag `gtms execute` consults
// defaults.execute before wiring lookup.
func TestBuildAdapterEntries_ExecuteMode3Default(t *testing.T) {
	// Tier 0 built-ins: manual-execute, agent-execute.
	// Tier 2 script adapters: manual-execute-script, agent-execute-script
	// (must be registered in config to appear in entries).
	t.Run("manual-execute", func(t *testing.T) {
		cfg := &config.Config{
			Project:  config.ProjectConfig{Name: "manual", Repo: "test/repo"},
			Adapters: map[string]map[string]*config.AdapterConfig{},
			Defaults: map[string]string{"execute": "manual-execute"},
		}
		entries := buildAdapterEntries(cfg)
		for _, e := range entries {
			if e.Command == "execute" && e.Name == "manual-execute" {
				assert.True(t, e.Default, "manual-execute must be marked Default")
				assert.False(t, e.Configured, "manual-execute must NOT be Configured")
				return
			}
		}
		t.Fatal("manual-execute entry not found")
	})

	t.Run("agent-execute", func(t *testing.T) {
		cfg := &config.Config{
			Project:  config.ProjectConfig{Name: "manual", Repo: "test/repo"},
			Adapters: map[string]map[string]*config.AdapterConfig{},
			Defaults: map[string]string{"execute": "agent-execute"},
		}
		entries := buildAdapterEntries(cfg)
		for _, e := range entries {
			if e.Command == "execute" && e.Name == "agent-execute" {
				assert.True(t, e.Default, "agent-execute must be marked Default")
				assert.False(t, e.Configured, "agent-execute must NOT be Configured")
				return
			}
		}
		t.Fatal("agent-execute entry not found")
	})

	t.Run("manual-execute-script", func(t *testing.T) {
		cfg := &config.Config{
			Project: config.ProjectConfig{Name: "manual", Repo: "test/repo"},
			Adapters: map[string]map[string]*config.AdapterConfig{
				"execute": {
					"manual-execute-script": {
						Mode:   "sync",
						Script: "gtms/adapters/manual-execute-script.sh",
					},
				},
			},
			Defaults: map[string]string{"execute": "manual-execute-script"},
		}
		entries := buildAdapterEntries(cfg)
		for _, e := range entries {
			if e.Command == "execute" && e.Name == "manual-execute-script" {
				assert.True(t, e.Default, "manual-execute-script must be marked Default")
				assert.False(t, e.Configured, "manual-execute-script must NOT be Configured")
				return
			}
		}
		t.Fatal("manual-execute-script entry not found")
	})

	t.Run("agent-execute-script", func(t *testing.T) {
		cfg := &config.Config{
			Project: config.ProjectConfig{Name: "manual", Repo: "test/repo"},
			Adapters: map[string]map[string]*config.AdapterConfig{
				"execute": {
					"agent-execute-script": {
						Mode:   "sync",
						Script: "gtms/adapters/agent-execute-script.sh",
					},
				},
			},
			Defaults: map[string]string{"execute": "agent-execute-script"},
		}
		entries := buildAdapterEntries(cfg)
		for _, e := range entries {
			if e.Command == "execute" && e.Name == "agent-execute-script" {
				assert.True(t, e.Default, "agent-execute-script must be marked Default")
				assert.False(t, e.Configured, "agent-execute-script must NOT be Configured")
				return
			}
		}
		t.Fatal("agent-execute-script entry not found")
	})
}

// ENH-160: When defaults.execute names a framework runner (bats-runner),
// the adapter should remain Default=true, Configured=false.
func TestBuildAdapterEntries_ExecuteFrameworkRunnerDefault(t *testing.T) {
	cfg := fixtureConfig() // defaults.execute = bats-runner
	entries := buildAdapterEntries(cfg)

	for _, e := range entries {
		if e.Command == "execute" && e.Name == "bats-runner" {
			assert.True(t, e.Default,
				"bats-runner must be marked Default (it IS the runtime default)")
			assert.False(t, e.Configured,
				"bats-runner must NOT be marked Configured (it is Default)")
		}
	}
}

// ENH-160/ENH-163: Table rendering shows "configured" label for Configured entries.
// After ENH-163, no production code sets Configured=true for execute Mode 3
// adapters, but the label rendering path still works for any entry with
// Configured=true (future-proofing).
func TestRenderAdaptersTable_ConfiguredLabel(t *testing.T) {
	entries := []adapterEntry{
		{Command: "create", Name: "hypothetical", Tier: 2, Tool: "echo hi", Mode: "sync", Default: false, Configured: true},
	}
	var buf strings.Builder
	renderAdaptersTable(&buf, entries, false)
	out := buf.String()
	assert.Contains(t, out, "configured",
		"table must show 'configured' label for Configured entries")
}

// ENH-160/ENH-163: JSON output includes the configured field (schema preserved).
// After ENH-163, Mode 3 execute defaults carry Default=true, not Configured=true.
func TestRunListAdapters_JSON_ConfiguredField(t *testing.T) {
	entries := []adapterEntry{
		{Command: "execute", Name: "manual-execute", Tier: 0, Tool: "(built-in)", Mode: "sync", Default: true, Configured: false},
		{Command: "create", Name: "manual-create", Tier: 0, Tool: "(built-in)", Mode: "sync", Default: true, Configured: false},
	}
	var buf strings.Builder
	err := runListAdapters(&buf, entries, true, false)
	require.NoError(t, err)

	var result []adapterEntry
	err = json.Unmarshal([]byte(buf.String()), &result)
	require.NoError(t, err)

	for _, e := range result {
		if e.Name == "manual-execute" {
			assert.True(t, e.Default, "JSON default field should be true for Mode 3 execute default")
			assert.False(t, e.Configured, "JSON configured field should be false")
		}
	}
}

func TestListCmd_Help(t *testing.T) {
	cmd := newListCmd()
	assert.Equal(t, "list <adapters|frameworks|all>", cmd.Use)
	assert.Contains(t, cmd.Long, "adapters")
	assert.Contains(t, cmd.Long, "frameworks")
}

// indexOf returns the index of s in slice, or -1.
func indexOf(slice []string, s string) int {
	for i, v := range slice {
		if v == s {
			return i
		}
	}
	return -1
}
