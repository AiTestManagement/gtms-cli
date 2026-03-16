package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/aitestmanagement/gtms-cli/internal/git"
	"github.com/aitestmanagement/gtms-cli/internal/output"
	"github.com/aitestmanagement/gtms-cli/internal/scaffold"
)

// newInitCmd builds the 'gtms init' command.
func newInitCmd() *cobra.Command {
	var nameFlag string
	var repoFlag string
	var adapterFlag string
	var forceFlag bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialise a GTMS project in the current directory",
		Long: `Initialise a GTMS project by creating the required directory structure,
generating a gtms.config file, and optionally setting up prompt templates.

  gtms init                          — minimal preset, infer name/repo
  gtms init --adapter claude         — claude preset with prompt templates
  gtms init --adapter github         — github preset with adapter stubs
  gtms init --name "My Project"      — specify project name
  gtms init --force                  — overwrite existing gtms.config`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("could not determine working directory: %w", err)
			}

			// Validate: git repo required
			if !git.IsRepo(ctx, cwd) {
				output.Errorf("Not a git repository.", "Run 'git init' first, then 'gtms init'.")
				return output.AsDisplayed(fmt.Errorf("not a git repository"))
			}

			// Validate: no existing config unless --force
			configPath := filepath.Join(cwd, "gtms.config")
			if _, err := os.Stat(configPath); err == nil && !forceFlag {
				output.Errorf("gtms.config already exists in this directory.",
					"Use --force to overwrite, or edit the existing config.")
				return output.AsDisplayed(fmt.Errorf("gtms.config already exists"))
			}

			// Validate: warn if not at git root
			gitRoot, err := git.ProjectRoot(ctx, cwd)
			if err == nil {
				// Normalise paths for comparison
				cwdClean := filepath.Clean(cwd)
				rootClean := filepath.Clean(gitRoot)
				if !strings.EqualFold(cwdClean, rootClean) {
					fmt.Fprintf(os.Stderr, "%s You are not at the git repository root. GTMS will be initialised here, not at %s.\n",
						output.IconWarning, rootClean)
				}
			}

			// Validate preset
			if adapterFlag == "" {
				adapterFlag = scaffold.PresetMinimal
			}
			if !scaffold.IsValidPreset(adapterFlag) {
				output.Errorf(
					fmt.Sprintf("Unknown adapter preset '%s'.", adapterFlag),
					fmt.Sprintf("Valid presets: %s", strings.Join(scaffold.ValidPresets(), ", ")),
				)
				return output.AsDisplayed(fmt.Errorf("unknown adapter preset '%s'", adapterFlag))
			}

			// Infer defaults
			name := nameFlag
			if name == "" {
				name = filepath.Base(cwd)
			}

			repo := repoFlag
			if repo == "" {
				repo = git.InferRepo(ctx, cwd, "org/repo-name")
			}

			// Run scaffolding
			opts := scaffold.Options{
				ProjectRoot: cwd,
				Name:        name,
				Repo:        repo,
				Preset:      adapterFlag,
				Force:       forceFlag,
			}

			result, err := scaffold.Init(opts)
			if err != nil {
				output.Errorf(err.Error(), "")
				return output.AsDisplayed(err)
			}

			// Print success output
			formatInitOutput(cwd, name, repo, adapterFlag, result)

			return nil
		},
	}

	cmd.Flags().StringVar(&nameFlag, "name", "", "Project name (default: directory name)")
	cmd.Flags().StringVar(&repoFlag, "repo", "", "Repository path, e.g. org/repo (default: inferred from git remote)")
	cmd.Flags().StringVar(&adapterFlag, "adapter", "", "Adapter preset: minimal, claude, github (default: minimal)")
	cmd.Flags().BoolVar(&forceFlag, "force", false, "Overwrite existing gtms.config")

	return cmd
}

// formatInitOutput prints the success summary after initialisation.
func formatInitOutput(root, name, repo, preset string, result *scaffold.Result) {
	fmt.Printf("%s GTMS initialised in %s\n", output.IconComplete, root)
	fmt.Printf("  Project: %s (%s)\n", name, repo)
	fmt.Printf("  Adapter preset: %s\n", preset)
	fmt.Println()
	fmt.Println("  Created:")
	fmt.Println("    gtms.config")
	fmt.Println("    test-tasks/ (5 status directories)")

	// Describe test-cases dir based on preset
	switch preset {
	case scaffold.PresetClaude, scaffold.PresetGitHub:
		fmt.Println("    test-cases/ (with prompts, guides)")
	default:
		fmt.Println("    test-cases/ (with guides)")
	}

	// Describe test-automation dirs based on preset
	switch preset {
	case scaffold.PresetClaude, scaffold.PresetGitHub:
		fmt.Println("    test-automation/ (records, specs, prompts)")
		if preset == scaffold.PresetGitHub {
			fmt.Println("    adapters/ (6 stub scripts)")
		}
	default:
		fmt.Println("    test-automation/ (records, specs)")
	}

	fmt.Println("    test-execution/")

	// List prompt templates if created
	for _, f := range result.FilesCreated {
		if strings.HasPrefix(f, "test-cases/prompts/") || strings.HasPrefix(f, "test-automation/prompts/") {
			fmt.Printf("    %s\n", f)
		}
	}

	fmt.Println()
	fmt.Println("  Next steps:")
	fmt.Println("    1. Review gtms.config and adjust adapters")
	fmt.Println("    2. Run 'gtms status' to verify setup")
	fmt.Println("    3. Run 'gtms create <requirement-id>' to create your first test case")
}
