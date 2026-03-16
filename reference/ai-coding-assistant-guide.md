# AI Coding Assistant Integration Guide

*How AI coding tools (Claude Code, Cursor, Copilot, etc.) work with GTMS.*

Architectural decisions that affect AI coding assistant integration are captured in ADRs — those record the *why*: what was decided, what alternatives were considered, and the rationale for the choice. This guide captures the *how*: which commands to call, when, and in what order. When a new ADR is created that changes how AI tools should interact with GTMS, this guide should be updated with the practical guidance.

---

## Principle

**GTMS is to testers and test cases what git is to developers and source code.** Git tracks, versions, and manages code. GTMS tracks, orchestrates, and manages the test lifecycle. They're not the same kind of tool — but they operate the same way: as CLI tools that AI coding assistants invoke through shell commands.

AI coding assistants invoke GTMS through its CLI — the same way they invoke git. They don't write git objects directly; they run `git commit`. They don't create GTMS pipeline artifacts directly; they run `gtms create`. See [ADR-009](adr/ADR-009-cli-as-integration-surface-for-ai-tools.md) for the rationale behind this decision.

---

## Deterministic by Design: Why GTMS Is Not a Skill

Git became the foundation of modern software development for one reason: it is completely deterministic. `git commit` either commits or it doesn't. `git status` reports exactly what's on disk. There is no "pass rate," no confidence interval, no statistical measure of reliability. It works, every time, the same way.

GTMS is built on the same principle. Every GTMS command is a deterministic operation:

- `gtms create` creates a task file and result contract, or returns an error — same inputs, same outcome, every time
- `gtms status` reads the filesystem and reports what's there — no interpretation, no variation
- `gtms gaps` scans records and reports what's missing — binary yes/no per category
- `gtms execute` invokes an adapter, records the exit code, updates the result contract — mechanical, repeatable

This is a deliberate architectural choice, not an implementation detail. AI agent skills are probabilistic by nature — their authors measure "pass rates" and run benchmarks to establish *confidence* that the skill works. The very existence of a pass rate metric is an admission that the skill might not work next time. A model update, a context change, a slightly different phrasing — any of these can shift the outcome.

GTMS sits on the opposite end of that spectrum. The orchestration layer — task lifecycle, file management, status tracking, gap analysis, triage routing — is deterministic infrastructure. An AI coding assistant should interact with it the same way it interacts with git: run the command, trust the result.

**Where the agentic work belongs:** The *adapters* are where probabilistic, AI-driven work happens. An adapter might use an LLM to generate test cases from a requirement, or an AI coding agent to write automation scripts. That work is creative, non-deterministic, and may vary between runs. But GTMS contains that uncertainty within the adapter boundary. The pipeline around it — task tracking, status reporting, gap analysis, triage classification — remains deterministic regardless of what the adapter does.

This separation is the architectural guarantee: **GTMS orchestrates reliably; adapters create freely.** The pipeline never has a "pass rate." It either works or it reports an error. AI coding assistants should treat it accordingly — as infrastructure to invoke, not a skill to hope triggers correctly.

## ToDo

**This document needs to be updated to reflect all the design decisions and implemenation work that has been completed up to 4th March 2026.** The following To Do list covers areas of documentation that we need to review and some point and, where relevant, capture in this document.

 - [ ] Review Prp documents
 - [ ] Review ADR documents
 - [ ] Review code base
 - [ ] Review helpd commands

---

## 1. The Integration Point: `gtms create`

`gtms create` is the integration boundary between the outside world and the GTMS pipeline.

**On the outside:** the requirement source — a PRP, a specification document, a feature description. This is what test cases will be built from. The AI assistant or human points `gtms create` at this source material.

**On the inside:** the adapter pipeline — prompt templates, guide directories, the configured adapter, output formatting, file naming conventions. GTMS handles the process of turning that source material into structured test case specifications.

The developer doesn't need to know what happens on the inside. They provide the *what* (the requirement), GTMS handles the *how* (the adapter, the template, the lifecycle).

Three actors converge at this point:
- **The human** knows *what* needs testing — the requirement, the business context, the risk
- **The AI assistant** knows *how* to invoke it — it reads the PRP, understands the context, constructs the right `gtms create` command
- **GTMS** knows *how to process it* — it resolves the adapter, assembles the prompt, invokes the adapter, tracks the lifecycle

Everything downstream (automate, execute, status, gaps, triage) flows from `gtms create`.

---

## 2. GTMS Commands for AI Assistants

These are the commands an AI coding assistant should know:

### Pipeline operations

| Command | When to use | What it does |
|---------|-------------|--------------|
| `gtms create <requirement>` | After a PRP is written or a requirement is identified | Creates test case specifications via the configured adapter |
| `gtms automate <tc-id>` | After test cases exist | Generates automation scripts via the configured adapter |
| `gtms execute <tc-id>` | After automation exists | Runs the automated test and records the result |
| `gtms triage <tc-id>` | After a test fails | Classifies the failure and triggers follow-on action |

### Pipeline visibility

| Command | When to use | What it shows |
|---------|-------------|---------------|
| `gtms status` | Anytime — the dashboard | Pipeline overview: which test cases are created, automated, executed, passing/failing |
| `gtms status <tc-id>` | Investigating one test case | Detail view for a single test case across all pipeline stages |
| `gtms gaps` | After changes — what's missing? | Four categories: no test cases, no automation, never executed, currently failing |
| `gtms map` | Understanding traceability | Links between requirements, test cases, automation, and execution |

### Project setup

| Command | When to use |
|---------|-------------|
| `gtms init` | Once, when setting up GTMS in a new project |

---

## 3. Slash Command Pattern

AI coding assistants can provide slash commands as convenience wrappers around GTMS CLI invocations. The slash command constructs the right `gtms.exe` invocation — it does not bypass the CLI.

### `/gtms-create`

The primary integration slash command. Takes a requirement reference and invokes `gtms.exe create`.

**Usage:**
```
/gtms-create PRPs/PRP-whatever.md
/gtms-create REQ-123
/gtms-create "automate command error handling"
```

**What it does:**
1. Validates the input (file exists, requirement is specified)
2. Runs: `gtms.exe create <requirement> --context-file <requirement>` (if input is a file path)
3. Shows the adapter output
4. Runs: `gtms.exe status` to show the updated pipeline

**What it does NOT do:**
- Generate test case files directly
- Read `gtms.config` to determine adapter configuration
- Replicate prompt template or guide directory logic
- Know or care which adapter tier handles the request

### Other potential slash commands

| Command | Wraps | Purpose |
|---------|-------|---------|
| `/gtms-status` | `gtms.exe status` | Quick pipeline check |
| `/gtms-gaps` | `gtms.exe gaps` | What needs attention? |
| `/gtms-execute <tc-id>` | `gtms.exe execute <tc-id> --adapter <adapter>` | Run a test through the pipeline |

These are optional — the AI assistant can always run the `gtms.exe` commands directly via bash.

---

## 4. Development Workflow Integration

### PRP lifecycle → GTMS pipeline

The natural integration point is during PRP execution. After the code is written and reviewed, test cases are created from the PRP:

```
Write PRP
  → Review PRP (/review-prp)
  → Execute PRP (/prp-base-execute)
    → Code changes implemented
    → Code reviewed (/review-general)
    → Test cases created: gtms create <PRP-file>      ← integration point
    → Tests automated: gtms automate <tc-id>
    → Tests executed: gtms execute <tc-id>
    → Pipeline validated: gtms status / gtms gaps
```

The PRP *is* the requirement document. Passing it to `gtms create` gives the adapter all the context it needs to generate meaningful test case specifications.

### Continuous visibility

During any development session, the AI assistant can run `gtms status` and `gtms gaps` to understand what's covered and what's missing. This should happen:

- **At the start of a session** — understand current state
- **After creating test cases** — verify they appeared in the pipeline
- **After executing tests** — check for failures
- **Before committing** — confirm no new gaps introduced

---

## 5. Understanding Execution Results

GTMS tracks test execution as **binary pass/fail only**. This is a deliberate design choice, not a limitation to work around. See [ADR-008](adr/ADR-008-binary-pass-fail-execution-results.md) for the full rationale.

### What GTMS records

When `gtms execute` completes, two fields are updated in the automation record (`test-automation/records/tc-xxx.automation.md`):

| Field | Value | Source |
|-------|-------|--------|
| `last-formal-result` | `pass` or `fail` | Adapter exit code (0 = pass, non-zero = fail) |
| `last-formal-run` | File path | Path to the framework's native result file |

That's it. No skipped, broken, flaky, or per-assertion detail. The exit code is the one signal every adapter universally produces — Playwright, BATS, pytest, JUnit runners, custom scripts. GTMS doesn't parse framework-specific output.

### Where the rich detail lives

The framework's native output (Playwright HTML report, JUnit XML, Allure data) lives wherever the adapter wrote it. The `last-formal-run` field points to it. If you need assertion-level detail, open that file — don't look for it in GTMS.

### How failures get classified

The triage command fills the gap between "it failed" and "why it failed":

```bash
gtms triage tc-007 --automation-wrong --summary "Selectors changed"
gtms triage tc-007 --test-wrong --summary "Expected result changed"
gtms triage tc-007 --app-wrong --defect JIRA-789 --summary "Payment gateway 500"
```

Each category triggers different follow-on actions:

| Category | What it means | What GTMS does |
|----------|---------------|----------------|
| `--automation-wrong` | The test script is broken | Sets automation status to `rework`, creates a new automate task |
| `--test-wrong` | The test case itself is wrong | Flags the test case as `needs-review` |
| `--app-wrong` | The application has a bug | Links the defect, keeps the test marked as failing |

**For AI assistants:** After `gtms execute` reports a failure, the next step is always triage — not re-running the test, not parsing the result file. Triage is how the pipeline decides what happens next.

### Two kinds of failure — know the difference

When `gtms execute` reports a non-zero exit code, an AI assistant must distinguish between two fundamentally different situations:

**1. Test failure (the test ran, the subject failed)**

The test script executed correctly — BATS parsed it, Playwright launched the browser, pytest collected the tests. The test did its job: it checked the application's behaviour and found a problem. The exit code means "the thing I tested is broken."

- **Dashboard shows:** EXECUTE ✓ (or ✗), LAST RESULT `fail`
- **What to do:** `gtms triage` — classify as `--app-wrong`, `--test-wrong`, or `--automation-wrong`
- **The fix belongs to:** the application, the test case spec, or the automation script — triage decides which

**2. Automation defect (the test itself is broken)**

The test script couldn't even run. BATS couldn't parse it (syntax error, stray text in the file). Playwright crashed on import. The test framework rejected the artefact before it could test anything. The exit code means "the test script is defective."

- **Dashboard shows:** EXECUTE `—`, no result recorded (the test never actually executed)
- **What to do:** fix the automation artefact, then re-execute. This is NOT a triage situation — there's nothing to classify because no test ran. The problem is in the AUTOMATE stage, not the EXECUTE stage.
- **The fix belongs to:** the generated test script. Either re-run `gtms automate` to regenerate it, or manually fix the `.bats`/`.spec.ts` file.

**Why this matters for AI assistants:** An AI agent that treats all non-zero exit codes the same will triage automation defects as test failures, polluting the pipeline with false classifications. The correct logic is:

```
if exit_code != 0:
    if test_framework_parsed_and_ran_tests:
        → this is a test failure → triage it
    else:
        → this is an automation defect → fix the script → re-execute
```

**Real-world example (discovered during dogfooding):** The BATS automate adapter generated `.bats` files where Claude's commentary text ("The file write was blocked by permissions...") leaked into the script. BATS tried to execute those lines as shell commands and failed with `command not found`. These are automation defects — the test never ran, the feature was never tested, and triaging them would be meaningless.

### Debugging failures: always go back to the spec

When a test fails — whether it's a test failure or an automation defect — the test case specification (`test-cases/tc-XXXXXXX-*.md`) is the authoritative reference for what the test should be doing.

The debugging workflow is:

```
1. Test fails
2. Read the test case spec — what is the test supposed to verify?
3. Read the automation artefact — does the script correctly implement the spec?
4. Identify the gap:
   a. Script doesn't match spec → automation defect → fix the script
   b. Script matches spec but spec is wrong → test case defect → update the spec
   c. Script matches spec, spec is correct, subject fails → real failure → triage it
```

**This is the traceability loop that makes GTMS valuable.** The test case spec is human-readable, framework-agnostic, and describes *what* to verify — not *how*. The automation artefact is framework-specific and describes *how*. When something breaks, comparing the two tells you exactly where the problem is.

**For AI assistants:** When debugging a test failure, always read the test case spec first (`test-cases/tc-XXXXXXX-*.md`), then the automation artefact (`test/acceptance/*.bats` or equivalent). Don't try to fix a failing test by reading only the test script — the spec is the source of truth for what the test should assert.

**Real-world example (discovered during dogfooding):** A BATS test for config output-dir override used `echo output_dir={output_dir}` as the mock adapter, then asserted `"custom/output"` appeared in the CLI output. The test failed because GTMS's CLI output shows the task summary, not the adapter's raw stdout. Going back to the test case spec revealed the intended verification: "config output-dir takes precedence over CWD." The spec was correct but the automation approach was wrong — the script needed a different strategy to verify the behaviour (e.g. a streaming mock that writes files, then checking where they landed).

### What NOT to do

- Don't try to extract skipped/broken/flaky status from framework output and write it to the automation record — GTMS doesn't track those states
- Don't treat a failed execution as an error in the pipeline — failures are expected; triage handles them
- Don't re-run a failing test hoping for a different result — triage it first, fix the root cause, then re-execute
- Don't triage an automation defect as a test failure — if the test framework couldn't parse the script, there's nothing to triage. Fix the script first.
- Don't fix a test by reading only the automation script — always go back to the test case spec to understand what the test is supposed to verify

---

## 6. The Feature Validation Workflow

This is the core workflow for using GTMS to validate a feature implementation. It was proven during ENH-036 dogfooding and is the pattern to follow for every feature, bug fix, or enhancement.

### The pattern

```
1. IMPLEMENT the feature (on a branch or worktree)
2. CREATE test cases:     gtms create <ref> --context-file <PRP-or-ENH-doc>
3. REVIEW a sample:       read 2-3 generated test case specs for quality
4. AUTOMATE:              gtms automate <tc-id> --adapter <adapter> --framework <framework>
5. EXECUTE:               gtms execute <tc-id> --adapter <runner>
6. DEBUG failures:        spec → script → identify gap → fix → re-execute
7. CHECK the dashboard:   gtms status / gtms gaps
8. MERGE:                 implementation + test cases + BATS scripts, all together
```

### Step by step

**Step 1: Feed the requirement document to `gtms create`**

The PRP, ENH document, or bug record *is* the requirement source. Pass it as context:

```bash
gtms create my-feature --reference ENH-036 --context-file PRPs/complete/PRP-ENH-036-subfolder-organisation.md
```

The first argument (`my-feature`) is the target folder — files will be created in `test-cases/my-feature/`. The `--reference` flag provides a label that flows through to `{reference}` in templates and `$GTMS_REFERENCE` for scripts. The `--context-file` provides the actual content the adapter uses to generate test cases.

**Important:** The `--reference` value flows through to the `requirement:` frontmatter field in each generated test case (via `{reference}` in the prompt template). This is the value `gtms map` uses to group test cases by requirement. Use a stable, human-readable identifier (e.g. `BUG-022`, `ENH-036`) — not a file path or code location, which will break when lines shift during refactoring.

**Step 2: Review before automating**

Don't blindly automate all generated test cases. Pick 2-3, read the specs, check that:
- The objective is clear and testable
- Preconditions describe a realistic fixture
- Steps are specific enough to translate into assertions
- Expected outcomes are verifiable (not vague)

If a test case spec is weak, the automated script will be weak. Fix the spec first.

**Step 3: Automate and execute a small batch first**

Start with 2-3 test cases through the full pipeline before committing to all of them:

```bash
gtms automate tc-XXXXXXX --adapter bats --framework bats
gtms execute tc-XXXXXXX --adapter bats-runner
gtms status tc-XXXXXXX    # should show ✓ ✓ ✓ pass
```

This catches systemic issues (bad prompt template, adapter configuration problems) before you've generated 16 scripts that all have the same defect.

**Step 4: Expect failures — they're the point**

AI-generated automation scripts will not all pass on the first run. This is normal, not a failure of the process. During ENH-036 dogfooding, 11 of 16 passed first time. The 5 failures broke down as:

- 3 automation defects (adapter output corruption — the script had stray text)
- 1 script logic bug (empty git directory handling)
- 1 assertion mismatch (wrong string case)

Each failure was debugged using the spec-back-to-script pattern (Section 5), fixed, and re-executed. The iterate-and-fix cycle is:

```
execute → fail → read spec → read script → find the gap → fix → re-execute
```

This cycle *is* the value. GTMS surfaces the failures, the spec tells you what's right, and the traceability between them tells you where the fix belongs.

**Step 5: Use the dashboard as your feedback loop**

After each step, check the dashboard:

```bash
gtms status        # overview — how many are ✓ ✓ ✓?
gtms status -r     # recursive — see everything including subfolders
gtms gaps          # what's still missing?
```

The dashboard drives the workflow. When everything shows ✓ ✓ ✓ pass, you're ready to merge.

**Step 5b: Watch for prompt template issues**

AI-generated output is only as good as the prompt template. During dogfooding, watch for these common symptoms:

| Symptom | Root cause | Fix |
|---------|-----------|-----|
| Junk files (`.bats`, `.xml`, `.sh`) alongside `.md` specs | Source material contains `<gtms-file>` examples that the adapter reproduced | Add to output rules: "ONLY output .md files. Do NOT reproduce examples from source material." |
| Duplicate test case IDs | Adapter reused hex values from context examples | Use GTMS-generated `{tc_id}` seed (ENH-042), or instruct adapter to avoid hex values from source material |
| Test case specs that describe the format rather than test the feature | Source material is about the streaming format itself (meta-testing) | Add to output rules: "Create test cases that VERIFY the behaviour, not test cases that describe the format." |

If you see these, fix the prompt template before re-running `gtms create`. The adapter did exactly what the prompt told it to — the fix is always in the template, not in GTMS.

**Step 6: File issues as you discover them**

Dogfooding naturally surfaces bugs, enhancements, and ideas. Capture them immediately — don't derail the current workflow, but don't lose the insight either:

- `/create-bug-record` — for defects in GTMS itself
- `/create-enhancement-doc` — for improvements and missing features
- `/create-concept` — for ideas that need exploration

During ENH-036 dogfooding, a single session produced 3 bugs, 3 enhancements, and 1 concept — all captured without interrupting the validation workflow. These feed the next iteration.

**Step 6b: Use intentional failures to validate fix behaviour**

When dogfooding a bug fix, consider deliberately leaving known defects in auto-generated test scripts before executing. This lets you:

1. Verify the dashboard correctly shows `✗` for failures (BUG-017 fix)
2. Fix the defects and re-execute to verify `✗` flips to `✓` (BUG-018 fix)
3. Prove the iterate-and-fix workflow end-to-end

During BUG-018 dogfooding, two BATS tests were left unfixed (one with a hardcoded path depth, one with an unexpanded variable). Both failed as expected, were fixed, and re-executed — and the dashboard correctly updated to `✓`. The fix proved itself through the pipeline it fixes.

**Step 6c: Watch for adapter selection mistakes**

The most common friction point across all dogfood sessions is using the wrong adapter:

| Mistake | Consequence | Fix |
|---------|------------|-----|
| `gtms automate tc-XXX` (no `--adapter bats`) | Files land in `test-automation/specs/local-claude/` instead of `test/acceptance/` | Use `--adapter bats --framework bats` |
| `gtms execute tc-XXX` (no `--adapter bats-runner`) | Defaults to `local-runner` (Playwright), fails immediately | Use `--adapter bats-runner` |
| Moving files without updating automation records | `bats-runner` can't find the spec file, execution fails | Update `artefact:` path in each automation record after moving |

Until ENH-043 (`{output_subdir}`) is implemented, moving files to subdirectories and updating automation records is a manual step after every automate run.

**Step 7: Merge implementation and tests together**

The feature implementation, the test cases, the automation scripts, and the pipeline artefacts all merge in one go. The tests validate the feature on the branch; when they pass, everything merges together. This ensures:

- The feature is never merged without tests
- The tests are never merged without the feature they test
- The pipeline history (task files, automation records) travels with the code

### When to create the tests: before or after implementation?

This is the first strategic decision when adopting GTMS, and getting it right is a key success factor.

**Approach A — Tests as part of the PRP (TDD-style)**

The PRP's implementation tasks include "write test cases and automation scripts." The agent writes them alongside the code changes in a single pass. This is classic TDD: define the expected behaviour first, then implement until the tests pass.

**Approach B — Tests after implementation**

Code changes first, then a separate step to create test cases and automate them against the actual behaviour. Two passes, but the tests are written against what was built, not what was predicted.

**Start with Approach B.** Here's why:

1. **Smaller learning curve.** The agent is already proven at implementing code changes via PRPs. Adding "also write tests for what you just built" is a smaller delta than "write tests first, then implement until they pass." The latter requires the agent to context-switch between test-writing and implementation, and to resist the temptation to weaken assertions rather than fix code.

2. **You can always evolve toward A.** Once the agent is reliably producing good tests after implementation (Approach B), you have the patterns and prompt templates to flip the order. B builds the muscle, A uses it differently.

3. **B is proven.** The ENH-036 dogfood cycle was: implement the feature, then `gtms create` test cases, then `gtms automate` into BATS scripts, then `gtms execute`. That's Approach B — and it surfaced real issues (3 automation defects, 1 script logic bug, 1 assertion mismatch out of 16 tests).

**When Approach A works well:** CLI tools where the contract is "given this input, expect this exit code and this output." The test case spec literally describes the command and expected output, which maps directly to a BATS assertion. You can write the test before the code exists and it'll fail predictably. Once your team is comfortable with GTMS and your test quality is consistently good, try A for a specific work item and see if it improves outcomes.

**The workflow difference in practice:**

```
Approach B (recommended starting point):
  PRP → implement code → gtms create → gtms automate → gtms execute → fix → merge

Approach A (TDD, when ready):
  PRP (includes test specs) → gtms create → gtms automate → implement code → gtms execute → fix → merge
```

Both approaches end in the same place: implementation and tests merged together, pipeline showing green. The difference is where the test creation step sits in the sequence.

### How each command determines its output directory

This is a critical distinction that trips up both humans and AI agents. Each pipeline command determines its output location differently:

| Command | Output directory determined by | What you need to do |
|---------|-------------------------------|---------------------|
| `create` | **Your current working directory (CWD)** | `cd` into the target subdirectory *before* running the command |
| `automate` | **Test case file location** + adapter `output-dir` config | Nothing — GTMS derives the subdirectory from where the `tc-*.md` file lives |
| `execute` | **Automation record** `artefact` path | Nothing — GTMS finds the test script from the automation record |

**Why `create` is different:** The `create` command generates *new* files — there's no existing test case to derive a location from. GTMS uses `DetectCreateOutputDir(root, cwd)` to decide where output lands. If you run `gtms create` from the project root, files land in `test-cases/` with no subdirectory. If you run it from `test-cases/my-feature/`, files land there.

**The `automate` and `execute` commands** operate on *existing* test cases, so they can derive the subdirectory from the test case's location in the filesystem. You can run these from any directory — they look up the target by ID.

**Common mistake:** Running `gtms create` from the project root and expecting files to land in a subdirectory. They won't — you must `cd` first:

```bash
# Wrong — files land in test-cases/ root
gtms create BUG-022 --context-file PRPs/bugs/BUG-022.md

# Right — files land in test-cases/sync-in-progress/
mkdir -p test-cases/sync-in-progress
cd test-cases/sync-in-progress
gtms create BUG-022 --context-file ../../PRPs/bugs/BUG-022.md
```

### CWD scoping for feature-based test organisation

Organise test cases in subfolders that match feature areas:

```
test-cases/
  login/
  payments/
  cwd-scoping/       ← test cases for the subfolder feature itself
```

When you `cd` into a subfolder, all GTMS commands scope to that folder:

```bash
cd test-cases/cwd-scoping/
gtms status          # shows only cwd-scoping test cases
gtms create ENH-036 --context-file ../../PRPs/complete/PRP-ENH-036.md  # output lands here
```

ID-based operations (`gtms status tc-XXXXXXX`, `gtms execute`, `gtms automate`, `gtms triage`) work globally from any directory — you don't need to be in the right folder to operate on a specific test case.

### Batch operations (current limitation)

GTMS currently processes one test case at a time. For multiple test cases, script it:

```bash
# Automate all test cases
for tc in tc-a1b2c3d tc-b2c3d4e tc-c3d4e5f; do
    gtms automate "$tc" --framework bats
done

# Execute all test cases (note: --adapter is required for non-default runners)
for tc in tc-a1b2c3d tc-b2c3d4e tc-c3d4e5f; do
    gtms execute "$tc" --adapter bats-runner
done
```

This is a known friction point (see CON-005). A future `--all` flag will automate everything that needs automating in the current scope.

### Execute requires the right adapter

The `gtms execute` command defaults to `local-runner` (Playwright). If your tests use BATS or another framework, you **must** specify `--adapter bats-runner` (or the appropriate runner). Without it, GTMS will invoke the wrong runner and all tests will fail with exit code 1.

```bash
# Wrong — uses default local-runner (Playwright), fails for BATS tests
gtms execute tc-a1b2c3d

# Right — specifies the BATS runner
gtms execute tc-a1b2c3d --adapter bats-runner
```

Check your `gtms.config` defaults section to see which adapter is the default for each command. If most of your tests use BATS, consider changing the execute default to `bats-runner`.

### Test directory organisation

Mirror the test case structure in test automation:

```
test-cases/                          test/acceptance/
  xml-tagged-streaming/       →        xml-tagged-streaming/
    tc-a1f3b72-*.md                      tc-a1f3b72-*.bats
    tc-b4e8c19-*.md                      tc-b4e8c19-*.bats
  cwd-scoping/                →        cwd-scoping/
    tc-a7b9c1d-*.md                      tc-a7b9c1d-*.bats
```

This keeps things navigable as test count grows. When creating new test cases with `gtms create`, run from the appropriate subfolder so output lands in the right place.

---

## 7. Debugging Dashboard Inconsistencies

The GTMS dashboard (`gtms status`) derives its display from task files, automation records, and result contracts on the filesystem. When the dashboard shows something unexpected, the debugging process is mechanical — trace the data back to the source files.

### The investigation process

This process was proven during BUG-015 dogfooding when the dashboard showed `✗` (failed execute) alongside `pass` (last result) for a test case — a seemingly contradictory state.

**Step 1: Identify the symptom**

```bash
gtms status -r --json
```

Use `--json` for precise field values. Look for inconsistencies:
- `execute_status: "failed"` but `last_formal_result: "pass"` — contradictory signals
- `automate_status: "none"` but you know automation exists — missing record
- `execute_status: "complete"` but `last_formal_result` is empty — result not recorded

**Step 2: Find the task files**

Task files are the primary source for dashboard status. Search for all task files related to the test case:

```bash
# Find all task files for a specific test case
find test-tasks/ -name "*tc-XXXXXXX*"
```

Check which directories they're in:
- `test-tasks/complete/` — successful completion
- `test-tasks/failed/` — failed execution
- `test-tasks/in-progress/` — still running (or orphaned)

**If multiple task files exist for the same test case**, the dashboard picks the most recent one. A stale failed task from a previous run can override a successful result from an earlier run if the failed one has a later timestamp.

**Step 3: Read the task file frontmatter**

```yaml
---
id: task-e56afc7
type: execute
target: tc-d3e4f5a
adapter: local-runner    # ← Which adapter was used?
status: failed           # ← What happened?
created: "2026-03-07"   # ← When?
error: Process exited with code 1
---
```

Key fields to check:
- **`adapter`** — Was the right adapter used? A common cause of false failures is using `local-runner` (Playwright) instead of `bats-runner` for BATS tests.
- **`status`** — Does it match what the dashboard shows?
- **`created`** — Is this the most recent task for this test case? An older successful task might be hidden by a newer failed one.
- **`error`** — What actually went wrong?

**Step 4: Check the automation record**

```bash
cat test-automation/records/tc-XXXXXXX.automation.md
```

The automation record stores `last-formal-result` and `artefact` path. If the dashboard shows a result that doesn't match the automation record, there may be a task file overriding it.

**Step 5: Identify the root cause**

Common root causes and their signatures:

| Dashboard symptom | Task file evidence | Root cause |
|---|---|---|
| `✗` but `pass` | Failed task with `adapter: local-runner`, complete task with `adapter: bats-runner` | Wrong adapter used on a later run — Playwright failed on a BATS test |
| `✗` but no error detail | Failed task with old timestamp, no recent complete task | Stale failed task from a previous attempt |
| `—` for AUTOMATE | No automation record in `test-automation/records/` | Automation was never run, or record was deleted |
| `✓` for AUTOMATE but `—` for EXECUTE | Automation record exists but no execute task file | Test was automated but never executed |

**Step 6: Resolve**

Once you understand the root cause:

- **Wrong adapter:** Delete the stale failed task file, or re-execute with the correct adapter (`--adapter bats-runner`)
- **Stale task:** Delete the orphaned task file from `test-tasks/failed/`
- **Missing record:** Re-run `gtms automate` or `gtms execute` as needed

### Real-world example: BUG-015 dogfood

During the BUG-015 dogfood session, `gtms status -r` showed:

```
tc-d3e4f5a  status-pipeline-view  ✓  ✓  ✗  pass
```

The `✗` (failed execute) alongside `pass` (last result) was contradictory. Investigation:

1. **Found two task files** for tc-d3e4f5a:
   - `test-tasks/complete/task-850c473-execute-tc-d3e4f5a.md` — `status: complete`, `adapter: bats-runner`, created March 4
   - `test-tasks/failed/task-e56afc7-execute-tc-d3e4f5a.md` — `status: failed`, `adapter: local-runner`, created March 7

2. **Root cause:** Someone ran `gtms execute tc-d3e4f5a` on March 7 without `--adapter bats-runner`. It defaulted to `local-runner` (Playwright), which naturally failed on a BATS test. The failed task (March 7) was newer than the successful one (March 4), so the dashboard showed `✗`.

3. **The `pass` in LAST RESULT** came from the automation record's `last-formal-result`, which was set by the earlier successful run on March 4. The failed run on March 7 didn't update `last-formal-result` (this is the ENH-040 gap).

4. **Resolution:** Delete the stale failed task (`test-tasks/failed/task-e56afc7-*.md`) since it was a user error, not a genuine test failure.

**Key insight:** The dashboard was telling the truth at every level — it just required understanding which source file was driving each column. The `✗` came from the most recent task file. The `pass` came from the automation record. Both were accurate for their own data source. The contradiction was in the user's action (wrong adapter), not in GTMS's reporting.

### For AI assistants: when to investigate vs. when to re-run

- **Investigate first** when the dashboard shows unexpected state — read the task files before taking action
- **Don't re-execute blindly** — if the root cause is a wrong adapter, re-executing with the same default will produce the same failure
- **Use `--json`** for all diagnostic commands — structured output is easier to parse and reason about than formatted tables
- **Check the `adapter` field** in failed task files — wrong adapter selection is the #1 cause of false failures across all dogfood sessions

---

## 8. Known Gotchas

| Gotcha | Detail | Workaround |
|--------|--------|------------|
| `CLAUDECODE` env var | When running inside Claude Code, Tier 1 adapters that invoke `claude` will fail with "cannot be launched inside another Claude Code session" | Use Tier 2 script adapters, or `unset CLAUDECODE` before running `gtms` commands |
| Adapter context | The Tier 1 adapter's Claude instance has less context than the Claude Code session | Write richer prompt templates and use `--context-file` to pass additional context |
| Windows binary | Must use `gtms.exe` not `gtms` on Windows/MINGW | Always include `.exe` suffix in slash commands and scripts |
| Execute defaults to wrong runner | `gtms execute` defaults to `local-runner` (Playwright). BATS tests silently fail with exit code 1 if the adapter isn't specified. | Always use `--adapter bats-runner` for BATS tests. Check `gtms.config` defaults section. |
| `cygpath -w` breaks script paths on MINGW | Converting paths to Windows backslash format causes `sh -c` to strip backslashes, so scripts aren't found. | Use UNIX-style paths throughout. `sh -c` handles them natively on MINGW. Never use `cygpath -w` for paths passed to shell commands. |
| `gtms create` output lands in wrong directory | `create` uses CWD to determine the output subdirectory — unlike `automate`/`execute` which derive it from the test case location. Running `gtms create` from the project root puts files in `test-cases/` with no subdirectory. | Always `mkdir -p test-cases/{slug}/ && cd test-cases/{slug}/` before running `gtms create`. See "How each command determines its output directory" in Section 6. |
| BATS `PROJECT_ROOT` must be exported | Setting `PROJECT_ROOT` in `setup_file()` without `export` makes it invisible in `setup()` subshells. Tests fail with "could not find bats-support". | Always use `export PROJECT_ROOT=...` in `setup_file()`. |

---

## 9. For AI Coding Tool Developers

If you're building or configuring an AI coding assistant to work with GTMS:

1. **Treat `gtms.exe` as a standard CLI tool** — invoke it the same way you invoke `git`, `npm`, or `make`
2. **Don't parse text output programmatically** — use `--json` flag (when available) for machine-readable output
3. **Don't bypass the CLI** — even if you could write test case files directly, go through `gtms create` so the lifecycle is tracked
4. **Read `gtms status` output to understand state** — it's the single source of truth for pipeline coverage
5. **Read `gtms gaps` output to find work** — it tells you exactly what needs attention
6. **Don't implement GTMS commands as agentic skills** — GTMS commands are deterministic operations, not probabilistic behaviors. A skill that "usually" creates a task file correctly is worse than a CLI command that always does. If your tool has a concept of skills or agent capabilities, the skill should be a thin wrapper that invokes the CLI — not a reimplementation of what the CLI does
7. **Don't edit GTMS-managed files directly** — task files, result contracts, automation records, and pipeline artifacts have specific formats, naming conventions, and lifecycle states. Writing to them directly bypasses validation, breaks the state machine, and produces artifacts that `gtms status` and `gtms gaps` can't track. Always go through the CLI, the same way you'd never write git objects by hand

---

## Related Documents

| Document | Purpose |
|----------|---------|
| [ADR-008](adr/ADR-008-binary-pass-fail-execution-results.md) | Architectural decision: binary pass/fail execution results |
| [ADR-009](adr/ADR-009-cli-as-integration-surface-for-ai-tools.md) | Architectural decision: CLI as integration surface |
| [Adapter Guide](adapter-guide.md) | Full adapter contract reference |
| [Framework Integration Guide](framework-integration-guide.md) | Connecting GTMS to test frameworks and deployment |
| [ARCHITECTURE.md](../ARCHITECTURE.md) | Package map, data flow, conventions |
