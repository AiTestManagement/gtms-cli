package testcase

import (
	"strings"
	"testing"
)

// REV-158 CLAUDE-007: an Unprovable candidate selected by SCOPE has no id -- that
// is precisely why it is unprovable -- so OwnershipError.ID must not be
// back-filled with its path. Four call sites did exactly that, and Error()
// rendered "ownership of test case ID 'gtms/test/cases/feat/README.md' cannot be
// proved from gtms/test/cases/feat/README.md": a path labelled as a TC id, then
// repeated, burying the repair sentence. ADR-024 s7 requires typed diagnostics
// whose fields carry the right facts.
func TestOwnershipError_ScopeDerivedUnprovableDoesNotLabelAPathAsAnID(t *testing.T) {
	c := Candidate{LogicalRel: "gtms/test/cases/feat/tc-11111111-broken.md", Unprovable: true, Reason: ReasonMissingID}
	oe := UnprovableInScope("gtms/test/cases/feat", c)

	if oe.ID != "" {
		t.Errorf("ID = %q, want empty: a scope-derived candidate has no TC id to report", oe.ID)
	}
	if oe.Scope != "gtms/test/cases/feat" {
		t.Errorf("Scope = %q, want the folder that selected the candidate", oe.Scope)
	}

	msg := oe.Error()
	if strings.Contains(msg, "test case ID '"+c.LogicalRel+"'") {
		t.Errorf("diagnostic labels a path as a test case ID:\n%s", msg)
	}
	if strings.Count(msg, c.LogicalRel) != 2 {
		// once as the source, once inside the repair sentence -- never three times.
		t.Errorf("path should appear exactly twice (source + repair), got %d:\n%s",
			strings.Count(msg, c.LogicalRel), msg)
	}
	if !strings.Contains(msg, "gtms/test/cases/feat/") {
		t.Errorf("diagnostic must name the scope that selected the candidate:\n%s", msg)
	}
	// The clause acceptance specs grep for is unchanged in both forms.
	if !strings.Contains(msg, "cannot be proved from "+c.LogicalRel+":") {
		t.Errorf("the shared 'cannot be proved from <path>:' clause must be preserved:\n%s", msg)
	}
	if !strings.Contains(msg, "add a test_case_id") {
		t.Errorf("the repair sentence for the specific failure must survive:\n%s", msg)
	}
}

// An id-derived Unprovable still reports the id, unchanged.
func TestOwnershipError_IDDerivedUnprovableStillNamesTheID(t *testing.T) {
	oe := &OwnershipError{
		ID: "tc-11111111", Outcome: OutcomeUnprovable,
		Path: "gtms/test/cases/feat/tc-11111111-broken.md", Reason: ReasonMalformed,
	}
	msg := oe.Error()
	if !strings.Contains(msg, "test case ID 'tc-11111111'") {
		t.Errorf("an id-derived diagnostic must still name the id:\n%s", msg)
	}
}

// A scope-derived error with no scope recorded still renders the cases root
// rather than an empty "under /".
func TestOwnershipError_ScopeDerivedWithoutScopeFallsBackToCasesRoot(t *testing.T) {
	oe := &OwnershipError{Outcome: OutcomeUnprovable, Path: "gtms/test/cases/x.md", Reason: ReasonUnreadable}
	if msg := oe.Error(); !strings.Contains(msg, "under gtms/test/cases/") {
		t.Errorf("want a cases-root fallback, got:\n%s", msg)
	}
}
