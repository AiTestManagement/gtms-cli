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
func LinkRecord(projectRoot string, cfg *config.Config, tcID, framework, artefact string, force, strict bool) ([]string, error) {

	// Strict-mode spec check (kept for CLI back-compatibility; the spec
	// must exist either way to compute testcase-hash, but the strict flag
	// still controls the diagnostic shape that fires first).
	if strict && !testcase.Exists(projectRoot, tcID) {
		paths := layout.Current()
		return nil, fmt.Errorf("test case '%s' not found in %s/", tcID, paths.TestCases)
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

// RefreshRecord recomputes stale hashes in an existing wiring record without
// requiring the user to supply --framework or --artefact. The identity fields
// (testcase, framework, adapter, artefact) are preserved verbatim; only the
// hash fields are updated.
//
// Validation mirrors LinkRecord:
//  1. Path-safety containment check on the stored artefact path.
//  2. Artefact file exists on disk.
//  3. Test case spec exists and is hashable.
//
// ENH-151 pending sentinel: when artefact-hash is PendingArtefactHash,
// refresh updates testcase-hash only and leaves artefact-hash as "pending".
// The record remains first-executeable through the ENH-151 bootstrap path.
//
// Returns (true, nil) when the record was rewritten with updated hashes,
// (false, nil) when both hashes already match current content (no-op),
// or (false, error) on validation failure.
func RefreshRecord(projectRoot string, rec *wiring.WiringRecord) (bool, error) {
	// Path-safety check on the stored artefact path.
	artefactAbs, _, safeErr := pathsafe.ResolveUnderRoot(projectRoot, rec.Artefact)
	if safeErr != nil {
		return false, fmt.Errorf("unsafe artefact path for wiring: %w", safeErr)
	}

	// Artefact must exist on disk. The pending sentinel only controls whether
	// the artefact hash gets recomputed (see below), it does not exempt the
	// record from artefact-existence validation. A pending record whose
	// artefact has been deleted is unrecoverable: refresh must surface that.
	if _, err := os.Stat(artefactAbs); os.IsNotExist(err) {
		return false, fmt.Errorf("artefact file not found: %s", rec.Artefact)
	} else if err != nil {
		return false, fmt.Errorf("checking artefact file: %w", err)
	}

	// Resolve and hash the test case spec.
	specPath, err := pipeline.ResolveTestCaseSpec(projectRoot, rec.TestCase)
	if err != nil {
		return false, fmt.Errorf("spec not found for %s: %w", rec.TestCase, err)
	}
	newTestCaseHash, err := pipeline.HashFile(filepath.Join(projectRoot, filepath.FromSlash(specPath)))
	if err != nil {
		return false, fmt.Errorf("hashing test case spec: %w", err)
	}

	// Compute new artefact-hash unless the sentinel is pending.
	newArtefactHash := rec.ArtefactHash
	if !wiring.IsPendingArtefactHash(rec.ArtefactHash) {
		h, err := pipeline.HashFile(artefactAbs)
		if err != nil {
			return false, fmt.Errorf("hashing artefact: %w", err)
		}
		newArtefactHash = h
	}

	// No-op when both hashes already match.
	if newTestCaseHash == rec.TestCaseHash && newArtefactHash == rec.ArtefactHash {
		return false, nil
	}

	// Write updated record preserving identity fields.
	updated := &wiring.WiringRecord{
		TestCase:     rec.TestCase,
		TestCaseHash: newTestCaseHash,
		Framework:    rec.Framework,
		Adapter:      rec.Adapter,
		Artefact:     rec.Artefact,
		ArtefactHash: newArtefactHash,
	}
	if _, err := wiring.Write(projectRoot, updated); err != nil {
		return false, fmt.Errorf("writing refreshed wiring: %w", err)
	}
	return true, nil
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
		return result, fmt.Errorf("test case '%s' not found in %s/", tcID, paths.TestCases)
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
