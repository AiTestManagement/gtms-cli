// Package link implements the core business logic for the gtms link command.
//
// CON-023 / ENH-145 retarget: link now writes the six-field wiring record at
// gtms/automation/wiring/*.wiring.yaml instead of the legacy automation
// record. The wiring `adapter:` field is the canonical EXECUTE adapter for
// the framework (e.g. `bats-runner`), NOT the historical `manual-link`
// provenance marker -- wiring carries identity, not provenance (CON-023 Q#2).
//
// ENH-111: link is a manual operation -- the user asserts the convention is
// satisfied. GTMS checks artefact existence (filesystem only) and writes the
// wiring record. No framework CLI is invoked.
package link

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

	// RecordExists is true if a wiring record already exists for this TC x
	// framework. (Field name preserved for CLI compatibility -- the meaning
	// now refers to the wiring record, not the legacy automation record.)
	RecordExists bool
}

// WiringIdentityMismatchError signals that a wiring file at the requested path
// declares a DIFFERENT test-case identity in its body than its filename implies
// (ADR-024 / tc-d1c39672). It is a data-integrity error, not absence, and
// crucially is NOT resolvable with --force -- so the CLI must not suggest --force
// for it. Callers detect it with errors.As.
type WiringIdentityMismatchError struct {
	Path        string
	DeclaredTC  string
	DeclaredFW  string
	RequestedTC string
	RequestedFW string
}

// Error renders the diagnostic with control characters stripped from every
// repository-controlled field.
//
// REV-159 CODEX-005. Path, DeclaredTC and DeclaredFW all come from a wiring file
// on disk, and wiring.Read validates its identity fields only for NON-EMPTINESS
// -- an escaped YAML scalar such as "tc-a\u001b[2J" parses, validates, and used
// to be interpolated raw with %s into a diagnostic the CLI prints straight to the
// terminal. A hand-edited or hostile wiring record could therefore inject ANSI or
// OSC sequences into `gtms link` output: clear the screen, rewrite preceding
// lines, or disguise the refusal as a success.
//
// ADR-024 s7 puts the fix at the CENTRAL rendering boundary, so this renders
// through the same testcase.SanitizeIdentityString every other typed identity
// diagnostic uses rather than defining a second rule here.
//
// Deliberately NOT done here: rejecting control characters in wiring identity
// fields during wiring.Read. That is defence in depth on a different boundary,
// it changes how existing on-disk records parse project-wide, and it is not
// needed to close this finding. Recorded as follow-up hardening.
func (e *WiringIdentityMismatchError) Error() string {
	return fmt.Sprintf("wiring record at %s declares identity %s--%s, not %s--%s; refusing to overwrite a foreign-identity record -- fix or remove it",
		testcase.SanitizeIdentityString(e.Path),
		testcase.SanitizeIdentityString(e.DeclaredTC), testcase.SanitizeIdentityString(e.DeclaredFW),
		testcase.SanitizeIdentityString(e.RequestedTC), testcase.SanitizeIdentityString(e.RequestedFW))
}

// resolveLinkOwner resolves a link target -- bare or folder-qualified -- to its
// proven owner, preserving each outcome AS ITSELF.
//
// REV-158 CLAUDE-006: link hashed its spec through pipeline.ResolveTestCaseSpec
// -> testcase.FindSource, which maps EVERY error, including a typed
// *OwnershipError, to "". The strict-mode gate did the same through
// testcase.Exists. So a tc-77777777 owned by two specs in different folders
// reported "test case spec for tc-77777777 not found under .../gtms/test/cases"
// with the hint "Check the artefact path or use --force to overwrite." The
// operation did refuse, so nothing was written, but an identity failure was
// rendered as absence -- the exact conversion ADR-024 s7 forbids -- and the hint
// pointed at a flag that cannot help. Every other mutating consumer in this area
// emits the typed diagnostic naming both owners.
//
// NotFound keeps link's established wording, which frozen acceptance specs
// assert. Ambiguous and Unprovable come back as the typed *OwnershipError for the
// CLI to render once.
func resolveLinkOwner(projectRoot, tcID string) (*testcase.Owner, error) {
	owner, err := testcase.ResolveOwner(projectRoot, tcID)
	if err == nil {
		return owner, nil
	}
	var oe *testcase.OwnershipError
	if errors.As(err, &oe) {
		return nil, oe
	}
	if errors.Is(err, testcase.ErrOwnerNotFound) {
		return nil, fmt.Errorf("test case '%s' not found in %s/", tcID, layout.Current().TestCases)
	}
	return nil, err
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

	// ADR-024 s5: resolve ownership ONCE, up front, and carry the owner to the
	// hash below instead of re-deriving the spec from the bare id there.
	owner, ownerErr := resolveLinkOwner(projectRoot, tcID)

	// Strict-mode spec check (kept for CLI back-compatibility; the spec
	// must exist either way to compute testcase-hash, but the strict flag
	// still controls the diagnostic shape that fires first).
	if strict && ownerErr != nil {
		return "", nil, ownerErr
	}
	// An ambiguous or unprovable identity refuses regardless of --strict: link is
	// a wiring WRITE, and ADR-024's matrix requires it to refuse and name the
	// owners rather than proceed or report absence (REV-158 CLAUDE-006).
	var ownershipErr *testcase.OwnershipError
	if errors.As(ownerErr, &ownershipErr) {
		return "", nil, ownershipErr
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

	// ADR-024 / tc-d1c39672 / REV-156 CLAUDE-002: a wiring file at the target path
	// whose parsed body identity disagrees with the requested (tc, framework) is a
	// data-integrity error, NOT absence. Refuse to write over it regardless of
	// --force: --force authorises overwriting a VALID record deliberately, it never
	// converts a foreign-identity record into mutation authority. This runs before
	// the not-force existence guard so the mismatch is reported on both paths.
	if existing, existingPath, ferr := wiring.Find(projectRoot, baseTCID, framework); ferr == nil && existing != nil &&
		(existing.TestCase != baseTCID || existing.Framework != framework) {
		return "", nil, &WiringIdentityMismatchError{
			Path:        existingPath,
			DeclaredTC:  existing.TestCase,
			DeclaredFW:  existing.Framework,
			RequestedTC: baseTCID,
			RequestedFW: framework,
		}
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

	// Compute testcase-hash from the spec resolved above as the owner (ADR-024 s5).
	if owner == nil {
		return "", warnings, fmt.Errorf("resolving test case spec for wiring: test case spec for %s not found under %s",
			baseTCID, layout.TestCasesDir(projectRoot))
	}
	testCaseHash, err := pipeline.HashFile(owner.SpecAbs())
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

// RefreshPlan is the computed, non-mutating result of comparing a wiring record
// against current spec/artefact content. It is produced by PlanRefresh and
// consumed by ApplyRefresh; the dry-run preview renders it directly while the
// real refresh applies it. Extracting the plan means the drift/comparison logic
// is computed in ONE place and never duplicated between preview and mutation
// (BUG-146).
//
// TestCaseHashChanged / ArtefactHashChanged describe exactly which hash fields
// the real refresh would rewrite. ArtefactHashChanged is always false when the
// stored artefact-hash is the pending sentinel (the real refresh preserves it),
// so the preview never claims a pending record's artefact-hash will change.
type RefreshPlan struct {
	TestCase            string
	Framework           string
	TestCaseHashChanged bool
	ArtefactHashChanged bool
	// Changed is true when either hash field would be rewritten.
	Changed bool
	// updated is the record ApplyRefresh writes. It is unexported because callers
	// interact with the plan through the changed-field booleans, not the raw
	// record; ApplyRefresh is the only consumer of this field.
	updated *wiring.WiringRecord
}

// PlanRefresh validates a wiring record and computes which hash fields a refresh
// would rewrite, WITHOUT writing anything. It performs the same validation as
// the real refresh (path-safety, artefact existence, spec resolution + hashing),
// so a preview surfaces exactly the errors the real refresh would.
//
// Validation mirrors LinkRecord:
//  1. Path-safety containment check on the stored artefact path.
//  2. Artefact file exists on disk.
//  3. Test case spec exists and is hashable.
//
// ENH-151 pending sentinel: when artefact-hash is PendingArtefactHash, the plan
// marks artefact-hash as unchanged (the real refresh leaves it "pending") and
// only testcase-hash can drift.
// REV-159 CODEX-006: snap is the OPERATION's snapshot, threaded in explicitly.
// PlanRefresh used to call resolveLinkOwner, which builds a project-wide snapshot
// of its own -- so a folder refresh walked and parsed the entire cases tree once
// per wiring record, on top of the snapshot runFolderRefresh had already built,
// and successive records could be judged against DIFFERENT ownership views if the
// tree changed mid-operation. Both violate ADR-024 s2's one-snapshot-per-operation
// rule; the second is a correctness bug, not just a cost.
//
// The snapshot is threaded rather than cached. A process-global cache was
// deliberately deleted on this branch: it makes re-resolution cheap instead of
// unnecessary, and leaves every consumer holding a bare id and its own idea of
// what that id means.
//
// A nil snap is a programming error, not a licence to resolve one here.
func PlanRefresh(projectRoot string, snap *testcase.Snapshot, rec *wiring.WiringRecord) (RefreshPlan, error) {
	plan := RefreshPlan{TestCase: rec.TestCase, Framework: rec.Framework}

	// Path-safety check on the stored artefact path.
	artefactAbs, _, safeErr := pathsafe.ResolveUnderRoot(projectRoot, rec.Artefact)
	if safeErr != nil {
		return RefreshPlan{}, fmt.Errorf("unsafe artefact path for wiring: %w", safeErr)
	}

	// Artefact must exist on disk. The pending sentinel only controls whether
	// the artefact hash gets recomputed (see below), it does not exempt the
	// record from artefact-existence validation. A pending record whose
	// artefact has been deleted is unrecoverable: refresh must surface that.
	if _, err := os.Stat(artefactAbs); os.IsNotExist(err) {
		return RefreshPlan{}, fmt.Errorf("artefact file not found: %s", rec.Artefact)
	} else if err != nil {
		return RefreshPlan{}, fmt.Errorf("checking artefact file: %w", err)
	}

	// Look the owner up in the snapshot the operation already built. Refresh is a
	// wiring WRITE, so an identity failure must stay an identity failure here too,
	// never "spec not found" (ADR-024 s7; REV-158 CLAUDE-006) -- which is why this
	// is a lookup against the operation snapshot rather than a bare id passed
	// downstream: every outcome survives intact.
	if snap == nil {
		return RefreshPlan{}, fmt.Errorf("refresh for %s requires the operation snapshot", rec.TestCase)
	}
	owner, ownerErr := snap.RequireOwner(rec.TestCase)
	if ownerErr != nil {
		var oe *testcase.OwnershipError
		if errors.As(ownerErr, &oe) {
			return RefreshPlan{}, oe
		}
		return RefreshPlan{}, fmt.Errorf("spec not found for %s: test case '%s' not found in %s/",
			rec.TestCase, rec.TestCase, layout.Current().TestCases)
	}
	newTestCaseHash, err := pipeline.HashFile(owner.SpecAbs())
	if err != nil {
		return RefreshPlan{}, fmt.Errorf("hashing test case spec: %w", err)
	}

	// Compute new artefact-hash unless the sentinel is pending.
	newArtefactHash := rec.ArtefactHash
	if !wiring.IsPendingArtefactHash(rec.ArtefactHash) {
		h, err := pipeline.HashFile(artefactAbs)
		if err != nil {
			return RefreshPlan{}, fmt.Errorf("hashing artefact: %w", err)
		}
		newArtefactHash = h
	}

	plan.TestCaseHashChanged = newTestCaseHash != rec.TestCaseHash
	plan.ArtefactHashChanged = newArtefactHash != rec.ArtefactHash
	plan.Changed = plan.TestCaseHashChanged || plan.ArtefactHashChanged

	// The record to write preserves identity fields; only hashes are updated.
	plan.updated = &wiring.WiringRecord{
		TestCase:     rec.TestCase,
		TestCaseHash: newTestCaseHash,
		Framework:    rec.Framework,
		Adapter:      rec.Adapter,
		Artefact:     rec.Artefact,
		ArtefactHash: newArtefactHash,
	}

	// REV-129 CODEX-129-001: a preview must not approve a record the real writer
	// will refuse. wiring.Write validates the testcase and framework as filename
	// components, and that check used to run only at write time -- so a stale
	// record whose framework is not a safe component (say a file named
	// tc-a--bad..fw.wiring.yaml declaring the same identity) previewed as "Would
	// refresh" and then failed for real, breaking the prediction and error parity
	// AC5/AC6 promise.
	//
	// Only a plan that would actually write is checked. ApplyRefresh skips the
	// write for an unchanged record, so an unsafely-named record whose hashes are
	// already current keeps reporting "Current" and exiting 0 -- identical in both
	// modes, and no change to the real refresh path (AC4).
	if plan.Changed {
		if err := wiring.ValidateWritable(projectRoot, plan.updated); err != nil {
			return RefreshPlan{}, fmt.Errorf("wiring cannot be written: %w", err)
		}
	}
	return plan, nil
}

// ApplyRefresh writes the planned wiring record when the plan reports a change.
// It performs ONLY the write step; all validation and comparison already
// happened in PlanRefresh. Returns (true, nil) when the record was rewritten,
// (false, nil) when the plan was a no-op, or (false, error) on write failure.
func ApplyRefresh(projectRoot string, plan RefreshPlan) (bool, error) {
	if !plan.Changed {
		return false, nil
	}
	if _, err := wiring.Write(projectRoot, plan.updated); err != nil {
		return false, fmt.Errorf("writing refreshed wiring: %w", err)
	}
	return true, nil
}

// RefreshRecord recomputes stale hashes in an existing wiring record without
// requiring the user to supply --framework or --artefact. The identity fields
// (testcase, framework, adapter, artefact) are preserved verbatim; only the
// hash fields are updated.
//
// It is the composition of PlanRefresh (validate + compare) and ApplyRefresh
// (write). The dry-run preview calls PlanRefresh alone; the real refresh calls
// both -- so the drift logic is shared, never duplicated (BUG-146).
//
// Returns (true, nil) when the record was rewritten with updated hashes,
// (false, nil) when both hashes already match current content (no-op),
// or (false, error) on validation failure.
func RefreshRecord(projectRoot string, snap *testcase.Snapshot, rec *wiring.WiringRecord) (bool, error) {
	plan, err := PlanRefresh(projectRoot, snap, rec)
	if err != nil {
		return false, err
	}
	return ApplyRefresh(projectRoot, plan)
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

	// REV-158 CLAUDE-006: --check is a direct single-TC read, which ADR-024's
	// matrix requires to refuse an ambiguous id and name the owners rather than
	// present one arbitrary owner (or absence) as fact.
	_, ownerErr := resolveLinkOwner(projectRoot, tcID)
	if strict && ownerErr != nil {
		return result, ownerErr
	}
	var ownershipErr *testcase.OwnershipError
	if errors.As(ownerErr, &ownershipErr) {
		return result, ownershipErr
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

	// Authority is the ONE ownership decision that authorises this repoint,
	// built from the operation's single snapshot by AuthoriseSingleRepoint or
	// AuthoriseScopeRepoint and carried through to the write (ADR-024 s5 /
	// REV-160 CLAUDE-003). It is REQUIRED: every repoint entry point refuses
	// without it, so the ownership state that authorised the command is the state
	// the record is written under, and no caller can reach the mutation with an
	// unproven identity.
	Authority *RepointAuthority
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
	// REV-159 CODEX-004: scope validation is containment-only. It used to run
	// through discoverScopeSpecs, which recognised tc-*.md filenames and NOTHING
	// else -- so a folder whose only spec was a validly renamed owner such as
	// login.md reported "no test cases found", even though the snapshot recognises
	// its body identity perfectly well and every other bulk command operates on it.
	// Body identity is authoritative (ADR-024 s1), so the filename walk cannot be
	// what decides whether a scope is empty.
	if err := validateScopeFolder(projectRoot, scopeFolder); err != nil {
		return RepointSummary{}, err
	}
	// ADR-024 ownership preflight, INSIDE the mutating entry point (CODEX-005).
	// The work set is the body-identity owners in scope, and it is now the SOLE
	// source of both identity and the empty-scope decision.
	auth, err := opts.requireScopeAuthority(scopeFolder, recursive)
	if err != nil {
		return RepointSummary{}, err
	}
	if len(auth.ownerIDs) == 0 {
		return RepointSummary{}, fmt.Errorf("no test cases found in '%s/'", scopeFolder)
	}
	return repointSelected(projectRoot, auth.ownerIDs, auth.knownIDs, false, opts.FromAdapter, opts.NewAdapter, framework, opts.DryRun)
}

// validateScopeFolder rejects an absent, non-directory, or out-of-tree repoint
// scope before anything is enumerated. It resolves containment ONLY -- it forms
// no opinion about which specs the folder holds, because that is body identity's
// question and testcase.ResolveFolder does not read spec files.
//
// The wording of the absent-folder diagnostic is preserved from the filename walk
// this replaced, so the user-visible refusal is unchanged.
func validateScopeFolder(projectRoot, scopeFolder string) error {
	if _, err := testcase.ResolveFolder(projectRoot, scopeFolder); err != nil {
		if errors.Is(err, testcase.ErrFolderNotFound) {
			return fmt.Errorf("folder '%s/' does not exist under the test cases directory", scopeFolder)
		}
		return err
	}
	return nil
}

// RepointAll runs a project-wide repoint: every wiring record is in scope.
//
// REV-158 CODEX-005: --all had NO ownership preflight of any kind, so a direct
// caller could rewrite every wiring record in a project containing an ambiguous
// or unprovable identity. Like folder repoint it has no per-id hold-back, so any
// such identity anywhere refuses the whole operation before a single rewrite.
func RepointAll(projectRoot string, opts RepointOptions) (RepointSummary, error) {
	framework, err := opts.resolveNewAdapter()
	if err != nil {
		return RepointSummary{}, err
	}
	// REV-159 CODEX-002: the owner set is RETAINED, not discarded. --all used to
	// run the preflight for its refusal side-effect and then select every wiring
	// record with all=true, so a record whose test-case ID has no spec at all was
	// rewritten even though requireScopeOwnership never examined that ID. An empty
	// cases tree full of orphan wiring passed the preflight and was mutated --
	// mutation with no uniquely proven physical owner, which is what ADR-024's
	// matrix forbids for a wiring write.
	auth, err := opts.requireScopeAuthority("", true)
	if err != nil {
		return RepointSummary{}, err
	}
	return repointSelected(projectRoot, auth.ownerIDs, auth.knownIDs, true, opts.FromAdapter, opts.NewAdapter, framework, opts.DryRun)
}

// repointSelected is the shared selection+mutation loop for bulk and --all
// scopes. Malformed in-scope records are errors; valid in-scope records are
// repointed when their stored adapter matches fromAdapter and counted as skipped
// otherwise; the framework preflight and per-record writes are delegated to
// RepointBatch.
//
// The two scopes DIVERGE on malformed records (REV-160 F5). Scoped: malformed
// records are reported as errors and the healthy records are still repointed, by
// RepointBatch. Project-wide: any unrepointable record -- malformed,
// unattributable, or ownerless -- aborts the WHOLE operation before RepointBatch
// is called, so nothing is written. --all is all-or-nothing, because a
// project-wide rewrite that half-succeeds leaves no scope the operator can name
// to finish or undo it.
//
// ownerIDs is the set of body identities the ownership preflight PROVED for this
// scope. knownIDs is every provable identity in the project, from the same
// snapshot; it exists only to attribute corrupt filenames.
//
// The returned error is an all-or-nothing refusal of the WHOLE operation. Repoint
// has no per-ID hold-back policy, so any record we cannot attribute to a proven
// owner aborts before a single rewrite.
func repointSelected(projectRoot string, ownerIDs map[string]bool, knownIDs []string, all bool, fromAdapter, newAdapter, newFramework string, dryRun bool) (RepointSummary, error) {
	allResults, err := wiring.DiscoverAll(projectRoot)
	if err != nil {
		return RepointSummary{
			Errors:  1,
			Results: []RepointResult{{Status: "error", Error: fmt.Errorf("discovering wiring: %w", err)}},
		}, nil
	}

	// Attribution ids for a CORRUPT record: the proven in-scope owners plus every
	// provable identity in the project.
	//
	// REV-159 CLAUDE-003. A corrupt record used to be matched with
	// inScopeTCIDs[dr.TCFromName], and TCFromName comes from ParseFilename, which
	// splits the stem at the FIRST "--". That agreed with the old filename-derived
	// scope set, but the scope set is now parsed BODY identities, and the corrupt
	// key was left behind. Both directions broke:
	//
	//   - a malformed tc-77777777--legacy--bats.wiring.yaml belonging to the
	//     in-scope owner tc-77777777--legacy yields TCFromName tc-77777777, which
	//     is not in the set, so the record was silently DROPPED and the folder
	//     repoint reported success with zero errors -- malformed rendered as
	//     absence on a mutating consumer (ADR-024 s1);
	//   - and if the shorter tc-77777777 were the in-scope owner instead, that same
	//     out-of-scope record was counted as ITS error and the repoint exited
	//     non-zero blaming an unrelated identity -- the cross-identity coupling
	//     BUG-188 exists to remove.
	//
	// TCFromNameForScope resolves the split against identities known to exist, which
	// is what reader.wiringPathsByTestCase already does for delete.
	attribution := append(append([]string{}, knownIDs...), sortedKeys(ownerIDs)...)

	var selected []*wiring.WiringRecord
	var malformed []wiring.DiscoveryResult
	var unnamed []string
	var unowned []string
	skipped := 0
	for _, dr := range allResults {
		if dr.Err != nil {
			attributed, ok := wiring.TCFromNameForScope(filepath.Base(dr.Path), attribution)
			if ok && ownerIDs[attributed] {
				malformed = append(malformed, dr)
				continue
			}
			if all {
				// Project-wide scope: a corrupt record we cannot attribute to any
				// proven owner still lies inside the scope being rewritten, so it
				// refuses rather than disappearing.
				unnamed = append(unnamed, filepath.Base(dr.Path))
			}
			continue
		}
		if !ownerIDs[dr.Record.TestCase] {
			if all {
				// REV-159 CODEX-002: under --all this record IS in scope, and its
				// identity has no proven physical owner. Refuse instead of rewriting
				// state attributed to an identity nothing owns.
				unowned = append(unowned, filepath.Base(dr.Path))
			}
			continue
		}
		if dr.Record.Adapter == fromAdapter {
			selected = append(selected, dr.Record)
		} else {
			skipped++
		}
	}

	// REV-160 / tc-68c17c1a. The refusal must be DIAGNOSTICALLY COMPLETE before it
	// is atomic. This used to refuse on the ownerless records alone and return
	// before RepointBatch ever saw `malformed`, so a project-wide run that hit both
	// kinds named only the ownerless file and the malformed ones vanished from the
	// report -- malformed rendered as absence (ADR-024 s1), on the one command whose
	// whole job here is to account for every record it refuses to touch.
	//
	// The loop above is read-only and has already classified every record, so the
	// fix is not to let --all continue per record: it is to name all three groups in
	// one refusal, still before a single write.
	if all && (len(malformed) > 0 || len(unnamed) > 0 || len(unowned) > 0) {
		return RepointSummary{}, projectWideRepointRefusal(malformed, unnamed, unowned)
	}

	summary := RepointBatch(projectRoot, selected, malformed, newAdapter, newFramework, dryRun)
	summary.Skipped += skipped
	return summary, nil
}

// projectWideRepointRefusal builds the all-or-nothing --all refusal. Every record
// the scan could not clear is named, grouped by WHY it was rejected, so the
// operator can act on each without re-running to discover the next one.
func projectWideRepointRefusal(malformed []wiring.DiscoveryResult, unnamed, unowned []string) error {
	// REV-160 F1 (ADR-024 s7). These are repository-derived filenames going to a
	// terminal: a wiring file may be named with any byte but "/" and NUL, ESC
	// included, so an unsanitised %s lets a hand-edited name rewrite the screen
	// and disguise a refusal as a success. Route every name through the CENTRAL
	// sanitiser, the same one WiringIdentityMismatchError (REV-159 CODEX-005) and
	// reader.CorruptWiringError (REV-160 CLAUDE-007) use -- not a second local rule.
	//
	// Sanitising BEFORE the sort keeps the printed order stable against the
	// rendered form. F6: clean() copies, so the caller's slices are not reordered.
	clean := func(in []string) []string {
		out := make([]string, 0, len(in))
		for _, name := range in {
			out = append(out, testcase.SanitizeIdentityString(name))
		}
		sort.Strings(out)
		return out
	}
	bad := make([]string, 0, len(malformed))
	for _, dr := range malformed {
		bad = append(bad, filepath.Base(dr.Path))
	}
	bad = clean(bad)
	unnamedClean := clean(unnamed)
	unownedClean := clean(unowned)

	total := len(bad) + len(unnamedClean) + len(unownedClean)
	var b strings.Builder
	fmt.Fprintf(&b,
		"refusing a project-wide repoint: %d wiring record(s) cannot be repointed safely; no wiring was changed:",
		total)
	if len(bad) > 0 {
		fmt.Fprintf(&b, "\n    malformed (unparseable, or filename and body identity disagree): %s", strings.Join(bad, ", "))
	}
	if len(unnamedClean) > 0 {
		// REV-160 F3: NOT "unrecognised filename". A corrupt tc-88888888--bats
		// record whose id simply has no spec lands here too, and its filename
		// carries a perfectly good identity -- it is the OWNER that is missing.
		// The old wording sent the operator hunting a filename-shape problem
		// that does not exist.
		fmt.Fprintf(&b, "\n    unattributable (no resolvable test case owner for this filename): %s", strings.Join(unnamedClean, ", "))
	}
	if len(unownedClean) > 0 {
		fmt.Fprintf(&b, "\n    no proven test case owner: %s", strings.Join(unownedClean, ", "))
	}
	b.WriteString("\n    Fix, restore or remove these records before repointing, or repoint the owning folders individually.")
	return fmt.Errorf("%s", b.String())
}

// sortedKeys returns the set's members in a deterministic order.
func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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

	// ADR-024 s5 / REV-160 CLAUDE-003: the ownership decision is CARRIED, not
	// re-derived. This used to build its own project-wide snapshot -- the third
	// one for a qualified repoint -- so the identity the write was made under was
	// not necessarily the identity the CLI refused on. Now the one decision the
	// operation made travels here and the write is keyed on it.
	//
	// The preflight is still INSIDE this mutating entry point (REV-158
	// CODEX-005), and more strictly than before: an authority can only be minted
	// by AuthoriseSingleRepoint from a real snapshot, so a non-CLI caller cannot
	// reach the mutation at all without one. NotFound remains permitted -- see
	// AuthoriseSingleRepoint for the orphan-repair policy and its three controls.
	auth, aerr := opts.requireSingleAuthority(tcID)
	if aerr != nil {
		return RepointResult{}, aerr
	}

	// Everything below keys on auth.ID(), the id the carried decision proves,
	// rather than on the bare argument. They are equal by requireSingleAuthority's
	// check; using the carried value is what keeps the write bound to the decision.
	rec, wiringPath, err := wiring.Find(projectRoot, auth.ID(), framework)
	if err != nil {
		return RepointResult{}, fmt.Errorf("reading wiring for %s--%s: %w", auth.ID(), framework, err)
	}
	if rec == nil {
		return RepointResult{}, fmt.Errorf("no wiring record found for %q (framework: %s)", auth.ID(), framework)
	}

	// REV-159 CLAUDE-004. wiring.Find locates the record by PATH -- it builds
	// tc-<id>--<framework>.wiring.yaml and reads whatever is there. Nothing checked
	// that the file's BODY declares the identity we asked for, and RepointRecord
	// then recomputes its WRITE path from rec.TestCase. So a file
	// tc-x--bats.wiring.yaml whose body declares tc-y made `gtms link tc-x
	// --repoint --adapter new` read tc-x's file, pass the sameWiringSelection
	// re-read against tc-y's file, and write the new adapter into tc-y's record --
	// reporting success for tc-x, whose own file was never touched.
	//
	// That is a mutation of a DIFFERENT test case's data reported as success for
	// yours. ADR-024's consumer matrix requires "Link/refresh/repoint write" to
	// never overwrite a mismatch; LinkRecord and refresh got that guard, repoint
	// did not. Same typed error, so the CLI renders one diagnostic and suppresses
	// the --force hint that cannot help here.
	if rec.TestCase != auth.ID() || rec.Framework != framework {
		return RepointResult{}, &WiringIdentityMismatchError{
			Path:        wiringPath,
			DeclaredTC:  rec.TestCase,
			DeclaredFW:  rec.Framework,
			RequestedTC: auth.ID(),
			RequestedFW: framework,
		}
	}

	if opts.FromAdapter != "" {
		if rec.Adapter != opts.FromAdapter {
			return RepointResult{}, fmt.Errorf("wiring for %s--%s is currently adapter %q, not %q",
				auth.ID(), framework, rec.Adapter, opts.FromAdapter)
		}
	} else if rec.Adapter == opts.NewAdapter {
		return RepointResult{
			TestCase:   auth.ID(),
			Framework:  framework,
			OldAdapter: rec.Adapter,
			NewAdapter: opts.NewAdapter,
			Status:     "already-current",
		}, nil
	}

	res, _ := RepointRecord(projectRoot, rec, opts.NewAdapter, opts.DryRun)
	return res, nil
}
