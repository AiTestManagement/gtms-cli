package pipeline

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
)

// ResolveArtefact returns the actual path to an artefact file.
// It tries storedPath first (fast, exact). If the file exists on disk at the
// stored path, it is returned immediately — the basename is NOT checked for
// the TC ID (ENH-110: the basename check was dropped to support shared-file
// frameworks like Playwright grouped tests where the TC ID lives only in the
// test name, not the filename).
//
// When the stored path is empty or the file does not exist on disk, the
// function falls back to globbing for files whose name contains the TC ID,
// excluding .git/, .gtms/, and the sentinel-discovered parent directory per
// ADR-014 + ENH-098/ADR-017.
//
// Directory exclusion rules:
//   - .git/ and .gtms/: basename-matched at any depth (historically safe).
//   - Sentinel parent (e.g. "gtms/" or "testing/"): root-anchored only via
//     filepath.Rel — nested directories with the same name are NOT excluded.
//
// layout.ParentDir reads a synchronized snapshot of the configured layout.
//
// Returns the resolved path, or error if zero or multiple matches found.
func ResolveArtefact(projectRoot, storedPath, testCaseID string) (string, error) {
	// Fast path: try the stored path first. If the file exists on disk,
	// use it directly. The stored path is a performance hint — no basename
	// validation is performed (ENH-110 dropped the basename-contains-TC-ID
	// check to support shared-file frameworks).
	//
	// BUG-057: canonicalise and containment-check the stored path via
	// pathsafe.ResolveUnderRoot. Both absolute and relative inputs are
	// validated — a malicious or corrupted automation record cannot point
	// at a file outside projectRoot. The returned relPath is always
	// filepath.ToSlash-normalised and project-relative, matching the glob
	// fallback shape.
	if storedPath != "" {
		absPath, relPath, safeErr := pathsafe.ResolveUnderRoot(projectRoot, storedPath)
		if safeErr != nil {
			return "", safeErr // typed *pathsafe.PathSafetyError propagates to caller
		}
		if _, err := os.Stat(absPath); err == nil {
			return relPath, nil // normalised relative form, same shape as glob fallback
		}
	}

	// Fallback: search for the file by tc-{id} pattern
	pattern := testCaseID // e.g. "tc-a3f72b1"
	var matches []string

	// ENH-098: derive parent dir name from layout defaults.
	// For "gtms/test/cases" this gives "gtms"; for "testing/cases" it gives "testing".
	parentDirName := layout.ParentDir()

	err := filepath.Walk(projectRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}

		// Skip GTMS-internal and VCS directories — these never contain artefacts
		if info.IsDir() {
			base := filepath.Base(path)
			// Per ADR-014 + ENH-098: skip .git and .gtms at any depth.
			if base == ".git" || base == ".gtms" {
				return filepath.SkipDir
			}
			// Per ADR-014 + ENH-098: skip the sentinel-discovered parent (which
			// contains cases/, automation/, tasks/, execution/, scripts/) only at
			// the project root — nested dirs with the same name are walked.
			if rel, relErr := filepath.Rel(projectRoot, path); relErr == nil && rel == parentDirName {
				return filepath.SkipDir
			}
			return nil
		}

		// Check if filename contains the tc-{id} pattern
		name := filepath.Base(path)
		if strings.Contains(name, pattern) {
			rel, relErr := filepath.Rel(projectRoot, path)
			if relErr == nil {
				matches = append(matches, filepath.ToSlash(rel))
			}
		}

		return nil
	})
	if err != nil {
		return "", fmt.Errorf("searching for artefact: %w", err)
	}

	switch len(matches) {
	case 0:
		// ENH-110: SPEC §2.5 error format — include stored path when available.
		if storedPath != "" {
			return "", fmt.Errorf("artefact for %s not found at %s and no glob match",
				testCaseID, storedPath)
		}
		return "", fmt.Errorf("artefact for %s not found (no stored path and no glob match)",
			testCaseID)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("multiple artefact files found for %s:\n  %s\n\nRe-run 'gtms automate' to update the record, or manually edit the artefact field in the automation record",
			testCaseID, strings.Join(matches, "\n  "))
	}
}

// AbsArtefactPath returns the absolute path for an artefact, resolving relative paths against projectRoot.
func AbsArtefactPath(projectRoot, artefactPath string) string {
	if filepath.IsAbs(artefactPath) {
		return artefactPath
	}
	return filepath.Join(projectRoot, artefactPath)
}

// ResolveTestCaseSpec returns the project-relative forward-slash path to the
// test case spec for the given TC ID. It honours subfolder-scoped cases
// (e.g. gtms/test/cases/{subfolder}/{tc}.md) by delegating to testcase.FindSource.
//
// ENH-117: single pipeline-level resolver used by all automation-record write
// paths. Callers must NOT compute the spec path locally.
func ResolveTestCaseSpec(projectRoot, tcID string) (string, error) {
	p := testcase.FindSource(projectRoot, tcID)
	if p == "" {
		return "", fmt.Errorf("test case spec for %s not found under %s", tcID, layout.TestCasesDir(projectRoot))
	}
	return p, nil
}

// HashFile returns the SHA-256 hash of the file at path, truncated to 16 hex chars.
// Used by execute (to store) and reader commands (to compare).
func HashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading file for hash: %w", err)
	}

	data = bytes.ReplaceAll(data, []byte("\r"), []byte(""))
	hash := sha256.Sum256(data)
	return fmt.Sprintf("%x", hash[:8]), nil // 8 bytes = 16 hex chars
}
