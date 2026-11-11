package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/aitestmanagement/gtms-cli/internal/adapter"
	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// alreadyPassingOnDisk mirrors the production bulk path (BUG-209): it scans
// .gtms/results ONCE via result.OpenScan, builds the Source-1 candidates, and asks
// isAlreadyPassing -- so these tests exercise the same single-scan selection the
// bulk run uses, now that isAlreadyPassing no longer enumerates the directory itself.
func alreadyPassingOnDisk(root, tcID, framework string) bool {
	scan := result.OpenScan(root)
	cand := buildExecutePassCandidates(scan)
	scan.Close()
	return isAlreadyPassing(root, tcID, framework, cand[tcID])
}

// writeHandoffRaw writes an arbitrary handoff YAML body under root/.gtms/results
// and stamps its mtime.
func writeHandoffRaw(t *testing.T, root, name, body string, mtime time.Time) {
	t.Helper()
	dir := filepath.Join(root, ".gtms", "results")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, name+".handoff.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	require.NoError(t, os.Chtimes(path, mtime, mtime))
}

// REV-262 round 3: isAlreadyPassing must select the newest terminal execute
// contract deterministically -- newer stamp wins; on a stamp tie the newer
// handoff file mtime wins; on a full tie a non-pass outranks a pass (fail
// closed). The outcome must not depend on os.ReadDir order. These tests force
// ties with equal `completed` stamps and set mtimes with os.Chtimes (no sleeps),
// and assert the result in BOTH filename orders.

// writeHandoff writes a terminal execute handoff for tcID under root/.gtms/results
// and stamps its mtime. pass=true writes status: complete + result: pass; pass=
// false writes status: error with no result.
func writeHandoff(t *testing.T, root, name, tcID, completed string, pass bool, mtime time.Time) {
	t.Helper()
	dir := filepath.Join(root, ".gtms", "results")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	body := fmt.Sprintf("task: %s\ncommand: execute\ntarget: %s\nadapter: a\nmode: sync\nframework: bats\n", name, tcID)
	if pass {
		body += "status: complete\nresult: pass\n"
	} else {
		body += "status: error\n"
	}
	body += fmt.Sprintf("completed: %s\n", completed)
	path := filepath.Join(dir, name+".handoff.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	require.NoError(t, os.Chtimes(path, mtime, mtime))
}

func TestENH188_IsAlreadyPassing_Tiebreak(t *testing.T) {
	const tc = "tc-dddddddd"
	const stampA = "2026-11-05T10:00:00Z"
	const stampB = "2026-11-05T10:00:05Z"
	base := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	newer := base.Add(2 * time.Second)

	// Each case sets up two handoffs (a pass and an error) and asserts
	// isAlreadyPassing in both filename orders. passName/errName select which
	// file os.ReadDir (sorted by name) returns first.
	type setup struct {
		passStamp, errStamp string
		passMtime, errMtime time.Time
		want                bool
		why                 string
	}
	cases := map[string]setup{
		"same stamp, error newer mtime -> not passing": {
			passStamp: stampA, errStamp: stampA,
			passMtime: base, errMtime: newer,
			want: false, why: "newer handoff (error) wins on the stamp tie",
		},
		"same stamp, pass newer mtime -> passing": {
			passStamp: stampA, errStamp: stampA,
			passMtime: newer, errMtime: base,
			want: true, why: "newer handoff (pass) wins on the stamp tie",
		},
		"same stamp, same mtime -> not passing (fail closed)": {
			passStamp: stampA, errStamp: stampA,
			passMtime: base, errMtime: base,
			want: false, why: "full tie: a non-pass outranks a pass",
		},
		"pass newer stamp, error newer mtime -> passing (stamp wins)": {
			passStamp: stampB, errStamp: stampA,
			passMtime: base, errMtime: newer,
			want: true, why: "the newer stamp wins even when the other mtime is newer",
		},
		"error newer stamp, pass newer mtime -> not passing (stamp wins)": {
			passStamp: stampA, errStamp: stampB,
			passMtime: newer, errMtime: base,
			want: false, why: "the newer stamp wins even when the other mtime is newer",
		},
	}

	for name, s := range cases {
		s := s
		for _, order := range []struct {
			label              string
			passName, errName string
		}{
			{"pass file first", "task-1111-execute-" + tc, "task-2222-execute-" + tc},
			{"error file first", "task-2222-execute-" + tc, "task-1111-execute-" + tc},
		} {
			t.Run(name+"/"+order.label, func(t *testing.T) {
				root := t.TempDir()
				writeHandoff(t, root, order.passName, tc, s.passStamp, true, s.passMtime)
				writeHandoff(t, root, order.errName, tc, s.errStamp, false, s.errMtime)
				got := alreadyPassingOnDisk(root, tc, "bats")
				assert.Equal(t, s.want, got, s.why)
			})
		}
	}
}

// TestENH188_IsAlreadyPassing_NonTerminalAndEmptyStamp pins REV-263 fix (d) and
// CLAUDE-003: a newer non-terminal execute handoff counts as a non-pass (so a
// failed-write run cannot fall back to an older pass), and an entry with no
// usable stamp never counts as a prior pass.
func TestENH188_IsAlreadyPassing_NonTerminalAndEmptyStamp(t *testing.T) {
	const tc = "tc-dddddddd"
	base := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)

	pass := func(name, completed string) string {
		return fmt.Sprintf("task: %s\ncommand: execute\ntarget: %s\nadapter: a\nmode: sync\nframework: bats\nstatus: complete\nresult: pass\ncompleted: %s\n", name, tc, completed)
	}
	pendingWithCreated := func(name, created string) string {
		return fmt.Sprintf("task: %s\ncommand: execute\ntarget: %s\nadapter: a\nmode: sync\nframework: bats\nstatus: pending\ncreated: %s\n", name, tc, created)
	}
	passNoStamp := func(name string) string {
		return fmt.Sprintf("task: %s\ncommand: execute\ntarget: %s\nadapter: a\nmode: sync\nframework: bats\nstatus: complete\nresult: pass\n", name, tc)
	}

	t.Run("newer non-terminal execute handoff outranks an older pass", func(t *testing.T) {
		root := t.TempDir()
		writeHandoffRaw(t, root, "task-1111-execute-"+tc, pass("task-1111", "2026-11-05T10:00:00Z"), base)
		// A later pending execute handoff (e.g. a run whose durable error write failed).
		writeHandoffRaw(t, root, "task-2222-execute-"+tc, pendingWithCreated("task-2222", "2026-11-05T10:00:05Z"), base.Add(2*time.Second))
		assert.False(t, alreadyPassingOnDisk(root, tc, "bats"), "a newer non-terminal execute handoff must count as a non-pass")
	})

	t.Run("empty-stamp pass alone does not count", func(t *testing.T) {
		root := t.TempDir()
		writeHandoffRaw(t, root, "task-1111-execute-"+tc, passNoStamp("task-1111"), base)
		assert.False(t, alreadyPassingOnDisk(root, tc, "bats"), "a pass with no usable stamp must not count as a prior pass")
	})

	t.Run("stamped pass outranks an empty-stamp sibling", func(t *testing.T) {
		root := t.TempDir()
		writeHandoffRaw(t, root, "task-1111-execute-"+tc, passNoStamp("task-1111"), base)
		writeHandoffRaw(t, root, "task-2222-execute-"+tc, pass("task-2222", "2026-11-05T10:00:00Z"), base)
		assert.True(t, alreadyPassingOnDisk(root, tc, "bats"), "a stamped pass is the only rankable entry")
	})
}

// TestENH188_FormatExecuteOutput_WarningsReachStderr pins REV-263 CLAUDE-002 /
// CODEX-002: a contract-write warning on the error path reaches stderr, not just
// InvokeResult.Warnings.
func TestENH188_FormatExecuteOutput_WarningsReachStderr(t *testing.T) {
	res := &adapter.InvokeResult{
		Status:      "error",
		Summary:     "Execution result was not recorded: the adapter exited 0 without reporting a terminal outcome (result contract status: pending)",
		Remediation: "A Tier 2 execute adapter must write a terminal status to $GTMS_RESULT_FILE before it exits, and a result when status is complete",
		Warnings:    []string{"contract update failed: access is denied"},
		Target:      "tc-dddddddd",
		Filename:    "task-xyz-execute-tc-dddddddd.md",
	}

	old := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = w
	formatExecuteOutput(res)
	_ = w.Close()
	os.Stderr = old
	out, _ := io.ReadAll(r)

	got := string(out)
	assert.Contains(t, got, "contract update failed: access is denied", "the contract-write warning must reach stderr on the error path")
	// REV-264 proof 3: the bound diagnostic is still shown alongside the warning.
	assert.Contains(t, got, "Execution result was not recorded", "the bound diagnostic first line must still be shown")
	assert.Contains(t, got, "A Tier 2 execute adapter must write a terminal status to $GTMS_RESULT_FILE before it exits", "the bound remediation line must still be shown")
}

// TestENH188_RenderBulkContractWarnings_IncludesPipelineFailure pins REV-265
// CODEX-003: the bulk warning filter now admits the "Pipeline record could not be
// written" warning so bulk reports a failed trusted execution-record write.
func TestENH188_RenderBulkContractWarnings_IncludesPipelineFailure(t *testing.T) {
	old := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = w
	renderBulkContractWarnings([]string{
		"Pipeline record could not be written: open gtms/execution: not a directory",
		"Adapter produced no output.", // an unrelated advisory must NOT be rendered here
	})
	_ = w.Close()
	os.Stderr = old
	out, _ := io.ReadAll(r)
	got := string(out)
	assert.Contains(t, got, "Pipeline record could not be written", "the pipeline-record failure must reach bulk stderr")
	assert.NotContains(t, got, "Adapter produced no output", "unrelated advisories stay out of the bulk contract-warning filter")
}

// writeExecRecord writes a trusted per-test execution record under gtms/execution/
// for (tcID, framework) with the given outcome and completed_at, and stamps mtime.
func writeExecRecord(t *testing.T, root, name, tcID, framework, outcome, completedAt string, mtime time.Time) {
	t.Helper()
	dir := layout.ExecutionDir(root)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	body := fmt.Sprintf("schema_version: \"0.1\"\ntask_id: %s\nframework: %s\ncompleted_at: %s\nresults:\n  - tc_id: %s\n    outcome: %s\n", name, framework, completedAt, tcID, outcome)
	path := filepath.Join(dir, name+"--"+tcID+".results.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	require.NoError(t, os.Chtimes(path, mtime, mtime))
}

// TestENH188_IsAlreadyPassing_ExecutionRecordVetoOnly pins REV-264 CODEX-002 and
// REV-265 CLAUDE-001: execution records (gtms/execution/, which are committed and
// survive a fresh clone) are VETO-ONLY. Only a local handoff can positively
// establish "already passing"; a newer applicable NON-pass execution record may
// veto that skip; a PASS execution record never establishes or strengthens one.
func TestENH188_IsAlreadyPassing_ExecutionRecordVetoOnly(t *testing.T) {
	const tc = "tc-dddddddd"
	base := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	passHandoff := func(name, completed string) string {
		return fmt.Sprintf("task: %s\ncommand: execute\ntarget: %s\nadapter: a\nmode: sync\nframework: bats\nstatus: complete\nresult: pass\ncompleted: %s\n", name, tc, completed)
	}
	errHandoff := func(name, completed string) string {
		return fmt.Sprintf("task: %s\ncommand: execute\ntarget: %s\nadapter: a\nmode: sync\nframework: bats\nstatus: error\ncompleted: %s\n", name, tc, completed)
	}

	t.Run("newer error execution record vetoes an older pass handoff", func(t *testing.T) {
		root := t.TempDir()
		writeHandoffRaw(t, root, "task-1111-execute-"+tc, passHandoff("task-1111", "2026-11-05T10:00:00Z"), base)
		// An identity-free leftover handoff (no target) cannot be associated, but
		// the trusted execution record carries tc + framework + outcome error.
		writeHandoffRaw(t, root, "task-2222-execute-"+tc, "status: pending\n", base.Add(time.Second))
		writeExecRecord(t, root, "task-2222", tc, "bats", "error", "2026-11-05T10:00:05Z", base.Add(2*time.Second))
		assert.False(t, alreadyPassingOnDisk(root, tc, "bats"), "a newer error execution record must veto an older pass")
	})

	t.Run("pass execution record with no handoff does not establish passing (fresh clone)", func(t *testing.T) {
		root := t.TempDir()
		// No .gtms/results/ handoff -- as on a fresh checkout. Only a committed
		// pass execution record exists; it must NOT make the test already passing.
		writeExecRecord(t, root, "task-1111", tc, "bats", "pass", "2026-11-05T10:00:00Z", base)
		assert.False(t, alreadyPassingOnDisk(root, tc, "bats"), "a committed pass record alone must not establish already-passing")
	})

	t.Run("newer pass execution record does not strengthen an older non-pass handoff", func(t *testing.T) {
		root := t.TempDir()
		writeHandoffRaw(t, root, "task-1111-execute-"+tc, errHandoff("task-1111", "2026-11-05T10:00:00Z"), base)
		writeExecRecord(t, root, "task-2222", tc, "bats", "pass", "2026-11-05T10:00:05Z", base.Add(2*time.Second))
		assert.False(t, alreadyPassingOnDisk(root, tc, "bats"), "a newer pass execution record must not flip an older non-pass handoff")
	})

	t.Run("local pass handoff establishes passing; a pass execution record is irrelevant", func(t *testing.T) {
		root := t.TempDir()
		writeHandoffRaw(t, root, "task-1111-execute-"+tc, passHandoff("task-1111", "2026-11-05T10:00:00Z"), base)
		writeExecRecord(t, root, "task-2222", tc, "bats", "pass", "2026-11-05T10:00:05Z", base.Add(2*time.Second))
		assert.True(t, alreadyPassingOnDisk(root, tc, "bats"), "a local pass handoff establishes passing")
	})

	t.Run("error execution record under a different framework does not veto", func(t *testing.T) {
		root := t.TempDir()
		writeHandoffRaw(t, root, "task-1111-execute-"+tc, passHandoff("task-1111", "2026-11-05T10:00:00Z"), base)
		writeExecRecord(t, root, "task-2222", tc, "playwright", "error", "2026-11-05T10:00:05Z", base.Add(2*time.Second))
		assert.True(t, alreadyPassingOnDisk(root, tc, "bats"), "a different-framework error must not affect the bats pass")
	})
}

// TestENH188_IsAlreadyPassing_LinkedExecutionDirFailsOpen pins REV-265 CODEX-001:
// a linked gtms/execution directory (symlink on Unix, junction on Windows) fails
// OPEN -- isAlreadyPassing returns false so a redirected directory can never
// suppress a run -- even when a local pass handoff exists.
func TestENH188_IsAlreadyPassing_LinkedExecutionDirFailsOpen(t *testing.T) {
	const tc = "tc-dddddddd"
	base := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	root := t.TempDir()
	// A local pass handoff would otherwise establish already-passing.
	writeHandoffRaw(t, root, "task-1111-execute-"+tc,
		fmt.Sprintf("task: task-1111\ncommand: execute\ntarget: %s\nadapter: a\nmode: sync\nframework: bats\nstatus: complete\nresult: pass\ncompleted: 2026-11-05T10:00:00Z\n", tc), base)

	// Point gtms/execution at a different real directory via a link.
	realTarget := t.TempDir()
	execDir := layout.ExecutionDir(root)
	require.NoError(t, os.MkdirAll(filepath.Dir(execDir), 0o755))
	if err := os.Symlink(realTarget, execDir); err != nil {
		t.Skipf("platform/environment cannot create a directory link here: %v", err)
	}

	assert.False(t, alreadyPassingOnDisk(root, tc, "bats"), "a linked execution directory must fail open (never skip)")
}

// TestENH188_IsAlreadyPassing_SpecialEntriesNotOpened pins REV-265 CODEX-001: a
// symlink or FIFO entry in gtms/execution must not be opened (execution.Read
// must never follow a link or block on a device). A FIFO would block a naive
// read, so this runs under a deadline. Unix-only (FIFO/symlink semantics).
func TestENH188_IsAlreadyPassing_SpecialEntriesNotOpened(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("FIFO/symlink entry test is Unix-only")
	}
	const tc = "tc-dddddddd"
	root := t.TempDir()
	execDir := layout.ExecutionDir(root)
	require.NoError(t, os.MkdirAll(execDir, 0o755))

	// A valid pass handoff, so a followed link would visibly change the result.
	base := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	writeHandoff(t, root, "task-1111-execute-"+tc, tc, "2026-11-05T10:00:00Z", true, base)

	// A symlink entry pointing outside, and a FIFO entry: neither may be opened.
	// REV-270 CLAUDE-001: both are named for this test case, so they pass the
	// filename filter and reach the regular-file check this test exists to pin.
	// The link's target is a newer NON-pass record: pass records are veto-only, so
	// only a non-pass target can show that the link was followed.
	outside := filepath.Join(t.TempDir(), "evil.results.yaml")
	require.NoError(t, os.WriteFile(outside, []byte("schema_version: \"0.1\"\ntask_id: x\nframework: bats\ncompleted_at: 2099-01-01T00:00:00Z\nresults:\n  - tc_id: "+tc+"\n    outcome: error\n"), 0o644))
	if err := os.Symlink(outside, filepath.Join(execDir, "task-link--"+tc+".results.yaml")); err != nil {
		t.Skipf("cannot create symlink entry here: %v", err)
	}
	fifoPath := filepath.Join(execDir, "task-fifo--"+tc+".results.yaml")
	if err := unixMkfifo(fifoPath); err != nil {
		t.Skipf("cannot create FIFO entry here: %v", err)
	}

	done := make(chan bool, 1)
	go func() {
		// Must not open the symlink (which would inject an outside veto) nor block
		// on the FIFO.
		done <- alreadyPassingOnDisk(root, tc, "bats")
	}()
	select {
	case got := <-done:
		// The symlink's outside error must be ignored (not opened), so the pass
		// handoff still stands.
		assert.True(t, got, "special entries must be skipped, not opened")
	case <-time.After(5 * time.Second):
		t.Fatal("isAlreadyPassing blocked -- a FIFO entry was opened")
	}
}
