package reader_test

// ADR-024 "Delivery boundary" (5a) enforcement guard -- assertion 3, the
// bounded-enumeration assertion.
//
// The static half of the guard (internal/testcase/adr024_boundary_guard_test.go)
// cannot see a future helper swap that keeps the call graph clean while doing the
// work anyway. 5a therefore pairs it with a runtime measurement, and is specific
// about the form:
//
//   - Instrumented COUNTS are the assertion. Elapsed time is never the gate --
//     this repository alone spans a ~274x difference between /mnt/c and native
//     ext4 for the same command.
//   - It is a SCALING property, measured over at least two fixture sizes with the
//     cases tree held FIXED and the wiring-record count increased.
//   - Project-wide snapshot constructions must be ZERO at both sizes.
//   - Spec reading attributable to IDENTITY RESOLUTION must not grow with the
//     wiring count.
//   - A single fixture size, or a constant ceiling such as "fewer than N reads",
//     is NOT a gate: every terminating program performs a bounded number of reads.
//
// EVERY DEFERRED ENTRY POINT IS MEASURED, not just bulk status. 5a's deferred row
// is "bulk status, map, gaps, triage listing (call sites include
// internal/reader/picker.go)", and an assertion that measures only
// reader.PipelineStatus leaves reader.Gaps, reader.Map and reader.ListTriage
// unmeasured -- the same coverage hole the static half had, in the half that is
// supposed to catch what static analysis cannot. Injecting a snapshot build into
// reader.Gaps's per-spec loop was invisible to both. The operations below are the
// runtime counterpart of deferredBulkReadConsumers in the static guard; the two
// lists are meant to name the same operations.
//
// reader.ClassifyWiring and the rest of internal/reader/picker.go are not listed
// separately: they are per-wiring-record helpers with no standalone operation to
// invoke, and every measured operation below drives them.
//
// HOW THE COUNT IS TAKEN, without touching production code.
//
// The fixture splits the cases tree in two: gtms/test/cases/feat/ holds the few
// specs the bulk read is SCOPED to, and gtms/test/cases/bulk/ holds decoy specs
// that a correctly scoped folder read has no reason to open. The decoys are padded
// so that reading the decoy folder even once is enormous next to everything else
// the operation legitimately reads.
//
// A project-wide snapshot construction walks the WHOLE cases tree and os.ReadFile
// + frontmatter.Parse's every .md in it, so it reads the decoys. The pre-migration
// resolver 5a defers these callers to is a filename-matched walk that does not
// read or parse spec bodies, so it reads none of them. That difference is the
// instrument: bytes read from the decoy folder ARE the count of project-wide
// snapshot constructions, scaled by the tree.
//
// Two of the measured operations -- the folder-summary roll-ups -- are unscoped by
// construction: they summarise EVERY folder, so opening the decoy folder is their
// correct behaviour and "zero decoy bytes" is not their gate. They are held to
// 5a's other sentence instead: "an operation performs at most one project-wide
// enumeration". Each such operation is allowed the frontmatter reading of its own
// single legitimate pass and nothing beyond it, and the scaling gate applies to
// them unchanged -- which is the gate that actually catches the O(N^2) shape.
//
// Attribution is exact rather than inferred. Each measurement runs twice, against
// two fixtures identical except that one has no decoy folder at all, and the
// decoy-attributable bytes are the difference. Nothing has to be assumed about
// what else the operation reads.
//
// The reading itself is counted from /proc/self/io (rchar), which counts bytes
// returned by read-family syscalls for this process whether or not they came from
// the page cache. That makes the measurement Linux-only; see the skip below.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/aitestmanagement/gtms-cli/internal/reader"
)

// Fixture shape. The cases tree is IDENTICAL at both wiring sizes -- 5a requires
// the wiring-record count to be the only thing that changes, because the
// regression is driven by per-record work over a fixed cases tree.
const (
	scopedSpecCount = 8      // specs under feat/, the scope of the bulk read
	decoySpecCount  = 24     // specs under bulk/, out of the read's scope
	decoySpecBytes  = 262144 // 256 KiB of padding each -> a ~6 MiB decoy folder
	smallWiringSet  = 200    // 5a's worked example
	largeWiringSet  = 2000   // 5a's worked example
)

// readCharsForSelf returns the rchar counter for this process: the number of bytes
// read-family syscalls have returned. Returns ok=false where /proc is not
// available.
func readCharsForSelf() (int64, bool) {
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

func tcIDAt(i int) string { return fmt.Sprintf("tc-%08x", 0x10000000+i) }

func writeFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// scopedFixtureScope is the feat/-only scope every scoped operation is measured
// under. It is the discriminator the whole instrument rests on: an operation
// scoped here has no legitimate reason to open bulk/.
func scopedFixtureScope(root string) *reader.ScopeInfo {
	return &reader.ScopeInfo{
		ScanDir:   filepath.Join(root, "gtms", "test", "cases", "feat"),
		RelPath:   "gtms/test/cases/feat/",
		Recursive: false,
	}
}

// bulkReadOp is one deferred entry point from 5a's matrix, together with how to
// invoke it and how many full passes over the out-of-scope decoy folder its
// correct implementation is entitled to.
type bulkReadOp struct {
	name string
	why  string
	// legitimatePasses is the number of full decoy-folder passes this operation
	// correctly performs. Zero for a scoped read, which has no reason to open the
	// folder at all; one for an unscoped roll-up, which summarises every folder
	// and therefore reads it once. The gate is always "not one pass more than
	// this", so 5a's "at most one project-wide enumeration" is what is asserted
	// in both cases.
	legitimatePasses int
	// run invokes the operation and sanity-checks that it actually did the work
	// the measurement attributes to it. A silently empty result would read as a
	// clean measurement.
	run func(t *testing.T, root string, wiringCount int)
}

func deferredBulkReadOps() []bulkReadOp {
	return []bulkReadOp{
		{
			name:             "PipelineStatus",
			why:              "bulk status: the scoped pipeline read behind `gtms status <folder>`",
			legitimatePasses: 0,
			run: func(t *testing.T, root string, _ int) {
				entries, err := reader.PipelineStatus(root, scopedFixtureScope(root), "", false)
				if err != nil {
					t.Fatalf("PipelineStatus: %v", err)
				}
				if len(entries) != scopedSpecCount {
					t.Fatalf("PipelineStatus returned %d entries, want %d -- the fixture is not "+
						"exercising the folder read this assertion is about", len(entries), scopedSpecCount)
				}
			},
		},
		{
			name:             "Gaps",
			why:              "gaps: the scoped coverage-gap read, which reaches specs via scanTestCases",
			legitimatePasses: 0,
			run: func(t *testing.T, root string, _ int) {
				rep, err := reader.Gaps(root, scopedFixtureScope(root), "", false)
				if err != nil {
					t.Fatalf("Gaps: %v", err)
				}
				if rep.TotalTestCases != scopedSpecCount {
					t.Fatalf("Gaps covered %d test cases, want %d -- the fixture is not exercising "+
						"the folder read this assertion is about", rep.TotalTestCases, scopedSpecCount)
				}
			},
		},
		{
			name:             "Map",
			why:              "map: the scoped traceability read, which reaches specs via scanTestCases",
			legitimatePasses: 0,
			run: func(t *testing.T, root string, _ int) {
				rep, err := reader.Map(root, scopedFixtureScope(root), "", false)
				if err != nil {
					t.Fatalf("Map: %v", err)
				}
				// The fixture specs carry no requirement, so every entry is unlinked.
				if len(rep.Unlinked) != scopedSpecCount {
					t.Fatalf("Map returned %d unlinked entries, want %d -- the fixture is not "+
						"exercising the folder read this assertion is about", len(rep.Unlinked), scopedSpecCount)
				}
			},
		},
		{
			name:             "ListTriage(scoped)",
			why:              "triage listing, spec-scoped: IDs come from the specs under the scope",
			legitimatePasses: 0,
			run: func(t *testing.T, root string, wiringCount int) {
				views, err := reader.ListTriage(root, scopedFixtureScope(root), "", true)
				if err != nil {
					t.Fatalf("ListTriage(scoped): %v", err)
				}
				if len(views) != wiringCount {
					t.Fatalf("ListTriage(scoped) returned %d views, want %d -- the fixture is not "+
						"exercising the per-record loop this assertion is about", len(views), wiringCount)
				}
			},
		},
		{
			name: "ListTriage(root)",
			why: "triage listing, root-scoped: the branch that calls the legacy resolver per record " +
				"for the orphan marker",
			legitimatePasses: 0,
			run: func(t *testing.T, root string, wiringCount int) {
				views, err := reader.ListTriage(root, nil, "", true)
				if err != nil {
					t.Fatalf("ListTriage(root): %v", err)
				}
				if len(views) != wiringCount {
					t.Fatalf("ListTriage(root) returned %d views, want %d -- the fixture is not "+
						"exercising the per-record loop this assertion is about", len(views), wiringCount)
				}
			},
		},
		{
			name:             "PipelineFolderSummary",
			why:              "bulk status, unscoped: the folder roll-up behind a bare `gtms status`",
			legitimatePasses: 1,
			run: func(t *testing.T, root string, _ int) {
				rows, err := reader.PipelineFolderSummary(root, "")
				if err != nil {
					t.Fatalf("PipelineFolderSummary: %v", err)
				}
				if len(rows) == 0 {
					t.Fatal("PipelineFolderSummary returned no folders -- the fixture is not " +
						"exercising the roll-up this assertion is about")
				}
			},
		},
		{
			name:             "GapsFolderSummary",
			why:              "gaps, unscoped: the folder roll-up behind a bare `gtms gaps`",
			legitimatePasses: 1,
			run: func(t *testing.T, root string, _ int) {
				rows, err := reader.GapsFolderSummary(root, "")
				if err != nil {
					t.Fatalf("GapsFolderSummary: %v", err)
				}
				if len(rows) == 0 {
					t.Fatal("GapsFolderSummary returned no folders -- the fixture is not " +
						"exercising the roll-up this assertion is about")
				}
			},
		},
	}
}

// buildBulkReadFixture creates a project with a fixed cases tree and wiringCount
// wiring records spread across the scoped specs. Each wiring record gets one
// matching triage record, so the triage listing's per-record loop scales on the
// same knob 5a specifies -- the wiring-record count -- rather than on a second one.
// withDecoys controls only whether the out-of-scope decoy folder exists;
// everything else is identical.
func buildBulkReadFixture(t *testing.T, wiringCount int, withDecoys bool) (root string, decoyBytes int64) {
	t.Helper()
	root = t.TempDir()
	casesDir := filepath.Join(root, "gtms", "test", "cases")

	for i := 0; i < scopedSpecCount; i++ {
		id := tcIDAt(i)
		writeFixtureFile(t, filepath.Join(casesDir, "feat", id+".md"),
			"---\ntest_case_id: "+id+"\ntitle: scoped "+id+"\n---\n\nbody\n")
	}

	if withDecoys {
		padding := strings.Repeat("decoy padding line to make a body read expensive\n", decoySpecBytes/48)
		for i := 0; i < decoySpecCount; i++ {
			id := tcIDAt(1000 + i)
			body := "---\ntest_case_id: " + id + "\ntitle: decoy " + id + "\n---\n\n" + padding
			writeFixtureFile(t, filepath.Join(casesDir, "bulk", id+".md"), body)
			decoyBytes += int64(len(body))
		}
	}

	wiringDir := filepath.Join(root, "gtms", "automation", "wiring")
	if err := os.MkdirAll(wiringDir, 0755); err != nil {
		t.Fatal(err)
	}
	triageDir := filepath.Join(root, "gtms", "triage")
	if err := os.MkdirAll(triageDir, 0755); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < wiringCount; n++ {
		id := tcIDAt(n % scopedSpecCount)
		framework := fmt.Sprintf("fw%05d", n/scopedSpecCount)
		content := "testcase: " + id + "\n" +
			"testcase-hash: 0011223344556677\n" +
			"framework: " + framework + "\n" +
			"adapter: " + framework + "-runner\n" +
			"artefact: test/acceptance/" + id + "--" + framework + ".bats\n" +
			"artefact-hash: aabbccddeeff0011\n"
		writeFixtureFile(t, filepath.Join(wiringDir, id+"--"+framework+".wiring.yaml"), content)

		triage := "version: 1\n" +
			"test_case_id: " + id + "\n" +
			"framework: " + framework + "\n" +
			"status: active\n" +
			"value: product-defect\n" +
			"classified_at: \"2026-01-01T00:00:00Z\"\n" +
			"snapshot:\n" +
			"    task: task-0000" + fmt.Sprintf("%04d", n%10000) + "\n" +
			"    result: fail\n"
		writeFixtureFile(t, filepath.Join(triageDir, id+"--"+framework+".triage.yaml"), triage)
	}
	return root, decoyBytes
}

// decoyBytesReadBy runs every deferred entry point once and returns, per
// operation, the bytes it read that are attributable to the out-of-scope decoy
// folder -- measured as the difference against an identical project that has no
// decoy folder.
//
// The two fixtures are built once and shared by every operation. rchar counts
// bytes returned by read syscalls whether or not they came from the page cache,
// so a warm cache on the later operations does not deflate their counts.
func decoyBytesReadBy(t *testing.T, wiringCount int) (attributed map[string]int64, oneDecoyPass int64) {
	t.Helper()

	measure := func(op bulkReadOp, root string) int64 {
		before, ok := readCharsForSelf()
		if !ok {
			t.Fatal("rchar unavailable mid-measurement")
		}
		op.run(t, root, wiringCount)
		after, ok := readCharsForSelf()
		if !ok {
			t.Fatal("rchar unavailable mid-measurement")
		}
		return after - before
	}

	withRoot, decoyTotal := buildBulkReadFixture(t, wiringCount, true)
	withoutRoot, _ := buildBulkReadFixture(t, wiringCount, false)

	attributed = map[string]int64{}
	for _, op := range deferredBulkReadOps() {
		// Order the two runs so neither gets a systematically colder cache.
		baseline := measure(op, withoutRoot)
		full := measure(op, withRoot)
		d := full - baseline
		if d < 0 {
			d = 0
		}
		attributed[op.name] = d
	}
	return attributed, decoyTotal
}

// TestADR024Boundary_BulkReadEnumerationIsBounded is assertion 3.
//
// It landed deliberately RED, before the resolver change, because 5a requires the
// guard to be the FIRST implementation artifact. Re-plan step 3 restored the
// boundary -- testcase.ResolveSpec is the pre-migration filename walk again -- so
// the assertion now runs unconditionally in the normal suite. There is no env
// override and no skip: a deferred bulk-read caller that starts building
// project-wide snapshots again fails `go test ./...` on the spot.
func TestADR024Boundary_BulkReadEnumerationIsBounded(t *testing.T) {
	if _, ok := readCharsForSelf(); !ok {
		t.Skip("ADR-024 5a assertion 3 needs /proc/self/io to count bytes read without " +
			"instrumenting production code; it is not available on this platform")
	}
	if testing.Short() {
		t.Skip("builds a multi-megabyte fixture and runs every deferred entry point at two sizes")
	}
	small, oneDecoyPass := decoyBytesReadBy(t, smallWiringSet)
	large, _ := decoyBytesReadBy(t, largeWiringSet)

	t.Logf("cases tree fixed at %d scoped specs + %d decoy specs (one full pass over the "+
		"decoy folder reads %d bytes)", scopedSpecCount, decoySpecCount, oneDecoyPass)
	names := make([]string, 0, len(small))
	for name := range small {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t.Logf("decoy-attributable bytes read by %s: %d wiring records -> %d; %d wiring records -> %d",
			name, smallWiringSet, small[name], largeWiringSet, large[name])
	}

	for _, op := range deferredBulkReadOps() {
		t.Run(op.name, func(t *testing.T) {
			// (a) An operation performs AT MOST ONE project-wide enumeration, and a
			// scoped one performs none. The threshold is not a chosen constant: it
			// is the cost of one full pass over a folder, times the number of passes
			// this operation legitimately makes. A scoped folder read has no reason
			// to open bulk/ at all; an unscoped roll-up reads it exactly once.
			ceiling := int64(op.legitimatePasses+1) * oneDecoyPass
			for _, m := range []struct {
				wiring int
				bytes  int64
			}{{smallWiringSet, small[op.name]}, {largeWiringSet, large[op.name]}} {
				if m.bytes >= ceiling {
					t.Errorf("with %d wiring records %s read %d bytes from the out-of-scope cases "+
						"folder -- %.1f full passes over it, against %d legitimate.\n"+
						"    %s\n"+
						"    ADR-024 5a: an operation performs at most one project-wide enumeration, "+
						"and project-wide snapshot constructions inside a deferred consumer must be "+
						"ZERO. Reading past this means the read went through project-wide snapshot "+
						"resolution.\n"+
						"    Hoisting a single snapshot into a deferred consumer does not satisfy "+
						"this: the prohibition is on reaching project-wide resolution AT ALL, not "+
						"per record.",
						m.wiring, op.name, m.bytes, float64(m.bytes)/float64(oneDecoyPass),
						op.legitimatePasses, op.why)
				}
			}

			// (b) The SCALING property: spec reading attributable to identity
			// resolution must not grow with the wiring count. Holding the cases tree
			// fixed and multiplying the wiring records by ten must not add even ONE
			// more pass over the cases tree.
			//
			// This is what a constant ceiling cannot express. A quadratic bulk read
			// over a small fixture performs a bounded number of reads and passes any
			// ceiling; it cannot pass this, because the extra records each drag the
			// whole tree in.
			if growth := large[op.name] - small[op.name]; growth >= oneDecoyPass {
				t.Errorf("multiplying the wiring records by %d (%d -> %d) over an UNCHANGED cases "+
					"tree added %d bytes of spec reading in %s -- %.1f extra full passes over the "+
					"cases tree.\n"+
					"    %s\n"+
					"    ADR-024 5a: the cases-tree enumerations, frontmatter parses and spec-file "+
					"opens ATTRIBUTABLE TO IDENTITY RESOLUTION must remain bounded as the wiring "+
					"count grows. Per-record identity resolution that walks and parses the whole "+
					"tree is the O(N^2) regression this assertion exists to catch.",
					largeWiringSet/smallWiringSet, smallWiringSet, largeWiringSet,
					growth, op.name, float64(growth)/float64(oneDecoyPass), op.why)
			}
		})
	}
}
