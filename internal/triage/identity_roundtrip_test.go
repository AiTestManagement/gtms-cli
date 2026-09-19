package triage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func doubleDashRecord(tc string) *Record {
	return &Record{
		Version: SchemaVersion, TestCaseID: tc, Framework: "bats",
		Status: StatusActive, Value: "environment",
		ClassifiedAt: "2026-08-28T09:00:00Z",
		Snapshot:     Snapshot{Task: "task-11111111", Result: "fail"},
	}
}

// TestReadAcceptsWriteOutput_TCIDContainingDoubleDash pins the regression found
// in review of ENH-201: identity validation split the filename at the FIRST
// "--", so a test case ID that itself contains "--" was read back as a
// different (test case, framework) pair and Read refused a record Write had
// just produced.
//
// "tc-foo--bar" is a legal, linkable ID -- IsTestCaseID only requires the "tc-"
// prefix. wiring pins the same case in
// TestDiscoverForScope_TCIDContainingDoubleDash (REV-130 CODEX-130-001).
func TestReadAcceptsWriteOutput_TCIDContainingDoubleDash(t *testing.T) {
	for _, tc := range []string{"tc-foo--bar", "tc-a--b--c", "tc-11111111"} {
		t.Run(tc, func(t *testing.T) {
			root := t.TempDir()
			path, err := Write(root, doubleDashRecord(tc))
			require.NoError(t, err, "Write must accept a legal test case ID")

			got, err := Read(root, path)
			require.NoError(t, err,
				"Read must accept what Write produced; splitting the filename at the first %q breaks IDs that contain it", "--")
			assert.Equal(t, tc, got.TestCaseID)
			assert.Equal(t, "bats", got.Framework)
		})
	}
}

// TestReadRejectsBodyFilenameMismatch keeps the guard the round-trip fix must
// not weaken: a copied or hand-edited file whose name disagrees with its body
// is still malformed, and the error names the path.
func TestReadRejectsBodyFilenameMismatch(t *testing.T) {
	root := t.TempDir()
	path, err := Write(root, doubleDashRecord("tc-11111111"))
	require.NoError(t, err)

	// Copy the record under a name Write would never have produced for it.
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	wrong := filepath.Join(filepath.Dir(path), "tc-22222222--bats"+FileSuffix)
	require.NoError(t, os.WriteFile(wrong, data, 0644))

	_, err = Read(root, wrong)
	require.Error(t, err, "a body/filename identity mismatch must still be refused")
	assert.Contains(t, err.Error(), wrong, "the error must name the offending path")
}

// TestListAllIncludesUnsplittableFilenames pins the second review finding: the
// root enumerator dropped any name it could not split, so a malformed record
// vanished from the listing instead of failing it. Root list must see every
// .triage.yaml file; Read decides whether it is valid.
func TestListAllIncludesUnsplittableFilenames(t *testing.T) {
	root := t.TempDir()
	_, err := Write(root, doubleDashRecord("tc-11111111"))
	require.NoError(t, err)

	dir := filepath.Dir(filepath.Join(root, "gtms", "triage", "x"))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "broken"+FileSuffix),
		[]byte("version: 1\ntest_case_id: tc-22222222\nframework: bats\n"), 0644))
	// A non-triage file must still be ignored.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.md"), []byte("x"), 0644))

	refs, err := ListAll(root)
	require.NoError(t, err)

	var names []string
	for _, r := range refs {
		names = append(names, filepath.Base(r.Path))
	}
	assert.Len(t, refs, 2, "every .triage.yaml must be enumerated, splittable or not")
	assert.Contains(t, names, "broken"+FileSuffix,
		"an unsplittable name must be enumerated so Read can fail it by path, not silently dropped")
	assert.NotContains(t, names, "notes.md", "non-triage files stay ignored")
}
