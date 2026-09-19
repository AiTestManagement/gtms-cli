package adapter

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// NEW-2: BuiltinAutomate's own ownership gate, tested DIRECTLY.
//
// Every fixture that drives automate through Invoke is refused at the invocation
// boundary first, so the adapter's last gate never executes in those tests --
// deleting it wholesale left the whole suite green. Its own comment says "a gate
// that exists only upstream is one a future caller can walk around", and that had
// become true of the gate itself: it was unreachable, so nothing defended it.
//
// These tests call BuiltinAutomate directly, which is exactly the shape of the
// caller the gate exists for -- a future entry point that does not go through
// InvokeWithRoot. They assert the refusal AND that nothing was produced: no
// adapter output, no wiring mutation.

// lastGateFixture builds a project with one normally named spec and returns the
// root, the context BuiltinAutomate would receive, and the planned path.
func lastGateFixture(t *testing.T) (root string, cfg *config.Config, ctx *AdapterContext, planned string) {
	t.Helper()
	root, cfg, _ = ownershipProject(t, "agent-automate", "")
	writeOwnedSpec(t, root, "gtms/test/cases/feat/tc-cccccccc-checkout.md", "tc-cccccccc")
	planned = filepath.Join(root, "test", "acceptance", "feat", "tc-cccccccc-checkout.bats")
	ctx = &AdapterContext{
		ProjectRoot:     root,
		TestCase:        "tc-cccccccc",
		Framework:       "bats",
		TestCaseFile:    "gtms/test/cases/feat/tc-cccccccc-checkout.md",
		OutputSubdir:    "feat/",
		PlannedArtefact: planned,
	}
	return root, cfg, ctx, planned
}

// wiringSnapshot returns every wiring filename and its bytes, so a test can prove
// the refused call mutated none of them.
func wiringSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	dir := filepath.Join(root, "gtms", "automation", "wiring")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(dir, e.Name()))
		require.NoError(t, readErr)
		out[e.Name()] = string(data)
	}
	return out
}

// (NEW-2 a) A foreign-claimed planned path is refused by the adapter itself.
func TestBuiltinAutomate_LastGateRefusesForeignClaimedPlannedPath(t *testing.T) {
	skipIfShort(t)
	root, cfg, ctx, planned := lastGateFixture(t)

	// Another identity's wiring claims exactly the path this call would write.
	rel, err := filepath.Rel(root, planned)
	require.NoError(t, err)
	_, info := seedForeignArtefact(t, root, "tc-dddddddd", "bats", filepath.ToSlash(rel))

	// The claim set is supplied WITHOUT the boundary having reserved anything,
	// which is what a caller bypassing InvokeWithRoot looks like.
	ctx.Claims, err = NewArtefactClaimSet(root)
	require.NoError(t, err)

	before := wiringSnapshot(t, root)
	obs := observeWrites(t)

	_, err = BuiltinAutomate(ctx, cfg)

	require.Error(t, err, "the adapter's own gate must refuse, not rely on an upstream one")
	var claimErr *ForeignArtefactClaimError
	require.True(t, errors.As(err, &claimErr), "want *ForeignArtefactClaimError, got %#v", err)
	assert.Equal(t, "tc-dddddddd", claimErr.Claimant)

	assert.Empty(t, obs.written, "no adapter output may be produced by a refused call")
	assert.Equal(t, before, wiringSnapshot(t, root), "no wiring record may be mutated by a refused call")
	assertIntact(t, planned, info, "the other identity's artefact must be untouched")
}

// (NEW-2 b) Incomplete claim state is refused by the adapter itself, including on
// the ctx.Claims == nil path where the adapter builds the set for itself. A
// corrupt record on disk must not read as "nothing claims this".
func TestBuiltinAutomate_LastGateRefusesUnprovableClaimState(t *testing.T) {
	skipIfShort(t)
	for _, tc := range []struct {
		name   string
		supply bool
	}{
		{"claim set supplied by the caller", true},
		{"no claim set supplied: the adapter builds and validates its own", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, cfg, ctx, planned := lastGateFixture(t)

			require.NoError(t, os.WriteFile(
				filepath.Join(root, "gtms", "automation", "wiring", "tc-dddddddd--bats.wiring.yaml"),
				[]byte("testcase: tc-dddddddd\n\tartefact: [unbalanced\n  : : :\n"), 0o644))

			if tc.supply {
				// A caller CAN only supply a valid set (the constructor refuses),
				// so the corrupt record is introduced after it was built -- the
				// TOCTOU shape. The adapter must still refuse rather than trust a
				// set that no longer matches disk.
				set, err := NewArtefactClaimSet(root)
				require.Error(t, err, "the constructor itself must refuse a corrupt record")
				assert.Nil(t, set)
				ctx.Claims = nil
			}

			before := wiringSnapshot(t, root)
			obs := observeWrites(t)

			_, err := BuiltinAutomate(ctx, cfg)

			require.Error(t, err)
			var unprovable *UnprovableArtefactOwnershipError
			require.True(t, errors.As(err, &unprovable), "want *UnprovableArtefactOwnershipError, got %#v", err)

			assert.Empty(t, obs.written, "no adapter output may be produced when ownership cannot be proved")
			assert.Equal(t, before, wiringSnapshot(t, root), "no wiring record may be mutated")
			assert.NoFileExists(t, planned)
		})
	}
}

// N3: the reservation follows DURABLE STATE, not the invocation's return value.
//
// BuiltinAutomate writes its wiring record and then returns; steps after it can
// still fail. Releasing on that failure would free a path the project's own
// wiring already names, and the next colliding item would reserve it and
// overwrite an artefact whose wiring still points at it.
func TestArtefactReservation_SettlesOnDurableWiringNotOnTheOuterResult(t *testing.T) {
	skipIfShort(t)
	root, _, _ := ownershipProject(t, "agent-automate", "")
	writeOwnedSpec(t, root, "gtms/test/cases/feat/x.md", "tc-aaaa")
	writeOwnedSpec(t, root, "gtms/test/cases/feat/tc-aaaa-x.md", "tc-aaaa-x")
	first, err := testcase.ResolveOwner(root, "tc-aaaa")
	require.NoError(t, err)
	second, err := testcase.ResolveOwner(root, "tc-aaaa-x")
	require.NoError(t, err)

	contested := filepath.Join(root, "test", "acceptance", "feat", "tc-aaaa-x.bats")
	set, err := NewArtefactClaimSet(root)
	require.NoError(t, err)

	// Item one: the wiring record IS written, then the item fails. Simulated at
	// the seam rather than by breaking an unrelated step, so the test states the
	// condition it means: durable state says written, the outer result says failed.
	reservation, err := set.Reserve(first.ID(), "bats", contested)
	require.NoError(t, err)
	require.NotNil(t, reservation)
	require.NoError(t, os.MkdirAll(filepath.Dir(contested), 0o755))
	require.NoError(t, os.WriteFile(contested, []byte("item one's artefact\n"), 0o644))
	_, err = wiring.Write(root, &wiring.WiringRecord{
		TestCase:     first.ID(),
		TestCaseHash: "1111111111111111",
		Framework:    "bats",
		Adapter:      "bats-runner",
		Artefact:     "test/acceptance/feat/tc-aaaa-x.bats",
		ArtefactHash: "2222222222222222",
	})
	require.NoError(t, err)

	reservation.Settle(false) // the invocation failed AFTER the wiring write

	// Item two must still be blocked: the project claims that path.
	_, err = set.Reserve(second.ID(), "bats", contested)
	require.Error(t, err,
		"item one's wiring names this path on disk, so releasing it would let item two overwrite an artefact that is still wired")
	var claimErr *ForeignArtefactClaimError
	require.True(t, errors.As(err, &claimErr), "want *ForeignArtefactClaimError, got %#v", err)
	assert.Equal(t, "tc-aaaa", claimErr.Claimant)

	// The converse: a failure with NO durable wiring really does free the path.
	other := filepath.Join(root, "test", "acceptance", "feat", "tc-cccccccc.bats")
	res2, err := set.Reserve("tc-cccccccc", "bats", other)
	require.NoError(t, err)
	res2.Settle(false)
	res3, err := set.Reserve("tc-eeeeeeee", "bats", other)
	require.NoError(t, err, "nothing durable claims that path, so it is free")
	require.NotNil(t, res3)
}

// N5: a claim's identity is captured when the set is built and stays valid for
// the whole operation. Deleting the file afterwards must not make the claim
// evaporate -- that would be "unprovable rendered as absent" arriving by a
// different route, and an earlier item's own --force sweep is enough to trigger it.
func TestArtefactClaimSet_ClaimSurvivesDeletionOfItsFile(t *testing.T) {
	skipIfShort(t)
	root, _, _ := ownershipProject(t, "agent-automate", "")
	abs, _ := seedForeignArtefact(t, root, "tc-dddddddd", "bats", "test/acceptance/feat/tc-x.bats")

	set, err := NewArtefactClaimSet(root)
	require.NoError(t, err)

	_, _, foreign, err := set.foreignHolder("tc-bbbbbbbb", abs)
	require.NoError(t, err)
	require.True(t, foreign, "precondition: the claim vetoes while the file exists")

	// The file goes away mid-operation. os.SameFile can no longer decide anything.
	require.NoError(t, os.Remove(abs))

	holder, _, foreign, err := set.foreignHolder("tc-bbbbbbbb", abs)
	require.NoError(t, err)
	assert.True(t, foreign,
		"the claim must keep vetoing after its file is deleted: identity was captured when the set was built")
	assert.Equal(t, "tc-dddddddd", holder)

	// And a file recreated at that path by someone else is still that claim's path.
	require.NoError(t, os.WriteFile(abs, []byte("someone else's content\n"), 0o644))
	holder, _, foreign, err = set.foreignHolder("tc-bbbbbbbb", abs)
	require.NoError(t, err)
	assert.True(t, foreign)
	assert.Equal(t, "tc-dddddddd", holder)
}

// N4: Commit must not swallow a failure to re-resolve the committed claim.
// Dropping it would leave the set asserting that a path this operation JUST
// WROTE is unowned, so the next item could reserve and overwrite it.
func TestArtefactClaimSet_CommitFailureMarksTheSetUnprovable(t *testing.T) {
	skipIfShort(t)
	root, _, _ := ownershipProject(t, "agent-automate", "")
	set, err := NewArtefactClaimSet(root)
	require.NoError(t, err)

	// Reserve a path, then make re-resolving it fail by turning its parent into
	// something that cannot be traversed.
	dir := filepath.Join(root, "test", "acceptance", "blocked")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	planned := filepath.Join(dir, "tc-aaaa.bats")
	reservation, err := set.Reserve("tc-aaaa", "bats", planned)
	require.NoError(t, err)
	require.NotNil(t, reservation)

	require.NoError(t, os.Chmod(dir, 0o000))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if _, statErr := os.Stat(planned); statErr == nil || errors.Is(statErr, os.ErrNotExist) {
		t.Skip("this filesystem ignores directory permissions (common on /mnt/c); the failure cannot be provoked here")
	}

	reservation.Commit()

	// The query is deliberately about an UNRELATED, perfectly resolvable path.
	// Asking about the blocked path itself proves nothing: that query fails on
	// its own stat whether or not Commit swallowed anything, so it would pass
	// against a swallowing implementation for the wrong reason. Asking about a
	// path that resolves cleanly isolates the latch: only a set that recorded the
	// failure refuses here.
	unrelated := filepath.Join(root, "test", "acceptance", "feat", "tc-eeeeeeee-unrelated.bats")
	_, _, _, err = set.foreignHolder("tc-bbbbbbbb", unrelated)
	require.Error(t, err,
		"the set answered a query normally after failing to record a claim it had just committed; "+
			"a dropped claim means the next item can reserve and overwrite a path this operation just wrote")
	var unprovable *UnprovableArtefactOwnershipError
	assert.True(t, errors.As(err, &unprovable), "want *UnprovableArtefactOwnershipError, got %#v", err)

	// And the same for the positive authority, which the sweep consults.
	_, err = set.claimedByOwner("tc-aaaa", unrelated)
	assert.Error(t, err, "the sweep must not proceed against a set that lost a claim")
}

// N7: the owner id is stripped only at a token boundary, so a longer word that
// merely starts with the id is not mistaken for it.
func TestDeriveArtefactBasename_StripsOwnerOnlyAtATokenBoundary(t *testing.T) {
	// "tc-aaaabbbb" is not "tc-aaaa" plus a slug; it is a different word.
	assert.Equal(t, "tc-aaaa-tc-aaaabbbb-x",
		DeriveArtefactBasename("gtms/test/cases/feat/tc-aaaabbbb-x.md", "tc-aaaa"),
		"stripping a non-boundary prefix would produce tc-aaaa-bbbb-x and widen the collision surface")

	// The boundary cases still strip, so no normally named spec churns.
	assert.Equal(t, "tc-aaaa-x", DeriveArtefactBasename("gtms/test/cases/feat/tc-aaaa-x.md", "tc-aaaa"))
	assert.Equal(t, "tc-aaaa", DeriveArtefactBasename("gtms/test/cases/feat/tc-aaaa.md", "tc-aaaa"))
	assert.Equal(t, "tc-aaaa-x", DeriveArtefactBasename("gtms/test/cases/feat/tc-aaaa.x.md", "tc-aaaa"))
}

// R3 (ADR-024 s7). The preservation diagnostic renders repository-derived
// FILENAMES into a terminal. A filename may contain any byte except "/" and NUL,
// ESC and LF included, so an unsanitised "%s" lets a hand-edited name rewrite the
// terminal or forge a line that reads like success while the real message says
// something else. This class of defect has already been found and fixed three
// times in this branch's area (REV-159 CODEX-005, REV-160 CLAUDE-007, REV-160
// F1), so the renderer is pinned directly.
//
// It is tested through the renderer rather than through a fixture because /mnt/c
// (NTFS via drvfs) cannot hold a filename containing ESC: a fixture-based test
// would pass vacuously on Windows and only ever run on Linux.
//
// Mutation check: drop SanitizeIdentityString from PreservedArtefactWarning and
// this fails on both the raw control bytes and the forged line count.
func TestPreservedArtefactWarning_SanitisesControlBytesInPaths(t *testing.T) {
	const (
		esc = "\x1b"
		bel = "\x07"
		cr  = "\r"
		lf  = "\n"
	)

	hostile := "test/acceptance/feat/tc-33333333-old" + esc + "[2J" + bel + cr + lf +
		"  ✓ Automated tc-33333333: everything is fine.bats"

	for _, tc := range []struct {
		name   string
		holder string
	}{
		{"unclaimed", ""},
		{"claimed by another identity", "tc-99999999" + esc + "[31m"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := PreservedArtefactWarning(hostile, "tc-33333333"+bel, tc.holder)

			// 1. No control byte survives into the terminal.
			for label, b := range map[string]string{"ESC": esc, "BEL": bel, "CR": cr} {
				assert.NotContains(t, msg, b,
					"the warning emits a raw %s byte from a repository-derived path; "+
						"a hand-edited name can rewrite the terminal.\nmessage: %q", label, msg)
			}

			// 2. An injected LF cannot fabricate a line. The warning is one line.
			assert.Equal(t, 1, len(strings.Split(msg, lf)),
				"the warning renders on more than one line -- an injected newline forged a line.\nmessage:\n%s", msg)

			// 3. Sanitising must not reduce the name to noise, or the operator
			//    cannot find the file the diagnostic is about.
			assert.Contains(t, msg, "test/acceptance/feat/tc-33333333-old",
				"sanitising destroyed the path; the operator cannot locate the file")
			assert.Contains(t, msg, "tc-33333333",
				"sanitising destroyed the target identity")

			// 4. The diagnostic still says the three things it must.
			assert.Contains(t, msg, "was preserved, not removed",
				"the warning must state that the file was PRESERVED")
			if tc.holder == "" {
				assert.Contains(t, msg, "no wiring record owned by",
					"the warning must state that no target-owned wiring authorises removal")
				assert.Contains(t, msg, artefactRepairOptions,
					"the warning must offer the repair options")
			} else {
				assert.Contains(t, msg, "claimed by the wiring record owned by",
					"the warning must name the identity that holds the file")
				assert.Contains(t, msg, "tc-99999999")
			}
		})
	}
}

// R1: the two preservation outcomes are different code paths and must stay
// distinguishable. A stale unprovable file ELSEWHERE in the output directory is
// preserved, reported, and the run still succeeds; the same file ON the planned
// path is a collision and the run refuses before anything is invoked.
func TestForceSweep_PreservedAndReportedVersusPreservedAndRefused(t *testing.T) {
	skipIfShort(t)

	t.Run("preserved and reported: authorised output is still produced", func(t *testing.T) {
		root, cfg, resolved := ownershipProject(t, "agent-automate", "")
		owner := writeOwnedSpec(t, root, "gtms/test/cases/feat/tc-cccccccc-checkout.md", "tc-cccccccc")

		// A stale file the target could have produced, whose wiring is gone.
		stale := filepath.Join(root, "test", "acceptance", "feat", "tc-cccccccc-old-slug.bats")
		require.NoError(t, os.MkdirAll(filepath.Dir(stale), 0o755))
		require.NoError(t, os.WriteFile(stale, []byte("# OLD_CONTENT\n"), 0o644))
		staleInfo, err := os.Stat(stale)
		require.NoError(t, err)

		// An unrelated file that is NOT a name this target produces: it must not
		// be reported, or the diagnostic becomes noise.
		unrelated := filepath.Join(root, "test", "acceptance", "feat", "tc-other99-thing.bats")
		require.NoError(t, os.WriteFile(unrelated, []byte("unrelated\n"), 0o644))

		removed := observeRemovals(t)
		res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-cccccccc",
			CommandFlags{Owner: owner, Framework: "bats", Force: true})

		require.NoError(t, err, "a preserved file elsewhere must not fail the run")
		require.NotNil(t, res)
		assert.NotEqual(t, "error", res.Status, "preservation is informational, not a failure")

		assert.FileExists(t, stale)
		data, err := os.ReadFile(stale)
		require.NoError(t, err)
		assert.Equal(t, "# OLD_CONTENT\n", string(data), "the preserved file must be byte-identical")
		nowInfo, err := os.Stat(stale)
		require.NoError(t, err)
		assert.True(t, os.SameFile(staleInfo, nowInfo))
		assert.NotContains(t, *removed, stale)

		// The authorised artefact was still produced.
		assert.FileExists(t, filepath.Join(root, "test", "acceptance", "feat", "tc-cccccccc-checkout.bats"))

		// Reported, exactly once, and scoped: the unrelated file is not mentioned.
		var mentions int
		for _, w := range res.Warnings {
			if strings.Contains(w, "tc-cccccccc-old-slug.bats") {
				mentions++
				assert.Contains(t, w, "was preserved, not removed")
				assert.Contains(t, w, "no wiring record owned by")
			}
			assert.NotContains(t, w, "tc-other99-thing.bats",
				"an unrelated file is not a candidate under this target's naming rule and must not be reported")
		}
		assert.Equal(t, 1, mentions, "the preserved candidate must be reported exactly once: %v", res.Warnings)
	})

	t.Run("preserved and refused: the planned path is occupied", func(t *testing.T) {
		root, cfg, resolved := ownershipProject(t, "agent-automate", "")
		owner := writeOwnedSpec(t, root, "gtms/test/cases/feat/tc-cccccccc-checkout.md", "tc-cccccccc")

		planned := filepath.Join(root, "test", "acceptance", "feat", "tc-cccccccc-checkout.bats")
		require.NoError(t, os.MkdirAll(filepath.Dir(planned), 0o755))
		require.NoError(t, os.WriteFile(planned, []byte("# UNPROVABLE_OCCUPANT\n"), 0o644))
		before, err := os.Stat(planned)
		require.NoError(t, err)

		removed := observeRemovals(t)
		obs := observeWrites(t)
		_, err = InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-cccccccc",
			CommandFlags{Owner: owner, Framework: "bats", Force: true})

		require.Error(t, err, "the planned path is occupied by a file nothing accounts for: that is a collision")
		var occupied *OccupiedArtefactPathError
		require.True(t, errors.As(err, &occupied), "want *OccupiedArtefactPathError, got %#v", err)
		assert.Equal(t, "test/acceptance/feat/tc-cccccccc-checkout.bats", occupied.Artefact)

		assert.Empty(t, obs.written, "nothing may be invoked")
		assert.Empty(t, *removed, "nothing may be deleted")
		data, err := os.ReadFile(planned)
		require.NoError(t, err)
		assert.Equal(t, "# UNPROVABLE_OCCUPANT\n", string(data))
		now, err := os.Stat(planned)
		require.NoError(t, err)
		assert.True(t, os.SameFile(before, now))
		assertNoPipelineState(t, root)
	})
}
