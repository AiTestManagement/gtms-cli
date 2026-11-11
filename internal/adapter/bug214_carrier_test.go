package adapter

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BUG-214 AC11 / Owner Decision 7: the output directory the create admission
// boundary already checked (CommandFlags.ResolvedOutputDir) is the value the
// adapter receives -- a Tier 1 adapter as {output_dir}, a Tier 2 adapter as
// GTMS_OUTPUT_DIR -- and it WINS over a different configured output-dir, because
// nothing downstream recomputes the target.

func TestInvokeWithRoot_Create_Tier1_ReceivesCheckedOutputDir(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	// A real, in-project directory distinct from the config output-dir, to prove
	// the carrier (not the config value) is what reaches the adapter.
	checked := filepath.Join(root, "checked-specs")
	require.NoError(t, os.MkdirAll(checked, 0o755))

	cfg := &config.Config{Project: config.ProjectConfig{Name: "Test", Repo: "org/test"}}
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock-tier1",
		// A DIFFERENT config output-dir, which must be ignored in favour of the carrier.
		Config: &config.AdapterConfig{Mode: "sync", Command: `echo "{output_dir}"`, OutputDir: "config/elsewhere/"},
		Tier:   1,
		Mode:   "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "feature",
		CommandFlags{Folder: "feature", ResolvedOutputDir: checked})
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)
	assert.Contains(t, res.Summary, checked)
	assert.NotContains(t, res.Summary, filepath.Join(root, "config/elsewhere"))
}

func TestInvokeWithRoot_Create_Tier2_ReceivesCheckedOutputDir(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	checked := filepath.Join(root, "checked-specs")
	require.NoError(t, os.MkdirAll(checked, 0o755))

	scriptDir := filepath.Join(root, "scripts")
	require.NoError(t, os.MkdirAll(scriptDir, 0o755))
	scriptPath := filepath.Join(scriptDir, "od-tier2.sh")
	// Record GTMS_OUTPUT_DIR so the test can prove what the adapter received.
	script := "#!/bin/sh\nprintf '%s' \"$GTMS_OUTPUT_DIR\" > \"$GTMS_PROJECT_ROOT/tier2-received.txt\"\n"
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o755))

	cfg := &config.Config{Project: config.ProjectConfig{Name: "Test", Repo: "org/test"}}
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "od-tier2",
		Config:  &config.AdapterConfig{Mode: "sync", Script: "scripts/od-tier2.sh", OutputDir: "config/elsewhere/"},
		Tier:    2,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "feature",
		CommandFlags{Folder: "feature", ResolvedOutputDir: checked})
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)

	got, readErr := os.ReadFile(filepath.Join(root, "tier2-received.txt"))
	require.NoError(t, readErr)
	assert.Equal(t, checked, string(got))
}

// Regression guard for the fallback: when the carrier is NOT set (direct callers,
// AC15's TestInvokeWithRoot_Create* shape), the historical config-or-folder
// computation still applies, so this change does not disturb those callers.
func TestInvokeWithRoot_Create_NoCarrier_FallsBackToConfig(t *testing.T) {
	skipIfShort(t)
	root := setupTestProject(t)

	cfg := &config.Config{Project: config.ProjectConfig{Name: "Test", Repo: "org/test"}}
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mock-tier1",
		Config:  &config.AdapterConfig{Mode: "sync", Command: `echo "{output_dir}"`, OutputDir: "custom/tests/"},
		Tier:    1,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "feature", CommandFlags{})
	require.NoError(t, err)
	assert.Equal(t, "complete", res.Status)
	assert.Contains(t, res.Summary, filepath.Join(root, "custom/tests/"))
}
