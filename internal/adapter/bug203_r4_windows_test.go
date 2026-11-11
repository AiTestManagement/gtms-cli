//go:build windows

package adapter

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shortPathOf returns the Windows 8.3 short spelling of an existing path, or "" when the
// volume does not generate short names.
func shortPathOf(t *testing.T, long string) string {
	t.Helper()
	p, err := syscall.UTF16PtrFromString(long)
	require.NoError(t, err)
	buf := make([]uint16, 1024)
	n, err := syscall.GetShortPathName(p, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || int(n) > len(buf) {
		return ""
	}
	short := syscall.UTF16ToString(buf[:n])
	if strings.EqualFold(short, long) {
		return ""
	}
	return short
}

// shortRootProject builds a stale-template project under a long-named directory and
// returns the project root spelled with its 8.3 short name; it skips when the platform
// cannot produce one.
func shortRootProject(t *testing.T) string {
	t.Helper()
	long := filepath.Join(t.TempDir(), "bug203-long-project-directory-name")
	require.NoError(t, os.MkdirAll(long, 0o755))
	short := shortPathOf(t, long)
	if short == "" || !strings.Contains(short, "~") {
		t.Skip("this volume does not generate 8.3 short names, so a short-path project root cannot be produced")
	}
	writeStaleBuiltinProject(t, short)
	return short
}

// TestBUG203_R4_WindowsShortPathRoot_ReportsLayoutRelativePath (Windows only): the project
// root is spelled with an 8.3 short name; pathsafe resolves it to the long form, so the
// quarantine must still report gtms/tasks/error/<task>/artifacts/<file> and the built-in
// template must still be recognised as a shipped producer (REV-209 R4-CLAUDE-001/R4-CODEX-002).
func TestBUG203_R4_WindowsShortPathRoot_ReportsLayoutRelativePath(t *testing.T) {
	skipIfShort(t)
	root := shortRootProject(t)

	res := invokeBuiltinManualCreate(t, root, "r4short")
	assertLayoutRelativeQuarantine(t, res)
}

// TestBUG203_R4_WindowsShortPathRoot_ProducerRecognition (Windows only): the template is
// spelled with the long form while the root is the short form (and vice versa).
func TestBUG203_R4_WindowsShortPathRoot_ProducerRecognition(t *testing.T) {
	root := shortRootProject(t)
	longRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	require.NotEqual(t, root, longRoot, "EvalSymlinks must expand the short name")

	resolved := &ResolvedAdapter{Command: "create", Name: "manual-create", Tier: 0}
	for name, pair := range map[string][2]string{
		"short root, long template": {root, filepath.Join(longRoot, filepath.FromSlash(staleTemplateRel))},
		"long root, short template": {longRoot, filepath.Join(root, filepath.FromSlash(staleTemplateRel))},
	} {
		t.Run(name, func(t *testing.T) {
			hint := staleScaffoldProducerHint(resolved, &AdapterContext{TemplateFile: pair[1]}, pair[0], staleViolation())
			assert.Contains(t, hint, "produced by "+staleTemplateRel+".")
			assert.Contains(t, hint, "gtms init --force --preset")
		})
	}
}
