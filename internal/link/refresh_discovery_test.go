package link

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// writeRawWiring drops arbitrary bytes at gtms/automation/wiring/{name}, bypassing
// wiring.Write's validation so tests can model malformed or identity-mismatched
// files that a hand edit or legacy tool could leave on disk.
func writeRawWiring(t *testing.T, root, name, content string) string {
	t.Helper()
	dir := layout.WiringDir(root)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

// writeValidWiring writes a schema-valid wiring record for (tc, framework) through
// the production writer.
func writeValidWiring(t *testing.T, root, tc, framework string) {
	t.Helper()
	_, err := wiring.Write(root, &wiring.WiringRecord{
		TestCase:     tc,
		TestCaseHash: "0011223344556677",
		Framework:    framework,
		Adapter:      framework + "-runner",
		Artefact:     "test/acceptance/" + tc + ".bats",
		ArtefactHash: "aabbccddeeff0011",
	})
	require.NoError(t, err)
}

// TestPartitionTCIDsByAmbiguity: only the colliding IDs are held back, and both
// halves come back sorted so the caller's output does not depend on walk order
// (REV-129 CLAUDE-003).
func TestPartitionTCIDsByAmbiguity(t *testing.T) {
	unique, ambiguous := PartitionTCIDsByAmbiguity(nil)
	assert.Empty(t, unique)
	assert.Empty(t, ambiguous)

	unique, ambiguous = PartitionTCIDsByAmbiguity([]string{"tc-c", "tc-a", "tc-b"})
	assert.Equal(t, []string{"tc-a", "tc-b", "tc-c"}, unique)
	assert.Empty(t, ambiguous)

	// One collision must not pull the unambiguous IDs out of scope.
	unique, ambiguous = PartitionTCIDsByAmbiguity([]string{"tc-a", "tc-b", "tc-b", "tc-c"})
	assert.Equal(t, []string{"tc-a", "tc-c"}, unique)
	assert.Equal(t, []string{"tc-b"}, ambiguous)

	// A trebled ID is reported once, not twice.
	unique, ambiguous = PartitionTCIDsByAmbiguity([]string{"tc-a", "tc-a", "tc-a"})
	assert.Empty(t, unique)
	assert.Equal(t, []string{"tc-a"}, ambiguous)

	// Several collisions, sorted.
	unique, ambiguous = PartitionTCIDsByAmbiguity([]string{"tc-z", "tc-z", "tc-m", "tc-m", "tc-q"})
	assert.Equal(t, []string{"tc-q"}, unique)
	assert.Equal(t, []string{"tc-m", "tc-z"}, ambiguous)
}

// TestDiscoverRefreshTargets_ValidRecord: a well-formed, identity-matching record
// is a selectable target with its Record set and no Err.
func TestDiscoverRefreshTargets_ValidRecord(t *testing.T) {
	root := t.TempDir()
	writeValidWiring(t, root, "tc-11111111", "bats")

	targets, err := DiscoverRefreshTargets(root, []string{"tc-11111111"}, "")
	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.Equal(t, "tc-11111111", targets[0].TCID)
	assert.Equal(t, "bats", targets[0].Framework)
	require.NotNil(t, targets[0].Record)
	assert.NoError(t, targets[0].Err)
	assert.Equal(t, "tc-11111111", targets[0].Record.TestCase)
}

// TestDiscoverRefreshTargets_IdentityMismatch_IsFailure: a file named
// tc-A--bats whose YAML declares testcase B must be a failing target labelled A,
// never a selectable record. This closes the CODEX-001 cross-record overwrite
// vector at the discovery boundary.
func TestDiscoverRefreshTargets_IdentityMismatch_IsFailure(t *testing.T) {
	root := t.TempDir()
	// Filename says tc-aaaaaaaa; YAML declares tc-bbbbbbbb.
	writeRawWiring(t, root, "tc-aaaaaaaa--bats.wiring.yaml",
		"testcase: tc-bbbbbbbb\n"+
			"testcase-hash: 0011223344556677\n"+
			"framework: bats\n"+
			"adapter: bats-runner\n"+
			"artefact: test/acceptance/tc-bbbbbbbb.bats\n"+
			"artefact-hash: aabbccddeeff0011\n")

	targets, err := DiscoverRefreshTargets(root, []string{"tc-aaaaaaaa"}, "")
	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.Equal(t, "tc-aaaaaaaa", targets[0].TCID, "reported under the requested (filename) identity, not the YAML identity")
	assert.Nil(t, targets[0].Record, "a mismatched record must never be exposed as writable")
	require.Error(t, targets[0].Err)
	assert.Contains(t, targets[0].Err.Error(), "malformed or unreadable wiring")
	assert.Contains(t, targets[0].Err.Error(), "identity mismatch")
}

// TestDiscoverRefreshTargets_Malformed_IsFailure: an unparseable in-scope file is
// a counted failure, not a silent drop (CODEX-002), while a valid record in the
// same scope is still returned.
func TestDiscoverRefreshTargets_Malformed_IsFailure(t *testing.T) {
	root := t.TempDir()
	writeValidWiring(t, root, "tc-c0c00001", "bats")
	// Unknown field -> strict schema decode fails -> malformed.
	writeRawWiring(t, root, "tc-c0c00002--bats.wiring.yaml",
		"testcase: tc-c0c00002\n"+
			"testcase-hash: 0011223344556677\n"+
			"framework: bats\n"+
			"adapter: bats-runner\n"+
			"artefact: test/acceptance/tc-c0c00002.bats\n"+
			"artefact-hash: aabbccddeeff0011\n"+
			"status: complete\n")

	targets, err := DiscoverRefreshTargets(root, []string{"tc-c0c00001", "tc-c0c00002"}, "")
	require.NoError(t, err)
	require.Len(t, targets, 2)

	// Sorted by TCID: tc-c0c00001 (valid) then tc-c0c00002 (malformed).
	assert.NotNil(t, targets[0].Record)
	assert.NoError(t, targets[0].Err)

	assert.Nil(t, targets[1].Record)
	require.Error(t, targets[1].Err)
	assert.Equal(t, "tc-c0c00002", targets[1].TCID)
	assert.Contains(t, targets[1].Err.Error(), "malformed or unreadable wiring")
}

// TestDiscoverRefreshTargets_OutOfScopeExcluded: records for TC IDs outside the
// requested set are not returned, and a shape-broken filename (no filename TC ID)
// is skipped rather than reported as a failure of this refresh.
func TestDiscoverRefreshTargets_OutOfScopeExcluded(t *testing.T) {
	root := t.TempDir()
	writeValidWiring(t, root, "tc-inscope0", "bats")
	writeValidWiring(t, root, "tc-outscope", "bats")
	// A file whose name does not parse as {tc}--{framework}.
	writeRawWiring(t, root, "not-a-wiring-name.wiring.yaml", "testcase: tc-x\n")

	targets, err := DiscoverRefreshTargets(root, []string{"tc-inscope0"}, "")
	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.Equal(t, "tc-inscope0", targets[0].TCID)
}

// TestDiscoverRefreshTargets_FrameworkNarrows: framework != "" selects only the
// matching framework record.
func TestDiscoverRefreshTargets_FrameworkNarrows(t *testing.T) {
	root := t.TempDir()
	writeValidWiring(t, root, "tc-22222222", "bats")
	writeValidWiring(t, root, "tc-22222222", "playwright")

	targets, err := DiscoverRefreshTargets(root, []string{"tc-22222222"}, "playwright")
	require.NoError(t, err)
	require.Len(t, targets, 1)
	assert.Equal(t, "playwright", targets[0].Framework)
}

// TestDiscoverRefreshTargets_OutOfScopeFileIsNeverOpened pins REV-129
// CLAUDE-003 / CODEX-129-002: scoping happens on the FILENAME, before any file
// is opened. Discovery used to read and YAML-parse every wiring file in the
// project and filter afterwards, which made a single-TC refresh O(N) in the
// size of the wiring directory and let an unrelated file -- oversized, corrupt,
// or not a regular file at all -- affect a command that was never about it.
//
// The out-of-scope fixtures here are unparseable. If either were opened, it
// would surface as a failing target with a "malformed or unreadable wiring"
// error; their total absence from the results is the evidence that neither was
// read.
func TestDiscoverRefreshTargets_OutOfScopeFileIsNeverOpened(t *testing.T) {
	root := t.TempDir()
	writeValidWiring(t, root, "tc-11111111", "bats")

	// Out of scope by TC ID, and unreadable if opened.
	writeRawWiring(t, root, "tc-99999999--bats.wiring.yaml", "{{{ not yaml at all\n")
	// In scope by TC ID but out of scope by framework, also unreadable.
	writeRawWiring(t, root, "tc-11111111--playwright.wiring.yaml", "%%% not yaml either\n")

	targets, err := DiscoverRefreshTargets(root, []string{"tc-11111111"}, "bats")
	require.NoError(t, err)
	require.Len(t, targets, 1, "only the in-scope record may be returned")
	assert.Equal(t, "tc-11111111", targets[0].TCID)
	assert.Equal(t, "bats", targets[0].Framework)
	assert.NoError(t, targets[0].Err, "an out-of-scope file must not surface as a failure")

	// Unfiltered, the framework-excluded file IS in scope and IS read -- proving
	// the exclusion above came from the scope filter, not from the file being
	// skipped for some other reason.
	all, err := DiscoverRefreshTargets(root, []string{"tc-11111111"}, "")
	require.NoError(t, err)
	require.Len(t, all, 2)
	var sawMalformed bool
	for _, tgt := range all {
		if tgt.Framework == "playwright" {
			sawMalformed = tgt.Err != nil
		}
	}
	assert.True(t, sawMalformed, "the playwright record is malformed and must fail when in scope")
}
