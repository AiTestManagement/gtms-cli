package reader

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
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
type DeleteResult struct {
	TestCasesProcessed   int
	TestCaseSpecsRemoved int
	AutomationRecords    int
	TestScripts          int
	TaskFiles            int
	ResultFiles          int
	ResultContracts      int
	FilesDeleted         []string
}

// TotalFiles returns the total number of files deleted (or that would be deleted).
func (r *DeleteResult) TotalFiles() int {
	return r.TestCaseSpecsRemoved + r.AutomationRecords + r.TestScripts + r.TaskFiles + r.ResultFiles + r.ResultContracts
}

// DeleteArtifacts traces and removes all pipeline artifacts for the given test case(s).
//
// If tcID is non-empty, only that single test case is deleted (scope is ignored).
// If tcID is empty, scope determines which test cases are deleted.
// When keepSpec is true, the test case spec file is preserved.
// When dryRun is true, files are counted but not removed.
func DeleteArtifacts(projectRoot string, scope *ScopeInfo, tcID string, keepSpec bool, dryRun bool) (*DeleteResult, error) {
	result := &DeleteResult{}

	if tcID != "" {
		if err := deleteSingleTC(projectRoot, tcID, keepSpec, dryRun, result); err != nil {
			return nil, err
		}
		result.TestCasesProcessed = 1
		return result, nil
	}

	return deleteByScope(projectRoot, scope, keepSpec, dryRun, result)
}

// deleteByScope deletes artifacts for all test cases within the given scope.
func deleteByScope(projectRoot string, scope *ScopeInfo, keepSpec bool, dryRun bool, result *DeleteResult) (*DeleteResult, error) {
	testCases, err := scanTestCases(projectRoot, scope)
	if err != nil {
		return nil, fmt.Errorf("scanning test cases: %w", err)
	}

	for _, tc := range testCases {
		if err := deleteSingleTC(projectRoot, tc.ID, keepSpec, dryRun, result); err != nil {
			return nil, err
		}
		result.TestCasesProcessed++
	}

	return result, nil
}

// deleteSingleTC traces and removes all artifacts for a single test case.
//
// Atomicity (ENH-128 AC #5): the function performs a precheck pass over EVERY
// record-declared artefact path (both `artefact` and `executed_artefact`
// fields across all automation records for this TC) BEFORE any os.Remove call.
// If any declared path resolves outside the project-owned allowlist,
// the function returns a PathSafetyError naming the offending path and no
// files are touched. This guards against partial-deletion-then-fail behaviour
// where a safety check fires too late and a "safe sibling" has already been
// removed.
func deleteSingleTC(projectRoot, tcID string, keepSpec, dryRun bool, result *DeleteResult) error {
	// 1. Discover automation records first -- their artefact fields drive the
	//    record-declared path set we must validate atomically.
	records, err := findAutomationRecordFiles(projectRoot, tcID)
	if err != nil {
		return fmt.Errorf("finding automation records for %s: %w", tcID, err)
	}

	// 2. PRECHECK: resolve every declared artefact path. Any path-safety
	//    violation must abort the operation BEFORE any deletion runs.
	scripts, err := findTestScripts(projectRoot, records)
	if err != nil {
		return err
	}
	resultFiles, err := findResultFilesFromRecords(projectRoot, records)
	if err != nil {
		return err
	}

	// 2b. Cross-category dedupe: scripts win priority over resultFiles.
	// BUG-073: a file declared as both artefact: and executed_artefact:
	// (possibly via different relative forms) resolves to the same canonical
	// absolute path. Without this step, the file would be deleted in 3b and
	// then 3c would attempt to delete it again, failing mid-operation.
	scriptSeen := make(map[string]bool, len(scripts))
	for _, p := range scripts {
		scriptSeen[p] = true
	}
	var dedupedResults []string
	for _, p := range resultFiles {
		if !scriptSeen[p] {
			dedupedResults = append(dedupedResults, p)
		}
	}
	resultFiles = dedupedResults

	// 3. After validation, perform the actual deletions in stable order.

	// 3a. Test case specs (skip if keepSpec)
	if !keepSpec {
		specs, err := findTestCaseSpecs(projectRoot, tcID)
		if err != nil {
			return fmt.Errorf("finding test case specs for %s: %w", tcID, err)
		}
		for _, path := range specs {
			if err := removeFile(path, dryRun); err != nil {
				return err
			}
			result.TestCaseSpecsRemoved++
			result.FilesDeleted = append(result.FilesDeleted, path)
		}
	}

	// 3b. Test scripts -- discovered from automation record artefact fields
	for _, path := range scripts {
		if err := removeFile(path, dryRun); err != nil {
			return err
		}
		result.TestScripts++
		result.FilesDeleted = append(result.FilesDeleted, path)
	}

	// 3c. Result files -- discovered from automation record executed_artefact fields
	for _, path := range resultFiles {
		if err := removeFile(path, dryRun); err != nil {
			return err
		}
		result.ResultFiles++
		result.FilesDeleted = append(result.FilesDeleted, path)
	}

	// 3d. Now delete the automation records themselves
	for _, path := range records {
		if err := removeFile(path, dryRun); err != nil {
			return err
		}
		result.AutomationRecords++
		result.FilesDeleted = append(result.FilesDeleted, path)
	}

	// 3e. Task files (all status dirs, all command types)
	tasks, err := findTaskFiles(projectRoot, tcID)
	if err != nil {
		return fmt.Errorf("finding task files for %s: %w", tcID, err)
	}
	for _, path := range tasks {
		if err := removeFile(path, dryRun); err != nil {
			return err
		}
		result.TaskFiles++
		result.FilesDeleted = append(result.FilesDeleted, path)
	}

	// 3f. Result contracts (.gtms/results/)
	contracts, err := findResultContracts(projectRoot, tcID)
	if err != nil {
		return fmt.Errorf("finding result contracts for %s: %w", tcID, err)
	}
	for _, path := range contracts {
		if err := removeFile(path, dryRun); err != nil {
			return err
		}
		result.ResultContracts++
		result.FilesDeleted = append(result.FilesDeleted, path)
	}

	return nil
}

// findTestCaseSpecs walks gtms/cases/ recursively to find spec files for the given TC ID.
// Matches filenames like tc-{id}-slug.md or tc-{id}.md.
func findTestCaseSpecs(projectRoot, tcID string) ([]string, error) {
	tcDir := layout.CasesDir(projectRoot)
	if _, err := os.Stat(tcDir); os.IsNotExist(err) {
		return nil, nil
	}

	var matches []string
	prefix := tcID + "-"
	exact := tcID + ".md"

	err := filepath.Walk(tcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if info.IsDir() {
			return nil
		}
		name := info.Name()
		if !strings.HasSuffix(name, ".md") {
			return nil
		}
		if strings.HasPrefix(name, prefix) || name == exact {
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
