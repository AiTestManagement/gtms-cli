// Package tapparse classifies TAP (Test Anything Protocol) output lines.
//
// BATS emits TAP-formatted results where:
//
//	ok 1 test name          → pass
//	not ok 2 test name      → fail
//	ok 3 test name # skip R → skip (reason R)
//
// The critical subtlety: skipped tests share the "ok" prefix with passes.
// Naive grep on "^ok" conflates the two, hiding regressions behind tool-
// availability skips (see ENH-091).
package tapparse

import (
	"strings"
)

// Result classifies a single TAP output line.
type Result int

const (
	// Ignored means the line is not a TAP test result (comment, plan, blank).
	Ignored Result = iota
	// Pass means the test passed.
	Pass
	// Fail means the test failed.
	Fail
	// Skip means the test was skipped (with an optional reason).
	Skip
)

// Line holds the parsed components of a TAP result line.
type Line struct {
	Result Result
	Name   string // test name (empty for non-result lines)
	Reason string // skip reason (empty unless Result == Skip)
}

// Parse classifies a single TAP output line and extracts its components.
func Parse(line string) Line {
	trimmed := strings.TrimSpace(line)

	// Check for "not ok" first (fail).
	if strings.HasPrefix(trimmed, "not ok ") {
		rest := trimmed[len("not ok "):]
		name := extractName(rest)
		return Line{Result: Fail, Name: name}
	}

	// Check for "ok" (pass or skip).
	if strings.HasPrefix(trimmed, "ok ") {
		rest := trimmed[len("ok "):]

		// Check for skip directive: "# skip" (case-insensitive).
		name, reason, isSkip := extractSkip(rest)
		if isSkip {
			return Line{Result: Skip, Name: name, Reason: reason}
		}

		name = extractName(rest)
		return Line{Result: Pass, Name: name}
	}

	return Line{Result: Ignored}
}

// extractSkip checks if the rest of a TAP line (after "ok N") contains a
// skip directive. Returns the test name, skip reason, and whether it's a skip.
func extractSkip(rest string) (name, reason string, isSkip bool) {
	// Find "# skip" (case-insensitive) in the line.
	lower := strings.ToLower(rest)
	idx := strings.Index(lower, "# skip")
	if idx < 0 {
		return "", "", false
	}

	// Everything before "# skip" is the test number + name.
	before := strings.TrimSpace(rest[:idx])
	name = stripNumber(before)

	// Everything after "# skip" is the reason (skip the "# skip" prefix itself).
	after := rest[idx+len("# skip"):]
	// The reason may be separated by a space or nothing.
	reason = strings.TrimSpace(after)

	return name, reason, true
}

// extractName strips the test number prefix from a TAP result, returning just
// the test name.
func extractName(rest string) string {
	return stripNumber(strings.TrimSpace(rest))
}

// stripNumber removes a leading integer (the TAP test number) from a string.
func stripNumber(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return strings.TrimSpace(s[i:])
}
