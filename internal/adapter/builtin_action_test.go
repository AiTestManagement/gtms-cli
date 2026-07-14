package adapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adrg/frontmatter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/scaffold"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

func TestBuiltinCreate_StampsSkeletonWithID(t *testing.T) {
	dir := t.TempDir()

	ctx := &AdapterContext{
		OutputDir:    dir,
		TestCaseIDs:  "tc-aabbccdd,tc-11223344",
		TestCaseName: "",
		Reference:    "",
	}

	result, err := BuiltinCreate(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Len(t, result.SavedFiles, 1)

	// File should exist
	outPath := result.SavedFiles[0]
	assert.FileExists(t, outPath)
	assert.Equal(t, filepath.Join(dir, "tc-aabbccdd.md"), outPath)

	// Read and verify content
	data, err := os.ReadFile(outPath)
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "test_case_id: tc-aabbccdd")
	assert.Contains(t, content, "priority: Medium")
	assert.Contains(t, content, "type: Functional")
	assert.NotContains(t, content, "status: draft")
	assert.NotContains(t, content, "name:")
	assert.Contains(t, content, "## Test Objective")
	assert.Contains(t, content, "## Test Steps")
}

func TestBuiltinCreate_WithName(t *testing.T) {
	dir := t.TempDir()

	ctx := &AdapterContext{
		OutputDir:    dir,
		TestCaseIDs:  "tc-aabbccdd",
		TestCaseName: "user-can-login",
	}

	result, err := BuiltinCreate(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, filepath.Join(dir, "tc-aabbccdd-user-can-login.md"), result.SavedFiles[0])

	data, err := os.ReadFile(result.SavedFiles[0])
	require.NoError(t, err)
	assert.Contains(t, string(data), `title: "user-can-login"`)
}

func TestBuiltinCreate_WithReference(t *testing.T) {
	dir := t.TempDir()

	ctx := &AdapterContext{
		OutputDir:   dir,
		TestCaseIDs: "tc-aabbccdd",
		Reference:   "REQ-001",
	}

	result, err := BuiltinCreate(ctx)
	require.NoError(t, err)

	data, err := os.ReadFile(result.SavedFiles[0])
	require.NoError(t, err)
	// ENH-161 substitution wraps non-empty --reference in a YAML
	// double-quoted scalar (see yamlQuotedScalarOrEmpty) so values
	// containing `: `, leading `-`, etc. round-trip cleanly.
	assert.Contains(t, string(data), `requirement: "REQ-001"`)
}

func TestBuiltinCreate_NoOutputDir(t *testing.T) {
	ctx := &AdapterContext{
		TestCaseIDs: "tc-aabbccdd",
	}

	_, err := BuiltinCreate(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "output directory not set")
}

func TestBuiltinCreate_NoTestCaseIDs(t *testing.T) {
	dir := t.TempDir()
	ctx := &AdapterContext{
		OutputDir: dir,
	}

	_, err := BuiltinCreate(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no test case IDs available")
}

func TestBuiltinPrime_StampsTemplate(t *testing.T) {
	dir := t.TempDir()

	// Create template file
	tmplPath := filepath.Join(dir, "template.yaml")
	tmplContent := `test_case_id: ${TESTCASE}
test_case_hash: ${TESTCASE_HASH}
framework: manual
result:
title: "${TC_TITLE}"
branch: ${BRANCH}
`
	require.NoError(t, os.WriteFile(tmplPath, []byte(tmplContent), 0644))

	outPath := filepath.Join(dir, "tc-aabbccdd--manual.result.yaml")
	ctx := &AdapterContext{
		TemplateFile: tmplPath,
		OutputFile:   outPath,
		TestCase:     "tc-aabbccdd",
		TestCaseHash: "abcdef0123456789",
		Branch:       "main",
		TCTitle:      "Login test",
		Force:        false,
	}

	result, err := BuiltinPrime(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.FileExists(t, outPath)

	data, err := os.ReadFile(outPath)
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "test_case_id: tc-aabbccdd")
	assert.Contains(t, content, "test_case_hash: abcdef0123456789")
	assert.Contains(t, content, "branch: main")
	assert.Contains(t, content, `title: "Login test"`)
}

func TestBuiltinPrime_RefusesOverwriteWithoutForce(t *testing.T) {
	dir := t.TempDir()

	tmplPath := filepath.Join(dir, "template.yaml")
	require.NoError(t, os.WriteFile(tmplPath, []byte("template"), 0644))

	outPath := filepath.Join(dir, "existing.yaml")
	require.NoError(t, os.WriteFile(outPath, []byte("existing content"), 0644))

	ctx := &AdapterContext{
		TemplateFile: tmplPath,
		OutputFile:   outPath,
		Force:        false,
	}

	result, err := BuiltinPrime(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, result.ExitCode)
	assert.Contains(t, result.Stderr, "already exists")
}

func TestBuiltinPrime_OverwritesWithForce(t *testing.T) {
	dir := t.TempDir()

	tmplPath := filepath.Join(dir, "template.yaml")
	require.NoError(t, os.WriteFile(tmplPath, []byte("test_case_id: ${TESTCASE}"), 0644))

	outPath := filepath.Join(dir, "existing.yaml")
	require.NoError(t, os.WriteFile(outPath, []byte("old content"), 0644))

	ctx := &AdapterContext{
		TemplateFile: tmplPath,
		OutputFile:   outPath,
		TestCase:     "tc-aabbccdd",
		Force:        true,
	}

	result, err := BuiltinPrime(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)

	data, err := os.ReadFile(outPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "tc-aabbccdd")
}

func TestBuiltinExecute_RecordsVerdict(t *testing.T) {
	dir := t.TempDir()

	resultFilePath := filepath.Join(dir, "result.yaml")
	require.NoError(t, os.WriteFile(resultFilePath, []byte("test_case_id: tc-aabbccdd\ntest_case_hash: abcdef0123456789\nresult: pass\n"), 0644))

	ctx := &AdapterContext{
		ResultValue:        "pass",
		TestCase:           "tc-aabbccdd",
		TestCaseHash:       "abcdef0123456789",
		ResultTestCaseHash: "abcdef0123456789",
		ResultTemplate:     resultFilePath,
	}

	result, err := BuiltinExecute(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stdout, "tc-aabbccdd -> pass")
	assert.Empty(t, result.Stderr) // no drift
}

func TestBuiltinExecute_DetectsDrift(t *testing.T) {
	dir := t.TempDir()

	resultFilePath := filepath.Join(dir, "result.yaml")
	require.NoError(t, os.WriteFile(resultFilePath, []byte("test_case_id: tc-aabbccdd\ntest_case_hash: abcdef0123456789\nresult: pass\n"), 0644))

	ctx := &AdapterContext{
		ResultValue:        "pass",
		TestCase:           "tc-aabbccdd",
		TestCaseHash:       "1111111111111111", // different from prime-time
		ResultTestCaseHash: "abcdef0123456789",
		ResultTemplate:     resultFilePath,
	}

	result, err := BuiltinExecute(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stderr, "drift")

	// Drift fields should be appended to the result file
	data, err := os.ReadFile(resultFilePath)
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "drift-detected: true")
	assert.Contains(t, content, "test_case_hash_at_execute: 1111111111111111")
}

func TestValidateTestCasePostFill_ValidFile(t *testing.T) {
	dir := t.TempDir()

	// Set up layout for test
	casesDir := filepath.Join(dir, "gtms", "test", "cases", "test-folder")
	require.NoError(t, os.MkdirAll(casesDir, 0755))

	// Write sentinel
	gtmsDir := filepath.Join(dir, "gtms")
	require.NoError(t, os.WriteFile(filepath.Join(gtmsDir, ".gtms-root"), []byte(""), 0644))

	// Write a valid TC file
	tcContent := `---
test_case_id: tc-aabbccdd
title: "Test case"
status: draft
---
## Test Objective
`
	require.NoError(t, os.WriteFile(filepath.Join(casesDir, "tc-aabbccdd-test.md"), []byte(tcContent), 0644))

	violations := ValidateTestCasePostFill(dir, "tc-aabbccdd")
	assert.Empty(t, violations)
}

func TestValidateTestCasePostFill_IDMismatch(t *testing.T) {
	dir := t.TempDir()
	casesDir := filepath.Join(dir, "gtms", "test", "cases", "test-folder")
	require.NoError(t, os.MkdirAll(casesDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gtms", ".gtms-root"), []byte(""), 0644))

	tcContent := `---
test_case_id: tc-99999999
title: "Mismatched ID"
---
`
	require.NoError(t, os.WriteFile(filepath.Join(casesDir, "tc-aabbccdd-test.md"), []byte(tcContent), 0644))

	violations := ValidateTestCasePostFill(dir, "tc-aabbccdd")
	require.NotEmpty(t, violations)
	assert.Contains(t, violations[0].Reason, "does not match filename ID")
}

func TestValidateTestCasePostFill_MissingFrontmatter(t *testing.T) {
	dir := t.TempDir()
	casesDir := filepath.Join(dir, "gtms", "test", "cases", "test-folder")
	require.NoError(t, os.MkdirAll(casesDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gtms", ".gtms-root"), []byte(""), 0644))

	tcContent := `---
title: "No test_case_id"
---
`
	require.NoError(t, os.WriteFile(filepath.Join(casesDir, "tc-aabbccdd-test.md"), []byte(tcContent), 0644))

	violations := ValidateTestCasePostFill(dir, "tc-aabbccdd")
	require.NotEmpty(t, violations)
	assert.Contains(t, violations[0].Reason, "missing required field")
}

func TestValidateTestCasePostFill_DuplicateIDs(t *testing.T) {
	dir := t.TempDir()
	casesDir := filepath.Join(dir, "gtms", "test", "cases", "test-folder")
	require.NoError(t, os.MkdirAll(casesDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gtms", ".gtms-root"), []byte(""), 0644))

	tc1 := `---
test_case_id: tc-aabbccdd
title: "First"
---
`
	tc2 := `---
test_case_id: tc-aabbccdd
title: "Duplicate"
---
`
	require.NoError(t, os.WriteFile(filepath.Join(casesDir, "tc-aabbccdd-first.md"), []byte(tc1), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(casesDir, "tc-aabbccdd-duplicate.md"), []byte(tc2), 0644))

	// Validate either file — should catch the duplicate
	violations := ValidateTestCasePostFill(dir, "tc-aabbccdd")
	found := false
	for _, v := range violations {
		if strings.Contains(v.Reason, "duplicate") {
			found = true
		}
	}
	assert.True(t, found, "should detect duplicate test_case_id")
}

func TestYamlEscape(t *testing.T) {
	assert.Equal(t, "hello", yamlEscape("hello"))
	// Backslash is doubled, quote is escaped
	assert.Equal(t, `say \\\"hi\\\"`, yamlEscape("say \\\"hi\\\""))
	// Newline becomes literal \n
	assert.Equal(t, `line1\nline2`, yamlEscape("line1\nline2"))
	// Tab becomes literal \t
	assert.Equal(t, `tab\there`, yamlEscape("tab\there"))
}

// --- ENH-151: BuiltinAutomate tests ---

// setupAutomateFixture creates a minimal project root with a slugged TC spec
// file and a gtms.config that has a bats-runner execute adapter. BUG-107:
// uses a slugged filename (tc-auto0001-test-skeleton.md) so BuiltinAutomate
// exercises the slug-preserving code path.
func setupAutomateFixture(t *testing.T) (root string) {
	t.Helper()
	root = t.TempDir()

	// TC spec (slugged filename per BUG-107)
	tcDir := filepath.Join(root, "gtms", "test", "cases", "my-feature")
	require.NoError(t, os.MkdirAll(tcDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tcDir, "tc-auto0001-test-skeleton.md"), []byte(`---
test_case_id: tc-auto0001
title: Test automation skeleton
requirement: REQ-A
priority: Medium
type: Functional
---

## Test Objective

Test something.
`), 0644))

	// gtms.config with bats-runner execute adapter
	require.NoError(t, os.WriteFile(filepath.Join(root, "gtms.config"), []byte(`project:
  name: test-project
  repo: org/test
adapters:
  execute:
    bats-runner:
      mode: sync
      script: run.sh
      framework: bats
defaults:
  execute: bats-runner
`), 0644))

	// Required dirs
	for _, dir := range []string{
		"gtms/tasks/pending",
		"gtms/tasks/in-progress",
		"gtms/tasks/complete",
		"gtms/tasks/error",
		"gtms/automation/wiring",
		"test/acceptance/my-feature",
	} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0755))
	}

	return root
}

// setupBareAutomateFixture creates a minimal project root with a bare TC spec
// file (no slug). Used to test the fallback path where the artefact filename
// uses only the test case ID. BUG-107.
func setupBareAutomateFixture(t *testing.T) (root string) {
	t.Helper()
	root = t.TempDir()

	// TC spec (bare filename -- no slug)
	tcDir := filepath.Join(root, "gtms", "test", "cases", "my-feature")
	require.NoError(t, os.MkdirAll(tcDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tcDir, "tc-auto0001.md"), []byte(`---
test_case_id: tc-auto0001
title: Bare test case
requirement: REQ-B
priority: Medium
type: Functional
---

## Test Objective

Bare spec test.
`), 0644))

	// Same gtms.config
	require.NoError(t, os.WriteFile(filepath.Join(root, "gtms.config"), []byte(`project:
  name: test-project
  repo: org/test
adapters:
  execute:
    bats-runner:
      mode: sync
      script: run.sh
      framework: bats
defaults:
  execute: bats-runner
`), 0644))

	for _, dir := range []string{
		"gtms/tasks/pending",
		"gtms/tasks/in-progress",
		"gtms/tasks/complete",
		"gtms/tasks/error",
		"gtms/automation/wiring",
		"test/acceptance/my-feature",
	} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0755))
	}

	return root
}

func TestBuiltinAutomate_StampsBATSSkeleton(t *testing.T) {
	root := setupAutomateFixture(t)
	cfg, err := loadTestConfig(root)
	require.NoError(t, err)

	ctx := &AdapterContext{
		ProjectRoot:  root,
		TestCase:     "tc-auto0001",
		Framework:    "bats",
		TestCaseFile: "gtms/test/cases/my-feature/tc-auto0001-test-skeleton.md",
		OutputSubdir: "my-feature/",
	}

	result, err := BuiltinAutomate(ctx, cfg)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	require.Len(t, result.SavedFiles, 1)

	// BUG-107: Skeleton file should preserve the source spec slug
	outPath := result.SavedFiles[0]
	assert.FileExists(t, outPath)
	assert.True(t, strings.HasSuffix(outPath, "tc-auto0001-test-skeleton.bats"),
		"skeleton filename should be tc-auto0001-test-skeleton.bats, got %s", outPath)

	// Read and verify skeleton content
	data, err := os.ReadFile(outPath)
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "setup_file()")
	assert.Contains(t, content, "_common_setup")
	assert.Contains(t, content, "@test")
	assert.Contains(t, content, "tc-auto0001")
	assert.Contains(t, content, "teardown()")
	assert.Contains(t, content, "common-setup.bash")
}

func TestBuiltinAutomate_WritesWiringWithPendingHash(t *testing.T) {
	root := setupAutomateFixture(t)
	cfg, err := loadTestConfig(root)
	require.NoError(t, err)

	ctx := &AdapterContext{
		ProjectRoot:  root,
		TestCase:     "tc-auto0001",
		Framework:    "bats",
		TestCaseFile: "gtms/test/cases/my-feature/tc-auto0001-test-skeleton.md",
		OutputSubdir: "my-feature/",
	}

	_, err = BuiltinAutomate(ctx, cfg)
	require.NoError(t, err)

	// Read wiring record and verify
	rec, wiringPath, err := wiring.Find(root, "tc-auto0001", "bats")
	require.NoError(t, err)
	require.NotNil(t, rec, "wiring record should exist")
	assert.NotEmpty(t, wiringPath)
	assert.Equal(t, "tc-auto0001", rec.TestCase)
	assert.Equal(t, "bats", rec.Framework)
	assert.Equal(t, "bats-runner", rec.Adapter, "wiring should carry the canonical execute adapter")
	assert.Equal(t, wiring.PendingArtefactHash, rec.ArtefactHash,
		"artefact-hash should be the pending sentinel")
	assert.True(t, wiring.IsRealArtefactHash(rec.TestCaseHash),
		"testcase-hash should be a real hash, got %q", rec.TestCaseHash)
	// BUG-107: wiring artefact should carry the slugged filename
	assert.Contains(t, rec.Artefact, "tc-auto0001-test-skeleton.bats",
		"artefact path should reference the slugged skeleton")
}

// TestBuiltinAutomate_HonorsConfiguredOutputDir is the BUG-125 regression: when
// output-dir is explicitly configured (OutputDirConfigured == true), the built-in
// stamps the spec under that dir (joined with the work-item subdir) and records that
// location in the wiring artefact -- NOT under the framework-native default.
func TestBuiltinAutomate_HonorsConfiguredOutputDir(t *testing.T) {
	root := setupAutomateFixture(t)
	cfg, err := loadTestConfig(root)
	require.NoError(t, err)

	// ctx.OutputDir is absolute in production (invoker joins it with projectRoot);
	// mirror that here with a brownfield-style harness path inside the project.
	customDir := filepath.Join(root, "PLAYWRIGHT-TESTS", "tests", "gtms")
	ctx := &AdapterContext{
		ProjectRoot:         root,
		TestCase:            "tc-auto0001",
		Framework:           "bats",
		TestCaseFile:        "gtms/test/cases/my-feature/tc-auto0001-test-skeleton.md",
		OutputSubdir:        "my-feature/",
		OutputDir:           customDir,
		OutputDirConfigured: true,
	}

	result, err := BuiltinAutomate(ctx, cfg)
	require.NoError(t, err)
	require.Len(t, result.SavedFiles, 1)

	outPath := result.SavedFiles[0]
	assert.FileExists(t, outPath)
	wantDir := filepath.Join(customDir, "my-feature")
	assert.Equal(t, filepath.Join(wantDir, "tc-auto0001-test-skeleton.bats"), outPath,
		"spec should be stamped under the configured output-dir/subdir")
	assert.NotContains(t, filepath.ToSlash(outPath), "test/acceptance",
		"configured output-dir must override the framework default")

	// Wiring artefact must record the configured location (project-relative, forward slashes).
	rec, _, err := wiring.Find(root, "tc-auto0001", "bats")
	require.NoError(t, err)
	require.NotNil(t, rec)
	assert.Contains(t, rec.Artefact, "PLAYWRIGHT-TESTS/tests/gtms/my-feature/tc-auto0001-test-skeleton.bats",
		"wiring artefact should point at the configured output-dir")
}

// TestBuiltinAutomate_UnsetOutputDirUsesFrameworkDefault locks the fallback: with
// output-dir NOT configured (OutputDirConfigured == false), the spec lands in the
// framework-native default (test/acceptance for BATS) -- the greenfield no-regression case.
func TestBuiltinAutomate_UnsetOutputDirUsesFrameworkDefault(t *testing.T) {
	root := setupAutomateFixture(t)
	cfg, err := loadTestConfig(root)
	require.NoError(t, err)

	ctx := &AdapterContext{
		ProjectRoot:  root,
		TestCase:     "tc-auto0001",
		Framework:    "bats",
		TestCaseFile: "gtms/test/cases/my-feature/tc-auto0001-test-skeleton.md",
		OutputSubdir: "my-feature/",
		// OutputDir / OutputDirConfigured deliberately left zero.
	}

	result, err := BuiltinAutomate(ctx, cfg)
	require.NoError(t, err)
	require.Len(t, result.SavedFiles, 1)

	outPath := filepath.ToSlash(result.SavedFiles[0])
	assert.Contains(t, outPath, "test/acceptance/my-feature/tc-auto0001-test-skeleton.bats",
		"unset output-dir should fall back to the BATS framework default")
}

func TestBuiltinAutomate_RejectsUnsupportedFramework(t *testing.T) {
	root := setupAutomateFixture(t)
	cfg, err := loadTestConfig(root)
	require.NoError(t, err)

	// BUG-111: "playwright" was added to the framework registry alongside
	// BATS. Remaining unregistered frameworks still error with "no automate
	// support found"; ENH-112 will add richer Playwright integration.
	for _, fw := range []string{"pester", "cypress"} {
		ctx := &AdapterContext{
			ProjectRoot:  root,
			TestCase:     "tc-auto0001",
			Framework:    fw,
			TestCaseFile: "gtms/test/cases/my-feature/tc-auto0001-test-skeleton.md",
			OutputSubdir: "my-feature/",
		}
		_, err = BuiltinAutomate(ctx, cfg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no automate support found")
		assert.Contains(t, err.Error(), fw)
	}
}

func TestBuiltinAutomate_RejectsNoFramework(t *testing.T) {
	root := setupAutomateFixture(t)
	cfg, err := loadTestConfig(root)
	require.NoError(t, err)

	ctx := &AdapterContext{
		ProjectRoot:  root,
		TestCase:     "tc-auto0001",
		Framework:    "",
		TestCaseFile: "gtms/test/cases/my-feature/tc-auto0001-test-skeleton.md",
	}

	_, err = BuiltinAutomate(ctx, cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no --framework specified")
}

// --- BUG-120: BuiltinAutomate manual-framework diagnostic test ---

func TestBuiltinAutomate_RejectsManualFramework(t *testing.T) {
	root := setupAutomateFixture(t)
	cfg, err := loadTestConfig(root)
	require.NoError(t, err)

	ctx := &AdapterContext{
		ProjectRoot:  root,
		TestCase:     "tc-auto0001",
		Framework:    "manual",
		TestCaseFile: "gtms/test/cases/my-feature/tc-auto0001-test-skeleton.md",
		OutputSubdir: "my-feature/",
	}

	_, err = BuiltinAutomate(ctx, cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not require automate")
	assert.Contains(t, err.Error(), "gtms prime")
	assert.Contains(t, err.Error(), "--adapter manual-execute")
	assert.Contains(t, err.Error(), "tc-auto0001")
}

// --- BUG-107: DeriveArtefactBasename tests ---

func TestDeriveArtefactBasename_SluggedPath(t *testing.T) {
	got := DeriveArtefactBasename("gtms/test/cases/my-feature/tc-aaa-login-happy.md", "tc-aaa")
	assert.Equal(t, "tc-aaa-login-happy", got)
}

func TestDeriveArtefactBasename_BarePath(t *testing.T) {
	got := DeriveArtefactBasename("gtms/test/cases/my-feature/tc-aaa.md", "tc-aaa")
	assert.Equal(t, "tc-aaa", got)
}

func TestDeriveArtefactBasename_EmptyPath(t *testing.T) {
	got := DeriveArtefactBasename("", "tc-fallback")
	assert.Equal(t, "tc-fallback", got)
}

func TestDeriveArtefactBasename_NestedPath(t *testing.T) {
	got := DeriveArtefactBasename("gtms/test/cases/a/b/tc-xxx-deep-slug.md", "tc-xxx")
	assert.Equal(t, "tc-xxx-deep-slug", got)
}

func TestDeriveArtefactBasename_NoExtension(t *testing.T) {
	got := DeriveArtefactBasename("gtms/test/cases/tc-aaa-slug", "tc-aaa")
	assert.Equal(t, "tc-aaa-slug", got)
}

// --- BUG-107: BuiltinAutomate bare spec fallback test ---

func TestBuiltinAutomate_BareSpecFallback(t *testing.T) {
	root := setupBareAutomateFixture(t)
	cfg, err := loadTestConfig(root)
	require.NoError(t, err)

	ctx := &AdapterContext{
		ProjectRoot:  root,
		TestCase:     "tc-auto0001",
		Framework:    "bats",
		TestCaseFile: "gtms/test/cases/my-feature/tc-auto0001.md",
		OutputSubdir: "my-feature/",
	}

	result, err := BuiltinAutomate(ctx, cfg)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	require.Len(t, result.SavedFiles, 1)

	// Bare spec: artefact filename should use bare TC ID
	outPath := result.SavedFiles[0]
	assert.FileExists(t, outPath)
	assert.True(t, strings.HasSuffix(outPath, "tc-auto0001.bats"),
		"bare spec should produce tc-auto0001.bats, got %s", outPath)

	// Wiring artefact should also be bare
	rec, _, err := wiring.Find(root, "tc-auto0001", "bats")
	require.NoError(t, err)
	require.NotNil(t, rec)
	assert.Contains(t, rec.Artefact, "tc-auto0001.bats",
		"wiring artefact should use bare TC ID for bare spec")
}

// loadTestConfig loads a gtms.config from the given root directory.
func loadTestConfig(root string) (*config.Config, error) {
	return config.LoadFromFile(filepath.Join(root, "gtms.config"))
}

// --- ENH-161: template-driven create + prime tests ---

// TestYamlQuotedScalarOrEmpty covers the substitution helper that converts a
// user-controlled value into either a complete YAML double-quoted scalar
// (carrying its own quotes) or the empty string. This is the contract the
// template line `requirement: ${REQUIREMENT}` relies on: the placeholder
// either expands to a properly-quoted scalar OR vanishes, never to a bare
// unquoted scalar carrying YAML-special characters.
func TestYamlQuotedScalarOrEmpty(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"plain", "REQ-001", `"REQ-001"`},
		// Bug regression: `--reference='REQ: value'` previously substituted
		// unquoted, yielding `requirement: REQ: value` (two `: ` mapping
		// starts) which broke YAML parsing.
		{"colon space", "REQ: value", `"REQ: value"`},
		{"leading dash", "-not-a-sequence", `"-not-a-sequence"`},
		{"hash comment", "REQ #42", `"REQ #42"`},
		{"backslash", `dir\path`, `"dir\\path"`},
		{"double quote", `say "hi"`, `"say \"hi\""`},
		{"newline", "line1\nline2", `"line1\nline2"`},
		{"tab", "a\tb", `"a\tb"`},
		{"all risk chars", `/&\":'#*?` + "-prefix", `"/&\\\":'#*?-prefix"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, yamlQuotedScalarOrEmpty(tc.in))
		})
	}
}

// TestBuiltinCreate_ReadsFromTemplateFile verifies the template-read code
// path: BuiltinCreate substitutes placeholders into content sourced from
// ctx.TemplateFile rather than the inline fallback.
func TestBuiltinCreate_ReadsFromTemplateFile(t *testing.T) {
	dir := t.TempDir()
	tmplDir := t.TempDir()
	tmplPath := filepath.Join(tmplDir, "manual-testcase.template.md")
	// Custom template content -- includes a marker not in the fallback so we
	// can prove the on-disk template was used.
	require.NoError(t, os.WriteFile(tmplPath, []byte(`---
test_case_id: ${TESTCASE_ID}
title: "${TITLE}"
requirement: ${REQUIREMENT}
priority: High
type: Functional
created: ${CREATED}
---

## Custom Marker From Template File

`), 0644))

	ctx := &AdapterContext{
		OutputDir:    dir,
		TemplateFile: tmplPath,
		TestCaseIDs:  "tc-aabbccdd",
		Reference:    "REQ-fromTemplate",
	}
	result, err := BuiltinCreate(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Empty(t, result.Stderr, "no fallback warning when template exists")

	data, err := os.ReadFile(result.SavedFiles[0])
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "## Custom Marker From Template File")
	assert.Contains(t, content, `requirement: "REQ-fromTemplate"`)
	assert.Contains(t, content, "test_case_id: tc-aabbccdd")
	// Frontmatter must be parseable.
	var fm struct {
		TestCaseID  string `yaml:"test_case_id"`
		Requirement string `yaml:"requirement"`
	}
	_, parseErr := frontmatter.Parse(strings.NewReader(content), &fm)
	require.NoError(t, parseErr)
	assert.Equal(t, "tc-aabbccdd", fm.TestCaseID)
	assert.Equal(t, "REQ-fromTemplate", fm.Requirement)
}

// TestBuiltinCreate_FallbackOnMissingTemplate verifies the missing-template
// fallback per AC #15: stderr warning naming the expected path, fallback
// shape, exit 0.
func TestBuiltinCreate_FallbackOnMissingTemplate(t *testing.T) {
	dir := t.TempDir()
	missingPath := filepath.Join(t.TempDir(), "does-not-exist.template.md")

	ctx := &AdapterContext{
		OutputDir:    dir,
		TemplateFile: missingPath,
		TestCaseIDs:  "tc-aabbccdd",
	}
	result, err := BuiltinCreate(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode, "missing template is non-fatal per AC #15")
	assert.Contains(t, result.Stderr, "warning:")
	assert.Contains(t, result.Stderr, missingPath, "stderr names the expected path")

	data, err := os.ReadFile(result.SavedFiles[0])
	require.NoError(t, err)
	// Fallback shape contains the same headings as TestcaseTemplateMD.
	assert.Contains(t, string(data), "## Test Objective")
	assert.Contains(t, string(data), "## Test Steps")
}

// TestBuiltinCreate_NonMissingReadErrorSurfaces verifies that read errors
// other than ENOENT (e.g. the template path points at a directory, or the
// file is unreadable) surface as a real error rather than silently
// triggering the fallback. The fallback is reserved for the genuinely
// "template not found" case; everything else is operator-actionable.
func TestBuiltinCreate_NonMissingReadErrorSurfaces(t *testing.T) {
	dir := t.TempDir()
	// Point TemplateFile at a directory -- os.ReadFile returns an "is a
	// directory" error, which is portable between Linux and Windows and
	// is NOT errors.Is(err, os.ErrNotExist).
	dirAsTemplate := filepath.Join(dir, "template-as-dir")
	require.NoError(t, os.Mkdir(dirAsTemplate, 0755))

	ctx := &AdapterContext{
		OutputDir:    dir,
		TemplateFile: dirAsTemplate,
		TestCaseIDs:  "tc-aabbccdd",
	}
	_, err := BuiltinCreate(ctx)
	require.Error(t, err, "non-missing read error must surface")
	assert.Contains(t, err.Error(), "reading create template")
	assert.Contains(t, err.Error(), dirAsTemplate)
}

// TestBuiltinPrime_NonMissingReadErrorSurfaces mirrors the create-side check
// for BuiltinPrime: non-ENOENT read errors must surface, not silently fall
// back.
func TestBuiltinPrime_NonMissingReadErrorSurfaces(t *testing.T) {
	dir := t.TempDir()
	dirAsTemplate := filepath.Join(dir, "template-as-dir")
	require.NoError(t, os.Mkdir(dirAsTemplate, 0755))

	ctx := &AdapterContext{
		TemplateFile: dirAsTemplate,
		OutputFile:   filepath.Join(dir, "out.yaml"),
		TestCase:     "tc-aabbccdd",
	}
	_, err := BuiltinPrime(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading prime template")
	assert.Contains(t, err.Error(), dirAsTemplate)
}

// TestBuiltinCreate_ReferenceWithColon is the regression test for the bug
// where `--reference='REQ: value'` produced unparseable frontmatter
// (`requirement: REQ: value`). After the fix, the value must round-trip
// through YAML parsing intact.
func TestBuiltinCreate_ReferenceWithColon(t *testing.T) {
	dir := t.TempDir()
	ctx := &AdapterContext{
		OutputDir:   dir,
		TestCaseIDs: "tc-aabbccdd",
		Reference:   "REQ: value",
	}
	result, err := BuiltinCreate(ctx)
	require.NoError(t, err)

	data, err := os.ReadFile(result.SavedFiles[0])
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, `requirement: "REQ: value"`, "colon-bearing reference is quoted")

	// Frontmatter must parse cleanly; this is what the bug broke.
	var fm struct {
		Requirement string `yaml:"requirement"`
	}
	_, parseErr := frontmatter.Parse(strings.NewReader(content), &fm)
	require.NoError(t, parseErr, "frontmatter must be parseable YAML")
	assert.Equal(t, "REQ: value", fm.Requirement, "value round-trips through YAML")
}

// TestBuiltinCreate_ReferenceWithRiskSurfaceChars covers AC #25's worst-case
// character coverage for ${REQUIREMENT}: `/`, `&`, `\`, `"`, `'`, leading
// `-`, and YAML-sensitive `:`, `#`, `*`, `?`.
func TestBuiltinCreate_ReferenceWithRiskSurfaceChars(t *testing.T) {
	dir := t.TempDir()
	worstCase := `-REQ/path&branch\quote:"value"'apos'#star*question?`

	ctx := &AdapterContext{
		OutputDir:   dir,
		TestCaseIDs: "tc-aabbccdd",
		Reference:   worstCase,
	}
	result, err := BuiltinCreate(ctx)
	require.NoError(t, err)

	data, err := os.ReadFile(result.SavedFiles[0])
	require.NoError(t, err)
	content := string(data)

	var fm struct {
		Requirement string `yaml:"requirement"`
	}
	_, parseErr := frontmatter.Parse(strings.NewReader(content), &fm)
	require.NoError(t, parseErr, "risk-surface chars must not break YAML")
	assert.Equal(t, worstCase, fm.Requirement, "worst-case value round-trips intact")
}

// TestBuiltinCreate_EmptyReferenceRendersBare covers AC #14: with no
// `--reference`, ${REQUIREMENT} substitutes to the empty string and the
// rendered line is `requirement: ` (bare key, parseable YAML, null value).
func TestBuiltinCreate_EmptyReferenceRendersBare(t *testing.T) {
	dir := t.TempDir()
	ctx := &AdapterContext{
		OutputDir:   dir,
		TestCaseIDs: "tc-aabbccdd",
		// Reference intentionally empty.
	}
	result, err := BuiltinCreate(ctx)
	require.NoError(t, err)

	data, err := os.ReadFile(result.SavedFiles[0])
	require.NoError(t, err)
	content := string(data)

	// Bare key with no value (no quotes inserted). Parseable as null.
	assert.Regexp(t, `(?m)^requirement: *$`, content, "empty reference yields bare key line")

	var fm struct {
		Requirement string `yaml:"requirement"`
	}
	_, parseErr := frontmatter.Parse(strings.NewReader(content), &fm)
	require.NoError(t, parseErr)
	assert.Empty(t, fm.Requirement, "empty value parses as empty string")
}

// TestBuiltinCreate_FallbackIsByteIdenticalToScaffold enforces the AC #15
// invariant that the missing-template fallback shape (adapter-side Go const)
// matches the scaffolded template content (scaffold-side Go const)
// byte-for-byte. Drift between the two would cause Tier 0 fallback output to
// differ from the on-disk template's day-one output, breaking handoff parity.
//
// Both constants are compiled by the same Go toolchain so line-ending and
// encoding normalisation is identical -- the comparison is a true byte
// equality check, not a source-text scan.
func TestBuiltinCreate_FallbackIsByteIdenticalToScaffold(t *testing.T) {
	assert.Equal(t,
		testcaseTemplateFallback,
		scaffold.TestcaseTemplateMD,
		"fallback shape must match scaffold.TestcaseTemplateMD byte-for-byte")
}

// TestBuiltinPrime_FallbackOnMissingTemplate verifies the prime-side missing
// template fallback (AC #15 mirror for prime).
func TestBuiltinPrime_FallbackOnMissingTemplate(t *testing.T) {
	dir := t.TempDir()
	missingPath := filepath.Join(t.TempDir(), "no-such-template.yaml")
	outPath := filepath.Join(dir, "tc-aabbccdd--manual.result.yaml")

	ctx := &AdapterContext{
		TemplateFile: missingPath,
		OutputFile:   outPath,
		TestCase:     "tc-aabbccdd",
		TestCaseHash: "abcdef0123456789",
		Branch:       "main",
	}
	result, err := BuiltinPrime(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Contains(t, result.Stderr, "warning:")
	assert.Contains(t, result.Stderr, missingPath, "stderr names the expected path")

	data, err := os.ReadFile(outPath)
	require.NoError(t, err)
	// Fallback shape contains the GTMS contract block.
	assert.Contains(t, string(data), "test_case_id: tc-aabbccdd")
	assert.Contains(t, string(data), "framework: manual")
}

// TestBuiltinPrime_TCSnapshotFieldsWithRiskChars covers AC #25's prime-stage
// risk surface: TC frontmatter fields snapshotted at prime time may carry
// quotes, colons, backslashes, etc. via operator edits to the TC file. The
// stamped result must remain parseable YAML.
func TestBuiltinPrime_TCSnapshotFieldsWithRiskChars(t *testing.T) {
	dir := t.TempDir()
	tmplPath := filepath.Join(dir, "manual-result.template.yaml")
	require.NoError(t, os.WriteFile(tmplPath, []byte(`test_case_id: ${TESTCASE}
test_case_hash: ${TESTCASE_HASH}
framework: manual
result:
title: "${TC_TITLE}"
requirement: "${TC_REQUIREMENT}"
priority: "${TC_PRIORITY}"
type: "${TC_TYPE}"
branch: ${BRANCH}
`), 0644))

	outPath := filepath.Join(dir, "tc-aabbccdd--manual.result.yaml")
	ctx := &AdapterContext{
		TemplateFile:  tmplPath,
		OutputFile:    outPath,
		TestCase:      "tc-aabbccdd",
		TestCaseHash:  "abcdef0123456789",
		Branch:        "main",
		TCTitle:       `Login: error "msg" with \ slash`,
		TCRequirement: "REQ: value-with-colon",
		TCPriority:    "-Medium",
		TCType:        `Functional #1`,
		Force:         false,
	}
	result, err := BuiltinPrime(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)

	data, err := os.ReadFile(outPath)
	require.NoError(t, err)
	var fm struct {
		Title       string `yaml:"title"`
		Requirement string `yaml:"requirement"`
		Priority    string `yaml:"priority"`
		Type        string `yaml:"type"`
	}
	require.NoError(t, yaml.Unmarshal(data, &fm), "stamped result must be parseable YAML")
	assert.Equal(t, ctx.TCTitle, fm.Title)
	assert.Equal(t, ctx.TCRequirement, fm.Requirement)
	assert.Equal(t, ctx.TCPriority, fm.Priority)
	assert.Equal(t, ctx.TCType, fm.Type)
}

// --- ENH-162: BuiltinAutomate template-orchestration tests ---

func TestBuiltinAutomate_ReadsTemplateFromDisk(t *testing.T) {
	root := setupAutomateFixture(t)
	cfg, err := loadTestConfig(root)
	require.NoError(t, err)

	// Write a custom BATS template with a sentinel
	tmplDir := filepath.Join(root, "gtms", "automation", "templates")
	require.NoError(t, os.MkdirAll(tmplDir, 0755))
	customTmpl := `#!/usr/bin/env bats
# CUSTOM TEMPLATE SENTINEL
# Test case: ${TESTCASE_ID}
setup_file() {
    export PROJECT_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/${PROJECT_ROOT_DEPTH}" && pwd)"
    load "$PROJECT_ROOT/test/test_helper/common-setup.bash"
}
setup() { _common_setup; }
@test "${TESTCASE_ID}: custom" { skip "skeleton"; }
teardown() { :; }
`
	require.NoError(t, os.WriteFile(filepath.Join(tmplDir, "bats.template.bats"), []byte(customTmpl), 0644))

	ctx := &AdapterContext{
		ProjectRoot:  root,
		TestCase:     "tc-auto0001",
		Framework:    "bats",
		TestCaseFile: "gtms/test/cases/my-feature/tc-auto0001-test-skeleton.md",
		OutputSubdir: "my-feature/",
	}

	result, err := BuiltinAutomate(ctx, cfg)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode)
	assert.Empty(t, result.Stderr, "no warning when template exists")

	data, err := os.ReadFile(result.SavedFiles[0])
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "CUSTOM TEMPLATE SENTINEL", "should read from disk template")
	assert.Contains(t, content, "tc-auto0001", "should substitute TC ID")
	assert.NotContains(t, content, "${TESTCASE_ID}", "placeholder should be substituted")
}

func TestBuiltinAutomate_FallbackOnMissingTemplate(t *testing.T) {
	root := setupAutomateFixture(t)
	cfg, err := loadTestConfig(root)
	require.NoError(t, err)

	// Do NOT create the template file -- it should be missing
	ctx := &AdapterContext{
		ProjectRoot:  root,
		TestCase:     "tc-auto0001",
		Framework:    "bats",
		TestCaseFile: "gtms/test/cases/my-feature/tc-auto0001-test-skeleton.md",
		OutputSubdir: "my-feature/",
	}

	result, err := BuiltinAutomate(ctx, cfg)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ExitCode, "missing template is non-fatal")
	assert.Contains(t, result.Stderr, "warning:")
	assert.Contains(t, result.Stderr, "bats.template.bats")
	assert.Contains(t, result.Stderr, "using built-in default")

	// Output should still be a valid BATS skeleton
	data, err := os.ReadFile(result.SavedFiles[0])
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "#!/usr/bin/env bats")
	assert.Contains(t, content, "tc-auto0001")
	assert.Contains(t, content, "_common_setup")
}

func TestBuiltinAutomate_NonMissingReadErrorSurfaces(t *testing.T) {
	root := setupAutomateFixture(t)
	cfg, err := loadTestConfig(root)
	require.NoError(t, err)

	// Create a directory at the template path
	tmplDir := filepath.Join(root, "gtms", "automation", "templates")
	require.NoError(t, os.MkdirAll(tmplDir, 0755))
	tmplPath := filepath.Join(tmplDir, "bats.template.bats")
	require.NoError(t, os.MkdirAll(tmplPath, 0755)) // directory, not a file

	ctx := &AdapterContext{
		ProjectRoot:  root,
		TestCase:     "tc-auto0001",
		Framework:    "bats",
		TestCaseFile: "gtms/test/cases/my-feature/tc-auto0001-test-skeleton.md",
		OutputSubdir: "my-feature/",
	}

	_, err = BuiltinAutomate(ctx, cfg)
	require.Error(t, err, "directory-at-template-path must error")
	assert.Contains(t, err.Error(), "reading bats automate template")
}

func TestBuiltinAutomate_FallbackByteIdenticalToTemplateRead(t *testing.T) {
	// Run BuiltinAutomate twice: once without template (fallback), once with
	// the template file containing the exact same const. The output artefacts
	// must be byte-identical.
	root1 := setupAutomateFixture(t)
	root2 := setupAutomateFixture(t)
	cfg1, err := loadTestConfig(root1)
	require.NoError(t, err)
	cfg2, err := loadTestConfig(root2)
	require.NoError(t, err)

	ctx1 := &AdapterContext{
		ProjectRoot:  root1,
		TestCase:     "tc-auto0001",
		Framework:    "bats",
		TestCaseFile: "gtms/test/cases/my-feature/tc-auto0001-test-skeleton.md",
		OutputSubdir: "my-feature/",
	}

	// root1: no template -> fallback path
	result1, err := BuiltinAutomate(ctx1, cfg1)
	require.NoError(t, err)
	assert.Contains(t, result1.Stderr, "warning:", "fallback path should warn")

	// root2: template from the const -> template-read path
	tmplDir := filepath.Join(root2, "gtms", "automation", "templates")
	require.NoError(t, os.MkdirAll(tmplDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tmplDir, "bats.template.bats"),
		[]byte(scaffold.BATSAutomateTemplate), 0644))

	ctx2 := &AdapterContext{
		ProjectRoot:  root2,
		TestCase:     "tc-auto0001",
		Framework:    "bats",
		TestCaseFile: "gtms/test/cases/my-feature/tc-auto0001-test-skeleton.md",
		OutputSubdir: "my-feature/",
	}
	result2, err := BuiltinAutomate(ctx2, cfg2)
	require.NoError(t, err)
	assert.Empty(t, result2.Stderr, "template-read path should not warn")

	data1, err := os.ReadFile(result1.SavedFiles[0])
	require.NoError(t, err)
	data2, err := os.ReadFile(result2.SavedFiles[0])
	require.NoError(t, err)

	assert.Equal(t, string(data1), string(data2),
		"fallback output must be byte-identical to template-read output when template matches const")
}
