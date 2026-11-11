package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BUG-203: a project scaffolded by any v0.3.x release keeps its own copies of the
// all-preset producer files (both testcase templates, both result templates, the
// create/prime scripts, the authoring guide, the result schema, the gtms-tests-create
// starter skill) and the demo create adapter. In v0.4.0 every create path stamps from
// the project's copy and strict validation then rejects the pre-v2 header, so the first
// gtms create after upgrading fails. gtms init --force now upgrades an unedited v0.3.x
// copy in place (LF or CRLF), keeps an edited copy with a .gtms-new companion, and leaves
// a current copy alone -- reusing the BUG-189/196/202 mechanism. These tests exercise
// every recognised file, including _demo/adapters/create-demo.sh, which is the sole CRLF
// coverage for the demo script (the BATS suite covers CRLF for the eleven preset files).

// bug203RecognisedAssets returns every file BUG-203 makes upgradeable: the ten/eleven
// common (all-preset) assets plus the demo create adapter.
func bug203RecognisedAssets(t *testing.T) []PresetAsset {
	t.Helper()
	common, err := commonUpgradeableAssets()
	require.NoError(t, err)
	return append(common, PresetAsset{
		RelPath: demoCreateScriptRelPath,
		Content: demoCreateScript,
		Perm:    0o755,
	})
}

// bug203DifferingPriorOf returns the first registered prior version of relPath that differs
// (EOL-insensitively) from the current shipped content -- the prior that actually upgrades.
// ENH-232 Decision 1 registers a second create-skill prior equal to the current (v0.4.0)
// body while the live body is unchanged, so "exactly one prior" no longer holds; the
// upgrade-path tests need the prior that differs from current.
func bug203DifferingPriorOf(t *testing.T, relPath, current string) string {
	t.Helper()
	priors := PresetAssetPriorVersions[relPath]
	require.NotEmpty(t, priors, "%s must declare at least one prior version", relPath)
	for _, p := range priors {
		if normalizeEOL(p) != normalizeEOL(current) {
			require.NotEqual(t, normalizeEOL(p), "", "%s prior must be non-empty", relPath)
			return p
		}
	}
	t.Fatalf("%s has no prior version differing from current content", relPath)
	return ""
}

func bug203Write(t *testing.T, dir, relPath, content string, perm os.FileMode) {
	t.Helper()
	abs := filepath.Join(dir, filepath.FromSlash(relPath))
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.WriteFile(abs, []byte(content), perm))
}

func bug203Read(t *testing.T, dir, relPath string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(relPath)))
	require.NoError(t, err)
	return string(b)
}

// TestBUG203_EveryRecognisedAssetUpgradePath drives the four force-upgrade cases for
// every recognised file in both LF and CRLF prior forms. AC3 (LF + CRLF), AC4 (edited
// companion), AC5 (current skip).
//
// ENH-232 Decision 1: an asset may now declare more than one recognised prior (the create
// skill has v0.3.3 and v0.4.0). This test verifies EVERY registered prior rather than
// assuming exactly one: a prior that differs from the current content drives the four
// upgrade cases; a prior that equals the current content (a pristine copy of today's
// shipped body) must read as current and be skipped. A separate current-content skip case
// runs once per asset.
func TestBUG203_EveryRecognisedAssetUpgradePath(t *testing.T) {
	skipIfShort(t)

	for _, asset := range bug203RecognisedAssets(t) {
		asset := asset
		priors := PresetAssetPriorVersions[asset.RelPath]
		require.NotEmpty(t, priors, "%s must declare at least one prior version", asset.RelPath)

		for idx, prior := range priors {
			prior := prior
			tag := fmt.Sprintf("%s/prior-%d", asset.RelPath, idx)

			if normalizeEOL(prior) == normalizeEOL(asset.Content) {
				// A registered prior equal to the current shipped body (e.g. the v0.4.0
				// create skill while the live body is unchanged): a pristine copy reads as
				// current and is skipped, not upgraded.
				t.Run(tag+"/equals-current-skipped", func(t *testing.T) {
					dir := initGitRepo(t)
					bug203Write(t, dir, asset.RelPath, prior, asset.Perm)
					result := &Result{}
					require.NoError(t, applyUpgradeableAsset(dir, asset, true, result))
					assert.Contains(t, result.FilesSkipped, asset.RelPath)
					assert.NotContains(t, strings.Join(result.Notes, "\n"), "Upgraded "+asset.RelPath)
					assert.NoFileExists(t, filepath.Join(dir, filepath.FromSlash(asset.RelPath))+".gtms-new")
				})
				continue
			}

			t.Run(tag+"/LF-prior-upgraded", func(t *testing.T) {
				dir := initGitRepo(t)
				bug203Write(t, dir, asset.RelPath, prior, asset.Perm)
				result := &Result{}
				require.NoError(t, applyUpgradeableAsset(dir, asset, true, result))
				assert.Equal(t, asset.Content, bug203Read(t, dir, asset.RelPath),
					"an unedited LF prior copy must be refreshed in place to the current content")
				assert.Contains(t, result.FilesCreated, asset.RelPath)
				assert.Contains(t, strings.Join(result.Notes, "\n"),
					"Upgraded "+asset.RelPath+" to the current shipped version.")
				assert.NoFileExists(t, filepath.Join(dir, filepath.FromSlash(asset.RelPath))+".gtms-new")
			})

			t.Run(tag+"/CRLF-prior-upgraded", func(t *testing.T) {
				dir := initGitRepo(t)
				crlf := strings.ReplaceAll(prior, "\n", "\r\n")
				require.Positive(t, strings.Count(crlf, "\r\n"), "CRLF fixture must contain CRLFs")
				bug203Write(t, dir, asset.RelPath, crlf, asset.Perm)
				result := &Result{}
				require.NoError(t, applyUpgradeableAsset(dir, asset, true, result))
				got := bug203Read(t, dir, asset.RelPath)
				assert.Equal(t, asset.Content, got,
					"a CRLF prior copy must be recognised and refreshed to current (LF) content")
				assert.Equal(t, 0, strings.Count(got, "\r"),
					"the refreshed file must have LF line endings")
				assert.Contains(t, result.FilesCreated, asset.RelPath)
				assert.NoFileExists(t, filepath.Join(dir, filepath.FromSlash(asset.RelPath))+".gtms-new")
			})

			t.Run(tag+"/edited-kept-with-companion", func(t *testing.T) {
				dir := initGitRepo(t)
				edited := prior + "\n# team customisation: keep this line\n"
				bug203Write(t, dir, asset.RelPath, edited, asset.Perm)
				result := &Result{}
				require.NoError(t, applyUpgradeableAsset(dir, asset, true, result))
				assert.Equal(t, edited, bug203Read(t, dir, asset.RelPath),
					"an edited copy must never be clobbered")
				companion := filepath.Join(dir, filepath.FromSlash(asset.RelPath)) + ".gtms-new"
				cb, err := os.ReadFile(companion)
				require.NoError(t, err)
				assert.Equal(t, asset.Content, string(cb),
					"the companion must carry the current shipped content")
				joined := strings.Join(result.Warnings, "\n")
				assert.Contains(t, joined, asset.RelPath+" differs from the shipped version and was left untouched.")
				assert.Contains(t, joined, ".gtms-new")

				// A second forced run must reuse the companion, not create .gtms-new.2.
				result2 := &Result{}
				require.NoError(t, applyUpgradeableAsset(dir, asset, true, result2))
				assert.NoFileExists(t, companion+".2",
					"a matching companion must be reused, not multiplied")
			})
		}

		t.Run(asset.RelPath+"/current-skipped", func(t *testing.T) {
			dir := initGitRepo(t)
			bug203Write(t, dir, asset.RelPath, asset.Content, asset.Perm)
			result := &Result{}
			require.NoError(t, applyUpgradeableAsset(dir, asset, true, result))
			assert.Contains(t, result.FilesSkipped, asset.RelPath)
			assert.NotContains(t, strings.Join(result.Notes, "\n"), "Upgraded "+asset.RelPath)
			assert.NotContains(t, strings.Join(result.Warnings, "\n"), asset.RelPath+" differs")
			assert.NoFileExists(t, filepath.Join(dir, filepath.FromSlash(asset.RelPath))+".gtms-new")
		})
	}
}

// TestBUG203_ExecutableAssetsKeepMode confirms the create/prime/demo scripts stay
// executable after an in-place upgrade (tc-406a3328 postcondition).
func TestBUG203_ExecutableAssetsKeepMode(t *testing.T) {
	skipIfShort(t)
	if runtime.GOOS == "windows" {
		t.Skip("NTFS does not track the Unix executable bit; the mode preservation is meaningful only on Unix")
	}
	for _, asset := range bug203RecognisedAssets(t) {
		if asset.Perm != 0o755 {
			continue
		}
		asset := asset
		t.Run(asset.RelPath, func(t *testing.T) {
			dir := initGitRepo(t)
			bug203Write(t, dir, asset.RelPath, bug203DifferingPriorOf(t, asset.RelPath, asset.Content), asset.Perm)
			require.NoError(t, applyUpgradeableAsset(dir, asset, true, &Result{}))
			info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(asset.RelPath)))
			require.NoError(t, err)
			assert.NotZero(t, info.Mode()&0o100,
				"%s must keep an executable bit after upgrade", asset.RelPath)
		})
	}
}

// TestBUG203_InitForceUpgradesPristineV03xProject is the end-to-end AC1/AC3 check: scaffold
// current, overwrite every common file with its v0.3.x copy, then gtms init --force
// upgrades them all in place and backs up gtms.config.
func TestBUG203_InitForceUpgradesPristineV03xProject(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)
	_, err := Init(Options{ProjectRoot: dir, Name: "v03x", Repo: "org/v03x", Preset: PresetManual})
	require.NoError(t, err)

	common, err := commonUpgradeableAssets()
	require.NoError(t, err)
	for _, asset := range common {
		bug203Write(t, dir, asset.RelPath, bug203DifferingPriorOf(t, asset.RelPath, asset.Content), asset.Perm)
	}

	result, err := Init(Options{ProjectRoot: dir, Name: "v03x", Repo: "org/v03x", Preset: PresetManual, Force: true})
	require.NoError(t, err)

	notes := strings.Join(result.Notes, "\n")
	for _, asset := range common {
		assert.Equal(t, asset.Content, bug203Read(t, dir, asset.RelPath),
			"%s must be upgraded to current", asset.RelPath)
		assert.Contains(t, notes, "Upgraded "+asset.RelPath+" to the current shipped version.")
		assert.NoFileExists(t, filepath.Join(dir, filepath.FromSlash(asset.RelPath))+".gtms-new")
	}
	assert.NotContains(t, strings.Join(result.Warnings, "\n"), "differs from the shipped version")

	// AC9: the user's config was backed up before --force rewrote it.
	require.NotEmpty(t, result.ConfigBackup, "init --force must back up gtms.config")
	assert.FileExists(t, filepath.Join(dir, result.ConfigBackup))
}

// TestBUG203_FreshInitReportsNoUpgradeArtefacts covers AC8 for a preset project: a first
// init writes current content and reports no upgrade/conflict/companion and no config
// backup.
func TestBUG203_FreshInitReportsNoUpgradeArtefacts(t *testing.T) {
	skipIfShort(t)
	for _, preset := range ValidPresets() {
		preset := preset
		t.Run(preset, func(t *testing.T) {
			dir := initGitRepo(t)
			result, err := Init(Options{ProjectRoot: dir, Name: "fresh", Repo: "org/fresh", Preset: preset})
			require.NoError(t, err)

			assert.Empty(t, result.ConfigBackup, "fresh init must not back up a config")
			assert.NoFileExists(t, filepath.Join(dir, "gtms.config.bak"))
			joined := strings.Join(append(append([]string{}, result.Notes...), result.Warnings...), "\n")
			assert.NotContains(t, joined, "Upgraded ")
			assert.NotContains(t, joined, "differs from the shipped version")

			common, err := commonUpgradeableAssets()
			require.NoError(t, err)
			for _, asset := range common {
				assert.FileExists(t, filepath.Join(dir, filepath.FromSlash(asset.RelPath)),
					"fresh init must scaffold %s", asset.RelPath)
				assert.NoFileExists(t, filepath.Join(dir, filepath.FromSlash(asset.RelPath))+".gtms-new")
			}
		})
	}
}

// TestBUG203_ConfigBackupNeverOverwrites covers AC9 / Owner Decision 1: the first backup
// (the user's original) survives repeated forced inits.
func TestBUG203_ConfigBackupNeverOverwrites(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)
	_, err := Init(Options{ProjectRoot: dir, Name: "cfg", Repo: "org/cfg", Preset: PresetManual})
	require.NoError(t, err)

	// Mark the user's config, then force-init: the backup must hold the marked config.
	configPath := filepath.Join(dir, "gtms.config")
	original := bug203Read(t, dir, "gtms.config") + "\n# team note: keep this line\n"
	require.NoError(t, os.WriteFile(configPath, []byte(original), 0o644))

	r1, err := Init(Options{ProjectRoot: dir, Name: "cfg", Repo: "org/cfg", Preset: PresetManual, Force: true})
	require.NoError(t, err)
	require.NotEmpty(t, r1.ConfigBackup)
	firstBackup := filepath.Join(dir, r1.ConfigBackup)
	assert.Equal(t, original, func() string { b, _ := os.ReadFile(firstBackup); return string(b) }(),
		"the first backup must hold the user's original config")
	assert.NotContains(t, bug203Read(t, dir, "gtms.config"), "# team note: keep this line",
		"the config itself was rewritten")

	// A second forced init must not replace the first backup.
	_, err = Init(Options{ProjectRoot: dir, Name: "cfg", Repo: "org/cfg", Preset: PresetManual, Force: true})
	require.NoError(t, err)
	assert.Equal(t, original, func() string { b, _ := os.ReadFile(firstBackup); return string(b) }(),
		"a second init --force must keep the first backup intact")
}

// TestBUG203_DemoForceUpgradesStaleCreateScript covers the demo half of AC1/AC3: a v0.3.x
// demo create adapter is upgraded in place under init --force --demo.
func TestBUG203_DemoForceUpgradesStaleCreateScript(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)
	_, err := Init(Options{ProjectRoot: dir, Name: "demo", Repo: "org/demo", Preset: PresetManual})
	require.NoError(t, err)
	_, err = DemoSeed(dir, false)
	require.NoError(t, err)

	// Overwrite with the stale v0.3.x demo script.
	bug203Write(t, dir, demoCreateScriptRelPath, bug203DifferingPriorOf(t, demoCreateScriptRelPath, demoCreateScript), 0o755)

	result, err := DemoSeed(dir, true)
	require.NoError(t, err)
	assert.Equal(t, demoCreateScript, bug203Read(t, dir, demoCreateScriptRelPath),
		"a stale v0.3.x demo create script must be upgraded in place under --force")
	assert.Contains(t, strings.Join(result.Notes, "\n"),
		"Upgraded "+demoCreateScriptRelPath+" to the current shipped version.")

	// A second forced reseed leaves the now-current script alone (AC5).
	result2, err := DemoSeed(dir, true)
	require.NoError(t, err)
	assert.NotContains(t, strings.Join(result2.Notes, "\n"), "Upgraded "+demoCreateScriptRelPath)
	assert.Contains(t, result2.FilesSkipped, demoCreateScriptRelPath)
}

// TestBUG203_InitForceAbortsOnSymlinkedConfig covers REV-209 R3-CODEX-001: a symlinked
// gtms.config must abort gtms init --force BEFORE any backup or rewrite, so the external
// target is neither read into a backup nor modified, and no backup is written.
func TestBUG203_InitForceAbortsOnSymlinkedConfig(t *testing.T) {
	skipIfShort(t)
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlink test (Windows symlink creation needs privilege)")
	}
	dir := initGitRepo(t)

	// An external config target outside the project, with recognisable content.
	external := t.TempDir()
	target := filepath.Join(external, "real.config")
	const targetContent = "project:\n  name: SECRET\n  repo: org/secret\n"
	require.NoError(t, os.WriteFile(target, []byte(targetContent), 0o644))

	// gtms.config is a symlink to that external target.
	configPath := filepath.Join(dir, "gtms.config")
	require.NoError(t, os.Symlink(target, configPath))

	_, err := Init(Options{ProjectRoot: dir, Name: "x", Repo: "org/x", Preset: PresetManual, Force: true})
	require.Error(t, err, "init --force must abort on a symlinked gtms.config")
	assert.Contains(t, err.Error(), "not a regular file")

	// The external target was neither read into a backup nor modified.
	after, rerr := os.ReadFile(target)
	require.NoError(t, rerr)
	assert.Equal(t, targetContent, string(after), "the external target must be unmodified")
	baks, _ := filepath.Glob(filepath.Join(dir, "gtms.config.bak*"))
	assert.Empty(t, baks, "no backup may be written when the config is a symlink")
	// gtms.config is still the symlink -- not rewritten.
	fi, lerr := os.Lstat(configPath)
	require.NoError(t, lerr)
	assert.NotZero(t, fi.Mode()&os.ModeSymlink, "gtms.config must still be the symlink (not rewritten)")
}

// TestBUG203_InitIgnoresConfigBak covers REV-209 R3-CODEX-002: the root-anchored
// gtms.config backup pattern is ignored for a fresh project and added to an existing
// project's .gitignore under init --force (even when the GTMS sentinel is already present).
func TestBUG203_InitIgnoresConfigBak(t *testing.T) {
	skipIfShort(t)
	const bakPattern = "/gtms.config.bak*"

	t.Run("fresh init ignores the backup pattern", func(t *testing.T) {
		dir := initGitRepo(t)
		_, err := Init(Options{ProjectRoot: dir, Name: "x", Repo: "org/x", Preset: PresetManual})
		require.NoError(t, err)
		gi, rerr := os.ReadFile(filepath.Join(dir, ".gitignore"))
		require.NoError(t, rerr)
		assert.Contains(t, string(gi), bakPattern, "a fresh project's .gitignore must ignore gtms.config backups")
	})

	t.Run("init --force adds the pattern to an existing sentinel-managed .gitignore", func(t *testing.T) {
		dir := initGitRepo(t)
		// A pre-existing project whose .gitignore already carries the GTMS sentinel but NOT
		// the backup pattern (as a project scaffolded before this fix would have).
		require.NoError(t, os.WriteFile(filepath.Join(dir, "gtms.config"), []byte("project:\n  name: x\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"),
			[]byte(".gtms/\ngtms/execution/attachments/\ngtms/execution/logs/\n"), 0o644))

		_, err := Init(Options{ProjectRoot: dir, Name: "x", Repo: "org/x", Preset: PresetManual, Force: true})
		require.NoError(t, err)

		gi, rerr := os.ReadFile(filepath.Join(dir, ".gitignore"))
		require.NoError(t, rerr)
		assert.Contains(t, string(gi), bakPattern,
			"init --force must ensure the backup pattern is ignored even when the sentinel is present")
		assert.Equal(t, 1, strings.Count(string(gi), bakPattern), "the pattern must not be duplicated")
	})
}
