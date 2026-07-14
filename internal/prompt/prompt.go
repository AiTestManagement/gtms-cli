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
//
// Uses a single-pass scanner: the template is read left-to-right, each {key} token
// is resolved by map lookup, and the value is emitted verbatim without rescanning.
// This guarantees deterministic output and prevents substitution into injected
// content (context files, guides, testcase content) that may contain
// placeholder-shaped text. See BUG-143.
func AssembleString(template string, vars map[string]string) string {
	// Fast path: no placeholders possible if no '{' present.
	if !strings.Contains(template, "{") {
		return template
	}

	var b strings.Builder
	b.Grow(len(template))

	i := 0
	for i < len(template) {
		if template[i] != '{' {
			b.WriteByte(template[i])
			i++
			continue
		}

		// Found '{' -- look for a valid key followed by '}'.
		// Valid key: one or more characters matching [a-z][a-z0-9_]*
		j := i + 1
		if j < len(template) && isKeyStart(template[j]) {
			// Scan the rest of the key.
			k := j + 1
			for k < len(template) && isKeyChar(template[k]) {
				k++
			}
			if k < len(template) && template[k] == '}' {
				// We have a valid placeholder {key}.
				key := template[j:k]
				if value, ok := vars[key]; ok {
					// Key present: emit value verbatim (no rescan).
					b.WriteString(value)
				} else {
					// Key absent: emit placeholder unchanged.
					b.WriteString(template[i : k+1])
				}
				i = k + 1
				continue
			}
		}

		// Not a valid placeholder -- emit the literal '{'.
		b.WriteByte('{')
		i++
	}

	return b.String()
}

// isKeyStart reports whether c is a valid first character of a placeholder key.
func isKeyStart(c byte) bool {
	return c >= 'a' && c <= 'z'
}

// isKeyChar reports whether c is a valid continuation character of a placeholder key.
func isKeyChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_'
}
