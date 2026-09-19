package pathsafe

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSanitizeControlChars pins REV-123 CODEX-001 at the helper's new home
// (promoted from internal/cli in BUG-187), widened by REV-166 CODEX-001. The
// rule: every Unicode control character (the Cc category -- C0 below 0x20, DEL
// 0x7F, and the C1 controls U+0080 to U+009F) becomes '?', printable and
// multibyte runes are untouched, and the string is NOT truncated. This is the
// coverage-gap half of BUG-187 Fix Direction item 6 that the terminal-safety
// BATS spec (tc-0c245100) cannot see: it asserts the exact escaping the CLI
// callers and the triage diagnostics both depend on, so a future change to the
// rule fails here rather than silently.
func TestSanitizeControlChars(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"clean path untouched", "gtms/triage/tc-abc12345--bats.triage.yaml", "gtms/triage/tc-abc12345--bats.triage.yaml"},
		{"ANSI ESC", "e\x1bvil", "e?vil"},
		{"NUL US DEL", "a\x00b\x1fc\x7fd", "a?b?c?d"},
		{"tab is a C0 control", "tab\there", "tab?here"},
		{"newline forges a line", "tc-88888888--bats\nFORGED.triage.yaml", "tc-88888888--bats?FORGED.triage.yaml"},
		{"ESC clear-line sequence", "name\x1b[2K.yaml", "name?[2K.yaml"},
		{"multibyte printable untouched", "café-über.yaml", "café-über.yaml"},
		{"empty stays empty", "", ""},
		// REV-166 CODEX-001: C1 controls (U+0080 to U+009F) are the Cc category
		// too and must be replaced. They survived the old byte test r < 0x20 ||
		// r == 0x7F and could inject terminal sequences once ENH-209 routed
		// stored free-text summary/defect through this helper. Written as \u
		// escapes so no raw control byte sits in the source.
		{"C1 CSI U+009B", "name2K.yaml", "name?2K.yaml"},
		{"C1 OSC U+009D", "name0;title.yaml", "name?0;title.yaml"},
		{"C1 ST U+009C", "name.yaml", "name?.yaml"},
		{"C1 PAD boundary U+0080", "ab", "a?b"},
		{"C1 APC boundary U+009F", "ab", "a?b"},
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, SanitizeControlChars(c.in), "SanitizeControlChars(%q) [%s]", c.in, c.name)
	}

	// Boundary: 0x20 (space) and 0x7E (~) are printable and must survive; 0x1F
	// and 0x7F must not.
	assert.Equal(t, " ~", SanitizeControlChars(" ~"))
	assert.Equal(t, "??", SanitizeControlChars("\x1f\x7f"))

	// Boundary: U+00A0 (no-break space) sits just past the C1 range and is NOT
	// a Cc control (category Zs), so it must survive; the whole C1 range just
	// below it must not.
	assert.Equal(t, " ", SanitizeControlChars(" "))

	// No truncation, unlike sanitizeForError in cli: a long clean string is
	// returned intact (REV-123 CODEX-001).
	long := strings.Repeat("gtms/triage/deep/", 20) + "tc-abc12345--bats.triage.yaml"
	assert.Equal(t, long, SanitizeControlChars(long))
}
