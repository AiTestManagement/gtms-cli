package cli

import (
	"github.com/spf13/cobra"

	"github.com/aitestmanagement/gtms-cli/internal/cli/help"
)

// newGettingStartedCmd creates the "getting-started" help topic. It shows
// the embedded linear quick start -- the shortest path from a fresh install
// to one test case through the pipeline.
//
// As a non-runnable command (no RunE), cobra lists this under
// "Additional help topics:" in root help and prints only Long (no
// Usage/Flags block) for both "gtms getting-started" and
// "gtms help getting-started".
func newGettingStartedCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "getting-started",
		Short: "Linear quick start for new projects and agents",
		Long:  help.GettingStarted,
	}
}
