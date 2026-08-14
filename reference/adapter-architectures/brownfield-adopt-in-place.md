# Brownfield adoption: bringing an existing suite under GTMS by tagging in place

One entry in the adapter-architectures series. This one is not a transport
architecture -- it is the recipe for making GTMS aware of tests that already
exist, without moving, splitting, or rewriting any of them.

The worked example is a Cucumber.js suite driving Playwright: around thirty
feature files, a few hundred scenarios, an existing CI pipeline running all of
it, and a freshly initialised GTMS reporting "No test cases found." A pilot
slice of 30 scenarios was adopted. Nothing in the suite moved.

Identifying detail is placeholdered on the same convention as the rest of the
series: `<profile>` for a runner profile, `<domain>` and `<Entity>` for domain
vocabulary in examples. Test case IDs are **synthetic** -- `tc-11111111` and
friends. GTMS-generated IDs are random hex and carry no meaning of their own,
but they are durable join keys: if a spec, wiring record, run log or screenshot
from the same project ever surfaces anywhere else, a matching ID connects the
two. Concrete examples cost nothing when the concrete value is invented. Where
a file is quoted unchanged it is labelled **verbatim**; where anything has been
substituted it is labelled **sanitised**.

---

## The whole thing is three steps

In practice the integration boiled down to this:

1. **Work out how the tests run today.** The pre-existing command-line
   nomenclature: how the suite is invoked, and how a *single* test is selected.
2. **Create GTMS test cases.** Minimal specs -- title and ID. GTMS mints the IDs.
3. **Tag the existing test with its test case ID, then link it.** Add the ID to
   the test where it already lives, and point a wiring record at the file
   containing it.

Steps 2 and 3 are mechanical and scriptable; on a 30-scenario slice they took
well under an hour between them. **Step 1 is where all the real effort lives**,
and it is not GTMS work at all -- it is reading an unfamiliar codebase to answer
one question. Budget accordingly. A recipe that makes steps 2 and 3 look like
the hard part will mislead you.

---

## The rule: adopt in place, never restructure

The suite stays exactly where the framework and CI expect it. GTMS attaches two
things to what already exists -- **identity** (the injected ID) and
**registration** (the wiring record) -- and changes nothing else.

The consequences are worth stating explicitly, because one of them contradicts
GTMS's normal guidance:

- **Many test cases to one file is the norm for adopted legacy.** This is a
  deliberate exception. For new GTMS-authored work the one-test-case-one-file
  rule still holds; for adopted suites, scenarios already share files and
  splitting them would be exactly the restructuring this pattern exists to
  avoid.
- **Selection is by identity, never by file path.** Both because the artefact
  file is shared and the ID is what disambiguates within it, and because a
  framework's path semantics can betray you. See the measurement below.
- **Artefacts stay under the framework's own tree.** The existing CI keeps
  running them unchanged, for free. GTMS reads that tree; it does not own it.
- **The runner adapter is where step 1's discovery gets encoded.** Whatever
  command the team already ran, parameterised by the test case ID.

---

## Step 1: discovery

On a different stack the findings will not apply, but the order of investigation
will. The only question that must be answered to write a runner is: **what is the
one command that runs one test, and how is a single test selected?**

Everything else -- page objects, helpers, reporting -- is irrelevant to adoption.
The runner shells out to the suite's own entry point and lets the suite do its
job.

### Where the answer lives, in the order that paid off

1. **The project's own script definitions** (here, `package.json` `scripts`).
   Read this first, always: it is the project's own declaration of how it is
   run. Here it exposed the nomenclature immediately -- the real binary is
   `npx cucumber-js`, runs are selected by a profile (`-p <name>`), and several
   profiles exist.

2. **The profile definition file**, named by that flag. This told us what a
   profile *is*, and revealed the line that dictated the whole selection design:
   `paths: [ 'features/**/*.feature' ]`. The profile globs every feature file.

3. **The environment-file template** (`.env.example` or equivalent). It names
   the required variables without containing secrets. Read the example, not the
   real environment file -- it documents the contract and leaks nothing.

4. **The CI workflow.** Read to confirm the invocation in a second independent
   place, and to mine it for environment truths not visible locally. It matched,
   and additionally surfaced two environment requirements that mattered for a
   clean run though neither was part of the invocation question.

5. **The browser-launch code** -- read only once a launch actually failed, to
   find where the browser path is resolved.

### Dead ends, kept because they cost time

- **Budgeting for the hardest authentication mode first.** The profile list
  advertised specialised modes, which suggested the app's authentication would
  need special handling. Against the simplest profile it needed none. The
  profile list advertises a suite's *hardest* modes; adopt against its
  *simplest* one first.
- **Trusting a documented path.** The project's own instructions named a browser
  path that disagreed with the code, which disagreed with what was actually
  installed -- three different values. Verify against the running system, not
  the docs.
- **Nearly running the wrong thing entirely.** The scaffolded GTMS preset
  assumed a different runner than the suite actually used. Follow the project's
  own evidence, not the tool's default. Adopt what the project runs.

### The one behaviour that had to be measured, not read

Documentation and intuition both said "point the runner at the file."
Measurement disproved it. Cucumber's positional path arguments **add to** the
profile's `paths` glob; they do not replace it:

```text
npx cucumber-js -p <profile> --dry-run <one feature file>    -> every scenario in the suite
npx cucumber-js -p <profile> --dry-run --tags "@tc-XXXXXXXX" -> 1 scenario
```

A path-based runner would have run the entire suite on every single-test
execute, and passed.

`--dry-run` is the right instrument: it resolves and counts without executing,
so the behaviour is measurable in about a tenth of a second with no side
effects. **Find your framework's equivalent and measure it before designing
selection.** The general lesson transfers even though this specific gotcha is a
cucumber-js one.

---

## Step 2: the minimal spec

For an adopted test the spec is deliberately hollow: **title, ID, and a pointer
to the test.** The existing test body remains the single source of truth for
steps and expected results, and is not copied.

Sanitised -- structure verbatim; domain nouns, the test case ID and the date replaced:

```markdown
---
test_case_id: tc-11111111
title: "User can add a new external <Entity> via Form"
requirement:
priority: Medium
type: Functional
created: <date>
---

## Test Objective

Adopted legacy scenario -- spec is intentionally minimal (title + id).

Source scenario: `features/<domain>/create-<domain>.feature`, selected by tag `@tc-11111111`.
The scenario body is the authority on steps and expected results; it is not duplicated here.
```

**Nothing beyond title and ID proved necessary.** The pipeline ran end to end --
create, link, execute, result -- on that alone. The frontmatter `test_case_id`
must match the filename ID, which GTMS validates before execute, but nothing in
the body is required.

One optional addition earned its place: a `## Notes` block on known-failing
adoptions, recording the expected failure so the dashboard's red is explained
rather than mysterious. It is documentation for humans; GTMS does not read it.

> **On source pointers:** the original specs cited `file:line`. Line numbers
> drift as files change, while the injected tag does not. Prefer a tag-only
> pointer -- it ages better, and it cost a repair pass here to add line numbers
> that were then a liability.

---

## Step 3: tag, then link

### Scope first -- adopt a slice, not everything

The full suite was not adopted at once. It already carried a `@smoke` marker on
its critical-path scenarios, so that became the pilot scope: 30 scenarios, all
specs in one flat folder keyed to the marker rather than mirroring the feature
tree.

Reusing an existing marker to scope a coherent pilot gives a meaningful
dashboard quickly, without a diff across every file in the suite.

### Scan before you bulk-edit

Before touching tracked files, scan for structures that make in-place ID
injection unsafe. For Gherkin, two matter:

- **Shared setup blocks** (`Background:`) -- a naive per-scenario edit can
  disturb them.
- **Tags above the `Feature:` line**, which apply to every scenario in the file
  and would make an injected per-scenario ID ambiguous.

Worth adding: **parameterised tests** (`Scenario Outline` with `Examples:`),
where one test case ID would map to N executions rather than one.

In this suite the scoped files had none of the first two, so appending a tag was
purely additive and safe. One parameterised test existed outside the pilot scope
and was noted for later handling. Had any been present inside the scope, the
plan would have changed *before* any file was touched -- which is the entire
point of scanning first.

### Add the identity

The edit is a single append to one existing line per test. A tag line
`@<domain> @create @smoke` becomes `@<domain> @create @smoke @tc-11111111`.
Nothing moves; no file is split.

Do the first few by hand to validate the approach, then script the rest. Per
test the script did: create the test case and capture the generated ID from
stdout (**GTMS owns the ID -- never invent one**), locate the existing tag line
and append the ID in place, write the minimal spec, then link.

### Verify the IDs -- twice, and the second check is the real one

**Structural:** every injected ID occurrence is distinct. Necessary, but weak --
it proves the strings differ, nothing more.

**Behavioural:** every ID actually resolves to exactly one test. Ask the runner:

```text
npx cucumber-js -p <profile> --dry-run --tags "@tc-aaaa or @tc-bbbb or ... or @tc-nnnn"
# expect: "<N> scenarios (<N> skipped)" where N == the number of tags
```

Both were run: 30 occurrences, 30 distinct, and the dry run resolved exactly 30
scenarios. More than N means one ID is on two tests; fewer means a dead ID
pointing at nothing. The behavioural check is the one that matters, because it
proves the IDs are live selectors rather than merely unique strings.

### Link

```text
gtms link <tc-id> --framework cucumber --artefact features/<path>.feature
```

Two test cases sharing one file is the normal case, and it is written exactly as
you would expect -- the same artefact path, twice:

```text
gtms link tc-11111111 --framework cucumber --artefact features/<domain>/create-<domain>.feature
gtms link tc-22222222 --framework cucumber --artefact features/<domain>/create-<domain>.feature
```

---

## The runner adapter

It shells out to the suite's own entry point. It contains no framework
knowledge, no page objects, and no domain logic.

Sanitised -- verbatim but for the profile placeholder:

```sh
#!/bin/sh
# Tier 2 sync execute adapter, LOCAL runs.
#
# Selects ONE scenario by its @tc-XXXXXXXX tag and runs it through the same
# cucumber-js profile developers use. Selection is by tag, never by path:
# cucumber positional path args ADD to the profile's `paths` rather than
# overriding them, so passing the artefact file would run the whole suite.
#
# The artefact file may be shared by several test cases (legacy scenarios are
# adopted in place, many TCs per .feature file). The tag is what disambiguates.

set -e

cd "${GTMS_WORK_DIR}"
. gtms/adapters/lib/classify-cucumber.sh

GTMS_ADAPTER_NAME="cucumber-runner"
TAG="@${GTMS_TESTCASE}"
PROFILE="${GTMS_ENVIRONMENT:-<profile>}"

# --- Check tooling ---
if ! command -v npx >/dev/null 2>&1; then
  DIAG="npx not found -- install Node.js and run 'npm ci' in ${GTMS_WORK_DIR}."
  echo "$DIAG" >&2
  write_contract "error" "" "$DIAG" "$DIAG"
  exit 1
fi

# --- Run the single tagged scenario ---
set +e
OUTPUT=$(npx cucumber-js -p "${PROFILE}" --tags "${TAG}" 2>&1)
EXIT_CODE=$?
set -e

classify_cucumber "${OUTPUT}" "${EXIT_CODE}"
write_contract "${CUKE_STATUS}" "${CUKE_RESULT}" "${CUKE_SUMMARY}" "${OUTPUT}"

echo "${OUTPUT}"
[ "${CUKE_STATUS}" = "complete" ] && { [ "${CUKE_RESULT}" = "pass" ] || [ "${CUKE_RESULT}" = "skip" ]; } && exit 0
exit 1
```

The non-obvious parts:

- `cd "${GTMS_WORK_DIR}"` -- GTMS invokes the adapter from an unspecified working
  directory; the suite must run from its own root.
- Sourcing the shared classifier means local and remote runs decide "pass"
  identically. The remote-execution entry in this series relies on the same file.
- `GTMS_TESTCASE` is the `tc-XXXXXXXX` ID. The whole design rests on that ID also
  being a selector the native runner understands.
- The profile falls back to a default but `gtms execute --env` can override it,
  reusing the suite's existing profile mechanism rather than inventing one.
- `set +e` around the run: a **failing test is a normal outcome to be
  classified**, not an adapter crash. Only tooling failures are crashes.
- The final two lines set the adapter's own exit code -- 0 only for pass or skip,
  so bulk runs aggregate correctly.

### Config

```yaml
  execute:
    cucumber-runner:
      mode: sync
      script: gtms/adapters/cucumber-runner.sh
      framework: cucumber
      output-dir: features
      artefact-glob: "features/**/{testcase}*.feature"
```

`framework: cucumber` is **required, not cosmetic.** An execute adapter with no
`framework` field falls back to using its own *name* as the framework, so this
adapter would resolve framework `cucumber-runner` and look for wiring that does
not exist. This bites every adapter whose name differs from its framework.

`artefact-glob` is how GTMS finds the artefact file. Note it is a *file* glob
even though run-time selection is by tag: the glob locates the file, the tag
locates the test within it.

### Classification: the single most important line

The classifier is a separate file precisely so the local and remote runners
cannot drift on what "pass" means. Its load-bearing branch:

```sh
elif echo "${_tally}" | grep -qE '^0 scenarios'; then
  # Cucumber exits 0 when a tag matches nothing. Unguarded, that reports a pass
  # for a test that never ran -- the worst failure mode for a coverage tool.
  CUKE_STATUS="error"
  CUKE_SUMMARY="tag matched no scenario -- wiring points at a scenario that no longer exists"
```

Classification runs off the runner's reported tally, not its exit code, and it
keeps two things strictly apart: `status: error` means the test **could not
run** (investigate the run), `result: fail` means it ran and the subject failed
(triage it).

This branch exists because of the measurement in step 1, and it is what stops a
deleted or retagged test from being silently recorded as a pass. Every framework
has *some* version of "silently ran nothing" -- find yours before you trust a
green dashboard.

---

## Living with shared files

Many test cases per file is the norm here, and it has consequences that are
inherent to the arrangement rather than defects. Expect them.

**Many-to-one linking is accepted silently.** Linking a second, third, or
fourth test case to the same artefact just works, with no warning. That silence
is what makes adopt-in-place possible, but it does mean nothing tells you the
file is shared -- you have to know.

**The wiring records share an identical `artefact-hash`,** because the hash is
of the file. That is the mechanism behind the next point.

**Staleness is therefore collective.** Editing the shared file -- including the
act of adding the tags in the first place -- flips *every* test case wired to
that file to stale at once:

```text
TEST CASE       CREATE  AUTOMATE  EXECUTE  LAST RESULT
--------------  ------  --------  -------  -----------
tc-11110000  s  ✓       ✓         — ⚠      —
tc-22220000  s  ✓       ✓         — ⚠      —
```

One call clears the lot:

```text
$ gtms link smoke --refresh
  ✓ Refreshed: tc-11110000 (cucumber)
  ✓ Refreshed: tc-22220000 (cucumber)

  2 refreshed, 0 current, 0 failed
```

**Know what that staleness signal does and does not mean.** It means the linked
file changed. It does not mean the automation's behaviour changed, and a clean
dashboard does not mean behaviour is unchanged -- the layer where behaviour
actually lives, the step definitions and page objects the tests call into, is
not covered by the hash at all. Edit a step definition and every dashboard stays
green. Treat stale as "the file moved under me", not as a behaviour guarantee.

**Deleting one test case does not remove the shared file.** `gtms delete`
preserves referenced artefacts by default and names each one it left behind, and
`--force-artefact-deletion` is share-aware: it removes the file only when no
test case outside the deletion set still references it. So retiring an adopted
test case is just `gtms delete <tc>` -- the test itself stays in the shared file,
and you strip its injected ID when you want it gone for good.

> Earlier releases did delete the shared file, destroying the other test cases'
> tests and orphaning their wiring. If you are working from notes written
> against an older build, the workaround they describe -- always dry-run first,
> retire by hand-editing tags and wiring records -- is no longer necessary.

**A moved scenario passes while its wiring quietly goes wrong.** This is the
one consequence worth understanding before adopting at scale, because it is
structural rather than incidental: selection is by **tag**, registration is by
**file path**, and the two can drift apart.

Deletion is handled -- the tag stops resolving and the classifier reports
`tag matched no scenario`, which is the loud failure it was built for. A *move*
is not. Reorganise the suite, split a large feature file, and the tag travels
with the scenario: the run still selects it, still executes it, still passes.
Only the wiring record is wrong, and nothing checks it, because the runner
never consults the artefact path in the first place.

The result is a green dashboard over a registration that no longer describes
reality -- until something does read the path (`gtms delete`'s share-awareness,
the artefact hash, a tool that opens the file) and behaves oddly for reasons
that lead nowhere near the actual cause.

The premise of adopt-in-place is that the existing suite keeps evolving on its
own terms, so this will happen eventually. Worth deciding in advance whether
anything re-checks that each wiring record still points at the file containing
its tag, and worth adding to the team's mental model that a passing adopted
test is not evidence its wiring is current.

---

## What transfers, and what is framework detail

**The pattern, reusable on any stack:**

- **Minimal spec; the existing test stays authoritative.** Never transcribe.
- **One test case per test unit, identified by an injected unique ID that is
  also the runtime selector.** The ID must double as something the native runner
  can select on. This is the load-bearing constraint of the whole pattern.
- **Adopt a pre-marked slice first.** Reuse an existing marker to scope a
  coherent pilot instead of a big-bang.
- **Structural pre-scan before bulk-editing**, to find constructs that make
  in-place ID injection unsafe.
- **Verify uniqueness structurally and behaviourally** -- distinct strings is
  the weak check; "each ID resolves to exactly one test" is the real one.
- **Select by identity, never by file path.**
- **Classify "could not run" apart from "ran and failed", off structured output
  rather than the process exit code.**
- **File-level artefact hashing makes staleness collective** wherever units
  share a file.

**Framework detail -- substitute your own:**

- The selector is a Gherkin `@tag` passed as `--tags`. Elsewhere it might be a
  test-name filter, an annotation, or a file-and-line reference.
- "Positional paths add to the profile glob" is a cucumber-js behaviour. The
  general lesson -- measure how your runner scopes a single test -- transfers;
  this specific trap does not.
- "Exits 0 when the selector matches nothing" is cucumber-js. The shape differs
  per framework; the failure mode does not.
- Scraping the human-readable tally line is specific to the configured
  formatter. See below.

---

## What I would do differently

- **Classify off the structured report from day one.** This suite already
  emitted a JSON report. Parsing that instead of scraping the human tally would
  drop the formatter dependency and would additionally distinguish a failed
  setup step from a failed assertion -- a distinction the tally cannot make.
  This is the recommended evolution of the pattern.
- **Make the adoption script idempotent.** Guard against double-tagging a test
  and against re-creating a spec that already exists, so a re-run after a
  partial failure is safe rather than duplicative.
- **Test the tagging regex on one file before the batch.** The first scripted
  run here adopted zero files -- a regex anchored with `$` that never matched
  CRLF line endings -- and it did so silently, writing nothing. Five seconds on
  a single file would have caught it.
- **Write generated files ASCII-only, or with a BOM.** PowerShell 5.1 reads a
  BOM-less script as ANSI, which produced mojibake in the generated specs and
  cost a repair pass. The remote-execution entry in this series covers this and
  the related stderr trap in more detail.
- **Do not wrap native tool calls in `$ErrorActionPreference = 'Stop'`.** It
  turns a native tool's stderr into a terminating error even on success. Use the
  exit code as the success signal.
- **Capture the pointer you actually want in the first pass** -- and consider
  whether a `file:line` pointer is worth having at all, given the injected tag
  is stable and line numbers are not.

The mechanical adoption of 30 tests ran well under an hour. Rework added roughly
another half hour on top, and every item of it was a scripting or tooling
mistake rather than GTMS friction. That ratio is the honest headline: the
pattern is cheap, and what costs you is the shell script you wrote at speed.
