package wiring

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSourceShape_NoPipelineImport guards the clean dependency direction
// pinned by the CON-023 cutover: internal/wiring is the new identity layer
// and internal/pipeline is the legacy bridge that wraps wiring. The legacy
// layer is allowed to depend on wiring; the reverse is not. Pipeline is
// being unwound; wiring must not pull it back in by reference.
//
// Parses imports via go/ast so doc-comment mentions of "internal/pipeline"
// (which are legitimate provenance notes) do not trigger the guard.
func TestSourceShape_NoPipelineImport(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if strings.HasSuffix(path, "/internal/pipeline") {
				t.Errorf("%s imports %s; wiring must not depend on the legacy bridge", name, path)
			}
		}
	}
}
