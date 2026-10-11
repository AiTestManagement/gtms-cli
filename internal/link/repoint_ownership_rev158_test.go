package link

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// seedAmbiguousProject creates two owners of tc-aaa11111 (one of them RENAMED,
// so a filename-counting check could not see it) plus a wiring record the
// repoint would rewrite.
func seedAmbiguousProject(t *testing.T) (root, wiringPath string, before []byte) {
	t.Helper()
	root = t.TempDir()
	createTCSpecAt(t, root, "feature-a", "tc-aaa11111")
	dir := filepath.Join(layout.TestCasesDir(root), "feature-b")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-deadbeef-renamed.md"),
		[]byte("---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-aaa11111\n---\n"), 0o644))

	artefact := filepath.Join(root, "test", "x.bats")
	require.NoError(t, os.MkdirAll(filepath.Dir(artefact), 0o755))
	require.NoError(t, os.WriteFile(artefact, []byte("x\n"), 0o644))

	p, err := wiring.Write(root, &wiring.WiringRecord{
		TestCase: "tc-aaa11111", TestCaseHash: "1111111111111111", Framework: "mockfw",
		Adapter: "old-runner", Artefact: "test/x.bats", ArtefactHash: "2222222222222222",
	})
	require.NoError(t, err)

	before, err = os.ReadFile(p)
	require.NoError(t, err)
	return root, p, before
}

// REV-158 CODEX-005: the repoint ownership guard used to live only in the Cobra
// wrappers, so link.RepointSingle, link.RepointBulk and link.RepointAll stayed
// callable without it. Any non-CLI caller could therefore rewrite wiring the CLI
// correctly refuses, contradicting this package's stated ownership of mutation
// safety.
//
// These call the core entry points DIRECTLY -- no Cobra, no CLI helper -- and
// require each to refuse and leave the wiring byte-identical. The fixture uses a
// RENAMED duplicate owner, which the old filename-counting preflight could not
// see at all.
//
// REV-160 CLAUDE-003 moved WHERE the decision is made, not whether it binds. It
// is made once, by AuthoriseSingleRepoint / AuthoriseScopeRepoint against the
// operation's snapshot, and carried to the write. The property these tests exist
// for is unchanged and in fact stronger: each case asserts BOTH that the
// authorisation refuses, AND that the entry point still refuses a caller that
// tries to skip it -- there is no route from a bare id to a wiring rewrite.
func TestRepointSingle_RefusesAmbiguousOwnershipWithoutTheCLI(t *testing.T) {
	root, wiringPath, before := seedAmbiguousProject(t)

	_, aerr := AuthoriseSingleRepoint(snapshotForTest(t, root), "tc-aaa11111")
	require.Error(t, aerr, "authorising an ambiguous identity must refuse")
	assert.Contains(t, aerr.Error(), "ambiguous")

	// And the entry point refuses a caller that tries to skip the decision.
	_, err := RepointSingle(root, "tc-aaa11111", mockfwOpts("old-runner", "new-runner"))
	require.Error(t, err, "RepointSingle must refuse without a carried authority")

	after, rerr := os.ReadFile(wiringPath)
	require.NoError(t, rerr)
	assert.Equal(t, before, after, "no wiring may be rewritten by a refused repoint")
}

func TestRepointBulk_RefusesAmbiguousOwnershipWithoutTheCLI(t *testing.T) {
	root, wiringPath, before := seedAmbiguousProject(t)

	_, aerr := AuthoriseScopeRepoint(root, snapshotForTest(t, root), "feature-a", false)
	require.Error(t, aerr, "authorising a scope holding an ambiguous identity must refuse")
	assert.Contains(t, aerr.Error(), "ambiguous")
	assert.Contains(t, aerr.Error(), "tc-deadbeef-renamed.md",
		"the renamed owner must be named -- a filename-counting preflight could not see it")

	_, err := RepointBulk(root, "feature-a", false, mockfwOpts("old-runner", "new-runner"))
	require.Error(t, err, "RepointBulk must refuse without a carried authority")

	after, rerr := os.ReadFile(wiringPath)
	require.NoError(t, rerr)
	assert.Equal(t, before, after)
}

func TestRepointAll_RefusesAmbiguousOwnershipWithoutTheCLI(t *testing.T) {
	root, wiringPath, before := seedAmbiguousProject(t)

	_, aerr := AuthoriseScopeRepoint(root, snapshotForTest(t, root), "", true)
	require.Error(t, aerr, "RepointAll had no ownership preflight of any kind")
	assert.Contains(t, aerr.Error(), "ambiguous")

	_, err := RepointAll(root, mockfwOpts("old-runner", "new-runner"))
	require.Error(t, err, "RepointAll must refuse without a carried authority")

	after, rerr := os.ReadFile(wiringPath)
	require.NoError(t, rerr)
	assert.Equal(t, before, after)
}

// An Unprovable candidate in scope refuses the same three entry points.
func TestRepointEntryPoints_RefuseUnprovableCandidate(t *testing.T) {
	seed := func(t *testing.T) string {
		t.Helper()
		root := t.TempDir()
		createTCSpecAt(t, root, "feature-a", "tc-aaa11111")
		dir := filepath.Join(layout.TestCasesDir(root), "feature-a")
		require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-bbb22222-broken.md"),
			[]byte("---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: [unclosed\n---\n"), 0o644))
		return root
	}

	t.Run("bulk", func(t *testing.T) {
		root := seed(t)
		_, err := AuthoriseScopeRepoint(root, snapshotForTest(t, root), "feature-a", false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be proved")
	})
	t.Run("all", func(t *testing.T) {
		root := seed(t)
		_, err := AuthoriseScopeRepoint(root, snapshotForTest(t, root), "", true)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be proved")
	})
	t.Run("single", func(t *testing.T) {
		root := seed(t)
		_, err := AuthoriseSingleRepoint(snapshotForTest(t, root), "tc-bbb22222")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be proved")
	})
}

// Positive control: a clean project still repoints through the core entry points.
func TestRepointEntryPoints_CleanProjectStillRepoints(t *testing.T) {
	root := t.TempDir()
	createTCSpecAt(t, root, "feature-a", "tc-aaa11111")
	artefact := filepath.Join(root, "test", "x.bats")
	require.NoError(t, os.MkdirAll(filepath.Dir(artefact), 0o755))
	require.NoError(t, os.WriteFile(artefact, []byte("x\n"), 0o644))
	_, werr := wiring.Write(root, &wiring.WiringRecord{
		TestCase: "tc-aaa11111", TestCaseHash: "1111111111111111", Framework: "mockfw",
		Adapter: "old-runner", Artefact: "test/x.bats", ArtefactHash: "2222222222222222",
	})
	require.NoError(t, werr)

	summary, err := RepointBulk(root, "feature-a", false,
		withScopeAuth(t, root, "feature-a", false, mockfwOpts("old-runner", "new-runner")))
	require.NoError(t, err)
	assert.Equal(t, 1, summary.Repointed)
}
