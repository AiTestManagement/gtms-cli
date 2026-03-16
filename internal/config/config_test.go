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
	assert.Equal(t, "test-cases/prompts/create-standard.md", lc.PromptTemplate)
	assert.Equal(t, "test-cases/guides/", lc.GuideDir)

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
