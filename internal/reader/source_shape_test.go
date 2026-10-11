package reader

// Source-shape tests verify compile-time / code-structure invariants that were
// previously asserted by BATS acceptance tests grepping Go source files.
// These are architectural guardrails, not behaviour tests.
// See ENH-088 for the full audit and migration rationale.

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Audit #15 (specscanner regex upper bound) was retired with internal/reader/
// specscanner.go, which ENH-205 deleted as a dead legacy spec walk.

// --- ENH-128: delete.go must contain zero framework-specific literals ---

// TestSourceShape_DeleteGoNoFrameworkLiterals verifies that delete.go contains
// no framework-specific literals outside of doc comments.
// ENH-128 AC #1: zero literal framework names, extensions, or paths.
func TestSourceShape_DeleteGoNoFrameworkLiterals(t *testing.T) {
	src, err := os.ReadFile("delete.go")
	require.NoError(t, err)

	// Strip doc comments (lines starting with //) to allow framework names in documentation
	var codeLines []string
	for _, line := range strings.Split(string(src), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		codeLines = append(codeLines, line)
	}
	code := strings.Join(codeLines, "\n")

	// Framework-specific literals that must not appear in non-comment code
	forbidden := []string{
		`"bats"`,
		`".bats"`,
		`"junit"`,
		`"test/acceptance"`,
		`"test", "acceptance"`,
		`"results/junit"`,
		`"results", "junit"`,
		`findBATSScripts`,
		`findJUnitResults`,
		`BATSScripts`,
		`JUnitResults`,
	}

	for _, lit := range forbidden {
		assert.False(t, strings.Contains(code, lit),
			"delete.go non-comment code must not contain framework literal %q", lit)
	}
}

// --- CON-023 / ENH-145/146 Task 12: architectural guardrails ---

// TestSourceShape_NoLegacyAutomationRecordReadsInReader pins that no
// production file in internal/reader/ reads or writes the retired
// gtms/automation/records/*.automation.md surface, nor depends on
// legacy automation-record helpers from internal/pipeline. The
// migration tool (scripts/migrate-wiring/) is the only legitimate
// remaining caller of those helpers.
//
// Non-comment code only -- provenance comments mentioning the legacy
// names are fine.
func TestSourceShape_NoLegacyAutomationRecordReadsInReader(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)

	forbidden := []string{
		`pipeline.FindAutomationRecord`,
		`pipeline.WriteAutomationRecord`,
		`pipeline.ReadAutomationRecord`,
		`pipeline.BuildAutomationRecord`,
		`pipeline.UpdateExecutionResult`,
		`pipeline.CreateAutomationRecord`,
		`pipeline.TryAutoCreateRecord`,
		`pipeline.ResolveArtefact`,
		`layout.RecordsDir(`,
		`".automation.md"`,
		`automation/records`,
	}

	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		require.NoError(t, err)
		code := stripGoComments(t, f, string(src))
		for _, lit := range forbidden {
			assert.NotContainsf(t, code, lit,
				"%s: production reader code must not reference legacy automation-record surface %q", f, lit)
		}
	}
}

// TestSourceShape_NoWiringWriteFromReader pins that no production code
// in internal/reader/ writes a wiring file. Wiring is read-only on the
// reader side; only gtms automate and gtms link create or refresh
// wiring records (CON-023 / ENH-145).
func TestSourceShape_NoWiringWriteFromReader(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		require.NoError(t, err)
		code := stripGoComments(t, f, string(src))
		// `wiring.Write(` is the canonical writer; reject any call to it
		// from the reader package.
		assert.NotContainsf(t, code, `wiring.Write(`,
			"%s: production reader code must not call wiring.Write -- wiring is read-only on the reader path", f)
	}
}

// stripGoComments removes // line comments and /* */ block comments
// from Go source so source-shape literal searches do not match
// provenance notes. Returns the comment-stripped source.
func stripGoComments(t *testing.T, name, src string) string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
	if err != nil {
		// Fall back to the raw source -- guarantees a strict (over)check.
		return src
	}
	// Walk file comments and blank them out in the source. Simpler than
	// a full token-level walk and good enough for our literal scans.
	out := []byte(src)
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			start := fset.Position(c.Pos()).Offset
			end := fset.Position(c.End()).Offset
			if start < 0 || end > len(out) || start > end {
				continue
			}
			for i := start; i < end; i++ {
				if out[i] != '\n' {
					out[i] = ' '
				}
			}
		}
	}
	return string(out)
}

// --- ENH-200 AC #8: one handoff scanner, and triage uses it ---

// resultsWalkLiterals are the literals a file needs in order to enumerate
// .gtms/results/ and parse handoffs for itself. Any file containing one is
// walking the results directory rather than asking the shared scanner.
var resultsWalkLiterals = []string{
	`".gtms", "results"`,
	`.handoff.yaml`,
	`result.Read(`,
}

// resultsWalkAllowlist is the complete set of production files in
// internal/reader/ permitted to touch the results directory directly, with the
// reason each is legitimate. Adding a file here is a deliberate architectural
// decision, not a formality.
//
//	wiring_scan.go -- the single canonical terminal-result scanner. Its
//	  ENH-146 join-precedence ladder is what makes (testcase, framework)
//	  selection unambiguous; a second scanner would answer differently.
//	delete.go, reset.go -- lifecycle commands that REMOVE handoffs. They do
//	  not select a latest terminal result, so they cannot disagree with the
//	  ladder.
var resultsWalkAllowlist = map[string]bool{
	"wiring_scan.go": true,
	"delete.go":      true,
	"reset.go":       true,
}

// TestSourceShape_TriageUsesSharedResultScanner pins ENH-200 AC #8, whose
// second half ("triage does not implement an independent handoff scanner") is
// a negative structural claim that no behavioural test can establish. Every
// acceptance case would stay green if someone added a private scanner
// tomorrow, because a rival scanner that happened to agree on the fixtures
// would be invisible. It would diverge later, on the ambiguous cases the
// ENH-146 ladder exists to arbitrate.
func TestSourceShape_TriageUsesSharedResultScanner(t *testing.T) {
	src, err := os.ReadFile("triage.go")
	require.NoError(t, err)
	code := stripGoComments(t, "triage.go", string(src))

	// Positive: triage selects results through the shared scanner.
	assert.Contains(t, code, "scanTerminalResultsForTC(",
		"triage.go must select the latest terminal result through the shared "+
			"scanner so it inherits the ENH-146 join-precedence and ambiguity rules")

	// Negative: and does not enumerate the results directory itself.
	for _, lit := range resultsWalkLiterals {
		assert.NotContainsf(t, code, lit,
			"triage.go must not walk .gtms/results/ itself (found %q) -- "+
				"result selection belongs to scanTerminalResultsForTC", lit)
	}
}

// TestSourceShape_SingleResultScanner pins that the set of reader files
// touching the results directory does not grow. This is the half of AC #8 that
// TestSourceShape_TriageUsesSharedResultScanner cannot cover: a second scanner
// could be introduced in a NEW file and triage could then call that instead.
func TestSourceShape_SingleResultScanner(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || resultsWalkAllowlist[f] {
			continue
		}
		src, err := os.ReadFile(f)
		require.NoError(t, err)
		code := stripGoComments(t, f, string(src))
		for _, lit := range resultsWalkLiterals {
			assert.NotContainsf(t, code, lit,
				"%s: production reader code outside the allowlist must not walk "+
					".gtms/results/ (found %q). If this file genuinely needs direct "+
					"access, add it to resultsWalkAllowlist with a reason -- do not "+
					"delete this assertion.", f, lit)
		}
	}
}

// --- ENH-202: status and gaps triage visibility reuses the shared readers ---

// triageDirWalkLiterals are the literals a reader file would need to open-code a
// gtms/triage/ scan for itself. Enumeration must instead go through the triage
// package (triage.ListForTC / triage.ListAll), so the whole reader shares one
// triage enumeration path -- the same reason ENH-200 pinned a single results
// scanner. layout.TriageDir is the on-disk directory helper; triage.FileSuffix
// is the record extension; a raw os.ReadDir on either would drift.
var triageDirWalkLiterals = []string{
	"layout.TriageDir(",
	"triage.FileSuffix",
}

// TestSourceShape_StatusGapsUseSharedTriageReader pins ENH-202 item 12 and
// Verification Scope item 6: the status/gaps triage-join code reuses the ENH-201
// read machinery (triage.ListAll + the shared scanTerminalResults scanner +
// compareRawResult) and does not open-code a second triage-directory scan. Like
// the sibling scanner guard, the negative half is a structural claim no
// behavioural test can make: a private triage-directory walk that agreed on the
// fixtures would be invisible until it drifted.
func TestSourceShape_StatusGapsUseSharedTriageReader(t *testing.T) {
	// Positive: the ENH-202 join file routes through the shared readers.
	vis, err := os.ReadFile("triage_visibility.go")
	require.NoError(t, err)
	visCode := stripGoComments(t, "triage_visibility.go", string(vis))
	assert.Contains(t, visCode, "triage.ListAll(",
		"triage_visibility.go must enumerate triage records through triage.ListAll "+
			"in ONE directory pass (the canonical all-records enumerator), not a "+
			"private scan and not a per-test-case ListForTC loop")
	assert.NotContains(t, visCode, "triage.ListForTC(",
		"triage_visibility.go must not re-read the triage directory once per test "+
			"case: ListForTC does a full os.ReadDir per call, which is O(N*M) across "+
			"a status or gaps view. Enumerate once with triage.ListAll and scope in "+
			"memory (REV-148 CODEX-003 / CLAUDE-003).")
	assert.Contains(t, visCode, "scanTerminalResults(",
		"triage_visibility.go must join results through the shared scanTerminalResults "+
			"scanner so it inherits the ENH-146 join-precedence rules")
	assert.Contains(t, visCode, "compareRawResult(",
		"triage_visibility.go must reuse compareRawResult for the raw-vs-raw nullable "+
			"comparison rather than reimplementing it")

	// Positive: gaps routes its triage counting through the shared helper.
	gaps, err := os.ReadFile("gaps.go")
	require.NoError(t, err)
	gapsCode := stripGoComments(t, "gaps.go", string(gaps))
	assert.Contains(t, gapsCode, "activeTriageByTC(",
		"gaps.go must count triage through the shared activeTriageByTC helper, not "+
			"a private triage-directory scan")

	// Negative: NO production reader file open-codes a triage-directory scan.
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		require.NoError(t, err)
		code := stripGoComments(t, f, string(src))
		for _, lit := range triageDirWalkLiterals {
			assert.NotContainsf(t, code, lit,
				"%s: production reader code must not open-code a triage-directory scan "+
					"(found %q). Triage enumeration belongs to the triage package "+
					"(triage.ListForTC / triage.ListAll); status and gaps reach it through "+
					"triage_visibility.go.", f, lit)
		}
	}
}
