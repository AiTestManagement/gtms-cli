package reader

// Source-shape tests verify compile-time / code-structure invariants that were
// previously asserted by BATS acceptance tests grepping Go source files.
// These are architectural guardrails, not behaviour tests.
// See ENH-088 for the full audit and migration rationale.

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

// --- Audit #15: specscanner regex uses {3,8} upper bound ---

func TestSourceShape_SpecScannerRegexUpperBound8(t *testing.T) {
	src, err := os.ReadFile("specscanner.go")
	require.NoError(t, err)
	content := string(src)

	assert.Contains(t, content, "{3,8}",
		"specscanner.go regex must use {3,8} to accept 3-to-8-char hex IDs")
}

// --- ENH-128: delete.go must contain zero framework-specific literals ---

// TestSourceShape_DeleteGoNoFrameworkLiterals verifies that delete.go contains
// no framework-specific literals outside of doc comments.
// ENH-128 AC #1: zero literal framework names, extensions, or paths.
func TestSourceShape_DeleteGoNoFrameworkLiterals(t *testing.T) {
	src, err := os.ReadFile("delete.go")
	require.NoError(t, err)

	// Strip doc comments (lines starting with //) to allow framework names in documentation
	var codeLines []string
	for _, line := range strings.Split(string(src), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		codeLines = append(codeLines, line)
	}
	code := strings.Join(codeLines, "\n")

	// Framework-specific literals that must not appear in non-comment code
	forbidden := []string{
		`"bats"`,
		`".bats"`,
		`"junit"`,
		`"test/acceptance"`,
		`"test", "acceptance"`,
		`"results/junit"`,
		`"results", "junit"`,
		`findBATSScripts`,
		`findJUnitResults`,
		`BATSScripts`,
		`JUnitResults`,
	}

	for _, lit := range forbidden {
		assert.False(t, strings.Contains(code, lit),
			"delete.go non-comment code must not contain framework literal %q", lit)
	}
}

// --- CON-023 / ENH-145/146 Task 12: architectural guardrails ---

// TestSourceShape_NoLegacyAutomationRecordReadsInReader pins that no
// production file in internal/reader/ reads or writes the retired
// gtms/automation/records/*.automation.md surface, nor depends on
// legacy automation-record helpers from internal/pipeline. The
// migration tool (scripts/migrate-wiring/) is the only legitimate
// remaining caller of those helpers.
//
// Non-comment code only — provenance comments mentioning the legacy
// names are fine.
func TestSourceShape_NoLegacyAutomationRecordReadsInReader(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)

	forbidden := []string{
		`pipeline.FindAutomationRecord`,
		`pipeline.WriteAutomationRecord`,
		`pipeline.ReadAutomationRecord`,
		`pipeline.BuildAutomationRecord`,
		`pipeline.UpdateExecutionResult`,
		`pipeline.CreateAutomationRecord`,
		`pipeline.TryAutoCreateRecord`,
		`pipeline.ResolveArtefact`,
		`layout.RecordsDir(`,
		`".automation.md"`,
		`automation/records`,
	}

	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		require.NoError(t, err)
		code := stripGoComments(t, f, string(src))
		for _, lit := range forbidden {
			assert.NotContainsf(t, code, lit,
				"%s: production reader code must not reference legacy automation-record surface %q", f, lit)
		}
	}
}

// TestSourceShape_NoWiringWriteFromReader pins that no production code
// in internal/reader/ writes a wiring file. Wiring is read-only on the
// reader side; only gtms automate and gtms link create or refresh
// wiring records (CON-023 / ENH-145).
func TestSourceShape_NoWiringWriteFromReader(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		require.NoError(t, err)
		code := stripGoComments(t, f, string(src))
		// `wiring.Write(` is the canonical writer; reject any call to it
		// from the reader package.
		assert.NotContainsf(t, code, `wiring.Write(`,
			"%s: production reader code must not call wiring.Write — wiring is read-only on the reader path", f)
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
		// Fall back to the raw source — guarantees a strict (over)check.
		return src
	}
	// Walk file comments and blank them out in the source. Simpler than
	// a full token-level walk and good enough for our literal scans.
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
