package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/adapter"
	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/result"
)

// CON-023 / ENH-145 / ENH-146:
//
// shouldSkipExecute now consumes (root, tcID, *ResolvedAdapter, framework).
// Framework is the wiring-selected framework name and is used to scope
// the already-passing fast path so a pass under one framework cannot
// silently skip another framework wired to the same TC. The legacy
// automation-record + ignore-list parameters are gone because wiring
// (read-only) is the new authority and "already passing" is derived from
// .gtms/results/ overlay, not from the record's last formal result.
//
// These tests preserve the ENH-134 manual-bypass contract against the new
// signature and add per-framework scoping coverage (counting unit:
// `(testcase, framework)` per ENH-146).

// seedPassingResult writes a terminal handoff under .gtms/results/ for
// the given (TC, framework) with status: complete + result: pass so
// isAlreadyPassing returns true when called with the matching framework.
func seedPassingResult(t *testing.T, root, taskID, tcID, framework string) {
	t.Helper()
	adapter := framework + "-runner"
	if framework == "" {
		adapter = "bats-runner"
	}
	rc := &result.ResultContract{
		Task:      taskID,
		Command:   "execute",
		Target:    tcID,
		Adapter:   adapter,
		Mode:      "sync",
		Framework: framework,
		Created:   "2026-05-19T10:00:00Z",
		Status:    "complete",
		Result:    "pass",
		Completed: "2026-05-19T10:01:00Z",
	}
	dir := filepath.Join(root, ".gtms", "results")
	require.NoError(t, os.MkdirAll(dir, 0755))
	_, err := result.Create(root, rc)
	require.NoError(t, err)
}

// TestShouldSkipExecute_ManualAdapter_NeverSkips: manual adapter bypasses
// skip logic entirely so every TC is re-evaluated on every manual run
// (ENH-134 contract; preserved post-CON-023).
func TestShouldSkipExecute_ManualAdapter_NeverSkips(t *testing.T) {
	root := t.TempDir()
	seedPassingResult(t, root, "task-manual01", "tc-00000001", "manual")

	resolved := &adapter.ResolvedAdapter{
		Name:   "manual-execute",
		Config: &config.AdapterConfig{Framework: "manual"},
	}

	reason := shouldSkipExecute(root, "tc-00000001", resolved, "manual")
	assert.Equal(t, "", reason, "manual adapter must never skip, even when results show a clean pass")
}

// TestShouldSkipExecute_NonManualAdapter_SkipsAlreadyPassing: a clean pass
// on the most recent terminal handoff for the same framework is the new
// "already passing" signal.
func TestShouldSkipExecute_NonManualAdapter_SkipsAlreadyPassing(t *testing.T) {
	root := t.TempDir()
	seedPassingResult(t, root, "task-bats01", "tc-00000002", "bats")

	resolved := &adapter.ResolvedAdapter{
		Name:   "bats-runner",
		Config: &config.AdapterConfig{Framework: "bats"},
	}

	reason := shouldSkipExecute(root, "tc-00000002", resolved, "bats")
	assert.Equal(t, "already passing", reason)
}

// TestShouldSkipExecute_NoPriorResult_DoesNotSkip: a TC with no terminal
// handoff on disk runs (regression guard for "fresh workspace, never run").
func TestShouldSkipExecute_NoPriorResult_DoesNotSkip(t *testing.T) {
	root := t.TempDir()
	resolved := &adapter.ResolvedAdapter{
		Name:   "bats-runner",
		Config: &config.AdapterConfig{Framework: "bats"},
	}
	reason := shouldSkipExecute(root, "tc-00000003", resolved, "bats")
	assert.Equal(t, "", reason)
}

// TestShouldSkipExecute_NilResolved_NormalBehaviour: a nil resolved
// adapter must not panic; defaults to non-manual skip path.
func TestShouldSkipExecute_NilResolved_NormalBehaviour(t *testing.T) {
	root := t.TempDir()
	seedPassingResult(t, root, "task-nil01", "tc-00000004", "bats")

	reason := shouldSkipExecute(root, "tc-00000004", nil, "bats")
	assert.Equal(t, "already passing", reason,
		"nil resolved adapter should follow normal skip logic (not manual)")
}

// TestShouldSkipExecute_MultiFramework_OtherFrameworkPassDoesNotSkip pins
// the ENH-146 counting-unit rule: skip decisions are scoped to
// `(testcase, framework)`. A Playwright pass on a TC must NOT skip a BATS
// execution for the same TC. Before this scoping landed, both ran through
// `isAlreadyPassing(tcID)` which collapsed all frameworks together.
func TestShouldSkipExecute_MultiFramework_OtherFrameworkPassDoesNotSkip(t *testing.T) {
	root := t.TempDir()
	// Seed a passing Playwright result.
	seedPassingResult(t, root, "task-pw01", "tc-00000005", "playwright")

	resolved := &adapter.ResolvedAdapter{
		Name:   "bats-runner",
		Config: &config.AdapterConfig{Framework: "bats"},
	}

	// Asking about BATS for the same TC must not be skipped — BATS has
	// no terminal result of its own yet, only Playwright does.
	reason := shouldSkipExecute(root, "tc-00000005", resolved, "bats")
	assert.Equal(t, "", reason,
		"Playwright pass must not skip BATS execution — skip is per (tc, framework)")

	// Sanity check the other direction: asking about Playwright still skips.
	pwResolved := &adapter.ResolvedAdapter{
		Name:   "playwright-runner",
		Config: &config.AdapterConfig{Framework: "playwright"},
	}
	pwReason := shouldSkipExecute(root, "tc-00000005", pwResolved, "playwright")
	assert.Equal(t, "already passing", pwReason,
		"Playwright pass must still skip a Playwright re-run on the same TC")
}

// TestIsAlreadyPassing_FrameworkScoped pins the per-framework filter on
// the underlying helper. Empty framework keeps the legacy permissive
// behaviour for any caller that hasn't been updated yet.
func TestIsAlreadyPassing_FrameworkScoped(t *testing.T) {
	root := t.TempDir()
	seedPassingResult(t, root, "task-bats02", "tc-00000006", "bats")
	seedPassingResult(t, root, "task-pw02", "tc-00000007", "playwright")

	assert.True(t, isAlreadyPassing(root, "tc-00000006", "bats"))
	assert.False(t, isAlreadyPassing(root, "tc-00000006", "playwright"),
		"BATS pass must not satisfy a Playwright skip check")
	assert.True(t, isAlreadyPassing(root, "tc-00000007", "playwright"))
	assert.False(t, isAlreadyPassing(root, "tc-00000007", "bats"),
		"Playwright pass must not satisfy a BATS skip check")

	// Permissive (empty framework) — any matching pass counts.
	assert.True(t, isAlreadyPassing(root, "tc-00000006", ""))
	assert.True(t, isAlreadyPassing(root, "tc-00000007", ""))
}
