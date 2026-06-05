package pathsafe

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- ValidateFilenameComponent tests (BUG-058) ---

func TestValidateFilenameComponent_ValidValues(t *testing.T) {
	valid := []struct {
		value string
		label string
	}{
		{"tc-abc12345", "test case ID"},
		{"playwright", "framework"},
		{"task-a1b2c3d4", "task ID"},
		{"my-framework-2", "framework"},
		{"bats", "framework"},
		{"manual", "framework"},
		{"cypress-e2e", "framework"},
		{"TC-UPPER123", "test case ID"},
		{"a", "single char"},
	}

	for _, tt := range valid {
		t.Run(tt.value, func(t *testing.T) {
			err := ValidateFilenameComponent(tt.value, tt.label)
			assert.NoError(t, err, "expected %q to be valid", tt.value)
		})
	}
}

func TestValidateFilenameComponent_RejectsEmpty(t *testing.T) {
	err := ValidateFilenameComponent("", "test case ID")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not be empty")
	assert.Contains(t, err.Error(), "test case ID")
}

func TestValidateFilenameComponent_RejectsDot(t *testing.T) {
	err := ValidateFilenameComponent(".", "framework")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not be '.' or '..'")
}

func TestValidateFilenameComponent_RejectsDotDot(t *testing.T) {
	err := ValidateFilenameComponent("..", "test case ID")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not be '.' or '..'")
}

func TestValidateFilenameComponent_RejectsForwardSlash(t *testing.T) {
	cases := []string{
		"x/y",
		"../escape",
		"tc-abc/../../etc",
		"/absolute",
		"sub/dir/file",
	}

	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			err := ValidateFilenameComponent(c, "test case ID")
			require.Error(t, err, "expected %q to be rejected", c)
			assert.Contains(t, err.Error(), "path separator")
		})
	}
}

func TestValidateFilenameComponent_RejectsBackslash(t *testing.T) {
	cases := []string{
		`x\y`,
		`..\escape`,
		`C:\windows`,
	}

	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			err := ValidateFilenameComponent(c, "framework")
			require.Error(t, err, "expected %q to be rejected", c)
			assert.Contains(t, err.Error(), "path separator")
		})
	}
}

func TestValidateFilenameComponent_RejectsPathTraversal(t *testing.T) {
	// These contain ".." but no "/" -- caught by the traversal check
	cases := []string{
		"tc-..abc",
		"abc..def",
	}

	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			err := ValidateFilenameComponent(c, "test case ID")
			require.Error(t, err, "expected %q to be rejected", c)
			assert.Contains(t, err.Error(), "path traversal sequence")
		})
	}
}

func TestValidateFilenameComponent_RejectsControlCharacters(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"null byte", "tc-abc\x00"},
		{"tab", "tc-abc\t"},
		{"newline", "tc-abc\n"},
		{"carriage return", "tc-abc\r"},
		{"DEL", "tc-abc\x7F"},
		{"bell", "tc-abc\x07"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateFilenameComponent(tt.value, "task ID")
			require.Error(t, err, "expected control char to be rejected")
			assert.Contains(t, err.Error(), "control character")
		})
	}
}

func TestValidateFilenameComponent_LabelInErrorMessage(t *testing.T) {
	err := ValidateFilenameComponent("x/y", "framework name")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "framework name")

	err = ValidateFilenameComponent("", "task ID")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "task ID")
}

// --- ResolveUnderRoot tests (BUG-057) ---

func TestResolveUnderRoot_RelativeInside(t *testing.T) {
	root := t.TempDir()

	// Create a file inside the project root.
	sub := filepath.Join(root, "test", "acceptance")
	require.NoError(t, os.MkdirAll(sub, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "tc-abc.bats"), []byte("x"), 0644))

	absPath, relPath, err := ResolveUnderRoot(root, "test/acceptance/tc-abc.bats")

	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(absPath), "absPath should be absolute")
	assert.Equal(t, "test/acceptance/tc-abc.bats", relPath)
}

func TestResolveUnderRoot_AbsoluteInside(t *testing.T) {
	root := t.TempDir()

	// Create a file inside the project root.
	sub := filepath.Join(root, "scripts")
	require.NoError(t, os.MkdirAll(sub, 0755))
	target := filepath.Join(sub, "run.sh")
	require.NoError(t, os.WriteFile(target, []byte("x"), 0644))

	absPath, relPath, err := ResolveUnderRoot(root, target)

	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(absPath), "absPath should be absolute")
	assert.Equal(t, "scripts/run.sh", relPath, "should be normalised to relative slash form")
}

func TestResolveUnderRoot_AbsoluteOutside(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir() // guaranteed different from root

	outsidePath := filepath.Join(outside, "evil.txt")
	require.NoError(t, os.WriteFile(outsidePath, []byte("bad"), 0644))

	_, _, err := ResolveUnderRoot(root, outsidePath)

	require.Error(t, err)
	var pse *PathSafetyError
	require.True(t, errors.As(err, &pse), "should be *PathSafetyError")
	assert.Equal(t, outsidePath, pse.Path)
}

func TestResolveUnderRoot_RelativeTraversalOutside(t *testing.T) {
	root := t.TempDir()

	// ../../etc/hosts traverses out of the project root.
	_, _, err := ResolveUnderRoot(root, "../../etc/hosts")

	require.Error(t, err)
	var pse *PathSafetyError
	require.True(t, errors.As(err, &pse), "should be *PathSafetyError")
	assert.Equal(t, "../../etc/hosts", pse.Path)
}

func TestResolveUnderRoot_EmptyInput(t *testing.T) {
	root := t.TempDir()

	_, _, err := ResolveUnderRoot(root, "")

	require.Error(t, err)
	assert.True(t, IsPathSafetyError(err), "empty input should produce PathSafetyError")
}

func TestResolveUnderRoot_SymlinkInsideTargetingOutside(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on Windows")
	}

	root := t.TempDir()
	outside := t.TempDir()

	// Create a real file outside the root.
	outsideFile := filepath.Join(outside, "secret.txt")
	require.NoError(t, os.WriteFile(outsideFile, []byte("secret"), 0644))

	// Create a symlink inside the root pointing outside.
	linkPath := filepath.Join(root, "evil-link.txt")
	require.NoError(t, os.Symlink(outsideFile, linkPath))

	_, _, err := ResolveUnderRoot(root, "evil-link.txt")

	require.Error(t, err, "symlink targeting outside root must be rejected")
	var pse *PathSafetyError
	require.True(t, errors.As(err, &pse), "should be *PathSafetyError")
}

func TestResolveUnderRoot_NonExistentPathInside(t *testing.T) {
	root := t.TempDir()

	// Path does not exist, but resolves inside root.
	absPath, relPath, err := ResolveUnderRoot(root, "does/not/exist.txt")

	require.NoError(t, err, "non-existent path inside root should not error")
	assert.True(t, filepath.IsAbs(absPath))
	assert.Equal(t, "does/not/exist.txt", relPath)
}

func TestResolveUnderRoot_ProjectRootItself(t *testing.T) {
	root := t.TempDir()

	// Absolute path equal to root itself.
	absPath, relPath, err := ResolveUnderRoot(root, root)

	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(absPath))
	assert.Equal(t, ".", relPath)
}

// --- IsWithinRoot tests (BUG-057) ---

func TestIsWithinRoot_ExactMatch(t *testing.T) {
	assert.True(t, IsWithinRoot("/project", "/project"))
}

func TestIsWithinRoot_Inside(t *testing.T) {
	sep := string(filepath.Separator)
	assert.True(t, IsWithinRoot("/project"+sep+"sub"+sep+"file.go", "/project"))
}

func TestIsWithinRoot_Outside(t *testing.T) {
	assert.False(t, IsWithinRoot("/other/dir", "/project"))
}

func TestIsWithinRoot_PrefixTrap(t *testing.T) {
	// "/project-evil" should NOT match "/project".
	assert.False(t, IsWithinRoot("/project-evil/file.go", "/project"))
}

// --- IsPathSafetyError tests (BUG-057) ---

func TestIsPathSafetyError_True(t *testing.T) {
	err := &PathSafetyError{Path: "bad", Cause: fmt.Errorf("test")}
	assert.True(t, IsPathSafetyError(err))
}

func TestIsPathSafetyError_False(t *testing.T) {
	err := fmt.Errorf("not a path safety error")
	assert.False(t, IsPathSafetyError(err))
}

func TestIsPathSafetyError_Wrapped(t *testing.T) {
	inner := &PathSafetyError{Path: "bad", Cause: fmt.Errorf("inner")}
	wrapped := fmt.Errorf("outer: %w", inner)
	assert.True(t, IsPathSafetyError(wrapped))
}

func TestIsPathSafetyError_Nil(t *testing.T) {
	assert.False(t, IsPathSafetyError(nil))
}
