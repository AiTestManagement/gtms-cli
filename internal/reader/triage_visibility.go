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
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
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
	// ENH-205: identity from the one project-wide snapshot. Affected identities are
	// left out of the counts (their diagnostics are surfaced by the folder-summary
	// operation this backs, which shares its snapshot -- see
	// PipelineFolderSummaryWithDiagnostics). This standalone entry is kept for
	// internal callers that want only the counts; it returns *IncompleteReadError
	// rather than partial counts when any identity is affected (REV-184 CODEX-004).
	snap, err := testcase.NewSnapshot(projectRoot)
	if err != nil {
		return nil, err
	}
	testCases, diags, err := resolveScopedSpecs(snap, projectRoot, nil)
	if err != nil {
		return nil, err
	}
	if ierr := incompleteReadErr(diags); ierr != nil {
		return nil, ierr
	}
	return folderTriagedCountsFromSpecs(projectRoot, testCases, defaultFramework)
}

// folderTriagedCountsFromSpecs computes per-folder triaged counts from
// already-resolved, proven specs, performing no identity resolution of its own.
func folderTriagedCountsFromSpecs(projectRoot string, testCases []testCaseFrontmatter, defaultFramework string) (map[string]int, error) {
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
	overlay, err := overlayForTriageRead(projectRoot)
	if err != nil {
		return err
	}
	return attachTriageToEntries(projectRoot, entries, overlay, triageValues, filter, requestedFramework)
}

// AttachTriageToEntriesFromScan is AttachTriageToEntries for a command that already
// scanned .gtms/results: diags comes from that scan, so the command scans once and its
// refusals are the only ones to report (section 8, CODEX-006).
func AttachTriageToEntriesFromScan(projectRoot string, entries []PipelineEntry, diags IdentityDiagnostics, triageValues map[string]string, filter string, requestedFramework string) error {
	return attachTriageToEntries(projectRoot, entries, diags.resultsOverlay, triageValues, filter, requestedFramework)
}

func attachTriageToEntries(projectRoot string, entries []PipelineEntry, overlay map[string]overlayHit, triageValues map[string]string, filter string, requestedFramework string) error {
	ids := make([]string, 0, len(entries))
	for i := range entries {
		ids = append(ids, entries[i].TestCaseID)
	}
	idx, err := buildFrameworkTriage(projectRoot, ids, overlay, triageValues, filter)
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
			fw := entries[i].Frameworks[j].Framework
			if ft := byFramework[fw]; ft != nil {
				// BUG-206: on a manual-ONLY (unwired) case the synthesized
				// `manual` row must NOT carry triage; it stays at the test-case
				// top level (Owner Decision 2), and leaving attachedManual false
				// lets the top-level fallback below fire. On a wired case the
				// manual row nests triage (BUG-127 / BUG-193, unchanged).
				if fw == "manual" && !entries[i].Wired {
					continue
				}
				entries[i].Frameworks[j].Triage = ft
				if fw == "manual" {
					attachedManual = true
				}
			}
		}
		// BUG-193 / BUG-206: a manual-only test case keeps its manual triage at
		// the test-case level. Since BUG-206 it has a synthesized manual row, but
		// that row deliberately does not carry the triage (BUG-206 Owner
		// Decision 2), so attach it at the test-case level instead. The gate is
		// "the case is NOT wired", not "no manual row received it": on a wired case whose manual artefact was
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
	overlay, err := overlayForTriageRead(projectRoot)
	if err != nil {
		return err
	}
	return attachTriageToDetail(projectRoot, detail, overlay, triageValues, filter, requestedFramework)
}

// AttachTriageToDetailFromScan is AttachTriageToDetail for a command that already
// scanned .gtms/results. See AttachTriageToEntriesFromScan.
func AttachTriageToDetailFromScan(projectRoot string, detail *PipelineDetailEntry, diags IdentityDiagnostics, triageValues map[string]string, filter string, requestedFramework string) error {
	return attachTriageToDetail(projectRoot, detail, diags.resultsOverlay, triageValues, filter, requestedFramework)
}

func attachTriageToDetail(projectRoot string, detail *PipelineDetailEntry, overlay map[string]overlayHit, triageValues map[string]string, filter string, requestedFramework string) error {
	if detail == nil {
		return nil
	}
	idx, err := buildFrameworkTriage(projectRoot, []string{detail.TestCaseID}, overlay, triageValues, filter)
	if err != nil {
		return err
	}
	byFramework := idx[detail.TestCaseID]
	attachedManual := false
	for j := range detail.Frameworks {
		fw := detail.Frameworks[j].Framework
		if ft := byFramework[fw]; ft != nil {
			// BUG-206: manual-ONLY (unwired) detail keeps its manual triage at
			// the test-case top level (Owner Decision 2); see
			// AttachTriageToEntries.
			if fw == "manual" && !detail.Wired {
				continue
			}
			detail.Frameworks[j].Triage = ft
			if fw == "manual" {
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

// overlayForTriageRead scans for a caller with no scan of its own. A refusal comes back
// as an *IncompleteReadError, because such a caller has no other way to report it.
func overlayForTriageRead(projectRoot string) (map[string]overlayHit, error) {
	wiringByTC, err := wiringScan(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("scanning wiring records: %w", err)
	}
	overlay, refusals := scanTerminalResults(projectRoot, wiringByTC)
	var diags IdentityDiagnostics
	diags.addResultsRefusals(refusals)
	if ierr := incompleteReadErr(diags); ierr != nil {
		return nil, ierr
	}
	return overlay, nil
}

// buildFrameworkTriage enumerates and joins triage records for the given test
// cases into presentation objects keyed by tcID -> framework. It joins each record
// with compareRawResult against the given overlay, so the result-changed comparison is
// identical to the ENH-201 read surface, and it does not scan .gtms/results itself.
func buildFrameworkTriage(projectRoot string, tcIDs []string, overlay map[string]overlayHit, triageValues map[string]string, filter string) (map[string]map[string]*FrameworkTriage, error) {
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
