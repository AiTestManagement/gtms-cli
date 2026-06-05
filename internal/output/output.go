// Package output provides CLI formatting utilities including status icons,
// TTY detection, table formatting, and error output helpers.
package output

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Status icon constants used throughout the CLI.
const (
	IconComplete   = "\u2713" // check mark
	IconInProgress = "\u25CF" // filled circle
	IconPending    = "\u25CB" // empty circle
	IconError      = "\u2717" // X mark
	IconWarning    = "\u26A0" // warning triangle
	IconSkipped    = "\u2298" // circled division slash (⊘) — runtime-skipped test
)

// StatusIcon returns the appropriate icon for a given status string.
func StatusIcon(status string) string {
	switch status {
	case "complete":
		return IconComplete
	case "in-progress":
		return IconInProgress
	case "pending":
		return IconPending
	case "error":
		return IconError
	case "warning":
		return IconWarning
	default:
		return IconPending
	}
}

// IsTTY returns true if the given writer is a terminal (not piped).
func IsTTY(w io.Writer) bool {
	if f, ok := w.(*os.File); ok {
		stat, err := f.Stat()
		if err != nil {
			return false
		}
		return (stat.Mode() & os.ModeCharDevice) != 0
	}
	return false
}

// Errorf writes a formatted error message to stderr using the GTMS
// error format: "X {message}\n    {hint}".
func Errorf(message string, hint string) {
	FprintError(os.Stderr, message, hint)
}

// displayedError wraps an error that has already been shown to the user
// via Errorf. This prevents main.go from printing it again.
type displayedError struct{ err error }

func (d *displayedError) Error() string { return d.err.Error() }
func (d *displayedError) Unwrap() error { return d.err }

// AsDisplayed wraps err to indicate it has already been printed.
func AsDisplayed(err error) error { return &displayedError{err: err} }

// IsDisplayed returns true if err was already shown to the user.
func IsDisplayed(err error) bool {
	var d *displayedError
	return errors.As(err, &d)
}

// Warnf writes a formatted warning message to stderr using the GTMS
// warning format: "  ⚠ {message}".
func Warnf(message string) {
	FprintWarning(os.Stderr, message)
}

// FprintWarning writes a formatted warning message to the given writer.
func FprintWarning(w io.Writer, message string) {
	fmt.Fprintf(w, "  %s %s\n", IconWarning, message)
}

// FprintError writes a formatted error message to the given writer.
func FprintError(w io.Writer, message string, hint string) {
	fmt.Fprintf(w, "%s %s\n", IconError, message)
	if hint != "" {
		fmt.Fprintf(w, "    %s\n", hint)
	}
}

// Dim writes text wrapped in ANSI dim/faint escape codes (\033[2m...\033[0m)
// when the writer is a terminal. When piped or redirected, writes plain text.
func Dim(w io.Writer, text string) {
	if IsTTY(w) {
		fmt.Fprintf(w, "\033[2m%s\033[0m", text)
	} else {
		fmt.Fprint(w, text)
	}
}

// Dimf writes a formatted string wrapped in ANSI dim/faint escape codes
// when the writer is a terminal. When piped or redirected, writes plain text.
func Dimf(w io.Writer, format string, args ...interface{}) {
	text := fmt.Sprintf(format, args...)
	Dim(w, text)
}

// Dimln writes text followed by a newline, wrapped in ANSI dim/faint escape
// codes when the writer is a terminal. When piped or redirected, writes plain text.
func Dimln(w io.Writer, text string) {
	if IsTTY(w) {
		fmt.Fprintf(w, "\033[2m%s\033[0m\n", text)
	} else {
		fmt.Fprintln(w, text)
	}
}

// Table formats tabular data with fixed-width columns and padding.
type Table struct {
	Headers []string
	Rows    [][]string
	Padding int // spaces between columns; default 2
}

// NewTable creates a new table with the given headers.
func NewTable(headers ...string) *Table {
	return &Table{
		Headers: headers,
		Padding: 2,
	}
}

// AddRow appends a row of values to the table.
func (t *Table) AddRow(values ...string) {
	t.Rows = append(t.Rows, values)
}

// Render writes the formatted table to the given writer.
func (t *Table) Render(w io.Writer) {
	if len(t.Headers) == 0 {
		return
	}

	padding := t.Padding
	if padding <= 0 {
		padding = 2
	}

	// Compute column widths
	widths := make([]int, len(t.Headers))
	for i, h := range t.Headers {
		widths[i] = len(h)
	}
	for _, row := range t.Rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	// Print header
	t.printRow(w, t.Headers, widths, padding)

	// Print separator
	parts := make([]string, len(widths))
	for i, w := range widths {
		parts[i] = strings.Repeat("-", w)
	}
	t.printRow(w, parts, widths, padding)

	// Print rows
	for _, row := range t.Rows {
		t.printRow(w, row, widths, padding)
	}
}

func (t *Table) printRow(w io.Writer, cells []string, widths []int, padding int) {
	for i, cell := range cells {
		if i >= len(widths) {
			break
		}
		if i > 0 {
			fmt.Fprint(w, strings.Repeat(" ", padding))
		}
		fmt.Fprintf(w, "%-*s", widths[i], cell)
	}
	fmt.Fprintln(w)
}

// String returns the rendered table as a string.
func (t *Table) String() string {
	var sb strings.Builder
	t.Render(&sb)
	return sb.String()
}
