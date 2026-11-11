package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BUG-214 admission boundary: `gtms create` must refuse a containment violation
// BEFORE the adapter starts and before any task or result file is written, and on
// an accepted target it must run the adapter with the containment-checked output
// directory. These tests drive the real create command with a MARKER adapter
// whose only side effect is a file, so its presence/absence proves whether the
// adapter actually ran.

// markerCreateConfig returns a config whose default create adapter is a Tier 2
// script that records that it ran (and the output dir it was given) into
// adapter-ran.txt at the project root.
func markerCreateConfig() *config.Config {
	return &config.Config{
		Project: config.ProjectConfig{Name: "Test", Repo: "org/test"},
		Adapters: map[string]map[string]*config.AdapterConfig{
			"create": {
				"marker": {
					Mode:   "sync",
					Script: "gtms/adapters/marker.sh",
				},
			},
		},
		Defaults: map[string]string{"create": "marker"},
	}
}

func writeMarkerAdapter(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, "gtms", "adapters")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	script := "#!/bin/sh\nprintf '%s' \"$GTMS_OUTPUT_DIR\" > \"$GTMS_PROJECT_ROOT/adapter-ran.txt\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "marker.sh"), []byte(script), 0o755))
}

func countFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() {
			n++
		}
		return nil
	})
	return n
}

// Refusal path: an out-of-tree linked folder is refused; the marker adapter never
// runs, and no task or result file is created.
func TestCreateAdmission_RefusesLinkedFolder_AdapterNeverStarts(t *testing.T) {
	skipIfShort(t)
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privilege on Windows; junction coverage is Pester (BUG-214 D10)")
	}
	root := setupCompatProject(t)
	writeMarkerAdapter(t, root)
	restore := withExecuteGlobals(t, root, markerCreateConfig())
	defer restore()

	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "gtms/test/cases/ext")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	out, err := runCmdCaptureStderr(t, newCreateCmd(), "ext", "outside-write")
	require.Error(t, err)
	assert.Contains(t, out, "resolves outside the test cases directory")

	// Adapter never ran.
	assert.NoFileExists(t, filepath.Join(root, "adapter-ran.txt"))
	// Nothing written outside the project.
	assert.Equal(t, 0, countFiles(t, outside), "outside dir must stay empty")
	// No task or result files created.
	assert.Equal(t, 0, countFiles(t, filepath.Join(root, "gtms", "tasks")), "no task files on refusal")
	assert.Equal(t, 0, countFiles(t, filepath.Join(root, ".gtms", "results")), "no result files on refusal")
}

// Accepted path: an ordinary in-tree folder runs the adapter with the
// containment-checked output directory (the real cases/<folder> path).
func TestCreateAdmission_AcceptsInTreeFolder_AdapterGetsCheckedPath(t *testing.T) {
	skipIfShort(t)
	root := setupCompatProject(t)
	writeMarkerAdapter(t, root)
	restore := withExecuteGlobals(t, root, markerCreateConfig())
	defer restore()

	_, err := runCmdCaptureStderr(t, newCreateCmd(), "feature", "ok-name")
	require.NoError(t, err)

	got, readErr := os.ReadFile(filepath.Join(root, "adapter-ran.txt"))
	require.NoError(t, readErr, "adapter must have run")

	// The adapter received the checked output dir, which physically denotes
	// gtms/test/cases/feature under the project root.
	wantResolved, err := filepath.EvalSymlinks(filepath.Join(root, "gtms", "test", "cases", "feature"))
	require.NoError(t, err)
	gotResolved, err := filepath.EvalSymlinks(string(got))
	require.NoError(t, err)
	assert.Equal(t, wantResolved, gotResolved)
}

// Name validation moves ahead of folder creation (Owner Decision 3 / AC14): an
// invalid name is refused without creating the target folder.
func TestCreateAdmission_InvalidName_CreatesNoFolder(t *testing.T) {
	skipIfShort(t)
	root := setupCompatProject(t)
	writeMarkerAdapter(t, root)
	restore := withExecuteGlobals(t, root, markerCreateConfig())
	defer restore()

	out, err := runCmdCaptureStderr(t, newCreateCmd(), "fresh-folder", "bad.name")
	require.Error(t, err)
	assert.Contains(t, out, "Invalid name 'bad.name'")

	assert.NoDirExists(t, filepath.Join(root, "gtms", "test", "cases", "fresh-folder"),
		"invalid name must not create the target folder")
	assert.NoFileExists(t, filepath.Join(root, "adapter-ran.txt"), "adapter must not run on invalid name")
}
