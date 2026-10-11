package reader

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BUG-173: `gtms delete` must treat a wiring reference as a reference, not a
// claim of exclusive ownership. These tests pin the safe default (preserve
// referenced artefacts), the `--force-artefact-deletion` opt-in, referential
// protection for shared artefacts, batch-wide dedupe, canonical-path aliasing,
// dry-run parity, and --keep-spec composition.

// seedWiredArtefact writes a spec, an on-disk artefact, and a wiring record
// linking the two. It returns the absolute artefact path.
func seedWiredArtefact(t *testing.T, root, tcID, folder, artefactRel string) string {
	t.Helper()

	tcDir := filepath.Join(root, "gtms", "test", "cases")
	if folder != "" {
		tcDir = filepath.Join(tcDir, folder)
	}
	require.NoError(t, os.MkdirAll(tcDir, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(tcDir, tcID+"-spec.md"),
		[]byte("---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: "+tcID+"\ntitle: "+tcID+"\n---\n"), 0644))

	artefactAbs := filepath.Join(root, filepath.FromSlash(artefactRel))
	require.NoError(t, os.MkdirAll(filepath.Dir(artefactAbs), 0755))
	require.NoError(t, os.WriteFile(artefactAbs, []byte("# artefact for "+tcID+"\n"), 0644))

	wiringDir := filepath.Join(root, "gtms", "automation", "wiring")
	require.NoError(t, os.MkdirAll(wiringDir, 0755))
	content := "testcase: " + tcID + "\n" +
		"testcase-hash: 0011223344556677\n" +
		"framework: bats\n" +
		"adapter: bats-runner\n" +
		"artefact: " + artefactRel + "\n" +
		"artefact-hash: aabbccddeeff0011\n"
	require.NoError(t, os.WriteFile(
		filepath.Join(wiringDir, tcID+"--bats.wiring.yaml"),
		[]byte(content), 0644))

	return artefactAbs
}

func wiringPath(root, tcID string) string {
	return filepath.Join(root, "gtms", "automation", "wiring", tcID+"--bats.wiring.yaml")
}

// TestBUG173_DefaultPreservesExclusiveArtefact: plain delete removes GTMS-owned
// state but preserves the referenced artefact and names it.
func TestBUG173_DefaultPreservesExclusiveArtefact(t *testing.T) {
	root := t.TempDir()
	artefact := seedWiredArtefact(t, root, "tc-11111111", "solo", "test/acceptance/solo/tc-11111111-solo.bats")

	result, err := DeleteArtifacts(root, nil, "tc-11111111", false, false, false)
	require.NoError(t, err)

	assert.Equal(t, 1, result.TestCaseSpecsRemoved, "spec removed")
	assert.Equal(t, 1, result.AutomationRecords, "wiring removed")
	assert.Equal(t, 0, result.TestScripts, "artefact must NOT be counted deleted by default")

	require.Len(t, result.PreservedArtefacts, 1)
	assert.Equal(t, "test/acceptance/solo/tc-11111111-solo.bats", result.PreservedArtefacts[0].Path)
	assert.Empty(t, result.PreservedArtefacts[0].SurvivingTCs, "safe-default preserve names no surviving TCs")

	_, statErr := os.Stat(artefact)
	assert.NoError(t, statErr, "artefact must survive a plain delete")
	_, statErr = os.Stat(wiringPath(root, "tc-11111111"))
	assert.True(t, os.IsNotExist(statErr), "wiring must be gone")
}

// TestBUG173_ForcedExclusiveDeletion: with the force flag, an exclusively-owned
// artefact is physically removed.
func TestBUG173_ForcedExclusiveDeletion(t *testing.T) {
	root := t.TempDir()
	artefact := seedWiredArtefact(t, root, "tc-11111111", "solo", "test/acceptance/solo/tc-11111111-solo.bats")

	result, err := DeleteArtifacts(root, nil, "tc-11111111", false, true, false)
	require.NoError(t, err)

	assert.Equal(t, 1, result.TestScripts, "forced exclusive deletion counts the artefact")
	assert.Empty(t, result.PreservedArtefacts, "no preservation for an exclusive forced delete")

	_, statErr := os.Stat(artefact)
	assert.True(t, os.IsNotExist(statErr), "artefact must be gone under force")
}

// TestBUG173_ForcedSharedPreservation: two TCs share one artefact; force-delete
// one -> artefact preserved, warning names the survivor.
func TestBUG173_ForcedSharedPreservation(t *testing.T) {
	root := t.TempDir()
	artefact := seedWiredArtefact(t, root, "tc-22222222", "scoped", "test/acceptance/shared/shared-scenarios.bats")
	// Second sharer reuses the SAME artefact path.
	seedWiredArtefact(t, root, "tc-33333333", "scoped", "test/acceptance/shared/shared-scenarios.bats")

	result, err := DeleteArtifacts(root, nil, "tc-22222222", false, true, false)
	require.NoError(t, err)

	assert.Equal(t, 0, result.TestScripts, "shared artefact must not be deleted even under force")
	require.Len(t, result.PreservedArtefacts, 1)
	assert.Equal(t, "test/acceptance/shared/shared-scenarios.bats", result.PreservedArtefacts[0].Path)
	assert.Equal(t, []string{"tc-33333333"}, result.PreservedArtefacts[0].SurvivingTCs)

	_, statErr := os.Stat(artefact)
	assert.NoError(t, statErr, "shared artefact must survive")
	// Survivor's wiring untouched.
	_, statErr = os.Stat(wiringPath(root, "tc-33333333"))
	assert.NoError(t, statErr, "survivor wiring must remain")
}

// TestBUG173_FolderDefaultPreservesOnce: folder delete over all sharers keeps
// the shared artefact and reports it exactly once (batch dedupe).
func TestBUG173_FolderDefaultPreservesOnce(t *testing.T) {
	root := t.TempDir()
	artefact := seedWiredArtefact(t, root, "tc-22222222", "scoped", "test/acceptance/shared/shared-scenarios.bats")
	seedWiredArtefact(t, root, "tc-33333333", "scoped", "test/acceptance/shared/shared-scenarios.bats")

	scope := &ScopeInfo{
		ScanDir: filepath.Join(root, "gtms", "test", "cases", "scoped"),
		RelPath: "gtms/test/cases/scoped/",
	}
	result, err := DeleteArtifacts(root, scope, "", false, false, false)
	require.NoError(t, err)

	assert.Equal(t, 2, result.TestCasesProcessed)
	assert.Equal(t, 2, result.TestCaseSpecsRemoved)
	assert.Equal(t, 2, result.AutomationRecords)
	assert.Equal(t, 0, result.TestScripts)
	require.Len(t, result.PreservedArtefacts, 1, "shared artefact reported exactly once")

	_, statErr := os.Stat(artefact)
	assert.NoError(t, statErr)
}

// TestBUG173_FolderForcedDeletesOnce: folder force-delete over all sharers
// removes the shared artefact exactly once, no double-remove error.
func TestBUG173_FolderForcedDeletesOnce(t *testing.T) {
	root := t.TempDir()
	artefact := seedWiredArtefact(t, root, "tc-22222222", "scoped", "test/acceptance/shared/shared-scenarios.bats")
	seedWiredArtefact(t, root, "tc-33333333", "scoped", "test/acceptance/shared/shared-scenarios.bats")

	scope := &ScopeInfo{
		ScanDir: filepath.Join(root, "gtms", "test", "cases", "scoped"),
		RelPath: "gtms/test/cases/scoped/",
	}
	result, err := DeleteArtifacts(root, scope, "", false, true, false)
	require.NoError(t, err, "no double-remove error when every reference is in the selection")

	assert.Equal(t, 1, result.TestScripts, "artefact removed exactly once")
	assert.Empty(t, result.PreservedArtefacts, "no warning when the selection covers all references")

	_, statErr := os.Stat(artefact)
	assert.True(t, os.IsNotExist(statErr))
}

// TestBUG173_ForcedSubsetPreservesExternalSharer: three sharers, forced folder
// delete of two -> artefact preserved, warning names the external third.
func TestBUG173_ForcedSubsetPreservesExternalSharer(t *testing.T) {
	root := t.TempDir()
	artefact := seedWiredArtefact(t, root, "tc-22222222", "scoped", "test/acceptance/shared/shared-scenarios.bats")
	seedWiredArtefact(t, root, "tc-33333333", "scoped", "test/acceptance/shared/shared-scenarios.bats")
	seedWiredArtefact(t, root, "tc-44444444", "external", "test/acceptance/shared/shared-scenarios.bats")

	scope := &ScopeInfo{
		ScanDir: filepath.Join(root, "gtms", "test", "cases", "scoped"),
		RelPath: "gtms/test/cases/scoped/",
	}
	result, err := DeleteArtifacts(root, scope, "", false, true, false)
	require.NoError(t, err)

	assert.Equal(t, 2, result.TestCasesProcessed)
	assert.Equal(t, 0, result.TestScripts, "artefact preserved: an external TC still references it")
	require.Len(t, result.PreservedArtefacts, 1)
	assert.Equal(t, []string{"tc-44444444"}, result.PreservedArtefacts[0].SurvivingTCs)

	_, statErr := os.Stat(artefact)
	assert.NoError(t, statErr)
	_, statErr = os.Stat(wiringPath(root, "tc-44444444"))
	assert.NoError(t, statErr, "external sharer's wiring untouched")
}

// TestBUG173_AliasPathsRecognisedAsShared: a surviving reference via a
// `./`-prefixed alias must still protect the artefact (canonical comparison).
func TestBUG173_AliasPathsRecognisedAsShared(t *testing.T) {
	root := t.TempDir()
	artefact := seedWiredArtefact(t, root, "tc-22222222", "scoped", "test/acceptance/shared/shared-scenarios.bats")
	seedWiredArtefact(t, root, "tc-33333333", "scoped", "test/acceptance/shared/shared-scenarios.bats")

	// Rewrite the survivor's artefact line to the `./`-prefixed alias.
	survivorWiring := wiringPath(root, "tc-33333333")
	aliased := "testcase: tc-33333333\n" +
		"testcase-hash: 0011223344556677\n" +
		"framework: bats\n" +
		"adapter: bats-runner\n" +
		"artefact: ./test/acceptance/shared/shared-scenarios.bats\n" +
		"artefact-hash: aabbccddeeff0011\n"
	require.NoError(t, os.WriteFile(survivorWiring, []byte(aliased), 0644))

	result, err := DeleteArtifacts(root, nil, "tc-22222222", false, true, false)
	require.NoError(t, err)

	assert.Equal(t, 0, result.TestScripts, "alias reference must canonicalise to the same path and protect it")
	require.Len(t, result.PreservedArtefacts, 1)
	assert.Equal(t, []string{"tc-33333333"}, result.PreservedArtefacts[0].SurvivingTCs)

	_, statErr := os.Stat(artefact)
	assert.NoError(t, statErr)
}

// TestBUG173_DryRunParity: dry-run produces the same plan as the real run and
// mutates nothing, in both default and forced modes.
func TestBUG173_DryRunParity(t *testing.T) {
	assertPlanEqual := func(t *testing.T, a, b *DeleteResult) {
		t.Helper()
		assert.Equal(t, a.TestCaseSpecsRemoved, b.TestCaseSpecsRemoved)
		assert.Equal(t, a.AutomationRecords, b.AutomationRecords)
		assert.Equal(t, a.TestScripts, b.TestScripts)
		assert.Equal(t, a.ResultFiles, b.ResultFiles)
		assert.Equal(t, len(a.PreservedArtefacts), len(b.PreservedArtefacts))
	}

	t.Run("default", func(t *testing.T) {
		root := t.TempDir()
		artefact := seedWiredArtefact(t, root, "tc-22222222", "scoped", "test/acceptance/shared/default.bats")
		seedWiredArtefact(t, root, "tc-33333333", "scoped", "test/acceptance/shared/default.bats")
		scope := &ScopeInfo{ScanDir: filepath.Join(root, "gtms", "test", "cases", "scoped"), RelPath: "gtms/test/cases/scoped/"}

		dry, err := DeleteArtifacts(root, scope, "", false, false, true)
		require.NoError(t, err)
		// Dry-run mutated nothing.
		_, statErr := os.Stat(artefact)
		assert.NoError(t, statErr)
		_, statErr = os.Stat(wiringPath(root, "tc-22222222"))
		assert.NoError(t, statErr, "dry-run must not remove wiring")

		real, err := DeleteArtifacts(root, scope, "", false, false, false)
		require.NoError(t, err)
		assertPlanEqual(t, dry, real)
		assert.Equal(t, 0, real.TestScripts)
		_, statErr = os.Stat(artefact)
		assert.NoError(t, statErr, "default real run preserves the artefact")
	})

	t.Run("forced", func(t *testing.T) {
		root := t.TempDir()
		artefact := seedWiredArtefact(t, root, "tc-55555555", "forced", "test/acceptance/shared/forced.bats")
		seedWiredArtefact(t, root, "tc-66666666", "forced", "test/acceptance/shared/forced.bats")
		scope := &ScopeInfo{ScanDir: filepath.Join(root, "gtms", "test", "cases", "forced"), RelPath: "gtms/test/cases/forced/"}

		dry, err := DeleteArtifacts(root, scope, "", false, true, true)
		require.NoError(t, err)
		_, statErr := os.Stat(artefact)
		assert.NoError(t, statErr, "forced dry-run must not remove the artefact")

		real, err := DeleteArtifacts(root, scope, "", false, true, false)
		require.NoError(t, err)
		assertPlanEqual(t, dry, real)
		assert.Equal(t, 1, real.TestScripts, "forced real run removes the artefact once")
		_, statErr = os.Stat(artefact)
		assert.True(t, os.IsNotExist(statErr))
	})
}

// TestBUG173_KeepSpecComposesWithDefault: --keep-spec (no force) keeps both the
// spec and the referenced artefact.
func TestBUG173_KeepSpecComposesWithDefault(t *testing.T) {
	root := t.TempDir()
	artefact := seedWiredArtefact(t, root, "tc-11111111", "solo", "test/acceptance/solo/tc-11111111-solo.bats")

	result, err := DeleteArtifacts(root, nil, "tc-11111111", true, false, false)
	require.NoError(t, err)

	assert.Equal(t, 0, result.TestCaseSpecsRemoved, "spec kept by --keep-spec")
	assert.Equal(t, 1, result.AutomationRecords, "wiring removed")
	require.Len(t, result.PreservedArtefacts, 1, "artefact preserved by safe default")

	specDir := filepath.Join(root, "gtms", "test", "cases", "solo")
	specs, _ := filepath.Glob(filepath.Join(specDir, "tc-11111111-*.md"))
	assert.Len(t, specs, 1, "spec still on disk")
	_, statErr := os.Stat(artefact)
	assert.NoError(t, statErr, "artefact still on disk")
}

// TestBUG173_KeepSpecForcedShared: --keep-spec --force-artefact-deletion on a
// shared artefact keeps the spec and preserves the artefact (external ref).
func TestBUG173_KeepSpecForcedShared(t *testing.T) {
	root := t.TempDir()
	artefact := seedWiredArtefact(t, root, "tc-22222222", "scoped", "test/acceptance/shared/shared-scenarios.bats")
	seedWiredArtefact(t, root, "tc-33333333", "scoped", "test/acceptance/shared/shared-scenarios.bats")

	result, err := DeleteArtifacts(root, nil, "tc-22222222", true, true, false)
	require.NoError(t, err)

	assert.Equal(t, 0, result.TestCaseSpecsRemoved, "spec kept")
	assert.Equal(t, 0, result.TestScripts, "shared artefact preserved despite force")
	require.Len(t, result.PreservedArtefacts, 1)
	assert.Equal(t, []string{"tc-33333333"}, result.PreservedArtefacts[0].SurvivingTCs)

	specs, _ := filepath.Glob(filepath.Join(root, "gtms", "test", "cases", "scoped", "tc-22222222-*.md"))
	assert.Len(t, specs, 1, "deleted TC's spec kept by --keep-spec")
	_, statErr := os.Stat(artefact)
	assert.NoError(t, statErr)
}
