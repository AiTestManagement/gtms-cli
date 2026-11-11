package cli

// REV-232 regression coverage for the ENH-231 automatic script-drift
// acceptance path:
//
//   - CODEX-001: a wiring file whose body declares another test case or
//     framework is never used to choose the acceptance write target.
//   - CODEX-002: the invocation contract carries the exact hash acceptance
//     wrote, even when the script changes between preflight and acceptance.
//   - CODEX-003: bulk acceptance prints the automatic-update notice.
//   - CLAUDE-001: a spec that cannot be verified is never "spec current", so a
//     script change next to it is never accepted.
//   - DCODEX-002: if the script changes again after acceptance, the run is
//     refused without invocation and without a second wiring update.
//   - DCLAUDE-001: an unverified spec next to a script change is refused as
//     "spec unavailable" in BOTH policies (ENH-222 Draft K; the 2026-10-27
//     owner decision superseded the old refuse-mode stale-wiring wording). No
//     refresh hint is offered -- a refresh cannot fix a missing/unreadable spec.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// setExecuteTestHook installs fn as the execute test seam for this test only.
func setExecuteTestHook(t *testing.T, fn func(stage string)) {
	t.Helper()
	executeTestHook = fn
	t.Cleanup(func() { executeTestHook = nil })
}

func runBulkWithFramework(t *testing.T, root, folder, framework string) (string, error) {
	t.Helper()
	restore := withExecuteGlobals(t, root, enh231Config(t, ""))
	defer restore()
	cmd := &cobra.Command{Use: "execute"}
	cmd.SetContext(context.Background())
	var err error
	out := captureStderr(t, func() {
		err = runBulkExecute(cmd, root, folder, "", "", "", framework, false, false, false, false)
	})
	return out, err
}

// handoffArtefactHashes returns the artefact-hash stamped into every result
// contract under .gtms/results/.
func handoffArtefactHashes(t *testing.T, root string) []string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(root, ".gtms", "results", "*.handoff.yaml"))
	var out []string
	for _, p := range m {
		b, err := os.ReadFile(p)
		require.NoError(t, err)
		var doc map[string]interface{}
		require.NoError(t, yaml.Unmarshal(b, &doc))
		h, _ := doc["artefact-hash"].(string)
		out = append(out, h)
	}
	return out
}

// --- CODEX-001: identity mismatch ---

func TestREV232_IdentityMismatchNeverAccepted(t *testing.T) {
	cases := []struct {
		name      string
		bulk      bool
		framework string // --framework; "" = no framework
		bodyTC    string
		bodyFW    string
		decoy     bool // also write the same bytes at the body-derived path
	}{
		{name: "single --framework, test case mismatch", framework: "bats", bodyTC: "tc-22222222", bodyFW: "bats", decoy: true},
		{name: "single --framework, framework mismatch", framework: "bats", bodyTC: enh231TC, bodyFW: "playwright", decoy: true},
		{name: "single no framework, test case mismatch", bodyTC: "tc-22222222", bodyFW: "bats", decoy: true},
		{name: "single no framework, framework mismatch", bodyTC: enh231TC, bodyFW: "playwright"},
		{name: "bulk --framework, test case mismatch", bulk: true, framework: "bats", bodyTC: "tc-22222222", bodyFW: "bats", decoy: true},
		{name: "bulk --framework, framework mismatch", bulk: true, framework: "bats", bodyTC: enh231TC, bodyFW: "playwright", decoy: true},
		{name: "bulk no framework, test case mismatch", bulk: true, bodyTC: "tc-22222222", bodyFW: "bats", decoy: true},
		{name: "bulk no framework, framework mismatch", bulk: true, bodyTC: enh231TC, bodyFW: "playwright"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			skipIfShort(t)
			root := setupCompatProject(t)
			enh231Spec(t, root, "drift", enh231TC)
			specHash, err := pipeline.HashFile(filepath.Join(root, "gtms/test/cases/drift", enh231TC+"-x.md"))
			require.NoError(t, err)
			artefact := enh231ArtefactPath(root)
			require.NoError(t, os.MkdirAll(filepath.Dir(artefact), 0755))
			require.NoError(t, os.WriteFile(artefact, []byte("# fixture artefact\n"), 0644))
			oldArt, err := pipeline.HashFile(artefact)
			require.NoError(t, err)

			// Spec current, script hash old: a script-only change once edited.
			raw, err := yaml.Marshal(&wiring.WiringRecord{
				TestCase: tc.bodyTC, TestCaseHash: specHash, Framework: tc.bodyFW,
				Adapter: "bats-runner", Artefact: "test/acceptance/" + enh231TC + ".bats", ArtefactHash: oldArt,
			})
			require.NoError(t, err)
			selectedPath, err := wiring.Path(root, enh231TC, "bats")
			require.NoError(t, err)
			require.NoError(t, os.MkdirAll(filepath.Dir(selectedPath), 0755))
			require.NoError(t, os.WriteFile(selectedPath, raw, 0644))
			bodyPath, err := wiring.Path(root, tc.bodyTC, tc.bodyFW)
			require.NoError(t, err)
			if tc.decoy {
				require.NoError(t, os.WriteFile(bodyPath, raw, 0644))
			}

			enh231EditScript(t, root)

			var out string
			if tc.bulk {
				out, _ = runBulkWithFramework(t, root, "drift", tc.framework)
			} else {
				args := []string{enh231TC}
				if tc.framework != "" {
					args = append(args, "--framework", tc.framework)
				}
				out, err = runSingle(t, root, enh231Config(t, ""), args...)
				require.Error(t, err, "single path must refuse: %s", out)
			}

			assert.NotContains(t, out, "wiring updated automatically")
			got, err := os.ReadFile(selectedPath)
			require.NoError(t, err)
			assert.Equal(t, string(raw), string(got), "selected wiring must be byte-for-byte unchanged")
			if tc.decoy {
				got, err = os.ReadFile(bodyPath)
				require.NoError(t, err)
				assert.Equal(t, string(raw), string(got), "body-derived wiring must be byte-for-byte unchanged")
			} else {
				_, statErr := os.Stat(bodyPath)
				assert.True(t, os.IsNotExist(statErr), "no wiring may be created at the body-derived path")
			}
			assert.Equal(t, 0, countHandoffs(t, root), "no adapter invocation")
			assert.Equal(t, 0, countExecuteTasks(t, root), "no execute task")
		})
	}
}

// --- CODEX-002 / CODEX-003: accepted hash and bulk notice ---

// The script changes between the drift preflight and acceptance. The wiring
// and the invocation's result contract must both carry the one hash
// acceptance wrote, not the preflight hash (the single path's old race).
// A further change after acceptance is DCODEX-002, covered below.
func TestREV232_ConcurrentEditWiringAndContractShareAcceptedHash(t *testing.T) {
	for _, bulk := range []bool{false, true} {
		name := map[bool]string{false: "single", true: "bulk"}[bulk]
		t.Run(name, func(t *testing.T) {
			skipIfShort(t)
			root := enh231SingleProject(t, "")
			preflightArt := enh231EditScript(t, root)
			artefact := enh231ArtefactPath(root)
			acceptedContent := []byte("# fixture artefact\n# edited again before acceptance\n")
			setExecuteTestHook(t, func(stage string) {
				if stage == "pre-accept" {
					_ = os.WriteFile(artefact, acceptedContent, 0644)
				}
			})

			var out string
			var err error
			if bulk {
				out, err = runBulk(t, root, "drift", enh231Config(t, ""), false)
			} else {
				out, err = runSingle(t, root, enh231Config(t, ""), enh231TC)
			}
			require.NoError(t, err, out)

			acceptedArt := pipeline.HashBytes(acceptedContent)
			require.NotEqual(t, preflightArt, acceptedArt, "the pre-accept hook must have changed the script")
			assert.Equal(t, acceptedArt, wiringArtefactHash(t, root, enh231TC), "wiring records the accepted hash")
			assert.Equal(t, []string{acceptedArt}, handoffArtefactHashes(t, root),
				"the invocation contract must carry the same accepted hash as the wiring")
			// ENH-222 Draft D / D2: the acceptance notice is printed after the
			// test is proven to have started, and says it ran.
			assert.Contains(t, out, "GTMS accepted the change and ran",
				"the %s path must print the automatic-acceptance notice", name)
			assert.NotContains(t, out, "wiring updated automatically",
				"ENH-222 retired the old notice wording")
		})
	}
}

// CODEX-003: a plain bulk acceptance prints the notice once per accepted test case.
func TestREV232_BulkAcceptancePrintsNotice(t *testing.T) {
	skipIfShort(t)
	root := t.TempDir()
	enh231Spec(t, root, "drift", enh231TC)
	seedLegacyRecord(t, root, legacyRecord{TC: enh231TC, Framework: "bats", Adapter: "bats-runner"})
	enh231EditScript(t, root)

	out, err := runBulk(t, root, "drift", enh231Config(t, ""), false)
	require.NoError(t, err, out)
	// ENH-222 Draft D2: grouped automatic acceptance names the count of started
	// test cases; the retired "wiring updated automatically" wording is gone.
	assert.Contains(t, out, "GTMS accepted the change and ran 1 test case.")
	assert.NotContains(t, out, "wiring updated automatically")
}

// --- CLAUDE-001: spec unverified ---

func TestREV232_CheckWiringDrift_SpecUnverifiedIsNotScriptOnly(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(t *testing.T, owner *testcase.Owner) *testcase.Owner
	}{
		{name: "empty spec path", prepare: func(t *testing.T, _ *testcase.Owner) *testcase.Owner { return nil }},
		{name: "missing spec", prepare: func(t *testing.T, o *testcase.Owner) *testcase.Owner {
			require.NoError(t, os.Remove(o.SpecAbs()))
			return o
		}},
		{name: "unreadable spec", prepare: func(t *testing.T, o *testcase.Owner) *testcase.Owner {
			makeUnreadable(t, o.SpecAbs())
			return o
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			enh231Spec(t, root, "drift", enh231TC)
			seedLegacyRecord(t, root, legacyRecord{TC: enh231TC, Framework: "bats", Adapter: "bats-runner"})
			owner, err := testcase.ResolveOwner(root, enh231TC)
			require.NoError(t, err)
			rec, _, err := wiring.Find(root, enh231TC, "bats")
			require.NoError(t, err)
			owner = tc.prepare(t, owner)

			// Script current: unverified spec alone keeps today's behaviour.
			dr := checkWiringDrift(root, rec, rec.Artefact, owner)
			assert.Empty(t, dr.diag)
			assert.True(t, dr.specUnverified)

			// Script changed: never script-only, always a refusal diagnostic.
			enh231EditScript(t, root)
			dr = checkWiringDrift(root, rec, rec.Artefact, owner)
			assert.True(t, dr.specUnverified, "an unverifiable spec must be reported, not treated as current")
			assert.True(t, dr.artefactStale)
			assert.False(t, dr.specStale, "a spec-hash failure is not spec drift either")
			assert.False(t, dr.scriptOnly(), "an unverified spec must exclude script-only acceptance")
			assert.True(t, dr.unverifiedScriptChange())
			assert.NotEmpty(t, dr.diag, "a script change next to an unverified spec is never current")
			assert.NotEmpty(t, dr.specProblem)
			// DCLAUDE-001: the diagnostic itself is policy-neutral.
			assert.NotContains(t, dr.diag, "not accepted")
		})
	}
}

// The spec disappears or becomes unreadable after execute resolved it (raced
// away before the drift preflight). The single path refuses, the bulk path
// does not dispatch, and the wiring is unchanged in both.
func TestREV232_SpecUnverifiedRefusesScriptAcceptance(t *testing.T) {
	type spoil struct {
		name  string
		apply func(t *testing.T, spec string)
	}
	spoils := []spoil{
		{name: "spec raced away", apply: func(t *testing.T, spec string) { _ = os.Remove(spec) }},
		{name: "spec unreadable", apply: func(t *testing.T, spec string) { makeUnreadable(t, spec) }},
	}
	for _, sp := range spoils {
		for _, bulk := range []bool{false, true} {
			sp, bulk := sp, bulk
			t.Run(sp.name+map[bool]string{false: " (single)", true: " (bulk)"}[bulk], func(t *testing.T) {
				skipIfShort(t)
				root := enh231SingleProject(t, "")
				spec := filepath.Join(root, "gtms/test/cases/drift", enh231TC+"-x.md")
				if sp.name == "spec unreadable" {
					// Probe first: makeUnreadable may skip, which must not
					// happen inside the hook while stderr is captured.
					probe := filepath.Join(t.TempDir(), "probe.md")
					require.NoError(t, os.WriteFile(probe, []byte("x"), 0644))
					makeUnreadable(t, probe)
				}
				const otherTC = "tc-22222222"
				if bulk {
					// REV-236 CLAUDE-002: a healthy second test case, which must
					// still be dispatched next to the spec-unverified refusal.
					enh231Spec(t, root, "drift", otherTC)
					seedLegacyRecord(t, root, legacyRecord{TC: otherTC, Framework: "bats", Adapter: "bats-runner"})
				}
				wiringBefore := wiringBytes(t, root, enh231TC)
				enh231EditScript(t, root)
				spoiled := false
				setExecuteTestHook(t, func(stage string) {
					if stage == "pre-drift" && !spoiled {
						spoiled = true
						sp.apply(t, spec)
					}
				})

				var out string
				var err error
				if bulk {
					out, err = runBulk(t, root, "drift", enh231Config(t, ""), false)
					// BUG-200: spec unverified is a not-run refusal (non-zero exit).
					// ENH-222 Draft K: the plain-language reason is "spec unavailable".
					require.Error(t, err, "a bulk spec-unverified refusal must exit non-zero: %s", out)
					assert.Contains(t, out, "not run (spec unavailable)")
					assert.Regexp(t, `tc-22222222\s+pass`, out, "the other test case must run and pass")
					assert.Equal(t, 1, countHandoffs(t, root), "the other test case must still be dispatched")
					assert.Equal(t, 1, countExecuteTasks(t, root), "only the other test case gets an execute task")
					assert.NotContains(t, out, "wiring updated automatically")
					assert.Equal(t, string(wiringBefore), string(wiringBytes(t, root, enh231TC)), "wiring must be byte-for-byte unchanged")
					return
				} else {
					out, err = runSingle(t, root, enh231Config(t, ""), enh231TC)
					require.Error(t, err, "single path must refuse: %s", out)
					// ENH-222 Draft K: the script changed but GTMS could not check the spec.
					assert.Contains(t, out, "GTMS could not check its test case spec")
				}
				assert.NotContains(t, out, "wiring updated automatically")
				enh231AssertNoMutation(t, root, wiringBefore, 0, 0)
			})
		}
	}
}

// --- DCODEX-002: script changed again after acceptance ---

// The script changes again right after acceptance (post-accept). The run is
// refused without invocation; the wiring keeps the accepted hash and is not
// updated a second time; the user is told to retry.
func TestREV232_ScriptChangedAgainAfterAcceptanceRefused(t *testing.T) {
	for _, bulk := range []bool{false, true} {
		name := map[bool]string{false: "single", true: "bulk"}[bulk]
		t.Run(name, func(t *testing.T) {
			skipIfShort(t)
			root := enh231SingleProject(t, "")
			acceptedArt := enh231EditScript(t, root)
			artefact := enh231ArtefactPath(root)
			againContent := []byte("# fixture artefact\n# edited after acceptance\n")
			setExecuteTestHook(t, func(stage string) {
				if stage == "post-accept" {
					_ = os.WriteFile(artefact, againContent, 0644)
				}
			})

			var out string
			var err error
			if bulk {
				out, err = runBulk(t, root, "drift", enh231Config(t, ""), false)
			} else {
				out, err = runSingle(t, root, enh231Config(t, ""), enh231TC)
			}
			require.Error(t, err, "a script changed again after acceptance must refuse: %s", out)
			assert.Contains(t, out, "changed again")
			assert.Contains(t, strings.ToLower(out), "retry")
			assert.Equal(t, acceptedArt, wiringArtefactHash(t, root, enh231TC),
				"the wiring keeps the accepted hash; it is not updated again")
			assert.NotEqual(t, pipeline.HashBytes(againContent), wiringArtefactHash(t, root, enh231TC))
			assert.Equal(t, 0, countHandoffs(t, root), "no adapter invocation")
			assert.Equal(t, 0, countExecuteTasks(t, root), "no execute task")
		})
	}
}

// --- DCLAUDE-001: spec unverified depends on the policy ---

// Accept mode: the single path refuses as spec unverified and points at the
// spec, not at 'gtms link --refresh'. Refuse mode: both paths keep the
// existing stale-wiring classification and remediation.
func TestREV232_SpecUnverifiedClassificationFollowsPolicy(t *testing.T) {
	cases := []struct {
		policy string
		bulk   bool
	}{
		{policy: "", bulk: false},
		{policy: "accept", bulk: false},
		{policy: "refuse", bulk: false},
		{policy: "refuse", bulk: true},
	}
	for _, tc := range cases {
		label := tc.policy
		if label == "" {
			label = "default"
		}
		t.Run(label+map[bool]string{false: " (single)", true: " (bulk)"}[tc.bulk], func(t *testing.T) {
			skipIfShort(t)
			root := enh231SingleProject(t, "")
			spec := filepath.Join(root, "gtms/test/cases/drift", enh231TC+"-x.md")
			wiringBefore := wiringBytes(t, root, enh231TC)
			enh231EditScript(t, root)
			setExecuteTestHook(t, func(stage string) {
				if stage == "pre-drift" {
					_ = os.Remove(spec)
				}
			})

			cfg := enh231Config(t, tc.policy)
			var out string
			var err error
			if tc.bulk {
				out, _ = runBulk(t, root, "drift", cfg, false)
			} else {
				out, err = runSingle(t, root, cfg, enh231TC)
				require.Error(t, err, "single path must refuse: %s", out)
			}

			// ENH-222 (2026-10-27 owner decision): BOTH policies refuse as
			// "spec unavailable" (Draft K). The old refuse-mode stale-wiring
			// wording and its refresh hint are superseded. No refresh hint is
			// offered in either policy -- a refresh cannot fix a missing spec.
			assert.NotContains(t, out, "not accepted", "refuse mode never accepts, so it must not say so")
			assert.NotContains(t, out, "spec unverified", "the internal token is retired")
			assert.Contains(t, out, "GTMS could not check its test case spec")
			assert.Contains(t, out, "Restore the file or make it readable")
			assert.NotContains(t, out, "gtms link --refresh", "a refresh cannot fix a missing spec, in either policy")
			if tc.bulk {
				assert.Contains(t, out, "not run (spec unavailable)")
			}
			assert.NotContains(t, out, "wiring updated automatically")
			enh231AssertNoMutation(t, root, wiringBefore, 0, 0)
		})
	}
}
