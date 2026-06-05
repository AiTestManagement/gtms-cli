package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/aitestmanagement/gtms-cli/internal/adapter"
)

// writeTCFixture writes a minimal test case file with frontmatter to the given directory.
func writeTCFixture(t *testing.T, dir, tcID, title string) string {
	t.Helper()
	filename := fmt.Sprintf("%s-slug.md", tcID)
	path := filepath.Join(dir, filename)
	content := fmt.Sprintf("---\ntest_case_id: %s\ntitle: %s\n---\n\n# %s\n", tcID, title, title)
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	return filename
}

func TestFormatCreateOutput_ListsSingleTC(t *testing.T) {
	root := t.TempDir()
	tcDir := filepath.Join(root, "gtms/cases", "my-feature")
	require.NoError(t, os.MkdirAll(tcDir, 0755))

	fname := writeTCFixture(t, tcDir, "tc-aaa11111", "Login happy path")
	relPath := filepath.Join("gtms/cases", "my-feature", fname)

	res := &adapter.InvokeResult{
		TaskID:        "task-abc12345",
		Status:        "complete",
		Target:        "my-feature",
		Filename:      "task-abc-create-my-feature.md",
		Adapter:       "local-claude",
		Mode:          "sync",
		Branch:        "feature/create-my-feature",
		ArtifactCount: 1,
		ArtifactPaths: []string{relPath},
	}

	out := captureStdout(t, func() {
		formatCreateOutput(res, root)
	})

	// ENH-120: headline names TC IDs and cases folder
	assert.Contains(t, out, "Created 1 test case in gtms/cases/my-feature/:")
	assert.Contains(t, out, "tc-aaa11111")
	assert.Contains(t, out, "Login happy path")
	// ENH-120: task filename and branch NOT in non-verbose output
	assert.NotContains(t, out, "Task created:")
	assert.NotContains(t, out, "Branch:")
	// Adapter line preserved
	assert.Contains(t, out, "Adapter: local-claude (sync)")
}

func TestFormatCreateOutput_ListsBulkTCsUnderThreshold(t *testing.T) {
	root := t.TempDir()
	tcDir := filepath.Join(root, "gtms/cases", "sprint-14")
	require.NoError(t, os.MkdirAll(tcDir, 0755))

	var paths []string
	for i := 1; i <= 3; i++ {
		id := fmt.Sprintf("tc-%08x", i)
		title := fmt.Sprintf("Test case number %d", i)
		fname := writeTCFixture(t, tcDir, id, title)
		paths = append(paths, filepath.Join("gtms/cases", "sprint-14", fname))
	}

	res := &adapter.InvokeResult{
		TaskID:        "task-xyz12345",
		Status:        "complete",
		Target:        "sprint-14",
		Filename:      "task-xyz-create-sprint-14.md",
		Adapter:       "local-claude",
		Mode:          "sync",
		Branch:        "feature/create-sprint-14",
		ArtifactCount: 3,
		ArtifactPaths: paths,
	}

	out := captureStdout(t, func() {
		formatCreateOutput(res, root)
	})

	// ENH-120: artefact-focused headline
	assert.Contains(t, out, "Created 3 test cases in gtms/cases/sprint-14/:")
	assert.Contains(t, out, "tc-00000001")
	assert.Contains(t, out, "tc-00000002")
	assert.Contains(t, out, "tc-00000003")
	assert.NotContains(t, out, "...and")
	assert.NotContains(t, out, "Task created:")
	assert.NotContains(t, out, "Branch:")
}

func TestFormatCreateOutput_ListsBulkTCsOverThreshold(t *testing.T) {
	root := t.TempDir()
	tcDir := filepath.Join(root, "gtms/cases", "big-feature")
	require.NoError(t, os.MkdirAll(tcDir, 0755))

	var paths []string
	for i := 1; i <= 7; i++ {
		id := fmt.Sprintf("tc-%08x", i)
		title := fmt.Sprintf("Test case number %d", i)
		fname := writeTCFixture(t, tcDir, id, title)
		paths = append(paths, filepath.Join("gtms/cases", "big-feature", fname))
	}

	res := &adapter.InvokeResult{
		TaskID:        "task-xyz12345",
		Status:        "complete",
		Target:        "big-feature",
		Filename:      "task-xyz-create-big-feature.md",
		Adapter:       "local-claude",
		Mode:          "sync",
		Branch:        "feature/create-big-feature",
		ArtifactCount: 7,
		ArtifactPaths: paths,
	}

	out := captureStdout(t, func() {
		formatCreateOutput(res, root)
	})

	// ENH-120: artefact-focused headline with count and sample
	assert.Contains(t, out, "Created 7 test cases in gtms/cases/big-feature/:")
	// First 5 should be listed
	assert.Contains(t, out, "tc-00000001")
	assert.Contains(t, out, "tc-00000005")
	// 6th and 7th should NOT be listed
	assert.NotContains(t, out, "tc-00000006")
	assert.NotContains(t, out, "tc-00000007")
	assert.Contains(t, out, "...and 2 more")
	assert.Contains(t, out, "gtms status big-feature")
	assert.NotContains(t, out, "Task created:")
	assert.NotContains(t, out, "Branch:")
}

func TestFormatCreateOutput_MalformedFrontmatter(t *testing.T) {
	root := t.TempDir()
	tcDir := filepath.Join(root, "gtms/cases", "bad-fm")
	require.NoError(t, os.MkdirAll(tcDir, 0755))

	// Valid file
	validFname := writeTCFixture(t, tcDir, "tc-aaa11111", "Good test case")
	validPath := filepath.Join("gtms/cases", "bad-fm", validFname)

	// Malformed file (no frontmatter)
	badFname := "tc-bbb22222-broken.md"
	badContent := "# No frontmatter here\nJust plain markdown.\n"
	require.NoError(t, os.WriteFile(filepath.Join(tcDir, badFname), []byte(badContent), 0644))
	badPath := filepath.Join("gtms/cases", "bad-fm", badFname)

	res := &adapter.InvokeResult{
		TaskID:        "task-xyz12345",
		Status:        "complete",
		Target:        "bad-fm",
		Filename:      "task-xyz-create-bad-fm.md",
		Adapter:       "local-claude",
		Mode:          "sync",
		Branch:        "feature/create-bad-fm",
		ArtifactCount: 2,
		ArtifactPaths: []string{validPath, badPath},
	}

	out := captureStdout(t, func() {
		formatCreateOutput(res, root)
	})

	// ENH-120: artefact-focused headline
	assert.Contains(t, out, "Created 2 test cases in gtms/cases/bad-fm/:")
	assert.Contains(t, out, "tc-aaa11111")
	assert.Contains(t, out, "Good test case")
	assert.Contains(t, out, "tc-bbb22222")
	assert.Contains(t, out, "(no frontmatter)")
	assert.NotContains(t, out, "Task created:")
}

func TestFormatCreateOutput_TruncatesLongTitle(t *testing.T) {
	root := t.TempDir()
	tcDir := filepath.Join(root, "gtms/cases", "long-titles")
	require.NoError(t, os.MkdirAll(tcDir, 0755))

	longTitle := strings.Repeat("a", 180)
	fname := writeTCFixture(t, tcDir, "tc-ccc33333", longTitle)
	relPath := filepath.Join("gtms/cases", "long-titles", fname)

	res := &adapter.InvokeResult{
		TaskID:        "task-xyz12345",
		Status:        "complete",
		Target:        "long-titles",
		Filename:      "task-xyz-create-long-titles.md",
		Adapter:       "local-claude",
		Mode:          "sync",
		Branch:        "feature/create-long-titles",
		ArtifactCount: 1,
		ArtifactPaths: []string{relPath},
	}

	out := captureStdout(t, func() {
		formatCreateOutput(res, root)
	})

	assert.Contains(t, out, "tc-ccc33333")
	// Title should be truncated to 72 chars (69 + "...")
	assert.Contains(t, out, "...")
	// Full 180-char title should NOT appear
	assert.NotContains(t, out, longTitle)
	// ENH-120: no task filename or branch in non-verbose output
	assert.NotContains(t, out, "Task created:")
	assert.NotContains(t, out, "Branch:")
}

func TestFormatCreateOutput_EmptyArtifactPaths(t *testing.T) {
	root := t.TempDir()

	res := &adapter.InvokeResult{
		TaskID:        "task-xyz12345",
		Status:        "complete",
		Target:        "empty",
		Filename:      "task-xyz-create-empty.md",
		Adapter:       "local-claude",
		Mode:          "sync",
		Branch:        "feature/create-empty",
		ArtifactCount: 0,
		ArtifactPaths: nil,
	}

	out := captureStdout(t, func() {
		formatCreateOutput(res, root)
	})

	// ENH-120: artefact-focused headline even for zero-artefact case
	assert.Contains(t, out, "Created test case for empty")
	assert.NotContains(t, out, "Task created:")
	assert.NotContains(t, out, "Branch:")
}

func TestReadTCFrontmatter_ValidFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "gtms/cases", "feature")
	require.NoError(t, os.MkdirAll(dir, 0755))

	fname := writeTCFixture(t, dir, "tc-12345678", "My Test Title")
	relPath := filepath.Join("gtms/cases", "feature", fname)

	id, title := readTCFrontmatter(root, relPath)
	assert.Equal(t, "tc-12345678", id)
	assert.Equal(t, "My Test Title", title)
}

// ENH-121 finding #4: skeleton writes `name:` (not `title:`); the reader
// must fall back to `name:` so named skeletons don't surface as "(untitled)".
func TestReadTCFrontmatter_FallsBackToNameWhenTitleEmpty(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "gtms/cases", "feature")
	require.NoError(t, os.MkdirAll(dir, 0755))

	// Mirrors the skeleton adapter shape: name: present, no title:.
	path := filepath.Join(dir, "tc-12345678-user-can-login.md")
	content := "---\ntest_case_id: tc-12345678\nname: \"user-can-login\"\nstatus: draft\n---\n\n## Steps\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	relPath := filepath.Join("gtms/cases", "feature", "tc-12345678-user-can-login.md")
	id, title := readTCFrontmatter(root, relPath)
	assert.Equal(t, "tc-12345678", id)
	assert.Equal(t, "user-can-login", title)
}

// Both fields empty → "(untitled)" sentinel preserved.
func TestReadTCFrontmatter_UntitledWhenBothEmpty(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "gtms/cases", "feature")
	require.NoError(t, os.MkdirAll(dir, 0755))

	path := filepath.Join(dir, "tc-deadbeef.md")
	content := "---\ntest_case_id: tc-deadbeef\nname: \"\"\n---\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	relPath := filepath.Join("gtms/cases", "feature", "tc-deadbeef.md")
	id, title := readTCFrontmatter(root, relPath)
	assert.Equal(t, "tc-deadbeef", id)
	assert.Equal(t, "(untitled)", title)
}

// `title:` wins when both are present (regression guard for AI-adapter path).
func TestReadTCFrontmatter_TitlePreferredOverName(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "gtms/cases", "feature")
	require.NoError(t, os.MkdirAll(dir, 0755))

	path := filepath.Join(dir, "tc-cafef00d.md")
	content := "---\ntest_case_id: tc-cafef00d\ntitle: \"Login happy path\"\nname: \"login-happy\"\n---\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))

	relPath := filepath.Join("gtms/cases", "feature", "tc-cafef00d.md")
	id, title := readTCFrontmatter(root, relPath)
	assert.Equal(t, "tc-cafef00d", id)
	assert.Equal(t, "Login happy path", title)
}

func TestReadTCFrontmatter_MissingFile(t *testing.T) {
	root := t.TempDir()
	relPath := filepath.Join("gtms/cases", "feature", "tc-deadbeef-missing.md")

	id, title := readTCFrontmatter(root, relPath)
	assert.Equal(t, "tc-deadbeef", id)
	assert.Equal(t, "(no frontmatter)", title)
}

func TestExtractTCIDFromFilename(t *testing.T) {
	tests := []struct {
		filename string
		expected string
	}{
		{"tc-aabbccdd-my-test.md", "tc-aabbccdd"},
		{"tc-12345678.md", "tc-12345678"},
		{"no-tc-id-here.md", "no-tc-id-here"},
		{"TC-AABBCCDD-upper.md", "TC-AABBCCDD-upper"}, // uppercase TC- not matched by regex, falls back to filename
	}
	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			result := extractTCIDFromFilename(tt.filename)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTruncateTitle(t *testing.T) {
	// Short title: no truncation
	assert.Equal(t, "short", truncateTitle("short"))

	// Exactly 72 chars: no truncation
	exact := strings.Repeat("x", 72)
	assert.Equal(t, exact, truncateTitle(exact))

	// 73 chars: truncated
	long := strings.Repeat("x", 73)
	result := truncateTitle(long)
	assert.Equal(t, 72, len(result))
	assert.True(t, strings.HasSuffix(result, "..."))
}

// captureStdout redirects os.Stdout to capture output from a function.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)

	os.Stdout = w

	fn()

	w.Close()
	os.Stdout = old

	data, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(data)
}
