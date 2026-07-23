package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBuildFrameworkEntries_FrameworklessGroupUnderNone pins the REV-105
// CLAUDE-002 REVISED disposition: `gtms list frameworks` groups adapters by
// their DECLARED framework, so an adapter with empty `framework:` -- create,
// automate, OR execute -- appears under (none). The name-fallback effective
// framework is an internal wiring-resolution detail surfaced via
// `canonical_for_wiring` on `gtms list adapters`, not in this inventory view.
// Applying the fallback here produced a confusing synthetic "<name> framework"
// row and broke the documented (none) grouping (tc-6391c031); the two views
// intentionally differ.
func TestBuildFrameworkEntries_FrameworklessGroupUnderNone(t *testing.T) {
	entries := []adapterEntry{
		{Command: "execute", Name: "custom-runner", Tier: 2, Tool: "adapters/custom.sh", Framework: "", Mode: "sync"},
		{Command: "execute", Name: "agent-execute", Tier: 0, Tool: "(built-in)", Framework: "", Mode: "sync"},
		{Command: "automate", Name: "some-automate", Tier: 2, Tool: "adapters/a.sh", Framework: "", Mode: "sync"},
		{Command: "execute", Name: "bats-runner", Tier: 2, Tool: "adapters/b.sh", Framework: "bats", Mode: "sync"},
	}
	fwEntries := buildFrameworkEntries(entries)

	fwMap := make(map[string][]string)
	for _, e := range fwEntries {
		fwMap[e.Framework] = e.Adapters
	}

	// Empty-framework EXECUTE adapter groups under (none) -- NOT a synthetic
	// "custom-runner" framework row.
	assert.NotContains(t, fwMap, "custom-runner",
		"a framework-less execute adapter must not create a synthetic framework row")
	assert.Contains(t, fwMap["(none)"], "execute:custom-runner",
		"a framework-less execute adapter groups under (none)")

	// Framework-less create/automate/Mode-3 adapters also stay under (none).
	assert.Contains(t, fwMap["(none)"], "execute:agent-execute")
	assert.Contains(t, fwMap["(none)"], "automate:some-automate")

	// A declared-framework adapter still groups under its framework.
	assert.Contains(t, fwMap["bats"], "execute:bats-runner")
}
