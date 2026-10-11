package reader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
	"github.com/aitestmanagement/gtms-cli/internal/triage"
)

// ENH-200 / CON-040 reader orchestration tests. These replace the retired
// category-model tests (task creation, gtms/triage-history appends, BUG-180
// spec-safety) whose behaviour no longer exists. The durable regression
// intents they carried -- framework resolution/ambiguity, no wiring -> error,
// and byte-safety of surrounding records -- are preserved here against the new
// classify/resolve contract.

// defaultTriageValues mirrors config.DefaultTriageValues for the reader tests
// so they exercise the same permitted set the CLI passes in.
func triageTestValues() map[string]string {
	return map[string]string{
		"app-wrong":        "",
		"test-wrong":       "Test definition problem",
		"automation-wrong": "Automation implementation problem",
		"environment":      "Environment failure",
		"intermittent":     "Intermittent or order-dependent failure",
		"blocked":          "",
	}
}

// writeTriageSpec seeds a TC spec that is deliberately hostile to any
// parse-and-rewrite path. BUG-180 was caused by triage marshalling the spec
// through a closed frontmatter struct, which silently dropped unmodelled keys
// and reset formatting. A minimal spec cannot detect that regression: every
// key in it is modelled, so a closed-struct round trip reproduces it
// byte-for-byte and the guard passes while unknown data is being destroyed.
//
// The fixture therefore carries the four things such a rewrite loses:
// a draft lifecycle state, an unmodelled top-level key, the reserved meta
// mapping (ENH-159), and a non-ASCII body byte. Callers assert whole-file
// byte equality across the triage call, which subsumes assertions about any
// individual one of them.
func writeTriageSpec(t *testing.T, root, tc string) {
	t.Helper()
	// The e-acute is built from a rune so this source file stays ASCII.
	body := "---\n" +
		"gtms_schema: \"test-case/v2\"\n" +
		"type: Test Case Spec\n" +
		"test_type: Functional\n" +
		"test_case_id: " + tc + "\n" +
		"title: \"Triage subject\"\n" +
		"requirement: ENH-200\n" +
		"status: draft\n" +
		"business_owner: payments\n" +
		"meta:\n" +
		"  source: field\n" +
		"created: 2026-08-26\n" +
		"---\n\n" +
		"## Test Objective\nClassify me.\n\n" +
		"## Steps\n1. Order a caf" + string(rune(0x00E9)) + " latte\n"
	writeFile(t, root, filepath.Join("gtms", "test", "cases", tc+".md"), body)
}

func triageRecordPath(root, tc, fw string) string {
	return filepath.Join(root, "gtms", "triage", tc+"--"+fw+".triage.yaml")
}

func TestClassifyTriage_WritesCompleteRecord(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeTriageSpec(t, root, "tc-11111111")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-11111111", Framework: "bats", Result: "fail"})

	rec, err := ClassifyTriage(root, "tc-11111111", "environment", "creds expired", "JIRA-789", "bats", triageTestValues())
	require.NoError(t, err)
	assert.Equal(t, triage.StatusActive, rec.Status)
	assert.Equal(t, "environment", rec.Value)
	assert.Equal(t, "fail", rec.Snapshot.Result)
	assert.Empty(t, rec.ResolvedAt)

	// The stored file exists and reads back.
	path := triageRecordPath(root, "tc-11111111", "bats")
	on, err := triage.Read(root, path)
	require.NoError(t, err)
	assert.Equal(t, "creds expired", on.Summary)
	assert.Equal(t, "JIRA-789", on.Defect)

	// testcase_hash_at_triage must equal pipeline.HashFile of the spec.
	owner, _ := testcase.ResolveOwner(root, "tc-11111111")
	wantHash, _ := pipeline.HashFile(owner.SpecAbs())
	assert.Equal(t, wantHash, on.Snapshot.TestcaseHashAtTriage)
	assert.NotEmpty(t, on.Snapshot.Task)
}

func TestClassifyTriage_RawSkipVocabulary(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeTriageSpec(t, root, "tc-33333333")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-33333333", Framework: "bats", Result: "skip"})

	rec, err := ClassifyTriage(root, "tc-33333333", "intermittent", "", "", "bats", triageTestValues())
	require.NoError(t, err)
	assert.Equal(t, "skip", rec.Snapshot.Result, "snapshot must store the raw contract vocabulary")

	data, _ := os.ReadFile(triageRecordPath(root, "tc-33333333", "bats"))
	assert.NotContains(t, string(data), "skipped", "the reader-normalised value must not leak into the record")
}

func TestClassifyTriage_UnconfiguredValueRefused(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeTriageSpec(t, root, "tc-11111111")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-11111111", Framework: "bats", Result: "fail"})

	_, err := ClassifyTriage(root, "tc-11111111", "not-a-value", "", "", "bats", triageTestValues())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not-a-value")
	assert.NoFileExists(t, triageRecordPath(root, "tc-11111111", "bats"))
}

func TestClassifyTriage_NoWiringRefused(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeTriageSpec(t, root, "tc-11111111")

	_, err := ClassifyTriage(root, "tc-11111111", "environment", "", "", "", triageTestValues())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no wiring record found")
}

func TestClassifyTriage_AmbiguousFrameworkRefused(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeTriageSpec(t, root, "tc-22222222")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-22222222", Framework: "bats", Result: "fail"})
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-22222222", Framework: "playwright", Result: "fail"})

	_, err := ClassifyTriage(root, "tc-22222222", "environment", "", "", "", triageTestValues())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bats")
	assert.Contains(t, err.Error(), "playwright")
	assert.Contains(t, err.Error(), "--framework")
}

func TestClassifyTriage_MissingSpecRefused(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	// wiring + handoff but NO spec.
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-88888888", Framework: "bats", Result: "fail"})

	_, err := ClassifyTriage(root, "tc-88888888", "environment", "", "", "bats", triageTestValues())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "specification")
	assert.NoFileExists(t, triageRecordPath(root, "tc-88888888", "bats"))
}

func TestClassifyTriage_NoResultRefused(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeTriageSpec(t, root, "tc-99999999")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-99999999", Framework: "bats"}) // wiring only, no result

	_, err := ClassifyTriage(root, "tc-99999999", "environment", "", "", "bats", triageTestValues())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no execution result")
	assert.NoFileExists(t, triageRecordPath(root, "tc-99999999", "bats"))
}

func TestClassifyTriage_NoSideEffects(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeTriageSpec(t, root, "tc-11111111")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-11111111", Framework: "bats", Result: "fail"})

	specPath := filepath.Join(root, "gtms", "test", "cases", "tc-11111111.md")
	wiringPath := filepath.Join(root, "gtms", "automation", "wiring", "tc-11111111--bats.wiring.yaml")
	specBefore, _ := os.ReadFile(specPath)
	wiringBefore, _ := os.ReadFile(wiringPath)

	_, err := ClassifyTriage(root, "tc-11111111", "automation-wrong", "sel changed", "", "bats", triageTestValues())
	require.NoError(t, err)

	specAfter, _ := os.ReadFile(specPath)
	wiringAfter, _ := os.ReadFile(wiringPath)
	assert.Equal(t, specBefore, specAfter, "spec must be byte-identical")
	assert.Equal(t, wiringBefore, wiringAfter, "wiring must be byte-identical")

	// No task queued, no legacy history written.
	assert.NoDirExists(t, filepath.Join(root, "gtms", "triage-history"))
	pending := filepath.Join(root, "gtms", "tasks", "pending")
	if entries, err := os.ReadDir(pending); err == nil {
		assert.Empty(t, entries, "no automate task should be queued")
	}
}

func TestClassifyTriage_ReclassifyReactivates(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeTriageSpec(t, root, "tc-11111111")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-11111111", Framework: "bats", Result: "fail"})

	_, err := ClassifyTriage(root, "tc-11111111", "environment", "creds", "JIRA-1", "bats", triageTestValues())
	require.NoError(t, err)
	_, _, err = ResolveTriage(root, "tc-11111111", "bats")
	require.NoError(t, err)

	rec, err := ClassifyTriage(root, "tc-11111111", "intermittent", "", "", "bats", triageTestValues())
	require.NoError(t, err)
	assert.Equal(t, triage.StatusActive, rec.Status)
	assert.Empty(t, rec.ResolvedAt)
	assert.Equal(t, "intermittent", rec.Value)
	assert.Empty(t, rec.Summary, "reclassification replaces context, not inherits it")
	assert.Empty(t, rec.Defect)
}

func TestClassifyTriage_BlocksOverwriteOnBadVersion(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeTriageSpec(t, root, "tc-66666666")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-66666666", Framework: "bats", Result: "fail"})

	path := triageRecordPath(root, "tc-66666666", "bats")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	badContent := "version: 2\ntest_case_id: tc-66666666\nframework: bats\nstatus: active\nvalue: environment\nsnapshot:\n  task: t\n  result: fail\n"
	require.NoError(t, os.WriteFile(path, []byte(badContent), 0644))

	_, err := ClassifyTriage(root, "tc-66666666", "intermittent", "", "", "bats", triageTestValues())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "version")
	after, _ := os.ReadFile(path)
	assert.Equal(t, badContent, string(after), "a record it cannot read must not be overwritten")
}

func TestResolveTriage_Idempotent(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeTriageSpec(t, root, "tc-55555555")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-55555555", Framework: "bats", Result: "fail"})

	_, err := ClassifyTriage(root, "tc-55555555", "environment", "creds", "", "bats", triageTestValues())
	require.NoError(t, err)

	rec, already, err := ResolveTriage(root, "tc-55555555", "bats")
	require.NoError(t, err)
	assert.False(t, already)
	assert.Equal(t, triage.StatusResolved, rec.Status)
	assert.NotEmpty(t, rec.ResolvedAt)

	path := triageRecordPath(root, "tc-55555555", "bats")
	digest1, _ := os.ReadFile(path)

	rec2, already2, err := ResolveTriage(root, "tc-55555555", "bats")
	require.NoError(t, err)
	assert.True(t, already2)
	assert.Equal(t, rec.ResolvedAt, rec2.ResolvedAt)
	digest2, _ := os.ReadFile(path)
	assert.Equal(t, digest1, digest2, "repeated resolve is byte-identical")
}

func TestResolveTriage_FrameworkSelection(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeTriageSpec(t, root, "tc-11111111")
	writeTriageSpec(t, root, "tc-22222222")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-11111111", Framework: "bats", Result: "fail"})
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-22222222", Framework: "bats", Result: "fail"})
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-22222222", Framework: "playwright", Result: "fail"})

	// Sole record -> resolves without --framework.
	_, err := ClassifyTriage(root, "tc-11111111", "environment", "", "", "bats", triageTestValues())
	require.NoError(t, err)
	_, _, err = ResolveTriage(root, "tc-11111111", "")
	require.NoError(t, err)

	// Two records -> ambiguity without --framework.
	_, err = ClassifyTriage(root, "tc-22222222", "environment", "", "", "bats", triageTestValues())
	require.NoError(t, err)
	_, err = ClassifyTriage(root, "tc-22222222", "environment", "", "", "playwright", triageTestValues())
	require.NoError(t, err)
	_, _, err = ResolveTriage(root, "tc-22222222", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bats")
	assert.Contains(t, err.Error(), "playwright")

	// No record -> clear error about the missing record, not upstream evidence.
	_, _, err = ResolveTriage(root, "tc-99999999", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no triage record")
}

func TestResolveTriage_IndependentOfEvidence(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeTriageSpec(t, root, "tc-55555555")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-55555555", Framework: "bats", Result: "fail"})

	_, err := ClassifyTriage(root, "tc-55555555", "environment", "creds", "", "bats", triageTestValues())
	require.NoError(t, err)

	// Remove all upstream evidence.
	require.NoError(t, os.RemoveAll(filepath.Join(root, ".gtms", "results")))
	require.NoError(t, os.RemoveAll(filepath.Join(root, "gtms", "automation", "wiring")))
	require.NoError(t, os.RemoveAll(filepath.Join(root, "gtms", "test", "cases")))

	rec, already, err := ResolveTriage(root, "tc-55555555", "bats")
	require.NoError(t, err, "resolve must succeed from the stored record alone")
	assert.False(t, already)
	assert.Equal(t, triage.StatusResolved, rec.Status)
	assert.Equal(t, "environment", rec.Value)
	assert.Equal(t, "creds", rec.Summary, "classification survives resolution")
}

// sanity: the record filename shape matches the wiring/automation identity.
func TestTriageRecordFilenameShape(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "gtms.config", "project:\n  name: x\n  repo: x\n")
	writeTriageSpec(t, root, "tc-11111111")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-11111111", Framework: "bats", Result: "fail"})
	_, err := ClassifyTriage(root, "tc-11111111", "environment", "", "", "bats", triageTestValues())
	require.NoError(t, err)
	entries, _ := os.ReadDir(filepath.Join(root, "gtms", "triage"))
	require.Len(t, entries, 1)
	assert.True(t, strings.HasSuffix(entries[0].Name(), "--bats.triage.yaml"))
}
