package testcase

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
)

// mkSymlinkOrSkip creates a symlink and skips the (sub)test when the platform
// cannot make one. BUG-214 Owner Decision 10 routes Windows junction cases to
// Pester; the POSIX symlink rows here therefore skip on Windows (and run on the
// Linux VPS), matching the project's symlink-test convention.
func mkSymlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("symlinks unsupported on this platform; Windows junction coverage is Pester (BUG-214 D10)")
		}
		t.Fatalf("creating symlink %s -> %s: %v", link, target, err)
	}
}

// evalOrFatal resolves a path's canonical form for comparing an accepted result
// against the directory it should physically denote.
func evalOrFatal(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", p, err)
	}
	return r
}

// newProject builds a minimal project tree: <parent>/project with gtms/test/cases
// present, plus a sibling <parent>/outside. Returns parent and project roots.
func newProject(t *testing.T) (parent, project string) {
	t.Helper()
	parent = t.TempDir()
	project = filepath.Join(parent, "project")
	if err := os.MkdirAll(filepath.Join(project, "gtms", "test", "cases"), 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(parent, "outside"), 0o755); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	return parent, project
}

func casesDir(project string) string { return filepath.Join(project, "gtms", "test", "cases") }

// TestResolveCreateTarget is the BUG-214 table over both target modes and every
// row of the target contract. Refused rows assert the stable phrase; accepted
// rows assert the returned directory physically denotes the expected location.
func TestResolveCreateTarget(t *testing.T) {
	t.Run("default/existing-in-tree-folder-accepted", func(t *testing.T) {
		_, project := newProject(t)
		if err := os.MkdirAll(filepath.Join(casesDir(project), "login"), 0o755); err != nil {
			t.Fatal(err)
		}
		got, err := ResolveCreateTarget(project, "", "login")
		if err != nil {
			t.Fatalf("unexpected refusal: %v", err)
		}
		if want := evalOrFatal(t, filepath.Join(casesDir(project), "login")); evalOrFatal(t, got) != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("default/missing-leaf-below-real-ancestor-accepted", func(t *testing.T) {
		_, project := newProject(t)
		// neither area nor newsub exists yet
		got, err := ResolveCreateTarget(project, "", "area/newsub")
		if err != nil {
			t.Fatalf("unexpected refusal: %v", err)
		}
		want := filepath.Join(evalOrFatal(t, casesDir(project)), "area", "newsub")
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("default/empty-folder-returns-cases-root", func(t *testing.T) {
		_, project := newProject(t)
		got, err := ResolveCreateTarget(project, "", "")
		if err != nil {
			t.Fatalf("unexpected refusal: %v", err)
		}
		if got != evalOrFatal(t, casesDir(project)) {
			t.Fatalf("got %q, want cases root", got)
		}
	})

	t.Run("default/in-tree-link-alias-accepted", func(t *testing.T) {
		_, project := newProject(t)
		real := filepath.Join(casesDir(project), "real")
		if err := os.MkdirAll(real, 0o755); err != nil {
			t.Fatal(err)
		}
		mkSymlinkOrSkip(t, real, filepath.Join(casesDir(project), "alias"))
		got, err := ResolveCreateTarget(project, "", "alias")
		if err != nil {
			t.Fatalf("unexpected refusal: %v", err)
		}
		if got != evalOrFatal(t, real) {
			t.Fatalf("got %q, want %q (the link target)", got, evalOrFatal(t, real))
		}
	})

	t.Run("default/existing-out-of-tree-link-refused", func(t *testing.T) {
		parent, project := newProject(t)
		mkSymlinkOrSkip(t, filepath.Join(parent, "outside"), filepath.Join(casesDir(project), "ext"))
		_, err := ResolveCreateTarget(project, "", "ext")
		assertRefused(t, err, PhraseOutsideTestCases)
	})

	t.Run("default/missing-child-below-out-of-tree-link-refused", func(t *testing.T) {
		parent, project := newProject(t)
		mkSymlinkOrSkip(t, filepath.Join(parent, "outside"), filepath.Join(casesDir(project), "ext"))
		_, err := ResolveCreateTarget(project, "", "ext/newsub")
		assertRefused(t, err, PhraseOutsideTestCases)
	})

	t.Run("default/in-project-link-outside-cases-root-refused", func(t *testing.T) {
		_, project := newProject(t)
		other := filepath.Join(project, "other")
		if err := os.MkdirAll(other, 0o755); err != nil {
			t.Fatal(err)
		}
		mkSymlinkOrSkip(t, other, filepath.Join(casesDir(project), "ext"))
		_, err := ResolveCreateTarget(project, "", "ext")
		// Inside the project but outside the test cases directory: must still be refused
		// with the test-cases phrase (second-stage boundary).
		assertRefused(t, err, PhraseOutsideTestCases)
	})

	t.Run("default/linked-cases-root-refused", func(t *testing.T) {
		parent, project := newProject(t)
		// Replace gtms/test/cases with a link to outside.
		if err := os.RemoveAll(casesDir(project)); err != nil {
			t.Fatal(err)
		}
		mkSymlinkOrSkip(t, filepath.Join(parent, "outside"), casesDir(project))
		_, err := ResolveCreateTarget(project, "", "feature")
		assertRefused(t, err, PhraseOutsideTestCases)
	})

	t.Run("default/broken-link-refused", func(t *testing.T) {
		parent, project := newProject(t)
		// ext -> outside/missing (does not exist)
		mkSymlinkOrSkip(t, filepath.Join(parent, "outside", "missing"), filepath.Join(casesDir(project), "ext"))
		_, err := ResolveCreateTarget(project, "", "ext/newsub")
		if err == nil {
			t.Fatal("expected refusal for a broken link, got nil")
		}
		// Phrase is left to the PRP for a broken link; any CreateContainmentError refusal is fine.
		var ce *CreateContainmentError
		if !errors.As(err, &ce) {
			t.Fatalf("expected *CreateContainmentError, got %T: %v", err, err)
		}
	})

	t.Run("outputdir/ordinary-in-project-accepted", func(t *testing.T) {
		_, project := newProject(t)
		got, err := ResolveCreateTarget(project, "custom/specs", "ignored-folder")
		if err != nil {
			t.Fatalf("unexpected refusal: %v", err)
		}
		want := filepath.Join(evalOrFatal(t, project), "custom", "specs")
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("outputdir/in-project-linked-accepted", func(t *testing.T) {
		_, project := newProject(t)
		realSpecs := filepath.Join(project, "real-specs")
		if err := os.MkdirAll(realSpecs, 0o755); err != nil {
			t.Fatal(err)
		}
		mkSymlinkOrSkip(t, realSpecs, filepath.Join(project, "specs-link"))
		got, err := ResolveCreateTarget(project, "specs-link", "")
		if err != nil {
			t.Fatalf("unexpected refusal: %v", err)
		}
		if got != evalOrFatal(t, realSpecs) {
			t.Fatalf("got %q, want %q (the link target)", got, evalOrFatal(t, realSpecs))
		}
	})

	t.Run("outputdir/dotdot-escape-refused", func(t *testing.T) {
		_, project := newProject(t)
		_, err := ResolveCreateTarget(project, "../outside", "")
		assertRefused(t, err, PhraseOutsideProject)
	})

	t.Run("outputdir/linked-parent-refused", func(t *testing.T) {
		parent, project := newProject(t)
		mkSymlinkOrSkip(t, filepath.Join(parent, "outside"), filepath.Join(project, "linked"))
		_, err := ResolveCreateTarget(project, "linked/specs", "")
		assertRefused(t, err, PhraseOutsideProject)
	})

	t.Run("outputdir/broken-link-refused", func(t *testing.T) {
		parent, project := newProject(t)
		// linked -> outside/missing (does not exist): broken link in the output-dir path.
		mkSymlinkOrSkip(t, filepath.Join(parent, "outside", "missing"), filepath.Join(project, "linked"))
		_, err := ResolveCreateTarget(project, "linked/specs", "")
		if err == nil {
			t.Fatal("expected refusal for a broken link in output-dir, got nil")
		}
		var ce *CreateContainmentError
		if !errors.As(err, &ce) {
			t.Fatalf("expected *CreateContainmentError, got %T: %v", err, err)
		}
		if ce.Phrase != PhraseOutsideProject {
			t.Fatalf("output-dir refusal phrase = %q, want %q", ce.Phrase, PhraseOutsideProject)
		}
	})

	t.Run("project-reached-through-link-accepted", func(t *testing.T) {
		parent, project := newProject(t)
		projectLink := filepath.Join(parent, "project-link")
		mkSymlinkOrSkip(t, project, projectLink)
		// Pass the LINK path as the project root; the result must land in the real project.
		got, err := ResolveCreateTarget(projectLink, "", "feature")
		if err != nil {
			t.Fatalf("unexpected refusal: %v", err)
		}
		want := filepath.Join(evalOrFatal(t, casesDir(project)), "feature")
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})
}

func assertRefused(t *testing.T, err error, wantPhrase string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected refusal with %q, got nil", wantPhrase)
	}
	var ce *CreateContainmentError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *CreateContainmentError, got %T: %v", err, err)
	}
	if ce.Phrase != wantPhrase {
		t.Fatalf("phrase = %q, want %q", ce.Phrase, wantPhrase)
	}
	if !strings.Contains(err.Error(), wantPhrase) {
		t.Fatalf("Error() %q does not contain stable phrase %q", err.Error(), wantPhrase)
	}
	// The cause chain preserves the pathsafe failure.
	if !pathsafe.IsPathSafetyError(err) {
		t.Fatalf("expected a wrapped *pathsafe.PathSafetyError in the chain: %v", err)
	}
}
