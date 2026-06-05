package result

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- TruncateUTF8 ---

func TestTruncateUTF8_ShortInputUnchanged(t *testing.T) {
	in := "Pester v5.7.1\nAll 5 tests passed\n"
	got, truncated := TruncateUTF8(in, 1024)
	assert.Equal(t, in, got)
	assert.False(t, truncated)
}

func TestTruncateUTF8_ExactBoundary(t *testing.T) {
	in := strings.Repeat("a", 64)
	got, truncated := TruncateUTF8(in, 64)
	assert.Equal(t, in, got)
	assert.False(t, truncated, "input exactly at boundary should not truncate")
}

func TestTruncateUTF8_BacksUpOnMultibyteRune(t *testing.T) {
	// "é" is 2 bytes (UTF-8: 0xC3 0xA9).
	prefix := strings.Repeat("a", 63)
	in := prefix + "é"
	require.Equal(t, 65, len(in))

	got, truncated := TruncateUTF8(in, 64)
	assert.True(t, truncated)
	assert.Equal(t, prefix, got, "must back up to the rune start before the multibyte sequence")
	assert.True(t, utf8.ValidString(got))
}

func TestTruncateUTF8_OversizeASCIIHitsCapExactly(t *testing.T) {
	in := strings.Repeat("x", 100)
	got, truncated := TruncateUTF8(in, 64)
	assert.True(t, truncated)
	assert.Equal(t, 64, len(got))
}

func TestTruncateUTF8_EmptyInput(t *testing.T) {
	got, truncated := TruncateUTF8("", 64)
	assert.Equal(t, "", got)
	assert.False(t, truncated)
}

// --- WriteLogSpill ---

func TestWriteLogSpill_EmptyIsNoOp(t *testing.T) {
	root := t.TempDir()
	path, err := WriteLogSpill(root, "task-empty01", "")
	require.NoError(t, err)
	assert.Empty(t, path)
	_, statErr := os.Stat(filepath.Join(root, ".gtms", "logs"))
	assert.True(t, os.IsNotExist(statErr), "no .gtms/logs/ directory should be created for empty spill")
}

func TestWriteLogSpill_WritesFileAndReturnsRelativePath(t *testing.T) {
	root := t.TempDir()
	full := "line 1\nline 2\nline 3\n"
	path, err := WriteLogSpill(root, "task-spill01", full)
	require.NoError(t, err)

	assert.Equal(t, ".gtms/logs/task-spill01.log", path)

	abs := filepath.Join(root, ".gtms", "logs", "task-spill01.log")
	data, readErr := os.ReadFile(abs)
	require.NoError(t, readErr)
	assert.Equal(t, full, string(data))
}

func TestWriteLogSpill_CreatesDirectoryOnDemand(t *testing.T) {
	root := t.TempDir()
	_, statErr := os.Stat(filepath.Join(root, ".gtms", "logs"))
	require.True(t, os.IsNotExist(statErr))

	_, err := WriteLogSpill(root, "task-dircreate", "content")
	require.NoError(t, err)

	info, statErr := os.Stat(filepath.Join(root, ".gtms", "logs"))
	require.NoError(t, statErr)
	assert.True(t, info.IsDir())
}

func TestWriteLogSpill_RejectsPathTraversal(t *testing.T) {
	root := t.TempDir()

	tests := []struct {
		name   string
		taskID string
	}{
		{"slash in taskID", "x/y"},
		{"backslash in taskID", "x\\y"},
		{"dotdot in taskID", "../escape"},
		{"empty taskID", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := WriteLogSpill(root, tt.taskID, "some log content")
			require.Error(t, err, "expected rejection for %s", tt.name)

			logsDir := filepath.Join(root, ".gtms", "logs")
			entries, _ := os.ReadDir(logsDir)
			assert.Empty(t, entries, "no spill file should be created for unsafe taskID")
		})
	}
}

// --- ApplyLogCap ---

func TestApplyLogCap_UnderCapNoSpill(t *testing.T) {
	root := t.TempDir()
	short := "All tests passed\n"

	truncated, spill, err := ApplyLogCap(root, "task-undercap", short)
	require.NoError(t, err)
	assert.Equal(t, short, truncated, "under-cap input must be returned verbatim")
	assert.Empty(t, spill, "no spill expected for under-cap input")

	// .gtms/logs/ must not be created for under-cap input.
	_, statErr := os.Stat(filepath.Join(root, ".gtms", "logs"))
	assert.True(t, os.IsNotExist(statErr))
}

func TestApplyLogCap_OverCapTruncatesAndSpills(t *testing.T) {
	root := t.TempDir()
	// 128 KB — twice the cap.
	bigLog := strings.Repeat("verbose framework output line\n", 4500)
	require.Greater(t, len(bigLog), NotesSizeCapBytes)

	truncated, spill, err := ApplyLogCap(root, "task-bigcap01", bigLog)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(truncated), NotesSizeCapBytes, "truncated log must not exceed cap")
	assert.Equal(t, ".gtms/logs/task-bigcap01.log", spill)

	// Spill file must carry the full content.
	abs := filepath.Join(root, ".gtms", "logs", "task-bigcap01.log")
	data, readErr := os.ReadFile(abs)
	require.NoError(t, readErr)
	assert.Equal(t, bigLog, string(data), "spill file must carry the full untruncated log")
}

// --- CapSummary ---

func TestCapSummary_UnderCapUnchanged(t *testing.T) {
	in := "Process exited with code 1: short stderr"
	got := CapSummary(in)
	assert.Equal(t, in, got)
}

func TestCapSummary_ExactBoundary(t *testing.T) {
	in := strings.Repeat("x", SummarySizeCapBytes)
	got := CapSummary(in)
	assert.Equal(t, in, got, "input exactly at boundary should not truncate")
}

func TestCapSummary_OverCapTruncatesAndAppendsMarker(t *testing.T) {
	in := strings.Repeat("y", SummarySizeCapBytes+4096)
	got := CapSummary(in)
	assert.True(t, len(got) <= SummarySizeCapBytes+len(summaryTruncationMarker),
		"truncated summary must fit within cap + marker")
	assert.Contains(t, got, summaryTruncationMarker,
		"truncation marker must be appended when content exceeds cap")
}

func TestApplyLogCap_SpillFailureReturnsTruncated(t *testing.T) {
	root := t.TempDir()
	bigLog := strings.Repeat("x", NotesSizeCapBytes+1024)
	require.Greater(t, len(bigLog), NotesSizeCapBytes)

	// Invalid task ID triggers WriteLogSpill rejection — truncated must still come back.
	truncated, spill, err := ApplyLogCap(root, "../escape", bigLog)
	require.Error(t, err)
	assert.LessOrEqual(t, len(truncated), NotesSizeCapBytes, "truncated text must land even on spill failure")
	assert.Empty(t, spill, "spill path must be empty on spill failure")
}
