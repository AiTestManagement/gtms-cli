package reader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// REV-159 CLAUDE-005 -- buildDeletePlan dereferenced a nil scope where the
// parallel reset path guards it.
//
// scopeRelFor tolerates a nil scope and returns "", but scope.Recursive on the
// next argument panics. reset.go writes `scope != nil && scope.Recursive` for the
// same input shape, so the two destructive planners disagreed.
//
// Classified as a regression introduced on THIS branch: at b524c9b5 the folder
// branch called scanTestCases(projectRoot, scope), which handled nil explicitly.
// It is not reachable from the CLI, but DeleteArtifacts is exported, and a call
// that previously returned a plan crashed the process instead.

func seedDeleteProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	cases := filepath.Join(root, "gtms", "test", "cases", "feat")
	if err := os.MkdirAll(cases, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	spec := filepath.Join(cases, "tc-abcd1234-login.md")
	if err := os.WriteFile(spec, []byte("---\ntest_case_id: tc-abcd1234\ntitle: \"t\"\n---\n\nbody\n"), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	return root
}

// The finding: an error, not a panic, and no partial mutation.
func TestDeleteArtifacts_NilScopeWithNoTCIDReturnsAnErrorInsteadOfPanicking(t *testing.T) {
	root := seedDeleteProject(t)
	spec := filepath.Join(root, "gtms", "test", "cases", "feat", "tc-abcd1234-login.md")

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("DeleteArtifacts panicked on a nil scope: %v\nan exported destructive API must return an error, not crash the process", r)
		}
	}()

	res, err := DeleteArtifacts(root, nil, "", false, false, false)
	if err == nil {
		t.Fatalf("nil scope with no test case ID was accepted (result %+v): for an all-or-nothing destructive plan, silently selecting nothing is indistinguishable from an empty project, and selecting everything would make a nil argument mean \"delete the whole project\"", res)
	}
	if !strings.Contains(err.Error(), "scope") {
		t.Fatalf("error %q does not explain that a scope is required", err.Error())
	}
	if _, statErr := os.Stat(spec); statErr != nil {
		t.Fatal("the refused plan removed a spec; a delete that refuses must mutate nothing")
	}
}

// The nil scope stays legitimate for a single-TC delete, which is how the CLI
// calls it for an unqualified target. The guard must refuse the folder branch
// only, not break that.
func TestDeleteArtifacts_NilScopeWithATCIDIsStillValid(t *testing.T) {
	root := seedDeleteProject(t)

	if _, err := DeleteArtifacts(root, nil, "tc-abcd1234", false, false, true); err != nil {
		t.Fatalf("an unqualified single-TC delete was refused: %v\na nil scope means \"unqualified\" here, which the CLI produces for every bare TC target", err)
	}
}

// The two destructive planners must agree on this input shape. reset already
// tolerated a nil scope; the point of the finding is the disagreement, so assert
// reset's side of it too.
func TestResetExecuteResults_NilScopeDoesNotPanic(t *testing.T) {
	root := seedDeleteProject(t)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ResetExecuteResults panicked on a nil scope: %v", r)
		}
	}()

	if _, err := ResetExecuteResults(root, nil, "", true); err != nil {
		t.Fatalf("reset refused a nil scope: %v", err)
	}
}
