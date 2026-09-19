// Package wiring owns the tracked automation-wiring schema introduced by
// CON-023 / ENH-145.
//
// A wiring record is a pure-YAML file at
// gtms/automation/wiring/{tc-id}--{framework}.wiring.yaml carrying exactly six
// identity fields: testcase, testcase-hash, framework, adapter, artefact,
// artefact-hash. No status, lifecycle, cycle, last-dev-result, or
// results-file -- those are not identity. Pre-CON-023 lived in the legacy
// .automation.md shape via internal/pipeline; this package replaces the
// identity portion of that contract.
//
// gtms automate, gtms link, and gtms link --adapter (repoint, ENH-192) are
// the writers. gtms execute reads wiring, recomputes hashes, reports drift,
// and never mutates it -- with one exception: the one-way pending -> <real
// hash> bootstrap performed by gtms execute on first run for wiring records
// created by the built-in agent-automate / manual-automate adapters (ENH-151).
// After bootstrap, the "never mutates" rule holds for all subsequent execute
// runs.
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

// ValidateWritable reports whether Write would reject rec, WITHOUT touching the
// filesystem: the six-field contract plus the filename-component safety checks
// on testcase and framework. Every check it runs is deterministic, so a caller
// that plans a write can guarantee the write itself will not fail validation.
//
// PlanRefresh uses it so a dry-run preview cannot approve a record the real
// writer will refuse (REV-129 CODEX-129-001). Before this existed, the
// filename-safety check ran only inside Write, so a stale record whose
// framework was not a safe filename component previewed as "Would refresh" and
// then failed for real.
func ValidateWritable(projectRoot string, rec *WiringRecord) error {
	_, err := plannedPath(projectRoot, rec)
	return err
}

// plannedPath runs every deterministic check Write performs before it writes,
// and returns the path Write would write to. It is the single definition of
// "this record is writable", shared by Write and ValidateWritable so the two
// can never drift apart.
func plannedPath(projectRoot string, rec *WiringRecord) (string, error) {
	if rec == nil {
		return "", errors.New("wiring record is required")
	}
	if err := rec.validate(); err != nil {
		return "", err
	}
	return Path(projectRoot, rec.TestCase, rec.Framework)
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
	path, err := plannedPath(projectRoot, rec)
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
//
// REV-158 CLAUDE-005: selection is by the record's PARSED body `testcase`, not
// by its filename prefix (ADR-024 s1). The filename prefix only decides which
// files are worth opening. A bare prefix returned
// "tc-77777777--legacy--bats.wiring.yaml" for tc-77777777, so `gtms execute
// tc-77777777` refused with "has multiple wiring records (bats, bats). Re-run
// with --framework <name>" -- an unactionable message naming the same framework
// twice, and the user could not execute the base test case at all. That is
// BUG-188's stated defect left reachable on a mutating consumer, since this
// selector also feeds bulk execute's framework resolution and triage. A record
// whose body declares a different identity than its filename implies is a
// data-integrity error owned by that other identity, so it is not this id's
// record either way.
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
		// Body identity is authoritative: the filename merely got the file opened.
		if rec.TestCase != testCaseID {
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

// DiscoveryResult holds the outcome of parsing a single wiring file. Unlike
// Scan and FindAllForTC which silently skip malformed files, DiscoverAll
// returns every file so callers can do per-file error accounting.
type DiscoveryResult struct {
	Path       string
	Record     *WiringRecord
	Err        error
	TCFromName string
	FWFromName string
}

// ParseFilename extracts the test case ID and framework from a wiring
// filename of the form {tc}--{framework}.wiring.yaml. Returns ("", "", false)
// when the filename does not match the expected shape.
func ParseFilename(name string) (tc, fw string, ok bool) {
	if !strings.HasSuffix(name, FileSuffix) {
		return "", "", false
	}
	stem := strings.TrimSuffix(name, FileSuffix)
	idx := strings.Index(stem, "--")
	if idx < 1 || idx == len(stem)-2 {
		return "", "", false
	}
	return stem[:idx], stem[idx+2:], true
}

// DiscoverAll reads every .wiring.yaml file in the wiring directory and
// returns a DiscoveryResult per file. Malformed files are included with a
// non-nil Err (and nil Record). Files whose name does not match the
// {tc}--{framework} shape are still returned with Err set.
//
// A missing wiring directory returns (nil, nil).
//
// Callers that only care about specific records should use DiscoverForScope so
// unrelated files are never opened.
func DiscoverAll(projectRoot string) ([]DiscoveryResult, error) {
	return discover(projectRoot, nil, "")
}

// DiscoverForScope is DiscoverAll narrowed to a known set of test case IDs, and
// optionally to one framework. A file is matched by its FILENAME, before it is
// opened, so an out-of-scope file is never read or parsed.
//
// That matters for more than speed. Refreshing one test case in a project with
// hundreds of wiring files should not read all of them (REV-129 CLAUDE-003),
// and an unrelated file that happens to sit in the wiring directory -- huge,
// corrupt, or not a regular file at all, where a FIFO would block the read
// indefinitely -- must not be able to affect a command that was never asked
// about it (REV-129 CODEX-129-002).
//
// Matching takes the LONGEST test case ID in scope that the filename starts with
// (see splitStemForScope), which is what makes it safe for IDs that themselves
// contain "--". ParseFilename splits on the FIRST "--", so it reads
// tc-foo--bar--bats.wiring.yaml as testcase "tc-foo", framework "bar--bats" --
// wrong for the perfectly linkable test case "tc-foo--bar" (IsTestCaseID only
// requires the "tc-" prefix). Scoping by ParseFilename made a refresh of
// "tc-foo--bar" report no wiring at all, and made a refresh of a folder
// containing "tc-foo" report the other record as an identity mismatch
// (REV-130 CODEX-130-001). Knowing the requested IDs removes the ambiguity.
//
// Identity validation is unaffected: the filename/content agreement check needs
// only the filename, which is known before the read.
//
// A filename that matches no ID in scope is skipped, not reported -- telling the
// caller about files outside the scope it asked for is exactly what scoping is
// meant to avoid.
func DiscoverForScope(projectRoot string, tcIDs []string, framework string) ([]DiscoveryResult, error) {
	scope := make(map[string]bool, len(tcIDs))
	for _, id := range tcIDs {
		scope[id] = true
	}
	return discover(projectRoot, scope, framework)
}

// splitStemForScope resolves a {tc}--{framework} filename stem against a known
// set of test case IDs, preferring the LONGEST ID that matches so an ID
// containing "--" wins over a shorter ID that happens to be its prefix. Both
// halves must be non-empty. Returns ok=false when no ID in scope matches.
func splitStemForScope(stem string, scope map[string]bool) (tc, framework string, ok bool) {
	best := ""
	for id := range scope {
		if len(id) <= len(best) {
			continue
		}
		if strings.HasPrefix(stem, id+"--") && len(stem) > len(id)+2 {
			best = id
		}
	}
	if best == "" {
		return "", "", false
	}
	return best, stem[len(best)+2:], true
}

// TCFromNameForScope attributes a wiring FILENAME to the test case id it belongs
// to, chosen as the LONGEST id in candidates that the filename starts with. It is
// the exported form of splitStemForScope for callers that must decide which id a
// record is RELEVANT to when the record's body cannot be read.
//
// This exists because ParseFilename splits on the FIRST "--", which cannot tell
// "tc-77777777--legacy--bats.wiring.yaml" (testcase tc-77777777--legacy,
// framework bats) from the same name read as testcase tc-77777777, framework
// "legacy--bats". Both readings are grammatical; only knowing which ids actually
// exist resolves it. Attributing by the first "--" filed an extended id's
// corruption under its shorter prefix sibling, which BOTH hid the corruption from
// the id that owned it AND blocked the unrelated shorter id (BUG-188 resurfacing
// inside F2's fix).
//
// It answers RELEVANCE ONLY, never identity. A readable record's identity is its
// parsed body `testcase` field (ADR-024 s1) and this function must not be used to
// override it; it is for records whose body is unreadable, malformed, or
// self-contradictory, where the filename is the only signal left.
//
// Returns ok=false when no candidate id matches, meaning the record is
// unattributable and relevant to no id.
func TCFromNameForScope(name string, candidates []string) (string, bool) {
	scope := make(map[string]bool, len(candidates))
	for _, id := range candidates {
		if id != "" {
			scope[id] = true
		}
	}
	tc, _, ok := splitStemForScope(strings.TrimSuffix(name, FileSuffix), scope)
	return tc, ok
}

// discover is the shared body of DiscoverAll and DiscoverForScope. A nil scope
// means "every file", which also restores DiscoverAll's contract of reporting a
// filename that does not match the {tc}--{framework} shape as a failing result:
// with no requested IDs to match against there is no scope such a file could be
// outside of.
func discover(projectRoot string, scope map[string]bool, framework string) ([]DiscoveryResult, error) {
	dir := layout.WiringDir(projectRoot)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading wiring directory: %w", err)
	}

	var out []DiscoveryResult
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, FileSuffix) {
			continue
		}
		path := filepath.Join(dir, name)

		// Pre-open gate (safety/perf). Identity is decided by the record BODY below
		// (ADR-024 s1); the filename only governs WHETHER we open the file.
		//   - Scoped: open only files whose filename plausibly belongs to a
		//     requested id, so an unrelated (huge/corrupt/FIFO) file the caller
		//     never asked about is never read (REV-129).
		//   - Unscoped (DiscoverAll): a filename that cannot even be a
		//     {tc}--{framework} shape is reported without a read (it can never be a
		//     valid wiring record), preserving the no-read behaviour for obviously
		//     malformed names while still opening every plausibly-shaped file.
		if scope != nil {
			_, fnFW, ok := splitStemForScope(strings.TrimSuffix(name, FileSuffix), scope)
			if !ok {
				continue
			}
			// Framework filter in the pre-open gate so a framework-excluded file is
			// never read (REV-129). splitStemForScope prefers the LONGEST scope id,
			// so when the record's own body id is in scope the filename framework it
			// yields equals the body framework; the post-read scope check below is
			// the authority either way.
			if framework != "" && fnFW != framework {
				continue
			}
		} else if _, _, shapeOK := ParseFilename(name); !shapeOK {
			out = append(out, DiscoveryResult{
				Path: path,
				Err:  fmt.Errorf("filename %q does not match {tc}--{framework}%s shape", name, FileSuffix),
			})
			continue
		}

		rec, readErr := Read(path)
		if readErr != nil {
			// Best-effort filename-derived labels for the diagnostic line only.
			tcN, fwN, _ := ParseFilename(name)
			out = append(out, DiscoveryResult{Path: path, Err: readErr, TCFromName: tcN, FWFromName: fwN})
			continue
		}

		// ADR-024 s1: the record body is authoritative and the filename MUST
		// reconstruct from it. A filename that does not reconstruct is a
		// data-integrity error (a cross-record overwrite vector: a file named
		// A--fw declaring testcase B would otherwise be selectable as B and its
		// write would land on B's real record), NOT a valid record. Reconstructing
		// from the body -- rather than splitting the filename at the first "--" --
		// is what correctly parses an extended id whose own separator contains "--"
		// (tc-x--legacy--bats), which ParseFilename could not (tc-d1c39672, and the
		// tc-f7d8f7c1/tc-31d05fff extended-id wiring cases).
		expected := rec.TestCase + "--" + rec.Framework + FileSuffix
		if expected != name {
			// Label the failure by the FILENAME-derived identity (best effort), not
			// the body's -- a per-id caller (folder refresh) attributes the failure
			// to the id it asked about, and a user greps for the filename's id. The
			// message names both sides so the disagreement is legible.
			tcN, fwN, _ := ParseFilename(name)
			out = append(out, DiscoveryResult{
				Path:       path,
				Err:        fmt.Errorf("wiring identity mismatch: filename %q implies testcase %q framework %q but record declares testcase %q framework %q", name, tcN, fwN, rec.TestCase, rec.Framework),
				TCFromName: tcN,
				FWFromName: fwN,
			})
			continue
		}

		// Scoped SELECTION is by body identity: a record whose body id is not in
		// the requested scope -- e.g. an extended sibling whose filename prefix
		// matched a shorter requested id in the pre-open gate -- is silently
		// skipped, never reported (it is out of the scope the caller asked for).
		if scope != nil {
			if !scope[rec.TestCase] {
				continue
			}
			if framework != "" && rec.Framework != framework {
				continue
			}
		}

		out = append(out, DiscoveryResult{
			Path:       path,
			Record:     rec,
			TCFromName: rec.TestCase,
			FWFromName: rec.Framework,
		})
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
