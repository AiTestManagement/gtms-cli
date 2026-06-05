package adapter

// Source-shape tests verify compile-time / code-structure invariants that were
// previously asserted by BATS acceptance tests grepping Go source files.
// These are architectural guardrails, not behaviour tests.
// See ENH-088 for the full audit and migration rationale.

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Audit #6: CommandFlags struct comments reference both create and automate ---

func TestSourceShape_CommandFlagsContextCommentReferencesCreateAndAutomate(t *testing.T) {
	src, err := os.ReadFile("types.go")
	require.NoError(t, err)
	content := string(src)

	// Find the Context field inside CommandFlags struct (not AdapterContext)
	// CommandFlags.Context has an inline comment like "// create + automate: ..."
	lines := strings.Split(content, "\n")
	inCommandFlags := false
	var contextLine string
	for _, line := range lines {
		if strings.Contains(line, "CommandFlags struct") {
			inCommandFlags = true
			continue
		}
		if inCommandFlags && strings.TrimSpace(line) == "}" {
			break
		}
		if inCommandFlags && strings.Contains(line, "Context ") &&
			!strings.Contains(line, "ContextFile") && strings.Contains(line, "string") {
			contextLine = line
			break
		}
	}
	require.NotEmpty(t, contextLine, "Context field line not found in CommandFlags struct")
	assert.Contains(t, strings.ToLower(contextLine), "create", "Context field comment should mention create")
	assert.Contains(t, strings.ToLower(contextLine), "automate", "Context field comment should mention automate")
}

func TestSourceShape_CommandFlagsContextFileCommentReferencesCreateAndAutomate(t *testing.T) {
	src, err := os.ReadFile("types.go")
	require.NoError(t, err)
	content := string(src)

	// Find ContextFile inside CommandFlags struct
	lines := strings.Split(content, "\n")
	inCommandFlags := false
	var contextFileLine string
	for _, line := range lines {
		if strings.Contains(line, "CommandFlags struct") {
			inCommandFlags = true
			continue
		}
		if inCommandFlags && strings.TrimSpace(line) == "}" {
			break
		}
		if inCommandFlags && strings.Contains(line, "ContextFile") && strings.Contains(line, "string") {
			contextFileLine = line
			break
		}
	}
	require.NotEmpty(t, contextFileLine, "ContextFile field line not found in CommandFlags struct")
	assert.Contains(t, strings.ToLower(contextFileLine), "create", "ContextFile comment should mention create")
	assert.Contains(t, strings.ToLower(contextFileLine), "automate", "ContextFile comment should mention automate")
}

// --- Audit #7: InvocationResult.Stdout comment documents streaming behaviour ---

func TestSourceShape_InvocationResultStdoutCommentDocumentsStreamingBehaviour(t *testing.T) {
	src, err := os.ReadFile("types.go")
	require.NoError(t, err)
	content := string(src)

	// Find the Stdout field line (inside InvocationResult)
	lines := strings.Split(content, "\n")
	var stdoutLine string
	for _, line := range lines {
		if strings.Contains(line, "Stdout") && strings.Contains(line, "string") &&
			!strings.Contains(line, "[]string") {
			stdoutLine = line
			break
		}
	}
	require.NotEmpty(t, stdoutLine, "Stdout field line not found in InvocationResult")
	assert.Contains(t, stdoutLine, "after closing tag")
	assert.Contains(t, stdoutLine, "before first delimiter")
	assert.Contains(t, stdoutLine, "all if no delimiters")
}

// --- Audit #9: invoker.go generates 20 tc-IDs, no math/rand ---

func TestSourceShape_InvokerGenerates20TcIDs(t *testing.T) {
	src, err := os.ReadFile("invoker.go")
	require.NoError(t, err)
	content := string(src)

	assert.Contains(t, content, `tcIDs := make([]string, 20)`)
	assert.Contains(t, content, `"tc-" + id.New()`)
	assert.Contains(t, content, `strings.Join(tcIDs, ",")`)
}

func TestSourceShape_InvokerNoMathRand(t *testing.T) {
	src, err := os.ReadFile("invoker.go")
	require.NoError(t, err)
	content := string(src)

	assert.NotContains(t, content, "math/rand",
		"invoker.go must not import math/rand — use crypto/rand via id.New()")
}

// --- Audit #10: writeFileBlock duplicate guard structure ---

func TestSourceShape_WriteFileBlockHasOsStatGuard(t *testing.T) {
	src, err := os.ReadFile("stream.go")
	require.NoError(t, err)
	content := string(src)

	assert.Contains(t, content, "os.Stat(")
	assert.Contains(t, content, "err == nil")
	assert.Contains(t, content, "skipping duplicate file")
	assert.Contains(t, content, "os.Stderr")
	assert.Contains(t, content, `return "", nil`)
}

// --- Audit #11: promptVars map includes tc_ids key ---

func TestSourceShape_InvokerPromptVarsIncludesTcIDs(t *testing.T) {
	src, err := os.ReadFile("invoker.go")
	require.NoError(t, err)
	content := string(src)

	assert.Contains(t, content, `"tc_ids"`)
	assert.Contains(t, content, "TestCaseIDs")
}

// --- Audit #12 + #19: TestCaseIDs field with 8hex comment ---

func TestSourceShape_TestCaseIDsFieldExists(t *testing.T) {
	src, err := os.ReadFile("types.go")
	require.NoError(t, err)
	content := string(src)

	assert.Contains(t, content, "TestCaseIDs")
	assert.Contains(t, content, `TestCaseIDs     string`)

	// Find the TestCaseIDs line and check its comment
	for _, line := range strings.Split(content, "\n") {
		if strings.Contains(line, "TestCaseIDs") {
			assert.Contains(t, line, "8hex", "TestCaseIDs comment should reference 8hex format")
			assert.Contains(t, strings.ToLower(line), "create command only",
				"TestCaseIDs comment should scope to create command")
			break
		}
	}
}

func TestSourceShape_NoStale7HexRefsInTypes(t *testing.T) {
	src, err := os.ReadFile("types.go")
	require.NoError(t, err)
	content := string(src)

	assert.NotContains(t, content, "7hex", "types.go should not contain stale 7hex references")
	assert.NotContains(t, content, "7-char", "types.go should not contain stale 7-char references")
}

// --- Audit #14: writeFileBlock call-site count and empty-path guards ---

func TestSourceShape_WriteFileBlockCallSiteCount(t *testing.T) {
	src, err := os.ReadFile("stream.go")
	require.NoError(t, err)
	content := string(src)

	// 4 call sites + 1 function definition = 5 occurrences of "writeFileBlock("
	count := strings.Count(content, "writeFileBlock(")
	assert.Equal(t, 5, count,
		"expected 4 writeFileBlock call sites + 1 definition = 5 occurrences")
}

func TestSourceShape_WriteFileBlockAllCallSitesGuardEmptyPath(t *testing.T) {
	src, err := os.ReadFile("stream.go")
	require.NoError(t, err)
	content := string(src)

	appendCount := strings.Count(content, "savedFiles = append(savedFiles, path)")
	assert.Equal(t, 4, appendCount, "expected exactly 4 savedFiles append sites")

	guardCount := strings.Count(content, `if path != ""`)
	assert.Equal(t, 4, guardCount, "expected exactly 4 empty-path guards")

	// Verify each append is preceded by its guard
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.Contains(line, "savedFiles = append(savedFiles, path)") {
			require.Greater(t, i, 0, "append cannot be on the first line")
			guardLine := strings.TrimSpace(lines[i-1])
			assert.Contains(t, guardLine, `if path != ""`,
				"line %d: append must be guarded by 'if path != \"\"' on preceding line", i+1)
		}
	}
}

// --- Audit #16: no stale 7-char references in invoker_test.go ---

func TestSourceShape_InvokerTestNoStale7CharRefs(t *testing.T) {
	src, err := os.ReadFile("invoker_test.go")
	require.NoError(t, err)
	content := string(src)

	assert.NotContains(t, content, "{7,8}", "invoker_test.go should not contain {7,8} regex patterns")

	stalePattern := regexp.MustCompile(`(?i)7hex|7-char|7 char|7 hex`)
	assert.False(t, stalePattern.MatchString(content),
		"invoker_test.go should not contain stale 7hex/7-char comment references")
}

// --- BUG-108: BuiltinAutomate must not contain BATS-specific skeleton literals ---

func TestSourceShape_BuiltinActionNoBATSSkeletonLiterals(t *testing.T) {
	src, err := os.ReadFile("builtin_action.go")
	require.NoError(t, err)
	content := string(src)

	// BATS shebang must not appear in core orchestration code
	assert.NotContains(t, content, "#!/usr/bin/env bats",
		"builtin_action.go must not contain BATS shebang -- skeleton belongs in framework_support.go")

	// BATS helper function calls must not appear in core
	assert.NotContains(t, content, "setup_file()",
		"builtin_action.go must not contain BATS setup_file() -- skeleton belongs in framework_support.go")

	// BATS @test marker must not appear in core
	assert.NotContains(t, content, "@test",
		"builtin_action.go must not contain BATS @test marker -- skeleton belongs in framework_support.go")

	// BATS helper load path must not appear in core
	assert.NotContains(t, content, "common-setup.bash",
		"builtin_action.go must not contain BATS helper filename -- skeleton belongs in framework_support.go")

	// BATS setup function name must not appear in core
	assert.NotContains(t, content, "_common_setup",
		"builtin_action.go must not contain BATS setup function name -- skeleton belongs in framework_support.go")
}

func TestSourceShape_BuiltinActionNoFrameworkBranch(t *testing.T) {
	src, err := os.ReadFile("builtin_action.go")
	require.NoError(t, err)
	content := string(src)

	// Core must not branch on specific framework values
	assert.NotContains(t, content, `framework == "bats"`,
		"builtin_action.go must not contain framework-specific equality check")
	assert.NotContains(t, content, `framework != "bats"`,
		"builtin_action.go must not contain framework-specific inequality check")
}
