package testcase

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// F5: only an ABSENT cases directory is an empty snapshot. A stat/access failure
// or an existing NON-directory cases root must return an error -- treating either
// as an empty project makes owners vanish into NotFound, which delete/reset read
// as proven absence and a licence to clean up.
func TestNewSnapshot_MissingCasesDir_EmptySnapshot(t *testing.T) {
	root := t.TempDir() // no gtms/test/cases at all
	snap, err := NewSnapshot(root)
	if err != nil {
		t.Fatalf("missing cases dir must be an empty snapshot, got error: %v", err)
	}
	if snap == nil || len(snap.candidates) != 0 {
		t.Fatalf("missing cases dir must yield an empty snapshot")
	}
}

func TestNewSnapshot_NonDirectoryCasesRoot_Errors(t *testing.T) {
	root := t.TempDir()
	// gtms/test/cases exists but is a FILE, not a directory.
	casesParent := filepath.Join(root, "gtms", "test")
	if err := os.MkdirAll(casesParent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(casesParent, "cases"), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewSnapshot(root); err == nil {
		t.Fatal("a non-directory cases root must return an error, not an empty snapshot")
	}
}

func TestNewSnapshot_AccessFailure_Errors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix directory-permission fixture")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	root := t.TempDir()
	cases := filepath.Join(root, "gtms", "test", "cases")
	if err := os.MkdirAll(cases, 0o755); err != nil {
		t.Fatal(err)
	}
	// Make the PARENT unsearchable so os.Stat(cases) fails with EACCES (not
	// ErrNotExist). Restore in cleanup so t.TempDir can be removed.
	parent := filepath.Join(root, "gtms", "test")
	if err := os.Chmod(parent, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })

	if _, err := NewSnapshot(root); err == nil {
		t.Fatal("a stat/access failure on the cases root must return an error, not an empty snapshot")
	}
}
