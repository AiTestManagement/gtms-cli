package adapter

import (
	"fmt"

	"github.com/aitestmanagement/gtms-cli/internal/config"
)

// Tier 0 built-in automate prerequisites (BUG-171 / REV-125 CODEX-001).
//
// The Tier 0 built-in automate path (BuiltinAutomate) has two hard
// preconditions that it checks, in a FIXED ORDER, before it stamps any
// skeleton or wiring:
//
//  1. framework support -- LookupFrameworkSupport(framework) must be non-nil
//     (only bats / playwright are registered). builtin_action.go checks this
//     FIRST.
//  2. canonical execute adapter -- ResolveCanonicalExecuteAdapter(framework)
//     must find a runner. builtin_action.go checks this SECOND.
//
// Order matters: an unsupported framework can never be made to work by adding
// an execute adapter, so the support failure must win. CheckTier0AutomatePrereqs
// centralises exactly that ordering so the bulk automate CLI can validate ONCE
// before iterating and produce the SAME diagnostic the single-TC invoke would,
// without the CLI knowing anything about adapter tiers.

// UnsupportedAutomateFrameworkError is returned when the requested framework
// has no registered automate support. Its message is byte-identical to the
// diagnostic BuiltinAutomate emits (builtin_action.go), so promoting it to a
// typed error changes no user-facing text -- it only lets a caller match the
// cause via errors.As.
type UnsupportedAutomateFrameworkError struct {
	Framework string
}

func (e *UnsupportedAutomateFrameworkError) Error() string {
	// MUST stay byte-identical to the string historically emitted by
	// BuiltinAutomate (fmt.Errorf("no automate support found for framework %q",
	// framework)) so existing prefix assertions (Go + BATS) keep passing.
	return fmt.Sprintf("no automate support found for framework %q", e.Framework)
}

// CheckTier0AutomatePrereqs validates the Tier 0 built-in automate path's
// framework prerequisites in BuiltinAutomate's exact order, WITHOUT invoking
// the adapter. It lets the bulk automate CLI stop once, before iteration, with
// the correct diagnostic.
//
// Returns nil ("prereqs satisfied, or not applicable") when:
//   - resolved is not the Tier 0 built-in automate path (agent-automate /
//     manual-automate). This is where the resolved.Tier==0 knowledge lives, so
//     the CLI never has to branch on tier.
//   - framework is "" (a separate per-TC "no --framework specified" case) or
//     "manual" (short-circuits inside BuiltinAutomate; wiring-free).
//
// Returns a typed error when a real precondition fails:
//   - *UnsupportedAutomateFrameworkError when the framework has no registered
//     automate support (checked FIRST, so it wins over a missing runner).
//   - *MissingExecuteAdapterError when the framework IS supported but no
//     canonical execute adapter is configured (checked SECOND).
func CheckTier0AutomatePrereqs(cfg *config.Config, resolved *ResolvedAdapter, framework string) error {
	if resolved == nil || resolved.Tier != 0 || !isBuiltinAutomateAdapter(resolved.Name) {
		return nil
	}
	if framework == "" || framework == "manual" {
		return nil
	}
	// Order mirrors BuiltinAutomate: framework support FIRST, then canonical
	// execute adapter. Do not reorder -- an unsupported framework can never be
	// fixed by adding an execute adapter.
	if LookupFrameworkSupport(framework) == nil {
		return &UnsupportedAutomateFrameworkError{Framework: framework}
	}
	if _, _, err := ResolveCanonicalExecuteAdapter(cfg, framework); err != nil {
		return err // *MissingExecuteAdapterError propagates
	}
	return nil
}
