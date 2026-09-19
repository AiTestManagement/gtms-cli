package triage

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSourceShape_ReadUsesCheckedEntryNotReadFile pins BUG-187 Fix Direction
// item 3 structurally: the decoded bytes must come from the same entry whose
// type was checked. An implementation that ran Lstat and then a second,
// unrelated os.ReadFile(path) would pass every behavioural test on a static
// filesystem yet reintroduce the check-to-use gap.
//
// This is an AST check, not a substring search, and the difference is the whole
// point. The first version of this guard asserted that record.go CONTAINS the
// text "readRegularEntry(" -- which the function's own declaration satisfies.
// It would have stayed green with the call site deleted, so it proved nothing
// about Read. Walking the call graph from Read's declaration is what actually
// pins the invariant (REV-162 follow-up).
func TestSourceShape_ReadUsesCheckedEntryNotReadFile(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "record.go", nil, parser.ParseComments)
	require.NoError(t, err, "record.go must parse")

	readDecl := findFunc(file, "Read")
	require.NotNil(t, readDecl, "record.go must declare Read")

	// Read must CALL readRegularEntry, not merely mention it.
	assert.True(t, callsFunc(readDecl, "readRegularEntry"),
		"Read must obtain its bytes through readRegularEntry, the entry-type gate; "+
			"a check followed by an unrelated path-based read reopens the check-to-use gap")

	// Read must carry the store authority so it can prove containment.
	require.NotNil(t, readDecl.Type.Params, "Read must take parameters")
	assert.Equal(t, []string{"projectRoot", "path"}, paramNames(readDecl),
		"Read must take (projectRoot, path): a bare Read(path) carries no authority "+
			"to prove the store is inside the project")

	// No os.ReadFile anywhere in the file: the bytes must come from the checked
	// descriptor. Asserted as a real selector expression, so the string appearing
	// in a comment cannot satisfy or break it.
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "os" && sel.Sel.Name == "ReadFile" {
				t.Errorf("record.go calls os.ReadFile at %s: triage bytes must come from the "+
					"same regular entry whose type Lstat checked (BUG-187 Fix Direction item 3)",
					fset.Position(call.Pos()))
			}
		}
		return true
	})
}

// TestSourceShape_ValidateDecidesOnRawPath pins the REV-162 blocker: the
// identity comparison must run on the RAW basename, and only diagnostics may
// use the sanitized form. Passing the sanitized path into the comparison makes
// it lossy, so a real filename carrying a control character where the body
// carries '?' compares equal and is accepted.
//
// Behavioural coverage lives in TestRead_RefusesControlCharFilenameIdentity;
// this guard additionally stops the two arguments being silently swapped back,
// which no black-box test could distinguish on a clean path.
func TestSourceShape_ValidateDecidesOnRawPath(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "record.go", nil, parser.ParseComments)
	require.NoError(t, err)

	validate := findFunc(file, "validate")
	require.NotNil(t, validate, "record.go must declare validate")
	assert.Equal(t, []string{"path", "displayPath"}, paramNames(validate),
		"validate must receive the raw path AND a display path, in that order: "+
			"it decides on the raw one and renders the other")

	// The identity comparison must read filepath.Base(path) -- never displayPath.
	var sawRawBase bool
	ast.Inspect(validate, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Base" {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); !ok || id.Name != "filepath" {
			return true
		}
		require.Len(t, call.Args, 1)
		arg, ok := call.Args[0].(*ast.Ident)
		require.True(t, ok, "filepath.Base must take a plain identifier")
		if arg.Name == "displayPath" {
			t.Errorf("validate compares filepath.Base(displayPath) at %s: the identity check "+
				"must use the RAW path, or a control-character filename compares equal to a "+
				"'?' in the body and is wrongly accepted", fset.Position(call.Pos()))
		}
		if arg.Name == "path" {
			sawRawBase = true
		}
		return true
	})
	assert.True(t, sawRawBase, "validate must derive the compared basename from the raw path")
}

// TestSourceShape_EnumeratorsDoNotSkipDirectories pins the second REV-162
// blocker. Selection in ListAll and ListForTC is by NAME; an entry shaped like a
// record must reach Read so Read can refuse it by type. An IsDir() skip made a
// forged directory VANISH from the listing -- exit 0, empty result, no
// diagnostic -- which is the one outcome a fail-closed boundary must not have.
func TestSourceShape_EnumeratorsDoNotSkipDirectories(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "record.go", nil, parser.ParseComments)
	require.NoError(t, err)

	for _, name := range []string{"ListAll", "ListForTC"} {
		fn := findFunc(file, name)
		require.NotNil(t, fn, "record.go must declare %s", name)
		ast.Inspect(fn, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "IsDir" {
				t.Errorf("%s calls IsDir at %s: a record-shaped directory must be enumerated "+
					"as a candidate and refused by Read, not skipped into silence",
					name, fset.Position(call.Pos()))
			}
			return true
		})
	}
}

// TestSourceShape_LstatSitesDistinguishAbsentFromUninspectable pins the third
// REV-162 blocker at EVERY site that probes for an existing record, not just
// one: only fs.ErrNotExist may be read as "nothing is there". Any other Lstat
// error means GTMS could not look, and a guard that cannot see its target must
// not wave a writer through or report an unreadable record as missing.
//
// This is an AST check for a real fs.ErrNotExist reference in the enclosing
// function. The first version asserted that Write's printed source CONTAINS the
// text "fs.ErrNotExist" -- which its own explanatory COMMENT satisfies, so
// deleting the discrimination while keeping the comment stayed green. That
// mattered more than usual here: the behavioural test skips on Windows, leaving
// this guard as the only Windows coverage of the invariant.
//
// It is deliberately three-site. The reader-side probes had the identical
// collapse and no structural guard at all.
func TestSourceShape_LstatSitesDistinguishAbsentFromUninspectable(t *testing.T) {
	for _, tc := range []struct{ file, fn string }{
		{"record.go", "Write"},
		{"../reader/triage.go", "ClassifyTriage"},
		{"../reader/triage.go", "selectTriageRecordPath"},
	} {
		fset := token.NewFileSet()
		src, err := parser.ParseFile(fset, tc.file, nil, parser.ParseComments)
		require.NoError(t, err, "%s must parse", tc.file)
		fn := findFunc(src, tc.fn)
		require.NotNil(t, fn, "%s must declare %s", tc.file, tc.fn)

		require.True(t, callsLstat(fn),
			"%s.%s must probe with os.Lstat, never os.Stat: os.Stat follows a link and "+
				"reports a DANGLING one as absent, which skips the guard entirely",
			tc.file, tc.fn)

		assert.True(t, referencesErrNotExist(fn),
			"%s.%s must discriminate fs.ErrNotExist from other Lstat errors as a real "+
				"expression, not merely mention it in a comment; collapsing the three-way "+
				"state converts an inspection failure into permission to proceed",
			tc.file, tc.fn)
	}
}

// --- helpers ---

func findFunc(file *ast.File, name string) *ast.FuncDecl {
	for _, d := range file.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return fn
		}
	}
	return nil
}

func paramNames(fn *ast.FuncDecl) []string {
	var out []string
	for _, f := range fn.Type.Params.List {
		for _, n := range f.Names {
			out = append(out, n.Name)
		}
	}
	return out
}

// callsLstat reports whether fn calls os.Lstat (and not os.Stat) as a real call
// expression.
func callsLstat(fn *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "os" && sel.Sel.Name == "Lstat" {
				found = true
			}
		}
		return true
	})
	return found
}

// referencesErrNotExist reports whether fn contains a genuine fs.ErrNotExist
// selector expression. A comment mentioning the name does not satisfy it -- that
// is precisely the vacuity this replaced.
func referencesErrNotExist(fn *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fn, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok && id.Name == "fs" && sel.Sel.Name == "ErrNotExist" {
			found = true
		}
		return true
	})
	return found
}

func callsFunc(fn *ast.FuncDecl, name string) bool {
	found := false
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, ok := call.Fun.(*ast.Ident); ok && id.Name == name {
			found = true
		}
		return true
	})
	return found
}
