package link

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
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

// createTCSpecFixture creates a test case spec file under gtms/test/cases/ so that
// (a) testcase.Exists returns true and (b) HashFile can compute testcase-hash.
// CON-023 makes the spec mandatory for wiring writes — the brownfield "link
// without spec" contract is retired.
func createTCSpecFixture(t *testing.T, root, tcID string) {
	t.Helper()
	dir := filepath.Join(root, "gtms", "test", "cases", "test")
	require.NoError(t, os.MkdirAll(dir, 0755))
	spec := "---\ntest_case_id: " + tcID + "\n---\n# Sample\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, tcID+"-sample.md"), []byte(spec), 0644))
}

// createTCSpecAt creates a spec under a specific sub-folder (for the
// folder-qualified target tests below).
func createTCSpecAt(t *testing.T, root, subdir, tcID string) {
	t.Helper()
	dir := filepath.Join(root, "gtms", "test", "cases", subdir)
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
func linkRecordErr(root string, cfg *config.Config, tcID, framework, artefact string, force, strict bool) error {
	_, _, err := LinkRecord(root, cfg, tcID, framework, artefact, force, strict)
	return err
}

func TestLinkRecord_HappyPath_WritesSixFieldWiring(t *testing.T) {
	root, cfg := commonLinkFixture(t)

	err := linkRecordErr(root, cfg, "tc-abc12345", "playwright", "tests/sample.spec.ts", false, false)
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

	err := linkRecordErr(root, emptyCfg, "tc-abc12345", "playwright", "tests/sample.spec.ts", false, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no execute adapter")
	assert.Contains(t, err.Error(), "playwright")

	// No wiring should be created
	rec, _, _ := wiring.Find(root, "tc-abc12345", "playwright")
	assert.Nil(t, rec)
}

func TestLinkRecord_ArtefactMissing(t *testing.T) {
	root, cfg := commonLinkFixture(t)

	err := linkRecordErr(root, cfg, "tc-abc12345", "playwright", "tests/does-not-exist.spec.ts", false, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	assert.Contains(t, err.Error(), "tests/does-not-exist.spec.ts")

	rec, _, _ := wiring.Find(root, "tc-abc12345", "playwright")
	assert.Nil(t, rec)
}

func TestLinkRecord_ExistingWiringNoForce(t *testing.T) {
	root, cfg := commonLinkFixture(t)

	require.NoError(t, linkRecordErr(root, cfg, "tc-abc12345", "playwright", "tests/sample.spec.ts", false, false))

	// Second link without --force fails
	err := linkRecordErr(root, cfg, "tc-abc12345", "playwright", "tests/sample.spec.ts", false, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
	assert.Contains(t, err.Error(), "--force")
}

func TestLinkRecord_ForceOverwritesAndRefreshesHashes(t *testing.T) {
	root, cfg := commonLinkFixture(t)

	require.NoError(t, linkRecordErr(root, cfg, "tc-abc12345", "playwright", "tests/sample.spec.ts", false, false))
	first, _, _ := wiring.Find(root, "tc-abc12345", "playwright")
	require.NotNil(t, first)

	// Edit the artefact so artefact-hash should change on force.
	require.NoError(t, os.WriteFile(filepath.Join(root, "tests/sample.spec.ts"), []byte("test-updated-content"), 0644))

	require.NoError(t, linkRecordErr(root, cfg, "tc-abc12345", "playwright", "tests/sample.spec.ts", true, false))
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

	err := linkRecordErr(root, playwrightConfig(), "tc-deadbeef", "playwright", "tests/sample.spec.ts", false, false)
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
	require.NoError(t, linkRecordErr(root, cfg, "tc-abc12345", "playwright", "tests/sample.spec.ts", false, false))

	result, err := CheckLink(root, "tc-abc12345", "playwright", "", false)
	require.NoError(t, err)
	assert.True(t, result.ArtefactExists)
	assert.True(t, result.RecordExists)
	assert.Equal(t, "tests/sample.spec.ts", result.Artefact)
}

func TestCheckLink_ExistingWiring_ArtefactGone(t *testing.T) {
	root, cfg := commonLinkFixture(t)
	require.NoError(t, linkRecordErr(root, cfg, "tc-abc12345", "playwright", "tests/sample.spec.ts", false, false))
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

// --- BUG-165 manual-framework wiring-free guidance ---

func TestLinkRecord_ManualFramework_GuidesInsteadOfGenericError(t *testing.T) {
	// BUG-165 verification criterion 207: gtms link --framework manual
	// must (a) write no wiring, (b) give prime/fill/execute guidance,
	// (c) NOT emit the generic "no execute adapter configured" error.
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "tests"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "tests/sample.yaml"), []byte("test"), 0644))
	createTCSpecFixture(t, root, "tc-abc12345")

	// Config with both -script variants registered (manual preset shape).
	cfg := &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"manual-execute-script": {Framework: "manual", Script: "gtms/adapters/manual-execute-script.sh", Mode: "sync"},
				"agent-execute-script":  {Framework: "manual", Script: "gtms/adapters/agent-execute-script.sh", Mode: "sync"},
			},
		},
		Defaults: map[string]string{"execute": "manual-execute-script"},
	}

	_, _, err := LinkRecord(root, cfg, "tc-abc12345", "manual", "tests/sample.yaml", false, false)
	require.Error(t, err)

	msg := err.Error()
	// (b) Must reference the prime/fill/execute workflow.
	assert.Contains(t, msg, "gtms prime", "guidance must reference the prime command")
	assert.Contains(t, msg, "gtms execute", "guidance must reference the execute command")
	assert.Contains(t, msg, "wiring-free", "guidance must explain manual is wiring-free")

	// (c) Must NOT contain the generic resolver error.
	assert.NotContains(t, msg, "no execute adapter configured",
		"must NOT emit the generic canonical-resolution error for manual framework")

	// (a) No wiring file written.
	rec, _, _ := wiring.Find(root, "tc-abc12345", "manual")
	assert.Nil(t, rec, "no wiring should be written for manual framework")
}

func TestLinkRecord_ManualFramework_GuidesEvenWithBadArtefact(t *testing.T) {
	// CLAUDE-001: for a manual TC the artefact is irrelevant, so the
	// wiring-free guidance must win even when --artefact does not exist,
	// rather than failing with "artefact file not found". Guards the
	// ordering (manual short-circuit runs before the artefact check).
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "tests"), 0755))
	createTCSpecFixture(t, root, "tc-abc12345")

	cfg := &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"manual-execute-script": {Framework: "manual", Script: "gtms/adapters/manual-execute-script.sh", Mode: "sync"},
			},
		},
		Defaults: map[string]string{"execute": "manual-execute-script"},
	}

	// Nonexistent artefact path -- guidance must still win.
	_, _, err := LinkRecord(root, cfg, "tc-abc12345", "manual", "tests/does-not-exist.yaml", false, false)
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "wiring-free", "manual guidance must win over artefact validation")
	assert.NotContains(t, msg, "artefact file not found",
		"manual short-circuit must run before the artefact-existence check (CLAUDE-001)")
}

// --- BUG-059 strict-mode tests (kept for CLI compatibility) ---

func TestLinkRecord_StrictRejectsPhantom(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/test/cases"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "tests"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "tests/sample.spec.ts"), []byte("test"), 0644))

	err := linkRecordErr(root, playwrightConfig(), "tc-deadbeef", "playwright", "tests/sample.spec.ts", false, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found in gtms/test/cases")

	rec, _, _ := wiring.Find(root, "tc-deadbeef", "playwright")
	assert.Nil(t, rec)
}

func TestLinkRecord_StrictAcceptsRealTC(t *testing.T) {
	root, cfg := commonLinkFixture(t)

	err := linkRecordErr(root, cfg, "tc-abc12345", "playwright", "tests/sample.spec.ts", false, true)
	require.NoError(t, err)

	rec, _, _ := wiring.Find(root, "tc-abc12345", "playwright")
	require.NotNil(t, rec)
	assert.Equal(t, "tc-abc12345", rec.TestCase)
}

func TestCheckLink_StrictRejectsPhantom(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/test/cases"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "tests"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "tests/sample.spec.ts"), []byte("test"), 0644))

	result, err := CheckLink(root, "tc-deadbeef", "playwright", "tests/sample.spec.ts", true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found in gtms/test/cases")
	assert.False(t, result.RecordExists)
}

func TestLinkRecord_StrictArtefactMissingStillErrors(t *testing.T) {
	root, cfg := commonLinkFixture(t)

	err := linkRecordErr(root, cfg, "tc-abc12345", "playwright", "tests/does-not-exist.spec.ts", false, true)
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

	err := linkRecordErr(root, playwrightConfig(), "login/tc-abc12345", "playwright", "tests/sample.spec.ts", false, true)
	require.NoError(t, err)

	rec, _, _ := wiring.Find(root, "tc-abc12345", "playwright")
	require.NotNil(t, rec)
}

func TestBUG059_LinkRecord_FolderQualifiedWrongFolder(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "tests"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "tests/sample.spec.ts"), []byte("test"), 0644))
	createTCSpecAt(t, root, "login", "tc-abc12345")

	err := linkRecordErr(root, playwrightConfig(), "checkout/tc-abc12345", "playwright", "tests/sample.spec.ts", false, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found in gtms/test/cases")
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

	_, warnings, err := LinkRecord(root, cfg, "tc-abc12345", "bats",
		"test/acceptance/sample.bats", false, false)
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

	_, warnings, err := LinkRecord(root, cfg, "tc-abc12345", "bats",
		"test/acceptance/sample.bats", false, false)
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

	_, warnings, err := LinkRecord(root, cfg, "tc-abc12345", "playwright",
		"tests/sample.spec.ts", false, false)
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

	_, _, err := LinkRecord(root, cfg, "tc-abc12345", "playwright",
		"../traversal-link-target.txt", false, false)
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

	_, _, err := LinkRecord(root, cfg, "tc-abc12345", "playwright",
		outsidePath, false, false)
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

	_, _, err := LinkRecord(root, cfg, "tc-abc12345", "playwright",
		absArtefact, false, false)
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

// --- ENH-156 RefreshRecord tests ---

// refreshFixture seeds a fully linked TC ready for refresh tests. Returns
// (projectRoot, config, tcID). The wiring record is written via LinkRecord.
func refreshFixture(t *testing.T) (string, *config.Config) {
	t.Helper()
	root := t.TempDir()
	cfg := playwrightConfig()

	// Spec
	createTCSpecFixture(t, root, "tc-refresh1")

	// Artefact
	require.NoError(t, os.MkdirAll(filepath.Join(root, "tests"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "tests/tc-refresh1.spec.ts"), []byte("test content"), 0644))

	// Link
	_, _, err := LinkRecord(root, cfg, "tc-refresh1", "playwright",
		"tests/tc-refresh1.spec.ts", false, false)
	require.NoError(t, err)

	return root, cfg
}

func TestRefreshRecord_HappyPath_RefreshesStaleTestcaseHash(t *testing.T) {
	root, _ := refreshFixture(t)

	// Read original wiring
	origRec, _, err := wiring.Find(root, "tc-refresh1", "playwright")
	require.NoError(t, err)
	require.NotNil(t, origRec)
	oldTCHash := origRec.TestCaseHash

	// Edit the spec to create testcase-hash drift
	specDir := filepath.Join(root, "gtms", "test", "cases", "test")
	entries, _ := os.ReadDir(specDir)
	for _, e := range entries {
		if !e.IsDir() {
			specFile := filepath.Join(specDir, e.Name())
			f, _ := os.OpenFile(specFile, os.O_APPEND|os.O_WRONLY, 0644)
			_, _ = f.WriteString("\n## ENH-156 drift\n")
			_ = f.Close()
		}
	}

	// Refresh
	refreshed, err := RefreshRecord(root, origRec)
	require.NoError(t, err)
	assert.True(t, refreshed, "should report the record was refreshed")

	// Verify the wiring on disk
	updated, _, err := wiring.Find(root, "tc-refresh1", "playwright")
	require.NoError(t, err)
	assert.NotEqual(t, oldTCHash, updated.TestCaseHash, "testcase-hash should change")
	assert.Equal(t, origRec.ArtefactHash, updated.ArtefactHash, "artefact-hash should be unchanged")
	assert.Equal(t, "playwright", updated.Framework)
	assert.Equal(t, "playwright-runner", updated.Adapter)
	assert.Equal(t, "tests/tc-refresh1.spec.ts", updated.Artefact)
}

func TestRefreshRecord_NoOp_WhenAlreadyCurrent(t *testing.T) {
	root, _ := refreshFixture(t)

	rec, _, err := wiring.Find(root, "tc-refresh1", "playwright")
	require.NoError(t, err)

	refreshed, err := RefreshRecord(root, rec)
	require.NoError(t, err)
	assert.False(t, refreshed, "should report no-op when hashes already match")
}

func TestRefreshRecord_RefreshesArtefactHash(t *testing.T) {
	root, _ := refreshFixture(t)

	rec, _, err := wiring.Find(root, "tc-refresh1", "playwright")
	require.NoError(t, err)
	oldArtHash := rec.ArtefactHash

	// Edit artefact to create drift
	artFile := filepath.Join(root, "tests", "tc-refresh1.spec.ts")
	f, _ := os.OpenFile(artFile, os.O_APPEND|os.O_WRONLY, 0644)
	_, _ = f.WriteString("\n// ENH-156 artefact drift\n")
	_ = f.Close()

	refreshed, err := RefreshRecord(root, rec)
	require.NoError(t, err)
	assert.True(t, refreshed)

	updated, _, _ := wiring.Find(root, "tc-refresh1", "playwright")
	assert.NotEqual(t, oldArtHash, updated.ArtefactHash, "artefact-hash should change")
	assert.Equal(t, rec.TestCaseHash, updated.TestCaseHash, "testcase-hash should be stable")
}

func TestRefreshRecord_PendingArtefactHash_Preserved(t *testing.T) {
	root := t.TempDir()
	createTCSpecFixture(t, root, "tc-pending1")

	// Manually seed wiring with pending artefact hash
	require.NoError(t, os.MkdirAll(filepath.Join(root, "tests"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "tests/tc-pending1.bats"), []byte("# skeleton"), 0644))

	// We need a real testcase hash for the seed
	specPath, _ := pipeline.ResolveTestCaseSpec(root, "tc-pending1")
	oldTCHash, _ := pipeline.HashFile(filepath.Join(root, filepath.FromSlash(specPath)))

	pendingRec := &wiring.WiringRecord{
		TestCase:     "tc-pending1",
		TestCaseHash: oldTCHash,
		Framework:    "bats",
		Adapter:      "bats-runner",
		Artefact:     "tests/tc-pending1.bats",
		ArtefactHash: wiring.PendingArtefactHash,
	}
	_, err := wiring.Write(root, pendingRec)
	require.NoError(t, err)

	// Edit spec to drift testcase-hash
	specDir := filepath.Join(root, "gtms", "test", "cases", "test")
	entries, _ := os.ReadDir(specDir)
	for _, e := range entries {
		if !e.IsDir() {
			specFile := filepath.Join(specDir, e.Name())
			f, _ := os.OpenFile(specFile, os.O_APPEND|os.O_WRONLY, 0644)
			_, _ = f.WriteString("\n## pending drift\n")
			_ = f.Close()
		}
	}

	refreshed, err := RefreshRecord(root, pendingRec)
	require.NoError(t, err)
	assert.True(t, refreshed)

	updated, _, _ := wiring.Find(root, "tc-pending1", "bats")
	assert.NotEqual(t, oldTCHash, updated.TestCaseHash, "testcase-hash should change")
	assert.Equal(t, wiring.PendingArtefactHash, updated.ArtefactHash,
		"artefact-hash must remain 'pending'")
}

func TestRefreshRecord_MissingArtefact_Fails(t *testing.T) {
	root, _ := refreshFixture(t)

	rec, _, _ := wiring.Find(root, "tc-refresh1", "playwright")

	// Delete artefact
	require.NoError(t, os.Remove(filepath.Join(root, "tests", "tc-refresh1.spec.ts")))

	refreshed, err := RefreshRecord(root, rec)
	require.Error(t, err)
	assert.False(t, refreshed)
	assert.Contains(t, err.Error(), "artefact file not found")
}

func TestRefreshRecord_UnsafeArtefactPath_Fails(t *testing.T) {
	root := t.TempDir()
	createTCSpecFixture(t, root, "tc-unsafe1")

	// Seed tampered wiring with escaping path
	tampered := &wiring.WiringRecord{
		TestCase:     "tc-unsafe1",
		TestCaseHash: "deadbeefdeadbeef",
		Framework:    "bats",
		Adapter:      "bats-runner",
		Artefact:     "../outside-project/tc-unsafe1.bats",
		ArtefactHash: "deadbeefdeadbeef",
	}
	_, err := wiring.Write(root, tampered)
	require.NoError(t, err)

	refreshed, err := RefreshRecord(root, tampered)
	require.Error(t, err)
	assert.False(t, refreshed)
	assert.Contains(t, err.Error(), "unsafe artefact path")
}

func TestRefreshRecord_MissingSpec_Fails(t *testing.T) {
	root := t.TempDir()

	// Create artefact but no spec
	require.NoError(t, os.MkdirAll(filepath.Join(root, "tests"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "tests/tc-nospec1.spec.ts"), []byte("test"), 0644))
	// Create gtms/test/cases dir so the directory scan works
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms", "test", "cases"), 0755))

	rec := &wiring.WiringRecord{
		TestCase:     "tc-nospec1",
		TestCaseHash: "deadbeefdeadbeef",
		Framework:    "playwright",
		Adapter:      "playwright-runner",
		Artefact:     "tests/tc-nospec1.spec.ts",
		ArtefactHash: "deadbeefdeadbeef",
	}
	_, _ = wiring.Write(root, rec)

	refreshed, err := RefreshRecord(root, rec)
	require.Error(t, err)
	assert.False(t, refreshed)
	assert.Contains(t, err.Error(), "spec not found")
}

// --- ENH-192: RepointRecord tests ---

// repointFixture seeds a wired TC with a real wiring record on disk.
func repointFixture(t *testing.T) (string, *wiring.WiringRecord) {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "tests"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "tests/sample.spec.ts"), []byte("test"), 0644))
	createTCSpecFixture(t, root, "tc-abc12345")

	rec := &wiring.WiringRecord{
		TestCase:     "tc-abc12345",
		TestCaseHash: "00112233aabbccdd",
		Framework:    "playwright",
		Adapter:      "old-runner",
		Artefact:     "tests/sample.spec.ts",
		ArtefactHash: "deadbeefcafef00d",
	}
	_, err := wiring.Write(root, rec)
	require.NoError(t, err)
	return root, rec
}

func TestRepointRecord_ChangesOnlyAdapterField(t *testing.T) {
	root, rec := repointFixture(t)

	result, err := RepointRecord(root, rec, "new-runner", false)
	require.NoError(t, err)
	assert.Equal(t, "repointed", result.Status)

	updated, _, findErr := wiring.Find(root, rec.TestCase, rec.Framework)
	require.NoError(t, findErr)
	require.NotNil(t, updated)
	assert.Equal(t, "new-runner", updated.Adapter)
	assert.Equal(t, rec.TestCase, updated.TestCase)
	assert.Equal(t, rec.TestCaseHash, updated.TestCaseHash)
	assert.Equal(t, rec.Framework, updated.Framework)
	assert.Equal(t, rec.Artefact, updated.Artefact)
	assert.Equal(t, rec.ArtefactHash, updated.ArtefactHash)
}

func TestRepointRecord_PreservesStaleHash(t *testing.T) {
	root, rec := repointFixture(t)
	// Hash is already arbitrary (not matching file content) -- a stale hash.
	result, err := RepointRecord(root, rec, "new-runner", false)
	require.NoError(t, err)
	assert.Equal(t, "repointed", result.Status)

	updated, _, _ := wiring.Find(root, rec.TestCase, rec.Framework)
	assert.Equal(t, "deadbeefcafef00d", updated.ArtefactHash, "stale hash preserved")
}

func TestRepointRecord_PreservesPendingHash(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "tests"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "tests/sample.spec.ts"), []byte("test"), 0644))

	rec := &wiring.WiringRecord{
		TestCase:     "tc-abc12345",
		TestCaseHash: "00112233aabbccdd",
		Framework:    "playwright",
		Adapter:      "old-runner",
		Artefact:     "tests/sample.spec.ts",
		ArtefactHash: wiring.PendingArtefactHash,
	}
	_, err := wiring.Write(root, rec)
	require.NoError(t, err)

	result, repErr := RepointRecord(root, rec, "new-runner", false)
	require.NoError(t, repErr)
	assert.Equal(t, "repointed", result.Status)

	updated, _, _ := wiring.Find(root, rec.TestCase, rec.Framework)
	assert.Equal(t, wiring.PendingArtefactHash, updated.ArtefactHash, "pending hash preserved")
}

func TestRepointRecord_DryRunWritesNothing(t *testing.T) {
	root, rec := repointFixture(t)

	wiringPath, _ := wiring.Path(root, rec.TestCase, rec.Framework)
	before, _ := os.ReadFile(wiringPath)

	result, err := RepointRecord(root, rec, "new-runner", true)
	require.NoError(t, err)
	assert.Equal(t, "repointed", result.Status)

	after, _ := os.ReadFile(wiringPath)
	assert.Equal(t, before, after, "dry-run must not modify the wiring file")
}

func TestRepointRecord_MissingArtefactWarnsAndRepoints(t *testing.T) {
	root := t.TempDir()
	rec := &wiring.WiringRecord{
		TestCase:     "tc-abc12345",
		TestCaseHash: "00112233aabbccdd",
		Framework:    "playwright",
		Adapter:      "old-runner",
		Artefact:     "tests/missing.spec.ts",
		ArtefactHash: "deadbeefcafef00d",
	}
	_, err := wiring.Write(root, rec)
	require.NoError(t, err)

	result, repErr := RepointRecord(root, rec, "new-runner", false)
	require.NoError(t, repErr)
	assert.Equal(t, "repointed", result.Status)
	assert.Contains(t, result.Warning, "artefact file not found")
}

func TestRepointRecord_AbsoluteInRootArtefactNoWarning(t *testing.T) {
	// An absolute stored artefact path that resolves inside the project root and
	// exists on disk must NOT trigger a missing-artefact warning. Guards the
	// ResolveUnderRoot-vs-naive-Join fix: filepath.Join(root, absPath) would
	// mangle the absolute path and spuriously report the file missing.
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "tests"), 0755))
	absArtefact := filepath.Join(root, "tests", "sample.spec.ts")
	require.NoError(t, os.WriteFile(absArtefact, []byte("test"), 0644))

	rec := &wiring.WiringRecord{
		TestCase:     "tc-abc12345",
		TestCaseHash: "00112233aabbccdd",
		Framework:    "playwright",
		Adapter:      "old-runner",
		Artefact:     absArtefact,
		ArtefactHash: "deadbeefcafef00d",
	}
	_, err := wiring.Write(root, rec)
	require.NoError(t, err)

	result, repErr := RepointRecord(root, rec, "new-runner", false)
	require.NoError(t, repErr)
	assert.Equal(t, "repointed", result.Status)
	assert.Empty(t, result.Warning, "existing absolute in-root artefact must not warn")

	updated, _, _ := wiring.Find(root, rec.TestCase, rec.Framework)
	require.NotNil(t, updated)
	assert.Equal(t, "new-runner", updated.Adapter)
	assert.Equal(t, absArtefact, updated.Artefact, "absolute artefact path preserved")
}

func TestRepointRecord_RootEscapingArtefactErrors(t *testing.T) {
	root := t.TempDir()
	rec := &wiring.WiringRecord{
		TestCase:     "tc-abc12345",
		TestCaseHash: "00112233aabbccdd",
		Framework:    "playwright",
		Adapter:      "old-runner",
		Artefact:     "../../etc/passwd",
		ArtefactHash: "deadbeefcafef00d",
	}
	_, err := wiring.Write(root, rec)
	require.NoError(t, err)

	result, repErr := RepointRecord(root, rec, "new-runner", false)
	require.NoError(t, repErr)
	assert.Equal(t, "error", result.Status)
	assert.Contains(t, result.Error.Error(), "unsafe artefact path")
}

func TestRepointRecord_StaleSelectionDetected(t *testing.T) {
	// CODEX-001: a concurrent writer changes the record AFTER the caller selected
	// `rec` at discovery time but BEFORE RepointRecord runs. RepointRecord's
	// authoritative re-read must detect the drift against the selection state and
	// refuse, rather than clobber the concurrent change with stale fields.
	root, rec := repointFixture(t) // rec = old-runner, on disk

	concurrent := &wiring.WiringRecord{
		TestCase:     rec.TestCase,
		TestCaseHash: rec.TestCaseHash,
		Framework:    rec.Framework,
		Adapter:      "concurrent-writer",
		Artefact:     rec.Artefact,
		ArtefactHash: rec.ArtefactHash,
	}
	_, err := wiring.Write(root, concurrent)
	require.NoError(t, err)

	// Call with the STALE selection (rec still says old-runner).
	result, repErr := RepointRecord(root, rec, "new-runner", false)
	require.NoError(t, repErr)
	assert.Equal(t, "error", result.Status)
	assert.Contains(t, result.Error.Error(), "concurrent modification")

	// The concurrent write survived -- not overwritten by the stale selection.
	onDisk, _, _ := wiring.Find(root, rec.TestCase, rec.Framework)
	require.NotNil(t, onDisk)
	assert.Equal(t, "concurrent-writer", onDisk.Adapter)
}

// --- RepointBatch tests ---

func TestRepointBatch_AllOrNothingFrameworkPreflight(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "tests"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "tests/a.spec.ts"), []byte("a"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "tests/b.bats"), []byte("b"), 0644))

	recA := &wiring.WiringRecord{
		TestCase: "tc-aaaa1111", TestCaseHash: "00112233aabbccdd",
		Framework: "playwright", Adapter: "old-runner",
		Artefact: "tests/a.spec.ts", ArtefactHash: "deadbeefcafef00d",
	}
	recB := &wiring.WiringRecord{
		TestCase: "tc-bbbb2222", TestCaseHash: "00112233aabbccdd",
		Framework: "bats", Adapter: "old-runner",
		Artefact: "tests/b.bats", ArtefactHash: "deadbeefcafef00d",
	}
	_, _ = wiring.Write(root, recA)
	_, _ = wiring.Write(root, recB)

	records := []*wiring.WiringRecord{recA, recB}
	summary := RepointBatch(root, records, nil, "new-runner", "playwright", false)

	// Must fail entirely -- no records repointed.
	assert.Equal(t, 0, summary.Repointed)
	assert.True(t, summary.Errors > 0, "framework mismatch must produce errors")

	// Verify files unchanged.
	updatedA, _, _ := wiring.Find(root, "tc-aaaa1111", "playwright")
	assert.Equal(t, "old-runner", updatedA.Adapter, "compatible record must NOT be partially updated")
	updatedB, _, _ := wiring.Find(root, "tc-bbbb2222", "bats")
	assert.Equal(t, "old-runner", updatedB.Adapter)
}

func TestRepointBatch_MalformedInScopeErrors(t *testing.T) {
	root := t.TempDir()
	malformed := []wiring.DiscoveryResult{
		{Path: "bad.wiring.yaml", TCFromName: "tc-bad", Err: fmt.Errorf("parse error")},
	}
	summary := RepointBatch(root, nil, malformed, "new-runner", "playwright", false)
	assert.Equal(t, 1, summary.Errors)
	assert.Equal(t, 0, summary.Repointed)
}

func TestRepointBatch_IdempotentRerun(t *testing.T) {
	root := t.TempDir()
	// Empty batch -- no records matched.
	summary := RepointBatch(root, nil, nil, "new-runner", "playwright", false)
	assert.Equal(t, 0, summary.Repointed)
	assert.Equal(t, 0, summary.Errors)
}

// --- AmbiguityCheck tests (count-based, project-wide; supersedes the CODEX-007
// path classification per REV-109 round 4, CODEX-011/CLAUDE-001) ---

// scopeTCIDs runs discoverScopeSpecs and returns the targeted TC-ID set, matching
// how RepointBulk drives AmbiguityCheck.
func scopeTCIDs(t *testing.T, root, folder string, recursive bool) map[string]bool {
	t.Helper()
	tcIDs, err := discoverScopeSpecs(root, folder, recursive)
	require.NoError(t, err)
	return tcIDs
}

func TestAmbiguityCheck_UniqueIDPasses(t *testing.T) {
	root := t.TempDir()
	createTCSpecAt(t, root, "feature-a", "tc-aaa11111")
	createTCSpecAt(t, root, "feature-b", "tc-bbb22222")

	assert.NoError(t, AmbiguityCheck(root, scopeTCIDs(t, root, "feature-a", false)))
}

func TestAmbiguityCheck_DuplicateOutsideScopeErrors(t *testing.T) {
	root := t.TempDir()
	createTCSpecAt(t, root, "feature-a", "tc-aaa11111")
	createTCSpecAt(t, root, "feature-b", "tc-aaa11111")

	err := AmbiguityCheck(root, scopeTCIDs(t, root, "feature-a", false))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ambiguous")
	assert.Contains(t, err.Error(), "tc-aaa11111")
}

func TestAmbiguityCheck_EmptyTargetsPasses(t *testing.T) {
	root := t.TempDir()
	assert.NoError(t, AmbiguityCheck(root, map[string]bool{}))
}

func TestAmbiguityCheck_ZeroHitTargetAborts(t *testing.T) {
	// A targeted ID with no spec anywhere under cases/ (an unscannable/out-of-tree
	// scope) aborts before any write.
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(layout.TestCasesDir(root), 0755))
	err := AmbiguityCheck(root, map[string]bool{"tc-ghost111": true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no spec file")
}

func TestAmbiguityCheck_NonRecursiveRootPlusSubfolderDuplicateErrors(t *testing.T) {
	// Same TC ID directly in the folder AND in a subfolder. Count-based: two specs
	// anywhere -> ambiguous, even under a non-recursive scope.
	root := t.TempDir()
	createTCSpecAt(t, root, "bulk", "tc-dup11111")
	createTCSpecAt(t, root, "bulk/nested", "tc-dup11111")

	err := AmbiguityCheck(root, scopeTCIDs(t, root, "bulk", false))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ambiguous")
	assert.Contains(t, err.Error(), "tc-dup11111")
}

func TestAmbiguityCheck_RecursiveWhollyInsideDuplicateErrors(t *testing.T) {
	// NEW behaviour (REV-109 round 4, CODEX-011): a duplicate wholly inside a
	// recursive scope is now ambiguous too -- both specs share one wiring record
	// keyed by the bare ID. Asserted deliberately.
	root := t.TempDir()
	createTCSpecAt(t, root, "bulk", "tc-dup22222")
	createTCSpecAt(t, root, "bulk/nested", "tc-dup22222")

	err := AmbiguityCheck(root, scopeTCIDs(t, root, "bulk", true))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ambiguous")
	assert.Contains(t, err.Error(), "tc-dup22222")
}

func TestAmbiguityCheck_TrailingSlashScopeStillDetects(t *testing.T) {
	// A trailing-slash folder target no longer affects classification (count-based).
	root := t.TempDir()
	createTCSpecAt(t, root, "scope", "tc-slash1111")
	createTCSpecAt(t, root, "outside", "tc-slash1111")

	err := AmbiguityCheck(root, scopeTCIDs(t, root, "scope/", false))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ambiguous")
}

func TestAmbiguityCheck_ScopeSymlinkedInsideCasesNoError(t *testing.T) {
	// A scope folder that is a symlink to another dir INSIDE cases/ resolves to one
	// physical spec per ID -> no ambiguity (count-based is symlink-immune).
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on Windows")
	}
	root := t.TempDir()
	createTCSpecAt(t, root, "real", "tc-sym111111")
	casesDir := layout.TestCasesDir(root)
	require.NoError(t, os.Symlink(filepath.Join(casesDir, "real"), filepath.Join(casesDir, "alias")))

	tcIDs, err := discoverScopeSpecs(root, "alias", false)
	require.NoError(t, err)
	assert.NoError(t, AmbiguityCheck(root, tcIDs))
}

func TestDiscoverScopeSpecs_EscapingSymlinkRejected(t *testing.T) {
	// CODEX-015: a scope folder symlinked OUTSIDE cases/ is rejected at discovery,
	// so an out-of-tree alias never reaches the ambiguity/selection stages.
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on Windows")
	}
	root := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "tc-out11111-test.md"),
		[]byte("---\ntest_case_id: tc-out11111\n---\n"), 0644))
	casesDir := layout.TestCasesDir(root)
	require.NoError(t, os.MkdirAll(casesDir, 0755))
	require.NoError(t, os.Symlink(outside, filepath.Join(casesDir, "alias")))

	_, err := discoverScopeSpecs(root, "alias", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "outside")
}

func TestAmbiguityCheck_WalkErrorAborts(t *testing.T) {
	// CODEX-004: a traversal error must abort the preflight, not silently shrink the
	// scan. Exercised via an unreadable subdirectory (POSIX; skipped when perms
	// cannot block reads, e.g. running as root).
	if runtime.GOOS == "windows" {
		t.Skip("unreadable-directory permission semantics differ on Windows")
	}
	root := t.TempDir()
	casesDir := layout.TestCasesDir(root)
	blocked := filepath.Join(casesDir, "blocked")
	require.NoError(t, os.MkdirAll(blocked, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(blocked, "tc-blk11111-test.md"), []byte("x"), 0644))
	require.NoError(t, os.Chmod(blocked, 0000))
	defer os.Chmod(blocked, 0755) //nolint:errcheck // best-effort restore for cleanup
	if _, err := os.ReadDir(blocked); err == nil {
		t.Skip("process can read a 0000 directory (likely root); cannot exercise a walk error")
	}

	err := AmbiguityCheck(root, map[string]bool{"tc-any11111": true})
	require.Error(t, err)
}

// --- Core repoint operation tests (CODEX-008/CODEX-016 hoist) ---

// execConfig builds a minimal *config.Config registering the given execute
// adapters under one framework, so the core repoint entry points can resolve
// the new adapter (existence + Mode-3 exclusion + framework derivation).
func execConfig(framework string, adapters ...string) *config.Config {
	execs := map[string]*config.AdapterConfig{}
	for _, a := range adapters {
		execs[a] = &config.AdapterConfig{Framework: framework, Mode: "sync", Command: "true"}
	}
	def := ""
	if len(adapters) > 0 {
		def = adapters[0]
	}
	return &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{"execute": execs},
		Defaults: map[string]string{"execute": def},
	}
}

func mockfwOpts(from, newAdapter string) RepointOptions {
	return RepointOptions{
		Config:      execConfig("mockfw", "old-runner", "new-runner", "other-runner"),
		FromAdapter: from,
		NewAdapter:  newAdapter,
	}
}

func singleOpts(from, newAdapter string) RepointOptions {
	// repointFixture seeds a playwright record, so resolve against playwright adapters.
	return RepointOptions{
		Config:      execConfig("playwright", "old-runner", "new-runner"),
		FromAdapter: from,
		NewAdapter:  newAdapter,
	}
}

// seedBulkFixture creates a spec (under folder) and a matching mockfw wiring
// record for a bulk-repoint test.
func seedBulkFixture(t *testing.T, root, folder, tcID, adapter string) {
	t.Helper()
	createTCSpecAt(t, root, folder, tcID)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "tests"), 0755))
	art := filepath.Join("tests", tcID+".mock")
	require.NoError(t, os.WriteFile(filepath.Join(root, art), []byte("x"), 0644))
	rec := &wiring.WiringRecord{
		TestCase:     tcID,
		TestCaseHash: "00112233aabbccdd",
		Framework:    "mockfw",
		Adapter:      adapter,
		Artefact:     filepath.ToSlash(art),
		ArtefactHash: "deadbeefcafef00d",
	}
	_, err := wiring.Write(root, rec)
	require.NoError(t, err)
}

func TestRepointBulk_MixedBatchCountsSkipped(t *testing.T) {
	// CODEX-006 at the core level: one matching + one non-matching in-scope record.
	root := t.TempDir()
	seedBulkFixture(t, root, "bulk", "tc-33333331", "old-runner")
	seedBulkFixture(t, root, "bulk", "tc-33333332", "other-runner")

	summary, err := RepointBulk(root, "bulk", false, mockfwOpts("old-runner", "new-runner"))
	require.NoError(t, err)
	assert.Equal(t, 1, summary.Repointed)
	assert.Equal(t, 1, summary.Skipped)
	assert.Equal(t, 0, summary.Errors)
}

func TestRepointBulk_TrailingSlashScopeDetectsAmbiguity(t *testing.T) {
	root := t.TempDir()
	seedBulkFixture(t, root, "scope", "tc-44444441", "old-runner")
	createTCSpecAt(t, root, "outside", "tc-44444441") // out-of-scope duplicate

	_, err := RepointBulk(root, "scope/", false, mockfwOpts("old-runner", "new-runner"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ambiguous")
}

func TestRepointBulk_RejectsMode3Adapter(t *testing.T) {
	// CODEX-016: the core op self-enforces Option A -- a Mode-3 reserved adapter name
	// is rejected before any mutation, even when called directly (not via the CLI).
	root := t.TempDir()
	seedBulkFixture(t, root, "bulk", "tc-33333331", "old-runner")

	_, err := RepointBulk(root, "bulk", false, RepointOptions{
		Config:      execConfig("mockfw", "old-runner", "new-runner"),
		FromAdapter: "old-runner", NewAdapter: "manual-execute",
	})
	require.Error(t, err)
}

func TestRepointBulk_RejectsUnconfiguredAdapter(t *testing.T) {
	// CODEX-016: an adapter not configured under adapters.execute is rejected in core.
	root := t.TempDir()
	seedBulkFixture(t, root, "bulk", "tc-33333331", "old-runner")

	_, err := RepointBulk(root, "bulk", false, RepointOptions{
		Config:      execConfig("mockfw", "old-runner", "new-runner"),
		FromAdapter: "old-runner", NewAdapter: "ghost-runner",
	})
	require.Error(t, err)
}

func TestRepointBulk_EscapingScopeSymlinkRejected(t *testing.T) {
	// CODEX-015: an out-of-tree scope symlink whose spec ID collides with a real
	// in-tree TC is rejected before any mutation -- the in-tree wiring is untouched.
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on Windows")
	}
	root := t.TempDir()
	seedBulkFixture(t, root, "real", "tc-55550001", "old-runner")
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "tc-55550001-test.md"),
		[]byte("---\ntest_case_id: tc-55550001\n---\n"), 0644))
	casesDir := layout.TestCasesDir(root)
	require.NoError(t, os.Symlink(outside, filepath.Join(casesDir, "alias")))

	_, err := RepointBulk(root, "alias", false, mockfwOpts("old-runner", "new-runner"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "outside")

	// The colliding in-tree wiring was NOT mutated.
	rec, _, _ := wiring.Find(root, "tc-55550001", "mockfw")
	require.NotNil(t, rec)
	assert.Equal(t, "old-runner", rec.Adapter)
}

func TestRepointSingle_AlreadyCurrent(t *testing.T) {
	root, rec := repointFixture(t) // adapter old-runner (playwright)
	res, err := RepointSingle(root, rec.TestCase, singleOpts("", "old-runner"))
	require.NoError(t, err)
	assert.Equal(t, "already-current", res.Status)
}

func TestRepointSingle_FromAdapterMismatchErrors(t *testing.T) {
	root, rec := repointFixture(t) // adapter old-runner
	_, err := RepointSingle(root, rec.TestCase, singleOpts("wrong-runner", "new-runner"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is currently adapter")
}

func TestRepointSingle_Repoints(t *testing.T) {
	root, rec := repointFixture(t) // adapter old-runner
	res, err := RepointSingle(root, rec.TestCase, singleOpts("old-runner", "new-runner"))
	require.NoError(t, err)
	assert.Equal(t, "repointed", res.Status)
	updated, _, _ := wiring.Find(root, rec.TestCase, rec.Framework)
	require.NotNil(t, updated)
	assert.Equal(t, "new-runner", updated.Adapter)
}

func TestRepointSingle_RejectsMode3Adapter(t *testing.T) {
	// CODEX-016: single-TC repoint self-enforces Option A too.
	root, rec := repointFixture(t)
	_, err := RepointSingle(root, rec.TestCase, singleOpts("old-runner", "agent-execute-script"))
	require.Error(t, err)
}
