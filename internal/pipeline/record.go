// record.go provides a reusable record-write primitive for creating automation
// records from caller-supplied options. This is the shared logic used by
// gtms link (ENH-111) and, in the future, gtms automate (ENH-112/113).
//
// The existing BuildAutomationRecord function in pipeline.go is tightly coupled
// to TaskFile + ResultContract. CreateAutomationRecord takes a RecordOptions
// struct instead, making it usable from any caller that has the required data.
package pipeline

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
	"github.com/aitestmanagement/gtms-cli/internal/result"
)

// RecordOptions carries caller-provided values for record creation.
// The primitive handles shared plumbing: directory creation, existing-record
// detection, cycle counting, path normalisation, artefact hashing, write.
type RecordOptions struct {
	TestCase      string // required: TC ID (e.g. "tc-abc123")
	Framework     string // required: framework name (e.g. "playwright")
	Artefact      string // required: path to test file
	Adapter       string // required: provenance (e.g. "manual-link", "claude-playwright")
	LastDevResult string // required: "linked", "verified", "pass", "fail", "error"
	Branch        string // optional: current git branch
	Summary       string // optional: adapter output summary
	Log           string // optional: adapter log output
	Environment   string // ENH-125: optional target environment (e.g. staging, production)
	ExecutedBy    string // ENH-125: optional pre-resolved identity (flag → env → git user.name)
	Force         bool   // if true, overwrite existing record and clear execute history
}

// CreateAutomationRecord creates or overwrites an automation record.
// Shared logic:
//  1. Ensure gtms/automation/records/ directory exists
//  2. Check for existing record -- refuse without Force, increment cycle with Force
//  3. Normalise artefact path (filepath.ToSlash)
//  4. Compute artefact SHA-256 hash
//  5. Build AutomationRecord struct with caller values + computed fields
//  6. Write via WriteAutomationRecord
//
// Returns an error if the record already exists and Force is false.
func CreateAutomationRecord(projectRoot string, opts RecordOptions) error {
	// BUG-058: reject identifiers containing path separators or traversal sequences
	// before they are used in filename construction.
	if err := pathsafe.ValidateFilenameComponent(opts.TestCase, "test case ID"); err != nil {
		return err
	}
	if err := pathsafe.ValidateFilenameComponent(opts.Framework, "framework"); err != nil {
		return err
	}

	// 1. Ensure records directory exists
	dir := layout.RecordsDir(projectRoot)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating records directory: %w", err)
	}

	// 2. Build record path and check for existing record
	recordPath := filepath.Join(dir, fmt.Sprintf("%s--%s.automation.md", opts.TestCase, opts.Framework))

	cycle := 1
	existing, err := readExistingAutomationRecord(recordPath, opts.Force)
	if err != nil {
		return err
	}
	if existing != nil {
		if !opts.Force {
			return fmt.Errorf("automation record already exists for %s--%s; use --force to overwrite",
				opts.TestCase, opts.Framework)
		}
		cycle = existing.Cycle + 1
	}

	// 3. Normalise artefact path to forward slashes
	normalisedArtefact := filepath.ToSlash(opts.Artefact)

	// 4. Compute artefact hash (best-effort; empty on error)
	artefactHash, _ := HashFile(AbsArtefactPath(projectRoot, opts.Artefact))

	// 5. Build the record -- zero-value fields are omitted by omitempty,
	// which naturally clears stale execute/diagnostic state on --force.
	record := &AutomationRecord{
		RecordCommon: RecordCommon{
			TestCase:    opts.TestCase,
			Framework:   opts.Framework,
			Status:      "developed",
			Branch:      opts.Branch,
			Environment: opts.Environment, // ENH-125
			ExecutedBy:  opts.ExecutedBy,  // ENH-125
		},
		Artefact:      normalisedArtefact,
		Adapter:       opts.Adapter,
		LastDevResult: opts.LastDevResult,
		Attempts:      1,
		Cycle:         cycle,
		ArtefactHash:  artefactHash,
		Summary:       opts.Summary,
	}

	// ENH-117: compute testcase-hash from the resolved spec file. Best-effort:
	// a missing spec or hash error leaves the field empty (omitempty drops it).
	if specPath, specErr := ResolveTestCaseSpec(projectRoot, opts.TestCase); specErr == nil {
		if hash, hashErr := HashFile(filepath.Join(projectRoot, filepath.FromSlash(specPath))); hashErr == nil {
			record.TestCaseHash = hash
		}
	}

	// Apply notes truncation if provided (same cap as BuildAutomationRecord)
	if opts.Log != "" {
		truncatedNotes, _ := result.TruncateUTF8(opts.Log, result.NotesSizeCapBytes)
		record.Notes = truncatedNotes
	}

	// 6. Write the record
	return WriteAutomationRecord(recordPath, record)
}
