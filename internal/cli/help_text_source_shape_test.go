package cli

// BUG-115: Source-shape guard for cobra help text literals.
//
// Two guards:
//   1. ASCII-only: every Use, Short, Long string on a cobra.Command composite
//      literal, and every flag-description string passed to Flags()/PersistentFlags()
//      registration calls, must contain only bytes <= 0x7F.
//   2. Use-vs-Args bracket consistency: commands with ExactArgs or MinimumNArgs
//      must wrap required positionals in <...>, not [...].
//
// This is a pure unit test (no os/exec, no git) -- runs in smoke tier.
// The guard is scoped to cobra literals extracted via go/ast. It deliberately
// does NOT blanket-scan internal/cli/*.go because the package legitimately
// uses non-ASCII bytes in runtime output for the CLAUDE.md-sanctioned status
// icons (check-mark, black-circle, white-circle, ballot-X, warning-sign) and
// test-file fixtures. Em-dashes in runtime error/status strings are NOT
// legitimate under CLAUDE.md and are swept in lockstep with help-text edits;
// the guard just does not enforce them at compile time because scoping the
// AST walk to string literals in every fmt.Errorf / fmt.Fprintf / return
// site is out of scope for BUG-115.
//
// Flag-method coverage: the AST walk recognises the pflag flag-registration
// methods enumerated in scanFlagDescriptionASCII. Keep the enumeration in
// sync with the pflag public API (github.com/spf13/pflag) as new Var forms
// are added. Adding a method that the guard does not know about will
// silently fall through the check.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHelpTextASCII_CobraCommandLiterals AST-scans every cobra.Command composite
// literal in internal/cli/*.go (excluding _test.go) and asserts that Use, Short,
// and Long string values contain only ASCII bytes (<= 0x7F).
func TestHelpTextASCII_CobraCommandLiterals(t *testing.T) {
	files := productionGoFiles(t)
	require.NotEmpty(t, files, "expected at least one production .go file")

	var offenders []string
	for _, fpath := range files {
		offenders = append(offenders, scanCobraCommandASCII(t, fpath)...)
	}

	if len(offenders) > 0 {
		t.Errorf("BUG-115 AC4(a): cobra.Command Use/Short/Long fields must contain "+
			"only ASCII bytes (<= 0x7F). Found %d violation(s):\n", len(offenders))
		for _, o := range offenders {
			t.Logf("  %s", o)
		}
	}
}

// TestHelpTextASCII_FlagDescriptions AST-scans flag registration calls
// (Flags().StringVar, BoolVar, etc. and PersistentFlags() equivalents) and
// asserts the usage-string argument contains only ASCII bytes.
func TestHelpTextASCII_FlagDescriptions(t *testing.T) {
	files := productionGoFiles(t)
	require.NotEmpty(t, files, "expected at least one production .go file")

	var offenders []string
	for _, fpath := range files {
		offenders = append(offenders, scanFlagDescriptionASCII(t, fpath)...)
	}

	if len(offenders) > 0 {
		t.Errorf("BUG-115 AC4(b): Cobra flag description strings must contain "+
			"only ASCII bytes (<= 0x7F). Found %d violation(s):\n", len(offenders))
		for _, o := range offenders {
			t.Logf("  %s", o)
		}
	}
}

// TestHelpTextBrackets_UseVsArgs cross-checks that cobra.Command Use fields
// use <...> for required positionals (ExactArgs, MinimumNArgs) and [...] for
// optional positionals (RangeArgs, MaximumNArgs).
func TestHelpTextBrackets_UseVsArgs(t *testing.T) {
	files := productionGoFiles(t)
	require.NotEmpty(t, files, "expected at least one production .go file")

	var violations []string
	for _, fpath := range files {
		violations = append(violations, scanUseVsArgs(t, fpath)...)
	}

	if len(violations) > 0 {
		t.Errorf("BUG-115 AC5: Use-vs-Args bracket mismatch. Required positionals "+
			"must use <...>, optional positionals must use [...]. Found %d violation(s):\n",
			len(violations))
		for _, v := range violations {
			t.Logf("  %s", v)
		}
	}
}

// --- helpers ---

// productionGoFiles returns all non-test .go files in the current package directory.
func productionGoFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
			files = append(files, e.Name())
		}
	}
	return files
}

// scanCobraCommandASCII extracts Use, Short, Long values from cobra.Command
// composite literals and returns offender descriptions for any non-ASCII byte.
func scanCobraCommandASCII(t *testing.T, filename string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, nil, parser.ParseComments)
	if err != nil {
		t.Logf("parse error on %s: %v (skipping)", filename, err)
		return nil
	}

	var offenders []string
	ast.Inspect(f, func(n ast.Node) bool {
		cl, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		// Check if this is a cobra.Command composite literal
		if !isCobraCommandType(cl.Type) {
			return true
		}
		for _, elt := range cl.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			ident, ok := kv.Key.(*ast.Ident)
			if !ok {
				continue
			}
			switch ident.Name {
			case "Use", "Short", "Long":
				val := extractStringValue(kv.Value)
				if val == "" {
					continue
				}
				for i, b := range []byte(val) {
					if b > 0x7F {
						pos := fset.Position(kv.Pos())
						offenders = append(offenders,
							fmt.Sprintf("%s:%d field=%s byte_offset=%d value=0x%02x",
								filename, pos.Line, ident.Name, i, b))
					}
				}
			}
		}
		return true
	})
	return offenders
}

// scanFlagDescriptionASCII finds flag registration calls and checks the usage
// string argument for non-ASCII bytes.
func scanFlagDescriptionASCII(t *testing.T, filename string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, nil, parser.ParseComments)
	if err != nil {
		return nil
	}

	// Flag registration methods and the position of the usage string argument.
	// Three shapes exist in pflag:
	//   {Type}Var(&v, name, default, usage)               -> usage index 3
	//   {Type}VarP(&v, name, shorthand, default, usage)   -> usage index 4
	//   Var(value, name, usage)                           -> usage index 2
	//   VarP(value, name, shorthand, usage)               -> usage index 3
	//   VarPF(value, name, shorthand, usage)              -> usage index 3
	// The `Var{,P,PF}` forms differ because pflag.Value carries its own default
	// and there is no separate default argument.
	//
	// Enumeration below is the exhaustive set of pflag flag-registration
	// methods used or plausibly used by an internal/cli/*.go author. Keep in
	// sync with the pflag public API when new Var forms are added.
	type methodInfo struct {
		name     string
		usageIdx int
	}
	nonP := []string{
		"StringVar", "BoolVar", "IntVar", "Int8Var", "Int16Var", "Int32Var", "Int64Var",
		"UintVar", "Uint8Var", "Uint16Var", "Uint32Var", "Uint64Var",
		"Float32Var", "Float64Var", "DurationVar", "CountVar",
		"StringSliceVar", "StringArrayVar",
		"IntSliceVar", "Int32SliceVar", "Int64SliceVar",
		"BoolSliceVar", "Float32SliceVar", "Float64SliceVar",
		"DurationSliceVar", "StringToStringVar", "StringToIntVar", "StringToInt64Var",
	}
	pVariants := []string{
		"StringVarP", "BoolVarP", "IntVarP", "Int8VarP", "Int16VarP", "Int32VarP", "Int64VarP",
		"UintVarP", "Uint8VarP", "Uint16VarP", "Uint32VarP", "Uint64VarP",
		"Float32VarP", "Float64VarP", "DurationVarP", "CountVarP",
		"StringSliceVarP", "StringArrayVarP",
		"IntSliceVarP", "Int32SliceVarP", "Int64SliceVarP",
		"BoolSliceVarP", "Float32SliceVarP", "Float64SliceVarP",
		"DurationSliceVarP", "StringToStringVarP", "StringToIntVarP", "StringToInt64VarP",
	}

	methods := make(map[string]int)
	for _, m := range nonP {
		methods[m] = 3
	}
	for _, m := range pVariants {
		methods[m] = 4
	}
	// `Var{,P,PF}` shape: value carries default, no separate default arg.
	methods["Var"] = 2
	methods["VarP"] = 3
	methods["VarPF"] = 3

	var offenders []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		usageIdx, known := methods[sel.Sel.Name]
		if !known {
			return true
		}
		// Verify the receiver chain contains Flags() or PersistentFlags()
		if !isFlagsChain(sel.X) {
			return true
		}
		if usageIdx >= len(call.Args) {
			return true
		}
		val := extractStringValue(call.Args[usageIdx])
		if val == "" {
			return true
		}
		for i, b := range []byte(val) {
			if b > 0x7F {
				pos := fset.Position(call.Pos())
				offenders = append(offenders,
					fmt.Sprintf("%s:%d method=%s byte_offset=%d value=0x%02x",
						filename, pos.Line, sel.Sel.Name, i, b))
			}
		}
		return true
	})
	return offenders
}

// scanUseVsArgs checks that cobra.Command Use fields match their Args validators.
// ExactArgs/MinimumNArgs -> first positional must use <...>
// RangeArgs(0,N)/MaximumNArgs -> positionals should use [...]
func scanUseVsArgs(t *testing.T, filename string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, nil, parser.ParseComments)
	if err != nil {
		return nil
	}

	var violations []string
	ast.Inspect(f, func(n ast.Node) bool {
		cl, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		if !isCobraCommandType(cl.Type) {
			return true
		}

		var useVal string
		var argsExpr ast.Expr
		for _, elt := range cl.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			ident, ok := kv.Key.(*ast.Ident)
			if !ok {
				continue
			}
			if ident.Name == "Use" {
				useVal = extractStringValue(kv.Value)
			}
			if ident.Name == "Args" {
				argsExpr = kv.Value
			}
		}

		if useVal == "" || argsExpr == nil {
			return true
		}

		argsType := classifyArgs(argsExpr)
		if argsType == "" {
			return true
		}

		pos := fset.Position(cl.Pos())

		// Extract the first positional from Use (after the command name).
		// Use format is "command <pos> [flags]" or "command [pos] [flags]".
		firstPositional := extractFirstPositional(useVal)
		if firstPositional == "" {
			return true
		}

		switch argsType {
		case "required":
			// ExactArgs or MinimumNArgs: first positional must use <...>
			if strings.HasPrefix(firstPositional, "[") && !strings.HasPrefix(firstPositional, "[flags]") {
				violations = append(violations,
					fmt.Sprintf("%s:%d Use=%q has optional bracket [%s] but Args requires the positional (ExactArgs/MinimumNArgs)",
						filename, pos.Line, useVal, firstPositional))
			}
		case "optional":
			// RangeArgs(0,N) or MaximumNArgs: first positional should use [...]
			if strings.HasPrefix(firstPositional, "<") {
				violations = append(violations,
					fmt.Sprintf("%s:%d Use=%q has required bracket <%s> but Args allows zero positionals (RangeArgs/MaximumNArgs)",
						filename, pos.Line, useVal, firstPositional))
			}
		}
		return true
	})
	return violations
}

// isCobraCommandType checks if a type expression is cobra.Command.
func isCobraCommandType(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return ident.Name == "cobra" && sel.Sel.Name == "Command"
}

// extractStringValue extracts the string value from a basic literal or
// raw string literal expression.
func extractStringValue(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind == token.STRING {
			val, err := strconv.Unquote(e.Value)
			if err != nil {
				return ""
			}
			return val
		}
	}
	return ""
}

// isFlagsChain checks if the expression is a chain containing Flags() or
// PersistentFlags() on a cmd-like receiver.
func isFlagsChain(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	return sel.Sel.Name == "Flags" || sel.Sel.Name == "PersistentFlags"
}

// classifyArgs determines whether an Args expression requires positionals
// ("required"), allows zero positionals ("optional"), or is unknown ("").
func classifyArgs(expr ast.Expr) string {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return ""
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	switch sel.Sel.Name {
	case "ExactArgs", "MinimumNArgs":
		// Both require at least one positional
		if len(call.Args) == 1 {
			lit, ok := call.Args[0].(*ast.BasicLit)
			if ok && lit.Kind == token.INT {
				n, _ := strconv.Atoi(lit.Value)
				if n > 0 {
					return "required"
				}
			}
		}
	case "RangeArgs":
		// RangeArgs(min, max) -- if min is 0, positionals are optional
		if len(call.Args) == 2 {
			lit, ok := call.Args[0].(*ast.BasicLit)
			if ok && lit.Kind == token.INT {
				n, _ := strconv.Atoi(lit.Value)
				if n == 0 {
					return "optional"
				}
				return "required"
			}
		}
	case "MaximumNArgs":
		return "optional"
	case "NoArgs":
		return "" // no positionals at all
	}
	return ""
}

// extractFirstPositional returns the first positional token from a Use string.
// The Use string format is "commandname <pos1> [pos2] [flags]" or similar.
// Returns the first token that starts with '<' or '[' (excluding "[flags]").
func extractFirstPositional(use string) string {
	// Split on whitespace and find the first <...> or [...] token
	parts := strings.Fields(use)
	for _, p := range parts[1:] { // skip the command name
		if p == "[flags]" {
			continue
		}
		if strings.HasPrefix(p, "<") || strings.HasPrefix(p, "[") {
			return p
		}
	}
	return ""
}

// --- Regression guards for the specific BUG-115 fixes ---

// TestHelpText_PrimeUseRequiredBrackets verifies the prime Use field uses
// angle brackets for its required positional.
func TestHelpText_PrimeUseRequiredBrackets(t *testing.T) {
	cmd := newPrimeCmd()
	useLine := cmd.UseLine()
	assert.Contains(t, useLine, "<test-case-id>",
		"prime Use should use <test-case-id> (required positional)")
	assert.NotContains(t, useLine, "[test-case-id]",
		"prime Use must not use [test-case-id] (looks optional but ExactArgs(1))")
}

// TestHelpText_DeleteUseRequiredBrackets verifies the delete Use field uses
// angle brackets for its required positional.
func TestHelpText_DeleteUseRequiredBrackets(t *testing.T) {
	cmd := newDeleteCmd()
	useLine := cmd.UseLine()
	assert.Contains(t, useLine, "<test-case-id | folder>",
		"delete Use should use <test-case-id | folder> (required positional)")
	assert.NotContains(t, useLine, "[test-case-id | folder]",
		"delete Use must not use [test-case-id | folder] (looks optional but ExactArgs(1))")
}

// TestHelpText_AutomateShortNoExecutableScripts verifies the automate Short
// no longer contains "executable scripts".
func TestHelpText_AutomateShortNoExecutableScripts(t *testing.T) {
	cmd := newAutomateCmd()
	assert.NotContains(t, cmd.Short, "executable scripts",
		"automate Short must not contain 'executable scripts' (outdated vocabulary)")
}

// TestHelpText_DeleteShortUsesArtefact verifies the delete Short uses the
// project vocabulary "artefacts" not "artifacts".
func TestHelpText_DeleteShortUsesArtefact(t *testing.T) {
	cmd := newDeleteCmd()
	assert.Contains(t, cmd.Short, "artefacts",
		"delete Short should use 'artefacts' (project vocabulary)")
	assert.NotContains(t, cmd.Short, "artifacts",
		"delete Short must not use 'artifacts' (US spelling)")
}

// TestHelpText_SectionSignAbsent verifies no cobra help literal contains the
// section sign character. This is a regression guard for the list.go fix.
func TestHelpText_SectionSignAbsent(t *testing.T) {
	files := productionGoFiles(t)
	for _, fpath := range files {
		offenders := scanCobraCommandASCII(t, fpath)
		for _, o := range offenders {
			// The section sign is 0xC2 0xA7 in UTF-8; if any non-ASCII byte
			// is found in cobra literals, the ASCII guard above catches it.
			// This test is redundant with the ASCII guard but makes the
			// section-sign regression explicit.
			if strings.Contains(o, "0xa7") || strings.Contains(o, "0xc2") {
				t.Errorf("section sign (U+00A7) found in cobra literal: %s", o)
			}
		}
	}
}

// TestHelpText_ListAdaptersLongSectionWord verifies that list.go Long strings
// use the word "section" (or equivalent) rather than the section sign glyph.
func TestHelpText_ListAdaptersLongSectionWord(t *testing.T) {
	cmd := newListCmd()
	for _, sub := range cmd.Commands() {
		long := sub.Long
		assert.NotContains(t, long, "§",
			"%s Long must not contain the section sign U+00A7", sub.Name())
	}
	assert.NotContains(t, cmd.Long, "§",
		"list Long must not contain the section sign U+00A7")
}

// allProductionFilesPath returns absolute paths to non-test .go files in the
// current package. Used only for path-based helpers (not needed by the AST
// scanners which operate on filenames relative to package dir).
func allProductionFilesPath(t *testing.T) []string {
	t.Helper()
	absDir, err := filepath.Abs(".")
	require.NoError(t, err)
	entries, err := os.ReadDir(absDir)
	require.NoError(t, err)
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
			files = append(files, filepath.Join(absDir, e.Name()))
		}
	}
	return files
}
