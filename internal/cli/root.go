// Package cli defines the Cobra command tree for the GTMS CLI.
// Commands are thin: they parse flags, call core packages, and format output.
package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/output"
)

// Global flags
var (
	verbose bool
	dryRun  bool
)

// appConfig holds the loaded configuration, set in PersistentPreRunE.
var appConfig *config.Config

// projectRoot holds the discovered project root directory.
var projectRoot string

// rootCmd is the base command for the GTMS CLI.
var rootCmd = &cobra.Command{
	Use:   "gtms",
	Short: "Git-based Test Management System",
	Long: `GTMS orchestrates AI coding agents through a three-stage pipeline:
CREATE test cases, AUTOMATE them, and EXECUTE them.

Each command delegates to a pluggable adapter registered in gtms.config.`,
	Version:       Version,
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Skip config loading for help, version, and init
		if cmd.Name() == "help" || cmd.Name() == "version" || cmd.Name() == "init" {
			return nil
		}

		// Find project root
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("could not determine working directory: %w", err)
		}

		root, err := config.FindProjectRoot(cwd)
		if err != nil {
			output.Errorf(err.Error(), "")
			return output.AsDisplayed(err)
		}
		projectRoot = root

		// ENH-098: discover the sentinel-marked parent directory and
		// initialize layout defaults to reflect its actual name. This
		// supports renamed parents (e.g. user did "git mv gtms/ testing/").
		parentName, err := config.FindParentDir(root)
		if err != nil {
			output.Errorf(err.Error(), "")
			return output.AsDisplayed(err)
		}
		layout.InitFromParent(parentName)

		// Load config
		cfg, err := config.Load(root)
		if err != nil {
			output.Errorf(err.Error(), "")
			return output.AsDisplayed(err)
		}
		appConfig = cfg

		// ENH-078: surface non-fatal config warnings (e.g. fail-exit-codes
		// set on a Tier 2 adapter) on stderr so users notice them once at
		// load time without breaking the run.
		for _, w := range cfg.Warnings {
			fmt.Fprintf(os.Stderr, "⚠ %s\n", w)
		}

		if verbose {
			fmt.Fprintf(os.Stderr, "Project root: %s\n", projectRoot)
			fmt.Fprintf(os.Stderr, "Project: %s (%s)\n", cfg.Project.Name, cfg.Project.Repo)
		}

		return nil
	},
}

func init() {
	rootCmd.SetVersionTemplate("gtms {{.Version}}\n")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose output")
	rootCmd.PersistentFlags().BoolVar(&dryRun, "dry-run", false, "Show what would be done without executing")

	// Register subcommands
	rootCmd.AddCommand(newCreateCmd())
	rootCmd.AddCommand(newDeleteCmd())
	rootCmd.AddCommand(newAutomateCmd())
	rootCmd.AddCommand(newExecuteCmd())
	rootCmd.AddCommand(newStatusCmd())
	rootCmd.AddCommand(newGapsCmd())
	rootCmd.AddCommand(newTriageCmd())
	rootCmd.AddCommand(newMapCmd())
	rootCmd.AddCommand(newLinkCmd())
	rootCmd.AddCommand(newListCmd())
	rootCmd.AddCommand(newResetCmd())
	rootCmd.AddCommand(newInitCmd())
	rootCmd.AddCommand(newPrimeCmd())
	rootCmd.AddCommand(newVersionCmd())
}

// ExecuteContext runs the root command with the given context.
// Subcommands receive the context via cmd.Context().
func ExecuteContext(ctx context.Context) error {
	return rootCmd.ExecuteContext(ctx)
}

// Execute runs the root command. Called from main.go.
func Execute() error {
	return ExecuteContext(context.Background())
}

// GetConfig returns the loaded config. Used by subcommands.
func GetConfig() *config.Config {
	return appConfig
}

// GetProjectRoot returns the discovered project root. Used by subcommands.
func GetProjectRoot() string {
	return projectRoot
}

// IsVerbose returns true if --verbose was set.
func IsVerbose() bool {
	return verbose
}

// IsDryRun returns true if --dry-run was set.
func IsDryRun() bool {
	return dryRun
}

