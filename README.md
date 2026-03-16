# GTMS — Git-based Test Management System

> *One tester orchestrating what used to take an army, never touching a selector.*

GTMS is a CLI tool that orchestrates AI coding agents through a three-stage pipeline:

```
CREATE test cases  →  AUTOMATE them  →  EXECUTE them
```

You write requirements. AI writes the tests. GTMS conducts the orchestra.

## Install

**Go install** (requires Go 1.21+):

```bash
go install github.com/aitestmanagement/gtms-cli/cmd/gtms@latest
```

**Download a binary** from [GitHub Releases](https://github.com/AiTestManagement/gtms-cli/releases), extract, and add to your PATH.

**Verify:**

```bash
gtms version
```

## Quick Start

```bash
# Initialise a project (in an existing git repo)
gtms init --name "My Project" --repo "org/my-repo" --adapter claude

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
      prompt-template: test-cases/prompts/create-standard.md
      command: 'claude -p "Create test cases from the source material." --append-system-prompt-file {prompt_file}'
  automate:
    local-claude:
      mode: sync
      prompt-template: test-automation/prompts/automate-standard.md
      command: 'claude -p "Generate an automated test from the test case." --append-system-prompt-file {prompt_file}'
  execute:
    local-runner:
      mode: sync
      command: 'npx playwright test {spec_file}'

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

## License

[MIT](LICENSE)
