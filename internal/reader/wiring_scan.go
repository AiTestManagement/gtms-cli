package reader

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
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
	if len(recs) == 0 {
		return nil, nil
	}
	return scanTerminalResults(projectRoot, map[string][]*wiring.WiringRecord{testCaseID: recs}), nil
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
	// Fast path: with no wiring on this project, every result is an
	// orphan in the overlay sense. Skip the directory walk — saves work
	// on fresh clones and pre-automate workspaces.
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
