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
		Name:   "manual-execute",
		Config: &config.AdapterConfig{},
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
	// The Name branch still trips for manual-execute.
	resolved := &ResolvedAdapter{Name: "manual-execute", Config: nil}
	assert.True(t, IsManualFramework(resolved))

	resolved = &ResolvedAdapter{Name: "playwright-runner", Config: nil}
	assert.False(t, IsManualFramework(resolved))
}
