# Agent instructions snippet

Paste the block below into your project's AI agent instruction file
(CLAUDE.md, AGENTS.md, .cursor/rules, or your tool's equivalent) so any coding
agent is routed to GTMS before it starts writing test cases. GTMS never edits
that file for you -- copy this in once.

---

## Testing: this project uses GTMS

This project uses GTMS (Git-based Test Management System) to manage its test
cases -- the create -> automate -> execute pipeline. It does not use GTMS for
unit tests; write and run those the usual way.

Before creating, automating, or executing any test cases, run `gtms agent` to
load the operating reference for the pipeline.

- `gtms agent`          -- how GTMS orchestrates the create -> automate -> execute pipeline
- `gtms skills`         -- starter agent skills under `gtms/skills/` you can install
- If `gtms` reports no project here, read `gtms getting-started` first.
