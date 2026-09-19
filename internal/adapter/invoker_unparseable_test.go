package adapter

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/aitestmanagement/gtms-cli/internal/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The exact BUG-191 variable-indentation malformed contract: the first log:
// block line is indented four spaces, so YAML infers a four-space block; the
// following two-space line closes the block early and the document does not
// parse. Kept byte-for-byte from the field reproduction so a tidier synthetic
// error cannot accidentally exercise a different branch.
const bug191MalformedContractScript = `#!/bin/sh
{
  printf 'task: %s\n'    "${GTMS_TASK_ID}"
  printf 'command: %s\n' "${GTMS_COMMAND}"
  printf 'target: %s\n'  "${GTMS_TESTCASE}"
  printf 'adapter: unparseable-tier2\n'
  printf 'mode: sync\n'
  printf 'status: complete\n'
  printf 'result: fail\n'
  printf 'summary: "1 failed of 1 - deliberate repro"\n'
  printf 'log: |\n'
  printf '    Determining projects to restore...\n'
  printf '  Error Message:\n'
  printf '    Stack Trace:\n'
} > "${GTMS_RESULT_FILE}"
exit 0
`

func execFlagsForBats(artefact string) CommandFlags {
	return CommandFlags{
		Framework:    "bats",
		ArtefactFile: artefact,
		ArtefactHash: "0123456789abcdef",
	}
}

// TestBUG191_UnparseableContract_FailsClosed is the primary BUG-191 unit test.
// A sync Tier 2 execute adapter writes an unparseable contract and exits 0.
// GTMS must report an adapter-contract error (not a pass), route the task to
// error/, and leave a readable reconstructed handoff carrying status: error,
// no result, and the trusted identity + overlay-join fields.
func TestBUG191_UnparseableContract_FailsClosed(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	scriptPath := filepath.Join(root, "testdata", "unparseable.sh")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "testdata"), 0755))
	require.NoError(t, os.WriteFile(scriptPath, []byte(bug191MalformedContractScript), 0755))

	cfg := &config.Config{Project: config.ProjectConfig{Name: "Test", Repo: "org/test"}}
	resolved := &ResolvedAdapter{
		Command: "execute",
		Name:    "unparseable-tier2",
		Config:  &config.AdapterConfig{Mode: "sync", Script: "testdata/unparseable.sh"},
		Tier:    2,
		Mode:    "sync",
	}
	artefact := "test/acceptance/unparseable-contract/tc-11111111-unparseable-subject.bats"

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-11111111", withOwner(t, root, "tc-11111111", execFlagsForBats(artefact)))
	require.NoError(t, err, "recovery returns an error-status result, not a Go error")

	// Command-facing result: error, no salvaged outcome, names the parse failure.
	assert.Equal(t, "error", res.Status)
	assert.Empty(t, res.Result, "an unusable document has no authoritative result")
	assert.Contains(t, res.Summary, "parsing result contract")
	assert.NotContains(t, res.Summary, "pass")

	// Task routed to error/, none in complete/.
	errorTasks, err := task.List(root, "error")
	require.NoError(t, err)
	require.Len(t, errorTasks, 1)
	assert.Equal(t, "tc-11111111", errorTasks[0].Target)
	assert.Equal(t, "error", errorTasks[0].Status)
	completeTasks, err := task.List(root, "complete")
	require.NoError(t, err)
	assert.Empty(t, completeTasks)

	// Durable handoff: readable, status error, no result, trusted join fields.
	handoffPath := result.ResultPath(root, errorTasks[0].ID)
	rc, err := result.Read(handoffPath)
	require.NoError(t, err, "the reconstructed handoff must be valid YAML, not the adapter's malformed bytes")
	assert.Equal(t, "error", rc.Status)
	assert.Empty(t, rc.Result)
	assert.Equal(t, "execute", rc.Command)
	assert.Equal(t, "tc-11111111", rc.Target)
	assert.Equal(t, "unparseable-tier2", rc.Adapter)
	assert.Equal(t, "sync", rc.Mode)
	assert.Equal(t, "bats", rc.Framework)
	assert.Equal(t, "0123456789abcdef", rc.ArtefactHash)
	assert.Equal(t, artefact, rc.Artefact)
	assert.Equal(t, errorTasks[0].ID, rc.Task)

	// created comes from the task file; completed is the recovery's own clock
	// and must be no earlier than created (tc-74dae8b8 steps 11-12).
	assert.Equal(t, errorTasks[0].Created, rc.Created)
	require.NotEmpty(t, rc.Completed)
	assert.GreaterOrEqual(t, rc.Completed, rc.Created)

	// The malformed intent is not salvaged (tc-74dae8b8 step 14).
	assert.NotEqual(t, "fail", rc.Result)
}

// TestBUG191_DirectoryAtHandoffPath_PreservesContentsAndStillRoutesToError is
// the platform-independent counterpart to tc-5fd98b0a, which skips where
// filesystem permissions cannot be enforced. It injects a non-fs.ErrNotExist
// read failure deterministically on every platform by replacing the handoff
// with a directory (os.ReadFile then fails with "is a directory"), proving the
// fix keys on "the contract could not be obtained" rather than special-casing
// missing + malformed only.
//
// REV-164 CLAUDE-003 / CODEX-001 changed what happens next. Recovery no longer
// deletes a directory found at the handoff path -- its contents are unknown, and
// preserving them and reporting is safer than destroying them. So this case now
// pins the whole non-destructive contract in one place:
//
//  1. reconstruction fails and says so;
//  2. the directory and the file inside it both survive byte for byte;
//  3. no temporary file is left behind in .gtms/results/;
//  4. the task still moves out of in-progress/ into error/ -- routing is not
//     conditional on the handoff rewrite succeeding.
//
// The complementary case, where recovery CAN write and must, is covered by
// TestBUG191_UnreadableModeZeroContract_WritesReadableHandoff below.
func TestBUG191_DirectoryAtHandoffPath_PreservesContentsAndStillRoutesToError(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	scriptPath := filepath.Join(root, "testdata", "unreadable.sh")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "testdata"), 0755))
	// Replace the pre-created handoff with a NON-EMPTY directory, then exit 0.
	script := "#!/bin/sh\n" +
		"rm -f \"${GTMS_RESULT_FILE}\"\n" +
		"mkdir \"${GTMS_RESULT_FILE}\"\n" +
		"printf 'sentinel\\n' > \"${GTMS_RESULT_FILE}/keep-me.txt\"\n" +
		"exit 0\n"
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0755))

	cfg := &config.Config{Project: config.ProjectConfig{Name: "Test", Repo: "org/test"}}
	resolved := &ResolvedAdapter{
		Command: "execute",
		Name:    "unreadable-handoff",
		Config:  &config.AdapterConfig{Mode: "sync", Script: "testdata/unreadable.sh"},
		Tier:    2,
		Mode:    "sync",
	}
	artefact := "test/acceptance/unparseable-contract/tc-88888888-unreadable-subject.bats"

	_, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-88888888", withOwner(t, root, "tc-88888888", execFlagsForBats(artefact)))
	require.Error(t, err, "an unwritable handoff path is a persistence failure, surfaced as a Go error")
	assert.Contains(t, err.Error(), "reconstructing result contract")
	assert.Contains(t, err.Error(), "is a directory")

	// 1 + 2. The squatting directory and its contents are untouched.
	errorTasks, listErr := task.List(root, "error")
	require.NoError(t, listErr)
	require.Len(t, errorTasks, 1, "the task must be routed to error/ even though the rewrite failed")
	handoffPath := result.ResultPath(root, errorTasks[0].ID)
	info, statErr := os.Stat(handoffPath)
	require.NoError(t, statErr)
	assert.True(t, info.IsDir(), "recovery must not delete a directory it did not create")
	body, readErr := os.ReadFile(filepath.Join(handoffPath, "keep-me.txt"))
	require.NoError(t, readErr, "the directory's contents must survive recovery")
	assert.Equal(t, "sentinel\n", string(body))

	// 3. No temp litter in .gtms/results/.
	entries, dirErr := os.ReadDir(filepath.Join(root, ".gtms", "results"))
	require.NoError(t, dirErr)
	for _, e := range entries {
		assert.NotContains(t, e.Name(), ".tmp-", "failed recovery left a temporary file behind: %s", e.Name())
	}

	// 4. Routed to error/, out of in-progress/, and never to complete/.
	assert.Equal(t, "tc-88888888", errorTasks[0].Target)
	inProgress, listErr := task.List(root, "in-progress")
	require.NoError(t, listErr)
	assert.Empty(t, inProgress, "the task must not be stranded in in-progress/")
	completeTasks, listErr := task.List(root, "complete")
	require.NoError(t, listErr)
	assert.Empty(t, completeTasks)
}

// TestBUG191_UnreadableModeZeroContract_WritesReadableHandoff is the tc-5fd98b0a
// field shape: the adapter leaves a well-formed file but removes read permission.
// Recovery CAN replace that file -- the rename acts through the writable parent
// directory regardless of the target's own mode -- so here the full durable
// contract holds, including a readable reconstructed handoff. It probes for the
// capability and skips where a mode change does not deny reads (root, Windows),
// which is why the directory case above carries the platform-independent half.
func TestBUG191_UnreadableModeZeroContract_WritesReadableHandoff(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	probe := filepath.Join(root, "probe.txt")
	require.NoError(t, os.WriteFile(probe, []byte("x"), 0644))
	require.NoError(t, os.Chmod(probe, 0000))
	if _, probeErr := os.ReadFile(probe); probeErr == nil {
		t.Skip("cannot make a file unreadable to this process (root, or a filesystem without POSIX read denial)")
	}

	scriptPath := filepath.Join(root, "testdata", "chmod-zero.sh")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "testdata"), 0755))
	script := "#!/bin/sh\nchmod 000 \"${GTMS_RESULT_FILE}\"\nexit 0\n"
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0755))

	cfg := &config.Config{Project: config.ProjectConfig{Name: "Test", Repo: "org/test"}}
	resolved := &ResolvedAdapter{
		Command: "execute",
		Name:    "unreadable-handoff",
		Config:  &config.AdapterConfig{Mode: "sync", Script: "testdata/chmod-zero.sh"},
		Tier:    2,
		Mode:    "sync",
	}
	artefact := "test/acceptance/unparseable-contract/tc-88888888-unreadable-subject.bats"

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-88888888", withOwner(t, root, "tc-88888888", execFlagsForBats(artefact)))
	require.NoError(t, err)
	assert.Equal(t, "error", res.Status)
	assert.Empty(t, res.Result)

	errorTasks, err := task.List(root, "error")
	require.NoError(t, err)
	require.Len(t, errorTasks, 1)
	completeTasks, err := task.List(root, "complete")
	require.NoError(t, err)
	assert.Empty(t, completeTasks)

	handoffPath := result.ResultPath(root, errorTasks[0].ID)
	info, statErr := os.Stat(handoffPath)
	require.NoError(t, statErr)
	require.False(t, info.IsDir(), "the handoff path must be a regular file after recovery")
	rc, err := result.Read(handoffPath)
	require.NoError(t, err, "recovery must not be defeated by the condition that triggered it")
	assert.Equal(t, "error", rc.Status)
	assert.Empty(t, rc.Result)
	assert.Equal(t, "bats", rc.Framework)
}

// TestBUG191_NonZeroExit_UnparseableContract_FailsClosed covers the non-zero
// adapter-exit path (tc-3940522b): the same malformed contract with a non-zero
// exit must also leave a readable error handoff whose diagnostic names BOTH the
// process exit code and the contract read failure.
func TestBUG191_NonZeroExit_UnparseableContract_FailsClosed(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	scriptPath := filepath.Join(root, "testdata", "unparseable-crash.sh")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "testdata"), 0755))
	// Same malformed body, but exit 4 instead of exit 0.
	crashScript := bug191MalformedContractScript[:len(bug191MalformedContractScript)-len("exit 0\n")] + "exit 4\n"
	require.NoError(t, os.WriteFile(scriptPath, []byte(crashScript), 0755))

	cfg := &config.Config{Project: config.ProjectConfig{Name: "Test", Repo: "org/test"}}
	resolved := &ResolvedAdapter{
		Command: "execute",
		Name:    "unparseable-tier2-crash",
		Config:  &config.AdapterConfig{Mode: "sync", Script: "testdata/unparseable-crash.sh"},
		Tier:    2,
		Mode:    "sync",
	}
	artefact := "test/acceptance/unparseable-contract/tc-11111111-unparseable-subject.bats"

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-11111111", withOwner(t, root, "tc-11111111", execFlagsForBats(artefact)))
	require.NoError(t, err)
	assert.Equal(t, "error", res.Status)

	errorTasks, err := task.List(root, "error")
	require.NoError(t, err)
	require.Len(t, errorTasks, 1)

	handoffPath := result.ResultPath(root, errorTasks[0].ID)
	rc, err := result.Read(handoffPath)
	require.NoError(t, err)
	assert.Equal(t, "error", rc.Status)
	assert.Empty(t, rc.Result)
	// Both diagnostics survive: the exit code and the parse failure.
	combined := rc.Summary + "\n" + rc.Log
	assert.Contains(t, combined, "code 4")
	assert.Contains(t, combined, "parsing result contract")
}

// TestBUG191_UnusableContract_DurableStateAcrossCommands is the command-scope
// half of BUG-191. handleSyncResult has ONE production call site shared by
// create, automate, prime and execute, so the fail-closed durable state is a
// single contract, not four -- this table says so out loud rather than leaving
// three of the four commands uncovered. Each row runs the same malformed-contract
// adapter and asserts the same two durable facts: a readable reconstructed
// handoff with status: error and no result, and the task routed to error/.
//
// It deliberately asserts nothing about process exit codes. Those differ by
// command by design (single-target automate exits 0 on a rejected handoff,
// pinned by tc-b65b61cf; execute, create and prime propagate status: error as a
// non-zero exit) and belong to the CLI layer, not here.
func TestBUG191_UnusableContract_DurableStateAcrossCommands(t *testing.T) {
	skipIfShort(t)

	cases := []struct {
		command string
		target  string
		// needsOwner is true for the commands whose invocation boundary
		// requires a proven test-case owner (automate, execute, prime).
		needsOwner bool
	}{
		{command: "create", target: "REQ-191-1"},
		{command: "automate", target: "tc-22222222", needsOwner: true},
		{command: "prime", target: "tc-33333333", needsOwner: true},
		{command: "execute", target: "tc-11111111", needsOwner: true},
	}

	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			root := setupTestProject(t)

			scriptPath := filepath.Join(root, "testdata", "unparseable.sh")
			require.NoError(t, os.MkdirAll(filepath.Join(root, "testdata"), 0755))
			require.NoError(t, os.WriteFile(scriptPath, []byte(bug191MalformedContractScript), 0755))

			cfg := &config.Config{Project: config.ProjectConfig{Name: "Test", Repo: "org/test"}}
			resolved := &ResolvedAdapter{
				Command: tc.command,
				Name:    "unparseable-tier2",
				Config:  &config.AdapterConfig{Mode: "sync", Script: "testdata/unparseable.sh"},
				Tier:    2,
				Mode:    "sync",
			}

			flags := CommandFlags{}
			if tc.command != "create" {
				flags.Framework = "bats"
			}
			if tc.command == "execute" {
				flags = execFlagsForBats("test/acceptance/unparseable-contract/tc-11111111-unparseable-subject.bats")
			}
			if tc.needsOwner {
				flags = withOwner(t, root, tc.target, flags)
			}

			res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, tc.target, flags)
			require.NoError(t, err, "recovery returns an error-status result, not a Go error")
			assert.Equal(t, "error", res.Status)
			assert.Empty(t, res.Result, "an unusable document has no authoritative result")

			errorTasks, err := task.List(root, "error")
			require.NoError(t, err)
			require.Len(t, errorTasks, 1, "%s must route its task to error/", tc.command)
			assert.Equal(t, tc.target, errorTasks[0].Target)
			completeTasks, err := task.List(root, "complete")
			require.NoError(t, err)
			assert.Empty(t, completeTasks)
			inProgress, err := task.List(root, "in-progress")
			require.NoError(t, err)
			assert.Empty(t, inProgress)

			rc, err := result.Read(result.ResultPath(root, errorTasks[0].ID))
			require.NoError(t, err, "the reconstructed handoff must be valid YAML for every command")
			assert.Equal(t, "error", rc.Status)
			assert.Empty(t, rc.Result)
			assert.Equal(t, tc.command, rc.Command)
			assert.Equal(t, tc.target, rc.Target)
			assert.Contains(t, rc.Summary, "parsing result contract")
		})
	}
}

// TestBUG191_RecoveredHandoff_KeepsTrustedProvenance is the REV-164
// CLAUDE-002 / CODEX-002 regression. Replacing the handoff document wholesale
// used to drop executed_by, environment, attempts and the git context that GTMS
// itself stamped before the adapter ran, so the dashboard row for a recovered
// run lost its operator and environment exactly when an infrastructure failure
// made them most useful. Every value asserted here comes from GTMS-held state or
// the repository, never from the adapter's unusable document.
func TestBUG191_RecoveredHandoff_KeepsTrustedProvenance(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)
	gitInit(t, root)

	scriptPath := filepath.Join(root, "testdata", "unparseable.sh")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "testdata"), 0755))
	require.NoError(t, os.WriteFile(scriptPath, []byte(bug191MalformedContractScript), 0755))

	cfg := &config.Config{Project: config.ProjectConfig{Name: "Test", Repo: "org/test"}}
	resolved := &ResolvedAdapter{
		Command: "execute",
		Name:    "unparseable-tier2",
		Config:  &config.AdapterConfig{Mode: "sync", Script: "testdata/unparseable.sh"},
		Tier:    2,
		Mode:    "sync",
	}

	flags := execFlagsForBats("test/acceptance/unparseable-contract/tc-11111111-unparseable-subject.bats")
	flags.ExecutedBy = "ci-runner"
	flags.Environment = "staging"

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-11111111", withOwner(t, root, "tc-11111111", flags))
	require.NoError(t, err)
	assert.Equal(t, "error", res.Status)

	errorTasks, err := task.List(root, "error")
	require.NoError(t, err)
	require.Len(t, errorTasks, 1)

	rc, err := result.Read(result.ResultPath(root, errorTasks[0].ID))
	require.NoError(t, err)
	assert.Equal(t, "ci-runner", rc.ExecutedBy, "--executed-by must survive onto the recovered handoff")
	assert.Equal(t, "staging", rc.Environment, "--env must survive onto the recovered handoff")
	assert.Equal(t, 1, rc.Attempts, "a recovered run is one attempt, like every other terminal write")
	assert.NotEmpty(t, rc.GitCommit, "git context is re-stamped from the repository, not the adapter")
	assert.NotEmpty(t, rc.GitBranch)
	require.NotNil(t, rc.GitDirty)
}
