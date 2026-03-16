package scaffold

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/aitestmanagement/gtms-cli/internal/config"
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

	// Configure git user for the test repo (required for some git operations)
	gitCfg := exec.Command("git", "-C", dir, "config", "user.email", "test@test.com")
	gitCfg.Run()
	gitCfg2 := exec.Command("git", "-C", dir, "config", "user.name", "Test")
	gitCfg2.Run()

	return dir
}

func TestInitMinimalPreset(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	result, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Test Project",
		Repo:        "org/test-repo",
		Preset:      PresetMinimal,
		Force:       false,
	})

	require.NoError(t, err)
	require.NotNil(t, result)

	// Config file should exist
	assert.FileExists(t, filepath.Join(dir, "gtms.config"))
	assert.Contains(t, result.FilesCreated, "gtms.config")

	// Task directories should exist
	for _, status := range []string{"pending", "in-progress", "in-review", "complete", "failed"} {
		assert.DirExists(t, filepath.Join(dir, "test-tasks", status))
		assert.FileExists(t, filepath.Join(dir, "test-tasks", status, ".gitkeep"))
	}

	// test-cases dir should exist
	assert.DirExists(t, filepath.Join(dir, "test-cases"))
	assert.DirExists(t, filepath.Join(dir, "test-cases", "guides"))

	// automation dirs should exist
	assert.DirExists(t, filepath.Join(dir, "test-automation", "records"))
	assert.DirExists(t, filepath.Join(dir, "test-automation", "specs"))

	// test-execution dir should exist
	assert.DirExists(t, filepath.Join(dir, "test-execution"))

	// Minimal preset should NOT create prompts dir or prompt templates
	assert.NoDirExists(t, filepath.Join(dir, "test-automation", "prompts"))
	assert.NoDirExists(t, filepath.Join(dir, "test-cases", "prompts"))

	// No adapter stubs
	assert.NoDirExists(t, filepath.Join(dir, "adapters"))
}

func TestInitClaudePreset(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	result, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Claude Project",
		Repo:        "org/claude-repo",
		Preset:      PresetClaude,
		Force:       false,
	})

	require.NoError(t, err)
	require.NotNil(t, result)

	// Config file should exist
	assert.FileExists(t, filepath.Join(dir, "gtms.config"))

	// Prompt templates should be created
	assert.FileExists(t, filepath.Join(dir, "test-cases", "prompts", "create-standard.md"))
	assert.FileExists(t, filepath.Join(dir, "test-automation", "prompts", "automate-standard.md"))

	// Guide file should be created
	assert.FileExists(t, filepath.Join(dir, "test-cases", "guides", "test-case-template.md"))

	// Verify prompt template content uses {reference} not {requirement}
	createContent, err := os.ReadFile(filepath.Join(dir, "test-cases", "prompts", "create-standard.md"))
	require.NoError(t, err)
	assert.Contains(t, string(createContent), "{reference}")
	assert.Contains(t, string(createContent), "{guides}")
	assert.NotContains(t, string(createContent), "{requirement}")

	automateContent, err := os.ReadFile(filepath.Join(dir, "test-automation", "prompts", "automate-standard.md"))
	require.NoError(t, err)
	assert.Contains(t, string(automateContent), "{testcase_content}")
	assert.Contains(t, string(automateContent), "{framework}")

	// Verify guide content
	guideContent, err := os.ReadFile(filepath.Join(dir, "test-cases", "guides", "test-case-template.md"))
	require.NoError(t, err)
	assert.Contains(t, string(guideContent), "Test Case Template")
	assert.Contains(t, string(guideContent), "Required Sections")

	// Files should be tracked in result
	foundCreate := false
	foundAutomate := false
	foundGuide := false
	for _, f := range result.FilesCreated {
		if f == "test-cases/prompts/create-standard.md" {
			foundCreate = true
		}
		if f == "test-automation/prompts/automate-standard.md" {
			foundAutomate = true
		}
		if f == "test-cases/guides/test-case-template.md" {
			foundGuide = true
		}
	}
	assert.True(t, foundCreate, "create template should be in FilesCreated")
	assert.True(t, foundAutomate, "automate template should be in FilesCreated")
	assert.True(t, foundGuide, "guide file should be in FilesCreated")

	// README should be created
	readmePath := filepath.Join(dir, "test-cases", "README.md")
	assert.FileExists(t, readmePath)
	readmeContent, err := os.ReadFile(readmePath)
	require.NoError(t, err)
	assert.Contains(t, string(readmeContent), "Prompt Template")
	assert.Contains(t, string(readmeContent), "Section Ordering Rules")
	assert.Contains(t, string(readmeContent), "{guides}")

	// Claude preset should NOT create adapter stubs
	assert.NoDirExists(t, filepath.Join(dir, "adapters"))
}

func TestInitGitHubPreset(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	result, err := Init(Options{
		ProjectRoot: dir,
		Name:        "GitHub Project",
		Repo:        "org/github-repo",
		Preset:      PresetGitHub,
		Force:       false,
	})

	require.NoError(t, err)
	require.NotNil(t, result)

	// Config file should exist
	assert.FileExists(t, filepath.Join(dir, "gtms.config"))

	// Prompt templates should be created
	assert.FileExists(t, filepath.Join(dir, "test-cases", "prompts", "create-standard.md"))
	assert.FileExists(t, filepath.Join(dir, "test-automation", "prompts", "automate-standard.md"))

	// Guide file should be created
	assert.FileExists(t, filepath.Join(dir, "test-cases", "guides", "test-case-template.md"))

	// Adapter stubs should be created
	stubs := []string{
		"adapters/github-create.sh",
		"adapters/github-create-status.sh",
		"adapters/github-automate.sh",
		"adapters/github-automate-status.sh",
		"adapters/github-execute.sh",
		"adapters/github-execute-status.sh",
	}
	for _, stub := range stubs {
		assert.FileExists(t, filepath.Join(dir, filepath.FromSlash(stub)), "missing stub: %s", stub)
	}

	// Verify stub content
	createStub, err := os.ReadFile(filepath.Join(dir, "adapters", "github-create.sh"))
	require.NoError(t, err)
	assert.Contains(t, string(createStub), "#!/bin/bash")
	assert.Contains(t, string(createStub), "GTMS_TASK_ID")
	assert.Contains(t, string(createStub), "GTMS_RESULT_FILE")
	assert.Contains(t, string(createStub), "GTMS_REFERENCE")
	assert.Contains(t, string(createStub), "GTMS_GUIDES")
	assert.NotContains(t, string(createStub), "GTMS_REQUIREMENT")
	assert.NotContains(t, string(createStub), "GTMS_FORMAT")

	// Check file permissions on Unix
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dir, "adapters", "github-create.sh"))
		require.NoError(t, err)
		assert.True(t, info.Mode()&0o111 != 0, "stub scripts should be executable")
	}

	// Adapter stubs should be tracked in result
	foundStubs := 0
	for _, f := range result.FilesCreated {
		if strings.HasPrefix(f, "adapters/") {
			foundStubs++
		}
	}
	assert.Equal(t, 6, foundStubs, "should have 6 adapter stubs in FilesCreated")
}

func TestInitForceOverwritesConfig(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	// Write an existing config
	existingConfig := "project:\n  name: Old\n  repo: old/repo\n"
	err := os.WriteFile(filepath.Join(dir, "gtms.config"), []byte(existingConfig), 0o644)
	require.NoError(t, err)

	// Init with --force should succeed
	result, err := Init(Options{
		ProjectRoot: dir,
		Name:        "New Project",
		Repo:        "new/repo",
		Preset:      PresetMinimal,
		Force:       true,
	})

	require.NoError(t, err)
	require.NotNil(t, result)

	// Config should be overwritten
	content, err := os.ReadFile(filepath.Join(dir, "gtms.config"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "New Project")
	assert.NotContains(t, string(content), "Old")
}

func TestInitFailsIfConfigExistsWithoutForce(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	// Write an existing config
	existingConfig := "project:\n  name: Existing\n  repo: existing/repo\n"
	err := os.WriteFile(filepath.Join(dir, "gtms.config"), []byte(existingConfig), 0o644)
	require.NoError(t, err)

	// Init without --force should fail
	_, err = Init(Options{
		ProjectRoot: dir,
		Name:        "New",
		Repo:        "new/repo",
		Preset:      PresetMinimal,
		Force:       false,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestInitInvalidPreset(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	_, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Test",
		Repo:        "org/repo",
		Preset:      "invalid-preset",
		Force:       false,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown adapter preset")
}

func TestInitExistingDirectoriesNoError(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	// Create some directories in advance
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "test-tasks", "pending"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "test-cases"), 0o755))

	// Put a file in pending to verify it is not overwritten
	existingFile := filepath.Join(dir, "test-tasks", "pending", "existing.md")
	require.NoError(t, os.WriteFile(existingFile, []byte("keep me"), 0o644))

	// Init should succeed without errors
	_, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Test",
		Repo:        "org/test",
		Preset:      PresetMinimal,
		Force:       false,
	})

	require.NoError(t, err)

	// Existing file should still be there
	content, err := os.ReadFile(existingFile)
	require.NoError(t, err)
	assert.Equal(t, "keep me", string(content))
}

func TestInitExistingGitkeepNotOverwritten(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	// Create a directory with a .gitkeep that has content
	pendingDir := filepath.Join(dir, "test-tasks", "pending")
	require.NoError(t, os.MkdirAll(pendingDir, 0o755))
	gitkeepPath := filepath.Join(pendingDir, ".gitkeep")
	require.NoError(t, os.WriteFile(gitkeepPath, []byte("custom content"), 0o644))

	_, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Test",
		Repo:        "org/test",
		Preset:      PresetMinimal,
		Force:       false,
	})

	require.NoError(t, err)

	// .gitkeep should not be overwritten
	content, err := os.ReadFile(gitkeepPath)
	require.NoError(t, err)
	assert.Equal(t, "custom content", string(content))
}

func TestConfigValidationMinimalPreset(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	_, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Validation Test",
		Repo:        "org/validation",
		Preset:      PresetMinimal,
		Force:       false,
	})
	require.NoError(t, err)

	// Load the generated config through the config package validator
	cfg, err := config.LoadFromFile(filepath.Join(dir, "gtms.config"))
	require.NoError(t, err)
	assert.Equal(t, "Validation Test", cfg.Project.Name)
	assert.Equal(t, "org/validation", cfg.Project.Repo)
}

func TestConfigValidationClaudePreset(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	_, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Claude Validation",
		Repo:        "org/claude-val",
		Preset:      PresetClaude,
		Force:       false,
	})
	require.NoError(t, err)

	cfg, err := config.LoadFromFile(filepath.Join(dir, "gtms.config"))
	require.NoError(t, err)
	assert.Equal(t, "Claude Validation", cfg.Project.Name)
	assert.Equal(t, "org/claude-val", cfg.Project.Repo)

	// Verify adapter structure
	assert.NotNil(t, cfg.Adapters["create"]["local-claude"])
	assert.Equal(t, "sync", cfg.Adapters["create"]["local-claude"].Mode)
	assert.NotEmpty(t, cfg.Adapters["create"]["local-claude"].Command)
	assert.Equal(t, "test-cases/prompts/create-standard.md", cfg.Adapters["create"]["local-claude"].PromptTemplate)
	assert.Equal(t, "test-cases/guides/", cfg.Adapters["create"]["local-claude"].GuideDir)

	assert.NotNil(t, cfg.Adapters["automate"]["local-claude"])
	assert.NotNil(t, cfg.Adapters["execute"]["local-runner"])

	// Verify defaults
	assert.Equal(t, "local-claude", cfg.Defaults["create"])
	assert.Equal(t, "local-claude", cfg.Defaults["automate"])
	assert.Equal(t, "local-runner", cfg.Defaults["execute"])
}

// TestScaffoldClaude_PromptFilePattern verifies that the claude preset command
// templates use {prompt_file} for prompt delivery (BUG-005 fix).
func TestScaffoldClaude_PromptFilePattern(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	_, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Prompt File Test",
		Repo:        "org/prompt-file",
		Preset:      PresetClaude,
		Force:       false,
	})
	require.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(dir, "gtms.config"))
	require.NoError(t, err)

	configStr := string(content)

	// Command templates should use {prompt_file} for prompt delivery
	assert.Contains(t, configStr, `{prompt_file}`,
		"command templates should contain {prompt_file} placeholder")

	// Verify the correct pattern is used
	assert.Contains(t, configStr, `--append-system-prompt-file {prompt_file}`,
		"command templates should use {prompt_file} pattern")

	// Config must still pass validation
	cfg, err := config.LoadFromFile(filepath.Join(dir, "gtms.config"))
	require.NoError(t, err)
	assert.NotEmpty(t, cfg.Adapters["create"]["local-claude"].Command)
}

// TestBUG007_CreateTemplateOutputRulesAtEnd verifies that the create prompt template
// places output rules AFTER variable-length content ({guides}, {context}).
// LLMs have positional attention bias -- instructions at the end are followed more reliably
// when preceded by large reference material. See BUG-007, ADR-001, and ENH-029.
// Note: For Claude Code, emphatic format instructions go in the -p user message (BUG-005).
// The template carries output rules as guidelines for all adapters.
func TestBUG007_CreateTemplateOutputRulesAtEnd(t *testing.T) {
	template := promptCreateStandard

	fileDelimiterPos := strings.Index(template, "<gtms-file")
	guidesPos := strings.Index(template, "{guides}")
	contextPos := strings.Index(template, "{context}")

	require.NotEqual(t, -1, fileDelimiterPos, "template must contain <gtms-file output tag instruction")
	require.NotEqual(t, -1, guidesPos, "template must contain {guides} variable")
	require.NotEqual(t, -1, contextPos, "template must contain {context} variable")

	// Output rules must come AFTER variable-length content
	assert.Greater(t, fileDelimiterPos, guidesPos,
		"output rules (<gtms-file>) must come after {guides}")
	assert.Greater(t, fileDelimiterPos, contextPos,
		"output rules (<gtms-file>) must come after {context}")

	// Verify output rules section exists (XML tag format)
	assert.Contains(t, template, "<output_rules>",
		"template must include <output_rules> XML tag")
	assert.Contains(t, template, "</output_rules>",
		"template must include closing </output_rules> XML tag")
}

func TestConfigValidationGitHubPreset(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	_, err := Init(Options{
		ProjectRoot: dir,
		Name:        "GitHub Validation",
		Repo:        "org/github-val",
		Preset:      PresetGitHub,
		Force:       false,
	})
	require.NoError(t, err)

	cfg, err := config.LoadFromFile(filepath.Join(dir, "gtms.config"))
	require.NoError(t, err)
	assert.Equal(t, "GitHub Validation", cfg.Project.Name)
	assert.Equal(t, "org/github-val", cfg.Project.Repo)

	// Verify adapter structure
	gc := cfg.Adapters["create"]["github-create"]
	require.NotNil(t, gc)
	assert.Equal(t, "async", gc.Mode)
	assert.Equal(t, "adapters/github-create.sh", gc.Script)
	assert.Equal(t, "adapters/github-create-status.sh", gc.StatusScript)
	assert.Equal(t, "test-cases/prompts/create-standard.md", gc.PromptTemplate)
	assert.Equal(t, "test-cases/guides/", gc.GuideDir)

	ga := cfg.Adapters["automate"]["github-automate"]
	require.NotNil(t, ga)
	assert.Equal(t, "async", ga.Mode)

	ge := cfg.Adapters["execute"]["github-actions"]
	require.NotNil(t, ge)
	assert.Equal(t, "async", ge.Mode)

	// Verify defaults
	assert.Equal(t, "github-create", cfg.Defaults["create"])
	assert.Equal(t, "github-automate", cfg.Defaults["automate"])
	assert.Equal(t, "github-actions", cfg.Defaults["execute"])
}

func TestCreateDirectoriesReturnsCorrectList(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	dirs, err := CreateDirectories(dir, PresetMinimal)
	require.NoError(t, err)

	expected := []string{
		"test-tasks/pending",
		"test-tasks/in-progress",
		"test-tasks/in-review",
		"test-tasks/complete",
		"test-tasks/failed",
		"test-cases",
		"test-cases/guides",
		"test-automation/records",
		"test-automation/specs",
		"test-execution",
	}
	assert.Equal(t, expected, dirs)
}

func TestCreateDirectoriesClaudeIncludesPrompts(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	dirs, err := CreateDirectories(dir, PresetClaude)
	require.NoError(t, err)

	assert.Contains(t, dirs, "test-cases/prompts")
	assert.Contains(t, dirs, "test-automation/prompts")
}

func TestCreateDirectoriesGitHubIncludesAdapters(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	dirs, err := CreateDirectories(dir, PresetGitHub)
	require.NoError(t, err)

	assert.Contains(t, dirs, "test-cases/prompts")
	assert.Contains(t, dirs, "test-automation/prompts")
	assert.Contains(t, dirs, "adapters")
}

func TestWriteConfigContent(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	path, err := WriteConfig(dir, "My Project", "org/my-repo", PresetMinimal)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "gtms.config"), path)

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(content), "My Project")
	assert.Contains(t, string(content), "org/my-repo")
}

func TestWritePromptTemplatesContent(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	files, err := WritePromptTemplates(dir)
	require.NoError(t, err)
	assert.Len(t, files, 2)

	// Verify create template uses {reference} not {requirement}
	createContent, err := os.ReadFile(filepath.Join(dir, "test-cases", "prompts", "create-standard.md"))
	require.NoError(t, err)
	assert.Contains(t, string(createContent), "{reference}")
	assert.NotContains(t, string(createContent), "{requirement}")
}

func TestWriteStarterGuides(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	files, err := WriteStarterGuides(dir)
	require.NoError(t, err)
	assert.Len(t, files, 1)
	assert.Equal(t, "test-cases/guides/test-case-template.md", files[0])

	// Verify content
	content, err := os.ReadFile(filepath.Join(dir, "test-cases", "guides", "test-case-template.md"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "Test Case Template")
	assert.Contains(t, string(content), "Required Sections")
	assert.Contains(t, string(content), "Principles")
}

func TestWriteAdapterStubsContent(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	files, err := WriteAdapterStubs(dir)
	require.NoError(t, err)
	assert.Len(t, files, 6)

	// Each file should exist and contain bash shebang
	for _, f := range files {
		content, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f)))
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(string(content), "#!/bin/bash"), "stub %s should start with shebang", f)
	}
}

func TestValidPresets(t *testing.T) {
	assert.True(t, IsValidPreset("minimal"))
	assert.True(t, IsValidPreset("claude"))
	assert.True(t, IsValidPreset("github"))
	assert.False(t, IsValidPreset("invalid"))
	assert.False(t, IsValidPreset(""))
}

func TestIntegrationFullInit(t *testing.T) {
	skipIfShort(t)
	// Create a temp directory and git init
	dir := initGitRepo(t)

	// Run full Init with claude preset
	result, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Integration Test",
		Repo:        "org/integration",
		Preset:      PresetClaude,
		Force:       false,
	})

	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify all directories exist
	expectedDirs := []string{
		"test-tasks/pending",
		"test-tasks/in-progress",
		"test-tasks/in-review",
		"test-tasks/complete",
		"test-tasks/failed",
		"test-cases",
		"test-cases/guides",
		"test-cases/prompts",
		"test-automation/records",
		"test-automation/specs",
		"test-automation/prompts",
		"test-execution",
	}
	for _, d := range expectedDirs {
		assert.DirExists(t, filepath.Join(dir, filepath.FromSlash(d)), "missing dir: %s", d)
	}

	// Verify all expected files
	expectedFiles := []string{
		"gtms.config",
		"test-cases/prompts/create-standard.md",
		"test-automation/prompts/automate-standard.md",
		"test-cases/guides/test-case-template.md",
	}
	for _, f := range expectedFiles {
		assert.FileExists(t, filepath.Join(dir, filepath.FromSlash(f)), "missing file: %s", f)
	}

	// Verify config loads and validates
	cfg, err := config.LoadFromFile(filepath.Join(dir, "gtms.config"))
	require.NoError(t, err)
	assert.Equal(t, "Integration Test", cfg.Project.Name)
	assert.Equal(t, "org/integration", cfg.Project.Repo)
	assert.Equal(t, "local-claude", cfg.Defaults["create"])

	// Verify result has accurate tracking
	assert.Equal(t, filepath.Join(dir, "gtms.config"), result.ConfigPath)
	assert.True(t, len(result.DirsCreated) >= 8, "should have at least 8 dirs created")
	assert.True(t, len(result.FilesCreated) >= 5, "should have at least 5 files created (config + 2 templates + 1 guide + 1 README)")
}

// ENH-010: YAML injection tests -- project names with special characters
// must produce valid YAML that passes config.LoadFromFile().

func TestYAMLSafeString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"plain string", "hello", "hello"},
		{"double quote", `my "project"`, `my \"project\"`},
		{"backslash", `path\to\thing`, `path\\to\\thing`},
		{"both", `say "hello\n"`, `say \"hello\\n\"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, yamlSafeString(tt.input))
		})
	}
}

func TestInitWithDoubleQuoteInName(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	_, err := Init(Options{
		ProjectRoot: dir,
		Name:        `My "Quoted" Project`,
		Repo:        "org/repo",
		Preset:      PresetMinimal,
		Force:       false,
	})
	require.NoError(t, err)

	cfg, err := config.LoadFromFile(filepath.Join(dir, "gtms.config"))
	require.NoError(t, err)
	assert.Equal(t, `My "Quoted" Project`, cfg.Project.Name)
}

func TestInitWithSingleQuoteInName(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	_, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Bill's Project",
		Repo:        "org/repo",
		Preset:      PresetMinimal,
		Force:       false,
	})
	require.NoError(t, err)

	cfg, err := config.LoadFromFile(filepath.Join(dir, "gtms.config"))
	require.NoError(t, err)
	assert.Equal(t, "Bill's Project", cfg.Project.Name)
}

func TestInitWithColonInName(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	_, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Project: Alpha",
		Repo:        "org/repo",
		Preset:      PresetMinimal,
		Force:       false,
	})
	require.NoError(t, err)

	cfg, err := config.LoadFromFile(filepath.Join(dir, "gtms.config"))
	require.NoError(t, err)
	assert.Equal(t, "Project: Alpha", cfg.Project.Name)
}

func TestInitWithDoubleQuoteInNameClaudePreset(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	_, err := Init(Options{
		ProjectRoot: dir,
		Name:        `My "Quoted" Project`,
		Repo:        `org/"special"`,
		Preset:      PresetClaude,
		Force:       false,
	})
	require.NoError(t, err)

	cfg, err := config.LoadFromFile(filepath.Join(dir, "gtms.config"))
	require.NoError(t, err)
	assert.Equal(t, `My "Quoted" Project`, cfg.Project.Name)
	assert.Equal(t, `org/"special"`, cfg.Project.Repo)
}

func TestIntegrationGitHubFullInit(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	result, err := Init(Options{
		ProjectRoot: dir,
		Name:        "GitHub Integration",
		Repo:        "org/github-int",
		Preset:      PresetGitHub,
		Force:       false,
	})

	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify config loads and validates
	cfg, err := config.LoadFromFile(filepath.Join(dir, "gtms.config"))
	require.NoError(t, err)
	assert.Equal(t, "GitHub Integration", cfg.Project.Name)
	assert.Equal(t, "github-create", cfg.Defaults["create"])

	// Verify adapter stubs exist
	assert.FileExists(t, filepath.Join(dir, "adapters", "github-create.sh"))
	assert.FileExists(t, filepath.Join(dir, "adapters", "github-execute-status.sh"))

	// Verify guide file exists
	assert.FileExists(t, filepath.Join(dir, "test-cases", "guides", "test-case-template.md"))

	// Total files: 1 config + 2 templates + 1 guide + 1 README + 6 stubs = 11
	assert.Equal(t, 11, len(result.FilesCreated))
}
