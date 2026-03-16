# GTMS Adapter Guide

*Working reference for building, testing, and reviewing adapters.*

---

## Purpose

This document is the practical reference for anyone building a GTMS adapter. It describes the adapter interface contract, configuration format, environment variables, result reporting, and file output conventions.

It replaces the original [Command & Adapter Pattern](./archive/gtms-command-adapter-pattern.md) (archived). Design rationale is captured in [ADR-005](./adr/ADR-005-result-contract-pattern.md) and [ADR-006](./adr/ADR-006-command-scoped-adapter-registration.md).

**This is a living document.** Sections are marked with their implementation status:
- **Implemented** — working in the current codebase
- **Planned** — designed but not yet implemented (with reference to the relevant enhancement or issue)

---

## Terminology

**Adapter** — a named, pluggable connector that does the actual work for a GTMS command. GTMS says *what* to do ("create test cases for JIRA-456"); the adapter decides *how* (spawn Claude Code, trigger a GitHub workflow, run Playwright). Adapters are registered in `gtms.config` under the command they serve.

**Sync adapter** — runs and finishes before returning control. GTMS waits, then shows the result. Like running a shell command.

**Async adapter** — kicks off work and returns immediately. The work happens in the background and the user checks on it later with `gtms {command} status`.

**Task file** — a markdown file in `test-tasks/` that records that work was requested. Every action command (create, automate, execute) creates one. Tracks what was asked, which adapter handled it, and what state the work is in. Task files are committed to git — they are the permanent audit trail.

**Result contract** — a YAML file in `.gtms/results/` that acts as the communication channel between an adapter and GTMS. GTMS creates it before invoking the adapter (pre-populated with task context). The adapter updates it with the outcome. Result contracts are transient working files, not committed to git.

**Pipeline record** — permanent metadata GTMS builds from result contracts and task context. Automation records, execution results, and task completion data are all pipeline records. GTMS owns their format — adapters never write them directly.

**Tier** — the implementation complexity of an adapter. Tier 1 is config-only (a single command template). Tier 2 is a script in any language. Tier 3 (Go module / SDK — planned, not yet implemented) will provide native API integration. See [ADR-002](../reference/adr/ADR-002-three-tier-adapter-evolution.md) for the evolution strategy. Choose the simplest tier that works.

---

## How Adapters Fit Into the Command Lifecycle

Every action command (create, automate, execute) follows six phases. Adapters are involved in phases 3-6:

```
PARSE → VALIDATE → RESOLVE → HANDOFF → MONITOR → RESULT
                      ▲          ▲         ▲         ▲
                      │          │         │         │
                   Which      Invoke    Watch     Read
                   adapter?   adapter   for       result
                                        outcome   contract
```

**What GTMS does before your adapter runs:**
1. Parses CLI input and validates the environment
2. Resolves which adapter to use (flag override → config default → built-in)
3. Generates a task ID and creates a task file in `test-tasks/pending/`
4. Creates a result contract in `.gtms/results/`
5. Builds an `AdapterContext` with everything the adapter needs
6. If `prompt-template` is set: assembles the prompt from the template and writes it to `.gtms/tmp/{task-id}-prompt.md`
7. Invokes the adapter (prompt is available via `{prompt_file}` / `$GTMS_PROMPT_FILE` and piped via stdin)

**What GTMS does after your adapter finishes:**
1. Reads the result contract (Tier 2) or infers from exit code (Tier 1)
2. Builds pipeline records (automation records, execution results)
3. Moves the task file to `complete/` or `failed/`
4. Reports the outcome to the user

Your adapter's job is the middle part: receive context, do work, report the outcome.

---

## End-to-End Workflow: Create → Automate → Execute

The three action commands form a pipeline. Each command produces artefacts that the next command consumes. Understanding this chain — especially the handoff between automate and execute — is essential for building adapters that work together.

### Step 1: Create test cases from a requirement

**Identifier-based** (adapter resolves the content):
```bash
gtms create REQ-123
```

**File-based** (GTMS injects the content via `--context-file`):
```bash
gtms create requirements/REQ-123.md --context-file requirements/REQ-123.md
```

> **Why `--context-file`?** The `{reference}` variable passes the CLI argument as a string — it doesn't read the file. If your adapter uses `--allowedTools ""` (recommended), it can't read files from disk either. `--context-file` tells GTMS to read the file and inject its content as `{context}` in the prompt template.

**What happens:**
1. GTMS invokes the create adapter (e.g. `local-claude`) with `{reference}` = `REQ-123` (or the file path)
2. The adapter generates test cases and outputs them using `<gtms-file>` tags
3. GTMS writes the files to `test-cases/`
4. Task file moves to `test-tasks/complete/`

**Files produced:**
```
test-cases/tc-a1b2c3d-login-valid-credentials.md
test-cases/tc-e8d9c0b-login-invalid-password.md
test-tasks/complete/task-f3a91b7-create-REQ-123.md
```

Each test case is a markdown file with YAML frontmatter (`id`, `title`, `requirement`, `status`) and structured steps.

### Step 2: Automate a test case

```bash
gtms automate tc-a1b2c3d --framework playwright
```

**What happens:**
1. GTMS invokes the automate adapter with `{testcase}` = `tc-a1b2c3d` and `{framework}` = `playwright`
2. The adapter reads the test case, generates an automation script, and outputs it using `<gtms-file>` tags
3. GTMS writes the spec file to `test-automation/specs/{adapter-name}/`
4. GTMS creates (or updates) an automation record at `test-automation/records/tc-a1b2c3d.automation.md`
5. The automation record's `artefact` field is populated with the path to the generated spec file

**Files produced:**
```
test-automation/specs/local-claude/tc-a1b2c3d-login-valid-credentials.spec.ts
test-automation/records/tc-a1b2c3d.automation.md
test-tasks/complete/task-b2c8d41-automate-tc-a1b2c3d.md
```

**The automation record** (frontmatter-only markdown) looks like:
```yaml
---
testcase: tc-a1b2c3d
framework: playwright
status: developed
artefact: test-automation/specs/local-claude/tc-a1b2c3d-login-valid-credentials.spec.ts
branch: feature/automate-tc-a1b2c3d
adapter: local-claude
last-dev-result: pass
attempts: 1
cycle: 1
---
```

### Step 3: Execute the automated test

```bash
gtms execute tc-a1b2c3d
```

**What happens:**
1. GTMS reads the automation record for `tc-a1b2c3d` and extracts the `artefact` field (the spec file path)
2. GTMS invokes the execute adapter with `{testcase}` = `tc-a1b2c3d` and `{spec_file}` = the artefact path
3. The adapter runs the test (e.g. `npx playwright test {spec_file}`)
4. GTMS updates the existing automation record with `last-formal-result` (pass/fail) and `last-formal-run` (artefact path)
5. Task file moves to `test-tasks/complete/` or `test-tasks/failed/`

**Files produced/updated:**
```
test-tasks/complete/task-d9e4f27-execute-tc-a1b2c3d.md    (or test-tasks/failed/)
test-automation/records/tc-a1b2c3d.automation.md  (updated with execution result)
```

### The Handoff: Automate → Execute

The connection between automate and execute is **automatic**. After `gtms automate` completes, the automation record at `test-automation/records/{tc-id}.automation.md` contains an `artefact` field with the path to the generated spec file. When you run `gtms execute`, GTMS reads this artefact path and passes it as `{spec_file}` to the execute adapter.

You don't need to supply the spec file path manually — GTMS handles the handoff:

```bash
# 1. Automate — generates spec file, records artefact path
gtms automate tc-a1b2c3d --framework playwright
# Output: ✓ task-b2c8d41 automate tc-a1b2c3d (1 files)

# 2. Execute — GTMS reads artefact from automation record automatically
gtms execute tc-a1b2c3d
```

> **Prerequisite:** `gtms execute` requires an automation record with status `developed` or `accepted`. If no automation record exists, GTMS will tell you to run `gtms automate` first.

### Full pipeline at a glance

```
gtms create REQ-123
  └─→ test-cases/tc-a1b2c3d-login-valid-credentials.md

gtms automate tc-a1b2c3d --framework playwright
  ├─→ test-automation/specs/local-claude/tc-a1b2c3d-login-valid-credentials.spec.ts
  └─→ test-automation/records/tc-a1b2c3d.automation.md
        └─ artefact: test-automation/specs/local-claude/tc-a1b2c3d-login-valid-credentials.spec.ts

gtms execute tc-a1b2c3d    (reads artefact path from automation record)
  └─→ test-automation/records/tc-a1b2c3d.automation.md (updated: last-formal-result, last-formal-run)
```

---

## Working with File-Based Requirements

When the source for `gtms create` is a file on disk (rather than an identifier like `REQ-123`), you need to tell GTMS to read the file content and inject it into the prompt. The `--context-file` flag does this.

### Two patterns

**Identifier-based** — the adapter resolves the content itself:
```bash
gtms create REQ-123
```
The adapter receives `{reference}` = `REQ-123` and is responsible for looking up the requirement (e.g. via an API, a tool, or file-reading permissions).

**File-based** — GTMS reads the file and injects the content:
```bash
gtms create requirements/REQ-123.md --context-file requirements/REQ-123.md
```
GTMS reads `requirements/REQ-123.md`, and the file content becomes available as `{context}` in prompt templates and `$GTMS_CONTEXT` for Tier 2 scripts.

### Why `--context-file` is needed

The `{reference}` variable always passes the CLI argument **as a string** — a path, an ID, a URL, whatever you typed. It does not read files.

When your adapter uses `--allowedTools ""` (recommended in all adapter examples to ensure raw text output), the AI agent cannot read files from disk independently. Without `--context-file`, the agent receives only the path string — not the file content — and produces zero output after a long wait.

> **Rule of thumb:** If your source is a local file and your adapter uses `--allowedTools ""`, always pass `--context-file` pointing to the same file. This is the most common `gtms create` scenario.

### How it works in the prompt

The prompt template (`create-standard.md`) has an `{context}` placeholder. When `--context-file` is set, GTMS reads the file and substitutes its content into that placeholder before assembling the final prompt. The adapter then receives the full requirement text inline — no file access required.

See the [Tier 1 Variable Reference](#variable-reference) and [Tier 2 Environment Variable Reference](#environment-variable-reference) for the full list of context variables.

---

## Configuration

Adapters are registered in `gtms.config` under the command they serve.

### Minimal Tier 1 Example

```yaml
project:
  name: "My Project"
  repo: org/my-repo

adapters:
  create:
    my-adapter:
      mode: sync
      command: 'my-tool --input "{reference}" --output "{output_dir}"'

defaults:
  create: my-adapter
```

### Minimal Tier 2 Example

```yaml
project:
  name: "My Project"
  repo: org/my-repo

adapters:
  create:
    my-script-adapter:
      mode: sync
      script: adapters/my-create.sh

defaults:
  create: my-script-adapter
```

### Full Config Example

```yaml
project:
  name: "My Project"
  repo: org/my-repo

adapters:
  create:
    local-claude:
      mode: sync
      prompt-template: test-cases/prompts/create-standard.md
      guide-dir: test-cases/guides/
      command: 'claude -p "Read the system prompt instructions. Create test cases from the source material. Output each test case using <gtms-file name=\"tc-{7hex}-{slug}.md\">...</gtms-file> tags. YAML frontmatter then markdown body. No code fences. Raw text only." --append-system-prompt-file {prompt_file} --allowedTools ""'
    github-create:
      mode: async
      prompt-template: test-cases/prompts/create-standard.md
      guide-dir: test-cases/guides/
      script: adapters/github-create.sh
      status-script: adapters/github-create-status.sh

  automate:
    local-claude:
      mode: sync
      prompt-template: test-automation/prompts/automate-standard.md
      command: 'claude -p "Read the system prompt instructions. Generate an automated test script from the test case. Output using <gtms-file name=\"filename\">...</gtms-file> tags. No code fences. Raw text only." --append-system-prompt-file {prompt_file} --allowedTools ""'

  execute:
    local-runner:
      mode: sync
      command: 'npx playwright test {spec_file} --reporter=junit'
    github-actions:
      mode: async
      script: adapters/github-execute.sh
      status-script: adapters/github-execute-status.sh

defaults:
  create: local-claude
  automate: local-claude
  execute: local-runner          # Change to bats-runner if most tests use BATS
```

> **Important:** The `execute` default determines which test runner GTMS invokes when `--adapter` is not specified. If your project primarily uses BATS tests, consider changing the default to `bats-runner`. Otherwise, you must specify `--adapter bats-runner` on every `gtms execute` call — forgetting this causes silent failures where the wrong runner is invoked and all tests fail with exit code 1.

### Config Fields

**Required:**

| Field | Description |
|-------|-------------|
| `mode` | `sync` or `async`. Sync: GTMS waits for completion. Async: GTMS returns immediately, user polls with `gtms {command} status`. |

**Implementation (exactly one of):**

| Field | Tier | Description |
|-------|------|-------------|
| `command` | 1 | Command template with `{variable}` placeholders. GTMS substitutes values and shells out. |
| `script` | 2 | Path to executable script (relative to project root). GTMS exports `GTMS_` environment variables and executes. |
| *(none)* | 0 | Built-in adapter handled by GTMS core (e.g. `local-reader` for status/gaps/triage). |

**Optional:**

| Field | Description |
|-------|-------------|
| `prompt-template` | Path to a prompt template file (relative to project root). GTMS reads the template, injects context variables, writes the assembled prompt to `.gtms/tmp/{task-id}-prompt.md`, and pipes it via stdin. For Tier 1: the file path is available as `{prompt_file}` and the content as `{prompt}` (deprecated). For Tier 2: the file path is available as `$GTMS_PROMPT_FILE`. |
| `guide-dir` | Path to a directory of `.md` guide files (relative to project root). GTMS reads all `.md` files alphabetically, wraps each in `<guide name="filename.md">` XML tags, and makes the content available as `{guides}` (Tier 1) or `$GTMS_GUIDES` (Tier 2). If the directory doesn't exist, the value is empty (no error). |
| `status-script` | Path to a script that checks async adapter progress. Called during `gtms {command} status`. Must update `$GTMS_RESULT_FILE` when the remote work completes. Requires `mode: async` and `script` to be set. |
| `timeout` | Maximum duration for adapter execution (e.g. `30s`, `5m`, `1h`). Uses Go duration format. If the adapter exceeds this time, GTMS cancels it and reports an error. If not set, no timeout is applied. |

**Validation rules:**
- `mode` must be `sync` or `async`
- At most one of `command`, `script`, `module` can be set
- `status-script` requires both `mode: async` and `script`
- Defaults must reference an adapter registered under the same command

### Command-Scoped Registration

Adapters are registered under the command they serve. The same adapter name can appear under multiple commands with different config:

```yaml
adapters:
  create:
    local-claude:                              # uses create-standard.md
      mode: sync
      prompt-template: test-cases/prompts/create-standard.md
      command: 'claude -p "Read the system prompt instructions. Create test cases..." --append-system-prompt-file {prompt_file} --allowedTools ""'
  automate:
    local-claude:                              # uses automate-standard.md
      mode: sync
      prompt-template: test-automation/prompts/automate-standard.md
      command: 'claude -p "Read the system prompt instructions. Generate automated tests..." --append-system-prompt-file {prompt_file} --allowedTools ""'
```

Resolution is scoped: `gtms create --adapter foo` looks only in `adapters.create`. If `foo` isn't there: `No adapter 'foo' registered for 'create'. Available adapters: local-claude.`

---

## Tier 1: Command Template Adapters

A Tier 1 adapter is entirely declarative — a command string with `{variable}` placeholders in `gtms.config`. No code required.

### How It Works

1. GTMS reads the command template from config
2. If `prompt-template` is set, GTMS reads the template file, substitutes context variables, writes the assembled prompt to a temp file (`.gtms/tmp/{task-id}-prompt.md`), sets `{prompt_file}` to the file path, sets `{prompt}` to the content (deprecated), and pipes the content to the process's stdin
3. GTMS substitutes all `{variable}` placeholders in the command template
4. GTMS executes the command via `sh -c`
5. GTMS interprets the exit code: 0 = `complete`, non-zero = `error`
6. The adapter author never sees or writes to the result contract

### Variable Reference

These variables are available in command templates via `{variable_name}`:

> All variables are always substituted. The "Populated for" column indicates which commands set a meaningful value — for other commands the variable resolves to an empty string.

| Variable | Description | Size | Populated for |
|----------|-------------|------|---------------|
| `{prompt}` | Fully assembled prompt (template + context) | **Unbounded** | All (if `prompt-template` set) |
| `{reference}` | The first positional argument to `gtms create` (e.g. `BUG-022`, `REQ-123`, a file path). This value flows through to the prompt template and typically ends up as the `requirement:` field in generated test case frontmatter — which is what `gtms map` uses to group test cases by requirement. Choose a stable, human-readable identifier. | Short | create (empty for automate/execute) |
| `{testcase}` | The target argument — ID only (for full content use `{testcase_content}`) | Short | automate, execute |
| `{testcase_content}` | Full content of the test case file | **Unbounded** | automate |
| `{output_dir}` | Output directory path | Short | All |
| `{spec_file}` | `--spec-file` flag value | Short | execute |
| `{prompt_template}` | Path to prompt template file | Short | All (if set) |
| `{branch}` | Feature branch name (`feature/{command}-{target}`) | Short | All |
| `{repo}` | Repository identifier from config | Short | All |
| `{task_id}` | Generated task ID (e.g. `task-a3f72b1`) | Short | All |
| `{result_file}` | Path to the result contract file | Short | All |
| `{project_root}` | Absolute path to project root | Short | All |
| `{work_dir}` | Working directory for the adapter | Short | All |
| `{focus}` | `--focus` flag value | Short | create |
| `{context}` | Content of `--context-file` flag file | **Unbounded** | create |
| `{context_file}` | Absolute path to file specified by `--context-file` flag | Short | create |
| `{guides}` | Concatenated content of all `.md` files from `guide-dir` | **Unbounded** | create (if `guide-dir` set) |
| `{prompt_file}` | Path to assembled prompt temp file (`.gtms/tmp/{task-id}-prompt.md`) | Short | All (if `prompt-template` set) |
| `{environment}` | `--env` flag value (target environment) | Short | automate, execute |
| `{output_subdir}` | Test case's subfolder under `test-cases/` (e.g. `cwd-scoping/`). Includes trailing `/` when non-empty, empty string for root-level test cases. Available in prompt templates for informational use. **Do not use to prefix `<gtms-file>` filenames** — GTMS automatically routes streamed files to the correct subdirectory (see [Subdirectory Routing](#subdirectory-routing)). | Short | automate, execute (empty for create) |

> **Size column:** "Short" variables are IDs, paths, or flags — typically under 200 characters. "**Unbounded**" variables contain file content that can grow to thousands of lines. This matters for prompt template ordering — see [Prompt Template Authoring](#prompt-template-authoring).

### Best For

- Single-command tool invocations
- Any adapter expressible as one shell command with variable substitution

### Limitations

- No conditional logic or multi-step processes
- No way to update the result contract (GTMS infers everything from exit code)
- All substituted values are shell-escaped before insertion (BUG-001 fix)
- **`{prompt}`, `{context}`, and `{guides}` caution:** These variables inline content as command-line arguments. Very long values hit OS limits (~32K on Windows). Use `{prompt_file}` (file path) instead — it works at any size. See [ADR-001](../reference/adr/ADR-001-prompt-delivery-via-file-and-stdin.md).

### MINGW64 / Windows Path Gotcha

On MINGW64 (Git Bash for Windows), Tier 1 commands execute via `sh -c` which handles UNIX-style paths natively. Do **not** convert paths with `cygpath -w` before passing them to shell commands — the backslashes in Windows paths (`C:\Users\...`) are interpreted as escape characters by `sh -c`, causing the path to be corrupted.

```bash
# Wrong — backslashes get stripped by sh -c
script_path="$(cygpath -w "$mock_script")"
gtms create ... --command "sh $script_path"

# Right — UNIX paths work natively
gtms create ... --command "sh $mock_script"
```

This applies to BATS test files, Tier 2 adapter scripts, and any shell context where paths flow through `sh -c`. If you're writing adapter scripts or test automation on Windows/MINGW, use UNIX paths throughout.

### Examples

**Local AI code generator (recommended — uses prompt file):**
```yaml
prompt-template: prompts/create.md
command: 'claude -p "Read the system prompt instructions. Create test cases from the source material. Output each test case using <gtms-file name=\"tc-{id}.md\">...</gtms-file> tags. No code fences. Raw text only." --append-system-prompt-file {prompt_file} --allowedTools ""'
```

> **Note:** The `-p` message must contain specific task and output format instructions — not a generic "execute the system prompt" directive. `--allowedTools ""` prevents the model from using tools and ensures raw text output. See [ADR-001](../reference/adr/ADR-001-prompt-delivery-via-file-and-stdin.md) for details.

**Local AI code generator (stdin — caution):**
```yaml
prompt-template: prompts/create.md
command: 'other-tool --prompt-from-stdin'
```

> **Warning:** Stdin piping is unreliable with Claude Code specifically (known issue with large prompts producing empty output). Use `--append-system-prompt-file {prompt_file}` instead for Claude Code. Stdin may work with other tools that support it.

**Tool with file-input flag (best):**
```yaml
prompt-template: prompts/create.md
command: 'aider --message-file {prompt_file}'
```

**Simple tool (no prompt assembly needed):**
```yaml
command: 'npx playwright test {spec_file} --reporter=junit'
```

**Legacy (deprecated — subject to OS size limits):**
```yaml
command: 'claude -p {prompt}'
```

> **WARNING: The `$(cat {prompt_file})` trap.** Do NOT use `command: 'tool -p "$(cat {prompt_file})"'`. Shell command substitution inlines the entire file as a command-line argument, recreating the same OS limit. Use a tool's file-input flag or let GTMS pipe via stdin instead.

---

## Tier 2: Script Adapters

A Tier 2 adapter is a script (any language) that GTMS executes. The script receives context as `GTMS_` environment variables and can update the result contract directly.

### How It Works

1. GTMS checks the script file exists
2. GTMS exports all context as `GTMS_` environment variables
3. If a prompt template was assembled, GTMS pipes the assembled prompt to the script's stdin
4. GTMS executes the script via `sh <scriptPath>` (see note below)
5. The script does its work (any number of steps, API calls, etc.)
6. **Sync scripts:** update `$GTMS_RESULT_FILE` with outcome fields, then exit
7. **Async scripts:** trigger the remote work, optionally update the result contract with a reference (run ID, issue URL), then exit
8. GTMS reads the result contract. If `status` was updated by the script, GTMS uses it. Otherwise, GTMS falls back to exit code (same as Tier 1).

> **Shell invocation:** GTMS runs scripts as `sh <path>`, not by executing them directly. This means the shebang line (`#!/bin/bash`) is ignored — the script is always interpreted by `sh`. On most Linux distributions and MINGW (Windows), `sh` is bash or bash-compatible, so bash features typically work. On minimal systems where `sh` is dash or another POSIX shell, bash-specific features (arrays, `[[`, process substitution) will fail. Write POSIX-compatible scripts for maximum portability.

### Environment Variable Reference

These variables are exported to Tier 2 scripts:

> All variables are always exported. The "Populated for" column indicates which commands set a meaningful value — for other commands the variable is an empty string.

| Variable | Description | Size | Populated for |
|----------|-------------|------|---------------|
| `GTMS_TASK_ID` | Generated task ID (e.g. `task-a3f72b1`) | Short | All |
| `GTMS_COMMAND` | Command that triggered the adapter (e.g. `create`) | Short | All |
| `GTMS_REFERENCE` | The first positional argument to `gtms create` (e.g. `BUG-022`, `REQ-123`, a file path). This value flows through to the prompt template and typically ends up as the `requirement:` field in generated test case frontmatter — which is what `gtms map` uses to group test cases. Choose a stable, human-readable identifier. | Short | create (empty for automate/execute — use `GTMS_TESTCASE` instead) |
| `GTMS_TESTCASE` | The target argument — ID only (for full content use `GTMS_TESTCASE_CONTENT`) | Short | automate, execute |
| `GTMS_TESTCASE_CONTENT` | Full content of the test case file | **Unbounded** | automate |
| `GTMS_OUTPUT_DIR` | Output directory path | Short | All |
| `GTMS_SPEC_FILE` | `--spec-file` flag value | Short | execute |
| `GTMS_PROMPT_TEMPLATE` | Path to prompt template file | Short | All (if set) |
| `GTMS_BRANCH` | Feature branch name | Short | All |
| `GTMS_REPO` | Repository identifier from config | Short | All |
| `GTMS_PROJECT_ROOT` | Absolute path to project root | Short | All |
| `GTMS_WORK_DIR` | Working directory for the adapter | Short | All |
| `GTMS_RESULT_FILE` | Path to the result contract YAML file | Short | All |
| `GTMS_FOCUS` | `--focus` flag value | Short | create |
| `GTMS_CONTEXT` | Content of `--context-file` flag file | **Unbounded** | create |
| `GTMS_CONTEXT_FILE` | Absolute path to file specified by `--context-file` flag | Short | create |
| `GTMS_GUIDES` | Concatenated content of all `.md` files from `guide-dir` | **Unbounded** | create (if `guide-dir` set) |
| `GTMS_PROMPT_FILE` | Path to assembled prompt temp file (`.gtms/tmp/{task-id}-prompt.md`) | Short | All (if `prompt-template` set) |
| `GTMS_ENVIRONMENT` | `--env` flag value (target environment) | Short | automate, execute |
| `GTMS_OUTPUT_SUBDIR` | Test case's subfolder under `test-cases/` (e.g. `cwd-scoping/`). Includes trailing `/` when non-empty, empty string for root-level test cases. | Short | automate, execute (empty for create) |

> **Large values:** `GTMS_CONTEXT` and `GTMS_GUIDES` contain full file content as environment variables. Linux supports ~128KB, Windows ~32KB. For very large content, use `$GTMS_PROMPT_FILE` (which contains the fully assembled prompt including context and guides) or read files directly (`$GTMS_CONTEXT_FILE` for context, or the guide directory).

> **Note:** GTMS now assembles the prompt for Tier 2 adapters when `prompt-template` is configured. The assembled prompt file is available at `$GTMS_PROMPT_FILE`. The raw template path is still available via `$GTMS_PROMPT_TEMPLATE` for scripts that need custom assembly.

> **Known issue:** `GTMS_WORK_DIR` currently always equals `GTMS_PROJECT_ROOT`. Worktree isolation is implemented in the `git` package but not yet wired into the invoker. Scripts that need git isolation should create their own branch. (REV-002 finding)

> **Security note:** Tier 2 scripts receive only a minimal allowlist of system variables (`PATH`, `HOME`, `TMPDIR`, `USER`, `SHELL`, `LANG`, `LC_ALL` + Windows vars) plus all `GTMS_*` variables. Parent process secrets are no longer inherited. See [Security Considerations](#tier-2-environment-isolation) for details.

### Output Directory by Command

GTMS sets `GTMS_OUTPUT_DIR` (and `{output_dir}`) based on the command. Each adapter can override the default with the `output-dir` config field:

| Command | Default output directory | Override with |
|---------|-------------------------|---------------|
| `create` | `{project_root}/test-cases` | `output-dir` on create adapter |
| `automate` | `{project_root}/test-automation/specs/{adapter-name}/` | `output-dir` on automate adapter |
| `execute` | `{project_root}/results` | `output-dir` on execute adapter |

Example config with custom output directories:

```yaml
adapters:
  create:
    playwright-claude:
      mode: sync
      output-dir: tests/features/     # BDD feature files land here
      command: 'claude -p "..." --append-system-prompt-file {prompt_file}'
  automate:
    playwright-claude:
      mode: sync
      output-dir: tests/e2e/          # automation specs land here
      command: 'claude -p "..." --append-system-prompt-file {prompt_file}'
```

> **Deprecation note:** The `spec-dir` field still works but is deprecated in favour of `output-dir`. If `spec-dir` is set and `output-dir` is not, GTMS copies `spec-dir` to `output-dir` internally. Setting both on the same adapter is an error. New configs should use `output-dir`.

### Updating the Result Contract

Tier 2 scripts can update the result contract at `$GTMS_RESULT_FILE`. The file is YAML. GTMS pre-populates these fields:

```yaml
task: task-a3f72b1
command: create
target: JIRA-456
adapter: my-adapter
mode: sync
created: 2025-02-12T10:30:00Z
status: pending
```

Your script should update it with outcome fields. The simplest approach is to overwrite the file, preserving the GTMS-set fields (`task`, `command`, `target`, `adapter`, `mode`, `created`) and adding the outcome fields:

```bash
cat > "${GTMS_RESULT_FILE}" <<EOF
task: ${GTMS_TASK_ID}
command: ${GTMS_COMMAND}
target: ${GTMS_REFERENCE}
adapter: my-adapter
mode: sync
created: ${GTMS_CREATED:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}
status: complete
artefact: path/to/output/file.md
attempts: 1
summary: "Generated 3 test cases"
completed: $(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF
```

> **Note:** The `created` field is the timestamp when GTMS originally invoked the adapter. If you overwrite the full file, carry this value through rather than replacing it. The `completed` field is the one your script should set to the current time.

If your script doesn't update the result contract, GTMS falls back to exit code handling (exit 0 = complete, non-zero = error). This means simple scripts that just need pass/fail don't need to touch the result contract at all.

### Best For

- Multi-step workflows
- Adapters that call multiple tools
- GitHub-based workflows using `gh` CLI
- Anything with conditional logic

### Examples

**Sync script — local tool with result reporting:**

```bash
#!/bin/bash
# adapters/my-create.sh
set -e

# Do the work
my-tool generate --source "${GTMS_REFERENCE}" --out "${GTMS_OUTPUT_DIR}"

# Report success (preserve GTMS-set fields, add outcome fields)
cat > "${GTMS_RESULT_FILE}" <<EOF
task: ${GTMS_TASK_ID}
command: ${GTMS_COMMAND}
target: ${GTMS_REFERENCE}
adapter: my-create
mode: sync
status: complete
artefact: ${GTMS_OUTPUT_DIR}
attempts: 1
summary: "Test cases generated for ${GTMS_REFERENCE}"
completed: $(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF
```

**Async script — trigger remote work:**

```bash
#!/bin/bash
# adapters/github-execute.sh
set -e

# Trigger the workflow
gh workflow run test-runner.yml \
  --ref main \
  -f test="${GTMS_TESTCASE}" \
  -f spec="${GTMS_SPEC_FILE}"

# Capture run ID for status polling
sleep 2
RUN_ID=$(gh run list --workflow=test-runner.yml --limit 1 --json databaseId -q '.[0].databaseId')

# Update result contract with what we know so far
cat > "${GTMS_RESULT_FILE}" <<EOF
task: ${GTMS_TASK_ID}
command: ${GTMS_COMMAND}
target: ${GTMS_TESTCASE}
adapter: github-actions
mode: async
status: pending
summary: "GitHub Actions workflow triggered, run ID: ${RUN_ID}"
log: https://github.com/${GTMS_REPO}/actions/runs/${RUN_ID}
run-id: ${RUN_ID}
EOF
```

**Status script — poll remote work:**

```bash
#!/bin/bash
# adapters/github-execute-status.sh
set -e

# Read run ID from result contract (set during trigger)
RUN_ID=$(grep '^run-id:' "${GTMS_RESULT_FILE}" | awk '{print $2}')
[ -z "${RUN_ID}" ] && exit 0

STATUS=$(gh run view "${RUN_ID}" --repo "${GTMS_REPO}" --json status -q '.status')
CONCLUSION=$(gh run view "${RUN_ID}" --repo "${GTMS_REPO}" --json conclusion -q '.conclusion')

if [ "${STATUS}" = "completed" ]; then
  if [ "${CONCLUSION}" = "success" ]; then
    RESULT_STATUS="complete"
  else
    RESULT_STATUS="error"
  fi

  cat > "${GTMS_RESULT_FILE}" <<EOF
task: ${GTMS_TASK_ID}
command: ${GTMS_COMMAND}
target: ${GTMS_TESTCASE}
adapter: github-actions
mode: async
status: ${RESULT_STATUS}
artefact: results/runs/${RUN_ID}/
summary: "Workflow ${RUN_ID}: ${CONCLUSION}"
log: https://github.com/${GTMS_REPO}/actions/runs/${RUN_ID}
completed: $(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF
fi
# If not completed, do nothing — result contract stays pending
```

---

## Result Contract Reference

The result contract is the communication channel between adapters and GTMS.

### Location

```
.gtms/results/{task-id}.result.yaml
```

One file per task, keyed by task ID. The `.gtms/` directory is gitignored — result contracts are transient.

### Fields

| Field | Written by | Required | Description |
|-------|-----------|----------|-------------|
| `task` | GTMS | Yes | Task ID — links back to the task file |
| `command` | GTMS | Yes | Command that triggered this (create, automate, execute) |
| `target` | GTMS | Yes | Requirement ID or test case ID |
| `adapter` | GTMS | Yes | Adapter instance name |
| `mode` | GTMS | Yes | `sync` or `async` |
| `created` | GTMS | Yes | Timestamp when adapter was invoked |
| `status` | GTMS, then adapter | Yes | `pending` → `complete` or `error` (see below) |
| `artefact` | Adapter | No | Primary output file path(s), comma-separated if multiple |
| `attempts` | Adapter | No | Number of attempts (defaults to 1) |
| `summary` | Adapter | No | Human-readable outcome description |
| `log` | Adapter | No | Pointer to raw output (directory path, URL, or CI artefact link) |
| `completed` | Adapter | No | Timestamp when work finished |

### Status Values

The `status` field tracks whether the adapter completed its execution, **not** the outcome of the work produced.

| Status | Meaning |
|--------|---------|
| `pending` | GTMS set this before invoking the adapter. If it never changes, the adapter didn't report back (crash, hang, or misconfiguration). |
| `complete` | The adapter finished its execution successfully. An EXECUTE adapter that runs a failing test suite still reports `complete` — test outcomes live in artefacts (JUnit XML, reports), not in this field. |
| `error` | The adapter could not complete its execution (tool not installed, network error, script error). |

### How Each Tier Reports Results

| Tier | How the adapter reports | What GTMS infers |
|------|------------------------|------------------|
| **Tier 1** | Exit code only. Adapter author never sees the result contract. | GTMS sets: `status` from exit code (0 = complete, non-zero = error), `attempts` = 1, `completed` = now. |
| **Tier 2** | Script updates `$GTMS_RESULT_FILE` directly. | GTMS reads the file after script exits. If `status` is still `pending`, falls back to exit code. |
| **Built-in** | GTMS core handles internally. | No external result contract needed. |

> **Artefact detection (Tier 1 / Tier 2 fallback):** When GTMS handles the result (Tier 1, or Tier 2 when the script doesn't update the contract), it populates the `artefact` field from two sources: (1) files written via stdout streaming (`<gtms-file>` tags), or (2) new files detected in the output directory after adapter invocation. The output directory scan uses a before/after snapshot to report only NEW files created by the adapter, not pre-existing files. If neither source produces files, the field remains empty.
>
> **Tier 2 scripts that update the contract directly** are responsible for setting the `artefact` field themselves. When a script writes `status: complete` to `$GTMS_RESULT_FILE`, GTMS uses the script's values as-is and does not run the output directory scan. If your Tier 2 script produces files but doesn't set the `artefact` field, the field will remain empty in the contract.

### What GTMS Does With Results

| Command | Pipeline record built | Key fields used |
|---------|----------------------|-----------------|
| `create` | None — test case files *are* the record | `status` (did adapter complete) |
| `automate` | Automation record (`.automation.md`) in `test-automation/records/` | `status`, `artefact`, `attempts`, `summary`, `log` |
| `execute` | Updates existing automation record with `last-formal-result` | `status`, `artefact`, `log` |

---

## File Output

How adapters produce files (test cases, spec files, test results) is a key part of the adapter contract. There are two approaches.

### Approach 1: Adapter Writes Files Directly

The adapter writes files to the filesystem itself. For Tier 2 scripts, the adapter should also report what it created via the result contract `artefact` field. For Tier 1 adapters without stdout streaming, GTMS cannot currently populate this field automatically (see Current Limitations).

```bash
# Adapter writes files
my-tool generate --output "${GTMS_OUTPUT_DIR}/tc-f1a2b3c-login.md"

# Report paths in result contract
cat > "${GTMS_RESULT_FILE}" <<EOF
...
artefact: test-cases/tc-f1a2b3c-login.md
EOF
```

**Pros:** Simple, adapter has full control. Works for downloading remote artefacts (CI results, etc.).
**Cons:** Adapter needs file-write permissions. For AI tools like Claude Code in `-p` mode, this requires `--dangerously-skip-permissions` (no TTY for permission prompts).

### Approach 2: Stdout Streaming — GTMS Writes Files (Recommended)

**Status: Implemented** — [ENH-001](../PRPs/enhancements/complete/ENH-001-stdout-to-file-post-processing.md). Reviewed in [REV-004](../PRPs/code_reviews/REV-004-streaming-stdout.md).

The adapter outputs file content to stdout using delimiters. GTMS streams stdout in real time and writes files incrementally as each delimited block completes. The adapter needs zero file-write permissions.

**XML tag format (recommended):**

```
<gtms-file name="tc-f1a2b3c-login.md">
---
id: tc-f1a2b3c
title: Login Authentication
requirement: JIRA-456
status: ready
---

## Objective
Verify user can log in with valid credentials.

## Steps
1. Navigate to login page
...
</gtms-file>

<gtms-file name="tc-e8d9c0b-login-failure.md">
---
id: tc-e8d9c0b
...
</gtms-file>
```

The XML format uses `<gtms-file name="...">` opening tags and `</gtms-file>` closing tags. Each tag must appear on its own line. Content after a closing tag reverts to summary mode, which cleanly separates adapter commentary from file content.

**Rules:**
- Filenames should be bare filenames without directory prefixes (e.g. `tc-abc.bats`, not `widgets/tc-abc.bats`). GTMS automatically routes streamed files to the correct subdirectory based on the test case's location — see [Subdirectory Routing](#subdirectory-routing). Relative subdirectory paths in filenames are still supported for backward compatibility, but will result in double-nesting if the test case is in a subdirectory.
- Filenames are sanitized: backslashes (`\`), absolute paths, and directory traversal (`..`) are rejected
- Any stdout before the first `<gtms-file>` tag is captured as the summary string
- Content after `</gtms-file>` is captured as summary, not file content
- If no `<gtms-file>` tags are found, all stdout is captured as the summary
- Files are written incrementally — completed files survive adapter crashes
- Only one file block is in memory at a time (constant memory regardless of output size)
- A trailing newline is appended to each file if not already present

**This applies to both Tier 1 and Tier 2 adapters equally.** The streaming parser is in the shared invocation layer (`internal/adapter/stream.go`), so any adapter for any command gets this automatically.

**What GTMS does with streamed files:**
- Written to `OutputDir + OutputSubdir` as each block completes (see [Subdirectory Routing](#subdirectory-routing))
- Paths are recorded in `InvocationResult.SavedFiles`
- The result contract `artefact` field is auto-populated with project-relative paths (comma-separated if multiple)

**Impact on adapter development:**
- Adapters become pure content producers — they output text, GTMS handles file I/O
- Prompt templates should instruct the AI to use the `<gtms-file>` tag format
- Adapters that write files directly (Approach 1) continue to work unchanged

### Recommendation

**Prefer stdout streaming** (Approach 2) for new adapters. It's more secure (no file-write permissions needed), more resilient (crash recovery — completed files survive), and works identically across tiers.

Approach 1 remains appropriate for adapters that download artefacts from external systems (CI results, remote files) where the adapter genuinely needs filesystem access.

---

## Subdirectory Routing

**Status: Implemented** — [BUG-021](../PRPs/bugs/complete/BUG-021-streaming-writer-ignores-output-subdir.md)

When a test case lives in a subdirectory under `test-cases/` (e.g. `test-cases/widgets/tc-abc.md`), GTMS automatically routes streamed output files to the matching subdirectory under the output directory. The adapter does not need to know or care about the subdirectory — GTMS handles it.

### How it works

1. GTMS detects the test case's subdirectory relative to `test-cases/` and sets `OutputSubdir` (e.g. `widgets/`)
2. When the adapter streams output via `<gtms-file>` tags, GTMS writes files to `OutputDir + OutputSubdir` (e.g. `test-automation/specs/widgets/`)
3. The adapter emits **bare filenames only** in `<gtms-file>` tags — no directory prefixes

### Example

Test case at `test-cases/widgets/tc-abc-login.md`:

```
Adapter outputs:     <gtms-file name="tc-abc-login.bats">
GTMS writes to:      test-automation/specs/widgets/tc-abc-login.bats
```

Test case at `test-cases/tc-abc-login.md` (root level):

```
Adapter outputs:     <gtms-file name="tc-abc-login.bats">
GTMS writes to:      test-automation/specs/tc-abc-login.bats
```

### Common mistake: double-nesting

If an adapter includes the subdirectory in the `<gtms-file>` filename (e.g. `<gtms-file name="widgets/tc-abc.bats">`), the file will be written to `test-automation/specs/widgets/widgets/tc-abc.bats` — double-nested. Prompt templates must instruct adapters to output bare filenames.

The `{output_subdir}` variable is available in prompt templates for informational purposes (e.g. telling the adapter which subdirectory the test case came from), but should **not** be used to prefix `<gtms-file>` filenames.

---

## Prompt Template Authoring

Prompt templates (`prompt-template` in config) are markdown files with `{variable}` placeholders. GTMS reads the template, substitutes variables, and delivers the assembled prompt to the adapter. Getting the template structure right is critical — a poorly ordered template can cause the AI to ignore your output format instructions entirely.

### Why Ordering Matters

LLMs have **positional attention bias** — they attend most strongly to the beginning and end of the prompt, with degraded attention for content in the middle ("lost in the middle" effect). This worsens as input length increases.

**Research findings:**
- **Anthropic**: Place long documents/data near the top of the prompt; place instructions at the end (improves response quality by up to 30%)
- **OpenAI (GPT-4.1 guide)**: When competing instructions exist, the model follows the one closer to the end
- **Liu et al., 2024**: U-shaped attention curve — beginning and end of context receive most attention

**The practical rule for GTMS templates:** Put unbounded reference material (`{guides}`, `{context}`) in the middle. Put output format instructions and action steps at the **end**.

### Recommended Template Structure

The shipped GTMS templates use **XML tags** for section boundaries (see [XML Tags in Prompt Templates](#xml-tags-in-prompt-templates) below). The ordering principle remains the same — unbounded content in the middle, output rules at the end:

```xml
<role>                               ← Role definition (short, fixed)
You are a test case creation specialist.
</role>

<task>                               ← Target and action (short)
Create test cases from: {reference}
</task>

<focus_area>                         ← {focus} (short, usually one line)
{focus}
</focus_area>

<source_material>                    ← {context} (UNBOUNDED — can be thousands of lines)
{context}
</source_material>

<quality_standards>                  ← {guides} (UNBOUNDED — XML-wrapped guide files)
{guides}
</quality_standards>

<output_rules>                       ← CRITICAL: at the END, after all variable content
- You MUST use <gtms-file name="filename">...</gtms-file> tags for each output file
- Do NOT summarise. Output ONLY tagged file blocks
- Each tag on its own line. Do NOT output files from examples in the source material.
</output_rules>
```

### Variable Size Categories

When designing templates, know which variables are short (safe anywhere) and which are unbounded (must be positioned carefully):

| Category | Variables | Typical size |
|----------|-----------|-------------|
| **Short** | `{reference}`, `{testcase}`, `{focus}`, `{framework}`, `{task_id}`, `{branch}`, `{repo}`, `{output_dir}`, `{spec_file}`, `{prompt_template}`, `{context_file}`, `{prompt_file}` | Under 200 chars |
| **Unbounded** | `{context}`, `{guides}`, `{prompt}`, `{testcase_content}` | Hundreds to thousands of lines |

> **Note:** `{framework}` is the value of the `--framework` flag on the `automate` command. It is available in prompt templates but is **not** available as a Tier 1 command template variable or Tier 2 environment variable. Use it in automate prompt templates to specify the target test framework (e.g. "playwright", "pytest").

Short variables can go anywhere in the template. Unbounded variables should go in the middle — after the task description, before the output instructions.

### XML Tags in Prompt Templates

The shipped GTMS prompt templates use XML tags (`<role>`, `<task>`, `<source_material>`, `<output_rules>`, etc.) instead of markdown headers (`## Task`, `## Output Rules`) to delineate sections. This is a recommended practice, not a requirement — adapter authors writing custom templates can use either approach. GTMS's prompt assembler treats the template as plain text and performs `{variable}` substitution regardless of format.

**Why XML tags?**

1. **Unambiguous boundaries** — The model knows exactly where instructions end and data begins. A `<source_material>` tag is a stronger signal than `## Additional Context`.
2. **Data vs. instruction separation** — Content inside `<source_material>` tags is clearly data, reducing the risk of the model treating user-provided context as instructions (prompt injection mitigation).
3. **Survives attention degradation** — XML tags are structural anchors that maintain their meaning even in the "lost in the middle" zone where markdown headers lose prominence.
4. **Semantic naming** — Tag names describe the role of the content (`<quality_standards>`, `<output_rules>`), not just a visual heading.
5. **Model-agnostic** — While Anthropic specifically recommends XML tags for Claude, they also improve prompt clarity for GPT-4, Gemini, and other models.

**Before (markdown headers):**
```markdown
## Task
Create test cases from: {reference}

## Quality Standards
{guides}

## Output Rules
- Each test case uses <gtms-file> tags
```

**After (XML tags):**
```xml
<task>
Create test cases from the source material provided below.
Source identifier: {reference}
</task>

<quality_standards>
{guides}
</quality_standards>

<output_rules>
- Each test case uses <gtms-file name="filename.md">...</gtms-file> tags
- Only output .md test case specification files — never reproduce example files from source material
</output_rules>
```

**Guide files are also XML-wrapped.** When `guide-dir` is configured, GTMS wraps each guide file in `<guide name="filename.md">` tags:

```xml
<guide name="test-case-template.md">
...guide content...
</guide>

<guide name="quality-principles.md">
...guide content...
</guide>
```

This gives the model clear boundaries between individual guide files and between guide content and surrounding prompt sections.

### Common Mistakes

**Putting output instructions before reference material:**
```markdown
## Output              ← AI forgets this after reading 3,000 lines of guides
<gtms-file name="...">

## Quality Standards
{guides}               ← 400 lines of guide content

## Additional Context
{context}               ← 2,600 lines of context
```
The AI reads the output instructions, then processes thousands of lines of reference material, and by the time it generates output it has deprioritised the format instructions.

**Weak output instructions:**
```markdown
## Output
Write each test case as a separate file.
```
When output instructions follow large reference material, they need emphasis: "MANDATORY", "You MUST", and negative instructions ("Do NOT summarise").

**Source material containing `<gtms-file>` examples (discovered during ENH-041 dogfood):**

When the `{context}` variable contains documentation that itself shows `<gtms-file>` tag examples (e.g. an enhancement doc describing the streaming format), the AI adapter will reproduce those examples as actual output file blocks. The adapter sees `<gtms-file name="example.bats">` in the source material and outputs it as a real file block — producing junk `.bats`, `.xml`, and other non-test-case files alongside the intended `.md` test case specs.

**Fix:** Output rules must explicitly constrain file types and instruct the adapter not to reproduce examples:
```xml
<output_rules>
- ONLY output .md test case specification files
- Do NOT reproduce example files, code snippets, or sample filenames from the source material
- Each file uses <gtms-file name="tc-{hex}-{slug}.md">...</gtms-file> tags
</output_rules>
```

**Using `{prompt}` in command templates for large prompts:**
```yaml
command: 'claude -p {prompt}'
```
The `{prompt}` variable inlines the entire assembled prompt as a command-line argument. This hits OS limits (~32K on Windows). Use `{prompt_file}` instead — see [ADR-001](../reference/adr/ADR-001-prompt-delivery-via-file-and-stdin.md).

**Incomplete BATS boilerplate in automate prompt templates:**
AI-generated BATS files consistently have three boilerplate issues: `PROJECT_ROOT` not exported (invisible in `setup()` subshell), depth hardcoded to `/../..` (breaks for subdirectory tests at `test/acceptance/{work-item}/`), and relative `load` paths (break at different depths). The automate prompt template must include the exact correct pattern — see [Gotcha #8 in the Adapter Authoring Walkthrough](adapter-authoring-walkthrough.md) for the correct boilerplate.

**AI-generated assertions targeting the wrong output line:**
When testing `gtms status tc-XXX` (detail view), the output has a header line (`tc-XXX  slug: title`) followed by detail lines (`EXECUTE:    ✓ (Pass)`). Auto-generated BATS tests may grep for "Pass" on the header line, which doesn't contain it. Always verify assertion targets against actual `gtms status` output. Use `assert_output --partial "Pass"` against the full output rather than grepping a specific line.

**Using Tier 2 `script:` adapters in BATS test fixtures:**
When a BATS test creates a fixture with a Tier 2 `script:` path pointing to a BATS temp directory, the invoker's `filepath.Join(projectRoot, scriptPath)` doubles the path if it's absolute. Use a Tier 1 `command: 'bash script.sh'` adapter with the script placed inside the fixture directory instead — simpler and avoids the path-joining issue.

### References

- [Lost in the Middle (Liu et al., 2024)](https://arxiv.org/abs/2307.03172) — U-shaped attention in LLMs
- [Anthropic Long Context Tips](https://docs.anthropic.com/en/docs/build-with-claude/prompt-engineering/long-context-tips) — Data at top, instructions at end
- [OpenAI GPT-4.1 Guide](https://cookbook.openai.com/examples/gpt4-1_prompting_guide) — Last instruction wins with competing directives
- [BUG-007](../PRPs/complete/PRP-BUG-007-prompt-template-ordering-sensitivity.md) — The GTMS bug that motivated these guidelines

---

## Building Your First Adapter

For a step-by-step walkthrough of building your first adapter — including tier decisions, worked examples, and real-world gotchas — see [Adapter Authoring Walkthrough](adapter-authoring-walkthrough.md).

**Prerequisites:** Your project needs a `gtms.config` and the GTMS folder structure (`test-tasks/`, `test-cases/`, `test-automation/`, `.gtms/`). Run `gtms init` to set this up.

---

## Adapter Review Checklist

Use this when reviewing a new or modified adapter.

### Configuration
- [ ] Registered under the correct command in `gtms.config`
- [ ] `mode` is appropriate (sync if GTMS should wait, async if work is long-running)
- [ ] Only one of `command`/`script` is set
- [ ] If async: `status-script` is set
- [ ] Default is updated if this is the primary adapter for the command
- [ ] Config passes validation (`go test ./internal/config/ -v`)

### Tier 1 Specific
- [ ] Command template uses correct `{variable}` names from the variable reference
- [ ] No variables that would contain shell-unsafe characters (see Security section)
- [ ] Command works when run manually with variables substituted by hand

### Tier 2 Specific
- [ ] Script file exists at the path specified in config
- [ ] Script is executable (`chmod +x`)
- [ ] Script uses `set -e` or equivalent error handling
- [ ] Script reads context from `GTMS_` environment variables (not hardcoded paths)
- [ ] Script updates `$GTMS_RESULT_FILE` with at least `status: complete` or `status: error`
- [ ] For async: trigger script exits quickly (doesn't block waiting for remote work)
- [ ] For async: status script reads remote reference from result contract and polls correctly

### Result Contract
- [ ] `status` is set to `complete` on success, `error` on failure
- [ ] `artefact` lists the primary output files
- [ ] `summary` provides a human-readable outcome
- [ ] `completed` timestamp is set

### File Output
- [ ] Files are written to the correct output directory for the command
- [ ] File names follow project conventions
- [ ] Files have required frontmatter/metadata (if applicable)

### Testing
- [ ] Adapter works with `gtms {command} {target}`
- [ ] Task file is created and moved to correct status directory
- [ ] Result contract is populated correctly
- [ ] Pipeline records are built (automation records for automate, execution results for execute)
- [ ] Error cases handled: what if the tool isn't installed? Network down? Invalid input?
- [ ] Acid tests still pass: `go test ./internal/adapter/ -run "Acid" -v`
- [ ] Full test suite passes: `go test ./...`

---

## Adapter Patterns

Three common patterns have emerged. These are documentation concepts, not enforced at runtime — your adapter doesn't need to fit neatly into one category.

### Engine Pattern (create, automate)

Generates or creates artefacts — test cases, automation code, documentation.

| Aspect | Typical behaviour |
|--------|-------------------|
| **Mode** | `sync` (local AI tool) or `async` (remote/GitHub-based) |
| **Input** | Target ID, prompt template, output directory |
| **Output** | Files in `test-cases/` or `test-automation/specs/` |
| **Example** | `claude -p "Read the system prompt instructions..." --append-system-prompt-file {prompt_file} --allowedTools ""`, GitHub Copilot issue assignment |

### Runner Pattern (execute)

Executes tests and returns results.

| Aspect | Typical behaviour |
|--------|-------------------|
| **Mode** | `sync` (local runner) or `async` (CI pipeline) |
| **Input** | Test case ID, spec file path |
| **Output** | Result files in `results/` (JUnit XML, reports) |
| **Example** | `npx playwright test`, GitHub Actions workflow |

### Analyser Pattern (status, gaps, triage)

Reads data and returns structured analysis. Currently handled by the built-in `local-reader` adapter.

| Aspect | Typical behaviour |
|--------|-------------------|
| **Mode** | `sync` |
| **Input** | Scope of analysis |
| **Output** | Structured data displayed to user |
| **Example** | Built-in filesystem reader, future AI-assisted triage |

---

## Security Considerations

### Command Injection in Tier 1 Templates (CRIT-1 — Fixed)

**Status: Fixed in BUG-001.** All Tier 1 template values are now shell-escaped before substitution using single-quote wrapping with internal quote escaping. This prevents command injection via target IDs, prompt content, and other user input.

**Residual considerations for adapter authors:**
- Shell escaping protects against injection but very long values (`{guides}`, `{context}`) may hit shell argument length limits
- For security-sensitive contexts with large content, prefer Tier 2 scripts which receive content via environment variables

### Tier 2 Environment Isolation

**BREAKING CHANGE (ENH-014):** Tier 2 scripts no longer inherit the full parent process environment. Scripts receive only a minimal allowlist of system variables plus all `GTMS_*` variables.

**Allowlist:** `PATH`, `HOME`, `TMPDIR`, `USER`, `SHELL`, `LANG`, `LC_ALL` (+ `USERPROFILE`, `SYSTEMROOT`, `COMSPEC`, `PATHEXT`, `TEMP`, `TMP` on Windows).

**Migration:** If your Tier 2 script relied on inherited environment variables (e.g. `GOPATH`, `NODE_PATH`, `API_KEY`), you have two options:
1. Export them inside the script: `export GOPATH=/path/to/go`
2. Use a wrapper script that sources the needed vars before calling your adapter

**Note:** Tier 1 adapters still inherit the full parent environment (Go default for `exec.Command` without `Env` set). This asymmetry is intentional — Tier 1 commands run via `sh -c` and may need `PATH`, `GOPATH`, etc. from the parent.

### Input Validation

GTMS performs minimal validation on target IDs. Values like `../../etc/passwd` or `$(whoami)` are not rejected. Adapter scripts should validate their inputs if they use them in sensitive contexts (file paths, shell commands, API calls).

---

## Current Limitations

These are known gaps between the documented contract and the current implementation. They are tracked and will be addressed in future releases.

| Limitation | Impact | Reference |
|-----------|--------|-----------|
| Worktree isolation not wired in | `GTMS_WORK_DIR` always equals project root. Multi-agent concurrent use is unsafe. | REV-002 |
| ~~Tier 1 artefact field not set (non-streaming)~~ | **Fixed (ENH-014).** GTMS now scans the output directory for new files when no streaming delimiters are found. | ENH-014 Item 1 |
| Windows `cmd /c` fallback not implemented | Both tiers use `sh -c` on all platforms. Works on MINGW but not plain Windows. | REV-002 CRIT-3 |
| Exit code extraction uses Unix-specific syscall | On Windows, non-zero exit codes always reported as 1 (no diagnostic detail) | REV-002 CRIT-2 |
| Async status polling only for execute | `status-script` is only called by `gtms execute status`. Create and automate status commands don't poll. | REV-002 |
| Stdout streaming requires `<gtms-file>` tags | Streaming only activates when adapter output contains `<gtms-file>` tags. Plain stdout is not captured to files. | ENH-001, ENH-041 |
| No input sanitization on target IDs | Target values flow into file paths, branch names, and shell commands without validation | REV-002 |
| Errors silently swallowed on state transitions | `task.Move()` and `result.Update()` failures discarded in some paths | REV-002 |
| ~~`--env` flag not implemented~~ | **Fixed (ENH-014).** `--env` flag available on `gtms automate` and `gtms execute`. Threaded to `{environment}` (Tier 1), `GTMS_ENVIRONMENT` (Tier 2), and `{environment}` in prompt templates. | ENH-014 Item 3 |
| `--dry-run` flag not functional | Flag is parsed but never checked — real tasks are created | REV-002 |

---

## Related Documents

| Document | Purpose |
|----------|---------|
| [gtms-command-adapter-pattern.md](./archive/gtms-command-adapter-pattern.md) | Archived design spec — see ADR-005, ADR-006 for design rationale |
| [ARCHITECTURE.md](../ARCHITECTURE.md) | Package map, data flow, how to add commands |
| [CLAUDE.md](../CLAUDE.md) | Project conventions, critical rules, testing patterns |
| [ENH-001](../PRPs/enhancements/complete/ENH-001-stdout-to-file-post-processing.md) | Stdout streaming enhancement (implemented) |
| [REV-002](../PRPs/code_reviews/REV-002-v1-deep-review.md) | Deep code review findings |
| [ADR-001](./adr/ADR-001-prompt-delivery-via-file-and-stdin.md) | Prompt delivery via file and stdin (explains why `{prompt_file}` is preferred) |
| [ADR-002](./adr/ADR-002-three-tier-adapter-evolution.md) | Three-tier adapter evolution strategy |
| [REV-004](../PRPs/code_reviews/complete/REV-004-streaming-stdout.md) | Streaming stdout code review |
