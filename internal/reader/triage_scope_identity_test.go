package reader

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
)

// REV-148 regression coverage for the shared scoped triage enumerator.
//
// Two properties are pinned here, and they pull in opposite directions, which
// is why they live together:
//
//  1. Identity (CODEX-001): a triage record belongs to the test case its BODY
//     names, never to a shorter ID whose filename prefix it happens to match.
//     "tc-aaaa1111--legacy--bats.triage.yaml" starts with "tc-aaaa1111--" but
//     belongs to tc-aaaa1111--legacy.
//  2. Scope (ENH-201 boundary): a malformed record OUTSIDE the requested scope
//     must not fail a scoped query, while one inside it must.
//
// A naive "read every record and key by body identity" pass satisfies (1) and
// breaks (2), because it opens out-of-scope records to discover their identity.

const (
	tcShort = "tc-aaaa1111"
	tcLong  = "tc-aaaa1111--legacy"
)

// putMalformedTriageRecord writes a record that triage.Read must refuse. The
// filename is well-formed, so it survives enumeration and only fails on read --
// which is exactly the case that decides whether scope is honoured.
func putMalformedTriageRecord(t *testing.T, root, tc, fw string) string {
	t.Helper()
	rel := filepath.Join("gtms", "triage", tc+"--"+fw+".triage.yaml")
	writeFile(t, root, rel, "version: 1\nbogus-unknown-key: 1\n")
	return filepath.Join(root, rel)
}

// triageRoot returns a CANONICAL project root.
//
// BUG-187 gave the triage store a physical boundary: the store directory is
// resolved through pathsafe.CanonicalRoot (filepath.EvalSymlinks) before any
// record path is built, so every path the product reports is the canonical one.
// t.TempDir() does not always hand back a canonical path -- on Windows it can be
// the 8.3 short form (C:\Users\RUNNER~1\...), and on macOS /var is a symlink to
// /private/var. A test that derived its expected path from the raw temp dir then
// compared a short-form path against the product's long-form one and failed on
// those platforms only, while passing on Linux where the two forms coincide.
//
// Canonicalising here keeps the expectations in this file in the same form the
// product uses. It is a no-op wherever t.TempDir() is already canonical.
func triageRoot(t *testing.T) string {
	t.Helper()
	root := pathsafe.CanonicalRoot(t.TempDir())
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	return root
}

// --- Identity: the prefix collision ---

func TestScopedTriage_PrefixCollisionIsNotAttributedToShorterID(t *testing.T) {
	root := triageRoot(t)
	// Only the LONGER test case is classified.
	putTriageRecord(t, root, tcLong, "bats", "environment", "fail", "")

	t.Run("scope holds only the shorter ID", func(t *testing.T) {
		byTC, err := activeTriageByTC(root, []string{tcShort})
		require.NoError(t, err)
		assert.Nil(t, byTC[tcShort],
			"a record whose body names %s must never be attributed to %s, whose "+
				"filename prefix it merely matches", tcLong, tcShort)
	})

	t.Run("scope holds only the longer ID", func(t *testing.T) {
		byTC, err := activeTriageByTC(root, []string{tcLong})
		require.NoError(t, err)
		require.NotNil(t, byTC[tcLong], "the owning test case must still find its record")
		assert.Equal(t, "environment", byTC[tcLong]["bats"].Value)
	})
}

func TestScopedTriage_BothIDsClassifiedDifferently(t *testing.T) {
	root := triageRoot(t)
	putTriageRecord(t, root, tcShort, "bats", "app-wrong", "fail", "")
	putTriageRecord(t, root, tcLong, "bats", "environment", "fail", "")

	t.Run("folder scope containing only one", func(t *testing.T) {
		byTC, err := activeTriageByTC(root, []string{tcShort})
		require.NoError(t, err)
		require.NotNil(t, byTC[tcShort])
		assert.Equal(t, "app-wrong", byTC[tcShort]["bats"].Value,
			"the shorter ID must keep its OWN value, not inherit the sibling's")
		assert.Nil(t, byTC[tcLong], "the out-of-scope test case must not appear")
	})

	t.Run("root scope containing both", func(t *testing.T) {
		byTC, err := activeTriageByTC(root, nil)
		require.NoError(t, err)
		require.NotNil(t, byTC[tcShort])
		require.NotNil(t, byTC[tcLong])
		assert.Equal(t, "app-wrong", byTC[tcShort]["bats"].Value)
		assert.Equal(t, "environment", byTC[tcLong]["bats"].Value,
			"each record must land on the test case its body names")
	})
}

// --- Scope: the malformed-record failure boundary ---

func TestScopedTriage_MalformedOutOfScopeDoesNotPoisonFolder(t *testing.T) {
	root := triageRoot(t)
	putTriageRecord(t, root, "tc-bbbb2222", "bats", "environment", "fail", "")
	putMalformedTriageRecord(t, root, "tc-cccc3333", "bats") // a different folder's record

	byTC, err := activeTriageByTC(root, []string{"tc-bbbb2222"})
	require.NoError(t, err,
		"a malformed record outside the requested scope must not fail a scoped query")
	require.NotNil(t, byTC["tc-bbbb2222"])
	assert.Equal(t, "environment", byTC["tc-bbbb2222"]["bats"].Value)
}

func TestScopedTriage_MalformedInScopeFailsAndNamesPath(t *testing.T) {
	root := triageRoot(t)
	path := putMalformedTriageRecord(t, root, "tc-bbbb2222", "bats")

	_, err := activeTriageByTC(root, []string{"tc-bbbb2222"})
	require.Error(t, err, "a malformed record INSIDE scope must fail closed")
	assert.Contains(t, err.Error(), path,
		"the error must name the offending file so it can be corrected")
}

func TestScopedTriage_RootScopeValidatesEveryRecordIncludingOrphans(t *testing.T) {
	root := triageRoot(t)
	// No spec exists for this test case: at root scope it is still read and
	// validated, so a corrupt orphan is reported rather than silently skipped.
	path := putMalformedTriageRecord(t, root, "tc-dddd4444", "bats")

	_, err := activeTriageByTC(root, nil)
	require.Error(t, err, "root scope must read and validate every triage record")
	assert.Contains(t, err.Error(), path)
}

// --- The agreed --framework JSON contract ---

// TestAttachTriageToDetail_SiblingFrameworkEntryStaysComplete pins the contract
// decision recorded in REV-148 against CODEX-002 / CLAUDE-001, which was
// DISMISSED rather than fixed.
//
// The ENH-146 JSON shape returns a frameworks[] array of per-framework
// information. A triage object nested under the playwright entry describes
// playwright and is therefore correctly attributed, not misattributed.
// --framework governs selected_framework and human presentation; it does not
// strip sibling entries of their own metadata. tc-82eebc80 step 1 scopes its
// absence assertion to the HUMAN view, and step 9 exercises --json without
// --framework, so neither spec requires the pruning the reviewers assumed.
//
// This test exists so that contract is enforced rather than merely intended: a
// future change that prunes sibling metadata will fail here with the reason.
func TestAttachTriageToDetail_SiblingFrameworkEntryStaysComplete(t *testing.T) {
	root := triageRoot(t)
	putTriageRecord(t, root, "tc-eeee5555", "playwright", "app-wrong", "fail", "")

	detail := &PipelineDetailEntry{
		TestCaseID:        "tc-eeee5555",
		SelectedFramework: "bats",
		Frameworks: []FrameworkEntry{
			{Framework: "bats"},
			{Framework: "playwright"},
		},
	}
	require.NoError(t, AttachTriageToDetail(root, detail, newTriageValues(), "app-wrong", ""))

	var bats, playwright *FrameworkTriage
	for i := range detail.Frameworks {
		switch detail.Frameworks[i].Framework {
		case "bats":
			bats = detail.Frameworks[i].Triage
		case "playwright":
			playwright = detail.Frameworks[i].Triage
		}
	}

	assert.Nil(t, bats,
		"the selected framework has no classification, so its entry carries no "+
			"triage member (tc-cd5d4d75 step 7)")
	require.NotNil(t, playwright,
		"the playwright entry must keep its own classification: --framework "+
			"selects a framework for presentation, it does not make other "+
			"frameworks' entries incomplete (REV-148, CODEX-002 dismissed)")
	assert.Equal(t, "app-wrong", playwright.Value)
}
