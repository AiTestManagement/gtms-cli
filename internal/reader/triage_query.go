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
	var recs []*triage.Record
	computeSpecMissing := false

	if scope == nil {
		// Root list: enumerate EVERY record and validate each. Identity comes
		// from Read, not the filename, so the root list includes records whose
		// names cannot be split and FAILS on a malformed one rather than dropping
		// it. Orphans are marked, so SpecMissing is computed.
		all, err := triage.ListAll(projectRoot)
		if err != nil {
			return nil, err
		}
		recs = make([]*triage.Record, 0, len(all))
		for _, r := range all {
			rec, rerr := triage.Read(projectRoot, r.Path)
			if rerr != nil {
				return nil, rerr
			}
			recs = append(recs, rec)
		}
		computeSpecMissing = true
	} else {
		// Folder list: discover the in-scope test-case IDs from the specs, then
		// attribute records by VALIDATED BODY IDENTITY through scopedTriageRecords
		// (ENH-210 item 12). The old path shortlisted by filename prefix through
		// triage.ListForTC and could admit tc-foo--bar's record into tc-foo's
		// folder; routing through scopedTriageRecords closes that leak and keeps
		// the fail-closed relevance boundary for prefix-compatible malformed
		// records in scope. A folder is spec-scoped, so orphans never appear and
		// SpecMissing stays false.
		tcs, err := scanTestCases(projectRoot, scope)
		if err != nil {
			return nil, fmt.Errorf("scanning test cases: %w", err)
		}
		// NON-NIL even when empty: a nil slice would make scopedTriageRecords read
		// every record (root behaviour) instead of an empty folder scope.
		ids := make([]string, 0, len(tcs))
		seen := make(map[string]bool)
		for _, tc := range tcs {
			if seen[tc.ID] {
				continue
			}
			seen[tc.ID] = true
			ids = append(ids, tc.ID)
		}
		recs, err = flattenScopedRecords(projectRoot, ids)
		if err != nil {
			return nil, err
		}
	}

	return buildTriageViews(projectRoot, recs, computeSpecMissing, valueFilter, includeResolved)
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
	recs, err := flattenScopedRecords(projectRoot, []string{tcID})
	if err != nil {
		return nil, err
	}
	// Spec existence is not a prerequisite for a bare record lookup; compute the
	// orphan marker so a record whose spec was removed is visibly [spec missing].
	return buildTriageViews(projectRoot, recs, true, valueFilter, includeResolved)
}

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
func buildTriageViews(projectRoot string, recs []*triage.Record, computeSpecMissing bool, valueFilter string, includeResolved bool) ([]TriageView, error) {
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
		v := TriageView{Record: rec, ResultChanged: compareRawResult(rec, overlay)}
		if computeSpecMissing {
			// A record is an orphan when no spec for its test case remains
			// anywhere in the cases tree.
			v.SpecMissing = testcase.FindSource(projectRoot, rec.TestCaseID) == ""
		}
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
