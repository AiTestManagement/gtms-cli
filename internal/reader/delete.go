package reader

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
)

// PathSafetyError is a type alias for pathsafe.PathSafetyError, preserving
// backward compatibility for callers that reference reader.PathSafetyError
// (e.g. internal/cli/delete.go). BUG-057: the canonical implementation now
// lives in internal/pathsafe/.
type PathSafetyError = pathsafe.PathSafetyError

// IsPathSafetyError delegates to pathsafe.IsPathSafetyError. Preserves the
// reader-package API surface for existing callers and tests.
func IsPathSafetyError(err error) bool {
	return pathsafe.IsPathSafetyError(err)
}

// DeleteResult describes what a delete operation did (or would do in dry-run mode).
//
// Categories are generic and framework-agnostic:
//   - TestScripts: test scripts discovered via automation record artefact fields
//   - ResultFiles: result/output files discovered via automation record executed_artefact fields
//
// ENH-128: replaced typed BATSScripts/JUnitResults fields with generic TestScripts/ResultFiles.
//
// BUG-173: PreservedArtefacts records referenced test artefacts that delete did
// NOT remove. These are never counted among the deleted totals.
type DeleteResult struct {
	TestCasesProcessed   int
	TestCaseSpecsRemoved int
	AutomationRecords    int
	TestScripts          int
	TaskFiles            int
	ResultFiles          int
	ResultContracts      int
	FilesDeleted         []string
	PreservedArtefacts   []PreservedArtefact
}

// PreservedArtefact names a referenced test artefact that delete deliberately
// left on disk (BUG-173). The safe default preserves every referenced artefact;
// a forced deletion (`--force-artefact-deletion`) still preserves an artefact
// that a test case OUTSIDE the deletion set references.
//
// SurvivingTCs is empty for the safe default (plain delete, no force). When a
// forced deletion is blocked by an external reference, SurvivingTCs holds the
// sorted set of referencing test case IDs outside the deletion set -- this
// drives the sharing warning.
type PreservedArtefact struct {
	Path         string   // project-relative, forward-slash
	SurvivingTCs []string // sorted; empty => safe-default preserve
}

// TotalFiles returns the total number of files deleted (or that would be deleted).
func (r *DeleteResult) TotalFiles() int {
	return r.TestCaseSpecsRemoved + r.AutomationRecords + r.TestScripts + r.TaskFiles + r.ResultFiles + r.ResultContracts
}

// DeleteArtifacts traces and removes pipeline artifacts for the given test case(s).
//
// If tcID is non-empty, only that single test case is deleted (scope is ignored).
// If tcID is empty, scope determines which test cases are deleted.
// When keepSpec is true, the test case spec file is preserved.
// When force is true (`--force-artefact-deletion`), referenced test artefacts
// become deletion candidates -- but an artefact referenced by any test case
// OUTSIDE the selected deletion set is still preserved (BUG-173).
// When dryRun is true, the plan is computed but no files are removed.
//
// BUG-173: the complete operation is planned (buildDeletePlan) BEFORE any
// filesystem mutation, so bulk output is independent of TC iteration order and
// dry-run previews the same plan as the real run. The ENH-128 atomic
// path-safety precheck runs during planning: an unsafe selected artefact path
// aborts the whole operation before any removal.
func DeleteArtifacts(projectRoot string, scope *ScopeInfo, tcID string, keepSpec, force, dryRun bool) (*DeleteResult, error) {
	plan, err := buildDeletePlan(projectRoot, scope, tcID, keepSpec, force)
	if err != nil {
		return nil, err
	}
	return executeDeletePlan(plan, dryRun)
}

// executeDeletePlan removes every file the plan resolved (a no-op per file in
// dry-run) and materialises the DeleteResult, including the preserved-artefact
// list. Deletion order matches the historical grouping: specs, artefacts,
// result files, records, task files, result contracts.
func executeDeletePlan(plan *deletePlan, dryRun bool) (*DeleteResult, error) {
	result := &DeleteResult{
		TestCasesProcessed: plan.testCasesProcessed,
		PreservedArtefacts: plan.preserved,
	}

	remove := func(paths []string, counter *int) error {
		for _, path := range paths {
			if err := removeFile(path, dryRun); err != nil {
				return err
			}
			*counter++
			result.FilesDeleted = append(result.FilesDeleted, path)
		}
		return nil
	}

	if err := remove(plan.specs, &result.TestCaseSpecsRemoved); err != nil {
		return nil, err
	}
	if err := remove(plan.artefactDeletes, &result.TestScripts); err != nil {
		return nil, err
	}
	if err := remove(plan.resultFiles, &result.ResultFiles); err != nil {
		return nil, err
	}
	if err := remove(plan.records, &result.AutomationRecords); err != nil {
		return nil, err
	}
	if err := remove(plan.taskFiles, &result.TaskFiles); err != nil {
		return nil, err
	}
	if err := remove(plan.resultContracts, &result.ResultContracts); err != nil {
		return nil, err
	}

	return result, nil
}

// findTestCaseSpecs walks gtms/test/cases/ recursively to find spec files for the given TC ID.
// Matches filenames like tc-{id}-slug.md or tc-{id}.md.
func findTestCaseSpecs(projectRoot, tcID string) ([]string, error) {
	tcDir := layout.TestCasesDir(projectRoot)
	if _, err := os.Stat(tcDir); os.IsNotExist(err) {
		return nil, nil
	}

	var matches []string

	err := filepath.Walk(tcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if info.IsDir() {
			return nil
		}
		// REV-122: the ONE shared spec predicate -- identical to the uniqueness guard
		// (testcase.FindAllSpecs) and the existence check (testcase.ResolveSpec), so
		// the guard counts exactly what this mutation removes.
		if testcase.MatchesSpecFile(info.Name(), tcID) {
			matches = append(matches, path)
		}
		return nil
	})

	return matches, err
}

// findAutomationRecordFiles finds all wiring records for the given TC ID.
// CON-023 / ENH-145: wiring records use framework-qualified naming
// `tc-{id}--{framework}.wiring.yaml` under gtms/automation/wiring/. The
// caller name is preserved for API stability; the underlying source is
// now wiring, not the retired .automation.md files.
func findAutomationRecordFiles(projectRoot, tcID string) ([]string, error) {
	pattern := filepath.Join(layout.WiringDir(projectRoot),
		fmt.Sprintf("%s--*.wiring.yaml", tcID))
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("globbing wiring records for %s: %w", tcID, err)
	}
	// Also include the manual record (if any).
	manualPath := filepath.Join(projectRoot, layout.Current().Manual, "records",
		fmt.Sprintf("%s--manual.result.yaml", tcID))
	if _, err := os.Stat(manualPath); err == nil {
		matches = append(matches, manualPath)
	}
	return matches, nil
}

// recordArtefactFields is a minimal struct for extracting artefact paths from automation records.
type recordArtefactFields struct {
	Artefact         string `yaml:"artefact"`
	ExecutedArtefact string `yaml:"executed_artefact"`
}

// findTestScripts reads the artefact field from each automation record and returns
// the resolved absolute paths of test scripts that exist on disk.
// Paths are deduplicated and validated against the project root (path safety).
func findTestScripts(projectRoot string, recordPaths []string) ([]string, error) {
	var rawPaths []string
	for _, recPath := range recordPaths {
		fields, err := parseRecordArtefactFields(recPath)
		if err != nil || fields.Artefact == "" {
			continue
		}
		rawPaths = append(rawPaths, fields.Artefact)
	}
	return resolveAndDedup(projectRoot, rawPaths)
}

// findResultFilesFromRecords reads the executed_artefact field from each automation record
// and returns the resolved absolute paths of result files that exist on disk.
// Paths are deduplicated and validated against the project root (path safety).
func findResultFilesFromRecords(projectRoot string, recordPaths []string) ([]string, error) {
	var rawPaths []string
	for _, recPath := range recordPaths {
		fields, err := parseRecordArtefactFields(recPath)
		if err != nil || fields.ExecutedArtefact == "" {
			continue
		}
		rawPaths = append(rawPaths, fields.ExecutedArtefact)
	}
	return resolveAndDedup(projectRoot, rawPaths)
}

// parseRecordArtefactFields reads artefact-related YAML fields from a
// wiring record file (CON-023 / ENH-145). The struct's
// ExecutedArtefact field is left blank because wiring carries identity
// only — the per-run artefact (if any) lives on the result contract.
// The function name is preserved for API stability; the underlying
// source is the new pure-YAML wiring shape.
func parseRecordArtefactFields(path string) (*recordArtefactFields, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Strip legacy markdown frontmatter delimiters if present (a manual
	// record may have leading "---" depending on how it was authored).
	content := string(data)
	if strings.HasPrefix(content, "---\n") {
		end := strings.Index(content[4:], "\n---")
		if end >= 0 {
			content = content[4 : 4+end]
		}
	}
	var fields recordArtefactFields
	if err := yaml.Unmarshal([]byte(content), &fields); err != nil {
		return nil, err
	}
	return &fields, nil
}

// resolveAndDedup takes a list of relative paths, resolves each against projectRoot,
// validates path safety, deduplicates, and returns only paths that exist on disk.
//
// ENH-128 AC #5 (atomicity): if ANY path fails the safety check, this function
// returns a PathSafetyError naming the offending path and the caller MUST
// abort the entire delete operation before any os.Remove runs. We do not
// silently skip unsafe paths -- a security refusal that exits 0 silently
// passes through CI pipelines and `&&` chains as success.
func resolveAndDedup(projectRoot string, relativePaths []string) ([]string, error) {
	seen := make(map[string]bool)
	var result []string

	for _, relPath := range relativePaths {
		absPath, _, err := pathsafe.ResolveUnderRoot(projectRoot, relPath)
		if err != nil {
			// Refuse the whole operation; do NOT silently filter.
			// ResolveUnderRoot already returns *pathsafe.PathSafetyError.
			return nil, err
		}
		if seen[absPath] {
			continue
		}
		// Only include paths that exist on disk
		if _, statErr := os.Stat(absPath); statErr != nil {
			continue
		}
		seen[absPath] = true
		result = append(result, absPath)
	}
	return result, nil
}

// findTaskFiles searches all 5 status directories for task files targeting the given TC ID.
// Task files are named: task-{task-id}-{command}-{tc-id}.md
// We match any file ending with "-{tcID}.md" to catch all command types.
func findTaskFiles(projectRoot, tcID string) ([]string, error) {
	var matches []string
	suffix := "-" + tcID + ".md"

	for _, statusDir := range []string{"pending", "in-progress", "in-review", "complete", "error"} {
		dir := filepath.Join(layout.TasksDir(projectRoot), statusDir)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}

		entries, err := os.ReadDir(dir)
		if err != nil {
			return matches, fmt.Errorf("reading %s: %w", dir, err)
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			if strings.HasSuffix(entry.Name(), suffix) {
				matches = append(matches, filepath.Join(dir, entry.Name()))
			}
		}
	}

	return matches, nil
}

// resultContractTarget is a minimal struct for parsing the target field from result contract YAML.
// We avoid importing internal/result to prevent circular dependencies.
type resultContractTarget struct {
	Target string `yaml:"target"`
}

// findResultContracts scans .gtms/results/ for result contract YAML files whose target field
// contains the given TC ID. Result contracts are named by task ID, not TC ID, so we must
// parse each file's YAML to check the target field via substring match.
func findResultContracts(projectRoot, tcID string) ([]string, error) {
	resultsDir := filepath.Join(projectRoot, ".gtms", "results")
	if _, err := os.Stat(resultsDir); os.IsNotExist(err) {
		return nil, nil
	}

	pattern := filepath.Join(resultsDir, "*.handoff.yaml")
	candidates, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("globbing result contracts: %w", err)
	}

	var matches []string
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			continue // skip unreadable files
		}

		var rc resultContractTarget
		if err := yaml.Unmarshal(data, &rc); err != nil {
			continue // skip malformed YAML
		}

		if strings.Contains(rc.Target, tcID) {
			matches = append(matches, path)
		}
	}

	return matches, nil
}

// removeFile removes a file from disk. In dry-run mode, it's a no-op.
func removeFile(path string, dryRun bool) error {
	if dryRun {
		return nil
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("removing %s: %w", path, err)
	}
	return nil
}
