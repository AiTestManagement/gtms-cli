package adapter

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/aitestmanagement/gtms-cli/internal/task"
)

// BUG-209 section 9 (CLAUDE-001): a Tier 1 adapter that replaces its handoff with an
// unsafe leaf can never become a pass, whatever its exit code. A directory stands in
// for the unsafe leaf because creating one needs no privilege on any platform.
func TestTier1_UnsafeLeafIsErrorNotPass(t *testing.T) {
	skipIfShort(t)
	cases := []struct {
		name    string
		command string
		failOn  []int
	}{
		{"exit 0", `rm -f {result_file} && mkdir {result_file} && exit 0`, nil},
		{"fail exit code", `rm -f {result_file} && mkdir {result_file} && exit 3`, []int{3}},
		{"error exit", `rm -f {result_file} && mkdir {result_file} && exit 4`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := setupTestProject(t)
			cfg := &config.Config{Project: config.ProjectConfig{Name: "Test", Repo: "org/test"}}
			resolved := &ResolvedAdapter{
				Command: "create",
				Name:    "unsafe-tier1",
				Config:  &config.AdapterConfig{Mode: "sync", Command: tc.command, FailExitCodes: tc.failOn},
				Tier:    1,
				Mode:    "sync",
			}

			res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "JIRA-456", CommandFlags{})
			require.Error(t, err, "an unsafe leaf must be a non-zero exit")
			assert.True(t, result.IsRefusal(err), "got %v", err)
			assert.Nil(t, res)

			complete, lerr := task.List(root, "complete")
			require.NoError(t, lerr)
			assert.Empty(t, complete, "no task may reach complete/")
			errored, lerr := task.List(root, "error")
			require.NoError(t, lerr)
			assert.Len(t, errored, 1, "the task must be filed under error/")

			entries, derr := os.ReadDir(filepath.Join(root, ".gtms", "results"))
			require.NoError(t, derr)
			require.Len(t, entries, 1)
			assert.True(t, entries[0].IsDir(), "the unsafe leaf must be left as it is")
		})
	}
}
