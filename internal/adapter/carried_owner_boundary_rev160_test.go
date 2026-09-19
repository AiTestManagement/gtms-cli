package adapter

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
)

// seedOwnerForTest returns the proven owner of tcID, writing a minimal spec
// first if the fixture does not already contain one. It never mints an Owner
// directly: Owner has no exported constructor precisely so every one in the
// process comes from a real snapshot resolution (ADR-024 s5).
func seedOwnerForTest(t *testing.T, root, tcID string) *testcase.Owner {
	t.Helper()
	if owner, err := testcase.ResolveOwner(root, tcID); err == nil {
		return owner
	}
	dir := filepath.Join(root, "gtms", "test", "cases")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, tcID+"-seeded.md"),
		[]byte("---\ntest_case_id: "+tcID+"\ntitle: seeded\n---\n\nbody\n"), 0o644))
	owner, err := testcase.ResolveOwner(root, tcID)
	require.NoError(t, err, "seeding an owner for %s", tcID)
	return owner
}

// withOwner attaches the proven owner of tcID to flags. Every automate, execute
// and prime invocation now carries one (REV-160 CODEX-002), so a test that
// exercises the invoker for those commands has to supply it the same way
// production does.
func withOwner(t *testing.T, root, tcID string, flags CommandFlags) CommandFlags {
	t.Helper()
	flags.Owner = seedOwnerForTest(t, root, tcID)
	return flags
}

// --- the boundary itself -------------------------------------------------

// boundaryProject is a minimal project with one real spec and a Tier 1 adapter
// whose command TOUCHES A MARKER FILE. The marker is what makes the "invoked
// nothing" assertions real rather than assumed.
func boundaryProject(t *testing.T, command string) (root string, cfg *config.Config, resolved *ResolvedAdapter, marker string) {
	t.Helper()
	root = t.TempDir()
	for _, dir := range []string{
		"gtms/tasks/pending", "gtms/tasks/complete", "gtms/tasks/error",
		"gtms/tasks/in-progress", "gtms/tasks/in-review",
		"gtms/test/cases", "gtms/automation/wiring",
		".gtms/results", ".gtms/worktrees", ".gtms/logs",
	} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0o755))
	}
	marker = filepath.Join(root, "adapter-was-invoked.txt")
	cfg = &config.Config{Project: config.ProjectConfig{Name: "Test", Repo: "org/test"}}
	resolved = &ResolvedAdapter{
		Command: command,
		Name:    "boundary-mock",
		Config: &config.AdapterConfig{
			Mode:    "sync",
			Command: "touch '" + filepath.ToSlash(marker) + "'",
		},
		Tier: 1,
		Mode: "sync",
	}
	return root, cfg, resolved, marker
}

// assertNoStateCreated is the "refused before anything happened" check: no task
// file, no result contract, no wiring record, and no adapter process.
func assertNoStateCreated(t *testing.T, root, marker string) {
	t.Helper()
	for _, glob := range []string{
		"gtms/tasks/*/*.md",
		".gtms/results/*",
		"gtms/automation/wiring/*",
	} {
		matches, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(glob)))
		assert.Empty(t, matches, "a refused invocation must leave no %s behind", glob)
	}
	_, statErr := os.Stat(marker)
	assert.True(t, os.IsNotExist(statErr),
		"the adapter ran: a refusal must happen BEFORE any external code is invoked")
}

// REV-160 CODEX-002, arm 1: no owner at all.
func TestInvokeWithRoot_NilOwner_RefusesBeforeCreatingAnyState(t *testing.T) {
	skipIfShort(t)
	for _, command := range []string{"automate", "execute", "prime"} {
		t.Run(command, func(t *testing.T) {
			root, cfg, resolved, marker := boundaryProject(t, command)
			seedOwnerForTest(t, root, "tc-a1b2c3d4") // the spec exists; the OWNER just was not carried

			_, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-a1b2c3d4", CommandFlags{})

			require.Error(t, err)
			var coe *CarriedOwnerError
			require.ErrorAs(t, err, &coe)
			assert.Equal(t, command, coe.Command)
			assert.Empty(t, coe.OwnerID)
			assertNoStateCreated(t, root, marker)
		})
	}
}

// Arm 2: an owner for a DIFFERENT test case. This is the attribution defect --
// task, result and wiring records would say tc-a while the adapter reads tc-b.
func TestInvokeWithRoot_OwnerForAnotherTestCase_RefusesBeforeCreatingAnyState(t *testing.T) {
	skipIfShort(t)
	for _, command := range []string{"automate", "execute", "prime"} {
		t.Run(command, func(t *testing.T) {
			root, cfg, resolved, marker := boundaryProject(t, command)
			seedOwnerForTest(t, root, "tc-aaaaaaaa")
			otherOwner := seedOwnerForTest(t, root, "tc-bbbbbbbb")

			_, err := InvokeWithRoot(context.Background(), root, cfg, resolved,
				"tc-aaaaaaaa", CommandFlags{Owner: otherOwner})

			require.Error(t, err)
			var coe *CarriedOwnerError
			require.ErrorAs(t, err, &coe)
			assert.Equal(t, "tc-aaaaaaaa", coe.Target)
			assert.Equal(t, "tc-bbbbbbbb", coe.OwnerID)
			assert.Contains(t, coe.Error(), "a different test case")
			assertNoStateCreated(t, root, marker)
		})
	}
}

// Arm 3: the owner was proven, then the spec CHANGED before the invocation --
// rewritten to declare another id, or removed entirely. The window is real: an
// adapter, an editor, or a concurrent command can rewrite it between the
// snapshot and the dispatch, and the bulk loops resolve every owner up front.
func TestInvokeWithRoot_SpecChangedAfterResolution_RefusesBeforeCreatingAnyState(t *testing.T) {
	skipIfShort(t)
	t.Run("rewritten to a foreign identity", func(t *testing.T) {
		root, cfg, resolved, marker := boundaryProject(t, "execute")
		owner := seedOwnerForTest(t, root, "tc-c1c1c1c1")

		require.NoError(t, os.WriteFile(owner.SpecAbs(),
			[]byte("---\ntest_case_id: tc-d2d2d2d2\ntitle: rewritten\n---\n"), 0o644))

		_, err := InvokeWithRoot(context.Background(), root, cfg, resolved,
			"tc-c1c1c1c1", CommandFlags{Owner: owner})

		require.Error(t, err)
		var coe *CarriedOwnerError
		require.ErrorAs(t, err, &coe)
		assert.Contains(t, coe.Error(), "no longer declares that identity")
		assertNoStateCreated(t, root, marker)
	})

	t.Run("removed", func(t *testing.T) {
		root, cfg, resolved, marker := boundaryProject(t, "automate")
		owner := seedOwnerForTest(t, root, "tc-e3e3e3e3")
		require.NoError(t, os.Remove(owner.SpecAbs()))

		_, err := InvokeWithRoot(context.Background(), root, cfg, resolved,
			"tc-e3e3e3e3", CommandFlags{Owner: owner})

		require.Error(t, err)
		var coe *CarriedOwnerError
		require.ErrorAs(t, err, &coe)
		assertNoStateCreated(t, root, marker)
	})
}

// The positive control: a correctly carried, still-valid owner passes the gate
// and the adapter actually runs. Without this the three refusals above would be
// satisfied by a boundary that refuses everything.
func TestInvokeWithRoot_CorrectOwner_StillInvokes(t *testing.T) {
	skipIfShort(t)
	root, cfg, resolved, marker := boundaryProject(t, "execute")
	owner := seedOwnerForTest(t, root, "tc-f4f4f4f4")

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved,
		"tc-f4f4f4f4", CommandFlags{Owner: owner})

	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)
	_, statErr := os.Stat(marker)
	assert.NoError(t, statErr, "a valid owner must still reach the adapter")
}

// create stays exempt BY DESIGN: it is folder-scoped because no test case exists
// yet (ADR-024 s1), so there is no owner for it to carry. If this ever starts
// failing, the gate has been widened past its warrant.
func TestInvokeWithRoot_CreateIsExemptFromTheOwnerGate(t *testing.T) {
	skipIfShort(t)
	root, cfg, resolved, marker := boundaryProject(t, "create")

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved,
		"REQ-100", CommandFlags{Folder: "checkout"})

	require.NoError(t, err, "create must not require an owner")
	assert.Equal(t, "complete", res.Status)
	_, statErr := os.Stat(marker)
	assert.NoError(t, statErr)
}

// The refusal renders through the central identity sanitiser (ADR-024 s7): both
// ids in it are untrusted -- one from the command line, one from a spec on disk.
func TestCarriedOwnerError_SanitisesTheTarget(t *testing.T) {
	e := &CarriedOwnerError{Command: "execute", Target: "tc-aaaa\x1b[2Jbbbb", Reason: "r"}
	assert.NotContains(t, e.Error(), "\x1b")
	assert.Contains(t, e.Error(), "tc-aaaa?[2Jbbbb")
	assert.True(t, errors.As(error(e), new(*CarriedOwnerError)))
}
