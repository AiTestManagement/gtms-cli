package cli

// BUG-211 Go coverage: gtms execute must refuse a wiring record whose body
// disagrees with the file it was selected from, before the body's framework,
// adapter or artefact is trusted -- on both the single and bulk paths, for
// pending and non-pending records. The pending bootstrap must write only the
// selected file, re-read before writing, and update its in-memory record only
// after a successful write.
//
// Canonical acceptance specs live in
// gtms/test/cases/execute-wiring-identity-guard/. These Go tests are the
// package-level companion to those black-box specs.

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/aitestmanagement/gtms-cli/internal/link"
	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

const (
	bug211TC      = "tc-11111111"
	bug211Foreign = "tc-22222222"
	bug211Control = "tc-33333333"
	bug211Third   = "tc-44444444"
)

// writeRawWiring writes rec to path verbatim, bypassing wiring.Write (whose
// destination derives from the body). This is the only way to seed a file
// whose name disagrees with its body.
func writeRawWiring(t *testing.T, path string, rec *wiring.WiringRecord) []byte {
	t.Helper()
	raw, err := yaml.Marshal(rec)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	require.NoError(t, os.WriteFile(path, raw, 0644))
	return raw
}

func readBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return b
}

func bug211SelectedPath(t *testing.T, root string) string {
	t.Helper()
	p, err := wiring.Path(root, bug211TC, "bats")
	require.NoError(t, err)
	return p
}

// runBulkIdentity runs the bulk path with an explicit framework, fail-fast and
// allow-stale control, mirroring runBulkWithFramework but with the extra knobs.
func runBulkIdentity(t *testing.T, root, folder, framework string, failFast, allowStale bool) (string, error) {
	t.Helper()
	restore := withExecuteGlobals(t, root, enh231Config(t, ""))
	defer restore()
	cmd := &cobra.Command{Use: "execute"}
	cmd.SetContext(context.Background())
	var err error
	out := captureStderr(t, func() {
		err = runBulkExecute(cmd, root, folder, "", "", "", framework, false, failFast, false, allowStale)
	})
	return out, err
}

// --- 1. Pending identity mismatch refused; bootstrap writes no body-derived file ---

func TestBUG211_PendingBootstrapRefusesIdentityMismatch(t *testing.T) {
	cases := []struct {
		name       string
		bulk       bool
		framework  string // --framework; "" = none
		allowStale bool
		bodyTC     string
		bodyFW     string
		decoy      bool // also seed a valid record at the body-derived path
	}{
		{name: "single --framework testcase mismatch", framework: "bats", bodyTC: bug211Foreign, bodyFW: "bats", decoy: true},
		{name: "single --framework framework mismatch", framework: "bats", bodyTC: bug211TC, bodyFW: "playwright", decoy: true},
		{name: "single no-framework framework mismatch", bodyTC: bug211TC, bodyFW: "playwright"},
		{name: "single --allow-stale framework mismatch", framework: "bats", allowStale: true, bodyTC: bug211TC, bodyFW: "playwright", decoy: true},
		{name: "bulk --framework testcase mismatch", bulk: true, framework: "bats", bodyTC: bug211Foreign, bodyFW: "bats", decoy: true},
		{name: "bulk --framework framework mismatch", bulk: true, framework: "bats", bodyTC: bug211TC, bodyFW: "playwright", decoy: true},
		{name: "bulk no-framework framework mismatch", bulk: true, bodyTC: bug211TC, bodyFW: "playwright"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			skipIfShort(t)
			root := setupCompatProject(t)
			folder := "bug211"
			enh231Spec(t, root, folder, bug211TC)
			specHash, err := pipeline.HashFile(filepath.Join(root, "gtms/test/cases", folder, bug211TC+"-x.md"))
			require.NoError(t, err)

			// A pending wiring whose body disagrees with its filename.
			selectedRec := &wiring.WiringRecord{
				TestCase: tc.bodyTC, TestCaseHash: specHash, Framework: tc.bodyFW,
				Adapter: "bats-runner", Artefact: "test/acceptance/" + bug211TC + ".bats",
				ArtefactHash: wiring.PendingArtefactHash,
			}
			selectedPath := bug211SelectedPath(t, root)
			selectedRaw := writeRawWiring(t, selectedPath, selectedRec)

			var decoyPath string
			var decoyRaw []byte
			if tc.decoy {
				decoyPath, err = wiring.Path(root, tc.bodyTC, tc.bodyFW)
				require.NoError(t, err)
				decoyRaw = writeRawWiring(t, decoyPath, &wiring.WiringRecord{
					TestCase: tc.bodyTC, TestCaseHash: specHash, Framework: tc.bodyFW,
					Adapter: "bats-runner", Artefact: "test/acceptance/decoy.bats",
					ArtefactHash: "aabbccddeeff0011",
				})
			} else {
				decoyPath, err = wiring.Path(root, tc.bodyTC, tc.bodyFW)
				require.NoError(t, err)
			}

			var out string
			if tc.bulk {
				enh231Spec(t, root, folder, bug211Control)
				seedLegacyRecord(t, root, legacyRecord{TC: bug211Control, Framework: "bats", Adapter: "bats-runner"})
				out, err = runBulkIdentity(t, root, folder, tc.framework, false, tc.allowStale)
				require.Error(t, err, "bulk must exit non-zero on an identity error: %s", out)
				assert.Contains(t, out, "identity", "bulk per-test line must carry the stable identity token")
				assert.Contains(t, out, bug211Control, "the healthy control still runs after the error")
				assert.Contains(t, out, "pass", "the healthy control still passes")
			} else {
				args := []string{bug211TC}
				if tc.framework != "" {
					args = append(args, "--framework", tc.framework)
				}
				if tc.allowStale {
					args = append(args, "--allow-stale")
				}
				out, err = runSingle(t, root, enh231Config(t, ""), args...)
				require.Error(t, err, "single must exit non-zero: %s", out)
				assert.Contains(t, out, "identity", "single refusal must carry the stable identity token")
				assert.NotContains(t, out, "No wiring record", "it must not be reported as missing wiring")
			}

			// The selected file stays pending and byte-for-byte unchanged.
			assert.Equal(t, string(selectedRaw), string(readBytes(t, selectedPath)),
				"selected wiring must be byte-for-byte unchanged")
			if tc.decoy {
				assert.Equal(t, string(decoyRaw), string(readBytes(t, decoyPath)),
					"body-derived wiring must be byte-for-byte unchanged")
			} else {
				_, statErr := os.Stat(decoyPath)
				assert.True(t, os.IsNotExist(statErr), "no wiring may be created at the body-derived path")
			}
			if tc.bulk {
				assert.Equal(t, 1, countHandoffs(t, root), "only the healthy control ran, not the mismatch")
			} else {
				assert.Equal(t, 0, countExecuteTasks(t, root), "no execute task for the mismatch")
				assert.Equal(t, 0, countHandoffs(t, root), "no result contract")
			}
		})
	}
}

// --- 2. Non-pending identity mismatch never runs ---

func TestBUG211_NonPendingIdentityMismatchNeverRuns(t *testing.T) {
	cases := []struct {
		name      string
		bulk      bool
		framework string
		bodyTC    string
		bodyFW    string
		decoy     bool
	}{
		{name: "single --framework testcase mismatch", framework: "bats", bodyTC: bug211Foreign, bodyFW: "bats", decoy: true},
		{name: "single --framework framework mismatch", framework: "bats", bodyTC: bug211TC, bodyFW: "playwright", decoy: true},
		{name: "single no-framework framework mismatch", bodyTC: bug211TC, bodyFW: "playwright"},
		{name: "bulk --framework testcase mismatch", bulk: true, framework: "bats", bodyTC: bug211Foreign, bodyFW: "bats", decoy: true},
		{name: "bulk --framework framework mismatch", bulk: true, framework: "bats", bodyTC: bug211TC, bodyFW: "playwright", decoy: true},
		{name: "bulk no-framework framework mismatch", bulk: true, bodyTC: bug211TC, bodyFW: "playwright"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			skipIfShort(t)
			root := setupCompatProject(t)
			folder := "bug211"
			enh231Spec(t, root, folder, bug211TC)
			specHash, err := pipeline.HashFile(filepath.Join(root, "gtms/test/cases", folder, bug211TC+"-x.md"))
			require.NoError(t, err)

			// A real (non-pending) artefact hash: verify fires before drift, so
			// the file need not exist for the refusal.
			selectedRec := &wiring.WiringRecord{
				TestCase: tc.bodyTC, TestCaseHash: specHash, Framework: tc.bodyFW,
				Adapter: "bats-runner", Artefact: "test/acceptance/" + bug211TC + ".bats",
				ArtefactHash: "ccddeeff00112233",
			}
			selectedPath := bug211SelectedPath(t, root)
			selectedRaw := writeRawWiring(t, selectedPath, selectedRec)

			decoyPath, err := wiring.Path(root, tc.bodyTC, tc.bodyFW)
			require.NoError(t, err)
			var decoyRaw []byte
			if tc.decoy {
				decoyRaw = writeRawWiring(t, decoyPath, &wiring.WiringRecord{
					TestCase: tc.bodyTC, TestCaseHash: specHash, Framework: tc.bodyFW,
					Adapter: "bats-runner", Artefact: "test/acceptance/decoy.bats",
					ArtefactHash: "aabbccddeeff0011",
				})
			}

			var out string
			if tc.bulk {
				enh231Spec(t, root, folder, bug211Control)
				seedLegacyRecord(t, root, legacyRecord{TC: bug211Control, Framework: "bats", Adapter: "bats-runner"})
				out, err = runBulkIdentity(t, root, folder, tc.framework, false, false)
				require.Error(t, err, "bulk must exit non-zero: %s", out)
				assert.Contains(t, out, "identity")
				assert.Contains(t, out, bug211Control, "the healthy control still runs after the error")
				assert.Contains(t, out, "pass", "the healthy control still passes")
				assert.Equal(t, 1, countHandoffs(t, root), "only the healthy control ran, not the mismatch")
			} else {
				args := []string{bug211TC}
				if tc.framework != "" {
					args = append(args, "--framework", tc.framework)
				}
				out, err = runSingle(t, root, enh231Config(t, ""), args...)
				require.Error(t, err, "single must exit non-zero: %s", out)
				assert.Contains(t, out, "identity")
				assert.Equal(t, 0, countHandoffs(t, root), "no adapter run under either framework")
				assert.Equal(t, 0, countExecuteTasks(t, root), "no execute task for the mismatch")
			}

			assert.Equal(t, string(selectedRaw), string(readBytes(t, selectedPath)),
				"selected wiring must be byte-for-byte unchanged")
			if tc.decoy {
				assert.Equal(t, string(decoyRaw), string(readBytes(t, decoyPath)),
					"body-derived wiring must be byte-for-byte unchanged")
			} else {
				_, statErr := os.Stat(decoyPath)
				assert.True(t, os.IsNotExist(statErr), "no wiring may be created at the body-derived path")
			}
		})
	}
}

// --- 3. Bulk fail-fast stops at the first identity mismatch ---

func TestBUG211_BulkFailFastStopsAtIdentityMismatch(t *testing.T) {
	skipIfShort(t)
	root := setupCompatProject(t)
	folder := "bug211bulk"

	// Mismatch sorts first; two healthy controls follow.
	enh231Spec(t, root, folder, bug211TC)
	specHash, err := pipeline.HashFile(filepath.Join(root, "gtms/test/cases", folder, bug211TC+"-x.md"))
	require.NoError(t, err)
	selectedPath := bug211SelectedPath(t, root)
	selectedRaw := writeRawWiring(t, selectedPath, &wiring.WiringRecord{
		TestCase: bug211Foreign, TestCaseHash: specHash, Framework: "bats",
		Adapter: "bats-runner", Artefact: "test/acceptance/" + bug211TC + ".bats",
		ArtefactHash: wiring.PendingArtefactHash,
	})
	// Body-derived control (tc-22222222) has no spec in the folder, so it is not
	// independently selected.
	decoyPath, err := wiring.Path(root, bug211Foreign, "bats")
	require.NoError(t, err)
	decoyRaw := writeRawWiring(t, decoyPath, &wiring.WiringRecord{
		TestCase: bug211Foreign, TestCaseHash: specHash, Framework: "bats",
		Adapter: "bats-runner", Artefact: "test/acceptance/decoy.bats",
		ArtefactHash: "aabbccddeeff0011",
	})

	for _, control := range []string{bug211Control, bug211Third} {
		enh231Spec(t, root, folder, control)
		seedLegacyRecord(t, root, legacyRecord{TC: control, Framework: "bats", Adapter: "bats-runner"})
	}
	control3Raw := readBytes(t, mustWiringPath(t, root, bug211Control))
	control4Raw := readBytes(t, mustWiringPath(t, root, bug211Third))

	out, err := runBulkIdentity(t, root, folder, "bats", true /* fail-fast */, false)
	require.Error(t, err, "fail-fast run must exit non-zero: %s", out)
	assert.Contains(t, out, "identity")
	assert.NotContains(t, out, bug211Control, "fail-fast stops before the first healthy control runs")
	assert.NotContains(t, out, bug211Third, "fail-fast stops before the second healthy control runs")

	assert.Equal(t, 0, countHandoffs(t, root), "no adapter ran")
	assert.Equal(t, 0, countExecuteTasks(t, root), "no execute task")
	assert.Equal(t, string(selectedRaw), string(readBytes(t, selectedPath)), "selected wiring unchanged")
	assert.Equal(t, string(decoyRaw), string(readBytes(t, decoyPath)), "body-derived wiring unchanged")
	assert.Equal(t, string(control3Raw), string(readBytes(t, mustWiringPath(t, root, bug211Control))), "unvisited control unchanged")
	assert.Equal(t, string(control4Raw), string(readBytes(t, mustWiringPath(t, root, bug211Third))), "unvisited control unchanged")
}

func mustWiringPath(t *testing.T, root, tcID string) string {
	t.Helper()
	p, err := wiring.Path(root, tcID, "bats")
	require.NoError(t, err)
	return p
}

// --- 4. Bootstrap refuses a concurrent wiring change between hashing and write ---

func TestBUG211_PendingBootstrapRefusesConcurrentWiringChange(t *testing.T) {
	skipIfShort(t)
	root := setupCompatProject(t)
	folder := "bug211"
	enh231Spec(t, root, folder, bug211TC)
	specHash, err := pipeline.HashFile(filepath.Join(root, "gtms/test/cases", folder, bug211TC+"-x.md"))
	require.NoError(t, err)

	artefact := filepath.Join(root, "test/acceptance", bug211TC+".bats")
	require.NoError(t, os.MkdirAll(filepath.Dir(artefact), 0755))
	require.NoError(t, os.WriteFile(artefact, []byte("# fixture artefact\n"), 0644))

	// A consistent pending wiring: the identity check passes, so the bootstrap
	// proceeds to hashing.
	rec := &wiring.WiringRecord{
		TestCase: bug211TC, TestCaseHash: specHash, Framework: "bats",
		Adapter: "bats-runner", Artefact: "test/acceptance/" + bug211TC + ".bats",
		ArtefactHash: wiring.PendingArtefactHash,
	}
	selectedPath := bug211SelectedPath(t, root)
	writeRawWiring(t, selectedPath, rec)
	sel := wiring.Selection{Record: rec, Path: selectedPath, TestCase: bug211TC, Framework: "bats"}

	// A competing writer changes the selected file after hashing, before the write.
	newerRec := &wiring.WiringRecord{
		TestCase: bug211TC, TestCaseHash: specHash, Framework: "bats",
		Adapter: "other-runner", Artefact: "test/acceptance/" + bug211TC + ".bats",
		ArtefactHash: wiring.PendingArtefactHash,
	}
	var newerRaw []byte
	setExecuteTestHook(t, func(stage string) {
		if stage == "post-bootstrap-hash" {
			newerRaw = writeRawWiring(t, selectedPath, newerRec)
		}
	})

	absArtefact := filepath.Join(root, "test/acceptance", bug211TC+".bats")
	hash, err := bootstrapPendingWiring(root, sel, absArtefact)
	require.Error(t, err, "a concurrent change must refuse")
	assert.True(t, errors.Is(err, link.ErrConcurrentWiringChange), "got %v", err)
	assert.Empty(t, hash, "a refusal returns no hash")
	assert.Equal(t, wiring.PendingArtefactHash, rec.ArtefactHash,
		"the in-memory record must stay pending after a refusal")
	assert.Equal(t, string(newerRaw), string(readBytes(t, selectedPath)),
		"the newer wiring must survive byte-for-byte")
}

// --- 5. In-memory hash is set only after a successful write ---

func TestBUG211_PendingBootstrapSuccessUpdatesInMemory(t *testing.T) {
	skipIfShort(t)
	root := setupCompatProject(t)
	folder := "bug211"
	enh231Spec(t, root, folder, bug211TC)
	specHash, err := pipeline.HashFile(filepath.Join(root, "gtms/test/cases", folder, bug211TC+"-x.md"))
	require.NoError(t, err)

	artefact := filepath.Join(root, "test/acceptance", bug211TC+".bats")
	require.NoError(t, os.MkdirAll(filepath.Dir(artefact), 0755))
	require.NoError(t, os.WriteFile(artefact, []byte("# fixture artefact\n"), 0644))
	wantHash, err := pipeline.HashFile(artefact)
	require.NoError(t, err)

	rec := &wiring.WiringRecord{
		TestCase: bug211TC, TestCaseHash: specHash, Framework: "bats",
		Adapter: "bats-runner", Artefact: "test/acceptance/" + bug211TC + ".bats",
		ArtefactHash: wiring.PendingArtefactHash,
	}
	selectedPath := bug211SelectedPath(t, root)
	writeRawWiring(t, selectedPath, rec)
	sel := wiring.Selection{Record: rec, Path: selectedPath, TestCase: bug211TC, Framework: "bats"}

	got, err := bootstrapPendingWiring(root, sel, artefact)
	require.NoError(t, err)
	assert.Equal(t, wantHash, got, "returns the exact hash written")
	assert.Equal(t, wantHash, rec.ArtefactHash, "in-memory record takes the written hash")
	onDisk, _, err := wiring.Find(root, bug211TC, "bats")
	require.NoError(t, err)
	assert.Equal(t, wantHash, onDisk.ArtefactHash, "on-disk file takes the written hash")
}

// --- 6. A write failure leaves both the file and the in-memory record pending ---

func TestBUG211_PendingBootstrapWriteFailureKeepsPending(t *testing.T) {
	skipIfShort(t)
	root := setupCompatProject(t)
	folder := "bug211"
	enh231Spec(t, root, folder, bug211TC)
	specHash, err := pipeline.HashFile(filepath.Join(root, "gtms/test/cases", folder, bug211TC+"-x.md"))
	require.NoError(t, err)

	artefact := filepath.Join(root, "test/acceptance", bug211TC+".bats")
	require.NoError(t, os.MkdirAll(filepath.Dir(artefact), 0755))
	require.NoError(t, os.WriteFile(artefact, []byte("# fixture artefact\n"), 0644))

	rec := &wiring.WiringRecord{
		TestCase: bug211TC, TestCaseHash: specHash, Framework: "bats",
		Adapter: "bats-runner", Artefact: "test/acceptance/" + bug211TC + ".bats",
		ArtefactHash: wiring.PendingArtefactHash,
	}
	selectedPath := bug211SelectedPath(t, root)
	before := writeRawWiring(t, selectedPath, rec)
	sel := wiring.Selection{Record: rec, Path: selectedPath, TestCase: bug211TC, Framework: "bats"}

	// Make the wiring directory unwritable so wiring.Write's temp-file creation
	// fails. Skips where the OS cannot enforce it (mirrors makeUnreadable).
	makeDirUnwritable(t, filepath.Dir(selectedPath))

	_, err = bootstrapPendingWiring(root, sel, artefact)
	require.Error(t, err, "a write failure must surface")
	assert.Equal(t, wiring.PendingArtefactHash, rec.ArtefactHash,
		"the in-memory record must stay pending when the write fails")
	assert.Equal(t, string(before), string(readBytes(t, selectedPath)),
		"the on-disk file must stay pending when the write fails")
}

// makeDirUnwritable removes write access to dir, or skips when this platform or
// user cannot enforce it. Modelled on makeUnreadable in enh231_single_test.go.
func makeDirUnwritable(t *testing.T, dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		user := os.Getenv("USERNAME")
		if user == "" {
			t.Skip("cannot determine the Windows user to deny write access")
		}
		if out, err := exec.Command("icacls", dir, "/deny", user+":(W)").CombinedOutput(); err != nil {
			t.Skipf("icacls cannot deny write access here: %v: %s", err, out)
		}
		t.Cleanup(func() { _ = exec.Command("icacls", dir, "/remove:d", user).Run() })
	} else {
		require.NoError(t, os.Chmod(dir, 0o500))
		t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	}
	// Probe: if a temp file can still be created, the bits are not enforced.
	if f, err := os.CreateTemp(dir, "probe-*"); err == nil {
		name := f.Name()
		_ = f.Close()
		_ = os.Remove(name)
		t.Skip("write permission is not enforced for this user; cannot make the directory unwritable")
	}
}

// REV-234 CLAUDE-001: bulk --framework is validated exactly like the single
// path. On a case-insensitive filesystem --framework BATS would otherwise open
// tc--bats.wiring.yaml and be refused as a misleading identity mismatch.
func TestBUG211_BulkFrameworkFlagCaseIsValidatedNotIdentityMismatch(t *testing.T) {
	skipIfShort(t)
	root := setupCompatProject(t)
	folder := "bug211bulk"

	enh231Spec(t, root, folder, bug211Control)
	seedLegacyRecord(t, root, legacyRecord{TC: bug211Control, Framework: "bats", Adapter: "bats-runner"})
	controlPath := mustWiringPath(t, root, bug211Control)
	controlRaw := readBytes(t, controlPath)

	out, err := runBulkIdentity(t, root, folder, "BATS", false, false)
	require.Error(t, err, "an upper-case --framework must be refused: %s", out)
	assert.Contains(t, err.Error(), "Invalid framework 'BATS'")
	assert.Contains(t, err.Error(), "lowercase letters, digits, and hyphens")
	assert.NotContains(t, out, "identity", "the refusal is flag validation, not a wiring identity mismatch")

	assert.Equal(t, 0, countHandoffs(t, root), "no adapter ran")
	assert.Equal(t, 0, countExecuteTasks(t, root), "no execute task")
	assert.Equal(t, string(controlRaw), string(readBytes(t, controlPath)), "wiring unchanged")
}
