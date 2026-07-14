package result

// Source-shape tests verify compile-time / code-structure invariants for
// the result package and its consumers. These are architectural guardrails,
// not behaviour tests.

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BUG-130: TestSourceShape_ResultsDirWalkersUseCommandPredicate flags any
// production file in internal/cli/ or internal/reader/ that walks
// .gtms/results and filters on rc.Command without routing through
// result.IsTerminalExecuteContract. This catches new consumers that
// derive pass/skip with inline command checks instead of the shared
// predicate.
//
// Detection heuristic: a file that contains BOTH a results-dir read
// indicator ("handoff.yaml" or ".gtms/results" or "result.Read") AND
// an inline command field access pattern ("rc.Command" or
// ".Command !=" or ".Command ==") in non-comment code MUST also
// contain "IsTerminalExecuteContract" in non-comment code. If it
// does not, it is likely filtering on command without the predicate.
func TestSourceShape_ResultsDirWalkersUseCommandPredicate(t *testing.T) {
	dirs := []string{
		filepath.Join("..", "cli"),
		filepath.Join("..", "reader"),
	}

	resultsDirIndicators := []string{
		"handoff.yaml",
		`.gtms/results`,
		`result.Read`,
	}

	commandAccessPatterns := []string{
		"rc.Command",
		".Command !=",
		".Command ==",
	}

	for _, dir := range dirs {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		require.NoError(t, err)

		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			src, err := os.ReadFile(f)
			require.NoError(t, err)
			code := stripGoComments(t, f, string(src))

			// Does this file read from the results directory?
			hasResultsRead := false
			for _, indicator := range resultsDirIndicators {
				if strings.Contains(code, indicator) {
					hasResultsRead = true
					break
				}
			}
			if !hasResultsRead {
				continue
			}

			// Does it access the Command field?
			hasCommandAccess := false
			for _, pattern := range commandAccessPatterns {
				if strings.Contains(code, pattern) {
					hasCommandAccess = true
					break
				}
			}
			if !hasCommandAccess {
				continue
			}

			// If it reads results AND accesses Command, it must use the predicate.
			assert.Containsf(t, code, "IsTerminalExecuteContract",
				"%s: reads .gtms/results and filters on rc.Command but does not use "+
					"result.IsTerminalExecuteContract -- route through the BUG-130 "+
					"structural guard instead of inline command checks", f)
		}
	}
}

// stripGoComments removes // line comments and /* */ block comments
// from Go source so source-shape literal searches do not match
// provenance notes. Returns the comment-stripped source.
func stripGoComments(t *testing.T, name, src string) string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
	if err != nil {
		// Fall back to the raw source -- guarantees a strict (over)check.
		return src
	}
	out := []byte(src)
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			start := fset.Position(c.Pos()).Offset
			end := fset.Position(c.End()).Offset
			if start < 0 || end > len(out) || start > end {
				continue
			}
			for i := start; i < end; i++ {
				if out[i] != '\n' {
					out[i] = ' '
				}
			}
		}
	}
	return string(out)
}
