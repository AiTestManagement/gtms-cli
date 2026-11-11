package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aitestmanagement/gtms-cli/internal/execution"
	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeRawExecRecord writes an execution record under an exact filename, so a
// test can give a record a name that disagrees with the test case inside it.
func writeRawExecRecord(t *testing.T, root, filename, tcID, outcome, completedAt string, mtime time.Time) {
	t.Helper()
	dir := layout.ExecutionDir(root)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	body := fmt.Sprintf("schema_version: \"0.1\"\ntask_id: task-x\nframework: bats\ncompleted_at: %s\nresults:\n  - tc_id: %s\n    outcome: %s\n", completedAt, tcID, outcome)
	path := filepath.Join(dir, filename)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	require.NoError(t, os.Chtimes(path, mtime, mtime))
}

// TestREV270_ExecutionRecordFilenameFilter pins REV-270 chunk-004-001: only a
// file whose name ends in --<tcID>.results.yaml can belong to the test case, and
// the decoded tc_id stays the authoritative check on the files that pass.
func TestREV270_ExecutionRecordFilenameFilter(t *testing.T) {
	const tc = "tc-dddddddd"
	const other = "tc-eeeeeeee"
	base := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	const passStamp = "2026-11-05T10:00:00Z"
	const laterStamp = "2026-11-05T10:00:05Z"

	t.Run("a record named for another test case is not read", func(t *testing.T) {
		root := t.TempDir()
		writeHandoff(t, root, "task-1111-execute-"+tc, tc, passStamp, true, base)
		// The content would veto the skip, so a true result proves the name alone
		// excluded this file: had it been parsed, the result would be false.
		writeRawExecRecord(t, root, "task-2222--"+other+".results.yaml", tc, "error", laterStamp, base.Add(time.Second))
		assert.True(t, alreadyPassingOnDisk(root, tc, "bats"), "a file named for another test case must be rejected before parsing")
	})

	t.Run("a correctly named record for another test case does not veto", func(t *testing.T) {
		root := t.TempDir()
		writeHandoff(t, root, "task-1111-execute-"+tc, tc, passStamp, true, base)
		writeRawExecRecord(t, root, "task-2222--"+tc+".results.yaml", other, "error", laterStamp, base.Add(time.Second))
		assert.True(t, alreadyPassingOnDisk(root, tc, "bats"), "the decoded tc_id stays authoritative after the name check")
	})

	t.Run("a correctly named record for this test case still vetoes", func(t *testing.T) {
		root := t.TempDir()
		writeHandoff(t, root, "task-1111-execute-"+tc, tc, passStamp, true, base)
		writeRawExecRecord(t, root, "task-2222--"+tc+".results.yaml", tc, "error", laterStamp, base.Add(time.Second))
		assert.False(t, alreadyPassingOnDisk(root, tc, "bats"), "the filter must not drop this test case's own records")
	})

	t.Run("the suffix matches what ResultsFilePath writes", func(t *testing.T) {
		path, err := execution.ResultsFilePath(t.TempDir(), "task-1111", tc)
		require.NoError(t, err)
		assert.True(t, strings.HasSuffix(path, execution.ResultsFileSuffix(tc)))
	})
}

// TestREV270_ExecutionRecordFilterRunsBeforeAnyRead locks the order a behaviour
// test cannot see without timing: the name check must come before DirEntry.Info
// and execution.Read in the execution-record scan, or a bulk run still pays to
// parse every record once per test case.
func TestREV270_ExecutionRecordFilterRunsBeforeAnyRead(t *testing.T) {
	raw, err := os.ReadFile("execute.go")
	require.NoError(t, err)
	src := string(raw)
	_, scan, found := strings.Cut(src, "// Source 2: trusted execution records")
	require.True(t, found, "execution-record scan not found in execute.go")

	filter := strings.Index(scan, "strings.HasSuffix(e.Name(), recordSuffix)")
	info := strings.Index(scan, "e.Info()")
	read := strings.Index(scan, "execution.Read(")
	require.GreaterOrEqual(t, filter, 0, "the filename filter is missing")
	require.GreaterOrEqual(t, info, 0)
	require.GreaterOrEqual(t, read, 0)
	assert.Less(t, filter, info, "the filename filter must run before DirEntry.Info")
	assert.Less(t, filter, read, "the filename filter must run before execution.Read")
}

// TestREV270_StampsCompareAsInstants pins REV-270 synthesis-004: selection compares
// the moments stamps name, not their text. Each case runs in both filename orders
// so the result cannot depend on directory read order.
func TestREV270_StampsCompareAsInstants(t *testing.T) {
	const tc = "tc-dddddddd"
	base := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	newer := base.Add(2 * time.Second)

	cases := map[string]struct {
		passStamp, errStamp string
		passMtime, errMtime time.Time
		want                bool
		why                 string
	}{
		"positive-offset pass, later UTC rejection": {
			passStamp: "2026-11-06T11:00:00+01:00", errStamp: "2026-11-06T10:30:00Z",
			passMtime: newer, errMtime: base,
			want: false, why: "the pass is 10:00Z and the rejection 10:30Z, so the rejection is newer",
		},
		"negative-offset pass, earlier UTC rejection": {
			passStamp: "2026-11-06T09:00:00-02:00", errStamp: "2026-11-06T10:30:00Z",
			passMtime: base, errMtime: newer,
			want: true, why: "the pass is 11:00Z and the rejection 10:30Z, so the pass is newer",
		},
		"fractional-second rejection after a whole-second pass": {
			passStamp: "2026-11-06T10:00:00Z", errStamp: "2026-11-06T10:00:00.5Z",
			passMtime: newer, errMtime: base,
			want: false, why: "half a second later is later, whatever the text sorts as",
		},
		"same instant, different offsets, rejection has newer mtime": {
			passStamp: "2026-11-06T11:00:00+01:00", errStamp: "2026-11-06T10:00:00Z",
			passMtime: base, errMtime: newer,
			want: false, why: "equal instants fall to the mtime tie break",
		},
		"same instant, different offsets, pass has newer mtime": {
			passStamp: "2026-11-06T10:00:00Z", errStamp: "2026-11-06T11:00:00+01:00",
			passMtime: newer, errMtime: base,
			want: true, why: "equal instants fall to the mtime tie break",
		},
		"same instant, different offsets, same mtime": {
			passStamp: "2026-11-06T11:00:00+01:00", errStamp: "2026-11-06T10:00:00Z",
			passMtime: base, errMtime: base,
			want: false, why: "on a full tie a non-pass outranks a pass",
		},
	}
	for name, c := range cases {
		c := c
		for _, order := range []struct{ label, passName, errName string }{
			{"pass file first", "task-1111-execute-" + tc, "task-2222-execute-" + tc},
			{"error file first", "task-2222-execute-" + tc, "task-1111-execute-" + tc},
		} {
			t.Run(name+"/"+order.label, func(t *testing.T) {
				root := t.TempDir()
				writeHandoff(t, root, order.passName, tc, c.passStamp, true, c.passMtime)
				writeHandoff(t, root, order.errName, tc, c.errStamp, false, c.errMtime)
				assert.Equal(t, c.want, alreadyPassingOnDisk(root, tc, "bats"), c.why)
			})
		}
	}

	t.Run("a later UTC execution record vetoes a positive-offset pass", func(t *testing.T) {
		root := t.TempDir()
		writeHandoff(t, root, "task-1111-execute-"+tc, tc, "2026-11-06T11:00:00+01:00", true, newer)
		writeExecRecord(t, root, "task-2222", tc, "bats", "error", "2026-11-06T10:30:00Z", base)
		assert.False(t, alreadyPassingOnDisk(root, tc, "bats"), "the veto must also compare instants")
	})
}

// TestREV270_MalformedStampNeverEstablishesSkip pins the other half of REV-270
// synthesis-004: a stamp that cannot be placed in time cannot support a skip, so
// the test case runs. Empty stamps keep their existing meaning and do not count.
func TestREV270_MalformedStampNeverEstablishesSkip(t *testing.T) {
	const tc = "tc-dddddddd"
	const good = "2026-11-06T10:00:00Z"
	const bad = "yesterday-ish"
	base := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)

	t.Run("a malformed pass alone", func(t *testing.T) {
		root := t.TempDir()
		writeHandoff(t, root, "task-1111-execute-"+tc, tc, bad, true, base)
		assert.False(t, alreadyPassingOnDisk(root, tc, "bats"))
	})
	t.Run("a malformed pass beside a valid pass", func(t *testing.T) {
		root := t.TempDir()
		writeHandoff(t, root, "task-1111-execute-"+tc, tc, good, true, base)
		writeHandoff(t, root, "task-2222-execute-"+tc, tc, bad, true, base)
		assert.False(t, alreadyPassingOnDisk(root, tc, "bats"))
	})
	t.Run("a malformed rejection beside a valid pass", func(t *testing.T) {
		root := t.TempDir()
		writeHandoff(t, root, "task-1111-execute-"+tc, tc, good, true, base)
		writeHandoff(t, root, "task-2222-execute-"+tc, tc, bad, false, base)
		assert.False(t, alreadyPassingOnDisk(root, tc, "bats"), "it may be the newer run, so it cannot be ignored")
	})
	t.Run("a malformed created stamp on a non-terminal handoff", func(t *testing.T) {
		root := t.TempDir()
		writeHandoff(t, root, "task-1111-execute-"+tc, tc, good, true, base)
		body := fmt.Sprintf("task: task-2222\ncommand: execute\ntarget: %s\nadapter: a\nmode: sync\nframework: bats\nstatus: pending\ncreated: %s\n", tc, bad)
		writeHandoffRaw(t, root, "task-2222-execute-"+tc, body, base)
		assert.False(t, alreadyPassingOnDisk(root, tc, "bats"))
	})
	t.Run("a malformed non-pass execution record", func(t *testing.T) {
		root := t.TempDir()
		writeHandoff(t, root, "task-1111-execute-"+tc, tc, good, true, base)
		writeExecRecord(t, root, "task-2222", tc, "bats", "error", bad, base)
		assert.False(t, alreadyPassingOnDisk(root, tc, "bats"))
	})
	t.Run("a malformed pass execution record stays irrelevant", func(t *testing.T) {
		root := t.TempDir()
		writeHandoff(t, root, "task-1111-execute-"+tc, tc, good, true, base)
		writeExecRecord(t, root, "task-2222", tc, "bats", "pass", bad, base)
		assert.True(t, alreadyPassingOnDisk(root, tc, "bats"), "pass records are veto-only and are never ranked")
	})
	t.Run("a malformed stamp under another framework does not apply", func(t *testing.T) {
		root := t.TempDir()
		writeHandoff(t, root, "task-1111-execute-"+tc, tc, good, true, base)
		body := fmt.Sprintf("task: task-2222\ncommand: execute\ntarget: %s\nadapter: a\nmode: sync\nframework: playwright\nstatus: error\ncompleted: %s\n", tc, bad)
		writeHandoffRaw(t, root, "task-2222-execute-"+tc, body, base)
		assert.True(t, alreadyPassingOnDisk(root, tc, "bats"))
	})
}
