package reader

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
)

// setupFixtureProject creates a temporary project directory with test fixtures.
// It mirrors the testdata/project/ structure from the spec.
func setupFixtureProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	// Create gtms.config
	writeFile(t, root, "gtms.config", `project:
  name: test-project
  repo: github.com/example/test
`)

	// Create test case files
	writeFile(t, root, filepath.Join("gtms/cases", "natural", "tc-007-checkout-guest.md"), `---
test_case_id: tc-007
title: Checkout Flow - Guest User
requirement: JIRA-456
priority: High
type: Integration
status: automated
tags: [checkout, guest, smoke]
created: 2026-02-19
---

## Steps
1. Navigate to homepage
2. Add item to cart
3. Checkout as guest
`)

	writeFile(t, root, filepath.Join("gtms/cases", "natural", "tc-008-checkout-registered.md"), `---
test_case_id: tc-008
title: Checkout Flow - Registered User
requirement: JIRA-456
status: automated
tags: [checkout, registered]
---

## Steps
1. Navigate to homepage
2. Log in
3. Add item to cart
4. Checkout
`)

	writeFile(t, root, filepath.Join("gtms/cases", "natural", "tc-009-login-sso.md"), `---
test_case_id: tc-009
title: Login - SSO
requirement: JIRA-789
status: ready
tags: [login, sso]
---

## Steps
1. Navigate to login page
2. Click SSO button
`)

	// Create spec files referencing tc-007 and tc-008 (tc-009 intentionally has NO spec file)
	writeFile(t, root, filepath.Join("gtms/automation", "specs", "playwright", "checkout.spec.ts"),
		"test('tc-007: Checkout guest', () => {});\ntest('tc-008: Checkout registered', () => {});")

	// Create automation records — CON-023: wiring + (optional) handoff overlay.
	seedLegacyRecord(t, root, legacyRecord{
		TC:               "tc-007",
		Framework:        "playwright",
		Adapter:          "local-claude",
		Artefact:         "gtms/automation/specs/tc-007-checkout-guest.spec.ts",
		Result:           "pass",
		ExecutedArtefact: "results/junit/tc-007.xml",
		Attempts:         2,
	})

	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-008",
		Framework: "playwright",
		Adapter:   "local-claude",
		Artefact:  "gtms/automation/specs/tc-008-checkout-registered.spec.ts",
		Attempts:  1,
		// No Result — wiring exists but no terminal handoff. Reader treats
		// ExecuteStatus as "none".
	})

	// Create task files
	writeFile(t, root, filepath.Join("gtms/tasks", "pending", "task-a1b2c3d-automate-tc-009.md"), `---
id: task-a1b2c3d
type: automate
target: tc-009
adapter: local-claude
status: pending
created: 2025-02-12T11:00:00Z
branch: feature/automate-tc-009
---

## Task
Automate tc-009 Login - SSO
`)

	writeFile(t, root, filepath.Join("gtms/tasks", "complete", "task-e4f5a6b-create-JIRA-456.md"), `---
id: task-e4f5a6b
type: create
target: JIRA-456
adapter: local-claude
status: complete
created: 2025-02-10T10:00:00Z
branch: feature/create-JIRA-456
---
`)

	writeFile(t, root, filepath.Join("gtms/tasks", "complete", "task-c7d8e9f-automate-tc-007.md"), `---
id: task-c7d8e9f
type: automate
target: tc-007
adapter: local-claude
status: complete
created: 2025-02-11T10:00:00Z
branch: feature/automate-tc-007
---
`)

	// Create empty results directory
	mkdirAll(t, root, filepath.Join("results", "junit"))

	return root
}

// writeFile creates a file with the given content, creating parent dirs as needed.
func writeFile(t *testing.T, root, relPath, content string) {
	t.Helper()
	fullPath := filepath.Join(root, relPath)
	dir := filepath.Dir(fullPath)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(fullPath, []byte(content), 0o644))
}

// mkdirAll creates a directory and all parents.
func mkdirAll(t *testing.T, root, relPath string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(root, relPath), 0o755))
}

// --- PipelineStatus tests ---

func TestPipelineStatus_FullFixture(t *testing.T) {
	root := setupFixtureProject(t)

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 3)

	// Entries should be sorted by TestCaseID
	assert.Equal(t, "tc-007", entries[0].TestCaseID)
	assert.Equal(t, "tc-008", entries[1].TestCaseID)
	assert.Equal(t, "tc-009", entries[2].TestCaseID)

	// tc-007: fully automated, last result = pass
	assert.Equal(t, "Checkout Flow - Guest User", entries[0].Title)
	assert.Equal(t, "checkout-guest", entries[0].Slug)
	assert.Equal(t, "complete", entries[0].CreateStatus)
	assert.Equal(t, "complete", entries[0].AutomateStatus) // accepted = complete
	assert.Equal(t, "complete", entries[0].ExecuteStatus)
	assert.Equal(t, "pass", entries[0].LastResult)
	assert.Equal(t, "playwright", entries[0].Framework) // ENH-072

	// tc-008: automated (accepted), never executed
	assert.Equal(t, "Checkout Flow - Registered User", entries[1].Title)
	assert.Equal(t, "checkout-registered", entries[1].Slug)
	assert.Equal(t, "complete", entries[1].CreateStatus)
	assert.Equal(t, "complete", entries[1].AutomateStatus)
	assert.Equal(t, "none", entries[1].ExecuteStatus) // no formal result
	assert.Equal(t, "none", entries[1].LastResult)
	assert.Equal(t, "playwright", entries[1].Framework) // ENH-072: framework set even when no result

	// tc-009: created but not automated, has a pending automate task
	assert.Equal(t, "Login - SSO", entries[2].Title)
	assert.Equal(t, "login-sso", entries[2].Slug)
	assert.Equal(t, "complete", entries[2].CreateStatus)
	assert.Equal(t, "pending", entries[2].AutomateStatus) // pending task
	assert.Equal(t, "none", entries[2].ExecuteStatus)
	assert.Equal(t, "none", entries[2].LastResult)
	assert.Equal(t, "", entries[2].Framework) // ENH-072: no automation record = empty framework
}

func TestPipelineStatus_EmptyProject(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", `project:
  name: empty
  repo: github.com/example/empty
`)

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestPipelineStatus_NoAutomationDir(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("gtms/cases", "tc-001.md"), `---
test_case_id: tc-001
title: Some Test
requirement: REQ-1
status: ready
---
`)

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "tc-001", entries[0].TestCaseID)
	assert.Equal(t, "", entries[0].Slug, "Short filename without slug portion should have empty slug")
	assert.Equal(t, "complete", entries[0].CreateStatus)
	assert.Equal(t, "none", entries[0].AutomateStatus)
	assert.Equal(t, "none", entries[0].ExecuteStatus)
}

func TestPipelineStatus_SlugDerived(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("gtms/cases", "tc-a1b2c3d-tier1-sync-happy-path.md"), `---
test_case_id: tc-a1b2c3d
title: Tier 1 Sync Happy Path
requirement: REQ-1
---
`)

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "tc-a1b2c3d", entries[0].TestCaseID)
	assert.Equal(t, "tier1-sync-happy-path", entries[0].Slug)
}

func TestPipelineDetail_SlugDerived(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("gtms/cases", "tc-a1b2c3d-tier1-sync-happy-path.md"), `---
test_case_id: tc-a1b2c3d
title: Tier 1 Sync Happy Path
requirement: REQ-1
---
`)

	detail, err := PipelineDetail(root, "tc-a1b2c3d", "", false)
	require.NoError(t, err)
	require.NotNil(t, detail)
	assert.Equal(t, "tier1-sync-happy-path", detail.Slug)
}

func TestPipelineStatus_MalformedFrontmatter(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")

	// Valid file
	writeFile(t, root, filepath.Join("gtms/cases", "tc-001.md"), `---
test_case_id: tc-001
title: Valid Test
requirement: REQ-1
status: ready
---
`)

	// Malformed file - should be skipped without crashing
	writeFile(t, root, filepath.Join("gtms/cases", "tc-bad.md"), `---
this is not valid yaml: [
broken: {{
---
`)

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	// Should have only the valid entry
	assert.Len(t, entries, 1)
	assert.Equal(t, "tc-001", entries[0].TestCaseID)
}

func TestPipelineStatus_MalformedAutomation(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("gtms/cases", "tc-001.md"), `---
test_case_id: tc-001
title: Valid Test
requirement: REQ-1
status: ready
---
`)
	// CON-023: reader no longer scans gtms/automation/records/, so the
	// malformed-frontmatter robustness check moves to a malformed wiring
	// file. The reader must skip the bad file and surface the TC as
	// "no automation" instead of crashing.
	writeFile(t, root, filepath.Join("gtms/automation", "wiring", "tc-001--bats.wiring.yaml"),
		"broken: yaml: [[[\n")

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
	// Should still show tc-001 but without automation
	assert.Equal(t, "none", entries[0].AutomateStatus)
}

func TestPipelineStatus_MalformedTask(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("gtms/cases", "tc-001.md"), `---
test_case_id: tc-001
title: Valid Test
requirement: REQ-1
status: ready
---
`)
	// Malformed task
	writeFile(t, root, filepath.Join("gtms/tasks", "pending", "task-bad.md"), `---
broken yaml [[[
---
`)

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}

func TestPipelineStatus_InProgressTask(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("gtms/cases", "tc-001.md"), `---
test_case_id: tc-001
title: Test Case
requirement: REQ-1
status: ready
---
`)
	writeFile(t, root, filepath.Join("gtms/tasks", "in-progress", "task-abc1234-automate-tc-001.md"), `---
id: task-abc1234
type: automate
target: tc-001
adapter: local-claude
status: in-progress
created: 2025-02-12T11:00:00Z
branch: feature/automate-tc-001
---
`)

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "in-progress", entries[0].AutomateStatus)
}

// --- BUG-017 regression tests ---

func TestBUG017_FailedExecuteTaskShowsFailedStatus(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("gtms/cases", "tc-a7b9c1d-checkout.md"), `---
test_case_id: tc-a7b9c1d
title: Checkout Test
requirement: REQ-1
---
`)
	// Wiring exists, no terminal handoff yet — automate complete, execute none.
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-a7b9c1d",
		Framework: "bats",
		Artefact:  "gtms/automation/specs/tc-a7b9c1d.bats",
		Attempts:  1,
	})
	// Error execute task in gtms/tasks/error/
	writeFile(t, root, filepath.Join("gtms/tasks", "error", "task-71b7453-execute-tc-a7b9c1d.md"), `---
id: task-71b7453
type: execute
target: tc-a7b9c1d
adapter: bats-runner
status: error
created: 2026-03-07T10:00:00Z
branch: main
---
`)

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	assert.Equal(t, "tc-a7b9c1d", entries[0].TestCaseID)
	assert.Equal(t, "complete", entries[0].CreateStatus)
	assert.Equal(t, "complete", entries[0].AutomateStatus)
	assert.Equal(t, "error", entries[0].ExecuteStatus, "Error execute task should set ExecuteStatus to 'error'")
	assert.Equal(t, "none", entries[0].LastResult, "No formal result should remain 'none'")
}

func TestBUG017_FailedExecuteOverridesPreviousPass(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("gtms/cases", "tc-001.md"), `---
test_case_id: tc-001
title: Test With Pass Then Fail
requirement: REQ-1
---
`)
	// Wiring + handoff carrying a passing terminal result, dated before
	// the newer failed task below so applyTaskStatus's BUG-044 record-
	// supersedes-task check doesn't swallow the override.
	seedLegacyRecord(t, root, legacyRecord{
		TC:         "tc-001",
		Framework:  "bats",
		Artefact:   "gtms/automation/specs/tc-001.bats",
		Result:     "pass",
		ExecutedAt: "2026-03-07T09:00:00Z",
		Attempts:   2,
	})
	// A newer failed execute task — re-execution failed
	writeFile(t, root, filepath.Join("gtms/tasks", "error", "task-abc1234-execute-tc-001.md"), `---
id: task-abc1234
type: execute
target: tc-001
adapter: bats-runner
status: error
created: 2026-03-07T10:00:00Z
branch: main
---
`)

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	// Failed task should override the previous pass — the latest execution didn't pass
	assert.Equal(t, "error", entries[0].ExecuteStatus, "Failed task should override previous pass")
	assert.Equal(t, "pass", entries[0].LastResult, "LastResult still reflects automation record")
}

func TestBUG017_FailedExecuteInPipelineDetail(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("gtms/cases", "tc-a7b9c1d-checkout.md"), `---
test_case_id: tc-a7b9c1d
title: Checkout Test
requirement: REQ-1
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-a7b9c1d",
		Framework: "bats",
		Artefact:  "gtms/automation/specs/tc-a7b9c1d.bats",
		Attempts:  1,
	})
	writeFile(t, root, filepath.Join("gtms/tasks", "error", "task-71b7453-execute-tc-a7b9c1d.md"), `---
id: task-71b7453
type: execute
target: tc-a7b9c1d
adapter: bats-runner
status: error
created: 2026-03-07T10:00:00Z
branch: main
---
`)

	detail, err := PipelineDetail(root, "tc-a7b9c1d", "", false)
	require.NoError(t, err)
	require.NotNil(t, detail)

	assert.Equal(t, "error", detail.ExecuteStatus, "Detail view should also show error execute status")
}

// --- BUG-018 regression tests ---

func TestBUG018_NewerCompleteOverridesOlderFailed(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("gtms/cases", "tc-001.md"), `---
test_case_id: tc-001
title: Test With Fail Then Pass
requirement: REQ-1
---
`)
	// Wiring + handoff with a passing terminal result.
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-001",
		Framework: "bats",
		Artefact:  "gtms/automation/specs/tc-001.bats",
		Result:    "pass",
		Attempts:  2,
	})
	// Older failed execute task
	writeFile(t, root, filepath.Join("gtms/tasks", "error", "task-old1234-execute-tc-001.md"), `---
id: task-old1234
type: execute
target: tc-001
adapter: bats-runner
status: error
created: 2026-03-07T09:00:00Z
branch: main
---
`)
	// Newer completed execute task
	writeFile(t, root, filepath.Join("gtms/tasks", "complete", "task-new5678-execute-tc-001.md"), `---
id: task-new5678
type: execute
target: tc-001
adapter: bats-runner
status: complete
created: 2026-03-07T10:00:00Z
branch: main
---
`)

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	// Newer completed task should win over older failed task
	assert.Equal(t, "complete", entries[0].ExecuteStatus,
		"Newer completed task should override older failed task")
	assert.Equal(t, "pass", entries[0].LastResult)
}

func TestBUG018_NewerFailedOverridesOlderComplete(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("gtms/cases", "tc-001.md"), `---
test_case_id: tc-001
title: Test With Pass Then Fail
requirement: REQ-1
---
`)
	// Wiring + handoff dated before the newer failed task so applyTaskStatus
	// considers the failed task as truly newer than the record's last run.
	seedLegacyRecord(t, root, legacyRecord{
		TC:         "tc-001",
		Framework:  "bats",
		Artefact:   "gtms/automation/specs/tc-001.bats",
		Result:     "pass",
		ExecutedAt: "2026-03-07T08:00:00Z",
		Attempts:   2,
	})
	// Older completed execute task
	writeFile(t, root, filepath.Join("gtms/tasks", "complete", "task-old1234-execute-tc-001.md"), `---
id: task-old1234
type: execute
target: tc-001
adapter: bats-runner
status: complete
created: 2026-03-07T09:00:00Z
branch: main
---
`)
	// Newer failed execute task
	writeFile(t, root, filepath.Join("gtms/tasks", "error", "task-new5678-execute-tc-001.md"), `---
id: task-new5678
type: execute
target: tc-001
adapter: bats-runner
status: error
created: 2026-03-07T10:00:00Z
branch: main
---
`)

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	// Newer failed task should win over older completed task
	assert.Equal(t, "error", entries[0].ExecuteStatus,
		"Newer failed task should override older completed task")
}

func TestBUG018_FailedOnlyStillShowsFailed(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("gtms/cases", "tc-001.md"), `---
test_case_id: tc-001
title: Test Failed Only
requirement: REQ-1
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-001",
		Framework: "bats",
		Artefact:  "gtms/automation/specs/tc-001.bats",
		Attempts:  1,
	})
	// Only a failed execute task, no completed task
	writeFile(t, root, filepath.Join("gtms/tasks", "error", "task-abc1234-execute-tc-001.md"), `---
id: task-abc1234
type: execute
target: tc-001
adapter: bats-runner
status: error
created: 2026-03-07T10:00:00Z
branch: main
---
`)

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	assert.Equal(t, "error", entries[0].ExecuteStatus,
		"Single failed task with no completed task should show failed")
}

func TestBUG018_NewerCompleteInPipelineDetail(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("gtms/cases", "tc-001.md"), `---
test_case_id: tc-001
title: Detail View Test
requirement: REQ-1
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-001",
		Framework: "bats",
		Artefact:  "gtms/automation/specs/tc-001.bats",
		Result:    "pass",
		Attempts:  2,
	})
	// Older failed
	writeFile(t, root, filepath.Join("gtms/tasks", "error", "task-old1234-execute-tc-001.md"), `---
id: task-old1234
type: execute
target: tc-001
adapter: bats-runner
status: error
created: 2026-03-07T09:00:00Z
branch: main
---
`)
	// Newer completed
	writeFile(t, root, filepath.Join("gtms/tasks", "complete", "task-new5678-execute-tc-001.md"), `---
id: task-new5678
type: execute
target: tc-001
adapter: bats-runner
status: complete
created: 2026-03-07T10:00:00Z
branch: main
---
`)

	detail, err := PipelineDetail(root, "tc-001", "", false)
	require.NoError(t, err)
	require.NotNil(t, detail)

	assert.Equal(t, "complete", detail.ExecuteStatus,
		"Detail view should also respect timestamp comparison")
}

// --- PipelineDetail tests ---

func TestPipelineDetail_Found(t *testing.T) {
	root := setupFixtureProject(t)

	detail, err := PipelineDetail(root, "tc-007", "", false)
	require.NoError(t, err)
	require.NotNil(t, detail)

	assert.Equal(t, "tc-007", detail.TestCaseID)
	assert.Equal(t, "checkout-guest", detail.Slug)
	assert.Equal(t, "Checkout Flow - Guest User", detail.Title)
	assert.Equal(t, "JIRA-456", detail.Requirement)
	assert.Equal(t, "complete", detail.CreateStatus)
	assert.Equal(t, "complete", detail.AutomateStatus)
	assert.Equal(t, "complete", detail.ExecuteStatus)
	assert.Equal(t, "pass", detail.LastResult)
	assert.Equal(t, "playwright", detail.Framework)
	assert.Equal(t, "gtms/automation/specs/tc-007-checkout-guest.spec.ts", detail.ArtefactPath)
	assert.Equal(t, "results/junit/tc-007.xml", detail.LastRunPath)
	assert.Contains(t, detail.Tags, "checkout")
	assert.Contains(t, detail.Tags, "guest")
	assert.Contains(t, detail.Tags, "smoke")
}

func TestPipelineDetail_NotFound(t *testing.T) {
	root := setupFixtureProject(t)

	detail, err := PipelineDetail(root, "tc-nonexistent", "", false)
	assert.Error(t, err)
	assert.Nil(t, detail)
	assert.Contains(t, err.Error(), "not found")
}

func TestPipelineDetail_NoAutomation(t *testing.T) {
	root := setupFixtureProject(t)

	detail, err := PipelineDetail(root, "tc-009", "", false)
	require.NoError(t, err)
	require.NotNil(t, detail)

	assert.Equal(t, "tc-009", detail.TestCaseID)
	assert.Equal(t, "Login - SSO", detail.Title)
	assert.Equal(t, "complete", detail.CreateStatus)
	assert.Equal(t, "pending", detail.AutomateStatus) // pending task exists
	assert.Equal(t, "none", detail.ExecuteStatus)
	assert.Equal(t, "none", detail.LastResult)
	assert.Empty(t, detail.Framework)
}

// TestPipelineDetail_CarriesLogAndLogSpill verifies ENH-077: the detail
// entry carries the diagnostic log payload through from the automation
// record so the CLI renderer can display it for fail/error outcomes.
func TestPipelineDetail_CarriesLogAndLogSpill(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")

	writeFile(t, root, filepath.Join("gtms/cases", "tc-logdet.md"), `---
test_case_id: tc-logdet
title: Detail log carrier
requirement: ENH-077
---
`)
	// CON-023: notes flow from the handoff `log:` field. The spill field
	// is no longer transported through the overlay path (the result
	// contract has `log:` only — no log-spill counterpart). The spill
	// assertion is retired; the log/notes carrier is still verified.
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-logdet",
		Framework: "bats",
		Artefact:  "gtms/automation/specs/tc-logdet.bats",
		Result:    "fail",
		Notes:     "not ok 1 - expected 1 got 0\n# in tc-logdet.bats line 12\n",
	})
	writeFile(t, root, filepath.Join("gtms/automation", "specs", "tc-logdet.bats"), "# stub")

	detail, err := PipelineDetail(root, "tc-logdet", "", false)
	require.NoError(t, err)
	require.NotNil(t, detail)

	assert.Equal(t, "fail", detail.LastResult)
	assert.Contains(t, detail.Notes, "not ok 1 - expected 1 got 0")
	assert.Contains(t, detail.Notes, "# in tc-logdet.bats line 12")
}

// --- Gaps tests ---

func TestGaps_FullFixture(t *testing.T) {
	root := setupFixtureProject(t)

	report, err := Gaps(root, nil, "", false)
	require.NoError(t, err)
	require.NotNil(t, report)

	// NoTests: we don't have an external requirements list, so this should be empty
	assert.Empty(t, report.NoTests)

	// NoAutomation: tc-009 has no automation record (BUG-015: now checks records, not specs)
	require.Len(t, report.NoAutomation, 1)
	assert.Equal(t, "tc-009", report.NoAutomation[0].ID)
	assert.Equal(t, "Login - SSO", report.NoAutomation[0].Title)

	// CON-023: NeverExecuted is retired ("not run here" is the expected
	// state on a fresh clone, not a gap). tc-008 is wired-but-not-executed
	// which now surfaces only via the PipelineEntry overlay, not in gaps.

	// CurrentlyFailing: none in our fixtures (tc-007 passes)
	assert.Empty(t, report.CurrentlyFailing)

	// CON-023: SpecButNoRecord is retired — "spec exists but no automation
	// record" was a transient state in the legacy model that wiring
	// eliminates (wiring IS the record). The category no longer ships.
}

func TestGaps_WithFailingTest(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")

	writeFile(t, root, filepath.Join("gtms/cases", "tc-001.md"), `---
test_case_id: tc-001
title: Failing Test
requirement: REQ-1
status: automated
---
`)

	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-001",
		Framework: "playwright",
		Adapter:   "local-claude",
		Artefact:  "gtms/automation/specs/tc-001.spec.ts",
		Result:    "fail",
		Attempts:  3,
	})

	// Add spec file so tc-001 is not in NoAutomation
	writeFile(t, root, filepath.Join("gtms/automation", "specs", "tc-001.spec.ts"),
		`test('tc-001: Failing test', () => {});`)

	report, err := Gaps(root, nil, "", false)
	require.NoError(t, err)

	// Should not be in NoAutomation (it has automation)
	assert.Empty(t, report.NoAutomation)

	// CON-023: NeverExecuted is retired. It has a terminal handoff anyway,
	// so this scenario lands in CurrentlyFailing below.

	// Should be in CurrentlyFailing
	require.Len(t, report.CurrentlyFailing, 1)
	assert.Equal(t, "tc-001", report.CurrentlyFailing[0].ID)
	assert.Equal(t, "Failing Test", report.CurrentlyFailing[0].Title)
}

func TestGaps_EmptyProject(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")

	report, err := Gaps(root, nil, "", false)
	require.NoError(t, err)
	require.NotNil(t, report)

	assert.Empty(t, report.NoTests)
	assert.Empty(t, report.NoAutomation)
	// NeverExecuted is retired (CON-023).
	assert.Empty(t, report.CurrentlyFailing)
	assert.Equal(t, 0, report.TotalGaps())
}

func TestGaps_TotalGaps(t *testing.T) {
	root := setupFixtureProject(t)

	report, err := Gaps(root, nil, "", false)
	require.NoError(t, err)

	// CON-023: NeverExecuted retired. Only the no-automation gap (tc-009)
	// remains; tc-008 is wired-but-not-executed which is no longer a gap.
	assert.Equal(t, 1, report.TotalGaps())
}

// --- BUG-009 regression tests ---

func TestBUG009_GapsScansSpecFiles(t *testing.T) {
	// BUG-015 changed Category 2 to use automation records instead of spec files.
	// A TC with a spec file but NO automation record IS now in NoAutomation (consistent
	// with status command) AND in SpecButNoRecord (new category).
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("gtms/cases", "tc-001.md"), `---
test_case_id: tc-001
title: Has Spec But No Record
requirement: REQ-1
status: ready
---
`)
	// Spec file references tc-001
	writeFile(t, root, filepath.Join("gtms/automation", "specs", "playwright", "tc-001.spec.ts"),
		`test('tc-001: Has spec', () => {});`)

	report, err := Gaps(root, nil, "", false)
	require.NoError(t, err)

	// tc-001 has no automation record, so it IS in NoAutomation (BUG-015)
	require.Len(t, report.NoAutomation, 1)
	assert.Equal(t, "tc-001", report.NoAutomation[0].ID)

	// CON-023: SpecButNoRecord category retired. The "spec exists but no
	// wiring" condition still surfaces via NoAutomation above; the
	// secondary signal is no longer rendered.
}

func TestBUG009_RecordExistsButNoSpec(t *testing.T) {
	// BUG-015 changed Category 2 to use automation records instead of spec files.
	// A TC with an automation record but NO spec file is NOT in NoAutomation (has record).
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("gtms/cases", "tc-001.md"), `---
test_case_id: tc-001
title: Has Record But No Spec
requirement: REQ-1
status: ready
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-001",
		Framework: "playwright",
		Adapter:   "local-claude",
		Artefact:  "gtms/automation/specs/tc-001.spec.ts",
		Attempts:  1,
	})

	// No spec file exists.
	report, err := Gaps(root, nil, "", false)
	require.NoError(t, err)

	// tc-001 has an automation record, so NOT in NoAutomation (BUG-015)
	assert.Empty(t, report.NoAutomation)

	// CON-023: SpecButNoRecord category retired.
}

// --- BUG-015 regression tests ---

func TestBUG015_GapsConsistentWithStatus(t *testing.T) {
	// A test case with spec coverage but no automation record should appear
	// in both NoAutomation (consistent with status) and SpecButNoRecord.
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("gtms/cases", "tc-001.md"), `---
test_case_id: tc-001
title: Has Spec No Record
requirement: REQ-1
status: ready
---
`)
	// Spec file references tc-001 but no automation record exists
	writeFile(t, root, filepath.Join("gtms/automation", "specs", "tc-001.spec.ts"),
		`test('tc-001: Has spec', () => {});`)

	report, err := Gaps(root, nil, "", false)
	require.NoError(t, err)

	// NoAutomation: no record means not automated (consistent with status command)
	require.Len(t, report.NoAutomation, 1)
	assert.Equal(t, "tc-001", report.NoAutomation[0].ID)

	// CON-023: SpecButNoRecord category retired (see ENH-146 §"Gap categories rebuilt").

	// Verify status also shows no automation for the same test case
	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "none", entries[0].AutomateStatus,
		"Status and gaps should agree: no automation record = not automated")
}

func TestBUG015_SpecAndRecordNotInSpecButNoRecord(t *testing.T) {
	// A test case with both spec file and automation record should NOT
	// appear in SpecButNoRecord.
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("gtms/cases", "tc-001.md"), `---
test_case_id: tc-001
title: Has Both Spec And Record
requirement: REQ-1
status: ready
---
`)
	writeFile(t, root, filepath.Join("gtms/automation", "specs", "tc-001.spec.ts"),
		`test('tc-001: Has spec', () => {});`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-001",
		Framework: "playwright",
		Adapter:   "local-claude",
		Artefact:  "gtms/automation/specs/tc-001.spec.ts",
		Attempts:  1,
	})

	report, err := Gaps(root, nil, "", false)
	require.NoError(t, err)

	// Has record: not in NoAutomation
	assert.Empty(t, report.NoAutomation)

	// CON-023: SpecButNoRecord category retired.
}

func TestBUG015_NoSpecNoRecordNotInSpecButNoRecord(t *testing.T) {
	// A test case with neither spec file nor automation record should appear
	// in NoAutomation but NOT in SpecButNoRecord.
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("gtms/cases", "tc-001.md"), `---
test_case_id: tc-001
title: Has Nothing
requirement: REQ-1
status: ready
---
`)

	report, err := Gaps(root, nil, "", false)
	require.NoError(t, err)

	// No record: in NoAutomation
	require.Len(t, report.NoAutomation, 1)
	assert.Equal(t, "tc-001", report.NoAutomation[0].ID)

	// CON-023: SpecButNoRecord category retired.
}

// --- Triage backward-compat test ---

func TestTriage_NoAutomationRecord(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")

	info, err := Triage(root, "tc-none")
	assert.Nil(t, info)
	assert.Error(t, err)
	// CON-023 / ENH-145: triage now reads wiring; error wording reflects that.
	assert.Contains(t, err.Error(), "no wiring record found")
}

// --- Edge cases ---

func TestPipelineStatus_NoMdFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	// Create cases dir with a non-md file
	writeFile(t, root, filepath.Join("gtms/cases", "README.txt"), "Not a test case")

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestPipelineStatus_EmptyFrontmatter(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	// File with valid frontmatter but empty ID
	writeFile(t, root, filepath.Join("gtms/cases", "empty.md"), `---
test_case_id: ""
title: Empty ID
---
`)

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	// Empty ID should be skipped
	assert.Empty(t, entries)
}

func TestPipelineStatus_NestedTestCases(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	// Test cases in nested subdirectories
	writeFile(t, root, filepath.Join("gtms/cases", "checkout", "tc-001.md"), `---
test_case_id: tc-001
title: Nested Checkout
requirement: REQ-1
status: ready
---
`)
	writeFile(t, root, filepath.Join("gtms/cases", "login", "tc-002.md"), `---
test_case_id: tc-002
title: Nested Login
requirement: REQ-2
status: ready
---
`)

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, "tc-001", entries[0].TestCaseID)
	assert.Equal(t, "tc-002", entries[1].TestCaseID)
}

func TestScanAutomationRecords_NonAutomationFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	// CON-023: reader now scans gtms/automation/wiring/ — files that
	// don't end with .wiring.yaml must be skipped.
	writeFile(t, root, filepath.Join("gtms/automation", "wiring", "README.md"), "# Wiring")
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-001",
		Framework: "playwright",
		Result:    "pass",
	})

	records, err := scanAutomationRecords(root)
	require.NoError(t, err)
	assert.Len(t, records, 1)
	assert.Contains(t, records, "tc-001")
}

// --- BUG-011 regression tests ---

func TestBUG011_StatusParsesTestCaseID(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("gtms/cases", "tc-bug011-test.md"), `---
test_case_id: tc-bug011
title: BUG-011 Regression Test
requirement: BUG-011
priority: High
type: Functional
created: 2026-02-21
---

## Steps
1. Verify frontmatter parsing
`)

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "tc-bug011", entries[0].TestCaseID)
	assert.Equal(t, "BUG-011 Regression Test", entries[0].Title)
	assert.Equal(t, "complete", entries[0].CreateStatus)
}

func TestBUG011_StatusNormalizesUppercaseID(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("gtms/cases", "tc-upper-test.md"), `---
test_case_id: TC-UPPER
title: Uppercase ID Test
requirement: BUG-011
---
`)

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "tc-upper", entries[0].TestCaseID, "ID should be normalized to lowercase")
}

func TestBUG011_DetailFindsTestCaseID(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("gtms/cases", "tc-bug011-detail.md"), `---
test_case_id: tc-bug011
title: BUG-011 Detail Test
requirement: BUG-011
priority: Medium
type: Integration
created: 2026-02-21
---
`)

	detail, err := PipelineDetail(root, "tc-bug011", "", false)
	require.NoError(t, err)
	require.NotNil(t, detail)
	assert.Equal(t, "tc-bug011", detail.TestCaseID)
	assert.Equal(t, "BUG-011 Detail Test", detail.Title)
	assert.Equal(t, "BUG-011", detail.Requirement)
}

// --- Scoped scanning tests (ENH-036) ---

// setupSubfolderFixture creates a project with test cases in subfolders.
func setupSubfolderFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")

	// Root-level test case
	writeFile(t, root, filepath.Join("gtms/cases", "tc-root.md"), `---
test_case_id: tc-root
title: Root Level
requirement: REQ-ROOT
---
`)

	// Login subfolder
	writeFile(t, root, filepath.Join("gtms/cases", "login", "tc-login1.md"), `---
test_case_id: tc-login1
title: Login Happy
requirement: REQ-LOGIN
---
`)
	writeFile(t, root, filepath.Join("gtms/cases", "login", "tc-login2.md"), `---
test_case_id: tc-login2
title: Login Error
requirement: REQ-LOGIN
---
`)

	// Nested subfolder under login
	writeFile(t, root, filepath.Join("gtms/cases", "login", "oauth", "tc-oauth1.md"), `---
test_case_id: tc-oauth1
title: OAuth Flow
requirement: REQ-LOGIN
---
`)

	// Payments subfolder
	writeFile(t, root, filepath.Join("gtms/cases", "payments", "tc-pay1.md"), `---
test_case_id: tc-pay1
title: Payment Flow
requirement: REQ-PAY
---
`)

	// Nested refunds under payments
	writeFile(t, root, filepath.Join("gtms/cases", "payments", "refunds", "tc-refund1.md"), `---
test_case_id: tc-refund1
title: Refund Flow
requirement: REQ-PAY
---
`)

	return root
}

func TestPipelineStatus_ShallowAtRoot(t *testing.T) {
	root := setupSubfolderFixture(t)
	tcDir := filepath.Join(root, "gtms/cases")

	scope := &ScopeInfo{ScanDir: tcDir, RelPath: "gtms/cases/", Recursive: false}
	entries, err := PipelineStatus(root, scope, "", false)
	require.NoError(t, err)

	// Shallow at gtms/cases/ root: only tc-root (no subdirectory test cases)
	require.Len(t, entries, 1)
	assert.Equal(t, "tc-root", entries[0].TestCaseID)
}

func TestPipelineStatus_RecursiveAtRoot(t *testing.T) {
	root := setupSubfolderFixture(t)
	tcDir := filepath.Join(root, "gtms/cases")

	scope := &ScopeInfo{ScanDir: tcDir, RelPath: "gtms/cases/", Recursive: true}
	entries, err := PipelineStatus(root, scope, "", false)
	require.NoError(t, err)

	// Recursive at gtms/cases/ root: all 6 test cases
	assert.Len(t, entries, 6)
}

func TestPipelineStatus_ShallowAtLogin(t *testing.T) {
	root := setupSubfolderFixture(t)
	loginDir := filepath.Join(root, "gtms/cases", "login")

	scope := &ScopeInfo{ScanDir: loginDir, RelPath: "gtms/cases/login/", Recursive: false}
	entries, err := PipelineStatus(root, scope, "", false)
	require.NoError(t, err)

	// Shallow at login: tc-login1, tc-login2 (NOT tc-oauth1)
	require.Len(t, entries, 2)

	ids := []string{entries[0].TestCaseID, entries[1].TestCaseID}
	assert.Contains(t, ids, "tc-login1")
	assert.Contains(t, ids, "tc-login2")
}

func TestPipelineStatus_RecursiveAtLogin(t *testing.T) {
	root := setupSubfolderFixture(t)
	loginDir := filepath.Join(root, "gtms/cases", "login")

	scope := &ScopeInfo{ScanDir: loginDir, RelPath: "gtms/cases/login/", Recursive: true}
	entries, err := PipelineStatus(root, scope, "", false)
	require.NoError(t, err)

	// Recursive at login: tc-login1, tc-login2, tc-oauth1
	assert.Len(t, entries, 3)
}

func TestPipelineStatus_ShallowExcludesSiblings(t *testing.T) {
	root := setupSubfolderFixture(t)
	loginDir := filepath.Join(root, "gtms/cases", "login")

	scope := &ScopeInfo{ScanDir: loginDir, RelPath: "gtms/cases/login/", Recursive: false}
	entries, err := PipelineStatus(root, scope, "", false)
	require.NoError(t, err)

	// Must NOT contain tc-pay1, tc-refund1, tc-root
	for _, e := range entries {
		assert.NotEqual(t, "tc-pay1", e.TestCaseID)
		assert.NotEqual(t, "tc-refund1", e.TestCaseID)
		assert.NotEqual(t, "tc-root", e.TestCaseID)
	}
}

func TestPipelineStatus_NilScopeIsUnscoped(t *testing.T) {
	root := setupSubfolderFixture(t)

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)

	// All 6 test cases (backward compat)
	assert.Len(t, entries, 6)
}

func TestPipelineStatus_EmptySubfolder(t *testing.T) {
	root := setupSubfolderFixture(t)
	emptyDir := filepath.Join(root, "gtms/cases", "empty")
	mkdirAll(t, root, filepath.Join("gtms/cases", "empty"))

	scope := &ScopeInfo{ScanDir: emptyDir, RelPath: "gtms/cases/empty/", Recursive: false}
	entries, err := PipelineStatus(root, scope, "", false)
	require.NoError(t, err)
	assert.Len(t, entries, 0)
}

func TestGaps_ScopedToLogin(t *testing.T) {
	root := setupSubfolderFixture(t)
	loginDir := filepath.Join(root, "gtms/cases", "login")

	scope := &ScopeInfo{ScanDir: loginDir, RelPath: "gtms/cases/login/", Recursive: false}
	report, err := Gaps(root, scope, "", false)
	require.NoError(t, err)

	// Only login TCs should appear in gap categories
	for _, entry := range report.NoAutomation {
		assert.True(t, entry.ID == "tc-login1" || entry.ID == "tc-login2",
			"unexpected TC in NoAutomation: %s", entry.ID)
	}
}

func TestMap_ScopedToLogin(t *testing.T) {
	root := setupSubfolderFixture(t)
	loginDir := filepath.Join(root, "gtms/cases", "login")

	scope := &ScopeInfo{ScanDir: loginDir, RelPath: "gtms/cases/login/", Recursive: false}
	report, err := Map(root, scope, "", false)
	require.NoError(t, err)

	// Only login TCs in groups
	totalTCs := 0
	for _, grp := range report.Groups {
		totalTCs += len(grp.TestCases)
	}
	totalTCs += len(report.Unlinked)
	assert.Equal(t, 2, totalTCs, "should only have tc-login1 and tc-login2")
}

// --- isStaleArtefact unit tests (TREV-004 gap #1) ---

func TestIsStaleArtefact_HashMismatch(t *testing.T) {
	root := t.TempDir()

	// Create artefact file
	writeFile(t, root, filepath.Join("tests", "tc-stale1-test.bats"), "original content")

	ar := automationFrontmatter{
		TestCase:     "tc-stale1",
		Artefact:     "tests/tc-stale1-test.bats",
		ArtefactHash: "0000000000000000", // deliberately wrong
	}

	assert.True(t, isStaleArtefact(root, ar), "should be stale when hash differs")
}

func TestIsStaleArtefact_HashMatches(t *testing.T) {
	root := t.TempDir()

	content := "matching content"
	writeFile(t, root, filepath.Join("tests", "tc-fresh1-test.bats"), content)

	// Compute the real hash
	realHash, err := pipeline.HashFile(filepath.Join(root, "tests", "tc-fresh1-test.bats"))
	require.NoError(t, err)

	ar := automationFrontmatter{
		TestCase:     "tc-fresh1",
		Artefact:     "tests/tc-fresh1-test.bats",
		ArtefactHash: realHash,
	}

	assert.False(t, isStaleArtefact(root, ar), "should not be stale when hash matches")
}

func TestIsStaleArtefact_EmptyHash(t *testing.T) {
	root := t.TempDir()

	ar := automationFrontmatter{
		TestCase:     "tc-nohash1",
		Artefact:     "tests/tc-nohash1-test.bats",
		ArtefactHash: "", // no hash stored (pre-existing record)
	}

	assert.False(t, isStaleArtefact(root, ar), "should not be stale when no hash stored")
}

func TestIsStaleArtefact_FileMissing(t *testing.T) {
	root := t.TempDir()

	ar := automationFrontmatter{
		TestCase:     "tc-gone1",
		Artefact:     "tests/tc-gone1-test.bats", // file doesn't exist
		ArtefactHash: "abcdef1234567890",
	}

	assert.False(t, isStaleArtefact(root, ar), "should not be stale when file is missing")
}

// --- MapEntry.Stale unit tests (TREV-004 gap #1) ---

func TestMap_StaleFlag_WhenHashMismatch(t *testing.T) {
	root := t.TempDir()

	writeFile(t, root, filepath.Join("gtms/cases", "tc-map01-stale.md"), `---
test_case_id: tc-map01
title: Stale Map Test
requirement: REQ-S
---
`)
	writeFile(t, root, filepath.Join("tests", "tc-map01-stale.bats"), "artefact content")
	seedLegacyRecord(t, root, legacyRecord{
		TC:           "tc-map01",
		Framework:    "bats",
		Artefact:     "tests/tc-map01-stale.bats",
		ArtefactHash: "0000000000000000", // intentionally wrong
		Result:       "pass",
	})

	report, err := Map(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, report.Groups, 1)

	entry := report.Groups[0].TestCases[0]
	assert.True(t, entry.Stale, "map entry should be stale when hash mismatches")
}

func TestMap_StaleFlag_WhenHashMatches(t *testing.T) {
	root := t.TempDir()

	content := "fresh artefact content"
	writeFile(t, root, filepath.Join("gtms/cases", "tc-map02-fresh.md"), `---
test_case_id: tc-map02
title: Fresh Map Test
requirement: REQ-F
---
`)
	writeFile(t, root, filepath.Join("tests", "tc-map02-fresh.bats"), content)

	realHash, err := pipeline.HashFile(filepath.Join(root, "tests", "tc-map02-fresh.bats"))
	require.NoError(t, err)

	seedLegacyRecord(t, root, legacyRecord{
		TC:           "tc-map02",
		Framework:    "bats",
		Artefact:     "tests/tc-map02-fresh.bats",
		ArtefactHash: realHash,
		Result:       "pass",
	})

	report, err := Map(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, report.Groups, 1)

	entry := report.Groups[0].TestCases[0]
	assert.False(t, entry.Stale, "map entry should not be stale when hash matches")
}

// --- ENH-040: Error vs Fail distinction tests ---

func TestENH040_PipelineStatus_ErrorExecuteStatus(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("gtms/cases", "tc-e40a-error-test.md"), `---
test_case_id: tc-e40a
title: Error Execute Test
requirement: REQ-1
---
`)
	// Terminal handoff carrying result: error.
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-e40a",
		Framework: "bats",
		Artefact:  "gtms/automation/specs/tc-e40a.bats",
		Result:    "error",
		Attempts:  1,
	})

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	assert.Equal(t, "error", entries[0].ExecuteStatus, "error last-formal-result should set ExecuteStatus to 'error'")
	assert.Equal(t, "error", entries[0].LastResult)
}

func TestENH040_PipelineStatus_FailExecuteStatus(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("gtms/cases", "tc-e40b-fail-test.md"), `---
test_case_id: tc-e40b
title: Fail Execute Test
requirement: REQ-1
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-e40b",
		Framework: "bats",
		Artefact:  "gtms/automation/specs/tc-e40b.bats",
		Result:    "fail",
		Attempts:  1,
	})

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	assert.Equal(t, "complete", entries[0].ExecuteStatus, "fail last-formal-result should set ExecuteStatus to 'complete'")
	assert.Equal(t, "fail", entries[0].LastResult)
}

func TestENH040_PipelineDetail_ErrorExecuteStatus(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("gtms/cases", "tc-e40c-detail-error.md"), `---
test_case_id: tc-e40c
title: Detail Error Test
requirement: REQ-1
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-e40c",
		Framework: "bats",
		Artefact:  "gtms/automation/specs/tc-e40c.bats",
		Result:    "error",
		Attempts:  1,
	})

	detail, err := PipelineDetail(root, "tc-e40c", "", false)
	require.NoError(t, err)
	require.NotNil(t, detail)

	assert.Equal(t, "error", detail.ExecuteStatus, "error last-formal-result should set ExecuteStatus to 'error' in detail view")
	assert.Equal(t, "error", detail.LastResult)
}

func TestENH040_Gaps_ExecutionErrors_Category(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")

	// tc-e40d has error result
	writeFile(t, root, filepath.Join("gtms/cases", "tc-e40d-error-gap.md"), `---
test_case_id: tc-e40d
title: Error Gap Test
requirement: REQ-1
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-e40d",
		Framework: "bats",
		Artefact:  "gtms/automation/specs/tc-e40d.bats",
		Result:    "error",
		Attempts:  1,
	})

	// tc-e40e has fail result
	writeFile(t, root, filepath.Join("gtms/cases", "tc-e40e-fail-gap.md"), `---
test_case_id: tc-e40e
title: Fail Gap Test
requirement: REQ-1
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-e40e",
		Framework: "bats",
		Artefact:  "gtms/automation/specs/tc-e40e.bats",
		Result:    "fail",
		Attempts:  1,
	})

	report, err := Gaps(root, nil, "", false)
	require.NoError(t, err)

	// tc-e40d should be in ExecutionErrors, NOT in CurrentlyFailing
	assert.Len(t, report.ExecutionErrors, 1, "error result should be in ExecutionErrors")
	assert.Equal(t, "tc-e40d", report.ExecutionErrors[0].ID)

	// tc-e40e should be in CurrentlyFailing, NOT in ExecutionErrors
	assert.Len(t, report.CurrentlyFailing, 1, "fail result should be in CurrentlyFailing")
	assert.Equal(t, "tc-e40e", report.CurrentlyFailing[0].ID)
}

func TestENH040_Gaps_TotalGaps_IncludesErrors(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")

	writeFile(t, root, filepath.Join("gtms/cases", "tc-e40f-total-gaps.md"), `---
test_case_id: tc-e40f
title: Total Gaps Error Test
requirement: REQ-1
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-e40f",
		Framework: "bats",
		Artefact:  "gtms/automation/specs/tc-e40f.bats",
		Result:    "error",
		Attempts:  1,
	})

	report, err := Gaps(root, nil, "", false)
	require.NoError(t, err)

	assert.Equal(t, 1, report.TotalGaps(), "ExecutionErrors should be counted in TotalGaps")
}

func TestENH040_Map_ErrorExecuteStatus(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("gtms/cases", "tc-e40g-map-error.md"), `---
test_case_id: tc-e40g
title: Map Error Test
requirement: REQ-1
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:        "tc-e40g",
		Framework: "bats",
		Artefact:  "gtms/automation/specs/tc-e40g.bats",
		Result:    "error",
		Attempts:  1,
	})

	report, err := Map(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, report.Groups, 1)

	entry := report.Groups[0].TestCases[0]
	assert.Equal(t, "error", entry.ExecuteStatus, "error last-formal-result should set ExecuteStatus to 'error' in map view")
	assert.Equal(t, "error", entry.LastResult)
}

// --- BUG-044 regression tests ---

func TestBUG044_StaleFailedTaskSupersededByPassingRecord(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("gtms/cases", "tc-001.md"), `---
test_case_id: tc-001
title: Stale failure with passing record
requirement: REQ-1
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:         "tc-001",
		Framework:  "bats",
		Artefact:   "gtms/automation/specs/tc-001.bats",
		Result:     "pass",
		ExecutedAt: "2026-04-19T14:00:00Z",
		Attempts:   2,
	})
	// Older failed task at T1 (no completed task sibling)
	writeFile(t, root, filepath.Join("gtms/tasks", "error", "task-aaa11111-execute-tc-001.md"), `---
id: task-aaa11111
type: execute
target: tc-001
adapter: bats-runner
status: error
created: "2026-04-19T13:00:00Z"
branch: main
---
`)

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	assert.Equal(t, "complete", entries[0].ExecuteStatus,
		"Passing record should supersede stale failed task")
	assert.Equal(t, "pass", entries[0].LastResult)
}

func TestBUG044_StaleFailedTaskSupersededBySkippedRecord(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("gtms/cases", "tc-001.md"), `---
test_case_id: tc-001
title: Stale failure with skipped record
requirement: REQ-1
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:         "tc-001",
		Framework:  "bats",
		Artefact:   "gtms/automation/specs/tc-001.bats",
		Result:     "skipped",
		ExecutedAt: "2026-04-19T14:00:00Z",
		Attempts:   1,
	})
	// Older failed task at T1
	writeFile(t, root, filepath.Join("gtms/tasks", "error", "task-bbb22222-execute-tc-001.md"), `---
id: task-bbb22222
type: execute
target: tc-001
adapter: bats-runner
status: error
created: "2026-04-19T13:00:00Z"
branch: main
---
`)

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	assert.Equal(t, "skipped", entries[0].ExecuteStatus,
		"Skipped record should supersede stale failed task")
	assert.Equal(t, "skipped", entries[0].LastResult)
}

func TestBUG044_GenuineFailureNewerThanRecord(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("gtms/cases", "tc-001.md"), `---
test_case_id: tc-001
title: Genuine failure newer than record
requirement: REQ-1
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:         "tc-001",
		Framework:  "bats",
		Artefact:   "gtms/automation/specs/tc-001.bats",
		Result:     "pass",
		ExecutedAt: "2026-04-19T12:00:00Z",
		Attempts:   2,
	})
	// Newer failed task at T2 — genuine failure
	writeFile(t, root, filepath.Join("gtms/tasks", "error", "task-ccc33333-execute-tc-001.md"), `---
id: task-ccc33333
type: execute
target: tc-001
adapter: bats-runner
status: error
created: "2026-04-19T14:00:00Z"
branch: main
---
`)

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	assert.Equal(t, "error", entries[0].ExecuteStatus,
		"Genuine failure newer than last record should still show failed")
}

func TestBUG044_NoLastRunAtPreservesExistingBehaviour(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("gtms/cases", "tc-001.md"), `---
test_case_id: tc-001
title: No last-formal-run-at on record
requirement: REQ-1
---
`)
	// CON-023: the new overlay always carries a `completed:` timestamp,
	// so the original "missing last-formal-run-at" scenario cannot be
	// modelled directly. Place the record's executed-at BEFORE the
	// failed task so the BUG-044 record-supersedes-task bypass does NOT
	// fire — preserving the underlying invariant the test guards:
	// "if the failed task is genuinely newer, it overrides."
	seedLegacyRecord(t, root, legacyRecord{
		TC:         "tc-001",
		Framework:  "bats",
		Artefact:   "gtms/automation/specs/tc-001.bats",
		Result:     "pass",
		ExecutedAt: "2026-04-19T12:00:00Z",
		Attempts:   1,
	})
	// Failed task
	writeFile(t, root, filepath.Join("gtms/tasks", "error", "task-ddd44444-execute-tc-001.md"), `---
id: task-ddd44444
type: execute
target: tc-001
adapter: bats-runner
status: error
created: "2026-04-19T13:00:00Z"
branch: main
---
`)

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	assert.Equal(t, "error", entries[0].ExecuteStatus,
		"Without last-formal-run-at, failed task should still win (backward compat)")
}

func TestBUG044_DetailViewAlsoReconciles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("gtms/cases", "tc-001.md"), `---
test_case_id: tc-001
title: Detail view reconciliation
requirement: REQ-1
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:         "tc-001",
		Framework:  "bats",
		Artefact:   "gtms/automation/specs/tc-001.bats",
		Result:     "pass",
		ExecutedAt: "2026-04-19T14:00:00Z",
		Attempts:   2,
	})
	// Older failed task at T1
	writeFile(t, root, filepath.Join("gtms/tasks", "error", "task-eee55555-execute-tc-001.md"), `---
id: task-eee55555
type: execute
target: tc-001
adapter: bats-runner
status: error
created: "2026-04-19T13:00:00Z"
branch: main
---
`)

	detail, err := PipelineDetail(root, "tc-001", "", false)
	require.NoError(t, err)
	require.NotNil(t, detail)

	assert.Equal(t, "complete", detail.ExecuteStatus,
		"Detail view should also reconcile stale failed task against passing record")
	assert.Equal(t, "pass", detail.LastResult)
}

// Regression: when the automation record's own result is fail/error, the
// record is NOT authoritative for suppressing the failed-task override.
// If the bypass fires unconditionally on LastRunAt>=failedCreated, the
// detail view renders "Fail"/"Error" instead of the "Failed" label that
// formatExecuteLabel emits only when ExecuteStatus=="error".
// Caught by CI (tc-39badd97 / tc-00a43f39 / tc-ca36470c / tc-e3b7c650).
func TestBUG044_RecordSaysFailDoesNotSuppressFailedOverride(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("gtms/cases", "tc-001.md"), `---
test_case_id: tc-001
title: Fail record + stale failed task
requirement: REQ-1
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:         "tc-001",
		Framework:  "bats",
		Artefact:   "gtms/automation/specs/tc-001.bats",
		Result:     "fail",
		ExecutedAt: "2026-04-19T14:00:00Z",
		Attempts:   1,
	})
	// Older failed task at T1
	writeFile(t, root, filepath.Join("gtms/tasks", "error", "task-fff66666-execute-tc-001.md"), `---
id: task-fff66666
type: execute
target: tc-001
adapter: bats-runner
status: error
created: "2026-04-19T13:00:00Z"
branch: main
---
`)

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	assert.Equal(t, "error", entries[0].ExecuteStatus,
		"Record result=fail must not trigger the BUG-044 bypass; ExecuteStatus must be 'failed' so the detail view renders 'Failed'")
	assert.Equal(t, "fail", entries[0].LastResult)
}

func TestBUG044_RecordSaysErrorDoesNotSuppressFailedOverride(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("gtms/cases", "tc-001.md"), `---
test_case_id: tc-001
title: Error record + stale failed task
requirement: REQ-1
---
`)
	seedLegacyRecord(t, root, legacyRecord{
		TC:         "tc-001",
		Framework:  "bats",
		Artefact:   "gtms/automation/specs/tc-001.bats",
		Result:     "error",
		ExecutedAt: "2026-04-19T14:00:00Z",
		Attempts:   1,
	})
	// Older failed task at T1
	writeFile(t, root, filepath.Join("gtms/tasks", "error", "task-ggg77777-execute-tc-001.md"), `---
id: task-ggg77777
type: execute
target: tc-001
adapter: bats-runner
status: error
created: "2026-04-19T13:00:00Z"
branch: main
---
`)

	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	assert.Equal(t, "error", entries[0].ExecuteStatus,
		"Record result=error must not trigger the BUG-044 bypass; ExecuteStatus must be 'failed' so the detail view renders 'Failed'")
	assert.Equal(t, "error", entries[0].LastResult)
}
