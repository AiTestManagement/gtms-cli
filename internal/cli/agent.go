package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/aitestmanagement/gtms-cli/internal/cli/help"
	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/onboarding"
)

// newAgentCmd creates the "agent" help topic. It shows the embedded
// AI coding-assistant quick reference -- the compact operating cheat sheet
// for AI agents driving GTMS through its CLI.
//
// The content is embedded via go:embed from a package-local mirror of
// reference/ai-coding-assistant-guide.md. A parity test in internal/cli/help/
// guards byte-equality between the mirror and the canonical file.
//
// As a non-runnable command (no RunE), cobra lists this under
// "Additional help topics:" in root help and prints only Long (no
// Usage/Flags block) for both "gtms agent" and "gtms help agent".
//
// ENH-185: the HelpFunc is overridden (not RunE -- cobra never reaches
// PersistentPreRunE for non-runnable commands) to check whether the
// project's agent instruction file mentions GTMS. If it does not, a
// single dim nudge line is printed to stderr before the guide content.
func newAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "AI agents start here -- operating quick reference",
		Long:  help.AgentGuide,
	}

	// ENH-185: agent-instructions nudge. Capture the default HelpFunc
	// before overriding so the guide still renders via cobra's standard
	// help template (Long-only for non-runnable commands).
	defaultHelp := cmd.HelpFunc()
	cmd.SetHelpFunc(func(c *cobra.Command, args []string) {
		cwd, err := os.Getwd()
		if err == nil {
			root, findErr := config.FindProjectRoot(cwd)
			if findErr == nil {
				onboarding.CheckAgentInstructions(root, os.Stderr)
			}
		}
		defaultHelp(c, args)
	})

	return cmd
}
