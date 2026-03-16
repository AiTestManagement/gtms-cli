package reader

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adrg/frontmatter"
	"gopkg.in/yaml.v3"

	"github.com/aitestmanagement/gtms-cli/internal/id"
	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
	"github.com/aitestmanagement/gtms-cli/internal/task"
)

// ValidTriageCategories lists the accepted triage category values.
var ValidTriageCategories = []string{"automation-wrong", "test-wrong", "app-wrong"}

// Triage is kept for backward compatibility; it delegates to GetTriageInfo.
func Triage(projectRoot string, testCaseID string) (*TriageInfo, error) {
	return GetTriageInfo(projectRoot, testCaseID)
}

// GetTriageInfo reads the current state of a test case for triage decision-making.
// It returns an error if the test case has no automation record or no execution history.
func GetTriageInfo(projectRoot, testCaseID string) (*TriageInfo, error) {
	// Find and read the automation record
	record, _, err := pipeline.FindAutomationRecord(projectRoot, testCaseID)
	if err != nil {
		return nil, fmt.Errorf("reading automation record: %w", err)
	}
	if record == nil {
		return nil, fmt.Errorf("no automation record found for '%s'", testCaseID)
	}

	// Verify execution history exists
	if record.LastFormalResult == "" {
		return nil, fmt.Errorf("no execution results found for '%s'. Run 'gtms execute %s' first", testCaseID, testCaseID)
	}

	// Convert pipeline.AutomationRecord to the reader's automationFrontmatter
	ar := &automationFrontmatter{
		TestCase:        record.TestCase,
		Framework:       record.Framework,
		Status:          record.Status,
		Artefact:        record.Artefact,
		Adapter:         record.Adapter,
		LastDevResult:   record.LastDevResult,
		LastFormalResult: record.LastFormalResult,
		LastFormalRun:   record.LastFormalRun,
		Attempts:        record.Attempts,
		Cycle:           record.Cycle,
		Defect:          record.Defect,
	}

	info := &TriageInfo{
		TestCaseID:       testCaseID,
		AutomationRecord: ar,
		LastResult:       record.LastFormalResult,
		LastRun:          record.LastFormalRun,
		FailureHistory:   []TriageEntry{}, // future: read from triage history file
	}

	return info, nil
}

// RecordTriage writes a triage decision and triggers the appropriate follow-on actions.
// category must be one of: automation-wrong, test-wrong, app-wrong.
func RecordTriage(projectRoot string, testCaseID string, category string, summary string, defect string) (*TriageResult, error) {
	// Validate category
	if !isValidCategory(category) {
		return nil, fmt.Errorf("invalid triage category: '%s'", category)
	}

	// Read the automation record
	record, recordPath, err := pipeline.FindAutomationRecord(projectRoot, testCaseID)
	if err != nil {
		return nil, fmt.Errorf("reading automation record: %w", err)
	}
	if record == nil {
		return nil, fmt.Errorf("no automation record found for '%s'", testCaseID)
	}

	// Verify execution history exists
	if record.LastFormalResult == "" {
		return nil, fmt.Errorf("no execution results found for '%s'. Run 'gtms execute %s' first", testCaseID, testCaseID)
	}

	result := &TriageResult{
		TestCaseID: testCaseID,
		Category:   category,
		Summary:    summary,
		Defect:     defect,
		Actions:    []string{},
	}

	switch category {
	case "automation-wrong":
		err = triageAutomationWrong(projectRoot, record, recordPath, testCaseID, summary, result)
	case "test-wrong":
		err = triageTestWrong(projectRoot, record, recordPath, testCaseID, summary, result)
	case "app-wrong":
		err = triageAppWrong(record, recordPath, testCaseID, defect, result)
	}

	if err != nil {
		return nil, err
	}

	return result, nil
}

// triageAutomationWrong handles the automation-wrong category:
// 1. Update automation record: status -> rework, increment cycle
// 2. Create new automate task in tasks/pending/
func triageAutomationWrong(projectRoot string, record *pipeline.AutomationRecord, recordPath string, testCaseID string, summary string, result *TriageResult) error {
	// Update automation record
	record.Status = "rework"
	record.Cycle++

	if err := pipeline.WriteAutomationRecord(recordPath, record); err != nil {
		return fmt.Errorf("updating automation record: %w", err)
	}
	result.Actions = append(result.Actions,
		fmt.Sprintf("Automation record: status -> rework, cycle %d", record.Cycle))

	// Create new automate task
	taskID := fmt.Sprintf("task-%s", id.New())
	body := fmt.Sprintf("## Triage: automation-wrong\n\nRe-automate %s after triage.\n", testCaseID)
	if summary != "" {
		body += fmt.Sprintf("\n**Reason:** %s\n", summary)
	}

	tf := &task.TaskFile{
		ID:      taskID,
		Type:    "automate",
		Target:  testCaseID,
		Status:  "pending",
		Created: time.Now().UTC().Format(time.RFC3339),
	}

	_, err := task.Create(projectRoot, tf, body)
	if err != nil {
		return fmt.Errorf("creating new automate task: %w", err)
	}

	result.NewTaskID = taskID
	result.Actions = append(result.Actions,
		fmt.Sprintf("New task created: %s-automate-%s", taskID, testCaseID))

	return nil
}

// triageTestWrong handles the test-wrong category:
// 1. Update automation record: status -> test-wrong
// 2. Update test case file: status -> needs-review
func triageTestWrong(projectRoot string, record *pipeline.AutomationRecord, recordPath string, testCaseID string, summary string, result *TriageResult) error {
	// Update automation record
	record.Status = "test-wrong"

	if err := pipeline.WriteAutomationRecord(recordPath, record); err != nil {
		return fmt.Errorf("updating automation record: %w", err)
	}
	result.Actions = append(result.Actions, "Automation record: status -> test-wrong")

	// Update test case file status
	if err := updateTestCaseStatus(projectRoot, testCaseID, "needs-review"); err != nil {
		return fmt.Errorf("updating test case status: %w", err)
	}
	result.Actions = append(result.Actions,
		fmt.Sprintf("Test case %s: status -> needs-review", testCaseID))

	return nil
}

// triageAppWrong handles the app-wrong category:
// 1. Update automation record: last-formal-result -> fail, add defect link
func triageAppWrong(record *pipeline.AutomationRecord, recordPath string, testCaseID string, defect string, result *TriageResult) error {
	// Update automation record
	record.LastFormalResult = "fail"
	if defect != "" {
		record.Defect = defect
	}

	if err := pipeline.WriteAutomationRecord(recordPath, record); err != nil {
		return fmt.Errorf("updating automation record: %w", err)
	}

	action := "Automation record: last-formal-result -> fail"
	if defect != "" {
		action += fmt.Sprintf(" (defect: %s)", defect)
	}
	result.Actions = append(result.Actions, action)

	return nil
}

// updateTestCaseStatus finds a test case file and updates its status field.
func updateTestCaseStatus(projectRoot, testCaseID, newStatus string) error {
	tcDir := filepath.Join(projectRoot, "test-cases")

	var tcPath string
	err := filepath.Walk(tcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".md") {
			return nil
		}

		// Check if this file matches the test case ID
		f, openErr := os.Open(path)
		if openErr != nil {
			return nil
		}
		defer f.Close()

		var fm testCaseFrontmatter
		_, parseErr := frontmatter.Parse(f, &fm)
		if parseErr != nil {
			return nil
		}

		if strings.EqualFold(fm.ID, testCaseID) {
			tcPath = path
			return filepath.SkipAll
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("searching for test case: %w", err)
	}
	if tcPath == "" {
		return fmt.Errorf("test case '%s' not found", testCaseID)
	}

	// Read the full file to get frontmatter and body
	f, err := os.Open(tcPath)
	if err != nil {
		return fmt.Errorf("opening test case file: %w", err)
	}

	var fm testCaseFrontmatter
	body, err := frontmatter.Parse(f, &fm)
	f.Close()
	if err != nil {
		return fmt.Errorf("parsing test case frontmatter: %w", err)
	}

	// Update status
	fm.Status = newStatus

	// Re-write the file
	yamlBytes, err := yaml.Marshal(&fm)
	if err != nil {
		return fmt.Errorf("marshalling updated frontmatter: %w", err)
	}

	content := fmt.Sprintf("---\n%s---\n%s", string(yamlBytes), string(body))
	if err := os.WriteFile(tcPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("writing updated test case: %w", err)
	}

	return nil
}

// isValidCategory checks if a category string is a valid triage category.
func isValidCategory(category string) bool {
	for _, c := range ValidTriageCategories {
		if c == category {
			return true
		}
	}
	return false
}
