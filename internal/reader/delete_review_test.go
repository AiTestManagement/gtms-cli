package reader

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/testcase"
)

// Regression coverage for the REV-112 worktree-review findings on the BUG-173
// delete planner: preserved-artefact precedence over every deletion category
// (CODEX-001), case-insensitive canonical identity (CODEX-002), fail-closed
// force on an unparseable surviving record (CODEX-003), and duplicate
// frontmatter IDs (CODEX-005).

// writeSpec writes a valid spec file (with frontmatter) at an explicit
// project-relative path and returns its absolute path.
func writeSpec(t *testing.T, root, tcID, relPath string) string {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(relPath))
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0755))
	require.NoError(t, os.WriteFile(abs,
		[]byte("---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: "+tcID+"\ntitle: "+tcID+"\n---\n"), 0644))
	return abs
}

// writeWiring writes a wiring record pointing tcID's artefact at artefactRel.
func writeWiring(t *testing.T, root, tcID, artefactRel string) {
	t.Helper()
	wiringDir := filepath.Join(root, "gtms", "automation", "wiring")
	require.NoError(t, os.MkdirAll(wiringDir, 0755))
	content := "testcase: " + tcID + "\n" +
		"testcase-hash: 0011223344556677\n" +
		"framework: bats\n" +
		"adapter: bats-runner\n" +
		"artefact: " + artefactRel + "\n" +
		"artefact-hash: aabbccddeeff0011\n"
	require.NoError(t, os.WriteFile(
		filepath.Join(wiringDir, tcID+"--bats.wiring.yaml"), []byte(content), 0644))
}

// CODEX-001: an artefact wired to a path that is ALSO a GTMS-owned file (here,
// the TC's own spec) must be preserved and NOT removed through the spec
// category. Before the fix, preserved artefacts only won over result files, so
// plain delete reported the path preserved while deleting it as a spec.
func TestBUG173_CODEX001_PreservedArtefactWinsOverSpecCategory(t *testing.T) {
	root := t.TempDir()
	specRel := "gtms/test/cases/solo/tc-c0de0001-spec.md"
	specAbs := writeSpec(t, root, "tc-c0de0001", specRel)
	// The wiring points the artefact at the spec file itself.
	writeWiring(t, root, "tc-c0de0001", specRel)

	result, err := DeleteArtifacts(root, nil, "tc-c0de0001", false, false, false)
	require.NoError(t, err)

	require.Len(t, result.PreservedArtefacts, 1)
	assert.Equal(t, specRel, result.PreservedArtefacts[0].Path)
	assert.Equal(t, 0, result.TestCaseSpecsRemoved,
		"the spec is also a referenced artefact -> preserved, not removed via the spec category")

	_, statErr := os.Stat(specAbs)
	assert.NoError(t, statErr, "a path reported preserved must still exist on disk")
	for _, d := range result.FilesDeleted {
		assert.NotEqual(t, specAbs, filepath.Join(root, filepath.FromSlash(d)),
			"preserved path must not appear in the deleted list")
	}
}

// TestCanonicalPathKey pins the OS-dependent identity used for shared-artefact
// matching: case-insensitive on Windows, case-sensitive elsewhere.
func TestCanonicalPathKey(t *testing.T) {
	a := filepath.FromSlash("/proj/Tests/Shared.bats")
	b := filepath.FromSlash("/proj/tests/shared.bats")
	if runtime.GOOS == "windows" {
		assert.Equal(t, canonicalPathKey(a), canonicalPathKey(b),
			"case-variant paths name one file on Windows and must share a key")
	} else {
		assert.NotEqual(t, canonicalPathKey(a), canonicalPathKey(b),
			"paths are case-sensitive off Windows")
	}
}

// CODEX-002: on a case-insensitive filesystem, a surviving reference recorded
// with different case must still protect a shared artefact from forced
// deletion. Runs only on Windows, where the aliasing is real.
func TestBUG173_CODEX002_CaseVariantAliasProtectsSharedArtefact(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("case-insensitive aliasing only manifests on Windows filesystems")
	}
	root := t.TempDir()
	artefact := seedWiredArtefact(t, root, "tc-22222222", "scoped", "test/acceptance/shared/Shared.bats")
	// Survivor references the SAME file via a differently-cased path.
	writeSpec(t, root, "tc-33333333", "gtms/test/cases/scoped/tc-33333333-spec.md")
	writeWiring(t, root, "tc-33333333", "test/acceptance/shared/shared.bats")

	result, err := DeleteArtifacts(root, nil, "tc-22222222", false, true, false)
	require.NoError(t, err)

	assert.Equal(t, 0, result.TestScripts, "case-variant survivor reference must protect the artefact")
	require.Len(t, result.PreservedArtefacts, 1)
	_, statErr := os.Stat(artefact)
	assert.NoError(t, statErr, "shared artefact must survive")
}

// CODEX-003: forced deletion must fail closed when a surviving record cannot be
// parsed -- its (possibly shared) artefact reference is invisible to the index,
// so exclusivity cannot be proven and the file must not be deleted.
func TestBUG173_CODEX003_ForceFailsClosedOnMalformedSurvivorWiring(t *testing.T) {
	root := t.TempDir()
	artefact := seedWiredArtefact(t, root, "tc-22222222", "scoped", "test/acceptance/shared/shared.bats")
	// A second sharer, whose wiring record is then corrupted.
	seedWiredArtefact(t, root, "tc-33333333", "scoped", "test/acceptance/shared/shared.bats")
	require.NoError(t, os.WriteFile(wiringPath(root, "tc-33333333"),
		[]byte("this: is: not: valid: wiring: ["), 0644))

	_, err := DeleteArtifacts(root, nil, "tc-22222222", false, true, false)
	require.Error(t, err, "force must abort when a surviving record is unparseable")
	assert.Contains(t, err.Error(), "force-artefact-deletion")

	_, statErr := os.Stat(artefact)
	assert.NoError(t, statErr, "artefact must survive the aborted forced delete")

	// The plain preserve-by-default path stays lenient: no removal, so a missed
	// reference cannot cause loss, and it must not error on the same state.
	_, err = DeleteArtifacts(root, nil, "tc-22222222", false, false, false)
	require.NoError(t, err, "plain delete stays lenient despite the malformed record")
}

// A non-canonical project root (accessed through a symlink) must still produce
// clean project-relative display paths. This is the portable analogue of the
// Windows 8.3 short-vs-long name split that broke BUG-173 on Windows CI: the raw
// root (the symlink) and the pathsafe-canonical root (its real target) diverge,
// so an uncanonicalised toProjectRel emits a spurious "../../" escape.
func TestBUG173_NonCanonicalRootProducesCleanRelPaths(t *testing.T) {
	real := t.TempDir()
	artefact := seedWiredArtefact(t, real, "tc-11111111", "solo", "test/acceptance/solo/tc-11111111-solo.bats")

	link := filepath.Join(t.TempDir(), "proj-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unsupported here: %v", err)
	}

	result, err := DeleteArtifacts(link, nil, "tc-11111111", false, false, false)
	require.NoError(t, err)

	require.Len(t, result.PreservedArtefacts, 1)
	assert.Equal(t, "test/acceptance/solo/tc-11111111-solo.bats", result.PreservedArtefacts[0].Path,
		"display path must be clean despite a non-canonical root")
	_, statErr := os.Stat(artefact)
	assert.NoError(t, statErr, "artefact preserved")
}

// The same non-canonical root must not break the CODEX-001 preserved-filter: if
// the root's canonical form is not used consistently, the spec path (joined
// under the raw root) and the artefact path (resolved via pathsafe) key
// differently and the spec-as-artefact leaks into the spec deletion category.
func TestBUG173_NonCanonicalRootPreservedFilterHolds(t *testing.T) {
	real := t.TempDir()
	specRel := "gtms/test/cases/solo/tc-c0de0001-spec.md"
	specAbs := writeSpec(t, real, "tc-c0de0001", specRel)
	writeWiring(t, real, "tc-c0de0001", specRel)

	link := filepath.Join(t.TempDir(), "proj-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unsupported here: %v", err)
	}

	result, err := DeleteArtifacts(link, nil, "tc-c0de0001", false, false, false)
	require.NoError(t, err)
	assert.Equal(t, 0, result.TestCaseSpecsRemoved,
		"spec-as-artefact must stay preserved even via a non-canonical root")
	_, statErr := os.Stat(specAbs)
	assert.NoError(t, statErr, "preserved spec must still exist")
}

// CODEX-005: a folder holding two specs with the same frontmatter ID must not
// double-gather and fail mid-removal on the second copy.
// ADR-024 / BUG-185 (tc-81893058): two specs in one folder claiming the SAME
// exact frontmatter id are AMBIGUOUS ownership. Delete must refuse the whole plan
// before any mutation and name both owners -- it must NOT silently remove both, as
// the pre-ADR-024 dedup path did. (The original CODEX-005 concern was a
// mid-operation double-remove crash; the ambiguity refusal makes that scenario
// unreachable and is the stronger, correct contract.)
func TestBUG185_DuplicateFrontmatterID_RefusesWholePlan(t *testing.T) {
	root := t.TempDir()
	specA := writeSpec(t, root, "tc-dup00001", "gtms/test/cases/dup/tc-dup00001-a.md")
	specB := writeSpec(t, root, "tc-dup00001", "gtms/test/cases/dup/tc-dup00001-b.md")

	scope := &ScopeInfo{
		ScanDir: filepath.Join(root, "gtms", "test", "cases", "dup"),
		RelPath: "gtms/test/cases/dup/",
	}
	beforeA, err := os.ReadFile(specA)
	require.NoError(t, err)
	beforeB, err := os.ReadFile(specB)
	require.NoError(t, err)

	_, err = DeleteArtifacts(root, scope, "", false, false, false)
	require.Error(t, err, "a duplicated exact frontmatter id must refuse, not silently remove both owners")

	var oe *testcase.OwnershipError
	require.ErrorAs(t, err, &oe, "refusal must be a typed OwnershipError")
	assert.Equal(t, testcase.OutcomeAmbiguous, oe.Outcome)
	assert.Len(t, oe.Owners, 2, "both conflicting spec owners must be named")

	// No mutation: both specs remain byte-identical, not merely present. Presence
	// alone would pass against a refusal that had already rewritten a file.
	afterA, readErrA := os.ReadFile(specA)
	afterB, readErrB := os.ReadFile(specB)
	require.NoError(t, readErrA, "spec A must remain after the refusal")
	require.NoError(t, readErrB, "spec B must remain after the refusal")
	assert.Equal(t, beforeA, afterA, "spec A must be byte-identical after the refusal")
	assert.Equal(t, beforeB, afterB, "spec B must be byte-identical after the refusal")
}
