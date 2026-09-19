package link

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aitestmanagement/gtms-cli/internal/config"
)

// REV-159 CLAUDE-004 -- single-TC repoint could rewrite a DIFFERENT test case's
// wiring record while reporting success for the one you asked for.
//
// wiring.Find locates a record by PATH: it builds tc-<id>--<framework>.wiring.yaml
// and reads whatever is there. Nothing checked that the file's BODY declares the
// identity requested, and RepointRecord recomputes its WRITE path from
// rec.TestCase. So tc-x's file, declaring tc-y in its body, sent the write to
// tc-y's record.
//
// The corruption is the finding, so asserting the error alone does not
// discriminate: these tests also prove the other test case's record is
// BYTE-IDENTICAL afterwards, hashed raw with no line-ending normalisation.

// rawSHA256 hashes the file's exact bytes. Deliberately NOT pipeline.HashFile,
// which strips \r -- a normalised hash cannot see a write that only changed line
// endings, and "untouched" here means untouched.
func rawSHA256(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func writeWiringRaw(t *testing.T, root, name, body string) string {
	t.Helper()
	dir := filepath.Join(root, "gtms", "automation", "wiring")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir wiring: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write wiring: %v", err)
	}
	return path
}

// repointTestConfig provides two bats execute adapters so a repoint from one to
// the other is a legitimate, resolvable operation.
func repointTestConfig() *config.Config {
	return &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"bats-runner": {Mode: "sync", Command: "true", Framework: "bats"},
				"bats-new":    {Mode: "sync", Command: "true", Framework: "bats"},
			},
		},
	}
}

func seedForeignIdentityRepoint(t *testing.T) (root, foreignPath, requestedPath string) {
	t.Helper()
	root = t.TempDir()

	art := filepath.Join(root, "tests")
	if err := os.MkdirAll(art, 0o755); err != nil {
		t.Fatalf("mkdir tests: %v", err)
	}
	if err := os.WriteFile(filepath.Join(art, "a.bats"), []byte("@test x { true; }\n"), 0o644); err != nil {
		t.Fatalf("write artefact: %v", err)
	}

	// A spec for each identity, so ownership resolves and the preflight is not
	// what refuses -- the identity mismatch must be.
	cases := filepath.Join(root, "gtms", "test", "cases")
	if err := os.MkdirAll(cases, 0o755); err != nil {
		t.Fatalf("mkdir cases: %v", err)
	}
	for _, id := range []string{"tc-aaaaaaaa", "tc-bbbbbbbb"} {
		body := "---\ntest_case_id: " + id + "\ntitle: \"t\"\n---\n\nbody\n"
		if err := os.WriteFile(filepath.Join(cases, id+"-spec.md"), []byte(body), 0o644); err != nil {
			t.Fatalf("write spec: %v", err)
		}
	}

	// The file NAMED for tc-aaaaaaaa, whose body declares tc-bbbbbbbb.
	requestedPath = writeWiringRaw(t, root, "tc-aaaaaaaa--bats.wiring.yaml",
		"testcase: tc-bbbbbbbb\ntestcase-hash: 1111222233334444\nframework: bats\nadapter: bats-runner\nartefact: tests/a.bats\nartefact-hash: aaaabbbbccccdddd\n")

	// tc-bbbbbbbb's REAL record, with the same six field values -- which is what
	// let the stale-selection re-read pass and the write land here.
	foreignPath = writeWiringRaw(t, root, "tc-bbbbbbbb--bats.wiring.yaml",
		"testcase: tc-bbbbbbbb\ntestcase-hash: 1111222233334444\nframework: bats\nadapter: bats-runner\nartefact: tests/a.bats\nartefact-hash: aaaabbbbccccdddd\n")

	return root, foreignPath, requestedPath
}

// The finding: refuse, and leave the foreign record byte-identical.
func TestRepointSingle_RefusesForeignIdentityAndLeavesTheOtherRecordUntouched(t *testing.T) {
	root, foreignPath, requestedPath := seedForeignIdentityRepoint(t)

	foreignBefore := rawSHA256(t, foreignPath)
	requestedBefore := rawSHA256(t, requestedPath)

	_, err := RepointSingle(root, "tc-aaaaaaaa", withSingleAuth(t, root, "tc-aaaaaaaa", RepointOptions{
		Config:     repointTestConfig(),
		NewAdapter: "bats-new",
	}))

	// 1. a refusal, not a success
	if err == nil {
		t.Fatal("RepointSingle succeeded: it read tc-aaaaaaaa's file, which declares tc-bbbbbbbb, and would write into tc-bbbbbbbb's record while reporting success for tc-aaaaaaaa")
	}
	// 2. the typed error the link path already returns
	var wme *WiringIdentityMismatchError
	if !errors.As(err, &wme) {
		t.Fatalf("error is %T (%v), want *WiringIdentityMismatchError -- the CLI keys the no---force hint off this type", err, err)
	}
	if wme.DeclaredTC != "tc-bbbbbbbb" || wme.RequestedTC != "tc-aaaaaaaa" {
		t.Fatalf("mismatch fields = declared %q requested %q, want tc-bbbbbbbb / tc-aaaaaaaa", wme.DeclaredTC, wme.RequestedTC)
	}
	// 3. the OTHER test case's record is byte-identical
	if got := rawSHA256(t, foreignPath); got != foreignBefore {
		t.Fatalf("tc-bbbbbbbb's wiring record was modified by a repoint of tc-aaaaaaaa (sha %s -> %s): this is the data corruption the finding is about", foreignBefore, got)
	}
	// 4. and nothing was written to the requested path either
	if got := rawSHA256(t, requestedPath); got != requestedBefore {
		t.Fatalf("tc-aaaaaaaa's file changed (sha %s -> %s); a refused repoint writes nothing", requestedBefore, got)
	}
}

// The guard lives in the CORE package, so it holds for every caller and not only
// the Cobra wrapper -- the same lesson REV-158 CODEX-005 recorded for RepointAll.
// --from-adapter must not route around it either: it is a precondition on a
// record we have no authority over.
func TestRepointSingle_ForeignIdentityRefusedWithFromAdapterToo(t *testing.T) {
	root, foreignPath, _ := seedForeignIdentityRepoint(t)
	foreignBefore := rawSHA256(t, foreignPath)

	_, err := RepointSingle(root, "tc-aaaaaaaa", withSingleAuth(t, root, "tc-aaaaaaaa", RepointOptions{
		Config:      repointTestConfig(),
		FromAdapter: "bats-runner",
		NewAdapter:  "bats-new",
	}))

	var wme *WiringIdentityMismatchError
	if !errors.As(err, &wme) {
		t.Fatalf("error is %T (%v), want *WiringIdentityMismatchError", err, err)
	}
	if got := rawSHA256(t, foreignPath); got != foreignBefore {
		t.Fatal("tc-bbbbbbbb's record was modified via the --from-adapter path")
	}
}

// A matching record still repoints: the guard must refuse foreign identities, not
// break the command.
func TestRepointSingle_MatchingIdentityStillRepoints(t *testing.T) {
	root, _, _ := seedForeignIdentityRepoint(t)

	res, err := RepointSingle(root, "tc-bbbbbbbb", withSingleAuth(t, root, "tc-bbbbbbbb", RepointOptions{
		Config:     repointTestConfig(),
		NewAdapter: "bats-new",
	}))
	if err != nil {
		t.Fatalf("RepointSingle on a record whose body matches its name failed: %v", err)
	}
	if res.NewAdapter != "bats-new" {
		t.Fatalf("new adapter = %q, want bats-new", res.NewAdapter)
	}
}

// REV-159 CODEX-005 -- the diagnostic renders repository-controlled fields, and
// wiring.Read validates identity fields only for non-emptiness. Control
// characters must not reach the terminal (ADR-024 s7).
func TestWiringIdentityMismatchError_SanitisesControlCharacters(t *testing.T) {
	e := &WiringIdentityMismatchError{
		Path:        "gtms/automation/wiring/tc-a\x1b[2J--bats.wiring.yaml",
		DeclaredTC:  "tc-b\x1b]0;pwned\x07",
		DeclaredFW:  "bats\r\n",
		RequestedTC: "tc-a",
		RequestedFW: "bats",
	}
	msg := e.Error()
	for _, bad := range []string{"\x1b", "\x07", "\r", "\n"} {
		if strings.Contains(msg, bad) {
			t.Fatalf("diagnostic still carries %q: a hand-edited wiring record can inject ANSI/OSC sequences into gtms link output\n%q", bad, msg)
		}
	}
	if !strings.Contains(msg, "tc-b") || !strings.Contains(msg, "tc-a") {
		t.Fatalf("sanitising destroyed the useful content: %q", msg)
	}
}
