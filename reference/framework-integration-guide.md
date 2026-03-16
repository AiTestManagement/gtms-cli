# Framework Integration Guide

*Getting GTMS into your project — from binary to working adapters.*

---

## Purpose

This guide covers what happens after you understand the adapter contract (see [Adapter Guide](adapter-guide.md)) and want to deploy GTMS in a real project. It answers:

- How do I get GTMS onto a new machine?
- How do I set up multi-machine test execution?
- How do I create GitHub Actions workflows for remote execution?
- How do I generate project-specific adapter config and prompt templates?

---

## 1. Getting GTMS onto a New Machine

### Build the binary

GTMS is a single Go binary with no runtime dependencies (no Go installation required on the target machine).

**Build for the current platform:**
```bash
go build -o gtms ./cmd/gtms       # Linux/macOS
go build -o gtms.exe ./cmd/gtms   # Windows (including Git Bash / MINGW)
```

> **Windows / MINGW gotcha:** Always use `gtms.exe` on Windows, even in Git Bash. A Linux binary (`gtms` without `.exe`) will silently do nothing in MINGW — it can't execute ELF binaries but won't report an error.

**Cross-compile for a different platform:**
```bash
GOOS=linux GOARCH=amd64 go build -o gtms ./cmd/gtms         # Linux x86_64
GOOS=darwin GOARCH=arm64 go build -o gtms ./cmd/gtms         # macOS Apple Silicon
GOOS=windows GOARCH=amd64 go build -o gtms.exe ./cmd/gtms    # Windows x86_64
```

### Prerequisites on the target machine

| Requirement | Why | How to check |
|-------------|-----|-------------|
| `git` on PATH | GTMS shells out to git for repo detection, branching, worktrees | `git --version` |
| `sh` or `bash` | Tier 1 and Tier 2 adapters execute via `sh -c` | `sh --version` or `bash --version` |
| `gh` CLI (optional) | Only needed if using GitHub Actions async adapters | `gh --version` |

No Node.js, Python, or framework-specific tools are needed for GTMS itself. Those are only needed on machines that run the adapters (which may be the same machine or different — see architecture patterns below).

### Verify the installation

```bash
# In any existing git repo:
cd /path/to/your/repo
gtms init --name "My Project" --repo "org/my-repo" --adapter claude

# Check the pipeline:
gtms status
gtms gaps
```

If `gtms init` succeeds and `gtms status` runs without error, GTMS is working.

### What `gtms init` creates

```
gtms.config                          # Adapter config (YAML)
test-tasks/
  pending/ in-progress/ in-review/ complete/ failed/
test-cases/
  prompts/create-standard.md         # Create prompt template (starter)
  guides/                            # Guide files for create adapters
test-automation/
  records/ specs/ prompts/
test-execution/
.gtms/                               # Gitignored working directory
  results/ worktrees/ logs/ tmp/
```

---

## 2. Multi-Machine Architecture Patterns

GTMS is a single-agent controller — one adapter per command invocation per shell. How you distribute work across machines depends on your setup.

### Pattern A: Local orchestration, remote execution

The tester runs GTMS and AI tools locally. Test execution happens on a remote runner (GitHub Actions, Jenkins, self-hosted).

```
┌─────────────────────────────────┐     ┌──────────────────────────────┐
│  Developer Machine              │     │  Remote Runner               │
│                                 │     │  (GitHub Actions / Jenkins)  │
│  gtms create REQ-123            │     │                              │
│  gtms automate tc-xxx           │     │  npx playwright test ...     │
│    └─ Claude generates specs    │     │    └─ Browsers run tests     │
│                                 │     │    └─ Results uploaded        │
│  gtms execute tc-xxx ──────────────→  │                              │
│    └─ async adapter triggers    │     │                              │
│       GitHub workflow           │     │                              │
│                                 │     │                              │
│  gtms execute status ←─────────────── │  (polls for completion)      │
│    └─ status-script checks run  │     │                              │
└─────────────────────────────────┘     └──────────────────────────────┘
```

**Config:**
- `create` and `automate` adapters: `mode: sync` (local AI tool)
- `execute` adapter: `mode: async` with `script` (trigger) + `status-script` (poll)

**Best for:** Teams where testers work on their own machines and test execution needs browsers, infrastructure, or environments that aren't available locally.

### Pattern B: Everything local

GTMS, AI tools, and test execution all run on the same machine.

```
┌─────────────────────────────────┐
│  Developer Machine              │
│                                 │
│  gtms create REQ-123            │
│    └─ Claude generates tests    │
│                                 │
│  gtms automate tc-xxx           │
│    └─ Claude generates specs    │
│                                 │
│  gtms execute tc-xxx            │
│    └─ npx playwright test ...   │
│    └─ Results in results/       │
└─────────────────────────────────┘
```

**Config:**
- All adapters: `mode: sync`
- Execute adapter: `command: 'npx playwright test {spec_file} --reporter=junit'`

**Best for:** Solo testers, demos, development, and projects where the test framework runs locally without issue.

### Pattern C: Multiple terminals, parallel agents

The tester opens multiple terminal windows, each running a different GTMS command. All commands run locally (Pattern B) or trigger remote work (Pattern A).

```
┌─ Terminal 1 ──────────────────┐  ┌─ Terminal 2 ──────────────────┐
│ gtms create REQ-123           │  │ gtms create REQ-456           │
│ gtms automate tc-a1b2c3d      │  │ gtms automate tc-e8d9c0b      │
│ gtms execute tc-a1b2c3d ...   │  │ gtms execute tc-e8d9c0b ...   │
└───────────────────────────────┘  └───────────────────────────────┘

┌─ Terminal 3 ──────────────────┐  ┌─ Terminal 4 ──────────────────┐
│ gtms create REQ-789           │  │ gtms status                   │
│ gtms automate tc-f4a3b2e      │  │ gtms gaps                     │
│ gtms execute tc-f4a3b2e ...   │  │ gtms map                      │
└───────────────────────────────┘  └───────────────────────────────┘
```

This is the GTMS "single-agent controller" model in action: one adapter per shell, multiple shells for parallelism. An external orchestration layer (tmux, scripts, a GUI) manages the fleet.

---

## 3. GitHub Actions Workflow for Remote Execution

This section shows a complete async execute adapter using GitHub Actions.

### The workflow file (`.github/workflows/test-runner.yml`)

```yaml
name: GTMS Test Runner

on:
  workflow_dispatch:
    inputs:
      test_case:
        description: 'Test case ID (e.g. tc-a1b2c3d)'
        required: true
        type: string
      spec_file:
        description: 'Path to spec file relative to repo root'
        required: true
        type: string

jobs:
  run-test:
    runs-on: self-hosted       # or ubuntu-latest for cloud runners
    timeout-minutes: 15

    steps:
      - name: Checkout repository
        uses: actions/checkout@v4

      - name: Setup Node.js
        uses: actions/setup-node@v4
        with:
          node-version: '20'

      - name: Install dependencies
        run: npm ci

      - name: Install Playwright browsers
        run: npx playwright install --with-deps chromium

      - name: Run test
        run: |
          npx playwright test "${{ inputs.spec_file }}" \
            --reporter=junit \
            --output=results/${{ inputs.test_case }}/
        continue-on-error: true

      - name: Upload results
        uses: actions/upload-artifact@v4
        if: always()
        with:
          name: test-results-${{ inputs.test_case }}-${{ github.run_number }}
          path: results/${{ inputs.test_case }}/
          retention-days: 30
```

### The adapter config (`gtms.config`)

```yaml
adapters:
  execute:
    github-actions:
      mode: async
      script: adapters/github-execute.sh
      status-script: adapters/github-execute-status.sh

defaults:
  execute: github-actions
```

### The trigger script (`adapters/github-execute.sh`)

```bash
#!/bin/bash
set -e

# Trigger the GitHub Actions workflow
gh workflow run test-runner.yml \
  --repo "${GTMS_REPO}" \
  --ref main \
  -f test_case="${GTMS_TESTCASE}" \
  -f spec_file="${GTMS_SPEC_FILE}"

# Wait briefly for the run to register
sleep 3

# Capture the run ID
RUN_ID=$(gh run list \
  --repo "${GTMS_REPO}" \
  --workflow=test-runner.yml \
  --limit 1 \
  --json databaseId \
  -q '.[0].databaseId')

# Update result contract with run reference
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

### The status script (`adapters/github-execute-status.sh`)

```bash
#!/bin/bash
set -e

# Read run ID from result contract
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
artefact: results/${GTMS_TESTCASE}/
summary: "Workflow ${RUN_ID}: ${CONCLUSION}"
log: https://github.com/${GTMS_REPO}/actions/runs/${RUN_ID}
completed: $(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF
fi
# If not completed, do nothing — result contract stays pending
```

### Usage

```bash
# Trigger remote execution
gtms execute tc-a1b2c3d --adapter github-actions \
  --spec-file test-automation/specs/local-claude/tc-a1b2c3d-login.spec.ts

# Poll for completion (run as many times as needed)
gtms execute status
```

> **Note:** The trigger and status scripts are reference implementations. Adapt them for your GitHub org, runner labels, and artefact handling. See [Adapter Guide — Tier 2 Examples](adapter-guide.md#examples-1) for more patterns.

---

## 4. Framework Bootstrap Prompts

### The concept

Rather than shipping generic prompt templates for every framework, GTMS uses **bootstrap prompts** — meta-prompts that an AI coding tool uses to analyse your project and generate project-specific configuration.

**Why bootstrap prompts are better than generic templates:**

Every project has its own test patterns, helper libraries, directory conventions, and assertion styles. A generic "write a Playwright test" template produces output that doesn't match your project. A bootstrap prompt says: "Look at this project's existing tests, understand its conventions, and generate GTMS config and templates that match."

**What a bootstrap prompt generates:**

1. **`gtms.config` adapter entries** — command templates with the right flags for your tools
2. **Automate prompt template** — project-specific instructions with `<gtms-file>` tag format rules, framework conventions, and existing test patterns
3. **Execute adapter config** — the command to run your framework's test runner

### How to use a bootstrap prompt

1. Copy the bootstrap prompt for your framework (see below)
2. Paste it into your AI coding tool (Claude Code, Cursor, etc.) in your project's directory
3. The AI analyses your project and generates the files
4. Review the generated config and templates
5. Run `gtms automate` and `gtms execute` to validate

### What each bootstrap prompt should include

If you're writing a bootstrap prompt for a framework not covered below, include these sections:

| Section | Purpose |
|---------|---------|
| **Project analysis instructions** | Tell the AI to scan for existing tests, config files, helpers |
| **GTMS adapter contract references** | Link to the adapter guide sections the AI needs |
| **Framework-specific guidance** | Framework conventions, assertion patterns, file naming |
| **Output format rules** | `<gtms-file>` tag format instructions (critical for streaming) |
| **Constraints** | What NOT to do (no code fences, no summaries, delimiter format is strict) |

---

## 5. Bootstrap Prompt: Playwright

Copy the prompt below and paste it into your AI coding tool while in your project directory. The AI will analyse your Playwright project and generate GTMS-compatible config and templates.

---

### Playwright Bootstrap Prompt

````
You are configuring GTMS (Git-based Test Management System) for a Playwright project. Your job is to analyse THIS project's Playwright setup and generate three files that integrate GTMS with the existing test infrastructure.

## Step 1: Analyse the project

Scan the project for:
- `playwright.config.ts` or `playwright.config.js` — base URL, test directory, reporters, browser config
- Existing test files (`*.spec.ts`, `*.spec.js`, `*.test.ts`) — patterns, imports, helpers, assertion styles
- Test helper files, fixtures, page objects — what abstractions exist
- `package.json` — Playwright version, test scripts, other test dependencies
- Directory structure — where tests live, how they're organized

Summarise what you find before generating files.

## Step 2: Generate the automate prompt template

Create a prompt template file for the GTMS automate command. This template tells an AI how to generate Playwright tests that match THIS project's conventions.

**File: `test-automation/prompts/automate-playwright.md`**

The template MUST include:
1. A role statement ("You are a test automation engineer...")
2. The test case content via `{testcase}` variable
3. The target framework via `{framework}` variable
4. THIS project's specific conventions (imports, helpers, fixtures, page objects) — extracted from Step 1
5. Example patterns from existing tests (sanitised/simplified)
6. Output format rules — this section MUST be at the END of the template (after all reference material) because LLMs have positional attention bias and deprioritise instructions buried in the middle

**Output format rules (include verbatim in the template):**

```
## Output Format (MANDATORY)

Output the generated test script using <gtms-file> XML tags.
The filename should match the test case ID with a descriptive slug.

<gtms-file name="tc-<id>-<slug>.spec.ts">
...test content...
</gtms-file>

Do NOT wrap output in code fences.
Do NOT include explanatory text after the file content.
Do NOT summarise — output ONLY the XML tags and file content.
One file per test case. If the test case has multiple scenarios, put them in one file.
```

**Template variable reference:**
- `{testcase}` — full content of the test case markdown file (always set for automate)
- `{framework}` — value of `--framework` flag (e.g. "playwright")
- `{context}` — additional context from `--context-file` (may be empty)
- `{guides}` — concatenated guide files from `guide-dir` config (may be empty)
- `{source}` — empty for automate (only set for create)
- Short variables like `{output_dir}`, `{task_id}`, `{branch}` are available but rarely needed in automate templates

## Step 3: Generate gtms.config adapter entries

Generate YAML adapter entries for this project. Use the actual paths, tools, and patterns discovered in Step 1.

**Automate adapter:**
```yaml
adapters:
  automate:
    playwright-claude:
      mode: sync
      prompt-template: test-automation/prompts/automate-playwright.md
      command: 'claude -p "Read the system prompt instructions. Generate a Playwright test script from the test case. Output using <gtms-file name=\"<filename>.spec.ts\"> tags, closed with </gtms-file>. No code fences. Raw text only." --append-system-prompt-file {prompt_file} --allowedTools ""'
```

**Execute adapter** (adjust the command based on `playwright.config.ts`):
```yaml
adapters:
  execute:
    playwright-runner:
      mode: sync
      command: 'npx playwright test {spec_file} --reporter=junit'
```

**Defaults:**
```yaml
defaults:
  automate: playwright-claude
  execute: playwright-runner
```

If the project already has a `gtms.config`, show only the adapter entries to merge — do not overwrite existing create adapters or project settings.

## Step 4: Verify

After generating the files:
1. Check that the automate prompt template references real helpers/fixtures from this project
2. Check that the execute command matches the project's `playwright.config.ts` settings
3. Check that all `<gtms-file>` tag format instructions are at the END of the prompt template

## Constraints

- Do NOT generate test cases or test scripts — only generate GTMS configuration and prompt templates
- Do NOT modify existing project files (package.json, playwright.config.ts, etc.)
- Do NOT add dependencies to the project
- The automate prompt template MUST include `<gtms-file>` tag format instructions — without them, GTMS cannot extract files from adapter output
- Put large reference material (project conventions, examples) in the MIDDLE of the prompt template. Put output format instructions at the END. See adapter-guide.md "Prompt Template Authoring" for why this matters.
````

---

## Future Bootstrap Prompts

The Playwright bootstrap prompt above is a template for other frameworks. Each follows the same four-step structure (analyse → generate template → generate config → verify) with framework-specific guidance.

| Framework | Status | Key differences from Playwright |
|-----------|--------|-------------------------------|
| **Playwright** | Available (above) | Baseline |
| **Cypress** | Planned | `cy.` commands, `cypress.config.js`, `cypress/support/` helpers |
| **pytest** | Planned | Python fixtures, `conftest.py`, `pytest.ini`/`pyproject.toml` |
| **Jest** | Planned | `jest.config.js`, test utilities, mock patterns |
| **Newman/Postman** | Planned | Collection-based, environment files, `newman run` command |
| **BATS** | Available in-project | See `test-automation/prompts/automate-bats.md` |

To contribute a bootstrap prompt for a new framework, follow the Playwright prompt structure and submit a PR.

---

## 6. Framework Integration Notes

Known gotchas discovered during dry runs and real usage. An AI agent reading these notes should never hit the same issue twice.

### Playwright

#### Config gotchas

| Gotcha | Detail |
|--------|--------|
| `testDir` restriction | `playwright.config.ts` `testDir` must include the directory where GTMS writes specs. Default is `./tests` which excludes `test-automation/specs/`. Fix: set `testDir: '.'` or move specs under `tests/`. Playwright silently skips specs outside `testDir` — no error, just zero tests found. |
| Multiple browser projects | Default Playwright config runs tests across multiple projects (chromium, firefox, webkit, mobile). 6 scaffold tests × 5 projects = 30 test runs. Use `--project=chromium` for faster feedback during development. |
| `forbidOnly` on CI | `forbidOnly: true` in CI config causes unexpected exit 1 if `test.only()` is left in a spec. |

#### Scaffold patterns

| Pattern | Detail |
|---------|--------|
| `test.fixme()` not `test.todo()` | Playwright has no `test.todo()`. Use `test.fixme('title', async ({ page }) => { })`. Claude may hallucinate `test.todo()` which causes runtime errors. |
| `test.fixme()` exit code | `test.fixme()` tests are reported as skipped and produce exit 0. Only failed assertions produce exit 1. |

#### Execute adapter notes

| Note | Detail |
|------|--------|
| Exit codes | `test.fixme()` = skipped = exit 0. Failed assertions = exit 1. |
| Reporter output | Use `--reporter=junit` for machine-readable output. JSON reporter (`--reporter=json`) sends to stdout which can interfere with Tier 1 adapters. |

#### Common AI hallucination risks

| Risk | Detail |
|------|--------|
| Page object hallucination | AI adapters invent methods that don't exist on page objects (e.g. `page.clickButton()`, `page.fillForm()`). The scaffold adapter (ENH-026) sidesteps this by generating `test.fixme()` stubs. Full automation adapters need project context via Tier 2 scripts. |
| `test.todo()` | Doesn't exist in Playwright. Claude will confidently generate it. Use `test.fixme()` instead. |
| `--allowedTools ""` blocks file reading | When using Claude with `--allowedTools ""`, the agent cannot read files from disk. Use `--context-file` for requirements, and GTMS auto-injects test case content via `{testcase_content}` for automate. |

#### Platform notes

| Note | Detail |
|------|--------|
| Binary format on Windows | Must use `gtms.exe` not `gtms` on Windows/MINGW. A Linux ELF binary silently fails — no error output, just nothing happens. |
| `CLAUDECODE` env var blocks nested sessions | When running GTMS from inside Claude Code (e.g. an AI agent operating the pipeline), Tier 1 adapters that invoke `claude` will fail with "cannot be launched inside another Claude Code session". Fix: `unset CLAUDECODE` before running `gtms` commands, or use a Tier 2 script adapter that unsets it internally. |

### BATS

BATS (Bash Automated Testing System) is used in-project for GTMS acceptance tests. See `test-automation/prompts/automate-bats.md` for the automate prompt template.

#### `go build` gotchas

| Gotcha | Detail |
|--------|--------|
| Absolute path breaks `go build` with spaces in path | `go build -o "$BIN" "$PROJECT_ROOT/cmd/gtms"` fails on MINGW64 when `PROJECT_ROOT` contains spaces (e.g. `My Documents`). Go reports `outside main module or its selected dependencies`. Fix: `cd "$PROJECT_ROOT" && go build -o "$BIN" ./cmd/gtms` — use a relative `./cmd/gtms` path after `cd`ing into the project root. |
| Git worktree paths | The `cd` + relative path fix is essential in git worktrees, where the working directory is typically deep (`.claude/worktrees/{name}/`). The absolute path form fails even without spaces in some configurations. |

#### `setup_file()` gotchas

| Gotcha | Detail |
|--------|--------|
| `PROJECT_ROOT` must be exported | `setup_file()` runs in a subprocess. If `PROJECT_ROOT` is not exported, it's invisible in `setup()` subshells. Use `export PROJECT_ROOT=...`. |
| Depth calculation for subdirectory tests | Tests in `test/acceptance/subdir/foo.bats` (3 levels) need `../../..` to reach the project root, and `load '../../test_helper/common-setup'` for the helper. Count carefully — off-by-one here silently fails to load helpers. |

#### Assertion gotchas

| Gotcha | Detail |
|--------|--------|
| Adapter echo output not in CLI stdout | GTMS CLI shows its own status lines (`✓ Task created...`), not the adapter's raw stdout. Adapter stdout goes into the result contract `summary` field. To assert on adapter output, read `.gtms/results/{task-id}.result.yaml` and check the `summary` field, not `assert_output`. |
| `ShellEscape` wraps empty strings | Tier 1 variable substitution shell-escapes all values (BUG-001 fix). Empty strings become `''` in the command. So `echo "{output_subdir}"` produces `''` not an empty string. Either match on `''` or use brackets `echo "[{output_subdir}]"` and match `['']`. |
| `--partial` mandatory | Always use `assert_output --partial` — exact matching breaks on CRLF line endings (Windows/MINGW). |

#### Automation record gotchas

| Gotcha | Detail |
|--------|--------|
| Execute requires `status: developed` | The `execute` command validates the automation record has `status` set to `accepted` or `developed`. A record missing the `status` field causes execute to fail with a confusing error. |
| Frontmatter field is `testcase` not `test_case_id` | Automation records use `testcase:` in frontmatter (matching the pipeline package struct tag). Test case files use `test_case_id:`. AI-generated tests often confuse the two. |

#### Streaming file output

| Gotcha | Detail |
|--------|--------|
| `$BATS_TEST_TMPDIR` in config YAML | When a mock adapter script path uses `$BATS_TEST_TMPDIR`, it must be in an unquoted section of the YAML so Bash expands it. Heredoc YAML with single-quoted strings prevents expansion. |
| Subdirectory filenames in `<gtms-file>` tags | Filenames with forward slashes (e.g. `name="widgets/tc-abc.bats"`) are allowed and the subdirectory is created automatically. Backslashes and `..` traversal are still rejected. |

---

## Related Documents

| Document | Purpose |
|----------|---------|
| [Adapter Guide](adapter-guide.md) | Full adapter contract reference — config, variables, result contracts |
| [Adapter Authoring Walkthrough](adapter-authoring-walkthrough.md) | Step-by-step tutorial for building adapters |
| [ARCHITECTURE.md](../ARCHITECTURE.md) | Package map, data flow, multi-machine model |
| [README.md](../README.md) | Quick start, command reference |
| [AI Coding Assistant Guide](ai-coding-assistant-guide.md) | How AI coding tools (Claude Code, Cursor) integrate with GTMS |
| [ADR-001](adr/ADR-001-prompt-delivery-via-file-and-stdin.md) | Why `{prompt_file}` is preferred over `{prompt}` |
| [ADR-009](adr/ADR-009-cli-as-integration-surface-for-ai-tools.md) | CLI as integration surface for AI coding assistants |
