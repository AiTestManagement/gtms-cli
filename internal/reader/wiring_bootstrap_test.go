package reader

// BUG-102: tests for the WiringBootstrap field on FrameworkEntry.
//
// The field is a pure projection of wiring.ArtefactHash via
// wiring.IsPendingArtefactHash: "pending" when the sentinel is set,
// "ready" when a real hash is present. It surfaces on all three JSON
// commands (status, status <tc>, map) through FrameworkEntry, and must
// NOT alter gaps output.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// setupBootstrapFixture creates a project with one TC and a wiring record.
// When pending is true, artefact-hash is set to the pending sentinel;
// otherwise a real hash is computed from the artefact file on disk.
func setupBootstrapFixture(t *testing.T, pending bool) string {
	t.Helper()
	root := t.TempDir()

	// gtms.config
	writeFile(t, root, "gtms.config", `project:
  name: bootstrap-test
  repo: github.com/example/test
`)

	// TC spec
	tcDir := filepath.Join("gtms", "test", "cases", "bootstrap")
	writeFile(t, root, filepath.Join(tcDir, "tc-b102a001-bootstrap-field.md"), `---
test_case_id: tc-b102a001
title: Bootstrap field test
requirement: REQ-B102
priority: Medium
type: Functional
created: 2026-06-01
---

## Steps
1. Verify wiring_bootstrap field.
`)

	// Artefact file (must exist on disk so artefact_present = true).
	artefactRel := "test/acceptance/bootstrap/tc-b102a001-bootstrap-field.bats"
	artefactAbs := filepath.Join(root, filepath.FromSlash(artefactRel))
	require.NoError(t, os.MkdirAll(filepath.Dir(artefactAbs), 0755))
	require.NoError(t, os.WriteFile(artefactAbs, []byte("# artefact stub\n"), 0644))

	// Compute real testcase-hash from the spec file.
	tcSpecPath := filepath.Join(root, tcDir, "tc-b102a001-bootstrap-field.md")
	tcHash, err := pipeline.HashFile(tcSpecPath)
	require.NoError(t, err)

	// Choose artefact-hash: pending sentinel or real hash.
	artefactHash := wiring.PendingArtefactHash
	if !pending {
		h, hErr := pipeline.HashFile(artefactAbs)
		require.NoError(t, hErr)
		artefactHash = h
	}

	// Write wiring record through the production writer.
	_, err = wiring.Write(root, &wiring.WiringRecord{
		TestCase:     "tc-b102a001",
		TestCaseHash: tcHash,
		Framework:    "bats",
		Adapter:      "bats-runner",
		Artefact:     artefactRel,
		ArtefactHash: artefactHash,
	})
	require.NoError(t, err)

	return root
}

// TestWiringBootstrap_PendingArtefactHash verifies that a wiring record
// with artefact-hash: pending produces wiring_bootstrap: "pending" on
// the FrameworkEntry.
func TestWiringBootstrap_PendingArtefactHash(t *testing.T) {
	root := setupBootstrapFixture(t, true)

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Len(t, entries[0].Frameworks, 1)

	assert.Equal(t, "pending", entries[0].Frameworks[0].WiringBootstrap,
		"artefact-hash sentinel must produce wiring_bootstrap: pending")
	// Pending wiring must still be classified as wired and not stale.
	assert.True(t, entries[0].Wired)
	assert.Equal(t, "", entries[0].Frameworks[0].WiringDrift,
		"pending wiring must not report drift")
}

// TestWiringBootstrap_ReadyArtefactHash verifies that a wiring record
// with a real artefact-hash produces wiring_bootstrap: "ready".
func TestWiringBootstrap_ReadyArtefactHash(t *testing.T) {
	root := setupBootstrapFixture(t, false)

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Len(t, entries[0].Frameworks, 1)

	assert.Equal(t, "ready", entries[0].Frameworks[0].WiringBootstrap,
		"real artefact-hash must produce wiring_bootstrap: ready")
}

// TestWiringBootstrap_DetailInheritsField verifies that PipelineDetail
// surfaces the field via the shared FrameworkEntry (no duplicate field).
func TestWiringBootstrap_DetailInheritsField(t *testing.T) {
	root := setupBootstrapFixture(t, true)

	detail, err := PipelineDetail(root, "tc-b102a001", "", false)
	require.NoError(t, err)
	require.NotNil(t, detail)
	require.Len(t, detail.Frameworks, 1)

	assert.Equal(t, "pending", detail.Frameworks[0].WiringBootstrap,
		"detail view must inherit wiring_bootstrap from FrameworkEntry")
}

// TestWiringBootstrap_MapInheritsField verifies that Map surfaces the
// field via the shared FrameworkEntry on MapEntry.Frameworks[].
func TestWiringBootstrap_MapInheritsField(t *testing.T) {
	root := setupBootstrapFixture(t, true)

	report, err := Map(root, nil, "", false)
	require.NoError(t, err)

	// The TC has requirement REQ-B102, so it appears in groups.
	var found bool
	for _, grp := range report.Groups {
		for _, me := range grp.TestCases {
			if me.TestCaseID == "tc-b102a001" {
				require.Len(t, me.Frameworks, 1)
				assert.Equal(t, "pending", me.Frameworks[0].WiringBootstrap,
					"map entry must inherit wiring_bootstrap from FrameworkEntry")
				found = true
			}
		}
	}
	assert.True(t, found, "TC tc-b102a001 must appear in map report")
}

// TestWiringBootstrap_GapsUnchanged verifies that a pending wiring does
// not appear in any gap category -- pending is not a gap.
func TestWiringBootstrap_GapsUnchanged(t *testing.T) {
	root := setupBootstrapFixture(t, true)

	report, err := Gaps(root, nil, "", false)
	require.NoError(t, err)

	// The TC must NOT appear in stale or missing categories.
	for _, entry := range report.StaleArtefactHash {
		assert.NotEqual(t, "tc-b102a001", entry.ID,
			"pending wiring must not appear in stale_artefact_hash")
	}
	for _, entry := range report.StaleTestCaseHash {
		assert.NotEqual(t, "tc-b102a001", entry.ID,
			"pending wiring must not appear in stale_testcase_hash")
	}
	for _, entry := range report.MissingArtefact {
		assert.NotEqual(t, "tc-b102a001", entry.ID,
			"pending wiring must not appear in missing_artefact")
	}
	for _, entry := range report.NoAutomation {
		assert.NotEqual(t, "tc-b102a001", entry.ID,
			"wired TC must not appear in no_automation")
	}
}

// TestWiringBootstrap_MultiFrameworkMixed verifies that a TC with two
// wiring records -- one pending, one ready -- surfaces both states
// independently on their respective FrameworkEntry rows.
func TestWiringBootstrap_MultiFrameworkMixed(t *testing.T) {
	root := setupBootstrapFixture(t, true) // creates bats wiring with pending

	// Add a second wiring record (playwright) with a ready hash.
	artefact2Rel := "test/acceptance/bootstrap/tc-b102a001-bootstrap-field.spec.ts"
	artefact2Abs := filepath.Join(root, filepath.FromSlash(artefact2Rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(artefact2Abs), 0755))
	require.NoError(t, os.WriteFile(artefact2Abs, []byte("// playwright stub\n"), 0644))

	tcSpecPath := filepath.Join(root, "gtms", "test", "cases", "bootstrap", "tc-b102a001-bootstrap-field.md")
	tcHash, err := pipeline.HashFile(tcSpecPath)
	require.NoError(t, err)
	artefact2Hash, err := pipeline.HashFile(artefact2Abs)
	require.NoError(t, err)

	_, err = wiring.Write(root, &wiring.WiringRecord{
		TestCase:     "tc-b102a001",
		TestCaseHash: tcHash,
		Framework:    "playwright",
		Adapter:      "playwright-runner",
		Artefact:     artefact2Rel,
		ArtefactHash: artefact2Hash,
	})
	require.NoError(t, err)

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Len(t, entries[0].Frameworks, 2, "two wiring records expected")

	// Frameworks are sorted lexically: bats first, playwright second.
	assert.Equal(t, "bats", entries[0].Frameworks[0].Framework)
	assert.Equal(t, "pending", entries[0].Frameworks[0].WiringBootstrap,
		"bats wiring has pending artefact-hash")

	assert.Equal(t, "playwright", entries[0].Frameworks[1].Framework)
	assert.Equal(t, "ready", entries[0].Frameworks[1].WiringBootstrap,
		"playwright wiring has real artefact-hash")
}
