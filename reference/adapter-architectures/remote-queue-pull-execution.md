# Remote execution by shared-drive queue (pull model)

One entry in the adapter-architectures series: worked accounts of how real
GTMS deployments were shaped. Each entry records a whole architecture --
including the alternatives that were rejected and why -- so the reasoning
survives and does not have to be rediscovered. The adapter guide
(`reference/adapter-guide.md`) remains the contract reference for config
keys, environment variables, and result shapes; these documents are the
narratives.

This one describes how a test case authored on a developer machine is
executed on a remote Windows workstation, and its result recorded back in
GTMS, in an estate where every inbound path to that workstation is closed.

Machine names are written as `<BOX-A>` and `<BOX-B>` throughout; substitute the
real workstation labels. The shared drive is written as `R:`, standing for an
organisation-provided shared filesystem. The Cucumber profile is written as
`<profile>`.

---

## What transfers, and what is Cucumber detail

The deployment ran Cucumber, so the examples are Cucumber-flavoured. The
architecture is not.

**The pattern -- reusable with any framework:**

- One queue directory per remote machine, on a shared filesystem.
- Claim-by-rename, so two agents cannot claim the same job concurrently.
  Note this is at-least-once delivery, not exactly-once -- see below.
- A heartbeat file checked before dispatch, so a stopped agent fails fast.
- The job carries the artefact content; the machine supplies the harness,
  pinned by commit sha.
- A single classifier file shared by the local and remote runners.
- A thin per-machine wrapper over one shared remote runner body.

**Framework detail -- replace with your own equivalents:**

- Selecting the scenario by tag (`--tags @tc-XXXXXXXX`).
- Parsing Cucumber's trailing tally line to classify the outcome.
- The specific environment variables the harness needs.

### The two lessons that transfer regardless

**1. Agent = transport and execution. Adapter = meaning.** The remote agent
runs a command and returns raw stdout plus an exit code. It makes no
judgement. Every pass/fail/skip decision happens on the dispatching machine,
in a file the local runner also uses. Full treatment below.

**2. Never classify on the exit code alone.** The runner under this harness
exits 0 when the tag matches nothing, so an exit-code classifier records a
test that never ran as a pass -- the worst possible failure for a coverage
tool, and it fires exactly when someone deletes or retags a scenario. Trap
the empty-selection case explicitly, and verify the trap end to end on the
remote path rather than assuming it. Full treatment below.

---

## What it does

```
gtms execute tc-XXXXXXXX --adapter remote-<box>
```

Runs exactly one Cucumber scenario on a remote workstation and records the
outcome locally, as if it had run on the developer machine.

The developer keeps the whole authoring loop local -- `gtms create`, tagging the
scenario, `gtms link` -- and only *execution* moves. Uncommitted scenarios run
remotely without being pushed first.

---

## Why this shape

Two more obvious designs were tried and rejected, both for reasons worth
recording so they are not revisited.

### Not GitHub Actions

A self-hosted runner is an available CI option, and it works -- but **a workflow
can only run pushed commits.**

GTMS's operating model is author-locally-then-validate. Putting GitHub in the
middle forces a commit and a push between writing a scenario and finding out
whether it works. That does not merely add latency; it breaks the loop, and it
invites validating something other than what was just written. It also brings
dispatch/run correlation, artefact upload/download, and a second place for
classification logic to live.

The one thing Actions is genuinely good at here -- run history and audit trail --
is the job GTMS already does.

### Not SSH or WinRM

Direct push to the box would have been simpler. The environment does not allow
it. The available remote-management protocols were either unreachable from the
developer machine or unavailable to the developer account, and no elevation was
available to change that. Every inbound path is closed.

Worth checking properly before designing around it: "we cannot reach the box"
and "we can reach the box but may not authenticate to it" look identical from
the outside and rule out the same designs, so establishing which one you face
costs little and settles the question.

### So: invert it

The dispatcher **drops a job on a shared drive**; an agent running on the box
picks it up. Nothing connects inbound.

This turned out to be better than the push model, not merely the only one
available:

**The agent runs as a normal process in a logged-on desktop session, so a headed
browser inherits a desktop for free.** SSH and WinRM both land in session 0,
which has no desktop -- a headed Chrome launched there has nowhere to draw. CI
systems hitting this wall may require additional interactive-session machinery
to work around it. The pull model never has the problem.

---

## Architecture

```
DEVELOPER MACHINE                 R: (shared)              REMOTE BOX
                                                           agent.ps1
gtms execute --adapter remote-<box>                        (interactive session)
   |                                                            |
   |-- heartbeat pre-flight ---->  <box>/agent.alive  ----------->  (touched ~1/s)
   |                                                            |
   |-- write job -------------->  <box>/inbox/  --------------->  claim (rename)
   |                                                            |    |
   |                                                            |    +- git checkout <sha>
   |        poll every 2s                                       |    +- write .feature from job
   |                                                            |    +- npx cucumber-js --tags @tc-X
   |                                                            |    +- restore .feature
   |<-- read result ------------  <box>/out/  <------------------  write result
   |
   +-- classify -> GTMS handoff contract
```

One queue directory per box, so one adapter targets one machine:

```
R:\gtms\queue\<BOX>\
    inbox\      jobs waiting
    work\       claimed, in flight
    out\        results
    done\       retired jobs
    agent.alive heartbeat
```

---

## The division of responsibility

> **Agent = transport and execution. Adapter = meaning.**

The agent runs a command and hands back raw stdout plus an exit code. It makes
no judgement about what happened.

All classification -- pass / fail / skip, the empty-tag trap, tally parsing --
happens on the dispatching machine, in `gtms/adapters/lib/classify-cucumber.sh`,
**the same file the local runner uses**. A local pass and a remote pass are
therefore the same thing by construction rather than by agreement between two
implementations that would drift.

A practical consequence: the agent almost never needs changing. New adapters,
new classification rules, and new result vocabulary all land locally. This
matters because updating the agent means asking someone to restart it.

---

## Components

| file | role |
|---|---|
| `gtms/adapters/lib/classify-cucumber.sh` | the single classifier + contract writer, shared local/remote |
| `gtms/adapters/cucumber-runner.sh` | local execute adapter |
| `gtms/adapters/remote-cucumber-runner.sh` | shared remote body: pre-flight, dispatch, poll, classify |
| `gtms/adapters/remote-<box>.sh` | 5-line per-box wrapper |
| `gtms/agent/agent.ps1` | the box-side poller |

### Sketch: the shared classifier

The classifier is the piece worth getting right, because it is where the
"adapter = meaning" rule actually lives. In shape:

- **One file, used by both runners.** The local adapter sources it after
  running the framework directly; the remote adapter sources it after pulling
  stdout back off the share. Neither has its own copy of the rules.
- **It takes raw framework output and an exit code, and produces the GTMS
  verdict** -- the status/result pair plus the handoff contract. Everything in
  the tally table above belongs to it, including the empty-selection trap.
- **It knows nothing about transport.** It cannot tell whether the output it
  is reading came from a local process or a file on a shared drive, and
  nothing in it should want to. That ignorance is what makes a local pass and
  a remote pass the same thing rather than two implementations that agree
  today.

The corollary is a rule about where changes go: a new result vocabulary, a
new trap, a framework that reports differently -- all of that is an edit to
this one file. If a change seems to need the agent's help, that is a signal
the boundary is being crossed in the wrong direction.

One thing to know going in: the tally it parses is human-readable output, and
human-readable output is not a stable interface. A framework upgrade can
reword it, and the failure mode is quiet -- an unrecognised tally falls to
whatever the classifier does when it matches nothing. Pin the framework
version, keep a fixture per tally form the classifier claims to handle so an
upgrade breaks a test rather than a dashboard, and if the framework offers a
machine-readable formatter, prefer it and record why if you do not.

GTMS ships its own version of this shape for BATS
(`adapters/lib/bats-tap.sh`) if a concrete starting point is useful; the
Cucumber one differs mainly in also writing the contract rather than only
returning a verdict.

### Per-box wrapper

The only difference between remote adapters is which box they target:

```sh
#!/bin/sh
GTMS_REMOTE_BOX="<BOX-A>"
GTMS_ADAPTER_NAME="remote-<box>"
export GTMS_REMOTE_BOX GTMS_ADAPTER_NAME
exec sh "${GTMS_WORK_DIR}/gtms/adapters/remote-cucumber-runner.sh"
```

Wrappers are used rather than adapter config fields because passing arbitrary
config into an adapter's environment is not a documented GTMS behaviour, and a
wrapper is obvious.

### gtms.config

```yaml
  execute:
    remote-<box>:
      mode: sync
      script: gtms/adapters/remote-<box>.sh
      framework: cucumber        # REQUIRED -- see note below
      output-dir: features
      artefact-glob: "features/**/{testcase}*.feature"
```

`framework: cucumber` is not optional. An adapter with no `framework` falls back
to using its own **name** as the framework, so `remote-<box>` would resolve
framework `remote-<box>` and look for wiring that does not exist.

Because these adapters share the `cucumber` framework with the wiring records,
`gtms execute <tc> --adapter remote-<box>` is a legal one-run override. A
cross-framework override would be rejected.

---

## The job contract

Written to `inbox/<task-id>.json` under a temp name, then renamed, so the agent
never reads a half-written file.

```json
{
  "task":     "task-XXXXXXXX",
  "testcase": "tc-XXXXXXXX",
  "profile":  "<profile>",
  "sha":      "<git HEAD on the dispatcher>",
  "artefact": "features/<path>.feature",
  "content":  "<base64 of the .feature file>",
  "created":  "<ISO timestamp>"
}
```

### Why the scenario travels as content

**The job carries the scenario; the box supplies the harness.**

- `content` is the `.feature` file as it exists on the developer machine, committed
  or not. The agent writes it over the checkout before running.
- `sha` pins the `.ts` layer -- step definitions, page objects, form helpers -- to a
  pushed commit.

This mirrors the project's authoring model: scenarios are GTMS's artefact and
change constantly; step definitions are shared infrastructure reviewed as code.
It is also what makes remote execution usable without a push per iteration.

The branch itself need not be pushed -- only the commit must be reachable, e.g.
because it is an ancestor of some pushed branch.

### The result contract

`out/<task-id>.json`:

```json
{ "task": "...", "testcase": "...", "host": "<BOX>", "sha_run": "...",
  "exit": 0, "stdout": "<full cucumber output>",
  "started": "...", "finished": "...", "duration_s": 106 }
```

`sha_run` is what the box *actually* ran, read back after checkout -- not what was
requested. The adapter records it in the summary so provenance is observed
rather than assumed.

The same scepticism is worth extending to the rest of the result. The
dispatcher finds the file by name, which tells it only that *something* wrote
to the path it was watching; the body still claims a task, a testcase, a host
and a sha. Checking those against what was asked for is cheap, and it is the
difference between recording a result and recording *this* result -- the same
reasoning that puts the task id in the filename rather than trusting the JSON.

Both contracts are also an interface between two components that update on
very different schedules. The dispatcher changes whenever an adapter does; the
agent, as noted above, is deliberately left alone for long stretches because
updating it means asking someone to restart it. That gap is exactly where a
silently changed field shape does its damage, so some way for each side to
recognise a payload it does not understand -- and say so, rather than
half-process it -- pays for itself the first time the two drift.

---

## Selection is by tag, never by path

The runner executes:

```sh
npx cucumber-js -p "$PROFILE" --tags "@$GTMS_TESTCASE"
```

Not the artefact path. **Cucumber positional path arguments add to the profile's
`paths`; they do not override them.** Measured:

| invocation | scenarios loaded |
|---|---|
| `-p <profile> --dry-run <one feature file>` | 266 -- the whole suite |
| `-p <profile> --dry-run --tags "@tc-XXXXXXXX"` | 1 |

A path-based runner would execute the entire suite on every `gtms execute`. The
tag is also what disambiguates when several test cases share one `.feature` file,
which is the norm for scenarios adopted in place.

---

## Result classification

Two orthogonal fields, kept strictly apart:

| field | meaning |
|---|---|
| `status: error` | the test **could not run** -- investigate the run |
| `result: fail` | it ran and the subject failed -- triage it |

Classification is driven by Cucumber's trailing tally line, not the exit code:

| tally | contract |
|---|---|
| `0 scenarios` | `status: error` -- tag matched nothing |
| contains `undefined` / `ambiguous` | `status: error` -- steps missing |
| contains `failed` | `complete` + `fail` |
| contains `skipped` | `complete` + `skip` |
| contains `passed` | `complete` + `pass` |
| no tally at all | `status: error` |

### The trap this exists to prevent

**Cucumber exits 0 when a tag matches nothing.** Classify on the exit code and a
test that never ran is recorded as a pass -- the worst possible failure for a
coverage tool, and it fires precisely when someone deletes or retags a scenario.
The `0 scenarios` case is trapped explicitly, and it is verified end to end on
the remote path, not assumed.

Two more places the same principle applies:

- **A dispatch timeout is `error`, never `fail`.** No result means the test could
  not run; it must not be recorded as the product being broken.
- **A missing agent is `error`, refused up front.** See below.

### Accepted: a failed `Given` reports as `fail`

When an API setup step fails, the scenario reports `1 scenario (1 failed)` with
the remaining steps skipped, and GTMS records `fail`. A test step genuinely
failed, so this is the honest report; `error` is reserved for the tooling being
unable to run at all. This is a decision, not an oversight.

---

## Liveness: the heartbeat

The agent writes `agent.alive` every poll. The adapter checks its age **before
dispatching** and refuses if it is missing or older than 60s.

Without this, a stopped agent is indistinguishable from a slow test: the job sits
in the inbox and the dispatcher waits out the full timeout for a result nobody
will ever write. With it, the failure is immediate and names the cause.

Two implementation details are load-bearing:

- The heartbeat is written **in place, never renamed**. A delete-then-rename
  leaves a window in which the file does not exist, and a reader landing in that
  window declares a healthy agent dead.
- The mtime is read **defensively**. A naive `stat ... || echo 0` converts a
  transient read miss into "age = the entire unix epoch", reporting a healthy
  agent as stale.
- **Whose clock decides the age is a real question.** The file is written by
  one machine and its freshness judged on another. If that comparison ends up
  measuring a remote timestamp against a local clock, skew between the two
  becomes indistinguishable from staleness -- and it fails asymmetrically,
  since a box running slightly behind looks permanently dead while one running
  ahead looks alive after it has stopped. Prefer a comparison the shared
  filesystem itself arbitrates, or size the threshold to swallow the skew the
  estate actually has.

---

## The agent

Run it **interactively**, in a logged-on desktop session:

```powershell
powershell -ExecutionPolicy Bypass -File R:\gtms\agent.ps1 -RepoPath <path to the test repo>
```

It auto-corrects a `-RepoPath` pointing at the repo root, and refuses to start if
`node_modules` is missing.

### Do NOT install it as a Windows Service

Services run in session 0, which has no desktop. A headed browser launched from
one fails. Running as a normal process in an interactive session is the entire
reason this design avoids the interactive-logon machinery the CI pipeline needs.

If it should survive a reconnect, use a **user-level** scheduled task triggered at
logon -- no admin required, and still in-session. Still not a service.

### Loop

```
every ~1s:
  write agent.alive                      (in place)
  claim inbox\*.json -> work\            (rename; no concurrent double-claim)
  read job, validate required fields     (retry ~3s; the share holds a brief lock)
  git fetch && git checkout <sha>
  write .feature from job content
  npx cucumber-js -p <profile> --tags @<tc>
  git checkout -- <artefact>             (leave the tree clean)
  write out\<task-id>.json               (temp name, then rename)
  move job -> done\
```

**The task id comes from the job's filename, never from its JSON.** The dispatcher
polls for `<task-id>.json`; deriving the id from a file that failed to parse would
write the result where nobody is waiting, turning an immediate error into a
timeout minutes later.

---

## Box prerequisites

Only two of these are inherent to the architecture: the box needs access to the
shared filesystem, and it needs a logged-on desktop session that stays alive.
Everything else depends on your harness and your environment.

The rest of this section is a worked example rather than a checklist -- one
harness's prerequisites, included because the *symptoms* of missing each one are
unhelpful enough to be worth recognising. Substitute your own.

1. A clone of the repo, with dependencies installed
2. An environment file for the profile, holding whatever credentials the harness
   needs, plus any of these your environment happens to require:
   - `CHROMIUM_PATH` -- a browser at a path your policy permits
   - `NODE_EXTRA_CA_CERTS` -- a root CA bundle
3. Playwright's Chromium installed at a permitted location:
   ```powershell
   $env:PLAYWRIGHT_BROWSERS_PATH="<permitted-path>"; npx playwright install chromium
   ```
4. Access to the shared drive
5. A logged-on desktop session that stays alive

On the environment file: if dotenv reports `injecting env (0)`, the file is empty
or missing, and the run will fall through to whatever browser path the support
hooks hardcode and fail with "executable doesn't exist".

On `NODE_EXTRA_CA_CERTS`: where an environment terminates TLS with its own CA,
note that Node does not use the Windows certificate store. Without it,
API-seeded scenarios fail with `unable to verify the first certificate` even
though the browser and PowerShell reach the same host fine.

On the browser path: application-control policy, where present, may deny
execution of binaries under the user profile, which is Playwright's default
install location. Installing under a permitted path is then required, not
cosmetic -- the symptom is `browserType.launch: spawn UNKNOWN`, which names
nothing useful.

---

## Operating the queue

Three things the design assumes but does not enforce. Worth deciding
deliberately rather than discovering.

### The queue is a trust boundary

Anything that can write to `inbox/` can make the agent check out a commit of
its choosing and run a scenario of its choosing -- in a logged-on desktop
session, with whatever credentials that session and the `.env` file carry.
That is not a flaw in the design; it is what a pull model *is*. But it means
the share's write permissions are the whole access control story, and they
should be set as narrowly as the dispatching accounts allow rather than
inherited from whatever the parent directory happened to have.

Read access matters too, for a different reason: `done/` accumulates the
content of every artefact ever dispatched (see Hazards).

**The job payload is untrusted input even when the writer is trusted.** Three
of its fields leave the JSON and become behaviour, so each is worth treating
as input rather than instruction:

- `sha` becomes a checkout. Unconstrained, that is "run any commit this box can
  reach", including one that was never reviewed.
- `artefact` becomes a file write inside the repository. A path that escapes
  the repo -- absolute, or climbing out with `..` -- writes wherever the agent
  can reach.
- `profile` becomes part of a command line.

The same applies to the task and testcase ids, which end up in filenames. The
principle is one line: anything from the job that reaches a path or a command
should be confined or chosen from a known set before it gets there, not
after. A corrupted or hostile job should fail loudly at validation rather than
part-way through a checkout.

**Captured stdout can carry secrets.** The result contract returns the full
framework output, and that lands in `out/` and then `done/` on a share, where
it stays. A scenario that fails while logging a request header, or a harness
that echoes its environment on error, writes credentials into a directory with
broader read access than the `.env` file they came from. Decide what gets
redacted before capture, and treat the queue's read permissions as protecting
test data, not just job metadata.

### Delivery is at-least-once, not exactly-once

Claim-by-rename does one job well: two agents cannot take the same file at the
same time, provided the rename is atomic. That requires the temp-write and the
rename to happen **within the same directory on the same filesystem** -- a move
across filesystems is a copy plus a delete, and loses the property the whole
mechanism rests on.

What it does not give you is exactly-once execution. An agent can claim a job,
run the test, and die before publishing the result. Any recovery that returns
that job to the queue runs the test a second time. Since scenarios here create
real records in a shared environment, a re-run is not free -- it is a second
set of side effects.

Treat this as at-least-once and decide the policy deliberately. The questions
worth answering before the first duplicate surprises someone:

- Are abandoned jobs in `work/` retried, or parked for a human?
- What happens to a result that arrives after the dispatcher gave up -- ignored,
  or reconciled?
- Can a task id repeat, and does an existing result file get overwritten or
  refused?
- Who cleans `work/`, `out/` and `done/`, and when?

None of these needs a sophisticated answer. They need a chosen one, written
down, because the default answers are whatever the filesystem happens to do.

### Things die mid-flight, on both sides

A dispatcher that dies leaves a stale local task -- covered under Diagnosing
failures.

An agent that dies mid-job is the more disruptive case, because it leaves two
things behind. The job sits in `work/` where nothing will reclaim it, and the
checkout is left dirty, because the agent writes the scenario over the working
tree and only restores it after the run. The next job on that box then fails
its checkout, and keeps failing, until someone cleans the tree by hand. One
interrupted run wedges the machine rather than one test case.

Worth deciding up front: whether the agent sweeps `work/` back to `inbox/` on
startup, whether it restores the tree defensively before claiming rather than
only after running, and whether a job that has been claimed for implausibly
long should be surfaced somewhere the dispatcher can see. None of that is
needed to make the pattern work, and all of it is cheaper to add than to
diagnose.

**Restoration belongs on every exit path, not the happy one.** In the loop
above it reads as the step after the run, which is where it happens when
nothing goes wrong. A crash, a malformed result, an interrupt or a hung
browser all skip it. Whatever the language calls a `finally`, that is where
the restore goes -- and the scope is worth stating explicitly, because
"restore the tree" can mean several things: the supplied artefact, the
original HEAD or branch, files the test itself generated, and any process
still running when the run was abandoned. Killing the test command is not the
same as killing what it spawned; an orphaned browser holds a profile
directory and can fail the next run for reasons that look nothing like the
cause.

**One agent per box queue.** Nothing in the design enforces it, and two agents
on the same box fail in a way that reads as anything but a duplicate: both
write the same heartbeat, so both look healthy; claim-by-rename stops them
taking the same job, so neither errors; and then both check out shas and write
scenarios over the same working tree. The pre-flight cannot detect this,
because the heartbeat says exactly what it always said.

### The dispatch timeout is a real decision

Two of the three timing values here are properties of the design and can be
copied: the agent's poll interval, and the heartbeat staleness threshold that
the pre-flight checks against.

The dispatch timeout is not. It has to clear the slowest scenario in the suite
with room to spare, and a browser-driven scenario against a shared environment
is not reliably fast -- the sample result above took a shade under two
minutes. Set it too low and healthy runs are recorded as `error`; set it too
high and a genuinely dead job holds the shell for as long as the timeout. The
heartbeat is what makes a generous timeout tolerable, because the common
failure -- no agent -- is caught before dispatch rather than by waiting.

---

## Diagnosing failures

| symptom | cause |
|---|---|
| `no agent heartbeat` / `heartbeat is stale` | agent not running on that box |
| `queue not usable at ...` | shared drive not mapped in the dispatching session |
| `tag matched no scenario` | the `@tc-` tag is missing from the feature file |
| `timed out after Ns` | agent alive but the job never completed; check its console |
| `git checkout <sha> failed [this box has uncommitted changes]` | dirty tree on the box |
| `git checkout <sha> failed [commit not found ...]` | the commit was never pushed |
| `injecting env (0)` in the log | `.env.<profile>` missing/empty on the box |
| `executable doesn't exist at ...` | `CHROMIUM_PATH` unset or browser not installed |
| `unable to verify the first certificate` | `NODE_EXTRA_CA_CERTS` unset |

A dispatcher that dies mid-flight leaves a stale task in
`gtms/tasks/in-progress/`, which blocks the next execute for that test case. Delete
that file -- `gtms reset` would also wipe the result history.

---

## Sharing one feature file across test cases

Several test cases sharing a single `.feature` file is the norm here: scenarios
are adopted in place and tagged, rather than written one file per test case. Two
consequences worth knowing.

**Selection must be by tag, not by file.** Covered above -- it is the tag that
picks one scenario out of a shared file.

**Deleting one test case does not remove the shared file.** `gtms delete`
preserves referenced test artefacts by default and names each one it left
behind:

```text
Deleted 2 file(s) for 1 test case(s):
  Test case specs:     1
  Automation records:  1

Preserved referenced artefacts (not deleted):
  features/<path>.feature
```

`--force-artefact-deletion` opts into removing the artefact, and it is
share-aware: it deletes only when no test case outside the deletion set still
references that file. That check reads each record's declared `testcase:`
identity, so a same-ID test case in another folder still protects the file. So retiring an adopted test case is just
`gtms delete <tc>`; the scenario stays in the shared file, and you remove its
`@tc-` tag when you want it gone. `--dry-run` previews either form.

> Earlier releases did delete the shared artefact and orphan the other test
> cases' wiring. If you are working from notes written against a build before
> that was fixed, the workaround they describe -- untag by hand and delete the
> wiring record yourself -- is no longer necessary.

---

## Hazards

### The queue is an accidental archive

`R:\gtms\queue\<BOX>\done\*.json` holds the base64 `content` of every artefact ever
dispatched -- including uncommitted work that exists nowhere else. It may therefore
serve as an unintended recovery source for a lost artefact, and a better one than
git for this particular case: git restores only the committed version of a file,
silently losing uncommitted tags.

Both halves matter. It is genuinely useful, and it is also an unmanaged copy of
work you may not realise is being retained.

Retention is the smaller half of the problem. The larger half is that this is
an accumulating store of test data and captured output sitting on a share whose
read permissions were probably set for "a queue directory", not for "an archive
of everything the suite has ever exercised, including whatever the output
happened to contain". Size the access control to the second description, and
decide the retention policy alongside it rather than after the directory gets
large enough to notice.

---

## Environment quirks encountered

These are properties of the estate this was built in, not GTMS requirements.
They cost real time and produce misleading symptoms.

| quirk | consequence |
|---|---|
| `mkdir -p` fails on the DFS UNC root (`Too many levels of symbolic links`) although read/write both work | use the mapped drive for the queue root; judge reachability by whether the directory is readable/writable, not by mkdir's exit code |
| `Move-Item -Force` is unreliable on the share (`Cannot create a file when that file already exists`) | clear the destination first; fall back to an in-place write rather than losing a result |
| PowerShell 5.1 reads a BOM-less `.ps1` as ANSI | keep generated content ASCII-only, or save with a BOM; otherwise non-ASCII becomes mojibake |
| `$ErrorActionPreference='Stop'` + **redirecting** a native command's stderr = terminating `NativeCommandError`, regardless of exit code | use `Continue` for native calls (`git`, `npx`) and `-ErrorAction Stop` explicitly on file reads |
| Cucumber's box-drawing characters arrive mangled | `chcp 65001` + UTF-8 console encoding in the agent |

The last two are a matched pair, and getting them backwards is easy: `Continue`
is required so native stderr does not kill the agent, but it also means a failed
`Get-Content` does **not** throw -- execution continues with a null object and
fails much later somewhere unrecognisable.

---

## Running more than one box

Each box has its own queue directory, its own agent, and its own adapter. A
workstation runs one job at a time, so parallelism comes from dispatching to
different boxes concurrently:

```
gtms execute tc-AAAA --adapter remote-<box-a>     # one shell
gtms execute tc-BBBB --adapter remote-<box-b>     # another
```

Both adapters block on their own queue, so no async contract is needed.

Before relying on this: scenarios create real records in the shared test
environment, so concurrent runs may collide on shared data. That is a test-data
question, not a transport one.
