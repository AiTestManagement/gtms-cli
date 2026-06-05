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

// builtinActionAdapters is the closed set of built-in adapter names for action
// commands (ENH-150). When a name matches this table and no config entry exists,
// the resolver returns a Tier 0 built-in. Config-defined adapters always take
// precedence (steps 1-2 find them first).
var builtinActionAdapters = map[string]map[string]bool{
	"create":   {"agent-create": true, "manual-create": true},
	"automate": {"agent-automate": true, "manual-automate": true},
	"prime":    {"agent-prime": true, "manual-prime": true},
	"execute":  {"agent-execute": true, "manual-execute": true},
}

// builtinCommandDefaults maps commands to their default built-in adapter name
// when no flag and no config default exist. Only "prime" has a default built-in
// (manual-prime) — other commands require explicit selection.
var builtinCommandDefaults = map[string]string{
	"prime": "manual-prime",
}

// Resolve determines which adapter to use for a given command.
//
// Resolution order:
//  1. If adapterFlag is non-empty, look up that name under cfg.Adapters[command]
//  2. Otherwise, look up cfg.Defaults[command], then find that adapter
//  3. For visibility commands (status, gaps, triage), fall back to built-in
//  4. For action commands, fall back to built-in command default if one exists
//  5. If nothing found, return an error listing available adapters
//
// At steps 1-2, if the name is found in config, the config entry wins.
// If the name is NOT in config but IS in the built-in action adapter table,
// a Tier 0 built-in is returned. Unknown names still error.
func Resolve(cfg *config.Config, command string, adapterFlag string) (*ResolvedAdapter, error) {
	var name string

	if adapterFlag != "" {
		name = adapterFlag
	} else if defaultName, ok := cfg.Defaults[command]; ok {
		name = defaultName
	} else if visibilityCommands[command] {
		return builtinAdapterNamed(command, "built-in"), nil
	} else if defaultBuiltin, ok := builtinCommandDefaults[command]; ok {
		name = defaultBuiltin
	} else {
		return nil, fmt.Errorf(
			"No default adapter configured for '%s'. Available adapters: %s",
			command, cfg.AdapterNamesString(command),
		)
	}

	// Look up the adapter in config
	adapters, ok := cfg.Adapters[command]
	if ok {
		if ac, found := adapters[name]; found {
			return &ResolvedAdapter{
				Command: command,
				Name:    name,
				Config:  ac,
				Tier:    computeTier(ac),
				Mode:    ac.Mode,
			}, nil
		}
	}

	// Config lookup failed — check built-in fallbacks
	if visibilityCommands[command] {
		return builtinAdapterNamed(command, "built-in"), nil
	}

	// ENH-150: check the closed built-in action adapter table
	if commandBuiltins, hasCommand := builtinActionAdapters[command]; hasCommand {
		if commandBuiltins[name] {
			return builtinAdapterNamed(command, name), nil
		}
	}

	// Unknown name — error with available adapters
	if !ok {
		return nil, fmt.Errorf(
			"No adapter '%s' registered for '%s'. Available adapters: (none)",
			name, command,
		)
	}
	return nil, fmt.Errorf(
		"No adapter '%s' registered for '%s'. Available adapters: %s",
		name, command, cfg.AdapterNamesString(command),
	)
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

// builtinAdapterNamed returns a ResolvedAdapter for a built-in adapter.
// Visibility commands pass "built-in" as name; action commands pass the
// requested adapter name (e.g. "agent-create", "manual-prime").
func builtinAdapterNamed(command, name string) *ResolvedAdapter {
	return &ResolvedAdapter{
		Command: command,
		Name:    name,
		Config:  &config.AdapterConfig{Mode: "sync"},
		Tier:    0,
		Mode:    "sync",
	}
}

// IsBuiltinActionAdapter reports whether name is a known built-in action
// adapter for the given command. Used by config validation to allow
// defaults.X to reference built-in names that have no adapters.X entry.
func IsBuiltinActionAdapter(command, name string) bool {
	if commandBuiltins, ok := builtinActionAdapters[command]; ok {
		return commandBuiltins[name]
	}
	return false
}

// BuiltinCommandDefault returns the implicit built-in default adapter name
// the resolver falls back to when neither --adapter nor cfg.Defaults[command]
// selects one. Currently only "prime" has an implicit default (manual-prime).
// The second return value is false if the command has no implicit default.
func BuiltinCommandDefault(command string) (string, bool) {
	name, ok := builtinCommandDefaults[command]
	return name, ok
}
