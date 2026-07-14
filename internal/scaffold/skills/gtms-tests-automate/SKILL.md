---
name: gtms-tests-automate
description: Turn test case specs into runnable test scripts with wiring records via gtms automate. Use when test case specs exist and the goal is repeatable automated execution -- the second stage of the GTMS pipeline.
---

# gtms-tests-automate

> This is a GTMS starter skill. If you are reading it under `gtms/skills/`,
> it is the uninstalled template -- copy this folder into your agent's skills
> directory and adapt the copy before use.

Produce a test script plus a wiring record for each test case using
`gtms automate`. The wiring record links spec, script, and framework; it is
what makes `gtms execute` repeatable.

## Before you start

- Run `gtms agent` and read the operating reference, especially the automate
  path under Workflow Sequences.
- Run `gtms list frameworks --json` to see which frameworks this project
  supports and which adapters target each.

## Arguments

- **target**: a `tc-XXXXXXXX` ID or a test case folder slug.
- **framework**: the test framework to automate for (for example `bats`,
  `playwright`).

## Steps

1. Check what is already automated with `gtms status <folder>`. Skip test
   cases whose AUTOMATE stage is already complete unless the user explicitly
   asks to regenerate.
2. For each test case to automate:

   ```sh
   gtms automate <tc-id> --framework <framework> --adapter agent-automate
   ```

   This stamps a script skeleton and a wiring record with
   `artefact-hash: pending`. Built-in skeletons exist for `bats` and
   `playwright`; other frameworks need a configured adapter (drop
   `--adapter agent-automate` to use the project default).
3. Fill each script body so it implements its spec literally: invoke the
   same commands the spec prescribes, use the spec's test data, and check
   every expected result the spec states. The spec is the authority -- if
   the spec looks wrong, stop and flag it; do not silently diverge from it.
4. Verify: the script exists at the path the automate output names, one
   wiring record exists per test case per framework, and
   `gtms status <folder>` shows the AUTOMATE stage complete.
   `artefact-hash: pending` is normal here -- the first execute settles it.
5. If you edit a script again after it has been executed at least once,
   acknowledge the deliberate change:

   ```sh
   gtms link --refresh <tc-id> --framework <framework>
   ```

## Writing BATS: every `run` replaces `$output`, `$stderr`, `$lines` and `$status`

If the target framework is BATS, this rule breaks more generated tests than
anything else. Read it before you fill a script body.

`run` sets four result variables -- `$output`, `$stderr`, `$lines` and `$status`
-- and every later `run` replaces all four. They hold the most recent `run` only,
never an earlier one. `--separate-stderr` gives you `$stderr` as its own variable;
it does not make it survive the next `run`.

### The safe shape

```sh
run --separate-stderr my-cli build --target release
assert_success                       # $status is still THIS run's -- assert it now
local out="$output" err="$stderr"    # capture before anything else runs

run my-cli verify                    # all four variables are now overwritten
assert_success
[[ "$out" == *"build complete"* ]]   # assert against the capture, not $output
```

Order matters: assert the status, capture what you need, then run anything else.
`$status` is the one that gets forgotten, because nothing about the shape of the
test reminds you it was overwritten -- so a late `assert_success` quietly checks
the wrong command's exit code.

Capture whatever you still need before the next `run`. Shell variables are enough
for a direct assertion; write to a file only when a later command has to consume
the content. When you do snapshot, prefer `printf '%s\n' "$output" > "$f"` right
after the `run`: it keeps the captured streams available for the normal
bats-assert checks and avoids mixing shell redirection with BATS's own capture.

### Wrong -- a stale read that fails loudly

```sh
run --separate-stderr my-cli build
run grep -c 'warning' build.log      # resets all four variables
[[ "$stderr" == *"deprecated"* ]]    # reads an empty string -- FAILS
```

Irritating, but you see it.

### Wrong -- a stale read that passes vacuously

```sh
run --separate-stderr my-cli build
run grep -c 'warning' build.log      # resets all four variables
refute_output --partial "error"      # checks grep's output, not the build's
[ -z "$stderr" ]                     # passes because $stderr was wiped, not because it was clean
```

Green, and asserting nothing. This is the mode that matters. A POSITIVE assertion
against a clobbered variable (`assert_output --partial`, `[[ "$output" == *x* ]]`)
fails loudly and gets noticed. A NEGATIVE one (`refute_output`,
`assert [ -z "$output" ]`) passes vacuously and nothing reports it.

### Wrong -- re-run recovery. Never do this.

```sh
run --separate-stderr my-cli build
run my-cli verify
run my-cli build --force             # re-running to get the lost output back
assert_output --partial "compiled"   # asserts against a DIFFERENT execution -- and passes
```

Never re-run the command under test to recover a value you clobbered, and never
add a flag or change state so the second invocation "works". A re-run is a
different execution against different state: a flag like `--force`, a cleanup, or
a directory the first run already populated all send it down a different path, so
the assertion lands on behaviour the spec never described. It passes, so nothing
will tell you. Capture the first time -- do not rely on noticing.

## Report

Summarise: test cases found / already automated / newly automated / failed
to automate, generated script paths, and any spec problems flagged in
step 3.

## Stop here

This skill ends at filled scripts and wiring records. Do NOT run
`gtms execute` from this skill -- execution is a separate stage the user
triggers. Close the report by recommending the `gtms-tests-execute` skill (and
the `gtms-tests-verify-intent` skill first, when script fidelity matters).
