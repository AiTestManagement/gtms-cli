package link

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// seedLinkedWiring creates a spec + artefact, links them, and returns the
// wiring record and the artefact path. It is the shared fixture for the
// ENH-231 acceptance tests.
func seedLinkedWiring(t *testing.T, root, tcID, artefactRel string) (*wiring.WiringRecord, string) {
	t.Helper()
	createTCSpecFixture(t, root, tcID)
	artefactAbs := filepath.Join(root, artefactRel)
	require.NoError(t, os.MkdirAll(filepath.Dir(artefactAbs), 0755))
	require.NoError(t, os.WriteFile(artefactAbs, []byte("@test \"x\" { true; }\n"), 0644))

	cfg := &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"exec-pass": {Framework: "bats", Mode: "sync", Command: "echo ok"},
			},
		},
		Defaults: map[string]string{"execute": "exec-pass"},
	}
	_, _, err := LinkRecord(root, cfg, tcID, "bats", artefactRel, false, false)
	require.NoError(t, err)

	rec, _, err := wiring.Find(root, tcID, "bats")
	require.NoError(t, err)
	require.NotNil(t, rec)
	return rec, artefactAbs
}

// selectionFor builds the selection execute would carry for rec when it was
// read from its canonical file for (rec.TestCase, rec.Framework).
func selectionFor(t *testing.T, root string, rec *wiring.WiringRecord) wiring.Selection {
	t.Helper()
	p, err := wiring.Path(root, rec.TestCase, rec.Framework)
	require.NoError(t, err)
	return wiring.Selection{Record: rec, Path: p, TestCase: rec.TestCase, Framework: rec.Framework}
}

func TestAcceptScriptDrift_WritesOnlyArtefactHash(t *testing.T) {
	root := t.TempDir()
	rec, artefactAbs := seedLinkedWiring(t, root, "tc-abc12345", "tests/tc-abc12345.bats")
	oldArt := rec.ArtefactHash
	oldSpec := rec.TestCaseHash

	// Edit the script so its hash drifts.
	require.NoError(t, os.WriteFile(artefactAbs, []byte("@test \"x\" { true; }\n# edited\n"), 0644))
	newArt, err := pipeline.HashFile(artefactAbs)
	require.NoError(t, err)
	require.NotEqual(t, oldArt, newArt)

	accepted, err := AcceptScriptDrift(root, selectionFor(t, root, rec))
	require.NoError(t, err)
	assert.Equal(t, newArt, accepted, "AcceptScriptDrift must return the exact hash it wrote")

	got, _, err := wiring.Find(root, "tc-abc12345", "bats")
	require.NoError(t, err)
	assert.Equal(t, newArt, got.ArtefactHash, "artefact-hash must be updated to the current script")
	assert.Equal(t, oldSpec, got.TestCaseHash, "testcase-hash must be preserved (spec drift stays strict)")
	assert.Equal(t, rec.Adapter, got.Adapter)
	assert.Equal(t, rec.Artefact, got.Artefact)
}

func TestAcceptScriptDrift_PendingIsNoOp(t *testing.T) {
	root := t.TempDir()
	rec, _ := seedLinkedWiring(t, root, "tc-abc12345", "tests/tc-abc12345.bats")
	rec.ArtefactHash = wiring.PendingArtefactHash

	accepted, err := AcceptScriptDrift(root, selectionFor(t, root, rec))
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNothingToAccept), "pending is bootstrap state, not drift")
	assert.Empty(t, accepted)
}

func TestAcceptScriptDrift_MissingArtefactRefused(t *testing.T) {
	root := t.TempDir()
	rec, artefactAbs := seedLinkedWiring(t, root, "tc-abc12345", "tests/tc-abc12345.bats")
	require.NoError(t, os.Remove(artefactAbs))

	before, _, err := wiring.Find(root, "tc-abc12345", "bats")
	require.NoError(t, err)

	accepted, err := AcceptScriptDrift(root, selectionFor(t, root, rec))
	require.Error(t, err)
	assert.Empty(t, accepted)

	after, _, err := wiring.Find(root, "tc-abc12345", "bats")
	require.NoError(t, err)
	assert.Equal(t, before.ArtefactHash, after.ArtefactHash, "wiring must be unchanged when the artefact is missing")
}

func TestAcceptScriptDrift_ConcurrentChangeRefused(t *testing.T) {
	root := t.TempDir()
	rec, artefactAbs := seedLinkedWiring(t, root, "tc-abc12345", "tests/tc-abc12345.bats")
	require.NoError(t, os.WriteFile(artefactAbs, []byte("@test \"x\" { true; }\n# edited\n"), 0644))

	// Simulate a concurrent writer changing the on-disk record AFTER the
	// preflight examined `rec` but before acceptance writes: repoint its adapter.
	onDiskChanged := &wiring.WiringRecord{
		TestCase:     rec.TestCase,
		TestCaseHash: rec.TestCaseHash,
		Framework:    rec.Framework,
		Adapter:      "exec-other",
		Artefact:     rec.Artefact,
		ArtefactHash: rec.ArtefactHash,
	}
	_, err := wiring.Write(root, onDiskChanged)
	require.NoError(t, err)

	accepted, err := AcceptScriptDrift(root, selectionFor(t, root, rec))
	assert.Empty(t, accepted)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrConcurrentWiringChange), "a concurrent change must refuse, not overwrite")

	// The concurrent writer's record survives unchanged.
	after, _, err := wiring.Find(root, "tc-abc12345", "bats")
	require.NoError(t, err)
	assert.Equal(t, "exec-other", after.Adapter)
	assert.Equal(t, onDiskChanged.ArtefactHash, after.ArtefactHash)
}

// TestAcceptScriptDrift_SharesRefreshSafety proves AcceptScriptDrift and
// PlanRefresh reject the same unsafe/missing artefact inputs via the shared
// validateWiringArtefact primitive.
func TestAcceptScriptDrift_SharesRefreshSafety(t *testing.T) {
	root := t.TempDir()
	// Unsafe path escaping the root.
	_, err := validateWiringArtefact(root, "../escape.bats")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsafe artefact path for wiring")

	// Missing but contained path.
	_, err = validateWiringArtefact(root, "tests/nope.bats")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "artefact file not found")
}

// TestAcceptScriptDrift_IdentityMismatchRefused covers REV-232 CODEX-001 at the
// helper level: a selected file whose body declares another test case or
// framework, or a selection whose path is not the canonical file for its
// expected identity, is refused before hashing or writing. Both the selected
// file and the file the body points at stay byte-for-byte unchanged.
func TestAcceptScriptDrift_IdentityMismatchRefused(t *testing.T) {
	cases := []struct {
		name       string
		bodyTC     string // testcase declared in the selected file's body
		bodyFW     string // framework declared in the selected file's body
		pathFW     string // framework in the selection path (default: expected)
		expectedFW string
	}{
		{name: "body declares another test case", bodyTC: "tc-bbbbbbbb", bodyFW: "bats", expectedFW: "bats"},
		{name: "body declares another framework", bodyTC: "tc-aaaaaaaa", bodyFW: "playwright", expectedFW: "bats"},
		{name: "selected path is not canonical for the expected identity", bodyTC: "tc-aaaaaaaa", bodyFW: "bats", pathFW: "playwright", expectedFW: "bats"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			createTCSpecFixture(t, root, "tc-aaaaaaaa")
			createTCSpecFixture(t, root, "tc-bbbbbbbb")
			artefactRel := "tests/shared.bats"
			artefactAbs := filepath.Join(root, artefactRel)
			require.NoError(t, os.MkdirAll(filepath.Dir(artefactAbs), 0755))
			require.NoError(t, os.WriteFile(artefactAbs, []byte("@test \"x\" { true; }\n# edited\n"), 0644))

			body := &wiring.WiringRecord{
				TestCase: tc.bodyTC, TestCaseHash: "0011223344556677", Framework: tc.bodyFW,
				Adapter: "exec-pass", Artefact: artefactRel, ArtefactHash: "aabbccddeeff0011",
			}
			raw, err := yaml.Marshal(body)
			require.NoError(t, err)

			pathFW := tc.pathFW
			if pathFW == "" {
				pathFW = tc.expectedFW
			}
			selectedPath, err := wiring.Path(root, "tc-aaaaaaaa", pathFW)
			require.NoError(t, err)
			require.NoError(t, os.MkdirAll(filepath.Dir(selectedPath), 0755))
			require.NoError(t, os.WriteFile(selectedPath, raw, 0644))
			// The file the body's identity points at holds the same bytes, so
			// a body-derived write would pass a body-derived re-read.
			bodyPath, err := wiring.Path(root, tc.bodyTC, tc.bodyFW)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(bodyPath, raw, 0644))

			accepted, err := AcceptScriptDrift(root, wiring.Selection{
				Record: body, Path: selectedPath, TestCase: "tc-aaaaaaaa", Framework: tc.expectedFW,
			})
			require.Error(t, err)
			assert.True(t, errors.Is(err, wiring.ErrIdentityMismatch), "got %v", err)
			assert.Empty(t, accepted)

			for _, p := range []string{selectedPath, bodyPath} {
				got, rerr := os.ReadFile(p)
				require.NoError(t, rerr)
				assert.Equal(t, string(raw), string(got), "%s must be byte-for-byte unchanged", p)
			}
		})
	}
}

// The re-read of the exact selected file must also confirm identity: a file
// rewritten after the preflight to declare another identity is refused.
func TestAcceptScriptDrift_ReReadIdentityChangeRefused(t *testing.T) {
	root := t.TempDir()
	rec, artefactAbs := seedLinkedWiring(t, root, "tc-abc12345", "tests/tc-abc12345.bats")
	require.NoError(t, os.WriteFile(artefactAbs, []byte("@test \"x\" { true; }\n# edited\n"), 0644))
	sel := selectionFor(t, root, rec)

	swapped := *rec
	swapped.Framework = "playwright"
	raw, err := yaml.Marshal(&swapped)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(sel.Path, raw, 0644))

	accepted, err := AcceptScriptDrift(root, sel)
	require.Error(t, err)
	assert.True(t, errors.Is(err, wiring.ErrIdentityMismatch), "got %v", err)
	assert.Empty(t, accepted)
	got, err := os.ReadFile(sel.Path)
	require.NoError(t, err)
	assert.Equal(t, string(raw), string(got))
	_, statErr := os.Stat(filepath.Join(filepath.Dir(sel.Path), "tc-abc12345--playwright.wiring.yaml"))
	assert.True(t, os.IsNotExist(statErr), "no wiring may be created at the body-derived path")
}

// REV-232 DCODEX-001: another command updates the wiring while the artefact is
// being validated and hashed (after the early re-read). The final re-read
// immediately before the write must catch it, refuse, and leave the newer
// wiring byte-for-byte unchanged.
func TestAcceptScriptDrift_WiringChangedDuringHashingSurvives(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(r *wiring.WiringRecord)
	}{
		{name: "testcase-hash", mutate: func(r *wiring.WiringRecord) { r.TestCaseHash = "1111111111111111" }},
		{name: "adapter", mutate: func(r *wiring.WiringRecord) { r.Adapter = "exec-other" }},
		{name: "artefact", mutate: func(r *wiring.WiringRecord) { r.Artefact = "tests/other.bats" }},
		{name: "artefact-hash", mutate: func(r *wiring.WiringRecord) { r.ArtefactHash = "2222222222222222" }},
		{name: "test-case", mutate: func(r *wiring.WiringRecord) { r.TestCase = "tc-99999999" }},
		{name: "framework", mutate: func(r *wiring.WiringRecord) { r.Framework = "playwright" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			rec, artefactAbs := seedLinkedWiring(t, root, "tc-abc12345", "tests/tc-abc12345.bats")
			require.NoError(t, os.WriteFile(artefactAbs, []byte("@test \"x\" { true; }\n# edited\n"), 0644))
			sel := selectionFor(t, root, rec)

			// The newer wiring a concurrent command writes during hashing,
			// written straight to the selected file so identity changes stay
			// at the selected path.
			newer := *rec
			tc.mutate(&newer)
			newerBytes, err := yaml.Marshal(&newer)
			require.NoError(t, err)
			fired := false
			acceptTestHook = func(stage string) {
				if stage == "post-hash" {
					fired = true
					require.NoError(t, os.WriteFile(sel.Path, newerBytes, 0644))
				}
			}
			t.Cleanup(func() { acceptTestHook = nil })

			accepted, err := AcceptScriptDrift(root, sel)
			require.True(t, fired, "the post-hash hook must have run")
			assert.Empty(t, accepted)
			require.Error(t, err, "a wiring change during hashing must refuse")
			// Every field, identity included, changed after selection was
			// verified, so each is a concurrent change the user should retry.
			assert.True(t, errors.Is(err, ErrConcurrentWiringChange), "got %v", err)

			got, err := os.ReadFile(sel.Path)
			require.NoError(t, err)
			assert.Equal(t, string(newerBytes), string(got), "the newer wiring must survive unchanged")
		})
	}
}
