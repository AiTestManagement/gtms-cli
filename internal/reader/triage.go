package reader

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aitestmanagement/gtms-cli/internal/id"
	"github.com/aitestmanagement/gtms-cli/internal/task"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// ValidTriageCategories lists the accepted triage category values.
var ValidTriageCategories = []string{"automation-wrong", "test-wrong", "app-wrong"}

// CON-023 / ENH-145 / ENH-146:
//
// Triage is rehomed to the wiring world. The legacy "mutate the
// automation record" path is retired -- wiring is read-only on the
// triage path, the same as on the execute path. The three categories
// now map to:
//
//   - automation-wrong: queue a pending automate task carrying
//     reason: "triaged as wrong" provenance. No wiring write.
//   - test-wrong:       append the decision to
//     gtms/triage-history/<tc>.md. The entry is written on EVERY
//     invocation: the category and timestamp are the decision, and
//     --summary is optional enrichment that must not gate persistence.
//     The TC spec is never written (BUG-180). No wiring write.
//   - app-wrong:        application/product is judged at fault, NOT
//     the automation. Does NOT queue an automate task (the automation
//     itself is presumed correct). When a defect link or triage summary
//     is supplied, the entry is appended to gtms/triage-history/<tc>.md
//     so the project keeps a permanent audit trail. With neither defect
//     nor summary, no history file is written (avoids noise). No wiring
//     write in any case.
//
// BUG-180: the unconditional-append rule is deliberately test-wrong ONLY.
// app-wrong keeps its content gate. Do not unify the two.

// Triage is kept for backward compatibility; it delegates to GetTriageInfo.
func Triage(projectRoot string, testCaseID string) (*TriageInfo, error) {
	return GetTriageInfo(projectRoot, testCaseID, "")
}

// GetTriageInfo reads the current state of a test case for triage decision-making.
// The framework parameter determines which record to operate on. If framework is
// empty, GetTriageInfo auto-resolves from the wiring records for the TC.
func GetTriageInfo(projectRoot, testCaseID, framework string) (*TriageInfo, error) {
	resolved, err := resolveTriageFramework(projectRoot, testCaseID, framework)
	if err != nil {
		return nil, err
	}
	return getTriageInfoForFramework(projectRoot, testCaseID, resolved)
}

// resolveTriageFramework resolves the framework for triage operations.
// If framework is already specified, returns it directly. If empty,
// auto-detects from the wiring records for the TC.
func resolveTriageFramework(projectRoot, testCaseID, framework string) (string, error) {
	if framework != "" {
		return framework, nil
	}
	recs, err := wiring.FindAllForTC(projectRoot, testCaseID)
	if err != nil {
		return "", fmt.Errorf("reading wiring records: %w", err)
	}
	if len(recs) == 0 {
		return "", fmt.Errorf("no wiring record found for '%s'", testCaseID)
	}
	if len(recs) > 1 {
		var frameworks []string
		for _, r := range recs {
			frameworks = append(frameworks, r.Framework)
		}
		return "", fmt.Errorf("multiple wiring records found for '%s' (%s). Specify --framework",
			testCaseID, strings.Join(frameworks, ", "))
	}
	return recs[0].Framework, nil
}

// getTriageInfoForFramework reads triage info for a specific framework
// by joining the wiring record with the latest terminal result overlay.
func getTriageInfoForFramework(projectRoot, testCaseID, framework string) (*TriageInfo, error) {
	rec, _, err := wiring.Find(projectRoot, testCaseID, framework)
	if err != nil {
		return nil, fmt.Errorf("reading wiring record: %w", err)
	}
	if rec == nil {
		return nil, fmt.Errorf("no wiring record found for '%s' (framework: %s)", testCaseID, framework)
	}

	// Look up the latest terminal handoff for (testCase, framework).
	// scanTerminalResultsForTC loads every wiring record for this TC so
	// the ENH-146 join ladder sees the full sibling set -- narrowing the
	// scope to only the selected record would let rung 4 (adapter
	// mapping) wrongly accept a frameworkless result whose adapter is
	// actually ambiguous across the TC's wiring.
	overlay, oErr := scanTerminalResultsForTC(projectRoot, testCaseID)
	if oErr != nil {
		return nil, fmt.Errorf("scanning terminal results: %w", oErr)
	}
	hit, hasResult := overlay[overlayKey(testCaseID, framework)]
	if !hasResult {
		return nil, fmt.Errorf("no execution results found for '%s'. Run 'gtms execute %s' first", testCaseID, testCaseID)
	}

	ar := &automationFrontmatter{
		TestCase:         rec.TestCase,
		Framework:        rec.Framework,
		Status:           "developed",
		Artefact:         rec.Artefact,
		Adapter:          rec.Adapter,
		Result:           hit.Result,
		ExecutedArtefact: hit.Artefact,
		ExecutedAt:       hit.ExecutedAt,
		ExecutedBy:       hit.ExecutedBy,
		Environment:      hit.Environment,
		ArtefactHash:     rec.ArtefactHash,
		TestCaseHash:     rec.TestCaseHash,
		Notes:            hit.Notes,
		Attempts:         hit.Attempts,
	}

	info := &TriageInfo{
		TestCaseID:       testCaseID,
		AutomationRecord: ar,
		LastResult:       hit.Result,
		LastRun:          hit.Artefact,
		Stale:            isStaleArtefact(projectRoot, *ar),
		FailureHistory:   []TriageEntry{},
	}
	return info, nil
}

// RecordTriage writes a triage decision and triggers the appropriate
// follow-on actions. Neither wiring nor the TC spec is ever mutated;
// instead automation-wrong queues a new automate task, and test-wrong
// and app-wrong both append an entry to the triage history log.
func RecordTriage(projectRoot string, testCaseID string, category string, summary string, defect string, framework string) (*TriageResult, error) {
	if !isValidCategory(category) {
		return nil, fmt.Errorf("invalid triage category: '%s'", category)
	}

	resolved, resolveErr := resolveTriageFramework(projectRoot, testCaseID, framework)
	if resolveErr != nil {
		return nil, resolveErr
	}
	framework = resolved

	// Confirm wiring exists for this (TC, framework).
	rec, _, err := wiring.Find(projectRoot, testCaseID, framework)
	if err != nil {
		return nil, fmt.Errorf("reading wiring record: %w", err)
	}
	if rec == nil {
		return nil, fmt.Errorf("no wiring record found for '%s' (framework: %s)", testCaseID, framework)
	}

	// Execution history must exist before triaging. Load every wiring
	// record for the TC so the join ladder's "exactly one matching
	// wiring" rule sees the full sibling set (a narrower view would let
	// rung 4 wrongly accept ambiguous adapter mappings -- see the
	// scanTerminalResultsForTC docstring).
	overlay, oErr := scanTerminalResultsForTC(projectRoot, testCaseID)
	if oErr != nil {
		return nil, fmt.Errorf("scanning terminal results: %w", oErr)
	}
	if _, ok := overlay[overlayKey(testCaseID, framework)]; !ok {
		return nil, fmt.Errorf("no execution results found for '%s'. Run 'gtms execute %s' first", testCaseID, testCaseID)
	}

	// REV-141 final round, CLAUDE-001: normalise ONCE, here, so every
	// consumer downstream sees the same value -- result.Summary and
	// result.Defect (which feed --json), the action strings, the CLI
	// renderer, and the history writer. Previously the writer applied
	// hasHistoryValue while the report sites still tested `!= ""`, so a
	// whitespace-only --defect was announced to the user and emitted in
	// --json while nothing was written to disk. That is the same
	// report-versus-disk divergence this record set out to remove,
	// narrowed to degenerate input.
	//
	// Values with real content keep their tabs, indentation, and
	// leading/trailing spaces; only content-free values collapse to "".
	summary = normaliseHistoryInput(summary)
	defect = normaliseHistoryInput(defect)

	result := &TriageResult{
		TestCaseID: testCaseID,
		Category:   category,
		Summary:    summary,
		Defect:     defect,
		Actions:    []string{},
	}

	switch category {
	case "automation-wrong":
		err = triageAutomationWrong(projectRoot, rec, testCaseID, summary, result)
	case "test-wrong":
		err = triageTestWrong(projectRoot, rec, testCaseID, defect, summary, result)
	case "app-wrong":
		err = triageAppWrong(projectRoot, rec, testCaseID, defect, summary, result)
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

// triageAutomationWrong queues a pending automate task with the
// "triaged as wrong" reason. The wiring record is read-only.
func triageAutomationWrong(projectRoot string, rec *wiring.WiringRecord, testCaseID, summary string, result *TriageResult) error {
	taskID := fmt.Sprintf("task-%s", id.New())
	body := fmt.Sprintf("## Triage: automation-wrong\n\nRe-automate %s after triage.\n\n**Reason:** triaged as wrong\n", testCaseID)
	if summary != "" {
		body += fmt.Sprintf("**Details:** %s\n", summary)
	}

	tf := &task.TaskFile{
		ID:        taskID,
		Type:      "automate",
		Target:    testCaseID,
		Status:    "pending",
		Created:   time.Now().UTC().Format(time.RFC3339),
		Framework: rec.Framework,
	}

	if _, err := task.Create(projectRoot, tf, body); err != nil {
		return fmt.Errorf("creating new automate task: %w", err)
	}

	result.NewTaskID = taskID
	result.Actions = append(result.Actions,
		fmt.Sprintf("New automate task queued: %s (reason: triaged as wrong)", taskID))
	return nil
}

// triageTestWrong records a test-wrong decision in the TC's triage
// history. Neither the TC spec nor wiring is touched.
//
// BUG-180: this used to rewrite the spec's frontmatter status to
// needs-review by marshalling a closed struct over the whole file, which
// silently deleted unmodelled keys, reset formatting, could erase a
// draft lifecycle state, and invalidated every wiring testcase-hash for
// the TC. The marker also had no read model, so the destruction bought
// nothing. The decision now lives in gtms/triage-history/<tc>.md.
//
// The append is UNCONDITIONAL. --summary is optional (no
// MarkFlagRequired on the flag), and the category plus timestamp are the
// durable decision on their own -- so unlike app-wrong, there is no
// defect/summary content gate here.
// REV-141 CLAUDE-001: `defect` is threaded through rather than discarded.
// RecordTriage sets result.Defect unconditionally and TriageResult
// serialises it as `defect`, so dropping it here made `--json` claim a
// defect link that nothing on disk held. Passing it through is preferred
// over rejecting `--defect --test-wrong`: the invocation already
// succeeded on v0.3.1, the writer already supports the field, and it
// adds no new CLI failure path.
func triageTestWrong(projectRoot string, rec *wiring.WiringRecord, testCaseID, defect, summary string, result *TriageResult) error {
	if err := appendTriageHistory(projectRoot, testCaseID, "test-wrong", rec.Framework, defect, summary); err != nil {
		return fmt.Errorf("recording test-wrong triage history: %w", err)
	}

	result.Actions = append(result.Actions, fmt.Sprintf(
		"test-wrong decision recorded in %s (framework: %s)",
		triageHistoryDisplayPath(testCaseID), rec.Framework))
	return nil
}

// triageHistoryDisplayPath renders the history location for user-facing
// output. It is built with forward slashes rather than filepath.Join so
// the message reads identically on Windows and Unix.
func triageHistoryDisplayPath(testCaseID string) string {
	return fmt.Sprintf("gtms/triage-history/%s.md", testCaseID)
}

// triageAppWrong records an app-wrong triage outcome. app-wrong means
// the application/product is at fault, NOT the automation, so no
// automate task is queued (the automation itself is presumed correct).
// When a defect link or summary is supplied, the entry is appended to
// gtms/triage-history/<tc>.md so the project keeps a permanent audit
// trail; with neither, no history file is written. Wiring is never
// mutated, and result.NewTaskID stays empty by contract.
//
// The wiring record parameter is retained for signature symmetry with
// the other triage handlers; it is intentionally unused here.
func triageAppWrong(projectRoot string, rec *wiring.WiringRecord, testCaseID, defect, summary string, result *TriageResult) error {
	_ = rec

	// BUG-180: this content gate is category-specific and stays. Only
	// test-wrong appends unconditionally.
	//
	// The framework argument is deliberately empty, so app-wrong entries
	// keep the content they have always had. Recording the resolved
	// framework was asked for by test-wrong alone; extending it to
	// app-wrong is a decision for BUG-174, which owns the eventual
	// per-TC vs per-TC/framework state model.
	// REV-141 round 4, CLAUDE-004: this gate and the field writers inside
	// appendTriageHistory must agree on what "empty" means, or a value of
	// " " passes here and then writes nothing, producing exactly the
	// heading-only entry this gate exists to prevent. Both sides now call
	// hasHistoryValue so they cannot drift apart again.
	if hasHistoryValue(defect) || hasHistoryValue(summary) {
		if err := appendTriageHistory(projectRoot, testCaseID, "app-wrong", "", defect, summary); err != nil {
			return fmt.Errorf("recording app-wrong triage history: %w", err)
		}
	}

	// NewTaskID stays empty: app-wrong does not queue an automate task.
	action := "Triage recorded: app-wrong"
	if defect != "" {
		action += fmt.Sprintf(" (defect: %s)", defect)
	}
	result.Actions = append(result.Actions, action)
	return nil
}

// appendTriageHistory appends one triage entry to
// gtms/triage-history/<tc>.md. The directory and file are created on
// first call and appended on subsequent calls so the audit trail
// builds up over time. Entries are dated (UTC, RFC3339) and headed with
// the triage category so consumers can scan the file chronologically.
//
// The filename is keyed on the bare TC ID even when framework is set.
// Recording the framework in the entry body preserves the information
// without deciding whether the eventual current-state model is per TC or
// per TC/framework -- that decision belongs to BUG-174.
//
// Whether an entry is worth writing at all is the caller's judgement:
// app-wrong gates on having a defect or summary (both empty would give a
// heading-only entry), while test-wrong always writes because its
// category and timestamp are themselves the decision.
//
// Field lines are bare "Key: value", not markdown bullets, so each field
// can be matched as a whole line. User-supplied values are flattened to a
// single line first -- see flattenHistoryValue.
func appendTriageHistory(projectRoot, testCaseID, category, framework, defect, summary string) error {
	dir := filepath.Join(projectRoot, "gtms", "triage-history")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating triage-history directory: %w", err)
	}
	path := filepath.Join(dir, testCaseID+".md")

	// REV-141 round 4, CODEX-001: refuse to append through a symlink.
	// The entry path is fully constructed from a validated TC ID, so this
	// is not path injection -- it guards a symlink already sitting on
	// disk. gtms/triage-history/ is not gitignored, so a repository can
	// carry one, and pointing it at the test-case spec would let
	// --test-wrong write to the very file BUG-180 promises never to touch.
	//
	// Honest about what this is NOT:
	//   - Not race-proof. There is a check-to-open interval, so a
	//     sufficiently determined local attacker can still win the race.
	//     Closing that needs a no-follow open, which Go does not offer
	//     portably (Windows ignores O_NOFOLLOW), and building that
	//     abstraction is out of scope here.
	//   - Only the final path component. A symlinked PARENT directory is
	//     a separate concern and is not covered.
	// It is sized for accidental or repository-carried symlinks, which is
	// the threat that actually touches BUG-180's data-safety promise.
	if info, lerr := os.Lstat(path); lerr == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to write triage history through a symlink: %s", path)
	}

	var entry strings.Builder
	fmt.Fprintf(&entry, "## %s -- %s\n", time.Now().UTC().Format(time.RFC3339), category)
	// REV-141 round 4, CLAUDE-002: framework goes through the same
	// neutralisation as its neighbours. It normally arrives from a
	// wiring record's controlled identity, but this helper should not
	// have one structurally unsafe field sitting beside two protected
	// ones -- wiring.Find does not cross-check a record's framework
	// field against the requested name.
	if framework = flattenHistoryValue(framework); hasHistoryValue(framework) {
		fmt.Fprintf(&entry, "Framework: %s\n", framework)
	}
	// The gate tests effective content, but the WRITTEN value is not
	// trimmed: a real value keeps its tabs, indentation, and repeated
	// spaces (REV-141 CODEX-001), while a whitespace-only value emits no
	// field at all.
	if defect = flattenHistoryValue(defect); hasHistoryValue(defect) {
		fmt.Fprintf(&entry, "Defect: %s\n", defect)
	}
	if summary = flattenHistoryValue(summary); hasHistoryValue(summary) {
		fmt.Fprintf(&entry, "Summary: %s\n", summary)
	}
	entry.WriteString("\n")

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("opening triage history file: %w", err)
	}

	// REV-141 CODEX-003, revised in round 4: the Write and Close results
	// are both checked before the caller is allowed to report success.
	// The caller appends its "decision recorded" action only on a nil
	// return, and this file is now the only durable home for a test-wrong
	// decision, so neither result may be discarded -- which an unchecked
	// `defer f.Close()` would do.
	//
	// Sync was tried here and REMOVED. On Windows, Go's O_APPEND open
	// deliberately drops GENERIC_WRITE in favour of FILE_APPEND_DATA
	// (syscall_windows.go), and FlushFileBuffers -- what File.Sync calls
	// -- is documented to require GENERIC_WRITE. A fatal Sync would
	// therefore have failed every `gtms triage --test-wrong` on Windows
	// AFTER the entry was already written, turning a theoretical
	// durability gain into a certain platform regression.
	//
	// So the contract is checked Write and checked Close, NOT crash-proof
	// durability: an entry acknowledged here survives normal operation,
	// not necessarily a host that loses power moments later.
	if _, werr := f.Write([]byte(entry.String())); werr != nil {
		f.Close()
		return fmt.Errorf("writing triage history entry: %w", werr)
	}
	if cerr := f.Close(); cerr != nil {
		return fmt.Errorf("closing triage history file: %w", cerr)
	}
	return nil
}

// hasHistoryValue reports whether a value carries content worth writing
// as a history field. Callers deciding WHETHER to write an entry and the
// writer deciding whether to emit each field must use this same rule --
// when they disagreed (REV-141 round 4, CLAUDE-004) a whitespace-only
// --summary passed the caller's gate and then produced a heading-only
// entry that the gate existed to prevent.
func hasHistoryValue(value string) bool {
	return strings.TrimSpace(flattenHistoryValue(value)) != ""
}

// normaliseHistoryInput converts a user-supplied --summary or --defect
// into the single canonical form used by BOTH the persisted history
// entry and everything that reports the value back (result.Summary /
// result.Defect, the action strings, the CLI renderer).
//
// A value carrying real content is only flattened, so its tabs,
// indentation, and leading/trailing spaces survive. A value with no
// effective content becomes "" -- which is what lets the plain `!= ""`
// tests at the report sites stay correct without each having to know
// about hasHistoryValue.
func normaliseHistoryInput(value string) string {
	value = flattenHistoryValue(value)
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return value
}

// flattenHistoryValue collapses a user-supplied --summary or --defect
// into a single line so it cannot forge structure in the audit log.
//
// The history file is append-only and line-oriented: a heading is any
// line starting with "## ", and each field is a whole line. A value
// containing a newline could therefore inject a fabricated entry --
// complete with its own timestamp and category -- into the record.
// BUG-180 promoted this file from a supplementary trail to the only
// durable home for a test-wrong decision, so the value is now worth
// protecting.
//
// REV-141 CODEX-001: only the STRUCTURAL characters are neutralised.
// The previous strings.Fields implementation folded every whitespace run
// and trimmed the ends, so tabs, indentation, and repeated spaces in a
// pasted stack trace were destroyed with no way to recover them -- a
// real loss once this file became the sole record. Newlines are the only
// characters that can forge a heading or a field line, so they are the
// only ones replaced. JSON-quoting the value would also be reversible
// but would introduce a THIRD history field shape on top of the two this
// change already leaves behind (see REV-141 CODEX-002 / CLAUDE-002).
//
// \r\n is handled before the bare cases so a CRLF pair collapses to one
// space rather than two.
func flattenHistoryValue(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return s
}

// isValidCategory checks if a category string is a valid triage category.
func isValidCategory(category string) bool {
	for _, c := range ValidTriageCategories {
		if c == category {
			return true
		}
	}
	return false
}
