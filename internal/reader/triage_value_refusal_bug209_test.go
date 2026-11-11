package reader

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BUG-209 AC30: gtms triage <tc> --value refuses on any refused handoff or refused
// results directory, and leaves all triage state unchanged.

func triageValueProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeTriageSpec(t, root, "tc-11111111")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-11111111", Framework: "bats", Result: "fail"})
	return root
}

func addStrayHardLink(t *testing.T, root string) string {
	t.Helper()
	target := filepath.Join(t.TempDir(), "outside.yaml")
	require.NoError(t, os.WriteFile(target, []byte("status: complete\n"), 0o644))
	stray := filepath.Join(root, ".gtms", "results", "task-99999999.handoff.yaml")
	require.NoError(t, os.Link(target, stray))
	return stray
}

func requireRefusedWith(t *testing.T, err error, want string) {
	t.Helper()
	var refused *IncompleteReadError
	require.True(t, errors.As(err, &refused), "expected IncompleteReadError, got %v", err)
	var b strings.Builder
	refused.Diagnostics.Render(&b)
	assert.Equal(t, 1, strings.Count(b.String(), want), "the refusal must appear once: %s", b.String())
}

func TestBUG209_TriageValueRefusesAndWritesNoRecord(t *testing.T) {
	root := triageValueProject(t)
	addStrayHardLink(t, root)

	_, err := ClassifyTriage(root, "tc-11111111", "environment", "", "", "bats", triageTestValues())
	requireRefusedWith(t, err, "refused handoff .gtms/results/task-99999999.handoff.yaml: has more than one hard link")

	_, statErr := os.Lstat(triageRecordPath(root, "tc-11111111", "bats"))
	assert.True(t, os.IsNotExist(statErr), "a refused classification must not write a record")
}

func TestBUG209_TriageValueRefusalLeavesExistingRecordUnchanged(t *testing.T) {
	root := triageValueProject(t)

	// Positive control: with a clean scan the same classification succeeds.
	_, err := ClassifyTriage(root, "tc-11111111", "environment", "first", "", "bats", triageTestValues())
	require.NoError(t, err)
	path := triageRecordPath(root, "tc-11111111", "bats")
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	addStrayHardLink(t, root)
	_, err = ClassifyTriage(root, "tc-11111111", "test-wrong", "second", "", "bats", triageTestValues())
	requireRefusedWith(t, err, "refused handoff .gtms/results/task-99999999.handoff.yaml: has more than one hard link")

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "a refused classification must leave the record unchanged")
}

func TestBUG209_TriageValueRefusesOnScanFailure(t *testing.T) {
	root := triageValueProject(t)
	result.InjectScanReadFailureForTest(errors.New("injected list failure"))
	t.Cleanup(func() { result.InjectScanReadFailureForTest(nil) })

	_, err := ClassifyTriage(root, "tc-11111111", "environment", "", "", "bats", triageTestValues())
	requireRefusedWith(t, err, "refused results directory .gtms/results: could not be inspected: injected list failure")
}

func TestBUG209_TriageValueMissingResultsDirIsNormal(t *testing.T) {
	root := triageValueProject(t)
	require.NoError(t, os.RemoveAll(filepath.Join(root, ".gtms", "results")))

	_, err := ClassifyTriage(root, "tc-11111111", "environment", "", "", "bats", triageTestValues())
	require.Error(t, err)
	var refused *IncompleteReadError
	assert.False(t, errors.As(err, &refused), "a missing results directory is not a refusal: %v", err)
	assert.Contains(t, err.Error(), "no execution result is available to classify")
}
