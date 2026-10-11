package testcase_test

// ADR-024 boundary guard -- END STATE (ENH-205 complete).
//
// ADR-024 5b required the table-driven boundary guard to collapse once every bulk
// read/list operation had migrated onto the snapshot and the legacy bare-ID-to-spec
// resolvers were deleted: "the table and its legacy state are removed along with the
// legacy resolvers, and the guard collapses to: every bulk operation reaches the
// snapshot, and no API that maps a bare ID to a spec path outside the snapshot
// exists. That last part is a source-shape assertion over the whole module."
//
// This file is that collapsed guard. It builds a transitive call graph over the
// module's own packages (the same machinery the migration guard used) and holds two
// properties:
//
//  1. Every bulk read/list operation reaches a project-wide snapshot constructor.
//  2. No legacy bare-ID-to-spec resolver is declared anywhere in the module -- so a
//     delegating wrapper (the kind ADR-024 forbids as a final state) cannot come
//     back. This is the source-shape half by NAME, and it is supplementary: the
//     effect-based check in adr024_effect_guard_test.go catches a direct
//     cases-tree walk or an ID-to-cases-path helper under ANY name (REV-184
//     R2-CODEX-001 / R2-CLAUDE-002).
//
// The migration history (per-operation flips, the counterfactual, the interim
// consumer-state table) is preserved in git and in the ENH-205 PRP; it is not needed
// now that the boundary is a single end state.
//
// Method-call resolution is by name and deliberately over-approximates: over-
// approximation can only add edges, so it can only make property 1 stricter.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const guardModulePrefix = "github.com/aitestmanagement/gtms-cli/"

// snapshotConstructors are the project-wide snapshot-building entry points. Every
// bulk operation must reach one of them.
var snapshotConstructors = []string{
	"testcase.NewSnapshot",
	"testcase.buildSnapshot",
}

// bulkOperations enumerates the semantic bulk read/list entry points ADR-024 5b
// migrated onto the snapshot. Each must reach a snapshot constructor. A rename fails
// the guard loudly rather than silently dropping an operation from coverage.
var bulkOperations = []string{
	"reader.PipelineStatus",
	"reader.PipelineStatusWithDiagnostics",
	"reader.PipelineFolderSummary",
	"reader.PipelineFolderSummaryWithDiagnostics",
	"reader.FolderTriagedCounts",
	"reader.Map",
	"reader.MapWithDiagnostics",
	"reader.MapTargetDiagnostics",
	"reader.Gaps",
	"reader.GapsWithDiagnostics",
	"reader.GapsFolderSummary",
	"reader.GapsFolderSummaryWithDiagnostics",
	"reader.ListTriage",
	"reader.ListTriageWithDiagnostics",
	"reader.ListTriageForTC",
}

// deletedLegacyResolvers are the bare-ID-to-spec resolvers and their delegating
// wrappers ENH-205 deleted. The source-shape assertion proves none is declared
// again anywhere in the module: reintroducing one (even as a thin wrapper over the
// snapshot) is the regression ADR-024 Required Behaviour 8 forbids as a final state.
// Each entry is "package.Function".
var deletedLegacyResolvers = []string{
	"testcase.ResolveSpec",
	"testcase.FindAllSpecs", // REV-184 CODEX-003: bare ID -> spec paths via its own snapshot
	"testcase.Exists",
	"testcase.FindSource",
	"pipeline.ResolveTestCaseSpec",
	"reader.scanTestCases",
	"reader.scanTestCasesAll",
	"reader.scanTestCasesRecursive",
	"reader.scanTestCasesShallow",
	"reader.ScanSpecFiles",
	"reader.ClassifyWiring",
	"reader.isStaleTestCaseHash",
	"cli.DiscoverTestCases",
	"cli.extractTestCaseID",
}

// --- call graph -------------------------------------------------------------

type guardFuncSite struct {
	pkg     string
	relFile string
	fd      *ast.FuncDecl
	imports map[string]string
}

type guardCallGraph struct {
	sites map[string][]*guardFuncSite
	cache map[string][]string
}

func buildGuardCallGraph(t *testing.T, root string, subtrees ...string) *guardCallGraph {
	t.Helper()
	g := &guardCallGraph{sites: map[string][]*guardFuncSite{}, cache: map[string][]string{}}
	pkgDirs := map[string]string{}
	for _, sub := range subtrees {
		base := filepath.Join(root, sub)
		if _, err := os.Stat(base); err != nil {
			continue
		}
		err := filepath.Walk(base, func(path string, info os.FileInfo, werr error) error {
			if werr != nil {
				return werr
			}
			if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			f, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				return fmt.Errorf("parsing %s: %w", path, perr)
			}
			pkg := f.Name.Name
			dir := filepath.Dir(path)
			if prev, ok := pkgDirs[pkg]; ok && prev != dir {
				return fmt.Errorf("package name %q is used by two directories (%s and %s); "+
					"the guard keys functions by package name and needs them unique", pkg, prev, dir)
			}
			pkgDirs[pkg] = dir

			imports := map[string]string{}
			for _, im := range f.Imports {
				p := strings.Trim(im.Path.Value, `"`)
				if !strings.HasPrefix(p, guardModulePrefix) {
					continue
				}
				name := p[strings.LastIndex(p, "/")+1:]
				local := name
				if im.Name != nil {
					local = im.Name.Name
				}
				imports[local] = name
			}
			rel, _ := filepath.Rel(root, path)
			for _, decl := range f.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok || fd.Name == nil || fd.Body == nil {
					continue
				}
				key := pkg + "." + fd.Name.Name
				g.sites[key] = append(g.sites[key], &guardFuncSite{
					pkg: pkg, relFile: filepath.ToSlash(rel), fd: fd, imports: imports,
				})
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scanning %s: %v", base, err)
		}
	}
	return g
}

func (g *guardCallGraph) has(key string) bool { return len(g.sites[key]) > 0 }

func (g *guardCallGraph) callees(key string) []string {
	if c, ok := g.cache[key]; ok {
		return c
	}
	out := map[string]bool{}
	for _, s := range g.sites[key] {
		ast.Inspect(s.fd.Body, func(n ast.Node) bool {
			ce, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fun := ce.Fun.(type) {
			case *ast.Ident:
				if k := s.pkg + "." + fun.Name; g.has(k) {
					out[k] = true
				}
			case *ast.SelectorExpr:
				sel := fun.Sel.Name
				if id, isIdent := fun.X.(*ast.Ident); isIdent {
					if p, isPkg := s.imports[id.Name]; isPkg {
						if k := p + "." + sel; g.has(k) {
							out[k] = true
						}
						return true
					}
				}
				if k := s.pkg + "." + sel; g.has(k) {
					out[k] = true
					return true
				}
				for _, p := range s.imports {
					if k := p + "." + sel; g.has(k) {
						out[k] = true
					}
				}
			}
			return true
		})
	}
	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	g.cache[key] = keys
	return keys
}

// pathTo returns the shortest call path from `from` into `targets`, or nil.
func (g *guardCallGraph) pathTo(from string, targets map[string]bool) []string {
	prev := map[string]string{from: ""}
	queue := []string{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, c := range g.callees(cur) {
			if _, seen := prev[c]; seen {
				continue
			}
			prev[c] = cur
			if targets[c] {
				var path []string
				for n := c; n != ""; n = prev[n] {
					path = append([]string{n}, path...)
				}
				return path
			}
			queue = append(queue, c)
		}
	}
	return nil
}

// --- helpers ----------------------------------------------------------------

func guardRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not locate the module root (go.mod)")
	return ""
}

func guardKeySet(keys []string) map[string]bool {
	m := make(map[string]bool, len(keys))
	for _, k := range keys {
		m[k] = true
	}
	return m
}

func guardRepoGraph(t *testing.T) *guardCallGraph {
	t.Helper()
	return buildGuardCallGraph(t, guardRepoRoot(t), "internal", "cmd")
}

// --- property 1: every bulk operation reaches the snapshot ------------------

func TestADR024Boundary_BulkOperationsReachSnapshot(t *testing.T) {
	g := guardRepoGraph(t)
	for _, k := range snapshotConstructors {
		if !g.has(k) {
			t.Fatalf("snapshot constructor %s no longer exists; update snapshotConstructors.", k)
		}
	}
	ctors := guardKeySet(snapshotConstructors)

	for _, op := range bulkOperations {
		op := op
		t.Run(op, func(t *testing.T) {
			if !g.has(op) {
				t.Fatalf("bulk operation %s does not exist. If it was renamed, update "+
					"bulkOperations -- do not drop it, or the operation stops being guarded.", op)
			}
			if g.pathTo(op, ctors) == nil {
				t.Errorf("bulk operation %s does not reach a project-wide snapshot constructor.\n"+
					"    ADR-024 5b end state: every bulk read/list operation resolves identity through "+
					"the shared snapshot. A bulk read that does not reach NewSnapshot is resolving "+
					"identity some other way -- the regression ENH-205 removed.", op)
			}
		})
	}
}

// --- property 2: no legacy bare-ID resolver can be reintroduced -------------

// TestADR024Boundary_NoLegacyResolverDeclared is the source-shape half. It proves
// none of the deleted bare-ID-to-spec resolvers (or their delegating wrappers) is
// declared anywhere in the module. Reintroducing one -- even as a thin wrapper over
// the snapshot -- reopens the route by which identity can be reduced to a bare ID
// and re-resolved outside the snapshot, which ADR-024 Required Behaviour 8 forbids
// as a final state.
func TestADR024Boundary_NoLegacyResolverDeclared(t *testing.T) {
	g := guardRepoGraph(t)
	for _, name := range deletedLegacyResolvers {
		if g.has(name) {
			var files []string
			for _, s := range g.sites[name] {
				files = append(files, s.relFile)
			}
			t.Errorf("legacy resolver %s is declared again (in %s).\n"+
				"    ADR-024 Required Behaviour 8: the bare-ID-to-spec resolvers were deleted and no "+
				"delegating wrapper may survive. Resolve identity through the snapshot API "+
				"(testcase.ResolveOwner / Snapshot.Resolve / Snapshot.RequireOwner /\n"+
				"    Snapshot.SelectFolder) and hash the file a proven Owner points at.", name, strings.Join(files, ", "))
		}
	}
}

// --- machinery self-test ----------------------------------------------------

// TestADR024Boundary_TransitivityIsActuallyTransitive proves the analyser detects a
// target reached through indirection, so property 1 cannot pass for the wrong reason.
func TestADR024Boundary_TransitivityIsActuallyTransitive(t *testing.T) {
	root := t.TempDir()
	mk := func(rel, src string) {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(src), 0644); err != nil {
			t.Fatal(err)
		}
	}
	mk("internal/testcase/testcase.go", `package testcase
func NewSnapshot() int { return 0 }
func build() int       { return NewSnapshot() }
`)
	mk("internal/reader/status.go", `package reader
import "`+guardModulePrefix+`internal/testcase"
func PipelineStatus() int { return helper() }
func helper() int         { return testcase.NewSnapshot() }
func Unrelated() int      { return 1 }
`)

	g := buildGuardCallGraph(t, root, "internal")
	ctors := guardKeySet([]string{"testcase.NewSnapshot"})

	path := g.pathTo("reader.PipelineStatus", ctors)
	if path == nil {
		t.Fatal("the analyser missed a constructor reached through one level of indirection; " +
			"property 1 would be decorative")
	}
	want := "reader.PipelineStatus -> reader.helper -> testcase.NewSnapshot"
	if got := strings.Join(path, " -> "); got != want {
		t.Errorf("reachability path = %q, want %q", got, want)
	}
	if g.pathTo("reader.Unrelated", ctors) != nil {
		t.Error("the analyser invented a path from a function that reaches nothing")
	}
}
