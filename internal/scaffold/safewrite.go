package scaffold

// BUG-213: containment-aware write helpers. Every production filesystem
// mutation in Init, DemoSeed and UpdateDemoGuidance routes through these so a
// symbolic link or Windows junction on a path component -- or on the
// destination leaf itself -- can never redirect a scaffold write outside the
// project. REV-165 established the containment rule for the asset installer;
// this generalises it to the direct writes that bypassed it. The AST guard in
// bug213_guard_test.go enforces that no covered function writes directly
// (Owner Decision 5).

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
)

// scaffoldLstat and scaffoldLinkCount are indirection points so the BUG-213
// inspection-error tests can drive both failure paths deterministically,
// independently of the BUG-208 .gitignore hooks (gitignoreLstat /
// gitignoreLinkCount).
var (
	scaffoldLstat     = os.Lstat
	scaffoldLinkCount = hardLinkCount
)

// classifyLeaf is the shared BUG-208 / BUG-213 leaf classifier: it inspects the
// final path component without following links (Lstat) and, for a regular file,
// reads its hard-link count. It returns an error only on an inspection failure
// (Lstat failing for a reason other than "not exist", or an unreadable
// hard-link count); callers must then make no filesystem change (fail closed).
// It never assumes a link count of one.
func classifyLeaf(path string, lstat func(string) (os.FileInfo, error), linkCount func(string, os.FileInfo) (uint64, error)) (gitignoreState, error) {
	info, err := lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return gitignoreState{kind: kindAbsent}, nil
		}
		return gitignoreState{}, err
	}
	mode := info.Mode()
	switch {
	case mode&os.ModeSymlink != 0:
		return gitignoreState{kind: kindSymlink, mode: mode}, nil
	case mode.IsDir():
		return gitignoreState{kind: kindDirectory, mode: mode}, nil
	case mode.IsRegular():
		n, cerr := linkCount(path, info)
		if cerr != nil {
			return gitignoreState{}, cerr
		}
		if n > 1 {
			return gitignoreState{kind: kindHardLinked, mode: mode}, nil
		}
		return gitignoreState{kind: kindRegular, mode: mode}, nil
	default:
		return gitignoreState{kind: kindSpecial, mode: mode}, nil
	}
}

// classifyScaffoldLeaf classifies a scaffold destination leaf via the BUG-213
// inspection hooks. Used only by the overwrite destinations, which need
// hard-link detection; create-only destinations use a simpler Lstat-only check
// (a hard link at a create-only destination is left alone, Owner Decision 3).
func classifyScaffoldLeaf(path string) (gitignoreState, error) {
	return classifyLeaf(path, scaffoldLstat, scaffoldLinkCount)
}

// nonRegularRefusalWarning is the operator-facing text when a scaffold
// destination leaf is a symlink, directory or special file where a regular file
// was expected. Shared by the asset installer (REV-165) and the new create-only
// writer so the wording cannot drift.
func nonRegularRefusalWarning(destRel string, mode os.FileMode) string {
	return fmt.Sprintf("%s is not a regular file (%s) and was left untouched. "+
		"GTMS will not write through a symlink or replace a directory; "+
		"replace it with a regular file and re-run if you want the shipped version.",
		destRel, mode.String())
}

// containmentRefusalWarning is the operator-facing text when a scaffold
// destination resolves outside the project (a parent component is a symlink or
// junction pointing elsewhere, or the leaf is a dangling link below such a
// component). Shared by the asset installer and the new writers.
func containmentRefusalWarning(destRel string, cause error) string {
	return fmt.Sprintf("%s resolves outside the project and was left untouched (%s). "+
		"GTMS will not create or replace files beyond the project root; check "+
		"whether a parent directory is a symlink or junction pointing elsewhere.",
		destRel, pathsafe.SanitizeControlChars(cause.Error()))
}

// overwriteStateRefusalWarning is the operator-facing text when an overwrite
// destination (a VS Code companion snippet, or a guidance file) is a symlink,
// hard link, directory or special file. Unlike nonRegularRefusalWarning it
// names the exact state (including "hard link"), because a hard link IS a
// regular file and must not be described as "not a regular file".
func overwriteStateRefusalWarning(destRel string, state gitignoreState) string {
	return fmt.Sprintf("%s is a %s (%s) and was left untouched. "+
		"GTMS will not write through it to avoid changing a linked or shared file; "+
		"replace it with a regular file and re-run if you want GTMS to manage it.",
		destRel, state.stateWord(), state.mode.String())
}

// scaffoldWriteOutcome is the result of a create-only write attempt.
type scaffoldWriteOutcome int

const (
	writeCreated       scaffoldWriteOutcome = iota // a new file was written
	writeSkippedExists                             // a regular file already existed (left alone)
	writeRefused                                   // non-regular leaf or escaping destination; warning returned
)

// safeCreateOnly writes content to the destination derived from destRel (a
// forward-slash, project-relative path) only when the leaf is absent and the
// destination resolves inside the project. It never overwrites, never follows a
// leaf symlink, and never creates a dangling link's target. An existing regular
// file (even one that is hard-linked) is left alone -- create-only destinations
// never write to an existing file, so a hard link there needs no detection.
//
// The returned warning is non-empty only for writeRefused. An inspection or
// filesystem error (other than "does not exist") is returned as err so the
// caller can fail closed (Owner Decision 1).
func safeCreateOnly(projectRoot, destRel string, content []byte, perm os.FileMode) (scaffoldWriteOutcome, string, error) {
	lexPath := filepath.Clean(filepath.Join(projectRoot, filepath.FromSlash(destRel)))
	info, statErr := scaffoldLstat(lexPath)
	if statErr != nil && !os.IsNotExist(statErr) {
		return 0, "", fmt.Errorf("inspecting %s: %w", destRel, statErr)
	}
	leafPresent := statErr == nil

	// A leaf that is itself a symlink, directory or special file is refused as
	// non-regular, whatever it points at (tc-76224e75). This is decided on the
	// unresolved leaf (Lstat, no follow) BEFORE the containment check, so a leaf
	// symlink's own target is never resolved -- a dangling or outside leaf
	// symlink must still carry the "not a regular file" warning, not a
	// containment warning.
	if leafPresent && !info.Mode().IsRegular() {
		return writeRefused, nonRegularRefusalWarning(destRel, info.Mode()), nil
	}

	// Regular or absent leaf: resolve containment on the destination now, not
	// only when the leaf is absent (REV-252 CLAUDE-001/CODEX-001). Lstat follows
	// the parent components, so an existing regular file reached through a parent
	// folder linked outside the project would otherwise be mis-reported as an
	// ordinary skip. This mirrors applyUpgradeableAssetForParent, which resolves
	// containment whether or not the leaf exists. A directory link whose target
	// stays inside the project still resolves OK and is accepted (tc-4e211520).
	absPath, _, resolveErr := pathsafe.ResolveUnderRoot(projectRoot, destRel)
	if resolveErr != nil {
		return writeRefused, containmentRefusalWarning(destRel, resolveErr), nil
	}

	if leafPresent {
		// Contained existing regular file -- create-only leaves it alone.
		return writeSkippedExists, "", nil
	}
	if mkErr := os.MkdirAll(filepath.Dir(absPath), 0o755); mkErr != nil {
		return 0, "", fmt.Errorf("creating directory for %s: %w", destRel, mkErr)
	}
	if wErr := os.WriteFile(absPath, content, perm); wErr != nil {
		return 0, "", fmt.Errorf("writing %s: %w", destRel, wErr)
	}
	return writeCreated, "", nil
}

// guardedCreateFile routes a create-only write through safeCreateOnly and maps
// the outcome onto result: a written file is reported created, an existing file
// is reported skipped, and a refused destination is reported skipped with its
// containment / non-regular warning. An inspection or filesystem error is
// returned so the caller fails closed.
func guardedCreateFile(projectRoot, destRel string, content []byte, perm os.FileMode, result *Result) error {
	outcome, warning, err := safeCreateOnly(projectRoot, destRel, content, perm)
	if err != nil {
		return err
	}
	switch outcome {
	case writeCreated:
		result.FilesCreated = append(result.FilesCreated, destRel)
	case writeSkippedExists:
		result.FilesSkipped = append(result.FilesSkipped, destRel)
	case writeRefused:
		result.FilesSkipped = append(result.FilesSkipped, destRel)
		result.FilesRefused = append(result.FilesRefused, destRel)
		result.Warnings = append(result.Warnings, warning)
	}
	return nil
}

// safeMkdirContained creates the directory destRel only when it resolves inside
// the project. resolveErr (non-nil) means the directory resolves outside (a
// parent component is a symlink or junction pointing elsewhere); the caller
// skips it and warns. fatalErr (non-nil) is an unexpected MkdirAll failure.
func safeMkdirContained(projectRoot, destRel string) (resolveErr, fatalErr error) {
	resolved, _, rErr := pathsafe.ResolveUnderRoot(projectRoot, destRel)
	if rErr != nil {
		return rErr, nil
	}
	if mkErr := os.MkdirAll(resolved, 0o755); mkErr != nil {
		return nil, fmt.Errorf("creating directory %s: %w", destRel, mkErr)
	}
	return nil, nil
}

// overwriteOutcomeKind classifies an overwrite write attempt.
type overwriteOutcomeKind int

const (
	overwriteWrote        overwriteOutcomeKind = iota // the file was written in place
	overwriteRefusedState                             // symlink / hard link / directory / special leaf
	overwriteRefusedEscape                            // the destination resolves outside the project
)

// overwriteResult carries the outcome of safeOverwrite plus the detail needed
// to build a warning (the leaf state for a refused-state outcome, the resolve
// error for a refused-escape outcome).
type overwriteResult struct {
	outcome overwriteOutcomeKind
	state   gitignoreState
	cause   error
}

// safeOverwrite writes content to an overwrite destination (a file GTMS rewrites
// on every run) only when the leaf is absent or a single-link regular file and
// the destination resolves inside the project. A symlink, hard link, directory
// or special file is refused without writing, preserving a shared or external
// file's identity and link count (Owner Decision 3 -- GTMS does not detach a
// hard link by atomically replacing it). An inspection or filesystem error is
// returned as err so the caller fails closed.
func safeOverwrite(projectRoot, destRel string, content []byte, perm os.FileMode) (overwriteResult, error) {
	lexPath := filepath.Clean(filepath.Join(projectRoot, filepath.FromSlash(destRel)))
	state, err := classifyScaffoldLeaf(lexPath)
	if err != nil {
		return overwriteResult{}, fmt.Errorf("inspecting %s: %w", destRel, err)
	}
	switch state.kind {
	case kindSymlink, kindDirectory, kindSpecial, kindHardLinked:
		return overwriteResult{outcome: overwriteRefusedState, state: state}, nil
	}
	// Absent or single-link regular file -- resolve containment before writing.
	absPath, _, resolveErr := pathsafe.ResolveUnderRoot(projectRoot, destRel)
	if resolveErr != nil {
		return overwriteResult{outcome: overwriteRefusedEscape, cause: resolveErr}, nil
	}
	if mkErr := os.MkdirAll(filepath.Dir(absPath), 0o755); mkErr != nil {
		return overwriteResult{}, fmt.Errorf("creating directory for %s: %w", destRel, mkErr)
	}
	if wErr := os.WriteFile(absPath, content, perm); wErr != nil {
		return overwriteResult{}, fmt.Errorf("writing %s: %w", destRel, wErr)
	}
	return overwriteResult{outcome: overwriteWrote}, nil
}

// initVscodeFile decides how init handles a .vscode/settings.json or
// extensions.json destination, classifying the leaf with the shared
// Lstat/containment-aware classifier BEFORE choosing a branch (REV-252
// CODEX-002). The previous os.Stat test followed a leaf symlink, so a live
// symlink was treated as an ordinary existing file and took the companion
// branch. Now:
//   - absent -> write the file itself (guardedCreateFile handles parent containment);
//   - a symlink, directory or special file -> refuse the destination as
//     non-regular (Owner Decision 3: every file-leaf symlink is refused), with
//     no companion written and no merge hint printed;
//   - a regular (or hard-linked) file that escapes the project through a linked
//     parent -> refuse the destination for containment (REV-254 OWNER-001), with
//     no companion and no merge hint;
//   - a contained regular (or hard-linked) file -> write the companion snippet.
// An inspection error is returned so init fails closed.
func initVscodeFile(projectRoot, existingRel, companionRel string, content []byte, mergeHint string, result *Result) error {
	absPath := filepath.Join(projectRoot, filepath.FromSlash(existingRel))
	state, err := classifyScaffoldLeaf(absPath)
	if err != nil {
		return fmt.Errorf("inspecting %s: %w", existingRel, err)
	}
	switch state.kind {
	case kindAbsent:
		return guardedCreateFile(projectRoot, existingRel, content, 0o644, result)
	case kindRegular, kindHardLinked:
		// REV-254 OWNER-001: a regular original reached through a parent folder
		// linked outside the project is a refused external destination, not an
		// ordinary existing-file skip. Check containment of the original before
		// touching the companion. On escape, refuse the original (no companion
		// write, no companion warning, no merge hint).
		if _, _, resolveErr := pathsafe.ResolveUnderRoot(projectRoot, existingRel); resolveErr != nil {
			result.FilesSkipped = append(result.FilesSkipped, existingRel)
			result.FilesRefused = append(result.FilesRefused, existingRel)
			result.Warnings = append(result.Warnings, containmentRefusalWarning(existingRel, resolveErr))
			return nil
		}
		return writeVscodeCompanion(projectRoot, companionRel, content, existingRel, mergeHint, result)
	default:
		// Symlink, directory or special file: refuse as non-regular, no companion.
		result.FilesSkipped = append(result.FilesSkipped, existingRel)
		result.FilesRefused = append(result.FilesRefused, existingRel)
		result.Warnings = append(result.Warnings, nonRegularRefusalWarning(existingRel, state.mode))
		return nil
	}
}

// writeVscodeCompanion handles the existing-settings/extensions.json case: the
// user's real file is reported skipped, and the GTMS snippet is written to its
// companion path -- an overwrite destination GTMS rewrites on every run. BUG-213
// Owner Decision 3: if the companion is a symlink, hard link, directory or
// special file, or resolves outside the project, it is refused without writing,
// and the normal "merge the snippet" hint is suppressed because no usable
// companion was written (that instruction would be misleading). Exactly one
// containment warning names the refused companion.
func writeVscodeCompanion(projectRoot, companionRel string, content []byte, existingRel, mergeHint string, result *Result) error {
	result.FilesSkipped = append(result.FilesSkipped, existingRel)
	res, err := safeOverwrite(projectRoot, companionRel, content, 0o644)
	if err != nil {
		return err
	}
	switch res.outcome {
	case overwriteWrote:
		result.FilesCreated = append(result.FilesCreated, companionRel)
		result.Warnings = append(result.Warnings, mergeHint)
	case overwriteRefusedState:
		result.Warnings = append(result.Warnings, overwriteStateRefusalWarning(companionRel, res.state))
	case overwriteRefusedEscape:
		result.Warnings = append(result.Warnings, containmentRefusalWarning(companionRel, res.cause))
	}
	return nil
}
