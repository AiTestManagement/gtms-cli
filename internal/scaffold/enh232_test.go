package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ENH-232: the gtms-okf-index starter skill ships as a Markdown-only navigation skill, the
// catalogue README becomes an upgradeable common asset, and the untouched v0.4.0
// gtms-tests-create body is registered as a recognised prior so DOC-071 can later change the
// shipped create skill with a working in-place upgrade route.

// TestENH232_InitScaffoldsOKFIndexSkill verifies a fresh init writes the sixth skill as a
// single SKILL.md whose name matches its folder, carries a non-empty description and the
// provenance anchor, and that the catalogue README lists it (AC: init scaffolds the skill).
func TestENH232_InitScaffoldsOKFIndexSkill(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)
	_, err := Init(Options{ProjectRoot: dir, Name: "okf", Repo: "org/okf", Preset: PresetManual})
	require.NoError(t, err)

	skillDir := filepath.Join(dir, "gtms", "skills", "gtms-okf-index")
	// The folder holds exactly one entry: SKILL.md (no script, no subdirectory).
	entries, err := os.ReadDir(skillDir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "gtms-okf-index must ship only SKILL.md")
	assert.Equal(t, "SKILL.md", entries[0].Name())
	assert.False(t, entries[0].IsDir())

	body, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	require.NoError(t, err)
	content := string(body)
	assert.Contains(t, content, "name: gtms-okf-index",
		"frontmatter name must match the folder name")
	assert.Regexp(t, `(?m)^description:\s*\S`, content,
		"frontmatter must carry a non-empty description")
	assert.Contains(t, content, "This is a GTMS starter skill",
		"skill body must carry the provenance anchor")

	readme, err := os.ReadFile(filepath.Join(dir, "gtms", "skills", "README.md"))
	require.NoError(t, err)
	assert.Contains(t, string(readme), "gtms-okf-index",
		"the catalogue README must list the new skill")

	// The five existing skills still ship.
	for _, name := range []string{
		"gtms-tests-create", "gtms-tests-automate", "gtms-tests-execute",
		"gtms-tests-prime", "gtms-tests-verify-intent",
	} {
		assert.FileExists(t, filepath.Join(dir, "gtms", "skills", name, "SKILL.md"), "missing %s", name)
	}
}

// TestENH232_SkillIsMarkdownOnly is the Go-level drift guard for Decision 2: the shipped
// gtms-okf-index skill runs no interpreter in a fenced block and references no bundled
// script. It mirrors tc-343dc1b3 so a regression is caught without the BATS suite.
func TestENH232_SkillIsMarkdownOnly(t *testing.T) {
	raw, err := skillsFS.ReadFile("skills/gtms-okf-index/SKILL.md")
	require.NoError(t, err)
	content := string(raw)

	assert.NotContains(t, strings.ToLower(content), "build_index.py",
		"the skill must not reference the tutorial's bundled script")

	forbiddenFirstWord := map[string]bool{
		"python": true, "python3": true, "py": true, "node": true, "npx": true,
		"deno": true, "bun": true, "pwsh": true, "powershell": true, "go": true,
		"ruby": true, "perl": true,
	}
	inFence := false
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if !inFence {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		assert.False(t, forbiddenFirstWord[strings.ToLower(fields[0])],
			"fenced line starts with a forbidden interpreter word: %q", line)
		for _, w := range fields {
			lw := strings.ToLower(w)
			for _, ext := range []string{".py", ".js", ".ps1", ".rb"} {
				assert.False(t, strings.HasSuffix(lw, ext),
					"fenced line references a script file %q (ext %s)", w, ext)
			}
		}
	}
}

// TestENH232_CreateSkillV040PriorVersion covers Decision 1. The untouched v0.4.0 create skill
// body is registered as a recognised prior alongside v0.3.3. DOC-071 changed the shipped
// skill, so the real shipped body upgrades a pristine v0.4.0 copy (LF or CRLF) in place and
// keeps an edited v0.4.0 copy with a companion.
func TestENH232_CreateSkillV040PriorVersion(t *testing.T) {
	skipIfShort(t)

	liveSkill, err := skillsFS.ReadFile("skills/gtms-tests-create/SKILL.md")
	require.NoError(t, err)
	live := string(liveSkill)
	// The untouched v0.4.0 body is the embedded LF-pinned snapshot, not the live body:
	// DOC-071 changed the shipped create skill, so the snapshot is now the upgrade source.
	v040 := priorVersionFile("gtms-tests-create-SKILL-v040.md")
	require.False(t, equalIgnoringEOL(v040, live),
		"DOC-071 changed the shipped create skill; the v0.4.0 snapshot must differ from it")
	require.Zero(t, strings.Count(v040, "\r"), "the v0.4.0 snapshot must stay LF-pinned")

	priors := PresetAssetPriorVersions[skillCreateRelPath]
	require.Len(t, priors, 2, "the create skill must declare the v0.3.3 and v0.4.0 priors")
	// Both the v0.3.3 and v0.4.0 bodies must be registered, and neither equals the live body.
	var hasV040, hasV033 bool
	for _, p := range priors {
		assert.False(t, equalIgnoringEOL(p, live), "no registered prior may equal the live body")
		if equalIgnoringEOL(p, v040) {
			hasV040 = true
		} else {
			hasV033 = true
		}
	}
	assert.True(t, hasV040, "the untouched v0.4.0 create-skill body must be a registered prior")
	assert.True(t, hasV033, "the v0.3.3 prior must remain registered")

	// The real shipped asset, exactly as gtms init --force installs it.
	var standIn PresetAsset
	common, err := commonUpgradeableAssets()
	require.NoError(t, err)
	for _, a := range common {
		if a.RelPath == skillCreateRelPath {
			standIn = a
		}
	}
	require.Equal(t, live, standIn.Content, "the catalogue must install the live create skill")

	t.Run("pristine v0.4.0 upgrades in place", func(t *testing.T) {
		dir := initGitRepo(t)
		bug203Write(t, dir, standIn.RelPath, v040, standIn.Perm)
		result := &Result{}
		require.NoError(t, applyUpgradeableAsset(dir, standIn, true, result))
		assert.Equal(t, standIn.Content, bug203Read(t, dir, standIn.RelPath),
			"a pristine v0.4.0 copy must upgrade to the new shipped body")
		assert.Contains(t, strings.Join(result.Notes, "\n"),
			"Upgraded "+standIn.RelPath+" to the current shipped version.")
		assert.NoFileExists(t, filepath.Join(dir, filepath.FromSlash(standIn.RelPath))+".gtms-new")
	})

	t.Run("pristine CRLF v0.4.0 upgrades in place with LF", func(t *testing.T) {
		dir := initGitRepo(t)
		bug203Write(t, dir, standIn.RelPath, strings.ReplaceAll(v040, "\n", "\r\n"), standIn.Perm)
		result := &Result{}
		require.NoError(t, applyUpgradeableAsset(dir, standIn, true, result))
		got := bug203Read(t, dir, standIn.RelPath)
		assert.Equal(t, standIn.Content, got, "a CRLF v0.4.0 copy must upgrade to the new shipped body")
		assert.Zero(t, strings.Count(got, "\r"), "the upgraded skill must have LF line endings")
		assert.NoFileExists(t, filepath.Join(dir, filepath.FromSlash(standIn.RelPath))+".gtms-new")
	})

	t.Run("edited v0.4.0 kept with companion", func(t *testing.T) {
		dir := initGitRepo(t)
		edited := v040 + "\n# team customisation: keep this line\n"
		bug203Write(t, dir, standIn.RelPath, edited, standIn.Perm)
		result := &Result{}
		require.NoError(t, applyUpgradeableAsset(dir, standIn, true, result))
		assert.Equal(t, edited, bug203Read(t, dir, standIn.RelPath),
			"an edited v0.4.0 copy must never be clobbered")
		companion := filepath.Join(dir, filepath.FromSlash(standIn.RelPath)) + ".gtms-new"
		cb, rerr := os.ReadFile(companion)
		require.NoError(t, rerr)
		assert.Equal(t, standIn.Content, string(cb), "the companion carries the new shipped body")
		assert.Contains(t, strings.Join(result.Warnings, "\n"),
			standIn.RelPath+" differs from the shipped version and was left untouched.")
	})
}

// TestENH232_InitForceUpgradesPristineReadme covers Decision 9 end-to-end: with a pristine
// five-skill README and the new skill removed, gtms init --force adds the sixth skill and
// upgrades the README in place; an installed-agent copy and edited project skills are
// untouched; an edited README is kept with a companion.
func TestENH232_InitForceUpgradesPristineReadme(t *testing.T) {
	skipIfShort(t)

	fiveSkillReadme := PresetAssetPriorVersions[skillCatalogueReadmeRelPath]
	require.NotEmpty(t, fiveSkillReadme, "the pristine five-skill README must be a registered prior")
	pristine := fiveSkillReadme[0]
	assert.NotContains(t, pristine, "gtms-okf-index", "the pristine README predates the sixth skill")

	t.Run("pristine README upgraded in place", func(t *testing.T) {
		dir := initGitRepo(t)
		_, err := Init(Options{ProjectRoot: dir, Name: "r", Repo: "org/r", Preset: PresetManual})
		require.NoError(t, err)

		// Simulate a pristine five-skill catalogue: remove the new skill, restore the old README.
		require.NoError(t, os.RemoveAll(filepath.Join(dir, "gtms", "skills", "gtms-okf-index")))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "gtms", "skills", "README.md"), []byte(pristine), 0o644))

		result, err := Init(Options{ProjectRoot: dir, Name: "r", Repo: "org/r", Preset: PresetManual, Force: true})
		require.NoError(t, err)

		assert.FileExists(t, filepath.Join(dir, "gtms", "skills", "gtms-okf-index", "SKILL.md"),
			"the sixth skill must be added by the forced refresh")
		readme := bug203Read(t, dir, skillCatalogueReadmeRelPath)
		assert.Contains(t, readme, "gtms-okf-index", "the pristine README must be upgraded in place")
		assert.Contains(t, strings.Join(result.Notes, "\n"),
			"Upgraded "+skillCatalogueReadmeRelPath+" to the current shipped version.")
		assert.NoFileExists(t, filepath.Join(dir, "gtms", "skills", "README.md.gtms-new"))
	})

	t.Run("edited README kept with one companion", func(t *testing.T) {
		dir := initGitRepo(t)
		_, err := Init(Options{ProjectRoot: dir, Name: "r", Repo: "org/r", Preset: PresetManual})
		require.NoError(t, err)

		require.NoError(t, os.RemoveAll(filepath.Join(dir, "gtms", "skills", "gtms-okf-index")))
		edited := pristine + "\n# team customisation: keep this line\n"
		require.NoError(t, os.WriteFile(filepath.Join(dir, "gtms", "skills", "README.md"), []byte(edited), 0o644))

		_, err = Init(Options{ProjectRoot: dir, Name: "r", Repo: "org/r", Preset: PresetManual, Force: true})
		require.NoError(t, err)

		assert.Equal(t, edited, bug203Read(t, dir, skillCatalogueReadmeRelPath),
			"an edited README must never be overwritten")
		companion := bug203Read(t, dir, skillCatalogueReadmeRelPath+".gtms-new")
		assert.Contains(t, companion, "gtms-okf-index", "the companion carries the current six-skill README")
		assert.FileExists(t, filepath.Join(dir, "gtms", "skills", "gtms-okf-index", "SKILL.md"),
			"the sixth skill is still added even when the README is edited")

		// A second forced run must not multiply companions.
		_, err = Init(Options{ProjectRoot: dir, Name: "r", Repo: "org/r", Preset: PresetManual, Force: true})
		require.NoError(t, err)
		assert.NoFileExists(t, filepath.Join(dir, "gtms", "skills", "README.md.gtms-new.2"),
			"a matching companion must be reused, not multiplied")
	})
}
