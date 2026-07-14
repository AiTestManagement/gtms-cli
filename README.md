# GTMS — Git-based Test Management System

> *One tester orchestrating what used to take an army, never touching a selector.*

GTMS is a CLI tool that orchestrates AI coding agents through a three-stage pipeline:

```
CREATE test cases  →  AUTOMATE them  →  EXECUTE them
```

You write requirements. AI writes the tests. GTMS conducts the orchestra.

## Install

GTMS ships as a single self-contained binary -- no installer, no runtime, no DLLs. The fastest way to start is to download it and run it from your project folder.

**Run it straight away (no install):**

Download the latest release from [GitHub Releases](https://github.com/aitestmanagement/gtms-cli/releases), unzip it, and drop `gtms.exe` (or `gtms` on macOS/Linux) into your project folder. Run it from there -- no PATH setup required:

```powershell
.\gtms.exe version    # PowerShell (the Windows default)
```

```bat
gtms.exe version      # cmd.exe
```

```bash
./gtms version        # macOS / Linux
```

Every `gtms ...` example in this README works the same way from that folder -- read them as `.\gtms.exe ...` (PowerShell), `gtms.exe ...` (cmd), or `./gtms ...` (macOS/Linux).

On Windows, if SmartScreen shows *"Windows protected your PC"*, click **More info -> Run anyway** -- SmartScreen reputation builds organically across the first few signed releases. See [SIGNING.md](SIGNING.md) for the signing policy and how to verify a binary.

**Prefer to type just `gtms` from anywhere?** Add the folder to your PATH, or install with Go (requires Go 1.21+):

```bash
go install github.com/aitestmanagement/gtms-cli/cmd/gtms@latest
```

Then the `gtms ...` examples below work verbatim.

## Quick Start

```bash
# Initialise a project (in an existing git repo)
gtms init --name "My Project" --repo "org/my-repo" --preset manual

# Create test cases from a requirement
gtms create login --reference REQ-001 --context-file requirements/login.md

# Automate a test case
gtms automate tc-f1a2b3c --framework playwright

# Run the automated test
gtms execute tc-f1a2b3c

# Check the pipeline
gtms status
gtms gaps
```

## Commands

| Command | What it does |
|---------|-------------|
| `gtms init` | Scaffold a GTMS project (directories, config, templates) |
| `gtms create <folder>` | Generate test cases from a requirement |
| `gtms automate <tc-id>` | Generate automation for a test case |
| `gtms execute <tc-id>` | Validate automated tests during development |
| `gtms status [folder]` | Pipeline overview or detail for one test case |
| `gtms gaps [folder]` | Coverage gap analysis |
| `gtms triage <tc-id>` | Classify a failure and trigger follow-on action |

## How It Works

GTMS doesn't write tests or run them — it delegates to **adapters**. An adapter is any tool that can do the work: Claude, GPT, Copilot, a shell script, or anything else you configure.

```yaml
# gtms.config
project:
  name: "My Project"
  repo: org/my-repo

adapters:
  create:
    local-claude:
      mode: sync
      prompt-template: gtms/test/prompts/create-standard.md   # you author this file; gtms init does not scaffold it
      command: 'claude -p "Create test cases from the source material." --append-system-prompt-file {prompt_file}'
  automate:
    local-claude:
      mode: sync
      prompt-template: gtms/automation/prompts/automate-standard.md   # you author this file too
      command: 'claude -p "Generate an automated test from the test case." --append-system-prompt-file {prompt_file}'
  execute:
    local-runner:
      mode: sync
      command: 'npx playwright test {artefact_file}'

defaults:
  create: local-claude
  automate: local-claude
  execute: local-runner
```

Two adapter tiers:

| Tier | Config | How it works |
|------|--------|-------------|
| **1** | `command` | Variable substitution into a shell command template |
| **2** | `script` | A script receives `GTMS_*` environment variables |

See the [Adapter Guide](reference/adapter-guide.md) for the full reference.

## What GTMS Is (and Isn't)

**GTMS is a test development tool, not a test execution engine.** `gtms execute` validates your tests during development — checking that what you've built works before you trust it. Once stable, your tests graduate into your real CI pipeline.

Git helps you develop code but doesn't run it in production. GTMS helps you develop tests but doesn't run them in production.

## Requirements

- Go 1.21+ (for building from source)
- Git on PATH
- An AI coding tool (Claude, GPT, Copilot, etc.) for create/automate adapters

## Verifying Releases

Windows binaries are Authenticode-signed via [SignPath Foundation](https://signpath.org/). See [SIGNING.md](SIGNING.md) for the signing policy and verification instructions.

## License

[MIT](LICENSE)
