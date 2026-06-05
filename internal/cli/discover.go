package cli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
)

// IsBulkFolder checks whether {cases-dir}/{arg}/ exists as a directory,
// indicating that the argument should be treated as a folder for bulk processing.
func IsBulkFolder(root, arg string) bool {
	dir := filepath.Join(layout.CasesDir(root), arg)
	info, err := os.Stat(dir)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// DiscoverTestCases finds all tc-*.md files in {cases-dir}/{folder}/ and returns
// their TC IDs in alphabetical order. When recursive is false, only the immediate
// directory is scanned. When recursive is true, subdirectories are included.
// Returns an error if the folder doesn't exist or contains no tc-*.md files.
func DiscoverTestCases(root, folder string, recursive bool) ([]string, error) {
	paths := layout.Current()
	dir := filepath.Join(root, paths.Cases, folder)
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("folder '%s/%s/' does not exist", paths.Cases, folder)
		}
		return nil, fmt.Errorf("checking folder %s/%s/: %w", paths.Cases, folder, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s/%s is not a directory", paths.Cases, folder)
	}

	var ids []string

	if recursive {
		// Walk all subdirectories
		err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
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
		if err != nil {
			return nil, fmt.Errorf("walking %s/%s/: %w", paths.Cases, folder, err)
		}
	} else {
		// Non-recursive: read immediate directory only
		entries, readErr := os.ReadDir(dir)
		if readErr != nil {
			return nil, fmt.Errorf("reading %s/%s/: %w", paths.Cases, folder, readErr)
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
		return nil, fmt.Errorf("No test cases found in %s/%s/", paths.Cases, folder)
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
