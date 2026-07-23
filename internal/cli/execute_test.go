package cli

import (
	"context"
	"fmt"
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
	writeTestFile(t, root, filepath.Join("gtms/test/cases", "pathsafe", "tc-deadbeef-unsafe.md"), `---
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
	specA := filepath.Join("gtms/test/cases", "pathsafe-ff", "tc-11111111-unsafe.md")
	writeTestFile(t, root, specA, `---
test_case_id: tc-11111111
title: Unsafe wiring (first)
---
`)
	specB := filepath.Join("gtms/test/cases", "pathsafe-ff", "tc-22222222-safe.md")
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

// ENH-163: isMode3ExecuteAdapterName must recognise all four Mode 3 names.
func TestIsMode3ExecuteAdapterName(t *testing.T) {
	// Positive cases: all four Mode 3 names.
	for _, name := range []string{
		"manual-execute",
		"agent-execute",
		"manual-execute-script",
		"agent-execute-script",
	} {
		assert.True(t, isMode3ExecuteAdapterName(name), "%s must be recognised as Mode 3", name)
	}

	// Negative cases.
	for _, name := range []string{
		"bats-runner",
		"playwright-runner",
		"local-runner",
		"manual-create",
		"agent-automate",
		"manual-prime",
		"",
	} {
		assert.False(t, isMode3ExecuteAdapterName(name), "%q must NOT be recognised as Mode 3", name)
	}
}

// ENH-163: bulk execute with defaults.execute=manual-execute on a folder
// of unwired TCs must bypass wiring (no "not wired" skips). The adapter
// itself may error (no git repo, etc.) but the critical assertion is that
// it attempted to run rather than skipping with "not wired".
func TestRunBulkExecute_Mode3DefaultBypassesWiring(t *testing.T) {
	skipIfShort(t)

	root := t.TempDir()

	// Two test case specs, neither with wiring records.
	writeTestFile(t, root, filepath.Join("gtms/test/cases", "manual-folder", "tc-aaaaaaaa-first.md"), `---
test_case_id: tc-aaaaaaaa
title: First manual TC
---
`)
	writeTestFile(t, root, filepath.Join("gtms/test/cases", "manual-folder", "tc-bbbbbbbb-second.md"), `---
test_case_id: tc-bbbbbbbb
title: Second manual TC
---
`)

	// Prime result files so manual-execute has something to read.
	manualDir := filepath.Join(root, "gtms", "manual", "records")
	require.NoError(t, os.MkdirAll(manualDir, 0755))
	for _, tc := range []string{"tc-aaaaaaaa", "tc-bbbbbbbb"} {
		content := fmt.Sprintf("test_case_id: %s\nframework: manual\nresult: pass\ntest_case_hash: deadbeef\n", tc)
		require.NoError(t, os.WriteFile(
			filepath.Join(manualDir, tc+"--manual.result.yaml"),
			[]byte(content), 0644))
	}

	// Config with defaults.execute = manual-execute (Mode 3 default).
	cfg := &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{},
		Defaults: map[string]string{"execute": "manual-execute"},
	}

	restore := withExecuteGlobals(t, root, cfg)
	defer restore()

	cmd := &cobra.Command{Use: "execute"}
	cmd.SetContext(context.Background())

	stderr := captureStderr(t, func() {
		_ = runBulkExecute(cmd, root, "manual-folder",
			"", "", "test-user", "",
			false, false, false, false,
		)
	})

	// Primary assertion: the Mode 3 bypass must prevent "not wired" skips.
	// The adapter may error for other reasons (no git init, etc.) but the
	// wiring gate must NOT be the cause.
	assert.NotContains(t, stderr, "not wired",
		"Mode 3 default must bypass wiring -- no 'not wired' skips expected")
	// Both TCs should appear in stderr (they were attempted, not skipped).
	assert.Contains(t, stderr, "tc-aaaaaaaa", "first TC must be processed")
	assert.Contains(t, stderr, "tc-bbbbbbbb", "second TC must be processed")
}

// ENH-163: bulk execute with defaults.execute=bats-runner on a folder
// with a mix of wired and unwired TCs must skip the unwired ones.
func TestRunBulkExecute_NonMode3DefaultKeepsWiringSkips(t *testing.T) {
	skipIfShort(t)

	root := t.TempDir()

	// One wired TC, one unwired TC.
	specWired := filepath.Join("gtms/test/cases", "mixed-folder", "tc-cccccccc-wired.md")
	writeTestFile(t, root, specWired, `---
test_case_id: tc-cccccccc
title: Wired TC
---
`)
	writeTestFile(t, root, filepath.Join("gtms/test/cases", "mixed-folder", "tc-dddddddd-unwired.md"), `---
test_case_id: tc-dddddddd
title: Unwired TC
---
`)

	// Write wiring + artefact for the wired TC.
	artefactRel := "test/acceptance/tc-cccccccc.bats"
	artefactAbs := filepath.Join(root, filepath.FromSlash(artefactRel))
	require.NoError(t, os.MkdirAll(filepath.Dir(artefactAbs), 0755))
	require.NoError(t, os.WriteFile(artefactAbs, []byte("# bats"), 0644))

	tcHash, err := pipeline.HashFile(filepath.Join(root, filepath.FromSlash(specWired)))
	require.NoError(t, err)
	artHash, err := pipeline.HashFile(artefactAbs)
	require.NoError(t, err)
	_, err = wiring.Write(root, &wiring.WiringRecord{
		TestCase:     "tc-cccccccc",
		TestCaseHash: tcHash,
		Framework:    "bats",
		Adapter:      "bats-runner",
		Artefact:     artefactRel,
		ArtefactHash: artHash,
	})
	require.NoError(t, err)

	cfg := &config.Config{
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

	restore := withExecuteGlobals(t, root, cfg)
	defer restore()

	cmd := &cobra.Command{Use: "execute"}
	cmd.SetContext(context.Background())

	var runErr error
	stderr := captureStderr(t, func() {
		runErr = runBulkExecute(cmd, root, "mixed-folder",
			"", "", "test-user", "",
			false, false, false, false,
		)
	})

	// The unwired TC must be skipped.
	_ = runErr // may error due to skip count
	assert.Contains(t, stderr, "tc-dddddddd",
		"unwired TC must appear in stderr")
	assert.Contains(t, stderr, "not wired",
		"unwired TC must be skipped with 'not wired' reason")
}

// BUG-165 decisive regression: dispatch-level proof that Mode 3 bypass
// ignores a stale/synthetic wiring record. The test seeds a wiring record
// naming "manual-execute-script" as the adapter, sets defaults.execute to
// "agent-execute-script", and runs bare execute (no --adapter flag). The
// Mode 3 bypass must fire because the default is a Mode 3 name, so the
// adapter that ACTUALLY RUNS is agent-execute-script (the default), NOT
// manual-execute-script (the wiring). This is NOT a resolver-level test --
// it exercises the CLI dispatch path in internal/cli/execute.go.
func TestExecuteDispatch_Mode3BypassesStaleWiring(t *testing.T) {
	skipIfShort(t)

	root := t.TempDir()

	// TC spec.
	writeTestFile(t, root, filepath.Join("gtms/test/cases", "m3bypass", "tc-b165a001-dispatch.md"), `---
test_case_id: tc-b165a001
title: BUG-165 dispatch regression
---
`)

	// Seed a synthetic wiring record naming manual-execute-script as the
	// adapter. Under the bug (interim narrowing), canonical resolution
	// could have picked this name. Under Option A, wiring is irrelevant
	// because the Mode 3 default bypasses wiring entirely.
	writeTamperedWiring(t, root, "tc-b165a001", "manual", "manual-execute-script",
		"gtms/manual/records/tc-b165a001--manual.result.yaml")

	// Prime a manual result file so the built-in manual-execute path has
	// something to read (the Mode 3 adapter will attempt to read it).
	manualDir := filepath.Join(root, "gtms", "manual", "records")
	require.NoError(t, os.MkdirAll(manualDir, 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(manualDir, "tc-b165a001--manual.result.yaml"),
		[]byte("test_case_id: tc-b165a001\nframework: manual\nresult: pass\ntest_case_hash: deadbeef\n"),
		0644))

	// Config: defaults.execute = agent-execute-script (Mode 3 name).
	// Both -script variants registered under adapters.execute so Resolve
	// can find them.
	cfg := &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"agent-execute-script": {
					Framework: "manual", Mode: "sync",
					Script: "echo ok",
				},
				"manual-execute-script": {
					Framework: "manual", Mode: "sync",
					Script: "echo ok",
				},
			},
		},
		Defaults: map[string]string{"execute": "agent-execute-script"},
	}

	restore := withExecuteGlobals(t, root, cfg)
	defer restore()

	cmd := &cobra.Command{Use: "execute"}
	cmd.SetContext(context.Background())

	stderr := captureStderr(t, func() {
		_ = runBulkExecute(cmd, root, "m3bypass",
			/* adapterFlag */ "",
			/* environmentFlag */ "",
			/* executedBy */ "test-user",
			/* frameworkFlag */ "",
			/* force */ false,
			/* failFast */ false,
			/* recursive */ false,
			/* allowStale */ false,
		)
	})

	// Primary assertion 1: wiring bypass must fire -- no "not wired" skip.
	assert.NotContains(t, stderr, "not wired",
		"Mode 3 default must bypass wiring -- no 'not wired' skips expected")

	// Primary assertion 2: the TC must be processed (attempted, not skipped).
	assert.Contains(t, stderr, "tc-b165a001",
		"TC must appear in stderr (was processed, not skipped)")

	// Primary assertion 3: the adapter that ran is agent-execute-script
	// (the Mode 3 default), NOT manual-execute-script (the wiring record).
	// Check the result contract on disk: InvokeWithRoot stamps the adapter
	// name in the handoff before the adapter is invoked, so even if the
	// adapter errors, the handoff exists with the correct adapter field.
	handoffs, _ := filepath.Glob(filepath.Join(root, ".gtms", "results", "*.handoff.yaml"))
	require.NotEmpty(t, handoffs, "at least one handoff must be written (adapter was invoked)")

	// Read the handoff and assert the adapter field.
	for _, hf := range handoffs {
		content, err := os.ReadFile(hf)
		require.NoError(t, err)
		body := string(content)
		if !strings.Contains(body, "tc-b165a001") {
			continue // not our TC
		}
		assert.Contains(t, body, "adapter: agent-execute-script",
			"the adapter that ran must be agent-execute-script (the Mode 3 default), not manual-execute-script (the wiring)")
		assert.NotContains(t, body, "adapter: manual-execute-script",
			"wiring record's adapter must NOT be used -- Mode 3 bypass must ignore it")
	}
}
