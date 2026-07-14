// Package onboarding provides read-only checks that surface onboarding
// guidance at runtime -- for example, nudging the user to paste the GTMS
// agent-instructions snippet when their instruction file omits a GTMS
// mention. The checks are side-effect-free: they never create or modify
// files.
package onboarding

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/output"
)

// recognisedInstructionFiles lists the project-root-relative paths that
// GTMS considers "agent instruction files". For regular files the content
// is read directly; for directories every regular file inside is scanned.
var recognisedInstructionFiles = []string{
	"CLAUDE.md",
	"AGENTS.md",
	filepath.Join(".github", "copilot-instructions.md"),
	filepath.Join(".cursor", "rules"),
}

// snippetRelPath is the project-relative path to the paste-ready snippet
// produced by gtms init (ENH-183).
var snippetRelPath = filepath.Join("gtms", "AGENTS-SNIPPET.md")

// NudgeWithSnippet is the nudge text printed when a recognised instruction
// file exists but contains no GTMS mention and the snippet file is present.
const NudgeWithSnippet = "Tip: your agent instructions do not mention gtms -- paste gtms/AGENTS-SNIPPET.md into your CLAUDE.md so agents find the pipeline automatically."

// NudgeWithoutSnippet is the nudge text printed when the snippet file is
// absent but a recognised instruction file exists without a GTMS mention.
const NudgeWithoutSnippet = "Tip: your agent instructions do not mention gtms -- run gtms agent for the operating quick reference."

// CheckAgentInstructions scans the recognised agent instruction files under
// projectRoot for a case-insensitive "gtms" mention. When a file exists but
// none mentions GTMS, a single dim nudge line is printed to w (stderr).
//
// Behaviour by state:
//   - Any recognised file mentions GTMS   -> silent (short-circuit)
//   - No recognised file/dir exists at all -> silent
//   - File(s) exist, none mentions GTMS   -> print one nudge line
//
// The function is read-only: it never creates or modifies any file.
func CheckAgentInstructions(projectRoot string, w io.Writer) {
	anyExists := false
	anyMentions := false

	for _, relPath := range recognisedInstructionFiles {
		absPath := filepath.Join(projectRoot, relPath)
		info, err := os.Stat(absPath)
		if err != nil {
			continue // does not exist or not readable
		}
		anyExists = true

		if info.IsDir() {
			if mentionsGTMSInDir(absPath) {
				anyMentions = true
				break
			}
		} else {
			if mentionsGTMSInFile(absPath) {
				anyMentions = true
				break
			}
		}
	}

	if !anyExists || anyMentions {
		return
	}

	// A recognised instruction file exists but none mentions GTMS.
	snippetPath := filepath.Join(projectRoot, snippetRelPath)
	if _, err := os.Stat(snippetPath); err == nil {
		output.Dimln(w, NudgeWithSnippet)
	} else {
		output.Dimln(w, NudgeWithoutSnippet)
	}
}

// mentionsGTMSInFile reads the file at path and returns true if its content
// contains "gtms" (case-insensitive). Read errors are treated as "no mention".
func mentionsGTMSInFile(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(data)), "gtms")
}

// mentionsGTMSInDir walks the directory at dirPath and returns true if any
// regular file inside mentions "gtms" (case-insensitive). Walk errors on
// individual files are silently skipped.
func mentionsGTMSInDir(dirPath string) bool {
	found := false
	_ = filepath.WalkDir(dirPath, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if mentionsGTMSInFile(path) {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}
