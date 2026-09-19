//go:build !windows

package result

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withUmask sets the process umask for the duration of the test and restores
// the previous value afterwards. The umask is process-global, so no test that
// uses it may call t.Parallel().
func withUmask(t *testing.T, mask int) {
	t.Helper()
	previous := syscall.Umask(mask)
	t.Cleanup(func() { syscall.Umask(previous) })
}

// fileMode returns the permission bits of path.
func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	return info.Mode().Perm()
}

// REV-164 CLAUDE-R01: Reconstruct must produce the same permissions as every
// other handoff write on the same machine.
//
// The bug it pins: Reconstruct used os.CreateTemp (which hardcodes 0600)
// followed by os.Chmod(0644). os.Chmod is NOT umask-filtered, but file
// creation IS -- and Create/Update both write with os.WriteFile(..., 0644).
// So under umask 077 a normal handoff came out 0600 while a RECOVERED handoff
// came out 0644, world-readable, and the recovered one is precisely the file
// carrying captured adapter stdout/stderr (ENH-212 records that as potentially
// secret-bearing). Under the default umask 022 both are 0644, which is why no
// existing test caught it.
//
// The test therefore runs under a NON-default umask and asserts the exact
// expected bits, so it cannot pass vacuously on a 022 box.
func TestReconstruct_RespectsUmask(t *testing.T) {
	withUmask(t, 0o077)

	root := t.TempDir()

	// Control: the write shape Create and Update actually use. Under umask 077
	// this must come out 0600. If it does not, the filesystem is not applying
	// the umask and the comparison below would prove nothing -- so fail loudly
	// rather than skip.
	control := filepath.Join(root, "control.handoff.yaml")
	require.NoError(t, os.WriteFile(control, []byte("task: control\n"), 0644))
	require.Equal(t, os.FileMode(0o600), fileMode(t, control),
		"umask 077 is not being applied to file creation on this filesystem; the assertion below would be meaningless")

	path := filepath.Join(root, "task-umask01.handoff.yaml")
	require.NoError(t, Reconstruct(path, errorContract("task-umask01")))

	assert.Equal(t, os.FileMode(0o600), fileMode(t, path),
		"the recovered handoff must respect the umask, not bypass it with chmod")
	assert.Equal(t, fileMode(t, control), fileMode(t, path),
		"a recovered handoff must carry the same permissions as a normal one")

	// The recovery still produced a readable, valid contract.
	rc, err := Read(path)
	require.NoError(t, err)
	assert.Equal(t, "error", rc.Status)

	assertNoTempLitter(t, root)
}

// The default-umask case still lands 0644, so tightening the mode under a
// restrictive umask has not made handoffs unreadable on a normal box.
func TestReconstruct_DefaultUmaskStillYields0644(t *testing.T) {
	withUmask(t, 0o022)

	root := t.TempDir()

	control := filepath.Join(root, "control.handoff.yaml")
	require.NoError(t, os.WriteFile(control, []byte("task: control\n"), 0644))
	require.Equal(t, os.FileMode(0o644), fileMode(t, control))

	path := filepath.Join(root, "task-umask02.handoff.yaml")
	require.NoError(t, Reconstruct(path, errorContract("task-umask02")))

	assert.Equal(t, os.FileMode(0o644), fileMode(t, path))
	assert.Equal(t, fileMode(t, control), fileMode(t, path))
	assertNoTempLitter(t, root)
}

// Create and Update are the parity target for the assertions above; pin that
// they too follow the umask, so a future change to either side is caught by
// the same suite rather than silently re-opening the divergence.
func TestCreateAndUpdate_RespectUmask(t *testing.T) {
	withUmask(t, 0o077)

	root := t.TempDir()

	path, err := Create(root, &ResultContract{
		Task:    "task-umask03",
		Command: "execute",
		Target:  "tc-11111111",
		Adapter: "some-adapter",
		Mode:    "sync",
	})
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fileMode(t, path))

	require.NoError(t, Update(path, map[string]interface{}{"status": "error"}))
	assert.Equal(t, os.FileMode(0o600), fileMode(t, path))
}
