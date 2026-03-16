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
func IsRepo(ctx context.Context, dir string) bool {
	_, err := run(ctx, dir, "rev-parse", "--git-dir")
	return err == nil
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

	// Strip trailing .git
	url = strings.TrimSuffix(url, ".git")

	// SSH format: git@github.com:org/repo
	if idx := strings.Index(url, ":"); idx > 0 && !strings.Contains(url[:idx], "/") {
		path := url[idx+1:]
		if path != "" {
			return path
		}
	}

	// HTTPS format: https://github.com/org/repo
	parts := strings.Split(url, "/")
	if len(parts) >= 2 {
		return parts[len(parts)-2] + "/" + parts[len(parts)-1]
	}

	return fallback
}
