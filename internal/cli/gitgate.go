package cli

import (
	"context"
	"fmt"

	"github.com/aitestmanagement/gtms-cli/internal/git"
	"github.com/aitestmanagement/gtms-cli/internal/output"
)

// ENH-189: the copy for the repository gate lives here, in one place, and is
// used by all five commands that require a repository (create, automate,
// execute, prime, init). A split honoured by one command and not the others
// would be worse than no split at all, so there is exactly one definition of
// each line and a table-driven test in gitgate_test.go proves every command
// prints it identically.
//
// The house error format is "<icon> {what went wrong}\n    {what to do}".
// output.Errorf takes a single hint, so each remedy is one line. Do not add a
// third line here: the acceptance specs assert that the whole error is exactly
// two lines.
const (
	// gitUnusable covers both "git is not on PATH" and "git is on PATH but
	// cannot execute". The user's next move is the same either way, and the
	// acceptance specs deliberately group them.
	gitUnusableMessage = "git could not be run."
	gitUnusableHint    = "Install git or fix your PATH -- GTMS shells out to the git CLI."

	// repoUnusable is the third state: a repository IS here, git just cannot
	// use it from this shell. Never suggest 'git init' for this one -- inside
	// an existing worktree it would try to nest a fresh repository.
	repoUnusableMessage = "The repository here cannot be used by git."
	repoUnusableHint    = "Run gtms from the shell that owns this worktree, or set GIT_DIR and GIT_WORK_TREE."

	// notARepo is the original, correct-when-true message. Unchanged.
	notARepoMessage = "Not a git repository."

	// The four action commands share one remedy; init has its own, because
	// the user has to create the repository before initialising GTMS in it.
	notARepoHintDefault = "Initialize a git repo with 'git init'."
	notARepoHintInit    = "Run 'git init' first, then 'gtms init'."
)

// checkRepoState is the seam the parity test replaces to drive each command
// through every classification without needing a broken git on disk.
var checkRepoState = git.CheckRepo

// requireGitRepo is the repository gate for every command that needs one.
// It returns nil when the command may proceed. Otherwise it prints the
// diagnostic matching what is actually wrong and returns an already-displayed
// error, so main.go does not print it a second time.
//
// notARepoHint is the only per-command variation: init's remedy differs.
func requireGitRepo(ctx context.Context, dir string, notARepoHint string) error {
	state, err := checkRepoState(ctx, dir)
	if err != nil {
		// The check never completed -- today that means the user interrupted
		// it (Ctrl-C or SIGTERM, via the signal.NotifyContext in main.go).
		// Report that, and print no diagnostic at all: every line above is a
		// finding about the machine, and we have no finding. Saying "git could
		// not be run. / Install git or fix your PATH" here sent people off to
		// repair an installation that was fine (REV-136 round 3 CLAUDE-002).
		return fmt.Errorf("the git repository check did not complete: %w", err)
	}

	switch state {
	case git.RepoOK:
		return nil
	case git.GitUnusable:
		output.Errorf(gitUnusableMessage, gitUnusableHint)
		return output.AsDisplayed(fmt.Errorf("git could not be run"))
	case git.RepoUnusable:
		output.Errorf(repoUnusableMessage, repoUnusableHint)
		return output.AsDisplayed(fmt.Errorf("repository here cannot be used"))
	default:
		// RepoNotFound, and anything unrecognised. Failing closed matters
		// because RepoOK is the zero value of git.RepoState: a default that
		// returned nil would let an unset value through the gate.
		output.Errorf(notARepoMessage, notARepoHint)
		return output.AsDisplayed(fmt.Errorf("not a git repository"))
	}
}
