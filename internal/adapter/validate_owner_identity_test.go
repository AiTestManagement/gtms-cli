package adapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/testcase"
)

// seedOwnedSpec writes one spec and returns (projectRoot, specAbs).
func seedOwnedSpec(t *testing.T, relPath, id string) (string, string) {
	t.Helper()
	root := t.TempDir()
	abs := filepath.Join(root, filepath.FromSlash(relPath))
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.WriteFile(abs, []byte("---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: "+id+"\ntitle: t\n---\nbody\n"), 0o644))
	return root, abs
}

// REV-158 CODEX-003: the post-fill gate must confirm the resolved owner STILL
// owns the id it was resolved for. Before the fix it accepted any non-empty
// test_case_id, so a spec rewritten between resolution and validation was waved
// through and the command invoked external code and hashed or wrote wiring under
// the stale original identity.
func TestValidateTestCaseOwner_OwnerIDChangedAfterResolution(t *testing.T) {
	root, abs := seedOwnedSpec(t, "gtms/test/cases/feat/tc-aaaa1111-login.md", "tc-aaaa1111")

	owner, err := testcase.ResolveOwner(root, "tc-aaaa1111")
	require.NoError(t, err)

	// The file changes after ownership was proved.
	require.NoError(t, os.WriteFile(abs, []byte("---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-bbbb2222\ntitle: t\n---\nbody\n"), 0o644))

	violations := ValidateTestCaseOwner(root, owner)
	require.Len(t, violations, 1, "a changed owner identity must fail validation")
	require.Contains(t, violations[0].Reason, "tc-bbbb2222")
	require.Contains(t, violations[0].Reason, "tc-aaaa1111")
}

// The same equality gate refuses an identity that no longer satisfies the
// execution grammar: owner.ID() is grammar-valid by construction, so a
// shell-unsafe id written into the file after resolution can never equal it and
// cannot reach a path, adapter, or shell (ADR-024 s6).
func TestValidateTestCaseOwner_OwnerIDBecomesGrammarInvalid(t *testing.T) {
	root, abs := seedOwnedSpec(t, "gtms/test/cases/feat/tc-aaaa1111-login.md", "tc-aaaa1111")

	owner, err := testcase.ResolveOwner(root, "tc-aaaa1111")
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(abs, []byte("---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: \"tc-safe&calc\"\ntitle: t\n---\nbody\n"), 0o644))

	violations := ValidateTestCaseOwner(root, owner)
	require.Len(t, violations, 1, "a grammar-invalid owner identity must fail validation")
}

// Positive control: an unchanged owner still validates, and a control character
// in a changed identity is sanitised out of the diagnostic (ADR-024 s7).
func TestValidateTestCaseOwner_UnchangedOwnerPasses(t *testing.T) {
	root, _ := seedOwnedSpec(t, "gtms/test/cases/feat/tc-aaaa1111-login.md", "tc-aaaa1111")

	owner, err := testcase.ResolveOwner(root, "tc-aaaa1111")
	require.NoError(t, err)
	require.Empty(t, ValidateTestCaseOwner(root, owner))
}

func TestValidateTestCaseOwner_ChangedIdentityIsSanitised(t *testing.T) {
	root, abs := seedOwnedSpec(t, "gtms/test/cases/feat/tc-aaaa1111-login.md", "tc-aaaa1111")

	owner, err := testcase.ResolveOwner(root, "tc-aaaa1111")
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(abs, []byte("---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: \"tc-\\u001b[31mred\"\ntitle: t\n---\nbody\n"), 0o644))

	violations := ValidateTestCaseOwner(root, owner)
	require.Len(t, violations, 1)
	require.False(t, strings.ContainsRune(violations[0].Reason, 0x1b),
		"a filesystem-derived identity must be sanitised before it is echoed")
}
