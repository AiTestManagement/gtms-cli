// discover.go provides adapter-declared artefact discovery for the execute
// command's lazy automation-record creation (ENH-136).
//
// When gtms execute encounters a test case with no automation record, the
// execute command calls TryAutoCreateRecord. If the resolved adapter declares
// an artefact-glob pattern, DiscoverArtefact walks the project tree to find
// matching artefact files. On exactly one match, CreateAutomationRecord is
// called to create the missing record so execution can continue.
//
// The glob pattern supports {testcase} variable substitution and ** for
// recursive directory matching. filepath.Glob does NOT support **, so this
// package uses filepath.Walk with custom matching.
package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
)

// AutoCreateOptions carries caller-provided values for lazy record creation
// at execute time. The execute command populates this from the resolved adapter
// and current git state.
type AutoCreateOptions struct {
	TestCaseID   string // required: TC ID (e.g. "tc-abc12345")
	Framework    string // required: framework name from adapter resolution
	AdapterName  string // required: resolved adapter name for provenance
	Branch       string // optional: current git branch
	ArtefactGlob string // the glob pattern from adapter config (empty = no discovery)
}

// TryAutoCreateRecord attempts lazy record creation when no automation record
// exists and the resolved adapter declares artefact discovery. Called by the
// execute command at both missing-record gates.
//
// Returns the created record and its path, or (nil, "", nil) when auto-create
// is not applicable (empty glob pattern).
// Returns (nil, "", error) on discovery or creation failure.
func TryAutoCreateRecord(projectRoot string, opts AutoCreateOptions) (*AutomationRecord, string, error) {
	if opts.ArtefactGlob == "" {
		return nil, "", nil
	}

	artefact, err := DiscoverArtefact(projectRoot, opts.ArtefactGlob, opts.TestCaseID)
	if err != nil {
		return nil, "", err
	}

	createErr := CreateAutomationRecord(projectRoot, RecordOptions{
		TestCase:      opts.TestCaseID,
		Framework:     opts.Framework,
		Artefact:      artefact,
		Adapter:       opts.AdapterName,
		LastDevResult: "linked",
		Branch:        opts.Branch,
		Force:         false, // never overwrite existing records
	})
	if createErr != nil {
		return nil, "", createErr
	}

	// Read back the created record
	recordPath := filepath.Join(layout.RecordsDir(projectRoot),
		fmt.Sprintf("%s--%s.automation.md", opts.TestCaseID, opts.Framework))
	record, readErr := ReadAutomationRecord(recordPath)
	if readErr != nil {
		return nil, "", fmt.Errorf("reading auto-created record: %w", readErr)
	}

	return record, recordPath, nil
}

// DiscoverArtefact performs adapter-declared artefact discovery for a test case.
// It substitutes {testcase} in the glob pattern, walks the project tree, and
// returns exactly one project-relative artefact path.
//
// Returns ("", nil) when globPattern is empty (no discovery configured).
// Returns ("", error) on zero matches, multiple matches, or path-safety violations.
//
// The glob pattern supports:
//   - Standard filepath.Match wildcards (*, ?)
//   - ** for recursive directory matching (zero or more levels)
//   - {testcase} variable substitution
//
// The walk skips .git/, .gtms/, and the sentinel parent directory per ADR-014.
func DiscoverArtefact(projectRoot, globPattern, testCaseID string) (string, error) {
	if globPattern == "" {
		return "", nil
	}

	// Substitute {testcase} with the actual test case ID
	pattern := strings.ReplaceAll(globPattern, "{testcase}", testCaseID)

	// Split pattern into segments for matching
	patternSegments := strings.Split(filepath.ToSlash(pattern), "/")

	// Walk the project tree and collect matches. Glob hits that fail the
	// path-safety check (e.g. an in-tree symlink whose target escapes the
	// project root) are tracked separately so a zero-safe-match result can
	// explain *why* nothing actionable was found rather than degrading to a
	// misleading "No artefact found".
	parentDirName := layout.ParentDir()
	var matches []string
	var unsafeMatches []string

	err := filepath.Walk(projectRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}

		// Skip directories per ADR-014 (same exclusion as ResolveArtefact)
		if info.IsDir() {
			base := filepath.Base(path)
			if base == ".git" || base == ".gtms" {
				return filepath.SkipDir
			}
			// Skip sentinel parent at project root only
			if rel, relErr := filepath.Rel(projectRoot, path); relErr == nil && rel == parentDirName {
				return filepath.SkipDir
			}
			return nil
		}

		// Check if file matches the pattern
		rel, relErr := filepath.Rel(projectRoot, path)
		if relErr != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		relSegments := strings.Split(relSlash, "/")

		if matchDoublestar(patternSegments, relSegments) {
			// Validate path safety
			_, safeRel, safeErr := pathsafe.ResolveUnderRoot(projectRoot, relSlash)
			if safeErr != nil {
				unsafeMatches = append(unsafeMatches, relSlash)
				return nil
			}
			matches = append(matches, safeRel)
		}

		return nil
	})
	if err != nil {
		return "", fmt.Errorf("searching for artefact: %w", err)
	}

	switch len(matches) {
	case 0:
		// A safe match always wins (the case 1/default arms below), so an
		// unsafe candidate only matters when it is the reason discovery found
		// nothing actionable. Surface it as a path-safety failure rather than
		// the generic "No artefact found", which would mislead the user into
		// creating an artefact that already exists (just outside the root).
		if len(unsafeMatches) > 0 {
			return "", fmt.Errorf(
				"Artefact for %s rejected: path resolves outside the project root (path safety): %s",
				testCaseID, strings.Join(unsafeMatches, "\n  "),
			)
		}
		// Show the concrete pattern that was searched so users can inspect the exact artefact path shape.
		return "", fmt.Errorf(
			"No artefact found for %s matching pattern '%s'",
			testCaseID, pattern,
		)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf(
			"Multiple artefacts found for %s:\n  %s\nUse 'gtms link' to specify the correct artefact.",
			testCaseID, strings.Join(matches, "\n  "),
		)
	}
}

// matchDoublestar checks whether pathSegments match patternSegments, where
// a "**" segment in the pattern matches zero or more path segments.
// Non-** segments are matched with filepath.Match.
func matchDoublestar(patternSegments, pathSegments []string) bool {
	return matchDS(patternSegments, pathSegments)
}

// matchDS is the recursive implementation of doublestar matching.
func matchDS(pattern, path []string) bool {
	for len(pattern) > 0 {
		seg := pattern[0]

		if seg == "**" {
			// ** at end of pattern matches everything remaining
			if len(pattern) == 1 {
				return true
			}
			// Try matching ** against zero or more path segments
			rest := pattern[1:]
			for i := 0; i <= len(path); i++ {
				if matchDS(rest, path[i:]) {
					return true
				}
			}
			return false
		}

		// No more path segments to match
		if len(path) == 0 {
			return false
		}

		// Match the current segment with filepath.Match
		matched, err := filepath.Match(seg, path[0])
		if err != nil || !matched {
			return false
		}

		pattern = pattern[1:]
		path = path[1:]
	}

	// Pattern exhausted — path must also be exhausted
	return len(path) == 0
}
