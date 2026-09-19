package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// REV-159 CODEX-001 -- execute invoked adapters when no owner existed.
//
// resolveTargetOwner's ErrOwnerNotFound was explicitly ignored, on the reasoning
// that a missing spec would surface through the wiring lookup. That is not true
// on every branch: a Mode-3 execute adapter (manual-execute, agent-execute, ...)
// BYPASSES wiring lookup entirely, so an orphan id reached adapter resolution
// with a nil Owner, created task and result state, and invoked external code for
// a test case that does not exist.
//
// ADR-024's consumer matrix is explicit: Automate / execute / prime refuse before
// mutation or invocation on Not found. Automate and prime already did.
//
// The discriminator is not the error alone -- it is that NOTHING was created and
// NOTHING was invoked. Those are the effects the finding is about.

// countFilesUnder returns how many regular files exist under dir (0 when absent).
func countFilesUnder(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil //nolint:nilerr // absent tree counts as zero
		}
		n++
		return nil
	})
	return n
}

// runExecuteInRoot drives the real cobra command from root, as a user would.
func runExecuteInRoot(t *testing.T, root string, args ...string) (string, error) {
	t.Helper()
	savedRoot, savedCfg := projectRoot, appConfig
	defer func() { projectRoot, appConfig = savedRoot, savedCfg }()

	savedWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(root))
	defer func() { _ = os.Chdir(savedWd) }()

	var execErr error
	out := captureStderr(t, func() {
		rootCmd.SetArgs(args)
		execErr = ExecuteContext(context.Background())
	})
	return out, execErr
}

// The Mode-3 branch, which bypasses wiring lookup and so had no second line of
// defence at all.
func TestExecute_OrphanIDCreatesNothingAndInvokesNothing_Mode3(t *testing.T) {
	skipIfShort(t)
	root := setupCompatProject(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "gtms", ".gtms-root"), []byte(""), 0o644))

	// manual-execute-script is a Mode-3 name that runs a REAL external script, so
	// the marker below actually discriminates. (The plain manual-execute adapter is
	// dispatched internally by GTMS and never runs a configured script, which would
	// have made this assertion vacuous -- verified before relying on it.)
	marker := filepath.Join(root, "adapter-was-invoked.txt")
	writeMockScript(t, root, "mock-me.sh", "#!/bin/bash\ntouch '"+filepath.ToSlash(marker)+"'\n")
	require.NoError(t, os.WriteFile(filepath.Join(root, "gtms.config"), []byte(
		"project:\n  name: orphan-test\n  repo: .\n"+
			"adapters:\n  execute:\n    manual-execute-script:\n      mode: sync\n      script: ./testdata/mock-me.sh\n"+
			"defaults:\n  execute: manual-execute-script\n"), 0o644))

	tasksBefore := countFilesUnder(t, filepath.Join(root, "gtms", "tasks"))
	resultsBefore := countFilesUnder(t, filepath.Join(root, ".gtms", "results"))

	// tc-0f0f0f0f has no spec anywhere in the project.
	stderr, err := runExecuteInRoot(t, root, "execute", "tc-0f0f0f0f", "--adapter", "manual-execute-script")

	// assert, not require: under the defect every one of the four effects below is
	// worth reporting, and aborting at the first hides the three that matter most.
	assert.Error(t, err, "execute proceeded for an id with no physical owner: a Mode-3 adapter bypasses wiring lookup, so nothing else refuses it")
	assert.Contains(t, strings.ToLower(stderr), "not found",
		"the refusal must say the test case does not exist")

	assert.NoFileExists(t, marker,
		"the execute adapter was INVOKED for a test case that does not exist -- ADR-024 requires a uniquely proven owner before external invocation")
	assert.Equal(t, tasksBefore, countFilesUnder(t, filepath.Join(root, "gtms", "tasks")),
		"a task file was created for an orphan id")
	assert.Equal(t, resultsBefore, countFilesUnder(t, filepath.Join(root, ".gtms", "results")),
		"a result contract was created for an orphan id")
}

// The wiring branch. Even here the nil owner was not harmless: checkWiringDrift
// skips the testcase-hash comparison when it has no spec to hash, so the drift
// preflight silently degraded to artefact-hash only.
func TestExecute_OrphanIDRefusedOnTheWiringBranchToo(t *testing.T) {
	skipIfShort(t)
	root := setupCompatProject(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "gtms", ".gtms-root"), []byte(""), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "gtms.config"), []byte(
		"project:\n  name: orphan-test\n  repo: .\n"+
			"adapters:\n  execute:\n    bats-runner:\n      mode: sync\n      command: \"true\"\n      framework: bats\n"), 0o644))

	stderr, err := runExecuteInRoot(t, root, "execute", "tc-1e1e1e1e", "--framework", "bats")

	require.Error(t, err, "execute must refuse an id with no physical owner before any wiring or adapter work")
	assert.Contains(t, strings.ToLower(stderr), "not found")
	assert.Equal(t, 0, countFilesUnder(t, filepath.Join(root, "gtms", "tasks")),
		"no task file may be created for an orphan id")
}
