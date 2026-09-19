package testcase

import (
	"os"
	"path/filepath"
	"testing"
)

// REV-159 CLAUDE-002 -- ADR-024 s3 requires scope comparison to follow the HOST
// filesystem's case semantics. Plain string equality and unconditional case
// folding are both named as insufficient.
//
// These tests cannot force a host to be case-insensitive, and Linux CI only ever
// exercises the case-sensitive branch. So each one PROBES the host, asserts the
// behaviour that host must produce, and LOGS which branch actually ran -- the
// same shape tc-a85a905e uses. A green run on Linux is therefore evidence for the
// case-sensitive contract only, and says so out loud.

// hostIsCaseInsensitive reports whether dir's filesystem resolves a differently
// cased name to the same directory. It asks the filesystem rather than guessing
// from runtime.GOOS -- a case-sensitive volume on macOS and a case-insensitive
// one on Linux both exist.
func hostIsCaseInsensitive(t *testing.T, dir string) bool {
	t.Helper()
	probe := filepath.Join(dir, "GtmsCaseProbe")
	if err := os.MkdirAll(probe, 0o755); err != nil {
		t.Fatalf("mkdir probe: %v", err)
	}
	defer os.RemoveAll(probe)

	lower, err := os.Stat(filepath.Join(dir, "gtmscaseprobe"))
	if err != nil {
		return false
	}
	upper, err := os.Stat(probe)
	if err != nil {
		return false
	}
	return os.SameFile(lower, upper)
}

// The finding itself, on the SELECTION path that delete, reset, automate,
// execute and link --refresh all share.
func TestSelectFolder_FollowsHostCaseSemantics(t *testing.T) {
	root, cases := casesDirFor(t)
	writeSpecID(t, cases, "FeatureCase/tc-11111111-login.md", "tc-11111111")

	snap := mustSnapshot(t, root)

	// Correct spelling must always select the spec, on every host.
	ids, _ := snap.SelectFolder("gtms/test/cases/FeatureCase", false)
	if len(ids) != 1 || ids[0] != "tc-11111111" {
		t.Fatalf("exact-case scope selected %v, want [tc-11111111]", ids)
	}

	// The differently cased request is where hosts legitimately differ.
	ids, _ = snap.SelectFolder("gtms/test/cases/featurecase", false)

	if hostIsCaseInsensitive(t, cases) {
		t.Log("BRANCH: case-INSENSITIVE host -- this run is evidence for the macOS/Windows contract")
		if len(ids) != 1 || ids[0] != "tc-11111111" {
			t.Fatalf("differently cased scope selected %v, want [tc-11111111]: on a case-insensitive filesystem featurecase and FeatureCase are ONE logical scope (ADR-024 s3), so delete/reset would report nothing to do and exit 0 while automate/execute/link fail with 'No test cases found'", ids)
		}
		return
	}

	t.Log("BRANCH: case-SENSITIVE host -- this run is NOT evidence for the case-insensitive branch of ADR-024 s3")
	if len(ids) != 0 {
		t.Fatalf("differently cased scope selected %v, want none: on a case-sensitive filesystem featurecase and FeatureCase are DISTINCT scopes and must not be conflated (that conflation is BUG-188's defect class)", ids)
	}
}

// The same rule on the folder-qualified DESTRUCTIVE authority path. A false
// refusal here tells the user their spec is only reachable through a link when it
// is in fact the physical owner sitting in the folder they named.
func TestRequireMutationAuthorityUnderScope_FollowsHostCaseSemantics(t *testing.T) {
	root, cases := casesDirFor(t)
	writeSpecID(t, cases, "FeatureCase/tc-22222222-pay.md", "tc-22222222")

	snap := mustSnapshot(t, root)

	if err := snap.RequireMutationAuthorityUnderScope("gtms/test/cases/FeatureCase", "tc-22222222"); err != nil {
		t.Fatalf("exact-case scope was refused authority: %v", err)
	}

	err := snap.RequireMutationAuthorityUnderScope("gtms/test/cases/featurecase", "tc-22222222")

	if hostIsCaseInsensitive(t, cases) {
		t.Log("BRANCH: case-INSENSITIVE host")
		if err != nil {
			t.Fatalf("differently cased scope refused authority on a case-insensitive filesystem: %v", err)
		}
		return
	}

	t.Log("BRANCH: case-SENSITIVE host -- not evidence for the case-insensitive branch")
	if err == nil {
		t.Fatal("differently cased scope granted authority on a case-sensitive filesystem: featurecase/ is a different folder and must not confer authority over FeatureCase/")
	}
}

// Membership must be decided by filesystem IDENTITY, not by case folding. Two
// genuinely different sibling folders whose names differ only in case must stay
// separate scopes wherever the host can represent them at all -- folding would
// merge them and hand one folder's delete authority over the other's specs.
func TestSelectFolder_DistinctSiblingsAreNotFoldedTogether(t *testing.T) {
	root, cases := casesDirFor(t)
	if hostIsCaseInsensitive(t, cases) {
		t.Skip("case-insensitive host cannot represent two folders differing only in case")
	}
	t.Log("BRANCH: case-SENSITIVE host")

	writeSpecID(t, cases, "Alpha/tc-33333333-a.md", "tc-33333333")
	writeSpecID(t, cases, "alpha/tc-44444444-b.md", "tc-44444444")

	snap := mustSnapshot(t, root)

	ids, _ := snap.SelectFolder("gtms/test/cases/Alpha", false)
	if len(ids) != 1 || ids[0] != "tc-33333333" {
		t.Fatalf("Alpha selected %v, want [tc-33333333] only: case folding would pull alpha/'s spec into Alpha/'s deletion scope", ids)
	}
	ids, _ = snap.SelectFolder("gtms/test/cases/alpha", false)
	if len(ids) != 1 || ids[0] != "tc-44444444" {
		t.Fatalf("alpha selected %v, want [tc-44444444] only", ids)
	}
}

// The RECOVERY MECHANISM, exercised on a case-sensitive host.
//
// The case-insensitive branch above cannot run on Linux, but the machinery it
// depends on can: resolveScopeSpelling recovers a directory's real spelling by
// os.SameFile identity, and a directory ALIAS reaches that same fallback -- the
// walk never records the link (filepath.Walk Lstats, so a symlink to a directory
// is not a directory to it), yet the alias and its target are one inode. So this
// asserts, on any host, that a scope named by a path the walk never saw is still
// resolved to the real directory by asking the filesystem. That is precisely the
// step macOS needs and byte comparison cannot do.
//
// It also holds ADR-024 s3's directory-alias rule: "An in-tree directory alias
// resolves to the real directory and remains usable."
func TestResolveScopeSpelling_RecoversRealDirectoryByFilesystemIdentity(t *testing.T) {
	root, cases := casesDirFor(t)
	writeSpecID(t, cases, "FeatureCase/tc-66666666-login.md", "tc-66666666")

	if err := os.Symlink(filepath.Join(cases, "FeatureCase"), filepath.Join(cases, "alias")); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}

	snap := mustSnapshot(t, root)

	got := snap.resolveScopeSpelling("gtms/test/cases/alias")
	if got != "gtms/test/cases/FeatureCase" {
		t.Fatalf("resolveScopeSpelling(alias) = %q, want the real directory gtms/test/cases/FeatureCase -- the SameFile fallback did not recover a spelling the walk never recorded", got)
	}

	ids, _ := snap.SelectFolder("gtms/test/cases/alias", false)
	if len(ids) != 1 || ids[0] != "tc-66666666" {
		t.Fatalf("SelectFolder through a directory alias selected %v, want [tc-66666666]", ids)
	}
}

// A scope naming no directory at all selects nothing and does not panic,
// whichever branch the host takes.
func TestSelectFolder_AbsentScopeSelectsNothing(t *testing.T) {
	root, cases := casesDirFor(t)
	writeSpecID(t, cases, "feat/tc-55555555-x.md", "tc-55555555")

	snap := mustSnapshot(t, root)
	if ids, _ := snap.SelectFolder("gtms/test/cases/nosuchfolder", true); len(ids) != 0 {
		t.Fatalf("absent scope selected %v, want none", ids)
	}
}
