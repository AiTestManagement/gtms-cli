//go:build windows

package scaffold

// BUG-213 REV-254 delta CODEX-001 (Windows only). The POSIX
// TestBUG213_LinkedVscodeWithExistingFilesRefused skips on Windows, and the
// tc-df4d0d17 Pester junction row leaves the outside folder empty, so on Windows
// nothing exercised the REV-254 OWNER-001 branch where settings.json /
// extensions.json already exist under an escaping .vscode/ junction. This test
// builds a REAL directory junction with `cmd.exe /d /c mklink /J` (a creation
// failure FAILS, never skips -- mklink /J needs no administrator rights) and
// drives initVscodeFile, the same code path the POSIX test uses.
//
// It relies on the go.mod-embedded winsymlink=0 default (BUG-205), under which a
// junction reports as a symlink and EvalSymlinks resolves it to its target, so
// GODEBUG is left untouched -- matching the BUG-205 junction Go tests, which do
// not save/clear/restore it (an explicit GODEBUG=winsymlink=1 is unsupported for
// junction safety).

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBUG213_LinkedVscodeWithExistingFilesRefused_WindowsJunction(t *testing.T) {
	skipIfShort(t)

	parent := t.TempDir()
	root := filepath.Join(parent, "project")
	outside := filepath.Join(parent, "outside")
	require.NoError(t, os.MkdirAll(root, 0o755))
	require.NoError(t, os.MkdirAll(outside, 0o755))

	const sContent = "OUTSIDE-SETTINGS-MARKER\n"
	const eContent = "OUTSIDE-EXTENSIONS-MARKER\n"
	require.NoError(t, os.WriteFile(filepath.Join(outside, "settings.json"), []byte(sContent), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "extensions.json"), []byte(eContent), 0o644))

	// .vscode is a directory junction to the outside folder. Creation must
	// succeed; a failure fails the test rather than skipping it.
	link := filepath.Join(root, ".vscode")
	if out, mkErr := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", link, outside).CombinedOutput(); mkErr != nil {
		t.Fatalf("mklink /J %q %q must succeed (must not skip): %v\n%s", link, outside, mkErr, out)
	}
	// Remove the junction as a link only (never recurse through it) before the
	// recursive t.TempDir cleanup, so a recursive delete can never follow it into
	// the target. Registered after t.TempDir, so it runs first (LIFO).
	t.Cleanup(func() { _ = exec.Command("cmd.exe", "/d", "/c", "rmdir", link).Run() })

	const mergeMarker = "MERGE-HINT-SHOULD-NOT-APPEAR"
	cases := []struct {
		existingRel, companionRel, outsideFile, outsideContent string
	}{
		{".vscode/settings.json", ".vscode/gtms-settings.json.snippet", "settings.json", sContent},
		{".vscode/extensions.json", ".vscode/gtms-extensions.json.snippet", "extensions.json", eContent},
	}
	for _, c := range cases {
		result := &Result{}
		require.NoError(t, initVscodeFile(root, c.existingRel, c.companionRel, []byte("{}"), mergeMarker, result))

		require.Len(t, result.Warnings, 1, "exactly one containment warning for %s", c.existingRel)
		warn := result.Warnings[0]
		assert.Contains(t, warn, c.existingRel, "the warning must name the refused original")
		assert.Contains(t, warn, "resolves outside the project")
		assert.NotContains(t, warn, mergeMarker, "no merge hint for a refused original")

		assert.Contains(t, result.FilesRefused, c.existingRel, "the original must be reported refused")
		assert.Contains(t, result.FilesSkipped, c.existingRel)
		assert.NotContains(t, result.FilesCreated, c.existingRel, "a refused original must never be created")
		assert.NotContains(t, result.FilesCreated, c.companionRel, "no companion for a refused original")

		// No companion written in the project or in the outside folder.
		_, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(c.companionRel)))
		assert.True(t, os.IsNotExist(statErr), "no companion may be written in the project")
		_, statErr = os.Lstat(filepath.Join(outside, filepath.Base(c.companionRel)))
		assert.True(t, os.IsNotExist(statErr), "no companion may be written into the outside folder")

		after, rerr := os.ReadFile(filepath.Join(outside, c.outsideFile))
		require.NoError(t, rerr)
		assert.Equal(t, c.outsideContent, string(after), "the outside file must be byte-for-byte unchanged")
	}

	// Junction cleanup uses cmd.exe /d /c rmdir, and the target survives it.
	if out, rmErr := exec.Command("cmd.exe", "/d", "/c", "rmdir", link).CombinedOutput(); rmErr != nil {
		t.Fatalf("rmdir on the junction must succeed: %v\n%s", rmErr, out)
	}
	_, statErr := os.Stat(outside)
	require.NoError(t, statErr, "the junction target folder must survive rmdir")
	for _, f := range []string{"settings.json", "extensions.json"} {
		_, statErr := os.Stat(filepath.Join(outside, f))
		require.NoError(t, statErr, "the outside file %s must survive junction removal", f)
	}
}
