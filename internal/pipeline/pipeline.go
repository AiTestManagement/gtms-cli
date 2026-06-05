// Package pipeline bridges transient result contracts to permanent git-committed records.
// It creates automation records from completed automate tasks and updates them with execution results.
package pipeline

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adrg/frontmatter"
	"gopkg.in/yaml.v3"

	"github.com/aitestmanagement/gtms-cli/internal/execution"
	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/aitestmanagement/gtms-cli/internal/task"
)

// summarySizeCapBytes is the maximum number of bytes retained in the committed
// automation record's `summary:` field. BUG-075: pre-BUG-070 the summary was
// stable automate-time text; post-BUG-070 it carries execute-time content
// (including full stderr for Tier 1 fails). The cap is applied at the
// persistence boundary (UpdateExecutionResult) so it defends every tier.
const summarySizeCapBytes = 1024

// logSizeCapBytes is an alias retained for backward compatibility with the
// end-to-end pipeline tests (TestBuildAutomationRecord_TruncatesOversizeLog…,
// TestUpdateExecutionResult_TruncatesOversizeLog…) that reference the old
// name. Points to the exported result.NotesSizeCapBytes (BUG-084 lifted the
// helpers to internal/result/). May be retired in a follow-up CON-023 cleanup
// PRP when pipeline.BuildAutomationRecord / UpdateExecutionResult are deleted.
const logSizeCapBytes = result.NotesSizeCapBytes

// RecordCommon holds fields shared between automation records and (future)
// manual result records. Both embed RecordCommon to share a uniform schema
// for dashboard rendering. Introduced by ENH-123 as a CON-020 prerequisite.
type RecordCommon struct {
	TestCase    string   `yaml:"testcase"`
	Framework   string   `yaml:"framework,omitempty"`
	Status      string   `yaml:"status"`                // developed, accepted
	Result      string   `yaml:"result,omitempty"`      // pass, fail, error, or skipped (was last-formal-result)
	ExecutedAt  string   `yaml:"executed_at,omitempty"` // RFC3339 UTC timestamp (was last-formal-run-at)
	ExecutedBy  string   `yaml:"executed_by,omitempty"` // CI runner identity or tester name
	Environment string   `yaml:"environment,omitempty"` // target environment (staging, production, etc.)
	Branch      string   `yaml:"branch,omitempty"`
	Defect      []string `yaml:"defect,omitempty"` // linked defect IDs (was string; ENH-123 changed to array)
	Notes       string   `yaml:"notes,omitempty"`  // diagnostic output / tester notes (was log)
}

// AutomationRecord represents a permanent automation record stored in gtms/automation/records/.
// It embeds RecordCommon for the shared field schema and adds automation-specific fields.
type AutomationRecord struct {
	RecordCommon     `yaml:",inline"`
	Artefact         string `yaml:"artefact,omitempty"`
	Adapter          string `yaml:"adapter,omitempty"`
	LastDevResult    string `yaml:"last-dev-result,omitempty"` // pass, fail, or error
	Attempts         int    `yaml:"attempts,omitempty"`
	Summary          string `yaml:"summary,omitempty"`
	NotesSpill       string `yaml:"notes-spill,omitempty"` // ENH-077: relative path to .gtms/logs/{task-id}.log when notes was truncated (was log-spill)
	Cycle            int    `yaml:"cycle"`
	ExecutedArtefact string `yaml:"executed_artefact,omitempty"` // path to executed artefact (was last-formal-run)
	ArtefactHash     string `yaml:"artefact-hash,omitempty"`
	TestCaseHash     string `yaml:"testcase-hash,omitempty"` // ENH-117: SHA-256 of the test case spec at record-write time
	ResultsFile      string `yaml:"results-file,omitempty"`  // ENH-109: pointer to per-test results YAML in gtms/execution/
}

// BuildAutomationRecord creates or updates an automation record from a completed automate task.
// The record is stored at gtms/automation/records/{tc-id}.automation.md.
func BuildAutomationRecord(projectRoot string, tf *task.TaskFile, rc *result.ResultContract) error {
	if tf == nil {
		return fmt.Errorf("task file is required")
	}
	if rc == nil {
		return fmt.Errorf("result contract is required")
	}

	// BUG-058: reject identifiers containing path separators or traversal sequences.
	if err := pathsafe.ValidateFilenameComponent(tf.Target, "test case ID"); err != nil {
		return err
	}
	if err := pathsafe.ValidateFilenameComponent(tf.Framework, "framework"); err != nil {
		return err
	}

	// Ensure the records directory exists
	dir := layout.RecordsDir(projectRoot)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating gtms/automation/records directory: %w", err)
	}

	recordPath := filepath.Join(dir, fmt.Sprintf("%s--%s.automation.md", tf.Target, tf.Framework))

	// Check if an existing record exists (for cycle counting)
	cycle := 1
	existing, err := readExistingAutomationRecord(recordPath, false)
	if err != nil {
		return err
	}
	if existing != nil {
		cycle = existing.Cycle + 1
	}

	// ENH-130: derive outcome from orthogonal contract via helpers.
	lastDevResult := recordResultFromContract(rc)

	// ENH-092: apply the same notes truncation as UpdateExecutionResult.
	// Oversize content (> result.NotesSizeCapBytes) is truncated at a UTF-8 boundary
	// and the full content spills to `.gtms/logs/{task-id}.log`.
	truncatedNotes, wasTruncated := result.TruncateUTF8(rc.Log, result.NotesSizeCapBytes)

	record := &AutomationRecord{
		RecordCommon: RecordCommon{
			TestCase:    tf.Target,
			Framework:   tf.Framework,
			Status:      "developed",
			Branch:      tf.Branch,
			Notes:       truncatedNotes,
			Environment: tf.Environment, // ENH-125: --env flag value rides task → record
			ExecutedBy:  tf.ExecutedBy,  // ENH-125: resolved identity rides task → record
		},
		Artefact:      rc.Artefact,
		Adapter:       tf.Adapter,
		LastDevResult: lastDevResult,
		Attempts:      rc.Attempts,
		Summary:       rc.Summary,
		Cycle:         cycle,
	}

	if wasTruncated {
		spillPath, spillErr := result.WriteLogSpill(projectRoot, tf.ID, rc.Log)
		if spillErr != nil {
			record.NotesSpill = ""
		} else {
			record.NotesSpill = spillPath
		}
	}

	// ENH-117: compute testcase-hash from the resolved spec file. Best-effort:
	// a missing spec or hash error leaves the field empty (omitempty drops it).
	if specPath, specErr := ResolveTestCaseSpec(projectRoot, tf.Target); specErr == nil {
		if hash, hashErr := HashFile(filepath.Join(projectRoot, filepath.FromSlash(specPath))); hashErr == nil {
			record.TestCaseHash = hash
		}
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

	// BUG-058: reject identifiers containing path separators or traversal sequences.
	if err := pathsafe.ValidateFilenameComponent(tf.Target, "test case ID"); err != nil {
		return err
	}
	if err := pathsafe.ValidateFilenameComponent(tf.Framework, "framework"); err != nil {
		return err
	}

	recordPath := filepath.Join(layout.RecordsDir(projectRoot),
		fmt.Sprintf("%s--%s.automation.md", tf.Target, tf.Framework))

	existing, err := ReadAutomationRecord(recordPath)
	if err != nil {
		return fmt.Errorf("reading automation record for update: %w", err)
	}

	// ENH-130: derive outcome from orthogonal contract via helpers.
	formalResult := recordResultFromContract(rc)

	existing.Result = formalResult
	existing.ExecutedArtefact = rc.Artefact
	// BUG-070: propagate the adapter's runtime summary to the automation
	// record. Always overwrite — a passing run clears any stale automate-time
	// summary, and an empty rc.Summary correctly blanks the field (latest
	// execution state wins, matching Notes semantics).
	// BUG-075: cap at summarySizeCapBytes so oversize Tier 1 stderr doesn't
	// blow up the committed record. The full payload is already in notes:.
	truncSummary, summaryWasTruncated := result.TruncateUTF8(rc.Summary, summarySizeCapBytes)
	if summaryWasTruncated {
		truncSummary += " … (truncated; see notes:)"
	}
	existing.Summary = truncSummary
	// ENH-125: overwrite environment + executed_by from the task file so
	// each execute run reflects the run that produced the result. An empty
	// value (no --env supplied, no resolvable identity) is preserved as
	// empty so the YAML marshaller drops the key via omitempty.
	existing.Environment = tf.Environment
	existing.ExecutedBy = tf.ExecutedBy
	// Prefer the adapter-reported `completed:` timestamp from the result
	// contract so the detail view can surface it without scanning
	// .gtms/results/; fall back to now() only when an adapter/invoker
	// failed to set it (pathological Tier 2 case).
	if rc.Completed != "" {
		existing.ExecutedAt = rc.Completed
	} else {
		existing.ExecutedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if rc.ArtefactHash != "" {
		existing.ArtefactHash = rc.ArtefactHash
	}

	// ENH-077: copy the diagnostic log payload from the transient result
	// contract into the committed automation record so it survives a
	// `.gtms/` wipe (ADR-011). Always overwrite — a passing run clears any
	// prior failure notes, and an empty rc.Log correctly blanks the field.
	// Oversize content (> result.NotesSizeCapBytes) is truncated at a UTF-8 boundary
	// and the full content spills to `.gtms/logs/{task-id}.log`.
	truncatedNotes, wasTruncated := result.TruncateUTF8(rc.Log, result.NotesSizeCapBytes)
	existing.Notes = truncatedNotes
	if wasTruncated {
		spillPath, spillErr := result.WriteLogSpill(projectRoot, tf.ID, rc.Log)
		if spillErr != nil {
			// Spill is best-effort — the truncated notes still land on the
			// record so the most important diagnostic bytes are committed
			// even when `.gtms/` is unwritable.
			existing.NotesSpill = ""
		} else {
			existing.NotesSpill = spillPath
		}
	} else {
		existing.NotesSpill = ""
	}

	// BUG-064: Write per-test results file to gtms/execution/.
	// The ResultsFile synthesises a single minimal TestResult row from
	// task-level data. Richer per-test data (steps, retries, attachments)
	// requires adapters to emit it through the handoff contract — out of scope.
	completedAt := rc.Completed
	if completedAt == "" {
		completedAt = time.Now().UTC().Format(time.RFC3339)
	}
	rf := &execution.ResultsFile{
		SchemaVersion: "0.1",
		TaskID:        tf.ID,
		Framework:     tf.Framework,
		Adapter:       rc.Adapter,
		StartedAt:     tf.Created,
		CompletedAt:   completedAt,
		Artefact:      rc.Artefact,
		Results: []execution.TestResult{
			{
				TCID:    tf.Target,
				Outcome: contractOutcome(rc),
				Message: rc.Summary,
			},
		},
	}
	rfPath, writeErr := execution.Write(projectRoot, rf)
	if writeErr != nil {
		// Soft warning: the automation record already captures the data.
		// Append a note so the issue is visible but do not fail the task.
		if existing.Notes != "" {
			existing.Notes += "\n"
		}
		existing.Notes += "execution results file could not be written: " + writeErr.Error()
	} else {
		// Store the project-relative path (forward-slash normalised) on the
		// automation record so readers can locate the file without scanning.
		if rel, relErr := filepath.Rel(projectRoot, rfPath); relErr == nil {
			existing.ResultsFile = filepath.ToSlash(rel)
		}
	}

	return WriteAutomationRecord(recordPath, existing)
}

// contractOutcome resolves the canonical test outcome from a contract.
// The contract's Result field is authoritative when set; an error status
// with no Result lifts to outcome "error" (adapter crashed, outcome
// unknown). Validation at result.Update should already have rejected
// a Status: complete with empty Result.
//
// ENH-130: replaces the legacy mapOutcome four-way switch on rc.Status.
func contractOutcome(rc *result.ResultContract) string {
	if rc.Result != "" {
		return rc.Result
	}
	if rc.Status == "error" {
		return "error"
	}
	// Defensive: should not happen post-validation. Treat as error
	// so a misbehaving adapter doesn't silently render as pass.
	return "error"
}

// recordResultFromContract resolves the contract outcome and applies the
// contract-to-record vocabulary mapping. The only conversion is
// skip -> skipped (Q10: contract uses "skip", automation record uses
// "skipped" per ENH-123).
//
// ENH-130: replaces inline four-way derivation switches in
// BuildAutomationRecord and UpdateExecutionResult.
func recordResultFromContract(rc *result.ResultContract) string {
	outcome := contractOutcome(rc)
	if outcome == "skip" {
		return "skipped"
	}
	return outcome
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

func readExistingAutomationRecord(recordPath string, forceOverwriteUnreadable bool) (*AutomationRecord, error) {
	existing, err := ReadAutomationRecord(recordPath)
	if err == nil {
		return existing, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if forceOverwriteUnreadable {
		return nil, nil
	}
	return nil, fmt.Errorf("existing automation record at %s is unreadable (corrupted?): %w", recordPath, err)
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

// FindAutomationRecord looks for a framework-qualified automation record for the given test case ID.
// The framework parameter determines which record to find (e.g. "bats" -> tc-xxx--bats.automation.md).
// Returns the record and its path, or nil if not found.
func FindAutomationRecord(projectRoot, testCaseID, framework string) (*AutomationRecord, string, error) {
	// BUG-058: reject identifiers containing path separators or traversal sequences.
	if err := pathsafe.ValidateFilenameComponent(testCaseID, "test case ID"); err != nil {
		return nil, "", err
	}
	if err := pathsafe.ValidateFilenameComponent(framework, "framework"); err != nil {
		return nil, "", err
	}

	recordPath := filepath.Join(layout.RecordsDir(projectRoot),
		fmt.Sprintf("%s--%s.automation.md", testCaseID, framework))

	record, err := ReadAutomationRecord(recordPath)
	if err != nil {
		if os.IsNotExist(err) || strings.Contains(err.Error(), "opening automation record") {
			return nil, "", nil
		}
		return nil, "", err
	}

	return record, recordPath, nil
}
