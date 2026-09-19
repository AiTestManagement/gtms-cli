package adapter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// sweep runs the --force cleanup, asserts it completed, and returns the
// informational preservation reports it produced so a caller can assert on them.
func sweep(t *testing.T, claims *ArtefactClaimSet, dir, target string) []string {
	t.Helper()
	retained, err := cleanupExistingOutputForOwner(claims, dir, target)
	require.NoError(t, err)
	return retained
}

// mustClaims builds the operation-scoped claim set a real invocation threads in.
// Tests that drive the sweep directly build one the same way the boundary does,
// so they exercise the same abstraction rather than a test-only shortcut.
func mustClaims(t *testing.T, root string) *ArtefactClaimSet {
	t.Helper()
	set, err := NewArtefactClaimSet(root)
	require.NoError(t, err)
	return set
}

// One wiring record per (owner, framework): seedClaim takes the framework
// explicitly so a test can give ONE identity several claimed artefacts without
// each wiring.Write silently overwriting the last.
func seedClaim(t *testing.T, root, owner, framework, relArtefact string) string {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(relArtefact))
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.WriteFile(abs, []byte("x"), 0o644))
	_, err := wiring.Write(root, &wiring.WiringRecord{
		TestCase:     owner,
		TestCaseHash: "1111111111111111",
		Framework:    framework,
		Adapter:      "bats-runner",
		Artefact:     relArtefact,
		ArtefactHash: "2222222222222222",
	})
	require.NoError(t, err)
	return abs
}

// REV-160 CLAUDE-004 -- tc-abc123 and tc-abc123--legacy are DIFFERENT test cases
// (ADR-024 s6 keeps the extended `--` form) and both write into the same output
// dir. The pre-BUG-188 sweep matched on a bare strings.HasPrefix of "tc-abc123-",
// which also matches "tc-abc123--legacy-...", so `gtms automate tc-abc123
// --force` os.Remove'd the sibling's artefact.
//
// The guard is no longer a dash-counting rule, because no basename rule can be
// correct: the TC-ID grammar admits single dashes, so "tc-abc123-legacy-x.bats"
// is indistinguishable by inspection from tc-abc123's own output and from
// tc-abc123-legacy's. Authority is the wiring record's parsed body identity, and
// this test pins the same separation through it -- including for a sibling whose
// name a dash rule would have waved straight through.
func TestCleanupExistingOutputForOwner_PreservesExtendedIDSibling(t *testing.T) {
	root := t.TempDir()

	ownOutput := seedClaim(t, root, "tc-abc123", "bats", "out/tc-abc123-old-slug.bats")
	sibling := seedClaim(t, root, "tc-abc123--legacy", "bats", "out/tc-abc123--legacy-x.bats")
	deeperSibling := seedClaim(t, root, "tc-abc123--legacy--v2", "bats", "out/tc-abc123--legacy--v2-y.bats")
	// A sibling a single-dash id owns. No basename rule survives this one: the
	// name is exactly what tc-abc123 plus the slug "legacy-x" would look like.
	singleDashSibling := seedClaim(t, root, "tc-abc123-legacy", "bats", "out/tc-abc123-legacy-x.bats")
	unrelated := seedClaim(t, root, "tc-other99", "bats", "out/tc-other99-unrelated.bats")

	dir := filepath.Join(root, "out")
	sweep(t, mustClaims(t, root), dir, "tc-abc123")

	assert.NoFileExists(t, ownOutput,
		"the target's own wiring claims this file, so --force must sweep it")
	assert.FileExists(t, sibling,
		"tc-abc123--legacy is a DIFFERENT test case; cleaning up tc-abc123 must not delete its artefact")
	assert.FileExists(t, deeperSibling,
		"tc-abc123--legacy--v2 is a different test case too")
	assert.FileExists(t, singleDashSibling,
		"tc-abc123-legacy is a different test case whose artefact name a prefix rule cannot tell from tc-abc123's own")
	assert.FileExists(t, unrelated)
}

// The same distinction one directory down: the sweep is a filepath.Walk, so a
// sibling's artefact in a nested output subdirectory is equally reachable.
func TestCleanupExistingOutputForOwner_PreservesExtendedIDSiblingInSubdir(t *testing.T) {
	root := t.TempDir()

	ownOutput := seedClaim(t, root, "tc-abc123", "bats", "out/generated/tc-abc123-slug.bats")
	sibling := seedClaim(t, root, "tc-abc123--legacy", "bats", "out/generated/tc-abc123--legacy-x.bats")

	sweep(t, mustClaims(t, root), filepath.Join(root, "out"), "tc-abc123")

	assert.NoFileExists(t, ownOutput)
	assert.FileExists(t, sibling)
}

// An UNCLAIMED file is never swept, whatever its name looks like. The sweep used
// to remove anything carrying the target's id as a basename prefix; that rule is
// gone, so a stray file only the filesystem knows about survives.
func TestCleanupExistingOutputForOwner_UnclaimedFileIsNeverSwept(t *testing.T) {
	root := t.TempDir()
	claimed := seedClaim(t, root, "tc-abc123", "bats", "out/tc-abc123-claimed.bats")

	stray := filepath.Join(root, "out", "tc-abc123-stray.bats")
	require.NoError(t, os.WriteFile(stray, []byte("x"), 0o644))

	sweep(t, mustClaims(t, root), filepath.Join(root, "out"), "tc-abc123")

	assert.NoFileExists(t, claimed)
	assert.FileExists(t, stray,
		"nothing claims this file, so nothing proves it is the target's to delete")
}
