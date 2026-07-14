// Package help embeds reference documents that ship inside the binary.
// The canonical source is reference/ai-coding-assistant-guide.md; this
// package carries a committed mirror so go:embed can reach it (go:embed
// cannot reference paths outside its own package directory). A parity
// test in parity_test.go guards byte-equality between the mirror and the
// canonical file.
package help

import _ "embed"

// AgentGuide is the AI coding-assistant quick reference, embedded from
// the package-local mirror of reference/ai-coding-assistant-guide.md.
//
//go:embed ai-coding-assistant-guide.md
var AgentGuide string

// SkillsOverview is the starter Agent Skills overview, printed by
// the "skills" help topic command. Unlike AgentGuide, this content
// has no canonical mirror -- it is authored directly in this package.
//
//go:embed skills-overview.md
var SkillsOverview string

// GettingStarted is the linear quick start overview, printed by the
// "getting-started" help topic command. Authored directly in this
// package (no canonical mirror, no parity test).
//
//go:embed getting-started.md
var GettingStarted string
