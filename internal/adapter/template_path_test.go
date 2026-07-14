package adapter

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestResolveTemplatePath_RoleRouting covers ENH-161 AC #7: the resolver
// derives the role-specific template path from the resolved adapter name,
// so switching defaults.{create,prime} routes to the correct template with
// zero source edits. Asserts the full four-cell mapping locked in the ENH.
func TestResolveTemplatePath_RoleRouting(t *testing.T) {
	root := "/fake/root"
	cases := []struct {
		command string
		adapter string
		want    string
	}{
		{"create", "manual-create", filepath.Join(root, "gtms", "test", "templates", "manual-testcase.template.md")},
		{"create", "agent-create", filepath.Join(root, "gtms", "test", "templates", "agent-testcase.template.md")},
		{"prime", "manual-prime", filepath.Join(root, "gtms", "manual", "templates", "manual-result.template.yaml")},
		{"prime", "agent-prime", filepath.Join(root, "gtms", "manual", "templates", "agent-result.template.yaml")},
	}
	for _, tc := range cases {
		t.Run(tc.command+"+"+tc.adapter, func(t *testing.T) {
			got := ResolveTemplatePath(root, tc.command, tc.adapter)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestResolveTemplatePath_ScriptSuffixStripped covers ENH-161 AC #18-#23:
// the built-in and Tier 2 script variants of each role share a single
// template, so the -script suffix is stripped before role detection.
func TestResolveTemplatePath_ScriptSuffixStripped(t *testing.T) {
	root := "/fake/root"
	cases := []struct {
		command string
		adapter string
		want    string
	}{
		{"create", "manual-create-script", filepath.Join(root, "gtms", "test", "templates", "manual-testcase.template.md")},
		{"create", "agent-create-script", filepath.Join(root, "gtms", "test", "templates", "agent-testcase.template.md")},
		{"prime", "manual-prime-script", filepath.Join(root, "gtms", "manual", "templates", "manual-result.template.yaml")},
		{"prime", "agent-prime-script", filepath.Join(root, "gtms", "manual", "templates", "agent-result.template.yaml")},
	}
	for _, tc := range cases {
		t.Run(tc.command+"+"+tc.adapter, func(t *testing.T) {
			got := ResolveTemplatePath(root, tc.command, tc.adapter)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestResolveTemplatePath_NonStampingCommand returns the empty string for
// commands that do not stamp a templated artefact (automate, execute). The
// caller is expected to no-op the template-file branch when the path is
// empty, matching the existing prime-side pre-ENH-161 behaviour.
func TestResolveTemplatePath_NonStampingCommand(t *testing.T) {
	assert.Empty(t, ResolveTemplatePath("/fake/root", "automate", "manual-automate"))
	assert.Empty(t, ResolveTemplatePath("/fake/root", "execute", "manual-execute"))
	assert.Empty(t, ResolveTemplatePath("/fake/root", "", "manual-create"))
}

// TestResolveTemplatePath_UnknownAdapterDefaultsToManualRole covers the
// fallback for any adapter name that does not start with "agent-": the
// resolver picks the manual template. This is conservative -- new Tier 1
// adapters introduced under the same command should land on the manual
// template until they explicitly opt into the agent role via the agent-
// prefix convention.
func TestResolveTemplatePath_UnknownAdapterDefaultsToManualRole(t *testing.T) {
	root := "/fake/root"
	got := ResolveTemplatePath(root, "create", "my-custom-adapter")
	assert.Equal(t, filepath.Join(root, "gtms", "test", "templates", "manual-testcase.template.md"), got)
}

// --- ENH-164: ResolveTemplatePath routes to gtms/test/templates/ ---

// TestResolveTemplatePath_NewLayoutRouting freezes the post-ENH-164 routing:
// the role-specific stamping templates resolve under gtms/test/templates/,
// not the legacy gtms/test/templates/. Mirrors the table shape of the
// existing TestResolveTemplatePath_RoleRouting test (ENH-161 four-cell
// mapping); the only change is the path target.
func TestResolveTemplatePath_NewLayoutRouting(t *testing.T) {
	root := "/fake/root"
	cases := []struct {
		command string
		adapter string
		want    string
	}{
		{"create", "manual-create", filepath.Join(root, "gtms", "test", "templates", "manual-testcase.template.md")},
		{"create", "agent-create", filepath.Join(root, "gtms", "test", "templates", "agent-testcase.template.md")},
	}
	for _, tc := range cases {
		t.Run(tc.command+"+"+tc.adapter, func(t *testing.T) {
			got := ResolveTemplatePath(root, tc.command, tc.adapter)
			assert.Equal(t, tc.want, got,
				"ENH-164 AC: ResolveTemplatePath(%q, %q, %q) must route under gtms/test/templates/",
				root, tc.command, tc.adapter)
		})
	}
}

// TestResolveTemplatePath_NewLayoutRouting_ScriptSuffixStripped is the
// ENH-164 counterpart to TestResolveTemplatePath_ScriptSuffixStripped:
// the -script suffix is stripped before role detection, and the resolved
// path is under the new gtms/test/templates/ slot.
func TestResolveTemplatePath_NewLayoutRouting_ScriptSuffixStripped(t *testing.T) {
	root := "/fake/root"
	cases := []struct {
		command string
		adapter string
		want    string
	}{
		{"create", "manual-create-script", filepath.Join(root, "gtms", "test", "templates", "manual-testcase.template.md")},
		{"create", "agent-create-script", filepath.Join(root, "gtms", "test", "templates", "agent-testcase.template.md")},
	}
	for _, tc := range cases {
		t.Run(tc.command+"+"+tc.adapter, func(t *testing.T) {
			got := ResolveTemplatePath(root, tc.command, tc.adapter)
			assert.Equal(t, tc.want, got,
				"ENH-164 AC: ResolveTemplatePath(%q, %q, %q) must route under gtms/test/templates/ after stripping -script suffix",
				root, tc.command, tc.adapter)
		})
	}
}

// TestResolveTemplatePath_NewLayoutRouting_UnknownAdapterDefaultsToManualRole
// is the ENH-164 counterpart to
// TestResolveTemplatePath_UnknownAdapterDefaultsToManualRole: the
// conservative fallback (unknown adapter -> manual role) still applies
// after the layout move, and the resolved path is under the new
// gtms/test/templates/ slot.
func TestResolveTemplatePath_NewLayoutRouting_UnknownAdapterDefaultsToManualRole(t *testing.T) {
	root := "/fake/root"
	got := ResolveTemplatePath(root, "create", "my-custom-adapter")
	assert.Equal(t, filepath.Join(root, "gtms", "test", "templates", "manual-testcase.template.md"), got,
		"ENH-164 AC: unknown adapter falls back to manual role under the new gtms/test/templates/ slot")
}
