# GTMS Adapter Guide

*Working reference for building, testing, and reviewing adapters.*

---

## Purpose

This document is the practical reference for anyone building a GTMS adapter. It describes the adapter interface contract, configuration format, environment variables, result reporting, and file output conventions.

It replaces the original [Command & Adapter Pattern](./archive/gtms-command-adapter-pattern.md) (archived); this guide is the current contract.

**This is a living document.** Sections are marked with their implementation status:
- **Implemented** — working in the current codebase
- **Planned** — designed but not yet implemented (with reference to the relevant enhancement or issue)

---

## Terminology

**Adapter** — a named, pluggable connector that does the actual work for a GTMS command. GTMS says *what* to do ("create test cases for JIRA-456"); the adapter decides *how* (spawn Claude Code, trigger a GitHub workflow, run Playwright). Adapters are registered in `gtms.config` under the command they serve.

**Sync adapter** — runs and finishes before returning control. GTMS waits, then shows the result. Like running a shell command.

**Async adapter** — kicks off work and returns immediately. The work happens in the background and the user checks on it later with `gtms {command} status`.

**Task file** — a markdown file in `gtms/tasks/` that records that work was requested. Every action command (create, automate, execute) creates one. Tracks what was asked, which adapter handled it, and what state the work is in. Task files are committed to git — they are the permanent audit trail.

**Result contract** — a YAML file in `.gtms/results/` that acts as the communication channel between an adapter and GTMS. GTMS creates it before invoking the adapter (pre-populated with task context). The adapter updates it with the outcome. Result contracts are transient working files, not committed to git.

**Pipeline record** — permanent metadata GTMS builds from result contracts and task context. Automation records, execution results, and task completion data are all pipeline records. GTMS owns their format — adapters never write them directly.

**Tier** — the implementation complexity of an adapter. Tier 1 is config-only (a single command template). Tier 2 is a script in any language. Tier 3 (Go module / SDK -- planned, not yet implemented) will provide native API integration. Choose the simplest tier that works.

---

## How Adapters Fit Into the Command Lifecycle

Every action command (create, automate, prime, execute) follows six phases. Adapters are involved in phases 3-6:

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
2. Resolves which adapter to use (flag override → config default → config lookup → built-in name table → error)
3. Generates a task ID and creates a task file in `gtms/tasks/pending/`
4. Creates a result contract in `.gtms/results/`
5. Builds an `AdapterContext` with everything the adapter needs
6. If `prompt-template` is set: assembles the prompt from the template and writes it to `.gtms/tmp/{task-id}-prompt.md`
7. Invokes the adapter (prompt is available via `{prompt_file}` / `$GTMS_PROMPT_FILE` and piped via stdin)

**What GTMS does after your adapter finishes:**
1. Reads the result contract (Tier 2) or infers from exit code (Tier 1)
2. Builds pipeline records (automation records, execution results)
3. Moves the task file to `complete/` or `error/`
4. Reports the outcome to the user

Your adapter's job is the middle part: receive context, do work, report the outcome.

---

## End-to-End Workflow: Create → Automate → Execute

The three action commands form a pipeline. Each command produces artefacts that the next command consumes. Understanding this chain -- especially the handoff between automate and execute -- is essential for building adapters that work together.

For full command reference (flags, modes, examples), see [USER-GUIDE.md](../USER-GUIDE.md#command-reference).

### Step 1: Create test cases from a requirement

```bash
gtms create login --reference REQ-123 --context-file requirements/REQ-123.md
```

The `<folder>` argument specifies where files land under `gtms/test/cases/`. The `--reference` flag provides a label for traceability. The `--context-file` flag tells GTMS to read the file and inject its content as `{context}` in the prompt template.

> **Why `--context-file`?** The `{reference}` variable passes the flag value **as a string** -- it doesn't read files. If your adapter uses `--allowedTools ""` (recommended), the AI agent can't read files from disk either. `--context-file` tells GTMS to read the file and inject its content as `{context}`.

**Without an AI adapter configured**, the skeleton create adapter (built-in) generates a blank test case file. Use `gtms create <folder> <name>` to create named skeletons that you fill in by hand.

**What happens:**
1. GTMS invokes the create adapter with the assembled prompt
2. The adapter generates test cases and outputs them using `<gtms-file>` tags
3. GTMS writes the files to `gtms/test/cases/<folder>/`
4. Task file moves to `gtms/tasks/complete/`

**Files produced:**
```
gtms/test/cases/login/tc-a1b2c3d-valid-credentials.md
gtms/test/cases/login/tc-e8d9c0b-invalid-password.md
gtms/tasks/complete/task-f3a91b7-create-login.md
```

Each test case is a markdown file with YAML frontmatter (`id`, `title`, `requirement`, `status`) and structured steps.

### Step 2: Automate a test case

```bash
gtms automate tc-a1b2c3d --framework playwright
```

**What happens:**
1. GTMS invokes the automate adapter with `{testcase}` = `tc-a1b2c3d` and `{framework}` = `playwright`
2. The adapter reads the test case, generates an automation script, and outputs it using `<gtms-file>` tags
3. GTMS writes the spec file to the framework's output dir -- for the `playwright` preset `gtms/scripts/playwright/`, for the `bats` preset `test/acceptance/` (an explicit `output-dir:` on the adapter overrides the default)
4. GTMS creates (or updates) a wiring record at `gtms/automation/wiring/tc-a1b2c3d--playwright.wiring.yaml`
5. The wiring record's `artefact` field is populated with the path to the generated spec file

**Files produced:**
```
gtms/scripts/playwright/tc-a1b2c3d-valid-credentials.spec.ts
gtms/automation/wiring/tc-a1b2c3d--playwright.wiring.yaml
gtms/tasks/complete/task-b2c8d41-automate-tc-a1b2c3d.md
```

### Step 3: Execute the automated test

```bash
gtms execute tc-a1b2c3d
```

**What happens:**
1. GTMS reads the wiring record for `tc-a1b2c3d` and extracts the `artefact` field (the spec file path)
2. GTMS invokes the execute adapter with `{testcase}` = `tc-a1b2c3d` and `{artefact_file}` = the artefact path
3. The adapter runs the test (e.g. `npx playwright test {artefact_file}`)
4. GTMS writes the outcome to the result handoff `.gtms/results/<task>.handoff.yaml` (`status`, `result`, `summary`) plus a per-test row `gtms/execution/<task>--<tc>.results.yaml`. The wiring record is not mutated by execute.
5. Task file moves to `gtms/tasks/complete/` or `gtms/tasks/error/`

**Manual result recording (via the prime pipeline):**

```bash
gtms prime tc-a1b2c3d --framework manual                          # stamp a result template
# Edit gtms/manual/records/tc-a1b2c3d--manual.result.yaml         # set result: pass|fail|skip
gtms execute tc-a1b2c3d --adapter manual-execute                  # record the outcome
```

Manual results go through the `manual-execute` adapter (Tier 0 built-in or Tier 2 script), which reads the user-edited result file and writes the standard pipeline handoff contract. The Tier 0 built-in is used when no config entry exists; a config entry with `script:` activates the Tier 2 script. See the manual-execute adapter contract tables below for the full env-var and result-contract shapes.

### The Handoff: Automate → Execute

The connection between automate and execute is **automatic**. After `gtms automate` completes, the wiring record contains an `artefact` field with the path to the generated spec file. When you run `gtms execute`, GTMS reads this artefact path and passes it as `{artefact_file}` to the execute adapter.

You don't need to supply the spec file path manually -- GTMS handles the handoff:

```bash
# 1. Automate — generates spec file, records artefact path
gtms automate tc-a1b2c3d --framework playwright

# 2. Execute -- GTMS reads artefact from the wiring record automatically
gtms execute tc-a1b2c3d
```

> **Prerequisite:** `gtms execute` requires a wiring record for the TC and framework (written by `gtms automate` or `gtms link`). The wiring record has no lifecycle status -- if none exists, execute fails with `No wiring record found for 'tc-XXX' (framework: bats)`.

> **`artefact:` is a cache, not the source of truth.** The stored path is an opportunistic pointer to the spec file; the canonical identity is the **TC ID plus framework**. `gtms execute` reads the artefact path straight from the wiring record and runs it -- filenames need not contain the TC ID (that rule was dropped to support shared-file frameworks like Playwright grouped tests, where the TC ID lives only in the test name). It does **not** glob the project tree for `tc-{id}`, and it does **not** rewrite or auto-heal the record. If the stored path is missing from disk, execute surfaces that failure rather than searching for a replacement -- repoint the wiring record with `gtms link` (or refresh it with `gtms link --refresh`), do not rely on a glob. Adapter authors should write the `artefact:` field accurately on automate so the pointer stays valid and the record remains a reliable human breadcrumb.

### Full pipeline at a glance

```
gtms create login --reference REQ-123 --context-file requirements/REQ-123.md
  └─→ gtms/test/cases/login/tc-a1b2c3d-valid-credentials.md

gtms automate tc-a1b2c3d --framework playwright
  ├─→ gtms/scripts/playwright/tc-a1b2c3d-valid-credentials.spec.ts
  └─→ gtms/automation/wiring/tc-a1b2c3d--playwright.wiring.yaml
        └─ artefact: gtms/scripts/playwright/tc-a1b2c3d-valid-credentials.spec.ts

gtms execute tc-a1b2c3d    (reads artefact path from wiring record)
  └─→ result handoff written: .gtms/results/<task>.handoff.yaml
        (status: complete | error, result: pass | fail | skip | error,
         completed: RFC3339 UTC, artefact, summary)
  └─→ per-test results row written: gtms/execution/<task>--<tc>.results.yaml
```

The wiring record is not mutated by `gtms execute`. The
per-run substrate is the result handoff (`.gtms/results/<task>.handoff.yaml`);
the per-test row (`gtms/execution/*.results.yaml`) is the durable secondary
substrate. Dashboards join the handoff onto wiring (by testcase + framework)
and surface per-framework state via `frameworks[].last_executed_here` /
`frameworks[].last_result_here` / `frameworks[].last_status_here` in
`gtms status --json`.

---

## Brownfield: Registering Pre-Existing Tests with `gtms link`

The standard pipeline assumes `gtms automate` *generates* the test artefact and writes the automation record. When the artefact already exists -- hand-authored BATS, an AI-generated spec produced outside GTMS, or a legacy test suite being adopted into GTMS -- there is no record on disk, and `gtms execute` will refuse with `No wiring record found for 'tc-XXX' (framework: bats)`.

`gtms link` is the brownfield entry point. It writes a wiring record pointing at an existing artefact file, whose `adapter:` field is the canonical execute adapter for the framework (e.g. `bats-runner`, `playwright-runner`). The user asserts that the framework's filter convention (TC ID embedded in the test name) is satisfied -- GTMS does not invoke any framework CLI to verify this. If the assertion is wrong, the error surfaces at `gtms execute` time when the framework's `--grep` finds zero matches.

### When to reach for it

- You have a `.bats` / `.spec.ts` / `.test.js` file on disk that was authored or generated outside `gtms automate`, and you want to run it through the pipeline.
- You're adopting a pre-existing test suite into GTMS (Brownfield Scenario B in the SPEC).
- A sub-agent or inline editor produced the artefact directly and skipped the automate step (recovery path).

### Single-TC form

```bash
gtms link tc-04221dff \
  --framework bats \
  --artefact test/acceptance/link-command-record-primitive/tc-04221dff-link-error-existing-record-without-force.bats
```

Checks: artefact file exists (filesystem only). Writes: `gtms/automation/wiring/tc-04221dff--bats.wiring.yaml`.

### Bulk-link loop (the pattern that worked for 23 BATS scripts)

```bash
for batsfile in test/acceptance/{slug}/tc-*.bats; do
  basename=$(basename "$batsfile" .bats)
  tc_id=$(echo "$basename" | sed -E 's/^(tc-[a-f0-9]{8}).*/\1/')
  ./gtms.exe link "$tc_id" --framework bats --artefact "$batsfile"
done
```

Adapt the regex for your framework's filename convention. The loop is idempotent only with `--force`: a second run without `--force` errors on each existing record (this is by design — `gtms link` will not silently overwrite).

### What a linked record looks like

A wiring record is pure YAML with exactly six identity fields.
There is no `status`, `branch`, `attempts`, or `cycle` -- those are not identity:

```yaml
testcase: tc-04221dff
testcase-hash: 4f2a9c1b7e6d0a35
framework: bats
adapter: bats-runner        # canonical execute adapter for the framework (resolved from config)
artefact: test/acceptance/link-command-record-primitive/tc-04221dff-link-error-existing-record-without-force.bats
artefact-hash: 8c91d4a2f077be13  # computed from the artefact file at link time
```

After a subsequent `gtms execute`, the per-run state is written to the result
handoff at `.gtms/results/<task>.handoff.yaml`; the
record itself is not mutated by execute. The handoff carries:

```yaml
status: complete
result: pass
completed: "2026-04-29T23:07:23Z"
summary: "All 3 tests passed"
```

The dashboard renders linked TCs identically to automate-produced TCs (`✓ ✓ ✓ pass [bats]`) -- provenance is not a status. Nor does the wiring record itself distinguish the two: its `adapter:` field is the canonical execute adapter for the framework (e.g. `bats-runner`), the same value an automate-produced record carries. The six-field wiring record encodes identity, not provenance, so there is no on-disk field that records whether a TC was linked or automated -- if you need to know, the artefact's own git history is the only trail.

### Useful flags

- `--check` — read-only validation. Reports artefact existence and record state, exits non-zero on missing artefact or absent record. Does not write.
- `--refresh` -- re-acknowledge existing wiring after you have reviewed a spec or artefact edit, without relinking or regenerating. It recomputes `testcase-hash` and `artefact-hash` in place, preserves the `framework` / `adapter` / `artefact` identity fields, runs no automate adapter, and never touches the artefact file. This is the safest of the three stale-wiring recovery paths (see the drift lesson below); reach for it when only the spec changed and the artefact still asserts the current intent. `gtms link --refresh tc-XXXXXXXX` refreshes every framework wiring for that TC (narrow with `--framework <fw>`); `gtms link --refresh <folder>` refreshes only the stale records under the folder (`-r` / `--recursive` descends, mirroring `gtms status` / `gtms execute`). Writes are per-record, not atomic across a batch: a record whose artefact was deleted or moved outside the project-owned allowlist fails with a diagnostic while the rest complete, and the command exits non-zero if any failed. A wiring record still carrying `artefact-hash: pending` (the bootstrap shape) keeps `pending` -- only `testcase-hash` is refreshed -- so it stays first-executeable. The other link flags do not combine with `--refresh`: `--artefact` (that is a relink), `--check` (use `gtms status`), and `--force` (`--refresh` is itself the acknowledgement) are each rejected with a diagnostic pointing at the right form. (`--env` and `--executed-by` were never wiring fields and were removed from `gtms link` entirely in BUG-137; passing them to any `link` form now fails with Cobra's `unknown flag` error.)
- `--force` -- overwrite an existing record (and refresh the paired wiring at `gtms/automation/wiring/<tc>--<framework>.wiring.yaml`, including `artefact-hash`). Writes fresh fields from the new flag values. Reach for `--force` only when you are relinking to a different artefact; when only the spec changed and you just want to acknowledge the current artefact, prefer `--refresh` above (it preserves the artefact and recomputes hashes without a relink). Per-run execute state lives on `.gtms/results/<task>.handoff.yaml`; existing handoffs are not deleted by `gtms link --force`, but the next `gtms execute` will write a fresh one alongside (or replace the prior one when the task-id collides).
- `--strict` -- opt-in TC-spec preflight. Rejects link calls whose TC ID has no matching spec under `gtms/test/cases/`, with `test case 'tc-XXXXXXXX' not found in gtms/test/cases/`. Off by default — the brownfield contract (link the artefact first, write the spec later) is preserved exactly as it was. Combine with `--check` (`gtms link --check --strict`) for a read-only validation that also catches phantom TC IDs. Use it when scripting bulk imports from a list of TC IDs and you want to fail fast on typos. Supports folder-qualified targets: `gtms link folder/tc-abc12345 --strict ...` requires the spec to live under `gtms/test/cases/folder/`.

### What `gtms link` is not

- **Not a framework verifier.** It does not run `playwright test --list --grep tc-{id}` or `bats --filter tc-{id}` or any other framework command. Verification (the "shift-left" check that exactly one test in the artefact matches the TC's filter) is the **adapter's** responsibility, run during automate or as a pre-execute check inside the adapter — not GTMS core. This boundary is deliberate (SOUL.md: GTMS conducts; adapters know their frameworks).
- **Not a substitute for `gtms automate`** when you actually want a fresh AI-generated artefact. Use automate for greenfield; use link only when the artefact already exists.

---

## Working with File-Based Requirements

When the source for `gtms create` is a file on disk, use `--context-file` to tell GTMS to read the file content and inject it into the prompt.

```bash
gtms create login --reference REQ-123 --context-file requirements/REQ-123.md
```

GTMS reads `requirements/REQ-123.md`, and the file content becomes available as `{context}` in prompt templates and `$GTMS_CONTEXT` for Tier 2 scripts. The `--reference` flag provides the traceability label (it passes the value as a string, not a file path).

### Why `--context-file` is needed

When your adapter uses `--allowedTools ""` (recommended in all adapter examples to ensure raw text output), the AI agent cannot read files from disk independently. Without `--context-file`, the agent receives only the reference string -- not the file content -- and produces zero output after a long wait.

> **Rule of thumb:** If your source is a local file and your adapter uses `--allowedTools ""`, always pass `--context-file` pointing to the file. This is the most common `gtms create` scenario.

### How it works in the prompt

Your create adapter's prompt template (the file named by `prompt-template:` in `gtms.config`, which you author -- `gtms init` does not scaffold one) has a `{context}` placeholder. When `--context-file` is set, GTMS reads the file and substitutes its content into that placeholder before assembling the final prompt. The adapter then receives the full requirement text inline -- no file access required.

See the [Tier 1 Variable Reference](#variable-reference) and [Tier 2 Environment Variable Reference](#environment-variable-reference) for the full list of context variables.

---

## Configuration

Adapters are registered in `gtms.config` under the command they serve. For the full configuration reference (all fields, validation rules, tier explanations), see [USER-GUIDE.md -- Configuration](../USER-GUIDE.md#configuration).

**Verifying your adapter is registered:** after editing `gtms.config`, run `gtms list adapters` to confirm the adapter appears in the expected command bucket with the right tier, framework, and mode. `gtms list adapters --show-tools` adds the command/script template column so you can audit the template that will actually be invoked. For scripting (CI shell-completion, agent tooling), `gtms list adapters --json` emits a stable schema. See [USER-GUIDE § gtms list](../USER-GUIDE.md#gtms-list).

### Choosing a Tier

| | **Tier 1** (command template) | **Tier 2** (script) |
|---|---|---|
| **Config** | `command: 'tool {artefact_file}'` | `script: gtms/adapters/my-script.sh` |
| **How it runs** | `sh -c` (with `cmd /c` fallback on Windows) | `sh <script>` (no fallback — requires `sh`) |
| **Context** | `{variable}` substitution in command string | `GTMS_*` environment variables |
| **Result handling** | Exit code only (0 = pass, non-zero = error; opt-in `fail-exit-codes:` maps listed codes to `fail`) | Script writes `$GTMS_RESULT_FILE` directly |
| **Can distinguish fail from error?** | Yes -- via `fail-exit-codes:` (opt-in) | Yes -- script writes `fail` or `error` explicitly |
| **Good for** | Single-command tools (bats, pytest, playwright, jest) | Multi-step workflows, CI triggers, custom result parsing |
| **Choose when** | Your tool is one command and its exit code already distinguishes pass/fail/error | You need conditional logic, output parsing (TAP/JUnit), or multiple steps |

**Default to Tier 1.** Only reach for Tier 2 when Tier 1 can't express what you need.

### Minimal Tier 1 Example

```yaml
project:
  name: "My Project"
  repo: org/my-repo

adapters:
  create:
    my-adapter:
      mode: sync
      command: 'my-tool --input {reference} --output {output_dir}'

defaults:
  create: my-adapter
```

### Placeholders go in BARE. Do not put shell quotes around them.

This is the single most important rule for writing a Tier 1 `command:`, and getting it
wrong fails silently.

**GTMS shell-escapes every Tier 1 placeholder value into one complete shell token before
it substitutes it.** A value with no special characters is passed through as-is. A value
containing a space, a backslash, or a shell metacharacter is wrapped in single quotes for
you. An empty value becomes `''`. You never have to quote a placeholder, and you must not.

So write this:

```yaml
command: 'my-tool --input {reference} --output {output_dir}'
```

Not this:

```yaml
command: 'my-tool --input "{reference}" --output "{output_dir}"'   # WRONG
```

**Why the wrong form is dangerous.** Suppose `{output_dir}` is `/tmp/my project/out`.
GTMS escapes it to the complete token `'/tmp/my project/out'`. If you wrapped it in your
own double quotes, the shell now receives:

```sh
my-tool --output "'/tmp/my project/out'"
```

The single quotes are no longer shell syntax -- they are literal characters in the path.
Your tool writes to a directory whose name starts with a quote character, exits 0, and
GTMS records a **pass** for a run that produced nothing where you expected it. That is
the whole bug: it does not crash, it lies.

**Prefixes and suffixes are fine.** A bare placeholder still composes, because adjacent
quoted and unquoted shell fragments form a single word:

```yaml
command: 'my-tool --out {output_dir}/report.xml'     # correct
command: 'my-tool --out={output_dir}'                # correct
```

**YAML quoting is a different thing, and it is fine.** The outer `'...'` around the whole
`command:` value above is YAML syntax -- it tells the YAML parser where the string starts
and ends. That is unrelated to putting shell quote characters *around a placeholder*
inside the string. Keep the YAML quotes; drop the shell quotes.

**Quote characters elsewhere in the template are fine too.** What matters is only whether
a `{placeholder}` sits inside them. This is a perfectly good command:

```yaml
command: 'my-ai-tool -p "Generate a spec. Raw text only." --system-file {prompt_file} --allowedTools ""'
```

The prose is quoted, there is an empty quoted argument, and `{prompt_file}` is bare. That
is exactly right.

**To put a value inside prose, use a prompt template, not the command.** Prompt-template
substitution is deliberately *textual and unescaped*, which is what prose needs. Put the
placeholder in the template file, and let the command consume the assembled result as a
bare `{prompt_file}`:

```yaml
prompt-template: prompts/create.md    # may contain: Create cases for "{reference}".
command: 'my-ai-tool --system-file {prompt_file}'
```

**Tier 2 is not affected by this rule.** Tier 2 adapters receive `GTMS_*` environment
variables, not substituted text, so normal shell quoting applies and is correct:

```sh
mkdir -p "$GTMS_OUTPUT_DIR"    # correct in a Tier 2 script
```

### Two invocation-time template warnings

`InvokeTier1` inspects the **original** `command:` template before substitution and can
emit two independent stderr warnings. Both are non-fatal: execution proceeds either way.

Both scan the `command:` template **only**. Neither ever inspects your `prompt-template`
file or the assembled prompt. Prompt templates use the same `{placeholder}` syntax but
follow the deliberately unescaped textual-substitution contract, so quoted prose there is
valid and is never warned about.

**Typo detection (unrecognised variable).** Unknown `{...}` placeholders do not fail
silently. GTMS warns, naming any token that is not a recognised variable, and lists the
valid alternatives. So `command: 'bats {artefact}'` (a typo of `{artefact_file}`) produces
a warning at invocation rather than a downstream `bats: file not found`. Detection is
broad: `\{[^}]+\}` matches any brace-delimited token, including uppercase, hyphenated, or
digit-bearing names.

**Shell-quoted placeholder detection.** If a **recognised** placeholder sits inside single
or double shell quotes -- `"{output_dir}"`, `"{output_dir}/seen.txt"`,
`"--output={output_dir}"`, `"prefix-{reference}-suffix"` -- GTMS warns and tells you how to
fix it. See the section above for why the quoted form silently corrupts the value.

Only **recognised** placeholders trigger it. An unrecognised token is never substituted, so
it is never escaped, so it cannot collide with your quotes -- that one gets the typo
warning instead. The two never both fire for the same token. The check names only the
offending placeholder: a bare placeholder in the same command is never mentioned, because
telling you to unquote something already correct would send you to fix working code.

Both warnings run against the **original** template, not the substituted command, so quote
characters that arrive inside a *value* (e.g. within `{context}` or `{testcase_content}`)
are treated as data and never trigger a false positive.

#### Limits and special cases

**It stays silent when it cannot tell.** If the template's quoting is unbalanced or
otherwise ambiguous, no warning is emitted at all. Under-warning is the deliberate trade:
the warning cannot block a bad run, so a false alarm on valid config would be pure noise --
and noise teaches you to ignore the one warning that matters.

**Command substitution is suppressed.** A placeholder inside a matched `$(...)` region does
**not** warn: `"$(cat {testcase_file})"` is valid and always works, because the
placeholder's real quoting context is the inner shell of the substitution, where GTMS's
escaping is correct. The suppression is region-based: if `$(...)` has already closed before
the placeholder (`"$(date) {output_dir}"`), the placeholder is back in the outer quoted
word and **does** warn. Nested substitutions work too.

**Multi-word arguments and second-shell commands still warn.** Under the complete-token
contract there is no Tier 1 way to build a multi-word shell argument containing an
interpolated value, because a bare `{x}` is always its own token. `git commit -m "Automate
{testcase}"` works today only because the value happens to be safe-charset; the moment a
value contains a space, GTMS single-quotes it and literal quote characters land inside the
argument. Same story for `ssh host "run {testcase}"` -- GTMS's escaping guarantee covers
only its immediate shell, not a second one.

**The remedy offers two routes**, depending on what you are doing:

1. **Interpolating into prose** -- move it to a `prompt-template` file (textual, unescaped
   substitution) and consume the result as bare `{prompt_file}`.
2. **Composing a shell argument or building a command for a second shell** -- use a Tier 2
   adapter or a wrapper script, where the value arrives as a `GTMS_` environment variable
   and normal shell quoting applies: `git commit -m "Automate $GTMS_TESTCASE"`.

### Minimal Tier 2 Example

```yaml
project:
  name: "My Project"
  repo: org/my-repo

adapters:
  create:
    my-script-adapter:
      mode: sync
      script: gtms/adapters/my-create.sh

defaults:
  create: my-script-adapter
```

### Full Config Example

This is the config the `bats` preset scaffolds -- the fullest of the three shipped
presets. Note the shape: the `defaults` point at Tier-0 built-ins, while richer
`manual-*-script` / `agent-*-script` Tier-2 slots ship *registered but dormant* so
you can activate one by making it the default (or via `--adapter`) without editing
scripts. The top-level `guidance: true` enables onboarding hints.

```yaml
project:
  name: "My Project"
  repo: org/my-repo

guidance: true                                     # onboarding hints on (default); set false to silence

adapters:
  create:
    manual-create-script:                          # dormant Tier 2; make it the default to activate
      mode: sync
      script: gtms/adapters/manual-create-script.sh
    agent-create-script:                           # dormant Tier 2 (agent role)
      mode: sync
      script: gtms/adapters/agent-create-script.sh
  prime:
    manual-prime-script:                           # dormant Tier 2
      mode: sync
      script: gtms/adapters/manual-prime-script.sh
      framework: manual
    agent-prime-script:                            # dormant Tier 2 (agent role)
      mode: sync
      script: gtms/adapters/agent-prime-script.sh
      framework: manual
  automate:
    agent-automate:                                # Tier 0 built-in; the agent fills the stamped skeleton
      mode: sync
      framework: bats
  execute:
    manual-execute-script:                         # dormant Tier 2
      mode: sync
      script: gtms/adapters/manual-execute-script.sh
      framework: manual
    agent-execute-script:                          # dormant Tier 2 (agent role)
      mode: sync
      script: gtms/adapters/agent-execute-script.sh
      framework: manual
    bats-runner:                                   # Tier 2 runner shipped by the bats preset
      mode: sync
      script: gtms/adapters/bats-runner.sh
      framework: bats
      output-dir: test/acceptance
      artefact-glob: "test/acceptance/**/{testcase}*.bats"

defaults:
  create: manual-create        # Tier 0 built-in (the script slots above stay dormant)
  prime: manual-prime          # Tier 0 built-in
  automate: agent-automate     # Tier 0 built-in, framework bats
  execute: bats-runner         # Tier 2 script shipped with the bats preset
```

> **Note:** The `execute` default determines which test runner GTMS invokes when `--adapter` is not specified. The shipped `bats` and `playwright` presets already default `execute` to their runner (`bats-runner` / `playwright-runner`), so this is handled for you out of the box. The warning only applies if you hand-write a config that leaves `execute` defaulted to `manual-execute` while your tests are BATS -- then bare `gtms execute` looks for a manual result and fails on an automated TC. Either set `defaults.execute: bats-runner` or pass `--adapter bats-runner`.

### Config Fields

**Top-level keys** (siblings of `project`, not adapter fields): `guidance: true`
(default) prints onboarding hint output after commands; set `guidance: false` to
silence it. Every shipped preset sets `guidance: true`. `demo_seeded` is a reserved
internal flag written by `gtms init --demo` -- do not set it by hand.

**Required:**

| Field | Description |
|-------|-------------|
| `mode` | `sync` or `async`. Sync: GTMS waits for completion. Async: GTMS returns immediately, user polls with `gtms {command} status`. |

**Implementation (exactly one of):**

| Field | Tier | Description |
|-------|------|-------------|
| `command` | 1 | Command template with `{variable}` placeholders. GTMS substitutes values and shells out. |
| `script` | 2 | Path to executable script (relative to project root). GTMS exports `GTMS_` environment variables and executes. |
| *(none)* | 0 | Built-in adapter handled by GTMS core. Visibility commands use `local-reader` (status/gaps/triage). Action commands have eight named built-ins across create/automate/prime/execute: `agent-create`, `manual-create`, `agent-automate`, `manual-automate`, `agent-prime`, `manual-prime`, `agent-execute`, `manual-execute`. The shipped `bats`/`playwright` presets default `automate` to `agent-automate`. |

**Optional:**

| Field | Description |
|-------|-------------|
| `prompt-template` | Path to a prompt template file (relative to project root). GTMS reads the template, injects context variables, writes the assembled prompt to `.gtms/tmp/{task-id}-prompt.md`, and pipes it via stdin. For Tier 1: the file path is available as `{prompt_file}` and the content as `{prompt}` (deprecated). For Tier 2: the file path is available as `$GTMS_PROMPT_FILE`. |
| `guide-dir` | Path to a directory of `.md` guide files (relative to project root). GTMS reads all `.md` files alphabetically, wraps each in `<guide name="filename.md">` XML tags, and makes the content available as `{guides}` (Tier 1) or `$GTMS_GUIDES` (Tier 2). Guides resolve from ONE directory: `guide-dir` if set, otherwise the default `gtms/test/guides/`. A custom `guide-dir` **replaces** the default -- it does not augment it, so the shipped `gtms/test/guides/gtms-test-case-authoring-guide.md` is no longer read unless you leave `guide-dir` unset or include that guide's content in your directory. There is no CLI flag for guides -- it is config-only. If the resolved directory doesn't exist, the value is empty (no error). |
| `status-script` | Path to a script that checks async adapter progress. Called during `gtms {command} status`. Must update `$GTMS_RESULT_FILE` when the remote work completes. Requires `mode: async` and `script` to be set. |
| `output-dir` | **Dual-role: write target + read root for discovery.** Where adapter output files are written (relative to project root). Must be a relative path -- absolute paths are rejected. An explicit `output-dir` always wins, for every tier including the built-in automate adapters. If not set, GTMS uses the standard default for the command and adapter class: create -> `gtms/test/cases/<folder>/`; automate on a **command/script (Tier 1/2)** adapter -> `gtms/automation/specs/<adapter>/`; automate on the **built-in `agent-automate`/`manual-automate`** -> the framework-native dir (`test/acceptance/` for BATS, `gtms/scripts/playwright/` for Playwright); execute -> `results/`. The same value also tells GTMS's runtime artefact discovery where to look when the stored `artefact:` cache is stale, so **declare it accurately even for read-only pipeline stages** (e.g. execute adapters that read pre-generated spec files). Brownfield: point `output-dir` at the directory your framework already scans (its `testDir`) so the built-in automate writes there and the framework discovers the spec. |
| `timeout` | Maximum duration for adapter execution (e.g. `30s`, `5m`, `1h`). Uses Go duration format. If the adapter exceeds this time, GTMS cancels it and reports an error. **Default: 30 minutes for sync adapters**. Set an explicit value to override. On cancellation, GTMS terminates the full process tree (not just the immediate child). The default can also be overridden at runtime via the `GTMS_DEFAULT_EXECUTE_TIMEOUT` env var (Go duration format); intended for acceptance-test harnesses so they don't have to wait for the 30-minute production default. Empty, unparseable, or non-positive values are ignored and the 30-minute default is used. |
| `framework` | Framework name for this adapter (e.g. `bats`, `playwright`, `pw-mobile`). Used to qualify wiring records: `tc-xxx--{framework}.wiring.yaml`. **Must be unique per command** -- two adapters under the same command with the same framework name will overwrite each other's records. See [Multi-Framework Adapters](#multi-framework-adapters) below. |
| `working-dir` | Project-relative directory the adapter process runs in (its current working directory). Applies to Tier 1 (`command:`) and Tier 2 (`script:`) adapters only -- setting it on a built-in (Tier 0) adapter emits a `⚠` load-time warning and is ignored. Must be a relative path (absolute rejected) and may not escape the project root with `..`. Unset: the adapter runs from the project root. Distinct from `output-dir` (where output goes) and from `{work_dir}` / `$GTMS_WORK_DIR` (the worktree base handed to the adapter for reference), neither of which `working-dir` changes. Brownfield use: point it at a harness subdirectory (its own `node_modules/` and config) so a shipped runner works unedited. |
| `artefact-glob` | Glob pattern with a `{testcase}` placeholder (must contain it; project-relative, no `..`; `**` matches recursively). Historically fed a lazy artefact-discovery/auto-create step, but the wiring cutover made the wiring record immutable on the execute path: `gtms execute` no longer globs or auto-creates a record, and a missing wiring record is a hard error resolved via `gtms automate` or `gtms link`. The field is still accepted and validated at config load. Example: `test/acceptance/**/{testcase}*.bats`. |

Create-stage stamped templates and prompt guides are separate artefact families. Files under `gtms/test/templates/*.template.md`, such as `manual-testcase.template.md` and `agent-testcase.template.md`, are stamped by create adapters. Files under `gtms/test/guides/*.md`, such as `gtms-test-case-authoring-guide.md`, are read from `guide-dir`, XML-wrapped, and injected into prompts as reference material.

**Validation rules:**
- `mode` must be `sync` or `async`
- At most one of `command`, `script`, `module` can be set
- `status-script` requires both `mode: async` and `script`
- `timeout` must be a valid Go duration (e.g. `5m`, `30s`, `2m30s`)
- `artefact-glob` must contain `{testcase}`, be project-relative (no absolute paths or `..`)
- `framework` must match `^[a-z0-9][a-z0-9-]*$` (lowercase letters, digits, hyphens only)
- `output-dir` must be a relative path (absolute paths are rejected)
- `working-dir` must be a relative path and must not contain `..` (parent-escape rejected); on a built-in adapter it warns and is ignored
- Defaults must reference an adapter registered under the same command

**Brownfield example -- run a harness from its subdirectory (`working-dir`):**

```yaml
adapters:
  execute:
    playwright-runner:
      mode: sync
      script: gtms/adapters/playwright-runner.sh   # shipped runner, unedited
      framework: playwright
      working-dir: PLAYWRIGHT-TESTS                 # run from the harness (its config + node_modules)
      output-dir: PLAYWRIGHT-TESTS/tests/gtms       # where the specs live (orthogonal knob)
```

`working-dir` sets the cwd; `output-dir` sets where output goes; they are
independent, and neither changes `{work_dir}` / `$GTMS_WORK_DIR` (the worktree
base). Omit `working-dir` and the adapter runs from the project root as before.

### Cancellation and process containment

Every sync-adapter invocation is bounded: the adapter's configured `timeout:` if
set, otherwise a 30-minute default. GTMS never waits on an adapter indefinitely.

When a timeout fires, GTMS terminates the **entire descendant process tree**, not
just the immediate child. On Unix the child is made its own process-group leader
(`Setpgid`) and the whole group is signalled; on Windows the child is wrapped in a
Job Object that reaps every descendant even if the immediate parent has exited.
Grandchildren, backgrounded descendants, and child servers that hold inherited
stdout/stderr open are all reaped -- this is what stops a wedged Playwright HTML
report server (see the Playwright note) from stranding the run.

**Out of scope:** a process that fully detaches (`setsid`, daemonises, or
re-parents to init) can escape the group/job and survive the timeout. Don't rely
on GTMS to reap a deliberately detached daemon.

A timeout writes `status: error` to the result contract with a timeout-attributing
summary, moves the task file to `gtms/tasks/error/`, and returns non-zero -- no
stranded `in-progress` task. `GTMS_DEFAULT_EXECUTE_TIMEOUT` (Go duration) overrides
the 30-minute default at runtime; it is a testability hook for acceptance harnesses,
not a production setting (empty, unparseable, or non-positive values are ignored).

### Command-Scoped Registration

Adapters are registered under the command they serve. The same adapter name can appear under multiple commands with different config:

```yaml
adapters:
  create:
    my-ai:                                     # uses your create prompt template
      mode: sync
      prompt-template: gtms/test/prompts/create.prompt.md
      command: 'my-ai-tool --system-file {prompt_file} --allowedTools ""'
  automate:
    my-ai:                                     # same name, different command bucket
      mode: sync
      prompt-template: gtms/automation/prompts/automate.prompt.md
      command: 'my-ai-tool --system-file {prompt_file} --allowedTools ""'
```

(The prompt-template files are ones you author -- `gtms init` does not scaffold
them.)

Resolution is scoped: `gtms create --adapter foo` looks only in `adapters.create`. If `foo` isn't there: `No adapter 'foo' registered for 'create'. Available adapters: my-ai.`

**Built-in name table fallback:** If resolution finds a name that is not in config but matches the closed set of built-in action adapters (`agent-create`, `manual-create`, `agent-automate`, `manual-automate`, `agent-prime`, `manual-prime`, `agent-execute`, `manual-execute`), the resolver returns a Tier 0 built-in. Config-defined adapters always take precedence -- the built-in table is only consulted after both flag-override and config-default lookups miss. For `prime`, a built-in command default (`manual-prime`) is returned when no flag and no `defaults.prime` config key exist.

### Preset Shipping: Adapters and Their Companion Authoring Artefacts

`gtms init` ships adapters under presets. The three presets are `manual`, `bats`,
and `playwright` (a preset is a workflow bundle -- adapter + framework + scaffold
assets -- not an adapter name). Many adapters have **companion authoring artefacts**
that travel with them -- guides, templates, snippets, READMEs -- written into the
user's project by the same preset.

**Rule of thumb:** if an adapter ships under every preset, its companion artefacts should ship under every preset too. Otherwise users of the "lighter" preset get the adapter with no authoring reference.

The shipped create scripts are `gtms/adapters/manual-create-script.sh` and
`gtms/adapters/agent-create-script.sh`. They ship under all three presets, and
their authoring guide (`gtms/test/guides/gtms-test-case-authoring-guide.md`) travels
with them across every preset so no preset strands the user without a reference for
the TC shape they produce.

**When designing a new adapter for `scaffold.go`:**
1. Decide which presets register the adapter in `gtms.config` (the `if opts.Preset == ...` blocks around the `Write*` calls).
2. Decide which presets write its companion artefacts (guides, templates, snippets).
3. Keep those two answers aligned. Diverging them creates a "shipped without docs" footgun.
4. Cross-check: run `gtms init --preset manual` end-to-end and verify the user gets something coherent.

**Registered-but-dormant is the shipped stance.** Each preset registers a
`manual-<stage>-script` + `agent-<stage>-script` Tier-2 pair on create/prime/execute,
but the `defaults` point at Tier-0 built-ins -- the `manual` preset defaults execute
to `manual-execute`, and `bats`/`playwright` default to their runner. The script
slots ship *registered but not the default* so a lighter preset's user still gets the
scripts and can activate one by making it the `defaults` entry (or via
`--adapter <name>`) without writing any code. This mirrors "guides shipped on all":
both reflect the principle of *don't strand the user of a lighter preset without the
tools to use what they were given*.

---

## Tier 1: Command Template Adapters

A Tier 1 adapter is entirely declarative — a command string with `{variable}` placeholders in `gtms.config`. No code required.

### How It Works

1. GTMS reads the command template from config
2. If `prompt-template` is set, GTMS reads the template file, substitutes context variables, writes the assembled prompt to a temp file (`.gtms/tmp/{task-id}-prompt.md`), sets `{prompt_file}` to the file path, sets `{prompt}` to the content (deprecated), and pipes the content to the process's stdin
3. GTMS substitutes all `{variable}` placeholders in the command template
4. GTMS executes the command via `sh -c`
5. GTMS interprets the exit code: 0 = `complete`, non-zero = `error`. If the adapter declares `fail-exit-codes: [<codes>]`, listed non-zero codes map to `fail` instead (see [Signalling fail from Tier 1](#signalling-fail-from-tier-1-fail-exit-codes))
6. The adapter author never sees or writes to the result contract

### Variable Reference

GTMS substitutes `{variable}` placeholders on two different surfaces, and the two sets are **different by design**: a Tier 1 `command:` string and a `prompt-template` file receive different variables. The two tables below are not meant to agree.

#### Tier 1 command-template variables

These variables are available in **Tier 1 `command:` templates** via `{variable_name}`:

> All variables are always substituted. The "Populated for" column indicates which commands set a meaningful value -- for other commands the variable resolves to an empty string.

| Variable | Description | Size | Populated for |
|----------|-------------|------|---------------|
| `{prompt}` | Fully assembled prompt (template + context) | **Unbounded** | All (if `prompt-template` set) |
| `{reference}` | The `--reference` flag value (e.g. `JIRA-456`, `REQ-123`). This value flows through to the prompt template and typically ends up as the `requirement:` field in generated test case frontmatter -- which is what `gtms map` uses to group test cases by requirement. Choose a stable, human-readable identifier. | Short | create (empty for automate/execute) |
| `{testcase}` | The target argument — ID only (for full content use `{testcase_content}`) | Short | automate, execute |
| `{testcase_content}` | Full content of the test case file | **Unbounded** | automate |
| `{output_dir}` | Output directory path | Short | All |
| `{artefact_file}` | Path to the automation artefact file (resolved from the wiring record's `artefact` field) | Short | execute |
| `{testcase_file}` | Path to the test case markdown file (resolved from `findTestCaseSource`) | Short | automate, execute |
| `{prompt_template}` | Path to prompt template file | Short | All (if set) |
| `{branch}` | Git branch associated with this task. For sync adapters: the current project branch at invocation time (empty if not in a git repo or on detached HEAD). For async adapters: a constructed task branch (`feature/{command}-{target}`). | Short | All |
| `{repo}` | Repository identifier from config | Short | All |
| `{task_id}` | Generated task ID (e.g. `task-a3f72b1`) | Short | All |
| `{result_file}` | Path to the result contract file | Short | All |
| `{project_root}` | Absolute path to project root | Short | All |
| `{work_dir}` | Project-root (worktree) base handed to the adapter for reference. NOT the run cwd -- set `working-dir` for that. | Short | All |
| `{focus}` | `--focus` flag value | Short | create |
| `{context}` | Content of `--context-file` flag file | **Unbounded** | create, automate |
| `{context_file}` | Absolute path to file specified by `--context-file` flag | Short | create, automate |
| `{guides}` | Concatenated content of all `.md` files from `guide-dir` | **Unbounded** | create, automate (if `guide-dir` set) |
| `{prompt_file}` | Path to assembled prompt temp file (`.gtms/tmp/{task-id}-prompt.md`) | Short | All (if `prompt-template` set) |
| `{environment}` | `--env` flag value (target environment) | Short | automate, execute |
| `{output_subdir}` | Test case's subfolder under `gtms/test/cases/` (e.g. `cwd-scoping/`). Includes trailing `/` when non-empty, empty string for root-level test cases. Available in prompt templates for informational use. **Do not use to prefix `<gtms-file>` filenames** — GTMS automatically routes streamed files to the correct subdirectory (see [Subdirectory Routing](#subdirectory-routing)). | Short | automate, execute (empty for create) |
| `{tc_ids}` | Comma-separated list of 20 pre-generated test case IDs in `tc-{8hex}` format. Use these IDs for generated test case files so they follow the GTMS naming convention. | Short | create |
| `{tc_name}` | Optional name from the second positional argument (e.g. `gtms create login user-can-login` → `user-can-login`). Empty if not provided. | Short | create |

> **Size column:** "Short" variables are IDs, paths, or flags -- typically under 200 characters. "**Unbounded**" variables contain file content that can grow to thousands of lines. This matters for prompt template ordering -- see [Prompt Template Authoring](#prompt-template-authoring).

#### Prompt-template placeholders

A `prompt-template` file (any tier) is assembled from a **different, smaller set** of 16 placeholders before it is delivered to the adapter. This is not the command-template set above: `{prompt}`, `{prompt_file}`, `{prompt_template}`, `{task_id}`, `{work_dir}`, `{repo}`, `{project_root}`, and `{result_file}` are **command-only** (several are self-referential and could never resolve inside a prompt), while `{framework}` is **prompt-only**. Do not assume a variable from the table above works in a prompt template.

| Placeholder | Description | Size |
|-------------|-------------|------|
| `{artefact_file}` | Path to the automation artefact file. | Short |
| `{branch}` | Git branch associated with the task. | Short |
| `{context}` | Content of the `--context-file` flag file. | **Unbounded** |
| `{context_file}` | Absolute path to the `--context-file` file. | Short |
| `{environment}` | `--env` flag value (target environment). | Short |
| `{focus}` | `--focus` flag value. | Short |
| `{framework}` | Target framework name, from the `--framework` flag or the adapter's `framework` config. **Prompt-only** -- not a Tier 1 command-template variable. | Short |
| `{guides}` | Concatenated `.md` guide files from `guide-dir`, each XML-wrapped. | **Unbounded** |
| `{output_dir}` | Output directory path. | Short |
| `{output_subdir}` | Test case's subfolder under `gtms/test/cases/` (trailing `/` when non-empty). Informational only -- do not use it to prefix `<gtms-file>` filenames. | Short |
| `{reference}` | The `--reference` flag value (create). | Short |
| `{tc_ids}` | Comma-separated list of pre-generated `tc-{8hex}` IDs (create). | Short |
| `{tc_name}` | Optional `[name]` positional argument (create). The one placeholder that spans both surfaces -- see [`{tc_name}` and AI-Adapter Semantics](#tc_name-and-ai-adapter-semantics) for the two-surface rule; do not restate it inline. | Short |
| `{testcase}` | Target test case ID only (use `{testcase_content}` for the body). | Short |
| `{testcase_content}` | Full content of the test case file. | **Unbounded** |
| `{testcase_file}` | Path to the test case markdown file. | Short |

> These 16 keys are the authoritative `promptVars` set (`internal/adapter/invoker.go`). The "Short vs Unbounded" split matters for template ordering -- see [Prompt Template Authoring](#prompt-template-authoring).

#### Injected values are data, not templates

Substitution runs once, left to right, over the template only. A `{variable}` in the `command:` string or the `prompt-template` file is resolved from the tables above; brace-delimited text that arrives *inside* an injected value is not.

The unbounded variables carry file content: `{context}` (the `--context-file` body), `{guides}` (the `guide-dir` files), `{testcase_content}` (the test case body), and Tier 1's `{prompt}` (the fully assembled prompt). If that content happens to contain text that looks like a GTMS placeholder -- a requirements document that mentions `{context}`, `{framework}`, or `{tc_name}` -- the text is emitted verbatim and is never substituted. The template is a template; injected values are data.

Assembly is also deterministic: the same template and the same inputs always produce the same assembled prompt.

Worked example -- a prompt template:

```
NAME=[{tc_name}]
CTX=[{context}]
```

run as `gtms create login user-can-login --context-file req.md`, where `req.md` contains the line `See {tc_name} in the spec.`, assembles to:

```
NAME=[user-can-login]
CTX=[See {tc_name} in the spec.]
```

The template's own `{tc_name}` is substituted; the `{tc_name}` that arrived inside the context body is left untouched.

### Best For

- Single-command tool invocations
- Any adapter expressible as one shell command with variable substitution

### Limitations

- No conditional logic or multi-step processes
- No way to update the result contract (GTMS infers everything from exit code)
- All substituted values are shell-escaped before insertion
- **`{prompt}`, `{context}`, and `{guides}` caution:** These variables inline content as command-line arguments. Very long values hit OS limits (~32K on Windows). Use `{prompt_file}` (file path) instead -- it works at any size.

### `{tc_name}` and AI-Adapter Semantics

The `{tc_name}` variable carries the optional `[name]` argument from `gtms create <folder> [name]`. It follows **Option A (conditional single-case)** semantics:

- **When `{tc_name}` is non-empty**: the AI must generate **exactly one** test case. Use the first ID from `{tc_ids}` and name the file `<first-id>-{tc_name}.md` (e.g. `tc-a3f72b10-user-can-login.md`). Set the `title:` frontmatter field to a human-readable form of the name. Do not generate additional test cases.
- **When `{tc_name}` is empty** (default, no second positional arg): preserve the standard multi-case behaviour. Generate one test case per distinct behaviour, using IDs from `{tc_ids}` in order with AI-chosen slugs.

**Two-surface rule for Claude Code adapters:** Claude Code uses `--append-system-prompt-file` for task context and `-p` for output format instructions. The `{tc_name}` variable and its conditional rule must appear in **both** surfaces:

1. **System prompt** (the `prompt-template` file, delivered via `{prompt_file}`) — carries the Option A conditional rule as part of `<output_rules>`.
2. **Command string** (the `command:` field in `gtms.config`, the `-p` user message) — carries the same conditional rule so the AI knows what filename shape to emit.

Threading `{tc_name}` into only one surface has no effect because Claude Code treats them as separate instruction channels.

**Frontmatter field convention:** AI adapters write `title:` (human-readable), not `name:` (slug). The direct-write create adapter writes `name:` because the user supplies a slug directly. The GTMS reader (`readTCFrontmatter`) prefers `title:` and falls back to `name:`, so both conventions produce correct headline output.

**Slug validation:** The CLI enforces strict validation (`^[a-zA-Z0-9_-]+$`) on the `[name]` argument before the adapter ever sees it. Invalid names (with spaces, shell metacharacters, slashes) are rejected with an actionable error message. Adapters do not need to validate or normalise the name.

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

**Local AI code generator (recommended -- uses prompt file):**
```yaml
prompt-template: prompts/create.md
command: 'claude -p "Read the system prompt instructions. Create test cases from the source material. Output each test case using <gtms-file name=\"tc-<8-char-hex>-<short-slug>.md\"> tags, closed with </gtms-file>. No code fences. Raw text only." --append-system-prompt-file {prompt_file} --allowedTools ""'
```

> **Note:** The `-p` message must contain specific task and output format instructions -- not a generic "execute the system prompt" directive. `--allowedTools ""` prevents the model from using tools and ensures raw text output.

> **Note on the shape above:** the filename pattern is written as literal placeholder text
> (`<8-char-hex>`), not as a `{...}` token. A `{...}` token here would be read as a GTMS
> template variable, and since it is not a recognised one, GTMS would warn about it at
> invocation. Note also that `{prompt_file}` sits **outside** the quoted prose -- bare, as
> required. The quoted prose itself contains no placeholder, which is why this command is
> correct.

**Local AI code generator (stdin -- caution):**
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
command: 'npx playwright test {artefact_file} --reporter=junit'
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

> **Shell invocation:** GTMS runs scripts as `sh <path>` (`internal/adapter/tier2.go:90`), not by executing them directly. The shebang line (`#!/usr/bin/env bash`) and the executable bit are both **ignored** -- `chmod +x` does nothing, and the script is always interpreted by whichever `sh` is on PATH. On MINGW (Windows) and macOS, `sh` is typically bash or bash-compatible, so bashisms silently work. **On Ubuntu/Debian/Alpine CI runners `sh` is dash**, and bashisms fail -- commonly with `Syntax error: redirection unexpected` or `Syntax error: "(" unexpected`. Author Tier 2 scripts (including BATS-fixture mocks that use `script:`) in POSIX sh only.
>
> Bashisms that break on dash: `<<<` herestrings, `read -ra`, `${arr[0]}` / `${arr[$i]}` array indexing, `[[ ... ]]` double-brackets, `<( … )` process substitution, `${var:0:3}` / `${var/pat/rep}` parameter expansion, `&>` / `|&` redirections. POSIX replacements: `cut -d, -fN` or `IFS=,; set -- $var; IFS=$OLDIFS` then positional `$1`/`$2`/… for array-style splits; `case` for pattern matching; standard `[ … ]` tests. `$(( … ))` arithmetic IS POSIX and safe to keep.
>
> **Debugging tip:** a Linux-only failure with `Process exited with code 2: …: N: Syntax error` (where `N` is dash's line number) is almost always a bashism in a Tier 2 script. Grep the offending script for `<<<`, `${.*\[`, `read -r?a`, `[[`.

### Cross-Shell Wrapper Pattern

When the test framework requires a different runtime (PowerShell, Python, Node), the Tier 2 script acts as a thin `.sh` wrapper that delegates to the real runtime. This is the standard pattern for any non-bash framework:

```bash
#!/bin/bash
set -e

# 1. Resolve the runtime — prefer the modern version, fall back to the legacy one
RUNTIME=""
if command -v pwsh >/dev/null 2>&1; then
    RUNTIME="pwsh"
elif command -v powershell >/dev/null 2>&1; then
    RUNTIME="powershell"
fi

if [ -z "${RUNTIME}" ]; then
    cat > "${GTMS_RESULT_FILE}" <<EOF
task: ${GTMS_TASK_ID}
command: execute
target: ${GTMS_TESTCASE}
adapter: pester-runner
mode: sync
status: error
summary: "No PowerShell found on PATH. Install pwsh (7+) or ensure powershell (5.1) is available"
completed: $(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF
    exit 1
fi

# 2. Invoke the resolved runtime, capture output
OUTPUT=$(${RUNTIME} -NoProfile -Command "Invoke-Pester -Path '${GTMS_ARTEFACT_FILE}' -CI" 2>&1) || EXIT=$?
EXIT=${EXIT:-0}

# 3. Parse output and write result contract (see Parsing Test Framework Output below)
```

The same pattern applies to other runtimes:
- **Python:** `python3` → `python` fallback
- **Node:** `node` (usually no fallback needed, but check `npx` availability)
- **Go test:** `go` (check `GOPATH` is set if needed)

**Two things to get right in step 1:**

1. **Try multiple binary names.** Runtimes often have version-specific names (`python3`/`python`, `pwsh`/`powershell`, `node`/`nodejs`). A user may have the older or newer version — try both and use whichever is found.

2. **Report a helpful error with installation instructions.** When neither binary is found, write `status: error` to the result contract with a message that tells the user exactly what to install. "command not found" from a raw shell error is unhelpful — the user sees it in `gtms status` with no context.

### Environment Variable Reference

These variables are exported to Tier 2 scripts:

> Every variable in this table is always exported (empty string when not meaningful for the command), with one exception: `GTMS_FORCE` is exported only when `--force` was passed. The "Populated for" column indicates which commands set a meaningful value.

| Variable | Description | Size | Populated for |
|----------|-------------|------|---------------|
| `GTMS_TASK_ID` | Generated task ID (e.g. `task-a3f72b1`) | Short | All |
| `GTMS_COMMAND` | Command that triggered the adapter (e.g. `create`) | Short | All |
| `GTMS_REFERENCE` | The `--reference` flag value (e.g. `JIRA-456`, `REQ-123`). This value flows through to the prompt template and typically ends up as the `requirement:` field in generated test case frontmatter -- which is what `gtms map` uses to group test cases. Choose a stable, human-readable identifier. | Short | create (empty for automate/execute -- use `GTMS_TESTCASE` instead) |
| `GTMS_TESTCASE` | The target argument — ID only (for full content use `GTMS_TESTCASE_CONTENT`) | Short | automate, execute |
| `GTMS_TESTCASE_CONTENT` | Full content of the test case file | **Unbounded** | automate |
| `GTMS_OUTPUT_DIR` | Output directory path | Short | All |
| `GTMS_ARTEFACT_FILE` | Path to the automation artefact file (resolved from the wiring record's `artefact` field) | Short | execute |
| `GTMS_TESTCASE_FILE` | Path to the test case markdown file (resolved from `findTestCaseSource`) | Short | automate, execute |
| `GTMS_PROMPT_TEMPLATE` | Path to prompt template file | Short | All (if set) |
| `GTMS_BRANCH` | Git branch associated with this task. For sync adapters: the current project branch (empty if not in a git repo or on detached HEAD). For async adapters: a constructed task branch (`feature/{command}-{target}`). | Short | All |
| `GTMS_REPO` | Repository identifier from config | Short | All |
| `GTMS_PROJECT_ROOT` | Absolute path to project root | Short | All |
| `GTMS_WORK_DIR` | The task's working-directory base handed to the adapter for reference -- the worktree path when the command created one, else the project root. NOT the process cwd (that is set from the resolved run directory: project root, or the `working-dir` subdir). Distinct from `GTMS_PROJECT_ROOT`. | Short | All |
| `GTMS_RESULT_FILE` | Path to the result contract YAML file | Short | All |
| `GTMS_FOCUS` | `--focus` flag value | Short | create |
| `GTMS_CONTEXT` | Content of `--context-file` flag file | **Unbounded** | create, automate |
| `GTMS_CONTEXT_FILE` | Absolute path to file specified by `--context-file` flag | Short | create, automate |
| `GTMS_GUIDES` | Concatenated content of all `.md` files from `guide-dir` | **Unbounded** | create, automate (if `guide-dir` set) |
| `GTMS_PROMPT_FILE` | Path to assembled prompt temp file (`.gtms/tmp/{task-id}-prompt.md`) | Short | All (if `prompt-template` set) |
| `GTMS_ENVIRONMENT` | `--env` flag value (target environment) | Short | automate, execute |
| `GTMS_OUTPUT_SUBDIR` | Test case's subfolder under `gtms/test/cases/` (e.g. `cwd-scoping/`). Includes trailing `/` when non-empty, empty string for root-level test cases. | Short | automate, execute (empty for create) |
| `GTMS_TC_IDS` | Comma-separated list of 20 pre-generated test case IDs in `tc-{8hex}` format. Use these IDs for generated test case files so they follow the GTMS naming convention. | Short | create |
| `GTMS_TC_NAME` | Optional name from the second positional argument (e.g. `gtms create login user-can-login` → `user-can-login`). Empty if not provided. | Short | create |
| `GTMS_RESULT_TEMPLATE` | Path to the manual result template file. Manual `prime` adapters stamp this template into the manual records dir; manual `execute` adapters use it as the source-of-truth schema reference for validation. | Short | prime, execute (manual-execute adapter only) |
| `GTMS_RESULT_VALUE` | Pre-parsed `result:` field value from the user-edited manual result YAML (`pass` / `fail` / `skip`, or empty if the user hasn't filled it in). Validated by GTMS before the script sees it; the script just needs to read this and write it to the handoff contract. | Short | execute (manual-execute adapter only) |
| `GTMS_RESULT_TESTCASE` | Pre-parsed `test_case_id:` field from the manual result YAML (echoed back to confirm the file matches the target TC). | Short | execute (manual-execute adapter only) |
| `GTMS_RESULT_TESTCASE_HASH` | Pre-parsed `test_case_hash:` field — SHA-256 of the test case spec at prime time. The adapter compares it to the current spec hash to surface drift. | Short | execute (manual-execute adapter only) |
| `GTMS_TC_TITLE` | TC frontmatter `title:` snapshot, copied at prime time for self-contained review. | Short | prime (manual-prime adapter) |
| `GTMS_TC_REQUIREMENT` | TC frontmatter `requirement:` snapshot, copied at prime time. | Short | prime (manual-prime adapter) |
| `GTMS_TC_PRIORITY` | TC frontmatter `priority:` snapshot, copied at prime time. | Short | prime (manual-prime adapter) |
| `GTMS_TC_TYPE` | TC frontmatter `type:` snapshot, copied at prime time. | Short | prime (manual-prime adapter) |
| `GTMS_RESULT_FRAMEWORK` | Pre-parsed `framework:` field from the manual result YAML (always `manual` for the shipped adapter; reserved for future framework-tagged manual records). | Short | execute (manual-execute adapter only) |
| `GTMS_TESTCASE_HASH` | 16-hex (truncated SHA-256) of the test case spec content, computed by GTMS at prime time and stamped into the manual result template. Distinct from `GTMS_RESULT_TESTCASE_HASH` (which is the value parsed back out of the filled manual result file). | Short | prime (manual-prime adapter) |
| `GTMS_TEMPLATE_FILE` | Path to the role-specific template GTMS hands the adapter: a testcase template under `gtms/test/templates/` for create, a result template for prime. The shipped scripts read it and fall back to a built-in heredoc when unset. | Short | create, prime |
| `GTMS_OUTPUT_FILE` | Path for the single stamped output file the prime adapter writes. | Short | prime (manual-prime adapter) |
| `GTMS_FORCE` | Set to `true` only when `--force` was passed; unset otherwise. Manual-prime consults it to permit overwriting an existing result file. | Short | prime, automate |

> **Large values:** `GTMS_CONTEXT` and `GTMS_GUIDES` contain full file content as environment variables. Linux supports ~128KB, Windows ~32KB. For very large content, use `$GTMS_PROMPT_FILE` (which contains the fully assembled prompt including context and guides) or read files directly (`$GTMS_CONTEXT_FILE` for context, or the guide directory).

> **Note:** GTMS now assembles the prompt for Tier 2 adapters when `prompt-template` is configured. The assembled prompt file is available at `$GTMS_PROMPT_FILE`. The raw template path is still available via `$GTMS_PROMPT_TEMPLATE` for scripts that need custom assembly.

> **Note:** `GTMS_WORK_DIR` carries the task's working-directory base (the worktree path when the command created one, else the project root) and is distinct from `GTMS_PROJECT_ROOT`. It is not the process cwd -- the cwd is set independently from the resolved run directory (the project root, or the configured `working-dir` subdir). To run an adapter from a subdirectory, set `working-dir` on the adapter (see the Config Fields table); do not rely on `GTMS_WORK_DIR` for that.

> **Security note:** Tier 2 scripts receive only a minimal allowlist of system variables (`PATH`, `HOME`, `TMPDIR`, `USER`, `SHELL`, `LANG`, `LC_ALL` + Windows vars) plus all `GTMS_*` variables. Parent process secrets are no longer inherited. See [Security Considerations](#tier-2-environment-isolation) for details.

### Output Directory by Command

GTMS sets `GTMS_OUTPUT_DIR` (and `{output_dir}`) based on the command. Each adapter can override the default with the `output-dir` config field:

| Command | Default output directory | Override with |
|---------|-------------------------|---------------|
| `create` | `{project_root}/gtms/test/cases` | `output-dir` on create adapter |
| `automate` (command/script adapter) | `{project_root}/gtms/automation/specs/{adapter-name}/` | `output-dir` on automate adapter |
| `automate` (built-in `agent-automate`/`manual-automate`) | framework-native: `test/acceptance/` for BATS, `gtms/scripts/playwright/` for Playwright | `output-dir` on the automate adapter |
| `execute` | `{project_root}/results` | `output-dir` on execute adapter |

The built-in automate adapters honour an explicit `output-dir` the same way Tier 1/2 adapters do; the framework-native default applies only when `output-dir` is unset.

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
result: pass
artefact: path/to/output/file.md
attempts: 1
summary: "Generated 3 test cases"
completed: $(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF
```

> **Note:** `status: complete` requires a `result:` field. For a create/automate adapter that completed its stage, write `result: pass`; the orthogonal `result:` axis carries the outcome while `status:` carries adapter execution state. The `created` field is the timestamp when GTMS originally invoked the adapter. If you overwrite the full file, carry this value through rather than replacing it. The `completed` field is the one your script should set to the current time.

If your script doesn't update the result contract, GTMS falls back to exit code handling (exit 0 = complete, non-zero = error). This means simple scripts that just need pass/fail don't need to touch the result contract at all.

### How GTMS Discovers Tier 2 Output Files

Tier 2 create / automate adapters can produce output two ways. **Both are supported and either pattern is valid:**

1. **Streaming**: emit `<gtms-file name="...">...</gtms-file>` markers to stdout. GTMS's streaming parser captures each block to disk under `$GTMS_OUTPUT_DIR` and tracks the resulting paths internally as it goes. This is the pattern AI adapters typically use because the LLM produces text token-by-token.

2. **Direct-write**: write the file(s) directly to `$GTMS_OUTPUT_DIR` from inside the script (e.g. `cat > "$GTMS_OUTPUT_DIR/$ID-$NAME.md" <<EOF...EOF`) and update the contract with `status: complete` + `result: pass`. The shipped `manual-create-script.sh` / `agent-create-script.sh` and most "I have a non-AI tool that produces files" adapters use this pattern.

GTMS unifies both patterns when reporting back to the CLI:

- If the streaming parser captured files (pattern 1), those paths populate `InvokeResult.ArtifactPaths` and the `gtms create` headline lists each TC by ID + title.
- If streaming captured nothing AND `$GTMS_OUTPUT_DIR` is set (pattern 2), GTMS scans the output directory for files that weren't there pre-invocation and uses those instead. Same headline rendering, same downstream pipeline behaviour.

**You don't need to mix the two.** Pick whichever fits your tool. Direct-write is generally simpler when the underlying tool already writes files; streaming is the natural fit when the adapter is producing output text it needs to chunk.

**You don't need to put the file path in `artefact:`.** The contract's `artefact:` field is informational — GTMS discovers files via streaming or scanOutputDir, not by parsing `artefact:`. Setting it is fine and recommended for downstream traceability, but absolute Windows paths like `C:\Users\...\file.md` will fail YAML parsing if unquoted; prefer relative paths or omit the field.

### Best For

- Multi-step workflows
- Adapters that call multiple tools
- GitHub-based workflows using `gh` CLI
- Anything with conditional logic

### Examples

**Sync script — local tool with result reporting:**

```bash
#!/bin/bash
# gtms/adapters/my-create.sh
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
result: pass
artefact: ${GTMS_OUTPUT_DIR}
attempts: 1
summary: "Test cases generated for ${GTMS_REFERENCE}"
completed: $(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF
```

**Async script — trigger remote work:**

```bash
#!/bin/bash
# gtms/adapters/github-execute.sh
set -e

# Trigger the workflow
gh workflow run test-runner.yml \
  --ref main \
  -f test="${GTMS_TESTCASE}" \
  -f spec="${GTMS_ARTEFACT_FILE}"

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
# gtms/adapters/github-execute-status.sh
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

## Manual-execute Adapter

The `manual-execute` adapter has two implementations: a **Tier 0 Go built-in** and a **Tier 2 shell script** (`gtms/adapters/manual-execute-script.sh`, registered under the name `manual-execute-script`) that ships with `gtms init`. Both round-trip a user-edited manual result file (`gtms/manual/records/{tc-id}--manual.result.yaml`) into the standard pipeline. When no config entry exists for the name `manual-execute`, the resolver returns the Tier 0 built-in; when a config entry with `script:` exists, the Tier 2 script takes precedence. Pair with `gtms prime` which stamps the file from a template.

**Lifecycle:** `gtms prime tc-X --framework manual` -> user edits the YAML -> `gtms execute tc-X --adapter manual-execute` -> `manual-execute` adapter validates, detects drift, writes the orthogonal handoff (`status: complete + result: pass|fail|skip`).

**Where it's wired:**

- On the **`manual` preset**, the execute default IS the Tier-0 `manual-execute` built-in (`defaults.execute: manual-execute`), so bare `gtms execute tc-X` records a manual result. The `manual-execute-script` + `agent-execute-script` Tier-2 pair is registered but dormant.
- On the **`bats` and `playwright` presets**, the execute default is the runner (`bats-runner` / `playwright-runner`), and the same `manual-execute-script` + `agent-execute-script` pair ships registered-but-dormant. To record a manual result there, invoke it per call: `gtms execute tc-X --adapter manual-execute` (or make one of the script slots the default).
- *(History note: the legacy `gtms execute --result pass|fail|skip` and `--notes` flags were removed along with the `pipeline.WriteManualResult` / `pipeline.RecordManualResult` direct writers. Manual outcome recording now flows through this adapter only -- the CLI no longer mutates a record for a manual outcome. A Go source-shape guard (`TestSourceShape_NoLegacyManualBypass`) locks the deletions in.)*

**Validation contract (Go-side, before the script runs):** GTMS parses the result YAML with `yaml.v3` and rejects malformed files with field-named errors:

- Missing required field (`test_case_id`, `test_case_hash`, `framework`, `result`) → names the field that's missing
- Invalid `result:` value (anything other than `pass` / `fail` / `skip` or empty) → names the value that was typed
- Malformed `test_case_hash` (not a 16-char hex string) → rejects with format error
- Missing result file at the expected path → directs the user to `gtms prime tc-X --framework manual`

Validation errors flow through the standard error path: `status: error` handoff, task moves to `gtms/tasks/error/`, pipeline records built — same lifecycle any other adapter uses.

**Drift diagnostic:** the script SHA-256s the test case spec at execute time and compares against the `test_case_hash:` baked in at prime time. If they diverge, an idempotent diagnostic block is appended to the result file (it's not rejected — drift may be intentional). The block is removed cleanly on the next prime if the user re-stamps.

### Adapter-First Detection Rule

The CLI decides "is this the manual-execute path?" via a single helper:

```go
// internal/adapter/framework.go
func IsManualFramework(resolved *ResolvedAdapter) bool {
    if resolved == nil {
        return false
    }
    if resolved.Name == "manual-execute" || resolved.Name == "agent-execute" {
        return true
    }
    if resolved.Config != nil && resolved.Config.Framework == "manual" {
        return true
    }
    return false
}
```

(`agent-execute` shares the `manual-execute` implementation on day one, so both names resolve as the manual path.)

The signature deliberately takes nothing else. Framework strings on the CLI flag (`--framework manual`) and the on-disk wiring record's `framework:` field cannot stand alone -- the predicate physically cannot be flipped by them. This is the **adapter first, framework second** rule.

**Why it matters for adapter authors:** when you build an adapter that needs CLI-side branching (e.g. "skip the generic artefact pre-check for my adapter's missing-file path"), the dispatch decision must key on the resolved adapter, not on framework metadata. The concrete failure case the rule defends against:

```
# bats or playwright preset, whose default execute adapter is bats-runner /
# playwright-runner (framework not manual):
gtms execute tc-X --framework manual
```

The flag yields a `"manual"` framework string, but resolution still returns the preset's non-manual default. A loose dispatch rule that trusted the flag would skip the pre-check and invoke a non-manual adapter with no artefact validation.

**Where the predicate is shared:** `internal/cli/execute.go` (artefact pre-check deferral, single-TC and bulk paths) and `internal/adapter/invoker.go` `buildAdapterContext` (manual context population). Both call sites use the same predicate so they stay symmetric -- a single source of truth for "is this the manual adapter path?". When you add a new adapter that needs the same kind of CLI-side dispatch, follow the same pattern: one predicate, all call sites share it.

A third call site was added without weakening the rule -- `cli/execute.go::shouldSkipExecute` now takes `resolved *adapter.ResolvedAdapter` and short-circuits the "already passing" optimisation when `adapter.IsManualFramework(resolved)` is true. Bulk execute on a non-manual adapter skips a TC only when it has a genuine prior EXECUTE-command `result: pass` against the current artefact; an `automate`/`prime`/`create` success is never treated as a test pass. A record with on-disk `framework: manual` that resolves through a non-manual adapter still skips -- the predicate physically cannot be flipped by record metadata.

### Manual-Prime Env-Var Contract

Like `manual-execute`, the `manual-prime` adapter has a **Tier 0 Go built-in** alongside its Tier 2 shell script (`gtms/adapters/manual-prime-script.sh`, registered under the name `manual-prime-script`). The Tier 0 built-in is used when no config entry exists; a config entry with `script:` activates the Tier 2 script. The `prime` command also has a built-in command default -- when no `--adapter` flag and no `defaults.prime` config key exist, the resolver returns `manual-prime` automatically.

The Tier 2 `manual-prime` adapter receives these environment variables when invoked via `gtms prime`:

| Variable | Description | Lifecycle | Command |
|----------|-------------|-----------|---------|
| `GTMS_TASK_ID` | Unique task identifier | Short | prime |
| `GTMS_COMMAND` | Always `prime` (prime is its own adapter bucket) | Short | prime |
| `GTMS_TESTCASE` | Target test case ID (e.g. `tc-a1b2c3d4`) | Short | prime |
| `GTMS_TESTCASE_FILE` | Absolute path to the test case spec file | Short | prime |
| `GTMS_TESTCASE_HASH` | 16-hex (truncated SHA-256) of the test case spec, for drift detection | Short | prime |
| `GTMS_TEMPLATE_FILE` | Path to the manual result template file | Short | prime |
| `GTMS_OUTPUT_FILE` | Path where the stamped result file should be written | Short | prime |
| `GTMS_BRANCH` | Current git branch name | Short | prime |
| `GTMS_FORCE` | `true` if `--force` was passed, empty otherwise | Short | prime |
| `GTMS_RESULT_FILE` | Path to the handoff contract (`.gtms/results/{task-id}.handoff.yaml`) | Short | prime |

### Result-Contract Output Shape for Manual Adapters

**Manual prime (`manual-prime` adapter):**

The prime task stamps a template and creates an automation record. On success, the handoff contract is:
- `status: complete` + `result: pass` — the prime task itself succeeded (this is NOT a test outcome; it records that the template was stamped)

**Manual execute (`manual-execute` adapter):**

Clean runs where the user-edited result file passes validation:
- `status: complete` + `result: pass | fail | skip` — the value is copied from the `result:` field in the user-authored YAML file

Validation or runtime errors:
- `status: error` with `result:` empty or omitted — frontmatter validation failed, result file missing, or adapter runtime error

The shipped manual-execute path does NOT write `status: complete + result: error`. The user-file vocabulary forbids `error` on the input side, so the only route to `result: error` on a handoff is through a non-manual adapter.

### VSCode Companion-File Behaviour

`gtms init` scaffolds `.vscode/settings.json` (yaml.schemas mapping) and `.vscode/extensions.json` (Red Hat YAML extension recommendation). When either file already exists (the common case for non-greenfield repos):

1. The existing file is **not touched** — no merge, no overwrite.
2. A companion snippet file is written alongside it: `.vscode/gtms-settings.json.snippet` or `.vscode/gtms-extensions.json.snippet`.
3. The init output emits a warning telling the user to merge the snippet content into their existing file.

The snippet library at `.vscode/gtms.code-snippets` is always written (skip-if-exists) regardless of whether the settings/extensions files are fresh or pre-existing.

### Result Vocabulary Cross-Reference

Four distinct result vocabularies exist across the pipeline. Each applies to a specific artefact:

| Artefact | Field | Vocabulary | Who writes |
|----------|-------|------------|------------|
| User-authored manual result file | `result:` | `pass \| fail \| skip` | Tester |
| Handoff contract | `status:` + `result:` | `status: pending \| in-progress \| complete \| error`; `result: pass \| fail \| skip \| error` | Adapter |
| Per-test execution YAML | `outcome:` | `pass \| fail \| skip \| error` | Pipeline |
| Automation record | `result:` | `pass \| fail \| skipped` | Pipeline |

The manual result file never carries `error`. The automation record maps `skip` to `skipped` (past tense) at the contract-to-record boundary. Adapter authors must write the handoff vocabulary, not the record vocabulary.

### Fixture-Authoring Lessons from the Legacy `--result` Migration

Migrating ~35 BATS fixtures off the legacy `--result` setup-shortcut surfaced a class of pitfalls. These apply whenever you're writing a fixture that exercises the manual pipeline or any test asserting on pipeline-written fields.

- **Direct file seeds do NOT substitute for pipeline writes when the assertion is about a pipeline-written field.** A fixture that writes an automation record by hand (`cat > gtms/automation/records/tc-X--manual.automation.md`) can verify reader behaviour (`gtms status`, `gtms gaps`, `gtms map` against a known-shape record), but cannot verify writer behaviour. If the test asserts on a pipeline-written field, the fixture must drive the full pipeline: `gtms init` → `gtms create` → `gtms prime --framework manual` → `perl -i -pe 's/^result:.*$/result: pass/' "gtms/manual/records/${TC_ID}--manual.result.yaml"` → `gtms execute "$TC_ID" --adapter manual-execute`. Reference fixtures: `test/acceptance/legacy-manual-bypass-removal/tc-0fab489d` (manual_coverage), `tc-1a084ff8` (handoff `completed:`), `tc-c5d6e7f8` (environment from `--env`). The `executed_at:` seed pattern verifies READ tolerance only; the writer assertion now reads from `.gtms/results/<task>.handoff.yaml` `completed:` because `pipeline.UpdateExecutionResult` no longer runs on the execute path after the wiring cutover.
- **Legacy record shapes don't round-trip as `manual_coverage: recorded`.** Direct seeds carrying the legacy bypass-writer shape (`status: accepted`, `adapter: manual`, no `executed_at:`) are recognised by the reader as a manual record but classified as `manual_coverage: prepared`, NOT `recorded`. If a fixture needs `recorded`, either use the full pipeline (above) or seed the current shape: `status: complete`, `adapter: manual-execute`, `executed_at: <RFC3339>`.
- **`selectAutomationRecord` preference order matters in fixtures.** Seeding both a bats automation record (with `artefact:` set) AND a manual record for the same TC lets the reader pick the bats one; the manual record's `result:` is masked. Either drop the bats stub or use the canonical pipeline.
- **Pure bypass-feature tests have no forward analogue.** A test whose entire product assertion was that `--result` works (e.g. "`gtms execute --result skip` is accepted", "`gtms execute --result unknown` is rejected", "`gtms execute --result pass` overrides skip on a BATS spec") has no equivalent after the bypass removal. Delete it, including the matching spec under `gtms/test/cases/`. Replacement coverage of the post-removal state lives in `test/acceptance/legacy-manual-bypass-removal/`.
- **Lock deletions in with a Go source-shape guard.** When a feature-removal slice deletes named CLI flags or exported functions, add an `internal/cli/source_shape_test.go` (or sibling) that reads the relevant source files via `os.ReadFile` and asserts the symbols are absent. This is a Go test (stays inside the BATS-boundary rule), runs in the standard suite, and catches re-introduction during refactors. Reference: `TestSourceShape_NoLegacyManualBypass` covers `runManualResult`, `WriteManualResult`, `RecordManualResult`.
- **Adapter-first dispatch survived the bypass removal unchanged.** `adapter.IsManualFramework(resolved)` keys on the resolved adapter, not on CLI flags or on-disk framework strings — removing the `--result` bypass did not require any change to the predicate. This is the lesson: when designing manual-aware behaviour, key it on the resolved adapter, never on flag presence or record metadata. Removing a flag then becomes a deletion of CLI surface, not a re-think of dispatch.

### Lessons: TC frontmatter snapshot at prime time

Four `GTMS_TC_*` env vars (`TITLE`, `REQUIREMENT`, `PRIORITY`, `TYPE`) are sourced from the TC frontmatter and stamped into the manual result file at prime time. The work surfaced two patterns worth promoting for any future adapter that copies free-form metadata onto user-edited disk artefacts.

- **`AdapterContext` + Tier 2 env-var allowlist must be kept in sync.** Adding a field to `internal/adapter/types.go` `AdapterContext` is necessary but **not sufficient** — `internal/adapter/tier2.go` has an explicit `env = append(env, ...)` allowlist that controls which fields actually reach the Tier 2 script (`minimalEnv()` strips everything else; see the [security note](#tier-2-environment-isolation)). Forget the matching `env = append` line and the value silently arrives as the empty string at the script. **Rule:** every new `AdapterContext` field intended for Tier 2 consumption needs a paired `env = append(env, "GTMS_FIELD=" + ctx.Field)` line in the same commit. The two cases must be tested together (a Go unit test that builds the context AND a BATS test that asserts the env var reaches the script).

- **Free-form values reaching YAML need explicit double-quoted-scalar escaping — and that includes embedded newlines.** Sed-only escaping (`\` and `"`) is insufficient when the source value can contain colon-space, `#`, quotes, backslashes, or — the easy-to-miss case — multiline scalars (legal YAML frontmatter via `|` literal or `>` folded). A multi-line value will split the sed substitution across lines and either break sed or stamp invalid YAML. The shipped pattern in `manualPrimeScript` uses an awk one-liner that handles all of these in a single pass and collapses the value to a single physical line:

  ```sh
  yaml_escape() {
    printf '%s' "$1" | awk '
      {
        gsub(/\\/, "\\\\")    # backslash first
        gsub(/"/,  "\\\"")    # then double-quote
        gsub(/\t/, "\\t")
        gsub(/\r/, "\\r")
        if (NR > 1) printf "\\n"
        printf "%s", $0
      }
    '
  }
  ```

  Run this BEFORE the existing `escape_sed` (`\`, `&`, `|`) so the YAML escapes survive sed insertion. The template emits `field: "${VAR}"` (double-quoted form); the awk + sed pipeline produces a valid double-quoted scalar on a single line for any input. **Constrained-shape fields** (TC ID regex, 16-hex hash, branch-name charset) keep their unquoted form — the asymmetry between quoted and unquoted scalars in the template is intentional. Hostile-input coverage: BATS `tc-b6730e7b` (single-line colon/hash/quote/backslash) + `tc-4f9a82c1` (multiline literal `|-` and folded `>-`).

### Lessons: drift signal propagation requires explicit reader wiring

A six-week gap in the manual pipeline: the `manual-execute` adapter had been stamping three drift diagnostic fields (`drift-detected: true`, `drift-detected-at:`, `test_case_hash_at_execute:`) into the result file on every drifted execute, but every read surface -- `gtms status`, `gtms gaps`, `gtms map`, plus their `--json` mirrors -- silently ignored them. The data lived on disk and in the one-shot stderr warning at execute time, and nowhere else. Four patterns generalise to any future adapter-written diagnostic.

- **Adapter-written diagnostics are write-only by default — every new field needs the four-step reader-wiring patch.** Adding a diagnostic key to the artefact gives you a `grep`-able forensic trail and nothing else. To make it visible on a read surface you need all four of: (a) a field on the relevant entry type in `internal/reader/types.go` (with `omitempty` JSON tag), (b) a reader helper call site that opens the artefact and extracts the field — for drift, immediately after each `selectAutomationRecord(...)` in `internal/reader/status.go`, `map.go`, and `gaps.go`, (c) renderer code in `internal/cli/status.go` / `map.go` / `gaps.go` that emits the text marker, and (d) a `--json` path that surfaces the field. Skip any one and the field is invisible. **Rule for design docs:** if a slice specifies a "diagnostic block" the adapter writes, the same slice must specify the reader propagation path -- otherwise the adapter is half-shipped.
- **Per-framework diagnostics gate on the selected record, not the all-records scan.** A TC can have records in multiple frameworks (e.g. `bats` and `manual` side-by-side). Diagnostics emitted by one framework's adapter (drift comes from `manual-execute`) must surface only when the displayed record is that framework — otherwise `[drift]` leaks onto a `pass [bats]` row because a sibling manual record happens to be drifted. The correct hook is **post-`selectAutomationRecord`, gated on `ar.Framework == "manual"`**. The tempting wrong hook is `deriveManualCoverage` (`internal/reader/status.go`), which scans every record before selection to classify `prepared` vs `recorded` -- reusing that path for drift leaks the marker across frameworks. Generalises to manual coverage staleness and any future per-framework diagnostic.
- **Diagnostic / audit timestamps render raw RFC3339 across all surfaces.** `formatRunAt(detail.RunAt)` is the right helper for the friendly "last run" display in the EXECUTE line. It is the **wrong** helper for any field that mirrors a stored audit value — those must stay byte-comparable with the YAML on disk and the JSON output so `grep` / `jq` workflows match across surfaces. Drift's first-pass implementation used `formatRunAt(detail.DriftDetectedAt)` and emitted `Detected at 2026-05-15 07:43 UTC`; the contract required `Drift detected: 2026-05-15T07:43:25Z` (raw RFC3339, matching the file). Tightened in commit `005ee9e7` to consume `detail.DriftDetectedAt` directly. **Rule:** if the field name carries `_at` and traces back to a `*-at:` line in a user-authored / adapter-authored YAML artefact, render raw; do not call `formatRunAt`.
- **Reader-helper-driven tests need fixtures that include `artefact:` on the automation record.** The new `readManualDriftDiagnostics` helper resolves the result-file path from the automation record's `artefact:` field. Pre-existing fixtures (authored before the adapter-discovery-and-auto-heal landed) often omit `artefact:` because the record was generated when the field was optional. Such fixtures silently bypass the new helper -- the test sees no drift surface and looks green for the wrong reason. Round 2 of the BATS iteration spent its budget adding `artefact: gtms/manual/records/tc-XXXX--manual.result.yaml` to six fixture files. **Rule:** any new reader helper that opens a sibling file via a path from the automation record requires fixtures to set `artefact:` explicitly; assert-the-helper-was-called coverage belongs alongside assert-the-output Go tests.

### Prime Renderer & Guidance Fallback

`gtms prime` ships its own command identity (the user thinks "prime", not "automate the manual-prime adapter"). Keeping that identity intact end-to-end across output rendering, guidance, and status hints requires four explicit contracts. Skip any one and the implementation reality of "manual-prime is internally an automate-stage adapter" leaks back into the user-facing surface.

- **Sibling renderer, not shared.** Each user-facing command gets its own `format{Command}Output` helper in `internal/cli/`. `gtms prime` initially reused `formatAutomateOutput`, which baked "Automated" / "Automation created" wording into prime output. The fix introduces `formatPrimeOutput` alongside it. Cost of duplication is low; cost of mutating a shared renderer to suit two callers later is high. Companion: a `whatHappened{Command}` text builder in `internal/cli/guidance.go` for the Next-block body.
- **Two guidance defaults must stay in sync.** Adding a `prime:` guidance key requires both `internal/scaffold/templates.go` `DefaultGuidanceYAML` (scaffolded into `.gtms/guidance.yaml` by `gtms init`, covers fresh projects) and `internal/config/guidance.go` `DefaultGuidance()` (the Go map returned when the user file is missing/malformed, covers projects with no file). Forgetting either silently drops the `Next:` block on the affected command.
- **Per-key fallback in `LoadGuidance`.** Existing projects with a hand-customised `.gtms/guidance.yaml` would lose `Next:` text for any newly-added command unless `LoadGuidance` merges in defaults for missing keys. The merge policy: user file wins on every key it sets; `DefaultGuidance()` fills any missing keys. New pipeline commands inherit this for free as long as both default sources include their key.
- **Status-hint resolver matches the real resolver.** Any helper that previews "what command would the user run next" (`statusHint`, `adapterHint`, `shouldRewriteToPrime`) must resolve adapters using the rules in `internal/adapter/resolver.go` `Resolve()` — or a strict subset. If `Resolve()` would error out (no default, no `--adapter`, non-visibility command), the hint helper must fall through to a generic suggestion rather than picking a "first registered" via map iteration. The canonical guard shape is `shouldRewriteToPrime()` (`internal/cli/status.go`): rewrite fires only when `defaults.{command} == <target>`, OR there is exactly one registered adapter under that command group and it is the target. Anything looser produces non-deterministic hints (Go map iteration order is unstable) in ambiguous multi-adapter projects.

### Lessons: stable testcase-hash field as a wiring-split prerequisite

A stable `testcase-hash` field was added to today's `gtms/automation/records/*.automation.md` shape ahead of the wiring split. The implementation landed first-pass green across 20 BATS specs (no iterations needed). Four patterns generalise to any future stable identity field on automation records / wiring records.

- **Stable identity fields are write-side-only -- execute reads, never repairs.** `testcase-hash` is written by `gtms automate` (create + `--force`), `gtms link`, and lazy auto-create (`pipeline.TryAutoCreateRecord`). `gtms execute` reads the field, recomputes the current spec hash, surfaces drift, and refuses to refresh the stored value. The repair paths are explicit, safest first: `gtms link --refresh <tc>` (recompute both hashes in place, preserve the artefact -- the acknowledgement path), then `gtms execute --allow-stale <tc>` (one-shot bypass, wiring stays stale), then `gtms link <tc> --artefact <path> --force` (relink) or `gtms automate <tc> --force` (regenerate, overwrites the artefact). This is the same ownership rule as `artefact-hash`. Critically, the non-mutation contract is **field-scoped**, not record-scoped -- the existing artefact-path auto-heal in `internal/cli/execute.go` may still rewrite the record file to refresh a stale `artefact:` line, and the new contract says only that `testcase-hash` must come through that rewrite untouched. Tests must assert on the field, not on byte-level record immutability -- assert-the-whole-record fixtures break the moment an unrelated heal fires. The same pattern (rare write paths; never repair on read; field-scoped immutability) applies to every future stable field the wiring layer accumulates.

- **Single resolver helper for all write paths, or subfolder cases get silently missed.** `pipeline.ResolveTestCaseSpec` (`internal/pipeline/resolve.go`) is the only caller that turns a TC ID into the active spec path. `BuildAutomationRecord`, `CreateAutomationRecord`, and `TryAutoCreateRecord` all go through it; `RecordOptions` deliberately does **not** grow a spec-path field. The temptation is to let each writer compute `gtms/test/cases/{tc}.md` directly — that hard-codes the root layout and silently misses subfolder-scoped cases under `gtms/test/cases/{subfolder}/{tc}.md`, producing empty hashes that *look* populated. The lesson generalises: any time the same fact is needed from three write paths, resolve it once at the pipeline layer; per-caller path computation is a recipe for one path drifting from the others. Confirmed by `tc-c1675a5f-automate-subfolder-spec-hash.bats` which would have failed if any writer skipped the resolver.

- **Three production write surfaces, not two.** A reviewer-found gap caught before PRP creation: the obvious writers are `gtms automate` and `gtms link`, but the lazy auto-create path (`pipeline.TryAutoCreateRecord`, invoked by `gtms execute` only when no record exists for the TC + framework) is a **third** writer that goes through `pipeline.CreateAutomationRecord` and therefore must populate every new stable field. It's easy to miss because it lives in execute's call graph and feels like execute work. Generalises: whenever you enumerate "who writes this field", trace `CreateAutomationRecord` / `BuildAutomationRecord` callers in `internal/pipeline/` rather than thinking in command names. The single resolver helper above defends against this — if the writer goes through `CreateAutomationRecord`, it gets the field for free.

- **Reviewer findings landed *into* the ENH record (not deferred to PRP) gave first-pass green.** Three findings -- the single-resolver requirement, the field-scoped non-mutation wording, and the lazy-create third write surface -- were folded into the acceptance criteria *before* the PRP was created. Result: the PRP, BATS suite, and implementation all consumed the same constraints from the same source. No drift between layers, no late-binding correction. The alternative pattern (review findings stored as PRP-only addenda) would have left the ENH record stale relative to the implementation contract. **Rule:** when reviewer findings rewrite ACs, the ENH record is the canonical home -- the PRP inherits, not invents.

### Template / Schema / Snippet Co-Evolution Lessons

One change thickened the manual result template from three sections to four, reshaped six VSCode snippets, revised the manual-result JSON Schema, and rewrote the `USER-GUIDE.md` manual-authoring section. The shape change was self-contained, but it surfaced six general lessons for any adapter that ships a scaffolded YAML template + companion snippets + schema.

- **Schema null-tolerance for pre-stamped empty keys.** A scaffolded template that ships keys with no value (e.g. `result:`, `steps:`) parses those keys as YAML `null`. The schema must accept `null` on every such field, or the freshly stamped file flags schema errors in VSCode before the tester records anything -- the opposite of what a scaffold should do. Here, `result` became `type: ["string", "null"]` with `enum: ["pass", "fail", "skip", null]`, and `steps` became `type: ["array", "null"]`. **Runtime enforcement is unchanged** -- `manual-execute` still rejects empty `result:` at execute time. The schema governs the editor surface; the adapter governs the runtime contract; the two can -- and sometimes should -- differ.
- **Snippet description is a contract with the template.** The `gtms-step-*` snippet descriptions referenced a "notes section" the template never scaffolded. The two slices drifted: a slice that adds tooling assuming a structure **must** ship or verify that structure in the scaffold. Apply this to any adapter that emits a structured artefact and a tooling layer that consumes it -- keep them committed to the same constants in the same commit, and add a unit test that grep-asserts every snippet description names a section that actually exists in the template.
- **VSCode snippet descriptions are plain text, not markdown.** Don't try to embed backticks, bold, italic, or links -- they render literally in the tooltip. The pre-fix step-snippet descriptions used Go raw-string + concatenation gymnastics (e.g. `` `...below ` + "`" + `steps:` + "`" + `)" ``) to inject backticks around `steps:`; the resulting JSON was valid but the tooltip showed literal backticks. If you want code styling in a snippet description, you don't get it -- drop the backticks and write plain prose.
- **Value-first snippets when the template owns the key.** The template ships `result:` (empty value); the `gtms-pass` / `gtms-fail` / `gtms-skip` snippets emit the value first (`pass\nexecuted_by: ...\nexecuted_at: ...`) so the tester places the cursor after `result: ` and expands. The snippets do NOT re-emit `result:`. Rule of thumb: when scaffold and snippet both touch the same field, the side stamped at scaffold time owns the key; the side expanded at edit time owns the value. Both touching the key is a duplicated-key bug.
- **Pre-release schema tightening, called out explicitly.** One change reshaped `defect:` from a scalar to a YAML list. Files authored against the earlier scalar snippet became editor-invalid. We took it as an intentional pre-1.0 tightening, documented in `RELEASES.md` (Unreleased), with trivial remediation (wrap the scalar in a single-item list). The alternative -- accepting both shapes with `["array", "string"]` -- would entrench the wrong shape long-term. **When tightening pre-1.0, call it out; don't absorb it silently.**
- **Section ordering -- free-form last.** The unbounded-length section (`steps:`) goes last so short, fixed-shape metadata (`branch:`) isn't pushed below it. Section order in the template: `GTMS contract` -> `OVERALL RESULT` -> `Optional metadata` -> `Steps`. This generalises: any scaffolded artefact with one free-form section and several fixed-shape sections should put the free-form one last.

---

## Manual Read/Report Semantics

This slice wired the reader, dashboard, gap, and bulk-execute surfaces to the manual pipeline. No new on-disk format -- the slice consumes the same automation records the manual-prime adapter stamps and the manual-execute adapter updates.

### Reader Sub-State (additive, no breaking change)

A new field `ManualCoverage string` (`"prepared"` | `"recorded"` | `""`) is added with `omitempty` to four reader types:

- `PipelineEntry` (used by `gtms status`)
- `PipelineDetailEntry` (used by `gtms status -r --detail`)
- `GapEntry` (used by `gtms gaps`)
- `MapEntry` (used by `gtms map`)

Folder summaries gain two `omitempty` int fields:

- `ManualPrepared` — TCs in this folder that have a manual record with `result:` empty
- `ManualRecorded` -- TCs in this folder that have a manual record with `result:` populated (`pass | fail | skipped`)

**Existing fields are unchanged.** `NoAutomation` semantics are preserved byte-for-byte — manual-only TCs still classify as `NoAutomation` because manual is an on-ramp. The new sub-state is the disambiguator inside the `NoAutomation` group, not a replacement for it.

The `deriveManualCoverage` helper iterates **all** automation records for a TC (not just the one `selectAutomationRecord` picks). A TC with both `bats` and `manual` records correctly reports `"recorded"` even when the selected record is the `bats` one — manual coverage is independent of which framework wins record selection.

### Two Discrete Dispatch Points (Not a Registry)

An early design sketched a `PipelineFramework` interface for manual record-semantics decisions. That collapsed into two discrete dispatch points because each manual-specific decision has exactly one call site:

1. `cli/execute.go::shouldSkipExecute(record, resolved)` — adapter-first bypass via `adapter.IsManualFramework(resolved)`. One new parameter, one early return at the top of the function.
2. `cli/prime.go::manualUpdateHash(...)` -- early return on the prime path before any adapter is invoked. Refreshes `test_case_hash:` and strips drift diagnostic fields in the user-authored result file; the automation record is not touched in either direction after the wiring cutover. The substrate manual results actually live on (the user-authored `gtms/manual/records/<tc>--manual.result.yaml`) carries its own `executed_at:` stamped by the manual snippets; that field is independent of the automation-record substrate (which is no longer mutated by execute or prime).

The "one dispatch point, no scattered checks" architectural constraint is satisfied without an interface or registry. **Lesson for future slices**: don't reach for a pluggable dispatch interface unless you have at least three call sites that need to vary together. Two single-site decisions are clearer as two early returns than as two methods on an interface that only has one non-default implementation.

### Adapter Authors: What This Changes for You

Nothing in the adapter contract itself. Slice 3 is purely a reader/CLI-dispatch slice — it does not change the manual-execute write path (slice 2 owns that), the result contract, or any environment variable. Your adapter still:

- Receives `GTMS_RESULT_TEMPLATE` / `GTMS_RESULT_VALUE` / `GTMS_RESULT_TESTCASE` / `GTMS_RESULT_TESTCASE_HASH` / `GTMS_RESULT_FRAMEWORK` exactly as documented for the `manual-execute` adapter.
- Writes the orthogonal handoff (`status: complete + result: pass|fail|skip`).
- Updates the same automation record (`framework: manual`, `result:` populated, `artefact:` pointing at the user-authored result file).

What changes is observability: a primed-but-not-executed TC is now visibly distinct from a no-coverage TC in the dashboard, so testers see in-flight manual coverage alongside fully-executed coverage.

### Re-Prime Preservation (`prime --update-hash`)

`gtms prime tc-X --framework manual --update-hash` is the audit-anchor refresh path. After a successful manual execute, the prior outcome is still semantically valid (the user executed against the test case content that was hashed); only the hash itself needs refreshing if the spec text was edited cosmetically. The path:

1. Re-hashes the current test case spec with `pipeline.HashFile`.
2. Reads the existing `gtms/manual/records/{tc}--manual.result.yaml`.
3. Updates `test_case_hash:` to the new value; removes any "drift detected" diagnostic block left by `manual-execute-script.sh`.
4. Returns. The manual-execute adapter is **not** invoked. The automation record is **not** rewritten.

`gtms prime --force` (destructive overwrite) is unchanged -- that path is an explicit reset signal that wipes both the result file and any prior outcome.

---

## Artefact Declaration Contract

Adapters declare their output paths through automation record frontmatter fields. `gtms delete` uses these fields to discover and remove adapter-produced artefacts without knowing anything about the framework that created them.

### Fields Used by Delete

| Field | Set by | Description | Used for |
|-------|--------|-------------|----------|
| `artefact` | GTMS (from adapter output) | Path to the generated test script (relative to project root) | Deleting test artefacts |
| `executed_artefact` | GTMS (from execute adapter) | Path to execution output / result file (relative to project root) | Deleting result files |

**Cross-field dedupe:** if `artefact` and `executed_artefact` resolve to the same canonical absolute path (e.g. `scripts/tc.sh` vs `./scripts/tc.sh`), `gtms delete` removes the file once, attributed to **Test artefacts** (artefact wins priority). The duplicate is silently filtered from the result-files deletion list. Adapters that legitimately point both fields at the same file -- common when the execute adapter runs the same script it was given -- do not need to worry about double-deletion failures.

### Path Safety Contract

All artefact paths stored in automation records **must be relative to the project root**. The `internal/pathsafe` package is the single source of truth for path-safety enforcement; three GTMS surfaces consume it:

1. **`gtms execute` (resolver fast-path)** -- `pipeline.ResolveArtefact` calls `pathsafe.ResolveUnderRoot(projectRoot, storedPath)` before any `os.Stat` or downstream open. Both absolute paths outside the root and `..`-traversing relative paths are rejected with a `*pathsafe.PathSafetyError`. The fast-path now returns the same `filepath.ToSlash`-normalised relative form as the glob fallback -- callers see one shape, not two.
2. **`gtms delete` (record-driven cleanup)** -- atomic, fail-loud: if any path declared by any record in scope resolves outside the project (via `..`, an absolute path, or a symlink that escapes), the deleter aborts the **entire** operation **before any file is removed**, returns a `*pathsafe.PathSafetyError`, prints `✗ Refusing to delete: artefact path "..." resolves outside the project-owned allowlist.` to stderr, and exits non-zero. Partial deletion is impossible. Existence checks are deferred -- non-existent files are silently skipped during the deletion pass once containment is confirmed for every entry.

Common mechanism (`pathsafe.ResolveUnderRoot` / `pathsafe.IsWithinRoot` / `*pathsafe.PathSafetyError`):

- **Canonicalisation** — `filepath.Abs` + `filepath.EvalSymlinks` on both the project root and the resolved candidate. Symlinks in path components are followed; the **target** must still resolve under the root, so an in-repo symlink pointing outside the repo is rejected.
- **Containment** — the canonical absolute path must equal the root or be prefixed by `root + os.Separator`. The "prefix-trap" case (`/project-evil` against root `/project`) is rejected.
- **Return shape** — both the canonical absolute path (for `os.Stat` / open) and the `filepath.ToSlash`-normalised project-relative path (for storage in records) are returned, so pipeline-style and reader-style callers share one implementation.

The "atomic, fail-loud" wording matters across all three surfaces: a security refusal that exits `0` would silently pass through CI pipelines and `&&` chains as success. Each surface signals failure to the shell so a corrupted or hand-edited record never quietly drops part of a batch.

Adapters that write artefact paths should use project-relative paths (e.g. `test/acceptance/my-feature/tc-a1b2c3d4-test.bats`, not `/home/user/project/test/acceptance/...`). Adapters that need to write outside the project tree must declare those paths via `gtms.config` (project or adapter-level configuration) — not by smuggling absolute paths into automation records.

> **Companion guard:** the same `internal/pathsafe` package also exposes `ValidateFilenameComponent(value, label)` -- applied at every filename-construction site in `internal/pipeline/` and `internal/execution/`. This catches caller-supplied identifiers (test case IDs, framework names, task IDs) that contain path separators, traversal sequences, or control characters before they're embedded in `filepath.Join` calls. Adapters never need to call this themselves — GTMS enforces it at the package boundary on every write.

### Migration Note: record-driven delete

Previously, `gtms delete` used hardcoded directory walks (`test/acceptance/*.bats`, `results/junit/*`) to find framework-specific artefacts. This has been replaced with record-driven discovery. Artefacts from earlier adapter runs that lack `artefact` or `executed_artefact` fields in their automation records will not be found by the new deleter. To clean them up, either re-run `automate`/`execute` to repopulate the records, or delete the orphaned files manually.

The first implementation pass silently filtered unsafe paths instead of refusing the operation; the four BATS tests `tc-c84dbaba`, `tc-a4acdb88`, `tc-8ea2d8b6`, `tc-d4e1a7f2` in `framework-agnostic-delete-scaffold/` caught it and the fix landed in the same worktree (commit `1b2a74df`). The contract above is the post-fix shape and is load-bearing security -- adapters that emit unsafe paths will now surface the failure loudly.

### Migration Note: path-safety package

The path-safety helpers originally lived in `internal/reader/delete.go`. They were lifted into a neutral `internal/pathsafe/` package (alongside the `ValidateFilenameComponent` filename guard) so `internal/pipeline` could consume the same implementation without an illegal `pipeline -> reader` import. `internal/reader/delete.go` now delegates to `pathsafe`; the previous `reader.PathSafetyError` and `reader.IsPathSafetyError` symbols are preserved as type alias / delegating helper to keep external callers working. New code should import `internal/pathsafe` directly.

### Artefact Discovery Contract -- retired on the execute path

**Not live today.** The wiring cutover made the wiring record the sole, immutable source of truth on the execute path: `gtms execute` reads the wiring record to pick the framework, execute adapter, and artefact, and does **no** auto-heal, **no** glob fallback, and **no** record auto-create (`internal/cli/execute.go`). A test case with no wiring record is a **hard error** (`No wiring record found for '<tc>' (framework: <fw>)`) -- resolve it with `gtms automate` or `gtms link`, not by globbing. The `artefact-glob` field is still accepted and validated at config load (must contain `{testcase}`, be project-relative, no `..`), but the glob-discovery/auto-create consumer it once fed (`pipeline.DiscoverArtefact` / `TryAutoCreateRecord`) is not wired into the current CLI path. The primitive's contract is retained below as reference for tooling and a possible future re-wire.

**DiscoverArtefact primitive contract (library-level; not invoked by execute today):**

1. **Adapter-first key, not framework-string.** The primitive reads `resolved.Config.ArtefactGlob`, the glob on the **resolved adapter's config**. A `--framework <fx>` flag with no adapter declaring that framework has no glob and therefore no match -- the framework string never substitutes for actual adapter resolution. This mirrors `adapter.IsManualFramework(resolved)` and the broader "adapter-first detection" rule (see [Adapter-First Detection Rule](#adapter-first-detection-rule)).
2. **Exactly-one-match contract.** `pipeline.DiscoverArtefact` returns the single project-relative match or an error. Zero matches and multiple matches are both errors -- never silent.
3. **Path safety is non-negotiable.** Every candidate path returned by the walk is run through `pathsafe.ResolveUnderRoot` before being added to the match set. Symlinks in path components are followed; if the target resolves outside the root, the candidate is silently dropped. Absolute paths and `..`-traversal segments in the glob itself are rejected at config-validation time. The same `internal/pathsafe` package documented above is the only authority.
4. **Wiring, not a status record.** The identity record execute resolves against is the six-field wiring record (`gtms link` writes it on the explicit path; the built-in automate writes it with `artefact-hash: pending` on the generated path). There is no `status: developed`/`accepted` field -- the earlier automation-record provenance is retired. Adapters never write the record themselves.
5. **Manual-execute is carved out.** Discovery never engages when `adapter.IsManualFramework(resolved)` is true. Manual records come exclusively from `gtms prime --framework manual` followed by `gtms execute --adapter manual-execute`.

**Pattern syntax:**

- `{testcase}` is the only placeholder. Substituted with the TC ID (e.g. `tc-a1b2c3d4`) before walking.
- `**` matches any number of path segments (doublestar).
- Other segments use `filepath.Match` semantics (`*`, `?`, character classes).
- Walk excludes `.git`, the sentinel `gtms/` parent, and anything outside the project root.

**Error-text contract (what users see):**

These are the error shapes the `pipeline.DiscoverArtefact` primitive defines (its library-level contract; not surfaced by the current execute path, which errors with `No wiring record found` instead). The sentences are intentionally capitalised:

```
# zero match — surfaces the post-substitution pattern, not the {testcase} template
No artefact found for tc-a1b2c3d4 matching pattern 'test/acceptance/**/tc-a1b2c3d4*.bats'

# ambiguous match — surfaces every candidate path plus the gtms link hint
Multiple artefacts found for tc-a1b2c3d4:
  test/acceptance/folder-a/tc-a1b2c3d4-first.bats
  test/acceptance/folder-b/tc-a1b2c3d4-second.bats
Use 'gtms link' to specify the correct artefact.
```

Showing the post-substitution pattern (rather than the raw `{testcase}` template) gives users the concrete path shape that was searched — what they'd grep for if hunting the missing artefact.

**Design lesson (round-2 fix, commit `7872897c`):** the initial implementation wired discovery into both execute call sites but the error paths were `// fall through to existing missing-record message` comments. The discovery-aware sentences were dead code; users saw only the legacy `No automation record found for '<tc>' (framework: <fx>). Run 'gtms automate <tc>' first.` regardless of what discovery actually computed. The BATS suite caught it -- when a primitive can return an error the caller might swallow, the AC checklist must include a *"user sees the error"* assertion, not just *"the primitive computes the error"*. Go unit tests pinned the primitive's contract but couldn't observe the CLI surface; BATS did.

---

## Result Contract Reference

The result contract is the communication channel between adapters and GTMS.

### Location

```
.gtms/results/{task-id}.handoff.yaml
```

One file per task, keyed by task ID. The `.gtms/` directory is gitignored — handoff contracts are transient.

### Fields

| Field | Written by | Required | Description |
|-------|-----------|----------|-------------|
| `task` | GTMS | Yes | Task ID — links back to the task file |
| `command` | GTMS | Yes | Command that triggered this (create, automate, execute) |
| `target` | GTMS | Yes | Requirement ID or test case ID |
| `adapter` | GTMS | Yes | Adapter instance name |
| `mode` | GTMS | Yes | `sync` or `async` |
| `created` | GTMS | Yes | Timestamp when adapter was invoked |
| `status` | GTMS, then adapter | Yes | `pending` -> `in-progress` -> `complete` or `error`. The test outcome lives on the orthogonal `result:` field, not on `status`. See below. |
| `result` | Adapter | On `complete` | Test outcome: `pass` / `fail` / `skip` / `error`. Required when `status: complete`; must be empty when `status: pending`/`in-progress`. Orthogonal to `status`. |
| `artefact` | Adapter | No | Primary output file path. Comma-separated multi-path is legitimate only on `create` (one file per test case). On `automate`, exactly one path is expected -- multi-file output is rejected at automate time and no record is written. |
| `artefact-hash` | Adapter or GTMS | No | SHA-256 hash of the artefact file at completion. Used for stale detection — if the artefact changes after execution, the result is marked stale. |
| `attempts` | Adapter | No | Number of attempts (defaults to 1) |
| `summary` | Adapter | No | Human-readable outcome description |
| `log` | Adapter / GTMS | No | Raw adapter output (stdout + stderr) or a pointer (URL/path) to it. On the contract the adapter-facing field is `log:`; when the pipeline builds the committed record it lands in the record's `notes:` field (renamed from `log:`). Surfaced via `gtms status <tc-id>` for fail/error results. Capped at 64 KB in the committed record -- oversize logs spill to `.gtms/logs/{task-id}.log` (transient, gitignored) and the record stores the spill path in `notes-spill:`. Tier 1 failure paths get this filled in automatically; Tier 2 scripts should write a `log: \|` block (see the canonical shape in `adapters/remote-pester-lean.sh`). |
| `warnings` | Adapter | No | List of non-fatal warning strings the adapter wants surfaced to the user. GTMS merges these into the CLI output alongside any internally-generated warnings. Example: `warnings:\n  - "prompt template missing guides section"`. Adapters that don't populate this field see zero behaviour change. **Note:** anything an adapter writes to stderr is also surfaced via the same warning channel on sync invocations -- see "Adapter Stderr -> Warnings" below. Both channels can be used together; contract warnings render before stderr-derived ones with no collision. |
| `completed` | Adapter | No | Timestamp when work finished |

### Status and Result Values (orthogonal contract)

`status:` carries the adapter's execution state; `result:` carries the test
outcome. They are orthogonal. `status: complete` REQUIRES a `result:` value;
`status: pending` / `in-progress` REQUIRE `result:` to be empty. The retired
values `status: fail` and `status: skipped` are **rejected** by validation -- an
execute adapter whose tests ran but failed writes `status: complete` +
`result: fail`, not `status: fail`.

**Status values** (adapter execution state):

| Status | Meaning | `result:` |
|--------|---------|-----------|
| `pending` | GTMS set this before invoking the adapter. If it never changes, the adapter didn't report back (crash, hang, or misconfiguration). | empty |
| `in-progress` | The adapter is running (async work in flight). | empty |
| `complete` | The adapter ran to completion. The test outcome is carried by `result:`. | required |
| `error` | The adapter could not complete its execution: the test framework couldn't start (tool not installed, syntax error in script, network failure). | empty |

**Result values** (test outcome; set only when `status: complete`):

| Result | Meaning | Pipeline |
|--------|---------|----------|
| `pass` | the test ran and passed | `pass` |
| `fail` | the test ran and failed (framework ran the tests, one or more failed) | `fail` |
| `skip` | the test was skipped at runtime (for BATS every TAP result line was `ok N # skip ...`). Renders as `⊘` on the dashboard; the pipeline maps contract `result: skip` -> committed record `result: skipped`. | `skipped` |
| `error` | the test could not run | `error` |

Emit `result:` only when it is non-empty (see the shipped `bats-runner.sh`
conditional heredoc for the reference pattern).

> **Tier 1 by default:** without `fail-exit-codes:` (see next subsection), a Tier 1 adapter can only produce `complete` (exit 0) or `error` (any non-zero). All assertion failures look identical to "couldn't run." Reach for `fail-exit-codes:` when your framework's exit code already encodes the distinction (BATS, Jest, pytest, RSpec all use exit 1 for "tests ran and failed"); reach for Tier 2 when you need to inspect framework output (TAP/JUnit) before deciding.

#### Signalling fail from Tier 1 (`fail-exit-codes`)

Add a `fail-exit-codes:` list to a Tier 1 adapter entry to map specific non-zero exit codes to `status: complete` + `result: fail` instead of `status: error`. Most modern test runners exit `1` on assertion failure and reserve `2`, `126`, `127`, `255` etc. for "couldn't run" situations — declaring `fail-exit-codes: [1]` is enough to match that convention.

```yaml
adapters:
  execute:
    bats-runner:
      mode: sync
      command: bats {artefact_file}
      framework: bats
      fail-exit-codes: [1]   # bats exits 1 on assertion failure
```

Semantics:

| Exit code | `fail-exit-codes:` unset (default) | `fail-exit-codes: [1]` |
|-----------|-----------------------------------|------------------------|
| `0` | `status: complete` + `result: pass` → pipeline `pass` | `status: complete` + `result: pass` → pipeline `pass` (unchanged) |
| `1` | `status: error` → pipeline `error` | `status: complete` + `result: fail` → pipeline `fail` |
| `127` (binary missing) | `status: error` → pipeline `error` | `status: error` → pipeline `error` (unchanged) |

The list accepts integers ≥ 1. `0` is reserved for pass and is rejected by the config loader. Tier 2 (`script:`) adapters ignore the key at runtime — setting it on a Tier 2 entry produces a load-time warning naming the adapter. If your framework signals failure with anything other than a non-zero exit code (or you need to inspect TAP/JUnit before deciding), use the Tier 2 pattern below instead.

```bash
# Tier 2 execute script — distinguish fail from error when exit code alone isn't enough
bats "${GTMS_ARTEFACT_FILE}" 2>"${TMPDIR}/stderr.txt"; EXIT=$?
if [ "$EXIT" -eq 0 ]; then
    STATUS="complete"; RESULT="pass"    # tests passed
elif [ "$EXIT" -eq 1 ]; then
    STATUS="complete"; RESULT="fail"    # tests ran, some failed (result: fail, not status: fail)
else
    STATUS="error"; RESULT=""           # couldn't run tests (e.g. exit 126/127 = not found)
fi
```

#### Canonical classification pattern (remote Tier 2, TAP + SSH)

For remote adapters, exit code alone is ambiguous — SSH layers its own exit codes on top of the framework's. The shipped `remote-bats-lean.sh` uses a three-way decision: inspect the **TAP output** to count genuine assertion failures, and use the **process exit code** to detect transport failures. Copy this pattern for any remote BATS adapter:

```bash
# Run remotely; capture stdout (TAP) and exit code
TAP_OUT=$(ssh "${REMOTE}" "cd ${REMOTE_DIR} && bats ${SPEC}") ; EXIT=$?

# Transport failure — SSH couldn't deliver the run (exit 255 is SSH's "connection failed")
if [ "$EXIT" -eq 255 ]; then
    STATUS="error"; RESULT=""; SUMMARY="SSH connection to ${REMOTE_HOST} failed (exit 255)"
# TAP-aware classification — parse what bats actually produced
elif echo "$TAP_OUT" | grep -q '^1\.\.'; then
    FAIL_COUNT=$(echo "$TAP_OUT" | grep -c '^not ok')
    if [ "$FAIL_COUNT" -gt 0 ]; then
        STATUS="complete"; RESULT="fail"; SUMMARY="${FAIL_COUNT} test(s) failed on ${REMOTE_HOST}"
    else
        STATUS="complete"; RESULT="pass"; SUMMARY="All tests passed on ${REMOTE_HOST}"
    fi
# No TAP plan line — bats didn't start (binary missing, syntax error, etc.)
else
    STATUS="error"; RESULT=""; SUMMARY="bats produced no TAP output (exit ${EXIT})"
fi
```

The same shape works for Pester + JUnit XML: count `<failure>` elements under `<testcase>`; treat missing/malformed XML as `error`. The key principle -- **don't conflate transport/infra exit codes with assertion outcomes** -- is enforced across all shipped BATS/Pester adapters.

#### Diagnostic log payload

On a `result: fail` or `status: error` result, the adapter's raw output is persisted to the committed record's `notes:` field (written by the adapter as the contract's `log:` field) and rendered under `gtms status <tc-id>` so a human or AI agent can diagnose the failure from one command -- no hunt through NUnit XML, no re-run with verbose flags, no `.gtms/results/` excavation.

- **Tier 2 adapters** should write a `log: |` block in the result contract. Use the canonical heredoc shape below — the two-space indent on every content line is YAML-significant (defines the block scalar's indentation). See `adapters/remote-pester-lean.sh` and `adapters/pester-runner.sh` for real examples.

  ```bash
  cat > "${GTMS_RESULT_FILE}" <<EOF
  ...
  summary: "${SUMMARY}"
  log: |
  $(echo "${OUTPUT}" | sed 's/^/  /')
  completed: $(date -u +%Y-%m-%dT%H:%M:%SZ)
  EOF
  ```

- **Tier 1 adapters** get `log:` filled in automatically on failure. GTMS captures stdout + stderr from the process and writes them into the result contract (stderr leads when both are present — most frameworks emit the error line to stderr). The mechanism fires for both the `fail` and `error` branches of the `fail-exit-codes:` mapping.

- **Pass runs overwrite** any previous failure log. That's deliberate: a green dashboard must never surface stale failure output. The renderer also guards against stale fixtures by hiding the log block on pass results even if the record still carries one.

- **Size cap.** Committed logs are truncated to 64 KB at a UTF-8 rune boundary. Oversize logs spill to `.gtms/logs/{task-id}.log` (gitignored, transient) and the automation record stores the spill path in `notes-spill:`. The detail view header indicates truncation and cites the spill file, so on the authoring machine the full output is still one file read away. On a fresh clone the truncated head is what's available -- by design. *Note: the result contract's adapter-facing field stays as `log:` -- only the pipeline record renamed to `notes:`.*

- **Reset clears it.** `gtms reset <tc-id>` zeros out the record's `notes:` and `notes-spill:` alongside the other execute-outcome fields. The spill file itself stays -- `.gtms/` is self-cleaning.

### How Each Tier Reports Results

| Tier | How the adapter reports | What GTMS infers |
|------|------------------------|------------------|
| **Tier 1** | Exit code only. Adapter author never sees the result contract. | GTMS sets: `status` from exit code (0 = complete, non-zero = error). If `fail-exit-codes:` is declared on the adapter entry, listed non-zero codes map to `fail` instead. `attempts` = 1, `completed` = now. |
| **Tier 2** | Script updates `$GTMS_RESULT_FILE` directly. | GTMS reads the file after script exits. If `status` is still `pending`, falls back to exit code. |
| **Built-in** | GTMS core handles internally. | No external result contract needed. |

> **Artefact detection (Tier 1 / Tier 2 fallback):** When GTMS handles the result (Tier 1, or Tier 2 when the script doesn't update the contract), it populates the `artefact` field from two sources: (1) files written via stdout streaming (`<gtms-file>` tags), or (2) new files detected in the output directory after adapter invocation. The output directory scan uses a before/after snapshot to report only NEW files created by the adapter, not pre-existing files. If neither source produces files, the field remains empty.
>
> **Tier 2 scripts that update the contract directly** are responsible for setting the `artefact` field themselves. When a script writes `status: complete` to `$GTMS_RESULT_FILE`, GTMS uses the script's values as-is and does not run the output directory scan. If your Tier 2 script produces files but doesn't set the `artefact` field, the field will remain empty in the contract.

### Adapter Stderr -> Warnings

On **sync** invocations (Tier 1 and Tier 2), anything an adapter writes to stderr is captured and surfaced to the user as `⚠` warning lines after the adapter completes — both on success and failure. The mechanism lives in `internal/adapter/invoker.go`'s `handleSyncResult`: each non-blank stderr line becomes a separate entry in `InvokeResult.Warnings`, which the CLI then renders via `output.Warnf`.

| Channel | Use it for | Format |
|---------|-----------|--------|
| Stdout `<gtms-file>` blocks | File output (test cases, automation scripts) | XML-tagged blocks |
| Stdout (outside tags) | Adapter's summary text | Free text |
| Stderr | Progress notes, deprecation warnings, drift detection, "you should know about X" messages | One message per line |
| Result contract `warnings:` (Tier 2) | Structured warnings the adapter wants to persist | YAML list |

**Behaviour:**

- Each non-blank stderr line becomes one warning entry. Blank lines are filtered.
- Lines are surfaced verbatim (no truncation, no ANSI stripping, no implicit cap on count).
- Stderr is surfaced on both success (exit 0) and failure paths -- failure has always shown stderr in the error summary; success-path surfacing was added later.
- Contract warnings render first, then stderr-derived warnings — no collision, no duplication.

**Async adapters are unaffected** — they don't run through `runAdapterProcess`, so the only warning channel for async work is the contract `warnings:` field, surfaced when the user runs `gtms {cmd} status <target>`.

**Don't mix narration with file output on stdout.** Put progress messages on stderr — they'll surface as warnings, won't pollute the captured summary, and won't interfere with `<gtms-file>` parsing.

```sh
# Tier 2 example: emit progress to stderr, files to stdout
echo "INFO: starting framework run" >&2
echo "WARN: deprecation notice from underlying tool" >&2
printf '<gtms-file name="result.txt">\nok\n</gtms-file>\n'
```

### What GTMS Does With Results

| Command | Record built | Key fields used |
|---------|----------------------|-----------------|
| `create` | None — test case files *are* the record | `status` (did adapter complete) |
| `automate` | Wiring record (`.wiring.yaml`) in `gtms/automation/wiring/` | `testcase`, `testcase-hash`, `framework`, `adapter`, `artefact`, `artefact-hash` |
| `execute` | Result handoff (`.gtms/results/<task>.handoff.yaml`) + per-test row (`gtms/execution/*.results.yaml`); the wiring record is NOT mutated | `status`, `result`, `summary`, `log` |

### Create Validation Contract

After a `create` adapter returns cleanly (exit 0), GTMS inspects every `.md` file in the output directory whose filename matches `tc-{8hex}-*.md` and enforces five invariants:

1. The frontmatter contains a non-empty `test_case_id` field.
2. `test_case_id` matches the format `^tc-[0-9a-f]{8}$`.
3. `test_case_id` equals the filename ID portion (the `tc-XXXXXXXX` prefix).
4. `test_case_id` is one of the **pre-generated batch IDs** passed to the adapter via `{tc_ids}` (Tier 1) / `$GTMS_TC_IDS` (Tier 2).
5. No two emitted specs share the same `test_case_id`.

Files that don't match the `tc-{8hex}-*.md` naming pattern (e.g. `notes.txt`, `config.yaml`, README fragments) are silently skipped — the validator only inspects files that look like GTMS spec output.

**On any violation:**

- The process exits non-zero; the CLI prints the `✗ Task failed: ...` summary on stderr with one indented line per offender (`    {filename}: {reason}`).
- The result contract is updated: `status: error`, and a new `validation-error:` field carries the same formatted summary for durability.
- The task file is moved to `gtms/tasks/error/`.
- The offending files are **left on disk** — never auto-deleted, never renamed. Users / AI agents need them in place to diagnose.

**On success** the invocation proceeds to the normal result-contract handling path with no behavioural change.

**Why this matters.** The contract exists because the dashboard's source-of-truth guarantee depends on filename IDs and frontmatter IDs agreeing. A single mismatched spec silently splits the downstream join keys (filename vs. frontmatter) and produces em-dashes in `gtms status` for tests that actually ran and passed. The validator closes that gap at write time rather than trying to detect it at display time.

### Spec-Authored Command Hallucinations (beyond ID integrity)

The create validator checks **ID integrity** (filename <-> frontmatter <-> batch IDs). It does NOT check the **content** of a spec for fidelity to the product. An AI create adapter can emit a spec that passes every ID-integrity invariant but prescribes `gtms` commands the product rejects. Observed classes (surfaced in dogfooding):

- **Hallucinated flags.** Spec prescribes `gtms delete tc-XXXXXXXX --yes`. The product has no `--yes` flag -- `gtms delete` is non-destructive by design and confirmation prompts were deliberately rejected. The LLM extrapolated from the generic "destructive CLI takes `--yes`/`--force`" convention. Symptom at execute time: `Error: unknown flag: --yes`. 12/12 specs in the batch carried this defect.
- **Rejected positional-arg prefix.** Spec prescribes `gtms delete gtms/test/cases/bulk`. The product explicitly rejects this (`internal/cli/validate.go:80-99`, friendly message: *"don't include the gtms/test/cases/ prefix — GTMS adds it automatically"*). Symptom: CLI validation error.
- **Vocabulary cross-contamination between sibling views.** When an ENH touches *one* of two similar code paths (e.g. detail-view vs folder-summary) and asks for a regression spec on the *unchanged* sibling, the AI can copy vocabulary from the in-scope view into assertions for the out-of-scope view. Surfaced in dogfooding: `tc-1aa2f86a` was generated to assert the folder-summary `Key:` legend is unchanged, but asserted the detail-view token set (`complete/pass`, `failed`, `error/stale`, `pending`) against a legend that uses entirely different vocabulary by design (`all pass`, `some failing`, `not yet attempted`). The command-level invocation was correct, so `/tests-verify-intent` couldn't catch it (both spec and BATS had drifted together) -- execute caught it cleanly as a cross-token mismatch. Mitigation until a dedicated `/specs-verify` check lands: when an ENH scope explicitly says "sibling view unchanged", grep the generated spec for tokens that belong to the *other* view and cross-reference against product source.

These are all **spec-quality** defects, not ID-integrity defects. They slip through the ID-integrity validator because the filenames and IDs are all correct -- the defect is in the prose *content* of the spec. See `feedback_bug_enh_records.md` / `dogfood_spec_authoring.md` in session memory for the running catalogue.

**Today's defences** (partial):
- `/tests-verify-intent` (renamed from `/tests-verify` 2026-07-10) was hardened (2026-04-17) to fail specs whose BATS doesn't invoke the prescribed command string literally — this catches the class when BATS quietly diverges from spec, which is often how a hallucinated flag first surfaces.
- A dedicated `/specs-verify` spec-vs-product check is proposed. Until it ships, the manual defence is to grep new `gtms/test/cases/<folder>/*.md` for suspicious flags (`--yes`, `--force`, `--confirm`) and cross-reference against `gtms <cmd> --help`.

**Upstream fix** (when tackled): tighten your create adapter's prompt template (the file named by `prompt-template:` in `gtms.config`, which you author -- `gtms init` does not scaffold one) with an explicit constraint -- *"only reference flags documented in `gtms <cmd> --help`"*. This removes the defect at generation rather than catching it downstream.

**What adapter authors need to do:**

| Tier | What to do |
|------|-----------|
| Tier 1 (`command:`) | Substitute `{tc_ids}` into your command and pass it through to your adapter. Use the IDs in the order supplied, one per emitted spec. Write each spec to `{output_dir}` — not to CWD-relative paths. |
| Tier 2 (`script:`) | Read `$GTMS_TC_IDS` (comma-separated). Write each spec to `$GTMS_OUTPUT_DIR`. Use the IDs in order. |
| Built-in | N/A — core handles this without adapter involvement. |

**Common failure modes observed in the wild (all rejected by the validator):**

- Adapter invents a fresh ID instead of using the pre-generated batch. Violates #4.
- Adapter uses the same batch ID for two files. Violates #5.
- Adapter writes a filename like `tc-AAAAAAAA-slug.md` with `test_case_id: tc-bbbbbbbb` in the frontmatter. Violates #3.
- Adapter emits a spec whose frontmatter YAML is missing `test_case_id` entirely. Violates #1.

Whatever the trigger — prompt drift, context compaction, model quirk — the validator treats all of them the same way: fail the task, name the file, leave the evidence.

**Scope boundary.** The validator runs **only** on the `create` command path. It does *not* run on `automate`, `execute`, `status`, `gaps`, `map`, or `triage`. Pre-existing mismatched specs (from before the validator shipped) are not auto-corrected — rename them manually, or wait for a future `gtms doctor` command.

**Walk depth.** The validator scans `outputDir` **top-level only** (`os.ReadDir`, not `filepath.Walk`) -- matching `snapshotDir` and `scanOutputDir`, which have always been flat. Files in subdirectories of `outputDir` are **never inspected**. This matches the GTMS contract: create outputs land flat in `outputDir`. If an adapter writes a spec into a subdirectory of `outputDir`, that file is invisible to the validator, invisible to artefact detection, and will not be counted in the result contract. Don't do it -- write directly into `outputDir`. Sub-folder invocations like `gtms create parent/child` get their own `outputDir` scoped to the leaf folder; validation and artefact detection happen flat at that leaf. (A past re-opening: before the fix, the validator walked recursively while the snapshot was flat, so nested pre-existing files from sibling invocations were wrongly rejected by parent-level invocations.)

### Post-Fill Validation Gate

The create validator runs at the *end* of `gtms create` (post-write). A complementary **post-fill validation gate** runs at the *entry* of `gtms automate`, `gtms prime`, and `gtms execute` -- before any adapter is invoked. It catches frontmatter corruption introduced during manual editing or by an upstream adapter.

The gate calls `adapter.ValidateTestCasePostFill(projectRoot, target)` and checks:

1. **Frontmatter `test_case_id` matches the filename ID** -- the same invariant the create validator enforces at write time, re-checked because a user or editor may have modified the file since creation.
2. **Required frontmatter fields present** — `test_case_id` must exist and be non-empty.
3. **No duplicate IDs in the same folder** — folder-scoped scan rejects two specs sharing a `test_case_id` in the same `gtms/test/cases/` subfolder.

The gate is **not tier-gated** — it runs identically for Tier 0, 1, and 2 adapters. On any violation the command exits non-zero with a `✗` error naming the file and reason. No adapter is invoked, no task file is created.

---

### Built-in Automate Adapters and the `pending` Bootstrap

Two Tier 0 built-in automate adapters -- `agent-automate` and `manual-automate` -- stamp a per-framework skeleton (BATS or Playwright) and write a wiring record without invoking any AI tool. They unblock the Mode 3 zero-config workflow: an agent (or human tester) gets a scaffolded skeleton + pre-wired execute pipeline in one command, fills the test body, then runs `gtms execute`.

#### What the built-ins stamp

`gtms automate tc-XXXX --framework bats --adapter agent-automate` produces:

1. A BATS skeleton file at `test/acceptance/{folder}/{tc-slug}.bats` (the artefact basename preserves the source spec's human-readable slug, e.g. `tc-aaaaaaaa-login-happy.bats`, not just the bare ID) with `setup_file()`, `setup()`, a placeholder `@test` block, and `teardown()`. The depth in `setup_file`'s `PROJECT_ROOT` discovery loop is computed from the subfolder structure. (Playwright lands the equivalent skeleton under `gtms/scripts/playwright/{folder}/`.)
2. A six-field wiring record at `gtms/automation/wiring/{tc-id}--bats.wiring.yaml`:

   ```yaml
   testcase: tc-XXXX
   testcase-hash: <real 16-char hex>   # hashed from the TC spec at write time
   framework: bats
   adapter: bats-runner                # canonical execute adapter for bats
   artefact: test/acceptance/{folder}/{tc-id}.bats
   artefact-hash: pending              # sentinel -- bootstrapped on first execute
   ```

`manual-automate` is a byte-for-byte identical implementation today; the separate name exists for contract stability so future versions can diverge (e.g. richer skeletons for AI agents vs. lighter ones for human authors).

`bats` and `playwright` are both supported -- each ships a per-framework skeleton template (see below). The `manual` framework has no automate stage and returns a prime/execute hint. Any other framework fails with `no automate support found for framework <name>`. Playwright's built-in skeleton is a deliberate one-file-per-TC starter; richer append-mode Playwright/Pester integration (multiple TCs sharing one artefact) is future work.

#### Per-framework skeleton templates

The built-in `agent-automate` / `manual-automate` adapters do NOT hardcode the
skeleton body. Each supported framework ships a user-customisable template file
that `gtms init` writes to disk, and the adapter reads it at automate time:

| Framework | Template file | Placeholders | Built-in fallback |
|-----------|--------------|--------------|-------------------|
| bats | `gtms/automation/templates/bats.template.bats` | `${TESTCASE_ID}`, `${PROJECT_ROOT_DEPTH}` | compiled-in default |
| playwright | `gtms/automation/templates/playwright.template.spec.ts` | `${TESTCASE_ID}` (only) | compiled-in default |

- `${TESTCASE_ID}` -- the tc-ID, substituted for every framework.
- `${PROJECT_ROOT_DEPTH}` (BATS only) -- the `../..` chain that walks from the
  artefact's directory back up to the project root; computed from the artefact's
  subfolder depth so the skeleton resolves `PROJECT_ROOT` at any nesting level.
  Playwright has no depth placeholder (Node resolves the root via `playwright.config.ts`).

**Read-with-fallback.** At automate time the adapter reads the on-disk template. If
the file is absent, it falls back to the compiled-in default (identical bytes) and
prints a stderr warning naming the project-relative template path. Any *other* read
error (permission denied, is-a-directory) is fatal so the operator can fix it. Edit
the on-disk file to customise the skeleton your team stamps; delete it and you get
the built-in default back.

**Scaffolding.** The template ships only with the matching preset: the `bats` preset
installs `bats.template.bats`, the `playwright` preset installs
`playwright.template.spec.ts`. The `manual` preset installs neither (manual TCs have
no automate stage).

#### The `pending → <real hash>` bootstrap

`PendingArtefactHash` (literal `pending`) is the single permitted sentinel value in the wiring schema. It exists because the built-in automate writes the skeleton *before* the agent fills the `@test` body -- hashing the empty skeleton at write time would guarantee a stale-wiring error on the very first `gtms execute`.

The bootstrap mechanism, implemented in `internal/cli/execute.go` `bootstrapPendingWiring`:

1. `gtms execute` reads the wiring record, sees `artefact-hash: pending`.
2. Computes the current artefact hash via `pipeline.HashFile`.
3. Writes the updated wiring back via `wiring.Write` (atomic temp-file + rename — see below).
4. Updates the in-memory wiring record so the downstream drift check sees the real hash.
5. Proceeds to invoke the canonical execute adapter normally.

**Contract guarantees** — these matter for both implementation and BATS test authorship:

| Guarantee | Source |
|-----------|--------|
| Bootstrap runs **before** the drift check and **before** `--allow-stale` evaluation | `internal/cli/execute.go` (single-TC path), `runBulkExecute` (bulk parity) |
| `gtms execute` may only transition `pending → <real hash>` — never overwrites a non-sentinel value | `bootstrapPendingWiring` guard + `tc-d596e69e-*.bats` |
| Missing or unhashable artefact while pending → exit non-zero, **no** wiring mutation, **no** adapter invocation, **no** `.gtms/results/` file | `tc-83f46d85-*.bats` |
| Write-back failure during bootstrap → exit non-zero, wiring stays pending on disk (atomic write) | `internal/wiring/wiring.go` `Write` |
| `--allow-stale` does NOT bypass the bootstrap | `tc-14399830-*.bats` |
| `wiring.Find` / `Read` reject `testcase-hash: pending` — only `artefact-hash` ever carries the sentinel | `wiring.validate()` |
| Reader classification never treats pending as stale (table view shows wired, not stale) | `internal/reader/picker.go` `ClassifyWiring`, `tc-c04895ed-*.bats` |

**Known gap:** the JSON / detail-view exposure of the pending state -- required by an acceptance criterion -- is **not** implemented. `gtms status --json`, `gtms gaps --json`, and `gtms map --json` surface only implicit signals (`wired: true`, empty `wiring_drift`, absent `last_result_here`). A typed field name is a future decision.

#### Atomic wiring writes

`wiring.Write` (`internal/wiring/wiring.go`) writes through a sibling temp file with fsync + rename, not a direct `os.WriteFile`. This upholds the bootstrap contract: a disk-full or interrupted bootstrap write-back can never leave a truncated wiring file on disk -- the prior pending state is preserved and the next execute can retry cleanly. The rename is atomic on POSIX and Windows (Go's stdlib uses `MOVEFILE_REPLACE_EXISTING`).

The atomic pattern is now the universal `wiring.Write` behaviour, not bootstrap-specific. Any caller — Tier 0 built-ins, the pipeline's `WriteAutomateWiring`, `gtms link` — benefits from the same crash-safety.

#### Pre-flight ordering: resolve, then write

`BuiltinAutomate` (`internal/adapter/builtin_action.go`) resolves the canonical execute adapter and computes `testcase-hash` **before** writing any artefact. Spec intent: a missing canonical execute adapter for the framework, or a TC spec that can't be hashed, must fail the command without leaving an orphan `.bats` file on disk.

Lesson for adapter authors writing similar built-ins: do all hard preconditions (config resolution, hash computation, path validation) before any filesystem mutation. The ordering means a precondition failure leaves the project state untouched — a property BATS regression depends on (e.g. `tc-facd66a7-*.bats` asserts both `assert_failure` AND `[ ! -f "$artefact_path" ]`).

#### Self-skip guard scope (`WriteAutomateWiring`)

When a built-in automate runs synchronously, `WriteAutomateWiring` is called twice: once internally by `BuiltinAutomate` (writing the pending wiring), and again via `handleSyncResult` → `buildPipelineRecords`. Without a guard, the second call would overwrite the sentinel with a real hash computed from the empty skeleton — defeating the bootstrap design.

The self-skip in `WriteAutomateWiring` (`internal/adapter/wiring.go`) is intentionally narrow:

```go
if isBuiltinAutomateAdapter(tf.Adapter) {
    existing, _, findErr := wiring.Find(projectRoot, tf.Target, tf.Framework)
    if findErr == nil && existing != nil &&
        wiring.IsPendingArtefactHash(existing.ArtefactHash) &&
        existing.Artefact == filepath.ToSlash(rc.Artefact) {
        return nil, nil
    }
}
```

Two conditions, both required:

- **Adapter name** is in the closed Tier 0 automate table (`isBuiltinAutomateAdapter` reads `builtinActionAdapters["automate"]`). Tier 1/2 adapters never produce pending wiring, so a Tier 1/2 run on a pending scaffold falls through the guard and writes its own wiring — the user can manually upgrade a Mode 3 scaffold to a Tier 1/2 generator without the wiring getting stuck pointing at the stale skeleton path.
- **Artefact path matches** the existing wiring's `artefact:` field. If the new call targets a different artefact path (e.g. an adapter that writes under a different folder), the guard doesn't fire — the new wiring is written and overrides the stale skeleton wiring.

Tier 1/2 adapter authors don't need to do anything special; their writes flow through unchanged.

#### Closed built-in name tables

`agent-automate` and `manual-automate` are registered in two closed tables:

- `internal/adapter/resolver.go` — `builtinActionAdapters["automate"]` (resolver Tier 0 fallback)
- `internal/config/config.go` — config validation accepts them as `defaults.automate:` values without requiring a matching `adapters.automate.*` entry

Resolution order is unchanged: a config-defined adapter with the same name wins. Setting `defaults.automate: agent-automate` in `gtms.config` with no `adapters.automate.*` block is the zero-config Mode 3 path (`tc-7e825cb5-*.bats`).

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
artefact: gtms/test/cases/tc-f1a2b3c-login.md
EOF
```

**Pros:** Simple, adapter has full control. Works for downloading remote artefacts (CI results, etc.).
**Cons:** Adapter needs file-write permissions. For AI tools like Claude Code in `-p` mode, this requires `--dangerously-skip-permissions` (no TTY for permission prompts).

### Approach 2: Stdout Streaming — GTMS Writes Files (Recommended)

**Status: Implemented.** Reviewed in [REV-004](../PRPs/code_reviews/REV-004-streaming-stdout.md).

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
- The result contract `artefact` field is auto-populated with project-relative paths. Comma-separated multi-path is legitimate only on `create`. On `automate`, GTMS rejects multi-file streaming output at automate time -- the task fails with `status: error` and no automation record is written, so a comma-separated `artefact:` never ships in a record GTMS writes.

**Impact on adapter development:**
- Adapters become pure content producers — they output text, GTMS handles file I/O
- Prompt templates should instruct the AI to use the `<gtms-file>` tag format
- Adapters that write files directly (Approach 1) continue to work unchanged

### Recommendation

**Prefer stdout streaming** (Approach 2) for new adapters. It's more secure (no file-write permissions needed), more resilient (crash recovery — completed files survive), and works identically across tiers.

Approach 1 remains appropriate for adapters that download artefacts from external systems (CI results, remote files) where the adapter genuinely needs filesystem access.

---

## Subdirectory Routing

**Status: Implemented.**

When a test case lives in a subdirectory under `gtms/test/cases/` (e.g. `gtms/test/cases/widgets/tc-abc.md`), GTMS automatically routes streamed output files to the matching subdirectory under the output directory. The adapter does not need to know or care about the subdirectory — GTMS handles it.

### How it works

1. GTMS detects the test case's subdirectory relative to `gtms/test/cases/` and sets `OutputSubdir` (e.g. `widgets/`)
2. When the adapter streams output via `<gtms-file>` tags, GTMS writes files to `OutputDir + OutputSubdir` (e.g. `gtms/automation/specs/widgets/`)
3. The adapter emits **bare filenames only** in `<gtms-file>` tags — no directory prefixes

### Example

Test case at `gtms/test/cases/widgets/tc-abc-login.md`:

```
Adapter outputs:     <gtms-file name="tc-abc-login.bats">
GTMS writes to:      gtms/automation/specs/widgets/tc-abc-login.bats
```

Test case at `gtms/test/cases/tc-abc-login.md` (root level):

```
Adapter outputs:     <gtms-file name="tc-abc-login.bats">
GTMS writes to:      gtms/automation/specs/tc-abc-login.bats
```

### Common mistake: double-nesting

If an adapter includes the subdirectory in the `<gtms-file>` filename (e.g. `<gtms-file name="widgets/tc-abc.bats">`), the file will be written to `gtms/automation/specs/widgets/widgets/tc-abc.bats` — double-nested. Prompt templates must instruct adapters to output bare filenames.

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
| **Short** | `{reference}`, `{testcase}`, `{focus}`, `{task_id}`, `{branch}`, `{repo}`, `{output_dir}`, `{artefact_file}`, `{testcase_file}`, `{prompt_template}`, `{context_file}`, `{prompt_file}`, `{tc_ids}`, `{tc_name}`, `{output_subdir}`, `{environment}` | Under 200 chars |
| **Unbounded** | `{context}`, `{guides}`, `{prompt}`, `{testcase_content}` | Hundreds to thousands of lines |

> **Note:** `{framework}` is available in **prompt templates** (via the prompt assembler in `invoker.go`) but is **not** a Tier 1 command template variable or Tier 2 environment variable. It does not appear in the `tier1.go` vars map or the `tier2.go` env var list. Use it in prompt templates to specify the target test framework (e.g. "playwright", "pytest"). The value comes from the `--framework` flag or the adapter's `framework` config field — see [Framework Resolution Order](#framework-resolution-order).

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
<guide name="gtms-test-case-authoring-guide.md">
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

**Source material containing `<gtms-file>` examples (discovered during dogfooding):**

When the `{context}` variable contains documentation that itself shows `<gtms-file>` tag examples (e.g. an enhancement doc describing the streaming format), the AI adapter will reproduce those examples as actual output file blocks. The adapter sees `<gtms-file name="example.bats">` in the source material and outputs it as a real file block — producing junk `.bats`, `.xml`, and other non-test-case files alongside the intended `.md` test case specs.

**Fix:** Output rules must explicitly constrain file types and instruct the adapter not to reproduce examples:
```xml
<output_rules>
- ONLY output .md test case specification files
- Do NOT reproduce example files, code snippets, or sample filenames from the source material
- Each file uses <gtms-file name="tc-{hex}-{slug}.md">...</gtms-file> tags
</output_rules>
```

**Test case ID collisions from AI-generated output:**
AI models reuse hex values from context rather than generating unique ones. If the source material contains `tc-a1b2c3d` as an example, the adapter may use it as a real test case ID, colliding with existing test cases. Use the `{tc_ids}` variable (create command) — GTMS pre-generates 20 unique IDs via `crypto/rand` and the prompt template should instruct the adapter to use them.

**Filename-to-frontmatter ID integrity is enforced:**
GTMS hands the adapter the batch of pre-generated IDs and the prompt template carries the contract ("use these IDs in order, do not invent, filename ID must match frontmatter `test_case_id`"). GTMS now **verifies it**: the create-path validator (see [Create Validation Contract](#create-validation-contract)) runs after the files land and **fails the task**, naming the file, if a filename ID and its frontmatter `test_case_id` diverge or the field is missing. The [Post-Fill Validation Gate](#post-fill-validation-gate) re-checks the same invariant at the entry of `automate` / `prime` / `execute`. There is no silent-corruption window on the shipped path -- a mismatch stops the pipeline instead of quietly producing a TC that never joins.

Adapter authors should still make the prompt rule emphatic so the AI gets it right the first time rather than failing the task:

```xml
<output_rules>
- Use the pre-generated test case IDs in order, one per test case: {tc_ids}
- Do NOT invent your own IDs -- use only the IDs provided above
- CRITICAL: frontmatter `test_case_id` MUST be byte-for-byte identical to the filename ID.
  Example: file `tc-a3f72b10-login.md` must carry `test_case_id: tc-a3f72b10`.
</output_rules>
```

If a mismatch does slip through an out-of-band edit, rename the spec file to match the frontmatter ID -- frontmatter is the canonical key for the pipeline join, so frontmatter wins.

**Multi-file automate output:**
`gtms automate` expects exactly one `<gtms-file>` tag per invocation. An AI that emits a second tag for a helper or fixture file (instead of using the framework's shared-helper module) will cause the task to fail with `status: error` at automate time — no automation record is written. Make the one-file rule explicit in automate prompt templates, not implicit. Example:

```xml
<output_rules>
- Emit exactly ONE <gtms-file> tag per invocation.
- Shared helpers belong in common-setup.bash (BATS) or GtmsTestHelper.psm1 (Pester), NOT in a second tag.
- GTMS rejects multi-file automate output at automate time.
</output_rules>
```

This rule is automate-specific. `create` legitimately emits many `<gtms-file>` tags (one per test case) and is unaffected by the guard.

**Using `{prompt}` in command templates for large prompts:**
```yaml
command: 'claude -p {prompt}'
```
The `{prompt}` variable inlines the entire assembled prompt as a command-line argument. This hits OS limits (~32K on Windows). Use `{prompt_file}` instead.

**Incomplete BATS boilerplate in automate prompt templates:**
AI-generated BATS files consistently have three boilerplate issues: `PROJECT_ROOT` not exported (invisible in `setup()` subshell), depth hardcoded to `/../..` (breaks for subdirectory tests at `test/acceptance/{work-item}/`), and relative `load` paths (break at different depths). The automate prompt template must include the exact correct pattern:

```bash
# CORRECT setup_file() boilerplate for BATS:
setup_file() {
    local dir="$( cd "$( dirname "$BATS_TEST_FILENAME" )" && pwd )"
    while [ ! -f "$dir/gtms.config" ] && [ "$dir" != "/" ]; do
        dir="$(dirname "$dir")"
    done
    export PROJECT_ROOT="$dir"
}

setup() {
    load "$PROJECT_ROOT/test/test_helper/common-setup"
    _common_setup
}
```

Key points: `export` is required (without it, `PROJECT_ROOT` is invisible in `setup()` subshells); the `gtms.config`-walking loop works at any depth; `load` uses `$PROJECT_ROOT` for an absolute path.

**Project root depth calculation — cross-framework pattern:**
Every test framework needs to resolve the project root from the test file's location. AI consistently gets the depth wrong when tests live in subdirectories. This is the single most common automation bug across all frameworks:

| Framework | Root resolution strategy | Subdirectory gotcha |
|-----------|------------------------|---------------------|
| **BATS** | Walk up to `gtms.config` (dynamic) | Solved — loop works at any depth |
| **Pester** | `$PSScriptRoot` + relative `..` (static) | Must count `..` levels: `test/pester/` = 2, `test/pester/subdir/` = 3 |
| **Playwright** | Resolved by Node via `playwright.config.ts` | `testDir` must include spec directory |
| **pytest** | Resolved by pytest via `conftest.py` | `conftest.py` must be at the right level |

The automate prompt template must call out the depth calculation explicitly, with examples for both root-level and subdirectory test files. BATS solved this with a dynamic `gtms.config`-walking loop — consider whether your framework can do something similar rather than hardcoding relative depth.

**Helper/module import paths break at the same time.** When the depth is wrong, the import path for shared helpers is also wrong. If your framework uses relative imports (e.g. Pester's `Import-Module "$PSScriptRoot\Helper.psm1"`), the path changes when the test moves to a subdirectory (`$PSScriptRoot\..\Helper.psm1`). The automate prompt template must show both patterns — root-level and subdirectory — or AI will hardcode the root-level path every time.

**Exact version pinning in generated tests:**
AI-generated tests often pin the exact framework version used during generation (e.g. Pester's `#Requires RequiredVersion = '5.6.1'`, or a `package.json` with `"jest": "29.7.0"`). When a user has a different patch version installed, the test fails before it even runs — with an error that looks like a missing dependency, not a version mismatch. Use **minimum version** constraints instead (e.g. `ModuleVersion = '5.4.0'`, `"jest": ">=29.0.0"`). The automate prompt template should specify this explicitly, or AI will default to exact pinning.

**AI-generated assertions targeting the wrong output line:**
When testing `gtms status tc-XXX` (detail view), the output has a header line (`tc-XXX  slug: title`) followed by detail lines (`EXECUTE:    ✓ (Pass)`). Auto-generated BATS tests may grep for "Pass" on the header line, which doesn't contain it. Always verify assertion targets against actual `gtms status` output. Use `assert_output --partial "Pass"` against the full output rather than grepping a specific line.

**Using Tier 2 `script:` adapters in BATS test fixtures:**
When a BATS test creates a fixture with a Tier 2 `script:` path pointing to a BATS temp directory, the invoker's `filepath.Join(projectRoot, scriptPath)` doubles the path if it's absolute. Use a Tier 1 `command: 'bash script.sh'` adapter with the script placed inside the fixture directory instead — simpler and avoids the path-joining issue.

### References

- [Lost in the Middle (Liu et al., 2024)](https://arxiv.org/abs/2307.03172) — U-shaped attention in LLMs
- [Anthropic Long Context Tips](https://docs.anthropic.com/en/docs/build-with-claude/prompt-engineering/long-context-tips) — Data at top, instructions at end
- [OpenAI GPT-4.1 Guide](https://cookbook.openai.com/examples/gpt4-1_prompting_guide) — Last instruction wins with competing directives

---

## Walkthrough: Building Adapters

**Prerequisites:** Your project needs a `gtms.config` and the GTMS folder structure (`gtms/tasks/`, `gtms/test/`, `gtms/automation/`, `.gtms/`). Run `gtms init` to set this up.

### Tier 2 Example: bats-runner Execute Adapter

This is the real bats-runner adapter currently registered in the project's `gtms.config`. It moved from Tier 1 (`command: 'bats {artefact_file}'`) to Tier 2 -- the new shape lets the adapter classify TAP output itself (pass / fail / skipped / error) rather than relying on core to sniff the stream.

**Step 1: Understand what your tool needs.** bats-runner is an execute adapter — it runs `.bats` test files. The BATS runner takes a single argument: the path to the file. GTMS provides this via `$GTMS_ARTEFACT_FILE` (Tier 2 env var equivalent of Tier 1's `{artefact_file}` substitution).

**Step 2: Write the config entry.**

```yaml
adapters:
  execute:
    bats-runner:
      mode: sync
      script: gtms/adapters/bats-runner.sh
      framework: bats
      output-dir: test/acceptance
```

`gtms init --preset bats` registers this entry by default and ships `gtms/adapters/bats-runner.sh` + `gtms/adapters/lib/bats-tap.sh` (the shared TAP classifier sourced by the wrapper).

**Step 3: The wrapper script** -- this is the shipped `bats-runner.sh`. It is
`#!/bin/sh` (GTMS runs Tier 2 via `sh <path>`, so POSIX only), sources the shared
classifier, scrubs leaked `BATS_*` env, checks the TAP plan line before trusting
the output, classifies, and writes the orthogonal contract -- emitting
`result:` only when it is non-empty:

```sh
#!/bin/sh
set -e

# Source the shared TAP classifier (moved out of Go core).
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
. "$SCRIPT_DIR/lib/bats-tap.sh"

# Scrub leaked BATS_* env from an enclosing bats run: a parent bats prepends
# $BATS_LIBEXEC to PATH, so `bats` would resolve to the libexec entry script
# (fails with "bats_readlinkf: command not found"). Strip both before running.
unset BATS_LIBEXEC BATS_ROOT BATS_CWD BATS_TMPDIR BATS_ROOT_PID BATS_VERSION
PATH=$(printf '%s' "$PATH" | tr ':' '\n' | grep -vE '/libexec/bats-core/?$' | tr '\n' ':' | sed 's/:$//')
export PATH

set +e
OUTPUT=$(bats "$GTMS_ARTEFACT_FILE" 2>&1)
EXIT_CODE=$?
set -e

# "Did bats run at all?" -- a valid TAP stream has a plan line 1..N.
HAS_PLAN=0
if echo "${OUTPUT}" | grep -Eq '^1\.\.[0-9]+$'; then HAS_PLAN=1; fi

if [ "${HAS_PLAN}" = "0" ]; then
    STATUS="error"; RESULT=""
    SUMMARY="Malformed or missing TAP output (exit ${EXIT_CODE})"
else
    CLASSIFIED=$(echo "${OUTPUT}" | classify_bats_status)   # pass|fail|skipped|error
    case "$CLASSIFIED" in
        pass)    STATUS="complete"; RESULT="pass" ;;
        fail)    STATUS="complete"; RESULT="fail" ;;
        skipped) STATUS="complete"; RESULT="skip" ;;
        error)   STATUS="error";    RESULT="" ;;
    esac
fi

# Orthogonal contract: emit result: only when non-empty.
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

echo "${OUTPUT}"

# Exit 0 only for a completed adapter run; 1 for error.
[ "${STATUS}" = "complete" ] && exit 0 || exit 1
```

Teaching points: `#!/bin/sh` POSIX-only; the `BATS_*` + libexec PATH scrub and
*why*; the `lib/bats-tap.sh` shared classifier; the `^1\.\.[0-9]+$`
plan-line gate ("did the framework run at all"); and `result:` emitted only when
non-empty.

**Step 4: Test it.**

```bash
gtms execute tc-xxx --adapter bats-runner
```

Check: task file in `gtms/tasks/complete/` (any completed run, including a
`result: fail`) or `gtms/tasks/error/` (adapter couldn't run), result contract in
`.gtms/results/task-{8hex}.handoff.yaml` with `status: complete` (+ `result: pass|fail|skip`) or `status: error`.

**What GTMS does behind the scenes:** Resolve adapter -> generate task ID -> create task file -> create result contract -> build context (`GTMS_ARTEFACT_FILE` from the wiring record) -> invoke `sh gtms/adapters/bats-runner.sh` with `GTMS_*` env vars -> adapter writes the contract -> GTMS reads it, persists `status`/`result` without override, maps to the pipeline record and moves the task file.

### Tier 2 Example: playwright-runner Execute Adapter (deliberately minimal)

The shipped `playwright-runner.sh` (from `gtms init --preset playwright`) is
intentionally skeletal: it runs a single TC-specific `.spec.ts` and maps
the exit code, with no JUnit parse and no append mode. It is the reference
for a *minimal* runner -- and for the missing-tooling diagnostic pattern:

```sh
#!/bin/sh
# playwright-runner.sh -- Tier 2 sync execute adapter. Single TC-specific spec,
# no JUnit parse, no append mode.
set -e

# Missing-tooling diagnostic. gtms init does NOT install Node/Playwright. The
# diagnostic is DUPLICATED into the result contract summary because the adapter's
# own stderr is captured but not threaded to the parent gtms process --
# writing it to the contract is how it surfaces via the execute output.
if ! command -v npx >/dev/null 2>&1; then
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

set +e
OUTPUT=$(npx playwright test "${GTMS_ARTEFACT_FILE}" 2>&1)
EXIT_CODE=$?
set -e

# Simple exit-code mapping. A minimal runner omits JUnit parsing, so it cannot
# distinguish "5 passed, 1 failed" from a clean fail: exit 0 -> pass, else fail.
if [ "$EXIT_CODE" -eq 0 ]; then
  STATUS="complete"; RESULT="pass"; SUMMARY="Playwright test passed"
else
  STATUS="complete"; RESULT="fail"; SUMMARY="Playwright test failed (exit ${EXIT_CODE})"
fi

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
```

Config (the `playwright` preset): `script: gtms/adapters/playwright-runner.sh`,
`framework: playwright`, `output-dir: gtms/scripts/playwright`, `artefact-glob:
"gtms/scripts/playwright/**/{testcase}*.spec.ts"`. What a minimal runner omits: a
JUnit parse (so no per-test tally in the `summary:`) and append mode (so one
spec file per TC, not multiple TCs sharing a file). To distinguish a partial fail or
extract counts, add JUnit parsing -- see the pattern below.

> **Why Tier 2 for BATS specifically?** Exit code alone can't distinguish "all tests skipped" from "all tests passed" -- bats exits 0 in both cases. The earlier Tier 1 form (`command: 'bats {artefact_file}'`) needed core to sniff stdout for `# skip` markers. That core sniff was retired and the classification pushed into the adapter; Tier 2 is where the classification logic lives. **General lesson**: when your tool's exit code under-determines the outcome (skip/pass conflated, partial success, conditional pass), reach for Tier 2 with explicit `status:` writes.

### Tier 2 Example: Script Adapter

When a single command isn't enough, Tier 2 gives you full scripting power. The script receives context as `GTMS_*` environment variables and can update the result contract directly.

```sh
#!/bin/sh
cat > "${GTMS_RESULT_FILE}" <<EOF
task: ${GTMS_TASK_ID}
command: ${GTMS_COMMAND}
target: ${GTMS_TESTCASE:-${GTMS_REFERENCE}}
adapter: my-adapter
mode: sync
created: $(date -u +%Y-%m-%dT%H:%M:%SZ)
status: complete
result: pass
artefact: output.md
attempts: 1
summary: "Work completed"
completed: $(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF
```

Config:
```yaml
adapters:
  create:
    my-adapter:
      mode: sync
      script: gtms/adapters/my-adapter.sh
```

**Important:** If your script uses `cat >` to overwrite the result contract, include ALL fields — the GTMS-prepopulated values are gone. If you only need pass/fail, skip the result contract and just `exit 0` or `exit 1` — GTMS handles the rest.

**When you need Tier 2:** GitHub Actions triggers (start workflow, capture run ID), multi-tool pipelines (linter -> generator -> formatter), custom result parsing (JUnit XML), retry logic -- and **execute adapters that need to distinguish a test failure (`result: fail`) from an adapter error (`status: error`)** (see [Status and Result Values](#status-and-result-values-enh-130-orthogonal-contract) for the pattern).

### Parsing Test Framework Output (Tier 2)

Execute adapters often need to extract a summary from framework-native output (JUnit XML, NUnit XML, TAP). Here's a pattern for JUnit/NUnit XML -- the most common format. (The shipped `playwright-runner.sh` deliberately does NOT do this; add it when you want a per-test tally rather than a bare pass/fail.)

```sh
#!/bin/sh
# Run the test framework, capture output
npx playwright test "${GTMS_ARTEFACT_FILE}" --reporter=junit > "${TMPDIR}/results.xml" 2>&1
EXIT=$?

# Parse JUnit XML for summary (tests, failures, errors)
if [ -f "${TMPDIR}/results.xml" ]; then
    TESTS=$(grep -oP 'tests="\K[0-9]+' "${TMPDIR}/results.xml" | head -1)
    FAILURES=$(grep -oP 'failures="\K[0-9]+' "${TMPDIR}/results.xml" | head -1)
    ERRORS=$(grep -oP 'errors="\K[0-9]+' "${TMPDIR}/results.xml" | head -1)
    SUMMARY="${TESTS:-0} tests, ${FAILURES:-0} failures, ${ERRORS:-0} errors"
else
    SUMMARY="Test framework produced no XML output"
fi

# Map to the orthogonal contract
if [ "$EXIT" -eq 0 ]; then
    STATUS="complete"; RESULT="pass"
elif [ -n "$TESTS" ] && [ "${TESTS:-0}" -gt 0 ]; then
    STATUS="complete"; RESULT="fail"   # framework ran tests but some failed
else
    STATUS="error"; RESULT=""          # framework couldn't run (syntax error, missing dep)
fi

cat > "${GTMS_RESULT_FILE}" <<EOF
task: ${GTMS_TASK_ID}
command: execute
target: ${GTMS_TESTCASE}
adapter: my-runner
mode: sync
created: $(date -u +%Y-%m-%dT%H:%M:%SZ)
status: ${STATUS}
result: ${RESULT}
artefact: ${TMPDIR}/results.xml
summary: "${SUMMARY}"
completed: $(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF
exit $EXIT
```

The key insight: if the XML contains a `tests` count > 0, the framework ran — so a non-zero exit is a test failure (`fail`), not an infrastructure error (`error`).

**XML format by framework** — the attribute names differ:

| Framework | XML format | Total tests | Failures | Errors |
|-----------|-----------|-------------|----------|--------|
| **Playwright** (`--reporter=junit`) | JUnit | `tests="N"` | `failures="N"` | `errors="N"` |
| **pytest** (`--junitxml`) | JUnit | `tests="N"` | `failures="N"` | `errors="N"` |
| **Jest** (`jest-junit`) | JUnit | `tests="N"` | `failures="N"` | `errors="N"` |
| **Pester** (`-CI` or NUnitXml) | NUnit | `total="N"` | `failed="N"` | — |
| **Go test** (`-json` + gotestsum) | JUnit | `tests="N"` | `failures="N"` | `errors="N"` |
| **Newman/Postman** | JUnit | `tests="N"` | `failures="N"` | `errors="N"` |

Adapt the `grep` pattern accordingly — e.g. for NUnit: `grep -oP 'total="\K[0-9]+'` instead of `tests="`.

---

## Testing Adapters

### Manual testing

```bash
gtms create my-feature --adapter my-adapter --reference REQ-001   # create
gtms automate tc-f1a2b3c --adapter my-adapter                     # automate
gtms execute tc-f1a2b3c --adapter my-adapter                      # execute
```

### What to verify

1. **Task file lifecycle:** file moved from `gtms/tasks/pending/` to `complete/` (success or clean test failure) or `error/` (adapter/runtime/validation error)
2. **Result contract:** `.gtms/results/task-{id}.handoff.yaml` — check `status`, `artefact`, `summary`, `completed`
3. **File output:** create → `gtms/test/cases/`, automate → `gtms/automation/specs/{adapter}/`, execute → `results/`

### How to debug

- `.gtms/results/` — result contract shows exactly what GTMS recorded
- `.gtms/tmp/` — assembled prompt files (verify prompt assembly)
- `gtms/tasks/error/` — task file `error:` frontmatter field has the failure reason
- Tier 1 stderr is captured in the result contract summary on failure

### Writing BATS acceptance tests with mock adapters

The project has a BATS test infrastructure at `test/acceptance/`. The `create_gtms_fixture()` helper creates an isolated test project in a temp directory:

```bash
@test "my adapter succeeds" {
    local dir
    dir="$(create_gtms_fixture "$(cat <<CONF
project:
  name: test-project
  repo: test/repo
adapters:
  execute:
    my-adapter:
      mode: sync
      command: 'echo "test passed"'
defaults:
  execute: my-adapter
CONF
)")"
    cd "$dir"

    run gtms.exe execute tc-f1a2b3c --adapter my-adapter
    assert_success
    assert_output --partial "task-"
}
```

Mock adapter scripts in `test-fixtures/`: `mock-adapter-success.sh`, `mock-adapter-fail.sh`, `mock-adapter-streaming.sh`, `mock-adapter-tier2.sh`, `mock-adapter-async.sh`, `mock-adapter-slow.sh`, `mock-adapter-slow-fail.sh`, `mock-adapter-slow-gated.sh`.

#### Mock adapter config checklist

When writing BATS tests with mock adapters, getting the `gtms.config` fixture right is the single most common source of test failures. Every field below exists for a reason — omitting any one produces a test that runs but checks the wrong paths, can't find records, or silently passes when it shouldn't.

**1. Always set `output-dir` explicitly.**

Without `output-dir`, GTMS writes to its defaults: `gtms/test/cases/<folder>/` for create, `gtms/automation/specs/<adapter-name>/` for automate. If your test pre-creates files or checks results at a specific path (e.g. `test/acceptance/my-feature/`), the mock must match:

```yaml
adapters:
  automate:
    mock-automate:
      mode: sync
      command: 'echo "mock"'
      output-dir: test/acceptance    # ← match where your test checks
```

Without this, the adapter writes to `gtms/automation/specs/mock-automate/` and your assertions against `test/acceptance/` find nothing.

**2. Use the correct template variables for the command.**

Template variables are command-specific. The most common mistake is using `{reference}` (create-only) in an automate adapter, or `{testcase}` in a create adapter. Both resolve to empty strings for the wrong command — the adapter runs but receives blank values.

| Command | Key variables | Common mistake |
|---------|--------------|----------------|
| create | `{reference}`, `{focus}`, `{tc_ids}` | Using `{testcase}` (empty for create) |
| automate | `{testcase}`, `{testcase_content}`, `{testcase_file}` | Using `{reference}` (empty for automate) |
| execute | `{testcase}`, `{artefact_file}`, `{testcase_file}` | Using `{reference}` (empty for execute) |

See the full [template variable table](#template-variables) for all variables and which commands populate them.

> GTMS warns at invocation time if you use a token it does not recognise, or if you wrap a recognised placeholder in shell quotes. See [Two invocation-time template warnings](#two-invocation-time-template-warnings).

**3. Set `framework` when chaining automate → execute.**

The `framework` field in the adapter config determines the wiring record filename (`tc-xxx--{framework}.wiring.yaml`). When execute looks up the record, it searches by framework. If the automate and execute adapters resolve to different framework values, execute can't find the record.

Framework is resolved as: `--framework` CLI flag → config `framework:` field → adapter name. If your automate adapter is named `mock-automate` and your execute adapter is named `mock-runner`, the frameworks won't match unless you set them explicitly:

```yaml
adapters:
  automate:
    mock-automate:
      mode: sync
      command: 'bash mock-stream.sh'
      framework: mock              # ← explicit framework
      output-dir: test/acceptance
  execute:
    mock-runner:
      mode: sync
      command: 'echo "pass"'
      framework: mock              # ← must match automate's framework
defaults:
  automate: mock-automate
  execute: mock-runner
```

**4. Streaming mock adapters must emit TC-ID-prefixed filenames.**

The shared `mock-adapter-streaming.sh` outputs generic filenames (`tc-001-login.md`, `tc-002-logout.md`). These don't match the TC IDs in your test fixture, so assertions that check for files by TC ID prefix will fail. For tests that verify file-level behaviour, use an inline mock that emits the correct TC ID:

```bash
# Inline mock that streams a file matching the test's TC ID
local mock_script="$BATS_TEST_TMPDIR/mock-stream.sh"
cat > "$mock_script" <<'SCRIPT'
#!/bin/bash
echo "Summary: generated 1 file"
echo '<gtms-file name="tc-aaa00010-my-test.bats">'
echo '#!/usr/bin/env bats'
echo '@test "placeholder" { true; }'
echo '</gtms-file>'
SCRIPT
```

Use the shared `mock-adapter-streaming.sh` only when you're testing GTMS orchestration (task files, result contracts) and don't care about specific output filenames.

**5. Write fixture files directly for chained-command tests.**

Mock adapters produce task files and result contracts but not real pipeline artifacts (test case specs, wiring records, artefact files). Tests that chain commands -- e.g. automate then execute -- must create the intermediate fixtures by hand:

```bash
# Create the wiring record fixture (required before gtms execute)
mkdir -p gtms/automation/wiring
cat > gtms/automation/wiring/tc-aaa00010--mock.wiring.yaml <<'EOF'
testcase: tc-aaa00010
testcase-hash: 4f2a9c1b7e6d0a35
framework: mock
adapter: mock-automate
artefact: test/acceptance/my-feature/tc-aaa00010-sample.bats
artefact-hash: pending
EOF

# Create the artefact file (execute verifies it exists)
mkdir -p test/acceptance/my-feature
echo '#!/usr/bin/env bats' > test/acceptance/my-feature/tc-aaa00010-sample.bats

git add -A && git commit -m "add fixtures"
```

See the [execute fixture checklist](#cross-framework) for the three things that must be correct: a wiring record exists for the TC and framework, its `framework:` matches the execute adapter, and the artefact file exists on disk.

#### Quick reference: common test failures and fixes

| Symptom | Cause | Fix |
|---------|-------|-----|
| Adapter runs but 0 files found at expected path | Missing `output-dir` in config | Add `output-dir:` matching your assertion path |
| Adapter command receives blank values | Wrong template variable for command | Use `{testcase}` for automate/execute, `{reference}` for create |
| Execute says `No wiring record found for '<tc>' (framework: <fw>)` | No wiring record for that TC+framework, or a framework mismatch between adapters | Create the `<tc>--<fw>.wiring.yaml` fixture (or run `gtms link`); set `framework:` explicitly on both automate and execute adapters |
| Streaming test checks wrong filename | Used shared mock-adapter-streaming.sh | Write inline mock with correct TC-ID-prefixed filename |
| Execute skips with "stale wiring" | The spec or artefact changed since the wiring record was written | `gtms link --refresh <tc>` to re-acknowledge (or refresh the fixture's hashes) |
| Execute says "artefact file not found" | Artefact path in the wiring record doesn't exist on disk | Create the file at the path in the wiring record's `artefact:` field |
| Automation record `executed_artefact:` stays empty after a Tier 1 mock execute | **Obsolete after the wiring cutover** -- `pipeline.UpdateExecutionResult` no longer runs on the execute path. `executed_at:` / `executed_artefact:` are not written to the automation record. The execute-timestamp substrate is `.gtms/results/<task>.handoff.yaml` `completed:`, which comes from `rc.Completed` regardless of whether `rc.Artefact` is populated. Tier 1 mocks no longer need to populate `rc.Artefact` for timestamp reasons. | Tier 1 mocks may still need to create files when an assertion depends on the result-file path (`rc.Artefact` flows through to the handoff `artefact:` field), but `command: 'echo PASS'` is now sufficient when the assertion is on the handoff `completed:` or `frameworks[].last_executed_here`. |

---

## Deployment

### Building the binary

GTMS is a single Go binary with no runtime dependencies (no Go installation required on the target machine).

```bash
go build -o gtms ./cmd/gtms       # Linux/macOS
go build -o gtms.exe ./cmd/gtms   # Windows (including Git Bash / MINGW)
```

**Cross-compile:**
```bash
GOOS=linux GOARCH=amd64 go build -o gtms ./cmd/gtms         # Linux x86_64
GOOS=darwin GOARCH=arm64 go build -o gtms ./cmd/gtms         # macOS Apple Silicon
GOOS=windows GOARCH=amd64 go build -o gtms.exe ./cmd/gtms    # Windows x86_64
```

### Prerequisites on the target machine

| Requirement | Why | How to check |
|-------------|-----|-------------|
| `git` on PATH | GTMS shells out to git for repo detection, branching, worktrees | `git --version` |
| `sh` or `bash` | Tier 1 adapters use `sh -c` (with `cmd /c` fallback on Windows). Tier 2 requires `sh`. | `sh --version` or `bash --version` |
| `gh` CLI (optional) | Only needed for GitHub Actions async adapters | `gh --version` |

### Multi-machine architecture patterns

**Pattern A: Local orchestration, remote execution.** The tester runs GTMS and AI tools locally. Test execution happens on a remote runner (GitHub Actions, Jenkins, self-hosted VPS). Create/automate adapters are `mode: sync` (local). Execute adapter is `mode: async` with `script` + `status-script`.

**Pattern B: Everything local.** GTMS, AI tools, and test execution all on the same machine. All adapters `mode: sync`. Best for solo testers, demos, development.

**Pattern C: Multiple terminals, parallel agents.** Multiple terminal windows, each running a different GTMS command. One adapter per shell, multiple shells for parallelism. An external orchestration layer (tmux, scripts, GUI) manages the fleet.

---

## CI/CD Integration

### GitHub Actions workflow for remote execution

A complete async execute adapter using GitHub Actions:

**Workflow file** (`.github/workflows/test-runner.yml`):
```yaml
name: GTMS Test Runner
on:
  workflow_dispatch:
    inputs:
      test_case:
        description: 'Test case ID (e.g. tc-a1b2c3d)'
        required: true
        type: string
      artefact_file:
        description: 'Path to automation artefact relative to repo root'
        required: true
        type: string
jobs:
  run-test:
    runs-on: self-hosted
    timeout-minutes: 15
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: '20'
      - run: npm ci
      - run: npx playwright install --with-deps chromium
      - name: Run test
        run: npx playwright test "${{ inputs.artefact_file }}" --reporter=junit --output=results/${{ inputs.test_case }}/
        continue-on-error: true
      - uses: actions/upload-artifact@v4
        if: always()
        with:
          name: test-results-${{ inputs.test_case }}-${{ github.run_number }}
          path: results/${{ inputs.test_case }}/
          retention-days: 30
```

**Adapter config:**
```yaml
adapters:
  execute:
    github-actions:
      mode: async
      script: gtms/adapters/github-execute.sh
      status-script: gtms/adapters/github-execute-status.sh
```

The trigger script starts the workflow via `gh workflow run` and writes the run ID to the result contract. The status script reads the run ID and polls `gh run view` for completion. See the [Remote Runner Pattern](#remote-runner-pattern-execute-over-ssh) above for SSH-based alternatives.

---

## Framework Bootstrap Prompts

Rather than shipping generic prompt templates for every framework, GTMS uses **bootstrap prompts** — meta-prompts that an AI coding tool uses to analyse your project and generate project-specific configuration.

**Why bootstrap prompts are better than generic templates:** Every project has its own test patterns, helper libraries, directory conventions, and assertion styles. A bootstrap prompt says: "Look at this project's existing tests, understand its conventions, and generate GTMS config and templates that match."

**What a bootstrap prompt generates:**
1. `gtms.config` adapter entries with the right flags for your tools
2. Automate prompt template with project-specific conventions and `<gtms-file>` format rules
3. Execute adapter config with the command to run your test runner

### How to use a bootstrap prompt

1. Copy the bootstrap prompt for your framework (see below)
2. Paste it into your AI coding tool in your project's directory
3. Review the generated config and templates
4. Run `gtms automate` and `gtms execute` to validate

### Available bootstrap prompts

| Framework | Status | Notes |
|-----------|--------|-------|
| **Playwright** | Available | See [USER-GUIDE.md](../USER-GUIDE.md) or `reference/archive/framework-integration-guide.md` for the full prompt |
| **BATS** | Available in-project | See `gtms/automation/prompts/automate-bats.md` |
| **Pester** | Available in-project | See `gtms/automation/prompts/automate-pester.md`. Execute via `adapters/pester-runner.sh` (Tier 2) |
| **Cypress** | Planned | `cy.` commands, `cypress.config.js`, `cypress/support/` helpers |
| **pytest** | Planned | Python fixtures, `conftest.py`, `pytest.ini`/`pyproject.toml` |
| **Jest** | Planned | `jest.config.js`, test utilities, mock patterns |
| **Newman/Postman** | Planned | Collection-based, environment files, `newman run` command |

### Writing a bootstrap prompt for a new framework

Include these sections: project analysis instructions (scan for existing tests, config files, helpers), GTMS adapter contract references, framework-specific guidance (conventions, assertion patterns, file naming), `<gtms-file>` output format rules (critical for streaming), and constraints (no code fences, no summaries).

**Template variable reference for automate prompts:**
- `{testcase_content}` — full content of the test case markdown file (always set for automate)
- `{context}` — additional context from `--context-file` (may be empty)
- `{guides}` — concatenated guide files from `guide-dir` config (may be empty)
- Short variables like `{output_dir}`, `{task_id}`, `{branch}` are available but rarely needed

> **Important:** `{framework}` is available in prompt templates but is NOT a Tier 1 command template variable. Use `{testcase_content}` for the test case content (not `{testcase}`, which is only the ID).

### Automate Prompt Template Quality Checklist

The automate prompt template is the highest-leverage artifact in adapter development. If the template has correct boilerplate patterns and strong output rules, AI-generated tests work on first pass. If not, every generated test has the same bugs. Review your template against this checklist:

- [ ] **Exact boilerplate** — show the complete setup/import/root-resolution pattern for your framework. Don't describe it — paste the literal code the AI should emit.
- [ ] **Subdirectory depth warning** — explicitly state how root resolution changes when the test is in a subdirectory, with both examples (root-level and nested). This is the most common AI automation bug.
- [ ] **Helper module/library usage** — if your project has shared test helpers, show the import pattern and list the available functions with signatures.
- [ ] **Assertion patterns** — show framework-specific assertion examples with `-Because` / descriptive messages. Include partial matching patterns to avoid CRLF issues.
- [ ] **Test case ID in test names** — instruct the AI to embed `tc-{hex}` in each test name for traceability.
- [ ] **`<gtms-file>` output format** — bare filenames only, correct extension (`.bats`, `.Tests.ps1`, `.spec.ts`), no directory prefixes.
- [ ] **Exactly one `<gtms-file>` per automate invocation** — instruct the AI to emit exactly one `<gtms-file>` tag per `gtms automate` run. Shared helpers belong in the framework's existing helper module (`common-setup.bash` for BATS, `GtmsTestHelper.psm1` for Pester), never in a second tag. GTMS rejects multi-file automate output at automate time -- the task fails and no automation record is written. `create` is unaffected: many files per invocation (one per test case) is the expected shape there.
- [ ] **Output rules at the END** — after all unbounded content (`{testcase_content}`, `{guides}`, `{context}`). See [Prompt Template Authoring](#prompt-template-authoring).
- [ ] **Negative instructions** — "Do NOT reproduce examples from source material", "Do NOT include directory prefixes in filenames", "Do NOT use framework v4 syntax".
- [ ] **One test per TC ID** — instruct the AI to generate exactly one test function/block per test case spec. Multi-step specs should be sequential steps within a single test, not split across multiple tests with the same ID. Duplicated IDs break `gtms gaps` traceability.
- [ ] **Stderr is not empty** — warn against asserting stderr is empty. GTMS writes guidance and warnings to stderr in normal operation. Show the correct anti-pattern (check for specific error content, not emptiness).
- [ ] **Mock adapter vs fixture guidance** — explain what mock adapters produce (task files, result contracts) and what they don't (test case files, automation records). Show how to write fixtures directly for tests that need to chain commands.

---

## Framework-Specific Notes

Known gotchas discovered during dry runs and real usage. When you discover a new framework integration issue, add it here under the relevant framework heading.

### Cross-Framework

**`--force` re-automate and duplicate cleanup (fixed):**
`gtms automate --force` now handles both manifestations of duplicate output files. Before invoking the adapter, GTMS cleans up existing output files by TC ID prefix — so if the AI generates a different filename slug (e.g. `tc-abc-init-demo.bats` → `tc-abc-init-demo-powershell.bats`), the old file is removed first. The streaming writer also accepts the `force` flag to overwrite same-filename duplicates rather than skipping them. Without `--force`, the duplicate file guard remains active as a safety net against accidental overwrites.

**Mock adapters don't produce pipeline artifacts:**
Mock adapters (`command: 'echo "..."'`) are useful for testing GTMS orchestration (task files, result contracts, exit codes) but they don't produce real test case files, automation records, or execution results. Tests that chain commands (create → automate → execute) and then check downstream artifacts must write fixture files directly. This applies to BATS, Pester, and any framework's test infrastructure.

**Test case fixtures must be in subfolders of `gtms/test/cases/`:**
GTMS does not discover test case files placed directly in `gtms/test/cases/`. Always create a subfolder (e.g. `gtms/test/cases/my-feature/tc-00000001-mock.md`). This catches out test fixtures that write TCs to the root `gtms/test/cases/` directory — `gtms status`, `gtms map`, and other visibility commands won't see them.

**Automation record schema renamed (legacy history):**
This describes the `internal/pipeline` `AutomationRecord` (`.automation.md`)
permanent-record layer, which the wiring cutover retired as a live
execute substrate. `gtms execute` no longer reads or writes automation records;
the live substrates are the wiring record and the result handoff (see the Handoff
section). Kept here as history for anyone reading a legacy record on disk.
Five YAML fields in the old `gtms/automation/records/tc-XXX--<fw>.automation.md` shape were renamed: `last-formal-result` -> `result`, `last-formal-run` -> `executed_artefact`, `last-formal-run-at` -> `executed_at`, `log` -> `notes`, `log-spill` -> `notes-spill`. Two universal fields added: `executed_by` (CLI flag `--executed-by` > `GTMS_EXECUTED_BY` env > `git config user.name`) and `environment` (from `--env`). The `defect:` field is now `[]string` -- `gtms triage --app-wrong --defect X` appends with dedup, doesn't overwrite. New `RecordCommon` struct embedded by `AutomationRecord`; future manual record type will share it.

**Important — the RESULT CONTRACT is unchanged:** the adapter-facing `log:` field in `.gtms/results/{task-id}.handoff.yaml` (`internal/result/result.go`) stays as `log:`. Only the pipeline record renamed to `notes:`. Adapter scripts that write `log:` to `$GTMS_RESULT_FILE` continue to work unchanged.

A `MigrateAutomationRecords()` walker exists in Go for migrating any legacy on-disk records, but is not on the live execute path.

**The live execute substrates are gitignored derived state:**
The per-run execute state lives in the result handoff (`.gtms/results/<task>.handoff.yaml`) and the per-test row (`gtms/execution/*.results.yaml`); the identity record is the wiring record (`gtms/automation/wiring/`). These, plus the legacy `gtms/automation/records/`, are gitignored (pipeline state, regenerable from `gtms execute` runs). Adapter authors should NOT assume any of them are committed source-of-truth. CI workflows that need a deterministic dashboard view should run `gtms execute -r <folder>` from a clean checkout before asserting on `gtms status`. Results reconstruct from spec + artefact + wiring; specs (`gtms/test/cases/`) and artefacts (`test/acceptance/...`) remain the committed source.

**Lifecycle vocabulary rename `failed` -> `error`:**
The lifecycle-error bucket is `gtms/tasks/error/` (previously `gtms/tasks/failed/`); task frontmatter `status: error` (previously `status: failed`); reader JSON `execute_status` field value is `"error"` (previously `"failed"`); CLI prose label is `Error` (previously `Failed`). Migration option (a) chosen -- one-time scripted rewrite in `scripts/migrate-failed-to-error.sh`, no legacy-tolerance bridge retained. Two lessons that generalise:

1. **Lifecycle vocabulary lives on seven surfaces** that must move in lockstep when the vocabulary changes — partial sweeps leave split-brain where one surface scaffolds an old-bucket task and another doesn't see it. The audit checklist:
   1. **Directory paths** under `gtms/tasks/<state>/` (created by `gtms init` scaffold).
   2. **Frontmatter `status:` values** in task files (written by `internal/task` + invoker; rejected at `task.Create`/`task.Move` if not in `ValidStatuses`).
   3. **Reader JSON field values** in `gtms status --json`, `gtms map --json`, `gtms gaps --json` — specifically `execute_status`.
   4. **CLI prose labels** — `internal/cli/status.go` `formatTaskStatus`/`formatDetailLabel`/`formatExecuteLabel` plus `internal/cli/create_status.go` `formatTaskStatus`.
   5. **BATS fixture seeds + assertions** — heredoc-written task files in `test/acceptance/**/*.bats` (paths, frontmatter, renderer output).
   6. **Pester fixture seeds + assertions** — equivalent at `test/pester/**/*.Tests.ps1` (asserts on the same bucket paths via `Get-ChildItem`).
   7. **Test-infrastructure helpers** that pre-create the lifecycle directories — `test/test_helper/common-setup.bash` for BATS and `test/pester/GtmsTestHelper.psm1` for Pester. Easy to miss because they're not in any `gtms/test/cases/` spec — grep on the bucket name explicitly.

   The rename hit all seven surfaces by completion. The Pester surface (6 + 7) escaped the Phase 3 BATS-only sweep and was caught by the GitHub Pester job: `tc-e3937fc8` failed in the first push and `tc-852191bc` was passing by accident -- its `failed/` branch never ran because the underlying bug is fixed on CI, so the legacy `Get-ChildItem` returned empty and the test fell through other error indicators.
2. **Lifecycle and test-outcome vocabulary are orthogonal** -- `status: error` (adapter/runtime/validation failed -> `gtms/tasks/error/`) and `result: fail` (test ran cleanly and reported a failure -> `gtms/tasks/complete/`) are independent dimensions. Find-replace of `"failed"` -> `"error"` corrupts the test-outcome family. When sweeping vocabulary across fixtures, classify each occurrence: (A) lifecycle path/status/label -> rename, (B) test-outcome contract (`result: fail`, `fail-exit-codes`, "1 failed" tallies) -> preserve, (C) archaeology asserting absence of a retired literal -> preserve with a `# legacy contract literal: intentional` comment.

**Helper-sync coverage gap:**
Previously, neither `scripts/remote-full-run-unix.sh` nor `scripts/remote-dir-run-unix.sh` synced the `scripts/` directory to the VPS workspace -- only `internal/`, `cmd/`, `test/`, `gtms/`, `reference/`, etc. Both helpers now `scp -r scripts/`. Lesson: when you add a top-level helper directory that BATS tests exec from (e.g. one-time migration scripts), audit the remote-helper sync block. Symptom of a missing sync is a BATS that passes locally on Windows but fails on Linux VPS with `Migration script not found at $PROJECT_ROOT/scripts/...`. The hypothesis ladder for "passes locally, fails remotely" is now: (a) stale binary, (b) awk/sed portability, (c) line-ending difference, (d) PROJECT_ROOT discovery, **(e) helper sync coverage gap** -- check (e) first when the symptom is `file not found`.

**Local `bats-runner` skip surfacing -- fixed, mixed-rollup later tightened:**
The original Tier 1 `bats-runner` (`command: bats {artefact_file}`) couldn't distinguish skip from pass -- BATS exits 0 for both, and the Tier 1 contract has no hook into stdout. That form was retired and replaced with a Tier 2 wrapper at `adapters/bats-runner.sh` that sources `adapters/lib/bats-tap.sh` and classifies via `classify_bats_status` (the same TAP rules the four `remote-bats-*` adapters now use). All five adapters in the BATS family -- `bats-runner`, `remote-bats-execute`, `remote-bats-lean`, `remote-bats-unix-execute`, `remote-bats-unix-lean` -- produce identical contract output for any given TAP input. The rollup rule was then tightened: any `# skip` directive without a `not ok` line demotes the spec file to `result: skip` (mixed pass+skip -> ⊘, not ✓; the `result:` axis carries this, not the retired `status: skipped`). Wrappers surface both pass and skip counts in the `summary:` field (e.g. `"2 passed, 1 skipped"`); the all-skip case still produces `"All N tests skipped"` for back-compat.

**Folder arguments are subfolder slugs, not prefixed paths:**
Commands like `gtms automate`, `gtms execute`, `gtms delete`, and `gtms reset` accept a folder argument that is the **subfolder slug** relative to `gtms/test/cases/` (e.g. `my-feature`). Passing the long-form `gtms/test/cases/my-feature` or the short-form `cases/my-feature` is rejected with *"Don't include the `gtms/test/cases/` prefix — GTMS adds it automatically"* — GTMS prepends the prefix itself.

**Task IDs appear in stderr, not stdout:**
GTMS outputs adapter metadata (adapter name, branch) on stdout. Task completion messages — including the task filename with its ID — go to stderr as guidance/warning messages. Tests that assert on the task ID must check stderr, not stdout.

**Execute fixture checklist (three things must be right):**
When writing fixture files for `gtms execute` tests: (1) a wiring record must exist for the TC and framework at `gtms/automation/wiring/<tc>--<fw>.wiring.yaml` (the six-field schema; no `status` field) -- without it, execute errors with `No wiring record found`; (2) the execute adapter config must have `framework:` matching the wiring record's `framework:` field -- without this, execute can't find the record; (3) the artefact file referenced in the wiring record's `artefact:` must actually exist on disk -- execute verifies the file before running the adapter.

**YAML `---` in result contract `log:` field breaks pipeline:**
If a Tier 2 adapter writes a `log:` block scalar to the result contract and the adapter output contains `---` (YAML document separator), Go's YAML parser silently truncates the contract. `UpdateExecutionResult` fails without any error or warning, leaving the automation record's `result` stuck on the previous value. The dashboard shows the wrong state. **Do NOT write raw adapter output to the `log:` field** -- either omit `log:` entirely, or sanitise the output to remove `---` lines. This affects any adapter whose output may contain `---` (Pester's ANSI output is a known trigger). *TODO: Update this entry when this is fixed -- the `log:` field should then be safe to use.*

**`--framework` filter scopes bulk execute:**
`gtms execute -r --adapter remote-bats-lean` (or with explicit `--framework bats`) skips TCs without a matching framework record quietly -- the skip is silent in text output (em-dashes for AUTOMATE / EXECUTE / LAST RESULT). Framework is resolved from the `--framework` flag or the adapter config's `framework:` field. The skip applies even with `--force` -- `--force` re-runs eligible TCs but does not attempt executions that cannot succeed. The folder-summary views (`status`, `gaps`) also respect `--framework`: `2/3 AUTOMATED` under a framework filter means 2 of 3 TCs in that folder have a record for the selected framework. Per-TC detail views still fall back to any available record when no framework match exists (strict per-TC filtering applies only with an explicit `--framework`). **`--json` consumers** can distinguish "no records anywhere" from "records exist under other frameworks" via per-TC `available_frameworks` (sorted list) and folder-level `framework_mismatch` (count) -- these are wired through the reader data layer. Text rendering is unchanged by design.

**Same BATS + Pester coverage on one TC:**
A single TC can carry wiring records for multiple frameworks -- `tc-xxx--bats.wiring.yaml` and `tc-xxx--pester.wiring.yaml` coexist. Automate twice with different `--adapter` values (e.g. `--adapter bats` then `--adapter pester`) to produce both. Each framework's `gtms execute` runs independently, and `gtms status --framework <name>` selects the right record. There is currently no single-command execute that dispatches per-TC to the correct framework adapter.

**Tier 1 `create` adapters MUST use `{output_dir}` -- CWD is the workdir, not the project root:**
Tier 1 commands execute with `cmd.Dir = ac.WorkDir` (`.gtms/<task-id>/`). A mock or adapter that writes via CWD-relative paths like `mkdir -p gtms/test/cases; cat > gtms/test/cases/tc-X.md ...` lands its files in `.gtms/<task-id>/gtms/test/cases/` -- not `<project-root>/gtms/test/cases/<folder>/` where the create validator scans. The validator finds nothing, returns no violations, and the test exits 0 when it should have failed. The pattern that works:

```yaml
adapters:
  create:
    mock:
      mode: sync
      command: 'bash $mock_script {output_dir}'
```

```bash
#!/bin/bash
OUT_DIR="$1"           # GTMS substitutes {output_dir} here
mkdir -p "$OUT_DIR"
cat > "$OUT_DIR/tc-aaaaaaaa-example.md" <<'SPEC'
...
SPEC
```

The same applies for any Tier 1 create adapter (not just BATS mocks). If you're testing the validator, the mock's file must land where the validator looks. Tier 2 adapters are fine by default — `$GTMS_OUTPUT_DIR` is exported automatically.

**Create validator error shape (result contract status is `error`, not `failed`):**
When the post-write validator rejects a spec batch, the result contract gets `status: error` -- matching the existing `rejectMultiFileAutomate` precedent. The **task file** still routes to `gtms/tasks/error/`, but assertions on contract YAML should look for `status: error`. The summary string and the separate `validation-error:` field both carry the formatted violation list.

**`gtms execute` requires an existing wiring record (adapter workflow design):**
The execute pipeline is keyed on the wiring record -- `gtms/automation/wiring/<tc-id>--<fw>.wiring.yaml` must exist before `gtms execute <tc-id>` will invoke an adapter. The wiring record has no lifecycle status field; it just has to exist and not be drifted. If your adapter's workflow skips the `gtms automate` step (e.g. you're shipping pre-written scripts), use `gtms link` to write a wiring record pointing at the artefact before invoking `gtms execute`; otherwise the user hits `No wiring record found for 'tc-XXX' (framework: <fw>)`. For manual testing, use `gtms prime --framework manual` to stamp the result file before executing with `--adapter manual-execute`.

**Mode 3 prime-path exemption:** When `gtms execute` resolves to `agent-execute` or `manual-execute` (the Tier 0 built-in action adapters), the wiring-record lookup is skipped. These adapters read the verdict from the filled result template (`gtms/manual/records/{tc-id}--manual.result.yaml`), not from an automation artefact. The prime pipeline (`gtms prime` -> user edits -> `gtms execute --adapter manual-execute`) is the intended entry path for these adapters.

**Execute needs a wiring record, not a `status:` gate:**
`gtms execute` resolves solely from a wiring record (`gtms/automation/wiring/<tc>--<fw>.wiring.yaml`). The six-field wiring schema has **no `status` field**, so there is no `developed`/`accepted` gate -- the earlier gate (and the `automation not ready` skip) is retired. **Anyone hand-crafting fixtures** -- BATS, Pester, scripted pipelines, migration scripts, seed data for demos -- must write a wiring record for the TC and framework (not an `.automation.md` record). Missing wiring errors with `No wiring record found for '<tc>' (framework: <fw>)`; a spec or artefact edit since the wiring was written surfaces as stale wiring (acknowledge with `gtms link --refresh <tc>`, or refresh the fixture's hashes).

### Playwright

**Config gotchas:**
- `testDir` in `playwright.config.ts` must include the directory where GTMS writes specs. The shipped `playwright` preset writes specs to `gtms/scripts/playwright` (the runner's `output-dir`), and Playwright's default `testDir: './tests'` excludes it. **Preferred fix:** set the automate/execute adapter `output-dir` to the directory Playwright already scans (its `testDir`), so GTMS writes specs where Playwright looks. Alternative: set `testDir: '.'` in the Playwright config, or point it at `gtms/scripts/playwright`. Playwright silently skips specs outside `testDir`.
- **Reporters that open a browser/UI wedge non-interactive `execute` (DOC-011).** A common config -- `reporter: [['list'], ['html', { open: 'on-failure' }]]` -- makes `gtms execute` hang on the first *failing* test: Playwright launches the HTML report server (default port `:9323`) and tries to open a browser that does not exist in a headless shell, so the process blocks. Symptoms: execute never returns on a run that includes a failure, a stranded `node` holds `:9323`, and a `● in-progress` task is left in `gtms/tasks/in-progress/`. Passing runs never trigger it, so it looks intermittent. **Fix:** set `open: 'never'` -- `reporter: [['list'], ['html', { open: 'never' }]]`. The env form `PW_TEST_HTML_REPORT_OPEN=never` does not help: a Tier-2 adapter's environment is stripped at the GTMS boundary, so it never reaches `npx playwright test`. Prefer the config change. (The default 30-minute execute timeout is the safety net that eventually reaps such a hang -- see the `timeout` field -- but stopping the trigger is better.)
- Multiple browser projects: 6 scaffold tests x 5 projects = 30 runs. Use `--project=chromium` during development.
- `forbidOnly: true` in CI config causes unexpected exit 1 if `test.only()` is left in a spec.

**Scaffold patterns:**
- Use `test.fixme()` not `test.todo()` — Playwright has no `test.todo()`. Claude will hallucinate it.
- `test.fixme()` produces exit 0 (skipped). Only failed assertions produce exit 1.

**AI hallucination risks:**
- Page object hallucination — AI invents methods (`page.clickButton()`, `page.fillForm()`)
- `--allowedTools ""` blocks file reading — use `--context-file` and `{testcase_content}` instead

### BATS

**`go build` gotchas:**
- Absolute paths with spaces break `go build` on MINGW. Fix: `cd "$PROJECT_ROOT" && go build -o "$BIN" ./cmd/gtms`
- Essential in git worktrees where the working directory is deep

**`setup_file()` gotchas:**
- `PROJECT_ROOT` must be exported (see [BATS boilerplate](#common-mistakes) above)
- Depth calculation: `test/acceptance/subdir/` needs `../../..` — count carefully

**Assertion gotchas:**
- Adapter echo output is NOT in CLI stdout — check `.gtms/results/{task-id}.handoff.yaml` `summary` field
- An empty Tier 1 value is escaped to `''`, not to nothing. `{output_subdir}` is empty for a root-level test case, so a command containing a bare `{output_subdir}` passes a literal `''` as that argument -- assert on `''`, not on empty output. (Tier 2 scripts are unaffected: `$GTMS_OUTPUT_SUBDIR` arrives genuinely empty.) Write the placeholder bare either way -- see [Placeholders go in BARE](#placeholders-go-in-bare-do-not-put-shell-quotes-around-them)
- `assert_output --partial` is the safe default for multi-word / free-form text where CRLF or whitespace may vary (Windows/MINGW). **But not for numeric counts or single-token values** -- see below
- **Default-too-forgiving:** the BATS adapter's historical default was "always use `--partial`". That rule is wrong for pure numeric counts (`wc -l` output) and any single-digit token, because `--partial "0"` also matches `10`, `100`, `0 errors`, etc. Use exact `assert_output "0"` for single-digit counts (trim with `tr -d '[:space:]'` first) and `[ -z "$output" ]` to enforce empty stdout rather than `refute_output --partial "<specific phrase>"`
- **Walk-up fail-fast:** every `setup_file()` that walks up to `gtms.config` must add a fail-fast guard: `[ -f "$dir/gtms.config" ] || { echo "PROJECT_ROOT discovery failed" >&2; return 1; }`. Without it, the loop terminates at `/` and produces cryptic `load: file not found` errors
- **`grep -i` aborts on multi-byte UTF-8 input under minimal locales**: on win-runner-1 (SSH session, no `LC_ALL` / `LANG` set), `grep -q -i` on GTMS product output containing em-dash (U+2014) or status glyphs (✓ ✗ ⊘ ⚠ ●) dies with `Aborted` (SIGABRT), not exit 1. Local MINGW64 and Ubuntu CI both tolerate it -- this is a VPS-specific locale issue that only surfaces via `remote-bats-lean`. **Always prefer bash glob for literal substring checks** against product output: `[[ "$var" == *"literal"* ]]` instead of `printf '%s' "$var" | grep -q -i "literal"`. Bash glob is locale-independent and matches bytes verbatim. The sweep converted 9 BATS tests; `grep -i` is still appropriate for content guaranteed ASCII-only (error messages, file paths, CI summary labels).
- **Whole-row glyph assertions on dashboard rows are structurally wrong**: `[[ "$row" != *$'\xe2\x9c\x93'* ]]` (checkmark absent) fails because CREATE and AUTOMATE columns legitimately contain ✓ when those stages are complete. To prove EXECUTE doesn't show ✓, assert the *presence* of the alternative icon: `[[ "$row" == *$'\xe2\x9a\xa0'* ]]` (warning triangle). If EXECUTE shows ⚠ it cannot also show ✓ -- no need to check for absence. Same principle applies to any per-column assertion: either extract the column text first, or use positive presence of the expected icon rather than row-wide absence of the unwanted one. Reference: `test/acceptance/spec-bats-alignment-drift/tc-4d4db93a-dashboard-error-no-checkmark.bats`.
- **When converting `grep -iE` to bash glob, preserve load-bearing case-insensitivity via bash character classes**: a naïve swap from `grep -qiE "in-progress|pending|running"` to `[[ "$var" == *"in-progress"* ]]` drops the `-i` -- and fails when the product renders `● In Progress` (title case). Use bash character classes: `[[ "$var" == *[Ii]"n-"[Pp]"rogress"* || "$var" == *[Ii]"n "[Pp]"rogress"* ]]`. Reference pattern in `test/acceptance/enh-096-enh-092-functional-followups/tc-1a392ff1-*.bats`. Rule of thumb: if the original `grep` used `-i` and you don't know whether the product string is already lowercase, assume case is load-bearing and add the character classes.

**Wiring record gotchas:**
- Execute requires a wiring record `gtms/automation/wiring/<tc-id>--<fw>.wiring.yaml` (six fields, no `status` gate); GTMS scans `gtms/automation/wiring/`, not the retired `gtms/automation/records/*.automation.md`.
- The wiring frontmatter field is `testcase:` (not `test_case_id:`) -- AI-generated fixtures often confuse the two.
- Cross-framework fixture pattern: when a BATS test needs to simulate "this TC has a Pester record" (or Cypress, or any other framework), write a wiring record at `gtms/automation/wiring/<tc-id>--<fw>.wiring.yaml` with the six identity fields (`testcase`, `testcase-hash`, `framework`, `adapter`, `artefact`, `artefact-hash`). AI often writes raw artefact files like `gtms/automation/<slug>/pester/<tc>.Tests.ps1` instead -- GTMS resolves execution from the wiring record, so those raw files are invisible and the fixture silently misrepresents the state it's trying to test. The `artefact:` value only needs to be a path string; the file doesn't need to exist unless the test itself touches it.

**Fixture placement gotcha:**
- BATS tests that exercise the resolver's glob-walk fallback (stale-hash, skip-list scenarios) must place primary artefact fixtures under user-controlled directories (`test/acceptance/`, `tests/`). Primaries placed inside `.git`, `.gtms`, `gtms/test/cases/`, `gtms/automation/`, or `gtms/tasks/` -- at any depth -- are invisible to the walk per the resolver's hardcoded skip list, so "multiple artefact" scenarios silently fire "no artefact found" instead. The `automate-bats.md` prompt template carries this as critical rule #11 at generation time; this note is a backup for manual authoring.
- **Note:** The `artefact-ignore:` config key was retired in the wiring-authoritative cutover. It is no longer accepted in `gtms.config`.

**Platform notes:**
- Windows binary: must use `gtms.exe` not `gtms` on Windows/MINGW
- `CLAUDECODE` env var blocks nested Claude sessions — use Tier 2 or `unset CLAUDECODE`
- `.bats` files must have LF line endings — CRLF causes "bad interpreter" errors

**Orthogonal contract -- replaces tri-state:**
- A Tier 2 BATS adapter that writes `status: error` on any non-zero exit conflates genuine assertion failures with infra failures (SSH down, bats not installed). Parse the TAP stream: `not ok` lines with a valid `1..N` plan → `status: complete` + `result: fail`. No plan line, or SSH exit 255 → `status: error` + `result:` empty. See `adapters/remote-bats-lean.sh` for the reference implementation. Legacy `status: fail` and `status: skipped` are **retired** — writing them triggers Tier 2 read-boundary validation rejection and recovery to `status: error`.

**Skip classification:**
- BATS `skip "reason"` inside a test body emits TAP `ok N # skip <reason>` and exits 0. This once looked indistinguishable from a real pass -- the dashboard rendered ✓ while the test never actually asserted. Skip classification was moved from core GTMS into the adapters themselves: each BATS adapter now sources `adapters/lib/bats-tap.sh` (a shared TAP classifier) and writes the appropriate `status:` + `result:` values to `$GTMS_RESULT_FILE` directly. The contract shape then changed: adapters write `status: complete` + `result: skip` (not the retired `status: skipped`). The rollup rule is **any-skip-without-fail demotes to skip**: if the TAP stream contains at least one `# skip` directive and zero `not ok` lines, the adapter writes `status: complete` + `result: skip` regardless of how many tests passed. The pass count is surfaced in the `summary:` field (e.g. `"2 passed, 3 skipped"`). All-skip files produce `"All N tests skipped"`. All-pass files produce `status: complete` + `result: pass`. Fail takes precedence -- any `not ok` line produces `status: complete` + `result: fail` regardless of skip lines. New BATS adapters should source `adapters/lib/bats-tap.sh` and use the `classify_bats_status` function for consistent classification. Non-BATS adapters that detect skips should write `status: complete` + `result: skip` to `$GTMS_RESULT_FILE` directly. The pipeline maps contract `result: skip` -> record `result: skipped`, which renders as `⊘` in both `gtms status` and `gtms map`.

**bats-in-bats env scrub:**
- When `gtms execute` is invoked from inside a parent `bats` run — common during dogfood / acceptance suites that build fixture projects and exercise the real adapter — the parent prepends its internal `$BATS_LIBEXEC` to PATH and exports `BATS_*` vars. PATH lookup then resolves `bats` to the libexec entry script, which expects helpers only the user-facing wrapper sources. Direct invocation fails with `bats_readlinkf: command not found` and `//bats-core/validator.bash: No such file or directory`, and the contract gets `status: error` with summary "Malformed or missing TAP output". The fix lives in `adapters/bats-runner.sh` (and the embedded scaffold template): unset `BATS_LIBEXEC BATS_ROOT BATS_CWD BATS_TMPDIR BATS_ROOT_PID BATS_VERSION` and strip `*/libexec/bats-core` entries from `PATH` before invoking `bats`. **Authors of any new adapter that shells out to a tool that may have been invoked by a parent process** (Pester invoking pwsh, Playwright invoking node, npm-installed tools that put `libexec` on PATH) should consider the same defensive scrub.

**Orthogonal contract vocabulary -- replaces the legacy `status:` overloads:**
- The result contract has two orthogonal axes: `status:` carries adapter-execution state (`pending | in-progress | complete | error`); `result:` carries test outcome (`pass | fail | skip | error`). Legacy `status: fail` and `status: skipped` are **retired and rejected** by validation. Both `bats-runner.sh` and all four `remote-bats-*` wrappers write the orthogonal form. **When authoring specs**: assert `status: complete` + `result: pass` for pass cases; `status: complete` + `result: fail` for fail; `status: complete` + `result: skip` for skip; `status: error` for adapter crash (with `result:` empty). **When asserting on the automation record**: `result: pass/fail/skipped/error` (note the pipeline maps contract `result: skip` → record `result: skipped`). Single-TC execute exits 0 for `status: complete` (any result) and exits non-zero for `status: error`.

**Mocks must self-classify:**
- There is no in-core classifier to fall back on. Any mock adapter in a BATS test MUST write both `status:` and `result:` to `$GTMS_RESULT_FILE` itself. For a pass mock: `status: complete` + `result: pass`. For a fail mock: `status: complete` + `result: fail`. For a skip mock: `status: complete` + `result: skip`. For a crash mock: `status: error` with `result:` empty or omitted. Mocks that write `status: complete` without a `result:` field will be rejected by Tier 2 read-boundary validation and recovered to `status: error`.

**Scaffold-template parity is a contract -- and a Go raw-string-with-backticks gotcha lurks underneath:**
- `gtms init` ships adapter scripts as embedded Go string constants in `internal/scaffold/templates.go` (`batsRunnerScript`, `batsTapHelper`, etc). Any edit to an in-tree adapter script under `adapters/` MUST be mirrored byte-for-byte into the corresponding constant -- otherwise `gtms init` writes a stale copy to fresh projects. The `tc-798bac31` test is the dual-update guard for the BATS family; if you're touching `adapters/*.sh` or `adapters/lib/bats-tap.sh`, run that test (or its broader `bats-runner-mixed-skip-rollup` folder) before merging. Same rule for any future adapter family that gets a scaffold constant.
- The trap underneath: a backtick-delimited Go raw string literal (`` const x = `...` ``) cannot contain a backtick. If the in-tree script has backticks in its comments (markdown-style emphasis around technical terms -- `` `gtms execute` ``, `` `bats` ``, etc.), the embedded constant must use the concat workaround `` ` + "`x`" + ` `` (see `templates.go` line 791 for `gtms.config` / `demo_seeded: false`, or `batsRunnerScript` for three uses). Anyone editing the constant naively to "clean up" the concat operators will silently strip backticks from the output and break the parity test on the next run. This was logged; the fix added a docstring above `batsRunnerScript` flagging the contract for the next editor.

### Pester

**`$PSScriptRoot` depth gotchas:**
- Pester uses `$PSScriptRoot` (the directory containing the `.Tests.ps1` file) for relative path resolution — unlike BATS which can walk up to `gtms.config` dynamically
- `test/pester/Foo.Tests.ps1` → 2 levels of `..` to project root
- `test/pester/subdir/Foo.Tests.ps1` → 3 levels of `..` to project root
- AI consistently hardcodes 2 levels regardless of actual depth. The automate prompt template must show both examples explicitly.
- `Import-Module` path also changes: `$PSScriptRoot\GtmsTestHelper.psm1` at root level, `$PSScriptRoot\..\GtmsTestHelper.psm1` in a subdirectory

**PowerShell 5.1 compatibility:**
- `Join-Path` only accepts TWO arguments on PS 5.1. `Join-Path $a 'b' 'c'` fails with "A positional parameter cannot be found". Nest calls instead: `Join-Path (Join-Path $a 'b') 'c'`. AI consistently generates the PS 7+ multi-argument form.

**NUnit XML output:**
- Pester outputs NUnit XML (not JUnit). Use `total=` and `failed=` attributes, not `tests=` and `failures=`
- Invoke with `-CI` flag for NUnit output: `pwsh -NoProfile -Command "Invoke-Pester -Path 'spec.Tests.ps1' -CI"`
- Output goes to `testResults.xml` in the working directory by default

**PowerShell runtime:**
- Use `pwsh` (PowerShell 7), not `powershell` (5.1). Pester 5 works on both but pwsh handles UTF-8 and ANSI better
- GitHub Actions `windows-latest` has pwsh pre-installed
- Always set UTF-8 encoding in `BeforeAll`: `[Console]::OutputEncoding = [System.Text.Encoding]::UTF8`

**Git stderr warnings become terminating errors under Pester `-CI`:**
- Pester's `-CI` mode (and `$config.Run.Exit = $true`) sets `$ErrorActionPreference = 'Stop'`. Git writes warnings to stderr (e.g. "LF will be replaced by CRLF"), PowerShell wraps them in `RemoteException`, and Pester treats them as test failures.
- In `GtmsTestHelper.psm1`, `New-GtmsTestRepo` temporarily sets `$ErrorActionPreference = 'SilentlyContinue'` around git calls. If you write custom test helpers that call git, do the same.
- Tests pass with `Invoke-Pester -Output Detailed` (lenient) but fail with `-CI` (strict) — this is almost always a suppressed-stderr issue.

**UTF-8 BOM breaks GTMS file parsing:**
- PowerShell 5.1's `Set-Content -Encoding UTF8` writes a BOM (byte order mark: `EF BB BF`). Go's YAML/frontmatter parser cannot handle BOM — it silently fails to parse the file. GTMS then skips the file entirely (`gtms status` shows "No test cases found" even though the file exists).
- Use `Write-GtmsFixture` (from `GtmsTestHelper.psm1`) instead of `Set-Content` for any file that GTMS will parse — test cases, automation records, task files, result contracts.
- PowerShell 7's `Set-Content -Encoding utf8NoBOM` also works, but PS 5.1 doesn't have that option.

**Stream capture:**
- Do NOT use `& .\gtms.exe 2>&1` — PowerShell wraps stderr in `ErrorRecord` objects that break string assertions
- Use `System.Diagnostics.Process` for clean stdout/stderr/exit code capture (the `Invoke-GtmsCli` helper in `GtmsTestHelper.psm1` does this)

**Assertion patterns:**
- Use `-Match` instead of `-Be` for string content — avoids CRLF mismatches
- Always use `-Because` parameter — Pester failure messages without it are unhelpful
- Use `Should -Not -Exist` / `Should -Exist` for file checks (not `Test-Path` in assertions)

**Execution policy:**
- Default PS 5.1 policy blocks unsigned scripts. Users need `Set-ExecutionPolicy -ExecutionPolicy RemoteSigned -Scope CurrentUser`
- Pester 5 must be installed separately on PS 5.1 — it ships with Pester 3.x: `Install-Module Pester -Force -SkipPublisherCheck -Scope CurrentUser`

**Version pinning:**
- Use `#Requires -Module @{ ModuleName = 'Pester'; ModuleVersion = '5.4.0' }` (minimum version), NOT `RequiredVersion = '5.6.1'` (exact version). Users install whatever Pester 5.x is current. Exact pinning fails on version mismatch.

**Mock adapter limitations in tests:**
- Mock adapters (`command: 'echo "..."'`) validate GTMS orchestration (task files, result contracts, exit codes) but do NOT produce pipeline artifacts (test case files, automation records, execution results)
- Tests that chain commands (create → automate → execute) must write fixture files directly instead of relying on upstream mock commands. See the Pester automate prompt template (`gtms/automation/prompts/automate-pester.md`) for fixture patterns.

**One `It` block per test case:**
- Each test case spec (one `test_case_id`) must map to exactly one Pester `It` block. If the spec has multiple steps, they go sequentially inside a single `It`. Splitting steps into separate `It` blocks with the same TC ID breaks `gtms gaps` traceability.

**Stderr is not empty in normal operation:**
- GTMS writes guidance messages and warnings to stderr. Do NOT assert stderr is empty (`Should -BeNullOrEmpty`). Check for specific error patterns instead: `$r.Stderr | Should -Not -Match '✗'`

**cmd.exe testing from within Pester:**
- Use `Invoke-GtmsViaCmd` (from `GtmsTestHelper.psm1`) for cmd.exe shell boundary tests. It wraps `cmd.exe /c` with the same `System.Diagnostics.Process` pattern as `Invoke-GtmsCli`.
- cmd.exe uses the system code page (typically 437 or 1252) — Unicode characters may not render. Assert that output is received rather than checking exact Unicode.

**File naming:**
- `.Tests.ps1` extension is required for Pester test discovery (capital T)
- Pester config `Filter.Tag` and `Filter.ExcludeTag` can filter by tags in `Describe`/`It` blocks

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
- [ ] Script updates `$GTMS_RESULT_FILE` with `status: complete` (+ a `result:` value) or `status: error` -- never the retired `status: fail`/`status: skipped`
- [ ] In-tree adapter scripts are ASCII-only (no smart quotes, em-dashes, or box-drawing bytes) -- enforced by `TestScaffoldAndAdapterSourcesASCIIOnly`
- [ ] Status decision parses framework output (TAP for BATS, JUnit XML for Pester) -- never relies on exit code alone (see the canonical classification pattern in the Result Contract section)
- [ ] Remote/transport settings (host, user, port, remote dir) come from env vars or config — NOT hardcoded in the script. Hardcoding blocks self-contained testing of the error path (you can't point the adapter at an unreachable host without forking the script)
- [ ] For async: trigger script exits quickly (doesn't block waiting for remote work)
- [ ] For async: status script reads remote reference from result contract and polls correctly

### Result Contract
- [ ] `status` is set to `complete` (adapter ran to completion) or `error` (couldn't run) -- the retired `fail`/`skipped` statuses are rejected
- [ ] `result` is set when `status: complete`: `pass` / `fail` / `skip` / `error` (empty when `status: error`) -- the orthogonal axis
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
| **Output** | Files in `gtms/test/cases/` or `gtms/automation/specs/` |
| **Example** | `claude -p "Read the system prompt instructions..." --append-system-prompt-file {prompt_file} --allowedTools ""`, GitHub Copilot issue assignment |

### Runner Pattern (execute)

Executes tests and returns results.

| Aspect | Typical behaviour |
|--------|-------------------|
| **Mode** | `sync` (local runner) or `async` (CI pipeline) |
| **Input** | Test case ID, spec file path |
| **Output** | Result files in `results/` (JUnit XML, reports) |
| **Example** | `npx playwright test`, GitHub Actions workflow, SSH to remote VPS (see Remote Runner Pattern below) |

### Analyser Pattern (status, gaps, triage)

Reads data and returns structured analysis. Handled by the built-in `local-reader` adapter (Tier 0).

| Aspect | Typical behaviour |
|--------|-------------------|
| **Mode** | `sync` |
| **Input** | Scope of analysis |
| **Output** | Structured data displayed to user |
| **Example** | Built-in filesystem reader, future AI-assisted triage |

### Built-in Action Pattern

Tier 0 adapters for action commands. Six named built-ins (`agent-create`, `manual-create`, `agent-prime`, `manual-prime`, `agent-execute`, `manual-execute`) handle the create/prime/execute lifecycle without external scripts. Intended for the Mode 3 prime-path workflow where no external AI tool or test runner is needed — the operator fills test cases and records verdicts directly.

| Aspect | Typical behaviour |
|--------|-------------------|
| **Mode** | `sync` |
| **Input** | Test case target, result template (execute adapters) |
| **Output** | Skeleton specs (create), stamped result files (prime), pipeline records (execute) |
| **Example** | `gtms create folder name` (skeleton), `gtms prime tc-X` (stamp template), `gtms execute tc-X --adapter manual-execute` (record verdict) |

### Remote Runner Pattern (execute over SSH)

Executes tests on a remote machine via SSH, returning results to the local pipeline. Validated with BATS tests on a Windows Server 2022 VPS over Tailscale (301/301 tests passing).

**Infrastructure:**
- Remote machine with test framework installed (e.g. BATS via npm, Playwright via npx)
- SSH key-based auth from local machine to remote (password-free)
- Network connectivity (Tailscale recommended for simplicity and security)

**Parallel session isolation:** Both adapter variants derive their remote directory from `basename "$GTMS_PROJECT_ROOT"`. When running from a worktree (e.g. `agent-abc123`), files sync to `/c/gtms-workspace/agent-abc123/` — isolated from other sessions. This enables multiple `gtms execute` sessions to run on the same VPS simultaneously without file collisions.

**Two adapter variants for different use cases:**

| Variant | When to use | Trade-off |
|---------|------------|-----------|
| **Full sync** (`remote-bats-execute.sh`) | Single TC or small folder runs | Slower — SCPs files per invocation, but always current |
| **Lean** (`remote-bats-lean.sh`) | Bulk runs (50+ TCs) | Fast — SSH only, but requires manual file sync first |

**Full sync adapter** (Tier 2, sync) — pushes files then runs:

```yaml
# gtms.config
execute:
  remote-bats:
    mode: sync
    framework: bats
    script: gtms/adapters/remote-bats-execute.sh
    output-dir: test/acceptance
```

```bash
#!/bin/bash
# adapters/remote-bats-execute.sh
set -e

REMOTE_HOST="win-runner-1"
REMOTE_USER="Administrator"
SSH_PORT=2222
PROJECT_SLOT=$(basename "${GTMS_PROJECT_ROOT:-gtms-v1}")
REMOTE_SLOT="${PROJECT_SLOT}-${GTMS_TASK_ID}"   # per-invocation isolation
REMOTE_DIR="/c/gtms-workspace/${REMOTE_SLOT}"
BASH_CMD="\"C:/Program Files/Git/bin/bash.exe\""

# Sync files to remote (REMOTE_SLOT is unique per task — concurrent agents can't overwrite each other)
SCP_OPTS="-P ${SSH_PORT} -q"
scp ${SCP_OPTS} -r "${GTMS_PROJECT_ROOT}/test/" "${REMOTE_USER}@${REMOTE_HOST}:C:/gtms-workspace/${REMOTE_SLOT}/"
scp ${SCP_OPTS} -r "${GTMS_PROJECT_ROOT}/gtms/test/cases/" "${REMOTE_USER}@${REMOTE_HOST}:C:/gtms-workspace/${REMOTE_SLOT}/"
scp ${SCP_OPTS} "${GTMS_PROJECT_ROOT}/go.mod" "${REMOTE_USER}@${REMOTE_HOST}:C:/gtms-workspace/${REMOTE_SLOT}/"
scp ${SCP_OPTS} "${GTMS_PROJECT_ROOT}/go.sum" "${REMOTE_USER}@${REMOTE_HOST}:C:/gtms-workspace/${REMOTE_SLOT}/"
scp ${SCP_OPTS} -r "${GTMS_PROJECT_ROOT}/cmd/" "${REMOTE_USER}@${REMOTE_HOST}:C:/gtms-workspace/${REMOTE_SLOT}/"
scp ${SCP_OPTS} -r "${GTMS_PROJECT_ROOT}/internal/" "${REMOTE_USER}@${REMOTE_HOST}:C:/gtms-workspace/${REMOTE_SLOT}/"

# Run BATS remotely
OUTPUT=$(ssh -p ${SSH_PORT} "${REMOTE_USER}@${REMOTE_HOST}" \
  "${BASH_CMD} -c \"cd ${REMOTE_DIR} && bats ${GTMS_ARTEFACT_FILE} 2>&1\"") || EXIT_CODE=$?
EXIT_CODE=${EXIT_CODE:-0}

# Update result contract
STATUS="complete"; [ "${EXIT_CODE}" != "0" ] && STATUS="error"
cat > "${GTMS_RESULT_FILE}" <<EOF
task: ${GTMS_TASK_ID}
command: execute
target: ${GTMS_ARTEFACT_FILE}
adapter: remote-bats
mode: sync
status: ${STATUS}
summary: "Tests ${STATUS} on ${REMOTE_HOST}"
completed: $(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF

echo "${OUTPUT}"
exit ${EXIT_CODE}
```

**Lean adapter** (Tier 2, sync) — SSH only, no file sync:

```bash
#!/bin/bash
# adapters/remote-bats-lean.sh — same as above but without the SCP block.
# Pre-sync files manually before bulk runs:
#   VPS_SLOT=$(basename "$(pwd)")
#   scp -P 2222 -r test/ cmd/ internal/ go.mod go.sum gtms.config \
#     gtms/test/cases/ gtms/automation/ test-fixtures/ adapters/ \
#     Administrator@win-runner-1:C:/gtms-workspace/${VPS_SLOT}/
```

**Lessons learned (validated 2026-03-22):**

- **SSH default shell on Windows Server**: the `DefaultShell` registry key (`HKLM:\SOFTWARE\OpenSSH`) doesn't reliably change the SSH session shell. Workaround: invoke bash explicitly in every SSH command: `ssh host "\"C:/Program Files/Git/bin/bash.exe\" -c \"command\""`
- **Port 22 may be blocked**: hosting providers often block port 22. Use an alternate port (e.g. 2222) in `sshd_config`.
- **SCP per test case doesn't scale**: for 300 tests, per-TC file sync takes hours. Use the lean adapter with a one-time manual sync instead.
- **BATS on Windows needs Git Bash**: install Git for Windows on the remote machine. BATS runs fine under Git Bash. No WSL or Cygwin needed.
- **SSH-triggered runs aren't visible in RDP**: SSH and RDP use separate Windows sessions. For demo visibility, trigger tests from a terminal in the RDP session, or use a file-watcher/scheduled-task mechanism.
- **Go may be needed on remote**: if BATS tests call `go build` in `setup_file()`, Go must be installed on the remote machine. Consider a `GTMS_BIN` env var override to skip remote builds.
- **SCP sync doesn't handle deletions**: `scp -r` copies files to the remote but never deletes files that were removed locally. If a test asserts a file was deleted, the stale file persists on the remote and the test fails. Fix: `ssh host "del C:/path/to/stale-file"` (Windows) or `ssh host "rm -f /path/to/stale-file"` (Linux) before running. Note: Windows SSH default shell is `cmd.exe`, so use `del` not `rm`.
- **SCP overwrite of `gtms.exe` is unreliable on Windows**: after rebuilding `gtms.exe` locally, a subsequent `scp` to an existing remote copy can silently leave the old binary in place (likely Windows file-locking or AV interference), even though `scp` reports success. Symptom: BATS runs still exhibit pre-fix behaviour and "fixture bugs" appear in tests that were already green locally. Fix: always remove the remote binary before copying — `ssh host "del C:/gtms-workspace/${VPS_SLOT}/gtms.exe" && scp gtms.exe host:C:/gtms-workspace/${VPS_SLOT}/`. Worth building into any pre-run sync step for lean adapters.
- **Concurrent agents overwrite each other's binary**: when multiple agents run against the same VPS in parallel, `scp gtms.exe` from one agent overwrites the binary while another agent's BATS tests are mid-run, causing intermittent failures that pass on retry. Fix: `remote-bats-execute.sh` now uses per-invocation slot isolation -- `REMOTE_SLOT="${PROJECT_SLOT}-${GTMS_TASK_ID}"` gives each task its own VPS directory and binary copy. Lean scripts (`remote-bats-lean.sh`, `remote-pester-lean.sh`) keep the shared project-scoped slot since they don't sync files; their headers document the concurrent-sync risk for manual pre-sync. Cleanup: stale per-invocation slots accumulate on the VPS (~5MB each); remove with `ssh host "rm -rf /c/gtms-workspace/gtms-v1-task-*"` after batch runs.
- **Lean-adapter pre-sync scope is narrower than product scope**: the default pre-sync list (`test/`, `cmd/`, `internal/`, `go.mod`, `go.sum`, `gtms.config`, `gtms/test/cases/`, `gtms/automation/`, `test-fixtures/`, `adapters/`) does **not** include `.github/`, `.claude/`, or `reference/`. BATS tests that `grep` product files in those directories for content (common when the feature-under-test is a workflow YAML, a skill markdown, or a reference doc -- e.g. 11 file-shape TCs against `.github/workflows/bats.yml`, `.claude/commands/tests-execute.md`, and `reference/ai-coding-assistant-guide.md`) will fail on the VPS with empty grep output even though they pass locally. Symptom: first VPS run shows ~half the TCs failing with `grep -qE '...'` assertions that matched locally. Fix: extend the pre-sync for that run to include the specific non-default paths the tests read from (`scp -r .github/ .claude/ reference/ host:...`), or fall back to the full-sync adapter. Consider whether the test really needs a file-shape assertion against a non-Go source file, or whether the same behaviour can be exercised through the CLI — see ai-coding-assistant-guide's "BATS boundary rule".
- **Lean-adapter on a fresh worktree fails until the VPS slot is primed**: `remote-bats-lean` skips the SCP block by design -- it assumes `/c/gtms-workspace/{slot}/` already contains a current `gtms.exe`, fixtures, and `gtms.config`. From a fresh `.claude/worktrees/agent-XXX/` slot, that directory is empty (or missing), so the lean adapter fails with `Malformed or missing TAP output from win-runner-1 (exit 1)` -- looks like a real test failure but is actually an unprimed slot. Fix: do the first bulk run of any new worktree with `remote-bats` (auto-syncing). After that initial bulk, switch to `remote-bats-lean` for single-TC re-runs and follow-up bulks -- the slot is now primed and lean is ~3× faster. The `/tests-execute` skill defaults correctly: `remote-bats` for the first bulk, lean only when files have already been synced.
- **Top-level docs are now part of the per-test sync**: both `remote-bats-execute.sh` (Windows VPS) and `remote-bats-unix-execute.sh` (Linux VPS) now `scp` `USER-GUIDE.md`, `README.md`, `ARCHITECTURE.md`, `CONTRIBUTING.md`, and `CLAUDE.md` to the workspace alongside `gtms.config`. `scripts/remote-full-run-unix.sh` includes `USER-GUIDE.md` in its top-level doc sync. This closes the gap where BATS tests that assert on doc-content (e.g. the manual-authoring section of `USER-GUIDE.md`) had to fall back to local `bats-runner` because the asserted-on doc wasn't present on the VPS. Rule for new top-level docs that BATS needs to assert on: extend the `for doc in USER-GUIDE.md README.md ...` loop in both adapter scripts AND the script-side `scp` line in `scripts/remote-full-run-unix.sh`. The sync is `scp ... 2>/dev/null || true` per file, so a missing doc on the local side is silently skipped -- adding a new file is additive and safe.

---

## Multi-Framework Adapters

A single test case can have wiring records from multiple frameworks. Each framework gets its own wiring record (`tc-xxx--{framework}.wiring.yaml`) with independent execution results. This is a key feature for teams that need to validate the same test case across different tools, platforms, or configurations.

### Framework Resolution Order

GTMS resolves the framework value using a three-step precedence chain (see `internal/adapter/framework.go`):

1. **`--framework` CLI flag** — explicit override (rare, for one-off runs)
2. **`framework` field in adapter config** — normal case (set in `gtms.config`)
3. **Adapter name** — last-resort fallback for backward compatibility

Most adapters should set `framework` in their config. The CLI flag exists for cases where you need to temporarily override (e.g. running a desktop adapter config against a mobile environment).

### How it works

The `framework` config field determines the record filename. Two adapters with **different** framework names produce **separate** records that coexist:

```yaml
adapters:
  automate:
    pw-desktop:
      mode: sync
      framework: pw-desktop
      script: gtms/adapters/pw-desktop-automate.sh
    pw-mobile:
      mode: sync
      framework: pw-mobile
      script: gtms/adapters/pw-mobile-automate.sh
  execute:
    pw-desktop:
      mode: sync
      framework: pw-desktop
      script: gtms/adapters/pw-desktop-execute.sh
    pw-mobile:
      mode: sync
      framework: pw-mobile
      script: gtms/adapters/pw-mobile-execute.sh
```

Running both adapters against the same test case:

```bash
gtms automate tc-a1b2c3d4 --adapter pw-desktop
gtms automate tc-a1b2c3d4 --adapter pw-mobile
```

Produces two independent wiring records:

```
gtms/automation/wiring/tc-a1b2c3d4--pw-desktop.wiring.yaml
gtms/automation/wiring/tc-a1b2c3d4--pw-mobile.wiring.yaml
```

Each can be executed independently and tracks its own artefact and hashes.

### Use cases

- **Cross-platform testing** — same test case automated for desktop and mobile (Playwright), or Linux (BATS) and Windows (Pester)
- **Multiple test frameworks** — unit tests (Jest) and E2E tests (Cypress) both validating the same requirement
- **Environment-specific runners** — staging vs production adapters producing separate result trails

### Important: framework names must be unique per command

Two adapters under the same command with the **same** framework name will overwrite each other's automation records. The framework name is the key — choose distinct, descriptive names:

| Instead of | Use |
|-----------|-----|
| `playwright` + `playwright` | `pw-desktop` + `pw-mobile` |
| `bats` + `bats` | `bats-linux` + `bats-windows` |
| `pytest` + `pytest` | `pytest-unit` + `pytest-integration` |

### Dashboard display

`gtms status` currently shows one framework's result per test case (selected by the `defaults.execute` framework or highest cycle count). To see all frameworks, use `gtms status --json`. Full multi-framework dashboard display is planned.

---

## Security Considerations

### Command Injection in Tier 1 Templates (CRIT-1 — Fixed)

**Status: Fixed.** All Tier 1 template values are now shell-escaped before substitution using single-quote wrapping with internal quote escaping. This prevents command injection via target IDs, prompt content, and other user input.

**Residual considerations for adapter authors:**
- Shell escaping protects against injection but very long values (`{guides}`, `{context}`) may hit shell argument length limits
- For security-sensitive contexts with large content, prefer Tier 2 scripts which receive content via environment variables

### Tier 2 Environment Isolation

**BREAKING CHANGE:** Tier 2 scripts no longer inherit the full parent process environment. Scripts receive only a minimal allowlist of system variables plus all `GTMS_*` variables.

**Allowlist:** `PATH`, `HOME`, `TMPDIR`, `USER`, `SHELL`, `LANG`, `LC_ALL` (+ `USERPROFILE`, `SYSTEMROOT`, `COMSPEC`, `PATHEXT`, `TEMP`, `TMP` on Windows).

**Migration:** If your Tier 2 script relied on inherited environment variables (e.g. `GOPATH`, `NODE_PATH`, `API_KEY`), you have two options:
1. Export them inside the script: `export GOPATH=/path/to/go`
2. Use a wrapper script that sources the needed vars before calling your adapter

**Note:** Tier 1 adapters still inherit the full parent environment (Go default for `exec.Command` without `Env` set). This asymmetry is intentional — Tier 1 commands run via `sh -c` and may need `PATH`, `GOPATH`, etc. from the parent.

**Gotcha: wrapper scripts can't pass env vars to Tier 2 adapters.** If a wrapper script (e.g. `remote-full-run.sh`) exports a custom env var and then calls `gtms execute`, that var will **not** reach the Tier 2 adapter script — `minimalEnv()` rebuilds the environment from scratch. If both a wrapper and an adapter need to agree on a value, derive it from the same source inside each script (e.g. both read `GTMS_PROJECT_ROOT`) rather than passing it via an env var. Discovered 2026-04-17 when `VPS_SLOT` was set by the sync script but stripped before reaching the adapter, causing sync and execution to target different VPS directories.

### Input Validation

**Filename-component safety.** All package-level functions that build filenames from caller-supplied identifiers -- test case IDs, framework names, task IDs -- now validate those components via `internal/pathsafe.ValidateFilenameComponent` before embedding them in a `filepath.Join` call. This covers all 8 write/read sites in `internal/pipeline/` and `internal/execution/`. The validator rejects empty strings, path separators (`/`, `\`), traversal sequences (`..`), the current-directory alias (`.`), and control characters. At the CLI layer, `gtms link` now runs `validateTargetID()` before `isTestCaseID()`, matching the guard chain used by `automate` and `execute`.

**Adapter authors** do not need to sanitise TC IDs or framework names for path safety — GTMS enforces this at the package boundary. However, adapters that use identifiers in other sensitive contexts (shell commands, API calls, URLs) should still validate their inputs.

---

## Current Limitations

These are known gaps between the documented contract and the current implementation. They are tracked and will be addressed in future releases.

| Limitation | Impact | Reference |
|-----------|--------|-----------|
| ~~Worktree isolation not wired in~~ | **Corrected.** `GTMS_WORK_DIR` is exported and carries the task's working-directory base (the worktree path when the command created one, else the project root), distinct from `GTMS_PROJECT_ROOT`. Concurrent agents are isolated by running in separate git worktrees managed by the external orchestration layer. |  |
| ~~Tier 1 artefact field not set (non-streaming)~~ | **Fixed.** GTMS now scans the output directory for new files when no streaming delimiters are found. |  |
| ~~Windows `cmd /c` fallback for Tier 2~~ | **Tier 1 fixed.** Tier 1 falls back to `cmd /c` on Windows when `sh` is not found. Tier 2 still requires `sh` -- no fallback. Install Git Bash or WSL for Tier 2 scripts on Windows. | REV-002 CRIT-3 |
| Exit code extraction uses Unix-specific syscall | On Windows, non-zero exit codes always reported as 1 (no diagnostic detail) | REV-002 CRIT-2 |
| Async status polling only for execute | `status-script` is only called by `gtms execute status`. Create and automate status commands don't poll. | REV-002 |
| Stdout streaming requires `<gtms-file>` tags | Streaming only activates when adapter output contains `<gtms-file>` tags. Plain stdout is not captured to files. |  |
| ~~No input sanitization on target IDs~~ | **Fixed.** All filename-construction sites validate identifier components via `internal/pathsafe.ValidateFilenameComponent`. Path separators, traversal sequences, and control characters are rejected at the package boundary. `gtms link` CLI guard gap also closed. |  |
| Errors silently swallowed on state transitions | `task.Move()` and `result.Update()` failures discarded in some paths | REV-002 |
| ~~`--env` flag not implemented~~ | **Fixed.** `--env` flag available on `gtms automate` and `gtms execute`. Threaded to `{environment}` (Tier 1), `GTMS_ENVIRONMENT` (Tier 2), and `{environment}` in prompt templates. |  |
| ~~`--dry-run` flag not functional~~ | **Fixed for `delete` and `reset`.** Both honour `--dry-run` as a local flag (the root-position `gtms --dry-run delete` form also works); the flag is hidden from every other command's help and parses as a silent no-op there. Per-command `--dry-run` for the remaining mutating commands is open as future work. |  |
| ~~Timeout kills process (SIGKILL)~~ | **Fixed.** Cancellation now kills the full process tree: Unix uses `setpgid` + `kill(-pgid, SIGKILL)`; Windows uses `taskkill /T /F /PID`. A 30-minute default timeout applies to all sync adapters when no `timeout:` is configured. |  |

---

## Related Documents

| Document | Purpose |
|----------|---------|
| [USER-GUIDE.md](../USER-GUIDE.md) | Complete feature reference -- command flags, config fields, prompt templates |
| [ARCHITECTURE.md](../ARCHITECTURE.md) | Package map, data flow, how to add commands |
