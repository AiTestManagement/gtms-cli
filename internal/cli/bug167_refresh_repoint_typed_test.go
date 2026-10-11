package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/config"
)

// REV-118 CODEX-001: link --refresh and repoint now consume the typed
// target.Result and dispatch by Result.Kind. A trailing-separator folder target
// (tc-abc12345/) is KindFolder and must route to the FOLDER handler, so it can
// never re-derive into a single-TC operation on the GLOBAL flat wiring. These
// tests seed a REAL global TC (under foo/) with REAL, deliberately-STALE wiring
// and snapshot the wiring bytes: a folder-routed no-op leaves them byte-identical,
// whereas the pre-fix single-TC path would rewrite (refresh) or re-adapter
// (repoint) them.

// twoAdapterLinkConfig has two bats execute adapters so repoint has a distinct
// --adapter target (new-runner) to move wiring onto.
func twoAdapterLinkConfig() *config.Config {
	return &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"bats-runner": {Framework: "bats", Mode: "sync", Command: "echo PASS"},
				"new-runner":  {Framework: "bats", Mode: "sync", Command: "echo PASS2"},
			},
		},
		Defaults: map[string]string{"execute": "bats-runner"},
	}
}

const globalWiringFile = "gtms/automation/wiring/tc-abc12345--bats.wiring.yaml"

// seedStaleGlobalWiring creates flat wiring for tc-abc12345 (spec lives under
// foo/), then drifts the artefact so the record is STALE -- a single-TC refresh
// would rewrite its hashes, a folder no-op would not.
func seedStaleGlobalWiring(t *testing.T, root string) {
	t.Helper()
	artefact := filepath.Join(root, "test/acceptance/foo/real-test.bats")
	_, err := runCmdCaptureStderr(t, newLinkCmd(), "tc-abc12345", "--framework", "bats", "--artefact", artefact)
	require.NoError(t, err, "seeding the global wiring must succeed")
	require.FileExists(t, filepath.Join(root, globalWiringFile))

	f, err := os.OpenFile(artefact, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, werr := f.WriteString("\n# artefact drift -> wiring is now stale\n")
	require.NoError(t, werr)
	require.NoError(t, f.Close())
}

func readGlobalWiring(t *testing.T, root string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, globalWiringFile))
	require.NoError(t, err)
	return b
}

// TestTrailingSeparator_RefreshRepoint_NoGlobalWiringMutation is the core
// data-safety gate: the four folder-shaped invocations must not touch the global
// tc-abc12345 wiring.
func TestTrailingSeparator_RefreshRepoint_NoGlobalWiringMutation(t *testing.T) {
	skipIfShort(t)

	cases := []struct {
		name string
		args []string
	}{
		{"refresh slash", []string{"tc-abc12345/", "--refresh"}},
		{"refresh backslash", []string{`tc-abc12345\`, "--refresh"}},
		{"repoint slash", []string{"tc-abc12345/", "--adapter", "new-runner"}},
		{"repoint backslash", []string{`tc-abc12345\`, "--adapter", "new-runner"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := setupScopedProject(t)
			restore := withExecuteGlobals(t, root, twoAdapterLinkConfig())
			defer restore()
			seedStaleGlobalWiring(t, root)

			before := readGlobalWiring(t, root)
			out, _ := runCmdCaptureStdout(t, newLinkCmd(), tc.args...)
			after := readGlobalWiring(t, root)

			// The global flat wiring must be byte-identical: no rewrite (refresh)
			// and no adapter move (repoint).
			assert.Equal(t, string(before), string(after),
				"%s must not mutate the global tc-abc12345 wiring bytes", tc.name)
			// The adapter must remain bats-runner (never new-runner).
			assert.Contains(t, string(after), "bats-runner")
			assert.NotContains(t, string(after), "new-runner",
				"%s must not repoint the global wiring to new-runner", tc.name)
			// No single-TC success reported for the global id.
			assert.NotContains(t, out, "Refreshed: tc-abc12345")
			assert.NotContains(t, out, "Already current: tc-abc12345")
			// No stray new wiring record, and no pipeline mutation.
			all, _ := filepath.Glob(filepath.Join(root, "gtms/automation/wiring", "*.wiring.yaml"))
			assert.Len(t, all, 1, "%s must not create a new wiring record", tc.name)
			assertNoPipelineMutation(t, root)
		})
	}
}

// TestTrailingSeparator_RefreshRepoint_PositiveControls proves the single-TC path
// still works for a BARE id (no separator): stale wiring refreshes, and a bare
// repoint moves the adapter. Without these, a fix that simply broke both paths
// would still pass the no-mutation gate above.
func TestTrailingSeparator_RefreshRepoint_PositiveControls(t *testing.T) {
	skipIfShort(t)

	t.Run("bare id refresh rewrites stale wiring", func(t *testing.T) {
		root := setupScopedProject(t)
		restore := withExecuteGlobals(t, root, twoAdapterLinkConfig())
		defer restore()
		seedStaleGlobalWiring(t, root)

		before := readGlobalWiring(t, root)
		_, err := runCmdCaptureStdout(t, newLinkCmd(), "tc-abc12345", "--refresh")
		require.NoError(t, err)
		after := readGlobalWiring(t, root)
		assert.NotEqual(t, string(before), string(after),
			"a bare-id refresh of stale wiring must rewrite it (single-TC path intact)")
	})

	t.Run("bare id repoint moves the adapter", func(t *testing.T) {
		root := setupScopedProject(t)
		restore := withExecuteGlobals(t, root, twoAdapterLinkConfig())
		defer restore()
		seedStaleGlobalWiring(t, root)

		_, err := runCmdCaptureStdout(t, newLinkCmd(), "tc-abc12345", "--adapter", "new-runner", "--from-adapter", "bats-runner")
		require.NoError(t, err)
		after := readGlobalWiring(t, root)
		assert.Contains(t, string(after), "new-runner",
			"a bare-id repoint must move the adapter (single-TC path intact)")
	})
}

// TestRefreshRepoint_ExistingTCShapedFolder_RoutesToFolder confirms an EXISTING
// tc-shaped folder with a trailing separator routes to the folder handler (its
// member TC's wiring is refreshed), not to a single-TC operation on a same-named
// global id.
func TestRefreshRepoint_ExistingTCShapedFolder_RoutesToFolder(t *testing.T) {
	skipIfShort(t)
	root := setupScopedProject(t)
	restore := withExecuteGlobals(t, root, twoAdapterLinkConfig())
	defer restore()

	// A real folder named "tc-shaped11" holding a member TC with stale wiring.
	memberSpec := filepath.Join(root, "gtms/test/cases/tc-shaped11/tc-99999999-member.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(memberSpec), 0o755))
	require.NoError(t, os.WriteFile(memberSpec, []byte("---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-99999999\ntitle: member\n---\n"), 0o644))
	memberArt := filepath.Join(root, "test/acceptance/tc-shaped11/member.bats")
	require.NoError(t, os.MkdirAll(filepath.Dir(memberArt), 0o755))
	require.NoError(t, os.WriteFile(memberArt, []byte("@test \"m\" { true; }\n"), 0o644))
	_, err := runCmdCaptureStderr(t, newLinkCmd(), "tc-shaped11/tc-99999999", "--framework", "bats", "--artefact", memberArt, "--strict")
	require.NoError(t, err)
	// drift the member artefact -> its wiring is stale
	f, _ := os.OpenFile(memberArt, os.O_APPEND|os.O_WRONLY, 0o644)
	_, _ = f.WriteString("\n# drift\n")
	_ = f.Close()

	memberWiring := filepath.Join(root, "gtms/automation/wiring/tc-99999999--bats.wiring.yaml")
	before, err := os.ReadFile(memberWiring)
	require.NoError(t, err)

	// "tc-shaped11/" -> KindFolder -> folder refresh reaches the member TC.
	_, _ = runCmdCaptureStdout(t, newLinkCmd(), "tc-shaped11/", "--refresh")

	after, err := os.ReadFile(memberWiring)
	require.NoError(t, err)
	assert.NotEqual(t, string(before), string(after),
		"an existing tc-shaped folder + trailing separator must route to the folder handler and refresh its member")
}

// TestRepoint_TrailingSeparatorFolder_RDoesNotRejectAsSingleTC confirms -r
// validation keys on Result.Kind: a folder-shaped target (even absent) is folder
// intent, so -r is NOT rejected as "single test case"; a bare TC still is.
func TestRepoint_RValidationUsesKind(t *testing.T) {
	skipIfShort(t)

	t.Run("bare TC + -r rejected as single test case", func(t *testing.T) {
		root := setupScopedProject(t)
		restore := withExecuteGlobals(t, root, twoAdapterLinkConfig())
		defer restore()
		out, err := runCmdCaptureStderr(t, newLinkCmd(),
			"tc-abc12345", "-r", "--adapter", "new-runner", "--from-adapter", "bats-runner")
		require.Error(t, err)
		assert.Contains(t, out, "only valid for a folder repoint")
	})

	t.Run("trailing-separator folder + -r not rejected as single test case", func(t *testing.T) {
		root := setupScopedProject(t)
		restore := withExecuteGlobals(t, root, twoAdapterLinkConfig())
		defer restore()
		out, _ := runCmdCaptureStderr(t, newLinkCmd(),
			"tc-abc12345/", "-r", "--adapter", "new-runner", "--from-adapter", "bats-runner")
		// KindFolder -> not the single-TC -r rejection (folder repoint of an empty
		// scope is a no-op, but the -r-single-TC guard must not fire).
		assert.NotContains(t, out, "only valid for a folder repoint")
	})
}

// TestRefreshRepoint_NoPostClassificationReDerivation is an architectural guard:
// the refresh/repoint orchestrators must dispatch by Result.Kind and must NOT
// re-derive the target kind via IsBulkFolder/isTestCaseID. This is a source-shape
// invariant (CLAUDE.md source-shape tests), enforced only over the two functions
// that were the REV-118 defect.
func TestRefreshRepoint_NoPostClassificationReDerivation(t *testing.T) {
	src, err := os.ReadFile("link.go")
	require.NoError(t, err)
	text := string(src)

	for _, fn := range []string{"func runRefresh(", "func runRepoint("} {
		start := strings.Index(text, fn)
		require.GreaterOrEqual(t, start, 0, "%s must exist", fn)
		// Slice to the next top-level func to bound the body.
		rest := text[start+len(fn):]
		end := strings.Index(rest, "\nfunc ")
		if end < 0 {
			end = len(rest)
		}
		body := rest[:end]
		assert.NotContains(t, body, "IsBulkFolder(",
			"%s must not re-derive kind via IsBulkFolder -- dispatch by Result.Kind", fn)
		assert.NotContains(t, body, "isTestCaseID(",
			"%s must not re-derive kind via isTestCaseID -- dispatch by Result.Kind", fn)
	}
}
