package layoutmigration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMigrate_PromptsAndGitkeepSurvive verifies that files skipped by step 4
// (prompts/, hidden files like .gitkeep) are NOT deleted by step 5's
// removeEmptyDirTree. This is the regression test for the data-loss bug
// where removeEmptyDirTree used os.RemoveAll unconditionally.
func TestMigrate_PromptsAndGitkeepSurvive(t *testing.T) {
	root := t.TempDir()

	// Create legacy gtms/cases/ tree with:
	// - prompts/create-standard.md (real tracked content, skipped by step 4)
	// - .gitkeep (hidden file, skipped by step 4)
	// - a user TC folder (moved by step 4)
	legacyDir := filepath.Join(root, "gtms", "cases")
	require.NoError(t, os.MkdirAll(filepath.Join(legacyDir, "prompts"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(legacyDir, "my-feature"), 0o755))

	promptFile := filepath.Join(legacyDir, "prompts", "create-standard.md")
	require.NoError(t, os.WriteFile(promptFile, []byte("prompt template content"), 0o644))

	gitkeepFile := filepath.Join(legacyDir, ".gitkeep")
	require.NoError(t, os.WriteFile(gitkeepFile, []byte(""), 0o644))

	tcFile := filepath.Join(legacyDir, "my-feature", "tc-abcd1234.md")
	require.NoError(t, os.WriteFile(tcFile, []byte("test case content"), 0o644))

	// Run migration
	require.NoError(t, Migrate(root))

	// prompts/create-standard.md must survive
	assert.FileExists(t, promptFile, "prompts/create-standard.md must survive migration")

	data, err := os.ReadFile(promptFile)
	require.NoError(t, err)
	assert.Equal(t, "prompt template content", string(data))

	// .gitkeep must survive
	assert.FileExists(t, gitkeepFile, ".gitkeep must survive migration")

	// User TC folder must have moved to the new location
	newTCFile := filepath.Join(root, "gtms", "test", "cases", "my-feature", "tc-abcd1234.md")
	assert.FileExists(t, newTCFile, "user TC must move to gtms/test/cases/")

	data, err = os.ReadFile(newTCFile)
	require.NoError(t, err)
	assert.Equal(t, "test case content", string(data))

	// The legacy my-feature/ should no longer exist at the old location
	assert.NoDirExists(t, filepath.Join(legacyDir, "my-feature"))

	// The legacy dir itself should still exist (prompts/ and .gitkeep remain)
	assert.DirExists(t, legacyDir, "legacy dir must survive when it contains skipped content")
}

// TestMigrate_EmptyLegacyDirRemovedCleanly verifies that when all content
// is moved and no skipped entries remain, the legacy directory tree is
// fully removed.
func TestMigrate_EmptyLegacyDirRemovedCleanly(t *testing.T) {
	root := t.TempDir()

	legacyDir := filepath.Join(root, "gtms", "cases")
	require.NoError(t, os.MkdirAll(filepath.Join(legacyDir, "templates"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(legacyDir, "guides"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(legacyDir, "my-feature"), 0o755))

	require.NoError(t, os.WriteFile(filepath.Join(legacyDir, "templates", "t.md"), []byte("t"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(legacyDir, "guides", "g.md"), []byte("g"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(legacyDir, "my-feature", "tc.md"), []byte("tc"), 0o644))

	require.NoError(t, Migrate(root))

	// Legacy dir should be fully removed (no skipped content remained)
	assert.NoDirExists(t, legacyDir, "empty legacy dir should be removed")
}

// TestMigrate_TemplatesAndGuidesMove verifies step 2 and step 3 move
// templates/ and guides/ to their new locations.
func TestMigrate_TemplatesAndGuidesMove(t *testing.T) {
	root := t.TempDir()

	legacyDir := filepath.Join(root, "gtms", "cases")
	require.NoError(t, os.MkdirAll(filepath.Join(legacyDir, "templates"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(legacyDir, "guides"), 0o755))

	require.NoError(t, os.WriteFile(
		filepath.Join(legacyDir, "templates", "manual-testcase.template.md"),
		[]byte("manual template"), 0o644))
	require.NoError(t, os.WriteFile(
		filepath.Join(legacyDir, "guides", "guide.md"),
		[]byte("guide content"), 0o644))

	require.NoError(t, Migrate(root))

	// Templates moved
	newTemplate := filepath.Join(root, "gtms", "test", "templates", "manual-testcase.template.md")
	assert.FileExists(t, newTemplate)
	data, err := os.ReadFile(newTemplate)
	require.NoError(t, err)
	assert.Equal(t, "manual template", string(data))

	// Guides moved
	newGuide := filepath.Join(root, "gtms", "test", "guides", "guide.md")
	assert.FileExists(t, newGuide)
	data, err = os.ReadFile(newGuide)
	require.NoError(t, err)
	assert.Equal(t, "guide content", string(data))
}

// TestNeedsLegacyMigration verifies the detection logic.
func TestNeedsLegacyMigration(t *testing.T) {
	root := t.TempDir()

	// No legacy dir -> false
	assert.False(t, NeedsLegacyMigration(root))

	// Legacy dir exists, no new dir -> true
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms", "cases"), 0o755))
	assert.True(t, NeedsLegacyMigration(root))

	// Both exist -> false (already migrated)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms", "test", "cases"), 0o755))
	assert.False(t, NeedsLegacyMigration(root))
}

// TestCheckGuideDir verifies the three-way safety check.
func TestCheckGuideDir(t *testing.T) {
	// Case 1: unset
	assert.NoError(t, CheckGuideDir(""))

	// Case 1: known default
	assert.NoError(t, CheckGuideDir("gtms/cases/guides"))
	assert.NoError(t, CheckGuideDir("gtms/cases/guides/"))

	// Case 2: customised inside legacy tree
	err := CheckGuideDir("gtms/cases/my-guides")
	assert.Error(t, err)
	var migErr *MigrationError
	assert.ErrorAs(t, err, &migErr)
	assert.Contains(t, migErr.Message, "gtms/cases/my-guides")

	// Case 3: customised outside legacy tree
	assert.NoError(t, CheckGuideDir("docs/guides"))
	assert.NoError(t, CheckGuideDir("my-guides"))
}

// TestCheckGuideDir_BackslashPathsClassifyAsCase2 verifies that Windows-style
// backslash separators are normalised before the slash-form prefix check.
// Without filepath.ToSlash normalisation, a config carrying "gtms\cases\my-guides"
// would slip past the Case 2 prefix and be misclassified as Case 3, defeating
// the safety rail on Windows hosts.
func TestCheckGuideDir_BackslashPathsClassifyAsCase2(t *testing.T) {
	err := CheckGuideDir(`gtms\cases\my-guides`)
	assert.Error(t, err)
	var migErr *MigrationError
	assert.ErrorAs(t, err, &migErr)
	// The error message echoes the raw input so the user can find it in their config.
	assert.Contains(t, migErr.Message, `gtms\cases\my-guides`)
}

// TestCheckGuideDir_BackslashKnownDefaultClassifiesAsCase1 verifies that the
// known-default legacy path written with backslashes is still recognised as
// Case 1 (clean rewrite), not misclassified as a customised Case 2 entry.
func TestCheckGuideDir_BackslashKnownDefaultClassifiesAsCase1(t *testing.T) {
	assert.NoError(t, CheckGuideDir(`gtms\cases\guides`))
	assert.True(t, IsCase1GuideDir(`gtms\cases\guides`))
}

// TestFirstCase2Offender_MultiAdapterMixedHaltsOnCase2 verifies that a mixed
// fixture with Case 1 (known default) + Case 2 (custom inside legacy) entries
// returns the Case 2 entry as the offender, with the underlying MigrationError
// for the caller to wrap.
func TestFirstCase2Offender_MultiAdapterMixedHaltsOnCase2(t *testing.T) {
	entries := []GuideDirEntry{
		{Command: "create", Adapter: "local-claude", Value: "gtms/cases/guides"},
		{Command: "automate", Adapter: "bats", Value: "gtms/cases/my-custom"},
		{Command: "execute", Adapter: "local-runner", Value: "docs/guides"},
	}

	offender, migErr := FirstCase2Offender(entries)
	require.NotNil(t, offender, "expected Case 2 entry to be returned")
	require.NotNil(t, migErr, "expected MigrationError to be returned")

	assert.Equal(t, "automate", offender.Command)
	assert.Equal(t, "bats", offender.Adapter)
	assert.Equal(t, "gtms/cases/my-custom", offender.Value)
	assert.Contains(t, migErr.Message, "gtms/cases/my-custom")
}

// TestFirstCase2Offender_AllCase1ReturnsNil verifies that a fixture where every
// entry is Case 1 (unset or known default) returns nil/nil so the caller knows
// migration is safe to proceed.
func TestFirstCase2Offender_AllCase1ReturnsNil(t *testing.T) {
	entries := []GuideDirEntry{
		{Command: "create", Adapter: "local-claude", Value: "gtms/cases/guides"},
		{Command: "create", Adapter: "github-create", Value: "gtms/cases/guides/"},
		{Command: "automate", Adapter: "bats", Value: ""},
	}

	offender, migErr := FirstCase2Offender(entries)
	assert.Nil(t, offender)
	assert.Nil(t, migErr)
}

// TestFirstCase2Offender_AllCase3ReturnsNil verifies that customised-outside
// entries do not trigger a halt -- they are out of scope for the legacy
// migration and should be left alone.
func TestFirstCase2Offender_AllCase3ReturnsNil(t *testing.T) {
	entries := []GuideDirEntry{
		{Command: "create", Adapter: "local-claude", Value: "docs/guides"},
		{Command: "automate", Adapter: "bats", Value: "shared/templates"},
	}

	offender, migErr := FirstCase2Offender(entries)
	assert.Nil(t, offender)
	assert.Nil(t, migErr)
}

// TestFirstCase2Offender_EmptyEntriesReturnsNil verifies the empty-slice
// degenerate case so callers can pass an empty slice safely.
func TestFirstCase2Offender_EmptyEntriesReturnsNil(t *testing.T) {
	offender, migErr := FirstCase2Offender(nil)
	assert.Nil(t, offender)
	assert.Nil(t, migErr)
}

// TestRemoveEmptyDirTree_LeavesNonEmptyDirs verifies the bottom-up
// empty-dir removal directly.
func TestRemoveEmptyDirTree_LeavesNonEmptyDirs(t *testing.T) {
	root := t.TempDir()

	dir := filepath.Join(root, "parent", "child")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	// Put a file in the child
	require.NoError(t, os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("keep"), 0o644))

	removeEmptyDirTree(filepath.Join(root, "parent"))

	// parent/ and child/ should both survive because child/ has a file
	assert.DirExists(t, filepath.Join(root, "parent"))
	assert.DirExists(t, dir)
	assert.FileExists(t, filepath.Join(dir, "keep.txt"))
}

// TestRemoveEmptyDirTree_RemovesEmptyTree verifies that a fully empty
// tree is removed.
func TestRemoveEmptyDirTree_RemovesEmptyTree(t *testing.T) {
	root := t.TempDir()

	dir := filepath.Join(root, "parent", "child", "grandchild")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	removeEmptyDirTree(filepath.Join(root, "parent"))

	assert.NoDirExists(t, filepath.Join(root, "parent"))
}

// halfMigratedConfig is the gtms.config shape produced by ENH-164's runtime
// shim after migrating a v0.2.0 install: guide-dir already repointed to the
// new gtms/test/guides slot but prompt-template still on the legacy path
// because ENH-164 intentionally deferred the prompts move to ENH-165.
const halfMigratedConfig = `project:
    name: test-project
    repo: test/test-project
adapters:
    create:
        local-claude:
            mode: sync
            command: claude -p {prompt}
            prompt-template: gtms/cases/prompts/create-standard.md
            guide-dir: gtms/test/guides
`

// TestNeedsPromptsMigration covers the half-migrated and clean-state cases.
func TestNeedsPromptsMigration(t *testing.T) {
	t.Run("half-migrated install needs migration", func(t *testing.T) {
		root := t.TempDir()
		legacyPath := filepath.Join(root, LegacyPromptTemplatePath)
		require.NoError(t, os.MkdirAll(filepath.Dir(legacyPath), 0o755))
		require.NoError(t, os.WriteFile(legacyPath, []byte("template"), 0o644))

		assert.True(t, NeedsPromptsMigration(root))
	})

	t.Run("post-ENH-165 install does not need migration", func(t *testing.T) {
		root := t.TempDir()
		newPath := filepath.Join(root, NewPromptTemplatePath)
		require.NoError(t, os.MkdirAll(filepath.Dir(newPath), 0o755))
		require.NoError(t, os.WriteFile(newPath, []byte("template"), 0o644))

		assert.False(t, NeedsPromptsMigration(root))
	})

	t.Run("project without any prompts dir does not need migration", func(t *testing.T) {
		root := t.TempDir()
		assert.False(t, NeedsPromptsMigration(root))
	})
}

// TestMigratePrompts_HalfMigratedClean exercises the ENH-164-half-migrated
// shape: gtms/test/cases/ exists, gtms/cases/prompts/create-standard.md +
// .gitkeep artefacts still in place. Expected outcome: clean migration,
// gtms/cases/ fully removed, prompt-template literal rewritten in
// gtms.config, no warning surfaced.
func TestMigratePrompts_HalfMigratedClean(t *testing.T) {
	root := t.TempDir()

	// Half-migrated state: gtms/test/cases/ already present.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms", "test", "cases"), 0o755))

	// Legacy gtms/cases/prompts/create-standard.md + scaffold .gitkeep
	// artefacts at both levels.
	legacyPromptsDir := filepath.Join(root, "gtms", "cases", "prompts")
	require.NoError(t, os.MkdirAll(legacyPromptsDir, 0o755))
	legacyTemplate := filepath.Join(legacyPromptsDir, "create-standard.md")
	require.NoError(t, os.WriteFile(legacyTemplate, []byte("prompt template content"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(legacyPromptsDir, ".gitkeep"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "gtms", "cases", ".gitkeep"), nil, 0o644))

	// gtms.config carrying the legacy prompt-template literal.
	configPath := filepath.Join(root, "gtms.config")
	require.NoError(t, os.WriteFile(configPath, []byte(halfMigratedConfig), 0o644))

	warning, err := MigratePrompts(root)
	require.NoError(t, err)
	assert.Empty(t, warning, "clean half-migrated tree should produce no warning")

	// Prompt template moved to new location.
	newTemplate := filepath.Join(root, "gtms", "test", "prompts", "create-standard.md")
	assert.FileExists(t, newTemplate)
	data, err := os.ReadFile(newTemplate)
	require.NoError(t, err)
	assert.Equal(t, "prompt template content", string(data))

	// Legacy tree fully removed (gitkeep artefacts cleaned up).
	assert.NoFileExists(t, legacyTemplate)
	assert.NoDirExists(t, filepath.Join(root, "gtms", "cases"))

	// Config rewritten.
	configData, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Contains(t, string(configData), "prompt-template: gtms/test/prompts/create-standard.md")
	assert.NotContains(t, string(configData), "gtms/cases/prompts")
}

// TestMigratePrompts_ComposesWithMigrate exercises the pre-ENH-164 v0.2.0-shape
// install: full legacy gtms/cases/ tree intact, no gtms/test/ tree present.
// Expected outcome: Migrate() then MigratePrompts() in sequence produce a
// clean post-ENH-165 final state in one shim invocation.
func TestMigratePrompts_ComposesWithMigrate(t *testing.T) {
	root := t.TempDir()

	legacyDir := filepath.Join(root, "gtms", "cases")
	require.NoError(t, os.MkdirAll(filepath.Join(legacyDir, "templates"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(legacyDir, "guides"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(legacyDir, "prompts"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(legacyDir, "my-feature"), 0o755))

	require.NoError(t, os.WriteFile(filepath.Join(legacyDir, "templates", "t.md"), []byte("t"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(legacyDir, "guides", "g.md"), []byte("g"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(legacyDir, "prompts", "create-standard.md"), []byte("prompt"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(legacyDir, "prompts", ".gitkeep"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(legacyDir, ".gitkeep"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(legacyDir, "my-feature", "tc.md"), []byte("tc"), 0o644))

	// gtms.config with legacy paths for both guide-dir and prompt-template.
	configPath := filepath.Join(root, "gtms.config")
	v020Config := `adapters:
    create:
        local-claude:
            mode: sync
            command: claude -p {prompt}
            prompt-template: gtms/cases/prompts/create-standard.md
            guide-dir: gtms/cases/guides
`
	require.NoError(t, os.WriteFile(configPath, []byte(v020Config), 0o644))

	// Sequence the two migrations as root.go does.
	require.NoError(t, Migrate(root))
	// Caller rewrites guide-dir post-Migrate (matches root.go's IsCase1GuideDir loop).
	require.NoError(t, RewriteGuideDirInConfig(root, "gtms/cases/guides", NewDefaultGuideDir))
	warning, err := MigratePrompts(root)
	require.NoError(t, err)
	assert.Empty(t, warning, "clean v0.2.0 migration should produce no warning")

	// Final state: gtms/cases/ gone; everything under gtms/test/.
	assert.NoDirExists(t, legacyDir)
	assert.FileExists(t, filepath.Join(root, "gtms", "test", "templates", "t.md"))
	assert.FileExists(t, filepath.Join(root, "gtms", "test", "guides", "g.md"))
	assert.FileExists(t, filepath.Join(root, "gtms", "test", "prompts", "create-standard.md"))
	assert.FileExists(t, filepath.Join(root, "gtms", "test", "cases", "my-feature", "tc.md"))

	// Config rewritten on both axes.
	configData, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Contains(t, string(configData), "prompt-template: gtms/test/prompts/create-standard.md")
	assert.Contains(t, string(configData), "guide-dir: gtms/test/guides")
	assert.NotContains(t, string(configData), "gtms/cases")
}

// TestMigratePrompts_UserContentTriggersWarning exercises the case where
// gtms/cases/ retains content the migration does not understand (e.g. the
// user dropped a notes file in there). Expected outcome: prompt template
// moved, config rewritten, but gtms/cases/ retained with the user file and
// a specific warning string returned naming the directory and the remediation.
func TestMigratePrompts_UserContentTriggersWarning(t *testing.T) {
	root := t.TempDir()

	// Half-migrated state with extra user content the migration won't touch.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms", "test", "cases"), 0o755))

	legacyDir := filepath.Join(root, "gtms", "cases")
	legacyPromptsDir := filepath.Join(legacyDir, "prompts")
	require.NoError(t, os.MkdirAll(legacyPromptsDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(legacyPromptsDir, "create-standard.md"), []byte("p"), 0o644))

	// Unrelated user content at the cases/ level.
	userNotes := filepath.Join(legacyDir, "my-notes.md")
	require.NoError(t, os.WriteFile(userNotes, []byte("my migration notes"), 0o644))

	configPath := filepath.Join(root, "gtms.config")
	require.NoError(t, os.WriteFile(configPath, []byte(halfMigratedConfig), 0o644))

	warning, err := MigratePrompts(root)
	require.NoError(t, err)

	// Warning surfaced and names the directory + remediation guidance.
	assert.Contains(t, warning, "gtms/cases")
	assert.Contains(t, warning, "Review and remove or move them manually")

	// Prompt template did move.
	assert.FileExists(t, filepath.Join(root, "gtms", "test", "prompts", "create-standard.md"))

	// Config was rewritten regardless.
	configData, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Contains(t, string(configData), "prompt-template: gtms/test/prompts/create-standard.md")

	// User content preserved at the legacy path.
	assert.DirExists(t, legacyDir)
	assert.FileExists(t, userNotes)
	notesData, err := os.ReadFile(userNotes)
	require.NoError(t, err)
	assert.Equal(t, "my migration notes", string(notesData))
}

// TestMigratePrompts_NoOpWhenAlreadyMigrated covers the post-ENH-165 case
// where MigratePrompts is invoked but there is nothing to do.
func TestMigratePrompts_NoOpWhenAlreadyMigrated(t *testing.T) {
	root := t.TempDir()

	// Only the new path exists; no legacy state.
	newPath := filepath.Join(root, NewPromptTemplatePath)
	require.NoError(t, os.MkdirAll(filepath.Dir(newPath), 0o755))
	require.NoError(t, os.WriteFile(newPath, []byte("template"), 0o644))

	warning, err := MigratePrompts(root)
	require.NoError(t, err)
	assert.Empty(t, warning)
}

// TestMigratePrompts_CollisionIdenticalContent covers the case where both
// the legacy and new create-standard.md exist with identical content.
// Expected outcome: the legacy copy is removed (it is redundant), the new
// file is left untouched, and the migration completes without warning.
// Without explicit collision handling, os.Rename would overwrite the new
// file silently on Unix and fail with a generic error on Windows.
func TestMigratePrompts_CollisionIdenticalContent(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms", "test", "cases"), 0o755))

	identical := []byte("identical prompt template content")

	legacyPath := filepath.Join(root, LegacyPromptTemplatePath)
	require.NoError(t, os.MkdirAll(filepath.Dir(legacyPath), 0o755))
	require.NoError(t, os.WriteFile(legacyPath, identical, 0o644))

	newPath := filepath.Join(root, NewPromptTemplatePath)
	require.NoError(t, os.MkdirAll(filepath.Dir(newPath), 0o755))
	require.NoError(t, os.WriteFile(newPath, identical, 0o644))

	configPath := filepath.Join(root, "gtms.config")
	require.NoError(t, os.WriteFile(configPath, []byte(halfMigratedConfig), 0o644))

	warning, err := MigratePrompts(root)
	require.NoError(t, err)
	assert.Empty(t, warning, "identical-content collision should be silent (legacy removed, new untouched)")

	// New file untouched and still contains the original bytes.
	data, err := os.ReadFile(newPath)
	require.NoError(t, err)
	assert.Equal(t, identical, data)

	// Legacy copy removed; legacy dir tree cleaned up.
	assert.NoFileExists(t, legacyPath)
	assert.NoDirExists(t, filepath.Join(root, "gtms", "cases"))
}

// TestMigratePrompts_CollisionDifferentContent covers the case where both
// the legacy and new create-standard.md exist with divergent content.
// Expected outcome: migration halts with a specific error naming both paths
// and asks the user to reconcile manually; neither file is touched, the
// legacy directory tree is preserved as-is.
func TestMigratePrompts_CollisionDifferentContent(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms", "test", "cases"), 0o755))

	legacyBytes := []byte("legacy customised content")
	newBytes := []byte("new customised content")

	legacyPath := filepath.Join(root, LegacyPromptTemplatePath)
	require.NoError(t, os.MkdirAll(filepath.Dir(legacyPath), 0o755))
	require.NoError(t, os.WriteFile(legacyPath, legacyBytes, 0o644))

	newPath := filepath.Join(root, NewPromptTemplatePath)
	require.NoError(t, os.MkdirAll(filepath.Dir(newPath), 0o755))
	require.NoError(t, os.WriteFile(newPath, newBytes, 0o644))

	configPath := filepath.Join(root, "gtms.config")
	require.NoError(t, os.WriteFile(configPath, []byte(halfMigratedConfig), 0o644))

	warning, err := MigratePrompts(root)
	require.Error(t, err, "divergent-content collision must halt")
	assert.Empty(t, warning)
	assert.Contains(t, err.Error(), LegacyPromptTemplatePath)
	assert.Contains(t, err.Error(), NewPromptTemplatePath)
	assert.Contains(t, err.Error(), "Reconcile manually")

	// Neither file was touched.
	legacyData, err := os.ReadFile(legacyPath)
	require.NoError(t, err)
	assert.Equal(t, legacyBytes, legacyData, "legacy file must not be modified on halt")

	newData, err := os.ReadFile(newPath)
	require.NoError(t, err)
	assert.Equal(t, newBytes, newData, "new file must not be modified on halt")

	// Legacy directory tree preserved.
	assert.DirExists(t, filepath.Join(root, "gtms", "cases", "prompts"))
}

// TestHasRenamedParentLegacy covers the ENH-098 + legacy-migration interaction:
// a v0.2.0-shape install whose parent dir was renamed must be detected so the
// caller can halt rather than silently no-op the migration.
func TestHasRenamedParentLegacy(t *testing.T) {
	t.Run("canonical gtms parent returns false", func(t *testing.T) {
		root := t.TempDir()
		// Even with gtms/cases/ present, the canonical parent name takes
		// the normal NeedsLegacyMigration path -- not this helper's concern.
		require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms", "cases"), 0o755))
		assert.False(t, HasRenamedParentLegacy(root, "gtms"))
	})

	t.Run("empty parent name returns false", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms", "cases"), 0o755))
		assert.False(t, HasRenamedParentLegacy(root, ""))
	})

	t.Run("renamed parent with legacy cases returns true", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, "testing", "cases"), 0o755))
		assert.True(t, HasRenamedParentLegacy(root, "testing"))
	})

	t.Run("renamed parent without legacy cases returns false", func(t *testing.T) {
		root := t.TempDir()
		// Renamed parent exists but only has the post-ENH-164 layout.
		require.NoError(t, os.MkdirAll(filepath.Join(root, "testing", "test", "cases"), 0o755))
		assert.False(t, HasRenamedParentLegacy(root, "testing"))
	})

	t.Run("renamed parent with legacy cases as a file (not dir) returns false", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, "testing"), 0o755))
		// Pathological: a file named cases instead of a directory.
		require.NoError(t, os.WriteFile(filepath.Join(root, "testing", "cases"), []byte("not a dir"), 0o644))
		assert.False(t, HasRenamedParentLegacy(root, "testing"))
	})
}

// TestRewritePromptTemplateInConfig_BackslashForm verifies that a Windows-
// authored gtms.config carrying the prompt-template in backslash form is
// rewritten to the canonical forward-slash new path. Without the explicit
// backslash pass, the shim would move create-standard.md successfully but
// leave the config pointing at gtms\cases\prompts\... (now missing).
func TestRewritePromptTemplateInConfig_BackslashForm(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "gtms.config")

	backslashConfig := `project:
    name: test-project
adapters:
    create:
        local-claude:
            prompt-template: gtms\cases\prompts\create-standard.md
            guide-dir: gtms/test/guides
`
	require.NoError(t, os.WriteFile(configPath, []byte(backslashConfig), 0o644))

	err := RewritePromptTemplateInConfig(root, LegacyPromptTemplatePath, NewPromptTemplatePath)
	require.NoError(t, err)

	data, err := os.ReadFile(configPath)
	require.NoError(t, err)

	assert.Contains(t, string(data), "prompt-template: gtms/test/prompts/create-standard.md")
	assert.NotContains(t, string(data), `gtms\cases`,
		"backslash form must be replaced; otherwise adapter resolves to missing legacy path")
}

// TestRewritePromptTemplateInConfig_ForwardSlashStillWorks guards against a
// regression where adding the backslash pass somehow breaks the canonical
// forward-slash path.
func TestRewritePromptTemplateInConfig_ForwardSlashStillWorks(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "gtms.config")
	require.NoError(t, os.WriteFile(configPath, []byte(halfMigratedConfig), 0o644))

	err := RewritePromptTemplateInConfig(root, LegacyPromptTemplatePath, NewPromptTemplatePath)
	require.NoError(t, err)

	data, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "prompt-template: gtms/test/prompts/create-standard.md")
	assert.NotContains(t, string(data), "gtms/cases/prompts")
}

// TestMigratePrompts_BackslashConfigRewritten end-to-end: a half-migrated
// install whose config carries the prompt-template in backslash form should
// end up with the file moved AND the config rewritten in one shim pass. This
// is the failure mode the reviewer caught -- without the backslash handling,
// the file moves successfully but the config stays pointing at the missing
// legacy path, and gtms create silently breaks on the next invocation.
func TestMigratePrompts_BackslashConfigRewritten(t *testing.T) {
	root := t.TempDir()

	// Half-migrated shape with new layout already present.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms", "test", "cases"), 0o755))

	// Legacy prompts/ slot intact.
	legacyPromptsDir := filepath.Join(root, "gtms", "cases", "prompts")
	require.NoError(t, os.MkdirAll(legacyPromptsDir, 0o755))
	legacyTemplate := filepath.Join(legacyPromptsDir, "create-standard.md")
	require.NoError(t, os.WriteFile(legacyTemplate, []byte("template body"), 0o644))

	// Windows-authored config with backslashes in prompt-template.
	backslashConfig := `project:
    name: test
adapters:
    create:
        local-claude:
            prompt-template: gtms\cases\prompts\create-standard.md
            guide-dir: gtms/test/guides
`
	configPath := filepath.Join(root, "gtms.config")
	require.NoError(t, os.WriteFile(configPath, []byte(backslashConfig), 0o644))

	warning, err := MigratePrompts(root)
	require.NoError(t, err)
	assert.Empty(t, warning)

	// File moved to new path.
	assert.FileExists(t, filepath.Join(root, "gtms", "test", "prompts", "create-standard.md"))

	// Config rewritten -- this is the assertion that fails pre-fix.
	data, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "prompt-template: gtms/test/prompts/create-standard.md",
		"shim must rewrite the backslash form to the new path; otherwise gtms create resolves to a missing file")
	assert.NotContains(t, string(data), `gtms\cases`)
}
