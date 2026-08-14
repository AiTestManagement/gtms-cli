package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/git"
	"github.com/aitestmanagement/gtms-cli/internal/output"
)

// withRepoState replaces the classification seam for the duration of a test.
// Nothing on disk has to be broken: the point of the seam is that a missing
// git, a broken git and an unreadable .git can all be driven deterministically
// on every platform.
func withRepoState(t *testing.T, state git.RepoState) {
	t.Helper()
	withRepoStateResult(t, state, nil)
}

// withRepoStateResult is the same seam for the case where the classifier could
// not answer at all -- an interrupted check, which returns an error rather than
// a state (REV-136 round 3 CLAUDE-002).
func withRepoStateResult(t *testing.T, state git.RepoState, err error) {
	t.Helper()
	saved := checkRepoState
	checkRepoState = func(context.Context, string) (git.RepoState, error) { return state, err }
	t.Cleanup(func() { checkRepoState = saved })
}

// gatedCommand is one of the five commands that require a repository. The args
// are the minimum needed to reach the git gate -- every one of these commands
// runs the gate before it looks at its target, so the fixture IDs below never
// have to exist.
type gatedCommand struct {
	name    string
	factory func() *cobra.Command
	args    []string
}

func gatedCommands() []gatedCommand {
	return []gatedCommand{
		{"create", newCreateCmd, []string{"diagnostics"}},
		{"automate", newAutomateCmd, nil},
		{"execute", newExecuteCmd, nil},
		{"prime", newPrimeCmd, []string{"tc-11111111"}},
		{"init", newInitCmd, nil},
	}
}

// TestGitGateParity_AllCommands is the ENH-189 AC 7 guard.
//
// Classification lives in internal/git and its tests cannot see a command that
// kept the old text. Without this table, one command could go on saying
// "Initialize a git repo with 'git init'" for a broken git while every other
// mandated test stayed green -- and a split honoured in one command but not the
// others is worse than no split at all.
func TestGitGateParity_AllCommands(t *testing.T) {
	restore := withExecuteGlobals(t, t.TempDir(), nil)
	t.Cleanup(restore)

	states := []struct {
		name    string
		state   git.RepoState
		message string
		// hint is the same for all five commands unless hintInit is set.
		hint     string
		hintInit string
		// forbidden fragments come straight from the acceptance specs in
		// gtms/test/cases/git-runtime-repo-diagnostics/. Every entry must be
		// copy that some OTHER state really emits -- a fragment no state can
		// produce passes unconditionally and reads as coverage while asserting
		// nothing.
		forbidden []string
	}{
		{
			name:    "git unusable",
			state:   git.GitUnusable,
			message: "git could not be run.",
			hint:    "Install git or fix your PATH -- GTMS shells out to the git CLI.",
			// tc-6829ef1d and tc-16f11172. The state-4 fragments are the real
			// competing message: "owning shell" appears nowhere in the product,
			// so asserting its absence proved nothing.
			forbidden: []string{
				"Not a git repository",
				"git init",
				"The repository here cannot be used by git.",
				"owns this worktree",
				"GIT_DIR",
			},
		},
		{
			name:    "repository unusable",
			state:   git.RepoUnusable,
			message: "The repository here cannot be used by git.",
			hint:    "Run gtms from the shell that owns this worktree, or set GIT_DIR and GIT_WORK_TREE.",
			// tc-dc258d2e. 'git init' is not merely unhelpful here, it is harmful.
			forbidden: []string{"Not a git repository", "Initialize a git repo", "Run 'git init'"},
		},
		{
			name:    "not a repository",
			state:   git.RepoNotFound,
			message: "Not a git repository.",
			hint:    "Initialize a git repo with 'git init'.",
			// The one legitimate divergence: init tells the user to create the
			// repository before initialising GTMS in it.
			hintInit: "Run 'git init' first, then 'gtms init'.",
			// tc-e7427d61.
			forbidden: []string{"git could not be run", "repository here cannot be used", "GIT_DIR"},
		},
	}

	for _, st := range states {
		for _, gc := range gatedCommands() {
			t.Run(st.name+"/"+gc.name, func(t *testing.T) {
				withRepoState(t, st.state)

				stderr, err := runCmdCaptureStderr(t, gc.factory(), gc.args...)

				require.Error(t, err, "the command must refuse to proceed")
				assert.True(t, output.IsDisplayed(err),
					"the error must be marked displayed so main.go does not print it twice")

				wantHint := st.hint
				if gc.name == "init" && st.hintInit != "" {
					wantHint = st.hintInit
				}

				lines := strings.Split(strings.TrimRight(stderr, "\n"), "\n")
				require.Len(t, lines, 2,
					"the house format is exactly two lines: a problem line and one hint line -- got %q", stderr)
				assert.Equal(t, output.IconError+" "+st.message, lines[0])
				assert.Equal(t, "    "+wantHint, lines[1],
					"the hint must be indented with exactly four spaces")

				for _, bad := range st.forbidden {
					assert.NotContains(t, stderr, bad,
						"the frozen acceptance spec forbids %q in this state", bad)
				}
			})
		}
	}
}

// TestGitGate_ProceedsWhenRepoIsUsable covers the fourth state. It calls the
// gate directly rather than running a whole command, because a command that
// passes the gate goes on to resolve adapters and touch the filesystem, which
// is not what this assertion is about.
func TestGitGate_ProceedsWhenRepoIsUsable(t *testing.T) {
	withRepoState(t, git.RepoOK)

	var err error
	stderr := captureStderr(t, func() {
		err = requireGitRepo(context.Background(), t.TempDir(), notARepoHintDefault)
	})

	assert.NoError(t, err)
	assert.Empty(t, stderr, "a usable repository must produce no diagnostic at all")
}

// TestGitGate_FailsClosedOnAnUnknownState: git.RepoOK is the zero value of
// git.RepoState, so a gate whose default branch returned nil would let an
// unset value straight through. Fail closed instead.
func TestGitGate_FailsClosedOnAnUnknownState(t *testing.T) {
	withRepoState(t, git.RepoState(99))

	var err error
	stderr := captureStderr(t, func() {
		err = requireGitRepo(context.Background(), t.TempDir(), notARepoHintDefault)
	})

	require.Error(t, err)
	assert.Contains(t, stderr, notARepoMessage)
}

// TestGitGate_ReportsAnInterruptWithoutDiagnosingAnything covers the gate half
// of REV-136 round 3 CLAUDE-002.
//
// Ctrl-C cancels the context main.go threads into the gate. Every message this
// file defines is a finding about the user's machine, and an interrupted check
// produced no finding -- so the gate must print none of them. Before the fix
// the user was told "git could not be run. / Install git or fix your PATH" and
// went off to repair an installation that was never broken.
func TestGitGate_ReportsAnInterruptWithoutDiagnosingAnything(t *testing.T) {
	withRepoStateResult(t, git.RepoUnusable, context.Canceled)

	var err error
	stderr := captureStderr(t, func() {
		err = requireGitRepo(context.Background(), t.TempDir(), notARepoHintDefault)
	})

	require.Error(t, err, "the command must still refuse to proceed")
	assert.ErrorIs(t, err, context.Canceled)
	assert.False(t, output.IsDisplayed(err),
		"nothing was printed here, so main.go must be left to report the error")
	assert.Empty(t, stderr, "an interrupt is not a diagnosis: print no message at all")

	for _, message := range []string{
		gitUnusableMessage, gitUnusableHint,
		repoUnusableMessage, repoUnusableHint,
		notARepoMessage, notARepoHintDefault,
	} {
		assert.NotContains(t, stderr, message,
			"an interrupted check must not be dressed up as a finding about the machine")
	}
}

// TestGitGate_HintsAreSingleLine pins the constraint that made ENH-189 choose a
// two-line message over the three-line one an earlier draft proposed:
// output.Errorf takes a single hint, and every acceptance spec asserts that no
// third line is emitted. Embedding a newline here would break all four.
func TestGitGate_HintsAreSingleLine(t *testing.T) {
	for _, hint := range []string{gitUnusableHint, repoUnusableHint, notARepoHintDefault, notARepoHintInit} {
		assert.NotContains(t, hint, "\n",
			"a hint with a newline would emit a third line and break the house error format")
	}
}
