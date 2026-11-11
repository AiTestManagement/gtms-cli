package result

// BUG-209: one shared implementation owns every GTMS-owned handoff operation --
// inspection, no-follow opening, reading, metadata capture, staging, atomic
// replacement and durability. Create, Update, Reconstruct and Read are thin
// contracts over it (result.go), and the shared results-directory scanner
// (scan.go) owns all enumeration of .gtms/results/.
//
// The approved contract is BUG-209 sections 1-10. The parts that are platform
// specific -- metadata capture/apply, directory fsync,
// the hard-link count and the Windows replace-with-retry -- live in
// safehandoff_unix.go and safehandoff_windows.go behind the small surface this
// file calls (classifyLinkCount, publishStaged).

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
)

// Section-10 reasons. These strings are the stable core the specs assert.
const (
	reasonIsLink        = "is a link"
	reasonHardLinked    = "has more than one hard link"
	reasonNotRegular    = "is not a regular file"
	reasonAlreadyExists = "already exists"
	reasonOutside       = "resolves outside the project"
	// inspection failure uses "could not be inspected: <cause>".
	reasonChanged = "could not be inspected: changed during the write"
)

// logicalResultsRel is the logical directory every diagnostic names, even when
// .gtms itself is the escaping ancestor (BUG-209 section 10).
const logicalResultsRel = ".gtms/results"

// handoffFirstLineRemediation and dirRemediation are the second lines of the
// section-10 diagnostics.
const (
	handoffRemediation = "remove it by hand, or replace it with an ordinary single-link regular file under .gtms/results, and retry"
	dirRemediation     = "make it an ordinary directory inside the project, or point its link to a directory inside the project, and retry"
)

// RefusedHandoffError is returned when a handoff leaf is unsafe to read or write
// (a link, a hard link, a special file, a Create collision, or uninspectable).
// Its Rel is the project-relative forward-slash handoff path.
type RefusedHandoffError struct {
	Rel    string
	Reason string
}

// FirstLine is the "what went wrong" line specs assert on its own.
//
// Control characters are escaped here, for display only (section 10). Rel keeps the
// raw name, so a caller can still tell two paths apart.
func (e *RefusedHandoffError) FirstLine() string {
	return fmt.Sprintf("refused handoff %s: %s", pathsafe.SanitizeControlChars(e.Rel), pathsafe.SanitizeControlChars(e.Reason))
}

func (e *RefusedHandoffError) Error() string {
	return e.FirstLine() + "\n    " + handoffRemediation
}

// Remediation is the second ("what to do about it") line of the diagnostic, so a
// caller can render it as the CLI's error/warning hint.
func (e *RefusedHandoffError) Remediation() string { return handoffRemediation }

// RefusedDirError is returned when .gtms/results (or .gtms above it) resolves
// outside the project, or cannot be inspected. Rel is always ".gtms/results".
type RefusedDirError struct {
	Rel    string
	Reason string
}

// FirstLine is the "what went wrong" line specs assert on its own.
func (e *RefusedDirError) FirstLine() string {
	return fmt.Sprintf("refused results directory %s: %s", pathsafe.SanitizeControlChars(e.Rel), pathsafe.SanitizeControlChars(e.Reason))
}

func (e *RefusedDirError) Error() string {
	return e.FirstLine() + "\n    " + dirRemediation
}

// Remediation is the second ("what to do about it") line of the diagnostic.
func (e *RefusedDirError) Remediation() string { return dirRemediation }

// maxUpdateRetries is how many times Update re-reads, re-merges and republishes after
// the initial attempt, when another writer safely replaced the handoff (section 6).
const maxUpdateRetries = 3

// errLeafReplaced says a safe regular file is now at the destination, but not the one
// that was inspected. Update retries on it. For any other operation it becomes a
// refusal, in writeHandoff.
var errLeafReplaced = errors.New("the handoff was replaced by another writer")

// ConcurrentUpdateError is returned when other writers kept safely replacing a handoff
// through every retry. The file is healthy, so this is deliberately not a refusal: it
// does not satisfy IsRefusal and carries no remediation.
type ConcurrentUpdateError struct {
	Rel     string
	Retries int
}

func (e *ConcurrentUpdateError) Error() string {
	return fmt.Sprintf("handoff %s kept changing during the update: other writers replaced it through %d retries",
		pathsafe.SanitizeControlChars(e.Rel), e.Retries)
}

// IsConcurrentUpdate reports whether err is (or wraps) a *ConcurrentUpdateError.
func IsConcurrentUpdate(err error) bool {
	var ce *ConcurrentUpdateError
	return errors.As(err, &ce)
}

// IsRefusal reports whether err is (or wraps) a BUG-209 refusal of either kind.
func IsRefusal(err error) bool {
	var rh *RefusedHandoffError
	var rd *RefusedDirError
	return errors.As(err, &rh) || errors.As(err, &rd)
}

// InjectPublishFailureForTest installs a failure-injection hook that makes a
// staged publication fail at a named boundary ("write", "meta", "sync",
// "rename"), with the bytes about to be published passed in so a test can target
// one specific write. It exists because BUG-209 makes read-only handoffs
// replaceable, so the ENH-188 persistence-failure tests can no longer force a
// write failure with a 0444 file and instead inject one here (BUG-209
// "Interaction With ENH-188"). Pass nil to clear. Not for production use.
func InjectPublishFailureForTest(fn func(stage string, data []byte) error) {
	publishStagedHook = fn
}

// InjectScanReadFailureForTest makes OpenScan's directory listing fail with err,
// which is otherwise hard to force after the directory opened. Pass nil to clear.
// Not for production use.
func InjectScanReadFailureForTest(err error) {
	scanReadFailure = err
}

// raceHookFn is the deterministic swap seam: a test uses it to swap a directory or a
// leaf at one exact point, rather than relying on timing. nil in production.
var raceHookFn func(stage string)

func raceHook(stage string) {
	if raceHookFn != nil {
		raceHookFn(stage)
	}
}

// boundDir is the verified results directory. The embedded root and dirFile are
// handles, so on Unix every operation is relative to the verified directory and a
// later swap of its path cannot redirect it. Windows uses paths for some calls, and
// so rechecks before use (see recheck).
type boundDir struct {
	*os.Root
	dirFile          *os.File    // "." through the root: openat base and directory fsync
	info             os.FileInfo // the verified directory, from the handle
	absPath          string
	physicalAtBind   string // absPath with every link resolved, when it was bound
	canonicalProject string // empty outside the canonical .gtms/results layout
}

// Close releases both handles.
func (b *boundDir) Close() error {
	if b.dirFile != nil {
		_ = b.dirFile.Close()
	}
	return b.Root.Close()
}

// verify proves that the path still resolves inside the project and to the directory
// the handle is bound to. A swap to an outside link is refused as outside; any other
// change or failure is refused as uninspectable.
func (b *boundDir) verify() error {
	changed := &RefusedDirError{Rel: logicalResultsRel, Reason: "could not be inspected: changed during the check"}

	// The logical path, through any link in its spelling.
	if b.canonicalProject != "" {
		logical, _, err := pathsafe.ResolveUnderRoot(b.canonicalProject, logicalResultsRel)
		if err != nil {
			return refusalForResolveError(err)
		}
		cur, err := os.Stat(logical)
		if err != nil {
			return inspectionRefusal(err)
		}
		if !os.SameFile(cur, b.info) {
			return changed
		}
	}

	// The physical path, which is the one path-based operations really use. Rechecking
	// only the logical path would pass when a link is retargeted to keep it right
	// while something else is put at the physical path (section 4, CLAUDE-002).
	if b.canonicalProject != "" {
		if _, _, err := pathsafe.ResolveUnderRoot(b.canonicalProject, b.absPath); err != nil {
			return refusalForResolveError(err)
		}
	}
	cur, err := os.Stat(b.absPath)
	if err != nil {
		return inspectionRefusal(err)
	}
	if !os.SameFile(cur, b.info) {
		return changed
	}
	resolved, err := filepath.EvalSymlinks(b.absPath)
	if err != nil {
		return inspectionRefusal(err)
	}
	if resolved != b.physicalAtBind {
		return changed
	}
	return nil
}

// recheck re-verifies the directory before a path-based use, on the platforms that
// have them. stage names the point for the swap seam.
func (b *boundDir) recheck(stage string) error {
	raceHook(stage)
	if !dirRechecks {
		return nil
	}
	return b.verify()
}

// bindDir verifies a just-opened root against the path and the project, and holds the
// handles. os.OpenRoot follows links, so the opened directory could otherwise be one
// swapped in after the path was resolved (CLAUDE-003).
func bindDir(r *os.Root, absDir, canonicalProject string) (*boundDir, error) {
	raceHook("dir-bind")
	info, err := r.Stat(".")
	if err != nil {
		r.Close()
		return nil, inspectionRefusal(err)
	}
	physical, err := filepath.EvalSymlinks(absDir)
	if err != nil {
		r.Close()
		return nil, inspectionRefusal(err)
	}
	b := &boundDir{Root: r, info: info, absPath: absDir, physicalAtBind: physical, canonicalProject: canonicalProject}
	if err := b.verify(); err != nil {
		r.Close()
		return nil, err
	}
	d, err := r.Open(".")
	if err != nil {
		r.Close()
		return nil, inspectionRefusal(err)
	}
	b.dirFile = d
	return b, nil
}

// refusalForResolveError maps a failed containment resolve to its refusal. Only a path
// really resolving outside the project is reported as outside; anything else is
// refused with its cause, because it is not known to be outside (CLAUDE-012).
func refusalForResolveError(resolveErr error) *RefusedDirError {
	if errors.Is(resolveErr, pathsafe.ErrOutsideRoot) {
		return &RefusedDirError{Rel: logicalResultsRel, Reason: reasonOutside}
	}
	cause := errors.Unwrap(resolveErr)
	if cause == nil {
		cause = resolveErr
	}
	return inspectionRefusal(cause)
}

// leafKind classifies a handoff leaf inspected without following links.
type leafKind int

const (
	kindAbsent leafKind = iota
	kindRegular         // single-link regular file
	kindSymlink
	kindHardLinked
	kindDirectory
	kindSpecial // FIFO, device, socket
	kindInspectErr
)

// reasonForKind maps an unsafe leaf kind to its section-10 reason.
func reasonForKind(k leafKind) string {
	switch k {
	case kindSymlink:
		return reasonIsLink
	case kindHardLinked:
		return reasonHardLinked
	case kindDirectory, kindSpecial:
		return reasonNotRegular
	default:
		return reasonNotRegular
	}
}

// projectRootFromHandoffPath derives the project root from a handoff path. Every
// caller builds the path as filepath.Join(projectRoot, ".gtms", "results",
// "<task>.handoff.yaml") (result.ResultPath and the scan sites), so the project
// root is three directories up. The lexical form is used even when .gtms/results
// is a link; containment is then resolved from that root.
func projectRootFromHandoffPath(path string) string {
	return filepath.Dir(filepath.Dir(filepath.Dir(path)))
}

// handoffRel returns the project-relative, forward-slash path of a handoff base
// name for diagnostics. It always names .gtms/results, never the link target.
func handoffRel(base string) string {
	return logicalResultsRel + "/" + base
}

// VerifyResultsDirContainment reports whether .gtms/results (and .gtms above it)
// resolves inside the project. It returns a *RefusedDirError when it escapes, and
// nil when it is contained OR absent-but-contained. Callers use it to refuse
// BEFORE creating any .gtms subdirectory (BUG-209 tc-c5b20358 Step 4 / AC7), so an
// escaping .gtms link never has results/worktrees/logs/tmp created inside its
// target.
func VerifyResultsDirContainment(projectRoot string) error {
	canonical := pathsafe.CanonicalRoot(projectRoot)
	if _, _, err := pathsafe.ResolveUnderRoot(canonical, logicalResultsRel); err != nil {
		return refusalForResolveError(err)
	}
	return nil
}

// VerifyCreatedResultsDir opens and verifies .gtms/results after GTMS has created the
// .gtms directories, before anything uses them (section 4, owner decision 2026-11-07).
// A swap during creation is refused here. The instant between the earlier containment
// check and the creation itself stays open and is accepted for v0.4.1: a process able
// to win it already has write access to the project.
func VerifyCreatedResultsDir(projectRoot string) error {
	raceHook("dir-created")
	b, absent, err := openResultsDirCanonical(projectRoot)
	if err != nil {
		return err
	}
	if absent {
		return &RefusedDirError{Rel: logicalResultsRel, Reason: "could not be inspected: missing after it was created"}
	}
	return b.Close()
}

// isCanonicalLayout reports whether dir is a real GTMS results directory
// (.../.gtms/results), so the .gtms escape check applies. Tests that stage a
// handoff in a flat temp directory are non-canonical: the directory is opened
// directly with no .gtms containment concern (there is no .gtms layering to
// escape), while the generic per-leaf safety still applies.
func isCanonicalLayout(dir string) bool {
	return filepath.Base(dir) == "results" && filepath.Base(filepath.Dir(dir)) == ".gtms"
}

// openResultsDirForPath verifies and opens the results directory that holds path.
// For the canonical .gtms/results layout it verifies containment against the
// project root (an escaping link -> *RefusedDirError); otherwise it opens the
// handoff's own directory directly. The returned root confines every subsequent
// operation and refuses to traverse an escaping link. Callers still classify each
// leaf explicitly, because os.Root follows an in-root leaf symlink while the
// contract refuses all leaf links.
//
// Returns (root, false, nil) when the directory exists and is safe (caller closes
// root), (nil, true, nil) when it is absent, and (nil, false, *RefusedDirError)
// when it resolves outside or cannot be inspected.
func openResultsDirForPath(path string) (root *boundDir, absent bool, err error) {
	dir := filepath.Dir(path)
	if isCanonicalLayout(dir) {
		return openResultsDirCanonical(projectRootFromHandoffPath(path))
	}
	return openRootAtDir(dir, "")
}

// openResultsDirCanonical verifies .gtms/results (and .gtms above it) resolves
// inside the project, then opens an os.Root bound to the resolved directory. Used
// by both the path-based writers/readers and the shared scanner (scan.go).
func openResultsDirCanonical(projectRoot string) (root *boundDir, absent bool, err error) {
	canonical := pathsafe.CanonicalRoot(projectRoot)
	abs, _, resolveErr := pathsafe.ResolveUnderRoot(canonical, logicalResultsRel)
	if resolveErr != nil {
		// BUG-209 AC31: anything but a real escape is refused with its cause. It is
		// not known to be outside, and it is not "no results".
		return nil, false, refusalForResolveError(resolveErr)
	}
	return openRootAtDir(abs, canonical)
}

// openRootAtDir opens an os.Root at an already-resolved absolute directory,
// mapping a missing directory to absent and any other failure to a
// *RefusedDirError that names the logical .gtms/results directory.
func openRootAtDir(absDir, canonicalProject string) (root *boundDir, absent bool, err error) {
	// BUG-209 AC35: os.OpenRoot opens the path before it checks for a directory, and that
	// open blocks on a FIFO, so anything but a directory is refused before opening it.
	info, statErr := os.Stat(absDir)
	if statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return nil, true, nil
		}
		return nil, false, inspectionRefusal(statErr)
	}
	if !info.IsDir() {
		return nil, false, inspectionRefusal(errors.New("not a directory"))
	}
	r, openErr := os.OpenRoot(absDir)
	if openErr != nil {
		if errors.Is(openErr, os.ErrNotExist) {
			return nil, true, nil
		}
		return nil, false, inspectionRefusal(openErr)
	}
	b, bindErr := bindDir(r, absDir, canonicalProject)
	if bindErr != nil {
		return nil, false, bindErr
	}
	return b, false, nil
}

// classifyLeaf inspects base within root without following links and returns its
// kind. An inspection failure (Lstat failing for a reason other than not-exist,
// or an unreadable hard-link count) returns kindInspectErr plus the cause -- the
// caller then fails closed (BUG-208/213 rule, never assume a link count of one).
func classifyLeaf(b *boundDir, base string) (leafKind, os.FileInfo, error) {
	var info os.FileInfo
	err := retryInspect(func() error {
		var e error
		info, e = b.Lstat(base)
		return e
	})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return kindAbsent, nil, nil
		}
		return kindInspectErr, nil, err
	}
	kind := classifyMode(info.Mode())
	if kind != kindRegular {
		return kind, info, nil
	}
	n, cerr := classifyLinkCount(b, base, info)
	if cerr != nil {
		return kindInspectErr, info, cerr
	}
	if n > 1 {
		return kindHardLinked, info, nil
	}
	return kindRegular, info, nil
}

// classifyMode maps a file mode to a leaf kind. A hard-link count is checked apart
// from this, by callers holding the file's information.
func classifyMode(mode os.FileMode) leafKind {
	switch {
	case mode&os.ModeSymlink != 0:
		return kindSymlink
	case mode.IsDir():
		return kindDirectory
	case mode.IsRegular():
		return kindRegular
	default:
		return kindSpecial
	}
}

// leafRead is one read of a leaf: the bytes, and the identity and modification time
// of the file they came from.
type leafRead struct {
	data  []byte
	id    leafID
	mtime int64
}

// leafOpenError explains a failed open of a leaf. A link, hard link or special file is
// refused; a missing file is reported as not found; anything else, including a mode-000
// regular file, is returned as a plain read error, so BUG-191 recovery still runs.
func leafOpenError(b *boundDir, base string, openErr error) error {
	kind, _, cause := classifyLeaf(b, base)
	if ref := refusalForLeaf(base, kind, cause); ref != nil {
		return ref
	}
	if kind == kindAbsent {
		return &os.PathError{Op: "open", Path: handoffRel(base), Err: os.ErrNotExist}
	}
	return &os.PathError{Op: "open", Path: handoffRel(base), Err: openErr}
}

// refusalForLeaf builds the RefusedHandoffError for an unsafe leaf kind, or nil
// for a safe (regular/absent) leaf. inspectCause is used for kindInspectErr.
func refusalForLeaf(base string, k leafKind, inspectCause error) *RefusedHandoffError {
	switch k {
	case kindRegular, kindAbsent:
		return nil
	case kindInspectErr:
		return &RefusedHandoffError{
			Rel:    handoffRel(base),
			Reason: "could not be inspected: " + pathsafe.SanitizeControlChars(errString(inspectCause)),
		}
	default:
		return &RefusedHandoffError{Rel: handoffRel(base), Reason: reasonForKind(k)}
	}
}

func errString(err error) string {
	if err == nil {
		return "unknown error"
	}
	return err.Error()
}

// stagedSuffixBytes is the size of the random part of a staged name, which is written
// as twice as many lowercase hex characters.
const stagedSuffixBytes = 8

// stagedName returns a unique staged-file name of the approved form
// "<handoff filename>.tmp-<random suffix>" (BUG-209 section 9). It never matches
// *.handoff.yaml, so no reader treats it as a handoff, and reset/delete can
// associate it with its handoff through isStagedNameFor.
func stagedName(base string) (string, error) {
	buf := make([]byte, stagedSuffixBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating staged file name: %w", err)
	}
	return base + ".tmp-" + hex.EncodeToString(buf), nil
}

// isStagedNameFor reports whether name is exactly the form stagedName generates for
// base. Cleanup removes only these, so a file that merely shares the prefix survives
// (section 9, CODEX-008).
func isStagedNameFor(base, name string) bool {
	prefix := base + ".tmp-"
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	suffix := name[len(prefix):]
	if len(suffix) != 2*stagedSuffixBytes {
		return false
	}
	for _, c := range suffix {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// HandoffState is a handoff's identity at one moment. A recovery captures it before the
// read or write that may fail, so that a later ReconstructIfUnchanged can prove the file
// it replaces is the one that failed, not a newer writer's (section 9).
type HandoffState struct {
	valid   bool
	present bool
	id      leafID
}

// CaptureHandoffState captures the state of the handoff at path, without reading it, so
// an unreadable file can still be captured. A missing file is a valid state. An unsafe
// leaf or results directory is refused.
func CaptureHandoffState(path string) (HandoffState, error) {
	base := filepath.Base(path)
	root, absent, err := openResultsDirForPath(path)
	if err != nil {
		return HandoffState{}, err
	}
	if absent {
		return HandoffState{valid: true}, nil
	}
	defer root.Close()
	kind, info, cause := classifyLeaf(root, base)
	if kind == kindAbsent {
		return HandoffState{valid: true}, nil
	}
	if ref := refusalForLeaf(base, kind, cause); ref != nil {
		return HandoffState{}, ref
	}
	id, idErr := identityFromLeaf(root, base, info)
	if idErr != nil {
		return HandoffState{}, refusalForLeaf(base, kindInspectErr, idErr)
	}
	return HandoffState{valid: true, present: true, id: id}, nil
}

// opKind selects the replacement semantics.
type opKind int

const (
	opCreate opKind = iota
	opUpdate
	opReconstruct
)

// readThroughRoot reads base through the verified directory, using one no-follow open
// for the inspection and the read. An unsafe leaf is refused. An absent leaf returns a
// not-exist error; a mode-000 regular file returns the permission error (so BUG-191
// recovery still runs).
func readThroughRoot(b *boundDir, base string) ([]byte, error) {
	lr, err := readLeaf(b, base)
	if err != nil {
		return nil, err
	}
	return lr.data, nil
}

// ensureResultsDirCreated creates the directory that will hold path. It is only
// called after openResultsDirForPath has reported the directory absent, which for
// the canonical layout proves containment at that moment. The caller then opens and
// verifies the created directory before using it (section 4). The instant between
// the check and the creation stays open and is accepted for v0.4.1.
func ensureResultsDirCreated(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating results directory: %w", err)
	}
	return nil
}

// writeHandoff performs op's safe, atomic, durable, metadata-preserving write of
// the handoff named by path within the verified results directory. For opUpdate,
// mergeFn receives the current on-disk bytes (read through the same no-follow
// handle used to inspect the leaf) and returns the bytes to publish. For
// opCreate and opReconstruct the file is never read; data is published directly.
//
// Every refusal (RefusedHandoffError / RefusedDirError) and every failure before
// publication leaves the destination exactly as it was.
func writeHandoff(path string, op opKind, data []byte, mergeFn func(existing []byte) ([]byte, error), prior *HandoffState) error {
	base := filepath.Base(path)

	root, absent, err := openResultsDirForPath(path)
	if err != nil {
		return err
	}
	if absent {
		if op == opUpdate {
			return fmt.Errorf("reading result contract for update: %w",
				&os.PathError{Op: "open", Path: handoffRel(base), Err: os.ErrNotExist})
		}
		if cErr := ensureResultsDirCreated(path); cErr != nil {
			return cErr
		}
		raceHook("dir-created")
		root, absent, err = openResultsDirForPath(path)
		if err != nil {
			return err
		}
		if absent {
			return fmt.Errorf("creating results directory: directory still absent after creation")
		}
	}
	defer root.Close()

	k, _, cause := classifyLeaf(root, base)
	present := k == kindRegular
	var expect *leafID

	switch op {
	case opCreate:
		if present {
			return &RefusedHandoffError{Rel: handoffRel(base), Reason: reasonAlreadyExists}
		}
		if ref := refusalForLeaf(base, k, cause); ref != nil {
			return ref
		}
	case opUpdate:
		if ref := refusalForLeaf(base, k, cause); ref != nil {
			return ref
		}
		if k == kindAbsent {
			return fmt.Errorf("reading result contract for update: %w",
				&os.PathError{Op: "open", Path: handoffRel(base), Err: os.ErrNotExist})
		}
		// The bytes merged and the file replaced must be the same file (section 1), so
		// the identity from this read goes on to publication. If another writer safely
		// replaced the file meanwhile, the whole read and merge runs again against the
		// new file, so concurrent updates stay last writer wins (section 6). An unsafe
		// leaf, an inspection failure, a containment change or malformed content is not
		// retried: each returns from here.
		for attempt := 0; ; attempt++ {
			read, rErr := readLeaf(root, base)
			if rErr != nil {
				if IsRefusal(rErr) {
					return rErr
				}
				return fmt.Errorf("reading result contract for update: %w", rErr)
			}
			merged, mErr := mergeFn(read.data)
			if mErr != nil {
				return mErr
			}
			raceHook("after-read")
			pErr := publishStaged(root, base, merged, true, false, &read.id)
			if !errors.Is(pErr, errLeafReplaced) {
				return pErr
			}
			if attempt >= maxUpdateRetries {
				return &ConcurrentUpdateError{Rel: handoffRel(base), Retries: maxUpdateRetries}
			}
		}
	case opReconstruct:
		if ref := refusalForLeaf(base, k, cause); ref != nil {
			return ref
		}
		if prior != nil {
			if !prior.valid || prior.present != present {
				return &RefusedHandoffError{Rel: handoffRel(base), Reason: reasonChanged}
			}
			if present {
				expect = &prior.id
			}
		}
	}

	err = publishStaged(root, base, data, present, op == opReconstruct, expect)
	if errors.Is(err, errLeafReplaced) {
		return &RefusedHandoffError{Rel: handoffRel(base), Reason: reasonChanged}
	}
	return err
}
