package reader

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// digestOf returns the SHA-256 of a file's bytes. Byte-identity after a refusal
// is what the ADR requires; os.Stat only proves the path still exists, and would
// pass just as happily for a file that had been truncated or rewritten.
func digestOf(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err, "reading %s for digest", path)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// digestAll snapshots the digests of every named path, in order.
func digestAll(t *testing.T, paths ...string) []string {
	t.Helper()
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = digestOf(t, p)
	}
	return out
}

// writeHandoffCmd seeds a handoff with an explicit command + target.
func writeHandoffCmd(t *testing.T, root, taskID, command, target string) string {
	t.Helper()
	dir := filepath.Join(root, ".gtms", "results")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, taskID+".handoff.yaml")
	body := "task: " + taskID + "\n"
	if command != "" {
		body += "command: " + command + "\n"
	}
	body += "target: " + target + "\nstatus: complete\nresult: pass\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}

// F1: result contracts are attributed by BOTH command and target. A create
// contract is FOLDER-scoped and must survive a TC deletion even when the folder
// name equals the requested TC id character-for-character; a bare `tc-id-ish`
// must survive; only automate/execute/prime with an EXACT target match are
// deleted. (The old last-component strip and the bare `target == tcID` both
// mis-attributed the create contract.)
func TestDelete_ResultContracts_AttributedByCommandAndTarget(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "tc-77777777", "gtms/test/cases/checkout/tc-77777777-owner.md")

	// SURVIVES: create contract whose folder target is exactly the TC id.
	createHandoff := writeHandoffCmd(t, root, "task-create01", "create", "tc-77777777")
	// SURVIVES: a bare id that merely extends the requested id.
	ishHandoff := writeHandoffCmd(t, root, "task-ish01", "execute", "tc-77777777-ish")
	// DELETED: an execute contract with the exact bare id.
	execHandoff := writeHandoffCmd(t, root, "task-exec01", "execute", "tc-77777777")

	res, err := DeleteArtifacts(root, nil, "tc-77777777", false, false, false)
	require.NoError(t, err)
	assert.Equal(t, 1, res.ResultContracts, "only the exact-match execute handoff is deleted")

	_, e1 := os.Stat(createHandoff)
	assert.NoError(t, e1, "create contract (folder named like the id) must SURVIVE")
	_, e2 := os.Stat(ishHandoff)
	assert.NoError(t, e2, "tc-77777777-ish must SURVIVE")
	_, e3 := os.Stat(execHandoff)
	assert.True(t, os.IsNotExist(e3), "the exact-match execute handoff is removed")
}

// F2: a corrupt wiring record RELEVANT to a selected id (its filename claims the
// id, its body declares a different identity) is a data-integrity error, never
// absence. Both dry-run and real delete must refuse the whole plan (even without
// --force-artefact-deletion) and leave every file byte-identical.
func TestDelete_CorruptRelevantWiring_AbortsWholePlan(t *testing.T) {
	root := t.TempDir()
	spec := writeSpec(t, root, "tc-77777777", "gtms/test/cases/checkout/tc-77777777-owner.md")
	handoff := writeHandoffCmd(t, root, "task-e1", "execute", "tc-77777777")

	// A wiring file NAMED tc-77777777--bats but whose body declares tc-99999999 --
	// filename/body identity mismatch, relevant to tc-77777777 by filename.
	wdir := filepath.Join(root, "gtms", "automation", "wiring")
	require.NoError(t, os.MkdirAll(wdir, 0o755))
	corrupt := filepath.Join(wdir, "tc-77777777--bats.wiring.yaml")
	body := "testcase: tc-99999999\ntestcase-hash: 0011223344556677\nframework: bats\nadapter: bats-runner\nartefact: test/x.bats\nartefact-hash: aabbccddeeff0011\n"
	require.NoError(t, os.WriteFile(corrupt, []byte(body), 0o644))

	// Byte-identity, per ADR-024: capture raw digests BEFORE, and compare after
	// EACH refusal. os.Stat would report a truncated or rewritten file as intact.
	watched := []string{spec, handoff, corrupt}
	before := digestAll(t, watched...)

	for _, dry := range []bool{true, false} {
		_, err := DeleteArtifacts(root, nil, "tc-77777777", false, false, dry)
		require.Error(t, err, "corrupt relevant wiring must abort (dry=%v)", dry)
		assert.Contains(t, err.Error(), "corrupt or its declared identity disagrees")

		after := digestAll(t, watched...)
		assert.Equal(t, before, after,
			"every file must be byte-identical after the refusal (dry=%v)", dry)
	}
}

// F2 relevance rule: an UNRELATED corrupt wiring record (its filename claims a
// different id) does not block the delete.
func TestDelete_CorruptUnrelatedWiring_DoesNotBlock(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "tc-77777777", "gtms/test/cases/checkout/tc-77777777-owner.md")

	wdir := filepath.Join(root, "gtms", "automation", "wiring")
	require.NoError(t, os.MkdirAll(wdir, 0o755))
	// Corrupt, but its filename claims tc-99999999 -- unrelated to tc-77777777.
	corrupt := filepath.Join(wdir, "tc-99999999--bats.wiring.yaml")
	body := "testcase: tc-88888888\ntestcase-hash: 0011223344556677\nframework: bats\nadapter: bats-runner\nartefact: test/x.bats\nartefact-hash: aabbccddeeff0011\n"
	require.NoError(t, os.WriteFile(corrupt, []byte(body), 0o644))

	_, err := DeleteArtifacts(root, nil, "tc-77777777", false, false, true)
	require.NoError(t, err, "an unrelated corrupt record must not block the operation")
}

// F1 (allowlist): attribution is a positive allowlist of the TC-scoped commands
// (automate, execute, prime), not "anything that is not create". A contract whose
// `command` is EMPTY or names some other command cannot be classified as
// TC-scoped, so it must SURVIVE the delete even though its target is an exact
// match for the deleted id. Under the old denylist every one of these was
// attributable and deleted.
func TestDelete_ResultContracts_UnknownCommandIsNotTestCaseScoped(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "tc-77777777", "gtms/test/cases/checkout/tc-77777777-owner.md")

	// SURVIVES: no `command:` field at all -- unclassifiable, so not attributable.
	emptyCmd := writeHandoffCmd(t, root, "task-empty01", "", "tc-77777777")
	// SURVIVES: a folder-scoped command that is not on the allowlist.
	otherCmd := writeHandoffCmd(t, root, "task-other01", "gaps", "tc-77777777")
	// DELETED: each of the three allowlisted TC-scoped commands.
	automateH := writeHandoffCmd(t, root, "task-auto01", "automate", "tc-77777777")
	executeH := writeHandoffCmd(t, root, "task-exec01", "execute", "tc-77777777")
	primeH := writeHandoffCmd(t, root, "task-prime01", "prime", "tc-77777777")

	res, err := DeleteArtifacts(root, nil, "tc-77777777", false, false, false)
	require.NoError(t, err)
	assert.Equal(t, 3, res.ResultContracts,
		"only automate/execute/prime contracts are attributable to a TC delete")

	_, e1 := os.Stat(emptyCmd)
	assert.NoError(t, e1, "a contract with an EMPTY command must SURVIVE the delete")
	_, e2 := os.Stat(otherCmd)
	assert.NoError(t, e2, "a contract with a non-allowlisted command must SURVIVE the delete")

	for _, p := range []string{automateH, executeH, primeH} {
		_, e := os.Stat(p)
		assert.True(t, os.IsNotExist(e), "allowlisted TC-scoped contract must be removed: %s", p)
	}
}

// writeCorruptWiringNamed writes an unreadable wiring record at an EXACT filename,
// so a test can control which id the filename could be attributed to. The body
// declares a different identity than any reading of the filename, so the record
// can never be identified by its body -- the filename is the only signal left.
func writeCorruptWiringNamed(t *testing.T, root, filename string) string {
	t.Helper()
	wdir := filepath.Join(root, "gtms", "automation", "wiring")
	require.NoError(t, os.MkdirAll(wdir, 0o755))
	path := filepath.Join(wdir, filename)
	body := "testcase: tc-99999999\ntestcase-hash: 0011223344556677\nframework: bats\n" +
		"adapter: bats-runner\nartefact: test/x.bats\nartefact-hash: aabbccddeeff0011\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}

// seedPrefixSiblingPair creates the two DISTINCT identities whose wiring filenames
// are grammatically ambiguous: tc-77777777 and tc-77777777--legacy. The corrupt
// wiring record is named tc-77777777--legacy--bats.wiring.yaml, which reads either
// as (tc-77777777--legacy, bats) or as (tc-77777777, legacy--bats).
func seedPrefixSiblingPair(t *testing.T, root string) (shortSpec, extSpec, corrupt string) {
	t.Helper()
	shortSpec = writeSpec(t, root, "tc-77777777", "gtms/test/cases/checkout/tc-77777777-short.md")
	extSpec = writeSpec(t, root, "tc-77777777--legacy", "gtms/test/cases/checkout/tc-77777777--legacy-ext.md")
	corrupt = writeCorruptWiringNamed(t, root, "tc-77777777--legacy--bats.wiring.yaml")
	return shortSpec, extSpec, corrupt
}

// F2 direction 1: corruption on tc-77777777--legacy--bats.wiring.yaml must BLOCK
// `gtms delete tc-77777777--legacy` -- the id that actually owns that filename.
//
// The first-"--" split filed this record under tc-77777777, so the extended id
// never saw its own corrupt record and the delete proceeded: corruption silently
// converted back into absence, the exact defect F2 exists to close.
func TestDelete_CorruptExtendedIDWiring_BlocksItsOwnID(t *testing.T) {
	root := t.TempDir()
	shortSpec, extSpec, corrupt := seedPrefixSiblingPair(t, root)

	watched := []string{shortSpec, extSpec, corrupt}
	before := digestAll(t, watched...)

	for _, dry := range []bool{true, false} {
		_, err := DeleteArtifacts(root, nil, "tc-77777777--legacy", false, false, dry)
		require.Error(t, err,
			"the extended id must be blocked by the corrupt record its own filename names (dry=%v)", dry)
		assert.Contains(t, err.Error(), "tc-77777777--legacy",
			"the refusal must name the extended id, not its prefix sibling")
		assert.Contains(t, err.Error(), "corrupt or its declared identity disagrees")

		assert.Equal(t, before, digestAll(t, watched...),
			"nothing may be mutated by the refusal (dry=%v)", dry)
	}
}

// F2 direction 2: that SAME corrupt extended-sibling record must NOT block
// `gtms delete tc-77777777`. It belongs to tc-77777777--legacy, a different
// identity; refusing here is a false refusal on an unrelated id.
//
// tc-77777777--legacy exists as a real owner, which is what settles the otherwise
// ambiguous filename -- so the record is attributed to it and is irrelevant here.
func TestDelete_CorruptExtendedIDWiring_DoesNotBlockPrefixSibling(t *testing.T) {
	root := t.TempDir()
	shortSpec, extSpec, corrupt := seedPrefixSiblingPair(t, root)

	res, err := DeleteArtifacts(root, nil, "tc-77777777", false, false, false)
	require.NoError(t, err,
		"a corrupt record owned by the extended sibling must not refuse the prefix id")
	assert.Equal(t, 1, res.TestCaseSpecsRemoved, "only tc-77777777's own spec is removed")

	_, e := os.Stat(shortSpec)
	assert.True(t, os.IsNotExist(e), "tc-77777777's spec is deleted")
	_, e2 := os.Stat(extSpec)
	assert.NoError(t, e2, "the extended sibling's spec must survive")
	_, e3 := os.Stat(corrupt)
	assert.NoError(t, e3, "the extended sibling's corrupt wiring must survive untouched")
}

// F2 fail-closed floor: when the extended id does NOT exist, the same filename has
// only one possible reading left -- (tc-77777777, framework "legacy--bats") -- so
// it IS relevant to tc-77777777 and must block it. This is what stops the
// selection-aware rule from becoming a way to dodge the guard by deleting a spec
// first: relevance falls back to the shortest surviving reading, never to "no id".
func TestDelete_CorruptExtendedIDWiring_BlocksPrefixWhenExtendedIDAbsent(t *testing.T) {
	root := t.TempDir()
	shortSpec := writeSpec(t, root, "tc-77777777", "gtms/test/cases/checkout/tc-77777777-short.md")
	corrupt := writeCorruptWiringNamed(t, root, "tc-77777777--legacy--bats.wiring.yaml")

	before := digestAll(t, shortSpec, corrupt)

	_, err := DeleteArtifacts(root, nil, "tc-77777777", false, false, false)
	require.Error(t, err, "with no extended owner, the record can only belong to tc-77777777")
	assert.Contains(t, err.Error(), "corrupt or its declared identity disagrees")

	assert.Equal(t, before, digestAll(t, shortSpec, corrupt),
		"the refusal must mutate nothing")
}
