package testcase

import (
	"path/filepath"
	"testing"
)

// REV-158 CLAUDE-002: an ordinary documentation file in a cases folder is not a
// spec candidate, so it can never become Unprovable and can never refuse a
// folder-scoped mutation. Before the fix every *.md under gtms/test/cases/ was
// admitted, so a plain README.md made delete, reset, automate, execute and
// link --refresh all fail closed on the whole folder.
func TestSnapshot_OrdinaryDocumentationIsNotAnOwnershipCandidate(t *testing.T) {
	documents := map[string]string{
		"README.md": "---\ntype: Reference\n---\n\n# Notes\n",
		"NOTES.md":  "# Notes\n\nHow this folder works.\n",
		"guide.md":  "---\ntitle: Author guide\n---\n\nNo testcase identity.\n",
	}
	for name, body := range documents {
		t.Run(name, func(t *testing.T) {
			root, cases := casesDirFor(t)
			writeSpecID(t, cases, "feat/tc-11111111-login.md", "tc-11111111")
			writeRaw(t, cases, "feat/"+name, body)

			snap := mustSnapshot(t, root)

			ids, unprovable := snap.SelectFolder("gtms/test/cases/feat", false)
			if len(unprovable) != 0 {
				t.Fatalf("SelectFolder reported %d unprovable candidate(s) (%q): %s must not enter the ownership model",
					len(unprovable), unprovable[0].LogicalRel, name)
			}
			if len(ids) != 1 || ids[0] != "tc-11111111" {
				t.Fatalf("SelectFolder ids => %v, want [tc-11111111]", ids)
			}
			if err := snap.RequireFolderOwnership("gtms/test/cases/feat", false); err != nil {
				t.Fatalf("RequireFolderOwnership refused the folder because of %s: %v", name, err)
			}
			if r := snap.Resolve("tc-11111111"); r.Outcome != OutcomeUniqueOwner {
				t.Fatalf("Resolve => %v, want UniqueOwner", r.Outcome)
			}
		})
	}
}

// REV-158 CLAUDE-002, the other half: admission must not be keyed on the
// requested id, or the renamed owners ADR-024 s4 expressly permits would stop
// being owners. A spec whose filename encodes a DIFFERENT id, and one whose
// filename is not id-shaped at all, both still own the id their body declares.
func TestSnapshot_RenamedOwnerRemainsAnOwner(t *testing.T) {
	root, cases := casesDirFor(t)
	writeSpecID(t, cases, "beta/tc-deadbeef-renamed.md", "tc-a1b2c3d4")
	writeSpecID(t, cases, "gamma/login-spec.md", "tc-c0ffee11")

	snap := mustSnapshot(t, root)

	r := snap.Resolve("tc-a1b2c3d4")
	if r.Outcome != OutcomeUniqueOwner || filepath.Base(r.OwnerLogicalRel) != "tc-deadbeef-renamed.md" {
		t.Fatalf("tc-a1b2c3d4 => %v %q, want UniqueOwner tc-deadbeef-renamed.md",
			r.Outcome, r.OwnerLogicalRel)
	}
	r = snap.Resolve("tc-c0ffee11")
	if r.Outcome != OutcomeUniqueOwner || filepath.Base(r.OwnerLogicalRel) != "login-spec.md" {
		t.Fatalf("tc-c0ffee11 => %v %q, want UniqueOwner login-spec.md (body identity is authoritative)",
			r.Outcome, r.OwnerLogicalRel)
	}
}

// A file that IS named like a spec but declares no identity still fails closed:
// the admission rule narrows Unprovable to plausible specs, it does not make
// malformed specs invisible (ADR-024's accepted brownfield cost).
func TestSnapshot_SpecShapedFileWithoutIDIsStillUnprovable(t *testing.T) {
	root, cases := casesDirFor(t)
	writeSpecID(t, cases, "feat/tc-11111111-login.md", "tc-11111111")
	writeRaw(t, cases, "feat/tc-22222222-noid.md", "---\ntitle: \"t\"\n---\nbody\n")

	snap := mustSnapshot(t, root)

	_, unprovable := snap.SelectFolder("gtms/test/cases/feat", false)
	if len(unprovable) != 1 {
		t.Fatalf("SelectFolder unprovable => %d, want 1 (a tc-named spec with no id must fail closed)", len(unprovable))
	}
	if unprovable[0].Reason != ReasonMissingID {
		t.Errorf("reason => %v, want ReasonMissingID", unprovable[0].Reason)
	}
}

// A README whose frontmatter DOES declare a test_case_id key is a spec by the
// declaring-frontmatter signal, so it still fails closed. The admission rule is
// "is this structurally a spec", not "is this named tc-*".
func TestSnapshot_NonSpecNameDeclaringIDKeyIsAdmitted(t *testing.T) {
	root, cases := casesDirFor(t)
	writeRaw(t, cases, "feat/README.md", "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: \"\"\ntitle: \"t\"\n---\nbody\n")

	snap := mustSnapshot(t, root)
	_, unprovable := snap.SelectFolder("gtms/test/cases/feat", false)
	if len(unprovable) != 1 {
		t.Fatalf("unprovable => %d, want 1: a file declaring a blank test_case_id is a spec that fails closed", len(unprovable))
	}
	if unprovable[0].Reason != ReasonMissingID {
		t.Errorf("reason => %v, want ReasonMissingID", unprovable[0].Reason)
	}
}
