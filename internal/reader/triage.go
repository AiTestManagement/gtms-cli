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
	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/task"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// ValidTriageCategories lists the accepted triage category values.
var ValidTriageCategories = []string{"automation-wrong", "test-wrong", "app-wrong"}

// CON-023 / ENH-145 / ENH-146:
//
// Triage is rehomed to the wiring world. The legacy "mutate the
// automation record" path is retired — wiring is read-only on the
// triage path, the same as on the execute path. The three categories
// now map to:
//
//   - automation-wrong: queue a pending automate task carrying
//     reason: "triaged as wrong" provenance. No wiring write.
//   - test-wrong:       update the TC spec frontmatter status to
//     needs-review. No wiring write.
//   - app-wrong:        application/product is judged at fault, NOT
//     the automation. Does NOT queue an automate task (the automation
//     itself is presumed correct). When a defect link or triage summary
//     is supplied, the entry is appended to gtms/triage-history/<tc>.md
//     so the project keeps a permanent audit trail. With neither defect
//     nor summary, no history file is written (avoids noise). No wiring
//     write in any case.

// Triage is kept for backward compatibility; it delegates to GetTriageInfo.
func Triage(projectRoot string, testCaseID string) (*TriageInfo, error) {
	return GetTriageInfo(projectRoot, testCaseID, "")
}

// GetTriageInfo reads the current state of a test case for triage decision-making.
// The framework parameter determines which record to operate on. If framework is
// empty, GetTriageInfo auto-resolves from the wiring records for the TC.
func GetTriageInfo(projectRoot, testCaseID, framework string) (*TriageInfo, error) {
	resolved, err := resolveTriageFramework(projectRoot, testCaseID, framework)
	if err != nil {
		return nil, err
	}
	return getTriageInfoForFramework(projectRoot, testCaseID, resolved)
}

// resolveTriageFramework resolves the framework for triage operations.
// If framework is already specified, returns it directly. If empty,
// auto-detects from the wiring records for the TC.
func resolveTriageFramework(projectRoot, testCaseID, framework string) (string, error) {
	if framework != "" {
		return framework, nil
	}
	recs, err := wiring.FindAllForTC(projectRoot, testCaseID)
	if err != nil {
		return "", fmt.Errorf("reading wiring records: %w", err)
	}
	if len(recs) == 0 {
		return "", fmt.Errorf("no wiring record found for '%s'", testCaseID)
	}
	if len(recs) > 1 {
		var frameworks []string
		for _, r := range recs {
			frameworks = append(frameworks, r.Framework)
		}
		return "", fmt.Errorf("multiple wiring records found for '%s' (%s). Specify --framework",
			testCaseID, strings.Join(frameworks, ", "))
	}
	return recs[0].Framework, nil
}

// getTriageInfoForFramework reads triage info for a specific framework
// by joining the wiring record with the latest terminal result overlay.
func getTriageInfoForFramework(projectRoot, testCaseID, framework string) (*TriageInfo, error) {
	rec, _, err := wiring.Find(projectRoot, testCaseID, framework)
	if err != nil {
		return nil, fmt.Errorf("reading wiring record: %w", err)
	}
	if rec == nil {
		return nil, fmt.Errorf("no wiring record found for '%s' (framework: %s)", testCaseID, framework)
	}

	// Look up the latest terminal handoff for (testCase, framework).
	// scanTerminalResultsForTC loads every wiring record for this TC so
	// the ENH-146 join ladder sees the full sibling set — narrowing the
	// scope to only the selected record would let rung 4 (adapter
	// mapping) wrongly accept a frameworkless result whose adapter is
	// actually ambiguous across the TC's wiring.
	overlay, oErr := scanTerminalResultsForTC(projectRoot, testCaseID)
	if oErr != nil {
		return nil, fmt.Errorf("scanning terminal results: %w", oErr)
	}
	hit, hasResult := overlay[overlayKey(testCaseID, framework)]
	if !hasResult {
		return nil, fmt.Errorf("no execution results found for '%s'. Run 'gtms execute %s' first", testCaseID, testCaseID)
	}

	ar := &automationFrontmatter{
		TestCase:         rec.TestCase,
		Framework:        rec.Framework,
		Status:           "developed",
		Artefact:         rec.Artefact,
		Adapter:          rec.Adapter,
		Result:           hit.Result,
		ExecutedArtefact: hit.Artefact,
		ExecutedAt:       hit.ExecutedAt,
		ExecutedBy:       hit.ExecutedBy,
		Environment:      hit.Environment,
		ArtefactHash:     rec.ArtefactHash,
		TestCaseHash:     rec.TestCaseHash,
		Notes:            hit.Notes,
		Attempts:         hit.Attempts,
	}

	info := &TriageInfo{
		TestCaseID:       testCaseID,
		AutomationRecord: ar,
		LastResult:       hit.Result,
		LastRun:          hit.Artefact,
		Stale:            isStaleArtefact(projectRoot, *ar),
		FailureHistory:   []TriageEntry{},
	}
	return info, nil
}

// RecordTriage writes a triage decision and triggers the appropriate
// follow-on actions. Wiring is never mutated; instead automation-wrong
// queues a new automate task, test-wrong updates the TC spec, and
// app-wrong appends a defect link to a triage history log.
func RecordTriage(projectRoot string, testCaseID string, category string, summary string, defect string, framework string) (*TriageResult, error) {
	if !isValidCategory(category) {
		return nil, fmt.Errorf("invalid triage category: '%s'", category)
	}

	resolved, resolveErr := resolveTriageFramework(projectRoot, testCaseID, framework)
	if resolveErr != nil {
		return nil, resolveErr
	}
	framework = resolved

	// Confirm wiring exists for this (TC, framework).
	rec, _, err := wiring.Find(projectRoot, testCaseID, framework)
	if err != nil {
		return nil, fmt.Errorf("reading wiring record: %w", err)
	}
	if rec == nil {
		return nil, fmt.Errorf("no wiring record found for '%s' (framework: %s)", testCaseID, framework)
	}

	// Execution history must exist before triaging. Load every wiring
	// record for the TC so the join ladder's "exactly one matching
	// wiring" rule sees the full sibling set (a narrower view would let
	// rung 4 wrongly accept ambiguous adapter mappings — see the
	// scanTerminalResultsForTC docstring).
	overlay, oErr := scanTerminalResultsForTC(projectRoot, testCaseID)
	if oErr != nil {
		return nil, fmt.Errorf("scanning terminal results: %w", oErr)
	}
	if _, ok := overlay[overlayKey(testCaseID, framework)]; !ok {
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
		err = triageAutomationWrong(projectRoot, rec, testCaseID, summary, result)
	case "test-wrong":
		err = triageTestWrong(projectRoot, testCaseID, summary, result)
	case "app-wrong":
		err = triageAppWrong(projectRoot, rec, testCaseID, defect, summary, result)
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

// triageAutomationWrong queues a pending automate task with the
// "triaged as wrong" reason. The wiring record is read-only.
func triageAutomationWrong(projectRoot string, rec *wiring.WiringRecord, testCaseID, summary string, result *TriageResult) error {
	taskID := fmt.Sprintf("task-%s", id.New())
	body := fmt.Sprintf("## Triage: automation-wrong\n\nRe-automate %s after triage.\n\n**Reason:** triaged as wrong\n", testCaseID)
	if summary != "" {
		body += fmt.Sprintf("**Details:** %s\n", summary)
	}

	tf := &task.TaskFile{
		ID:        taskID,
		Type:      "automate",
		Target:    testCaseID,
		Status:    "pending",
		Created:   time.Now().UTC().Format(time.RFC3339),
		Framework: rec.Framework,
	}

	if _, err := task.Create(projectRoot, tf, body); err != nil {
		return fmt.Errorf("creating new automate task: %w", err)
	}

	result.NewTaskID = taskID
	result.Actions = append(result.Actions,
		fmt.Sprintf("New automate task queued: %s (reason: triaged as wrong)", taskID))
	return nil
}

// triageTestWrong updates the TC spec frontmatter status to needs-review.
// Wiring is not touched.
func triageTestWrong(projectRoot, testCaseID, summary string, result *TriageResult) error {
	if err := updateTestCaseStatus(projectRoot, testCaseID, "needs-review"); err != nil {
		return fmt.Errorf("updating test case status: %w", err)
	}
	action := fmt.Sprintf("Test case %s: status -> needs-review", testCaseID)
	if summary != "" {
		action += " (" + summary + ")"
	}
	result.Actions = append(result.Actions, action)
	return nil
}

// triageAppWrong records an app-wrong triage outcome. app-wrong means
// the application/product is at fault, NOT the automation, so no
// automate task is queued (the automation itself is presumed correct).
// When a defect link or summary is supplied, the entry is appended to
// gtms/triage-history/<tc>.md so the project keeps a permanent audit
// trail; with neither, no history file is written. Wiring is never
// mutated, and result.NewTaskID stays empty by contract.
//
// The wiring record parameter is retained for signature symmetry with
// the other triage handlers; it is intentionally unused here.
func triageAppWrong(projectRoot string, rec *wiring.WiringRecord, testCaseID, defect, summary string, result *TriageResult) error {
	_ = rec

	if defect != "" || summary != "" {
		if err := appendTriageHistory(projectRoot, testCaseID, defect, summary); err != nil {
			return fmt.Errorf("recording app-wrong triage history: %w", err)
		}
	}

	// NewTaskID stays empty: app-wrong does not queue an automate task.
	action := "Triage recorded: app-wrong"
	if defect != "" {
		action += fmt.Sprintf(" (defect: %s)", defect)
	}
	result.Actions = append(result.Actions, action)
	return nil
}

// appendTriageHistory appends one app-wrong triage entry to
// gtms/triage-history/<tc>.md. The directory and file are created on
// first call and appended on subsequent calls so the audit trail
// builds up over time. Entries are dated (UTC, RFC3339) and prefixed
// with the triage category so consumers can scan the file
// chronologically. Caller is responsible for the defect/summary
// non-empty pre-check — appending with both empty would produce a
// header-only entry that is intentionally not written.
func appendTriageHistory(projectRoot, testCaseID, defect, summary string) error {
	dir := filepath.Join(projectRoot, "gtms", "triage-history")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating triage-history directory: %w", err)
	}
	path := filepath.Join(dir, testCaseID+".md")

	var entry strings.Builder
	fmt.Fprintf(&entry, "## %s — app-wrong\n", time.Now().UTC().Format(time.RFC3339))
	if defect != "" {
		fmt.Fprintf(&entry, "- Defect: %s\n", defect)
	}
	if summary != "" {
		fmt.Fprintf(&entry, "- Summary: %s\n", summary)
	}
	entry.WriteString("\n")

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("opening triage history file: %w", err)
	}
	defer f.Close()
	if _, err := f.Write([]byte(entry.String())); err != nil {
		return fmt.Errorf("writing triage history entry: %w", err)
	}
	return nil
}

// updateTestCaseStatus finds a test case file and updates its status field.
func updateTestCaseStatus(projectRoot, testCaseID, newStatus string) error {
	tcDir := layout.CasesDir(projectRoot)

	var tcPath string
	err := filepath.Walk(tcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".md") {
			return nil
		}

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

	fm.Status = newStatus
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
