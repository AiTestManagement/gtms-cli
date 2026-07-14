package adapter

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/aitestmanagement/gtms-cli/internal/reader"
)

// writeFile creates a file with the given content, creating parent dirs as needed.
func writeFile(t *testing.T, root, relPath, content string) {
	t.Helper()
	fullPath := filepath.Join(root, relPath)
	dir := filepath.Dir(fullPath)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(fullPath, []byte(content), 0o644))
}

func setupMinimalProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: test\n  repo: test\n")
	writeFile(t, root, filepath.Join("gtms/test/cases", "tc-001.md"), `---
test_case_id: tc-001
title: Test One
requirement: REQ-1
status: ready
---
`)
	return root
}

func TestInvokeBuiltin_StatusOverview(t *testing.T) {
	root := setupMinimalProject(t)

	result, err := InvokeBuiltin(context.Background(), "status", nil, root, []string{}, "")
	require.NoError(t, err)

	entries, ok := result.([]reader.PipelineEntry)
	require.True(t, ok, "expected []reader.PipelineEntry")
	require.Len(t, entries, 1)
	assert.Equal(t, "tc-001", entries[0].TestCaseID)
}

func TestInvokeBuiltin_StatusDetail(t *testing.T) {
	root := setupMinimalProject(t)

	result, err := InvokeBuiltin(context.Background(), "status", []string{"tc-001"}, root, []string{}, "")
	require.NoError(t, err)

	detail, ok := result.(*reader.PipelineDetailEntry)
	require.True(t, ok, "expected *reader.PipelineDetailEntry")
	assert.Equal(t, "tc-001", detail.TestCaseID)
	assert.Equal(t, "Test One", detail.Title)
}

func TestInvokeBuiltin_StatusDetailNotFound(t *testing.T) {
	root := setupMinimalProject(t)

	_, err := InvokeBuiltin(context.Background(), "status", []string{"tc-nonexistent"}, root, []string{}, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestInvokeBuiltin_Gaps(t *testing.T) {
	root := setupMinimalProject(t)

	result, err := InvokeBuiltin(context.Background(), "gaps", nil, root, []string{}, "")
	require.NoError(t, err)

	report, ok := result.(*reader.GapReport)
	require.True(t, ok, "expected *reader.GapReport")
	// tc-001 has no automation
	require.Len(t, report.NoAutomation, 1)
	assert.Equal(t, "tc-001", report.NoAutomation[0].ID)
}

func TestInvokeBuiltin_Triage(t *testing.T) {
	_, err := InvokeBuiltin(context.Background(), "triage", []string{"tc-001"}, "/fake/path", []string{}, "")
	assert.Error(t, err)
	// CON-023 / ENH-145: triage reads wiring; error wording reflects that.
	assert.Contains(t, err.Error(), "no wiring record found")
}

func TestInvokeBuiltin_UnknownCommand(t *testing.T) {
	_, err := InvokeBuiltin(context.Background(), "unknown", nil, "/fake/path", []string{}, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown built-in command")
}
