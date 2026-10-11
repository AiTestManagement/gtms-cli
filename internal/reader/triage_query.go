package reader

import (
	"fmt"
	"sort"

	"github.com/aitestmanagement/gtms-cli/internal/testcase"
	"github.com/aitestmanagement/gtms-cli/internal/triage"
)

// ENH-201 read surface for structured triage records.
//
// ShowTriage returns one record; ListTriage returns many for a scope. Both join
// each record to the ENH-146 terminal-result overlay so the read surface can
// report whether the latest execution result still matches what was classified.
// The comparison is RAW-vs-RAW (skip stays skip, never the presentation value
// skipped) and NULLABLE: nil means "no latest terminal result to compare
// against", which a plain Boolean cannot express honestly.

// TriageView is one triage record joined to the terminal-result overlay for its
// (test case, framework) pair.
type TriageView struct {
	Record *triage.Record

	// ResultChanged compares the classified raw snapshot result against the
	// latest terminal raw result: non-nil true when they differ, non-nil false
	// when they match, and nil when no latest terminal handoff exists to compare
	// against (comparison unavailable).
	ResultChanged *bool

	// SpecMissing is true when no test-case spec exists anywhere in the cases
	// tree for the record's test case (an orphan). Computed for the root list
	// and the test-case-target list (ENH-210) -- both query the record namespace
	// rather than derive scope from specs -- whose human output marks such
	// records "[spec missing]". A folder list is spec-scoped, so orphans never
	// appear and SpecMissing stays false there.
	SpecMissing bool
}

// ShowTriage selects one triage record for (testCaseID, framework) using the
// shared selection rule, reads and validates it, and joins it to the latest
// terminal result. Selection is never lifecycle-filtered: a resolved record is
// returned directly (ENH-201 item 5). Selection reads no spec, wiring, or
// handoff; only the result JOIN consults the overlay, and its absence yields a
// nil comparison rather than an error.
func ShowTriage(projectRoot, testCaseID, framework string) (*TriageView, error) {
	path, err := selectTriageRecordPath(projectRoot, testCaseID, framework)
	if err != nil {
		return nil, err
	}
	rec, err := triage.Read(projectRoot, path)
	if err != nil {
		return nil, err
	}

	// Single-TC overlay is sufficient for one record (ENH-201 item 7).
	overlay, err := scanTerminalResultsForTC(projectRoot, rec.TestCaseID)
	if err != nil {
		return nil, fmt.Errorf("scanning terminal results: %w", err)
	}
	return &TriageView{Record: rec, ResultChanged: compareRawResult(rec, overlay)}, nil
}

// ListTriage enumerates, validates, filters, joins, and orders triage records
// for a scope. A nil scope is the root list (every triage record, including
// orphans). A non-nil scope is spec-scoped: test case IDs are discovered from
// the specifications under the scope, then each ID's records are enumerated.
//
// The list is FAIL-CLOSED (ENH-201 item 14): every in-scope record is read and
// validated before any view is produced, so one malformed in-scope record
// fails the whole call with its path and no partial result. A malformed record
// outside a folder's scope is never read and so cannot poison that folder's
// list.
//
// The result JOIN uses ONE canonical bulk terminal-result scan for the whole
// project (ENH-201 item 7); it must not scan per test case or per record.
func ListTriage(projectRoot string, scope *ScopeInfo, valueFilter string, includeResolved bool) ([]TriageView, error) {
	views, diags, err := ListTriageWithDiagnostics(projectRoot, scope, valueFilter, includeResolved)
	if err != nil {
		return nil, err
	}
	if ierr := incompleteReadErr(diags); ierr != nil {
		return nil, ierr
	}
	return views, nil
}

// ListTriageWithDiagnostics is the ENH-205 triage listing on the snapshot. It
// resolves each record's ID and the scope's identities from ONE snapshot per
// operation, and applies policy (e) point 8:
//
//   - A record whose ID resolves Ambiguous or Unprovable is left out (it cannot be
//     attributed to one test case) and the identity is diagnosed.
//   - "[spec missing]" now means the snapshot returned NotFound for that ID, so a
//     renamed owner is no longer reported missing.
//   - The root listing's scope is the whole tree: an Unprovable candidate tied to no
//     record is still diagnosed, and the command exits 1.
//   - A folder listing selects records through the snapshot's in-scope proven owners;
//     an Ambiguous ID with an owner in scope is excluded and diagnosed.
func ListTriageWithDiagnostics(projectRoot string, scope *ScopeInfo, valueFilter string, includeResolved bool) ([]TriageView, IdentityDiagnostics, error) {
	snap, err := testcase.NewSnapshot(projectRoot)
	if err != nil {
		return nil, IdentityDiagnostics{}, err
	}

	var recs []*triage.Record
	var resolve triageRecordResolver

	// ONE identity pass per operation: the scope's proven owners and the
	// diagnostics come from the same resolveScopedSpecs call.
	specs, diags, err := resolveScopedSpecs(snap, projectRoot, scope)
	if err != nil {
		return nil, IdentityDiagnostics{}, err
	}

	if scope == nil {
		// Root list: enumerate EVERY record and validate each. Identity comes from
		// Read, not the filename. The diagnostics scope is the whole tree, so every
		// Ambiguous/Unprovable identity in the tree is surfaced -- including an
		// Unprovable candidate tied to no record.
		all, aerr := triage.ListAll(projectRoot)
		if aerr != nil {
			return nil, IdentityDiagnostics{}, aerr
		}
		recs = make([]*triage.Record, 0, len(all))
		for _, r := range all {
			rec, rerr := triage.Read(projectRoot, r.Path)
			if rerr != nil {
				return nil, IdentityDiagnostics{}, rerr
			}
			recs = append(recs, rec)
		}
		resolve = snapshotTriageResolver(snap)
	} else {
		// Folder list: discover the in-scope PROVEN test-case IDs from the snapshot,
		// then attribute records by validated body identity. An Ambiguous owner in
		// scope is excluded from the proven ids (and diagnosed), so its records are
		// not selected. A folder is spec-scoped, so orphans never appear and
		// SpecMissing stays false.
		//
		// ADR-024 5b point 8: each record's ID is resolved with section 4a's PER-ID
		// relevance, exactly as the bare single-TC form resolves it -- the one
		// exception to the scope-only bulk relevance rule. So a proven in-scope
		// owner whose ID is Unprovable per-id (an unreadable file elsewhere whose
		// name matches it) has its records left out AND is diagnosed: never a
		// silent absence (REV-184 CLAUDE-001).
		ids := make([]string, 0, len(specs))
		seen := make(map[string]bool)
		for _, tc := range specs {
			if seen[tc.ID] {
				continue
			}
			seen[tc.ID] = true
			if r := snap.Resolve(tc.ID); r.Outcome == testcase.OutcomeUnprovable {
				diags.addUnprovableResolution(r)
				continue
			}
			ids = append(ids, tc.ID)
		}
		var rerr error
		recs, rerr = flattenScopedRecords(projectRoot, ids)
		if rerr != nil {
			return nil, IdentityDiagnostics{}, rerr
		}
		resolve = folderTriageResolver
	}

	views, err := buildTriageViews(projectRoot, recs, resolve, valueFilter, includeResolved)
	if err != nil {
		return nil, IdentityDiagnostics{}, err
	}
	return views, diags, nil
}

// ListTriageForTC lists every triage record stored for one test case, queried by
// its bare ID against the RECORD namespace (never via a spec), exactly as --show
// selects. It is the ENH-210 test-case-target list route: it lists all framework
// records for the test case, marks an orphaned record [spec missing] (SpecMissing
// computed), and inherits scopedTriageRecords' exact-body-identity attribution
// and relevant/unprovable fail-closed boundary. A prefix sibling (tc-foo--bar)
// is never attributed to the shorter ID; a malformed record whose filename could
// represent tcID fails the query closed with its path; a malformed record whose
// filename cannot represent tcID is never opened.
func ListTriageForTC(projectRoot, tcID, valueFilter string, includeResolved bool) ([]TriageView, error) {
	snap, err := testcase.NewSnapshot(projectRoot)
	if err != nil {
		return nil, err
	}
	return ListTriageForTCWithSnapshot(snap, projectRoot, tcID, valueFilter, includeResolved)
}

// ListTriageForTCWithSnapshot is ListTriageForTC against a snapshot the caller
// already built, so a qualified or bare `triage --list <tc-id>` runs its ownership
// check and this listing against ONE snapshot (ADR-024 5b "Qualified targets resolve
// once"). Spec existence is not a prerequisite for a bare record lookup; the orphan
// marker comes from the snapshot ("[spec missing]" == NotFound).
//
// The single-target ownership rule is enforced HERE, not left to the caller
// (REV-184 CODEX-004): an ID whose spec is Ambiguous or Unprovable is refused with
// the typed *testcase.OwnershipError and no views, because a record keyed by that
// ID cannot be attributed to one test case (ADR-024 5b "Single-TC forms").
func ListTriageForTCWithSnapshot(snap *testcase.Snapshot, projectRoot, tcID, valueFilter string, includeResolved bool) ([]TriageView, error) {
	if oerr := snap.RequireOwnership(tcID); oerr != nil {
		return nil, oerr
	}
	recs, err := flattenScopedRecords(projectRoot, []string{tcID})
	if err != nil {
		return nil, err
	}
	return buildTriageViews(projectRoot, recs, snapshotTriageResolver(snap), valueFilter, includeResolved)
}

// triageRecordResolver decides, per record test-case ID, whether the record's spec
// is missing (renders "[spec missing]") and whether the record must be OMITTED
// because its ID cannot be attributed to one test case (Ambiguous/Unprovable).
type triageRecordResolver func(tcID string) (specMissing, omit bool)

// snapshotTriageResolver resolves a record's ID through the snapshot: NotFound is
// spec-missing (listed), Ambiguous/Unprovable is omitted, UniqueOwner is listed and
// present.
func snapshotTriageResolver(snap *testcase.Snapshot) triageRecordResolver {
	return func(tcID string) (bool, bool) {
		switch snap.Resolve(tcID).Outcome {
		case testcase.OutcomeNotFound:
			return true, false
		case testcase.OutcomeAmbiguous, testcase.OutcomeUnprovable:
			return false, true
		default:
			return false, false
		}
	}
}

// folderTriageResolver is used by a folder list, which selects records only for
// proven in-scope owners: nothing is spec-missing (spec-scoped) and nothing is
// omitted here (Ambiguous owners were already excluded from the proven id set).
func folderTriageResolver(string) (bool, bool) { return false, false }

// flattenScopedRecords enumerates the triage records owned by the given test-case
// IDs through the body-authoritative scopedTriageRecords boundary and flattens
// the tcID->framework->record map into a flat slice. ids must be NON-NIL (an
// empty non-nil slice is an empty scope; a nil slice would read every record).
func flattenScopedRecords(projectRoot string, ids []string) ([]*triage.Record, error) {
	byTC, err := scopedTriageRecords(projectRoot, ids)
	if err != nil {
		return nil, err
	}
	var recs []*triage.Record
	for _, byFramework := range byTC {
		for _, rec := range byFramework {
			recs = append(recs, rec)
		}
	}
	return recs, nil
}

// buildTriageViews joins each record to the ONE canonical bulk terminal-result
// scan, applies the lifecycle-then-value filters, optionally computes the orphan
// [spec missing] marker, and returns views in the deterministic (test_case_id,
// framework) order every list scope shares. It is the single builder behind the
// root, folder, and test-case-target list routes so their JSON and human shapes
// cannot diverge.
func buildTriageViews(projectRoot string, recs []*triage.Record, resolve triageRecordResolver, valueFilter string, includeResolved bool) ([]TriageView, error) {
	wiringByTC, err := wiringScan(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("scanning wiring records: %w", err)
	}
	overlay := scanTerminalResults(projectRoot, wiringByTC)

	views := make([]TriageView, 0, len(recs))
	for _, rec := range recs {
		if !includeResolved && rec.Status != triage.StatusActive {
			continue
		}
		if valueFilter != "" && rec.Value != valueFilter {
			continue
		}
		// ENH-205: the snapshot decides orphan-vs-present and whether the record's
		// ID is attributable to one test case. A record whose ID is Ambiguous or
		// Unprovable is omitted (its identity is diagnosed by the operation).
		specMissing, omit := resolve(rec.TestCaseID)
		if omit {
			continue
		}
		v := TriageView{Record: rec, ResultChanged: compareRawResult(rec, overlay), SpecMissing: specMissing}
		views = append(views, v)
	}

	sort.Slice(views, func(i, j int) bool {
		a, b := views[i].Record, views[j].Record
		if a.TestCaseID != b.TestCaseID {
			return a.TestCaseID < b.TestCaseID
		}
		return a.Framework < b.Framework
	})
	return views, nil
}

// compareRawResult joins a record to the terminal-result overlay and returns
// the nullable raw-vs-raw comparison. A missing overlay hit yields nil
// (comparison unavailable). The comparison never normalises: a skip snapshot
// against a skip result is false, not a comparison of skip against skipped.
func compareRawResult(rec *triage.Record, overlay map[string]overlayHit) *bool {
	hit, ok := overlay[overlayKey(rec.TestCaseID, rec.Framework)]
	if !ok {
		return nil
	}
	changed := rec.Snapshot.Result != hit.RawResult
	return &changed
}
