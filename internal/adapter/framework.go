package adapter

// ResolveFramework determines the framework value using a three-level precedence chain:
//  1. Explicit --framework CLI flag (rare override)
//  2. framework field in adapter config (normal case)
//  3. Adapter name (last-resort fallback for backward compatibility)
func ResolveFramework(resolved *ResolvedAdapter, flagFramework string) string {
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
func IsManualFramework(resolved *ResolvedAdapter) bool {
	if resolved == nil {
		return false
	}
	// ENH-150: agent-execute shares the manual-execute implementation on day one.
	if resolved.Name == "manual-execute" || resolved.Name == "agent-execute" {
		return true
	}
	if resolved.Config != nil && resolved.Config.Framework == "manual" {
		return true
	}
	return false
}
