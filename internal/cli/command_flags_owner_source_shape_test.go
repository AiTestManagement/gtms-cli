package cli

// ADR-024 s5 structural guard: a work item must be incapable of reaching adapter
// dispatch without the owner its command resolved.
//
// REV-158 CODEX-001 and CODEX-002 were one architectural gap surfacing at two
// call sites: both bulk loops iterated resolved owners and then built
// adapter.CommandFlags without Owner. Nothing in the type system objected --
// Owner is a nil-able pointer field -- and the behavioural consequence was silent
// on one path (bulk automate reported success while writing no wiring) and
// invisible on the other (bulk execute handed adapters an empty spec path).
//
// Setting the field on both loops fixes today's omission. This guard is what
// stops the next one: every CommandFlags literal in a command that dispatches an
// owner-bearing operation must NAME Owner, so the omission cannot be written
// again without the author deleting a test that says why.
//
// create is deliberately excluded: it runs before any test case exists, so there
// is no owner to carry (ADR-024 s1).

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

// ownerBearingCommandFiles are the CLI files whose commands act on an existing
// test case and therefore resolve an owner before dispatch.
var ownerBearingCommandFiles = []string{
	"automate.go",
	"execute.go",
	"prime.go",
}

// TestCommandFlags_OwnerBearingDispatchNamesOwner fails if any
// adapter.CommandFlags literal in an owner-bearing command omits the Owner key.
//
// If this fails, the fix is to pass the owner the command already resolved --
// never to remove the file from the list.
func TestCommandFlags_OwnerBearingDispatchNamesOwner(t *testing.T) {
	dir := packageDirForOwnerGuard(t)

	for _, name := range ownerBearingCommandFiles {
		path := filepath.Join(dir, name)
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}

		found := 0
		ast.Inspect(parsed, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok || !isAdapterCommandFlags(lit.Type) {
				return true
			}
			found++
			if !namesKey(lit, "Owner") {
				t.Errorf("%s:%d: adapter.CommandFlags literal does not set Owner.\n"+
					"ADR-024 s5: a command that acts on an existing test case resolves its owner "+
					"ONCE and passes it forward. Dispatching without it hands the adapter an empty "+
					"spec path and makes the wiring write fail as a downgraded warning "+
					"(REV-158 CODEX-001/CODEX-002).",
					name, fset.Position(lit.Pos()).Line)
			}
			return true
		})

		if found == 0 {
			t.Errorf("%s: no adapter.CommandFlags literal found -- the guard has drifted off "+
				"its target and is no longer checking anything", name)
		}
	}
}

// isAdapterCommandFlags reports whether a composite-literal type is
// adapter.CommandFlags.
func isAdapterCommandFlags(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel == nil || sel.Sel.Name != "CommandFlags" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "adapter"
}

// namesKey reports whether a keyed composite literal sets the named field.
func namesKey(lit *ast.CompositeLit, key string) bool {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		ident, ok := kv.Key.(*ast.Ident)
		if ok && ident.Name == key {
			return true
		}
	}
	return false
}

// packageDirForOwnerGuard returns this package's source directory.
func packageDirForOwnerGuard(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
