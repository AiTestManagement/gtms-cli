package reader

// ENH-202 triage visibility for the status and gaps read surfaces.
//
// This file is the ONLY new triage-join code for status/gaps, and it exists to
// satisfy ENH-202 item 12: status and gaps must reuse the ENH-201 read
// machinery rather than fork it. Every triage record here is enumerated through
// triage.ListAll (the canonical all-records enumerator) in ONE directory pass
// and read through triage.Read; every result comparison goes through
// compareRawResult against an overlay produced by the single canonical
// scanTerminalResults scanner. There is no second triage-directory scan and no
// second terminal-result selection path.
//
// The source-shape guards in source_shape_test.go pin this: this file must not
// walk .gtms/results/ itself (it takes the overlay from scanTerminalResults) and
// must not open-code a gtms/triage/ scan (it uses triage.ListAll).

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/triage"
)

// identitySeparator mirrors the triage package's filename separator. A record
// is named "{test-case-id}--{framework}.triage.yaml", and a test case ID may
// itself contain the separator, which is why ownership is never inferred from a
// filename prefix alone (see scopedTriageRecords).
const identitySeparator = "--"

// scopedTriageRecords performs ONE gtms/triage/ directory pass and returns the
// records belonging to the requested test cases, keyed by their VALIDATED body
// identity: tcID -> framework -> record.
//
// Identity (ENH-200): a prefix match is not proof of ownership.
// "tc-foo--bar--bats.triage.yaml" starts with "tc-foo--" but belongs to
// tc-foo--bar, not tc-foo. Each candidate filename is therefore matched against
// the LONGEST complete in-scope ID, and the record is accepted only when its own
// body identity agrees. triage.Read already refuses a filename that disagrees
// with its body; this adds the scope check on top of that.
//
// Scope (ENH-201 boundary): when tcIDs is non-nil, only in-scope candidates are
// OPENED at all, so a malformed record belonging to some other folder cannot
// fail a scoped query. Root-scope callers pass nil, which reads and validates
// every record in the project -- including one whose test case no longer exists,
// so a corrupt orphan is reported rather than silently skipped.
func scopedTriageRecords(projectRoot string, tcIDs []string) (map[string]map[string]*triage.Record, error) {
	refs, err := triage.ListAll(projectRoot)
	if err != nil {
		return nil, err
	}

	var inScope map[string]bool
	if tcIDs != nil {
		inScope = make(map[string]bool, len(tcIDs))
		for _, id := range tcIDs {
			inScope[id] = true
		}
	}

	out := make(map[string]map[string]*triage.Record)
	for _, ref := range refs {
		if inScope != nil && longestScopedID(filepath.Base(ref.Path), inScope) == "" {
			// Out of scope: never opened, so it cannot poison this query.
			continue
		}
		rec, err := triage.Read(projectRoot, ref.Path)
		if err != nil {
			return nil, err
		}
		// Key by the body identity, never by the requested ID. A record whose
		// body names a test case outside this scope is dropped rather than
		// attributed to the shorter ID whose prefix it happened to match.
		if inScope != nil && !inScope[rec.TestCaseID] {
			continue
		}
		if out[rec.TestCaseID] == nil {
			out[rec.TestCaseID] = make(map[string]*triage.Record)
		}
		out[rec.TestCaseID][rec.Framework] = rec
	}
	return out, nil
}

// longestScopedID returns the longest in-scope test case ID that base could
// belong to, or "" when none does. Longest wins so that with both tc-foo and
// tc-foo--bar in scope, "tc-foo--bar--bats.triage.yaml" is a candidate for
// tc-foo--bar rather than tc-foo.
//
// The record extension is deliberately NOT referenced here: reconstructing a
// triage filename in reader code is exactly what the source-shape guard forbids,
// and a leading-prefix test does not need it. Ownership is settled by the body
// identity in scopedTriageRecords, not by this filename shortlist.
func longestScopedID(base string, inScope map[string]bool) string {
	best := ""
	for id := range inScope {
		if !strings.HasPrefix(base, id+identitySeparator) {
			continue
		}
		if len(id) > len(best) {
			best = id
		}
	}
	return best
}

// activeTriageByTC enumerates triage records for the given test case IDs and
// returns tcID -> framework -> *triage.Record, filtered to ACTIVE records only.
// It is the shared counting basis for the folder/gaps aggregations: those need
// only to know whether an applicable active classification exists, not the
// result-changed comparison, so it takes no overlay.
//
// Fail-closed like ListTriage: a malformed in-scope record fails the whole call
// with its path, rather than being silently dropped.
func activeTriageByTC(projectRoot string, tcIDs []string) (map[string]map[string]*triage.Record, error) {
	all, err := scopedTriageRecords(projectRoot, tcIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[string]map[string]*triage.Record, len(all))
	for tc, byFramework := range all {
		for fw, rec := range byFramework {
			if rec.Status != triage.StatusActive {
				continue
			}
			if out[tc] == nil {
				out[tc] = make(map[string]*triage.Record)
			}
			out[tc][fw] = rec
		}
	}
	return out, nil
}

// tcIsTriaged reports whether a test case counts once as triaged under a
// framework selection (ENH-202 item 9):
//
//   - defaultFramework == "" : any active classification makes it triaged.
//   - defaultFramework != "" : only an active classification for that exact
//     framework makes it triaged; a classification on another framework does
//     not count.
func tcIsTriaged(byFramework map[string]*triage.Record, defaultFramework string) bool {
	if len(byFramework) == 0 {
		return false
	}
	if defaultFramework == "" {
		return true
	}
	_, ok := byFramework[defaultFramework]
	return ok
}

// FolderTriagedCounts returns the per-folder count of test cases with at least
// one applicable active triage classification, deduped once per test case and
// framework-aware (defaultFramework restricts counting to that framework). It
// backs the root `gtms status --show-triage` TRIAGED column; the CLI calls it
// only under --show-triage so default status output is unchanged. Every folder
// that has test cases appears in the map (with 0 when none are triaged).
func FolderTriagedCounts(projectRoot, defaultFramework string) (map[string]int, error) {
	testCases, err := scanTestCases(projectRoot, nil)
	if err != nil {
		return nil, fmt.Errorf("scanning test cases: %w", err)
	}

	// Root scope: nil means "read and validate every triage record", so a
	// corrupt orphan is reported rather than skipped for having no test case.
	byTC, err := activeTriageByTC(projectRoot, nil)
	if err != nil {
		return nil, err
	}

	tcDir := layout.TestCasesDir(projectRoot)
	counts := make(map[string]int)
	for _, tc := range testCases {
		folder := deriveFolderName(tcDir, tc.SourceFile)
		if _, ok := counts[folder]; !ok {
			counts[folder] = 0
		}
		if tcIsTriaged(byTC[tc.ID], defaultFramework) {
			counts[folder]++
		}
	}
	return counts, nil
}

// AttachTriageToEntries decorates FrameworkEntry.Triage on every per-test-case
// status entry. filter=="" attaches every record (active AND resolved) so
// per-TC JSON exposes lifecycle; filter!="" attaches only active records whose
// value matches, so a filtered view surfaces only matching classifications.
// triageValues supplies the friendly label best-effort (nil is fine -> labels
// omitted). The caller owns whether the attached data is also rendered in
// human output.
// requestedFramework is the EXPLICIT --framework value ("" when none was
// given). It gates only the BUG-193 entry-level manual carrier: a strict
// non-manual selection on a manual-only case must not surface the manual
// classification for rendering, filtering, or JSON (REV-170 CODEX-001).
func AttachTriageToEntries(projectRoot string, entries []PipelineEntry, triageValues map[string]string, filter string, requestedFramework string) error {
	ids := make([]string, 0, len(entries))
	for i := range entries {
		ids = append(ids, entries[i].TestCaseID)
	}
	idx, err := buildFrameworkTriage(projectRoot, ids, triageValues, filter)
	if err != nil {
		return err
	}
	for i := range entries {
		byFramework := idx[entries[i].TestCaseID]
		if byFramework == nil {
			continue
		}
		attachedManual := false
		for j := range entries[i].Frameworks {
			if ft := byFramework[entries[i].Frameworks[j].Framework]; ft != nil {
				entries[i].Frameworks[j].Triage = ft
				if entries[i].Frameworks[j].Framework == "manual" {
					attachedManual = true
				}
			}
		}
		// BUG-193: a manual-only test case has frameworks[] == [] (ENH-146), so a
		// manual triage record cannot nest under a framework entry. Attach it at
		// the test-case level instead. The gate is "the case is NOT wired", not
		// "no manual row received it": on a wired case whose manual artefact was
		// removed after classification, the BUG-127 synthesised manual row is
		// gone but the manual record remains, and attaching it here would let the
		// selected wired framework inherit a manual classification (BUG-193 worktree review, 2026-09-13).
		// Such a record stays framework-isolated and reachable via triage --show.
		if !entries[i].Wired && !attachedManual && manualCarrierAllowed(requestedFramework) {
			if mft := byFramework["manual"]; mft != nil {
				entries[i].Triage = mft
			}
		}
	}
	return nil
}

// AttachTriageToDetail decorates FrameworkEntry.Triage on a single-test-case
// detail entry, using the same rules as AttachTriageToEntries.
func AttachTriageToDetail(projectRoot string, detail *PipelineDetailEntry, triageValues map[string]string, filter string, requestedFramework string) error {
	if detail == nil {
		return nil
	}
	idx, err := buildFrameworkTriage(projectRoot, []string{detail.TestCaseID}, triageValues, filter)
	if err != nil {
		return err
	}
	byFramework := idx[detail.TestCaseID]
	attachedManual := false
	for j := range detail.Frameworks {
		if ft := byFramework[detail.Frameworks[j].Framework]; ft != nil {
			detail.Frameworks[j].Triage = ft
			if detail.Frameworks[j].Framework == "manual" {
				attachedManual = true
			}
		}
	}
	// BUG-193: manual-only detail carries the manual triage at the test-case
	// level (see AttachTriageToEntries for why the gate is !Wired).
	if !detail.Wired && !attachedManual && manualCarrierAllowed(requestedFramework) {
		if mft := byFramework["manual"]; mft != nil {
			detail.Triage = mft
		}
	}
	return nil
}

// manualCarrierAllowed reports whether the BUG-193 entry-level manual triage
// carrier may be attached under the given explicit --framework value: no
// explicit framework, or explicitly "manual". Any other explicit framework is a
// strict selection that must not fall back to the manual classification
// (ENH-082 strict contract; REV-170 CODEX-001).
func manualCarrierAllowed(requestedFramework string) bool {
	return requestedFramework == "" || requestedFramework == "manual"
}

// buildFrameworkTriage enumerates and joins triage records for the given test
// cases into presentation objects keyed by tcID -> framework. It runs ONE bulk
// terminal-result scan (scanTerminalResults) -- the same canonical scanner
// ListTriage uses -- and joins each record with compareRawResult, so the
// result-changed comparison is identical to the ENH-201 read surface.
func buildFrameworkTriage(projectRoot string, tcIDs []string, triageValues map[string]string, filter string) (map[string]map[string]*FrameworkTriage, error) {
	wiringByTC, err := wiringScan(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("scanning wiring records: %w", err)
	}
	overlay := scanTerminalResults(projectRoot, wiringByTC)

	all, err := scopedTriageRecords(projectRoot, tcIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[string]map[string]*FrameworkTriage)
	for tc, byFramework := range all {
		for fw, rec := range byFramework {
			if filter != "" {
				// A value filter surfaces only active records that match it.
				if rec.Status != triage.StatusActive || rec.Value != filter {
					continue
				}
			}
			if out[tc] == nil {
				out[tc] = make(map[string]*FrameworkTriage)
			}
			out[tc][fw] = frameworkTriageFromRecord(rec, overlay, triageValues)
		}
	}
	return out, nil
}

// frameworkTriageFromRecord builds the presentation object for one triage
// record, joining it to the terminal-result overlay for the nullable
// result-changed comparison and the raw latest result (renderer-only).
func frameworkTriageFromRecord(rec *triage.Record, overlay map[string]overlayHit, triageValues map[string]string) *FrameworkTriage {
	ft := &FrameworkTriage{
		Value:        rec.Value,
		Status:       rec.Status,
		Summary:      rec.Summary,
		Defect:       rec.Defect,
		ClassifiedAt: rec.ClassifiedAt,
		ResolvedAt:   rec.ResolvedAt,
		Snapshot: FrameworkTriageSnapshot{
			Task:                 rec.Snapshot.Task,
			Result:               rec.Snapshot.Result,
			CompletedAt:          rec.Snapshot.CompletedAt,
			Artefact:             rec.Snapshot.Artefact,
			ArtefactHash:         rec.Snapshot.ArtefactHash,
			TestcaseHashAtTriage: rec.Snapshot.TestcaseHashAtTriage,
		},
		ResultChangedSinceTriage: compareRawResult(rec, overlay),
	}
	if triageValues != nil {
		ft.Label = triageValues[rec.Value]
	}
	if hit, ok := overlay[overlayKey(rec.TestCaseID, rec.Framework)]; ok {
		ft.LatestResult = hit.RawResult
	}
	return ft
}
