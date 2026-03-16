// Package pipeline bridges transient result contracts to permanent git-committed records.
// It creates automation records from completed automate tasks and updates them with execution results.
package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/adrg/frontmatter"
	"gopkg.in/yaml.v3"

	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/aitestmanagement/gtms-cli/internal/task"
)

// AutomationRecord represents a permanent automation record stored in test-automation/records/.
type AutomationRecord struct {
	TestCase        string `yaml:"testcase"`
	Framework       string `yaml:"framework,omitempty"`
	Status          string `yaml:"status"`                        // developed, accepted
	Artefact        string `yaml:"artefact,omitempty"`
	Branch          string `yaml:"branch,omitempty"`
	Adapter         string `yaml:"adapter,omitempty"`
	LastDevResult   string `yaml:"last-dev-result,omitempty"`     // pass or fail
	Attempts        int    `yaml:"attempts,omitempty"`
	Summary         string `yaml:"summary,omitempty"`
	Log             string `yaml:"log,omitempty"`
	Cycle           int    `yaml:"cycle"`
	LastFormalResult string `yaml:"last-formal-result,omitempty"` // pass or fail
	LastFormalRun   string `yaml:"last-formal-run,omitempty"`
	Defect          string `yaml:"defect,omitempty"`
}

// BuildAutomationRecord creates or updates an automation record from a completed automate task.
// The record is stored at test-automation/records/{tc-id}.automation.md.
func BuildAutomationRecord(projectRoot string, tf *task.TaskFile, rc *result.ResultContract) error {
	if tf == nil {
		return fmt.Errorf("task file is required")
	}
	if rc == nil {
		return fmt.Errorf("result contract is required")
	}

	// Ensure the records directory exists
	dir := filepath.Join(projectRoot, "test-automation", "records")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating test-automation/records directory: %w", err)
	}

	recordPath := filepath.Join(dir, fmt.Sprintf("%s.automation.md", tf.Target))

	// Check if an existing record exists (for cycle counting)
	cycle := 1
	existing, err := ReadAutomationRecord(recordPath)
	if err == nil && existing != nil {
		cycle = existing.Cycle + 1
	}

	// Determine last-dev-result
	lastDevResult := "fail"
	if rc.Status == "complete" {
		lastDevResult = "pass"
	}

	record := &AutomationRecord{
		TestCase:      tf.Target,
		Framework:     tf.Framework,
		Status:        "developed",
		Artefact:      rc.Artefact,
		Branch:        tf.Branch,
		Adapter:       tf.Adapter,
		LastDevResult: lastDevResult,
		Attempts:      rc.Attempts,
		Summary:       rc.Summary,
		Log:           rc.Log,
		Cycle:         cycle,
	}

	return WriteAutomationRecord(recordPath, record)
}

// UpdateExecutionResult updates an automation record with execution results.
func UpdateExecutionResult(projectRoot string, tf *task.TaskFile, rc *result.ResultContract) error {
	if tf == nil {
		return fmt.Errorf("task file is required")
	}
	if rc == nil {
		return fmt.Errorf("result contract is required")
	}

	recordPath := filepath.Join(projectRoot, "test-automation", "records",
		fmt.Sprintf("%s.automation.md", tf.Target))

	existing, err := ReadAutomationRecord(recordPath)
	if err != nil {
		return fmt.Errorf("reading automation record for update: %w", err)
	}

	// Determine formal result
	lastFormalResult := "fail"
	if rc.Status == "complete" {
		lastFormalResult = "pass"
	}

	existing.LastFormalResult = lastFormalResult
	existing.LastFormalRun = rc.Artefact

	return WriteAutomationRecord(recordPath, existing)
}

// ReadAutomationRecord reads an automation record from the given path.
func ReadAutomationRecord(path string) (*AutomationRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening automation record: %w", err)
	}
	defer f.Close()

	var record AutomationRecord
	_, err = frontmatter.Parse(f, &record)
	if err != nil {
		return nil, fmt.Errorf("parsing automation record frontmatter: %w", err)
	}

	return &record, nil
}

// WriteAutomationRecord writes an automation record to the given path as markdown with YAML frontmatter.
func WriteAutomationRecord(path string, record *AutomationRecord) error {
	fm, err := yaml.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshalling automation record: %w", err)
	}

	content := fmt.Sprintf("---\n%s---\n", string(fm))
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("writing automation record: %w", err)
	}

	return nil
}

// FindAutomationRecord looks for an automation record for the given test case ID.
// Returns the record and its path, or nil if not found.
func FindAutomationRecord(projectRoot, testCaseID string) (*AutomationRecord, string, error) {
	recordPath := filepath.Join(projectRoot, "test-automation", "records",
		fmt.Sprintf("%s.automation.md", testCaseID))

	record, err := ReadAutomationRecord(recordPath)
	if err != nil {
		if os.IsNotExist(err) || strings.Contains(err.Error(), "opening automation record") {
			return nil, "", nil
		}
		return nil, "", err
	}

	return record, recordPath, nil
}
