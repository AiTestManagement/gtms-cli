package cli

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateTargetID_ValidInputs(t *testing.T) {
	valid := []string{
		"REQ-001",
		"tc-007",
		"tc-a1b2c3d",
		"my.test-case",
		"cwd-scoping/tc-abc123",
		"path/to/requirement",
		"JIRA-456",
		"simple",
		"a",
		"A123",
		"tc-a1b2c3d-login-happy-path",
		"my_test_01",
		strings.Repeat("a", 128), // exactly at max length
	}

	for _, id := range valid {
		t.Run(id, func(t *testing.T) {
			err := validateTargetID(id)
			assert.NoError(t, err, "expected %q to be valid", id)
		})
	}
}

func TestValidateTargetID_InvalidInputs(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"shell command substitution", "tc-$(whoami)"},
		{"shell semicolon", "tc-;rm -rf /"},
		{"path traversal", "../../etc/passwd"},
		{"backtick execution", "tc-`id`"},
		{"pipe injection", "tc-|cat /etc/passwd"},
		{"space in ID", "tc 007"},
		{"backslash", `tc-foo\bar`},
		{"single quote", "tc-foo'bar"},
		{"double quote", `tc-foo"bar`},
		{"ampersand", "tc-foo&bar"},
		{"angle bracket", "tc-foo>bar"},
		{"percent encoding", "tc-007%0anewline"},
		{"newline", "tc-007\nnewline"},
		{"tab", "tc-007\ttab"},
		{"starts with dot", ".hidden"},
		{"starts with dash", "-invalid"},
		{"starts with slash", "/absolute"},
		{"starts with underscore", "_leading"},
		{"double dot traversal", "foo/../bar"},
		{"exceeds max length", strings.Repeat("a", 129)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTargetID(tt.input)
			assert.Error(t, err, "expected %q to be invalid", tt.input)
		})
	}
}

func TestValidateTargetID_SubfolderScopedTargets(t *testing.T) {
	// ENH-036: subfolder-scoped targets must be accepted
	valid := []string{
		"cwd-scoping/tc-abc123",
		"feature/tc-001",
		"deep/nested/tc-007",
	}

	for _, id := range valid {
		t.Run(id, func(t *testing.T) {
			err := validateTargetID(id)
			assert.NoError(t, err, "subfolder-scoped target %q should be valid", id)
		})
	}
}

func TestIsTestCaseID_ValidFormats(t *testing.T) {
	valid := []string{
		"tc-007",
		"tc-a1b2c3d",
		"tc-a1b2c3d-login-happy",
		"my-test-01",
		"cwd-scoping/tc-abc123",
		"AB-123",
	}

	for _, id := range valid {
		t.Run(id, func(t *testing.T) {
			assert.True(t, isTestCaseID(id), "expected %q to be a valid test case ID", id)
		})
	}
}

func TestIsTestCaseID_InvalidFormats(t *testing.T) {
	// These should fail because they lack a dash with non-empty parts
	noDash := []string{
		"justletters",
		"123",
	}

	for _, id := range noDash {
		t.Run(id, func(t *testing.T) {
			assert.False(t, isTestCaseID(id), "expected %q to be an invalid test case ID", id)
		})
	}

	// Verify that IDs with dashes and non-empty parts still pass
	// (isTestCaseID only checks format, not safety -- safety is validateTargetID's job)
	assert.True(t, isTestCaseID("no-dash-needed-but-this-passes"), "ID with dash and non-empty parts should pass format check")
}

// --- validateFolderArg tests (ENH-049) ---

func TestValidateFolderArg_ValidInputs(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"bug-022", "bug-022"},
		{"payments/checkout", "payments/checkout"},
		{"sprint-14", "sprint-14"},
		{"a", "a"},
		{"deep/nested/folder", "deep/nested/folder"},
		{"bug-022/", "bug-022"},             // trailing slash trimmed
		{"payments/checkout/", "payments/checkout"}, // trailing slash trimmed
		{"A123", "A123"},
		{"my_test", "my_test"},
		{"v2.1", "v2.1"},               // dots allowed in non-leading positions
		{"sprint-2.0", "sprint-2.0"},   // dots allowed in non-leading positions
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, err := validateFolderArg(tt.input)
			assert.NoError(t, err, "expected %q to be valid", tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestValidateFolderArg_InvalidInputs(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"dot", "."},
		{"dotdot", ".."},
		{"test-cases prefix", "test-cases/foo"},
		{"test-cases alone", "test-cases"},
		{"empty after trim", "/"},
		{"absolute path", "/absolute/path"},
		{"starts with slash", "/foo"},
		{"starts with dash", "-invalid"},
		{"starts with underscore", "_leading"},
		{"shell metachar", "foo;bar"},
		{"space", "foo bar"},
		{"double dot traversal", "foo/../bar"},
		{"exceeds max length", strings.Repeat("a", 129)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateFolderArg(tt.input)
			assert.Error(t, err, "expected %q to be invalid", tt.input)
		})
	}
}

func TestValidateFolderArg_TestCasesPrefixHelpfulError(t *testing.T) {
	_, err := validateFolderArg("test-cases/foo")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "don't include the test-cases/ prefix")
	assert.Contains(t, err.Error(), "gtms create foo")
}

func TestValidateFolderArg_DotHelpfulError(t *testing.T) {
	_, err := validateFolderArg(".")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not a valid folder name")
	assert.Contains(t, err.Error(), "gtms create bug-022")
}

func TestSanitizeForError_TruncatesLongStrings(t *testing.T) {
	long := strings.Repeat("x", 100)
	result := sanitizeForError(long)
	assert.Equal(t, 43, len(result)) // 40 chars + "..."
	assert.True(t, strings.HasSuffix(result, "..."))
}

func TestSanitizeForError_ReplacesControlChars(t *testing.T) {
	input := "tc-\x00\x1f\x7f-test"
	result := sanitizeForError(input)
	assert.NotContains(t, result, "\x00")
	assert.NotContains(t, result, "\x1f")
	assert.NotContains(t, result, "\x7f")
	assert.Contains(t, result, "?")
}
