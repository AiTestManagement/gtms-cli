package adapter

// Structural guard: the artefact path an automate run writes is PLANNED EXACTLY
// ONCE per invocation.
//
// The defect this pins: planTier0AutomateArtefact was called twice on every Tier
// 0 automate invocation -- once inside enforceArtefactOwnership, whose result was
// collision-checked and reserved, and again inside buildAdapterContext, whose
// result was what the adapter actually wrote to. The adapter therefore consumed a
// value that no ownership check had ever inspected. It happened to agree with the
// authorised one only because every input to the planner was identical at both
// call sites; nothing made that true, and nothing would have failed when it
// stopped being true.
//
// The fix carries the reserved path (ArtefactReservation.PlannedPath) from the
// boundary into buildAdapterContext, so the authorised value and the written
// value are one value. This guard is what stops the second call being written
// again: a recomputation is invisible in behaviour for exactly as long as the
// inputs agree, so the structure -- one planner, one call site -- is the thing
// worth asserting directly.
//
// If this fails, the fix is to CARRY the already-planned path to wherever it is
// needed, never to add a second planning call and never to delete this guard.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// plannerFunctions are the functions that DECIDE an artefact path. Each is
// allowed exactly one call site across all production (non-test) Go source in
// the module.
//
// PlanArtefactPath is listed alongside planTier0AutomateArtefact because it is
// exported: a second planning call could be reintroduced by calling it directly
// and bypassing the unexported wrapper entirely.
var plannerFunctions = map[string]int{
	"planTier0AutomateArtefact": 1,
	"PlanArtefactPath":          1,
}

func TestArtefactPlanning_HasExactlyOneProductionCallSite(t *testing.T) {
	root := moduleRootForPlanningGuard(t)

	counts := map[string]int{}
	sites := map[string][]string{}

	walk := func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			if strings.HasPrefix(info.Name(), ".") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		parsed, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			t.Fatalf("parsing %s: %v", path, parseErr)
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		ast.Inspect(parsed, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := calleeName(call.Fun)
			if _, tracked := plannerFunctions[name]; !tracked {
				return true
			}
			counts[name]++
			sites[name] = append(sites[name],
				filepath.ToSlash(rel)+":"+itoa(fset.Position(call.Pos()).Line))
			return true
		})
		return nil
	}

	// Only the module's Go source trees hold production code, and walking those
	// two keeps the guard fast on the repo's large data directories.
	for _, tree := range []string{"cmd", "internal"} {
		if err := filepath.Walk(filepath.Join(root, tree), walk); err != nil {
			t.Fatalf("walking %s: %v", tree, err)
		}
	}

	for name, want := range plannerFunctions {
		if counts[name] != want {
			t.Errorf("%s has %d production call site(s), want %d: %s\n"+
				"The artefact path must be planned ONCE per invocation and the AUTHORISED value carried\n"+
				"from there. A second planning call gives the adapter a path that no ownership check\n"+
				"inspected -- it agrees with the reserved path only while every planner input happens to\n"+
				"match at both sites. Carry ArtefactReservation.PlannedPath instead of planning again.",
				name, counts[name], want, strings.Join(sites[name], ", "))
		}
	}
}

// TestArtefactPlanning_GuardTargetsRealFunctions fails if a planner is renamed or
// removed, so the call-site count above cannot silently start counting nothing.
func TestArtefactPlanning_GuardTargetsRealFunctions(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	declared := map[string]bool{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		parsed, parseErr := parser.ParseFile(fset, filepath.Join(dir, entry.Name()), nil, 0)
		if parseErr != nil {
			t.Fatalf("parsing %s: %v", entry.Name(), parseErr)
		}
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Recv == nil && fn.Name != nil {
				declared[fn.Name.Name] = true
			}
		}
	}
	for name := range plannerFunctions {
		if !declared[name] {
			t.Errorf("planner %s is no longer declared in package adapter -- the one-call-site guard "+
				"has drifted off its target. Point it at the function that replaced it; do not delete it.", name)
		}
	}
}

// calleeName returns the identifier a call expression invokes, for both plain
// calls (f()) and qualified ones (adapter.F()).
func calleeName(expr ast.Expr) string {
	switch fun := expr.(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		if fun.Sel != nil {
			return fun.Sel.Name
		}
	}
	return ""
}

// itoa avoids pulling strconv in for one call site.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// moduleRootForPlanningGuard walks up from this package's directory to the
// directory holding go.mod, so the guard covers EVERY production package rather
// than only this one -- PlanArtefactPath is exported and callable from anywhere.
func moduleRootForPlanningGuard(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate go.mod above the adapter package")
		}
		dir = parent
	}
}
