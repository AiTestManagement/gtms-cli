package reader

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/aitestmanagement/gtms-cli/internal/execution"
	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// CON-023 / ENH-145 / ENH-146:
//
// The reader's canonical automation-identity source is now wiring
// (gtms/automation/wiring/*.wiring.yaml) overlaid with the latest
// terminal handoffs (.gtms/results/*.handoff.yaml, status: complete or
// status: error only). Manual-only TCs (no wiring) surface via primed
// manual result templates (gtms/manual/records/*--manual.result.yaml).
//
// This file owns the new scan + overlay + build helpers. status.go,
// gaps.go, map.go all route through these.

// wiringScan returns wiring records keyed by test case ID. Delegates to
// the package-level wiringScanFn seam (defaultWiringScan in production).
func wiringScan(projectRoot string) (map[string][]*wiring.WiringRecord, error) {
	return wiringScanFn(projectRoot)
}

// defaultWiringScan is the production implementation backing wiringScan.
// Scans gtms/automation/wiring/ on disk.
func defaultWiringScan(projectRoot string) (map[string][]*wiring.WiringRecord, error) {
	return wiring.Scan(projectRoot)
}

// overlayHit captures the joined fields from the latest terminal result
// contract for one (testcase, framework) pair.
type overlayHit struct {
	Status      string // "complete" | "error"
	Result      string // "pass" | "fail" | "skipped" | "error" | ""
	ExecutedAt  string
	ExecutedBy  string
	Environment string
	Notes       string
	NotesSpill  string
	Summary     string
	Artefact    string
	Attempts    int
	GitCommit   string
	GitBranch   string
	GitDirty    *bool
	Adapter     string // ENH-191: the adapter that produced this result

	// ENH-200 additive fields for triage snapshot capture. These are new and
	// must not change the meaning of the existing fields above (overlayHit is
	// shared with the status/gaps/map overlays). Triage reads these; the
	// status/gaps consumers ignore them.
	Task         string // handoff task ID (rc.Task)
	ArtefactHash string // handoff artefact-hash, verbatim (may be empty)
	RawResult    string // rc.Result verbatim: pass|fail|skip|error (NOT reader-normalised)
	RawCompleted string // rc.Completed verbatim (empty when the handoff omitted it)
}

// overlayKey is the canonical map key for the join.
func overlayKey(testCase, framework string) string {
	return testCase + "\x00" + framework
}

// scanTerminalResultsForTC loads every wiring record for one TC and runs
// the overlay scan with that scope. It exists because some single-TC
// reader paths (triage, single-TC status detail) need to consult the
// terminal overlay without scanning the whole project.
//
// Loading all wiring for the TC (rather than only the selected/known
// record) is necessary so the ENH-146 join ladder's "exactly one
// matching wiring" rule sees the TC's full sibling set. A narrower
// wiring set would let rung 4 (unambiguous adapter mapping) wrongly
// accept a frameworkless result against the singleton when two
// currently-existing wiring records for the TC share the same adapter.
func scanTerminalResultsForTC(projectRoot, testCaseID string) (map[string]overlayHit, error) {
	recs, err := wiring.FindAllForTC(projectRoot, testCaseID)
	if err != nil {
		return nil, err
	}
	// Delegate to the single canonical scanner so the wiring join AND the
	// BUG-193 proven-manual claim are applied exactly once, in one place. A
	// manual-only TC (recs empty) still reaches the scanner so its wiring-free
	// manual claim is merged; the scanner keys everything by (tc, framework), so
	// the extra project-wide manual keys it returns are simply not looked up here.
	wiringByTC := map[string][]*wiring.WiringRecord{}
	if len(recs) > 0 {
		wiringByTC[testCaseID] = recs
	}
	return scanTerminalResults(projectRoot, wiringByTC), nil
}

// scanTerminalResults walks .gtms/results/ and returns the latest
// terminal EXECUTE handoff per (testcase, framework). Non-terminal
// handoffs (status: pending, in-progress) are excluded, and so are
// non-execute commands: gtms automate/create/prime also write terminal
// (complete/error) handoffs, but only `command: execute` results may
// overlay the execute columns (BUG-124). CON-023 / ENH-146
// terminal-handoff discipline.
//
// Each result file is joined to a currently existing wiring record
// using the ENH-146 join precedence ladder (Pin the join precedence):
//
//  1. Explicit `framework` on the result: keep iff (target + framework)
//     resolves to a currently existing wiring record. Otherwise excluded
//     as orphan (Edge Case 5).
//  2. Else `target + artefact-hash` matches exactly one currently
//     existing wiring record → use that framework.
//  3. Else `target + artefact` path matches exactly one currently
//     existing wiring record → use that framework.
//  4. Else unambiguous adapter-to-framework mapping (the TC has exactly
//     one wiring record whose `adapter:` equals the result's adapter) →
//     use that framework.
//  5. Else exclude the result from the overlay.
//
// The reader never guesses: a result overlays a wiring record only when
// the join resolves to exactly one currently existing wiring record. A
// stale or missing-artefact wiring record still counts as "currently
// existing" for join purposes — currency-tier classification is the
// picker's concern, not the overlay's.
//
// "Latest" uses Completed (RFC3339) when set, falling back to Created.
// On a Completed-stamp tie (two terminal handoffs for the same
// (target, framework) written within the same RFC3339 second — common on
// fast CI runners where a re-execute follows the original within one
// wall-clock second), the file's mtime breaks the tie. Filesystem mtime
// is sub-second on every supported platform (ext4 ns, NTFS 100 ns), so
// the genuinely-later handoff wins deterministically. Without this
// tiebreaker, `os.ReadDir` iteration order picks the winner, which on
// ext4 is non-deterministic and causes a passing re-execute to render
// as Error on the dashboard. Bad files are skipped silently; the reader
// must not blind itself to the rest because one handoff is malformed.
func scanTerminalResults(projectRoot string, wiringByTC map[string][]*wiring.WiringRecord) map[string]overlayHit {
	// The wiring join below is skipped when the project has no wiring (every
	// result would be an overlay orphan). BUG-193: the proven-manual claim is
	// wiring-free, so it is merged AFTER this block regardless of wiring -- the
	// old fast-path `return nil` here would have hidden a manual-only result.
	out := scanWiringTerminalResults(projectRoot, wiringByTC)

	// BUG-193: merge the proven manual claim for every TC with a terminal manual
	// execute handoff whose artefact proves out. Add-if-absent so a genuine
	// manual WIRING record's join (rung 1 above) wins over the synthesized claim.
	out = mergeManualClaims(out, buildManualClaims(projectRoot, ""))
	if len(out) == 0 {
		return nil
	}
	return out
}

// scanWiringTerminalResults is the ENH-146 wiring join: it walks .gtms/results/
// and returns the latest terminal EXECUTE handoff per (testcase, framework) that
// joins to a currently existing wiring record. It is the wiring-only half of
// scanTerminalResults; BUG-193's wiring-free manual claim is layered on top by
// the caller. Returns an empty map when the project has no wiring.
func scanWiringTerminalResults(projectRoot string, wiringByTC map[string][]*wiring.WiringRecord) map[string]overlayHit {
	if len(wiringByTC) == 0 {
		return nil
	}
	dir := filepath.Join(projectRoot, ".gtms", "results")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	type stamped struct {
		stamp string
		mtime int64 // UnixNano; tiebreaks equal Completed stamps.
		hit   overlayHit
	}
	by := make(map[string]stamped)

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".handoff.yaml") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		rc, rErr := result.Read(path)
		if rErr != nil || rc == nil {
			continue
		}
		// BUG-130 structural guard: only terminal execute contracts may
		// overlay the execute columns. Non-execute commands (automate/
		// create/prime) also write terminal handoffs via the shared
		// invoker path; without this gate an automate-success handoff
		// renders as a false-green EXECUTE pass (BUG-124). Manual
		// results route through `gtms execute --adapter manual-execute`
		// (command: execute), so they are preserved.
		if !result.IsTerminalExecuteContract(rc) {
			continue
		}
		framework := resolveOverlayFramework(rc, wiringByTC[rc.Target])
		if framework == "" {
			// No unique currently existing wiring record — result is an
			// orphan in the overlay sense. Excluded per ENH-146 §"Pin
			// the join precedence".
			continue
		}
		stamp := rc.Completed
		if stamp == "" {
			stamp = rc.Created
		}
		var mtime int64
		if fi, statErr := os.Stat(path); statErr == nil {
			mtime = fi.ModTime().UnixNano()
		}
		key := overlayKey(rc.Target, framework)
		readerResult := rc.Result
		if readerResult == "skip" {
			readerResult = "skipped"
		}
		hit := overlayHit{
			Status:      rc.Status,
			Result:      readerResult,
			ExecutedAt:  stamp,
			ExecutedBy:  rc.ExecutedBy,
			Environment: rc.Environment,
			Notes:       rc.Log,
			NotesSpill:  rc.NotesSpill,
			Summary:     rc.Summary,
			Artefact:    rc.Artefact,
			Attempts:    rc.Attempts,
			GitCommit:   rc.GitCommit,
			GitBranch:   rc.GitBranch,
			GitDirty:    rc.GitDirty,
			Adapter:     rc.Adapter, // ENH-191: carry the actual runner
			// ENH-200: raw provenance for triage snapshot capture. RawResult
			// keeps the contract vocabulary (skip, not skipped); RawCompleted
			// is the handoff's `completed` verbatim so triage can omit
			// snapshot.completed_at when the handoff never supplied one.
			Task:         rc.Task,
			ArtefactHash: rc.ArtefactHash,
			RawResult:    rc.Result,
			RawCompleted: rc.Completed,
		}
		prev, ok := by[key]
		if !ok || stamp > prev.stamp || (stamp == prev.stamp && mtime > prev.mtime) {
			by[key] = stamped{stamp: stamp, mtime: mtime, hit: hit}
		}
	}

	if len(by) == 0 {
		return nil
	}
	out := make(map[string]overlayHit, len(by))
	for k, v := range by {
		out[k] = v.hit
	}
	return out
}

// resolveOverlayFramework runs the ENH-146 join precedence ladder against
// the wiring records currently on disk for one TC. Returns the framework
// name to overlay this result against, or "" to exclude it.
//
// tcWiring is the set of currently existing wiring records for the
// result's TC (nil/empty → the TC has no wiring; the result is an orphan
// regardless of which ladder rung the caller would have tried).
func resolveOverlayFramework(rc *result.ResultContract, tcWiring []*wiring.WiringRecord) string {
	if rc == nil || len(tcWiring) == 0 {
		return ""
	}

	// Rung 1: explicit framework, verified against currently existing wiring.
	if rc.Framework != "" {
		for _, w := range tcWiring {
			if w.Framework == rc.Framework {
				return rc.Framework
			}
		}
		// Explicit framework, but no matching wiring → orphan. Don't fall
		// through to subsequent rungs (the user/adapter has told us which
		// framework this result belongs to and the wiring is gone).
		return ""
	}

	// Rung 2: unique target+artefact-hash match.
	// ENH-151: pending hashes must never be used as join keys. A result
	// carrying "pending" (defensive — should not occur) or a wiring record
	// with "pending" artefact-hash must not match on this rung.
	if rc.ArtefactHash != "" && !wiring.IsPendingArtefactHash(rc.ArtefactHash) {
		if fw, ok := uniqueWiringFramework(tcWiring, func(w *wiring.WiringRecord) bool {
			return !wiring.IsPendingArtefactHash(w.ArtefactHash) && w.ArtefactHash == rc.ArtefactHash
		}); ok {
			return fw
		}
	}

	// Rung 3: unique target+artefact path match.
	if rc.Artefact != "" {
		if fw, ok := uniqueWiringFramework(tcWiring, func(w *wiring.WiringRecord) bool {
			return w.Artefact == rc.Artefact
		}); ok {
			return fw
		}
	}

	// Rung 4: unambiguous adapter mapping for this TC.
	if rc.Adapter != "" {
		if fw, ok := uniqueWiringFramework(tcWiring, func(w *wiring.WiringRecord) bool {
			return w.Adapter == rc.Adapter
		}); ok {
			return fw
		}
	}

	// Rung 5: exclude.
	return ""
}

// uniqueWiringFramework returns the framework of the single wiring record
// in tcWiring that satisfies `match`. Returns ("", false) when zero or
// multiple records match — the caller falls through to the next rung.
func uniqueWiringFramework(tcWiring []*wiring.WiringRecord, match func(*wiring.WiringRecord) bool) (string, bool) {
	var found *wiring.WiringRecord
	for _, w := range tcWiring {
		if !match(w) {
			continue
		}
		if found != nil {
			// Ambiguous — multiple wiring records match. The ladder rule
			// is "exactly one"; ambiguity falls through to the next rung.
			return "", false
		}
		found = w
	}
	if found == nil {
		return "", false
	}
	return found.Framework, true
}

// BUG-193: proven-manual claim.
//
// A manual-only test case is wiring-free by design (CON-023), so the ENH-146
// wiring join above can never surface its execution result. The manual claim
// closes that gap WITHOUT inventing wiring: it admits the manual framework's
// latest terminal execute handoff into the overlay only when that handoff's
// artefact can be PROVEN to be the canonical in-project manual result file for
// the same (test case, framework: manual) pair. The durable execution file is
// never consulted -- it corroborates but does not authorise (BUG-193 Evidence
// authority). This lives in wiring_scan.go, the one file allowed to walk
// .gtms/results/, so triage inherits exactly one result-selection mechanism.

// mergeManualClaims folds proven manual claims into an existing overlay with
// ADD-IF-ABSENT semantics: a genuine manual WIRING record's join (resolveOverlay
// Framework rung 1) already keyed overlayKey(tc,"manual") and must win, so the
// synthesized claim only fills a key the wiring join left empty. Never weakens
// the orphan rule for any other framework -- a non-manual handoff is never a
// manual claim.
func mergeManualClaims(out, manual map[string]overlayHit) map[string]overlayHit {
	if len(manual) == 0 {
		return out
	}
	if out == nil {
		out = make(map[string]overlayHit, len(manual))
	}
	for k, v := range manual {
		if _, exists := out[k]; !exists {
			out[k] = v
		}
	}
	return out
}

// buildManualClaims walks .gtms/results/ for terminal manual execute handoffs and
// returns the latest proven manual claim per test case, keyed overlayKey(tc,
// "manual"). When tcFilter is non-empty only that test case is considered.
//
// A handoff yields a claim only when it names framework: manual, is a terminal
// execute contract, and its artefact proves out via manualArtefactProven. The
// claim copies the handoff's Task, RawResult, RawCompleted, Artefact and
// ArtefactHash verbatim so triage snapshots the selected execution, not the
// editable manual template. "Latest" uses Completed (RFC3339) falling back to
// Created, with filesystem mtime breaking an equal-second tie -- identical to the
// wiring join's rule.
func buildManualClaims(projectRoot, tcFilter string) map[string]overlayHit {
	dir := filepath.Join(projectRoot, ".gtms", "results")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	type stamped struct {
		stamp string
		mtime int64
		hit   overlayHit
	}
	by := make(map[string]stamped)

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".handoff.yaml") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		rc, rErr := result.Read(path)
		if rErr != nil || rc == nil {
			continue
		}
		// framework: manual is the authority (BUG-193): the adapter NAME
		// manual-execute is never treated as a framework, so the handoff's
		// framework field -- corrected from the manual template to "manual" -- is
		// what admits the claim.
		if rc.Framework != "manual" {
			continue
		}
		if !result.IsTerminalExecuteContract(rc) {
			continue
		}
		if tcFilter != "" && rc.Target != tcFilter {
			continue
		}
		if !manualClaimAdmissible(e.Name(), rc) {
			continue
		}
		if !manualArtefactProven(projectRoot, rc) {
			continue
		}
		stamp := rc.Completed
		if stamp == "" {
			stamp = rc.Created
		}
		var mtime int64
		if fi, statErr := os.Stat(path); statErr == nil {
			mtime = fi.ModTime().UnixNano()
		}
		readerResult := rc.Result
		if readerResult == "skip" {
			readerResult = "skipped"
		}
		hit := overlayHit{
			Status:       rc.Status,
			Result:       readerResult,
			ExecutedAt:   stamp,
			Adapter:      rc.Adapter,
			Artefact:     rc.Artefact,
			Task:         rc.Task,
			ArtefactHash: rc.ArtefactHash,
			RawResult:    rc.Result,
			RawCompleted: rc.Completed,
		}
		key := overlayKey(rc.Target, "manual")
		prev, ok := by[key]
		if !ok || stamp > prev.stamp || (stamp == prev.stamp && mtime > prev.mtime) {
			by[key] = stamped{stamp: stamp, mtime: mtime, hit: hit}
		}
	}

	if len(by) == 0 {
		return nil
	}
	out := make(map[string]overlayHit, len(by))
	for k, v := range by {
		out[k] = v.hit
	}
	return out
}

// manualArtefactProven reports whether rc's artefact is the canonical in-project
// manual result file for rc's test case: the exact pair path
// <parent>/manual/records/<tc>--manual.result.yaml, present as a regular file
// (not a directory and not a symlink), parseable as YAML, and whose body
// identifies the same test case and framework: manual. Any other artefact --
// out-of-project, a different test case, a directory, malformed, a non-manual
// body, a byte-identical copy at a non-canonical path, or a symlink -- fails the
// proof (BUG-193 verification item 8 / tc-c78c0008).
func manualArtefactProven(projectRoot string, rc *result.ResultContract) bool {
	if rc == nil || rc.Target == "" {
		return false
	}
	// REV-170 CLAUDE-003: the target comes from handoff YAML. Refuse separators,
	// dot segments and control characters BEFORE it is joined into a path, so a
	// crafted target cannot steer the proof outside the records directory.
	if err := pathsafe.ValidateFilenameComponent(rc.Target, "handoff target"); err != nil {
		return false
	}
	recordsDir := filepath.ToSlash(filepath.Join(layout.Current().Manual, "records")) + "/"
	canonicalRel := filepath.ToSlash(filepath.Join(layout.Current().Manual, "records", rc.Target+"--manual.result.yaml"))
	if !strings.HasPrefix(canonicalRel, recordsDir) || filepath.ToSlash(rc.Artefact) != canonicalRel {
		return false
	}
	// Lstat the LEXICAL path (not Stat) so a symlink at the canonical leaf is
	// seen as a link and refused; IsRegular() also refuses a directory and a
	// missing file.
	lexical := filepath.Join(projectRoot, filepath.FromSlash(canonicalRel))
	fi, statErr := os.Lstat(lexical)
	if statErr != nil || !fi.Mode().IsRegular() {
		return false
	}
	// REV-170 CODEX-003: an ancestor symlink or junction (e.g. manual/records
	// pointing outside the project) passes the leaf Lstat, so resolve the
	// candidate through the shared containment boundary and read only the
	// canonical in-project target it yields.
	abs, _, resolveErr := pathsafe.ResolveUnderRoot(projectRoot, canonicalRel)
	if resolveErr != nil {
		return false
	}
	mr, parseErr := parseManualResultFile(abs)
	if parseErr != nil || mr == nil {
		return false
	}
	return mr.TestCaseID == rc.Target && mr.Framework == "manual"
}

// manualClaimAdmissible is the REV-170 CODEX-002 provenance gate for a manual
// handoff, applied ONLY on the manual-claim path so wired-framework authority
// is untouched. A claim is admitted only when every value that will be copied
// into a triage snapshot or printed by the classification is classifiable and
// format-safe: a non-empty result in the contract vocabulary, a non-empty
// filename-safe task that agrees with the handoff filename, a completed stamp
// that is empty or RFC3339, and an artefact hash that is empty or free of
// separators and control characters.
func manualClaimAdmissible(fileName string, rc *result.ResultContract) bool {
	if rc == nil {
		return false
	}
	if rc.Result == "" || result.Validate(rc) != nil {
		return false
	}
	if pathsafe.ValidateFilenameComponent(rc.Task, "handoff task") != nil {
		return false
	}
	if fileName != rc.Task+".handoff.yaml" {
		return false
	}
	if rc.Completed != "" {
		if _, err := time.Parse(time.RFC3339, rc.Completed); err != nil {
			return false
		}
	}
	if rc.ArtefactHash != "" && pathsafe.ValidateFilenameComponent(rc.ArtefactHash, "artefact hash") != nil {
		return false
	}
	return true
}

// manualClaimForTC returns the proven manual claim for one test case, or
// (zero, false). Used by triage framework inference (resolveTriageFramework) so
// proven manual evidence is a first-class candidate alongside wiring frameworks.
func manualClaimForTC(projectRoot, testCaseID string) (overlayHit, bool) {
	claims := buildManualClaims(projectRoot, testCaseID)
	hit, ok := claims[overlayKey(testCaseID, "manual")]
	return hit, ok
}

// manualRecord captures the parsed contents of one
// gtms/manual/records/*--manual.result.yaml file plus the drift fields
// surfaced under ENH-117.
type manualRecord struct {
	TestCaseID            string
	TestCaseHash          string
	Framework             string
	Result                string // "pass" | "fail" | "skipped" | "" (primed but not run)
	DriftDetected         bool
	DriftDetectedAt       string
	TestCaseHashAtExecute string
	ArtefactPath          string // project-relative path to the manual result file
}

// scanManualByTC returns parsed manual result records keyed by TC ID.
// Manual TCs may not exist; missing dir returns nil. Files that fail
// to parse are silently skipped.
func scanManualByTC(projectRoot string) map[string]*manualRecord {
	dir := filepath.Join(projectRoot, layout.Current().Manual, "records")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	out := make(map[string]*manualRecord)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "--manual.result.yaml") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		mr, err := parseManualResultFile(path)
		if err != nil {
			continue
		}
		if mr.TestCaseID == "" {
			continue
		}
		out[mr.TestCaseID] = &manualRecord{
			TestCaseID:            mr.TestCaseID,
			TestCaseHash:          mr.TestCaseHash,
			Framework:             "manual",
			Result:                normaliseManualResult(mr.Result),
			DriftDetected:         mr.DriftDetected,
			DriftDetectedAt:       mr.DriftDetectedAt,
			TestCaseHashAtExecute: mr.TestCaseHashAtExecute,
			ArtefactPath:          filepath.ToSlash(filepath.Join(layout.Current().Manual, "records", e.Name())),
		}
	}
	return out
}

// manualExecutionLastRunAt joins a manual pipeline entry to the canonical
// committed execution timestamp needed by BUG-044's recordSupersedes
// reconciliation (BUG-162 Symptom B). The manual result FILE deliberately
// carries no timestamp (CON-020 4-field contract), so the run-at is recovered
// from gtms/execution/*.results.yaml -- which, unlike the .gtms/results/
// handoffs, is COMMITTED and therefore survives the fresh-checkout scenario
// that makes Symptom B routine.
//
// It returns the newest trustworthy `completed_at` for the TC, canonicalized to
// whole-second UTC "Z" (see the return statement for why), or "" when no
// trustworthy candidate exists (leaving LastRunAt empty so the existing
// conservative error-task-wins path stays in force).
//
// Contract (BUG-162 PRP decisions log B1-B6):
//   - Candidate identity (B1): a results file is a candidate only if it holds a
//     row whose tc_id == tcID, its framework (file-level or that row's) is
//     "manual", and that row's outcome (normalised skip->skipped) equals
//     normalisedOutcome (the current manual result file's outcome).
//   - Validation (B2): the candidate must also have a completed_at that parses
//     as RFC3339. A file that fails to parse or fails any check is skipped
//     individually; the scan CONTINUES to older candidates.
//   - Ordering (B3/B4): candidates are ranked by PARSED completed_at, newest
//     wins. Never filesystem mtime.
//   - Tie-break (B5): equal completed_at -> the greatest-lexical FILENAME wins
//     (embeds the task id; machine-independent, unlike mtime).
func manualExecutionLastRunAt(projectRoot, tcID, normalisedOutcome string) string {
	if tcID == "" || normalisedOutcome == "" {
		return ""
	}
	dir := layout.ExecutionDir(projectRoot)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}

	var bestParsed time.Time
	var bestName string // filename of the winning candidate (tie-break)
	found := false

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".results.yaml") {
			continue
		}
		rf, rErr := execution.Read(filepath.Join(dir, e.Name()))
		if rErr != nil || rf == nil {
			continue // B2: skip malformed, keep searching.
		}
		// Locate the row for this TC with a matching outcome and manual framework.
		matched := false
		for _, row := range rf.Results {
			if row.TCID != tcID {
				continue
			}
			// Framework may be declared file-level or per-row; accept either.
			fw := row.Framework
			if fw == "" {
				fw = rf.Framework
			}
			if fw != "manual" {
				continue
			}
			if normaliseManualResult(row.Outcome) != normalisedOutcome {
				continue
			}
			matched = true
			break
		}
		if !matched {
			continue
		}
		// B2: completed_at must be present and RFC3339-parseable.
		parsed, pErr := time.Parse(time.RFC3339, rf.CompletedAt)
		if pErr != nil {
			continue
		}
		// B3/B4 newest-wins; B5 filename tie-break on equal timestamps.
		if !found ||
			parsed.After(bestParsed) ||
			(parsed.Equal(bestParsed) && e.Name() > bestName) {
			bestParsed = parsed
			bestName = e.Name()
			found = true
		}
	}

	if !found {
		return ""
	}
	// Canonicalize to whole-second UTC "Z" so the lexical comparison in
	// applyTaskStatus' recordSupersedes (entry.LastRunAt >= task.Created) is
	// chronologically correct. A raw completed_at with a non-Z offset (e.g.
	// "...T13:00:00+02:00") or fractional seconds would sort lexically wrong
	// against the whole-second-Z task timestamps. bestParsed already holds the
	// parsed instant; .UTC().Format(RFC3339) normalises the offset to Z and
	// drops sub-second precision, matching task-creation's timestamp form.
	return bestParsed.UTC().Format(time.RFC3339)
}

type manualResultFM struct {
	TestCaseID            string `yaml:"test_case_id"`
	TestCaseHash          string `yaml:"test_case_hash"`
	Framework             string `yaml:"framework"`
	Result                string `yaml:"result"`
	DriftDetected         bool   `yaml:"drift-detected"`
	DriftDetectedAt       string `yaml:"drift-detected-at"`
	TestCaseHashAtExecute string `yaml:"test_case_hash_at_execute"`
}

func parseManualResultFile(path string) (*manualResultFM, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var fm manualResultFM
	if err := yaml.Unmarshal(data, &fm); err != nil {
		return nil, err
	}
	return &fm, nil
}

// normaliseManualResult maps the manual result file's `result:` to the
// reader vocabulary (legacy: "skipped" rather than "skip", and "" stays
// "" so deriveManualCoverage classifies as "prepared").
func normaliseManualResult(r string) string {
	switch r {
	case "skip":
		return "skipped"
	default:
		return r
	}
}

// buildPipelineEntry assembles the ENH-146 pinned PipelineEntry shape
// for one TC. It populates both the new JSON-visible fields
// (Wired, ManualReady, SelectedFramework, Frameworks[]) and the
// legacy Go-internal carriers (AutomateStatus, ExecuteStatus, etc.)
// that the CLI table formatter still consumes.
func buildPipelineEntry(
	projectRoot string,
	tc testCaseFrontmatter,
	wiringRecs []*wiring.WiringRecord,
	overlay map[string]overlayHit,
	manual *manualRecord,
	defaultFramework string,
	strictFramework bool,
) PipelineEntry {
	entry := PipelineEntry{
		TestCaseID:     tc.ID,
		Slug:           deriveSlug(tc),
		Title:          tc.Title,
		CreateStatus:   deriveCreateStatus(tc),
		AutomateStatus: "none",
		ExecuteStatus:  "none",
		LastResult:     "none",
	}

	// Classify every wiring record so the picker can rank them.
	classifications := make([]Classification, len(wiringRecs))
	for i, w := range wiringRecs {
		classifications[i] = ClassifyWiring(projectRoot, w)
	}

	// Render per-framework entries (in lexical order for deterministic JSON).
	frameworks := make([]FrameworkEntry, 0, len(wiringRecs))
	for i, w := range wiringRecs {
		c := classifications[i]
		// BUG-102: derive bootstrap state from artefact-hash sentinel.
		bootstrap := "ready"
		if wiring.IsPendingArtefactHash(w.ArtefactHash) {
			bootstrap = "pending"
		}
		fe := FrameworkEntry{
			Framework:       w.Framework,
			Wired:           true,
			WiringDrift:     driftLabel(c),
			ArtefactPresent: c.ArtefactPresent,
			Artefact:        w.Artefact,
			WiringBootstrap: bootstrap,
			WiredAdapter:    w.Adapter, // ENH-191: wiring-derived provenance
		}
		if hit, ok := overlay[overlayKey(w.TestCase, w.Framework)]; ok {
			fe.LastExecutedHere = hit.ExecutedAt
			fe.LastStatusHere = hit.Status
			if hit.Status == "complete" {
				fe.LastResultHere = hit.Result
			}
			fe.GitCommit = hit.GitCommit
			fe.GitBranch = hit.GitBranch
			fe.GitDirty = hit.GitDirty
			fe.Summary = hit.Summary
			fe.LogExcerpt = hit.Notes
			fe.ExecutedBy = hit.ExecutedBy
			fe.Environment = hit.Environment
			// ENH-191: last-run adapter from the overlay (only when a
			// terminal result is joined).
			if hit.Adapter != "" {
				fe.LastRunAdapter = hit.Adapter
			}
		}
		frameworks = append(frameworks, fe)
	}
	// Sort frameworks lexically for deterministic JSON output.
	sortFrameworksByName(frameworks)
	entry.Frameworks = frameworks
	entry.Wired = len(wiringRecs) > 0

	// BUG-127: a recorded manual/prime result is first-class regardless of
	// wiring. ManualReady is a per-TC signal keyed on the presence of a manual
	// result file, NOT on the TC being un-wired -- a primed+executed result on a
	// case that later graduates to automate must still surface.
	entry.ManualReady = manual != nil

	// BUG-127: on a WIRED case, model the manual result as a first-class
	// FrameworkEntry so it is visible in --json and selectable via
	// --framework manual (frameworks[] is otherwise built only from wiring
	// records). Gated on entry.Wired: a manual-only TC already surfaces via
	// ManualReady + the legacy carriers below and keeps an empty frameworks[]
	// per the ENH-146 contract (map_phase3d), so we must NOT add one there.
	// DUP GUARD: a manual WIRING record (CON-023 Edge Case 1) already produced a
	// Framework=="manual" entry via the wiringRecs loop above; do not add a second.
	if entry.Wired && manual != nil {
		hasManual := false
		for _, fe := range entry.Frameworks {
			if fe.Framework == "manual" {
				hasManual = true
				break
			}
		}
		if !hasManual {
			me := FrameworkEntry{
				Framework:       "manual",
				Wired:           false, // synthesized result-file entry, not a wiring record
				Artefact:        manual.ArtefactPath,
				ArtefactPresent: true, // scanManualByTC parsed the file, so it exists
			}
			if manual.Result != "" {
				me.LastResultHere = manual.Result
				me.LastStatusHere = "complete"
				if manual.Result == "skipped" {
					me.LastStatusHere = "skipped"
				}
			}
			entry.Frameworks = append(entry.Frameworks, me)
			sortFrameworksByName(entry.Frameworks)
		}
	}

	// Manual-only TC (no wiring) with a primed/recorded manual result: populate
	// the legacy AUTOMATE / LAST RESULT / EXECUTE carriers so the CLI table
	// formatter and downstream consumers don't see "none". Mirrors what the
	// pre-cutover deriveAutomateStatus / deriveExecuteResult returned for
	// framework=manual records. (ManualReady is set above, regardless of wiring.)
	if !entry.Wired && manual != nil {
		entry.AutomateStatus = "manual"
		entry.Framework = "manual"
		if manual.Result != "" {
			entry.LastResult = manual.Result
			if manual.Result == "skipped" {
				entry.ExecuteStatus = "skipped"
			} else {
				entry.ExecuteStatus = "complete"
			}
			// BUG-162 Symptom B: recover the committed execution timestamp so
			// applyTaskStatus' recordSupersedes can fire on the manual path.
			entry.LastRunAt = manualExecutionLastRunAt(projectRoot, tc.ID, manual.Result)
		}
	}

	// Picker selects per-TC.
	if entry.Wired {
		picked, pickedClass, _ := pickWiring(projectRoot, wiringRecs, classifications, defaultFramework, strictFramework)
		if picked != nil {
			entry.SelectedFramework = picked.Framework
			entry.Framework = picked.Framework

			// Legacy carriers — keep the table renderer happy.
			// "complete" matches the legacy deriveAutomateStatus output for
			// status: accepted, so map.go's automated-count predicate
			// (e.AutomateStatus == "complete" || "developed") still hits.
			entry.AutomateStatus = "complete"
			if picked.Framework == "manual" {
				entry.AutomateStatus = "manual"
			}
			if hit, ok := overlay[overlayKey(picked.TestCase, picked.Framework)]; ok {
				entry.LastResult = legacyResultLabel(hit)
				// BUG-085: LastResultDate intentionally NOT populated on the
				// wiring overlay path. LastRunAt is the canonical post-cutover
				// stage-time carrier (rendered via formatRunAt as
				// "YYYY-MM-DD HH:MM UTC"). Populating both produced a doubled
				// timestamp on the EXECUTE detail line and an RFC3339 leak in
				// the overview LAST RESULT cell.
				entry.LastRunAt = hit.ExecutedAt
				entry.ExecuteStatus = legacyExecuteStatus(hit)
			}
			entry.Stale = pickedClass.StaleTestcaseHash || pickedClass.StaleArtefactHash
			entry.StaleTestCaseHash = pickedClass.StaleTestcaseHash
		}
	}

	// BUG-127: explicit `--framework manual` on a WIRED case. pickWiring only
	// sees wiring records, so it cannot select a manual RESULT file; resolve it
	// here. PRECEDENCE GUARD `SelectedFramework != "manual"`: if a manual WIRING
	// record exists the picker already selected it (with overlay carriers) -- that
	// wiring-derived selection wins and must not be overwritten with result-file
	// values. Placed before the drift block below so its
	// `SelectedFramework == "manual"` gate fires and manual drift surfaces here.
	if entry.Wired && strictFramework && defaultFramework == "manual" && manual != nil && entry.SelectedFramework != "manual" {
		entry.SelectedFramework = "manual"
		entry.Framework = "manual"
		entry.AutomateStatus = "manual"
		if manual.Result != "" {
			entry.LastResult = manual.Result
			if manual.Result == "skipped" {
				entry.ExecuteStatus = "skipped"
			} else {
				entry.ExecuteStatus = "complete"
			}
			// BUG-162 Symptom B: same execution-timestamp join on the BUG-127
			// wired-plus-`--framework manual` selection path. BUG-127 stays closed.
			entry.LastRunAt = manualExecutionLastRunAt(projectRoot, tc.ID, manual.Result)
		}
	}

	// Manual-coverage sub-state (ENH-134) — derived from manual result file.
	if manual != nil {
		switch manual.Result {
		case "":
			entry.ManualCoverage = "prepared"
		default:
			entry.ManualCoverage = "recorded"
		}
		// Drift diagnostics surface from the manual file ONLY when the
		// picker selected manual (or the TC is manual-only). When a
		// non-manual framework is the selected wiring, the manual file's
		// drift signal must NOT bleed into the selected framework's row
		// — that would falsely flag drift against the bats/playwright
		// artefact-hash chain. (BUG-079 follow-up under CON-023.)
		if !entry.Wired || entry.SelectedFramework == "manual" {
			entry.DriftDetected = manual.DriftDetected
			entry.DriftDetectedAt = manual.DriftDetectedAt
			entry.TestCaseHashAtExecute = manual.TestCaseHashAtExecute
		}
	}

	// Available frameworks across all wiring records (for BUG-043 signal).
	if names := frameworksByName(wiringRecs); names != nil {
		entry.AvailableFrameworks = names
	}

	return entry
}

// legacyResultLabel maps an overlayHit to the legacy LastResult
// vocabulary the TTY renderer expects: "pass" | "fail" | "skipped" |
// "error" | "none". Adapter-error executions (status: error, no
// result) map to "error" — keeps existing test expectations.
func legacyResultLabel(hit overlayHit) string {
	if hit.Status == "error" {
		return "error"
	}
	if hit.Result != "" {
		return hit.Result
	}
	return "none"
}

// legacyExecuteStatus maps an overlayHit to the legacy ExecuteStatus
// vocabulary: "complete" | "error" | "skipped" | "none".
//
// ENH-130 / CON-023: status (adapter execution) and result (test outcome)
// are orthogonal. The legacy carrier collapses them into a single column,
// so both "adapter failure" (status: error) and "test outcome error"
// (status: complete, result: error) must render as "error" — matching
// the pre-cutover PipelineDetail mapping that surfaced last-dev-result=error
// as ExecuteStatus=error.
func legacyExecuteStatus(hit overlayHit) string {
	if hit.Status == "error" {
		return "error"
	}
	if hit.Result == "error" {
		return "error"
	}
	if hit.Result == "skipped" {
		return "skipped"
	}
	if hit.Result != "" {
		return "complete"
	}
	return "none"
}

// sortFrameworksByName sorts a FrameworkEntry slice lexically by
// framework name. Used for deterministic JSON output.
func sortFrameworksByName(fws []FrameworkEntry) {
	for i := 0; i < len(fws); i++ {
		for j := i + 1; j < len(fws); j++ {
			if fws[i].Framework > fws[j].Framework {
				fws[i], fws[j] = fws[j], fws[i]
			}
		}
	}
}
