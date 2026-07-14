package adapter

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/config"
)

// ENH-168: configurable adapter working-dir / RunDir.
//
// cwd assertions use a relative marker file written by the adapter ("ran-here"):
// because cmd.Dir == RunDir, a relative path lands in RunDir. This is robust across
// MSYS (/c/...) vs native (C:\...) path rendering, which a `pwd` comparison is not.

// TestAdapterFacingPath covers the path-contract helper (AC6): a project-relative
// carrier is absolutized only when working-dir has moved the cwd off the project root.
func TestAdapterFacingPath(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "harness")
	// Real carriers (findTestCaseSource / pathsafe.ResolveUnderRoot) are
	// filepath.ToSlash'd project-relative -- forward slash on every OS. Mirror that
	// shape rather than filepath.FromSlash, which would be unrealistic on Windows.
	rel := "gtms/test/cases/feature-a/tc-1.md"

	active := &AdapterContext{ProjectRoot: root, RunDir: sub}     // working-dir active
	inactive := &AdapterContext{ProjectRoot: root, RunDir: root}  // working-dir not set
	defensive := &AdapterContext{ProjectRoot: root, RunDir: ""}   // RunDir unset (defensive arm)

	// active: relative -> absolute under root, normalized to forward slash (BUG-126).
	assert.Equal(t, filepath.ToSlash(filepath.Join(root, rel)), adapterFacingPath(active, rel), "active: relative -> absolute, forward-slash")
	assert.Equal(t, rel, adapterFacingPath(inactive, rel), "inactive: unchanged (no regression)")
	assert.Equal(t, rel, adapterFacingPath(defensive, rel), "RunDir empty: unchanged")
	assert.Equal(t, "", adapterFacingPath(active, ""), "empty path: unchanged")
	// already-absolute: normalized to forward slash for the shell consumer (BUG-126).
	abs := filepath.Join(root, "already-abs.md")
	assert.Equal(t, filepath.ToSlash(abs), adapterFacingPath(active, abs), "already absolute: normalized to forward slash")
}

// TestBUG126_AdapterFacingPath_ForwardSlashOnWindows pins the separator contract:
// every path adapterFacingPath hands to an adapter is forward-slash, so a shell
// runner under Git Bash / MSYS can pass it to a CLI (e.g. `npx playwright test`)
// that rejects backslash paths on Windows. The NotContains("\\") assertions are
// no-ops on Unix and load-bearing on the Windows CI matrix -- that is intended (cf
// the file header note above): the bug only manifests on Windows.
func TestBUG126_AdapterFacingPath_ForwardSlashOnWindows(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "harness")
	active := &AdapterContext{ProjectRoot: root, RunDir: sub}    // working-dir active
	inactive := &AdapterContext{ProjectRoot: root, RunDir: root} // working-dir not set

	// Working-dir active: a project-relative carrier is absolutized via filepath.Join
	// (OS-native, backslash on Windows) and must come back forward-slash.
	got := adapterFacingPath(active, "gtms/test/cases/feature-a/tc-1.spec.ts")
	require.NotContains(t, got, "\\", "absolutized carrier must not contain a backslash separator")
	require.True(t, filepath.IsAbs(filepath.FromSlash(got)), "absolutized carrier is still an absolute path")

	// Already-absolute OS-native input (e.g. a user --artefact-file C:\...): the
	// single-point normalization must forward-slash it too.
	absNative := filepath.Join(root, "a", "b", "tc.spec.ts")
	gotAbs := adapterFacingPath(active, absNative)
	require.NotContains(t, gotAbs, "\\", "already-absolute carrier must not contain a backslash separator")

	// Flat layout (no working-dir): a forward-slash relative carrier is returned
	// unchanged -- no regression.
	relFwd := "gtms/test/cases/feature-a/tc-1.spec.ts"
	assert.Equal(t, relFwd, adapterFacingPath(inactive, relFwd), "flat layout: forward-slash carrier unchanged")
}

// TestBuildAdapterContext_RunDir covers the invoker computing RunDir = base [+ working-dir] (AC2/AC3).
func TestBuildAdapterContext_RunDir(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{Project: config.ProjectConfig{Name: "T", Repo: "o/r"}}
	resPath := filepath.Join(root, "r.yaml")

	withWD := &ResolvedAdapter{Command: "execute", Name: "runner", Tier: 1, Mode: "sync",
		Config: &config.AdapterConfig{WorkingDir: "harness"}}
	ac := buildAdapterContext(root, "task-1", withWD, "tc-1", CommandFlags{}, "br", cfg, root, resPath)
	assert.Equal(t, filepath.Join(root, "harness"), ac.RunDir, "working-dir set -> base joined with it")

	noWD := &ResolvedAdapter{Command: "execute", Name: "runner", Tier: 1, Mode: "sync",
		Config: &config.AdapterConfig{}}
	ac2 := buildAdapterContext(root, "task-2", noWD, "tc-1", CommandFlags{}, "br", cfg, root, resPath)
	assert.Equal(t, root, ac2.RunDir, "working-dir unset -> RunDir == project root (no regression)")
}

func runDirFixture(t *testing.T) (root, sub string) {
	t.Helper()
	root = t.TempDir()
	sub = filepath.Join(root, "harness")
	require.NoError(t, os.MkdirAll(sub, 0755))
	return root, sub
}

// TestInvokeTier1_RunsFromRunDir: a Tier-1 command runs from RunDir (AC2).
func TestInvokeTier1_RunsFromRunDir(t *testing.T) {
	skipIfShort(t)
	root, sub := runDirFixture(t)
	ac := &AdapterContext{TaskID: "task-wd1", Command: "execute", ProjectRoot: root, WorkDir: root, RunDir: sub}

	_, err := InvokeTier1(context.Background(), ac, `: > ran-here`)
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(sub, "ran-here"), "marker should be created in RunDir")
	assert.NoFileExists(t, filepath.Join(root, "ran-here"), "marker must NOT be in the project root")
}

// TestInvokeTier1_UnsetRunsFromProjectRoot: RunDir == ProjectRoot -> runs from root (AC3).
func TestInvokeTier1_UnsetRunsFromProjectRoot(t *testing.T) {
	skipIfShort(t)
	root, _ := runDirFixture(t)
	ac := &AdapterContext{TaskID: "task-wd2", Command: "execute", ProjectRoot: root, WorkDir: root, RunDir: root}

	_, err := InvokeTier1(context.Background(), ac, `: > ran-here`)
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(root, "ran-here"))
}

// TestInvokeTier2_RunsFromRunDir: a Tier-2 script runs from RunDir (AC2) -- the field
// Tier-2 previously sourced from ProjectRoot.
func TestInvokeTier2_RunsFromRunDir(t *testing.T) {
	skipIfShort(t)
	root, sub := runDirFixture(t)
	script := filepath.Join(root, "run.sh")
	require.NoError(t, os.WriteFile(script, []byte("#!/usr/bin/env sh\n: > ran-here\n"), 0755))
	ac := &AdapterContext{TaskID: "task-wd3", Command: "execute", ProjectRoot: root, WorkDir: root, RunDir: sub}

	_, err := InvokeTier2(context.Background(), ac, script)
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(sub, "ran-here"), "Tier-2 script should run from RunDir")
	assert.NoFileExists(t, filepath.Join(root, "ran-here"))
}

// TestTier1Tier2_CwdParity: given the same RunDir, both tiers run from the identical cwd,
// SET and UNSET -- guarding the WorkDir/ProjectRoot -> RunDir unification (AC4).
func TestTier1Tier2_CwdParity(t *testing.T) {
	skipIfShort(t)
	script := func(dir string) string {
		p := filepath.Join(dir, "run.sh")
		require.NoError(t, os.WriteFile(p, []byte("#!/usr/bin/env sh\n: > ran-here\n"), 0755))
		return p
	}

	// Round A: working-dir active (RunDir = subdir).
	rootA, subA := runDirFixture(t)
	ac1 := &AdapterContext{TaskID: "task-pa1", Command: "execute", ProjectRoot: rootA, WorkDir: rootA, RunDir: subA}
	_, err := InvokeTier1(context.Background(), ac1, `: > ran-here`)
	require.NoError(t, err)
	ac2 := &AdapterContext{TaskID: "task-pa2", Command: "execute", ProjectRoot: rootA, WorkDir: rootA, RunDir: subA}
	_, err = InvokeTier2(context.Background(), ac2, script(rootA))
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(subA, "ran-here"))
	assert.NoFileExists(t, filepath.Join(rootA, "ran-here"), "neither tier ran from root when working-dir active")

	// Round B: working-dir unset (RunDir == ProjectRoot) -> both run from root.
	rootB := t.TempDir()
	ac3 := &AdapterContext{TaskID: "task-pb1", Command: "execute", ProjectRoot: rootB, WorkDir: rootB, RunDir: rootB}
	_, err = InvokeTier1(context.Background(), ac3, `: > t1`)
	require.NoError(t, err)
	ac4 := &AdapterContext{TaskID: "task-pb2", Command: "execute", ProjectRoot: rootB, WorkDir: rootB, RunDir: rootB}
	_, err = InvokeTier2(context.Background(), ac4, script(rootB))
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(rootB, "t1"), "Tier-1 ran from project root when unset")
	assert.FileExists(t, filepath.Join(rootB, "ran-here"), "Tier-2 ran from project root when unset")
}

// TestInvokeTier1_SpacesInRunDir: a RunDir containing a space is honoured (cross-platform edge).
func TestInvokeTier1_SpacesInRunDir(t *testing.T) {
	skipIfShort(t)
	root := t.TempDir()
	sub := filepath.Join(root, "my harness")
	require.NoError(t, os.MkdirAll(sub, 0755))
	ac := &AdapterContext{TaskID: "task-sp1", Command: "execute", ProjectRoot: root, WorkDir: root, RunDir: sub}

	_, err := InvokeTier1(context.Background(), ac, `: > ran-here`)
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(sub, "ran-here"), "space-bearing RunDir should not break invocation")
}

// TestWorkDirVar_NotOverloaded: {work_dir} (Tier 1) still substitutes the WorkDir base, not
// RunDir, even when working-dir is active (AC5).
func TestWorkDirVar_NotOverloaded(t *testing.T) {
	skipIfShort(t)
	root, sub := runDirFixture(t)
	ac := &AdapterContext{TaskID: "task-wv1", Command: "execute", ProjectRoot: root, WorkDir: root, RunDir: sub}

	_, err := InvokeTier1(context.Background(), ac, `printf '%s' {work_dir} > wd.txt`)
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(sub, "wd.txt"))
	require.NoError(t, err)
	got := strings.TrimSpace(string(data))
	assert.Equal(t, root, got, "{work_dir} must remain the WorkDir base (project root)")
	assert.NotEqual(t, sub, got, "{work_dir} must NOT be overloaded with the run cwd")
}
