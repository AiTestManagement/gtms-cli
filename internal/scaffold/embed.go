package scaffold

import "embed"

// presetsFS embeds the preset YAML files shipped with the binary.
// Each file uses {name} and {repo} as placeholders for project-specific values.
//
//go:embed presets/*.yaml
var presetsFS embed.FS
