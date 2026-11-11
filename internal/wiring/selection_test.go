package wiring

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func writeRawWiring(t *testing.T, root, fileTC, fileFW string, body *WiringRecord) string {
	t.Helper()
	p, err := Path(root, fileTC, fileFW)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	raw, err := yaml.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, raw, 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func selectionBody(tc, fw string) *WiringRecord {
	return &WiringRecord{
		TestCase: tc, TestCaseHash: "0011223344556677", Framework: fw,
		Adapter: "bats-runner", Artefact: "tests/x.bats", ArtefactHash: "aabbccddeeff0011",
	}
}

// REV-232 CODEX-001: FindAllForTCSelections reports the selected file and the
// filename's framework, not the body's, so a later writer can check the body.
func TestFindAllForTCSelections_FrameworkFromFilename(t *testing.T) {
	root := t.TempDir()
	p := writeRawWiring(t, root, "tc-12345678", "bats", selectionBody("tc-12345678", "playwright"))

	sels, err := FindAllForTCSelections(root, "tc-12345678")
	if err != nil {
		t.Fatal(err)
	}
	if len(sels) != 1 {
		t.Fatalf("got %d selections, want 1", len(sels))
	}
	sel := sels[0]
	if sel.Path != p || sel.TestCase != "tc-12345678" || sel.Framework != "bats" {
		t.Fatalf("selection = %+v, want path %s, tc-12345678, bats", sel, p)
	}
	if err := VerifySelection(root, sel); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("VerifySelection = %v, want ErrIdentityMismatch for a body framework mismatch", err)
	}
}

func TestVerifySelection(t *testing.T) {
	root := t.TempDir()
	good := writeRawWiring(t, root, "tc-12345678", "bats", selectionBody("tc-12345678", "bats"))
	other, err := Path(root, "tc-12345678", "playwright")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		sel     Selection
		wantErr bool
	}{
		{name: "consistent", sel: Selection{Record: selectionBody("tc-12345678", "bats"), Path: good, TestCase: "tc-12345678", Framework: "bats"}},
		{name: "body test case differs", sel: Selection{Record: selectionBody("tc-87654321", "bats"), Path: good, TestCase: "tc-12345678", Framework: "bats"}, wantErr: true},
		{name: "body framework differs", sel: Selection{Record: selectionBody("tc-12345678", "playwright"), Path: good, TestCase: "tc-12345678", Framework: "bats"}, wantErr: true},
		{name: "path not canonical for identity", sel: Selection{Record: selectionBody("tc-12345678", "bats"), Path: other, TestCase: "tc-12345678", Framework: "bats"}, wantErr: true},
		{name: "unsafe expected framework", sel: Selection{Record: selectionBody("tc-12345678", "bats"), Path: good, TestCase: "tc-12345678", Framework: "../bats"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := VerifySelection(root, tc.sel)
			if tc.wantErr && !errors.Is(err, ErrIdentityMismatch) {
				t.Fatalf("got %v, want ErrIdentityMismatch", err)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("got %v, want nil", err)
			}
		})
	}
}
