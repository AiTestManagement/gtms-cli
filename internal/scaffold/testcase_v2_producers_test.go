package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var canonicalTestcaseKeyOrder = []string{
	"gtms_schema",
	"type",
	"test_type",
	"test_case_id",
	"title",
	"requirement",
	"priority",
	"created",
}

func TestScaffold_TestcaseProducersEmitCanonicalV2(t *testing.T) {
	producers := map[string]string{
		"role template":          TestcaseTemplateMD,
		"agent script fallback":  agentCreateScript,
		"manual script fallback": manualCreateScript,
		"demo creator":           demoCreateScript,
	}
	for name, content := range producers {
		t.Run(name, func(t *testing.T) {
			assertCanonicalV2Blocks(t, content)
		})
	}
}

func TestScaffold_FreshInitWritesV2RoleAssetsAcrossPresets(t *testing.T) {
	skipIfShort(t)
	for _, preset := range ValidPresets() {
		t.Run(preset, func(t *testing.T) {
			root := initGitRepo(t)
			_, err := Init(Options{ProjectRoot: root, Name: "v2", Repo: "org/v2", Preset: preset})
			require.NoError(t, err)

			for _, rel := range []string{
				"gtms/test/templates/manual-testcase.template.md",
				"gtms/test/templates/agent-testcase.template.md",
				"gtms/adapters/manual-create-script.sh",
				"gtms/adapters/agent-create-script.sh",
			} {
				body, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
				require.NoError(t, readErr)
				assertCanonicalV2Blocks(t, string(body))
			}
		})
	}
}

func TestScaffold_TrackedRoleAndGuideMirrorsMatch(t *testing.T) {
	for _, rel := range []string{
		"../../gtms/test/templates/manual-testcase.template.md",
		"../../gtms/test/templates/agent-testcase.template.md",
	} {
		body, err := os.ReadFile(rel)
		require.NoError(t, err)
		assert.Equal(t, normalizeAsset(TestcaseTemplateMD), normalizeAsset(string(body)))
	}

	guide, err := os.ReadFile("../../gtms/test/guides/gtms-test-case-authoring-guide.md")
	require.NoError(t, err)
	assert.Equal(t, normalizeAsset(starterGuideContent), normalizeAsset(string(guide)))
}

func TestPromptMirror_SchemaAndFieldLinesMatchEmbedded(t *testing.T) {
	mirror, err := os.ReadFile("../../gtms/test/prompts/create-standard.md")
	require.NoError(t, err)
	assert.Equal(t, promptSchemaLines(promptCreateStandard), promptSchemaLines(string(mirror)))
}

func assertCanonicalV2Blocks(t *testing.T, content string) {
	t.Helper()
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	blocks := 0
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "---" {
			continue
		}
		end := i + 1
		for end < len(lines) && strings.TrimSpace(lines[end]) != "---" {
			end++
		}
		if end == len(lines) {
			continue
		}
		block := lines[i+1 : end]
		hasIdentity := false
		for _, line := range block {
			if strings.HasPrefix(strings.TrimSpace(line), "test_case_id:") {
				hasIdentity = true
				break
			}
		}
		if !hasIdentity {
			i = end
			continue
		}
		blocks++
		require.GreaterOrEqual(t, len(block), len(canonicalTestcaseKeyOrder))
		for keyIndex, key := range canonicalTestcaseKeyOrder {
			line := strings.TrimSpace(block[keyIndex])
			assert.True(t, strings.HasPrefix(line, key+":"), "key %d = %q, want %s", keyIndex, line, key)
		}
		assert.Equal(t, `gtms_schema: "test-case/v2"`, strings.TrimSpace(block[0]))
		assert.Equal(t, "type: Test Case Spec", strings.TrimSpace(block[1]))
		assert.Equal(t, "test_type: Functional", strings.TrimSpace(block[2]))
		i = end
	}
	require.Positive(t, blocks, "producer contained no testcase frontmatter")
}

func normalizeAsset(content string) string {
	return strings.TrimRight(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
}

func promptSchemaLines(content string) []string {
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		if strings.Contains(line, "Required frontmatter fields") || strings.Contains(line, "gtms_schema") {
			lines = append(lines, line)
		}
	}
	return lines
}
