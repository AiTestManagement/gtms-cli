---
name: gtms-tests-create
description: Author test case specifications through gtms create, with you (the agent) filling each spec body. Use when new or changed behaviour needs test coverage and no test case specs exist for it yet -- the first stage of the GTMS pipeline.
---

# gtms-tests-create

> This is a GTMS starter skill. If you are reading it under `gtms/skills/`,
> it is the uninstalled template -- copy this folder into your agent's skills
> directory and adapt the copy before use.

Author test case specs under `gtms/test/cases/<folder>/`. The `agent-create`
built-in stamps one skeleton file per invocation with a GTMS-owned ID; you
author each spec body. If the project configures an AI create adapter as its
default, that is the alternative path: drop `--adapter agent-create`, let it
generate the bodies, and shift your effort to reviewing them.

## Before you start

- Run `gtms agent` and read the operating reference, especially the Mode 3
  ground rules and Workflow Sequences.
- Read any authoring guides the project keeps under `gtms/test/guides/`.
- Look at a stamped skeleton (or `gtms/test/templates/agent-testcase.template.md`
  if present) so you know which sections GTMS writes and which you fill.
  Use the stamped sections; do not invent or remove headings.

## Arguments

- **folder**: a descriptive topic slug for the test case folder (for example
  `login-lockout`, `csv-export-limits`). Name the behaviour under test, not
  a ticket number.
- **source** (optional): the requirement, change description, or context
  file the test cases cover.

## Steps

1. Read the source material end to end. Walk its acceptance criteria (or
   equivalent) and decide the test case count up front. Classify each item:
   - **Direct test case** -- one focused TC covers it (most items).
   - **Cross-cutting test case** -- a behaviour the contract implies but
     never enumerates (regression guards, interaction between features).
   - **Out of scope** -- anything that cannot be verified through the
     application's external surface belongs in the project's unit tests,
     not here.

   One test case per behaviour. If the count is running far past the source
   material's own item count, you are probably splitting too finely.
2. Before asserting on any concrete output (JSON fields, messages, file
   shapes), probe the application's actual behaviour with your own tools.
   One caution: if the work item CHANGES that output, today's behaviour is
   the thing being replaced -- derive those assertion values from the
   requirement text, and probe only the parts the change leaves alone.
3. Decide a concise kebab-case name for each test case (letters, numbers,
   dashes, underscores only), then stamp one skeleton per test case:

   ```sh
   gtms create <folder> <name> --reference <requirement-id> --adapter agent-create
   ```

   Add `--context-file <path>` when a source document exists. Capture each
   `tc-XXXXXXXX` ID from stdout. GTMS owns these IDs -- never invent, guess,
   or rename one, and never edit the frontmatter `test_case_id`.
4. If specs will reference shared test data (fixture records, sample
   inputs), pin the concrete values up front and use the same mapping in
   every spec's Test Data section. Symbolic placeholders ("the pending
   record", "some user") break automation later; concrete beats symbolic.
5. Fill each spec body against this rubric:
   - **Objective**: the behaviour under test and the requirement it covers.
   - **Preconditions**: concrete setup, end to end -- no "assume X".
   - **Test Data**: the pinned concrete values from step 4.
   - **Steps**: one atomic action per step, each with an explicit expected
     observation.
   - **Final outcome and postconditions**: what must be true, and what must
     NOT have changed.
   - Only prescribe commands and flags that actually exist -- verify against
     the application's own help before writing them into a step.
6. Verify the suite:

   ```sh
   gtms status <folder> --json
   ```

   Confirm the count matches the number of test cases you authored and
   every ID appears -- a frontmatter error drops a spec from the listing
   silently.

## Report

Summarise: files created (ID, behaviour, priority), a requirement-to-TC
mapping table (including items classified out of scope and why), any
cross-cutting test cases added, and open questions. If the source material
is unclear or contradicts itself, stop and ask -- do not paper over
ambiguity with invented assertions.

## Stop here

This skill ends at authored, verified specs. Do NOT run `gtms automate`,
`gtms prime`, or any execute flow from this skill -- those are separate
stages the user triggers. Close the report by recommending the sibling
paths: the `gtms-tests-automate` skill for a repeatable script, or the
`gtms-tests-prime` skill for a one-time recorded result.
