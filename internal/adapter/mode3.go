package adapter

import "fmt"

// IsMode3ExecuteAdapterName reports whether the given execute adapter
// name is one of the four Mode 3 execute adapters. These are dispatched
// by name before wiring lookup at execute time (internal/cli/execute.go),
// so they bypass wiring resolution entirely. The Tier-0 names
// (manual-execute, agent-execute) read a filled result template; the
// Tier-2 names (manual-execute-script, agent-execute-script) run their
// configured scripts. Both pairs share the property that wiring cannot
// select them -- they are never canonical wiring runners.
//
// This is intentionally a name-based predicate, distinct from
// IsManualFramework. The wiring-bypass decision happens before
// the adapter is resolved, so the CLI cannot ask the resolved adapter
// what it is. If a future enhancement adds another Mode 3 execute
// adapter, update this list alongside the adapter registration --
// otherwise the new adapter will fall through to wiring lookup and
// fail with "No wiring records found".
//
// ENH-191: exported so that both the CLI dispatch (execute.go) and
// the adapter layer (resolver exclusion, override validation) can
// share one authoritative list.
func IsMode3ExecuteAdapterName(name string) bool {
	return name == "manual-execute" ||
		name == "agent-execute" ||
		name == "manual-execute-script" ||
		name == "agent-execute-script"
}

// ValidateReservedMode3Framework enforces the BUG-210 manual-only AND
// synchronous-only contract for a SELECTED reserved Mode 3 execute adapter. Call
// it only when the chosen execute adapter IsMode3ExecuteAdapterName (selected
// explicitly via --adapter or through defaults.execute), and call it before any
// wiring lookup, task, handoff or execution record is created and before the
// adapter is invoked.
//
// These four adapters always record manual evidence and always run synchronously,
// so a non-manual framework -- whether passed as --framework or set as the
// adapter's configured framework -- and a configured mode: async are refused.
// Omitting --framework, or passing --framework manual, is allowed (configFramework
// empty or "manual"); mode must be "sync" (an omitted mode is already rejected at
// config-load by validateAdapter, so it never reaches here as "").
//
// Precedence (owner-approved, BUG-210 Approved Contract):
//  1. A non-manual explicit flag gives the flag diagnostic, even if the configuration
//     is also non-manual.
//  2. Otherwise, a non-manual configured framework gives the configuration (framework)
//     diagnostic. So --framework manual with framework: fx still gives this one.
//  3. Otherwise, a configured mode: async gives the configuration (mode) diagnostic.
//
// The returned (message, hint) pair is designed for output.Errorf, which prefixes the
// error glyph and indents the hint, reproducing the exact two-line diagnostics the
// specs assert. refused is false (with empty message/hint) when the run is permitted.
// This is a pure predicate with no I/O, so it never validates at config-load time.
func ValidateReservedMode3Framework(adapterName, flagFramework, configFramework, configMode string) (message, hint string, refused bool) {
	if flagFramework != "" && flagFramework != "manual" {
		return fmt.Sprintf("Adapter '%s' records manual results and cannot run under framework '%s'.", adapterName, flagFramework),
			fmt.Sprintf("Omit --framework, or use --framework manual. To run the %s automation, use its adapter.", flagFramework),
			true
	}
	if configFramework != "" && configFramework != "manual" {
		return fmt.Sprintf("Adapter '%s' records manual results but gtms.config sets framework '%s'.", adapterName, configFramework),
			"Remove the framework setting or set it to manual.",
			true
	}
	if configMode == "async" {
		return fmt.Sprintf("Adapter '%s' records manual results but gtms.config sets mode '%s'.", adapterName, configMode),
			"Set its mode to sync.",
			true
	}
	return "", "", false
}
