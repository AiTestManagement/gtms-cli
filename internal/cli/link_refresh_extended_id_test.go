package cli

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BUG-188 Gap 2 (tc-31d05fff step 3): a single-TC refresh of the SHORTER id, with
// the extended sibling's wiring present in the same folder, exits 0 and refreshes
// only its own record -- it no longer mis-attributes tc-x--legacy--bats to tc-x
// and reports a spurious identity mismatch.
func TestLinkRefresh_SingleShortID_IgnoresExtendedSibling(t *testing.T) {
	skipIfShort(t)
	root := setupCompatProject(t)
	restore := withExecuteGlobals(t, root, batsLinkConfig())
	defer restore()

	shortWiring := seedStaleLinkedTC(t, root, "checkout", "tc-77777777")
	// The extended sibling in the same folder, wired (its drift state is irrelevant).
	_ = seedStaleLinkedTC(t, root, "checkout", "tc-77777777--legacy")

	before, err := os.ReadFile(shortWiring)
	require.NoError(t, err)

	out, err := runCmdCaptureStdout(t, newLinkCmd(), "tc-77777777", "--refresh")
	require.NoError(t, err, "single-TC refresh of the shorter id must exit 0 despite the extended sibling")
	assert.NotContains(t, out, "identity mismatch", "no spurious mismatch from the sibling")

	after, err := os.ReadFile(shortWiring)
	require.NoError(t, err)
	assert.NotEqual(t, string(before), string(after), "the shorter id's stale wiring was actually refreshed")
}
