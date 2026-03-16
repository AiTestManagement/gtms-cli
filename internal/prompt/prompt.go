// Package prompt handles template assembly for adapter invocations.
// Templates use {variable} placeholders that are replaced with values from a vars map.
package prompt

import (
	"fmt"
	"os"
	"strings"
)

// Assemble reads a template file and replaces {variable} placeholders with values
// from the vars map. Unrecognised placeholders are left as-is.
func Assemble(templatePath string, vars map[string]string) (string, error) {
	data, err := os.ReadFile(templatePath)
	if err != nil {
		return "", fmt.Errorf("reading prompt template: %w", err)
	}

	return AssembleString(string(data), vars), nil
}

// AssembleString replaces {variable} placeholders in a template string with values
// from the vars map. Unrecognised placeholders are left as-is.
func AssembleString(template string, vars map[string]string) string {
	result := template
	for key, value := range vars {
		placeholder := "{" + key + "}"
		result = strings.ReplaceAll(result, placeholder, value)
	}
	return result
}
