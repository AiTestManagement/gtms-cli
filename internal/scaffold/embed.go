package scaffold

import "embed"

// presetsFS embeds the preset YAML files shipped with the binary.
// Each file uses {name} and {repo} as placeholders for project-specific values.
//
//go:embed presets/*.yaml
var presetsFS embed.FS

// skillsFS embeds the starter Agent Skills shipped with the binary.
// Each skill is a directory containing SKILL.md in the Agent Skills open-standard
// format (agentskills.io). The catalog README is at skills/README.md.
// Written to gtms/skills/ by WriteExampleSkills during gtms init.
//
//go:embed skills
var skillsFS embed.FS

// AgentsSnippetMD is the agent-instructions discovery snippet scaffolded
// as gtms/AGENTS-SNIPPET.md by gtms init. Contains a paste-ready block
// for the project's AI agent instruction file (CLAUDE.md, AGENTS.md, etc.).
// The Draft A section within this file is LOCKED (ENH-183, 2026-07-11).
//
//go:embed agents-snippet.md
var AgentsSnippetMD string
