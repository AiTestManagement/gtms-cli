package reader

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/testcase"
)

func TestParseTestCaseFile_UsesNormalizedV2Metadata(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("gtms/test/cases/feat", "tc-12345678-v2.md"),
		readerV2Fixture("tc-12345678", "Security"))

	snap, err := testcase.NewSnapshot(root)
	require.NoError(t, err)
	owner, err := snap.RequireOwner("tc-12345678")
	require.NoError(t, err)
	tc, badField := testCaseFromNormalized(owner.Metadata(), owner.DecodeFailure())
	require.Empty(t, badField)
	require.NotNil(t, tc)
	assert.Equal(t, "tc-12345678", tc.ID)
	assert.Equal(t, "Normalized title", tc.Title)
	assert.Equal(t, "ENH-219", tc.Requirement)
	assert.Equal(t, "High", tc.Priority)
	assert.Equal(t, "Security", tc.Type)
	assert.Equal(t, []string{"schema", "reader"}, tc.Tags)
	assert.Equal(t, "2026-09-26", tc.Created)
}

func TestScanTestCases_StrictSchemaWarnsAndOmitsWhileDeferred(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("gtms/test/cases/feat", "tc-23456789-valid.md"),
		readerV2Fixture("tc-23456789", "Functional"))
	writeFile(t, root, filepath.Join("gtms/test/cases/feat", "tc-34567890-invalid.md"),
		readerV2Fixture("tc-34567890", ""))

	entries, diags, err := PipelineStatusWithDiagnostics(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "tc-23456789", entries[0].TestCaseID)
	text := diagText(diags)
	assert.Contains(t, text, "ownership of test case ID 'tc-34567890' cannot be proved")
	assert.Contains(t, text, "set test_type to a non-empty category")
	assert.NotContains(t, text, "ownership of a candidate spec under")
}

func TestScanTestCases_FrontmatteredCompanionsStaySilent(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("gtms/test/cases/feat", "tc-45678901-valid.md"),
		readerV2Fixture("tc-45678901", "Functional"))
	companion := "---\ntype: Reference\ntitle: Notes\n---\n"
	writeFile(t, root, filepath.Join("gtms/test/cases/feat", "README.md"), companion)
	writeFile(t, root, filepath.Join("gtms/test/cases/feat", "note.md"), companion)
	writeFile(t, root, filepath.Join("gtms/test/cases/feat/nested", "guide.md"), companion)

	entries, diags, err := PipelineStatusWithDiagnostics(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "tc-45678901", entries[0].TestCaseID)
	assert.False(t, diags.Affected(), "frontmattered reference companions must stay silent")
}

func TestDirectDetail_SchemaFailureIsUnprovableNotAbsent(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("gtms/test/cases/feat", "tc-56789012-invalid.md"),
		readerV2Fixture("tc-56789012", ""))

	_, err := PipelineDetail(root, "tc-56789012", "", false)
	require.Error(t, err)
	assert.False(t, errors.Is(err, testcase.ErrOwnerNotFound))
	var ownershipErr *testcase.OwnershipError
	require.ErrorAs(t, err, &ownershipErr)
	assert.Equal(t, testcase.OutcomeUnprovable, ownershipErr.Outcome)
	assert.Contains(t, err.Error(), "set test_type to a non-empty category")
}

func readerV2Fixture(id, testType string) string {
	lines := []string{
		"---",
		"gtms_schema: test-case/v2",
		"type: Test Case Spec",
	}
	if testType != "" {
		lines = append(lines, "test_type: "+testType)
	}
	lines = append(lines,
		"test_case_id: "+id,
		"title: Normalized title",
		"requirement: ENH-219",
		"priority: High",
		"created: 2026-09-26",
		"tags: [schema, reader]",
		"---",
		"",
	)
	return strings.Join(lines, "\n")
}
