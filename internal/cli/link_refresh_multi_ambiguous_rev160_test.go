package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/output"
)

// REV-160 CLAUDE-006 -- relocated coverage.
//
// link.PartitionTCIDsByAmbiguity was the filename-counting ambiguity model
// ADR-024 replaces, and this branch orphaned it; it is removed. Most of its unit
// test asserted the string-counting mechanics of that superseded model, but two
// of its cases expressed identity outcomes that are still valid and had no other
// home once runFolderRefresh moved to the snapshot:
//
//   - several collisions in one scope are reported in SORTED order, so the
//     output does not depend on directory-walk order; and
//   - an id claimed by THREE specs is reported once, not twice.
//
// Both are re-expressed here through the snapshot model that actually decides it
// now: snap.SelectFolder picks the folder's owner ids and snap.RequireOwnership
// holds each ambiguous one back.
func TestLinkRefresh_MultipleAmbiguousIDs_ReportedOnceEachInSortedOrder(t *testing.T) {
	skipIfShort(t)
	root := setupCompatProject(t)
	restore := withExecuteGlobals(t, root, batsLinkConfig())
	defer restore()

	folder := "multi-dup"

	// An unambiguous, stale-but-valid neighbour: several collisions must not stop
	// the rest of the folder refreshing.
	neighbourWiring := seedStaleLinkedTC(t, root, folder, "tc-d0000001")
	neighbourBefore, err := os.ReadFile(neighbourWiring)
	require.NoError(t, err)

	specDir := filepath.Join(root, "gtms/test/cases", folder)
	writeDup := func(id, slug string) {
		require.NoError(t, os.WriteFile(filepath.Join(specDir, "tc-"+id[3:]+"-"+slug+".md"),
			[]byte("---\ntest_case_id: "+id+"\ntitle: "+slug+"\n---\n"), 0o644))
	}
	// tc-mmmmmmmm claimed twice; tc-zzzzzzzz claimed THREE times. Written in an
	// order that is not the sorted order, so a walk-order-dependent report would
	// come out z-before-m.
	writeDup("tc-zzzzzzzz", "z-one")
	writeDup("tc-zzzzzzzz", "z-two")
	writeDup("tc-zzzzzzzz", "z-three")
	writeDup("tc-mmmmmmmm", "m-one")
	writeDup("tc-mmmmmmmm", "m-two")

	out, err := runCmdCaptureStdout(t, newLinkCmd(), folder, "--refresh")
	require.Error(t, err, "ambiguous ids in scope must drive a non-zero exit")

	// Count the HELD-BACK LINES, not raw id occurrences: the diagnostic also
	// lists each claiming spec's path, and those paths legitimately contain the id.
	var heldLines []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), output.IconError+" tc-") {
			heldLines = append(heldLines, strings.TrimSpace(line))
		}
	}
	require.Len(t, heldLines, 2,
		"exactly two ids are ambiguous, so exactly two hold-back lines:\n%s", out)
	assert.True(t, strings.HasPrefix(heldLines[0], output.IconError+" tc-mmmmmmmm:"),
		"held-back ids must be reported in sorted order, not in directory-walk order:\n%s", out)
	assert.True(t, strings.HasPrefix(heldLines[1], output.IconError+" tc-zzzzzzzz:"),
		"a trebled id must be reported once, after the doubled one:\n%s", out)

	assert.Contains(t, out, "1 refreshed, 0 current, 2 failed",
		"exactly two ids are held back and the unambiguous neighbour still refreshes:\n%s", out)

	neighbourAfter, err := os.ReadFile(neighbourWiring)
	require.NoError(t, err)
	assert.NotEqual(t, string(neighbourBefore), string(neighbourAfter),
		"collisions must not stop the unambiguous neighbour from refreshing")
}
