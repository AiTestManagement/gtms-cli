package reader

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBulkReads_ReservedCompanionsAreSilent(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: reserved\n  repo: org/reserved\n")
	fixtures := map[string]string{
		"gtms/test/cases/index.md":              "---\ntest_case_id: [broken\n",
		"gtms/test/cases/log.md":                "---\ntest_case_id: tc-11111111\n---\n",
		"gtms/test/cases/nested/index.md":       "---\ntest_case_id: tc-22222222\n---\n",
		"gtms/test/cases/nested/log.md":         "---\ntest_case_id: [broken\n",
		"gtms/test/cases/README.md":             "---\ntype: Reference\n---\n",
		"gtms/test/cases/nested/notes.md":       "notes\n",
		"gtms/test/cases/nested/UPPER.MD":       "---\ntest_case_id: tc-33333333\n---\n",
		"gtms/test/cases/nested/attachment.txt": "test_case_id: tc-44444444\n",
		"gtms/test/cases/nested/extensionless":  "test_case_id: tc-55555555\n",
	}
	for path, body := range fixtures {
		writeFile(t, root, path, body)
	}

	stderr := captureReaderStderr(t, func() {
		entries, err := PipelineStatus(root, nil, "", false)
		require.NoError(t, err)
		assert.Empty(t, entries)

		folders, err := PipelineFolderSummary(root, "")
		require.NoError(t, err)
		assert.Empty(t, folders)

		mapping, err := Map(root, nil, "", false)
		require.NoError(t, err)
		assert.Empty(t, mapping.Groups)
		assert.Empty(t, mapping.Unlinked)

		gaps, err := Gaps(root, nil, "", false)
		require.NoError(t, err)
		assert.Equal(t, 0, gaps.TotalTestCases)

		views, err := ListTriage(root, nil, "", false)
		require.NoError(t, err)
		assert.Empty(t, views)
	})
	assert.NotContains(t, stderr, "warning: skipping")
	assert.NotContains(t, stderr, "index.md")
	assert.NotContains(t, stderr, "log.md")

	t.Run("malformed canonical control remains diagnosed", func(t *testing.T) {
		controlRoot := t.TempDir()
		writeFile(t, controlRoot, "gtms.config", "project:\n  name: control\n  repo: org/control\n")
		writeFile(t, controlRoot, filepath.Join("gtms/test/cases", "tc-deadbeef-broken.md"),
			"---\ntest_case_id: [broken\n")
		_, err := PipelineStatus(controlRoot, nil, "", false)
		require.Error(t, err)
		assert.True(t, strings.Contains(err.Error(), "tc-deadbeef-broken.md") || strings.Contains(err.Error(), "tc-deadbeef"),
			"diagnostic must identify the malformed canonical control: %v", err)
	})
}

func captureReaderStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = old })

	fn()
	require.NoError(t, w.Close())
	os.Stderr = old
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	return string(data)
}
