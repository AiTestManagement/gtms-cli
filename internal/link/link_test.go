package link

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// playwrightConfig produces a minimal *config.Config that registers a
// playwright execute adapter — the new LinkRecord requires cfg so it can
// resolve the canonical execute adapter for the framework (CON-023 / ENH-145).
func playwrightConfig() *config.Config {
	return &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"playwright-runner": {Framework: "playwright", Mode: "sync", Command: "echo ok"},
			},
		},
		Defaults: map[string]string{"execute": "playwright-runner"},
	}
}

// createTCSpecFixture creates a test case spec file under gtms/cases/ so that
// (a) testcase.Exists returns true and (b) HashFile can compute testcase-hash.
// CON-023 makes the spec mandatory for wiring writes — the brownfield "link
// without spec" contract is retired.
func createTCSpecFixture(t *testing.T, root, tcID string) {
	t.Helper()
	dir := filepath.Join(root, "gtms", "cases", "test")
	require.NoError(t, os.MkdirAll(dir, 0755))
	spec := "---\ntest_case_id: " + tcID + "\n---\n# Sample\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, tcID+"-sample.md"), []byte(spec), 0644))
}

// createTCSpecAt creates a spec under a specific sub-folder (for the
// folder-qualified target tests below).
func createTCSpecAt(t *testing.T, root, subdir, tcID string) {
	t.Helper()
	dir := filepath.Join(root, "gtms", "cases", subdir)
	require.NoError(t, os.MkdirAll(dir, 0755))
	spec := "---\ntest_case_id: " + tcID + "\n---\n# " + tcID + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, tcID+"-test.md"), []byte(spec), 0644))
}

// commonLinkFixture seeds spec + artefact for the happy-path tests. Returns
// the playwright-config for the new LinkRecord signature.
func commonLinkFixture(t *testing.T) (string, *config.Config) {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "tests"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "tests/sample.spec.ts"), []byte("test"), 0644))
	createTCSpecFixture(t, root, "tc-abc12345")
	return root, playwrightConfig()
}

// linkRecordErr wraps LinkRecord for tests that only care about the error
// (the fallback-diagnostic warnings are exercised by their own dedicated
// tests below — TestLinkRecord_FallbackDiagnostic_* and
// TestLinkRecord_*NoWarning). The happy-path tests above use this wrapper
// to keep the long signature out of every assertion; the fallback tests
// call LinkRecord directly so they can inspect the warnings slice.
func linkRecordErr(root string, cfg *config.Config, tcID, framework, artefact, branch, environment, executedBy string, force, strict bool) error {
	_, err := LinkRecord(root, cfg, tcID, framework, artefact, branch, environment, executedBy, force, strict)
	return err
}

func TestLinkRecord_HappyPath_WritesSixFieldWiring(t *testing.T) {
	root, cfg := commonLinkFixture(t)

	err := linkRecordErr(root, cfg, "tc-abc12345", "playwright", "tests/sample.spec.ts", "main", "", "", false, false)
	require.NoError(t, err)

	// Verify wiring file was created with all six fields.
	rec, path, findErr := wiring.Find(root, "tc-abc12345", "playwright")
	require.NoError(t, findErr)
	require.NotNil(t, rec, "wiring record must be created")
	assert.Contains(t, path, "gtms"+string(filepath.Separator)+"automation"+string(filepath.Separator)+"wiring")

	assert.Equal(t, "tc-abc12345", rec.TestCase)
	assert.Equal(t, "playwright", rec.Framework)
	assert.Equal(t, "playwright-runner", rec.Adapter, "wiring.adapter is the canonical EXECUTE adapter, not 'manual-link'")
	assert.Equal(t, "tests/sample.spec.ts", rec.Artefact)
	assert.NotEmpty(t, rec.TestCaseHash, "testcase-hash must be computed at write time")
	assert.NotEmpty(t, rec.ArtefactHash, "artefact-hash must be computed at write time")
}

func TestLinkRecord_NoExecuteAdapter_FailsClearly(t *testing.T) {
	root, _ := commonLinkFixture(t)
	emptyCfg := &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{"execute": {}},
		Defaults: map[string]string{"execute": ""},
	}

	err := linkRecordErr(root, emptyCfg, "tc-abc12345", "playwright", "tests/sample.spec.ts", "main", "", "", false, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no execute adapter")
	assert.Contains(t, err.Error(), "playwright")

	// No wiring should be created
	rec, _, _ := wiring.Find(root, "tc-abc12345", "playwright")
	assert.Nil(t, rec)
}

func TestLinkRecord_ArtefactMissing(t *testing.T) {
	root, cfg := commonLinkFixture(t)

	err := linkRecordErr(root, cfg, "tc-abc12345", "playwright", "tests/does-not-exist.spec.ts", "main", "", "", false, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	assert.Contains(t, err.Error(), "tests/does-not-exist.spec.ts")

	rec, _, _ := wiring.Find(root, "tc-abc12345", "playwright")
	assert.Nil(t, rec)
}

func TestLinkRecord_ExistingWiringNoForce(t *testing.T) {
	root, cfg := commonLinkFixture(t)

	require.NoError(t, linkRecordErr(root, cfg, "tc-abc12345", "playwright", "tests/sample.spec.ts", "main", "", "", false, false))

	// Second link without --force fails
	err := linkRecordErr(root, cfg, "tc-abc12345", "playwright", "tests/sample.spec.ts", "main", "", "", false, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
	assert.Contains(t, err.Error(), "--force")
}

func TestLinkRecord_ForceOverwritesAndRefreshesHashes(t *testing.T) {
	root, cfg := commonLinkFixture(t)

	require.NoError(t, linkRecordErr(root, cfg, "tc-abc12345", "playwright", "tests/sample.spec.ts", "main", "", "", false, false))
	first, _, _ := wiring.Find(root, "tc-abc12345", "playwright")
	require.NotNil(t, first)

	// Edit the artefact so artefact-hash should change on force.
	require.NoError(t, os.WriteFile(filepath.Join(root, "tests/sample.spec.ts"), []byte("test-updated-content"), 0644))

	require.NoError(t, linkRecordErr(root, cfg, "tc-abc12345", "playwright", "tests/sample.spec.ts", "main", "", "", true, false))
	second, _, _ := wiring.Find(root, "tc-abc12345", "playwright")
	require.NotNil(t, second)

	assert.NotEqual(t, first.ArtefactHash, second.ArtefactHash, "force must refresh artefact-hash")
	assert.Equal(t, first.TestCaseHash, second.TestCaseHash, "testcase-hash should be stable when spec is unchanged")
}

func TestLinkRecord_NoSpec_FailsBecauseTestCaseHashCannotBeComputed(t *testing.T) {
	// CON-023 retires the ENH-111 brownfield contract. Without a spec we
	// cannot compute testcase-hash, and wiring is six-fields-or-fail.
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "tests"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "tests/sample.spec.ts"), []byte("test"), 0644))

	err := linkRecordErr(root, playwrightConfig(), "tc-deadbeef", "playwright", "tests/sample.spec.ts", "main", "", "", false, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "test case spec")

	rec, _, _ := wiring.Find(root, "tc-deadbeef", "playwright")
	assert.Nil(t, rec)
}

func TestCheckLink_WithArtefactExists(t *testing.T) {
	root, _ := commonLinkFixture(t)

	result, err := CheckLink(root, "tc-abc12345", "playwright", "tests/sample.spec.ts", false)
	require.NoError(t, err)

	assert.True(t, result.ArtefactExists)
	assert.Equal(t, "tests/sample.spec.ts", result.Artefact)
	assert.False(t, result.RecordExists, "wiring not yet written")
}

func TestCheckLink_WithArtefactMissing(t *testing.T) {
	root := t.TempDir()

	result, err := CheckLink(root, "tc-abc12345", "playwright", "tests/missing.spec.ts", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	assert.False(t, result.ArtefactExists)
}

func TestCheckLink_ExistingWiring_ArtefactPresent(t *testing.T) {
	root, cfg := commonLinkFixture(t)
	require.NoError(t, linkRecordErr(root, cfg, "tc-abc12345", "playwright", "tests/sample.spec.ts", "main", "", "", false, false))

	result, err := CheckLink(root, "tc-abc12345", "playwright", "", false)
	require.NoError(t, err)
	assert.True(t, result.ArtefactExists)
	assert.True(t, result.RecordExists)
	assert.Equal(t, "tests/sample.spec.ts", result.Artefact)
}

func TestCheckLink_ExistingWiring_ArtefactGone(t *testing.T) {
	root, cfg := commonLinkFixture(t)
	require.NoError(t, linkRecordErr(root, cfg, "tc-abc12345", "playwright", "tests/sample.spec.ts", "main", "", "", false, false))
	require.NoError(t, os.Remove(filepath.Join(root, "tests/sample.spec.ts")))

	result, err := CheckLink(root, "tc-abc12345", "playwright", "", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	assert.False(t, result.ArtefactExists)
	assert.True(t, result.RecordExists)
}

func TestCheckLink_NoWiringNoArtefact(t *testing.T) {
	root := t.TempDir()

	_, err := CheckLink(root, "tc-abc12345", "playwright", "", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no existing wiring")
}

// --- BUG-059 strict-mode tests (kept for CLI compatibility) ---

func TestLinkRecord_StrictRejectsPhantom(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/cases"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "tests"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "tests/sample.spec.ts"), []byte("test"), 0644))

	err := linkRecordErr(root, playwrightConfig(), "tc-deadbeef", "playwright", "tests/sample.spec.ts", "main", "", "", false, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found in gtms/cases")

	rec, _, _ := wiring.Find(root, "tc-deadbeef", "playwright")
	assert.Nil(t, rec)
}

func TestLinkRecord_StrictAcceptsRealTC(t *testing.T) {
	root, cfg := commonLinkFixture(t)

	err := linkRecordErr(root, cfg, "tc-abc12345", "playwright", "tests/sample.spec.ts", "main", "", "", false, true)
	require.NoError(t, err)

	rec, _, _ := wiring.Find(root, "tc-abc12345", "playwright")
	require.NotNil(t, rec)
	assert.Equal(t, "tc-abc12345", rec.TestCase)
}

func TestCheckLink_StrictRejectsPhantom(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/cases"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "tests"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "tests/sample.spec.ts"), []byte("test"), 0644))

	result, err := CheckLink(root, "tc-deadbeef", "playwright", "tests/sample.spec.ts", true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found in gtms/cases")
	assert.False(t, result.RecordExists)
}

func TestLinkRecord_StrictArtefactMissingStillErrors(t *testing.T) {
	root, cfg := commonLinkFixture(t)

	err := linkRecordErr(root, cfg, "tc-abc12345", "playwright", "tests/does-not-exist.spec.ts", "main", "", "", false, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	assert.Contains(t, err.Error(), "tests/does-not-exist.spec.ts")
}

// --- BUG-059 folder-qualified target tests ---

func TestBUG059_LinkRecord_FolderQualifiedTarget(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "tests"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "tests/sample.spec.ts"), []byte("test"), 0644))
	createTCSpecAt(t, root, "login", "tc-abc12345")

	err := linkRecordErr(root, playwrightConfig(), "login/tc-abc12345", "playwright", "tests/sample.spec.ts", "main", "", "", false, true)
	require.NoError(t, err)

	rec, _, _ := wiring.Find(root, "tc-abc12345", "playwright")
	require.NotNil(t, rec)
}

func TestBUG059_LinkRecord_FolderQualifiedWrongFolder(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "tests"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "tests/sample.spec.ts"), []byte("test"), 0644))
	createTCSpecAt(t, root, "login", "tc-abc12345")

	err := linkRecordErr(root, playwrightConfig(), "checkout/tc-abc12345", "playwright", "tests/sample.spec.ts", "main", "", "", false, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found in gtms/cases")
}

// --- CON-023 / ENH-145 canonical-adapter fallback diagnostic ---

// TestLinkRecord_FallbackDiagnostic_NamesCompetingAdapters pins the
// requirement that when multiple execute adapters match the framework
// and no default selects one, LinkRecord still writes wiring but returns
// a warning naming the chosen adapter and the competing matches. This
// is what stops "wiring silently nails a lexically-first adapter" from
// being invisible to the user.
func TestLinkRecord_FallbackDiagnostic_NamesCompetingAdapters(t *testing.T) {
	root, _ := commonLinkFixture(t)

	// Three execute adapters all matching framework=bats, no default.
	cfg := &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"bats-runner":      {Framework: "bats", Mode: "sync", Command: "echo ok"},
				"remote-bats":      {Framework: "bats", Mode: "sync", Command: "echo ok"},
				"remote-bats-lean": {Framework: "bats", Mode: "sync", Command: "echo ok"},
			},
		},
		Defaults: map[string]string{}, // no execute default
	}
	// Use a TC whose spec exists. The fixture seeds tc-abc12345.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "test/acceptance"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "test/acceptance/sample.bats"), []byte("# bats"), 0644))

	warnings, err := LinkRecord(root, cfg, "tc-abc12345", "bats",
		"test/acceptance/sample.bats", "main", "", "", false, false)
	require.NoError(t, err)
	require.Len(t, warnings, 1,
		"a single fallback-diagnostic warning must surface when len(matches) > 1")

	w := warnings[0]
	assert.Contains(t, w, "bats", "warning must name the framework")
	// Chosen lexically-first: "bats-runner" comes before remote-* lexically.
	assert.Contains(t, w, "bats-runner", "warning must name the chosen adapter")
	assert.Contains(t, w, "remote-bats", "warning must name the competing adapter")
	assert.Contains(t, w, "remote-bats-lean", "warning must name the competing adapter")
	assert.Contains(t, w, "defaults.execute",
		"warning must point the user at the gtms.config knob that suppresses it")

	// Wiring must still be written — the warning is a hint, not a refusal.
	rec, _, _ := wiring.Find(root, "tc-abc12345", "bats")
	require.NotNil(t, rec)
	assert.Equal(t, "bats-runner", rec.Adapter)
}

// TestLinkRecord_DefaultSelectsAdapter_NoWarning: when defaults.execute
// pins one of the matching adapters, the chosen adapter is unambiguous
// and no warning is emitted.
func TestLinkRecord_DefaultSelectsAdapter_NoWarning(t *testing.T) {
	root, _ := commonLinkFixture(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "test/acceptance"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "test/acceptance/sample.bats"), []byte("# bats"), 0644))

	cfg := &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"bats-runner": {Framework: "bats", Mode: "sync", Command: "echo ok"},
				"remote-bats": {Framework: "bats", Mode: "sync", Command: "echo ok"},
			},
		},
		Defaults: map[string]string{"execute": "remote-bats"},
	}

	warnings, err := LinkRecord(root, cfg, "tc-abc12345", "bats",
		"test/acceptance/sample.bats", "main", "", "", false, false)
	require.NoError(t, err)
	// Single-default fast path: no fallback warning even though two
	// adapters match the framework — the default disambiguates.
	assert.Empty(t, warnings)

	rec, _, _ := wiring.Find(root, "tc-abc12345", "bats")
	require.NotNil(t, rec)
	assert.Equal(t, "remote-bats", rec.Adapter)
}

// TestLinkRecord_SingleMatch_NoWarning: the single-match path is
// unambiguous by construction and must not emit a warning.
func TestLinkRecord_SingleMatch_NoWarning(t *testing.T) {
	root, cfg := commonLinkFixture(t)

	warnings, err := LinkRecord(root, cfg, "tc-abc12345", "playwright",
		"tests/sample.spec.ts", "main", "", "", false, false)
	require.NoError(t, err)
	assert.Empty(t, warnings)
}

// --- BUG-057 path-safety tests (CON-023 wiring write side) ---

// TestLinkRecord_RejectsRelativeTraversalOutsideRoot pins that
// LinkRecord refuses to write a wiring record whose artefact path uses
// `..` segments that escape projectRoot, even when the file behind the
// traversal exists on disk.
func TestLinkRecord_RejectsRelativeTraversalOutsideRoot(t *testing.T) {
	root, cfg := commonLinkFixture(t)

	// Create a file outside the project root that the traversal would land on.
	parent := filepath.Dir(root)
	escape := filepath.Join(parent, "traversal-link-target.txt")
	require.NoError(t, os.WriteFile(escape, []byte("outside"), 0644))
	t.Cleanup(func() { _ = os.Remove(escape) })

	_, err := LinkRecord(root, cfg, "tc-abc12345", "playwright",
		"../traversal-link-target.txt", "main", "", "", false, false)
	require.Error(t, err)
	assert.True(t, pathsafe.IsPathSafetyError(err),
		"relative traversal outside project root must produce *pathsafe.PathSafetyError")

	// No wiring file written for an unsafe input.
	rec, _, _ := wiring.Find(root, "tc-abc12345", "playwright")
	assert.Nil(t, rec, "no wiring should be written for an unsafe artefact path")
}

// TestLinkRecord_RejectsAbsoluteOutsideRoot pins that LinkRecord refuses
// to write a wiring record whose artefact path is an absolute path
// outside projectRoot.
func TestLinkRecord_RejectsAbsoluteOutsideRoot(t *testing.T) {
	root, cfg := commonLinkFixture(t)

	outside := t.TempDir()
	outsidePath := filepath.Join(outside, "outside-link-target.txt")
	require.NoError(t, os.WriteFile(outsidePath, []byte("outside"), 0644))

	_, err := LinkRecord(root, cfg, "tc-abc12345", "playwright",
		outsidePath, "main", "", "", false, false)
	require.Error(t, err)
	assert.True(t, pathsafe.IsPathSafetyError(err),
		"absolute outside-root artefact must produce *pathsafe.PathSafetyError")

	rec, _, _ := wiring.Find(root, "tc-abc12345", "playwright")
	assert.Nil(t, rec, "no wiring should be written for an unsafe artefact path")
}

// TestLinkRecord_AbsoluteInsideRootNormalisesToRelative pins that
// LinkRecord normalises an absolute-inside-root --artefact value to the
// project-relative slash-form on disk. Wiring should never store an
// absolute path verbatim.
func TestLinkRecord_AbsoluteInsideRootNormalisesToRelative(t *testing.T) {
	root, cfg := commonLinkFixture(t)

	// The fixture seeds tests/sample.spec.ts. Feed its absolute path.
	absArtefact := filepath.Join(root, "tests", "sample.spec.ts")

	_, err := LinkRecord(root, cfg, "tc-abc12345", "playwright",
		absArtefact, "main", "", "", false, false)
	require.NoError(t, err)

	rec, _, _ := wiring.Find(root, "tc-abc12345", "playwright")
	require.NotNil(t, rec)
	assert.Equal(t, "tests/sample.spec.ts", rec.Artefact,
		"absolute path inside root must be normalised to project-relative slash form")
}

// TestCheckLink_RejectsExistingUnsafeWiring pins that CheckLink fails
// with a path-safety error when an existing wiring record stores an
// artefact path that resolves outside the project root (defense in
// depth for tampered checked-in wiring).
func TestCheckLink_RejectsExistingUnsafeWiring(t *testing.T) {
	root, _ := commonLinkFixture(t)

	// Directly write a tampered wiring record bypassing LinkRecord's
	// safety check — this models a checked-in wiring file that someone
	// edited by hand.
	tampered := &wiring.WiringRecord{
		TestCase:     "tc-abc12345",
		TestCaseHash: "deadbeefdeadbeef",
		Framework:    "playwright",
		Adapter:      "playwright-runner",
		Artefact:     "../traversal-check-target.txt",
		ArtefactHash: "deadbeefdeadbeef",
	}
	_, err := wiring.Write(root, tampered)
	require.NoError(t, err)

	// Even with the file present at the unsafe path, CheckLink must
	// reject it as a path-safety violation.
	parent := filepath.Dir(root)
	escape := filepath.Join(parent, "traversal-check-target.txt")
	require.NoError(t, os.WriteFile(escape, []byte("outside"), 0644))
	t.Cleanup(func() { _ = os.Remove(escape) })

	result, checkErr := CheckLink(root, "tc-abc12345", "playwright", "", false)
	require.Error(t, checkErr)
	assert.True(t, pathsafe.IsPathSafetyError(checkErr),
		"existing wiring with outside-root artefact must yield *pathsafe.PathSafetyError")
	assert.True(t, result.RecordExists, "the wiring record was still found")
	assert.False(t, result.ArtefactExists,
		"unsafe artefact must not be reported as existing")
}

// TestCheckLink_RejectsSuppliedUnsafeArtefact pins that --artefact on a
// CheckLink call is held to the same containment standard as LinkRecord.
func TestCheckLink_RejectsSuppliedUnsafeArtefact(t *testing.T) {
	root, _ := commonLinkFixture(t)

	parent := filepath.Dir(root)
	escape := filepath.Join(parent, "traversal-check-supplied.txt")
	require.NoError(t, os.WriteFile(escape, []byte("outside"), 0644))
	t.Cleanup(func() { _ = os.Remove(escape) })

	_, checkErr := CheckLink(root, "tc-abc12345", "playwright",
		"../traversal-check-supplied.txt", false)
	require.Error(t, checkErr)
	assert.True(t, pathsafe.IsPathSafetyError(checkErr),
		"unsafe --artefact must produce *pathsafe.PathSafetyError")
}
