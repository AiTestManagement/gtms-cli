package adapter

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/config"
)

// tier0AutomateResolved is a Tier 0 built-in automate adapter (agent-automate),
// the path CheckTier0AutomatePrereqs guards.
func tier0AutomateResolved() *ResolvedAdapter {
	return &ResolvedAdapter{Command: "automate", Name: "agent-automate", Tier: 0, Mode: "sync"}
}

// TestCheckTier0AutomatePrereqs_UnsupportedBeforeRunner pins REV-125 CODEX-001:
// framework-support validation MUST precede canonical-runner resolution. An
// unsupported framework (e.g. cypress) returns the unsupported-framework error
// even when NO execute adapter exists -- adding a runner could never help, so
// the support failure wins.
func TestCheckTier0AutomatePrereqs_UnsupportedBeforeRunner(t *testing.T) {
	// No execute adapters configured at all: a missing runner is also true here,
	// so this proves the support check wins the ordering.
	cfg := &config.Config{}

	err := CheckTier0AutomatePrereqs(cfg, tier0AutomateResolved(), "cypress")
	require.Error(t, err)

	var unsupported *UnsupportedAutomateFrameworkError
	require.True(t, errors.As(err, &unsupported),
		"unsupported framework must win over missing runner; got %q", err.Error())
	assert.Equal(t, "cypress", unsupported.Framework)

	var mae *MissingExecuteAdapterError
	assert.False(t, errors.As(err, &mae),
		"must NOT be a missing-runner error for an unsupported framework")

	// Message is byte-identical to BuiltinAutomate's historical diagnostic.
	assert.Equal(t, `no automate support found for framework "cypress"`, err.Error())
}

// TestCheckTier0AutomatePrereqs_SupportedNoRunner: a SUPPORTED framework with no
// configured runner returns MissingExecuteAdapterError (the second check).
func TestCheckTier0AutomatePrereqs_SupportedNoRunner(t *testing.T) {
	// Supported framework (bats) but only a playwright runner configured.
	cfg := &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"pw-runner": {Framework: "playwright", Script: "run.sh", Mode: "sync"},
			},
		},
	}

	err := CheckTier0AutomatePrereqs(cfg, tier0AutomateResolved(), "bats")
	require.Error(t, err)

	var mae *MissingExecuteAdapterError
	require.True(t, errors.As(err, &mae),
		"supported framework without a runner must return the missing-runner error; got %q", err.Error())
	assert.Equal(t, "bats", mae.Framework)

	var unsupported *UnsupportedAutomateFrameworkError
	assert.False(t, errors.As(err, &unsupported),
		"a supported framework must not report as unsupported")
}

// TestCheckTier0AutomatePrereqs_SupportedWithRunner: supported framework with a
// matching runner -> nil (prereqs satisfied).
func TestCheckTier0AutomatePrereqs_SupportedWithRunner(t *testing.T) {
	cfg := &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"bats-runner": {Framework: "bats", Script: "run.sh", Mode: "sync"},
			},
		},
	}
	assert.NoError(t, CheckTier0AutomatePrereqs(cfg, tier0AutomateResolved(), "bats"))
}

// TestCheckTier0AutomatePrereqs_NotApplicable: the check is a no-op for a Tier 1
// adapter, for a non-automate-builtin name, and for framework ""/"manual".
func TestCheckTier0AutomatePrereqs_NotApplicable(t *testing.T) {
	cfg := &config.Config{}

	// Tier 1 command adapter whose framework fell back to its name -- must NOT
	// be pre-rejected (BUG-171 follow-up regression axis).
	tier1 := &ResolvedAdapter{Command: "automate", Name: "mock", Tier: 1, Mode: "sync"}
	assert.NoError(t, CheckTier0AutomatePrereqs(cfg, tier1, "mock"))

	// Tier 0 but not a built-in automate adapter name.
	notAutomate := &ResolvedAdapter{Command: "automate", Name: "some-other", Tier: 0, Mode: "sync"}
	assert.NoError(t, CheckTier0AutomatePrereqs(cfg, notAutomate, "cypress"))

	// framework "manual" short-circuits inside BuiltinAutomate.
	assert.NoError(t, CheckTier0AutomatePrereqs(cfg, tier0AutomateResolved(), "manual"))

	// framework "" is the separate "no --framework specified" per-TC case.
	assert.NoError(t, CheckTier0AutomatePrereqs(cfg, tier0AutomateResolved(), ""))

	// nil resolved.
	assert.NoError(t, CheckTier0AutomatePrereqs(cfg, nil, "cypress"))
}
