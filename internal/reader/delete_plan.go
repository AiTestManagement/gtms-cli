package reader

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// canonicalPathKey returns a comparison key for an absolute path. On
// case-insensitive filesystems (Windows) two paths that differ only in case
// name the same physical file, so they must collapse to one identity;
// otherwise a differently-cased alias would slip past the shared-artefact
// reference check and a forced delete could remove a file a surviving test
// case still points to (CODEX-002). The original path is preserved separately
// for display and removal -- this key is used only for matching/dedup.
func canonicalPathKey(abs string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(abs)
	}
	return abs
}

// artefactCand pairs an artefact's original absolute path (used for physical
// removal) with its project-relative display path. Candidates are keyed by
// canonicalPathKey so aliases and case-variants dedupe to one entry.
type artefactCand struct {
	abs string
	rel string
}

// dedupByKey returns paths with duplicates (by canonicalPathKey) removed,
// preserving first-seen order. Defends bulk/folder deletes against duplicate
// frontmatter TC IDs, which would otherwise gather the same physical path more
// than once and fail mid-operation on the second removal (CODEX-005).
func dedupByKey(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	out := paths[:0:0]
	for _, p := range paths {
		k := canonicalPathKey(p)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, p)
	}
	return out
}

// filterOutKeys drops any path whose canonicalPathKey is in exclude, preserving
// order. Used to give preserved artefacts precedence over every GTMS-owned
// deletion category, not just result files (CODEX-001).
func filterOutKeys(paths []string, exclude map[string]bool) []string {
	if len(exclude) == 0 {
		return paths
	}
	out := paths[:0:0]
	for _, p := range paths {
		if exclude[canonicalPathKey(p)] {
			continue
		}
		out = append(out, p)
	}
	return out
}

// deletePlan is the fully-resolved set of filesystem changes for a delete
// operation, computed BEFORE any removal (BUG-173). Planning the whole
// operation up front makes bulk output independent of TC iteration order and
// makes a dry-run preview identical to the subsequent real run: nothing is
// removed while the plan is built, so a shared artefact is classified the same
// way whether or not earlier siblings would have been deleted first.
type deletePlan struct {
	testCasesProcessed int
	specs              []string // absolute paths (spec files; empty when keepSpec)
	records            []string // wiring + manual records for selected TCs
	taskFiles          []string
	resultContracts    []string
	artefactDeletes    []string // canonical abs artefact paths to physically remove
	resultFiles        []string // executed_artefact result files (deduped, minus artefact candidates)
	preserved          []PreservedArtefact
}

// buildDeletePlan resolves the complete deletion plan for a single TC (tcID
// non-empty) or a scope of TCs (tcID empty).
//
// Atomicity (ENH-128 AC #5): every SELECTED test case's declared artefact and
// executed_artefact path is resolved via pathsafe.ResolveUnderRoot during
// planning, REGARDLESS of force. A selected path that escapes the project-owned
// allowlist returns a *PathSafetyError and the whole operation aborts before
// any removal -- even for a plain, preserve-by-default delete.
//
// Referential protection (BUG-173): each referenced artefact is classified
// against project-wide wiring/manual references. Plain delete preserves every
// referenced artefact. A forced delete removes an artefact only when every
// reference to it belongs to the selected deletion set; otherwise the artefact
// is preserved and the surviving external TC IDs are recorded for the warning.
func buildDeletePlan(projectRoot string, scope *ScopeInfo, tcID string, keepSpec, force bool) (*deletePlan, error) {
	// Canonicalise the root the same way pathsafe.ResolveUnderRoot does, so the
	// GTMS-owned paths built by joining under it (specs/records/tasks/contracts)
	// and the display paths from toProjectRel share one identity with artefact
	// paths that go through ResolveUnderRoot. Without this, a Windows 8.3 short
	// root (RUNNER~1) and the long form (runneradmin) diverge: canonical keys
	// stop matching (preserved artefacts leak into a delete category) and
	// filepath.Rel emits a spurious "../../" escape (BUG-173 Windows CI).
	projectRoot = pathsafe.CanonicalRoot(projectRoot)

	// ADR-024: one immutable project-wide snapshot backs the whole plan, so a
	// dry-run preview and the real removal share one ownership decision and the
	// tree is enumerated exactly once (BUG-185 instrumented-enumeration
	// requirement).
	snap, err := testcase.NewSnapshot(projectRoot)
	if err != nil {
		return nil, err
	}

	// 1. Resolve the selection set of TC IDs from the snapshot.
	//    Folder scope selects the REAL owners physically under the (canonical)
	//    scope directory; a spec-file symlink never confers its target's
	//    membership, and an in-scope Unprovable candidate refuses the whole plan.
	var selection []string
	if tcID != "" {
		// ADR-024 s3 / REV-158 CLAUDE-001: a folder-qualified single-TC delete
		// requires MUTATION AUTHORITY under that folder, not mere reachability. The
		// plan below removes the owner's CANONICAL file, so a spec symlink must
		// never make a target elsewhere deletable "because it was reached through
		// that link". An unqualified target (nil scope) is unchanged.
		if scope != nil {
			if err := snap.RequireMutationAuthorityUnderScope(scopeRelFor(projectRoot, scope), tcID); err != nil {
				return nil, err
			}
		}
		selection = []string{tcID}
	} else {
		// REV-159 CLAUDE-005. scopeRelFor tolerates a nil scope and returns "", but
		// scope.Recursive on the next argument dereferences it. The equivalent line
		// in the reset rewrite (internal/reader/reset.go) writes
		// `scope != nil && scope.Recursive`, so the two destructive planners
		// disagreed on the same input shape.
		//
		// This is a regression introduced on THIS branch: at b524c9b5 the folder
		// branch called scanTestCases(projectRoot, scope), which handled a nil scope
		// explicitly and scanned the whole tree. It is not reachable from the CLI --
		// delete's folder mode always builds a scope and its TC mode always passes a
		// non-empty id -- but reader.DeleteArtifacts is an exported API, and a call
		// that previously returned a plan now crashes the process.
		//
		// Refusing explicitly rather than mirroring reset's `scope != nil &&`: for a
		// DESTRUCTIVE planner, silently selecting nothing is indistinguishable from
		// "the project is empty", and restoring the old whole-project selection would
		// make a nil argument mean "delete everything". Neither is a good default for
		// an all-or-nothing destructive plan. A caller that means the whole project
		// says so with a scope.
		if scope == nil {
			return nil, fmt.Errorf("a folder-scoped delete requires a scope; pass a *ScopeInfo, or name a test case ID")
		}
		scopeRel := scopeRelFor(projectRoot, scope)
		ids, inScopeUnprovable := snap.SelectFolder(scopeRel, scope.Recursive)
		if len(inScopeUnprovable) > 0 {
			return nil, testcase.UnprovableInScope(scopeRel, inScopeUnprovable[0])
		}
		selection = ids
	}
	selection = dedupByKey(selection)

	// ADR-024 destructive authority: every selected id must be a UNIQUE
	// project-wide owner before any file is planned. Ambiguous or Unprovable
	// ownership refuses the WHOLE plan (delete/reset are all-or-nothing). A
	// NotFound id is NOT refused -- delete cleans orphaned flat state (wiring,
	// tasks, handoffs) keyed by exact id even when no spec remains.
	ownerAbs := make(map[string]string, len(selection))
	for _, id := range selection {
		res := snap.Resolve(id)
		switch res.Outcome {
		case testcase.OutcomeAmbiguous, testcase.OutcomeUnprovable:
			return nil, testcase.OwnershipErrorFor(res)
		case testcase.OutcomeUniqueOwner:
			ownerAbs[id] = res.OwnerCanonicalAbs
		}
	}

	selectionSet := make(map[string]bool, len(selection))
	for _, id := range selection {
		selectionSet[id] = true
	}

	// 2. Project-wide reference index: canonical artefact key -> set of TC IDs
	//    that reference it. `unreadable` lists records that could not be parsed.
	//    A forced delete must not proceed on an incomplete index: a malformed
	//    surviving record could hide the only remaining reference to a shared
	//    artefact, so force fails closed (CODEX-003). Plain preserve-by-default
	//    delete stays lenient -- it removes no artefacts, so a missed reference
	//    cannot cause loss.
	refIndex, unreadable := buildArtefactReferenceIndex(projectRoot)
	if force && len(unreadable) > 0 {
		return nil, fmt.Errorf(
			"cannot safely honour --force-artefact-deletion: %d wiring/manual record(s) could not be parsed, so shared-artefact protection cannot be guaranteed; fix or remove then retry: %s",
			len(unreadable), strings.Join(unreadable, ", "))
	}

	// Enumerate wiring ONCE and index by body identity, reused across the whole
	// selection (M2: avoid re-parsing every wiring file per selected id).
	//
	// The attribution universe for a CORRUPT record is every id this project
	// provably contains, plus the ids being deleted. Both halves are needed: the
	// known ids let an extended sibling (tc-x--legacy) claim its own filename
	// instead of losing it to its shorter prefix, and the selection keeps an
	// ORPHANED id -- one whose spec is already gone but whose flat wiring remains --
	// able to claim its record. Body identity is untouched by this; it governs
	// only records that cannot be read.
	attributionIDs := append(snap.KnownIDs(), selection...)
	wiringIndex, corruptWiring, werr := wiringPathsByTestCase(projectRoot, attributionIDs)
	if werr != nil {
		return nil, werr
	}

	// F2 (ADR-024 s1): a corrupt wiring record RELEVANT to a selected id -- its
	// filename attributes it to that id but its body is malformed, unreadable, or
	// declares a different identity -- is a data-integrity error, NEVER absence.
	// Abort the WHOLE plan before any mutation, regardless of
	// --force-artefact-deletion: silently treating it as absent would delete the
	// spec/tasks/handoffs and leave the unattributed corrupt wiring behind.
	// Corrupt wiring attributed to some OTHER id stays out of scope (relevance
	// rule), including a `--` sibling of the id being deleted.
	//
	// REV-160 CLAUDE-007: rendered through the typed *CorruptWiringError so the
	// repository-derived path and parse error pass the CENTRAL sanitiser
	// (ADR-024 s7) instead of being interpolated raw with %s, and so runDelete
	// can give it the same output.Errorf shape as the other refusals.
	for _, id := range selection {
		if cw, bad := corruptWiring[id]; bad {
			return nil, &CorruptWiringError{TCID: id, Path: cw.Path, Err: cw.Err}
		}
	}

	plan := &deletePlan{}

	// artefactCandidates: canonical key -> {abs, rel}, deduplicated across the
	// whole selection (BUG-073 + BUG-173 batch dedupe; CODEX-002 case-fold).
	artefactCandidates := make(map[string]artefactCand)
	// resultFileSeen dedupes executed_artefact result files across the selection.
	resultFileSeen := make(map[string]bool)
	var resultFileOrder []string

	// 3. Gather per selected TC.
	for _, id := range selection {
		plan.testCasesProcessed++

		if !keepSpec {
			// ADR-024: the spec to remove is the UNIQUE physical owner resolved above
			// (by body identity), never a filename-prefix match. A NotFound id has no
			// spec to remove (orphan cleanup still proceeds for its flat state).
			if owner, ok := ownerAbs[id]; ok {
				plan.specs = append(plan.specs, owner)
			}
		}

		records := automationRecordsForID(projectRoot, id, wiringIndex)
		plan.records = append(plan.records, records...)

		// Atomic precheck: resolve declared artefact paths. A path-safety
		// violation aborts the entire plan before any removal.
		scripts, err := findTestScripts(projectRoot, records)
		if err != nil {
			return nil, err
		}
		for _, abs := range scripts {
			k := canonicalPathKey(abs)
			if _, seen := artefactCandidates[k]; !seen {
				artefactCandidates[k] = artefactCand{abs: abs, rel: toProjectRel(projectRoot, abs)}
			}
		}

		resultFiles, err := findResultFilesFromRecords(projectRoot, records)
		if err != nil {
			return nil, err
		}
		for _, abs := range resultFiles {
			k := canonicalPathKey(abs)
			if !resultFileSeen[k] {
				resultFileSeen[k] = true
				resultFileOrder = append(resultFileOrder, abs)
			}
		}

		tasks, err := findTaskFiles(projectRoot, id)
		if err != nil {
			return nil, fmt.Errorf("finding task files for %s: %w", id, err)
		}
		plan.taskFiles = append(plan.taskFiles, tasks...)

		contracts, err := findResultContracts(projectRoot, id)
		if err != nil {
			return nil, fmt.Errorf("finding result contracts for %s: %w", id, err)
		}
		plan.resultContracts = append(plan.resultContracts, contracts...)
	}

	// 4. Classify each deduped artefact candidate. Iterate in sorted canonical
	//    order so output is deterministic regardless of TC iteration order.
	//    preservedKeys records the canonical key of every artefact that is kept,
	//    so it can be excluded from every GTMS-owned deletion category below.
	keys := make([]string, 0, len(artefactCandidates))
	for k := range artefactCandidates {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	preservedKeys := make(map[string]bool)
	for _, k := range keys {
		cand := artefactCandidates[k]

		var external []string
		for tc := range refIndex[k] {
			if !selectionSet[tc] {
				external = append(external, tc)
			}
		}
		sort.Strings(external)

		switch {
		case !force:
			// Safe default: preserve every referenced artefact.
			plan.preserved = append(plan.preserved, PreservedArtefact{Path: cand.rel})
			preservedKeys[k] = true
		case len(external) > 0:
			// Forced, but a TC outside the deletion set still references it.
			plan.preserved = append(plan.preserved, PreservedArtefact{Path: cand.rel, SurvivingTCs: external})
			preservedKeys[k] = true
		default:
			// Forced and exclusive to the deletion set: delete exactly once.
			plan.artefactDeletes = append(plan.artefactDeletes, cand.abs)
		}
	}

	// 5. Result files: keep the existing "delete executed_artefact output"
	//    behaviour, but EXCLUDE any path that is also an artefact candidate
	//    (BUG-073 cross-category dedupe -- artefact classification wins, so a
	//    preserved artefact is never removed via the result-file backdoor).
	for _, abs := range resultFileOrder {
		if _, isArtefact := artefactCandidates[canonicalPathKey(abs)]; isArtefact {
			continue
		}
		plan.resultFiles = append(plan.resultFiles, abs)
	}

	// 6. Preserved artefacts win over EVERY GTMS-owned category, not only
	//    result files: an artefact wired to a path that is also a selected TC's
	//    spec/record/task/contract must not be removed through that category
	//    while the summary reports it preserved (CODEX-001). Also dedupe each
	//    physical list by canonical key so a duplicate frontmatter ID cannot
	//    schedule the same path for removal twice (CODEX-005).
	plan.specs = dedupByKey(filterOutKeys(plan.specs, preservedKeys))
	plan.records = dedupByKey(filterOutKeys(plan.records, preservedKeys))
	plan.taskFiles = dedupByKey(filterOutKeys(plan.taskFiles, preservedKeys))
	plan.resultContracts = dedupByKey(filterOutKeys(plan.resultContracts, preservedKeys))
	plan.resultFiles = dedupByKey(filterOutKeys(plan.resultFiles, preservedKeys))

	return plan, nil
}

// buildArtefactReferenceIndex scans ALL project wiring records (and manual
// records) and returns a map from an artefact's canonical path key to the set
// of test case IDs that reference it, plus a list of record paths that could
// not be parsed. Keying via canonicalPathKey on the pathsafe-resolved absolute
// path means aliases (`path/f` vs `./path/f`) and, on Windows, case-variants
// collapse to one entry, so a surviving alias reference still protects a shared
// artefact (BUG-173 spec tc-9dcaf5c0; CODEX-002).
//
// A reference whose path fails to resolve is skipped rather than fatal -- it is
// not a deletion target. But a record that cannot be PARSED at all is reported
// in `unreadable`: its artefact reference is invisible to this index, so a
// forced delete relying on the index to prove exclusivity must fail closed
// rather than silently delete a still-shared file (CODEX-003). The caller
// enforces that only for the destructive force path; plain preserve-by-default
// delete ignores `unreadable` because it removes no artefacts.
func buildArtefactReferenceIndex(projectRoot string) (index map[string]map[string]bool, unreadable []string) {
	index = make(map[string]map[string]bool)

	add := func(tc, rawArtefact string) {
		if tc == "" || rawArtefact == "" {
			return
		}
		abs, _, err := pathsafe.ResolveUnderRoot(projectRoot, rawArtefact)
		if err != nil {
			return
		}
		k := canonicalPathKey(abs)
		if index[k] == nil {
			index[k] = make(map[string]bool)
		}
		index[k][tc] = true
	}

	// Wiring records (project-wide). The reference's TC identity is the record's
	// PARSED body `testcase` field, NOT a filename split (ADR-024 / BUG-188): the
	// old wiring.ParseFilename split `tc-77777777--legacy--playwright` at the first
	// `--` and mis-attributed the extended sibling's artefact reference to the
	// shorter id, collapsing the shared-artefact protection for a `--` sibling
	// (tc-85334613). A record whose artefact reference cannot be determined --
	// unparseable, or filename/body identity mismatch, or empty artefact -- is
	// reported unreadable so a forced delete fails closed (CODEX-003).
	discovered, derr := wiring.DiscoverAll(projectRoot)
	if derr != nil {
		unreadable = append(unreadable, fmt.Sprintf("%s (%v)", layout.WiringDir(projectRoot), derr))
	}
	for _, dr := range discovered {
		if dr.Record == nil {
			unreadable = append(unreadable, dr.Path)
			continue
		}
		fields, perr := parseRecordArtefactFields(dr.Path)
		if perr != nil || fields.Artefact == "" {
			unreadable = append(unreadable, dr.Path)
			continue
		}
		add(dr.Record.TestCase, fields.Artefact)
	}

	// Manual records also carry an `artefact:` field.
	manualDir := filepath.Join(projectRoot, layout.Current().Manual, "records")
	if entries, err := os.ReadDir(manualDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), "--manual.result.yaml") {
				continue
			}
			path := filepath.Join(manualDir, e.Name())
			fields, perr := parseRecordArtefactFields(path)
			if perr != nil {
				unreadable = append(unreadable, path)
				continue
			}
			if fields.Artefact == "" {
				continue
			}
			tc := strings.TrimSuffix(e.Name(), "--manual.result.yaml")
			add(tc, fields.Artefact)
		}
	}

	sort.Strings(unreadable)
	return index, unreadable
}

// scopeRelFor renders a scope's scan directory as a CANONICAL project-relative
// path, for snapshot scope selection.
//
// REV-158 CLAUDE-004: the scan directory must be canonicalised before it is
// relativised, because the project root it is relativised against already is.
// ResetExecuteResults and buildDeletePlan both canonicalise the root, but the
// root ScopeInfo is built from the RAW root in internal/cli/scope.go, so whenever
// the project is reached through a symlink -- macOS /var and /tmp, a symlinked
// home or work directory, a Windows 8.3 short path -- the two disagreed.
// filepath.Rel then produced a "../.."-prefixed string, logicalInScope matched
// nothing, and reset reported "[dry-run] Nothing to clear." and exited 0 from a
// project it had just listed one test case for. The old resetByScope walked
// ScanDir directly and was unaffected, so this was a silent regression with a
// success exit code.
//
// Canonicalising HERE rather than at the caller is deliberate: it also makes a
// directory alias resolve to its real directory for callers that build a scope
// by hand, which the folder-qualified authority check depends on (ADR-024 s3).
func scopeRelFor(projectRoot string, scope *ScopeInfo) string {
	if scope == nil {
		return ""
	}
	return toProjectRel(projectRoot, pathsafe.CanonicalRoot(scope.ScanDir))
}

// toProjectRel renders an absolute path as a forward-slash project-relative
// path for display. Mirrors the CLI's existing filepath.Rel(projectRoot, ...)
// treatment of DeleteResult.FilesDeleted so preserved and deleted paths read
// identically.
func toProjectRel(projectRoot, abs string) string {
	rel, err := filepath.Rel(projectRoot, abs)
	if err != nil {
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(rel)
}
