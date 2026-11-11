package cli

// ENH-225 JSON byte-equality goldens.
//
// ENH-225 changes only human-output wording in gtms status / gtms map /
// gtms gaps. These goldens prove the three --json contracts are byte-for-byte
// unchanged by the wording edits. They were captured from the UNMODIFIED
// (pre-ENH-225) code against a deterministic drift fixture (spec drift, script
// drift, both, no drift) and must keep matching after the product edits.
//
// The fixture never executes, so no mtime- or date-derived fields appear in the
// JSON and all artefact paths are relative -- the output is deterministic and
// the golden is stable across machines.
//
// To regenerate (only when the JSON contract legitimately changes, which
// ENH-225 does NOT): run with GTMS_UPDATE_GOLDEN=1.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupENH225DriftFixture builds a four-test-case drift fixture in a temp
// project and returns the project root and the drift-labels scope.
//
//	tc-11111111 -- spec drift   (spec rewritten after wiring seeded)
//	tc-22222222 -- script drift (artefact rewritten after wiring seeded)
//	tc-33333333 -- both          (spec and artefact rewritten)
//	tc-44444444 -- no drift      (control)
func setupENH225DriftFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	type fx struct {
		id         string
		slug       string
		title      string
		driftSpec  bool
		driftScript bool
	}
	fixtures := []fx{
		{"tc-11111111", "spec-drift", "Spec drift fixture", true, false},
		{"tc-22222222", "script-drift", "Script drift fixture", false, true},
		{"tc-33333333", "both-drift", "Both drift fixture", true, true},
		{"tc-44444444", "no-drift", "No drift control", false, false},
	}

	spec := func(id, title string) string {
		return "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\n" +
			"test_type: Functional\ntest_case_id: " + id + "\ntitle: " + title +
			"\nrequirement: ENH-225-FIXTURE\n---\n\n## Test Objective\n\nFixture.\n"
	}

	for _, f := range fixtures {
		specRel := filepath.Join("gtms/test/cases/drift-labels", f.id+"-"+f.slug+".md")
		writeTestFile(t, root, specRel, spec(f.id, f.title))
		// Seed wiring (+ artefact) with hashes matching the original content.
		seedLegacyRecord(t, root, legacyRecord{TC: f.id, Framework: "bats"})

		if f.driftSpec {
			writeTestFile(t, root, specRel, spec(f.id, f.title)+"\n## Drift\n\n# drift introduced for ENH-225\n")
		}
		if f.driftScript {
			artefact := filepath.Join(root, "test/acceptance", f.id+".bats")
			require.NoError(t, os.WriteFile(artefact,
				[]byte("# fixture artefact for "+f.id+"\n# drift introduced for ENH-225\n"), 0o644))
		}
	}
	return root
}

func assertGoldenJSON(t *testing.T, name string, got []byte) {
	t.Helper()
	goldenPath := filepath.Join("testdata", "enh225", name)
	if os.Getenv("GTMS_UPDATE_GOLDEN") == "1" {
		require.NoError(t, os.MkdirAll(filepath.Dir(goldenPath), 0o755))
		require.NoError(t, os.WriteFile(goldenPath, got, 0o644))
		t.Logf("updated golden %s (%d bytes)", goldenPath, len(got))
		return
	}
	want, err := os.ReadFile(goldenPath)
	require.NoError(t, err, "golden %s missing -- regenerate with GTMS_UPDATE_GOLDEN=1", goldenPath)
	if !bytes.Equal(want, got) {
		t.Fatalf("JSON contract changed for %s: ENH-225 must leave --json byte-for-byte unchanged.\n"+
			"--- want (%d bytes) ---\n%s\n--- got (%d bytes) ---\n%s",
			name, len(want), string(want), len(got), string(got))
	}
}

// TestENH225_StatusJSONGolden pins gtms status --json byte-for-byte.
func TestENH225_StatusJSONGolden(t *testing.T) {
	root := setupENH225DriftFixture(t)
	scope, err := buildScopeFromArg(root, "drift-labels", false)
	require.NoError(t, err)

	var buf bytes.Buffer
	require.NoError(t, runStatusOverview(&buf, root, scope, true, "", false, false, "", nil))
	assertGoldenJSON(t, "status.json", buf.Bytes())
}

// TestENH225_GapsJSONGolden pins gtms gaps --json byte-for-byte.
func TestENH225_GapsJSONGolden(t *testing.T) {
	root := setupENH225DriftFixture(t)
	scope, err := buildScopeFromArg(root, "drift-labels", false)
	require.NoError(t, err)

	var buf bytes.Buffer
	require.NoError(t, runGaps(&buf, root, scope, true, "", false))
	assertGoldenJSON(t, "gaps.json", buf.Bytes())
}

// TestENH225_MapJSONGolden pins gtms map --json byte-for-byte.
func TestENH225_MapJSONGolden(t *testing.T) {
	root := setupENH225DriftFixture(t)
	scope, err := buildScopeFromArg(root, "drift-labels", false)
	require.NoError(t, err)

	var buf bytes.Buffer
	require.NoError(t, runMap(&buf, root, scope, false, true, "", false))
	assertGoldenJSON(t, "map.json", buf.Bytes())
}

// TestENH225_StatusDetailDriftWording verifies the human gtms status <tc> detail
// line names the actual drift kind (ENH-225 acceptance criterion 3) and never
// prints the retired "script modified since last run".
func TestENH225_StatusDetailDriftWording(t *testing.T) {
	root := setupENH225DriftFixture(t)

	detail := func(tc string) string {
		var buf bytes.Buffer
		require.NoError(t, runStatusDetail(&buf, statusTestSnap(t, root), root, tc, false, "bats", false, false, "", nil))
		return buf.String()
	}

	specOut := detail("tc-11111111")
	assert.Contains(t, specOut, "⚠")
	assert.Contains(t, specOut, "-- test case spec changed")
	assert.NotContains(t, specOut, "automation script")

	scriptOut := detail("tc-22222222")
	assert.Contains(t, scriptOut, "⚠")
	assert.Contains(t, scriptOut, "-- automation script changed")
	assert.NotContains(t, scriptOut, "test case spec")

	bothOut := detail("tc-33333333")
	assert.Contains(t, bothOut, "⚠")
	assert.Contains(t, bothOut, "-- test case spec and automation script changed")

	noneOut := detail("tc-44444444")
	assert.NotContains(t, noneOut, "-- test case spec changed")
	assert.NotContains(t, noneOut, "-- automation script changed")

	for _, out := range []string{specOut, scriptOut, bothOut, noneOut} {
		assert.NotContains(t, strings.ToLower(out), "script modified since last run")
	}
}
