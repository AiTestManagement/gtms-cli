// Package adapter handles adapter resolution, tier computation, and invocation.
package adapter

import (
	"fmt"

	"github.com/aitestmanagement/gtms-cli/internal/config"
)

// ResolvedAdapter is the output of the resolution phase (Phase 3).
// It identifies which adapter will handle a command invocation.
type ResolvedAdapter struct {
	Command string               // the command that triggered resolution (e.g. "create")
	Name    string               // adapter instance name (e.g. "local-claude")
	Config  *config.AdapterConfig // from gtms.config
	Tier    int                  // 1=Command, 2=Script, 3=Module, 0=built-in
	Mode    string               // "async" or "sync"
}

// visibilityCommands are commands that fall back to the built-in adapter
// when no adapter is configured.
var visibilityCommands = map[string]bool{
	"status": true,
	"gaps":   true,
	"triage": true,
}

// Resolve determines which adapter to use for a given command.
//
// Resolution order:
//  1. If adapterFlag is non-empty, look up that name under cfg.Adapters[command]
//  2. Otherwise, look up cfg.Defaults[command], then find that adapter
//  3. For visibility commands (status, gaps, triage), fall back to built-in
//  4. If nothing found, return an error listing available adapters
func Resolve(cfg *config.Config, command string, adapterFlag string) (*ResolvedAdapter, error) {
	var name string

	if adapterFlag != "" {
		name = adapterFlag
	} else if defaultName, ok := cfg.Defaults[command]; ok {
		name = defaultName
	} else if visibilityCommands[command] {
		return builtinAdapter(command), nil
	} else {
		return nil, fmt.Errorf(
			"No default adapter configured for '%s'. Available adapters: %s",
			command, cfg.AdapterNamesString(command),
		)
	}

	// Look up the adapter in config
	adapters, ok := cfg.Adapters[command]
	if !ok {
		if visibilityCommands[command] {
			return builtinAdapter(command), nil
		}
		return nil, fmt.Errorf(
			"No adapter '%s' registered for '%s'. Available adapters: (none)",
			name, command,
		)
	}

	ac, ok := adapters[name]
	if !ok {
		return nil, fmt.Errorf(
			"No adapter '%s' registered for '%s'. Available adapters: %s",
			name, command, cfg.AdapterNamesString(command),
		)
	}

	return &ResolvedAdapter{
		Command: command,
		Name:    name,
		Config:  ac,
		Tier:    computeTier(ac),
		Mode:    ac.Mode,
	}, nil
}

// computeTier determines the adapter tier based on which config field is set.
func computeTier(ac *config.AdapterConfig) int {
	switch {
	case ac.Command != "":
		return 1
	case ac.Script != "":
		return 2
	case ac.Module != "":
		return 3
	default:
		return 0 // built-in
	}
}

// builtinAdapter returns a ResolvedAdapter for the built-in reader.
func builtinAdapter(command string) *ResolvedAdapter {
	return &ResolvedAdapter{
		Command: command,
		Name:    "built-in",
		Config:  &config.AdapterConfig{Mode: "sync"},
		Tier:    0,
		Mode:    "sync",
	}
}
