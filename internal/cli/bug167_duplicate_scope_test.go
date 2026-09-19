package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
	"github.com/aitestmanagement/gtms-cli/internal/target"
)

// REV-120 CODEX-001: a folder-qualified single-TC target must resolve to EXACTLY
// ONE project-wide spec. Cross-folder duplicate IDs are not prevented today
// (tree-wide uniqueness ENH-100/101 are open) and the downstream mutation is flat
// (bare-ID), so `delete foo/tc-x` would collateral-delete `bar/tc-x`.
// requireScopedSpec now aborts such a qualified target BEFORE any mutation, naming
// every conflicting spec. setupScopedProject seeds tc-abc12345 under foo/; addBarDup
// adds a same-ID spec under bar/.

func addBarDup(t *testing.T, root string) {
	t.Helper()
	dup := filepath.Join(root, "gtms/test/cases/bar/tc-abc12345-dup.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(dup), 0o755))
	require.NoError(t, os.WriteFile(dup, []byte("---\ntest_case_id: tc-abc12345\ntitle: bar duplicate\n---\n"), 0o644))
}

func TestRequireScopedSpec_CrossFolderDuplicate_Ambiguous(t *testing.T) {
	root := setupScopedProject(t)
	addBarDup(t, root)

	// Qualified target with a cross-folder duplicate -> ambiguous, names both.
	err := requireScopedSpec(root, target.Result{Kind: target.KindTCID, Target: "foo/tc-abc12345", TCID: "tc-abc12345"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ambiguous")
	assert.Contains(t, err.Error(), "foo/tc-abc12345")
	assert.Contains(t, err.Error(), "bar/tc-abc12345")

	// Bare (unqualified) target is unchanged -- no ambiguity check, global behaviour.
	assert.NoError(t, requireScopedSpec(root, target.Result{Kind: target.KindTCID, Target: "tc-abc12345", TCID: "tc-abc12345"}))
}

// TestRequireScopedSpec_AmbiguityError_SanitizesPaths covers REV-123 CODEX-001: the
// conflicting spec paths in the ambiguity diagnostic are filesystem-derived, so a
// control/ANSI-escape character in a checked-out directory name must be sanitized
// (replaced with '?') before it is echoed, not passed through to the terminal.
func TestRequireScopedSpec_AmbiguityError_SanitizesPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Windows filenames cannot contain control characters (0x00-0x1F), so the
		// exploit fixture (a directory named with ESC) cannot be created there. The
		// sanitizer itself is covered portably by TestSanitizeControlChars.
		t.Skip("control characters are not valid in Windows filenames")
	}
	root := setupScopedProject(t) // real spec: foo/tc-abc12345-real.md
	// A duplicate under a directory whose name contains an ESC (0x1b) control char.
	weird := filepath.Join(root, "gtms/test/cases", "e\x1bvil")
	require.NoError(t, os.MkdirAll(weird, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(weird, "tc-abc12345-dup.md"),
		[]byte("---\ntest_case_id: tc-abc12345\n---\n"), 0o644))

	err := requireScopedSpec(root, target.Result{Kind: target.KindTCID, Target: "foo/tc-abc12345", TCID: "tc-abc12345"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ambiguous")
	assert.NotContains(t, err.Error(), "\x1b", "the ESC control char must not reach the terminal")
	assert.Contains(t, err.Error(), "e?vil", "the control char is replaced with '?'")
}

// TestSanitizeControlChars covers the display sanitizer directly. Unlike the
// filesystem fixture above it is portable -- it runs on Windows, where a
// control-character path cannot exist, so the sanitizer logic still has coverage.
//
// BUG-187 promoted the helper to pathsafe.SanitizeControlChars; this test now
// pins the CLI caller's dependency on that shared rule. The rule itself is also
// pinned at its new home by internal/pathsafe TestSanitizeControlChars.
func TestSanitizeControlChars(t *testing.T) {
	cases := []struct{ in, want string }{
		{"gtms/test/cases/foo/tc-abc12345.md", "gtms/test/cases/foo/tc-abc12345.md"}, // clean path untouched
		{"e\x1bvil", "e?vil"},         // ANSI ESC (0x1b)
		{"a\x00b\x1fc\x7fd", "a?b?c?d"}, // NUL, US, DEL
		{"tab\there", "tab?here"},     // C0 control (tab, 0x09)
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, pathsafe.SanitizeControlChars(c.in), "SanitizeControlChars(%q)", c.in)
	}
	// No truncation (unlike sanitizeForError): a long clean path returns intact.
	long := strings.Repeat("gtms/test/cases/deep/", 10) + "tc-abc12345.md"
	assert.Equal(t, long, pathsafe.SanitizeControlChars(long))
}

func TestDelete_CrossFolderDuplicate_Aborts_NoDeletion(t *testing.T) {
	skipIfShort(t)
	root := setupScopedProject(t)
	addBarDup(t, root)
	restore := withExecuteGlobals(t, root, batsExecuteConfig())
	defer restore()

	fooSpec := filepath.Join(root, "gtms/test/cases/foo/tc-abc12345-real.md")
	barSpec := filepath.Join(root, "gtms/test/cases/bar/tc-abc12345-dup.md")

	out, err := runCmdCaptureStderr(t, newDeleteCmd(), "foo/tc-abc12345")
	require.Error(t, err)
	assert.Contains(t, out, "ambiguous")
	// BOTH specs must survive -- the decisive no-data-loss assertion.
	assert.FileExists(t, fooSpec, "the requested-folder spec must survive an ambiguous abort")
	assert.FileExists(t, barSpec, "the OTHER-folder same-ID spec must NOT be collateral-deleted")
	assertNoPipelineMutation(t, root)
}

func TestReset_CrossFolderDuplicate_Aborts(t *testing.T) {
	skipIfShort(t)
	root := setupScopedProject(t)
	addBarDup(t, root)
	restore := withExecuteGlobals(t, root, batsExecuteConfig())
	defer restore()

	out, err := runCmdCaptureStderr(t, newResetCmd(), "foo/tc-abc12345")
	require.Error(t, err)
	assert.Contains(t, out, "ambiguous")
	assertNoPipelineMutation(t, root)
}

func TestLink_CrossFolderDuplicate_Aborts_WritesNothing(t *testing.T) {
	skipIfShort(t)
	root := setupScopedProject(t)
	addBarDup(t, root)
	restore := withExecuteGlobals(t, root, batsLinkConfig())
	defer restore()

	artefact := filepath.Join(root, "test/acceptance/foo/real-test.bats")
	out, err := runCmdCaptureStderr(t, newLinkCmd(),
		"foo/tc-abc12345", "--artefact", artefact, "--framework", "bats", "--strict")
	require.Error(t, err)
	assert.Contains(t, out, "ambiguous")
	wiring, _ := filepath.Glob(filepath.Join(root, "gtms/automation/wiring", "tc-abc12345*"))
	assert.Empty(t, wiring, "an ambiguous qualified link must write no wiring")
}

func TestAutomate_CrossFolderDuplicate_Aborts_NoMutation(t *testing.T) {
	skipIfShort(t)
	root := setupScopedProject(t)
	addBarDup(t, root)
	restore := withExecuteGlobals(t, root, batsExecuteConfig())
	defer restore()

	out, err := runCmdCaptureStderr(t, newAutomateCmd(), "foo/tc-abc12345")
	require.Error(t, err)
	assert.Contains(t, out, "ambiguous")
	assertNoPipelineMutation(t, root)
}

// TestDelete_ScanError_FailsClosed_NoDeletion covers REV-121 CODEX-001: when the
// project-wide uniqueness scan is incomplete (an unreadable subtree that could hide
// a duplicate), the qualified delete aborts with no mutation -- fail closed.
func TestDelete_ScanError_FailsClosed_NoDeletion(t *testing.T) {
	skipIfShort(t)
	if runtime.GOOS == "windows" {
		t.Skip("unix directory-permission fixture")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	root := setupScopedProject(t) // foo/tc-abc12345
	restore := withExecuteGlobals(t, root, batsExecuteConfig())
	defer restore()

	fooSpec := filepath.Join(root, "gtms/test/cases/foo/tc-abc12345-real.md")
	secret := filepath.Join(root, "gtms/test/cases/secret")
	require.NoError(t, os.MkdirAll(secret, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(secret, "tc-abc12345-hidden.md"),
		[]byte("---\ntest_case_id: tc-abc12345\n---\n"), 0o644))
	require.NoError(t, os.Chmod(secret, 0o000))
	t.Cleanup(func() { _ = os.Chmod(secret, 0o755) })

	_, err := runCmdCaptureStderr(t, newDeleteCmd(), "foo/tc-abc12345")
	require.Error(t, err, "an incomplete uniqueness scan must abort the qualified delete")
	assert.FileExists(t, fooSpec, "no deletion may occur when the scan is incomplete")
}

// TestDelete_WrongFolderNonSpecDecoy_Aborts_NoMutation covers REV-122 CODEX-001: a
// non-.md decoy (tc-x-notes.txt) under the requested folder must not satisfy the
// scoped existence check and let the qualified delete broaden to the bare ID and
// remove the real spec that lives in ANOTHER folder.
func TestDelete_WrongFolderNonSpecDecoy_Aborts_NoMutation(t *testing.T) {
	skipIfShort(t)
	root := setupScopedProject(t) // real spec: gtms/test/cases/foo/tc-abc12345-real.md
	restore := withExecuteGlobals(t, root, batsExecuteConfig())
	defer restore()

	// A non-.md decoy under bar/ must NOT make "bar/tc-abc12345" resolve.
	decoy := filepath.Join(root, "gtms/test/cases/bar/tc-abc12345-notes.txt")
	require.NoError(t, os.MkdirAll(filepath.Dir(decoy), 0o755))
	require.NoError(t, os.WriteFile(decoy, []byte("not a spec"), 0o644))
	fooSpec := filepath.Join(root, "gtms/test/cases/foo/tc-abc12345-real.md")

	out, err := runCmdCaptureStderr(t, newDeleteCmd(), "bar/tc-abc12345")
	require.Error(t, err)
	assert.Contains(t, out, "not found")
	assert.FileExists(t, fooSpec,
		"a non-.md decoy in the requested folder must not delete the real spec elsewhere")
}

// TestDelete_UniqueQualified_StillDeletes is the positive control: a qualified
// target with NO cross-folder duplicate still resolves and deletes its one spec.
func TestDelete_UniqueQualified_StillDeletes(t *testing.T) {
	skipIfShort(t)
	root := setupScopedProject(t) // only foo/ has tc-abc12345
	restore := withExecuteGlobals(t, root, batsExecuteConfig())
	defer restore()

	fooSpec := filepath.Join(root, "gtms/test/cases/foo/tc-abc12345-real.md")
	_, err := runCmdCaptureStderr(t, newDeleteCmd(), "foo/tc-abc12345")
	require.NoError(t, err)
	assert.NoFileExists(t, fooSpec, "a unique qualified target must still delete its one spec")
}
