package result

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateAndRead(t *testing.T) {
	root := t.TempDir()

	rc := &ResultContract{
		Task:    "task-abc1234",
		Command: "create",
		Target:  "JIRA-456",
		Adapter: "local-claude",
		Mode:    "sync",
		Created: "2025-02-14T10:00:00Z",
		Status:  "pending",
	}

	path, err := Create(root, rc)
	require.NoError(t, err)
	assert.FileExists(t, path)
	assert.Contains(t, path, filepath.Join(".gtms", "results"))
	assert.Contains(t, path, "task-abc1234.handoff.yaml")

	// Read back
	readRC, err := Read(path)
	require.NoError(t, err)
	assert.Equal(t, "task-abc1234", readRC.Task)
	assert.Equal(t, "create", readRC.Command)
	assert.Equal(t, "JIRA-456", readRC.Target)
	assert.Equal(t, "local-claude", readRC.Adapter)
	assert.Equal(t, "sync", readRC.Mode)
	assert.Equal(t, "2025-02-14T10:00:00Z", readRC.Created)
	assert.Equal(t, "pending", readRC.Status)
}

func TestCreate_DefaultStatus(t *testing.T) {
	root := t.TempDir()

	rc := &ResultContract{
		Task:    "task-def5678",
		Command: "automate",
		Target:  "tc-001",
		Adapter: "test",
		Mode:    "sync",
		Created: "2025-02-14T10:00:00Z",
	}

	path, err := Create(root, rc)
	require.NoError(t, err)

	readRC, err := Read(path)
	require.NoError(t, err)
	assert.Equal(t, "pending", readRC.Status)
}

func TestCreate_MissingTask(t *testing.T) {
	root := t.TempDir()

	_, err := Create(root, &ResultContract{Command: "create"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "task ID is required")
}

func TestUpdate(t *testing.T) {
	root := t.TempDir()

	rc := &ResultContract{
		Task:    "task-upd1234",
		Command: "create",
		Target:  "JIRA-789",
		Adapter: "test-adapter",
		Mode:    "sync",
		Created: "2025-02-14T10:00:00Z",
		Status:  "pending",
	}

	path, err := Create(root, rc)
	require.NoError(t, err)

	// Update specific fields (ENH-130: complete requires result)
	err = Update(path, map[string]interface{}{
		"status":    "complete",
		"result":    "pass",
		"artefact":  "gtms/test/cases/tc-001-checkout.md",
		"attempts":  1,
		"summary":   "Successfully created test case",
		"completed": "2025-02-14T10:05:00Z",
	})
	require.NoError(t, err)

	// Read and verify
	readRC, err := Read(path)
	require.NoError(t, err)
	assert.Equal(t, "complete", readRC.Status)
	assert.Equal(t, "gtms/test/cases/tc-001-checkout.md", readRC.Artefact)
	assert.Equal(t, 1, readRC.Attempts)
	assert.Equal(t, "Successfully created test case", readRC.Summary)
	assert.Equal(t, "2025-02-14T10:05:00Z", readRC.Completed)

	// Original fields preserved
	assert.Equal(t, "task-upd1234", readRC.Task)
	assert.Equal(t, "create", readRC.Command)
	assert.Equal(t, "JIRA-789", readRC.Target)
	assert.Equal(t, "test-adapter", readRC.Adapter)
}

func TestUpdate_ErrorStatus(t *testing.T) {
	root := t.TempDir()

	rc := &ResultContract{
		Task:    "task-err1234",
		Command: "create",
		Target:  "JIRA-111",
		Adapter: "test",
		Mode:    "sync",
		Created: "2025-02-14T10:00:00Z",
		Status:  "pending",
	}

	path, err := Create(root, rc)
	require.NoError(t, err)

	err = Update(path, map[string]interface{}{
		"status":    "error",
		"summary":   "Process exited with code 1",
		"completed": "2025-02-14T10:01:00Z",
	})
	require.NoError(t, err)

	readRC, err := Read(path)
	require.NoError(t, err)
	assert.Equal(t, "error", readRC.Status)
	assert.Equal(t, "Process exited with code 1", readRC.Summary)
}

func TestResultPath(t *testing.T) {
	path := ResultPath("/projects/myapp", "task-abc1234")
	assert.Equal(t, filepath.Join("/projects/myapp", ".gtms", "results", "task-abc1234.handoff.yaml"), path)
}

func TestCreateWithAllFields(t *testing.T) {
	root := t.TempDir()

	rc := &ResultContract{
		Task:      "task-full123",
		Command:   "create",
		Target:    "JIRA-999",
		Adapter:   "full-adapter",
		Mode:      "async",
		Created:   "2025-02-14T10:00:00Z",
		Status:    "complete",
		Result:    "pass", // ENH-130: complete requires result
		Artefact:  "gtms/test/cases/tc-099.md",
		Attempts:  3,
		Summary:   "Completed after retries",
		Log:       "attempt 1 failed\nattempt 2 failed\nattempt 3 succeeded",
		Completed: "2025-02-14T10:30:00Z",
	}

	path, err := Create(root, rc)
	require.NoError(t, err)

	readRC, err := Read(path)
	require.NoError(t, err)
	assert.Equal(t, "complete", readRC.Status)
	assert.Equal(t, "gtms/test/cases/tc-099.md", readRC.Artefact)
	assert.Equal(t, 3, readRC.Attempts)
	assert.Equal(t, "Completed after retries", readRC.Summary)
	assert.Contains(t, readRC.Log, "attempt 1 failed")
	assert.Equal(t, "2025-02-14T10:30:00Z", readRC.Completed)
}

// --- ENH-096: adapter-injectable warnings field ---

func TestCreateAndRead_Warnings(t *testing.T) {
	root := t.TempDir()

	rc := &ResultContract{
		Task:     "task-warn001",
		Command:  "create",
		Target:   "feature-warn",
		Adapter:  "mock-warn",
		Mode:     "sync",
		Created:  "2026-04-19T10:00:00Z",
		Status:   "complete",
		Result:   "pass", // ENH-130: complete requires result
		Warnings: []string{"prompt template missing guides section", "model returned fewer TCs than requested"},
	}

	path, err := Create(root, rc)
	require.NoError(t, err)

	readRC, err := Read(path)
	require.NoError(t, err)
	assert.Equal(t, "complete", readRC.Status)
	require.Len(t, readRC.Warnings, 2)
	assert.Equal(t, "prompt template missing guides section", readRC.Warnings[0])
	assert.Equal(t, "model returned fewer TCs than requested", readRC.Warnings[1])
}

func TestCreateAndRead_NoWarnings(t *testing.T) {
	root := t.TempDir()

	rc := &ResultContract{
		Task:    "task-nowarn",
		Command: "create",
		Target:  "feature-ok",
		Adapter: "mock",
		Mode:    "sync",
		Created: "2026-04-19T10:00:00Z",
		Status:  "complete",
		Result:  "pass", // ENH-130: complete requires result
	}

	path, err := Create(root, rc)
	require.NoError(t, err)

	readRC, err := Read(path)
	require.NoError(t, err)
	assert.Nil(t, readRC.Warnings)
}

func TestRead_WarningsFromRawYAML(t *testing.T) {
	root := t.TempDir()

	// Simulate adapter writing warnings directly into the YAML (as Tier 2 scripts do)
	yaml := `task: task-rawwarn
command: create
target: feature-raw
adapter: mock-warn
mode: sync
created: "2026-04-19T10:00:00Z"
status: complete
warnings:
  - "prompt template missing guides section"
completed: "2026-04-19T10:05:00Z"
`
	path := writeRawResultContract(t, root, "task-rawwarn", yaml)

	rc, err := Read(path)
	require.NoError(t, err)
	require.Len(t, rc.Warnings, 1)
	assert.Equal(t, "prompt template missing guides section", rc.Warnings[0])
	assert.Equal(t, "2026-04-19T10:05:00Z", rc.Completed)
}

// --- BUG-036: YAML document separator in log field ---

// writeRawResultContract writes raw YAML bytes to a result contract file,
// simulating what a Tier 2 adapter script writes via heredoc.
func writeRawResultContract(t *testing.T, root, taskID, yamlContent string) string {
	t.Helper()
	dir := filepath.Join(root, ".gtms", "results")
	require.NoError(t, os.MkdirAll(dir, 0755))
	path := filepath.Join(dir, taskID+".handoff.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yamlContent), 0644))
	return path
}

func TestRead_LogWithYAMLDocumentSeparator(t *testing.T) {
	root := t.TempDir()

	// Simulate adapter output with --- inside the log block scalar
	yaml := `task: task-bug036a
command: execute
target: tc-7b4f45c7
adapter: remote-pester-lean
mode: sync
created: "2026-04-12T10:00:00Z"
status: complete
summary: "All tests passed"
log: |
  Pester v5.7.1
  ---
  Tests completed
completed: "2026-04-12T10:05:00Z"
`
	path := writeRawResultContract(t, root, "task-bug036a", yaml)

	rc, err := Read(path)
	require.NoError(t, err)
	assert.Equal(t, "task-bug036a", rc.Task)
	assert.Equal(t, "complete", rc.Status)
	assert.Equal(t, "All tests passed", rc.Summary)
	assert.Contains(t, rc.Log, "Pester v5.7.1")
	assert.Equal(t, "2026-04-12T10:05:00Z", rc.Completed, "completed field must survive --- in log")
}

func TestRead_LogWithYAMLDocumentEndMarker(t *testing.T) {
	root := t.TempDir()

	yaml := `task: task-bug036b
command: execute
target: tc-abc
adapter: test
mode: sync
created: "2026-04-12T10:00:00Z"
status: complete
log: |
  some output
  ...
  more output
completed: "2026-04-12T10:05:00Z"
`
	path := writeRawResultContract(t, root, "task-bug036b", yaml)

	rc, err := Read(path)
	require.NoError(t, err)
	assert.Equal(t, "complete", rc.Status)
	assert.Equal(t, "2026-04-12T10:05:00Z", rc.Completed, "completed field must survive ... in log")
}

func TestRead_LogWithMultipleSeparators(t *testing.T) {
	root := t.TempDir()

	yaml := `task: task-bug036c
command: execute
target: tc-multi
adapter: test
mode: sync
created: "2026-04-12T10:00:00Z"
status: complete
log: |
  --- header ---
  output line 1
  ---
  output line 2
  ---
  output line 3
completed: "2026-04-12T10:10:00Z"
`
	path := writeRawResultContract(t, root, "task-bug036c", yaml)

	rc, err := Read(path)
	require.NoError(t, err)
	assert.Equal(t, "complete", rc.Status)
	assert.Equal(t, "2026-04-12T10:10:00Z", rc.Completed, "completed must survive multiple --- lines")
}

func TestRead_LogInlineValue(t *testing.T) {
	root := t.TempDir()

	// Inline log value (not a block scalar) -- should work unchanged
	yaml := `task: task-bug036d
command: execute
target: tc-inline
adapter: test
mode: sync
created: "2026-04-12T10:00:00Z"
status: complete
log: "simple inline log"
completed: "2026-04-12T10:05:00Z"
`
	path := writeRawResultContract(t, root, "task-bug036d", yaml)

	rc, err := Read(path)
	require.NoError(t, err)
	assert.Equal(t, "simple inline log", rc.Log)
	assert.Equal(t, "2026-04-12T10:05:00Z", rc.Completed)
}

func TestRead_NoLogField(t *testing.T) {
	root := t.TempDir()

	// Contract without log field -- must work as before
	yaml := `task: task-bug036e
command: execute
target: tc-nolog
adapter: test
mode: sync
created: "2026-04-12T10:00:00Z"
status: complete
completed: "2026-04-12T10:05:00Z"
`
	path := writeRawResultContract(t, root, "task-bug036e", yaml)

	rc, err := Read(path)
	require.NoError(t, err)
	assert.Equal(t, "complete", rc.Status)
	assert.Empty(t, rc.Log)
	assert.Equal(t, "2026-04-12T10:05:00Z", rc.Completed)
}

func TestRead_EmptyLog(t *testing.T) {
	root := t.TempDir()

	yaml := `task: task-bug036f
command: execute
target: tc-empty
adapter: test
mode: sync
created: "2026-04-12T10:00:00Z"
status: complete
log: ""
completed: "2026-04-12T10:05:00Z"
`
	path := writeRawResultContract(t, root, "task-bug036f", yaml)

	rc, err := Read(path)
	require.NoError(t, err)
	assert.Empty(t, rc.Log)
	assert.Equal(t, "2026-04-12T10:05:00Z", rc.Completed)
}

// Test the sanitize helper directly

func TestSanitizeResultYAML_PreservesCleanYAML(t *testing.T) {
	clean := `task: task-001
command: execute
status: complete
completed: "2026-04-12T10:00:00Z"
`
	result := sanitizeResultYAML([]byte(clean))
	assert.Equal(t, clean, string(result))
}

func TestSanitizeResultYAML_NeutralizesBareSeparatorInLog(t *testing.T) {
	input := `task: task-002
log: |
  line one
  ---
  line two
completed: "2026-04-12T10:00:00Z"
`
	result := string(sanitizeResultYAML([]byte(input)))
	assert.Contains(t, result, "- - -")
	assert.Contains(t, result, "completed:")
	assert.NotContains(t, result, "\n  ---\n")
}

func TestSanitizeResultYAML_NeutralizesDocEndMarkerInLog(t *testing.T) {
	input := `task: task-003
log: |
  output
  ...
  more
completed: "2026-04-12T10:00:00Z"
`
	result := string(sanitizeResultYAML([]byte(input)))
	assert.Contains(t, result, ". . .")
	assert.Contains(t, result, "completed:")
}

func TestSanitizeResultYAML_DoesNotTouchSeparatorOutsideLog(t *testing.T) {
	// A --- outside a log block scalar should NOT be modified
	// (it's a legitimate YAML document separator in the contract structure)
	input := `task: task-004
status: complete
`
	result := string(sanitizeResultYAML([]byte(input)))
	assert.Equal(t, input, result)
}

func TestUpdate_LogWithYAMLDocumentSeparator(t *testing.T) {
	root := t.TempDir()

	// Write a contract with --- in log (ENH-130: complete requires result)
	yaml := `task: task-upd036
command: execute
target: tc-upd
adapter: test
mode: sync
created: "2026-04-12T10:00:00Z"
status: complete
result: pass
log: |
  output
  ---
  more output
completed: "2026-04-12T10:05:00Z"
`
	path := writeRawResultContract(t, root, "task-upd036", yaml)

	// Update should be able to read the contract despite ---
	err := Update(path, map[string]interface{}{
		"status": "complete",
		"result": "pass",
	})
	require.NoError(t, err)

	// Read back and verify fields are intact
	rc, err := Read(path)
	require.NoError(t, err)
	assert.Equal(t, "complete", rc.Status)
	assert.Equal(t, "task-upd036", rc.Task)
}

// --- ENH-130: Validate function and orthogonal contract tests ---

func TestValidate_ValidCombinations(t *testing.T) {
	tests := []struct {
		name   string
		status string
		result string
	}{
		{"pending empty result", "pending", ""},
		{"in-progress empty result", "in-progress", ""},
		{"error empty result", "error", ""},
		{"error with pass", "error", "pass"},
		{"error with fail", "error", "fail"},
		{"error with skip", "error", "skip"},
		{"error with error", "error", "error"},
		{"complete pass", "complete", "pass"},
		{"complete fail", "complete", "fail"},
		{"complete skip", "complete", "skip"},
		{"complete error", "complete", "error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc := &ResultContract{Status: tt.status, Result: tt.result}
			err := Validate(rc)
			assert.NoError(t, err)
		})
	}
}

func TestValidate_InvalidCombinations(t *testing.T) {
	tests := []struct {
		name      string
		status    string
		result    string
		wantError string
	}{
		{"complete empty result", "complete", "", "result is empty"},
		{"legacy fail status", "fail", "", "invalid contract status"},
		{"legacy skipped status", "skipped", "", "invalid contract status"},
		{"bogus result value", "complete", "bogus", "invalid contract result"},
		{"pending with pass", "pending", "pass", "result must be empty"},
		{"in-progress with pass", "in-progress", "pass", "result must be empty"},
		{"pending with fail", "pending", "fail", "result must be empty"},
		{"in-progress with fail", "in-progress", "fail", "result must be empty"},
		{"empty status", "", "", "invalid contract status"},
		{"unknown status", "unknown", "", "invalid contract status"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc := &ResultContract{Status: tt.status, Result: tt.result}
			err := Validate(rc)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantError)
		})
	}
}

func TestResultField_YAMLRoundTrip(t *testing.T) {
	root := t.TempDir()

	rc := &ResultContract{
		Task:    "task-rt130",
		Command: "execute",
		Target:  "tc-abc",
		Adapter: "test",
		Mode:    "sync",
		Created: "2026-05-07T10:00:00Z",
		Status:  "complete",
		Result:  "fail",
	}

	path, err := Create(root, rc)
	require.NoError(t, err)

	readRC, err := Read(path)
	require.NoError(t, err)
	assert.Equal(t, "fail", readRC.Result)
	assert.Equal(t, "complete", readRC.Status)
	// Verify other fields preserved
	assert.Equal(t, "task-rt130", readRC.Task)
	assert.Equal(t, "execute", readRC.Command)
}

func TestResultField_OmittedWhenEmpty(t *testing.T) {
	root := t.TempDir()

	rc := &ResultContract{
		Task:    "task-omit130",
		Command: "create",
		Target:  "feature",
		Adapter: "test",
		Mode:    "sync",
		Created: "2026-05-07T10:00:00Z",
		Status:  "pending",
		// Result intentionally empty
	}

	path, err := Create(root, rc)
	require.NoError(t, err)

	// Read raw file to check result: is not present
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "result:")
}

func TestUpdate_PostMergeValidation(t *testing.T) {
	root := t.TempDir()

	// Create a pending contract
	rc := &ResultContract{
		Task:    "task-merge130",
		Command: "execute",
		Target:  "tc-merge",
		Adapter: "test",
		Mode:    "sync",
		Created: "2026-05-07T10:00:00Z",
		Status:  "pending",
	}
	path, err := Create(root, rc)
	require.NoError(t, err)

	// Update to in-progress (valid)
	err = Update(path, map[string]interface{}{
		"status": "in-progress",
	})
	assert.NoError(t, err)

	// Update to complete WITHOUT result (invalid — should fail)
	err = Update(path, map[string]interface{}{
		"status": "complete",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "result is empty")

	// Update to complete WITH result (valid)
	err = Update(path, map[string]interface{}{
		"status": "complete",
		"result": "pass",
	})
	assert.NoError(t, err)
}

func TestUpdate_RecoverFromMalformedContract(t *testing.T) {
	root := t.TempDir()

	// Simulate a script that wrote a bogus result value directly
	yaml := `task: task-bogus130
command: execute
target: tc-bogus
adapter: bad-script
mode: sync
created: "2026-05-07T10:00:00Z"
status: complete
result: bogus
completed: "2026-05-07T10:05:00Z"
`
	path := writeRawResultContract(t, root, "task-bogus130", yaml)

	// Recovery update: clear the stale invalid result, set to error
	err := Update(path, map[string]interface{}{
		"status":  "error",
		"result":  "",
		"summary": "adapter wrote invalid contract: invalid result bogus",
	})
	assert.NoError(t, err)

	// Verify the recovery write succeeded
	readRC, err := Read(path)
	require.NoError(t, err)
	assert.Equal(t, "error", readRC.Status)
	assert.Empty(t, readRC.Result)
	assert.Contains(t, readRC.Summary, "invalid contract")
}

// --- CON-023 / ENH-146 result-contract additions ---

func TestNewFields_RoundTrip(t *testing.T) {
	root := t.TempDir()
	dirty := true
	rc := &ResultContract{
		Task:        "task-enh146a",
		Command:     "execute",
		Target:      "tc-enh146",
		Adapter:     "bats-runner",
		Mode:        "sync",
		Created:     "2026-05-19T10:00:00Z",
		Status:      "complete",
		Result:      "pass",
		Framework:   "bats",
		GitCommit:   "deadbeef1234",
		GitBranch:   "integration",
		GitDirty:    &dirty,
		ExecutedBy:  "Bill Echlin",
		Environment: "staging",
	}
	path, err := Create(root, rc)
	require.NoError(t, err)

	got, err := Read(path)
	require.NoError(t, err)
	assert.Equal(t, "bats", got.Framework)
	assert.Equal(t, "deadbeef1234", got.GitCommit)
	assert.Equal(t, "integration", got.GitBranch)
	require.NotNil(t, got.GitDirty)
	assert.True(t, *got.GitDirty)
	assert.Equal(t, "Bill Echlin", got.ExecutedBy)
	assert.Equal(t, "staging", got.Environment)
}

func TestNewFields_OmittedWhenEmpty(t *testing.T) {
	root := t.TempDir()
	rc := &ResultContract{
		Task: "task-enh146b", Command: "execute", Target: "tc-enh146", Adapter: "bats-runner",
		Mode: "sync", Created: "2026-05-19T10:00:00Z", Status: "pending",
	}
	path, err := Create(root, rc)
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	for _, key := range []string{"framework:", "git-commit:", "git-branch:", "git-dirty:", "executed_by:", "environment:"} {
		assert.NotContains(t, string(data), key, "field %s must be omitted when unset", key)
	}
}

// TestGitDirty_TriStateSurvivesUpdateRoundTrip guards the *bool foot-gun
// called out in PRP-ENH-145-146-147 Task 3: an Update merge that uses a
// generic map must preserve the three states (nil = unavailable, &false
// = clean, &true = dirty). A plain bool with omitempty would conflate
// "unavailable" with "clean".
func TestGitDirty_TriStateSurvivesUpdateRoundTrip(t *testing.T) {
	root := t.TempDir()
	rc := &ResultContract{
		Task: "task-tri", Command: "execute", Target: "tc-tri", Adapter: "bats-runner",
		Mode: "sync", Created: "2026-05-19T10:00:00Z", Status: "in-progress",
	}
	path, err := Create(root, rc)
	require.NoError(t, err)

	// (1) git-dirty: true survives.
	require.NoError(t, Update(path, map[string]interface{}{
		"status":    "complete",
		"result":    "pass",
		"completed": "2026-05-19T10:01:00Z",
		"git-dirty": true,
	}))
	got, err := Read(path)
	require.NoError(t, err)
	require.NotNil(t, got.GitDirty, "git-dirty:true must survive Update round-trip")
	assert.True(t, *got.GitDirty)

	// (2) git-dirty: false survives.
	require.NoError(t, Update(path, map[string]interface{}{"git-dirty": false}))
	got, err = Read(path)
	require.NoError(t, err)
	require.NotNil(t, got.GitDirty, "git-dirty:false must survive Update round-trip and stay distinguishable from nil")
	assert.False(t, *got.GitDirty)

	// (3) An Update that does NOT touch git-dirty leaves the stored value alone.
	require.NoError(t, Update(path, map[string]interface{}{"summary": "still here"}))
	got, err = Read(path)
	require.NoError(t, err)
	require.NotNil(t, got.GitDirty)
	assert.False(t, *got.GitDirty)
	assert.Equal(t, "still here", got.Summary)
}

// --- BUG-130: IsTerminalExecuteContract predicate ---

// TestIsTerminalExecuteContract is a table-driven test covering every
// (command, status) combination that matters. Only (execute, complete)
// and (execute, error) return true; every other combination is false.
func TestIsTerminalExecuteContract(t *testing.T) {
	tests := []struct {
		name    string
		command string
		status  string
		want    bool
	}{
		// Positive cases: execute + terminal status
		{"execute complete", "execute", "complete", true},
		{"execute error", "execute", "error", true},

		// Execute but non-terminal
		{"execute pending", "execute", "pending", false},
		{"execute in-progress", "execute", "in-progress", false},

		// Non-execute commands with terminal status
		{"automate complete", "automate", "complete", false},
		{"automate error", "automate", "error", false},
		{"create complete", "create", "complete", false},
		{"create error", "create", "error", false},
		{"prime complete", "prime", "complete", false},
		{"prime error", "prime", "error", false},

		// Non-execute + non-terminal (double negative)
		{"automate pending", "automate", "pending", false},
		{"create in-progress", "create", "in-progress", false},

		// Edge: empty command
		{"empty command complete", "", "complete", false},
		// Edge: empty status
		{"execute empty status", "execute", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc := &ResultContract{Command: tt.command, Status: tt.status}
			got := IsTerminalExecuteContract(rc)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestIsTerminalExecuteContract_Nil verifies the nil safety guard.
func TestIsTerminalExecuteContract_Nil(t *testing.T) {
	assert.False(t, IsTerminalExecuteContract(nil), "nil contract must return false")
}
