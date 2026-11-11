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
| `create` | `gtms create <folder> [name]` | Stamp or generate test case specs in `<folder>/` under the test cases directory (`gtms/test/cases/` by default), or in the create adapter's `output-dir`. A target that resolves outside that boundary is refused before anything is written. No built-in default: needs `--adapter` or `defaults.create` (every scaffolded preset sets one) |
| `prime` | `gtms prime <tc-id>` | Stamp a manual result template at `gtms/manual/records/<tc-id>--manual.result.yaml` with an empty `result:` field to fill |
| `automate` | `gtms automate [tc-id \| folder]` | Stamp a test script skeleton plus a wiring record linking it to the test case. Bare form runs every test case (bulk). On the `manual` preset it stops and points you at the prime path |
| `execute` | `gtms execute [tc-id \| folder]` | Run the wired script, or record a primed result -- the effective adapter decides which path (see Workflow Sequences) |
| `triage` | `gtms triage <tc-id>` | Classify the latest result with `--value <key>`, `--resolve` an existing classification, or read records back with `--show` / `--list` |

### Visibility Commands

| Command | Syntax | Purpose |
|---------|--------|---------|
| `status` | `gtms status [tc-id \| folder]` | Pipeline dashboard. Bare = folder summary; folder = per-TC table; `tc-id` = detail card. `--show-triage` / `--triage <value>` add the triage lens |
| `gaps` | `gtms gaps [folder]` | Coverage gaps by category. Folder scope only -- a TC ID errors and points you at `gtms status <tc-id>`. TRIAGED / UNTRIAGED columns are always on |
| `map` | `gtms map [tc-id \| folder]` | Test cases grouped by requirement (traceability); `--detail` for full titles |
| `list` | `gtms list <adapters\|frameworks\|all>` | Adapter and framework inventory. Bare `gtms list` prints help, not data |

Over a folder, `-r`, or the whole project, `status`, `gaps`, `map`, and
`triage --list` leave out a duplicated test case ID or an unreadable test case
spec, name each on stderr, and exit 1 while still printing the rest. A refused
handoff under `.gtms/results/` does the same: it is left out and named on
stderr, and the command exits 1. Check the exit code, not just the output (see
AI-Specific Gotchas).

### Lifecycle Commands

| Command | Syntax | Purpose |
|---------|--------|---------|
| `init` | `gtms init [--preset manual\|bats\|playwright]` | Scaffold a project. Plain `gtms init` in a repo with no GTMS project = `manual` preset; in an initialised project it lists presets and exits. `--presets` lists without scaffolding |
| `link` | `gtms link <tc-id> --framework <fw> --artefact <path>` | Write or repair a wiring record by hand; `--refresh` re-hashes to confirm a deliberate spec edit (or a script edit under `execute.on-script-change: refuse`); `--adapter <name>` repoints the stored execute adapter (see Key Flags) |
| `delete` | `gtms delete <tc-id \| folder>` | Remove GTMS-owned state: test case specs, wiring records, manual records, result files, task files, result contracts. Referenced test artefacts are PRESERVED by default (`--force-artefact-deletion` to remove them; `--keep-spec` to keep specs; `--dry-run` to preview). A refused handoff is left untouched: delete finishes the safe cleanup, names it with the manual fix, and exits non-zero (`--dry-run` too) |
| `reset` | `gtms reset [tc-id \| folder]` | Clear recorded results: every handoff under `.gtms/results/` (all stages), their exact `<handoff>.tmp-<16 hex>` staging leftovers, and finished execute task files. Wiring records, test case specs and committed `gtms/execution/` records untouched (`--dry-run` to preview). Any refused handoff in `.gtms/results/`, even another TC's, is left in place: reset finishes the safe cleanup, names it with the manual fix, and exits non-zero (`--dry-run` too) |
| `version` | `gtms version` | Print the version (canonical form; `--version` also works) |

Task-level run listing: `gtms create status`, `gtms automate status`,
`gtms execute status` -- one row per invocation that reached its adapter (a
preflight refusal or a bulk skip writes no task). Reach for these when a run
has not shown up on `gtms status` and you want to know whether the adapter is
still running or already failed. `-v` on the automate/execute detail views
adds a `Handoff:` line naming the result contract under `.gtms/results/`.

### Key Flags

| Flag | Commands | Purpose |
|------|----------|---------|
| `--adapter` | create, prime, automate, execute, link | Select the adapter (overrides `defaults.<command>`). On a wired execute, selects a same-framework adapter as the runner for this invocation only; cross-framework is rejected. All four Mode 3 execute names (manual-execute, agent-execute, and their -script variants) take the prime path instead and are never wiring runners or repoint targets. They are manual-only and sync-only: a non-manual `--framework`, a non-manual configured `framework:` or a configured `mode: async` is refused when one is selected (see Gotchas). On link, repoints the stored execute adapter permanently: same framework only, over a single TC (bare `tc-XXXXXXXX` or folder-qualified `folder/tc-XXXXXXXX`) or a folder/`-r`/`--all` scope |
| `--framework` | prime, automate, execute, link, triage, status, gaps, map | Framework label on write commands; selector/filter on read commands. Required on execute when a TC is wired for more than one framework; with a Mode 3 execute adapter only `manual` (or no flag) is accepted; required on triage when more than one candidate exists (several wired frameworks, or a wired framework plus a recorded manual execution -- `manual` is a candidate, `manual-execute` is an adapter name and does not select it) |
| `--reference` | create | Requirement identifier recorded in the test case (e.g. `REQ-001`) |
| `--focus` | create | Focus area within the source document (prompt-driven adapters) |
| `--context-file` | create, prime, automate | Supplementary context file for the adapter |
| `--env` | automate, execute | Environment label recorded and passed to the adapter; GTMS does not route on it |
| `--executed-by` | prime, automate, execute | Operator identity recorded on the run (default: `GTMS_EXECUTED_BY`, then git `user.name`) |
| `--repo` | init | Repository identity stored as `project.repo`. Inferred from the remote named `origin`; when no identity resolves there, GTMS stores the reserved value `no-origin-remote`. That sentinel reaches adapters unchanged as `{repo}` / `GTMS_REPO` -- treat it as "identity not configured", never build a URL from it. `--repo org/name` sets it explicitly |
| `-r, --recursive` | automate, execute, status, gaps, map, delete, reset, link (refresh, repoint), triage (`--list` only) | Include subdirectories |
| `--force` | init, prime, automate, execute, link | Meaning varies: overwrite config, first saving it as `gtms.config.bak` (then `.bak.2`, ...; never overwritten, named in the output), or allow a nested project (init; with `--preset <name>` or `--demo` it also refreshes scaffolded files: a pristine recognised older shipped copy is upgraded in place, a current copy is skipped, and an edited or unknown copy is left untouched while the shipped version is written beside it as a `.gtms-new` companion (or the next `.gtms-new.N` name when needed); CRLF copies count as unedited; every route covers the test case and result templates, create/prime scripts, authoring guide, result schema, `gtms-tests-create` skill and skills `README.md`; `--preset bats` adds `bats-runner.sh` and the BATS template, `--preset playwright` adds `playwright-runner.sh`, and `--demo` adds `_demo/adapters/create-demo.sh` with no BATS or Playwright files; the v0.2.0 bats runner is not recognised and gets the companion; bare `gtms init --force` in an existing project changes nothing), overwrite result template (prime), reprocess and replace only output files the test case provably owns -- it never substitutes for ownership (automate), overwrite an existing wiring record (link), re-run already-passing TCs in a bulk run (execute -- change checks still apply, and an automated TC with an active execute task is still skipped; manual and agent execution unchanged) |
| `--update-hash` | prime | Refresh `test_case_hash` only; preserves the filled `result:` and execute history |
| `--allow-stale` | execute | Bypass the spec and script change checks for this run only; accepts nothing for later runs. Does NOT update wiring, and does NOT bypass a missing or unreadable script or the pending bootstrap |
| `--guidance-off` / `--guidance-on` | init | Turn the post-command guidance block off or on and record the choice in `gtms.config`. Guidance is on by default and prints after most commands, so turning it off narrows what you have to parse |
| `--fail-fast` | automate, execute | Stop a bulk run on first failure |
| `--json` | status, gaps, map, list, triage | Machine-readable output. On triage: single object for `--value` and `--show`, bare array for `--list` (`[]` when empty) |
| `--show`, `--list` | triage | Read records back. Exactly one PRIMARY operation per invocation (`--value`, `--resolve`, `--show`, `--list`); under `--list`, `--value` is a filter, not a second operation, so `--list --value <key>` is one operation. `--show` returns one record active OR resolved and prints its `summary`/`defect` when present; `--list [folder|test-case-id]` returns many, active-only until `--include-resolved`. The `--list` positional is a folder OR a test-case ID (existence-first; a trailing `/` forces folder); a test-case ID lists every framework record for that test case. Root and test-case-target `--list` include orphans, marked `[spec missing]` (no spec declares that id); a folder list does not. `--list <tc-id>` REFUSES a duplicated or unreadable target (nothing on stdout, not even `[]`; exit 1). A stored defect renders as a trailing `[<defect>]` marker (before `[spec missing]`). Under `--list`, `--value` filters instead of classifying, `--framework` is rejected, and `-r` is rejected with a test-case target |
| `--summary` / `--defect` | triage | Optional rationale text / defect ID stored with a `--value` classification |
| `--include-resolved` | triage | Widen a `--list` to resolved records (no resolved-only view exists) |
| `--show-triage` | status | Turn on triage presentation: folder-summary `TRIAGED` count, per-TC `TRIAGE` column, detail-card `TRIAGE:` block. WITHOUT it human status output is byte-identical to what it was before triage existed |
| `--triage <value>` | status | Filter to test cases carrying that stored value; implies `--show-triage`. REFUSED at bare root (needs a folder, TC ID, or `-r`). An empty match is exit 0 with a diagnostic, not an error -- unless a duplicated or unreadable test case in scope makes the command exit 1 |
| `--show-tools` | list | Add the TOOL column (command/script path); JSON always includes `tool` |
| `--dry-run` | delete, reset, link (refresh, repoint) | Preview without touching files, listing exactly which records would change. On link it works in either flag position, but the two modes do not share a scope set: an `--adapter` repoint takes a single TC, a folder, `-r`, or `--all`; a `--refresh` takes a single TC, a folder, `-r`, or `--framework`. `--all` is repoint-only -- `link --all --refresh` is rejected before any preview runs. `link` write mode and `--check` still REJECT it; every other command parses it but ignores it -- do not rely on it elsewhere |
| `--keep-spec` | delete | Delete pipeline artefacts, keep the test case specs |
| `--force-artefact-deletion` | delete | Also delete the test artefacts wiring and manual records point at. Without it they are preserved and named in the summary. With it, an artefact is removed only when every reference to it belongs to the deletion set. That check reads each record's declared `testcase:` identity, so a same-ID test case in another folder and a longer `tc-xxxxxxxx--variant` sibling both count as outside references and their artefacts are preserved. A record that cannot be parsed, or whose declared identity disagrees with its filename, makes the forced delete refuse rather than risk a shared file. A bare TC ID that exists in more than one folder is refused before anything is removed -- qualify it as `folder/tc-xxxxxxxx` |
| `--artefact` / `--check` / `--refresh` / `--strict` | link | Artefact path (write mode) / validate without writing / re-hash in place / reject TC IDs with no test case spec |
| `--from-adapter` / `--all` | link | Repoint only: exact-match filter on the stored adapter (MANDATORY for folder, recursive, and `--all` repoints) / project-wide repoint scope (takes no positional, no `-r`) |
| `-v, --verbose` | global | More detail on any command. On an execute change refusal it adds the recorded and current hashes, which are hidden otherwise |

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
- Downstream commands resolve the id through the test case spec's
  `test_case_id` frontmatter, project-wide. An id declared by two test case
  specs, or a candidate test case spec that cannot be read or carries no usable
  `test_case_id`, refuses the command. A filename disagreeing with the body id
  does NOT: that is a renamed owner and it works. A test case spec without the
  `test-case/v2` header (`gtms_schema`, `type: Test Case Spec`, `test_type`)
  cannot be read, so it refuses too. Keep ids unique project-wide --
  `gtms create` validates only the files one invocation emitted, so nothing
  stops you WRITING a duplicate; you find out at the next command that has to
  act on it.

**Creating a test case is a finished job. Stop there.** Stamp the spec, fill it
in, report the allocated TC ID and file path, stop. `gtms create` does not imply
`gtms prime`, `gtms automate`, or `gtms execute` -- run those only when the
request asked for them. Running them unasked creates records the user did not
ask for, and on the prime path can mean exercising the application itself.

```sh
gtms create <folder> <name> --adapter agent-create
# fill the stamped TC spec, report the TC ID and file path, then stop
```

A request that already names a broader outcome -- "create and automate a test
case", "create a test case, run it manually and record the result" -- authorises
that path. Carry it out; do not ask again.

When the user wants to continue but has not named a path, explain both in a line
each and ask which. Do not choose for them. Neither is the default: not prime
because it is listed first, not automate because a framework is configured.

```text
Created and populated test case tc-XXXXXXXX.

Would you like to continue with:

1. Prime -- exercise it now and record a result, without creating a reusable
   automation script.
2. Automate -- create a reusable test script that can be rerun later or in CI.
```

Both paths below are user-chosen continuations, not stages of create.

**Prime path** -- record a one-time result. No script, no wiring record.
Use when you (or the user) exercise the feature directly and record the
outcome.

```sh
gtms create <folder> <name> --adapter agent-create
# fill the stamped TC spec body (title, steps, expected results); leave test_case_id alone

gtms prime <tc-id> --adapter agent-prime
# exercise the feature with your own tools, then record the outcome in
# gtms/manual/records/<tc-id>--manual.result.yaml: result: pass | fail | skip

gtms execute <tc-id> --adapter agent-execute
# reads the filled template, validates it, records the result

gtms status <tc-id>
```

`agent-execute`, `manual-execute` and their `-script` variants record a manual
result only, synchronously: omit `--framework` or pass `--framework manual`;
any other framework is refused before a task, handoff or execution record
exists. GTMS records `framework: manual` and `mode: sync` whatever a script
writes, provided it can save the corrected handoff.

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
# gtms/scripts/playwright/<folder>/ for playwright, or the adapter's
# output-dir) and a wiring record with artefact-hash: pending -- fill the
# script body. An unfilled skeleton executes as skip, not pass.
# A fresh project has no bats-assert: fill BATS bodies with core checks
# ([ "$status" -eq 0 ], [[ "$output" == *text* ]]); load any shared
# helper inside setup(), not setup_file().

gtms execute <tc-id>
# bare on purpose: the wiring record names the adapter. The first run
# settles pending -> real hash (one-time bootstrap), then runs the script.

gtms status <tc-id>
```

Built-in `agent-automate` / `manual-automate` ship skeletons for BATS and
Playwright only; other frameworks need a configured adapter. Automate fails
*before writing anything* when no execute adapter is configured for the
framework -- no orphan skeleton is left on disk. The manual preset only
configures the manual execution path; before using the automate path for BATS
or Playwright, configure an execute adapter for that framework, or use the
prime path.

The two paths are siblings, not stages: prime a TC now, automate it later.
When both artefacts exist, the wired framework's result is the headline row
in `gtms status`; force the prime path past wiring with
`--adapter agent-execute`. Bare `gtms execute <tc-id>` follows the wiring
unless the adapter in effect (`--adapter`, else `defaults.execute`) is a
prime-path one (`manual-execute`, `agent-execute`, or their `-script`
variants); on the manual preset, where the default is `manual-execute`, the
bare form takes the prime path and ignores wiring. The primed result stays visible as the `manual`
entry in `gtms status --json` and under `gtms status <tc-id> --framework manual`.

### Discovering adapters and frameworks

Ask the project what is wired up instead of guessing or parsing `gtms.config`
yourself:

```sh
gtms list adapters --json      # command, name, tier, tool, framework, mode, default, canonical_for_wiring
gtms list frameworks --json    # each framework with its command:name adapters (e.g. execute:bats-runner)
```

`list` is the authoritative inventory: built-in Tier 0 adapters do not appear
in the YAML. Eight action built-ins resolve with no config at all:
`agent-create`, `agent-prime`, `agent-automate`, `agent-execute` and their
`manual-*` twins (same implementation today; use `agent-*` when an agent is
driving, `manual-*` for a human). Resolver precedence: `--adapter` flag ->
`defaults.<command>` -> built-in fallback (`manual-prime` on prime only;
other commands error). A `gtms.config` adapter named like a built-in
replaces that built-in. On a wired execute, the wiring record -- not
`defaults.execute` -- names the adapter; `gtms execute --adapter <name>`
overrides it for one run (same framework only), `gtms link --adapter <name>`
repoints it permanently. In `gtms list adapters --json`,
`canonical_for_wiring: true` marks the adapter automate stamps into new
wiring for that framework.

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

Execute stays bare because each wiring record names its runner -- unless
`defaults.execute` or `--adapter` names a prime-path execute adapter, which
bypasses wiring. Omit `[name]` on create to let a configured AI adapter emit
one test case per behaviour; pass it to force exactly one.

### Review finding -> regression test

Convert a concrete behavioural gap from a review into a durable check: run
the Mode 3 automate path above with one focused test case
(`gtms create <folder> <finding-slug> --adapter agent-create`), then
automate, execute, and confirm on `gtms status <tc-id>`. The chain is:
finding -> TC spec -> script -> result -> dashboard proof.

### Failure investigation

```sh
gtms status <tc-id>        # detail card: which adapter ran, result, log
gtms triage <tc-id> --value environment --summary "..."         # classify the latest result (opaque, project-configured value)
gtms triage <tc-id> --value app-wrong --defect <id>             # --summary and --defect are optional with any value
gtms triage <tc-id> --resolve                                   # mark the existing classification resolved (kept, not deleted)
gtms triage <tc-id> --show --json                               # read ONE record back (active or resolved)
gtms triage --list                                              # read MANY back (every record, orphans marked [spec missing])
gtms triage --list <tc-id>                                      # every framework record for ONE test case (orphans included; refuses a duplicated/unreadable id; no -r)
gtms triage --list <folder> -r --value environment              # scoped + filtered; add --include-resolved for resolved ones
gtms status <folder> --show-triage                              # the same classifications on the dashboard
```

`--value` keys come from `triage.values` in `gtms.config` (built-in defaults
apply when no triage block is configured); a value carries no behaviour -- it is
just a label. Classification writes one record at
`gtms/triage/<tc-id>--<framework>.triage.yaml` and touches nothing else.
`--value` needs a readable test case spec and an execution result for the
chosen framework: a wired framework proves it through its wiring record, the
`manual` framework through an execution recorded by
`gtms execute --adapter manual-execute` and needs no wiring. A primed result
file that was never executed is not enough, and neither is an execution whose
local evidence is gone (a fresh clone): execute again first. Any terminal
result can be classified, pass included. Editing the manual result file after
executing does not change what triage classifies. `--framework` is required when
more than one candidate exists -- the candidates are every wired framework
plus `manual` when a manual execution has been recorded. `--resolve` needs
only an existing record.

Reads: `--show` is never lifecycle-filtered; `--list` is active-only until
`--include-resolved`. `--list --json` is a bare array ordered by test case ID
then framework, `[]` when empty; `--show --json` is a single object. Both carry
`result_changed_since_triage`: `true` / `false` / `null`, where `null` means NO
latest result to compare against -- not "unchanged". A hand-edited record that
fails validation is refused BY PATH, and a list containing one in scope fails
whole rather than returning partial output. Read records through these flags,
never by opening `gtms/triage/*.yaml`.

A record must also BE a regular file. A symlink, directory, FIFO, socket or
device with a record-shaped name is refused by name before it is read -- a
symlink even when it points at a valid record inside the project, because the
check is on the entry, not its target. `gtms/triage/` itself may be a symlink,
but only to a target inside the project; one pointing outside fails every triage
read and write.

Two shapes to never create: a record entry that is not a regular file, and a
`gtms/triage/` link whose target leaves the project. An in-project store link is
fine. Writes refuse to replace a non-regular entry and leave it, and anything it
points at, untouched.

### Coverage gap filling

```sh
gtms gaps               # folder summary: CREATED / NOT AUTOMATED / FAILING / TRIAGED / UNTRIAGED
gtms gaps <folder>      # full category breakdown with affected TC IDs
gtms gaps -r            # full report across every folder
```

Then fill per category: `gtms create` for missing specs, the automate path
for unautomated TCs, `gtms execute` for never-run wiring, `triage` for
failures.

TRIAGED / UNTRIAGED are always shown and count CLASSIFICATIONS, not failures:
`triaged + untriaged == created` for every folder row, whatever each TC's
result, so a passing TC can be triaged and a TC with several active
classifications still counts once. `skipped` is a separate RESULT count and
overlaps both columns on purpose. Detailed gaps splits its currently-failing
heading the same way: `Currently failing: N (X triaged, Y untriaged)`, with
`currently_failing_triaged` / `currently_failing_untriaged` in `--json`.

### Multi-framework validation

One test case can carry wiring for several frameworks side by side
(`<tc-id>--<fw>.wiring.yaml` per framework):

```sh
gtms automate <tc-id> --framework bats
gtms automate <tc-id> --framework playwright
gtms execute <tc-id> --framework bats          # selector, required when multi-wired
gtms status <folder> --framework playwright    # strict filter: no fallback to other frameworks
gtms status <tc-id> --framework manual         # a primed result alongside the wiring
```

`--framework` never crosses paths: it picks among wiring records. To record
a prime-path result on a wired test case, pass `--adapter agent-execute`.

### Clean slate re-execution

```sh
gtms reset <folder> --dry-run   # preview
gtms reset <folder>             # clear result history (all handoffs + finished execute tasks)
gtms execute <folder>           # re-run; wiring and test case specs were untouched
```

Bare `gtms reset` covers root-level test cases only; `-r` for everything.
Reset (and its `--dry-run`) exits non-zero while any handoff under
`.gtms/results/` is refused, even another TC's; it leaves that file in place
and clears the rest. Fix the named path, then reset again. Reset does not
remove committed `gtms/execution/` records.
Wrong wiring (artefact or adapter) is not a reset problem: overwrite it with
`gtms link <tc-id> --framework <fw> --artefact <path> --force` or
`gtms automate <tc-id> --force`. Use `gtms delete <tc-id>` only to remove the
whole test case.

---

## Result Interpretation

### Status Icons

| Icon | Meaning |
|------|---------|
| `✓` (green) | Complete / pass |
| `●` (yellow) | In progress |
| `○` (grey) | Pending / not started |
| `✗` (red) | Error / fail |
| `⚠` (yellow) | Spec or script changed since the wiring was written (status key `⚠ = spec or script changed`), appended after the stage icon. Under the default `execute.on-script-change: accept`, a script-only `⚠` clears on the next execute; a spec-change `⚠`, or any `⚠` under `refuse`, needs `gtms link --refresh <tc-id> --framework <fw>` (see Stale Detection). On `gtms map` it is also the EXECUTE icon for an execute error, whether the source was a terminal result of `error` or a lifecycle status of `error`; text views of `gtms status` render both as `✗` |
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
A `fail` means the assertion ran and the subject failed -- triage it. A
latest `status: error` with no `result:` supersedes an earlier pass on the
dashboard.

In `gtms status --json`, `frameworks[]` holds one entry per framework with
evidence. A wired entry (`wired: true`) carries
`wiring_bootstrap: pending | ready` (first-execute bootstrap state) and
`wired_adapter` (the stored default runner). Once a terminal result is
joined it also carries `last_status_here` (execution state),
`last_result_here` (outcome), and `last_run_adapter` (who produced that
result) -- absent before the first result, so guard your jq. Wired and
last-run adapters differ after a one-run `--adapter` override, or after
repointing wiring that already has a result. Read those fields; do not
parse the icon column.

A manual result file adds an entry with `framework: "manual"` and
`wired: false`, on a wired TC and on a manual-only TC alike -- so a
manual-only TC no longer has `frameworks: []`. It carries `artefact` (the
manual result file), `artefact_present: true`, empty `wiring_drift` and
`wiring_bootstrap`, and never `wired_adapter`, `last_run_adapter` or
`last_executed_here`. Primed only: no `last_*` fields. Recorded pass/fail:
`last_status_here: complete`, `last_result_here: pass | fail`. Recorded skip:
both `skipped`. To ask "is this TC wired?", read the TC's top-level `wired`,
never `frameworks[]` emptiness. `selected_framework` is `null` by default and
`"manual"` only with `--framework manual`. A manual-only TC's `triage` object
stays at the top level, not on the `manual` entry; a wired TC nests manual
triage under its `manual` entry. `gtms map --json` carries the same entries,
and `gtms map --framework manual --json` puts the manual result file in the
top-level `artefact_path`. A refused handoff is left out of every JSON view;
stdout stays valid JSON, the diagnostic goes to stderr, and the exit is
non-zero.

### Stale Detection

The wiring record stores two hashes; each drifts independently, and execute
treats them differently:

- **`testcase-hash` drift** -- the TC spec changed since the wiring was
  written. Execute refuses under every setting
  (`was not run because its test case spec changed`; bulk
  `not run (spec changed)`). Check the script still tests the spec, then
  confirm that one TC: `gtms link --refresh <tc-id> --framework <fw>`.
  Confirm changed specs one TC at a time, never with a folder refresh.
  Execute never writes `testcase-hash`.
- **`artefact-hash` drift** -- the script changed since the last settled
  hash. Under the default `execute.on-script-change: accept`, the next
  execute records the new hash and runs the script
  (`GTMS accepted the change and ran <tc-id>`, then `File updated:` and the
  wiring file) -- no refresh. Under `refuse` (set in `gtms.config`), execute
  refuses (`not run (script changed)`) until the same per-TC refresh; bulk
  offers a folder refresh only when one shared changed script is the only
  change in that folder.

`--allow-stale` runs once with both checks bypassed and accepts nothing for
later runs. A missing or unreadable script (`script missing`,
`script unreadable`) or a spec GTMS cannot read (`spec unavailable`) is not
drift: restore the file or regenerate the script
(`gtms automate <tc-id> --framework <fw> --force`); `--refresh` cannot fix
it. Refusals hide the hash values; add `-v` to see them.

Tell a `⚠` apart: the `gtms status <tc-id>` detail card says
`test case spec changed`, `automation script changed`, or both; `gtms gaps`
lists `Test case specs changed` and `Automation scripts changed` separately;
`gtms status --json` carries `wiring_drift: testcase | artefact | both` per
framework. Under `accept`, only an `artefact`-only `⚠` clears on the next
execute.

Bulk execute prints `not run (<reason>)` for each refused TC (`spec changed`,
`script changed`, `script missing`, `script unreadable`, `spec unavailable`,
`choose a framework`), with the explanation underneath, and ends with
`N passed, N skipped, N not run, N failed, N errored`. Any not run, failed,
or errored TC makes the run exit non-zero.

`artefact-hash: pending` is not drift -- it is the normal state between
`gtms automate` and the first `gtms execute`, which settles it into a real
hash. `--allow-stale` does not bypass that bootstrap, and `--refresh` leaves
`pending` as `pending`.

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
| Identity comes from the body, not the filename | A test case spec's `test_case_id:` frontmatter IS its identity. A filename that disagrees is a renamed owner, NOT an error: `tc-aaaaaaaa-file.md` declaring `tc-bbbbbbbb` owns `tc-bbbbbbbb`, so `gtms prime tc-bbbbbbbb` works and `gtms prime tc-aaaaaaaa` reports not found. Every command that takes one test case refuses a duplicate id or an unreadable owner spec. An id no spec declares is refused by `automate`, `prime`, `execute`, `link`, `status <tc-id>` and `map <tc-id>`, but accepted by `reset` and `delete` (to clean up orphaned state) and listed by `triage --list <tc-id>` (marked `[spec missing]`) | Target the id in the BODY, not the one in the filename. If a command says a test case is not found, read the test case spec's `test_case_id:` -- that is the id to use. Still consume the pre-generated ids from `{tc_ids}` / `$GTMS_TC_IDS` when authoring: `gtms create` validates that its adapter honoured them |
| `CLAUDECODE` env var | Tier 1 adapters invoking `claude` inside Claude Code fail with "cannot be launched inside another session" | Use Tier 2 script adapters, or `unset CLAUDECODE` first |
| Windows binary name | Must use `gtms.exe` not `gtms` on Windows/MINGW | Always include `.exe` suffix in scripts |
| Wrong adapter on execute | `gtms execute` uses the wiring record's adapter by default. A cross-framework override (e.g. Playwright adapter on a BATS-wired TC) is rejected before any task is created | For one run, pass `--adapter <name>` where the adapter shares the wiring's framework. To change the runner permanently, `gtms link <tc-id> --adapter <name>` repoints the wiring record. A folder, `-r` or `--all` repoint also needs `--from-adapter <current>`, and changes only the records currently using that adapter. `defaults.execute` does not change a wired test case's runner (on the manual preset, where it is `manual-execute`, a bare execute takes the prime path instead -- pass `--framework` and `--adapter` to run the wired script) |
| Result-contract orthogonal vocabulary | The handoff contract has two orthogonal axes: `status:` carries adapter-execution state (`pending \| in-progress \| complete \| error`); `result:` carries test outcome (`pass \| fail \| skip \| error`). Validation rejects `status: fail` and `status: skipped`; writing either triggers a recovery overwrite to `status: error`. `status: complete` requires a non-empty `result:` field. `status: pass` and `status: fail` are invalid contract values | When reading or writing the **contract**: `status: complete` + `result: pass` for pass, `status: complete` + `result: fail` for fail, `status: complete` + `result: skip` for skip, `status: error` for adapter crash. On the **automation record**: `result: pass/fail/skipped/error`. The pipeline maps contract `result: skip` -> record `result: skipped` |
| A renamed test case spec's artefact is named for its OWNER, not its filename | `gtms automate` names the artefact `<owner-id>-<slug>`, where the slug is the whole source filename stem. A normally named test case spec is unchanged: `tc-1a2b3c4d-login.md` owned by `tc-1a2b3c4d` gives `tc-1a2b3c4d-login.bats`. A RENAMED owner keeps the full stem as description: `tc-aaaaaaaa-file.md` whose body declares `tc-bbbbbbbb` gives `tc-bbbbbbbb-tc-aaaaaaaa-file.bats`, NOT `tc-bbbbbbbb-file.bats` | Do not rename such an artefact to look tidier. Shortening it means deciding that `tc-aaaaaaaa-` is another identity's prefix and stripping it, which the id grammar makes ambiguous -- and a sweep keyed on that guess deletes the other identity's file. The name is deliberately ugly so ownership is never inferred |
| Automate will not overwrite an artefact it cannot prove is the target's | Ownership is a wiring record whose parsed `testcase` is the target and whose `artefact` names that file. `gtms automate` refuses when the planned output path is claimed by another test case, when it is occupied by a file no wiring record accounts for, when any wiring record cannot be read or parsed, or (with `--force`) when the output-directory sweep reaches a file it cannot examine. NOT gated on `--force`: a plain run refuses on the same terms | Do not retry `gtms automate` -- it is the command refusing. Re-establish ownership with `gtms link <tc-id> --framework <fw> --artefact <path>`, remove the file by hand once you have identified it, or point the adapter at another output location. The planned-path guarantee covers built-in automate only: a `command:` / `script:` adapter picks its own output path, so keeping it inside its own test case's artefact is the adapter's job |
| `gtms create` refuses a target outside its boundary | Create checks its target before it creates a folder, writes a task or result file, or starts the adapter. Default target: the `<folder>` argument must resolve inside the active test cases directory (`gtms/test/cases/` in a default layout), and that directory must resolve inside the project, through any symlink or junction; a link that stays inside the project but leaves the test cases directory is still refused: `create target "<input>" resolves outside the test cases directory`. A create adapter's configured `output-dir` must resolve inside the project; `..`, an escaping link or a broken link is refused: `create target "<input>" resolves outside the project`. A broken link in the default target gets the test-cases phrase. Exit is non-zero with no hint line. Automate, execute and prime `output-dir` are unchanged. `{output_dir}` / `GTMS_OUTPUT_DIR` carry the checked, link-resolved folder; GTMS cannot police other paths an external adapter writes on its own | Do not retry the unchanged command -- it is refused again. Replace the link with a real folder, repoint it inside the test cases directory, or move the adapter's `output-dir` inside the project (or drop it to use the default), then rerun. Links that stay inside their boundary, and a project opened through a link or junction, work normally |
| Automate pass is not a test pass | Only an EXECUTE-command `result: pass` counts as a test pass. A successful `automate`/`prime`/`create` is pipeline progress, not a test outcome -- `gtms status`, `gtms gaps`, and bulk `gtms execute` all read an automated-but-never-executed TC as not executed | Never infer a pass from a green automate. Bulk `gtms execute <folder>` runs freshly automated TCs; it skips a TC only on a genuine prior execute pass against the current artefact. `--force` re-runs even those -- it is not needed to run never-executed tests |
| Multi-file automate output | AI adapter emits two or more `<gtms-file>` tags for a single `gtms automate` invocation. GTMS rejects the run at automate time: task moves to `gtms/tasks/error/`, result contract `status: error`, **no** wiring record is written. The streamed files remain on disk for inspection | Inspect the streamed files, fix the adapter's prompt or output so it emits exactly one `<gtms-file>` per test case, re-run `gtms automate`. `create` is unaffected -- many files per invocation is legitimate there |
| `testcase-hash` is a write-side-only field | The `testcase-hash` field on the wiring record is written by `gtms automate` (create + `--force`) and `gtms link`. `gtms execute` never writes or refreshes it, and it is barred from auto-creating a wiring record -- so testcase-hash drift between the test case spec and the wiring record persists until an explicit confirmation. An agent that "fixes" a `Test case specs changed` signal by re-running execute will see the drift remain. `artefact-hash` differs: under the default `execute.on-script-change: accept`, execute records the hash of a script-only change it accepts | Check that the script still tests the spec, then confirm that one TC with `gtms link --refresh <tc> --framework <fw>` (or regenerate with `gtms automate <tc> --framework <fw> --force`, or relink with `gtms link <tc> --framework <fw> --artefact <path> --force`) -- not `gtms execute --force`. Never confirm changed specs with a folder refresh |
| TC ID `.md` suffix auto-stripped | All commands that accept a TC ID argument (`automate`, `execute`, `prime`, `link`, `triage`, `delete`, `map`, `reset`, `status`) silently normalise `tc-xxx.md` -> `tc-xxx`. Only TC-ID-shaped arguments are affected (base starts with `tc-`); folder names like `release.md` are never modified. Tab-completed filenames "just work" | Pass either `tc-a1b2c3d4` or `tc-a1b2c3d4.md` -- both resolve identically. A TC ID taken straight from a directory listing needs no `.md` stripping before you feed it to a `gtms` command |
| Folder arg is subfolder name (prefixes rejected) | Positional folder args must be the subfolder slug, not a prefixed path. Both `gtms/test/cases/my-feature` (long-form) and `test/cases/my-feature` (short-form) are rejected with "don't include the `gtms/test/cases/` prefix -- GTMS adds it automatically" | Pass the subfolder slug only: `gtms automate my-feature`. Same rule for create/delete/execute/gaps/map/reset/status/triage |
| CLI stream routing | Task completion, Adapter, Branch, Target folder, Processing header, summary counts -> **stdout**. Spinner, guidance, warnings, errors -> **stderr**. Bulk per-TC progress lines -> stderr (uses `\r` overwrite) | Parse `stdout` for task IDs and results. Parse `stderr` for errors only |
| PowerShell 5.1 turns GTMS stderr chrome into a fatal error | Three ingredients, all required: Windows PowerShell 5.1, `$ErrorActionPreference = 'Stop'`, and a redirect or capture of a native command's stderr inside PowerShell (`2>$null`, `2>&1`). Left alone, `gtms` stderr is just console text. The moment PowerShell captures the stream it wraps each stderr line in a `NativeCommandError` ErrorRecord, and `EAP='Stop'` promotes the first one to terminating -- so the redirect intended to silence the noise is what makes it fatal. GTMS is behaving correctly: chrome (spinner, guidance, warnings) goes to stderr by contract and the exit code is the failure signal. This never reproduces interactively, because EAP defaults to `Continue` at the prompt -- the failure appears exactly when ad-hoc commands graduate into a script | Treat `$LASTEXITCODE` as the ONLY failure signal from `gtms`; EAP governs cmdlet errors, not native tools. Then pick one: leave `gtms` stderr alone, scope `$ErrorActionPreference = 'Continue'` around the native call, or push the redirect into the child so PowerShell never sees the stream (`cmd /c "gtms.exe ... 2>nul"` survives). PowerShell 7 does not manufacture ErrorRecords from native stderr this way; 7.3+ adds `$PSNativeCommandUseErrorActionPreference`, which ties EAP to native exit codes -- different semantics, opt-in. `sh`-based adapters and shells are unaffected |
| Read-command not-found errors -> stderr | Both `gtms map tc-X` (text) and `gtms map tc-X --json` write `✗ Test case <id> not found.` to **stderr**. JSON consumers can run `gtms map tc-X --json 2>/dev/null \| jq` cleanly without scraping error chrome | Parse stdout for data and read stderr for diagnostics. When a read command prints nothing on stdout, check stderr before concluding the test case is missing |
| `gtms gaps` positional arg rejects TC IDs (existence-first) | The positional arg to `gtms gaps` is a folder/requirement scope, not a TC ID. A bare TC ID with no folder of that name -- e.g. `gtms gaps tc-a1b2c3d4` -- rejects with `✗ argument must be a folder, not a TC ID` on stderr and a non-zero exit. The check is **existence-first**: a `tc-`-prefixed name that IS a real folder (e.g. `gtms/test/cases/tc-regression/`) still scopes normally | For per-TC views use `gtms status tc-X`, and `gtms map tc-X` for traceability detail -- both accept TC IDs natively. For folder scope, pass the folder slug |
| Test case specs need the `test-case/v2` header | Every spec's frontmatter opens with `gtms_schema: "test-case/v2"`, then `type: Test Case Spec`, then `test_type: <category>`. `type` is ALWAYS the literal `Test Case Spec`; the test category (Functional, Integration, ...) goes in `test_type`. GTMS reads only v2 specs: an old spec (category in `type`, no `gtms_schema`) is refused, never guessed at, and `gtms create` fails any emitted spec without the header. The category reaches adapters as `GTMS_TEST_TYPE` (`GTMS_TC_TYPE` is gone) | Let `gtms create` stamp the header and never edit those three lines except to set the category. If a command prints `add gtms_schema: "test-case/v2" in <file>` or another `set ... in <file>` repair, make exactly that change and rerun. Converting a whole project: USER-GUIDE.md -> GTMS test case OKF profile -> Converting test case specs from an earlier version; after editing specs run `gtms link --refresh <folder> -r` |
| Read views exit 1 with partial output | `gtms status`, `map`, `gaps`, and `triage --list` over a folder, `-r`, or the whole project print every test case they can read, LEAVE OUT a duplicated id or an unreadable spec (no `test_case_id`, malformed frontmatter, invalid id, missing or wrong v2 header), name each on stderr (`✗ ... cannot be proved from <path>: <repair>.`), end with `✗ N test cases could not be read; the output above leaves them out`, and exit 1. `--json` stays valid (`[]` / zero totals when nothing is readable). `gtms status <tc-id>`, `map <tc-id>`, and `triage --list <tc-id>` refuse an affected target with nothing on stdout. When the target is fine, `map <tc-id>` still prints its requirement group but exits 1 if that group holds a duplicate or any spec in the project is unreadable. A refused handoff or results directory (see the refused-handoff row) has the same effect on `status`, `map`, `gaps` and `triage`: the refused file is left out, named on stderr, and the command exits 1 with JSON still valid | Check the exit code as well as the output: exit 1 with normal-looking rows or JSON means the result is incomplete. `jq -e` alone does not catch it. Fix the named files, then rerun |
| `gtms create` lists generated TC ids | After `gtms create`, the generated `tc-XXXXXXXX` ids and titles print on **stdout** directly under the "Task created" line (up to 10 inline; a bulk run over the threshold gets a truncated list with `...and N more. Run gtms status <folder> to see all.`). Works with both sync create and async `gtms create status`, which lists test cases on completion. The stderr guidance block carries a count summary (`N test cases created in gtms/test/cases/<target>/`), so no TC-id substrings reach stderr | To feed the next command, grep stdout for lines matching `^  tc-[0-9a-f]{8}  ` |
| Adapter warnings | Adapters can write `warnings: ["msg1", "msg2"]` to the result contract. GTMS merges these into the CLI output for `create`, `automate`, and `execute` commands, displayed with a `⚠` indicator. Adapters that don't populate this field see zero behaviour change | Surface adapter quality signals (e.g. "prompt template missing guides section") without failing the task. A warning never changes the recorded outcome -- read the `⚠` lines for advisory detail and the result contract for the outcome itself |
| Adapter stderr -> warnings | Anything an adapter writes to stderr on a **sync** invocation (Tier 1 or Tier 2) is line-split and surfaced as `⚠` warning lines on both success and failure paths. Each non-blank stderr line becomes a separate warning entry; blank lines are filtered, content is verbatim, no implicit cap. Async adapters are unaffected. Coexists with contract `warnings:` -- contract entries render first, stderr entries second, no collision | Use stderr for progress messages, deprecation warnings, and "you should know about X" diagnostics -- they surface as warnings without polluting stdout / `<gtms-file>` parsing or the captured summary. Don't write narration to stdout. An adapter that streams no `<gtms-file>` block also draws structural warnings ("Adapter ran successfully but produced 0 files." / "Adapter produced no output."), so the warning count can exceed the number of stderr lines |
| `log:` field YAML safety | GTMS sanitises `---` (YAML document separators) in the `log:` field, so adapters can safely write raw output there | The `log:` field is surfaced under `gtms status <tc-id>` for fail/error results. It lives on the terminal handoff / result contract (`.gtms/results/*.handoff.yaml`) -- truncated to 64 KB, with `notes-spill:` pointing at the full text under `.gtms/logs/{task-id}.log`. Execute writes the handoff plus a `gtms/execution/*.results.yaml` row, not a committed record |
| `--framework` filters TC list | `gtms execute -r --framework bats` skips TCs with no matching wiring. Bulk skip reasons read `no <framework> wiring` (e.g. `no bats wiring`) when a framework is set, and `not wired` when the TC has no wiring at all. A TC that is multi-wired with no `--framework` is not skipped: it is `not run (choose a framework)`, followed by its available frameworks, and makes the run exit non-zero. **Text output for pipeline skips renders as em-dashes** -- there is no `⚠ skipped (...)` glyph in any dashboard row; the skip is a data-layer signal, not a rendered one | Match skip reasons in bulk execute logs with `no .* wiring\|not wired`, and the multi-wired refusal with `not run (choose a framework)`. In `gtms status` / `gaps` text output, pipeline skips are indistinguishable from "no wiring anywhere" -- em-dashes either way. In `--json`, the folder-level `framework_mismatch` count reports how many TCs an explicit `--framework` filtered out. Per-TC detail view applies strict framework filtering when `--framework` is explicit |
| `--framework` strict per-TC | When `--framework X` is passed **explicitly** to `gtms status`/`gaps`/`map`, per-TC views (including folder detail, TC detail, JSON, and categorisation) filter strictly -- TCs without an X record show `—` in AUTOMATE / EXECUTE / LAST RESULT instead of falling back to another framework's record. When `--framework` is **omitted**, the adapter config default is used with graceful fallback preserved | On a dual-framework TC, an explicit `--framework pester` against a TC that carries only a bats record shows em-dashes in all three columns, not the bats result. With no flag, the record you see may come from either framework, so do not rely on which one appears |
| Create adapter ID-mismatch | GTMS inspects every emitted `.md` file after a `create` adapter returns. If any test case spec violates the contract -- missing or wrong `test-case/v2` header, unparseable frontmatter, no `test_case_id`, filename ID != frontmatter `test_case_id`, malformed ID, ID not in the pre-generated batch, or two test case specs sharing an ID -- the task exits non-zero, stderr shows `✗ Task failed: ...` with `    {filename}: {reason}` underneath, the result contract gets `status: error` + `validation-error: ...`, the task moves to `gtms/tasks/error/`, and the summary opens `Created spec failed validation.` A rejected file this create provably wrote (filename ID in its batch) is moved unchanged to `<gtms folder>/tasks/error/<task-id>/artifacts/` and reported as `Rejected output preserved at: <path>`; valid sibling specs stay. A file GTMS cannot prove it owns, a non-regular file, or an outside-project path stays where it is and is named; a failed move reads `Rejected output could not be preserved and remains at: <path> (<reason>)`. There is no degraded exception: unparseable frontmatter or a missing `test_case_id` fails the task too | **Authoring an adapter:** consume the pre-generated IDs via `{tc_ids}` / `$GTMS_TC_IDS` in order, one per test case spec, make each filename ID match its frontmatter `test_case_id`, and open every spec's frontmatter with the v2 header (`gtms_schema: "test-case/v2"`). **Reacting to a failure:** read the `    {filename}: {reason}` line -- it names the exact test case spec and the exact invariant that broke; inspect the quarantined copy under `artifacts/`, not the cases folder. Regenerate that test case spec with the correct ID rather than renaming the file. A header failure (no frontmatter, `gtms_schema` missing, malformed or another version) adds a producer line: for a shipped template or script it says `Run gtms init --force --preset <preset>` (demo script: `gtms init --force --demo`); for a custom adapter it says to emit `gtms_schema: "test-case/v2"` -- fix the adapter, do not run init. See `reference/adapter-guide.md` -> **Create Validation Contract** |
| Adapter name used as framework fallback | An execute adapter with no `framework:` field resolves its framework from the adapter's **name**. An adapter named `bats-runner` with no `framework:` set therefore resolves framework `bats-runner` and drives the wiring lookup toward `tc-XXX--bats-runner.wiring.yaml`, which won't exist if the wiring was written for framework `bats` -- so the run aborts before the adapter command runs | Under any adapter whose name doesn't equal the intended framework, set `framework: <fw>` in the adapter config: `adapters: { execute: { bats-runner: { framework: bats, mode: sync, command: '...' } } }`. Explicit `framework:` beats the adapter-name fallback |
| `gtms/test/cases/` prefix in folder args; trailing separator means "folder" | `gtms status gtms/test/cases/my-folder` is rejected -- GTMS adds the prefix itself. A trailing `/` or `\` is NOT rejected: it is the explicit "this is a folder" signal. So `gtms delete tc-abc12345/` is treated as a folder target named `tc-abc12345/` and does NOT delete the test case `tc-abc12345`. It is not a silent no-op either: when no such folder exists the command FAILS with `✗ No test cases folder found at gtms/test/cases/tc-abc12345/.` and a NON-ZERO exit | Pass just the folder slug: `gtms status my-folder`. Same rule for create/automate/execute/gaps. Never append a separator to a TC ID -- drop it to address the test case. Do not treat a trailing-separator target as harmless in a script: it exits non-zero, so an unguarded `set -e` run aborts there. When building a target string programmatically, strip any trailing separator before appending a TC ID |
| Detail view vs folder summary | `gtms status` (no arg) -> folder summary table with `TC / CREATE / AUTOMATE / EXECUTE` columns and icons (`✓ ✗ ● ○`), fraction suffix only on non-`✓` cells (e.g. `✗ 2/6`). Priority: `✗ > ● > ○ > ✓` (worst-news-wins). CREATE always `✓`. `gtms status <folder>` -> per-TC detail table with `CREATE / AUTOMATE / EXECUTE` columns and ✓/✗/— icons. `gtms status <tc-id>` -> per-stage detail view with each stage label followed by `, <timestamp>` inside its parens (CREATE/AUTOMATE show date `YYYY-MM-DD`, EXECUTE shows `YYYY-MM-DD HH:MM UTC`; never-run stages show `—` with no timestamp). For fail/error results a `Log:` block follows with the adapter's raw output (indented two spaces; header cites spill file when truncated to 64 KB). Output parsers written for one surface will not match another. **The EXECUTE icon means different things on the two surfaces.** In the folder/dashboard table it reports whether the stage RAN, so a test whose recorded result is `fail` still shows `✓` there and the failure is carried by the LAST RESULT column. In the `gtms status <tc-id>` detail view it reports the test RESULT: `✗` for a fail, `⊘` for a skip, `✗` for a run error, `✓` only for a pass. The same failing test is therefore `✓` in the table and `✗` on its own detail card -- intended, not a bug | Match your parsing to the invocation. The summary (no folder arg) carries icons, optional fractions, and a key-footer line (`Key: ✓ = all pass ...`). Folder detail carries TC IDs, icons and `[framework]` tags. TC detail adds the timestamp suffixes -- inserted BEFORE substrings like `[bats]` and `Pass`, so a substring match still hits -- and a `Log:` block on fail/error. JSON summary adds `passing`, `failing`, `errored`, `in_flight` alongside the existing counts |
| Status single-TC `--json` is flat; bulk status is a bare array; map is always a requirement-group tree | `gtms status <tc-id> --json` emits a flat **status detail** object: TC identity is top-level `testcase`, while execution state/outcome are per framework at `frameworks[].last_status_here` / `last_result_here`. Bulk status (`<folder>` / `-r`) is a bare array of status entries. `gtms map --json` uses a different contract on every target form: the document is a requirement-group tree whose entries carry `test_case_id`, `execute_status`, and `last_result`. Single-TC map remains group-preserving: the requested TC appears inside its whole requirement group, or under `unlinked[]` when it has no requirement. None of these forms has an `{entries: [...]}` wrapper. Missing result fields are normal before first execution, so CI/agent consumers must fail closed when they require one | Use `jq -e` so a missing TC/framework/field fails instead of silently reading as success. Status single: `jq -er '.frameworks[] \| select(.framework == "bats") \| .last_status_here'`; replace the final field with `.last_result_here` for outcome. Status bulk: `jq -er '.[] \| select(.testcase == "tc-XXX") \| .frameworks[] \| select(.framework == "bats") \| .last_status_here'`. Map, any target form: `jq -er '.. \| objects \| select(.test_case_id? == "tc-XXX") \| .execute_status'` (use `.last_result` for outcome). These selectors are command-specific; do not copy one from status to map or vice versa |
| Dual-framework TCs | One TC can carry wiring for several frameworks side by side: `gtms/automation/wiring/<tc-id>--bats.wiring.yaml` AND `<tc-id>--pester.wiring.yaml`. Each framework's execute runs independently; `gtms status --framework X` selects the X wiring | Automate (or `gtms link`) once per framework to produce both wiring records. Execute each framework separately -- single-command multi-framework execute is deferred |
| BATS tool-skip blind spot | BATS TAP marks skipped tests as `ok N # skip reason`, sharing the `ok` prefix with real passes, so a skip can read as a pass. CI summaries count skips separately and warn when skip > 0. If a skip appears, investigate: a tool-gated skip (e.g. `jq not available`) hides whether the test actually works | Review the "Skipped Tests" section of the CI step summary. Either install the missing tool or rewrite the test to avoid the dependency |
| Any skip demotes a BATS file to ⊘ | A `.bats` file with one passing `@test` and one skipping `@test` classifies as a skip: any `# skip` directive without a `not ok` line demotes the whole file. Fail still wins -- any `not ok` line classifies as fail regardless of skip count. The shipped `bats-runner` surfaces both counts in the result-contract `summary:` field (e.g. `"2 passed, 1 skipped"`) | The result contract reads `status: complete` + `result: skip`; `status: skipped` is rejected. Text views show ⊘ in both `gtms status` and `gtms map`. The JSON vocabularies differ: status exposes `frameworks[].last_status_here: "complete"` plus `last_result_here: "skipped"`; each map entry exposes `execute_status: "skipped"` plus `last_result: "skipped"` inside the surrounding traceability tree. Use the command-specific fail-closed selectors in the JSON-shape row above |
| Adapter-first dispatch | `gtms execute tc-X --framework manual` does NOT select the `manual-execute` adapter on **any** preset. The framework flag is independent of adapter resolution: dispatch keys on the resolved adapter's name or its configured framework, never on the CLI `--framework` flag as an adapter selector. A wired execute is routed by the wiring record's adapter; manual execute requires `--adapter manual-execute` or a manual execute default. To opt into manual-execute on any preset (`manual`, `bats`, `playwright`), pass `--adapter manual-execute` -- the `manual` preset already wires it as the execute default, `bats` and `playwright` do not. The four Mode 3 execute adapters (`manual-execute`, `agent-execute`, `manual-execute-script`, `agent-execute-script`) are manual-only and sync-only. When one is selected, GTMS refuses before any task, handoff or execution record, in this precedence: a non-manual `--framework` (`✗ Adapter '<adapter>' records manual results and cannot run under framework '<framework>'.` / `Omit --framework, or use --framework manual. To run the <framework> automation, use its adapter.`), then a non-manual configured `framework:` (`✗ Adapter '<adapter>' records manual results but gtms.config sets framework '<framework>'.` / `Remove the framework setting or set it to manual.`), then a configured `mode: async` (`✗ Adapter '<adapter>' records manual results but gtms.config sets mode 'async'.` / `Set its mode to sync.`). An unselected misconfigured entry does not break `status`, `gaps`, `map` or `triage`. Their errors stay on the manual view and never mark a wired framework as errored. Older handoffs and execution records are not rewritten: a wired framework's `last_run_adapter` naming one of the four marks a manual run still shown on that row | To confirm which adapter actually ran, read the `adapter:` field of the handoff under `.gtms/results/` rather than trusting the flag you passed. With a Mode 3 execute adapter, omit `--framework` or pass `--framework manual`; to run automation, use that framework's adapter. In `gtms.config`, give these adapters `framework: manual` (or none) and `mode: sync` (mode cannot be omitted). Clear a misattributed older row with `gtms reset <tc-id>`, then re-run the framework's own adapter -- reset also clears any genuine result there and leaves `gtms/execution/` records alone |
| Manual `gtms execute` missing-result-file directs to `gtms prime` | When `gtms execute` runs against a manual-execute adapter and the result file at `gtms/manual/records/{tc-id}--manual.result.yaml` is missing, the error message names `gtms prime tc-X --framework manual` -- not the generic `gtms automate` hint. The error flows through the standard `status: error` handoff path: task lands in `gtms/tasks/error/`, pipeline records are built, no silent short-circuit | Expect the prime hint on stderr and an error task on disk. If you see the generic "Re-run gtms automate" hint instead, the adapter resolved for that test case is not manual-execute -- check `defaults.execute` in `gtms.config`, or pass `--adapter manual-execute` explicitly |
| `gtms execute` bulk exits non-zero on any failure | Bulk `gtms execute` exits non-zero if any TC fails, errors, or was not run (refused: `not run (<reason>)`), even though it processed every TC -- a UNIX-style aggregate exit code. The summary reads `N passed, N skipped, N not run, N failed, N errored`. "The command completed" is not the same as "exit 0" | Read the per-TC results rather than the exit code alone when you need to know which TC failed. Bulk exits 0 only when nothing failed, errored, or was not run; a single-TC execute exits 0 for pass and non-zero for fail or error |
| `gtms execute` has no `--result` / `--notes` flags | The `--result` and `--notes` flags were removed in v0.2.0; Cobra rejects them with "unknown flag" | Record a manual outcome through the prime pipeline: `gtms prime <tc-id> --framework manual` -> edit `gtms/manual/records/<tc-id>--manual.result.yaml` -> `gtms execute <tc-id> --adapter manual-execute`. Never propose `--result` or `--notes` |
| `gtms link` repoint duplicate scan depends on the target form | Repoint mode (`gtms link <tc-id> --adapter <name>`) accepts a folder-qualified target such as `feature/tc-a1b2c3d4` as well as a bare ID; the qualified form is scope-validated, not rejected. That validation is also what runs the cross-folder duplicate check, so the scan depends on how you write the target: FOLDER and RECURSIVE repoints scan every TC they touch, and a FOLDER-QUALIFIED single-TC repoint is checked too -- all three abort before any write if a targeted TC ID has more than one test case spec anywhere in the project. A BARE single-TC target and `--all` skip the scan. Repoint separately rejects the four reserved prime-path execute adapter names as targets -- execute selects those by name before consulting wiring, so a record repointed to one would never drive a run. Repoint does NOT pick a test case spec: it rewrites the flat wiring record keyed on the bare TC ID plus framework, so when duplicate test case specs share an ID they all share that one record and a repoint affects every one of them. The qualifier only decides whether the ambiguity guard runs first -- it never narrows what gets rewritten. The `.md`-suffix auto-strip applies to repoint targets: `tc-a1b2c3d4.md` resolves like `tc-a1b2c3d4` | Qualify the target (`folder/tc-XXXXXXXX`) so the duplicate check runs on a single-TC repoint. Passing the bare ID skips that check -- it does NOT limit the repoint to one test case spec, it just rewrites the shared wiring unvalidated, so only use the bare form when you already know the ID is unique. On folder, recursive, or `--all` repoints, `--from-adapter <old>` is MANDATORY. Preview bulk repoints with `--dry-run` first (works in both flag positions) |
| An unusable Tier 2 result contract is recorded as an error, not a pass | If, after a synchronous Tier 2 adapter returns, its result contract is unparseable, missing, or unreadable, GTMS writes a readable replacement handoff with `status: error` and no `result:`, files the task under `gtms/tasks/error/`, and bulk `gtms execute` counts the TC as errored, not passed. If GTMS cannot save that replacement (refused, other writers keep replacing it, or both write attempts fail), the run fails instead. Exit policy is per command: `execute`, `create` and `prime` exit non-zero; single-target `automate` renders the error and exits 0; bulk `automate` exits non-zero. Tier 1 is unchanged. The usual cause is a `log: \|` block whose indent YAML infers from the tool's first output line | Write `log: \|2` (explicit two-space indent) fed by `sed 's/^/  /'`, fix the script, and re-run the affected TCs -- a pass recorded through an unusable contract was never verified. Check the runner for bare `log: \|` headers first; if it already writes `log: \|2` the cause is elsewhere. For a runner that `gtms init` shipped, either change its two `log: \|` lines to `log: \|2` (fixes only this defect; an older Playwright runner keeps its exit-code classification), or run `gtms init --force --preset <bats\|playwright>`, which refreshes a pristine recognised older runner in place and rewrites `gtms.config` after saving the old file as `gtms.config.bak` (or the next free `.bak.N`); re-apply your customisations from the backup it names. An edited runner gets a `.gtms-new` companion to merge. BATS recognises the v0.3.0 to v0.3.2 runner; its v0.2.0 runner is not recognised and takes the two-line edit or the companion. See `reference/adapter-guide.md` -> **Updating the Result Contract** |
| A Tier 2 execute adapter must write a terminal status | A synchronous Tier 2 `execute` adapter that exits (any code) leaving a readable contract still `pending`, `in-progress` (what GTMS stamps before invoking) or empty-status is NOT a pass: GTMS records `status: error` with no `result:`, files the task under `gtms/tasks/error/`, writes an error execution record, and exits non-zero (bulk counts it errored). The usual message is `✗ Execution result was not recorded: the adapter exited 0 without reporting a terminal outcome (result contract status: <status>)` (`exited with code N` on a non-zero exit, with stderr following), then `A Tier 2 execute adapter must write a terminal status to $GTMS_RESULT_FILE before it exits, and a result when status is complete`. On a refused handoff or a concurrency error the refusal or concurrency message replaces it and no execution record is written. GTMS stamps `completed` on a valid terminal contract that omits it, keeping an adapter value; failing to save that or another GTMS-owned field fails the run. Tier 1 stays exit-code-owned, and Tier 2 `create` / `automate` / `prime` keep the exit-code fallback -- but no outcome counts until its terminal handoff write is saved. Known edges after a failed save: an adapter-written `result: pass` can stay in the handoff and let a later bulk run skip the TC; `gtms status` can keep showing an older pass | Before the script exits, write `status: complete` plus `result: pass \| fail \| skip`, or `status: error`. If `gtms execute` failed while saving its result, run `gtms reset <tc-id>` and re-run the TC, or pass `--force` to the bulk run, before trusting that framework's row or an already-passing skip |
| Refused handoffs under `.gtms/results/` | GTMS refuses, for reads and writes, a handoff that is a symlink/junction, hard link, directory, FIFO, device or socket, and a `.gtms/results` that resolves outside the project or cannot be inspected (a FIFO there is refused, not opened). In-project directory links and a project reached through a link work. Messages: `✗ refused handoff <path>: <reason>` / `remove it by hand, or replace it with an ordinary single-link regular file under .gtms/results, and retry`, and `✗ refused results directory .gtms/results: <reason>` / `make it an ordinary directory inside the project, or point its link to a directory inside the project, and retry`. Reasons: `is a link`, `has more than one hard link`, `is not a regular file`, `already exists`, `resolves outside the project`, `could not be inspected: <cause>`. Found before invocation, the adapter does not run; an adapter that replaces its own handoff is caught after it ran, the task errors, and execute (only execute) prints `<tc> started, then failed.` (bulk: `started, then failed`). A refused file is never a result, vetoes every already-passing skip in that bulk run (the exit still follows the TCs' own outcomes), makes `status`/`gaps`/`map`/`triage` exit non-zero (and a `<cmd> status` subcommand whose own task it is), and makes `triage --value` refuse. Concurrency is different: `handoff <path> kept changing during the update: other writers replaced it through <N> retries` is not a refusal and the file needs no repair. GTMS's own handoff writes are all-or-nothing (complete old or complete new document); an adapter's direct writes to `$GTMS_RESULT_FILE` are not covered. Only exact `<handoff>.tmp-<16 lowercase hex>` files are GTMS staging leftovers | Never create a link or special file in `.gtms/results/`; write `$GTMS_RESULT_FILE` in place and close it before exiting. To repair, remove the refused file or replace it with an ordinary single-link file, and make `.gtms/results` an ordinary in-project folder. `gtms reset` / `gtms delete` leave a refused file in place, finish the safe cleanup and exit non-zero (`--dry-run` too) -- use that as a pre-check. After a concurrency error on execute, re-run the TC (`--force` in bulk) |
| Triage category names are values, not flags | `--automation-wrong`, `--test-wrong`, and `--app-wrong` are not registered flags. Cobra rejects them with `Error: unknown flag: --test-wrong` and writes no GTMS project state. `automation-wrong`, `test-wrong`, and `app-wrong` remain default keys accepted through `--value` | Classify with `gtms triage <tc-id> --value <key>`, for example `--value test-wrong`. Classification records the triage decision only: it queues no automate task, edits no test case spec, and appends no history. Run any follow-up separately; read the stored decision with `--show` or `--list --json` |
| Source build output | Inside an initialised checkout, `gtms/` is the data directory. Giving `go build` the bare output name `gtms` writes `gtms/gtms` (`gtms/gtms.exe` on Windows) and exits successfully | For a local checkout build, use `gtms.exe` on Windows or `gtms-linux` on Linux/macOS/WSL, or run `make build`. Installed or downloaded Unix binaries remain correctly named `gtms` |

---

## Debugging Failures

When a test fails, always go back to the spec:

1. Read the test case spec (`gtms/test/cases/<folder>/tc-XXXXXXXX-*.md`) -- what should the test verify?
2. Read the automation artefact -- does the script correctly implement the spec?
3. Identify the gap:
   - Script doesn't match spec -> fix the script
   - Spec is wrong -> update the spec
   - Script matches spec, subject fails -> triage it

**Don't re-execute blindly.** If the root cause is a wrong adapter or broken script, re-executing produces the same failure. Investigate first.

---

## Further Reading

- USER-GUIDE.md -- the full narrative: modes, walkthroughs, file formats, configuration, command reference, troubleshooting (run gtms --help for the version-pinned URL). The `test-case/v2` format and its repair messages: USER-GUIDE.md -> GTMS test case OKF profile
- Adapter Guide -- adapter tiers, configuration, env-var and result contracts, framework notes (run gtms --help for the version-pinned URL)
- `gtms skills` -- starter agent skills: five wrap the pipeline (create, automate, execute, prime, verify-intent) and gtms-okf-index adds repository navigation of the test-case index; install by copying from gtms/skills/ into your skills directory
- `gtms getting-started` -- linear quick start for a new project or a new agent: the shortest path from `gtms init` to a recorded result
- `gtms <command> --help` -- flag-level detail from the binary you are running
