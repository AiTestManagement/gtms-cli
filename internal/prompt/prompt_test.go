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
		"output_dir":  "gtms/cases/",
		"branch":      "feature/create-JIRA-456",
	}

	result, err := Assemble(tmplPath, vars)
	require.NoError(t, err)
	assert.Contains(t, result, "requirement JIRA-456")
	assert.Contains(t, result, "Output format: markdown")
	assert.Contains(t, result, "Output to: gtms/cases/")
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
