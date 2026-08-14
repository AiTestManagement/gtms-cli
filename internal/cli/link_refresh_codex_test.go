package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// REV-127 / Codex regression suite for BUG-146. These pin the four confirmed
// findings: preview/refresh must agree on the SAME identity (CODEX-001), malformed
// in-scope wiring is a counted failure not a silent drop (CODEX-002), root-position
// --dry-run state must not leak across executions (CODEX-003 / AC8), and a folder
// with duplicate bare TC IDs fails closed before any preview or write (CODEX-004).

// seedStaleLinkedTC writes a spec + artefact under folder/, links a current bats
// wiring record, then drifts the spec so the wiring is stale (a refresh would
// rewrite its testcase-hash). Returns the wiring file path. folder must be a real
// scope; the link is folder-qualified + --strict so the spec is scope-validated.
func seedStaleLinkedTC(t *testing.T, root, folder, tcID string) string {
	t.Helper()
	specDir := filepath.Join(root, "gtms/test/cases", folder)
	require.NoError(t, os.MkdirAll(specDir, 0o755))
	specPath := filepath.Join(specDir, tcID+"-spec.md")
	require.NoError(t, os.WriteFile(specPath,
		[]byte("---\ntest_case_id: "+tcID+"\ntitle: t\n---\n# body\n"), 0o644))

	art := filepath.Join(root, "test/acceptance", folder, tcID+".bats")
	require.NoError(t, os.MkdirAll(filepath.Dir(art), 0o755))
	require.NoError(t, os.WriteFile(art, []byte("@test \"x\" { true; }\n"), 0o644))

	_, err := runCmdCaptureStderr(t, newLinkCmd(), folder+"/"+tcID, "--framework", "bats", "--artefact", art, "--strict")
	require.NoError(t, err, "seed link must succeed")

	// Drift the spec -> testcase-hash is now stale.
	f, err := os.OpenFile(specPath, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, _ = f.WriteString("\n## drift\nmore body\n")
	require.NoError(t, f.Close())

	return filepath.Join(root, "gtms/automation/wiring", tcID+"--bats.wiring.yaml")
}

// TestLinkRefresh_FolderMalformedWiring_CountedFailure pins CODEX-002 and the
// tc-62328e8d spec: a malformed in-scope wiring record is a per-record failure in
// BOTH the dry and real paths (non-zero exit, stable "malformed or unreadable
// wiring" label), valid records in the same folder still process, and the malformed
// file is never written.
func TestLinkRefresh_FolderMalformedWiring_CountedFailure(t *testing.T) {
	skipIfShort(t)
	root := setupCompatProject(t)
	restore := withExecuteGlobals(t, root, batsLinkConfig())
	defer restore()

	folder := "malformed-scope"
	validWiring := seedStaleLinkedTC(t, root, folder, "tc-c0c00001")

	// tc-c0c00002: spec present (enters spec-first discovery) + a malformed wiring
	// file (unknown field fails strict decode).
	specDir := filepath.Join(root, "gtms/test/cases", folder)
	require.NoError(t, os.WriteFile(filepath.Join(specDir, "tc-c0c00002-spec.md"),
		[]byte("---\ntest_case_id: tc-c0c00002\ntitle: t\n---\n"), 0o644))
	malformed := filepath.Join(root, "gtms/automation/wiring", "tc-c0c00002--bats.wiring.yaml")
	malformedBytes := "testcase: tc-c0c00002\ntestcase-hash: 0011223344556677\nframework: bats\nadapter: bats-runner\nartefact: test/acceptance/malformed-scope/tc-c0c00002.bats\nartefact-hash: aabbccddeeff0011\nstatus: complete\n"
	require.NoError(t, os.WriteFile(malformed, []byte(malformedBytes), 0o644))

	validBefore, err := os.ReadFile(validWiring)
	require.NoError(t, err)

	// --- Preview ---
	out, err := runCmdCaptureStdout(t, newLinkCmd(), folder, "--refresh", "--dry-run")
	require.Error(t, err, "a malformed in-scope record must drive a non-zero exit")
	assert.Contains(t, out, "[dry-run] Would refresh: tc-c0c00001 (bats)")
	// CODEX-128-002 / AC2: the discovery-failure line is labelled too, so an
	// all-failures preview can never read as a real refresh failure.
	assert.Contains(t, out, "[dry-run] tc-c0c00002 (bats): malformed or unreadable wiring")
	assert.Contains(t, out, "[dry-run] 1 would refresh, 0 current, 1 failed")

	assertBytesEqual(t, validWiring, string(validBefore), "dry-run must not write the valid record")
	assertBytesEqual(t, malformed, malformedBytes, "dry-run must not write the malformed record")

	// --- Real ---
	out2, err2 := runCmdCaptureStdout(t, newLinkCmd(), folder, "--refresh")
	require.Error(t, err2)
	assert.Contains(t, out2, "Refreshed: tc-c0c00001 (bats)")
	assert.Contains(t, out2, "tc-c0c00002 (bats): malformed or unreadable wiring")
	assert.Contains(t, out2, "1 refreshed, 0 current, 1 failed")
	assert.NotContains(t, out2, "[dry-run]", "a real refresh must never carry the preview label")

	assertBytesEqual(t, malformed, malformedBytes, "the malformed record must never be written")
	validAfter, err := os.ReadFile(validWiring)
	require.NoError(t, err)
	assert.NotEqual(t, string(validBefore), string(validAfter), "the valid stale record must be refreshed by the real run")
}

// TestLinkRefresh_SingleTargetPreviewFailure_CarriesDryRunMarker pins
// CODEX-128-002. A single-target refresh prints exactly one line and has no dry
// summary, so if a validation failure omits the marker the whole preview is
// indistinguishable from a real refresh failure (AC2 requires an unmistakable
// [dry-run] label). The cause text stays identical across modes -- three BATS
// specs grep it -- so only the marker differs.
func TestLinkRefresh_SingleTargetPreviewFailure_CarriesDryRunMarker(t *testing.T) {
	skipIfShort(t)
	root := setupCompatProject(t)
	restore := withExecuteGlobals(t, root, batsLinkConfig())
	defer restore()

	// Link a current record, then delete the artefact so PlanRefresh fails.
	linkCurrentTC(t, root, "tc-eeee1111")
	require.NoError(t, os.Remove(filepath.Join(root, "test/acceptance/misc", "tc-eeee1111.bats")))

	wpath := filepath.Join(root, "gtms/automation/wiring", "tc-eeee1111--bats.wiring.yaml")
	before, err := os.ReadFile(wpath)
	require.NoError(t, err)

	out, err := runCmdCaptureStdout(t, newLinkCmd(), "tc-eeee1111", "--refresh", "--dry-run")
	require.Error(t, err)
	assert.Contains(t, out, "[dry-run] tc-eeee1111 (bats): artefact file not found",
		"a preview validation failure must carry the [dry-run] label")

	out2, err2 := runCmdCaptureStdout(t, newLinkCmd(), "tc-eeee1111", "--refresh")
	require.Error(t, err2)
	assert.Contains(t, out2, "tc-eeee1111 (bats): artefact file not found",
		"the real refresh reports the same cause")
	assert.NotContains(t, out2, "[dry-run]", "a real refresh must never carry the preview label")

	assertBytesEqual(t, wpath, string(before), "neither failed refresh may write")
}

// TestLinkRefresh_NoRecordPreview_CarriesDryRunMarker pins the no-record half of
// CODEX-128-002. When a single-TC refresh finds nothing to act on, that message is
// the command's ONLY output, so an unlabelled line makes the preview
// indistinguishable from a real refresh failure. Both branches are covered: the
// bare one and the --framework-filtered one. Cause substring, hint, and exit status
// stay identical across modes -- only the marker differs.
func TestLinkRefresh_NoRecordPreview_CarriesDryRunMarker(t *testing.T) {
	skipIfShort(t)
	root := setupCompatProject(t)
	restore := withExecuteGlobals(t, root, batsLinkConfig())
	defer restore()

	// A spec with no wiring at all, so discovery returns zero targets.
	specDir := filepath.Join(root, "gtms/test/cases/misc")
	require.NoError(t, os.MkdirAll(specDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(specDir, "tc-eeee2222-spec.md"),
		[]byte("---\ntest_case_id: tc-eeee2222\ntitle: t\n---\n# body\n"), 0o644))

	cases := []struct {
		name  string
		args  []string
		cause string
	}{
		{"bare", []string{"tc-eeee2222", "--refresh"}, "No wiring records found for 'tc-eeee2222'."},
		{"framework-filtered", []string{"tc-eeee2222", "--refresh", "--framework", "bats"},
			"No wiring record found for 'tc-eeee2222' (framework: bats)."},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dryErr, err := runCmdCaptureStderr(t, newLinkCmd(), append(append([]string{}, tc.args...), "--dry-run")...)
			require.Error(t, err)
			assert.Contains(t, dryErr, "[dry-run] "+tc.cause,
				"a no-record preview must carry the [dry-run] label")

			realErr, err2 := runCmdCaptureStderr(t, newLinkCmd(), tc.args...)
			require.Error(t, err2)
			assert.Contains(t, realErr, tc.cause, "the real refresh reports the same cause")
			assert.NotContains(t, realErr, "[dry-run]",
				"a real refresh must never carry the preview label")
		})
	}
}

// TestLinkRefresh_IdentityMismatch_DoesNotOverwriteForeignRecord pins CODEX-001:
// a file named tc-aaaaaaaa--bats whose YAML declares tc-bbbbbbbb is a per-record
// failure under the requested identity (A), and the real refresh does NOT write it
// onto B's legitimate wiring. Preview and real agree.
func TestLinkRefresh_IdentityMismatch_DoesNotOverwriteForeignRecord(t *testing.T) {
	skipIfShort(t)
	root := setupCompatProject(t)
	restore := withExecuteGlobals(t, root, batsLinkConfig())
	defer restore()

	// B: a legitimate, current wiring record for tc-bbbbbbbb.
	linkCurrentTC(t, root, "tc-bbbbbbbb")
	bWiring := filepath.Join(root, "gtms/automation/wiring", "tc-bbbbbbbb--bats.wiring.yaml")
	bBefore, err := os.ReadFile(bWiring)
	require.NoError(t, err)

	// The mismatch file: filename says tc-aaaaaaaa, YAML declares tc-bbbbbbbb with a
	// DIFFERENT artefact-hash. The old Find/FindAllForTC path would select it under A
	// and write it to B's path via wiring.Path(rec.TestCase), corrupting B.
	aFile := filepath.Join(root, "gtms/automation/wiring", "tc-aaaaaaaa--bats.wiring.yaml")
	aBytes := "testcase: tc-bbbbbbbb\ntestcase-hash: 1111111111111111\nframework: bats\nadapter: bats-runner\nartefact: test/acceptance/misc/tc-bbbbbbbb.bats\nartefact-hash: 2222222222222222\n"
	require.NoError(t, os.WriteFile(aFile, []byte(aBytes), 0o644))

	// --- Preview ---
	out, err := runCmdCaptureStdout(t, newLinkCmd(), "tc-aaaaaaaa", "--refresh", "--dry-run")
	require.Error(t, err)
	assert.Contains(t, out, "tc-aaaaaaaa (bats): malformed or unreadable wiring")
	assert.Contains(t, out, "identity mismatch")

	// --- Real ---
	out2, err2 := runCmdCaptureStdout(t, newLinkCmd(), "tc-aaaaaaaa", "--refresh")
	require.Error(t, err2)
	assert.Contains(t, out2, "tc-aaaaaaaa (bats): malformed or unreadable wiring")

	assertBytesEqual(t, bWiring, string(bBefore), "B's wiring must not be overwritten via a mismatched A-named file")
	assertBytesEqual(t, aFile, aBytes, "the mismatched file must not be rewritten")
}

// TestLinkRefresh_DuplicateFolderTCID_FailsClosed pins CODEX-004 as narrowed by
// REV-129 CLAUDE-003. A bare TC ID claimed by two specs in scope is held back and
// reported as a counted failure -- its wiring is never hashed from an arbitrary
// spec -- while every unambiguous test case in the same folder refreshes as
// normal. The command still exits non-zero. Preview and real agree record for
// record; only the [dry-run] label differs.
func TestLinkRefresh_DuplicateFolderTCID_FailsClosed(t *testing.T) {
	skipIfShort(t)
	root := setupCompatProject(t)
	restore := withExecuteGlobals(t, root, batsLinkConfig())
	defer restore()

	folder := "dup-scope"

	// An unambiguous, stale-but-valid neighbour in the SAME folder. Before
	// CLAUDE-003 the collision below stopped this one refreshing at all.
	neighbourWiring := seedStaleLinkedTC(t, root, folder, "tc-d0000001")
	neighbourBefore, err := os.ReadFile(neighbourWiring)
	require.NoError(t, err)

	specDir := filepath.Join(root, "gtms/test/cases", folder)
	require.NoError(t, os.WriteFile(filepath.Join(specDir, "tc-dddddddd-one.md"),
		[]byte("---\ntest_case_id: tc-dddddddd\ntitle: one\n---\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(specDir, "tc-dddddddd-two.md"),
		[]byte("---\ntest_case_id: tc-dddddddd\ntitle: two\n---\n"), 0o644))

	// A real wiring so a buggy path would have something to (wrongly) act on.
	wrec := filepath.Join(root, "gtms/automation/wiring", "tc-dddddddd--bats.wiring.yaml")
	_, werr := wiring.Write(root, &wiring.WiringRecord{
		TestCase: "tc-dddddddd", TestCaseHash: "0011223344556677", Framework: "bats",
		Adapter: "bats-runner", Artefact: "test/acceptance/dup-scope/d.bats", ArtefactHash: "aabbccddeeff0011",
	})
	require.NoError(t, werr)
	before, err := os.ReadFile(wrec)
	require.NoError(t, err)

	// --- Preview: ambiguous ID counted as a failure, neighbour still previewed ---
	out, err := runCmdCaptureStdout(t, newLinkCmd(), folder, "--refresh", "--dry-run")
	require.Error(t, err, "an ambiguous ID in scope must drive a non-zero exit")
	assert.Contains(t, out, "[dry-run] tc-dddddddd (bats): ambiguous refresh scope",
		"reported per in-scope RECORD, naming its framework like every other per-record line")
	assert.Contains(t, out, "give each test case a unique tc-<id>",
		"the remedy travels with the line -- there is no per-record hint to fall back on")
	assert.Contains(t, out, "[dry-run] Would refresh: tc-d0000001 (bats)",
		"an unambiguous record in the same folder must still be previewed")
	assert.Contains(t, out, "[dry-run] 1 would refresh, 0 current, 1 failed")
	assertBytesEqual(t, wrec, string(before), "a preview must not mutate wiring")
	assertBytesEqual(t, neighbourWiring, string(neighbourBefore), "a preview must not mutate wiring")

	// --- Real: same accounting, neighbour actually refreshed, ambiguous untouched ---
	out2, err2 := runCmdCaptureStdout(t, newLinkCmd(), folder, "--refresh")
	require.Error(t, err2)
	assert.Contains(t, out2, "tc-dddddddd (bats): ambiguous refresh scope")
	assert.NotContains(t, out2, "[dry-run]", "a real refresh must never carry the preview label")
	assert.Contains(t, out2, "Refreshed: tc-d0000001 (bats)")
	assert.Contains(t, out2, "1 refreshed, 0 current, 1 failed")

	assertBytesEqual(t, wrec, string(before),
		"the ambiguous record must never be written -- which spec to hash is undecidable")
	neighbourAfter, err := os.ReadFile(neighbourWiring)
	require.NoError(t, err)
	assert.NotEqual(t, string(neighbourBefore), string(neighbourAfter),
		"one collision must not stop the rest of the folder from refreshing")
}

// TestLinkRefresh_DryRunState_ExecutionScoped_RealSingleton is the BUG-146 AC8
// replacement (CODEX-003). It runs TWO consecutive executions through the REAL
// singleton root command in one process: a root-position `--dry-run` refresh, then
// a real refresh WITHOUT the flag. If the persistent --dry-run global leaked, the
// second run would silently preview and write nothing; this proves it actually
// writes.
func TestLinkRefresh_DryRunState_ExecutionScoped_RealSingleton(t *testing.T) {
	skipIfShort(t)

	root := initGitRepo(t)
	writeMinimalConfig(t, root)
	// Sentinel so FindParentDir resolves the "gtms" parent.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "gtms", ".gtms-root"), []byte(""), 0o644))

	// Spec + artefact (new gtms/test/ layout so no legacy migration fires).
	specDir := filepath.Join(root, "gtms/test/cases/foo")
	require.NoError(t, os.MkdirAll(specDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(specDir, "tc-abc12345-real.md"),
		[]byte("---\ntest_case_id: tc-abc12345\ntitle: real\n---\n# body\n"), 0o644))
	art := filepath.Join(root, "test/acceptance/foo/real.bats")
	require.NoError(t, os.MkdirAll(filepath.Dir(art), 0o755))
	require.NoError(t, os.WriteFile(art, []byte("@test \"x\" { true; }\n"), 0o644))

	// Stale but valid wiring: hashes are deliberately wrong so a refresh rewrites it.
	wpath := filepath.Join(root, "gtms/automation/wiring/tc-abc12345--bats.wiring.yaml")
	_, werr := wiring.Write(root, &wiring.WiringRecord{
		TestCase: "tc-abc12345", TestCaseHash: "0011223344556677", Framework: "bats",
		Adapter: "bats-runner", Artefact: "test/acceptance/foo/real.bats", ArtefactHash: "aabbccddeeff0011",
	})
	require.NoError(t, werr)

	// PersistentPreRunE overwrites these package globals; restore them after.
	savedRoot, savedCfg := projectRoot, appConfig
	defer func() { projectRoot, appConfig = savedRoot, savedCfg }()

	// Run from the project root so PersistentPreRunE discovers it via os.Getwd.
	// Manual save/restore (module targets go1.21, so t.Chdir is unavailable).
	savedWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(root))
	defer func() { _ = os.Chdir(savedWd) }()

	before, err := os.ReadFile(wpath)
	require.NoError(t, err)

	// Execution 1: root-position --dry-run (sets the persistent global dryRun=true).
	out1 := captureStdout(t, func() {
		rootCmd.SetArgs([]string{"--dry-run", "link", "--refresh", "tc-abc12345"})
		require.NoError(t, ExecuteContext(context.Background()))
	})
	assert.Contains(t, out1, "[dry-run]", "the first execution must run as a preview")
	afterDry, err := os.ReadFile(wpath)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(afterDry), "the dry-run preview must write nothing")

	// Execution 2: real refresh, no --dry-run. A leaked persistent global would keep
	// this in preview mode and write nothing.
	out2 := captureStdout(t, func() {
		rootCmd.SetArgs([]string{"link", "--refresh", "tc-abc12345"})
		require.NoError(t, ExecuteContext(context.Background()))
	})
	assert.Contains(t, out2, "Refreshed: tc-abc12345", "the second execution must actually refresh")
	assert.NotContains(t, out2, "[dry-run]", "the second execution must not be a preview")
	afterReal, err := os.ReadFile(wpath)
	require.NoError(t, err)
	assert.NotEqual(t, string(before), string(afterReal), "the real refresh must write the stale wiring")
}

// seedFlagLeakFixture builds a project whose top folder holds one stale bats record
// and one stale playwright record, plus a stale bats record in a nested subfolder.
// It sets up the sentinel + config so ExecuteContext's PersistentPreRunE discovers
// the root, and chdirs into it. Returns the project root and the three wiring paths
// (topBats, topPlaywright, nestedBats). Package globals and cwd are restored via
// t.Cleanup.
func seedFlagLeakFixture(t *testing.T) (root, topBats, topPlaywright, nestedBats string) {
	t.Helper()
	root = initGitRepo(t)
	writeMinimalConfig(t, root)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "gtms", ".gtms-root"), []byte(""), 0o644))

	// Two specs directly in the top folder (bats + playwright), one nested spec.
	topDir := filepath.Join(root, "gtms/test/cases/topfolder")
	subDir := filepath.Join(topDir, "sub")
	require.NoError(t, os.MkdirAll(subDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(topDir, "tc-top11111.md"),
		[]byte("---\ntest_case_id: tc-top11111\ntitle: top-bats\n---\n# body\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(topDir, "tc-top22222.md"),
		[]byte("---\ntest_case_id: tc-top22222\ntitle: top-playwright\n---\n# body\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(subDir, "tc-sub33333.md"),
		[]byte("---\ntest_case_id: tc-sub33333\ntitle: nested-bats\n---\n# body\n"), 0o644))

	// Matching artefacts.
	mkArtefact := func(rel, body string) {
		p := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
	mkArtefact("test/acceptance/topfolder/tc-top11111.bats", "@test \"x\" { true; }\n")
	mkArtefact("test/acceptance/topfolder/tc-top22222.spec.ts", "test('x', () => {});\n")
	mkArtefact("test/acceptance/topfolder/sub/tc-sub33333.bats", "@test \"y\" { true; }\n")

	// Stale-but-valid wiring: hashes deliberately wrong so a refresh rewrites each.
	seedWiring := func(tcID, framework, adapter, artefact string) string {
		_, werr := wiring.Write(root, &wiring.WiringRecord{
			TestCase: tcID, TestCaseHash: "0011223344556677", Framework: framework,
			Adapter: adapter, Artefact: artefact, ArtefactHash: "aabbccddeeff0011",
		})
		require.NoError(t, werr)
		return filepath.Join(root, "gtms/automation/wiring", tcID+"--"+framework+".wiring.yaml")
	}
	topBats = seedWiring("tc-top11111", "bats", "bats-runner", "test/acceptance/topfolder/tc-top11111.bats")
	topPlaywright = seedWiring("tc-top22222", "playwright", "pw-runner", "test/acceptance/topfolder/tc-top22222.spec.ts")
	nestedBats = seedWiring("tc-sub33333", "bats", "bats-runner", "test/acceptance/topfolder/sub/tc-sub33333.bats")

	// Restore PersistentPreRunE-mutated globals and cwd after the test.
	savedRoot, savedCfg := projectRoot, appConfig
	t.Cleanup(func() { projectRoot, appConfig = savedRoot, savedCfg })
	savedWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(root))
	t.Cleanup(func() { _ = os.Chdir(savedWd) })

	return root, topBats, topPlaywright, nestedBats
}

// TestLinkRefresh_AllFlagState_ExecutionScoped_RealSingleton pins CODEX-128-001:
// Cobra retains EVERY parsed flag on the singleton command tree, not just
// --dry-run. A first `--dry-run link topfolder --refresh -r --framework bats`
// leaves recursiveFlag=true and frameworkFlag="bats" bound to their closure
// variables. Without a comprehensive reset, a later plain
// `link topfolder --refresh` in the SAME process would silently recurse into the
// subfolder and stay filtered to bats -- mutating out of the requested scope.
//
// The fixture makes both leaks independently observable: the second (unfiltered,
// non-recursive) refresh must update BOTH the top bats and the top playwright
// records (framework filter reset), while the nested bats record stays
// byte-identical (recursion reset). Runs the root-position and local-position
// --dry-run variants so both the persistent global and the closure-bound local
// flag are exercised.
func TestLinkRefresh_AllFlagState_ExecutionScoped_RealSingleton(t *testing.T) {
	skipIfShort(t)

	// exec1 is the dry, recursive, framework-narrowed preview; the position of
	// --dry-run differs per variant. exec2 is always the plain real refresh.
	variants := map[string][]string{
		"root-position dry-run":  {"--dry-run", "link", "topfolder", "--refresh", "-r", "--framework", "bats"},
		"local-position dry-run": {"link", "topfolder", "--refresh", "-r", "--framework", "bats", "--dry-run"},
	}

	for name, exec1Args := range variants {
		exec1Args := exec1Args
		t.Run(name, func(t *testing.T) {
			_, topBats, topPlaywright, nestedBats := seedFlagLeakFixture(t)

			readAll := func() (string, string, string) {
				b, err := os.ReadFile(topBats)
				require.NoError(t, err)
				p, err := os.ReadFile(topPlaywright)
				require.NoError(t, err)
				n, err := os.ReadFile(nestedBats)
				require.NoError(t, err)
				return string(b), string(p), string(n)
			}
			batsBefore, pwBefore, nestedBefore := readAll()

			// Execution 1: dry + recursive + framework-narrowed. Writes nothing;
			// leaves recursiveFlag/frameworkFlag set on the singleton if not reset.
			out1 := captureStdout(t, func() {
				rootCmd.SetArgs(exec1Args)
				require.NoError(t, ExecuteContext(context.Background()))
			})
			assert.Contains(t, out1, "[dry-run]", "the first execution must run as a preview")
			b1, p1, n1 := readAll()
			assert.Equal(t, batsBefore, b1, "dry-run must not write the top bats record")
			assert.Equal(t, pwBefore, p1, "dry-run must not write the top playwright record")
			assert.Equal(t, nestedBefore, n1, "dry-run must not write the nested record")

			// Execution 2: plain real refresh, no --dry-run / -r / --framework. If any
			// flag leaked, this would preview, or recurse, or filter to bats only.
			out2 := captureStdout(t, func() {
				rootCmd.SetArgs([]string{"link", "topfolder", "--refresh"})
				require.NoError(t, ExecuteContext(context.Background()))
			})
			assert.NotContains(t, out2, "[dry-run]", "the second execution must not be a preview (dry-run leaked)")
			assert.Contains(t, out2, "Refreshed: tc-top11111 (bats)", "the top bats record must refresh")
			assert.Contains(t, out2, "Refreshed: tc-top22222 (playwright)",
				"the top playwright record must refresh (framework filter leaked)")

			b2, p2, n2 := readAll()
			assert.NotEqual(t, batsBefore, b2, "the top bats record must be rewritten by the real refresh")
			assert.NotEqual(t, pwBefore, p2,
				"the top playwright record must be rewritten -- a leaked --framework bats would skip it")
			assert.Equal(t, nestedBefore, n2,
				"the nested record must stay byte-identical -- a leaked -r would recurse and refresh it")
		})
	}
}

// linkCurrentTC writes a spec + artefact and links a CURRENT bats wiring record for
// tcID (no drift), so the wiring hashes match current content.
func linkCurrentTC(t *testing.T, root, tcID string) {
	t.Helper()
	specDir := filepath.Join(root, "gtms/test/cases/misc")
	require.NoError(t, os.MkdirAll(specDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(specDir, tcID+"-spec.md"),
		[]byte("---\ntest_case_id: "+tcID+"\ntitle: t\n---\n# body\n"), 0o644))
	art := filepath.Join(root, "test/acceptance/misc", tcID+".bats")
	require.NoError(t, os.MkdirAll(filepath.Dir(art), 0o755))
	require.NoError(t, os.WriteFile(art, []byte("@test \"x\" { true; }\n"), 0o644))
	_, err := runCmdCaptureStderr(t, newLinkCmd(), tcID, "--framework", "bats", "--artefact", art)
	require.NoError(t, err)
}

// assertBytesEqual asserts the file at path has exactly want bytes.
func assertBytesEqual(t *testing.T, path, want, msg string) {
	t.Helper()
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, want, string(got), msg)
}

// TestLinkRefresh_EmptyScopeMessages pins REV-129 CLAUDE-002: the folder preview
// distinguishes "there was wiring and none of it is stale" from "there is no
// wiring here at all". The old wording claimed the latter was the former, which
// is the opposite diagnosis -- it tells a user whose folder was never linked
// that nothing needs doing. Both cases still exit 0 and still print the summary.
func TestLinkRefresh_EmptyScopeMessages(t *testing.T) {
	skipIfShort(t)
	root := setupCompatProject(t)
	restore := withExecuteGlobals(t, root, batsLinkConfig())
	defer restore()

	// Folder A: one current (non-stale) wiring record.
	linkCurrentTC(t, root, "tc-cccc1111")

	outCurrent, err := runCmdCaptureStdout(t, newLinkCmd(), "misc", "--refresh", "--dry-run")
	require.NoError(t, err, "an all-current scope exits 0")
	assert.Contains(t, outCurrent, "[dry-run] Nothing to refresh -- all wiring is current.")
	assert.Contains(t, outCurrent, "[dry-run] 0 would refresh, 1 current, 0 failed")

	// Folder B: a spec with no wiring record at all.
	emptyDir := filepath.Join(root, "gtms/test/cases", "unlinked")
	require.NoError(t, os.MkdirAll(emptyDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(emptyDir, "tc-cccc2222-spec.md"),
		[]byte("---\ntest_case_id: tc-cccc2222\ntitle: t\n---\n# body\n"), 0o644))

	outEmpty, err := runCmdCaptureStdout(t, newLinkCmd(), "unlinked", "--refresh", "--dry-run")
	require.NoError(t, err, "an empty scope still exits 0 (AC2)")
	assert.Contains(t, outEmpty, "[dry-run] No wiring records in scope.")
	assert.NotContains(t, outEmpty, "all wiring is current",
		"a folder with no wiring must not be described as current")
	assert.Contains(t, outEmpty, "[dry-run] 0 would refresh, 0 current, 0 failed")
}

// TestLinkRefresh_DirectoryScanFailure_CarriesDryRunMarker pins REV-129 round-4
// CODEX-001: the marker rule extends to the operation-level errors too. When
// discovery fails at the DIRECTORY level -- the wiring directory is unreadable,
// or is not a directory at all -- that one "Scanning wiring" line is the whole
// output of the command, so without the label a preview is indistinguishable
// from a real refresh failure. Both the single-TC and folder paths are covered.
func TestLinkRefresh_DirectoryScanFailure_CarriesDryRunMarker(t *testing.T) {
	skipIfShort(t)
	root := setupCompatProject(t)
	restore := withExecuteGlobals(t, root, batsLinkConfig())
	defer restore()

	folder := "scanfail"
	specDir := filepath.Join(root, "gtms/test/cases", folder)
	require.NoError(t, os.MkdirAll(specDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(specDir, "tc-ffff1111-spec.md"),
		[]byte("---\ntest_case_id: tc-ffff1111\ntitle: t\n---\n# body\n"), 0o644))

	// Replace the wiring DIRECTORY with a regular file. It exists, so discovery
	// does not take the missing-directory shortcut, but os.ReadDir fails on it.
	// A plain file works the same way on every platform, unlike a permission bit.
	wdir := filepath.Join(root, "gtms/automation/wiring")
	require.NoError(t, os.RemoveAll(wdir))
	require.NoError(t, os.MkdirAll(filepath.Dir(wdir), 0o755))
	require.NoError(t, os.WriteFile(wdir, []byte("not a directory\n"), 0o644))

	for _, tc := range []struct {
		name   string
		target string
		cause  string
	}{
		{"single-TC", "tc-ffff1111", "Scanning wiring for tc-ffff1111"},
		{"folder", folder, "Scanning wiring for " + folder + "/"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dryErr, err := runCmdCaptureStderr(t, newLinkCmd(), tc.target, "--refresh", "--dry-run")
			require.Error(t, err)
			assert.Contains(t, dryErr, "[dry-run] "+tc.cause,
				"a directory-level preview failure must carry the [dry-run] label")

			realErr, err2 := runCmdCaptureStderr(t, newLinkCmd(), tc.target, "--refresh")
			require.Error(t, err2)
			assert.Contains(t, realErr, tc.cause, "the real refresh reports the same cause")
			assert.NotContains(t, realErr, "[dry-run]",
				"a real refresh must never carry the preview label")
		})
	}
}

// TestLinkRefresh_AmbiguousIDRespectsWiringScope pins REV-130 CODEX-130-002 and
// the round-5 CLAUDE-001 that independently found the same defect. The ambiguity
// guard is about a write that would hash an arbitrary one of two same-ID specs,
// so it must fire per in-scope wiring RECORD -- not per spec ID, before discovery
// has decided what is even in scope. Counting up-front failed a --framework bats
// refresh over an ambiguous ID that only had a playwright record, and failed a
// folder that had no wiring at all, both of which previously exited 0.
func TestLinkRefresh_AmbiguousIDRespectsWiringScope(t *testing.T) {
	skipIfShort(t)
	root := setupCompatProject(t)
	restore := withExecuteGlobals(t, root, batsLinkConfig())
	defer restore()

	// A folder whose only content is two specs claiming one bare ID, and no
	// wiring anywhere.
	nowiring := "ambiguous-nowiring"
	dir := filepath.Join(root, "gtms/test/cases", nowiring)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-ab000001-one.md"),
		[]byte("---\ntest_case_id: tc-ab000001\ntitle: one\n---\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tc-ab000001-two.md"),
		[]byte("---\ntest_case_id: tc-ab000001\ntitle: two\n---\n"), 0o644))

	out, err := runCmdCaptureStdout(t, newLinkCmd(), nowiring, "--refresh", "--dry-run")
	require.NoError(t, err, "nothing to refresh means nothing to fail -- exit 0")
	assert.Contains(t, out, "[dry-run] No wiring records in scope.")
	assert.NotContains(t, out, "ambiguous refresh scope",
		"an ambiguous ID with no wiring in scope has no record to fail")

	out2, err2 := runCmdCaptureStdout(t, newLinkCmd(), nowiring, "--refresh")
	require.NoError(t, err2, "the real refresh agrees -- this is the pre-change behaviour")
	assert.Contains(t, out2, "0 refreshed, 0 current, 0 failed")

	// A folder where the ambiguous ID has ONLY a playwright record. A bats-scoped
	// refresh must not fail on it.
	scoped := "ambiguous-scoped"
	dir2 := filepath.Join(root, "gtms/test/cases", scoped)
	require.NoError(t, os.MkdirAll(dir2, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir2, "tc-ab000002-one.md"),
		[]byte("---\ntest_case_id: tc-ab000002\ntitle: one\n---\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir2, "tc-ab000002-two.md"),
		[]byte("---\ntest_case_id: tc-ab000002\ntitle: two\n---\n"), 0o644))
	_, werr := wiring.Write(root, &wiring.WiringRecord{
		TestCase: "tc-ab000002", TestCaseHash: "0011223344556677", Framework: "playwright",
		Adapter: "pw-runner", Artefact: "test/acceptance/ambiguous-scoped/x.spec.ts",
		ArtefactHash: "aabbccddeeff0011",
	})
	require.NoError(t, werr)

	outBats, errBats := runCmdCaptureStdout(t, newLinkCmd(), scoped, "--refresh", "--dry-run", "--framework", "bats")
	require.NoError(t, errBats, "no bats record is in scope, so there is nothing to fail")
	assert.NotContains(t, outBats, "ambiguous refresh scope")

	// Unfiltered, the playwright record IS in scope -- now the ambiguity is real
	// and must be reported and counted, naming the framework like any other
	// per-record line.
	outAll, errAll := runCmdCaptureStdout(t, newLinkCmd(), scoped, "--refresh", "--dry-run")
	require.Error(t, errAll)
	assert.Contains(t, outAll, "[dry-run] tc-ab000002 (playwright): ambiguous refresh scope")
	assert.Contains(t, outAll, "[dry-run] 0 would refresh, 0 current, 1 failed")
}
