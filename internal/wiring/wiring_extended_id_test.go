package wiring

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
)

func writeWiringFile(t *testing.T, root, name, body string) {
	t.Helper()
	dir := layout.WiringDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// BUG-188 Gap 1: an extended-id wiring filename whose OWN separator contains "--"
// (tc-x--legacy--bats) must be parsed as testcase tc-x--legacy / framework bats
// from the record BODY, not split at the first "--". The old ParseFilename split
// made DiscoverAll flag it as an identity mismatch and drop it.
func TestDiscoverAll_ExtendedIDReconstructsFromBody(t *testing.T) {
	root := t.TempDir()
	body := "testcase: tc-77777777--legacy\ntestcase-hash: 1111111111111111\nframework: bats\nadapter: bats-runner\nartefact: test/x.bats\nartefact-hash: pending\n"
	writeWiringFile(t, root, "tc-77777777--legacy--bats.wiring.yaml", body)

	results, err := DiscoverAll(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Err != nil {
		t.Fatalf("extended-id wiring must not be an identity mismatch: %v", r.Err)
	}
	if r.Record == nil {
		t.Fatal("extended-id wiring must be a selectable record")
	}
	if r.Record.TestCase != "tc-77777777--legacy" || r.Record.Framework != "bats" {
		t.Fatalf("identity from body: got testcase=%q framework=%q", r.Record.TestCase, r.Record.Framework)
	}
}

// The shorter and extended siblings coexist and each resolves to its OWN record.
func TestDiscoverAll_PrefixSiblingsAreDistinctRecords(t *testing.T) {
	root := t.TempDir()
	writeWiringFile(t, root, "tc-77777777--bats.wiring.yaml",
		"testcase: tc-77777777\ntestcase-hash: 1111111111111111\nframework: bats\nadapter: bats-runner\nartefact: test/a.bats\nartefact-hash: pending\n")
	writeWiringFile(t, root, "tc-77777777--legacy--bats.wiring.yaml",
		"testcase: tc-77777777--legacy\ntestcase-hash: 2222222222222222\nframework: bats\nadapter: bats-runner\nartefact: test/b.bats\nartefact-hash: pending\n")

	results, err := DiscoverAll(root)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]*DiscoveryResult{}
	for i := range results {
		if results[i].Err != nil {
			t.Fatalf("unexpected error for %s: %v", results[i].Path, results[i].Err)
		}
		if results[i].Record != nil {
			byID[results[i].Record.TestCase] = &results[i]
		}
	}
	if byID["tc-77777777"] == nil || byID["tc-77777777--legacy"] == nil {
		t.Fatalf("both siblings must be distinct records; got keys %v", keysOf(byID))
	}
}

func keysOf(m map[string]*DiscoveryResult) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
