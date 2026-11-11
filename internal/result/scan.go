package result

// BUG-209 section 8: the shared results-directory scanner. It owns ALL
// enumeration of .gtms/results/. It verifies and opens the directory ONCE (an
// escaping link is refused before anything is enumerated, so even an empty
// outside directory is refused rather than shown as "no results"), then reads
// every *.handoff.yaml through that verified no-follow handle. An unsafe leaf
// becomes a refusal, not a row; a malformed or unreadable regular file is skipped
// silently, as the readers did before.
//
// No production caller uses os.ReadDir, filepath.Glob or an equivalent on the
// handoff directory outside this abstraction (the source guard enforces that).

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
	"gopkg.in/yaml.v3"
)

// ScanEntry is one safe, parseable handoff found by the scanner.
type ScanEntry struct {
	Base  string          // file name, e.g. task-ab12cd34.handoff.yaml
	RC    *ResultContract // parsed contract
	MTime int64           // file modification time (UnixNano), for stamp-tie breaking
}

// ScanRefusal names one unsafe handoff leaf the scanner refused to read.
type ScanRefusal struct {
	Base string
	Err  *RefusedHandoffError
}

// Scan is the result of opening and reading the results directory once. The
// caller MUST call Close. When DirRefusal is non-nil the whole directory was
// refused and Entries/Refusals are empty; when Absent is true the directory does
// not exist (behave as today, silently).
type Scan struct {
	root       *boundDir
	names      []string // every entry name, for leftover-staged-file matching
	Entries    []ScanEntry
	Refusals   []ScanRefusal
	DirRefusal *RefusedDirError
	Absent     bool
}

// scanReadFailure is the InjectScanReadFailureForTest seam.
var scanReadFailure error

// scanOpenCounter counts OpenScan calls for a test. nil in production.
var scanOpenCounter *int

// CountScansForTest makes OpenScan increment *counter, so a test can prove a command
// scans the results directory once. Pass nil to clear. Not for production use.
func CountScansForTest(counter *int) {
	scanOpenCounter = counter
}

// inspectionRefusal is the refusal for an unexpected failure to open or list the
// results directory. BUG-209 AC31: it is refused and reported, never shown as no
// results.
func inspectionRefusal(err error) *RefusedDirError {
	return &RefusedDirError{
		Rel:    logicalResultsRel,
		Reason: "could not be inspected: " + pathsafe.SanitizeControlChars(err.Error()),
	}
}

// OpenScan verifies and opens .gtms/results under projectRoot and reads every
// handoff through the verified handle. It has no error return on purpose, so no
// caller can turn a failure into "no results": any failure to open or list the
// directory is reported on Scan.DirRefusal, an unsafe leaf on Scan.Refusals, and only
// a directory that really does not exist sets Scan.Absent. Callers apply the
// section-8 command matrix to those fields.
func OpenScan(projectRoot string) *Scan {
	if scanOpenCounter != nil {
		*scanOpenCounter++
	}
	root, absent, err := openResultsDirCanonical(projectRoot)
	if err != nil {
		var rd *RefusedDirError
		if errors.As(err, &rd) {
			return &Scan{DirRefusal: rd}
		}
		return &Scan{DirRefusal: inspectionRefusal(err)}
	}
	if absent {
		return &Scan{Absent: true}
	}

	s := &Scan{root: root}
	entries, readErr := listThroughHandle(root)
	if readErr == nil {
		readErr = scanReadFailure
	}
	if readErr != nil {
		root.Close()
		return &Scan{DirRefusal: inspectionRefusal(readErr)}
	}
	for _, e := range entries {
		name := e.Name()
		s.names = append(s.names, name)
		// Only the name decides. A directory with this suffix is not skipped: it
		// reaches readLeaf and is refused (section 8, CLAUDE-002).
		if !strings.HasSuffix(name, ".handoff.yaml") {
			continue // ignores *.tmp-<rand> staged files and non-handoff entries
		}
		read, rErr := readLeaf(root, name)
		if rErr != nil {
			var rd *RefusedDirError
			if errors.As(rErr, &rd) {
				// The directory itself changed, so nothing from it can be believed.
				root.Close()
				return &Scan{DirRefusal: rd}
			}
			var rh *RefusedHandoffError
			switch {
			case errors.As(rErr, &rh):
				s.Refusals = append(s.Refusals, ScanRefusal{Base: name, Err: rh})
			case errors.Is(rErr, os.ErrNotExist):
				// Removed since it was listed, so it is simply gone.
			case errors.Is(rErr, os.ErrPermission):
				// An unreadable (mode 000) regular file is skipped silently, as the
				// pre-BUG-209 readers did (section 8).
			default:
				// Any other failure could hide the newest evidence, so it is refused
				// rather than treated as no result (CLAUDE-011).
				s.Refusals = append(s.Refusals, ScanRefusal{Base: name, Err: refusalForLeaf(name, kindInspectErr, rErr)})
			}
			continue
		}
		rc, parseErr := parseContractBytes(read.data)
		if parseErr != nil || rc == nil {
			continue // a malformed regular file is skipped, as before
		}
		s.Entries = append(s.Entries, ScanEntry{Base: name, RC: rc, MTime: read.mtime})
	}
	return s
}

// listThroughHandle enumerates the verified directory through its handle, in name
// order (section 8, CLAUDE-004, CODEX-007).
func listThroughHandle(b *boundDir) ([]os.DirEntry, error) {
	raceHook("dir-list")
	d, err := b.Open(".")
	if err != nil {
		return nil, err
	}
	defer d.Close()
	entries, err := d.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}

// Close releases the directory handle. Safe to call on a DirRefusal/Absent scan.
func (s *Scan) Close() {
	if s != nil && s.root != nil {
		s.root.Close()
		s.root = nil
	}
}

// Dir returns the resolved absolute results directory, or "" when none was
// opened (absent or refused).
func (s *Scan) Dir() string {
	if s == nil || s.root == nil {
		return ""
	}
	return s.root.Name()
}

// RemoveEntry deletes one regular single-link file named name, through the verified
// handle, after rechecking the directory where the platform needs it. An entry that
// has become unsafe since the scan is refused and left untouched, so nothing is
// followed or removed through a link. A missing file is not an error.
func (s *Scan) RemoveEntry(name string) error {
	if s == nil || s.root == nil {
		return nil
	}
	if err := s.root.recheck("dir-delete"); err != nil {
		return err
	}
	kind, _, cause := classifyLeaf(s.root, name)
	if kind == kindAbsent {
		return nil
	}
	if ref := refusalForLeaf(name, kind, cause); ref != nil {
		return ref
	}
	raceHook("leaf-delete")
	if err := s.root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Remove deletes the handoff named base and any safe leftover staged file of exactly the
// generated form, through the verified handle. A file that merely shares the prefix is
// kept and not mentioned. A staged-named entry that is not a regular single-link file is
// kept and returned as a refusal, so reset and delete treat it the same (AC35).
func (s *Scan) Remove(base string) ([]*RefusedHandoffError, error) {
	if s == nil || s.root == nil {
		return nil, nil
	}
	if err := s.RemoveEntry(base); err != nil {
		return nil, err
	}
	safe, refused := s.LeftoversFor(base)
	for _, name := range safe {
		err := s.RemoveEntry(name)
		var rh *RefusedHandoffError
		switch {
		case err == nil:
		case errors.As(err, &rh):
			refused = append(refused, rh)
		default:
			return refused, err
		}
	}
	return refused, nil
}

// LeftoversFor sorts the entries named in exactly the generated staged form for base.
// Regular single-link files are safe to remove (AC21). Anything else with that name is
// refused and left in place, so a plan or a dry run reports what a real run would
// (AC35).
func (s *Scan) LeftoversFor(base string) (safe []string, refused []*RefusedHandoffError) {
	if s == nil || s.root == nil {
		return nil, nil
	}
	for _, name := range s.names {
		if !isStagedNameFor(base, name) {
			continue
		}
		kind, _, cause := classifyLeaf(s.root, name)
		if kind == kindAbsent {
			continue
		}
		if ref := refusalForLeaf(name, kind, cause); ref != nil {
			refused = append(refused, ref)
			continue
		}
		safe = append(safe, name)
	}
	return safe, refused
}

// HandoffRelFor returns the project-relative forward-slash path a diagnostic uses
// for base.
func HandoffRelFor(base string) string {
	return handoffRel(base)
}

// parseContractBytes parses handoff bytes into a ResultContract, applying the
// same YAML sanitisation Read uses.
func parseContractBytes(data []byte) (*ResultContract, error) {
	sanitized := sanitizeResultYAML(data)
	var rc ResultContract
	if err := yaml.Unmarshal(sanitized, &rc); err != nil {
		return nil, err
	}
	return &rc, nil
}

// handoffPathFor returns the lexical handoff path under projectRoot for a task id,
// used by callers that must build a path for a single-file Read/Update/Reconstruct
// after a scan.
func handoffPathFor(projectRoot, base string) string {
	return filepath.Join(projectRoot, ".gtms", "results", base)
}
