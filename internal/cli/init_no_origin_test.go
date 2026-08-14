package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/scaffold"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureStdout is defined in create_test.go (same package): it redirects
// os.Stdout to a pipe around fn and returns what fn wrote.

// ENH-190: the reserved no-origin sentinel is translated to friendly prose in
// banners; a real repo is passed through unchanged.
func TestRepoBannerLabel(t *testing.T) {
	assert.Equal(t, "no origin remote configured", repoBannerLabel(config.NoOriginRepo),
		"the reserved sentinel must be rendered as friendly prose, never raw")
	assert.Equal(t, "acme/widgets", repoBannerLabel("acme/widgets"),
		"a real repo identifier must be displayed unchanged")
	assert.Equal(t, "org/repo-name", repoBannerLabel("org/repo-name"),
		"repoBannerLabel translates only the reserved sentinel, not the old placeholder")
}

// ENH-190: normal-init completion banner translates the sentinel and prints the
// one-time guidance line, and leaks neither the raw sentinel nor the old
// org/repo-name placeholder.
func TestFormatInitOutput_NoOriginSentinel(t *testing.T) {
	root := t.TempDir()
	out := captureStdout(t, func() {
		formatInitOutput(root, "my-project", config.NoOriginRepo, scaffold.PresetManual, &scaffold.Result{}, false)
	})

	assert.Contains(t, out, "Project: my-project (no origin remote configured)",
		"the completion banner must translate the sentinel")
	assert.Contains(t, out, noOriginGuidance,
		"the one-time init guidance must print when the sentinel is selected")
	assert.NotContains(t, out, "org/repo-name",
		"the retired placeholder must never appear")
	assert.NotContains(t, out, config.NoOriginRepo,
		"the raw sentinel value must never leak to the user")
}

// ENH-190: with a real inferred repo, the completion banner shows the repo and
// prints no no-origin guidance or friendly phrase.
func TestFormatInitOutput_RealRepo_NoGuidance(t *testing.T) {
	root := t.TempDir()
	out := captureStdout(t, func() {
		formatInitOutput(root, "my-project", "acme/widgets", scaffold.PresetManual, &scaffold.Result{}, false)
	})

	assert.Contains(t, out, "Project: my-project (acme/widgets)",
		"a real repo must be shown verbatim in the banner")
	assert.NotContains(t, out, noOriginGuidance,
		"guidance must not print when a real repo was inferred")
	assert.NotContains(t, out, "no origin remote configured",
		"the friendly no-origin phrase must not print for a real repo")
}

// ENH-190: the fresh demo path prints the same completion banner and guidance
// as normal init when showProject is true.
func TestFormatDemoOutput_FreshBanner_NoOrigin(t *testing.T) {
	root := t.TempDir()
	out := captureStdout(t, func() {
		formatDemoOutput(root, "demo-project", config.NoOriginRepo, true, &scaffold.DemoSeedResult{}, false)
	})

	assert.Contains(t, out, "Demo data seeded",
		"the demo header must still print")
	assert.Contains(t, out, "Project: demo-project (no origin remote configured)",
		"fresh demo init must print the translated completion banner")
	assert.Contains(t, out, noOriginGuidance,
		"fresh demo init must print the one-time guidance for the sentinel")
	assert.NotContains(t, out, "org/repo-name")
	assert.NotContains(t, out, config.NoOriginRepo)
}

// ENH-190: fresh demo with an explicit/real repo shows it and prints no guidance.
func TestFormatDemoOutput_FreshBanner_RealRepo(t *testing.T) {
	root := t.TempDir()
	out := captureStdout(t, func() {
		formatDemoOutput(root, "demo-project", "chosen/demo", true, &scaffold.DemoSeedResult{}, false)
	})

	assert.Contains(t, out, "Project: demo-project (chosen/demo)")
	assert.NotContains(t, out, noOriginGuidance)
	assert.NotContains(t, out, "no origin remote configured")
}

// ENH-190: the existing-config demo-merge path prints no project banner
// (unchanged pre-ENH-190 behaviour).
func TestFormatDemoOutput_MergePath_NoBanner(t *testing.T) {
	root := t.TempDir()
	out := captureStdout(t, func() {
		formatDemoOutput(root, "", "", false, &scaffold.DemoSeedResult{}, false)
	})

	assert.Contains(t, out, "Demo data seeded")
	assert.NotContains(t, out, "Project:",
		"the demo-merge path must not print a project completion banner")
	assert.NotContains(t, out, noOriginGuidance)
}

// ENH-190: guard that the retired placeholder is gone from init.go's inference
// fallbacks -- both call sites now pass the reserved sentinel.
func TestInitFallbackUsesSentinel(t *testing.T) {
	src, err := os.ReadFile("init.go")
	require.NoError(t, err)
	body := string(src)
	assert.NotContains(t, body, `"org/repo-name"`,
		"init.go must no longer pass the retired org/repo-name placeholder to git.InferRepo")
	assert.Equal(t, 2, strings.Count(body, "git.InferRepo(ctx, cwd, config.NoOriginRepo)"),
		"both InferRepo call sites (normal + fresh demo) must pass config.NoOriginRepo")
}
