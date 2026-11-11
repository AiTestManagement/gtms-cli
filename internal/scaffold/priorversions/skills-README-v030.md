# GTMS starter skills

Starter agent skills for the GTMS pipeline, in the Agent Skills open-standard
format (one folder per skill containing a `SKILL.md`; see agentskills.io).

| Skill | Pipeline stage |
|-------|----------------|
| `gtms-tests-create` | CREATE -- author test case specs |
| `gtms-tests-automate` | AUTOMATE -- turn specs into wired scripts |
| `gtms-tests-execute` | EXECUTE -- run wired scripts, record results |
| `gtms-tests-prime` | PRIME -- stamp and fill a one-time result record, recorded via execute; no script |
| `gtms-tests-verify-intent` | Quality gate -- verify scripts implement their specs |

## Installing

Copy a skill folder into your agent's skills discovery directory --
`.claude/skills/`, `.agents/skills/`, or your tool's equivalent:

```sh
cp -r gtms/skills/gtms-tests-create .claude/skills/gtms-tests-create
```

The installed copy is yours: adapt it to your project's conventions, add
your own steps, tighten the report format. This catalog is the uninstalled
template set -- do not run skills from here, and make customisations in the
installed copy, not in this directory. Re-running `gtms init` never
overwrites these files.

Run `gtms skills` for the overview and `gtms agent` for the operating
reference every skill builds on.
