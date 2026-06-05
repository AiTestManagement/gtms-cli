package execution

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteAndRead_AllCoreFields(t *testing.T) {
	root := t.TempDir()
	setupExecutionDir(t, root)

	rf := &ResultsFile{
		SchemaVersion: "0.1",
		TaskID:        "task-bf9cd4d3",
		Framework:     "playwright",
		Adapter:       "claude-playwright",
		StartedAt:     "2026-04-25T09:51:53Z",
		CompletedAt:   "2026-04-25T09:51:54Z",
		Artefact:      "tests/login.spec.ts",
		Results: []TestResult{
			{
				TCID:       "tc-abc12345",
				Outcome:    "fail",
				StartedAt:  "2026-04-25T09:51:53Z",
				DurationMS: 412,
				Message:    "Expected error text not visible",
				StackTrace: "Expected locator('.error') to have text: 'Invalid credentials'\nReceived: ''\n  at tests/login.spec.ts:42:15",
				Stdout:     "navigating to /login",
				Stderr:     "",
				Attachments: []Attachment{
					{Type: "screenshot", Path: "gtms/execution/attachments/tc-abc12345/20260425-095153-screenshot.png", MimeType: "image/png"},
					{Type: "video", Path: "gtms/execution/attachments/tc-abc12345/20260425-095153-video.webm", MimeType: "video/webm"},
				},
				Steps: []Step{
					{Name: "navigate", Outcome: "pass", DurationMS: 142},
					{Name: "fill form", Outcome: "pass", DurationMS: 85},
					{Name: "assert error", Outcome: "fail", DurationMS: 185},
				},
				Retries: []Retry{
					{Outcome: "fail", DurationMS: 390, Message: "flaky network"},
				},
				Links: []Link{
					{Label: "trace", URL: "https://trace.playwright.dev/abc123"},
				},
				Framework:    "playwright",
				Adapter:      "claude-playwright",
				SourceFormat: "junit-xml",
				Extras:       map[string]interface{}{"browser": "chromium", "viewport_width": 1280},
			},
		},
	}

	path, err := Write(root, rf)
	require.NoError(t, err)
	assert.FileExists(t, path)
	assert.Contains(t, path, "task-bf9cd4d3--tc-abc12345.results.yaml")

	// Read back
	readRF, err := Read(path)
	require.NoError(t, err)

	assert.Equal(t, "0.1", readRF.SchemaVersion)
	assert.Equal(t, "task-bf9cd4d3", readRF.TaskID)
	assert.Equal(t, "playwright", readRF.Framework)
	assert.Equal(t, "claude-playwright", readRF.Adapter)
	assert.Equal(t, "2026-04-25T09:51:53Z", readRF.StartedAt)
	assert.Equal(t, "2026-04-25T09:51:54Z", readRF.CompletedAt)
	assert.Equal(t, "tests/login.spec.ts", readRF.Artefact)

	require.Len(t, readRF.Results, 1)
	tr := readRF.Results[0]
	assert.Equal(t, "tc-abc12345", tr.TCID)
	assert.Equal(t, "fail", tr.Outcome)
	assert.Equal(t, 412, tr.DurationMS)
	assert.Equal(t, "Expected error text not visible", tr.Message)
	assert.Contains(t, tr.StackTrace, "locator('.error')")
	assert.Equal(t, "navigating to /login", tr.Stdout)

	require.Len(t, tr.Attachments, 2)
	assert.Equal(t, "screenshot", tr.Attachments[0].Type)
	assert.Equal(t, "image/png", tr.Attachments[0].MimeType)

	require.Len(t, tr.Steps, 3)
	assert.Equal(t, "navigate", tr.Steps[0].Name)
	assert.Equal(t, "pass", tr.Steps[0].Outcome)

	require.Len(t, tr.Retries, 1)
	assert.Equal(t, "fail", tr.Retries[0].Outcome)

	require.Len(t, tr.Links, 1)
	assert.Equal(t, "trace", tr.Links[0].Label)

	assert.Equal(t, "junit-xml", tr.SourceFormat)
	assert.Equal(t, "chromium", tr.Extras["browser"])
}

func TestWriteAndRead_EmptyOptionalCollections(t *testing.T) {
	root := t.TempDir()
	setupExecutionDir(t, root)

	rf := &ResultsFile{
		SchemaVersion: "0.1",
		TaskID:        "task-empty001",
		Framework:     "bats",
		Adapter:       "bats-runner",
		StartedAt:     "2026-04-25T10:00:00Z",
		CompletedAt:   "2026-04-25T10:00:01Z",
		Results: []TestResult{
			{
				TCID:       "tc-def45678",
				Outcome:    "pass",
				DurationMS: 50,
				// All optional collections left empty/nil
			},
		},
	}

	path, err := Write(root, rf)
	require.NoError(t, err)

	readRF, err := Read(path)
	require.NoError(t, err)

	tr := readRF.Results[0]
	assert.Equal(t, "pass", tr.Outcome)
	assert.Empty(t, tr.Message)
	assert.Empty(t, tr.StackTrace)
	assert.Empty(t, tr.Stdout)
	assert.Empty(t, tr.Stderr)
	assert.Nil(t, tr.Attachments)
	assert.Nil(t, tr.Steps)
	assert.Nil(t, tr.Retries)
	assert.Nil(t, tr.Links)
	assert.Nil(t, tr.Extras)
}

func TestWrite_FilenamePattern(t *testing.T) {
	root := t.TempDir()
	setupExecutionDir(t, root)

	rf := &ResultsFile{
		SchemaVersion: "0.1",
		TaskID:        "task-a1b2c3d4",
		Framework:     "jest",
		Adapter:       "jest-runner",
		StartedAt:     "2026-04-25T10:00:00Z",
		CompletedAt:   "2026-04-25T10:00:01Z",
		Results: []TestResult{
			{TCID: "tc-e5f6a7b8", Outcome: "pass"},
		},
	}

	path, err := Write(root, rf)
	require.NoError(t, err)

	assert.Equal(t, "task-a1b2c3d4--tc-e5f6a7b8.results.yaml", filepath.Base(path))
}

func TestResultsFilePath(t *testing.T) {
	path, err := ResultsFilePath("/projects/myapp", "task-abc12345", "tc-def67890")
	require.NoError(t, err)
	expected := filepath.Join("/projects/myapp", "gtms", "execution", "task-abc12345--tc-def67890.results.yaml")
	assert.Equal(t, expected, path)
}

func TestWrite_MissingTaskID(t *testing.T) {
	root := t.TempDir()

	rf := &ResultsFile{
		SchemaVersion: "0.1",
		Results:       []TestResult{{TCID: "tc-xxx", Outcome: "pass"}},
	}

	_, err := Write(root, rf)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "task ID is required")
}

func TestWrite_EmptyResults(t *testing.T) {
	root := t.TempDir()

	rf := &ResultsFile{
		SchemaVersion: "0.1",
		TaskID:        "task-notest01",
		Results:       []TestResult{},
	}

	_, err := Write(root, rf)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "at least one test result")
}

func TestWrite_RejectsMultipleResults(t *testing.T) {
	root := t.TempDir()

	rf := &ResultsFile{
		SchemaVersion: "0.1",
		TaskID:        "task-batch01",
		Results: []TestResult{
			{TCID: "tc-aaa11111", Outcome: "pass"},
			{TCID: "tc-bbb22222", Outcome: "fail"},
		},
	}

	path, err := Write(root, rf)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "batch execution results")
	assert.Empty(t, path)
	assert.NoFileExists(t, filepath.Join(root, "gtms", "execution", "task-batch01--tc-aaa11111.results.yaml"))
}

func TestWrite_MissingTCID(t *testing.T) {
	root := t.TempDir()

	rf := &ResultsFile{
		SchemaVersion: "0.1",
		TaskID:        "task-notcid01",
		Results:       []TestResult{{Outcome: "pass"}},
	}

	_, err := Write(root, rf)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "tc_id")
}

func TestExtrasMapRoundTrip(t *testing.T) {
	root := t.TempDir()
	setupExecutionDir(t, root)

	extras := map[string]interface{}{
		"browser":        "chromium",
		"viewport_width": 1280,
		"headless":       true,
		"custom_tag":     "regression",
	}

	rf := &ResultsFile{
		SchemaVersion: "0.1",
		TaskID:        "task-extras01",
		Framework:     "playwright",
		Adapter:       "pw-runner",
		StartedAt:     "2026-04-25T10:00:00Z",
		CompletedAt:   "2026-04-25T10:00:01Z",
		Results: []TestResult{
			{
				TCID:    "tc-ext12345",
				Outcome: "pass",
				Extras:  extras,
			},
		},
	}

	path, err := Write(root, rf)
	require.NoError(t, err)

	readRF, err := Read(path)
	require.NoError(t, err)

	readExtras := readRF.Results[0].Extras
	assert.Equal(t, "chromium", readExtras["browser"])
	assert.Equal(t, true, readExtras["headless"])
	assert.Equal(t, "regression", readExtras["custom_tag"])
	// YAML unmarshals integers as int, not float64 (unlike JSON)
	assert.Equal(t, 1280, readExtras["viewport_width"])
}

func TestRead_NonexistentFile(t *testing.T) {
	_, err := Read("/nonexistent/path/results.yaml")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reading results file")
}

func TestRead_MalformedYAML(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "bad.yaml")
	require.NoError(t, os.WriteFile(path, []byte("this is not valid yaml: [unclosed"), 0644))

	_, err := Read(path)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parsing results file")
}

// setupExecutionDir creates the gtms/execution/ directory under the given root.
func setupExecutionDir(t *testing.T, root string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms", "execution"), 0755))
}

// --- BUG-058: path separator sanitization at package boundary ---

func TestWrite_RejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	setupExecutionDir(t, root)

	tests := []struct {
		name   string
		taskID string
		tcID   string
	}{
		{"slash in taskID", "x/y", "tc-abc12345"},
		{"backslash in taskID", "x\\y", "tc-abc12345"},
		{"dotdot in taskID", "../escape", "tc-abc12345"},
		{"empty taskID", "", "tc-abc12345"},
		{"slash in tcID", "task-abc12345", "x/y"},
		{"backslash in tcID", "task-abc12345", "x\\y"},
		{"dotdot in tcID", "task-abc12345", "../escape"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rf := &ResultsFile{
				SchemaVersion: "0.1",
				TaskID:        tt.taskID,
				Framework:     "bats",
				Results: []TestResult{
					{TCID: tt.tcID, Outcome: "pass"},
				},
			}

			// Handle empty taskID which triggers the earlier "task ID is required" check
			if tt.taskID == "" {
				_, err := Write(root, rf)
				require.Error(t, err, "expected rejection for %s", tt.name)
				return
			}

			_, err := Write(root, rf)
			require.Error(t, err, "expected rejection for %s", tt.name)

			// Verify no file was written in the execution directory
			entries, _ := os.ReadDir(filepath.Join(root, "gtms", "execution"))
			assert.Empty(t, entries, "no results file should be created for unsafe input")
		})
	}
}

func TestResultsFilePath_RejectsPathTraversal(t *testing.T) {
	tests := []struct {
		name   string
		taskID string
		tcID   string
	}{
		{"slash in taskID", "x/y", "tc-abc12345"},
		{"backslash in taskID", "x\\y", "tc-abc12345"},
		{"dotdot in taskID", "../escape", "tc-abc12345"},
		{"empty taskID", "", "tc-abc12345"},
		{"slash in tcID", "task-abc12345", "x/y"},
		{"backslash in tcID", "task-abc12345", "x\\y"},
		{"dotdot in tcID", "task-abc12345", "../escape"},
		{"empty tcID", "task-abc12345", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ResultsFilePath("/projects/myapp", tt.taskID, tt.tcID)
			require.Error(t, err, "expected rejection for %s", tt.name)
		})
	}
}
