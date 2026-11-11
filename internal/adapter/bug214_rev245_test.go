package adapter

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// REV-245 (BUG-214 Owner Decision 13 / AC16): when the project is reached through a
// symlink or junction, create's canonical output directory must NOT cause the result
// contract's artefact value or InvokeResult.ArtifactPaths to escape the root with
// "../". Both detection routes are covered: adapter-reported SavedFiles (Tier 0
// built-in create) and a direct write discovered by scanning the output directory
// (Tier 2 script).

// linkedProject builds <parent>/project (with the dirs a create invocation needs and a
// gtms.config) plus <parent>/project-link -> project, and returns the LINK path to use
// as the invocation project root and the canonical cases/<folder> output directory the
// admission boundary would have produced.
func linkedProject(t *testing.T, folder string) (linkRoot, canonicalOutputDir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlinks unsupported on this platform; Windows junction coverage is Pester (BUG-214 D10)")
	}
	parent := t.TempDir()
	project := filepath.Join(parent, "project")
	for _, d := range []string{
		"gtms/tasks/pending", "gtms/tasks/complete", "gtms/tasks/error",
		"gtms/tasks/in-progress", "gtms/tasks/in-review",
		"gtms/test/cases", "gtms/automation",
		".gtms/results", ".gtms/worktrees", ".gtms/logs",
	} {
		require.NoError(t, os.MkdirAll(filepath.Join(project, d), 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(project, "gtms.config"),
		[]byte("project:\n  name: Test\n  repo: org/test\n"), 0o644))

	linkRoot = filepath.Join(parent, "project-link")
	if err := os.Symlink(project, linkRoot); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	// The admission boundary resolves cases/<folder> under the canonical project root.
	canonicalProject, err := filepath.EvalSymlinks(project)
	require.NoError(t, err)
	canonicalOutputDir = filepath.Join(canonicalProject, "gtms", "test", "cases", folder)
	require.NoError(t, os.MkdirAll(canonicalOutputDir, 0o755))
	return linkRoot, canonicalOutputDir
}

func assertProjectRelative(t *testing.T, label string, paths []string) {
	t.Helper()
	require.NotEmpty(t, paths, "%s: expected at least one artefact path", label)
	for _, p := range paths {
		assert.NotContains(t, p, "..", "%s: artefact path %q must stay project-relative", label, p)
		assert.False(t, filepath.IsAbs(p), "%s: artefact path %q must be relative", label, p)
		assert.True(t, strings.HasPrefix(p, "gtms/test/cases/"),
			"%s: artefact path %q should be under the test cases dir", label, p)
	}
}

func readContractArtefact(t *testing.T, projectRoot, taskID string) string {
	t.Helper()
	rc, err := result.Read(filepath.Join(projectRoot, ".gtms", "results", taskID+".handoff.yaml"))
	require.NoError(t, err)
	return rc.Artefact
}

// Route 1: adapter-reported SavedFiles (Tier 0 built-in create).
func TestInvokeWithRoot_Create_LinkedProject_SavedFiles_NoDotDot(t *testing.T) {
	skipIfShort(t)
	linkRoot, canonicalOut := linkedProject(t, "feature")

	cfg := &config.Config{Project: config.ProjectConfig{Name: "Test", Repo: "org/test"}}
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "manual-create",
		Config:  &config.AdapterConfig{Mode: "sync"}, // Tier 0: no command/script/module
		Tier:    0,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), linkRoot, cfg, resolved, "feature",
		CommandFlags{Folder: "feature", Name: "via-link", ResolvedOutputDir: canonicalOut})
	require.NoError(t, err)
	require.Equal(t, "complete", res.Status)

	assertProjectRelative(t, "SavedFiles ArtifactPaths", res.ArtifactPaths)

	contractArtefact := readContractArtefact(t, linkRoot, res.TaskID)
	assert.NotContains(t, contractArtefact, "..", "contract artefact must stay project-relative")
	assert.True(t, strings.HasPrefix(contractArtefact, "gtms/test/cases/"),
		"contract artefact %q should be under the test cases dir", contractArtefact)
}

// Route 2: direct write found by scanning the output directory (Tier 2 script that
// writes into $GTMS_OUTPUT_DIR without emitting streaming markers or a contract).
func TestInvokeWithRoot_Create_LinkedProject_ScanOutputDir_NoDotDot(t *testing.T) {
	skipIfShort(t)
	linkRoot, canonicalOut := linkedProject(t, "feature")

	scriptDir := filepath.Join(canonicalOut, "..", "..", "..", "adapters") // project/gtms/adapters
	require.NoError(t, os.MkdirAll(scriptDir, 0o755))
	scriptPath := filepath.Join(scriptDir, "direct-write.sh")
	// Write one VALID v2 spec directly into the output dir -- no contract, no streaming
	// markers -- so detection falls to scanOutputDir. The id must be one GTMS generated
	// for this invocation (GTMS_TC_IDS) and must match the filename, or create's
	// post-write validator (BUG-038) rejects it before the path is recorded.
	// Heredoc (not printf): a format string beginning with "---" is read as options
	// by dash's printf. The id comes from GTMS_TC_IDS so the spec is batch-owned.
	script := "#!/bin/sh\n" +
		"mkdir -p \"$GTMS_OUTPUT_DIR\"\n" +
		"ID=$(echo \"$GTMS_TC_IDS\" | cut -d, -f1)\n" +
		"cat > \"$GTMS_OUTPUT_DIR/$ID-direct.md\" <<EOF\n" +
		"---\n" +
		"gtms_schema: \"test-case/v2\"\n" +
		"type: Test Case Spec\n" +
		"test_type: Functional\n" +
		"test_case_id: $ID\n" +
		"title: Direct\n" +
		"---\n" +
		"EOF\n"
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o755))

	cfg := &config.Config{Project: config.ProjectConfig{Name: "Test", Repo: "org/test"}}
	resolved := &ResolvedAdapter{
		Command: "create",
		Name:    "direct-tier2",
		Config:  &config.AdapterConfig{Mode: "sync", Script: "gtms/adapters/direct-write.sh"},
		Tier:    2,
		Mode:    "sync",
	}

	res, err := InvokeWithRoot(context.Background(), linkRoot, cfg, resolved, "feature",
		CommandFlags{Folder: "feature", ResolvedOutputDir: canonicalOut})
	require.NoError(t, err)
	require.Equal(t, "complete", res.Status, "summary: %s", res.Summary)

	assertProjectRelative(t, "scanOutputDir ArtifactPaths", res.ArtifactPaths)

	contractArtefact := readContractArtefact(t, linkRoot, res.TaskID)
	assert.NotContains(t, contractArtefact, "..", "contract artefact must stay project-relative")
	assert.True(t, strings.HasPrefix(contractArtefact, "gtms/test/cases/"),
		"contract artefact %q should be under the test cases dir", contractArtefact)
}

// REV-245 delta round 2 (D2-CLAUDE-001): canonicalProjectRel must NOT resolve an
// in-project symlink in the artefact path. A Tier 0 built-in automate whose
// output-dir runs through an in-project symlinked folder must record the UNRESOLVED
// link-form artefact, so it matches the pending wiring BuiltinAutomate wrote (plain
// filepath.Rel) and the pending sentinel is preserved (AC15 / Owner Decision 11).
func TestInvokeWithRoot_Automate_Tier0_InProjectSymlinkOutputDir_WiringSentinelPreserved(t *testing.T) {
	skipIfShort(t)
	if runtime.GOOS == "windows" {
		t.Skip("symlinks unsupported on this platform; Windows junction coverage is Pester (BUG-214 D10)")
	}
	root, cfg := setupAutomateTestProject(t)

	// In-project symlinked output folder: link-specs -> real-specs.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "real-specs"), 0o755))
	if err := os.Symlink(filepath.Join(root, "real-specs"), filepath.Join(root, "link-specs")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	resolved := &ResolvedAdapter{
		Command: "automate", Name: "agent-automate",
		Config: &config.AdapterConfig{Mode: "sync", OutputDir: "link-specs"},
		Tier:   0, Mode: "sync",
	}
	flags := CommandFlags{Framework: "playwright", Owner: ownerForTest(t, root, "tc-auto")}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-auto", flags)
	require.NoError(t, err)

	rec, _, err := wiring.Find(root, "tc-auto", "playwright")
	require.NoError(t, err)
	require.NotNil(t, rec)

	// The pending sentinel survives: with the old whole-path EvalSymlinks the contract
	// artefact became "real-specs/..." and no longer matched the "link-specs/..." wiring,
	// so WriteAutomateWiring overwrote the pending hash with a real one.
	assert.True(t, wiring.IsPendingArtefactHash(rec.ArtefactHash),
		"pending sentinel must survive an in-project symlinked output-dir")
	assert.Equal(t, "link-specs/tc-auto-test.spec.ts", rec.Artefact,
		"wiring artefact must stay the unresolved link-form path")

	// The contract artefact equals the wiring artefact (the match the guard needs).
	rc, err := result.Read(filepath.Join(root, ".gtms", "results", res.TaskID+".handoff.yaml"))
	require.NoError(t, err)
	assert.Equal(t, rec.Artefact, rc.Artefact,
		"contract artefact must equal the pending wiring artefact")
}

// REV-245 delta round 2 (D2-CODEX-001): scanOutputDir must record a symlink artefact
// as the ENTRY that was created, not its target. An in-project target must not be
// misidentified as some other project-relative path.
func TestScanOutputDir_SymlinkArtefact_InProjectTarget_RecordedAsEntry(t *testing.T) {
	skipIfShort(t)
	if runtime.GOOS == "windows" {
		t.Skip("symlinks unsupported on this platform; Windows junction coverage is Pester (BUG-214 D10)")
	}
	root := t.TempDir()
	outDir := filepath.Join(root, "gtms", "test", "cases", "feature")
	require.NoError(t, os.MkdirAll(outDir, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "other"), 0o755))
	target := filepath.Join(root, "other", "target.md")
	require.NoError(t, os.WriteFile(target, []byte("x"), 0o644))
	if err := os.Symlink(target, filepath.Join(outDir, "linkentry.md")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	got := scanOutputDir(root, outDir, nil)
	assert.Contains(t, got, "gtms/test/cases/feature/linkentry.md",
		"the symlink entry's own path must be recorded")
	assert.NotContains(t, got, "other/target.md",
		"the symlink target must NOT be substituted for the entry")
	for _, p := range got {
		assert.NotContains(t, p, "..", "path %q must stay project-relative", p)
	}
}

// REV-245 delta round 2 (D2-CODEX-001): a symlink artefact whose target is OUTSIDE the
// project must still be recorded as the in-project entry path, never as a "../" path.
func TestScanOutputDir_SymlinkArtefact_ExternalTarget_NoDotDot(t *testing.T) {
	skipIfShort(t)
	if runtime.GOOS == "windows" {
		t.Skip("symlinks unsupported on this platform; Windows junction coverage is Pester (BUG-214 D10)")
	}
	root := t.TempDir()
	outDir := filepath.Join(root, "gtms", "test", "cases", "feature")
	require.NoError(t, os.MkdirAll(outDir, 0o755))

	external := t.TempDir() // outside root
	extFile := filepath.Join(external, "x.md")
	require.NoError(t, os.WriteFile(extFile, []byte("x"), 0o644))
	if err := os.Symlink(extFile, filepath.Join(outDir, "linkentry.md")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	got := scanOutputDir(root, outDir, nil)
	assert.Contains(t, got, "gtms/test/cases/feature/linkentry.md",
		"the symlink entry's own path must be recorded")
	for _, p := range got {
		assert.NotContains(t, p, "..", "external-target symlink must not yield a traversal path: %q", p)
		assert.False(t, filepath.IsAbs(p), "path %q must be relative", p)
	}
}

// REV-245 delta round 3 (D3-CODEX-001): a valid in-project folder whose NAME begins
// with two dots ("..link-specs") must take the plain lexical branch, not be mistaken
// for a parent-traversal escape. A Tier 0 automate whose output-dir is such a folder
// (itself an in-project symlink) must keep the lexical "..link-specs/..." artefact,
// matching the pending wiring, so the sentinel survives.
func TestInvokeWithRoot_Automate_Tier0_DotDotPrefixedSymlinkOutputDir_WiringSentinelPreserved(t *testing.T) {
	skipIfShort(t)
	if runtime.GOOS == "windows" {
		t.Skip("symlinks unsupported on this platform; Windows junction coverage is Pester (BUG-214 D10)")
	}
	root, cfg := setupAutomateTestProject(t)

	// In-project symlinked output folder whose name begins with two dots.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "real-specs"), 0o755))
	if err := os.Symlink(filepath.Join(root, "real-specs"), filepath.Join(root, "..link-specs")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	resolved := &ResolvedAdapter{
		Command: "automate", Name: "agent-automate",
		Config: &config.AdapterConfig{Mode: "sync", OutputDir: "..link-specs"},
		Tier:   0, Mode: "sync",
	}
	flags := CommandFlags{Framework: "playwright", Owner: ownerForTest(t, root, "tc-auto")}

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-auto", flags)
	require.NoError(t, err)

	rec, _, err := wiring.Find(root, "tc-auto", "playwright")
	require.NoError(t, err)
	require.NotNil(t, rec)

	assert.True(t, wiring.IsPendingArtefactHash(rec.ArtefactHash),
		"pending sentinel must survive a '..'-prefixed in-project output-dir name")
	assert.Equal(t, "..link-specs/tc-auto-test.spec.ts", rec.Artefact,
		"a '..'-prefixed name is not an escape; the lexical spelling must be kept")

	rc, err := result.Read(filepath.Join(root, ".gtms", "results", res.TaskID+".handoff.yaml"))
	require.NoError(t, err)
	assert.Equal(t, rec.Artefact, rc.Artefact,
		"contract artefact must equal the pending wiring artefact")
}

// REV-245 delta round 3 (D3-CLAUDE-001): exercise the FALLBACK branch of
// canonicalProjectRel with a symlink artefact. The project is reached through a
// symlink (so the plain lexical branch escapes and the fallback runs), and the output
// dir holds a symlink pointing OUTSIDE the project. scanOutputDir must record the
// entry's own project-relative path -- the final component is never resolved -- with
// no leading "..".
func TestScanOutputDir_Fallback_SymlinkArtefact_LinkedProject_RecordedAsEntry(t *testing.T) {
	skipIfShort(t)
	if runtime.GOOS == "windows" {
		t.Skip("symlinks unsupported on this platform; Windows junction coverage is Pester (BUG-214 D10)")
	}
	parent := t.TempDir()
	project := filepath.Join(parent, "project")
	outDirRel := filepath.Join("gtms", "test", "cases", "feature")
	require.NoError(t, os.MkdirAll(filepath.Join(project, outDirRel), 0o755))

	linkRoot := filepath.Join(parent, "project-link")
	if err := os.Symlink(project, linkRoot); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	canonicalProject, err := filepath.EvalSymlinks(project)
	require.NoError(t, err)
	canonicalOut := filepath.Join(canonicalProject, outDirRel)

	// A symlink entry in the output dir pointing OUTSIDE the project.
	external := t.TempDir()
	extFile := filepath.Join(external, "x.md")
	require.NoError(t, os.WriteFile(extFile, []byte("x"), 0o644))
	if err := os.Symlink(extFile, filepath.Join(canonicalOut, "linkentry.md")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	// projectRoot = the LINK (forces the fallback branch); outputDir = the canonical path.
	got := scanOutputDir(linkRoot, canonicalOut, nil)
	assert.Contains(t, got, "gtms/test/cases/feature/linkentry.md",
		"fallback must record the symlink entry's own in-project path")
	for _, p := range got {
		assert.NotContains(t, p, "..", "fallback must not emit a traversal path: %q", p)
		assert.False(t, filepath.IsAbs(p), "path %q must be relative", p)
	}
}
