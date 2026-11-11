package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBUG202_BatsTemplateForceUpgradePath proves BUG-202 made the bats automate
// skeleton template an upgradeable preset asset (it had no prior-versions entry
// before) without making it clobberable. A project still holding the
// v0.3.0-v0.4.0 helper-backed body is refreshed in place to the current
// self-contained body; a user-edited template is kept with a companion; the
// current body is skipped. Mirrors TestBUG196_BatsRunnerForceUpgradePath.
func TestBUG202_BatsTemplateForceUpgradePath(t *testing.T) {
	skipIfShort(t)
	tmplRel := "gtms/automation/templates/bats.template.bats"
	tmplPath := func(dir string) string {
		return filepath.Join(dir, "gtms", "automation", "templates", "bats.template.bats")
	}
	writeTmpl := func(dir, content string) {
		p := tmplPath(dir)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
	readTmpl := func(dir string) string {
		b, err := os.ReadFile(tmplPath(dir))
		require.NoError(t, err)
		return string(b)
	}
	companionPath := func(dir string) string {
		return tmplPath(dir) + ".gtms-new"
	}

	t.Run("pristine v0.4.0 template + force -> refreshed in place", func(t *testing.T) {
		dir := initGitRepo(t)
		writeTmpl(dir, batsAutomateTemplateV040)
		result := &Result{}
		require.NoError(t, installPresetAssets(dir, PresetBats, true, result))
		assert.Equal(t, BATSAutomateTemplate, readTmpl(dir),
			"a pristine v0.4.0 template must be refreshed to the current self-contained body")
		assert.Contains(t, result.FilesCreated, tmplRel)
		assert.NoFileExists(t, companionPath(dir),
			"no companion when a known prior body is refreshed in place")
		assert.Contains(t, strings.Join(result.Notes, "\n"),
			"Upgraded gtms/automation/templates/bats.template.bats to the current shipped version.")
		// The refreshed body must be helper-free.
		assert.NotContains(t, readTmpl(dir), "common-setup.bash")
		assert.NotContains(t, readTmpl(dir), "_common_setup")
	})

	t.Run("user-edited template + force -> kept, companion written", func(t *testing.T) {
		dir := initGitRepo(t)
		edited := batsAutomateTemplateV040 + "\n# team customisation: keep this line\n"
		writeTmpl(dir, edited)
		result := &Result{}
		require.NoError(t, installPresetAssets(dir, PresetBats, true, result))
		assert.Equal(t, edited, readTmpl(dir), "a user-edited template must not be clobbered")
		cb, err := os.ReadFile(companionPath(dir))
		require.NoError(t, err)
		assert.Equal(t, BATSAutomateTemplate, string(cb),
			"the companion carries the current shipped body")
		joined := strings.Join(result.Warnings, "\n")
		assert.Contains(t, joined, tmplRel)
		assert.Contains(t, joined, ".gtms-new")
		// BUG-202 genericised the shared conflict warning: no BUG-189 / Playwright.
		assert.NotContains(t, joined, "BUG-189")
		assert.NotContains(t, joined, "Playwright")
	})

	t.Run("current template + force -> skipped", func(t *testing.T) {
		dir := initGitRepo(t)
		writeTmpl(dir, BATSAutomateTemplate)
		result := &Result{}
		require.NoError(t, installPresetAssets(dir, PresetBats, true, result))
		assert.Contains(t, result.FilesSkipped, tmplRel)
		assert.NotContains(t, result.FilesCreated, tmplRel)
	})

	t.Run("v0.4.0 template WITHOUT force -> left untouched", func(t *testing.T) {
		dir := initGitRepo(t)
		writeTmpl(dir, batsAutomateTemplateV040)
		result := &Result{}
		require.NoError(t, installPresetAssets(dir, PresetBats, false, result))
		assert.Equal(t, batsAutomateTemplateV040, readTmpl(dir),
			"without --force an existing template is never modified")
		assert.Contains(t, result.FilesSkipped, tmplRel)
	})
}

// TestSourceShape_BatsTemplatePriorVersionCatalog guards the BUG-202 upgrade
// catalogue entry: the exact v0.4.0 body is fingerprinted and wired to the
// template's RelPath, and the live template no longer carries the helper.
func TestSourceShape_BatsTemplatePriorVersionCatalog(t *testing.T) {
	require.NotEmpty(t, batsAutomateTemplatePriorVersions,
		"the upgrade path needs at least one known prior version to match against")
	// The seeded prior version must be the affected helper-backed body.
	assert.Contains(t, batsAutomateTemplatePriorVersions[0], "common-setup.bash",
		"the seeded prior version must be the pre-BUG-202 helper-backed template")
	assert.Contains(t, batsAutomateTemplatePriorVersions[0], "_common_setup")

	// The catalogue must be wired to the template's RelPath so the upgrade path finds it.
	prior, ok := PresetAssetPriorVersions["gtms/automation/templates/bats.template.bats"]
	require.True(t, ok, "bats.template.bats must declare prior versions for the upgrade path")
	assert.NotEmpty(t, prior)

	// The current shipped template must be self-contained (the whole point of BUG-202).
	assert.NotContains(t, BATSAutomateTemplate, "common-setup.bash",
		"the current shipped template must not reference the project helper")
	assert.NotContains(t, BATSAutomateTemplate, "_common_setup")
	assert.False(t, isKnownPriorAsset("gtms/automation/templates/bats.template.bats", BATSAutomateTemplate),
		"the current body must not be misclassified as a prior version")
}

// toCRLF converts an LF test literal to the CRLF form Git produces on a Windows
// checkout, so the upgrade path can be exercised against a realistic on-disk copy.
func toCRLF(s string) string {
	return strings.ReplaceAll(s, "\n", "\r\n")
}

// TestBUG202_CRLFPristineTemplateUpgradesInPlace proves CODEX-002: the force
// upgrade path recognises pristine shipped assets regardless of line endings, so
// a Windows checkout of an old template is refreshed in place rather than
// misclassified as a user edit. The refresh always writes the shipped LF bytes.
func TestBUG202_CRLFPristineTemplateUpgradesInPlace(t *testing.T) {
	skipIfShort(t)
	tmplRel := "gtms/automation/templates/bats.template.bats"
	tmplPath := func(dir string) string {
		return filepath.Join(dir, "gtms", "automation", "templates", "bats.template.bats")
	}
	writeTmpl := func(dir, content string) {
		p := tmplPath(dir)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}
	readTmpl := func(dir string) string {
		b, err := os.ReadFile(tmplPath(dir))
		require.NoError(t, err)
		return string(b)
	}
	companionPath := func(dir string) string {
		return tmplPath(dir) + ".gtms-new"
	}

	t.Run("CRLF v0.4.0 template + force -> refreshed in place to LF current", func(t *testing.T) {
		dir := initGitRepo(t)
		writeTmpl(dir, toCRLF(batsAutomateTemplateV040))
		result := &Result{}
		require.NoError(t, installPresetAssets(dir, PresetBats, true, result))
		assert.Equal(t, BATSAutomateTemplate, readTmpl(dir),
			"a CRLF pristine v0.4.0 template must be refreshed to the current LF body")
		assert.Contains(t, result.FilesCreated, tmplRel)
		assert.NoFileExists(t, companionPath(dir),
			"a recognised CRLF prior template must not get a .gtms-new companion")
	})

	t.Run("CRLF copy of the current template + force -> treated as current (skipped)", func(t *testing.T) {
		dir := initGitRepo(t)
		writeTmpl(dir, toCRLF(BATSAutomateTemplate))
		result := &Result{}
		require.NoError(t, installPresetAssets(dir, PresetBats, true, result))
		assert.Contains(t, result.FilesSkipped, tmplRel)
		assert.NotContains(t, result.FilesCreated, tmplRel)
		assert.NoFileExists(t, companionPath(dir),
			"a CRLF copy of the current template is up to date, not a conflict")
	})
}

// TestBUG202_CRLFPriorRunnerRecognised proves the CODEX-002 fix applies to every
// upgradeable preset asset, not only the bats template: a CRLF copy of a prior
// bats-runner body is still recognised as a known prior version.
func TestBUG202_CRLFPriorRunnerRecognised(t *testing.T) {
	assert.True(t, isKnownPriorAsset("gtms/adapters/bats-runner.sh", toCRLF(batsRunnerScriptPreBUG196)),
		"a CRLF copy of a prior bats-runner body must be recognised as a known prior version")
	assert.True(t, isKnownPriorAsset("gtms/adapters/playwright-runner.sh", toCRLF(playwrightRunnerScriptV1)),
		"a CRLF copy of a prior playwright-runner body must be recognised as a known prior version")
}

// TestBUG202_CRLFCurrentCompanionNotDuplicated proves REV-207 CODEX-R2-001:
// writeCompanion compares an existing .gtms-new EOL-insensitively, so a current
// companion checked out on Windows with CRLF is reused rather than treated as
// user content. A customised template keeps its one companion across repeated
// forced inits -- no .gtms-new.2 is spawned.
func TestBUG202_CRLFCurrentCompanionNotDuplicated(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)
	tmplPath := filepath.Join(dir, "gtms", "automation", "templates", "bats.template.bats")
	require.NoError(t, os.MkdirAll(filepath.Dir(tmplPath), 0o755))

	// A customised (user-edited) template: never clobbered, always drives the
	// companion path.
	customised := batsAutomateTemplateV040 + "\n# team customisation: keep this line\n"
	require.NoError(t, os.WriteFile(tmplPath, []byte(customised), 0o644))

	// The current shipped content already present as a .gtms-new companion, but
	// with Windows CRLF line endings (as a Git checkout would produce).
	companionAbs := tmplPath + ".gtms-new"
	require.NoError(t, os.WriteFile(companionAbs, []byte(toCRLF(BATSAutomateTemplate)), 0o644))

	result := &Result{}
	require.NoError(t, installPresetAssets(dir, PresetBats, true, result))

	// The customised template is untouched.
	got, err := os.ReadFile(tmplPath)
	require.NoError(t, err)
	assert.Equal(t, customised, string(got), "a customised template must never be clobbered")

	// The CRLF companion is reused, not re-created: its bytes are unchanged and
	// no numbered .gtms-new.2 was spawned.
	companionGot, err := os.ReadFile(companionAbs)
	require.NoError(t, err)
	assert.Equal(t, toCRLF(BATSAutomateTemplate), string(companionGot),
		"the existing CRLF companion must be left exactly as found")
	assert.NoFileExists(t, tmplPath+".gtms-new.2",
		"a CRLF copy of the current shipped content must not spawn a numbered companion")
}
