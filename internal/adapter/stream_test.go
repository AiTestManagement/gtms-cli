package adapter

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/result"
)

func TestParseStreaming_MultipleFiles(t *testing.T) {
	input := "<gtms-file name=\"test1.md\">\nHello World\n</gtms-file>\n<gtms-file name=\"test2.md\">\nSecond file\nWith two lines\n</gtms-file>\n<gtms-file name=\"test3.md\">\nThird\n</gtms-file>"
	outputDir := t.TempDir()

	res, err := parseStreamingOutput(strings.NewReader(input), outputDir)
	require.NoError(t, err)

	assert.Empty(t, res.Summary)
	assert.Len(t, res.SavedFiles, 3)

	content1, err := os.ReadFile(filepath.Join(outputDir, "test1.md"))
	require.NoError(t, err)
	assert.Equal(t, "Hello World\n", string(content1))

	content2, err := os.ReadFile(filepath.Join(outputDir, "test2.md"))
	require.NoError(t, err)
	assert.Equal(t, "Second file\nWith two lines\n", string(content2))

	content3, err := os.ReadFile(filepath.Join(outputDir, "test3.md"))
	require.NoError(t, err)
	assert.Equal(t, "Third\n", string(content3))
}

func TestParseStreaming_NoDelimiters(t *testing.T) {
	input := "plain text output\nwith multiple lines"
	outputDir := t.TempDir()

	res, err := parseStreamingOutput(strings.NewReader(input), outputDir)
	require.NoError(t, err)

	assert.Equal(t, "plain text output\nwith multiple lines", res.Summary)
	assert.Nil(t, res.SavedFiles)
}

func TestParseStreaming_SingleFile(t *testing.T) {
	input := "<gtms-file name=\"output.txt\">\nSingle file content\n</gtms-file>"
	outputDir := t.TempDir()

	res, err := parseStreamingOutput(strings.NewReader(input), outputDir)
	require.NoError(t, err)

	assert.Empty(t, res.Summary)
	assert.Len(t, res.SavedFiles, 1)

	content, err := os.ReadFile(filepath.Join(outputDir, "output.txt"))
	require.NoError(t, err)
	assert.Equal(t, "Single file content\n", string(content))
}

func TestParseStreaming_SummaryBeforeFirstDelimiter(t *testing.T) {
	input := "Preamble line 1\nPreamble line 2\n<gtms-file name=\"file1.md\">\nFile content\n</gtms-file>"
	outputDir := t.TempDir()

	res, err := parseStreamingOutput(strings.NewReader(input), outputDir)
	require.NoError(t, err)

	assert.Equal(t, "Preamble line 1\nPreamble line 2", res.Summary)
	assert.Len(t, res.SavedFiles, 1)

	content, err := os.ReadFile(filepath.Join(outputDir, "file1.md"))
	require.NoError(t, err)
	assert.Equal(t, "File content\n", string(content))
}

func TestParseStreaming_EmptyContent(t *testing.T) {
	// Empty block (open tag followed immediately by close tag) — empty block gets no file
	input := "<gtms-file name=\"empty.md\">\n</gtms-file>\n<gtms-file name=\"notempty.md\">\nContent\n</gtms-file>"
	outputDir := t.TempDir()

	res, err := parseStreamingOutput(strings.NewReader(input), outputDir)
	require.NoError(t, err)

	// Empty block is skipped (no content to write), only notempty.md is written
	assert.Len(t, res.SavedFiles, 1)
	assert.Contains(t, res.SavedFiles[0], "notempty.md")

	content, err := os.ReadFile(filepath.Join(outputDir, "notempty.md"))
	require.NoError(t, err)
	assert.Equal(t, "Content\n", string(content))
}

func TestParseStreaming_LargeContent(t *testing.T) {
	// Create a line larger than the default 64KB scanner limit
	largeLine := strings.Repeat("x", 100*1024) // 100KB
	input := "<gtms-file name=\"large.txt\">\n" + largeLine + "\n</gtms-file>"
	outputDir := t.TempDir()

	res, err := parseStreamingOutput(strings.NewReader(input), outputDir)
	require.NoError(t, err)

	assert.Len(t, res.SavedFiles, 1)

	content, err := os.ReadFile(filepath.Join(outputDir, "large.txt"))
	require.NoError(t, err)
	assert.Len(t, string(content), 100*1024+1) // content + trailing newline
}

func TestParseStreaming_MaliciousFilename(t *testing.T) {
	input := "<gtms-file name=\"../../etc/passwd\">\nmalicious content\n</gtms-file>\n<gtms-file name=\"safe.md\">\nOK\n</gtms-file>"
	outputDir := t.TempDir()

	res, err := parseStreamingOutput(strings.NewReader(input), outputDir)
	require.NoError(t, err)

	// Malicious file skipped, safe file written
	assert.Len(t, res.SavedFiles, 1)
	assert.Contains(t, res.SavedFiles[0], "safe.md")

	// Content after malicious delimiter should NOT leak to summary
	assert.NotContains(t, res.Summary, "malicious content")
	assert.Empty(t, res.Summary) // no preamble in this input

	// Ensure no file was written outside outputDir
	assert.NoFileExists(t, filepath.Join(outputDir, "..", "..", "etc", "passwd"))
}

func TestParseStreaming_WindowsLineEndings(t *testing.T) {
	// bufio.ScanLines strips \r, so \r\n should be handled
	input := "<gtms-file name=\"win.txt\">\r\nLine one\r\nLine two\r\n</gtms-file>"
	outputDir := t.TempDir()

	res, err := parseStreamingOutput(strings.NewReader(input), outputDir)
	require.NoError(t, err)

	assert.Len(t, res.SavedFiles, 1)

	content, err := os.ReadFile(filepath.Join(outputDir, "win.txt"))
	require.NoError(t, err)
	assert.Equal(t, "Line one\nLine two\n", string(content))
}

func TestParseStreaming_NoTrailingNewline(t *testing.T) {
	// Last block with closing tag but no trailing newline in content
	input := "<gtms-file name=\"noeol.txt\">\nContent without newline at end\n</gtms-file>"
	outputDir := t.TempDir()

	res, err := parseStreamingOutput(strings.NewReader(input), outputDir)
	require.NoError(t, err)

	assert.Len(t, res.SavedFiles, 1)

	content, err := os.ReadFile(filepath.Join(outputDir, "noeol.txt"))
	require.NoError(t, err)
	assert.Equal(t, "Content without newline at end\n", string(content))
}

func TestParseStreaming_EmptyOutputDir(t *testing.T) {
	// When outputDir is empty, all output is treated as summary
	input := "<gtms-file name=\"test.md\">\nContent\n</gtms-file>"

	res, err := parseStreamingOutput(strings.NewReader(input), "")
	require.NoError(t, err)

	assert.Equal(t, "<gtms-file name=\"test.md\">\nContent\n</gtms-file>", res.Summary)
	assert.Nil(t, res.SavedFiles)
}

func TestParseStreaming_FilenameWithSlash(t *testing.T) {
	input := "<gtms-file name=\"sub/file.md\">\nContent\n</gtms-file>\n<gtms-file name=\"safe.md\">\nOK\n</gtms-file>"
	outputDir := t.TempDir()

	res, err := parseStreamingOutput(strings.NewReader(input), outputDir)
	require.NoError(t, err)

	// Relative subdirectory paths are allowed — both files written
	assert.Len(t, res.SavedFiles, 2)

	content, err := os.ReadFile(filepath.Join(outputDir, "sub", "file.md"))
	require.NoError(t, err)
	assert.Equal(t, "Content\n", string(content))

	content2, err := os.ReadFile(filepath.Join(outputDir, "safe.md"))
	require.NoError(t, err)
	assert.Equal(t, "OK\n", string(content2))
}

func TestParseStreaming_SubdirectoryCreated(t *testing.T) {
	input := "<gtms-file name=\"deep/nested/dir/file.bats\">\n#!/usr/bin/env bats\n@test \"works\" { true; }\n</gtms-file>"
	outputDir := t.TempDir()

	res, err := parseStreamingOutput(strings.NewReader(input), outputDir)
	require.NoError(t, err)

	assert.Len(t, res.SavedFiles, 1)

	path := filepath.Join(outputDir, "deep", "nested", "dir", "file.bats")
	assert.FileExists(t, path)

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(content), "@test")
}

func TestParseStreaming_FilenameWithBackslash(t *testing.T) {
	input := "<gtms-file name=\"sub\\file.md\">\nContent\n</gtms-file>\n<gtms-file name=\"safe.md\">\nOK\n</gtms-file>"
	outputDir := t.TempDir()

	res, err := parseStreamingOutput(strings.NewReader(input), outputDir)
	require.NoError(t, err)

	assert.Len(t, res.SavedFiles, 1)
	assert.Contains(t, res.SavedFiles[0], "safe.md")
}

func TestParseStreaming_EmptyInput(t *testing.T) {
	res, err := parseStreamingOutput(strings.NewReader(""), t.TempDir())
	require.NoError(t, err)

	assert.Empty(t, res.Summary)
	assert.Nil(t, res.SavedFiles)
}

// --- XML format unit tests ---

func TestParseStreaming_XMLSingleFile(t *testing.T) {
	input := "<gtms-file name=\"test.md\">\nContent\n</gtms-file>"
	outputDir := t.TempDir()

	res, err := parseStreamingOutput(strings.NewReader(input), outputDir)
	require.NoError(t, err)

	assert.Empty(t, res.Summary)
	assert.Len(t, res.SavedFiles, 1)

	content, err := os.ReadFile(filepath.Join(outputDir, "test.md"))
	require.NoError(t, err)
	assert.Equal(t, "Content\n", string(content))
}

func TestParseStreaming_XMLMultipleFiles(t *testing.T) {
	input := "<gtms-file name=\"f1.md\">\nA\n</gtms-file>\n<gtms-file name=\"f2.md\">\nB\n</gtms-file>"
	outputDir := t.TempDir()

	res, err := parseStreamingOutput(strings.NewReader(input), outputDir)
	require.NoError(t, err)

	assert.Empty(t, res.Summary)
	assert.Len(t, res.SavedFiles, 2)

	content1, err := os.ReadFile(filepath.Join(outputDir, "f1.md"))
	require.NoError(t, err)
	assert.Equal(t, "A\n", string(content1))

	content2, err := os.ReadFile(filepath.Join(outputDir, "f2.md"))
	require.NoError(t, err)
	assert.Equal(t, "B\n", string(content2))
}

func TestParseStreaming_XMLSummaryAfterClose(t *testing.T) {
	input := "<gtms-file name=\"test.md\">\nContent\n</gtms-file>\nDesign notes:\nThis is a summary"
	outputDir := t.TempDir()

	res, err := parseStreamingOutput(strings.NewReader(input), outputDir)
	require.NoError(t, err)

	assert.Len(t, res.SavedFiles, 1)
	assert.Equal(t, "Design notes:\nThis is a summary", res.Summary)

	content, err := os.ReadFile(filepath.Join(outputDir, "test.md"))
	require.NoError(t, err)
	assert.Equal(t, "Content\n", string(content))
}

func TestParseStreaming_XMLSummaryBeforeAndAfter(t *testing.T) {
	input := "Preamble\n<gtms-file name=\"test.md\">\nContent\n</gtms-file>\nEpilogue"
	outputDir := t.TempDir()

	res, err := parseStreamingOutput(strings.NewReader(input), outputDir)
	require.NoError(t, err)

	assert.Equal(t, "Preamble\nEpilogue", res.Summary)
	assert.Len(t, res.SavedFiles, 1)

	content, err := os.ReadFile(filepath.Join(outputDir, "test.md"))
	require.NoError(t, err)
	assert.Equal(t, "Content\n", string(content))
}

func TestParseStreaming_XMLMaliciousFilename(t *testing.T) {
	input := "<gtms-file name=\"../../etc/passwd\">\nmalicious\n</gtms-file>\n<gtms-file name=\"safe.md\">\nOK\n</gtms-file>"
	outputDir := t.TempDir()

	res, err := parseStreamingOutput(strings.NewReader(input), outputDir)
	require.NoError(t, err)

	// Malicious file skipped, safe file written
	assert.Len(t, res.SavedFiles, 1)
	assert.Contains(t, res.SavedFiles[0], "safe.md")

	// Content after malicious delimiter should NOT leak to summary
	assert.NotContains(t, res.Summary, "malicious")
	assert.Empty(t, res.Summary) // no preamble in this input

	// Ensure no file was written outside outputDir
	assert.NoFileExists(t, filepath.Join(outputDir, "..", "..", "etc", "passwd"))
}

func TestParseStreaming_XMLEmptyBlock(t *testing.T) {
	input := "<gtms-file name=\"empty.md\">\n</gtms-file>\n<gtms-file name=\"real.md\">\nContent\n</gtms-file>"
	outputDir := t.TempDir()

	res, err := parseStreamingOutput(strings.NewReader(input), outputDir)
	require.NoError(t, err)

	// Empty block is skipped (no content to write), only real.md is written
	assert.Len(t, res.SavedFiles, 1)
	assert.Contains(t, res.SavedFiles[0], "real.md")

	content, err := os.ReadFile(filepath.Join(outputDir, "real.md"))
	require.NoError(t, err)
	assert.Equal(t, "Content\n", string(content))
}

func TestParseStreaming_XMLTagCollision(t *testing.T) {
	input := "<gtms-file name=\"test.html\">\n<div>Some HTML</div>\n</file>\n<file name=\"other\">\n</gtms-file>"
	outputDir := t.TempDir()

	res, err := parseStreamingOutput(strings.NewReader(input), outputDir)
	require.NoError(t, err)

	// 1 file saved (test.html), content includes the inner HTML and </file> tags
	assert.Len(t, res.SavedFiles, 1)
	assert.Contains(t, res.SavedFiles[0], "test.html")

	content, err := os.ReadFile(filepath.Join(outputDir, "test.html"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "<div>Some HTML</div>")
	assert.Contains(t, string(content), "</file>")
	assert.Contains(t, string(content), "<file name=\"other\">")
}

func TestParseStreaming_XMLEmptyOutputDir(t *testing.T) {
	input := "<gtms-file name=\"test.md\">\nContent\n</gtms-file>"

	res, err := parseStreamingOutput(strings.NewReader(input), "")
	require.NoError(t, err)

	// All output becomes summary when outputDir is empty
	assert.Equal(t, "<gtms-file name=\"test.md\">\nContent\n</gtms-file>", res.Summary)
	assert.Nil(t, res.SavedFiles)
}

func TestParseStreaming_XMLWindowsLineEndings(t *testing.T) {
	input := "<gtms-file name=\"win.txt\">\r\nLine one\r\n</gtms-file>"
	outputDir := t.TempDir()

	res, err := parseStreamingOutput(strings.NewReader(input), outputDir)
	require.NoError(t, err)

	assert.Len(t, res.SavedFiles, 1)

	content, err := os.ReadFile(filepath.Join(outputDir, "win.txt"))
	require.NoError(t, err)
	assert.Equal(t, "Line one\n", string(content))
}

// --- ENH-042: Duplicate filename guard tests ---

func TestWriteFileBlock_SkipsDuplicate(t *testing.T) {
	outputDir := t.TempDir()
	filename := "existing-file.md"

	// Pre-create the file with known content
	originalContent := "original content\n"
	err := os.WriteFile(filepath.Join(outputDir, filename), []byte(originalContent), 0644)
	require.NoError(t, err)

	// Attempt to write same filename with different content
	path, err := writeFileBlock(outputDir, filename, "new content")
	assert.NoError(t, err)
	assert.Empty(t, path, "should return empty path for duplicate")

	// Original content should be unchanged
	data, err := os.ReadFile(filepath.Join(outputDir, filename))
	require.NoError(t, err)
	assert.Equal(t, originalContent, string(data), "original file should not be overwritten")
}

func TestParseStreaming_DuplicateFileSkipped(t *testing.T) {
	outputDir := t.TempDir()

	// Pre-create a file that will collide
	err := os.WriteFile(filepath.Join(outputDir, "existing.md"), []byte("original\n"), 0644)
	require.NoError(t, err)

	// Stream input tries to write to the same filename
	input := "<gtms-file name=\"existing.md\">\nNew content\n</gtms-file>\n<gtms-file name=\"fresh.md\">\nFresh content\n</gtms-file>"

	res, err := parseStreamingOutput(strings.NewReader(input), outputDir)
	require.NoError(t, err)

	// Only the fresh file should be in savedFiles
	assert.Len(t, res.SavedFiles, 1)
	assert.Contains(t, res.SavedFiles[0], "fresh.md")

	// Original file should be unchanged
	data, err := os.ReadFile(filepath.Join(outputDir, "existing.md"))
	require.NoError(t, err)
	assert.Equal(t, "original\n", string(data))

	// Fresh file should be written
	freshData, err := os.ReadFile(filepath.Join(outputDir, "fresh.md"))
	require.NoError(t, err)
	assert.Equal(t, "Fresh content\n", string(freshData))
}

// --- Integration tests: actual shell commands with delimited output ---

func TestStreamingTier1_WithDelimiters(t *testing.T) {
	skipIfShort(t)
	outputDir := t.TempDir()
	ac := &AdapterContext{
		TaskID:      "task-stream-t1",
		Command:     "create",
		Reference: "REQ-001",
		ProjectRoot: t.TempDir(),
		WorkDir:     t.TempDir(),
		OutputDir:   outputDir,
	}

	// printf outputs XML-tagged file blocks
	result, err := InvokeTier1(context.Background(), ac, `printf '<gtms-file name="test1.md">\nHello\n</gtms-file>\n<gtms-file name="test2.md">\nWorld\n</gtms-file>\n'`)
	require.NoError(t, err)

	assert.Equal(t, 0, result.ExitCode)
	assert.Len(t, result.SavedFiles, 2)
	assert.Empty(t, result.Stdout) // no pre-tag content

	content1, err := os.ReadFile(filepath.Join(outputDir, "test1.md"))
	require.NoError(t, err)
	assert.Equal(t, "Hello\n", string(content1))

	content2, err := os.ReadFile(filepath.Join(outputDir, "test2.md"))
	require.NoError(t, err)
	assert.Equal(t, "World\n", string(content2))
}

func TestStreamingTier2_WithDelimiters(t *testing.T) {
	skipIfShort(t)
	dir := t.TempDir()
	outputDir := t.TempDir()

	// Create a script that outputs XML-tagged file blocks
	scriptPath := filepath.Join(dir, "stream-adapter.sh")
	script := `#!/bin/bash
printf '<gtms-file name="test1.md">\nHello\n</gtms-file>\n<gtms-file name="test2.md">\nWorld\n</gtms-file>\n'
`
	err := os.WriteFile(scriptPath, []byte(script), 0755)
	require.NoError(t, err)

	ac := &AdapterContext{
		TaskID:      "task-stream-t2",
		Command:     "create",
		Reference: "REQ-002",
		ProjectRoot: dir,
		WorkDir:     dir,
		OutputDir:   outputDir,
		ResultFile:  filepath.Join(dir, "result.yaml"),
	}

	result, err := InvokeTier2(context.Background(), ac, scriptPath)
	require.NoError(t, err)

	assert.Equal(t, 0, result.ExitCode)
	assert.Len(t, result.SavedFiles, 2)
	assert.Empty(t, result.Stdout)

	content1, err := os.ReadFile(filepath.Join(outputDir, "test1.md"))
	require.NoError(t, err)
	assert.Equal(t, "Hello\n", string(content1))

	content2, err := os.ReadFile(filepath.Join(outputDir, "test2.md"))
	require.NoError(t, err)
	assert.Equal(t, "World\n", string(content2))
}

func TestStreamingTier1_BackwardCompat(t *testing.T) {
	skipIfShort(t)
	ac := &AdapterContext{
		TaskID:      "task-stream-compat",
		Command:     "create",
		ProjectRoot: t.TempDir(),
		WorkDir:     t.TempDir(),
		OutputDir:   t.TempDir(),
	}

	result, err := InvokeTier1(context.Background(), ac, `echo "plain output"`)
	require.NoError(t, err)

	assert.Equal(t, 0, result.ExitCode)
	assert.Nil(t, result.SavedFiles)
	assert.Equal(t, "plain output", result.Stdout)
}

func TestStreamingTier1_SummaryAndFiles(t *testing.T) {
	skipIfShort(t)
	outputDir := t.TempDir()
	ac := &AdapterContext{
		TaskID:      "task-stream-mixed",
		Command:     "create",
		ProjectRoot: t.TempDir(),
		WorkDir:     t.TempDir(),
		OutputDir:   outputDir,
	}

	// Output has preamble text followed by a file block
	result, err := InvokeTier1(context.Background(), ac, `printf 'Processing REQ-123...\n<gtms-file name="tc-001.md">\nTest case content\n</gtms-file>\n'`)
	require.NoError(t, err)

	assert.Equal(t, 0, result.ExitCode)
	assert.Len(t, result.SavedFiles, 1)
	assert.Equal(t, "Processing REQ-123...", result.Stdout)

	content, err := os.ReadFile(filepath.Join(outputDir, "tc-001.md"))
	require.NoError(t, err)
	assert.Equal(t, "Test case content\n", string(content))
}

func TestStreamingInvoke_ArtefactUsesRelativePaths(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
	}

	// Adapter outputs XML-tagged file blocks to stdout
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "stream-adapter",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `printf '<gtms-file name="tc-100.md">\nTest content\n</gtms-file>\n<gtms-file name="tc-101.md">\nMore content\n</gtms-file>\n'`},
		Tier:    1,
		Mode:    "sync",
	}

	flags := CommandFlags{}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "JIRA-REL", flags)
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)

	// Verify result contract artefact has relative paths (not absolute)
	rcPath := result.ResultPath(root, res.TaskID)
	rc, err := result.Read(rcPath)
	require.NoError(t, err)

	assert.NotEmpty(t, rc.Artefact, "artefact should be populated from SavedFiles")
	assert.NotContains(t, rc.Artefact, root, "artefact should not contain absolute project root")
	assert.Contains(t, rc.Artefact, "test-cases/tc-100.md", "artefact should contain relative path")
	assert.Contains(t, rc.Artefact, "test-cases/tc-101.md", "artefact should contain both file paths")

	// Verify files were actually written
	assert.FileExists(t, filepath.Join(root, "test-cases", "tc-100.md"))
	assert.FileExists(t, filepath.Join(root, "test-cases", "tc-101.md"))
}

// --- BUG-021: OutputSubdir appended to streaming output directory ---

func TestStreamingTier1_OutputSubdirAppended(t *testing.T) {
	skipIfShort(t)
	outputDir := t.TempDir()
	ac := &AdapterContext{
		TaskID:       "task-bug021-t1-subdir",
		Command:      "automate",
		TestCase:     "tc-test1",
		ProjectRoot:  t.TempDir(),
		WorkDir:      t.TempDir(),
		OutputDir:    outputDir,
		OutputSubdir: "widgets/",
	}

	result, err := InvokeTier1(context.Background(), ac, `printf '<gtms-file name="tc-test1.bats">\n#!/usr/bin/env bats\n@test "hello" { true; }\n</gtms-file>\n'`)
	require.NoError(t, err)

	assert.Equal(t, 0, result.ExitCode)
	assert.Len(t, result.SavedFiles, 1)

	// File must land in outputDir/widgets/, not outputDir/
	expectedPath := filepath.Join(outputDir, "widgets", "tc-test1.bats")
	assert.FileExists(t, expectedPath, "file should be in subdirectory")
	assert.NoFileExists(t, filepath.Join(outputDir, "tc-test1.bats"), "file should NOT be in output root")
}

func TestStreamingTier1_EmptyOutputSubdirUnchanged(t *testing.T) {
	skipIfShort(t)
	outputDir := t.TempDir()
	ac := &AdapterContext{
		TaskID:       "task-bug021-t1-empty",
		Command:      "automate",
		TestCase:     "tc-test2",
		ProjectRoot:  t.TempDir(),
		WorkDir:      t.TempDir(),
		OutputDir:    outputDir,
		OutputSubdir: "",
	}

	result, err := InvokeTier1(context.Background(), ac, `printf '<gtms-file name="tc-test2.bats">\ncontent\n</gtms-file>\n'`)
	require.NoError(t, err)

	assert.Equal(t, 0, result.ExitCode)
	assert.Len(t, result.SavedFiles, 1)

	// File must land directly in outputDir (no extra subdirectory)
	assert.FileExists(t, filepath.Join(outputDir, "tc-test2.bats"))
}

func TestStreamingTier2_OutputSubdirAppended(t *testing.T) {
	skipIfShort(t)
	dir := t.TempDir()
	outputDir := t.TempDir()

	scriptPath := filepath.Join(dir, "subdir-stream.sh")
	script := `#!/bin/bash
printf '<gtms-file name="tc-test3.bats">\n#!/usr/bin/env bats\n@test "hello" { true; }\n</gtms-file>\n'
`
	err := os.WriteFile(scriptPath, []byte(script), 0755)
	require.NoError(t, err)

	ac := &AdapterContext{
		TaskID:       "task-bug021-t2-subdir",
		Command:      "automate",
		TestCase:     "tc-test3",
		ProjectRoot:  dir,
		WorkDir:      dir,
		OutputDir:    outputDir,
		OutputSubdir: "deep/nested/",
		ResultFile:   filepath.Join(dir, "result.yaml"),
	}

	result, err := InvokeTier2(context.Background(), ac, scriptPath)
	require.NoError(t, err)

	assert.Equal(t, 0, result.ExitCode)
	assert.Len(t, result.SavedFiles, 1)

	// File must land in outputDir/deep/nested/, not outputDir/
	expectedPath := filepath.Join(outputDir, "deep", "nested", "tc-test3.bats")
	assert.FileExists(t, expectedPath, "file should be in nested subdirectory")
	assert.NoFileExists(t, filepath.Join(outputDir, "tc-test3.bats"), "file should NOT be in output root")
}

func TestStreamingTier2_EmptyOutputSubdirUnchanged(t *testing.T) {
	skipIfShort(t)
	dir := t.TempDir()
	outputDir := t.TempDir()

	scriptPath := filepath.Join(dir, "nosubdir-stream.sh")
	script := `#!/bin/bash
printf '<gtms-file name="tc-test4.bats">\ncontent\n</gtms-file>\n'
`
	err := os.WriteFile(scriptPath, []byte(script), 0755)
	require.NoError(t, err)

	ac := &AdapterContext{
		TaskID:       "task-bug021-t2-empty",
		Command:      "automate",
		TestCase:     "tc-test4",
		ProjectRoot:  dir,
		WorkDir:      dir,
		OutputDir:    outputDir,
		OutputSubdir: "",
		ResultFile:   filepath.Join(dir, "result.yaml"),
	}

	result, err := InvokeTier2(context.Background(), ac, scriptPath)
	require.NoError(t, err)

	assert.Equal(t, 0, result.ExitCode)
	assert.Len(t, result.SavedFiles, 1)

	// File must land directly in outputDir (no extra subdirectory)
	assert.FileExists(t, filepath.Join(outputDir, "tc-test4.bats"))
}
