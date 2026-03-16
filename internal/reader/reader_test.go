package reader

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	writeFile(t, root, filepath.Join("test-cases", "natural", "tc-007-checkout-guest.md"), `---
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

	writeFile(t, root, filepath.Join("test-cases", "natural", "tc-008-checkout-registered.md"), `---
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

	writeFile(t, root, filepath.Join("test-cases", "natural", "tc-009-login-sso.md"), `---
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
	writeFile(t, root, filepath.Join("test-automation", "specs", "playwright", "checkout.spec.ts"),
		"test('tc-007: Checkout guest', () => {});\ntest('tc-008: Checkout registered', () => {});")

	// Create automation records
	writeFile(t, root, filepath.Join("test-automation", "records", "tc-007.automation.md"), `---
testcase: tc-007
framework: playwright
status: accepted
artefact: test-automation/specs/tc-007-checkout-guest.spec.ts
adapter: local-claude
last-dev-result: pass
last-formal-result: pass
last-formal-run: results/junit/tc-007.xml
attempts: 2
cycle: 1
---

## Automation Notes
Automated checkout flow for guest users.
`)

	writeFile(t, root, filepath.Join("test-automation", "records", "tc-008.automation.md"), `---
testcase: tc-008
framework: playwright
status: accepted
artefact: test-automation/specs/tc-008-checkout-registered.spec.ts
adapter: local-claude
last-dev-result: pass
last-formal-result: ""
last-formal-run: ""
attempts: 1
cycle: 1
---

## Automation Notes
Automated checkout flow for registered users.
`)

	// Create task files
	writeFile(t, root, filepath.Join("test-tasks", "pending", "task-a1b2c3d-automate-tc-009.md"), `---
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

	writeFile(t, root, filepath.Join("test-tasks", "complete", "task-e4f5a6b-create-JIRA-456.md"), `---
id: task-e4f5a6b
type: create
target: JIRA-456
adapter: local-claude
status: complete
created: 2025-02-10T10:00:00Z
branch: feature/create-JIRA-456
---
`)

	writeFile(t, root, filepath.Join("test-tasks", "complete", "task-c7d8e9f-automate-tc-007.md"), `---
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

	entries, err := PipelineStatus(root, nil)
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

	// tc-008: automated (accepted), never executed
	assert.Equal(t, "Checkout Flow - Registered User", entries[1].Title)
	assert.Equal(t, "checkout-registered", entries[1].Slug)
	assert.Equal(t, "complete", entries[1].CreateStatus)
	assert.Equal(t, "complete", entries[1].AutomateStatus)
	assert.Equal(t, "none", entries[1].ExecuteStatus) // no formal result
	assert.Equal(t, "none", entries[1].LastResult)

	// tc-009: created but not automated, has a pending automate task
	assert.Equal(t, "Login - SSO", entries[2].Title)
	assert.Equal(t, "login-sso", entries[2].Slug)
	assert.Equal(t, "complete", entries[2].CreateStatus)
	assert.Equal(t, "pending", entries[2].AutomateStatus) // pending task
	assert.Equal(t, "none", entries[2].ExecuteStatus)
	assert.Equal(t, "none", entries[2].LastResult)
}

func TestPipelineStatus_EmptyProject(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", `project:
  name: empty
  repo: github.com/example/empty
`)

	entries, err := PipelineStatus(root, nil)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestPipelineStatus_NoAutomationDir(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("test-cases", "tc-001.md"), `---
test_case_id: tc-001
title: Some Test
requirement: REQ-1
status: ready
---
`)

	entries, err := PipelineStatus(root, nil)
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
	writeFile(t, root, filepath.Join("test-cases", "tc-a1b2c3d-tier1-sync-happy-path.md"), `---
test_case_id: tc-a1b2c3d
title: Tier 1 Sync Happy Path
requirement: REQ-1
---
`)

	entries, err := PipelineStatus(root, nil)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "tc-a1b2c3d", entries[0].TestCaseID)
	assert.Equal(t, "tier1-sync-happy-path", entries[0].Slug)
}

func TestPipelineDetail_SlugDerived(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("test-cases", "tc-a1b2c3d-tier1-sync-happy-path.md"), `---
test_case_id: tc-a1b2c3d
title: Tier 1 Sync Happy Path
requirement: REQ-1
---
`)

	detail, err := PipelineDetail(root, "tc-a1b2c3d")
	require.NoError(t, err)
	require.NotNil(t, detail)
	assert.Equal(t, "tier1-sync-happy-path", detail.Slug)
}

func TestPipelineStatus_MalformedFrontmatter(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")

	// Valid file
	writeFile(t, root, filepath.Join("test-cases", "tc-001.md"), `---
test_case_id: tc-001
title: Valid Test
requirement: REQ-1
status: ready
---
`)

	// Malformed file - should be skipped without crashing
	writeFile(t, root, filepath.Join("test-cases", "tc-bad.md"), `---
this is not valid yaml: [
broken: {{
---
`)

	entries, err := PipelineStatus(root, nil)
	require.NoError(t, err)
	// Should have only the valid entry
	assert.Len(t, entries, 1)
	assert.Equal(t, "tc-001", entries[0].TestCaseID)
}

func TestPipelineStatus_MalformedAutomation(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("test-cases", "tc-001.md"), `---
test_case_id: tc-001
title: Valid Test
requirement: REQ-1
status: ready
---
`)
	// Malformed automation record
	writeFile(t, root, filepath.Join("test-automation", "records", "tc-001.automation.md"), `---
broken: yaml: [[[
---
`)

	entries, err := PipelineStatus(root, nil)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
	// Should still show tc-001 but without automation
	assert.Equal(t, "none", entries[0].AutomateStatus)
}

func TestPipelineStatus_MalformedTask(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("test-cases", "tc-001.md"), `---
test_case_id: tc-001
title: Valid Test
requirement: REQ-1
status: ready
---
`)
	// Malformed task
	writeFile(t, root, filepath.Join("test-tasks", "pending", "task-bad.md"), `---
broken yaml [[[
---
`)

	entries, err := PipelineStatus(root, nil)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}

func TestPipelineStatus_InProgressTask(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("test-cases", "tc-001.md"), `---
test_case_id: tc-001
title: Test Case
requirement: REQ-1
status: ready
---
`)
	writeFile(t, root, filepath.Join("test-tasks", "in-progress", "task-abc1234-automate-tc-001.md"), `---
id: task-abc1234
type: automate
target: tc-001
adapter: local-claude
status: in-progress
created: 2025-02-12T11:00:00Z
branch: feature/automate-tc-001
---
`)

	entries, err := PipelineStatus(root, nil)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "in-progress", entries[0].AutomateStatus)
}

// --- BUG-017 regression tests ---

func TestBUG017_FailedExecuteTaskShowsFailedStatus(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("test-cases", "tc-a7b9c1d-checkout.md"), `---
test_case_id: tc-a7b9c1d
title: Checkout Test
requirement: REQ-1
---
`)
	// Automation record exists (accepted, no formal result yet)
	writeFile(t, root, filepath.Join("test-automation", "records", "tc-a7b9c1d.automation.md"), `---
testcase: tc-a7b9c1d
framework: bats
status: accepted
artefact: test-automation/specs/tc-a7b9c1d.bats
adapter: bats-runner
attempts: 1
cycle: 1
---
`)
	// Failed execute task in test-tasks/failed/
	writeFile(t, root, filepath.Join("test-tasks", "failed", "task-71b7453-execute-tc-a7b9c1d.md"), `---
id: task-71b7453
type: execute
target: tc-a7b9c1d
adapter: bats-runner
status: failed
created: 2026-03-07T10:00:00Z
branch: main
---
`)

	entries, err := PipelineStatus(root, nil)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	assert.Equal(t, "tc-a7b9c1d", entries[0].TestCaseID)
	assert.Equal(t, "complete", entries[0].CreateStatus)
	assert.Equal(t, "complete", entries[0].AutomateStatus)
	assert.Equal(t, "failed", entries[0].ExecuteStatus, "Failed execute task should set ExecuteStatus to 'failed'")
	assert.Equal(t, "none", entries[0].LastResult, "No formal result should remain 'none'")
}

func TestBUG017_FailedExecuteOverridesPreviousPass(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("test-cases", "tc-001.md"), `---
test_case_id: tc-001
title: Test With Pass Then Fail
requirement: REQ-1
---
`)
	// Automation record with a passing formal result (from earlier execution)
	writeFile(t, root, filepath.Join("test-automation", "records", "tc-001.automation.md"), `---
testcase: tc-001
framework: bats
status: accepted
artefact: test-automation/specs/tc-001.bats
adapter: bats-runner
last-formal-result: pass
attempts: 2
cycle: 1
---
`)
	// A newer failed execute task — re-execution failed
	writeFile(t, root, filepath.Join("test-tasks", "failed", "task-abc1234-execute-tc-001.md"), `---
id: task-abc1234
type: execute
target: tc-001
adapter: bats-runner
status: failed
created: 2026-03-07T10:00:00Z
branch: main
---
`)

	entries, err := PipelineStatus(root, nil)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	// Failed task should override the previous pass — the latest execution didn't pass
	assert.Equal(t, "failed", entries[0].ExecuteStatus, "Failed task should override previous pass")
	assert.Equal(t, "pass", entries[0].LastResult, "LastResult still reflects automation record")
}

func TestBUG017_FailedExecuteInPipelineDetail(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("test-cases", "tc-a7b9c1d-checkout.md"), `---
test_case_id: tc-a7b9c1d
title: Checkout Test
requirement: REQ-1
---
`)
	writeFile(t, root, filepath.Join("test-automation", "records", "tc-a7b9c1d.automation.md"), `---
testcase: tc-a7b9c1d
framework: bats
status: accepted
artefact: test-automation/specs/tc-a7b9c1d.bats
adapter: bats-runner
attempts: 1
cycle: 1
---
`)
	writeFile(t, root, filepath.Join("test-tasks", "failed", "task-71b7453-execute-tc-a7b9c1d.md"), `---
id: task-71b7453
type: execute
target: tc-a7b9c1d
adapter: bats-runner
status: failed
created: 2026-03-07T10:00:00Z
branch: main
---
`)

	detail, err := PipelineDetail(root, "tc-a7b9c1d")
	require.NoError(t, err)
	require.NotNil(t, detail)

	assert.Equal(t, "failed", detail.ExecuteStatus, "Detail view should also show failed execute status")
}

// --- BUG-018 regression tests ---

func TestBUG018_NewerCompleteOverridesOlderFailed(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("test-cases", "tc-001.md"), `---
test_case_id: tc-001
title: Test With Fail Then Pass
requirement: REQ-1
---
`)
	// Automation record with passing result
	writeFile(t, root, filepath.Join("test-automation", "records", "tc-001.automation.md"), `---
testcase: tc-001
framework: bats
status: accepted
artefact: test-automation/specs/tc-001.bats
adapter: bats-runner
last-formal-result: pass
attempts: 2
cycle: 1
---
`)
	// Older failed execute task
	writeFile(t, root, filepath.Join("test-tasks", "failed", "task-old1234-execute-tc-001.md"), `---
id: task-old1234
type: execute
target: tc-001
adapter: bats-runner
status: failed
created: 2026-03-07T09:00:00Z
branch: main
---
`)
	// Newer completed execute task
	writeFile(t, root, filepath.Join("test-tasks", "complete", "task-new5678-execute-tc-001.md"), `---
id: task-new5678
type: execute
target: tc-001
adapter: bats-runner
status: complete
created: 2026-03-07T10:00:00Z
branch: main
---
`)

	entries, err := PipelineStatus(root, nil)
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
	writeFile(t, root, filepath.Join("test-cases", "tc-001.md"), `---
test_case_id: tc-001
title: Test With Pass Then Fail
requirement: REQ-1
---
`)
	writeFile(t, root, filepath.Join("test-automation", "records", "tc-001.automation.md"), `---
testcase: tc-001
framework: bats
status: accepted
artefact: test-automation/specs/tc-001.bats
adapter: bats-runner
last-formal-result: pass
attempts: 2
cycle: 1
---
`)
	// Older completed execute task
	writeFile(t, root, filepath.Join("test-tasks", "complete", "task-old1234-execute-tc-001.md"), `---
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
	writeFile(t, root, filepath.Join("test-tasks", "failed", "task-new5678-execute-tc-001.md"), `---
id: task-new5678
type: execute
target: tc-001
adapter: bats-runner
status: failed
created: 2026-03-07T10:00:00Z
branch: main
---
`)

	entries, err := PipelineStatus(root, nil)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	// Newer failed task should win over older completed task
	assert.Equal(t, "failed", entries[0].ExecuteStatus,
		"Newer failed task should override older completed task")
}

func TestBUG018_FailedOnlyStillShowsFailed(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("test-cases", "tc-001.md"), `---
test_case_id: tc-001
title: Test Failed Only
requirement: REQ-1
---
`)
	writeFile(t, root, filepath.Join("test-automation", "records", "tc-001.automation.md"), `---
testcase: tc-001
framework: bats
status: accepted
artefact: test-automation/specs/tc-001.bats
adapter: bats-runner
attempts: 1
cycle: 1
---
`)
	// Only a failed execute task, no completed task
	writeFile(t, root, filepath.Join("test-tasks", "failed", "task-abc1234-execute-tc-001.md"), `---
id: task-abc1234
type: execute
target: tc-001
adapter: bats-runner
status: failed
created: 2026-03-07T10:00:00Z
branch: main
---
`)

	entries, err := PipelineStatus(root, nil)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	assert.Equal(t, "failed", entries[0].ExecuteStatus,
		"Single failed task with no completed task should show failed")
}

func TestBUG018_NewerCompleteInPipelineDetail(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("test-cases", "tc-001.md"), `---
test_case_id: tc-001
title: Detail View Test
requirement: REQ-1
---
`)
	writeFile(t, root, filepath.Join("test-automation", "records", "tc-001.automation.md"), `---
testcase: tc-001
framework: bats
status: accepted
artefact: test-automation/specs/tc-001.bats
adapter: bats-runner
last-formal-result: pass
attempts: 2
cycle: 1
---
`)
	// Older failed
	writeFile(t, root, filepath.Join("test-tasks", "failed", "task-old1234-execute-tc-001.md"), `---
id: task-old1234
type: execute
target: tc-001
adapter: bats-runner
status: failed
created: 2026-03-07T09:00:00Z
branch: main
---
`)
	// Newer completed
	writeFile(t, root, filepath.Join("test-tasks", "complete", "task-new5678-execute-tc-001.md"), `---
id: task-new5678
type: execute
target: tc-001
adapter: bats-runner
status: complete
created: 2026-03-07T10:00:00Z
branch: main
---
`)

	detail, err := PipelineDetail(root, "tc-001")
	require.NoError(t, err)
	require.NotNil(t, detail)

	assert.Equal(t, "complete", detail.ExecuteStatus,
		"Detail view should also respect timestamp comparison")
}

// --- PipelineDetail tests ---

func TestPipelineDetail_Found(t *testing.T) {
	root := setupFixtureProject(t)

	detail, err := PipelineDetail(root, "tc-007")
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
	assert.Equal(t, "test-automation/specs/tc-007-checkout-guest.spec.ts", detail.ArtefactPath)
	assert.Equal(t, "results/junit/tc-007.xml", detail.LastRunPath)
	assert.Contains(t, detail.Tags, "checkout")
	assert.Contains(t, detail.Tags, "guest")
	assert.Contains(t, detail.Tags, "smoke")
}

func TestPipelineDetail_NotFound(t *testing.T) {
	root := setupFixtureProject(t)

	detail, err := PipelineDetail(root, "tc-nonexistent")
	assert.Error(t, err)
	assert.Nil(t, detail)
	assert.Contains(t, err.Error(), "not found")
}

func TestPipelineDetail_NoAutomation(t *testing.T) {
	root := setupFixtureProject(t)

	detail, err := PipelineDetail(root, "tc-009")
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

// --- Gaps tests ---

func TestGaps_FullFixture(t *testing.T) {
	root := setupFixtureProject(t)

	report, err := Gaps(root, []string{"test-automation/specs/playwright/"}, nil)
	require.NoError(t, err)
	require.NotNil(t, report)

	// NoTests: we don't have an external requirements list, so this should be empty
	assert.Empty(t, report.NoTests)

	// NoAutomation: tc-009 has no automation record (BUG-015: now checks records, not specs)
	require.Len(t, report.NoAutomation, 1)
	assert.Equal(t, "tc-009", report.NoAutomation[0].ID)
	assert.Equal(t, "Login - SSO", report.NoAutomation[0].Title)

	// NeverExecuted: tc-008 has automation (accepted) but no last-formal-result
	require.Len(t, report.NeverExecuted, 1)
	assert.Equal(t, "tc-008", report.NeverExecuted[0].ID)

	// CurrentlyFailing: none in our fixtures (tc-007 passes)
	assert.Empty(t, report.CurrentlyFailing)

	// SpecButNoRecord: tc-009 has no spec and no record, tc-007/tc-008 have both
	assert.Empty(t, report.SpecButNoRecord)
}

func TestGaps_WithFailingTest(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")

	writeFile(t, root, filepath.Join("test-cases", "tc-001.md"), `---
test_case_id: tc-001
title: Failing Test
requirement: REQ-1
status: automated
---
`)

	writeFile(t, root, filepath.Join("test-automation", "records", "tc-001.automation.md"), `---
testcase: tc-001
framework: playwright
status: accepted
artefact: test-automation/specs/tc-001.spec.ts
adapter: local-claude
last-formal-result: fail
attempts: 3
cycle: 1
---
`)

	// Add spec file so tc-001 is not in NoAutomation
	writeFile(t, root, filepath.Join("test-automation", "specs", "tc-001.spec.ts"),
		`test('tc-001: Failing test', () => {});`)

	report, err := Gaps(root, []string{"test-automation/specs/"}, nil)
	require.NoError(t, err)

	// Should not be in NoAutomation (it has automation)
	assert.Empty(t, report.NoAutomation)

	// Should not be in NeverExecuted (it has a result)
	assert.Empty(t, report.NeverExecuted)

	// Should be in CurrentlyFailing
	require.Len(t, report.CurrentlyFailing, 1)
	assert.Equal(t, "tc-001", report.CurrentlyFailing[0].ID)
	assert.Equal(t, "Failing Test", report.CurrentlyFailing[0].Title)
}

func TestGaps_EmptyProject(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")

	report, err := Gaps(root, []string{"test-automation/specs/"}, nil)
	require.NoError(t, err)
	require.NotNil(t, report)

	assert.Empty(t, report.NoTests)
	assert.Empty(t, report.NoAutomation)
	assert.Empty(t, report.NeverExecuted)
	assert.Empty(t, report.CurrentlyFailing)
	assert.Equal(t, 0, report.TotalGaps())
}

func TestGaps_TotalGaps(t *testing.T) {
	root := setupFixtureProject(t)

	report, err := Gaps(root, []string{"test-automation/specs/playwright/"}, nil)
	require.NoError(t, err)

	// 1 no-automation (tc-009) + 1 never-executed (tc-008)
	assert.Equal(t, 2, report.TotalGaps())
}

// --- BUG-009 regression tests ---

func TestBUG009_GapsScansSpecFiles(t *testing.T) {
	// BUG-015 changed Category 2 to use automation records instead of spec files.
	// A TC with a spec file but NO automation record IS now in NoAutomation (consistent
	// with status command) AND in SpecButNoRecord (new category).
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("test-cases", "tc-001.md"), `---
test_case_id: tc-001
title: Has Spec But No Record
requirement: REQ-1
status: ready
---
`)
	// Spec file references tc-001
	writeFile(t, root, filepath.Join("test-automation", "specs", "playwright", "tc-001.spec.ts"),
		`test('tc-001: Has spec', () => {});`)

	report, err := Gaps(root, []string{"test-automation/specs/playwright/"}, nil)
	require.NoError(t, err)

	// tc-001 has no automation record, so it IS in NoAutomation (BUG-015)
	require.Len(t, report.NoAutomation, 1)
	assert.Equal(t, "tc-001", report.NoAutomation[0].ID)

	// tc-001 has a spec file but no record, so it IS in SpecButNoRecord (BUG-015)
	require.Len(t, report.SpecButNoRecord, 1)
	assert.Equal(t, "tc-001", report.SpecButNoRecord[0].ID)
}

func TestBUG009_RecordExistsButNoSpec(t *testing.T) {
	// BUG-015 changed Category 2 to use automation records instead of spec files.
	// A TC with an automation record but NO spec file is NOT in NoAutomation (has record).
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("test-cases", "tc-001.md"), `---
test_case_id: tc-001
title: Has Record But No Spec
requirement: REQ-1
status: ready
---
`)
	writeFile(t, root, filepath.Join("test-automation", "records", "tc-001.automation.md"), `---
testcase: tc-001
framework: playwright
status: accepted
artefact: test-automation/specs/tc-001.spec.ts
adapter: local-claude
attempts: 1
cycle: 1
---
`)

	// No spec file exists.
	report, err := Gaps(root, []string{"test-automation/specs/playwright/"}, nil)
	require.NoError(t, err)

	// tc-001 has an automation record, so NOT in NoAutomation (BUG-015)
	assert.Empty(t, report.NoAutomation)

	// tc-001 has no spec file, so NOT in SpecButNoRecord either
	assert.Empty(t, report.SpecButNoRecord)
}

// --- BUG-015 regression tests ---

func TestBUG015_GapsConsistentWithStatus(t *testing.T) {
	// A test case with spec coverage but no automation record should appear
	// in both NoAutomation (consistent with status) and SpecButNoRecord.
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("test-cases", "tc-001.md"), `---
test_case_id: tc-001
title: Has Spec No Record
requirement: REQ-1
status: ready
---
`)
	// Spec file references tc-001 but no automation record exists
	writeFile(t, root, filepath.Join("test-automation", "specs", "tc-001.spec.ts"),
		`test('tc-001: Has spec', () => {});`)

	report, err := Gaps(root, []string{"test-automation/specs/"}, nil)
	require.NoError(t, err)

	// NoAutomation: no record means not automated (consistent with status command)
	require.Len(t, report.NoAutomation, 1)
	assert.Equal(t, "tc-001", report.NoAutomation[0].ID)

	// SpecButNoRecord: has spec reference but no formal record
	require.Len(t, report.SpecButNoRecord, 1)
	assert.Equal(t, "tc-001", report.SpecButNoRecord[0].ID)

	// Verify status also shows no automation for the same test case
	entries, err := PipelineStatus(root, nil)
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
	writeFile(t, root, filepath.Join("test-cases", "tc-001.md"), `---
test_case_id: tc-001
title: Has Both Spec And Record
requirement: REQ-1
status: ready
---
`)
	writeFile(t, root, filepath.Join("test-automation", "specs", "tc-001.spec.ts"),
		`test('tc-001: Has spec', () => {});`)
	writeFile(t, root, filepath.Join("test-automation", "records", "tc-001.automation.md"), `---
testcase: tc-001
framework: playwright
status: accepted
artefact: test-automation/specs/tc-001.spec.ts
adapter: local-claude
attempts: 1
cycle: 1
---
`)

	report, err := Gaps(root, []string{"test-automation/specs/"}, nil)
	require.NoError(t, err)

	// Has record: not in NoAutomation
	assert.Empty(t, report.NoAutomation)

	// Has both spec and record: not in SpecButNoRecord
	assert.Empty(t, report.SpecButNoRecord)
}

func TestBUG015_NoSpecNoRecordNotInSpecButNoRecord(t *testing.T) {
	// A test case with neither spec file nor automation record should appear
	// in NoAutomation but NOT in SpecButNoRecord.
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("test-cases", "tc-001.md"), `---
test_case_id: tc-001
title: Has Nothing
requirement: REQ-1
status: ready
---
`)

	report, err := Gaps(root, []string{"test-automation/specs/"}, nil)
	require.NoError(t, err)

	// No record: in NoAutomation
	require.Len(t, report.NoAutomation, 1)
	assert.Equal(t, "tc-001", report.NoAutomation[0].ID)

	// No spec: not in SpecButNoRecord
	assert.Empty(t, report.SpecButNoRecord)
}

// --- Triage backward-compat test ---

func TestTriage_NoAutomationRecord(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")

	info, err := Triage(root, "tc-none")
	assert.Nil(t, info)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no automation record found")
}

// --- Edge cases ---

func TestPipelineStatus_NoMdFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	// Create test-cases dir with a non-md file
	writeFile(t, root, filepath.Join("test-cases", "README.txt"), "Not a test case")

	entries, err := PipelineStatus(root, nil)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestPipelineStatus_EmptyFrontmatter(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	// File with valid frontmatter but empty ID
	writeFile(t, root, filepath.Join("test-cases", "empty.md"), `---
test_case_id: ""
title: Empty ID
---
`)

	entries, err := PipelineStatus(root, nil)
	require.NoError(t, err)
	// Empty ID should be skipped
	assert.Empty(t, entries)
}

func TestPipelineStatus_NestedTestCases(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	// Test cases in nested subdirectories
	writeFile(t, root, filepath.Join("test-cases", "checkout", "tc-001.md"), `---
test_case_id: tc-001
title: Nested Checkout
requirement: REQ-1
status: ready
---
`)
	writeFile(t, root, filepath.Join("test-cases", "login", "tc-002.md"), `---
test_case_id: tc-002
title: Nested Login
requirement: REQ-2
status: ready
---
`)

	entries, err := PipelineStatus(root, nil)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, "tc-001", entries[0].TestCaseID)
	assert.Equal(t, "tc-002", entries[1].TestCaseID)
}

func TestScanAutomationRecords_NonAutomationFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	// File that doesn't end with .automation.md should be skipped
	writeFile(t, root, filepath.Join("test-automation", "records", "README.md"), "# Records")
	writeFile(t, root, filepath.Join("test-automation", "records", "tc-001.automation.md"), `---
testcase: tc-001
framework: playwright
status: accepted
last-formal-result: pass
---
`)

	records, err := scanAutomationRecords(root)
	require.NoError(t, err)
	assert.Len(t, records, 1)
	assert.Contains(t, records, "tc-001")
}

// --- BUG-011 regression tests ---

func TestBUG011_StatusParsesTestCaseID(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("test-cases", "tc-bug011-test.md"), `---
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

	entries, err := PipelineStatus(root, nil)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "tc-bug011", entries[0].TestCaseID)
	assert.Equal(t, "BUG-011 Regression Test", entries[0].Title)
	assert.Equal(t, "complete", entries[0].CreateStatus)
}

func TestBUG011_StatusNormalizesUppercaseID(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("test-cases", "tc-upper-test.md"), `---
test_case_id: TC-UPPER
title: Uppercase ID Test
requirement: BUG-011
---
`)

	entries, err := PipelineStatus(root, nil)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "tc-upper", entries[0].TestCaseID, "ID should be normalized to lowercase")
}

func TestBUG011_DetailFindsTestCaseID(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeFile(t, root, filepath.Join("test-cases", "tc-bug011-detail.md"), `---
test_case_id: tc-bug011
title: BUG-011 Detail Test
requirement: BUG-011
priority: Medium
type: Integration
created: 2026-02-21
---
`)

	detail, err := PipelineDetail(root, "tc-bug011")
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
	writeFile(t, root, filepath.Join("test-cases", "tc-root.md"), `---
test_case_id: tc-root
title: Root Level
requirement: REQ-ROOT
---
`)

	// Login subfolder
	writeFile(t, root, filepath.Join("test-cases", "login", "tc-login1.md"), `---
test_case_id: tc-login1
title: Login Happy
requirement: REQ-LOGIN
---
`)
	writeFile(t, root, filepath.Join("test-cases", "login", "tc-login2.md"), `---
test_case_id: tc-login2
title: Login Error
requirement: REQ-LOGIN
---
`)

	// Nested subfolder under login
	writeFile(t, root, filepath.Join("test-cases", "login", "oauth", "tc-oauth1.md"), `---
test_case_id: tc-oauth1
title: OAuth Flow
requirement: REQ-LOGIN
---
`)

	// Payments subfolder
	writeFile(t, root, filepath.Join("test-cases", "payments", "tc-pay1.md"), `---
test_case_id: tc-pay1
title: Payment Flow
requirement: REQ-PAY
---
`)

	// Nested refunds under payments
	writeFile(t, root, filepath.Join("test-cases", "payments", "refunds", "tc-refund1.md"), `---
test_case_id: tc-refund1
title: Refund Flow
requirement: REQ-PAY
---
`)

	return root
}

func TestPipelineStatus_ShallowAtRoot(t *testing.T) {
	root := setupSubfolderFixture(t)
	tcDir := filepath.Join(root, "test-cases")

	scope := &ScopeInfo{ScanDir: tcDir, RelPath: "test-cases/", Recursive: false}
	entries, err := PipelineStatus(root, scope)
	require.NoError(t, err)

	// Shallow at test-cases/ root: only tc-root (no subdirectory test cases)
	require.Len(t, entries, 1)
	assert.Equal(t, "tc-root", entries[0].TestCaseID)
}

func TestPipelineStatus_RecursiveAtRoot(t *testing.T) {
	root := setupSubfolderFixture(t)
	tcDir := filepath.Join(root, "test-cases")

	scope := &ScopeInfo{ScanDir: tcDir, RelPath: "test-cases/", Recursive: true}
	entries, err := PipelineStatus(root, scope)
	require.NoError(t, err)

	// Recursive at test-cases/ root: all 6 test cases
	assert.Len(t, entries, 6)
}

func TestPipelineStatus_ShallowAtLogin(t *testing.T) {
	root := setupSubfolderFixture(t)
	loginDir := filepath.Join(root, "test-cases", "login")

	scope := &ScopeInfo{ScanDir: loginDir, RelPath: "test-cases/login/", Recursive: false}
	entries, err := PipelineStatus(root, scope)
	require.NoError(t, err)

	// Shallow at login: tc-login1, tc-login2 (NOT tc-oauth1)
	require.Len(t, entries, 2)

	ids := []string{entries[0].TestCaseID, entries[1].TestCaseID}
	assert.Contains(t, ids, "tc-login1")
	assert.Contains(t, ids, "tc-login2")
}

func TestPipelineStatus_RecursiveAtLogin(t *testing.T) {
	root := setupSubfolderFixture(t)
	loginDir := filepath.Join(root, "test-cases", "login")

	scope := &ScopeInfo{ScanDir: loginDir, RelPath: "test-cases/login/", Recursive: true}
	entries, err := PipelineStatus(root, scope)
	require.NoError(t, err)

	// Recursive at login: tc-login1, tc-login2, tc-oauth1
	assert.Len(t, entries, 3)
}

func TestPipelineStatus_ShallowExcludesSiblings(t *testing.T) {
	root := setupSubfolderFixture(t)
	loginDir := filepath.Join(root, "test-cases", "login")

	scope := &ScopeInfo{ScanDir: loginDir, RelPath: "test-cases/login/", Recursive: false}
	entries, err := PipelineStatus(root, scope)
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

	entries, err := PipelineStatus(root, nil)
	require.NoError(t, err)

	// All 6 test cases (backward compat)
	assert.Len(t, entries, 6)
}

func TestPipelineStatus_EmptySubfolder(t *testing.T) {
	root := setupSubfolderFixture(t)
	emptyDir := filepath.Join(root, "test-cases", "empty")
	mkdirAll(t, root, filepath.Join("test-cases", "empty"))

	scope := &ScopeInfo{ScanDir: emptyDir, RelPath: "test-cases/empty/", Recursive: false}
	entries, err := PipelineStatus(root, scope)
	require.NoError(t, err)
	assert.Len(t, entries, 0)
}

func TestGaps_ScopedToLogin(t *testing.T) {
	root := setupSubfolderFixture(t)
	loginDir := filepath.Join(root, "test-cases", "login")

	scope := &ScopeInfo{ScanDir: loginDir, RelPath: "test-cases/login/", Recursive: false}
	report, err := Gaps(root, nil, scope)
	require.NoError(t, err)

	// Only login TCs should appear in gap categories
	for _, entry := range report.NoAutomation {
		assert.True(t, entry.ID == "tc-login1" || entry.ID == "tc-login2",
			"unexpected TC in NoAutomation: %s", entry.ID)
	}
}

func TestMap_ScopedToLogin(t *testing.T) {
	root := setupSubfolderFixture(t)
	loginDir := filepath.Join(root, "test-cases", "login")

	scope := &ScopeInfo{ScanDir: loginDir, RelPath: "test-cases/login/", Recursive: false}
	report, err := Map(root, scope)
	require.NoError(t, err)

	// Only login TCs in groups
	totalTCs := 0
	for _, grp := range report.Groups {
		totalTCs += len(grp.TestCases)
	}
	totalTCs += len(report.Unlinked)
	assert.Equal(t, 2, totalTCs, "should only have tc-login1 and tc-login2")
}
