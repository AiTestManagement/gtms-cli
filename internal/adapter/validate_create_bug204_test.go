package adapter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aitestmanagement/gtms-cli/internal/testcase"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bug204CraftedCreateOutput is a create adapter output whose meta mapping holds
// the BUG-204 nine-level, nine-way alias chain with the alias used as a key.
const bug204CraftedCreateOutput = "---\n" +
	"gtms_schema: \"test-case/v2\"\n" +
	"type: Test Case Spec\n" +
	"test_type: Functional\n" +
	"test_case_id: tc-11111111\n" +
	"title: \"Crafted create output\"\n" +
	"priority: High\n" +
	"created: 2026-11-05\n" +
	"meta:\n" +
	"  a: &a [\"x\",\"x\",\"x\",\"x\",\"x\",\"x\",\"x\",\"x\",\"x\"]\n" +
	"  b: &b [*a,*a,*a,*a,*a,*a,*a,*a,*a]\n" +
	"  c: &c [*b,*b,*b,*b,*b,*b,*b,*b,*b]\n" +
	"  d: &d [*c,*c,*c,*c,*c,*c,*c,*c,*c]\n" +
	"  e: &e [*d,*d,*d,*d,*d,*d,*d,*d,*d]\n" +
	"  f: &f [*e,*e,*e,*e,*e,*e,*e,*e,*e]\n" +
	"  g: &g [*f,*f,*f,*f,*f,*f,*f,*f,*f]\n" +
	"  h: &h [*g,*g,*g,*g,*g,*g,*g,*g,*g]\n" +
	"  i: &i [*h,*h,*h,*h,*h,*h,*h,*h,*h]\n" +
	"  ? *i\n" +
	"  : refused\n" +
	"---\n\n## Test Objective\nCrafted adapter output.\n"

// TestBUG204_CreateSchemaFailureReason confirms the shared decoder bound renders
// the create "could not parse frontmatter" classification (AC5).
func TestBUG204_CreateSchemaFailureReason(t *testing.T) {
	decoded := testcase.DecodeCandidate([]byte(bug204CraftedCreateOutput))
	require.NotNil(t, decoded.Failure)
	assert.Equal(t, testcase.SchemaFailureMalformedYAML, decoded.Failure.Kind)
	assert.Equal(t, "", decoded.Failure.Key)
	assert.Contains(t, createSchemaFailureReason(decoded.Failure), "could not parse frontmatter")
}

// TestBUG204_ValidateCreateSpecsRefusesCraftedAliasKey confirms create validation
// refuses the crafted output as a violation and does not admit it (AC5).
func TestBUG204_ValidateCreateSpecsRefusesCraftedAliasKey(t *testing.T) {
	dir := t.TempDir()
	name := "tc-11111111-crafted-output.md"
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(bug204CraftedCreateOutput), 0o644))

	result, err := ValidateCreateSpecs(dir, []string{"tc-11111111"}, nil, nil)
	require.NoError(t, err)
	require.Len(t, result.Violations, 1)
	assert.Equal(t, name, result.Violations[0].File)
	assert.Contains(t, result.Violations[0].Reason, "could not parse frontmatter")
}
