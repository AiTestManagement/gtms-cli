---
name: gtms-tests-prime
description: Stamp and fill a one-time test result record through the GTMS prime pipeline, then have gtms execute record it formally. Use when you or the user exercise the feature directly and the outcome needs to be on the dashboard without writing an automation script.
---

# gtms-tests-prime

> This is a GTMS starter skill. If you are reading it under `gtms/skills/`,
> it is the uninstalled template -- copy this folder into your agent's skills
> directory and adapt the copy before use.

The prime pipeline separates three roles. `gtms prime` stamps a result
record with an empty `result:` field -- it does NOT record anything. You (or
the user) exercise the feature and fill the record. `gtms execute` then
validates the filled record and formally records the result in the
pipeline. No script, no wiring record. Prime and automate are siblings, not
stages -- a test case can be primed now and automated later.

## Before you start

- Run `gtms agent` and read the operating reference, especially the prime
  path under Workflow Sequences.

## Arguments

- **tc-id**: the `tc-XXXXXXXX` ID of a test case spec that already exists
  (run the `gtms-tests-create` skill first if not).

## Steps

1. Stamp the result template:

   ```sh
   gtms prime <tc-id> --adapter agent-prime
   ```

   This writes `gtms/manual/records/<tc-id>--manual.result.yaml` with an
   empty `result:` field. Nothing is recorded in the pipeline yet.
2. Exercise the feature by following the spec's steps with your own tools.
   Actually perform the steps -- never record a result you did not observe.
3. Fill the stamped file: set `result:` to `pass`, `fail`, or `skip`, and
   record what you observed in the fields the template offers. Do not edit
   the GTMS-managed fields (hashes, identifiers).
4. Record it formally in the pipeline:

   ```sh
   gtms execute <tc-id> --adapter agent-execute
   ```

   This is the recording step: execute validates the filled record and the
   result lands on the dashboard.

5. Verify with `gtms status <tc-id>`.

## Notes

- Re-priming the same test case needs `--force` and stamps a fresh, empty
  form -- the previous content is discarded.
- If only the spec changed and the recorded result is still valid, run
  `gtms prime <tc-id> --update-hash` instead: it refreshes the hash, keeps
  the result, and needs no re-execute.

## Report

Summarise: the test case, the observed behaviour, the recorded result, and
anything ambiguous in the spec that the user should confirm before trusting
the outcome.

## Stop here

This skill ends at the recorded result. A `fail` result goes to the user
with the observations as evidence -- do not start fixing the application
from this skill.
