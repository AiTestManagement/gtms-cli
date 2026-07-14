---
name: gtms-tests-execute
description: Run wired test scripts through gtms execute and record results in the GTMS pipeline. Use when test cases have been automated and you need pass/fail results on the dashboard -- the third stage of the GTMS pipeline.
---

# gtms-tests-execute

> This is a GTMS starter skill. If you are reading it under `gtms/skills/`,
> it is the uninstalled template -- copy this folder into your agent's skills
> directory and adapt the copy before use.

Run wired test scripts with `gtms execute` so results are recorded in the
pipeline and visible on `gtms status`. Running scripts directly with the
framework's own runner is fine for quick debugging, but the recorded path is
the default -- the evidence trail is the point.

## Before you start

- Run `gtms agent` and read the operating reference, especially Result
  Interpretation (status vs result, stale detection).

## Arguments

- **target**: a `tc-XXXXXXXX` ID or a test case folder slug.
- **framework** (optional): required when a test case is wired for more than
  one framework.

## Steps

1. Confirm the target is wired: `gtms status <folder>`. If the AUTOMATE
   stage is empty, run the `gtms-tests-automate` skill first.
2. Execute a single test case:

   ```sh
   gtms execute <tc-id>
   ```

   Bare on purpose: the wiring record names the execute adapter. Do not pass
   `--adapter` on a wired execute -- a mismatched adapter is the top cause of
   false failures. The first execute after automate settles
   `artefact-hash: pending` into a real hash.

   For a folder:

   ```sh
   gtms execute <folder> --framework <framework>
   ```

   A bulk run exits non-zero if ANY test case fails or errors -- that is an
   aggregate exit code, not a tooling problem. Read the results before
   concluding anything.
3. Read the outcome on `gtms status <tc-id>`. Keep the two axes separate:
   `status` says whether the adapter ran (`error` = could not run --
   investigate the run); `result` says what the test found (`fail` = ran and
   the subject failed -- investigate the subject).
4. Diagnose failures by going back to the spec: read the spec, read the
   script, find the gap -- then classify and report, do not fix.
   - Script does not match spec: report the exact gap and the remediation
     (the `gtms-tests-verify-intent` skill for a full sweep, or a
     user-triggered fix step). Note in the report that after any script
     fix, the wiring must be refreshed with `gtms link --refresh <tc-id>`
     before re-executing.
   - Spec is wrong: flag it to the user; do not silently rewrite the spec.
   - Script matches spec and the subject fails: record the triage:

     ```sh
     gtms triage <tc-id> --app-wrong --summary "<what is broken>"
     ```

   Never re-execute blindly -- the same cause produces the same failure.
5. Verify the end state with `gtms status <folder>` and `gtms gaps <folder>`.
   Treat any stale-wiring warning as a wiring problem to resolve, never as a
   pass.

## Report

Summarise: test cases executed, pass/fail/skip counts, each skip and its
reason, each failure with its diagnosis (script / spec / subject), and
follow-ups.

## Stop here

This skill ends at recorded results and diagnosed failures. Do NOT edit
scripts, specs, or the application from this skill -- every failure is
reported with its diagnosis and remediation, and fixes are a separate step
the user triggers. Subject defects go to the user with the triage record as
evidence. Everything green: the pipeline is the proof -- point at
`gtms status`.
