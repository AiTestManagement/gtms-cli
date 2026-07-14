# GTMS Quick Reference for AI Coding Assistants

*Compact operating reference for AI agents driving GTMS through its CLI. For
the full narrative, walkthroughs, and configuration depth, see
USER-GUIDE.md (run gtms --help for the version-pinned URL).*

GTMS is to testers and test cases what git is to developers and source code.
Invoke `gtms` the same way you invoke `git`: a standard CLI whose artefacts
are plain text files in the repo. GTMS stamps files and tracks results; you
(the agent) fill in the content.

---

## Commands

### Pipeline Commands

| Command | Syntax | Purpose |
|---------|--------|---------|
| `create` | `gtms create <folder> [name]` | Stamp or generate test case specs in `gtms/test/cases/<folder>/`. No built-in default: needs `--adapter` or `defaults.create` (every scaffolded preset sets one) |
| `prime` | `gtms prime <tc-id>` | Stamp a manual result template at `gtms/manual/records/<tc-id>--manual.result.yaml` with an empty `result:` field to fill |
| `automate` | `gtms automate [tc-id \| folder]` | Stamp a test script skeleton plus a wiring record linking it to the test case. Bare form runs every test case (bulk) |
| `execute` | `gtms execute [tc-id \| folder]` | Run the wired script, or record a primed result -- the effective adapter decides which path (see Workflow Sequences) |
| `triage` | `gtms triage <tc-id>` | Classify a failure: exactly one of `--automation-wrong`, `--test-wrong`, `--app-wrong` |

### Visibility Commands

| Command | Syntax | Purpose |
|---------|--------|---------|
| `status` | `gtms status [tc-id \| folder]` | Pipeline dashboard. Bare = folder summary; folder = per-TC table; `tc-id` = detail card |
| `gaps` | `gtms gaps [folder]` | Coverage gaps by category. Folder scope only -- a TC ID errors and points you at `gtms status <tc-id>` |
| `map` | `gtms map [tc-id \| folder]` | Test cases grouped by requirement (traceability); `--detail` for full titles |
| `list` | `gtms list <adapters\|frameworks\|all>` | Adapter and framework inventory. Bare `gtms list` prints help, not data |

### Lifecycle Commands

| Command | Syntax | Purpose |
|---------|--------|---------|
| `init` | `gtms init [--preset manual\|bats\|playwright]` | Scaffold a project. Plain `gtms init` in an empty repo = `manual` preset; in an initialised project it lists presets and exits. `--presets` lists without scaffolding |
| `link` | `gtms link <tc-id> --framework <fw> --artefact <path>` | Write or repair a wiring record by hand; `--refresh` re-hashes after a deliberate spec or script edit |
| `delete` | `gtms delete <tc-id \| folder>` | Remove specs and all pipeline artefacts (`--keep-spec` to keep specs; `--dry-run` to preview) |
| `reset` | `gtms reset [tc-id \| folder]` | Clear execute results; wiring and specs untouched (`--dry-run` to preview) |
| `version` | `gtms version` | Print the version (canonical form; `--version` also works) |

Task-level run listing: `gtms create status`, `gtms automate status`,
`gtms execute status` -- one row per invocation. Reach for these when a run
has not shown up on `gtms status` and you want to know whether the adapter is
still running or already failed. `-v` on the automate/execute detail views
adds a `Handoff:` line naming the result contract under `.gtms/results/`.

### Key Flags

| Flag | Commands | Purpose |
|------|----------|---------|
| `--adapter` | create, prime, automate, execute | Select the adapter (overrides `defaults.<command>`). On a wired execute it must agree with the wiring record's adapter |
| `--framework` | prime, automate, execute, link, triage, status, gaps, map | Framework label on write commands; selector/filter on read commands. Required on execute/triage when a TC is wired for more than one framework |
| `--reference` | create | Requirement identifier recorded in the test case (e.g. `REQ-001`) |
| `--focus` | create | Focus area within the source document (prompt-driven adapters) |
| `--context-file` | create, prime, automate | Supplementary context file for the adapter |
| `--env` | automate, execute | Target environment recorded on the run (e.g. `staging`) |
| `--executed-by` | prime, automate, execute | Operator identity (default: git `user.name`) |
| `-r, --recursive` | automate, execute, status, gaps, map, delete, reset, link (refresh) | Include subdirectories |
| `--force` | init, prime, automate, execute, link | Meaning varies: overwrite config (init), overwrite result template (prime), reprocess/overwrite (automate, link), re-run bulk skips (execute) |
| `--update-hash` | prime | Refresh `test_case_hash` only; preserves the filled `result:` and execute history |
| `--allow-stale` | execute | Skip the wiring drift check for this run only; does NOT update wiring or bypass the pending bootstrap |
| `--fail-fast` | automate, execute | Stop a bulk run on first failure |
| `--json` | status, gaps, map, list, triage | Machine-readable output |
| `--show-tools` | list | Add the TOOL column (command/script path); JSON always includes `tool` |
| `--dry-run` | delete, reset only | Preview without touching files. Other commands parse it but ignore it -- do not rely on it elsewhere |
| `--keep-spec` | delete | Delete pipeline artefacts, keep the test case specs |
| `--artefact` / `--check` / `--refresh` / `--strict` | link | Artefact path (write mode) / validate without writing / re-hash in place / reject TC IDs with no spec |
| `-v, --verbose` | global | More detail on any command |

---

## Workflow Sequences

### Mode 3: the inline agent spine

Mode 3 is the default operating mode for an AI agent already working in the
repo: the agent runs `gtms`, GTMS stamps files, the agent fills them, GTMS
reads them back. The filesystem is the handoff. Ground rules:

- GTMS owns every `tc-XXXXXXXX` identifier. Never generate, guess, or rename
  one. Capture the ID from the create output.
- Declare intent with `--adapter agent-*` on create, prime, and automate.
  One exception: on the automate path, run `gtms execute` **bare** -- the
  wiring record names the adapter; `--adapter agent-execute` would select the
  prime path and read a result template instead of running the script.
- Downstream commands re-validate the spec: frontmatter/filename ID mismatch,
  missing required frontmatter, and duplicate IDs in the same folder are
  rejected. Keep IDs unique project-wide (GTMS only enforces per-folder).

After `gtms create`, choose one of two sibling paths per test case:

**Prime path** -- record a one-time result. No script, no wiring record.
Use when you (or the user) exercise the feature directly and record the
outcome.

```sh
gtms create <folder> <name> --adapter agent-create
# fill the stamped TC spec body (title, steps, expected results)

gtms prime <tc-id> --adapter agent-prime
# exercise the feature with your own tools, then record the outcome in
# gtms/manual/records/<tc-id>--manual.result.yaml: result: pass | fail | skip

gtms execute <tc-id> --adapter agent-execute
# reads the filled template, validates it, records the result

gtms status <tc-id>
```

Re-priming needs `--force` (fresh form, discards content). If only the TC
spec changed and the recorded result is still valid, use
`gtms prime <tc-id> --update-hash` instead -- it refreshes the hash and
keeps the result; do not re-execute after it.

**Automate path** -- produce a repeatable script plus a wiring record.
Use when the value is re-runnability (CI, future agents, humans).

```sh
gtms create <folder> <name> --adapter agent-create
# fill the TC spec

gtms automate <tc-id> --framework bats --adapter agent-automate
# stamps a script skeleton (test/acceptance/<folder>/ for bats,
# gtms/scripts/playwright/<folder>/ for playwright) and a wiring record
# with artefact-hash: pending -- fill the script body

gtms execute <tc-id>
# bare on purpose: the wiring record names the adapter. The first run
# settles pending -> real hash (one-time bootstrap), then runs the script.

gtms status <tc-id>
```

Built-in `agent-automate` / `manual-automate` ship skeletons for BATS and
Playwright only; other frameworks need a configured adapter. Automate fails
*before writing anything* when no execute adapter is configured for the
framework -- no orphan skeleton is left on disk.

The two paths are siblings, not stages: prime a TC now, automate it later.
When both artefacts exist, the wired framework's result is the headline row
in `gtms status`; force the prime path past wiring with
`--adapter agent-execute`.

### Discovering adapters and frameworks

Ask the project what is wired up instead of guessing or parsing `gtms.config`
yourself:

```sh
gtms list adapters --json      # name, tier, framework, mode, default, tool
gtms list frameworks --json    # frameworks and the adapters targeting each
```

`list` is the authoritative inventory: built-in Tier 0 adapters do not appear
in the YAML. Eight action built-ins resolve with no config at all:
`agent-create`, `agent-prime`, `agent-automate`, `agent-execute` and their
`manual-*` twins (same implementation today; use `agent-*` when an agent is
driving, `manual-*` for a human). Resolver precedence: `--adapter` flag ->
`defaults.<command>` -> built-in fallback (`manual-prime` on prime only;
other commands error). On a wired execute, the wiring record -- not
`defaults.execute` -- names the adapter.

### Feature validation (configured adapters)

With AI create/automate adapters and a framework runner configured in
`gtms.config` (defaults set), the whole flow is hands-off:

```sh
gtms create <folder> --reference <req> --context-file <path>
gtms status <folder>                     # verify test cases appeared
gtms automate <folder> --framework <fw>  # generate scripts
gtms execute <folder>                    # run them (wiring routes the runner)
gtms status
```

### Review finding -> regression test

Convert a concrete behavioural gap from a review into a durable check: run
the Mode 3 automate path above with one focused test case
(`gtms create <folder> <finding-slug> --adapter agent-create`), then
automate, execute, and confirm on `gtms status <tc-id>`. The chain is:
finding -> TC spec -> script -> result -> dashboard proof.

### Failure investigation

```sh
gtms status <tc-id>        # detail card: which adapter ran, result, log
gtms triage <tc-id> --automation-wrong --summary "..."          # script broken -> queues re-automate
gtms triage <tc-id> --test-wrong --summary "..."                # spec wrong -> marked needs-review
gtms triage <tc-id> --app-wrong --defect <id> --summary "..."   # app bug -> triage history entry
```

`triage` needs an executed test case (wiring record + execution results) and
`--framework` when the TC is wired for more than one framework.

### Coverage gap filling

```sh
gtms gaps               # folder summary: CREATED / NOT AUTOMATED / FAILING
gtms gaps <folder>      # full category breakdown with affected TC IDs
```

Then fill per category: `gtms create` for missing specs, the automate path
for unautomated TCs, `gtms execute` for never-run wiring, `triage` for
failures.

### Multi-framework validation

One test case can carry wiring for several frameworks side by side
(`<tc-id>--<fw>.wiring.yaml` per framework):

```sh
gtms automate <tc-id> --framework bats
gtms automate <tc-id> --framework playwright
gtms execute <tc-id> --framework bats          # selector, required when multi-wired
gtms status <folder> --framework playwright    # strict filter: no fallback to other frameworks
```

### Clean slate re-execution

```sh
gtms reset <folder>     # clear result history (handoffs + finished execute tasks)
gtms execute <folder>   # re-run; wiring and specs were untouched
```

Bare `gtms reset` covers root-level test cases only; `-r` for everything.

---

## Result Interpretation

### Status Icons

| Icon | Meaning |
|------|---------|
| `✓` (green) | Complete / pass |
| `●` (yellow) | In progress |
| `○` (grey) | Pending / not started |
| `✗` (red) | Error / fail |
| `⚠` (yellow) | Stale (wiring drift) |
| `⊘` | Skipped -- the adapter ran but the test body opted to skip |
| `—` | No data / stage not attempted. The em-dash is load-bearing CLI output, not a rendering error |

### Execution Results

Two orthogonal fields describe every run; do not conflate them:

| Field | Carries | Values |
|-------|---------|--------|
| `status:` | Adapter execution state | `pending`, `in-progress`, `complete`, `error` |
| `result:` | Test outcome | `pass`, `fail`, `skip`, `error` |

`status: complete` + `result: fail` means the adapter ran cleanly and the
test failed. `error` is NOT `fail`: an error means the test could not run
(infrastructure, missing artefact) -- investigate the run, not the subject.
A `fail` means the assertion ran and the subject failed -- triage it.

In `gtms status --json`, each wired `frameworks[]` entry carries
`last_status_here` (execution state) and `last_result_here` (outcome), plus
`wiring_bootstrap: pending | ready` for the first-execute bootstrap state.
Read those fields; do not parse the icon column.

### Stale Detection

The wiring record stores two hashes; each drifts independently:

- **`artefact-hash` drift** -- the test script changed since the last
  settled hash. The next execute stops and asks you to acknowledge.
- **`testcase-hash` drift** -- the TC spec changed since wiring was stamped.
  Surfaces as stale wiring on `status` and `gaps`.

Acknowledge a deliberate edit with `gtms link --refresh <tc-id>` (re-hashes
both), or run once with `--allow-stale` if you know the change is safe.
Re-running `gtms execute` never repairs drift by itself.

`artefact-hash: pending` is not drift -- it is the normal state between
`gtms automate` and the first `gtms execute`, which settles it into a real
hash. `--allow-stale` does not bypass that bootstrap.

On the prime path, the result file carries `test_case_hash` from prime time;
a later spec edit flags drift on the manual record. Clear it with
`gtms prime <tc-id> --update-hash` (keeps the recorded result).

---

## Integration Principles

1. **Treat `gtms` as a standard CLI tool** -- invoke it the same way you invoke `git`
2. **Use `--json` for programmatic output** -- don't parse text tables
3. **Don't bypass the CLI** -- go through `gtms create` so the lifecycle is tracked, even if you could write files directly
4. **Read `gtms status` to understand state** -- single source of truth for pipeline coverage
5. **Read `gtms gaps` to find work** -- it tells you exactly what needs attention
6. **Read `gtms list` to discover adapters and frameworks** -- use `gtms list adapters --json` / `gtms list frameworks --json` / `gtms list all --json`; bare `gtms list` prints help, not data. Don't parse `gtms.config` YAML yourself. `list` is the authoritative inventory: it normalises deprecated fields, surfaces built-in Tier 0 adapters that don't appear in the YAML, and the JSON schema is stable across releases
7. **Don't reimplement GTMS commands as skills** -- GTMS commands are deterministic, not probabilistic. A thin CLI wrapper is correct; a reimplementation is not
8. **Don't edit GTMS-managed files directly** -- task files, result contracts, wiring records, and automation records have specific formats and lifecycle states. Writing directly bypasses validation

---

## AI-Specific Gotchas

| Gotcha | Detail | Workaround |
|--------|--------|------------|
| Post-fill validation gate | `automate`, `prime`, and `execute` entry points validate TC frontmatter before proceeding. If the TC's frontmatter `test_case_id` doesn't match the filename ID, or required fields are missing, or duplicate IDs exist in the same folder, the command exits non-zero with a validation error. Agents that fill TC content must ensure frontmatter `test_case_id:` matches the `tc-XXXXXXXX` in the filename | Consume the pre-generated IDs from `{tc_ids}` / `$GTMS_TC_IDS` when authoring specs. After filling TC content, verify `test_case_id:` matches before invoking the next pipeline stage. The validation error message names the exact file and invariant that broke |
| `CLAUDECODE` env var | Tier 1 adapters invoking `claude` inside Claude Code fail with "cannot be launched inside another session" | Use Tier 2 script adapters, or `unset CLAUDECODE` first |
| Windows binary name | Must use `gtms.exe` not `gtms` on Windows/MINGW | Always include `.exe` suffix in scripts |
| Wrong adapter on execute | `gtms execute` uses the config default. BATS tests fail if the default is a Playwright runner | Specify `--adapter bats-runner` for BATS, or set the default in `gtms.config`. Note: `gtms init --preset bats` ships `bats-runner` as the default execute adapter -- BATS-first projects work out-of-the-box |
| Result-contract orthogonal vocabulary | The handoff contract has two orthogonal axes: `status:` carries adapter-execution state (`pending \| in-progress \| complete \| error`); `result:` carries test outcome (`pass \| fail \| skip \| error`). Legacy `status: fail` and `status: skipped` are **retired and rejected** by validation -- writing them triggers a recovery overwrite to `status: error`. `status: complete` requires a non-empty `result:` field. AI-generated specs that assert `status: pass` or `status: fail` on the contract fail | When asserting on the **contract**: `status: complete` + `result: pass` for pass, `status: complete` + `result: fail` for fail, `status: complete` + `result: skip` for skip, `status: error` for adapter crash. When asserting on the **automation record**: `result: pass/fail/skipped/error`. The pipeline maps contract `result: skip` -> record `result: skipped` |
| Automate pass is not a test pass | Only an EXECUTE-command `result: pass` counts as a test pass. A successful `automate`/`prime`/`create` is pipeline progress, not a test outcome -- `gtms status`, `gtms gaps`, and bulk `gtms execute` all read an automated-but-never-executed TC as not executed | Never infer a pass from a green automate. Bulk `gtms execute <folder>` runs freshly automated TCs; it skips a TC only on a genuine prior execute pass against the current artefact. `--force` re-runs even those -- it is not needed to run never-executed tests |
| Multi-file automate output | AI adapter emits two or more `<gtms-file>` tags for a single `gtms automate` invocation. GTMS rejects the run at automate time: task moves to `gtms/tasks/error/`, result contract `status: error`, **no** wiring record is written. The streamed files remain on disk for inspection | Inspect the streamed files, fix the adapter's prompt or output so it emits exactly one `<gtms-file>` per test case, re-run `gtms automate`. `create` is unaffected -- many files per invocation is legitimate there |
| `gtms/tasks/` missing on a fresh worktree / checkout | `gtms execute` validates `gtms/tasks/` exists at startup. GTMS keeps the directory gitignored, so a fresh worktree or clone doesn't have it -- every first-time bulk execute fails with `Required directory 'gtms/tasks' not found` before any test runs. Hits sub-agents that run `/tests-execute` inside a freshly-created worktree | `mkdir -p gtms/tasks` before the first `gtms execute` call in any fresh checkout. No commit needed -- the directory is gitignored. The pre-flight should be in any agent prompt that drives bulk execute against an untouched worktree |
| `testcase-hash` is a write-side-only field | The `testcase-hash` field on the wiring record is written by `gtms automate` (create + `--force`) and `gtms link`. `gtms execute` never writes or refreshes it, and it is barred from auto-creating a wiring record -- so testcase-hash drift between the spec and the wiring record persists until an explicit re-wire. An agent that "fixes" a `Stale wiring (testcase)` signal by re-running execute will see the drift remain | To clear `testcase-hash` drift, run `gtms automate <tc> --framework <fw> --force` or `gtms link <tc> --framework <fw> --artefact <path>` -- not `gtms execute --force`. Same ownership rule as `artefact-hash`: the hashes are written by the wiring-producing commands, never by execute |
| TC ID `.md` suffix auto-stripped | All commands that accept a TC ID argument (`automate`, `execute`, `link`, `triage`, `delete`, `map`, `reset`, `status`) silently normalise `tc-xxx.md` -> `tc-xxx`. Only TC-ID-shaped arguments are affected (base starts with `tc-`); folder names like `release.md` are never modified. Tab-completed filenames "just work" | Pass either `tc-a1b2c3d4` or `tc-a1b2c3d4.md` -- both resolve identically. In BATS fixtures that programmatically extract TC IDs from directory listings, no need to strip `.md` before feeding to `gtms` commands |
| Folder arg is subfolder name (prefixes rejected) | Positional folder args must be the subfolder slug, not a prefixed path. Both `gtms/test/cases/my-feature` (long-form) and `test/cases/my-feature` (short-form) are rejected with "don't include the `gtms/test/cases/` prefix — GTMS adds it automatically" | Pass the subfolder slug only: `gtms automate my-feature`. Same rule for create/delete/execute/gaps/map/reset/status/triage |
| CLI stream routing | Task completion, Adapter, Branch, Target folder, Processing header, summary counts -> **stdout**. Spinner, guidance, warnings, errors -> **stderr**. Bulk per-TC progress lines -> stderr (uses `\r` overwrite) | Parse `stdout` for task IDs and results. Parse `stderr` for errors only |
| Read-command not-found errors -> stderr | Both `gtms map tc-X` (text) and `gtms map tc-X --json` write `✗ Test case <id> not found.` to **stderr**, same as every other CLI argument-validation error. Both not-found sites in `map.go` previously used `output.FprintError(w, ...)` where `w` was the runMap writer (= stdout) -- surfaced by BATS `tc-4ca9d531` and fixed by routing both through `output.Errorf`. Result: JSON consumers can `gtms map tc-X --json 2>/dev/null \| jq` cleanly without scraping error chrome | When adding a new "X not found"-style error to a read command, use `output.Errorf(msg, hint)` (the stderr router), not `output.FprintError(w, ...)` against a content writer. Reserve writer-based `FprintError` for errors that genuinely belong inline with the content stream (extremely rare in this codebase). BATS specs asserting on the message MUST use `run --separate-stderr` and assert against `$stderr` |
| `gtms gaps` positional arg rejects TC IDs (existence-first) | The positional arg to `gtms gaps` is a folder/requirement scope, not a TC ID. Passing a bare TC ID -- e.g. `gtms gaps tc-a1b2c3d4` -- and no folder of that name exists rejects with `✗ argument must be a folder, not a TC ID` to stderr, non-zero exit. This previously silently no-op'd: the arg was accepted as a folder name, `gtms/test/cases/tc-a1b2c3d4/` didn't exist, so the report ran against an empty TC set and returned `total_test_cases: 0`. The check is **existence-first**: `isTestCaseID` is a loose prefix detector (`tc-` + `len > 3`), so legitimate `tc-*` folder names (e.g. `gtms/test/cases/tc-regression/`) keep scoping normally | For per-TC views, use `gtms status tc-X` (and `gtms map tc-X` for traceability detail) -- both accept TC IDs natively. For folder scope, pass the folder slug. BATS specs that exercise the rejection must use `run --separate-stderr` and assert on `$stderr` for the rejection message |
| `gtms create` lists generated TC ids | After `gtms create`, the generated `tc-XXXXXXXX` ids + titles print on **stdout** directly under the "Task created" line (up to 10 inline; bulk over threshold gets a truncated list with `...and N more. Run gtms status <folder> to see all.`). Previously the caller had to `ls` the output folder to discover the ids. Malformed / missing frontmatter degrades to filename-only -- never blocks the happy path. Works with both sync create and async `gtms create status` (async lists TCs on completion). Stderr guidance block prints a count summary (`N test cases created in gtms/test/cases/<target>/`) instead of per-file paths -- no TC-id substrings leak to stderr | To feed the next command, grep stdout for lines matching `^  tc-[0-9a-f]{8}  `. In BATS tests that assert on stdout-only, use `run --separate-stderr` -- stderr now contains only the count summary, not per-file paths |
| Adapter warnings | Adapters can write `warnings: ["msg1", "msg2"]` to the result contract. GTMS merges these into the CLI output for `create`, `automate`, and `execute` commands, displayed with a `⚠` indicator. Adapters that don't populate this field see zero behaviour change | Surface adapter quality signals (e.g. "prompt template missing guides section") without failing the task. In BATS tests, assert warning text with `assert_output --partial "⚠"` or check for the specific warning string |
| Adapter stderr -> warnings | Anything an adapter writes to stderr on a **sync** invocation (Tier 1 or Tier 2) is line-split and surfaced as `⚠` warning lines on both success and failure paths. Each non-blank stderr line becomes a separate warning entry; blank lines are filtered, content is verbatim, no implicit cap. Async adapters are unaffected (no `runAdapterProcess` path). Coexists with contract `warnings:` -- contract entries render first, stderr entries second, no collision | Use stderr for progress messages, deprecation warnings, and "you should know about X" diagnostics -- they'll surface as warnings without polluting stdout / `<gtms-file>` parsing or the captured summary. Don't write narration to stdout. **For tests asserting on warning *count*: the mock MUST stream a valid `<gtms-file>` block so GTMS doesn't inject structural warnings ("Adapter ran successfully but produced 0 files." / "Adapter produced no output.") that inflate the count and mask the channel under test** |
| `log:` field YAML safety | GTMS sanitises `---` (YAML document separators) in the `log:` field, so adapters can safely write raw output there | The `log:` field is surfaced under `gtms status <tc-id>` for fail/error results. It lives on the terminal handoff / result contract (`.gtms/results/*.handoff.yaml`) -- truncated to 64 KB, with `notes-spill:` pointing at the full text under `.gtms/logs/{task-id}.log`. Execute writes the handoff plus a `gtms/execution/*.results.yaml` row, not a committed record |
| `--framework` filters TC list | `gtms execute -r --framework bats` skips TCs with no matching wiring. Bulk skip reasons come from `selectWiringForBulk`: `no <framework> wiring` (e.g. `no bats wiring`) when a framework is set, `not wired` when the TC has no wiring at all, and `multiple frameworks -- specify --framework` when a TC is multi-wired and none was given. **Text output for pipeline skips renders as em-dashes** -- there is no `⚠ skipped (...)` glyph in any dashboard row; the skip is a data-layer signal, not a rendered one | Match skip reasons in bulk execute logs with `no .* wiring\|not wired\|multiple frameworks`. In `gtms status` / `gaps` text output, pipeline skips are indistinguishable from "no wiring anywhere" -- em-dashes either way. In `--json`, the folder-level `framework_mismatch` count reports how many TCs an explicit `--framework` filtered out. Per-TC detail view applies strict framework filtering when `--framework` is explicit |
| `--framework` strict per-TC | When `--framework X` is passed **explicitly** to `gtms status`/`gaps`/`map`, per-TC views (including folder detail, TC detail, JSON, and categorisation) filter strictly -- TCs without an X record show `—` in AUTOMATE / EXECUTE / LAST RESULT instead of falling back to another framework's record. When `--framework` is **omitted**, adapter config default is used with graceful fallback preserved | Assertions for dual-framework TCs: when the test passes `--framework pester` and the TC has only a bats record, assert em-dashes in all three columns (not a bats-result row). When no flag is passed, assertions about which framework's record appears must tolerate fallback. `selectAutomationRecord(records, framework, strictFramework bool)` is the internal signature |
| Create adapter ID-mismatch | ~~`gtms create` does not verify that the adapter obeyed the ID contract~~ *(Fixed)* | GTMS now inspects every emitted `.md` file after a `create` adapter returns. If any spec violates the contract -- filename ID != frontmatter `test_case_id`, malformed ID, missing field, ID not in the pre-generated batch, or two specs sharing an ID -- the task exits non-zero, stderr shows `✗ Task failed: ...` with `    {filename}: {reason}` underneath, the result contract gets `status: error` + `validation-error: ...`, the task moves to `gtms/tasks/error/`, and the offending files stay on disk. **For AI agents authoring adapters:** consume the pre-generated IDs via `{tc_ids}` / `$GTMS_TC_IDS` in order, one per spec, and make sure each spec's filename ID matches its frontmatter `test_case_id`. **For AI agents reacting to a failure:** read the `    {filename}: {reason}` line -- it names the exact spec and the exact invariant that broke. Don't rename the file (that was the old workaround); regenerate the spec with the correct ID. See `reference/adapter-guide.md` -> **Create Validation Contract** for the full contract |
| Adapter name used as framework fallback | When an execute adapter has no `framework:` field, `adapter.ResolveFramework` falls back to the adapter's **name**. An adapter named `bats-runner` with no `framework:` set therefore resolves framework `bats-runner` and drives the wiring lookup toward `tc-XXX--bats-runner.wiring.yaml`, which won't exist if the wiring was written for framework `bats` -- so the run aborts before the resolver runs | Under any adapter whose name doesn't equal the intended framework, set `framework: <fw>` in the adapter config: `adapters: { execute: { bats-runner: { framework: bats, mode: sync, command: '...' } } }`. Explicit `framework:` beats the adapter-name fallback |
| `gtms/test/cases/` prefix in folder args | `gtms status gtms/test/cases/my-folder/` is rejected -- GTMS adds the prefix itself and trailing slashes are also rejected | Pass just the folder slug: `gtms status my-folder`. Same rule for create/automate/execute/gaps |
| Detail view vs folder summary | `gtms status` (no arg) -> folder summary table with `TC / CREATE / AUTOMATE / EXECUTE` columns and icons (`✓ ✗ ● ○`), fraction suffix only on non-`✓` cells (e.g. `✗ 2/6`). Priority: `✗ > ● > ○ > ✓` (worst-news-wins). CREATE always `✓`. `gtms status <folder>` -> per-TC detail table with `CREATE / AUTOMATE / EXECUTE` columns and ✓/✗/— icons. `gtms status <tc-id>` -> per-stage detail view with each stage label followed by `, <timestamp>` inside its parens (CREATE/AUTOMATE show date `YYYY-MM-DD`, EXECUTE shows `YYYY-MM-DD HH:MM UTC`; never-run stages show `—` with no timestamp). For fail/error results a `Log:` block follows with the adapter's raw output (indented two spaces; header cites spill file when truncated to 64 KB). Assertions written for one format won't match the other | Match the assertion to the invocation: summary (no folder arg) asserts icons, optional fractions, and a key-footer line (`Key: ✓ = all pass ...`); folder detail asserts TC IDs, icons, `[framework]` tags; TC detail can additionally assert on the timestamp suffixes (use partial match -- existing substrings like `[bats]` and `Pass` still hit because the timestamp is inserted before them) or on the `Log:` block for fail/error cases. JSON summary adds `passing`, `failing`, `errored`, `in_flight` fields alongside the existing counts |
| Single-TC `--json` is flat; bulk status `--json` is a bare array, map is a tree | `gtms status <tc-id> --json` and `gtms map <tc-id> --json` emit a **flat object** (a `PipelineDetailEntry`) -- fields like `test_case_id`, `execute_status`, `last_result` at the top level, no wrapper. By contrast `gtms status <folder> --json` and `gtms status -r --json` emit a **bare JSON array** of entries (marshalled directly from `[]PipelineEntry`), while `gtms map <folder> --json` / `gtms map -r --json` emit a **tree of requirement groups**. Neither bulk form uses an `{entries: [...]}` wrapper. AI-authored specs that copy a bulk-shape jq filter into a single-TC step silently extract null, and the mismatch only surfaces at execute time | Single-TC: `jq -r '.execute_status'` (direct field access). Bulk/folder/recursive status: `jq '.[] \| select(.test_case_id == "tc-XXX") \| .execute_status'` (bare array). Bulk/folder map (tree): `jq '.. \| objects \| select(.test_case_id? == "tc-XXX") \| .execute_status'`. When authoring **specs**, prefer intent-only language ("capture the `execute_status` field from the single-TC detail JSON object") so regenerated scripts don't copy a stale shape |
| Dual-framework TCs | One TC can carry wiring for several frameworks side by side: `gtms/automation/wiring/<tc-id>--bats.wiring.yaml` AND `<tc-id>--pester.wiring.yaml`. Each framework's execute runs independently; `gtms status --framework X` selects the X wiring | Automate (or `gtms link`) once per framework to produce both wiring records. Execute each framework separately -- single-command multi-framework execute is deferred |
| Mixed pass+skip BATS files now demote to ⊘ | Previously, a `.bats` file with one passing `@test` and one skipping `@test` rolled up to ✓ pass on the dashboard. This was flipped: any `# skip` directive without a `not ok` line now classifies the file as a skip. All five BATS execute wrappers (`bats-runner` + four `remote-bats-*` variants) surface both counts in the result-contract `summary:` field (e.g. `"2 passed, 1 skipped"`). Fail still wins -- any `not ok` line classifies as fail regardless of skip count. (The earlier Tier 1 vs Tier 2 local-vs-remote asymmetry has been retired -- the local `bats-runner` is now a Tier 2 wrapper using the same shared `classify_bats_status` helper as the remote variants) | When asserting on a mixed-skip TC's outcome: the result contract is `status: complete` + `result: skip` (never `status: skipped` -- that vocabulary is retired), and the reader exposes it as ⊘ in `gtms status` / `gtms map` (text + `--json execute_status: "skipped"`; the reader renames `skip` -> `skipped`). When writing a mixed-skip BATS fixture for a test that exercises the rule, include at least one `@test` whose body is `skip "..."` and at least one that asserts something -- empty placeholder files produce `status: error` (no TAP plan), not a skip |
| Adapter-first dispatch | `gtms execute tc-X --framework manual` does NOT select the `manual-execute` adapter on **any** preset. The framework flag is independent of adapter resolution -- adapter dispatch keys on `resolved.Name == "manual-execute"` OR `resolved.Config.Framework == "manual"`, never on the CLI `--framework` flag as an adapter selector. A wired execute is routed by the wiring record's adapter; manual execute requires `--adapter manual-execute` or a manual execute default. To opt into manual-execute on any preset (`manual`, `bats`, `playwright`), pass `--adapter manual-execute` -- the `manual` preset already wires it as the execute default, `bats` and `playwright` do not. The internal helper `adapter.IsManualFramework(resolved)` is the single source of truth and is shared between `cli/execute.go` (artefact pre-check deferral) and `adapter/invoker.go` `buildAdapterContext` (manual context population). | When asserting on adapter dispatch in BATS, exercise the resolved adapter shape, not the framework flag. Specs that say "`--framework manual` should not engage manual-execute" must verify via the handoff `adapter:` field, not by trusting the flag. When building a new adapter that needs CLI-side branching, follow the same pattern: one predicate, both call sites share it |
| Manual `gtms execute` missing-result-file directs to `gtms prime` | When `gtms execute` runs against a manual-execute adapter and the result file at `gtms/manual/records/{tc-id}--manual.result.yaml` is missing, the error message names `gtms prime tc-X --framework manual` -- not the generic `gtms automate` hint. The error flows through the standard `status: error` handoff path: task lands in `gtms/tasks/error/`, pipeline records are built, no silent short-circuit | When testing the manual missing-file path, expect to see the prime hint in stderr AND the error-task placement on disk. If you see the generic "Re-run gtms automate" hint instead, the dispatch helper isn't resolving manual -- check whether your fixture's `gtms.config` actually wires manual-execute as the resolved adapter for the TC under test |
| `gtms execute` bulk exits non-zero on any failure | `runBulkExecute` (`internal/cli/execute.go:439-441`) returns a non-zero exit code if any TC fails, errors, or skipped-but-not-clean -- even though the bulk command "completed" in the sense that it processed every TC. Spec wording like "the command completes" or "execute runs to completion" reads to a BATS author as `assert_success` (exit 0), but the product is correctly using a UNIX-style aggregate exit code. Surfaced when `tc-663f0e5e` (adapter-driven failed execute) was authored with `assert_success` and had to be flipped to `assert_failure` | When asserting on bulk-execute exit codes: `assert_success` only when **every** TC in the batch passed (or was a clean tooling skip); `assert_failure` whenever any TC's outcome maps to fail/error. For single-TC executes: `assert_success` for pass, `assert_failure` for fail/error. The spec author should write "exits 0 when every TC passes; exits non-zero if any TC fails or errors" rather than ambiguous "completes" / "runs to completion" -- both interpretations of the latter are linguistically valid and BATS authors will pick the wrong one |
| `gtms execute --result` / `--notes` are gone | The legacy `--result pass\|fail\|skip` and `--notes` flags on `gtms execute` were removed. The `runManualResult`, `pipeline.WriteManualResult`, and `pipeline.RecordManualResult` symbols are deleted. A Go source-shape guard (`TestSourceShape_NoLegacyManualBypass` in `internal/cli/source_shape_test.go`) locks the deletions in -- re-introducing any of those names fails the build. The old framework-routing logic and the bypass-only path-safety branch retired alongside, and the manual `--result` folder-target path was retired as superseded. Future quick-record UX will sit on top of the prime pipeline, not a resurrected direct writer | Manual outcome recording flows exclusively through `gtms prime --framework manual` -> edit `gtms/manual/records/<tc-id>--manual.result.yaml` -> `gtms execute <tc-id> --adapter manual-execute`. Never propose `--result` or `--notes` -- Cobra will reject them with "unknown flag" and the source-shape guard makes re-introduction a build failure. When migrating a fixture that used `--result` as a setup shortcut, see the manual-bypass fixture-authoring lessons in `reference/adapter-guide.md` |

---

## Debugging Failures

When a test fails, always go back to the spec:

1. Read the test case spec (`gtms/test/cases/tc-XXXXXXXX-*.md`) -- what should the test verify?
2. Read the automation artefact -- does the script correctly implement the spec?
3. Identify the gap:
   - Script doesn't match spec -> fix the script
   - Spec is wrong -> update the spec
   - Script matches spec, subject fails -> triage it

**Don't re-execute blindly.** If the root cause is a wrong adapter or broken script, re-executing produces the same failure. Investigate first.

---

## Further Reading

- USER-GUIDE.md -- the full narrative: modes, walkthroughs, file formats, configuration, command reference, troubleshooting (run gtms --help for the version-pinned URL)
- Adapter Guide -- adapter tiers, configuration, env-var and result contracts, framework notes (run gtms --help for the version-pinned URL)
- `gtms skills` -- starter agent skills for the pipeline (create, automate, execute, prime, verify-intent); install by copying from gtms/skills/ into your skills directory
- `gtms getting-started` -- linear quick start for a new project or a new agent: the shortest path from `gtms init` to a recorded result
- `gtms <command> --help` -- flag-level detail from the binary you are running
