package wiring

import "testing"

// REV-158 CLAUDE-005: FindAllForTC selected wiring by a bare filename prefix, so
// tc-77777777--legacy--bats.wiring.yaml was returned for tc-77777777. `gtms
// execute tc-77777777` then refused with "has multiple wiring records (bats,
// bats). Re-run with --framework <name>" -- a message naming the same framework
// twice, with no way for the user to select the base test case at all. The same
// selector feeds bulk execute's framework resolution and triage.
//
// Both directions are pinned: neither id's wiring may leak into the other's.
func TestFindAllForTC_ExtendedIDSiblingDoesNotLeak(t *testing.T) {
	root := t.TempDir()
	base := "testcase: tc-77777777\ntestcase-hash: 1111111111111111\nframework: bats\nadapter: bats-runner\nartefact: test/base.bats\nartefact-hash: 2222222222222222\n"
	legacy := "testcase: tc-77777777--legacy\ntestcase-hash: 3333333333333333\nframework: bats\nadapter: bats-runner\nartefact: test/legacy.bats\nartefact-hash: 4444444444444444\n"
	writeWiringFile(t, root, "tc-77777777--bats.wiring.yaml", base)
	writeWiringFile(t, root, "tc-77777777--legacy--bats.wiring.yaml", legacy)

	got, err := FindAllForTC(root, "tc-77777777")
	if err != nil {
		t.Fatalf("FindAllForTC: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("FindAllForTC(tc-77777777) => %d records, want 1: the extended-id sibling belongs to tc-77777777--legacy", len(got))
	}
	if got[0].Artefact != "test/base.bats" {
		t.Errorf("artefact => %q, want test/base.bats", got[0].Artefact)
	}

	got, err = FindAllForTC(root, "tc-77777777--legacy")
	if err != nil {
		t.Fatalf("FindAllForTC: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("FindAllForTC(tc-77777777--legacy) => %d records, want 1", len(got))
	}
	if got[0].Artefact != "test/legacy.bats" {
		t.Errorf("artefact => %q, want test/legacy.bats", got[0].Artefact)
	}
}

// A record whose body declares an identity its filename does not imply belongs
// to that other identity, never to the id whose prefix the filename happens to
// carry (ADR-024 s1: body identity is authoritative).
func TestFindAllForTC_ForeignBodyIdentityIsNotSelected(t *testing.T) {
	root := t.TempDir()
	foreign := "testcase: tc-99999999\ntestcase-hash: 1111111111111111\nframework: bats\nadapter: bats-runner\nartefact: test/x.bats\nartefact-hash: 2222222222222222\n"
	writeWiringFile(t, root, "tc-77777777--bats.wiring.yaml", foreign)

	got, err := FindAllForTC(root, "tc-77777777")
	if err != nil {
		t.Fatalf("FindAllForTC: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("FindAllForTC => %d records, want 0: the record declares tc-99999999", len(got))
	}
}
