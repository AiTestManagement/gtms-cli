package testcase_test

// ADR-024 boundary guard -- EFFECT half (REV-184 R2-CODEX-001 / R2-CLAUDE-002).
//
// ADR-024 5b's end state: "no API that maps a bare ID to a spec path outside the
// snapshot exists. That last part is a source-shape assertion over the whole
// module, so a delegating wrapper cannot come back."
//
// The name blacklist in adr024_boundary_guard_test.go only stops the DELETED
// resolvers coming back under their old names. This file checks the EFFECT, so a
// new resolver under any name is caught. Outside internal/testcase (the snapshot's
// package) and internal/layout (which only defines the path, and does no I/O),
// non-test code must not:
//
//	(A) enumerate the test-cases tree directly: call filepath.Walk / WalkDir /
//	    Glob, os.ReadDir / DirFS, ioutil.ReadDir, fs.WalkDir / ReadDir / Glob, or
//	    (*os.File).Readdir / Readdirnames / ReadDir on a value derived from the
//	    cases root (os.Open / os.OpenFile pass the path's taint to the returned
//	    *os.File) -- directly, or by handing it to a helper that does;
//	(B) return, as a string result, a value derived from BOTH the cases root and
//	    one of its own non-root string parameters -- the shape of a bare-ID (or
//	    name) to spec-path resolver built by joining onto the tree. Parameters
//	    named root / projectRoot / repoRoot / projRoot are the project root and
//	    do not count; a helper that only names the cases directory is not flagged.
//	    A bare `return` counts as returning the named result variables; or
//	(C) take a non-root string parameter and return, as a string result, a value
//	    derived from a snapshot owner-path accessor -- Owner.SpecAbs() /
//	    SpecRel() (matched by method name on any receiver), testcase.OwnerAbsFor,
//	    or a Resolution's OwnerCanonicalAbs / OwnerLogicalRel / UnprovablePath
//	    field, directly or through a module function that returns one. That is
//	    the shape of a renamed snapshot-delegating resolver (bare ID in, spec path
//	    out). It is conservative: flow from the parameter to the result is not
//	    required, because the ID disappears into the snapshot call. A helper that
//	    takes an already-resolved *Owner and no other string is not flagged.
//
// "Derived from the cases root" is a taint analysis over the AST. Sources:
// layout.TestCasesDir(...); X.TestCases where X is layout.Current() or a variable
// assigned from it; a string literal naming a cases path ("gtms/test/cases",
// "gtms/cases", ...); a struct field any code assigns a tainted value (tracked by
// field name, module-wide -- so a path laundered through a struct such as
// ScopeInfo.ScanDir is still followed); and the result of any module function
// found to return a tainted string. Taint flows through assignment, range,
// append, the standard-library path/string builders in effPathBuilders
// (filepath.Join, filepath.Clean, fmt.Sprintf, strings.TrimPrefix ...), and any
// module function whose string parameter flows to its string result (a
// "passthrough", e.g. a canonicalising helper). Calls INTO internal/testcase are
// the sanctioned route and never propagate taint. A module function whose string
// parameter reaches a sink is a "walker"; passing it a tainted argument is itself
// a finding (A), so a generic walk(dir string) helper cannot hide the enumeration.
//
// The analysis over-approximates where that is cheap (shadowing is ignored,
// fields are matched by name). Over-approximation can only add findings; a
// legitimate finding is listed in effectGuardAllowlist with its reason, and an
// allowlist entry that no longer fires fails the guard so the list cannot rot.
//
// Known limits, stated so nobody over-reads the guard:
//   - A walk rooted ABOVE the cases tree (the project root) is not cases-rooted
//     and is not flagged. The two current project-root walks
//     (pipeline.ResolveArtefact, pipeline.DiscoverArtefact) look for automation
//     artefacts and skip the GTMS parent directory entirely.
//   - Walkers written as METHODS are not followed. Method calls are not resolved
//     (there is no go/types information), so a method with a string dir
//     parameter that walks it is invisible when a caller passes it the cases
//     root; a plain function is followed. (REV-184 round 3 CODEX-002, dismissed:
//     same class as the walker check, needs type resolution.)
//   - Walkers written as LOCAL CLOSURES are not followed. A func literal
//     assigned to a variable (walk := func(dir string) { filepath.Walk(dir, ...) })
//     and called with the cases root is not a module function, so it is never
//     recorded as a walker, and returns inside function literals are skipped.
//     A sink inside the closure on a CAPTURED tainted variable of the enclosing
//     function is still seen. (REV-184 round 3 CLAUDE-002, closure part.)
//   - Check (C) tracks owner paths through local variables, path/string
//     builders and module functions, not through struct fields.
//
// Every bulk read still has to reach the snapshot (property 1).

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// effectGuardAllowlist holds findings that are legitimate today, each with the
// reason it is not identity resolution. Keys are the finding strings the guard
// prints. Do not add an entry to get green without a reason a reviewer can check.
var effectGuardAllowlist = map[string]string{
	// gtms create's output dir defaults to the cases folder (invoker.go, "create"
	// and default cases). These are the PRODUCER side: GTMS lists that folder
	// before and after the adapter runs to find the files the adapter just wrote.
	// ADR-024 clause 1a keeps this contract ("ValidateCreateSpecs ... is NOT retired");
	// it decides no ownership of an existing spec.
	"adapter.InvokeWithRoot -> adapter.snapshotDir":         "create producer: pre-invoke listing of the create output folder, diffed later to find newly written files",
	"adapter.InvokeWithRoot -> adapter.handleSyncResult":    "create producer: scanOutputDir lists the create output folder after a sync run to report the files the adapter wrote",
	"adapter.InvokeWithRoot -> adapter.ValidateCreateSpecs": "create producer: BUG-038 check 3, kept by ADR-024 clause 1a -- validates specs the adapter just emitted against GTMS's pre-generated ids",
	// Field-name over-approximation: OutputDir is a cases folder only for create.
	// This call is gated to automate --force, where OutputDir is the automation
	// output dir (or PlannedArtefact's dir) and OutputSubdir is a relative folder.
	"adapter.InvokeWithRoot -> adapter.cleanupExistingOutputForOwner": "automate --force artefact sweep over the automation output dir; flagged only because the OutputDir field name is shared with create",
	// Relative-path and message helpers: they take an already-resolved path or a
	// folder name and produce a relative folder or user-facing text. None maps an
	// id to a spec file, and none of their results is used to open the tree.
	"adapter.deriveOutputSubdir: maps parameter testCaseSourcePath to a cases-tree path": "strips the cases prefix from a PROVEN owner's source path to get its relative subfolder for automation output",
	"cli.folderNotFoundMessage: maps parameter folder to a cases-tree path":              "user-facing error text: \"folder '<cases>/<folder>/' does not exist\"",
	"cli.folderNotFoundMessage: maps parameter prefix to a cases-tree path":              "user-facing error text (the dry-run marker prefix)",
	"cli.whatHappenedBulkAutomate: maps parameter folder to a cases-tree path":           "user-facing summary text: \"N automations created in gtms/test/cases/<folder>/\"",
	"cli.whatHappenedBulkExecute: maps parameter folder to a cases-tree path":            "user-facing summary text: \"N passed ... in gtms/test/cases/<folder>/\"",
}

// effectGuardExcludedPkgs are the packages whose code may touch the cases tree:
// testcase owns the snapshot; layout only names the directory.
var effectGuardExcludedPkgs = map[string]bool{"testcase": true, "layout": true}

var casesLiteralRE = regexp.MustCompile(`(^|/)cases(/|$)`)

// --- model -------------------------------------------------------------------

type effFunc struct {
	key     string
	pkg     string
	relFile string
	fset    *token.FileSet
	fd      *ast.FuncDecl
	imports map[string]string // local name -> full import path
}

type effState struct {
	funcs        map[string][]*effFunc
	fields       map[string]bool         // struct field names that carry a cases path
	returnsCases map[string]bool         // module funcs returning a cases path
	returnsOwner map[string]bool         // module funcs returning a snapshot owner path (check C)
	walkers      map[string]map[int]bool // module func -> string param indices reaching a sink
	passthrough  map[string]map[int]bool // module func -> string param indices flowing to a string result
	changed      bool
}

func loadEffFuncs(t *testing.T, root string, subtrees ...string) *effState {
	t.Helper()
	st := &effState{
		funcs:        map[string][]*effFunc{},
		fields:       map[string]bool{},
		returnsCases: map[string]bool{},
		returnsOwner: map[string]bool{},
		walkers:      map[string]map[int]bool{},
		passthrough:  map[string]map[int]bool{},
	}
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
			if effectGuardExcludedPkgs[pkg] {
				return nil
			}
			imports := map[string]string{}
			for _, im := range f.Imports {
				p, _ := strconv.Unquote(im.Path.Value)
				local := p[strings.LastIndex(p, "/")+1:]
				if im.Name != nil {
					local = im.Name.Name
				}
				imports[local] = p
			}
			rel, _ := filepath.Rel(root, path)
			for _, decl := range f.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok || fd.Name == nil || fd.Body == nil {
					continue
				}
				key := pkg + "." + fd.Name.Name
				st.funcs[key] = append(st.funcs[key], &effFunc{
					key: key, pkg: pkg, relFile: filepath.ToSlash(rel), fset: fset, fd: fd, imports: imports,
				})
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scanning %s: %v", base, err)
		}
	}
	return st
}

// --- taint over one function ---------------------------------------------------

type effTaint struct {
	f          *effFunc
	st         *effState
	sources    bool // false in walker mode: only the seeded parameter is tainted
	owner      bool // owner mode (check C): the sources are the snapshot owner-path accessors
	vars       map[string]bool
	layoutVals map[string]bool
}

func (c *effTaint) pkgOf(e ast.Expr) (string, bool) {
	id, ok := e.(*ast.Ident)
	if !ok {
		return "", false
	}
	p, ok := c.f.imports[id.Name]
	return p, ok
}

func isModulePath(p string) bool { return strings.HasPrefix(p, guardModulePrefix) }
func lastSeg(p string) string    { return p[strings.LastIndex(p, "/")+1:] }

// calleeKey returns the module function key a call targets, if any.
func (c *effTaint) calleeKey(ce *ast.CallExpr) string {
	switch fun := ce.Fun.(type) {
	case *ast.Ident:
		return c.f.pkg + "." + fun.Name
	case *ast.SelectorExpr:
		if p, ok := c.pkgOf(fun.X); ok {
			if isModulePath(p) {
				return lastSeg(p) + "." + fun.Sel.Name
			}
			return ""
		}
		return "" // method: not resolved without type information
	}
	return ""
}

func (c *effTaint) isLayoutCurrent(e ast.Expr) bool {
	ce, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := ce.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	p, ok := c.pkgOf(sel.X)
	return ok && p == guardModulePrefix+"internal/layout" && sel.Sel.Name == "Current"
}

func (c *effTaint) tainted(e ast.Expr) bool {
	switch x := e.(type) {
	case nil:
		return false
	case *ast.ParenExpr:
		return c.tainted(x.X)
	case *ast.Ident:
		return c.vars[x.Name]
	case *ast.BasicLit:
		if !c.sources || x.Kind != token.STRING {
			return false
		}
		s, err := strconv.Unquote(x.Value)
		return err == nil && casesLiteralRE.MatchString(s)
	case *ast.BinaryExpr:
		return c.tainted(x.X) || c.tainted(x.Y)
	case *ast.UnaryExpr:
		return c.tainted(x.X)
	case *ast.StarExpr:
		return c.tainted(x.X)
	case *ast.IndexExpr:
		return c.tainted(x.X)
	case *ast.SliceExpr:
		return c.tainted(x.X)
	case *ast.KeyValueExpr:
		return c.tainted(x.Value)
	case *ast.CompositeLit:
		for _, el := range x.Elts {
			if c.tainted(el) {
				return true
			}
		}
		return false
	case *ast.SelectorExpr:
		if c.owner && effOwnerFields[x.Sel.Name] {
			return true
		}
		if p, ok := c.pkgOf(x.X); ok {
			// Package-qualified constant/variable.
			return c.sources && p == guardModulePrefix+"internal/layoutmigration" &&
				strings.Contains(x.Sel.Name, "CasesDir")
		}
		if c.sources {
			if x.Sel.Name == "TestCases" {
				if c.isLayoutCurrent(x.X) {
					return true
				}
				if id, ok := x.X.(*ast.Ident); ok && c.layoutVals[id.Name] {
					return true
				}
			}
			if c.st.fields[x.Sel.Name] {
				return true
			}
		}
		return c.tainted(x.X)
	case *ast.CallExpr:
		anyArg := func(idx map[int]bool) bool {
			for i, a := range x.Args {
				if (idx == nil || idx[i]) && c.tainted(a) {
					return true
				}
			}
			return false
		}
		if c.owner {
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
				p, isPkg := c.pkgOf(sel.X)
				if !isPkg && effOwnerMethods[sel.Sel.Name] {
					return true // Owner.SpecAbs() / Owner.SpecRel(), any receiver
				}
				if isPkg && p == guardModulePrefix+"internal/testcase" && effOwnerFuncs[sel.Sel.Name] {
					return true
				}
			}
			if k := c.calleeKey(x); k != "" && c.st.returnsOwner[k] {
				return true
			}
		}
		switch fun := x.Fun.(type) {
		case *ast.Ident:
			switch fun.Name {
			case "append", "string":
				return anyArg(nil)
			}
		case *ast.SelectorExpr:
			p, isPkg := c.pkgOf(fun.X)
			if !isPkg {
				// Method calls are not resolved (no type information). Only the
				// directory-listing methods propagate, and those are sinks anyway.
				return false
			}
			if p == guardModulePrefix+"internal/testcase" {
				return false // the sanctioned route
			}
			if p == guardModulePrefix+"internal/layout" {
				return c.sources && fun.Sel.Name == "TestCasesDir"
			}
			if !isModulePath(p) {
				return effPathBuilders[p][fun.Sel.Name] && anyArg(nil)
			}
		}
		k := c.calleeKey(x)
		if k == "" {
			return false
		}
		if c.sources && c.st.returnsCases[k] {
			return true
		}
		return len(c.st.passthrough[k]) > 0 && anyArg(c.st.passthrough[k])
	}
	return false
}

func (c *effTaint) mark(lhs ast.Expr, t bool, rhs ast.Expr) {
	switch l := lhs.(type) {
	case *ast.Ident:
		if l.Name == "_" {
			return
		}
		// Local facts: propagate's own length check drives their fixpoint.
		if t {
			c.vars[l.Name] = true
		}
		if rhs != nil && c.isLayoutCurrent(rhs) {
			c.layoutVals[l.Name] = true
		}
	case *ast.SelectorExpr:
		if t && c.sources && !c.st.fields[l.Sel.Name] {
			c.st.fields[l.Sel.Name] = true
			c.st.changed = true
		}
	}
}

// propagate runs the function body to a local fixpoint.
func (c *effTaint) propagate() {
	for {
		before := len(c.vars) + len(c.layoutVals) + len(c.st.fields)
		ast.Inspect(c.f.fd.Body, func(n ast.Node) bool {
			switch s := n.(type) {
			case *ast.AssignStmt:
				if len(s.Rhs) == 1 && len(s.Lhs) > 1 {
					t := c.tainted(s.Rhs[0])
					for _, l := range s.Lhs {
						c.mark(l, t, nil)
					}
				} else {
					for i, l := range s.Lhs {
						if i < len(s.Rhs) {
							c.mark(l, c.tainted(s.Rhs[i]), s.Rhs[i])
						}
					}
				}
			case *ast.ValueSpec:
				if len(s.Values) == 1 && len(s.Names) > 1 {
					t := c.tainted(s.Values[0])
					for _, nm := range s.Names {
						c.mark(nm, t, nil)
					}
				} else {
					for i, nm := range s.Names {
						if i < len(s.Values) {
							c.mark(nm, c.tainted(s.Values[i]), s.Values[i])
						}
					}
				}
			case *ast.RangeStmt:
				if c.tainted(s.X) {
					c.mark(s.Key, true, nil)
					c.mark(s.Value, true, nil)
				}
			case *ast.CompositeLit:
				if c.sources {
					for _, el := range s.Elts {
						if kv, ok := el.(*ast.KeyValueExpr); ok {
							if k, ok := kv.Key.(*ast.Ident); ok && c.tainted(kv.Value) && !c.st.fields[k.Name] {
								c.st.fields[k.Name] = true
								c.st.changed = true
							}
						}
					}
				}
			}
			return true
		})
		if len(c.vars)+len(c.layoutVals)+len(c.st.fields) == before {
			return
		}
	}
}

// effPathBuilders are the standard-library calls whose result carries a tainted
// argument onward (path and string construction).
var effPathBuilders = map[string]map[string]bool{
	"path/filepath": {"Join": true, "Clean": true, "Abs": true, "EvalSymlinks": true, "Dir": true,
		"FromSlash": true, "ToSlash": true, "Rel": true},
	"path": {"Join": true, "Clean": true, "Dir": true},
	"fmt":  {"Sprintf": true, "Sprint": true},
	"strings": {"TrimSuffix": true, "TrimPrefix": true, "TrimRight": true, "TrimLeft": true, "TrimSpace": true,
		"ReplaceAll": true, "Replace": true, "ToLower": true, "Join": true},
	// os.Open / os.OpenFile carry the path's taint to the returned *os.File, so a
	// later (*os.File).Readdir / Readdirnames / ReadDir on it is a sink hit.
	"os": {"DirFS": true, "Open": true, "OpenFile": true},
}

// Check (C) sources: the snapshot's owner-path accessors. Methods are matched by
// name on any receiver (no type information); fields likewise.
var (
	effOwnerMethods = map[string]bool{"SpecAbs": true, "SpecRel": true}
	effOwnerFuncs   = map[string]bool{"OwnerAbsFor": true}
	effOwnerFields  = map[string]bool{"OwnerCanonicalAbs": true, "OwnerLogicalRel": true, "UnprovablePath": true}
)

// effRootParams are parameter names that carry the project root, not an ID.
var effRootParams = map[string]bool{"root": true, "projectRoot": true, "repoRoot": true, "projRoot": true}

var effSinks = map[string]map[string]bool{
	"path/filepath": {"Walk": true, "WalkDir": true, "Glob": true},
	"os":            {"ReadDir": true, "DirFS": true},
	"io/ioutil":     {"ReadDir": true},
	"io/fs":         {"WalkDir": true, "ReadDir": true, "Glob": true},
}

var effDirMethods = map[string]bool{"Readdir": true, "Readdirnames": true, "ReadDir": true}

// sinkHits lists the enumeration findings in the (already propagated) function.
func (c *effTaint) sinkHits() []string {
	var hits []string
	ast.Inspect(c.f.fd.Body, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		anyArg := func() bool {
			for _, a := range ce.Args {
				if c.tainted(a) {
					return true
				}
			}
			return false
		}
		if sel, ok := ce.Fun.(*ast.SelectorExpr); ok {
			if p, isPkg := c.pkgOf(sel.X); isPkg {
				if effSinks[p][sel.Sel.Name] && anyArg() {
					hits = append(hits, c.f.key+": "+lastSeg(p)+"."+sel.Sel.Name)
				}
			} else if effDirMethods[sel.Sel.Name] && c.tainted(sel.X) {
				hits = append(hits, c.f.key+": ."+sel.Sel.Name)
			}
		}
		if k := c.calleeKey(ce); k != "" {
			for i := range c.st.walkers[k] {
				if i < len(ce.Args) && c.tainted(ce.Args[i]) {
					hits = append(hits, c.f.key+" -> "+k)
					break
				}
			}
		}
		return true
	})
	return hits
}

// returnHits reports string results that return a tainted value. Returns inside
// function literals belong to the literal and are skipped.
func (c *effTaint) returnHits() bool { return returnsWhere(c.f.fd, c.tainted) }

// returnsWhere reports whether any return statement of fd (outside function
// literals) returns, in a string result position, an expression satisfying pred.
// A bare `return` in a function with named results returns those variables, so
// it is evaluated as returning each named result by name.
func returnsWhere(fd *ast.FuncDecl, pred func(ast.Expr) bool) bool {
	res := fd.Type.Results
	if res == nil {
		return false
	}
	var isStr []bool
	var named []string // result variable names by position ("" when unnamed)
	for _, fld := range res.List {
		id, ok := fld.Type.(*ast.Ident)
		s := ok && id.Name == "string"
		if len(fld.Names) == 0 {
			isStr = append(isStr, s)
			named = append(named, "")
			continue
		}
		for _, nm := range fld.Names {
			isStr = append(isStr, s)
			named = append(named, nm.Name)
		}
	}
	found := false
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		rs, ok := n.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		if len(rs.Results) == 0 {
			for i, nm := range named {
				if isStr[i] && nm != "" && nm != "_" && pred(&ast.Ident{Name: nm}) {
					found = true
				}
			}
			return true
		}
		if len(rs.Results) != len(isStr) {
			return true
		}
		for i, r := range rs.Results {
			if isStr[i] && pred(r) {
				found = true
			}
		}
		return true
	})
	return found
}

func stringParamNames(fd *ast.FuncDecl) []string {
	var names []string
	for _, fld := range fd.Type.Params.List {
		id, ok := fld.Type.(*ast.Ident)
		isStr := ok && id.Name == "string"
		if len(fld.Names) == 0 {
			names = append(names, "")
			continue
		}
		for _, nm := range fld.Names {
			if isStr {
				names = append(names, nm.Name)
			} else {
				names = append(names, "")
			}
		}
	}
	return names
}

// analyse runs the module-wide fixpoint and returns the sorted findings.
func (st *effState) analyse() []string {
	keys := make([]string, 0, len(st.funcs))
	for k := range st.funcs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for iter := 0; ; iter++ {
		st.changed = false
		for _, k := range keys {
			for _, f := range st.funcs[k] {
				c := &effTaint{f: f, st: st, sources: true, vars: map[string]bool{}, layoutVals: map[string]bool{}}
				c.propagate()
				if c.returnHits() && !st.returnsCases[k] {
					st.returnsCases[k] = true
					st.changed = true
				}
				o := &effTaint{f: f, st: st, owner: true, vars: map[string]bool{}, layoutVals: map[string]bool{}}
				o.propagate()
				if o.returnHits() && !st.returnsOwner[k] {
					st.returnsOwner[k] = true
					st.changed = true
				}
				for i, pn := range stringParamNames(f.fd) {
					if pn == "" || pn == "_" || (st.walkers[k][i] && st.passthrough[k][i]) {
						continue
					}
					w := &effTaint{f: f, st: st, vars: map[string]bool{pn: true}, layoutVals: map[string]bool{}}
					w.propagate()
					if !st.walkers[k][i] && len(w.sinkHits()) > 0 {
						if st.walkers[k] == nil {
							st.walkers[k] = map[int]bool{}
						}
						st.walkers[k][i] = true
						st.changed = true
					}
					if !st.passthrough[k][i] && w.returnHits() {
						if st.passthrough[k] == nil {
							st.passthrough[k] = map[int]bool{}
						}
						st.passthrough[k][i] = true
						st.changed = true
					}
				}
			}
		}
		if !st.changed {
			break
		}
		if iter > 50 {
			panic("effect guard fixpoint did not converge")
		}
	}

	set := map[string]bool{}
	for _, k := range keys {
		for _, f := range st.funcs[k] {
			c := &effTaint{f: f, st: st, sources: true, vars: map[string]bool{}, layoutVals: map[string]bool{}}
			c.propagate()
			for _, h := range c.sinkHits() {
				set[h] = true
			}
			// (B): a string result built from BOTH the cases root and a
			// non-root string parameter -- the shape of an ID (or folder/name)
			// to spec-path resolver. A parameter is root-like only by name
			// (effRootParams); every other string parameter counts.
			for _, pn := range stringParamNames(f.fd) {
				if pn == "" || pn == "_" || effRootParams[pn] {
					continue
				}
				w := &effTaint{f: f, st: st, vars: map[string]bool{pn: true}, layoutVals: map[string]bool{}}
				w.propagate()
				if returnsWhere(f.fd, func(r ast.Expr) bool { return c.tainted(r) && w.tainted(r) }) {
					set[k+": maps parameter "+pn+" to a cases-tree path"] = true
				}
			}
			// (C): a renamed snapshot-delegating resolver -- a function that
			// takes a non-root string parameter (an ID, a name, a path) and
			// returns, as a string, a value derived from a snapshot owner-path
			// accessor. Conservative: flow from the parameter is not required,
			// because the ID disappears into the snapshot call.
			if st.returnsOwner[k] {
				for _, pn := range stringParamNames(f.fd) {
					if pn != "" && pn != "_" && !effRootParams[pn] {
						set[k+": takes string parameter "+pn+" and returns a snapshot owner path"] = true
					}
				}
			}
		}
	}
	out := make([]string, 0, len(set))
	for h := range set {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// --- the guard -------------------------------------------------------------------

func TestADR024Boundary_NoDirectCasesTreeAccessOutsideSnapshot(t *testing.T) {
	st := loadEffFuncs(t, guardRepoRoot(t), "internal", "cmd")
	findings := st.analyse()

	fired := map[string]bool{}
	var bad []string
	for _, f := range findings {
		fired[f] = true
		if _, ok := effectGuardAllowlist[f]; !ok {
			bad = append(bad, f)
		}
	}
	if len(bad) > 0 {
		t.Errorf("non-test code outside internal/testcase touches the test-cases tree directly:\n    %s\n"+
			"    ADR-024 5b end state: identity is resolved only through the snapshot "+
			"(testcase.NewSnapshot / ResolveOwner / Snapshot.Resolve / RequireOwner / SelectFolder). "+
			"A direct walk or a cases-path-returning helper is a bare-ID resolver under a new name. "+
			"If the access is genuinely not identity resolution, add it to effectGuardAllowlist WITH a reason.",
			strings.Join(bad, "\n    "))
	}
	for k := range effectGuardAllowlist {
		if !fired[k] {
			t.Errorf("effectGuardAllowlist entry %q no longer fires; remove it so the list stays exact", k)
		}
	}
}

// TestADR024Boundary_EffectGuardMachinery proves the analyser catches every shape
// it claims to, and ignores the sanctioned route, so the guard cannot pass for the
// wrong reason.
func TestADR024Boundary_EffectGuardMachinery(t *testing.T) {
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
	const L = `"` + guardModulePrefix + `internal/layout"`
	const T = `"` + guardModulePrefix + `internal/testcase"`
	mk("internal/layout/layout.go", `package layout
type Paths struct{ TestCases string }
func Current() Paths { return Paths{} }
func TestCasesDir(r string) string { return r }
`)
	mk("internal/testcase/snap.go", `package testcase
import "path/filepath"
func NewSnapshot(r string) error { return filepath.Walk(r, nil) }
`)
	mk("internal/reader/r.go", `package reader
import (
	"os"
	"path/filepath"
	`+L+`
	`+T+`
)
type Scope struct{ ScanDir string }
func Direct(root string)            { _ = filepath.Walk(layout.TestCasesDir(root), nil) }
func ViaJoin(root, f string)        { d := filepath.Join(layout.TestCasesDir(root), f); os.ReadDir(d) }
func ViaCurrent(root string)        { p := layout.Current(); os.ReadDir(filepath.Join(root, p.TestCases)) }
func ViaLiteral(root string)        { os.ReadDir(filepath.Join(root, "gtms/test/cases")) }
func walk(dir string)               { _ = filepath.WalkDir(dir, nil) }
func relay(dir string)              { walk(dir) }
func ViaHelper(root string)         { relay(layout.TestCasesDir(root)) }
func Build(root string) Scope       { return Scope{ScanDir: layout.TestCasesDir(root)} }
func ViaField(s Scope)              { os.ReadDir(s.ScanDir) }
func casesRoot(root string) string  { return layout.TestCasesDir(root) }
func ViaReturn(root string)         { os.ReadDir(casesRoot(root)) }
func SpecPath(root, id string) string { return filepath.Join(layout.TestCasesDir(root), id+".md") }
func canon(p string) string          { return filepath.Clean(p) }
func ViaPassthrough(root string)    { os.ReadDir(canon(layout.TestCasesDir(root))) }
func Sanctioned(root string)        { _ = testcase.NewSnapshot(root) }
func Unrelated(root string)         { os.ReadDir(filepath.Join(root, ".gtms", "results")) }
func ViaOpen(root string)           { f, _ := os.Open(layout.TestCasesDir(root)); f.Readdirnames(-1) }
func ViaOpenFile(root string)       { f, _ := os.OpenFile(layout.TestCasesDir(root), os.O_RDONLY, 0); f.ReadDir(-1) }
func ViaOpenReaddir(root string)    { f, err := os.Open(layout.TestCasesDir(root)); _ = err; f.Readdir(-1) }
func openWalk(dir string)           { f, _ := os.Open(dir); f.Readdirnames(-1) }
func ViaOpenHelper(root string)     { openWalk(layout.TestCasesDir(root)) }
func OpenUnrelated(root string)     { f, _ := os.Open(filepath.Join(root, ".gtms")); f.Readdirnames(-1) }
func NamedSpec(root, id string) (p string) { p = filepath.Join(layout.TestCasesDir(root), id); return }
func NamedPair(root, id string) (p string, err error) { p = filepath.Join(layout.TestCasesDir(root), id+".md"); return }
func NamedUnrelated(root, id string) (p string) { p = filepath.Join(root, ".gtms", id); return }
`)
	mk("internal/lookup/l.go", `package lookup
import (
	"path/filepath"
	`+T+`
)
func SpecFor(s *testcase.Snapshot, id string) string { o, _ := s.RequireOwner(id); return o.SpecAbs() }
func RelFor(s *testcase.Snapshot, name string) (string, error) { return s.Resolve(name).OwnerLogicalRel, nil }
func AbsFor(root string, s *testcase.Snapshot, key string) string { return testcase.OwnerAbsFor(root, s.Get(key)) }
func CanonFor(s *testcase.Snapshot, id string) string { r := s.Resolve(id); return r.OwnerCanonicalAbs }
func Unprov(s *testcase.Snapshot, id string) string { return filepath.ToSlash(s.Resolve(id).UnprovablePath) }
func NamedRel(s *testcase.Snapshot, id string) (p string) { o, _ := s.RequireOwner(id); p = o.SpecRel(); return }
func viaOwner(o *testcase.Owner) string { return o.SpecAbs() }
func Indirect(s *testcase.Snapshot, id string) string { return viaOwner(s.Get(id)) }
func OwnerRootOnly(root string, o *testcase.Owner) string { return testcase.OwnerAbsFor(root, o) }
func NoOwnerPath(s *testcase.Snapshot, id string) string { return id }
`)
	st := loadEffFuncs(t, root, "internal")
	got := map[string]bool{}
	for _, f := range st.analyse() {
		got[f] = true
	}
	for _, want := range []string{
		"reader.Direct: filepath.Walk",
		"reader.ViaJoin: os.ReadDir",
		"reader.ViaCurrent: os.ReadDir",
		"reader.ViaLiteral: os.ReadDir",
		"reader.ViaHelper -> reader.relay",
		"reader.ViaField: os.ReadDir",
		"reader.ViaReturn: os.ReadDir",
		"reader.ViaPassthrough: os.ReadDir",
		"reader.SpecPath: maps parameter id to a cases-tree path",
		// os.Open / os.OpenFile taint the returned *os.File (CODEX-003 / CLAUDE-001).
		"reader.ViaOpen: .Readdirnames",
		"reader.ViaOpenFile: .ReadDir",
		"reader.ViaOpenReaddir: .Readdir",
		"reader.ViaOpenHelper -> reader.openWalk",
		// A bare return returns the named results (CODEX-004 / CLAUDE-002).
		"reader.NamedSpec: maps parameter id to a cases-tree path",
		"reader.NamedPair: maps parameter id to a cases-tree path",
		// (C) renamed snapshot-delegating resolvers (CODEX-001).
		"lookup.SpecFor: takes string parameter id and returns a snapshot owner path",
		"lookup.RelFor: takes string parameter name and returns a snapshot owner path",
		"lookup.AbsFor: takes string parameter key and returns a snapshot owner path",
		"lookup.CanonFor: takes string parameter id and returns a snapshot owner path",
		"lookup.Unprov: takes string parameter id and returns a snapshot owner path",
		"lookup.NamedRel: takes string parameter id and returns a snapshot owner path",
		"lookup.Indirect: takes string parameter id and returns a snapshot owner path",
	} {
		if !got[want] {
			t.Errorf("effect guard missed %q; findings were %v", want, keysOf(got))
		}
	}
	for f := range got {
		if strings.HasPrefix(f, "reader.Sanctioned") || strings.HasPrefix(f, "reader.Unrelated") ||
			strings.HasPrefix(f, "reader.casesRoot") || // root-only: names the directory, maps no ID
			strings.HasPrefix(f, "reader.OpenUnrelated") || strings.HasPrefix(f, "reader.NamedUnrelated") ||
			strings.HasPrefix(f, "lookup.viaOwner") || // no string parameter: takes a resolved owner
			strings.HasPrefix(f, "lookup.OwnerRootOnly") || // root-only string parameter
			strings.HasPrefix(f, "lookup.NoOwnerPath") ||
			strings.HasPrefix(f, "lookup.AbsFor: takes string parameter root") ||
			strings.HasPrefix(f, "testcase.") {
			t.Errorf("effect guard flagged a sanctioned or unrelated access: %q", f)
		}
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
