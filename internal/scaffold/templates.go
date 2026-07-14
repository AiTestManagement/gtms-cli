// Package scaffold provides project scaffolding for gtms init.
// It creates directory structures, generates config files from presets,
// and writes starter prompt templates and adapter stub scripts.
package scaffold

import (
	"fmt"
	"os"
	"strings"
)

// loadPreset reads a preset YAML file from the embedded filesystem and substitutes
// {name} and {repo} placeholders with the project-specific values.
// ENH-128: presets are now external .yaml files instead of compiled Go string literals.
func loadPreset(preset, name, repo string) (string, error) {
	data, err := presetsFS.ReadFile("presets/" + preset + ".yaml")
	if err != nil {
		return "", fmt.Errorf("reading preset %s: %w", preset, err)
	}
	content := string(data)
	content = strings.ReplaceAll(content, "{name}", yamlSafeString(name))
	content = strings.ReplaceAll(content, "{repo}", yamlSafeString(repo))
	return content, nil
}

// Valid preset names for the --preset flag.
// BUG-111: presets are workflow bundles (command routes, adapters, frameworks,
// installed assets), not authoring-tool selectors.
const (
	PresetManual     = "manual"
	PresetBats       = "bats"
	PresetPlaywright = "playwright"
)

// PresetDescription returns a one-line description for the given preset.
var PresetDescriptions = map[string]string{
	PresetManual:     "Manual testing workflow with result templates and schema validation",
	PresetBats:       "BATS shell testing with local runner and TAP classification",
	PresetPlaywright: "Playwright browser testing with local TC-specific spec execution",
}

// PresetAsset describes a file that a specific preset installs beyond common scaffold.
type PresetAsset struct {
	RelPath string
	Content string
	Perm    os.FileMode
}

// PresetAssets maps each preset to the files it installs beyond common scaffold.
// BUG-111 / ADR-022: framework-specific assets are preset-owned, not unconditional.
var PresetAssets = map[string][]PresetAsset{
	PresetManual: {}, // no extra assets (manual framework has no automate stage -- `gtms automate` on a manual TC returns the prime/execute hint per BUG-120)
	PresetBats: {
		{RelPath: "gtms/adapters/bats-runner.sh", Content: batsRunnerScript, Perm: 0o755},
		{RelPath: "gtms/adapters/lib/bats-tap.sh", Content: batsTapHelper, Perm: 0o755},
		// ENH-162: per-framework automate skeleton template
		{RelPath: "gtms/automation/templates/bats.template.bats", Content: BATSAutomateTemplate, Perm: 0o644},
	},
	PresetPlaywright: {
		{RelPath: "gtms/adapters/playwright-runner.sh", Content: playwrightRunnerScript, Perm: 0o755},
		// ENH-162: per-framework automate skeleton template
		{RelPath: "gtms/automation/templates/playwright.template.spec.ts", Content: PlaywrightAutomateTemplate, Perm: 0o644},
	},
}

// ValidPresets returns the list of valid preset names.
func ValidPresets() []string {
	return []string{PresetManual, PresetBats, PresetPlaywright}
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

// configForPreset returns the config content for the given preset.
// ENH-128: presets are loaded from embedded .yaml files instead of compiled Go strings.
func configForPreset(name, repo, preset string) string {
	content, err := loadPreset(preset, name, repo)
	if err != nil {
		// Fallback should never happen with valid preset names (validated upstream).
		// Return a minimal valid config to prevent panic.
		return fmt.Sprintf("project:\n  name: \"%s\"\n  repo: \"%s\"\n", yamlSafeString(name), yamlSafeString(repo))
	}
	return content
}

// TestcaseTemplateMD is the day-one TC skeleton template with placeholders.
// ENH-161: written to disk by gtms init. BuiltinCreate reads this file at
// runtime; its fallback shape in adapter/builtin_action.go must match.
//
// Placeholders: ${TESTCASE_ID}, ${TITLE}, ${REQUIREMENT}, ${CREATED}
const TestcaseTemplateMD = `---
test_case_id: ${TESTCASE_ID}
title: "${TITLE}"
requirement: ${REQUIREMENT}
priority: Medium
type: Functional
created: ${CREATED}
---

## Test Objective


## Preconditions

-

## Test Data

-

## Test Steps

1.
   - Expected observation:

## Expected Final Outcome

-

## Postconditions

-

## Notes

`

// BATSAutomateTemplate is the parameterised BATS automate skeleton template
// with ${TESTCASE_ID} and ${PROJECT_ROOT_DEPTH} placeholders. ENH-162:
// this is the single source of truth -- the scaffold writes it to disk as
// gtms/automation/templates/bats.template.bats, and the adapter falls back
// to it when the file is missing. adapter/framework_support.go references
// this const via BATSSupport.FallbackContent().
const BATSAutomateTemplate = `#!/usr/bin/env bats

# Auto-generated by gtms automate --adapter agent-automate
# Test case: ${TESTCASE_ID}

setup_file() {
    export PROJECT_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/${PROJECT_ROOT_DEPTH}" && pwd)"
    load "$PROJECT_ROOT/test/test_helper/common-setup.bash"
}

setup() {
    _common_setup
}

@test "${TESTCASE_ID}: <describe what this test validates>" {
    # TODO: fill in the test body
    skip "skeleton -- not yet implemented"
}

teardown() {
    :
}
`

// PlaywrightAutomateTemplate is the parameterised Playwright automate skeleton
// template with ${TESTCASE_ID} placeholder. ENH-162: single source of truth,
// same discipline as BATSAutomateTemplate above.
const PlaywrightAutomateTemplate = `// Auto-generated by gtms automate --adapter agent-automate
// Test case: ${TESTCASE_ID}

import { test, expect } from '@playwright/test';

test('${TESTCASE_ID}: <describe what this test validates>', async ({ page }) => {
  // TODO: fill in the test body
  test.skip(true, 'skeleton -- not yet implemented');
});
`

// RESERVED FOR ENH-176 -- not wired into `gtms init` today. Under DOC-018
// Direction 2 (Retire), no shipped preset scaffolds a starter create prompt
// template; `gtms/test/prompts/create-standard.md` is user-authored. This
// constant and its keep-alive tests are kept deliberately (NOT deleted) so
// ENH-176 (ship the starter template) does not start from a blank sheet.
//
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
- Do NOT invent your own IDs -- use only the IDs provided above
- Each test case is a separate file block using <gtms-file name="<id>-<short-slug>.md">...</gtms-file> tags (e.g. <gtms-file name="tc-a3f72b10-login-valid.md">)
- Each file block contains a complete test case with YAML frontmatter
- Frontmatter test_case_id must match the ID used in the filename (e.g. tc-a3f72b10)
- Required frontmatter fields: test_case_id, title, requirement (use the reference value), priority, type, created
- You may include a brief summary before the first <gtms-file> tag
- If {tc_name} is non-empty, generate exactly one test case. Use the first ID from {tc_ids} and name the file <first-id>-{tc_name}.md (e.g. <gtms-file name="tc-a3f72b10-user-can-login.md">). Set the title: frontmatter field to a human-readable form of the name. Do not generate additional test cases.
- If {tc_name} is empty, generate one test case per distinct behavior using the IDs from {tc_ids} in order with AI-chosen slugs
- Follow the quality standards above
</output_rules>
`

// RESERVED FOR ENH-176 -- not wired into `gtms init` today (see the
// promptCreateStandard note above). The automate starter prompt template is
// user-authored under DOC-018 Direction 2; kept here for ENH-176.
//
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

This is the authoring reference for test cases in ` + "`gtms/test/cases/`" + `. It documents
the section structure produced by ` + "`gtms create`" + ` and the richer frontmatter
keys you can add as a TC matures from skeleton to fully-authored spec.

## Frontmatter

### What ` + "`gtms create`" + ` writes (skeleton default)

` + "```yaml" + `
---
test_case_id: tc-<8-char-hex-lowercase>
title: "<slug if [name] was passed, empty otherwise>"
requirement: <reference value -- present only when --reference was passed>
priority: Medium
type: Functional
created: YYYY-MM-DD
---
` + "```" + `

This is the contract the skeleton create adapter emits and the pipeline reads.
` + "`test_case_id`" + ` is the join key for every downstream stage; do not change it.
` + "`priority`" + ` and ` + "`type`" + ` are stamped with safe defaults so the dashboard always
has something to sort and filter by; edit them as the TC matures.

### Recommended additions when authoring a richer spec

When you grow a test case beyond the skeleton -- by hand or via an AI adapter --
this header line gives reviewers a one-glance summary; rewrite the stamped
` + "`title`" + ` slug into a full sentence as soon as the behaviour is clear:

` + "```yaml" + `
title: "<Action> should <expected outcome> when <condition>"
` + "```" + `

` + "`priority`" + ` and ` + "`type`" + ` accept the following ranges; the skeleton defaults are
safe starting points:

` + "```yaml" + `
priority: High | Medium | Low
type: Functional | Performance | Security | Usability | Integration
` + "```" + `

These additions are optional. Nothing in the pipeline requires them beyond the
defaults the stamper already writes; rewrite them when they earn their keep.

## Test Objective

State what specific behaviour this test case verifies and why it matters.
One test case should cover one behaviour -- if you need an "and" in the
objective, split into separate test cases.

## Preconditions

List every condition that must be true before Step 1 begins. Include
environment state, user authentication, data that must already exist,
and configuration settings.

## Test Data

Provide exact values used during execution. Avoid placeholders like
"valid email" -- instead use a concrete value such as "user@example.com".
Include both valid and boundary data where relevant.

## Test Steps

Each step is one atomic action with an expected observation. Use the
format:

` + "```" + `
1. Perform <action>
   - Expected observation: <what should happen>
` + "```" + `

Steps must be unambiguous -- another tester should produce the same
result by following them exactly.

## Expected Final Outcome

Describe the overall success criteria after all steps complete. This is
the definitive pass/fail signal: if this outcome is observed, the test
passes.

## Postconditions

Document the expected system state after the test. Include any cleanup
that should happen or side-effects the tester should be aware of.

## Notes

Optional section for context, edge cases to consider, links to
related test cases, or known issues.

## Principles

- One test case per specific behavior
- Steps must be atomic and unambiguous
- Expected results must be specific and verifiable
- Use exact values, not ranges or placeholders
- Link back to the source requirement
`

// RESERVED FOR ENH-176 -- not written by `gtms init` today. Under DOC-018
// Direction 2 this README is not scaffolded; its directory tree still shows the
// pre-ENH-164/165 layout (prompts/ and guides/ nested under gtms/test/cases/)
// and must be corrected to the real ENH-165 layout as part of ENH-176 before it
// ships. Kept here (NOT deleted) so ENH-176 can revise and wire it.
//
// testCasesReadme is the README for the gtms/test/cases/ directory.
// It explains how prompts, guides, config, and the directory structure work together.
// Serves as both user documentation and AI context for tools helping develop the test creation process.
const testCasesReadme = `# Test Cases

This directory contains test cases created by GTMS and the supporting files that control how they are generated.

## Directory Structure

` + "```" + `
gtms/test/cases/
  prompts/
    create-standard.md    -- Prompt template: instructions for the AI adapter
  guides/
    gtms-test-case-authoring-guide.md -- Quality standards embedded into every prompt
  tc-*.md                 -- Generated test case files
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
      prompt-template: gtms/test/prompts/create-standard.md    # <- prompt template
      guide-dir: gtms/test/guides/                              # <- quality standards
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
<role>           <- Short: role definition
<task>           <- Short: target and action
<focus_area>     <- Short: {focus}
<source_material><- UNBOUNDED: {context}
<quality_standards><- UNBOUNDED: {guides}
<output_rules>   <- CRITICAL: at the END
` + "```" + `

If you move output instructions before ` + "`{guides}`" + ` or ` + "`{context}`" + `, the AI may ignore them when those sections expand to thousands of lines.

## Guides

Guide files in ` + "`guides/`" + ` define quality standards that are embedded into every prompt via the ` + "`{guides}`" + ` variable. GTMS reads all ` + "`.md`" + ` files alphabetically and wraps each in ` + "`<guide name=\"...\">` XML tags" + ` for clear boundaries.

To add more quality standards, create additional ` + "`.md`" + ` files in ` + "`guides/`" + `. They are picked up automatically.

## Output Format

The adapter must output test cases using XML-tagged file blocks:

` + "```" + `
<gtms-file name="tc-a3f72b10-login-valid.md">
---
test_case_id: tc-a3f72b10
title: "Login should succeed with valid credentials"
...
---
(test case content)
</gtms-file>

<gtms-file name="tc-b4e8c210-login-invalid.md">
...
</gtms-file>
` + "```" + `

GTMS streams stdout and writes each file to ` + "`gtms/test/cases/`" + ` as it completes. If no ` + "`<gtms-file>`" + ` tags are found, the output is captured as a summary but no test case files are created.
`

// adapterStubScript returns a stub adapter script for the given adapter name and command.
func adapterStubScript(adapterName, command string) string {
	return fmt.Sprintf(`#!/bin/sh
# Stub adapter script for %s
# Replace this with your GitHub Actions / API integration.
#
# Available environment variables:
#   GTMS_TASK_ID, GTMS_COMMAND, GTMS_REFERENCE, GTMS_TESTCASE,
#   GTMS_TESTCASE_CONTENT, GTMS_OUTPUT_DIR, GTMS_ARTEFACT_FILE, GTMS_TESTCASE_FILE,
#   GTMS_PROMPT_TEMPLATE,
#   GTMS_BRANCH, GTMS_REPO, GTMS_PROJECT_ROOT, GTMS_WORK_DIR,
#   GTMS_RESULT_FILE, GTMS_FOCUS, GTMS_CONTEXT, GTMS_CONTEXT_FILE,
#   GTMS_GUIDES, GTMS_PROMPT_FILE
#
# To report results, write YAML to $GTMS_RESULT_FILE with at minimum:
#   status: complete (or error)
#   result: pass (or fail/skip/error -- required when status: complete)
#   artefact: path/to/output

echo "STUB: %s adapter not yet implemented" >&2
exit 1
`, adapterName, adapterName)
}

// adapterStatusStubScript returns a stub status-check script for the given adapter name.
func adapterStatusStubScript(adapterName string) string {
	return fmt.Sprintf(`#!/bin/sh
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

// DefaultGuidanceYAML is the default content for .gtms/guidance.yaml.
// Teams can customise this file to add workflow-specific guidance.
// The "Next:" header is printed by Go code; this file contains only the body lines.
const DefaultGuidanceYAML = `init: |
  gtms init --demo               -- seed demo data for learning the pipeline
  or
  gtms create <folder>           -- create a test case skeleton to fill in

create: |
  gtms status <folder>           -- see your test cases in the pipeline
  or
  gtms prime <tc-id> --framework manual   -- record a manual test result
  Tip: edit gtms/adapters/manual-create-script.sh to customise the test case template

prime: |
  gtms execute <tc-id> --adapter manual-execute  -- record the manual test result
  gtms status <tc-id>                            -- see detail for this test case

automate: |
  gtms execute <tc-id>           -- run the automated test
  gtms status <tc-id>            -- see detail for this test case

execute: |
  gtms status                    -- see the pipeline dashboard
  gtms gaps                      -- check for coverage gaps
`

// agentCreateScript is the Tier 2 create adapter script for agent workflows.
// ENH-160: renamed from agentSkeletonScript. ENH-161: template-driven --
// reads role-specific template from GTMS_TEMPLATE_FILE, falls back to heredoc.
const agentCreateScript = `#!/bin/sh
# agent-create-script.sh -- Tier 2 sync adapter for agent test case creation.
#
# To customise the test case shape, edit your role's template at:
#   gtms/test/templates/agent-testcase.template.md
# To customise the stamping behaviour, edit this script.
# To activate: set defaults.create to agent-create-script in gtms.config.

set -e

# GTMS provides these environment variables:
#   GTMS_OUTPUT_DIR      -- where to write the test case file
#   GTMS_TC_IDS          -- comma-separated list of pre-generated test case IDs
#   GTMS_REFERENCE       -- the --reference flag value (may be empty)
#   GTMS_RESULT_FILE     -- path to the handoff contract YAML
#   GTMS_TASK_ID         -- unique task identifier
#   GTMS_TEMPLATE_FILE   -- path to the role-specific testcase template

if [ -z "$GTMS_OUTPUT_DIR" ]; then
  echo "ERROR: GTMS_OUTPUT_DIR not set" >&2
  exit 1
fi

mkdir -p "$GTMS_OUTPUT_DIR"

# Take the first ID from the comma-separated list
ID=$(echo "$GTMS_TC_IDS" | cut -d',' -f1)

if [ -z "$ID" ]; then
  echo "ERROR: GTMS_TC_IDS not set or empty" >&2
  exit 1
fi

# Build filename -- include name slug if provided
if [ -n "$GTMS_TC_NAME" ]; then
  OUTFILE="$GTMS_OUTPUT_DIR/${ID}-${GTMS_TC_NAME}.md"
  NAME_VALUE="${GTMS_TC_NAME}"
else
  OUTFILE="$GTMS_OUTPUT_DIR/${ID}.md"
  NAME_VALUE=""
fi

# escape_sed: Escape sed-replacement specials (\ & |) in values.
escape_sed() { printf '%s' "$1" | sed -e 's/[\\&|]/\\&/g'; }

# yaml_escape: Encode a value for safe insertion as a YAML double-quoted scalar.
yaml_escape() {
  printf '%s' "$1" | awk '
    {
      gsub(/\\/, "\\\\")
      gsub(/"/,  "\\\"")
      gsub(/\t/, "\\t")
      gsub(/\r/, "\\r")
      if (NR > 1) printf "\\n"
      printf "%s", $0
    }
  '
}

# yaml_quoted_or_empty: For non-empty input, print a complete YAML double-quoted
# scalar (carrying its own quotes). For empty input, print nothing. Mirrors the
# Go-side yamlQuotedScalarOrEmpty helper so the substituted value either lands
# as a properly-quoted scalar in an unquoted placeholder context (e.g. the
# requirement line in the testcase template) or leaves the value position bare.
yaml_quoted_or_empty() {
  if [ -z "$1" ]; then
    printf ''
  else
    printf '"%s"' "$(yaml_escape "$1")"
  fi
}

# Prepare substitution values
ID_E=$(escape_sed "$ID")
NAME_E=$(escape_sed "$NAME_VALUE")
REF_E=$(escape_sed "$(yaml_quoted_or_empty "$GTMS_REFERENCE")")
DATE_E=$(escape_sed "$(date +%Y-%m-%d)")

# ENH-161: Read template from GTMS_TEMPLATE_FILE; fall back to heredoc if missing.
if [ -n "$GTMS_TEMPLATE_FILE" ] && [ -f "$GTMS_TEMPLATE_FILE" ]; then
  sed \
    -e "s|\${TESTCASE_ID}|${ID_E}|g" \
    -e "s|\${TITLE}|${NAME_E}|g" \
    -e "s|\${REQUIREMENT}|${REF_E}|g" \
    -e "s|\${CREATED}|${DATE_E}|g" \
    "$GTMS_TEMPLATE_FILE" > "$OUTFILE"
else
  if [ -n "$GTMS_TEMPLATE_FILE" ]; then
    echo "warning: create template not found: $GTMS_TEMPLATE_FILE -- using built-in default" >&2
  fi
  # Fallback: inline heredoc (day-one shape). REF_LINE emits a properly-quoted
  # YAML scalar when --reference is set, or a bare key when empty -- matching
  # the substitution semantics of the template-read path above.
  if [ -n "$GTMS_REFERENCE" ]; then
    REF_LINE="requirement: \"$(yaml_escape "$GTMS_REFERENCE")\""
  else
    REF_LINE="requirement: "
  fi
cat > "$OUTFILE" <<TCEOF
---
test_case_id: ${ID}
title: "${NAME_VALUE}"
${REF_LINE}
priority: Medium
type: Functional
created: $(date +%Y-%m-%d)
---

## Test Objective


## Preconditions

-

## Test Data

-

## Test Steps

1.
   - Expected observation:

## Expected Final Outcome

-

## Postconditions

-

## Notes

TCEOF
fi

# Update handoff contract
if [ -n "$GTMS_RESULT_FILE" ]; then
cat > "$GTMS_RESULT_FILE" <<REOF
task: ${GTMS_TASK_ID}
command: create
target: $(basename "$GTMS_OUTPUT_DIR")
adapter: agent-create-script
mode: sync
status: complete
result: pass
artefact: ${OUTFILE}
summary: "Created agent-create-script test case ${ID}"
completed: $(date -u +%Y-%m-%dT%H:%M:%SZ)
REOF
fi

echo "Created agent-create-script test case: ${OUTFILE}"
` + ""

// manualCreateScript is the Tier 2 create adapter script shipped by gtms init.
// ENH-160: renamed from skeletonCreateScript. ENH-161: template-driven --
// reads role-specific template from GTMS_TEMPLATE_FILE, falls back to heredoc.
// Uses GTMS_OUTPUT_DIR, GTMS_TC_IDS, GTMS_REFERENCE, GTMS_RESULT_FILE, GTMS_TASK_ID,
// GTMS_TEMPLATE_FILE.
const manualCreateScript = `#!/bin/sh
# manual-create-script.sh -- Tier 2 sync adapter for manual test case creation.
#
# To customise the test case shape, edit your role's template at:
#   gtms/test/templates/manual-testcase.template.md
# To customise the stamping behaviour, edit this script.

set -e

# GTMS provides these environment variables:
#   GTMS_OUTPUT_DIR      -- where to write the test case file
#   GTMS_TC_IDS          -- comma-separated list of pre-generated test case IDs
#   GTMS_REFERENCE       -- the --reference flag value (may be empty)
#   GTMS_RESULT_FILE     -- path to the handoff contract YAML
#   GTMS_TASK_ID         -- unique task identifier
#   GTMS_TEMPLATE_FILE   -- path to the role-specific testcase template

if [ -z "$GTMS_OUTPUT_DIR" ]; then
  echo "ERROR: GTMS_OUTPUT_DIR not set" >&2
  exit 1
fi

mkdir -p "$GTMS_OUTPUT_DIR"

# Take the first ID from the comma-separated list
ID=$(echo "$GTMS_TC_IDS" | cut -d',' -f1)

if [ -z "$ID" ]; then
  echo "ERROR: GTMS_TC_IDS not set or empty" >&2
  exit 1
fi

# Build filename -- include name slug if provided
if [ -n "$GTMS_TC_NAME" ]; then
  OUTFILE="$GTMS_OUTPUT_DIR/${ID}-${GTMS_TC_NAME}.md"
  NAME_VALUE="${GTMS_TC_NAME}"
else
  OUTFILE="$GTMS_OUTPUT_DIR/${ID}.md"
  NAME_VALUE=""
fi

# escape_sed: Escape sed-replacement specials (\ & |) in values.
escape_sed() { printf '%s' "$1" | sed -e 's/[\\&|]/\\&/g'; }

# yaml_escape: Encode a value for safe insertion as a YAML double-quoted scalar.
yaml_escape() {
  printf '%s' "$1" | awk '
    {
      gsub(/\\/, "\\\\")
      gsub(/"/,  "\\\"")
      gsub(/\t/, "\\t")
      gsub(/\r/, "\\r")
      if (NR > 1) printf "\\n"
      printf "%s", $0
    }
  '
}

# yaml_quoted_or_empty: For non-empty input, print a complete YAML double-quoted
# scalar (carrying its own quotes). For empty input, print nothing. Mirrors the
# Go-side yamlQuotedScalarOrEmpty helper so the substituted value either lands
# as a properly-quoted scalar in an unquoted placeholder context (e.g. the
# requirement line in the testcase template) or leaves the value position bare.
yaml_quoted_or_empty() {
  if [ -z "$1" ]; then
    printf ''
  else
    printf '"%s"' "$(yaml_escape "$1")"
  fi
}

# Prepare substitution values
ID_E=$(escape_sed "$ID")
NAME_E=$(escape_sed "$NAME_VALUE")
REF_E=$(escape_sed "$(yaml_quoted_or_empty "$GTMS_REFERENCE")")
DATE_E=$(escape_sed "$(date +%Y-%m-%d)")

# ENH-161: Read template from GTMS_TEMPLATE_FILE; fall back to heredoc if missing.
if [ -n "$GTMS_TEMPLATE_FILE" ] && [ -f "$GTMS_TEMPLATE_FILE" ]; then
  sed \
    -e "s|\${TESTCASE_ID}|${ID_E}|g" \
    -e "s|\${TITLE}|${NAME_E}|g" \
    -e "s|\${REQUIREMENT}|${REF_E}|g" \
    -e "s|\${CREATED}|${DATE_E}|g" \
    "$GTMS_TEMPLATE_FILE" > "$OUTFILE"
else
  if [ -n "$GTMS_TEMPLATE_FILE" ]; then
    echo "warning: create template not found: $GTMS_TEMPLATE_FILE -- using built-in default" >&2
  fi
  # Fallback: inline heredoc (day-one shape). REF_LINE emits a properly-quoted
  # YAML scalar when --reference is set, or a bare key when empty -- matching
  # the substitution semantics of the template-read path above.
  if [ -n "$GTMS_REFERENCE" ]; then
    REF_LINE="requirement: \"$(yaml_escape "$GTMS_REFERENCE")\""
  else
    REF_LINE="requirement: "
  fi
cat > "$OUTFILE" <<TCEOF
---
test_case_id: ${ID}
title: "${NAME_VALUE}"
${REF_LINE}
priority: Medium
type: Functional
created: $(date +%Y-%m-%d)
---

## Test Objective


## Preconditions

-

## Test Data

-

## Test Steps

1.
   - Expected observation:

## Expected Final Outcome

-

## Postconditions

-

## Notes

TCEOF
fi

# Update handoff contract
if [ -n "$GTMS_RESULT_FILE" ]; then
cat > "$GTMS_RESULT_FILE" <<REOF
task: ${GTMS_TASK_ID}
command: create
target: $(basename "$GTMS_OUTPUT_DIR")
adapter: manual-create-script
mode: sync
status: complete
result: pass
artefact: ${OUTFILE}
summary: "Created manual-create-script test case ${ID}"
completed: $(date -u +%Y-%m-%dT%H:%M:%SZ)
REOF
fi

echo "Created manual-create-script test case: ${OUTFILE}"
`

// demoLoginRequirement is the sample requirement document for the demo.
const demoLoginRequirement = `# Login Feature

## Overview

A login page that allows users to authenticate with email and password.

## Requirements

### REQ-LOGIN-001: Valid Login

Users must be able to log in with a valid email address and password. On successful authentication, the user should be redirected to the dashboard.

**Acceptance criteria:**
- Email field accepts valid email format
- Password field is masked
- Successful login redirects to /dashboard
- A session token is stored

### REQ-LOGIN-002: Invalid Credentials

When a user enters an incorrect password, the system should display an error message without revealing whether the email exists.

**Acceptance criteria:**
- Error message: "Invalid email or password"
- No account enumeration (same message for wrong email and wrong password)
- Failed attempt is logged
- User remains on the login page

### REQ-LOGIN-003: Empty Field Validation

The login form must validate that both email and password fields are filled before submission.

**Acceptance criteria:**
- Empty email shows: "Email is required"
- Empty password shows: "Password is required"
- Form is not submitted until both fields have values
- Validation messages appear inline next to the relevant field
`

// demoCreateScript is the Tier 2 create adapter script for the demo.
// It reads GTMS_OUTPUT_DIR and GTMS_TC_IDS, writes 3 pre-baked test case files.
const demoCreateScript = `#!/bin/sh
# Demo create adapter -- writes 3 pre-baked test cases using GTMS-generated IDs.
# This is a working example of a Tier 2 (script) adapter.

set -e

# GTMS provides these environment variables:
#   GTMS_OUTPUT_DIR   -- where to write test case files
#   GTMS_TC_IDS       -- comma-separated list of pre-generated test case IDs
#   GTMS_RESULT_FILE  -- path to the handoff contract YAML
#                       (e.g. .gtms/results/task-{id}.handoff.yaml)

if [ -z "$GTMS_OUTPUT_DIR" ]; then
  echo "ERROR: GTMS_OUTPUT_DIR not set" >&2
  exit 1
fi

mkdir -p "$GTMS_OUTPUT_DIR"

# Split TC IDs into an array
IFS=',' read -r ID1 ID2 ID3 REST <<EOF
$GTMS_TC_IDS
EOF

# Test case 1: Valid login
if [ -n "$ID1" ]; then
cat > "$GTMS_OUTPUT_DIR/${ID1}-login-valid-credentials.md" <<TCEOF
---
test_case_id: ${ID1}
title: "Valid login succeeds with correct credentials"
requirement: _demo
priority: High
type: Functional
created: $(date +%Y-%m-%d)
---

## Test Objective
Verify that a user can log in with valid email and password and is redirected to the dashboard.

## Preconditions
- User account exists with email "user@example.com" and password "SecurePass123"
- User is on the login page

## Test Data
- Email: user@example.com
- Password: SecurePass123

## Test Steps
1. Enter "user@example.com" in the email field
   - Expected: Email is accepted
2. Enter "SecurePass123" in the password field
   - Expected: Password is masked
3. Click the "Log In" button
   - Expected: Form is submitted

## Expected Final Outcome
- User is redirected to /dashboard
- A session token is stored
- Welcome message displays the user's name

## Postconditions
- User session is active
- Login event is recorded in audit log
TCEOF
fi

# Test case 2: Invalid password
if [ -n "$ID2" ]; then
cat > "$GTMS_OUTPUT_DIR/${ID2}-login-invalid-password.md" <<TCEOF
---
test_case_id: ${ID2}
title: "Invalid password is rejected with error message"
requirement: _demo
priority: High
type: Functional
created: $(date +%Y-%m-%d)
---

## Test Objective
Verify that an incorrect password displays a generic error message without revealing account existence.

## Preconditions
- User account exists with email "user@example.com"
- User is on the login page

## Test Data
- Email: user@example.com
- Password: WrongPassword99

## Test Steps
1. Enter "user@example.com" in the email field
   - Expected: Email is accepted
2. Enter "WrongPassword99" in the password field
   - Expected: Password is masked
3. Click the "Log In" button
   - Expected: Form is submitted

## Expected Final Outcome
- Error message displays: "Invalid email or password"
- User remains on the login page
- No session token is created

## Postconditions
- Failed login attempt is logged
- Account is not locked (first failed attempt)
TCEOF
fi

# Test case 3: Empty fields
if [ -n "$ID3" ]; then
cat > "$GTMS_OUTPUT_DIR/${ID3}-login-empty-fields.md" <<TCEOF
---
test_case_id: ${ID3}
title: "Empty fields show validation errors"
requirement: _demo
priority: Medium
type: Functional
created: $(date +%Y-%m-%d)
---

## Test Objective
Verify that submitting the login form with empty fields shows inline validation errors.

## Preconditions
- User is on the login page
- Both email and password fields are empty

## Test Data
- Email: (empty)
- Password: (empty)

## Test Steps
1. Leave the email field empty
   - Expected: No error yet (validation on submit)
2. Leave the password field empty
   - Expected: No error yet (validation on submit)
3. Click the "Log In" button
   - Expected: Form validation triggers

## Expected Final Outcome
- Email field shows: "Email is required"
- Password field shows: "Password is required"
- Form is not submitted to the server
- Validation messages appear inline next to each field

## Postconditions
- No network request was made
- User remains on the login page
TCEOF
fi

# Count files created
FILE_COUNT=$(ls "$GTMS_OUTPUT_DIR"/*.md 2>/dev/null | wc -l)

# Update handoff contract
if [ -n "$GTMS_RESULT_FILE" ]; then
cat > "$GTMS_RESULT_FILE" <<REOF
task: ${GTMS_TASK_ID}
command: create
target: _demo
adapter: demo
mode: sync
status: complete
result: pass
artefact: ${GTMS_OUTPUT_DIR}
summary: "Created ${FILE_COUNT} demo test cases"
completed: $(date -u +%Y-%m-%dT%H:%M:%SZ)
REOF
fi

echo "Created ${FILE_COUNT} demo test cases in ${GTMS_OUTPUT_DIR}"
`

// demoAutomateShScript is the Tier 2 automate adapter that generates .sh test scripts.
const demoAutomateShScript = `#!/bin/sh
# Demo automate adapter (sh) -- generates a .sh test script from a test case.
# This is a working example of a Tier 2 (script) adapter.

set -e

# GTMS provides these environment variables:
#   GTMS_TESTCASE      -- the test case ID (e.g. tc-a3f72b10)
#   GTMS_OUTPUT_DIR    -- where to write the automation script
#   GTMS_RESULT_FILE   -- path to the handoff contract YAML
#                        (e.g. .gtms/results/task-{id}.handoff.yaml)

if [ -z "$GTMS_TESTCASE" ] || [ -z "$GTMS_OUTPUT_DIR" ]; then
  echo "ERROR: GTMS_TESTCASE and GTMS_OUTPUT_DIR must be set" >&2
  exit 1
fi

mkdir -p "$GTMS_OUTPUT_DIR"

SPEC_FILE="$GTMS_OUTPUT_DIR/${GTMS_TESTCASE}-test.sh"

cat > "$SPEC_FILE" <<SPECEOF
#!/bin/sh
# Auto-generated test script for ${GTMS_TESTCASE}
# Run with: sh ${SPEC_FILE}

echo "PASS: ${GTMS_TESTCASE}"
exit 0
SPECEOF

chmod +x "$SPEC_FILE"

# Update handoff contract
if [ -n "$GTMS_RESULT_FILE" ]; then
cat > "$GTMS_RESULT_FILE" <<REOF
task: ${GTMS_TASK_ID}
command: automate
target: ${GTMS_TESTCASE}
adapter: demo-sh
mode: sync
status: complete
result: pass
artefact: ${SPEC_FILE}
summary: "Generated sh test script for ${GTMS_TESTCASE}"
completed: $(date -u +%Y-%m-%dT%H:%M:%SZ)
REOF
fi

echo "Generated test script: ${SPEC_FILE}"
`

// demoAutomateCmdScript is the Tier 2 automate adapter that generates .cmd test scripts.
const demoAutomateCmdScript = `#!/bin/sh
# Demo automate adapter (cmd) -- generates a .cmd test script from a test case.
# The adapter itself is .sh (Tier 2 requires POSIX shell), but its output is .cmd.

set -e

if [ -z "$GTMS_TESTCASE" ] || [ -z "$GTMS_OUTPUT_DIR" ]; then
  echo "ERROR: GTMS_TESTCASE and GTMS_OUTPUT_DIR must be set" >&2
  exit 1
fi

mkdir -p "$GTMS_OUTPUT_DIR"

SPEC_FILE="$GTMS_OUTPUT_DIR/${GTMS_TESTCASE}-test.cmd"

# Write a Windows batch file
printf '@echo off\r\nREM Auto-generated test script for %s\r\necho PASS: %s\r\nexit /b 0\r\n' "$GTMS_TESTCASE" "$GTMS_TESTCASE" > "$SPEC_FILE"

# Update handoff contract
if [ -n "$GTMS_RESULT_FILE" ]; then
cat > "$GTMS_RESULT_FILE" <<REOF
task: ${GTMS_TASK_ID}
command: automate
target: ${GTMS_TESTCASE}
adapter: demo-cmd
mode: sync
status: complete
result: pass
artefact: ${SPEC_FILE}
summary: "Generated cmd test script for ${GTMS_TESTCASE}"
completed: $(date -u +%Y-%m-%dT%H:%M:%SZ)
REOF
fi

echo "Generated test script: ${SPEC_FILE}"
`

// demoGuidanceInit is the demo-specific guidance for the init command.
// Replaces the default init guidance when --demo is used.
const demoGuidanceInit = `cat _demo/login-feature.md              -- read the sample requirement
gtms create --adapter demo _demo --context-file _demo/login-feature.md
`

// demoGuidanceCreate is the demo-specific guidance for the create command.
const demoGuidanceCreate = `gtms status _demo                -- see the pipeline dashboard
gtms automate --adapter demo-sh <tc-id>  -- automate a test case (or demo-cmd on Windows)
`

// demoGuidanceAutomate is the demo-specific guidance for the automate command.
const demoGuidanceAutomate = `gtms execute --adapter <same-adapter> <tc-id>  -- use the same adapter you automated with
gtms status _demo                              -- check progress
`

// demoGuidanceExecute is the demo-specific guidance for the execute command.
const demoGuidanceExecute = `gtms status _demo              -- see the pipeline dashboard
gtms gaps _demo                -- check for coverage gaps
gtms map _demo                 -- see the traceability map
`

// demoBridgeGuide is the getting-started-with-ai.md bridge document.
const demoBridgeGuide = `# Getting Started with AI Test Automation

You have completed the GTMS demo pipeline! Here is what happened:

1. **CREATE** -- An adapter generated test cases from a requirement document
2. **AUTOMATE** -- An adapter generated executable test scripts from test cases
3. **EXECUTE** -- GTMS ran the test scripts and recorded results

## What Next?

The demo used simple shell scripts as adapters. In a real project, you would
connect AI coding tools (Claude, GitHub Copilot, etc.) as adapters:

- **Create adapters** use AI to generate test cases from requirements
- **Automate adapters** use AI to generate test scripts (Playwright, Cypress, etc.)
- **Execute adapters** run test frameworks and capture results

## Setting Up Real Adapters

1. Choose a preset: ` + "`gtms init --preset manual`" + ` or ` + "`gtms init --preset bats`" + `
2. Or configure adapters manually in ` + "`gtms.config`" + `
3. See the adapter guide: https://github.com/aitestmanagement/gtms-cli#adapters

## Cleaning Up Demo Data

To remove demo artifacts:
` + "```" + `
rm -rf _demo/
rm -rf gtms/test/cases/_demo/
rm -rf gtms/automation/specs/demo-sh/
rm -rf gtms/automation/specs/demo-cmd/
` + "```" + `

Then remove the demo adapter entries from ` + "`gtms.config`" + ` and set ` + "`demo_seeded: false`" + `.
`

// batsRunnerScript is the local Tier 2 BATS execute adapter shipped by gtms init.
// ENH-127: Replaces the Tier 1 command: bats {artefact_file} default.
// The adapter classifies skip status itself via the shared TAP helper.
//
// BUG-072: this constant must mirror adapters/bats-runner.sh byte-for-byte --
// tc-798bac31 enforces that with `diff -u`. The Go concat workaround below
// embeds the in-tree script's literal backticks (which a backtick-delimited
// raw string can't contain directly).
const batsRunnerScript = `#!/bin/sh
# bats-runner.sh -- Tier 2 sync execute adapter for local BATS runs.
#
# ENH-127: Replaces the Tier 1 ` + "`command: bats {artefact_file}`" + ` default.
# The adapter classifies skip status itself via the shared TAP helper
# instead of relying on core GTMS (classifyBATSSkip was removed).
#
# Receives: GTMS_ARTEFACT_FILE, GTMS_RESULT_FILE, GTMS_TASK_ID (plus others via Tier 2 env)
# GTMS_RESULT_FILE points at .gtms/results/{task-id}.handoff.yaml -- the
# adapter writes its outcome there as YAML (status/summary/log/completed).

set -e

# --- Source shared TAP classifier ---
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
. "$SCRIPT_DIR/lib/bats-tap.sh"

# --- Scrub leaked BATS_* env from any enclosing bats run ---
# When ` + "`gtms execute`" + ` is invoked from inside a bats test (common during
# dogfood / acceptance suites), the parent bats prepends its internal
# $BATS_LIBEXEC to PATH and exports BATS_* vars. PATH lookup then resolves
# ` + "`bats`" + ` to the libexec entry script, which expects helpers that only the
# user-facing wrapper sources -- invoking it directly fails with errors like
# "bats_readlinkf: command not found". Strip libexec entries from PATH and
# unset BATS_* before running our own bats so we always go through the
# wrapper.
unset BATS_LIBEXEC BATS_ROOT BATS_CWD BATS_TMPDIR BATS_ROOT_PID BATS_VERSION
PATH=$(printf '%s' "$PATH" | tr ':' '\n' | grep -vE '/libexec/bats-core/?$' | tr '\n' ':' | sed 's/:$//')
export PATH

# --- Run BATS locally ---
set +e
OUTPUT=$(bats "$GTMS_ARTEFACT_FILE" 2>&1)
EXIT_CODE=$?
set -e

# --- Classify result ---
# Check for a TAP plan line to detect whether bats ran at all.
HAS_PLAN=0
if echo "${OUTPUT}" | grep -Eq '^1\.\.[0-9]+$'; then
    HAS_PLAN=1
fi

if [ "${HAS_PLAN}" = "0" ]; then
    # No TAP plan line -- bats itself couldn't run or produced garbage
    STATUS="error"
    RESULT=""
    SUMMARY="Malformed or missing TAP output (exit ${EXIT_CODE})"
else
    # Valid TAP -- classify via shared helper
    CLASSIFIED=$(echo "${OUTPUT}" | classify_bats_status)
    case "$CLASSIFIED" in
        pass)
            STATUS="complete"
            RESULT="pass"
            PASS_COUNT=$(echo "${OUTPUT}" | grep -Ec '^ok ' || true)
            SUMMARY="All ${PASS_COUNT} tests passed"
            ;;
        fail)
            STATUS="complete"
            RESULT="fail"
            PASS_COUNT=$(echo "${OUTPUT}" | grep -Ec '^ok ' || true)
            FAIL_COUNT=$(echo "${OUTPUT}" | grep -Ec '^not ok ' || true)
            SUMMARY="${PASS_COUNT} passed, ${FAIL_COUNT} failed"
            ;;
        skipped)
            STATUS="complete"
            RESULT="skip"
            PASS_COUNT=$(echo "${OUTPUT}" | grep -Ec '^ok ' || true)
            SKIP_COUNT=$(echo "${OUTPUT}" | grep -Eci '^ok .* # skip' || true)
            PASS_ONLY=$((PASS_COUNT - SKIP_COUNT))
            if [ "$PASS_ONLY" -eq 0 ]; then
                SUMMARY="All ${SKIP_COUNT} tests skipped"
            else
                SUMMARY="${PASS_ONLY} passed, ${SKIP_COUNT} skipped"
            fi
            ;;
        error)
            STATUS="error"
            RESULT=""
            SUMMARY="No test results found in TAP output (exit ${EXIT_CODE})"
            ;;
    esac
fi

# --- Update result contract ---
# ENH-130: orthogonal contract -- status carries adapter state, result carries test outcome.
# Only include result: field when RESULT is non-empty (omit for error/no-outcome cases).
if [ -n "${RESULT}" ]; then
cat > "${GTMS_RESULT_FILE}" <<EOF
task: ${GTMS_TASK_ID}
command: execute
target: ${GTMS_ARTEFACT_FILE}
adapter: bats-runner
mode: sync
status: ${STATUS}
result: ${RESULT}
summary: "${SUMMARY}"
log: |
$(echo "${OUTPUT}" | sed 's/^/  /')
completed: $(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF
else
cat > "${GTMS_RESULT_FILE}" <<EOF
task: ${GTMS_TASK_ID}
command: execute
target: ${GTMS_ARTEFACT_FILE}
adapter: bats-runner
mode: sync
status: ${STATUS}
summary: "${SUMMARY}"
log: |
$(echo "${OUTPUT}" | sed 's/^/  /')
completed: $(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF
fi

# Echo summary to stdout for GTMS CLI output
echo "${OUTPUT}"

# Exit 0 for complete (adapter ran successfully), 1 for error (adapter broke)
if [ "${STATUS}" = "complete" ]; then
    exit 0
else
    exit 1
fi
`

// manualResultTemplate is the four-section YAML template for manual test results.
// Uses ${VAR} placeholders that the manual-prime adapter substitutes via sed.
// ENH-132: CON-020 Decision 3 -- no inline value-hint comments on field lines
// (those collide with ENH-E snippet expansion).
// BUG-077: four sections (GTMS contract -> OVERALL RESULT -> Optional metadata -> Steps).
// Result snippets are value-first; step snippets expand under `steps:`.
const manualResultTemplate = `# yaml-language-server: $schema=../../schemas/manual-result.schema.json
# -- GTMS contract (do not edit) ------------------------------------------
test_case_id: ${TESTCASE}
test_case_hash: ${TESTCASE_HASH}
framework: manual

# -- OVERALL RESULT -------------------------------------------------------
result:

# -- Optional metadata ----------------------------------------------------
title: "${TC_TITLE}"
requirement: "${TC_REQUIREMENT}"
priority: "${TC_PRIORITY}"
type: "${TC_TYPE}"
branch: ${BRANCH}

# -- Steps (optional) -----------------------------------------------------
steps:
`

// AgentResultTemplateYAML is the day-one agent result template content.
// ENH-161: byte-identical to manualResultTemplate on day one. Separate
// constant for the scaffold to write to agent-result.template.yaml.
// The divergence is in the file (two files), not the content.
const AgentResultTemplateYAML = manualResultTemplate

// manualResultSchema is the JSON Schema for manual-result.schema.json.
// ENH-132/ENH-142: Requires test_case_id, test_case_hash, framework, result.
// BUG-077: enumerates snippet-emitted fields (steps, executed_by, executed_at,
// defect, skip_reason). result type relaxed to accept null so freshly-stamped
// templates (bare `result:` key) are schema-valid in the editor.
// Allows additional user fields (additionalProperties: true per CON-020 Decision 11).
const manualResultSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "GTMS Manual Test Result",
  "description": "Schema for user-authored manual test result files.",
  "type": "object",
  "required": ["test_case_id", "test_case_hash", "framework", "result"],
  "properties": {
    "test_case_id": {
      "type": "string",
      "pattern": "^tc-[a-f0-9]{8}$",
      "description": "GTMS contract: test case identifier"
    },
    "test_case_hash": {
      "type": "string",
      "pattern": "^[a-f0-9]{16}$",
      "description": "GTMS contract: SHA-256 hash of the test case file content at prime time (16 hex chars)"
    },
    "framework": {
      "type": "string",
      "const": "manual",
      "description": "GTMS contract: test framework identifier"
    },
    "result": {
      "type": ["string", "null"],
      "enum": ["pass", "fail", "skip", null],
      "description": "REQUIRED: test outcome recorded by the tester (null while freshly stamped)"
    },
    "title": {
      "type": "string",
      "description": "Optional metadata: test case title snapshot at prime time"
    },
    "requirement": {
      "type": "string",
      "description": "Optional metadata: requirement reference snapshot at prime time"
    },
    "priority": {
      "type": "string",
      "description": "Optional metadata: test case priority snapshot at prime time"
    },
    "type": {
      "type": "string",
      "description": "Optional metadata: test case type snapshot at prime time"
    },
    "branch": {
      "type": "string",
      "description": "Optional metadata: git branch at prime time"
    },
    "executed_by": {
      "type": "string",
      "description": "Identity that recorded the result (stamped by gtms-pass/fail/skip snippet)"
    },
    "executed_at": {
      "type": "string",
      "description": "Timestamp when the result was recorded -- ISO 8601 local time, no timezone enforcement"
    },
    "defect": {
      "type": "array",
      "items": { "type": "string" },
      "description": "Defect identifiers linked to a fail result (e.g. JIRA ticket IDs)"
    },
    "skip_reason": {
      "type": "string",
      "description": "Reason for skipping the test (paired with result: skip)"
    },
    "steps": {
      "type": ["array", "null"],
      "description": "Optional step-level results (null while freshly stamped)",
      "items": {
        "type": "object",
        "properties": {
          "step": {
            "type": "string",
            "description": "Step identifier"
          },
          "name": {
            "type": "string",
            "description": "Step description"
          },
          "status": {
            "type": "string",
            "enum": ["pass", "fail", "skip"],
            "description": "Step outcome"
          },
          "notes": {
            "type": "string",
            "description": "Observations for this step"
          },
          "defect": {
            "type": "array",
            "items": { "type": "string" },
            "description": "Defect identifiers linked to a failing step"
          }
        },
        "required": ["step", "name", "status"]
      }
    }
  },
  "additionalProperties": true
}
`

// manualPrimeScript is the Tier 2 sync adapter for manual test result preparation.
// ENH-160: renamed from manual-prime.sh to manual-prime-script.sh. Adapter
// identity updated from "manual-prime" to "manual-prime-script".
// ENH-132: stamps one blank manual result template for the given test case.
const manualPrimeScript = `#!/bin/sh
# manual-prime-script.sh -- Tier 2 sync adapter for manual test result preparation.
# Stamps one blank manual result template for the given test case.
#
# Receives: GTMS_TESTCASE, GTMS_TESTCASE_HASH, GTMS_TEMPLATE_FILE,
#           GTMS_OUTPUT_FILE, GTMS_BRANCH, GTMS_RESULT_FILE, GTMS_TASK_ID,
#           GTMS_FORCE, GTMS_TC_TITLE, GTMS_TC_REQUIREMENT,
#           GTMS_TC_PRIORITY, GTMS_TC_TYPE

set -e

# Validate required env vars
for var in GTMS_TESTCASE GTMS_TESTCASE_HASH GTMS_TEMPLATE_FILE GTMS_OUTPUT_FILE GTMS_BRANCH GTMS_RESULT_FILE GTMS_TASK_ID; do
  eval "val=\$$var"
  if [ -z "$val" ]; then
    echo "ERROR: $var not set" >&2
    exit 1
  fi
done

# Safety: refuse to overwrite existing result file unless --force
if [ -f "$GTMS_OUTPUT_FILE" ] && [ "$GTMS_FORCE" != "true" ]; then
  echo "Manual result file already exists: $GTMS_OUTPUT_FILE" >&2
  echo "Use --force to overwrite, or delete the file manually." >&2
  exit 1
fi

# Ensure output directory exists
mkdir -p "$(dirname "$GTMS_OUTPUT_FILE")"

# Stamp result from template with variable substitution.
# escape_sed: Escape sed-replacement specials (\ & |) in values so branch names
# containing those characters do not corrupt the substitution.
escape_sed() { printf '%s' "$1" | sed -e 's/[\\&|]/\\&/g'; }

# yaml_escape: Encode a value for safe insertion as a YAML double-quoted scalar.
# Escapes backslash, double-quote, tab, CR, and embedded newlines (folded/literal
# scalars in TC frontmatter can legally produce multi-line strings). Output is
# always single-line so the downstream sed pipeline cannot split on a raw \n.
# Must be applied BEFORE escape_sed so the YAML escape sequences survive sed
# insertion.
yaml_escape() {
  printf '%s' "$1" | awk '
    {
      gsub(/\\/, "\\\\")
      gsub(/"/,  "\\\"")
      gsub(/\t/, "\\t")
      gsub(/\r/, "\\r")
      if (NR > 1) printf "\\n"
      printf "%s", $0
    }
  '
}

TESTCASE_E=$(escape_sed "$GTMS_TESTCASE")
TESTCASE_HASH_E=$(escape_sed "$GTMS_TESTCASE_HASH")
BRANCH_E=$(escape_sed "$GTMS_BRANCH")

# Snapshot fields: yaml_escape first (\ -> \\, " -> \"), then escape_sed.
TC_TITLE_E=$(escape_sed "$(yaml_escape "$GTMS_TC_TITLE")")
TC_REQUIREMENT_E=$(escape_sed "$(yaml_escape "$GTMS_TC_REQUIREMENT")")
TC_PRIORITY_E=$(escape_sed "$(yaml_escape "$GTMS_TC_PRIORITY")")
TC_TYPE_E=$(escape_sed "$(yaml_escape "$GTMS_TC_TYPE")")

# ENH-161: Check template exists; fall back to inline heredoc if missing.
if [ -f "$GTMS_TEMPLATE_FILE" ]; then
  sed \
    -e "s|\${TESTCASE}|${TESTCASE_E}|g" \
    -e "s|\${TESTCASE_HASH}|${TESTCASE_HASH_E}|g" \
    -e "s|\${BRANCH}|${BRANCH_E}|g" \
    -e "s|\${TC_TITLE}|${TC_TITLE_E}|g" \
    -e "s|\${TC_REQUIREMENT}|${TC_REQUIREMENT_E}|g" \
    -e "s|\${TC_PRIORITY}|${TC_PRIORITY_E}|g" \
    -e "s|\${TC_TYPE}|${TC_TYPE_E}|g" \
    "$GTMS_TEMPLATE_FILE" > "$GTMS_OUTPUT_FILE"
else
  echo "warning: prime template not found: $GTMS_TEMPLATE_FILE -- using built-in default" >&2
  # ENH-161 AC #20: byte-identical fallback output to Tier 0 manualResultTemplateFallback
  # (internal/adapter/builtin_action.go). Section-divider comments are preserved so the
  # Tier 0 / Tier 2 missing-template paths produce identical artefacts.
  cat > "$GTMS_OUTPUT_FILE" <<FALLBACKEOF
# yaml-language-server: \$schema=../../schemas/manual-result.schema.json
# -- GTMS contract (do not edit) ------------------------------------------
test_case_id: ${GTMS_TESTCASE}
test_case_hash: ${GTMS_TESTCASE_HASH}
framework: manual

# -- OVERALL RESULT -------------------------------------------------------
result:

# -- Optional metadata ----------------------------------------------------
title: "$(yaml_escape "$GTMS_TC_TITLE")"
requirement: "$(yaml_escape "$GTMS_TC_REQUIREMENT")"
priority: "$(yaml_escape "$GTMS_TC_PRIORITY")"
type: "$(yaml_escape "$GTMS_TC_TYPE")"
branch: ${GTMS_BRANCH}

# -- Steps (optional) -----------------------------------------------------
steps:
FALLBACKEOF
fi

# Update handoff contract -- ENH-130 orthogonal shape
cat > "$GTMS_RESULT_FILE" <<EOF
task: ${GTMS_TASK_ID}
command: automate
target: ${GTMS_TESTCASE}
adapter: manual-prime-script
mode: sync
status: complete
result: pass
artefact: ${GTMS_OUTPUT_FILE}
summary: "Stamped manual result template for ${GTMS_TESTCASE}"
completed: $(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF

echo "Stamped manual result template: ${GTMS_OUTPUT_FILE}"
`

// agentPrimeScript is the Tier 2 sync adapter for agent test result preparation.
// ENH-160: identical to manualPrimeScript on day one, separate name for
// future agent-specific customisation. Registered as dormant slot in
// gtms.config (adapters.prime.agent-prime-script).
const agentPrimeScript = `#!/bin/sh
# agent-prime-script.sh -- Tier 2 sync adapter for agent test result preparation.
# Stamps one blank manual result template for the given test case.
# Edit this script to customise the agent prime flow.
#
# Receives: GTMS_TESTCASE, GTMS_TESTCASE_HASH, GTMS_TEMPLATE_FILE,
#           GTMS_OUTPUT_FILE, GTMS_BRANCH, GTMS_RESULT_FILE, GTMS_TASK_ID,
#           GTMS_FORCE, GTMS_TC_TITLE, GTMS_TC_REQUIREMENT,
#           GTMS_TC_PRIORITY, GTMS_TC_TYPE

set -e

# Validate required env vars
for var in GTMS_TESTCASE GTMS_TESTCASE_HASH GTMS_TEMPLATE_FILE GTMS_OUTPUT_FILE GTMS_BRANCH GTMS_RESULT_FILE GTMS_TASK_ID; do
  eval "val=\$$var"
  if [ -z "$val" ]; then
    echo "ERROR: $var not set" >&2
    exit 1
  fi
done

# Safety: refuse to overwrite existing result file unless --force
if [ -f "$GTMS_OUTPUT_FILE" ] && [ "$GTMS_FORCE" != "true" ]; then
  echo "Manual result file already exists: $GTMS_OUTPUT_FILE" >&2
  echo "Use --force to overwrite, or delete the file manually." >&2
  exit 1
fi

# Ensure output directory exists
mkdir -p "$(dirname "$GTMS_OUTPUT_FILE")"

# Stamp result from template with variable substitution.
# escape_sed: Escape sed-replacement specials (\ & |) in values so branch names
# containing those characters do not corrupt the substitution.
escape_sed() { printf '%s' "$1" | sed -e 's/[\\&|]/\\&/g'; }

# yaml_escape: Encode a value for safe insertion as a YAML double-quoted scalar.
yaml_escape() {
  printf '%s' "$1" | awk '
    {
      gsub(/\\/, "\\\\")
      gsub(/"/,  "\\\"")
      gsub(/\t/, "\\t")
      gsub(/\r/, "\\r")
      if (NR > 1) printf "\\n"
      printf "%s", $0
    }
  '
}

TESTCASE_E=$(escape_sed "$GTMS_TESTCASE")
TESTCASE_HASH_E=$(escape_sed "$GTMS_TESTCASE_HASH")
BRANCH_E=$(escape_sed "$GTMS_BRANCH")

# Snapshot fields: yaml_escape first (\ -> \\, " -> \"), then escape_sed.
TC_TITLE_E=$(escape_sed "$(yaml_escape "$GTMS_TC_TITLE")")
TC_REQUIREMENT_E=$(escape_sed "$(yaml_escape "$GTMS_TC_REQUIREMENT")")
TC_PRIORITY_E=$(escape_sed "$(yaml_escape "$GTMS_TC_PRIORITY")")
TC_TYPE_E=$(escape_sed "$(yaml_escape "$GTMS_TC_TYPE")")

# ENH-161: Check template exists; fall back to inline heredoc if missing.
if [ -f "$GTMS_TEMPLATE_FILE" ]; then
  sed \
    -e "s|\${TESTCASE}|${TESTCASE_E}|g" \
    -e "s|\${TESTCASE_HASH}|${TESTCASE_HASH_E}|g" \
    -e "s|\${BRANCH}|${BRANCH_E}|g" \
    -e "s|\${TC_TITLE}|${TC_TITLE_E}|g" \
    -e "s|\${TC_REQUIREMENT}|${TC_REQUIREMENT_E}|g" \
    -e "s|\${TC_PRIORITY}|${TC_PRIORITY_E}|g" \
    -e "s|\${TC_TYPE}|${TC_TYPE_E}|g" \
    "$GTMS_TEMPLATE_FILE" > "$GTMS_OUTPUT_FILE"
else
  echo "warning: prime template not found: $GTMS_TEMPLATE_FILE -- using built-in default" >&2
  # ENH-161 AC #20: byte-identical fallback output to Tier 0 manualResultTemplateFallback
  # (internal/adapter/builtin_action.go). Section-divider comments are preserved so the
  # Tier 0 / Tier 2 missing-template paths produce identical artefacts.
  cat > "$GTMS_OUTPUT_FILE" <<FALLBACKEOF
# yaml-language-server: \$schema=../../schemas/manual-result.schema.json
# -- GTMS contract (do not edit) ------------------------------------------
test_case_id: ${GTMS_TESTCASE}
test_case_hash: ${GTMS_TESTCASE_HASH}
framework: manual

# -- OVERALL RESULT -------------------------------------------------------
result:

# -- Optional metadata ----------------------------------------------------
title: "$(yaml_escape "$GTMS_TC_TITLE")"
requirement: "$(yaml_escape "$GTMS_TC_REQUIREMENT")"
priority: "$(yaml_escape "$GTMS_TC_PRIORITY")"
type: "$(yaml_escape "$GTMS_TC_TYPE")"
branch: ${GTMS_BRANCH}

# -- Steps (optional) -----------------------------------------------------
steps:
FALLBACKEOF
fi

# Update handoff contract -- ENH-130 orthogonal shape
cat > "$GTMS_RESULT_FILE" <<EOF
task: ${GTMS_TASK_ID}
command: automate
target: ${GTMS_TESTCASE}
adapter: agent-prime-script
mode: sync
status: complete
result: pass
artefact: ${GTMS_OUTPUT_FILE}
summary: "Stamped manual result template for ${GTMS_TESTCASE}"
completed: $(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF

echo "Stamped manual result template: ${GTMS_OUTPUT_FILE}"
`

// manualExecuteScript is the Tier 2 sync adapter for manual test result execution.
// ENH-160: renamed from manual-execute.sh to manual-execute-script.sh. Adapter
// identity updated from "manual-execute" to "manual-execute-script".
// ENH-133: reads parsed values from GTMS env vars (Go-side YAML parsing),
// computes drift diagnostics, and writes the handoff contract.
const manualExecuteScript = `#!/bin/sh
# manual-execute-script.sh -- Tier 2 sync adapter for manual test result execution.
# Reads parsed values from GTMS env vars (Go-side YAML parsing handles
# validation and field extraction), computes drift diagnostics, and writes
# the handoff contract.
#
# Receives: GTMS_TASK_ID, GTMS_TESTCASE, GTMS_TESTCASE_HASH,
#           GTMS_RESULT_TEMPLATE, GTMS_RESULT_VALUE,
#           GTMS_RESULT_TESTCASE_HASH, GTMS_RESULT_FILE

set -e

# Validate required env vars
for var in GTMS_TASK_ID GTMS_TESTCASE GTMS_TESTCASE_HASH GTMS_RESULT_TEMPLATE GTMS_RESULT_VALUE GTMS_RESULT_FILE; do
  eval "val=\$$var"
  if [ -z "$val" ]; then
    echo "ERROR: $var not set" >&2
    exit 1
  fi
done

# --- Drift detection ---
# Compare current testcase hash (GTMS_TESTCASE_HASH) with prime-time hash
# from the result file (GTMS_RESULT_TESTCASE_HASH).
if [ -n "$GTMS_RESULT_TESTCASE_HASH" ] && [ "$GTMS_TESTCASE_HASH" != "$GTMS_RESULT_TESTCASE_HASH" ]; then
  DRIFT_TIMESTAMP=$(date -u +%Y-%m-%dT%H:%M:%SZ)

  # Idempotent: remove existing drift lines before appending fresh ones.
  # Use grep -v to filter, write to temp, then move back.
  TMPFILE="${GTMS_RESULT_TEMPLATE}.tmp"
  grep -v '^drift-detected:' "$GTMS_RESULT_TEMPLATE" | \
    grep -v '^drift-detected-at:' | \
    grep -v '^test_case_hash_at_execute:' > "$TMPFILE" || true
  mv "$TMPFILE" "$GTMS_RESULT_TEMPLATE"

  # Append drift diagnostic fields
  printf '\ndrift-detected: true\n' >> "$GTMS_RESULT_TEMPLATE"
  printf 'drift-detected-at: %s\n' "$DRIFT_TIMESTAMP" >> "$GTMS_RESULT_TEMPLATE"
  printf 'test_case_hash_at_execute: %s\n' "$GTMS_TESTCASE_HASH" >> "$GTMS_RESULT_TEMPLATE"

  echo "WARN: test case has changed since prime -- drift diagnostics recorded" >&2
fi

# --- Write handoff contract ---
cat > "$GTMS_RESULT_FILE" <<EOF
task: ${GTMS_TASK_ID}
command: execute
target: ${GTMS_TESTCASE}
adapter: manual-execute-script
mode: sync
status: complete
result: ${GTMS_RESULT_VALUE}
artefact: ${GTMS_RESULT_TEMPLATE}
summary: "Manual execute result for ${GTMS_TESTCASE}: ${GTMS_RESULT_VALUE}"
completed: $(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF

echo "Manual execute recorded: ${GTMS_TESTCASE} -> ${GTMS_RESULT_VALUE}"
`

// agentExecuteScript is the Tier 2 sync adapter for agent test result execution.
// ENH-160: identical to manualExecuteScript on day one, separate name for
// future agent-specific customisation. Registered as dormant slot in
// gtms.config (adapters.execute.agent-execute-script).
const agentExecuteScript = `#!/bin/sh
# agent-execute-script.sh -- Tier 2 sync adapter for agent test result execution.
# Reads parsed values from GTMS env vars (Go-side YAML parsing handles
# validation and field extraction), computes drift diagnostics, and writes
# the handoff contract.
#
# Receives: GTMS_TASK_ID, GTMS_TESTCASE, GTMS_TESTCASE_HASH,
#           GTMS_RESULT_TEMPLATE, GTMS_RESULT_VALUE,
#           GTMS_RESULT_TESTCASE_HASH, GTMS_RESULT_FILE

set -e

# Validate required env vars
for var in GTMS_TASK_ID GTMS_TESTCASE GTMS_TESTCASE_HASH GTMS_RESULT_TEMPLATE GTMS_RESULT_VALUE GTMS_RESULT_FILE; do
  eval "val=\$$var"
  if [ -z "$val" ]; then
    echo "ERROR: $var not set" >&2
    exit 1
  fi
done

# --- Drift detection ---
# Compare current testcase hash (GTMS_TESTCASE_HASH) with prime-time hash
# from the result file (GTMS_RESULT_TESTCASE_HASH).
if [ -n "$GTMS_RESULT_TESTCASE_HASH" ] && [ "$GTMS_TESTCASE_HASH" != "$GTMS_RESULT_TESTCASE_HASH" ]; then
  DRIFT_TIMESTAMP=$(date -u +%Y-%m-%dT%H:%M:%SZ)

  # Idempotent: remove existing drift lines before appending fresh ones.
  # Use grep -v to filter, write to temp, then move back.
  TMPFILE="${GTMS_RESULT_TEMPLATE}.tmp"
  grep -v '^drift-detected:' "$GTMS_RESULT_TEMPLATE" | \
    grep -v '^drift-detected-at:' | \
    grep -v '^test_case_hash_at_execute:' > "$TMPFILE" || true
  mv "$TMPFILE" "$GTMS_RESULT_TEMPLATE"

  # Append drift diagnostic fields
  printf '\ndrift-detected: true\n' >> "$GTMS_RESULT_TEMPLATE"
  printf 'drift-detected-at: %s\n' "$DRIFT_TIMESTAMP" >> "$GTMS_RESULT_TEMPLATE"
  printf 'test_case_hash_at_execute: %s\n' "$GTMS_TESTCASE_HASH" >> "$GTMS_RESULT_TEMPLATE"

  echo "WARN: test case has changed since prime -- drift diagnostics recorded" >&2
fi

# --- Write handoff contract ---
cat > "$GTMS_RESULT_FILE" <<EOF
task: ${GTMS_TASK_ID}
command: execute
target: ${GTMS_TESTCASE}
adapter: agent-execute-script
mode: sync
status: complete
result: ${GTMS_RESULT_VALUE}
artefact: ${GTMS_RESULT_TEMPLATE}
summary: "Agent execute result for ${GTMS_TESTCASE}: ${GTMS_RESULT_VALUE}"
completed: $(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF

echo "Agent execute recorded: ${GTMS_TESTCASE} -> ${GTMS_RESULT_VALUE}"
`

// vscodeSettingsTemplate is the basic .vscode/settings.json for schema wiring.
// ENH-132: written only when .vscode/settings.json does not already exist.
const vscodeSettingsTemplate = `{
  "yaml.schemas": {
    "gtms/schemas/manual-result.schema.json": "gtms/manual/records/*.result.yaml"
  }
}
`

// vscodeExtensionsTemplate is the .vscode/extensions.json with Red Hat YAML recommendation.
// ENH-132: written only when .vscode/extensions.json does not already exist.
const vscodeExtensionsTemplate = `{
  "recommendations": [
    "redhat.vscode-yaml"
  ]
}
`

// batsTapHelper is the shared TAP classifier sourced by BATS execute adapters.
// ENH-127: Provides classify_bats_status. ENH-126: mixed pass+skip -> skipped.
const batsTapHelper = `#!/bin/sh
# bats-tap.sh -- shared TAP classifier for BATS execute adapters.
#
# ENH-127: Moved from internal/adapter/invoker.go (classifyBATSSkip) into a
# sourced shell helper so every BATS adapter classifies skip status itself
# instead of relying on core GTMS.
#
# Usage:
#   . "$(dirname "$0")/lib/bats-tap.sh"
#   STATUS=$(echo "$TAP_OUTPUT" | classify_bats_status)
#
# classify_bats_status reads TAP output from stdin and prints one of:
#   pass     -- at least one result line, not all skips, no failures
#   fail     -- at least one "not ok" result line
#   skipped  -- every TAP result line is a skip (>=1 skip, zero passes, zero fails)
#   error    -- no TAP result lines found at all
#
# Semantic parity with the deleted Go function classifyBATSSkip (ENH-094):
#   - any "not ok" line                          -> fail
#   - every result line is "ok N # skip ..." (>=1) -> skipped  (all-skip rule)
#   - all-pass with no skips                     -> pass
#   - mixed pass+skip (>=1 pass, >=1 skip)       -> skipped   (ENH-126: any skip demotes)
#   - no result lines at all                     -> error

classify_bats_status() {
    local result_lines=0
    local skip_lines=0
    local fail_lines=0

    while IFS= read -r line; do
        # Check for "not ok" first (fail).
        case "$line" in
            "not ok "*)
                result_lines=$((result_lines + 1))
                fail_lines=$((fail_lines + 1))
                ;;
            "ok "*)
                # Check for skip directive: "# skip" (case-insensitive).
                if echo "$line" | grep -qi '# skip'; then
                    result_lines=$((result_lines + 1))
                    skip_lines=$((skip_lines + 1))
                else
                    result_lines=$((result_lines + 1))
                fi
                ;;
        esac
    done

    if [ "$result_lines" -eq 0 ]; then
        echo "error"
    elif [ "$fail_lines" -gt 0 ]; then
        echo "fail"
    elif [ "$skip_lines" -gt 0 ]; then
        # ENH-126: any skip without fail demotes to skipped (mixed pass+skip)
        echo "skipped"
    else
        echo "pass"
    fi
}
`

// playwrightRunnerScript is the local Tier 2 Playwright execute adapter shipped
// by gtms init --preset playwright. BUG-111: deliberately skeletal -- runs a
// single TC-specific .spec.ts file. Does not implement ENH-152 append mode,
// shared-file selection, or JUnit parsing.
const playwrightRunnerScript = `#!/bin/sh
# playwright-runner.sh -- Tier 2 sync execute adapter for local Playwright runs.
# Runs a single TC-specific .spec.ts file via npx playwright test.
# BUG-111: deliberately skeletal -- does not implement ENH-152 append mode,
# shared-file selection, or JUnit parsing.

set -e

# --- Check tooling ---
if ! command -v npx >/dev/null 2>&1; then
  # BUG-111 AC #15/#30: actionable diagnostic on missing local Node/Playwright
  # tooling. The summary is duplicated in the result contract so it surfaces
  # via formatExecuteOutput when the adapter exits with status: error -- the
  # adapter's own stderr stream is captured but not threaded through to the
  # parent gtms process today.
  DIAG="Playwright or Node not found -- gtms init does not install Node, Playwright, browsers, or third-party tooling. Install Node and run 'npx playwright install', then re-run."
  echo "$DIAG" >&2
  cat > "${GTMS_RESULT_FILE}" <<EOF
task: ${GTMS_TASK_ID}
command: execute
target: ${GTMS_ARTEFACT_FILE}
adapter: playwright-runner
mode: sync
status: error
summary: "${DIAG}"
completed: $(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF
  exit 1
fi

# --- Run playwright test ---
set +e
OUTPUT=$(npx playwright test "${GTMS_ARTEFACT_FILE}" 2>&1)
EXIT_CODE=$?
set -e

# --- Classify result (simple exit-code mapping) ---
if [ "$EXIT_CODE" -eq 0 ]; then
  STATUS="complete"
  RESULT="pass"
  SUMMARY="Playwright test passed"
else
  STATUS="complete"
  RESULT="fail"
  SUMMARY="Playwright test failed (exit ${EXIT_CODE})"
fi

# --- Write handoff contract ---
cat > "${GTMS_RESULT_FILE}" <<EOF
task: ${GTMS_TASK_ID}
command: execute
target: ${GTMS_ARTEFACT_FILE}
adapter: playwright-runner
mode: sync
status: ${STATUS}
result: ${RESULT}
summary: "${SUMMARY}"
log: |
$(echo "${OUTPUT}" | sed 's/^/  /')
completed: $(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF

echo "${OUTPUT}"
exit 0
`

// tasksReadmeContent is the warning file placed at gtms/tasks/.README.md.
// ENH-135: Signals that the tasks directory is GTMS-managed state.
const tasksReadmeContent = `GTMS-managed state -- do not edit by hand. See PROCESS.md for the task lifecycle.
`

// vscodeGtmsSnippets is the VSCode snippet library for manual result authoring.
// ENH-135: Reverses BUG-027 Finding 1 -- snippets are appropriate now that
// manual result files are hand-edited YAML (CON-020), not skeleton-generated.
// BUG-077: result snippets are value-first (template owns the `result:` key);
// step snippets carry two-space indentation; `defect:` is a YAML list.
//
// Frontmatter result snippets: gtms-pass, gtms-fail, gtms-skip
// Body step-result snippets: gtms-step-pass, gtms-step-fail, gtms-step-skip
const vscodeGtmsSnippets = `{
  // GTMS manual result snippets -- expand in --manual.result.yaml files
  // under gtms/manual/records/.
  // BUG-027 originally removed snippets for skeleton TC files.
  // CON-020 reverses this: manual result files are hand-edited YAML,
  // so snippet support is appropriate for the result-file authoring flow.
  // BUG-077: result snippets are value-first; step snippets two-space indented.

  "gtms-pass": {
    "prefix": "gtms-pass",
    "scope": "yaml",
    "description": "Record a PASS result -- expand after 'result: '",
    "body": [
      "pass",
      "executed_by: ${1:tester}",
      "executed_at: $CURRENT_YEAR-$CURRENT_MONTH-${CURRENT_DATE}T$CURRENT_HOUR:$CURRENT_MINUTE:$CURRENT_SECOND"
    ]
  },
  "gtms-fail": {
    "prefix": "gtms-fail",
    "scope": "yaml",
    "description": "Record a FAIL result -- expand after 'result: '",
    "body": [
      "fail",
      "executed_by: ${1:tester}",
      "executed_at: $CURRENT_YEAR-$CURRENT_MONTH-${CURRENT_DATE}T$CURRENT_HOUR:$CURRENT_MINUTE:$CURRENT_SECOND",
      "defect:",
      "  - ${2:JIRA-XXX}"
    ]
  },
  "gtms-skip": {
    "prefix": "gtms-skip",
    "scope": "yaml",
    "description": "Record a SKIP result -- expand after 'result: '",
    "body": [
      "skip",
      "executed_by: ${1:tester}",
      "executed_at: $CURRENT_YEAR-$CURRENT_MONTH-${CURRENT_DATE}T$CURRENT_HOUR:$CURRENT_MINUTE:$CURRENT_SECOND",
      "skip_reason: ${2:reason}"
    ]
  },
  "gtms-step-pass": {
    "prefix": "gtms-step-pass",
    "scope": "yaml",
    "description": "Add a passing step under the steps: section -- expand on a blank line below steps:",
    "body": [
      "  - step: ${1:step-id}",
      "    name: ${2:Step description}",
      "    status: pass",
      "    notes: ${3:Observed expected behaviour}"
    ]
  },
  "gtms-step-fail": {
    "prefix": "gtms-step-fail",
    "scope": "yaml",
    "description": "Add a failing step under the steps: section -- expand on a blank line below steps:",
    "body": [
      "  - step: ${1:step-id}",
      "    name: ${2:Step description}",
      "    status: fail",
      "    notes: ${3:What went wrong}",
      "    defect:",
      "      - ${4:JIRA-XXX}"
    ]
  },
  "gtms-step-skip": {
    "prefix": "gtms-step-skip",
    "scope": "yaml",
    "description": "Add a skipped step under the steps: section -- expand on a blank line below steps:",
    "body": [
      "  - step: ${1:step-id}",
      "    name: ${2:Step description}",
      "    status: skip",
      "    notes: ${3:Why this step was skipped}"
    ]
  }
}
`
