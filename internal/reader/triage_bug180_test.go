package reader

// BUG-180: test-wrong triage must never write to the TC spec.
//
// The v0.3.1 implementation of --test-wrong parsed the spec into a
// closed frontmatter struct, set status to needs-review, and marshalled
// the struct back over the whole file. That silently deleted unmodelled
// keys, reset YAML formatting and field order, could replace a draft
// lifecycle state, and changed the spec bytes -- which invalidated every
// wiring testcase-hash for the TC and manufactured drift across every
// framework wired to it. The rationale passed via --summary was never
// persisted beyond the terminal.
//
// The containment removes the write entirely and records the decision in
// gtms/triage-history/<tc>.md. Whole-file byte equality is the primary
// invariant below: it subsumes per-field assertions about unknown keys,
// YAML ordering, draft state, and body encoding.
//
// Non-ASCII fixture data is built from Go escape sequences rather than
// pasted literals, so this file stays ASCII-clean at rest per CLAUDE.md.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
)

// emDash is the character CLAUDE.md bans from anything a terminal
// renders. Spelled as a Unicode escape so this file stays ASCII at rest.
const emDash = string(rune(0x2014))

// seedBUG180Project builds a temp project with one TC spec, a bats
// wiring record whose testcase-hash genuinely matches the seeded spec,
// and a failing terminal handoff so triage's preconditions are met.
// Returns the project root and the absolute spec path.
func seedBUG180Project(t *testing.T, tcID, folder, filename, specBody string) (string, string) {
	t.Helper()
	root := t.TempDir()

	writeFile(t, root, "gtms.config", "project:\n  name: bug180\n  repo: github.com/example/bug180\n")
	writeFile(t, root, filepath.Join("gtms/test/cases", folder, filename), specBody)
	mkdirAll(t, root, "gtms/tasks")
	mkdirAll(t, root, "gtms/tasks/pending")

	specPath := filepath.Join(root, "gtms/test/cases", folder, filename)
	seedBUG180Wiring(t, root, tcID, "bats", currentSpecHash(t, specPath))
	seedBUG180Handoff(t, root, tcID, "bats")

	return root, specPath
}

// seedBUG180Wiring writes a wiring record for (tc, framework) carrying
// the supplied testcase-hash. Pass a real hash for a "current" record or
// a fixed dummy for a deliberately stale one.
func seedBUG180Wiring(t *testing.T, root, tcID, framework, testCaseHash string) {
	t.Helper()
	writeFile(t, root,
		filepath.Join("gtms/automation", "wiring", tcID+"--"+framework+".wiring.yaml"),
		"testcase: "+tcID+"\n"+
			"testcase-hash: "+testCaseHash+"\n"+
			"framework: "+framework+"\n"+
			"adapter: "+framework+"-runner\n"+
			"artefact: gtms/automation/specs/"+tcID+"."+framework+"\n"+
			"artefact-hash: aabbccddeeff0011\n")
}

// seedBUG180Handoff writes a terminal failing result contract so triage
// accepts the TC.
func seedBUG180Handoff(t *testing.T, root, tcID, framework string) {
	t.Helper()
	writeFile(t, root,
		filepath.Join(".gtms", "results", "task-"+tcID+"-"+framework+".handoff.yaml"),
		"task: task-"+tcID+"-"+framework+"\n"+
			"command: execute\n"+
			"target: "+tcID+"\n"+
			"adapter: "+framework+"-runner\n"+
			"mode: sync\n"+
			"created: \"2026-08-11T10:00:00Z\"\n"+
			"status: complete\n"+
			"result: fail\n"+
			"framework: "+framework+"\n"+
			"completed: \"2026-08-11T10:01:00Z\"\n")
}

// currentSpecHash computes the wiring testcase-hash for a spec exactly
// the way the drift detector does.
func currentSpecHash(t *testing.T, specPath string) string {
	t.Helper()
	h, err := pipeline.HashFile(specPath)
	require.NoError(t, err)
	return h
}

// readBUG180History returns the triage history for a TC as a string.
func readBUG180History(t *testing.T, root, tcID string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "gtms", "triage-history", tcID+".md"))
	require.NoError(t, err)
	return string(b)
}

// bug180HeadingTimestamp extracts the timestamp from the first history
// heading, which has the shape "## <RFC3339> -- <category>".
func bug180HeadingTimestamp(t *testing.T, history string) string {
	t.Helper()
	for _, line := range strings.Split(history, "\n") {
		if !strings.HasPrefix(line, "## ") {
			continue
		}
		rest := strings.TrimPrefix(line, "## ")
		idx := strings.Index(rest, " -- ")
		require.GreaterOrEqual(t, idx, 0, "heading must use the ASCII separator: %q", line)
		return rest[:idx]
	}
	t.Fatalf("no history heading found in %q", history)
	return ""
}

// wiringTestCaseHash pulls the testcase-hash back out of a wiring
// record's bytes, so a post-triage classification is computed from what
// is actually on disk rather than from a value the test remembered.
func wiringTestCaseHash(t *testing.T, wiringBytes []byte) string {
	t.Helper()
	for _, line := range strings.Split(string(wiringBytes), "\n") {
		if strings.HasPrefix(line, "testcase-hash:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "testcase-hash:"))
		}
	}
	t.Fatalf("no testcase-hash in wiring record")
	return ""
}

func TestBUG180_TestWrong_PreservesSpecBytes(t *testing.T) {
	// The spec deliberately carries everything the closed-struct rewrite
	// used to destroy: a draft lifecycle state, a custom key, a reserved
	// mapping, non-alphabetical field order, irregular spacing, and a
	// non-ASCII body.
	body := "---\n" +
		"status: draft\n" +
		"test_case_id: tc-11111111\n" +
		"title: Preserve me exactly\n" +
		"business_owner: payments\n" +
		"meta:\n" +
		"  source: field\n" +
		"requirement: BUG-180\n" +
		"tags: [checkout,   guest]\n" +
		"---\n" +
		"\n" +
		"## Steps\n" +
		"1. Order a caf" + string(rune(0x00E9)) + " latte\n"

	root, specPath := seedBUG180Project(t, "tc-11111111", "triage-safety", "tc-11111111-preserve-me.md", body)

	before, err := os.ReadFile(specPath)
	require.NoError(t, err)

	result, err := RecordTriage(root, "tc-11111111", "test-wrong", "Expected checkout wording changed", "", "bats")
	require.NoError(t, err)
	require.NotNil(t, result)

	after, err := os.ReadFile(specPath)
	require.NoError(t, err)
	assert.Equal(t, before, after,
		"the complete spec -- custom keys, reserved mapping, draft status, field order, spacing, and UTF-8 body -- must be byte-identical")

	history := readBUG180History(t, root, "tc-11111111")
	assert.Contains(t, history, "test-wrong")
	assert.Contains(t, history, "Framework: bats")
	assert.Contains(t, history, "Expected checkout wording changed")
}

func TestBUG180_TestWrong_WritesHistoryWithoutSummary(t *testing.T) {
	// --summary has no MarkFlagRequired, so a bare --test-wrong is a
	// first-class invocation. The category and timestamp are the
	// decision; persistence must not be gated on the summary. This is
	// the regression that would appear if test-wrong reused app-wrong's
	// `defect != "" || summary != ""` caller guard.
	body := "---\ntest_case_id: tc-22222222\ntitle: No summary supplied\nrequirement: BUG-180\n---\n\n## Steps\n1. Run it\n"
	root, specPath := seedBUG180Project(t, "tc-22222222", "triage-safety", "tc-22222222-no-summary.md", body)

	before, err := os.ReadFile(specPath)
	require.NoError(t, err)

	_, err = RecordTriage(root, "tc-22222222", "test-wrong", "", "", "")
	require.NoError(t, err)

	history := readBUG180History(t, root, "tc-22222222")
	assert.Contains(t, history, "test-wrong", "category is recorded even with no summary")
	assert.Contains(t, history, "Framework: bats", "resolved framework is recorded even with no summary")

	ts := bug180HeadingTimestamp(t, history)
	_, parseErr := time.Parse(time.RFC3339, ts)
	assert.NoError(t, parseErr, "heading carries a parseable RFC3339 timestamp, got %q", ts)

	after, err := os.ReadFile(specPath)
	require.NoError(t, err)
	assert.Equal(t, before, after, "spec must be untouched")
}

func TestBUG180_TestWrong_RepeatedTriageAppends(t *testing.T) {
	// Guards an implementation that only avoids rewriting when the status
	// already happens to equal needs-review. Append-only auditing is
	// intentional: idempotence here means repeated invocations are
	// non-destructive, not that the second decision is deduplicated.
	body := "---\ntest_case_id: tc-44444444\ntitle: Repeated triage\nrequirement: BUG-180\nstatus: automated\n---\n\n## Steps\n1. Run it twice\n"
	root, specPath := seedBUG180Project(t, "tc-44444444", "triage-safety", "tc-44444444-repeat.md", body)

	before, err := os.ReadFile(specPath)
	require.NoError(t, err)

	_, err = RecordTriage(root, "tc-44444444", "test-wrong", "First review of expected values", "", "bats")
	require.NoError(t, err)
	_, err = RecordTriage(root, "tc-44444444", "test-wrong", "Second review confirms expected values", "", "bats")
	require.NoError(t, err)

	history := readBUG180History(t, root, "tc-44444444")
	assert.Equal(t, 2, strings.Count(history, "-- test-wrong"), "both decisions are recorded")
	assert.Contains(t, history, "First review of expected values")
	assert.Contains(t, history, "Second review confirms expected values")
	assert.Less(t,
		strings.Index(history, "First review of expected values"),
		strings.Index(history, "Second review confirms expected values"),
		"entries append in chronological order")

	after, err := os.ReadFile(specPath)
	require.NoError(t, err)
	assert.Equal(t, before, after, "repeated triage still performs no spec write")
}

func TestBUG180_TestWrong_RecordsResolvedFramework(t *testing.T) {
	// The entry body carries the framework the command resolved, while
	// the history file stays keyed on the bare TC ID. Whether the
	// eventual current-state model is per TC or per TC/framework is a
	// BUG-174 decision that this fix must not pre-empt by inventing a
	// framework-qualified filename.
	body := "---\ntest_case_id: tc-33333333\ntitle: Multi framework\nrequirement: BUG-180\n---\n\n## Steps\n1. Run it\n"
	root, specPath := seedBUG180Project(t, "tc-33333333", "triage-safety", "tc-33333333-multi.md", body)

	// Wire the second framework so the TC genuinely has a sibling.
	seedBUG180Wiring(t, root, "tc-33333333", "playwright", currentSpecHash(t, specPath))
	seedBUG180Handoff(t, root, "tc-33333333", "playwright")

	_, err := RecordTriage(root, "tc-33333333", "test-wrong", "Playwright expectation is obsolete", "", "playwright")
	require.NoError(t, err)

	history := readBUG180History(t, root, "tc-33333333")
	assert.Contains(t, history, "test-wrong")
	assert.Contains(t, history, "Playwright expectation is obsolete")

	// The framework must appear as a whole line, and only the resolved one.
	lines := strings.Split(history, "\n")
	assert.Contains(t, lines, "Framework: playwright", "resolved framework recorded as an exact line")
	assert.NotContains(t, lines, "Framework: bats", "the non-selected framework must not appear")

	// No framework-qualified history file may exist.
	entries, err := os.ReadDir(filepath.Join(root, "gtms", "triage-history"))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "tc-33333333.md", entries[0].Name(),
		"history stays keyed on the bare TC ID; a per-framework filename is BUG-174's decision")
}

func TestBUG180_TestWrong_LeavesLegacyNeedsReviewMarker(t *testing.T) {
	// Specs already carrying status: needs-review from v0.3.1 are left
	// alone. The marker is inert in the current read model, and touching
	// those specs would reintroduce the very write this fix removes.
	// BUG-174 owns any future migration.
	body := "---\ntest_case_id: tc-55555555\ntitle: Legacy marker\nrequirement: BUG-180\nstatus: needs-review\nmigration_guard: keep-me\n---\n\n## Steps\n1. Leave me alone\n"
	root, specPath := seedBUG180Project(t, "tc-55555555", "triage-safety", "tc-55555555-legacy-marker.md", body)

	before, err := os.ReadFile(specPath)
	require.NoError(t, err)

	_, err = RecordTriage(root, "tc-55555555", "test-wrong", "Legacy marker remains inert", "", "bats")
	require.NoError(t, err)

	after, err := os.ReadFile(specPath)
	require.NoError(t, err)
	assert.Equal(t, before, after, "no migration or cleanup write")
	assert.Contains(t, string(after), "status: needs-review")
	assert.Contains(t, string(after), "migration_guard: keep-me")

	history := readBUG180History(t, root, "tc-55555555")
	assert.Contains(t, history, "test-wrong")
	assert.Contains(t, history, "Framework: bats")
	assert.Contains(t, history, "Legacy marker remains inert")
}

func TestBUG180_TestWrong_DoesNotChangeDriftClassification(t *testing.T) {
	// The old rewrite changed spec bytes, so every wiring record holding
	// the previous testcase-hash flipped to stale -- across every
	// framework wired to the TC. The fixture starts with one current
	// record and one already-stale record, so this proves triage changes
	// neither classification, not merely that nothing became stale.
	body := "---\ntest_case_id: tc-66666666\ntitle: Drift guard\nrequirement: BUG-180\n---\n\n## Steps\n1. Run it\n"
	root, specPath := seedBUG180Project(t, "tc-66666666", "triage-safety", "tc-66666666-drift.md", body)
	seedBUG180Wiring(t, root, "tc-66666666", "playwright", "0000000000000000")
	seedBUG180Handoff(t, root, "tc-66666666", "playwright")

	batsWiring := filepath.Join(root, "gtms/automation", "wiring", "tc-66666666--bats.wiring.yaml")
	pwWiring := filepath.Join(root, "gtms/automation", "wiring", "tc-66666666--playwright.wiring.yaml")

	batsBefore, err := os.ReadFile(batsWiring)
	require.NoError(t, err)
	pwBefore, err := os.ReadFile(pwWiring)
	require.NoError(t, err)

	batsStaleBefore := isStaleTestCaseHash(root, automationFrontmatter{
		TestCase: "tc-66666666", TestCaseHash: currentSpecHash(t, specPath)})
	pwStaleBefore := isStaleTestCaseHash(root, automationFrontmatter{
		TestCase: "tc-66666666", TestCaseHash: "0000000000000000"})
	require.False(t, batsStaleBefore, "fixture sanity: bats starts current")
	require.True(t, pwStaleBefore, "fixture sanity: playwright starts stale")

	_, err = RecordTriage(root, "tc-66666666", "test-wrong", "Drift must not move", "", "bats")
	require.NoError(t, err)

	batsAfter, err := os.ReadFile(batsWiring)
	require.NoError(t, err)
	pwAfter, err := os.ReadFile(pwWiring)
	require.NoError(t, err)
	assert.Equal(t, batsBefore, batsAfter, "wiring is read-only on the triage path")
	assert.Equal(t, pwBefore, pwAfter, "wiring is read-only on the triage path")

	assert.Equal(t, batsStaleBefore, isStaleTestCaseHash(root, automationFrontmatter{
		TestCase: "tc-66666666", TestCaseHash: wiringTestCaseHash(t, batsAfter)}),
		"a current record must not be reclassified as stale")
	assert.Equal(t, pwStaleBefore, isStaleTestCaseHash(root, automationFrontmatter{
		TestCase: "tc-66666666", TestCaseHash: wiringTestCaseHash(t, pwAfter)}),
		"an already-stale record must not be reclassified either")
}

func TestBUG180_TestWrong_PreservesDraftCount(t *testing.T) {
	// The rewrite replaced status: draft with needs-review, silently
	// decrementing the folder summary's draft count. needs-review has no
	// read-model presentation, so the user lost a visible state and
	// gained an invisible one.
	body := "---\ntest_case_id: tc-77777777\ntitle: Draft guard\nrequirement: BUG-180\nstatus: draft\n---\n\n## Steps\n1. Stay a draft\n"
	root, _ := seedBUG180Project(t, "tc-77777777", "triage-safety", "tc-77777777-draft.md", body)

	before, err := PipelineFolderSummary(root, "")
	require.NoError(t, err)
	require.Len(t, before, 1)
	require.Equal(t, 1, before[0].DraftCount, "fixture sanity: the folder starts with one draft")

	_, err = RecordTriage(root, "tc-77777777", "test-wrong", "Draft must survive", "", "bats")
	require.NoError(t, err)

	after, err := PipelineFolderSummary(root, "")
	require.NoError(t, err)
	require.Len(t, after, 1)
	assert.Equal(t, before[0].DraftCount, after[0].DraftCount,
		"triage must not change the folder summary's draft count")
}

func TestBUG180_HistoryHeadingIsAscii(t *testing.T) {
	// CLAUDE.md forbids em dashes in anything a terminal renders. The
	// heading previously used a literal one, which surfaces as mojibake
	// on Windows consoles and in some CI log viewers.
	body := "---\ntest_case_id: tc-88888888\ntitle: Ascii heading\nrequirement: BUG-180\n---\n\n## Steps\n1. Run it\n"
	root, _ := seedBUG180Project(t, "tc-88888888", "triage-safety", "tc-88888888-ascii.md", body)

	_, err := RecordTriage(root, "tc-88888888", "test-wrong", "ASCII please", "", "bats")
	require.NoError(t, err)
	_, err = RecordTriage(root, "tc-88888888", "app-wrong", "Also ASCII", "BUG-180-ASCII", "bats")
	require.NoError(t, err)

	history := readBUG180History(t, root, "tc-88888888")
	assert.Contains(t, history, "-- test-wrong", "test-wrong heading uses the ASCII separator")
	assert.Contains(t, history, "-- app-wrong", "app-wrong heading uses the ASCII separator")
	assert.NotContains(t, history, emDash, "no em dash may appear in triage history")
}

func TestBUG180_HistoryEntryCannotBeForgedByAMultiLineSummary(t *testing.T) {
	// The history file is line-oriented: "## " starts an entry and each
	// field is a whole line. Now that it is the only durable home for a
	// test-wrong decision, a newline in --summary must not be able to
	// fabricate a second entry with its own timestamp and category.
	body := "---\ntest_case_id: tc-cccc0000\ntitle: Injection guard\nrequirement: BUG-180\n---\n\n## Steps\n1. Run it\n"
	root, _ := seedBUG180Project(t, "tc-cccc0000", "triage-safety", "tc-cccc0000-injection.md", body)

	forged := "real reason\n## 1999-01-01T00:00:00Z -- app-wrong\nDefect: FORGED-001"
	_, err := RecordTriage(root, "tc-cccc0000", "test-wrong", forged, "", "bats")
	require.NoError(t, err)

	// Headings must be counted line-wise, not by substring: the forged
	// text legitimately survives *inside* the Summary line, which is the
	// point -- it is quoted, not structural.
	history := readBUG180History(t, root, "tc-cccc0000")
	headings := 0
	for _, line := range strings.Split(history, "\n") {
		if strings.HasPrefix(line, "## ") {
			headings++
		}
	}
	assert.Equal(t, 1, headings, "exactly one heading line was written")
	assert.Contains(t, history, "## ")
	assert.True(t, strings.HasPrefix(history, "## "), "the real heading is first")
	assert.NotContains(t, history, "\n## 1999-01-01T00:00:00Z",
		"the forged heading must not start a line")
	assert.Contains(t, history, "Summary: real reason ## 1999-01-01T00:00:00Z -- app-wrong Defect: FORGED-001",
		"the text is preserved in full, flattened onto the Summary line")
}

func TestBUG180_AppWrong_KeepsContentGate(t *testing.T) {
	// Generalising the history writer for unconditional test-wrong
	// entries must not leak that rule into app-wrong. app-wrong with
	// neither defect nor summary still writes nothing.
	populated := "---\ntest_case_id: tc-99999999\ntitle: Populated app wrong\nrequirement: BUG-180\n---\n\n## Steps\n1. Run it\n"
	root, _ := seedBUG180Project(t, "tc-99999999", "triage-safety", "tc-99999999-populated.md", populated)

	// A second TC in the same project for the empty-decision case.
	emptyRel := filepath.Join("gtms/test/cases", "triage-safety", "tc-aaaa0000-empty.md")
	writeFile(t, root, emptyRel,
		"---\ntest_case_id: tc-aaaa0000\ntitle: Empty app wrong\nrequirement: BUG-180\n---\n\n## Steps\n1. Run it\n")
	seedBUG180Wiring(t, root, "tc-aaaa0000", "bats", currentSpecHash(t, filepath.Join(root, emptyRel)))
	seedBUG180Handoff(t, root, "tc-aaaa0000", "bats")

	_, err := RecordTriage(root, "tc-99999999", "app-wrong", "Application response is incorrect", "BUG-180-REGRESSION", "bats")
	require.NoError(t, err)

	history := readBUG180History(t, root, "tc-99999999")
	assert.Contains(t, history, "app-wrong")
	assert.Contains(t, history, "BUG-180-REGRESSION")
	assert.Contains(t, history, "Application response is incorrect")

	_, err = RecordTriage(root, "tc-aaaa0000", "app-wrong", "", "", "bats")
	require.NoError(t, err)

	_, statErr := os.Stat(filepath.Join(root, "gtms", "triage-history", "tc-aaaa0000.md"))
	assert.True(t, os.IsNotExist(statErr),
		"app-wrong keeps its content gate: no defect and no summary writes no file")
}

func TestBUG180_AutomationWrong_KeepsDetailsBody(t *testing.T) {
	// automation-wrong shares no code path with the generalised history
	// writer, but BUG-180's regression scope asks for its task body to be
	// confirmed intact. Nothing else asserts the **Details:** line.
	body := "---\ntest_case_id: tc-bbbb0000\ntitle: Automation wrong\nrequirement: BUG-180\n---\n\n## Steps\n1. Run it\n"
	root, _ := seedBUG180Project(t, "tc-bbbb0000", "triage-safety", "tc-bbbb0000-automation.md", body)

	result, err := RecordTriage(root, "tc-bbbb0000", "automation-wrong", "Selectors changed after redesign", "", "bats")
	require.NoError(t, err)
	require.NotEmpty(t, result.NewTaskID)

	entries, err := os.ReadDir(filepath.Join(root, "gtms/tasks", "pending"))
	require.NoError(t, err)
	require.Len(t, entries, 1)

	taskBytes, err := os.ReadFile(filepath.Join(root, "gtms/tasks", "pending", entries[0].Name()))
	require.NoError(t, err)
	taskBody := string(taskBytes)
	assert.Contains(t, taskBody, "**Reason:** triaged as wrong")
	assert.Contains(t, taskBody, "**Details:** Selectors changed after redesign")
}

// --- REV-141 remediation coverage ---
//
// The three fixes below all touch the same canonical persistence path
// that BUG-180 just made authoritative, so each gets a focused test.

// REV-141 CLAUDE-001: --defect was accepted, echoed into TriageResult
// (and therefore into --json), and then dropped on the floor by
// triageTestWrong. Both sides must now agree.
func TestBUG180_TestWrong_PersistsSuppliedDefect(t *testing.T) {
	body := "---\ntest_case_id: tc-dddd0000\ntitle: Defect passthrough\nrequirement: BUG-180\n---\n\n## Steps\n1. Run it\n"
	root, specPath := seedBUG180Project(t, "tc-dddd0000", "triage-safety", "tc-dddd0000-defect.md", body)
	before := currentSpecHash(t, specPath)

	result, err := RecordTriage(root, "tc-dddd0000", "test-wrong", "Expected values are stale", "JIRA-4242", "bats")
	require.NoError(t, err)

	// Side 1: the machine-readable result.
	assert.Equal(t, "JIRA-4242", result.Defect,
		"TriageResult.Defect feeds the --json defect field")

	// Side 2: the durable record. Whole-line match -- the field is a
	// line, not a substring, so a value bleeding into another field
	// would not satisfy this.
	history := readBUG180History(t, root, "tc-dddd0000")
	var sawDefect bool
	for _, line := range strings.Split(history, "\n") {
		if line == "Defect: JIRA-4242" {
			sawDefect = true
		}
	}
	assert.True(t, sawDefect, "history carries the defect as its own field line; got:\n%s", history)

	assert.Equal(t, before, currentSpecHash(t, specPath), "spec still untouched")
}

// REV-141 CODEX-001: only newlines are structural. Everything else the
// user typed must survive byte-for-byte, because this file is the sole
// record and a pasted stack trace loses its meaning when reindented.
func TestBUG180_History_PreservesOrdinaryWhitespace(t *testing.T) {
	body := "---\ntest_case_id: tc-eeee0000\ntitle: Whitespace fidelity\nrequirement: BUG-180\n---\n\n## Steps\n1. Run it\n"
	root, _ := seedBUG180Project(t, "tc-eeee0000", "triage-safety", "tc-eeee0000-ws.md", body)

	// Leading and trailing spaces, a tab, and a repeated-space run.
	summary := "  want:\tfoo    got:\tbar  "
	_, err := RecordTriage(root, "tc-eeee0000", "test-wrong", summary, "", "bats")
	require.NoError(t, err)

	history := readBUG180History(t, root, "tc-eeee0000")
	assert.Contains(t, history, "Summary: "+summary+"\n",
		"tabs, repeated spaces, and leading/trailing space survive exactly")
}

// REV-141 CODEX-001: all three line-break forms must be neutralised, and
// a CRLF pair must collapse to ONE space rather than two.
func TestBUG180_History_NeutralisesEveryNewlineForm(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"lf", "first\nsecond", "first second"},
		{"crlf", "first\r\nsecond", "first second"},
		{"cr", "first\rsecond", "first second"},
		{"mixed", "a\nb\r\nc\rd", "a b c d"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := flattenHistoryValue(tc.in)
			assert.Equal(t, tc.want, got)
			assert.NotContains(t, got, "\n")
			assert.NotContains(t, got, "\r")
		})
	}
}

// REV-141 CODEX-001 follow-on: dropping strings.Fields must not start
// emitting a field for a value that is only whitespace. The pre-REV-141
// behaviour (Fields folded "   " to "") is preserved by trimming for the
// emptiness test while writing the value untrimmed.
func TestBUG180_History_WhitespaceOnlyValueEmitsNoField(t *testing.T) {
	body := "---\ntest_case_id: tc-ffff0000\ntitle: Blank summary\nrequirement: BUG-180\n---\n\n## Steps\n1. Run it\n"
	root, _ := seedBUG180Project(t, "tc-ffff0000", "triage-safety", "tc-ffff0000-blank.md", body)

	_, err := RecordTriage(root, "tc-ffff0000", "test-wrong", " \t\n ", "", "bats")
	require.NoError(t, err)

	history := readBUG180History(t, root, "tc-ffff0000")
	assert.NotContains(t, history, "Summary:",
		"a whitespace-only summary emits no field line")
	assert.Contains(t, history, "-- test-wrong",
		"the decision itself is still recorded unconditionally")
}

// REV-141 CODEX-003: the caller appends "decision recorded" on a nil
// return, so a failed write must surface as an error and must NOT
// produce a success action. The seam: occupy the history path with a
// directory so the file open fails on every platform.
//
// Scope, stated plainly so this is not over-trusted: the reachable seam
// here is the OPEN failure, which was already checked before REV-141.
// This test therefore pins the success ORDERING contract -- no action
// without a clean persist -- and not the Sync/Close error propagation
// that CODEX-003 specifically added. Injecting a Sync or Close failure
// needs an *os.File indirection this package does not have, and adding
// one purely for a test was judged worse than the gap. If that seam ever
// appears, extend this test rather than writing a second one.
func TestBUG180_History_WriteFailureBlocksSuccessAction(t *testing.T) {
	body := "---\ntest_case_id: tc-aaaa1111\ntitle: Write failure\nrequirement: BUG-180\n---\n\n## Steps\n1. Run it\n"
	root, specPath := seedBUG180Project(t, "tc-aaaa1111", "triage-safety", "tc-aaaa1111-wfail.md", body)
	before := currentSpecHash(t, specPath)

	// A directory where the entry file belongs: OpenFile for writing
	// fails, exercising the error path rather than the happy path.
	blocker := filepath.Join(root, "gtms", "triage-history", "tc-aaaa1111.md")
	require.NoError(t, os.MkdirAll(blocker, 0755))

	result, err := RecordTriage(root, "tc-aaaa1111", "test-wrong", "should not be acknowledged", "", "bats")
	require.Error(t, err, "a failed durable write must reach the caller")
	assert.Nil(t, result, "no result -- and therefore no success action -- on a failed write")
	assert.Contains(t, err.Error(), "test-wrong triage history",
		"the error names the operation that failed")

	assert.Equal(t, before, currentSpecHash(t, specPath),
		"the spec is untouched even on the failure path")
}

// --- REV-141 round 4 remediation coverage ---

// CLAUDE-002: framework is written into the same line-oriented file as
// its neighbours, so it needs the same neutralisation. wiring.Find does
// not cross-check a record's framework field against the requested name,
// so a crafted wiring YAML is a real (if low-trust) path in.
func TestBUG180_History_FrameworkIsNeutralised(t *testing.T) {
	for _, form := range []struct{ name, injected string }{
		{"lf", "bats\n## 1999-01-01T00:00:00Z -- app-wrong"},
		{"crlf", "bats\r\n## 1999-01-01T00:00:00Z -- app-wrong"},
		{"cr", "bats\r## 1999-01-01T00:00:00Z -- app-wrong"},
	} {
		t.Run(form.name, func(t *testing.T) {
			// Exercise the writer directly: reaching it through
			// RecordTriage would need a wiring record whose framework
			// field contains a newline, which wiring.Write will not
			// produce. The neutralisation contract is the writer's.
			root := t.TempDir()
			require.NoError(t, appendTriageHistory(root, "tc-fw00000", "test-wrong", form.injected, "", "why"))

			raw, err := os.ReadFile(filepath.Join(root, "gtms", "triage-history", "tc-fw00000.md"))
			require.NoError(t, err)
			history := string(raw)

			headings := 0
			for _, line := range strings.Split(history, "\n") {
				if strings.HasPrefix(line, "## ") {
					headings++
				}
			}
			assert.Equal(t, 1, headings,
				"a newline in framework must not forge a second heading; got:\n%s", history)
			assert.Contains(t, history, "Framework: bats ## 1999-01-01T00:00:00Z -- app-wrong",
				"the value survives in full, flattened onto its own field line")
		})
	}
}

// CLAUDE-004: the app-wrong caller gate and the writer's field gate must
// agree, or a whitespace-only value produces the heading-only entry the
// gate exists to prevent.
func TestBUG180_AppWrong_WhitespaceOnlyValuesWriteNoEntry(t *testing.T) {
	cases := []struct{ name, defect, summary string }{
		{"summary only spaces", "", "   "},
		{"defect only tabs", "\t\t", ""},
		{"both only newlines", "\n", "\r\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := "---\ntest_case_id: tc-ws000001\ntitle: Blank app-wrong\nrequirement: BUG-180\n---\n\n## Steps\n1. Run it\n"
			root, _ := seedBUG180Project(t, "tc-ws000001", "triage-safety", "tc-ws000001-blank.md", body)

			_, err := RecordTriage(root, "tc-ws000001", "app-wrong", tc.summary, tc.defect, "bats")
			require.NoError(t, err)

			_, statErr := os.Stat(filepath.Join(root, "gtms", "triage-history", "tc-ws000001.md"))
			assert.True(t, os.IsNotExist(statErr),
				"no history file at all -- a heading-only entry is what the gate prevents")
		})
	}
}

// CODEX-001: a symlink sitting at the entry path must be refused, and
// crucially its TARGET must be left alone -- the attack this closes is
// pointing the history file at the test-case spec that BUG-180 promises
// never to write.
func TestBUG180_History_RefusesSymlinkWithoutTouchingTarget(t *testing.T) {
	body := "---\ntest_case_id: tc-sym00001\ntitle: Symlink guard\nrequirement: BUG-180\n---\n\n## Steps\n1. Run it\n"
	root, specPath := seedBUG180Project(t, "tc-sym00001", "triage-safety", "tc-sym00001-symlink.md", body)
	specBefore := currentSpecHash(t, specPath)

	histDir := filepath.Join(root, "gtms", "triage-history")
	require.NoError(t, os.MkdirAll(histDir, 0755))
	link := filepath.Join(histDir, "tc-sym00001.md")

	// Windows needs elevation or developer mode for os.Symlink; skip
	// rather than fail where the OS will not create the fixture.
	if err := os.Symlink(specPath, link); err != nil {
		t.Skipf("cannot create symlink on this platform: %v", err)
	}

	_, err := RecordTriage(root, "tc-sym00001", "test-wrong", "should be refused", "", "bats")
	require.Error(t, err, "writing through a symlink must be refused")
	assert.Contains(t, err.Error(), "symlink", "the error names the reason")

	assert.Equal(t, specBefore, currentSpecHash(t, specPath),
		"the symlink target -- here the spec itself -- is byte-for-byte untouched")

	info, lerr := os.Lstat(link)
	require.NoError(t, lerr)
	assert.NotZero(t, info.Mode()&os.ModeSymlink, "the symlink is left in place, not replaced")
}

// Pins the success ORDERING: the action is only reported once the bytes
// are actually on disk, verified by reading the file back rather than
// trusting the return value.
//
// Scope, stated plainly so this is not over-trusted (REV-141 final
// round, CLAUDE-002): this test does NOT cover the checked-Write /
// checked-Close change itself. Reverting that code to the old
// `defer f.Close()` shape leaves this test green, because the happy path
// behaved identically before. Proving the checked close would need an
// *os.File indirection this package does not have; adding one purely for
// a test was judged worse than the gap, the same call made for the
// symlink guard's TOCTOU window. Its sibling
// TestBUG180_History_WriteFailureBlocksSuccessAction carries the same
// caveat for the error path.
func TestBUG180_History_SuccessActionOnlyAfterBytesLand(t *testing.T) {
	body := "---\ntest_case_id: tc-ok000001\ntitle: Success ordering\nrequirement: BUG-180\n---\n\n## Steps\n1. Run it\n"
	root, _ := seedBUG180Project(t, "tc-ok000001", "triage-safety", "tc-ok000001-ok.md", body)

	result, err := RecordTriage(root, "tc-ok000001", "test-wrong", "recorded", "", "bats")
	require.NoError(t, err)
	require.NotNil(t, result)

	// The action is only meaningful if the bytes really landed: read the
	// file back rather than trusting the return value.
	history := readBUG180History(t, root, "tc-ok000001")
	assert.Contains(t, history, "Summary: recorded")

	var sawAction bool
	for _, a := range result.Actions {
		if strings.Contains(a, "test-wrong decision recorded in") {
			sawAction = true
		}
	}
	assert.True(t, sawAction, "success action present once the entry is on disk; actions: %v", result.Actions)
}

// REV-141 final round, CLAUDE-001: report and disk must agree even for
// degenerate input. A whitespace-only --defect used to be announced in
// the action string and emitted in --json while nothing was written, and
// for app-wrong the gate unification made that strictly worse by
// suppressing the history file too. Normalising once in RecordTriage
// collapses content-free values to "" for every consumer at once.
func TestBUG180_ContentFreeValuesAreNotReportedOrPersisted(t *testing.T) {
	for _, category := range []string{"test-wrong", "app-wrong"} {
		t.Run(category, func(t *testing.T) {
			body := "---\ntest_case_id: tc-cf000001\ntitle: Content-free input\nrequirement: BUG-180\n---\n\n## Steps\n1. Run it\n"
			root, _ := seedBUG180Project(t, "tc-cf000001", "triage-safety", "tc-cf000001-contentfree.md", body)

			result, err := RecordTriage(root, "tc-cf000001", category, "  \t ", " \r\n ", "bats")
			require.NoError(t, err)
			require.NotNil(t, result)

			// Report side: nothing claimed. These feed --json via
			// omitempty and the CLI's "Defect linked:" line.
			assert.Empty(t, result.Defect, "a content-free defect must not be reported")
			assert.Empty(t, result.Summary, "a content-free summary must not be reported")
			for _, a := range result.Actions {
				assert.NotContains(t, a, "defect:",
					"the action string must not announce a defect that was never stored")
			}

			// Disk side: agrees with the report.
			path := filepath.Join(root, "gtms", "triage-history", "tc-cf000001.md")
			raw, statErr := os.ReadFile(path)
			if category == "app-wrong" {
				assert.True(t, os.IsNotExist(statErr),
					"app-wrong with no effective content writes no history file")
				return
			}
			require.NoError(t, statErr, "test-wrong still records the decision unconditionally")
			history := string(raw)
			assert.Contains(t, history, "-- test-wrong", "category and timestamp are the decision")
			assert.NotContains(t, history, "Defect:", "no empty Defect field line")
			assert.NotContains(t, history, "Summary:", "no empty Summary field line")
		})
	}
}

// Real content must still survive the same normalisation untouched --
// the guard against "fix the degenerate case, break the normal one".
func TestBUG180_RealContentSurvivesNormalisation(t *testing.T) {
	body := "---\ntest_case_id: tc-rc000001\ntitle: Real content\nrequirement: BUG-180\n---\n\n## Steps\n1. Run it\n"
	root, _ := seedBUG180Project(t, "tc-rc000001", "triage-safety", "tc-rc000001-real.md", body)

	summary := "  want:\tfoo    got:\tbar  "
	result, err := RecordTriage(root, "tc-rc000001", "test-wrong", summary, "JIRA-9", "bats")
	require.NoError(t, err)

	assert.Equal(t, summary, result.Summary, "reported value keeps its exact whitespace")
	assert.Equal(t, "JIRA-9", result.Defect)

	history := readBUG180History(t, root, "tc-rc000001")
	assert.Contains(t, history, "Summary: "+summary+"\n", "persisted value matches the reported one")
	assert.Contains(t, history, "Defect: JIRA-9\n")
}
