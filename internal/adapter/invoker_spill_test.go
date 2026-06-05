package adapter

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/result"
)

// BUG-084: integration tests for the result-contract log-cap + spill producer
// at the four write sites — Tier 1 success, Tier 1 failure, Tier 2 contract-
// updated, and the async finalizer. Tier 1 success and Tier 2 contract-updated
// run against the live invoker through InvokeWithRoot; the failure case
// exercises buildFailureLog → ApplyLogCap.

// TestInvoker_LargeFailureLog_SpillsAndCaps exercises the Tier 1 failure path
// with an adapter emitting > 64 KB of stderr.
func TestInvoker_LargeFailureLog_SpillsAndCaps(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	// Produce ~131 KB on stderr then exit non-zero. Use a yes-like loop with
	// a head to stay portable on Windows MINGW64. The line pattern keeps
	// truncation easy to reason about.
	cmd := `awk 'BEGIN { for (i=0; i<5000; i++) print "verbose stderr line that makes the payload large enough to exceed 64 KB and trigger the spill"; }' >&2 ; exit 1`
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock-bigfail",
		Config:  &config.AdapterConfig{Mode: "sync", Command: cmd},
		Tier:    1,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "BUG084-LARGE", CommandFlags{})
	require.NoError(t, err)
	assert.Equal(t, "error", res.Status)

	rcPath := result.ResultPath(root, res.TaskID)
	rc, err := result.Read(rcPath)
	require.NoError(t, err)

	assert.LessOrEqual(t, len(rc.Log), result.NotesSizeCapBytes,
		"rc.Log must be capped to NotesSizeCapBytes")
	require.NotEmpty(t, rc.NotesSpill, "oversize log must produce notes-spill")
	assert.True(t, strings.HasPrefix(rc.NotesSpill, ".gtms/logs/task-"),
		"notes-spill must point under .gtms/logs/")
	assert.True(t, strings.HasSuffix(rc.NotesSpill, ".log"),
		"notes-spill must end with .log")

	// Spill file must exist with the full payload (≥ 131 KB ≫ cap).
	spillAbs := filepath.Join(root, filepath.FromSlash(rc.NotesSpill))
	stat, err := os.Stat(spillAbs)
	require.NoError(t, err, "spill file must exist on disk")
	assert.Greater(t, stat.Size(), int64(result.NotesSizeCapBytes),
		"spill file must carry the full untruncated payload")
}

// TestInvoker_SmallFailureLog_NoSpill verifies that under-cap log payloads
// are preserved verbatim and no spill file is created.
func TestInvoker_SmallFailureLog_NoSpill(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock-smallfail",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `echo "short error message" >&2 ; exit 1`},
		Tier:    1,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "BUG084-SMALL", CommandFlags{})
	require.NoError(t, err)
	assert.Equal(t, "error", res.Status)

	rcPath := result.ResultPath(root, res.TaskID)
	rc, err := result.Read(rcPath)
	require.NoError(t, err)

	assert.NotEmpty(t, rc.Log, "small failure log must still land on contract")
	assert.Empty(t, rc.NotesSpill, "no spill expected for under-cap payload")

	// No spill file should exist under .gtms/logs/.
	logsDir := filepath.Join(root, ".gtms", "logs")
	entries, _ := os.ReadDir(logsDir)
	for _, e := range entries {
		assert.False(t, strings.HasSuffix(e.Name(), ".log"),
			"no .log spill file should exist for under-cap payload")
	}
}

// TestInvoker_LargeFailure_SummaryStaysBounded covers the BUG-084 sibling
// cap: the Tier 1 failure path builds summary from "Process exited with code
// N: <stderr>", so oversize stderr can bloat the contract via summary even
// when log: is correctly truncated. SummarySizeCapBytes (BUG-075) must apply.
func TestInvoker_LargeFailure_SummaryStaysBounded(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	cmd := `awk 'BEGIN { for (i=0; i<5000; i++) print "verbose stderr line that makes the payload large enough to exceed 64 KB and trigger the spill"; }' >&2 ; exit 1`
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock-bigfail-summary",
		Config:  &config.AdapterConfig{Mode: "sync", Command: cmd},
		Tier:    1,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "BUG084-SUMM", CommandFlags{})
	require.NoError(t, err)
	assert.Equal(t, "error", res.Status)

	rcPath := result.ResultPath(root, res.TaskID)
	rc, err := result.Read(rcPath)
	require.NoError(t, err)

	// Summary bound: SummarySizeCapBytes + the truncation marker (room for ~30
	// extra bytes). Anything beyond ~1100 bytes means the cap did not fire.
	assert.LessOrEqual(t, len(rc.Summary), result.SummarySizeCapBytes+64,
		"rc.Summary must be capped to SummarySizeCapBytes plus a small marker")

	// Whole-contract bound: must comfortably fit under the BATS bound used by
	// tc-ab94430c (< 131072). Without the summary cap, this is the regression
	// that surfaced on the VPS.
	stat, err := os.Stat(rcPath)
	require.NoError(t, err)
	assert.Less(t, stat.Size(), int64(131072),
		"whole result contract must fit under the BATS 131072 byte bound")
}

// TestInvoker_Tier2_OversizedLog_SpillsAndCaps exercises the Tier 2 contract-
// updated path with a script that writes an oversize log: block scalar via
// heredoc. The invoker should cap the log on disk and stamp notes-spill.
func TestInvoker_Tier2_OversizedLog_SpillsAndCaps(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	scriptPath := filepath.Join(root, "testdata", "mock-tier2-biglog.sh")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "testdata"), 0755))

	// Build a Tier 2 script that emits ~131 KB inside the contract's log: block
	// scalar, then sets status: complete + result: fail. The invoker's Tier 2
	// contract-updated path should detect rc.Log > NotesSizeCapBytes and spill.
	script := `#!/bin/sh
ID=$(echo "${GTMS_TC_IDS}" | cut -d',' -f1)
OUTFILE="${GTMS_OUTPUT_DIR}/${ID}-bigtest.md"
mkdir -p "${GTMS_OUTPUT_DIR}"
cat > "${OUTFILE}" <<TCEOF
---
test_case_id: ${ID}
name: "bigtest"
---
TCEOF
{
  printf 'task: %s\n' "${GTMS_TASK_ID}"
  printf 'command: create\n'
  printf 'target: %s\n' "${GTMS_REFERENCE}"
  printf 'adapter: mock-tier2-biglog\n'
  printf 'mode: sync\n'
  printf 'status: complete\n'
  printf 'result: fail\n'
  printf 'attempts: 1\n'
  printf 'summary: "Mock tier2 produced oversized log"\n'
  printf 'completed: "2026-05-29T10:00:00Z"\n'
  printf 'log: |\n'
  awk 'BEGIN { for (i=0; i<5000; i++) print "  verbose framework output line that makes the payload large enough to exceed 64 KB"; }'
} > "${GTMS_RESULT_FILE}"
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0755))

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock-tier2-biglog",
		Config:  &config.AdapterConfig{Mode: "sync", Script: "testdata/mock-tier2-biglog.sh"},
		Tier:    2,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "demo-folder", CommandFlags{Folder: "demo-folder"})
	require.NoError(t, err)
	// Status: complete + Result: fail per the script.
	assert.Equal(t, "complete", res.Status)

	rcPath := result.ResultPath(root, res.TaskID)
	rc, err := result.Read(rcPath)
	require.NoError(t, err)

	assert.LessOrEqual(t, len(rc.Log), result.NotesSizeCapBytes,
		"Tier 2 rc.Log must be capped to NotesSizeCapBytes")
	require.NotEmpty(t, rc.NotesSpill, "oversize Tier 2 log must produce notes-spill")
	assert.True(t, strings.HasPrefix(rc.NotesSpill, ".gtms/logs/task-"),
		"notes-spill must point under .gtms/logs/")

	spillAbs := filepath.Join(root, filepath.FromSlash(rc.NotesSpill))
	stat, err := os.Stat(spillAbs)
	require.NoError(t, err, "Tier 2 spill file must exist on disk")
	assert.Greater(t, stat.Size(), int64(result.NotesSizeCapBytes),
		"Tier 2 spill file must carry the full untruncated payload")
}
