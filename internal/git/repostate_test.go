package git

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ENH-189 requires the unrunnable-git and unreadable-.git states to be
// exercised through a deterministic seam rather than by chmod'ing a binary or
// damaging a real git on disk: permission and executable-bit semantics differ
// enough between Unix and Windows that the direct approach yields a test that
// passes on one platform and means nothing on the other.
//
// stubGit is that seam for the git CLI.
type stubGit struct {
	// calls records "dir :: args" for every invocation, so tests can assert
	// how many git processes the classifier would have spawned.
	calls []string

	// notInstalled makes every call behave as if git is absent from PATH.
	notInstalled bool

	// versionFails makes `git --version` fail while leaving lookup intact.
	// This is the BUG-155 shape: the binary resolves and then cannot run.
	versionFails bool

	// revParseOK lists the directories where `rev-parse --git-dir` succeeds.
	revParseOK map[string]bool

	// cancelOnVersion, when set, is invoked from inside the `git --version`
	// probe and then makes it fail. That reproduces an interrupt arriving
	// while the probe is in flight, which no already-cancelled context can
	// simulate.
	cancelOnVersion func()
}

func (s *stubGit) run(ctx context.Context, dir string, args ...string) (string, error) {
	s.calls = append(s.calls, dir+" :: "+strings.Join(args, " "))

	if s.notInstalled {
		return "", ErrNotInstalled
	}
	// exec.CommandContext fails at Start when the context is ALREADY cancelled,
	// whatever the command would otherwise have done, and it does so after the
	// PATH lookup -- hence this test sits below notInstalled. Reproducing that
	// order matters: the whole question in REV-136 round 3 CLAUDE-002 is what
	// the classifier concludes from a git call that failed because the user
	// pressed Ctrl-C.
	if ctx != nil && ctx.Err() != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), ctx.Err())
	}
	if len(args) == 1 && args[0] == "--version" {
		if s.cancelOnVersion != nil {
			s.cancelOnVersion()
			return "", fmt.Errorf("git --version: %w", context.Canceled)
		}
		if s.versionFails {
			return "", fmt.Errorf("git --version: exit status 126")
		}
		return "git version 2.99.0", nil
	}
	if s.revParseOK[dir] {
		return ".git", nil
	}
	// Deliberately mirrors git's real wording. The classifier must never key
	// off this text: git says "not a git repository" for the dangling-pointer
	// state, which is precisely the state that IS a repository.
	return "", fmt.Errorf("git %s: fatal: not a git repository: %s", strings.Join(args, " "), dir)
}

// stubEntry describes one path as os.Stat would report it.
type stubEntry struct {
	err   error // non-nil: stat fails with this (wrapped in a PathError)
	isDir bool

	// dangling marks a symlink whose target is missing. os.Stat follows the
	// link and reports "does not exist"; os.Lstat sees the link itself and
	// succeeds. The stub reproduces exactly that split.
	dangling bool
}

// stubFS maps a path to how it stats. Any path absent from the map does not
// exist.
type stubFS map[string]stubEntry

func (f stubFS) stat(name string) (os.FileInfo, error) {
	e, ok := f[name]
	if !ok || e.dangling {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
	}
	if e.err != nil {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: e.err}
	}
	return stubInfo{name: filepath.Base(name), dir: e.isDir}, nil
}

// lstat is the no-follow twin of stat. It differs from stat on exactly one
// kind of entry: a dangling symlink, which exists as a link even though its
// target does not.
func (f stubFS) lstat(name string) (os.FileInfo, error) {
	e, ok := f[name]
	if !ok {
		return nil, &fs.PathError{Op: "lstat", Path: name, Err: fs.ErrNotExist}
	}
	if e.dangling {
		return stubInfo{name: filepath.Base(name), link: true}, nil
	}
	if e.err != nil {
		return nil, &fs.PathError{Op: "lstat", Path: name, Err: e.err}
	}
	return stubInfo{name: filepath.Base(name), dir: e.isDir}, nil
}

// stubInfo is the minimum os.FileInfo the classifier reads: it only asks
// whether the entry is a directory.
type stubInfo struct {
	name string
	dir  bool
	link bool
}

func (i stubInfo) Name() string { return i.name }
func (i stubInfo) Size() int64  { return 0 }
func (i stubInfo) Mode() fs.FileMode {
	if i.dir {
		return fs.ModeDir | 0o755
	}
	if i.link {
		return fs.ModeSymlink | 0o777
	}
	return 0o644
}
func (i stubInfo) ModTime() time.Time { return time.Time{} }
func (i stubInfo) IsDir() bool        { return i.dir }
func (i stubInfo) Sys() any           { return nil }

// gitFile is a .git pointer file (a worktree or submodule reference).
func gitFile() stubEntry { return stubEntry{} }

// gitDir is a real .git directory: it needs a HEAD to count as a repository,
// which callers register alongside it.
func gitDir() stubEntry { return stubEntry{isDir: true} }

// danglingLink is a symlink named .git whose target has been moved or deleted.
func danglingLink() stubEntry { return stubEntry{dangling: true} }

func stubEnv(pairs map[string]string) func(string) string {
	return func(key string) string { return pairs[key] }
}

// TestCheckRepo_Classification is the ENH-189 AC 9 table: git-not-found,
// git-not-executable, valid repo, not-a-repo, and the third state in BOTH its
// forms (a .git pointer git cannot resolve, and a .git that cannot be read).
func TestCheckRepo_Classification(t *testing.T) {
	// A real absolute path keeps the ancestor walk platform-correct without
	// depending on any real repository: every stat is stubbed.
	base := t.TempDir()
	leaf := filepath.Join(base, "project")
	dotGit := filepath.Join(leaf, ".git")
	parentDotGit := filepath.Join(base, ".git")
	parentHead := filepath.Join(parentDotGit, "HEAD")

	tests := []struct {
		name  string
		git   *stubGit
		files stubFS
		env   map[string]string
		want  RepoState
		why   string
	}{
		{
			name:  "git absent from PATH",
			git:   &stubGit{notInstalled: true},
			files: stubFS{},
			want:  GitUnusable,
			why:   "a user with no git must not be told to run 'git init'",
		},
		{
			name:  "git present but cannot execute",
			git:   &stubGit{versionFails: true},
			files: stubFS{dotGit: gitFile()},
			want:  GitUnusable,
			why:   "the BUG-155 shape: lookup succeeds, the process cannot run",
		},
		{
			name:  "valid repository",
			git:   &stubGit{revParseOK: map[string]bool{leaf: true}},
			files: stubFS{dotGit: gitDir(), filepath.Join(dotGit, "HEAD"): gitFile()},
			want:  RepoOK,
			why:   "git resolved a git dir, so the command proceeds",
		},
		{
			name:  "genuinely not a repository",
			git:   &stubGit{},
			files: stubFS{},
			want:  RepoNotFound,
			why:   "git works, nothing on disk -- the only state 'git init' fixes",
		},
		{
			name:  "third state: .git pointer git cannot resolve",
			git:   &stubGit{},
			files: stubFS{dotGit: gitFile()},
			want:  RepoUnusable,
			why:   "a dangling worktree pointer: metadata present, git refuses it",
		},
		{
			name:  "third state: malformed .git pointer file",
			git:   &stubGit{},
			files: stubFS{dotGit: gitFile()},
			want:  RepoUnusable,
			why: "a .git file counts as metadata whatever it contains -- requiring it to " +
				"parse would route the malformed case straight back to 'git init'",
		},
		{
			name:  "third state: .git cannot be read",
			git:   &stubGit{},
			files: stubFS{dotGit: {err: fs.ErrPermission}},
			want:  RepoUnusable,
			why:   "an unreadable .git is metadata git cannot use, not an absence",
		},
		{
			name:  "third state: real .git directory git refuses",
			git:   &stubGit{},
			files: stubFS{dotGit: gitDir(), filepath.Join(dotGit, "HEAD"): gitFile()},
			want:  RepoUnusable,
			why:   "a populated .git directory git will not open (e.g. dubious ownership)",
		},
		{
			name:  "stray empty .git directory is not a repository",
			git:   &stubGit{},
			files: stubFS{parentDotGit: gitDir()},
			want:  RepoNotFound,
			why: "an empty directory that merely happens to be named .git (a stray /tmp/.git " +
				"is easy to create) must not make every directory beneath it 'unusable'",
		},
		// REV-133 round 1 test 7 (paired guard for CODEX-002). GIT_DIR keeps its
		// short-circuit: dropping it along with GIT_WORK_TREE would swing the
		// fix too far and send an explicitly named repository to 'git init'.
		{
			name:  "GIT_DIR alone declares a repository git then fails on",
			git:   &stubGit{},
			files: stubFS{},
			env:   map[string]string{"GIT_DIR": "/somewhere/else/.git"},
			want:  RepoUnusable,
			why:   "the user named a repository explicitly; 'git init' is wrong",
		},
		// REV-133 round 1 test 6 (CODEX-002). Note the direction: unlike the other
		// four findings this one does not produce unsafe 'git init' advice, it
		// SUPPRESSES correct advice. Git does not treat a standalone
		// GIT_WORK_TREE as declaring a repository, so before the fix a genuine
		// non-repository that merely had GIT_WORK_TREE exported was told "the
		// repository here cannot be used" and lost its valid 'git init' remedy.
		{
			name:  "GIT_WORK_TREE alone does not declare a repository",
			git:   &stubGit{},
			files: stubFS{},
			env:   map[string]string{"GIT_WORK_TREE": "/somewhere/else"},
			want:  RepoNotFound,
			why: "git does not resolve a repository from a work tree alone, so this is a " +
				"genuine non-repository and 'git init' is the right advice",
		},
		// REV-133 round 1 test 1 (CODEX-001). Permission errors must fail closed as
		// RepoUnusable, never RepoNotFound.
		{
			name: "third state: .git directory whose HEAD cannot be read",
			git:  &stubGit{},
			files: stubFS{
				dotGit:                        gitDir(),
				filepath.Join(dotGit, "HEAD"): {err: fs.ErrPermission},
			},
			want: RepoUnusable,
			why: "an unreadable HEAD means the repository exists and cannot be read; " +
				"'git init' inside an inaccessible repository is the harmful answer",
		},
		// REV-133 round 1 test 2 (paired guard for CODEX-001), widened by
		// REV-135 round 2 CODEX-001. GUARD: an EMPTY .git directory -- no HEAD and none of the
		// canonical markers -- must still answer RepoNotFound. This is the rule
		// that stops a stray /tmp/.git from making every non-repository beneath
		// it report "the repository here cannot be used".
		{
			name:  "stray empty .git directory with no HEAD and no markers stays not-a-repository",
			git:   &stubGit{},
			files: stubFS{dotGit: gitDir()},
			want:  RepoNotFound,
			why: "an empty directory named .git has neither HEAD nor objects/refs/config, so it " +
				"is not plausibly a repository; treating it as metadata would swing the fix too far",
		},
		// REV-135 round 2 test 1 (CODEX-001). A repository whose HEAD was deleted still
		// has its object store, refs and config, and 'git init' inside it is the
		// harmful advice this whole classification exists to prevent.
		{
			name: "third state: .git directory with HEAD deleted but objects/refs/config present",
			git:  &stubGit{},
			files: stubFS{
				dotGit:                           gitDir(),
				filepath.Join(dotGit, "objects"): gitDir(),
				filepath.Join(dotGit, "refs"):    gitDir(),
				filepath.Join(dotGit, "config"):  gitFile(),
			},
			want: RepoUnusable,
			why: "a damaged repository is still a repository: any canonical marker means there " +
				"is something here to repair, not somewhere to run 'git init'",
		},
		// REV-136 round 3 test 1 (CODEX-001). The marker list has to name every
		// stable entry of git's own layout, not a memorable subset of it. A
		// repository that has lost HEAD, objects, refs AND config still has an
		// index, and it is still a repository to repair rather than a place to
		// run 'git init'.
		{
			name: "third state: .git directory whose only surviving marker is index",
			git:  &stubGit{},
			files: stubFS{
				dotGit:                         gitDir(),
				filepath.Join(dotGit, "index"): gitFile(),
			},
			want: RepoUnusable,
			why: "index is a canonical .git entry, so a .git holding one is damaged metadata " +
				"and 'git init' inside it is the harmful answer",
		},
		// REV-136 round 3 test 2 (CODEX-001). Same rule, the other named entry.
		// packed-refs is where a gc'd repository keeps its refs, so a repository
		// can legitimately have packed-refs when loose refs/ is gone.
		{
			name: "third state: .git directory whose only surviving marker is packed-refs",
			git:  &stubGit{},
			files: stubFS{
				dotGit:                               gitDir(),
				filepath.Join(dotGit, "packed-refs"): gitFile(),
			},
			want: RepoUnusable,
			why: "packed-refs is a canonical .git entry; a packed repository that lost its " +
				"other entries is damaged, not absent",
		},
		// REV-136 round 3 (paired guard for CODEX-001), green by design. GUARD:
		// widening the marker list must not sweep in entries that non-repository
		// scaffolding creates. A bare .git/hooks is exactly that -- the
		// artefact-discovery-stale-hash acceptance case tc-d4317da0 builds one on
		// purpose -- and it must stay outside the list.
		{
			name: "a .git directory holding only hooks is not a repository",
			git:  &stubGit{},
			files: stubFS{
				dotGit:                         gitDir(),
				filepath.Join(dotGit, "hooks"): gitDir(),
			},
			want: RepoNotFound,
			why: "hooks is optional and is what a bare 'mkdir -p .git/hooks' fixture creates, so " +
				"it cannot be the evidence that a repository is present",
		},
		// REV-135 round 2 test 2 (CODEX-001). The same asymmetry round 1 fixed for .git
		// itself: c.stat follows the link, so a HEAD symlink whose target is gone
		// reads as absent even though the entry is plainly there.
		{
			name: "third state: .git directory whose HEAD is a dangling symlink",
			git:  &stubGit{},
			files: stubFS{
				dotGit:                        gitDir(),
				filepath.Join(dotGit, "HEAD"): danglingLink(),
			},
			want: RepoUnusable,
			why: "HEAD must count as present when it exists as a directory ENTRY; following the " +
				"link to a missing target reports an absence that is not there",
		},
		// REV-133 round 1 test 3 (CODEX-003 / CLAUDE-001). Git always examines the
		// starting directory; a ceiling only truncates the ascent.
		{
			name:  "metadata in a starting directory that is itself a ceiling is still found",
			git:   &stubGit{},
			files: stubFS{dotGit: gitFile()},
			env:   map[string]string{"GIT_CEILING_DIRECTORIES": leaf},
			want:  RepoUnusable,
			why: "a ceiling entry only stops the walk when it is a STRICT ancestor of the " +
				"starting directory; the starting directory itself is always inspected",
		},
		// REV-133 round 1 test 5 (CLAUDE-002). os.Stat follows the link to a missing
		// target; os.Lstat sees the link, which is the honest answer.
		{
			name:  "third state: dangling .git symlink",
			git:   &stubGit{},
			files: stubFS{dotGit: danglingLink()},
			want:  RepoUnusable,
			why: "a .git symlink with a moved or deleted target is broken metadata, not an " +
				"absence -- 'git init' would follow the dangling link and create its target",
		},
		{
			name:  "ancestor .git that git CAN use",
			git:   &stubGit{revParseOK: map[string]bool{base: true}},
			files: stubFS{parentDotGit: gitDir(), parentHead: gitFile()},
			want:  RepoNotFound,
			why: "git excluded the leaf deliberately (e.g. a filesystem boundary); " +
				"deferring to git's own verdict keeps the existing not-a-repo BATS fixtures green",
		},
		// REV-133 round 1 test 4 (paired guard for CODEX-003 / CLAUDE-001). Inspecting
		// the starting directory before applying the ceiling must not weaken
		// the ceiling for ancestors: starting BELOW the ceiling, the walk must
		// still refuse to ascend into it.
		{
			name:  "ancestor .git above a ceiling directory is not seen",
			git:   &stubGit{},
			files: stubFS{parentDotGit: gitDir(), parentHead: gitFile()},
			env:   map[string]string{"GIT_CEILING_DIRECTORIES": base},
			want:  RepoNotFound,
			why: "the ceiling is a strict ancestor of the starting directory here, so it " +
				"stops the ascent and the repository above it is correctly not seen",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := repoChecker{
				run:   tc.git.run,
				stat:  tc.files.stat,
				lstat: tc.files.lstat,
				env:   stubEnv(tc.env),
			}
			state, err := c.check(context.Background(), leaf)
			require.NoError(t, err, "no case in this table cancels the context, so the "+
				"check must always complete and answer with a state")
			assert.Equal(t, tc.want, state, tc.why)
		})
	}
}

// TestCheckRepo_HappyPathCostsOneGitCall pins the hot path. Probing git's
// runnability up front would double the git processes every successful command
// spawns, so the probe must run only after rev-parse has already failed.
func TestCheckRepo_HappyPathCostsOneGitCall(t *testing.T) {
	base := t.TempDir()
	g := &stubGit{revParseOK: map[string]bool{base: true}}
	c := repoChecker{run: g.run, stat: stubFS{}.stat, lstat: stubFS{}.lstat, env: stubEnv(nil)}

	state, err := c.check(context.Background(), base)
	require.NoError(t, err)
	require.Equal(t, RepoOK, state)
	assert.Len(t, g.calls, 1, "a successful check must spawn exactly one git process")
	assert.Contains(t, g.calls[0], "rev-parse --git-dir")
}

// TestCheckRepo_MissingGitSkipsTheVersionProbe: run() already turns a missing
// binary into ErrNotInstalled, so there is nothing to learn from a second call.
func TestCheckRepo_MissingGitSkipsTheVersionProbe(t *testing.T) {
	base := t.TempDir()
	g := &stubGit{notInstalled: true}
	c := repoChecker{run: g.run, stat: stubFS{}.stat, lstat: stubFS{}.lstat, env: stubEnv(nil)}

	state, err := c.check(context.Background(), base)
	require.NoError(t, err)
	require.Equal(t, GitUnusable, state)
	assert.Len(t, g.calls, 1, "ErrNotInstalled is conclusive; do not probe again")
}

// TestCheckRepo_SkipsTheReprobeOfTheStartingDirectory pins REV-135 round 2 CLAUDE-003.
//
// When the metadata turns out to be in the starting directory itself -- the
// common third-state case, and the one the tc-dc258d2e acceptance case
// exercises -- the ancestor re-probe would ask git about the very directory
// whose rev-parse has already failed. The answer is guaranteed to be the same,
// so the third process is pure waste on every gated command in that state.
func TestCheckRepo_SkipsTheReprobeOfTheStartingDirectory(t *testing.T) {
	base := t.TempDir()
	leaf := filepath.Join(base, "project")
	g := &stubGit{}
	files := stubFS{filepath.Join(leaf, ".git"): gitFile()}
	c := repoChecker{run: g.run, stat: files.stat, lstat: files.lstat, env: stubEnv(nil)}

	state, err := c.check(context.Background(), leaf)
	require.NoError(t, err)
	require.Equal(t, RepoUnusable, state,
		"the classification itself must not change; only the process count does")
	assert.Equal(t, []string{
		leaf + " :: rev-parse --git-dir",
		leaf + " :: --version",
	}, g.calls, "metadata in the starting directory must not be re-probed: git has already "+
		"answered for this exact directory")
}

// TestCheckRepo_StillReprobesAGenuineAncestor is the paired GUARD for
// REV-135 round 2 CLAUDE-003 and is green by design. Skipping the re-probe must apply
// ONLY to the starting directory: when the metadata really is in an ancestor,
// git's verdict on that ancestor is what separates "git excluded the leaf
// deliberately" (RepoNotFound) from "the repository is broken" (RepoUnusable),
// so that third process must still run.
func TestCheckRepo_StillReprobesAGenuineAncestor(t *testing.T) {
	base := t.TempDir()
	leaf := filepath.Join(base, "project")
	g := &stubGit{revParseOK: map[string]bool{base: true}}
	files := stubFS{
		filepath.Join(base, ".git"):         gitDir(),
		filepath.Join(base, ".git", "HEAD"): gitFile(),
	}
	c := repoChecker{run: g.run, stat: files.stat, lstat: files.lstat, env: stubEnv(nil)}

	state, err := c.check(context.Background(), leaf)
	require.NoError(t, err)
	require.Equal(t, RepoNotFound, state)
	assert.Equal(t, []string{
		leaf + " :: rev-parse --git-dir",
		leaf + " :: --version",
		base + " :: rev-parse --git-dir",
	}, g.calls, "an ancestor is a different directory, so git must still be asked about it")
}

// TestCheckRepo_DoesNotBlameGitForAnInterrupt covers REV-136 round 3 CLAUDE-002.
//
// main.go builds the command context with signal.NotifyContext, so Ctrl-C or
// SIGTERM cancels the context that reaches this classifier. Once cancelled,
// every later git call fails at Start -- and that failure is shaped exactly
// like an unrunnable git. Classifying it told a user whose installation is
// perfectly healthy to "Install git or fix your PATH", which is the same
// wrong-remedy class ENH-189 exists to remove.
//
// An interrupt is not a repository state, so it is propagated, not classified.
func TestCheckRepo_DoesNotBlameGitForAnInterrupt(t *testing.T) {
	base := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	g := &stubGit{}
	c := repoChecker{run: g.run, stat: stubFS{}.stat, lstat: stubFS{}.lstat, env: stubEnv(nil)}

	state, err := c.check(ctx, base)

	require.Error(t, err, "an interrupted check has no answer to give, and must say so")
	assert.ErrorIs(t, err, context.Canceled)
	assert.NotEqual(t, GitUnusable, state,
		"an interrupt must not be reported as a broken git installation")
	assert.Equal(t, RepoUnusable, state,
		"the state alongside an error is the fail-closed placeholder: a caller that "+
			"ignored the error must still never be told to run 'git init'")
	assert.Len(t, g.calls, 1,
		"once the context is cancelled there is nothing to learn from another git process")
}

// TestCheckRepo_PropagatesAnInterruptDuringTheRunnabilityProbe closes the
// narrow race in the same finding (REV-136 round 3 CLAUDE-002): the interrupt
// can land between the guard and the `git --version` probe, in which case the
// probe itself is the call that fails at Start. The verdict must be the same.
func TestCheckRepo_PropagatesAnInterruptDuringTheRunnabilityProbe(t *testing.T) {
	base := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())

	// cancelOnVersion fires the interrupt during the probe, so the guard above
	// it sees a live context and only the post-probe re-test can catch it.
	g := &stubGit{cancelOnVersion: cancel}
	c := repoChecker{run: g.run, stat: stubFS{}.stat, lstat: stubFS{}.lstat, env: stubEnv(nil)}

	state, err := c.check(ctx, base)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.NotEqual(t, GitUnusable, state,
		"an interrupt landing during the probe is still an interrupt, not a broken git")
}

// TestCheckRepo_PropagatesAnInterruptDuringTheMetadataWalk covers
// REV-136 round 4 CODEX-003.
//
// The round-3 fix guarded every git call, but discovery is not a git call: by
// the time the walk starts, both git processes have already returned, so no
// guard in check can see an interrupt that arrives during it. The walk ran to
// the filesystem root regardless and handed back a finished verdict -- here
// RepoNotFound, the one state whose remedy is 'git init'. A Ctrl-C is not an
// empty directory, and it must not be answered as one.
//
// The stat seam makes the timing exact: the interrupt fires from inside the
// first filesystem probe, which is the only moment that reproduces the defect.
func TestCheckRepo_PropagatesAnInterruptDuringTheMetadataWalk(t *testing.T) {
	base := t.TempDir()
	leaf := filepath.Join(base, "deep", "project")
	ctx, cancel := context.WithCancel(context.Background())

	g := &stubGit{}
	files := stubFS{}
	statCalls := 0
	stat := func(name string) (os.FileInfo, error) {
		statCalls++
		cancel()
		return files.stat(name)
	}
	c := repoChecker{run: g.run, stat: stat, lstat: files.lstat, env: stubEnv(nil)}

	state, err := c.check(ctx, leaf)

	require.Error(t, err, "an interrupt during discovery leaves the check with no answer, "+
		"and it must say so rather than invent one")
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, RepoUnusable, state,
		"the state alongside an error is the fail-closed placeholder, never RepoNotFound: "+
			"a caller that ignored the error must still not be told to run 'git init'")
	assert.Equal(t, 1, statCalls,
		"the walk must stop at the probe the interrupt landed in, not carry on climbing "+
			"to the filesystem root -- each remaining stat delays an exit the user has "+
			"already asked for")
	assert.Len(t, g.calls, 2,
		"the interrupt arrives after both git calls, so the process count is unchanged; "+
			"only the verdict is")
}

// TestFindMetadata_DoesNotProbeAnAlreadyCancelledWalk is the paired guard for
// REV-136 round 4 CODEX-003 on the other side of the probe: a context cancelled
// before discovery begins must cost zero filesystem calls, not one.
//
// This is checked on findMetadata directly because check's own guards catch a
// pre-cancelled context earlier, so the entry guard is unreachable from there.
// It is still the contract findMetadata owes any future caller.
func TestFindMetadata_DoesNotProbeAnAlreadyCancelledWalk(t *testing.T) {
	base := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	statCalls := 0
	files := stubFS{filepath.Join(base, ".git"): gitFile()}
	stat := func(name string) (os.FileInfo, error) {
		statCalls++
		return files.stat(name)
	}
	c := repoChecker{run: (&stubGit{}).run, stat: stat, lstat: files.lstat, env: stubEnv(nil)}

	ancestor, start, found, err := c.findMetadata(ctx, base)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.False(t, found, "a cancelled walk reached no conclusion, so it found nothing")
	assert.Empty(t, ancestor)
	assert.Equal(t, base, start, "start is still reported: it is computed, not probed")
	assert.Zero(t, statCalls,
		"metadata really is present here, so only the entry guard can explain a walk "+
			"that touched the filesystem zero times")
}

// TestCheckRepo_DoesNotClassifyFromGitStderr is the regression guard for the
// trap at the heart of ENH-189: git's own message for the dangling-pointer
// state contains "not a git repository". Anything that classified by matching
// that text would recreate the misleading branch this work removes.
func TestCheckRepo_DoesNotClassifyFromGitStderr(t *testing.T) {
	base := t.TempDir()
	leaf := filepath.Join(base, "worktree")
	g := &stubGit{}
	files := stubFS{filepath.Join(leaf, ".git"): gitFile()}
	c := repoChecker{
		run:   g.run,
		stat:  files.stat,
		lstat: files.lstat,
		env:   stubEnv(nil),
	}

	// The stub returns exactly what real git returns here.
	_, err := g.run(context.Background(), leaf, "rev-parse", "--git-dir")
	require.Error(t, err)
	require.Contains(t, strings.ToLower(err.Error()), "not a git repository",
		"the fixture must reproduce git's misleading wording, or this test proves nothing")

	state, stateErr := c.check(context.Background(), leaf)
	require.NoError(t, stateErr)
	assert.Equal(t, RepoUnusable, state,
		"classification must come from the metadata probe, not from git's stderr")
}

// TestIsCeiling_UsesThePlatformCaseRule covers REV-135 round 2 CODEX-003.
//
// Windows compares paths without regard to case, so a GIT_CEILING_DIRECTORIES
// entry that differs from the walked directory only in casing names the SAME
// directory there and must still stop the walk. Raw string equality silently
// let the walk climb past it.
//
// Both platform branches are exercised on every platform by swapping the
// pathsAreCaseInsensitive seam. That is deliberate: this project runs its
// acceptance suite on a Windows runner but its Go suite mostly on Linux, and a
// rule only one platform can check is a rule that rots.
func TestIsCeiling_UsesThePlatformCaseRule(t *testing.T) {
	root := string(filepath.Separator)
	ceiling := filepath.Join(root, "Users", "Bill", "Projects")
	sameDirDifferentCase := filepath.Join(root, "users", "bill", "projects")
	ceilings := []string{ceiling}

	restore := func(v bool) func() {
		prev := pathsAreCaseInsensitive
		pathsAreCaseInsensitive = v
		return func() { pathsAreCaseInsensitive = prev }
	}

	t.Run("exact match stops the walk on every platform", func(t *testing.T) {
		defer restore(false)()
		assert.True(t, isCeiling(ceilings, ceiling))
		defer restore(true)()
		assert.True(t, isCeiling(ceilings, ceiling))
	})

	t.Run("windows: a casing-only difference is the same directory", func(t *testing.T) {
		defer restore(true)()
		assert.True(t, isCeiling(ceilings, sameDirDifferentCase),
			"on Windows these are one directory, so the ceiling must stop the walk")
	})

	t.Run("unix: a casing-only difference is a different directory", func(t *testing.T) {
		defer restore(false)()
		assert.False(t, isCeiling(ceilings, sameDirDifferentCase),
			"on a case-sensitive filesystem these are two directories and only the "+
				"listed one is a ceiling")
	})

	t.Run("both sides are cleaned before comparison", func(t *testing.T) {
		defer restore(false)()
		assert.True(t, isCeiling(ceilings, filepath.Join(ceiling, "sub", "..")))
	})
}

func TestParseCeilings(t *testing.T) {
	assert.Nil(t, parseCeilings(""), "unset means no ceilings")

	// The absolute entry comes from t.TempDir() rather than being built from
	// filepath.Separator. A separator-rooted path is absolute on Unix but NOT
	// on Windows, where filepath.IsAbs wants a volume, so "\abs\one" was
	// dropped and this test failed on the Windows runner only. t.TempDir()
	// is genuinely absolute on both.
	absOne := t.TempDir()

	// Git ignores relative entries; so do we.
	got := parseCeilings(strings.Join([]string{
		absOne,
		filepath.Join("relative", "two"),
		"",
	}, string(filepath.ListSeparator)))

	require.Len(t, got, 1)
	assert.Equal(t, filepath.Clean(absOne), got[0])

	// The Windows drop above is real product behaviour, not just a test
	// artefact: a Git-Bash-style ceiling such as /tmp/foo is volume-less, so
	// GTMS discards a ceiling that Git for Windows would honour. Pin it here
	// so the divergence is visible at the code rather than only in the record.
	// Tracked as ENH-196; fixing it means translating MSYS path forms, and a
	// leading-slash-only fix would be a no-op because the comparison would
	// still never match a C:\... walked path.
	if runtime.GOOS == "windows" {
		assert.Empty(t, parseCeilings("/tmp/msys-style"),
			"known ENH-196 divergence: MSYS-form ceilings are dropped on Windows")
	} else {
		assert.Len(t, parseCeilings("/tmp/msys-style"), 1,
			"a POSIX absolute ceiling is honoured on Unix")
	}
}

// TestIsRepo_MatchesCheckRepo keeps the legacy bool helper honest: it must be a
// pure narrowing of CheckRepo, not a second implementation that could drift.
func TestIsRepo_MatchesCheckRepo(t *testing.T) {
	skipIfShort(t)

	dir := t.TempDir()
	ctx := context.Background()
	state, err := CheckRepo(ctx, dir)
	require.NoError(t, err)
	assert.Equal(t, state == RepoOK, IsRepo(ctx, dir))
}
