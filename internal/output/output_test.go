package output

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStatusIcon(t *testing.T) {
	tests := []struct {
		status string
		want   string
	}{
		{"complete", IconComplete},
		{"in-progress", IconInProgress},
		{"pending", IconPending},
		{"failed", IconError},
		{"error", IconError},
		{"warning", IconWarning},
		{"unknown", IconPending}, // default
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			got := StatusIcon(tt.status)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsTTY_NonFile(t *testing.T) {
	// A bytes.Buffer is not a *os.File, so IsTTY should return false.
	var buf bytes.Buffer
	assert.False(t, IsTTY(&buf))
}

func TestFprintError_WithHint(t *testing.T) {
	var buf bytes.Buffer
	FprintError(&buf, "Something went wrong", "Try again later")
	output := buf.String()
	assert.Contains(t, output, IconError)
	assert.Contains(t, output, "Something went wrong")
	assert.Contains(t, output, "    Try again later")
}

func TestFprintError_WithoutHint(t *testing.T) {
	var buf bytes.Buffer
	FprintError(&buf, "Something went wrong", "")
	output := buf.String()
	assert.Contains(t, output, IconError)
	assert.Contains(t, output, "Something went wrong")
	// Should not have an indented hint line
	lines := strings.Split(strings.TrimSpace(output), "\n")
	assert.Len(t, lines, 1)
}

func TestTable_BasicRender(t *testing.T) {
	tbl := NewTable("ID", "STATUS", "TARGET")
	tbl.AddRow("task-a1b2c3d", "pending", "REQ-001")
	tbl.AddRow("task-e4f5a6b", "complete", "REQ-002")

	output := tbl.String()
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")

	// Should have header + separator + 2 data rows
	assert.Len(t, lines, 4)

	// Header line should contain column names
	assert.Contains(t, lines[0], "ID")
	assert.Contains(t, lines[0], "STATUS")
	assert.Contains(t, lines[0], "TARGET")

	// Separator line should contain dashes
	assert.Contains(t, lines[1], "---")

	// Data rows should contain values
	assert.Contains(t, lines[2], "task-a1b2c3d")
	assert.Contains(t, lines[2], "pending")
	assert.Contains(t, lines[3], "task-e4f5a6b")
	assert.Contains(t, lines[3], "complete")
}

func TestTable_ColumnAlignment(t *testing.T) {
	tbl := NewTable("A", "B")
	tbl.AddRow("short", "x")
	tbl.AddRow("very long value", "y")

	output := tbl.String()
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")

	// All lines should have consistent alignment
	// The "B" column should start at the same position in each line
	assert.Len(t, lines, 4)

	// Find position of "B" header -- the second column starts after the first column width + padding
	// First column width = max("A", "short", "very long value") = 15
	// Padding = 2
	// So second column starts at position 17
	for _, line := range lines {
		// Each line should be at least 17 chars to reach the second column
		assert.GreaterOrEqual(t, len(line), 17, "Line too short: %q", line)
	}
}

func TestTable_EmptyHeaders(t *testing.T) {
	tbl := NewTable()
	var buf bytes.Buffer
	tbl.Render(&buf)
	assert.Empty(t, buf.String())
}

func TestIconConstants(t *testing.T) {
	// Verify icons are non-empty UTF-8 strings
	assert.NotEmpty(t, IconComplete)
	assert.NotEmpty(t, IconInProgress)
	assert.NotEmpty(t, IconPending)
	assert.NotEmpty(t, IconError)
	assert.NotEmpty(t, IconWarning)
}

func TestDisplayedError(t *testing.T) {
	inner := assert.AnError
	wrapped := AsDisplayed(inner)

	assert.True(t, IsDisplayed(wrapped), "wrapped error should be detected as displayed")
	assert.Equal(t, inner.Error(), wrapped.Error(), "message should pass through")
	assert.False(t, IsDisplayed(inner), "unwrapped error should not be displayed")
}

func TestDisplayedError_Nil(t *testing.T) {
	assert.False(t, IsDisplayed(nil), "nil error should not be displayed")
}

func TestWarnf(t *testing.T) {
	var buf bytes.Buffer
	FprintWarning(&buf, "test warning message")
	output := buf.String()
	assert.Contains(t, output, IconWarning)
	assert.Contains(t, output, "test warning message")
	// Should be a single line
	lines := strings.Split(strings.TrimSpace(output), "\n")
	assert.Len(t, lines, 1)
}
