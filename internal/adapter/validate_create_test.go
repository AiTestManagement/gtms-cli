package adapter

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/aitestmanagement/gtms-cli/internal/task"
)

// --- Pure unit tests (no shell, no skipIfShort) ---

func TestValidateCreateSpecs_HappyPath(t *testing.T) {
	dir := t.TempDir()
	batchIDs := []string{"tc-aaaaaaaa", "tc-bbbbbbbb", "tc-cccccccc"}

	// Write 3 well-formed spec files
	for _, id := range batchIDs {
		content := "---\ntest_case_id: " + id + "\ntitle: test\n---\n# Test\n"
		filename := id + "-some-slug.md"
		require.NoError(t, os.WriteFile(filepath.Join(dir, filename), []byte(content), 0644))
	}

	valResult, err := ValidateCreateSpecs(dir, batchIDs, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, valResult.Violations, "well-formed specs should produce no violations")
	assert.Empty(t, valResult.Degraded, "well-formed specs should produce no degraded entries")
}

func TestValidateCreateSpecs_MismatchedID(t *testing.T) {
	dir := t.TempDir()
	batchIDs := []string{"tc-aaaaaaaa", "tc-bbbbbbbb"}

	// Filename says tc-aaaaaaaa, frontmatter says tc-bbbbbbbb
	content := "---\ntest_case_id: tc-bbbbbbbb\ntitle: test\n---\n# Test\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-aaaaaaaa-slug.md"), []byte(content), 0644))

	valResult, err := ValidateCreateSpecs(dir, batchIDs, nil, nil)
	require.NoError(t, err)
	require.Len(t, valResult.Violations, 1)
	assert.Equal(t, "tc-aaaaaaaa-slug.md", valResult.Violations[0].File)
	assert.Contains(t, valResult.Violations[0].Reason, "does not match filename ID")
	assert.Contains(t, valResult.Violations[0].Reason, "tc-bbbbbbbb")
	assert.Contains(t, valResult.Violations[0].Reason, "tc-aaaaaaaa")
}

func TestValidateCreateSpecs_MissingTestCaseID(t *testing.T) {
	// BUG-106: missing test_case_id degrades to filename-only listing
	// instead of producing a hard violation.
	dir := t.TempDir()
	batchIDs := []string{"tc-aaaaaaaa"}

	// Frontmatter has no test_case_id field
	content := "---\ntitle: test\npriority: high\n---\n# Test\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-aaaaaaaa-slug.md"), []byte(content), 0644))

	valResult, err := ValidateCreateSpecs(dir, batchIDs, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, valResult.Violations, "missing test_case_id should not produce a hard violation (BUG-106)")
	require.Len(t, valResult.Degraded, 1)
	assert.Equal(t, "tc-aaaaaaaa-slug.md", valResult.Degraded[0].File)
	assert.Contains(t, valResult.Degraded[0].Reason, "missing required field 'test_case_id'")
}

func TestValidateCreateSpecs_DuplicateID(t *testing.T) {
	dir := t.TempDir()
	batchIDs := []string{"tc-aaaaaaaa", "tc-bbbbbbbb"}

	// Two files both claiming tc-aaaaaaaa in frontmatter
	// (first file has matching filename, second has different filename but same frontmatter ID)
	content1 := "---\ntest_case_id: tc-aaaaaaaa\ntitle: test1\n---\n# Test 1\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-aaaaaaaa-first.md"), []byte(content1), 0644))

	content2 := "---\ntest_case_id: tc-aaaaaaaa\ntitle: test2\n---\n# Test 2\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-bbbbbbbb-second.md"), []byte(content2), 0644))

	valResult, err := ValidateCreateSpecs(dir, batchIDs, nil, nil)
	require.NoError(t, err)

	// Should have violations: duplicate ID on second file, plus mismatch on second file
	var dupViolation *SpecValidationError
	for i := range valResult.Violations {
		if strings.Contains(valResult.Violations[i].Reason, "duplicate test_case_id") {
			dupViolation = &valResult.Violations[i]
			break
		}
	}
	require.NotNil(t, dupViolation, "expected a duplicate test_case_id violation")
	assert.Contains(t, dupViolation.Reason, "tc-aaaaaaaa")
}

func TestValidateCreateSpecs_InventedID(t *testing.T) {
	dir := t.TempDir()
	batchIDs := []string{"tc-aaaaaaaa", "tc-bbbbbbbb"}

	// File uses an ID not in the batch
	inventedID := "tc-deadbeef"
	content := "---\ntest_case_id: " + inventedID + "\ntitle: test\n---\n# Test\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, inventedID+"-slug.md"), []byte(content), 0644))

	valResult, err := ValidateCreateSpecs(dir, batchIDs, nil, nil)
	require.NoError(t, err)

	var inventedViolation *SpecValidationError
	for i := range valResult.Violations {
		if strings.Contains(valResult.Violations[i].Reason, "not in the pre-generated batch") {
			inventedViolation = &valResult.Violations[i]
			break
		}
	}
	require.NotNil(t, inventedViolation, "expected a not-in-batch violation")
	assert.Contains(t, inventedViolation.Reason, inventedID)
}

func TestValidateCreateSpecs_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	batchIDs := []string{"tc-aaaaaaaa"}

	valResult, err := ValidateCreateSpecs(dir, batchIDs, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, valResult.Violations, "empty directory should produce no violations")
	assert.Empty(t, valResult.Degraded, "empty directory should produce no degraded entries")
}

func TestValidateCreateSpecs_NonMdFilesIgnored(t *testing.T) {
	dir := t.TempDir()
	batchIDs := []string{"tc-aaaaaaaa"}

	// Write non-.md files that happen to have tc- prefix
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-aaaaaaaa-slug.txt"), []byte("not markdown"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-aaaaaaaa-slug.yaml"), []byte("not markdown"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Not a spec"), 0644))

	valResult, err := ValidateCreateSpecs(dir, batchIDs, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, valResult.Violations, "non-spec files should be ignored")
}

func TestValidateCreateSpecs_LeavesBadFilesOnDisk(t *testing.T) {
	dir := t.TempDir()
	batchIDs := []string{"tc-aaaaaaaa"}

	// Write a mismatched spec
	badFile := filepath.Join(dir, "tc-aaaaaaaa-slug.md")
	content := "---\ntest_case_id: tc-bbbbbbbb\ntitle: test\n---\n# Test\n"
	require.NoError(t, os.WriteFile(badFile, []byte(content), 0644))

	valResult, err := ValidateCreateSpecs(dir, batchIDs, nil, nil)
	require.NoError(t, err)
	require.NotEmpty(t, valResult.Violations)

	// Verify the file was NOT deleted
	_, statErr := os.Stat(badFile)
	assert.NoError(t, statErr, "bad file should still exist on disk after validation failure")
}

func TestValidateCreateSpecs_MultipleErrors(t *testing.T) {
	// BUG-106: missing test_case_id now degrades instead of hard-failing.
	// File 1 (mismatched ID) is a violation. File 2 (missing test_case_id)
	// is a degraded entry. The combined total across both lists is >= 2.
	dir := t.TempDir()
	batchIDs := []string{"tc-aaaaaaaa", "tc-bbbbbbbb"}

	// File 1: mismatched ID -> violation
	content1 := "---\ntest_case_id: tc-bbbbbbbb\ntitle: test1\n---\n# Test 1\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-aaaaaaaa-first.md"), []byte(content1), 0644))

	// File 2: missing test_case_id -> degraded (BUG-106)
	content2 := "---\ntitle: test2\n---\n# Test 2\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-bbbbbbbb-second.md"), []byte(content2), 0644))

	valResult, err := ValidateCreateSpecs(dir, batchIDs, nil, nil)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(valResult.Violations), 1, "should report violation for mismatched ID")
	assert.Len(t, valResult.Degraded, 1, "should report degraded entry for missing test_case_id")
}

func TestValidateCreateSpecs_NonExistentDir(t *testing.T) {
	valResult, err := ValidateCreateSpecs("/nonexistent/path/that/does/not/exist", []string{"tc-aaaaaaaa"}, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, valResult.Violations, "non-existent directory should produce no violations")
}

func TestValidateCreateSpecs_EmptyOutputDir(t *testing.T) {
	valResult, err := ValidateCreateSpecs("", []string{"tc-aaaaaaaa"}, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, valResult.Violations, "empty outputDir should produce no violations")
}

func TestValidateCreateSpecs_IgnoresNestedFiles(t *testing.T) {
	// BUG-040 re-opening: the validator walks top-level only. A pre-existing
	// spec in a subdirectory of outputDir -- e.g. from an earlier
	// `gtms create parent/sub-a` -- must NOT be inspected when a later
	// `gtms create parent` invocation validates its own output. Otherwise
	// the nested file's frontmatter ID (not in parent's batch) would trip
	// check 4 and fail the parent invocation.
	dir := t.TempDir()
	batchIDs := []string{"tc-aaaaaaaa"}

	// Top-level file: well-formed, in the batch -- should validate cleanly.
	topContent := "---\ntest_case_id: tc-aaaaaaaa\ntitle: top\n---\n# Top\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-aaaaaaaa-top.md"), []byte(topContent), 0644))

	// Nested file: frontmatter ID mismatches filename AND is not in the batch.
	// If the validator descended, this would produce two violations.
	subDir := filepath.Join(dir, "sub-a")
	require.NoError(t, os.MkdirAll(subDir, 0755))
	nestedContent := "---\ntest_case_id: tc-22222222\ntitle: nested drift\n---\n# Nested\n"
	require.NoError(t, os.WriteFile(filepath.Join(subDir, "tc-11111111-nested-drift.md"), []byte(nestedContent), 0644))

	valResult, err := ValidateCreateSpecs(dir, batchIDs, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, valResult.Violations, "nested files must not be inspected; only the clean top-level file is in scope")
}

func TestFormatValidationErrors_Empty(t *testing.T) {
	formatted := FormatValidationErrors(nil)
	assert.Empty(t, formatted)
}

func TestFormatValidationErrors_Single(t *testing.T) {
	violations := []SpecValidationError{
		{File: "tc-aaaaaaaa-slug.md", Reason: "frontmatter test_case_id 'tc-bbbbbbbb' does not match filename ID 'tc-aaaaaaaa'"},
	}
	formatted := FormatValidationErrors(violations)
	assert.Contains(t, formatted, "1 violation(s)")
	assert.Contains(t, formatted, "tc-aaaaaaaa-slug.md")
	assert.Contains(t, formatted, "does not match filename ID")
}

func TestFormatValidationErrors_Multiple(t *testing.T) {
	violations := []SpecValidationError{
		{File: "tc-aaaaaaaa-slug.md", Reason: "mismatch"},
		{File: "tc-bbbbbbbb-slug.md", Reason: "missing field"},
	}
	formatted := FormatValidationErrors(violations)
	assert.Contains(t, formatted, "2 violation(s)")
	assert.Contains(t, formatted, "tc-aaaaaaaa-slug.md")
	assert.Contains(t, formatted, "tc-bbbbbbbb-slug.md")
}

// --- Integration tests (shell-dependent, skip in short mode) ---

func TestBUG038_CreateValidatorRejectsIDMismatch(t *testing.T) {
	skipIfShort(t)
	if runtime.GOOS == "windows" {
		t.Skip("Tier 1 sh -c adapter tests require Unix shell")
	}

	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	// Tier 1 adapter that writes a spec file with a mismatched frontmatter ID.
	// The adapter writes to gtms/test/cases/bug038test/ which is the target folder.
	outputDir := filepath.Join(root, "gtms/test/cases", "bug038test")
	require.NoError(t, os.MkdirAll(outputDir, 0755))

	// The command template uses sh -c, which will write a spec file.
	// We can't know the batch IDs ahead of time (they're generated at invocation),
	// so we write a frontmatter ID that is clearly wrong regardless of batch.
	cmdTemplate := `sh -c 'echo "---
test_case_id: tc-deadbeef
title: bad spec
---
# Test" > "` + filepath.ToSlash(outputDir) + `/tc-aaaaaaaa-bad-spec.md"'`

	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock-tier1-bad",
		Config:  &config.AdapterConfig{Mode: "sync", Command: cmdTemplate},
		Tier:    1,
		Mode:    "sync",
	}

	flags := CommandFlags{Folder: "bug038test"}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "bug038test", flags)

	// The validator should catch the mismatch and return a result (not an error)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "error", res.Status)
	assert.Contains(t, res.Summary, "does not match filename ID")

	// Verify task was moved to error
	errorTasks, taskErr := task.List(root, "error")
	require.NoError(t, taskErr)
	assert.Len(t, errorTasks, 1)

	// Verify result contract has validation-error
	rcPath := result.ResultPath(root, res.TaskID)
	rc, rcErr := result.Read(rcPath)
	require.NoError(t, rcErr)
	assert.Equal(t, "error", rc.Status)

	// Verify the bad file is still on disk
	badFile := filepath.Join(outputDir, "tc-aaaaaaaa-bad-spec.md")
	_, statErr := os.Stat(badFile)
	assert.NoError(t, statErr, "bad file should remain on disk for inspection")
}

func TestBUG038_CreateValidatorHappyPath(t *testing.T) {
	skipIfShort(t)
	if runtime.GOOS == "windows" {
		t.Skip("Tier 1 sh -c adapter tests require Unix shell")
	}

	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	outputDir := filepath.Join(root, "gtms/test/cases", "bug038ok")
	require.NoError(t, os.MkdirAll(outputDir, 0755))

	// Adapter that writes no spec files (just echoes).
	// The validator should find zero files to check and pass through.
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock-tier1-ok",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `echo "all good"`},
		Tier:    1,
		Mode:    "sync",
	}

	flags := CommandFlags{Folder: "bug038ok"}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "bug038ok", flags)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "complete", res.Status, "happy path should complete successfully")
}

// --- BUG-040: pre-existing file exclusion tests ---

func TestValidateCreateSpecs_SkipsPreExistingFiles(t *testing.T) {
	dir := t.TempDir()

	// Pre-existing file from a prior invocation (not in current batch)
	preContent := "---\ntest_case_id: tc-aaaaaaaa\ntitle: old test\n---\n# Old Test\n"
	preFile := "tc-aaaaaaaa-old-slug.md"
	require.NoError(t, os.WriteFile(filepath.Join(dir, preFile), []byte(preContent), 0644))

	// New file from current invocation (in current batch)
	newContent := "---\ntest_case_id: tc-bbbbbbbb\ntitle: new test\n---\n# New Test\n"
	newFile := "tc-bbbbbbbb-new-slug.md"
	require.NoError(t, os.WriteFile(filepath.Join(dir, newFile), []byte(newContent), 0644))

	// Only tc-bbbbbbbb is in the current batch
	batchIDs := []string{"tc-bbbbbbbb"}

	// Mark tc-aaaaaaaa-old-slug.md as pre-existing
	preExisting := map[string]struct{}{
		preFile: {},
	}

	valResult, err := ValidateCreateSpecs(dir, batchIDs, preExisting, nil)
	require.NoError(t, err)
	assert.Empty(t, valResult.Violations, "pre-existing file should be skipped; new file is in batch")
}

func TestValidateCreateSpecs_StillCatchesNewFileViolations(t *testing.T) {
	dir := t.TempDir()

	// Pre-existing valid file (will be skipped)
	preContent := "---\ntest_case_id: tc-aaaaaaaa\ntitle: old test\n---\n# Old Test\n"
	preFile := "tc-aaaaaaaa-old-slug.md"
	require.NoError(t, os.WriteFile(filepath.Join(dir, preFile), []byte(preContent), 0644))

	// New file with mismatched frontmatter ID (should be caught)
	newContent := "---\ntest_case_id: tc-deadbeef\ntitle: bad test\n---\n# Bad Test\n"
	newFile := "tc-bbbbbbbb-bad-slug.md"
	require.NoError(t, os.WriteFile(filepath.Join(dir, newFile), []byte(newContent), 0644))

	batchIDs := []string{"tc-bbbbbbbb"}
	preExisting := map[string]struct{}{
		preFile: {},
	}

	valResult, err := ValidateCreateSpecs(dir, batchIDs, preExisting, nil)
	require.NoError(t, err)
	require.NotEmpty(t, valResult.Violations, "new file with mismatched ID should still be caught")

	// Only the new file should be reported, not the pre-existing one
	for _, v := range valResult.Violations {
		assert.Equal(t, newFile, v.File, "violation should only be on the new file")
	}
}

func TestValidateCreateSpecs_NilPreExistingValidatesAll(t *testing.T) {
	dir := t.TempDir()

	// A file not in the batch -- when preExisting is nil, it should be validated (and fail)
	content := "---\ntest_case_id: tc-aaaaaaaa\ntitle: test\n---\n# Test\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-aaaaaaaa-slug.md"), []byte(content), 0644))

	// Batch does NOT include tc-aaaaaaaa
	batchIDs := []string{"tc-bbbbbbbb"}

	valResult, err := ValidateCreateSpecs(dir, batchIDs, nil, nil)
	require.NoError(t, err)
	require.NotEmpty(t, valResult.Violations, "nil preExisting should validate all files")
	assert.Contains(t, valResult.Violations[0].Reason, "not in the pre-generated batch")
}

func TestBUG040_IterativeCreateSucceeds(t *testing.T) {
	skipIfShort(t)
	if runtime.GOOS == "windows" {
		t.Skip("Tier 1 sh -c adapter tests require Unix shell")
	}

	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	outputDir := filepath.Join(root, "gtms/test/cases", "bug040iter")
	require.NoError(t, os.MkdirAll(outputDir, 0755))

	// Simulate a pre-existing spec file from a prior invocation.
	// This file has an ID NOT in the current batch -- the exact scenario BUG-040 describes.
	preContent := "---\ntest_case_id: tc-11111111\ntitle: prior invocation spec\n---\n# Prior\n"
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "tc-11111111-prior-spec.md"), []byte(preContent), 0644))

	// Adapter that writes no new spec files (just echoes). The validator should
	// see the pre-existing file in the snapshot and skip it, resulting in success.
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock-tier1-iter",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `echo "second invocation"`},
		Tier:    1,
		Mode:    "sync",
	}

	flags := CommandFlags{Folder: "bug040iter"}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "bug040iter", flags)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "complete", res.Status, "iterative create should succeed with pre-existing files")

	// Verify the pre-existing file is still on disk
	_, statErr := os.Stat(filepath.Join(outputDir, "tc-11111111-prior-spec.md"))
	assert.NoError(t, statErr, "pre-existing file should remain untouched")
}

// --- BUG-104: bare tc-{8hex}.md filename validation tests ---

func TestValidateCreateSpecs_BareID_HappyPath(t *testing.T) {
	// AC 1: bare tc-{8hex}.md with valid frontmatter and batch ID passes validation.
	dir := t.TempDir()
	batchIDs := []string{"tc-aaaaaaaa", "tc-bbbbbbbb"}

	content := "---\ntest_case_id: tc-aaaaaaaa\ntitle: bare file test\n---\n# Test\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-aaaaaaaa.md"), []byte(content), 0644))

	valResult, err := ValidateCreateSpecs(dir, batchIDs, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, valResult.Violations, "bare tc-{8hex}.md with valid frontmatter should produce no violations")
	assert.Empty(t, valResult.Degraded, "bare tc-{8hex}.md with valid frontmatter should produce no degraded entries")
}

func TestValidateCreateSpecs_BareID_MissingTestCaseID(t *testing.T) {
	// BUG-106: bare tc-{8hex}.md with missing test_case_id degrades
	// to filename-only listing instead of hard-failing.
	dir := t.TempDir()
	batchIDs := []string{"tc-aaaaaaaa"}

	content := "---\ntitle: no id field\npriority: high\n---\n# Test\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-aaaaaaaa.md"), []byte(content), 0644))

	valResult, err := ValidateCreateSpecs(dir, batchIDs, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, valResult.Violations, "missing test_case_id on bare file should not hard-fail (BUG-106)")
	require.Len(t, valResult.Degraded, 1)
	assert.Equal(t, "tc-aaaaaaaa.md", valResult.Degraded[0].File)
	assert.Contains(t, valResult.Degraded[0].Reason, "missing required field 'test_case_id'")
}

func TestValidateCreateSpecs_BareID_MalformedTestCaseID(t *testing.T) {
	// AC 2: bare tc-{8hex}.md with malformed test_case_id format fails.
	// This is a parseable but invalid test_case_id -- strict validation applies.
	dir := t.TempDir()
	batchIDs := []string{"tc-aaaaaaaa"}

	content := "---\ntest_case_id: not-a-valid-id\ntitle: bad format\n---\n# Test\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-aaaaaaaa.md"), []byte(content), 0644))

	valResult, err := ValidateCreateSpecs(dir, batchIDs, nil, nil)
	require.NoError(t, err)
	require.Len(t, valResult.Violations, 1)
	assert.Equal(t, "tc-aaaaaaaa.md", valResult.Violations[0].File)
	assert.Contains(t, valResult.Violations[0].Reason, "does not match expected format tc-{8hex}")
}

func TestValidateCreateSpecs_BareID_MismatchedID(t *testing.T) {
	// AC 3: bare tc-{8hex}.md whose frontmatter ID differs from filename ID fails.
	dir := t.TempDir()
	batchIDs := []string{"tc-aaaaaaaa", "tc-bbbbbbbb"}

	content := "---\ntest_case_id: tc-bbbbbbbb\ntitle: mismatched\n---\n# Test\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-aaaaaaaa.md"), []byte(content), 0644))

	valResult, err := ValidateCreateSpecs(dir, batchIDs, nil, nil)
	require.NoError(t, err)
	require.NotEmpty(t, valResult.Violations)

	var mismatchViolation *SpecValidationError
	for i := range valResult.Violations {
		if strings.Contains(valResult.Violations[i].Reason, "does not match filename ID") {
			mismatchViolation = &valResult.Violations[i]
			break
		}
	}
	require.NotNil(t, mismatchViolation, "expected ID-mismatch violation for bare file")
	assert.Equal(t, "tc-aaaaaaaa.md", mismatchViolation.File)
	assert.Contains(t, mismatchViolation.Reason, "tc-bbbbbbbb")
	assert.Contains(t, mismatchViolation.Reason, "tc-aaaaaaaa")
}

func TestValidateCreateSpecs_BareID_NotInBatch(t *testing.T) {
	// AC 4: bare tc-{8hex}.md with valid frontmatter but ID not in batch fails.
	dir := t.TempDir()
	batchIDs := []string{"tc-bbbbbbbb"} // tc-aaaaaaaa is NOT in the batch

	content := "---\ntest_case_id: tc-aaaaaaaa\ntitle: out of batch\n---\n# Test\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-aaaaaaaa.md"), []byte(content), 0644))

	valResult, err := ValidateCreateSpecs(dir, batchIDs, nil, nil)
	require.NoError(t, err)
	require.NotEmpty(t, valResult.Violations)

	var batchViolation *SpecValidationError
	for i := range valResult.Violations {
		if strings.Contains(valResult.Violations[i].Reason, "not in the pre-generated batch") {
			batchViolation = &valResult.Violations[i]
			break
		}
	}
	require.NotNil(t, batchViolation, "expected not-in-batch violation for bare file")
	assert.Equal(t, "tc-aaaaaaaa.md", batchViolation.File)
	assert.Contains(t, batchViolation.Reason, "tc-aaaaaaaa")
}

func TestValidateCreateSpecs_BareAndSlugged_DuplicateID(t *testing.T) {
	// AC 5: one bare + one slugged file sharing test_case_id are reported as duplicates.
	dir := t.TempDir()
	batchIDs := []string{"tc-aaaaaaaa"}

	// Bare file claims tc-aaaaaaaa
	bare := "---\ntest_case_id: tc-aaaaaaaa\ntitle: bare\n---\n# Bare\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-aaaaaaaa.md"), []byte(bare), 0644))

	// Slugged file also claims tc-aaaaaaaa (via frontmatter)
	slugged := "---\ntest_case_id: tc-aaaaaaaa\ntitle: slugged\n---\n# Slugged\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-aaaaaaaa-some-slug.md"), []byte(slugged), 0644))

	valResult, err := ValidateCreateSpecs(dir, batchIDs, nil, nil)
	require.NoError(t, err)

	var dupViolation *SpecValidationError
	for i := range valResult.Violations {
		if strings.Contains(valResult.Violations[i].Reason, "duplicate test_case_id") {
			dupViolation = &valResult.Violations[i]
			break
		}
	}
	require.NotNil(t, dupViolation, "expected duplicate violation when bare+slugged share test_case_id")
	assert.Contains(t, dupViolation.Reason, "tc-aaaaaaaa")
}

func TestValidateCreateSpecs_MalformedShape(t *testing.T) {
	// AC 6: malformed filename matching tc-{8hex} but neither bare nor slugged form
	// (e.g. tc-aaaaaaaafoo.md) fails with a prescriptive shape error.
	dir := t.TempDir()
	batchIDs := []string{"tc-aaaaaaaa"}

	content := "---\ntest_case_id: tc-aaaaaaaa\ntitle: malformed\n---\n# Test\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-aaaaaaaafoo.md"), []byte(content), 0644))

	valResult, err := ValidateCreateSpecs(dir, batchIDs, nil, nil)
	require.NoError(t, err)
	require.Len(t, valResult.Violations, 1)
	assert.Equal(t, "tc-aaaaaaaafoo.md", valResult.Violations[0].File)
	assert.Contains(t, valResult.Violations[0].Reason, "not a valid shape")
	assert.Contains(t, valResult.Violations[0].Reason, "tc-{8hex}.md")
	assert.Contains(t, valResult.Violations[0].Reason, "tc-{8hex}-slug.md")
}

func TestValidateCreateSpecs_BareID_PreExistingSkipped(t *testing.T) {
	// AC 9: bare-ID file in the preExisting set is silently skipped (BUG-040 regression).
	dir := t.TempDir()
	batchIDs := []string{"tc-bbbbbbbb"} // tc-aaaaaaaa intentionally NOT in batch

	// Bare file from a prior invocation -- not in current batch, but preExisting
	content := "---\ntest_case_id: tc-aaaaaaaa\ntitle: old bare\n---\n# Old\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-aaaaaaaa.md"), []byte(content), 0644))

	preExisting := map[string]struct{}{
		"tc-aaaaaaaa.md": {},
	}

	valResult, err := ValidateCreateSpecs(dir, batchIDs, preExisting, nil)
	require.NoError(t, err)
	assert.Empty(t, valResult.Violations, "pre-existing bare file should be silently skipped")
}

func TestValidateCreateSpecs_MalformedShape_PreExistingSkipped(t *testing.T) {
	// AC 9 extension: malformed-shape file in preExisting set is silently skipped.
	dir := t.TempDir()
	batchIDs := []string{"tc-bbbbbbbb"}

	content := "---\ntest_case_id: tc-aaaaaaaa\ntitle: malformed old\n---\n# Old\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-aaaaaaaafoo.md"), []byte(content), 0644))

	preExisting := map[string]struct{}{
		"tc-aaaaaaaafoo.md": {},
	}

	valResult, err := ValidateCreateSpecs(dir, batchIDs, preExisting, nil)
	require.NoError(t, err)
	assert.Empty(t, valResult.Violations, "pre-existing malformed file should be silently skipped")
}

// --- BUG-106: degraded listing policy tests ---

func TestValidateCreateSpecs_BUG106_MissingFrontmatter_Degrades(t *testing.T) {
	// BUG-106: a bare file with NO YAML frontmatter at all degrades to
	// filename-only listing instead of hard-failing. This is the core
	// policy pin for tc-bf911180.
	dir := t.TempDir()
	batchIDs := []string{"tc-aaaaaaaa"}

	// File has no --- delimiters, just body content.
	content := "# Missing frontmatter fixture\n\nThis file intentionally has no YAML frontmatter.\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-aaaaaaaa.md"), []byte(content), 0644))

	valResult, err := ValidateCreateSpecs(dir, batchIDs, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, valResult.Violations, "missing frontmatter should not produce a hard violation")
	require.Len(t, valResult.Degraded, 1)
	assert.Equal(t, "tc-aaaaaaaa.md", valResult.Degraded[0].File)
}

func TestValidateCreateSpecs_BUG106_MalformedYAML_Degrades(t *testing.T) {
	// BUG-106: a bare file with malformed YAML frontmatter degrades to
	// filename-only listing instead of hard-failing. This is the core
	// policy pin for tc-e678f58f.
	//
	// The frontmatter library may partially parse malformed YAML. When it
	// can extract something but test_case_id is empty or garbled, the file
	// degrades via the "missing test_case_id" path. When it fails entirely,
	// the file degrades via the "could not parse frontmatter" path. Either
	// way, the result is Degraded (not Violations).
	dir := t.TempDir()
	batchIDs := []string{"tc-aaaaaaaa"}

	// Malformed YAML: unterminated quote, no closing --- fence.
	// The frontmatter library may partially parse this, yielding an empty
	// test_case_id. Either degradation path (parse error or missing ID) is
	// acceptable -- the key assertion is that no hard violation is produced.
	content := "---\ntest_case_id: tc-aaaaaaaa\ntitle: \"open quote with colon: parser should fail\nrequirement: BUG-106\n\nbody has no closing fence\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-aaaaaaaa.md"), []byte(content), 0644))

	valResult, err := ValidateCreateSpecs(dir, batchIDs, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, valResult.Violations, "malformed YAML should not produce a hard violation")
	require.Len(t, valResult.Degraded, 1)
	assert.Equal(t, "tc-aaaaaaaa.md", valResult.Degraded[0].File)
}

func TestValidateCreateSpecs_BUG106_MixedValidAndDegraded(t *testing.T) {
	// BUG-106: when one file is valid and another has missing frontmatter,
	// the valid sibling should pass strict checks while the missing-frontmatter
	// sibling degrades. No hard violations should appear for the degraded file.
	// This is the policy pin for tc-a5159593.
	dir := t.TempDir()
	batchIDs := []string{"tc-aaaaaaaa", "tc-bbbbbbbb"}

	// Valid file
	validContent := "---\ntest_case_id: tc-aaaaaaaa\ntitle: Valid sibling title\n---\n# Valid\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-aaaaaaaa.md"), []byte(validContent), 0644))

	// File with no frontmatter at all -- degrades to filename-only.
	noFrontmatterContent := "# No frontmatter\n\nThis file has no YAML header.\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-bbbbbbbb.md"), []byte(noFrontmatterContent), 0644))

	valResult, err := ValidateCreateSpecs(dir, batchIDs, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, valResult.Violations, "mixed batch should produce no hard violations")
	require.Len(t, valResult.Degraded, 1, "only the no-frontmatter file should degrade")
	assert.Equal(t, "tc-bbbbbbbb.md", valResult.Degraded[0].File)
}

func TestValidateCreateSpecs_BUG106_ParseableIDOutsideBatch_HardFails(t *testing.T) {
	// BUG-106 precedence: when test_case_id IS parseable, strict validation
	// applies. An out-of-batch parseable ID is a violation, not degradation.
	// This is the policy pin for tc-1e350bd6.
	dir := t.TempDir()
	batchIDs := []string{"tc-bbbbbbbb"} // tc-99999999 is NOT in the batch

	content := "---\ntest_case_id: tc-99999999\ntitle: Out of batch fixture\n---\n# Test\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-99999999.md"), []byte(content), 0644))

	valResult, err := ValidateCreateSpecs(dir, batchIDs, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, valResult.Degraded, "parseable ID outside batch should not degrade")
	require.NotEmpty(t, valResult.Violations, "parseable ID outside batch should produce a violation")
	assert.Contains(t, valResult.Violations[0].Reason, "not in the pre-generated batch")
}

func TestValidateCreateSpecs_BUG106_SluggedMissingTestCaseID_Degrades(t *testing.T) {
	// BUG-106: the degradation policy applies uniformly to both bare and
	// slugged files. A slugged file with missing test_case_id degrades.
	dir := t.TempDir()
	batchIDs := []string{"tc-aaaaaaaa"}

	content := "---\ntitle: no id field\npriority: high\n---\n# Test\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-aaaaaaaa-some-slug.md"), []byte(content), 0644))

	valResult, err := ValidateCreateSpecs(dir, batchIDs, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, valResult.Violations, "missing test_case_id on slugged file should not hard-fail (BUG-106)")
	require.Len(t, valResult.Degraded, 1)
	assert.Equal(t, "tc-aaaaaaaa-some-slug.md", valResult.Degraded[0].File)
}

// --- BUG-110: ownedFiles scoping tests ---

func TestValidateCreateSpecs_OwnedFiles_OnlyValidatesOwned(t *testing.T) {
	// BUG-110 Option A: when ownedFiles is non-nil, only files in that
	// set enter validation. A sibling file (out-of-batch for the current
	// invocation) in the same directory is silently skipped.
	dir := t.TempDir()
	batchA := []string{"tc-aaaaaaaa"}

	// File owned by this invocation -- in batchA, should validate cleanly.
	ownedContent := "---\ntest_case_id: tc-aaaaaaaa\ntitle: owned\n---\n# Owned\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-aaaaaaaa-owned.md"), []byte(ownedContent), 0644))

	// Sibling file from another invocation -- NOT in batchA. Without
	// ownedFiles, this would produce a "not in the pre-generated batch"
	// violation. With ownedFiles, it must be silently skipped.
	siblingContent := "---\ntest_case_id: tc-bbbbbbbb\ntitle: sibling\n---\n# Sibling\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-bbbbbbbb-sibling.md"), []byte(siblingContent), 0644))

	ownedFiles := map[string]struct{}{
		"tc-aaaaaaaa-owned.md": {},
	}

	valResult, err := ValidateCreateSpecs(dir, batchA, nil, ownedFiles)
	require.NoError(t, err)
	assert.Empty(t, valResult.Violations, "sibling file should be skipped when ownedFiles is non-nil")
	assert.Empty(t, valResult.Degraded, "no degraded entries expected")
}

func TestValidateCreateSpecs_OwnedFiles_StillCatchesOwnedViolations(t *testing.T) {
	// BUG-110 Option A: files in ownedFiles still undergo strict
	// validation. An owned file with a mismatched frontmatter ID
	// must produce a violation.
	dir := t.TempDir()
	batchA := []string{"tc-aaaaaaaa"}

	// Owned file with mismatched frontmatter ID -- should produce violation.
	badContent := "---\ntest_case_id: tc-deadbeef\ntitle: bad\n---\n# Bad\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-aaaaaaaa-bad.md"), []byte(badContent), 0644))

	ownedFiles := map[string]struct{}{
		"tc-aaaaaaaa-bad.md": {},
	}

	valResult, err := ValidateCreateSpecs(dir, batchA, nil, ownedFiles)
	require.NoError(t, err)
	require.NotEmpty(t, valResult.Violations, "owned file with mismatched ID must produce a violation")
	assert.Contains(t, valResult.Violations[0].Reason, "does not match filename ID")
}

func TestValidateCreateSpecs_OwnedFiles_NilFallsBackToDirScan(t *testing.T) {
	// BUG-110 Option A backward compatibility: when ownedFiles is nil,
	// all files enter validation (same as pre-BUG-110 behavior).
	dir := t.TempDir()
	batchA := []string{"tc-aaaaaaaa"}

	// File NOT in batchA -- should produce a violation when ownedFiles is nil.
	content := "---\ntest_case_id: tc-bbbbbbbb\ntitle: out of batch\n---\n# Test\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-bbbbbbbb-slug.md"), []byte(content), 0644))

	valResult, err := ValidateCreateSpecs(dir, batchA, nil, nil)
	require.NoError(t, err)
	require.NotEmpty(t, valResult.Violations, "nil ownedFiles should validate all files")
	assert.Contains(t, valResult.Violations[0].Reason, "not in the pre-generated batch")
}

func TestValidateCreateSpecs_ConcurrentSimulation(t *testing.T) {
	// BUG-110 AC #11: deterministic reproduction of the original race.
	// Two invocations share the same OutputDir. Each has its own batch
	// and its own ownedFiles set. Both should validate successfully --
	// neither should trip on the other's files.
	dir := t.TempDir()

	batchA := []string{"tc-aaaaaaaa"}
	batchB := []string{"tc-bbbbbbbb"}

	// Invocation A's file
	contentA := "---\ntest_case_id: tc-aaaaaaaa\ntitle: invocation A\n---\n# A\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-aaaaaaaa-inv-a.md"), []byte(contentA), 0644))

	// Invocation B's file
	contentB := "---\ntest_case_id: tc-bbbbbbbb\ntitle: invocation B\n---\n# B\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-bbbbbbbb-inv-b.md"), []byte(contentB), 0644))

	ownedA := map[string]struct{}{
		"tc-aaaaaaaa-inv-a.md": {},
	}
	ownedB := map[string]struct{}{
		"tc-bbbbbbbb-inv-b.md": {},
	}

	// Invocation A validates its own file only
	valResultA, errA := ValidateCreateSpecs(dir, batchA, nil, ownedA)
	require.NoError(t, errA)
	assert.Empty(t, valResultA.Violations, "invocation A should pass -- tc-bbbbbbbb is not its concern")

	// Invocation B validates its own file only
	valResultB, errB := ValidateCreateSpecs(dir, batchB, nil, ownedB)
	require.NoError(t, errB)
	assert.Empty(t, valResultB.Violations, "invocation B should pass -- tc-aaaaaaaa is not its concern")

	// Without ownedFiles, invocation A would fail on tc-bbbbbbbb
	valResultNoOwned, errNoOwned := ValidateCreateSpecs(dir, batchA, nil, nil)
	require.NoError(t, errNoOwned)
	require.NotEmpty(t, valResultNoOwned.Violations, "without ownedFiles, invocation A should fail on sibling file")
	assert.Contains(t, valResultNoOwned.Violations[0].Reason, "not in the pre-generated batch")
}

func TestValidateCreateSpecs_OwnedFiles_PreExistingTakesPrecedence(t *testing.T) {
	// Edge case: a file is in both preExisting and ownedFiles.
	// preExisting should take precedence (skip the file entirely).
	dir := t.TempDir()
	batchA := []string{"tc-aaaaaaaa"}

	content := "---\ntest_case_id: tc-aaaaaaaa\ntitle: test\n---\n# Test\n"
	filename := "tc-aaaaaaaa-slug.md"
	require.NoError(t, os.WriteFile(filepath.Join(dir, filename), []byte(content), 0644))

	preExisting := map[string]struct{}{
		filename: {},
	}
	ownedFiles := map[string]struct{}{
		filename: {},
	}

	valResult, err := ValidateCreateSpecs(dir, batchA, preExisting, ownedFiles)
	require.NoError(t, err)
	assert.Empty(t, valResult.Violations, "preExisting should take precedence over ownedFiles")
}
