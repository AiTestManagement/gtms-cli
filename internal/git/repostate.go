package git

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// RepoState is the outcome of the repository check that gates every command
// requiring a git repository (ENH-189).
//
// It replaces the bool that IsRepo used to return. A bool could only say
// "usable / not usable", so four completely different realities -- with four
// completely different remedies -- all collapsed into "Not a git repository.
// Initialize a git repo with 'git init'." That sent users whose git was
// missing, broken, or unusable in this shell entirely the wrong way, and in
// the worktree case the suggested remedy was actively harmful.
type RepoState int

const (
	// RepoOK means git ran and resolved a repository for the directory.
	RepoOK RepoState = iota

	// RepoNotFound means git ran perfectly well and there is genuinely no
	// repository here. This is the only state for which 'git init' is the
	// right advice.
	RepoNotFound

	// GitUnusable means git could not be run at all: either it is absent
	// from PATH, or the binary resolves but cannot execute (the BUG-155
	// shape, where git.exe is found but cannot load its DLLs).
	GitUnusable

	// RepoUnusable means repository metadata was detected but git cannot
	// use it -- a dangling worktree pointer, a .git written by a different
	// shell (a WSL path read by Windows git), or a .git that cannot be
	// read. There IS a repository here; 'git init' would damage it.
	RepoUnusable
)

// repoChecker performs the classification. Its four fields are seams: the
// real implementation wires them to git, os.Stat, os.Lstat and os.Getenv,
// while tests inject fakes. ENH-189 requires this because the unrunnable-git
// and unreadable-.git states cannot be produced portably on disk -- chmod and
// executable-bit semantics differ enough between Unix and Windows that a
// filesystem-based test would pass on one platform and mean nothing on the
// other.
//
// stat and lstat are separate seams because the difference between them is
// load-bearing: stat follows a symlink to its target, lstat reports the link
// itself, and a .git symlink with a missing target is the one case where those
// two answers disagree (REV-133 round 1 CLAUDE-002).
type repoChecker struct {
	run   func(ctx context.Context, dir string, args ...string) (string, error)
	stat  func(name string) (os.FileInfo, error)
	lstat func(name string) (os.FileInfo, error)
	env   func(key string) string
}

// defaultChecker returns a repoChecker wired to the real git CLI, filesystem
// and process environment.
func defaultChecker() repoChecker {
	return repoChecker{run: run, stat: os.Stat, lstat: os.Lstat, env: os.Getenv}
}

// CheckRepo classifies the git situation for dir. See RepoState for what each
// answer means. Prefer this over IsRepo for anything the user will read: the
// whole point is to say which of the four realities applies.
//
// The error is non-nil only when the check could not be COMPLETED, which today
// means one thing: ctx was cancelled -- because the user pressed Ctrl-C or the
// process was sent SIGTERM (main.go builds the command context with
// signal.NotifyContext) -- while git was running OR while the metadata walk was
// probing the filesystem. That is not a repository state and must not be
// classified as one -- see the cancellation note in check.
//
// Callers must test the error before the state. The state returned alongside a
// non-nil error is RepoUnusable, chosen only so that a caller who ignores the
// error still fails closed and still never advises 'git init'; it is not a
// finding about the directory.
func CheckRepo(ctx context.Context, dir string) (RepoState, error) {
	return defaultChecker().check(ctx, dir)
}

// check implements the classification.
//
// The order matters. The happy path costs exactly one git invocation, the same
// as before ENH-189; the extra probes only run once something has already gone
// wrong.
//
// One rule runs through every failure branch below: no classification is ever
// derived from a probe that ran, or finished, AFTER the context was cancelled.
// A cancelled context makes exec.CommandContext fail at Start, so every
// subsequent git invocation returns an error whatever the machine's actual
// state is. Reading those errors as evidence produced a false diagnosis about
// the user's installation ("git could not be run. / Install git or fix your
// PATH") for what was really an interrupt -- the same wrong-remedy class
// ENH-189 exists to remove (REV-136 round 3 CLAUDE-002).
//
// The rule covers the filesystem half of the check too. Discovery is not a git
// call, so no guard here could see an interrupt that lands during it; the walk
// carries its own guards and reports cancellation as an error rather than as a
// verdict. Without that, a Ctrl-C partway up the walk still produced a finished
// answer -- usually RepoNotFound, the one state that advises 'git init' -- and
// a slow stat on a network filesystem delayed the exit that the user had
// already asked for (REV-136 round 4 CODEX-003).
func (c repoChecker) check(ctx context.Context, dir string) (RepoState, error) {
	_, err := c.run(ctx, dir, "rev-parse", "--git-dir")
	if err == nil {
		return RepoOK, nil
	}
	if errors.Is(err, ErrNotInstalled) {
		// Conclusive whatever else is going on: the PATH lookup fails before
		// exec looks at the context, so this is a real absent git.
		return GitUnusable, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return RepoUnusable, ctxErr
	}

	// git might be broken rather than merely reporting "no repository here".
	// Establish runnability DIRECTLY with a trivial command instead of reading
	// the shape of the rev-parse failure: the BUG-155 case resolves the binary
	// successfully and then fails, so an exec-error-versus-exit-code split
	// would file it under "not a repository" -- the exact wrong answer ENH-189
	// exists to prevent.
	if _, verErr := c.run(ctx, dir, "--version"); verErr != nil {
		// Re-test: the interrupt may have landed during this very probe.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return RepoUnusable, ctxErr
		}
		return GitUnusable, nil
	}

	// git works and still could not resolve a repository. Either there is
	// genuinely nothing here, or there is something here git cannot use.
	ancestor, start, found, walkErr := c.findMetadata(ctx, dir)
	if walkErr != nil {
		return RepoUnusable, walkErr
	}
	if !found {
		return RepoNotFound, nil
	}
	if ancestor == "" {
		// GIT_DIR declared a repository and git still failed. There is no
		// directory to re-probe, so the metadata is unusable.
		return RepoUnusable, nil
	}
	if ancestor == start {
		// The metadata is in the starting directory itself -- the common third
		// state, and the one the tc-dc258d2e acceptance case exercises. git has
		// already answered for this exact directory at the top of this
		// function, so re-probing it would spawn a third process guaranteed to
		// reproduce the same failure (REV-135 round 2 CLAUDE-003).
		return RepoUnusable, nil
	}

	// Metadata found in an ANCESTOR. Ask git whether it can use that directory
	// rather than guessing from the filesystem. If git is happy there, it
	// excluded dir deliberately (for example a filesystem boundary it will not
	// cross), so dir really is outside a repository.
	if _, ancErr := c.run(ctx, ancestor, "rev-parse", "--git-dir"); ancErr == nil {
		return RepoNotFound, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return RepoUnusable, ctxErr
	}
	return RepoUnusable, nil
}

// findMetadata reports whether repository metadata was detected for dir.
//
// It returns the directory the metadata was found in (the "ancestor"), the
// absolute form of dir itself (the "start"), whether anything was found, and an
// error. Start is returned because the caller must be able to tell the two
// apart: metadata found in the starting directory needs no re-probe, since git
// has already been asked about that exact directory (REV-135 round 2
// CLAUDE-003).
//
// The error is non-nil only for a cancelled ctx, and it means the walk reached
// no conclusion -- not that the directory is in some particular state. The
// caller must return it rather than turn the (empty) result into a verdict. An
// interrupt arriving here used to be invisible: both git calls have already
// returned by this point, so no guard in check could catch it, and the walk ran
// to the filesystem root and handed back a finished answer for a question the
// user had already withdrawn (REV-136 round 4 CODEX-003).
//
// The discovery boundary is deliberate and specified rather than inherited
// from a naive ancestor walk (ENH-189 states this as a constraint):
//
//   - GIT_DIR, when set, declares a repository even though no .git entry may
//     be discoverable anywhere near dir. That case returns an empty ancestor,
//     because there is no directory to re-probe. GIT_WORK_TREE deliberately
//     does NOT get the same treatment: git does not resolve a repository from
//     a work tree alone, so honouring it here would answer "the repository
//     here cannot be used" in a directory that has no repository at all, and
//     would withhold the 'git init' advice that is actually correct there
//     (REV-133 round 1 CODEX-002).
//   - Otherwise the walk runs from dir up to the filesystem root. A directory
//     listed in GIT_CEILING_DIRECTORIES stops the walk only when it is a
//     STRICT ANCESTOR of dir: git always examines the directory the command
//     started in, even when that directory is itself listed as a ceiling, and
//     a ceiling only prevents discovery from ascending into it. Hence the
//     ceiling is tested against the parent about to be entered, never against
//     the current directory (REV-133 round 1 CODEX-003 / CLAUDE-001).
//   - A .git that cannot be stat'ed for any reason other than "does not exist"
//     counts as present. An unreadable .git is metadata git cannot use, not an
//     absence of metadata, and 'git init' is the wrong answer for it.
//
// Filesystem boundaries (git's GIT_DISCOVERY_ACROSS_FILESYSTEM) are not
// modelled here, so this walk can find a repository across a mount that git
// itself would never have considered.
//
// The caller's re-probe of the returned ancestor NARROWS that divergence
// rather than closing it, and the difference matters. The re-probe only
// rescues the case where the ancestor repository is healthy: git can use it,
// so the state falls back to RepoNotFound. When the ancestor above the mount
// is itself damaged, or refused under safe.directory -- common on container
// volumes and network shares, which is exactly where mounts are -- the
// re-probe fails too, and a genuine non-repository inside the mount is
// classified RepoUnusable. The user then loses the 'git init' advice that was
// correct for them. Note the direction: this withholds correct advice, it does
// not give harmful advice.
//
// Closing it needs a filesystem-identity seam (device ids on Unix, volume
// identity on Windows), which is a larger change than the classifier has
// warranted so far. Tracked as ENH-195 (REV-135 round 2 CODEX-002).
func (c repoChecker) findMetadata(ctx context.Context, dir string) (ancestor, start string, found bool, err error) {
	abs, absErr := filepath.Abs(dir)
	if absErr != nil {
		abs = filepath.Clean(dir)
	}
	start = abs

	// Nothing below this point should touch the filesystem once the user has
	// asked to stop.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", start, false, ctxErr
	}

	if c.env("GIT_DIR") != "" {
		return "", start, true, nil
	}

	ceilings := parseCeilings(c.env("GIT_CEILING_DIRECTORIES"))

	// Bounded: each iteration performs a stat syscall and the walk terminates
	// at the filesystem root, so this needs no explicit yield (CLAUDE.md
	// rule 7). The two ctx checks below are cheap atomic reads that add no
	// blocking of their own and, equally, take none away -- the stat is still
	// what paces every iteration.
	cur := start
	for {
		// Before the probe: on a slow or hung network filesystem one stat can
		// take seconds, so the loop must not start another one after an
		// interrupt has arrived.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", start, false, ctxErr
		}

		// The starting directory is inspected unconditionally, and every
		// subsequent directory has already cleared the ceiling test below
		// before being entered.
		hit := c.hasRepoMetadata(cur)

		// After the probe: the interrupt can land while that stat is in
		// flight, and an answer computed from a probe the user has already
		// cancelled must not become a state -- including the "found it" answer
		// on the next line.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", start, false, ctxErr
		}
		if hit {
			return cur, start, true, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", start, false, nil
		}
		if isCeiling(ceilings, parent) {
			return "", start, false, nil
		}
		cur = parent
	}
}

// pathsAreCaseInsensitive records whether the host compares paths without
// regard to case. It is a var rather than an inline runtime.GOOS test so that
// both branches can be exercised from a test on either platform: this project
// runs its acceptance suite on a Windows runner but its Go suite mostly on
// Linux, and a platform rule only one of them can check is a rule that rots.
var pathsAreCaseInsensitive = runtime.GOOS == "windows"

// isCeiling reports whether dir is one of the GIT_CEILING_DIRECTORIES entries.
//
// Both sides are cleaned and then compared with the host's own path-equality
// rule, which on Windows ignores case. Raw byte equality was wrong there: a
// ceiling entry written with different casing names the SAME directory, and
// failing to match it let the walk climb straight past a boundary the user had
// set. This project runs its acceptance suite on a Windows runner, so that is
// a live path, not a hypothetical one (REV-135 round 2 CODEX-003).
//
// Known divergence, deliberate, in the same spirit as the filesystem-boundary
// decision documented on findMetadata: git additionally RESOLVES SYMLINKS in
// ceiling entries before comparing (and an empty entry in the list switches
// that resolution off for the entries after it). GTMS compares cleaned paths
// only. A ceiling written through a symlink therefore will not match the
// resolved directory the walk actually visits, and the walk continues past it.
// The consequence is bounded: at worst an ancestor repository above the
// intended ceiling is found, and the caller then re-probes it with git, which
// falls back to RepoNotFound when git can use it. Modelling git's resolution
// rules is a larger behaviour than this classifier needs.
func isCeiling(ceilings []string, dir string) bool {
	dir = filepath.Clean(dir)
	for _, ceiling := range ceilings {
		if pathEqual(dir, ceiling) {
			return true
		}
	}
	return false
}

// pathEqual compares two already-cleaned paths using the host filesystem's
// case rule.
func pathEqual(a, b string) bool {
	if pathsAreCaseInsensitive {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// hasRepoMetadata reports whether dir holds something that is plausibly a
// repository, as opposed to an unrelated entry that merely happens to be
// called .git.
//
// The distinction is not academic. A stray empty .git directory sitting in a
// shared parent -- /tmp/.git is a real and easily created example -- would
// otherwise make every non-repository beneath it report "the repository here
// cannot be used", when the honest answer is that there is no repository and
// 'git init' is exactly right.
//
// The rules, and why each one is where the line falls:
//
//   - Missing: no metadata. The ordinary case. "Missing" means missing as a
//     directory ENTRY, which is why the check falls back to Lstat: os.Stat
//     follows a symlink, so a .git symlink whose target has been moved or
//     deleted reports "does not exist" even though an entry named .git is
//     plainly sitting there. That entry is broken metadata, not an absence,
//     and 'git init' would follow the dangling link and create its target
//     (REV-133 round 1 CLAUDE-002).
//   - Present but un-stat'able: metadata. We cannot read it, and neither can
//     git, so 'git init' is the wrong advice.
//   - A .git DIRECTORY: metadata if it holds HEAD or any other canonical
//     repository marker -- see repoMarkers for the list and for which entries
//     are deliberately NOT on it. Requiring HEAD alone was too
//     narrow in two ways, both of which produced the harmful answer: a
//     repository whose HEAD had been deleted still has its object store, refs
//     and config and is a damaged repository to repair rather than a place to
//     run 'git init'; and a legacy HEAD symlink with a missing target reads as
//     absent through Stat, the same following-the-link asymmetry already fixed
//     for .git itself. Presence is therefore tested as a directory ENTRY, via
//     entryExists, for HEAD and for each marker (REV-135 round 2 CODEX-001).
//
//     An EMPTY .git directory still has none of them and so is still not
//     metadata. That is the rule that rejects the stray-directory case and it
//     is load-bearing: without it a single /tmp/.git would make every
//     non-repository beneath it report "the repository here cannot be used".
//   - A .git FILE: always metadata. A regular file named .git at a directory
//     root is a worktree/submodule pointer and essentially nothing else, and
//     its content being malformed or dangling is precisely the third state
//     this classification exists to name. Requiring the file to parse would
//     send exactly those cases back to 'git init'.
func (c repoChecker) hasRepoMetadata(dir string) bool {
	dotGit := filepath.Join(dir, ".git")

	info, err := c.stat(dotGit)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return true
		}
		// Stat followed a link to a target that is not there. Ask again
		// without following: if the .git entry itself exists, it is a
		// dangling symlink, and that is metadata.
		_, linkErr := c.lstat(dotGit)
		return linkErr == nil
	}
	if info != nil && info.IsDir() {
		for _, marker := range repoMarkers {
			if c.entryExists(filepath.Join(dotGit, marker)) {
				return true
			}
		}
		return false
	}
	return true
}

// repoMarkers are the entries whose presence inside a .git directory makes it
// a repository rather than a coincidentally named folder. HEAD leads because
// an intact repository always has it and the loop then stops at the first
// stat; the rest are what survives when HEAD has been deleted.
//
// The list has to cover git's stable layout, not a memorable subset of it: any
// entry left out is a repository that gets told to run 'git init'. index and
// packed-refs are here for that reason -- a repository stripped down to its
// index, or a gc'd one whose loose refs/ has been replaced by packed-refs, is
// damaged rather than absent (REV-136 round 3 CODEX-001).
//
// Entries that are OPTIONAL or that non-repository scaffolding creates are
// deliberately excluded, because each one would push a genuine
// non-repository into "the repository here cannot be used". hooks is the
// clearest example: `mkdir -p .git/hooks` is a one-line way to make a folder
// that is not a repository, and the artefact-discovery-stale-hash acceptance
// case tc-d4317da0 does exactly that. logs is the same shape -- reflogs can
// be disabled or pruned away entirely.
var repoMarkers = []string{"HEAD", "objects", "refs", "config", "index", "packed-refs"}

// entryExists reports whether name is present as a directory ENTRY.
//
// "Present" leans the same way as the rest of this file: an entry we cannot
// stat is present-and-unreadable, not absent, because git cannot read it
// either and 'git init' is the wrong answer for it. Only fs.ErrNotExist from
// BOTH the following Stat and the no-follow Lstat means genuinely not there --
// which is exactly what makes a dangling symlink (Stat says gone, Lstat says
// here) count as present.
func (c repoChecker) entryExists(name string) bool {
	if _, err := c.stat(name); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return true
	}
	_, linkErr := c.lstat(name)
	return linkErr == nil || !errors.Is(linkErr, fs.ErrNotExist)
}

// parseCeilings splits GIT_CEILING_DIRECTORIES the way git does: entries are
// separated by the platform path-list separator (':' on Unix, ';' on Windows),
// and non-absolute entries are ignored.
//
// Known divergence, deliberate, stated in the same spirit as the symlink note
// on isCeiling and the filesystem-boundary note on findMetadata: on Windows an
// entry written in MSYS / Git-Bash form -- "/tmp/bats-run-xxx", "/c/Users/..."
// -- is DROPPED here, because filepath.IsAbs is false for a path with no
// volume. Git for Windows accepts a leading '/' and honours such a ceiling, so
// git stops discovery where GTMS carries on. The walk can then reach an
// ancestor repository git would never have looked at, and if git cannot use
// that ancestor a genuine non-repository is answered "the repository here
// cannot be used" instead of getting its correct 'git init' advice.
//
// Do not "fix" this by accepting a leading slash as absolute. That is a no-op:
// the entry would clean to "\tmp\..." while every walked directory arrives in
// "C:\..." form, so isCeiling could never match it, and the code would look
// handled while behaving identically. The real fix is translating MSYS path
// form to native form, which needs an MSYS root mapping GTMS cannot reliably
// know. Tracked as ENH-196 (REV-136 round 3 CLAUDE-001).
//
// Note the scope of the case-folding rule above: it makes the COMPARISON
// case-correct on Windows. It does not make Windows ceiling handling correct.
func parseCeilings(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	for _, entry := range filepath.SplitList(raw) {
		if entry == "" || !filepath.IsAbs(entry) {
			continue
		}
		out = append(out, filepath.Clean(entry))
	}
	return out
}
