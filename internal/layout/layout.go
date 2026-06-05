// Package layout centralises the GTMS-managed directory names so they are
// defined in one place. Production code reads layout paths through Current()
// or helper functions instead of repeating literal strings like "gtms/cases"
// or "gtms/automation".
//
// ENH-093 introduced the package. ENH-098 flipped the defaults to the nested
// layout (e.g. "gtms/cases") and added InitFromParent() for renamed parents.
//
// Current().* values store paths with forward-slash separators. Callers must
// use filepath.Join at the boundary to produce OS-native paths.
package layout

import (
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Paths holds the names of the GTMS-managed directories.
// Each field is a project-relative path segment (e.g. "test-cases").
type Paths struct {
	// Cases is the directory containing test case spec files.
	Cases string

	// Automation is the directory containing automation records and specs.
	Automation string

	// Tasks is the directory containing task files organised by status.
	Tasks string

	// Execution is the directory containing per-run execution artefacts.
	Execution string

	// Scripts is the default adapter output directory for generated test scripts, e.g. "gtms/scripts".
	Scripts string

	// Manual is the directory containing manual testing records and templates (ENH-132).
	Manual string

	// Schemas is the directory containing JSON Schema files (ENH-132).
	Schemas string
}

// defaultPaths holds the current directory names. All production code must read
// these values through Current() or package helper functions rather than
// embedding literal path strings.
//
// ENH-098: defaults are the nested layout under "gtms/". If the user renames
// the parent directory (e.g. git mv gtms/ testing/), InitFromParent() is
// called at startup to update these values to reflect the actual parent name.
var (
	defaultMu    sync.RWMutex
	defaultPaths = pathsForParent("gtms")
)

// Current returns a snapshot of the current directory names.
func Current() Paths {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultPaths
}

// InitFromParent updates the current layout to use the given parent directory name.
// Called at startup after sentinel discovery determines the actual parent
// name (which may differ from the default "gtms" if the user renamed it).
//
// parentDirName must be a single path segment — non-empty, no forward or
// back slashes, and not "." or "..". Violating this contract panics.
func InitFromParent(parentDirName string) {
	if parentDirName == "" || parentDirName == "." || parentDirName == ".." ||
		strings.ContainsAny(parentDirName, "/\\") {
		panic("layout.InitFromParent: parentDirName must be a single path segment" +
			" (no separators, not empty, not \".\" or \"..\"): got " + strconv.Quote(parentDirName))
	}
	next := pathsForParent(parentDirName)

	defaultMu.Lock()
	defaultPaths = next
	defaultMu.Unlock()
}

func pathsForParent(parentDirName string) Paths {
	return Paths{
		Cases:      parentDirName + "/cases",
		Automation: parentDirName + "/automation",
		Tasks:      parentDirName + "/tasks",
		Execution:  parentDirName + "/execution",
		Scripts:    parentDirName + "/scripts",
		Manual:     parentDirName + "/manual",
		Schemas:    parentDirName + "/schemas",
	}
}

// ParentDir returns the parent directory name from the current Cases path.
// For "gtms/cases" it returns "gtms"; for "testing/cases" it returns "testing".
func ParentDir() string {
	paths := Current()
	return filepath.Dir(paths.Cases)
}

// CasesDir returns the absolute path to the test-cases directory.
func CasesDir(projectRoot string) string {
	paths := Current()
	return filepath.Join(projectRoot, paths.Cases)
}

// AutomationDir returns the absolute path to the test-automation directory.
func AutomationDir(projectRoot string) string {
	paths := Current()
	return filepath.Join(projectRoot, paths.Automation)
}

// RecordsDir returns the absolute path to test-automation/records/.
//
// CON-023 / ENH-145: this directory is the legacy automation-record store
// and is being retired by the wiring cutover. Production read/write paths
// must use WiringDir() instead; the migration tool (scripts/migrate-wiring/)
// is the only remaining caller post-cutover.
func RecordsDir(projectRoot string) string {
	paths := Current()
	return filepath.Join(projectRoot, paths.Automation, "records")
}

// WiringDir returns the absolute path to gtms/automation/wiring/, the
// tracked store of automation wiring records introduced by CON-023 / ENH-145.
// Files in this directory follow the pattern {tc}--{framework}.wiring.yaml
// and carry the six-field identity schema (see internal/wiring).
func WiringDir(projectRoot string) string {
	paths := Current()
	return filepath.Join(projectRoot, paths.Automation, "wiring")
}

// SpecsDir returns the absolute path to test-automation/specs/.
func SpecsDir(projectRoot string) string {
	paths := Current()
	return filepath.Join(projectRoot, paths.Automation, "specs")
}

// TasksDir returns the absolute path to the tasks directory (default: gtms/tasks).
func TasksDir(projectRoot string) string {
	paths := Current()
	return filepath.Join(projectRoot, paths.Tasks)
}

// ExecutionDir returns the absolute path to the test-execution directory.
func ExecutionDir(projectRoot string) string {
	paths := Current()
	return filepath.Join(projectRoot, paths.Execution)
}

// ScriptsDir returns the absolute path to the scripts directory.
func ScriptsDir(projectRoot string) string {
	paths := Current()
	return filepath.Join(projectRoot, paths.Scripts)
}

// AttachmentsDir returns the absolute path to the execution attachments directory.
// Attachments are TC-keyed: gtms/execution/attachments/tc-{id}/
func AttachmentsDir(projectRoot string) string {
	paths := Current()
	return filepath.Join(projectRoot, paths.Execution, "attachments")
}

// ExecutionLogsDir returns the absolute path to the execution logs directory.
// Logs are task-keyed: gtms/execution/logs/task-{id}.log
func ExecutionLogsDir(projectRoot string) string {
	paths := Current()
	return filepath.Join(projectRoot, paths.Execution, "logs")
}

// ManualDir returns the absolute path to the manual testing directory.
func ManualDir(projectRoot string) string {
	paths := Current()
	return filepath.Join(projectRoot, paths.Manual)
}

// ManualRecordsDir returns the absolute path to the manual records directory.
func ManualRecordsDir(projectRoot string) string {
	paths := Current()
	return filepath.Join(projectRoot, paths.Manual, "records")
}

// ManualTemplatesDir returns the absolute path to the manual templates directory.
func ManualTemplatesDir(projectRoot string) string {
	paths := Current()
	return filepath.Join(projectRoot, paths.Manual, "templates")
}

// SchemasDir returns the absolute path to the JSON schemas directory.
func SchemasDir(projectRoot string) string {
	paths := Current()
	return filepath.Join(projectRoot, paths.Schemas)
}
