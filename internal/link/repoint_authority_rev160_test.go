package link

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/testcase"
)

// snapshotForTest builds the operation's single snapshot the way the CLI does.
func snapshotForTest(t *testing.T, root string) *testcase.Snapshot {
	t.Helper()
	snap, err := testcase.NewSnapshot(root)
	require.NoError(t, err)
	return snap
}

// scopeOwnerIDsForTest is the test-side equivalent of what the CLI does for a
// folder or --all repoint: one snapshot, one AuthoriseScopeRepoint, and the
// in-scope owner set that comes back on the authority. It replaces direct calls
// to the removed requireScopeOwnership helper -- the questions are identical, the
// answer is now carried rather than recomputed at the write.
func scopeOwnerIDsForTest(t *testing.T, root, folder string, recursive bool) (map[string]bool, error) {
	t.Helper()
	auth, err := AuthoriseScopeRepoint(root, snapshotForTest(t, root), folder, recursive)
	if err != nil {
		return nil, err
	}
	return auth.ownerIDs, nil
}

// singleAuthForTest authorises a single-TC repoint against a fresh snapshot.
func singleAuthForTest(t *testing.T, root, tcID string) *RepointAuthority {
	t.Helper()
	auth, err := AuthoriseSingleRepoint(snapshotForTest(t, root), tcID)
	require.NoError(t, err)
	return auth
}

// withSingleAuth returns opts carrying the single-TC authority for tcID.
func withSingleAuth(t *testing.T, root, tcID string, opts RepointOptions) RepointOptions {
	t.Helper()
	opts.Authority = singleAuthForTest(t, root, tcID)
	return opts
}

// withScopeAuth returns opts carrying the scope authority for (folder, recursive).
func withScopeAuth(t *testing.T, root, folder string, recursive bool, opts RepointOptions) RepointOptions {
	t.Helper()
	auth, err := AuthoriseScopeRepoint(root, snapshotForTest(t, root), folder, recursive)
	require.NoError(t, err)
	opts.Authority = auth
	return opts
}

// --- REV-160 CLAUDE-003: the carried decision must be LOAD-BEARING -------

// Without a carried authority the mutation refuses outright. This is what makes
// re-deriving ownership at the write impossible to reintroduce quietly: there is
// no code path from a bare id to a wiring rewrite any more.
func TestRepointSingle_WithoutCarriedAuthority_Refuses(t *testing.T) {
	root := t.TempDir()
	createTCSpecAt(t, root, "feature-a", "tc-aaa11111")

	_, err := RepointSingle(root, "tc-aaa11111", RepointOptions{
		Config:     execConfig("mockfw", "old-runner", "new-runner"),
		NewAdapter: "new-runner",
	})
	require.Error(t, err, "a repoint with no carried ownership decision must refuse")
	assert.Contains(t, err.Error(), "no carried ownership authority")
}

// An authority for a DIFFERENT test case does not authorise this one. Without
// this check a caller could satisfy the signature with any authority it had, and
// the write would be back to trusting the bare id argument.
func TestRepointSingle_AuthorityForAnotherTestCase_Refuses(t *testing.T) {
	root := t.TempDir()
	createTCSpecAt(t, root, "feature-a", "tc-aaa11111")
	createTCSpecAt(t, root, "feature-b", "tc-bbb22222")

	_, err := RepointSingle(root, "tc-aaa11111", RepointOptions{
		Config:     execConfig("mockfw", "old-runner", "new-runner"),
		NewAdapter: "new-runner",
		Authority:  singleAuthForTest(t, root, "tc-bbb22222"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not cover this target")
}

// A scope authority is not a single-TC authority and vice versa.
func TestRepoint_AuthorityKindsAreNotInterchangeable(t *testing.T) {
	root := t.TempDir()
	createTCSpecAt(t, root, "feature-a", "tc-aaa11111")

	_, err := RepointSingle(root, "tc-aaa11111", RepointOptions{
		Config:     execConfig("mockfw", "old-runner", "new-runner"),
		NewAdapter: "new-runner",
		Authority:  mustScopeAuth(t, root, "feature-a", false),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scope authority")

	_, berr := RepointBulk(root, "feature-a", false, RepointOptions{
		Config:     execConfig("mockfw", "old-runner", "new-runner"),
		NewAdapter: "new-runner",
		Authority:  singleAuthForTest(t, root, "tc-aaa11111"),
	})
	require.Error(t, berr)
	assert.Contains(t, berr.Error(), "single-test-case authority")
}

// A scope authority authorises exactly its own scope.
func TestRepointBulk_AuthorityForAnotherScope_Refuses(t *testing.T) {
	root := t.TempDir()
	createTCSpecAt(t, root, "feature-a", "tc-aaa11111")
	createTCSpecAt(t, root, "feature-b", "tc-bbb22222")

	_, err := RepointBulk(root, "feature-a", false, RepointOptions{
		Config:     execConfig("mockfw", "old-runner", "new-runner"),
		NewAdapter: "new-runner",
		Authority:  mustScopeAuth(t, root, "feature-b", false),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "was authorised for")

	// The recursive flag is part of the scope, not a detail: a shallow decision
	// does not authorise a recursive rewrite.
	_, rerr := RepointBulk(root, "feature-a", true, RepointOptions{
		Config:     execConfig("mockfw", "old-runner", "new-runner"),
		NewAdapter: "new-runner",
		Authority:  mustScopeAuth(t, root, "feature-a", false),
	})
	require.Error(t, rerr)
	assert.Contains(t, rerr.Error(), "recursive")
}

func mustScopeAuth(t *testing.T, root, folder string, recursive bool) *RepointAuthority {
	t.Helper()
	auth, err := AuthoriseScopeRepoint(root, snapshotForTest(t, root), folder, recursive)
	require.NoError(t, err)
	return auth
}

// --- the orphan-repair policy and its three controls (REV-160 CODEX-001) ---

// Control 1: only proven ABSENCE is permitted. An orphan authorises a repoint...
func TestAuthoriseSingleRepoint_OrphanIsPermitted(t *testing.T) {
	root := t.TempDir()
	createTCSpecAt(t, root, "feature-a", "tc-aaa11111")

	auth, err := AuthoriseSingleRepoint(snapshotForTest(t, root), "tc-99999999")
	require.NoError(t, err, "repoint may still retarget wiring whose spec has been removed")
	assert.True(t, auth.IsOrphan())
	assert.Nil(t, auth.Owner())
	assert.Equal(t, "tc-99999999", auth.ID())
}

// ...but an AMBIGUOUS id does not, and neither does an UNPROVABLE one. Nothing
// about the orphan policy relaxes these.
func TestAuthoriseSingleRepoint_AmbiguousAndUnprovableStillRefuse(t *testing.T) {
	root := t.TempDir()
	createTCSpecAt(t, root, "feature-a", "tc-aaa11111")
	createTCSpecAt(t, root, "feature-b", "tc-aaa11111")

	_, err := AuthoriseSingleRepoint(snapshotForTest(t, root), "tc-aaa11111")
	require.Error(t, err, "a duplicated id is Ambiguous, not absent")
	var oe *testcase.OwnershipError
	require.ErrorAs(t, err, &oe)
	assert.Equal(t, testcase.OutcomeAmbiguous, oe.Outcome)

	root2 := t.TempDir()
	dir := filepath.Join(root2, "gtms", "test", "cases", "feature-a")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-bbb22222-broken.md"),
		[]byte("---\ntest_case_id: [unclosed\n---\n"), 0o644))
	_, err2 := AuthoriseSingleRepoint(snapshotForTest(t, root2), "tc-bbb22222")
	require.Error(t, err2, "an unreadable candidate is Unprovable, not absent")
}

// A proven unique owner is carried, not discarded: the decision the write runs
// under names the spec that authorised it.
func TestAuthoriseSingleRepoint_UniqueOwnerIsCarried(t *testing.T) {
	root := t.TempDir()
	createTCSpecAt(t, root, "feature-a", "tc-aaa11111")

	auth := singleAuthForTest(t, root, "tc-aaa11111")
	assert.False(t, auth.IsOrphan())
	require.NotNil(t, auth.Owner())
	assert.Equal(t, "tc-aaa11111", auth.Owner().ID())
	assert.Contains(t, auth.Owner().SpecRel(), "feature-a")
}

// --- CODEX-001: the orphan-repair capability actually works end to end -----

// The POSITIVE control the disposition requires: orphan repair is a real
// capability, not just an outcome the authorisation tolerates. A wiring record
// whose spec has been removed is retargeted to the new adapter and the file on
// disk actually changes.
func TestRepointSingle_OrphanWiringIsActuallyRepaired(t *testing.T) {
	root := seedRepointProject(t)
	orphan := writeOwnedWiring(t, root, "tc-99999999") // no spec anywhere
	before := rawSHA256(t, orphan)

	res, err := RepointSingle(root, "tc-99999999", withSingleAuth(t, root, "tc-99999999",
		RepointOptions{
			Config:      repointTestConfig(),
			FromAdapter: "bats-runner",
			NewAdapter:  "bats-new",
		}))
	require.NoError(t, err, "repointing an orphan is explicit command policy; removing it would take away the only in-place repair for an orphaned record")
	assert.Equal(t, "repointed", res.Status)
	assert.Equal(t, "bats-new", res.NewAdapter)
	assert.NotEqual(t, before, rawSHA256(t, orphan), "the orphan record must actually be rewritten")
}

// Control 2 as it applies to an ORPHAN specifically: absence of an owner does not
// license overwriting a record that declares somebody else. The record here is
// NAMED for the orphan id but its body declares a different test case.
func TestRepointSingle_OrphanCannotOverwriteAForeignIdentityRecord(t *testing.T) {
	root := seedRepointProject(t)
	writeSpecAt(t, root, "feat/tc-88888888-real.md", "tc-88888888")
	foreign := writeWiringRaw(t, root, "tc-99999999--bats.wiring.yaml",
		"testcase: tc-88888888\ntestcase-hash: 1111222233334444\nframework: bats\nadapter: bats-runner\nartefact: tests/a.bats\nartefact-hash: aaaabbbbccccdddd\n")
	before := rawSHA256(t, foreign)

	_, err := RepointSingle(root, "tc-99999999", withSingleAuth(t, root, "tc-99999999",
		RepointOptions{
			Config:      repointTestConfig(),
			FromAdapter: "bats-runner",
			NewAdapter:  "bats-new",
		}))
	require.Error(t, err)
	var wme *WiringIdentityMismatchError
	require.ErrorAs(t, err, &wme, "the orphan path must still refuse a foreign-identity record")
	assert.Equal(t, before, rawSHA256(t, foreign), "nothing may be written to another test case's record")
}

// Control 3: orphan repair is a SINGLE-TC capability. A folder scope selects only
// records whose identity is a proven in-scope owner, so an orphan record in the
// same project is left untouched rather than swept along.
func TestRepointBulk_FolderScopeDoesNotRepairAnOrphan(t *testing.T) {
	root := seedRepointProject(t)
	writeSpecAt(t, root, "feat/tc-11111111-login.md", "tc-11111111")
	owned := writeOwnedWiring(t, root, "tc-11111111")
	orphan := writeOwnedWiring(t, root, "tc-99999999") // no spec anywhere
	ownedBefore := rawSHA256(t, owned)
	orphanBefore := rawSHA256(t, orphan)

	summary, err := RepointBulk(root, "feat", false, withScopeAuth(t, root, "feat", false,
		RepointOptions{
			Config:      repointTestConfig(),
			FromAdapter: "bats-runner",
			NewAdapter:  "bats-new",
		}))
	require.NoError(t, err)
	assert.Equal(t, 1, summary.Repointed, "only the proven in-scope owner's record is repointed")
	assert.NotEqual(t, ownedBefore, rawSHA256(t, owned))
	assert.Equal(t, orphanBefore, rawSHA256(t, orphan),
		"a bulk scope has no orphan-repair policy; the orphan record must be byte-identical")
}
