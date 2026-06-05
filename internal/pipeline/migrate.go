// migrate.go provides a one-shot migration function for pre-release automation
// records that still use the old field names (ENH-123).
//
// The migration renames YAML keys:
//   last-formal-result  -> result
//   last-formal-run     -> executed_artefact
//   last-formal-run-at  -> executed_at
//   log                 -> notes
//   log-spill           -> notes-spill
//   defect (string)     -> defect ([]string)
//
// New fields (executed_by, environment) are left blank.
// The migration is idempotent: records already using new names are skipped.
package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/adrg/frontmatter"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
)

// legacyAutomationRecord mirrors the old field names for reading pre-ENH-123
// automation records. Only used during migration.
type legacyAutomationRecord struct {
	TestCase        string `yaml:"testcase"`
	Framework       string `yaml:"framework"`
	Status          string `yaml:"status"`
	Artefact        string `yaml:"artefact"`
	Branch          string `yaml:"branch"`
	Adapter         string `yaml:"adapter"`
	LastDevResult   string `yaml:"last-dev-result"`
	LastFormalResult string `yaml:"last-formal-result"`
	LastFormalRun   string `yaml:"last-formal-run"`
	LastFormalRunAt string `yaml:"last-formal-run-at"`
	ArtefactHash    string `yaml:"artefact-hash"`
	TestCaseHash    string `yaml:"testcase-hash"` // ENH-117: preserved during migration
	Log             string `yaml:"log"`
	LogSpill        string `yaml:"log-spill"`
	Attempts        int    `yaml:"attempts"`
	Summary         string `yaml:"summary"`
	Cycle           int    `yaml:"cycle"`
	Defect          string `yaml:"defect"`
	ResultsFile     string `yaml:"results-file"`
	// New fields (read but likely empty in legacy records)
	Result           string `yaml:"result"`
	ExecutedArtefact string `yaml:"executed_artefact"`
	ExecutedAt       string `yaml:"executed_at"`
	Notes            string `yaml:"notes"`
	NotesSpill       string `yaml:"notes-spill"`
}

// MigrateAutomationRecords walks gtms/automation/records/ and migrates any
// records that still use the old field names to the new ENH-123 schema.
// Returns the count of records migrated. Records already in the new format
// are skipped (idempotent).
func MigrateAutomationRecords(projectRoot string) (int, error) {
	dir := layout.RecordsDir(projectRoot)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return 0, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("reading records directory: %w", err)
	}

	migrated := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".automation.md") {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		didMigrate, err := migrateOneRecord(path)
		if err != nil {
			return migrated, fmt.Errorf("migrating %s: %w", entry.Name(), err)
		}
		if didMigrate {
			migrated++
		}
	}

	return migrated, nil
}

// migrateOneRecord reads a single automation record, checks if it needs
// migration, and writes it back with new field names if so.
func migrateOneRecord(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("opening: %w", err)
	}

	var legacy legacyAutomationRecord
	_, err = frontmatter.Parse(f, &legacy)
	f.Close()
	if err != nil {
		return false, fmt.Errorf("parsing frontmatter: %w", err)
	}

	// Determine if migration is needed: check if any old fields are populated
	// while corresponding new fields are empty.
	needsMigration := false
	if legacy.LastFormalResult != "" && legacy.Result == "" {
		needsMigration = true
	}
	if legacy.LastFormalRun != "" && legacy.ExecutedArtefact == "" {
		needsMigration = true
	}
	if legacy.LastFormalRunAt != "" && legacy.ExecutedAt == "" {
		needsMigration = true
	}
	if legacy.Log != "" && legacy.Notes == "" {
		needsMigration = true
	}
	if legacy.LogSpill != "" && legacy.NotesSpill == "" {
		needsMigration = true
	}

	if !needsMigration {
		return false, nil
	}

	// Build new record from legacy data
	var defect []string
	if legacy.Defect != "" {
		defect = []string{legacy.Defect}
	}

	// Prefer new field values if already populated (partial migration)
	result := legacy.Result
	if result == "" {
		result = legacy.LastFormalResult
	}
	executedArtefact := legacy.ExecutedArtefact
	if executedArtefact == "" {
		executedArtefact = legacy.LastFormalRun
	}
	executedAt := legacy.ExecutedAt
	if executedAt == "" {
		executedAt = legacy.LastFormalRunAt
	}
	notes := legacy.Notes
	if notes == "" {
		notes = legacy.Log
	}
	notesSpill := legacy.NotesSpill
	if notesSpill == "" {
		notesSpill = legacy.LogSpill
	}

	record := &AutomationRecord{
		RecordCommon: RecordCommon{
			TestCase:  legacy.TestCase,
			Framework: legacy.Framework,
			Status:    legacy.Status,
			Result:    result,
			ExecutedAt: executedAt,
			Branch:    legacy.Branch,
			Defect:    defect,
			Notes:     notes,
		},
		Artefact:         legacy.Artefact,
		Adapter:          legacy.Adapter,
		LastDevResult:    legacy.LastDevResult,
		Attempts:         legacy.Attempts,
		Summary:          legacy.Summary,
		NotesSpill:       notesSpill,
		Cycle:            legacy.Cycle,
		ExecutedArtefact: executedArtefact,
		ArtefactHash:     legacy.ArtefactHash,
		TestCaseHash:     legacy.TestCaseHash,
		ResultsFile:      legacy.ResultsFile,
	}

	return true, WriteAutomationRecord(path, record)
}
