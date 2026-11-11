Starter Agent Skills for the GTMS Pipeline

GTMS ships starter agent skills as generic, ready-to-install examples in the
Agent Skills open-standard format (agentskills.io). Five wrap the stages of the
GTMS pipeline -- arguments, steps, verification, report format, and a pointer to
the next stage -- so an agent can drive the workflow correctly from day one. One
more, gtms-okf-index, is a repository navigation skill rather than a pipeline
stage (see NAVIGATION SKILL below).

PIPELINE SKILLS

  Skill                       Stage
  -----                       -----
  gtms-tests-create           CREATE -- author test case specs
  gtms-tests-automate         AUTOMATE -- turn specs into wired scripts
  gtms-tests-execute          EXECUTE -- run scripts, record results
  gtms-tests-prime            PRIME -- stamp and fill a one-time result record
  gtms-tests-verify-intent    Quality gate -- verify scripts match specs

NAVIGATION SKILL

  gtms-okf-index              Repository navigation -- build and refresh the OKF
                              index.md files under the test-cases tree so cases
                              are browsable. Not a pipeline stage.

Each pipeline skill's first step is "run gtms agent and read the operating
reference."
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
  belong in the installed copy only.

REFRESHING THE CATALOG

  Refresh with gtms init --force --preset <preset> (the preset the project was
  created with) or gtms init --force --demo. Bare gtms init --force in an
  existing project changes nothing. A refresh:

    - adds any starter skill missing from gtms/skills/;
    - upgrades two files in place, and only when each is an unedited copy of
      a recognised older shipped version: gtms-tests-create/SKILL.md and the
      catalog README.md;
    - keeps an edited or unrecognised copy of either of those two files
      untouched, writing the shipped version beside it as a .gtms-new
      companion;
    - leaves existing copies of every other skill unchanged, edited or not.

  GTMS never manages installed copies under .claude/skills/, .agents/skills/
  or any other agent directory; re-copy a skill there yourself after a
  refresh.

FURTHER READING

  gtms agent    Operating quick reference (commands, flags, sequences)
  gtms --help   Full guide and adapter-contract permalinks
