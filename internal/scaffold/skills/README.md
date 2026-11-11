# GTMS starter skills

Starter agent skills for the GTMS pipeline, in the Agent Skills open-standard
format (one folder per skill containing a `SKILL.md`; see agentskills.io).

The five pipeline skills wrap one stage each. `gtms-okf-index` is different:
it is a repository navigation skill, not a pipeline stage. It maintains the
OKF `index.md` files under the test-cases tree so cases are browsable.

| Skill | Role |
|-------|------|
| `gtms-tests-create` | CREATE -- author test case specs |
| `gtms-tests-automate` | AUTOMATE -- turn specs into wired scripts |
| `gtms-tests-execute` | EXECUTE -- run wired scripts, record results |
| `gtms-tests-prime` | PRIME -- stamp and fill a one-time result record, recorded via execute; no script |
| `gtms-tests-verify-intent` | Quality gate -- verify scripts implement their specs |
| `gtms-okf-index` | Repository navigation (OKF index) -- build and refresh the test-case index.md files |

## Installing

Copy a skill folder into your agent's skills discovery directory --
`.claude/skills/`, `.agents/skills/`, or your tool's equivalent:

```sh
cp -r gtms/skills/gtms-tests-create .claude/skills/gtms-tests-create
```

The installed copy is yours: adapt it to your project's conventions, add
your own steps, tighten the report format. This catalog is the uninstalled
template set -- do not run skills from here, and make customisations in the
installed copy, not in this directory.

To refresh this catalogue, run `gtms init --force --preset <preset>` (the
preset the project was created with) or `gtms init --force --demo`. Bare
`gtms init --force` in an existing project changes nothing. A refresh:

- adds any starter skill that is missing from this directory;
- upgrades two files in place, and only when each is an unedited copy of a
  recognised older shipped version: `gtms-tests-create/SKILL.md` and this
  `README.md`;
- keeps an edited or unrecognised copy of either of those two files
  untouched, writing the shipped version beside it as a `.gtms-new`
  companion to merge by hand;
- leaves existing copies of every other skill unchanged, edited or not.

GTMS never manages installed copies under `.claude/skills/`,
`.agents/skills/` or any other agent directory; re-copy a skill there
yourself after a refresh.

Run `gtms skills` for the overview and `gtms agent` for the operating
reference every skill builds on.
