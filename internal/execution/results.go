// Package execution manages per-test execution results (CON-016 schema).
//
// These files are distinct from the ADR-005 handoff contracts in internal/result.
// Handoff contracts (.gtms/results/*.handoff.yaml) are transient GTMS-adapter
// protocol files. Per-test results (gtms/execution/*.results.yaml) are committed
// pipeline state that carries rich test outcome data.
//
// ENH-109 introduces this package as the foundational data layer for the
// convention-based pipeline.
package execution

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
	"gopkg.in/yaml.v3"
)

// ResultsFile represents a per-test execution results file.
// Filename convention: task-{id}--tc-{id}.results.yaml
// Location: gtms/execution/
//
// The Results array is future-proofed for batch execute (one invocation,
// multiple TCs). In v1, every execute task produces exactly one entry.
type ResultsFile struct {
	SchemaVersion string       `yaml:"schema_version"`
	TaskID        string       `yaml:"task_id"`
	Framework     string       `yaml:"framework"`
	Adapter       string       `yaml:"adapter"`
	StartedAt     string       `yaml:"started_at"`
	CompletedAt   string       `yaml:"completed_at"`
	Artefact      string       `yaml:"artefact,omitempty"`
	Results       []TestResult `yaml:"results"`
}

// TestResult represents one test case result within an execution.
type TestResult struct {
	TCID         string                 `yaml:"tc_id"`
	Outcome      string                 `yaml:"outcome"` // pass, fail, skip, error
	StartedAt    string                 `yaml:"started_at,omitempty"`
	DurationMS   int                    `yaml:"duration_ms,omitempty"`
	Message      string                 `yaml:"message,omitempty"`
	StackTrace   string                 `yaml:"stack_trace,omitempty"`
	Stdout       string                 `yaml:"stdout,omitempty"`
	Stderr       string                 `yaml:"stderr,omitempty"`
	Attachments  []Attachment           `yaml:"attachments,omitempty"`
	Steps        []Step                 `yaml:"steps,omitempty"`
	Retries      []Retry                `yaml:"retries,omitempty"`
	Links        []Link                 `yaml:"links,omitempty"`
	Framework    string                 `yaml:"framework,omitempty"`
	Adapter      string                 `yaml:"adapter,omitempty"`
	SourceFormat string                 `yaml:"source_format,omitempty"`
	Extras       map[string]interface{} `yaml:"extras,omitempty"`
}

// Attachment describes a file attachment (screenshot, video, trace, etc.).
type Attachment struct {
	Type     string `yaml:"type"`
	Path     string `yaml:"path"`
	MimeType string `yaml:"mime_type,omitempty"`
}

// Step represents a per-step breakdown within a test.
type Step struct {
	Name       string `yaml:"name"`
	Outcome    string `yaml:"outcome"`
	DurationMS int    `yaml:"duration_ms,omitempty"`
}

// Retry represents a prior attempt when the framework retried a test.
type Retry struct {
	Outcome    string `yaml:"outcome"`
	DurationMS int    `yaml:"duration_ms,omitempty"`
	Message    string `yaml:"message,omitempty"`
}

// Link is an external URL reference (trace viewer, CI page, etc.).
type Link struct {
	Label string `yaml:"label"`
	URL   string `yaml:"url"`
}

// Write writes a per-test results file to gtms/execution/.
// The filename is task-{id}--tc-{id}.results.yaml, derived from the first
// TestResult entry's TCID. Returns the absolute filepath of the written file.
func Write(projectRoot string, rf *ResultsFile) (string, error) {
	if rf.TaskID == "" {
		return "", fmt.Errorf("results file task ID is required")
	}
	if len(rf.Results) == 0 {
		return "", fmt.Errorf("results file must contain at least one test result")
	}
	if len(rf.Results) != 1 {
		return "", fmt.Errorf("batch execution results (N=%d) not yet supported; write one ResultsFile per test case", len(rf.Results))
	}

	// Derive TC ID from the first result entry for the filename
	tcID := rf.Results[0].TCID
	if tcID == "" {
		return "", fmt.Errorf("first test result must have a tc_id")
	}

	// BUG-058: reject identifiers containing path separators or traversal sequences
	// before they are used in filename construction.
	if err := pathsafe.ValidateFilenameComponent(rf.TaskID, "task ID"); err != nil {
		return "", err
	}
	if err := pathsafe.ValidateFilenameComponent(tcID, "test case ID"); err != nil {
		return "", err
	}

	dir := layout.ExecutionDir(projectRoot)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("creating execution directory: %w", err)
	}

	filename := fmt.Sprintf("%s--%s.results.yaml", rf.TaskID, tcID)
	path := filepath.Join(dir, filename)

	data, err := yaml.Marshal(rf)
	if err != nil {
		return "", fmt.Errorf("marshalling results file: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", fmt.Errorf("writing results file: %w", err)
	}

	return path, nil
}

// Read parses a per-test results file from the given path.
func Read(path string) (*ResultsFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading results file: %w", err)
	}

	var rf ResultsFile
	if err := yaml.Unmarshal(data, &rf); err != nil {
		return nil, fmt.Errorf("parsing results file: %w", err)
	}

	return &rf, nil
}

// ResultsFilePath returns the expected path for a results file given
// a project root, task ID, and test case ID.
//
// BUG-058: validates that taskID and tcID are safe filename components.
func ResultsFilePath(projectRoot, taskID, tcID string) (string, error) {
	if err := pathsafe.ValidateFilenameComponent(taskID, "task ID"); err != nil {
		return "", err
	}
	if err := pathsafe.ValidateFilenameComponent(tcID, "test case ID"); err != nil {
		return "", err
	}
	return filepath.Join(layout.ExecutionDir(projectRoot),
		fmt.Sprintf("%s--%s.results.yaml", taskID, tcID)), nil
}
