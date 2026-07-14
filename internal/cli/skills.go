package cli

import (
	"github.com/spf13/cobra"

	"github.com/aitestmanagement/gtms-cli/internal/cli/help"
)

// newSkillsCmd creates the "skills" help topic. It shows the embedded
// starter Agent Skills overview -- where each skill fits in the GTMS
// pipeline, where gtms init put them, and how to install one into
// the agent's own skills directory.
//
// As a non-runnable command (no RunE), cobra lists this under
// "Additional help topics:" in root help and prints only Long (no
// Usage/Flags block) for both "gtms skills" and "gtms help skills".
func newSkillsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "skills",
		Short: "Starter agent skills for the GTMS pipeline",
		Long:  help.SkillsOverview,
	}
}
