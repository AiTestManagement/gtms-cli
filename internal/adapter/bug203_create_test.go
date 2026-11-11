package adapter

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
	"github.com/aitestmanagement/gtms-cli/internal/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BUG-203 AC6: when a create fails because a stale v0.3.x scaffolded producer emitted the
// pre-v2 header, GTMS names the producing template or script and how to refresh it, so
// the user does not look at the stamped spec the error otherwise names.

func staleViolation() []SpecValidationError {
	return []SpecValidationError{{
		File:   "tc-aaaaaaaa.md",
		Reason: "missing schema discriminator: key 'gtms_schema' is absent",
	}}
}

func TestBUG203_StaleProducerHint_BuiltinNamesTemplate(t *testing.T) {
	root := t.TempDir()
	tmpl := filepath.Join(root, "gtms", "test", "templates", "manual-testcase.template.md")
	resolved := &ResolvedAdapter{Command: "create", Name: "manual-create", Tier: 0}
	ac := &AdapterContext{TemplateFile: tmpl}

	hint := staleScaffoldProducerHint(resolved, ac, root, staleViolation())
	assert.Contains(t, hint, "gtms/test/templates/manual-testcase.template.md",
		"a builtin create must name the role testcase template as the producer")
	// REV-209 R2-CLAUDE-001: the actionable command is the preset form (plain
	// `gtms init --force` is a no-op in an existing project).
	assert.Contains(t, hint, "gtms init --force --preset",
		"the hint must name the preset refresh command")
	assert.Contains(t, hint, "manual, bats, or playwright",
		"the hint must offer the three preset choices")
}

func TestBUG203_StaleProducerHint_ScriptNamesScript(t *testing.T) {
	root := t.TempDir()
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "manual-create-script",
		Tier:    2,
		Config:  &config.AdapterConfig{Mode: "sync", Script: "gtms/adapters/manual-create-script.sh"},
	}
	// Even though a template path is also present, the configured script wins.
	ac := &AdapterContext{TemplateFile: filepath.Join(root, "gtms", "test", "templates", "manual-testcase.template.md")}

	hint := staleScaffoldProducerHint(resolved, ac, root, staleViolation())
	assert.Contains(t, hint, "gtms/adapters/manual-create-script.sh",
		"a configured shipped script create must name the script as the producer")
	assert.Contains(t, hint, "gtms init --force --preset",
		"a shipped scaffolded create script is refreshed by the preset form")
	assert.Contains(t, hint, "manual, bats, or playwright")
}

// REV-209 CLAUDE-002: a producer under _demo/ is only refreshed by the demo refresh
// command, so the hint must say `gtms init --force --demo`, not plain `gtms init --force`.
func TestBUG203_StaleProducerHint_DemoScriptNamesDemoRefresh(t *testing.T) {
	root := t.TempDir()
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "demo",
		Tier:    2,
		Config:  &config.AdapterConfig{Mode: "sync", Script: "_demo/adapters/create-demo.sh"},
	}
	ac := &AdapterContext{}

	hint := staleScaffoldProducerHint(resolved, ac, root, staleViolation())
	assert.Contains(t, hint, "_demo/adapters/create-demo.sh",
		"the demo create adapter must be named as the producer")
	assert.Contains(t, hint, "gtms init --force --demo",
		"a _demo/ producer must point at the demo refresh command")
	// The recommended command must carry --demo (plain `gtms init --force` would not
	// re-seed the demo). The hint is a single line ending in the refresh command.
	assert.True(t, strings.Contains(hint, "gtms init --force --demo"),
		"the demo hint names the --demo refresh, not the bare preset refresh")
}

func TestBUG203_StaleProducerHint_NonStaleFailureGetsNoHint(t *testing.T) {
	root := t.TempDir()
	resolved := &ResolvedAdapter{Command: "create", Name: "manual-create", Tier: 0}
	ac := &AdapterContext{TemplateFile: filepath.Join(root, "gtms", "test", "templates", "manual-testcase.template.md")}

	nonStale := []SpecValidationError{{
		File:   "tc-aaaaaaaa.md",
		Reason: "frontmatter test_case_id 'tc-deadbeef' does not match filename ID 'tc-aaaaaaaa'",
	}}
	assert.Empty(t, staleScaffoldProducerHint(resolved, ac, root, nonStale),
		"a non-stale-header failure must not get the refresh hint (refreshing scaffold would not fix it)")
}

// REV-209 R2-CODEX-003: a CUSTOM command adapter (Tier 1, no script) emitting a bad header
// must NOT be told to run gtms init -- init cannot repair custom code, and the role template
// it never read must not be blamed. The adapter is named and the user is told to fix it.
func TestBUG203_StaleProducerHint_CustomCommandAdapter_NoRefresh(t *testing.T) {
	root := t.TempDir()
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "my-ai-create",
		Tier:    1,
		Config:  &config.AdapterConfig{Mode: "sync", Command: `my-ai-tool --emit`},
	}
	// A TemplateFile is always passed, but it must NOT be attributed to a non-Tier-0 adapter.
	ac := &AdapterContext{TemplateFile: filepath.Join(root, "gtms", "test", "templates", "manual-testcase.template.md")}

	hint := staleScaffoldProducerHint(resolved, ac, root, staleViolation())
	assert.NotContains(t, hint, "gtms init",
		"a custom command adapter must not be told to run gtms init")
	assert.NotContains(t, hint, "manual-testcase.template.md",
		"a custom command adapter's failure must not be blamed on the role template it never read")
	assert.Contains(t, hint, "my-ai-create", "the custom adapter must be named")
	assert.Contains(t, hint, "test-case/v2", "the user must be told to fix the v2 header the adapter emits")
}

// REV-209 R2-CODEX-003: a CUSTOM script adapter (Tier 2, non-catalogue script path) gets the
// same treatment -- named, no gtms init suggestion, no scaffold blame.
func TestBUG203_StaleProducerHint_CustomScriptAdapter_NoRefresh(t *testing.T) {
	root := t.TempDir()
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "house-style-create",
		Tier:    2,
		Config:  &config.AdapterConfig{Mode: "sync", Script: "gtms/adapters/house-style-create.sh"},
	}
	ac := &AdapterContext{}

	hint := staleScaffoldProducerHint(resolved, ac, root, staleViolation())
	assert.NotContains(t, hint, "gtms init",
		"a custom script adapter must not be told to run gtms init")
	assert.Contains(t, hint, "house-style-create", "the custom adapter must be named")
	assert.Contains(t, hint, "gtms/adapters/house-style-create.sh", "the custom adapter's script path must be named")
	assert.Contains(t, hint, "test-case/v2", "the user must be told to fix the v2 header the adapter emits")
}

// TestBUG203_PathContainment_RejectsEscapes proves the containment primitive the quarantine
// loop relies on (REV-209 R2-CODEX-001): a `..` traversal and an escaping symlink both fail
// ResolveUnderRoot, while an in-root path resolves.
func TestBUG203_PathContainment_RejectsEscapes(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "project")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms", "test", "cases", "feature"), 0o755))

	// In-root path resolves.
	_, _, err := pathsafe.ResolveUnderRoot(root, filepath.Join(root, "gtms", "test", "cases", "feature", "tc-aaaaaaaa.md"))
	assert.NoError(t, err, "an in-root path must resolve")

	// `..` traversal escapes.
	_, _, err = pathsafe.ResolveUnderRoot(root, filepath.Join(root, "..", "outside", "tc-bbbbbbbb.md"))
	assert.Error(t, err, "a .. traversal out of the root must be rejected")

	if runtime.GOOS != "windows" {
		outside := filepath.Join(base, "outside")
		require.NoError(t, os.MkdirAll(outside, 0o755))
		link := filepath.Join(root, "gtms", "test", "cases", "escaped")
		require.NoError(t, os.Symlink(outside, link))
		_, _, err = pathsafe.ResolveUnderRoot(root, filepath.Join(link, "tc-cccccccc.md"))
		assert.Error(t, err, "a path through an escaping symlink must be rejected")
	}
}

// createOwnedPrev2Script writes an OWNED invalid spec (generated filename id, pre-v2 header)
// into GTMS_OUTPUT_DIR and a clean handoff. Used by the containment and renamed-layout E2Es.
const createOwnedPrev2Script = `#!/bin/sh
# GTMS does not create the output directory for Tier 2 adapters (only the Tier 0
# built-in does); the shipped scripts all mkdir -p it themselves, so this fixture does
# too. This keeps the fixture correct in a renamed layout, where the test does not
# pre-create the folder.
mkdir -p "${GTMS_OUTPUT_DIR}"
ID=$(printf '%s' "$GTMS_TC_IDS" | cut -d, -f1)
cat > "${GTMS_OUTPUT_DIR}/${ID}-owned.md" <<SPEC
---
test_case_id: ${ID}
title: "owned stale"
type: Functional
---
# Test
SPEC
{
  printf 'task: %s\n'    "${GTMS_TASK_ID}"
  printf 'command: create\n'
  printf 'target: %s\n'  "${GTMS_REFERENCE}"
  printf 'adapter: owned-prev2\n'
  printf 'mode: sync\n'
  printf 'status: complete\n'
  printf 'result: pass\n'
  printf 'summary: "written"\n'
  printf 'completed: 2026-10-16T00:00:00Z\n'
} > "${GTMS_RESULT_FILE}"
exit 0
`

// TestBUG203_Quarantine_RefusesSymlinkedOutputDir covers REV-209 R2-CODEX-001: when the
// create output directory is a symlink escaping the project root, an owned invalid spec is
// NOT moved -- it is left in place and reported with the path-containment message.
func TestBUG203_Quarantine_RefusesSymlinkedOutputDir(t *testing.T) {
	skipIfShort(t)
	if runtime.GOOS == "windows" {
		t.Skip("symlink output-dir containment test uses POSIX symlinks and a Tier 2 sh adapter")
	}

	root := setupTestProject(t)
	outside := t.TempDir() // a separate temp tree, outside the project root

	// gtms/test/cases/escaped -> outside (an escaping symlink used as the output dir).
	link := filepath.Join(root, "gtms", "test", "cases", "escaped")
	require.NoError(t, os.Symlink(outside, link))

	scriptRel := "testdata/create-owned-prev2.sh"
	scriptPath := filepath.Join(root, scriptRel)
	require.NoError(t, os.MkdirAll(filepath.Dir(scriptPath), 0o755))
	require.NoError(t, os.WriteFile(scriptPath, []byte(createOwnedPrev2Script), 0o755))

	cfg := &config.Config{Project: config.ProjectConfig{Name: "Test", Repo: "org/test"}}
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "owned-prev2",
		Config:  &config.AdapterConfig{Mode: "sync", Script: scriptRel},
		Tier:    2,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "escaped", CommandFlags{Folder: "escaped"})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "error", res.Status)

	// The spec is still in the (escaping) output dir -- never moved.
	remainingOutside, _ := filepath.Glob(filepath.Join(outside, "*-owned.md"))
	assert.Len(t, remainingOutside, 1, "a spec in an escaping output dir must be left in place")
	// Nothing was moved into this task's artefacts.
	moved, _ := filepath.Glob(filepath.Join(layout.TasksDir(root), "error", res.TaskID, "artifacts", "*-owned.md"))
	assert.Empty(t, moved, "a spec whose path escapes the project must never be quarantined")
	assert.Contains(t, res.Summary, "outside the project or cannot be safely resolved",
		"the path-containment message must be reported")
}

// TestBUG203_Quarantine_RenamedLayout covers REV-209 R2-CLAUDE-002: with a renamed GTMS
// parent, the quarantine artefact lands under the renamed tasks folder (layout.TasksDir),
// and no stray gtms/ tree is created.
func TestBUG203_Quarantine_RenamedLayout(t *testing.T) {
	skipIfShort(t)
	if runtime.GOOS == "windows" {
		t.Skip("Tier 2 sh adapter test")
	}

	layout.InitFromParent("testing")
	defer layout.InitFromParent("gtms")

	root := t.TempDir()
	for _, dir := range []string{
		"testing/tasks/pending", "testing/tasks/complete", "testing/tasks/error",
		"testing/tasks/in-progress", "testing/tasks/in-review",
		"testing/test/cases", "testing/automation",
		".gtms/results", ".gtms/worktrees", ".gtms/logs", "testdata",
	} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "gtms.config"),
		[]byte("project:\n  name: Test\n  repo: org/test\n"), 0o644))

	scriptRel := "testdata/create-owned-prev2.sh"
	require.NoError(t, os.WriteFile(filepath.Join(root, scriptRel), []byte(createOwnedPrev2Script), 0o755))

	cfg := &config.Config{Project: config.ProjectConfig{Name: "Test", Repo: "org/test"}}
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "owned-prev2",
		Config:  &config.AdapterConfig{Mode: "sync", Script: scriptRel},
		Tier:    2,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "renamed", CommandFlags{Folder: "renamed"})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "error", res.Status)

	// The quarantined spec lands under the RENAMED tasks folder.
	moved, _ := filepath.Glob(filepath.Join(root, "testing", "tasks", "error", res.TaskID, "artifacts", "*-owned.md"))
	assert.Len(t, moved, 1, "the quarantine artefact must land under the renamed tasks folder")
	// No stray gtms/ tree was created.
	_, statErr := os.Stat(filepath.Join(root, "gtms"))
	assert.True(t, os.IsNotExist(statErr), "no stray gtms/ tree may be created in a renamed layout")
}

// --- REV-209 CODEX-001/CLAUDE-001: ownership gate ---

func TestBUG203_OwnedByBatch(t *testing.T) {
	batch := map[string]struct{}{"tc-12345678": {}, "tc-abcdef01": {}}
	assert.True(t, ownedByBatch("tc-12345678-slug.md", batch), "a batch id in a slugged filename is owned")
	assert.True(t, ownedByBatch("tc-abcdef01.md", batch), "a batch id in a bare filename is owned")
	assert.False(t, ownedByBatch("tc-ffffffff-foreign.md", batch), "an id not in the batch is not owned")
	assert.False(t, ownedByBatch("tc-1234567.md", batch), "a malformed (7-hex) filename yields no canonical id and is not owned")
	assert.False(t, ownedByBatch("tc-12345678foo.md", batch), "a malformed-shape filename is not owned")
}

// createStalePrev2HeaderScript is a Tier 2 create adapter that stamps the pre-v2 header
// (no gtms_schema) under the GTMS-generated id (what a real v0.3.x create script does, so
// the file is attributable to this invocation), then writes a clean handoff, exiting 0.
const createStalePrev2HeaderScript = `#!/bin/sh
mkdir -p "${GTMS_OUTPUT_DIR}"
ID=$(echo "$GTMS_TC_IDS" | cut -d',' -f1)
cat > "${GTMS_OUTPUT_DIR}/${ID}-stale.md" <<SPEC
---
test_case_id: ${ID}
title: "stale"
requirement:
priority: Medium
type: Functional
created: 2026-10-14
---
# Test
SPEC
{
  printf 'task: %s\n'    "${GTMS_TASK_ID}"
  printf 'command: %s\n' "${GTMS_COMMAND}"
  printf 'target: %s\n'  "${GTMS_REFERENCE}"
  printf 'adapter: stale-prev2\n'
  printf 'mode: sync\n'
  printf 'status: complete\n'
  printf 'result: pass\n'
  printf 'summary: "spec written"\n'
  printf 'completed: 2026-10-14T00:00:00Z\n'
} > "${GTMS_RESULT_FILE}"
exit 0
`

// TestBUG203_StaleScriptCreate_QuarantinesSpecAndNamesProducer is the invoker-level AC6 +
// AC10 check for a stale SHIPPED create script: a v0.3.x copy installed at the shipped path
// (gtms/adapters/manual-create-script.sh, configured as manual-create-script) emits an owned
// pre-v2 spec; it is quarantined, and the failure names that shipped producer and the
// actionable preset refresh command (REV-209 R2-CODEX-003: only a shipped upgradeable
// producer gets scaffold-refresh guidance -- the custom-adapter cases are covered by the
// no-refresh tests above).
func TestBUG203_StaleScriptCreate_QuarantinesSpecAndNamesProducer(t *testing.T) {
	skipIfShort(t)
	if runtime.GOOS == "windows" {
		t.Skip("Tier 2 sh adapter tests require a Unix shell")
	}

	root := setupTestProject(t)

	// Install the stale v0.3.x create script at the SHIPPED path so the attribution rule
	// recognises it as an upgradeable scaffold asset (not custom code).
	scriptRel := "gtms/adapters/manual-create-script.sh"
	scriptPath := filepath.Join(root, filepath.FromSlash(scriptRel))
	require.NoError(t, os.MkdirAll(filepath.Dir(scriptPath), 0o755))
	require.NoError(t, os.WriteFile(scriptPath, []byte(createStalePrev2HeaderScript), 0o755))

	outputDir := filepath.Join(root, "gtms/test/cases", "bug203stale")
	require.NoError(t, os.MkdirAll(outputDir, 0o755))

	cfg := &config.Config{Project: config.ProjectConfig{Name: "Test", Repo: "org/test"}}
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "manual-create-script",
		Config:  &config.AdapterConfig{Mode: "sync", Script: scriptRel},
		Tier:    2,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "bug203stale", CommandFlags{Folder: "bug203stale"})
	require.NoError(t, err)
	require.NotNil(t, res)

	assert.Equal(t, "error", res.Status)
	// AC6: the failure names the shipped producing script and the actionable preset command.
	assert.Contains(t, res.Summary, scriptRel,
		"the failure must name the stale shipped producing script")
	assert.Contains(t, res.Summary, "gtms init --force --preset",
		"a shipped scaffolded create script is refreshed by the preset form")
	assert.Contains(t, res.Summary, "manual, bats, or playwright",
		"the hint must offer the three preset choices")
	// AC10: the failure headline and the preserved-at line.
	assert.Contains(t, res.Summary, "Created spec failed validation.")
	assert.Contains(t, res.Summary, "Rejected output preserved at:")

	// AC10: the attributable invalid spec leaves the cases tree (no *-stale.md remains)
	// and is preserved in the error artefacts folder byte-for-byte.
	remaining, _ := filepath.Glob(filepath.Join(outputDir, "*-stale.md"))
	assert.Empty(t, remaining, "the attributable invalid spec must leave the cases tree")
	quarantined, _ := filepath.Glob(filepath.Join(layout.TasksDir(root), "error", res.TaskID, "artifacts", "*-stale.md"))
	require.Len(t, quarantined, 1, "the invalid spec must be preserved in the error artefacts folder")
	qbytes, qerr := os.ReadFile(quarantined[0])
	require.NoError(t, qerr)
	assert.Contains(t, string(qbytes), `title: "stale"`,
		"the quarantined spec must be preserved byte-for-byte")
}

// createMixedValidAndInvalidScript writes ONE valid spec and TWO invalid specs, all three
// carrying GENERATED batch ids (so all three are attributable to this invocation). The two
// invalid ones carry a frontmatter id that differs from their generated filename id
// (mismatch), so they are rejected and -- being attributable -- quarantined.
const createMixedValidAndInvalidScript = `#!/bin/sh
ID1=$(echo "$GTMS_TC_IDS" | cut -d',' -f1)
ID2=$(echo "$GTMS_TC_IDS" | cut -d',' -f2)
ID3=$(echo "$GTMS_TC_IDS" | cut -d',' -f3)
cat > "$GTMS_OUTPUT_DIR/${ID1}-good.md" <<EOF
---
gtms_schema: "test-case/v2"
type: Test Case Spec
test_type: Functional
test_case_id: ${ID1}
title: good
---
# Good
EOF
cat > "$GTMS_OUTPUT_DIR/${ID2}-bad1.md" <<EOF
---
gtms_schema: "test-case/v2"
type: Test Case Spec
test_type: Functional
test_case_id: tc-bbbbbbbb
title: bad1
---
# Bad1
EOF
cat > "$GTMS_OUTPUT_DIR/${ID3}-bad2.md" <<EOF
---
gtms_schema: "test-case/v2"
type: Test Case Spec
test_type: Functional
test_case_id: tc-dddddddd
title: bad2
---
# Bad2
EOF
{
  printf 'task: %s\n'    "${GTMS_TASK_ID}"
  printf 'command: %s\n' "${GTMS_COMMAND}"
  printf 'target: %s\n'  "${GTMS_REFERENCE}"
  printf 'adapter: mixed\n'
  printf 'mode: sync\n'
  printf 'status: complete\n'
  printf 'result: pass\n'
  printf 'summary: "mixed"\n'
  printf 'completed: 2026-10-15T00:00:00Z\n'
} > "${GTMS_RESULT_FILE}"
exit 0
`

// TestBUG203_MixedValidAndInvalid_QuarantinesOnlyInvalid covers AC11: a single create that
// produces one valid and two invalid (but attributable) specs keeps the valid spec in
// gtms/test/cases/, quarantines both invalid specs byte-for-byte, fails overall, and
// reports one "Rejected output preserved at:" line per quarantined artefact.
func TestBUG203_MixedValidAndInvalid_QuarantinesOnlyInvalid(t *testing.T) {
	skipIfShort(t)
	if runtime.GOOS == "windows" {
		t.Skip("Tier 2 sh adapter tests require a Unix shell")
	}

	root := setupTestProject(t)

	scriptRel := "testdata/create-mixed.sh"
	scriptPath := filepath.Join(root, scriptRel)
	require.NoError(t, os.MkdirAll(filepath.Dir(scriptPath), 0o755))
	require.NoError(t, os.WriteFile(scriptPath, []byte(createMixedValidAndInvalidScript), 0o755))

	outputDir := filepath.Join(root, "gtms/test/cases", "bug203mixed")
	require.NoError(t, os.MkdirAll(outputDir, 0o755))

	cfg := &config.Config{Project: config.ProjectConfig{Name: "Test", Repo: "org/test"}}
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "mixed",
		Config:  &config.AdapterConfig{Mode: "sync", Script: scriptRel},
		Tier:    2,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "bug203mixed", CommandFlags{Folder: "bug203mixed"})
	require.NoError(t, err)
	require.NotNil(t, res)

	// Overall failure.
	assert.Equal(t, "error", res.Status)
	assert.Contains(t, res.Summary, "Created spec failed validation.")

	// The valid spec stays in the cases tree.
	goods, _ := filepath.Glob(filepath.Join(outputDir, "*-good.md"))
	require.Len(t, goods, 1, "the one valid spec must remain in gtms/test/cases/")

	// Both invalid specs leave the cases tree and are preserved in the artefacts folder.
	badsInCases, _ := filepath.Glob(filepath.Join(outputDir, "*-bad*.md"))
	assert.Empty(t, badsInCases, "both invalid specs must leave the cases tree")
	artifactsDir := filepath.Join(root, "gtms", "tasks", "error", res.TaskID, "artifacts")
	bad1, _ := filepath.Glob(filepath.Join(artifactsDir, "*-bad1.md"))
	bad2, _ := filepath.Glob(filepath.Join(artifactsDir, "*-bad2.md"))
	assert.Len(t, bad1, 1, "bad1 must be quarantined")
	assert.Len(t, bad2, 1, "bad2 must be quarantined")

	// One preserved-at line per quarantined artefact (two).
	preservedLines := 0
	for _, line := range strings.Split(res.Summary, "\n") {
		if strings.Contains(line, "Rejected output preserved at:") {
			preservedLines++
		}
	}
	assert.Equal(t, 2, preservedLines, "one Rejected output preserved at: line per quarantined artefact")
}

// createForeignInvalidScript writes one valid owned spec plus one INVALID spec whose
// filename id is NOT in this invocation's batch (a stand-in for a concurrent sibling
// create's output). The foreign file must be reported but never moved.
const createForeignInvalidScript = `#!/bin/sh
ID=$(echo "$GTMS_TC_IDS" | cut -d',' -f1)
cat > "$GTMS_OUTPUT_DIR/${ID}-good.md" <<EOF
---
gtms_schema: "test-case/v2"
type: Test Case Spec
test_type: Functional
test_case_id: ${ID}
title: good
---
# Good
EOF
cat > "$GTMS_OUTPUT_DIR/tc-ffffffff-foreign.md" <<EOF
---
gtms_schema: "test-case/v2"
type: Test Case Spec
test_type: Functional
test_case_id: tc-eeeeeeee
title: foreign
---
# Foreign
EOF
{
  printf 'task: %s\n'    "${GTMS_TASK_ID}"
  printf 'command: %s\n' "${GTMS_COMMAND}"
  printf 'target: %s\n'  "${GTMS_REFERENCE}"
  printf 'adapter: foreign\n'
  printf 'mode: sync\n'
  printf 'status: complete\n'
  printf 'result: pass\n'
  printf 'summary: "foreign"\n'
  printf 'completed: 2026-10-16T00:00:00Z\n'
} > "${GTMS_RESULT_FILE}"
exit 0
`

// TestBUG203_ForeignInvalidFile_ReportedNotMoved is the REV-209 CODEX-001/CLAUDE-001
// concurrency regression: a newly appeared invalid file whose id is not in this
// invocation's batch (as a concurrent sibling create's output would be) is reported but
// NEVER moved out of the cases tree into this task's artefacts.
func TestBUG203_ForeignInvalidFile_ReportedNotMoved(t *testing.T) {
	skipIfShort(t)
	if runtime.GOOS == "windows" {
		t.Skip("Tier 2 sh adapter tests require a Unix shell")
	}

	root := setupTestProject(t)

	scriptRel := "testdata/create-foreign.sh"
	scriptPath := filepath.Join(root, scriptRel)
	require.NoError(t, os.MkdirAll(filepath.Dir(scriptPath), 0o755))
	require.NoError(t, os.WriteFile(scriptPath, []byte(createForeignInvalidScript), 0o755))

	outputDir := filepath.Join(root, "gtms/test/cases", "bug203foreign")
	require.NoError(t, os.MkdirAll(outputDir, 0o755))

	cfg := &config.Config{Project: config.ProjectConfig{Name: "Test", Repo: "org/test"}}
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "foreign",
		Config:  &config.AdapterConfig{Mode: "sync", Script: scriptRel},
		Tier:    2,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "bug203foreign", CommandFlags{Folder: "bug203foreign"})
	require.NoError(t, err)
	require.NotNil(t, res)

	assert.Equal(t, "error", res.Status, "a foreign invalid file still fails the create")

	// The foreign file is NOT moved: it stays in the cases tree and never lands in this
	// task's artefacts directory.
	assert.FileExists(t, filepath.Join(outputDir, "tc-ffffffff-foreign.md"),
		"an unattributable foreign file must be left in place, never moved")
	assert.NoFileExists(t, filepath.Join(root, "gtms", "tasks", "error", res.TaskID, "artifacts", "tc-ffffffff-foreign.md"),
		"an unattributable foreign file must never be quarantined into this task's artefacts")

	// The error reports it with the ownership-unproven guidance.
	assert.Contains(t, res.Summary, "could not prove this create owns it",
		"the foreign file must be reported with the ownership-unproven guidance")
	assert.Contains(t, res.Summary, "tc-ffffffff-foreign.md")
}

// createSymlinkedSpecsScript writes an external bad-header target, then two OWNED rejected
// specs that are SYMLINKS to it -- one absolute, one relative. Both must be left in place
// (REV-209 R3-CODEX-004): moving a symlink would preserve the link, not the validated bytes,
// and resolving an in-project symlink would make its external target the thing that moves.
const createSymlinkedSpecsScript = `#!/bin/sh
mkdir -p "${GTMS_OUTPUT_DIR}"
ID1=$(printf '%s' "$GTMS_TC_IDS" | cut -d, -f1)
ID2=$(printf '%s' "$GTMS_TC_IDS" | cut -d, -f2)
# External target with a stale (pre-v2) header, so reading it is a validation failure.
printf '%s\n' '---' 'test_case_id: tc-99999999' 'type: Functional' '---' '# Target' > "${GTMS_PROJECT_ROOT}/ext-target.md"
ln -s "${GTMS_PROJECT_ROOT}/ext-target.md" "${GTMS_OUTPUT_DIR}/${ID1}-abslink.md"
ln -s "../../../../ext-target.md" "${GTMS_OUTPUT_DIR}/${ID2}-rellink.md"
{
  printf 'task: %s\n'    "${GTMS_TASK_ID}"
  printf 'command: create\n'
  printf 'target: %s\n'  "${GTMS_REFERENCE}"
  printf 'adapter: symlinked\n'
  printf 'mode: sync\n'
  printf 'status: complete\n'
  printf 'result: pass\n'
  printf 'summary: "written"\n'
  printf 'completed: 2026-10-16T00:00:00Z\n'
} > "${GTMS_RESULT_FILE}"
exit 0
`

// TestBUG203_SymlinkedSpecs_LeftInPlace covers REV-209 R3-CODEX-004: an owned rejected spec
// that is a symlink (absolute or relative) is never quarantined -- it is left in place with
// the "not a regular file" diagnostic, and its external target is untouched.
func TestBUG203_SymlinkedSpecs_LeftInPlace(t *testing.T) {
	skipIfShort(t)
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlink + Tier 2 sh adapter test")
	}

	root := setupTestProject(t)

	scriptRel := "testdata/create-symlinked.sh"
	scriptPath := filepath.Join(root, scriptRel)
	require.NoError(t, os.MkdirAll(filepath.Dir(scriptPath), 0o755))
	require.NoError(t, os.WriteFile(scriptPath, []byte(createSymlinkedSpecsScript), 0o755))

	outputDir := filepath.Join(root, "gtms/test/cases", "bug203symlink")
	require.NoError(t, os.MkdirAll(outputDir, 0o755))

	cfg := &config.Config{Project: config.ProjectConfig{Name: "Test", Repo: "org/test"}}
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "symlinked",
		Config:  &config.AdapterConfig{Mode: "sync", Script: scriptRel},
		Tier:    2,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "bug203symlink", CommandFlags{Folder: "bug203symlink"})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "error", res.Status)

	// Both symlink specs are left in place; neither is quarantined.
	for _, name := range []string{"abslink.md", "rellink.md"} {
		matches, _ := filepath.Glob(filepath.Join(outputDir, "*-"+name))
		assert.Len(t, matches, 1, "the symlink spec %s must be left in place", name)
		moved, _ := filepath.Glob(filepath.Join(layout.TasksDir(root), "error", res.TaskID, "artifacts", "*-"+name))
		assert.Empty(t, moved, "a symlink spec must never be quarantined")
	}
	assert.Contains(t, res.Summary, "it is not a regular file",
		"a symlink spec must be reported with the not-a-regular-file diagnostic")
	// The external target is untouched.
	targetBytes, terr := os.ReadFile(filepath.Join(root, "ext-target.md"))
	require.NoError(t, terr)
	assert.Contains(t, string(targetBytes), "# Target", "the symlink target must be untouched")
}

// TestBUG203_TaskReadersIgnoreArtifactSubfolder confirms the quarantine path is safe: a
// spec sitting under gtms/tasks/error/<task-id>/artifacts/ is never read as a task file,
// so status -r / map / gaps keep working. Pure unit test (no shell).
func TestBUG203_TaskReadersIgnoreArtifactSubfolder(t *testing.T) {
	root := setupTestProject(t)

	// A legitimate error task file at the top level of the error folder.
	errorDir := filepath.Join(root, "gtms", "tasks", "error")
	require.NoError(t, os.MkdirAll(errorDir, 0o755))
	taskFile := "task-a3f72b10-create-feature.md"
	taskBody := "---\nid: task-a3f72b10\ntype: create\ntarget: feature\nadapter: x\nstatus: error\ncreated: 2026-10-15T00:00:00Z\n---\n"
	require.NoError(t, os.WriteFile(filepath.Join(errorDir, taskFile), []byte(taskBody), 0o644))

	// A quarantined spec in the task's artifacts subfolder -- it must be ignored.
	artifactsDir := filepath.Join(errorDir, "task-a3f72b10", "artifacts")
	require.NoError(t, os.MkdirAll(artifactsDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(artifactsDir, "tc-aaaaaaaa-bad.md"),
		[]byte("---\ntest_case_id: tc-bbbbbbbb\n---\n# rejected\n"), 0o644))

	tasks, err := task.List(root, "error")
	require.NoError(t, err)
	require.Len(t, tasks, 1, "task.List must read only the top-level task file, not the quarantined spec")
	assert.Equal(t, "task-a3f72b10", tasks[0].ID)
}

// TestBUG203_QuarantineRejectedSpec_MovesAndPreserves exercises the move helper directly
// (pure Go, runs on every platform including Windows where the Tier 2 E2E tests skip).
func TestBUG203_QuarantineRejectedSpec_MovesAndPreserves(t *testing.T) {
	root := t.TempDir()
	outputDir := filepath.Join(root, "gtms", "test", "cases", "feature")
	require.NoError(t, os.MkdirAll(outputDir, 0o755))
	const body = "---\ntest_case_id: tc-bbbbbbbb\n---\n# rejected body\n"
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "tc-aaaaaaaa-bad.md"), []byte(body), 0o644))

	artifactsDir := filepath.Join(root, "gtms", "tasks", "error", "task-a3f72b10", "artifacts")
	// REV-209 R3-CODEX-003: the helper takes the already-resolved src and dest paths.
	dest, ok, err := quarantineRejectedSpec(root, filepath.Join(outputDir, "tc-aaaaaaaa-bad.md"), filepath.Join(artifactsDir, "tc-aaaaaaaa-bad.md"))
	require.NoError(t, err)
	require.True(t, ok, "a successful move must report ok=true")

	assert.Equal(t, "gtms/tasks/error/task-a3f72b10/artifacts/tc-aaaaaaaa-bad.md", dest,
		"the returned path must be project-relative with forward slashes")
	assert.NoFileExists(t, filepath.Join(outputDir, "tc-aaaaaaaa-bad.md"),
		"the spec must be moved out of the cases tree")
	moved, rerr := os.ReadFile(filepath.Join(artifactsDir, "tc-aaaaaaaa-bad.md"))
	require.NoError(t, rerr)
	assert.Equal(t, body, string(moved), "the quarantined spec must be preserved byte-for-byte")

	// A missing source is reported, not a panic or an empty-dir write.
	_, ok2, missErr := quarantineRejectedSpec(root, filepath.Join(outputDir, "tc-ffffffff-absent.md"), filepath.Join(artifactsDir, "tc-ffffffff-absent.md"))
	assert.False(t, ok2)
	assert.Error(t, missErr)
}

// TestBUG203_QuarantineRejectedSpec_FailureIsHonest covers REV-209 CODEX-002/CLAUDE-004:
// when the move cannot complete, the helper returns ok=false and an error, so the caller
// never claims preservation. A regular file planted at the artefacts-directory path makes
// MkdirAll fail, and the source must remain untouched.
func TestBUG203_QuarantineRejectedSpec_FailureIsHonest(t *testing.T) {
	root := t.TempDir()
	outputDir := filepath.Join(root, "gtms", "test", "cases", "feature")
	require.NoError(t, os.MkdirAll(outputDir, 0o755))
	src := filepath.Join(outputDir, "tc-aaaaaaaa-bad.md")
	require.NoError(t, os.WriteFile(src, []byte("body\n"), 0o644))

	// Plant a FILE where the artefacts DIRECTORY needs to be, so MkdirAll fails.
	errorDir := filepath.Join(root, "gtms", "tasks", "error", "task-a3f72b10")
	require.NoError(t, os.MkdirAll(errorDir, 0o755))
	blocker := filepath.Join(errorDir, "artifacts")
	require.NoError(t, os.WriteFile(blocker, []byte("not a dir"), 0o644))

	// dest is under the blocker file, so MkdirAll(filepath.Dir(dest)) fails.
	dest, ok, err := quarantineRejectedSpec(root, src, filepath.Join(blocker, "tc-aaaaaaaa-bad.md"))
	assert.False(t, ok, "a failed move must report ok=false")
	assert.Error(t, err, "a failed move must return an error")
	assert.Empty(t, dest)
	assert.FileExists(t, src, "the source must remain in place when the move fails")
}

// TestBUG203_QuarantineRejectedSpec_RejectsNonRegular covers REV-209 R3-CODEX-004 at the
// helper level: a symlink source is never moved (the link, not the validated bytes).
func TestBUG203_QuarantineRejectedSpec_RejectsNonRegular(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlink test")
	}
	root := t.TempDir()
	outputDir := filepath.Join(root, "gtms", "test", "cases", "feature")
	require.NoError(t, os.MkdirAll(outputDir, 0o755))
	target := filepath.Join(root, "secret.md")
	require.NoError(t, os.WriteFile(target, []byte("secret\n"), 0o644))
	link := filepath.Join(outputDir, "tc-aaaaaaaa-link.md")
	require.NoError(t, os.Symlink(target, link))

	artifactsDir := filepath.Join(root, "gtms", "tasks", "error", "task-a3f72b10", "artifacts")
	_, ok, err := quarantineRejectedSpec(root, link, filepath.Join(artifactsDir, "tc-aaaaaaaa-link.md"))
	assert.False(t, ok, "a symlink source must not be quarantined")
	assert.Error(t, err)
	assert.FileExists(t, link, "the symlink must be left in place")
	assert.FileExists(t, target, "the symlink's target must be untouched")
}

// TestBUG203_QuarantineCreateFailureSummary_Shape pins the summary layout: the fixed
// headline, one preserved-at line per quarantined artefact, one line per unattributable
// file left in place (with the exact inspection guidance), one per failed move (naming the
// file's actual location), then the per-violation reason detail.
func TestBUG203_QuarantineCreateFailureSummary_Shape(t *testing.T) {
	violations := []SpecValidationError{
		{File: "tc-aaaaaaaa-bad1.md", Reason: "frontmatter test_case_id 'tc-bbbbbbbb' does not match filename ID 'tc-aaaaaaaa'"},
		{File: "tc-cccccccc-bad2.md", Reason: "frontmatter test_case_id 'tc-dddddddd' does not match filename ID 'tc-cccccccc'"},
	}
	preserved := []string{"gtms/tasks/error/task-x/artifacts/tc-aaaaaaaa-bad1.md"}
	// REV-209 R3-CLAUDE-001: an unattributable filename carrying a control character (legal
	// on Linux) must be sanitised when rendered.
	unattributable := []string{"gtms/test/cases/foo/tc-ffffffff-\x1b[31mevil.md"}
	notRegular := []string{"gtms/test/cases/foo/tc-11111111-link.md"}
	pathUnsafe := []string{"../shared/tc-eeeeeeee-escape.md"}
	moveFailed := []quarantineFailure{{path: "gtms/test/cases/foo/tc-cccccccc-bad2.md", err: assertErr("disk full")}}

	summary := quarantineCreateFailureSummary(violations, preserved, unattributable, notRegular, pathUnsafe, moveFailed)

	assert.True(t, strings.HasPrefix(summary, "Created spec failed validation."),
		"the headline must lead the summary")
	assert.Contains(t, summary, "Rejected output preserved at: gtms/tasks/error/task-x/artifacts/tc-aaaaaaaa-bad1.md")
	assert.Contains(t, summary, "Rejected output was left in place because GTMS could not prove this create owns it:")
	assert.Contains(t, summary, "Rejected output was left in place because it is not a regular file: gtms/test/cases/foo/tc-11111111-link.md.")
	assert.Contains(t, summary, "Rejected output was left in place because its path is outside the project or cannot be safely resolved: ../shared/tc-eeeeeeee-escape.md.")
	assert.Contains(t, summary, "Rejected output could not be preserved and remains at: gtms/test/cases/foo/tc-cccccccc-bad2.md")
	assert.Contains(t, summary, "does not match filename ID",
		"the per-violation reason detail is kept so a non-stale failure still says why")
	// R3-CLAUDE-001: the raw control character must NOT reach the rendered summary.
	assert.NotContains(t, summary, "\x1b", "adapter-controlled filenames must be sanitised when rendered")
}

type assertErr string

func (e assertErr) Error() string { return string(e) }
