//go:build windows

package result

// BUG-209 Windows half of the shared safe-handoff implementation: DACL (with its
// protected state), owner, group and the read-only attribute are preserved across
// a replacement; the staged file never inherits a wider directory ACL even
// briefly (it is created with a protected owner-only DACL); and the final rename
// is retried for up to 2 seconds on the sharing/lock/access-denied errors a
// reader-held or read-only destination produces.
//
// Section 4 (owner decision, 2026-11-07): these calls use paths, so the verified
// directory's identity and containment are rechecked immediately before opening an
// enumerated entry, before publication and before deletion, and any change is
// refused. The last instant after that final check stays open and is accepted for
// v0.4.1. ENH-235 tracks a full handle-relative redesign.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// dirRechecks is true here: path-based calls must re-verify the directory before use.
const dirRechecks = true

// leafID identifies a leaf between inspection and publication.
type leafID = winID

// publishStagedHook mirrors the Unix failure-injection seam so the cross-platform
// Go tests can drive a boundary failure, inspecting the published bytes. nil in
// production. Installed via InjectPublishFailureForTest.
var publishStagedHook func(stage string, data []byte) error

func injectFailure(stage string, data []byte) error {
	if publishStagedHook != nil {
		return publishStagedHook(stage, data)
	}
	return nil
}

// noFollowInspectFlags opens the object itself, not what a reparse point names, and
// lets a directory open so that it can be classified.
const noFollowInspectFlags = windows.FILE_FLAG_OPEN_REPARSE_POINT | windows.FILE_FLAG_BACKUP_SEMANTICS

const shareAll = windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE

// retryInspect runs op, retrying for a short bound on the transient errors a
// concurrent replacement causes, so a reader sees the old or new document rather
// than a false failure (CLAUDE-011, AC4). The bound is short because a file whose ACL
// really does deny access would otherwise wait the whole time on every scan.
func retryInspect(op func() error) error {
	const bound = 250 * time.Millisecond
	delay := 2 * time.Millisecond
	deadline := time.Now().Add(bound)
	for {
		err := op()
		if err == nil || !isRetryableReplace(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(delay)
		if delay *= 2; delay > 50*time.Millisecond {
			delay = 50 * time.Millisecond
		}
	}
}

// classifyLinkCount returns the hard-link count of a regular file. Windows has no
// Nlink in the FileInfo, so the object itself is opened for metadata only (no access
// rights, share everything, no reparse following) and GetFileInformationByHandle is
// queried. An unreadable count is an inspection failure -- never assume one.
func classifyLinkCount(b *boundDir, base string, _ os.FileInfo) (uint64, error) {
	info, err := byHandleInfo(filepath.Join(b.Name(), base))
	if err != nil {
		return 0, err
	}
	return uint64(info.NumberOfLinks), nil
}

func byHandleInfo(path string) (windows.ByHandleFileInformation, error) {
	var zero windows.ByHandleFileInformation
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return zero, err
	}
	var info windows.ByHandleFileInformation
	err = retryInspect(func() error {
		h, oerr := windows.CreateFile(p, 0, shareAll, nil, windows.OPEN_EXISTING, noFollowInspectFlags, 0)
		if oerr != nil {
			return oerr
		}
		defer windows.CloseHandle(h)
		return windows.GetFileInformationByHandle(h, &info)
	})
	if err != nil {
		return zero, err
	}
	return info, nil
}

type winID struct {
	vol  uint32
	high uint32
	low  uint32
}

func idFromInfo(info windows.ByHandleFileInformation) winID {
	return winID{vol: info.VolumeSerialNumber, high: info.FileIndexHigh, low: info.FileIndexLow}
}

// kindFromHandleInfo classifies an object from its by-handle information. mode is
// its os.FileMode from the same handle, used to tell a link or junction from another
// reparse point such as a cloud-sync placeholder, which is an ordinary file.
func kindFromHandleInfo(info windows.ByHandleFileInformation, mode os.FileMode) leafKind {
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 && mode&(os.ModeSymlink|os.ModeIrregular) != 0 {
		return kindSymlink
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		return kindDirectory
	}
	if mode&os.ModeType != 0 && mode&os.ModeSymlink == 0 && mode&os.ModeIrregular == 0 {
		return kindSpecial
	}
	if info.NumberOfLinks > 1 {
		return kindHardLinked
	}
	return kindRegular
}

// readLeaf rechecks the verified directory, opens base once without following a
// reparse point, then inspects and reads that same handle, so the bytes, the type,
// the link count and the identity all describe one object (sections 1 and 8).
func readLeaf(b *boundDir, base string) (leafRead, error) {
	if err := b.recheck("dir-open-entry"); err != nil {
		return leafRead{}, err
	}
	raceHook("leaf-open")
	path := filepath.Join(b.Name(), base)
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return leafRead{}, refusalForLeaf(base, kindInspectErr, err)
	}
	var h windows.Handle
	oerr := retryInspect(func() error {
		var e error
		h, e = windows.CreateFile(p, windows.GENERIC_READ, shareAll, nil, windows.OPEN_EXISTING, noFollowInspectFlags, 0)
		return e
	})
	if oerr != nil {
		return leafRead{}, leafOpenError(b, base, oerr)
	}
	f := os.NewFile(uintptr(h), path)
	defer f.Close()

	var info windows.ByHandleFileInformation
	if ierr := windows.GetFileInformationByHandle(h, &info); ierr != nil {
		return leafRead{}, refusalForLeaf(base, kindInspectErr, ierr)
	}
	fi, serr := f.Stat()
	if serr != nil {
		return leafRead{}, refusalForLeaf(base, kindInspectErr, serr)
	}
	if ref := refusalForLeaf(base, kindFromHandleInfo(info, fi.Mode()), nil); ref != nil {
		return leafRead{}, ref
	}
	data, rerr := io.ReadAll(f)
	if rerr != nil {
		return leafRead{}, rerr
	}
	return leafRead{data: data, id: idFromInfo(info), mtime: fi.ModTime().UnixNano()}, nil
}

func isRetryableReplace(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
		errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}

// ownerOnlySecurityAttributes builds a SECURITY_ATTRIBUTES carrying a protected
// DACL that grants the current process user full control and inherits nothing, so
// the staged file is never wider than the final file at any moment.
func ownerOnlySecurityAttributes() (*windows.SecurityAttributes, error) {
	proc, err := windows.GetCurrentProcess()
	if err != nil {
		return nil, err
	}
	var token windows.Token
	if err := windows.OpenProcessToken(proc, windows.TOKEN_QUERY, &token); err != nil {
		return nil, err
	}
	defer token.Close()
	tu, err := token.GetTokenUser()
	if err != nil {
		return nil, err
	}
	sddl := "D:P(A;;FA;;;" + tu.User.Sid.String() + ")"
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return nil, err
	}
	sa := &windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(*sa))
	return sa, nil
}

func getFileReadonly(path string) (bool, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false, err
	}
	attrs, err := windows.GetFileAttributes(p)
	if err != nil {
		return false, err
	}
	return attrs&windows.FILE_ATTRIBUTE_READONLY != 0, nil
}

func setFileReadonly(path string, readonly bool) error {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attrs, err := windows.GetFileAttributes(p)
	if err != nil {
		return err
	}
	if readonly {
		attrs |= windows.FILE_ATTRIBUTE_READONLY
	} else {
		attrs &^= windows.FILE_ATTRIBUTE_READONLY
	}
	return windows.SetFileAttributes(p, attrs)
}

// capturedMeta is every guaranteed item of an existing handoff, all read from one
// handle on the object itself.
type capturedMeta struct {
	owner     *windows.SID
	group     *windows.SID
	dacl      *windows.ACL
	protected bool
	readonly  bool
	id        winID
}

// captureMeta reads the guaranteed metadata of destPath. Any failure to inspect a
// guaranteed item refuses the write (section 5, AC15, CLAUDE-010, CODEX-005). A NULL
// DACL that the file really has is preserved as it is: it is not a read failure.
func captureMeta(destPath, base string, data []byte) (capturedMeta, error) {
	var m capturedMeta
	refuse := func(cause error) (capturedMeta, error) {
		return m, refusalForLeaf(base, kindInspectErr, cause)
	}
	inject := func(stage string) error { return injectFailure("capture-"+stage, data) }

	p, err := windows.UTF16PtrFromString(destPath)
	if err != nil {
		return refuse(err)
	}
	if err := inject("open"); err != nil {
		return refuse(err)
	}
	var h windows.Handle
	err = retryInspect(func() error {
		var e error
		h, e = windows.CreateFile(p, windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES, shareAll, nil,
			windows.OPEN_EXISTING, noFollowInspectFlags, 0)
		return e
	})
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			return m, &RefusedHandoffError{Rel: handoffRel(base), Reason: reasonChanged}
		}
		return refuse(err)
	}
	f := os.NewFile(uintptr(h), destPath)
	defer f.Close()

	var info windows.ByHandleFileInformation
	if err := inject("info"); err != nil {
		return refuse(err)
	}
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return refuse(err)
	}
	fi, err := f.Stat()
	if err != nil {
		return refuse(err)
	}
	if ref := refusalForLeaf(base, kindFromHandleInfo(info, fi.Mode()), nil); ref != nil {
		return m, ref
	}
	m.id = idFromInfo(info)
	m.readonly = info.FileAttributes&windows.FILE_ATTRIBUTE_READONLY != 0

	if err := inject("sd"); err != nil {
		return refuse(err)
	}
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.GROUP_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return refuse(err)
	}
	if err := inject("control"); err != nil {
		return refuse(err)
	}
	ctrl, _, err := sd.Control()
	if err != nil {
		return refuse(err)
	}
	m.protected = ctrl&windows.SE_DACL_PROTECTED != 0
	if err := inject("owner"); err != nil {
		return refuse(err)
	}
	if m.owner, _, err = sd.Owner(); err != nil {
		return refuse(err)
	}
	if err := inject("group"); err != nil {
		return refuse(err)
	}
	if m.group, _, err = sd.Group(); err != nil {
		return refuse(err)
	}
	if err := inject("dacl"); err != nil {
		return refuse(err)
	}
	if m.dacl, _, err = sd.DACL(); err != nil {
		return refuse(err)
	}
	return m, nil
}

// identityFromLeaf returns the identity of a leaf classifyLeaf has just inspected.
func identityFromLeaf(b *boundDir, base string, _ os.FileInfo) (leafID, error) {
	if err := b.recheck("dir-capture"); err != nil {
		return leafID{}, err
	}
	info, err := byHandleInfo(filepath.Join(b.Name(), base))
	if err != nil {
		return leafID{}, err
	}
	return idFromInfo(info), nil
}

// recheckDestination re-verifies the directory and the destination immediately
// before a publication attempt. An existing leaf must still be the inspected one;
// an absent one must still be absent.
func recheckDestination(b *boundDir, base, destPath string, present bool, captured winID, data []byte) error {
	if err := b.recheck("dir-publish"); err != nil {
		return err
	}
	// The "recheck" injection seam lets a test swap the leaf in its hook just before
	// this check, so the comparison below detects it.
	_ = injectFailure("recheck", data)
	kind, _, cause := classifyLeaf(b, base)
	if !present {
		if kind != kindAbsent {
			return &RefusedHandoffError{Rel: handoffRel(base), Reason: reasonChanged}
		}
		return nil
	}
	if kind == kindAbsent {
		return &RefusedHandoffError{Rel: handoffRel(base), Reason: reasonChanged}
	}
	if ref := refusalForLeaf(base, kind, cause); ref != nil {
		return ref
	}
	info, err := byHandleInfo(destPath)
	if err != nil {
		return refusalForLeaf(base, kindInspectErr, err)
	}
	if idFromInfo(info) != captured {
		return errLeafReplaced
	}
	return nil
}

// removeStaged removes a staged file after rechecking the directory. A file path that
// no longer reaches the verified directory is not removed: it could name something
// else, and a leftover is ignored by readers (section 4, CODEX-001).
func removeStaged(b *boundDir, stagedPath string) {
	if err := b.recheck("dir-cleanup"); err != nil {
		return
	}
	_ = os.Remove(stagedPath)
}

// publishStaged implements the Windows replacement. See the Unix twin for the
// shared ordering contract; here the metadata is the DACL, owner, group and the
// read-only attribute, and the rename is bounded-retry.
func publishStaged(b *boundDir, base string, data []byte, present, _ bool, expect *leafID) error {
	if err := b.recheck("dir-publish"); err != nil {
		return err
	}
	dir := b.Name()
	destPath := filepath.Join(dir, base)

	var meta capturedMeta
	if present {
		var err error
		if meta, err = captureMeta(destPath, base, data); err != nil {
			return err
		}
		if expect != nil && meta.id != *expect {
			return errLeafReplaced
		}
	}

	// Create the staged file. For a replacement it is protected owner-only from
	// the first instant; for a new handoff it inherits the directory ACL (the
	// approved "inherited ACL" new-handoff behaviour).
	var sa *windows.SecurityAttributes
	if present {
		var saErr error
		if sa, saErr = ownerOnlySecurityAttributes(); saErr != nil {
			return fmt.Errorf("preparing staged handoff security: %w", saErr)
		}
	}

	stagedPath, handle, err := createStagedWindows(b, dir, base, sa)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(handle), stagedPath)
	cleanup := func() {
		f.Close()
		removeStaged(b, stagedPath)
	}

	if hookErr := injectFailure("write", data); hookErr != nil {
		cleanup()
		return fmt.Errorf("writing staged handoff: %w", hookErr)
	}
	if _, err := f.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("writing staged handoff: %w", err)
	}

	if present {
		si := windows.OWNER_SECURITY_INFORMATION | windows.GROUP_SECURITY_INFORMATION | windows.DACL_SECURITY_INFORMATION
		if meta.protected {
			si |= windows.PROTECTED_DACL_SECURITY_INFORMATION
		} else {
			si |= windows.UNPROTECTED_DACL_SECURITY_INFORMATION
		}
		if err := injectFailure("meta", data); err != nil {
			cleanup()
			return fmt.Errorf("applying handoff security: %w", err)
		}
		if err := windows.SetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT,
			windows.SECURITY_INFORMATION(si), meta.owner, meta.group, meta.dacl, nil); err != nil {
			// Exact owner/group/DACL are guaranteed; if they cannot be applied,
			// refuse rather than publish a file with different effective access.
			cleanup()
			return &RefusedHandoffError{Rel: handoffRel(base), Reason: "could not be inspected: preserving Windows security failed: " + err.Error()}
		}
	}

	if err := injectFailure("sync", data); err != nil {
		cleanup()
		return fmt.Errorf("flushing staged handoff: %w", err)
	}
	if err := f.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("flushing staged handoff: %w", err)
	}
	if err := f.Close(); err != nil {
		removeStaged(b, stagedPath)
		return fmt.Errorf("closing staged handoff: %w", err)
	}

	if present && meta.readonly {
		if err := b.recheck("dir-stage"); err != nil {
			return err
		}
		if err := setFileReadonly(stagedPath, true); err != nil {
			removeStaged(b, stagedPath)
			return fmt.Errorf("applying read-only attribute to staged handoff: %w", err)
		}
	}

	if err := injectFailure("rename", data); err != nil {
		removeStaged(b, stagedPath)
		return fmt.Errorf("publishing handoff: %w", err)
	}
	if err := replaceWithRetry(b, base, stagedPath, destPath, present, meta, data); err != nil {
		removeStaged(b, stagedPath)
		return err
	}
	return nil
}

// createStagedWindows creates a uniquely named staged file in dir with the given
// security attributes, retrying the random suffix on the rare collision. The directory
// is rechecked immediately before each create, so a junction swapped in after an
// earlier check cannot redirect the staged file (section 4, CODEX-001).
func createStagedWindows(b *boundDir, dir, base string, sa *windows.SecurityAttributes) (string, windows.Handle, error) {
	for attempt := 0; attempt < 10000; attempt++ {
		if err := b.recheck("dir-stage"); err != nil {
			return "", 0, err
		}
		name, err := stagedName(base)
		if err != nil {
			return "", 0, err
		}
		stagedPath := filepath.Join(dir, name)
		p, perr := windows.UTF16PtrFromString(stagedPath)
		if perr != nil {
			return "", 0, perr
		}
		// WRITE_DAC|WRITE_OWNER are required so SetSecurityInfo can apply the
		// captured owner, group and DACL to the handle; GENERIC_WRITE alone does
		// not grant them.
		h, cerr := windows.CreateFile(p,
			windows.GENERIC_READ|windows.GENERIC_WRITE|windows.WRITE_DAC|windows.WRITE_OWNER, 0, sa,
			windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if errors.Is(cerr, windows.ERROR_FILE_EXISTS) || errors.Is(cerr, windows.ERROR_ALREADY_EXISTS) {
			continue
		}
		if cerr != nil {
			return "", 0, fmt.Errorf("creating staged handoff: %w", cerr)
		}
		return stagedPath, h, nil
	}
	return "", 0, fmt.Errorf("exhausted attempts to create a unique staged handoff")
}

// replaceWithRetry renames src over dst, retrying for up to 2 seconds (10ms backoff
// doubling to a 200ms cap) on the access-denied, sharing-violation and lock-violation
// errors a reader-held destination produces.
//
// Every attempt first rechecks the directory and the destination, because the wait
// can last long enough for either to change. A read-only destination has the
// attribute cleared only around the move itself and restored straight after a failed
// attempt, so it stays read-only through the backoff, and a failure to restore it is
// returned rather than ignored (CLAUDE-013). A destination that was absent is moved
// without the replace flag, so a file that appears is never overwritten.
func replaceWithRetry(b *boundDir, base, src, dst string, present bool, meta capturedMeta, data []byte) error {
	srcPtr, err := windows.UTF16PtrFromString(src)
	if err != nil {
		return err
	}
	dstPtr, err := windows.UTF16PtrFromString(dst)
	if err != nil {
		return err
	}
	flags := uint32(windows.MOVEFILE_WRITE_THROUGH)
	if present {
		flags |= windows.MOVEFILE_REPLACE_EXISTING
	}
	const cap = 200 * time.Millisecond
	delay := 10 * time.Millisecond
	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := recheckDestination(b, base, dst, present, meta.id, data); err != nil {
			return err
		}

		var moveErr error
		cleared := false
		if present && meta.readonly {
			if clearErr := setFileReadonly(dst, false); clearErr != nil {
				moveErr = fmt.Errorf("clearing the read-only attribute: %w", clearErr)
			} else {
				cleared = true
			}
		}
		if moveErr == nil {
			moveErr = windows.MoveFileEx(srcPtr, dstPtr, flags)
			if moveErr == nil {
				return nil
			}
		}
		if !present && (errors.Is(moveErr, windows.ERROR_ALREADY_EXISTS) || errors.Is(moveErr, windows.ERROR_FILE_EXISTS)) {
			return &RefusedHandoffError{Rel: handoffRel(base), Reason: reasonChanged}
		}
		if cleared {
			if recheckErr := b.recheck("dir-cleanup"); recheckErr != nil {
				return recheckErr
			}
			if restoreErr := setFileReadonly(dst, true); restoreErr != nil {
				return fmt.Errorf("publishing handoff %s: %w; and restoring its read-only attribute failed: %v",
					handoffRel(base), moveErr, restoreErr)
			}
		}
		if !isRetryableReplace(moveErr) || time.Now().After(deadline) {
			return fmt.Errorf("publishing handoff %s: %w", handoffRel(base), moveErr)
		}
		time.Sleep(delay)
		if delay *= 2; delay > cap {
			delay = cap
		}
	}
}
