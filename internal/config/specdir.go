package config

import (
	"fmt"
	"sort"
)

// CollectSpecDirs gathers all spec directories from the config.
// For adapters with OutputDir set, it uses that value.
// For adapters without OutputDir, it uses the convention: test-automation/specs/{adapter-name}/
// Returns a deduplicated, sorted list.
func CollectSpecDirs(cfg *Config) []string {
	seen := make(map[string]bool)

	for _, adapters := range cfg.Adapters {
		for name, ac := range adapters {
			if ac.OutputDir != "" {
				seen[ac.OutputDir] = true
			} else {
				seen[fmt.Sprintf("test-automation/specs/%s/", name)] = true
			}
		}
	}

	result := make([]string, 0, len(seen))
	for dir := range seen {
		result = append(result, dir)
	}
	sort.Strings(result)
	return result
}
