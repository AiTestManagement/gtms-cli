// spill.go provides UTF-8-safe log truncation and on-disk log spill helpers
// for the result-contract producer paths. Lifted from internal/pipeline/pipeline.go
// during BUG-084 (CON-023 wiring cutover) so the producer can live alongside
// the result.* writers in internal/adapter/invoker.go and
// internal/cli/status_common.go.
//
// The 64 KB cap and the .gtms/logs/{task-id}.log filename convention are the
// load-bearing user contract from ENH-077 (execute side) and ENH-092 (automate
// side). Do not change either without updating ENH-077/ENH-092 docs, the
// renderer header literal in internal/cli/status.go, and the BATS suites that
// pin the behaviour.
package result

import (
	"fmt"
	"os"
	"path/filepath"
	"unicode/utf8"

	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
)

// NotesSizeCapBytes is the maximum number of bytes retained in the result
// contract's `log:` field. Oversize content is truncated at a UTF-8 rune
// boundary and the full content is spilled to `.gtms/logs/{task-id}.log`
// (ENH-077 / ENH-092). User-visible contract — see internal/cli/status.go
// header literal "truncated to 64 KB" before changing.
const NotesSizeCapBytes = 64 * 1024

// SummarySizeCapBytes is the maximum number of bytes retained in the result
// contract's `summary:` field. BUG-075 introduced the cap on the (now-retired)
// pipeline.UpdateExecutionResult path so noisy Tier 1 stderr couldn't bloat
// the committed record. BUG-084 lifts it alongside NotesSizeCapBytes after the
// CON-023 wiring cutover dropped the producer. `summary:` is the short
// human-readable field surfaced in `gtms status` lists — the full stderr
// remains available via `log:` (and, when oversize, the .gtms/logs spill).
const SummarySizeCapBytes = 1024

// summaryTruncationMarker is appended when CapSummary truncates. Points the
// reader at the canonical full-payload location (`log:` field, possibly with
// a notes-spill pointer).
const summaryTruncationMarker = " … (truncated; see log:)"

// TruncateUTF8 returns s truncated to at most maxBytes bytes, stopping at a
// UTF-8 rune boundary so the result is always valid UTF-8. The second return
// value is true when truncation occurred.
//
// If s is already within the cap, it is returned unchanged with false.
// If maxBytes backs up before any valid rune start (pathological all-leading-
// continuation-bytes input), the empty string is returned with true.
func TruncateUTF8(s string, maxBytes int) (string, bool) {
	if len(s) <= maxBytes {
		return s, false
	}
	for i := maxBytes; i > 0; i-- {
		if utf8.RuneStart(s[i]) {
			return s[:i], true
		}
	}
	return "", true
}

// WriteLogSpill writes the full log content to `.gtms/logs/{taskID}.log` and
// returns the path relative to projectRoot (forward-slash normalised).
// Returns ("", nil) when fullLog is empty.
//
// The spill directory is created on demand; `.gtms/` is gitignored so spill
// files are transient and do not travel with the repo (ADR-011).
func WriteLogSpill(projectRoot, taskID, fullLog string) (string, error) {
	if fullLog == "" {
		return "", nil
	}
	// BUG-058: reject task IDs containing path separators or traversal sequences.
	if err := pathsafe.ValidateFilenameComponent(taskID, "task ID"); err != nil {
		return "", err
	}
	dir := filepath.Join(projectRoot, ".gtms", "logs")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("creating .gtms/logs: %w", err)
	}
	abs := filepath.Join(dir, taskID+".log")
	if err := os.WriteFile(abs, []byte(fullLog), 0644); err != nil {
		return "", fmt.Errorf("writing spill file: %w", err)
	}
	rel, err := filepath.Rel(projectRoot, abs)
	if err != nil {
		return filepath.ToSlash(abs), nil
	}
	return filepath.ToSlash(rel), nil
}

// CapSummary applies the summary size cap with a truncation marker suffix.
// Returns s unchanged when within the cap; otherwise returns the largest
// valid-UTF-8 prefix below the cap plus the marker. The full payload is
// expected to live on `log:` (with notes-spill when oversize).
func CapSummary(s string) string {
	if len(s) <= SummarySizeCapBytes {
		return s
	}
	truncated, _ := TruncateUTF8(s, SummarySizeCapBytes)
	return truncated + summaryTruncationMarker
}

// ApplyLogCap composes TruncateUTF8 + WriteLogSpill for the result-contract
// producer paths. It returns the truncated log, the project-relative spill
// path (or ""), and any error from the spill write.
//
// Best-effort: on spill write failure it returns the truncated text, an empty
// spill path, and the error so callers can surface it as a warning while
// still landing the truncated bytes on the contract.
//
// When fullLog is within the cap, returns (fullLog, "", nil) — callers should
// not write a notes-spill key in that case (omitempty drops it).
func ApplyLogCap(projectRoot, taskID, fullLog string) (string, string, error) {
	truncated, wasTruncated := TruncateUTF8(fullLog, NotesSizeCapBytes)
	if !wasTruncated {
		return fullLog, "", nil
	}
	spill, err := WriteLogSpill(projectRoot, taskID, fullLog)
	if err != nil {
		return truncated, "", err
	}
	return truncated, spill, nil
}
