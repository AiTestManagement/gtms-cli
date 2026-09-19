package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/triage"
)

// ENH-200 / CON-040 CLI tests. The retired TriageResult JSON and RecordTriage
// path are gone; these cover the mutation JSON contract and the five-stage
// validation order (the deeper behavioural coverage is in the
// configurable-triage-storage acceptance specs).

func sampleTriageRecord() *triage.Record {
	return &triage.Record{
		Version:      triage.SchemaVersion,
		TestCaseID:   "tc-11111111",
		Framework:    "bats",
		Status:       triage.StatusActive,
		Value:        "environment",
		Summary:      "Shared creds expired",
		Defect:       "JIRA-789",
		ClassifiedAt: "2026-08-25T10:15:00Z",
		Snapshot: triage.Snapshot{
			Task:                 "task-0a0f57cf",
			Result:               "fail",
			CompletedAt:          "2026-08-25T09:42:00Z",
			Artefact:             "test/probe.bats",
			ArtefactHash:         "aaaabbbbccccdddd",
			TestcaseHashAtTriage: "def4567890abcdef",
		},
	}
}

func TestWriteTriageJSON_FullRecord(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, writeTriageJSON(&buf, sampleTriageRecord(), "Environment failure"))
	out := buf.String()

	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &m))
	assert.Equal(t, "tc-11111111", m["test_case_id"])
	assert.Equal(t, "environment", m["value"])
	assert.Equal(t, "Environment failure", m["label"])
	assert.Equal(t, "active", m["status"])
	snap, ok := m["snapshot"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "fail", snap["result"])
	assert.Equal(t, "task-0a0f57cf", snap["task"])

	// No retired or ENH-201 keys, anywhere in the document.
	for _, forbidden := range []string{"actions", "new_task_id", "result_changed_since_triage", "category"} {
		assert.NotContains(t, out, `"`+forbidden+`"`)
	}
}

func TestWriteTriageJSON_OmitsEmptyOptionals(t *testing.T) {
	rec := sampleTriageRecord()
	rec.Summary = ""
	rec.Defect = ""
	rec.Snapshot.ArtefactHash = ""
	rec.Snapshot.CompletedAt = ""

	var buf bytes.Buffer
	require.NoError(t, writeTriageJSON(&buf, rec, "")) // empty label
	out := buf.String()

	for _, absent := range []string{`"summary"`, `"defect"`, `"label"`, `"resolved_at"`, `"artefact_hash"`, `"completed_at"`} {
		assert.NotContains(t, out, absent)
	}
}

func TestWriteTriageJSON_Resolved(t *testing.T) {
	rec := sampleTriageRecord()
	rec.Status = triage.StatusResolved
	rec.ResolvedAt = "2026-08-26T14:30:00Z"

	var buf bytes.Buffer
	require.NoError(t, writeTriageJSON(&buf, rec, ""))
	var m map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &m))
	assert.Equal(t, "resolved", m["status"])
	assert.Equal(t, "2026-08-26T14:30:00Z", m["resolved_at"])
}

// runTriage executes a fresh triage command with args and returns combined
// output and the error. These invocations are rejected during validation
// (stages 1-5) before any project/config access, so no fixture is needed.
func runTriage(args ...string) (string, error) {
	cmd := newTriageCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

// ENH-203 removed the three retired category flags. These replace the four
// migration-only tests that used to pin the retired-flag rejection stage. They
// assert the positive removal contract: the flags are not registered, each is
// answered with Cobra's unknown-flag error (never the old migration message),
// and none appears in help. The flag-lookup-is-nil assertion is the mutation
// guard a help-absence check cannot provide, since a hidden registration is
// exactly what the pre-ENH-203 state was.
var retiredTriageFlags = []string{"automation-wrong", "test-wrong", "app-wrong"}

func TestTriageCmd_RetiredFlagsNotRegistered(t *testing.T) {
	cmd := newTriageCmd()
	for _, name := range retiredTriageFlags {
		assert.Nil(t, cmd.Flags().Lookup(name),
			"retired flag --%s must not be registered on the triage command", name)
	}
}

func TestTriageCmd_RetiredFlagIsUnknownFlag(t *testing.T) {
	for _, name := range retiredTriageFlags {
		_, err := runTriage("--" + name)
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), "unknown flag: --"+name,
			"--%s must produce Cobra's unknown-flag error", name)
		// The retired migration diagnostic must be gone.
		assert.NotContains(t, err.Error(), "were retired", name)
	}
}

func TestTriageCmd_RetiredFlagUnknownEvenWithValidOperation(t *testing.T) {
	// A retired flag combined with an otherwise-valid classification is still
	// rejected as unknown before any GTMS validation stage runs.
	_, err := runTriage("tc-11111111", "--value", "environment", "--test-wrong")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown flag: --test-wrong")
}

func TestTriageCmd_RetiredFlagsAbsentFromHelp(t *testing.T) {
	cmd := newTriageCmd()
	help := cmd.UsageString() + cmd.Long
	for _, name := range retiredTriageFlags {
		assert.NotContains(t, help, "--"+name,
			"retired flag --%s must not appear in help", name)
	}
}

func TestTriageCmd_TwoPositionalsRejected(t *testing.T) {
	_, err := runTriage("tc-11111111", "tc-22222222", "--value", "environment")
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "one test case")
}

func TestTriageCmd_MissingOperation(t *testing.T) {
	_, err := runTriage("tc-11111111")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--value")
	assert.Contains(t, err.Error(), "--resolve")
}

func TestTriageCmd_ConflictingOperations(t *testing.T) {
	_, err := runTriage("tc-11111111", "--value", "environment", "--resolve")
	require.Error(t, err)
	// ENH-201 generalised the two-operation message to name every conflicting
	// operation (so --show/--list conflicts report uniformly). The regression
	// intent is unchanged: a conflict is reported and both operations are named.
	assert.Contains(t, strings.ToLower(err.Error()), "conflicting operations")
	assert.Contains(t, err.Error(), "--value")
	assert.Contains(t, err.Error(), "--resolve")
}

func TestTriageCmd_ResolveWithSummaryRejected(t *testing.T) {
	_, err := runTriage("tc-11111111", "--resolve", "--summary", "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--summary")
}

func TestTriageCmd_UnsafeTargetBeforeOperation(t *testing.T) {
	// No operation flag: the path-safety diagnostic (stage 3) must still win
	// over the missing-operation error (stage 4). This is the bug-058 guard.
	_, err := runTriage("../../../tc-abc12345", "--summary", "test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsafe characters")
}
