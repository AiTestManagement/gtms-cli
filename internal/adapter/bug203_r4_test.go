package adapter

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/scaffold"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// REV-209 round 4 (BUG-203):
//   - R4-CLAUDE-001 / R4-CODEX-002: quarantine reporting and shipped-producer recognition
//     use the RESOLVED project root, so a symlinked root (Unix) or a Windows 8.3 short-path
//     root still reports gtms/tasks/error/<task>/artifacts/<file> and still recognises the
//     built-in template as a shipped producer.
//   - R4-CODEX-001 (scoped to BUG-207): a renamed-parent project never gets the
//     gtms init --force recommendation; it gets the limitation plus the manual route, and
//     following that manual route makes gtms create succeed.

const staleTemplateRel = "gtms/test/templates/manual-testcase.template.md"

// staleV03xTemplate is the v0.3.x manual testcase template (pre-v2 header): the Tier 0
// built-in stamps it verbatim, so the created spec fails strict v2 validation.
func staleV03xTemplate(t *testing.T) string {
	t.Helper()
	priors := scaffold.PresetAssetPriorVersions[staleTemplateRel]
	require.Len(t, priors, 1, "the v0.3.x prior of %s must be declared", staleTemplateRel)
	return priors[0]
}

// writeStaleBuiltinProject builds a default-layout project at root whose role testcase
// template is the stale v0.3.x copy.
func writeStaleBuiltinProject(t *testing.T, root string) {
	t.Helper()
	for _, dir := range []string{
		"gtms/tasks/pending", "gtms/tasks/complete", "gtms/tasks/error",
		"gtms/tasks/in-progress", "gtms/tasks/in-review",
		"gtms/test/cases", "gtms/test/templates", "gtms/automation",
		".gtms/results", ".gtms/worktrees", ".gtms/logs",
	} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "gtms", ".gtms-root"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "gtms.config"),
		[]byte("project:\n  name: Test\n  repo: org/test\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, filepath.FromSlash(staleTemplateRel)),
		[]byte(staleV03xTemplate(t)), 0o644))
}

func invokeBuiltinManualCreate(t *testing.T, root, folder string) *InvokeResult {
	t.Helper()
	cfg := &config.Config{Project: config.ProjectConfig{Name: "Test", Repo: "org/test"}}
	resolved := &ResolvedAdapter{Command: "create", Name: "manual-create", Tier: 0, Mode: "sync",
		Config: &config.AdapterConfig{Mode: "sync"}}
	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, folder, CommandFlags{Folder: folder})
	require.NoError(t, err)
	require.NotNil(t, res)
	return res
}

// assertLayoutRelativeQuarantine checks the create failed, reported the quarantine with a
// layout-relative path under the task's error artefacts folder, and attributed the stale
// header to the shipped built-in template with the preset refresh command.
func assertLayoutRelativeQuarantine(t *testing.T, res *InvokeResult) {
	t.Helper()
	assert.Equal(t, "error", res.Status)
	want := "Rejected output preserved at: gtms/tasks/error/" + res.TaskID + "/artifacts/tc-"
	assert.Contains(t, res.Summary, want,
		"the preserved-at path must be layout-relative to the project root")
	assert.NotContains(t, res.Summary, "preserved at: ..", "no spurious ../ path")
	assert.NotContains(t, res.Summary, "preserved at: /", "no absolute path")
	assert.NotRegexp(t, `preserved at: [A-Za-z]:`, res.Summary, "no absolute Windows path")
	// Producer recognition: the shipped built-in template is named, with the preset refresh.
	assert.Contains(t, res.Summary, staleTemplateRel,
		"the stale header must be attributed to the shipped built-in template")
	assert.Contains(t, res.Summary, "gtms init --force --preset",
		"a shipped producer must get the preset refresh command, not the custom-adapter text")
}

// TestBUG203_R4_SymlinkedRoot_ReportsLayoutRelativePath (Unix only): the project is reached
// through a symlinked root, so the resolved destination differs from the root spelling.
func TestBUG203_R4_SymlinkedRoot_ReportsLayoutRelativePath(t *testing.T) {
	skipIfShort(t)
	if runtime.GOOS == "windows" {
		t.Skip("symlinked-root case is Unix-only (Windows symlink creation needs privilege); the Windows short-path case covers Windows")
	}
	base := t.TempDir()
	real := filepath.Join(base, "real-project")
	require.NoError(t, os.MkdirAll(real, 0o755))
	writeStaleBuiltinProject(t, real)
	link := filepath.Join(base, "linked-project")
	require.NoError(t, os.Symlink(real, link))

	res := invokeBuiltinManualCreate(t, link, "r4symlink")
	assertLayoutRelativeQuarantine(t, res)
	moved, _ := filepath.Glob(filepath.Join(real, "gtms", "tasks", "error", res.TaskID, "artifacts", "tc-*.md"))
	assert.Len(t, moved, 1, "the rejected spec must be quarantined in the error artefacts folder")
}

// TestBUG203_R4_ProducerRecognition_ResolvedTemplateUnderSymlinkedRoot (Unix only): the
// template path is spelled through the resolved root while the project root is the
// symlink -- the mismatched spelling that used to defeat lexical relativisation.
func TestBUG203_R4_ProducerRecognition_ResolvedTemplateUnderSymlinkedRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinked-root case is Unix-only (Windows symlink creation needs privilege)")
	}
	base := t.TempDir()
	real := filepath.Join(base, "real")
	tmpl := filepath.Join(real, filepath.FromSlash(staleTemplateRel))
	require.NoError(t, os.MkdirAll(filepath.Dir(tmpl), 0o755))
	require.NoError(t, os.WriteFile(tmpl, []byte("x"), 0o644))
	link := filepath.Join(base, "link")
	require.NoError(t, os.Symlink(real, link))
	realResolved, err := filepath.EvalSymlinks(real)
	require.NoError(t, err)

	resolved := &ResolvedAdapter{Command: "create", Name: "manual-create", Tier: 0}
	for name, tf := range map[string]string{
		"template spelled through the link":          filepath.Join(link, filepath.FromSlash(staleTemplateRel)),
		"template spelled through the resolved root": filepath.Join(realResolved, filepath.FromSlash(staleTemplateRel)),
	} {
		t.Run(name, func(t *testing.T) {
			hint := staleScaffoldProducerHint(resolved, &AdapterContext{TemplateFile: tf}, link, staleViolation())
			assert.Contains(t, hint, "produced by "+staleTemplateRel+".")
			assert.Contains(t, hint, "gtms init --force --preset")
		})
	}
}

// --- R4-CODEX-001 (scoped to BUG-207): renamed-parent projects ---

// TestBUG203_R4_RenamedParent_HintRecommendsInitForce is the BUG-207 inversion of the
// former TestBUG203_R4_RenamedParent_HintNeverRecommendsInitForce (named owner approval,
// Owner Decision 7): init --force is now layout-aware, so a stale shipped producer in a
// renamed-parent project gets the normal narrow preset-refresh command and no BUG-207 text.
func TestBUG203_R4_RenamedParent_HintRecommendsInitForce(t *testing.T) {
	layout.InitFromParent("testing")
	defer layout.InitFromParent("gtms")
	root := t.TempDir()

	cases := map[string]struct {
		resolved *ResolvedAdapter
		ac       *AdapterContext
		producer string
	}{
		"built-in template": {
			resolved: &ResolvedAdapter{Command: "create", Name: "manual-create", Tier: 0},
			ac:       &AdapterContext{TemplateFile: filepath.Join(root, "testing", "test", "templates", "manual-testcase.template.md")},
			producer: "testing/test/templates/manual-testcase.template.md",
		},
		"shipped create script": {
			resolved: &ResolvedAdapter{Command: "create", Name: "manual-create-script", Tier: 2,
				Config: &config.AdapterConfig{Mode: "sync", Script: "testing/adapters/manual-create-script.sh"}},
			ac:       &AdapterContext{},
			producer: "testing/adapters/manual-create-script.sh",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			hint := staleScaffoldProducerHint(tc.resolved, tc.ac, root, staleViolation())
			assert.Contains(t, hint, tc.producer, "the producer is still named")
			assert.Contains(t, hint, "gtms init --force --preset",
				"a renamed-parent project now gets the normal preset-refresh command")
			assert.NotContains(t, hint, "BUG-207", "no user-facing hint may name the internal record id (ENH-214)")
			assert.NotContains(t, hint, "in an empty folder", "the manual copy workaround is retired")
			assert.NotContains(t, hint, "GTMS folder was renamed", "the renamed-folder limitation text is retired")
		})
	}
}

// A same-named file outside the active GTMS parent is custom code, not a shipped producer.
func TestBUG203_R4_ShippedRecognitionIsParentScoped(t *testing.T) {
	assert.True(t, isShippedPresetCreateProducer("gtms/adapters/manual-create-script.sh", "gtms"))
	assert.True(t, isShippedPresetCreateProducer("./gtms/adapters/agent-create-script.sh", "gtms"))
	assert.True(t, isShippedPresetCreateProducer("testing/test/templates/agent-testcase.template.md", "testing"))
	assert.False(t, isShippedPresetCreateProducer("custom/adapters/manual-create-script.sh", "gtms"))
	assert.False(t, isShippedPresetCreateProducer("gtms/adapters/manual-create-script.sh", "testing"))
	assert.False(t, isShippedPresetCreateProducer("gtms/adapters/house-style-create.sh", "gtms"))
}

// TestBUG203_R4_RenamedParent_InitForceRefreshMakesCreateSucceed is the BUG-207 inversion
// of the former TestBUG203_R4_RenamedParent_ManualWorkaroundMakesCreateSucceed (named owner
// approval, Owner Decision 1/7). The old manual-copy route is replaced by the automatic
// refresh route: the first create fails with the normal preset-refresh command (no BUG-207),
// running gtms init --force --preset manual refreshes the stale producer under the renamed
// folder in place, and the second create then succeeds under the renamed cases folder with
// no canonical gtms/ tree.
func TestBUG203_R4_RenamedParent_InitForceRefreshMakesCreateSucceed(t *testing.T) {
	skipIfShort(t)
	layout.InitFromParent("testing")
	defer layout.InitFromParent("gtms")

	// v0.3.x-shaped project whose GTMS folder was renamed gtms/ -> testing/.
	root := t.TempDir()
	writeStaleBuiltinProject(t, root)
	require.NoError(t, os.Rename(filepath.Join(root, "gtms"), filepath.Join(root, "testing")))
	parent, err := config.FindParentDir(root)
	require.NoError(t, err)
	require.Equal(t, "testing", parent, "fixture must be a renamed-parent project")

	// 1. The create fails; the guidance names the shipped producer and the normal
	//    preset-refresh command, and never leaks the internal record id.
	first := invokeBuiltinManualCreate(t, root, "workaround")
	require.Equal(t, "error", first.Status)
	assert.Contains(t, first.Summary, "testing/test/templates/manual-testcase.template.md")
	assert.Contains(t, first.Summary, "gtms init --force --preset")
	assert.NotContains(t, first.Summary, "BUG-207")

	// 2. Automatic route: gtms init --force --preset manual refreshes the renamed folder
	//    in place -- no empty-folder scaffold, no manual copy.
	_, err = scaffold.Init(scaffold.Options{ProjectRoot: root, Name: "r", Repo: "org/r", Preset: scaffold.PresetManual, Force: true})
	require.NoError(t, err)
	tmpl, err := os.ReadFile(filepath.Join(root, "testing", "test", "templates", "manual-testcase.template.md"))
	require.NoError(t, err)
	require.Contains(t, string(tmpl), "test-case/v2", "the renamed template must now be current")

	// 3. gtms create now succeeds, writing a valid spec under the renamed cases folder.
	second := invokeBuiltinManualCreate(t, root, "workaround")
	assert.Equal(t, "complete", second.Status, "create must succeed after the refresh: %s", second.Summary)
	specs, _ := filepath.Glob(filepath.Join(root, "testing", "test", "cases", "workaround", "tc-*.md"))
	assert.Len(t, specs, 1, "the created spec must land in the renamed cases folder")
	_, statErr := os.Stat(filepath.Join(root, "gtms"))
	assert.True(t, os.IsNotExist(statErr), "no default-layout gtms/ tree may appear")
	for _, s := range specs {
		b, _ := os.ReadFile(s)
		assert.True(t, strings.Contains(string(b), `gtms_schema: "test-case/v2"`) || strings.Contains(string(b), "gtms_schema: test-case/v2"),
			"the created spec must carry the v2 header")
	}
}
