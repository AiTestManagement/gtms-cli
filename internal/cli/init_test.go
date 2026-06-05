package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func skipIfShort(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping: requires git")
	}
}

// initGitRepo creates a temporary directory with git init for testing.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init", dir)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git init failed: %s", string(out))

	gitCfg := exec.Command("git", "-C", dir, "config", "user.email", "test@test.com")
	gitCfg.Run()
	gitCfg2 := exec.Command("git", "-C", dir, "config", "user.name", "Test")
	gitCfg2.Run()

	return dir
}

// writeMinimalConfig writes a minimal valid gtms.config to the given directory.
func writeMinimalConfig(t *testing.T, dir string) {
	t.Helper()
	cfg := `project:
  name: test-project
  repo: org/test-repo
adapters:
  create:
    echo-adapter:
      mode: sync
      command: "echo done"
defaults:
  create: echo-adapter
`
	err := os.WriteFile(filepath.Join(dir, "gtms.config"), []byte(cfg), 0644)
	require.NoError(t, err)
}

func TestInitErrorsWhenAncestorProjectExists(t *testing.T) {
	skipIfShort(t)

	// Setup: git repo with gtms.config at root
	root := initGitRepo(t)
	writeMinimalConfig(t, root)

	// Create a subdirectory
	subdir := filepath.Join(root, "some", "subdir")
	require.NoError(t, os.MkdirAll(subdir, 0755))

	// Save and restore cwd
	origDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Chdir into subdirectory
	require.NoError(t, os.Chdir(subdir))

	// BUG-111: plain init (no --preset) now lists presets, so use --preset to
	// trigger the ancestor detection.
	cmd := newInitCmd()
	cmd.SetArgs([]string{"--preset", "bats"})
	err = cmd.Execute()

	// Should error with ancestor project message
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ancestor GTMS project exists")

	// No gtms.config should be created in the subdirectory
	_, statErr := os.Stat(filepath.Join(subdir, "gtms.config"))
	assert.True(t, os.IsNotExist(statErr), "gtms.config should not be created in subdirectory")
}

func TestInitForceAllowsNestedProject(t *testing.T) {
	skipIfShort(t)

	// Setup: git repo with gtms.config at root
	root := initGitRepo(t)
	writeMinimalConfig(t, root)

	// Create a subdirectory
	subdir := filepath.Join(root, "nested")
	require.NoError(t, os.MkdirAll(subdir, 0755))

	// Save and restore cwd
	origDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Chdir into subdirectory
	require.NoError(t, os.Chdir(subdir))

	// BUG-111: --force now requires --preset to scaffold
	cmd := newInitCmd()
	cmd.SetArgs([]string{"--force", "--preset", "bats"})
	err = cmd.Execute()

	// Should succeed
	assert.NoError(t, err)

	// gtms.config should be created in the subdirectory
	assert.FileExists(t, filepath.Join(subdir, "gtms.config"))
}

func TestInitNoAncestorProjectProceeds(t *testing.T) {
	skipIfShort(t)

	// Setup: git repo with NO gtms.config anywhere
	root := initGitRepo(t)

	// Create a subdirectory
	subdir := filepath.Join(root, "subproject")
	require.NoError(t, os.MkdirAll(subdir, 0755))

	// Save and restore cwd
	origDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Chdir into subdirectory
	require.NoError(t, os.Chdir(subdir))

	// BUG-111: plain init lists presets; use --preset to scaffold
	cmd := newInitCmd()
	cmd.SetArgs([]string{"--preset", "bats"})
	err = cmd.Execute()

	assert.NoError(t, err)
	assert.FileExists(t, filepath.Join(subdir, "gtms.config"))
}

func TestInitAtProjectRootStillBlockedByExistingConfig(t *testing.T) {
	skipIfShort(t)

	// Setup: git repo with gtms.config at root
	root := initGitRepo(t)
	writeMinimalConfig(t, root)

	// Save and restore cwd
	origDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Chdir to root (where gtms.config already exists)
	require.NoError(t, os.Chdir(root))

	// BUG-111: --preset required to trigger scaffolding; plain init lists presets
	cmd := newInitCmd()
	cmd.SetArgs([]string{"--preset", "bats"})
	err = cmd.Execute()

	// Should error with the existing config message (not ancestor message)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "gtms.config already exists")
}

func TestInitGuidanceOffFromSubdirectoryUpdatesAncestorConfig(t *testing.T) {
	skipIfShort(t)

	root := initGitRepo(t)
	writeMinimalConfig(t, root)

	subdir := filepath.Join(root, "some", "subdir")
	require.NoError(t, os.MkdirAll(subdir, 0755))

	origDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { os.Chdir(origDir) })
	require.NoError(t, os.Chdir(subdir))

	cmd := newInitCmd()
	cmd.SetArgs([]string{"--guidance-off"})
	err = cmd.Execute()
	assert.NoError(t, err)

	// Ancestor config should now contain guidance: false
	content, readErr := os.ReadFile(filepath.Join(root, "gtms.config"))
	require.NoError(t, readErr)
	assert.Contains(t, string(content), "guidance: false")

	// Subdirectory should NOT have a new gtms.config
	_, statErr := os.Stat(filepath.Join(subdir, "gtms.config"))
	assert.True(t, os.IsNotExist(statErr), "gtms.config should not be created in subdirectory")
}

func TestInitGuidanceOnFromSubdirectoryUpdatesAncestorConfig(t *testing.T) {
	skipIfShort(t)

	root := initGitRepo(t)
	// Config with guidance: false
	cfg := `project:
  name: test-project
  repo: org/test-repo
guidance: false
adapters:
  create:
    echo-adapter:
      mode: sync
      command: "echo done"
defaults:
  create: echo-adapter
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "gtms.config"), []byte(cfg), 0644))

	subdir := filepath.Join(root, "nested", "deeper")
	require.NoError(t, os.MkdirAll(subdir, 0755))

	origDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { os.Chdir(origDir) })
	require.NoError(t, os.Chdir(subdir))

	cmd := newInitCmd()
	cmd.SetArgs([]string{"--guidance-on"})
	err = cmd.Execute()
	assert.NoError(t, err)

	content, readErr := os.ReadFile(filepath.Join(root, "gtms.config"))
	require.NoError(t, readErr)
	assert.Contains(t, string(content), "guidance: true")

	_, statErr := os.Stat(filepath.Join(subdir, "gtms.config"))
	assert.True(t, os.IsNotExist(statErr))
}

func TestInitGuidanceOffFromProjectRootStillWorks(t *testing.T) {
	skipIfShort(t)

	root := initGitRepo(t)
	writeMinimalConfig(t, root)

	origDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { os.Chdir(origDir) })
	require.NoError(t, os.Chdir(root))

	cmd := newInitCmd()
	cmd.SetArgs([]string{"--guidance-off"})
	err = cmd.Execute()
	assert.NoError(t, err)

	content, readErr := os.ReadFile(filepath.Join(root, "gtms.config"))
	require.NoError(t, readErr)
	assert.Contains(t, string(content), "guidance: false")
}

func TestInitGuidanceFlagErrorsWithNoAncestorProject(t *testing.T) {
	skipIfShort(t)

	run := func(t *testing.T, flag string) {
		// Git repo but NO ancestor gtms.config
		root := initGitRepo(t)

		subdir := filepath.Join(root, "subproject")
		require.NoError(t, os.MkdirAll(subdir, 0755))

		origDir, err := os.Getwd()
		require.NoError(t, err)
		t.Cleanup(func() { os.Chdir(origDir) })
		require.NoError(t, os.Chdir(subdir))

		cmd := newInitCmd()
		cmd.SetArgs([]string{flag})
		err = cmd.Execute()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "requires an existing GTMS project")

		// gtms.config must NOT have been created (no fall-through to scaffolding)
		assert.NoFileExists(t, filepath.Join(subdir, "gtms.config"))
		assert.NoFileExists(t, filepath.Join(root, "gtms.config"))
	}

	t.Run("guidance-off", func(t *testing.T) { run(t, "--guidance-off") })
	t.Run("guidance-on", func(t *testing.T) { run(t, "--guidance-on") })
}

// --- ENH-107: S4 detector tests ---

func TestInit_S4_DetectsFlatLayout(t *testing.T) {
	skipIfShort(t)

	tests := []struct {
		name    string
		dirs    []string
		wantErr bool
	}{
		{"test-cases only", []string{"test-cases"}, true},
		{"test-automation only", []string{"test-automation"}, true},
		{"test-tasks only", []string{"test-tasks"}, true},
		{"test-execution only", []string{"test-execution"}, true},
		{"all four", []string{"test-cases", "test-automation", "test-tasks", "test-execution"}, true},
		{"test-tasks and test-execution", []string{"test-tasks", "test-execution"}, true},
		{"none present", []string{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := initGitRepo(t)

			for _, d := range tt.dirs {
				require.NoError(t, os.MkdirAll(filepath.Join(root, d), 0o755))
			}

			origDir, err := os.Getwd()
			require.NoError(t, err)
			t.Cleanup(func() { os.Chdir(origDir) })
			require.NoError(t, os.Chdir(root))

			cmd := newInitCmd()
			cmd.SetArgs([]string{})
			err = cmd.Execute()

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "flat v0.1.0 layout detected")
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestInit_S4_ErrorListsOnlyPresentDirs(t *testing.T) {
	// Unit test for flatLayoutErrorMessage -- no git needed
	msg, _ := flatLayoutErrorMessage([]string{"test-tasks", "test-execution"}, "linux")
	assert.Contains(t, msg, "test-tasks")
	assert.Contains(t, msg, "test-execution")
	assert.NotContains(t, msg, "test-cases")
	assert.NotContains(t, msg, "test-automation")
}

func TestInit_S4_HintCrossPlatform(t *testing.T) {
	dirs := []string{"test-cases"}

	_, hintUnix := flatLayoutErrorMessage(dirs, "linux")
	assert.Contains(t, hintUnix, "mkdir -p gtms && touch gtms/.gtms-root")
	assert.NotContains(t, hintUnix, "New-Item")

	_, hintWin := flatLayoutErrorMessage(dirs, "windows")
	assert.Contains(t, hintWin, "New-Item -ItemType File")
	assert.NotContains(t, hintWin, "mkdir -p gtms && touch")
}

func TestInit_S4_HintIncludesPreservationFooter(t *testing.T) {
	_, hint := flatLayoutErrorMessage([]string{"test-cases"}, "linux")
	assert.Contains(t, hint, "GTMS will preserve your migrated contents when you re-run init.")
}

func TestInit_S4_ErrorRecipeListsGitMvForPresentDirs(t *testing.T) {
	_, hint := flatLayoutErrorMessage([]string{"test-cases", "test-tasks"}, "linux")
	assert.Contains(t, hint, "git mv test-cases gtms/cases")
	assert.Contains(t, hint, "git mv test-tasks gtms/tasks")
	assert.NotContains(t, hint, "git mv test-automation")
	assert.NotContains(t, hint, "git mv test-execution")
}

func TestInit_S4_ForceFlatLayoutHardError(t *testing.T) {
	skipIfShort(t)

	root := initGitRepo(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "test-cases"), 0o755))

	origDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { os.Chdir(origDir) })
	require.NoError(t, os.Chdir(root))

	cmd := newInitCmd()
	cmd.SetArgs([]string{"--force"})
	err = cmd.Execute()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "flat v0.1.0 layout detected")
}

func TestInit_S4_DemoFlatLayoutHardError(t *testing.T) {
	skipIfShort(t)

	root := initGitRepo(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "test-automation"), 0o755))

	origDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { os.Chdir(origDir) })
	require.NoError(t, os.Chdir(root))

	cmd := newInitCmd()
	cmd.SetArgs([]string{"--demo"})
	err = cmd.Execute()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "flat v0.1.0 layout detected")
}

func TestInit_S4_DemoForceFlatLayoutHardError(t *testing.T) {
	skipIfShort(t)

	root := initGitRepo(t)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "test-tasks"), 0o755))

	origDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { os.Chdir(origDir) })
	require.NoError(t, os.Chdir(root))

	cmd := newInitCmd()
	cmd.SetArgs([]string{"--demo", "--force"})
	err = cmd.Execute()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "flat v0.1.0 layout detected")
}

func TestInitForceFromSubdirDoesNotModifyAncestor(t *testing.T) {
	skipIfShort(t)

	// Setup: git repo with gtms.config at root
	root := initGitRepo(t)
	writeMinimalConfig(t, root)

	// Read the ancestor config content before
	ancestorCfgPath := filepath.Join(root, "gtms.config")
	beforeContent, err := os.ReadFile(ancestorCfgPath)
	require.NoError(t, err)

	// Create a subdirectory
	subdir := filepath.Join(root, "nested-project")
	require.NoError(t, os.MkdirAll(subdir, 0755))

	// Save and restore cwd
	origDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Chdir into subdirectory
	require.NoError(t, os.Chdir(subdir))

	// Run init with --force
	cmd := newInitCmd()
	cmd.SetArgs([]string{"--force"})
	err = cmd.Execute()

	assert.NoError(t, err)

	// Ancestor config should be untouched
	afterContent, err := os.ReadFile(ancestorCfgPath)
	require.NoError(t, err)
	assert.Equal(t, string(beforeContent), string(afterContent), "ancestor gtms.config should not be modified")
}

// --- ENH-107: Demo happy path from empty repo ---

func TestInitDemoFromEmptyRepo(t *testing.T) {
	skipIfShort(t)

	root := initGitRepo(t)

	origDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { os.Chdir(origDir) })
	require.NoError(t, os.Chdir(root))

	cmd := newInitCmd()
	cmd.SetArgs([]string{"--demo"})
	err = cmd.Execute()

	require.NoError(t, err)

	// Nested layout should be created
	assert.DirExists(t, filepath.Join(root, "gtms", "cases"))
	assert.DirExists(t, filepath.Join(root, "gtms", "tasks", "pending"))
	assert.FileExists(t, filepath.Join(root, "gtms", ".gtms-root"))

	// Demo guide should be present
	assert.FileExists(t, filepath.Join(root, "gtms", "cases", "guides", "getting-started-with-ai.md"))

	// Config should exist with demo_seeded and demo adapters
	assert.FileExists(t, filepath.Join(root, "gtms.config"))
	content, err := os.ReadFile(filepath.Join(root, "gtms.config"))
	require.NoError(t, err)
	configStr := string(content)
	assert.Contains(t, configStr, "demo_seeded: true")
	assert.Contains(t, configStr, "demo")
}
