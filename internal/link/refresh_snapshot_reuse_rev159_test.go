package link

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aitestmanagement/gtms-cli/internal/testcase"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// REV-159 CODEX-006 -- refresh rebuilt the ownership snapshot for every record.
//
// The CLI already built one snapshot to partition refresh targets, but
// PlanRefresh called resolveLinkOwner, which built ANOTHER. A folder refresh
// therefore walked and parsed the whole cases tree once per wiring record, and
// successive records could be judged against different ownership views if the
// tree changed mid-operation -- ADR-024 s2 requires one snapshot per operation,
// and the second half of that is a correctness property, not just a cost.
//
// This is a SCALING assertion, in the shape ADR-024 5a requires: the cases tree
// is held FIXED and the wiring-record count is increased. A per-record snapshot
// makes the work grow with the wiring count; reusing the operation's snapshot
// does not. A single fixture size would not discriminate, because every
// terminating program reads a bounded number of bytes.
//
// The instrument is the same one internal/reader's bounded-enumeration guard
// uses: a decoy folder OUTSIDE the refresh scope, whose bytes are read only when
// something enumerates the project-wide tree, counted through /proc/self/io so no
// production code has to be instrumented.

const (
	refreshScopedSpecs = 4
	refreshDecoySpecs  = 12
	refreshDecoyBytes  = 65536 // 64 KiB each -> a ~768 KiB decoy folder
	refreshSmallSet    = 25
	refreshLargeSet    = 250
)

// rcharForSelf returns bytes returned by read-family syscalls for this process.
func rcharForSelf() (int64, bool) {
	data, err := os.ReadFile("/proc/self/io")
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "rchar:") {
			continue
		}
		n, perr := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "rchar:")), 10, 64)
		if perr != nil {
			return 0, false
		}
		return n, true
	}
	return 0, false
}

func refreshTCID(i int) string { return fmt.Sprintf("tc-%08x", 0x20000000+i) }

// buildRefreshFixture builds a project whose cases tree is IDENTICAL at every
// wiring count. withDecoys is the only difference between the two roots measured,
// so subtracting one run from the other attributes bytes to the decoy folder.
func buildRefreshFixture(t *testing.T, wiringCount int, withDecoys bool) (root string, decoyBytes int64) {
	t.Helper()
	root = t.TempDir()
	cases := filepath.Join(root, "gtms", "test", "cases")

	for i := 0; i < refreshScopedSpecs; i++ {
		id := refreshTCID(i)
		p := filepath.Join(cases, "feat", id+"-spec.md")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("---\ntest_case_id: "+id+"\ntitle: \"t\"\n---\n\nbody\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if withDecoys {
		pad := strings.Repeat("x", refreshDecoyBytes)
		for i := 0; i < refreshDecoySpecs; i++ {
			id := refreshTCID(1000 + i)
			body := "---\ntest_case_id: " + id + "\ntitle: \"t\"\n---\n\n" + pad + "\n"
			p := filepath.Join(cases, "decoy", id+"-spec.md")
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			decoyBytes += int64(len(body))
		}
	}

	art := filepath.Join(root, "tests")
	if err := os.MkdirAll(art, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(art, "a.bats"), []byte("@test x { true; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	wdir := filepath.Join(root, "gtms", "automation", "wiring")
	if err := os.MkdirAll(wdir, 0o755); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < wiringCount; n++ {
		id := refreshTCID(n % refreshScopedSpecs)
		framework := fmt.Sprintf("fw%05d", n/refreshScopedSpecs)
		body := "testcase: " + id + "\ntestcase-hash: 1111222233334444\nframework: " + framework +
			"\nadapter: bats-runner\nartefact: tests/a.bats\nartefact-hash: aaaabbbbccccdddd\n"
		if err := os.WriteFile(filepath.Join(wdir, id+"--"+framework+".wiring.yaml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root, decoyBytes
}

// decoyBytesForRefresh runs one folder-refresh-shaped operation -- ONE snapshot,
// then PlanRefresh per discovered record, exactly as runFolderRefresh does -- and
// returns the bytes attributable to the out-of-scope decoy folder.
func decoyBytesForRefresh(t *testing.T, wiringCount int) (attributed, oneDecoyPass int64) {
	t.Helper()

	measure := func(root string) int64 {
		records, err := wiring.DiscoverAll(root)
		if err != nil {
			t.Fatalf("DiscoverAll: %v", err)
		}
		if len(records) != wiringCount {
			t.Fatalf("fixture has %d wiring records, want %d", len(records), wiringCount)
		}
		before, ok := rcharForSelf()
		if !ok {
			t.Fatal("rchar unavailable mid-measurement")
		}
		// The operation's single snapshot, as the CLI builds it.
		snap, serr := testcase.NewSnapshot(root)
		if serr != nil {
			t.Fatalf("NewSnapshot: %v", serr)
		}
		for _, dr := range records {
			if dr.Record == nil {
				continue
			}
			if _, perr := PlanRefresh(root, snap, dr.Record); perr != nil {
				t.Fatalf("PlanRefresh: %v", perr)
			}
		}
		after, ok := rcharForSelf()
		if !ok {
			t.Fatal("rchar unavailable mid-measurement")
		}
		return after - before
	}

	withRoot, decoyTotal := buildRefreshFixture(t, wiringCount, true)
	withoutRoot, _ := buildRefreshFixture(t, wiringCount, false)

	baseline := measure(withoutRoot)
	full := measure(withRoot)

	attributed = full - baseline
	if attributed < 0 {
		attributed = 0
	}
	return attributed, decoyTotal
}

func TestRefresh_BuildsOneSnapshotPerOperationNotPerRecord(t *testing.T) {
	if _, ok := rcharForSelf(); !ok {
		t.Skip("needs /proc/self/io to count bytes read without instrumenting production code")
	}
	if testing.Short() {
		t.Skip("builds a sub-megabyte fixture and runs two refresh passes per size")
	}

	small, oneDecoyPass := decoyBytesForRefresh(t, refreshSmallSet)
	large, _ := decoyBytesForRefresh(t, refreshLargeSet)

	t.Logf("cases tree fixed at %d scoped + %d decoy specs (one full pass over the decoy folder reads %d bytes)",
		refreshScopedSpecs, refreshDecoySpecs, oneDecoyPass)
	t.Logf("decoy-attributable bytes: %d wiring records -> %d; %d wiring records -> %d",
		refreshSmallSet, small, refreshLargeSet, large)

	// The operation legitimately reads the decoy folder ONCE, for its own
	// snapshot. The defect is reading it again per record, so the discriminator is
	// whether the cost SCALES with the wiring count.
	//
	// Ten passes is a deliberately generous ceiling: the small run is 25 records
	// and the large 250, so a per-record implementation reads the decoy folder 250
	// times and clears this by an enormous margin, while a correct one reads it
	// once at both sizes.
	ceiling := oneDecoyPass * 10
	if large > ceiling {
		t.Fatalf("refresh read %d decoy bytes at %d wiring records (one pass is %d bytes, ceiling %d): "+
			"identity resolution is being rebuilt per record instead of reusing the operation's snapshot (ADR-024 s2)",
			large, refreshLargeSet, oneDecoyPass, ceiling)
	}

	// The scaling property itself: 10x the wiring records must not mean
	// meaningfully more project-wide enumeration.
	if large > small+ceiling {
		t.Fatalf("decoy bytes grew from %d to %d when the wiring count grew %dx with the cases tree held fixed: "+
			"project-wide enumeration must not scale with the number of wiring records",
			small, large, refreshLargeSet/refreshSmallSet)
	}
}
