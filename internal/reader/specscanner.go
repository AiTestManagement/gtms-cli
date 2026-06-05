package reader

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// tcIDPattern matches tc-xxx identifiers (case insensitive for legacy compatibility).
// Covers both legacy short IDs (e.g. tc-007) and canonical 8-hex IDs (e.g. tc-a1b2c3d0).
var tcIDPattern = regexp.MustCompile(`(?i)TC-[0-9a-f]{3,8}\b`)

// ScanSpecFiles walks each specDir (relative to projectRoot), reads all files,
// and extracts tc-xxx references. Returns a map from normalised (lowercase) tc ID
// to the list of spec file paths (relative to projectRoot) that reference it.
// Directories that don't exist are silently skipped.
func ScanSpecFiles(projectRoot string, specDirs []string) (map[string][]string, error) {
	result := make(map[string][]string)

	for _, specDir := range specDirs {
		absDir := filepath.Join(projectRoot, specDir)

		// Skip if directory doesn't exist
		info, err := os.Stat(absDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		if !info.IsDir() {
			continue
		}

		err = filepath.Walk(absDir, func(path string, fi os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if fi.IsDir() {
				return nil
			}

			content, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil // skip unreadable files
			}

			matches := tcIDPattern.FindAll(content, -1)
			if len(matches) == 0 {
				return nil
			}

			// Compute relative path from projectRoot
			relPath, relErr := filepath.Rel(projectRoot, path)
			if relErr != nil {
				relPath = path
			}
			relPath = filepath.ToSlash(relPath)

			seen := make(map[string]bool)
			for _, m := range matches {
				id := strings.ToLower(string(m))
				if !seen[id] {
					seen[id] = true
					result[id] = append(result[id], relPath)
				}
			}

			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	return result, nil
}
