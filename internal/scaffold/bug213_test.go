package scaffold

// BUG-213: containment of gtms init scaffold writes. These Go tests cover the
// rows BATS/Pester cannot or must not: inspection-error injection (hooks), hard
// links, the active-parent canonical refusal, and the guidance-pair rule. The
// user-facing symlink/junction behaviour is covered by the canonical specs in
// gtms/test/cases/init-scaffold-write-containment/ (BATS + Pester).

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBUG213_CreateOnlyInspectionErrorFailsClosed: an Lstat failure other than
// "not exist" on a create-only destination is returned as an error (fail
// closed, Owner Decision 1), not treated as absent -- so nothing is written.
func TestBUG213_CreateOnlyInspectionErrorFailsClosed(t *testing.T) {
	dir := t.TempDir()
	sentinel := errors.New("synthetic scaffold lstat failure")
	orig := scaffoldLstat
	scaffoldLstat = func(string) (os.FileInfo, error) { return nil, sentinel }
	defer func() { scaffoldLstat = orig }()

	outcome, warn, err := safeCreateOnly(dir, "gtms/file.txt", []byte("x"), 0o644)
	require.Error(t, err, "an Lstat inspection failure must be returned")
	assert.ErrorIs(t, err, sentinel)
	assert.Equal(t, scaffoldWriteOutcome(0), outcome)
	assert.Empty(t, warn, "an inspection error must not warn")

	_, statErr := os.Stat(filepath.Join(dir, "gtms", "file.txt"))
	assert.True(t, os.IsNotExist(statErr), "nothing may be written on an inspection error")
}

// TestBUG213_OverwriteInspectionErrorFailsClosed: a regular overwrite
// destination whose hard-link count cannot be read fails closed and leaves the
// file byte-for-byte unchanged.
func TestBUG213_OverwriteInspectionErrorFailsClosed(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".gtms"), 0o755))
	target := filepath.Join(dir, ".gtms", "guidance.yaml.bak")
	const before = "init: KEEP\n"
	require.NoError(t, os.WriteFile(target, []byte(before), 0o644))

	sentinel := errors.New("synthetic scaffold link-count failure")
	orig := scaffoldLinkCount
	scaffoldLinkCount = func(string, os.FileInfo) (uint64, error) { return 0, sentinel }
	defer func() { scaffoldLinkCount = orig }()

	_, err := safeOverwrite(dir, ".gtms/guidance.yaml.bak", []byte("new"), 0o644)
	require.Error(t, err, "an unreadable link count must fail closed")
	assert.ErrorIs(t, err, sentinel)

	after, rerr := os.ReadFile(target)
	require.NoError(t, rerr)
	assert.Equal(t, before, string(after), "the overwrite destination must be unchanged on an inspection error")
}

// TestBUG213_InitFailsClosedOnInspectionError: an inspection error reaching a
// guarded scaffold write during Init surfaces as a non-nil error (init exits
// non-zero), not a silent skip.
func TestBUG213_InitFailsClosedOnInspectionError(t *testing.T) {
	skipIfShort(t)
	dir := t.TempDir()
	gitInitAt(t, dir)

	sentinel := errors.New("synthetic scaffold lstat failure for tasks/.README.md")
	orig := scaffoldLstat
	scaffoldLstat = func(p string) (os.FileInfo, error) {
		if strings.HasSuffix(filepath.ToSlash(p), "tasks/.README.md") {
			return nil, sentinel
		}
		return os.Lstat(p)
	}
	defer func() { scaffoldLstat = orig }()

	_, err := Init(Options{ProjectRoot: dir, Name: "i", Repo: "org/i", Preset: PresetManual})
	require.Error(t, err, "init must fail closed on a scaffold-write inspection error")
	assert.ErrorIs(t, err, sentinel)
}

// TestBUG213_RefusedGitkeepReportedSkipped pins Owner Decision 1 for the
// create-only .gitkeep destinations that are otherwise silent: when a .gitkeep
// write is refused (its own leaf is non-regular, or a parent component escapes
// the project), it must be reported under FilesSkipped (so it prints beneath
// "Skipped (already exist):") and FilesRefused. A created or already-present
// .gitkeep stays silent, so the normal-path output is unchanged.
func TestBUG213_RefusedGitkeepReportedSkipped(t *testing.T) {
	t.Run("non-regular .gitkeep leaf (cross-platform)", func(t *testing.T) {
		root := t.TempDir()
		// A directory where the .gitkeep file is expected is non-regular, so the
		// create-only writer refuses it -- no symlink privilege needed.
		require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms", "adapters", ".gitkeep"), 0o755))

		result := &Result{}
		_, err := createDirectories(root, PresetManual, "gtms", result)
		require.NoError(t, err)
		assert.Contains(t, result.FilesSkipped, "gtms/adapters/.gitkeep",
			"a refused .gitkeep must be listed under Skipped (already exist)")
		assert.Contains(t, result.FilesRefused, "gtms/adapters/.gitkeep",
			"a refused .gitkeep must be recorded refused so the Created: banner omits it")
	})

	t.Run("parent folder escapes the project (POSIX)", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX symlink test (Windows symlink creation needs privilege; the junction path is Pester's)")
		}
		parent := t.TempDir()
		root := filepath.Join(parent, "project")
		outside := filepath.Join(parent, "outside")
		require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms"), 0o755))
		require.NoError(t, os.MkdirAll(outside, 0o755))
		// gtms/adapters -> ../../outside (resolves to parent/outside, outside the project).
		require.NoError(t, os.Symlink(filepath.Join("..", "..", "outside"), filepath.Join(root, "gtms", "adapters")))

		result := &Result{}
		_, err := createDirectories(root, PresetManual, "gtms", result)
		require.NoError(t, err)
		assert.Contains(t, result.FilesSkipped, "gtms/adapters/.gitkeep",
			"a .gitkeep below a linked subfolder must be reported skipped")
		assert.Contains(t, result.FilesRefused, "gtms/adapters/.gitkeep")
		assert.Contains(t, result.DirsRefused, "gtms/adapters",
			"the escaping directory must be distinguished from its refused .gitkeep leaf")

		entries, rerr := os.ReadDir(outside)
		require.NoError(t, rerr)
		assert.Empty(t, entries, "nothing may be written into the outside folder")
	})

	t.Run("normal run keeps .gitkeep silent", func(t *testing.T) {
		root := t.TempDir()
		result := &Result{}
		_, err := createDirectories(root, PresetManual, "gtms", result)
		require.NoError(t, err)
		for _, s := range result.FilesSkipped {
			assert.NotContains(t, s, ".gitkeep", "a created .gitkeep must not appear under Skipped on a normal run")
		}
		assert.Empty(t, result.FilesRefused, "a normal run refuses nothing")
	})
}

// TestBUG213_ExistingFileUnderEscapingParentRefused pins REV-252
// CLAUDE-001/CODEX-001: a create-only destination whose parent folder is linked
// outside the project is refused for containment even when the outside target
// already holds a regular file at that name -- it must not be mis-reported as an
// ordinary "already exists" skip. POSIX symlink (the junction path is Pester's).
func TestBUG213_ExistingFileUnderEscapingParentRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlink test (Windows symlink creation needs privilege; the junction path is Pester's)")
	}
	parent := t.TempDir()
	root := filepath.Join(parent, "project")
	outside := filepath.Join(parent, "outside")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms"), 0o755))
	require.NoError(t, os.MkdirAll(outside, 0o755))
	const outsideContent = "OUTSIDE-FILE-MARKER\n"
	require.NoError(t, os.WriteFile(filepath.Join(outside, "manual-execute-script.sh"), []byte(outsideContent), 0o644))
	// gtms/adapters -> ../../outside, so the destination already exists in the outside folder.
	require.NoError(t, os.Symlink(filepath.Join("..", "..", "outside"), filepath.Join(root, "gtms", "adapters")))

	destRel := "gtms/adapters/manual-execute-script.sh"

	outcome, warn, err := safeCreateOnly(root, destRel, []byte("#!/bin/sh\n"), 0o644)
	require.NoError(t, err)
	assert.Equal(t, writeRefused, outcome, "an existing file under an escaping parent must be refused, not skipped")
	assert.Contains(t, warn, "resolves outside the project")

	result := &Result{}
	require.NoError(t, guardedCreateFile(root, destRel, []byte("#!/bin/sh\n"), 0o644, result))
	assert.Contains(t, result.FilesRefused, destRel, "it must be recorded refused so the Created: banner omits it")
	assert.Contains(t, result.FilesSkipped, destRel)
	assert.NotContains(t, result.FilesCreated, destRel, "a refused destination must never be listed as created")

	after, rerr := os.ReadFile(filepath.Join(outside, "manual-execute-script.sh"))
	require.NoError(t, rerr)
	assert.Equal(t, outsideContent, string(after), "the outside file must be byte-for-byte unchanged")
}

// TestBUG213_REV255_LinkedSkillsDirectoryRecordedRefused pins the reporting
// distinction added after REV-255: the starter-skills catalog is not created by
// createDirectories, so an escaping gtms/skills link must still be recorded as
// a refused directory. Refused skill files retain their individual warnings.
func TestBUG213_REV255_LinkedSkillsDirectoryRecordedRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlink test (the Windows junction behavior is covered by Pester)")
	}

	for _, tc := range []struct {
		name       string
		linkRel    string
		targetRel  string
		refusedRel string
	}{
		{name: "catalog directory", linkRel: "gtms/skills", targetRel: "../../outside", refusedRel: "gtms/skills"},
		{name: "nested skill directory", linkRel: "gtms/skills/gtms-tests-prime", targetRel: "../../../outside", refusedRel: "gtms/skills/gtms-tests-prime"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, "project")
			outside := filepath.Join(base, "outside")
			require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, filepath.FromSlash(tc.linkRel))), 0o755))
			require.NoError(t, os.MkdirAll(outside, 0o755))
			require.NoError(t, os.Symlink(filepath.FromSlash(tc.targetRel), filepath.Join(root, filepath.FromSlash(tc.linkRel))))

			result := &Result{}
			require.NoError(t, writeExampleSkillsForParent(root, result, "gtms"))
			assert.Contains(t, result.DirsRefused, tc.refusedRel)
			assert.NotEmpty(t, result.FilesRefused, "each refused starter-skill file remains reported")

			entries, err := os.ReadDir(outside)
			require.NoError(t, err)
			assert.Empty(t, entries, "nothing may be written through the linked skills directory")
		})
	}
}

// TestBUG213_LiveVscodeLeafNonRegularRefused pins REV-252 CODEX-002: init
// classifies each VS Code leaf without following links before choosing the
// companion branch, so a live symlink (or any non-regular leaf) at
// settings.json / extensions.json is refused as non-regular with no companion
// written and no merge hint, instead of silently taking the companion branch.
func TestBUG213_LiveVscodeLeafNonRegularRefused(t *testing.T) {
	const mergeMarker = "MERGE-HINT-SHOULD-NOT-APPEAR"

	t.Run("directory where the VS Code file is expected (cross-platform)", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, ".vscode", "settings.json"), 0o755))
		result := &Result{}
		require.NoError(t, initVscodeFile(root, ".vscode/settings.json", ".vscode/gtms-settings.json.snippet",
			[]byte("{}"), mergeMarker, result))
		assert.Contains(t, result.FilesRefused, ".vscode/settings.json")
		assert.NotContains(t, result.FilesCreated, ".vscode/gtms-settings.json.snippet",
			"no companion may be written for a non-regular original")
		warns := strings.Join(result.Warnings, "\n")
		assert.Contains(t, warns, ".vscode/settings.json")
		assert.Contains(t, warns, "is not a regular file")
		assert.NotContains(t, warns, mergeMarker, "no merge hint for a refused original")
	})

	for _, name := range []string{"settings.json", "extensions.json"} {
		t.Run("live "+name+" symlink to an in-project target (POSIX)", func(t *testing.T) {
			if runtime.GOOS == "windows" {
				t.Skip("POSIX symlink test (Windows symlink creation needs privilege)")
			}
			root := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(root, ".vscode"), 0o755))
			require.NoError(t, os.MkdirAll(filepath.Join(root, "notes"), 0o755))
			const keep = "IN-PROJECT-KEEP\n"
			keepPath := filepath.Join(root, "notes", "keep.json")
			require.NoError(t, os.WriteFile(keepPath, []byte(keep), 0o644))
			require.NoError(t, os.Symlink(filepath.Join("..", "notes", "keep.json"), filepath.Join(root, ".vscode", name)))

			companionRel := ".vscode/gtms-" + name + ".snippet"
			result := &Result{}
			require.NoError(t, initVscodeFile(root, ".vscode/"+name, companionRel, []byte("{}"), mergeMarker, result))

			assert.Contains(t, result.FilesRefused, ".vscode/"+name, "a live VS Code leaf symlink must be refused")
			warns := strings.Join(result.Warnings, "\n")
			assert.Contains(t, warns, "is not a regular file")
			assert.NotContains(t, warns, mergeMarker)

			_, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(companionRel)))
			assert.True(t, os.IsNotExist(statErr), "no companion may be written for a refused original")
			after, rerr := os.ReadFile(keepPath)
			require.NoError(t, rerr)
			assert.Equal(t, keep, string(after), "the symlink target must be unchanged")
		})
	}
}

// TestBUG213_LinkedVscodeWithExistingFilesRefused pins REV-254 OWNER-001: when
// .vscode/ is linked outside the project and the outside folder already holds
// settings.json / extensions.json as regular files, each original is refused for
// containment (one warning naming it, FilesSkipped + FilesRefused, never
// Created:), with no companion written anywhere and no merge hint, and the
// outside files are byte-for-byte unchanged. POSIX symlink (junctions: Pester).
func TestBUG213_LinkedVscodeWithExistingFilesRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlink test (Windows symlink creation needs privilege; the junction path is Pester's)")
	}
	parent := t.TempDir()
	root := filepath.Join(parent, "project")
	outside := filepath.Join(parent, "outside")
	require.NoError(t, os.MkdirAll(root, 0o755))
	require.NoError(t, os.MkdirAll(outside, 0o755))
	const sContent = "OUTSIDE-SETTINGS-MARKER\n"
	const eContent = "OUTSIDE-EXTENSIONS-MARKER\n"
	require.NoError(t, os.WriteFile(filepath.Join(outside, "settings.json"), []byte(sContent), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "extensions.json"), []byte(eContent), 0o644))
	// .vscode -> ../outside: the destinations resolve to the existing outside files.
	require.NoError(t, os.Symlink(filepath.Join("..", "outside"), filepath.Join(root, ".vscode")))

	const mergeMarker = "MERGE-HINT-SHOULD-NOT-APPEAR"
	cases := []struct {
		existingRel, companionRel, outsideFile, outsideContent string
	}{
		{".vscode/settings.json", ".vscode/gtms-settings.json.snippet", "settings.json", sContent},
		{".vscode/extensions.json", ".vscode/gtms-extensions.json.snippet", "extensions.json", eContent},
	}
	for _, c := range cases {
		result := &Result{}
		require.NoError(t, initVscodeFile(root, c.existingRel, c.companionRel, []byte("{}"), mergeMarker, result))

		require.Len(t, result.Warnings, 1, "exactly one containment warning for %s", c.existingRel)
		warn := result.Warnings[0]
		assert.Contains(t, warn, c.existingRel, "the warning must name the refused original")
		assert.Contains(t, warn, "resolves outside the project")
		assert.NotContains(t, warn, mergeMarker, "no merge hint for a refused original")

		assert.Contains(t, result.FilesSkipped, c.existingRel)
		assert.Contains(t, result.FilesRefused, c.existingRel)
		assert.NotContains(t, result.FilesCreated, c.existingRel, "a refused original must never be created")
		assert.NotContains(t, result.FilesCreated, c.companionRel, "no companion for a refused original")

		// No companion written anywhere -- not in the project, not in the outside target.
		_, statErr := os.Lstat(filepath.Join(outside, filepath.Base(c.companionRel)))
		assert.True(t, os.IsNotExist(statErr), "no companion may be written into the outside folder")

		after, rerr := os.ReadFile(filepath.Join(outside, c.outsideFile))
		require.NoError(t, rerr)
		assert.Equal(t, c.outsideContent, string(after), "the outside file must be byte-for-byte unchanged")
	}
}

// TestBUG213_CanonicalParentLinkedOutsideRefused is Reproduction A: a canonical
// gtms/ that is a symbolic link resolving outside the project is refused before
// any mutation, and nothing is written into the outside folder or the project.
// POSIX symlink test; the Windows junction path is covered by Pester.
func TestBUG213_CanonicalParentLinkedOutsideRefused(t *testing.T) {
	skipIfShort(t)
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlink test (Windows symlink creation needs privilege; junctions are Pester's)")
	}
	parent := t.TempDir()
	project := filepath.Join(parent, "project")
	outside := filepath.Join(parent, "outside")
	require.NoError(t, os.MkdirAll(project, 0o755))
	require.NoError(t, os.MkdirAll(outside, 0o755))
	gitInitAt(t, project)
	require.NoError(t, os.Symlink(filepath.Join("..", "outside"), filepath.Join(project, "gtms")))

	_, err := Init(Options{ProjectRoot: project, Name: "a", Repo: "org/a", Preset: PresetManual})
	require.Error(t, err, "a canonical gtms/ linked outside the project must be refused")
	assert.Contains(t, err.Error(), "resolves outside the project")

	entries, rerr := os.ReadDir(outside)
	require.NoError(t, rerr)
	assert.Empty(t, entries, "nothing may be written into the outside folder")

	_, statErr := os.Stat(filepath.Join(project, "gtms.config"))
	assert.True(t, os.IsNotExist(statErr), "no gtms.config may be written before the refusal")
}

// TestBUG213_GuidancePairWithheldOnHardLinkedBackup covers Owner Decision 3's
// guidance-pair rule on BOTH Linux and Windows (os.Link needs no special
// Windows rights): when .gtms/guidance.yaml.bak is a hard link to a file
// outside the project, neither guidance file is written, GuidanceModified stays
// false, exactly one warning names the backup, and the hard link keeps its
// identity and link count.
func TestBUG213_GuidancePairWithheldOnHardLinkedBackup(t *testing.T) {
	skipIfShort(t)
	parent := t.TempDir()
	project := filepath.Join(parent, "project")
	outside := filepath.Join(parent, "outside")
	require.NoError(t, os.MkdirAll(project, 0o755))
	require.NoError(t, os.MkdirAll(outside, 0o755))
	gitInitAt(t, project)

	_, err := Init(Options{ProjectRoot: project, Name: "g", Repo: "org/g", Preset: PresetManual})
	require.NoError(t, err)
	_, err = DemoSeed(project, false)
	require.NoError(t, err)

	guidancePath := filepath.Join(project, ".gtms", "guidance.yaml")
	before, err := os.ReadFile(guidancePath)
	require.NoError(t, err)

	shared := filepath.Join(outside, "shared-backup.yaml")
	const sharedContent = "init: OUTSIDE-BACKUP-MARKER\n"
	require.NoError(t, os.WriteFile(shared, []byte(sharedContent), 0o644))
	bak := filepath.Join(project, ".gtms", "guidance.yaml.bak")
	_ = os.Remove(bak)
	require.NoError(t, os.Link(shared, bak), "same-filesystem hard link must succeed")

	modified, warn, err := UpdateDemoGuidance(project)
	require.NoError(t, err, "a withheld update is not an error (Owner Decision 1)")
	assert.False(t, modified, "the guidance update must be withheld")
	assert.Contains(t, warn, ".gtms/guidance.yaml.bak", "the one warning must name the unsafe backup")
	assert.Contains(t, warn, "left unchanged")

	afterShared, err := os.ReadFile(shared)
	require.NoError(t, err)
	assert.Equal(t, sharedContent, string(afterShared), "the outside file must be byte-for-byte unchanged")

	info, err := os.Lstat(bak)
	require.NoError(t, err)
	n, cerr := hardLinkCount(bak, info)
	require.NoError(t, cerr)
	assert.Equal(t, uint64(2), n, "the hard link identity must be preserved (no atomic replace)")

	after, err := os.ReadFile(guidancePath)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "guidance.yaml must not change when the update is withheld")
}

// TestBUG213_HardLinkedVscodeCompanionRefused covers Owner Decision 3 for an
// overwrite destination: a hard-linked .vscode companion snippet is refused
// without writing, the outside twin is unchanged with its link count intact,
// init exits 0, the companion is not listed as created, and the normal
// merge-the-snippet hint is suppressed.
func TestBUG213_HardLinkedVscodeCompanionRefused(t *testing.T) {
	skipIfShort(t)
	parent := t.TempDir()
	project := filepath.Join(parent, "project")
	outside := filepath.Join(parent, "outside")
	require.NoError(t, os.MkdirAll(project, 0o755))
	require.NoError(t, os.MkdirAll(outside, 0o755))
	gitInitAt(t, project)

	// First init creates .vscode/settings.json, so the next forced init takes
	// the companion-snippet path.
	_, err := Init(Options{ProjectRoot: project, Name: "v", Repo: "org/v", Preset: PresetManual})
	require.NoError(t, err)

	shared := filepath.Join(outside, "shared-settings.snippet")
	const sharedContent = "SHARED-SNIPPET-MARKER\n"
	require.NoError(t, os.WriteFile(shared, []byte(sharedContent), 0o644))
	companion := filepath.Join(project, ".vscode", "gtms-settings.json.snippet")
	_ = os.Remove(companion)
	require.NoError(t, os.Link(shared, companion), "same-filesystem hard link must succeed")

	result, err := Init(Options{ProjectRoot: project, Name: "v", Repo: "org/v", Preset: PresetManual, Force: true})
	require.NoError(t, err, "init must exit 0 on a hard-linked companion")

	afterShared, rerr := os.ReadFile(shared)
	require.NoError(t, rerr)
	assert.Equal(t, sharedContent, string(afterShared), "the outside twin must be byte-for-byte unchanged")

	info, lerr := os.Lstat(companion)
	require.NoError(t, lerr)
	n, cerr := hardLinkCount(companion, info)
	require.NoError(t, cerr)
	assert.Equal(t, uint64(2), n, "the hard link identity must be preserved")

	assert.NotContains(t, result.FilesCreated, ".vscode/gtms-settings.json.snippet",
		"a refused companion must not be listed as created")
	warns := strings.Join(result.Warnings, "\n")
	assert.Contains(t, warns, ".vscode/gtms-settings.json.snippet", "exactly one warning must name the companion")
	assert.Contains(t, warns, "left untouched")
	assert.NotContains(t, warns, "merge the snippet at .vscode/gtms-settings.json.snippet",
		"the merge-the-snippet hint must be suppressed for a refused companion")
}
