package reader

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/testcase"
)

// BUG-185 (tc-af0ee202): triage classification is a mutating consumer by effect.
// An Ambiguous or Unprovable id refuses at step 0, before hashing a spec or
// writing any triage record.
func TestClassifyTriage_AmbiguousID_RefusesBeforeWrite(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "tc-a1b2c3d4", "gtms/test/cases/alpha/tc-a1b2c3d4-first.md")
	writeSpec(t, root, "tc-a1b2c3d4", "gtms/test/cases/beta/tc-deadbeef-renamed.md")

	values := map[string]string{"environment": "Environment"}
	_, err := ClassifyTriage(root, "tc-a1b2c3d4", "environment", "", "", "bats", values)
	require.Error(t, err)
	var oe *testcase.OwnershipError
	require.ErrorAs(t, err, &oe)
	assert.Equal(t, testcase.OutcomeAmbiguous, oe.Outcome)

	// No triage record was written.
	_, statErr := os.Stat(filepath.Join(root, "gtms", "triage"))
	assert.True(t, os.IsNotExist(statErr), "no triage store should be created on refusal")
}

func TestClassifyTriage_UnprovableID_RefusesBeforeWrite(t *testing.T) {
	root := t.TempDir()
	spec := filepath.Join(root, "gtms/test/cases/unprovable/tc-b2c3d4e5-no-identity.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(spec), 0o755))
	require.NoError(t, os.WriteFile(spec, []byte("---\ntitle: no id\n---\nbody\n"), 0o644))

	values := map[string]string{"environment": "Environment"}
	_, err := ClassifyTriage(root, "tc-b2c3d4e5", "environment", "", "", "bats", values)
	require.Error(t, err)
	var oe *testcase.OwnershipError
	require.ErrorAs(t, err, &oe)
	assert.Equal(t, testcase.OutcomeUnprovable, oe.Outcome)
	assert.Equal(t, testcase.ReasonMissingID, oe.Reason)
}
