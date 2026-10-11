package adapter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// The claim set is OPERATION STATE: scanned once, threaded through every
// invocation the operation makes, and updated as the operation changes
// ownership. These tests pin both halves of that -- the read bound it exists to
// give, and the mutability it must not lose in exchange.
//
// The read bound is asserted as a SCALING property over two fixture sizes on
// each axis, never as a constant ceiling: every terminating program performs a
// bounded number of reads, so "fewer than N" is not a gate. Counts are the
// assertion and elapsed time is never used -- this repository spans a ~274x
// difference between /mnt/c and native ext4 for the same command.
//
// The count is taken through readWiringRecord, the one seam claim discovery
// reads records through, so it attributes reads to discovery specifically and
// not to the per-item single-record lookups (wiring.Find and friends) that are
// legitimately O(items).

// countWiringReads swaps in a counting readWiringRecord for the duration of a
// test and returns a pointer to the live count.
func countWiringReads(t *testing.T) *int {
	t.Helper()
	original := readWiringRecord
	count := 0
	readWiringRecord = func(path string) (*wiring.WiringRecord, error) {
		count++
		return original(path)
	}
	t.Cleanup(func() { readWiringRecord = original })
	return &count
}

// seedDecoyWiring writes n wiring records for identities that are not in any
// batch below. They are what a per-item rescan would re-read for every single
// test case, so they are the instrument: with one scan per operation the read
// count equals their number no matter how many items run.
func seedDecoyWiring(t *testing.T, root string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("tc-decoy%04d", i)
		_, err := wiring.Write(root, &wiring.WiringRecord{
			TestCase:     id,
			TestCaseHash: "1111111111111111",
			Framework:    "bats",
			Adapter:      "bats-runner",
			Artefact:     "test/acceptance/decoy/" + id + ".bats",
			ArtefactHash: "2222222222222222",
		})
		require.NoError(t, err)
	}
}

// seedBatchOwners writes n normally named specs and returns their proven owners,
// in a stable order.
func seedBatchOwners(t *testing.T, root string, n int) []*testcase.Owner {
	t.Helper()
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		ids[i] = fmt.Sprintf("tc-batch%04d", i)
		abs := filepath.Join(root, "gtms", "test", "cases", "feat", ids[i]+"-case.md")
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		body := "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: " + ids[i] + "\ntitle: Batch case\npriority: Medium\n---\n\n## Test Objective\n\nBatch.\n"
		require.NoError(t, os.WriteFile(abs, []byte(body), 0o644))
	}
	owners := make([]*testcase.Owner, n)
	for i, id := range ids {
		owner, err := testcase.ResolveOwner(root, id)
		require.NoError(t, err)
		owners[i] = owner
	}
	return owners
}

// runBatch mirrors the bulk automate loop in internal/cli/automate.go: ONE claim
// set built before the batch mutates anything, threaded into every invocation.
// It returns the set and the per-item errors, and it does not stop on failure --
// the CLI's policy is to count and continue.
func runBatch(t *testing.T, root string, cfg *config.Config, resolved *ResolvedAdapter, owners []*testcase.Owner, force bool) (*ArtefactClaimSet, []error) {
	t.Helper()
	claims, err := NewArtefactClaimSet(root)
	require.NoError(t, err, "the batch-wide scan must succeed before any item runs")

	errs := make([]error, len(owners))
	for i, owner := range owners {
		_, invokeErr := InvokeWithRoot(context.Background(), root, cfg, resolved, owner.ID(), CommandFlags{
			Owner:     owner,
			Claims:    claims,
			Framework: "bats",
			Force:     force,
		})
		errs[i] = invokeErr
	}
	return claims, errs
}

// (8) Wiring reads stay O(wiring records) for a whole operation, on both axes.
//
// Axis 1 -- items vary, wiring held fixed: the count must be IDENTICAL, because
// the operation scans once. This is the axis that catches O(items x wiring): a
// per-item rescan makes the count grow with the batch, and it is the shape the
// REV-159 regression had.
//
// Axis 2 -- wiring varies, items held fixed: the count must equal the number of
// records exactly, i.e. one scan and no more. Asserting equality rather than an
// upper bound is what makes "one scan" the claim, instead of "not too many".
func TestArtefactClaimSet_WiringReadsAreBoundedPerOperationNotPerItem(t *testing.T) {
	skipIfShort(t)

	measure := func(t *testing.T, items, decoys int) int {
		t.Helper()
		root, cfg, resolved := ownershipProject(t, "agent-automate", "")
		owners := seedBatchOwners(t, root, items)
		seedDecoyWiring(t, root, decoys)

		reads := countWiringReads(t)
		_, errs := runBatch(t, root, cfg, resolved, owners, false)
		for i, err := range errs {
			require.NoError(t, err, "item %d of the batch must succeed", i)
		}
		return *reads
	}

	t.Run("items vary, wiring fixed: the count does not move", func(t *testing.T) {
		const decoys = 12
		small := measure(t, 2, decoys)
		large := measure(t, 6, decoys)

		assert.Equal(t, decoys, small, "a 2-item batch must read each wiring record exactly once")
		assert.Equal(t, decoys, large, "a 6-item batch must read each wiring record exactly once")
		assert.Equal(t, small, large,
			"wiring reads scaled with the number of test cases: %d reads for 2 items vs %d for 6, "+
				"which is the O(test cases x wiring records) shape", small, large)
	})

	t.Run("wiring varies, items fixed: exactly one scan of each record", func(t *testing.T) {
		const items = 4
		few := measure(t, items, 10)
		many := measure(t, items, 25)

		assert.Equal(t, 10, few)
		assert.Equal(t, 25, many)
		assert.Less(t, many, items*25,
			"reading %d records %d times would be %d reads; one scan is %d", 25, items, items*25, many)
	})
}

// (9) The set is MUTABLE operation state. Two mechanisms keep a later item from
// selecting a path an earlier one took: the reservation held across the
// invocation, and the claim committed once wiring is written. Both are pinned,
// because a frozen index would pass neither and a bulk run would let two test
// cases write the same artefact.
func TestArtefactClaimSet_TracksOwnershipChangesDuringTheOperation(t *testing.T) {
	skipIfShort(t)

	// A reservation is visible to the next Reserve on the same path, and a
	// released one is not. This is the in-flight half, checked directly because a
	// sequential batch commits before the next item starts.
	t.Run("a reservation holds the path and a release frees it", func(t *testing.T) {
		root, _, _ := ownershipProject(t, "agent-automate", "")
		set, err := NewArtefactClaimSet(root)
		require.NoError(t, err)
		path := filepath.Join(root, "test", "acceptance", "feat", "tc-aaaa-x.bats")

		first, err := set.Reserve("tc-aaaa", "bats", path)
		require.NoError(t, err)
		require.NotNil(t, first)

		_, err = set.Reserve("tc-aaaa-x", "bats", path)
		require.Error(t, err, "a path reserved earlier in this operation is not available")
		var claimErr *ForeignArtefactClaimError
		require.True(t, errors.As(err, &claimErr))
		assert.Equal(t, "tc-aaaa", claimErr.Claimant)
		assert.True(t, claimErr.InFlight, "the holder took it during this run, not in a pre-existing record")
		assert.Contains(t, claimErr.Error(), "was already claimed earlier in this run by")

		first.Release()
		second, err := set.Reserve("tc-aaaa-x", "bats", path)
		require.NoError(t, err, "releasing a reservation must free the path again")
		require.NotNil(t, second)
	})

	// The batch half. Two DIFFERENT identities whose planned artefact paths
	// collide: tc-aaaa with the spec x.md plans "tc-aaaa" + "-x", and tc-aaaa-x
	// with the spec tc-aaaa-x.md plans "tc-aaaa-x" + no slug. Both are
	// test/acceptance/feat/tc-aaaa-x.bats. This is reachable, it is not a naming
	// bug (the grammar allows single dashes in an id), and only live operation
	// state catches it -- when the second item is checked, the first item's claim
	// exists nowhere but in memory and on disk, never in a snapshot taken before
	// the batch began.
	t.Run("a later item cannot take a path an earlier item claimed", func(t *testing.T) {
		root, cfg, resolved := ownershipProject(t, "agent-automate", "")
		writeOwnedSpec(t, root, "gtms/test/cases/feat/x.md", "tc-aaaa")
		writeOwnedSpec(t, root, "gtms/test/cases/feat/tc-aaaa-x.md", "tc-aaaa-x")
		first, err := testcase.ResolveOwner(root, "tc-aaaa")
		require.NoError(t, err)
		second, err := testcase.ResolveOwner(root, "tc-aaaa-x")
		require.NoError(t, err)

		contested := filepath.Join(root, "test", "acceptance", "feat", "tc-aaaa-x.bats")
		require.Equal(t, contested,
			filepath.Join(root, "test", "acceptance", "feat", DeriveArtefactBasename(first.SpecRel(), first.ID())+".bats"),
			"fixture check: both identities must plan the same path")

		obs := observeWrites(t)
		claims, errs := runBatch(t, root, cfg, resolved, []*testcase.Owner{first, second}, false)

		require.NoError(t, errs[0], "the first item owns the path and must succeed")

		// The damage is asserted before the missing refusal, so a frozen set
		// reports the COLLISION it permitted and not merely an absent error: two
		// writes to one path, the second by the identity that does not own it.
		assert.Equal(t, []string{contested}, obs.written,
			"the refused item must never have been asked to write")
		assert.Equal(t, []string{"tc-aaaa"}, obs.writtenBy,
			"only the identity that owns the path may write it")

		require.Error(t, errs[1], "the second item must be refused: the path is no longer free")
		var claimErr *ForeignArtefactClaimError
		require.True(t, errors.As(errs[1], &claimErr), "want *ForeignArtefactClaimError, got %#v", errs[1])
		assert.Equal(t, "tc-aaaa", claimErr.Claimant)

		// The committed claim is in the set, attributed to the identity that
		// actually wrote it -- that is what made the second check possible.
		held, _, found, err := claims.foreignHolder("tc-aaaa-x", contested)
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, "tc-aaaa", held)
	})

	// A failed item must not leave its path held. This follows the command's
	// existing partial-failure policy: the bulk CLI counts an item failed when
	// the invocation errors or the result status is "error", and those are the
	// same two conditions the invoker releases on.
	t.Run("a failed item releases its path", func(t *testing.T) {
		root, cfg, resolved := ownershipProject(t, "agent-automate", "")
		owner := writeOwnedSpec(t, root, "gtms/test/cases/feat/tc-cccccccc-checkout.md", "tc-cccccccc")

		obs := observeWrites(t)
		obs.writeErr = errors.New("skeleton generation failed")

		claims, err := NewArtefactClaimSet(root)
		require.NoError(t, err)
		planned := filepath.Join(root, "test", "acceptance", "feat", "tc-cccccccc-checkout.bats")

		_, invokeErr := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-cccccccc", CommandFlags{
			Owner: owner, Claims: claims, Framework: "bats",
		})
		require.Error(t, invokeErr)

		_, _, found, err := claims.foreignHolder("tc-dddddddd", planned)
		require.NoError(t, err)
		assert.False(t, found, "a failed item must not leave the path held against the next one")
	})
}
