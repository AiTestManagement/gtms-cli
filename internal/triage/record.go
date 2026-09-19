// Package triage owns the structured triage-classification record introduced
// by ENH-200 / CON-040.
//
// A triage record is a pure-YAML file at
// gtms/triage/{tc-id}--{framework}.triage.yaml holding the current human
// classification of the latest execution result for one (test case, framework)
// pair. The record is independent state: writing one never mutates a test-case
// spec, a wiring record, an automation record, or an execution handoff.
//
// gtms triage --value is the classify/reclassify writer; gtms triage --resolve
// closes a record without deleting it. Both go through this package's atomic
// Write. Read enforces a strict schema with a version guard so a record written
// by a future GTMS is never silently downgraded by an older binary.
package triage

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
)

// --- BUG-187: physical triage-store boundary -------------------------------
//
// The structured triage store is machine-owned project state. These helpers add
// the physical ownership boundary the schema/identity validation never had:
// they prove the store directory is inside the canonical project root, and they
// refuse a final record entry that is not a direct regular file. They follow the
// live internal/pathsafe containment model (CanonicalRoot / ResolveUnderRoot /
// IsWithinRoot) already used by delete and execute. The atomic temp-file-plus-
// rename writer is deliberately untouched; the missing work was authority and
// entry-type validation BEFORE that pattern runs.

// storeDir returns the canonical absolute triage-store directory for
// projectRoot, proving it lies within the canonical project root.
//
// The store directory is derived from the discovered layout parent
// (layout.TriageDir); it is not configurable, so the only way it can leave the
// project is an on-disk symlink at <parent>/triage whose target escapes the
// root. Containment is judged on CANONICAL targets (BUG-187 clause 1): a project
// reached through a symlinked ancestor above its root stays valid, and an
// in-project store alias (a symlink whose target is still inside the root) is
// accepted and resolved to its real directory so reads and writes land there. An
// escaped store returns the *pathsafe.PathSafetyError from ResolveUnderRoot
// unchanged, so every caller fails closed with the offending path named.
//
// The store directory need not exist yet: ResolveUnderRoot resolves the nearest
// existing ancestor and re-attaches the missing leaf, so a fresh project whose
// gtms/triage/ has not been created canonicalises cleanly.
func storeDir(projectRoot string) (string, error) {
	root := pathsafe.CanonicalRoot(projectRoot)
	absDir, _, err := pathsafe.ResolveUnderRoot(root, layout.TriageDir(projectRoot))
	if err != nil {
		return "", err
	}
	return absDir, nil
}

// withinStore reports whether path lies inside the canonical store dir. Every
// internal producer (Path, ListAll, ListForTC) already builds paths under the
// canonical dir; this is defence in depth so a future direct caller cannot
// smuggle an out-of-store path past the entry-type gate.
//
// The record leaf may not exist yet, so its own symlinks are not resolved, but
// the PARENT directory's symlinks are: a store reached through a symlinked
// ancestor (a common shape for temp dirs on macOS and some CI) then still
// compares equal to the EvalSymlinks-resolved canonical dir, rather than
// spuriously reading as an escape.
func withinStore(path, dir string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	if resolvedParent, perr := filepath.EvalSymlinks(filepath.Dir(abs)); perr == nil {
		abs = filepath.Join(resolvedParent, filepath.Base(abs))
	}
	return pathsafe.IsWithinRoot(filepath.Clean(abs), filepath.Clean(dir))
}

// entryTypeError refuses a triage-store entry that is not a direct regular file.
// The offending path is repository-derived (an entry name GTMS did not choose),
// so Error() renders it through pathsafe.SanitizeControlChars: a crafted name
// carrying a newline or an ANSI escape must not forge or clear a terminal line
// (BUG-187 clause 4, tc-0c245100). The message identifies the entry as one GTMS
// refuses to touch -- never as an absent record -- so a reader is sent to the
// real file rather than told the record is missing.
//
// op names the operation that was refused. Both the read gate and the write gate
// return this type, and a reader told "GTMS will not read it" after running
// --value or --resolve is being pointed at the wrong operation: what actually
// happened is that GTMS declined to REPLACE the entry and left it, and its
// target, untouched. The shared prefix is identical for both so assertions that
// match on it are unaffected.
type entryTypeError struct {
	path string
	kind string // human label: "symbolic link", "named pipe (FIFO)", ...
	op   string // "read" or "replace"; defaults to "read" when unset
}

func (e *entryTypeError) Error() string {
	tail := "GTMS will not read it"
	if e.op == opReplace {
		tail = "GTMS will not replace it, and its target is unchanged"
	}
	return fmt.Sprintf("refusing triage record %s: it is a %s, not a regular file; %s",
		pathsafe.SanitizeControlChars(e.path), e.kind, tail)
}

// Operations an entryTypeError can report.
const (
	opRead    = "read"
	opReplace = "replace"
)

// describeMode names a non-regular file mode for a refusal diagnostic.
func describeMode(m os.FileMode) string {
	switch {
	case m&os.ModeSymlink != 0:
		return "symbolic link"
	case m.IsDir():
		return "directory"
	case m&os.ModeNamedPipe != 0:
		return "named pipe (FIFO)"
	case m&os.ModeSocket != 0:
		return "socket"
	case m&os.ModeCharDevice != 0:
		return "character device"
	case m&os.ModeDevice != 0:
		return "block device"
	case m&os.ModeIrregular != 0:
		return "irregular file"
	default:
		return "non-regular file"
	}
}

// readRegularEntry reads path only when it is a direct regular file, returning
// its bytes. It is the physical entry-type gate in front of every triage read.
//
// Order matters. os.Lstat runs FIRST and never follows a link, so a symbolic
// link (including a DANGLING one, which os.Stat reports as absent), a FIFO, a
// socket, a directory, or a device is refused BEFORE any open. Opening a FIFO
// blocks until a writer appears, so an "open then check the error" gate would
// HANG the command rather than refuse it (tc-41c817b8). Only after Lstat proves
// a regular, non-symlink entry is the file opened; the bytes are read from that
// same descriptor and fstat re-confirms it is regular, so the decoded bytes come
// from the entry whose type was checked (BUG-187 Fix Direction item 3), not from
// a second path-based read a swap could redirect.
//
// Residual race (stated honestly): between the Lstat and the Open an active
// attacker with write access to the store's parent could swap the leaf for a
// symlink to a regular file; Open would follow it and fstat would still see a
// regular file. Defeating that needs a race-proof per-component openat with
// O_NOFOLLOW, which is not portable across the platforms GTMS targets and is
// beyond this bug's minimum threat model of static, repository-carried entries.
// The fstat re-check narrows the window; it does not close it.
func readRegularEntry(path string) ([]byte, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		// NotExist stays NotExist (via %w) so a genuinely absent record keeps the
		// normal "no record" handling in the caller.
		return nil, fmt.Errorf("opening triage record: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return nil, &entryTypeError{path: path, kind: describeMode(fi.Mode()), op: opRead}
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening triage record: %w", err)
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat triage record: %w", err)
	}
	if !st.Mode().IsRegular() {
		return nil, &entryTypeError{path: path, kind: describeMode(st.Mode()), op: opRead}
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("reading triage record: %w", err)
	}
	return data, nil
}

// --- end BUG-187 boundary --------------------------------------------------

// SchemaVersion is the only triage-record version this binary supports.
const SchemaVersion = 1

// FileSuffix is the on-disk suffix for triage records.
const FileSuffix = ".triage.yaml"

// Status values.
const (
	StatusActive   = "active"
	StatusResolved = "resolved"
)

// Record is the on-disk triage schema. Field order here is the order
// yaml.Marshal emits, which keeps the written file deterministic.
type Record struct {
	Version      int      `yaml:"version"`
	TestCaseID   string   `yaml:"test_case_id"`
	Framework    string   `yaml:"framework"`
	Status       string   `yaml:"status"`
	Value        string   `yaml:"value"`
	Summary      string   `yaml:"summary,omitempty"`
	Defect       string   `yaml:"defect,omitempty"`
	ClassifiedAt string   `yaml:"classified_at"`
	ResolvedAt   string   `yaml:"resolved_at,omitempty"`
	Snapshot     Snapshot `yaml:"snapshot"`
}

// Snapshot copies the essential execution provenance from the selected
// handoff at classification time. Optional fields are omitted when the
// handoff did not supply them; artefact provenance is copied ONLY from the
// handoff and is never backfilled from wiring (CON-040).
type Snapshot struct {
	Task                 string `yaml:"task"`
	Result               string `yaml:"result"` // RAW contract vocabulary: pass|fail|skip|error
	CompletedAt          string `yaml:"completed_at,omitempty"`
	Artefact             string `yaml:"artefact,omitempty"`
	ArtefactHash         string `yaml:"artefact_hash,omitempty"`
	TestcaseHashAtTriage string `yaml:"testcase_hash_at_triage,omitempty"`
}

// Path returns the absolute on-disk path for a triage record identified by
// (testCaseID, framework). Both inputs are validated as filename components so
// a crafted TC ID or framework cannot escape the triage directory.
func Path(projectRoot, testCaseID, framework string) (string, error) {
	if err := pathsafe.ValidateFilenameComponent(testCaseID, "test case ID"); err != nil {
		return "", err
	}
	if err := pathsafe.ValidateFilenameComponent(framework, "framework"); err != nil {
		return "", err
	}
	// BUG-187: build under the CANONICAL store directory, proving containment.
	// An escaped store returns the PathSafetyError here so Path's callers (direct
	// reads via selectTriageRecordPath, the classify overwrite guard, and Write)
	// all fail closed before any file operation. An in-project alias resolves to
	// its real directory, so the record lands there rather than through the link.
	dir, err := storeDir(projectRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir,
		fmt.Sprintf("%s--%s%s", testCaseID, framework, FileSuffix)), nil
}

// Write serialises rec to gtms/triage/{tc}--{framework}.triage.yaml atomically.
//
// The atomic pattern is copied deliberately from wiring.Write (ENH-200 item 3
// says copy the proven pattern into this package rather than refactoring wiring
// through a new shared helper): marshal deterministically into a sibling temp
// file, fsync, close, then rename over the target, cleaning the temp file up on
// any pre-rename failure. Rename replaces an existing file atomically on POSIX
// and Windows, so an interrupted or disk-full write leaves the prior record
// untouched rather than truncated.
func Write(projectRoot string, rec *Record) (string, error) {
	if rec == nil {
		return "", errors.New("triage record is required")
	}
	path, err := Path(projectRoot, rec.TestCaseID, rec.Framework)
	if err != nil {
		// Includes an escaped store directory (PathSafetyError): refuse BEFORE
		// MkdirAll/CreateTemp/Rename, so nothing is created outside the project.
		return "", err
	}

	// BUG-187: never replace a non-regular existing entry. os.Lstat does not
	// follow the link, so a symlink (including a DANGLING one, which os.Stat
	// reports as absent) or any other non-regular entry at the target is refused
	// rather than overwritten. A refused write leaves the link and its external
	// target untouched, and -- because this precedes MkdirAll and CreateTemp --
	// creates no temp file. This is the write-side twin of readRegularEntry: it
	// closes the dangling-link bypass where the classify path skipped its read
	// guard (tc-41c817b8 step 7, tc-a655aaf8).
	// Lstat has THREE outcomes and they must stay distinct. Collapsing them into
	// "err == nil means check it, anything else means absent" is how a guard
	// becomes a permission: a directory the process cannot traverse, or a path
	// whose parent is not a directory, returns an error that is NOT
	// fs.ErrNotExist, and treating that as absence would let the writer proceed
	// against an entry it never managed to inspect. Only a genuine
	// does-not-exist may mean "nothing here to protect".
	switch fi, lerr := os.Lstat(path); {
	case lerr == nil && !fi.Mode().IsRegular():
		return "", &entryTypeError{path: path, kind: describeMode(fi.Mode()), op: opReplace}
	case lerr != nil && !errors.Is(lerr, fs.ErrNotExist):
		return "", fmt.Errorf("refusing to write triage record %s: cannot determine what is already there: %w",
			pathsafe.SanitizeControlChars(path), lerr)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("creating triage directory: %w", err)
	}

	data, err := yaml.Marshal(rec)
	if err != nil {
		return "", fmt.Errorf("marshalling triage record: %w", err)
	}

	tmp, err := os.CreateTemp(dir, "triage-*.tmp")
	if err != nil {
		return "", fmt.Errorf("creating temp triage file: %w", err)
	}
	tmpPath := tmp.Name()
	// Best-effort cleanup: a no-op after a successful rename (target no longer
	// at tmpPath), removes the partial file on any pre-rename failure.
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("writing triage record: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("syncing triage record: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("closing triage record: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return "", fmt.Errorf("renaming triage record into place: %w", err)
	}
	return path, nil
}

// versionProbe reads only the version key without strict decoding, so a record
// carrying an unknown field or a malformed version can still be inspected for
// its version before the strict decode runs.
type versionProbe struct {
	Version any `yaml:"version"`
}

// Read parses a triage file with a version guard followed by strict schema
// decoding. A missing, malformed, or unsupported version returns an error
// naming the path and the offending version. A record whose version is valid
// but which carries an unrecognised field returns an error naming the path and
// the offending key. Neither path modifies the file, so a record this binary
// cannot fully understand is never overwritten (ENH-200 item 14).
//
// BUG-187: Read takes projectRoot so it can prove containment. It refuses a
// store directory that escapes the canonical project root, refuses a final entry
// that is not a direct regular file (before opening it), and reads the decoded
// bytes from that same checked entry. Every triage read surface -- direct reads,
// enumeration + read, and the mutation paths -- funnels through here, so the
// physical boundary cannot be bypassed by a future consumer.
func Read(projectRoot, path string) (*Record, error) {
	// Prove the store directory is contained, then that path is inside it. All
	// internal producers (Path, ListAll, ListForTC) already build paths under the
	// canonical store dir; withinStore is defence in depth for a future direct
	// caller.
	dir, err := storeDir(projectRoot)
	if err != nil {
		return nil, err
	}
	if !withinStore(path, dir) {
		return nil, fmt.Errorf("refusing triage record %s: it is outside the triage store %s",
			pathsafe.SanitizeControlChars(path), pathsafe.SanitizeControlChars(dir))
	}

	// Physical entry-type gate: refuse a symlink / FIFO / socket / directory /
	// device / dangling link before opening, and read the bytes from the same
	// regular entry whose type was checked.
	data, err := readRegularEntry(path)
	if err != nil {
		return nil, err
	}

	// BUG-187: path is repository-derived (an enumerated entry name GTMS did not
	// choose), so every diagnostic that echoes it renders the SANITIZED form. A
	// crafted regular file whose name carries a newline or ANSI escape and whose
	// body identity disagrees would otherwise reach these malformed-record
	// messages and forge a terminal line. sanitize is a no-op on a clean path, so
	// existing callers and BATS assertions that match a normal path are
	// unaffected (clause 4, terminal-safe refusals).
	safePath := pathsafe.SanitizeControlChars(path)

	// Version guard first: a wrong version must be reported as a version
	// problem even if the rest of the record is otherwise malformed.
	var probe versionProbe
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("parsing triage record %s: %w", safePath, err)
	}
	switch v := probe.Version.(type) {
	case nil:
		return nil, fmt.Errorf("triage record %s has no version; only version %d is supported", safePath, SchemaVersion)
	case int:
		if v != SchemaVersion {
			return nil, fmt.Errorf("triage record %s has unsupported version %d; only version %d is supported", safePath, v, SchemaVersion)
		}
	default:
		return nil, fmt.Errorf("triage record %s has malformed version %v; only version %d is supported", safePath, probe.Version, SchemaVersion)
	}

	// Strict decode: an unrecognised field is a hard error naming the key.
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	var rec Record
	if err := dec.Decode(&rec); err != nil {
		return nil, fmt.Errorf("invalid triage record %s: %w", safePath, err)
	}

	// Semantic + identity validation (ENH-201 items 10, 11). Structural
	// validation above (version guard + strict decode) accepts a record whose
	// fields are individually well-typed but collectively nonsensical: an empty
	// value, an absent test_case_id, status: banana, an unknown snapshot.result,
	// a lifecycle contradiction, or a body identity that disagrees with the
	// filename. This step refuses all of those with the path named. It lives in
	// Read so every caller -- including ENH-200's mutation paths -- inherits it.
	//
	// BOTH forms are passed, and the split matters. validate DECIDES on the raw
	// path and only RENDERS the sanitized one. Handing it the sanitized path
	// alone (as this code first did) made the identity comparison lossy: it
	// derives filepath.Base(path) and compares it to the name built forward from
	// the body, so a regular file whose real name carries a control character
	// where the body carries '?' would compare EQUAL and be accepted. That
	// silently weakened the ENH-201 identity invariant this boundary exists to
	// protect. Decisions use raw bytes; only diagnostics are sanitized.
	if err := rec.validate(path, safePath); err != nil {
		return nil, err
	}
	return &rec, nil
}

// validResults is the raw contract-result vocabulary a snapshot may carry.
// It is deliberately the RAW set (skip, not skipped); the reader's presentation
// normalisation never reaches the stored record.
var validResults = map[string]bool{
	"pass": true, "fail": true, "skip": true, "error": true,
}

// validate performs ENH-201 semantic and identity checks on a decoded record.
// Every error names path so a malformed record fails loudly and locatably.
// Timestamp strings stay opaque: a readable but unparseable classified_at or
// resolved_at is NOT rejected here (ENH-201 item 10).
func (r *Record) validate(path, displayPath string) error {
	// Required non-empty fields.
	required := []struct {
		name string
		val  string
	}{
		{"test_case_id", r.TestCaseID},
		{"framework", r.Framework},
		{"status", r.Status},
		{"value", r.Value},
		{"classified_at", r.ClassifiedAt},
		{"snapshot.task", r.Snapshot.Task},
		{"snapshot.result", r.Snapshot.Result},
	}
	for _, f := range required {
		if strings.TrimSpace(f.val) == "" {
			return fmt.Errorf("malformed triage record %s: required field '%s' is empty", displayPath, f.name)
		}
	}

	// status vocabulary. The VALUE is echoed, and it comes from the record body
	// -- repository-carried content GTMS did not choose -- so it is sanitized for
	// the same reason the path is (BUG-187 clause 4). A hostile body is exactly
	// as capable of forging a terminal line as a hostile filename; sanitizing
	// only the path left the other half of the message open.
	if r.Status != StatusActive && r.Status != StatusResolved {
		return fmt.Errorf("malformed triage record %s: status '%s' must be '%s' or '%s'",
			displayPath, pathsafe.SanitizeControlChars(r.Status), StatusActive, StatusResolved)
	}

	// snapshot.result vocabulary (raw contract set); the echoed value is
	// body-derived, so it is sanitized too.
	if !validResults[r.Snapshot.Result] {
		return fmt.Errorf("malformed triage record %s: snapshot.result '%s' must be one of pass, fail, skip, error",
			displayPath, pathsafe.SanitizeControlChars(r.Snapshot.Result))
	}

	// Lifecycle agreement: active has no resolved_at; resolved has one.
	switch r.Status {
	case StatusActive:
		if strings.TrimSpace(r.ResolvedAt) != "" {
			return fmt.Errorf("malformed triage record %s: an active record must not carry resolved_at", displayPath)
		}
	case StatusResolved:
		if strings.TrimSpace(r.ResolvedAt) == "" {
			return fmt.Errorf("malformed triage record %s: a resolved record must carry a non-empty resolved_at", displayPath)
		}
	}

	// Identity: the filename must be exactly what Write would have produced for
	// this body. Comparing the WHOLE basename rather than splitting the filename
	// is deliberate. A test case ID may itself contain "--" -- IsTestCaseID only
	// requires the "tc-" prefix, so tc-foo--bar is linkable and Write produces
	// tc-foo--bar--bats.triage.yaml (see wiring's
	// TestDiscoverForScope_TCIDContainingDoubleDash, pinning REV-130
	// CODEX-130-001). Splitting at the first "--" reads that back as
	// tc-foo / bar--bats, so Read would refuse a record Write had just created.
	//
	// Deriving the expected name forward from the body has no such ambiguity:
	// it round-trips every legal identity exactly, and it still catches a copied
	// or hand-edited file whose name and body disagree.
	wantBase := r.TestCaseID + identitySeparator + r.Framework + FileSuffix
	if gotBase := filepath.Base(path); gotBase != wantBase {
		// test_case_id and framework are body-derived and echoed, so both are
		// sanitized. wantBase is rendered with %q, which escapes control
		// characters itself, so it is already terminal-safe.
		return fmt.Errorf("malformed triage record %s: filename does not match its body identity (test_case_id '%s', framework '%s' imply %q)",
			displayPath, pathsafe.SanitizeControlChars(r.TestCaseID),
			pathsafe.SanitizeControlChars(r.Framework), wantBase)
	}
	return nil
}

// identitySeparator joins the test case ID and framework in a triage record's
// filename. It is NOT safe to split a filename on it: a test case ID may itself
// contain "--", so the split point is ambiguous. Build names forward from a
// record's identity; never parse them backward. See Read's identity check.
const identitySeparator = "--"

// ListForTC enumerates the triage records for one test case, returning the
// framework and absolute path of each. Filenames are matched by the
// {tc}--{framework}.triage.yaml shape without reading the files, so listing is
// cheap and independent of record contents. Framework names are returned in
// directory order; callers that need determinism should sort.
func ListForTC(projectRoot, testCaseID string) ([]TCRecordRef, error) {
	if err := pathsafe.ValidateFilenameComponent(testCaseID, "test case ID"); err != nil {
		return nil, err
	}
	// BUG-187: enumerate the CANONICAL store dir, refusing an escaped store so
	// every read surface built on this enumerator fails closed (tc-025dfdae).
	dir, err := storeDir(projectRoot)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading triage directory: %w", err)
	}

	prefix := testCaseID + "--"
	var out []TCRecordRef
	for _, e := range entries {
		// BUG-187: a directory is NOT skipped. Selection here is by NAME only, so
		// an entry called tc-x--bats.triage.yaml is a candidate record whatever it
		// physically is, and Read refuses it by type. Skipping directories made a
		// forged directory with a record-shaped name VANISH from the listing --
		// exit 0, no record, no diagnostic -- which is the one outcome this
		// boundary must never produce. Refusing loudly beats disappearing quietly.
		name := e.Name()
		if !strings.HasSuffix(name, FileSuffix) {
			continue
		}
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		framework := strings.TrimSuffix(strings.TrimPrefix(name, prefix), FileSuffix)
		if framework == "" {
			continue
		}
		out = append(out, TCRecordRef{
			Framework: framework,
			Path:      filepath.Join(dir, name),
		})
	}
	return out, nil
}

// TCRecordRef locates one triage record for a test case without reading it.
type TCRecordRef struct {
	Framework string
	Path      string
}

// RecordRef locates one triage record anywhere in the triage directory without
// reading it.
//
// It carries only the path. Identity deliberately is not inferred here: a test
// case ID may contain "--", so a filename cannot be split into (test case,
// framework) unambiguously. Callers that need identity get it from Read, which
// takes it from the record body and verifies the filename against it.
type RecordRef struct {
	Path string
}

// ListAll enumerates EVERY triage record in the project, returning one ref per
// file whose name ends in FileSuffix. It is the single canonical all-records
// enumerator for the root `gtms triage --list`, added alongside ListForTC so
// the CLI never open-codes a triage-directory scan (a second enumeration path
// would drift exactly the way the handoff scanner did before ENH-200 pinned
// it). A missing triage directory returns (nil, nil). Refs are returned in
// directory order; callers that need determinism should sort.
//
// It deliberately does NOT parse identity out of the filename, and therefore
// skips nothing that carries the suffix. A name the old parser could not split
// -- broken.triage.yaml, say -- was silently dropped, which broke two contracts
// at once: root list must include every triage record, and a malformed in-scope
// record must fail the whole listing rather than disappear from it. Identity is
// established by Read, from the body, where a mismatch is a named error.
func ListAll(projectRoot string) ([]RecordRef, error) {
	// BUG-187: enumerate the CANONICAL store dir, refusing an escaped store so the
	// root list and the status/gaps overlays that use it fail closed (tc-025dfdae
	// steps 2-3).
	dir, err := storeDir(projectRoot)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading triage directory: %w", err)
	}

	var out []RecordRef
	for _, e := range entries {
		// BUG-187: no IsDir skip -- see the note in ListForTC. A record-shaped
		// directory is enumerated as a candidate so Read refuses it by type,
		// instead of being omitted and reported as a clean empty store.
		if !strings.HasSuffix(e.Name(), FileSuffix) {
			continue
		}
		out = append(out, RecordRef{Path: filepath.Join(dir, e.Name())})
	}
	return out, nil
}
