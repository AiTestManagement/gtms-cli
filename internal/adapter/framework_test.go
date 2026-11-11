package adapter

import (
	"testing"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestResolveFramework_FlagWins(t *testing.T) {
	resolved := &ResolvedAdapter{
		Name:   "claude-bats",
		Config: &config.AdapterConfig{Framework: "bats"},
	}
	result := ResolveFramework(resolved, "custom")
	assert.Equal(t, "custom", result)
}

func TestResolveFramework_ConfigWins(t *testing.T) {
	resolved := &ResolvedAdapter{
		Name:   "claude-bats",
		Config: &config.AdapterConfig{Framework: "bats"},
	}
	result := ResolveFramework(resolved, "")
	assert.Equal(t, "bats", result)
}

func TestResolveFramework_AdapterNameFallback(t *testing.T) {
	resolved := &ResolvedAdapter{
		Name:   "my-adapter",
		Config: &config.AdapterConfig{},
	}
	result := ResolveFramework(resolved, "")
	assert.Equal(t, "my-adapter", result)
}

// BUG-210: a reserved Mode 3 execute adapter always resolves to "manual",
// ahead of the flag and config, including the omitted-framework custom script
// case that previously fell through to the adapter name.
func TestResolveFramework_ReservedMode3AlwaysManual(t *testing.T) {
	for _, name := range []string{"manual-execute", "agent-execute", "manual-execute-script", "agent-execute-script"} {
		// Omitted framework, no config framework (custom script definition).
		resolved := &ResolvedAdapter{Command: "execute", Name: name, Config: &config.AdapterConfig{}}
		assert.Equal(t, "manual", ResolveFramework(resolved, ""), "%s omitted-framework must resolve to manual", name)
		// --framework manual.
		assert.Equal(t, "manual", ResolveFramework(resolved, "manual"), "%s --framework manual", name)
		// Configured framework: manual (scaffolded).
		resolvedCfg := &ResolvedAdapter{Command: "execute", Name: name, Config: &config.AdapterConfig{Framework: "manual"}}
		assert.Equal(t, "manual", ResolveFramework(resolvedCfg, ""), "%s config manual", name)
	}
}

// REV-272 CODEX-003: reserved-name behaviour is command-scoped. An adapter with a
// reserved execute name registered under create/automate/prime is ordinary -- its
// framework resolves normally and it is not recognised as a manual path. Fails on
// the previous code, which keyed on the name alone.
func TestReservedMode3_CommandScoped(t *testing.T) {
	for _, cmd := range []string{"create", "automate", "prime"} {
		for _, name := range []string{"manual-execute", "agent-execute", "manual-execute-script", "agent-execute-script"} {
			// A same-named non-execute adapter with its own framework keeps it.
			r := &ResolvedAdapter{Command: cmd, Name: name, Config: &config.AdapterConfig{Framework: "playwright"}}
			assert.Equal(t, "playwright", ResolveFramework(r, ""),
				"%s/%s: non-execute reserved-name adapter must keep its configured framework", cmd, name)
			assert.False(t, IsManualFramework(r),
				"%s/%s: non-execute reserved-name adapter must not be a manual path", cmd, name)

			// With no framework configured, it falls back to the adapter name, not "manual".
			rn := &ResolvedAdapter{Command: cmd, Name: name, Config: &config.AdapterConfig{}}
			assert.Equal(t, name, ResolveFramework(rn, ""),
				"%s/%s: non-execute reserved-name adapter must fall back to its name", cmd, name)
			assert.False(t, IsManualFramework(rn), "%s/%s: still not a manual path", cmd, name)
		}
	}
}

// --- IsManualFramework: the two script variants (BUG-210) ---

func TestIsManualFramework_ReservedScriptVariants(t *testing.T) {
	for _, name := range []string{"manual-execute-script", "agent-execute-script"} {
		// No config framework -- the custom-definition case that returned false
		// before BUG-210 and left manual context unpopulated. Command is execute.
		assert.True(t, IsManualFramework(&ResolvedAdapter{Command: "execute", Name: name, Config: &config.AdapterConfig{}}),
			"%s with no config framework must be recognised as manual", name)
		// Scaffolded config framework: manual.
		assert.True(t, IsManualFramework(&ResolvedAdapter{Command: "execute", Name: name, Config: &config.AdapterConfig{Framework: "manual"}}),
			"%s with config framework manual must be recognised as manual", name)
	}
}

// --- IsManualFramework tests ---
//
// The predicate keys exclusively on the *resolved adapter*. The signature
// deliberately takes nothing else — framework strings on the CLI flag or
// on-disk automation record cannot stand alone. The "adapter first,
// framework second" boundary holds: the generic artefact pre-check still
// fires for non-manual adapters even when the framework flag/record happens
// to say "manual".

func TestIsManualFramework_AdapterNameManualExecute(t *testing.T) {
	// Resolved adapter is literally named "manual-execute" — the manual
	// path owns the lifecycle.
	resolved := &ResolvedAdapter{
		Command: "execute",
		Name:    "manual-execute",
		Config:  &config.AdapterConfig{},
	}
	assert.True(t, IsManualFramework(resolved))
}

func TestIsManualFramework_AdapterNameManualExecute_OptInOnNonMinimal(t *testing.T) {
	// User explicitly opts in via `--adapter manual-execute` on a
	// non-minimal preset. The on-disk automation record was authored
	// against a different framework (e.g. bats). The resolved adapter
	// is still manual-execute → predicate must be true.
	resolved := &ResolvedAdapter{
		Name:   "manual-execute",
		Config: &config.AdapterConfig{Framework: "manual"},
	}
	assert.True(t, IsManualFramework(resolved))
}

func TestIsManualFramework_ConfigFrameworkManual(t *testing.T) {
	// Adapter named anything but with framework: manual in its config.
	// This is the natural minimal-preset path: `gtms execute tc-X` with
	// no flag, default adapter has framework: manual.
	resolved := &ResolvedAdapter{
		Name:   "some-other-name",
		Config: &config.AdapterConfig{Framework: "manual"},
	}
	assert.True(t, IsManualFramework(resolved))
}

func TestIsManualFramework_NonManualAdapter(t *testing.T) {
	// The user's failure case: non-minimal preset (claude/github) with
	// default execute adapter local-runner. User runs
	// `gtms execute tc-X --framework manual` (no --adapter). The resolved
	// adapter is NOT manual-execute and its config framework isn't manual,
	// so the predicate is false even though the CLI flag and on-disk
	// record both say "manual" — those signals never reach this helper.
	resolved := &ResolvedAdapter{
		Name:   "local-runner",
		Config: &config.AdapterConfig{Framework: "playwright"},
	}
	assert.False(t, IsManualFramework(resolved))
}

func TestIsManualFramework_NilResolvedSafe(t *testing.T) {
	// Helper must not panic when called before the adapter is resolved.
	assert.False(t, IsManualFramework(nil))
}

func TestIsManualFramework_NilConfigSafe(t *testing.T) {
	// Helper must not panic when ResolvedAdapter has no Config (defensive).
	// The reserved-name branch still trips for an execute manual-execute.
	resolved := &ResolvedAdapter{Command: "execute", Name: "manual-execute", Config: nil}
	assert.True(t, IsManualFramework(resolved))

	resolved = &ResolvedAdapter{Command: "execute", Name: "playwright-runner", Config: nil}
	assert.False(t, IsManualFramework(resolved))
}
