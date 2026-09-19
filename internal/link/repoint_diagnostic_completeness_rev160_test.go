package link

import (
	"strings"
	"testing"
)

// REV-160 / acceptance tc-68c17c1a. A project-wide repoint refusal must be
// DIAGNOSTICALLY COMPLETE before it is atomic.
//
// repointSelected classifies every discovered record in one read-only pass, but
// it used to refuse on the ownerless group alone and return before RepointBatch
// ever saw the malformed group. A run that hit both kinds therefore named only
// the ownerless files, and the malformed ones vanished from the report -- the
// malformed-as-absence failure ADR-024 s1 forbids, on the one command whose job
// in this state is to account for every record it will not touch.
//
// The fix is NOT to let --all continue per record. It is to name all three
// rejection groups in a single refusal, still before a single write.
//
// This is the discriminator for that behaviour: revert the refusal to the
// ownerless-only early return and it fails, because the malformed records
// disappear from the message.
func TestRepointAll_RefusalNamesEveryUnrepointableRecord(t *testing.T) {
	root := seedRepointProject(t)

	// Owners exist for 61/62/63, so 62 and 63 are attributable-but-malformed
	// rather than ownerless. 99999999 has no spec anywhere.
	writeSpecAt(t, root, "scope/tc-66666661-probe.md", "tc-66666661")
	writeSpecAt(t, root, "scope/tc-66666662-probe.md", "tc-66666662")
	writeSpecAt(t, root, "outside/tc-66666663-probe.md", "tc-66666663")

	// 1. A valid record that WOULD otherwise be repointed (adapter matches).
	good := writeOwnedWiring(t, root, "tc-66666661")
	// 2. Malformed TC records, correct filename shape, unparseable body.
	bad62 := writeWiringRaw(t, root, "tc-66666662--bats.wiring.yaml", "[ this is not valid yaml\n")
	bad63 := writeWiringRaw(t, root, "tc-66666663--bats.wiring.yaml", "[ this is not valid yaml\n")
	// 3. A filename that carries no test case identity at all.
	unnamed := writeWiringRaw(t, root, "not-a-valid-identity.wiring.yaml", "testcase: nope\n")
	// 4. A well-formed record whose identity nothing physically owns.
	orphan := writeOwnedWiring(t, root, "tc-99999999")
	// 5. REV-160 F2. A valid, owned record on a DIFFERENT adapter: legitimately
	//    SKIPPED, not unrepointable. Without this arm the test cannot tell a
	//    complete refusal from one that names every record it did not select --
	//    an implementation that did the latter passed the first version of this
	//    test and the whole package. In a real project that is 500 healthy
	//    records reported as broken.
	writeSpecAt(t, root, "scope/tc-66666664-probe.md", "tc-66666664")
	otherAdapter := writeWiringRaw(t, root, "tc-66666664--bats.wiring.yaml",
		"testcase: tc-66666664\ntestcase-hash: 1111222233334444\nframework: bats\n"+
			"adapter: some-other-runner\nartefact: tests/a.bats\nartefact-hash: aaaabbbbccccdddd\n")

	files := map[string]string{
		"tc-66666661 (valid)":     good,
		"tc-66666662 (malformed)": bad62,
		"tc-66666663 (malformed)": bad63,
		"not-a-valid-identity":    unnamed,
		"tc-99999999 (ownerless)": orphan,
		"tc-66666664 (skipped)":   otherAdapter,
	}
	before := make(map[string]string, len(files))
	for label, path := range files {
		before[label] = rawSHA256(t, path)
	}

	// Not a dry run: the refusal must hold on the mutating path.
	_, err := RepointAll(root, withScopeAuth(t, root, "", true, RepointOptions{
		Config:      repointTestConfig(),
		FromAdapter: "bats-runner",
		NewAdapter:  "bats-new",
	}))

	if err == nil {
		t.Fatal("RepointAll proceeded despite malformed, unnamed and ownerless wiring; --all is all-or-nothing")
	}
	msg := err.Error()

	// Every record the scan could not clear must be named, whatever its group.
	for _, want := range []string{
		"tc-66666662--bats.wiring.yaml",
		"tc-66666663--bats.wiring.yaml",
		"not-a-valid-identity.wiring.yaml",
		"tc-99999999--bats.wiring.yaml",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal omits %s -- a record it refused to touch is absent from the diagnostic.\n"+
				"    An operator cannot act on what the refusal does not name, and has to re-run to\n"+
				"    discover the next one. Full message:\n%s", want, msg)
		}
	}

	// Healthy records are not slandered as unrepointable -- neither the one that
	// would be repointed nor the one legitimately skipped for its adapter.
	for _, healthy := range []string{
		"tc-66666661--bats.wiring.yaml", // selected for repoint
		"tc-66666664--bats.wiring.yaml", // valid and owned, just on another adapter
	} {
		if strings.Contains(msg, healthy) {
			t.Errorf("refusal names the VALID record %s as unrepointable.\n"+
				"    Naming every record that was not selected is not diagnostic completeness:\n"+
				"    a project whose records mostly sit on another adapter would report them all\n"+
				"    as broken. Full message:\n%s", healthy, msg)
		}
	}

	// Nothing was rewritten -- including the record that would have been repointed.
	for label, path := range files {
		if got := rawSHA256(t, path); got != before[label] {
			t.Errorf("%s was mutated despite the refusal; --all must write nothing when any record is unrepointable", label)
		}
	}
}

// REV-160, review point 2. The `--` sibling pair under --all. BUG-188's
// wiring.ParseFilename split a stem at the FIRST "--", so a malformed
// tc-77777777--legacy--bats.wiring.yaml was attributed to tc-77777777 and the
// refusal blamed an unrelated identity -- the cross-identity coupling this branch
// exists to remove. Nothing in internal/link covered the pair under a project-wide
// refusal, so the correct behaviour was unpinned.
func TestRepointAll_RefusalDoesNotBlameAPrefixSibling(t *testing.T) {
	root := seedRepointProject(t)
	writeSpecAt(t, root, "feat/tc-77777777-short.md", "tc-77777777")
	writeSpecAt(t, root, "feat/tc-77777777--legacy-long.md", "tc-77777777--legacy")

	short := writeOwnedWiring(t, root, "tc-77777777")
	long := writeWiringRaw(t, root, "tc-77777777--legacy--bats.wiring.yaml", "[ this is not valid yaml\n")

	beforeShort := rawSHA256(t, short)
	beforeLong := rawSHA256(t, long)

	_, err := RepointAll(root, withScopeAuth(t, root, "", true, RepointOptions{
		Config:      repointTestConfig(),
		FromAdapter: "bats-runner",
		NewAdapter:  "bats-new",
	}))
	if err == nil {
		t.Fatal("RepointAll proceeded despite a malformed record")
	}
	msg := err.Error()

	if !strings.Contains(msg, "tc-77777777--legacy--bats.wiring.yaml") {
		t.Errorf("refusal omits the malformed extended-id record:\n%s", msg)
	}
	if strings.Contains(msg, "\n    no proven test case owner: tc-77777777--bats.wiring.yaml") ||
		strings.Contains(msg, ", tc-77777777--bats.wiring.yaml") {
		t.Errorf("refusal blames the SHORT sibling tc-77777777 for its longer sibling's corruption.\n"+
			"    tc-77777777 and tc-77777777--legacy are DIFFERENT test cases (BUG-188):\n%s", msg)
	}
	if got := rawSHA256(t, short); got != beforeShort {
		t.Error("the healthy short sibling was mutated despite the refusal")
	}
	if got := rawSHA256(t, long); got != beforeLong {
		t.Error("the malformed record was mutated despite the refusal")
	}
}
