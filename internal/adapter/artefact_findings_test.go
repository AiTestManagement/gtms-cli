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

	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// --- F1 W1: the reporting-scope predicate, tested directly ------------------

// IsOwnerDerivedArtefactName had no direct test: its only cover was one
// Invoke-level fixture with two files, neither an extended-id sibling nor a slug
// the sanitiser would alter. Gutting the predicate to `return ok && rest != ""`
// -- which reinstates the bare resemblance rule ADR-024 retired, inside the
// reporting scope -- therefore left the whole suite green.
//
// The rows below are chosen so that gutting it cannot stay green: each rejection
// is a name the bare rule accepts.
func TestIsOwnerDerivedArtefactName(t *testing.T) {
	for _, tc := range []struct {
		name    string
		base    string
		ownerID string
		want    bool
		why     string
	}{
		// Positives: names the owner-derived rule actually produces.
		{"bare owner id", "tc-x.bats", "tc-x", true,
			"an unslugged artefact is exactly the owner id"},
		{"owner plus slug", "tc-cccccccc-checkout.bats", "tc-cccccccc", true,
			"the ordinary shape: owner id, separator, sanitised slug"},
		{"dotted slug", "tc-x-v1.2-checkout.bats", "tc-x", true,
			"dots survive the sanitiser, so they are producible"},
		{"compound extension", "tc-x-another.Tests.ps1", "tc-x", true,
			"only the final extension is stripped; the rest is a valid slug"},

		// Rejections. Every one of these is ACCEPTED by a bare
		// `CutPrefix(stem, owner+"-") && rest != ""`.
		{"extended-id sibling", "tc-x--legacy-y.bats", "tc-x", false,
			"tc-x--legacy is a DIFFERENT id (ADR-024 s6); the remainder starts with a dash, which the sanitiser would never emit"},
		{"slug the sanitiser would alter", "tc-x-old slug.bats", "tc-x", false,
			"a space is not producible, so this is not a name tc-x writes"},
		{"internal double dash", "tc-x-a--b.bats", "tc-x", false,
			"the sanitiser collapses `--` to `-`, so `a--b` is not a slug it emits"},
		{"empty slug", "tc-x-.bats", "tc-x", false,
			"owner id plus a bare separator is not a name the rule produces"},
		{"different owner", "tc-other99-thing.bats", "tc-x", false,
			"nothing to do with this target"},
		{"non-boundary prefix", "tc-xxxx-thing.bats", "tc-x", false,
			"tc-xxxx merely starts with tc-x; the owner must end at a separator"},
		{"empty owner", "tc-x-thing.bats", "", false,
			"with no owner there is no owner-derived name"},

		// DOCUMENTED LIMIT, and the reason it is a row rather than a rejection.
		// The review asked that owner tc-aaaa reject "tc-aaaa-bbbb-file" because
		// tc-aaaa-bbbb is itself a valid longer id. It is -- and so is
		// tc-cccccccc-checkout, by exactly the same grammar
		// (^tc-[a-z0-9]+(--?[a-z0-9]+)*$ admits single dashes). No predicate over
		// (base, ownerID) can reject one and keep the other, so rejecting this
		// would delete the primary positive case above and with it the whole
		// reporting feature.
		//
		// The ambiguity is resolved where it can be resolved: by the CLAIM
		// lookup. If tc-aaaa-bbbb really owns this file, the sweep finds its
		// wiring record and reports "claimed by tc-aaaa-bbbb" -- which is the
		// right answer and better than silence. Reporting authorises nothing;
		// only wiring does.
		{"ambiguous longer-id shape", "tc-aaaa-bbbb-file.bats", "tc-aaaa", true,
			"grammatically identical to the ordinary owner+slug shape; disambiguated by the claim lookup, not by the name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, IsOwnerDerivedArtefactName(tc.base, tc.ownerID), tc.why)
		})
	}
}

// --- F1 W2: hardlink aliasing, with a REAL os.Link --------------------------

// A hardlink is two different paths naming ONE file, so pathKey can never match
// one to the other. This is the alias form that only a physical-identity
// comparison can see, and it had no test at all.
//
// The proof that carries it is os.SameFile over the FileInfo CAPTURED when the
// claim set was built. That capture is the point: os.SameFile compares stat data
// already recorded in the two values, so it keeps recognising the alias after
// the claimed path is unlinked -- which is what an earlier item's own --force
// sweep does to it mid-operation.
//
// A stored device+inode key used to sit alongside that comparison. It was
// removed as provably redundant (see isSame): it was computed from the same
// FileInfo, so it could never be available when SameFile was not, and on Unix
// SameFile IS dev+ino equality. This test is what confirms the remaining
// comparison genuinely carries the case -- deleting the os.SameFile branch makes
// the post-unlink assertion below fail.
func TestArtefactClaimSet_HardlinkAliasIsTheSameClaimAfterDeletion(t *testing.T) {
	skipIfShort(t)
	root, _, _ := ownershipProject(t, "agent-automate", "")
	claimed, _ := seedForeignArtefact(t, root, "tc-dddddddd", "bats", "test/acceptance/feat/tc-x.bats")
	alias := filepath.Join(filepath.Dir(claimed), "tc-x-alias.bats")

	// Probed, never inferred from the platform: some filesystems refuse links.
	if err := os.Link(claimed, alias); err != nil {
		t.Skipf("this filesystem does not support hardlinks (%v); this run is NOT evidence for "+
			"hardlink aliasing, and a filesystem that supports them remains REQUIRED coverage", err)
	}
	require.NotEqual(t, claimed, alias, "the two paths must differ, or pathKey would carry the test")

	// The two paths canonicalise differently, so pathKey can never match one to
	// the other. Pinned explicitly, because it is what makes the post-unlink
	// assertion below a test of os.SameFile specifically rather than of whichever
	// comparison happens to fire.
	claimedCand, err := resolveCandidate(claimed)
	require.NoError(t, err)
	aliasCand, err := resolveCandidate(alias)
	require.NoError(t, err)
	require.NotEqual(t, claimedCand.pathKey, aliasCand.pathKey,
		"the alias must not share a path key with the claimed path, or this test proves nothing about os.SameFile")

	set, err := NewArtefactClaimSet(root)
	require.NoError(t, err)

	// While both paths exist. This half also warms the comparison on platforms
	// whose os.SameFile loads file identity lazily, which is why it runs first.
	// assert, not require: if this regresses, the post-unlink half below is the
	// more interesting failure and must still be reported.
	holder, _, foreign, err := set.foreignHolder("tc-bbbbbbbb", alias)
	require.NoError(t, err)
	assert.True(t, foreign, "a hardlink of a claimed file is that claim's file")
	assert.Equal(t, "tc-dddddddd", holder)

	// Remove the CLAIMED path. pathKey(alias) != pathKey(claimed), so the only
	// thing left that can prove these are one file is the captured FileInfo.
	require.NoError(t, os.Remove(claimed))

	holder, _, foreign, err = set.foreignHolder("tc-bbbbbbbb", alias)
	require.NoError(t, err)
	assert.True(t, foreign,
		"the claimed path is gone and the alias has a different path key, so only the FileInfo "+
			"captured when the set was built can still prove these are one file -- without it another "+
			"identity's artefact stops being defended the moment its claimed name is unlinked")
	assert.Equal(t, "tc-dddddddd", holder)
}

// --- F2: no forged output line, driven from repository content --------------

// hostileComponent is a directory name carrying an ESC sequence, a real newline
// and a line that imitates the success output. ASCII only in the source: the
// forged glyph is spelled "[check]" rather than the real one.
const hostileComponent = "feat\x1b[2Jx\n  [check] Automated tc-cccccccc: everything is fine"

// makeHostileDir creates a work-item directory whose NAME is hostile, or skips.
// /mnt/c (NTFS via drvfs) cannot hold ESC or newline in a filename, so this is
// probed rather than assumed; on such a host the run announces that it is not
// evidence instead of passing vacuously.
func makeHostileDir(t *testing.T, root, under string) string {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(under), hostileComponent)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Skipf("this filesystem cannot hold a directory name containing ESC or a newline (%v); "+
			"this run is NOT evidence for the forged-line criterion, and a filesystem that can "+
			"(ext4, apfs) remains REQUIRED coverage", err)
	}
	return dir
}

// assertNoForgedLine is the assertion the review asked for: not merely that
// bytes were replaced, but that the rendered diagnostic is still the number of
// lines it is supposed to be, and that the forged success text never begins one.
func assertNoForgedLine(t *testing.T, msg string, wantLines int) {
	t.Helper()
	for label, b := range map[string]string{"ESC": "\x1b", "LF": "\n", "CR": "\r"} {
		assert.NotContains(t, msg, b,
			"the diagnostic emits a raw %s byte from repository content.\nmessage: %q", label, msg)
	}
	lines := strings.Split(msg, "\n")
	assert.Equal(t, wantLines, len(lines),
		"the diagnostic renders %d lines, want %d -- repository content forged a line.\nmessage:\n%s",
		len(lines), wantLines, msg)
	for _, line := range lines {
		assert.False(t, strings.HasPrefix(strings.TrimSpace(line), "[check] Automated"),
			"a line of the diagnostic begins with forged success text.\nmessage:\n%s", msg)
	}
}

// The end-to-end control. A hostile WORK-ITEM DIRECTORY flows
// deriveOutputSubdir -> PlanArtefactPath -> claims.display(planned) -> the
// error, so the path reaching the terminal is repository content and nothing in
// the test constructs the error by hand.
func TestForeignArtefactClaim_HostileWorkItemDirectoryCannotForgeALine(t *testing.T) {
	skipIfShort(t)
	root, cfg, resolved := ownershipProject(t, "agent-automate", "")
	makeHostileDir(t, root, "gtms/test/cases")

	specRel := "gtms/test/cases/" + hostileComponent + "/tc-cccccccc-checkout.md"
	owner := writeOwnedSpec(t, root, specRel, "tc-cccccccc")

	// Another identity claims exactly the path this run would write.
	makeHostileDir(t, root, "test/acceptance")
	artefactRel := "test/acceptance/" + hostileComponent + "/tc-cccccccc-checkout.bats"
	seedForeignArtefact(t, root, "tc-dddddddd", "bats", artefactRel)

	_, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-cccccccc",
		CommandFlags{Owner: owner, Framework: "bats", Force: true})

	require.Error(t, err)
	var claimErr *ForeignArtefactClaimError
	require.True(t, errors.As(err, &claimErr), "want *ForeignArtefactClaimError, got %#v", err)
	require.Contains(t, claimErr.Artefact, "\x1b",
		"fixture check: the raw struct field must carry the hostile bytes, or the test proves nothing")

	assertNoForgedLine(t, err.Error(), 1)
	assert.Contains(t, err.Error(), "tc-cccccccc-checkout.bats",
		"sanitising must not destroy the path the operator has to find")
}

// The same control for the other type, whose Reason embeds a repository-derived
// wiring FILENAME twice: once directly and once inside the wrapped parser error.
func TestUnprovableOwnership_HostileWiringFilenameCannotForgeALine(t *testing.T) {
	skipIfShort(t)
	root, cfg, resolved := ownershipProject(t, "agent-automate", "")
	owner := writeOwnedSpec(t, root, "gtms/test/cases/feat/tc-cccccccc-checkout.md", "tc-cccccccc")

	hostileName := hostileComponent + wiring.FileSuffix
	if err := os.WriteFile(filepath.Join(root, "gtms", "automation", "wiring", hostileName),
		[]byte("testcase: tc-dddddddd\n\tartefact: [unbalanced\n"), 0o644); err != nil {
		t.Skipf("this filesystem cannot hold a filename containing ESC or a newline (%v); "+
			"this run is NOT evidence for the forged-line criterion", err)
	}

	_, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-cccccccc",
		CommandFlags{Owner: owner, Framework: "bats", Force: true})

	require.Error(t, err)
	var unprovable *UnprovableArtefactOwnershipError
	require.True(t, errors.As(err, &unprovable), "want *UnprovableArtefactOwnershipError, got %#v", err)
	require.Contains(t, unprovable.Reason, "\x1b", "fixture check: the raw Reason must carry the hostile bytes")

	assertNoForgedLine(t, err.Error(), 1)
}

// --- F3: a contested artefact is never removed, and the refusal is atomic ---

// Two wiring records naming ONE file is a data-integrity fault this command
// cannot adjudicate. It used to be resolved silently in favour of whoever ran:
// claimedByOwner was asked first and deleted on yes, so the other identity's
// claim was never consulted. That was the sharpest asymmetry in the design --
// an UNCLAIMED look-alike was preserved and reported, while a file two
// identities actively claimed was destroyed without a word.
func TestForceSweep_ContestedArtefactSurvivesAndRefusesAtomically(t *testing.T) {
	skipIfShort(t)
	root, cfg, resolved := ownershipProject(t, "agent-automate", "")
	owner := writeOwnedSpec(t, root, "gtms/test/cases/feat/tc-cccccccc-checkout.md", "tc-cccccccc")

	// A file the target's own wiring claims and nothing else does. It sorts
	// BEFORE the contested file, so a sweep that removed as it walked would
	// already have deleted this one by the time it refused.
	sole, soleInfo := seedForeignArtefact(t, root, "tc-cccccccc", "legacy1",
		"test/acceptance/feat/tc-cccccccc-aaa-sole.bats")

	// The contested file: claimed by the target under one framework and by
	// another identity under another. Hand-edited wiring is exactly how this
	// arises in the field.
	contested, contestedInfo := seedForeignArtefact(t, root, "tc-cccccccc", "playwright",
		"test/acceptance/feat/tc-cccccccc-shared.bats")
	_, err := wiring.Write(root, &wiring.WiringRecord{
		TestCase:     "tc-dddddddd",
		TestCaseHash: "5555555555555555",
		Framework:    "bats",
		Adapter:      "bats-runner",
		Artefact:     "test/acceptance/feat/tc-cccccccc-shared.bats",
		ArtefactHash: "6666666666666666",
	})
	require.NoError(t, err)
	require.Less(t, filepath.Base(sole), filepath.Base(contested),
		"fixture check: the removable file must sort first, or atomicity is untested")

	removed := observeRemovals(t)
	obs := observeWrites(t)

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-cccccccc",
		CommandFlags{Owner: owner, Framework: "bats", Force: true})

	require.Error(t, err, "a contested artefact must refuse, not be deleted on one of the two claims")
	var contestedErr *ContestedArtefactError
	require.True(t, errors.As(err, &contestedErr), "want *ContestedArtefactError, got %#v", err)
	assert.Equal(t, "tc-cccccccc", contestedErr.Owner)
	assert.Equal(t, "tc-dddddddd", contestedErr.Claimant)
	assert.Equal(t, "test/acceptance/feat/tc-cccccccc-shared.bats", contestedErr.Artefact)
	assert.Nil(t, res)

	assertIntact(t, contested, contestedInfo, "the contested artefact must survive byte-identical")
	assertIntact(t, sole, soleInfo,
		"the refusal must be ATOMIC: a file the sweep had already planned to remove, and would have "+
			"reached first, must still be there")
	assert.Empty(t, *removed, "an atomic refusal removes nothing at all, not merely nothing further")
	assert.Empty(t, obs.written, "the adapter must not run")
}

// --- F5: the occupied-path refusal at the direct built-in boundary ----------

// BuiltinAutomate re-asserted the foreign-claim half of the boundary's check but
// not the occupied-path half, so a caller bypassing InvokeWithRoot could still
// overwrite a file nothing accounts for. The argument that justifies the gate
// existing at all applies to both halves or to neither.
func TestBuiltinAutomate_LastGateRefusesOccupiedPlannedPath(t *testing.T) {
	skipIfShort(t)
	root, cfg, ctx, planned := lastGateFixture(t)

	require.NoError(t, os.MkdirAll(filepath.Dir(planned), 0o755))
	require.NoError(t, os.WriteFile(planned, []byte("# UNACCOUNTED_FOR_OCCUPANT\n"), 0o644))
	before, err := os.Stat(planned)
	require.NoError(t, err)

	ctx.Claims, err = NewArtefactClaimSet(root)
	require.NoError(t, err)

	beforeWiring := wiringSnapshot(t, root)
	obs := observeWrites(t)

	_, err = BuiltinAutomate(ctx, cfg)

	require.Error(t, err, "the adapter's own gate must refuse an occupied planned path, not rely on an upstream one")
	var occupied *OccupiedArtefactPathError
	require.True(t, errors.As(err, &occupied), "want *OccupiedArtefactPathError, got %#v", err)
	assert.Equal(t, "tc-cccccccc", occupied.Owner)

	assert.Empty(t, obs.written, "no adapter output may be produced by a refused call")
	assert.Equal(t, beforeWiring, wiringSnapshot(t, root), "no wiring record may be mutated")
	data, err := os.ReadFile(planned)
	require.NoError(t, err)
	assert.Equal(t, "# UNACCOUNTED_FOR_OCCUPANT\n", string(data))
	now, err := os.Stat(planned)
	require.NoError(t, err)
	assert.True(t, os.SameFile(before, now), "the occupant was replaced, not preserved")
}

// A file the TARGET's own wiring accounts for is not "occupied": that is an
// ordinary re-automate, and the new check must not break it.
func TestBuiltinAutomate_LastGateAllowsOwnClaimedPlannedPath(t *testing.T) {
	skipIfShort(t)
	root, cfg, ctx, planned := lastGateFixture(t)

	rel, err := filepath.Rel(root, planned)
	require.NoError(t, err)
	seedForeignArtefact(t, root, "tc-cccccccc", "bats", filepath.ToSlash(rel))

	ctx.Claims, err = NewArtefactClaimSet(root)
	require.NoError(t, err)

	res, err := BuiltinAutomate(ctx, cfg)
	require.NoError(t, err, "re-automating over the target's OWN wired artefact must still work")
	require.NotNil(t, res)
	assert.Equal(t, []string{planned}, res.SavedFiles)
}

// --- O2: the widened refusal surface, pinned as chosen policy ---------------

// Consulting the foreign veto FIRST (F3) widened what the sweep examines:
// foreignHolder now runs on EVERY file the walk sees, not only on names matching
// IsOwnerDerivedArtefactName, and resolveCandidate raises
// UnprovableArtefactOwnershipError on any stat failure that is not "does not
// exist". So an unrelated, unresolvable entry in the output directory -- a
// symlink loop is the easiest to construct -- now aborts the whole automate run,
// where before the patch it was simply skipped.
//
// That is the CHOSEN POLICY, not an accident, and this test exists to pin it
// rather than to soften it. The reasoning is the same fail-closed rule the rest
// of the design follows: a file the process cannot examine is a file whose
// ownership cannot be proved, and proceeding past it means sweeping a directory
// while blind to one of its entries. The cost is an operator-visible refusal on
// a broken output directory; the alternative cost is deleting someone's artefact
// because the entry that would have vetoed it could not be read.
//
// What the test therefore requires is that the refusal be USEFUL: it must name
// the path, say why, say what to do, arrive before anything runs or is deleted,
// and be terminal-safe.
func TestForceSweep_UnexaminableEntryFailsClosedBeforeAnyMutation(t *testing.T) {
	skipIfShort(t)
	root, cfg, resolved := ownershipProject(t, "agent-automate", "")
	owner := writeOwnedSpec(t, root, "gtms/test/cases/feat/tc-cccccccc-checkout.md", "tc-cccccccc")
	outDir := filepath.Join(root, "test", "acceptance", "feat")
	require.NoError(t, os.MkdirAll(outDir, 0o755))

	// A file the target's own wiring claims, named so it sorts BEFORE the broken
	// entry. The sweep plans it for removal and must still not remove it.
	planned, plannedInfo := seedForeignArtefact(t, root, "tc-cccccccc", "legacy1",
		"test/acceptance/feat/aaa-owned-by-target.bats")

	// The unresolvable entry: two symlinks pointing at each other. Probed, never
	// assumed -- a filesystem that cannot express this cannot host the test.
	loopA := filepath.Join(outDir, "loopa")
	loopB := filepath.Join(outDir, "loopb")
	if err := os.Symlink("loopb", loopA); err != nil {
		t.Skipf("this filesystem does not support symlinks (%v); this run is NOT evidence for the "+
			"fail-closed refusal, and a filesystem that supports them remains REQUIRED coverage", err)
	}
	if err := os.Symlink("loopa", loopB); err != nil {
		t.Skipf("this filesystem does not support symlinks (%v); this run is NOT evidence for the "+
			"fail-closed refusal", err)
	}
	if _, statErr := os.Stat(loopA); statErr == nil || errors.Is(statErr, os.ErrNotExist) {
		t.Skipf("this filesystem does not produce an unresolvable stat for a symlink loop (got %v); "+
			"this run is NOT evidence for the fail-closed refusal, and a filesystem that does "+
			"remains REQUIRED coverage", statErr)
	}

	removed := observeRemovals(t)
	obs := observeWrites(t)

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-cccccccc",
		CommandFlags{Owner: owner, Framework: "bats", Force: true})

	// Fails closed.
	require.Error(t, err, "an entry whose ownership cannot be examined must refuse the run")
	var unprovable *UnprovableArtefactOwnershipError
	require.True(t, errors.As(err, &unprovable), "want *UnprovableArtefactOwnershipError, got %#v", err)
	assert.Nil(t, res)

	// Before the adapter, and before any mutation of the output directory.
	assert.Empty(t, obs.written, "the adapter must not run when an entry cannot be examined")
	assert.Empty(t, *removed,
		"nothing may be deleted: the sweep plans the whole directory before removing anything, so a "+
			"file it had already planned to remove survives the refusal")
	assertIntact(t, planned, plannedInfo, "the target's own claimed file must survive untouched")
	assert.FileExists(t, loopA)
	assert.FileExists(t, filepath.Join(root, "gtms", "test", "cases", "feat", "tc-cccccccc-checkout.md"))
	assert.NoFileExists(t, filepath.Join(root, "test", "acceptance", "feat", "tc-cccccccc-checkout.bats"),
		"no artefact may be produced")

	// WHERE the refusal lands, pinned as it actually is. This one comes from the
	// SWEEP, which runs after the task file and result contract are created, so
	// unlike a boundary refusal it does NOT leave zero lifecycle state: the run
	// is recorded as a failed task rather than as never having happened. That is
	// the existing failure lifecycle, not something this policy invents, and it
	// is asserted rather than assumed so a future move of the check is visible.
	taskFiles, _ := filepath.Glob(filepath.Join(root, "gtms", "tasks", "*", "*.md"))
	require.Len(t, taskFiles, 1, "the failed run is recorded as one task file")
	assert.Contains(t, filepath.ToSlash(taskFiles[0]), "/tasks/error/",
		"a swept-refused run belongs in error/, not left in pending/")
	results, _ := filepath.Glob(filepath.Join(root, ".gtms", "results", "*"))
	assert.Len(t, results, 1, "its result contract is written too")

	// And the diagnostic is worth reading: it names the offending path, says why,
	// and says what to do -- on one terminal-safe line.
	msg := err.Error()
	assert.Contains(t, msg, "loopa", "the refusal must name the entry it could not examine")
	assert.Contains(t, msg, "could not be examined", "the refusal must say why")
	assert.Contains(t, msg, "repair or remove that file, then re-run", "the refusal must say what to do")
	assertNoForgedLine(t, msg, 1)
}

// The widened surface is bounded to --force, and that bound is worth pinning:
// without --force there is no sweep, so the same broken directory does not stop
// anything. It says exactly how far the policy reaches.
func TestAutomate_UnexaminableEntryDoesNotAffectRunsWithoutForce(t *testing.T) {
	skipIfShort(t)
	root, cfg, resolved := ownershipProject(t, "agent-automate", "")
	owner := writeOwnedSpec(t, root, "gtms/test/cases/feat/tc-cccccccc-checkout.md", "tc-cccccccc")
	outDir := filepath.Join(root, "test", "acceptance", "feat")
	require.NoError(t, os.MkdirAll(outDir, 0o755))

	if err := os.Symlink("loopb", filepath.Join(outDir, "loopa")); err != nil {
		t.Skipf("this filesystem does not support symlinks (%v); this run is NOT evidence for the bound", err)
	}
	if err := os.Symlink("loopa", filepath.Join(outDir, "loopb")); err != nil {
		t.Skipf("this filesystem does not support symlinks (%v); this run is NOT evidence for the bound", err)
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-cccccccc",
		CommandFlags{Owner: owner, Framework: "bats"})

	require.NoError(t, err, "no --force means no sweep, so an unexaminable entry is never reached")
	require.NotNil(t, res)
	assert.FileExists(t, filepath.Join(outDir, "tc-cccccccc-checkout.bats"))
}
