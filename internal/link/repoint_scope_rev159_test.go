package link

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// REV-159 CODEX-002, CODEX-004 and CLAUDE-003 -- the bulk-repoint half of the
// round-four findings. All three are the same shape: repoint's scope set became
// parsed BODY identities, and something around it was left filename-derived.

func seedRepointProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	art := filepath.Join(root, "tests")
	if err := os.MkdirAll(art, 0o755); err != nil {
		t.Fatalf("mkdir tests: %v", err)
	}
	if err := os.WriteFile(filepath.Join(art, "a.bats"), []byte("@test x { true; }\n"), 0o644); err != nil {
		t.Fatalf("write artefact: %v", err)
	}
	return root
}

func writeSpecAt(t *testing.T, root, relPath, id string) {
	t.Helper()
	p := filepath.Join(root, "gtms", "test", "cases", filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: " + id + "\ntitle: \"t\"\n---\n\nbody\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
}

func writeOwnedWiring(t *testing.T, root, tcID string) string {
	t.Helper()
	return writeWiringRaw(t, root, tcID+"--bats.wiring.yaml",
		"testcase: "+tcID+"\ntestcase-hash: 1111222233334444\nframework: bats\nadapter: bats-runner\nartefact: tests/a.bats\nartefact-hash: aaaabbbbccccdddd\n")
}

// ---------------------------------------------------------------- CODEX-004

// A folder whose only spec is a VALIDLY RENAMED owner must be repointable. The
// filename walk this replaced recognised tc-*.md and nothing else, so such a
// folder reported "no test cases found" while every other bulk command operated
// on it happily -- body identity is authoritative (ADR-024 s1).
func TestRepointBulk_FolderWhoseOnlyOwnerHasANonTCFilename(t *testing.T) {
	root := seedRepointProject(t)
	writeSpecAt(t, root, "feat/login.md", "tc-11111111")
	writeOwnedWiring(t, root, "tc-11111111")

	summary, err := RepointBulk(root, "feat", false, withScopeAuth(t, root, "feat", false, RepointOptions{
		Config:      repointTestConfig(),
		FromAdapter: "bats-runner",
		NewAdapter:  "bats-new",
	}))
	if err != nil {
		t.Fatalf("RepointBulk refused a folder whose only owner is named login.md: %v\nfilename-derived spec discovery cannot decide whether a scope is empty", err)
	}
	if summary.Repointed != 1 {
		t.Fatalf("repointed %d records, want 1 (summary %+v)", summary.Repointed, summary)
	}
}

// The empty-scope decision still refuses, and still with the established wording.
func TestRepointBulk_GenuinelyEmptyFolderStillRefuses(t *testing.T) {
	root := seedRepointProject(t)
	if err := os.MkdirAll(filepath.Join(root, "gtms", "test", "cases", "empty"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	_, err := RepointBulk(root, "empty", false, withScopeAuth(t, root, "empty", false, RepointOptions{
		Config:      repointTestConfig(),
		FromAdapter: "bats-runner",
		NewAdapter:  "bats-new",
	}))
	if err == nil || !strings.Contains(err.Error(), "no test cases found in 'empty/'") {
		t.Fatalf("error = %v, want the established \"no test cases found in 'empty/'\" refusal", err)
	}
}

// ---------------------------------------------------------------- CODEX-002

// --all used to run the ownership preflight purely for its refusal side-effect
// and then select EVERY wiring record, so a record whose test-case ID has no
// spec at all was rewritten though the preflight never examined that ID. An
// empty cases tree full of orphan wiring passed and was mutated.
func TestRepointAll_RefusesOrphanWiringWithNoPhysicalOwner(t *testing.T) {
	root := seedRepointProject(t)
	if err := os.MkdirAll(filepath.Join(root, "gtms", "test", "cases"), 0o755); err != nil {
		t.Fatalf("mkdir cases: %v", err)
	}
	orphan := writeOwnedWiring(t, root, "tc-99999999") // no spec anywhere
	before := rawSHA256(t, orphan)

	_, err := RepointAll(root, withScopeAuth(t, root, "", true, RepointOptions{
		Config:      repointTestConfig(),
		FromAdapter: "bats-runner",
		NewAdapter:  "bats-new",
	}))
	if err == nil {
		t.Fatal("RepointAll rewrote wiring for an identity with no physical owner: ADR-024 requires a wiring write to mutate only a uniquely proven owner")
	}
	if !strings.Contains(err.Error(), "tc-99999999--bats.wiring.yaml") {
		t.Fatalf("refusal does not name the orphan record: %v", err)
	}
	if got := rawSHA256(t, orphan); got != before {
		t.Fatal("the orphan record was mutated despite the refusal; --all is all-or-nothing")
	}
}

// The converse: --all still works when every record has a proven owner.
func TestRepointAll_ProceedsWhenEveryRecordHasAProvenOwner(t *testing.T) {
	root := seedRepointProject(t)
	writeSpecAt(t, root, "feat/tc-11111111-login.md", "tc-11111111")
	writeSpecAt(t, root, "other/renamed.md", "tc-22222222")
	writeOwnedWiring(t, root, "tc-11111111")
	writeOwnedWiring(t, root, "tc-22222222")

	summary, err := RepointAll(root, withScopeAuth(t, root, "", true, RepointOptions{
		Config:      repointTestConfig(),
		FromAdapter: "bats-runner",
		NewAdapter:  "bats-new",
	}))
	if err != nil {
		t.Fatalf("RepointAll refused a project where every record has a proven owner: %v", err)
	}
	if summary.Repointed != 2 {
		t.Fatalf("repointed %d, want 2 (summary %+v)", summary.Repointed, summary)
	}
}

// ---------------------------------------------------------------- CLAUDE-003

// A corrupt wiring record belonging to an EXTENDED in-scope id must be reported,
// not silently dropped. ParseFilename splits the stem at the FIRST "--", so
// tc-33333333--legacy--bats.wiring.yaml yielded tc-33333333 -- which is not the
// in-scope owner tc-33333333--legacy, so the record vanished and the folder
// repoint reported success with zero errors. Malformed is never absence
// (ADR-024 s1), least of all on a mutating consumer.
func TestRepointBulk_CorruptRecordOfAnExtendedIDIsNotDropped(t *testing.T) {
	root := seedRepointProject(t)
	writeSpecAt(t, root, "feat/tc-33333333--legacy-spec.md", "tc-33333333--legacy")
	writeWiringRaw(t, root, "tc-33333333--legacy--bats.wiring.yaml", "testcase: [unclosed\n")

	summary, err := RepointBulk(root, "feat", false, withScopeAuth(t, root, "feat", false, RepointOptions{
		Config:      repointTestConfig(),
		FromAdapter: "bats-runner",
		NewAdapter:  "bats-new",
	}))
	if err != nil {
		t.Fatalf("unexpected operation error: %v", err)
	}
	if summary.Errors == 0 {
		t.Fatalf("corrupt wiring for the in-scope owner tc-33333333--legacy was silently dropped and the repoint reported success (summary %+v): a first-\"--\" filename split attributes it to tc-33333333, which is not the owner", summary)
	}
}

// The other direction of the same defect. A corrupt record belonging to the
// EXTENDED id must NOT be charged to the unrelated shorter id when the shorter
// one is what is in scope -- that is the cross-identity coupling BUG-188 exists
// to remove. tc-33333333 and tc-33333333--legacy are different test cases.
func TestRepointBulk_CorruptRecordOfAnotherIDIsNotChargedToTheShorterOne(t *testing.T) {
	root := seedRepointProject(t)
	writeSpecAt(t, root, "feat/tc-33333333-login.md", "tc-33333333")
	writeSpecAt(t, root, "elsewhere/tc-33333333--legacy-spec.md", "tc-33333333--legacy")
	writeOwnedWiring(t, root, "tc-33333333")
	// Corrupt, and belongs to tc-33333333--legacy, which is NOT in the feat scope.
	writeWiringRaw(t, root, "tc-33333333--legacy--bats.wiring.yaml", "testcase: [unclosed\n")

	summary, err := RepointBulk(root, "feat", false, withScopeAuth(t, root, "feat", false, RepointOptions{
		Config:      repointTestConfig(),
		FromAdapter: "bats-runner",
		NewAdapter:  "bats-new",
	}))
	if err != nil {
		t.Fatalf("unexpected operation error: %v", err)
	}
	if summary.Errors != 0 {
		t.Fatalf("a corrupt record belonging to tc-33333333--legacy was charged to the unrelated in-scope tc-33333333 (summary %+v): the repoint exits non-zero blaming an identity that has nothing to do with it", summary)
	}
	if summary.Repointed != 1 {
		t.Fatalf("repointed %d, want 1: the in-scope owner's own record must still be repointed", summary.Repointed)
	}
}
