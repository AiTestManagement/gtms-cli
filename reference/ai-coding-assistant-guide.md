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
| `triage` | `gtms triage <tc-id>` | Classify the latest result with `--value <key>`, `--resolve` an existing classification, or read records back with `--show` / `--list` |

### Visibility Commands

| Command | Syntax | Purpose |
|---------|--------|---------|
| `status` | `gtms status [tc-id \| folder]` | Pipeline dashboard. Bare = folder summary; folder = per-TC table; `tc-id` = detail card. `--show-triage` / `--triage <value>` add the triage lens |
| `gaps` | `gtms gaps [folder]` | Coverage gaps by category. Folder scope only -- a TC ID errors and points you at `gtms status <tc-id>`. TRIAGED / UNTRIAGED columns are always on |
| `map` | `gtms map [tc-id \| folder]` | Test cases grouped by requirement (traceability); `--detail` for full titles |
| `list` | `gtms list <adapters\|frameworks\|all>` | Adapter and framework inventory. Bare `gtms list` prints help, not data |

### Lifecycle Commands

| Command | Syntax | Purpose |
|---------|--------|---------|
| `init` | `gtms init [--preset manual\|bats\|playwright]` | Scaffold a project. Plain `gtms init` in a repo with no GTMS project = `manual` preset; in an initialised project it lists presets and exits. `--presets` lists without scaffolding |
| `link` | `gtms link <tc-id> --framework <fw> --artefact <path>` | Write or repair a wiring record by hand; `--refresh` re-hashes after a deliberate spec or script edit; `--adapter <name>` repoints the stored execute adapter (see Key Flags) |
| `delete` | `gtms delete <tc-id \| folder>` | Remove GTMS-owned state: test case specs, wiring records, manual records, result files, task files, result contracts. Referenced test artefacts are PRESERVED by default (`--force-artefact-deletion` to remove them; `--keep-spec` to keep specs; `--dry-run` to preview) |
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
| `--adapter` | create, prime, automate, execute, link | Select the adapter (overrides `defaults.<command>`). On a wired execute, selects a same-framework adapter as the runner for this invocation only; cross-framework is rejected. All four Mode 3 execute names (manual-execute, agent-execute, and their -script variants) take the prime path instead and are never wiring runners or repoint targets. On link, repoints the stored execute adapter permanently: same framework only, over a single TC (bare `tc-XXXXXXXX` or folder-qualified `folder/tc-XXXXXXXX`) or a folder/`-r`/`--all` scope |
| `--framework` | prime, automate, execute, link, triage, status, gaps, map | Framework label on write commands; selector/filter on read commands. Required on execute when a TC is wired for more than one framework; required on triage when more than one candidate exists (several wired frameworks, or a wired framework plus a recorded manual execution -- `manual` is a candidate, `manual-execute` is an adapter name and does not select it) |
| `--reference` | create | Requirement identifier recorded in the test case (e.g. `REQ-001`) |
| `--focus` | create | Focus area within the source document (prompt-driven adapters) |
| `--context-file` | create, prime, automate | Supplementary context file for the adapter |
| `--env` | automate, execute | Environment label recorded and passed to the adapter; GTMS does not route on it |
| `--executed-by` | prime, automate, execute | Operator identity recorded on the run (default: `GTMS_EXECUTED_BY`, then git `user.name`) |
| `--repo` | init | Repository identity stored as `project.repo`. Inferred from the remote named `origin`; when no identity resolves there, GTMS stores the reserved value `no-origin-remote`. That sentinel reaches adapters unchanged as `{repo}` / `GTMS_REPO` -- treat it as "identity not configured", never build a URL from it. `--repo org/name` sets it explicitly |
| `-r, --recursive` | automate, execute, status, gaps, map, delete, reset, link (refresh, repoint), triage (`--list` only) | Include subdirectories |
| `--force` | init, prime, automate, execute, link | Meaning varies: overwrite config or allow a nested project (init; with `--preset bats` or `--preset playwright` it also upgrades a pristine recognised older bats or Playwright runner in place, skips the current shipped runner, and leaves an edited or unknown runner untouched while writing the shipped version beside it as a `.gtms-new` companion (or the next `.gtms-new.N` name when needed); the v0.2.0 bats runner is not recognised and gets the companion; no other preset asset is upgraded), overwrite result template (prime), reprocess/overwrite (automate, link), re-run already-passing TCs in a bulk run (execute -- wiring and drift skips still apply) |
| `--update-hash` | prime | Refresh `test_case_hash` only; preserves the filled `result:` and execute history |
| `--allow-stale` | execute | Skip the wiring drift check for this run only; does NOT update wiring or bypass the pending bootstrap |
| `--guidance-off` / `--guidance-on` | init | Turn the post-command guidance block off or on and record the choice in `gtms.config`. Guidance is on by default and prints after most commands, so turning it off narrows what you have to parse |
| `--fail-fast` | automate, execute | Stop a bulk run on first failure |
| `--json` | status, gaps, map, list, triage | Machine-readable output. On triage reads: single object for `--show`, bare array for `--list` (`[]` when empty) |
| `--show`, `--list` | triage | Read records back. Exactly one PRIMARY operation per invocation (`--value`, `--resolve`, `--show`, `--list`); under `--list`, `--value` is a filter, not a second operation, so `--list --value <key>` is one operation. `--show` returns one record active OR resolved and prints its `summary`/`defect` when present; `--list [folder|test-case-id]` returns many, active-only until `--include-resolved`. The `--list` positional is a folder OR a test-case ID (existence-first; a trailing `/` forces folder); a test-case ID lists every framework record for that test case. Root and test-case-target `--list` include orphans, marked `[spec missing]`; a folder list does not. A stored defect renders as a trailing `[<defect>]` marker (before `[spec missing]`). Under `--list`, `--value` filters instead of classifying, `--framework` is rejected, and `-r` is rejected with a test-case target |
| `--include-resolved` | triage | Widen a `--list` to resolved records (no resolved-only view exists) |
| `--show-triage` | status | Turn on triage presentation: folder-summary `TRIAGED` count, per-TC `TRIAGE` column, detail-card `TRIAGE:` block. WITHOUT it human status output is byte-identical to what it was before triage existed |
| `--triage <value>` | status | Filter to test cases carrying that stored value; implies `--show-triage`. REFUSED at bare root (needs a folder, TC ID, or `-r`). An empty match is exit 0 with a diagnostic, not an error |
| `--show-tools` | list | Add the TOOL column (command/script path); JSON always includes `tool` |
| `--dry-run` | delete, reset, link (refresh, repoint) | Preview without touching files, listing exactly which records would change. On link it works in either flag position, but the two modes do not share a scope set: an `--adapter` repoint takes a single TC, a folder, `-r`, or `--all`; a `--refresh` takes a single TC, a folder, `-r`, or `--framework`. `--all` is repoint-only -- `link --all --refresh` is rejected before any preview runs. `link` write mode and `--check` still REJECT it; every other command parses it but ignores it -- do not rely on it elsewhere |
| `--keep-spec` | delete | Delete pipeline artefacts, keep the test case specs |
| `--force-artefact-deletion` | delete | Also delete the test artefacts wiring and manual records point at. Without it they are preserved and named in the summary. With it, an artefact is removed only when every reference to it belongs to the deletion set. That check reads each record's declared `testcase:` identity, so a same-ID test case in another folder and a longer `tc-xxxxxxxx--variant` sibling both count as outside references and their artefacts are preserved. A record that cannot be parsed, or whose declared identity disagrees with its filename, makes the forced delete refuse rather than risk a shared file. A bare TC ID that exists in more than one folder is refused before anything is removed -- qualify it as `folder/tc-xxxxxxxx` |
| `--artefact` / `--check` / `--refresh` / `--strict` | link | Artefact path (write mode) / validate without writing / re-hash in place / reject TC IDs with no spec |
| `--from-adapter` / `--all` | link | Repoint only: exact-match filter on the stored adapter (MANDATORY for folder, recursive, and `--all` repoints) / project-wide repoint scope (takes no positional, no `-r`) |
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
- Downstream commands resolve the id through the spec's `test_case_id`
  frontmatter, project-wide. An id declared by two specs, or a candidate spec
  that cannot be read or carries no usable `test_case_id`, refuses the command.
  A filename disagreeing with the body id does NOT: that is a renamed owner and
  it works. Keep ids unique project-wide -- `gtms create` validates only the
  files one invocation emitted, so nothing stops you WRITING a duplicate; you
  find out at the next command that has to act on it.

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
framework -- no orphan skeleton is left on disk. The manual preset only
configures the manual execution path; before using the automate path for BATS
or Playwright, configure an execute adapter for that framework, or use the
prime path.

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
gtms triage --list <tc-id>                                      # every framework record for ONE test case (orphans included; no -r)
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
file that was never executed is not enough. `--framework` is required when
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
| `⚠` (yellow) | Stale (wiring drift), appended after the stage icon where stale wiring is shown. On `gtms map` it is also the EXECUTE icon for an execute error, whether the source was a terminal result of `error` or a lifecycle status of `error`; text views of `gtms status` render both as `✗` |
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
`wiring_bootstrap: pending | ready` (first-execute bootstrap state) and
`wired_adapter` (the stored default runner). Once a terminal result is
joined it also carries `last_status_here` (execution state),
`last_result_here` (outcome), and `last_run_adapter` (who produced that
result) -- absent before the first result, so guard your jq. Wired and
last-run adapters differ after a one-run `--adapter` override, or after
repointing wiring that already has a result. Read those fields; do not
parse the icon column.

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
| Identity comes from the body, not the filename | A test case spec's `test_case_id:` frontmatter IS its identity. A filename that disagrees is a renamed owner, NOT an error: `tc-aaaaaaaa-file.md` declaring `tc-bbbbbbbb` owns `tc-bbbbbbbb`, so `gtms prime tc-bbbbbbbb` works and `gtms prime tc-aaaaaaaa` reports not found. `automate`, `prime` and `execute` still refuse a target with no proven owner, a duplicate id, or missing required frontmatter | Target the id in the BODY, not the one in the filename. If a command says a test case is not found, read the test case spec's `test_case_id:` -- that is the id to use. Still consume the pre-generated ids from `{tc_ids}` / `$GTMS_TC_IDS` when authoring: `gtms create` validates that its adapter honoured them |
| `CLAUDECODE` env var | Tier 1 adapters invoking `claude` inside Claude Code fail with "cannot be launched inside another session" | Use Tier 2 script adapters, or `unset CLAUDECODE` first |
| Windows binary name | Must use `gtms.exe` not `gtms` on Windows/MINGW | Always include `.exe` suffix in scripts |
| Wrong adapter on execute | `gtms execute` uses the wiring record's adapter by default. A cross-framework override (e.g. Playwright adapter on a BATS-wired TC) is rejected before any task is created | For same-framework overrides, pass `--adapter <name>` where the adapter shares the wiring's framework. For projects with one framework, set the default in `gtms.config`. `gtms init --preset bats` ships `bats-runner` as the default execute adapter |
| Result-contract orthogonal vocabulary | The handoff contract has two orthogonal axes: `status:` carries adapter-execution state (`pending \| in-progress \| complete \| error`); `result:` carries test outcome (`pass \| fail \| skip \| error`). Validation rejects `status: fail` and `status: skipped`; writing either triggers a recovery overwrite to `status: error`. `status: complete` requires a non-empty `result:` field. `status: pass` and `status: fail` are invalid contract values | When reading or writing the **contract**: `status: complete` + `result: pass` for pass, `status: complete` + `result: fail` for fail, `status: complete` + `result: skip` for skip, `status: error` for adapter crash. On the **automation record**: `result: pass/fail/skipped/error`. The pipeline maps contract `result: skip` -> record `result: skipped` |
| A renamed test case spec's artefact is named for its OWNER, not its filename | `gtms automate` names the artefact `<owner-id>-<slug>`, where the slug is the whole source filename stem. A normally named test case spec is unchanged: `tc-1a2b3c4d-login.md` owned by `tc-1a2b3c4d` gives `tc-1a2b3c4d-login.bats`. A RENAMED owner keeps the full stem as description: `tc-aaaaaaaa-file.md` whose body declares `tc-bbbbbbbb` gives `tc-bbbbbbbb-tc-aaaaaaaa-file.bats`, NOT `tc-bbbbbbbb-file.bats` | Do not rename such an artefact to look tidier. Shortening it means deciding that `tc-aaaaaaaa-` is another identity's prefix and stripping it, which the id grammar makes ambiguous -- and a sweep keyed on that guess deletes the other identity's file. The name is deliberately ugly so ownership is never inferred |
| Automate will not overwrite an artefact it cannot prove is the target's | Ownership is a wiring record whose parsed `testcase` is the target and whose `artefact` names that file. `gtms automate` refuses when the planned output path is claimed by another test case, when it is occupied by a file no wiring record accounts for, when any wiring record cannot be read or parsed, or (with `--force`) when the output-directory sweep reaches a file it cannot examine. NOT gated on `--force`: a plain run refuses on the same terms | Do not retry `gtms automate` -- it is the command refusing. Re-establish ownership with `gtms link <tc-id> --framework <fw> --artefact <path>`, remove the file by hand once you have identified it, or point the adapter at another output location. The planned-path guarantee covers built-in automate only: a `command:` / `script:` adapter picks its own output path, so keeping it inside its own test case's artefact is the adapter's job |
| Automate pass is not a test pass | Only an EXECUTE-command `result: pass` counts as a test pass. A successful `automate`/`prime`/`create` is pipeline progress, not a test outcome -- `gtms status`, `gtms gaps`, and bulk `gtms execute` all read an automated-but-never-executed TC as not executed | Never infer a pass from a green automate. Bulk `gtms execute <folder>` runs freshly automated TCs; it skips a TC only on a genuine prior execute pass against the current artefact. `--force` re-runs even those -- it is not needed to run never-executed tests |
| Multi-file automate output | AI adapter emits two or more `<gtms-file>` tags for a single `gtms automate` invocation. GTMS rejects the run at automate time: task moves to `gtms/tasks/error/`, result contract `status: error`, **no** wiring record is written. The streamed files remain on disk for inspection | Inspect the streamed files, fix the adapter's prompt or output so it emits exactly one `<gtms-file>` per test case, re-run `gtms automate`. `create` is unaffected -- many files per invocation is legitimate there |
| `testcase-hash` is a write-side-only field | The `testcase-hash` field on the wiring record is written by `gtms automate` (create + `--force`) and `gtms link`. `gtms execute` never writes or refreshes it, and it is barred from auto-creating a wiring record -- so testcase-hash drift between the test case spec and the wiring record persists until an explicit re-wire. An agent that "fixes" a `Stale wiring (testcase)` signal by re-running execute will see the drift remain | To clear `testcase-hash` drift, run `gtms automate <tc> --framework <fw> --force` or `gtms link <tc> --framework <fw> --artefact <path>` -- not `gtms execute --force`. Same ownership rule as `artefact-hash`: the hashes are written by the wiring-producing commands, never by execute |
| TC ID `.md` suffix auto-stripped | All commands that accept a TC ID argument (`automate`, `execute`, `link`, `triage`, `delete`, `map`, `reset`, `status`) silently normalise `tc-xxx.md` -> `tc-xxx`. Only TC-ID-shaped arguments are affected (base starts with `tc-`); folder names like `release.md` are never modified. Tab-completed filenames "just work" | Pass either `tc-a1b2c3d4` or `tc-a1b2c3d4.md` -- both resolve identically. A TC ID taken straight from a directory listing needs no `.md` stripping before you feed it to a `gtms` command |
| Folder arg is subfolder name (prefixes rejected) | Positional folder args must be the subfolder slug, not a prefixed path. Both `gtms/test/cases/my-feature` (long-form) and `test/cases/my-feature` (short-form) are rejected with "don't include the `gtms/test/cases/` prefix -- GTMS adds it automatically" | Pass the subfolder slug only: `gtms automate my-feature`. Same rule for create/delete/execute/gaps/map/reset/status/triage |
| CLI stream routing | Task completion, Adapter, Branch, Target folder, Processing header, summary counts -> **stdout**. Spinner, guidance, warnings, errors -> **stderr**. Bulk per-TC progress lines -> stderr (uses `\r` overwrite) | Parse `stdout` for task IDs and results. Parse `stderr` for errors only |
| PowerShell 5.1 turns GTMS stderr chrome into a fatal error | Three ingredients, all required: Windows PowerShell 5.1, `$ErrorActionPreference = 'Stop'`, and a redirect or capture of a native command's stderr inside PowerShell (`2>$null`, `2>&1`). Left alone, `gtms` stderr is just console text. The moment PowerShell captures the stream it wraps each stderr line in a `NativeCommandError` ErrorRecord, and `EAP='Stop'` promotes the first one to terminating -- so the redirect intended to silence the noise is what makes it fatal. GTMS is behaving correctly: chrome (spinner, guidance, warnings) goes to stderr by contract and the exit code is the failure signal. This never reproduces interactively, because EAP defaults to `Continue` at the prompt -- the failure appears exactly when ad-hoc commands graduate into a script | Treat `$LASTEXITCODE` as the ONLY failure signal from `gtms`; EAP governs cmdlet errors, not native tools. Then pick one: leave `gtms` stderr alone, scope `$ErrorActionPreference = 'Continue'` around the native call, or push the redirect into the child so PowerShell never sees the stream (`cmd /c "gtms.exe ... 2>nul"` survives). PowerShell 7 does not manufacture ErrorRecords from native stderr this way; 7.3+ adds `$PSNativeCommandUseErrorActionPreference`, which ties EAP to native exit codes -- different semantics, opt-in. `sh`-based adapters and shells are unaffected |
| Read-command not-found errors -> stderr | Both `gtms map tc-X` (text) and `gtms map tc-X --json` write `✗ Test case <id> not found.` to **stderr**. JSON consumers can run `gtms map tc-X --json 2>/dev/null \| jq` cleanly without scraping error chrome | Parse stdout for data and read stderr for diagnostics. When a read command prints nothing on stdout, check stderr before concluding the test case is missing |
| `gtms gaps` positional arg rejects TC IDs (existence-first) | The positional arg to `gtms gaps` is a folder/requirement scope, not a TC ID. A bare TC ID with no folder of that name -- e.g. `gtms gaps tc-a1b2c3d4` -- rejects with `✗ argument must be a folder, not a TC ID` on stderr and a non-zero exit. The check is **existence-first**: a `tc-`-prefixed name that IS a real folder (e.g. `gtms/test/cases/tc-regression/`) still scopes normally | For per-TC views use `gtms status tc-X`, and `gtms map tc-X` for traceability detail -- both accept TC IDs natively. For folder scope, pass the folder slug |
| `gtms create` lists generated TC ids | After `gtms create`, the generated `tc-XXXXXXXX` ids and titles print on **stdout** directly under the "Task created" line (up to 10 inline; a bulk run over the threshold gets a truncated list with `...and N more. Run gtms status <folder> to see all.`). Malformed or missing frontmatter degrades to filename-only and does not block the listing. Works with both sync create and async `gtms create status`, which lists test cases on completion. The stderr guidance block carries a count summary (`N test cases created in gtms/test/cases/<target>/`), so no TC-id substrings reach stderr | To feed the next command, grep stdout for lines matching `^  tc-[0-9a-f]{8}  ` |
| Adapter warnings | Adapters can write `warnings: ["msg1", "msg2"]` to the result contract. GTMS merges these into the CLI output for `create`, `automate`, and `execute` commands, displayed with a `⚠` indicator. Adapters that don't populate this field see zero behaviour change | Surface adapter quality signals (e.g. "prompt template missing guides section") without failing the task. A warning never changes the recorded outcome -- read the `⚠` lines for advisory detail and the result contract for the outcome itself |
| Adapter stderr -> warnings | Anything an adapter writes to stderr on a **sync** invocation (Tier 1 or Tier 2) is line-split and surfaced as `⚠` warning lines on both success and failure paths. Each non-blank stderr line becomes a separate warning entry; blank lines are filtered, content is verbatim, no implicit cap. Async adapters are unaffected. Coexists with contract `warnings:` -- contract entries render first, stderr entries second, no collision | Use stderr for progress messages, deprecation warnings, and "you should know about X" diagnostics -- they surface as warnings without polluting stdout / `<gtms-file>` parsing or the captured summary. Don't write narration to stdout. An adapter that streams no `<gtms-file>` block also draws structural warnings ("Adapter ran successfully but produced 0 files." / "Adapter produced no output."), so the warning count can exceed the number of stderr lines |
| `log:` field YAML safety | GTMS sanitises `---` (YAML document separators) in the `log:` field, so adapters can safely write raw output there | The `log:` field is surfaced under `gtms status <tc-id>` for fail/error results. It lives on the terminal handoff / result contract (`.gtms/results/*.handoff.yaml`) -- truncated to 64 KB, with `notes-spill:` pointing at the full text under `.gtms/logs/{task-id}.log`. Execute writes the handoff plus a `gtms/execution/*.results.yaml` row, not a committed record |
| `--framework` filters TC list | `gtms execute -r --framework bats` skips TCs with no matching wiring. Bulk skip reasons read `no <framework> wiring` (e.g. `no bats wiring`) when a framework is set, `not wired` when the TC has no wiring at all, and `multiple frameworks -- specify --framework` when a TC is multi-wired and none was given. **Text output for pipeline skips renders as em-dashes** -- there is no `⚠ skipped (...)` glyph in any dashboard row; the skip is a data-layer signal, not a rendered one | Match skip reasons in bulk execute logs with `no .* wiring\|not wired\|multiple frameworks`. In `gtms status` / `gaps` text output, pipeline skips are indistinguishable from "no wiring anywhere" -- em-dashes either way. In `--json`, the folder-level `framework_mismatch` count reports how many TCs an explicit `--framework` filtered out. Per-TC detail view applies strict framework filtering when `--framework` is explicit |
| `--framework` strict per-TC | When `--framework X` is passed **explicitly** to `gtms status`/`gaps`/`map`, per-TC views (including folder detail, TC detail, JSON, and categorisation) filter strictly -- TCs without an X record show `—` in AUTOMATE / EXECUTE / LAST RESULT instead of falling back to another framework's record. When `--framework` is **omitted**, the adapter config default is used with graceful fallback preserved | On a dual-framework TC, an explicit `--framework pester` against a TC that carries only a bats record shows em-dashes in all three columns, not the bats result. With no flag, the record you see may come from either framework, so do not rely on which one appears |
| Create adapter ID-mismatch | GTMS inspects every emitted `.md` file after a `create` adapter returns. If any test case spec violates the contract -- filename ID != frontmatter `test_case_id`, malformed ID, ID not in the pre-generated batch, or two test case specs sharing an ID -- the task exits non-zero, stderr shows `✗ Task failed: ...` with `    {filename}: {reason}` underneath, the result contract gets `status: error` + `validation-error: ...`, the task moves to `gtms/tasks/error/`, and the offending files stay on disk. A test case spec with unparseable frontmatter, or none carrying `test_case_id`, is the exception: GTMS degrades it to filename-only listing and the create task still succeeds | **Authoring an adapter:** consume the pre-generated IDs via `{tc_ids}` / `$GTMS_TC_IDS` in order, one per test case spec, and make each filename ID match its frontmatter `test_case_id`. **Reacting to a failure:** read the `    {filename}: {reason}` line -- it names the exact test case spec and the exact invariant that broke. Regenerate that test case spec with the correct ID rather than renaming the file. See `reference/adapter-guide.md` -> **Create Validation Contract** |
| Adapter name used as framework fallback | An execute adapter with no `framework:` field resolves its framework from the adapter's **name**. An adapter named `bats-runner` with no `framework:` set therefore resolves framework `bats-runner` and drives the wiring lookup toward `tc-XXX--bats-runner.wiring.yaml`, which won't exist if the wiring was written for framework `bats` -- so the run aborts before the adapter command runs | Under any adapter whose name doesn't equal the intended framework, set `framework: <fw>` in the adapter config: `adapters: { execute: { bats-runner: { framework: bats, mode: sync, command: '...' } } }`. Explicit `framework:` beats the adapter-name fallback |
| `gtms/test/cases/` prefix in folder args; trailing separator means "folder" | `gtms status gtms/test/cases/my-folder` is rejected -- GTMS adds the prefix itself. A trailing `/` or `\` is NOT rejected: it is the explicit "this is a folder" signal. So `gtms delete tc-abc12345/` is treated as a folder target named `tc-abc12345/` and does NOT delete the test case `tc-abc12345`. It is not a silent no-op either: when no such folder exists the command FAILS with `✗ No test cases folder found at gtms/test/cases/tc-abc12345/.` and a NON-ZERO exit | Pass just the folder slug: `gtms status my-folder`. Same rule for create/automate/execute/gaps. Never append a separator to a TC ID -- drop it to address the test case. Do not treat a trailing-separator target as harmless in a script: it exits non-zero, so an unguarded `set -e` run aborts there. When building a target string programmatically, strip any trailing separator before appending a TC ID |
| Detail view vs folder summary | `gtms status` (no arg) -> folder summary table with `TC / CREATE / AUTOMATE / EXECUTE` columns and icons (`✓ ✗ ● ○`), fraction suffix only on non-`✓` cells (e.g. `✗ 2/6`). Priority: `✗ > ● > ○ > ✓` (worst-news-wins). CREATE always `✓`. `gtms status <folder>` -> per-TC detail table with `CREATE / AUTOMATE / EXECUTE` columns and ✓/✗/— icons. `gtms status <tc-id>` -> per-stage detail view with each stage label followed by `, <timestamp>` inside its parens (CREATE/AUTOMATE show date `YYYY-MM-DD`, EXECUTE shows `YYYY-MM-DD HH:MM UTC`; never-run stages show `—` with no timestamp). For fail/error results a `Log:` block follows with the adapter's raw output (indented two spaces; header cites spill file when truncated to 64 KB). Output parsers written for one surface will not match another. **The EXECUTE icon means different things on the two surfaces.** In the folder/dashboard table it reports whether the stage RAN, so a test whose recorded result is `fail` still shows `✓` there and the failure is carried by the LAST RESULT column. In the `gtms status <tc-id>` detail view it reports the test RESULT: `✗` for a fail, `⊘` for a skip, `✗` for a run error, `✓` only for a pass. The same failing test is therefore `✓` in the table and `✗` on its own detail card -- intended, not a bug | Match your parsing to the invocation. The summary (no folder arg) carries icons, optional fractions, and a key-footer line (`Key: ✓ = all pass ...`). Folder detail carries TC IDs, icons and `[framework]` tags. TC detail adds the timestamp suffixes -- inserted BEFORE substrings like `[bats]` and `Pass`, so a substring match still hits -- and a `Log:` block on fail/error. JSON summary adds `passing`, `failing`, `errored`, `in_flight` alongside the existing counts |
| Status single-TC `--json` is flat; bulk status is a bare array; map is always a requirement-group tree | `gtms status <tc-id> --json` emits a flat **status detail** object: TC identity is top-level `testcase`, while execution state/outcome are per framework at `frameworks[].last_status_here` / `last_result_here`. Bulk status (`<folder>` / `-r`) is a bare array of status entries. `gtms map --json` uses a different contract on every target form: the document is a requirement-group tree whose entries carry `test_case_id`, `execute_status`, and `last_result`. Single-TC map remains group-preserving: the requested TC appears inside its whole requirement group, or under `unlinked[]` when it has no requirement. None of these forms has an `{entries: [...]}` wrapper. Missing result fields are normal before first execution, so CI/agent consumers must fail closed when they require one | Use `jq -e` so a missing TC/framework/field fails instead of silently reading as success. Status single: `jq -er '.frameworks[] \| select(.framework == "bats") \| .last_status_here'`; replace the final field with `.last_result_here` for outcome. Status bulk: `jq -er '.[] \| select(.testcase == "tc-XXX") \| .frameworks[] \| select(.framework == "bats") \| .last_status_here'`. Map, any target form: `jq -er '.. \| objects \| select(.test_case_id? == "tc-XXX") \| .execute_status'` (use `.last_result` for outcome). These selectors are command-specific; do not copy one from status to map or vice versa |
| Dual-framework TCs | One TC can carry wiring for several frameworks side by side: `gtms/automation/wiring/<tc-id>--bats.wiring.yaml` AND `<tc-id>--pester.wiring.yaml`. Each framework's execute runs independently; `gtms status --framework X` selects the X wiring | Automate (or `gtms link`) once per framework to produce both wiring records. Execute each framework separately -- single-command multi-framework execute is deferred |
| BATS tool-skip blind spot | BATS TAP marks skipped tests as `ok N # skip reason`, sharing the `ok` prefix with real passes, so a skip can read as a pass. CI summaries count skips separately and warn when skip > 0. If a skip appears, investigate: a tool-gated skip (e.g. `jq not available`) hides whether the test actually works | Review the "Skipped Tests" section of the CI step summary. Either install the missing tool or rewrite the test to avoid the dependency |
| Any skip demotes a BATS file to ⊘ | A `.bats` file with one passing `@test` and one skipping `@test` classifies as a skip: any `# skip` directive without a `not ok` line demotes the whole file. Fail still wins -- any `not ok` line classifies as fail regardless of skip count. All five BATS execute wrappers (`bats-runner` plus four `remote-bats-*` variants) surface both counts in the result-contract `summary:` field (e.g. `"2 passed, 1 skipped"`) | The result contract reads `status: complete` + `result: skip`; `status: skipped` is rejected. Text views show ⊘ in both `gtms status` and `gtms map`. The JSON vocabularies differ: status exposes `frameworks[].last_status_here: "complete"` plus `last_result_here: "skipped"`; each map entry exposes `execute_status: "skipped"` plus `last_result: "skipped"` inside the surrounding traceability tree. Use the command-specific fail-closed selectors in the JSON-shape row above |
| Adapter-first dispatch | `gtms execute tc-X --framework manual` does NOT select the `manual-execute` adapter on **any** preset. The framework flag is independent of adapter resolution: dispatch keys on the resolved adapter's name or its configured framework, never on the CLI `--framework` flag as an adapter selector. A wired execute is routed by the wiring record's adapter; manual execute requires `--adapter manual-execute` or a manual execute default. To opt into manual-execute on any preset (`manual`, `bats`, `playwright`), pass `--adapter manual-execute` -- the `manual` preset already wires it as the execute default, `bats` and `playwright` do not | To confirm which adapter actually ran, read the `adapter:` field of the handoff under `.gtms/results/` rather than trusting the flag you passed |
| Manual `gtms execute` missing-result-file directs to `gtms prime` | When `gtms execute` runs against a manual-execute adapter and the result file at `gtms/manual/records/{tc-id}--manual.result.yaml` is missing, the error message names `gtms prime tc-X --framework manual` -- not the generic `gtms automate` hint. The error flows through the standard `status: error` handoff path: task lands in `gtms/tasks/error/`, pipeline records are built, no silent short-circuit | Expect the prime hint on stderr and an error task on disk. If you see the generic "Re-run gtms automate" hint instead, the adapter resolved for that test case is not manual-execute -- check `defaults.execute` in `gtms.config`, or pass `--adapter manual-execute` explicitly |
| `gtms execute` bulk exits non-zero on any failure | Bulk `gtms execute` exits non-zero if any TC fails, errors, or is a skipped-but-not-clean run, even though it processed every TC -- a UNIX-style aggregate exit code. "The command completed" is not the same as "exit 0" | Read the per-TC results rather than the exit code alone when you need to know which TC failed. Bulk exits 0 only when every TC passed or was a clean tooling skip; a single-TC execute exits 0 for pass and non-zero for fail or error |
| `gtms execute` has no `--result` / `--notes` flags | The `--result` and `--notes` flags were removed in v0.2.0; Cobra rejects them with "unknown flag" | Record a manual outcome through the prime pipeline: `gtms prime <tc-id> --framework manual` -> edit `gtms/manual/records/<tc-id>--manual.result.yaml` -> `gtms execute <tc-id> --adapter manual-execute`. Never propose `--result` or `--notes` |
| `gtms link` repoint duplicate scan depends on the target form | Repoint mode (`gtms link <tc-id> --adapter <name>`) accepts a folder-qualified target such as `feature/tc-a1b2c3d4` as well as a bare ID; the qualified form is scope-validated, not rejected. That validation is also what runs the cross-folder duplicate check, so the scan depends on how you write the target: FOLDER and RECURSIVE repoints scan every TC they touch, and a FOLDER-QUALIFIED single-TC repoint is checked too -- all three abort before any write if a targeted TC ID has more than one test case spec anywhere in the project. A BARE single-TC target and `--all` skip the scan. Repoint separately rejects the four reserved prime-path execute adapter names as targets -- execute selects those by name before consulting wiring, so a record repointed to one would never drive a run. Repoint does NOT pick a test case spec: it rewrites the flat wiring record keyed on the bare TC ID plus framework, so when duplicate test case specs share an ID they all share that one record and a repoint affects every one of them. The qualifier only decides whether the ambiguity guard runs first -- it never narrows what gets rewritten. The `.md`-suffix auto-strip applies to repoint targets: `tc-a1b2c3d4.md` resolves like `tc-a1b2c3d4` | Qualify the target (`folder/tc-XXXXXXXX`) so the duplicate check runs on a single-TC repoint. Passing the bare ID skips that check -- it does NOT limit the repoint to one test case spec, it just rewrites the shared wiring unvalidated, so only use the bare form when you already know the ID is unique. On folder, recursive, or `--all` repoints, `--from-adapter <old>` is MANDATORY. Preview bulk repoints with `--dry-run` first (works in both flag positions) |
| An unusable Tier 2 result contract is an error, never a pass | If, after a synchronous Tier 2 adapter returns, its result contract is unparseable, missing, or unreadable, GTMS writes a readable replacement handoff with `status: error` and no `result:`, files the task under `gtms/tasks/error/`, and bulk `gtms execute` counts the TC as errored, not passed. Exit policy is per command: `execute`, `create` and `prime` exit non-zero; single-target `automate` renders the error and exits 0; bulk `automate` exits non-zero. Tier 1 is unchanged. The usual cause is a `log: \|` block whose indent YAML infers from the tool's first output line | Write `log: \|2` (explicit two-space indent) fed by `sed 's/^/  /'`, fix the script, and re-run the affected TCs -- a pass recorded through an unusable contract was never verified. Check the runner for bare `log: \|` headers first; if it already writes `log: \|2` the cause is elsewhere. For a runner that `gtms init` shipped, either change its two `log: \|` lines to `log: \|2` (fixes only this defect; an older Playwright runner keeps its exit-code classification), or run `gtms init --force --preset <bats\|playwright>`, which refreshes a pristine recognised older runner in place but also rewrites `gtms.config` with no backup (copy it aside first and re-apply your customisations afterwards). An edited runner gets a `.gtms-new` companion to merge. BATS recognises the v0.3.0 to v0.3.2 runner; its v0.2.0 runner is not recognised and takes the two-line edit or the companion. See `reference/adapter-guide.md` -> **Updating the Result Contract** |
| Triage category names are values, not flags | `--automation-wrong`, `--test-wrong`, and `--app-wrong` are not registered flags. Cobra rejects them with `Error: unknown flag: --test-wrong` and writes no GTMS project state. `automation-wrong`, `test-wrong`, and `app-wrong` remain default keys accepted through `--value` | Classify with `gtms triage <tc-id> --value <key>`, for example `--value test-wrong`. Classification records the triage decision only: it queues no automate task, edits no test case spec, and appends no history. Run any follow-up separately; read the stored decision with `--show` or `--list --json` |
| Source build output | Inside an initialised checkout, `gtms/` is the data directory. Giving `go build` the bare output name `gtms` writes `gtms/gtms` (`gtms/gtms.exe` on Windows) and exits successfully | For a local checkout build, use `gtms.exe` on Windows or `gtms-linux` on Linux/macOS/WSL, or run `make build`. Installed or downloaded Unix binaries remain correctly named `gtms` |

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
