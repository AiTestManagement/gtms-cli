package testcase_test

// ADR-024 boundary guard -- IDENTITY-USE half (ENH-205 acceptance criterion
// "source-shape guards cover ... substring ownership, discarded identity errors").
//
// adr024_effect_guard_test.go stops code outside the snapshot from FINDING spec
// files (walks, ID-to-path resolvers). This file stops it from DECIDING identity
// or ownership any other way, and from THROWING AWAY the snapshot's answer. Both
// checks run over non-test code outside internal/testcase and internal/layout
// (the effect guard's loader; layout cannot import testcase and names paths only).
//
// (D) Substring ownership. ADR-024 4a allows exactly one filename test -- the
//     exact shortlist MatchesSpecFile, used inside the snapshot. Outside it, no
//     code may compare a test-case ID with a spec file name or path using a
//     string-matching operation: strings.HasPrefix / HasSuffix / Contains /
//     TrimPrefix / TrimSuffix / CutPrefix / CutSuffix / Index / LastIndex /
//     EqualFold / Count, path.Match / filepath.Match, regexp.MatchString, or
//     == / !=. Any call to testcase.MatchesSpecFile outside the package is also a
//     finding. To keep legitimate string work out of scope, a finding needs BOTH
//     operands to carry a taint:
//       - spec-path taint: a value derived from the cases root (the effect
//         guard's source analysis), from a snapshot owner-path accessor
//         (SpecAbs / SpecRel / OwnerAbsFor / OwnerCanonicalAbs / OwnerLogicalRel
//         / UnprovablePath), from a candidate's LogicalRel / CanonicalRel, or
//         from an identifier or field whose name says it holds a spec file or
//         path (specPath, specFile, SpecRel, SourceFile, TestCaseFile,
//         TestCaseSourcePath ...);
//       - ID taint: an identifier named id / ids or ending in tcID, testCaseID,
//         caseID, ownerID, targetID, requestedID, wantID (any case), a field of
//         one of those names or TestCase, or an .ID() method call.
//     Taint flows through assignment, range, +, the effect guard's path/string
//     builders plus filepath.Base / path.Base, and module "passthrough" helpers.
//     It also flows into and out of struct fields per local object and field:
//     `h.Value = o.SpecAbs()`, `h := T{Value: o.SpecAbs()}` (nested keyed
//     literals too, and a field selected straight off a literal,
//     outer{In: pair{Value: p}}.In.Value) and a copy `b := h` taint h.Value /
//     b.Value, and only that field. A copy is kept as an alias edge (b -> h)
//     resolved when a field is looked up, so chains of any depth keep their
//     taint and a cycle (h.Next = h) resolves h.Next.Value to h.Value and
//     terminates. Selecting from a WHOLE tainted value still does not propagate,
//     so identity parsed from a spec's contents (parse(specPath).ID) stays clean.
//     Spec-path taint (not ID taint) additionally flows from the first argument
//     of strings.Split / SplitN / SplitAfter / SplitAfterN / Cut / Fields and of
//     the regexp extraction methods FindString / FindStringSubmatch /
//     FindAllString / FindAllStringSubmatch / ReplaceAllString /
//     ReplaceAllLiteralString / ReplaceAllStringFunc (matched by name on any
//     receiver), including index and slice expressions on their results.
//     A module function whose string parameters, seeded one as a spec path and
//     one as an ID, reach such a comparison is a "matcher"; a call handing it a
//     spec path and an ID is itself a finding, so a helper cannot hide one.
//     Artefact, wiring and triage file names are NOT spec paths, so matching an
//     ID against them (e.g. reader.longestScopedID's triage shortlist) is not
//     flagged.
//
// (E) Discarded identity errors. The error or identity outcome from an
//     identity/ownership call must be consumed. Guarded calls:
//     testcase.NewSnapshot / ResolveOwner / OwnershipErrorFor / UnprovableInScope;
//     Snapshot.Resolve / ResolveScopedOwner / RequireOwner /
//     RequireOwnerUnderFolder / RequireOwnership / RequireFolderOwnership /
//     RequireMutationAuthorityUnderScope / SelectFolder; and every
//     package-level function in internal/reader named *WithDiagnostics or
//     *WithSnapshot, exported or not. The "carrier" results --
//     the positions typed error, *OwnershipError, *ScopeAuthorityError,
//     Resolution, []Candidate (SelectFolder's unprovable list) or
//     IdentityDiagnostics -- are read from the declarations themselves, so a
//     signature change is followed. A finding is a carrier that is (1) the
//     result of a call used as a statement (including go / defer), (2) assigned
//     to _, or (3) assigned to a variable that is not GUARANTEED to be read
//     before every exit or overwrite (the "unused err" that a re-used err
//     variable hides from the compiler). The read check follows control flow
//     conservatively (see readAfter): a read only inside `if debug { ... }`, or
//     after a return / panic, does not count; an if counts only when its
//     condition reads the value or every branch does; loops check both the next
//     iteration and the exit. A read in the right-hand side of the overwriting
//     assignment itself counts (`err = fmt.Errorf("x: %w", err)`); only the
//     left operand of && / || counts as a guaranteed read. Calling Affected() on
//     an IdentityDiagnostics carrier is NOT a read: it neither renders, returns
//     nor preserves the diagnostics (REV-184 R8-CLAUDE-001, human decision);
//     any other use of the value still counts. A bare return counts
//     as reading the named results. For a call with several carriers, any
//     return inside the body of an `if` whose condition proves one of its error
//     results non-nil (`err != nil`, `nil != err`, or && with one) also
//     consumes the other carriers on that path, since Go leaves the other
//     results meaningless then; the else branch and `err == nil` get no waiver.
//     The error in the condition must be the SAME variable (by declaration, not
//     spelling: `if err := write(); err != nil` is a different err), and it must
//     not have been reassigned between the call and the condition, textually or
//     around a loop that does not contain the call.
//     Snapshot methods are matched by name on any receiver (there is no type
//     information); the guard therefore also fails if any type outside
//     internal/testcase declares a method with one of those names.
//
// Known limits, stated so nobody over-reads the guard:
//   - (D) is name- and flow-based, not type-based. An ID held in a variable with
//     an unrelated name (x, s) and never derived from an ID-named value is
//     invisible; so is a spec path that never touched a spec-path source.
//     switch/case on a spec name and hand-written byte loops are not seen;
//     nor are positional struct literals (T{specPath}) or fields of slice / map
//     elements (rows[i].Value = ...).
//   - (D) and (E) ignore shadowing: a later read of a DIFFERENT variable with the
//     same name counts as using the value (can hide a discard); a write in a
//     sibling branch counts as an overwrite (can add a finding -- allowlist it).
//     The one exception is the (E) sibling-error waiver, which resolves the
//     error by declaration.
//   - (E) short-circuit reads are judged per condition, not per path: in
//     `(debug && err != nil) || err == nil` err is read on every path through
//     the alternate operands together, but no single operand guarantees it, so
//     the carrier is conservatively reported (REV-184 R7-CODEX-004, dismissed;
//     no such condition exists in product code).
//   - (E) the sibling-error reassignment check is textual, so it can withhold
//     the waiver when nothing was reassigned on the path: the guarded call's
//     own left-hand side in a for post clause counts as a later write to the
//     sibling (REV-184 R8-CODEX-001), and so does a write inside a function
//     literal that is never invoked (R8-CODEX-002). Both are false positives
//     (they add findings, never hide one), dismissed; neither occurs in
//     product code.
//   - (E) the "Affected() alone is not consumption" rule recognises only the
//     direct form `diags.Affected()`. The syntactic variants
//     `(diags.Affected)()` and the method expression
//     `reader.IdentityDiagnostics.Affected(diags)` still count as reading the
//     carrier, so they can hide a discard (REV-184 R9-CODEX-001, Medium,
//     dismissed under the stopping rule by human decision 2026-09-25; no such
//     form exists in product code, and an exhaustive caller review found no
//     real diagnostic drop).
//   - Walkers/matchers written as methods or local closures are not followed,
//     as in the effect guard.
//
// Every finding that is legitimate goes in the allowlist below with a reason; an
// allowlist entry that no longer fires fails the guard so the list cannot rot.

import (
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
	"time"
)

// substringGuardAllowlist holds (D) findings that are legitimate, each with the
// reason it is not an ownership decision.
var substringGuardAllowlist = map[string]string{}

// discardGuardAllowlist holds (E) findings that are legitimate, each with the
// reason the discarded carrier cannot carry an identity error there.
var discardGuardAllowlist = map[string]string{
	"cli.resolveBulkOwners: Snapshot.SelectFolder result 2 discarded via _":      "RequireFolderOwnership(scopeRel, recursive) on the same snapshot returned nil on the line before; it fails whenever this unprovable list is non-empty, so the discarded value is always empty",
	"link.AuthoriseScopeRepoint: Snapshot.SelectFolder result 2 discarded via _": "RequireFolderOwnership(scopeRel, recursive) on the same snapshot returned nil on the line before; it fails whenever this unprovable list is non-empty, so the discarded value is always empty",
}

// ============================================================================
// (D) substring ownership
// ============================================================================

var (
	subSpecNameRE = regexp.MustCompile(`(?i)(spec(path|file|filename|rel|abs|name|base|basename|stem)s?$|^(testcasefile|testcasesourcepath|sourcefile|tcfile|tcpath|logicalrel|canonicalrel)$)`)
	subIDNameRE   = regexp.MustCompile(`(?i)(^ids?$|(tc|testcase|case|owner|target|requested|want)ids?$|^testcase$)`)

	// subMatchOps are the string-matching calls (D) inspects: both of the first
	// two arguments are operands.
	subMatchOps = map[string]map[string]bool{
		"strings": {"HasPrefix": true, "HasSuffix": true, "Contains": true, "TrimPrefix": true,
			"TrimSuffix": true, "CutPrefix": true, "CutSuffix": true, "Index": true, "LastIndex": true,
			"EqualFold": true, "Count": true},
		"path":          {"Match": true},
		"path/filepath": {"Match": true},
		"regexp":        {"MatchString": true},
	}
)

// subBuilders extends the effect guard's path/string builders with the basename
// functions a filename comparison uses.
func subBuilder(p, name string) bool {
	if effPathBuilders[p][name] {
		return true
	}
	return (p == "path/filepath" || p == "path") && name == "Base"
}

var (
	// subSplitters carry spec-path taint from their FIRST argument (the string
	// being taken apart) to every result, so a piece of a spec file name is still
	// a spec path: strings.Split(filepath.Base(specPath), "-")[0].
	subSplitters = map[string]map[string]bool{
		"strings": {"Split": true, "SplitN": true, "SplitAfter": true, "SplitAfterN": true,
			"Cut": true, "Fields": true},
	}
	// subExtractMethods are the regexp extraction methods, matched by name on any
	// receiver (no type information); like subSplitters they carry spec-path taint
	// from their first argument: re.FindStringSubmatch(filepath.Base(specPath))[1].
	subExtractMethods = map[string]bool{"FindString": true, "FindStringSubmatch": true,
		"FindAllString": true, "FindAllStringSubmatch": true, "ReplaceAllString": true,
		"ReplaceAllLiteralString": true, "ReplaceAllStringFunc": true}
)

const (
	subKindSpec = iota
	subKindID
)

type subState struct {
	eff      *effState
	matchers map[string]map[[2]int]bool // module func -> (spec param, id param) pairs reaching a match
	changed  bool
}

type subTaint struct {
	f      *effFunc
	s      *subState
	kind   int
	seeded bool // seeded mode: only the seeded parameters are tainted
	hook   func(ast.Expr) bool
	vars   map[string]bool
	alias  map[string]map[string]bool // copy edges: field key prefix -> source key prefix
}

func (c *subTaint) nameSource(name string) bool {
	if c.seeded {
		return false
	}
	if c.kind == subKindSpec {
		return subSpecNameRE.MatchString(name)
	}
	return subIDNameRE.MatchString(name)
}

func (c *subTaint) pkgOf(e ast.Expr) (string, bool) { return (&effTaint{f: c.f}).pkgOf(e) }

func (c *subTaint) tainted(e ast.Expr) bool {
	if e == nil {
		return false
	}
	if !c.seeded && c.hook != nil && c.hook(e) {
		return true
	}
	switch x := e.(type) {
	case *ast.ParenExpr:
		return c.tainted(x.X)
	case *ast.Ident:
		return c.vars[x.Name] || c.nameSource(x.Name)
	case *ast.BinaryExpr:
		return x.Op == token.ADD && (c.tainted(x.X) || c.tainted(x.Y))
	case *ast.StarExpr:
		return c.tainted(x.X)
	case *ast.UnaryExpr:
		return c.tainted(x.X)
	case *ast.IndexExpr:
		return c.tainted(x.X)
	case *ast.SliceExpr:
		return c.tainted(x.X)
	case *ast.SelectorExpr:
		if _, isPkg := c.pkgOf(x.X); isPkg {
			return false
		}
		// A field is a spec path or an ID by its own name, or because this function
		// stored one into that field of that local object (h.Value = o.SpecAbs(),
		// h := T{Value: o.SpecAbs()}). Selecting from a tainted WHOLE value does
		// not propagate: a struct parsed FROM a spec file carries body identity
		// (tc.ID), which is the sanctioned answer.
		if k := c.selKey(x); k != "" && c.keyTainted(k) {
			return true
		}
		if v := litSel(x); v != nil && c.tainted(v) {
			return true
		}
		return c.nameSource(x.Sel.Name)
	case *ast.CallExpr:
		anyArg := func(idx map[int]bool) bool {
			for i, a := range x.Args {
				if (idx == nil || idx[i]) && c.tainted(a) {
					return true
				}
			}
			return false
		}
		switch fun := x.Fun.(type) {
		case *ast.Ident:
			if fun.Name == "append" || fun.Name == "string" {
				return anyArg(nil)
			}
		case *ast.SelectorExpr:
			p, isPkg := c.pkgOf(fun.X)
			if !isPkg {
				// Methods are not resolved; only Owner.ID()-style accessors source an
				// ID, and regexp extraction methods carry a spec path onward.
				if c.kind == subKindSpec && subExtractMethods[fun.Sel.Name] {
					return len(x.Args) > 0 && c.tainted(x.Args[0])
				}
				return c.kind == subKindID && !c.seeded && fun.Sel.Name == "ID" && len(x.Args) == 0
			}
			if p == guardModulePrefix+"internal/testcase" {
				return false
			}
			if !isModulePath(p) {
				if c.kind == subKindSpec && subSplitters[p][fun.Sel.Name] {
					return len(x.Args) > 0 && c.tainted(x.Args[0])
				}
				return subBuilder(p, fun.Sel.Name) && anyArg(nil)
			}
		}
		k := (&effTaint{f: c.f}).calleeKey(x)
		if k == "" {
			return false
		}
		return len(c.s.eff.passthrough[k]) > 0 && anyArg(c.s.eff.passthrough[k])
	}
	return false
}

// selKey names a selector chain rooted at a local identifier ("h", "h.Value",
// "h.In.Value"), or returns "" (package-qualified, call results, index ...).
// Field taint is kept in vars under these dotted keys, which no plain
// identifier can collide with.
func (c *subTaint) selKey(e ast.Expr) string {
	switch x := unparen(e).(type) {
	case *ast.Ident:
		return x.Name
	case *ast.StarExpr:
		return c.selKey(x.X)
	case *ast.SelectorExpr:
		if _, isPkg := c.pkgOf(x.X); isPkg {
			return ""
		}
		if b := c.selKey(x.X); b != "" {
			return b + "." + x.Sel.Name
		}
	}
	return ""
}

// litSel resolves a field selection through keyed composite literals, however
// deeply nested: outer{In: pair{Value: v}}.In.Value is v. It returns nil when
// the chain does not bottom out in a literal holding that key.
func litSel(x *ast.SelectorExpr) ast.Expr {
	base := unparen(x.X)
	if inner, ok := base.(*ast.SelectorExpr); ok {
		if base = litSel(inner); base == nil {
			return nil
		}
	}
	return litField(base, x.Sel.Name)
}

// compositeOf returns the composite literal e is (possibly behind & or parens).
func compositeOf(e ast.Expr) *ast.CompositeLit {
	e = unparen(e)
	if u, ok := e.(*ast.UnaryExpr); ok && u.Op == token.AND {
		e = unparen(u.X)
	}
	lit, _ := e.(*ast.CompositeLit)
	return lit
}

// litField returns the value keyed name in the composite literal e, if any.
// Positional literals carry no field names and are not followed.
func litField(e ast.Expr, name string) ast.Expr {
	lit := compositeOf(e)
	if lit == nil {
		return nil
	}
	for _, el := range lit.Elts {
		if kv, ok := el.(*ast.KeyValueExpr); ok {
			if k, ok := kv.Key.(*ast.Ident); ok && k.Name == name {
				return kv.Value
			}
		}
	}
	return nil
}

// mark records that lhs holds a tainted value (t) and, per local object and
// field, which fields of lhs hold one: from a keyed composite literal on the
// right (recursively), or -- for a copy of another local object or field -- an
// alias edge lhs -> rhs that keyTainted follows when a field is looked up.
func (c *subTaint) mark(lhs, rhs ast.Expr, t bool) {
	k := c.selKey(lhs)
	if k == "" || k == "_" {
		return
	}
	if t {
		c.vars[k] = true
	}
	if rhs == nil {
		return
	}
	if lit := compositeOf(rhs); lit != nil {
		for _, el := range lit.Elts {
			if kv, ok := el.(*ast.KeyValueExpr); ok {
				if f, ok := kv.Key.(*ast.Ident); ok {
					c.mark(&ast.SelectorExpr{X: ast.NewIdent(k), Sel: f}, kv.Value, c.tainted(kv.Value))
				}
			}
		}
		return
	}
	if rk := c.selKey(rhs); rk != "" && rk != k {
		if c.alias == nil {
			c.alias = map[string]map[string]bool{}
		}
		if c.alias[k] == nil {
			c.alias[k] = map[string]bool{}
		}
		c.alias[k][rk] = true
	}
}

// edgeCount is the number of alias edges, for the propagation fixpoint.
func (c *subTaint) edgeCount() int {
	n := 0
	for _, d := range c.alias {
		n += len(d)
	}
	return n
}

// keyTainted reports whether the field key holds a tainted value: stored there
// directly, or reached by rewriting a prefix through alias edges (b.Value with
// b -> h is h.Value). Cycles are detected rather than cut off by depth: a key
// already on the current rewrite chain is not revisited, and an edge that
// LENGTHENS the key may be used only once per chain. Every key on a chain is
// then bounded in length, so the walk terminates, while finite acyclic chains
// of any depth resolve fully.
func (c *subTaint) keyTainted(key string) bool {
	onPath := map[string]bool{}
	grown := map[[2]string]bool{}
	var walk func(k string) bool
	walk = func(k string) bool {
		if c.vars[k] {
			return true
		}
		if onPath[k] {
			return false
		}
		onPath[k] = true
		defer delete(onPath, k)
		for src, dsts := range c.alias {
			if k != src && !strings.HasPrefix(k, src+".") {
				continue
			}
			for dst := range dsts {
				nk := dst + k[len(src):]
				if strings.Count(nk, ".") > strings.Count(k, ".") {
					e := [2]string{src, dst}
					if grown[e] {
						continue // this lengthening edge is already on the chain: a cycle
					}
					grown[e] = true
					hit := walk(nk)
					delete(grown, e)
					if hit {
						return true
					}
					continue
				}
				if walk(nk) {
					return true
				}
			}
		}
		return false
	}
	return walk(key)
}

func (c *subTaint) propagate() {
	for {
		before := len(c.vars) + c.edgeCount()
		ast.Inspect(c.f.fd.Body, func(n ast.Node) bool {
			switch s := n.(type) {
			case *ast.AssignStmt:
				if len(s.Rhs) == 1 && len(s.Lhs) > 1 {
					t := c.tainted(s.Rhs[0])
					for _, l := range s.Lhs {
						c.mark(l, nil, t)
					}
				} else {
					for i, l := range s.Lhs {
						if i < len(s.Rhs) {
							c.mark(l, s.Rhs[i], c.tainted(s.Rhs[i]))
						}
					}
				}
			case *ast.ValueSpec:
				if len(s.Values) == 1 && len(s.Names) > 1 {
					t := c.tainted(s.Values[0])
					for _, nm := range s.Names {
						c.mark(nm, nil, t)
					}
				} else {
					for i, nm := range s.Names {
						if i < len(s.Values) {
							c.mark(nm, s.Values[i], c.tainted(s.Values[i]))
						}
					}
				}
			case *ast.RangeStmt:
				if c.tainted(s.X) {
					c.mark(s.Key, nil, true)
					c.mark(s.Value, nil, true)
				}
			}
			return true
		})
		if len(c.vars)+c.edgeCount() == before {
			return
		}
	}
}

// subHits lists the (D) findings in f given a propagated spec taint and ID taint.
func subHits(f *effFunc, s *subState, spec, id *subTaint) []string {
	var hits []string
	pair := func(a, b ast.Expr) bool {
		return (spec.tainted(a) && id.tainted(b)) || (id.tainted(a) && spec.tainted(b))
	}
	ast.Inspect(f.fd.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BinaryExpr:
			if (x.Op == token.EQL || x.Op == token.NEQ) && pair(x.X, x.Y) {
				hits = append(hits, f.key+": "+x.Op.String()+" compares a spec path with a test-case ID")
			}
		case *ast.CallExpr:
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
				if p, isPkg := spec.pkgOf(sel.X); isPkg {
					if p == guardModulePrefix+"internal/testcase" && sel.Sel.Name == "MatchesSpecFile" {
						hits = append(hits, f.key+": calls testcase.MatchesSpecFile outside the snapshot")
					}
					if subMatchOps[p][sel.Sel.Name] && len(x.Args) >= 2 && pair(x.Args[0], x.Args[1]) {
						hits = append(hits, f.key+": "+lastSeg(p)+"."+sel.Sel.Name+" matches a spec path against a test-case ID")
					}
				}
			}
			if k := (&effTaint{f: f}).calleeKey(x); k != "" {
				for pr := range s.matchers[k] {
					if pr[0] < len(x.Args) && pr[1] < len(x.Args) &&
						spec.tainted(x.Args[pr[0]]) && id.tainted(x.Args[pr[1]]) {
						hits = append(hits, f.key+" -> "+k+": passes a spec path and a test-case ID to a matcher")
						break
					}
				}
			}
		}
		return true
	})
	return hits
}

// analyseSubstring runs (D) over an already-analysed effect state.
func analyseSubstring(eff *effState) []string {
	s := &subState{eff: eff, matchers: map[string]map[[2]int]bool{}}
	keys := make([]string, 0, len(eff.funcs))
	for k := range eff.funcs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for iter := 0; ; iter++ {
		s.changed = false
		for _, k := range keys {
			for _, f := range eff.funcs[k] {
				params := stringParamNames(f.fd)
				for i, pi := range params {
					for j, pj := range params {
						if i == j || pi == "" || pj == "" || pi == "_" || pj == "_" || s.matchers[k][[2]int{i, j}] {
							continue
						}
						sp := &subTaint{f: f, s: s, kind: subKindSpec, seeded: true, vars: map[string]bool{pi: true}}
						id := &subTaint{f: f, s: s, kind: subKindID, seeded: true, vars: map[string]bool{pj: true}}
						sp.propagate()
						id.propagate()
						if len(subHits(f, s, sp, id)) > 0 {
							if s.matchers[k] == nil {
								s.matchers[k] = map[[2]int]bool{}
							}
							s.matchers[k][[2]int{i, j}] = true
							s.changed = true
						}
					}
				}
			}
		}
		if !s.changed {
			break
		}
		if iter > 50 {
			panic("substring guard fixpoint did not converge")
		}
	}

	set := map[string]bool{}
	for _, k := range keys {
		for _, f := range eff.funcs[k] {
			// The effect guard's module-wide struct-field taint (ScanDir, OutputDir,
			// and by name-collision Results ...) marks directories and records, not
			// spec paths, so (D) consults its cases/owner sources on a copy with
			// that field set emptied, and never through a field selector other than
			// layout's TestCases and the snapshot's owner-path fields.
			noFields := *eff
			noFields.fields = map[string]bool{}
			cases := &effTaint{f: f, st: &noFields, sources: true, vars: map[string]bool{}, layoutVals: map[string]bool{}}
			cases.propagate()
			owner := &effTaint{f: f, st: &noFields, owner: true, vars: map[string]bool{}, layoutVals: map[string]bool{}}
			owner.propagate()
			hook := func(e ast.Expr) bool {
				if sel, ok := e.(*ast.SelectorExpr); ok {
					if _, isPkg := cases.pkgOf(sel.X); !isPkg && sel.Sel.Name != "TestCases" && !effOwnerFields[sel.Sel.Name] {
						return false
					}
				}
				return cases.tainted(e) || owner.tainted(e)
			}
			sp := &subTaint{f: f, s: s, kind: subKindSpec, vars: map[string]bool{}, hook: hook}
			id := &subTaint{f: f, s: s, kind: subKindID, vars: map[string]bool{}}
			sp.propagate()
			id.propagate()
			for _, h := range subHits(f, s, sp, id) {
				set[h] = true
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

func TestADR024Boundary_NoSubstringOwnershipOutsideSnapshot(t *testing.T) {
	st := loadEffFuncs(t, guardRepoRoot(t), "internal", "cmd")
	st.analyse()
	checkAllowlisted(t, analyseSubstring(st), substringGuardAllowlist, "substringGuardAllowlist",
		"non-test code outside internal/testcase decides test-case identity by matching an ID against a spec file name or path",
		"ADR-024 4a: the only filename test is the exact shortlist inside the snapshot (MatchesSpecFile); "+
			"ownership is decided by body identity via testcase.NewSnapshot / Snapshot.Resolve / RequireOwner. "+
			"If the comparison is genuinely not an ownership decision, add it to substringGuardAllowlist WITH a reason.")
}

// TestADR024Boundary_SubstringGuardMachinery proves (D) catches each shape it
// claims to and leaves ordinary string work, and ID matching against non-spec
// file names, alone.
func TestADR024Boundary_SubstringGuardMachinery(t *testing.T) {
	root := t.TempDir()
	writeGuardModule(t, root, map[string]string{
		"internal/layout/layout.go": `package layout
func TestCasesDir(r string) string { return r }
`,
		"internal/testcase/snap.go": `package testcase
type Owner struct{}
func (o *Owner) SpecAbs() string { return "" }
func (o *Owner) ID() string { return "" }
func MatchesSpecFile(base, tcID string) bool { return false }
`,
		"internal/reader/r.go": `package reader
import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	` + guardQuote("internal/layout") + `
	` + guardQuote("internal/testcase") + `
)
type Ref struct{ Path string }
func Prefix(specPath, id string) bool { return strings.HasPrefix(filepath.Base(specPath), id+"-") }
func Contains(o *testcase.Owner, tcID string) bool { return strings.Contains(o.SpecAbs(), tcID) }
func Match(root, id string) bool { m, _ := filepath.Match(id+"*", filepath.Join(layout.TestCasesDir(root), "x.md")); return m }
func Equal(specFile string, o *testcase.Owner) bool { return filepath.Base(specFile) == o.ID()+".md" }
func Trim(tc struct{ SourceFile, ID string }) string { return strings.TrimPrefix(filepath.Base(tc.SourceFile), tc.ID) }
func Regex(o *testcase.Owner, ownerID string) bool { ok, _ := regexp.MatchString("^"+ownerID, o.SpecAbs()); return ok }
func Shortlist(specName, id string) bool { return testcase.MatchesSpecFile(specName, id) }
func owns(name, key string) bool { return strings.HasPrefix(name, key+"-") }
func relay(a, b string) bool { return owns(a, b) }
func ViaHelper(o *testcase.Owner, id string) bool { return relay(filepath.Base(o.SpecAbs()), id) }
func Laundered(o *testcase.Owner, id string) bool { p := o.SpecAbs(); base := filepath.Base(p); return strings.HasSuffix(base, id+".md") }
// Field-carried taint: neutral field names (Value, Key) on both sides.
type pair struct{ Value, Key string }
type outer struct{ In pair }
func FieldAssign(o *testcase.Owner, tcID string) bool { var h pair; h.Value = o.SpecAbs(); return strings.HasPrefix(h.Value, tcID) }
func FieldLiteral(o *testcase.Owner, tcID string) bool { h := pair{Value: o.SpecAbs()}; return strings.Contains(h.Value, tcID) }
func FieldBothSides(o *testcase.Owner, id string) bool { h := &pair{Value: filepath.Base(o.SpecAbs()), Key: id}; return strings.HasPrefix(h.Value, h.Key) }
func FieldIDSide(specPath string, o *testcase.Owner) bool { var h pair; h.Key = o.ID(); return filepath.Base(specPath) == h.Key+".md" }
func FieldCopy(o *testcase.Owner, ownerID string) bool { a := pair{Value: o.SpecAbs()}; b := a; return b.Value == ownerID+".md" }
func FieldNested(o *testcase.Owner, id string) bool { x := outer{In: pair{Value: o.SpecAbs()}}; return strings.HasSuffix(x.In.Value, id+".md") }
func FieldInline(o *testcase.Owner, id string) bool { return pair{Value: o.SpecAbs()}.Value == id }
func FieldInlineNested(o *testcase.Owner, id string) bool { return outer{In: pair{Value: o.SpecAbs()}}.In.Value == id }
// Self-referential and cyclic field copies must terminate (REV-184 round 6).
type node struct{ Value string; Next *node }
func SelfRef(o *testcase.Owner, id string) bool { h := &node{Value: o.SpecAbs()}; h.Next = h; return strings.HasPrefix(h.Value, id) }
// A self-alias still resolves h.Next.Value to h.Value; an acyclic chain of any
// depth keeps its taint; a lengthening cycle terminates (REV-184 round 7).
func SelfRefField(o *testcase.Owner, id string) bool { h := &node{Value: o.SpecAbs()}; h.Next = h; return h.Next.Value == id }
func DeepChain(o *testcase.Owner, id string) bool { var h deep; h.A.B.C.D.E.F.G.H = o.SpecAbs(); b := h; return b.A.B.C.D.E.F.G.H == id }
func GrowCycle(o *testcase.Owner, id string) bool { n := &node{Value: o.SpecAbs()}; a := n.Next.Next; n.Next = a; return a.Next.Value == id }
func CycleRef(o *testcase.Owner, id string) bool { a := &node{Value: o.SpecAbs()}; b := &node{}; a.Next = b; b.Next = a; return strings.HasPrefix(b.Next.Value, id) }
// Split / regexp extraction of a spec file name.
var idRE = regexp.MustCompile("^(tc-[0-9]+)")
func SplitBase(specPath, id string) bool { return strings.Split(filepath.Base(specPath), "-")[0] == id }
func RegexExtract(specPath, id string) bool { return idRE.FindStringSubmatch(filepath.Base(specPath))[1] == id }
func CutBase(o *testcase.Owner, id string) bool { before, _, _ := strings.Cut(filepath.Base(o.SpecAbs()), "--"); return before == id }
func FieldsBase(specFile, id string) bool { parts := strings.Fields(specFile); return parts[0][:3] == id }
// Not flagged: ID against a triage/wiring/artefact file name.
func TriageShortlist(r Ref, id string) bool { return strings.HasPrefix(filepath.Base(r.Path), id+"--") }
func ArtefactName(name, ownerID string) bool { return strings.HasPrefix(name, ownerID+"-") }
// Not flagged: spec path against a non-ID string, or two IDs.
func SpecSuffix(specPath string) bool { return strings.HasSuffix(specPath, ".md") }
func SpecUnderFolder(specPath, folder string) bool { return strings.HasPrefix(specPath, folder+"/") }
func SameID(o *testcase.Owner, id string) bool { return o.ID() == id }
func Unrelated(version string) bool { return strings.HasPrefix(version, "v") }
func DirEntries(dir, id string) bool { es, _ := os.ReadDir(dir); return len(es) > 0 && strings.HasPrefix(es[0].Name(), id) }
func HelperNonSpec(r Ref, id string) bool { return relay(filepath.Base(r.Path), id) }
// Not flagged: body identity read FROM a spec file, and ID fields of records
// whose field names collide with the effect guard's laundered-path field set.
type fm struct{ ID string }
func parse(p string) fm { return fm{} }
func BodyIdentity(specPath, id string) bool { tc := parse(specPath); return tc.ID == id }
type rec struct{ Results []struct{ TCID string } }
func RecordRows(r rec, tcID string) bool { for _, row := range r.Results { if row.TCID != tcID { return false } }; return true }
func Build(root string) rec { var r rec; r.Results = append(r.Results, struct{ TCID string }{layout.TestCasesDir(root)}); return r }
// Not flagged: a field holding identity parsed FROM the spec's contents, a
// different field of an object whose other field holds a spec path, a field fed
// from a non-spec path, and split / regexp work on non-spec strings.
func FieldFromContents(specPath, id string) bool { h := pair{Value: parse(specPath).ID}; return h.Value == id }
func FieldOtherKey(o *testcase.Owner, id string) bool { h := pair{Value: o.SpecAbs(), Key: "x"}; return strings.HasPrefix(h.Key, id) }
func FieldNonSpec(r Ref, id string) bool { var h pair; h.Value = r.Path; return strings.HasPrefix(h.Value, id) }
func SplitNonSpec(version, id string) bool { return strings.Split(version, ".")[0] == id }
func RegexNonSpec(name, id string) bool { return idRE.FindString(name) == id }
func SplitSeparatorOnly(specPath, name, id string) bool { return strings.Split(name, specPath)[0] == id }
func FieldInlineNestedOther(o *testcase.Owner, id string) bool { return outer{In: pair{Value: o.SpecAbs(), Key: "k"}}.In.Key == id }
`,
	})
	st := loadEffFuncs(t, root, "internal")
	st.analyse()
	// A self-referential field copy once grew selector keys without bound; run
	// the analysis under a deadline so a regression fails instead of hanging.
	done := make(chan []string, 1)
	go func() { done <- analyseSubstring(st) }()
	var findings []string
	select {
	case findings = <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("substring guard did not terminate within 60s (self-referential field copy?)")
	}
	got := map[string]bool{}
	for _, f := range findings {
		got[f] = true
	}
	for _, want := range []string{
		"reader.Prefix: strings.HasPrefix matches a spec path against a test-case ID",
		"reader.Contains: strings.Contains matches a spec path against a test-case ID",
		"reader.Match: filepath.Match matches a spec path against a test-case ID",
		"reader.Equal: == compares a spec path with a test-case ID",
		"reader.Trim: strings.TrimPrefix matches a spec path against a test-case ID",
		"reader.Regex: regexp.MatchString matches a spec path against a test-case ID",
		"reader.Shortlist: calls testcase.MatchesSpecFile outside the snapshot",
		"reader.ViaHelper -> reader.relay: passes a spec path and a test-case ID to a matcher",
		"reader.Laundered: strings.HasSuffix matches a spec path against a test-case ID",
		"reader.FieldAssign: strings.HasPrefix matches a spec path against a test-case ID",
		"reader.FieldLiteral: strings.Contains matches a spec path against a test-case ID",
		"reader.FieldBothSides: strings.HasPrefix matches a spec path against a test-case ID",
		"reader.FieldIDSide: == compares a spec path with a test-case ID",
		"reader.FieldCopy: == compares a spec path with a test-case ID",
		"reader.FieldNested: strings.HasSuffix matches a spec path against a test-case ID",
		"reader.FieldInline: == compares a spec path with a test-case ID",
		"reader.FieldInlineNested: == compares a spec path with a test-case ID",
		"reader.SelfRef: strings.HasPrefix matches a spec path against a test-case ID",
		"reader.CycleRef: strings.HasPrefix matches a spec path against a test-case ID",
		"reader.SelfRefField: == compares a spec path with a test-case ID",
		"reader.DeepChain: == compares a spec path with a test-case ID",
		"reader.SplitBase: == compares a spec path with a test-case ID",
		"reader.RegexExtract: == compares a spec path with a test-case ID",
		"reader.CutBase: == compares a spec path with a test-case ID",
		"reader.FieldsBase: == compares a spec path with a test-case ID",
	} {
		if !got[want] {
			t.Errorf("substring guard missed %q; findings were %v", want, keysOf(got))
		}
	}
	for f := range got {
		for _, clean := range []string{"reader.TriageShortlist", "reader.ArtefactName", "reader.SpecSuffix",
			"reader.SpecUnderFolder", "reader.SameID", "reader.Unrelated", "reader.DirEntries",
			"reader.HelperNonSpec", "reader.BodyIdentity", "reader.RecordRows", "reader.owns", "reader.relay",
			"reader.FieldFromContents", "reader.FieldOtherKey", "reader.FieldNonSpec", "reader.SplitNonSpec",
			"reader.RegexNonSpec", "reader.SplitSeparatorOnly", "reader.FieldInlineNestedOther", "testcase."} {
			if strings.HasPrefix(f, clean) {
				t.Errorf("substring guard flagged legitimate string work: %q", f)
			}
		}
	}
}

// ============================================================================
// (E) discarded identity errors
// ============================================================================

// discSnapshotMethods are the guarded *Snapshot methods; discTestcaseFuncs the
// guarded package functions. Reader functions are selected by discReaderRE.
var (
	discSnapshotMethods = []string{"Resolve", "ResolveScopedOwner", "RequireOwner", "RequireOwnerUnderFolder",
		"RequireOwnership", "RequireFolderOwnership", "RequireMutationAuthorityUnderScope", "SelectFolder"}
	discTestcaseFuncs = []string{"NewSnapshot", "ResolveOwner", "OwnershipErrorFor", "UnprovableInScope"}
	discReaderRE      = regexp.MustCompile(`(WithDiagnostics|WithSnapshot)$`)
	discCarrierTypes  = map[string]bool{"error": true, "*OwnershipError": true, "*ScopeAuthorityError": true,
		"Resolution": true, "[]Candidate": true, "IdentityDiagnostics": true}
)

type discAPI struct {
	nres     int
	carriers map[int]bool
	errs     map[int]bool // carrier positions typed error
	diags    map[int]bool // carrier positions typed IdentityDiagnostics
}

func typeString(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.StarExpr:
		return "*" + typeString(x.X)
	case *ast.ArrayType:
		if x.Len == nil {
			return "[]" + typeString(x.Elt)
		}
	case *ast.SelectorExpr:
		return typeString(x.Sel)
	}
	return "?"
}

func resultShape(fd *ast.FuncDecl) discAPI {
	a := discAPI{carriers: map[int]bool{}, errs: map[int]bool{}, diags: map[int]bool{}}
	if fd.Type.Results == nil {
		return a
	}
	for _, fld := range fd.Type.Results.List {
		n := len(fld.Names)
		if n == 0 {
			n = 1
		}
		for i := 0; i < n; i++ {
			if discCarrierTypes[typeString(fld.Type)] {
				a.carriers[a.nres] = true
			}
			if typeString(fld.Type) == "error" {
				a.errs[a.nres] = true
			}
			if typeString(fld.Type) == "IdentityDiagnostics" {
				a.diags[a.nres] = true
			}
			a.nres++
		}
	}
	return a
}

// loadDiscAPI reads the guarded declarations from internal/testcase and
// internal/reader, keyed "testcase.F", "Snapshot.M" and "reader.F". Every named
// guarded testcase function and method must be found with at least one carrier.
func loadDiscAPI(t *testing.T, root string) map[string]discAPI {
	t.Helper()
	api := map[string]discAPI{}
	want := map[string]bool{}
	for _, m := range discSnapshotMethods {
		want["Snapshot."+m] = true
	}
	for _, f := range discTestcaseFuncs {
		want["testcase."+f] = true
	}
	for _, sub := range []string{"internal/testcase", "internal/reader"} {
		files, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(sub), "*.go"))
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				t.Fatalf("parsing %s: %v", path, err)
			}
			for _, d := range f.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok {
					continue
				}
				// The fixed testcase API is exported by definition; reader functions
				// are matched by suffix whether exported or not, so an unexported
				// helper returning the same carriers is guarded too.
				var key string
				switch {
				case f.Name.Name == "testcase" && fd.Name.IsExported() && fd.Recv != nil && typeString(fd.Recv.List[0].Type) == "*Snapshot":
					key = "Snapshot." + fd.Name.Name
				case f.Name.Name == "testcase" && fd.Name.IsExported() && fd.Recv == nil:
					key = "testcase." + fd.Name.Name
				case f.Name.Name == "reader" && fd.Recv == nil && discReaderRE.MatchString(fd.Name.Name):
					key = "reader." + fd.Name.Name
					want[key] = true
				}
				if want[key] {
					api[key] = resultShape(fd)
				}
			}
		}
	}
	readers := 0
	for k := range want {
		a, ok := api[k]
		if !ok || len(a.carriers) == 0 {
			t.Errorf("discard guard: guarded identity API %s not found with an error/outcome result; "+
				"update the guard's API list rather than let it go unguarded", k)
		}
		if strings.HasPrefix(k, "reader.") {
			readers++
		}
	}
	if readers == 0 {
		t.Errorf("discard guard: no reader *WithDiagnostics / *WithSnapshot function found")
	}
	return api
}

type discChecker struct {
	f    *effFunc
	api  map[string]discAPI
	hits []string
}

func (d *discChecker) callee(e ast.Expr) (string, discAPI, bool) {
	ce, ok := unparen(e).(*ast.CallExpr)
	if !ok {
		return "", discAPI{}, false
	}
	var key string
	switch fun := ce.Fun.(type) {
	case *ast.Ident:
		key = d.f.pkg + "." + fun.Name
	case *ast.SelectorExpr:
		if p, isPkg := (&effTaint{f: d.f}).pkgOf(fun.X); isPkg {
			if !isModulePath(p) {
				return "", discAPI{}, false
			}
			key = lastSeg(p) + "." + fun.Sel.Name
		} else {
			key = "Snapshot." + fun.Sel.Name
		}
	}
	a, ok := d.api[key]
	return key, a, ok
}

func unparen(e ast.Expr) ast.Expr {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			return e
		}
		e = p.X
	}
}

func (d *discChecker) add(key string, pos int, how string) {
	d.hits = append(d.hits, d.f.key+": "+key+" result "+strconv.Itoa(pos+1)+" discarded "+how)
}

// run walks the function body keeping the ancestor stack (for enclosing loops).
func (d *discChecker) run() {
	var stack []ast.Node
	ast.Inspect(d.f.fd.Body, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		switch s := n.(type) {
		case *ast.ExprStmt:
			if key, a, ok := d.callee(s.X); ok {
				d.addAll(key, a, "(call statement)")
			}
		case *ast.GoStmt:
			if key, a, ok := d.callee(s.Call); ok {
				d.addAll(key, a, "(go statement)")
			}
		case *ast.DeferStmt:
			if key, a, ok := d.callee(s.Call); ok {
				d.addAll(key, a, "(defer statement)")
			}
		case *ast.AssignStmt:
			d.assign(s.Lhs, s.Rhs, s, stack)
		case *ast.ValueSpec:
			lhs := make([]ast.Expr, len(s.Names))
			for i, nm := range s.Names {
				lhs[i] = nm
			}
			d.assign(lhs, s.Values, s, stack)
		}
		stack = append(stack, n)
		return true
	})
}

func (d *discChecker) addAll(key string, a discAPI, how string) {
	for i := 0; i < a.nres; i++ {
		if a.carriers[i] {
			d.add(key, i, how)
		}
	}
}

func (d *discChecker) assign(lhs, rhs []ast.Expr, stmt ast.Node, stack []ast.Node) {
	check := func(key string, pos int, l ast.Expr, siblingErrs map[*ast.Object]bool, isDiags bool) {
		id, ok := l.(*ast.Ident)
		if !ok {
			return // stored into a field / element: kept
		}
		if id.Name == "_" {
			d.add(key, pos, "via _")
			return
		}
		if !d.readAfter(id.Name, siblingErrs, isDiags, stmt, stack) {
			d.add(key, pos, "(value "+id.Name+" not read on every path)")
		}
	}
	if len(rhs) == 1 && len(lhs) > 1 {
		if key, a, ok := d.callee(rhs[0]); ok && a.nres == len(lhs) {
			for i := range lhs {
				if !a.carriers[i] {
					continue
				}
				// The same call's OTHER error results: returning one of them under
				// a test of it propagates the call's failure (Go's "err != nil means
				// the other results are meaningless").
				// Keyed by declaration (the parser's object), not by spelling, so a
				// shadowing `if err := write(); err != nil` is a different err.
				sib := map[*ast.Object]bool{}
				for j, l := range lhs {
					if id, ok := l.(*ast.Ident); ok && j != i && a.errs[j] && id.Name != "_" && id.Obj != nil {
						sib[id.Obj] = true
					}
				}
				check(key, i, lhs[i], sib, a.diags[i])
			}
		}
		return
	}
	for i, r := range rhs {
		if i >= len(lhs) {
			break
		}
		if key, a, ok := d.callee(r); ok && a.nres == 1 && a.carriers[0] {
			check(key, 0, lhs[i], nil, a.diags[0])
		}
	}
}

// readAfter reports whether the value assigned to name by stmt is GUARANTEED to
// be read: on every path from stmt, a read of name comes before any overwrite of
// it and before the function (or enclosing function literal) exits. A read only
// on some paths -- inside `if debug { log(err) }`, or after a return -- does not
// count. The walk is structural and conservative:
//   - a statement reads the value when an expression it evaluates up front
//     names it; an assignment's right-hand side is evaluated before its
//     left-hand side is written, so `err = fmt.Errorf("x: %w", err)` reads;
//   - an if reads it when its init or condition does, or when every branch does
//     (a missing else is a branch that does not);
//   - a switch reads it when its init, tag or first case expression does, or
//     when every clause does (no default is a clause that does not);
//   - a for / range body may run zero times, unless the for has no condition;
//   - return (other than a bare return of named results), panic, os.Exit,
//     log.Fatal*, goto, fallthrough and labelled break / continue end the path
//     unread, so statements after them in the block are never reached;
//   - at the end of a loop body (or on continue) the post statement and loop
//     condition run, then both the next iteration and the loop exit must read;
//     an unlabelled break continues after the loop / switch / select;
//   - only the left operand of && / || is always evaluated, so a read only in
//     the right one (`debug || err != nil`) is not guaranteed;
//   - after a post statement that is the checked assignment itself, the walk
//     continues at the loop condition, body and exit (the post is not re-run);
//   - siblingErrs are the same call's other error results: ANY return inside
//     the body of an if whose condition proves one of them non-nil (`err !=
//     nil`, `nil != err`, or `&&` with one of those) is the call's failure path
//     and so consumes this result too (`r, diags, err := f(); if err != nil {
//     return err }`). Not in the else branch, not under `err == nil`, and not
//     under a condition that proves nothing; a plain `return r, err` does not.
//     The tested error must be the same declaration as the sibling, and must
//     not be reassigned between the call and the condition (see reassigned).
func (d *discChecker) readAfter(name string, siblingErrs map[*ast.Object]bool, isDiags bool, stmt ast.Node, stack []ast.Node) bool {
	fl := &discFlow{name: name, siblings: siblingErrs, isDiags: isDiags, call: stmt, body: d.f.fd.Body,
		writes: map[*ast.Ident]bool{}, loops: map[ast.Node]int{}, sibWrites: map[*ast.Object][]token.Pos{}}
	sibWrite := func(e ast.Expr) {
		if id, ok := e.(*ast.Ident); ok && id.Obj != nil && siblingErrs[id.Obj] {
			fl.sibWrites[id.Obj] = append(fl.sibWrites[id.Obj], id.Pos())
		}
	}
	ast.Inspect(d.f.fd.Body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			if s.Tok == token.ASSIGN || s.Tok == token.DEFINE {
				for _, l := range s.Lhs {
					if id, ok := l.(*ast.Ident); ok {
						fl.writes[id] = true
					}
					sibWrite(l)
				}
			}
		case *ast.ValueSpec:
			for _, nm := range s.Names {
				fl.writes[nm] = true
				sibWrite(nm)
			}
		case *ast.RangeStmt:
			sibWrite(s.Key)
			sibWrite(s.Value)
			fl.loopNodes = append(fl.loopNodes, s)
		case *ast.ForStmt:
			fl.loopNodes = append(fl.loopNodes, s)
		}
		return true
	})
	// A bare return reads the named results of the innermost enclosing function.
	ftype := d.f.fd.Type
	for _, anc := range stack {
		if lit, ok := anc.(*ast.FuncLit); ok {
			ftype = lit.Type
		}
	}
	if ftype.Results != nil {
		for _, fld := range ftype.Results.List {
			for _, nm := range fld.Names {
				if nm.Name == name {
					fl.namedResult = true
				}
			}
		}
	}
	// The statement holding a ValueSpec is the DeclStmt around its GenDecl.
	cur, st := stmt, stack
	if _, ok := stmt.(*ast.ValueSpec); ok {
		for i := len(stack) - 1; i >= 0; i-- {
			if ds, ok := stack[i].(*ast.DeclStmt); ok {
				cur, st = ds, stack[:i]
				break
			}
		}
	}
	return fl.after(st, cur)
}

type discFlow struct {
	name        string
	siblings    map[*ast.Object]bool // the same call's other error results, by declaration
	call        ast.Node             // the statement holding the guarded call
	body        *ast.BlockStmt
	sibWrites   map[*ast.Object][]token.Pos
	loopNodes   []ast.Node
	errPath     int // > 0 inside the body of an if proving a sibling non-nil
	writes      map[*ast.Ident]bool
	namedResult bool
	isDiags     bool             // the value is an IdentityDiagnostics: x.Affected() alone is not a read
	loops       map[ast.Node]int // loopBack memo: 1 in progress, 2 reads, 3 does not
}

// flowOut summarises the paths through a statement that have NOT read the
// value: they fall through to what follows, are lost (overwritten or the
// function exits), or leave by an unlabelled break or continue. All false means
// every path through the statement reads the value.
type flowOut struct{ through, lost, brk, cont bool }

func (o flowOut) or(p flowOut) flowOut {
	return flowOut{o.through || p.through, o.lost || p.lost, o.brk || p.brk, o.cont || p.cont}
}

var (
	flowRead = flowOut{}
	flowPass = flowOut{through: true}
	flowLost = flowOut{lost: true}
)

// then sequences first and the rest of a statement: only first's unread
// fall-through paths reach rest.
func (o flowOut) then(rest func() flowOut) flowOut {
	if !o.through {
		return o
	}
	r := rest()
	return flowOut{r.through, o.lost || r.lost, o.brk || r.brk, o.cont || r.cont}
}

// reads reports whether evaluating n is guaranteed to name the value other than
// as an assignment target. A selector's field name is not the variable (x.err
// is not err). Only the left operand of && and || is always evaluated, so a
// mention only in the right one (debug || err != nil) is not a guaranteed read.
func (fl *discFlow) reads(n ast.Node) bool { return fl.mentions(n, fl.name) }

func (fl *discFlow) mentions(n ast.Node, name string) bool {
	if n == nil {
		return false
	}
	found := false
	ast.Inspect(n, func(m ast.Node) bool {
		if found {
			return false
		}
		switch x := m.(type) {
		case *ast.CallExpr:
			// diags.Affected() only asks whether anything was affected; it does not
			// render, return or preserve the diagnostics (REV-184 R8-CLAUDE-001).
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok && fl.isDiags && sel.Sel.Name == "Affected" && len(x.Args) == 0 {
				if id, ok := unparen(sel.X).(*ast.Ident); ok && id.Name == name {
					return false
				}
			}
		case *ast.BinaryExpr:
			if x.Op == token.LAND || x.Op == token.LOR {
				found = fl.mentions(x.X, name)
				return false
			}
		case *ast.SelectorExpr:
			found = fl.mentions(x.X, name)
			return false
		case *ast.Ident:
			if x.Name == name && !fl.writes[x] {
				found = true
			}
		}
		return true
	})
	return found
}

// ifBranches evaluates an if's body and else. When the condition PROVES a
// sibling error non-nil, the body is the call's failure path: Go leaves the
// call's other results meaningless there, so any return inside the body
// consumes this one. The else branch, and a body under a condition that does
// not prove it (err == nil, debug || err != nil, errors.Is(err, x)), get no
// such waiver.
func (fl *discFlow) ifBranches(x *ast.IfStmt) flowOut {
	el := flowPass
	if x.Else != nil {
		el = fl.stmt(x.Else)
	}
	proven := fl.provesSiblingErr(x.Cond)
	if proven {
		fl.errPath++
	}
	out := fl.seq(x.Body.List)
	if proven {
		fl.errPath--
	}
	return out.or(el)
}

// provesSiblingErr: cond is true only when a sibling error is non-nil --
// `err != nil`, `nil != err`, or a conjunction with one of those as an operand.
func (fl *discFlow) provesSiblingErr(cond ast.Expr) bool {
	b, ok := unparen(cond).(*ast.BinaryExpr)
	if !ok {
		return false
	}
	switch b.Op {
	case token.LAND:
		return fl.provesSiblingErr(b.X) || fl.provesSiblingErr(b.Y)
	case token.NEQ:
		isNil := func(e ast.Expr) bool { id, ok := unparen(e).(*ast.Ident); return ok && id.Name == "nil" }
		isSib := func(e ast.Expr) bool {
			id, ok := unparen(e).(*ast.Ident)
			return ok && id.Obj != nil && fl.siblings[id.Obj] && !fl.reassigned(id.Obj, id.Pos())
		}
		return (isSib(b.X) && isNil(b.Y)) || (isNil(b.X) && isSib(b.Y))
	}
	return false
}

// reassigned: the sibling declared as obj may hold a different value at pos
// than the guarded call left in it -- a write to it lies textually between the
// call statement and pos (an if-init assignment included) and is not in a
// branch that excludes the call, or lies inside a loop that contains pos and
// can reach pos on a later iteration.
func (fl *discFlow) reassigned(obj *ast.Object, pos token.Pos) bool {
	in := func(n ast.Node, p token.Pos) bool { return n.Pos() <= p && p < n.End() }
	for _, w := range fl.sibWrites[obj] {
		excl := fl.exclusive(fl.call.Pos(), w)
		if fl.call.End() <= w && w < pos && !excl {
			return true
		}
		for _, l := range fl.loopNodes {
			if in(l, pos) && in(l, w) && (!in(l, fl.call.Pos()) || w >= pos || excl) {
				return true
			}
		}
	}
	return false
}

// exclusive: positions a and b lie in different branches of the same if/else
// or in different clauses of the same switch / select, so one run through
// that statement cannot execute both.
func (fl *discFlow) exclusive(a, b token.Pos) bool {
	chain := func(p token.Pos) []ast.Node {
		var out []ast.Node
		ast.Inspect(fl.body, func(n ast.Node) bool {
			if n == nil || p < n.Pos() || p >= n.End() {
				return false
			}
			out = append(out, n)
			return true
		})
		return out
	}
	ca, cb := chain(a), chain(b)
	i := 0
	for i < len(ca) && i < len(cb) && ca[i] == cb[i] {
		i++
	}
	if i == 0 || i >= len(ca) || i >= len(cb) {
		return false
	}
	switch lca := ca[i-1].(type) {
	case *ast.IfStmt:
		isBranch := func(n ast.Node) bool { return n == ast.Node(lca.Body) || (lca.Else != nil && n == ast.Node(lca.Else)) }
		return isBranch(ca[i]) && isBranch(cb[i])
	case *ast.BlockStmt:
		_, okA := ca[i].(*ast.CaseClause)
		_, okB := cb[i].(*ast.CaseClause)
		_, commA := ca[i].(*ast.CommClause)
		_, commB := cb[i].(*ast.CommClause)
		return (okA && okB) || (commA && commB)
	}
	return false
}

func (fl *discFlow) seq(list []ast.Stmt) flowOut {
	acc := flowRead
	for _, s := range list {
		o := fl.stmt(s)
		acc = flowOut{false, acc.lost || o.lost, acc.brk || o.brk, acc.cont || o.cont}
		if !o.through {
			return acc
		}
	}
	acc.through = true
	return acc
}

func isExitCall(e ast.Expr) bool {
	ce, ok := unparen(e).(*ast.CallExpr)
	if !ok {
		return false
	}
	switch fun := ce.Fun.(type) {
	case *ast.Ident:
		return fun.Name == "panic"
	case *ast.SelectorExpr:
		if pk, ok := fun.X.(*ast.Ident); ok {
			switch pk.Name {
			case "os":
				return fun.Sel.Name == "Exit"
			case "log":
				return strings.HasPrefix(fun.Sel.Name, "Fatal") || strings.HasPrefix(fun.Sel.Name, "Panic")
			}
		}
	}
	return false
}

// stmt evaluates s entered on a path that has not yet read the value.
func (fl *discFlow) stmt(s ast.Stmt) flowOut {
	switch x := s.(type) {
	case nil:
		return flowPass
	case *ast.ExprStmt:
		if fl.reads(x.X) {
			return flowRead
		}
		if isExitCall(x.X) {
			return flowLost
		}
		return flowPass
	case *ast.AssignStmt:
		if fl.reads(x) {
			return flowRead // right-hand side, index/selector targets, op-assign
		}
		for _, l := range x.Lhs {
			if id, ok := l.(*ast.Ident); ok && id.Name == fl.name {
				return flowLost // overwritten (a := in an inner scope is counted too)
			}
		}
		return flowPass
	case *ast.DeclStmt:
		if fl.reads(x) {
			return flowRead
		}
		if gd, ok := x.Decl.(*ast.GenDecl); ok {
			for _, sp := range gd.Specs {
				if vs, ok := sp.(*ast.ValueSpec); ok {
					for _, nm := range vs.Names {
						if nm.Name == fl.name {
							return flowLost
						}
					}
				}
			}
		}
		return flowPass
	case *ast.ReturnStmt:
		if fl.reads(x) || (len(x.Results) == 0 && fl.namedResult) || fl.errPath > 0 {
			return flowRead
		}
		return flowLost
	case *ast.BranchStmt:
		if x.Label == nil && x.Tok == token.BREAK {
			return flowOut{brk: true}
		}
		if x.Label == nil && x.Tok == token.CONTINUE {
			return flowOut{cont: true}
		}
		return flowLost // goto, fallthrough, labelled break / continue
	case *ast.BlockStmt:
		return fl.seq(x.List)
	case *ast.LabeledStmt:
		return fl.stmt(x.Stmt)
	case *ast.IfStmt:
		return fl.stmt(x.Init).then(func() flowOut {
			if fl.reads(x.Cond) {
				return flowRead
			}
			return fl.ifBranches(x)
		})
	case *ast.SwitchStmt:
		return fl.stmt(x.Init).then(func() flowOut {
			if fl.reads(x.Tag) || fl.firstCaseReads(x.Body) {
				return flowRead
			}
			return fl.clauses(x.Body, false)
		})
	case *ast.TypeSwitchStmt:
		return fl.stmt(x.Init).then(func() flowOut {
			if fl.reads(x.Assign) {
				return flowRead
			}
			return fl.clauses(x.Body, false)
		})
	case *ast.SelectStmt:
		return fl.clauses(x.Body, true)
	case *ast.ForStmt:
		return fl.stmt(x.Init).then(func() flowOut {
			if fl.reads(x.Cond) {
				return flowRead
			}
			b := fl.seq(x.Body.List)
			// With no condition the loop leaves only by break (or return / panic);
			// a pass that loops again starts from the same unread state.
			return flowOut{through: x.Cond != nil || b.brk, lost: b.lost}
		})
	case *ast.RangeStmt:
		if fl.reads(x.X) {
			return flowRead
		}
		if fl.rangeWrites(x) {
			return flowLost
		}
		return flowOut{through: true, lost: fl.seq(x.Body.List).lost}
	case *ast.GoStmt, *ast.DeferStmt, *ast.SendStmt, *ast.IncDecStmt:
		if fl.reads(s) {
			return flowRead
		}
	}
	return flowPass
}

func (fl *discFlow) rangeWrites(r *ast.RangeStmt) bool {
	for _, e := range []ast.Expr{r.Key, r.Value} {
		if id, ok := e.(*ast.Ident); ok && id.Name == fl.name {
			return true
		}
	}
	return false
}

// firstCaseReads: the first case expression of a switch is always evaluated.
func (fl *discFlow) firstCaseReads(body *ast.BlockStmt) bool {
	for _, c := range body.List {
		if cl, ok := c.(*ast.CaseClause); ok && len(cl.List) > 0 {
			return fl.reads(cl.List[0])
		}
	}
	return false
}

// clauses evaluates a switch / select body: every clause is a branch, a break
// leaves the statement, and without a default (switch only) no clause may run.
func (fl *discFlow) clauses(body *ast.BlockStmt, exhaustive bool) flowOut {
	out := flowRead
	for _, c := range body.List {
		var o flowOut
		switch cl := c.(type) {
		case *ast.CaseClause:
			exhaustive = exhaustive || cl.List == nil
			o = fl.seq(cl.Body)
		case *ast.CommClause:
			o = fl.stmt(cl.Comm).then(func() flowOut { return fl.seq(cl.Body) })
		}
		if o.brk {
			o.brk, o.through = false, true
		}
		out = out.or(o)
	}
	if !exhaustive {
		out.through = true
	}
	return out
}

func stmtsAfter(list []ast.Stmt, cur ast.Node) []ast.Stmt {
	for i, s := range list {
		if ast.Node(s) == cur {
			return list[i+1:]
		}
	}
	return nil
}

// after reports whether every unread path leaving cur reads the value, where cur
// is a statement (or a statement's body / branch) and st holds its ancestors.
func (fl *discFlow) after(st []ast.Node, cur ast.Node) bool {
	if len(st) == 0 {
		return false // fell off the end of the function
	}
	parent, rest := st[len(st)-1], st[:len(st)-1]
	next := func() bool { return fl.after(rest, parent) }
	switch p := parent.(type) {
	case *ast.BlockStmt:
		switch cur.(type) {
		case *ast.CaseClause, *ast.CommClause:
			return next() // the end of a clause leaves the switch / select
		}
		return fl.resolve(st, fl.seq(stmtsAfter(p.List, cur)), next)
	case *ast.CaseClause:
		return fl.resolve(st, fl.seq(stmtsAfter(p.Body, cur)), next)
	case *ast.CommClause:
		if cur == ast.Node(p.Comm) {
			return fl.resolve(st, fl.seq(p.Body), next)
		}
		return fl.resolve(st, fl.seq(stmtsAfter(p.Body, cur)), next)
	case *ast.LabeledStmt, *ast.SelectStmt:
		return next()
	case *ast.IfStmt:
		if cur != ast.Node(p.Init) {
			return next()
		}
		if fl.reads(p.Cond) {
			return true
		}
		return fl.resolve(st, fl.ifBranches(p), next)
	case *ast.SwitchStmt:
		if cur != ast.Node(p.Init) {
			return next()
		}
		if fl.reads(p.Tag) || fl.firstCaseReads(p.Body) {
			return true
		}
		return fl.resolve(st, fl.clauses(p.Body, false), next)
	case *ast.TypeSwitchStmt:
		if cur != ast.Node(p.Init) {
			return next()
		}
		if fl.reads(p.Assign) {
			return true
		}
		return fl.resolve(st, fl.clauses(p.Body, false), next)
	case *ast.ForStmt:
		// After the init, or after the post statement that is itself the
		// assignment being checked, the loop continues at its condition; the
		// post is not run again first.
		if cur == ast.Node(p.Init) || cur == ast.Node(p.Post) {
			return fl.loopEnter(rest, p)
		}
		return fl.loopBack(rest, p)
	case *ast.RangeStmt:
		return fl.loopBack(rest, p)
	}
	return false // the end of a function literal, or an unmodelled parent
}

// resolve follows o's unread paths out of a statement list whose ancestors are
// st: lost fails, break / continue go to their target, fall-through to through.
func (fl *discFlow) resolve(st []ast.Node, o flowOut, through func() bool) bool {
	if o.lost {
		return false
	}
	if o.brk && !fl.jump(st, false) {
		return false
	}
	if o.cont && !fl.jump(st, true) {
		return false
	}
	return !o.through || through()
}

// jump follows an unlabelled break (cont false) or continue out of st.
func (fl *discFlow) jump(st []ast.Node, cont bool) bool {
	for i := len(st) - 1; i >= 0; i-- {
		switch n := st[i].(type) {
		case *ast.ForStmt, *ast.RangeStmt:
			if cont {
				return fl.loopBack(st[:i], n)
			}
			return fl.after(st[:i], n)
		case *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
			if !cont {
				return fl.after(st[:i], n)
			}
		case *ast.FuncLit:
			return false
		}
	}
	return false
}

// loopEnter: a for statement's condition, then its body and (with a
// condition) its exit.
func (fl *discFlow) loopEnter(st []ast.Node, l *ast.ForStmt) bool {
	if fl.reads(l.Cond) {
		return true
	}
	if !fl.loopBody(st, l, l.Body) {
		return false
	}
	return l.Cond == nil || fl.after(st, l)
}

// loopBack: the end of an iteration. Every entry is in the same unread state,
// so the answer is memoised per loop, and a loop already being evaluated is
// neutral (its other exits are already being checked).
func (fl *discFlow) loopBack(st []ast.Node, loop ast.Node) bool {
	switch fl.loops[loop] {
	case 1, 2:
		return true
	case 3:
		return false
	}
	fl.loops[loop] = 1
	ok := false
	switch l := loop.(type) {
	case *ast.ForStmt:
		o := fl.stmt(l.Post)
		ok = !o.lost && (!o.through || fl.loopEnter(st, l))
	case *ast.RangeStmt:
		ok = !fl.rangeWrites(l) && fl.loopBody(st, l, l.Body) && fl.after(st, l)
	}
	fl.loops[loop] = 3
	if ok {
		fl.loops[loop] = 2
	}
	return ok
}

// loopBody: one pass of the body from the start.
func (fl *discFlow) loopBody(st []ast.Node, loop ast.Node, body *ast.BlockStmt) bool {
	o := fl.seq(body.List)
	if o.lost {
		return false
	}
	if o.brk && !fl.after(st, loop) {
		return false
	}
	return !(o.cont || o.through) || fl.loopBack(st, loop)
}

func analyseDiscards(eff *effState, api map[string]discAPI) []string {
	set := map[string]bool{}
	for _, fs := range eff.funcs {
		for _, f := range fs {
			d := &discChecker{f: f, api: api}
			d.run()
			for _, h := range d.hits {
				set[h] = true
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

// discMethodCollisions lists methods outside internal/testcase that share a name
// with a guarded Snapshot method: the name match would then be ambiguous.
func discMethodCollisions(eff *effState) []string {
	guarded := map[string]bool{}
	for _, m := range discSnapshotMethods {
		guarded[m] = true
	}
	var out []string
	for _, fs := range eff.funcs {
		for _, f := range fs {
			if f.fd.Recv != nil && guarded[f.fd.Name.Name] {
				out = append(out, f.relFile+": method "+f.fd.Name.Name)
			}
		}
	}
	sort.Strings(out)
	return out
}

func TestADR024Boundary_NoDiscardedIdentityErrors(t *testing.T) {
	root := guardRepoRoot(t)
	api := loadDiscAPI(t, root)
	st := loadEffFuncs(t, root, "internal", "cmd")
	if c := discMethodCollisions(st); len(c) > 0 {
		t.Errorf("discard guard matches Snapshot methods by name, but these types outside internal/testcase "+
			"declare the same name, so a discard on them would be misattributed (and a real one could hide):\n    %s\n"+
			"    rename the method or teach the guard receiver types", strings.Join(c, "\n    "))
	}
	checkAllowlisted(t, analyseDiscards(st, api), discardGuardAllowlist, "discardGuardAllowlist",
		"non-test code outside internal/testcase discards the error or identity outcome of an identity/ownership call",
		"ADR-024 policy (e): no read path converts an identity error into absence. Consume the error "+
			"(return it, report it, or branch on it). If the value provably cannot carry an identity error "+
			"there, add the finding to discardGuardAllowlist WITH that reason.")
}

// TestADR024Boundary_DiscardGuardMachinery proves (E) catches every discard shape
// it claims to and leaves consumed errors alone.
func TestADR024Boundary_DiscardGuardMachinery(t *testing.T) {
	root := t.TempDir()
	writeGuardModule(t, root, map[string]string{
		"internal/testcase/snap.go": `package testcase
type Snapshot struct{}
type Owner struct{}
type Resolution struct{ Outcome int }
type Candidate struct{}
type OwnershipError struct{}
func (e *OwnershipError) Error() string { return "" }
func NewSnapshot(r string) (*Snapshot, error) { return nil, nil }
func ResolveOwner(r, id string) (*Owner, error) { return nil, nil }
func OwnershipErrorFor(r Resolution) *OwnershipError { return nil }
func UnprovableInScope(s string, c Candidate) *OwnershipError { return nil }
func (s *Snapshot) Resolve(id string) Resolution { return Resolution{} }
func (s *Snapshot) ResolveScopedOwner(id string) Resolution { return Resolution{} }
func (s *Snapshot) RequireOwner(id string) (*Owner, error) { return nil, nil }
func (s *Snapshot) RequireOwnerUnderFolder(r, f, id string) (*Owner, error) { return nil, nil }
func (s *Snapshot) RequireOwnership(id string) error { return nil }
func (s *Snapshot) RequireFolderOwnership(f string, rec bool) error { return nil }
func (s *Snapshot) RequireMutationAuthorityUnderScope(f, id string) error { return nil }
func (s *Snapshot) SelectFolder(f string, rec bool) (ids []string, unprovable []Candidate) { return nil, nil }
`,
		"internal/reader/r.go": `package reader
import ` + guardQuote("internal/testcase") + `
type IdentityDiagnostics struct{}
func (IdentityDiagnostics) Affected() bool { return false }
func StatusWithDiagnostics(r string) ([]string, IdentityDiagnostics, error) { return nil, IdentityDiagnostics{}, nil }
func DetailWithSnapshot(s *testcase.Snapshot, id string) (string, error) { return "", nil }
func InPkg(r string) []string { e, _, _ := StatusWithDiagnostics(r); return e }
func statusWithDiagnostics(r string) ([]string, IdentityDiagnostics, error) { return nil, IdentityDiagnostics{}, nil }
func Local(r string) ([]string, error) { e, _, err := statusWithDiagnostics(r); return e, err }
`,
		"internal/cli/c.go": `package cli
import (
	"errors"
	"fmt"
	` + guardQuote("internal/reader") + `
	` + guardQuote("internal/testcase") + `
)
type holder struct{ err error }
func report(d reader.IdentityDiagnostics) error { return nil }
// A read that is not guaranteed to run before every exit or overwrite.
func ConditionalRead(s *testcase.Snapshot, id string, debug bool) error {
	err := s.RequireOwnership(id)
	if debug {
		fmt.Println(err)
	}
	return nil
}
func UnreachableRead(s *testcase.Snapshot, id string) error {
	err := s.RequireOwnership(id)
	return nil
	if err != nil {
		return err
	}
	return nil
}
func EarlyReturn(s *testcase.Snapshot, id string) error {
	err := s.RequireOwnership(id)
	if id == "" {
		return nil
	}
	return err
}
func PanicFirst(s *testcase.Snapshot, id string) error { err := s.RequireOwnership(id); panic(id); return err }
func BreakUnread(s *testcase.Snapshot, ids []string) error {
	var err error
	for _, id := range ids {
		err = s.RequireOwnership(id)
		if id == "stop" {
			break
		}
		if err != nil {
			return err
		}
	}
	return nil
}
func NextIterationOverwrites(s *testcase.Snapshot, ids []string) error {
	var err error
	for _, id := range ids {
		err = s.RequireOwnership(id)
	}
	return err
}
func SuccessDropsDiags(r string) ([]string, error) { e, diags, err := reader.StatusWithDiagnostics(r); return e, err }
// The sibling-error waiver applies only in the body of an if PROVING the error
// non-nil: not in the else branch, and not under err == nil.
func ElseDropsDiags(r string) ([]string, error) {
	e, diags, err := reader.StatusWithDiagnostics(r)
	if err != nil {
		return nil, err
	} else {
		return e, err
	}
}
func EqNilDropsDiags(r string) ([]string, error) {
	e, diags, err := reader.StatusWithDiagnostics(r)
	if err == nil {
		return e, err
	} else {
		return nil, err
	}
}
func UnprovenDropsDiags(r string, debug bool) ([]string, error) {
	e, diags, err := reader.StatusWithDiagnostics(r)
	if debug || err != nil {
		return nil, err
	}
	return e, errors.Join(err, report(diags))
}
// The sibling waiver resolves the error by declaration: a shadowing if-init
// err, or an err reassigned after the call, is not the call's error.
func write(e []string) error { return nil }
func ShadowInitErr(r string) ([]string, error) {
	e, diags, err := reader.StatusWithDiagnostics(r)
	if err != nil {
		return nil, err
	}
	if err := write(e); err != nil {
		return nil, err
	}
	return e, report(diags)
}
func ShadowInitNewErr(r string) ([]string, error) {
	e, diags, err := reader.StatusWithDiagnostics(r)
	if err != nil {
		return nil, err
	}
	if err := write(e); err != nil {
		return nil, errors.New("write failed")
	}
	return e, report(diags)
}
func ReassignedSibling(r string) ([]string, error) {
	e, diags, err := reader.StatusWithDiagnostics(r)
	err = write(e)
	if err != nil {
		return nil, err
	}
	return e, report(diags)
}
// diags.Affected() is not consumption: the render error path drops diags.
func render(e []string, affected bool) error { return nil }
func AffectedArg(r string) ([]string, error) {
	e, diags, err := reader.StatusWithDiagnostics(r)
	if err != nil {
		return nil, err
	}
	if werr := render(e, diags.Affected()); werr != nil {
		return nil, werr
	}
	return e, report(diags)
}
func AffectedCond(r string) ([]string, error) {
	e, diags, err := reader.StatusWithDiagnostics(r)
	if err != nil {
		return nil, err
	}
	if diags.Affected() {
		return nil, errors.New("incomplete")
	}
	return e, nil
}
// A read only in the right operand of || / && is not guaranteed.
func OrRight(s *testcase.Snapshot, id string, debug bool) error {
	err := s.RequireOwnership(id)
	if debug || err != nil {
		return err
	}
	return nil
}
func AndRight(s *testcase.Snapshot, id string, debug bool) error {
	err := s.RequireOwnership(id)
	if debug && err != nil {
		return err
	}
	return nil
}
func AssignRight(s *testcase.Snapshot, id string, debug bool) bool { err := s.RequireOwnership(id); ok := debug && err == nil; return ok }
// A guarded call in a for post clause whose value is then dropped at loop exit.
func PostDropped(s *testcase.Snapshot, ids []string) error {
	var err error
	for i := 0; i < len(ids); err = s.RequireOwnership(ids[i]) {
		if err != nil {
			return err
		}
		i++
	}
	return nil
}
// Not flagged: reads guaranteed on every path.
func WrapSelf(s *testcase.Snapshot, id string) error { err := s.RequireOwnership(id); err = fmt.Errorf("x: %w", err); return err }
func JoinSelf(s *testcase.Snapshot, id string, other error) error { err := s.RequireOwnership(id); err = errors.Join(err, other); return err }
func ErrFirst(r string) ([]string, error) {
	e, diags, err := reader.StatusWithDiagnostics(r)
	if err != nil {
		return nil, fmt.Errorf("status: %w", err)
	}
	return e, report(diags)
}
func BothBranchesRead(s *testcase.Snapshot, id string, debug bool) error {
	err := s.RequireOwnership(id)
	if debug {
		fmt.Println(err)
	} else {
		return err
	}
	return err
}
func RangeTopRead(s *testcase.Snapshot, ids []string) error {
	var err error
	for _, id := range ids {
		if err != nil {
			return err
		}
		err = s.RequireOwnership(id)
	}
	return err
}
func BreakOnErr(s *testcase.Snapshot, ids []string) error {
	var err error
	for _, id := range ids {
		err = s.RequireOwnership(id)
		if err != nil {
			break
		}
	}
	return err
}
func SwitchCaseRead(s *testcase.Snapshot, id string) int {
	err := s.RequireOwnership(id)
	switch {
	case err != nil:
		return 1
	}
	return 0
}
// Not flagged: any return inside the body of an if proving the sibling error
// non-nil, including one returning a NEW error, and nil != err.
var errNotFound = errors.New("not found")
func ErrPathNewError(r string) ([]string, error) {
	e, diags, err := reader.StatusWithDiagnostics(r)
	if err != nil {
		if errors.Is(err, errNotFound) {
			return nil, errors.New("target not found")
		}
		return nil, err
	}
	return e, report(diags)
}
func NilFirst(r string) ([]string, error) {
	e, diags, err := reader.StatusWithDiagnostics(r)
	if nil != err {
		return nil, err
	}
	return e, report(diags)
}
func ProvenConjunct(r string, debug bool) ([]string, error) {
	e, diags, err := reader.StatusWithDiagnostics(r)
	if debug && err != nil {
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	return e, report(diags)
}
// Not flagged: a write to the sibling in a branch that excludes the call is
// not an intervening reassignment.
func ExclusiveBranchWrite(r string, k bool) ([]string, error) {
	var e []string
	var diags reader.IdentityDiagnostics
	var err error
	if k {
		e, diags, err = reader.StatusWithDiagnostics(r)
	} else {
		e, diags, err = reader.StatusWithDiagnostics(r + "/x")
	}
	if err != nil {
		return nil, err
	}
	return e, report(diags)
}
// Not flagged: Affected() plus diags passed on to the finishing helper on
// every path.
func AffectedThenFinish(r string) ([]string, error) {
	e, diags, err := reader.StatusWithDiagnostics(r)
	if err != nil {
		return nil, err
	}
	if werr := render(e, diags.Affected()); werr != nil {
		return nil, errors.Join(werr, report(diags))
	}
	return e, report(diags)
}
// Not flagged: a read in the LEFT operand of && / || is guaranteed.
func AndLeft(s *testcase.Snapshot, id string, debug bool) error {
	err := s.RequireOwnership(id)
	if err != nil && debug {
		return err
	}
	return nil
}
// Not flagged: a guarded call in the post clause, read in the body and after
// the loop (the post is not re-run before the condition).
func PostRead(s *testcase.Snapshot, ids []string) error {
	var err error
	for i := 0; i < len(ids); err = s.RequireOwnership(ids[i]) {
		if err != nil {
			return err
		}
		i++
	}
	return err
}
func SnapBlank(r string) *testcase.Snapshot { s, _ := testcase.NewSnapshot(r); return s }
func OwnerBlank(s *testcase.Snapshot, id string) *testcase.Owner { o, _ := s.RequireOwner(id); return o }
func Stmt(s *testcase.Snapshot, id string) { s.RequireOwnership(id) }
func Deferred(s *testcase.Snapshot, id string) { defer s.RequireOwnership(id) }
func BlankOne(s *testcase.Snapshot, f string) { _ = s.RequireFolderOwnership(f, true) }
func Unused(s *testcase.Snapshot, id string) error {
	err := s.RequireOwnership("x")
	if err != nil {
		return err
	}
	o, err := s.RequireOwner(id)
	err = errors.New("overwritten")
	_ = o
	return err
}
func NeverRead(s *testcase.Snapshot, id string) *testcase.Owner {
	var err error
	var o *testcase.Owner
	if id != "" {
		o, err = s.RequireOwner(id)
	}
	return o
}
func Unprov(s *testcase.Snapshot, f string) []string { ids, _ := s.SelectFolder(f, true); return ids }
func ResolveStmt(s *testcase.Snapshot, id string) { s.Resolve(id) }
func DiagBlank(r string) []string { e, _, err := reader.StatusWithDiagnostics(r); if err != nil { return nil }; return e }
func ErrBlank(s *testcase.Snapshot, id string) string { d, _ := reader.DetailWithSnapshot(s, id); return d }
func OwnErrBlank(r testcase.Resolution) { _ = testcase.OwnershipErrorFor(r) }
func ResolveOwnerBlank(r, id string) *testcase.Owner { o, _ := testcase.ResolveOwner(r, id); return o }
// Not flagged: every carrier consumed.
func Returned(s *testcase.Snapshot, id string) error { return s.RequireOwnership(id) }
func Checked(s *testcase.Snapshot, id string) (*testcase.Owner, error) {
	o, err := s.RequireOwner(id)
	if err != nil {
		return nil, err
	}
	return o, nil
}
func IfInit(s *testcase.Snapshot, id string) error { if err := s.RequireOwnership(id); err != nil { return err }; return nil }
func OwnerDropped(s *testcase.Snapshot, id string) error { _, err := s.RequireOwner(id); return err }
func Stored(s *testcase.Snapshot, id string, h *holder) { h.err = s.RequireOwnership(id) }
func Named(s *testcase.Snapshot, id string) (err error) { _, err = s.RequireOwner(id); return }
func Wrapped(s *testcase.Snapshot, id string) error { return errors.Join(s.RequireOwnership(id)) }
func InLoop(s *testcase.Snapshot, ids []string) error {
	var err error
	for i := 0; err == nil && i < len(ids); i++ {
		err = s.RequireOwnership(ids[i])
	}
	return nil
}
func AllUsed(r string) ([]string, reader.IdentityDiagnostics, error) { return reader.StatusWithDiagnostics(r) }
func Both(s *testcase.Snapshot, f string) ([]string, int) { ids, u := s.SelectFolder(f, true); return ids, len(u) }
func ResolveUsed(s *testcase.Snapshot, id string) int { r := s.Resolve(id); return r.Outcome }
func Unguarded(s *testcase.Snapshot) { s.String() }
func SwitchArms(s *testcase.Snapshot, id string, k int) error {
	var err error
	switch k {
	case 1:
		_, err = s.RequireOwner(id)
	default:
		err = s.RequireOwnership(id)
	}
	return err
}
func IfArms(s *testcase.Snapshot, id string, k bool) error {
	var err error
	if k {
		_, err = s.RequireOwner(id)
	} else {
		err = nil
	}
	return err
}
func DeadArm(s *testcase.Snapshot, id string, k bool) error {
	var err error
	if k {
		_, err = s.RequireOwner(id)
	} else {
		err = nil
	}
	return nil
}
`,
	})
	api := loadDiscAPI(t, root)
	st := loadEffFuncs(t, root, "internal")
	got := map[string]bool{}
	for _, f := range analyseDiscards(st, api) {
		got[f] = true
	}
	for _, want := range []string{
		"cli.SnapBlank: testcase.NewSnapshot result 2 discarded via _",
		"cli.OwnerBlank: Snapshot.RequireOwner result 2 discarded via _",
		"cli.Stmt: Snapshot.RequireOwnership result 1 discarded (call statement)",
		"cli.Deferred: Snapshot.RequireOwnership result 1 discarded (defer statement)",
		"cli.BlankOne: Snapshot.RequireFolderOwnership result 1 discarded via _",
		"cli.Unused: Snapshot.RequireOwner result 2 discarded (value err not read on every path)",
		"cli.NeverRead: Snapshot.RequireOwner result 2 discarded (value err not read on every path)",
		"cli.Unprov: Snapshot.SelectFolder result 2 discarded via _",
		"cli.ResolveStmt: Snapshot.Resolve result 1 discarded (call statement)",
		"cli.DiagBlank: reader.StatusWithDiagnostics result 2 discarded via _",
		"cli.ErrBlank: reader.DetailWithSnapshot result 2 discarded via _",
		"cli.OwnErrBlank: testcase.OwnershipErrorFor result 1 discarded via _",
		"cli.ResolveOwnerBlank: testcase.ResolveOwner result 2 discarded via _",
		"reader.InPkg: reader.StatusWithDiagnostics result 2 discarded via _",
		"reader.InPkg: reader.StatusWithDiagnostics result 3 discarded via _",
		// The write in the else arm is excluded, and nothing reads err afterwards.
		"cli.DeadArm: Snapshot.RequireOwner result 2 discarded (value err not read on every path)",
		// An unexported reader helper is guarded like an exported one.
		"reader.Local: reader.statusWithDiagnostics result 2 discarded via _",
		// Reads that are conditional, unreachable, after an exit, or skipped by a
		// break / the next iteration's overwrite.
		"cli.ConditionalRead: Snapshot.RequireOwnership result 1 discarded (value err not read on every path)",
		"cli.UnreachableRead: Snapshot.RequireOwnership result 1 discarded (value err not read on every path)",
		"cli.EarlyReturn: Snapshot.RequireOwnership result 1 discarded (value err not read on every path)",
		"cli.PanicFirst: Snapshot.RequireOwnership result 1 discarded (value err not read on every path)",
		"cli.BreakUnread: Snapshot.RequireOwnership result 1 discarded (value err not read on every path)",
		"cli.NextIterationOverwrites: Snapshot.RequireOwnership result 1 discarded (value err not read on every path)",
		// Returning the sibling error unconditionally drops the diagnostics on success.
		"cli.SuccessDropsDiags: reader.StatusWithDiagnostics result 2 discarded (value diags not read on every path)",
		// No sibling waiver in the else branch, under err == nil, or under a
		// condition that does not prove the error non-nil.
		"cli.ElseDropsDiags: reader.StatusWithDiagnostics result 2 discarded (value diags not read on every path)",
		"cli.EqNilDropsDiags: reader.StatusWithDiagnostics result 2 discarded (value diags not read on every path)",
		"cli.UnprovenDropsDiags: reader.StatusWithDiagnostics result 2 discarded (value diags not read on every path)",
		// The waiver needs the same declaration, not reassigned since the call.
		"cli.ShadowInitErr: reader.StatusWithDiagnostics result 2 discarded (value diags not read on every path)",
		"cli.ShadowInitNewErr: reader.StatusWithDiagnostics result 2 discarded (value diags not read on every path)",
		"cli.ReassignedSibling: reader.StatusWithDiagnostics result 2 discarded (value diags not read on every path)",
		// Affected() alone is not consumption.
		"cli.AffectedArg: reader.StatusWithDiagnostics result 2 discarded (value diags not read on every path)",
		"cli.AffectedCond: reader.StatusWithDiagnostics result 2 discarded (value diags not read on every path)",
		// A read only in the right operand of || / && is not guaranteed.
		"cli.OrRight: Snapshot.RequireOwnership result 1 discarded (value err not read on every path)",
		"cli.AndRight: Snapshot.RequireOwnership result 1 discarded (value err not read on every path)",
		"cli.AssignRight: Snapshot.RequireOwnership result 1 discarded (value err not read on every path)",
		// The post-clause value is dropped when the loop exits.
		"cli.PostDropped: Snapshot.RequireOwnership result 1 discarded (value err not read on every path)",
	} {
		if !got[want] {
			t.Errorf("discard guard missed %q; findings were %v", want, keysOf(got))
		}
	}
	for f := range got {
		for _, clean := range []string{"cli.Returned", "cli.Checked", "cli.IfInit", "cli.OwnerDropped",
			"cli.Stored", "cli.Named", "cli.Wrapped", "cli.InLoop", "cli.AllUsed", "cli.Both",
			"cli.ResolveUsed", "cli.Unguarded", "cli.SwitchArms", "cli.IfArms", "cli.DiagBlank: reader.StatusWithDiagnostics result 3",
			"cli.Unused: Snapshot.RequireOwnership", "cli.SnapBlank: testcase.NewSnapshot result 1",
			"cli.OwnerBlank: Snapshot.RequireOwner result 1", "cli.WrapSelf", "cli.JoinSelf", "cli.ErrFirst",
			"cli.BothBranchesRead", "cli.RangeTopRead", "cli.BreakOnErr", "cli.SwitchCaseRead",
			"cli.SuccessDropsDiags: reader.StatusWithDiagnostics result 3", "reader.Local: reader.statusWithDiagnostics result 3",
			"cli.ElseDropsDiags: reader.StatusWithDiagnostics result 3", "cli.EqNilDropsDiags: reader.StatusWithDiagnostics result 3",
			"cli.UnprovenDropsDiags: reader.StatusWithDiagnostics result 3", "cli.ErrPathNewError", "cli.NilFirst",
			"cli.ProvenConjunct", "cli.AndLeft", "cli.PostRead", "cli.ExclusiveBranchWrite", "cli.AffectedThenFinish",
			"cli.AffectedArg: reader.StatusWithDiagnostics result 3", "cli.AffectedCond: reader.StatusWithDiagnostics result 3",
			"cli.ShadowInitErr: reader.StatusWithDiagnostics result 3", "cli.ShadowInitNewErr: reader.StatusWithDiagnostics result 3"} {
			if strings.HasPrefix(f, clean) {
				t.Errorf("discard guard flagged a consumed carrier: %q", f)
			}
		}
	}

	// A method outside internal/testcase that reuses a guarded name is reported.
	writeGuardModule(t, root, map[string]string{"internal/other/o.go": `package other
type T struct{}
func (T) RequireOwner(id string) error { return nil }
`})
	if c := discMethodCollisions(loadEffFuncs(t, root, "internal")); len(c) != 1 || !strings.Contains(c[0], "method RequireOwner") {
		t.Errorf("method-name collision not reported: %v", c)
	}
}

// ============================================================================
// shared helpers
// ============================================================================

func guardQuote(rel string) string { return `"` + guardModulePrefix + rel + `"` }

func writeGuardModule(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, src := range files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(src), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

// checkAllowlisted fails on every finding not in the allowlist, and on every
// allowlist entry that no longer fires.
func checkAllowlisted(t *testing.T, findings []string, allow map[string]string, listName, what, advice string) {
	t.Helper()
	fired := map[string]bool{}
	var bad []string
	for _, f := range findings {
		fired[f] = true
		if _, ok := allow[f]; !ok {
			bad = append(bad, f)
		}
	}
	if len(bad) > 0 {
		t.Errorf("%s:\n    %s\n    %s", what, strings.Join(bad, "\n    "), advice)
	}
	for k := range allow {
		if !fired[k] {
			t.Errorf("%s entry %q no longer fires; remove it so the list stays exact", listName, k)
		}
	}
}
