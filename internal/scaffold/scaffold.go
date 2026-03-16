package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
)

// Options configures the Init scaffolding operation.
type Options struct {
	ProjectRoot string // absolute path to the directory to scaffold in
	Name        string // project name for config
	Repo        string // repository path (e.g. "org/repo")
	Preset      string // adapter preset: "minimal", "claude", "github"
	Force       bool   // overwrite existing gtms.config if present
}

// Result describes what Init created.
type Result struct {
	ConfigPath   string   // path to the generated gtms.config
	DirsCreated  []string // directories created (relative to project root)
	FilesCreated []string // files created (relative to project root)
}

// Init scaffolds a GTMS project in the given directory.
// It creates directories, writes a config file, and optionally
// writes prompt templates and adapter stubs based on the preset.
func Init(opts Options) (*Result, error) {
	if !IsValidPreset(opts.Preset) {
		return nil, fmt.Errorf("unknown adapter preset '%s'. Valid presets: minimal, claude, github", opts.Preset)
	}

	// Check for existing config
	configPath := filepath.Join(opts.ProjectRoot, "gtms.config")
	if _, err := os.Stat(configPath); err == nil && !opts.Force {
		return nil, fmt.Errorf("gtms.config already exists in this directory")
	}

	result := &Result{}

	// Create directories
	dirs, err := CreateDirectories(opts.ProjectRoot, opts.Preset)
	if err != nil {
		return nil, fmt.Errorf("creating directories: %w", err)
	}
	result.DirsCreated = dirs

	// Write config
	cfgPath, err := WriteConfig(opts.ProjectRoot, opts.Name, opts.Repo, opts.Preset)
	if err != nil {
		return nil, fmt.Errorf("writing config: %w", err)
	}
	result.ConfigPath = cfgPath
	result.FilesCreated = append(result.FilesCreated, "gtms.config")

	// Write prompt templates for claude and github presets
	if opts.Preset == PresetClaude || opts.Preset == PresetGitHub {
		files, err := WritePromptTemplates(opts.ProjectRoot)
		if err != nil {
			return nil, fmt.Errorf("writing prompt templates: %w", err)
		}
		result.FilesCreated = append(result.FilesCreated, files...)
	}

	// Write starter guides for claude and github presets
	if opts.Preset == PresetClaude || opts.Preset == PresetGitHub {
		files, err := WriteStarterGuides(opts.ProjectRoot)
		if err != nil {
			return nil, fmt.Errorf("writing starter guides: %w", err)
		}
		result.FilesCreated = append(result.FilesCreated, files...)
	}

	// Write test-cases README for claude and github presets
	if opts.Preset == PresetClaude || opts.Preset == PresetGitHub {
		readmePath := filepath.Join(opts.ProjectRoot, "test-cases", "README.md")
		if err := os.WriteFile(readmePath, []byte(testCasesReadme), 0o644); err != nil {
			return nil, fmt.Errorf("writing test-cases/README.md: %w", err)
		}
		result.FilesCreated = append(result.FilesCreated, "test-cases/README.md")
	}

	// Write adapter stubs for github preset
	if opts.Preset == PresetGitHub {
		files, err := WriteAdapterStubs(opts.ProjectRoot)
		if err != nil {
			return nil, fmt.Errorf("writing adapter stubs: %w", err)
		}
		result.FilesCreated = append(result.FilesCreated, files...)
	}

	return result, nil
}

// CreateDirectories creates the GTMS directory structure with .gitkeep files.
// The preset determines whether prompt directories are created.
// Returns the list of directories created (relative paths).
func CreateDirectories(root string, preset string) ([]string, error) {
	dirs := []string{
		"test-tasks/pending",
		"test-tasks/in-progress",
		"test-tasks/in-review",
		"test-tasks/complete",
		"test-tasks/failed",
		"test-cases",
		"test-cases/guides",
		"test-automation/records",
		"test-automation/specs",
		"test-execution",
	}

	// Add prompt dirs for claude and github presets
	if preset == PresetClaude || preset == PresetGitHub {
		dirs = append(dirs, "test-cases/prompts", "test-automation/prompts")
	}

	// Add adapters dir for github preset
	if preset == PresetGitHub {
		dirs = append(dirs, "adapters")
	}

	created := []string{}
	for _, dir := range dirs {
		absDir := filepath.Join(root, filepath.FromSlash(dir))
		if err := os.MkdirAll(absDir, 0o755); err != nil {
			return nil, fmt.Errorf("creating directory %s: %w", dir, err)
		}
		created = append(created, dir)

		// Write .gitkeep in leaf directories
		gitkeep := filepath.Join(absDir, ".gitkeep")
		if _, err := os.Stat(gitkeep); os.IsNotExist(err) {
			if err := os.WriteFile(gitkeep, []byte(""), 0o644); err != nil {
				return nil, fmt.Errorf("writing .gitkeep in %s: %w", dir, err)
			}
		}
	}

	return created, nil
}

// WriteConfig generates and writes a gtms.config file based on the preset.
// Returns the absolute path to the config file.
func WriteConfig(root, name, repo, preset string) (string, error) {
	content := configForPreset(name, repo, preset)
	configPath := filepath.Join(root, "gtms.config")

	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("writing gtms.config: %w", err)
	}

	return configPath, nil
}

// WritePromptTemplates writes starter prompt templates to test-cases/prompts/
// and test-automation/prompts/. Returns the list of files created (relative paths).
func WritePromptTemplates(root string) ([]string, error) {
	templates := []struct {
		path    string
		content string
	}{
		{"test-cases/prompts/create-standard.md", promptCreateStandard},
		{"test-automation/prompts/automate-standard.md", promptAutomateStandard},
	}

	var created []string
	for _, tmpl := range templates {
		absPath := filepath.Join(root, filepath.FromSlash(tmpl.path))
		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			return nil, fmt.Errorf("creating directory for %s: %w", tmpl.path, err)
		}
		if err := os.WriteFile(absPath, []byte(tmpl.content), 0o644); err != nil {
			return nil, fmt.Errorf("writing %s: %w", tmpl.path, err)
		}
		created = append(created, tmpl.path)
	}

	return created, nil
}

// WriteStarterGuides writes a starter test case guide to test-cases/guides/.
// Returns the list of files created (relative paths).
func WriteStarterGuides(root string) ([]string, error) {
	guides := []struct {
		path    string
		content string
	}{
		{"test-cases/guides/test-case-template.md", starterGuideContent},
	}

	var created []string
	for _, g := range guides {
		absPath := filepath.Join(root, filepath.FromSlash(g.path))
		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			return nil, fmt.Errorf("creating directory for %s: %w", g.path, err)
		}
		if err := os.WriteFile(absPath, []byte(g.content), 0o644); err != nil {
			return nil, fmt.Errorf("writing %s: %w", g.path, err)
		}
		created = append(created, g.path)
	}

	return created, nil
}

// WriteAdapterStubs writes placeholder adapter scripts to the adapters/ directory.
// Returns the list of files created (relative paths).
func WriteAdapterStubs(root string) ([]string, error) {
	adaptersDir := filepath.Join(root, "adapters")
	if err := os.MkdirAll(adaptersDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating adapters directory: %w", err)
	}

	stubs := map[string]string{
		"adapters/github-create.sh":         adapterStubScript("github-create", "create"),
		"adapters/github-create-status.sh":  adapterStatusStubScript("github-create"),
		"adapters/github-automate.sh":       adapterStubScript("github-automate", "automate"),
		"adapters/github-automate-status.sh": adapterStatusStubScript("github-automate"),
		"adapters/github-execute.sh":        adapterStubScript("github-execute", "execute"),
		"adapters/github-execute-status.sh": adapterStatusStubScript("github-execute"),
	}

	var created []string
	for relPath, content := range stubs {
		absPath := filepath.Join(root, filepath.FromSlash(relPath))
		if err := os.WriteFile(absPath, []byte(content), 0o755); err != nil {
			return nil, fmt.Errorf("writing %s: %w", relPath, err)
		}
		created = append(created, relPath)
	}

	return created, nil
}
