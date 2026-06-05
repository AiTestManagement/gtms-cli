package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// captureStderr redirects os.Stderr to a pipe for the duration of fn and
// returns whatever was written. Used by the bulk-execute tests to assert
// which TCs the loop actually printed progress for (a stronger signal
// than just absence of result files when drift would also suppress them).
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = w

	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	fn()

	require.NoError(t, w.Close())
	os.Stderr = orig
	return <-done
}

// withExecuteGlobals overrides the package-level projectRoot and appConfig
// used by execute's bulk path, then returns a restore function. Mirrors
// withGapsGlobals from gaps_test.go.
func withExecuteGlobals(t *testing.T, root string, cfg *config.Config) func() {
	t.Helper()
	savedRoot := projectRoot
	savedCfg := appConfig
	projectRoot = root
	appConfig = cfg
	return func() {
		projectRoot = savedRoot
		appConfig = savedCfg
	}
}

// batsExecuteConfig is the smallest *config.Config needed for runBulkExecute
// to resolve a bats execute adapter for tampered-wiring tests.
func batsExecuteConfig() *config.Config {
	return &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"bats-runner": {
					Framework: "bats", Mode: "sync",
					Command: "echo PASS",
				},
			},
		},
		Defaults: map[string]string{"execute": "bats-runner"},
	}
}

// writeTamperedWiring writes a wiring file with an arbitrary (possibly
// unsafe) artefact path, bypassing the link/automate writers that would
// reject it. Models a checked-in or hand-edited tampered wiring file.
func writeTamperedWiring(t *testing.T, root, tc, framework, adapter, artefact string) {
	t.Helper()
	rec := &wiring.WiringRecord{
		TestCase:     tc,
		TestCaseHash: "deadbeefdeadbeef",
		Framework:    framework,
		Adapter:      adapter,
		Artefact:     artefact,
		ArtefactHash: "deadbeefdeadbeef",
	}
	_, err := wiring.Write(root, rec)
	require.NoError(t, err)
}

// BUG-057: bulk execute must report tampered-wiring artefact paths as an
// execution error (not a skip) and leave no task/result state behind.
func TestRunBulkExecute_TamperedWiring_RelativeTraversal_Errored(t *testing.T) {
	root := t.TempDir()

	// Test case spec under a single-TC folder.
	writeTestFile(t, root, filepath.Join("gtms/cases", "pathsafe", "tc-deadbeef-unsafe.md"), `---
test_case_id: tc-deadbeef
title: Path-safety regression
---
`)

	// Tampered wiring with a relative-traversal artefact escaping the project root.
	writeTamperedWiring(t, root, "tc-deadbeef", "bats", "bats-runner",
		"../traversal-target.txt")

	restore := withExecuteGlobals(t, root, batsExecuteConfig())
	defer restore()

	cmd := &cobra.Command{Use: "execute"}
	cmd.SetContext(context.Background())

	err := runBulkExecute(cmd, root, "pathsafe",
		/* adapterFlag */ "",
		/* environmentFlag */ "",
		/* executedBy */ "",
		/* frameworkFlag */ "",
		/* force */ false,
		/* failFast */ false,
		/* recursive */ false,
		/* allowStale */ false,
	)
	require.Error(t, err, "bulk execute must return a non-zero status when path-safety errored")

	// No task file should be created for a path-safety reject.
	tasks, _ := filepath.Glob(filepath.Join(root, "gtms", "tasks", "*", "task-*.md"))
	assert.Empty(t, tasks, "no task file should be created on path-safety reject")

	// No result handoff should exist.
	results, _ := filepath.Glob(filepath.Join(root, ".gtms", "results", "*.handoff.yaml"))
	assert.Empty(t, results, "no result handoff should be created on path-safety reject")

	// No per-test results file should exist.
	exec, _ := filepath.Glob(filepath.Join(root, "gtms", "execution", "*.results.yaml"))
	assert.Empty(t, exec, "no execution results file should be created on path-safety reject")
}

// BUG-057: bulk execute with --fail-fast must halt the batch on a
// path-safety failure, not skip past it.
//
// The "safe" second TC is fully wired (real testcase-hash + artefact-hash
// computed via pipeline.HashFile, real on-disk artefact, real execute
// adapter command). If --fail-fast were broken and the loop reached the
// second TC, drift would pass, the bats-runner mock adapter would run,
// and a terminal handoff / execution results file would be observable.
// The test asserts both negative state (no result files) and absence of
// the second TC in captured stderr output for double coverage.
func TestRunBulkExecute_TamperedWiring_FailFast_HaltsBatch(t *testing.T) {
	root := t.TempDir()

	// Two TCs: first one is unsafe, second one is safe. With --fail-fast,
	// the safe TC must never run.
	specA := filepath.Join("gtms/cases", "pathsafe-ff", "tc-11111111-unsafe.md")
	writeTestFile(t, root, specA, `---
test_case_id: tc-11111111
title: Unsafe wiring (first)
---
`)
	specB := filepath.Join("gtms/cases", "pathsafe-ff", "tc-22222222-safe.md")
	writeTestFile(t, root, specB, `---
test_case_id: tc-22222222
title: Safe wiring (second)
---
`)

	// Unsafe wiring on the first TC.
	writeTamperedWiring(t, root, "tc-11111111", "bats", "bats-runner",
		"../traversal-ff-target.txt")

	// Safe wiring on the second TC with a real on-disk artefact AND
	// real hashes computed the same way the production drift check
	// computes them. If --fail-fast were broken, drift would pass and
	// the adapter would run, producing observable result files.
	safeArtefactRel := "test/acceptance/tc-22222222.bats"
	safeArtefact := filepath.Join(root, filepath.FromSlash(safeArtefactRel))
	require.NoError(t, os.MkdirAll(filepath.Dir(safeArtefact), 0755))
	require.NoError(t, os.WriteFile(safeArtefact, []byte("# bats artefact"), 0644))

	tcHash, err := pipeline.HashFile(filepath.Join(root, filepath.FromSlash(specB)))
	require.NoError(t, err)
	artHash, err := pipeline.HashFile(safeArtefact)
	require.NoError(t, err)
	_, err = wiring.Write(root, &wiring.WiringRecord{
		TestCase:     "tc-22222222",
		TestCaseHash: tcHash,
		Framework:    "bats",
		Adapter:      "bats-runner",
		Artefact:     safeArtefactRel,
		ArtefactHash: artHash,
	})
	require.NoError(t, err)

	restore := withExecuteGlobals(t, root, batsExecuteConfig())
	defer restore()

	cmd := &cobra.Command{Use: "execute"}
	cmd.SetContext(context.Background())

	var runErr error
	stderr := captureStderr(t, func() {
		runErr = runBulkExecute(cmd, root, "pathsafe-ff",
			"", "", "", "",
			false, /* force */
			true,  /* failFast */
			false, /* recursive */
			false, /* allowStale */
		)
	})
	require.Error(t, runErr, "bulk execute must return non-zero with errored>0 under --fail-fast")

	// Primary signal: the bulk-loop progress line for the second TC must
	// never appear in stderr — that line is only printed if the loop
	// actually processes that TC. The unsafe TC's error line is fine.
	assert.NotContains(t, stderr, "tc-22222222",
		"--fail-fast must abort the batch before processing the second TC")
	assert.Contains(t, stderr, "tc-11111111",
		"the first (unsafe) TC must still be reported")

	// Belt-and-braces: even if the loop reached the safe TC, the real
	// hashes mean drift would pass and the adapter would run. No
	// result handoff / execution file should exist on disk.
	results, _ := filepath.Glob(filepath.Join(root, ".gtms", "results", "*.handoff.yaml"))
	assert.Empty(t, results, "--fail-fast must halt the batch before any TC runs the adapter")

	exec, _ := filepath.Glob(filepath.Join(root, "gtms", "execution", "*.results.yaml"))
	assert.Empty(t, exec, "no execution results file should be created when --fail-fast halts on path-safety reject")

	// Guard the captureStderr / mock layout: confirm the captured
	// stderr is non-empty so we know the redirect worked and the
	// NotContains assertion is meaningful.
	require.NotEmpty(t, strings.TrimSpace(stderr), "stderr capture must not be empty (sanity check)")
}
