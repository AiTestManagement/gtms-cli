package reader

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPipelineStatus_NoPhantomRowsOnFreshScaffold covers ENH-164 §
// "Layout and scaffold" AC: on a fresh post-ENH-164 init, gtms status shows
// zero phantom rows. The fix is structural -- templates and guides become
// siblings of cases/ under gtms/test/, not children, so the TC walker
// physically cannot reach them. This test pins that structural property
// against the reader API.
//
// Layout staged here:
//
//	gtms/test/cases/      (empty)
//	gtms/test/templates/  (contains the ENH-161 stamping templates carrying
//	                       frontmatter `test_case_id: ${TESTCASE_ID}`)
//	gtms/test/guides/     (contains a guide markdown)
//
// Pre-ENH-164 the templates/ and guides/ entries lived under gtms/test/cases/
// and leaked into scanTestCases. Post-ENH-164 they live one level up under
// gtms/test/ and are not under the TC scan root, so PipelineStatus returns
// zero entries and PipelineFolderSummary contains no folder named
// "templates" or "guides".
func TestPipelineStatus_NoPhantomRowsOnFreshScaffold(t *testing.T) {
	root := t.TempDir()

	// gtms.config so the project root is recognisable.
	writeFile(t, root, "gtms.config", `project:
  name: enh-164-fresh-scaffold
  repo: org/enh-164-fresh-scaffold
`)

	// gtms/test/cases/ exists but is empty -- a fresh scaffold has no user TCs.
	mkdirAll(t, root, filepath.Join("gtms", "test", "cases"))

	// gtms/test/templates/ holds the ENH-161 role-specific stamping templates.
	// These carry the literal frontmatter placeholder `test_case_id: ${TESTCASE_ID}`
	// which, pre-ENH-164, leaked into the reader as the ${testcase_id} phantom row.
	writeFile(t, root, filepath.Join("gtms", "test", "templates", "manual-testcase.template.md"), `---
test_case_id: ${TESTCASE_ID}
title: "${TITLE}"
requirement: ${REQUIREMENT}
created: ${CREATED}
---

## Test Objective

## Test Steps
`)
	writeFile(t, root, filepath.Join("gtms", "test", "templates", "agent-testcase.template.md"), `---
test_case_id: ${TESTCASE_ID}
title: "${TITLE}"
requirement: ${REQUIREMENT}
created: ${CREATED}
---

## Test Objective

## Test Steps
`)

	// gtms/test/guides/ holds a guide markdown. Has no frontmatter; pre-ENH-164
	// it sat under gtms/test/guides/ and could not trip the reader, but the
	// folder name itself appeared in PipelineFolderSummary because deriveFolderName
	// used the first path component after gtms/test/cases/.
	writeFile(t, root, filepath.Join("gtms", "test", "guides", "gtms-test-case-authoring-guide.md"),
		"# GTMS Test Case Authoring Guide\n\nA guide.\n")

	// --- PipelineStatus: zero entries on a fresh scaffold ---
	entries, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	assert.Empty(t, entries,
		"ENH-164 AC: PipelineStatus on a fresh post-ENH-164 scaffold must return zero entries; "+
			"templates and guides under gtms/test/ are siblings of cases/, not children, so the TC walker cannot reach them")

	// Belt and braces: even if some future change re-admits stamping templates,
	// no entry must carry the literal-placeholder id.
	for _, e := range entries {
		assert.NotEqual(t, "${testcase_id}", e.TestCaseID,
			"ENH-164 AC: no PipelineStatus entry may carry id ${testcase_id} (lowercased placeholder leak)")
	}

	// --- PipelineFolderSummary: no folder named "templates" or "guides" ---
	folders, err := PipelineFolderSummary(root, "")
	require.NoError(t, err)
	for _, f := range folders {
		assert.NotEqual(t, "templates", f.Folder,
			"ENH-164 AC: folder summary on a fresh post-ENH-164 scaffold must not contain a folder named 'templates'")
		assert.NotEqual(t, "guides", f.Folder,
			"ENH-164 AC: folder summary on a fresh post-ENH-164 scaffold must not contain a folder named 'guides'")
	}
}

// writeFile and mkdirAll are defined in reader_test.go (same package) and are
// reused here without redeclaration.
