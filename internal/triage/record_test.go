package triage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
)

// writeFile is a small helper for placing hand-written records under the
// triage directory.
func writeTriageFile(t *testing.T, root, name, content string) string {
	t.Helper()
	dir := layout.TriageDir(root)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

func TestWriteReadRoundTrip(t *testing.T) {
	root := t.TempDir()
	rec := &Record{
		Version:      SchemaVersion,
		TestCaseID:   "tc-11111111",
		Framework:    "bats",
		Status:       StatusActive,
		Value:        "environment",
		Summary:      "creds expired",
		Defect:       "JIRA-789",
		ClassifiedAt: "2026-08-25T10:15:00Z",
		Snapshot: Snapshot{
			Task:                 "task-0a0f57cf",
			Result:               "fail",
			CompletedAt:          "2026-08-25T09:42:00Z",
			Artefact:             "test/probe.bats",
			ArtefactHash:         "aaaabbbbccccdddd",
			TestcaseHashAtTriage: "def4567890abcdef",
		},
	}
	path, err := Write(root, rec)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if filepath.Base(path) != "tc-11111111--bats.triage.yaml" {
		t.Fatalf("unexpected filename: %s", path)
	}
	got, err := Read(root, path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Value != "environment" || got.Snapshot.Result != "fail" || got.Snapshot.Task != "task-0a0f57cf" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestWriteOmitsEmptyOptionals(t *testing.T) {
	root := t.TempDir()
	rec := &Record{
		Version:      SchemaVersion,
		TestCaseID:   "tc-44444444",
		Framework:    "bats",
		Status:       StatusActive,
		Value:        "blocked",
		ClassifiedAt: "2026-08-25T10:15:00Z",
		Snapshot: Snapshot{
			Task:                 "task-1",
			Result:               "error",
			Artefact:             "test/thin.bats",
			TestcaseHashAtTriage: "0123456789abcdef",
		},
	}
	path, err := Write(root, rec)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	data, _ := os.ReadFile(path)
	s := string(data)
	for _, k := range []string{"summary:", "defect:", "resolved_at:", "completed_at:", "artefact_hash:"} {
		if strings.Contains(s, k) {
			t.Fatalf("expected key %q omitted, file:\n%s", k, s)
		}
	}
}

func TestReadVersionGuard(t *testing.T) {
	root := t.TempDir()
	body := "test_case_id: tc-66666666\nframework: %s\nstatus: active\nvalue: environment\nsnapshot:\n  task: t\n  result: fail\n"

	cases := []struct {
		name     string
		fw       string
		versLine string
		wantFrag string
	}{
		{"v2", "bats", "version: 2\n", "unsupported version 2"},
		{"missing", "playwright", "", "no version"},
		{"nan", "pester", "version: not-a-number\n", "malformed version"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			content := c.versLine + strings.Replace(body, "%s", c.fw, 1)
			p := writeTriageFile(t, root, "tc-66666666--"+c.fw+".triage.yaml", content)
			_, err := Read(root, p)
			if err == nil {
				t.Fatalf("expected error")
			}
			if !strings.Contains(err.Error(), c.wantFrag) || !strings.Contains(err.Error(), c.fw) {
				t.Fatalf("error %q missing %q or path frag %q", err.Error(), c.wantFrag, c.fw)
			}
		})
	}
}

func TestReadStrictUnknownField(t *testing.T) {
	root := t.TempDir()
	content := "version: 1\ntest_case_id: tc-66666666\nframework: cypress\nstatus: active\nvalue: environment\nseverity: high\nsnapshot:\n  task: t\n  result: fail\n"
	p := writeTriageFile(t, root, "tc-66666666--cypress.triage.yaml", content)
	_, err := Read(root, p)
	if err == nil {
		t.Fatalf("expected strict-decode error")
	}
	if !strings.Contains(err.Error(), "severity") || !strings.Contains(err.Error(), "cypress") {
		t.Fatalf("error %q must name the unknown key and the path", err.Error())
	}
}

func TestListForTC(t *testing.T) {
	root := t.TempDir()
	writeTriageFile(t, root, "tc-22222222--bats.triage.yaml", "version: 1\n")
	writeTriageFile(t, root, "tc-22222222--playwright.triage.yaml", "version: 1\n")
	writeTriageFile(t, root, "tc-33333333--bats.triage.yaml", "version: 1\n")

	refs, err := ListForTC(root, "tc-22222222")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("expected 2 records, got %d", len(refs))
	}
	seen := map[string]bool{}
	for _, r := range refs {
		seen[r.Framework] = true
	}
	if !seen["bats"] || !seen["playwright"] {
		t.Fatalf("missing framework refs: %+v", refs)
	}

	// No records for an unrelated TC, and a missing dir returns nil not error.
	none, err := ListForTC(t.TempDir(), "tc-99999999")
	if err != nil || none != nil {
		t.Fatalf("expected nil,nil for empty project got %v,%v", none, err)
	}
}
