package cli

// Tree-integrity tests audit the active dogfood corpus on disk for
// invariants that the product cannot enforce at runtime (e.g. AI-generated
// spec files where filename ID disagrees with frontmatter test_case_id —
// the BUG-038 class). They walk the project's gtms/cases/ tree from the
// repo root rather than constructing fixtures in t.TempDir().
//
// Replaces test/acceptance/expand-id-width-to-8-hex/tc-4bbb946 step 3
// (BUG-068 close-out, 2026-05-05). The other 10 retired tests in that
// directory checked one-shot ENH-065 migration outcomes that the product
// makes regression-impossible (id.New() emits 8-char hex by construction);
// only the filename-vs-frontmatter audit had ongoing value, so it was
// promoted into go test ./... here.

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTreeIntegrity_TestCaseFilenameMatchesFrontmatterID walks the active
// gtms/cases/ tree from the project root and asserts that every tc-*.md
// file's filename ID matches the test_case_id: declared in its YAML
// frontmatter. The active BUG-038 detector.
//
// Carve-outs:
//   - specs-verify-fixture/ — ENH-095 negative-test fixtures that
//     deliberately carry mismatched / malformed IDs so /specs-verify
//     can demonstrate catching them.
func TestTreeIntegrity_TestCaseFilenameMatchesFrontmatterID(t *testing.T) {
	root := findGTMSProjectRoot(t)
	casesDir := filepath.Join(root, "gtms", "cases")
	if info, err := os.Stat(casesDir); err != nil || !info.IsDir() {
		t.Skipf("gtms/cases/ not found at %s — repo-shape audit skipped", casesDir)
	}

	var mismatches []string
	checked := 0

	walkErr := filepath.WalkDir(casesDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			rel, _ := filepath.Rel(casesDir, path)
			rel = filepath.ToSlash(rel)
			if rel == "specs-verify-fixture" {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasPrefix(name, "tc-") || !strings.HasSuffix(name, ".md") {
			return nil
		}

		filenameID := extractTestCaseID(name)
		if filenameID == "" {
			return nil
		}

		frontmatterID, err := readFrontmatterTestCaseID(path)
		if err != nil {
			rel, _ := filepath.Rel(root, path)
			mismatches = append(mismatches, fmt.Sprintf("  %s — read failed: %v", filepath.ToSlash(rel), err))
			return nil
		}

		checked++

		if frontmatterID == "" {
			rel, _ := filepath.Rel(root, path)
			mismatches = append(mismatches, fmt.Sprintf("  %s — missing test_case_id frontmatter", filepath.ToSlash(rel)))
			return nil
		}

		if filenameID != frontmatterID {
			rel, _ := filepath.Rel(root, path)
			mismatches = append(mismatches, fmt.Sprintf("  %s — filename=%s frontmatter=%s",
				filepath.ToSlash(rel), filenameID, frontmatterID))
		}
		return nil
	})
	require.NoError(t, walkErr, "walking gtms/cases/")

	// Sanity floor: this test is only meaningful against the active dogfood
	// corpus. If we found no specs, we're probably running in a partial
	// checkout and should not assert.
	const sanityFloor = 100
	if checked < sanityFloor {
		t.Skipf("only %d test cases scanned under %s — below sanity floor of %d, audit skipped",
			checked, casesDir, sanityFloor)
	}

	if len(mismatches) > 0 {
		t.Fatalf("filename ID does not match frontmatter test_case_id in %d file(s) (scanned %d):\n%s",
			len(mismatches), checked, strings.Join(mismatches, "\n"))
	}
}

// findGTMSProjectRoot walks up from the test's CWD looking for gtms.config.
// Internal-cli tests run with CWD = internal/cli, so the walk is short.
func findGTMSProjectRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "gtms.config")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("project root not found: no gtms.config in any ancestor of %s", dir)
		}
		dir = parent
	}
}

// readFrontmatterTestCaseID parses YAML frontmatter from path and returns the
// test_case_id value. Returns "" if no frontmatter or no test_case_id line.
// Strips surrounding quotes and \r so behaviour matches across platforms.
func readFrontmatterTestCaseID(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", nil
	}
	for i := 1; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "---" {
			return "", nil
		}
		if !strings.HasPrefix(line, "test_case_id:") {
			continue
		}
		val := strings.TrimSpace(strings.TrimPrefix(line, "test_case_id:"))
		val = strings.TrimRight(val, "\r")
		val = strings.Trim(val, `"'`)
		return val, nil
	}
	return "", nil
}
