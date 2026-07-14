// Package testcase provides shared test-case existence validation.
//
// BUG-059: extracted from duplicated private helpers in pipeline and cli
// packages to provide a single canonical check used by all write paths
// (link, automate, execute).
package testcase

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
)

// Exists reports whether a test case spec file exists under the
// project's cases directory for the given target.
//
// Target shapes:
//   - Unqualified: "tc-abc123" -- matches anywhere under gtms/test/cases/
//   - Folder-qualified: "folder/tc-abc123" -- matches only under gtms/test/cases/folder/
//
// A match requires a file whose base name starts with the TC ID followed
// by "-" or "." (e.g. tc-abc123-login-test.md, tc-abc123.md).
func Exists(projectRoot, target string) bool {
	// Parse folder-qualified targets: "folder/tc-abc123" -> folder="folder", tcID="tc-abc123"
	var searchDir string
	tcID := target
	if idx := strings.LastIndex(target, "/"); idx >= 0 {
		folder := target[:idx]
		tcID = target[idx+1:]
		searchDir = filepath.Join(layout.TestCasesDir(projectRoot), folder)
	} else {
		searchDir = layout.TestCasesDir(projectRoot)
	}

	// If the search directory does not exist, the TC cannot exist.
	if _, err := os.Stat(searchDir); os.IsNotExist(err) {
		return false
	}

	found := false
	_ = filepath.Walk(searchDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if strings.HasPrefix(base, tcID+"-") || strings.HasPrefix(base, tcID+".") {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// FindSource returns the project-relative path (forward-slash separated) to the
// test case spec file for the given target. Returns "" if not found.
//
// ENH-134: extracted to support manualUpdateHash which needs the TC file path
// for hash computation.
func FindSource(projectRoot, target string) string {
	searchDir := layout.TestCasesDir(projectRoot)
	tcID := target
	if idx := strings.LastIndex(target, "/"); idx >= 0 {
		folder := target[:idx]
		tcID = target[idx+1:]
		searchDir = filepath.Join(layout.TestCasesDir(projectRoot), folder)
	}

	if _, err := os.Stat(searchDir); os.IsNotExist(err) {
		return ""
	}

	var found string
	_ = filepath.Walk(searchDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if strings.HasPrefix(base, tcID+"-") || strings.HasPrefix(base, tcID+".") {
			rel, relErr := filepath.Rel(projectRoot, path)
			if relErr == nil {
				found = filepath.ToSlash(rel)
			} else {
				found = path
			}
			return filepath.SkipAll
		}
		return nil
	})
	return found
}
