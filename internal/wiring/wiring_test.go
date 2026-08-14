package wiring

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
)

func sampleRecord() *WiringRecord {
	return &WiringRecord{
		TestCase:     "tc-a1b2c3d4",
		TestCaseHash: "00112233aabbccdd",
		Framework:    "bats",
		Adapter:      "bats-runner",
		Artefact:     "test/acceptance/login.bats",
		ArtefactHash: "deadbeefcafef00d",
	}
}

func TestWrite_ProducesPureYAMLAtCanonicalPath(t *testing.T) {
	root := t.TempDir()
	rec := sampleRecord()

	path, err := Write(root, rec)
	if err != nil {
		t.Fatalf("Write returned error: %v", err)
	}

	wantPath := filepath.Join(layout.WiringDir(root), "tc-a1b2c3d4--bats.wiring.yaml")
	if path != wantPath {
		t.Errorf("path = %q; want %q", path, wantPath)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading written file: %v", err)
	}

	if bytes.HasPrefix(data, []byte("---")) {
		t.Errorf("wiring output starts with frontmatter fence; want pure YAML.\nGot:\n%s", data)
	}
	if bytes.Contains(data, []byte("\n---\n")) {
		t.Errorf("wiring output contains frontmatter fence; want pure YAML.\nGot:\n%s", data)
	}

	for _, key := range []string{"testcase:", "testcase-hash:", "framework:", "adapter:", "artefact:", "artefact-hash:"} {
		if !bytes.Contains(data, []byte(key)) {
			t.Errorf("wiring output missing key %q.\nGot:\n%s", key, data)
		}
	}
}

func TestWriteRead_RoundTrip(t *testing.T) {
	root := t.TempDir()
	rec := sampleRecord()

	path, err := Write(root, rec)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if *got != *rec {
		t.Errorf("round-trip mismatch:\n got  %+v\n want %+v", got, rec)
	}
}

func TestWrite_RejectsMissingRequiredField(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		name string
		mut  func(r *WiringRecord)
	}{
		{"missing-testcase", func(r *WiringRecord) { r.TestCase = "" }},
		{"missing-testcase-hash", func(r *WiringRecord) { r.TestCaseHash = "" }},
		{"missing-framework", func(r *WiringRecord) { r.Framework = "" }},
		{"missing-adapter", func(r *WiringRecord) { r.Adapter = "" }},
		{"missing-artefact", func(r *WiringRecord) { r.Artefact = "" }},
		{"missing-artefact-hash", func(r *WiringRecord) { r.ArtefactHash = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := sampleRecord()
			tc.mut(rec)
			if _, err := Write(root, rec); err == nil {
				t.Errorf("Write succeeded; want validation error")
			}
		})
	}
}

func TestRead_RejectsUnknownFields(t *testing.T) {
	root := t.TempDir()
	dir := layout.WiringDir(root)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	cases := map[string]string{
		"status":          "status: developed",
		"lifecycle":       "lifecycle: accepted",
		"cycle":           "cycle: 3",
		"last-dev-result": "last-dev-result: pass",
		"results-file":    "results-file: gtms/execution/foo.results.yaml",
	}

	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			content := "testcase: tc-aaaaaaaa\n" +
				"testcase-hash: 0011223344556677\n" +
				"framework: bats\n" +
				"adapter: bats-runner\n" +
				"artefact: test/acceptance/x.bats\n" +
				"artefact-hash: 8899aabbccddeeff\n" +
				extra + "\n"
			path := filepath.Join(dir, "tc-aaaaaaaa--"+name+".wiring.yaml")
			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				t.Fatalf("seed: %v", err)
			}
			if _, err := Read(path); err == nil {
				t.Errorf("Read accepted unknown field %q; want strict-schema error", name)
			}
		})
	}
}

func TestRead_RejectsMissingRequiredField(t *testing.T) {
	root := t.TempDir()
	dir := layout.WiringDir(root)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// missing artefact-hash
	content := "testcase: tc-aaaaaaaa\n" +
		"testcase-hash: 0011223344556677\n" +
		"framework: bats\n" +
		"adapter: bats-runner\n" +
		"artefact: test/acceptance/x.bats\n"
	path := filepath.Join(dir, "tc-aaaaaaaa--bats.wiring.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := Read(path); err == nil {
		t.Errorf("Read accepted record with missing artefact-hash; want validation error")
	}
}

func TestFind_MissingFileReturnsNilNoError(t *testing.T) {
	root := t.TempDir()
	rec, path, err := Find(root, "tc-deadbeef", "bats")
	if err != nil {
		t.Fatalf("Find unexpected error: %v", err)
	}
	if rec != nil || path != "" {
		t.Errorf("Find on missing wiring: got rec=%v path=%q; want nil/\"\"", rec, path)
	}
}

// TestFind_PropagatesNonNotFoundErrors guards the contract that Find only
// suppresses os.ErrNotExist — every other open/read/parse failure must
// surface so the caller doesn't silently treat a corrupted (or
// permission-denied) wiring file as "no wiring." Previously the
// implementation also swallowed any error whose message contained the
// "opening wiring record" string, which was over-broad.
//
// We use a malformed YAML file to simulate a non-not-found error path
// portably (permission denial is harder to script on Windows).
func TestFind_PropagatesNonNotFoundErrors(t *testing.T) {
	root := t.TempDir()
	dir := layout.WiringDir(root)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Write a malformed wiring file: YAML that doesn't parse into the
	// six-field schema (unbalanced quotes -> parse failure inside Read,
	// wrapped via fmt.Errorf — not os.ErrNotExist).
	path := filepath.Join(dir, "tc-broken1--bats.wiring.yaml")
	if err := os.WriteFile(path, []byte("testcase: \"unterminated\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	rec, gotPath, err := Find(root, "tc-broken1", "bats")
	if err == nil {
		t.Fatalf("Find on malformed wiring: want error, got rec=%v path=%q", rec, gotPath)
	}
	if rec != nil {
		t.Errorf("Find on malformed wiring: rec must be nil; got %+v", rec)
	}
	// A parse error is not a not-found.
	if errors.Is(err, os.ErrNotExist) {
		t.Errorf("Find treated a malformed file as not-exist; want propagated error")
	}
}

func TestFind_ReturnsRecord(t *testing.T) {
	root := t.TempDir()
	wantPath, err := Write(root, sampleRecord())
	if err != nil {
		t.Fatalf("seed Write: %v", err)
	}
	got, gotPath, err := Find(root, "tc-a1b2c3d4", "bats")
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if got == nil || gotPath != wantPath {
		t.Errorf("Find: rec=%+v path=%q; wantPath=%q", got, gotPath, wantPath)
	}
}

func TestFindAllForTC_MultipleFrameworks(t *testing.T) {
	root := t.TempDir()
	want := map[string]*WiringRecord{
		"bats":       {TestCase: "tc-12345678", TestCaseHash: "aaaa1111bbbb2222", Framework: "bats", Adapter: "bats-runner", Artefact: "test/acceptance/x.bats", ArtefactHash: "bbbb2222cccc3333"},
		"manual":     {TestCase: "tc-12345678", TestCaseHash: "aaaa1111bbbb2222", Framework: "manual", Adapter: "manual-execute", Artefact: "test/manual/x.md", ArtefactHash: "cccc3333dddd4444"},
		"playwright": {TestCase: "tc-12345678", TestCaseHash: "aaaa1111bbbb2222", Framework: "playwright", Adapter: "playwright-runner", Artefact: "tests/x.spec.ts", ArtefactHash: "dddd4444eeee5555"},
	}
	for _, r := range want {
		if _, err := Write(root, r); err != nil {
			t.Fatalf("seed Write %s: %v", r.Framework, err)
		}
	}

	// Sibling TC must not leak into the result.
	other := sampleRecord()
	if _, err := Write(root, other); err != nil {
		t.Fatalf("seed sibling: %v", err)
	}

	got, err := FindAllForTC(root, "tc-12345678")
	if err != nil {
		t.Fatalf("FindAllForTC: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d records; want %d (%v)", len(got), len(want), got)
	}
	for _, r := range got {
		w, ok := want[r.Framework]
		if !ok {
			t.Errorf("unexpected framework in result: %s", r.Framework)
			continue
		}
		if *r != *w {
			t.Errorf("framework %s: got %+v; want %+v", r.Framework, r, w)
		}
	}
}

func TestScan_KeysByTestCase(t *testing.T) {
	root := t.TempDir()
	seed := []*WiringRecord{
		{TestCase: "tc-11111111", TestCaseHash: "aaaa1111bbbb2222", Framework: "bats", Adapter: "bats-runner", Artefact: "x.bats", ArtefactHash: "bbbb2222cccc3333"},
		{TestCase: "tc-11111111", TestCaseHash: "aaaa1111bbbb2222", Framework: "manual", Adapter: "manual-execute", Artefact: "x.md", ArtefactHash: "cccc3333dddd4444"},
		{TestCase: "tc-22222222", TestCaseHash: "dddd4444eeee5555", Framework: "bats", Adapter: "bats-runner", Artefact: "y.bats", ArtefactHash: "eeee5555ffff6666"},
	}
	for _, r := range seed {
		if _, err := Write(root, r); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	got, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(got["tc-11111111"]) != 2 {
		t.Errorf("tc-11111111 has %d records; want 2", len(got["tc-11111111"]))
	}
	if len(got["tc-22222222"]) != 1 {
		t.Errorf("tc-22222222 has %d records; want 1", len(got["tc-22222222"]))
	}
	if len(got) != 2 {
		t.Errorf("Scan returned %d TCs; want 2 — keys=%v", len(got), keys(got))
	}
}

func TestScan_MissingDirReturnsNil(t *testing.T) {
	root := t.TempDir()
	got, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan on missing dir: %v", err)
	}
	if got != nil {
		t.Errorf("Scan on missing dir: got %v; want nil", got)
	}
}

func TestScan_SkipsNonWiringFiles(t *testing.T) {
	root := t.TempDir()
	dir := layout.WiringDir(root)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Decoy files that must be ignored.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("notes"), 0644); err != nil {
		t.Fatalf("seed decoy: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tc-aaaaaaaa--bats.automation.md"), []byte("---\ntestcase: tc-aaaaaaaa\n---\n"), 0644); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	// Real wiring file alongside the decoys.
	if _, err := Write(root, sampleRecord()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(got) != 1 || len(got["tc-a1b2c3d4"]) != 1 {
		t.Errorf("Scan picked up decoys; got %v", got)
	}
}

func TestPath_RejectsUnsafeInputs(t *testing.T) {
	root := t.TempDir()
	cases := []struct{ tc, fw string }{
		{"../tc-evil", "bats"},
		{"tc-good", "../evil"},
		{"tc/slash", "bats"},
		{"tc-good", "fw\\bs"},
		{"", "bats"},
		{"tc-good", ""},
	}
	for _, c := range cases {
		if _, err := Path(root, c.tc, c.fw); err == nil {
			t.Errorf("Path(%q,%q) accepted unsafe input", c.tc, c.fw)
		}
	}
}

func TestWriteRead_PureYAMLBytesStable(t *testing.T) {
	// Guards against accidental frontmatter wrapping or extra trailing bytes
	// that would break BATS byte-equality assertions on Windows.
	root := t.TempDir()
	path, err := Write(root, sampleRecord())
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read first: %v", err)
	}
	if _, err := Write(root, sampleRecord()); err != nil {
		t.Fatalf("Write again: %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read second: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Errorf("rewriting the same record produced different bytes.\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	// Sanity: no markdown body and no fence sequences anywhere.
	if strings.Contains(string(first), "---") {
		t.Errorf("output contains '---' anywhere; want pure YAML.\n%s", first)
	}
}

func keys(m map[string][]*WiringRecord) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// --- ENH-151: PendingArtefactHash sentinel and hash predicates ---

func TestIsPendingArtefactHash(t *testing.T) {
	assert := func(value string, want bool) {
		t.Helper()
		if got := IsPendingArtefactHash(value); got != want {
			t.Errorf("IsPendingArtefactHash(%q) = %v; want %v", value, got, want)
		}
	}
	assert("pending", true)
	assert("", false)
	assert("PENDING", false)
	assert("Pending", false)
	assert("deadbeefcafef00d", false)
	assert("pending ", false)
}

func TestIsRealArtefactHash(t *testing.T) {
	cases := map[string]bool{
		"deadbeefcafef00d": true,
		"00112233aabbccdd": true,
		"0000000000000000": true,
		"pending":          false,
		"deadbeefcafef0":   false, // 15 chars
		"deadbeefcafef00d1": false, // 17 chars
		"DEADBEEFCAFEF00D": false, // uppercase
		"":                 false,
		"not-a-hex-string": false,
		"ghijklmnopqrstuv": false,
	}
	for input, want := range cases {
		if got := IsRealArtefactHash(input); got != want {
			t.Errorf("IsRealArtefactHash(%q) = %v; want %v", input, got, want)
		}
	}
}

func TestValidate_AcceptsPendingArtefactHash(t *testing.T) {
	root := t.TempDir()
	rec := &WiringRecord{
		TestCase:     "tc-pend0001",
		TestCaseHash: "0011223344556677",
		Framework:    "bats",
		Adapter:      "bats-runner",
		Artefact:     "test/acceptance/tc-pend0001.bats",
		ArtefactHash: PendingArtefactHash,
	}

	path, err := Write(root, rec)
	if err != nil {
		t.Fatalf("Write with pending artefact-hash: %v", err)
	}

	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read with pending artefact-hash: %v", err)
	}
	if got.ArtefactHash != PendingArtefactHash {
		t.Errorf("round-trip lost pending sentinel: got %q", got.ArtefactHash)
	}
}

func TestValidate_RejectsArbitraryArtefactHash(t *testing.T) {
	root := t.TempDir()
	cases := []string{
		"garbage",
		"12345",
		"DEADBEEFCAFEF00D", // uppercase
		"",
		"not-hex-16-chars",
		"abcdef123456789",  // 15 hex chars
		"abcdef12345678901", // 17 hex chars
	}
	for _, bad := range cases {
		rec := sampleRecord()
		rec.ArtefactHash = bad
		if _, err := Write(root, rec); err == nil {
			t.Errorf("Write accepted artefact-hash %q; want validation error", bad)
		}
	}
}

func TestValidate_RejectsArbitraryTestCaseHash(t *testing.T) {
	root := t.TempDir()
	cases := []string{
		"garbage",
		"pending", // pending is NOT valid for testcase-hash
		"AAAA",
	}
	for _, bad := range cases {
		rec := sampleRecord()
		rec.TestCaseHash = bad
		if _, err := Write(root, rec); err == nil {
			t.Errorf("Write accepted testcase-hash %q; want validation error", bad)
		}
	}
}

// --- ENH-192: ParseFilename and DiscoverAll tests ---

func TestParseFilename_ValidShape(t *testing.T) {
	tc, fw, ok := ParseFilename("tc-abc12345--bats.wiring.yaml")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if tc != "tc-abc12345" || fw != "bats" {
		t.Errorf("got tc=%q fw=%q", tc, fw)
	}
}

func TestParseFilename_InvalidShapes(t *testing.T) {
	cases := []string{
		"not-a-wiring.yaml",
		"nodashes.wiring.yaml",
		"--framework.wiring.yaml",
		"tc-abc--.wiring.yaml",
	}
	for _, name := range cases {
		_, _, ok := ParseFilename(name)
		if ok {
			t.Errorf("ParseFilename(%q) expected ok=false", name)
		}
	}
}

func TestDiscoverAll_ReturnsAllFiles(t *testing.T) {
	root := t.TempDir()
	recA := sampleRecord()
	recA.TestCase = "tc-aaaa1111"
	recA.Framework = "bats"
	_, _ = Write(root, recA)

	recB := sampleRecord()
	recB.TestCase = "tc-bbbb2222"
	recB.Framework = "playwright"
	_, _ = Write(root, recB)

	results, err := DiscoverAll(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("unexpected error for %s: %v", r.Path, r.Err)
		}
		if r.Record == nil {
			t.Errorf("record should not be nil for %s", r.Path)
		}
	}
}

func TestDiscoverAll_MalformedFileIncluded(t *testing.T) {
	root := t.TempDir()
	rec := sampleRecord()
	_, _ = Write(root, rec)

	// Write a malformed wiring file.
	dir := layout.WiringDir(root)
	badPath := filepath.Join(dir, "tc-bad11111--bats.wiring.yaml")
	_ = os.WriteFile(badPath, []byte("not: valid: yaml: ["), 0644)

	results, err := DiscoverAll(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	var found bool
	for _, r := range results {
		if r.TCFromName == "tc-bad11111" {
			found = true
			if r.Err == nil {
				t.Error("expected error for malformed file")
			}
			if r.Record != nil {
				t.Error("record should be nil for malformed file")
			}
		}
	}
	if !found {
		t.Error("malformed file not included in results")
	}
}

func TestDiscoverAll_ShapeViolatingFilename(t *testing.T) {
	root := t.TempDir()
	dir := layout.WiringDir(root)
	_ = os.MkdirAll(dir, 0755)

	// File with .wiring.yaml suffix but no -- separator.
	badPath := filepath.Join(dir, "noshape.wiring.yaml")
	_ = os.WriteFile(badPath, []byte("testcase: tc-x\n"), 0644)

	results, err := DiscoverAll(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Err == nil {
		t.Error("expected error for shape-violating filename")
	}
}

func TestDiscoverAll_EmptyDirectory(t *testing.T) {
	root := t.TempDir()
	dir := layout.WiringDir(root)
	_ = os.MkdirAll(dir, 0755)

	results, err := DiscoverAll(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestDiscoverAll_IdentityMismatchIsError(t *testing.T) {
	// CODEX-002: a schema-valid file whose declared testcase/framework disagrees
	// with its filename must be reported as an error and never exposed as a
	// selectable record (otherwise it is a cross-record overwrite vector).
	root := t.TempDir()
	dir := layout.WiringDir(root)
	_ = os.MkdirAll(dir, 0755)

	// Filename says tc-aaa11111--bats, but the record declares tc-bbb22222.
	content := "testcase: tc-bbb22222\ntestcase-hash: 1111111111111111\nframework: bats\nadapter: some-runner\nartefact: test/x.bats\nartefact-hash: pending\n"
	path := filepath.Join(dir, "tc-aaa11111--bats.wiring.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	results, err := DiscoverAll(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Err == nil {
		t.Error("expected identity-mismatch error")
	} else if !strings.Contains(results[0].Err.Error(), "identity mismatch") {
		t.Errorf("expected 'identity mismatch' in error, got: %v", results[0].Err)
	}
	if results[0].Record != nil {
		t.Error("record should be nil for an identity-mismatched file")
	}
	if results[0].TCFromName != "tc-aaa11111" {
		t.Errorf("expected TCFromName tc-aaa11111, got %q", results[0].TCFromName)
	}
}

// TestDiscoverForScope_SkipsOutOfScopeWithoutReading pins REV-129 CLAUDE-003 /
// CODEX-129-002 at the wiring boundary: the scope is matched against the
// FILENAME and decided BEFORE the file is opened. The out-of-scope fixture is
// unparseable, so if it were read it would come back as a failing
// DiscoveryResult; its absence is the evidence it was not.
func TestDiscoverForScope_SkipsOutOfScopeWithoutReading(t *testing.T) {
	root := t.TempDir()
	rec := sampleRecord()
	rec.TestCase = "tc-aaaa1111"
	rec.Framework = "bats"
	if _, err := Write(root, rec); err != nil {
		t.Fatal(err)
	}

	dir := layout.WiringDir(root)
	unreadable := filepath.Join(dir, "tc-bbbb2222--bats"+FileSuffix)
	if err := os.WriteFile(unreadable, []byte("{{{ not yaml\n"), 0644); err != nil {
		t.Fatal(err)
	}

	results, err := DiscoverForScope(root, []string{"tc-aaaa1111"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected only the in-scope record, got %d results", len(results))
	}
	if results[0].TCFromName != "tc-aaaa1111" || results[0].Err != nil {
		t.Fatalf("unexpected result: %+v", results[0])
	}

	// DiscoverAll (no filter) still reads everything, including the bad file --
	// so the exclusion above came from the scope filter, not from the file being
	// skipped for some unrelated reason.
	all, err := DiscoverAll(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("DiscoverAll should return both files, got %d", len(all))
	}
}

// TestDiscoverForScope_UnparseableFilenameSkippedWhenScoped: a filename that
// does not match {tc}--{framework} cannot match any requested ID, so a scoped
// discovery skips it. Unfiltered DiscoverAll still reports it as a shape
// failure -- that contract is unchanged.
func TestDiscoverForScope_UnparseableFilenameSkippedWhenScoped(t *testing.T) {
	root := t.TempDir()
	dir := layout.WiringDir(root)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "noseparator"+FileSuffix), []byte("x: 1\n"), 0644); err != nil {
		t.Fatal(err)
	}

	scoped, err := DiscoverForScope(root, []string{"noseparator", "tc-aaaa1111"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped) != 0 {
		t.Fatalf("expected the shape-violating file to be skipped when scoped, got %d", len(scoped))
	}

	all, err := DiscoverAll(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Err == nil {
		t.Fatalf("DiscoverAll must still report the shape violation, got %+v", all)
	}
}

// TestDiscoverForScope_TCIDContainingDoubleDash pins REV-130 CODEX-130-001.
// IsTestCaseID only requires the "tc-" prefix, so "tc-foo--bar" is a linkable
// test case and wiring.Write happily produces tc-foo--bar--bats.wiring.yaml.
// ParseFilename splits on the FIRST "--" and reads that as testcase "tc-foo",
// framework "bar--bats" -- so scoping by it made a refresh of tc-foo--bar find
// nothing, and made a refresh that included tc-foo report this record as an
// identity mismatch. Scoped matching takes the LONGEST requested ID instead.
func TestDiscoverForScope_TCIDContainingDoubleDash(t *testing.T) {
	root := t.TempDir()
	rec := sampleRecord()
	rec.TestCase = "tc-foo--bar"
	rec.Framework = "bats"
	if _, err := Write(root, rec); err != nil {
		t.Fatal(err)
	}

	results, err := DiscoverForScope(root, []string{"tc-foo--bar"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected the record to be found under its real ID, got %d results", len(results))
	}
	if results[0].TCFromName != "tc-foo--bar" || results[0].FWFromName != "bats" {
		t.Fatalf("wrong split: tc=%q fw=%q", results[0].TCFromName, results[0].FWFromName)
	}
	if results[0].Err != nil {
		t.Fatalf("a correctly-named record must not be an identity mismatch: %v", results[0].Err)
	}

	// The framework filter works on the correctly-split framework.
	narrowed, err := DiscoverForScope(root, []string{"tc-foo--bar"}, "bats")
	if err != nil {
		t.Fatal(err)
	}
	if len(narrowed) != 1 {
		t.Fatalf("--framework bats must still match, got %d", len(narrowed))
	}

	// The longest ID wins when a shorter one is also in scope.
	both, err := DiscoverForScope(root, []string{"tc-foo", "tc-foo--bar"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(both) != 1 || both[0].TCFromName != "tc-foo--bar" || both[0].Err != nil {
		t.Fatalf("longest-ID match expected, got %+v", both)
	}
}

// TestSplitStemForScope covers the matcher directly, including the boundaries
// that keep it from inventing identities.
func TestSplitStemForScope(t *testing.T) {
	scope := map[string]bool{"tc-a": true, "tc-a--b": true, "tc-long": true}

	cases := []struct {
		stem   string
		wantTC string
		wantFW string
		wantOK bool
	}{
		{"tc-a--bats", "tc-a", "bats", true},
		{"tc-a--b--bats", "tc-a--b", "bats", true},   // longest ID wins
		{"tc-long--playwright", "tc-long", "playwright", true},
		{"tc-zzz--bats", "", "", false},              // not in scope
		{"tc-a", "", "", false},                      // no separator
		{"tc-a--", "", "", false},                    // empty framework
		{"--bats", "", "", false},                    // empty test case
	}
	for _, c := range cases {
		tc, fw, ok := splitStemForScope(c.stem, scope)
		if ok != c.wantOK || tc != c.wantTC || fw != c.wantFW {
			t.Errorf("splitStemForScope(%q) = (%q, %q, %v), want (%q, %q, %v)",
				c.stem, tc, fw, ok, c.wantTC, c.wantFW, c.wantOK)
		}
	}
}
