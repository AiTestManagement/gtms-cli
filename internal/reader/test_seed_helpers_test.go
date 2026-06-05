package reader

// CON-023 / ENH-145 / ENH-146 test seed helpers.
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
// for the new reader to surface the same view. Tests that previously
// inlined the YAML can call seedLegacyRecord instead.
//
// Refactor (Phase 3A cleanup): wiring and result-contract writes now
// go through internal/wiring.Write and internal/result.Create rather
// than hand-built YAML strings, so the seed helpers always emit the
// exact on-disk shape that production writers do. The single
// hand-built YAML that remains is the manual result file: it follows
// a different, user-authored schema (see adapter.ManualResultFile)
// that lives in the adapter package and isn't reusable from a reader
// test helper without a cyclic import. A small local seedManualRecord
// struct mirrors that schema instead.

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
	TC               string // testcase ID (required)
	Framework        string // framework (required; "manual" routes to manual-record path)
	Adapter          string // adapter name; defaults to "{framework}-runner"
	Artefact         string // artefact path; defaults to test/acceptance/{tc}.bats
	TestCaseHash     string // testcase content hash; defaults to "0011223344556677"
	ArtefactHash     string // artefact content hash; defaults to "aabbccddeeff0011"
	Result           string // pass | fail | skip | skipped | error | "" (no terminal result)
	ExecutedAt       string // RFC3339 UTC; defaults to "2026-05-19T10:01:00Z"
	ExecutedArtefact string // optional artefact path written by run
	ExecutedBy       string
	Environment      string
	Notes            string // diagnostic log
	Summary          string
	Attempts         int
	// AdapterError == true forces handoff status: error (no result field).
	// Used to model the legacy "last-dev-result: error" path.
	AdapterError bool
	// SkipArtefactFile == true suppresses the auto-stub artefact write
	// (used by tests that exercise the missing-artefact tier).
	SkipArtefactFile bool
}

// seedManualRecord is the in-package mirror of adapter.ManualResultFile.
// It's local to the test helper so the reader test code does not import
// the adapter package (which would risk a cycle and isn't necessary —
// the on-disk YAML is the contract).
type seedManualRecord struct {
	TestCase  string `yaml:"test_case_id"`
	Framework string `yaml:"framework"`
	Result    string `yaml:"result,omitempty"`
}

// seedLegacyRecord writes the wiring + (optional) handoff + (optional)
// manual record for one legacy-record fixture. Use this in test setup in
// place of an inline writeFile() of a .automation.md file.
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

	// Apply defaults so legacy callers don't have to plumb hashes.
	if r.Adapter == "" {
		r.Adapter = r.Framework + "-runner"
	}
	if r.Artefact == "" {
		r.Artefact = "test/acceptance/" + r.TC + ".bats"
	}

	// Compute real hashes from disk so the wiring record passes the
	// reader's classification gate (TierCurrent — both hashes match
	// current content AND artefact present). Tests that want to assert
	// drift or missing-artefact behaviour set TestCaseHash / ArtefactHash
	// explicitly and that value wins over the disk-derived one.
	tcSpecPath, _ := pipeline.ResolveTestCaseSpec(root, r.TC)
	if tcSpecPath != "" && r.TestCaseHash == "" {
		if h, err := pipeline.HashFile(filepath.Join(root, filepath.FromSlash(tcSpecPath))); err == nil {
			r.TestCaseHash = h
		}
	}
	if r.TestCaseHash == "" {
		r.TestCaseHash = "0011223344556677"
	}

	// Auto-create a stub artefact file if the caller didn't and we're
	// going to need its content for the hash. Tests that want to test the
	// missing-artefact tier explicitly leave SkipArtefactFile=true.
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

	// Write wiring through the production writer so the on-disk shape
	// always matches what gtms automate / gtms link would produce.
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
		// No terminal result — wiring exists but the (tc, fw) never executed.
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

	// result.Create writes to .gtms/results/<task-id>.handoff.yaml and
	// runs the same validation production uses. The validation guarantees
	// pending/in-progress contracts don't carry result, status:complete
	// carries one of pass/fail/skip/error, etc.
	_, err = result.Create(root, rc)
	require.NoError(t, err)
}

// seedWiringWithRealArtefactHash writes wiring + artefact files with a
// computed real hash, so isStaleArtefact returns false. Convenience for
// the freshness-check tests.
func seedWiringWithRealArtefactHash(t *testing.T, root, tcID, framework, artefactPath, artefactContent string) string {
	t.Helper()
	abs := filepath.Join(root, artefactPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0755))
	require.NoError(t, os.WriteFile(abs, []byte(artefactContent), 0644))
	return abs
}
