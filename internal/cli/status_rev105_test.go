package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStatusDetail_RunnerLineScopedToSelectedFramework is the REV-105 CLAUDE-001
// regression. On a TC wired under two frameworks that both have a terminal
// result, the ENH-191 Runner-provenance line must be scoped to the framework
// the EXECUTE line represents (the selected framework) -- not render a second,
// unlabeled Runner line for the OTHER framework's runner, which misattributes
// execution provenance. The pre-fix loop iterated detail.Frameworks unfiltered.
func TestStatusDetail_RunnerLineScopedToSelectedFramework(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, filepath.Join("gtms/test/cases", "tc-mf000001-multi.md"), `---
test_case_id: tc-mf000001
title: Multi-framework runner provenance
---
`)
	// Wired + executed under two frameworks, each with a distinct runner name.
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-mf000001", Framework: "bats", Adapter: "bats-runner", Result: "pass"})
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-mf000001", Framework: "playwright", Adapter: "pw-runner", Result: "pass"})

	var buf bytes.Buffer
	// Text detail (jsonOut=false), selecting the bats framework.
	require.NoError(t, runStatusDetail(&buf, root, "tc-mf000001", false, "bats", false))
	out := buf.String()

	assert.Contains(t, out, "Runner: bats-runner",
		"the selected framework's runner must be shown")
	assert.NotContains(t, out, "pw-runner",
		"the other framework's runner must NOT leak into the bats detail view (REV-105 CLAUDE-001)")
	assert.Equal(t, 1, strings.Count(out, "Runner:"),
		"exactly one Runner line -- for the selected framework's result on display")
}
