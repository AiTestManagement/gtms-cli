package prompt

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssemble_VariableSubstitution(t *testing.T) {
	dir := t.TempDir()
	tmplPath := filepath.Join(dir, "template.txt")

	template := `Create test cases for requirement {requirement}.
Output format: {format}
Output to: {output_dir}
Branch: {branch}`

	err := os.WriteFile(tmplPath, []byte(template), 0644)
	require.NoError(t, err)

	vars := map[string]string{
		"requirement": "JIRA-456",
		"format":      "markdown",
		"output_dir":  "gtms/test/cases/",
		"branch":      "feature/create-JIRA-456",
	}

	result, err := Assemble(tmplPath, vars)
	require.NoError(t, err)
	assert.Contains(t, result, "requirement JIRA-456")
	assert.Contains(t, result, "Output format: markdown")
	assert.Contains(t, result, "Output to: gtms/test/cases/")
	assert.Contains(t, result, "Branch: feature/create-JIRA-456")
}

func TestAssemble_MissingVarsLeftAsIs(t *testing.T) {
	dir := t.TempDir()
	tmplPath := filepath.Join(dir, "template.txt")

	template := `Requirement: {requirement}
Unknown: {unknown_var}
Framework: {framework}`

	err := os.WriteFile(tmplPath, []byte(template), 0644)
	require.NoError(t, err)

	vars := map[string]string{
		"requirement": "JIRA-123",
	}

	result, err := Assemble(tmplPath, vars)
	require.NoError(t, err)
	assert.Contains(t, result, "Requirement: JIRA-123")
	assert.Contains(t, result, "{unknown_var}", "unrecognised placeholders should be left as-is")
	assert.Contains(t, result, "{framework}", "missing vars should be left as-is")
}

func TestAssemble_EmptyTemplate(t *testing.T) {
	dir := t.TempDir()
	tmplPath := filepath.Join(dir, "empty.txt")

	err := os.WriteFile(tmplPath, []byte(""), 0644)
	require.NoError(t, err)

	result, err := Assemble(tmplPath, map[string]string{"key": "val"})
	require.NoError(t, err)
	assert.Equal(t, "", result)
}

func TestAssemble_TemplateWithNoVariables(t *testing.T) {
	dir := t.TempDir()
	tmplPath := filepath.Join(dir, "static.txt")

	template := "This template has no variables at all."
	err := os.WriteFile(tmplPath, []byte(template), 0644)
	require.NoError(t, err)

	result, err := Assemble(tmplPath, map[string]string{"key": "val"})
	require.NoError(t, err)
	assert.Equal(t, template, result)
}

func TestAssemble_FileNotFound(t *testing.T) {
	_, err := Assemble("/nonexistent/template.txt", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reading prompt template")
}

func TestAssembleString(t *testing.T) {
	template := "Hello {name}, welcome to {project}!"
	vars := map[string]string{
		"name":    "Alice",
		"project": "GTMS",
	}

	result := AssembleString(template, vars)
	assert.Equal(t, "Hello Alice, welcome to GTMS!", result)
}

func TestAssembleString_MultipleOccurrences(t *testing.T) {
	template := "{name} says hello. {name} is here."
	vars := map[string]string{"name": "Bob"}

	result := AssembleString(template, vars)
	assert.Equal(t, "Bob says hello. Bob is here.", result)
}

func TestAssembleString_EmptyVars(t *testing.T) {
	template := "No substitution for {key}."
	result := AssembleString(template, nil)
	assert.Equal(t, "No substitution for {key}.", result)
}

// TestAssembleString_Determinism1000x verifies that AssembleString produces
// byte-identical output across 1000 invocations with the same inputs. Before
// the BUG-143 fix, Go's random map iteration order caused nondeterministic
// results when injected values contained placeholder-shaped tokens.
func TestAssembleString_Determinism1000x(t *testing.T) {
	// Template with multiple placeholders.
	template := "NAME=[{tc_name}]\nREF=[{reference}]\nCTX=[{context}]\nFW=[{framework}]\n"

	// The context value deliberately contains tokens that match other keys.
	vars := map[string]string{
		"tc_name":   "my-test",
		"reference": "REQ-42",
		"context":   "This context mentions {tc_name} and {reference} and {framework} tokens.",
		"framework": "bats",
	}

	results := make(map[string]struct{})
	for i := 0; i < 1000; i++ {
		out := AssembleString(template, vars)
		results[out] = struct{}{}
	}

	assert.Equal(t, 1, len(results), "AssembleString must be deterministic: expected 1 unique output, got %d", len(results))

	// Also verify the actual content is correct.
	result := AssembleString(template, vars)
	assert.Equal(t, "NAME=[my-test]\nREF=[REQ-42]\nCTX=[This context mentions {tc_name} and {reference} and {framework} tokens.]\nFW=[bats]\n", result)
}

// TestAssembleString_InjectedValuePreservation verifies that placeholder-shaped
// tokens carried inside an injected value are emitted verbatim and never
// substituted. This is the core property of the BUG-143 fix: injected content
// is data, not a template.
func TestAssembleString_InjectedValuePreservation(t *testing.T) {
	template := "NAME=[{tc_name}]\nCTX=[{context}]\nGUIDES=[{guides}]\n"

	vars := map[string]string{
		"tc_name": "resolved-name",
		"context": "INJECTED {tc_name} and {reference} and {guides} tokens.",
		"guides":  "<guide name=\"g1.md\">\nGUIDE has {tc_name} inside.\n</guide>",
		// reference is NOT in vars -- tests that an absent key inside injected
		// content is also left alone (not just present keys).
	}

	result := AssembleString(template, vars)

	// Template's own {tc_name} must be substituted.
	assert.Contains(t, result, "NAME=[resolved-name]")

	// Injected {tc_name} inside context must survive verbatim.
	assert.Contains(t, result, "INJECTED {tc_name} and {reference} and {guides} tokens.")

	// Injected {tc_name} inside guides must survive verbatim.
	assert.Contains(t, result, "GUIDE has {tc_name} inside.")

	// The template's {context} and {guides} slots must be substituted
	// (not left as literals).
	assert.NotContains(t, result, "CTX=[{context}]")
	assert.NotContains(t, result, "GUIDES=[{guides}]")
}
