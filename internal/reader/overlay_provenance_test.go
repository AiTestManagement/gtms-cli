package reader

// ENH-200 AC #11 regression test around the shared terminal-result scanner.
//
// ENH-200 added four fields to overlayHit so triage can capture snapshot
// provenance from the handoff it classified. overlayHit is shared with the
// status, gaps, and map overlays, so the addition had to be purely additive:
// the reader-facing vocabulary those consumers depend on must not shift.
//
// This asserts the behaviour rather than the declaration. A source-shape test
// grepping for field names would pass even if the scanner never populated
// them, and would break on a harmless rename. Calling the scanner and reading
// what it returns proves both halves at once: the new provenance arrives, and
// the old normalisation survives beside it.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestScanTerminalResults_CarriesProvenanceAndKeepsReaderVocabulary seeds one
// terminal handoff whose raw contract result is "skip" -- the single value
// where the raw and reader vocabularies genuinely differ -- and asserts the
// scanner surfaces both forms plus the rest of the ENH-200 provenance.
func TestScanTerminalResults_CarriesProvenanceAndKeepsReaderVocabulary(t *testing.T) {
	root := t.TempDir()

	const (
		tc           = "tc-11111111"
		framework    = "bats"
		artefact     = "test/acceptance/probe.bats"
		taskID       = "task-0a0f57cf"
		artefactHash = "aaaabbbbccccdddd"
		completedAt  = "2026-08-25T09:42:00Z"
	)

	// A real artefact and spec, so the wiring record passes the reader's
	// classification gate rather than being filtered out as stale.
	specPath := filepath.Join(root, "gtms", "test", "cases", tc+".md")
	require.NoError(t, os.MkdirAll(filepath.Dir(specPath), 0755))
	require.NoError(t, os.WriteFile(specPath,
		[]byte("---\ntest_case_id: "+tc+"\n---\n\n## Test Objective\nProbe.\n"), 0644))

	artefactPath := filepath.Join(root, filepath.FromSlash(artefact))
	require.NoError(t, os.MkdirAll(filepath.Dir(artefactPath), 0755))
	require.NoError(t, os.WriteFile(artefactPath, []byte("# probe\n"), 0644))

	seedLegacyRecord(t, root, legacyRecord{
		TC:        tc,
		Framework: framework,
		Result:    "skip",
		Artefact:  artefact,
	})

	// Overwrite the seeded handoff with one carrying the exact provenance
	// under test. Written inline rather than via the helper because the
	// helper stamps its own task ID, hash, and completion stamp, and the
	// point of this test is that those specific values survive the join.
	resultsDir := filepath.Join(root, ".gtms", "results")
	require.NoError(t, os.MkdirAll(resultsDir, 0755))
	entries, err := os.ReadDir(resultsDir)
	require.NoError(t, err)
	for _, e := range entries {
		require.NoError(t, os.Remove(filepath.Join(resultsDir, e.Name())))
	}
	handoff := "task: " + taskID + "\n" +
		"command: execute\n" +
		"target: " + tc + "\n" +
		"adapter: " + framework + "-runner\n" +
		"mode: sync\n" +
		"created: \"2026-08-25T09:40:00Z\"\n" +
		"status: complete\n" +
		"result: skip\n" +
		"framework: " + framework + "\n" +
		"artefact: " + artefact + "\n" +
		"artefact-hash: " + artefactHash + "\n" +
		"completed: \"" + completedAt + "\"\n"
	require.NoError(t, os.WriteFile(
		filepath.Join(resultsDir, taskID+".handoff.yaml"), []byte(handoff), 0644))

	overlay, err := scanTerminalResultsForTC(root, tc)
	require.NoError(t, err)

	hit, ok := overlay[overlayKey(tc, framework)]
	require.True(t, ok, "scanner must join the handoff to the (tc, framework) pair")

	// ENH-200 additive provenance, consumed by the triage snapshot.
	assert.Equal(t, taskID, hit.Task,
		"Task must carry the handoff task ID so a triage record can correlate "+
			"back to the run it classified")
	assert.Equal(t, artefactHash, hit.ArtefactHash,
		"ArtefactHash must be the handoff's own hash, verbatim; the triage "+
			"snapshot must never fall back to the wiring hash")
	assert.Equal(t, "skip", hit.RawResult,
		"RawResult must preserve the raw result-contract vocabulary; a triage "+
			"record stores what the run reported, not what the reader displays")
	assert.Equal(t, completedAt, hit.RawCompleted,
		"RawCompleted must carry the handoff's completed stamp verbatim")

	// Pre-existing reader vocabulary, consumed by status, gaps, and map.
	// This is the assertion that makes the addition provably additive.
	assert.Equal(t, "skipped", hit.Result,
		"Result must still be the reader-normalised \"skipped\"; ENH-200 added "+
			"RawResult beside it rather than changing what status/gaps render")
}

// TestScanTerminalResults_OmitsAbsentOptionalProvenance pins the other half of
// the contract: when a handoff omits the optional artefact-hash and completed
// keys, the scanner reports them empty rather than inventing a value. The
// triage writer relies on this to omit those snapshot fields entirely.
func TestScanTerminalResults_OmitsAbsentOptionalProvenance(t *testing.T) {
	root := t.TempDir()

	const (
		tc        = "tc-44444444"
		framework = "bats"
		artefact  = "test/acceptance/thin.bats"
		taskID    = "task-thin0001"
	)

	specPath := filepath.Join(root, "gtms", "test", "cases", tc+".md")
	require.NoError(t, os.MkdirAll(filepath.Dir(specPath), 0755))
	require.NoError(t, os.WriteFile(specPath,
		[]byte("---\ntest_case_id: "+tc+"\n---\n\n## Test Objective\nThin.\n"), 0644))

	artefactPath := filepath.Join(root, filepath.FromSlash(artefact))
	require.NoError(t, os.MkdirAll(filepath.Dir(artefactPath), 0755))
	require.NoError(t, os.WriteFile(artefactPath, []byte("# thin\n"), 0644))

	seedLegacyRecord(t, root, legacyRecord{
		TC:        tc,
		Framework: framework,
		Result:    "error",
		Artefact:  artefact,
	})

	resultsDir := filepath.Join(root, ".gtms", "results")
	entries, err := os.ReadDir(resultsDir)
	require.NoError(t, err)
	for _, e := range entries {
		require.NoError(t, os.Remove(filepath.Join(resultsDir, e.Name())))
	}
	// No artefact-hash key, no completed key.
	handoff := "task: " + taskID + "\n" +
		"command: execute\n" +
		"target: " + tc + "\n" +
		"adapter: " + framework + "-runner\n" +
		"mode: sync\n" +
		"created: \"2026-08-25T09:40:00Z\"\n" +
		"status: error\n" +
		"result: error\n" +
		"framework: " + framework + "\n" +
		"artefact: " + artefact + "\n"
	require.NoError(t, os.WriteFile(
		filepath.Join(resultsDir, taskID+".handoff.yaml"), []byte(handoff), 0644))

	overlay, err := scanTerminalResultsForTC(root, tc)
	require.NoError(t, err)
	hit, ok := overlay[overlayKey(tc, framework)]
	require.True(t, ok, "a terminal error handoff must still join")

	assert.Equal(t, taskID, hit.Task)
	assert.Equal(t, "error", hit.RawResult)
	assert.Empty(t, hit.ArtefactHash,
		"an absent handoff artefact-hash must arrive empty so the triage "+
			"snapshot can omit the field rather than substituting the wiring hash")
	assert.Empty(t, hit.RawCompleted,
		"an absent handoff completed stamp must arrive empty so the triage "+
			"snapshot omits completed_at rather than presenting a substitute")
}
