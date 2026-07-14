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
	"github.com/aitestmanagement/gtms-cli/internal/layoutmigration"
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

// rootLongBase is the compile-time portion of the root command help. The
// version-correct permalink suffix is appended at runtime in init().
const rootLongBase = `GTMS tracks your testing the way Git tracks your code.

It does that by orchestrating AI coding agents through a three-stage pipeline:
CREATE test cases, AUTOMATE them, and EXECUTE them. A PRIME path feeds the same
pipeline without automation scripts -- GTMS stamps a result template that a
human tester, or an AI agent, fills in and records.

IF YOU ARE AN AI CODING ASSISTANT, run ` + "`gtms agent`" + ` now: it prints your
operating quick reference -- commands, flags, workflow sequences, result
interpretation, and gotchas -- everything you need to drive GTMS correctly.`

// rootCmd is the base command for the GTMS CLI.
var rootCmd = &cobra.Command{
	Use:   "gtms",
	Short: "Git-based Test Management System",
	Version:       Version,
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Skip config loading for help, version, init, and help topics
		if cmd.Name() == "help" || cmd.Name() == "version" || cmd.Name() == "init" || cmd.Name() == "agent" || cmd.Name() == "skills" || cmd.Name() == "getting-started" {
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

		// ENH-098 + ENH-164/165 interaction: a v0.2.0-shape install whose
		// parent dir was renamed via ENH-098 (e.g. gtms -> testing) still
		// has its legacy cases/ slot under the renamed parent
		// (testing/cases/). The migration core's filesystem constants are
		// anchored at canonical gtms/cases/, so NeedsLegacyMigration would
		// return false and the standard migration would silently no-op
		// while the reader walked the new <renamedParent>/test/cases/ layout
		// and found nothing -- losing visibility silently. Halt with a
		// clear remediation path so the user can either rename the parent
		// back to gtms/ temporarily or migrate by hand.
		if layoutmigration.HasRenamedParentLegacy(root, parentName) {
			migErr := fmt.Errorf(
				"renamed-parent legacy migration is not supported: detected %s/cases/ under renamed parent.\n"+
					"    The ENH-164 layout migration is anchored at the canonical 'gtms/' parent name and cannot\n"+
					"    handle ENH-098 renamed parents directly. Either:\n"+
					"      1. Rename the parent back to 'gtms/' temporarily (mv %s gtms), run any gtms command to\n"+
					"         trigger the migration, then rename to your preferred name again (mv gtms %s).\n"+
					"      2. Migrate manually:\n"+
					"         - mv %s/cases/templates/* %s/test/templates/   (if templates/ exists)\n"+
					"         - mv %s/cases/guides/*    %s/test/guides/      (if guides/ exists)\n"+
					"         - mv %s/cases/prompts/*   %s/test/prompts/     (if prompts/ exists)\n"+
					"         - mv %s/cases/<user-tcs>  %s/test/cases/\n"+
					"         - rmdir %s/cases\n"+
					"         - update gtms.config guide-dir and prompt-template literals from %s/cases/... to %s/test/...",
				parentName, parentName, parentName,
				parentName, parentName,
				parentName, parentName,
				parentName, parentName,
				parentName, parentName,
				parentName,
				parentName, parentName)
			output.Errorf(migErr.Error(), "")
			return output.AsDisplayed(migErr)
		}

		// ENH-164: runtime migration shim for legacy gtms/cases/ trees.
		// Runs before any reader/writer operation to migrate v0.2.0-shape
		// installs to the new gtms/test/ layout on first invocation.
		if layoutmigration.NeedsLegacyMigration(root) {
			// Sweep every adapter entry across all command keys for a non-empty
			// guide-dir. This is wider than create-only because guide-dir is a
			// field on the shared AdapterConfig: it's semantically create-facing
			// today, but the migration safety rule is about filesystem
			// preservation, not command semantics. If any adapter points inside
			// gtms/cases/, the shim must refuse to migrate that tree rather
			// than silently move or orphan user-owned guide content.
			var guideDirEntries []layoutmigration.GuideDirEntry
			if cfg.Adapters != nil {
				for cmdName, adapters := range cfg.Adapters {
					for adapterName, ac := range adapters {
						if ac != nil && ac.GuideDir != "" {
							guideDirEntries = append(guideDirEntries, layoutmigration.GuideDirEntry{
								Command: cmdName,
								Adapter: adapterName,
								Value:   ac.GuideDir,
							})
						}
					}
				}
			}

			// Three-way safety check across every collected entry. On the first
			// Case 2 hit, halt with a specific error naming the offending
			// command + adapter + path so the user can locate and fix it
			// rather than guessing which adapter triggered the halt.
			if offender, migErr := layoutmigration.FirstCase2Offender(guideDirEntries); offender != nil {
				scoped := fmt.Errorf(
					"adapters.%s.%s.guide-dir %s",
					offender.Command, offender.Adapter, migErr.Error())
				output.Errorf(scoped.Error(), "")
				return output.AsDisplayed(scoped)
			}

			// Perform the migration
			if err := layoutmigration.Migrate(root); err != nil {
				output.Errorf("layout migration failed: "+err.Error(), "")
				return output.AsDisplayed(err)
			}

			// Case 1: rewrite known-default guide-dir values across every
			// scanned entry. Case 3 (customised outside legacy tree) entries
			// are left alone; only the entries that match the known legacy
			// default are repointed.
			for _, e := range guideDirEntries {
				if layoutmigration.IsCase1GuideDir(e.Value) && e.Value != "" {
					if err := layoutmigration.RewriteGuideDirInConfig(root, e.Value, layoutmigration.NewDefaultGuideDir); err != nil {
						output.Errorf("guide-dir rewrite failed: "+err.Error(), "")
						return output.AsDisplayed(err)
					}
				}
			}

			// Re-initialize layout after migration (paths.TestCases now points to the new location)
			// and reload config to pick up any guide-dir rewrite.
			layout.InitFromParent(parentName)
			cfg, err = config.Load(root)
			if err != nil {
				output.Errorf(err.Error(), "")
				return output.AsDisplayed(err)
			}
			appConfig = cfg

			fmt.Fprintf(os.Stderr, "✓ Migrated legacy gtms/cases/ tree to gtms/test/ layout.\n")
		}

		// ENH-165: migrate the prompts/ slot from gtms/cases/prompts/ to
		// gtms/test/prompts/. This is independent of NeedsLegacyMigration
		// because the half-migrated state (post-ENH-164 install where
		// prompts/ was intentionally preserved in place at gtms/cases/prompts/)
		// has gtms/test/cases/ already present, so NeedsLegacyMigration
		// returns false. Either runs after a fresh ENH-164 Migrate() above
		// (pre-ENH-164 v0.2.0 install -> full migration happens in one shim
		// invocation) or fires standalone on a post-ENH-164 install that
		// still carries the legacy prompts/.
		if layoutmigration.NeedsPromptsMigration(root) {
			warning, err := layoutmigration.MigratePrompts(root)
			if err != nil {
				output.Errorf("prompts migration failed: "+err.Error(), "")
				return output.AsDisplayed(err)
			}

			// Reload config to pick up the prompt-template rewrite.
			cfg, err = config.Load(root)
			if err != nil {
				output.Errorf(err.Error(), "")
				return output.AsDisplayed(err)
			}
			appConfig = cfg

			if warning != "" {
				fmt.Fprintf(os.Stderr, "⚠ %s\n", warning)
			}
			fmt.Fprintf(os.Stderr, "✓ Migrated legacy gtms/cases/prompts/ to gtms/test/prompts/.\n")
		}

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
	// BUG-113: hide --dry-run from all help output. The flag still parses
	// everywhere (silent no-op preserved); only delete and reset re-register
	// it as a local flag so it appears in their help.
	rootCmd.PersistentFlags().MarkHidden("dry-run")

	// Hide Cobra's auto-generated `completion` command from `gtms --help`.
	// The command still works if invoked directly.
	rootCmd.CompletionOptions.HiddenDefaultCmd = true

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
	rootCmd.AddCommand(newAgentCmd())
	rootCmd.AddCommand(newSkillsCmd())
	rootCmd.AddCommand(newGettingStartedCmd())

	// ENH-157: assemble root Long at runtime so the version-correct
	// permalink uses the release tag on tagged builds and "main" on dev.
	//
	// BUG-116 contract (do NOT trim to Windows-only): the foot must state the
	// full cross-platform script-adapter guarantee. ENH-179 briefly narrowed it
	// to a Windows-only line and regressed the help-cross-platform-adapter BATS
	// (tc-4eeb7a9f, tc-c3bee3a6). Keep the exact wording, and keep the wrap after
	// "on Windows," so "GTMS resolves a POSIX shell for script adapters across
	// platforms" stays intact on one line (a BATS grep asserts it appears once).
	rootCmd.Long = rootLongBase +
		"\n\nFull guide:       " + guideURL("USER-GUIDE.md", "") +
		"\nAdapter contract: " + guideURL("reference/adapter-guide.md", "") +
		"\n\nGTMS resolves a POSIX shell for script adapters across platforms; on Windows," +
		"\na standard Git for Windows install is detected automatically."
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

