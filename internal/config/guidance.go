package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// GuidanceConfig maps command names to their "Next" body text.
type GuidanceConfig map[string]string

// LoadGuidance reads .gtms/guidance.yaml from the project root.
// If the file is missing or malformed, it returns built-in defaults.
func LoadGuidance(projectRoot string) GuidanceConfig {
	path := filepath.Join(projectRoot, ".gtms", "guidance.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return DefaultGuidance()
	}

	var cfg GuidanceConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return DefaultGuidance()
	}

	// Per-key fallback: fill missing keys from compiled defaults so new
	// guidance keys (e.g. "prime") are available even when a user-customised
	// guidance.yaml omits them. User values win; defaults fill gaps.
	defaults := DefaultGuidance()
	for k, v := range defaults {
		if _, ok := cfg[k]; !ok {
			cfg[k] = v
		}
	}

	return cfg
}

// DefaultGuidance returns the compiled-in fallback guidance config.
func DefaultGuidance() GuidanceConfig {
	return GuidanceConfig{
		"init": "gtms init --demo               — seed demo data for learning the pipeline\n" +
			"or\n" +
			"gtms create <folder>           — create a test case skeleton to fill in\n",
		"create": "gtms status <folder>           — see your test cases in the pipeline\n" +
			"or\n" +
			"gtms prime <tc-id> --framework manual   — record a manual test result\n",
		"prime": "gtms execute <tc-id> --adapter manual-execute  — record the manual test result\n" +
			"gtms status <tc-id>                            — see detail for this test case\n",
		"automate": "gtms execute <tc-id>           — run the automated test\n" +
			"gtms status <tc-id>            — see detail for this test case\n",
		"execute": "gtms status                    — see the pipeline dashboard\n" +
			"gtms gaps                      — check for coverage gaps\n",
	}
}
