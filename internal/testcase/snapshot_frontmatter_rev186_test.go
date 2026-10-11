package testcase

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"
)

// REV-186 CODEX-001: the owner's retained frontmatter is an immutable string, so a
// caller cannot corrupt the snapshot through what Resolve returned. Two Resolve
// calls on one snapshot must return identical content, and that content must be the
// file's frontmatter region.
func TestSnapshotOwnerFrontmatter_REV186_ImmutableAcrossResolves(t *testing.T) {
	root, cases := casesDirFor(t)
	writeSpecID(t, cases, "feat/tc-11111111-real.md", "tc-11111111")

	snap := mustSnapshot(t, root)
	first := snap.Resolve("tc-11111111")
	if first.Outcome != OutcomeUniqueOwner {
		t.Fatalf("outcome = %v, want UniqueOwner", first.Outcome)
	}
	want := "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-11111111\ntitle: \"t\"\n---\n"
	if first.OwnerFrontmatter != want {
		t.Fatalf("OwnerFrontmatter = %q, want %q", first.OwnerFrontmatter, want)
	}
	// A caller that derives and mutates a byte copy cannot reach the snapshot.
	b := []byte(first.OwnerFrontmatter)
	for i := range b {
		b[i] = 'X'
	}
	second := snap.Resolve("tc-11111111")
	if second.OwnerFrontmatter != want {
		t.Fatalf("second Resolve OwnerFrontmatter = %q, want %q", second.OwnerFrontmatter, want)
	}
}

// REV-186 CODEX-002: aliases of one canonical physical file retain ONE frontmatter
// value, not one per logical path.
func TestSnapshotOwnerFrontmatter_REV186_AliasesShareOneValue(t *testing.T) {
	root, cases := casesDirFor(t)
	real := writeSpecID(t, cases, "feat/tc-11111111-real.md", "tc-11111111")
	for _, alias := range []string{"a/tc-11111111-alias.md", "b/tc-11111111-alias2.md"} {
		p := filepath.Join(cases, filepath.FromSlash(alias))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.Symlink(real, p); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
	}

	snap := mustSnapshot(t, root)
	var owners []Candidate
	for _, c := range snap.candidates {
		if c.BodyID == "tc-11111111" {
			owners = append(owners, c)
		}
	}
	if len(owners) != 3 {
		t.Fatalf("got %d candidates for tc-11111111, want 3 (real + 2 aliases)", len(owners))
	}
	canon := owners[0].CanonicalRel
	ptr := unsafe.StringData(owners[0].Frontmatter)
	for _, c := range owners {
		if c.CanonicalRel != canon {
			t.Fatalf("candidate %s canonical %s, want %s", c.LogicalRel, c.CanonicalRel, canon)
		}
		if !strings.HasPrefix(c.Frontmatter, "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-11111111") {
			t.Fatalf("candidate %s frontmatter = %q", c.LogicalRel, c.Frontmatter)
		}
		if unsafe.StringData(c.Frontmatter) != ptr {
			t.Fatalf("candidate %s retains its own frontmatter copy; aliases of one canonical file must share one value", c.LogicalRel)
		}
	}
}
