package reader

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/result"
)

// AC35: reset and delete treat an unsafe staged-looking entry identically, in a real run
// and in a dry run: it is named, it is left in place, and the exit is non-zero.
func TestBUG209_ResetAndDeleteTreatUnsafeStagedEntriesTheSame(t *testing.T) {
	const line = ".tmp-fedcba9876543210: is not a regular file"
	assertRefused := func(t *testing.T, incomplete bool, refused []*result.RefusedHandoffError) {
		t.Helper()
		assert.True(t, incomplete, "the exit must be non-zero")
		require.Len(t, refused, 1)
		assert.Contains(t, refused[0].FirstLine(), line)
	}
	seed := func(t *testing.T) (string, string, string) {
		t.Helper()
		root, handoff, other := removalRaceProject(t)
		require.NoError(t, os.Remove(other))
		stray := handoff + ".tmp-fedcba9876543210"
		require.NoError(t, os.Mkdir(stray, 0o755))
		return root, handoff, stray
	}

	for _, dryRun := range []bool{true, false} {
		name := "real"
		if dryRun {
			name = "dry-run"
		}
		t.Run("reset/"+name, func(t *testing.T) {
			root, handoff, stray := seed(t)
			scan := result.OpenScan(root)
			defer scan.Close()
			res := &ResetResult{}
			_, err := clearAutomationRecords(scan, "tc-11111111", dryRun, res)
			require.NoError(t, err)
			assertRefused(t, res.Incomplete(), res.HandoffRefusals)
			assert.DirExists(t, stray)
			if dryRun {
				assert.FileExists(t, handoff)
			} else {
				assert.NoFileExists(t, handoff)
			}
		})
		t.Run("delete/"+name, func(t *testing.T) {
			root, handoff, stray := seed(t)
			scan := result.OpenScan(root)
			contracts, refused := scanResultContractsForTC(scan, "tc-11111111")
			scan.Close()
			require.Len(t, contracts, 1, "the unsafe entry is not planned for removal")
			plan := &deletePlan{projectRoot: root, resultContracts: contracts, handoffRefusals: refused}
			res, err := executeDeletePlan(plan, dryRun)
			require.NoError(t, err)
			assertRefused(t, res.Incomplete(), res.HandoffRefusals)
			assert.DirExists(t, stray)
			if dryRun {
				assert.FileExists(t, handoff)
			} else {
				assert.NoFileExists(t, handoff)
			}
		})
	}
}
