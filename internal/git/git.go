// Package git provides shell-out wrappers around the git CLI.
// All functions invoke git via os/exec and return structured errors.
package git

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrNotInstalled is returned when git is not found on PATH.
var ErrNotInstalled = fmt.Errorf("Git is not installed or not on PATH. GTMS requires git.")

// run executes a git command in the given directory and returns
// the trimmed stdout. If dir is empty, the current directory is used.
func run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	if err != nil {
		// Check if git itself is missing
		if execErr, ok := err.(*exec.Error); ok && execErr.Err == exec.ErrNotFound {
			return "", ErrNotInstalled
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// IsRepo returns true if the given directory is inside a git repository.
//
// It answers only "usable or not". For anything the user will read, use
// CheckRepo instead: a false here can mean git is missing, git is broken, git
// cannot use the repository that is present, or there is genuinely no
// repository -- four situations with four different remedies (ENH-189). A
// check that was interrupted before it could answer also lands here as false,
// which is the fail-closed direction.
func IsRepo(ctx context.Context, dir string) bool {
	state, err := CheckRepo(ctx, dir)
	return err == nil && state == RepoOK
}

// ProjectRoot returns the top-level directory of the git repository
// containing the given directory.
func ProjectRoot(ctx context.Context, dir string) (string, error) {
	root, err := run(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("not a git repository: %w", err)
	}
	return filepath.Clean(root), nil
}

// CurrentBranch returns the name of the currently checked-out branch.
func CurrentBranch(ctx context.Context, dir string) (string, error) {
	branch, err := run(ctx, dir, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", fmt.Errorf("could not determine current branch: %w", err)
	}
	return branch, nil
}

// HeadCommit returns the SHA of the current HEAD commit. Added for
// CON-023 / ENH-146 result-contract Git-context stamping. Returns an
// error if HEAD cannot be resolved (e.g. a freshly init'd repo with
// no commits) — callers stamp the contract field only on success.
func HeadCommit(ctx context.Context, dir string) (string, error) {
	sha, err := run(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("could not determine HEAD commit: %w", err)
	}
	return sha, nil
}

// IsDirty reports whether the working tree has uncommitted changes
// (modified, added, deleted, or untracked files) at the moment the call
// runs. CON-023 / ENH-146: stamped on the result contract before the
// result file is written so the result write itself cannot make the
// workspace look dirty. Returns an error if git status fails; callers
// stamp the contract field only on success (nil pointer = unavailable).
func IsDirty(ctx context.Context, dir string) (bool, error) {
	out, err := run(ctx, dir, "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("could not read working-tree status: %w", err)
	}
	return out != "", nil
}

// FileExists returns true if the given path is tracked by git.
func FileExists(ctx context.Context, dir string, path string) bool {
	_, err := run(ctx, dir, "ls-files", "--error-unmatch", path)
	return err == nil
}

// ListFiles returns tracked files matching the given pattern under dir.
// If pattern is empty, all files in the directory are returned.
func ListFiles(ctx context.Context, dir string, subdir string, pattern string) ([]string, error) {
	args := []string{"ls-files"}
	if subdir != "" {
		args = append(args, subdir)
	}
	if pattern != "" {
		args = append(args, "--", pattern)
	}
	out, err := run(ctx, dir, args...)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// CreateWorktree creates a new git worktree at the given path on a new branch.
func CreateWorktree(ctx context.Context, dir string, path string, branch string) error {
	_, err := run(ctx, dir, "worktree", "add", path, "-b", branch)
	if err != nil {
		return fmt.Errorf("failed to create worktree: %w", err)
	}
	return nil
}

// RemoveWorktree removes a git worktree at the given path.
func RemoveWorktree(ctx context.Context, dir string, path string) error {
	_, err := run(ctx, dir, "worktree", "remove", path)
	if err != nil {
		return fmt.Errorf("failed to remove worktree: %w", err)
	}
	return nil
}

// ListWorktrees returns the porcelain output of git worktree list.
func ListWorktrees(ctx context.Context, dir string) (string, error) {
	return run(ctx, dir, "worktree", "list", "--porcelain")
}

// RemoteURL returns the URL of the given remote (typically "origin").
// Returns an empty string and an error if the remote is not configured.
func RemoteURL(ctx context.Context, dir string, remote string) (string, error) {
	url, err := run(ctx, dir, "remote", "get-url", remote)
	if err != nil {
		return "", fmt.Errorf("could not get URL for remote '%s': %w", remote, err)
	}
	return url, nil
}

// InferRepo attempts to extract an "org/repo" path from the origin remote URL.
// It handles HTTPS URLs (https://github.com/org/repo.git) and SSH URLs
// (git@github.com:org/repo.git). Returns fallback if the remote is not set
// or cannot be parsed.
func InferRepo(ctx context.Context, dir string, fallback string) string {
	url, err := RemoteURL(ctx, dir, "origin")
	if err != nil || url == "" {
		return fallback
	}

	// Strip trailing slashes, then .git, then any slash the .git was hiding.
	// The order matters: a remote recorded as ".../repo.git/" would otherwise
	// keep its ".git" (the suffix trim misses it behind the slash) and persist
	// "acme/repo.git" as the repository identity.
	url = strings.TrimRight(url, "/")
	url = strings.TrimSuffix(url, ".git")
	url = strings.TrimRight(url, "/")

	// Scheme URLs -- https://host/path, ssh://git@host/path, git://host/path --
	// carry the repository path after the host. Everything before the first "/"
	// that follows the scheme is host information (optionally userinfo@ and
	// :port), so the path is simply what remains.
	//
	// The WHOLE path is kept, not the last two segments: a repository's identity
	// must not depend on the transport used to clone it. GitLab nests groups, so
	// gitlab.com/group/sub/repo and git@gitlab.com:group/sub/repo are the same
	// project and must both yield "group/sub/repo" (REV CODEX-001). Keeping only
	// the last two would also mangle a host:port URL with a single path segment.
	if i := strings.Index(url, "://"); i >= 0 {
		rest := url[i+3:]
		if j := strings.Index(rest, "/"); j >= 0 {
			if path := strings.Trim(rest[j+1:], "/"); path != "" {
				return path
			}
		}
		return fallback
	}

	// SCP-style SSH (git@github.com:org/repo) puts the path after a colon that
	// is NOT part of a URL scheme -- which is why the scheme case above has to
	// be settled first.
	//
	// ENH-190: the previous test was `no "/" before the colon`, which is true of
	// "https" as well as "git@github.com", so every HTTPS remote took this
	// branch and returned "//github.com/org/repo" -- a mangled value persisted
	// into project.repo for the most common remote format.
	// In SCP syntax the colon separates host from path, so it always precedes
	// any "/". Requiring that keeps a filesystem path whose directory name
	// happens to contain a colon (/srv/acme:archive/repo) out of this branch --
	// it is a path, not a host:path pair, and belongs to the fallback below.
	//
	// This is the ORIGINAL pre-ENH-190 guard, restored. It was always the right
	// test for SCP-versus-path; its only fault was that "https" also has no "/"
	// before its colon, so schemes fell in here too. That is now impossible:
	// the scheme case above returns before this point.
	if idx := strings.Index(url, ":"); idx > 0 && !strings.Contains(url[:idx], "/") {
		if path := url[idx+1:]; path != "" {
			return path
		}
	}

	// A bare filesystem-path remote (/srv/git/repo, ../mirror/repo) has neither
	// a scheme nor an SCP colon. Keep the last two segments, as the pre-ENH-190
	// code did for anything that was not SCP-style.
	//
	// Falling through to the fallback here would be actively misleading: init
	// passes the no-origin sentinel as its fallback, so a user who HAS a
	// path remote would be told "no origin remote configured".
	parts := strings.Split(url, "/")
	if len(parts) >= 2 {
		org, repo := parts[len(parts)-2], parts[len(parts)-1]
		if org != "" && repo != "" {
			return org + "/" + repo
		}
	}

	return fallback
}
