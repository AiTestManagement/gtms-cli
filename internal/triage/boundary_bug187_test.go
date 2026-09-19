package triage

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
)

// BUG-187: physical triage-store boundary. These Go unit tests own the coverage
// the canonical BATS specs in gtms/test/cases/triage-store-path-boundary/
// explicitly delegate here: the full entry-type table (including socket/FIFO in
// the unix sibling file), the same-entry read invariant, the store-directory
// escape, the in-project alias, and a project reached through a symlinked
// ancestor. Every symlink-dependent case ANNOUNCES a skip as non-evidence when
// the platform cannot create the link, rather than passing vacuously.

// mkSymlinkOrSkip creates a symlink, skipping the test (announcing no evidence)
// when the platform or filesystem cannot create one.
func mkSymlinkOrSkip(t *testing.T, oldname, newname string) {
	t.Helper()
	if err := os.Symlink(oldname, newname); err != nil {
		t.Skipf("symlinks not creatable here (%v); this case produced NO evidence", err)
	}
}

// realTriageDir creates and returns the real (non-canonical) triage directory
// for root, so tests can plant entries in it before exercising the boundary.
func realTriageDir(t *testing.T, root string) string {
	t.Helper()
	dir := layout.TriageDir(root)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	return dir
}

func assertEntryTypeError(t *testing.T, err error, wantKind string) {
	t.Helper()
	require.Error(t, err)
	var ete *entryTypeError
	require.Truef(t, errors.As(err, &ete), "expected *entryTypeError, got %T: %v", err, err)
	assert.Containsf(t, err.Error(), wantKind, "diagnostic should name the entry kind %q: %s", wantKind, err.Error())
	assert.NotContains(t, strings.ToLower(err.Error()), "no such file",
		"a present-but-non-regular entry must not be reported as absent")
}

func TestRead_RefusesSymlinkToValidRecord(t *testing.T) {
	root := t.TempDir()
	ext := t.TempDir()
	extRec := filepath.Join(ext, "external.triage.yaml")
	require.NoError(t, os.WriteFile(extRec, []byte(validRecordBody("tc-11111111", "bats")), 0o644))

	dir := realTriageDir(t, root)
	link := filepath.Join(dir, "tc-11111111--bats.triage.yaml")
	mkSymlinkOrSkip(t, extRec, link)

	path, err := Path(root, "tc-11111111", "bats")
	require.NoError(t, err)
	_, err = Read(root, path)
	assertEntryTypeError(t, err, "symbolic link")

	// The external target is never rewritten and the link stays a link.
	fi, lerr := os.Lstat(link)
	require.NoError(t, lerr)
	assert.NotZero(t, fi.Mode()&os.ModeSymlink, "the store entry must still be a symlink")
	got, _ := os.ReadFile(extRec)
	assert.Equal(t, validRecordBody("tc-11111111", "bats"), string(got))
}

func TestRead_RefusesDanglingSymlink(t *testing.T) {
	root := t.TempDir()
	dir := realTriageDir(t, root)
	link := filepath.Join(dir, "tc-11111111--bats.triage.yaml")
	mkSymlinkOrSkip(t, filepath.Join(dir, "does-not-exist.yaml"), link)

	path, err := Path(root, "tc-11111111", "bats")
	require.NoError(t, err)
	_, err = Read(root, path)
	// os.Stat would report this as absent; Lstat sees the link, so it is refused
	// as a present non-regular entry, not as a missing record.
	assertEntryTypeError(t, err, "symbolic link")
}

func TestRead_RefusesDirectoryEntry(t *testing.T) {
	root := t.TempDir()
	dir := realTriageDir(t, root)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "tc-11111111--bats.triage.yaml"), 0o755))

	path, err := Path(root, "tc-11111111", "bats")
	require.NoError(t, err)
	_, err = Read(root, path)
	assertEntryTypeError(t, err, "directory")
	assert.NotContains(t, err.Error(), "parsing triage record", "a directory must be refused before any YAML parse")
	assert.NotContains(t, err.Error(), "invalid triage record", "a directory must be refused before strict decode")
}

func TestRead_RegularRecordStillReads(t *testing.T) {
	// The over-reach guard at unit level: a directly written regular record must
	// pass the boundary unchanged.
	root := t.TempDir()
	path, err := Write(root, doubleDashRecord("tc-11111111"))
	require.NoError(t, err)
	rec, err := Read(root, path)
	require.NoError(t, err)
	assert.Equal(t, "tc-11111111", rec.TestCaseID)
	assert.Equal(t, "environment", rec.Value)
}

func TestRead_RefusesEscapedStoreDirectory(t *testing.T) {
	root := t.TempDir()
	ext := t.TempDir()
	// A valid regular record sits in the external directory the store points at.
	require.NoError(t, os.WriteFile(filepath.Join(ext, "tc-11111111--bats.triage.yaml"),
		[]byte(validRecordBody("tc-11111111", "bats")), 0o644))
	// Replace gtms/triage with a symlink to the external directory.
	parent := filepath.Dir(layout.TriageDir(root)) // <root>/gtms
	require.NoError(t, os.MkdirAll(parent, 0o755))
	mkSymlinkOrSkip(t, ext, layout.TriageDir(root))

	// Every surface must refuse with a path-safety error, reading nothing.
	_, err := ListAll(root)
	require.Error(t, err)
	assert.True(t, pathsafe.IsPathSafetyError(err), "ListAll must refuse an escaped store: %v", err)

	_, err = ListForTC(root, "tc-11111111")
	require.Error(t, err)
	assert.True(t, pathsafe.IsPathSafetyError(err), "ListForTC must refuse an escaped store: %v", err)

	_, perr := Path(root, "tc-11111111", "bats")
	require.Error(t, perr)
	assert.True(t, pathsafe.IsPathSafetyError(perr), "Path must refuse an escaped store: %v", perr)

	// Write must refuse and create nothing in the external directory.
	before, _ := os.ReadDir(ext)
	_, werr := Write(root, doubleDashRecord("tc-22222222"))
	require.Error(t, werr)
	assert.True(t, pathsafe.IsPathSafetyError(werr), "Write must refuse an escaped store: %v", werr)
	after, _ := os.ReadDir(ext)
	assert.Len(t, after, len(before), "a refused write must create nothing in the external directory")
	// No temp file left behind either.
	for _, e := range after {
		assert.NotContains(t, e.Name(), "triage-", "no temp file may be left in the external directory")
	}
}

func TestRead_InProjectStoreAliasWorks(t *testing.T) {
	root := t.TempDir()
	// gtms/triage -> gtms/triage-store, both inside the project.
	realStore := filepath.Join(root, "gtms", "triage-store")
	require.NoError(t, os.MkdirAll(realStore, 0o755))
	parent := filepath.Dir(layout.TriageDir(root))
	require.NoError(t, os.MkdirAll(parent, 0o755))
	mkSymlinkOrSkip(t, realStore, layout.TriageDir(root))

	path, err := Write(root, doubleDashRecord("tc-33333333"))
	require.NoError(t, err)

	// The record must land as a regular file under the alias's real target.
	landed := filepath.Join(realStore, "tc-33333333--bats.triage.yaml")
	fi, serr := os.Lstat(landed)
	require.NoError(t, serr, "record must be written through the in-project alias to its real target")
	assert.True(t, fi.Mode().IsRegular(), "the written record must be a regular file, not a link")

	rec, err := Read(root, path)
	require.NoError(t, err)
	assert.Equal(t, "tc-33333333", rec.TestCaseID)
}

func TestRead_SymlinkedAncestorProjectWorks(t *testing.T) {
	realRoot := t.TempDir()
	aliasParent := t.TempDir()
	alias := filepath.Join(aliasParent, "proj")
	mkSymlinkOrSkip(t, realRoot, alias)

	// Write and read through the aliased project path.
	path, err := Write(alias, doubleDashRecord("tc-44444444"))
	require.NoError(t, err)
	rec, err := Read(alias, path)
	require.NoError(t, err)
	assert.Equal(t, "tc-44444444", rec.TestCaseID)

	// The record physically lives under the real root.
	landed := filepath.Join(realRoot, "gtms", "triage", "tc-44444444--bats.triage.yaml")
	fi, serr := os.Lstat(landed)
	require.NoError(t, serr, "the record must live under the canonical real root")
	assert.True(t, fi.Mode().IsRegular())
}

func TestWrite_RefusesReplacingSymlinkedLeaf(t *testing.T) {
	root := t.TempDir()
	ext := t.TempDir()
	extRec := filepath.Join(ext, "external.triage.yaml")
	require.NoError(t, os.WriteFile(extRec, []byte(validRecordBody("tc-11111111", "bats")), 0o644))
	before, _ := os.ReadFile(extRec)

	dir := realTriageDir(t, root)
	link := filepath.Join(dir, "tc-11111111--bats.triage.yaml")
	mkSymlinkOrSkip(t, extRec, link)

	// A fresh classify would build a new record; Write must refuse to replace the
	// link and must not write through it.
	_, err := Write(root, doubleDashRecord("tc-11111111"))
	assertEntryTypeError(t, err, "symbolic link")

	fi, _ := os.Lstat(link)
	assert.NotZero(t, fi.Mode()&os.ModeSymlink, "the link must survive a refused write")
	after, _ := os.ReadFile(extRec)
	assert.Equal(t, string(before), string(after), "the external target must be byte-unchanged")
}

func TestWrite_RefusesReplacingDanglingLeaf(t *testing.T) {
	root := t.TempDir()
	dir := realTriageDir(t, root)
	link := filepath.Join(dir, "tc-11111111--bats.triage.yaml")
	mkSymlinkOrSkip(t, filepath.Join(dir, "gone.yaml"), link)

	// os.Stat reports the dangling link as absent, so a Stat-guarded classify
	// path would skip its read and call the writer; the Write-side Lstat gate is
	// what refuses it here.
	_, err := Write(root, doubleDashRecord("tc-11111111"))
	assertEntryTypeError(t, err, "symbolic link")

	fi, lerr := os.Lstat(link)
	require.NoError(t, lerr, "the dangling link must still exist")
	assert.NotZero(t, fi.Mode()&os.ModeSymlink, "it must still be a symlink, not a replaced regular file")
}

// TestReadRegularEntry_ReadsFromCheckedEntry pins BUG-187 Fix Direction item 3
// at the primitive: readRegularEntry returns the bytes of the exact regular file
// it validated. The complementary source-shape guard (record.go uses
// readRegularEntry and not a second os.ReadFile) lives in
// boundary_source_shape_test.go.
func TestReadRegularEntry_ReadsFromCheckedEntry(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "rec.yaml")
	require.NoError(t, os.WriteFile(p, []byte("hello-body"), 0o644))
	data, err := readRegularEntry(p)
	require.NoError(t, err)
	assert.Equal(t, "hello-body", string(data))

	// A genuinely absent path preserves NotExist so callers keep normal handling.
	_, err = readRegularEntry(filepath.Join(dir, "missing.yaml"))
	require.Error(t, err)
	assert.True(t, os.IsNotExist(errors.Unwrap(err)) || strings.Contains(err.Error(), "no such file"),
		"an absent entry must surface as NotExist, got %v", err)
}

// --- REV-162 blockers: regressions the first implementation could not see ---

// TestRead_RefusesControlCharFilenameIdentity is the regression for the
// identity-validation defect both reviewers found independently (CODEX-001,
// CLAUDE-001).
//
// The entry is a REGULAR file, so it passes the entry-type gate and reaches
// validate -- which is why no existing test caught this: tc-0c245100 reaches a
// hostile name only through a symlink, refused earlier. Its real basename
// carries an ESC byte exactly where the body carries '?'. Sanitizing before the
// comparison renders the two equal, so a lossy validate ACCEPTS a record whose
// filename does not match its body identity.
func TestRead_RefusesControlCharFilenameIdentity(t *testing.T) {
	root := t.TempDir()
	dir := realTriageDir(t, root)

	// Body claims framework "bats?FORGED"; the file on disk uses ESC, not '?'.
	const bodyFramework = "bats?FORGED"
	realName := "tc-88888888--bats\x1bFORGED.triage.yaml"
	realPath := filepath.Join(dir, realName)
	if err := os.WriteFile(realPath, []byte(validRecordBody("tc-88888888", bodyFramework)), 0o644); err != nil {
		t.Skipf("cannot create a filename containing a control character here (%v); this case produced NO evidence", err)
	}

	// Prove the fixture is the shape the test claims: the real name must differ
	// from the body-implied name ONLY in control-character positions. Without
	// this, a filesystem that silently rewrote the byte would make the assertion
	// below pass for the wrong reason.
	require.NotEqual(t, "tc-88888888--"+bodyFramework+".triage.yaml", realName,
		"fixture invalid: the on-disk name must differ from the body-implied name")
	require.Equal(t, "tc-88888888--"+bodyFramework+".triage.yaml",
		pathsafe.SanitizeControlChars(realName),
		"fixture invalid: the names must collapse to equal only AFTER sanitisation -- "+
			"that collapse is the defect under test")

	_, err := Read(root, realPath)
	require.Error(t, err, "a regular file whose real name disagrees with its body identity must be refused")
	assert.Contains(t, err.Error(), "does not match its body identity",
		"the refusal must be the identity error, not an unrelated failure")
	// The diagnostic still may not carry a raw control character.
	assert.NotContains(t, err.Error(), "\x1b",
		"the diagnostic must render the sanitised path even though the decision used the raw one")
}

// TestListAll_RefusesRecordShapedDirectory is the regression for the silent
// omission. A directory named like a record used to be dropped by an IsDir()
// skip, so `gtms triage --list --json` at root returned [] and exit 0 with a
// forged entry sitting in the store. Enumeration selects by name; Read refuses
// by type.
func TestListAll_RefusesRecordShapedDirectory(t *testing.T) {
	root := t.TempDir()
	dir := realTriageDir(t, root)
	forged := filepath.Join(dir, "tc-55555555--bats.triage.yaml")
	require.NoError(t, os.MkdirAll(forged, 0o755))

	refs, err := ListAll(root)
	require.NoError(t, err, "enumeration itself does not read, so it does not fail here")
	require.Len(t, refs, 1, "a record-shaped directory must be ENUMERATED as a candidate, not skipped into silence")

	_, rerr := Read(root, refs[0].Path)
	assertEntryTypeError(t, rerr, "directory")
}

// TestListForTC_RefusesRecordShapedDirectory is the same guard on the scoped
// enumerator.
func TestListForTC_RefusesRecordShapedDirectory(t *testing.T) {
	root := t.TempDir()
	dir := realTriageDir(t, root)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "tc-55555555--bats.triage.yaml"), 0o755))

	refs, err := ListForTC(root, "tc-55555555")
	require.NoError(t, err)
	require.Len(t, refs, 1, "a record-shaped directory must be a candidate for the scoped enumerator too")

	_, rerr := Read(root, refs[0].Path)
	assertEntryTypeError(t, rerr, "directory")
}

// TestWrite_FailsClosedWhenEntryCannotBeInspected is the regression for the
// collapsed three-way state. A path whose PARENT is a regular file makes Lstat
// return ENOTDIR -- an error that is emphatically not fs.ErrNotExist. Treating
// every Lstat error as absence let the writer proceed against an entry it had
// never managed to inspect. ENOTDIR is used rather than a permission denial
// because it is deterministic and does not depend on the test running unprivileged.
func TestWrite_FailsClosedWhenEntryCannotBeInspected(t *testing.T) {
	root := t.TempDir()
	// gtms/triage is a regular FILE, so any record path "under" it cannot be
	// stat-ed: Lstat returns ENOTDIR, which is emphatically not fs.ErrNotExist.
	// Treating every Lstat error as absence let the writer proceed against an
	// entry it had never managed to inspect. ENOTDIR is used rather than a
	// permission denial because it is deterministic and does not depend on the
	// test running unprivileged (root ignores mode bits).
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gtms"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "gtms", "triage"), []byte("not a directory\n"), 0o644))

	// Prove the premise before relying on it. If Lstat returns NotExist here the
	// fixture cannot construct the state under test, and a pass would be
	// meaningless -- so announce that and stop rather than assert into fog.
	//
	// Windows takes this skip: it maps a path under a regular file to
	// ERROR_PATH_NOT_FOUND, which Go surfaces as fs.ErrNotExist, so an
	// uninspectable-but-present entry cannot be built this way there. The
	// invariant is still guarded on Windows structurally, by
	// TestSourceShape_WriteDistinguishesAbsentFromUninspectable, which fails if
	// the fs.ErrNotExist discrimination is removed from Write on any platform.
	probe := filepath.Join(root, "gtms", "triage", "tc-11111111--bats.triage.yaml")
	_, lerr := os.Lstat(probe)
	require.Error(t, lerr, "premise: the probe path must not be stat-able")
	if errors.Is(lerr, fs.ErrNotExist) {
		t.Skipf("this platform reports an unreachable path as NotExist (%v), so the "+
			"absent-vs-uninspectable distinction cannot be constructed here; "+
			"this case produced NO evidence for BUG-187", lerr)
	}

	_, werr := Write(root, doubleDashRecord("tc-11111111"))
	require.Error(t, werr, "a write whose target cannot be inspected must fail closed")
	assert.Contains(t, werr.Error(), "cannot determine what is already there",
		"the refusal must say the entry could not be inspected, not that it was absent")
}

// TestEntryTypeError_NamesTheRefusedOperation covers CLAUDE-002: a refused
// REPLACE must not report itself as a refused read.
func TestEntryTypeError_NamesTheRefusedOperation(t *testing.T) {
	read := (&entryTypeError{path: "p", kind: "symbolic link", op: opRead}).Error()
	replace := (&entryTypeError{path: "p", kind: "symbolic link", op: opReplace}).Error()

	const shared = "refusing triage record p: it is a symbolic link, not a regular file;"
	assert.Contains(t, read, shared, "the shared prefix must be stable: BATS assertions match on it")
	assert.Contains(t, replace, shared, "the shared prefix must be stable for both operations")

	assert.Contains(t, read, "will not read it")
	assert.Contains(t, replace, "will not replace it")
	assert.NotContains(t, replace, "will not read it",
		"a refused write must not tell the user GTMS declined to READ the entry")
}

// TestRead_SanitisesBodyDerivedValuesInDiagnostics covers the other half of
// clause 4. The record BODY is repository-carried content too, so a value
// echoed from it is exactly as capable of forging a terminal line as a
// filename. Sanitizing only the path left status, snapshot.result, test_case_id
// and framework rendering raw.
func TestRead_SanitisesBodyDerivedValuesInDiagnostics(t *testing.T) {
	root := t.TempDir()
	dir := realTriageDir(t, root)
	path := filepath.Join(dir, "tc-11111111--bats.triage.yaml")

	// A well-formed record whose STATUS decodes to an ANSI erase-line sequence.
	// The ESC is written as a YAML \x escape, not a raw byte: YAML rejects raw
	// control characters in a scalar, so a raw byte would fail at parse and never
	// reach the status check this test is about. The DECODED value carries the
	// real ESC, which is what a hostile record would do.
	body := "version: 1\n" +
		"test_case_id: tc-11111111\n" +
		"framework: bats\n" +
		"status: \"act\\x1b[2Kive\"\n" +
		"value: flaky\n" +
		"classified_at: \"2026-08-28T12:00:00Z\"\n" +
		"snapshot:\n  task: task-11111111\n  result: fail\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))

	_, err := Read(root, path)
	require.Error(t, err, "an out-of-vocabulary status must be refused")
	require.Contains(t, err.Error(), "status", "premise: the refusal must be the status-vocabulary error")

	assert.NotContains(t, err.Error(), "\x1b",
		"a status echoed from the record body must not carry a raw ESC into the terminal")
	assert.Contains(t, err.Error(), "act?[2Kive",
		"the offending value must still be shown, escaped, so it can be corrected")
}
