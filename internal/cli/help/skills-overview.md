Starter Agent Skills for the GTMS Pipeline

GTMS ships five starter agent skills as generic, ready-to-install examples in
the Agent Skills open-standard format (agentskills.io). Each skill wraps one
stage of the GTMS pipeline -- arguments, steps, verification, report format,
and a pointer to the next stage -- so an agent can drive the workflow correctly
from day one.

PIPELINE SKILLS

  Skill                       Stage
  -----                       -----
  gtms-tests-create           CREATE -- author test case specs
  gtms-tests-automate         AUTOMATE -- turn specs into wired scripts
  gtms-tests-execute          EXECUTE -- run scripts, record results
  gtms-tests-prime            PRIME -- stamp and fill a one-time result record
  gtms-tests-verify-intent    Quality gate -- verify scripts match specs

Each skill's first step is "run gtms agent and read the operating reference."
The skill adds process discipline on top of that contract -- ordering, checks,
report shape, and chaining to the next skill -- without restating commands or
flags.

WHERE THEY LIVE

  gtms init writes the catalog to gtms/skills/<name>/SKILL.md.
  The catalog is the uninstalled template set. Do not run skills from there.

INSTALLING A SKILL

  Copy a skill folder into your agent's skills discovery directory:

    cp -r gtms/skills/gtms-tests-create .claude/skills/gtms-tests-create

  Common discovery directories: .claude/skills/, .agents/skills/, or
  your tool's equivalent. The installed copy is yours -- adapt it to your
  project's conventions. The catalog copy is the read-only starter; edits
  belong in the installed copy only. Re-running gtms init never overwrites
  existing files.

FURTHER READING

  gtms agent    Operating quick reference (commands, flags, sequences)
  gtms --help   Full guide and adapter-contract permalinks
