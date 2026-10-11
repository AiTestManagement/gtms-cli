package reader

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// REV-158 CLAUDE-003 at the consumer boundary. A corrupt extended-ID sibling
// used to refuse delete of its unrelated shorter prefix: with a valid
// tc-11111111-login.md and a malformed tc-11111111--legacy.md, `gtms delete
// tc-11111111 --dry-run` exited 1 "because of a file belonging to a different
// test case". Both directions are pinned.
func TestDelete_ExtendedIDSiblingCorruptionDoesNotBlockShorterID(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "tc-11111111", "gtms/test/cases/feat/tc-11111111-login.md")
	writeDoc(t, root, "gtms/test/cases/feat/tc-11111111--legacy.md", "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: [unclosed\n---\nbody\n")

	_, err := DeleteArtifacts(root, nil, "tc-11111111", false, false, true)
	require.NoError(t, err, "a malformed tc-11111111--legacy.md is a DIFFERENT test case and must not refuse tc-11111111")
}

func TestDelete_ShorterIDCorruptionDoesNotBlockExtendedID(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "tc-11111111--legacy", "gtms/test/cases/feat/tc-11111111--legacy-sibling.md")
	writeDoc(t, root, "gtms/test/cases/feat/tc-11111111-login.md", "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: [unclosed\n---\nbody\n")

	_, err := DeleteArtifacts(root, nil, "tc-11111111--legacy", false, false, true)
	require.NoError(t, err, "a malformed tc-11111111-login.md is a DIFFERENT test case and must not refuse tc-11111111--legacy")
}

// Positive control: each id IS still refused by corruption of its own spec.
func TestDelete_OwnCorruptionStillRefuses(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "gtms/test/cases/feat/tc-11111111-login.md", "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: [unclosed\n---\nbody\n")

	_, err := DeleteArtifacts(root, nil, "tc-11111111", false, false, true)
	require.Error(t, err, "a malformed spec of the targeted id must refuse")
}
