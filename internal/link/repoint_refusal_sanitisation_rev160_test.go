package link

import (
	"strings"
	"testing"

	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// REV-160 F1 (ADR-024 s7). The project-wide refusal renders repository-derived
// wiring FILENAMES into a terminal diagnostic. A wiring file may be named with
// any byte except "/" and NUL, so an unsanitised "%s" lets a hand-edited or
// hostile name inject ANSI/OSC sequences -- clear the screen, rewrite the lines
// above, or forge a line that reads like success -- while the command exits
// non-zero and the operator believes something else happened.
//
// This exact call site has now regressed three times in this area: REV-159
// CODEX-005 (WiringIdentityMismatchError), REV-160 CLAUDE-007
// (reader.CorruptWiringError), and F1 here. It is pinned directly rather than
// through the filesystem because /mnt/c (NTFS via drvfs) cannot hold a filename
// containing ESC, so a fixture-based test would pass vacuously on Windows and
// only ever run on Linux. The renderer is callable, so test the renderer.
//
// Mutation check: drop SanitizeIdentityString from projectWideRepointRefusal and
// this fails on both the raw control bytes and the forged line count.
func TestProjectWideRepointRefusal_SanitisesControlBytesInRecordNames(t *testing.T) {
	const (
		esc = "\x1b"
		bel = "\x07"
		cr  = "\r"
		lf  = "\n"
	)

	malformed := []wiring.DiscoveryResult{
		// filepath.Base is applied inside the renderer, so give it a full path.
		{Path: "/w/gtms/automation/wiring/tc-66666662--" + esc + "[2Jbats.wiring.yaml"},
	}
	unnamed := []string{"not-a-valid" + bel + "-identity.wiring.yaml"}
	// The LF arm is the sharpest: an unsanitised newline lets a filename forge a
	// whole extra diagnostic line.
	unowned := []string{"tc-99999999--" + cr + lf + "    no proven test case owner: (none).wiring.yaml"}

	msg := projectWideRepointRefusal(malformed, unnamed, unowned).Error()

	// 1. No control byte survives into the terminal.
	for label, b := range map[string]string{"ESC": esc, "BEL": bel, "CR": cr} {
		if strings.Contains(msg, b) {
			t.Errorf("refusal emits a raw %s byte from a wiring filename; "+
				"a hand-edited name can rewrite the terminal.\nmessage: %q", label, msg)
		}
	}

	// 2. An injected LF cannot fabricate a line. Header + 3 groups + hint = 5.
	if got := len(strings.Split(msg, lf)); got != 5 {
		t.Errorf("refusal renders %d lines, want 5 -- an injected newline forged a line.\nmessage:\n%s", got, msg)
	}

	// 3. The sanitised record stays IDENTIFIABLE: sanitising must not reduce the
	//    name to noise, or the operator cannot find the file it names.
	for _, want := range []string{
		"tc-66666662--",
		"not-a-valid",
		"tc-99999999--",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("sanitising destroyed the identity %q; the operator cannot locate the record.\nmessage:\n%s", want, msg)
		}
	}

	// 4. The surrounding diagnostic is intact: count, every group label, and the hint.
	for _, want := range []string{
		"refusing a project-wide repoint: 3 wiring record(s)",
		"malformed (unparseable, or filename and body identity disagree):",
		"unattributable (no resolvable test case owner for this filename):",
		"no proven test case owner:",
		"Fix, restore or remove these records before repointing",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal lost %q.\nmessage:\n%s", want, msg)
		}
	}
}
