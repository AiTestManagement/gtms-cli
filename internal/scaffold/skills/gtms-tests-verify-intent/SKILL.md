---
name: gtms-tests-verify-intent
description: Verify that automated test scripts faithfully implement their test case specs. Use after automation scripts are filled in or edited, and before trusting execution results -- it catches scripts that quietly diverge from what the spec prescribes.
---

# gtms-tests-verify-intent

> This is a GTMS starter skill. If you are reading it under `gtms/skills/`,
> it is the uninstalled template -- copy this folder into your agent's skills
> directory and adapt the copy before use.

Compare each test case spec against the script that claims to implement it.
The spec is the authority. This skill decides whether the script executes
what the spec prescribes -- it does NOT decide whether the spec itself is
correct. If the spec looks wrong, report that separately; never rewrite
either side to make them agree.

## Arguments

- **folder**: the test case folder slug to verify.
- **mode** (optional): `summary` (one line per test case) or `detail`
  (step-by-step table per test case). Default `summary`.

## Steps

1. Pair specs with scripts. Specs live in `gtms/test/cases/<folder>/`; find
   each test case's wired script via the artefact path on:

   ```sh
   gtms status <tc-id>
   ```

   Report unmatched items as warnings: a spec with no script, or a script
   with no spec.
2. **Command-level literal match (mandatory, checked first).** Every product
   command the spec prescribes must appear in the script identically: same
   subcommand, same positional arguments, same flags and values (flag order
   aside). ANY divergence fails the pair. No "minor deviation" carve-out, no
   "intent preserved" rationalisation -- surface the divergence, do not
   adjudicate it.
3. **Assertion-level intent match (only if step 2 passes).** Check that the
   script sets up the spec's preconditions, uses the spec's test data,
   performs each numbered step, checks each step's expected result, and
   verifies the final outcome. Assertion FORM may differ freely (partial
   match, regex, exact compare) -- flag gaps in WHAT is asserted, not HOW.
4. **Verdict per pair: strictly PASS or FAIL.** If you are tempted to write
   "minor", "warning", or "still validates equivalent behaviour", the
   verdict is FAIL with the gap listed as the reason. Do not construct an
   unsourced explanation for why a gap is acceptable -- you are comparing
   two files, not judging the product.

## Report

- Summary mode: one `PASS`/`FAIL` line per test case with a brief gap
  description on failures, then a tally (`n/m verified, x warnings`).
- Detail mode: per test case, a step-by-step table (spec step, implemented
  yes/no, notes) plus preconditions / test data / final outcome checks.
- For every FAIL: precise remediation -- the exact command or assertion to
  add or correct, written so it can be handed straight to a fix step.

## Stop here

This skill ends at verdicts and remediation notes. Do NOT edit scripts or
specs from this skill -- report the failures and hand the remediation notes
to the user (or to a fix step the user triggers). Recommend re-running this
skill after fixes, before trusting `gtms-tests-execute` results.
