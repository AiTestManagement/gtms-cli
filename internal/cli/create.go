package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/adrg/frontmatter"
	"github.com/aitestmanagement/gtms-cli/internal/adapter"
	"github.com/aitestmanagement/gtms-cli/internal/git"
	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/output"
	"github.com/spf13/cobra"
)

// validNamePattern matches only safe characters for the test case name argument.
var validNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// newCreateCmd builds the 'gtms create' command with its flags and subcommands.
func newCreateCmd() *cobra.Command {
	var adapterFlag string
	var focusFlag string
	var contextFileFlag string
	var referenceFlag string

	cmd := &cobra.Command{
		Use:   "create <folder> [name]",
		Short: "Create test cases from requirements",
		Long: `Create test cases by delegating to a configured adapter. The adapter
receives a prompt assembled from your template, guides, and CLI flags, then
generates test case specs in gtms/cases/<folder>/. See USER-GUIDE.md for details
on prompt assembly, input layers, and data flow.

Examples:
  gtms create bug-022 --context-file PRPs/bugs/BUG-022.md --reference BUG-022
  gtms create payments/checkout --reference REQ-123 --focus "guest checkout"
  gtms create sprint-14 --adapter github-create
  gtms create login user-can-login  — create a named test case skeleton`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cfg := GetConfig()
			root := GetProjectRoot()

			// Validate and clean folder argument
			folder, err := validateFolderArg(args[0])
			if err != nil {
				output.Errorf(err.Error(), "")
				return output.AsDisplayed(err)
			}

			// Validate: git repo exists
			if !git.IsRepo(ctx, root) {
				output.Errorf("Not a git repository.", "Initialize a git repo with 'git init'.")
				return output.AsDisplayed(fmt.Errorf("not a git repository"))
			}

			// Validate: folder structure exists
			if err := validateFolderStructure(root); err != nil {
				return err
			}

			// Read context file if provided
			var contextContent string
			var absContextPath string
			if contextFileFlag != "" {
				var err error
				absContextPath, err = filepath.Abs(contextFileFlag)
				if err != nil {
					output.Errorf(fmt.Sprintf("Invalid context file path: %s", contextFileFlag),
						"Provide a valid file path.")
					return output.AsDisplayed(fmt.Errorf("invalid context file path: %w", err))
				}
				data, err := os.ReadFile(absContextPath)
				if err != nil {
					output.Errorf(fmt.Sprintf("Cannot read context file: %s", contextFileFlag),
						"Check that the file exists and is readable.")
					return output.AsDisplayed(fmt.Errorf("reading context file: %w", err))
				}
				contextContent = string(data)
			}

			// Warn if --reference looks like a file path but doesn't exist
			if referenceFlag != "" && looksLikeFilePath(referenceFlag) {
				if _, err := os.Stat(referenceFlag); os.IsNotExist(err) {
					output.Warnf(fmt.Sprintf("Reference looks like a file path but does not exist: %s", sanitizeForError(referenceFlag)))
					fmt.Fprintf(os.Stderr, "    Proceeding anyway — the adapter will receive this value as-is.\n")
				}
			}

			// Resolve adapter
			resolved, err := adapter.Resolve(cfg, "create", adapterFlag)
			if err != nil {
				output.Errorf(err.Error(), "Check your gtms.config file.")
				return output.AsDisplayed(err)
			}

			if IsVerbose() {
				fmt.Fprintf(os.Stderr, "Resolved adapter: %s (tier %d, mode %s)\n",
					resolved.Name, resolved.Tier, resolved.Mode)
			}

			// Auto-create the target folder under gtms/cases/ (only when adapter doesn't override output-dir)
			if resolved.Config.OutputDir == "" {
				paths := layout.Current()
				outputDir := filepath.Join(root, paths.Cases, folder)
				if err := os.MkdirAll(outputDir, 0755); err != nil {
					return fmt.Errorf("creating output directory: %w", err)
				}
				fmt.Printf("  → Target folder: %s/%s/\n", paths.Cases, folder)
			} else {
				fmt.Printf("  → Output directory: %s (from adapter config)\n", resolved.Config.OutputDir)
			}

			// Extract and validate optional name from second positional arg
			var nameArg string
			if len(args) > 1 {
				nameArg = args[1]
				if !validNamePattern.MatchString(nameArg) {
					msg := fmt.Sprintf("Invalid name '%s'. Only letters, numbers, dashes, and underscores are allowed.", nameArg)
					output.Errorf(msg, "Use a name like: user-can-login")
					return output.AsDisplayed(fmt.Errorf(msg))
				}
			}

			// Invoke adapter
			flags := adapter.CommandFlags{
				Adapter:     adapterFlag,
				Focus:       focusFlag,
				ContextFile: absContextPath,
				Context:     contextContent,
				Folder:      folder,
				Reference:   referenceFlag,
				Name:        nameArg,
			}

			// Start spinner for sync adapters
			var spinner *output.Spinner
			if resolved.Mode == "sync" {
				spinner = output.NewSpinner(os.Stderr, fmt.Sprintf("Running %s...", resolved.Name))
				spinner.Start()
			}

			result, err := adapter.InvokeWithRoot(ctx, root, cfg, resolved, folder, flags)

			// Stop spinner BEFORE any output
			if spinner != nil {
				spinner.Stop()
			}

			if err != nil {
				return err
			}

			// Format output
			formatCreateOutput(result, root)

			// Propagate adapter-level failures (e.g. BUG-038 spec validation
			// errors) as a non-zero exit code for CI observability. The error
			// message has already been rendered via formatCreateOutput, so use
			// output.AsDisplayed to suppress cobra's "Error:" re-print.
			if result != nil && result.Status == "error" {
				return output.AsDisplayed(fmt.Errorf("%s", result.Summary))
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&adapterFlag, "adapter", "", "Adapter to use (overrides default)")
	cmd.Flags().StringVar(&focusFlag, "focus", "", "Focus area within the source document")
	cmd.Flags().StringVar(&contextFileFlag, "context-file", "", "Path to context file (essential for file-based requirements)")
	cmd.Flags().StringVar(&referenceFlag, "reference", "", "Reference identifier (e.g. REQ-001, BUG-022)")

	// Add create status subcommand
	cmd.AddCommand(newCreateStatusCmd())

	return cmd
}

// validateFolderStructure checks that the required GTMS directories exist.
func validateFolderStructure(root string) error {
	paths := layout.Current()
	required := []string{paths.Tasks, paths.Cases}
	for _, dir := range required {
		path := filepath.Join(root, dir)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			msg := fmt.Sprintf("Required directory '%s' not found.", dir)
			output.Errorf(msg, fmt.Sprintf("Create the GTMS folder structure: %s/, %s/, %s/", paths.Tasks, paths.Cases, paths.Automation))
			return output.AsDisplayed(fmt.Errorf(msg))
		}
	}
	return nil
}

// maxInlineCount is the maximum number of test cases listed inline in create output.
// Beyond this threshold, only the first maxInlineCount are shown with a "...and N more" hint.
const maxInlineCount = 5

// maxTitleLen is the maximum length of a test case title before truncation.
const maxTitleLen = 72

// formatCreateOutput prints the result of a create command.
// ENH-120: headline surfaces artefacts (TC IDs), not internal task filenames.
func formatCreateOutput(res *adapter.InvokeResult, projectRoot string) {
	if res.Status == "error" {
		output.Errorf(
			fmt.Sprintf("Task failed: %s", res.Filename),
			res.Summary,
		)
		printCommandGuidance("create", whatHappenedCreate(res))
		return
	}

	// ENH-120: artefact-focused headline — surface TC IDs, not task filenames.
	if len(res.ArtifactPaths) > 0 {
		printCreatedHeadline(res.ArtifactPaths, res.Target, projectRoot)
	} else if len(res.Warnings) > 0 && res.ArtifactCount == 0 {
		fmt.Printf("  %s Completed with warnings for %s\n", output.IconWarning, res.Target)
	} else if res.ArtifactCount > 0 {
		fmt.Printf("  %s Created %d files for %s\n", output.IconComplete, res.ArtifactCount, res.Target)
	} else {
		fmt.Printf("  %s Created test case for %s\n", output.IconComplete, res.Target)
	}

	fmt.Printf("    Adapter: %s (%s)\n", res.Adapter, res.Mode)

	// ENH-120: task ID and branch demoted to verbose-only output
	if IsVerbose() {
		fmt.Fprintf(os.Stderr, "    Task: %s\n", res.TaskID)
		fmt.Fprintf(os.Stderr, "    Branch: %s\n", res.Branch)
	}

	if res.Mode == "async" {
		fmt.Println("    Check progress: gtms create status")
	}

	for _, w := range res.Warnings {
		output.Warnf(w)
	}

	printCommandGuidance("create", whatHappenedCreate(res))
}

// tcInfo holds a test case ID and title extracted from frontmatter.
type tcInfo struct {
	id    string
	title string
}

// printCreatedHeadline renders the artefact-focused headline for create output.
// ENH-120: the headline IS the TC list — TC IDs are the primary content,
// not a secondary detail. For bulk creates exceeding maxInlineCount, a truncated
// list with a follow-up hint is shown.
func printCreatedHeadline(paths []string, target string, projectRoot string) {
	entries := make([]tcInfo, 0, len(paths))
	for _, relPath := range paths {
		id, title := readTCFrontmatter(projectRoot, relPath)
		if id != "" {
			entries = append(entries, tcInfo{id: id, title: title})
		}
	}
	if len(entries) == 0 {
		fmt.Printf("  %s Created test case for %s\n", output.IconComplete, target)
		return
	}

	n := len(entries)
	layoutPaths := layout.Current()
	casesDir := fmt.Sprintf("%s/%s", layoutPaths.Cases, target)
	if n <= maxInlineCount {
		label := "test case"
		if n > 1 {
			label = "test cases"
		}
		fmt.Printf("  %s Created %d %s in %s/:\n", output.IconComplete, n, label, casesDir)
		for _, e := range entries {
			fmt.Printf("      %s  %s\n", e.id, truncateTitle(e.title))
		}
	} else {
		fmt.Printf("  %s Created %d test cases in %s/:\n", output.IconComplete, n, casesDir)
		for _, e := range entries[:maxInlineCount] {
			fmt.Printf("      %s  %s\n", e.id, truncateTitle(e.title))
		}
		fmt.Printf("      ...and %d more. Run `gtms status %s` to see all.\n", n-maxInlineCount, target)
	}
}

// readTCFrontmatter reads a test case file and extracts its test_case_id and title.
// On any error, it falls back to extracting the tc-id from the filename.
func readTCFrontmatter(projectRoot, relPath string) (id, title string) {
	// Extract tc-id from filename as fallback
	base := filepath.Base(relPath)
	fallbackID := extractTCIDFromFilename(base)

	absPath := filepath.Join(projectRoot, relPath)
	f, err := os.Open(absPath)
	if err != nil {
		return fallbackID, "(no frontmatter)"
	}
	defer f.Close()

	var fm struct {
		ID    string `yaml:"test_case_id"`
		Title string `yaml:"title"`
		Name  string `yaml:"name"`
	}
	_, err = frontmatter.Parse(f, &fm)
	if err != nil || fm.ID == "" {
		return fallbackID, "(no frontmatter)"
	}

	// Prefer `title:`; fall back to `name:` when title is empty so the
	// skeleton adapter (which writes `name:` only) doesn't surface as
	// "(untitled)" in the create headline. ENH-121 finding #4.
	title = fm.Title
	if title == "" {
		title = fm.Name
	}
	if title == "" {
		title = "(untitled)"
	}
	return strings.ToLower(fm.ID), title
}

// tcIDPattern matches tc-XXXXXXXX in a filename.
var tcIDPattern = regexp.MustCompile(`(tc-[0-9a-fA-F]{8})`)

// extractTCIDFromFilename extracts a tc-XXXXXXXX pattern from a filename.
// Returns the filename base (without extension) if no pattern is found.
func extractTCIDFromFilename(filename string) string {
	if m := tcIDPattern.FindString(filename); m != "" {
		return strings.ToLower(m)
	}
	ext := filepath.Ext(filename)
	if ext != "" {
		return strings.TrimSuffix(filename, ext)
	}
	return filename
}

// truncateTitle shortens a title to maxTitleLen characters, appending "..."
// if truncation occurred.
func truncateTitle(title string) string {
	if len(title) <= maxTitleLen {
		return title
	}
	return title[:maxTitleLen-3] + "..."
}
