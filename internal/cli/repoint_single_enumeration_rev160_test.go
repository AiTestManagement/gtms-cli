package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/config"
)

// REV-160 CLAUDE-003 -- repoint performs at most ONE project-wide enumeration.
//
// ADR-024 s5a: "An operation performs at most one project-wide enumeration."
// A qualified single-TC repoint ran three (requireScopedSpec, refuseUnownedTarget,
// and RepointSingle's own NewSnapshot) and a folder or --all repoint ran two.
// That is a CORRECTNESS problem, not a performance one: each guard answered
// against a DIFFERENT snapshot, so the filesystem was free to change between the
// preflight decisions and the record written need not correspond to the
// ownership state that authorised the command.
//
// HOW THE COUNT IS TAKEN, without touching production code. This is the same
// instrument the ADR-024 bounded-enumeration guard uses (see
// internal/reader/adr024_bounded_enumeration_test.go), reduced to what this
// finding needs.
//
// A project-wide snapshot construction walks the WHOLE cases tree and reads every
// .md in it. So a folder of large DECOY specs, which a repoint of one test case
// in a different folder has no other reason to open, is read exactly once per
// enumeration. Running the identical command against two fixtures -- one with the
// decoy folder, one without -- and subtracting gives the decoy-attributable bytes
// exactly, with nothing assumed about what else the command reads. Dividing by
// the decoy folder's size gives the enumeration count.
//
// Bytes are counted from /proc/self/io (rchar), which counts bytes returned by
// read-family syscalls whether or not they came from the page cache. That makes
// the measurement Linux-only.

const (
	repointDecoySpecs     = 24
	repointDecoySpecBytes = 256 * 1024
)

func rcharNow(t *testing.T) int64 {
	t.Helper()
	data, err := os.ReadFile("/proc/self/io")
	require.NoError(t, err, "reading /proc/self/io")
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, "rchar:"); ok {
			n, perr := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			require.NoError(t, perr)
			return n
		}
	}
	t.Fatal("no rchar line in /proc/self/io")
	return 0
}

// seedRepointEnumerationProject builds a project with one wired test case under
// feat/, optionally alongside a folder of large decoy specs.
func seedRepointEnumerationProject(t *testing.T, withDecoys bool) string {
	t.Helper()
	root := setupCompatProject(t)

	specDir := filepath.Join(root, "gtms/test/cases/feat")
	require.NoError(t, os.MkdirAll(specDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(specDir, "tc-77777777-spec.md"),
		[]byte("---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-77777777\ntitle: t\n---\n# body\n"), 0o644))

	art := filepath.Join(root, "test/acceptance/feat/tc-77777777.bats")
	require.NoError(t, os.MkdirAll(filepath.Dir(art), 0o755))
	require.NoError(t, os.WriteFile(art, []byte("@test \"x\" { true; }\n"), 0o644))

	wiringDir := filepath.Join(root, "gtms/automation/wiring")
	require.NoError(t, os.MkdirAll(wiringDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(wiringDir, "tc-77777777--bats.wiring.yaml"),
		[]byte("testcase: tc-77777777\ntestcase-hash: 0011223344556677\nframework: bats\n"+
			"adapter: bats-runner\nartefact: test/acceptance/feat/tc-77777777.bats\n"+
			"artefact-hash: aabbccddeeff0011\n"), 0o644))

	if withDecoys {
		decoyDir := filepath.Join(root, "gtms/test/cases/decoy")
		require.NoError(t, os.MkdirAll(decoyDir, 0o755))
		pad := strings.Repeat("x", repointDecoySpecBytes)
		for i := 0; i < repointDecoySpecs; i++ {
			id := fmt.Sprintf("tc-decoy%03d", i)
			body := "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: " + id + "\ntitle: decoy\n---\n" + pad + "\n"
			require.NoError(t, os.WriteFile(filepath.Join(decoyDir, id+"-spec.md"), []byte(body), 0o644))
		}
	}
	return root
}

func twoAdapterConfig() *config.Config {
	return &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"bats-runner": {Framework: "bats", Mode: "sync", Command: "echo PASS"},
				"bats-other":  {Framework: "bats", Mode: "sync", Command: "echo PASS"},
			},
		},
		Defaults: map[string]string{"execute": "bats-runner"},
	}
}

// measureRepointDecoyBytes runs one repoint and returns the decoy-attributable
// bytes read: the difference between the same command against a fixture with the
// decoy folder and one without.
func measureRepointDecoyBytes(t *testing.T, args ...string) int64 {
	t.Helper()
	run := func(withDecoys bool) int64 {
		root := seedRepointEnumerationProject(t, withDecoys)
		restore := withExecuteGlobals(t, root, twoAdapterConfig())
		defer restore()

		before := rcharNow(t)
		_, err := runCmdCaptureStdout(t, newLinkCmd(), args...)
		after := rcharNow(t)
		require.NoError(t, err, "the repoint under measurement must succeed")
		return after - before
	}
	// Warm both paths first so one-off package/config reads do not land in the
	// measured half only.
	_ = run(false)
	_ = run(true)
	return run(true) - run(false)
}

// The finding's own case: a FOLDER-QUALIFIED single-TC repoint. It ran three
// enumerations before this change.
func TestRepoint_QualifiedSingleTC_PerformsExactlyOneEnumeration(t *testing.T) {
	skipIfShort(t)
	if runtime.GOOS != "linux" {
		t.Skip("/proc/self/io is Linux-only")
	}
	assertOneEnumeration(t, measureRepointDecoyBytes(t,
		"feat/tc-77777777", "--adapter", "bats-other"))
}

// The bare form ran two.
func TestRepoint_BareSingleTC_PerformsExactlyOneEnumeration(t *testing.T) {
	skipIfShort(t)
	if runtime.GOOS != "linux" {
		t.Skip("/proc/self/io is Linux-only")
	}
	assertOneEnumeration(t, measureRepointDecoyBytes(t,
		"tc-77777777", "--adapter", "bats-other"))
}

// And so did the folder form.
func TestRepoint_Folder_PerformsExactlyOneEnumeration(t *testing.T) {
	skipIfShort(t)
	if runtime.GOOS != "linux" {
		t.Skip("/proc/self/io is Linux-only")
	}
	assertOneEnumeration(t, measureRepointDecoyBytes(t,
		"feat", "--from-adapter", "bats-runner", "--adapter", "bats-other"))
}

func assertOneEnumeration(t *testing.T, decoyBytes int64) {
	t.Helper()
	oneEnumeration := int64(repointDecoySpecs) * int64(repointDecoySpecBytes)
	enumerations := float64(decoyBytes) / float64(oneEnumeration)

	// The instrument has to be working: a repoint DOES need one project-wide
	// enumeration, so zero decoy bytes would mean the measurement is blind, not
	// that the operation is free.
	assert.Greater(t, enumerations, 0.5,
		"measured %.2f enumerations (%d decoy bytes): the instrument read nothing, so the ceiling below proves nothing",
		enumerations, decoyBytes)

	// The gate. Two enumerations would land near 2.0; the pre-fix qualified path
	// measured three.
	assert.Less(t, enumerations, 1.5,
		"repoint performed %.2f project-wide enumerations (%d decoy bytes, one enumeration = %d bytes).\n"+
			"ADR-024 s5a allows at most one, and the point is correctness, not speed: independent snapshots let the filesystem change between preflight decisions, so the record written need not correspond to the ownership state that authorised the command.",
		enumerations, decoyBytes, oneEnumeration)
}
