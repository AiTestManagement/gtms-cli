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
