---
name: gtms-okf-index
description: Build and refresh the OKF index.md navigation files under the active test-cases tree so people and agents can find test cases without opening every file. Use when you want a browsable index, then folder, then test case path over a flat pile of specs. This is repository navigation, not a pipeline stage.
---

# gtms-okf-index

> This is a GTMS starter skill. If you are reading it under `gtms/skills/`,
> it is the uninstalled template -- copy this folder into your agent's skills
> directory and adapt the copy before use.

This skill maintains the OKF navigation layer: one `index.md` in each
qualifying folder of the test-cases tree, so a reader opens the index, then
the folder, then the case, instead of scanning every spec. It is repository
navigation, not a sixth pipeline stage. GTMS reserves `index.md` and `log.md`
but never creates or maintains them, so this agent workflow owns the index
half. It changes no test case and records no result.

## Before you start

Start from the operating reference: run `gtms agent` and read it before you
touch any file. Everything below builds on the commands and flags it defines.

```sh
gtms agent
```

## Workflow

### 1. Read GTMS first, and fail closed on any non-zero exit

Run exactly two read commands, each once over the whole tree:

```sh
gtms status -r --json
gtms map --json
```

Both `gtms status -r --json` and `gtms map --json` must exit with code
zero before you scan the filesystem or write anything. If either `exit`
code is non-zero, stop and report the diagnostics, and do not write or
change any index -- a non-zero command can still print partial JSON after a
refused handoff or an unreadable spec, and a partial index is worse than
none.

### 2. Let status own identity, and map supply requirements only

`gtms status -r --json` is the authoritative source of truth for the set of
admitted test case id values and for each `title`. Treat its id and title
set as the identities you will index; nothing else overrides it.

`gtms map --json` supplies the `requirement` for those same ids, and only
the requirement -- use it for just that column, never to add or drop an id.

If the two views disagree -- an id that `status` admits is missing from
`map`, or the sets otherwise differ or mismatch -- stop and report it.
Never silently use the smaller set.

### 3. Discover the one sentinel and the active cases tree

From the project root, inspect the direct child directories for a regular
`.gtms-root` sentinel. There must be exactly one; the active cases tree is
that sentinel's parent plus `test/cases` (the canonical default is
`gtms/test/cases`, but a renamed parent such as `testing/test/cases` is
equally valid, so read it from the `.gtms-root` you found rather than
hard-coding the path). A missing, linked or ambiguous sentinel stops the
workflow without writing: restore or run `gtms init` when none exists,
remove all but one when several exist, and replace a linked sentinel with a
regular file.

### 4. Join each admitted id to exactly one file by its body id

Recursively scan the Markdown candidates under the active cases tree. Do
not follow a symbolic link or junction that points at a directory: never
descend into a linked directory, matching GTMS's own traversal, so a
candidate below a link is not indexed.

Match each admitted id against the frontmatter `test_case_id` in the file
body, never by filename. A filename is never identity: GTMS permits an
owner whose filename does not contain its id, so do not infer an id from a
file's name. If an id matches zero files (no file carries it) or matches
more than one file (several bodies share it), stop and report that id.
Read `priority` from the single matched spec; render an absent priority as
blank.

### 5. Rebuild the whole qualifying tree deterministically

A qualifying folder is the cases root, any folder holding at least one
admitted test case, or any folder with a qualifying descendant. Rebuild
every `index.md` across the entire qualifying tree in one pass, not just the
folder the current conversation happens to be about -- a whole-tree rebuild
is the contract, because a partial index misleads. A parent index links only
to children that will themselves get an index, so you never emit a link to a
folder index that will not exist.

Each folder's `index.md` lists its own admitted cases as a table of id (as a
link to the spec file), title, requirement and priority, and links to its
qualifying subfolders.

A folder that previously had a generated `index.md` but no longer qualifies --
its last admitted case was removed or moved -- must not keep a stale listing
that advertises cases that are gone. These folders sit outside the qualifying
set the scan above visits, so find them explicitly: scan the whole active cases
tree for every `index.md` that begins with the `<!-- gtms-okf-index: generated -->`
marker, and for any whose folder is no longer in the qualifying set, rewrite it
to an empty listing, keeping its summary block between the summary markers
intact, so the whole graph stays consistent. Rewrite a stale generated index in
place; never delete a stale generated index, even when its listing is empty.
Never delete or empty an unmarked user file, and only ever rewrite an index that
carries the generated marker and is a safe regular file.

### 6. Write only marked, safe destinations

Every generated index begins with the exact marker line
`<!-- gtms-okf-index: generated -->`, and carries exactly one such marker.
Before overwriting any existing `index.md`, confirm it is an ordinary regular
file that already carries that generated marker. Stop and report the
destination -- do not overwrite it -- when it is unmarked (it lacks the
marker, or is not marked as generated). Check each kind of unsafe destination
on its own, because ruling out one does not rule out the others:

- Stop when the `index.md` destination is a symbolic link or junction.
- Stop when the `index.md` destination is a hard link (its file has more than
  one link).
- Stop when the `index.md` destination is a directory, or is not a regular
  file (a FIFO, socket, device or other special file).
- Stop when the `index.md` destination is escaping: it, or a folder above it,
  resolves outside the project or outside the active cases tree.

You may use the platform shell to inspect the destination; if you cannot
establish that it is a safe regular file you own, stop rather than overwrite
it. This is agent guidance, not a GTMS enforcement guarantee.

Never edit a test case spec and never edit `log.md`: the spec files and the
narrative `log.md` are off limits to this skill, which only ever writes
`index.md`.

### 7. Preserve the human summary between its markers

Each generated index carries exactly one summary block, bounded by
`<!-- gtms-okf-index: summary -->` and `<!-- gtms-okf-index: /summary -->`,
in that order, directly after the generated marker. The bytes a human wrote
between `<!-- gtms-okf-index: summary -->` and
`<!-- gtms-okf-index: /summary -->` are theirs: preserve and keep them
exactly on every refresh, and rebuild only the generated listing outside
that pair.

Marker defects are never repaired in place. If an existing index has a
missing, duplicate (more than one), malformed or misordered marker -- the
generated marker or either summary marker, including a summary end marker
before its start -- stop and report that index instead of overwriting it.

Writing a folder summary is opt-in, one folder at a time. Do not summarise a
folder unless asked; leave the summary block empty on folders you were not
asked to summarise, and ask before summarising more than a handful at once,
because summaries are the slow and expensive part while the listing is
instant.

## Larger or frequently indexed repositories

For a large repository, or one where the index is regenerated frequently,
recommend that the project write its own deterministic script and run that
instead of this agent workflow, which gets slow at scale. GTMS does not ship
that script; it is the project's own code, and this skill never bundles or
runs an interpreter. A project-owned deterministic script should:

- declare and check its runtime instead of assuming Python, Node or any
  other interpreter is installed;
- discover the single regular direct-child `.gtms-root` and use its
  `test/cases` tree rather than a hard-coded path;
- reconcile membership with GTMS's admitted-case JSON views, require every
  GTMS read to exit zero before writing, and never follow a linked
  directory while scanning;
- rebuild the whole qualifying graph idempotently, link only to indexes it
  will create, preserve the marked summary block, touch only marked
  `index.md` files, never edit a test case or `log.md`, and refuse linked,
  hard-linked, non-regular or escaping destinations with atomic writes;
- report every partial failure and exit non-zero, so a run that updated only
  part of the graph is never mistaken for a complete refresh by CI or callers,
  while still writing nothing when a GTMS read exits non-zero.

## Report

Summarise: the active cases tree you indexed, how many folders got an index,
any folder you were asked to summarise, and anything that made you stop
(a non-zero read, a view mismatch, an id with zero or several matches, or an
unsafe destination) so the user can fix it before re-running.
