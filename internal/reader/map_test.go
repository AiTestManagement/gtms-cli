package reader

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMap_GroupsByRequirement(t *testing.T) {
	root := t.TempDir()

	// 2 test cases for REQ-A, 1 for REQ-B
	writeFile(t, root, filepath.Join("test-cases", "tc-aaa1111-login-happy.md"), `---
test_case_id: tc-aaa1111
title: Login Happy Path
requirement: REQ-A
---
`)
	writeFile(t, root, filepath.Join("test-cases", "tc-aaa2222-login-error.md"), `---
test_case_id: tc-aaa2222
title: Login Error Path
requirement: REQ-A
---
`)
	writeFile(t, root, filepath.Join("test-cases", "tc-bbb1111-checkout-flow.md"), `---
test_case_id: tc-bbb1111
title: Checkout Flow
requirement: REQ-B
---
`)

	report, err := Map(root, nil)
	require.NoError(t, err)
	require.NotNil(t, report)

	// 2 groups
	require.Len(t, report.Groups, 2)

	// Groups sorted alphabetically
	assert.Equal(t, "REQ-A", report.Groups[0].Requirement)
	assert.Equal(t, "REQ-B", report.Groups[1].Requirement)

	// REQ-A has 2 test cases, REQ-B has 1
	assert.Len(t, report.Groups[0].TestCases, 2)
	assert.Len(t, report.Groups[1].TestCases, 1)

	// Test cases within groups sorted by ID
	assert.Equal(t, "tc-aaa1111", report.Groups[0].TestCases[0].TestCaseID)
	assert.Equal(t, "tc-aaa2222", report.Groups[0].TestCases[1].TestCaseID)

	// No unlinked
	assert.Empty(t, report.Unlinked)
}

func TestMap_UnlinkedTestCases(t *testing.T) {
	root := t.TempDir()

	writeFile(t, root, filepath.Join("test-cases", "tc-aaa1111-linked.md"), `---
test_case_id: tc-aaa1111
title: Linked Test
requirement: REQ-A
---
`)
	writeFile(t, root, filepath.Join("test-cases", "tc-bbb1111-orphan.md"), `---
test_case_id: tc-bbb1111
title: Unlinked Test
requirement: ""
---
`)

	report, err := Map(root, nil)
	require.NoError(t, err)

	assert.Len(t, report.Groups, 1)
	require.Len(t, report.Unlinked, 1)
	assert.Equal(t, "tc-bbb1111", report.Unlinked[0].TestCaseID)
	assert.Equal(t, "orphan", report.Unlinked[0].Slug)
	assert.Equal(t, "Unlinked Test", report.Unlinked[0].Title)
}

func TestMap_SlugDerivation(t *testing.T) {
	root := t.TempDir()

	writeFile(t, root, filepath.Join("test-cases", "tc-a1b2c3d-tier1-sync-happy-path.md"), `---
test_case_id: tc-a1b2c3d
title: Tier 1 Sync Happy Path Test
requirement: REQ-1
---
`)

	report, err := Map(root, nil)
	require.NoError(t, err)
	require.Len(t, report.Groups, 1)
	require.Len(t, report.Groups[0].TestCases, 1)

	entry := report.Groups[0].TestCases[0]
	assert.Equal(t, "tier1-sync-happy-path", entry.Slug)
	assert.Equal(t, "Tier 1 Sync Happy Path Test", entry.Title)
}

func TestMap_SlugDerivation_ShortFilename(t *testing.T) {
	// Test case where filename has no slug portion (e.g. "tc-001.md")
	root := t.TempDir()

	writeFile(t, root, filepath.Join("test-cases", "tc-001.md"), `---
test_case_id: tc-001
title: Short Filename Test
requirement: REQ-1
---
`)

	report, err := Map(root, nil)
	require.NoError(t, err)
	require.Len(t, report.Groups, 1)
	require.Len(t, report.Groups[0].TestCases, 1)

	// Slug should be empty (no panic)
	assert.Equal(t, "", report.Groups[0].TestCases[0].Slug)
}

func TestMap_EmptyProject(t *testing.T) {
	root := t.TempDir()

	report, err := Map(root, nil)
	require.NoError(t, err)
	require.NotNil(t, report)

	assert.Empty(t, report.Groups)
	assert.Empty(t, report.Unlinked)
	assert.Equal(t, 0, report.Summary.TotalRequirements)
	assert.Equal(t, 0, report.Summary.TotalTestCases)
	assert.Equal(t, 0, report.Summary.Automated)
	assert.Equal(t, 0, report.Summary.Executed)
	assert.Equal(t, 0, report.Summary.UnlinkedCount)
}

func TestMap_WithAutomation(t *testing.T) {
	root := t.TempDir()

	writeFile(t, root, filepath.Join("test-cases", "tc-aaa1111-automated.md"), `---
test_case_id: tc-aaa1111
title: Automated Test
requirement: REQ-1
---
`)
	writeFile(t, root, filepath.Join("test-automation", "records", "tc-aaa1111.automation.md"), `---
testcase: tc-aaa1111
framework: playwright
status: accepted
artefact: test-automation/specs/tc-aaa1111.spec.ts
adapter: local-claude
attempts: 1
cycle: 1
---
`)

	report, err := Map(root, nil)
	require.NoError(t, err)
	require.Len(t, report.Groups, 1)

	entry := report.Groups[0].TestCases[0]
	assert.Equal(t, "complete", entry.AutomateStatus)
}

func TestMap_WithExecution(t *testing.T) {
	root := t.TempDir()

	writeFile(t, root, filepath.Join("test-cases", "tc-aaa1111-executed.md"), `---
test_case_id: tc-aaa1111
title: Executed Test
requirement: REQ-1
---
`)
	writeFile(t, root, filepath.Join("test-automation", "records", "tc-aaa1111.automation.md"), `---
testcase: tc-aaa1111
framework: playwright
status: accepted
last-formal-result: pass
attempts: 1
cycle: 1
---
`)

	report, err := Map(root, nil)
	require.NoError(t, err)
	require.Len(t, report.Groups, 1)

	entry := report.Groups[0].TestCases[0]
	assert.Equal(t, "pass", entry.LastResult)
	assert.Equal(t, "complete", entry.ExecuteStatus)
}

func TestMap_ExecuteStatusReflectsResult(t *testing.T) {
	root := t.TempDir()

	writeFile(t, root, filepath.Join("test-cases", "tc-aaa1111-failing.md"), `---
test_case_id: tc-aaa1111
title: Failing Test
requirement: REQ-1
---
`)
	writeFile(t, root, filepath.Join("test-automation", "records", "tc-aaa1111.automation.md"), `---
testcase: tc-aaa1111
framework: playwright
status: accepted
last-formal-result: fail
attempts: 2
cycle: 1
---
`)

	report, err := Map(root, nil)
	require.NoError(t, err)
	require.Len(t, report.Groups, 1)

	entry := report.Groups[0].TestCases[0]
	assert.Equal(t, "fail", entry.LastResult)
	assert.Equal(t, "complete", entry.ExecuteStatus)
}

func TestMap_Summary(t *testing.T) {
	root := t.TempDir()

	// 2 test cases across 2 requirements
	writeFile(t, root, filepath.Join("test-cases", "tc-aaa1111-first.md"), `---
test_case_id: tc-aaa1111
title: First Test
requirement: REQ-A
---
`)
	writeFile(t, root, filepath.Join("test-cases", "tc-bbb1111-second.md"), `---
test_case_id: tc-bbb1111
title: Second Test
requirement: REQ-B
---
`)
	// 1 unlinked
	writeFile(t, root, filepath.Join("test-cases", "tc-ccc1111-unlinked.md"), `---
test_case_id: tc-ccc1111
title: Unlinked
---
`)

	// 2 with automation
	writeFile(t, root, filepath.Join("test-automation", "records", "tc-aaa1111.automation.md"), `---
testcase: tc-aaa1111
framework: playwright
status: accepted
last-formal-result: pass
attempts: 1
cycle: 1
---
`)
	writeFile(t, root, filepath.Join("test-automation", "records", "tc-bbb1111.automation.md"), `---
testcase: tc-bbb1111
framework: playwright
status: accepted
attempts: 1
cycle: 1
---
`)

	report, err := Map(root, nil)
	require.NoError(t, err)

	assert.Equal(t, 2, report.Summary.TotalRequirements)
	assert.Equal(t, 3, report.Summary.TotalTestCases)
	assert.Equal(t, 2, report.Summary.Automated)
	assert.Equal(t, 1, report.Summary.Executed)
	assert.Equal(t, 1, report.Summary.UnlinkedCount)
}

func TestMap_ActiveTaskOverride(t *testing.T) {
	root := t.TempDir()

	writeFile(t, root, filepath.Join("test-cases", "tc-aaa1111-pending.md"), `---
test_case_id: tc-aaa1111
title: Pending Automate
requirement: REQ-1
---
`)
	// Pending automate task but no automation record
	mkdirAll(t, root, filepath.Join("test-tasks", "pending"))
	writeFile(t, root, filepath.Join("test-tasks", "pending", "task-x1y2z3a-automate-tc-aaa1111.md"), `---
id: task-x1y2z3a
type: automate
target: tc-aaa1111
adapter: local-claude
status: pending
created: 2026-02-20T10:00:00Z
branch: feature/automate
---
`)

	report, err := Map(root, nil)
	require.NoError(t, err)
	require.Len(t, report.Groups, 1)

	entry := report.Groups[0].TestCases[0]
	assert.Equal(t, "pending", entry.AutomateStatus)
}

func TestMap_ActiveTaskOverride_InProgress(t *testing.T) {
	root := t.TempDir()

	writeFile(t, root, filepath.Join("test-cases", "tc-aaa1111-active.md"), `---
test_case_id: tc-aaa1111
title: Active Automate
requirement: REQ-1
---
`)
	// Automation record shows complete (status: accepted)
	writeFile(t, root, filepath.Join("test-automation", "records", "tc-aaa1111.automation.md"), `---
testcase: tc-aaa1111
framework: playwright
status: accepted
attempts: 1
cycle: 1
---
`)
	// In-progress task should override the "complete" automation status
	mkdirAll(t, root, filepath.Join("test-tasks", "in-progress"))
	writeFile(t, root, filepath.Join("test-tasks", "in-progress", "task-x1y2z3a-automate-tc-aaa1111.md"), `---
id: task-x1y2z3a
type: automate
target: tc-aaa1111
adapter: local-claude
status: in-progress
created: 2026-02-20T10:00:00Z
branch: feature/automate
---
`)

	report, err := Map(root, nil)
	require.NoError(t, err)
	require.Len(t, report.Groups, 1)

	entry := report.Groups[0].TestCases[0]
	assert.Equal(t, "in-progress", entry.AutomateStatus)
}

func TestMap_ActiveTaskOverride_ExecuteTask(t *testing.T) {
	root := t.TempDir()

	writeFile(t, root, filepath.Join("test-cases", "tc-aaa1111-executing.md"), `---
test_case_id: tc-aaa1111
title: Executing Test
requirement: REQ-1
---
`)
	// Pending execute task — no automation record needed
	mkdirAll(t, root, filepath.Join("test-tasks", "pending"))
	writeFile(t, root, filepath.Join("test-tasks", "pending", "task-x1y2z3a-execute-tc-aaa1111.md"), `---
id: task-x1y2z3a
type: execute
target: tc-aaa1111
adapter: local-runner
status: pending
created: 2026-02-20T10:00:00Z
branch: feature/execute
---
`)

	report, err := Map(root, nil)
	require.NoError(t, err)
	require.Len(t, report.Groups, 1)

	entry := report.Groups[0].TestCases[0]
	assert.Equal(t, "pending", entry.ExecuteStatus)
}

func TestMap_MalformedTestCase(t *testing.T) {
	root := t.TempDir()

	// Valid test case
	writeFile(t, root, filepath.Join("test-cases", "tc-aaa1111-valid.md"), `---
test_case_id: tc-aaa1111
title: Valid Test
requirement: REQ-1
---
`)
	// Malformed test case
	writeFile(t, root, filepath.Join("test-cases", "tc-bad.md"), `---
broken: yaml: [[[
---
`)

	report, err := Map(root, nil)
	require.NoError(t, err)
	require.Len(t, report.Groups, 1)
	assert.Len(t, report.Groups[0].TestCases, 1)
	assert.Equal(t, "tc-aaa1111", report.Groups[0].TestCases[0].TestCaseID)
}
