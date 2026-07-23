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
	"bytes"
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
// ENH-191: return value extended to (adapterName, warnings, error) so the CLI
// caller can emit the wiring-time report "Execute adapter for wiring: <name>".
func LinkRecord(projectRoot string, cfg *config.Config, tcID, framework, artefact string, force, strict bool) (string, []string, error) {

	// Strict-mode spec check (kept for CLI back-compatibility; the spec
	// must exist either way to compute testcase-hash, but the strict flag
	// still controls the diagnostic shape that fires first).
	if strict && !testcase.Exists(projectRoot, tcID) {
		paths := layout.Current()
		return "", nil, fmt.Errorf("test case '%s' not found in %s/", tcID, paths.TestCases)
	}

	// Extract base TC ID for record creation (strip folder prefix).
	baseTCID := tcID
	if idx := strings.LastIndex(tcID, "/"); idx >= 0 {
		baseTCID = tcID[idx+1:]
	}

	// BUG-165 / Option A: the manual framework is wiring-free (CON-023
	// Q#12). Manual TCs use the prime/fill/execute workflow; there is
	// nothing for link to wire. Short-circuit with targeted guidance
	// instead of falling through to the generic canonical-resolution
	// error ("no execute adapter configured for framework 'manual'").
	// Mirrors the BUG-120 automate short-circuit in builtin_action.go.
	// This runs BEFORE the artefact path/existence checks (CLAUDE-001):
	// for a manual TC the artefact is irrelevant, so the wiring-free
	// guidance must win regardless of whether --artefact resolves.
	//
	// Scope: this check covers only framework=="manual". A non-manual
	// framework whose only execute adapters happen to be Mode 3 names
	// would still hit the generic resolver error -- that broader case
	// is out of scope for BUG-165.
	if framework == "manual" {
		return "", nil, fmt.Errorf(
			"The manual framework is wiring-free -- there is nothing to link. "+
				"Run 'gtms prime %s --framework manual' to stamp a result file, "+
				"fill it in, then 'gtms execute %s' to record the outcome.",
			baseTCID, baseTCID)
	}

	// BUG-057: path-safety containment check on the artefact path. Wiring
	// records must only point at files under projectRoot. ResolveUnderRoot
	// canonicalises the input (handling absolute, relative, and traversal
	// shapes uniformly), so the stored path is always project-relative
	// slash-normalised on success.
	artefactPath, storedArtefact, safeErr := pathsafe.ResolveUnderRoot(projectRoot, artefact)
	if safeErr != nil {
		return "", nil, fmt.Errorf("unsafe artefact path for wiring: %w", safeErr)
	}
	if _, err := os.Stat(artefactPath); os.IsNotExist(err) {
		return "", nil, fmt.Errorf("artefact file not found: %s", artefact)
	} else if err != nil {
		return "", nil, fmt.Errorf("checking artefact file: %w", err)
	}

	// Refuse to overwrite without --force.
	if !force {
		if existing, _, err := wiring.Find(projectRoot, baseTCID, framework); err == nil && existing != nil {
			return "", nil, fmt.Errorf("wiring record already exists for %s--%s -- pass --force to overwrite", baseTCID, framework)
		}
	}

	// Resolve canonical execute adapter for the framework. matches is
	// non-nil when the resolver fell back lexically/to-default among
	// multiple framework matches -- surface that as a warning so the user
	// can pin a default if the implicit choice is wrong. The diagnostic
	// wording is shared with WriteAutomateWiring via
	// adapter.CanonicalFallbackWarning (CON-023 / ENH-145 review-fix).
	executeAdapter, matches, err := adapter.ResolveCanonicalExecuteAdapter(cfg, framework)
	if err != nil {
		return "", nil, err
	}
	var warnings []string
	if w := adapter.CanonicalFallbackWarning(executeAdapter, matches, framework); w != "" {
		warnings = append(warnings, w)
	}

	// Compute testcase-hash from the resolved spec file.
	specPath, err := pipeline.ResolveTestCaseSpec(projectRoot, baseTCID)
	if err != nil {
		return "", warnings, fmt.Errorf("resolving test case spec for wiring: %w", err)
	}
	testCaseHash, err := pipeline.HashFile(filepath.Join(projectRoot, filepath.FromSlash(specPath)))
	if err != nil {
		return "", warnings, fmt.Errorf("hashing test case spec for wiring: %w", err)
	}

	// Compute artefact-hash from the artefact file (absolute path already
	// resolved above; pass through HashFile which reads bytes and SHAs).
	artefactHash, err := pipeline.HashFile(artefactPath)
	if err != nil {
		return "", warnings, fmt.Errorf("hashing artefact for wiring: %w", err)
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
		return "", warnings, err
	}
	return executeAdapter, warnings, nil
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

// RepointResult holds the outcome of a single wiring repoint.
type RepointResult struct {
	TestCase   string
	Framework  string
	OldAdapter string
	NewAdapter string
	Status     string // "repointed", "error"
	Warning    string
	Error      error
}

// RepointSummary holds batch-level counts.
type RepointSummary struct {
	Repointed int
	Skipped   int
	Warnings  int
	Errors    int
	Results   []RepointResult
}

// RepointRecord changes only the adapter field of an existing wiring record.
// The caller is responsible for "already current" detection -- this function
// receives a record that definitely needs repointing.
//
// Artefact-path safety (BUG-057): a root-escaping path is a per-record error;
// a contained-but-missing file is a warning (repoint still proceeds); an
// absolute path resolving inside the root is preserved unchanged.
//
// Optimistic lost-update detection: raw file bytes are read before and after
// the decision; any external modification between reads fails the record.
func RepointRecord(projectRoot string, rec *wiring.WiringRecord, newAdapter string, dryRun bool) (RepointResult, error) {
	result := RepointResult{
		TestCase:   rec.TestCase,
		Framework:  rec.Framework,
		OldAdapter: rec.Adapter,
		NewAdapter: newAdapter,
	}

	wiringPath, err := wiring.Path(projectRoot, rec.TestCase, rec.Framework)
	if err != nil {
		result.Status = "error"
		result.Error = err
		return result, nil
	}

	initialContent, err := os.ReadFile(wiringPath)
	if err != nil {
		result.Status = "error"
		result.Error = fmt.Errorf("reading wiring file: %w", err)
		return result, nil
	}

	// CODEX-001: authoritative re-read. The caller selected `rec` at discovery
	// time; if the on-disk record has changed since then, the selection state is
	// stale and writing rec.* would clobber the concurrent change with old
	// hashes/artefact. Compare the authoritative record against the selection
	// state and refuse on any drift. This widens detection from the old
	// check-to-rename window to the full discovery-to-write window.
	onDisk, err := wiring.Read(wiringPath)
	if err != nil {
		result.Status = "error"
		result.Error = fmt.Errorf("re-reading wiring file: %w", err)
		return result, nil
	}
	if !sameWiringSelection(onDisk, rec) {
		result.Status = "error"
		result.Error = fmt.Errorf("concurrent modification detected for %s--%s: on-disk wiring changed since it was selected", rec.TestCase, rec.Framework)
		return result, nil
	}

	// Artefact-path containment check. ResolveUnderRoot returns the resolved
	// absolute path, which correctly handles both relative and absolute-in-root
	// stored paths. A naive filepath.Join(root, absPath) would mangle an
	// absolute stored path and emit a spurious missing-artefact warning.
	absArtefact, _, safeErr := pathsafe.ResolveUnderRoot(projectRoot, onDisk.Artefact)
	if safeErr != nil {
		result.Status = "error"
		result.Error = fmt.Errorf("unsafe artefact path %q: %w", onDisk.Artefact, safeErr)
		return result, nil
	}

	// Warn if the artefact file is missing but the path is safe. A non-NotExist
	// Stat error (permission/I/O) is NOT a safe "missing" signal -- fail the record
	// rather than re-persist a potentially root-escaping path (CODEX-014).
	if _, statErr := os.Stat(absArtefact); statErr != nil {
		if os.IsNotExist(statErr) {
			result.Warning = fmt.Sprintf("artefact file not found: %s (repoint proceeds, execute remains blocked)", onDisk.Artefact)
		} else {
			result.Status = "error"
			result.Error = fmt.Errorf("checking artefact %q: %w", onDisk.Artefact, statErr)
			return result, nil
		}
	}

	if dryRun {
		result.Status = "repointed"
		return result, nil
	}

	// Final narrow window: re-read bytes immediately before write to catch a
	// change landing between the authoritative read and the write.
	currentContent, err := os.ReadFile(wiringPath)
	if err != nil {
		result.Status = "error"
		result.Error = fmt.Errorf("re-reading wiring file: %w", err)
		return result, nil
	}
	if !bytes.Equal(initialContent, currentContent) {
		result.Status = "error"
		result.Error = fmt.Errorf("concurrent modification detected for %s--%s: file changed between read and write", rec.TestCase, rec.Framework)
		return result, nil
	}

	// Build the updated record from the authoritative on-disk state (validated
	// equal to the selection state), changing only the adapter.
	updated := &wiring.WiringRecord{
		TestCase:     onDisk.TestCase,
		TestCaseHash: onDisk.TestCaseHash,
		Framework:    onDisk.Framework,
		Adapter:      newAdapter,
		Artefact:     onDisk.Artefact,
		ArtefactHash: onDisk.ArtefactHash,
	}
	if _, err := wiring.Write(projectRoot, updated); err != nil {
		result.Status = "error"
		result.Error = fmt.Errorf("writing repointed wiring: %w", err)
		return result, nil
	}

	result.Status = "repointed"
	return result, nil
}

// sameWiringSelection reports whether the authoritative on-disk record still
// matches the state the caller selected at discovery time. All six fields must
// be identical; any difference means a concurrent writer changed the record.
func sameWiringSelection(a, b *wiring.WiringRecord) bool {
	return a.TestCase == b.TestCase &&
		a.TestCaseHash == b.TestCaseHash &&
		a.Framework == b.Framework &&
		a.Adapter == b.Adapter &&
		a.Artefact == b.Artefact &&
		a.ArtefactHash == b.ArtefactHash
}

// RepointBatch repoints a set of wiring records. It enforces all-or-nothing
// framework preflight: if any selected record's framework does not match
// newFramework, the entire batch fails before the first write.
func RepointBatch(projectRoot string, records []*wiring.WiringRecord, malformedInScope []wiring.DiscoveryResult, newAdapter, newFramework string, dryRun bool) RepointSummary {
	summary := RepointSummary{}

	// Count malformed in-scope records as errors.
	for _, m := range malformedInScope {
		tc := m.TCFromName
		if tc == "" {
			tc = filepath.Base(m.Path)
		}
		summary.Errors++
		summary.Results = append(summary.Results, RepointResult{
			TestCase: tc,
			Status:   "error",
			Error:    fmt.Errorf("malformed wiring: %v", m.Err),
		})
	}

	// Phase 1: all-or-nothing framework preflight.
	var mismatches []string
	for _, rec := range records {
		if rec.Framework != newFramework {
			mismatches = append(mismatches, fmt.Sprintf("%s--%s (framework %q)", rec.TestCase, rec.Framework, rec.Framework))
		}
	}
	if len(mismatches) > 0 {
		summary.Errors += len(records)
		for _, rec := range records {
			summary.Results = append(summary.Results, RepointResult{
				TestCase:   rec.TestCase,
				Framework:  rec.Framework,
				OldAdapter: rec.Adapter,
				NewAdapter: newAdapter,
				Status:     "error",
				Error:      fmt.Errorf("framework mismatch: adapter requires %q but batch includes %s", newFramework, strings.Join(mismatches, ", ")),
			})
		}
		return summary
	}

	// Phase 2: repoint each record.
	for _, rec := range records {
		res, _ := RepointRecord(projectRoot, rec, newAdapter, dryRun)
		summary.Results = append(summary.Results, res)
		switch res.Status {
		case "repointed":
			summary.Repointed++
		case "error":
			summary.Errors++
		}
		if res.Warning != "" {
			summary.Warnings++
		}
	}

	return summary
}

// AmbiguityCheck aborts a repoint before any write if any targeted TC ID has MORE
// THAN ONE spec file anywhere under gtms/test/cases/ (all paths named), or ZERO
// hits in the project-wide scan (an unscannable or out-of-tree scope). Wiring is
// keyed by the bare TC ID, so duplicates are ambiguous wherever they live --
// including wholly inside a recursive scope, where both specs share one wiring
// record. Keying on the bare TC ID (from the filename) rather than classifying
// spec PATHS as in/out of scope eliminates the folder-argument spelling/case/
// symlink classification problem (supersedes the outside-scope-only rule; REV-109
// rounds 2-3, CODEX-011/CLAUDE-001). Traversal errors abort the preflight
// (CODEX-004). ENH-100/ENH-101 own general TC-ID uniqueness.
func AmbiguityCheck(projectRoot string, targetTCIDs map[string]bool) error {
	casesDir := layout.TestCasesDir(projectRoot)

	hitsByTC := make(map[string][]string)
	if _, statErr := os.Stat(casesDir); statErr == nil {
		// filepath.Walk visits in lexical order, so the collected paths are stable.
		walkErr := filepath.Walk(casesDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			base := filepath.Base(path)
			if !strings.HasPrefix(base, "tc-") || !strings.HasSuffix(base, ".md") {
				return nil
			}
			rest := strings.TrimSuffix(strings.TrimPrefix(base, "tc-"), ".md")
			tcID := "tc-" + strings.SplitN(rest, "-", 2)[0]
			if !targetTCIDs[tcID] {
				return nil
			}
			rel, relErr := filepath.Rel(casesDir, path)
			if relErr != nil {
				rel = path
			}
			hitsByTC[tcID] = append(hitsByTC[tcID], filepath.ToSlash(rel))
			return nil
		})
		if walkErr != nil {
			return fmt.Errorf("ambiguity preflight could not complete the project-wide spec scan: %w", walkErr)
		}
	} else if !os.IsNotExist(statErr) {
		return fmt.Errorf("ambiguity preflight could not stat the cases directory: %w", statErr)
	}

	for tcID := range targetTCIDs {
		paths := hitsByTC[tcID]
		switch {
		case len(paths) == 0:
			return fmt.Errorf("targeted TC ID %s has no spec file under gtms/test/cases/ (unscannable or out-of-tree scope) -- aborting before any write", tcID)
		case len(paths) > 1:
			return fmt.Errorf("ambiguous TC ID %s: found %d spec files (%s); wiring is keyed by the bare TC ID, so duplicates are ambiguous wherever they live",
				tcID, len(paths), strings.Join(paths, ", "))
		}
	}

	return nil
}

// discoverScopeSpecs walks the real cases directory for scopeFolder and returns
// the set of in-scope TC IDs (extracted from spec filenames). The ambiguity
// preflight is count-based over the bare TC IDs, so only the ID set is needed --
// spec paths are no longer classified by scope.
func discoverScopeSpecs(projectRoot, scopeFolder string, recursive bool) (map[string]bool, error) {
	casesDir := layout.TestCasesDir(projectRoot)
	scopeDir := filepath.Join(casesDir, scopeFolder)
	info, statErr := os.Stat(scopeDir)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return nil, fmt.Errorf("folder '%s/' does not exist under the test cases directory", scopeFolder)
		}
		return nil, fmt.Errorf("checking folder '%s/': %w", scopeFolder, statErr)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("'%s' is not a directory", scopeFolder)
	}

	// CODEX-015: the scope must resolve to a location BENEATH the cases directory.
	// An escaping symlink (or any out-of-tree scope) is rejected before discovery,
	// so a colliding in-tree TC ID can never be mutated via an out-of-tree alias.
	if _, _, err := pathsafe.ResolveUnderRoot(casesDir, scopeFolder); err != nil {
		return nil, fmt.Errorf("folder '%s/' resolves outside the test cases directory -- refusing an out-of-tree scope: %w", scopeFolder, err)
	}

	tcIDs := make(map[string]bool)
	collect := func(name string) {
		if !strings.HasPrefix(name, "tc-") || !strings.HasSuffix(name, ".md") {
			return
		}
		rest := strings.TrimSuffix(strings.TrimPrefix(name, "tc-"), ".md")
		tcIDs["tc-"+strings.SplitN(rest, "-", 2)[0]] = true
	}

	if recursive {
		walkErr := filepath.Walk(scopeDir, func(path string, fi os.FileInfo, werr error) error {
			if werr != nil {
				return werr
			}
			if fi.IsDir() {
				return nil
			}
			collect(filepath.Base(path))
			return nil
		})
		if walkErr != nil {
			return nil, fmt.Errorf("walking '%s/': %w", scopeFolder, walkErr)
		}
	} else {
		entries, readErr := os.ReadDir(scopeDir)
		if readErr != nil {
			return nil, fmt.Errorf("reading '%s/': %w", scopeFolder, readErr)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			collect(e.Name())
		}
	}

	if len(tcIDs) == 0 {
		return nil, fmt.Errorf("no test cases found in '%s/'", scopeFolder)
	}
	return tcIDs, nil
}

// RepointOptions carries the inputs for a repoint. The new adapter's existence,
// Mode-3 exclusion, and inherent framework are resolved INSIDE the core entry
// points (resolveNewAdapter), so the exported repoint contract self-enforces
// Option A for every caller, not only the Cobra path (CODEX-016).
type RepointOptions struct {
	Config        *config.Config
	FromAdapter   string
	NewAdapter    string
	FrameworkFlag string
	DryRun        bool
}

// resolveNewAdapter validates opts.NewAdapter against config (existence + Mode-3
// exclusion) and derives its inherent framework, honouring an optional
// --framework assertion. Every core entry point calls this before any mutation.
func (opts RepointOptions) resolveNewAdapter() (framework string, err error) {
	_, fw, err := adapter.ResolveWiringExecuteAdapter(opts.Config, opts.NewAdapter, opts.FrameworkFlag)
	if err != nil {
		return "", fmt.Errorf("cannot repoint to adapter %q: %w", opts.NewAdapter, err)
	}
	return fw, nil
}

// RepointBulk runs the full folder/recursive repoint operation: new-adapter
// resolution, spec discovery (with scope containment), count-based ambiguity
// preflight, wiring discovery, scope+adapter selection with skipped/malformed
// accounting, and the all-or-nothing framework preflight and mutation. All
// repoint safety invariants live here in the core package, not in the CLI
// (CODEX-008/CODEX-016).
func RepointBulk(projectRoot, scopeFolder string, recursive bool, opts RepointOptions) (RepointSummary, error) {
	framework, err := opts.resolveNewAdapter()
	if err != nil {
		return RepointSummary{}, err
	}
	tcIDs, err := discoverScopeSpecs(projectRoot, scopeFolder, recursive)
	if err != nil {
		return RepointSummary{}, err
	}
	if err := AmbiguityCheck(projectRoot, tcIDs); err != nil {
		return RepointSummary{}, err
	}
	return repointSelected(projectRoot, tcIDs, false, opts.FromAdapter, opts.NewAdapter, framework, opts.DryRun), nil
}

// RepointAll runs a project-wide repoint: no scope folder and no ambiguity
// preflight (every wiring record is in scope).
func RepointAll(projectRoot string, opts RepointOptions) (RepointSummary, error) {
	framework, err := opts.resolveNewAdapter()
	if err != nil {
		return RepointSummary{}, err
	}
	return repointSelected(projectRoot, nil, true, opts.FromAdapter, opts.NewAdapter, framework, opts.DryRun), nil
}

// repointSelected is the shared selection+mutation loop for bulk and --all
// scopes. Malformed in-scope records are errors; valid in-scope records are
// repointed when their stored adapter matches fromAdapter and counted as skipped
// otherwise; the framework preflight and per-record writes are delegated to
// RepointBatch.
func repointSelected(projectRoot string, inScopeTCIDs map[string]bool, all bool, fromAdapter, newAdapter, newFramework string, dryRun bool) RepointSummary {
	allResults, err := wiring.DiscoverAll(projectRoot)
	if err != nil {
		return RepointSummary{
			Errors:  1,
			Results: []RepointResult{{Status: "error", Error: fmt.Errorf("discovering wiring: %w", err)}},
		}
	}

	var selected []*wiring.WiringRecord
	var malformed []wiring.DiscoveryResult
	skipped := 0
	for _, dr := range allResults {
		if dr.Err != nil {
			if all || inScopeTCIDs[dr.TCFromName] {
				malformed = append(malformed, dr)
			}
			continue
		}
		if !all && !inScopeTCIDs[dr.Record.TestCase] {
			continue
		}
		if dr.Record.Adapter == fromAdapter {
			selected = append(selected, dr.Record)
		} else {
			skipped++
		}
	}

	summary := RepointBatch(projectRoot, selected, malformed, newAdapter, newFramework, dryRun)
	summary.Skipped += skipped
	return summary
}

// RepointSingle repoints one TC's wiring: it resolves and validates the new
// adapter (existence + Mode-3 exclusion + framework), then enforces the four-case
// --from-adapter precondition -- with --from-adapter the stored adapter must match
// (else error); without it, an already-current target is an idempotent success
// (Status "already-current"). Resolution, precondition, and mutation all live in
// the core package (CODEX-008/CODEX-016).
func RepointSingle(projectRoot, tcID string, opts RepointOptions) (RepointResult, error) {
	framework, err := opts.resolveNewAdapter()
	if err != nil {
		return RepointResult{}, err
	}

	rec, _, err := wiring.Find(projectRoot, tcID, framework)
	if err != nil {
		return RepointResult{}, fmt.Errorf("reading wiring for %s--%s: %w", tcID, framework, err)
	}
	if rec == nil {
		return RepointResult{}, fmt.Errorf("no wiring record found for %q (framework: %s)", tcID, framework)
	}

	if opts.FromAdapter != "" {
		if rec.Adapter != opts.FromAdapter {
			return RepointResult{}, fmt.Errorf("wiring for %s--%s is currently adapter %q, not %q",
				tcID, framework, rec.Adapter, opts.FromAdapter)
		}
	} else if rec.Adapter == opts.NewAdapter {
		return RepointResult{
			TestCase:   tcID,
			Framework:  framework,
			OldAdapter: rec.Adapter,
			NewAdapter: opts.NewAdapter,
			Status:     "already-current",
		}, nil
	}

	res, _ := RepointRecord(projectRoot, rec, opts.NewAdapter, opts.DryRun)
	return res, nil
}
