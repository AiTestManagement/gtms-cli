Getting Started with GTMS

GTMS orchestrates the test pipeline: CREATE test case specs, AUTOMATE them
into scripts, EXECUTE them and record results. This is the shortest path
from a fresh install to one test case through that pipeline.

If you are an AI coding agent operating in a repository, start here: that
is Mode 3, the recommended starting mode -- the agent runs the gtms
commands itself. (Mode 1 is a human running the commands; Mode 2 has GTMS
dispatch configured adapters. See USER-GUIDE.md for the three modes.)

RUNNING GTMS FROM A FOLDER (NO INSTALL)

  Dropped gtms.exe into your project folder instead of adding it to PATH?
  Invoke it as .\gtms.exe (PowerShell), gtms.exe (cmd), or ./gtms
  (macOS/Linux), and read every "gtms ..." command below accordingly. To
  type just gtms from anywhere, add the folder to your PATH.

STEP 1 -- READ THE OPERATING REFERENCE

    gtms agent          -- commands, flags, sequences, gotchas

  Works before the project is initialised. Read it before anything else.

STEP 2 -- INITIALISE (skip if gtms.config already exists)

    gtms init                     -- scaffold with the manual default preset
    gtms init --presets           -- list the available presets
    gtms init --preset bats       -- scaffold with a named preset

  Pick the preset matching how your tests will run:
    manual       -- a person or agent performs tests and fills result records
    bats         -- shell tests run by the BATS runner
    playwright   -- browser tests run by Playwright
  If your framework is not listed, start from the closest preset and adapt.

  Already initialised? Orient with gtms status (per-folder pipeline rollup)
  and gtms gaps (what is missing at each stage).

STEP 3 -- INSTALL THE STARTER SKILLS

    gtms skills         -- the gtms/skills/ catalog and install steps

  Five skills wrap the pipeline stages with process discipline (ordering,
  verification, chaining): gtms-tests-create, gtms-tests-automate,
  gtms-tests-execute, gtms-tests-prime, gtms-tests-verify-intent.

STEP 4 -- RUN ONE TEST CASE THROUGH THE PIPELINE

  Most reliable start: drive the pipeline with the installed skills. Run
  gtms-tests-create for a work item, then gtms-tests-automate and
  gtms-tests-execute -- or gtms-tests-prime for the manual path. Each
  skill verifies its stage and names the next one.

  The underlying commands, if you drive them directly:

  Automated path -- a script runs the test and records the outcome:
    gtms create login-flow        -- author a test case spec
    gtms automate tc-a1b2c3d4     -- turn the spec into a wired script
    gtms execute tc-a1b2c3d4      -- run the script, record the result

  Manual path -- no script; a person or agent performs the test:
    gtms prime tc-a1b2c3d4        -- stamp a result record to fill in
    gtms execute tc-a1b2c3d4      -- record the filled-in result formally

  Adapter flags differ by mode (agent-* vs manual-*) -- gtms agent and the
  skills carry that detail. Check progress with gtms status login-flow.

ALREADY HAVE TESTS? (BROWNFIELD)

  GTMS does not require rewriting an existing suite. For each existing
  test: author its spec with gtms create, then register the existing
  script with gtms link instead of generating one with gtms automate:

    gtms link tc-a1b2c3d4 --framework bats --artefact test/login.bats

  If your runner is not a preset, initialise from the closest preset and
  point that framework's execute adapter at your runner in gtms.config.

FURTHER READING

  gtms agent      Operating quick reference
  gtms skills     Starter skills catalog and install steps
  gtms --help     Full command list
