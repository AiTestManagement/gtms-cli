package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testdataPath(name string) string {
	return filepath.Join("testdata", name)
}

func TestLoadValidConfig(t *testing.T) {
	cfg, err := LoadFromFile(testdataPath("valid-config.yaml"))
	require.NoError(t, err)

	assert.Equal(t, "My Project", cfg.Project.Name)
	assert.Equal(t, "org/my-repo", cfg.Project.Repo)

	// Check adapters exist
	assert.NotNil(t, cfg.Adapters["create"])
	assert.NotNil(t, cfg.Adapters["create"]["local-claude"])
	assert.NotNil(t, cfg.Adapters["create"]["github-create"])
	assert.NotNil(t, cfg.Adapters["automate"])
	assert.NotNil(t, cfg.Adapters["execute"])

	// Check adapter fields
	lc := cfg.Adapters["create"]["local-claude"]
	assert.Equal(t, "sync", lc.Mode)
	assert.Equal(t, `claude -p {prompt}`, lc.Command)
	assert.Equal(t, "gtms/cases/prompts/create-standard.md", lc.PromptTemplate)
	assert.Equal(t, "gtms/cases/guides/", lc.GuideDir)

	gc := cfg.Adapters["create"]["github-create"]
	assert.Equal(t, "async", gc.Mode)
	assert.Equal(t, "adapters/github-create.sh", gc.Script)
	assert.Equal(t, "adapters/github-create-status.sh", gc.StatusScript)

	// Check defaults
	assert.Equal(t, "local-claude", cfg.Defaults["create"])
	assert.Equal(t, "local-claude", cfg.Defaults["automate"])
	assert.Equal(t, "local-runner", cfg.Defaults["execute"])
}

func TestLoadMinimalConfig(t *testing.T) {
	cfg, err := LoadFromFile(testdataPath("minimal-config.yaml"))
	require.NoError(t, err)

	assert.Equal(t, "Minimal", cfg.Project.Name)
	assert.Equal(t, "org/minimal", cfg.Project.Repo)
	assert.Empty(t, cfg.Adapters)
	assert.Empty(t, cfg.Defaults)
}

func TestLoadBuiltinAdapter(t *testing.T) {
	cfg, err := LoadFromFile(testdataPath("builtin-adapter.yaml"))
	require.NoError(t, err)

	ac := cfg.Adapters["create"]["local-builtin"]
	require.NotNil(t, ac)
	assert.Equal(t, "sync", ac.Mode)
	assert.Empty(t, ac.Command)
	assert.Empty(t, ac.Script)
	assert.Empty(t, ac.Module)
}

func TestInvalidYAML(t *testing.T) {
	_, err := LoadFromFile(testdataPath("invalid-yaml.yaml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Failed to parse gtms.config")
}

func TestMissingProjectName(t *testing.T) {
	_, err := LoadFromFile(testdataPath("invalid-no-project-name.yaml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "'project.name' is required")
}

func TestMissingProjectRepo(t *testing.T) {
	_, err := LoadFromFile(testdataPath("invalid-no-project-repo.yaml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "'project.repo' is required")
}

func TestInvalidMode(t *testing.T) {
	_, err := LoadFromFile(testdataPath("invalid-bad-mode.yaml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid mode 'invalid'")
	assert.Contains(t, err.Error(), "Must be 'async' or 'sync'")
}

func TestCommandAndScript(t *testing.T) {
	_, err := LoadFromFile(testdataPath("invalid-command-and-script.yaml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dual-adapter")
	assert.Contains(t, err.Error(), "command")
	assert.Contains(t, err.Error(), "script")
}

func TestBadDefault(t *testing.T) {
	_, err := LoadFromFile(testdataPath("invalid-bad-default.yaml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent-adapter")
	assert.Contains(t, err.Error(), "not registered")
}

func TestBadDefaultNoCommand(t *testing.T) {
	_, err := LoadFromFile(testdataPath("invalid-default-no-command.yaml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "execute")
	assert.Contains(t, err.Error(), "some-adapter")
	assert.Contains(t, err.Error(), "not registered")
}

func TestStatusScriptRequiresAsync(t *testing.T) {
	_, err := LoadFromFile(testdataPath("invalid-status-script-sync.yaml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status-script")
	assert.Contains(t, err.Error(), "async")
}

func TestStatusScriptRequiresScript(t *testing.T) {
	_, err := LoadFromFile(testdataPath("invalid-status-script-no-script.yaml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status-script")
	assert.Contains(t, err.Error(), "script")
}

func TestFileNotFound(t *testing.T) {
	_, err := LoadFromFile("testdata/nonexistent.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "No gtms.config found")
}

func TestFindProjectRoot(t *testing.T) {
	// Create a temp directory with a gtms.config file
	dir := t.TempDir()
	configPath := filepath.Join(dir, ConfigFileName)
	require.NoError(t, os.WriteFile(configPath, []byte("project:\n  name: Test\n  repo: org/test\n"), 0644))

	// Create a subdirectory
	sub := filepath.Join(dir, "deep", "nested")
	require.NoError(t, os.MkdirAll(sub, 0755))

	// FindProjectRoot from subdirectory should find the root
	root, err := FindProjectRoot(sub)
	require.NoError(t, err)
	assert.Equal(t, filepath.Clean(dir), filepath.Clean(root))
}

func TestFindProjectRoot_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := FindProjectRoot(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "No gtms.config found")
}

// BUG-087: artefact-ignore key retired — config load must reject it explicitly.

func TestLoad_ArtefactIgnore_KeyRejectedAsRetired(t *testing.T) {
	_, err := LoadFromFile(testdataPath("retired-artefact-ignore.yaml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "artefact-ignore")
	assert.Contains(t, err.Error(), "retired")
}

func TestAdapterNames(t *testing.T) {
	cfg, err := LoadFromFile(testdataPath("valid-config.yaml"))
	require.NoError(t, err)

	names := cfg.AdapterNames("create")
	assert.Equal(t, []string{"github-create", "local-claude"}, names)

	names = cfg.AdapterNames("nonexistent")
	assert.Nil(t, names)
}

func TestAdapterNamesString(t *testing.T) {
	cfg, err := LoadFromFile(testdataPath("valid-config.yaml"))
	require.NoError(t, err)

	s := cfg.AdapterNamesString("create")
	assert.Equal(t, "github-create, local-claude", s)
}

func TestConfigLoad_ValidTimeout(t *testing.T) {
	cfg, err := LoadFromFile(testdataPath("valid-timeout.yaml"))
	require.NoError(t, err)

	ac := cfg.Adapters["create"]["local-claude"]
	require.NotNil(t, ac)
	assert.Equal(t, "5m", ac.Timeout)
}

func TestConfigLoad_InvalidTimeout(t *testing.T) {
	_, err := LoadFromFile(testdataPath("invalid-timeout.yaml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid timeout")
	assert.Contains(t, err.Error(), "banana")
}

func TestOutputDirParsesFromYAML(t *testing.T) {
	cfg, err := LoadFromFile(testdataPath("valid-output-dir.yaml"))
	require.NoError(t, err)

	assert.Equal(t, "tests/features/", cfg.Adapters["create"]["my-creator"].OutputDir)
	assert.Equal(t, "tests/e2e/", cfg.Adapters["automate"]["my-automator"].OutputDir)
}

func TestSpecDirNormalizedToOutputDir(t *testing.T) {
	cfg, err := LoadFromFile(testdataPath("valid-spec-dir-compat.yaml"))
	require.NoError(t, err)

	ac := cfg.Adapters["automate"]["playwright"]
	require.NotNil(t, ac)
	assert.Equal(t, "e2e/", ac.OutputDir, "normalization should copy spec-dir to output-dir")
	assert.Equal(t, "e2e/", ac.SpecDir, "original spec-dir field should be unchanged")
}

func TestBothOutputDirAndSpecDirError(t *testing.T) {
	_, err := LoadFromFile(testdataPath("invalid-both-dirs.yaml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "both 'output-dir' and 'spec-dir'")
	assert.Contains(t, err.Error(), "playwright")
}

func TestSpecDirField(t *testing.T) {
	cfg, err := LoadFromFile(testdataPath("valid-spec-dir-compat.yaml"))
	require.NoError(t, err)

	ac := cfg.Adapters["automate"]["playwright"]
	require.NotNil(t, ac)
	assert.Equal(t, "e2e/", ac.SpecDir, "spec-dir should parse from YAML")
}

func TestSpecDirDefaultsToEmpty(t *testing.T) {
	cfg, err := LoadFromFile(testdataPath("valid-config.yaml"))
	require.NoError(t, err)

	ac := cfg.Adapters["create"]["local-claude"]
	require.NotNil(t, ac)
	assert.Empty(t, ac.SpecDir, "spec-dir should default to empty when omitted")
}

func TestSpecDirRejectsAbsolute(t *testing.T) {
	// spec-dir is normalized to output-dir before validation runs,
	// so an absolute spec-dir triggers the absolute output-dir error
	dir := t.TempDir()
	absPath := filepath.Join(dir, "specs")

	cfgContent := "project:\n  name: Test\n  repo: org/test\nadapters:\n  automate:\n    my-adapter:\n      mode: sync\n      spec-dir: " + filepath.ToSlash(absPath) + "\n      command: 'echo test'\ndefaults:\n  automate: my-adapter\n"
	configPath := filepath.Join(dir, "gtms.config")
	require.NoError(t, os.WriteFile(configPath, []byte(cfgContent), 0644))

	_, err := LoadFromFile(configPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "absolute output-dir")
}

func TestGuidanceParsesFromYAML(t *testing.T) {
	cfg, err := LoadFromFile(testdataPath("guidance-off.yaml"))
	require.NoError(t, err)
	assert.False(t, cfg.Guidance, "guidance should be false when set to false in config")
}

func TestGuidanceDefaultsTrue(t *testing.T) {
	cfg, err := LoadFromFile(testdataPath("minimal-config.yaml"))
	require.NoError(t, err)
	assert.True(t, cfg.Guidance, "guidance should default to true when omitted")
}

func TestWriteConfigRoundTrip(t *testing.T) {
	// Load a config, modify it, write it, reload and verify
	cfg, err := LoadFromFile(testdataPath("minimal-config.yaml"))
	require.NoError(t, err)
	assert.True(t, cfg.Guidance, "minimal config should default to guidance on")

	// Toggle guidance off then back on to test round-trip
	cfg.Guidance = false

	// Write to temp file
	dir := t.TempDir()
	outPath := filepath.Join(dir, "gtms.config")
	require.NoError(t, WriteConfig(outPath, cfg))

	// Reload and verify
	cfg2, err := LoadFromFile(outPath)
	require.NoError(t, err)
	assert.False(t, cfg2.Guidance, "round-tripped config should have guidance: false")
	assert.Equal(t, "Minimal", cfg2.Project.Name)
	assert.Equal(t, "org/minimal", cfg2.Project.Repo)
}

func TestValidFramework(t *testing.T) {
	cfg, err := LoadFromFile(testdataPath("valid-framework.yaml"))
	require.NoError(t, err)

	ac := cfg.Adapters["automate"]["claude-bats"]
	require.NotNil(t, ac)
	assert.Equal(t, "bats", ac.Framework)

	ac2 := cfg.Adapters["automate"]["claude-pester"]
	require.NotNil(t, ac2)
	assert.Equal(t, "pester-v2", ac2.Framework)
}

func TestInvalidFramework(t *testing.T) {
	_, err := LoadFromFile(testdataPath("invalid-framework.yaml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid framework")
	assert.Contains(t, err.Error(), "bats/v2")
	assert.Contains(t, err.Error(), "lowercase letters, digits, and hyphens")
}

func TestAbsoluteOutputDirError(t *testing.T) {
	// Use a programmatic config with a platform-appropriate absolute path
	// (filepath.IsAbs("/foo") is false on Windows, so we need a real absolute path)
	dir := t.TempDir()
	absPath := filepath.Join(dir, "output") // guaranteed absolute on any platform

	cfgContent := "project:\n  name: Test\n  repo: org/test\nadapters:\n  create:\n    my-adapter:\n      mode: sync\n      output-dir: " + filepath.ToSlash(absPath) + "\n      command: 'echo test'\ndefaults:\n  create: my-adapter\n"
	configPath := filepath.Join(dir, "gtms.config")
	require.NoError(t, os.WriteFile(configPath, []byte(cfgContent), 0644))

	_, err := LoadFromFile(configPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "absolute output-dir")
	assert.Contains(t, err.Error(), "Must be a relative path")
}

func TestDemoSeededParsesFromYAML(t *testing.T) {
	cfg, err := LoadFromFile(testdataPath("demo-seeded.yaml"))
	require.NoError(t, err)
	assert.True(t, cfg.DemoSeeded, "demo_seeded should be true when set in config")
}

func TestDemoSeededDefaultsFalse(t *testing.T) {
	cfg, err := LoadFromFile(testdataPath("minimal-config.yaml"))
	require.NoError(t, err)
	assert.False(t, cfg.DemoSeeded, "demo_seeded should default to false when omitted")
}

func TestDemoSeededWriteConfigRoundTrip(t *testing.T) {
	cfg, err := LoadFromFile(testdataPath("minimal-config.yaml"))
	require.NoError(t, err)
	assert.False(t, cfg.DemoSeeded)

	cfg.DemoSeeded = true

	dir := t.TempDir()
	outPath := filepath.Join(dir, "gtms.config")
	require.NoError(t, WriteConfig(outPath, cfg))

	cfg2, err := LoadFromFile(outPath)
	require.NoError(t, err)
	assert.True(t, cfg2.DemoSeeded, "round-tripped config should have demo_seeded: true")
	assert.Equal(t, "Minimal", cfg2.Project.Name)
	assert.Equal(t, "org/minimal", cfg2.Project.Repo)
}

// ENH-078: fail-exit-codes config-schema tests.

func TestConfigLoad_ValidFailExitCodes(t *testing.T) {
	cfg, err := LoadFromFile(testdataPath("valid-fail-exit-codes.yaml"))
	require.NoError(t, err)

	ac := cfg.Adapters["execute"]["tier1-fail"]
	require.NotNil(t, ac)
	assert.Equal(t, []int{1}, ac.FailExitCodes)
	assert.Empty(t, cfg.Warnings, "Tier 1 adapter with valid fail-exit-codes should not warn")
}

func TestConfigLoad_InvalidFailExitCodes_String(t *testing.T) {
	_, err := LoadFromFile(testdataPath("invalid-fail-exit-codes-string.yaml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fail-exit-codes")
}

func TestConfigLoad_InvalidFailExitCodes_Negative(t *testing.T) {
	_, err := LoadFromFile(testdataPath("invalid-fail-exit-codes-negative.yaml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fail-exit-codes")
	assert.Contains(t, err.Error(), "-1")
}

func TestConfigLoad_InvalidFailExitCodes_Zero(t *testing.T) {
	_, err := LoadFromFile(testdataPath("invalid-fail-exit-codes-zero.yaml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fail-exit-codes")
	assert.Contains(t, err.Error(), "0")
}

func TestConfigLoad_InvalidFailExitCodes_Nested(t *testing.T) {
	_, err := LoadFromFile(testdataPath("invalid-fail-exit-codes-nested.yaml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fail-exit-codes")
}

func TestConfigLoad_InvalidFailExitCodes_Scalar(t *testing.T) {
	_, err := LoadFromFile(testdataPath("invalid-fail-exit-codes-scalar.yaml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fail-exit-codes")
}

func TestConfigLoad_Tier2FailExitCodes_EmitsWarning(t *testing.T) {
	cfg, err := LoadFromFile(testdataPath("valid-tier2-fail-exit-codes-warns.yaml"))
	require.NoError(t, err, "Tier 2 entry with fail-exit-codes must load successfully (warning, not error)")

	ac := cfg.Adapters["execute"]["tier2-with-key"]
	require.NotNil(t, ac)
	assert.Equal(t, []int{1}, ac.FailExitCodes, "field must be parsed even though it will be ignored at runtime")

	require.Len(t, cfg.Warnings, 1, "exactly one warning expected for the Tier 2 entry")
	w := cfg.Warnings[0]
	assert.Contains(t, w, "tier2-with-key", "warning must name the offending adapter")
	assert.Contains(t, w, "fail-exit-codes", "warning must name the field")
	assert.Contains(t, w, "ignored", "warning must say it will be ignored")
}

// ENH-098: sentinel-based parent directory discovery

func TestFindParentDir_D1_HappyPath(t *testing.T) {
	root := t.TempDir()
	parentDir := filepath.Join(root, "gtms")
	require.NoError(t, os.MkdirAll(parentDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(parentDir, SentinelFileName), []byte(""), 0644))

	name, err := FindParentDir(root)
	require.NoError(t, err)
	assert.Equal(t, "gtms", name)
}

func TestFindParentDir_D1_RenamedParent(t *testing.T) {
	root := t.TempDir()
	parentDir := filepath.Join(root, "testing")
	require.NoError(t, os.MkdirAll(parentDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(parentDir, SentinelFileName), []byte(""), 0644))

	name, err := FindParentDir(root)
	require.NoError(t, err)
	assert.Equal(t, "testing", name)
}

func TestFindParentDir_D2_NoSentinel(t *testing.T) {
	root := t.TempDir()
	// Create some directories but no sentinel
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0755))

	_, err := FindParentDir(root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "No .gtms-root sentinel found")
	assert.Contains(t, err.Error(), "gtms init")
}

func TestFindParentDir_D3_MultipleSentinels(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"gtms", "testing"} {
		d := filepath.Join(root, dir)
		require.NoError(t, os.MkdirAll(d, 0755))
		require.NoError(t, os.WriteFile(filepath.Join(d, SentinelFileName), []byte(""), 0644))
	}

	_, err := FindParentDir(root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Multiple")
	assert.Contains(t, err.Error(), "gtms")
	assert.Contains(t, err.Error(), "testing")
}

func TestFindParentDir_D4_GrandchildSentinel(t *testing.T) {
	root := t.TempDir()
	// Sentinel at grandchild level — should not be found
	deep := filepath.Join(root, "gtms", "cases")
	require.NoError(t, os.MkdirAll(deep, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(deep, SentinelFileName), []byte(""), 0644))

	_, err := FindParentDir(root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "No .gtms-root sentinel found")
}

func TestFindParentDir_D5_SentinelInDotGtms(t *testing.T) {
	root := t.TempDir()
	// Sentinel inside .gtms — excluded from scan
	dotGtms := filepath.Join(root, ".gtms")
	require.NoError(t, os.MkdirAll(dotGtms, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dotGtms, SentinelFileName), []byte(""), 0644))

	_, err := FindParentDir(root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "No .gtms-root sentinel found")
}

func TestFindParentDir_D9_NonEmptySentinel(t *testing.T) {
	root := t.TempDir()
	parentDir := filepath.Join(root, "gtms")
	require.NoError(t, os.MkdirAll(parentDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(parentDir, SentinelFileName), []byte("version: 1\n"), 0644))

	name, err := FindParentDir(root)
	require.NoError(t, err)
	assert.Equal(t, "gtms", name)
}

func TestFindParentDir_D2_EmptyRoot(t *testing.T) {
	root := t.TempDir()
	_, err := FindParentDir(root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "No .gtms-root sentinel found")
}

func TestFindParentDir_D5_SentinelInDotGit(t *testing.T) {
	root := t.TempDir()
	dotGit := filepath.Join(root, ".git")
	require.NoError(t, os.MkdirAll(dotGit, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dotGit, SentinelFileName), []byte(""), 0644))

	_, err := FindParentDir(root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "No .gtms-root sentinel found")
}

// ENH-136: artefact-glob config-schema tests.

func TestArtefactGlob_ValidPattern(t *testing.T) {
	dir := t.TempDir()
	cfgContent := `project:
  name: Test
  repo: org/test
adapters:
  execute:
    bats-runner:
      mode: sync
      framework: bats
      artefact-glob: "test/acceptance/**/{testcase}*.bats"
      command: 'echo test'
defaults:
  execute: bats-runner
`
	configPath := filepath.Join(dir, "gtms.config")
	require.NoError(t, os.WriteFile(configPath, []byte(cfgContent), 0644))

	cfg, err := LoadFromFile(configPath)
	require.NoError(t, err)
	ac := cfg.Adapters["execute"]["bats-runner"]
	require.NotNil(t, ac)
	assert.Equal(t, "test/acceptance/**/{testcase}*.bats", ac.ArtefactGlob)
}

func TestArtefactGlob_EmptyIsValid(t *testing.T) {
	dir := t.TempDir()
	cfgContent := `project:
  name: Test
  repo: org/test
adapters:
  execute:
    runner:
      mode: sync
      command: 'echo test'
defaults:
  execute: runner
`
	configPath := filepath.Join(dir, "gtms.config")
	require.NoError(t, os.WriteFile(configPath, []byte(cfgContent), 0644))

	cfg, err := LoadFromFile(configPath)
	require.NoError(t, err)
	ac := cfg.Adapters["execute"]["runner"]
	require.NotNil(t, ac)
	assert.Empty(t, ac.ArtefactGlob)
}

func TestArtefactGlob_MissingTestcasePlaceholder(t *testing.T) {
	dir := t.TempDir()
	cfgContent := `project:
  name: Test
  repo: org/test
adapters:
  execute:
    runner:
      mode: sync
      artefact-glob: "test/acceptance/**/*.bats"
      command: 'echo test'
defaults:
  execute: runner
`
	configPath := filepath.Join(dir, "gtms.config")
	require.NoError(t, os.WriteFile(configPath, []byte(cfgContent), 0644))

	_, err := LoadFromFile(configPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "artefact-glob")
	assert.Contains(t, err.Error(), "{testcase}")
}

func TestArtefactGlob_AbsolutePath(t *testing.T) {
	dir := t.TempDir()
	absGlob := filepath.Join(dir, "test", "{testcase}*.bats")
	cfgContent := "project:\n  name: Test\n  repo: org/test\nadapters:\n  execute:\n    runner:\n      mode: sync\n      artefact-glob: " + filepath.ToSlash(absGlob) + "\n      command: 'echo test'\ndefaults:\n  execute: runner\n"
	configPath := filepath.Join(dir, "gtms.config")
	require.NoError(t, os.WriteFile(configPath, []byte(cfgContent), 0644))

	_, err := LoadFromFile(configPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "artefact-glob")
	assert.Contains(t, err.Error(), "absolute")
}

func TestArtefactGlob_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	cfgContent := `project:
  name: Test
  repo: org/test
adapters:
  execute:
    runner:
      mode: sync
      artefact-glob: "../escape/{testcase}*.bats"
      command: 'echo test'
defaults:
  execute: runner
`
	configPath := filepath.Join(dir, "gtms.config")
	require.NoError(t, os.WriteFile(configPath, []byte(cfgContent), 0644))

	_, err := LoadFromFile(configPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "artefact-glob")
	assert.Contains(t, err.Error(), "..")
}

func TestFindParentDir_D10_SymlinkRejected(t *testing.T) {
	root := t.TempDir()
	parentDir := filepath.Join(root, "gtms")
	require.NoError(t, os.MkdirAll(parentDir, 0755))

	sentinelPath := filepath.Join(parentDir, SentinelFileName)
	// Create a symlink pointing at the temp dir itself (target doesn't matter)
	err := os.Symlink(root, sentinelPath)
	if err != nil {
		t.Skipf("os.Symlink not available (likely Windows without developer mode): %v", err)
	}

	_, err = FindParentDir(root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Symlink sentinel")
}
