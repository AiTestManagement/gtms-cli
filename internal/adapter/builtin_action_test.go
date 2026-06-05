package adapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/config"
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
	assert.Contains(t, string(data), "requirement: REQ-001")
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
	casesDir := filepath.Join(dir, "gtms", "cases", "test-folder")
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
	casesDir := filepath.Join(dir, "gtms", "cases", "test-folder")
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
	casesDir := filepath.Join(dir, "gtms", "cases", "test-folder")
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
	casesDir := filepath.Join(dir, "gtms", "cases", "test-folder")
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
	tcDir := filepath.Join(root, "gtms", "cases", "my-feature")
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
	tcDir := filepath.Join(root, "gtms", "cases", "my-feature")
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
		TestCaseFile: "gtms/cases/my-feature/tc-auto0001-test-skeleton.md",
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
		TestCaseFile: "gtms/cases/my-feature/tc-auto0001-test-skeleton.md",
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
			TestCaseFile: "gtms/cases/my-feature/tc-auto0001-test-skeleton.md",
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
		TestCaseFile: "gtms/cases/my-feature/tc-auto0001-test-skeleton.md",
	}

	_, err = BuiltinAutomate(ctx, cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no --framework specified")
}

// --- BUG-107: DeriveArtefactBasename tests ---

func TestDeriveArtefactBasename_SluggedPath(t *testing.T) {
	got := DeriveArtefactBasename("gtms/cases/my-feature/tc-aaa-login-happy.md", "tc-aaa")
	assert.Equal(t, "tc-aaa-login-happy", got)
}

func TestDeriveArtefactBasename_BarePath(t *testing.T) {
	got := DeriveArtefactBasename("gtms/cases/my-feature/tc-aaa.md", "tc-aaa")
	assert.Equal(t, "tc-aaa", got)
}

func TestDeriveArtefactBasename_EmptyPath(t *testing.T) {
	got := DeriveArtefactBasename("", "tc-fallback")
	assert.Equal(t, "tc-fallback", got)
}

func TestDeriveArtefactBasename_NestedPath(t *testing.T) {
	got := DeriveArtefactBasename("gtms/cases/a/b/tc-xxx-deep-slug.md", "tc-xxx")
	assert.Equal(t, "tc-xxx-deep-slug", got)
}

func TestDeriveArtefactBasename_NoExtension(t *testing.T) {
	got := DeriveArtefactBasename("gtms/cases/tc-aaa-slug", "tc-aaa")
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
		TestCaseFile: "gtms/cases/my-feature/tc-auto0001.md",
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
