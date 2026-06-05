// Package wiring owns the tracked automation-wiring schema introduced by
// CON-023 / ENH-145.
//
// A wiring record is a pure-YAML file at
// gtms/automation/wiring/{tc-id}--{framework}.wiring.yaml carrying exactly six
// identity fields: testcase, testcase-hash, framework, adapter, artefact,
// artefact-hash. No status, lifecycle, cycle, last-dev-result, or
// results-file — those are not identity. Pre-CON-023 lived in the legacy
// .automation.md shape via internal/pipeline; this package replaces the
// identity portion of that contract.
//
// gtms automate and gtms link are the only writers. gtms execute reads
// wiring, recomputes hashes, reports drift, and never mutates it — with one
// exception: the one-way pending → <real hash> bootstrap performed by
// gtms execute on first run for wiring records created by the built-in
// agent-automate / manual-automate adapters (ENH-151). After bootstrap, the
// "never mutates" rule holds for all subsequent execute runs.
package wiring

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
)

// PendingArtefactHash is the sentinel value written to artefact-hash by the
// built-in agent-automate / manual-automate adapters (ENH-151). It means
// "uninitialised, awaiting first execute." gtms execute recognises the
// sentinel and bootstraps it to a real hash before adapter invocation.
const PendingArtefactHash = "pending"

// realHashRegex matches a real artefact/testcase hash: exactly 16 lowercase
// hex characters, matching pipeline.HashFile's 8-byte SHA-256 truncation.
var realHashRegex = regexp.MustCompile(`^[0-9a-f]{16}$`)

// IsPendingArtefactHash reports whether value is the uninitialised sentinel.
func IsPendingArtefactHash(value string) bool {
	return value == PendingArtefactHash
}

// IsRealArtefactHash reports whether value is a valid 16-char lowercase hex
// hash as produced by pipeline.HashFile.
func IsRealArtefactHash(value string) bool {
	return realHashRegex.MatchString(value)
}

// FileSuffix is the on-disk suffix for wiring files.
const FileSuffix = ".wiring.yaml"

// WiringRecord is the on-disk wiring schema. Exactly six fields; the order
// here is the order yaml.Marshal emits.
type WiringRecord struct {
	TestCase     string `yaml:"testcase"`
	TestCaseHash string `yaml:"testcase-hash"`
	Framework    string `yaml:"framework"`
	Adapter      string `yaml:"adapter"`
	Artefact     string `yaml:"artefact"`
	ArtefactHash string `yaml:"artefact-hash"`
}

// Path returns the absolute on-disk path for a wiring record identified by
// (testCaseID, framework). Both inputs are validated as filename components.
func Path(projectRoot, testCaseID, framework string) (string, error) {
	if err := pathsafe.ValidateFilenameComponent(testCaseID, "test case ID"); err != nil {
		return "", err
	}
	if err := pathsafe.ValidateFilenameComponent(framework, "framework"); err != nil {
		return "", err
	}
	return filepath.Join(layout.WiringDir(projectRoot),
		fmt.Sprintf("%s--%s%s", testCaseID, framework, FileSuffix)), nil
}

// Write serialises rec to gtms/automation/wiring/{tc}--{framework}.wiring.yaml.
// The output is pure YAML — no "---" fences, no markdown body. The wiring
// directory is created on demand. Returns the absolute path written.
//
// All six fields must be present and valid. testcase-hash requires a real
// 16-char hex hash. artefact-hash may be either a real hash or the
// PendingArtefactHash sentinel (ENH-151). Callers that cannot supply
// hashes at write time should return an error to the user rather than
// write a partial record.
func Write(projectRoot string, rec *WiringRecord) (string, error) {
	if rec == nil {
		return "", errors.New("wiring record is required")
	}
	if err := rec.validate(); err != nil {
		return "", err
	}

	path, err := Path(projectRoot, rec.TestCase, rec.Framework)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", fmt.Errorf("creating wiring directory: %w", err)
	}

	data, err := yaml.Marshal(rec)
	if err != nil {
		return "", fmt.Errorf("marshalling wiring record: %w", err)
	}

	// Atomic write: serialise into a sibling temp file, fsync, then rename
	// onto the target. Rename replaces an existing file atomically on POSIX
	// and Windows (Go's stdlib uses MOVEFILE_REPLACE_EXISTING). This upholds
	// the ENH-151 bootstrap contract: if disk-full or an interrupted write
	// fails partway, the existing wiring is left untouched — Read will still
	// see the prior pending sentinel and the next execute can retry the
	// bootstrap cleanly. A non-atomic os.WriteFile could truncate the
	// existing wiring before the new bytes are flushed, leaving an
	// unreadable file on disk.
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "wiring-*.tmp")
	if err != nil {
		return "", fmt.Errorf("creating temp wiring file: %w", err)
	}
	tmpPath := tmp.Name()
	// Best-effort cleanup: succeeds-as-noop after a successful rename
	// (target no longer at tmpPath), removes the partial file on any
	// pre-rename failure.
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("writing wiring record: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("syncing wiring record: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("closing wiring record: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return "", fmt.Errorf("renaming wiring record into place: %w", err)
	}
	return path, nil
}

// Read parses a wiring file with strict schema enforcement. Unknown fields
// (status, lifecycle, cycle, last-dev-result, results-file, ...) are hard
// errors, not silently ignored. Missing required fields are also errors.
func Read(path string) (*WiringRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening wiring record: %w", err)
	}
	defer f.Close()

	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)

	var rec WiringRecord
	if err := dec.Decode(&rec); err != nil {
		return nil, fmt.Errorf("parsing wiring record %s: %w", path, err)
	}
	if err := rec.validate(); err != nil {
		return nil, fmt.Errorf("invalid wiring record %s: %w", path, err)
	}
	return &rec, nil
}

// Find looks up the wiring record for an explicit (testCaseID, framework)
// pair. Returns (nil, "", nil) when the file does not exist — no wiring is
// a legitimate state, not an error. Returns an error for any other open or
// parse failure (permission denied, malformed YAML, unknown fields, etc.)
// so the caller sees the underlying problem rather than treating it as
// "no wiring."
//
// Read wraps os.Open with %w, so errors.Is(err, os.ErrNotExist) propagates
// through the error chain correctly here — the previous string-match
// fallback (strings.Contains "opening wiring record") was over-broad and
// also swallowed permission errors as "no wiring."
func Find(projectRoot, testCaseID, framework string) (*WiringRecord, string, error) {
	path, err := Path(projectRoot, testCaseID, framework)
	if err != nil {
		return nil, "", err
	}
	rec, err := Read(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", nil
		}
		return nil, "", err
	}
	return rec, path, nil
}

// FindAllForTC returns every wiring record for a test case, one per framework.
// Records are returned in lexical order by framework. Filenames that do not
// match the {tc}--{framework}.wiring.yaml shape are skipped; parse errors are
// logged to stderr and the bad file is skipped (a single corrupt wiring record
// must not blind the reader to the rest).
func FindAllForTC(projectRoot, testCaseID string) ([]*WiringRecord, error) {
	if err := pathsafe.ValidateFilenameComponent(testCaseID, "test case ID"); err != nil {
		return nil, err
	}
	dir := layout.WiringDir(projectRoot)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading wiring directory: %w", err)
	}

	prefix := testCaseID + "--"
	var out []*WiringRecord
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, FileSuffix) {
			continue
		}
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		path := filepath.Join(dir, name)
		rec, err := Read(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: skipping wiring file %s: %v\n", path, err)
			continue
		}
		out = append(out, rec)
	}
	return out, nil
}

// Scan reads every wiring record under gtms/automation/wiring/, returning a
// map keyed by test case ID with one entry per framework. Bad files are
// skipped with a stderr warning; the rest of the scan continues.
//
// A missing wiring directory is treated as "no wiring yet" (nil, nil), not an
// error.
func Scan(projectRoot string) (map[string][]*WiringRecord, error) {
	dir := layout.WiringDir(projectRoot)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading wiring directory: %w", err)
	}

	out := make(map[string][]*WiringRecord)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, FileSuffix) {
			continue
		}
		path := filepath.Join(dir, name)
		rec, err := Read(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: skipping wiring file %s: %v\n", path, err)
			continue
		}
		out[rec.TestCase] = append(out[rec.TestCase], rec)
	}
	return out, nil
}

// validate enforces the six-field contract. Filename safety is re-checked
// here because Read does not know the path-derived names; Write re-checks
// via Path.
//
// ENH-151: artefact-hash accepts exactly two shapes — a real 16-char
// lowercase hex hash or PendingArtefactHash ("pending"). testcase-hash
// requires a real hash (no sentinel — the TC is fully written at automate
// time). The other four fields remain strictly non-empty.
func (r *WiringRecord) validate() error {
	if r.TestCase == "" {
		return errors.New("missing required field: testcase")
	}
	if !IsRealArtefactHash(r.TestCaseHash) {
		if r.TestCaseHash == "" {
			return errors.New("missing required field: testcase-hash")
		}
		return fmt.Errorf("invalid testcase-hash %q: must be a 16-char lowercase hex string", r.TestCaseHash)
	}
	if r.Framework == "" {
		return errors.New("missing required field: framework")
	}
	if r.Adapter == "" {
		return errors.New("missing required field: adapter")
	}
	if r.Artefact == "" {
		return errors.New("missing required field: artefact")
	}
	if !IsRealArtefactHash(r.ArtefactHash) && !IsPendingArtefactHash(r.ArtefactHash) {
		if r.ArtefactHash == "" {
			return errors.New("missing required field: artefact-hash")
		}
		return fmt.Errorf("invalid artefact-hash %q: must be a 16-char lowercase hex string or %q", r.ArtefactHash, PendingArtefactHash)
	}
	return nil
}
