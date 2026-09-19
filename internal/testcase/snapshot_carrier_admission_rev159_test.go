package testcase

import (
	"strings"
	"testing"
)

// REV-159 CODEX-003 / CLAUDE-006 -- the same defect found independently by two
// reviewers, and the reason ADR-024 grew section 4a.
//
// Admission used to key its second arm on a SUCCESSFUL frontmatter parse. A
// renamed spec is admitted only by that arm (its filename encodes no id), so the
// moment its frontmatter broke it left the ownership model entirely -- and a
// folder delete, reset, automate, execute or repoint over the folder it sits in
// proceeded and reported success. That is Unprovable converted into absence on a
// destructive path, ADR-024's central rejected behaviour.
//
// 4a's answer: admission detects the identity CARRIER without parsing.

// The finding itself. A renamed spec whose YAML is broken must remain a
// candidate, stay Unprovable, and refuse the folder that contains it.
func TestAdmission_MalformedRenamedSpecStillRefusesItsFolder(t *testing.T) {
	root, cases := casesDirFor(t)
	writeSpecID(t, cases, "feat/tc-11111111-login.md", "tc-11111111")
	// Renamed (no tc- envelope) AND malformed: the value is an unclosed quote, so
	// no YAML parser reaches the id. The carrier line is nonetheless right there.
	writeRaw(t, cases, "feat/login.md", "---\ntest_case_id: \"tc-22222222\ntitle: broken\n---\n\nbody\n")

	snap := mustSnapshot(t, root)

	_, unprovable := snap.SelectFolder("gtms/test/cases/feat", false)
	if len(unprovable) != 1 {
		t.Fatalf("SelectFolder reported %d unprovable candidates, want 1: a malformed renamed spec inside the selected folder must not vanish", len(unprovable))
	}
	if unprovable[0].Reason != ReasonMalformed {
		t.Fatalf("reason = %v, want ReasonMalformed", unprovable[0].Reason)
	}
	err := snap.RequireFolderOwnership("gtms/test/cases/feat", false)
	if err == nil {
		t.Fatal("RequireFolderOwnership allowed the folder: a destructive operation would proceed over an unprovable in-scope spec")
	}
	if !strings.Contains(err.Error(), "login.md") {
		t.Fatalf("diagnostic %q does not name the offending file", err.Error())
	}
}

// The unreadable half of the same finding: the filename envelope must not be the
// only thing keeping a candidate alive when its body cannot be read.
func TestAdmission_MalformedRenamedSpecIsAdmittedNotDiscarded(t *testing.T) {
	root, cases := casesDirFor(t)
	writeRaw(t, cases, "feat/checkout-spec.md", "---\n  test_case_id :\ttc-33333333\n")

	snap := mustSnapshot(t, root)
	if got := len(snap.candidates); got != 1 {
		t.Fatalf("snapshot holds %d candidates, want 1: an unterminated frontmatter region carrying the key is still a carrier (ADR-024 4a)", got)
	}
}

// ADR-024 4a's delimiter rule, and the reason it is not byte-exact: every spec
// file in THIS repository is CRLF, so a byte-exact "---" comparison would admit
// none of them and drop valid renamed owners along with malformed ones.
func TestAdmission_DelimiterToleratesCRLFBOMAndTrailingBlanks(t *testing.T) {
	// Each fixture is RENAMED (so only the carrier arm can admit it) and MALFORMED
	// inside the region -- an unclosed quote, and no closing delimiter -- so nothing
	// parses and admission is the only thing that can keep the candidate alive.
	// Without both properties the frontmatter parses, the file is admitted as an
	// owner, and the delimiter rule is never exercised at all.
	cases := []struct {
		name string
		body string
	}{
		{"crlf", "---\r\ntest_case_id: \"tc-44444444\r\n  bad: [\r\n"},
		{"bom", "\xef\xbb\xbf---\ntest_case_id: \"tc-44444444\n  bad: [\n"},
		{"trailing_spaces", "---   \ntest_case_id: \"tc-44444444\n  bad: [\n"},
		{"trailing_tab", "---\t\ntest_case_id: \"tc-44444444\n  bad: [\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, dir := casesDirFor(t)
			writeRaw(t, dir, "feat/renamed.md", tc.body)

			snap := mustSnapshot(t, root)
			if len(snap.candidates) != 1 {
				t.Fatalf("%s spec was not admitted: a %s delimiter must be recognised, or this repository's own specs stop being candidates", tc.name, tc.name)
			}
			if !snap.candidates[0].Unprovable {
				t.Fatalf("%s candidate is not Unprovable; the fixture parsed, so this subtest never exercised the carrier arm", tc.name)
			}
		})
	}
}

// The other direction, and the defect a whole-file byte scan reintroduces
// (REV-158 CLAUDE-002). Documentation that merely QUOTES the token must not
// become an Unprovable candidate that refuses every mutation in its folder.
// Files in this repository really do carry the literal line outside frontmatter.
func TestAdmission_CarrierOutsideTheLeadingRegionDoesNotAdmit(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			"fenced_block_in_readme",
			"# How specs look\n\n```yaml\n---\ntest_case_id: tc-55555555\n---\n```\n",
		},
		{
			"after_the_region_closed",
			"---\ntitle: notes\n---\n\ntest_case_id: tc-55555555\n",
		},
		{
			"no_leading_delimiter",
			"Some notes.\ntest_case_id: tc-55555555\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, dir := casesDirFor(t)
			writeSpecID(t, dir, "feat/tc-66666666-real.md", "tc-66666666")
			writeRaw(t, dir, "feat/NOTES.md", tc.body)

			snap := mustSnapshot(t, root)
			_, unprovable := snap.SelectFolder("gtms/test/cases/feat", false)
			if len(unprovable) != 0 {
				t.Fatalf("%q was admitted (%q): documentation quoting the token outside the leading region must never refuse a folder mutation",
					tc.name, unprovable[0].LogicalRel)
			}
			if err := snap.RequireFolderOwnership("gtms/test/cases/feat", false); err != nil {
				t.Fatalf("folder refused because of ordinary documentation: %v", err)
			}
		})
	}
}

// Admission is INDEPENDENT of any requested id (ADR-024 4a), and a valid renamed
// owner is admitted by the carrier arm and owns the id its body declares.
func TestAdmission_ValidRenamedOwnerIsAdmittedByTheCarrier(t *testing.T) {
	root, cases := casesDirFor(t)
	writeSpecID(t, cases, "beta/checkout.md", "tc-77777777")

	snap := mustSnapshot(t, root)
	if r := snap.Resolve("tc-77777777"); r.Outcome != OutcomeUniqueOwner {
		t.Fatalf("Resolve => %v, want UniqueOwner: every valid spec begins with a delimiter and carries the key, so the carrier arm admits it whatever its filename", r.Outcome)
	}
}

// The limit ADR-024 4a accepts EXPLICITLY, asserted so it stays a decision rather
// than becoming an undetected drift: a noncanonically-named file with no
// detectable carrier is attributable to nothing and is invisible to ownership,
// even in the selected scope. Widening this is ENH-100 / ENH-101, not BUG-185.
func TestAdmission_AcceptedLimit_NoEnvelopeAndNoCarrierIsInvisible(t *testing.T) {
	root, cases := casesDirFor(t)
	writeSpecID(t, cases, "feat/tc-88888888-real.md", "tc-88888888")
	writeRaw(t, cases, "feat/mystery.md", "\x00\x01 binary-ish, no delimiter, no carrier\n")

	snap := mustSnapshot(t, root)
	_, unprovable := snap.SelectFolder("gtms/test/cases/feat", false)
	if len(unprovable) != 0 {
		t.Fatalf("mystery.md became a candidate (%q); ADR-024 4a records it as the accepted blind spot, and admitting every in-scope .md is the README refusal it rejects", unprovable[0].LogicalRel)
	}
}
