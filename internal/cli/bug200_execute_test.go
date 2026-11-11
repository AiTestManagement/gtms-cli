package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// BUG-200: a bulk gtms execute that refuses to run any test case must report
// those test cases as "not run" (counted apart from skipped/failed/errored) and
// exit non-zero; normal skips still exit 0. These Go-level tests mirror the
// canonical specs in gtms/test/cases/bulk-execute-refusal-exit-code/. BATS
// coverage is authored separately.

// bug200Config provides bats-runner (default), a second same-framework
// bats adapter (alt-bats-runner) for override tests, and a playwright-runner
// for the multi-framework test. All are Tier 1 "echo PASS" adapters.
func bug200Config() *config.Config {
	return &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"bats-runner":       {Framework: "bats", Mode: "sync", Command: "echo PASS"},
				"alt-bats-runner":   {Framework: "bats", Mode: "sync", Command: "echo PASS"},
				"playwright-runner": {Framework: "playwright", Mode: "sync", Command: "echo PASS"},
			},
		},
		Defaults: map[string]string{"execute": "bats-runner"},
	}
}

// bug200Capture runs fn with os.Stdout and os.Stderr redirected to pipes and
// returns their combined content. The per-TC lines go to stderr and the
// summary line goes to stdout, so both are needed to assert the full outcome.
func bug200Capture(t *testing.T, fn func()) string {
	t.Helper()
	origOut, origErr := os.Stdout, os.Stderr
	rOut, wOut, err := os.Pipe()
	require.NoError(t, err)
	rErr, wErr, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout, os.Stderr = wOut, wErr

	ch := make(chan string, 2)
	go func() { b, _ := io.ReadAll(rOut); ch <- string(b) }()
	go func() { b, _ := io.ReadAll(rErr); ch <- string(b) }()

	fn()

	require.NoError(t, wOut.Close())
	require.NoError(t, wErr.Close())
	os.Stdout, os.Stderr = origOut, origErr
	return <-ch + "\n" + <-ch
}

// bug200Bulk runs the bulk execute path with full flag control and returns the
// combined stdout+stderr and the RunE error (nil == exit 0).
func bug200Bulk(t *testing.T, root, folder, framework string, cfg *config.Config, force, failFast, allowStale bool) (string, error) {
	t.Helper()
	restore := withExecuteGlobals(t, root, cfg)
	defer restore()
	cmd := &cobra.Command{Use: "execute"}
	cmd.SetContext(context.Background())
	var err error
	out := bug200Capture(t, func() {
		err = runBulkExecute(cmd, root, folder, "", "", "", framework, force, failFast, false, allowStale)
	})
	return out, err
}

// bug200BulkAdapter runs the bulk execute path with an --adapter override
// (and optional --framework), always with --force.
func bug200BulkAdapter(t *testing.T, root, folder, adapterFlag, framework string, cfg *config.Config, failFast bool) (string, error) {
	t.Helper()
	restore := withExecuteGlobals(t, root, cfg)
	defer restore()
	cmd := &cobra.Command{Use: "execute"}
	cmd.SetContext(context.Background())
	var err error
	out := bug200Capture(t, func() {
		err = runBulkExecute(cmd, root, folder, adapterFlag, "", "", framework, true, failFast, false, false)
	})
	return out, err
}

// bug200EditSpec appends a line to a seeded spec so its testcase-hash drifts.
func bug200EditSpec(t *testing.T, root, folder, tcID string) {
	t.Helper()
	p := filepath.Join(root, "gtms", "test", "cases", folder, tcID+"-x.md")
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0644)
	require.NoError(t, err)
	_, _ = f.WriteString("\nExtra step added for BUG-200.\n")
	require.NoError(t, f.Close())
}

func bug200ArtefactPath(root, tcID string) string {
	return filepath.Join(root, "test", "acceptance", tcID+".bats")
}

const bug200Folder = "bug200-bulk"

// --- Core fix: a refused TC is not run and exits non-zero ---

func TestBUG200_SpecDriftNotRunExitsNonZero(t *testing.T) {
	skipIfShort(t)
	root := t.TempDir()
	enh231Spec(t, root, bug200Folder, "tc-11111111")
	enh231Spec(t, root, bug200Folder, "tc-22222222")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-11111111", Framework: "bats", Adapter: "bats-runner"})
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-22222222", Framework: "bats", Adapter: "bats-runner"})

	// Refuse tc-11111111 via spec drift; tc-22222222 stays healthy.
	bug200EditSpec(t, root, bug200Folder, "tc-11111111")
	before := countHandoffs(t, root)

	out, err := bug200Bulk(t, root, bug200Folder, "", bug200Config(), false, false, false)
	require.Error(t, err, "a bulk refusal must exit non-zero")
	assert.Contains(t, out, "tc-11111111")
	assert.Contains(t, out, "not run")
	assert.Contains(t, out, "1 passed, 0 skipped, 1 not run, 0 failed, 0 errored")
	// The healthy TC ran exactly once; the refused one produced no handoff.
	assert.Equal(t, before+1, countHandoffs(t, root), "only the healthy TC may run")
}

// --- Missing artefact: not run in every hash/flag state ---

func TestBUG200_MissingArtefactNotRun(t *testing.T) {
	skipIfShort(t)
	cases := []struct {
		name       string
		pending    bool
		allowStale bool
	}{
		{"real hash", false, false},
		{"real hash --allow-stale", false, true},
		{"pending hash", true, false},
		{"pending hash --allow-stale", true, true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			skipIfShort(t)
			root := t.TempDir()
			enh231Spec(t, root, bug200Folder, "tc-11111111")
			enh231Spec(t, root, bug200Folder, "tc-22222222")
			seedLegacyRecord(t, root, legacyRecord{TC: "tc-22222222", Framework: "bats", Adapter: "bats-runner"})

			hash := ""
			if tc.pending {
				hash = wiring.PendingArtefactHash
			}
			seedLegacyRecord(t, root, legacyRecord{
				TC: "tc-11111111", Framework: "bats", Adapter: "bats-runner",
				ArtefactHash: hash,
			})
			require.NoError(t, os.Remove(bug200ArtefactPath(root, "tc-11111111")))
			before := countHandoffs(t, root)

			out, err := bug200Bulk(t, root, bug200Folder, "", bug200Config(), true, false, tc.allowStale)
			require.Error(t, err, "a missing artefact must exit non-zero")
			assert.Contains(t, out, "not run (script missing)")
			assert.Equal(t, before+1, countHandoffs(t, root), "only the healthy TC may run; --allow-stale must not run a missing artefact")

			// The pending record stays pending.
			if tc.pending {
				rec, _, _ := wiring.Find(root, "tc-11111111", "bats")
				require.NotNil(t, rec)
				assert.Equal(t, wiring.PendingArtefactHash, rec.ArtefactHash)
			}
		})
	}
}

// --- Unreadable artefact (a directory at the path): not run ---

func TestBUG200_UnreadableArtefactNotRun(t *testing.T) {
	skipIfShort(t)
	for _, allowStale := range []bool{false, true} {
		allowStale := allowStale
		name := "default"
		if allowStale {
			name = "--allow-stale"
		}
		t.Run(name, func(t *testing.T) {
			skipIfShort(t)
			root := t.TempDir()
			enh231Spec(t, root, bug200Folder, "tc-11111111")
			enh231Spec(t, root, bug200Folder, "tc-22222222")
			seedLegacyRecord(t, root, legacyRecord{TC: "tc-11111111", Framework: "bats", Adapter: "bats-runner"})
			seedLegacyRecord(t, root, legacyRecord{TC: "tc-22222222", Framework: "bats", Adapter: "bats-runner"})

			// Replace the artefact file with a directory at the same path.
			art := bug200ArtefactPath(root, "tc-11111111")
			require.NoError(t, os.Remove(art))
			require.NoError(t, os.MkdirAll(art, 0755))
			before := countHandoffs(t, root)

			out, err := bug200Bulk(t, root, bug200Folder, "", bug200Config(), true, false, allowStale)
			require.Error(t, err)
			assert.Contains(t, out, "not run (script unreadable)")
			assert.Equal(t, before+1, countHandoffs(t, root), "an unreadable artefact must not run, even with --allow-stale")
		})
	}
}

// --- Multiple frameworks, no --framework: not run ---

func TestBUG200_MultipleFrameworksNotRun(t *testing.T) {
	skipIfShort(t)
	root := t.TempDir()
	enh231Spec(t, root, bug200Folder, "tc-11111111")
	enh231Spec(t, root, bug200Folder, "tc-22222222")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-11111111", Framework: "bats", Adapter: "bats-runner", Artefact: "test/acceptance/a.bats"})
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-11111111", Framework: "playwright", Adapter: "playwright-runner", Artefact: "test/acceptance/b.spec.ts"})
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-22222222", Framework: "bats", Adapter: "bats-runner"})
	before := countHandoffs(t, root)

	out, err := bug200Bulk(t, root, bug200Folder, "", bug200Config(), true, false, false)
	require.Error(t, err)
	assert.Contains(t, out, "not run")
	assert.Contains(t, out, "1 passed, 0 skipped, 1 not run, 0 failed, 0 errored")
	assert.Equal(t, before+1, countHandoffs(t, root), "only the healthy TC may run")
}

// --- Malformed applicable wiring: error on every selection path ---

func writeMalformedWiring(t *testing.T, root, name string) {
	t.Helper()
	dir := filepath.Join(root, "gtms", "automation", "wiring")
	require.NoError(t, os.MkdirAll(dir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("this is: [not valid yaml"), 0644))
}

func TestBUG200_MalformedApplicableWiringErrors(t *testing.T) {
	skipIfShort(t)

	t.Run("explicit framework", func(t *testing.T) {
		skipIfShort(t)
		root := t.TempDir()
		enh231Spec(t, root, bug200Folder, "tc-11111111")
		enh231Spec(t, root, bug200Folder, "tc-22222222")
		seedLegacyRecord(t, root, legacyRecord{TC: "tc-22222222", Framework: "bats", Adapter: "bats-runner"})
		writeMalformedWiring(t, root, "tc-11111111--bats.wiring.yaml")

		out, err := bug200Bulk(t, root, bug200Folder, "bats", bug200Config(), true, false, false)
		require.Error(t, err)
		assert.Contains(t, out, "error")
		assert.Contains(t, out, "1 passed, 0 skipped, 0 not run, 0 failed, 1 errored")
	})

	t.Run("adapter override", func(t *testing.T) {
		skipIfShort(t)
		root := t.TempDir()
		enh231Spec(t, root, bug200Folder, "tc-11111111")
		enh231Spec(t, root, bug200Folder, "tc-22222222")
		seedLegacyRecord(t, root, legacyRecord{TC: "tc-22222222", Framework: "bats", Adapter: "bats-runner"})
		writeMalformedWiring(t, root, "tc-11111111--bats.wiring.yaml")

		// --adapter alt-bats-runner implies framework bats: the framework is
		// known, so the malformed exact file is an error.
		out, err := bug200BulkAdapter(t, root, bug200Folder, "alt-bats-runner", "", bug200Config(), false)
		require.Error(t, err)
		assert.Regexp(t, `tc-11111111\s+error`, out)
		assert.Contains(t, out, "1 passed, 0 skipped, 0 not run, 0 failed, 1 errored")
	})

	t.Run("discovery matches generic scanner", func(t *testing.T) {
		skipIfShort(t)
		root := t.TempDir()
		enh231Spec(t, root, bug200Folder, "tc-11111111")
		enh231Spec(t, root, bug200Folder, "tc-22222222")
		// A valid bats record AND a malformed playwright record for tc-11111111.
		seedLegacyRecord(t, root, legacyRecord{TC: "tc-11111111", Framework: "bats", Adapter: "bats-runner"})
		seedLegacyRecord(t, root, legacyRecord{TC: "tc-22222222", Framework: "bats", Adapter: "bats-runner"})
		writeMalformedWiring(t, root, "tc-11111111--playwright.wiring.yaml")
		before := countHandoffs(t, root)

		// No-framework discovery keeps today's generic-scanner behaviour
		// (BUG-200 scope cut 2026-10-25): the malformed file is warned about
		// and dropped, and the valid bats record runs. BUG-212 owns changing
		// this; update this test when it does.
		out, err := bug200Bulk(t, root, bug200Folder, "", bug200Config(), true, false, false)
		require.NoError(t, err, out)
		assert.Contains(t, out, "warning: skipping wiring file")
		assert.Contains(t, out, "2 passed, 0 skipped, 0 not run, 0 failed, 0 errored")
		assert.Equal(t, before+2, countHandoffs(t, root))
	})

	t.Run("out-of-scope malformed ignored", func(t *testing.T) {
		skipIfShort(t)
		root := t.TempDir()
		enh231Spec(t, root, bug200Folder, "tc-11111111")
		seedLegacyRecord(t, root, legacyRecord{TC: "tc-11111111", Framework: "bats", Adapter: "bats-runner"})
		// Malformed wiring for a TC that is not in the run (no spec).
		writeMalformedWiring(t, root, "tc-99999999--bats.wiring.yaml")

		out, err := bug200Bulk(t, root, bug200Folder, "", bug200Config(), true, false, false)
		require.NoError(t, err, "an unrelated malformed file must not fail the run")
		assert.Contains(t, out, "1 passed, 0 skipped, 0 not run, 0 failed, 0 errored")
	})
}

// --- Malformed-wiring error stops --fail-fast; a not-run refusal does not ---

func TestBUG200_FailFast(t *testing.T) {
	skipIfShort(t)

	t.Run("error stops fail-fast", func(t *testing.T) {
		skipIfShort(t)
		root := t.TempDir()
		enh231Spec(t, root, bug200Folder, "tc-11111111")
		enh231Spec(t, root, bug200Folder, "tc-22222222")
		writeMalformedWiring(t, root, "tc-11111111--bats.wiring.yaml")
		seedLegacyRecord(t, root, legacyRecord{TC: "tc-22222222", Framework: "bats", Adapter: "bats-runner"})
		before := countHandoffs(t, root)

		_, err := bug200Bulk(t, root, bug200Folder, "bats", bug200Config(), true, true /*failFast*/, false)
		require.Error(t, err)
		assert.Equal(t, before, countHandoffs(t, root), "fail-fast must stop at the error, before tc-22222222 runs")
	})

	t.Run("adapter override error stops fail-fast", func(t *testing.T) {
		skipIfShort(t)
		root := t.TempDir()
		enh231Spec(t, root, bug200Folder, "tc-11111111")
		enh231Spec(t, root, bug200Folder, "tc-22222222")
		writeMalformedWiring(t, root, "tc-11111111--bats.wiring.yaml")
		seedLegacyRecord(t, root, legacyRecord{TC: "tc-22222222", Framework: "bats", Adapter: "bats-runner"})
		before := countHandoffs(t, root)

		_, err := bug200BulkAdapter(t, root, bug200Folder, "alt-bats-runner", "", bug200Config(), true /*failFast*/)
		require.Error(t, err)
		assert.Equal(t, before, countHandoffs(t, root), "fail-fast must stop at the error, before tc-22222222 runs")
	})

	t.Run("refusal does not stop fail-fast", func(t *testing.T) {
		skipIfShort(t)
		root := t.TempDir()
		enh231Spec(t, root, bug200Folder, "tc-11111111")
		enh231Spec(t, root, bug200Folder, "tc-22222222")
		seedLegacyRecord(t, root, legacyRecord{TC: "tc-11111111", Framework: "bats", Adapter: "bats-runner"})
		seedLegacyRecord(t, root, legacyRecord{TC: "tc-22222222", Framework: "bats", Adapter: "bats-runner"})
		bug200EditSpec(t, root, bug200Folder, "tc-11111111") // tc-11111111 refused, comes first
		before := countHandoffs(t, root)

		out, err := bug200Bulk(t, root, bug200Folder, "", bug200Config(), true, true /*failFast*/, false)
		require.Error(t, err, "a refusal still makes the run exit non-zero")
		assert.Contains(t, out, "not run")
		assert.Equal(t, before+1, countHandoffs(t, root), "the later healthy TC still runs after a not-run refusal")
	})
}

// --- --force cannot mask a refusal ---

func TestBUG200_ForceDoesNotMaskRefusal(t *testing.T) {
	skipIfShort(t)
	root := t.TempDir()
	enh231Spec(t, root, bug200Folder, "tc-11111111")
	enh231Spec(t, root, bug200Folder, "tc-22222222")
	// Both already passed, then both specs edited: both refused.
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-11111111", Framework: "bats", Adapter: "bats-runner", Result: "pass"})
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-22222222", Framework: "bats", Adapter: "bats-runner", Result: "pass"})
	bug200EditSpec(t, root, bug200Folder, "tc-11111111")
	bug200EditSpec(t, root, bug200Folder, "tc-22222222")
	before := countHandoffs(t, root)

	out, err := bug200Bulk(t, root, bug200Folder, "", bug200Config(), true /*force*/, false, false)
	require.Error(t, err, "--force must not turn a refusal into a successful exit")
	assert.Contains(t, out, "0 passed, 0 skipped, 2 not run, 0 failed, 0 errored")
	assert.Equal(t, before, countHandoffs(t, root), "no adapter may run")
}

// --- Normal skips alone still exit 0 ---

func TestBUG200_NormalSkipsExitZero(t *testing.T) {
	skipIfShort(t)
	root := t.TempDir()
	enh231Spec(t, root, bug200Folder, "tc-11111111") // not wired
	enh231Spec(t, root, bug200Folder, "tc-22222222") // already passing
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-22222222", Framework: "bats", Adapter: "bats-runner", Result: "pass"})

	out, err := bug200Bulk(t, root, bug200Folder, "", bug200Config(), false, false, false)
	require.NoError(t, err, "normal skips alone must exit 0")
	assert.Contains(t, out, "0 passed, 2 skipped, 0 not run, 0 failed, 0 errored")
	assert.NotContains(t, out, "not run (")
}

// --- Single-TC parity: --allow-stale never runs a missing/unreadable artefact ---

func TestBUG200_SingleAllowStaleRefusesMissingOrUnreadable(t *testing.T) {
	skipIfShort(t)

	t.Run("missing real hash", func(t *testing.T) {
		skipIfShort(t)
		root := initGitRepo(t)
		enh231Spec(t, root, bug200Folder, "tc-11111111")
		seedLegacyRecord(t, root, legacyRecord{TC: "tc-11111111", Framework: "bats", Adapter: "bats-runner"})
		require.NoError(t, os.Remove(bug200ArtefactPath(root, "tc-11111111")))

		out, err := runSingle(t, root, bug200Config(), "tc-11111111", "--framework", "bats", "--allow-stale")
		require.Error(t, err, "--allow-stale must not run a missing artefact")
		// ENH-222 Draft I: name the file; never a refresh hint.
		assert.Contains(t, out, "tc-11111111 was not run because its automation script is missing")
		assert.NotContains(t, out, "gtms link --refresh")
		assert.Equal(t, 0, countHandoffs(t, root), "the adapter must not be invoked")
		assert.Equal(t, 0, countExecuteTasks(t, root), "no execute task may be created")
	})

	t.Run("missing pending hash", func(t *testing.T) {
		skipIfShort(t)
		root := initGitRepo(t)
		enh231Spec(t, root, bug200Folder, "tc-11111111")
		seedLegacyRecord(t, root, legacyRecord{
			TC: "tc-11111111", Framework: "bats", Adapter: "bats-runner",
			ArtefactHash: wiring.PendingArtefactHash, SkipArtefactFile: true,
		})

		out, err := runSingle(t, root, bug200Config(), "tc-11111111", "--framework", "bats", "--allow-stale")
		require.Error(t, err)
		assert.Contains(t, out, "tc-11111111 was not run because its automation script is missing")
		assert.Equal(t, 0, countHandoffs(t, root))
		rec, _, _ := wiring.Find(root, "tc-11111111", "bats")
		require.NotNil(t, rec)
		assert.Equal(t, wiring.PendingArtefactHash, rec.ArtefactHash, "the pending record stays pending")
	})

	t.Run("unreadable directory", func(t *testing.T) {
		skipIfShort(t)
		root := initGitRepo(t)
		enh231Spec(t, root, bug200Folder, "tc-11111111")
		seedLegacyRecord(t, root, legacyRecord{TC: "tc-11111111", Framework: "bats", Adapter: "bats-runner"})
		art := bug200ArtefactPath(root, "tc-11111111")
		require.NoError(t, os.Remove(art))
		require.NoError(t, os.MkdirAll(art, 0755))

		out, err := runSingle(t, root, bug200Config(), "tc-11111111", "--framework", "bats", "--allow-stale")
		require.Error(t, err)
		assert.Contains(t, out, "tc-11111111 was not run because its automation script could not be read")
		assert.Equal(t, 0, countHandoffs(t, root))
	})
}

// --- Existing error controls stay errors (BUG-211 identity, unsafe path, unconfigured adapter) ---

func TestBUG200_ExistingErrorControlsUnchanged(t *testing.T) {
	skipIfShort(t)

	t.Run("identity mismatch", func(t *testing.T) {
		skipIfShort(t)
		root := t.TempDir()
		enh231Spec(t, root, bug200Folder, "tc-11111111")
		enh231Spec(t, root, bug200Folder, "tc-22222222")
		// Filename says bats, body says playwright.
		writeTamperedWiring(t, root, "tc-11111111", "bats", "bats-runner", "test/acceptance/x.bats")
		rec, path, _ := wiring.Find(root, "tc-11111111", "bats")
		require.NotNil(t, rec)
		rec.Framework = "playwright"
		require.NoError(t, os.WriteFile(path, []byte(
			"testcase: tc-11111111\ntestcase-hash: 0011223344556677\nframework: playwright\nadapter: bats-runner\nartefact: test/acceptance/x.bats\nartefact-hash: aabbccddeeff0011\n"), 0644))
		seedLegacyRecord(t, root, legacyRecord{TC: "tc-22222222", Framework: "bats", Adapter: "bats-runner"})

		out, err := bug200Bulk(t, root, bug200Folder, "bats", bug200Config(), true, false, false)
		require.Error(t, err)
		assert.Contains(t, out, "identity", "an identity mismatch stays an error with the identity token")
		assert.Contains(t, out, "1 errored")
	})

	t.Run("unsafe artefact path", func(t *testing.T) {
		skipIfShort(t)
		root := t.TempDir()
		enh231Spec(t, root, bug200Folder, "tc-11111111")
		enh231Spec(t, root, bug200Folder, "tc-22222222")
		writeTamperedWiring(t, root, "tc-11111111", "bats", "bats-runner", "../outside.sh")
		seedLegacyRecord(t, root, legacyRecord{TC: "tc-22222222", Framework: "bats", Adapter: "bats-runner"})

		out, err := bug200Bulk(t, root, bug200Folder, "bats", bug200Config(), true, false, false)
		require.Error(t, err)
		assert.Contains(t, out, "1 errored")
	})

	t.Run("unconfigured adapter", func(t *testing.T) {
		skipIfShort(t)
		root := t.TempDir()
		enh231Spec(t, root, bug200Folder, "tc-11111111")
		enh231Spec(t, root, bug200Folder, "tc-22222222")
		// Adapter resolution fails before the artefact check, so the artefact
		// file need not exist.
		writeTamperedWiring(t, root, "tc-11111111", "bats", "missing-runner", "test/acceptance/x.bats")
		seedLegacyRecord(t, root, legacyRecord{TC: "tc-22222222", Framework: "bats", Adapter: "bats-runner"})

		out, err := bug200Bulk(t, root, bug200Folder, "bats", bug200Config(), true, false, false)
		require.Error(t, err)
		assert.Contains(t, out, "1 errored")
	})
}

// --- REV-236 DCODEX-001: a framework containing "--" in discovery ---

// A valid record tc-77777777--legacy--bats.wiring.yaml whose body is
// (tc-77777777, framework legacy--bats), next to the spec tc-77777777--legacy,
// belongs to tc-77777777 by its body. Discovery must select and run it for
// tc-77777777, not hand it to the extended id by filename.
func TestREV236_DoubleDashFrameworkRunsInDiscovery(t *testing.T) {
	skipIfShort(t)
	root := t.TempDir()
	enh231Spec(t, root, bug200Folder, "tc-77777777")
	enh231Spec(t, root, bug200Folder, "tc-77777777--legacy")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-77777777", Framework: "legacy--bats", Adapter: "legacy-runner"})
	cfg := bug200Config()
	cfg.Adapters["execute"]["legacy-runner"] = &config.AdapterConfig{Framework: "legacy--bats", Mode: "sync", Command: "echo PASS"}
	before := countHandoffs(t, root)

	out, err := bug200Bulk(t, root, bug200Folder, "", cfg, true, false, false)
	require.NoError(t, err, out)
	assert.Contains(t, out, "1 passed, 1 skipped, 0 not run, 0 failed, 0 errored")
	assert.Regexp(t, `tc-77777777--legacy\s+skipped`, out, "the extended id has no wiring of its own")
	assert.Equal(t, before+1, countHandoffs(t, root), "tc-77777777 runs its legacy--bats record")
}

// Out-of-scope malformed or unprovable specs must not block a bulk run.
func TestREV236_OutOfScopeUnprovableSpecDoesNotBlockRun(t *testing.T) {
	skipIfShort(t)
	root := t.TempDir()
	enh231Spec(t, root, bug200Folder, "tc-77777777")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-77777777", Framework: "bats", Adapter: "bats-runner"})
	writeTestFile(t, root, "gtms/test/cases/other-folder/tc-77777777--legacy-x.md", "---\ntest_case_id: [unclosed\n---\n")
	writeTestFile(t, root, "gtms/test/cases/other-folder/tc-99999999-x.md", "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntitle: no id\n---\n")
	before := countHandoffs(t, root)

	out, err := bug200Bulk(t, root, bug200Folder, "", bug200Config(), true, true /*failFast*/, false)
	require.NoError(t, err, "out-of-scope unprovable specs must not block the run: %s", out)
	assert.Contains(t, out, "1 passed, 0 skipped, 0 not run, 0 failed, 0 errored")
	assert.Equal(t, before+1, countHandoffs(t, root))
}
