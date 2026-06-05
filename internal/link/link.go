// Package link implements the core business logic for the gtms link command.
//
// CON-023 / ENH-145 retarget: link now writes the six-field wiring record at
// gtms/automation/wiring/*.wiring.yaml instead of the legacy automation
// record. The wiring `adapter:` field is the canonical EXECUTE adapter for
// the framework (e.g. `bats-runner`), NOT the historical `manual-link`
// provenance marker — wiring carries identity, not provenance (CON-023 Q#2).
//
// ENH-111: link is a manual operation -- the user asserts the convention is
// satisfied. GTMS checks artefact existence (filesystem only) and writes the
// wiring record. No framework CLI is invoked.
package link

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/adapter"
	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// CheckResult holds the outcome of a --check validation.
type CheckResult struct {
	// TestCase is the TC ID that was checked.
	TestCase string

	// Framework is the framework name.
	Framework string

	// Artefact is the artefact path that was validated.
	Artefact string

	// ArtefactExists is true if the artefact file was found on disk.
	ArtefactExists bool

	// RecordExists is true if a wiring record already exists for this TC ×
	// framework. (Field name preserved for CLI compatibility — the meaning
	// now refers to the wiring record, not the legacy automation record.)
	RecordExists bool
}

// LinkRecord creates a wiring record for a pre-existing test artefact.
//
// Validation order:
//  1. (--strict, kept for CLI compatibility) Verify the test case spec
//     exists. CON-023 makes this effectively mandatory because we cannot
//     compute testcase-hash without the spec; we surface the same error
//     here whether the user passed --strict or not, but the flag name
//     remains so existing scripts that opt-in don't break.
//  2. Verify the artefact file exists on disk.
//  3. Resolve the canonical execute adapter for `framework`. If no execute
//     adapter is configured for the framework, fail with the same shape
//     used by `gtms automate`.
//  4. Compute testcase-hash and artefact-hash.
//  5. Write the wiring record. With --force, overwrite any existing wiring;
//     without --force and a record exists, fail with a clear error.
//
// Returns warnings the CLI layer should surface to the user. The canonical
// adapter resolver may pick a deterministic lexical fallback when multiple
// execute adapters match the framework; in that case the diagnostic names
// the chosen adapter and the competing matches so the user can pin a
// default in gtms.config if the implicit choice is wrong.
//
// Unused parameters preserved for CLI compatibility (branch, environment,
// executedBy): wiring records carry only the six identity fields, so these
// values are dropped — the runtime fields belong on the result contract.
func LinkRecord(projectRoot string, cfg *config.Config, tcID, framework, artefact, branch, environment, executedBy string, force, strict bool) ([]string, error) {
	_ = branch
	_ = environment
	_ = executedBy

	// Strict-mode spec check (kept for CLI back-compatibility; the spec
	// must exist either way to compute testcase-hash, but the strict flag
	// still controls the diagnostic shape that fires first).
	if strict && !testcase.Exists(projectRoot, tcID) {
		paths := layout.Current()
		return nil, fmt.Errorf("test case '%s' not found in %s/", tcID, paths.Cases)
	}

	// Extract base TC ID for record creation (strip folder prefix).
	baseTCID := tcID
	if idx := strings.LastIndex(tcID, "/"); idx >= 0 {
		baseTCID = tcID[idx+1:]
	}

	// BUG-057: path-safety containment check on the artefact path. Wiring
	// records must only point at files under projectRoot. ResolveUnderRoot
	// canonicalises the input (handling absolute, relative, and traversal
	// shapes uniformly), so the stored path is always project-relative
	// slash-normalised on success.
	artefactPath, storedArtefact, safeErr := pathsafe.ResolveUnderRoot(projectRoot, artefact)
	if safeErr != nil {
		return nil, fmt.Errorf("unsafe artefact path for wiring: %w", safeErr)
	}
	if _, err := os.Stat(artefactPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("artefact file not found: %s", artefact)
	} else if err != nil {
		return nil, fmt.Errorf("checking artefact file: %w", err)
	}

	// Refuse to overwrite without --force.
	if !force {
		if existing, _, err := wiring.Find(projectRoot, baseTCID, framework); err == nil && existing != nil {
			return nil, fmt.Errorf("wiring record already exists for %s--%s — pass --force to overwrite", baseTCID, framework)
		}
	}

	// Resolve canonical execute adapter for the framework. matches is
	// non-nil when the resolver fell back lexically/to-default among
	// multiple framework matches — surface that as a warning so the user
	// can pin a default if the implicit choice is wrong. The diagnostic
	// wording is shared with WriteAutomateWiring via
	// adapter.CanonicalFallbackWarning (CON-023 / ENH-145 review-fix).
	executeAdapter, matches, err := adapter.ResolveCanonicalExecuteAdapter(cfg, framework)
	if err != nil {
		return nil, err
	}
	var warnings []string
	if w := adapter.CanonicalFallbackWarning(executeAdapter, matches, framework); w != "" {
		warnings = append(warnings, w)
	}

	// Compute testcase-hash from the resolved spec file.
	specPath, err := pipeline.ResolveTestCaseSpec(projectRoot, baseTCID)
	if err != nil {
		return warnings, fmt.Errorf("resolving test case spec for wiring: %w", err)
	}
	testCaseHash, err := pipeline.HashFile(filepath.Join(projectRoot, filepath.FromSlash(specPath)))
	if err != nil {
		return warnings, fmt.Errorf("hashing test case spec for wiring: %w", err)
	}

	// Compute artefact-hash from the artefact file (absolute path already
	// resolved above; pass through HashFile which reads bytes and SHAs).
	artefactHash, err := pipeline.HashFile(artefactPath)
	if err != nil {
		return warnings, fmt.Errorf("hashing artefact for wiring: %w", err)
	}

	rec := &wiring.WiringRecord{
		TestCase:     baseTCID,
		TestCaseHash: testCaseHash,
		Framework:    framework,
		Adapter:      executeAdapter,
		Artefact:     storedArtefact,
		ArtefactHash: artefactHash,
	}
	if _, err := wiring.Write(projectRoot, rec); err != nil {
		return warnings, err
	}
	return warnings, nil
}

// CheckLink validates inputs and reports link health without writing anything.
//
// When artefact is provided: checks that the artefact file exists.
// When artefact is empty: looks up the existing wiring record and re-checks
// the stored artefact path (quality-of-life for re-checking existing links).
//
// Returns an error if validation fails (artefact missing, no wiring record
// when re-checking, etc.).
func CheckLink(projectRoot, tcID, framework, artefact string, strict bool) (CheckResult, error) {
	result := CheckResult{
		TestCase:  tcID,
		Framework: framework,
	}

	if strict && !testcase.Exists(projectRoot, tcID) {
		paths := layout.Current()
		return result, fmt.Errorf("test case '%s' not found in %s/", tcID, paths.Cases)
	}

	// Extract base TC ID for record lookup (strip folder prefix).
	baseTCID := tcID
	if idx := strings.LastIndex(tcID, "/"); idx >= 0 {
		baseTCID = tcID[idx+1:]
	}

	// Look up existing wiring record regardless of artefact flag.
	existing, _, _ := wiring.Find(projectRoot, baseTCID, framework)
	result.RecordExists = (existing != nil)

	// Determine which artefact path to validate.
	checkPath := artefact
	if checkPath == "" {
		if existing == nil {
			return result, fmt.Errorf("no existing wiring for %s--%s and no --artefact provided", tcID, framework)
		}
		checkPath = existing.Artefact
	}
	result.Artefact = checkPath

	// BUG-057: path-safety check on the supplied or stored artefact path.
	// Tampered wiring or a malicious --artefact value must fail here, not
	// silently report ArtefactExists=false. ResolveUnderRoot returns the
	// canonical absolute path used for os.Stat.
	absPath, _, safeErr := pathsafe.ResolveUnderRoot(projectRoot, checkPath)
	if safeErr != nil {
		result.ArtefactExists = false
		return result, fmt.Errorf("unsafe artefact path for wiring: %w", safeErr)
	}
	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		result.ArtefactExists = false
		return result, fmt.Errorf("artefact file not found: %s", checkPath)
	} else if err != nil {
		result.ArtefactExists = false
		return result, fmt.Errorf("checking artefact file: %w", err)
	}

	result.ArtefactExists = true
	return result, nil
}
