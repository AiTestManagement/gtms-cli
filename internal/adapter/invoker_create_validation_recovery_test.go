package adapter

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/aitestmanagement/gtms-cli/internal/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A Tier 2 create adapter that fails in BOTH ways at once: it emits a spec
// whose frontmatter test_case_id does not match its filename ID (a strict
// BUG-038 violation), AND it leaves an unparseable handoff behind, AND it
// exits 0.
//
// The malformed handoff is the byte-for-byte BUG-191 shape: the first log:
// block line is indented four spaces, so YAML infers a four-space block and
// the following two-space line closes it early.
const createBadSpecAndMalformedContractScript = `#!/bin/sh
cat > "${GTMS_OUTPUT_DIR}/tc-aaaaaaaa-bad-spec.md" <<'SPEC'
---
gtms_schema: "test-case/v2"
type: Test Case Spec
test_type: Functional
test_case_id: tc-deadbeef
title: bad spec
---
# Test
SPEC
{
  printf 'task: %s\n'    "${GTMS_TASK_ID}"
  printf 'command: %s\n' "${GTMS_COMMAND}"
  printf 'target: %s\n'  "${GTMS_REFERENCE}"
  printf 'adapter: bad-spec-and-malformed\n'
  printf 'mode: sync\n'
  printf 'status: complete\n'
  printf 'result: pass\n'
  printf 'summary: "specs written"\n'
  printf 'log: |\n'
  printf '    Determining projects to restore...\n'
  printf '  Error Message:\n'
  printf '    Stack Trace:\n'
} > "${GTMS_RESULT_FILE}"
exit 0
`

// REV-164 CODEX-001: gtms create's post-write validation branch runs BEFORE
// handleSyncResult, so it never reached the BUG-191 recovery. It wrote its
// error handoff with result.Update and discarded the error; when the handoff
// was also unusable, Update could not decode it, wrote nothing, and left an
// unreadable handoff behind while still routing the task to error/.
//
// That falsified BUG-191's command-scope contract, which promises all four
// synchronous commands leave a READABLE error handoff for an unusable
// contract. This pins both diagnostics surviving on that combined path.
func TestREV164_CreateValidationFailure_UnusableContract_StillLeavesReadableHandoff(t *testing.T) {
	skipIfShort(t)
	if runtime.GOOS == "windows" {
		t.Skip("Tier 2 sh adapter tests require a Unix shell")
	}

	root := setupTestProject(t)

	scriptPath := filepath.Join(root, "testdata", "create-bad-spec-malformed.sh")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "testdata"), 0755))
	require.NoError(t, os.WriteFile(scriptPath, []byte(createBadSpecAndMalformedContractScript), 0755))

	outputDir := filepath.Join(root, "gtms/test/cases", "rev164create")
	require.NoError(t, os.MkdirAll(outputDir, 0755))

	cfg := &config.Config{Project: config.ProjectConfig{Name: "Test", Repo: "org/test"}}
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "bad-spec-and-malformed",
		Config:  &config.AdapterConfig{Mode: "sync", Script: "testdata/create-bad-spec-malformed.sh"},
		Tier:    2,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "rev164create", CommandFlags{Folder: "rev164create"})
	require.NoError(t, err, "recovery returns an error-status result, not a Go error")
	require.NotNil(t, res)

	// gtms create propagates status: error as a non-zero CLI exit. That must
	// not change.
	assert.Equal(t, "error", res.Status)

	// BOTH diagnostics reach the operator. Neither displaces the other.
	assert.Contains(t, res.Summary, "does not match filename ID",
		"the validation violation is why this branch exists and must still be reported")
	assert.Contains(t, res.Summary, "result contract unusable",
		"the operator must also learn the adapter's handoff could not be read")

	// Task routed to error/.
	errorTasks, taskErr := task.List(root, "error")
	require.NoError(t, taskErr)
	require.Len(t, errorTasks, 1)
	assert.Equal(t, "error", errorTasks[0].Status)

	// The durable handoff is READABLE -- this is the guarantee that was broken.
	handoffPath := result.ResultPath(root, res.TaskID)
	rc, rcErr := result.Read(handoffPath)
	require.NoError(t, rcErr, "an unusable contract must still leave a readable error handoff")
	assert.Equal(t, "error", rc.Status)
	assert.Empty(t, rc.Result, "an unusable document has no authoritative outcome")
	assert.Equal(t, "create", rc.Command)
	assert.Equal(t, errorTasks[0].ID, rc.Task)
	assert.Contains(t, rc.Summary, "does not match filename ID")
	assert.Contains(t, rc.Summary, "result contract unusable")
	assert.Contains(t, rc.ValidationError, "does not match filename ID",
		"the BUG-038 validation-error key must survive the reconstruction")

	// The adapter's claimed pass is not salvaged anywhere.
	assert.NotEqual(t, "pass", rc.Result)

	// The bad spec stays on disk for inspection (BUG-038 behaviour, unchanged).
	assert.FileExists(t, filepath.Join(outputDir, "tc-aaaaaaaa-bad-spec.md"))
}
