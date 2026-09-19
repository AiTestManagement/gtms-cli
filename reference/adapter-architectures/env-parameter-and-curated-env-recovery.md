# `--env` as an adapter-owned parameter, and recovering what the curated environment strips

One entry in the adapter-architectures series. Two adapter techniques that,
together, let a single `gtms execute` path run an existing suite in **any** mode
the suite defines -- including launching the browser as a different operating
system user -- without adding GTMS concepts or writing one adapter per mode.

The worked example continues the suite from the adoption entry in this series:
a Cucumber.js suite driving Playwright, with several run profiles.

Identifying detail is placeholdered on the series convention: `<default-profile>`
and `<alt-profile>` for run profiles, `<workstation>` for a machine, `<app-host>`
for the application under test, `<fileserver>` for a file share, and
`<user-a>` / `<user-b>` for accounts. Test case IDs are **synthetic**. Where a
file is quoted unchanged it is labelled **verbatim**; where anything has been
substituted it is labelled **sanitised**.

---

## Part 1: `--env` as a profile selector

### The enabling fact is a negative

GTMS's `--env` flag is documented as *"Environment label recorded and passed to
the adapter; GTMS does not route on it."* That last clause is the whole trick.
GTMS hands the value to the adapter as `GTMS_ENVIRONMENT` and stays out of the
way, so the adapter is free to interpret it however it likes.

The runner interprets it as the framework profile:

```sh
PROFILE="${GTMS_ENVIRONMENT:-<default-profile>}"
...
npx cucumber-js -p "${PROFILE}" --tags "@${GTMS_TESTCASE}"
```

Which gives you every mode the suite already defines, selected per run:

```text
gtms execute <tc>                        ->  -p <default-profile>
gtms execute <tc> --env <alt-profile>    ->  -p <alt-profile>
```

One adapter, every mode, no GTMS change.

### Why this is design rather than a hack

A pass-through channel is inherently repurposable. Because GTMS does not route
on `--env`, the value arrives at the adapter untouched and the adapter owns its
meaning. A Cucumber adapter reads it as a profile; a different adapter could
read the same field as a Kubernetes namespace, a dataset tag, or a device ID.

**GTMS provides the value; the adapter decides the meaning.** That decoupling is
what makes the channel general, and it is a deliberate part of the flag's
contract rather than an accident you are exploiting.

### The default-vs-override idiom

`${GTMS_ENVIRONMENT:-<default>}` gives you both behaviours at once:

- Omit `--env` and you get the default profile.
- Pass `--env X` and you override to profile `X` for that one run.

To make a different mode the *default* for every run, change only the fallback.
Everything stays overridable per run.

### The same value forwards to a remote runner

Where execution is dispatched to another machine (see the remote-queue entry in
this series), the same value travels with the job and the far-side agent uses it
for its own profile selection. The dispatching adapter reads
`GTMS_ENVIRONMENT`, writes it into the job, and the agent applies it. So a
single command runs a specialised mode on a remote box, driven from your own
machine, with no additional plumbing.

---

## Part 2: recovering the ambient environment GTMS strips

### First, know which tier you are on

This problem is **specific to Tier 2 script adapters**, and the asymmetry is
easy to miss from inside a failing adapter:

- **Tier 2 (`script:`)** runs with a curated environment: a small allowlist of
  system variables plus the documented `GTMS_*` variables that GTMS itself
  generates for the task. That set is an explicit list maintained by GTMS, not
  a prefix filter: GTMS does not scan the caller's environment, so a variable
  you set in the invoking shell is not forwarded merely because its name begins
  with `GTMS_`. Anything else the caller had is absent by design. The one
  deliberate pass-through is `--env`, which GTMS surfaces as
  `GTMS_ENVIRONMENT`.
- **Tier 1 (`command:`)** inherits the caller's full environment.

So the recovery work below is a Tier 2 concern only. If your suite depends
heavily on ambient environment and you do not need the isolation, Tier 1 is a
legitimate answer to the whole problem. The exact allowlist and the rationale
for the split are in `reference/adapter-guide.md` under Tier 2 Environment
Isolation.

### Measure it; do not guess

The fastest way to find out what your adapter actually receives is to ask it. A
throwaway probe adapter that dumps its own environment settles in one run what
reading documentation guesses at:

```text
NODE_EXTRA_CA_CERTS=[]      <- stripped
USERNAME=[]                 <- stripped
COMPUTERNAME=[]             <- stripped
whoami=[<user-a>]           <- but these commands still work
hostname=[<workstation>]
```

That last pair is the important observation. The *variables* are gone, but the
*commands that produce the same facts* are still on `PATH`. That is what makes
recovery possible rather than merely diagnosable.

The same probe confirmed the Part 1 mechanism from the other side:
`GTMS_ENVIRONMENT` arrived holding exactly the value passed to `--env`.

### What broke, and how it presented

Two concrete failures followed from the stripping, and neither error message
points at the environment:

| stripped variable | what needed it | how the failure surfaced |
|---|---|---|
| `NODE_EXTRA_CA_CERTS` | Node's TLS trust for an internally-issued certificate on `<app-host>` | `unable to verify the first certificate`, at the first API call |
| `USERNAME` + `COMPUTERNAME` | the suite's credential-decode helper, which uses host and user identity to locate the right credential material | the suite reported it could not resolve the account, before the browser even launched |

Neither is a credential or certificate *problem*. The bundle was valid and the
credential file existed. The adapter simply could not see the values needed to
use them. Expect this class of failure to present as a domain error rather than
a configuration one.

### Why the adapter is the right place to fix it

Both variables are read **at process startup**:

- Node reads `NODE_EXTRA_CA_CERTS` once, when it initialises its trust store.
- The credential helper reads its identity variables when it runs.

This is what makes the obvious fix wrong. Loading an environment file *inside*
the already-started process -- `dotenv` and its equivalents -- is **too late**
for anything read at startup, because the trust store is already built by the
time that code executes.

The adapter runs *before* the framework process starts, so it is the last point
at which the environment can still be shaped. Export there.

**The general rule: a variable read at process startup must be exported by
whatever launches the process, not loaded from within it.** That outlives this
stack and this framework.

### The recovery, sanitised

Placed in the runner before it invokes the framework:

```sh
# (1) TLS trust. The suite's config hardcodes one env file regardless of profile,
#     so read the CA path from there and export it before node starts.
if [ -z "${NODE_EXTRA_CA_CERTS}" ] && [ -f "${GTMS_WORK_DIR}/.env.<default-profile>" ]; then
  _ca=$(grep -E '^NODE_EXTRA_CA_CERTS=' "${GTMS_WORK_DIR}/.env.<default-profile>" | head -1 \
        | cut -d= -f2- | sed "s/^['\"]//; s/['\"]$//")
  [ -n "${_ca}" ] && export NODE_EXTRA_CA_CERTS="${_ca}"
fi

# (2) Identity for the credential decode. Recover from whoami/hostname,
#     stripping any domain prefix. Harmless for runs that do not need it.
[ -z "${USERNAME}" ]     && export USERNAME="$(whoami 2>/dev/null | sed 's/.*\\//')"
[ -z "${COMPUTERNAME}" ] && export COMPUTERNAME="$(hostname 2>/dev/null)"
```

Three things about this are worth carrying to another suite:

- **Each recovery is guarded by `[ -z ... ]`.** It fills a gap; it never
  overrides a value that is already present. That is what makes it safe to leave
  in permanently rather than toggling it per mode.
- **Read the env file the suite actually loads, not the one named after the
  profile.** Here the suite's config hardcodes a single env file whatever
  profile is selected, so `.env.<alt-profile>` does not exist. Deriving the env
  filename from the profile name looks natural and is wrong. Check what the
  suite loads before assuming a naming convention.
- **The remote path does not need any of this.** The far-side agent runs the
  framework directly in its own shell session and inherits that session's
  ambient environment natively. Only the local Tier 2 adapter, spawned inside
  the curated environment, has to recover anything.
- **A recovered value that is wrong is harder to debug than one that is
  missing.** This is the technique's real cost, and it is worth knowing before
  relying on it. `whoami` and `hostname` return *a* plausible answer on every
  machine, so recovery always appears to succeed. If the form it produces is not
  the form the consumer expects -- case, a domain prefix handled differently, a
  service account, a hostname that is not the credential store's key -- you get
  the original symptom back with a value now present. The `[ -z ]` guard will
  not re-trigger, and nothing marks the value as synthesised rather than
  inherited. Where a recovery is load-bearing, log what it derived the first
  time you wire it up, and check it against what the consumer actually wants
  rather than against what looks right.

### A trap worth remembering: UNC paths through shell layers

The certificate bundle lived at a UNC path (`\\<fileserver>\...\cacert.pem`).
Writing that into the env file from a shell kept collapsing the leading `\\`
into a single `\`, and a single-backslash path is not a valid UNC path -- Node
silently cannot read it, falls back to its built-in certificate authorities, and
TLS fails. Both `echo` and `printf` ate the backslash.

The reliable fix was to stop fighting the shell and emit the byte from a
language that is not doing backslash interpretation:

```sh
node -e 'const b=String.fromCharCode(92); require("fs").appendFileSync(".env.<default-profile>",
  "NODE_EXTRA_CA_CERTS="+b+b+"<fileserver>"+b+"...\\cacert.pem\n")'
```

The general lesson: when a literal backslash has to survive several layers of
shell quoting, construct it by character code in a real programming language
rather than escaping it through each layer.

---

## Worked example: a multi-user workflow, end to end

The test that motivated all of this was one that could not run under the default
profile at all. It exercises a segregation-of-duties flow: `<user-a>` seeds a
record through the API, then the browser relaunches as a **different operating
system user**, `<user-b>`, to process it -- which is the entire reason the
alternate profile exists. Its distinguishing behaviour is that it drives the
browser through a control channel and can launch it under a different account.
This entry calls that the account-switching behaviour rather than reusing the
suite's own name for the mode.

```text
gtms execute tc-11111111 --env <alt-profile>
```

The two techniques unblocked it in layers, and it is worth seeing how many
distinct failures sat on top of each other:

| layer | what changed | fixed by |
|---|---|---|
| profile | ran under the alternate profile instead of the default | Part 1 (`--env` -> profile) |
| user switching | no longer refused as unsupported under the default profile | running under the alternate profile now |
| TLS | the API-seeding step reached the app instead of a certificate error | Part 2 (`NODE_EXTRA_CA_CERTS`) |
| credential decode | resolved the run-as account instead of failing to find it | Part 2 (`USERNAME` / `COMPUTERNAME`) |
| browser launch | browser launched as a different OS user | account provisioning and application-control policy -- environmental, not GTMS |

With a suitably provisioned account it then ran eight steps of the workflow:
the control connection established, `<user-a>` created the record via the API,
the application relaunched as `<user-b>`, and the record was carried through the
processing steps. The final step, saving, failed.

**It failed on the application's own business rule** -- a record's creator
cannot also be the user who processes it -- because the test environment used
one account for both roles. That is precisely the rule the
account-switching behaviour exists to satisfy: the processor must differ from
the creator.

This is worth stating plainly rather than dressing up. The machinery is proven
all the way to real business validation; going green is a matter of provisioning
two distinct capable accounts, which is environment configuration rather than
anything to do with GTMS or the adapter. "Proven up to business validation" is
an honest and useful place for a worked example to stop.

---

## What transfers

- **A pass-through parameter that the tool does not interpret is a general
  extension point.** `--env` is documented as recorded-and-passed, and that
  negative is what makes it repurposable. Look for the same shape in other
  tools: the field nobody routes on is the field you can own.
- **The default-vs-override idiom** (`${VAR:-default}`) gives per-run override
  and a project default from one line, with no configuration surface.
- **Probe before you theorise.** A throwaway adapter that dumps its environment
  answers in one run what documentation guesses at.
- **Startup-read variables must be exported by the launcher, not loaded in
  process.** The single most transferable point here, and the one that makes an
  otherwise-correct-looking `dotenv` fix fail mysteriously.
- **Guard every recovery so it fills gaps rather than overriding.** Safe to
  leave in permanently.
- **Check which environment your adapter tier actually gets** before designing
  around the problem -- on this tool, one tier inherits everything and the other
  does not.

Framework and platform detail to substitute: the profile mechanism and its
`-p` flag, `NODE_EXTRA_CA_CERTS` as Node's trust-store hook, `whoami` and
`hostname` as the identity fallback, and UNC path handling as a Windows concern.
