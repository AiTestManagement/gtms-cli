package scaffold

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BUG-208: gtms init must classify the .gitignore leaf without following links
// before reading or writing it. These tests cover the file-state contract rows
// that BATS cannot (hard links, inspection errors) or must not (FIFO, which
// would hang a pre-fix run), plus the per-preset matrix (AC7) that BATS does not
// repeat. The user-facing symlink/dangling/directory/--force/--demo behaviour is
// additionally covered by the canonical specs in
// gtms/test/cases/init-gitignore-linked-special-files/.

// gitInitAt runs `git init` in an existing directory (unlike initGitRepo, which
// makes its own t.TempDir()). BUG-208 AC10 needs the project and the external
// file as siblings under one parent so os.Link stays on one filesystem.
func gitInitAt(t *testing.T, dir string) {
	t.Helper()
	out, err := exec.Command("git", "init", dir).CombinedOutput()
	require.NoError(t, err, "git init failed: %s", string(out))
	_ = exec.Command("git", "-C", dir, "config", "user.email", "test@test.com").Run()
	_ = exec.Command("git", "-C", dir, "config", "user.name", "Test").Run()
}

// assertListsAllEntries asserts the warning text lists every GTMS entry (D2).
func assertListsAllEntries(t *testing.T, warnings string) {
	t.Helper()
	for _, entry := range gitignoreEntries {
		assert.Contains(t, warnings, entry, "the warning must list every GTMS entry to add by hand")
	}
}

// TestBUG208_FileStateTable drives ensureGitignore directly across the rows of
// the file-state contract that do not need git or a subprocess. FIFO, hard-link
// and inspection-error rows are separate tests (see below); the symlink rows
// skip on Windows, as bug203_r4_test.go does.
func TestBUG208_FileStateTable(t *testing.T) {
	const external = "node_modules/\n"

	cases := []struct {
		name        string
		skipWindows bool
		// setup prepares .gitignore in dir and returns the external target path
		// to check afterwards ("" when there is none).
		setup      func(t *testing.T, dir string) string
		wantAction GitignoreAction
		wantWarn   bool
		wantState  string
		// after runs post-assertions; extTarget is setup's return.
		after func(t *testing.T, dir, extTarget string)
	}{
		{
			name:       "absent creates regular file with all entries",
			setup:      func(t *testing.T, dir string) string { return "" },
			wantAction: GitignoreCreated,
			wantWarn:   false,
			after: func(t *testing.T, dir, _ string) {
				data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
				require.NoError(t, err)
				assert.Equal(t, ".gtms/\ngtms/execution/attachments/\ngtms/execution/logs/\n/gtms.config.bak*\n", string(data))
			},
		},
		{
			name: "regular single-link without sentinel appends",
			setup: func(t *testing.T, dir string) string {
				require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("node_modules/\n"), 0o644))
				return ""
			},
			wantAction: GitignoreAppended,
			wantWarn:   false,
			after: func(t *testing.T, dir, _ string) {
				data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
				require.NoError(t, err)
				assert.Equal(t, "node_modules/\n.gtms/\ngtms/execution/attachments/\ngtms/execution/logs/\n/gtms.config.bak*\n", string(data))
			},
		},
		{
			name: "regular single-link with sentinel is unchanged",
			setup: func(t *testing.T, dir string) string {
				require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("node_modules/\n.gtms/\n"), 0o644))
				return ""
			},
			wantAction: GitignoreUnchanged,
			wantWarn:   false,
			after: func(t *testing.T, dir, _ string) {
				data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
				require.NoError(t, err)
				assert.Equal(t, "node_modules/\n.gtms/\n", string(data))
			},
		},
		{
			name:        "valid symlink to external file is never written through",
			skipWindows: true,
			setup: func(t *testing.T, dir string) string {
				target := filepath.Join(t.TempDir(), "shared.gitignore")
				require.NoError(t, os.WriteFile(target, []byte(external), 0o644))
				require.NoError(t, os.Symlink(target, filepath.Join(dir, ".gitignore")))
				return target
			},
			wantAction: GitignoreUnchanged,
			wantWarn:   true,
			wantState:  "symbolic link",
			after: func(t *testing.T, dir, extTarget string) {
				data, err := os.ReadFile(extTarget)
				require.NoError(t, err)
				assert.Equal(t, external, string(data), "external target must be unchanged")
				fi, err := os.Lstat(filepath.Join(dir, ".gitignore"))
				require.NoError(t, err)
				assert.NotZero(t, fi.Mode()&os.ModeSymlink, ".gitignore must still be the symlink")
			},
		},
		{
			name:        "dangling symlink does not create the target",
			skipWindows: true,
			setup: func(t *testing.T, dir string) string {
				target := filepath.Join(t.TempDir(), "missing.gitignore")
				require.NoError(t, os.Symlink(target, filepath.Join(dir, ".gitignore")))
				return target
			},
			wantAction: GitignoreUnchanged,
			wantWarn:   true,
			wantState:  "symbolic link",
			after: func(t *testing.T, dir, extTarget string) {
				_, err := os.Lstat(extTarget)
				assert.True(t, os.IsNotExist(err), "the dangling link target must not be created")
				fi, err := os.Lstat(filepath.Join(dir, ".gitignore"))
				require.NoError(t, err)
				assert.NotZero(t, fi.Mode()&os.ModeSymlink, ".gitignore must still be the symlink")
			},
		},
		{
			name: "directory is not read or written",
			setup: func(t *testing.T, dir string) string {
				require.NoError(t, os.Mkdir(filepath.Join(dir, ".gitignore"), 0o755))
				return ""
			},
			wantAction: GitignoreUnchanged,
			wantWarn:   true,
			wantState:  "directory",
			after: func(t *testing.T, dir, _ string) {
				fi, err := os.Lstat(filepath.Join(dir, ".gitignore"))
				require.NoError(t, err)
				assert.True(t, fi.IsDir(), ".gitignore must still be a directory")
				entries, err := os.ReadDir(filepath.Join(dir, ".gitignore"))
				require.NoError(t, err)
				assert.Empty(t, entries, "nothing must be written inside the directory")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.skipWindows && runtime.GOOS == "windows" {
				t.Skip("POSIX symlink test (Windows symlink creation needs privilege)")
			}
			dir := t.TempDir()
			extTarget := tc.setup(t, dir)

			result := &Result{}
			action, err := ensureGitignore(dir, result)
			require.NoError(t, err)
			assert.Equal(t, tc.wantAction, action)

			warnings := strings.Join(result.Warnings, "\n")
			if tc.wantWarn {
				assert.Equal(t, 1, strings.Count(warnings, "skipped updating .gitignore"),
					"exactly one consolidated warning with the stable token")
				assert.Contains(t, warnings, tc.wantState, "the warning must name the state")
				assertListsAllEntries(t, warnings)
				// None of the forbidden/retired forms may appear.
				for _, forbidden := range []string{
					"Created .gitignore", "Updated .gitignore (added .gtms/)",
					".gitignore is not a regular file", "skipped adding", "updating .gitignore: read",
				} {
					assert.NotContains(t, warnings, forbidden)
				}
			} else {
				assert.NotContains(t, warnings, "skipped updating .gitignore")
			}

			if tc.after != nil {
				tc.after(t, dir, extTarget)
			}
		})
	}
}

// TestBUG208_InspectionErrorLstatIsReturned covers AC11: an Lstat failure other
// than "not exist" is returned as an error, not treated as an absent file. It
// is deterministic via a hook, not file permissions (which CI may run elevated
// enough to defeat, per BUG-208 Verification).
func TestBUG208_InspectionErrorLstatIsReturned(t *testing.T) {
	dir := t.TempDir()
	sentinel := errors.New("synthetic lstat inspection failure")
	orig := gitignoreLstat
	gitignoreLstat = func(string) (os.FileInfo, error) { return nil, sentinel }
	defer func() { gitignoreLstat = orig }()

	result := &Result{}
	action, err := ensureGitignore(dir, result)
	require.Error(t, err, "an Lstat failure must be returned, not swallowed")
	assert.ErrorIs(t, err, sentinel)
	assert.Empty(t, action)
	assert.Empty(t, result.Warnings, "an inspection error must not warn")

	_, statErr := os.Lstat(filepath.Join(dir, ".gitignore"))
	assert.True(t, os.IsNotExist(statErr), "no .gitignore may be created when inspection fails")
}

// TestBUG208_InspectionErrorLinkCountIsReturned covers D6/AC10: a regular file
// whose hard-link count cannot be read fails closed with an inspection error and
// no .gitignore change. Deterministic via the link-count hook.
func TestBUG208_InspectionErrorLinkCountIsReturned(t *testing.T) {
	dir := t.TempDir()
	const before = "node_modules/\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(before), 0o644))

	sentinel := errors.New("synthetic link-count inspection failure")
	orig := gitignoreLinkCount
	gitignoreLinkCount = func(string, os.FileInfo) (uint64, error) { return 0, sentinel }
	defer func() { gitignoreLinkCount = orig }()

	result := &Result{}
	action, err := ensureGitignore(dir, result)
	require.Error(t, err, "an unreadable link count must fail closed")
	assert.ErrorIs(t, err, sentinel)
	assert.Empty(t, action)
	assert.Empty(t, result.Warnings)

	after, rerr := os.ReadFile(filepath.Join(dir, ".gitignore"))
	require.NoError(t, rerr)
	assert.Equal(t, before, string(after), ".gitignore must be byte-for-byte unchanged on inspection error")
}

// TestBUG208_HardLinkedGitignoreRefused covers AC10/D6 on BOTH Linux and Windows
// (os.Link needs no special Windows rights): a .gitignore hard-linked to a file
// outside the project, on the same filesystem, is left byte-for-byte unchanged
// and still the same file (link count still 2); init exits 0, prints the D3
// warning once and reports GitignoreUnchanged.
func TestBUG208_HardLinkedGitignoreRefused(t *testing.T) {
	skipIfShort(t)
	parent := t.TempDir()
	project := filepath.Join(parent, "project")
	outside := filepath.Join(parent, "outside")
	require.NoError(t, os.MkdirAll(project, 0o755))
	require.NoError(t, os.MkdirAll(outside, 0o755))
	gitInitAt(t, project)
	require.NoError(t, os.WriteFile(filepath.Join(project, "gtms.config"), []byte("project:\n  name: h\n"), 0o644))

	shared := filepath.Join(outside, "shared.gitignore")
	const sharedContent = "node_modules/\n"
	require.NoError(t, os.WriteFile(shared, []byte(sharedContent), 0o644))
	gitignore := filepath.Join(project, ".gitignore")
	require.NoError(t, os.Link(shared, gitignore), "same-filesystem hard link must succeed")

	// --force so both ensureGitignore and ensureGitignoreHasConfigBak run; neither
	// may write through the hard link or add a second warning.
	result, err := Init(Options{ProjectRoot: project, Name: "h", Repo: "org/h", Preset: PresetManual, Force: true})
	require.NoError(t, err, "init must exit 0 on a hard-linked .gitignore")

	after, rerr := os.ReadFile(shared)
	require.NoError(t, rerr)
	assert.Equal(t, sharedContent, string(after), "the hard-linked shared file must be byte-for-byte unchanged")

	info, lerr := os.Lstat(gitignore)
	require.NoError(t, lerr)
	n, cerr := hardLinkCount(gitignore, info)
	require.NoError(t, cerr)
	assert.Equal(t, uint64(2), n, "the file must still be hard-linked (identity preserved)")

	assert.Equal(t, GitignoreUnchanged, result.GitignoreAction)
	warnings := strings.Join(result.Warnings, "\n")
	assert.Equal(t, 1, strings.Count(warnings, "skipped updating .gitignore"), "exactly one .gitignore warning per run (D2)")
	assert.Contains(t, warnings, "hard link", "the warning must name the hard-link state")
	assertListsAllEntries(t, warnings)
	for _, forbidden := range []string{".gitignore is not a regular file", "skipped adding", "updating .gitignore: read"} {
		assert.NotContains(t, warnings, forbidden)
	}
}

// TestBUG208_ExternalSymlinkPerPreset covers AC7's per-preset matrix: one unsafe
// entry (an external symlink) through each preset -- manual, bats, playwright --
// with identical results. Skips on Windows, as the symlink cases do.
func TestBUG208_ExternalSymlinkPerPreset(t *testing.T) {
	skipIfShort(t)
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlink test (Windows symlink creation needs privilege)")
	}
	const sharedContent = "node_modules/\n"
	for _, preset := range []string{PresetManual, PresetBats, PresetPlaywright} {
		t.Run(preset, func(t *testing.T) {
			parent := t.TempDir()
			project := filepath.Join(parent, "project")
			outside := filepath.Join(parent, "outside")
			require.NoError(t, os.MkdirAll(project, 0o755))
			require.NoError(t, os.MkdirAll(outside, 0o755))
			gitInitAt(t, project)

			shared := filepath.Join(outside, "shared.gitignore")
			require.NoError(t, os.WriteFile(shared, []byte(sharedContent), 0o644))
			require.NoError(t, os.Symlink(shared, filepath.Join(project, ".gitignore")))

			result, err := Init(Options{ProjectRoot: project, Name: "p", Repo: "org/p", Preset: preset})
			require.NoError(t, err)

			after, rerr := os.ReadFile(shared)
			require.NoError(t, rerr)
			assert.Equal(t, sharedContent, string(after), "external target unchanged for preset %s", preset)

			fi, lerr := os.Lstat(filepath.Join(project, ".gitignore"))
			require.NoError(t, lerr)
			assert.NotZero(t, fi.Mode()&os.ModeSymlink, ".gitignore must still be the symlink")

			assert.Equal(t, GitignoreUnchanged, result.GitignoreAction)
			warnings := strings.Join(result.Warnings, "\n")
			assert.Equal(t, 1, strings.Count(warnings, "skipped updating .gitignore"))
			assert.Contains(t, warnings, "symbolic link")
			assertListsAllEntries(t, warnings)
		})
	}
}
