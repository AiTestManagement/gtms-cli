package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/reader"
	"github.com/aitestmanagement/gtms-cli/internal/triage"
)

// ENH-209 renderer coverage: printTriageView (--show) prints summary/defect
// lines, printTriageListLine (--list) appends a terminal-safe [<defect>] marker
// before [spec missing], and both sanitise control characters. JSON is covered
// separately (unchanged); these are the human renderers only.

func showString(t *testing.T, rec *triage.Record) string {
	t.Helper()
	var buf bytes.Buffer
	printTriageView(&buf, &reader.TriageView{Record: rec})
	return buf.String()
}

func listString(t *testing.T, rec *triage.Record, specMissing bool) string {
	t.Helper()
	var buf bytes.Buffer
	printTriageListLine(&buf, &reader.TriageView{Record: rec, SpecMissing: specMissing})
	return buf.String()
}

func TestPrintTriageView_PrintsSummaryAndDefect(t *testing.T) {
	out := showString(t, sampleTriageRecord())
	assert.Contains(t, out, "Summary: Shared creds expired")
	assert.Contains(t, out, "Defect:  JIRA-789")
	// Both lines come after the Snapshot result line.
	assert.Less(t, strings.Index(out, "Snapshot result:"), strings.Index(out, "Summary:"))
	assert.Less(t, strings.Index(out, "Summary:"), strings.Index(out, "Defect:"))
}

func TestPrintTriageView_OmitsEmptyFields(t *testing.T) {
	rec := sampleTriageRecord()
	rec.Summary = ""
	rec.Defect = ""
	out := showString(t, rec)
	assert.NotContains(t, out, "Summary:")
	assert.NotContains(t, out, "Defect:")
	// The common case is untouched: identity + snapshot result only.
	assert.Contains(t, out, "Snapshot result:")
}

func TestPrintTriageView_SanitisesControlChars(t *testing.T) {
	rec := sampleTriageRecord()
	rec.Summary = "line one\nline two \x1b[31mred"
	rec.Defect = "JIRA-792\x1b[0m"
	out := showString(t, rec)
	assert.NotContains(t, out, "\x1b", "no raw ESC byte may reach the terminal")
	assert.Contains(t, out, "Summary: line one?line two ?[31mred")
	assert.Contains(t, out, "Defect:  JIRA-792?[0m")
}

func TestPrintTriageListLine_DefectMarker(t *testing.T) {
	rec := sampleTriageRecord() // defect JIRA-789
	out := listString(t, rec, false)
	assert.Contains(t, out, "-- environment [JIRA-789]")
	assert.NotContains(t, out, "[spec missing]")
}

func TestPrintTriageListLine_NoDefectNoMarker(t *testing.T) {
	rec := sampleTriageRecord()
	rec.Defect = ""
	out := listString(t, rec, false)
	// A row without a defect ends at the value, with no trailing marker (the
	// framework's own [bats] bracket is part of the base row, not a marker).
	assert.True(t, strings.HasSuffix(strings.TrimRight(out, "\n"), "-- environment"),
		"a defect-less row must end at the value with no trailing marker")
}

func TestPrintTriageListLine_MarkerOrderDefectThenSpecMissing(t *testing.T) {
	rec := sampleTriageRecord()
	rec.Defect = "JIRA-791"
	out := listString(t, rec, true)
	assert.Contains(t, out, "[JIRA-791] [spec missing]")
	// [spec missing] must be the final token.
	assert.True(t, strings.HasSuffix(strings.TrimRight(out, "\n"), "[spec missing]"))
	assert.Less(t, strings.Index(out, "[JIRA-791]"), strings.Index(out, "[spec missing]"))
}

func TestPrintTriageListLine_EscapesBracketsAndSanitises(t *testing.T) {
	rec := sampleTriageRecord()
	rec.Defect = "[spec missing]"
	out := listString(t, rec, false)
	// Literal brackets are backslash-escaped so a stored value cannot forge a marker.
	assert.Contains(t, out, `[\[spec missing\]]`)
	// The forged marker must not appear as a genuine trailing [spec missing].
	assert.False(t, strings.HasSuffix(strings.TrimRight(out, "\n"), " [spec missing]"))

	rec.Defect = "JIRA-792\x1b[0m"
	out2 := listString(t, rec, false)
	assert.NotContains(t, out2, "\x1b")
	assert.Contains(t, out2, `[JIRA-792?\[0m]`)
}

func TestPrintTriageListLine_EscapesBackslashBeforeBracket(t *testing.T) {
	// REV-166 CLAUDE-001: a stored backslash adjacent to a bracket must not let
	// the marker close early. Escaping the backslash first turns `a\]b` into
	// `a\\\]b`, so the only unescaped `]` is the marker terminator.
	rec := sampleTriageRecord()
	rec.Defect = `a\]b`
	out := listString(t, rec, false)
	// `a\]b` -> escape `\` first (`a\\]b`) -> escape `]` (`a\\\]b`) -> marker
	// `[a\\\]b]`. The only unescaped `]` is the marker terminator, and it is the
	// final character of the line, so the marker cannot close early.
	assert.Contains(t, out, `[a\\\]b]`)
	assert.True(t, strings.HasSuffix(strings.TrimRight(out, "\n"), `[a\\\]b]`))
}

func TestPrintTriageListLine_MinimalViewRenders(t *testing.T) {
	require.NotEmpty(t, listString(t, sampleTriageRecord(), false))
}

// --- REV-166 CODEX-001: C1 controls must be sanitised on every human surface ---

// C1 controls (U+0080 to U+009F) are terminal-active (CSI U+009B, ST U+009C,
// OSC U+009D) and survived the old byte test. Built via string(rune(...)) so the
// source stays pure ASCII and no raw control byte is committed.
var (
	c1CSI = string(rune(0x9b)) // U+009B
	c1ST  = string(rune(0x9c)) // U+009C
	c1OSC = string(rune(0x9d)) // U+009D
)

// hasC1 reports whether s contains any C1 control rune.
func hasC1(s string) bool {
	for _, r := range s {
		if r >= 0x80 && r <= 0x9f {
			return true
		}
	}
	return false
}

func TestPrintTriageView_SanitisesC1Controls(t *testing.T) {
	rec := sampleTriageRecord()
	rec.Summary = "line" + c1CSI + "2K one"
	rec.Defect = "JIRA-792" + c1ST + c1OSC
	out := showString(t, rec)
	assert.False(t, hasC1(out), "no C1 control rune may reach --show output")
	assert.Contains(t, out, "Summary: line?2K one")
	assert.Contains(t, out, "Defect:  JIRA-792??")
}

func TestPrintTriageListLine_SanitisesC1InDefectMarker(t *testing.T) {
	rec := sampleTriageRecord()
	rec.Defect = "JIRA" + c1CSI + "789"
	out := listString(t, rec, false)
	assert.False(t, hasC1(out), "no C1 control rune may reach --list output")
	assert.Contains(t, out, "[JIRA?789]")
}

func TestRenderTriageDetail_SanitisesC1Controls(t *testing.T) {
	// The status TRIAGE detail block receives the same hardening.
	ft := &reader.FrameworkTriage{
		Value:   "environment",
		Status:  "active",
		Summary: "setup" + c1CSI + "failed",
		Defect:  "JIRA-9" + c1OSC + "9",
		Snapshot: reader.FrameworkTriageSnapshot{
			Task:   "task-11111111",
			Result: "fail",
		},
	}
	var buf bytes.Buffer
	renderTriageDetail(&buf, ft)
	out := buf.String()
	assert.False(t, hasC1(out), "no C1 control rune may reach the status TRIAGE block")
	assert.Contains(t, out, "Summary:  setup?failed")
	assert.Contains(t, out, "Defect:   JIRA-9?9")
}

func TestTriageJSON_RetainsRawC1Controls(t *testing.T) {
	// JSON is unchanged: the stored strings, including C1 controls, are returned
	// raw (Go json emits C1 as literal UTF-8, not \u-escaped, since they are
	// above 0x20). Only human rendering sanitises.
	rec := sampleTriageRecord()
	rec.Summary = "line" + c1CSI + "2K one"
	rec.Defect = "JIRA-792" + c1ST
	var buf bytes.Buffer
	require.NoError(t, writeTriageJSON(&buf, rec, ""))
	assert.True(t, hasC1(buf.String()),
		"--show/--list JSON must return the raw stored C1 bytes unchanged")
}
