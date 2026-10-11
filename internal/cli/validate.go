package cli

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
	"github.com/aitestmanagement/gtms-cli/internal/target"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
)

// requireScopedSpec enforces the qualified-to-flat identity boundary for a
// folder-qualified single-TC target. It is the single guard every folder-aware
// command runs BEFORE reducing res.Target to the bare res.TCID for a flat
// wiring/task lookup or mutation, so strengthening it here protects every caller.
//
// For a QUALIFIED target (res.Target contains "/") it requires that the bare ID
// resolves to EXACTLY ONE project-wide spec, and that spec is under the requested
// folder:
//   - REV-113 CODEX-001: the spec must exist under the requested folder (else a
//     wrong-folder target would silently operate on the same-ID TC elsewhere).
//   - REV-120 CODEX-001: the bare ID must be UNIQUE project-wide. Cross-folder
//     duplicate IDs are not prevented today (tree-wide uniqueness ENH-100/ENH-101
//     are open), and the downstream mutation is flat/bare-ID -- so
//     `delete foo/tc-x` would also delete `bar/tc-x`. Abort BEFORE any mutation and
//     name every conflicting spec.
//
// Unqualified (bare) targets skip both checks -- the global/brownfield path is
// unchanged. Non-TC kinds are not applicable.
func requireScopedSpec(root string, res target.Result) error {
	if res.Kind != target.KindTCID {
		return nil
	}
	if !strings.Contains(res.Target, "/") {
		return nil // unqualified: global lookup, no scoping
	}

	// One snapshot serves both checks below (ADR-024 s2). They used to build two:
	// the since-deleted FindAllSpecs walked the tree, then Exists walked it again to ask a question
	// the same snapshot could already answer.
	snap, err := testcase.NewSnapshot(root)
	if err != nil {
		return err
	}
	return requireScopedSpecIn(snap, root, res)
}

// requireScopedSpecIn is requireScopedSpec against a snapshot the CALLER already
// built, for an operation that has other guards to run against the same one
// (REV-160 CLAUDE-003). Repoint uses it so its qualifier check, its ownership
// decision and its eventual write all answer against ONE enumeration: three
// independent snapshots let the filesystem change between preflight decisions,
// so the record written need not correspond to the state that authorised the
// command.
func requireScopedSpecIn(snap *testcase.Snapshot, root string, res target.Result) error {
	if res.Kind != target.KindTCID || !strings.Contains(res.Target, "/") {
		return nil
	}

	// Ambiguity guard: a qualified target must resolve to a UNIQUE project-wide
	// spec, or the flat bare-ID operation would affect same-ID specs in other
	// folders (REV-120 CODEX-001).
	r := snap.Resolve(res.TCID)
	switch r.Outcome {
	case testcase.OutcomeAmbiguous:
		// REV-123 CODEX-001: the conflicting paths are filesystem-derived (checked-out
		// directory names), so sanitize each for display before echoing -- a control
		// or ANSI-escape character in a path must not manipulate the terminal.
		display := make([]string, len(r.Owners))
		for i, m := range r.Owners {
			display[i] = pathsafe.SanitizeControlChars(m)
		}
		return fmt.Errorf("test case ID '%s' is ambiguous -- it exists in %d places:\n    %s\n    Resolve the duplicate IDs (or use a form that targets a single spec) before targeting '%s'.",
			res.TCID, len(r.Owners), strings.Join(display, "\n    "), res.Target)
	case testcase.OutcomeUnprovable:
		// Fail closed (the contract the deleted FindAllSpecs had): an unprovable candidate must
		// abort the qualified operation rather than be undercounted.
		return testcase.OwnershipErrorFor(r)
	}

	// Scoped existence: the (unique) spec must live under the requested folder.
	folder, _ := testcase.SplitQualifiedTarget(res.Target)
	if _, ferr := snap.RequireOwnerUnderFolder(root, folder, res.TCID); ferr == nil {
		return nil
	}
	return fmt.Errorf("test case '%s' not found in %s/", res.Target, layout.Current().TestCases)
}

// resolveTargetOwner is the SINGLE identity resolution for a single-TC mutating
// command (automate, execute, prime). It builds one snapshot, runs every guard
// against it, and returns the proven owner for the command to pass downstream
// (ADR-024 s5).
//
// It replaces the old three-call sequence -- requireScopedSpec, then
// refuseUnownedTarget, then testcase.Exists -- which resolved the same id three
// times and then threw the answer away, leaving every downstream consumer to
// resolve it again for itself. The guards are unchanged in order and wording;
// what changes is that the command now KEEPS the owner.
//
// Returns:
//   - (owner, "", nil) when ownership is uniquely proven;
//   - (nil, hint, err) for a refusal, where hint is what to print beside err;
//   - (nil, "", err) wrapping testcase.ErrOwnerNotFound when nothing owns the id.
//
// NotFound is returned rather than rendered because the three commands legitimately
// differ on it: automate and prime refuse with "not found", while execute proceeds
// so a missing spec still surfaces as its own wiring diagnostic. Deciding that here
// would silently change execute's behaviour.
//
// scopedHint is the caller's guidance for a folder-qualifier failure, which differs
// per command.
func resolveTargetOwner(root string, res target.Result, scopedHint string) (*testcase.Owner, string, error) {
	if res.Kind != target.KindTCID {
		return nil, "", fmt.Errorf("resolveTargetOwner requires a test case target, got %v", res.Kind)
	}

	snap, err := testcase.NewSnapshot(root)
	if err != nil {
		return nil, "", err
	}

	folder, _ := testcase.SplitQualifiedTarget(res.Target)

	// Stage 1 (qualified only): the bare id must be UNIQUE project-wide before a
	// qualified target is reduced to it for a flat mutation (REV-120 CODEX-001),
	// and the owner must live under the requested folder (REV-113 CODEX-001).
	// Wording is preserved verbatim -- frozen acceptance specs assert it.
	if folder != "" {
		r := snap.Resolve(res.TCID)
		switch r.Outcome {
		case testcase.OutcomeAmbiguous:
			// REV-123 CODEX-001: owner paths are filesystem-derived, so sanitize each
			// before echoing -- a control or ANSI-escape character in a path must not
			// manipulate the terminal.
			display := make([]string, len(r.Owners))
			for i, m := range r.Owners {
				display[i] = pathsafe.SanitizeControlChars(m)
			}
			return nil, scopedHint, fmt.Errorf("test case ID '%s' is ambiguous -- it exists in %d places:\n    %s\n    Resolve the duplicate IDs (or use a form that targets a single spec) before targeting '%s'.",
				res.TCID, len(r.Owners), strings.Join(display, "\n    "), res.Target)
		case testcase.OutcomeUnprovable:
			return nil, scopedHint, testcase.OwnershipErrorFor(r)
		}

		if _, ferr := snap.RequireOwnerUnderFolder(root, folder, res.TCID); ferr != nil {
			if errors.Is(ferr, testcase.ErrOwnerNotFound) {
				return nil, scopedHint, fmt.Errorf("test case '%s' not found in %s/", res.Target, layout.Current().TestCases)
			}
			return nil, scopedHint, ferr
		}
	}

	// Stage 2: ADR-024 ownership. Ambiguous (including a renamed second owner) or
	// Unprovable (including a shell-unsafe body id) refuses before any adapter is
	// invoked or state is written.
	owner, oerr := snap.RequireOwner(res.TCID)
	if oerr != nil {
		return nil, "", oerr
	}
	return owner, "", nil
}

// refuseUnownedTarget refuses a mutating operation whose target's ownership is
// not uniquely proven (ADR-024). It covers ALL target forms -- a single bare/
// qualified TC id, and a folder (bulk) scope, recursive or not -- so a mutating
// consumer (automate, execute, prime) fails closed before any adapter is invoked
// or state is written. A grammar-invalid body id resolves as Unprovable, so this
// also enforces the execution-bound grammar boundary (tc-b127e260). NotFound is
// left to each command's own existence wording. Returns a typed
// *testcase.OwnershipError so the caller can render it once on stderr, non-zero.
func refuseUnownedTarget(root string, res target.Result, recursive bool) error {
	snap, err := testcase.NewSnapshot(root)
	if err != nil {
		return err
	}
	switch res.Kind {
	case target.KindFolder:
		scopeRel, ok := folderScopeRel(root, res.Target)
		if !ok {
			return nil // a missing/out-of-tree folder is handled by IsBulkFolder upstream
		}
		return snap.RequireFolderOwnership(scopeRel, recursive)
	case target.KindTCID:
		return snap.RequireOwnership(res.TCID)
	default:
		return nil
	}
}

// resolveBulkOwners returns the body-identity owner ids physically under a folder
// scope, AFTER refusing the whole operation if any is Ambiguous/Unprovable. It is
// the single source of the bulk automate/execute work-set (ADR-024 C row of the
// contract matrix / REV trap #6): the iteration set MUST be the resolved body-id
// owners, not the filename-truncated ids from DiscoverTestCases -- otherwise an
// extended-id sibling (tc-x--legacy) is truncated to tc-x, silently skipped, and
// the wrong id invoked against an adapter, while the preflight (which validated
// the correct body-id set) passes and masks it. One snapshot backs both the
// refusal and the returned owners. A missing/out-of-tree folder returns a
// not-found error (mirroring DiscoverTestCases), handled by the caller.
func resolveBulkOwners(root, folder string, recursive bool) ([]*testcase.Owner, error) {
	snap, err := testcase.NewSnapshot(root)
	if err != nil {
		return nil, err
	}
	scopeRel, ok := folderScopeRel(root, folder)
	if !ok {
		return nil, fmt.Errorf("folder '%s/%s/' does not exist", layout.Current().TestCases, folder)
	}
	if err := snap.RequireFolderOwnership(scopeRel, recursive); err != nil {
		return nil, err
	}
	ids, _ := snap.SelectFolder(scopeRel, recursive)
	if len(ids) == 0 {
		// Mirror DiscoverTestCases: an empty owner set is a "No test cases found"
		// error, not a silent empty run (a caller printing "No default adapter"
		// before this would be a worse diagnosis).
		return nil, fmt.Errorf("No test cases found in %s/%s/", layout.Current().TestCases, folder)
	}

	// ADR-024 s5: return the resolved OWNERS, not the ids. RequireFolderOwnership
	// has already proved each one unique, so this re-reads the same snapshot rather
	// than re-resolving anything -- and the bulk loop then passes an owner per
	// iteration instead of handing each iteration a bare id to resolve again.
	owners := make([]*testcase.Owner, 0, len(ids))
	for _, id := range ids {
		owner, oerr := snap.RequireOwner(id)
		if oerr != nil {
			return nil, oerr
		}
		owners = append(owners, owner)
	}
	return owners, nil
}

// folderScopeRel resolves a cases-relative folder to its canonical
// project-relative form for snapshot scope selection, following a directory
// alias and applying host case-sensitivity via the OS.
func folderScopeRel(root, folder string) (string, bool) {
	abs, err := testcase.ResolveFolder(root, folder)
	if err != nil {
		return "", false
	}
	rel, rerr := filepath.Rel(pathsafe.CanonicalRoot(root), abs)
	if rerr != nil {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// invalidTargetFormatError builds the historical "Invalid test case ID format"
// compatibility response for a positional target that the shared classifier
// (target.Classify) rejected. The pre-BUG-167 commands overrode the raw
// validator error with this generic message, and ~15 frozen acceptance specs
// (bug-058-pathsafe-validation, tc-id-md-suffix-normalisation, etc.) assert the
// "Invalid test case ID format" substring. This keeps that substring while also
// surfacing the classifier's specific cause and the TC-format hint. Returns
// (message, hint) for output.Errorf; the hint carries its own 4-space indent for
// the second line since FprintError only indents the first.
func invalidTargetFormatError(rawArg string, cause error) (string, string) {
	msg := fmt.Sprintf("Invalid test case ID format: '%s'.", sanitizeForError(rawArg))
	hint := cause.Error() + "\n    Use format like tc-a1b2c3d4."
	return msg, hint
}

// The positional-target validators/normalisers below are thin delegations to the
// shared internal/target package (BUG-167). The authoritative implementations
// live there so every folder-aware command classifies and normalises a target
// identically. These unexported wrappers keep the existing cli call sites and
// white-box tests (validate_test.go, discover_test.go, gaps_test.go) unchanged.

// validateTargetID delegates to target.ValidateTargetID.
func validateTargetID(t string) error {
	return target.ValidateTargetID(t)
}

// validateFolderArg delegates to target.ValidateFolderArg.
func validateFolderArg(folder string) (string, error) {
	return target.ValidateFolderArg(folder)
}

// normaliseTarget delegates to target.NormaliseTarget.
func normaliseTarget(t string) string {
	return target.NormaliseTarget(t)
}

// filePathExtensions is the set of file extensions that suggest a value is a file path.
var filePathExtensions = map[string]bool{
	".md": true, ".txt": true, ".yaml": true, ".yml": true,
	".json": true, ".xml": true, ".csv": true, ".html": true,
	".go": true, ".py": true, ".js": true, ".ts": true,
}

// looksLikeFilePath returns true if value looks like it could be a file path.
// A value looks like a file path if it contains path separators (/ or \)
// or ends with a common file extension.
func looksLikeFilePath(value string) bool {
	if value == "" {
		return false
	}
	if strings.ContainsAny(value, "/\\") {
		return true
	}
	ext := strings.ToLower(filepath.Ext(value))
	return filePathExtensions[ext]
}

// sanitizeForError truncates and cleans a target string for safe inclusion in error messages.
// Prevents the error message itself from being an injection vector. Retained in cli
// (not delegated) because create.go and framework_hint.go call it directly.
func sanitizeForError(s string) string {
	runes := []rune(s)
	if len(runes) > 40 {
		s = string(runes[:40]) + "..."
	}
	// Replace control characters and common shell metacharacters for display
	var b strings.Builder
	for _, r := range s {
		if r < 32 || r == 127 {
			b.WriteRune('?')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// The control-character escaping rule formerly implemented here as
// sanitizeControlChars was promoted to pathsafe.SanitizeControlChars in BUG-187
// so the CLI ambiguity diagnostics and the triage storage-boundary diagnostics
// share one rule. Both call sites above now use pathsafe.SanitizeControlChars.
// sanitizeForError (a different, truncating helper) stays here.
