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

// Artefact naming and destructive-cleanup authority.
//
// The defect: automate derived its artefact basename from the spec FILENAME.
// Under ADR-024 clause 1a a spec's identity is its body test_case_id and a
// disagreeing filename is a valid RENAMED OWNER, so a spec at
// tc-aaaaaaaa-file.md declaring test_case_id: tc-bbbbbbbb produced
// tc-bbbbbbbb's artefact under the name tc-aaaaaaaa-file.bats. The --force
// sweep then decided ownership by basename prefix, so automating the unrelated
// tc-aaaaaaaa deleted it. That is BUG-185's failure class.
//
// These seven contracts are written to be DISCRIMINATING: each one is designed
// so that a plausible wrong implementation -- a filename-derived name, a
// post-hoc rename, a recomputing fallback that makes the boundary's planning
// dead code, a fail-open wiring scan, a sweep pointed at the wrong directory, or
// a path comparison done on strings -- fails it.
//
// The two SEAMS the tests observe through:
//
//   - the FrameworkSupport registry. BuiltinAutomate writes the artefact only
//     via support.GenerateSkeleton(outPath), so replacing the registered support
//     records WHICH PATH the implementation selected for the write, at the
//     moment it selects it. That is the distinction that matters: a post-hoc
//     rename and a correct implementation both end with the right file on disk,
//     and differ only in which path they opened.
//   - removeArtefactFile, the single indirection through which the lifecycle
//     deletes a file. It records which paths an implementation chose to destroy,
//     which "the file still exists" cannot: a remove-and-recreate satisfies the
//     survival assertion and is not the same program.
//
// A read-only parent directory was deliberately NOT used. It turns a violation
// into a generic permission failure, which shows that something failed but not
// which path the implementation chose -- and that is the whole question.

const ownershipFixtureConfig = `project:
  name: ownership-fixture
  repo: org/test
adapters:
  execute:
    bats-runner:
      mode: sync
      script: run.sh
      framework: bats
defaults:
  execute: bats-runner
`

// --- seams ---------------------------------------------------------------

// writeObserver records every path the adapter is ASKED to write, and runs an
// optional assertion at the instant of each write.
type writeObserver struct {
	inner FrameworkSupport
	// written is the ordered list of paths the adapter was asked to write.
	written []string
	// writtenBy is the identity each of those writes was made FOR, recorded in
	// step with written. Two items of one batch writing the same path is only
	// visible as damage if the observer can say who each write belonged to.
	writtenBy []string
	onWrite   func(outPath string)
	writeErr  error
}

func (w *writeObserver) Extension() string              { return w.inner.Extension() }
func (w *writeObserver) OutputDir(subdir string) string { return w.inner.OutputDir(subdir) }
func (w *writeObserver) TemplatePath(projectRoot string) string {
	return w.inner.TemplatePath(projectRoot)
}
func (w *writeObserver) FallbackContent() string { return w.inner.FallbackContent() }

func (w *writeObserver) GenerateSkeleton(tcID, projectRoot, outPath, templateBody string) error {
	w.written = append(w.written, outPath)
	w.writtenBy = append(w.writtenBy, tcID)
	if w.onWrite != nil {
		w.onWrite(outPath)
	}
	if w.writeErr != nil {
		return w.writeErr
	}
	return w.inner.GenerateSkeleton(tcID, projectRoot, outPath, templateBody)
}

// observeWrites swaps the bats FrameworkSupport for a recorder and restores it
// when the test ends. It is the write seam.
func observeWrites(t *testing.T) *writeObserver {
	t.Helper()
	original := frameworkRegistry["bats"]
	obs := &writeObserver{inner: original}
	frameworkRegistry["bats"] = obs
	t.Cleanup(func() { frameworkRegistry["bats"] = original })
	return obs
}

// observeRemovals swaps removeArtefactFile for a recorder. It is the delete seam.
func observeRemovals(t *testing.T) *[]string {
	t.Helper()
	original := removeArtefactFile
	var removed []string
	removeArtefactFile = func(path string) error {
		removed = append(removed, path)
		return original(path)
	}
	t.Cleanup(func() { removeArtefactFile = original })
	return &removed
}

// --- fixture -------------------------------------------------------------

// ownershipProject builds a project root that InvokeWithRoot can drive end to
// end, and returns the resolved Tier 0 automate adapter for it.
//
// adapterName is deliberately a parameter: the guard must key off the DISPATCH
// CLASSIFICATION (tier 0 + command "automate"), which is what invokeBuiltinAction
// switches on, and not off a built-in adapter NAME. A gtms.config entry with no
// command/script/module resolves to tier 0 under any name its author picked.
func ownershipProject(t *testing.T, adapterName, outputDir string) (string, *config.Config, *ResolvedAdapter) {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "gtms.config"), []byte(ownershipFixtureConfig), 0o644))
	for _, dir := range []string{
		"gtms/tasks/pending", "gtms/tasks/in-progress", "gtms/tasks/in-review",
		"gtms/tasks/complete", "gtms/tasks/error",
		"gtms/automation/wiring", "gtms/test/cases/feat",
		".gtms/results", ".gtms/worktrees", ".gtms/logs",
	} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0o755))
	}
	cfg, err := config.LoadFromFile(filepath.Join(root, "gtms.config"))
	require.NoError(t, err)
	resolved := &ResolvedAdapter{
		Command: "automate",
		Name:    adapterName,
		Config:  &config.AdapterConfig{Mode: "sync", OutputDir: outputDir},
		Tier:    0,
		Mode:    "sync",
	}
	return root, cfg, resolved
}

// writeOwnedSpec writes a spec at relPath whose BODY declares bodyID. The two
// are independent on purpose: that disagreement is the renamed-owner case
// ADR-024 permits, and it is what made the artefact name a lie.
func writeOwnedSpec(t *testing.T, root, relPath, bodyID string) *testcase.Owner {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(relPath))
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	body := "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: " + bodyID + "\ntitle: Ownership fixture\npriority: Medium\n---\n\n## Test Objective\n\nOwnership fixture spec.\n"
	require.NoError(t, os.WriteFile(abs, []byte(body), 0o644))
	owner, err := testcase.ResolveOwner(root, bodyID)
	require.NoError(t, err, "resolving the owner just written")
	return owner
}

const distinctiveBytes = "#!/usr/bin/env bats\n# DISTINCTIVE: this is another identity's real test body\n@test \"do not destroy me\" { true; }\n"

// seedForeignArtefact drops distinctive bytes at relPath and has claimant's
// wiring claim them. Returns the absolute path and its FileInfo, so a later
// assertion can prove the SAME INODE survived rather than merely a file with the
// same contents.
func seedForeignArtefact(t *testing.T, root, claimant, framework, relPath string) (string, os.FileInfo) {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(relPath))
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.WriteFile(abs, []byte(distinctiveBytes), 0o644))
	_, err := wiring.Write(root, &wiring.WiringRecord{
		TestCase:     claimant,
		TestCaseHash: "1111111111111111",
		Framework:    framework,
		Adapter:      "bats-runner",
		Artefact:     relPath,
		ArtefactHash: "2222222222222222",
	})
	require.NoError(t, err)
	info, err := os.Stat(abs)
	require.NoError(t, err)
	return abs, info
}

// assertIntact proves the file is byte-for-byte AND inode-for-inode what it was.
// The inode half is what makes remove-and-recreate visible.
func assertIntact(t *testing.T, path string, before os.FileInfo, why string) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err, why)
	assert.Equal(t, distinctiveBytes, string(data), why)
	now, err := os.Stat(path)
	require.NoError(t, err, why)
	assert.True(t, os.SameFile(before, now), "%s (the file was replaced, not preserved)", why)
}

func assertNoPipelineState(t *testing.T, root string) {
	t.Helper()
	for _, glob := range []string{"gtms/tasks/*/*.md", ".gtms/results/*"} {
		matches, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(glob)))
		assert.Empty(t, matches, "a refusal must leave no %s behind", glob)
	}
}

func automateFlags(owner *testcase.Owner, force bool) CommandFlags {
	return CommandFlags{Owner: owner, Framework: "bats", Force: force}
}

// --- (1) owner-derived naming -------------------------------------------

// The artefact is named for the identity that OWNS it, and the legacy
// filename-derived path is never created. The two paths DIFFER here, which is
// what makes the assertion meaningful: an implementation that wrote at the
// filename-derived path and renamed afterwards would leave the same final file,
// so the write seam -- not the final listing -- is the discriminator.
func TestOwnerDerivedNaming_RenamedOwnerWritesOnlyOwnerDerivedPath(t *testing.T) {
	skipIfShort(t)
	root, cfg, resolved := ownershipProject(t, "agent-automate", "")
	owner := writeOwnedSpec(t, root, "gtms/test/cases/feat/tc-aaaaaaaa-file.md", "tc-bbbbbbbb")
	obs := observeWrites(t)

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-bbbbbbbb", automateFlags(owner, false))
	require.NoError(t, err)
	require.NotNil(t, res)

	ownerDerived := filepath.Join(root, "test", "acceptance", "feat", "tc-bbbbbbbb-tc-aaaaaaaa-file.bats")
	legacyDerived := filepath.Join(root, "test", "acceptance", "feat", "tc-aaaaaaaa-file.bats")
	require.NotEqual(t, ownerDerived, legacyDerived, "the fixture must make the two candidate names differ")

	assert.Equal(t, []string{ownerDerived}, obs.written,
		"the adapter must be asked to write the OWNER-derived path, once, and no other")
	assert.FileExists(t, ownerDerived)
	assert.NoFileExists(t, legacyDerived,
		"no artefact may ever exist under an id the owner does not hold")

	rec, _, err := wiring.Find(root, "tc-bbbbbbbb", "bats")
	require.NoError(t, err)
	require.NotNil(t, rec)
	assert.Equal(t, "test/acceptance/feat/tc-bbbbbbbb-tc-aaaaaaaa-file.bats", rec.Artefact)
}

// --- (2) the carrier is load-bearing ------------------------------------

// The planned path is computed at the invocation boundary and CARRIED to the
// adapter. Without it the adapter refuses: it has no second way to decide where
// to write, so the boundary's planning cannot become dead code that a mutation
// could delete unnoticed.
func TestPlannedArtefactCarrier_IsMandatoryForTier0(t *testing.T) {
	skipIfShort(t)

	t.Run("missing carrier refuses and writes nothing", func(t *testing.T) {
		root, cfg, _ := ownershipProject(t, "agent-automate", "")
		writeOwnedSpec(t, root, "gtms/test/cases/feat/tc-cccccccc-checkout.md", "tc-cccccccc")
		obs := observeWrites(t)

		_, err := BuiltinAutomate(&AdapterContext{
			ProjectRoot:  root,
			TestCase:     "tc-cccccccc",
			Framework:    "bats",
			TestCaseFile: "gtms/test/cases/feat/tc-cccccccc-checkout.md",
			OutputSubdir: "feat/",
			// PlannedArtefact deliberately empty.
		}, cfg)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "no planned artefact path")
		assert.Empty(t, obs.written, "nothing may be written without a planned, ownership-checked destination")
		matches, _ := filepath.Glob(filepath.Join(root, "gtms", "automation", "wiring", "*"))
		assert.Empty(t, matches)
	})

	// The gate is the dispatch classification, not the adapter's name: a config
	// entry with no command/script/module is tier 0 whatever it is called, and
	// must be planned and guarded exactly like agent-automate.
	t.Run("tier 0 automate under a custom adapter name is still planned", func(t *testing.T) {
		root, cfg, resolved := ownershipProject(t, "house-automate", "")
		owner := writeOwnedSpec(t, root, "gtms/test/cases/feat/tc-aaaaaaaa-file.md", "tc-bbbbbbbb")
		obs := observeWrites(t)

		_, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-bbbbbbbb", automateFlags(owner, false))
		require.NoError(t, err)
		assert.Equal(t, []string{filepath.Join(root, "test", "acceptance", "feat", "tc-bbbbbbbb-tc-aaaaaaaa-file.bats")},
			obs.written, "the planner must key off tier+command, not the adapter name")
	})
}

// --- (3) the foreign path is never opened -------------------------------

// A THIRD identity's artefact sits at exactly the path the old filename-derived
// rule would have chosen, holding distinctive bytes. Running automate for the
// renamed owner -- with --force, so the sweep runs too -- must never open that
// path for writing and never select it for removal.
//
// The assertion is made at the instant of the write, not only afterwards, so an
// implementation that wrote there and renamed away cannot pass by tidying up.
func TestForeignClaimedLegacyPath_IsNeverOpenedForWriteOrRemoval(t *testing.T) {
	skipIfShort(t)
	root, cfg, resolved := ownershipProject(t, "agent-automate", "")
	owner := writeOwnedSpec(t, root, "gtms/test/cases/feat/tc-aaaaaaaa-file.md", "tc-bbbbbbbb")
	legacy, legacyInfo := seedForeignArtefact(t, root, "tc-dddddddd", "bats",
		"test/acceptance/feat/tc-aaaaaaaa-file.bats")

	removed := observeRemovals(t)
	obs := observeWrites(t)
	obs.onWrite = func(outPath string) {
		assert.NotEqual(t, legacy, outPath, "the adapter was pointed at another identity's artefact")
		assertIntact(t, legacy, legacyInfo, "the foreign artefact must be untouched at the moment of the write")
	}

	_, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-bbbbbbbb", automateFlags(owner, true))
	require.NoError(t, err)

	ownerDerived := filepath.Join(root, "test", "acceptance", "feat", "tc-bbbbbbbb-tc-aaaaaaaa-file.bats")
	assert.Equal(t, []string{ownerDerived}, obs.written)
	assert.NotContains(t, *removed, legacy, "the foreign artefact was selected for deletion")
	assertIntact(t, legacy, legacyInfo, "the foreign artefact must survive the whole run unchanged")

	rec, _, err := wiring.Find(root, "tc-dddddddd", "bats")
	require.NoError(t, err)
	require.NotNil(t, rec)
	assert.Equal(t, "test/acceptance/feat/tc-aaaaaaaa-file.bats", rec.Artefact,
		"the third identity's wiring must still point at its own artefact")
}

// --- (4) sweep scope ------------------------------------------------------

// The --force sweep must run in the directory the write actually lands in. With
// output-dir UNSET that is the framework-native test/acceptance/, while the
// adapter context's OutputDir is the gtms/automation/specs/<adapter>/ default --
// a sweep scoped from the latter walks a directory the artefact has never been
// in, and contract 3 silently does nothing at all. Both configurations are
// driven end to end.
func TestForceCleanup_ScopedToPlannedOutputLocationAndAuthorisedByWiring(t *testing.T) {
	skipIfShort(t)
	for _, tc := range []struct {
		name      string
		outputDir string
		outRel    string
	}{
		{"output-dir unset (framework default)", "", "test/acceptance/feat"},
		{"output-dir configured (brownfield)", "spec/e2e", "spec/e2e/feat"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, cfg, resolved := ownershipProject(t, "agent-automate", tc.outputDir)
			owner := writeOwnedSpec(t, root, "gtms/test/cases/feat/tc-aaaaaaaa-file.md", "tc-bbbbbbbb")

			// The owner's OWN legacy artefact, still claimed by its own wiring
			// under a second framework so the claim survives the new bats write.
			ownLegacy, _ := seedForeignArtefact(t, root, "tc-bbbbbbbb", "legacy-bats",
				tc.outRel+"/tc-aaaaaaaa-file.bats")
			// A third identity's artefact in the same directory.
			foreign, foreignInfo := seedForeignArtefact(t, root, "tc-dddddddd", "bats",
				tc.outRel+"/tc-dddddddd-other.bats")
			// An unclaimed file whose name prefix-matches the target.
			stray := filepath.Join(root, filepath.FromSlash(tc.outRel), "tc-bbbbbbbb-stray.bats")
			require.NoError(t, os.WriteFile(stray, []byte("stray"), 0o644))

			removed := observeRemovals(t)
			_, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-bbbbbbbb", automateFlags(owner, true))
			require.NoError(t, err)

			assert.NoFileExists(t, ownLegacy,
				"the owner's OWN wiring claims this file, so --force must sweep it -- and it can only be reached by sweeping the directory the write lands in")
			assert.Contains(t, *removed, ownLegacy)
			assertIntact(t, foreign, foreignInfo, "another identity's artefact is never collateral")
			assert.FileExists(t, stray,
				"nothing claims this file, so nothing proves it is the target's to delete")
			assert.FileExists(t, filepath.Join(root, filepath.FromSlash(tc.outRel), "tc-bbbbbbbb-tc-aaaaaaaa-file.bats"))
		})
	}
}

// --- (5) unprovable ownership --------------------------------------------

// A wiring record that cannot be parsed does not mean "unclaimed"; it means the
// claim that would have vetoed this run may be the one that could not be read.
// The fixture is built so the ONLY claim on the planned path lives in the
// unreadable record, which is what makes the fail-open failure mode reachable:
// with the corrupt record skipped, the path looks free and the adapter
// OVERWRITES it -- the same data loss as a deletion, arrived at by writing.
//
// Both mutating paths must be blocked, and the refusal must land before the
// adapter runs and before any filesystem mutation.
func TestUnprovableWiring_RefusesBeforeAdapterExecutionAndBeforeAnyMutation(t *testing.T) {
	skipIfShort(t)
	root, cfg, resolved := ownershipProject(t, "agent-automate", "")
	owner := writeOwnedSpec(t, root, "gtms/test/cases/feat/tc-aaaaaaaa-file.md", "tc-bbbbbbbb")

	// The artefact the run is about to write to, holding another identity's bytes.
	planned := filepath.Join(root, "test", "acceptance", "feat", "tc-bbbbbbbb-tc-aaaaaaaa-file.bats")
	require.NoError(t, os.MkdirAll(filepath.Dir(planned), 0o755))
	require.NoError(t, os.WriteFile(planned, []byte(distinctiveBytes), 0o644))
	plannedInfo, err := os.Stat(planned)
	require.NoError(t, err)

	// Its ONLY claim is inside a record that cannot be parsed.
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "gtms", "automation", "wiring", "tc-dddddddd--bats.wiring.yaml"),
		[]byte("testcase: tc-dddddddd\n\tartefact: [unbalanced\n  : : :\n"), 0o644))

	removed := observeRemovals(t)
	obs := observeWrites(t)

	_, err = InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-bbbbbbbb", automateFlags(owner, true))

	// The consequence is asserted before the refusal, so a fail-open scan reports
	// the DAMAGE it caused and not merely a missing error.
	assert.Empty(t, obs.written, "the adapter must not run when ownership cannot be proved")
	assert.Empty(t, *removed, "nothing may be deleted when ownership cannot be proved")
	assertIntact(t, planned, plannedInfo, "the artefact whose only claim sits in the corrupt record must not be overwritten")
	assertNoPipelineState(t, root)

	require.Error(t, err, "an unprovable wiring index must refuse the run")
	var unprovable *UnprovableArtefactOwnershipError
	require.True(t, errors.As(err, &unprovable), "want *UnprovableArtefactOwnershipError, got %#v", err)
}

// --- (6) physical file identity ------------------------------------------

// Claims are matched by physical file identity, not by path spelling. A path
// that reaches the same file by a different route -- a redundant traversal, a
// symlinked directory, or (where the filesystem allows it) a different case --
// is the SAME claim, and a string comparison gets that wrong.
func TestArtefactClaims_MatchByPhysicalFileIdentity(t *testing.T) {
	skipIfShort(t)

	t.Run("alias path through a redundant traversal", func(t *testing.T) {
		root, _, _ := ownershipProject(t, "agent-automate", "")
		abs, _ := seedForeignArtefact(t, root, "tc-dddddddd", "bats", "test/acceptance/feat/tc-x.bats")
		claims, err := NewArtefactClaimSet(root)
		require.NoError(t, err)

		alias := filepath.Join(filepath.Dir(abs), "..", "feat", filepath.Base(abs))
		holder, _, foreign, err := claims.foreignHolder("tc-bbbbbbbb", alias)
		require.NoError(t, err)
		assert.True(t, foreign, "an alias of a claimed path is still that claim's file")
		assert.Equal(t, "tc-dddddddd", holder)
	})

	// F7: a candidate spelled RELATIVE to the process working directory must
	// still match an absolute claim. Both sides are absolutised through
	// absoluteCleanPath, so they cannot miss each other on spelling alone. This
	// is weighed as a safety defect rather than a tidiness one: the failure shape
	// is a SILENT MISS on the veto -- the sweep would conclude "unclaimed" and
	// delete another identity's artefact -- which is the exact shape this branch
	// exists to remove.
	t.Run("candidate spelled relative to the working directory", func(t *testing.T) {
		root, _, _ := ownershipProject(t, "agent-automate", "")
		abs, _ := seedForeignArtefact(t, root, "tc-dddddddd", "bats", "test/acceptance/feat/tc-x.bats")
		claims, err := NewArtefactClaimSet(root)
		require.NoError(t, err)

		wd, err := os.Getwd()
		require.NoError(t, err)
		require.NoError(t, os.Chdir(root))
		t.Cleanup(func() { _ = os.Chdir(wd) })

		rel, err := filepath.Rel(root, abs)
		require.NoError(t, err)
		require.False(t, filepath.IsAbs(rel))

		holder, _, foreign, err := claims.foreignHolder("tc-bbbbbbbb", rel)
		require.NoError(t, err)
		assert.True(t, foreign, "a relative spelling of a claimed path is still that claim's file")
		assert.Equal(t, "tc-dddddddd", holder)

		owned, err := claims.claimedByOwner("tc-dddddddd", rel)
		require.NoError(t, err)
		assert.True(t, owned, "the positive authority must match on a relative spelling too")
	})

	t.Run("symlinked directory", func(t *testing.T) {
		root, _, _ := ownershipProject(t, "agent-automate", "")
		abs, _ := seedForeignArtefact(t, root, "tc-dddddddd", "bats", "test/acceptance/feat/tc-x.bats")
		link := filepath.Join(root, "linked-feat")
		if err := os.Symlink(filepath.Dir(abs), link); err != nil {
			t.Skipf("symlinks are unavailable on this filesystem (%v); this run is NOT evidence for symlink aliasing", err)
		}
		claims, err := NewArtefactClaimSet(root)
		require.NoError(t, err)

		holder, _, foreign, err := claims.foreignHolder("tc-bbbbbbbb", filepath.Join(link, filepath.Base(abs)))
		require.NoError(t, err)
		assert.True(t, foreign, "reaching a claimed file through a symlinked directory is still that claim's file")
		assert.Equal(t, "tc-dddddddd", holder)
	})

	// Case sensitivity is PROBED by experiment, never inferred from the path
	// string, and it is probed in MORE THAN ONE PLACE: the default temp location
	// and the volume the repository itself sits on frequently differ. Under WSL,
	// TMPDIR is ext4 (case-sensitive) while the checkout on /mnt/c folds case, so
	// probing only t.TempDir() would announce a gap that the host can actually
	// cover. The probe result decides whether this branch is evidence or an
	// announced gap; it is never silently passed.
	t.Run("differently-cased spelling", func(t *testing.T) {
		root, probe, insensitive := caseInsensitiveRoot(t)
		t.Logf("case-sensitivity probes: %s", probe)
		if !insensitive {
			t.Skipf("every filesystem reachable from this run is case-SENSITIVE (%s), so a cased alias is "+
				"genuinely a different file here; this run is NOT evidence for the case-collision criterion, "+
				"and a case-insensitive host (Windows / macOS default / WSL /mnt/c) remains REQUIRED coverage", probe)
		}

		abs, _ := seedForeignArtefact(t, root, "tc-dddddddd", "bats", "test/acceptance/feat/tc-x.bats")
		cased := filepath.Join(filepath.Dir(abs), strings.ToUpper(filepath.Base(abs)))
		claims, err := NewArtefactClaimSet(root)
		require.NoError(t, err)
		holder, _, foreign, err := claims.foreignHolder("tc-bbbbbbbb", cased)
		require.NoError(t, err)
		assert.True(t, foreign,
			"on a case-insensitive filesystem %s and %s are ONE file, so the claim must hold for both spellings",
			filepath.Base(abs), filepath.Base(cased))
		assert.Equal(t, "tc-dddddddd", holder)
	})
}

// caseInsensitiveRoot returns a directory on a filesystem PROVED to fold case,
// trying the default temp location first and then the volume the repository is
// checked out on. It returns the probe transcript either way, so a skip can say
// exactly what was tested and what was found.
func caseInsensitiveRoot(t *testing.T) (root string, transcript string, ok bool) {
	t.Helper()
	var probes []string

	tmp := t.TempDir()
	if insensitive, why := probeCaseInsensitive(tmp); insensitive {
		return tmp, "temp dir (" + tmp + "): " + why, true
	} else {
		probes = append(probes, "temp dir ("+tmp+"): "+why)
	}

	if wd, err := os.Getwd(); err == nil {
		if repoTmp, mkErr := os.MkdirTemp(wd, "gtms-caseprobe-"); mkErr == nil {
			t.Cleanup(func() { _ = os.RemoveAll(repoTmp) })
			if insensitive, why := probeCaseInsensitive(repoTmp); insensitive {
				return repoTmp, "repo volume (" + repoTmp + "): " + why, true
			} else {
				probes = append(probes, "repo volume ("+repoTmp+"): "+why)
			}
		} else {
			probes = append(probes, "repo volume: could not create a probe dir: "+mkErr.Error())
		}
	}
	return "", strings.Join(probes, "; "), false
}

// probeCaseInsensitive determines, by experiment on dir itself, whether this
// filesystem folds case. It writes a file and tries to stat it under a
// differently-cased name, then reports what happened. Nothing is inferred from
// the path string: the same repository sits on a case-insensitive mount on one
// machine and a case-sensitive one on another.
func probeCaseInsensitive(dir string) (bool, string) {
	lower := filepath.Join(dir, "gtms-case-probe.tmp")
	upper := filepath.Join(dir, "GTMS-CASE-PROBE.TMP")
	if err := os.WriteFile(lower, []byte("probe"), 0o644); err != nil {
		return false, "the probe file could not be written: " + err.Error()
	}
	defer func() { _ = os.Remove(lower) }()

	lowerInfo, err := os.Stat(lower)
	if err != nil {
		return false, "the probe file could not be stat'ed: " + err.Error()
	}
	upperInfo, err := os.Stat(upper)
	if err != nil {
		return false, "stat of the upper-cased name failed: " + err.Error()
	}
	if !os.SameFile(lowerInfo, upperInfo) {
		return false, "the upper-cased name resolved to a DIFFERENT file"
	}
	return true, "the upper-cased name resolved to the SAME file"
}

// --- (7) the slug can never carry identity -------------------------------

// The basename is ownerID + descriptive slug, and the slug step never decides
// how much of the filename is identity. It cannot: tcIDGrammar admits single
// dashes, so "tc-aaaa-bbbb-checkout" is genuinely ambiguous between tc-aaaa with
// slug "bbbb-checkout" and tc-aaaa-bbbb with slug "checkout". A parser that
// answers that is wrong half the time, and it was: cleanup for tc-aaaa deleted
// tc-aaaa-bbbb's artefact.
func TestNaming_SlugNeverCarriesIdentity(t *testing.T) {
	t.Run("names are constructed from the owner", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			specRel string
			ownerID string
			want    string
		}{
			// Unchanged for every normally named spec: the CONSTRUCTED answer
			// equals the existing stem. No fast path produces this.
			{"slugged", "gtms/test/cases/feat/tc-cccccccc-checkout.md", "tc-cccccccc", "tc-cccccccc-checkout"},
			{"bare", "gtms/test/cases/feat/tc-cccccccc.md", "tc-cccccccc", "tc-cccccccc"},
			{"nested", "gtms/test/cases/a/b/tc-xxx-deep-slug.md", "tc-xxx", "tc-xxx-deep-slug"},
			{"extended id", "gtms/test/cases/feat/tc-abc123--legacy-x.md", "tc-abc123--legacy", "tc-abc123--legacy-x"},
			{"no extension", "gtms/test/cases/tc-aaa-slug", "tc-aaa", "tc-aaa-slug"},
			{"dotted slug", "gtms/test/cases/feat/tc-cccccccc-v1.2-checkout.md", "tc-cccccccc", "tc-cccccccc-v1.2-checkout"},
			// The single-dash id that broke the old parser, from BOTH sides.
			{"single-dash id owns it", "gtms/test/cases/feat/tc-aaaa-bbbb-checkout.md", "tc-aaaa-bbbb", "tc-aaaa-bbbb-checkout"},
			{"shorter id, same filename", "gtms/test/cases/feat/tc-aaaa-bbbb-checkout.md", "tc-aaaa", "tc-aaaa-bbbb-checkout"},
			// Renamed owners keep the whole foreign stem as description.
			{"renamed owner", "gtms/test/cases/feat/tc-aaaaaaaa-file.md", "tc-bbbbbbbb", "tc-bbbbbbbb-tc-aaaaaaaa-file"},
			{"arbitrary rename", "gtms/test/cases/feat/login-spec.md", "tc-bbbbbbbb", "tc-bbbbbbbb-login-spec"},
			{"no spec path", "", "tc-fallback", "tc-fallback"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got := DeriveArtefactBasename(tc.specRel, tc.ownerID)
				assert.Equal(t, tc.want, got)
				assert.True(t, strings.HasPrefix(got, tc.ownerID),
					"every artefact name must begin with the id that owns it")
			})
		}
	})

	// Determinism: a pure function of (spec filename, owner id), independent of
	// the directory it was found in.
	t.Run("deterministic and directory-independent", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			assert.Equal(t, "tc-bbbbbbbb-login-spec", DeriveArtefactBasename("gtms/test/cases/feat/login-spec.md", "tc-bbbbbbbb"))
		}
		assert.Equal(t, "tc-bbbbbbbb-login-spec", DeriveArtefactBasename("gtms/test/cases/elsewhere/login-spec.md", "tc-bbbbbbbb"))
	})

	// The reproduction, end to end: tc-aaaa must not be able to delete
	// tc-aaaa-bbbb's artefact, whose name no basename rule can distinguish from
	// tc-aaaa's own output.
	t.Run("a shorter id cannot reach a longer id's artefact", func(t *testing.T) {
		skipIfShort(t)
		root, _, _ := ownershipProject(t, "agent-automate", "")
		victim, victimInfo := seedForeignArtefact(t, root, "tc-aaaa-bbbb", "bats",
			"test/acceptance/feat/tc-aaaa-bbbb-checkout.bats")

		removed := observeRemovals(t)
		sweep(t, mustClaims(t, root), filepath.Join(root, "test", "acceptance", "feat"), "tc-aaaa")

		assert.Empty(t, *removed)
		assertIntact(t, victim, victimInfo, "tc-aaaa-bbbb's artefact must survive a sweep for tc-aaaa")
	})
}
