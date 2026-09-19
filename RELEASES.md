# Release notes

Release notes for `gtms`, newest version first.

These notes are published in the `gtms-cli` repository and linked from each
GitHub Release from v0.3.3 onward. Full documentation is in `USER-GUIDE.md`;
install instructions are in `README.md`.

---

## v0.3.3

Configurable triage. You define what a failure can be classified as, GTMS
records it and shows it back, and it never acts on your behalf. Plus honest
outcomes from Playwright runs and from adapter result contracts.

**Changes**

- Triage values are yours. A `triage:` block in `gtms.config` lists the keys a
  classification may use; `gtms init` scaffolds six, and a project with no
  block gets those same defaults. Classify with
  `gtms triage <test-case-id> --value <key>`, optionally `--summary` and
  `--defect`, and `--resolve` once it is dealt with.
- A classification is a record, not an action. Nothing is queued, no spec is
  edited, no result changes. One git-tracked YAML record per test case and
  framework lands under `gtms/triage/`, holding a snapshot of what it classified.
- Read it back with `gtms triage --show` and `--list`, which takes a folder or
  a test-case ID, filters by value, and prints JSON. `gtms status --show-triage`
  adds triage to the dashboard, `--triage <value>` filters to it, and
  `gtms gaps` counts triaged against untriaged. Status output without the flag
  is unchanged.
- Results recorded through the built-in `manual` framework can be classified
  too. Having no wiring record, they were invisible to triage before.
- `--automation-wrong`, `--test-wrong` and `--app-wrong` are gone. All three
  names remain valid values, so classify with `--value` instead.
- The shipped Playwright runner reads Playwright's JSON report instead of the
  exit code. A run where every test skipped is recorded as skipped, not passed,
  and a missing or malformed report is an error rather than a test outcome.
  Node must be installed alongside `npx`.
- The BATS and Playwright runners `gtms init` ships write their log block as
  `log: |2`. The old form let YAML infer the indent from your tool's first
  output line, so ordinary indented output could make the contract unreadable.
- An adapter result contract GTMS cannot use is an error, never a pass. Invalid
  YAML, missing or unreadable all record `status: error`.

**Upgrading**

- Re-run any Playwright suite that reported a pass while skipping everything.
  Those passes were never evidence of a passing test.
- `gtms init --force --preset bats` or `--preset playwright` upgrades a shipped
  runner you have not edited. An edited one is left alone and the current
  version written beside it as `.gtms-new` to merge.

**Known limitations**

- A test-case ID pasted with a trailing non-breaking space is reported as an
  invalid folder name. The character is invisible; delete it and retry.

---

## v0.3.2

Reliability and usability. Fixes from the first v0.3.1 field install, both
v0.3.1 known issues closed, and a destructive write removed from `gtms triage`.
No new commands.

**Changes**

- `gtms delete` keeps your test scripts by default. `--force-artefact-deletion`
  removes them -- and even then, a script still referenced by a test case
  outside what you are deleting is kept.
- Folder arguments behave identically across every folder-aware command: mixed
  case, either separator, trailing separator, folder-qualified test case IDs.
  Closes both known issues shipped with v0.3.1.
- `gtms triage --test-wrong` no longer rewrites your test case spec. Decisions
  append to `gtms/triage-history/<test-case-id>.md`, and `--defect` is recorded
  there rather than printed and forgotten.
- `gtms link --refresh --dry-run` previews a refresh at any scope without
  writing anything.
- Git problems are diagnosed precisely, including a repository that exists but
  cannot be used from the current shell -- where GTMS no longer suggests a
  `git init` that would make things worse.
- `gtms init` handles a repository with no origin remote instead of recording a
  placeholder URL. HTTPS remote inference also fixed.
- `gtms automate` against a manual preset names both ways forward instead of
  dead-ending.
- A manual failure no longer shows a pass icon, and a stale error no longer
  hides a newer manual result.

**Docs.** User guide, adapter guide and the built-in `gtms agent` cheat sheet
refreshed together. New: worked adapter architectures under
`reference/adapter-architectures/`, taken from real installs.

**Known limitations**

- Triage decisions are recorded but nothing reads them back yet, and a later
  passing run does not clear or flag an earlier decision. Fixed in v0.3.3.
- Test case IDs are unique per folder, not across your whole tree. If the same
  ID exists in two folders, `gtms delete` on one folder also deletes the
  other's spec. Check for duplicates first; `--dry-run` lists what would go.
