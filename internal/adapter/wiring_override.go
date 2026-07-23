package adapter

import (
	"fmt"

	"github.com/aitestmanagement/gtms-cli/internal/config"
)

// ResolveWiringExecuteAdapter resolves and validates an adapter for use as a
// wiring-driven execute override (ENH-191). The five decided properties:
//
//  1. Resolves only from adapters.execute.
//  2. Rejects Mode 3 reserved names (they are dispatched before wiring lookup
//     and never enter the override branch).
//  3. Computes the inherent effective framework via ResolveFramework(resolved, "")
//     -- empty flag argument, so the result is config framework or adapter-name
//     fallback.
//  4. Validates an explicit --framework when supplied: it must equal the inherent
//     framework (assertion, not override). Mismatch is a pre-task error.
//  5. Never inspects or resolves the old wired adapter.
//
// Returns: (resolved adapter, effective framework, error).
// The effective framework is the inherent framework (property 3) and serves as
// the wiring selector for implied-framework dispatch.
func ResolveWiringExecuteAdapter(cfg *config.Config, adapterName, explicitFramework string) (*ResolvedAdapter, string, error) {
	if adapterName == "" {
		return nil, "", fmt.Errorf("adapter name is required")
	}

	// Property 2: reject Mode 3 reserved names.
	if IsMode3ExecuteAdapterName(adapterName) {
		return nil, "", fmt.Errorf(
			"adapter %q is a Mode 3 prime-path adapter and cannot be used as a wiring override"+
				" -- Mode 3 adapters are dispatched before wiring lookup",
			adapterName)
	}

	// Property 1: resolve only from adapters.execute.
	resolved, err := Resolve(cfg, "execute", adapterName)
	if err != nil {
		return nil, "", fmt.Errorf("adapter %q is not configured under adapters.execute: %w", adapterName, err)
	}

	// Property 3: inherent effective framework (empty flag argument).
	inherentFramework := ResolveFramework(resolved, "")

	// Property 4: explicit --framework must equal inherent framework.
	if explicitFramework != "" && explicitFramework != inherentFramework {
		return nil, "", fmt.Errorf(
			"--framework %q does not match adapter %q (inherent framework: %q)"+
				" -- the flag asserts the adapter's framework, it cannot override it",
			explicitFramework, adapterName, inherentFramework)
	}

	return resolved, inherentFramework, nil
}
