// Package config handles loading and validating the gtms.config file.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// frameworkRegex validates framework values: lowercase alphanumeric + hyphens.
var frameworkRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// builtinActionAdapterNames is the closed set of built-in adapter names for
// action commands (ENH-150). Used by config validation to allow defaults.X
// to reference built-in names without a matching adapters.X entry.
// This mirrors the table in adapter/resolver.go.
var builtinActionAdapterNames = map[string]map[string]bool{
	"create":   {"agent-create": true, "manual-create": true},
	"automate": {"agent-automate": true, "manual-automate": true},
	"prime":    {"agent-prime": true, "manual-prime": true},
	"execute":  {"agent-execute": true, "manual-execute": true},
}

// isBuiltinActionAdapter reports whether name is a known built-in action
// adapter for the given command.
func isBuiltinActionAdapter(command, name string) bool {
	if commandBuiltins, ok := builtinActionAdapterNames[command]; ok {
		return commandBuiltins[name]
	}
	return false
}

// BuiltinActionAdapterNames returns a deep copy of the closed set of built-in
// action adapter names keyed by command. Callers can mutate the result freely
// without affecting package state. Used by internal/cli to include Tier 0
// action built-ins in list output.
func BuiltinActionAdapterNames() map[string]map[string]bool {
	out := make(map[string]map[string]bool, len(builtinActionAdapterNames))
	for command, names := range builtinActionAdapterNames {
		cp := make(map[string]bool, len(names))
		for name, v := range names {
			cp[name] = v
		}
		out[command] = cp
	}
	return out
}

// ConfigFileName is the expected name of the GTMS configuration file.
const ConfigFileName = "gtms.config"

// Config represents the parsed gtms.config file.
type Config struct {
	Project     ProjectConfig                        `yaml:"project"`
	Adapters    map[string]map[string]*AdapterConfig `yaml:"adapters"`
	Defaults    map[string]string                    `yaml:"defaults"`
	Guidance    bool                                  `yaml:"guidance"`
	DemoSeeded  bool                                 `yaml:"demo_seeded,omitempty"`

	// Warnings holds non-fatal load-time notices surfaced by validate().
	// Not serialized; the CLI prints them to stderr after Load returns.
	Warnings []string `yaml:"-"`
}

// ProjectConfig holds project-level metadata.
type ProjectConfig struct {
	Name string `yaml:"name"`
	Repo string `yaml:"repo"`
}

// AdapterConfig represents a single adapter definition under a command.
type AdapterConfig struct {
	Mode           string `yaml:"mode"`
	Command        string `yaml:"command,omitempty"`
	Script         string `yaml:"script,omitempty"`
	Module         string `yaml:"module,omitempty"`
	PromptTemplate string `yaml:"prompt-template,omitempty"`
	StatusScript   string `yaml:"status-script,omitempty"`
	GuideDir       string `yaml:"guide-dir,omitempty"`
	Framework      string `yaml:"framework,omitempty"`  // Test framework identifier (e.g. "bats", "pester", "playwright")
	SpecDir        string `yaml:"spec-dir,omitempty"`   // Deprecated: use output-dir instead. Normalized to OutputDir at load time.
	OutputDir      string `yaml:"output-dir,omitempty"` // Where adapter output files are written
	WorkingDir     string `yaml:"working-dir,omitempty"` // ENH-168: run the adapter (Tier 1/2) from this project-relative dir (cwd)
	Timeout        string `yaml:"timeout,omitempty"`

	// ArtefactGlob (ENH-136) is a glob pattern with {testcase} variable
	// substitution that the execute command uses for lazy automation-record
	// creation when no record exists. The pattern is project-relative and
	// must contain the {testcase} placeholder. Example:
	//   artefact-glob: "test/acceptance/**/{testcase}*.bats"
	// The ** segment matches zero or more directory levels. Only execute
	// adapters should set this; setting it on create or automate adapters
	// has no effect.
	ArtefactGlob string `yaml:"artefact-glob,omitempty"`

	// FailExitCodes (ENH-078) lists non-zero exit codes a Tier 1 adapter uses
	// to signal "ran and failed" (status: fail) vs "couldn't run" (status: error).
	// Empty list (default): all non-zero exits → status: error (legacy behaviour).
	// Tier 1 only — setting this on a Tier 2 (script:) entry emits a load-time
	// warning and is ignored at runtime.
	//
	// We type this as []int and validate shape/value semantics in
	// UnmarshalYAML so that malformed input (string, nested list, scalar,
	// negative, zero) produces an error message naming the field.
	FailExitCodes []int `yaml:"fail-exit-codes,omitempty"`
}

// UnmarshalYAML provides field-level validation for AdapterConfig so that
// shape errors on fail-exit-codes carry a message that names the field.
// (yaml.v3's default error mentions only the type mismatch.) Any other
// fields are decoded via the same struct tags as the default behaviour.
func (ac *AdapterConfig) UnmarshalYAML(node *yaml.Node) error {
	// Two-pass decoding: first into a permissive shape that lets us spot
	// a malformed fail-exit-codes node before yaml.v3 surfaces its raw
	// "cannot unmarshal X into []int" error.
	if node.Kind == yaml.MappingNode {
		for i := 0; i < len(node.Content)-1; i += 2 {
			keyNode := node.Content[i]
			valNode := node.Content[i+1]
			if keyNode.Value != "fail-exit-codes" {
				continue
			}
			if valNode.Kind != yaml.SequenceNode {
				return fmt.Errorf(
					"invalid fail-exit-codes value: must be a YAML list of integers >= 1 (e.g. fail-exit-codes: [1]); got %s at line %d",
					nodeKindLabel(valNode.Kind), valNode.Line,
				)
			}
			for _, child := range valNode.Content {
				if child.Kind != yaml.ScalarNode {
					return fmt.Errorf(
						"invalid fail-exit-codes entry at line %d: must be an integer >= 1, not a %s",
						child.Line, nodeKindLabel(child.Kind),
					)
				}
				// Tag-level check: yaml.v3 sets Tag to e.g. "!!str" for unquoted "one"
				// or quoted "1" — both should be rejected as not-an-integer.
				if child.Tag != "" && child.Tag != "!!int" {
					return fmt.Errorf(
						"invalid fail-exit-codes entry %q at line %d: must be an integer >= 1",
						child.Value, child.Line,
					)
				}
			}
		}
	}

	// Defer to the default decoder by using a type alias to avoid
	// recursion into this method.
	type plainAdapterConfig AdapterConfig
	var plain plainAdapterConfig
	if err := node.Decode(&plain); err != nil {
		return err
	}
	*ac = AdapterConfig(plain)
	return nil
}

// nodeKindLabel returns a short human label for a yaml.Node kind so that
// validation errors are readable without exposing internal constants.
func nodeKindLabel(k yaml.Kind) string {
	switch k {
	case yaml.DocumentNode:
		return "document"
	case yaml.SequenceNode:
		return "list"
	case yaml.MappingNode:
		return "mapping"
	case yaml.ScalarNode:
		return "scalar"
	case yaml.AliasNode:
		return "alias"
	default:
		return "unknown"
	}
}

// FindProjectRoot walks up from startDir looking for a directory that
// contains ConfigFileName. Returns the directory path or an error.
func FindProjectRoot(startDir string) (string, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", err
	}

	for {
		configPath := filepath.Join(dir, ConfigFileName)
		if _, err := os.Stat(configPath); err == nil {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root
			return "", fmt.Errorf("No gtms.config found. Run 'gtms init' to set up this project.")
		}
		dir = parent
	}
}

// SentinelFileName is the name of the sentinel marker file that identifies
// the GTMS parent directory. Created by gtms init inside the parent dir.
const SentinelFileName = ".gtms-root"

// FindParentDir scans the direct children of projectRoot for a directory
// containing SentinelFileName. Returns the bare directory name (e.g. "gtms"
// or "testing") — not a full path.
//
// Discovery rules (ENH-098 Sentinel Behaviour Matrix):
//   - D1:  exactly one child has the sentinel → return its name
//   - D2:  no child has the sentinel → error
//   - D3:  multiple children have the sentinel → error listing all names
//   - D4/D5: grandchild or .git/.gtms sentinels → not scanned (treated as D2)
//   - D9:  non-empty sentinel → accepted (presence-only check)
//   - D10: symlink sentinel → rejected with specific error
//   - D11: unreadable directory → error with OS message
func FindParentDir(projectRoot string) (string, error) {
	entries, err := os.ReadDir(projectRoot)
	if err != nil {
		return "", fmt.Errorf("Cannot read project root '%s': %v\n    Check the directory exists and is readable.", projectRoot, err)
	}

	var found []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Skip VCS and GTMS operational dirs
		if name == ".git" || name == ".gtms" {
			continue
		}

		sentinelPath := filepath.Join(projectRoot, name, SentinelFileName)
		info, err := os.Lstat(sentinelPath)
		if err != nil {
			continue // sentinel not present in this child
		}

		// D10: reject symlinks
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("Symlink sentinel at %s — sentinel must be a regular file", sentinelPath)
		}

		found = append(found, name)
	}

	switch len(found) {
	case 0:
		return "", fmt.Errorf("No %s sentinel found in any direct child of %s.\n    Run 'gtms init' to scaffold a new project, or restore '%s'.",
			SentinelFileName, projectRoot, filepath.Join(projectRoot, "gtms", SentinelFileName))
	case 1:
		return found[0], nil
	default:
		sort.Strings(found)
		return "", fmt.Errorf("Multiple %s sentinels found: %s.\n    Exactly one parent directory is allowed. Remove the sentinel from all but one.",
			SentinelFileName, strings.Join(formatDirNames(found), ", "))
	}
}

// formatDirNames wraps each name in single quotes for error messages.
func formatDirNames(names []string) []string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = "'" + n + "/'"
	}
	return quoted
}

// Load reads and parses the gtms.config file from the given project root.
func Load(projectRoot string) (*Config, error) {
	configPath := filepath.Join(projectRoot, ConfigFileName)
	return LoadFromFile(configPath)
}

// LoadFromFile reads and parses a config file at the given path.
func LoadFromFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("No gtms.config found. Run 'gtms init' to set up this project.")
		}
		return nil, fmt.Errorf("Failed to read gtms.config: %w", err)
	}

	// BUG-087: reject retired config keys before unmarshalling so
	// yaml.v3 cannot silently ignore them as unknown fields.
	if err := rejectRetiredKeys(data); err != nil {
		return nil, err
	}

	cfg := Config{Guidance: true} // guidance ON by default; explicit `guidance: false` overrides
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		// ENH-078: AdapterConfig.UnmarshalYAML returns clear "invalid
		// fail-exit-codes ..." messages; surface them with our gtms.config
		// prefix so users see the file the error came from.
		if strings.Contains(err.Error(), "fail-exit-codes") {
			return nil, fmt.Errorf("gtms.config: %v", err)
		}
		return nil, fmt.Errorf("Failed to parse gtms.config: %v", err)
	}

	if err := validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// retiredKeys maps top-level config keys that were removed from the schema
// to a human-readable retirement message. rejectRetiredKeys scans raw YAML
// for these keys and rejects the config before yaml.Unmarshal can silently
// discard them as unknown fields.
var retiredKeys = map[string]string{
	"artefact-ignore": "gtms.config: 'artefact-ignore' was retired in the wiring-authoritative cutover (CON-023). The key is no longer accepted. Remove it from your config file.",
}

// rejectRetiredKeys decodes raw YAML just enough to inspect the top-level
// mapping keys. If any match a retired key, an error is returned with the
// retirement message. This prevents yaml.v3 from silently ignoring removed
// fields when the struct tag no longer exists.
func rejectRetiredKeys(data []byte) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil // let the main unmarshal surface the parse error
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(root.Content)-1; i += 2 {
		key := root.Content[i].Value
		if msg, ok := retiredKeys[key]; ok {
			return fmt.Errorf("%s", msg)
		}
	}
	return nil
}

// validate checks the parsed config for logical errors.
func validate(cfg *Config) error {
	// Project fields
	if cfg.Project.Name == "" {
		return fmt.Errorf("gtms.config: 'project.name' is required")
	}
	if cfg.Project.Repo == "" {
		return fmt.Errorf("gtms.config: 'project.repo' is required")
	}

	// Mutual exclusion + normalization (must run before validateAdapter)
	for _, adapters := range cfg.Adapters {
		for name, ac := range adapters {
			// Reject if user explicitly set BOTH fields
			if ac.OutputDir != "" && ac.SpecDir != "" {
				return fmt.Errorf(
					"gtms.config: adapter '%s' has both 'output-dir' and 'spec-dir' set. Use 'output-dir' only (spec-dir is deprecated).",
					name,
				)
			}
			// Normalize: copy spec-dir → output-dir for backward compat
			if ac.SpecDir != "" && ac.OutputDir == "" {
				ac.OutputDir = ac.SpecDir
			}
		}
	}

	// Validate each adapter
	for command, adapters := range cfg.Adapters {
		for name, ac := range adapters {
			if err := validateAdapter(command, name, ac); err != nil {
				return err
			}
			// ENH-078: warn when fail-exit-codes is set on a non-Tier-1 adapter.
			// Tier 1 = Command set; everything else (script, module, built-in)
			// ignores the key at runtime, so the user should know.
			if len(ac.FailExitCodes) > 0 && ac.Command == "" {
				cfg.Warnings = append(cfg.Warnings,
					fmt.Sprintf(
						"adapter '%s' sets fail-exit-codes but is not a Tier 1 (command:) adapter — the key will be ignored.",
						name,
					),
				)
			}
			// ENH-168: working-dir only affects Tier 1/2 adapters (which exec a child
			// process whose cwd is set). A built-in (Tier 0) adapter execs nothing, so
			// the key has no effect. Warn rather than error so the command still runs.
			if ac.WorkingDir != "" && ac.Command == "" && ac.Script == "" && ac.Module == "" {
				cfg.Warnings = append(cfg.Warnings,
					fmt.Sprintf(
						"adapter '%s' sets working-dir but is a built-in (Tier 0) adapter — working-dir has no effect and will be ignored.",
						name,
					),
				)
			}
		}
	}

	// Validate defaults reference existing adapters.
	// ENH-150: allow defaults to reference built-in action adapter names
	// (e.g. defaults.prime: manual-prime) even when no adapters.prime bucket
	// exists in config. The resolver falls back to the built-in name table.
	for command, adapterName := range cfg.Defaults {
		adapters, ok := cfg.Adapters[command]
		if !ok {
			// No adapter bucket for this command — only valid if the name
			// is a known built-in action adapter.
			if !isBuiltinActionAdapter(command, adapterName) {
				return fmt.Errorf(
					"gtms.config: default '%s' references adapter '%s' which is not registered under adapters.%s",
					command, adapterName, command,
				)
			}
			continue
		}
		if _, ok := adapters[adapterName]; !ok {
			// Adapter not in config bucket — valid if it's a built-in name.
			if !isBuiltinActionAdapter(command, adapterName) {
				return fmt.Errorf(
					"gtms.config: default '%s' references adapter '%s' which is not registered under adapters.%s",
					command, adapterName, command,
				)
			}
		}
	}

	return nil
}

// validateAdapter checks a single adapter config entry.
func validateAdapter(command, name string, ac *AdapterConfig) error {
	// Mode must be async or sync
	if ac.Mode != "async" && ac.Mode != "sync" {
		return fmt.Errorf(
			"gtms.config: adapter '%s' has invalid mode '%s'. Must be 'async' or 'sync'.",
			name, ac.Mode,
		)
	}

	// At most one of command, script, module
	count := 0
	set := []string{}
	if ac.Command != "" {
		count++
		set = append(set, "command")
	}
	if ac.Script != "" {
		count++
		set = append(set, "script")
	}
	if ac.Module != "" {
		count++
		set = append(set, "module")
	}

	if count > 1 {
		sort.Strings(set)
		return fmt.Errorf(
			"Adapter '%s' has both '%s' and '%s' defined. Use one or the other.",
			name, set[0], set[1],
		)
	}

	// output-dir must be relative (spec-dir is normalized to output-dir before this runs)
	if ac.OutputDir != "" && filepath.IsAbs(ac.OutputDir) {
		return fmt.Errorf(
			"gtms.config: adapter '%s' has absolute output-dir '%s'. Must be a relative path.",
			name, ac.OutputDir,
		)
	}

	// working-dir (ENH-168) must be relative and must not escape the project root.
	// Mirrors the output-dir absolute check and the artefact-glob '..' check so the
	// messages are consistent with the existing validators.
	if ac.WorkingDir != "" {
		if filepath.IsAbs(ac.WorkingDir) {
			return fmt.Errorf(
				"gtms.config: adapter '%s' has absolute working-dir '%s'. Must be a relative path.",
				name, ac.WorkingDir,
			)
		}
		for _, part := range strings.Split(filepath.ToSlash(ac.WorkingDir), "/") {
			if part == ".." {
				return fmt.Errorf(
					"gtms.config: adapter '%s' has working-dir '%s' containing '..'. Patterns must be inside the project root.",
					name, ac.WorkingDir,
				)
			}
		}
	}

	// timeout must be a valid duration
	if ac.Timeout != "" {
		if _, err := time.ParseDuration(ac.Timeout); err != nil {
			return fmt.Errorf(
				"gtms.config: adapter '%s' has invalid timeout '%s': %v",
				name, ac.Timeout, err,
			)
		}
	}

	// framework must match ^[a-z0-9][a-z0-9-]*$ if set
	if ac.Framework != "" && !frameworkRegex.MatchString(ac.Framework) {
		return fmt.Errorf(
			"gtms.config: adapter '%s' has invalid framework '%s'. Framework must contain only lowercase letters, digits, and hyphens.",
			name, ac.Framework,
		)
	}

	// fail-exit-codes (ENH-078): every entry must be >= 1.
	// Reject 0 (reserved for pass) and negatives. yaml.v3 rejects strings,
	// nested lists, and bare scalars at unmarshal time — see LoadFromFile
	// for the user-facing wrapping of those errors.
	for _, code := range ac.FailExitCodes {
		if code < 1 {
			return fmt.Errorf(
				"gtms.config: adapter '%s' has invalid fail-exit-codes entry %d. Exit codes must be integers >= 1 (0 is reserved for pass).",
				name, code,
			)
		}
	}

	// artefact-glob (ENH-136): must contain {testcase}, be relative, no ".."
	if ac.ArtefactGlob != "" {
		if !strings.Contains(ac.ArtefactGlob, "{testcase}") {
			return fmt.Errorf(
				"gtms.config: adapter '%s' has artefact-glob '%s' without {testcase} placeholder. The pattern must contain {testcase}.",
				name, ac.ArtefactGlob,
			)
		}
		if filepath.IsAbs(ac.ArtefactGlob) || strings.HasPrefix(ac.ArtefactGlob, "/") {
			return fmt.Errorf(
				"gtms.config: adapter '%s' has absolute artefact-glob '%s'. Must be a project-relative pattern.",
				name, ac.ArtefactGlob,
			)
		}
		for _, part := range strings.Split(filepath.ToSlash(ac.ArtefactGlob), "/") {
			if part == ".." {
				return fmt.Errorf(
					"gtms.config: adapter '%s' has artefact-glob '%s' containing '..'. Patterns must be inside the project root.",
					name, ac.ArtefactGlob,
				)
			}
		}
	}

	// status-script requires async mode and script
	if ac.StatusScript != "" {
		if ac.Mode != "async" {
			return fmt.Errorf(
				"gtms.config: adapter '%s' has 'status-script' but mode is '%s'. Mode must be 'async' when using status-script.",
				name, ac.Mode,
			)
		}
		if ac.Script == "" {
			return fmt.Errorf(
				"gtms.config: adapter '%s' has 'status-script' but no 'script' defined. status-script requires script.",
				name,
			)
		}
	}

	return nil
}

// WriteConfig marshals cfg to YAML and writes it to the given path.
// Note: this will not preserve comments from the original file.
func WriteConfig(path string, cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshalling config: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return nil
}

// AdapterNames returns a sorted list of adapter names registered under
// the given command. Used for error messages.
func (cfg *Config) AdapterNames(command string) []string {
	adapters, ok := cfg.Adapters[command]
	if !ok {
		return nil
	}
	names := make([]string, 0, len(adapters))
	for name := range adapters {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// AdapterNamesString returns a comma-separated string of adapter names
// for a given command.
func (cfg *Config) AdapterNamesString(command string) string {
	return strings.Join(cfg.AdapterNames(command), ", ")
}

// DefaultFramework returns the framework value from the default automate adapter's config.
// Returns empty string if no default automate adapter is configured or it has no framework field.
func DefaultFramework(cfg *Config) string {
	if cfg == nil {
		return ""
	}
	defaultName, ok := cfg.Defaults["automate"]
	if !ok {
		return ""
	}
	adapters, ok := cfg.Adapters["automate"]
	if !ok {
		return ""
	}
	ac, ok := adapters[defaultName]
	if !ok {
		return ""
	}
	return ac.Framework
}

// ValidateFramework checks if a framework value matches the required pattern.
// Returns true if valid (or empty). Returns false for values containing
// slashes, spaces, uppercase, or other invalid characters.
func ValidateFramework(framework string) bool {
	if framework == "" {
		return true
	}
	return frameworkRegex.MatchString(framework)
}
