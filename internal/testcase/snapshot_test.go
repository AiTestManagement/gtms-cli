package testcase

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// writeSpecID writes a spec file with the given body test_case_id.
func writeSpecID(t *testing.T, casesDir, relName, id string) string {
	t.Helper()
	path := filepath.Join(casesDir, filepath.FromSlash(relName))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := "---\ntest_case_id: " + id + "\ntitle: \"t\"\n---\n\nbody\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	return path
}

// writeRaw writes an arbitrary spec body (for malformed / missing-id fixtures).
func writeRaw(t *testing.T, casesDir, relName, body string) string {
	t.Helper()
	path := filepath.Join(casesDir, filepath.FromSlash(relName))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write raw: %v", err)
	}
	return path
}

func casesDirFor(t *testing.T) (root, casesDir string) {
	t.Helper()
	root = t.TempDir()
	casesDir = filepath.Join(root, "gtms", "test", "cases")
	if err := os.MkdirAll(casesDir, 0o755); err != nil {
		t.Fatalf("mkdir cases: %v", err)
	}
	return root, casesDir
}

func mustSnapshot(t *testing.T, root string) *Snapshot {
	t.Helper()
	snap, err := NewSnapshot(root)
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}
	return snap
}

// tc-589314f1: prefix sibling resolves to its own body owner, not the longer one.
func TestResolve_PrefixSiblingResolvesToOwnBodyOwner(t *testing.T) {
	root, cases := casesDirFor(t)
	writeSpecID(t, cases, "checkout/tc-77777777-owner.md", "tc-77777777")
	writeSpecID(t, cases, "checkout/tc-77777777--legacy-sibling.md", "tc-77777777--legacy")

	snap := mustSnapshot(t, root)

	r := snap.Resolve("tc-77777777")
	if r.Outcome != OutcomeUniqueOwner || filepath.Base(r.OwnerLogicalRel) != "tc-77777777-owner.md" {
		t.Fatalf("tc-77777777 => %v %q, want UniqueOwner tc-77777777-owner.md", r.Outcome, r.OwnerLogicalRel)
	}
	r = snap.Resolve("tc-77777777--legacy")
	if r.Outcome != OutcomeUniqueOwner || filepath.Base(r.OwnerLogicalRel) != "tc-77777777--legacy-sibling.md" {
		t.Fatalf("tc-77777777--legacy => %v %q, want the sibling", r.Outcome, r.OwnerLogicalRel)
	}
}

// tc-589314f1 step 4: with ONLY the longer spec present, the shorter id is NotFound,
// never silently resolved to the sibling (rejects "prefer longest filename").
func TestResolve_OnlyLongerPresent_ShorterIsNotFound(t *testing.T) {
	root, cases := casesDirFor(t)
	writeSpecID(t, cases, "checkout/tc-77777777--legacy-sibling.md", "tc-77777777--legacy")

	snap := mustSnapshot(t, root)
	if r := snap.Resolve("tc-77777777"); r.Outcome != OutcomeNotFound {
		t.Fatalf("tc-77777777 => %v, want NotFound", r.Outcome)
	}
}

// tc-dbc64e99 / tc-81893058: a renamed second owner whose filename does NOT
// shortlist the id is found by body identity -> Ambiguous naming both.
func TestResolve_RenamedDuplicateOwnerIsAmbiguous(t *testing.T) {
	root, cases := casesDirFor(t)
	writeSpecID(t, cases, "alpha/tc-a1b2c3d4-first.md", "tc-a1b2c3d4")
	writeSpecID(t, cases, "beta/tc-deadbeef-renamed-owner.md", "tc-a1b2c3d4")

	snap := mustSnapshot(t, root)
	r := snap.Resolve("tc-a1b2c3d4")
	if r.Outcome != OutcomeAmbiguous || len(r.Owners) != 2 {
		t.Fatalf("=> %v owners=%v, want Ambiguous with 2 owners", r.Outcome, r.Owners)
	}
	// Both physical owners named, including the renamed one filenames can't shortlist.
	joined := r.Owners[0] + "|" + r.Owners[1]
	if !contains(joined, "tc-a1b2c3d4-first.md") || !contains(joined, "tc-deadbeef-renamed-owner.md") {
		t.Fatalf("owners %v must name both physical owners", r.Owners)
	}
}

// tc-a10e1070: a real spec plus an in-tree symlink alias to it count as ONE owner.
func TestResolve_AliasCountsAsOneOwner(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink alias test requires POSIX symlink semantics")
	}
	root, cases := casesDirFor(t)
	real := writeSpecID(t, cases, "real/tc-11111111-owner.md", "tc-11111111")
	aliasDir := filepath.Join(cases, "aliases")
	if err := os.MkdirAll(aliasDir, 0o755); err != nil {
		t.Fatalf("mkdir alias: %v", err)
	}
	if err := os.Symlink(real, filepath.Join(aliasDir, "tc-11111111-alias.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	snap := mustSnapshot(t, root)
	if r := snap.Resolve("tc-11111111"); r.Outcome != OutcomeUniqueOwner {
		t.Fatalf("alias+real => %v, want UniqueOwner (one physical owner)", r.Outcome)
	}

	// Control: replace alias with a real copy -> two physical owners -> Ambiguous.
	if err := os.Remove(filepath.Join(aliasDir, "tc-11111111-alias.md")); err != nil {
		t.Fatalf("rm alias: %v", err)
	}
	writeSpecID(t, cases, "aliases/tc-11111111-copy.md", "tc-11111111")
	snap = mustSnapshot(t, root)
	if r := snap.Resolve("tc-11111111"); r.Outcome != OutcomeAmbiguous {
		t.Fatalf("two real copies => %v, want Ambiguous", r.Outcome)
	}
}

// tc-d11c3c91: a cross-folder spec symlink does NOT confer its target's folder
// membership. SelectFolder(alpha) must not include the linked target's id.
func TestSelectFolder_CrossFolderSymlinkDoesNotConferMembership(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink membership test requires POSIX symlink semantics")
	}
	root, cases := casesDirFor(t)
	writeSpecID(t, cases, "alpha/tc-11111111-alpha.md", "tc-11111111")
	betaReal := writeSpecID(t, cases, "beta/tc-22222222-beta.md", "tc-22222222")
	if err := os.Symlink(betaReal, filepath.Join(cases, "alpha", "tc-22222222-linked.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	snap := mustSnapshot(t, root)
	ids, unprov := snap.SelectFolder("gtms/test/cases/alpha", true)
	if len(unprov) != 0 {
		t.Fatalf("unexpected unprovable in alpha: %v", unprov)
	}
	if len(ids) != 1 || ids[0] != "tc-11111111" {
		t.Fatalf("SelectFolder(alpha) => %v, want only tc-11111111 (link must not pull tc-22222222 in)", ids)
	}
}

// All four Unprovable variants classify as Unprovable, none as NotFound or a
// proven non-owner. (BUG-185 closure requirement.)
func TestResolve_AllFourUnprovableVariants(t *testing.T) {
	root, cases := casesDirFor(t)
	// missing id (well-formed YAML, no test_case_id)
	writeRaw(t, cases, "u/tc-aaaaaaaa-missing.md", "---\ntitle: \"t\"\nrequirement: X\n---\nbody\n")
	// malformed frontmatter
	writeRaw(t, cases, "u/tc-bbbbbbbb-malformed.md", "---\ntest_case_id: [unclosed\ntitle:\n---\nbody\n")
	// grammar-invalid id
	writeRaw(t, cases, "u/tc-cccccccc-badid.md", "---\ntest_case_id: \"tc-safe&calc\"\n---\nbody\n")
	// unreadable regular file -- see the capability probe below. Permissions are
	// restored by Cleanup whatever happens, including on a failed assertion, so a
	// t.TempDir teardown is never blocked by a 0000 file.
	unreadable := writeSpecID(t, cases, "u/tc-dddddddd-unreadable.md", "tc-dddddddd")
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o644) })
	unreadableProvable := true
	var unreadableWhy string
	if err := os.Chmod(unreadable, 0o000); err != nil {
		unreadableProvable, unreadableWhy = false, fmt.Sprintf("chmod is not supported here (%v)", err)
	} else if _, err := os.ReadFile(unreadable); err == nil {
		// The permission change was accepted and the file is STILL readable:
		// Windows, a root process, and some mounts all behave this way. The
		// fixture cannot be built, so the row cannot be evidence either way.
		unreadableProvable, unreadableWhy = false, "the file is still readable after chmod 0000"
	}

	snap := mustSnapshot(t, root)

	cases2 := []struct {
		id     string
		reason UnprovableReason
	}{
		{"tc-aaaaaaaa", ReasonMissingID},
		{"tc-bbbbbbbb", ReasonMalformed},
		{"tc-cccccccc", ReasonGrammarInvalid},
	}
	for _, c := range cases2 {
		r := snap.Resolve(c.id)
		if r.Outcome != OutcomeUnprovable {
			t.Errorf("%s => %v, want Unprovable", c.id, r.Outcome)
			continue
		}
		if r.Reason != c.reason {
			t.Errorf("%s reason => %v, want %v", c.id, r.Reason, c.reason)
		}
	}
	// The unreadable row runs only where the fixture could actually be built.
	// This used to gate on os.Geteuid() != 0, which is a ROOT check, not a
	// CAPABILITY check: Geteuid returns -1 on Windows, so the row ran there,
	// chmod 0000 did not prevent reading, and the resolver correctly reported a
	// proven owner -- failing an assertion about a condition the host cannot
	// express. The other three variants are unaffected and always run.
	if unreadableProvable {
		if r := snap.Resolve("tc-dddddddd"); r.Outcome != OutcomeUnprovable || r.Reason != ReasonUnreadable {
			t.Errorf("unreadable => %v/%v, want Unprovable/Unreadable", r.Outcome, r.Reason)
		}
	} else {
		t.Logf("unreadable variant NOT EVIDENCED on this host: %s. "+
			"The other three Unprovable variants ran. A POSIX host (Ubuntu CI) is the positive "+
			"evidence that an unreadable spec classifies as Unprovable/Unreadable.", unreadableWhy)
	}
}

// An unrelated malformed file outside the shortlist and scope does not block a
// resolvable id (ADR-024 relevance rule).
func TestResolve_UnrelatedMalformedDoesNotBlock(t *testing.T) {
	root, cases := casesDirFor(t)
	writeSpecID(t, cases, "good/tc-99999999-owner.md", "tc-99999999")
	writeRaw(t, cases, "junk/tc-zzzzzzzz-malformed.md", "---\ntest_case_id: [bad\n---\n")

	snap := mustSnapshot(t, root)
	if r := snap.Resolve("tc-99999999"); r.Outcome != OutcomeUniqueOwner {
		t.Fatalf("tc-99999999 => %v, want UniqueOwner despite unrelated malformed file", r.Outcome)
	}
}

// A prefix sibling that is readable, valid, and DIFFERENT is a proven non-owner:
// excluded, never Unprovable (the BUG-188 headline in resolver form).
func TestResolve_ValidDifferentSiblingIsProvenNonOwner(t *testing.T) {
	root, cases := casesDirFor(t)
	writeSpecID(t, cases, "checkout/tc-77777777-owner.md", "tc-77777777")
	writeSpecID(t, cases, "checkout/tc-77777777--legacy-sibling.md", "tc-77777777--legacy")

	snap := mustSnapshot(t, root)
	if r := snap.Resolve("tc-77777777"); r.Outcome != OutcomeUniqueOwner {
		t.Fatalf("=> %v, want UniqueOwner (sibling is a proven non-owner, not Unprovable)", r.Outcome)
	}
}

func TestValidExecutionID_Grammar(t *testing.T) {
	valid := []string{"tc-77777777", "tc-a1b2c3d4", "tc-77777777--legacy", "tc-deadbeef--v2--x"}
	invalid := []string{"tc-safe&calc", "tc-77777777; rm -rf", "tc-../escape", "tc-with space", "tc-", "notc-1234", "TC-ABCD"}
	for _, id := range valid {
		if !ValidExecutionID(id) {
			t.Errorf("ValidExecutionID(%q) = false, want true", id)
		}
	}
	for _, id := range invalid {
		if ValidExecutionID(id) {
			t.Errorf("ValidExecutionID(%q) = true, want false", id)
		}
	}
}

// RequireOwnership / RequireFolderOwnership back the automate/execute/prime and
// triage/repoint refusals. UniqueOwner and NotFound pass; Ambiguous and
// Unprovable refuse.
func TestRequireOwnership_Outcomes(t *testing.T) {
	root, cases := casesDirFor(t)
	writeSpecID(t, cases, "control/tc-33333333-unique.md", "tc-33333333")
	writeSpecID(t, cases, "alpha/tc-11111111-owner.md", "tc-11111111")
	writeSpecID(t, cases, "beta/tc-22222222-renamed.md", "tc-11111111") // renamed duplicate
	writeRaw(t, cases, "gamma/tc-b2c3d4e5-noid.md", "---\ntitle: x\n---\n")

	snap := mustSnapshot(t, root)

	if err := snap.RequireOwnership("tc-33333333"); err != nil {
		t.Errorf("unique owner must pass: %v", err)
	}
	if err := snap.RequireOwnership("tc-nonexistent"); err != nil {
		t.Errorf("not-found must pass (handled by existence wording): %v", err)
	}
	if err := snap.RequireOwnership("tc-11111111"); err == nil {
		t.Errorf("ambiguous (renamed duplicate) must refuse")
	}
	if err := snap.RequireOwnership("tc-b2c3d4e5"); err == nil {
		t.Errorf("unprovable (missing id) must refuse")
	}
}

func TestRequireFolderOwnership_Outcomes(t *testing.T) {
	root, cases := casesDirFor(t)
	// alpha holds a duplicate whose second owner lives in beta (outside alpha).
	writeSpecID(t, cases, "alpha/tc-a1b2c3d4-first.md", "tc-a1b2c3d4")
	writeSpecID(t, cases, "beta/tc-deadbeef-renamed.md", "tc-a1b2c3d4")
	// gamma holds an unprovable spec.
	writeRaw(t, cases, "gamma/tc-b2c3d4e5-noid.md", "---\ntitle: x\n---\n")
	// solo holds a clean unique owner.
	writeSpecID(t, cases, "solo/tc-99999999-only.md", "tc-99999999")

	snap := mustSnapshot(t, root)

	if err := snap.RequireFolderOwnership("gtms/test/cases/alpha", false); err == nil {
		t.Errorf("folder alpha must refuse: its id has a second owner in beta")
	}
	if err := snap.RequireFolderOwnership("gtms/test/cases/gamma", false); err == nil {
		t.Errorf("folder gamma must refuse: it holds an unprovable spec")
	}
	if err := snap.RequireFolderOwnership("gtms/test/cases/solo", false); err != nil {
		t.Errorf("folder solo (unique) must pass: %v", err)
	}
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
