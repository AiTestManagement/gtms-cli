package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/aitestmanagement/gtms-cli/internal/reader"
)

// BUG-193 worktree review (2026-09-13) defence in depth for the BUG-193 entry-level triage carrier: a
// selected (wired) framework answers for itself and never inherits an
// entry-level manual triage, even if the reader were ever to set one on a
// wired entry. Only a manual-only entry (no selected framework) reads it.
func TestSelectedFrameworkTriage_BUG193_WiredNeverInheritsEntryLevel(t *testing.T) {
	manual := &reader.FrameworkTriage{Value: "environment", Status: "active"}

	wired := reader.PipelineEntry{
		TestCaseID:        "tc-22222222",
		Wired:             true,
		SelectedFramework: "reqnroll",
		Frameworks:        []reader.FrameworkEntry{{Framework: "reqnroll", Wired: true}},
		Triage:            manual, // hostile shape: must be ignored
	}
	assert.Nil(t, selectedFrameworkTriage(wired), "wired selection must not inherit the entry-level manual triage")
	assert.Empty(t, filterEntriesByTriage([]reader.PipelineEntry{wired}), "--triage filtering must not match through the entry-level carrier")

	manualOnly := reader.PipelineEntry{
		TestCaseID: "tc-11111111",
		Wired:      false,
		Frameworks: []reader.FrameworkEntry{},
		Triage:     manual,
	}
	assert.Same(t, manual, selectedFrameworkTriage(manualOnly), "manual-only entry reads the entry-level carrier")

	wiredDetail := &reader.PipelineDetailEntry{
		TestCaseID:        "tc-22222222",
		Wired:             true,
		SelectedFramework: "reqnroll",
		Frameworks:        []reader.FrameworkEntry{{Framework: "reqnroll", Wired: true}},
		Triage:            manual,
	}
	assert.Nil(t, selectedDetailTriage(wiredDetail))

	manualOnlyDetail := &reader.PipelineDetailEntry{TestCaseID: "tc-11111111", Triage: manual}
	assert.Same(t, manual, selectedDetailTriage(manualOnlyDetail))
}
