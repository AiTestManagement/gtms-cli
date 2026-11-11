package adapter

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// REV-246 chunk-001-001 (BUG-214 post-merge release-review correction): the create
// spec-rejection diagnostic must name a rejected spec's original location as a normal
// project-relative path when the project is reached through a link. BUG-214 made
// create's output directory canonical while the invocation project root keeps the
// link spelling, so the old plain filepath.Rel rendered "../project/gtms/test/cases/...".
//
// The adapter writes a spec whose id is not in this invocation's batch, so validation
// rejects it and the quarantine branch reports it as unattributable -- the branch that
// prints origRel (a successfully quarantined spec is reported by its new location
// instead, so it would not exercise origRel).
func TestInvokeWithRoot_Create_LinkedProject_RejectedSpecDiagnostic_ProjectRelative(t *testing.T) {
	skipIfShort(t)
	linkRoot, canonicalOut := linkedProject(t, "feature") // skips where symlinks are unavailable

	scriptRel := "testdata/create-foreign.sh"
	scriptPath := filepath.Join(linkRoot, scriptRel)
	require.NoError(t, os.MkdirAll(filepath.Dir(scriptPath), 0o755))
	require.NoError(t, os.WriteFile(scriptPath, []byte(createForeignInvalidScript), 0o755))

	cfg := &config.Config{Project: config.ProjectConfig{Name: "Test", Repo: "org/test"}}
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "foreign",
		Config:  &config.AdapterConfig{Mode: "sync", Script: scriptRel},
		Tier:    2,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), linkRoot, cfg, resolved, "feature",
		CommandFlags{Folder: "feature", ResolvedOutputDir: canonicalOut})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Equal(t, "error", res.Status, "the rejected spec must fail the create; summary: %s", res.Summary)

	// The rejection/quarantine diagnostic branch really ran: the unattributable message is present.
	const marker = "Rejected output was left in place because GTMS could not prove this create owns it: "
	require.Contains(t, res.Summary, marker, "the unattributable quarantine diagnostic must run; summary: %s", res.Summary)

	// It names the original location as a normal project-relative path under gtms/test/cases/.
	assert.Contains(t, res.Summary, marker+"gtms/test/cases/feature/tc-ffffffff-foreign.md.",
		"the original location must be the project-relative path; summary: %s", res.Summary)

	// And never as a "../" path or an absolute path.
	named := regexp.MustCompile(regexp.QuoteMeta(marker) + `([^\s]+?)\.(\s|$)`).FindStringSubmatch(res.Summary)
	require.Len(t, named, 3, "could not extract the named path; summary: %s", res.Summary)
	assert.False(t, strings.HasPrefix(named[1], "../"), "named path %q must not start with ../", named[1])
	assert.False(t, filepath.IsAbs(named[1]) || strings.HasPrefix(named[1], "/"),
		"named path %q must not be absolute", named[1])
	assert.NotContains(t, res.Summary, "../project/", "the summary must not contain a ../project/ path")

	// The file is left in place (ownership unproven), unchanged behaviour.
	assert.FileExists(t, filepath.Join(canonicalOut, "tc-ffffffff-foreign.md"))
}
