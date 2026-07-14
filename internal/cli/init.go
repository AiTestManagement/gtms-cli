package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/git"
	"github.com/aitestmanagement/gtms-cli/internal/output"
	"github.com/aitestmanagement/gtms-cli/internal/scaffold"
)

// newInitCmd builds the 'gtms init' command.
func newInitCmd() *cobra.Command {
	var nameFlag string
	var repoFlag string
	var presetFlag string
	var presetsFlag bool
	var forceFlag bool
	var guidanceOffFlag bool
	var guidanceOnFlag bool
	var demoFlag bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialise a GTMS project in the current directory",
		Long: `Initialise a GTMS project by creating the required directory structure,
generating a gtms.config file, updating .gitignore, and setting up adapter
support for the chosen workflow preset.

Each preset is a workflow bundle that configures command routes, adapter names,
framework labels, and installed support assets.

Available presets:
  manual       Manual testing with result templates and schema validation
  bats         BATS shell testing with local runner and TAP classification
  playwright   Playwright browser testing with local TC-specific spec execution

Plain 'gtms init' in a repository with no GTMS project scaffolds the 'manual'
default preset (the beginner-friendly path). In an already-initialised project
it lists the available presets and exits without mutation. Use '--presets' to
list presets without scaffolding in either case.

Examples:
  gtms init                          -- scaffold with the manual default preset (no existing gtms.config)
  gtms init --presets                -- list available presets without scaffolding
  gtms init --preset manual          -- manual-only testing workflow
  gtms init --preset bats            -- BATS shell testing with local runner
  gtms init --preset playwright      -- Playwright browser testing
  gtms init --name "My Project"      -- specify project name (default: directory name)
  gtms init --force --preset bats    -- overwrite existing gtms.config
  gtms init --demo                   -- seed demo data for learning the pipeline
  gtms init --guidance-off           -- disable post-command guidance messages
  gtms init --guidance-on            -- re-enable post-command guidance messages

Preset switching is not supported in-place. To choose a different preset for
an existing project, remove gtms.config and generated scaffold assets, then
re-run init.`,
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

			// Validate: mutual exclusion of guidance flags
			if guidanceOffFlag && guidanceOnFlag {
				output.Errorf("Cannot use --guidance-off and --guidance-on together.",
					"Use one or the other.")
				return output.AsDisplayed(fmt.Errorf("--guidance-off and --guidance-on are mutually exclusive"))
			}

			// Validate: --demo and --preset are mutually exclusive
			if demoFlag && presetFlag != "" {
				output.Errorf("Cannot use --demo with --preset.",
					"Demo provides its own adapters.")
				return output.AsDisplayed(fmt.Errorf("--demo and --preset are mutually exclusive"))
			}

			// Handle guidance toggle on existing config (before the "already exists" guard)
			// Only return early if --demo is NOT also set; when --demo is set,
			// guidance-off is handled inside the demo flow instead.
			//
			// The guidance flags are config toggles on an existing project, not
			// project-creation flags. They work from any subdirectory -- if cwd
			// has no gtms.config, walk ancestors to find the project root.
			configPath := filepath.Join(cwd, "gtms.config")
			if (guidanceOffFlag || guidanceOnFlag) && !demoFlag {
				if presetFlag != "" {
					// guidance flags with --preset: proceed to scaffolding, apply guidance after
				} else {
					targetConfig := ""
					if _, err := os.Stat(configPath); err == nil {
						targetConfig = configPath
					} else if ancestorRoot, err := config.FindProjectRoot(cwd); err == nil {
						targetConfig = filepath.Join(ancestorRoot, "gtms.config")
					}
					if targetConfig != "" {
						cfg, err := config.LoadFromFile(targetConfig)
						if err != nil {
							output.Errorf(err.Error(), "")
							return output.AsDisplayed(err)
						}
						cfg.Guidance = !guidanceOffFlag
						if err := config.WriteConfig(targetConfig, cfg); err != nil {
							output.Errorf(err.Error(), "")
							return output.AsDisplayed(err)
						}
						// Include the path in the success message when the
						// updated config is not in cwd, so users know which
						// project was modified.
						pathHint := ""
						targetDir := filepath.Dir(targetConfig)
						if !strings.EqualFold(filepath.Clean(targetDir), filepath.Clean(cwd)) {
							pathHint = fmt.Sprintf(" for %s", targetDir)
						}
						if guidanceOffFlag {
							fmt.Fprintf(os.Stderr, "%s Guidance messages disabled%s.\n", output.IconComplete, pathHint)
						} else {
							fmt.Fprintf(os.Stderr, "%s Guidance messages enabled%s.\n", output.IconComplete, pathHint)
						}
						return nil
					}
					// No config anywhere -- guidance flags require an existing project.
					output.Errorf(
						"--guidance-off/--guidance-on requires an existing GTMS project, but none was found in this directory or any ancestor.",
						"Run 'gtms init --preset <name>' first to scaffold a project, or cd into an existing project.",
					)
					return output.AsDisplayed(fmt.Errorf("guidance flag requires an existing GTMS project"))
				}
			}

			// S4: detect flat v0.1.0 layout -- structural migration guard.
			// This fires regardless of --force, --demo, --preset, or plain init
			// because flat + nested coexistence is always a broken state.
			if flatDirs := detectFlatLayout(cwd); len(flatDirs) > 0 {
				msg, hint := flatLayoutErrorMessage(flatDirs, runtime.GOOS)
				output.Errorf(msg, hint)
				return output.AsDisplayed(fmt.Errorf("flat v0.1.0 layout detected"))
			}

			// BUG-111 (post-merge revision): preset visibility lives in two
			// places now:
			//   - `gtms init --presets`                        -> list and exit, never scaffold.
			//   - plain `gtms init` / `--force` in existing project -> list + re-init recipe hint, no mutation.
			// Plain `gtms init` / `--force` in an empty repo falls THROUGH this
			// branch and scaffolds the manual default preset below (the
			// beginner-friendly path -- AC #5 / #26 / #27 post-merge revision).
			//
			// Guards: --demo, --preset, and guidance-only invocations have
			// already returned above; this block fires only for plain init,
			// --force-only, and --presets.
			if presetsFlag || (presetFlag == "" && !demoFlag && !guidanceOffFlag && !guidanceOnFlag) {
				// Check whether gtms.config exists in THIS directory. The
				// ancestor case (no cwd config, but a parent has one) must
				// fall through to the nested-project guard below; reporting
				// "already initialized" from a nested dir would mask the
				// "scaffolding inside another project" footgun.
				existingProject := false
				if _, err := os.Stat(configPath); err == nil {
					existingProject = true
				}

				// --presets always lists and exits without mutation, whether
				// the cwd is the project root or empty.
				if presetsFlag {
					printPresetList(os.Stderr)
					return nil
				}

				// Plain init / --force at the project root: list presets,
				// hint at the re-init recipe, no mutation.
				if existingProject {
					fmt.Fprintf(os.Stderr, "This project is already initialized.\n\n")
					printPresetList(os.Stderr)
					fmt.Fprintf(os.Stderr, "\nTo choose a different preset, remove gtms.config and generated scaffold assets, then re-run 'gtms init --preset <name>'.\n")
					// ENH-183: Re-surface the snippet CTA on re-init.
					fmt.Fprintf(os.Stderr, "\n%s Wire your AI agent in: paste gtms/AGENTS-SNIPPET.md into your CLAUDE.md / AGENTS.md\n", output.IconPending)
					return nil
				}

				// Plain init / --force in an empty repo: fall through to
				// scaffold the manual default preset. Set the preset flag
				// so the downstream scaffolding code path runs unchanged.
				// (The nested-project guard at line ~310 still fires for the
				// ancestor case before any scaffolding happens.)
				presetFlag = scaffold.PresetManual
			}

			// Handle --demo flag (before the "already exists" guard)
			if demoFlag {
				configExists := false
				if _, err := os.Stat(configPath); err == nil {
					configExists = true
				}

				if configExists && !forceFlag {
					// Config exists -- check idempotency, then merge demo data
					cfg, err := config.LoadFromFile(configPath)
					if err != nil {
						output.Errorf(err.Error(), "")
						return output.AsDisplayed(err)
					}
					if cfg.DemoSeeded {
						// Always refresh guidance (picks up latest demo guidance text)
						_ = scaffold.UpdateDemoGuidance(cwd)
						fmt.Fprintf(os.Stderr, "Demo data already seeded. Guidance refreshed.\nTo re-seed files, delete _demo/ and set demo_seeded: false in gtms.config.\n")
						return nil
					}
					demoResult, err := scaffold.DemoSeed(cwd)
					if err != nil {
						output.Errorf(err.Error(), "")
						return output.AsDisplayed(err)
					}
					if guidanceOffFlag {
						cfg2, loadErr := config.LoadFromFile(configPath)
						if loadErr == nil {
							cfg2.Guidance = false
							_ = config.WriteConfig(configPath, cfg2)
						}
					}
					formatDemoOutput(cwd, demoResult, !guidanceOffFlag)
					return nil
				}

				if !configExists || forceFlag {
					// No config (or --force) -- run full init first with bats preset, then seed demo
					name := nameFlag
					if name == "" {
						name = filepath.Base(cwd)
					}
					repo := repoFlag
					if repo == "" {
						repo = git.InferRepo(ctx, cwd, "org/repo-name")
					}

					// Warn if not at git root
					gitRoot, grErr := git.ProjectRoot(ctx, cwd)
					if grErr == nil {
						cwdClean := filepath.Clean(cwd)
						rootClean := filepath.Clean(gitRoot)
						if !strings.EqualFold(cwdClean, rootClean) {
							fmt.Fprintf(os.Stderr, "%s You are not at the git repository root. GTMS will be initialised here, not at %s.\n",
								output.IconWarning, rootClean)
						}
					}

					initOpts := scaffold.Options{
						ProjectRoot: cwd,
						Name:        name,
						Repo:        repo,
						// BUG-111 AC #31: demo init must NOT silently install BATS or
						// Playwright preset assets. Use the manual preset so the demo
						// path provides core scaffolding without framework runner files;
						// demo brings its own _demo/adapters/ scripts.
						Preset:      scaffold.PresetManual,
						Force:       forceFlag,
					}
					_, err := scaffold.Init(initOpts)
					if err != nil {
						output.Errorf(err.Error(), "")
						return output.AsDisplayed(err)
					}

					demoResult, err := scaffold.DemoSeed(cwd)
					if err != nil {
						output.Errorf(err.Error(), "")
						return output.AsDisplayed(err)
					}
					if guidanceOffFlag {
						cfg, loadErr := config.LoadFromFile(configPath)
						if loadErr == nil {
							cfg.Guidance = false
							_ = config.WriteConfig(configPath, cfg)
						}
					}
					formatDemoOutput(cwd, demoResult, !guidanceOffFlag)
					return nil
				}
			}

			// (S4 detection hoisted above --demo/--force branches)

			// Validate: no existing config unless --force
			if _, err := os.Stat(configPath); err == nil && !forceFlag {
				output.Errorf("gtms.config already exists in this directory.",
					"Use --force to overwrite, or edit the existing config.")
				return output.AsDisplayed(fmt.Errorf("gtms.config already exists"))
			}

			// Validate: no ancestor GTMS project unless --force
			if ancestorRoot, err := config.FindProjectRoot(cwd); err == nil && !forceFlag {
				cwdAbs := filepath.Clean(cwd)
				ancestorClean := filepath.Clean(ancestorRoot)
				if !strings.EqualFold(cwdAbs, ancestorClean) {
					output.Errorf(
						fmt.Sprintf("A GTMS project already exists at %s.", ancestorClean),
						"Use --force to create a nested project, or run 'gtms init' from the project root.",
					)
					return output.AsDisplayed(fmt.Errorf("ancestor GTMS project exists at %s", ancestorClean))
				}
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
			if !scaffold.IsValidPreset(presetFlag) {
				output.Errorf(
					fmt.Sprintf("Unknown preset '%s'.", presetFlag),
					fmt.Sprintf("Valid presets: %s", strings.Join(scaffold.ValidPresets(), ", ")),
				)
				return output.AsDisplayed(fmt.Errorf("unknown preset '%s'", presetFlag))
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
				Preset:      presetFlag,
				Force:       forceFlag,
			}

			result, err := scaffold.Init(opts)
			if err != nil {
				output.Errorf(err.Error(), "")
				return output.AsDisplayed(err)
			}

			// If guidance flag was passed during first init, update the config
			if guidanceOffFlag || guidanceOnFlag {
				cfg, loadErr := config.LoadFromFile(configPath)
				if loadErr == nil {
					cfg.Guidance = !guidanceOffFlag
					_ = config.WriteConfig(configPath, cfg)
				}
			}

			// Determine guidance state for this output
			isGuidanceEnabled := !guidanceOffFlag

			// Print success output
			formatInitOutput(cwd, name, repo, presetFlag, result, isGuidanceEnabled)

			return nil
		},
	}

	cmd.Flags().StringVar(&nameFlag, "name", "", "Project name (default: directory name)")
	cmd.Flags().StringVar(&repoFlag, "repo", "", "Repository path, e.g. org/repo (default: inferred from git remote)")
	cmd.Flags().StringVar(&presetFlag, "preset", "", "Workflow preset: manual, bats, playwright")
	cmd.Flags().BoolVar(&presetsFlag, "presets", false, "List available presets and exit")
	cmd.Flags().BoolVar(&forceFlag, "force", false, "Overwrite existing config or allow nested project creation")
	cmd.Flags().BoolVar(&guidanceOffFlag, "guidance-off", false, "Disable post-command guidance messages (updates gtms.config)")
	cmd.Flags().BoolVar(&guidanceOnFlag, "guidance-on", false, "Re-enable post-command guidance messages (updates gtms.config)")
	cmd.Flags().BoolVar(&demoFlag, "demo", false, "Seed demo data for learning the GTMS pipeline (not combinable with --preset)")

	return cmd
}

// printPresetList writes the preset listing to the given writer.
func printPresetList(w *os.File) {
	fmt.Fprintf(w, "Available presets:\n\n")
	for _, name := range scaffold.ValidPresets() {
		desc := scaffold.PresetDescriptions[name]
		fmt.Fprintf(w, "  %-12s %s\n", name, desc)
	}
	fmt.Fprintf(w, "\nUsage:\n")
	fmt.Fprintf(w, "  gtms init --preset <name>     Scaffold a GTMS project with the named preset\n")
}

// formatInitOutput prints the success summary after initialisation.
func formatInitOutput(root, name, repo, preset string, result *scaffold.Result, guidanceEnabled bool) {
	// AC #26: Preset: <name> within first three output lines, exact match
	// (BUG-111 specs tc-0283b006, tc-a575fc7a, tc-dbf2bec3 all require a line
	// that exactly matches "Preset: <name>", no leading indent).
	fmt.Printf("%s GTMS initialised in %s\n", output.IconComplete, root)
	fmt.Printf("Preset: %s\n", preset)
	fmt.Printf("Project: %s (%s)\n", name, repo)
	fmt.Println()

	// S3 awareness: when items were restored in a pre-existing gtms/, show them first
	if len(result.FilesRestored) > 0 {
		fmt.Println("  Reconstructed:")
		for _, f := range result.FilesRestored {
			fmt.Printf("    %s\n", f)
		}
		fmt.Println()
	}

	// ENH-186: gtms/skills/, gtms/test/templates/ and gtms/AGENTS-SNIPPET.md are
	// listed here because the stderr enumeration that used to be their only
	// mention is now a count summary. They sit with the rest of the gtms/ tree
	// rather than after the .vscode entries, so the block reads in one group.
	fmt.Println("  Created:")
	fmt.Println("    gtms.config")
	fmt.Println("    gtms/ (pipeline parent directory)")
	fmt.Println("    gtms/.gtms-root (sentinel)")
	fmt.Println("    gtms/AGENTS-SNIPPET.md")
	fmt.Println("    gtms/tasks/ (5 status directories)")
	fmt.Println("    gtms/test/cases/ (with guides)")
	fmt.Println("    gtms/test/guides/gtms-test-case-authoring-guide.md")
	fmt.Println("    gtms/test/templates/manual-testcase.template.md")
	fmt.Println("    gtms/test/templates/agent-testcase.template.md")
	fmt.Println("    gtms/skills/ (starter agent skills)")
	fmt.Println("    gtms/automation/ (wiring, specs)")

	// BUG-149: Preset-conditional automate skeleton templates.
	// Driven off result.FilesCreated so manual preset (which creates nothing
	// under this prefix) prints nothing, satisfying tc-0b82d0e3 / tc-2fb023a4.
	for _, f := range result.FilesCreated {
		if strings.HasPrefix(f, "gtms/automation/templates/") {
			fmt.Printf("    %s\n", f)
		}
	}

	fmt.Println("    gtms/scripts/")
	fmt.Println("    gtms/execution/")

	// Manual scaffold entries (always created on a fresh init).
	fmt.Println("    gtms/manual/records/")
	fmt.Println("    gtms/manual/templates/")
	fmt.Println("    gtms/manual/templates/manual-result.template.yaml")
	fmt.Println("    gtms/manual/templates/agent-result.template.yaml")
	fmt.Println("    gtms/schemas/")
	fmt.Println("    gtms/schemas/manual-result.schema.json")
	fmt.Println("    gtms/tasks/.README.md")

	// ENH-160: Common adapter scripts (both roles, renamed to *-script convention)
	fmt.Println("    gtms/adapters/manual-create-script.sh")
	fmt.Println("    gtms/adapters/agent-create-script.sh")
	fmt.Println("    gtms/adapters/manual-prime-script.sh")
	fmt.Println("    gtms/adapters/agent-prime-script.sh")
	fmt.Println("    gtms/adapters/manual-execute-script.sh")
	fmt.Println("    gtms/adapters/agent-execute-script.sh")

	// Preset-owned assets (framework runners etc.)
	commonScripts := map[string]bool{
		"gtms/adapters/manual-create-script.sh":  true,
		"gtms/adapters/agent-create-script.sh":   true,
		"gtms/adapters/manual-prime-script.sh":   true,
		"gtms/adapters/agent-prime-script.sh":    true,
		"gtms/adapters/manual-execute-script.sh": true,
		"gtms/adapters/agent-execute-script.sh":  true,
	}
	for _, f := range result.FilesCreated {
		if strings.HasPrefix(f, "gtms/adapters/") && !commonScripts[f] {
			fmt.Printf("    %s\n", f)
		}
	}

	// VSCode entries -- print explicitly only when actually created
	for _, f := range []string{".vscode/settings.json", ".vscode/extensions.json", ".vscode/gtms.code-snippets"} {
		if containsString(result.FilesCreated, f) {
			fmt.Printf("    %s\n", f)
		}
	}

	// List companion snippet files if the companion path was taken.
	for _, f := range result.FilesCreated {
		if strings.HasPrefix(f, ".vscode/gtms-") && strings.HasSuffix(f, ".snippet") {
			fmt.Printf("    %s\n", f)
		}
	}

	// Show skipped files
	if len(result.FilesSkipped) > 0 {
		fmt.Println()
		fmt.Println("  Skipped (already exist):")
		for _, f := range result.FilesSkipped {
			fmt.Printf("    %s\n", f)
		}
	}

	// Show warnings (actionable; rendered with ! glyph).
	if len(result.Warnings) > 0 {
		fmt.Println()
		for _, w := range result.Warnings {
			fmt.Fprintf(os.Stderr, "%s %s\n", output.IconWarning, w)
		}
	}

	// Show informational notes (rendered with "Note:" prefix, no glyph).
	if len(result.Notes) > 0 {
		if len(result.Warnings) == 0 {
			fmt.Println()
		}
		for _, n := range result.Notes {
			fmt.Fprintf(os.Stderr, "  Note: %s\n", n)
		}
	}

	// Print guidance block
	printGuidance(os.Stderr, "init", whatHappenedInit(result), root, guidanceEnabled)

	// ENH-183: Trailing CTA pointer to the agent-instructions snippet.
	// This is the absolute last line of init output -- after guidance footer --
	// so the eye lands on it. Uses IconPending (neutral glyph) to distinguish
	// from success/warning/error lines.
	fmt.Fprintf(os.Stderr, "\n%s Wire your AI agent in: paste gtms/AGENTS-SNIPPET.md into your CLAUDE.md / AGENTS.md\n", output.IconPending)
}

// formatDemoOutput prints the success summary after demo seeding.
func formatDemoOutput(root string, result *scaffold.DemoSeedResult, guidanceEnabled bool) {
	fmt.Printf("%s Demo data seeded in %s\n", output.IconComplete, root)
	fmt.Println()

	if len(result.FilesCreated) > 0 {
		fmt.Println("  Created:")
		for _, f := range result.FilesCreated {
			fmt.Printf("    %s\n", f)
		}
	}
	if len(result.FilesSkipped) > 0 {
		fmt.Println("  Skipped (already exist):")
		for _, f := range result.FilesSkipped {
			fmt.Printf("    %s\n", f)
		}
	}
	if result.ConfigModified {
		fmt.Println("  Updated: gtms.config (demo adapters added)")
	}
	if result.GuidanceModified {
		fmt.Println("  Updated: .gtms/guidance.yaml (demo guidance)")
	}

	// ENH-186: count-based summary instead of per-file enumeration.
	// The demo Created: block and Updated: lines above are the authoritative
	// listing; this summary names no path.
	whatHappened := fmt.Sprintf("Created %d demo files.", len(result.FilesCreated))

	printGuidance(os.Stderr, "init", whatHappened, root, guidanceEnabled)
}

// containsString reports whether s is present in slice.
func containsString(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// detectFlatLayout returns the flat v0.1.0 directories present in root.
// An empty slice means no flat layout detected.
func detectFlatLayout(root string) []string {
	flatDirs := []string{"test-cases", "test-automation", "test-tasks", "test-execution"}
	var present []string
	for _, d := range flatDirs {
		if fi, err := os.Stat(filepath.Join(root, d)); err == nil && fi.IsDir() {
			present = append(present, d)
		}
	}
	return present
}

// flatToNested maps flat v0.1.0 directory names to their nested equivalents.
var flatToNested = map[string]string{
	"test-cases":      "gtms/test/cases",
	"test-automation": "gtms/automation",
	"test-tasks":      "gtms/tasks",
	"test-execution":  "gtms/execution",
}

// flatLayoutErrorMessage builds the user-facing error message and hint for S4 detection.
// goos is passed as a parameter (typically runtime.GOOS) so tests can exercise both arms.
func flatLayoutErrorMessage(presentDirs []string, goos string) (string, string) {
	msg := fmt.Sprintf("Flat v0.1.0 layout detected (%s present).",
		strings.Join(presentDirs, ", "))

	var recipe strings.Builder
	recipe.WriteString("GTMS v0.2+ uses gtms/test/cases/, gtms/automation/ etc.\n    To migrate:\n")
	for _, d := range presentDirs {
		nested := flatToNested[d]
		fmt.Fprintf(&recipe, "      git mv %s %s\n", d, nested)
	}

	if goos == "windows" {
		recipe.WriteString("      New-Item -ItemType File gtms\\.gtms-root -Force\n")
	} else {
		recipe.WriteString("      mkdir -p gtms && touch gtms/.gtms-root\n")
	}
	recipe.WriteString("    Then re-run 'gtms init'.\n")
	recipe.WriteString("    GTMS will preserve your migrated contents when you re-run init.")

	return msg, recipe.String()
}
