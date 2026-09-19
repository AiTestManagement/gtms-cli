package testcase

import (
	"path/filepath"
	"testing"
)

// REV-158 CLAUDE-003: Resolve's relevance shortlist for Unprovable candidates
// used a bare filename prefix, so an unreadable extended-ID sibling
// (tc-11111111--legacy.md) was treated as relevant to the unrelated shorter id
// tc-11111111 and refused every operation on it. That is the cross-identity
// coupling BUG-188 exists to remove; the wiring side of the same change had
// already avoided it.
//
// Both directions are pinned: the shorter id must not be blocked by the longer
// one's corruption, and the longer id must not be blocked by the shorter one's.
func TestResolve_ExtendedIDSiblingCorruptionIsNotRelevantToShorterID(t *testing.T) {
	root, cases := casesDirFor(t)
	writeSpecID(t, cases, "feat/tc-11111111-login.md", "tc-11111111")
	writeRaw(t, cases, "feat/tc-11111111--legacy.md", "---\ntest_case_id: [unclosed\n---\nbody\n")

	snap := mustSnapshot(t, root)

	r := snap.Resolve("tc-11111111")
	if r.Outcome != OutcomeUniqueOwner {
		t.Fatalf("Resolve(tc-11111111) => %v (unprovable path %q), want UniqueOwner: a malformed tc-11111111--legacy.md belongs to a DIFFERENT test case",
			r.Outcome, r.UnprovablePath)
	}
	if filepath.Base(r.OwnerLogicalRel) != "tc-11111111-login.md" {
		t.Errorf("owner => %q, want tc-11111111-login.md", r.OwnerLogicalRel)
	}
	// The corrupt file is still relevant to its OWN id, which must fail closed.
	if r := snap.Resolve("tc-11111111--legacy"); r.Outcome != OutcomeUnprovable {
		t.Errorf("Resolve(tc-11111111--legacy) => %v, want Unprovable (its own file is malformed)", r.Outcome)
	}
}

func TestResolve_ShorterIDCorruptionIsNotRelevantToExtendedID(t *testing.T) {
	root, cases := casesDirFor(t)
	writeSpecID(t, cases, "feat/tc-11111111--legacy-sibling.md", "tc-11111111--legacy")
	writeRaw(t, cases, "feat/tc-11111111-login.md", "---\ntest_case_id: [unclosed\n---\nbody\n")

	snap := mustSnapshot(t, root)

	r := snap.Resolve("tc-11111111--legacy")
	if r.Outcome != OutcomeUniqueOwner {
		t.Fatalf("Resolve(tc-11111111--legacy) => %v (unprovable path %q), want UniqueOwner: a malformed tc-11111111-login.md belongs to a DIFFERENT test case",
			r.Outcome, r.UnprovablePath)
	}
	if r := snap.Resolve("tc-11111111"); r.Outcome != OutcomeUnprovable {
		t.Errorf("Resolve(tc-11111111) => %v, want Unprovable (its own file is malformed)", r.Outcome)
	}
}
