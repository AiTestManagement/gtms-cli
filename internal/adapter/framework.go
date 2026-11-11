package adapter

// ResolveFramework determines the framework value using a three-level precedence chain:
//  1. Explicit --framework CLI flag (rare override)
//  2. framework field in adapter config (normal case)
//  3. Adapter name (last-resort fallback for backward compatibility)
//
// BUG-210: a reserved Mode 3 execute adapter (manual-execute, agent-execute,
// manual-execute-script, agent-execute-script) always records manual evidence.
// Its framework is GTMS-owned, so it resolves to "manual" ahead of the chain
// above. Admission (cli/execute.go via ValidateReservedMode3Framework) has
// already refused any non-manual --framework flag or configured framework for
// these adapters, so the only values reaching here are manual/omitted; forcing
// "manual" also fixes the omitted-framework case, where the chain would
// otherwise fall through to the adapter name (e.g. "manual-execute-script").
func ResolveFramework(resolved *ResolvedAdapter, flagFramework string) string {
	if isReservedMode3Execute(resolved) {
		return "manual"
	}
	if flagFramework != "" {
		return flagFramework
	}
	if resolved.Config.Framework != "" {
		return resolved.Config.Framework
	}
	return resolved.Name
}

// IsManualFramework reports whether the resolved adapter is the manual-execute
// adapter path — the actor that owns missing-artefact handling (path-aware
// "Run 'gtms prime --framework manual'" hint, status:error handoff, error/
// task placement). Callers use this to decide whether to skip the generic
// artefact pre-check in cli/execute.go.
//
// Adapter first, framework second: the predicate keys exclusively on the
// resolved adapter — framework strings on the CLI flag or on-disk automation
// record never stand alone. Concrete failure case the rule defends against:
//
//	# Non-minimal preset (claude/github), default execute adapter is
//	# local-runner / github-actions:
//	gtms execute tc-X --framework manual
//
// The flag yields a "manual" framework string, but resolution still returns
// the preset's non-manual default. A loose rule that trusted the flag would
// skip the pre-check and invoke a non-manual adapter with an empty
// ArtefactFile — bypassing the pre-check that exists precisely to catch that.
//
// True iff:
//   - resolved.Name == "manual-execute", OR
//   - resolved.Config != nil && resolved.Config.Framework == "manual"
//
// resolved may be nil (returns false) and resolved.Config may be nil (only
// the Name branch is consulted in that case).
// isReservedMode3Execute reports whether resolved is a reserved Mode 3 adapter
// selected for the EXECUTE command. BUG-210 / REV-272 CODEX-003: the four
// reserved names are manual-only and synchronous-only ONLY under execute; an
// adapter with the same name registered under create/automate/prime is ordinary.
// Framework resolution, manual-context recognition and the runtime normalisation
// in handleSyncResult all key on the command as well as the name, so a reserved
// name cannot rewrite an automate/prime handoff or its attribution. Admission in
// cli/execute.go is execute-only by construction. IsMode3ExecuteAdapterName stays
// a pure name check for the CLI dispatch callers that already know the command.
func isReservedMode3Execute(resolved *ResolvedAdapter) bool {
	return resolved != nil && resolved.Command == "execute" && IsMode3ExecuteAdapterName(resolved.Name)
}

func IsManualFramework(resolved *ResolvedAdapter) bool {
	if resolved == nil {
		return false
	}
	// ENH-150: agent-execute shares the manual-execute implementation on day one.
	// BUG-210: all four reserved Mode 3 execute adapters are manual-only, so the
	// two script variants (manual-execute-script, agent-execute-script) are
	// recognised too. This is what makes a custom script definition with no
	// "framework: manual" setting populate manual context (populateManualExecute
	// Fields) instead of failing late with missing GTMS_RESULT_* env vars.
	// REV-272 CODEX-003: recognition is scoped to the EXECUTE command (and still
	// keyed on the resolved adapter, never the CLI flag), so an adapter with one of
	// these names registered under create/automate/prime stays ordinary, and the
	// adapter-first boundary (tc-2838d632) holds.
	if isReservedMode3Execute(resolved) {
		return true
	}
	if resolved.Config != nil && resolved.Config.Framework == "manual" {
		return true
	}
	return false
}
