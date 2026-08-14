package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
)

// IsBulkFolder reports whether arg names an existing, in-tree test-case folder
// (bulk processing). It delegates to the authoritative testcase.ResolveFolder, so
// an out-of-tree folder symlink is treated as "not a bulk folder" (err != nil)
// rather than followed via a raw os.Stat (REV-119 CODEX-001).
func IsBulkFolder(root, arg string) bool {
	_, err := testcase.ResolveFolder(root, arg)
	return err == nil
}

// DiscoverTestCases finds all tc-*.md files in {cases-dir}/{folder}/ and returns
// their TC IDs in alphabetical order. When recursive is false, only the immediate
// directory is scanned. When recursive is true, subdirectories are included.
// Returns an error if the folder doesn't exist or contains no tc-*.md files.
//
// REV-119 CODEX-001: the scan directory is the CANONICAL, contained path from
// testcase.ResolveFolder -- never a raw filepath.Join that could follow an
// out-of-tree symlink. A containment failure is surfaced as an error (no scan).
func DiscoverTestCases(root, folder string, recursive bool) ([]string, error) {
	paths := layout.Current()
	dir, rerr := testcase.ResolveFolder(root, folder)
	if rerr != nil {
		if errors.Is(rerr, testcase.ErrFolderNotFound) {
			return nil, fmt.Errorf("folder '%s/%s/' does not exist", paths.TestCases, folder)
		}
		return nil, rerr // containment/safety failure -- do not scan
	}

	var ids []string

	if recursive {
		// Walk all subdirectories
		werr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil // skip unreadable entries
			}
			if d.IsDir() {
				return nil
			}
			name := d.Name()
			if !strings.HasPrefix(name, "tc-") || !strings.HasSuffix(name, ".md") {
				return nil
			}
			tcID := extractTestCaseID(name)
			if tcID != "" {
				ids = append(ids, tcID)
			}
			return nil
		})
		if werr != nil {
			return nil, fmt.Errorf("walking %s/%s/: %w", paths.TestCases, folder, werr)
		}
	} else {
		// Non-recursive: read immediate directory only
		entries, readErr := os.ReadDir(dir)
		if readErr != nil {
			return nil, fmt.Errorf("reading %s/%s/: %w", paths.TestCases, folder, readErr)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if !strings.HasPrefix(name, "tc-") || !strings.HasSuffix(name, ".md") {
				continue
			}
			tcID := extractTestCaseID(name)
			if tcID != "" {
				ids = append(ids, tcID)
			}
		}
	}

	sort.Strings(ids)

	if len(ids) == 0 {
		return nil, fmt.Errorf("No test cases found in %s/%s/", paths.TestCases, folder)
	}

	return ids, nil
}

// extractTestCaseID extracts the test case ID from a filename.
// For "tc-a1b2c3d-login-happy.md" returns "tc-a1b2c3d".
// For "tc-007-simple.md" returns "tc-007".
// The pattern is: tc-{identifier} where identifier is the first segment after "tc-".
func extractTestCaseID(filename string) string {
	// Remove .md extension
	name := strings.TrimSuffix(filename, ".md")
	if name == filename {
		return "" // no .md extension
	}

	// Must start with "tc-"
	if !strings.HasPrefix(name, "tc-") {
		return ""
	}

	// Split after "tc-" to get the remaining parts
	rest := name[3:] // everything after "tc-"
	if rest == "" {
		return ""
	}

	// The TC ID is "tc-" plus the first dash-delimited segment
	parts := strings.SplitN(rest, "-", 2)
	return "tc-" + parts[0]
}
