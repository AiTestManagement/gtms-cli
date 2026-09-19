package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// F4 (structural): runSingleTCRefresh must not DISCARD the snapshot build error
// and skip the ownership guard. The error-to-permission-conversion shape is
// `NewSnapshot(...); <err> == nil { guard }` -- it acts only on success and drops
// the failure. Because downstream spec resolution also fails on the same error,
// the discarded-error defect is invisible to a behavioural test (both paths
// refuse), so this structural guard is what actually catches it. The fixed code
// binds the error and returns on it.
func TestSourceShape_SingleTCRefresh_DoesNotSwallowSnapshotError(t *testing.T) {
	src, err := os.ReadFile("link.go")
	require.NoError(t, err)
	code := string(src)

	start := strings.Index(code, "func runSingleTCRefresh(")
	require.GreaterOrEqual(t, start, 0, "runSingleTCRefresh must exist")
	body := code[start:]
	if next := strings.Index(body[1:], "\nfunc "); next >= 0 {
		body = body[:next+1]
	}

	require.Contains(t, body, "testcase.NewSnapshot(root)",
		"runSingleTCRefresh must build the snapshot for its ownership guard")

	// The swallow shape: NewSnapshot(...) inlined in an `; <name> == nil` guard.
	swallow := regexp.MustCompile(`NewSnapshot\([^)]*\);\s*\w+\s*==\s*nil`)
	assert.NotRegexp(t, swallow, body,
		"the snapshot build error must be surfaced (return on error), never discarded via `; serr == nil`")
}

// F4: single-TC link --refresh must NOT skip the ownership guard and rewrite
// wiring when the snapshot build fails (enumeration/containment/read error).
// Injecting a snapshot failure (the cases root is a non-directory, which
// NewSnapshot now errors on per F5) must make BOTH dry-run and real refresh refuse
// non-zero and leave the wiring byte-identical -- no error-to-permission
// conversion on a mutating consumer.
func TestLinkRefresh_SingleTC_SnapshotError_RefusesAndWritesNothing(t *testing.T) {
	skipIfShort(t)
	root := setupCompatProject(t)
	restore := withExecuteGlobals(t, root, batsLinkConfig())
	defer restore()

	// A valid wiring record that a broken guard would rewrite.
	wdir := filepath.Join(root, "gtms", "automation", "wiring")
	require.NoError(t, os.MkdirAll(wdir, 0o755))
	wpath := filepath.Join(wdir, "tc-aaaa1111--bats.wiring.yaml")
	body := "testcase: tc-aaaa1111\ntestcase-hash: 0011223344556677\nframework: bats\nadapter: bats-runner\nartefact: test/x.bats\nartefact-hash: aabbccddeeff0011\n"
	require.NoError(t, os.WriteFile(wpath, []byte(body), 0o644))
	before, err := os.ReadFile(wpath)
	require.NoError(t, err)

	// Inject the snapshot failure: replace the cases DIRECTORY with a FILE.
	cases := filepath.Join(root, "gtms", "test", "cases")
	require.NoError(t, os.RemoveAll(cases))
	require.NoError(t, os.WriteFile(cases, []byte("not a dir"), 0o644))

	for _, args := range [][]string{
		{"tc-aaaa1111", "--refresh", "--dry-run"},
		{"tc-aaaa1111", "--refresh"},
	} {
		out, rerr := runCmdCaptureStderr(t, newLinkCmd(), args...)
		require.Error(t, rerr, "refresh must refuse when the snapshot build fails: %v", args)

		// Discriminates the fix: F4 SURFACES the snapshot error (the cases root is
		// not a directory). The swallowed-error shape would skip the guard and fail
		// downstream instead, with a "spec not found" message that never names the
		// directory problem.
		assert.Contains(t, out, "not a directory",
			"the snapshot build error must be surfaced, not swallowed: %v", args)

		after, aerr := os.ReadFile(wpath)
		require.NoError(t, aerr)
		assert.Equal(t, string(before), string(after),
			"wiring must be byte-identical -- refresh wrote nothing: %v", args)
	}
}
