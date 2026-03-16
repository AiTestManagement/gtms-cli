// Package config handles loading and validating the gtms.config file.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ConfigFileName is the expected name of the GTMS configuration file.
const ConfigFileName = "gtms.config"

// Config represents the parsed gtms.config file.
type Config struct {
	Project  ProjectConfig                        `yaml:"project"`
	Adapters map[string]map[string]*AdapterConfig `yaml:"adapters"`
	Defaults map[string]string                    `yaml:"defaults"`
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
	SpecDir        string `yaml:"spec-dir,omitempty"`   // Deprecated: use output-dir instead. Normalized to OutputDir at load time.
	OutputDir      string `yaml:"output-dir,omitempty"` // Where adapter output files are written
	Timeout        string `yaml:"timeout,omitempty"`
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

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("Failed to parse gtms.config: %v", err)
	}

	if err := validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
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
		}
	}

	// Validate defaults reference existing adapters
	for command, adapterName := range cfg.Defaults {
		adapters, ok := cfg.Adapters[command]
		if !ok {
			return fmt.Errorf(
				"gtms.config: default '%s' references adapter '%s' which is not registered under adapters.%s",
				command, adapterName, command,
			)
		}
		if _, ok := adapters[adapterName]; !ok {
			return fmt.Errorf(
				"gtms.config: default '%s' references adapter '%s' which is not registered under adapters.%s",
				command, adapterName, command,
			)
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

	// timeout must be a valid duration
	if ac.Timeout != "" {
		if _, err := time.ParseDuration(ac.Timeout); err != nil {
			return fmt.Errorf(
				"gtms.config: adapter '%s' has invalid timeout '%s': %v",
				name, ac.Timeout, err,
			)
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
