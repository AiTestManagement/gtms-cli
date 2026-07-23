package adapter

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
