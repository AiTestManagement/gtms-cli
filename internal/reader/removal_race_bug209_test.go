package reader

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/result"
)

// BUG-209 section 8: an entry that becomes unsafe between the scan and its removal is
// left untouched and reported as a refusal by both reset and delete, so both exit
// non-zero in the same way, and the rest of the cleanup continues. A hard link stands in
// for the change because creating one needs no privilege on any platform.

func removalRaceProject(t *testing.T) (root, unsafe, safe string) {
	t.Helper()
	root = t.TempDir()
	dir := filepath.Join(root, ".gtms", "results")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	unsafe = filepath.Join(dir, "task-aaaaaaaa.handoff.yaml")
	safe = filepath.Join(dir, "task-bbbbbbbb.handoff.yaml")
	body := "task: %s\ncommand: execute\ntarget: tc-11111111\nadapter: a\nmode: sync\nframework: bats\nstatus: complete\nresult: pass\n"
	require.NoError(t, os.WriteFile(unsafe, []byte(fmt.Sprintf(body, "task-aaaaaaaa")), 0o644))
	require.NoError(t, os.WriteFile(safe, []byte(fmt.Sprintf(body, "task-bbbbbbbb")), 0o644))
	return root, unsafe, safe
}

func TestBUG209_ResetEntryBecomingUnsafeIsRefusedAndCleanupContinues(t *testing.T) {
	root, unsafe, safe := removalRaceProject(t)
	scan := result.OpenScan(root)
	defer scan.Close()
	require.Len(t, scan.Entries, 2)

	require.NoError(t, os.Link(unsafe, filepath.Join(t.TempDir(), "other-link")))

	res := &ResetResult{}
	cleared, err := clearAutomationRecords(scan, "tc-11111111", false, res)
	require.NoError(t, err, "a refusal is reported on the result, not as a failed command")
	assert.Equal(t, 1, cleared, "only the safe handoff counts as cleared")
	assert.FileExists(t, unsafe, "the entry that became unsafe must be left untouched")
	assert.NoFileExists(t, safe, "the rest of the cleanup must continue")
	require.Len(t, res.HandoffRefusals, 1)
	assert.Equal(t, "refused handoff .gtms/results/task-aaaaaaaa.handoff.yaml: has more than one hard link", res.HandoffRefusals[0].FirstLine())
	assert.True(t, res.Incomplete(), "the exit must be non-zero")
}

func TestBUG209_DeleteEntryBecomingUnsafeIsRefusedAndCleanupContinues(t *testing.T) {
	root, unsafe, safe := removalRaceProject(t)
	plan := &deletePlan{projectRoot: root, resultContracts: []string{unsafe, safe}}

	require.NoError(t, os.Link(unsafe, filepath.Join(t.TempDir(), "other-link")))

	res := &DeleteResult{}
	require.NoError(t, removeResultContracts(plan, res, false))
	assert.Equal(t, 1, res.ResultContracts, "only the safe handoff counts as removed")
	assert.FileExists(t, unsafe, "the entry that became unsafe must be left untouched")
	assert.NoFileExists(t, safe, "the rest of the cleanup must continue")
	require.Len(t, res.HandoffRefusals, 1)
	assert.Equal(t, "refused handoff .gtms/results/task-aaaaaaaa.handoff.yaml: has more than one hard link", res.HandoffRefusals[0].FirstLine())
	assert.True(t, res.Incomplete(), "the exit must be non-zero")
}

// Section 4: a directory refused at execution stops handoff removal and is reported,
// though it was fine when the plan was built.
func TestBUG209_DeleteDirectoryRefusedAtExecutionRemovesNothing(t *testing.T) {
	root, unsafe, safe := removalRaceProject(t)
	plan := &deletePlan{projectRoot: root, resultContracts: []string{unsafe, safe}}

	result.InjectScanReadFailureForTest(assert.AnError)
	t.Cleanup(func() { result.InjectScanReadFailureForTest(nil) })

	res := &DeleteResult{}
	require.NoError(t, removeResultContracts(plan, res, false))
	assert.Zero(t, res.ResultContracts)
	assert.FileExists(t, unsafe)
	assert.FileExists(t, safe)
	require.NotNil(t, res.DirRefusal)
	assert.True(t, res.Incomplete(), "the exit must be non-zero")
}
