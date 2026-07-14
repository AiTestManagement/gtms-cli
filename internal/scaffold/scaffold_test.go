package scaffold

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aitestmanagement/gtms-cli/internal/config"
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
		Preset:      PresetBats,
		Force:       false,
	})

	require.NoError(t, err)
	require.NotNil(t, result)

	// Config file should exist
	assert.FileExists(t, filepath.Join(dir, "gtms.config"))
	assert.Contains(t, result.FilesCreated, "gtms.config")

	// Task directories should exist
	for _, status := range []string{"pending", "in-progress", "in-review", "complete", "error"} {
		assert.DirExists(t, filepath.Join(dir, "gtms/tasks", status))
		assert.FileExists(t, filepath.Join(dir, "gtms/tasks", status, ".gitkeep"))
	}

	// test cases dir should exist
	assert.DirExists(t, filepath.Join(dir, "gtms/test/cases"))
	assert.DirExists(t, filepath.Join(dir, "gtms/test", "guides"))

	// automation dirs should exist
	// CON-023 / ENH-145: wiring/ replaces the legacy records/ directory.
	assert.DirExists(t, filepath.Join(dir, "gtms/automation", "wiring"))
	assert.DirExists(t, filepath.Join(dir, "gtms/automation", "specs"))

	// execution dir should exist
	assert.DirExists(t, filepath.Join(dir, "gtms/execution"))

	// Minimal preset should NOT create prompts dir or prompt templates
	assert.NoDirExists(t, filepath.Join(dir, "gtms/automation", "prompts"))
	assert.NoDirExists(t, filepath.Join(dir, "gtms/test/cases", "prompts"))

	// ENH-119: the starter test-case template guide ships with every preset
	// (including minimal) because it documents the skeleton create adapter,
	// which itself ships under all presets.
	assert.FileExists(t, filepath.Join(dir, "gtms/test", "guides", "gtms-test-case-authoring-guide.md"))
	assert.Contains(t, result.FilesCreated, "gtms/test/guides/gtms-test-case-authoring-guide.md")

	// ENH-160: manual-create-script adapter should be created under gtms/adapters/
	assert.DirExists(t, filepath.Join(dir, "gtms", "adapters"))
	assert.FileExists(t, filepath.Join(dir, "gtms", "adapters", "manual-create-script.sh"))
	assert.FileExists(t, filepath.Join(dir, "gtms", "adapters", "agent-create-script.sh"))

	// Verify manual-create-script content
	createContent, err := os.ReadFile(filepath.Join(dir, "gtms", "adapters", "manual-create-script.sh"))
	require.NoError(t, err)
	assert.Contains(t, string(createContent), "#!/bin/sh")
	assert.Contains(t, string(createContent), "GTMS_OUTPUT_DIR")
	assert.Contains(t, string(createContent), "GTMS_TC_IDS")
	assert.Contains(t, string(createContent), "GTMS_REFERENCE")
	assert.Contains(t, string(createContent), "GTMS_RESULT_FILE")
	assert.Contains(t, string(createContent), "priority: Medium")
	assert.Contains(t, string(createContent), "type: Functional")
	assert.NotContains(t, string(createContent), "status: draft")

	// ENH-160: All 6 scripts should exist
	assert.FileExists(t, filepath.Join(dir, "gtms", "adapters", "manual-prime-script.sh"))
	assert.FileExists(t, filepath.Join(dir, "gtms", "adapters", "agent-prime-script.sh"))
	assert.FileExists(t, filepath.Join(dir, "gtms", "adapters", "manual-execute-script.sh"))
	assert.FileExists(t, filepath.Join(dir, "gtms", "adapters", "agent-execute-script.sh"))

	// Legacy files should NOT exist
	assert.NoFileExists(t, filepath.Join(dir, "gtms", "adapters", "create-skeleton.sh"))
	assert.NoFileExists(t, filepath.Join(dir, "gtms", "adapters", "agent-skeleton.sh"))
	assert.NoFileExists(t, filepath.Join(dir, "gtms", "adapters", "manual-prime.sh"))
	assert.NoFileExists(t, filepath.Join(dir, "gtms", "adapters", "manual-execute.sh"))

	// ENH-162: BATS automate template should be created
	assert.FileExists(t, filepath.Join(dir, "gtms", "automation", "templates", "bats.template.bats"))
	assert.Contains(t, result.FilesCreated, "gtms/automation/templates/bats.template.bats")
	batsTempl, batsErr := os.ReadFile(filepath.Join(dir, "gtms", "automation", "templates", "bats.template.bats"))
	require.NoError(t, batsErr)
	assert.Contains(t, string(batsTempl), "${TESTCASE_ID}")
	assert.Contains(t, string(batsTempl), "${PROJECT_ROOT_DEPTH}")
	assert.Contains(t, string(batsTempl), "#!/usr/bin/env bats")
	assert.Contains(t, string(batsTempl), "common-setup.bash")
	assert.Contains(t, string(batsTempl), `skip "skeleton -- not yet implemented"`)

	// ENH-162: Playwright template should NOT be created for bats preset
	assert.NoFileExists(t, filepath.Join(dir, "gtms", "automation", "templates", "playwright.template.spec.ts"))

	// ENH-127: BATS execute adapter and TAP classifier should be created
	assert.FileExists(t, filepath.Join(dir, "gtms", "adapters", "bats-runner.sh"))
	assert.DirExists(t, filepath.Join(dir, "gtms", "adapters", "lib"))
	assert.FileExists(t, filepath.Join(dir, "gtms", "adapters", "lib", "bats-tap.sh"))

	// Verify BATS runner content
	batsRunnerContent, err := os.ReadFile(filepath.Join(dir, "gtms", "adapters", "bats-runner.sh"))
	require.NoError(t, err)
	assert.Contains(t, string(batsRunnerContent), "#!/bin/sh")
	assert.Contains(t, string(batsRunnerContent), "GTMS_ARTEFACT_FILE")
	assert.Contains(t, string(batsRunnerContent), "GTMS_RESULT_FILE")
	assert.Contains(t, string(batsRunnerContent), "classify_bats_status")

	// Verify TAP classifier content
	batsTapContent, err := os.ReadFile(filepath.Join(dir, "gtms", "adapters", "lib", "bats-tap.sh"))
	require.NoError(t, err)
	assert.Contains(t, string(batsTapContent), "classify_bats_status")
	assert.Contains(t, string(batsTapContent), "skipped")

	// BUG-027 originally removed VSCode snippets because TC files were
	// skeleton-generated (not hand-written). CON-020 reverses this:
	// manual result files ARE hand-edited YAML, so snippet support is
	// appropriate for the result-file authoring flow.
	assert.FileExists(t, filepath.Join(dir, ".vscode", "gtms.code-snippets"))

	// Create script should use correct frontmatter field names (BUG-027, Findings 6+11)
	assert.Contains(t, string(createContent), "test_case_id:")
	assert.NotContains(t, string(createContent), "\nid: ${ID}")
	assert.Contains(t, string(createContent), "requirement:")
	assert.NotContains(t, string(createContent), "source: ${GTMS_REFERENCE}")

	// ENH-160: Verify config has built-in defaults, script slots registered
	cfg, err := config.LoadFromFile(filepath.Join(dir, "gtms.config"))
	require.NoError(t, err)
	require.NotNil(t, cfg.Adapters["create"])
	require.NotNil(t, cfg.Adapters["create"]["manual-create-script"])
	assert.Equal(t, "sync", cfg.Adapters["create"]["manual-create-script"].Mode)
	assert.Equal(t, "gtms/adapters/manual-create-script.sh", cfg.Adapters["create"]["manual-create-script"].Script)
	require.NotNil(t, cfg.Adapters["create"]["agent-create-script"])
	assert.Equal(t, "manual-create", cfg.Defaults["create"])
	// Legacy slot names should not exist
	assert.Nil(t, cfg.Adapters["create"]["skeleton"])
}

func TestInitManualPreset(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	result, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Manual Project",
		Repo:        "org/manual-repo",
		Preset:      PresetManual,
		Force:       false,
	})

	require.NoError(t, err)
	require.NotNil(t, result)

	// Config file should exist
	assert.FileExists(t, filepath.Join(dir, "gtms.config"))

	// Guide file should be created (all presets)
	assert.FileExists(t, filepath.Join(dir, "gtms/test", "guides", "gtms-test-case-authoring-guide.md"))

	// Manual preset should NOT create BATS assets
	assert.NoFileExists(t, filepath.Join(dir, "gtms", "adapters", "bats-runner.sh"))
	assert.NoDirExists(t, filepath.Join(dir, "gtms", "adapters", "lib"))

	// Manual preset should NOT create Playwright assets
	assert.NoFileExists(t, filepath.Join(dir, "gtms", "adapters", "playwright-runner.sh"))

	// ENH-162: Manual preset should NOT create automation templates directory
	assert.NoDirExists(t, filepath.Join(dir, "gtms", "automation", "templates"))

	// No prompt dirs (no prompt-based presets anymore)
	assert.NoDirExists(t, filepath.Join(dir, "gtms/test/cases", "prompts"))
	assert.NoDirExists(t, filepath.Join(dir, "gtms/automation", "prompts"))

	// ENH-160: All 6 scripts should exist
	assert.FileExists(t, filepath.Join(dir, "gtms", "adapters", "manual-create-script.sh"))
	assert.FileExists(t, filepath.Join(dir, "gtms", "adapters", "agent-create-script.sh"))
	assert.FileExists(t, filepath.Join(dir, "gtms", "adapters", "manual-prime-script.sh"))
	assert.FileExists(t, filepath.Join(dir, "gtms", "adapters", "agent-prime-script.sh"))
	assert.FileExists(t, filepath.Join(dir, "gtms", "adapters", "manual-execute-script.sh"))
	assert.FileExists(t, filepath.Join(dir, "gtms", "adapters", "agent-execute-script.sh"))

	// Legacy files should NOT exist
	assert.NoFileExists(t, filepath.Join(dir, "gtms", "adapters", "create-skeleton.sh"))
	assert.NoFileExists(t, filepath.Join(dir, "gtms", "adapters", "agent-skeleton.sh"))

	// VSCode snippets still created (manual result authoring)
	assert.FileExists(t, filepath.Join(dir, ".vscode", "gtms.code-snippets"))

	// ENH-160: Verify config has built-in defaults
	cfg, err := config.LoadFromFile(filepath.Join(dir, "gtms.config"))
	require.NoError(t, err)
	require.NotNil(t, cfg.Adapters["create"]["manual-create-script"])
	require.NotNil(t, cfg.Adapters["create"]["agent-create-script"])
	assert.Equal(t, "manual-create", cfg.Defaults["create"])
	assert.Equal(t, "manual-prime", cfg.Defaults["prime"])
	assert.Equal(t, "manual-execute", cfg.Defaults["execute"])

	// ENH-160: manual-execute-script adapter registered (not manual-execute)
	manualExecScript := cfg.Adapters["execute"]["manual-execute-script"]
	require.NotNil(t, manualExecScript)
	assert.Equal(t, "sync", manualExecScript.Mode)
	assert.Equal(t, "manual", manualExecScript.Framework)

	// Legacy slot names should NOT exist in config
	assert.Nil(t, cfg.Adapters["create"]["skeleton"])
	assert.Nil(t, cfg.Adapters["execute"]["manual-execute"])
	assert.Nil(t, cfg.Adapters["prime"]["manual-prime"])

	// No bats-runner in manual preset config
	assert.Nil(t, cfg.Adapters["execute"]["bats-runner"])
}

func TestInitPlaywrightPreset(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	result, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Playwright Project",
		Repo:        "org/pw-repo",
		Preset:      PresetPlaywright,
		Force:       false,
	})

	require.NoError(t, err)
	require.NotNil(t, result)

	// Config file should exist
	assert.FileExists(t, filepath.Join(dir, "gtms.config"))

	// Guide file should be created (all presets)
	assert.FileExists(t, filepath.Join(dir, "gtms/test", "guides", "gtms-test-case-authoring-guide.md"))

	// Playwright preset installs playwright-runner.sh
	assert.FileExists(t, filepath.Join(dir, "gtms", "adapters", "playwright-runner.sh"))

	// Verify playwright runner content
	pwContent, err := os.ReadFile(filepath.Join(dir, "gtms", "adapters", "playwright-runner.sh"))
	require.NoError(t, err)
	assert.Contains(t, string(pwContent), "npx playwright test")
	assert.Contains(t, string(pwContent), "GTMS_RESULT_FILE")
	assert.Contains(t, string(pwContent), "GTMS_ARTEFACT_FILE")

	// ENH-162: Playwright automate template should be created
	assert.FileExists(t, filepath.Join(dir, "gtms", "automation", "templates", "playwright.template.spec.ts"))
	assert.Contains(t, result.FilesCreated, "gtms/automation/templates/playwright.template.spec.ts")
	pwTempl, pwTmplErr := os.ReadFile(filepath.Join(dir, "gtms", "automation", "templates", "playwright.template.spec.ts"))
	require.NoError(t, pwTmplErr)
	assert.Contains(t, string(pwTempl), "${TESTCASE_ID}")
	assert.Contains(t, string(pwTempl), "import { test, expect }")
	assert.Contains(t, string(pwTempl), `test.skip(true, 'skeleton -- not yet implemented')`)

	// ENH-162: BATS template should NOT be created for playwright preset
	assert.NoFileExists(t, filepath.Join(dir, "gtms", "automation", "templates", "bats.template.bats"))

	// Playwright preset should NOT install BATS assets (BUG-111 AC #16)
	assert.NoFileExists(t, filepath.Join(dir, "gtms", "adapters", "bats-runner.sh"))
	assert.NoFileExists(t, filepath.Join(dir, "gtms", "adapters", "lib", "bats-tap.sh"))

	// ENH-160: All 6 common scripts should be present
	assert.FileExists(t, filepath.Join(dir, "gtms", "adapters", "manual-create-script.sh"))
	assert.FileExists(t, filepath.Join(dir, "gtms", "adapters", "agent-create-script.sh"))
	assert.FileExists(t, filepath.Join(dir, "gtms", "adapters", "manual-prime-script.sh"))
	assert.FileExists(t, filepath.Join(dir, "gtms", "adapters", "agent-prime-script.sh"))
	assert.FileExists(t, filepath.Join(dir, "gtms", "adapters", "manual-execute-script.sh"))
	assert.FileExists(t, filepath.Join(dir, "gtms", "adapters", "agent-execute-script.sh"))

	// Legacy files should NOT exist
	assert.NoFileExists(t, filepath.Join(dir, "gtms", "adapters", "create-skeleton.sh"))
	assert.NoFileExists(t, filepath.Join(dir, "gtms", "adapters", "agent-skeleton.sh"))

	// VSCode snippets still created (manual result authoring)
	assert.FileExists(t, filepath.Join(dir, ".vscode", "gtms.code-snippets"))

	// ENH-160: Verify config has built-in defaults for create/prime, playwright-runner for execute
	cfg, err := config.LoadFromFile(filepath.Join(dir, "gtms.config"))
	require.NoError(t, err)
	require.NotNil(t, cfg.Adapters["create"]["manual-create-script"])
	require.NotNil(t, cfg.Adapters["create"]["agent-create-script"])
	assert.Equal(t, "manual-create", cfg.Defaults["create"])
	assert.Equal(t, "manual-prime", cfg.Defaults["prime"])
	assert.Equal(t, "playwright-runner", cfg.Defaults["execute"])

	// Playwright-runner adapter registered
	pwExec := cfg.Adapters["execute"]["playwright-runner"]
	require.NotNil(t, pwExec)
	assert.Equal(t, "sync", pwExec.Mode)
	assert.Equal(t, "playwright", pwExec.Framework)
	assert.Equal(t, "gtms/adapters/playwright-runner.sh", pwExec.Script)

	// No bats-runner in playwright preset config
	assert.Nil(t, cfg.Adapters["execute"]["bats-runner"])
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
		Preset:      PresetBats,
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
		Preset:      PresetBats,
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
	assert.Contains(t, err.Error(), "unknown preset")
}

func TestInitExistingDirectoriesNoError(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	// Create some directories in advance
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "gtms/tasks", "pending"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "gtms/test/cases"), 0o755))

	// Put a file in pending to verify it is not overwritten
	existingFile := filepath.Join(dir, "gtms/tasks", "pending", "existing.md")
	require.NoError(t, os.WriteFile(existingFile, []byte("keep me"), 0o644))

	// Init should succeed without errors
	_, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Test",
		Repo:        "org/test",
		Preset:      PresetBats,
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
	pendingDir := filepath.Join(dir, "gtms/tasks", "pending")
	require.NoError(t, os.MkdirAll(pendingDir, 0o755))
	gitkeepPath := filepath.Join(pendingDir, ".gitkeep")
	require.NoError(t, os.WriteFile(gitkeepPath, []byte("custom content"), 0o644))

	_, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Test",
		Repo:        "org/test",
		Preset:      PresetBats,
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
		Preset:      PresetBats,
		Force:       false,
	})
	require.NoError(t, err)

	// Load the generated config through the config package validator
	cfg, err := config.LoadFromFile(filepath.Join(dir, "gtms.config"))
	require.NoError(t, err)
	assert.Equal(t, "Validation Test", cfg.Project.Name)
	assert.Equal(t, "org/validation", cfg.Project.Repo)

	// ENH-127: minimal preset must scaffold a working out-of-the-box BATS
	// execute pipeline -- bats-runner registered as a Tier 2 execute adapter
	// and set as the default.
	bats := cfg.Adapters["execute"]["bats-runner"]
	require.NotNil(t, bats, "minimal preset should register bats-runner under execute")
	assert.Equal(t, "sync", bats.Mode)
	assert.Equal(t, "gtms/adapters/bats-runner.sh", bats.Script)
	assert.Equal(t, "bats", bats.Framework)
	assert.Equal(t, "test/acceptance", bats.OutputDir)
	// ENH-127: minimal preset's defaults.execute is bats-runner (BATS-first
	// out-of-the-box). ENH-133 originally flipped this to manual-execute but
	// reverted post-CI: the change collided with ENH-127 and broke 31 BATS
	// fixtures whose intent was to exercise the shipped bats-runner wrapper.
	// Manual execute is still available on minimal -- opt in via
	// `--adapter manual-execute` (consistent with claude / github presets).
	assert.Equal(t, "bats-runner", cfg.Defaults["execute"])

	// ENH-160: manual-execute-script adapter is registered (opt-in via
	// --adapter manual-execute-script). MUST NOT be the default.
	manualExecScript := cfg.Adapters["execute"]["manual-execute-script"]
	require.NotNil(t, manualExecScript, "bats preset should register manual-execute-script under execute")
	assert.Equal(t, "sync", manualExecScript.Mode)
	assert.Equal(t, "gtms/adapters/manual-execute-script.sh", manualExecScript.Script)
	assert.Equal(t, "manual", manualExecScript.Framework)

	// ENH-160: agent-execute-script also registered as dormant
	agentExecScript := cfg.Adapters["execute"]["agent-execute-script"]
	require.NotNil(t, agentExecScript, "bats preset should register agent-execute-script under execute")
	assert.Equal(t, "manual", agentExecScript.Framework)

	// ENH-160: defaults.create flipped to built-in
	assert.Equal(t, "manual-create", cfg.Defaults["create"])
	assert.Equal(t, "manual-prime", cfg.Defaults["prime"])

	// BUG-123: bats preset wires automate so `gtms automate <tc>` works out of
	// the box. Default is agent-automate (Tier 0 built-in) carrying framework: bats.
	assert.Equal(t, "agent-automate", cfg.Defaults["automate"])
	batsAutomate := cfg.Adapters["automate"]["agent-automate"]
	require.NotNil(t, batsAutomate, "bats preset should register agent-automate under automate")
	assert.Equal(t, "sync", batsAutomate.Mode)
	assert.Equal(t, "bats", batsAutomate.Framework)
	assert.Empty(t, batsAutomate.Script, "agent-automate is a Tier 0 built-in (no script)")
	assert.Empty(t, batsAutomate.Command, "agent-automate is a Tier 0 built-in (no command)")
}

func TestConfigValidationManualPreset(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	_, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Manual Validation",
		Repo:        "org/manual-val",
		Preset:      PresetManual,
		Force:       false,
	})
	require.NoError(t, err)

	cfg, err := config.LoadFromFile(filepath.Join(dir, "gtms.config"))
	require.NoError(t, err)
	assert.Equal(t, "Manual Validation", cfg.Project.Name)
	assert.Equal(t, "org/manual-val", cfg.Project.Repo)

	// ENH-160: Verify adapter structure uses new slot names
	assert.NotNil(t, cfg.Adapters["create"]["manual-create-script"])
	assert.Equal(t, "sync", cfg.Adapters["create"]["manual-create-script"].Mode)
	assert.NotNil(t, cfg.Adapters["create"]["agent-create-script"])

	// ENH-160: manual-execute-script registered (not manual-execute)
	manualExecScript := cfg.Adapters["execute"]["manual-execute-script"]
	require.NotNil(t, manualExecScript, "manual preset should register manual-execute-script under execute")
	assert.Equal(t, "sync", manualExecScript.Mode)
	assert.Equal(t, "gtms/adapters/manual-execute-script.sh", manualExecScript.Script)
	assert.Equal(t, "manual", manualExecScript.Framework)

	// No bats-runner or playwright-runner in manual preset
	assert.Nil(t, cfg.Adapters["execute"]["bats-runner"])
	assert.Nil(t, cfg.Adapters["execute"]["playwright-runner"])

	// ENH-160: Legacy slot names should NOT exist
	assert.Nil(t, cfg.Adapters["create"]["skeleton"])
	assert.Nil(t, cfg.Adapters["execute"]["manual-execute"])

	// ENH-160: Verify defaults point to built-ins
	assert.Equal(t, "manual-create", cfg.Defaults["create"])
	assert.Equal(t, "manual-prime", cfg.Defaults["prime"])
	assert.Equal(t, "manual-execute", cfg.Defaults["execute"])

	// BUG-123: manual preset wires automate to manual-automate (framework: manual)
	// so `gtms automate <tc>` returns the prime/execute diagnostic instead of the
	// generic "No default adapter configured" error.
	assert.Equal(t, "manual-automate", cfg.Defaults["automate"])
	manualAutomate := cfg.Adapters["automate"]["manual-automate"]
	require.NotNil(t, manualAutomate, "manual preset should register manual-automate under automate")
	assert.Equal(t, "sync", manualAutomate.Mode)
	assert.Equal(t, "manual", manualAutomate.Framework)
	assert.Empty(t, manualAutomate.Script, "manual-automate is a Tier 0 built-in (no script)")
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

func TestConfigValidationPlaywrightPreset(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	_, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Playwright Validation",
		Repo:        "org/pw-val",
		Preset:      PresetPlaywright,
		Force:       false,
	})
	require.NoError(t, err)

	cfg, err := config.LoadFromFile(filepath.Join(dir, "gtms.config"))
	require.NoError(t, err)
	assert.Equal(t, "Playwright Validation", cfg.Project.Name)
	assert.Equal(t, "org/pw-val", cfg.Project.Repo)

	// Verify playwright-runner adapter registered
	pw := cfg.Adapters["execute"]["playwright-runner"]
	require.NotNil(t, pw)
	assert.Equal(t, "sync", pw.Mode)
	assert.Equal(t, "playwright", pw.Framework)
	assert.Equal(t, "gtms/adapters/playwright-runner.sh", pw.Script)

	// ENH-160: manual-execute-script also registered (all presets)
	manualExecScript := cfg.Adapters["execute"]["manual-execute-script"]
	require.NotNil(t, manualExecScript, "playwright preset should register manual-execute-script under execute")
	assert.Equal(t, "sync", manualExecScript.Mode)
	assert.Equal(t, "manual", manualExecScript.Framework)

	// No bats-runner in playwright preset
	assert.Nil(t, cfg.Adapters["execute"]["bats-runner"])

	// ENH-160: Verify defaults point to built-ins
	assert.Equal(t, "manual-create", cfg.Defaults["create"])
	assert.Equal(t, "manual-prime", cfg.Defaults["prime"])
	assert.Equal(t, "playwright-runner", cfg.Defaults["execute"])

	// BUG-123: playwright preset wires automate so `gtms automate <tc>` works out
	// of the box. Default is agent-automate (Tier 0 built-in) carrying framework: playwright.
	assert.Equal(t, "agent-automate", cfg.Defaults["automate"])
	pwAutomate := cfg.Adapters["automate"]["agent-automate"]
	require.NotNil(t, pwAutomate, "playwright preset should register agent-automate under automate")
	assert.Equal(t, "sync", pwAutomate.Mode)
	assert.Equal(t, "playwright", pwAutomate.Framework)
	assert.Empty(t, pwAutomate.Script, "agent-automate is a Tier 0 built-in (no script)")
}

func TestCreateDirectoriesReturnsCorrectList(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	dirs, err := CreateDirectories(dir, PresetBats)
	require.NoError(t, err)

	expected := []string{
		"gtms/tasks/pending",
		"gtms/tasks/in-progress",
		"gtms/tasks/in-review",
		"gtms/tasks/complete",
		"gtms/tasks/error",
		"gtms/test/cases",
		"gtms/test/templates",
		"gtms/test/guides",
		"gtms/test/prompts",
		"gtms/automation/wiring",
		"gtms/automation/specs",
		"gtms/scripts",
		"gtms/execution",
		"gtms/manual/records",
		"gtms/manual/templates",
		"gtms/schemas",
		"gtms/adapters",
	}
	assert.Equal(t, expected, dirs)
}

// BUG-111: No preset creates prompt dirs anymore -- prompts were claude/github specific.
// All presets create gtms/adapters/ for common adapter scripts.
func TestCreateDirectoriesAllPresetsIncludeAdapters(t *testing.T) {
	skipIfShort(t)
	for _, preset := range ValidPresets() {
		t.Run(preset, func(t *testing.T) {
			dir := initGitRepo(t)
			dirs, err := CreateDirectories(dir, preset)
			require.NoError(t, err)
			assert.Contains(t, dirs, "gtms/adapters")
			// ENH-165: scaffold creates gtms/test/prompts/ as a tracked slot.
			assert.Contains(t, dirs, "gtms/test/prompts")
			// Legacy and non-scaffold prompt dirs absent.
			assert.NotContains(t, dirs, "gtms/cases/prompts")
			assert.NotContains(t, dirs, "gtms/test/cases/prompts")
			assert.NotContains(t, dirs, "gtms/automation/prompts")
		})
	}
}

func TestWriteConfigContent(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	path, err := WriteConfig(dir, "My Project", "org/my-repo", PresetBats)
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
	createContent, err := os.ReadFile(filepath.Join(dir, "gtms/test", "prompts", "create-standard.md"))
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
	assert.Equal(t, "gtms/test/guides/gtms-test-case-authoring-guide.md", files[0])

	// Verify content
	content, err := os.ReadFile(filepath.Join(dir, "gtms/test", "guides", "gtms-test-case-authoring-guide.md"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "Test Case Template")
	assert.Contains(t, string(content), "## Test Objective")
	assert.Contains(t, string(content), "## Test Steps")
	assert.Contains(t, string(content), "Principles")
}

// BUG-111: WriteAdapterStubs removed (github stubs no longer exist).
// Replaced by installPresetAssets for preset-owned adapter files.
func TestInstallPresetAssetsBats(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "gtms", "adapters"), 0o755))
	result := &Result{}
	err := installPresetAssets(dir, PresetBats, result)
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(dir, "gtms", "adapters", "bats-runner.sh"))
	assert.FileExists(t, filepath.Join(dir, "gtms", "adapters", "lib", "bats-tap.sh"))
	assert.Contains(t, result.FilesCreated, "gtms/adapters/bats-runner.sh")
	assert.Contains(t, result.FilesCreated, "gtms/adapters/lib/bats-tap.sh")
}

func TestInstallPresetAssetsManualEmpty(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "gtms", "adapters"), 0o755))
	result := &Result{}
	err := installPresetAssets(dir, PresetManual, result)
	require.NoError(t, err)
	assert.Empty(t, result.FilesCreated, "manual preset should not install extra assets")
}

func TestInstallPresetAssetsPlaywright(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "gtms", "adapters"), 0o755))
	result := &Result{}
	err := installPresetAssets(dir, PresetPlaywright, result)
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(dir, "gtms", "adapters", "playwright-runner.sh"))
	assert.Contains(t, result.FilesCreated, "gtms/adapters/playwright-runner.sh")
	// No BATS assets
	assert.NoFileExists(t, filepath.Join(dir, "gtms", "adapters", "bats-runner.sh"))
}

func TestValidPresets(t *testing.T) {
	assert.True(t, IsValidPreset("manual"))
	assert.True(t, IsValidPreset("bats"))
	assert.True(t, IsValidPreset("playwright"))
	assert.False(t, IsValidPreset("minimal"))
	assert.False(t, IsValidPreset("claude"))
	assert.False(t, IsValidPreset("github"))
	assert.False(t, IsValidPreset("invalid"))
	assert.False(t, IsValidPreset(""))
}

func TestIntegrationFullInit(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	// Run full Init with bats preset (replaces old claude integration test)
	result, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Integration Test",
		Repo:        "org/integration",
		Preset:      PresetBats,
		Force:       false,
	})

	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify core directories exist
	expectedDirs := []string{
		"gtms/tasks/pending",
		"gtms/tasks/in-progress",
		"gtms/tasks/in-review",
		"gtms/tasks/complete",
		"gtms/tasks/error",
		"gtms/test/cases",
		"gtms/test/guides",
		"gtms/automation/wiring",
		"gtms/automation/specs",
		"gtms/execution",
	}
	for _, d := range expectedDirs {
		assert.DirExists(t, filepath.Join(dir, filepath.FromSlash(d)), "missing dir: %s", d)
	}

	// Verify expected files
	expectedFiles := []string{
		"gtms.config",
		"gtms/test/guides/gtms-test-case-authoring-guide.md",
		"gtms/adapters/bats-runner.sh",
		"gtms/adapters/lib/bats-tap.sh",
	}
	for _, f := range expectedFiles {
		assert.FileExists(t, filepath.Join(dir, filepath.FromSlash(f)), "missing file: %s", f)
	}

	// Verify config loads and validates
	cfg, err := config.LoadFromFile(filepath.Join(dir, "gtms.config"))
	require.NoError(t, err)
	assert.Equal(t, "Integration Test", cfg.Project.Name)
	assert.Equal(t, "org/integration", cfg.Project.Repo)
	assert.Equal(t, "manual-create", cfg.Defaults["create"])
	assert.Equal(t, "bats-runner", cfg.Defaults["execute"])

	// Verify result has accurate tracking
	assert.Equal(t, filepath.Join(dir, "gtms.config"), result.ConfigPath)
	assert.True(t, len(result.DirsCreated) >= 8, "should have at least 8 dirs created")
	assert.True(t, len(result.FilesCreated) >= 5, "should have at least 5 files created")
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
		Preset:      PresetBats,
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
		Preset:      PresetBats,
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
		Preset:      PresetBats,
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
		Preset:      PresetManual,
		Force:       false,
	})
	require.NoError(t, err)

	cfg, err := config.LoadFromFile(filepath.Join(dir, "gtms.config"))
	require.NoError(t, err)
	assert.Equal(t, `My "Quoted" Project`, cfg.Project.Name)
	assert.Equal(t, `org/"special"`, cfg.Project.Repo)
}

func TestIntegrationPlaywrightFullInit(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	result, err := Init(Options{
		ProjectRoot: dir,
		Name:        "PW Integration",
		Repo:        "org/pw-int",
		Preset:      PresetPlaywright,
		Force:       false,
	})

	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify config loads and validates
	cfg, err := config.LoadFromFile(filepath.Join(dir, "gtms.config"))
	require.NoError(t, err)
	assert.Equal(t, "PW Integration", cfg.Project.Name)
	assert.Equal(t, "manual-create", cfg.Defaults["create"])
	assert.Equal(t, "playwright-runner", cfg.Defaults["execute"])

	// Verify playwright runner exists
	assert.FileExists(t, filepath.Join(dir, "gtms", "adapters", "playwright-runner.sh"))

	// Verify guide file exists
	assert.FileExists(t, filepath.Join(dir, "gtms/test", "guides", "gtms-test-case-authoring-guide.md"))

	// ENH-161/ENH-162: Total files: 1 sentinel + 1 config + 1 guide
	// + 1 manual-create-script + 1 agent-create-script + 1 playwright-runner
	// + 1 playwright-automate-template (ENH-162)
	// + 1 manual-result-template + 1 manual-tc-template + 1 agent-tc-template
	// + 1 agent-result-template + 1 AGENTS-SNIPPET.md (ENH-183) + 1 schema
	// + 1 manual-prime-script + 1 agent-prime-script
	// + 1 manual-execute-script + 1 agent-execute-script + 1 tasks-readme
	// + 1 vscode-settings + 1 vscode-extensions + 1 vscode-snippets + 1 guidance
	// + 6 starter skills (5 SKILL.md + 1 README.md) = 28
	assert.Equal(t, 28, len(result.FilesCreated),
		"playwright preset should create 28 files; got: %v", result.FilesCreated)
}

func TestInitCreatesGuidanceYAML(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	result, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Guidance Test",
		Repo:        "org/guidance",
		Preset:      PresetBats,
		Force:       false,
	})

	require.NoError(t, err)

	// .gtms/guidance.yaml should be created
	guidancePath := filepath.Join(dir, ".gtms", "guidance.yaml")
	assert.FileExists(t, guidancePath)

	// Should be tracked in FilesCreated
	assert.Contains(t, result.FilesCreated, ".gtms/guidance.yaml")
}

func TestInitSkipsExistingGuidanceYAML(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	// Pre-create .gtms/guidance.yaml with custom content
	gtmsDir := filepath.Join(dir, ".gtms")
	require.NoError(t, os.MkdirAll(gtmsDir, 0755))
	customContent := "create: |\n  custom guidance\n"
	require.NoError(t, os.WriteFile(filepath.Join(gtmsDir, "guidance.yaml"), []byte(customContent), 0644))

	result, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Skip Test",
		Repo:        "org/skip",
		Preset:      PresetBats,
		Force:       false,
	})

	require.NoError(t, err)

	// Should be in FilesSkipped, not FilesCreated
	assert.Contains(t, result.FilesSkipped, ".gtms/guidance.yaml")
	assert.NotContains(t, result.FilesCreated, ".gtms/guidance.yaml")

	// Original content should be preserved
	content, err := os.ReadFile(filepath.Join(gtmsDir, "guidance.yaml"))
	require.NoError(t, err)
	assert.Equal(t, customContent, string(content))
}

// --- Demo seeding tests ---

func TestDemoSeedFreshProject(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	// Run minimal init first (DemoSeed requires existing config)
	_, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Demo Test",
		Repo:        "org/demo",
		Preset:      PresetBats,
		Force:       false,
	})
	require.NoError(t, err)

	// Seed demo data
	result, err := DemoSeed(dir)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify demo files created
	assert.FileExists(t, filepath.Join(dir, "_demo", "login-feature.md"))
	assert.FileExists(t, filepath.Join(dir, "_demo", "adapters", "create-demo.sh"))
	assert.FileExists(t, filepath.Join(dir, "_demo", "adapters", "automate-demo-sh.sh"))
	assert.FileExists(t, filepath.Join(dir, "_demo", "adapters", "automate-demo-cmd.sh"))
	assert.FileExists(t, filepath.Join(dir, "gtms/test", "guides", "getting-started-with-ai.md"))

	// Verify files tracked in result
	assert.Contains(t, result.FilesCreated, "_demo/login-feature.md")
	assert.Contains(t, result.FilesCreated, "_demo/adapters/create-demo.sh")
	assert.Contains(t, result.FilesCreated, "_demo/adapters/automate-demo-sh.sh")
	assert.Contains(t, result.FilesCreated, "_demo/adapters/automate-demo-cmd.sh")
	assert.Contains(t, result.FilesCreated, "gtms/test/guides/getting-started-with-ai.md")
	assert.True(t, result.ConfigModified)
	assert.True(t, result.GuidanceModified)
}

func TestDemoSeedConfigValidation(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	_, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Demo Validation",
		Repo:        "org/demo-val",
		Preset:      PresetBats,
		Force:       false,
	})
	require.NoError(t, err)

	_, err = DemoSeed(dir)
	require.NoError(t, err)

	// Config must pass validation after demo merge
	cfg, err := config.LoadFromFile(filepath.Join(dir, "gtms.config"))
	require.NoError(t, err)

	// Verify demo adapters exist
	assert.NotNil(t, cfg.Adapters["create"]["demo"])
	assert.Equal(t, "sync", cfg.Adapters["create"]["demo"].Mode)
	assert.Equal(t, "_demo/adapters/create-demo.sh", cfg.Adapters["create"]["demo"].Script)

	assert.NotNil(t, cfg.Adapters["automate"]["demo-sh"])
	assert.Equal(t, "sync", cfg.Adapters["automate"]["demo-sh"].Mode)

	assert.NotNil(t, cfg.Adapters["automate"]["demo-cmd"])
	assert.Equal(t, "sync", cfg.Adapters["automate"]["demo-cmd"].Mode)

	assert.NotNil(t, cfg.Adapters["execute"]["demo-sh"])
	assert.Equal(t, "sync", cfg.Adapters["execute"]["demo-sh"].Mode)
	assert.Equal(t, "sh {artefact_file}", cfg.Adapters["execute"]["demo-sh"].Command)

	assert.NotNil(t, cfg.Adapters["execute"]["demo-cmd"])
	assert.Equal(t, "sync", cfg.Adapters["execute"]["demo-cmd"].Mode)
	assert.Equal(t, "cmd /c {artefact_file}", cfg.Adapters["execute"]["demo-cmd"].Command)

	// Verify DemoSeeded flag is set
	assert.True(t, cfg.DemoSeeded)

	// Verify defaults were NOT modified by demo seed (built-in default from bats preset preserved)
	assert.Equal(t, "manual-create", cfg.Defaults["create"], "demo should preserve existing create default")
}

func TestDemoSeedExistingProjectPreservesAdapters(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	// Init with bats preset (has existing adapters)
	_, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Existing Adapters",
		Repo:        "org/existing",
		Preset:      PresetBats,
		Force:       false,
	})
	require.NoError(t, err)

	_, err = DemoSeed(dir)
	require.NoError(t, err)

	cfg, err := config.LoadFromFile(filepath.Join(dir, "gtms.config"))
	require.NoError(t, err)

	// Original adapters should still exist
	assert.NotNil(t, cfg.Adapters["create"]["manual-create-script"], "existing create adapter should be preserved")
	assert.NotNil(t, cfg.Adapters["execute"]["bats-runner"], "existing execute adapter should be preserved")

	// Demo adapters should also exist
	assert.NotNil(t, cfg.Adapters["create"]["demo"])
	assert.NotNil(t, cfg.Adapters["automate"]["demo-sh"])
	assert.NotNil(t, cfg.Adapters["execute"]["demo-sh"])

	// Defaults should still point to original adapters
	assert.Equal(t, "manual-create", cfg.Defaults["create"])
	assert.Equal(t, "bats-runner", cfg.Defaults["execute"])
}

func TestDemoSeedCreatesBackups(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	_, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Backup Test",
		Repo:        "org/backup",
		Preset:      PresetBats,
		Force:       false,
	})
	require.NoError(t, err)

	_, err = DemoSeed(dir)
	require.NoError(t, err)

	// Backup of config should exist
	assert.FileExists(t, filepath.Join(dir, "gtms.config.bak"))

	// Backup of guidance should exist
	assert.FileExists(t, filepath.Join(dir, ".gtms", "guidance.yaml.bak"))
}

func TestDemoSeedGuidanceUpdated(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	_, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Guidance Test",
		Repo:        "org/guidance",
		Preset:      PresetBats,
		Force:       false,
	})
	require.NoError(t, err)

	_, err = DemoSeed(dir)
	require.NoError(t, err)

	// Read guidance.yaml and check init key was updated
	content, err := os.ReadFile(filepath.Join(dir, ".gtms", "guidance.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "login-feature.md")
	assert.Contains(t, string(content), "--adapter demo")
}

func TestDemoAdapterScriptsHaveShebang(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	_, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Shebang Test",
		Repo:        "org/shebang",
		Preset:      PresetBats,
		Force:       false,
	})
	require.NoError(t, err)

	_, err = DemoSeed(dir)
	require.NoError(t, err)

	scripts := []string{
		"_demo/adapters/create-demo.sh",
		"_demo/adapters/automate-demo-sh.sh",
		"_demo/adapters/automate-demo-cmd.sh",
	}
	for _, s := range scripts {
		content, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(s)))
		require.NoError(t, err, "reading %s", s)
		assert.True(t, strings.HasPrefix(string(content), "#!/bin/sh"), "script %s should start with #!/bin/sh", s)
	}

	// Check permissions on Unix
	if runtime.GOOS != "windows" {
		for _, s := range scripts {
			info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(s)))
			require.NoError(t, err)
			assert.True(t, info.Mode()&0o111 != 0, "script %s should be executable", s)
		}
	}
}

func TestDemoSeedSkipsExistingFiles(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	_, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Skip Demo",
		Repo:        "org/skip-demo",
		Preset:      PresetBats,
		Force:       false,
	})
	require.NoError(t, err)

	// Pre-create one demo file with custom content
	demoDir := filepath.Join(dir, "_demo")
	require.NoError(t, os.MkdirAll(demoDir, 0o755))
	customContent := "# My custom requirement\n"
	require.NoError(t, os.WriteFile(filepath.Join(demoDir, "login-feature.md"), []byte(customContent), 0o644))

	result, err := DemoSeed(dir)
	require.NoError(t, err)

	// login-feature.md should be skipped
	assert.Contains(t, result.FilesSkipped, "_demo/login-feature.md")
	assert.NotContains(t, result.FilesCreated, "_demo/login-feature.md")

	// Verify custom content preserved
	content, err := os.ReadFile(filepath.Join(demoDir, "login-feature.md"))
	require.NoError(t, err)
	assert.Equal(t, customContent, string(content))

	// Other demo files should still be created
	assert.Contains(t, result.FilesCreated, "_demo/adapters/create-demo.sh")
}

func TestDemoRequirementContent(t *testing.T) {
	// Pure unit test -- no git needed
	assert.Contains(t, demoLoginRequirement, "Login Feature")
	assert.Contains(t, demoLoginRequirement, "REQ-LOGIN-001")
	assert.Contains(t, demoLoginRequirement, "REQ-LOGIN-002")
	assert.Contains(t, demoLoginRequirement, "REQ-LOGIN-003")
}

func TestDemoCreateScriptContent(t *testing.T) {
	// Pure unit test
	assert.Contains(t, demoCreateScript, "GTMS_OUTPUT_DIR")
	assert.Contains(t, demoCreateScript, "GTMS_TC_IDS")
	assert.Contains(t, demoCreateScript, "GTMS_RESULT_FILE")
	assert.Contains(t, demoCreateScript, "login-valid-credentials")
	assert.Contains(t, demoCreateScript, "login-invalid-password")
	assert.Contains(t, demoCreateScript, "login-empty-fields")
}

// --- REV-061: Skeleton script uses GTMS_TC_NAME ---

func TestSkeletonCreateScript_UsesGTMS_TC_NAME(t *testing.T) {
	// Verify the skeleton template correctly references GTMS_TC_NAME
	assert.Contains(t, manualCreateScript, "${GTMS_TC_NAME}")
	assert.Contains(t, manualCreateScript, "GTMS_TC_NAME")
	// Should use the name in the filename
	assert.Contains(t, manualCreateScript, "${ID}-${GTMS_TC_NAME}.md")
}

// --- ENH-135: BUG-027 Finding 1 reversed (CON-020) ---

func TestVSCodeSnippetsShipped(t *testing.T) {
	// BUG-027 originally removed snippets for skeleton TC files.
	// CON-020 reverses this: manual result files are hand-edited YAML,
	// so snippet support is appropriate for the result-file authoring flow.
	assert.Contains(t, vscodeGtmsSnippets, "gtms-pass")
	assert.Contains(t, vscodeGtmsSnippets, "gtms-fail")
	assert.Contains(t, vscodeGtmsSnippets, "gtms-skip")
	assert.Contains(t, vscodeGtmsSnippets, "gtms-step-pass")
	assert.Contains(t, vscodeGtmsSnippets, "gtms-step-fail")
	assert.Contains(t, vscodeGtmsSnippets, "gtms-step-skip")
}

// --- ENH-107: ensureGitignore baseline coverage ---

func TestEnsureGitignore_CreatesFileWhenAbsent(t *testing.T) {
	dir := t.TempDir()

	action, err := ensureGitignore(dir)
	require.NoError(t, err)
	assert.Equal(t, GitignoreCreated, action)

	content, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	require.NoError(t, err)
	expected := ".gtms/\ngtms/execution/attachments/\ngtms/execution/logs/\n"
	assert.Equal(t, expected, string(content))
}

func TestEnsureGitignore_AppendsEntryWhenMissing(t *testing.T) {
	dir := t.TempDir()

	// Pre-create .gitignore without any GTMS entries
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("node_modules/\n"), 0o644))

	action, err := ensureGitignore(dir)
	require.NoError(t, err)
	assert.Equal(t, GitignoreAppended, action)

	content, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "node_modules/\n")
	assert.Contains(t, string(content), ".gtms/\n")
	assert.Contains(t, string(content), "gtms/execution/attachments/\n")
	assert.Contains(t, string(content), "gtms/execution/logs/\n")
}

func TestEnsureGitignore_IdempotentWhenPresent(t *testing.T) {
	dir := t.TempDir()

	original := "node_modules/\n.gtms/\ngtms/execution/attachments/\ngtms/execution/logs/\nbuild/\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(original), 0o644))

	action, err := ensureGitignore(dir)
	require.NoError(t, err)
	assert.Equal(t, GitignoreUnchanged, action)

	content, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	require.NoError(t, err)
	assert.Equal(t, original, string(content), "file should be unchanged when entries already present")
}

func TestEnsureGitignore_HandlesCRLF(t *testing.T) {
	dir := t.TempDir()

	// CRLF line endings with all GTMS entries already present
	crlf := "node_modules/\r\n.gtms/\r\ngtms/execution/attachments/\r\ngtms/execution/logs/\r\nbuild/\r\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(crlf), 0o644))

	action, err := ensureGitignore(dir)
	require.NoError(t, err)
	assert.Equal(t, GitignoreUnchanged, action)

	content, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	require.NoError(t, err)
	// Should be idempotent -- no duplicate entries
	assert.Equal(t, crlf, string(content), "file should be unchanged when entries already present (CRLF)")
}

func TestEnsureGitignore_IdempotentWithWhitespace(t *testing.T) {
	dir := t.TempDir()

	// .gitignore with whitespace around the GTMS entries
	original := "  .gtms/  \n  gtms/execution/attachments/  \n  gtms/execution/logs/  \n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(original), 0o644))

	action, err := ensureGitignore(dir)
	require.NoError(t, err)
	assert.Equal(t, GitignoreUnchanged, action)

	content, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	require.NoError(t, err)
	assert.Equal(t, original, string(content), "file should be unchanged when entries present with whitespace")
}

// TestEnsureGitignore_SentinelOnlyLeftUnchanged guards the ENH-108 contract:
// a project that pre-dates ENH-109 (only the .gtms/ sentinel in .gitignore,
// none of the gtms/execution/ entries that ENH-109 added) must be treated as
// already managed and left byte-for-byte unchanged on `gtms init`. Adding the
// newer entries silently would break ENH-108's idempotency tests in
// test/acceptance/gitignore-action-reporting/.
func TestEnsureGitignore_SentinelOnlyLeftUnchanged(t *testing.T) {
	dir := t.TempDir()

	original := "node_modules/\n.gtms/\ndist/\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(original), 0o644))

	action, err := ensureGitignore(dir)
	require.NoError(t, err)
	assert.Equal(t, GitignoreUnchanged, action)

	content, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	require.NoError(t, err)
	assert.Equal(t, original, string(content), "file should be unchanged when the .gtms/ sentinel is present, even if newer entries are absent")
}

// --- ENH-107: Sentinel preservation (S5, D9) ---

func TestInitForce_PreservesSentinelContent(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	// First init
	_, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Sentinel Test",
		Repo:        "org/sentinel",
		Preset:      PresetBats,
		Force:       false,
	})
	require.NoError(t, err)

	// Write non-empty sentinel content
	sentinelPath := filepath.Join(dir, "gtms", ".gtms-root")
	customContent := "version: 1\n"
	require.NoError(t, os.WriteFile(sentinelPath, []byte(customContent), 0o644))

	// Re-init with --force
	_, err = Init(Options{
		ProjectRoot: dir,
		Name:        "Sentinel Test",
		Repo:        "org/sentinel",
		Preset:      PresetBats,
		Force:       true,
	})
	require.NoError(t, err)

	// Sentinel content must survive byte-for-byte
	content, err := os.ReadFile(sentinelPath)
	require.NoError(t, err)
	assert.Equal(t, customContent, string(content), "non-empty sentinel must survive --force")
}

// --- ENH-107: S3 deliberate reconstruct ---

func TestInit_S3_ExistingGtmsPreservesSubdirs(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	// Pre-create gtms/test/cases/ with a file (simulates S3: existing gtms/ without config)
	casesDir := filepath.Join(dir, "gtms", "test", "cases")
	require.NoError(t, os.MkdirAll(casesDir, 0o755))
	existingFile := filepath.Join(casesDir, "foo.md")
	require.NoError(t, os.WriteFile(existingFile, []byte("keep me"), 0o644))

	// Run init (no config exists, no flat layout)
	result, err := Init(Options{
		ProjectRoot: dir,
		Name:        "S3 Test",
		Repo:        "org/s3",
		Preset:      PresetBats,
		Force:       false,
	})
	require.NoError(t, err)

	// Existing file must be untouched
	content, err := os.ReadFile(existingFile)
	require.NoError(t, err)
	assert.Equal(t, "keep me", string(content))

	// Sentinel should be in FilesRestored (not FilesCreated) because gtms/ pre-existed
	assert.Contains(t, result.FilesRestored, "gtms/.gtms-root",
		"sentinel should be in FilesRestored when gtms/ pre-existed")
	assert.NotContains(t, result.FilesCreated, "gtms/.gtms-root",
		"sentinel should NOT be in FilesCreated when gtms/ pre-existed")
}

// --- BUG-111: Preset output-dir verification ---

func TestPlaywrightPresetEmitsOutputDir(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	_, err := Init(Options{
		ProjectRoot: dir,
		Name:        "OutputDir PW",
		Repo:        "org/od-pw",
		Preset:      PresetPlaywright,
		Force:       false,
	})
	require.NoError(t, err)

	cfg, err := config.LoadFromFile(filepath.Join(dir, "gtms.config"))
	require.NoError(t, err)

	// execute.playwright-runner should have output-dir
	pw := cfg.Adapters["execute"]["playwright-runner"]
	require.NotNil(t, pw)
	assert.Equal(t, "gtms/scripts/playwright", pw.OutputDir)
	assert.Equal(t, "playwright", pw.Framework)
}

func TestBatsPresetEmitsOutputDir(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	_, err := Init(Options{
		ProjectRoot: dir,
		Name:        "OutputDir BATS",
		Repo:        "org/od-bats",
		Preset:      PresetBats,
		Force:       false,
	})
	require.NoError(t, err)

	cfg, err := config.LoadFromFile(filepath.Join(dir, "gtms.config"))
	require.NoError(t, err)

	// execute.bats-runner should have output-dir
	bats := cfg.Adapters["execute"]["bats-runner"]
	require.NotNil(t, bats)
	assert.Equal(t, "test/acceptance", bats.OutputDir)
	assert.Equal(t, "bats", bats.Framework)
}

func TestMinimalPresetSkeletonHasNoOutputDir(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	_, err := Init(Options{
		ProjectRoot: dir,
		Name:        "OutputDir Minimal",
		Repo:        "org/od-minimal",
		Preset:      PresetBats,
		Force:       false,
	})
	require.NoError(t, err)

	// ENH-127: the minimal preset's bats-runner adapter has
	// `output-dir: test/acceptance`, but the create.skeleton adapter
	// must NOT carry an output-dir. Verify per-adapter rather than
	// globally on the file.
	cfg, err := config.LoadFromFile(filepath.Join(dir, "gtms.config"))
	require.NoError(t, err)
	createScript := cfg.Adapters["create"]["manual-create-script"]
	require.NotNil(t, createScript)
	assert.Empty(t, createScript.OutputDir, "create.manual-create-script should not declare output-dir")
}

// ENH-132: Manual prime scaffold tests

func TestInitMinimalPreset_ManualScaffold(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	result, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Manual Test",
		Repo:        "org/manual-test",
		Preset:      PresetBats,
		Force:       false,
	})

	require.NoError(t, err)
	require.NotNil(t, result)

	// Manual directories should exist
	assert.DirExists(t, filepath.Join(dir, "gtms", "manual", "records"))
	assert.DirExists(t, filepath.Join(dir, "gtms", "manual", "templates"))
	assert.DirExists(t, filepath.Join(dir, "gtms", "schemas"))

	// Manual result template should be created
	templatePath := filepath.Join(dir, "gtms", "manual", "templates", "manual-result.template.yaml")
	assert.FileExists(t, templatePath)
	templateContent, err := os.ReadFile(templatePath)
	require.NoError(t, err)
	assert.Contains(t, string(templateContent), "yaml-language-server")
	assert.Contains(t, string(templateContent), "GTMS contract")
	assert.Contains(t, string(templateContent), "OVERALL RESULT")
	assert.Contains(t, string(templateContent), "Optional metadata")
	assert.Contains(t, string(templateContent), "Steps (optional)")
	assert.Contains(t, string(templateContent), "${TESTCASE}")
	assert.Contains(t, string(templateContent), "${TESTCASE_HASH}")
	assert.Contains(t, string(templateContent), "framework: manual")
	assert.Contains(t, string(templateContent), "result:")
	assert.Contains(t, string(templateContent), "steps:")
	assert.Contains(t, string(templateContent), "${BRANCH}")

	// Schema should be created
	schemaPath := filepath.Join(dir, "gtms", "schemas", "manual-result.schema.json")
	assert.FileExists(t, schemaPath)
	schemaContent, err := os.ReadFile(schemaPath)
	require.NoError(t, err)
	assert.Contains(t, string(schemaContent), `"test_case_id"`)
	assert.Contains(t, string(schemaContent), `"test_case_hash"`)
	assert.Contains(t, string(schemaContent), `"framework"`)
	assert.Contains(t, string(schemaContent), `"result"`)
	assert.Contains(t, string(schemaContent), `"^[a-f0-9]{16}$"`)
	assert.Contains(t, string(schemaContent), `"additionalProperties": true`)

	// ENH-160: Manual-prime-script adapter should be created
	primePath := filepath.Join(dir, "gtms", "adapters", "manual-prime-script.sh")
	assert.FileExists(t, primePath)
	primeContent, err := os.ReadFile(primePath)
	require.NoError(t, err)
	assert.Contains(t, string(primeContent), "#!/bin/sh")
	assert.Contains(t, string(primeContent), "GTMS_TESTCASE_HASH")
	assert.Contains(t, string(primeContent), "GTMS_TEMPLATE_FILE")
	assert.Contains(t, string(primeContent), "GTMS_OUTPUT_FILE")
	assert.Contains(t, string(primeContent), "status: complete")
	assert.Contains(t, string(primeContent), "result: pass")

	// VSCode settings and extensions should be created
	assert.FileExists(t, filepath.Join(dir, ".vscode", "settings.json"))
	settingsContent, err := os.ReadFile(filepath.Join(dir, ".vscode", "settings.json"))
	require.NoError(t, err)
	assert.Contains(t, string(settingsContent), "yaml.schemas")
	assert.Contains(t, string(settingsContent), "manual-result.schema.json")

	assert.FileExists(t, filepath.Join(dir, ".vscode", "extensions.json"))
	extContent, err := os.ReadFile(filepath.Join(dir, ".vscode", "extensions.json"))
	require.NoError(t, err)
	assert.Contains(t, string(extContent), "redhat.vscode-yaml")

	// ENH-160: Config should include manual-prime-script under adapters.prime
	cfg, err := config.LoadFromFile(filepath.Join(dir, "gtms.config"))
	require.NoError(t, err)
	manualPrime := cfg.Adapters["prime"]["manual-prime-script"]
	require.NotNil(t, manualPrime, "manual-prime-script adapter should be in config under adapters.prime")
	assert.Equal(t, "sync", manualPrime.Mode)
	assert.Equal(t, "manual", manualPrime.Framework)
	assert.Contains(t, manualPrime.Script, "manual-prime-script.sh")

	// Template must NOT contain attempts: (per ENH-118)
	assert.NotContains(t, string(templateContent), "attempts:")

	// Files should be tracked in result
	assert.Contains(t, result.FilesCreated, "gtms/manual/templates/manual-result.template.yaml")
	assert.Contains(t, result.FilesCreated, "gtms/schemas/manual-result.schema.json")
	assert.Contains(t, result.FilesCreated, "gtms/adapters/manual-prime-script.sh")
	assert.Contains(t, result.FilesCreated, ".vscode/settings.json")
	assert.Contains(t, result.FilesCreated, ".vscode/extensions.json")
}

func TestInitMinimalPreset_VSCodeSettingsPreserved(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	// Pre-create .vscode/settings.json with custom content
	vscodeDir := filepath.Join(dir, ".vscode")
	require.NoError(t, os.MkdirAll(vscodeDir, 0o755))
	existing := `{"editor.fontSize": 14}`
	require.NoError(t, os.WriteFile(filepath.Join(vscodeDir, "settings.json"), []byte(existing), 0o644))

	result, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Preserve Settings",
		Repo:        "org/preserve",
		Preset:      PresetBats,
		Force:       false,
	})

	require.NoError(t, err)

	// Existing settings.json should NOT be overwritten
	content, err := os.ReadFile(filepath.Join(vscodeDir, "settings.json"))
	require.NoError(t, err)
	assert.Equal(t, existing, string(content), "existing .vscode/settings.json must not be overwritten")

	// Should appear in skipped, not created
	assert.Contains(t, result.FilesSkipped, ".vscode/settings.json")
	assert.NotContains(t, result.FilesCreated, ".vscode/settings.json")
}

func TestInitMinimalPreset_VSCodeExtensionsPreserved(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	// Pre-create .vscode/extensions.json with custom content
	vscodeDir := filepath.Join(dir, ".vscode")
	require.NoError(t, os.MkdirAll(vscodeDir, 0o755))
	existing := `{"recommendations": ["ms-python.python"]}`
	require.NoError(t, os.WriteFile(filepath.Join(vscodeDir, "extensions.json"), []byte(existing), 0o644))

	result, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Preserve Extensions",
		Repo:        "org/preserve",
		Preset:      PresetBats,
		Force:       false,
	})

	require.NoError(t, err)

	// Existing extensions.json should NOT be overwritten
	content, err := os.ReadFile(filepath.Join(vscodeDir, "extensions.json"))
	require.NoError(t, err)
	assert.Equal(t, existing, string(content), "existing .vscode/extensions.json must not be overwritten")

	// Should appear in skipped, not created
	assert.Contains(t, result.FilesSkipped, ".vscode/extensions.json")
	assert.NotContains(t, result.FilesCreated, ".vscode/extensions.json")
}

func TestManualResultTemplate_NoInlineValueHints(t *testing.T) {
	// CON-020 Decision 3: no inline value-hint comments on field lines
	// (collide with ENH-E snippet expansion)
	assert.NotContains(t, manualResultTemplate, "# pass | fail | skip",
		"template must not have inline value-hint comments per CON-020 Decision 3")
	assert.NotContains(t, manualResultTemplate, "# RFC3339",
		"template must not have inline value-hint comments per CON-020 Decision 3")
}

func TestManualResultTemplate_FourSectionModel(t *testing.T) {
	// BUG-077: four sections -- GTMS contract -> OVERALL RESULT -> Optional metadata -> Steps
	assert.Contains(t, manualResultTemplate, "GTMS contract")
	assert.Contains(t, manualResultTemplate, "OVERALL RESULT")
	assert.Contains(t, manualResultTemplate, "Optional metadata")
	assert.Contains(t, manualResultTemplate, "Steps (optional)")

	// Verify section order
	contractIdx := strings.Index(manualResultTemplate, "GTMS contract")
	resultIdx := strings.Index(manualResultTemplate, "OVERALL RESULT")
	optionalIdx := strings.Index(manualResultTemplate, "Optional metadata")
	stepsIdx := strings.Index(manualResultTemplate, "Steps (optional)")
	assert.Greater(t, resultIdx, contractIdx, "OVERALL RESULT section should come after GTMS contract")
	assert.Greater(t, optionalIdx, resultIdx, "Optional metadata should come after OVERALL RESULT")
	assert.Greater(t, stepsIdx, optionalIdx, "Steps section should come after Optional metadata")
}

// --- BUG-077: Template and snippet consistency tests ---

func TestBUG077_TemplateHasStepsKey(t *testing.T) {
	assert.Contains(t, manualResultTemplate, "\nsteps:",
		"template must contain a steps: key for step snippet expansion")
}

func TestBUG077_SnippetsValueFirst(t *testing.T) {
	// Result snippets must not re-emit the result: key (Issue 3)
	assert.NotContains(t, vscodeGtmsSnippets, `"result: pass"`,
		"gtms-pass snippet must not contain 'result: pass' -- template owns the key")
	assert.NotContains(t, vscodeGtmsSnippets, `"result: fail"`,
		"gtms-fail snippet must not contain 'result: fail' -- template owns the key")
	assert.NotContains(t, vscodeGtmsSnippets, `"result: skip"`,
		"gtms-skip snippet must not contain 'result: skip' -- template owns the key")
}

func TestBUG077_StepSnippetIndentation(t *testing.T) {
	// Step snippets must carry two-space indentation (Issues 1, 2)
	assert.Contains(t, vscodeGtmsSnippets, `"  - step:`,
		"gtms-step-pass snippet body must start with two-space indented list item")
	assert.Contains(t, vscodeGtmsSnippets, `"    name:`,
		"gtms-step-pass snippet body must have four-space indented name field")
}

func TestBUG077_SnippetDescriptionsNoNotesSection(t *testing.T) {
	// No snippet description should reference "notes section" (Issue 1)
	assert.NotContains(t, vscodeGtmsSnippets, "notes section",
		"no snippet description should reference the non-existent 'notes section'")
}

func TestBUG077_DefectIsList(t *testing.T) {
	// defect: must be a YAML list, not a scalar (Issue 8)
	assert.Contains(t, vscodeGtmsSnippets, `"defect:"`,
		"gtms-fail snippet must emit defect: as a key")
	assert.Contains(t, vscodeGtmsSnippets, `"  - ${2:JIRA-XXX}"`,
		"gtms-fail snippet must emit defect as a list item")
}

func TestBUG077_SchemaEnumeratesSnippetFields(t *testing.T) {
	// Schema must enumerate snippet-emitted fields (Target State Section 4)
	assert.Contains(t, manualResultSchema, `"executed_by"`)
	assert.Contains(t, manualResultSchema, `"executed_at"`)
	assert.Contains(t, manualResultSchema, `"steps"`)
	assert.Contains(t, manualResultSchema, `"defect"`)
	assert.Contains(t, manualResultSchema, `"skip_reason"`)
}

func TestBUG077_SchemaResultAcceptsNull(t *testing.T) {
	// result type must accept null so freshly-stamped files are schema-valid
	assert.Contains(t, manualResultSchema, `"null"`,
		"result type must include null for freshly-stamped templates")
	assert.Contains(t, manualResultSchema, `["string", "null"]`,
		"result type should be a union of string and null")
}

// --- ENH-135: Companion-file and new scaffold tests ---

func TestInitCompanionSnippetForExistingSettings(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	// Pre-create .vscode/settings.json with custom content
	vscodeDir := filepath.Join(dir, ".vscode")
	require.NoError(t, os.MkdirAll(vscodeDir, 0o755))
	customContent := `{"editor.fontSize": 14}`
	require.NoError(t, os.WriteFile(filepath.Join(vscodeDir, "settings.json"), []byte(customContent), 0o644))

	result, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Companion Test",
		Repo:        "org/companion",
		Preset:      PresetBats,
	})
	require.NoError(t, err)

	// Original file should be untouched
	content, err := os.ReadFile(filepath.Join(vscodeDir, "settings.json"))
	require.NoError(t, err)
	assert.Equal(t, customContent, string(content))

	// Companion snippet should exist
	assert.FileExists(t, filepath.Join(vscodeDir, "gtms-settings.json.snippet"))

	// Result tracking
	assert.Contains(t, result.FilesSkipped, ".vscode/settings.json")
	assert.Contains(t, result.FilesCreated, ".vscode/gtms-settings.json.snippet")

	// Warning about merging
	foundWarning := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "settings.json already exists") && strings.Contains(w, "gtms-settings.json.snippet") {
			foundWarning = true
			break
		}
	}
	assert.True(t, foundWarning, "should warn about merging settings.json snippet")
}

func TestInitCompanionSnippetForExistingExtensions(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	// Pre-create .vscode/extensions.json with custom content
	vscodeDir := filepath.Join(dir, ".vscode")
	require.NoError(t, os.MkdirAll(vscodeDir, 0o755))
	customContent := `{"recommendations": ["ms-python.python"]}`
	require.NoError(t, os.WriteFile(filepath.Join(vscodeDir, "extensions.json"), []byte(customContent), 0o644))

	result, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Companion Test",
		Repo:        "org/companion",
		Preset:      PresetBats,
	})
	require.NoError(t, err)

	// Original file should be untouched
	content, err := os.ReadFile(filepath.Join(vscodeDir, "extensions.json"))
	require.NoError(t, err)
	assert.Equal(t, customContent, string(content))

	// Companion snippet should exist
	assert.FileExists(t, filepath.Join(vscodeDir, "gtms-extensions.json.snippet"))

	// Result tracking
	assert.Contains(t, result.FilesSkipped, ".vscode/extensions.json")
	assert.Contains(t, result.FilesCreated, ".vscode/gtms-extensions.json.snippet")

	// Warning about merging
	foundWarning := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "extensions.json already exists") && strings.Contains(w, "gtms-extensions.json.snippet") {
			foundWarning = true
			break
		}
	}
	assert.True(t, foundWarning, "should warn about merging extensions.json snippet")
}

func TestInitFreshVSCodeSettingsNoCompanion(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	result, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Fresh Test",
		Repo:        "org/fresh",
		Preset:      PresetBats,
	})
	require.NoError(t, err)

	// Fresh settings.json should exist
	assert.FileExists(t, filepath.Join(dir, ".vscode", "settings.json"))

	// No companion snippet
	assert.NoFileExists(t, filepath.Join(dir, ".vscode", "gtms-settings.json.snippet"))

	// settings.json should be in FilesCreated, not FilesSkipped
	assert.Contains(t, result.FilesCreated, ".vscode/settings.json")
	assert.NotContains(t, result.FilesSkipped, ".vscode/settings.json")
}

func TestInitRenamedParentWarning(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	// Create a renamed parent with sentinel BEFORE init
	renamedDir := filepath.Join(dir, "testing")
	require.NoError(t, os.MkdirAll(renamedDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(renamedDir, ".gtms-root"), []byte(""), 0o644))

	result, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Renamed Test",
		Repo:        "org/renamed",
		Preset:      PresetBats,
	})
	require.NoError(t, err)

	// Should have a renamed-parent warning
	foundWarning := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "renamed parent directory") && strings.Contains(w, "testing") {
			foundWarning = true
			break
		}
	}
	assert.True(t, foundWarning, "should warn about renamed parent directory")
}

func TestInitShipsTasksReadme(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	result, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Tasks README Test",
		Repo:        "org/tasks-readme",
		Preset:      PresetBats,
	})
	require.NoError(t, err)

	readmePath := filepath.Join(dir, "gtms", "tasks", ".README.md")
	assert.FileExists(t, readmePath)

	content, err := os.ReadFile(readmePath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "GTMS-managed state")
	assert.Contains(t, string(content), "do not edit by hand")

	assert.Contains(t, result.FilesCreated, "gtms/tasks/.README.md")
}

func TestInitShipsSnippetsFile(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	result, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Snippets Test",
		Repo:        "org/snippets",
		Preset:      PresetBats,
	})
	require.NoError(t, err)

	snippetsPath := filepath.Join(dir, ".vscode", "gtms.code-snippets")
	assert.FileExists(t, snippetsPath)

	content, err := os.ReadFile(snippetsPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "gtms-pass")
	assert.Contains(t, string(content), "gtms-fail")
	assert.Contains(t, string(content), "gtms-skip")
	assert.Contains(t, string(content), "gtms-step-pass")

	assert.Contains(t, result.FilesCreated, ".vscode/gtms.code-snippets")
}

func TestInitWorkspaceRootNote(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	result, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Workspace Test",
		Repo:        "org/workspace",
		Preset:      PresetBats,
	})
	require.NoError(t, err)

	// ENH-135: workspace-root informational message lives in Notes,
	// not Warnings (tc-12d29771 + tc-9e4fd6d9 contract).
	foundNote := false
	for _, n := range result.Notes {
		if strings.Contains(n, "workspace") &&
			strings.Contains(n, ".vscode/settings.json") &&
			strings.Contains(n, "yaml-language-server") &&
			strings.Contains(n, "fallback") {
			foundNote = true
			break
		}
	}
	assert.True(t, foundNote, "should have workspace-root informational note in result.Notes")

	// And the same message must NOT have leaked into Warnings.
	for _, w := range result.Warnings {
		if strings.Contains(w, "assumes VSCode is opened at this directory") {
			t.Errorf("workspace-root note must live in Notes, not Warnings: %q", w)
		}
	}
}

// --- BUG-080: DefaultGuidanceYAML must include a prime: block ---

func TestDefaultGuidanceYAML_ContainsPrimeBlock(t *testing.T) {
	assert.Contains(t, DefaultGuidanceYAML, "prime:",
		"DefaultGuidanceYAML must include a 'prime:' block for manual pipeline guidance")
	assert.Contains(t, DefaultGuidanceYAML, "manual-execute",
		"prime guidance block should reference manual-execute adapter")
}

func TestInitRedHatYAMLNote(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	result, err := Init(Options{
		ProjectRoot: dir,
		Name:        "YAML Ext Test",
		Repo:        "org/yaml-ext",
		Preset:      PresetBats,
	})
	require.NoError(t, err)

	// Should have Red Hat YAML extension note
	foundNote := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "Red Hat YAML extension") && strings.Contains(w, "redhat.vscode-yaml") {
			foundNote = true
			break
		}
	}
	assert.True(t, foundNote, "should have Red Hat YAML extension recommendation")
}

// --- ENH-119: Richer skeleton body golden test ---

func TestSkeletonCreateScript_RicherBodySections(t *testing.T) {
	// Verify the skeleton script contains the seven preferred H2 headings
	// in the correct order and uses expected-observation vocabulary.
	expectedHeadings := []string{
		"## Test Objective",
		"## Preconditions",
		"## Test Data",
		"## Test Steps",
		"## Expected Final Outcome",
		"## Postconditions",
		"## Notes",
	}
	for _, h := range expectedHeadings {
		assert.Contains(t, manualCreateScript, h,
			"skeleton should contain heading: %s", h)
	}

	// Verify expected-observation vocabulary
	assert.Contains(t, manualCreateScript, "Expected observation:")

	// Verify old vocabulary is gone
	assert.NotContains(t, manualCreateScript, "**Expected:**")

	// Verify no execution-outcome enum values in YAML context
	// (The word "pass" appears in "result: pass" for the handoff contract,
	// which is correct. Check the heredoc body between the frontmatter
	// closing --- and TCEOF does not contain bare "pass"/"fail"/"skip" values.)
	// We check that the TC body section (between --- and TCEOF) does not
	// have ": pass", ": fail", or ": skip" as YAML values.
	// The handoff contract section is separate and allowed to use result: pass.
	assert.NotContains(t, manualCreateScript, "status: pass")
	assert.NotContains(t, manualCreateScript, "status: fail")
	assert.NotContains(t, manualCreateScript, "status: skip")
}

func TestStarterGuideContent_ENH119_RicherSections(t *testing.T) {
	// Verify the template guide contains all seven section headings
	expectedHeadings := []string{
		"## Test Objective",
		"## Preconditions",
		"## Test Data",
		"## Test Steps",
		"## Expected Final Outcome",
		"## Postconditions",
		"## Notes",
	}
	for _, h := range expectedHeadings {
		assert.Contains(t, starterGuideContent, h,
			"guide should contain heading: %s", h)
	}

	// Each heading should be followed by at least one line of guidance text
	// (not just a bare heading). Check for key guidance phrases.
	assert.Contains(t, starterGuideContent, "State what specific behaviour")
	assert.Contains(t, starterGuideContent, "List every condition")
	assert.Contains(t, starterGuideContent, "Provide exact values")
	assert.Contains(t, starterGuideContent, "Each step is one atomic action")
	assert.Contains(t, starterGuideContent, "overall success criteria")
	assert.Contains(t, starterGuideContent, "expected system state")
	assert.Contains(t, starterGuideContent, "Optional section for context")

	// Guide should include the Expected observation format example
	assert.Contains(t, starterGuideContent, "Expected observation")
}

// --- ENH-161: role-specific template scaffold assertions ---

// TestInit_ScaffoldsAllFourRoleTemplates covers AC #1-#5: `gtms init` writes
// the four role-specific template files at their expected paths and lists
// them in the FilesCreated result. This closes the gap the user finding
// surfaced: scaffold_test.go was only checking counts, not asserting the
// new template files were actually written with the expected content.
func TestInit_ScaffoldsAllFourRoleTemplates(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	result, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Role Templates",
		Repo:        "org/role-templates",
		Preset:      PresetBats,
		Force:       false,
	})
	require.NoError(t, err)

	cases := []struct {
		path        string
		requiredStr []string
	}{
		{
			path: "gtms/test/templates/manual-testcase.template.md",
			requiredStr: []string{
				"test_case_id: ${TESTCASE_ID}",
				`title: "${TITLE}"`,
				"requirement: ${REQUIREMENT}",
				"created: ${CREATED}",
				"## Test Objective",
				"## Test Steps",
			},
		},
		{
			path: "gtms/test/templates/agent-testcase.template.md",
			requiredStr: []string{
				"test_case_id: ${TESTCASE_ID}",
				`title: "${TITLE}"`,
				"requirement: ${REQUIREMENT}",
				"created: ${CREATED}",
			},
		},
		{
			path: "gtms/manual/templates/manual-result.template.yaml",
			requiredStr: []string{
				"test_case_id: ${TESTCASE}",
				"framework: manual",
				`title: "${TC_TITLE}"`,
				`requirement: "${TC_REQUIREMENT}"`,
			},
		},
		{
			path: "gtms/manual/templates/agent-result.template.yaml",
			requiredStr: []string{
				"test_case_id: ${TESTCASE}",
				"framework: manual",
				`title: "${TC_TITLE}"`,
				`requirement: "${TC_REQUIREMENT}"`,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			assert.FileExists(t, filepath.Join(dir, tc.path), "AC #1-#3: template file must be scaffolded")
			assert.Contains(t, result.FilesCreated, tc.path, "AC #5: template listed in init success output")

			data, err := os.ReadFile(filepath.Join(dir, tc.path))
			require.NoError(t, err)
			for _, want := range tc.requiredStr {
				assert.Contains(t, string(data), want, "template at %s must contain %q", tc.path, want)
			}
		})
	}
}

// TestInit_TestcaseTemplatesByteIdenticalDayOne covers AC #2: the manual
// and agent testcase templates have byte-identical day-one content. The
// file split is the future-divergence affordance, not a content change.
func TestInit_TestcaseTemplatesByteIdenticalDayOne(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	_, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Byte Identity",
		Repo:        "org/byte-identity",
		Preset:      PresetBats,
		Force:       false,
	})
	require.NoError(t, err)

	manual, err := os.ReadFile(filepath.Join(dir, "gtms/test/templates/manual-testcase.template.md"))
	require.NoError(t, err)
	agent, err := os.ReadFile(filepath.Join(dir, "gtms/test/templates/agent-testcase.template.md"))
	require.NoError(t, err)
	assert.Equal(t, manual, agent, "AC #2: manual and agent testcase templates byte-identical day one")
}

// TestInit_ResultTemplatesByteIdenticalDayOne covers AC #3 + #4: the agent
// result template is byte-identical to the existing manual result template,
// and the existing manual template content is unchanged from pre-ENH-161.
func TestInit_ResultTemplatesByteIdenticalDayOne(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	_, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Result Identity",
		Repo:        "org/result-identity",
		Preset:      PresetBats,
		Force:       false,
	})
	require.NoError(t, err)

	manual, err := os.ReadFile(filepath.Join(dir, "gtms/manual/templates/manual-result.template.yaml"))
	require.NoError(t, err)
	agent, err := os.ReadFile(filepath.Join(dir, "gtms/manual/templates/agent-result.template.yaml"))
	require.NoError(t, err)
	assert.Equal(t, manual, agent, "AC #3: manual and agent result templates byte-identical day one")
}

// --- ENH-164: new gtms/test/{cases,templates,guides} slot scaffold ---

// TestInit_ScaffoldsNewTestSlots covers ENH-164 "Layout and scaffold" AC:
// `gtms init` creates the three sibling slots under gtms/test/.
func TestInit_ScaffoldsNewTestSlots(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	_, err := Init(Options{
		ProjectRoot: dir,
		Name:        "ENH-164 Slot Scaffold",
		Repo:        "org/enh-164-slot-scaffold",
		Preset:      PresetBats,
		Force:       false,
	})
	require.NoError(t, err)

	assert.DirExists(t, filepath.Join(dir, "gtms", "test", "cases"),
		"ENH-164: gtms init must scaffold gtms/test/cases/")
	assert.DirExists(t, filepath.Join(dir, "gtms", "test", "templates"),
		"ENH-164: gtms init must scaffold gtms/test/templates/")
	assert.DirExists(t, filepath.Join(dir, "gtms", "test", "guides"),
		"ENH-164: gtms init must scaffold gtms/test/guides/")
}

// TestInit_DoesNotCreateLegacyCasesDir covers ENH-164 "Layout and scaffold"
// AC: no files land under gtms/test/cases/. The check runs against every valid
// preset so a future preset cannot regress silently.
func TestInit_DoesNotCreateLegacyCasesDir(t *testing.T) {
	skipIfShort(t)
	for _, preset := range ValidPresets() {
		t.Run(preset, func(t *testing.T) {
			dir := initGitRepo(t)
			_, err := Init(Options{
				ProjectRoot: dir,
				Name:        "ENH-164 No Legacy",
				Repo:        "org/enh-164-no-legacy",
				Preset:      preset,
				Force:       false,
			})
			require.NoError(t, err)

			assert.NoDirExists(t, filepath.Join(dir, "gtms", "cases"),
				"ENH-164: gtms init (preset=%s) must not create the legacy gtms/test/cases/ directory", preset)
			assert.NoDirExists(t, filepath.Join(dir, "gtms", "cases", "templates"),
				"ENH-164: gtms init (preset=%s) must not create the legacy gtms/test/templates/ directory", preset)
			assert.NoDirExists(t, filepath.Join(dir, "gtms", "cases", "guides"),
				"ENH-164: gtms init (preset=%s) must not create the legacy gtms/test/guides/ directory", preset)
		})
	}
}

// TestInit_StampingTemplatesAtNewPaths covers ENH-164 "Test surface" AC:
// role-specific stamping templates land at gtms/test/templates/. Asserts both
// file existence and that the templates carry the ENH-161 frontmatter
// placeholder shape (test_case_id: ${TESTCASE_ID}) so a wrong move that
// silently writes the wrong body cannot pass.
func TestInit_StampingTemplatesAtNewPaths(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	result, err := Init(Options{
		ProjectRoot: dir,
		Name:        "ENH-164 Template Paths",
		Repo:        "org/enh-164-template-paths",
		Preset:      PresetBats,
		Force:       false,
	})
	require.NoError(t, err)

	manualRel := "gtms/test/templates/manual-testcase.template.md"
	agentRel := "gtms/test/templates/agent-testcase.template.md"

	assert.FileExists(t, filepath.Join(dir, manualRel),
		"ENH-164: manual stamping template must land at %s", manualRel)
	assert.FileExists(t, filepath.Join(dir, agentRel),
		"ENH-164: agent stamping template must land at %s", agentRel)
	assert.Contains(t, result.FilesCreated, manualRel,
		"ENH-164: manual stamping template must be listed in init success output")
	assert.Contains(t, result.FilesCreated, agentRel,
		"ENH-164: agent stamping template must be listed in init success output")

	manualBody, err := os.ReadFile(filepath.Join(dir, manualRel))
	require.NoError(t, err)
	agentBody, err := os.ReadFile(filepath.Join(dir, agentRel))
	require.NoError(t, err)
	assert.Contains(t, string(manualBody), "test_case_id: ${TESTCASE_ID}",
		"ENH-164: manual stamping template at new path must carry ENH-161 frontmatter placeholder")
	assert.Contains(t, string(agentBody), "test_case_id: ${TESTCASE_ID}",
		"ENH-164: agent stamping template at new path must carry ENH-161 frontmatter placeholder")
}

// --- ENH-180: Starter Agent Skills ---

func TestWriteExampleSkills(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)
	result := &Result{}

	err := WriteExampleSkills(dir, result)
	require.NoError(t, err)

	// Verify all six files were created.
	expectedFiles := []string{
		"gtms/skills/README.md",
		"gtms/skills/gtms-tests-create/SKILL.md",
		"gtms/skills/gtms-tests-automate/SKILL.md",
		"gtms/skills/gtms-tests-execute/SKILL.md",
		"gtms/skills/gtms-tests-prime/SKILL.md",
		"gtms/skills/gtms-tests-verify-intent/SKILL.md",
	}
	for _, f := range expectedFiles {
		assert.FileExists(t, filepath.Join(dir, filepath.FromSlash(f)), "missing: %s", f)
		assert.Contains(t, result.FilesCreated, f, "should be listed in FilesCreated: %s", f)
	}

	// Verify name: frontmatter matches directory name for each SKILL.md.
	skillDirs := []string{
		"gtms-tests-create",
		"gtms-tests-automate",
		"gtms-tests-execute",
		"gtms-tests-prime",
		"gtms-tests-verify-intent",
	}
	for _, name := range skillDirs {
		content, readErr := os.ReadFile(filepath.Join(dir, "gtms", "skills", name, "SKILL.md"))
		require.NoError(t, readErr)
		body := string(content)

		assert.Contains(t, body, "name: "+name,
			"SKILL.md name: frontmatter must match directory name %s", name)
		assert.Contains(t, body, "description:",
			"SKILL.md must have a description: field in %s", name)
		assert.Contains(t, body, "This is a GTMS starter skill",
			"SKILL.md must contain provenance anchor in %s", name)
	}

	// Verify idempotency: modify one file, re-call, verify it was not overwritten.
	modPath := filepath.Join(dir, "gtms", "skills", "gtms-tests-create", "SKILL.md")
	require.NoError(t, os.WriteFile(modPath, []byte("custom content"), 0o644))
	result2 := &Result{}
	err = WriteExampleSkills(dir, result2)
	require.NoError(t, err)
	assert.Contains(t, result2.FilesSkipped, "gtms/skills/gtms-tests-create/SKILL.md",
		"modified file should be skipped on re-call")

	modContent, _ := os.ReadFile(modPath)
	assert.Equal(t, "custom content", string(modContent),
		"idempotency: modified file must not be overwritten")
}

func TestWriteExampleSkills_InitIntegration(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)

	result, err := Init(Options{
		ProjectRoot: dir,
		Name:        "Skills Test",
		Repo:        "org/skills-test",
		Preset:      PresetManual,
		Force:       false,
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify skills files were created by Init.
	assert.FileExists(t, filepath.Join(dir, "gtms", "skills", "README.md"))
	assert.FileExists(t, filepath.Join(dir, "gtms", "skills", "gtms-tests-create", "SKILL.md"))
	assert.FileExists(t, filepath.Join(dir, "gtms", "skills", "gtms-tests-execute", "SKILL.md"))

	assert.Contains(t, result.FilesCreated, "gtms/skills/README.md",
		"catalog README should appear in Init FilesCreated")
	assert.Contains(t, result.FilesCreated, "gtms/skills/gtms-tests-create/SKILL.md",
		"skill files should appear in Init FilesCreated")
}

// --- ENH-187: BATS run result-variable lifetime guidance in the automate skill ---

// scaffoldedAutomateSkill scaffolds the starter skills into a temp dir and
// returns the body of the automate SKILL.md as the CLI actually writes it.
// Reading the scaffolded artefact (not the repo source) is deliberate: it is
// what a user's agent ends up reading.
func scaffoldedAutomateSkill(t *testing.T) string {
	t.Helper()
	dir := initGitRepo(t)
	require.NoError(t, WriteExampleSkills(dir, &Result{}))

	body, err := os.ReadFile(filepath.Join(dir, "gtms", "skills", "gtms-tests-automate", "SKILL.md"))
	require.NoError(t, err)
	return string(body)
}

// TestENH187_AutomateSkillTeachesRunVariableLifetime pins the semantic anchors
// of the run-lifetime guidance into place.
//
// Existing scaffold coverage asserts the skill file exists with valid
// frontmatter, and the fenced-block drift test only checks that CLI command
// names resolve. Neither would notice this entire section being deleted, so
// without this test the guidance can silently rot out of the skill and nothing
// goes red.
func TestENH187_AutomateSkillTeachesRunVariableLifetime(t *testing.T) {
	skipIfShort(t)
	body := scaffoldedAutomateSkill(t)

	// All four run result variables must be named. An incomplete rule that omits
	// $status or $lines has a live failure mode: an agent preserves $output
	// correctly and then asserts the WRONG command's exit code.
	for _, v := range []string{"$output", "$stderr", "$lines", "$status"} {
		assert.Contains(t, body, v,
			"automate skill must name the run result variable %s", v)
	}

	// The rule must be STATED, not merely demonstrated by a sample that happens
	// to use the variables. Match the shape of the claim (every ... run ...
	// replaces/resets/overwrites) rather than one sentence, so a reworded but
	// still-correct skill does not go red.
	assert.Regexp(t, "(?i)every[^\n]*run[^\n]*(replace|reset|overwrite)", body,
		"automate skill must state that every run replaces the result variables, not just use them in a sample")

	// The safe shape: run --separate-stderr -> assert the status -> capture.
	assert.Contains(t, body, "run --separate-stderr",
		"automate skill must show the --separate-stderr invocation form")
	assert.Contains(t, body, "assert_success",
		"automate skill must show the status being asserted while it is still the current run's")

	// Re-run recovery is the mode that stays green. It must be forbidden.
	assert.Regexp(t, `(?i)never re-run the command under test`, body,
		"automate skill must forbid re-running the command under test to recover a lost value")

	// The vacuous-negative case: the half an agent will not work out for itself.
	assert.Contains(t, body, "refute_output",
		"automate skill must name refute_output as a negative assertion that goes quiet")
	assert.Contains(t, body, `[ -z "$output" ]`,
		"automate skill must name the empty-output check as a negative assertion that goes quiet")
	assert.Regexp(t, `(?i)vacuous`, body,
		"automate skill must say that a stale read under a negative assertion passes vacuously")
}

// TestENH187_AutomateSkillDoesNotClaimRedirectionBreaksAssertions guards against
// a specific falsehood.
//
// Claiming that redirecting inside a `run` invocation (run cmd > f 2> g) throws
// away $status, $output or the bats-assert helpers is FALSE. `run` is a bash
// function; a redirection written after it binds to the function call, not to
// the command inside it, and BATS captures the command's streams internally
// regardless -- assert_success still works.
//
// This is a regression guard, not a hypothetical: the claim shipped once in an
// earlier draft of the equivalent guidance and was caught only at review. It
// matters most here, because a section whose whole purpose is to correct false
// beliefs about `run` must not contain one -- and this file scaffolds into
// projects we will never see again.
//
// A stated PREFERENCE for printf snapshotting, with a true rationale, is fine
// and is not what this test rejects.
func TestENH187_AutomateSkillDoesNotClaimRedirectionBreaksAssertions(t *testing.T) {
	skipIfShort(t)
	body := scaffoldedAutomateSkill(t)

	// The exact phrase the falsehood shipped with.
	assert.NotContains(t, strings.ToLower(body), "throws away",
		"automate skill must not claim redirection throws away the run result variables")

	damageVerbs := []string{"throws away", "breaks", "loses", "discards", "disables"}
	for i, line := range strings.Split(body, "\n") {
		lower := strings.ToLower(line)
		mentionsRedirection := strings.Contains(lower, "redirect") ||
			(strings.Contains(lower, "run ") && strings.Contains(line, "> "))
		if !mentionsRedirection {
			continue
		}
		for _, verb := range damageVerbs {
			assert.NotContains(t, lower, verb,
				"line %d claims redirection %q -- that is false; redirection binds to the run function call, not the command inside it, and BATS still captures the streams: %s",
				i+1, verb, line)
		}
	}
}

// TestENH187_AutomateSkillStaysGenericAndASCII checks the whole scaffolded
// skill, not only the new section. The guidance is lifted from an internal
// prompt full of record numbers and repo paths, so the copy-paste leak is a real
// and immediate hazard.
//
// Generic references to CI stay legal -- a BATS-authoring skill may reasonably
// say "this will fail in CI". What must not appear is THIS repo: record IDs,
// repo paths, workflow paths, runner names, org names. The forbidden set is
// enumerated as concrete tokens rather than described, so the check is
// mechanical rather than a judgement call.
func TestENH187_AutomateSkillStaysGenericAndASCII(t *testing.T) {
	skipIfShort(t)
	body := scaffoldedAutomateSkill(t)

	assert.NotRegexp(t, `(?:BUG|ENH|CON|ADR)-\d`, body,
		"scaffolded skill must not carry record identifiers")

	forbidden := []string{
		"test/acceptance/",
		"gtms/automation/prompts/",
		"internal/scaffold/",
		"win-runner",
		"remote-dir-run-unix.sh",
		".github/workflows/",
		"bechlin/",
		"aitestmanagement/",
	}
	for _, tok := range forbidden {
		assert.NotContains(t, body, tok,
			"scaffolded skill must not leak the repo-specific token %q", tok)
	}

	for i, r := range body {
		require.Less(t, r, rune(128),
			"scaffolded skill must be ASCII-only: non-ASCII rune %q at byte offset %d", r, i)
	}
}

// TestENH187_RunLifetimeGuidanceReachesEveryPreset asserts the new content is
// scaffolded by all three presets.
//
// The skills catalog is written unconditionally by Init, so preset-independence
// is expected rather than in doubt -- but that expectation is load-bearing: it is
// what lets the acceptance suite exercise a single preset and still claim
// coverage. An assumption carrying that much weight should be asserted.
func TestENH187_RunLifetimeGuidanceReachesEveryPreset(t *testing.T) {
	skipIfShort(t)

	for _, preset := range []string{PresetManual, PresetBats, PresetPlaywright} {
		t.Run(preset, func(t *testing.T) {
			dir := initGitRepo(t)
			_, err := Init(Options{
				ProjectRoot: dir,
				Name:        "Preset Test",
				Repo:        "org/preset-test",
				Preset:      preset,
			})
			require.NoError(t, err)

			body, err := os.ReadFile(filepath.Join(dir, "gtms", "skills", "gtms-tests-automate", "SKILL.md"))
			require.NoError(t, err)

			// Marker for "the new guidance is present": all four variable names.
			// The pre-change skill contained zero of them, so this cleanly
			// separates old content from new.
			for _, v := range []string{"$output", "$stderr", "$lines", "$status"} {
				assert.Contains(t, string(body), v,
					"preset %s must scaffold the automate skill carrying %s", preset, v)
			}
		})
	}
}

// --- ENH-183: Agent-instructions snippet tests ---

// TestENH183_AgentsSnippetGoldenDraftA verifies the embedded AgentsSnippetMD
// contains the locked Draft A block byte-identical. Anchor greps alone would
// still pass if the wrapper prose were dropped or the wording changed; this
// golden test pins the exact wording.
func TestENH183_AgentsSnippetGoldenDraftA(t *testing.T) {
	// Locked Draft A -- byte-identical match required.
	// Uses string concatenation for backtick-containing lines so the Go
	// raw string literal does not need escaping.
	const draftA = "## Testing: this project uses GTMS\n" +
		"\n" +
		"This project uses GTMS (Git-based Test Management System) to manage its test\n" +
		"cases -- the create -> automate -> execute pipeline. It does not use GTMS for\n" +
		"unit tests; write and run those the usual way.\n" +
		"\n" +
		"Before creating, automating, or executing any test cases, run `gtms agent` to\n" +
		"load the operating reference for the pipeline.\n" +
		"\n" +
		"- `gtms agent`          -- how GTMS orchestrates the create -> automate -> execute pipeline\n" +
		"- `gtms skills`         -- starter agent skills under `gtms/skills/` you can install\n" +
		"- If `gtms` reports no project here, read `gtms getting-started` first.\n"

	assert.Contains(t, AgentsSnippetMD, draftA,
		"AgentsSnippetMD must contain the locked Draft A block byte-identical")
}

// TestENH183_SnippetWriteFiresForEveryPreset verifies the agent-instructions
// snippet is scaffolded for every valid preset. Guards against a regression
// that wires the write into only one preset branch.
func TestENH183_SnippetWriteFiresForEveryPreset(t *testing.T) {
	skipIfShort(t)
	for _, preset := range ValidPresets() {
		t.Run(preset, func(t *testing.T) {
			dir := initGitRepo(t)
			result, err := Init(Options{
				ProjectRoot: dir,
				Name:        "test",
				Repo:        "org/repo",
				Preset:      preset,
			})
			require.NoError(t, err)

			snippetPath := filepath.Join(dir, "gtms", "AGENTS-SNIPPET.md")
			assert.FileExists(t, snippetPath,
				"gtms/AGENTS-SNIPPET.md must be created for preset %s", preset)

			content, err := os.ReadFile(snippetPath)
			require.NoError(t, err)
			assert.Contains(t, string(content), "gtms agent",
				"snippet must contain 'gtms agent' anchor for preset %s", preset)
			assert.Contains(t, string(content), "gtms skills",
				"snippet must contain 'gtms skills' anchor for preset %s", preset)
			assert.Contains(t, string(content), "gtms getting-started",
				"snippet must contain 'gtms getting-started' anchor for preset %s", preset)
			assert.Contains(t, string(content), "unit test",
				"snippet must contain the unit-test carve-out for preset %s", preset)

			assert.Contains(t, result.FilesCreated, "gtms/AGENTS-SNIPPET.md",
				"gtms/AGENTS-SNIPPET.md must appear in FilesCreated for preset %s", preset)
		})
	}
}
