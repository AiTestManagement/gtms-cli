package testcase

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestIsReservedCompanion_ExactCaseSensitive(t *testing.T) {
	tests := map[string]bool{
		"index.md":     true,
		"log.md":       true,
		"Index.md":     false,
		"INDEX.MD":     false,
		"index.MD":     false,
		"index.md.bak": false,
		"notes.md":     false,
		"":             false,
	}
	for name, want := range tests {
		t.Run(name, func(t *testing.T) {
			if got := IsReservedCompanion(name); got != want {
				t.Fatalf("IsReservedCompanion(%q) = %v, want %v", name, got, want)
			}
		})
	}
}

func TestIsReservedCasesCompanionPath(t *testing.T) {
	root, cases := casesDirFor(t)
	if err := os.MkdirAll(filepath.Join(cases, "feature"), 0o755); err != nil {
		t.Fatal(err)
	}
	differentCaseNested := filepath.Join(cases, "level-one", "level-two")
	if err := os.MkdirAll(differentCaseNested, 0o755); err != nil {
		t.Fatal(err)
	}
	differentCaseParent := filepath.Join(root, "GTMS", "test", "cases")
	_, differentCaseErr := os.Stat(differentCaseParent)
	_, differentCaseNestedErr := os.Stat(filepath.Join(differentCaseParent, "level-one", "level-two"))
	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "cases root", path: "gtms/test/cases/index.md", want: true},
		{name: "nested cases", path: "gtms/test/cases/feature/log.md", want: true},
		{name: "traversal", path: "gtms/test/cases/../../index.md", want: false},
		{name: "escaping parent", path: "../outside/index.md", want: false},
		{name: "sibling prefix", path: "gtms/test/cases-archive/index.md", want: false},
		{name: "absolute", path: filepath.Join(root, "gtms", "test", "cases", "index.md"), want: false},
		{name: "custom output", path: "custom/output/index.md", want: false},
		{name: "ordinary cases file", path: "gtms/test/cases/feature/Index.md", want: false},
		{name: "filesystem case semantics", path: "GTMS/test/cases/index.md", want: differentCaseErr == nil},
		{name: "nested filesystem case semantics", path: "GTMS/test/cases/level-one/level-two/log.md", want: differentCaseNestedErr == nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsReservedCasesCompanionPath(root, tt.path); got != tt.want {
				t.Fatalf("IsReservedCasesCompanionPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}

	t.Run("broken reserved symlink", func(t *testing.T) {
		link := filepath.Join(cases, "aliases", "index.md")
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			t.Fatal(err)
		}
		mustSymlink(t, filepath.Join(root, "missing-target.md"), link)
		if !IsReservedCasesCompanionPath(root, "gtms/test/cases/aliases/index.md") {
			t.Fatal("broken reserved symlink was not treated as reserved")
		}
	})

	t.Run("out-of-project reserved symlink", func(t *testing.T) {
		outside := filepath.Join(t.TempDir(), "outside.md")
		if err := os.WriteFile(outside, []byte("outside\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(cases, "external", "log.md")
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			t.Fatal(err)
		}
		mustSymlink(t, outside, link)
		if !IsReservedCasesCompanionPath(root, "gtms/test/cases/external/log.md") {
			t.Fatal("out-of-project reserved symlink was not treated as reserved")
		}
	})

}

func TestSnapshot_ReservedCompanionsExcludedBeforeAdmission(t *testing.T) {
	root, cases := casesDirFor(t)
	writeSpecID(t, cases, "feature/tc-11111111-owner.md", "tc-11111111")
	writeRaw(t, cases, "index.md", "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: [broken\n")
	writeRaw(t, cases, "log.md", "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-22222222\n---\n")
	writeRaw(t, cases, "nested/index.md", "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: \"\"\n---\n")
	writeRaw(t, cases, "nested/log.md", "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-33333333\n---\n")
	writeRaw(t, cases, "feature/tc-44444444-malformed.md", "---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: [broken\n")

	variants := []struct {
		dir  string
		name string
		id   string
	}{
		{dir: "case-title", name: "Index.md", id: "tc-55555555"},
		{dir: "case-all", name: "INDEX.MD", id: "tc-66666666"},
		{dir: "case-ext", name: "index.MD", id: "tc-77777777"},
	}
	for _, v := range variants {
		writeSpecID(t, cases, v.dir+"/"+v.name, v.id)
		entries, err := os.ReadDir(filepath.Join(cases, v.dir))
		if err != nil {
			t.Fatalf("ReadDir(%s): %v", v.dir, err)
		}
		if len(entries) != 1 || entries[0].Name() != v.name {
			t.Fatalf("on-disk spelling in %s = %v, want %q", v.dir, entryNames(entries), v.name)
		}
	}

	snap := mustSnapshot(t, root)
	if got := snap.KnownIDs(); len(got) != 2 || got[0] != "tc-11111111" || got[1] != "tc-55555555" {
		t.Fatalf("KnownIDs = %v, want ordinary owner and case-variant Index.md only", got)
	}
	_, unprovable := snap.SelectFolder("gtms/test/cases", true)
	if len(unprovable) != 1 || filepath.Base(unprovable[0].LogicalRel) != "tc-44444444-malformed.md" {
		t.Fatalf("unprovable = %#v, want only malformed canonical control", unprovable)
	}
	for _, cand := range snap.candidates {
		if IsReservedCompanion(filepath.Base(cand.LogicalRel)) {
			t.Fatalf("reserved companion entered snapshot candidates: %s", cand.LogicalRel)
		}
	}
	if err := snap.RequireFolderOwnership("gtms/test/cases/nested", true); err != nil {
		t.Fatalf("reserved companions caused mutation refusal: %v", err)
	}

	t.Run("reserved logical alias", func(t *testing.T) {
		aliasRoot, aliasCases := casesDirFor(t)
		target := writeSpecID(t, aliasCases, "real/tc-88888888-owner.md", "tc-88888888")
		link := filepath.Join(aliasCases, "aliases", "index.md")
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			t.Fatal(err)
		}
		mustSymlink(t, target, link)
		aliasSnap := mustSnapshot(t, aliasRoot)
		if len(aliasSnap.candidates) != 1 {
			t.Fatalf("candidates = %d, want only real owner", len(aliasSnap.candidates))
		}
	})

	t.Run("non-reserved logical alias", func(t *testing.T) {
		aliasRoot, aliasCases := casesDirFor(t)
		target := writeSpecID(t, aliasCases, "real/tc-99999999-owner.md", "tc-99999999")
		link := filepath.Join(aliasCases, "aliases", "owner-alias.md")
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			t.Fatal(err)
		}
		mustSymlink(t, target, link)
		aliasSnap := mustSnapshot(t, aliasRoot)
		var logical []string
		for _, cand := range aliasSnap.candidates {
			logical = append(logical, filepath.Base(cand.LogicalRel))
		}
		sort.Strings(logical)
		if len(logical) != 2 || logical[0] != "owner-alias.md" || logical[1] != "tc-99999999-owner.md" {
			t.Fatalf("logical candidates = %v, want real owner and non-reserved alias", logical)
		}
	})
}

func entryNames(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}
