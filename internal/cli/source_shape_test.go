package cli

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

// --- Audit #1: bulk skip uses skipIcon(reason), not hardcoded IconWarning ---

func TestSourceShape_BulkSkipUsesSkipIconHelper(t *testing.T) {
	src, err := os.ReadFile("execute.go")
	require.NoError(t, err)
	content := string(src)

	assert.Contains(t, content, "skipIcon(reason)",
		"bulk skip Fprintf must call skipIcon(reason) for dynamic icon selection")

	// The Fprintf line containing "skipped (%s)" must NOT contain "output.IconWarning"
	for _, line := range strings.Split(content, "\n") {
		if strings.Contains(line, `skipped (%s)`) {
			assert.NotContains(t, line, "output.IconWarning",
				"bulk skip Fprintf must not hardcode output.IconWarning — use skipIcon(reason)")
		}
	}
}

// --- Audit #2: shouldSkipExecute function shape ---

func TestSourceShape_ShouldSkipExecuteFunctionExists(t *testing.T) {
	src, err := os.ReadFile("execute.go")
	require.NoError(t, err)
	content := string(src)

	assert.Contains(t, content, "func shouldSkipExecute",
		"shouldSkipExecute function must exist in execute.go")

	// Verify signature contains expected parameter types
	for _, line := range strings.Split(content, "\n") {
		if strings.Contains(line, "func shouldSkipExecute") {
			assert.Contains(t, line, "root, tcID string")
			assert.Contains(t, line, "string")
			break
		}
	}
}

func TestSourceShape_ShouldSkipExecuteReasonStrings(t *testing.T) {
	src, err := os.ReadFile("execute.go")
	require.NoError(t, err)
	content := string(src)

	// CON-023 / ENH-145 / ENH-146: the legacy reasons "no automation
	// record" and "automation not ready" are retired (no record on the
	// execute path; wiring is the source of truth). The bulk loop now
	// uses wiring-aware reasons: "not wired", "stale wiring", "missing
	// artefact", "multiple frameworks — specify --framework", plus the
	// retained "already passing" and "active task exists".
	expectedReasons := []string{
		"not wired",
		"already passing",
		"active task exists",
		"stale wiring",
		"missing artefact",
	}
	for _, reason := range expectedReasons {
		assert.Contains(t, content, reason,
			"the bulk-execute path must surface reason string: %s", reason)
	}

	// At least 2 empty returns (no-skip default path on selectWiringForBulk
	// success + no-skip default on shouldSkipExecute).
	emptyReturnCount := strings.Count(content, `return ""`)
	assert.GreaterOrEqual(t, emptyReturnCount, 2,
		"bulk-skip surface must have at least 2 'return \"\"' paths")
}

func TestSourceShape_ShouldSkipExecuteHasStaleHashBypass(t *testing.T) {
	src, err := os.ReadFile("execute.go")
	require.NoError(t, err)
	content := string(src)

	// CON-023 / ENH-145: drift detection is now in checkWiringDrift,
	// which recomputes testcase-hash and artefact-hash against the
	// wiring record. --allow-stale is the bypass at the CLI surface.
	assert.Contains(t, content, "ArtefactHash",
		"execute.go must compute ArtefactHash for the result contract")
	assert.Contains(t, content, "checkWiringDrift",
		"execute.go must call checkWiringDrift before invoking the adapter")
	assert.Contains(t, content, "--allow-stale",
		"execute.go must surface --allow-stale as the drift-check bypass")
}

// --- Audit #3: skipIcon is unexported with correct signature ---

func TestSourceShape_SkipIconUnexportedWithCorrectSignature(t *testing.T) {
	src, err := os.ReadFile("execute.go")
	require.NoError(t, err)
	content := string(src)

	assert.Contains(t, content, "func skipIcon(reason string) string",
		"skipIcon must be unexported with signature: func skipIcon(reason string) string")

	assert.NotContains(t, content, "func SkipIcon",
		"exported SkipIcon must not exist — the function is package-private")
}

// --- Audit #5: execute_skip_test.go smoke-tier properties ---

func TestSourceShape_ExecuteSkipTestNoOsExec(t *testing.T) {
	src, err := os.ReadFile("execute_skip_test.go")
	require.NoError(t, err)
	content := string(src)

	assert.NotContains(t, content, "os/exec",
		"execute_skip_test.go must not import os/exec — it is a pure unit test")
}

func TestSourceShape_ExecuteSkipTestNoShortSkip(t *testing.T) {
	src, err := os.ReadFile("execute_skip_test.go")
	require.NoError(t, err)
	content := string(src)

	assert.NotContains(t, content, "skipIfShort",
		"execute_skip_test.go must not contain skipIfShort — runs in smoke tier")
	assert.NotContains(t, content, "testing.Short",
		"execute_skip_test.go must not check testing.Short — runs in smoke tier")
}

// --- ENH-120: formatXxxOutput functions must not contain "Task created:" in success paths ---

func TestSourceShape_NoTaskCreatedInSuccessOutput(t *testing.T) {
	// The format*Output functions in create.go, automate.go, execute.go must
	// not use "Task created:" in the success (non-error) output path.
	// Error paths are allowed to keep "Task failed:" for debugging.
	cliFiles := []string{"create.go", "automate.go", "execute.go"}
	for _, filename := range cliFiles {
		src, err := os.ReadFile(filename)
		require.NoError(t, err, "failed to read %s", filename)
		content := string(src)

		assert.NotContains(t, content, `"Task created:`,
			"%s must not use 'Task created:' in output — use artefact-focused headline (ENH-120)", filename)
		assert.NotContains(t, content, `Task completed with warnings:`,
			"%s must not use 'Task completed with warnings:' — use artefact-focused headline (ENH-120)", filename)
	}
}

// --- ENH-128: cli/delete.go must not contain framework-specific output labels ---

func TestSourceShape_CliDeleteGoNoFrameworkLabels(t *testing.T) {
	src, err := os.ReadFile("delete.go")
	require.NoError(t, err)

	// Strip doc comments to allow framework names in documentation
	var codeLines []string
	for _, line := range strings.Split(string(src), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		codeLines = append(codeLines, line)
	}
	code := strings.Join(codeLines, "\n")

	// Framework-specific output labels that must not appear
	forbidden := []string{
		`"BATS scripts"`,
		`"JUnit results"`,
		`BATS scripts:`,
		`JUnit results:`,
		`BATSScripts`,
		`JUnitResults`,
	}

	for _, lit := range forbidden {
		assert.False(t, strings.Contains(code, lit),
			"cli/delete.go non-comment code must not contain framework label %q", lit)
	}
}

// --- Audit #8: CLI command files contain no ID generation logic ---

func TestSourceShape_CLIFilesNoIDGeneration(t *testing.T) {
	cliFiles := []string{"create.go", "automate.go", "execute.go"}
	forbidden := []string{"TestCaseIDs", "tc_ids", "id.New()", "crypto/rand"}

	for _, filename := range cliFiles {
		src, err := os.ReadFile(filename)
		require.NoError(t, err, "failed to read %s", filename)
		content := string(src)

		for _, symbol := range forbidden {
			assert.NotContains(t, content, symbol,
				"%s must not contain ID generation logic (%s) — that belongs in the adapter/invoker", filename, symbol)
		}
	}
}

// --- ENH-138: legacy manual bypass symbols must not exist ---

func TestSourceShape_NoLegacyManualBypass(t *testing.T) {
	src, err := os.ReadFile("execute.go")
	require.NoError(t, err)
	content := string(src)

	for _, sym := range []string{"runManualResult", "resultFlag", "notesFlag"} {
		assert.NotContains(t, content, sym,
			"execute.go must not contain %q — ENH-138 removed the legacy manual bypass", sym)
	}

	// Also verify the pipeline-side symbols are gone
	pSrc, err := os.ReadFile("../pipeline/pipeline.go")
	require.NoError(t, err)
	pContent := string(pSrc)

	for _, sym := range []string{"WriteManualResult", "RecordManualResult"} {
		assert.NotContains(t, pContent, sym,
			"pipeline.go must not contain %q — ENH-138 removed the legacy manual writers", sym)
	}
}

// --- CON-023 / ENH-145/146 Task 12: architectural guardrails ---

// TestSourceShape_NoLegacyAutomationRecordReadsInCLI pins that no
// production file in internal/cli/ reads or writes the retired
// gtms/automation/records/*.automation.md surface, nor depends on
// legacy automation-record helpers from internal/pipeline. The
// migration tool (scripts/migrate-wiring/) is the only legitimate
// remaining caller of those helpers.
//
// Non-comment code only — provenance comments mentioning the legacy
// names are fine.
func TestSourceShape_NoLegacyAutomationRecordReadsInCLI(t *testing.T) {
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
		code := stripGoCommentsCLI(t, f, string(src))
		for _, lit := range forbidden {
			assert.NotContainsf(t, code, lit,
				"%s: production CLI code must not reference legacy automation-record surface %q", f, lit)
		}
	}
}

// TestSourceShape_WiringWriteFromExecute_OnlyInBootstrap pins that
// wiring.Write in execute.go is confined to the bootstrapPendingWiring
// function (ENH-151). The execute path is otherwise read-only for wiring
// (CON-023 / ENH-145). The single allowed call is the one-way
// pending → <real hash> bootstrap that runs before adapter invocation.
//
// Shape: count occurrences of wiring.Write( in the comment-stripped
// source. Exactly one is permitted (inside bootstrapPendingWiring);
// a second call anywhere else in execute.go is a violation.
func TestSourceShape_WiringWriteFromExecute_OnlyInBootstrap(t *testing.T) {
	src, err := os.ReadFile("execute.go")
	require.NoError(t, err)
	code := stripGoCommentsCLI(t, "execute.go", string(src))
	count := strings.Count(code, `wiring.Write(`)
	assert.Equal(t, 1, count,
		"execute.go must contain exactly one wiring.Write call (inside bootstrapPendingWiring); found %d", count)
	assert.Contains(t, code, `func bootstrapPendingWiring(`,
		"execute.go must define bootstrapPendingWiring — the single allowed wiring-mutation path on execute")
}

// stripGoCommentsCLI removes // line comments and /* */ block comments
// from Go source so source-shape literal searches do not match
// provenance notes. Returns the comment-stripped source.
func stripGoCommentsCLI(t *testing.T, name, src string) string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
	if err != nil {
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
