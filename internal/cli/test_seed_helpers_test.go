package cli

// CON-023 / ENH-145 / ENH-146 test seed helpers for the cli package.
//
// The legacy fixture shape was a single .automation.md file with YAML
// frontmatter carrying identity + outcome. Post-cutover that splits into:
//
//   gtms/automation/wiring/{tc}--{framework}.wiring.yaml — identity
//   .gtms/results/{taskID}.handoff.yaml                  — terminal outcome
//   gtms/manual/records/{tc}--manual.result.yaml         — manual TCs
//
// seedLegacyRecord takes a small struct mirroring the legacy frontmatter
// fields tests cared about and emits the correct combination of files
// for the new reader to surface the same view.
//
// Refactor (Phase 3A cleanup): wiring and result-contract writes now
// go through internal/wiring.Write and internal/result.Create rather
// than hand-built YAML strings, so the seed helpers always emit the
// exact on-disk shape that production writers do. The single
// hand-built YAML that remains is the manual result file: it follows
// a user-authored schema (see adapter.ManualResultFile) and a small
// local seedManualRecord struct mirrors it here so the cli test
// package doesn't import the adapter package.

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// legacyRecord mirrors the legacy frontmatter shape. Empty strings mean
// "field absent" — the helper emits a wiring-only seed if Result is
// empty, otherwise also a terminal handoff.
type legacyRecord struct {
	TC               string
	Framework        string
	Adapter          string
	Artefact         string
	TestCaseHash     string
	ArtefactHash     string
	Result           string
	ExecutedAt       string
	ExecutedArtefact string
	ExecutedBy       string
	Environment      string
	Notes            string
	Summary          string
	Attempts         int
	AdapterError     bool
	SkipArtefactFile bool
}

// seedManualRecord mirrors adapter.ManualResultFile so the cli test
// package can write manual fixtures without importing the adapter
// package.
type seedManualRecord struct {
	TestCase  string `yaml:"test_case_id"`
	Framework string `yaml:"framework"`
	Result    string `yaml:"result,omitempty"`
}

// seedLegacyRecord writes the wiring + (optional) handoff + (optional)
// manual record for one legacy-record fixture.
func seedLegacyRecord(t *testing.T, root string, r legacyRecord) {
	t.Helper()
	if r.TC == "" {
		t.Fatalf("seedLegacyRecord: TC is required")
	}
	if r.Framework == "" {
		t.Fatalf("seedLegacyRecord: Framework is required")
	}

	// Manual TCs: no wiring. The reader surfaces them via the manual
	// record file.
	if r.Framework == "manual" {
		mDir := filepath.Join(root, "gtms", "manual", "records")
		require.NoError(t, os.MkdirAll(mDir, 0755))
		body, err := yaml.Marshal(seedManualRecord{
			TestCase:  r.TC,
			Framework: "manual",
			Result:    r.Result,
		})
		require.NoError(t, err)
		path := filepath.Join(mDir, r.TC+"--manual.result.yaml")
		require.NoError(t, os.WriteFile(path, body, 0644))
		return
	}

	// Apply defaults.
	if r.Adapter == "" {
		r.Adapter = r.Framework + "-runner"
	}
	if r.Artefact == "" {
		r.Artefact = "test/acceptance/" + r.TC + ".bats"
	}

	// Real hashes where possible so the wiring lands in TierCurrent and
	// drift/missing-artefact gap categories stay quiet for these tests.
	tcSpecPath, _ := pipeline.ResolveTestCaseSpec(root, r.TC)
	if tcSpecPath != "" && r.TestCaseHash == "" {
		if h, err := pipeline.HashFile(filepath.Join(root, filepath.FromSlash(tcSpecPath))); err == nil {
			r.TestCaseHash = h
		}
	}
	if r.TestCaseHash == "" {
		r.TestCaseHash = "0011223344556677"
	}

	artefactAbs := filepath.Join(root, filepath.FromSlash(r.Artefact))
	if !r.SkipArtefactFile {
		if _, err := os.Stat(artefactAbs); err != nil {
			require.NoError(t, os.MkdirAll(filepath.Dir(artefactAbs), 0755))
			require.NoError(t, os.WriteFile(artefactAbs,
				[]byte("# fixture artefact for "+r.TC+"\n"), 0644))
		}
	}
	if r.ArtefactHash == "" {
		if h, err := pipeline.HashFile(artefactAbs); err == nil {
			r.ArtefactHash = h
		}
	}
	if r.ArtefactHash == "" {
		r.ArtefactHash = "aabbccddeeff0011"
	}

	// Write wiring through the production writer.
	_, err := wiring.Write(root, &wiring.WiringRecord{
		TestCase:     r.TC,
		TestCaseHash: r.TestCaseHash,
		Framework:    r.Framework,
		Adapter:      r.Adapter,
		Artefact:     r.Artefact,
		ArtefactHash: r.ArtefactHash,
	})
	require.NoError(t, err)

	if r.Result == "" && !r.AdapterError {
		return
	}

	if r.ExecutedAt == "" {
		r.ExecutedAt = "2026-05-19T10:01:00Z"
	}

	taskID := fmt.Sprintf("task-%s-%s", r.TC, r.Framework)
	contractResult := r.Result
	if contractResult == "skipped" {
		contractResult = "skip"
	}

	rc := &result.ResultContract{
		Task:        taskID,
		Command:     "execute",
		Target:      r.TC,
		Adapter:     r.Adapter,
		Mode:        "sync",
		Created:     "2026-05-19T10:00:00Z",
		Framework:   r.Framework,
		Completed:   r.ExecutedAt,
		Artefact:    r.ExecutedArtefact,
		ExecutedBy:  r.ExecutedBy,
		Environment: r.Environment,
		Summary:     r.Summary,
		Log:         r.Notes,
		Attempts:    r.Attempts,
	}
	if r.AdapterError {
		rc.Status = "error"
	} else {
		rc.Status = "complete"
		rc.Result = contractResult
	}

	_, err = result.Create(root, rc)
	require.NoError(t, err)
}
