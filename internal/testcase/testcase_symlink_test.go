package testcase

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
)

// REV-119: ResolveFolder containment matrix -- the authoritative folder resolver
// distinguishes a genuine in-tree miss (ErrFolderNotFound) from a containment
// failure (a *pathsafe.PathSafetyError), and never follows an out-of-tree symlink.

func TestResolveFolder_RealAndAbsentInTree(t *testing.T) {
	root := t.TempDir()
	cases := filepath.Join(root, "gtms", "test", "cases")
	require(t, os.MkdirAll(filepath.Join(cases, "feature", "nested"), 0o755))

	// Real, contained folder resolves without error.
	got, err := ResolveFolder(root, "feature/nested")
	assert.NoError(t, err)
	assert.NotEmpty(t, got)

	// The cases root itself (empty relative folder).
	_, err = ResolveFolder(root, "")
	assert.NoError(t, err)

	// Absent but safely-resolved in-tree folder -> ErrFolderNotFound (distinct).
	_, err = ResolveFolder(root, "no-such-folder")
	assert.True(t, errors.Is(err, ErrFolderNotFound),
		"an absent in-tree folder must be ErrFolderNotFound, got: %v", err)
	assert.False(t, pathsafe.IsPathSafetyError(err),
		"an absent in-tree folder is NOT a containment failure")
}

func TestResolveFolder_OutOfTreeSymlink_Rejected(t *testing.T) {
	skipWithoutSymlinks(t)
	root := t.TempDir()
	external := t.TempDir() // sibling temp dir, outside the project
	require(t, os.MkdirAll(filepath.Join(external, "tc-scope"), 0o755))
	cases := filepath.Join(root, "gtms", "test", "cases")
	require(t, os.MkdirAll(cases, 0o755))
	mustSymlink(t, external, filepath.Join(cases, "escape"))

	// A folder resolving through the escaping symlink must be a CONTAINMENT
	// failure, never silently ErrFolderNotFound (which would let a caller fall
	// through to another classification path).
	_, err := ResolveFolder(root, "escape/tc-scope")
	assert.Error(t, err)
	assert.False(t, errors.Is(err, ErrFolderNotFound),
		"out-of-tree symlink folder must NOT be reported as not-found")
	assert.True(t, pathsafe.IsPathSafetyError(err),
		"out-of-tree symlink folder must be a PathSafetyError")

	// The escaping symlink used directly as the folder is also rejected.
	_, err = ResolveFolder(root, "escape")
	assert.True(t, pathsafe.IsPathSafetyError(err),
		"an out-of-tree folder symlink must be rejected")
}

func TestResolveFolder_InTreeAlias_Allowed(t *testing.T) {
	skipWithoutSymlinks(t)
	root := t.TempDir()
	cases := filepath.Join(root, "gtms", "test", "cases")
	require(t, os.MkdirAll(filepath.Join(cases, "real"), 0o755))
	mustSymlink(t, filepath.Join(cases, "real"), filepath.Join(cases, "alias"))

	// A symlink resolving back INSIDE the cases tree is a legitimate alias.
	_, err := ResolveFolder(root, "alias")
	assert.NoError(t, err, "an in-tree folder alias must resolve")
}

// REV-120: FindAllSpecs counts every in-tree spec carrying a bare ID -- the
// project-wide identity used by the qualified-target ambiguity guard.
func TestFindAllSpecs_CountsInTreeMatches(t *testing.T) {
	root := t.TempDir()
	cases := filepath.Join(root, "gtms", "test", "cases")
	writeSpec(t, filepath.Join(cases, "foo"), "tc-abc12345-a.md")
	writeSpec(t, filepath.Join(cases, "bar"), "tc-abc12345-b.md")
	writeSpec(t, filepath.Join(cases, "foo"), "tc-99999999-only.md")

	dup, err := FindAllSpecs(root, "tc-abc12345")
	assert.NoError(t, err)
	assert.Len(t, dup, 2, "a cross-folder duplicate id must count both specs")

	uniq, err := FindAllSpecs(root, "tc-99999999")
	assert.NoError(t, err)
	assert.Len(t, uniq, 1)

	absent, err := FindAllSpecs(root, "tc-00000000")
	assert.NoError(t, err)
	assert.Len(t, absent, 0)
}

// REV-122: MatchesSpecFile is the single shared spec predicate. A prefixed non-.md
// decoy is never a spec (so it can neither invent a duplicate nor satisfy a scoped
// existence check).
func TestMatchesSpecFile(t *testing.T) {
	cases := []struct {
		base, tcID string
		want       bool
	}{
		{"tc-abc12345.md", "tc-abc12345", true},            // exact form
		{"tc-abc12345-login-test.md", "tc-abc12345", true}, // slug form
		{"tc-abc12345-notes.txt", "tc-abc12345", false},    // non-.md decoy
		{"tc-abc12345.txt", "tc-abc12345", false},          // non-.md
		{"tc-abc12345.bak.md", "tc-abc12345", false},       // not exact, no "-" separator
		{"tc-abc12345other.md", "tc-abc12345", false},      // no separator -> different id
		{"tc-abc123.md", "tc-abc12345", false},             // different id
		// REV-158 CLAUDE-003: an EXTENDED id is a different test case, in both
		// directions. "tc-x--legacy*.md" is never a file of "tc-x".
		{"tc-abc12345--legacy.md", "tc-abc12345", false},          // extended id, exact form
		{"tc-abc12345--legacy-sibling.md", "tc-abc12345", false},  // extended id, slug form
		{"tc-abc12345--legacy.md", "tc-abc12345--legacy", true},   // the extended id's own file
		{"tc-abc12345--legacy-x.md", "tc-abc12345--legacy", true}, // ...with a slug
		{"tc-abc12345-login.md", "tc-abc12345--legacy", false},    // shorter sibling is not the longer id's
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, MatchesSpecFile(c.base, c.tcID), "MatchesSpecFile(%q, %q)", c.base, c.tcID)
	}
}

// TestExists_NonSpecDecoy_NotAScopedSpec covers REV-122 CODEX-001: a non-.md decoy
// under a folder must NOT satisfy the scoped existence check (which would let a
// qualified command broaden to the global ID and delete the real spec elsewhere).
func TestExists_NonSpecDecoy_NotAScopedSpec(t *testing.T) {
	root := t.TempDir()
	cases := filepath.Join(root, "gtms", "test", "cases")
	require(t, os.MkdirAll(filepath.Join(cases, "foo"), 0o755))
	require(t, os.WriteFile(filepath.Join(cases, "foo", "tc-abc12345-notes.txt"), []byte("decoy"), 0o644))
	writeSpec(t, filepath.Join(cases, "bar"), "tc-abc12345-real.md")

	assert.False(t, Exists(root, "foo/tc-abc12345"),
		"a non-.md decoy must NOT satisfy scoped existence under foo/")
	assert.True(t, Exists(root, "bar/tc-abc12345"), "the real .md spec resolves under bar/")
	assert.True(t, Exists(root, "tc-abc12345"), "bare id resolves globally to the real spec")
}

// REV-121 CODEX-002: only real .md spec forms ("<tcID>.md" / "<tcID>-*.md") count,
// aligned with the delete/reset mutation resolver. A prefixed non-spec file must not
// inflate the duplicate count.
func TestFindAllSpecs_OnlyMarkdownSpecForms(t *testing.T) {
	root := t.TempDir()
	cases := filepath.Join(root, "gtms", "test", "cases")
	writeSpec(t, filepath.Join(cases, "foo"), "tc-abc12345-real.md") // prefix form: counts
	writeSpec(t, filepath.Join(cases, "bar"), "tc-abc12345.md")      // exact form: counts
	// Prefixed NON-spec files must NOT count.
	require(t, os.WriteFile(filepath.Join(cases, "foo", "tc-abc12345-notes.txt"), []byte("x"), 0o644))
	require(t, os.WriteFile(filepath.Join(cases, "foo", "tc-abc12345.bak"), []byte("x"), 0o644))
	require(t, os.MkdirAll(filepath.Join(cases, "foo", "tc-abc12345-subdir"), 0o755))

	got, err := FindAllSpecs(root, "tc-abc12345")
	assert.NoError(t, err)
	assert.Len(t, got, 2, "only the two .md specs count; prefixed non-.md files/dirs must not")
}

// REV-121 CODEX-001: an incomplete filesystem scan must FAIL CLOSED (return an
// error) so the qualified ambiguity guard aborts rather than undercounting a hidden
// duplicate and mutating shared flat state.
func TestFindAllSpecs_ScanError_FailsClosed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix directory-permission fixture")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	root := t.TempDir()
	cases := filepath.Join(root, "gtms", "test", "cases")
	writeSpec(t, filepath.Join(cases, "foo"), "tc-abc12345-real.md")
	// An unreadable subtree that could hide a duplicate.
	secret := filepath.Join(cases, "secret")
	writeSpec(t, secret, "tc-abc12345-hidden.md")
	require(t, os.Chmod(secret, 0o000))
	t.Cleanup(func() { _ = os.Chmod(secret, 0o755) }) // allow TempDir cleanup

	_, err := FindAllSpecs(root, "tc-abc12345")
	assert.Error(t, err, "an unreadable subtree makes the scan incomplete -- must fail closed")
}

func TestFindAllSpecs_OutOfTreeSpecSymlink_NotCounted(t *testing.T) {
	skipWithoutSymlinks(t)
	root := t.TempDir()
	cases := filepath.Join(root, "gtms", "test", "cases")
	writeSpec(t, filepath.Join(cases, "foo"), "tc-abc12345-real.md")
	// An out-of-tree spec symlink inside a safe dir must NOT inflate the count.
	require(t, os.MkdirAll(filepath.Join(cases, "bar"), 0o755))
	outside := t.TempDir()
	require(t, os.WriteFile(filepath.Join(outside, "evil.md"), []byte("evil"), 0o644))
	mustSymlink(t, filepath.Join(outside, "evil.md"), filepath.Join(cases, "bar", "tc-abc12345-ext.md"))

	got, err := FindAllSpecs(root, "tc-abc12345")
	assert.NoError(t, err)
	assert.Len(t, got, 1, "an out-of-tree spec symlink must not be counted as a duplicate")
}

// REV-123 CODEX-002: ResolveSpec and FindAllSpecs must relativise against the
// CANONICAL project root. A NON-canonical projectRoot (here a symlink to the real
// root; on Windows an 8.3 short path) would otherwise make filepath.Rel emit a
// spurious "../.." instead of "gtms/test/cases/...".
func TestSpecResolvers_NonCanonicalRoot_CleanProjectRelativePaths(t *testing.T) {
	skipWithoutSymlinks(t)
	real := t.TempDir()
	foo := filepath.Join(real, "gtms", "test", "cases", "foo")
	require(t, os.MkdirAll(foo, 0o755))
	require(t, os.WriteFile(filepath.Join(foo, "tc-abc12345-real.md"), []byte("---\ntest_case_id: tc-abc12345\ntitle: t\n---\nspec\n"), 0o644))

	// A symlink to the real root -- a non-canonical projectRoot.
	link := filepath.Join(t.TempDir(), "linkroot")
	mustSymlink(t, real, link)

	const want = "gtms/test/cases/foo/tc-abc12345-real.md"

	all, err := FindAllSpecs(link, "tc-abc12345")
	assert.NoError(t, err)
	if assert.Len(t, all, 1) {
		assert.Equal(t, want, all[0], "FindAllSpecs must yield a clean project-relative path")
		assert.NotContains(t, all[0], "..", "no spurious ../ from a non-canonical root")
	}

	rel, err := ResolveSpec(link, "foo/tc-abc12345")
	assert.NoError(t, err)
	assert.Equal(t, want, rel, "ResolveSpec must yield a clean project-relative path")
}

func TestResolveFolder_CasesRootOutOfTreeSymlink_Rejected(t *testing.T) {
	skipWithoutSymlinks(t)
	root := t.TempDir()
	external := t.TempDir()
	require(t, os.MkdirAll(filepath.Join(external, "feature"), 0o755))
	require(t, os.MkdirAll(filepath.Join(root, "gtms", "test"), 0o755))
	mustSymlink(t, external, filepath.Join(root, "gtms", "test", "cases"))

	// A cases-root symlink escaping the project poisons every folder resolution.
	_, err := ResolveFolder(root, "feature")
	assert.True(t, pathsafe.IsPathSafetyError(err),
		"an out-of-project cases-root symlink must be rejected")
}

// REV-115: symlink containment matrix. testcase.ResolveSpec must reject any
// target (scope directory or individual spec file) that resolves OUTSIDE the
// canonical gtms/test/cases/ directory, while still supporting legitimate
// in-tree aliases. Symlink creation needs privilege on Windows, so these tests
// skip there; the non-symlink cases in testcase_test.go run everywhere.

func skipWithoutSymlinks(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privilege on Windows")
	}
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported in this environment: %v", err)
	}
}

// writeSpec writes a real spec whose body test_case_id is derived from the
// filename (ADR-024: identity is decided by the parsed body, so a fixture must
// carry a valid test_case_id to be a provable owner). The id is the leading
// "tc-<run>" token of the filename before the slug separator.
func writeSpec(t *testing.T, dir, name string) {
	t.Helper()
	require(t, os.MkdirAll(dir, 0o755))
	body := "---\ntest_case_id: " + idFromSpecName(name) + "\ntitle: \"t\"\n---\n\nspec\n"
	require(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
}

// idFromSpecName extracts the leading tc-id token from a spec filename such as
// "tc-abc12345-slug.md" -> "tc-abc12345" or "tc-abc12345.md" -> "tc-abc12345".
func idFromSpecName(name string) string {
	stem := strings.TrimSuffix(name, ".md")
	if !strings.HasPrefix(stem, "tc-") {
		return stem
	}
	rest := stem[len("tc-"):]
	if i := strings.IndexAny(rest, "-."); i >= 0 {
		rest = rest[:i]
	}
	return "tc-" + rest
}

// REV-116 CODEX-001: the cases directory ITSELF is validated under the project
// root before it is trusted as the containment boundary.

// 1. Cases-root symlink resolving OUTSIDE the project -> rejected. Before the fix
// this external directory became the trusted root and any spec inside it passed.
func TestResolveSpec_CasesRootOutOfTreeSymlink_Rejected(t *testing.T) {
	skipWithoutSymlinks(t)
	root := t.TempDir()
	external := t.TempDir() // sibling temp dir, not under root
	writeSpec(t, external, "tc-77777777-x.md")
	require(t, os.MkdirAll(filepath.Join(root, "gtms", "test"), 0o755))
	mustSymlink(t, external, filepath.Join(root, "gtms", "test", "cases"))

	assert.False(t, Exists(root, "tc-77777777"),
		"a cases-dir symlink resolving outside the project must be rejected")
	// Also fail closed for a folder-qualified lookup through the same alias.
	writeSpec(t, filepath.Join(external, "sub"), "tc-77aa7777-x.md")
	assert.False(t, Exists(root, "sub/tc-77aa7777"),
		"a qualified lookup through an out-of-project cases-dir symlink must be rejected")
}

// 2. Cases-root symlink resolving to an IN-PROJECT directory -> allowed. An
// in-project alias canonicalises to its in-tree target and still resolves.
func TestResolveSpec_CasesRootInTreeSymlink_Allowed(t *testing.T) {
	skipWithoutSymlinks(t)
	root := t.TempDir()
	realCases := filepath.Join(root, "real-cases") // still under the project root
	writeSpec(t, realCases, "tc-88888888-x.md")
	require(t, os.MkdirAll(filepath.Join(root, "gtms", "test"), 0o755))
	mustSymlink(t, realCases, filepath.Join(root, "gtms", "test", "cases"))

	assert.True(t, Exists(root, "tc-88888888"),
		"an in-project cases-dir alias must resolve like the real directory")
}

// 3. Root-component in-tree directory symlink -> true.
func TestResolveSpec_RootInTreeDirSymlink_Allowed(t *testing.T) {
	skipWithoutSymlinks(t)
	root := t.TempDir()
	cases := filepath.Join(root, "gtms", "test", "cases")
	writeSpec(t, filepath.Join(cases, "real"), "tc-11111111-x.md")
	mustSymlink(t, filepath.Join(cases, "real"), filepath.Join(cases, "alias"))

	assert.True(t, Exists(root, "alias/tc-11111111"),
		"an in-tree directory alias must resolve like the real directory")
}

// 4. Intermediate in-tree directory symlink -> true.
func TestResolveSpec_IntermediateInTreeDirSymlink_Allowed(t *testing.T) {
	skipWithoutSymlinks(t)
	root := t.TempDir()
	cases := filepath.Join(root, "gtms", "test", "cases")
	writeSpec(t, filepath.Join(cases, "realparent", "sub"), "tc-22222222-x.md")
	mustSymlink(t, filepath.Join(cases, "realparent"), filepath.Join(cases, "aliasparent"))

	assert.True(t, Exists(root, "aliasparent/sub/tc-22222222"),
		"an in-tree intermediate alias component must resolve")
}

// 5. Root-component out-of-tree directory symlink -> false.
func TestResolveSpec_RootOutOfTreeDirSymlink_Rejected(t *testing.T) {
	skipWithoutSymlinks(t)
	root := t.TempDir()
	cases := filepath.Join(root, "gtms", "test", "cases")
	require(t, os.MkdirAll(cases, 0o755))
	outside := filepath.Join(root, "outside")
	writeSpec(t, outside, "tc-33333333-x.md")
	mustSymlink(t, outside, filepath.Join(cases, "escape"))

	assert.False(t, Exists(root, "escape/tc-33333333"),
		"a scope symlink resolving outside cases/ must be rejected")
}

// 6. Intermediate out-of-tree directory symlink -> false.
func TestResolveSpec_IntermediateOutOfTreeDirSymlink_Rejected(t *testing.T) {
	skipWithoutSymlinks(t)
	root := t.TempDir()
	cases := filepath.Join(root, "gtms", "test", "cases")
	require(t, os.MkdirAll(cases, 0o755))
	outside := filepath.Join(root, "outside")
	writeSpec(t, filepath.Join(outside, "sub"), "tc-44444444-x.md")
	mustSymlink(t, outside, filepath.Join(cases, "escapeparent"))

	assert.False(t, Exists(root, "escapeparent/sub/tc-44444444"),
		"an intermediate symlink component resolving outside cases/ must be rejected")
}

// 7. In-tree spec symlink targeting ANOTHER in-tree spec -> true (documented:
// consistent with resolved containment).
func TestResolveSpec_InTreeSpecSymlink_ToInTreeSpec_Allowed(t *testing.T) {
	skipWithoutSymlinks(t)
	root := t.TempDir()
	cases := filepath.Join(root, "gtms", "test", "cases")
	writeSpec(t, filepath.Join(cases, "a"), "tc-55555555-x.md")
	require(t, os.MkdirAll(filepath.Join(cases, "b"), 0o755))
	mustSymlink(t, filepath.Join(cases, "a", "tc-55555555-x.md"), filepath.Join(cases, "b", "tc-55555555-x.md"))

	assert.True(t, Exists(root, "b/tc-55555555"),
		"a spec symlink resolving to another in-tree spec is allowed")
}

// 8. In-tree spec symlink targeting an OUT-OF-TREE file -> false.
func TestResolveSpec_InTreeSpecSymlink_ToOutOfTree_Rejected(t *testing.T) {
	skipWithoutSymlinks(t)
	root := t.TempDir()
	cases := filepath.Join(root, "gtms", "test", "cases")
	require(t, os.MkdirAll(filepath.Join(cases, "c"), 0o755))
	outside := filepath.Join(root, "outside")
	require(t, os.MkdirAll(outside, 0o755))
	require(t, os.WriteFile(filepath.Join(outside, "evil.md"), []byte("evil"), 0o644))
	mustSymlink(t, filepath.Join(outside, "evil.md"), filepath.Join(cases, "c", "tc-66666666-x.md"))

	assert.False(t, Exists(root, "c/tc-66666666"),
		"a spec symlink resolving outside cases/ must be rejected even inside a safe dir")
}

// 9. Missing spec beneath a valid in-tree alias -> false.
func TestResolveSpec_MissingSpecUnderValidAlias_False(t *testing.T) {
	skipWithoutSymlinks(t)
	root := t.TempDir()
	cases := filepath.Join(root, "gtms", "test", "cases")
	require(t, os.MkdirAll(filepath.Join(cases, "real9"), 0o755)) // empty
	mustSymlink(t, filepath.Join(cases, "real9"), filepath.Join(cases, "alias9"))

	assert.False(t, Exists(root, "alias9/tc-99999999"),
		"a valid in-tree alias with no matching spec must be false")
}

// 10. Unqualified global lookup remains functional but cannot accept an
// out-of-tree spec symlink.
func TestResolveSpec_UnqualifiedGlobal_RejectsOutOfTreeSpecSymlink(t *testing.T) {
	skipWithoutSymlinks(t)
	root := t.TempDir()
	cases := filepath.Join(root, "gtms", "test", "cases")

	// Functional: a real in-tree spec still resolves globally.
	writeSpec(t, filepath.Join(cases, "d"), "tc-good1111-x.md")
	assert.True(t, Exists(root, "tc-good1111"), "global lookup must still find a real in-tree spec")

	// An out-of-tree spec symlink inside a real dir must NOT be accepted globally.
	require(t, os.MkdirAll(filepath.Join(cases, "e"), 0o755))
	outside := filepath.Join(root, "outside")
	require(t, os.MkdirAll(outside, 0o755))
	require(t, os.WriteFile(filepath.Join(outside, "bad.md"), []byte("bad"), 0o644))
	mustSymlink(t, filepath.Join(outside, "bad.md"), filepath.Join(cases, "e", "tc-bad11111-x.md"))
	assert.False(t, Exists(root, "tc-bad11111"),
		"global lookup must not accept an out-of-tree spec symlink")
}
