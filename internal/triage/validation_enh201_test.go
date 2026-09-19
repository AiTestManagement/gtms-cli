package triage

import (
	"path/filepath"
	"strings"
	"testing"
)

// ENH-201: semantic + identity validation added to Read, and the all-records
// enumerator ListAll. These guards refuse records that decode cleanly but are
// collectively nonsensical, and pin the single canonical enumeration path.

// validRecordBody returns a well-formed active version-1 record body for the
// given (tc, framework). Individual semantic tests mutate one field of this
// baseline to prove the guard that field trips.
func validRecordBody(tc, fw string) string {
	return "version: 1\n" +
		"test_case_id: " + tc + "\n" +
		"framework: " + fw + "\n" +
		"status: active\n" +
		"value: environment\n" +
		"classified_at: 2026-08-27T09:00:00Z\n" +
		"snapshot:\n" +
		"  task: task-11111111\n" +
		"  result: fail\n"
}

func TestReadSemanticValidation_AcceptsValidActiveAndResolved(t *testing.T) {
	root := t.TempDir()

	p := writeTriageFile(t, root, "tc-11111111--bats.triage.yaml", validRecordBody("tc-11111111", "bats"))
	if _, err := Read(root, p); err != nil {
		t.Fatalf("valid active record rejected: %v", err)
	}

	resolved := "version: 1\ntest_case_id: tc-22222222\nframework: bats\nstatus: resolved\nvalue: environment\nclassified_at: 2026-08-27T09:00:00Z\nresolved_at: 2026-08-27T10:00:00Z\nsnapshot:\n  task: t\n  result: pass\n"
	pr := writeTriageFile(t, root, "tc-22222222--bats.triage.yaml", resolved)
	if _, err := Read(root, pr); err != nil {
		t.Fatalf("valid resolved record rejected: %v", err)
	}
}

func TestReadSemanticValidation_RequiredFieldsAndVocabulary(t *testing.T) {
	root := t.TempDir()
	base := validRecordBody("tc-11111111", "bats")

	cases := []struct {
		name     string
		body     string
		wantFrag string
	}{
		{"empty value", strings.Replace(base, "value: environment\n", "value: \"\"\n", 1), "value"},
		{"missing test_case_id", strings.Replace(base, "test_case_id: tc-11111111\n", "test_case_id: \"\"\n", 1), "test_case_id"},
		{"missing classified_at", strings.Replace(base, "classified_at: 2026-08-27T09:00:00Z\n", "classified_at: \"\"\n", 1), "classified_at"},
		{"missing snapshot.task", strings.Replace(base, "  task: task-11111111\n", "  task: \"\"\n", 1), "snapshot.task"},
		{"missing snapshot.result", strings.Replace(base, "  result: fail\n", "  result: \"\"\n", 1), "snapshot.result"},
		{"bad status", strings.Replace(base, "status: active\n", "status: banana\n", 1), "status"},
		{"bad snapshot.result", strings.Replace(base, "  result: fail\n", "  result: exploded\n", 1), "snapshot.result"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := writeTriageFile(t, root, "tc-11111111--bats.triage.yaml", c.body)
			_, err := Read(root, p)
			if err == nil {
				t.Fatalf("expected malformed error for %s", c.name)
			}
			if !strings.Contains(err.Error(), c.wantFrag) || !strings.Contains(err.Error(), p) {
				t.Fatalf("error %q must name %q and the path", err.Error(), c.wantFrag)
			}
		})
	}
}

func TestReadSemanticValidation_LifecycleAgreement(t *testing.T) {
	root := t.TempDir()

	// Active record carrying resolved_at -> contradiction.
	activeWithResolved := strings.Replace(validRecordBody("tc-11111111", "bats"),
		"snapshot:\n", "resolved_at: 2026-08-27T10:00:00Z\nsnapshot:\n", 1)
	p := writeTriageFile(t, root, "tc-11111111--bats.triage.yaml", activeWithResolved)
	if _, err := Read(root, p); err == nil || !strings.Contains(err.Error(), "resolved_at") {
		t.Fatalf("expected active/resolved_at contradiction, got %v", err)
	}

	// Resolved record missing resolved_at -> contradiction.
	resolvedNoAt := "version: 1\ntest_case_id: tc-33333333\nframework: bats\nstatus: resolved\nvalue: environment\nclassified_at: 2026-08-27T09:00:00Z\nsnapshot:\n  task: t\n  result: pass\n"
	p2 := writeTriageFile(t, root, "tc-33333333--bats.triage.yaml", resolvedNoAt)
	if _, err := Read(root, p2); err == nil || !strings.Contains(err.Error(), "resolved_at") {
		t.Fatalf("expected resolved-without-resolved_at contradiction, got %v", err)
	}
}

func TestReadSemanticValidation_IdentityMatchesFilename(t *testing.T) {
	root := t.TempDir()

	// Body framework disagrees with filename framework.
	fwMismatch := validRecordBody("tc-66666666", "playwright")
	p := writeTriageFile(t, root, "tc-66666666--bats.triage.yaml", fwMismatch)
	if _, err := Read(root, p); err == nil ||
		!strings.Contains(err.Error(), "framework") ||
		!strings.Contains(err.Error(), "bats") ||
		!strings.Contains(err.Error(), "playwright") {
		t.Fatalf("expected framework identity mismatch naming both, got %v", err)
	}

	// Body test_case_id disagrees with filename test case.
	tcMismatch := validRecordBody("tc-99999999", "bats")
	p2 := writeTriageFile(t, root, "tc-66666666--bats.triage.yaml", tcMismatch)
	if _, err := Read(root, p2); err == nil ||
		!strings.Contains(err.Error(), "test_case_id") {
		t.Fatalf("expected test_case_id identity mismatch, got %v", err)
	}
}

func TestReadSemanticValidation_OpaqueTimestampAndUnconfiguredValue(t *testing.T) {
	root := t.TempDir()

	// A value that is merely unconfigured is NOT malformed, and a non-RFC3339
	// classified_at is not parse-gated.
	body := "version: 1\ntest_case_id: tc-11111111\nframework: bats\nstatus: active\nvalue: retired-value\nclassified_at: whenever-o-clock\nsnapshot:\n  task: t\n  result: skip\n"
	p := writeTriageFile(t, root, "tc-11111111--bats.triage.yaml", body)
	rec, err := Read(root, p)
	if err != nil {
		t.Fatalf("unconfigured value / opaque timestamp rejected: %v", err)
	}
	if rec.Value != "retired-value" || rec.Snapshot.Result != "skip" {
		t.Fatalf("round-trip mismatch: %+v", rec)
	}
}

func TestListAll(t *testing.T) {
	root := t.TempDir()
	writeTriageFile(t, root, "tc-22222222--bats.triage.yaml", "version: 1\n")
	writeTriageFile(t, root, "tc-22222222--playwright.triage.yaml", "version: 1\n")
	writeTriageFile(t, root, "tc-44444444--bats.triage.yaml", "version: 1\n") // orphan-shaped
	writeTriageFile(t, root, "not-a-record.txt", "ignore me")

	refs, err := ListAll(root)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(refs) != 3 {
		t.Fatalf("expected 3 records, got %d: %+v", len(refs), refs)
	}
	// RecordRef carries only a path: identity is established by Read from the
	// record body, because a test case ID may itself contain "--" and a
	// filename therefore cannot be split unambiguously.
	seen := map[string]bool{}
	for _, r := range refs {
		seen[filepath.Base(r.Path)] = true
	}
	for _, want := range []string{
		"tc-22222222--bats.triage.yaml",
		"tc-22222222--playwright.triage.yaml",
		"tc-44444444--bats.triage.yaml",
	} {
		if !seen[want] {
			t.Fatalf("missing %s in %+v", want, refs)
		}
	}

	// Missing triage directory -> nil, nil.
	none, err := ListAll(t.TempDir())
	if err != nil || none != nil {
		t.Fatalf("expected nil,nil for empty project, got %v,%v", none, err)
	}
}
