package adapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aitestmanagement/gtms-cli/internal/scaffold"
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

// --- ENH-162: Template-driven GenerateSkeleton with templateBody parameter ---

func TestBATSSupport_GenerateSkeleton_Content(t *testing.T) {
	root := t.TempDir()
	outDir := filepath.Join(root, "test", "acceptance", "my-feature")
	require.NoError(t, os.MkdirAll(outDir, 0755))

	outPath := filepath.Join(outDir, "tc-abcd1234-my-test.bats")
	support := &BATSSupport{}

	// ENH-162: pass the fallback template body as the 4th arg
	err := support.GenerateSkeleton("tc-abcd1234", root, outPath, scaffold.BATSAutomateTemplate)
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

	// No unsubstituted placeholders remain
	assert.NotContains(t, content, "${TESTCASE_ID}")
	assert.NotContains(t, content, "${PROJECT_ROOT_DEPTH}")

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

	err := support.GenerateSkeleton("tc-abcd1234", root, outPath, scaffold.BATSAutomateTemplate)
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

	err := support.GenerateSkeleton("tc-cccccccc", root, outPath, scaffold.BATSAutomateTemplate)
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

	err := support.GenerateSkeleton("tc-eeee0000", root, outPath, scaffold.BATSAutomateTemplate)
	require.NoError(t, err)

	data, err := os.ReadFile(outPath)
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, `"$(dirname "$BATS_TEST_FILENAME")/../.."`)
}

// --- ENH-162: TemplatePath and FallbackContent ---

func TestBATSSupport_TemplatePath(t *testing.T) {
	support := &BATSSupport{}
	path := support.TemplatePath("/project")
	assert.Contains(t, path, "automation")
	assert.Contains(t, path, "templates")
	assert.True(t, strings.HasSuffix(path, "bats.template.bats"))
}

func TestBATSSupport_FallbackContent(t *testing.T) {
	support := &BATSSupport{}
	content := support.FallbackContent()
	assert.Contains(t, content, "${TESTCASE_ID}")
	assert.Contains(t, content, "${PROJECT_ROOT_DEPTH}")
	assert.Contains(t, content, "#!/usr/bin/env bats")
}

func TestPlaywrightSupport_TemplatePath(t *testing.T) {
	support := &PlaywrightSupport{}
	path := support.TemplatePath("/project")
	assert.Contains(t, path, "automation")
	assert.Contains(t, path, "templates")
	assert.True(t, strings.HasSuffix(path, "playwright.template.spec.ts"))
}

func TestPlaywrightSupport_FallbackContent(t *testing.T) {
	support := &PlaywrightSupport{}
	content := support.FallbackContent()
	assert.Contains(t, content, "${TESTCASE_ID}")
	assert.Contains(t, content, "import { test, expect }")
}

// --- ENH-162: Playwright GenerateSkeleton with template body ---

func TestPlaywrightSupport_GenerateSkeleton_SubstitutesTestcaseID(t *testing.T) {
	root := t.TempDir()
	outDir := filepath.Join(root, "gtms", "scripts", "playwright", "my-feature")
	require.NoError(t, os.MkdirAll(outDir, 0755))

	outPath := filepath.Join(outDir, "tc-ff001122-pw-test.spec.ts")
	support := &PlaywrightSupport{}

	err := support.GenerateSkeleton("tc-ff001122", root, outPath, scaffold.PlaywrightAutomateTemplate)
	require.NoError(t, err)

	data, err := os.ReadFile(outPath)
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "tc-ff001122")
	assert.NotContains(t, content, "${TESTCASE_ID}")
	assert.Contains(t, content, "import { test, expect }")
	assert.Contains(t, content, "test.skip(true, 'skeleton -- not yet implemented')")
}

// --- ENH-162: Custom template body substitution ---

func TestBATSSupport_GenerateSkeleton_CustomTemplate(t *testing.T) {
	root := t.TempDir()
	outDir := filepath.Join(root, "test", "acceptance", "custom")
	require.NoError(t, os.MkdirAll(outDir, 0755))

	outPath := filepath.Join(outDir, "tc-aabb0011-custom.bats")
	support := &BATSSupport{}

	customTemplate := `#!/usr/bin/env bats
# Custom template for ${TESTCASE_ID}
setup_file() {
    export PROJECT_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/${PROJECT_ROOT_DEPTH}" && pwd)"
    load "$PROJECT_ROOT/test/test_helper/common-setup.bash"
    load "$PROJECT_ROOT/test/test_helper/team-helpers.bash"
}
`

	err := support.GenerateSkeleton("tc-aabb0011", root, outPath, customTemplate)
	require.NoError(t, err)

	data, err := os.ReadFile(outPath)
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "tc-aabb0011")
	assert.Contains(t, content, "team-helpers.bash")
	assert.NotContains(t, content, "${TESTCASE_ID}")
	assert.NotContains(t, content, "${PROJECT_ROOT_DEPTH}")
	// test/acceptance/custom -> 3 segments -> ../../..
	assert.Contains(t, content, "../../..")
}

func TestPlaywrightSupport_GenerateSkeleton_CustomTemplate(t *testing.T) {
	root := t.TempDir()
	outDir := filepath.Join(root, "gtms", "scripts", "playwright")
	require.NoError(t, os.MkdirAll(outDir, 0755))

	outPath := filepath.Join(outDir, "tc-cc001122-pw.spec.ts")
	support := &PlaywrightSupport{}

	customTemplate := `import { test, expect } from '@playwright/test';
import { teamExpect } from './team-helpers';
test('${TESTCASE_ID}: custom', async ({ page }) => {});
`

	err := support.GenerateSkeleton("tc-cc001122", root, outPath, customTemplate)
	require.NoError(t, err)

	data, err := os.ReadFile(outPath)
	require.NoError(t, err)
	content := string(data)

	assert.Contains(t, content, "tc-cc001122")
	assert.Contains(t, content, "team-helpers")
	assert.NotContains(t, content, "${TESTCASE_ID}")
}

// --- ENH-162 AC #12: Removing a placeholder does not cause error ---

func TestBATSSupport_GenerateSkeleton_MissingPlaceholderDoesNotError(t *testing.T) {
	root := t.TempDir()
	outDir := filepath.Join(root, "test", "acceptance", "noplaceholder")
	require.NoError(t, os.MkdirAll(outDir, 0755))

	outPath := filepath.Join(outDir, "tc-dddd0000-noplaceholder.bats")
	support := &BATSSupport{}

	// Template without ${PROJECT_ROOT_DEPTH} -- user removed it
	templateWithout := `#!/usr/bin/env bats
setup_file() {
    export PROJECT_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/../../.." && pwd)"
}
@test "${TESTCASE_ID}: test" {
    skip "skeleton"
}
`

	err := support.GenerateSkeleton("tc-dddd0000", root, outPath, templateWithout)
	require.NoError(t, err)

	data, err := os.ReadFile(outPath)
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "tc-dddd0000")
	// The literal ../../.. is preserved since no placeholder to substitute
	assert.Contains(t, content, "../../..")
}

// --- ENH-162 AC #9 shared-const parity: fallback const = scaffolded template ---

func TestBATSSupport_FallbackContent_MatchesFallbackConst(t *testing.T) {
	support := &BATSSupport{}
	assert.Equal(t, scaffold.BATSAutomateTemplate, support.FallbackContent(),
		"BATSSupport.FallbackContent() must return the scaffold.BATSAutomateTemplate const")
}

func TestPlaywrightSupport_FallbackContent_MatchesFallbackConst(t *testing.T) {
	support := &PlaywrightSupport{}
	assert.Equal(t, scaffold.PlaywrightAutomateTemplate, support.FallbackContent(),
		"PlaywrightSupport.FallbackContent() must return the scaffold.PlaywrightAutomateTemplate const")
}
