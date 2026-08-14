package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/config"
)

// TestAutomate_BulkTier1NoFramework_DoesNotPreflightRefuse pins the BUG-171
// follow-up boundary: the bulk validate-once missing-runner preflight fires only
// for the Tier 0 built-in automate path. A Tier 1 (command) automate adapter that
// declares no `framework:` resolves its effective framework by falling back to
// the adapter NAME ("mock" here), which matches no configured execute adapter --
// but the adapter is about to run and iterate normally, so the batch must not be
// refused before the loop.
//
// Regression: the original gate keyed only on `framework != "" && != "manual"`,
// so it fired for this fallback name and broke recursive-bulk-automate-execute.
//
// This asserts only the pre-run boundary. It makes NO claim that Tier 1/2
// adapters are exempt from canonical execute-adapter resolution in general
// (that may still be required later, at wiring time, after an artefact exists).
func TestAutomate_BulkTier1NoFramework_DoesNotPreflightRefuse(t *testing.T) {
	skipIfShort(t)
	root := setupCompatProject(t)

	// One discoverable TC in a folder so bulk mode has something to iterate.
	folder := filepath.Join(root, "gtms/test/cases/iterate")
	require.NoError(t, os.MkdirAll(folder, 0o755))
	spec := `---
test_case_id: tc-aaaa1111
title: First
---

## Steps
1. do
`
	require.NoError(t, os.WriteFile(filepath.Join(folder, "tc-aaaa1111-first.md"), []byte(spec), 0o644))

	// Tier 1 automate adapter with NO framework field -> ResolveFramework falls
	// back to the adapter name "mock". No execute adapter is configured for it.
	cfg := &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"automate": {
				"mock": {Mode: "sync", Command: "echo done"},
			},
		},
		Defaults: map[string]string{"automate": "mock"},
	}
	restore := withExecuteGlobals(t, root, cfg)
	defer restore()

	out, _ := runCmdCaptureStderr(t, newAutomateCmd(), "iterate")

	// The Tier 0-only preflight must NOT have fired: neither its capitalized
	// headline nor its unique manual-prime hint may appear.
	assert.NotContains(t, out, "No execute adapter configured for framework",
		"the Tier 0 validate-once preflight must not refuse a Tier 1 bulk run")
	assert.NotContains(t, out, "manual prime path",
		"the automate missing-runner guidance (preflight-only) must not appear")

	// Proof the command entered the per-TC loop rather than exiting before it:
	// the per-TC progress line names the discovered TC on stderr.
	assert.Contains(t, out, "tc-aaaa1111",
		"bulk automate should have iterated over the discovered test case")
}

// TestAutomate_BulkUnsupportedFramework_ReportsUnsupportedNotAddRunner pins
// REV-125 CODEX-001 at the CLI: for the Tier 0 built-in automate path, a bulk
// request with a syntactically valid but UNSUPPORTED framework (cypress) must
// stop once before iterating with the established unsupported-framework
// diagnostic -- NOT missing-runner / add-a-runner advice, which would mislead
// (adding a cypress runner cannot make the built-in path work).
func TestAutomate_BulkUnsupportedFramework_ReportsUnsupportedNotAddRunner(t *testing.T) {
	skipIfShort(t)
	root := setupCompatProject(t)

	folder := filepath.Join(root, "gtms/test/cases/unsupported")
	require.NoError(t, os.MkdirAll(folder, 0o755))
	spec := `---
test_case_id: tc-44440000
title: First
---

## Steps
1. do
`
	require.NoError(t, os.WriteFile(filepath.Join(folder, "tc-44440000-first.md"), []byte(spec), 0o644))

	// No execute adapters at all: proves the support check wins the ordering.
	cfg := &config.Config{}
	restore := withExecuteGlobals(t, root, cfg)
	defer restore()

	out, err := runCmdCaptureStderr(t, newAutomateCmd(), "unsupported", "--framework", "cypress", "--adapter", "agent-automate")
	require.Error(t, err)

	// Established unsupported-framework diagnostic, exactly the message
	// BuiltinAutomate emits.
	assert.Contains(t, out, `no automate support found for framework "cypress"`,
		"bulk must surface the established unsupported-framework diagnostic")

	// The misdiagnosis being guarded against must be ABSENT: no add-a-runner /
	// missing-runner advice, and no BUG-171 manual prime-path guidance.
	assert.NotContains(t, out, "execute adapter",
		"an unsupported framework must not be reported as a missing-runner problem")
	assert.NotContains(t, out, "manual prime path",
		"automate's missing-runner prime hint must not appear for an unsupported framework")

	// Validate-once: it stopped before iteration; the TC was not processed.
	assert.NotContains(t, out, "tc-44440000",
		"bulk must stop before iterating; no per-TC progress for an unsupported framework")
}
