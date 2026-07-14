package layout

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultFieldValues(t *testing.T) {
	paths := Current()
	assert.Equal(t, "gtms", paths.Parent)
	assert.Equal(t, "gtms/test/cases", paths.TestCases)
	assert.Equal(t, "gtms/test/templates", paths.TestTemplates)
	assert.Equal(t, "gtms/test/guides", paths.TestGuides)
	// ENH-165: new prompts slot under gtms/test/.
	assert.Equal(t, "gtms/test/prompts", paths.TestPrompts)
	assert.Equal(t, "gtms/automation", paths.Automation)
	assert.Equal(t, "gtms/tasks", paths.Tasks)
	assert.Equal(t, "gtms/execution", paths.Execution)
	assert.Equal(t, "gtms/scripts", paths.Scripts)
	assert.Equal(t, "gtms/manual", paths.Manual)
	assert.Equal(t, "gtms/schemas", paths.Schemas)
}

func TestAutomationDir(t *testing.T) {
	root := "/project"
	assert.Equal(t, filepath.Join(root, "gtms", "automation"), AutomationDir(root))
}

func TestRecordsDir(t *testing.T) {
	root := "/project"
	assert.Equal(t, filepath.Join(root, "gtms", "automation", "records"), RecordsDir(root))
}

func TestWiringDir(t *testing.T) {
	root := "/project"
	assert.Equal(t, filepath.Join(root, "gtms", "automation", "wiring"), WiringDir(root))
}

func TestWiringDir_WithRenamedParent(t *testing.T) {
	resetLayoutForTest(t)
	InitFromParent("testing")
	root := "/project"
	assert.Equal(t, filepath.Join(root, "testing", "automation", "wiring"), WiringDir(root))
}

func TestSpecsDir(t *testing.T) {
	root := "/project"
	assert.Equal(t, filepath.Join(root, "gtms", "automation", "specs"), SpecsDir(root))
}

func TestTasksDir(t *testing.T) {
	root := "/project"
	assert.Equal(t, filepath.Join(root, "gtms", "tasks"), TasksDir(root))
}

func TestExecutionDir(t *testing.T) {
	root := "/project"
	assert.Equal(t, filepath.Join(root, "gtms", "execution"), ExecutionDir(root))
}

func TestScriptsDir(t *testing.T) {
	root := "/project"
	assert.Equal(t, filepath.Join(root, "gtms", "scripts"), ScriptsDir(root))
}

func TestInitFromParent(t *testing.T) {
	resetLayoutForTest(t)

	InitFromParent("testing")
	paths := Current()
	assert.Equal(t, "testing", paths.Parent)
	assert.Equal(t, "testing/test/cases", paths.TestCases)
	assert.Equal(t, "testing/test/templates", paths.TestTemplates)
	assert.Equal(t, "testing/test/guides", paths.TestGuides)
	// ENH-165: prompts slot tracks renamed parent.
	assert.Equal(t, "testing/test/prompts", paths.TestPrompts)
	assert.Equal(t, "testing/automation", paths.Automation)
	assert.Equal(t, "testing/tasks", paths.Tasks)
	assert.Equal(t, "testing/execution", paths.Execution)
	assert.Equal(t, "testing/scripts", paths.Scripts)
	assert.Equal(t, "testing/manual", paths.Manual)
	assert.Equal(t, "testing/schemas", paths.Schemas)
}

func TestParentDir(t *testing.T) {
	// With default "gtms/test/cases", ParentDir should return "gtms"
	resetLayoutForTest(t)

	assert.Equal(t, "gtms", ParentDir())

	InitFromParent("testing")
	assert.Equal(t, "testing", ParentDir())
}

func TestInitFromParent_RejectsInvalidInput(t *testing.T) {
	resetLayoutForTest(t)

	cases := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"forward slash", "a/b"},
		{"dot-dot", ".."},
		{"single dot", "."},
		{"embedded dot-dot with slash", "a/../b"},
		{"backslash", "a\\b"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Panics(t, func() { InitFromParent(tc.input) },
				"InitFromParent(%q) should panic", tc.input)
		})
	}
}

func TestAttachmentsDir(t *testing.T) {
	root := "/project"
	assert.Equal(t, filepath.Join(root, "gtms", "execution", "attachments"), AttachmentsDir(root))
}

func TestExecutionLogsDir(t *testing.T) {
	root := "/project"
	assert.Equal(t, filepath.Join(root, "gtms", "execution", "logs"), ExecutionLogsDir(root))
}

func TestAttachmentsDir_WithRenamedParent(t *testing.T) {
	resetLayoutForTest(t)

	InitFromParent("testing")
	root := "/project"
	assert.Equal(t, filepath.Join(root, "testing", "execution", "attachments"), AttachmentsDir(root))
}

func TestExecutionLogsDir_WithRenamedParent(t *testing.T) {
	resetLayoutForTest(t)

	InitFromParent("testing")
	root := "/project"
	assert.Equal(t, filepath.Join(root, "testing", "execution", "logs"), ExecutionLogsDir(root))
}

// ENH-132: Manual and Schemas directory helpers
func TestManualDir(t *testing.T) {
	root := "/project"
	assert.Equal(t, filepath.Join(root, "gtms", "manual"), ManualDir(root))
}

func TestManualRecordsDir(t *testing.T) {
	root := "/project"
	assert.Equal(t, filepath.Join(root, "gtms", "manual", "records"), ManualRecordsDir(root))
}

func TestManualTemplatesDir(t *testing.T) {
	root := "/project"
	assert.Equal(t, filepath.Join(root, "gtms", "manual", "templates"), ManualTemplatesDir(root))
}

func TestSchemasDir(t *testing.T) {
	root := "/project"
	assert.Equal(t, filepath.Join(root, "gtms", "schemas"), SchemasDir(root))
}

func TestManualDir_WithRenamedParent(t *testing.T) {
	resetLayoutForTest(t)

	InitFromParent("testing")
	root := "/project"
	assert.Equal(t, filepath.Join(root, "testing", "manual"), ManualDir(root))
}

func TestManualRecordsDir_WithRenamedParent(t *testing.T) {
	resetLayoutForTest(t)

	InitFromParent("testing")
	root := "/project"
	assert.Equal(t, filepath.Join(root, "testing", "manual", "records"), ManualRecordsDir(root))
}

func TestManualTemplatesDir_WithRenamedParent(t *testing.T) {
	resetLayoutForTest(t)

	InitFromParent("testing")
	root := "/project"
	assert.Equal(t, filepath.Join(root, "testing", "manual", "templates"), ManualTemplatesDir(root))
}

func TestSchemasDir_WithRenamedParent(t *testing.T) {
	resetLayoutForTest(t)

	InitFromParent("testing")
	root := "/project"
	assert.Equal(t, filepath.Join(root, "testing", "schemas"), SchemasDir(root))
}

func TestInitFromParent_AcceptsValidInput(t *testing.T) {
	resetLayoutForTest(t)

	cases := []struct {
		input    string
		wantCase string
	}{
		{"gtms", "gtms/test/cases"},
		{"testing", "testing/test/cases"},
		{"my-tests", "my-tests/test/cases"},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			assert.NotPanics(t, func() { InitFromParent(tc.input) },
				"InitFromParent(%q) should not panic", tc.input)
			assert.Equal(t, tc.wantCase, Current().TestCases)
			// Restore for next subtest
			InitFromParent("gtms")
		})
	}
}

func TestInitFromParent_IsReinitializable(t *testing.T) {
	resetLayoutForTest(t)

	InitFromParent("testing")
	assert.Equal(t, "testing/test/cases", Current().TestCases)

	InitFromParent("gtms")
	assert.Equal(t, "gtms/test/cases", Current().TestCases)
}

func TestInitFromParentAndReaders_NoRace(t *testing.T) {
	resetLayoutForTest(t)

	parents := []string{"testing-a", "testing-b", "gtms"}
	var wg sync.WaitGroup

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				_ = Current()
				_ = ParentDir()
				_ = TestCasesDir("/project")
				_ = RecordsDir("/project")
				_ = ExecutionLogsDir("/project")
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			InitFromParent(parents[i%len(parents)])
		}
	}()

	wg.Wait()

	got := Current().TestCases
	assert.Contains(t, []string{"testing-a/test/cases", "testing-b/test/cases", "gtms/test/cases"}, got)
}

func resetLayoutForTest(t *testing.T) {
	t.Helper()
	orig := Current()
	t.Cleanup(func() {
		defaultMu.Lock()
		defaultPaths = orig
		defaultMu.Unlock()
	})
}

// --- ENH-164: new test-slot layout helpers ---
//
// NOTE FOR IMPLEMENTATION AGENT: the existing `func TestCasesDir(t
// *testing.T)` at the top of this file tests the legacy `CasesDir(root)`
// helper, which ceases to exist after the rename. You must DELETE that
// legacy test function as part of the refactor so its name is freed for
// the new test below. If you don't, you'll hit a redeclaration compile
// error ("TestCasesDir redeclared in this block") because the new
// production function `func TestCasesDir(root string) string` and the
// legacy test `func TestCasesDir(t *testing.T)` are both in package
// layout. Same applies to any other legacy tests that exercise the old
// `CasesDir` symbol.

func TestTestCasesDir(t *testing.T) {
	resetLayoutForTest(t)
	root := "/project"
	assert.Equal(t, filepath.Join(root, "gtms", "test", "cases"), TestCasesDir(root))
}

func TestTestTemplatesDir(t *testing.T) {
	resetLayoutForTest(t)
	root := "/project"
	assert.Equal(t, filepath.Join(root, "gtms", "test", "templates"), TestTemplatesDir(root))
}

func TestTestGuidesDir(t *testing.T) {
	resetLayoutForTest(t)
	root := "/project"
	assert.Equal(t, filepath.Join(root, "gtms", "test", "guides"), TestGuidesDir(root))
}

// ENH-165: gtms/test/prompts/ slot for Tier 1 adapter prompt templates.
// Double-Test prefix is deliberate -- the production helper is TestPromptsDir
// and the test function is TestTestPromptsDir to mirror the TestTestCasesDir /
// TestTestTemplatesDir / TestTestGuidesDir family.
func TestTestPromptsDir(t *testing.T) {
	resetLayoutForTest(t)
	root := "/project"
	assert.Equal(t, filepath.Join(root, "gtms", "test", "prompts"), TestPromptsDir(root))
}

func TestTestPromptsDir_WithRenamedParent(t *testing.T) {
	resetLayoutForTest(t)
	InitFromParent("testing")
	root := "/project"
	assert.Equal(t, filepath.Join(root, "testing", "test", "prompts"), TestPromptsDir(root))
}

func TestTestCasesDir_WithRenamedParent(t *testing.T) {
	resetLayoutForTest(t)
	InitFromParent("testing")
	root := "/project"
	assert.Equal(t, filepath.Join(root, "testing", "test", "cases"), TestCasesDir(root))
}

func TestTestTemplatesDir_WithRenamedParent(t *testing.T) {
	resetLayoutForTest(t)
	InitFromParent("testing")
	root := "/project"
	assert.Equal(t, filepath.Join(root, "testing", "test", "templates"), TestTemplatesDir(root))
}

func TestTestGuidesDir_WithRenamedParent(t *testing.T) {
	resetLayoutForTest(t)
	InitFromParent("testing")
	root := "/project"
	assert.Equal(t, filepath.Join(root, "testing", "test", "guides"), TestGuidesDir(root))
}

// --- ENH-164: source-shape guardrail -- legacy CasesDir symbol must not
// remain anywhere in Go source under internal/ or cmd/. Scope is strictly
// .go files under those two roots; documentation, PRPs, archived records,
// and historical text references are out of scope per the ENH §
// "Helper rename" acceptance criterion.
//
// Implementation: walks .go files under internal/ and cmd/ from the project
// root, parses each with go/parser, and inspects the AST for any
// *ast.Ident with Name == "CasesDir". Because comments are not part of
// the inspected AST and string literals are *ast.BasicLit (not Ident),
// historical mentions in comments and test-fixture strings are naturally
// excluded. Only Go identifier uses -- declarations, calls, selector
// fields like `layout.CasesDir`, etc. -- trigger a hit.

func TestSourceShape_NoLegacyCasesDirSymbol(t *testing.T) {
	root := findGTMSProjectRootForLayoutTest(t)

	fset := token.NewFileSet()
	var offenders []string
	for _, sub := range []string{"internal", "cmd"} {
		base := filepath.Join(root, sub)
		walkErr := filepath.WalkDir(base, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			file, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if parseErr != nil {
				return fmt.Errorf("parse %s: %w", path, parseErr)
			}
			ast.Inspect(file, func(n ast.Node) bool {
				ident, ok := n.(*ast.Ident)
				if !ok {
					return true
				}
				if ident.Name == "CasesDir" {
					rel, _ := filepath.Rel(root, path)
					if rel == "" {
						rel = path
					}
					offenders = append(offenders, fmt.Sprintf("%s:%d", filepath.ToSlash(rel), fset.Position(ident.Pos()).Line))
				}
				return true
			})
			return nil
		})
		require.NoError(t, walkErr, "walking %s", sub)
	}

	assert.Empty(t, offenders,
		"ENH-164 AC: legacy CasesDir symbol must not remain in Go source under internal/ or cmd/; "+
			"rename to TestCasesDir. Offending references: %v", offenders)
}

// findGTMSProjectRootForLayoutTest walks up from cwd looking for gtms.config.
// The unique name avoids colliding with the same helper defined in
// internal/cli/tree_integrity_test.go.
func findGTMSProjectRootForLayoutTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "gtms.config")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("project root not found: no gtms.config in any ancestor of %s", dir)
		}
		dir = parent
	}
}
