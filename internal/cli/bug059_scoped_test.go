package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/target"
)

// addEscapeSymlink adds an out-of-tree symlink scope `gtms/test/cases/escape`
// pointing to a sibling dir OUTSIDE the cases tree that contains a tc-abc12345
// spec. Returns after skipping on platforms without symlink support. REV-115: a
// qualified target through this symlink must be rejected (containment).
func addEscapeSymlink(t *testing.T, root string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privilege on Windows")
	}
	outside := filepath.Join(root, "outside-cases")
	require.NoError(t, os.MkdirAll(outside, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "tc-abc12345-out.md"),
		[]byte("---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-abc12345\n---\n"), 0o644))
	if err := os.Symlink(outside, filepath.Join(root, "gtms/test/cases/escape")); err != nil {
		t.Skipf("symlink unsupported in this environment: %v", err)
	}
}

// assertNoPipelineMutation fails if any task file or result contract was written.
func assertNoPipelineMutation(t *testing.T, root string) {
	t.Helper()
	tasks, _ := filepath.Glob(filepath.Join(root, "gtms", "tasks", "*", "task-*.md"))
	assert.Empty(t, tasks, "no task file may be written on a rejected out-of-tree target")
	results, _ := filepath.Glob(filepath.Join(root, ".gtms", "results", "*.handoff.yaml"))
	assert.Empty(t, results, "no result contract may be written on a rejected out-of-tree target")
}

// BUG-059 / REV-113 CODEX-001: target.Classify preserves the folder qualifier in
// Result.Target (case preserved) and the bare id in Result.TCID. Single-TC callers
// scope-validate the spec via Target BEFORE using TCID for flat wiring, so a
// folder-qualified target whose spec lives in a different folder is rejected
// rather than silently operating on the same bare id elsewhere.

// setupScopedProject seeds a project where tc-abc12345 lives ONLY under foo/.
// bar/ exists but is empty. An artefact file exists for the link paths.
func setupScopedProject(t *testing.T) string {
	t.Helper()
	root := setupCompatProject(t) // git repo + gtms/test/cases + gtms/automation

	fooSpec := filepath.Join(root, "gtms/test/cases/foo/tc-abc12345-real.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(fooSpec), 0o755))
	require.NoError(t, os.WriteFile(fooSpec,
		[]byte("---\ngtms_schema: \"test-case/v2\"\ntype: Test Case Spec\ntest_type: Functional\ntest_case_id: tc-abc12345\ntitle: Real spec under foo\n---\n# real\n"), 0o644))

	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms/test/cases/bar"), 0o755))

	art := filepath.Join(root, "test/acceptance/foo/real-test.bats")
	require.NoError(t, os.MkdirAll(filepath.Dir(art), 0o755))
	require.NoError(t, os.WriteFile(art, []byte("@test \"x\" { true; }\n"), 0o644))
	return root
}

// batsLinkConfig gives LinkRecord a bats execute adapter to resolve for wiring.
func batsLinkConfig() *config.Config {
	return &config.Config{
		Adapters: map[string]map[string]*config.AdapterConfig{
			"execute": {
				"bats-runner": {Framework: "bats", Mode: "sync", Command: "echo PASS"},
			},
		},
		Defaults: map[string]string{"execute": "bats-runner"},
	}
}

// --- Unit test of the shared scoped-existence guard ---

func TestRequireScopedSpec_ScopesToFolder(t *testing.T) {
	root := setupScopedProject(t)

	// Wrong folder -> rejected, error names the TC and the cases location.
	err := requireScopedSpec(root, target.Result{Kind: target.KindTCID, Target: "bar/tc-abc12345", TCID: "tc-abc12345"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tc-abc12345")
	assert.Contains(t, err.Error(), "gtms/test/cases")

	// Correct folder -> allowed.
	assert.NoError(t, requireScopedSpec(root, target.Result{Kind: target.KindTCID, Target: "foo/tc-abc12345", TCID: "tc-abc12345"}))

	// Unqualified -> skipped (global/brownfield path unchanged).
	assert.NoError(t, requireScopedSpec(root, target.Result{Kind: target.KindTCID, Target: "tc-abc12345", TCID: "tc-abc12345"}))

	// Folder kind -> not applicable.
	assert.NoError(t, requireScopedSpec(root, target.Result{Kind: target.KindFolder, Target: "bar"}))
}

// --- CLI: execute / automate wrong-folder qualified -> rejected ---

func TestExecute_WrongFolderQualified_Rejected(t *testing.T) {
	skipIfShort(t)
	root := setupScopedProject(t)
	restore := withExecuteGlobals(t, root, batsExecuteConfig())
	defer restore()

	out, err := runCmdCaptureStderr(t, newExecuteCmd(), "bar/tc-abc12345")
	require.Error(t, err)
	assert.Contains(t, out, "not found")
	assert.Contains(t, out, "gtms/test/cases")
}

func TestAutomate_WrongFolderQualified_Rejected(t *testing.T) {
	skipIfShort(t)
	root := setupScopedProject(t)
	restore := withExecuteGlobals(t, root, batsExecuteConfig())
	defer restore()

	out, err := runCmdCaptureStderr(t, newAutomateCmd(), "bar/tc-abc12345")
	require.Error(t, err)
	assert.Contains(t, out, "bar/tc-abc12345")
	assert.Contains(t, out, "not found")
}

// --- CLI: standard link wrong-folder --strict -> rejected, writes nothing ---

func TestLink_StandardWrongFolderQualified_Rejected_WritesNothing(t *testing.T) {
	skipIfShort(t)
	root := setupScopedProject(t)
	restore := withExecuteGlobals(t, root, batsLinkConfig())
	defer restore()

	artefact := filepath.Join(root, "test/acceptance/foo/real-test.bats")
	out, err := runCmdCaptureStderr(t, newLinkCmd(),
		"bar/tc-abc12345", "--artefact", artefact, "--framework", "bats", "--strict")
	require.Error(t, err)
	assert.Contains(t, out, "tc-abc12345")
	assert.Contains(t, out, "gtms/test/cases")

	wiring, _ := filepath.Glob(filepath.Join(root, "gtms/automation/wiring", "tc-abc12345*"))
	assert.Empty(t, wiring, "no wiring record may be written on a scoped rejection")
}

func TestLink_StandardCorrectFolderQualified_Succeeds(t *testing.T) {
	skipIfShort(t)
	root := setupScopedProject(t)
	restore := withExecuteGlobals(t, root, batsLinkConfig())
	defer restore()

	artefact := filepath.Join(root, "test/acceptance/foo/real-test.bats")
	out, err := runCmdCaptureStderr(t, newLinkCmd(),
		"foo/tc-abc12345", "--artefact", artefact, "--framework", "bats", "--strict")
	assert.NotContains(t, out, "not found")
	require.NoError(t, err)

	wiring, _ := filepath.Glob(filepath.Join(root, "gtms/automation/wiring", "tc-abc12345--bats.wiring.yaml"))
	assert.NotEmpty(t, wiring, "correct-folder qualified strict link must write the flat wiring for the bare id")
}

// --- CLI: link --refresh / repoint wrong-folder -> rejected ---

func TestLink_RefreshWrongFolderQualified_Rejected(t *testing.T) {
	skipIfShort(t)
	root := setupScopedProject(t)
	restore := withExecuteGlobals(t, root, batsLinkConfig())
	defer restore()

	out, err := runCmdCaptureStderr(t, newLinkCmd(), "bar/tc-abc12345", "--refresh")
	require.Error(t, err)
	assert.Contains(t, out, "not found")
	assert.Contains(t, out, "gtms/test/cases")
}

func TestLink_RepointWrongFolderQualified_Rejected(t *testing.T) {
	skipIfShort(t)
	root := setupScopedProject(t)
	restore := withExecuteGlobals(t, root, batsLinkConfig())
	defer restore()

	out, err := runCmdCaptureStderr(t, newLinkCmd(),
		"bar/tc-abc12345", "--adapter", "bats-runner", "--from-adapter", "bats-runner")
	require.Error(t, err)
	assert.Contains(t, out, "not found")
	assert.Contains(t, out, "gtms/test/cases")
}

// --- REV-114 CLAUDE-001: NON-strict standard link must also honor the qualifier ---

func TestLink_StandardWrongFolderQualified_NonStrict_Rejected_WritesNothing(t *testing.T) {
	skipIfShort(t)
	root := setupScopedProject(t)
	restore := withExecuteGlobals(t, root, batsLinkConfig())
	defer restore()

	artefact := filepath.Join(root, "test/acceptance/foo/real-test.bats")
	// NO --strict: the scoped guard must still reject the wrong-folder qualifier.
	out, err := runCmdCaptureStderr(t, newLinkCmd(),
		"bar/tc-abc12345", "--artefact", artefact, "--framework", "bats")
	require.Error(t, err)
	assert.Contains(t, out, "not found")
	assert.Contains(t, out, "gtms/test/cases")

	wiring, _ := filepath.Glob(filepath.Join(root, "gtms/automation/wiring", "tc-abc12345*"))
	assert.Empty(t, wiring, "non-strict wrong-folder qualified link must write nothing")
}

func TestLink_StandardCorrectFolderQualified_NonStrict_Succeeds(t *testing.T) {
	skipIfShort(t)
	root := setupScopedProject(t)
	restore := withExecuteGlobals(t, root, batsLinkConfig())
	defer restore()

	artefact := filepath.Join(root, "test/acceptance/foo/real-test.bats")
	out, err := runCmdCaptureStderr(t, newLinkCmd(),
		"foo/tc-abc12345", "--artefact", artefact, "--framework", "bats")
	assert.NotContains(t, out, "not found")
	require.NoError(t, err)

	wiring, _ := filepath.Glob(filepath.Join(root, "gtms/automation/wiring", "tc-abc12345--bats.wiring.yaml"))
	assert.NotEmpty(t, wiring, "correct-folder qualified non-strict link must write the flat wiring")
}

func TestLink_StandardUnqualifiedBareID_NonStrict_Unchanged(t *testing.T) {
	skipIfShort(t)
	root := setupScopedProject(t) // tc-abc12345 lives under foo/
	restore := withExecuteGlobals(t, root, batsLinkConfig())
	defer restore()

	artefact := filepath.Join(root, "test/acceptance/foo/real-test.bats")
	// A BARE id (no qualifier) keeps the ENH-111 global/brownfield behaviour: the
	// scoped guard is skipped, the spec is found globally under foo/, link succeeds.
	out, err := runCmdCaptureStderr(t, newLinkCmd(),
		"tc-abc12345", "--artefact", artefact, "--framework", "bats")
	assert.NotContains(t, out, "not found")
	require.NoError(t, err)

	wiring, _ := filepath.Glob(filepath.Join(root, "gtms/automation/wiring", "tc-abc12345--bats.wiring.yaml"))
	assert.NotEmpty(t, wiring, "unqualified bare-id link must keep global/brownfield behaviour")
}

func TestLink_CheckWrongFolderQualified_Rejected(t *testing.T) {
	skipIfShort(t)
	root := setupScopedProject(t)
	restore := withExecuteGlobals(t, root, batsLinkConfig())
	defer restore()

	artefact := filepath.Join(root, "test/acceptance/foo/real-test.bats")
	// --check must pass through the SAME qualified-target gate as the write path.
	out, err := runCmdCaptureStderr(t, newLinkCmd(),
		"bar/tc-abc12345", "--check", "--artefact", artefact, "--framework", "bats")
	require.Error(t, err)
	assert.Contains(t, out, "not found")
	assert.Contains(t, out, "gtms/test/cases")
}

// --- REV-115/REV-119: out-of-tree symlink qualified target rejected at CLI, no mutation ---
// REV-119: with the shared folder resolver, `escape/tc-abc12345` (escape -> out of
// tree) is rejected at CLASSIFICATION as a containment failure ("resolves outside
// the test cases directory") -- earlier and clearer than the prior spec-lookup
// "not found". The security property (rejected, zero mutation) is unchanged.

func TestLink_OutOfTreeSymlinkQualified_Rejected_WritesNothing(t *testing.T) {
	skipIfShort(t)
	root := setupScopedProject(t)
	addEscapeSymlink(t, root) // gtms/test/cases/escape -> outside-cases (out-of-tree)
	restore := withExecuteGlobals(t, root, batsLinkConfig())
	defer restore()

	artefact := filepath.Join(root, "test/acceptance/foo/real-test.bats")
	out, err := runCmdCaptureStderr(t, newLinkCmd(),
		"escape/tc-abc12345", "--artefact", artefact, "--framework", "bats")
	require.Error(t, err)
	assert.Contains(t, out, "resolves outside")

	wiring, _ := filepath.Glob(filepath.Join(root, "gtms/automation/wiring", "tc-abc12345*"))
	assert.Empty(t, wiring, "an out-of-tree symlink scope must write no wiring")
}

func TestExecute_OutOfTreeSymlinkQualified_Rejected_NoMutation(t *testing.T) {
	skipIfShort(t)
	root := setupScopedProject(t)
	addEscapeSymlink(t, root)
	restore := withExecuteGlobals(t, root, batsExecuteConfig())
	defer restore()

	out, err := runCmdCaptureStderr(t, newExecuteCmd(), "escape/tc-abc12345")
	require.Error(t, err)
	assert.Contains(t, out, "resolves outside")
	assertNoPipelineMutation(t, root)
}

func TestAutomate_OutOfTreeSymlinkQualified_Rejected_NoMutation(t *testing.T) {
	skipIfShort(t)
	root := setupScopedProject(t)
	addEscapeSymlink(t, root)
	restore := withExecuteGlobals(t, root, batsExecuteConfig())
	defer restore()

	out, err := runCmdCaptureStderr(t, newAutomateCmd(), "escape/tc-abc12345")
	require.Error(t, err)
	assert.Contains(t, out, "resolves outside")
	assertNoPipelineMutation(t, root)
}
