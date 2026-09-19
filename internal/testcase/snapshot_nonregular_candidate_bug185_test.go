package testcase

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// BUG-185: "Out-of-tree links and non-regular candidates are rejected before
// they are opened."
//
// The out-of-tree half is covered elsewhere (testcase_symlink_test.go,
// adr024_boundary_guard_test.go). This file covers the NON-REGULAR half:
// snapshot.go:213-216 stats the canonically resolved target of every walked
// .md entry and drops anything that is not a regular file, before
// parseBodyIdentity / hasIdentityCarrier ever open it.
//
// The rows below plant one odd shape each and record WHICH check rejects it,
// because only two of the four reach that guard. A FIFO and a symlink to a
// directory do (they resolve to a real, contained, non-regular target). A
// dangling symlink is stopped one step earlier by ResolveUnderRoot, and a real
// directory never reaches classifyCandidate at all. Each row's rejectedBy field
// records what was verified, not what was assumed.
//
// What the code genuinely does with a non-regular entry, established by reading
// snapshot.go and confirmed by these fixtures: the entry becomes NEITHER an
// owner NOR an Unprovable candidate. classifyCandidate returns ok=false at
// snapshot.go:215, so buildSnapshot (snapshot.go:186-189) never appends a
// Candidate at all. The entry is therefore invisible to Resolve (NotFound, not
// Unprovable) and to SelectFolder (absent from both return values), and the
// surrounding folder-scoped mutation guard RequireFolderOwnership proceeds over
// the real specs in the folder instead of refusing the whole folder.
//
// Each row also asserts the snapshot COMPLETES. That is the point of rejecting
// "before they are opened": a FIFO with no writer blocks forever on open, so a
// guard that admitted it would not merely misclassify, it would wedge every
// command that builds a snapshot.

// nonRegularRow is one non-regular shape planted inside a cases folder.
type nonRegularRow struct {
	name string
	// id is the id its filename spells; nothing may ever resolve to it.
	id string
	// plant creates the shape at featDir, or returns a reason to skip THIS ROW.
	plant func(t *testing.T, featDir string) (path string, skip string)
	// rejectedBy names the check that actually rejects this shape. Not every
	// shape reaches the non-regular guard, and the rows say which do.
	rejectedBy string
}

func TestSnapshot_NonRegularCandidateIsNeverOpenedAsASpec(t *testing.T) {
	rows := []nonRegularRow{
		{
			name:       "fifo",
			id:         "tc-22222222",
			rejectedBy: "the non-regular guard (snapshot.go:213-216): os.Stat succeeds and Mode().IsRegular() is false",
			plant: func(t *testing.T, featDir string) (string, string) {
				p := filepath.Join(featDir, "tc-22222222-fifo.md")
				if err := mkfifoForTest(p); err != nil {
					return "", "cannot create a FIFO here: " + err.Error()
				}
				return p, ""
			},
		},
		{
			name: "dangling-symlink",
			id:   "tc-33333333",
			// NOT the non-regular guard, checked rather than assumed: the link
			// itself exists, so pathsafe.resolveExistingPrefix lstats it
			// successfully and calls EvalSymlinks ON THE LINK (pathsafe.go:225-231),
			// which fails on the missing target. ResolveUnderRoot therefore returns
			// an error and classifyCandidate:210-212 rejects the entry one step
			// before the stat. Removing the non-regular guard leaves this row
			// green, and that is the honest result, not a hole in the row.
			rejectedBy: "the containment/canonicalisation arm (snapshot.go:209-212, via pathsafe.go:225-231)",
			plant: func(t *testing.T, featDir string) (string, string) {
				p := filepath.Join(featDir, "tc-33333333-dangling.md")
				if err := os.Symlink(filepath.Join(featDir, "no-such-target.md"), p); err != nil {
					return "", "cannot create a symlink here: " + err.Error()
				}
				return p, ""
			},
		},
		{
			name: "directory-named-like-a-spec",
			id:   "tc-44444444",
			// NOT the guard: filepath.Walk reports a real directory with
			// info.IsDir() true, so buildSnapshot returns at snapshot.go:177-182
			// and classifyCandidate is never called for it. Asserted here anyway
			// because the outcome the criterion cares about -- never admitted as a
			// spec -- must hold for this shape too, whichever check delivers it.
			rejectedBy: "the walk's directory branch (snapshot.go:177-182), before classifyCandidate is called",
			plant: func(t *testing.T, featDir string) (string, string) {
				p := filepath.Join(featDir, "tc-44444444-dir.md")
				if err := os.MkdirAll(p, 0o755); err != nil {
					t.Fatalf("mkdir %s: %v", p, err)
				}
				return p, ""
			},
		},
		{
			name: "symlink-to-a-directory-named-like-a-spec",
			id:   "tc-55555555",
			// The directory shape that DOES reach the guard: filepath.Walk lstats,
			// so a symlink to a directory arrives with info.IsDir() false, and
			// ResolveUnderRoot resolves it happily to an in-tree directory. Only
			// snapshot.go:214's IsRegular check stops it.
			rejectedBy: "the non-regular guard (snapshot.go:213-216): the resolved target is a directory, not a regular file",
			plant: func(t *testing.T, featDir string) (string, string) {
				target := filepath.Join(featDir, "real-subdir")
				if err := os.MkdirAll(target, 0o755); err != nil {
					t.Fatalf("mkdir %s: %v", target, err)
				}
				p := filepath.Join(featDir, "tc-55555555-symdir.md")
				if err := os.Symlink(target, p); err != nil {
					return "", "cannot create a symlink here: " + err.Error()
				}
				return p, ""
			},
		},
	}

	for _, row := range rows {
		row := row
		t.Run(row.name, func(t *testing.T) {
			root, cases := casesDirFor(t)
			featRel := "gtms/test/cases/feat"
			writeSpecID(t, cases, "feat/tc-11111111-login.md", "tc-11111111")
			feat := filepath.Join(cases, "feat")

			planted, skip := row.plant(t, feat)
			if skip != "" {
				// Capability skip for THIS ROW only. A skipped row proves nothing:
				// it is a gap in the evidence, not a pass.
				t.Skipf("SKIPPED, NOT EVIDENCE: this row could not be planted on this filesystem (%s); the %s shape is UNVERIFIED here", skip, row.name)
			}
			if _, lerr := os.Lstat(planted); lerr != nil {
				t.Fatalf("fixture %s was not created: %v", planted, lerr)
			}

			// The snapshot must COMPLETE. Opening a FIFO with no writer blocks
			// forever, so a build that never returns is itself the failure.
			snap := snapshotWithin(t, root, 20*time.Second)

			// 1. Not an owner: the non-regular entry contributes no candidate, so
			//    nothing resolves to the id its filename spells.
			for _, c := range snap.candidates {
				if filepath.Base(c.LogicalRel) == filepath.Base(planted) {
					t.Fatalf("%s was admitted as a candidate (%+v): %s should have rejected it before it was opened", row.name, c, row.rejectedBy)
				}
			}
			if r := snap.Resolve(row.id); r.Outcome != OutcomeNotFound {
				t.Fatalf("Resolve(%s) => %v (unprovable=%q reason=%v owner=%q), want OutcomeNotFound: a non-regular entry is neither an owner nor Unprovable",
					row.id, r.Outcome, r.UnprovablePath, r.Reason, r.OwnerLogicalRel)
			}

			// 2. Not an Unprovable candidate either -- the other way a bad entry
			//    could bite, by failing the whole folder closed.
			for _, recursive := range []bool{false, true} {
				ids, unprovable := snap.SelectFolder(featRel, recursive)
				if len(unprovable) != 0 {
					t.Fatalf("SelectFolder(recursive=%v) reported %d unprovable candidate(s) (first %q, reason %v): %s must not block a folder-scoped mutation",
						recursive, len(unprovable), unprovable[0].LogicalRel, unprovable[0].Reason, row.name)
				}
				if len(ids) != 1 || ids[0] != "tc-11111111" {
					t.Fatalf("SelectFolder(recursive=%v) ids => %v, want [tc-11111111]: the real spec beside %s must still be selected",
						recursive, ids, row.name)
				}
			}

			// 3. The surrounding operation reaches its documented safe outcome: the
			//    folder-scoped mutation guard permits the folder (it does not error
			//    out on the odd entry), and the real spec beside it still resolves
			//    to its single owner (it does not silently proceed over nothing).
			for _, recursive := range []bool{false, true} {
				if err := snap.RequireFolderOwnership(featRel, recursive); err != nil {
					t.Fatalf("RequireFolderOwnership(recursive=%v) refused the folder because of %s: %v", recursive, row.name, err)
				}
			}
			r := snap.Resolve("tc-11111111")
			if r.Outcome != OutcomeUniqueOwner || filepath.Base(r.OwnerLogicalRel) != "tc-11111111-login.md" {
				t.Fatalf("Resolve(tc-11111111) => %v %q, want UniqueOwner tc-11111111-login.md", r.Outcome, r.OwnerLogicalRel)
			}
		})
	}
}

// A directory named like a spec is excluded from ownership as a CANDIDATE, but
// it is still a directory: the walk records it (snapshot.go:177-182) and any
// real spec inside it is selected by a recursive folder scope. Asserting this
// pins the difference between "not a spec" and "not there".
func TestSnapshot_DirectoryNamedLikeASpecStillHoldsSpecs(t *testing.T) {
	root, cases := casesDirFor(t)
	writeSpecID(t, cases, "feat/tc-11111111-login.md", "tc-11111111")
	writeSpecID(t, cases, "feat/tc-44444444-dir.md/tc-66666666-inner.md", "tc-66666666")

	snap := mustSnapshot(t, root)

	if r := snap.Resolve("tc-44444444"); r.Outcome != OutcomeNotFound {
		t.Fatalf("Resolve(tc-44444444) => %v, want OutcomeNotFound: the directory itself is not a spec", r.Outcome)
	}
	ids, unprovable := snap.SelectFolder("gtms/test/cases/feat", true)
	if len(unprovable) != 0 {
		t.Fatalf("unprovable => %d (%q), want 0", len(unprovable), unprovable[0].LogicalRel)
	}
	if len(ids) != 2 || ids[0] != "tc-11111111" || ids[1] != "tc-66666666" {
		t.Fatalf("recursive ids => %v, want [tc-11111111 tc-66666666]: a spec inside the odd-named directory is still selected", ids)
	}
}

// snapshotWithin builds a snapshot and fails if it has not returned within
// limit. A non-regular entry that got opened rather than rejected can block
// indefinitely (a FIFO with no writer), which no test timeout attributes to the
// row that caused it.
func snapshotWithin(t *testing.T, root string, limit time.Duration) *Snapshot {
	t.Helper()
	type result struct {
		snap *Snapshot
		err  error
	}
	done := make(chan result, 1)
	go func() {
		snap, err := NewSnapshot(root)
		done <- result{snap, err}
	}()
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("NewSnapshot: %v", got.err)
		}
		return got.snap
	case <-time.After(limit):
		t.Fatalf("NewSnapshot did not return within %s: the non-regular entry was OPENED instead of being rejected (snapshot.go:213-216)", limit)
		return nil
	}
}
