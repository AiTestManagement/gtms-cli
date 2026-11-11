package scaffold

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBUG203_R4_RenamedParentForceRefreshesRenamedFolder is the BUG-207 inversion of the
// former TestBUG203_R4_RenamedParentForceDoesNotClaimUpgrade (named owner approval,
// Owner Decision 1/5/7): gtms init --force in a renamed-parent project now refreshes the
// renamed folder's stale files in place, creates no canonical gtms/ tree, names the
// renamed destination in every upgrade note, and emits no user-facing BUG-207 string.
func TestBUG203_R4_RenamedParentForceRefreshesRenamedFolder(t *testing.T) {
	skipIfShort(t)
	dir := initGitRepo(t)
	const tmplRel = "test/templates/manual-testcase.template.md"
	prior := bug203DifferingPriorOf(t, "gtms/"+tmplRel, TestcaseTemplateMD)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "gtms.config"), []byte("project:\n  name: r\n  repo: org/r\n"), 0o644))
	bug203Write(t, dir, "testing/.gtms-root", "", 0o644)
	bug203Write(t, dir, "testing/"+tmplRel, prior, 0o644)

	result, err := Init(Options{ProjectRoot: dir, Name: "r", Repo: "org/r", Preset: PresetManual, Force: true})
	require.NoError(t, err)

	warnings := strings.Join(result.Warnings, "\n")
	assert.NotContains(t, warnings, "BUG-207", "no user-facing warning may name the internal record id (ENH-214)")
	assert.NotContains(t, warnings, "Automatic refresh does not support renamed GTMS folders",
		"the retired limitation warning must be gone")

	// The renamed folder's stale template is refreshed in place to the current shipped version.
	got := bug203Read(t, dir, "testing/"+tmplRel)
	assert.NotEqual(t, prior, got, "the renamed folder's file is refreshed by init (BUG-207)")
	assert.Contains(t, got, `gtms_schema: "test-case/v2"`, "the refreshed template carries the current v2 header")

	// No canonical gtms/ tree or second sentinel is created.
	_, statErr := os.Stat(filepath.Join(dir, "gtms"))
	assert.True(t, os.IsNotExist(statErr), "no default-layout gtms/ tree may appear")

	// Every upgrade note names the renamed destination, never the canonical gtms/ path.
	notes := strings.Join(result.Notes, "\n")
	assert.Contains(t, notes, "Upgraded testing/"+tmplRel, "the upgrade note names the renamed destination")
	assert.NotContains(t, notes, "Upgraded gtms/"+tmplRel, "no upgrade note may name the canonical gtms/ path")
}

// TestBUG203_R4_ForceDoesNotWriteThroughSymlinkedGitignore covers REV-209 R4-CODEX-003
// (Unix only): a symlinked .gitignore in a sentinel-managed project is never rewritten by
// the config-backup ignore step; init warns and skips instead.
func TestBUG203_R4_ForceDoesNotWriteThroughSymlinkedGitignore(t *testing.T) {
	skipIfShort(t)
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlink test (Windows symlink creation needs privilege)")
	}
	dir := initGitRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gtms.config"), []byte("project:\n  name: x\n"), 0o644))

	// An external .gitignore target that already carries the GTMS sentinel (so the earlier
	// ensureGitignore step leaves it alone) but not the backup pattern.
	external := filepath.Join(t.TempDir(), "shared.gitignore")
	const targetContent = ".gtms/\ngtms/execution/attachments/\ngtms/execution/logs/\n"
	require.NoError(t, os.WriteFile(external, []byte(targetContent), 0o644))
	gitignore := filepath.Join(dir, ".gitignore")
	require.NoError(t, os.Symlink(external, gitignore))

	result, err := Init(Options{ProjectRoot: dir, Name: "x", Repo: "org/x", Preset: PresetManual, Force: true})
	require.NoError(t, err)

	after, rerr := os.ReadFile(external)
	require.NoError(t, rerr)
	assert.Equal(t, targetContent, string(after), "the symlink target outside the project must be unmodified")
	fi, lerr := os.Lstat(gitignore)
	require.NoError(t, lerr)
	assert.NotZero(t, fi.Mode()&os.ModeSymlink, ".gitignore must still be the symlink")
	// BUG-208 D3: the warning now comes from the earlier ensureGitignore step with the
	// stable token, and the retired `.gitignore is not a regular file` wording is gone.
	// The /gtms.config.bak* entry is still listed (it is in gitignoreEntries).
	assert.Contains(t, strings.Join(result.Warnings, "\n"), "skipped updating .gitignore",
		"the operator must be warned that the ignore update was skipped")
	assert.Contains(t, strings.Join(result.Warnings, "\n"), configBakIgnorePattern)
}
