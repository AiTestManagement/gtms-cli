package adapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
)

func strictOwnedSpec(id, title, requirement, priority, testType, newline string, bom bool) []byte {
	doc := "---\n" +
		"gtms_schema: \"test-case/v2\"\n" +
		"type: Test Case Spec\n" +
		"test_type: " + testType + "\n" +
		"test_case_id: " + id + "\n" +
		"title: " + title + "\n" +
		"requirement: " + requirement + "\n" +
		"priority: " + priority + "\n" +
		"created: 2026-09-26\n" +
		"---\nbody\n"
	doc = strings.ReplaceAll(doc, "\n", newline)
	if bom {
		doc = "\xef\xbb\xbf" + doc
	}
	return []byte(doc)
}

func TestValidateTestCaseOwner_RefusesSameIDSchemaMutation(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "gtms", "test", "cases", "feat", "tc-aaaa1111-login.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.WriteFile(abs, strictOwnedSpec("tc-aaaa1111", "Before", "ENH-219", "Medium", "Functional", "\n", false), 0o644))
	owner, err := testcase.ResolveOwner(root, "tc-aaaa1111")
	require.NoError(t, err)

	mutated := strings.Replace(string(strictOwnedSpec("tc-aaaa1111", "Before", "ENH-219", "Medium", "Functional", "\n", false)), "test_type: Functional\n", "", 1)
	require.NoError(t, os.WriteFile(abs, []byte(mutated), 0o644))
	_, violations := ValidateTestCaseOwnerFresh(root, owner)
	require.Len(t, violations, 1)
	require.Contains(t, violations[0].Reason, "schema changed")
}

func TestValidateTestCaseOwner_RefusesTestTypeMutation(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "gtms", "test", "cases", "feat", "tc-aaaa1111-login.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.WriteFile(abs, strictOwnedSpec("tc-aaaa1111", "Before", "ENH-219", "Medium", "Functional", "\n", false), 0o644))
	owner, err := testcase.ResolveOwner(root, "tc-aaaa1111")
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(abs, strictOwnedSpec("tc-aaaa1111", "Before", "ENH-219", "Medium", "Security", "\n", false), 0o644))
	_, violations := ValidateTestCaseOwnerFresh(root, owner)
	require.Len(t, violations, 1)
	require.Contains(t, violations[0].Reason, "test_type changed")
}

func TestValidateTestCaseOwner_FreshMetadataReachesPrime(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "gtms", "test", "cases", "feat", "tc-aaaa1111-login.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.WriteFile(abs, strictOwnedSpec("tc-aaaa1111", "Before", "OLD", "Low", "Exploratory", "\n", false), 0o644))
	owner, err := testcase.ResolveOwner(root, "tc-aaaa1111")
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(abs, strictOwnedSpec("tc-aaaa1111", "Fresh title", "ENH-219", "High", "Exploratory", "\r\n", true), 0o644))
	resolved := &ResolvedAdapter{Command: "prime", Name: "manual-prime", Tier: 1, Mode: "sync", Config: &config.AdapterConfig{Framework: "manual"}}
	fresh, err := requireCarriedOwner(root, resolved, "tc-aaaa1111", CommandFlags{Owner: owner})
	require.NoError(t, err)
	wantHash, err := pipeline.HashFile(abs)
	require.NoError(t, err)
	require.Equal(t, "Fresh title", fresh.Metadata.Title)
	require.Equal(t, "ENH-219", fresh.Metadata.Requirement)
	require.Equal(t, "High", fresh.Metadata.Priority)
	require.Equal(t, "Exploratory", fresh.Metadata.TestType)
	require.Equal(t, wantHash, fresh.Hash)

	cfg := &config.Config{Project: config.ProjectConfig{Repo: "org/repo"}}
	for _, command := range []string{"automate", "prime"} {
		t.Run(command, func(t *testing.T) {
			resolved := &ResolvedAdapter{Command: command, Name: "manual-prime", Tier: 1, Mode: "sync", Config: &config.AdapterConfig{Framework: "manual"}}
			ctx := buildAdapterContext(root, "task-fresh", resolved, "tc-aaaa1111", CommandFlags{Owner: owner, Framework: "manual"}, "branch", cfg, root, filepath.Join(root, "result.yaml"), "", fresh)
			require.Equal(t, fresh, ctx.ValidatedOwner)
			require.Equal(t, fresh.Hash, ctx.TestCaseHash)
			require.Equal(t, "Fresh title", ctx.TCTitle)
			require.Equal(t, "ENH-219", ctx.TCRequirement)
			require.Equal(t, "High", ctx.TCPriority)
			require.Equal(t, "Exploratory", ctx.TestType)
		})
	}
}

func TestBuildAdapterContext_PrimeUsesRoleTemplate(t *testing.T) {
	for _, tc := range []struct {
		adapter string
		marker  string
	}{
		{adapter: "manual-prime", marker: "manual_template_marker"},
		{adapter: "agent-prime", marker: "agent_template_marker"},
	} {
		t.Run(tc.adapter, func(t *testing.T) {
			root := t.TempDir()
			casePath := filepath.Join(root, "gtms", "test", "cases", "feat", "tc-aaaa1111-login.md")
			require.NoError(t, os.MkdirAll(filepath.Dir(casePath), 0o755))
			require.NoError(t, os.WriteFile(casePath, strictOwnedSpec("tc-aaaa1111", "Title", "ENH-219", "High", "Functional", "\n", false), 0o644))
			owner, err := testcase.ResolveOwner(root, "tc-aaaa1111")
			require.NoError(t, err)

			manualDir := filepath.Join(root, "gtms", "manual")
			templateDir := filepath.Join(manualDir, "templates")
			require.NoError(t, os.MkdirAll(templateDir, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(templateDir, "manual-result.template.yaml"), []byte("marker: manual_template_marker\n"), 0o644))
			require.NoError(t, os.WriteFile(filepath.Join(templateDir, "agent-result.template.yaml"), []byte("marker: agent_template_marker\n"), 0o644))

			resolved := &ResolvedAdapter{Command: "prime", Name: tc.adapter, Tier: 0, Mode: "sync", Config: &config.AdapterConfig{Framework: "manual"}}
			fresh, err := requireCarriedOwner(root, resolved, "tc-aaaa1111", CommandFlags{Owner: owner})
			require.NoError(t, err)
			cfg := &config.Config{Project: config.ProjectConfig{Repo: "org/repo"}}
			ctx := buildAdapterContext(root, "task-role-template", resolved, "tc-aaaa1111", CommandFlags{Owner: owner}, "branch", cfg, root, filepath.Join(root, "result.yaml"), "", fresh)

			result, err := BuiltinPrime(ctx)
			require.NoError(t, err)
			require.Equal(t, 0, result.ExitCode)
			stamped, err := os.ReadFile(ctx.OutputFile)
			require.NoError(t, err)
			require.Contains(t, string(stamped), tc.marker)
		})
	}
}

func TestBuildAdapterContext_UsesFreshNormalizedTestType(t *testing.T) {
	ctx := buildPrimeContextWithDocument(t, strictOwnedSpec("tc-aaaa1111", "Title", "ENH-219", "High", "Security", "\n", false))
	require.Equal(t, "Security", ctx.TestType)
}

func TestBuildAdapterContext_ConceptTypeNeverBecomesCategory(t *testing.T) {
	ctx := buildPrimeContextWithDocument(t, strictOwnedSpec("tc-aaaa1111", "Title", "ENH-219", "High", "Security", "\n", false))
	require.NotEqual(t, "Test Case Spec", ctx.TestType)
	require.Equal(t, "Security", ctx.TestType)
}

func buildPrimeContextWithDocument(t *testing.T, doc []byte) *AdapterContext {
	t.Helper()
	root := t.TempDir()
	abs := filepath.Join(root, "gtms", "test", "cases", "feat", "tc-aaaa1111-login.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.WriteFile(abs, doc, 0o644))
	owner, err := testcase.ResolveOwner(root, "tc-aaaa1111")
	require.NoError(t, err)
	resolved := &ResolvedAdapter{Command: "prime", Name: "manual-prime", Tier: 1, Mode: "sync", Config: &config.AdapterConfig{Framework: "manual"}}
	fresh, err := requireCarriedOwner(root, resolved, "tc-aaaa1111", CommandFlags{Owner: owner})
	require.NoError(t, err)
	cfg := &config.Config{Project: config.ProjectConfig{Repo: "org/repo"}}
	return buildAdapterContext(root, "task-fresh", resolved, "tc-aaaa1111", CommandFlags{Owner: owner}, "branch", cfg, root, filepath.Join(root, "result.yaml"), "", fresh)
}
