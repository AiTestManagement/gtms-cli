package testcase

import (
	"testing"
)

// REV-158 CODEX-004: a persisted body identity is validated EXACTLY as authored.
// Lowercasing and trimming used to run BEFORE the grammar check, so a spec
// declaring "TC-ABCD1234" or " tc-abcd1234 " was silently normalised into the
// valid owner tc-abcd1234 -- letting a mutating command act under an identity the
// file does not declare and bypassing the ADR-024 s6 positive-grammar refusal.
func TestParseBodyIdentity_AuthoredIdentityValidatedBeforeNormalisation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		relName  string
		authored string
		lookup   string
	}{
		{"uppercase", "u/tc-abcd1234-upper.md", "TC-ABCD1234", "tc-abcd1234"},
		{"whitespace", "u/tc-abcd1234-space.md", "\" tc-abcd1234 \"", "tc-abcd1234"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, cases := casesDirFor(t)
			writeRaw(t, cases, tc.relName, "---\ntest_case_id: "+tc.authored+"\ntitle: \"t\"\n---\nbody\n")

			snap := mustSnapshot(t, root)

			r := snap.Resolve(tc.lookup)
			if r.Outcome != OutcomeUnprovable {
				t.Fatalf("Resolve(%q) => %v, want Unprovable: a body id of %s is not the valid id %s",
					tc.lookup, r.Outcome, tc.authored, tc.lookup)
			}
			if r.Reason != ReasonGrammarInvalid {
				t.Errorf("reason => %v, want ReasonGrammarInvalid", r.Reason)
			}
			// And it must never be reachable as an owner under the normalised id.
			if _, err := snap.RequireOwner(tc.lookup); err == nil {
				t.Errorf("RequireOwner(%q) succeeded: a normalised body id must not grant ownership", tc.lookup)
			}
		})
	}
}
