package adapter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
)

// F3(b): a uniquely owned RENAMED spec (filename id != body id, which ADR-024 s1
// permits) must reach every downstream consumer correctly. Under the threaded
// design that is now true by construction rather than by each consumer
// re-resolving successfully: orchestration resolves the owner ONCE and adapter
// input, post-fill validation and hashing all consume that same value.
func TestRenamedOwner_ReachesAdapterInputValidationAndHashing(t *testing.T) {
	root := t.TempDir()
	renamedRel := "gtms/test/cases/beta/tc-deadbeef-renamed.md"
	renamedAbs := filepath.Join(root, filepath.FromSlash(renamedRel))
	if err := os.MkdirAll(filepath.Dir(renamedAbs), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\ntest_case_id: tc-a1b2c3d4\ntitle: Renamed owner\n---\n\n## Test Objective\nx\n"
	if err := os.WriteFile(renamedAbs, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	// The ONE resolution an operation performs.
	owner, err := testcase.ResolveOwner(root, "tc-a1b2c3d4")
	if err != nil {
		t.Fatalf("resolving the renamed owner: %v", err)
	}
	if owner.SpecRel() != renamedRel {
		t.Fatalf("resolved owner = %q, want %q", owner.SpecRel(), renamedRel)
	}

	// (1) Adapter input: the source handed to the adapter is the renamed owner.
	if src := ownerSource(owner); src != renamedRel {
		t.Fatalf("adapter input: source = %q, want %q", src, renamedRel)
	}

	// (2) Post-fill validation is REACHED and passes (no false violation for a
	// renamed owner). The old filename walk never found a file named tc-a1b2c3d4*
	// and silently returned nil by BYPASS; the gate now reads the owner it is given.
	if v := ValidateTestCaseOwner(root, owner); len(v) != 0 {
		t.Fatalf("post-fill validation of a valid renamed owner must pass, got %v", v)
	}

	// (3) Hashing uses the same owner, so the hash is of the renamed file on disk.
	got, err := pipeline.HashFile(renamedAbs)
	if err != nil {
		t.Fatal(err)
	}
	viaOwner, err := pipeline.HashFile(testcase.OwnerAbsFor(root, owner))
	if err != nil {
		t.Fatal(err)
	}
	if got != viaOwner {
		t.Fatalf("hash via resolved owner %q != hash of renamed file %q", viaOwner, got)
	}
}

// F3(b) discriminator: a renamed owner with MALFORMED frontmatter must be caught
// by the reached validation (it is not silently bypassed). This is caught upstream
// as Unprovable, but the post-fill fallback must not paper over a parse failure.
func TestRenamedOwner_MissingIDStillCaughtIfReachedDirectly(t *testing.T) {
	root := t.TempDir()
	// A spec whose filename does not shortlist the target and whose body has no id.
	p := filepath.Join(root, "gtms/test/cases/beta/tc-deadbeef-noid.md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("---\ntitle: no id\n---\nx\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Resolving tc-a1b2c3d4 yields no owner, so there is nothing for the gate to
	// validate -- not-found is handled by the command's own wording, not as a
	// post-fill violation. This asserts the gate does not invent one.
	owner, err := testcase.ResolveOwner(root, "tc-a1b2c3d4")
	if err == nil {
		t.Fatalf("expected no owner for tc-a1b2c3d4, got %q", owner.SpecRel())
	}
	if v := ValidateTestCaseOwner(root, nil); len(v) != 0 {
		t.Fatalf("no owner => no post-fill violation, got %v", v)
	}
}
