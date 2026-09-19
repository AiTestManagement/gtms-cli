package testcase_test

// ADR-024 "Delivery boundary" (5a) enforcement guard -- assertions 1 and 2.
//
// 5a is a prohibition on the IMPLEMENTATION, not a policy for a caller. It was
// violated once already by changing a shared helper's body with no caller edited:
// testcase.ResolveSpec was reimplemented on top of testcase.NewSnapshot, and every
// per-record bulk-read caller silently became O(N^2). A guard that inspects only a
// caller's own source text passes identically before and after that change, so the
// ADR rules it out. This guard therefore builds a transitive call graph over the
// module's own packages and asks reachability questions.
//
// Three assertions are required by 5a. Two are here:
//
//  1. Caller-set equality. The enumerated deferred bulk-read callers must EQUAL
//     the derived set of callers of the legacy resolver surface. A bulk-read
//     caller added later fails the guard until someone classifies it, and a
//     caller that quietly leaves or joins the surface fails it too.
//
//  2. Transitive reachability, BIDIRECTIONAL.
//     2a. No deferred read-only consumer may reach a snapshot-building entry
//     point or an owner-resolution API, transitively, through any depth of
//     helper.
//     2b. No first-half (row-1, mutating) consumer may reach the legacy resolver,
//     transitively.
//
// THE DEFERRED SIDE IS COVERED BY TWO INDEPENDENT CONTROLS, and both are needed.
//
// The first is the enumerated set deferredBulkReadConsumers: 5a's matrix names
// SEMANTIC ENTRY POINTS -- "bulk status, map, gaps, triage listing (call sites
// include internal/reader/picker.go)" -- and those are what assertion 2a must
// hold. The second is the DERIVED set of direct callers of the legacy resolver
// surface, which assertion 1 pins by equality.
//
// The derived set alone is NOT the consumer inventory, and relying on it was a
// real hole in an earlier revision of this guard. Deriving from
// directCallersOf(legacyResolverSurface) yields three per-record HELPERS
// (reader.ClassifyWiring, reader.isStaleTestCaseHash, reader.buildTriageViews) because
// they are the functions that happen to name the resolver. reader.Gaps and
// reader.Map reach their specs through reader.scanTestCases instead, so neither
// appeared anywhere in the guard: injecting a per-record -- or a hoisted,
// once-per-operation -- testcase.NewSnapshot into reader.Gaps's loop left the
// entire suite green, which is precisely the regression 5a exists to catch.
//
// The enumerated set alone is not sufficient either: it cannot notice a NEW
// caller of the legacy resolver appearing somewhere nobody thought to enumerate.
// So the derived equality check is kept as a second control rather than replaced,
// and a third control asserts the derived helpers are all reachable FROM the
// enumerated entry points -- which is what makes the two sets one boundary
// instead of two disconnected lists.
//
// The third assertion -- the bounded-enumeration scaling property -- is a runtime
// measurement and lives in internal/reader/adr024_bounded_enumeration_test.go.
//
// All three ran RED when the guard was committed, before the resolver change,
// because 5a requires the guard to be the FIRST implementation artifact. Re-plan
// step 3 restored the boundary -- testcase.ResolveSpec is the pre-migration
// filename walk again, and the two mutating pipeline callers moved to the
// snapshot API -- so they now run unconditionally in the normal suite. There is
// no env override and no skip. The red baseline they were committed with is kept
// at PRPs/evidence/ADR-024-boundary-guard-failing-baseline.md, so a later green
// run stays falsifiable: the exact reachability paths recorded there are the ones
// that must be gone.
//
// Why "the call graph from each named caller to the constructor is empty" rather
// than "each permitted helper does not itself reach it": 5a offers both, but
// requires the permitted-helper set to be CLOSED UNDER CALLS. Asserting emptiness
// of the whole transitive set gives closure for free -- a helper reaching the
// constructor through one level of indirection is on the path and fails.
//
// Method-call resolution is by name, and deliberately over-approximates: a
// selector whose base is not a known package alias resolves to the same-named
// declaration in the enclosing package, or failing that in every internal package
// the file imports. Over-approximation can only add edges, so it can only make the
// guard stricter -- it cannot let a real path through.

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

// snapshotConstructors are the project-wide snapshot-building entry points. A
// deferred bulk-read caller must not reach any of them at any depth. buildSnapshot
// is listed alongside the exported constructor so a second exported wrapper added
// later is still caught at the point it performs the walk.
var snapshotConstructors = []string{
	"testcase.NewSnapshot",
	"testcase.buildSnapshot",
}

// legacyResolverSurface is the pre-migration resolver and the thin wrappers that
// are indistinguishable from it to a caller. 5a names
// pipeline.ResolveTestCaseSpec / testcase.ResolveSpec as THE shared helper that
// must be split rather than reimplemented; Exists and FindSource are ResolveSpec
// with its error mapped to a bool and a string, and pipeline.ResolveTestCaseSpec
// is a straight delegation to FindSource. Treating the four as one surface is what
// makes assertion 1 meaningful: otherwise a caller could move one hop down the
// wrapper chain and leave the enumerated set without changing anything real.
//
// testcase.FindAllSpecs is deliberately NOT here. It is the first-half
// qualified-target ambiguity guard and is specified to build a snapshot.
var legacyResolverSurface = []string{
	"testcase.ResolveSpec",
	"testcase.Exists",
	"testcase.FindSource",
	"pipeline.ResolveTestCaseSpec",
}

// ownerResolutionAPIs are the snapshot-backed identity-resolution entry points.
// 5a's prohibition is on reaching "project-wide snapshot resolution AT ALL", and
// a snapshot CONSTRUCTOR is only half of that: a consumer handed a snapshot from
// elsewhere -- a package-level cache, a parameter added later, a struct field --
// reaches project-wide resolution without ever naming NewSnapshot. Listing the
// resolution methods alongside the constructors closes that route.
//
// testcase.ResolveOwner is here as well as being a constructor caller, because it
// is the shape a migration reaches for first: one call that builds a snapshot and
// resolves one target.
var ownerResolutionAPIs = []string{
	"testcase.ResolveOwner",
	"testcase.Resolve",
	"testcase.RequireOwner",
	"testcase.RequireOwnerUnderFolder",
	"testcase.RequireOwnership",
	"testcase.RequireFolderOwnership",
	"testcase.RequireMutationAuthorityUnderScope",
	"testcase.SelectFolder",
	"testcase.KnownIDs",
}

// boundaryEntry is one classified function: which ADR-024 5a row it sits in and
// why. The "why" is the effects test from 5a -- what the function DOES, not what
// package it lives in or what it is called.
type boundaryEntry struct {
	fn  string
	why string
}

// deferredBulkReadConsumers enumerates the SEMANTIC ENTRY POINTS of 5a's deferred
// row, transcribed from its matrix: "Bulk status, map, gaps, triage listing (call
// sites include internal/reader/picker.go)". These are the operations a user
// invokes; assertion 2a holds them, whole, against snapshot construction and
// owner resolution.
//
// This set is enumerated rather than derived, and that is the point. A derived
// set can only ever contain functions that NAME the legacy resolver, which makes
// it a list of per-record helpers -- reader.Gaps and reader.Map name nothing on
// that surface, because they reach specs through reader.scanTestCases, so no
// derivation puts them in scope. 5a's row is about what the operation does, not
// about which helper it happens to call, so the row has to be written down.
//
// Consequently this set is a SUPERSET of the derived caller set, not equal to it,
// and it is deliberately not padded-checked the way deferredBulkReadCallers is:
// an entry here must exist (a rename fails loudly) but need not touch the legacy
// resolver at all. The unscoped folder-summary roll-ups are included alongside
// their scoped forms because "bulk status" and "gaps" are each two entry points
// in the code, and a reader of the matrix would expect both.
var deferredBulkReadConsumers = []boundaryEntry{
	{"reader.PipelineStatus", "bulk status: the scoped pipeline read behind `gtms status <folder>`"},
	{"reader.PipelineFolderSummary", "bulk status: the unscoped folder-summary roll-up behind `gtms status`"},
	{"reader.Gaps", "gaps: the scoped coverage-gap read -- reaches specs via scanTestCases, never naming the resolver"},
	{"reader.GapsFolderSummary", "gaps: the unscoped folder-summary roll-up"},
	{"reader.Map", "map: the scoped traceability read -- reaches specs via scanTestCases, never naming the resolver"},
	{"reader.ListTriage", "triage listing: the scoped and root-scoped triage record lists"},
}

// deferredBulkReadConsumerFiles are source files 5a names as deferred call sites.
// Every function declared in them is treated as a deferred consumer, derived from
// the file rather than listed, so a helper added to picker.go later is in scope
// the moment it is written instead of when someone remembers to enumerate it.
var deferredBulkReadConsumerFiles = []string{"internal/reader/picker.go"}

// deferredBulkReadCallers enumerates the bulk read and list consumers 5a defers to
// ENH-205. They keep the pre-migration resolver, so they are the ONLY functions
// permitted to call the legacy resolver surface.
//
// This is the SECOND control, and it is derived, not semantic: it is the set of
// functions that call the legacy resolver surface directly. It cannot define the
// consumer inventory -- see deferredBulkReadConsumers for why -- but it catches
// what an enumerated list cannot: a new caller of the legacy resolver appearing
// somewhere nobody thought to enumerate. Both controls are required; neither
// replaces the other.
//
// This list is asserted EQUAL to the derived caller set, so it is not an
// allowlist that can be padded: adding an entry that is not a real caller fails
// just as loudly as omitting one that is.
var deferredBulkReadCallers = []boundaryEntry{
	{"reader.ClassifyWiring", "per-wiring-record currency and drift for status, gaps and map -- triage LISTING by 5a's effects test, not triage classification"},
	{"reader.isStaleTestCaseHash", "bulk status spec-drift flag, computed per automation record"},
	{"reader.buildTriageViews", "triage listing: the read-only orphan [spec missing] marker shared by the root, folder and test-case-target record lists (ENH-210 moved the FindSource call from ListTriage into this shared builder, which ListTriage and ListTriageForTC both call)"},
}

// firstHalfConsumers enumerates row-1 entry points: every consumer that writes,
// removes, overwrites, or invokes external code, plus direct single-TC read. They
// must resolve through the snapshot and must NOT reach the legacy resolver.
//
// 5a classifies by EFFECT. pipeline.BuildAutomationRecord and
// CreateAutomationRecord are here because they hash a resolved spec and write a
// record -- "hashing on a MUTATING path: wiring writes" -- regardless of living in
// a package whose name suggests reading.
var firstHalfConsumers = []boundaryEntry{
	{"reader.buildDeletePlan", "delete: builds the destructive plan"},
	{"reader.DeleteArtifacts", "delete: the removal entry point"},
	{"reader.buildArtefactReferenceIndex", "artefact-reference indexing (ADR-024 s8)"},
	{"reader.ResetExecuteResults", "reset"},
	{"reader.ClassifyTriage", "triage classification: hashes a resolved owner and writes a triage record"},
	{"reader.PipelineDetail", "direct single-TC read -- row 1 despite being read-only"},
	{"link.LinkRecord", "link write"},
	{"link.PlanRefresh", "link refresh"},
	{"link.RefreshRecord", "link refresh write"},
	{"link.RepointRecord", "link repoint write"},
	{"link.RepointSingle", "link repoint"},
	{"link.RepointBulk", "link repoint"},
	{"link.RepointAll", "link repoint"},
	{"cli.runBulkAutomate", "automate dispatch"},
	{"cli.runBulkExecute", "execute dispatch"},
	{"adapter.buildPipelineRecords", "pipeline write after adapter completion"},
	{"adapter.WriteAutomateWiring", "wiring write and its spec hash"},
	{"adapter.ValidateTestCaseOwner", "post-fill validation"},
	{"adapter.ownerSource", "adapter source selection"},
	{"pipeline.BuildAutomationRecord", "wiring write: hashes the resolved spec and writes the record"},
	{"pipeline.CreateAutomationRecord", "wiring write: hashes the resolved spec and writes the record"},
}

// --- call graph -------------------------------------------------------------

type guardFuncSite struct {
	pkg     string
	relFile string
	fd      *ast.FuncDecl
	imports map[string]string // local name -> internal package name
}

type guardCallGraph struct {
	sites map[string][]*guardFuncSite // "pkg.Func" -> declarations
	cache map[string][]string
}

// buildGuardCallGraph parses every non-test .go file under the given subtrees and
// indexes function and method declarations by "package.Name". Methods with the
// same name on different types merge into one key: another over-approximation
// that can only add edges.
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

// funcsInFile returns every "pkg.Func" declared in the given module-relative
// source file, sorted. It backs the file-derived half of the deferred consumer
// set: 5a names internal/reader/picker.go as a deferred call site, and deriving
// its functions from the file means a helper added there later is in scope
// immediately rather than when someone remembers to enumerate it.
func (g *guardCallGraph) funcsInFile(rel string) []string {
	var out []string
	for key, sites := range g.sites {
		for _, s := range sites {
			if s.relFile == rel {
				out = append(out, key)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// callees returns every "pkg.Func" this function can call, including calls made
// from closures in its body -- a closure passed to filepath.Walk is part of the
// enclosing function for reachability purposes.
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
				// A method call, or a call on an expression. Prefer the enclosing
				// package, then fall back to every internal package this file imports.
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

// directCallersOf returns every function outside targets that calls into targets.
func (g *guardCallGraph) directCallersOf(targets map[string]bool) []string {
	var out []string
	for key := range g.sites {
		if targets[key] {
			continue
		}
		for _, c := range g.callees(key) {
			if targets[c] {
				out = append(out, key)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// pathTo returns the shortest call path from `from` into `targets`, or nil.
// Nodes in `cut` are never expanded (`from` itself is always expanded), which is
// how 5a's "where a call site is reachable from both rows, the DEFERRED row
// governs" is encoded: a first-half consumer may reach the legacy resolver only
// by going through a caller already classified deferred.
func (g *guardCallGraph) pathTo(from string, targets, cut map[string]bool) []string {
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
			if cut[c] {
				continue
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

func guardEntryKeys(entries []boundaryEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.fn)
	}
	sort.Strings(out)
	return out
}

func guardRepoGraph(t *testing.T) *guardCallGraph {
	t.Helper()
	return buildGuardCallGraph(t, guardRepoRoot(t), "internal", "cmd")
}

// --- assertion 1: caller-set equality ---------------------------------------

// TestADR024Boundary_LegacyCallerSetIsEnumerated asserts the enumerated deferred
// caller set EQUALS the derived set of callers of the legacy resolver surface.
//
// This is the assertion that makes the other two hold their value over time. A
// bulk-read caller added later is not silently deferred: it is an unclassified
// caller, and it fails here until someone puts it in a row.
func TestADR024Boundary_LegacyCallerSetIsEnumerated(t *testing.T) {
	g := guardRepoGraph(t)
	surface := guardKeySet(legacyResolverSurface)
	for _, k := range legacyResolverSurface {
		if !g.has(k) {
			t.Fatalf("legacy resolver surface names %s, which no longer exists. "+
				"If the surface moved, update legacyResolverSurface -- do not delete the assertion.", k)
		}
	}

	derived := g.directCallersOf(surface)
	enumerated := guardEntryKeys(deferredBulkReadCallers)

	derivedSet := guardKeySet(derived)
	enumSet := guardKeySet(enumerated)

	for _, d := range derived {
		if !enumSet[d] {
			t.Errorf("%s calls the legacy resolver surface but is not classified.\n"+
				"    ADR-024 5a: every caller of the pre-migration resolver must be an enumerated\n"+
				"    DEFERRED bulk-read caller. Classify it by EFFECT: if it writes, removes,\n"+
				"    overwrites, invokes external code, or hashes on a mutating path, it is row 1 and\n"+
				"    must move to the snapshot API. If it is bulk read or list, add it to\n"+
				"    deferredBulkReadCallers with the effect that justifies deferral.\n"+
				"    Path to the surface: %v", d, g.pathTo(d, surface, nil))
		}
	}
	for _, e := range enumerated {
		if !derivedSet[e] {
			if !g.has(e) {
				t.Errorf("%s is enumerated as a deferred bulk-read caller but does not exist.", e)
				continue
			}
			t.Errorf("%s is enumerated as a deferred bulk-read caller but no longer calls the "+
				"legacy resolver surface.\n"+
				"    ADR-024 5a: the enumerated set is asserted EQUAL to the real caller set, so a\n"+
				"    stale entry fails too. If it was migrated to the snapshot, remove it here and\n"+
				"    add it to firstHalfConsumers.", e)
		}
	}

	if t.Failed() {
		t.Logf("derived callers of the legacy resolver surface: %v", derived)
		t.Logf("enumerated deferred bulk-read callers:          %v", enumerated)
	}
}

// --- assertion 2a: deferred -> snapshot must be unreachable ------------------

// deferredConsumerEntries returns the full deferred consumer inventory: the
// enumerated semantic entry points from 5a's matrix, plus every function declared
// in the files 5a names as deferred call sites. It fails the test if an
// enumerated entry no longer exists, or if a named call-site file has vanished --
// either would silently shrink what assertion 2a covers.
func deferredConsumerEntries(t *testing.T, g *guardCallGraph) []boundaryEntry {
	t.Helper()
	out := make([]boundaryEntry, 0, len(deferredBulkReadConsumers)+4)
	seen := map[string]bool{}
	for _, e := range deferredBulkReadConsumers {
		if !g.has(e.fn) {
			t.Fatalf("%s is enumerated as a deferred bulk-read consumer but does not exist.\n"+
				"    ADR-024 5a names it in the deferred row of the consumer matrix. If it was "+
				"renamed, update deferredBulkReadConsumers -- do not delete the entry, or the "+
				"entry point stops being guarded.", e.fn)
		}
		if seen[e.fn] {
			t.Fatalf("%s is enumerated twice in deferredBulkReadConsumers", e.fn)
		}
		seen[e.fn] = true
		out = append(out, e)
	}
	for _, rel := range deferredBulkReadConsumerFiles {
		fns := g.funcsInFile(rel)
		if len(fns) == 0 {
			t.Fatalf("ADR-024 5a names %s as a deferred call site, but the guard found no "+
				"functions declared there. If the file moved, update "+
				"deferredBulkReadConsumerFiles -- an empty derivation guards nothing.", rel)
		}
		for _, fn := range fns {
			if seen[fn] {
				continue
			}
			seen[fn] = true
			out = append(out, boundaryEntry{fn, "declared in " + rel + ", a call site ADR-024 5a names in the deferred row"})
		}
	}
	return out
}

// TestADR024Boundary_DeferredConsumersDoNotReachSnapshot holds 5a's deferred row
// as the matrix states it: the semantic ENTRY POINTS -- bulk status, map, gaps,
// triage listing, and the picker.go call sites -- may not reach project-wide
// snapshot construction or owner resolution, at any depth.
//
// This is the assertion the counterfactuals target. Injecting a
// testcase.NewSnapshot into reader.Gaps's or reader.Map's per-spec loop fails it
// and names the path; so does hoisting one snapshot to the top of either
// function, because 5a prohibits reaching project-wide resolution AT ALL, "not
// per record, and not once per operation".
//
// It exists because the derived caller set does not reach Gaps or Map at all:
// they resolve specs through reader.scanTestCases and name nothing on the legacy
// resolver surface, so a guard built only from directCallersOf() passed both
// injections unchanged.
func TestADR024Boundary_DeferredConsumersDoNotReachSnapshot(t *testing.T) {
	g := guardRepoGraph(t)
	targetKeys := append(append([]string{}, snapshotConstructors...), ownerResolutionAPIs...)
	for _, k := range targetKeys {
		if !g.has(k) {
			t.Fatalf("%s no longer exists; update snapshotConstructors / ownerResolutionAPIs "+
				"rather than letting the target set shrink.", k)
		}
	}
	targets := guardKeySet(targetKeys)

	for _, d := range deferredConsumerEntries(t, g) {
		t.Run(d.fn, func(t *testing.T) {
			if path := g.pathTo(d.fn, targets, nil); path != nil {
				t.Errorf("deferred bulk-read consumer %s reaches project-wide snapshot resolution.\n"+
					"    reachability path: %s\n"+
					"    %s\n"+
					"    ADR-024 5a: bulk status, map, gaps and triage listing keep their "+
					"pre-migration resolver until ENH-205 migrates them deliberately. They MUST NOT "+
					"reach project-wide snapshot resolution AT ALL -- not per record, and not once "+
					"per operation. Hoisting a single snapshot into a deferred consumer is "+
					"prohibited too: it changes that consumer's outcomes and diagnostics, which is "+
					"ENH-205's decision to make with its own policy matrix.",
					d.fn, strings.Join(path, " -> "), d.why)
			}
		})
	}
}

// TestADR024Boundary_DerivedCallersSitUnderEnumeratedConsumers ties the two
// deferred-side controls together. Every function the derived control classifies
// as a deferred caller of the legacy resolver must be reachable from at least one
// enumerated semantic entry point.
//
// Without this, the two sets can drift into being two disconnected lists: a
// per-record helper could keep the legacy resolver while the operation that used
// to call it has moved to the snapshot, and both controls would still pass -- the
// equality check because the helper is still enumerated, assertion 2a because the
// entry point no longer reaches it. This assertion fails instead, and names the
// helper that has been left behind.
func TestADR024Boundary_DerivedCallersSitUnderEnumeratedConsumers(t *testing.T) {
	g := guardRepoGraph(t)
	consumers := deferredConsumerEntries(t, g)

	for _, d := range deferredBulkReadCallers {
		t.Run(d.fn, func(t *testing.T) {
			if !g.has(d.fn) {
				t.Fatalf("%s does not exist", d.fn)
			}
			target := guardKeySet([]string{d.fn})
			for _, c := range consumers {
				if c.fn == d.fn {
					return // an entry point may be its own deferred caller
				}
				if g.pathTo(c.fn, target, nil) != nil {
					return
				}
			}
			var names []string
			for _, c := range consumers {
				names = append(names, c.fn)
			}
			t.Errorf("%s is classified as a deferred caller of the legacy resolver, but no "+
				"enumerated deferred consumer reaches it.\n"+
				"    %s\n"+
				"    ADR-024 5a defers whole OPERATIONS -- bulk status, map, gaps, triage listing. "+
				"A helper on the legacy resolver that no deferred operation calls is either dead, "+
				"or is now called from somewhere unclassified. Find its caller and classify that, "+
				"or remove the helper.\n"+
				"    enumerated deferred consumers: %v", d.fn, d.why, names)
		})
	}
}

// TestADR024Boundary_DeferredCallersDoNotReachSnapshot is the assertion the
// counterfactual targets. If ResolveSpec is changed back to calling NewSnapshot
// internally, with no deferred caller edited, this fails and names the path.
func TestADR024Boundary_DeferredCallersDoNotReachSnapshot(t *testing.T) {
	g := guardRepoGraph(t)
	ctors := guardKeySet(snapshotConstructors)
	for _, k := range snapshotConstructors {
		if !g.has(k) {
			t.Fatalf("snapshot constructor %s no longer exists; update snapshotConstructors.", k)
		}
	}

	for _, d := range deferredBulkReadCallers {
		t.Run(d.fn, func(t *testing.T) {
			if !g.has(d.fn) {
				t.Fatalf("%s does not exist", d.fn)
			}
			if path := g.pathTo(d.fn, ctors, nil); path != nil {
				t.Errorf("deferred bulk-read caller %s reaches a project-wide snapshot constructor.\n"+
					"    reachability path: %s\n"+
					"    %s\n"+
					"    ADR-024 5a: a deferred bulk-read caller MUST NOT reach project-wide snapshot\n"+
					"    resolution AT ALL -- not per record, and not once per operation. This is a\n"+
					"    prohibition on the IMPLEMENTATION: it is violated by changing what a shared\n"+
					"    helper does underneath, without touching this caller at all. Split the shared\n"+
					"    helper -- first-half callers move to the snapshot API, deferred callers keep\n"+
					"    the existing helper with its body unchanged.",
					d.fn, strings.Join(path, " -> "), d.why)
			}
		})
	}
}

// --- assertion 2b: first-half -> legacy resolver must be unreachable ---------

// TestADR024Boundary_FirstHalfConsumersDoNotReachLegacyResolver is the converse.
// Without it, a caller that acquires a mutating effect while remaining on the
// legacy resolver passes both other assertions unchanged -- set equality holds and
// snapshot reachability stays empty -- leaving a mutating consumer resolving
// identity by filename. That is the standing rejected approach "Lenient mutation".
func TestADR024Boundary_FirstHalfConsumersDoNotReachLegacyResolver(t *testing.T) {
	g := guardRepoGraph(t)
	surface := guardKeySet(legacyResolverSurface)
	deferred := guardKeySet(guardEntryKeys(deferredBulkReadCallers))

	for _, c := range firstHalfConsumers {
		t.Run(c.fn, func(t *testing.T) {
			if !g.has(c.fn) {
				t.Fatalf("%s does not exist; firstHalfConsumers is stale", c.fn)
			}
			if deferred[c.fn] {
				t.Errorf("%s is classified in BOTH rows.\n"+
					"    ADR-024 5a: a consumer that acquires a mutating effect leaves the deferred row.\n"+
					"    Remove it from deferredBulkReadCallers and move it to the snapshot API.", c.fn)
				return
			}
			if path := g.pathTo(c.fn, surface, deferred); path != nil {
				t.Errorf("first-half consumer %s reaches the legacy resolver.\n"+
					"    reachability path: %s\n"+
					"    %s\n"+
					"    ADR-024 5a: row-1 consumers use the snapshot and receive an explicit Owner.\n"+
					"    A read-friendly exception must not reach a writer or an adapter.\n"+
					"    (Paths through an enumerated DEFERRED caller are not reported: where a call\n"+
					"    site is reachable from both rows, the deferred row governs until ENH-205.)",
					c.fn, strings.Join(path, " -> "), c.why)
			}
		})
	}
}

// --- machinery self-test ----------------------------------------------------

// TestADR024Boundary_TransitivityIsActuallyTransitive proves the analyser detects
// a target reached through one level of indirection, and that a source-text check
// of the caller would not. It runs unconditionally, so the machinery cannot rot
// while the assertions above are skipped.
//
// The synthetic module mirrors the exact shape of the regression: a deferred
// caller calls a wrapper, the wrapper calls the resolver, and only the resolver
// names the constructor.
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
func ResolveSpec() int { return NewSnapshot() }
func FindSource() int  { return ResolveSpec() }
`)
	mk("internal/pipeline/resolve.go", `package pipeline
import "`+guardModulePrefix+`internal/testcase"
func ResolveTestCaseSpec() int { return testcase.FindSource() }
`)
	mk("internal/reader/picker.go", `package reader
import "`+guardModulePrefix+`internal/pipeline"
func ClassifyWiring() int { return pipeline.ResolveTestCaseSpec() }
func Unrelated() int      { return 1 }
`)

	g := buildGuardCallGraph(t, root, "internal")
	ctors := guardKeySet([]string{"testcase.NewSnapshot"})

	path := g.pathTo("reader.ClassifyWiring", ctors, nil)
	if path == nil {
		t.Fatal("the analyser missed a constructor reached through three levels of indirection; " +
			"assertion 2a would be decorative")
	}
	want := "reader.ClassifyWiring -> pipeline.ResolveTestCaseSpec -> testcase.FindSource -> testcase.ResolveSpec -> testcase.NewSnapshot"
	if got := strings.Join(path, " -> "); got != want {
		t.Errorf("reachability path = %q, want %q", got, want)
	}

	// The property that makes a source-text check insufficient: ClassifyWiring's own
	// body does not mention the constructor at all.
	src, err := os.ReadFile(filepath.Join(root, "internal", "reader", "picker.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "NewSnapshot") {
		t.Fatal("the fixture is wrong: the deferred caller must not name the constructor, " +
			"otherwise it does not demonstrate why a source-text check fails")
	}

	if g.pathTo("reader.Unrelated", ctors, nil) != nil {
		t.Error("the analyser invented a path from a function that calls nothing")
	}

	// Cutting the deferred caller removes the path, which is how 5a's
	// "deferred row governs" is encoded for assertion 2b.
	cut := guardKeySet([]string{"pipeline.ResolveTestCaseSpec"})
	if g.pathTo("reader.ClassifyWiring", ctors, cut) != nil {
		t.Error("cut nodes must not be expanded")
	}
}
