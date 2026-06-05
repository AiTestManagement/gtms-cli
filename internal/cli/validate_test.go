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
		"cwd-scoping/tc-abc123",
	}

	for _, id := range valid {
		t.Run(id, func(t *testing.T) {
			assert.True(t, isTestCaseID(id), "expected %q to be a valid test case ID", id)
		})
	}
}

func TestIsTestCaseID_InvalidFormats(t *testing.T) {
	invalid := []string{
		"justletters",
		"123",
		"folder-a",
		"bug-022",
		"my-test-01",
		"AB-123",
		"no-dash-needed-but-this-passes",
		"tc-",  // too short -- needs content after "tc-"
		"tc",   // missing dash
	}

	for _, id := range invalid {
		t.Run(id, func(t *testing.T) {
			assert.False(t, isTestCaseID(id), "expected %q to be an invalid test case ID", id)
		})
	}
}

// --- normaliseTarget tests (BUG-054) ---

func TestNormaliseTarget(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// TC ID with .md suffix — should strip
		{"tc-abc12345.md", "tc-abc12345"},
		{"tc-007.md", "tc-007"},
		{"tc-a1b2c3d4-login-happy.md", "tc-a1b2c3d4-login-happy"},

		// TC ID without .md — unchanged
		{"tc-abc12345", "tc-abc12345"},
		{"tc-007", "tc-007"},

		// Non-TC arguments — unchanged (even with .md)
		{"my-folder", "my-folder"},
		{"my-folder.md", "my-folder.md"},
		{"bug-022.md", "bug-022.md"},

		// Subfolder-scoped TC with .md — should strip
		{"cwd-scoping/tc-abc123.md", "cwd-scoping/tc-abc123"},
		{"deep/nested/tc-abc123.md", "deep/nested/tc-abc123"},

		// Subfolder-scoped TC without .md — unchanged
		{"cwd-scoping/tc-abc123", "cwd-scoping/tc-abc123"},

		// Degenerate: tc- followed by only .md (no real ID content) — leave alone
		{"tc-.md", "tc-.md"},

		// Not lowercased tc- prefix — function checks for "tc-" literally
		{"TC-ABC12345.MD", "TC-ABC12345.MD"},

		// Empty string — unchanged
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, normaliseTarget(tt.input))
		})
	}
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
		// BUG-035: backslash separators (PowerShell) normalised to forward slashes
		{"parent\\child", "parent/child"},
		{"parent\\child\\grandchild", "parent/child/grandchild"},
		{"parent\\child/grandchild", "parent/child/grandchild"},
		{"bug-022\\", "bug-022"}, // trailing backslash trimmed
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
		{"full cases prefix", "gtms/cases/foo"},
		{"full cases alone", "gtms/cases"},
		{"short cases prefix", "cases/foo"},
		{"short cases alone", "cases"},
		{"empty after trim", "/"},
		{"absolute path", "/absolute/path"},
		{"starts with slash", "/foo"},
		{"starts with dash", "-invalid"},
		{"shell metachar", "foo;bar"},
		{"space", "foo bar"},
		{"double dot traversal", "foo/../bar"},
		{"exceeds max length", strings.Repeat("a", 129)},
		// BUG-035: backslash normalisation must not bypass existing guards
		{"backslash traversal", "..\\parent"},
		{"embedded backslash traversal", "foo\\..\\bar"},
		{"backslash exceeds max length", strings.Repeat("a\\", 65)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateFolderArg(tt.input)
			assert.Error(t, err, "expected %q to be invalid", tt.input)
		})
	}
}

func TestValidateFolderArg_TestCasesPrefixHelpfulError(t *testing.T) {
	_, err := validateFolderArg("gtms/cases/foo")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "don't include the gtms/cases/ prefix")
	assert.Contains(t, err.Error(), "gtms create foo")
}

func TestValidateFolderArg_DotHelpfulError(t *testing.T) {
	_, err := validateFolderArg(".")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not a valid folder name")
	assert.Contains(t, err.Error(), "gtms create bug-022")
}

// BUG-035: backslash path-traversal must be rejected with the traversal error,
// not the generic "invalid characters" error, after normalisation.
func TestValidateFolderArg_BackslashTraversalError(t *testing.T) {
	_, err := validateFolderArg("..\\parent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "path traversal")
}

// --- looksLikeFilePath tests (ENH-024) ---

func TestLooksLikeFilePath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"forward slash path", "docs/file.md", true},
		{"backslash path", "path\\to\\file.txt", true},
		{"md extension", "file.md", true},
		{"txt extension", "file.txt", true},
		{"yaml extension", "config.yaml", true},
		{"yml extension", "config.yml", true},
		{"json extension", "data.json", true},
		{"xml extension", "data.xml", true},
		{"go extension", "main.go", true},
		{"py extension", "script.py", true},
		{"js extension", "app.js", true},
		{"ts extension", "app.ts", true},
		{"csv extension", "data.csv", true},
		{"html extension", "page.html", true},
		{"uppercase extension", "FILE.MD", true},
		{"url with slash", "https://example.com/page", true}, // known acceptable false positive — URLs contain slashes
		{"JIRA ticket", "JIRA-123", false},
		{"requirement ID", "REQ-001", false},
		{"simple identifier", "some-requirement", false},
		{"empty string", "", false},
		{"unknown extension", "file.unknown", false},
		{"no extension no slash", "just-a-name", false},
		{"dot but no known ext", "file.xyz", false},
		{"number only", "12345", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := looksLikeFilePath(tt.input)
			assert.Equal(t, tt.expected, result, "looksLikeFilePath(%q)", tt.input)
		})
	}
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

// --- Name argument validation tests (REV-061) ---

func TestValidNamePattern(t *testing.T) {
	valid := []string{
		"user-can-login",
		"login_happy",
		"A123",
		"simple",
		"with-dashes-and_underscores",
	}
	for _, name := range valid {
		assert.True(t, validNamePattern.MatchString(name), "expected %q to be valid", name)
	}
}

func TestValidNamePattern_RejectsInvalid(t *testing.T) {
	invalid := []string{
		"has space",
		"semi;colon",
		"back`tick",
		"pipe|char",
		"dollar$var",
		"$(whoami)",
		"path/slash",
		"",
	}
	for _, name := range invalid {
		assert.False(t, validNamePattern.MatchString(name), "expected %q to be invalid", name)
	}
}
