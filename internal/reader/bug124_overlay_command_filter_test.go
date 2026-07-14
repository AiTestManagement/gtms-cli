package reader

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// BUG-124: the reader's terminal-result overlay (scanTerminalResults) joined
// handoffs by (testcase, framework) filtered only on status (complete|error),
// with no command filter. So an automate-success handoff (command: automate,
// status: complete, result: pass) was surfaced as the EXECUTE result -- a
// false-green pass on a framework TC that was automated but never executed.
// The fix adds a `command == "execute"` gate. These tests pin the contract
// both ways: automate handoffs must NOT surface as execute results, and
// genuine execute results (error and pass) MUST still surface.

// TestBUG124_AutomateHandoffNotShownAsExecuteResult is the bug: an automate
// handoff on a pending (never-executed) wiring must not render as EXECUTE.
func TestBUG124_AutomateHandoffNotShownAsExecuteResult(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join("gtms/test/cases", "demo", "tc-aaa1111-pw.md"), `---
test_case_id: tc-aaa1111
title: Playwright TC
---
`)
	// Automated but never executed: pending wiring + a command:automate
	// complete/pass handoff (the exact shape `gtms automate` writes).
	seedLegacyRecord(t, root, legacyRecord{
		TC:           "tc-aaa1111",
		Framework:    "playwright",
		Command:      "automate",
		Result:       "pass",
		ArtefactHash: wiring.PendingArtefactHash,
	})

	detail, err := PipelineDetail(root, "tc-aaa1111", "", false)
	require.NoError(t, err)
	require.NotNil(t, detail)
	assert.Equal(t, "none", detail.ExecuteStatus, "automate handoff must not surface as EXECUTE complete")
	assert.Equal(t, "none", detail.LastResult, "automate handoff must not surface as a pass result")

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Len(t, entries[0].Frameworks, 1)
	fe := entries[0].Frameworks[0]
	assert.Equal(t, "pending", fe.WiringBootstrap, "wiring is still pending (never executed)")
	assert.Equal(t, "", fe.LastResultHere, "no execute result on a never-executed TC")
	assert.Equal(t, "", fe.LastStatusHere, "no execute status on a never-executed TC")
}

// TestBUG124_ExecuteErrorStillSurfaces guards the demo-critical property: a
// genuine execute that errored (e.g. missing Playwright/Node tooling) writes
// command:execute, status:error, and must remain visible on the dashboard.
// This is why the suppress-on-pending alternative was rejected.
func TestBUG124_ExecuteErrorStillSurfaces(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join("gtms/test/cases", "demo", "tc-bbb2222-bats.md"), `---
test_case_id: tc-bbb2222
title: Bats TC
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:           "tc-bbb2222",
		Framework:    "bats",
		AdapterError: true, // command:execute, status:error, no result
	})

	detail, err := PipelineDetail(root, "tc-bbb2222", "", false)
	require.NoError(t, err)
	require.NotNil(t, detail)
	assert.Equal(t, "error", detail.ExecuteStatus, "a real execute error must remain visible")
	assert.Equal(t, "error", detail.LastResult)
}

// TestBUG124_ExecutePassStillSurfaces guards against over-suppression: a
// genuine execute pass (command:execute, complete, pass) must still render.
func TestBUG124_ExecutePassStillSurfaces(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, filepath.Join("gtms/test/cases", "demo", "tc-ccc3333-bats.md"), `---
test_case_id: tc-ccc3333
title: Bats TC
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-ccc3333",
		Framework: "bats",
		Result:    "pass", // command defaults to execute
	})

	detail, err := PipelineDetail(root, "tc-ccc3333", "", false)
	require.NoError(t, err)
	require.NotNil(t, detail)
	assert.Equal(t, "complete", detail.ExecuteStatus, "a real execute pass must still surface")
	assert.Equal(t, "pass", detail.LastResult)
}
