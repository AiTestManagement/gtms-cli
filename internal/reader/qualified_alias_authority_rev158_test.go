package reader

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/testcase"
)

// seedAliasProject builds the CLAUDE-001 fixture: the real spec lives in beta/,
// and alpha/ holds a symlink to it.
func seedAliasProject(t *testing.T) (root, realSpec, link string) {
	t.Helper()
	skipIfNoSymlinks(t)
	root = t.TempDir()
	realSpec = writeSpec(t, root, "tc-22222222", "gtms/test/cases/beta/tc-22222222-beta.md")

	alpha := filepath.Join(root, "gtms", "test", "cases", "alpha")
	require.NoError(t, os.MkdirAll(alpha, 0o755))
	link = filepath.Join(alpha, "tc-22222222-linked.md")
	require.NoError(t, os.Symlink(realSpec, link))
	return root, realSpec, link
}

// scopeForQualified mirrors what the CLI now passes for "alpha/tc-22222222".
func scopeForQualified(root, folder string) *ScopeInfo {
	return &ScopeInfo{
		ScanDir:   filepath.Join(root, "gtms", "test", "cases", folder),
		RelPath:   "gtms/test/cases/" + folder + "/",
		Recursive: true,
	}
}

// REV-158 CLAUDE-001, the highest-consequence finding. A folder-qualified delete
// reached through a spec symlink used to remove the OUT-OF-SCOPE target: with
// beta/tc-22222222-beta.md real and alpha/tc-22222222-linked.md a symlink to it,
// `gtms delete alpha/tc-22222222` exited 0 and deleted the file in beta, leaving
// a dangling link in alpha. `gtms delete alpha` (the folder form, the only form
// the tc-d11c3c91 acceptance spec exercises) correctly preserved beta, so the
// existing spec could not catch it.
//
// This is ADR-024 s3's explicit prohibition, and BUG-185's own failure class --
// destroying out-of-scope data -- arrived at through an alias.
func TestDelete_QualifiedThroughSpecAlias_RefusesAndPreservesTarget(t *testing.T) {
	root, realSpec, link := seedAliasProject(t)
	before, rerr := os.ReadFile(realSpec)
	require.NoError(t, rerr)

	_, err := DeleteArtifacts(root, scopeForQualified(root, "alpha"), "tc-22222222", false, false, false)
	require.Error(t, err, "a spec symlink must not grant authority to delete what it points at")
	var sae *testcase.ScopeAuthorityError
	require.ErrorAs(t, err, &sae)
	assert.Equal(t, "tc-22222222", sae.ID)
	assert.Contains(t, sae.OwnerPath, "beta")

	after, aerr := os.ReadFile(realSpec)
	require.NoError(t, aerr, "the out-of-scope target must still exist")
	assert.Equal(t, before, after, "the out-of-scope target must be byte-identical")
	_, lerr := os.Lstat(link)
	assert.NoError(t, lerr, "the link itself must be untouched by a refusal")
}

// reset reaches the same gate and must refuse identically.
func TestReset_QualifiedThroughSpecAlias_Refuses(t *testing.T) {
	root, _, _ := seedAliasProject(t)
	writeHandoff(t, root, "task-11111111", "tc-22222222")

	_, err := ResetExecuteResults(root, scopeForQualified(root, "alpha"), "tc-22222222", false)
	require.Error(t, err, "a spec symlink must not grant authority to reset what it points at")
	var sae *testcase.ScopeAuthorityError
	require.ErrorAs(t, err, &sae)

	handoff := filepath.Join(root, ".gtms", "results", "task-11111111.handoff.yaml")
	assertPresent(t, handoff)
}

// Positive control 1: targeting the spec where it LIVES still works.
func TestDelete_QualifiedAtRealLocation_Succeeds(t *testing.T) {
	root, realSpec, _ := seedAliasProject(t)

	res, err := DeleteArtifacts(root, scopeForQualified(root, "beta"), "tc-22222222", false, false, false)
	require.NoError(t, err, "the owner really does live under beta/")
	assert.Equal(t, 1, res.TestCasesProcessed)
	_, serr := os.Stat(realSpec)
	assert.Error(t, serr, "the spec should have been deleted from its real location")
}

// Positive control 2: an UNQUALIFIED target is unchanged -- a bare id is a
// project-wide lookup and imposes no scope requirement.
func TestDelete_BareIDThroughAliasProject_StillDeletesOwner(t *testing.T) {
	root, realSpec, _ := seedAliasProject(t)

	_, err := DeleteArtifacts(root, nil, "tc-22222222", false, false, false)
	require.NoError(t, err)
	_, serr := os.Stat(realSpec)
	assert.Error(t, serr, "a bare-id delete removes the project-wide unique owner")
}

// Positive control 3: a DIRECTORY alias still resolves to the real directory, so
// naming a linked folder keeps working (ADR-024 s3).
func TestDelete_QualifiedThroughDirectoryAlias_Succeeds(t *testing.T) {
	skipIfNoSymlinks(t)
	root := t.TempDir()
	realSpec := writeSpec(t, root, "tc-33333333", "gtms/test/cases/real/tc-33333333-x.md")
	casesDir := filepath.Join(root, "gtms", "test", "cases")
	require.NoError(t, os.Symlink(filepath.Join(casesDir, "real"), filepath.Join(casesDir, "aliasdir")))

	_, err := DeleteArtifacts(root, scopeForQualified(root, "aliasdir"), "tc-33333333", false, false, false)
	require.NoError(t, err, "a directory alias resolves to the real directory and remains usable")
	_, serr := os.Stat(realSpec)
	assert.Error(t, serr, "the spec should have been deleted")
}

// Positive control 4: a spec symlink INSIDE the folder its target lives in still
// grants authority -- the owner really is there.
func TestDelete_QualifiedWithSameFolderAlias_Succeeds(t *testing.T) {
	skipIfNoSymlinks(t)
	root := t.TempDir()
	realSpec := writeSpec(t, root, "tc-44444444", "gtms/test/cases/one/tc-44444444-x.md")
	require.NoError(t, os.Symlink(realSpec, filepath.Join(root, "gtms", "test", "cases", "one", "tc-44444444-alias.md")))

	_, err := DeleteArtifacts(root, scopeForQualified(root, "one"), "tc-44444444", false, false, false)
	require.NoError(t, err, "the owner lives in the folder the user named")
}
