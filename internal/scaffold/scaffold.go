package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"gopkg.in/yaml.v3"
)

// GitignoreAction describes what ensureGitignore did to .gitignore.
type GitignoreAction string

const (
	// GitignoreCreated means .gitignore was absent and was created with .gtms/.
	GitignoreCreated GitignoreAction = "created"
	// GitignoreAppended means .gitignore existed but lacked .gtms/; the entry was appended.
	GitignoreAppended GitignoreAction = "appended"
	// GitignoreUnchanged means .gitignore already contained .gtms/; no modification was needed.
	GitignoreUnchanged GitignoreAction = "unchanged"
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
	ConfigPath      string          // path to the generated gtms.config
	DirsCreated     []string        // directories created (relative to project root)
	FilesCreated    []string        // files created (relative to project root)
	FilesSkipped    []string        // files not written because they already existed
	FilesRestored   []string        // files restored/reconstructed in a pre-existing gtms/ directory
	Warnings        []string        // ENH-135: actionable warning messages (rendered with ! glyph)
	Notes           []string        // ENH-135: informational notes (rendered with "Note:" prefix, no glyph)
	GitignoreAction GitignoreAction // what happened to .gitignore (S7/S8/S9)
}

// Init scaffolds a GTMS project in the given directory.
// It creates directories, writes a config file, and optionally
// writes prompt templates and adapter stubs based on the preset.
func Init(opts Options) (*Result, error) {
	if !IsValidPreset(opts.Preset) {
		return nil, fmt.Errorf("unknown preset '%s'. Valid presets: %s", opts.Preset, strings.Join(ValidPresets(), ", "))
	}

	// Check for existing config
	configPath := filepath.Join(opts.ProjectRoot, "gtms.config")
	if _, err := os.Stat(configPath); err == nil && !opts.Force {
		return nil, fmt.Errorf("gtms.config already exists in this directory")
	}

	result := &Result{}

	// Check if gtms/ already exists before creating directories (S3 detection).
	gtmsDir := filepath.Join(opts.ProjectRoot, "gtms")
	gtmsPreExisted := false
	if fi, err := os.Stat(gtmsDir); err == nil && fi.IsDir() {
		gtmsPreExisted = true
	}

	// ENH-135: Detect renamed-parent layout BEFORE creating directories.
	// If a sentinel already exists under a non-"gtms" name, the user has
	// a renamed-parent layout (ENH-098). We store this for the warning
	// after Init completes. Note: on `gtms init --force` in a renamed-parent
	// project, CreateDirectories will also create gtms/.gtms-root, which
	// may produce a dual-sentinel state. That is a known follow-up concern
	// (not fixed in this slice).
	var renamedParentName string
	if parentName, fpErr := config.FindParentDir(opts.ProjectRoot); fpErr == nil && parentName != "gtms" {
		renamedParentName = parentName
	}

	// Create directories
	dirs, err := CreateDirectories(opts.ProjectRoot, opts.Preset)
	if err != nil {
		return nil, fmt.Errorf("creating directories: %w", err)
	}
	result.DirsCreated = dirs

	// Create sentinel file (.gtms-root) inside the parent directory.
	// S5/D9: preserve existing non-empty sentinel content (reserved for future use).
	// S3: if sentinel is newly created in a pre-existing gtms/, track as restored.
	sentinelPath := filepath.Join(opts.ProjectRoot, "gtms", ".gtms-root") // scaffold.go always writes the canonical name; layout.InitFromParent handles post-rename discovery.
	if _, statErr := os.Stat(sentinelPath); statErr != nil {
		// Sentinel does not exist -- create it
		if err := os.WriteFile(sentinelPath, []byte(""), 0o644); err != nil {
			return nil, fmt.Errorf("creating sentinel file: %w", err)
		}
		if gtmsPreExisted {
			result.FilesRestored = append(result.FilesRestored, "gtms/.gtms-root")
		} else {
			result.FilesCreated = append(result.FilesCreated, "gtms/.gtms-root")
		}
	}
	// If sentinel already exists, leave it untouched (S5 preservation)

	// Ensure .gtms/ is listed in .gitignore (idempotent)
	giAction, err := ensureGitignore(opts.ProjectRoot)
	if err != nil {
		return nil, fmt.Errorf("updating .gitignore: %w", err)
	}
	result.GitignoreAction = giAction

	// Write config
	cfgPath, err := WriteConfig(opts.ProjectRoot, opts.Name, opts.Repo, opts.Preset)
	if err != nil {
		return nil, fmt.Errorf("writing config: %w", err)
	}
	result.ConfigPath = cfgPath
	result.FilesCreated = append(result.FilesCreated, "gtms.config")

	// Write starter guides (all presets -- ENH-119).
	// The enriched test-case-template.md is the authoring reference for the
	// skeleton create adapter, which itself ships under every preset, so the
	// guide must travel with it.
	{
		files, err := WriteStarterGuides(opts.ProjectRoot)
		if err != nil {
			return nil, fmt.Errorf("writing starter guides: %w", err)
		}
		result.FilesCreated = append(result.FilesCreated, files...)
	}

	// Write skeleton create adapter script (all presets)
	// BUG-053: adapters nest under gtms/ for ENH-098 three-item root footprint
	skeletonPath := filepath.Join(opts.ProjectRoot, "gtms", "adapters", "create-skeleton.sh")
	if _, err := os.Stat(skeletonPath); os.IsNotExist(err) {
		if err := os.WriteFile(skeletonPath, []byte(skeletonCreateScript), 0o755); err != nil {
			return nil, fmt.Errorf("writing gtms/adapters/create-skeleton.sh: %w", err)
		}
		result.FilesCreated = append(result.FilesCreated, "gtms/adapters/create-skeleton.sh")
	} else {
		result.FilesSkipped = append(result.FilesSkipped, "gtms/adapters/create-skeleton.sh")
	}

	// ENH-150: Write agent-skeleton create adapter script (all presets, dormant)
	agentSkeletonPath := filepath.Join(opts.ProjectRoot, "gtms", "adapters", "agent-skeleton.sh")
	if _, err := os.Stat(agentSkeletonPath); os.IsNotExist(err) {
		if err := os.WriteFile(agentSkeletonPath, []byte(agentSkeletonScript), 0o755); err != nil {
			return nil, fmt.Errorf("writing gtms/adapters/agent-skeleton.sh: %w", err)
		}
		result.FilesCreated = append(result.FilesCreated, "gtms/adapters/agent-skeleton.sh")
	} else {
		result.FilesSkipped = append(result.FilesSkipped, "gtms/adapters/agent-skeleton.sh")
	}

	// BUG-111 / ADR-022: Install preset-owned assets (framework-specific files).
	// BATS runner, TAP helper, Playwright runner etc. are no longer unconditional.
	if err := installPresetAssets(opts.ProjectRoot, opts.Preset, result); err != nil {
		return nil, fmt.Errorf("installing preset assets: %w", err)
	}

	// ENH-132: Write manual-prime scaffold files (all presets)
	manualTemplatePath := filepath.Join(opts.ProjectRoot, "gtms", "manual", "templates", "manual-result.template.yaml")
	writeFileIfNotExists(manualTemplatePath, []byte(manualResultTemplate), "gtms/manual/templates/manual-result.template.yaml", result)

	schemaPath := filepath.Join(opts.ProjectRoot, "gtms", "schemas", "manual-result.schema.json")
	writeFileIfNotExists(schemaPath, []byte(manualResultSchema), "gtms/schemas/manual-result.schema.json", result)

	manualPrimePath := filepath.Join(opts.ProjectRoot, "gtms", "adapters", "manual-prime.sh")
	if _, err := os.Stat(manualPrimePath); os.IsNotExist(err) {
		if err := os.WriteFile(manualPrimePath, []byte(manualPrimeScript), 0o755); err != nil {
			return nil, fmt.Errorf("writing gtms/adapters/manual-prime.sh: %w", err)
		}
		result.FilesCreated = append(result.FilesCreated, "gtms/adapters/manual-prime.sh")
	} else {
		result.FilesSkipped = append(result.FilesSkipped, "gtms/adapters/manual-prime.sh")
	}

	// ENH-133: Write manual-execute adapter script (all presets)
	manualExecutePath := filepath.Join(opts.ProjectRoot, "gtms", "adapters", "manual-execute.sh")
	writeFileIfNotExists(manualExecutePath, []byte(manualExecuteScript), "gtms/adapters/manual-execute.sh", result)

	// ENH-135: Ship gtms/tasks/.README.md warning file.
	tasksReadmePath := filepath.Join(opts.ProjectRoot, "gtms", "tasks", ".README.md")
	writeFileIfNotExists(tasksReadmePath, []byte(tasksReadmeContent), "gtms/tasks/.README.md", result)

	// ENH-132/135: Write .vscode/settings.json and extensions.json.
	// ENH-135: Companion-file behaviour -- when the user already has
	// settings.json or extensions.json, write a .snippet companion
	// instead of skipping silently.
	vscodeDir := filepath.Join(opts.ProjectRoot, ".vscode")
	if err := os.MkdirAll(vscodeDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating .vscode directory: %w", err)
	}

	vscodeSettingsPath := filepath.Join(vscodeDir, "settings.json")
	if _, statErr := os.Stat(vscodeSettingsPath); statErr == nil {
		// File exists -- write companion snippet instead
		snippetPath := filepath.Join(vscodeDir, "gtms-settings.json.snippet")
		if err := os.WriteFile(snippetPath, []byte(vscodeSettingsTemplate), 0o644); err != nil {
			return nil, fmt.Errorf("writing .vscode/gtms-settings.json.snippet: %w", err)
		}
		result.FilesSkipped = append(result.FilesSkipped, ".vscode/settings.json")
		result.FilesCreated = append(result.FilesCreated, ".vscode/gtms-settings.json.snippet")
		result.Warnings = append(result.Warnings,
			".vscode/settings.json already exists; merge the snippet at "+
				".vscode/gtms-settings.json.snippet into your settings.json to "+
				"enable schema validation.")
	} else {
		writeFileIfNotExists(vscodeSettingsPath, []byte(vscodeSettingsTemplate), ".vscode/settings.json", result)
	}

	vscodeExtensionsPath := filepath.Join(vscodeDir, "extensions.json")
	if _, statErr := os.Stat(vscodeExtensionsPath); statErr == nil {
		// File exists -- write companion snippet instead
		snippetPath := filepath.Join(vscodeDir, "gtms-extensions.json.snippet")
		if err := os.WriteFile(snippetPath, []byte(vscodeExtensionsTemplate), 0o644); err != nil {
			return nil, fmt.Errorf("writing .vscode/gtms-extensions.json.snippet: %w", err)
		}
		result.FilesSkipped = append(result.FilesSkipped, ".vscode/extensions.json")
		result.FilesCreated = append(result.FilesCreated, ".vscode/gtms-extensions.json.snippet")
		result.Warnings = append(result.Warnings,
			".vscode/extensions.json already exists; add redhat.vscode-yaml to "+
				"the recommendations array. See .vscode/gtms-extensions.json.snippet "+
				"for the snippet to merge in.")
	} else {
		writeFileIfNotExists(vscodeExtensionsPath, []byte(vscodeExtensionsTemplate), ".vscode/extensions.json", result)
	}

	// ENH-135: Ship VSCode snippet library for manual result authoring.
	// Reverses BUG-027 Finding 1 -- snippets are appropriate now that
	// manual result files are hand-edited YAML (CON-020), not skeleton-generated.
	snippetsPath := filepath.Join(vscodeDir, "gtms.code-snippets")
	writeFileIfNotExists(snippetsPath, []byte(vscodeGtmsSnippets), ".vscode/gtms.code-snippets", result)

	// Write .gtms/guidance.yaml
	guidancePath := filepath.Join(opts.ProjectRoot, ".gtms", "guidance.yaml")
	if err := os.MkdirAll(filepath.Dir(guidancePath), 0o755); err != nil {
		return nil, fmt.Errorf("creating .gtms directory: %w", err)
	}
	writeFileIfNotExists(guidancePath, []byte(DefaultGuidanceYAML), ".gtms/guidance.yaml", result)

	// ENH-135: Renamed-parent warning (fires only on InitFromParent layouts).
	if renamedParentName != "" {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("This project uses a renamed parent directory (%s). "+
				"The .vscode/settings.json schema mapping was generated for the "+
				"default 'gtms/' layout. If you open VSCode at a different workspace "+
				"root, schema validation will fall back to the inline directive on "+
				"each result file.", renamedParentName))
	}

	// ENH-135: Always-on workspace-root informational note.
	// Fires whenever settings.json or its companion was handled.
	// Stored in Notes (rendered with "Note:" prefix, no `!` warning glyph)
	// because the message is informational, not actionable.
	result.Notes = append(result.Notes,
		"The workspace .vscode/settings.json mapping assumes VSCode is opened "+
			"at this directory. The inline yaml-language-server directive on each "+
			"result file is the universal fallback.")

	// ENH-135: Red Hat YAML extension recommendation (surface b per CON-020 Decision 9).
	result.Warnings = append(result.Warnings,
		"For schema validation in VSCode, install the Red Hat YAML extension (redhat.vscode-yaml).")

	return result, nil
}

// writeFileIfNotExists writes content to absPath if the file does not already exist.
// If the file exists, it appends relPath to result.FilesSkipped.
// If it writes successfully, it appends relPath to result.FilesCreated.
func writeFileIfNotExists(absPath string, content []byte, relPath string, result *Result) {
	if _, err := os.Stat(absPath); err == nil {
		result.FilesSkipped = append(result.FilesSkipped, relPath)
		return
	}
	if err := os.WriteFile(absPath, content, 0o644); err == nil {
		result.FilesCreated = append(result.FilesCreated, relPath)
	}
}

// installPresetAssets writes framework-specific files owned by the given preset.
// BUG-111 / ADR-022: framework assets are deliberate preset policy, not unconditional.
func installPresetAssets(projectRoot, preset string, result *Result) error {
	assets, ok := PresetAssets[preset]
	if !ok {
		return nil // no assets for this preset
	}
	for _, asset := range assets {
		absPath := filepath.Join(projectRoot, filepath.FromSlash(asset.RelPath))
		// Ensure parent directory exists (e.g. gtms/adapters/lib/)
		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			return fmt.Errorf("creating directory for %s: %w", asset.RelPath, err)
		}
		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			if err := os.WriteFile(absPath, []byte(asset.Content), asset.Perm); err != nil {
				return fmt.Errorf("writing %s: %w", asset.RelPath, err)
			}
			result.FilesCreated = append(result.FilesCreated, asset.RelPath)
		} else {
			result.FilesSkipped = append(result.FilesSkipped, asset.RelPath)
		}
	}
	return nil
}

// CreateDirectories creates the GTMS directory structure with .gitkeep files.
// Returns the list of directories created (relative paths).
//
// ENH-098: These directory name literals are scaffolded into consumer projects
// under the nested gtms/ parent layout. The sentinel file (.gtms-root) is
// created inside the parent to enable rename-resilient discovery.
func CreateDirectories(root string, preset string) ([]string, error) {
	dirs := []string{
		"gtms/tasks/pending",
		"gtms/tasks/in-progress",
		"gtms/tasks/in-review",
		"gtms/tasks/complete",
		"gtms/tasks/error",
		"gtms/cases",
		"gtms/cases/guides",
		// CON-023 / ENH-145: wiring/ replaces the legacy records/ directory.
		// Wiring files are tracked (.gitignore must NOT exclude this dir).
		"gtms/automation/wiring",
		"gtms/automation/specs",
		"gtms/scripts",
		"gtms/execution",
	}

	// ENH-132: Manual testing and schema directories (all presets)
	dirs = append(dirs,
		"gtms/manual/records",
		"gtms/manual/templates",
		"gtms/schemas",
	)

	// All presets ship at least one adapter script (create-skeleton.sh)
	// BUG-053: adapters nest under gtms/ for ENH-098 three-item root footprint
	// BUG-111: adapters/lib/ is now created by preset asset installation, not here
	dirs = append(dirs, "gtms/adapters")

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

// WritePromptTemplates writes starter prompt templates to gtms/cases/prompts/
// and gtms/automation/prompts/. Returns the list of files created (relative paths).
func WritePromptTemplates(root string) ([]string, error) {
	templates := []struct {
		path    string
		content string
	}{
		{"gtms/cases/prompts/create-standard.md", promptCreateStandard},
		{"gtms/automation/prompts/automate-standard.md", promptAutomateStandard},
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

// WriteStarterGuides writes a starter test case guide to gtms/cases/guides/.
// Returns the list of files created (relative paths).
func WriteStarterGuides(root string) ([]string, error) {
	guides := []struct {
		path    string
		content string
	}{
		{"gtms/cases/guides/test-case-template.md", starterGuideContent},
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

// WriteAdapterStubs writes placeholder adapter scripts to the gtms/adapters/ directory.
// Returns the list of files created (relative paths).
// BUG-053: moved from adapters/ to gtms/adapters/ for ENH-098 three-item root footprint.
func WriteAdapterStubs(root string) ([]string, error) {
	adaptersDir := filepath.Join(root, "gtms", "adapters")
	if err := os.MkdirAll(adaptersDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating gtms/adapters directory: %w", err)
	}

	stubs := map[string]string{
		"gtms/adapters/github-create.sh":         adapterStubScript("github-create", "create"),
		"gtms/adapters/github-create-status.sh":  adapterStatusStubScript("github-create"),
		"gtms/adapters/github-automate.sh":       adapterStubScript("github-automate", "automate"),
		"gtms/adapters/github-automate-status.sh": adapterStatusStubScript("github-automate"),
		"gtms/adapters/github-execute.sh":        adapterStubScript("github-execute", "execute"),
		"gtms/adapters/github-execute-status.sh": adapterStatusStubScript("github-execute"),
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

// DemoSeedResult describes what DemoSeed created or modified.
type DemoSeedResult struct {
	FilesCreated    []string // files created (relative to project root)
	FilesSkipped    []string // files not written because they already existed
	ConfigModified  bool     // whether gtms.config was updated with demo adapters
	GuidanceModified bool   // whether guidance.yaml was updated
}

// DemoSeed seeds demo data into an existing GTMS project.
// The project must already have a valid gtms.config (run Init first if needed).
func DemoSeed(projectRoot string) (*DemoSeedResult, error) {
	result := &DemoSeedResult{}

	// Create demo directories
	demoDirs := []string{"_demo", "_demo/adapters"}
	for _, dir := range demoDirs {
		absDir := filepath.Join(projectRoot, filepath.FromSlash(dir))
		if err := os.MkdirAll(absDir, 0o755); err != nil {
			return nil, fmt.Errorf("creating directory %s: %w", dir, err)
		}
	}

	// Write demo files (skip if they already exist)
	demoFiles := []struct {
		relPath string
		content string
		perm    os.FileMode
	}{
		{"_demo/login-feature.md", demoLoginRequirement, 0o644},
		{"_demo/adapters/create-demo.sh", demoCreateScript, 0o755},
		{"_demo/adapters/automate-demo-sh.sh", demoAutomateShScript, 0o755},
		{"_demo/adapters/automate-demo-cmd.sh", demoAutomateCmdScript, 0o755},
		{"gtms/cases/guides/getting-started-with-ai.md", demoBridgeGuide, 0o644},
	}

	for _, f := range demoFiles {
		absPath := filepath.Join(projectRoot, filepath.FromSlash(f.relPath))
		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			return nil, fmt.Errorf("creating directory for %s: %w", f.relPath, err)
		}
		if _, err := os.Stat(absPath); err == nil {
			result.FilesSkipped = append(result.FilesSkipped, f.relPath)
			continue
		}
		if err := os.WriteFile(absPath, []byte(f.content), f.perm); err != nil {
			return nil, fmt.Errorf("writing %s: %w", f.relPath, err)
		}
		result.FilesCreated = append(result.FilesCreated, f.relPath)
	}

	// Load and merge config
	configPath := filepath.Join(projectRoot, "gtms.config")
	cfg, err := config.LoadFromFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("loading config for demo merge: %w", err)
	}

	// Backup existing config
	backupFile(configPath)

	// Merge demo adapter entries
	if cfg.Adapters == nil {
		cfg.Adapters = make(map[string]map[string]*config.AdapterConfig)
	}
	if cfg.Adapters["create"] == nil {
		cfg.Adapters["create"] = make(map[string]*config.AdapterConfig)
	}
	if cfg.Adapters["automate"] == nil {
		cfg.Adapters["automate"] = make(map[string]*config.AdapterConfig)
	}
	if cfg.Adapters["execute"] == nil {
		cfg.Adapters["execute"] = make(map[string]*config.AdapterConfig)
	}

	cfg.Adapters["create"]["demo"] = &config.AdapterConfig{
		Mode:   "sync",
		Script: "_demo/adapters/create-demo.sh",
	}
	cfg.Adapters["automate"]["demo-sh"] = &config.AdapterConfig{
		Mode:   "sync",
		Script: "_demo/adapters/automate-demo-sh.sh",
	}
	cfg.Adapters["automate"]["demo-cmd"] = &config.AdapterConfig{
		Mode:   "sync",
		Script: "_demo/adapters/automate-demo-cmd.sh",
	}
	cfg.Adapters["execute"]["demo-sh"] = &config.AdapterConfig{
		Mode:    "sync",
		Command: "sh {artefact_file}",
	}
	cfg.Adapters["execute"]["demo-cmd"] = &config.AdapterConfig{
		Mode:    "sync",
		Command: "cmd /c {artefact_file}",
	}

	cfg.DemoSeeded = true

	if err := config.WriteConfig(configPath, cfg); err != nil {
		return nil, fmt.Errorf("writing config with demo entries: %w", err)
	}
	result.ConfigModified = true

	// Update guidance.yaml
	if err := UpdateDemoGuidance(projectRoot); err != nil {
		return nil, err
	}
	result.GuidanceModified = true

	return result, nil
}

// UpdateDemoGuidance writes demo-specific guidance to .gtms/guidance.yaml.
// Safe to call multiple times — always overwrites with current demo guidance.
func UpdateDemoGuidance(projectRoot string) error {
	guidancePath := filepath.Join(projectRoot, ".gtms", "guidance.yaml")
	if err := os.MkdirAll(filepath.Dir(guidancePath), 0o755); err != nil {
		return fmt.Errorf("creating .gtms directory: %w", err)
	}

	backupFile(guidancePath)

	guidance := config.LoadGuidance(projectRoot)
	guidance["init"] = demoGuidanceInit
	guidance["create"] = demoGuidanceCreate
	guidance["automate"] = demoGuidanceAutomate
	guidance["execute"] = demoGuidanceExecute

	guidanceData, err := yaml.Marshal(guidance)
	if err != nil {
		return fmt.Errorf("marshalling guidance: %w", err)
	}
	if err := os.WriteFile(guidancePath, guidanceData, 0o644); err != nil {
		return fmt.Errorf("writing guidance.yaml: %w", err)
	}
	return nil
}

// gitignoreSentinel is the line ensureGitignore treats as the "GTMS already
// manages this project's .gitignore" marker. If it's present, ensureGitignore
// leaves the file byte-for-byte unchanged — even if newer entries below were
// added in later GTMS versions. This preserves backward compatibility with
// projects initialised before ENH-109 expanded the entry set, and honours the
// ENH-108 contract that `gtms init` must not modify a `.gitignore` that already
// declares GTMS as managed.
const gitignoreSentinel = ".gtms/"

// gitignoreEntries lists the rules ensureGitignore writes when seeding a fresh
// .gitignore (or appending to one that doesn't yet contain the sentinel).
// ENH-109 added the gtms/execution/ rules to keep per-task spill logs and
// TC-keyed binary attachments out of source control while leaving the durable
// per-test results YAMLs (gtms/execution/*.results.yaml) trackable.
var gitignoreEntries = []string{
	gitignoreSentinel,
	"gtms/execution/attachments/",
	"gtms/execution/logs/",
}

// ensureGitignore ensures the GTMS-managed entries are listed in .gitignore.
// Creates the file with all entries if absent. If the file exists and the
// sentinel (.gtms/) is already present, leaves the file untouched — the
// project is already GTMS-managed, and silently rewriting it would violate the
// ENH-108 byte-for-byte idempotency contract. If the file exists but the
// sentinel is absent, appends every entry (treating the project as newly
// adopting GTMS). Returns a GitignoreAction describing the outcome.
func ensureGitignore(projectRoot string) (GitignoreAction, error) {
	gitignorePath := filepath.Join(projectRoot, ".gitignore")

	data, err := os.ReadFile(gitignorePath)
	if err != nil {
		if os.IsNotExist(err) {
			var content strings.Builder
			for _, entry := range gitignoreEntries {
				content.WriteString(entry)
				content.WriteString("\n")
			}
			return GitignoreCreated, os.WriteFile(gitignorePath, []byte(content.String()), 0o644)
		}
		return "", err
	}

	content := string(data)
	for _, line := range splitLines(content) {
		if strings.TrimSpace(line) == gitignoreSentinel {
			return GitignoreUnchanged, nil
		}
	}

	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	for _, entry := range gitignoreEntries {
		content += entry + "\n"
	}
	return GitignoreAppended, os.WriteFile(gitignorePath, []byte(content), 0o644)
}

// splitLines splits a string into lines, handling both \n and \r\n.
func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Split(s, "\n")
}

// backupFile copies a file to {path}.bak if it exists. Errors are silently ignored
// since backups are belt-and-braces insurance, not critical operations.
func backupFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	_ = os.WriteFile(path+".bak", data, 0o644)
}
