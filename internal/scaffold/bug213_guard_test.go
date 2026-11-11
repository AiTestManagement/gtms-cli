package scaffold

// BUG-213 Owner Decision 5 source guard. All production Init, DemoSeed and
// UpdateDemoGuidance filesystem mutations must go through the small set of
// containment-aware writers. This AST guard fails if a new direct os.WriteFile,
// os.MkdirAll, os.Rename, os.Create/os.CreateTemp or os.OpenFile appears in any
// scaffold function outside the named allowlist, and if writeCompanion /
// writeFileAtomic gain a new caller outside the checked asset-installer path.
//
// It is a compile-time/code-structure guardrail (ENH-088 style), not a
// behaviour test, and runs in all tiers (pure AST parsing, no os/exec).

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBUG213_SourceGuard_NoUnsanctionedDirectWrites(t *testing.T) {
	// Functions permitted to make direct filesystem-mutating os.* calls: the
	// containment-aware writers themselves, plus the separately protected
	// gtms.config writers, the BUG-208 .gitignore writers, the checked asset
	// installer, the atomic writer it owns, and the dormant public helpers that
	// have no production callers (BUG-213 out of scope).
	allowedDirectWrite := map[string]bool{
		// BUG-213 containment-aware writers (safewrite.go).
		"safeCreateOnly":     true,
		"safeMkdirContained": true,
		"safeOverwrite":      true,
		// Asset installer and its atomic writer (REV-165).
		"applyUpgradeableAssetForParent": true,
		"writeFileAtomic":                true,
		// gtms.config writers (separately protected).
		"writeConfigForParent":       true,
		"WriteConfig":                true,
		"backupConfigNeverOverwrite": true,
		// BUG-208 .gitignore writers.
		"ensureGitignoreForParent":    true,
		"ensureGitignore":             true,
		"ensureGitignoreHasConfigBak": true,
		// Dormant public helpers, no production callers (BUG-213 out of scope).
		"WritePromptTemplates": true,
		"WriteStarterGuides":   true,
		"WriteAdapterStubs":    true,
	}
	mutating := map[string]bool{
		"WriteFile": true, "MkdirAll": true, "Mkdir": true,
		"Rename": true, "Create": true, "CreateTemp": true, "OpenFile": true,
	}
	// writeCompanion and writeFileAtomic are not containment-aware on their own;
	// they are safe only because the checked installer hands them an absolute
	// path it has already validated. Pin their callers.
	writeCompanionCallers := map[string]bool{"applyUpgradeableAssetForParent": true}
	writeFileAtomicCallers := map[string]bool{"applyUpgradeableAssetForParent": true, "writeCompanion": true}

	entries, err := os.ReadDir(".")
	require.NoError(t, err, "reading internal/scaffold/")

	fset := token.NewFileSet()
	var violations []string
	scanned := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		scanned++
		file, perr := parser.ParseFile(fset, name, nil, 0)
		require.NoError(t, perr, "parsing %s", name)

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			fnName := fn.Name.Name
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch fun := call.Fun.(type) {
				case *ast.SelectorExpr:
					if x, ok := fun.X.(*ast.Ident); ok && x.Name == "os" && mutating[fun.Sel.Name] {
						if !allowedDirectWrite[fnName] {
							violations = append(violations, fmt.Sprintf(
								"%s: %s() calls os.%s directly -- route it through a containment-aware writer (safeCreateOnly/safeMkdirContained/safeOverwrite)",
								name, fnName, fun.Sel.Name))
						}
					}
				case *ast.Ident:
					switch fun.Name {
					case "writeCompanion":
						if !writeCompanionCallers[fnName] {
							violations = append(violations, fmt.Sprintf(
								"%s: %s() calls writeCompanion -- allowed only from applyUpgradeableAssetForParent", name, fnName))
						}
					case "writeFileAtomic":
						if !writeFileAtomicCallers[fnName] {
							violations = append(violations, fmt.Sprintf(
								"%s: %s() calls writeFileAtomic -- allowed only from the checked asset-installer path", name, fnName))
						}
					}
				}
				return true
			})
		}
	}

	require.NotZero(t, scanned, "expected to scan at least one scaffold source file")
	require.Empty(t, violations,
		"BUG-213 Owner Decision 5: unsanctioned direct filesystem writes in internal/scaffold/:\n%s",
		strings.Join(violations, "\n"))
}
