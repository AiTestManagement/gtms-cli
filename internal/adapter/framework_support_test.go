package adapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- BUG-108: FrameworkSupport interface and BATS implementation tests ---

func TestLookupFrameworkSupport_BatsFound(t *testing.T) {
	support := LookupFrameworkSupport("bats")
	require.NotNil(t, support, "bats framework support should be registered")
}

func TestLookupFrameworkSupport_UnknownReturnsNil(t *testing.T) {
	support := LookupFrameworkSupport("framework-without-automate-support")
	assert.Nil(t, support, "unknown framework should return nil")
}

func TestLookupFrameworkSupport_ManualReturnsNil(t *testing.T) {
	// manual is not a skeleton-generating framework
	support := LookupFrameworkSupport("manual")
	assert.Nil(t, support, "manual framework should not have automate support")
}

func TestBATSSupport_Extension(t *testing.T) {
	support := &BATSSupport{}
	assert.Equal(t, ".bats", support.Extension())
}

func TestBATSSupport_OutputDir_EmptySubdir(t *testing.T) {
	support := &BATSSupport{}
	assert.Equal(t, "test/acceptance", support.OutputDir(""))
}

func TestBATSSupport_OutputDir_SingleSegment(t *testing.T) {
	support := &BATSSupport{}
	assert.Equal(t, "test/acceptance/my-feature", support.OutputDir("my-feature/"))
}

func TestBATSSupport_OutputDir_NestedSegments(t *testing.T) {
	support := &BATSSupport{}
	assert.Equal(t, "test/acceptance/bug-108/nested-path", support.OutputDir("bug-108/nested-path/"))
}

func TestBATSSupport_OutputDir_NoTrailingSlash(t *testing.T) {
	support := &BATSSupport{}
	assert.Equal(t, "test/acceptance/my-feature", support.OutputDir("my-feature"))
}

func TestBATSSupport_GenerateSkeleton_Content(t *testing.T) {
	root := t.TempDir()
	outDir := filepath.Join(root, "test", "acceptance", "my-feature")
	require.NoError(t, os.MkdirAll(outDir, 0755))

	outPath := filepath.Join(outDir, "tc-abcd1234-my-test.bats")
	support := &BATSSupport{}

	err := support.GenerateSkeleton("tc-abcd1234", root, outPath)
	require.NoError(t, err)

	data, err := os.ReadFile(outPath)
	require.NoError(t, err)
	content := string(data)

	// Verify BATS-specific content is present
	assert.Contains(t, content, "#!/usr/bin/env bats")
	assert.Contains(t, content, "setup_file()")
	assert.Contains(t, content, "_common_setup")
	assert.Contains(t, content, "@test")
	assert.Contains(t, content, "tc-abcd1234")
	assert.Contains(t, content, "teardown()")
	assert.Contains(t, content, "common-setup.bash")

	// BUG-108 AC 9: must NOT contain obsolete bare common_setup
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "common_setup" {
			t.Error("skeleton contains obsolete bare 'common_setup' call; should be '_common_setup'")
		}
	}
}

func TestBATSSupport_GenerateSkeleton_DepthSingleSegment(t *testing.T) {
	// test/acceptance/my-feature/tc-xxx.bats -> 3 dir segments -> "../../.."
	root := t.TempDir()
	outDir := filepath.Join(root, "test", "acceptance", "my-feature")
	require.NoError(t, os.MkdirAll(outDir, 0755))

	outPath := filepath.Join(outDir, "tc-abcd1234-test.bats")
	support := &BATSSupport{}

	err := support.GenerateSkeleton("tc-abcd1234", root, outPath)
	require.NoError(t, err)

	data, err := os.ReadFile(outPath)
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, `"$(dirname "$BATS_TEST_FILENAME")/../../.."`)
}

func TestBATSSupport_GenerateSkeleton_DepthNestedPath(t *testing.T) {
	// test/acceptance/bug-108/nested-path/tc-xxx.bats -> 4 dir segments -> "../../../.."
	root := t.TempDir()
	outDir := filepath.Join(root, "test", "acceptance", "bug-108", "nested-path")
	require.NoError(t, os.MkdirAll(outDir, 0755))

	outPath := filepath.Join(outDir, "tc-cccccccc-nested-helper-path.bats")
	support := &BATSSupport{}

	err := support.GenerateSkeleton("tc-cccccccc", root, outPath)
	require.NoError(t, err)

	data, err := os.ReadFile(outPath)
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, `"$(dirname "$BATS_TEST_FILENAME")/../../../.."`)
}

func TestBATSSupport_GenerateSkeleton_DepthRootLevel(t *testing.T) {
	// test/acceptance/tc-xxx.bats -> 2 dir segments -> "../.."
	root := t.TempDir()
	outDir := filepath.Join(root, "test", "acceptance")
	require.NoError(t, os.MkdirAll(outDir, 0755))

	outPath := filepath.Join(outDir, "tc-eeee0000-root-test.bats")
	support := &BATSSupport{}

	err := support.GenerateSkeleton("tc-eeee0000", root, outPath)
	require.NoError(t, err)

	data, err := os.ReadFile(outPath)
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, `"$(dirname "$BATS_TEST_FILENAME")/../.."`)
}
