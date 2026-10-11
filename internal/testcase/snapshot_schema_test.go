package testcase

import (
	"strings"
	"sync/atomic"
	"testing"
)

func TestSnapshot_StrictSchemaFailureIsUnprovable(t *testing.T) {
	root, cases := casesDirFor(t)
	writeRaw(t, cases, "feat/tc-12345678-missing-category.md", strictV2Fixture("tc-12345678", ""))

	snap := mustSnapshot(t, root)
	resolution := snap.Resolve("tc-12345678")
	if resolution.Outcome != OutcomeUnprovable {
		t.Fatalf("Resolve outcome = %v, want Unprovable", resolution.Outcome)
	}
	if resolution.Reason != ReasonMalformed {
		t.Fatalf("reason = %v, want ReasonMalformed", resolution.Reason)
	}
	if resolution.SchemaFailure == nil || resolution.SchemaFailure.Kind != SchemaFailureMissingTestCategory {
		t.Fatalf("schema failure = %#v, want missing test category", resolution.SchemaFailure)
	}
	want := "ownership of test case ID 'tc-12345678' cannot be proved from gtms/test/cases/feat/tc-12345678-missing-category.md: set test_type to a non-empty category in gtms/test/cases/feat/tc-12345678-missing-category.md, or remove the file."
	if got := OwnershipErrorFor(resolution).Error(); got != want {
		t.Fatalf("ownership error = %q, want %q", got, want)
	}

	controlRoot, controlCases := casesDirFor(t)
	writeRaw(t, controlCases, "feat/tc-87654321-duplicate.md", string(strictV2Document(
		"\"bad\\x1b\\nkey\": first\n\"bad\\x1b\\nkey\": second\n")))
	controlResolution := mustSnapshot(t, controlRoot).Resolve("tc-87654321")
	if controlResolution.Outcome != OutcomeUnprovable {
		t.Fatalf("control-key Resolve outcome = %v, want Unprovable", controlResolution.Outcome)
	}
	controlDiagnostic := OwnershipErrorFor(controlResolution).Error()
	if !strings.Contains(controlDiagnostic, "remove duplicate YAML key 'bad??key'") {
		t.Fatalf("control-key ownership error = %q, want sanitized repair sentence", controlDiagnostic)
	}
	assertNoControlBytes(t, controlDiagnostic)
}

func TestSnapshot_SchemaFailureRetainsParsedIdentityForRelevance(t *testing.T) {
	root, cases := casesDirFor(t)
	writeRaw(t, cases, "feat/renamed-owner.md", strictV2Fixture("tc-23456789", ""))

	snap := mustSnapshot(t, root)
	resolution := snap.Resolve("tc-23456789")
	if resolution.Outcome != OutcomeUnprovable {
		t.Fatalf("renamed schema-invalid owner resolved as %v, want Unprovable", resolution.Outcome)
	}
	if !strings.Contains(OwnershipErrorFor(resolution).Error(), "ownership of test case ID 'tc-23456789'") {
		t.Fatalf("parsed identity did not select the ID-form envelope: %v", OwnershipErrorFor(resolution))
	}

	_, unprovable := snap.SelectFolder("gtms/test/cases/feat", false)
	if len(unprovable) != 1 || unprovable[0].BodyID != "tc-23456789" {
		t.Fatalf("folder unprovable = %#v, want retained parsed identity", unprovable)
	}
	if got := UnprovableInScope("gtms/test/cases/feat", unprovable[0]).Error(); !strings.HasPrefix(got, "ownership of test case ID 'tc-23456789'") {
		t.Fatalf("folder diagnostic did not select the ID-form envelope: %q", got)
	}
}

func TestSnapshot_DuplicateNonIdentityKeyRetainsIdentityForRelevance(t *testing.T) {
	root, cases := casesDirFor(t)
	writeRaw(t, cases, "feat/renamed-owner.md", string(strictV2Document(
		"title: first\ntitle: second\n")))

	resolution := mustSnapshot(t, root).Resolve("tc-abcdef12")
	if resolution.Outcome != OutcomeUnprovable {
		t.Fatalf("renamed duplicate-key owner resolved as %v, want Unprovable", resolution.Outcome)
	}
	if resolution.SchemaFailure == nil || resolution.SchemaFailure.Kind != SchemaFailureDuplicateYAMLKey {
		t.Fatalf("schema failure = %#v, want duplicate YAML key", resolution.SchemaFailure)
	}
	if got := OwnershipErrorFor(resolution).Error(); !strings.Contains(got, "remove duplicate YAML key 'title'") {
		t.Fatalf("ownership error = %q, want duplicate-key repair sentence", got)
	}

	duplicateIDRoot, duplicateIDCases := casesDirFor(t)
	writeRaw(t, duplicateIDCases, "feat/renamed-owner.md", string(strictV2Document(
		"test_case_id: tc-fedcba98\n")))
	duplicateIDSnapshot := mustSnapshot(t, duplicateIDRoot)
	if got := duplicateIDSnapshot.Resolve("tc-abcdef12").Outcome; got != OutcomeNotFound {
		t.Fatalf("first duplicated identity resolved as %v, want NotFound", got)
	}
	if got := duplicateIDSnapshot.Resolve("tc-fedcba98").Outcome; got != OutcomeNotFound {
		t.Fatalf("second duplicated identity resolved as %v, want NotFound", got)
	}
	_, unprovable := duplicateIDSnapshot.SelectFolder("gtms/test/cases/feat", false)
	if len(unprovable) != 1 || unprovable[0].BodyID != "" || unprovable[0].Reason != ReasonMalformed {
		t.Fatalf("duplicated identity candidate = %#v, want identity-less malformed", unprovable)
	}
}

func TestSnapshot_OneReadOneParseAdmission(t *testing.T) {
	root, cases := casesDirFor(t)
	writeRaw(t, cases, "feat/renamed.md", "---\ntest_case_id: tc-34567890\ntitle: [unterminated\n")

	before := atomic.LoadInt64(&candidateReads)
	snap := mustSnapshot(t, root)
	if got := atomic.LoadInt64(&candidateReads) - before; got != 1 {
		t.Fatalf("physical candidate reads = %d, want 1", got)
	}
	if len(snap.candidates) != 1 || !snap.candidates[0].Unprovable {
		t.Fatalf("single decoded carrier = %#v, want one Unprovable candidate", snap.candidates)
	}
}

func strictV2Fixture(id, testType string) string {
	doc := "---\n" +
		"gtms_schema: test-case/v2\n" +
		"type: Test Case Spec\n"
	if testType != "" {
		doc += "test_type: " + testType + "\n"
	}
	return doc +
		"test_case_id: " + id + "\n" +
		"title: Snapshot fixture\n" +
		"requirement: ENH-219\n" +
		"priority: Medium\n" +
		"created: 2026-09-26\n" +
		"---\n"
}
