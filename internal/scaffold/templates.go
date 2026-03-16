// Package scaffold provides project scaffolding for gtms init.
// It creates directory structures, generates config files from presets,
// and writes starter prompt templates and adapter stub scripts.
package scaffold

import (
	"fmt"
	"strings"
)

// Valid preset names for the --adapter flag.
const (
	PresetMinimal = "minimal"
	PresetClaude  = "claude"
	PresetGitHub  = "github"
)

// ValidPresets returns the list of valid preset names.
func ValidPresets() []string {
	return []string{PresetMinimal, PresetClaude, PresetGitHub}
}

// IsValidPreset returns true if the given name is a valid preset.
func IsValidPreset(name string) bool {
	for _, p := range ValidPresets() {
		if p == name {
			return true
		}
	}
	return false
}

// yamlSafeString escapes a string for safe inclusion in a YAML double-quoted value.
// It escapes backslashes and double quotes to prevent YAML injection.
func yamlSafeString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

// configMinimal returns the minimal preset config template.
func configMinimal(name, repo string) string {
	return fmt.Sprintf(`project:
  name: "%s"
  repo: "%s"

adapters: {}

defaults: {}
`, yamlSafeString(name), yamlSafeString(repo))
}

// configClaude returns the claude preset config template.
func configClaude(name, repo string) string {
	return fmt.Sprintf(`project:
  name: "%s"
  repo: "%s"

adapters:
  create:
    local-claude:
      mode: sync
      prompt-template: test-cases/prompts/create-standard.md
      guide-dir: test-cases/guides/
      command: 'claude -p "Read the system prompt instructions. Create test cases from the reference material. Use the pre-generated IDs from the prompt. Output each test case using <gtms-file name=\"<id>-<short-slug>.md\"> tags, closed with </gtms-file>. YAML frontmatter then markdown body. No code fences. Raw text only." --append-system-prompt-file {prompt_file} --allowedTools ""'
  automate:
    local-claude:
      mode: sync
      prompt-template: test-automation/prompts/automate-standard.md
      command: 'claude -p "Read the system prompt instructions. Generate an automated test script from the test case. Output using <gtms-file name=\"<filename>\"> tags, closed with </gtms-file>. No code fences. Raw text only." --append-system-prompt-file {prompt_file} --allowedTools ""'
  execute:
    local-runner:
      mode: sync
      command: 'npx playwright test {spec_file} --reporter=junit --output=results/junit/'

defaults:
  create: local-claude
  automate: local-claude
  execute: local-runner
`, yamlSafeString(name), yamlSafeString(repo))
}

// configGitHub returns the github preset config template.
func configGitHub(name, repo string) string {
	return fmt.Sprintf(`project:
  name: "%s"
  repo: "%s"

adapters:
  create:
    github-create:
      mode: async
      prompt-template: test-cases/prompts/create-standard.md
      guide-dir: test-cases/guides/
      script: adapters/github-create.sh
      status-script: adapters/github-create-status.sh
  automate:
    github-automate:
      mode: async
      prompt-template: test-automation/prompts/automate-standard.md
      script: adapters/github-automate.sh
      status-script: adapters/github-automate-status.sh
  execute:
    github-actions:
      mode: async
      script: adapters/github-execute.sh
      status-script: adapters/github-execute-status.sh

defaults:
  create: github-create
  automate: github-automate
  execute: github-actions
`, yamlSafeString(name), yamlSafeString(repo))
}

// configForPreset returns the config content for the given preset.
func configForPreset(name, repo, preset string) string {
	switch preset {
	case PresetClaude:
		return configClaude(name, repo)
	case PresetGitHub:
		return configGitHub(name, repo)
	default:
		return configMinimal(name, repo)
	}
}

// promptCreateStandard is the starter create prompt template.
// Uses XML tags for clear semantic boundaries between instructions and data.
// For Claude Code: output format instructions go in the -p user message (command template),
// not here. This template provides task context for --append-system-prompt-file.
// For other adapters: this template IS the full prompt, so output rules are included.
// Section ordering follows LLM attention research: reference material first,
// output rules at the end (see BUG-007, ADR-001, ENH-029).
const promptCreateStandard = `<role>
You are a test case creation specialist.
</role>

<task>
Create test cases from the reference material provided below.
Reference: {reference}
</task>

<focus_area>
{focus}
</focus_area>

<source_material>
{context}
</source_material>

<quality_standards>
{guides}
</quality_standards>

<output_rules>
- Use the pre-generated test case IDs in order, one per test case: {tc_ids}
- Do NOT invent your own IDs — use only the IDs provided above
- Each test case is a separate file block using <gtms-file name="<id>-<short-slug>.md">...</gtms-file> tags (e.g. <gtms-file name="tc-a3f72b1-login-valid.md">)
- Each file block contains a complete test case with YAML frontmatter
- Frontmatter test_case_id must match the ID used in the filename (e.g. tc-a3f72b1)
- Required frontmatter fields: test_case_id, title, requirement (use the reference value), priority, type, created
- You may include a brief summary before the first <gtms-file> tag
- One test case per distinct behavior
- Follow the quality standards above
</output_rules>
`

// promptAutomateStandard is the starter automate prompt template.
// Uses XML tags for clear semantic boundaries (see ENH-029).
const promptAutomateStandard = `<role>
You are a test automation engineer.
</role>

<task>
Generate an automated test script from the test case provided below.
</task>

<test_case>
{testcase_content}
</test_case>

<framework>
{framework}
</framework>

<output_rules>
- Output using <gtms-file name="<filename>">...</gtms-file> tags
- No code fences -- raw text only
- Save the spec file to: {output_dir}
</output_rules>
`

// starterGuideContent is the default test case template guide.
const starterGuideContent = `# Test Case Template

Use this template for all test cases created by GTMS.

## Required Frontmatter

` + "```yaml" + `
---
test_case_id: tc-<7-char-hex-lowercase>
title: "<Action> should <expected outcome> when <condition>"
requirement: "<reference value>"
priority: High | Medium | Low
type: Functional | Performance | Security | Usability | Integration
created: YYYY-MM-DD
---
` + "```" + `

## Required Sections

1. **Test Objective** — What behavior is being verified and why
2. **Preconditions** — All conditions that must be true before Step 1
3. **Test Data** — Exact values to use (no placeholders)
4. **Test Steps** — One atomic action per step with expected result
5. **Expected Final Outcome** — Specific pass/fail criteria
6. **Postconditions** — Expected system state after test

## Principles

- One test case per specific behavior
- Steps must be atomic and unambiguous
- Expected results must be specific and verifiable
- Use exact values, not ranges or placeholders
- Link back to the source requirement
`

// testCasesReadme is the README for the test-cases/ directory.
// It explains how prompts, guides, config, and the directory structure work together.
// Serves as both user documentation and AI context for tools helping develop the test creation process.
const testCasesReadme = `# Test Cases

This directory contains test cases created by GTMS and the supporting files that control how they are generated.

## Directory Structure

` + "```" + `
test-cases/
  prompts/
    create-standard.md    — Prompt template: instructions for the AI adapter
  guides/
    test-case-template.md — Quality standards embedded into every prompt
  tc-*.md                 — Generated test case files
` + "```" + `

## How Test Case Creation Works

When you run ` + "`gtms create <folder>`" + `, GTMS assembles a prompt and sends it to the configured adapter:

1. GTMS reads the **prompt template** (` + "`prompts/create-standard.md`" + `)
2. GTMS reads all ` + "`.md`" + ` files from the **guides directory** (` + "`guides/`" + `)
3. GTMS substitutes template variables (` + "`{reference}`" + `, ` + "`{guides}`" + `, ` + "`{context}`" + `, ` + "`{focus}`" + `)
4. The assembled prompt is written to ` + "`.gtms/tmp/{task-id}-prompt.md`" + `
5. The adapter receives the prompt via file path (` + "`{prompt_file}`" + `) and/or stdin

## Config Connection

The ` + "`gtms.config`" + ` adapter entry ties everything together:

` + "```yaml" + `
adapters:
  create:
    local-claude:
      mode: sync
      prompt-template: test-cases/prompts/create-standard.md    # ← prompt template
      guide-dir: test-cases/guides/                              # ← quality standards
      command: 'claude -p "..." --append-system-prompt-file {prompt_file} --allowedTools ""'
` + "```" + `

## Prompt Template

The prompt template (` + "`prompts/create-standard.md`" + `) controls what the AI adapter does. It uses ` + "`{variable}`" + ` placeholders that GTMS substitutes before sending to the adapter.

### Variables Available

| Variable | Size | Description |
|----------|------|-------------|
| ` + "`{reference}`" + ` | Short | The --reference flag value passed to ` + "`gtms create`" + ` |
| ` + "`{focus}`" + ` | Short | Value of ` + "`--focus`" + ` flag (scope within source) |
| ` + "`{context}`" + ` | **Unbounded** | Content of ` + "`--context-file`" + ` (can be thousands of lines) |
| ` + "`{guides}`" + ` | **Unbounded** | All guide files, each XML-wrapped (can be hundreds of lines) |

### Section Ordering Rules

Template ordering affects AI output quality. LLMs attend most strongly to the beginning and end of the prompt, with degraded attention for content in the middle.

**Rule: Put unbounded content in the middle. Put output format instructions at the end.**

The shipped templates use XML tags (` + "`<role>`" + `, ` + "`<task>`" + `, ` + "`<source_material>`" + `, ` + "`<output_rules>`" + `, etc.) to provide unambiguous semantic boundaries that survive attention degradation. The ordering principle still applies within the XML structure:

` + "```" + `
<role>           ← Short: role definition
<task>           ← Short: target and action
<focus_area>     ← Short: {focus}
<source_material>← UNBOUNDED: {context}
<quality_standards>← UNBOUNDED: {guides}
<output_rules>   ← CRITICAL: at the END
` + "```" + `

If you move output instructions before ` + "`{guides}`" + ` or ` + "`{context}`" + `, the AI may ignore them when those sections expand to thousands of lines.

## Guides

Guide files in ` + "`guides/`" + ` define quality standards that are embedded into every prompt via the ` + "`{guides}`" + ` variable. GTMS reads all ` + "`.md`" + ` files alphabetically and wraps each in ` + "`<guide name=\"...\">` XML tags" + ` for clear boundaries.

To add more quality standards, create additional ` + "`.md`" + ` files in ` + "`guides/`" + `. They are picked up automatically.

## Output Format

The adapter must output test cases using XML-tagged file blocks:

` + "```" + `
<gtms-file name="tc-a3f72b1-login-valid.md">
---
test_case_id: tc-a3f72b1
title: "Login should succeed with valid credentials"
...
---
(test case content)
</gtms-file>

<gtms-file name="tc-b4e8c21-login-invalid.md">
...
</gtms-file>
` + "```" + `

GTMS streams stdout and writes each file to ` + "`test-cases/`" + ` as it completes. If no ` + "`<gtms-file>`" + ` tags are found, the output is captured as a summary but no test case files are created.
`

// adapterStubScript returns a stub adapter script for the given adapter name and command.
func adapterStubScript(adapterName, command string) string {
	return fmt.Sprintf(`#!/bin/bash
# Stub adapter script for %s
# Replace this with your GitHub Actions / API integration.
#
# Available environment variables:
#   GTMS_TASK_ID, GTMS_COMMAND, GTMS_REFERENCE, GTMS_TESTCASE,
#   GTMS_TESTCASE_CONTENT, GTMS_OUTPUT_DIR, GTMS_SPEC_FILE, GTMS_PROMPT_TEMPLATE,
#   GTMS_BRANCH, GTMS_REPO, GTMS_PROJECT_ROOT, GTMS_WORK_DIR,
#   GTMS_RESULT_FILE, GTMS_FOCUS, GTMS_CONTEXT, GTMS_CONTEXT_FILE,
#   GTMS_GUIDES, GTMS_PROMPT_FILE
#
# To report results, write YAML to $GTMS_RESULT_FILE with at minimum:
#   status: complete (or error)
#   artefact: path/to/output

echo "STUB: %s adapter not yet implemented" >&2
exit 1
`, adapterName, adapterName)
}

// adapterStatusStubScript returns a stub status-check script for the given adapter name.
func adapterStatusStubScript(adapterName string) string {
	return fmt.Sprintf(`#!/bin/bash
# Stub status-check script for %s
# Replace this with your GitHub Actions / API status check.
#
# Available environment variables:
#   GTMS_TASK_ID, GTMS_RESULT_FILE
#
# Read the result file and check if the async operation has completed.
# Update $GTMS_RESULT_FILE with the current status.

echo "STUB: %s status check not yet implemented" >&2
exit 1
`, adapterName, adapterName)
}
