//go:build !windows

package result

// BUG-209 Unix half of the shared safe-handoff implementation: metadata capture
// and application (permission bits + uid/gid), atomic rename, directory durability
// and the hard-link count. Every operation here is relative to the verified
// directory handle (section 4), so a directory swapped after binding cannot redirect
// it.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// dirRechecks is false here: handle-relative operations cannot be redirected by a
// later swap of the path, so nothing needs re-verifying before each use.
const dirRechecks = false

// leafID identifies a leaf between inspection and publication.
type leafID = unixIdentity

// publishStagedHook lets Go failure-injection tests drive a failure at a named
// boundary ("write", "meta", "sync", "rename"), inspecting the bytes being
// published so a test can target one specific write (e.g. the ENH-188 recovery
// write, whose data carries "status: error"). nil in production. Installed via
// InjectPublishFailureForTest.
var publishStagedHook func(stage string, data []byte) error

func injectFailure(stage string, data []byte) error {
	if publishStagedHook != nil {
		return publishStagedHook(stage, data)
	}
	return nil
}

// retryInspect runs op once. Only Windows has transient sharing errors to retry.
func retryInspect(op func() error) error { return op() }

// classifyLinkCount returns the hard-link count of a regular file. On Unix the
// count is the Nlink field of the stat_t backing info (no second open needed), so
// a mode-000 regular file is still classified without a permission error. A count
// that cannot be read is an inspection failure -- never assume one.
func classifyLinkCount(_ *boundDir, _ string, info os.FileInfo) (uint64, error) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("unexpected FileInfo backing %T for link count", info.Sys())
	}
	return uint64(st.Nlink), nil
}

// unixIdentity identifies the leaf between inspection and publication. Device and
// inode alone are not enough: a filesystem can hand a freed inode straight to a
// file recreated at the same path, as ext4 does, so a swap by remove-and-recreate
// would look unchanged. Size and modification time catch that case.
type unixIdentity struct {
	dev     uint64
	ino     uint64
	size    int64
	mtimeNs int64
}

func identityOf(info os.FileInfo) (unixIdentity, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return unixIdentity{}, false
	}
	return unixIdentity{dev: uint64(st.Dev), ino: st.Ino, size: info.Size(), mtimeNs: info.ModTime().UnixNano()}, true
}

// readLeaf opens base once, relative to the verified directory, without following a
// link and without blocking on a FIFO. It then inspects and reads that same open
// file, so the bytes, the type, the link count and the identity all describe one
// object (sections 1 and 8, CLAUDE-005, CODEX-002).
func readLeaf(b *boundDir, base string) (leafRead, error) {
	raceHook("leaf-open")
	var (
		fd  int
		err error
	)
	for {
		fd, err = unix.Openat(int(b.dirFile.Fd()), base, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		if !errors.Is(err, unix.EINTR) {
			break
		}
	}
	if err != nil {
		return leafRead{}, leafOpenError(b, base, err)
	}
	f := os.NewFile(uintptr(fd), base)
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return leafRead{}, refusalForLeaf(base, kindInspectErr, err)
	}
	kind := classifyMode(info.Mode())
	if kind == kindRegular {
		n, cerr := classifyLinkCount(b, base, info)
		if cerr != nil {
			return leafRead{}, refusalForLeaf(base, kindInspectErr, cerr)
		}
		if n > 1 {
			kind = kindHardLinked
		}
	}
	if ref := refusalForLeaf(base, kind, nil); ref != nil {
		return leafRead{}, ref
	}
	id, ok := identityOf(info)
	if !ok {
		return leafRead{}, refusalForLeaf(base, kindInspectErr, fmt.Errorf("unexpected FileInfo backing %T", info.Sys()))
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return leafRead{}, err
	}
	return leafRead{data: data, id: id, mtime: info.ModTime().UnixNano()}, nil
}

// identityFromLeaf returns the identity of a leaf classifyLeaf has just inspected.
func identityFromLeaf(_ *boundDir, _ string, info os.FileInfo) (leafID, error) {
	id, ok := identityOf(info)
	if !ok {
		return leafID{}, fmt.Errorf("unexpected FileInfo backing %T", info.Sys())
	}
	return id, nil
}

// metadataRefusal is the refusal for a guaranteed item that could not be applied. It
// matches Windows, so the same failure routes the same way on both (section 5).
func metadataRefusal(base, what string, cause error) *RefusedHandoffError {
	return refusalForLeaf(base, kindInspectErr, fmt.Errorf("%s failed: %w", what, cause))
}

// recheckDestination re-inspects the destination immediately before publishing.
// A leaf swapped since it was captured is refused. An absent destination must
// still be absent. The accepted residual (section 4) is a leaf replaced in the final
// instant between this check and the rename: it is itself replaced, but publication
// is relative to the verified directory handle, so nothing escapes.
func recheckDestination(b *boundDir, base string, present bool, captured leafID, data []byte) error {
	// The "recheck" injection seam lets a test swap the leaf in its hook just before
	// this check, so the comparison below detects it.
	_ = injectFailure("recheck", data)
	kind, info, cause := classifyLeaf(b, base)
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
	id, ok := identityOf(info)
	if !ok {
		return refusalForLeaf(base, kindInspectErr, fmt.Errorf("unexpected FileInfo backing %T", info.Sys()))
	}
	if id != captured {
		return errLeafReplaced
	}
	return nil
}

// stagedNeedsChown reports whether the staged file's owner differs from the captured
// owner. It is compared against the staged file, not the process: a new file's group
// comes from its directory on macOS and BSD, and in a setgid directory on Linux.
func stagedNeedsChown(stagedUID, stagedGID, capturedUID, capturedGID int) bool {
	return stagedUID != capturedUID || stagedGID != capturedGID
}

// publishStaged writes data to base within the verified directory atomically,
// durably and with metadata preserved, leaving the destination unchanged on any
// failure before the rename.
//
//   - present=false: a new handoff. The staged file is created with mode 0644 so
//     the kernel's umask filters it (REV-164); no chmod widens it afterwards.
//   - present=true: an existing single-link regular file. The staged file starts
//     owner-only (0600) so it is never wider than the final file at any instant,
//     then the captured permission bits and uid/gid are applied before the rename.
//   - bumpZeroPerm (Reconstruct): a captured mode of 000 is replaced by 0600 so a
//     recovered handoff is readable by its owner; every other mode is kept.
//   - expect, when set, is the identity of the file the data was merged from, so a
//     file swapped since that read is refused rather than replaced.
func publishStaged(b *boundDir, base string, data []byte, present, bumpZeroPerm bool, expect *leafID) error {
	var (
		capturedPerm os.FileMode
		capturedUID  int
		capturedGID  int
		capturedID   leafID
	)
	if present {
		kind, info, cause := classifyLeaf(b, base)
		if kind == kindAbsent {
			return &RefusedHandoffError{Rel: handoffRel(base), Reason: reasonChanged}
		}
		if ref := refusalForLeaf(base, kind, cause); ref != nil {
			return ref
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return refusalForLeaf(base, kindInspectErr, fmt.Errorf("unexpected FileInfo backing %T", info.Sys()))
		}
		capturedPerm = info.Mode().Perm()
		capturedUID = int(st.Uid)
		capturedGID = int(st.Gid)
		capturedID, ok = identityOf(info)
		if !ok {
			return refusalForLeaf(base, kindInspectErr, fmt.Errorf("unexpected FileInfo backing %T", info.Sys()))
		}
		if expect != nil && capturedID != *expect {
			return errLeafReplaced
		}
	}

	createMode := os.FileMode(0o644)
	if present {
		createMode = 0o600
	}

	staged, err := createStaged(b, base, createMode)
	if err != nil {
		return err
	}
	stagedPath := staged.name
	cleanup := func() {
		staged.file.Close()
		_ = b.Remove(stagedPath)
	}

	if hookErr := injectFailure("write", data); hookErr != nil {
		cleanup()
		return fmt.Errorf("writing staged handoff: %w", hookErr)
	}
	if _, err := staged.file.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("writing staged handoff: %w", err)
	}

	// BUG-209 section 5 (owner decision, 2026-11-06): POSIX and macOS ACLs and xattrs
	// are neither preserved nor stripped in v0.4.1. An ACL on the old file is lost,
	// and an ACL the new file inherits from its directory is left in place. ENH-234
	// tracks this. Only the permission bits and uid/gid below are guaranteed.

	if present {
		finalPerm := capturedPerm
		if bumpZeroPerm && finalPerm == 0 {
			finalPerm = 0o600
		}
		if err := injectFailure("meta", data); err != nil {
			cleanup()
			return fmt.Errorf("applying handoff permissions: %w", err)
		}
		if err := staged.file.Chmod(finalPerm); err != nil {
			cleanup()
			return metadataRefusal(base, "applying handoff permissions", err)
		}
		// uid/gid are guaranteed: if they differ from the staged file and cannot be
		// applied, refuse rather than change the owner. The "chown" injection seam
		// lets a test exercise the refusal without a second user (there is none in
		// CI).
		ownership := fmt.Sprintf("preserving handoff ownership (uid %d gid %d)", capturedUID, capturedGID)
		if cerr := injectFailure("chown", data); cerr != nil {
			cleanup()
			return metadataRefusal(base, ownership, cerr)
		}
		stagedInfo, err := staged.file.Stat()
		if err != nil {
			cleanup()
			return metadataRefusal(base, "inspecting staged handoff ownership", err)
		}
		stagedStat, ok := stagedInfo.Sys().(*syscall.Stat_t)
		if !ok {
			cleanup()
			return metadataRefusal(base, "inspecting staged handoff ownership", fmt.Errorf("unexpected FileInfo backing %T", stagedInfo.Sys()))
		}
		if stagedNeedsChown(int(stagedStat.Uid), int(stagedStat.Gid), capturedUID, capturedGID) {
			if err := staged.file.Chown(capturedUID, capturedGID); err != nil {
				cleanup()
				return metadataRefusal(base, ownership, err)
			}
			// A chown can clear permission bits on some systems, so apply them again.
			if err := staged.file.Chmod(finalPerm); err != nil {
				cleanup()
				return metadataRefusal(base, "applying handoff permissions", err)
			}
		}
	}

	if err := injectFailure("sync", data); err != nil {
		cleanup()
		return fmt.Errorf("flushing staged handoff: %w", err)
	}
	if err := staged.file.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("flushing staged handoff: %w", err)
	}
	if err := staged.file.Close(); err != nil {
		_ = b.Remove(stagedPath)
		return fmt.Errorf("closing staged handoff: %w", err)
	}

	if err := recheckDestination(b, base, present, capturedID, data); err != nil {
		_ = b.Remove(stagedPath)
		return err
	}

	if err := injectFailure("rename", data); err != nil {
		_ = b.Remove(stagedPath)
		return fmt.Errorf("publishing handoff: %w", err)
	}
	if err := b.Rename(stagedPath, base); err != nil {
		_ = b.Remove(stagedPath)
		return fmt.Errorf("publishing handoff: %w", err)
	}

	// Durability: sync the verified directory so the rename survives a crash (section
	// 7). It syncs the descriptor held since binding rather than reopening a path. A
	// failure here is AFTER publication -- the new file is already in place -- so it
	// is surfaced as an error rather than ignored (section 7 / AC3).
	if err := injectFailure("dirsync", data); err != nil {
		return fmt.Errorf("syncing results directory after publishing handoff: %w", err)
	}
	if err := b.dirFile.Sync(); err != nil {
		return fmt.Errorf("syncing results directory after publishing handoff: %w", err)
	}
	return nil
}

type stagedFile struct {
	file *os.File
	name string
}

// createStaged creates a uniquely named staged file within the verified directory,
// retrying the random suffix on the rare collision. O_EXCL keeps the collision
// safety.
func createStaged(b *boundDir, base string, mode os.FileMode) (stagedFile, error) {
	for attempt := 0; attempt < 10000; attempt++ {
		name, err := stagedName(base)
		if err != nil {
			return stagedFile{}, err
		}
		f, err := b.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, mode)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return stagedFile{}, fmt.Errorf("creating staged handoff: %w", err)
		}
		return stagedFile{file: f, name: name}, nil
	}
	return stagedFile{}, fmt.Errorf("exhausted attempts to create a unique staged handoff")
}
