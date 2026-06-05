package adapter

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
	"gopkg.in/yaml.v3"
)

// ManualResultFile represents the user-authored manual result file parsed Go-side.
// Only the GTMS contract fields are modelled; additional user fields are ignored.
type ManualResultFile struct {
	TestCase     string `yaml:"test_case_id"`
	TestCaseHash string `yaml:"test_case_hash"`
	Framework    string `yaml:"framework"`
	Result       string `yaml:"result"`
}

// validManualResults is the set of allowed result values in the user-authored
// manual result file. Unlike the handoff contract, the user file does NOT
// allow "error" — that asymmetry is by design (CON-020 Decision 5/8).
var validManualResults = map[string]bool{
	"pass": true,
	"fail": true,
	"skip": true,
}

// testcaseHashPattern validates the 16-character lowercase hex format used by
// pipeline.HashFile (truncated SHA-256, 8 bytes = 16 hex chars).
var testcaseHashPattern = regexp.MustCompile(`^[a-f0-9]{16}$`)

// parseManualResultFile reads and parses a user-authored manual result file.
// Returns a clean error message on malformed YAML that includes the file path.
func parseManualResultFile(path string) (*ManualResultFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("could not read manual result file %s: %w", path, err)
	}

	// Strip the inline schema directive line if present (it's a YAML comment,
	// but some parsers might trip on it — though yaml.v3 handles comments fine).
	var mrf ManualResultFile
	if err := yaml.Unmarshal(data, &mrf); err != nil {
		return nil, fmt.Errorf("could not parse %s: %v", path, err)
	}

	return &mrf, nil
}

// validateManualResultFile validates the four contract fields of a manual result file.
// Returns a descriptive error naming the specific field and allowed values.
func validateManualResultFile(mrf *ManualResultFile, expectedTC, expectedFramework string) error {
	if mrf.TestCase == "" {
		return fmt.Errorf("required field missing: test_case_id")
	}
	if mrf.TestCase != expectedTC {
		return fmt.Errorf("test_case_id mismatch: file has %q, expected %q", mrf.TestCase, expectedTC)
	}

	if mrf.TestCaseHash == "" {
		return fmt.Errorf("required field missing: test_case_hash")
	}
	if !testcaseHashPattern.MatchString(mrf.TestCaseHash) {
		return fmt.Errorf("invalid test_case_hash format %q: must be 16 lowercase hex characters", mrf.TestCaseHash)
	}

	if mrf.Framework == "" {
		return fmt.Errorf("required field missing: framework")
	}
	if mrf.Framework != expectedFramework {
		return fmt.Errorf("framework mismatch: file has %q, expected %q", mrf.Framework, expectedFramework)
	}

	if mrf.Result == "" {
		return fmt.Errorf("result field is empty — set result: pass | fail | skip before running gtms execute")
	}
	if !validManualResults[mrf.Result] {
		return fmt.Errorf("invalid result value %q — must be exactly pass, fail, or skip", mrf.Result)
	}

	return nil
}

// populateManualExecuteFields populates the AdapterContext fields needed by
// the manual-execute adapter. Called from buildAdapterContext when the
// resolved framework is "manual" and the command is "execute".
//
// CON-023 / ENH-145 contract: manual-only TCs have no wiring record. The
// canonical manual artefact lives directly at
// gtms/manual/records/<tc>--manual.result.yaml. This function reads that
// file straight from disk — the legacy automation-record lookup is gone.
//
// Steps:
//  1. Build the manual result file path from layout.Current().Manual.
//  2. Verify the file exists; if not, defer an actionable error that
//     names `gtms prime --framework manual` as the corrective command.
//  3. Parse the user-authored result file (yaml.v3, ManualResultFile struct).
//  4. Validate the four contract fields (test_case_id, test_case_hash,
//     framework, result).
//  5. Compute the current testcase hash for drift comparison.
//  6. Populate ctx fields for env var export to the Tier 2 script.
//
// On any error along the way, sets ctx.ManualExecuteError so the invoker
// can persist a failure handoff after buildAdapterContext returns. The
// manual result file is the only on-disk artefact consulted; no record
// under gtms/automation/ (wiring or legacy) is read.
func populateManualExecuteFields(ctx *AdapterContext, projectRoot, target string, flags CommandFlags) {
	framework := "manual"

	// 1. The manual result file IS the artefact for a manual-only TC.
	manualPaths := layout.Current()
	relResultFile := filepath.ToSlash(filepath.Join(manualPaths.Manual, "records",
		fmt.Sprintf("%s--%s.result.yaml", target, framework)))
	absResultFile := filepath.Join(projectRoot, manualPaths.Manual, "records",
		fmt.Sprintf("%s--%s.result.yaml", target, framework))

	// 2. Existence check + actionable error.
	if _, statErr := os.Stat(absResultFile); os.IsNotExist(statErr) {
		ctx.ManualExecuteError = fmt.Errorf("manual result file not found at %s — run gtms prime %s --framework manual to create it", relResultFile, target)
		return
	}
	artefactPath := relResultFile

	// 3. Parse the result file Go-side
	mrf, parseErr := parseManualResultFile(absResultFile)
	if parseErr != nil {
		ctx.ManualExecuteError = parseErr
		return
	}

	// 4. Validate the contract fields
	if valErr := validateManualResultFile(mrf, target, framework); valErr != nil {
		ctx.ManualExecuteError = valErr
		return
	}

	// 5. Compute current testcase hash for drift comparison
	tcSource := findTestCaseSource(projectRoot, target)
	currentHash := ""
	if tcSource != "" {
		absTC := filepath.Join(projectRoot, tcSource)
		if h, hashErr := pipeline.HashFile(absTC); hashErr == nil {
			currentHash = h
		}
	}

	// 6. Populate context fields (env-var carriers for the Tier 2 script).
	ctx.ResultTemplate = absResultFile
	ctx.TestCaseHash = currentHash
	ctx.ResultValue = mrf.Result
	ctx.ResultTestCase = mrf.TestCase
	ctx.ResultTestCaseHash = mrf.TestCaseHash
	ctx.ResultFramework = mrf.Framework

	// Point output dir at the manual records directory so the CLI headline
	// surfaces the correct artefact path.
	ctx.OutputDir = layout.ManualRecordsDir(projectRoot)
	ctx.OutputSubdir = ""

	// Set the artefact file for the pipeline to use
	if !filepath.IsAbs(artefactPath) {
		ctx.ArtefactFile = artefactPath
	} else {
		if rel, relErr := filepath.Rel(projectRoot, artefactPath); relErr == nil && !strings.HasPrefix(rel, "..") {
			ctx.ArtefactFile = filepath.ToSlash(rel)
		} else {
			ctx.ArtefactFile = artefactPath
		}
	}
}
