package link

import (
	"fmt"
	"path/filepath"

	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
)

// RepointAuthority is the ONE ownership decision that authorises a repoint,
// carried from the single snapshot the operation built through to the write.
//
// REV-160 CLAUDE-003. A qualified single-TC repoint used to run THREE independent
// project-wide enumerations for one operation -- requireScopedSpec in the Cobra
// wrapper, refuseUnownedTarget beside it, and RepointSingle's own NewSnapshot --
// and a folder or --all repoint ran two. Each answered against a DIFFERENT
// snapshot, so the ownership state that authorised the command was not
// necessarily the state the record was written under: the filesystem is free to
// change between them. ADR-024 s5 is "resolve once and pass the owner forward",
// and s5a adds "an operation performs at most one project-wide enumeration". A
// smaller enumeration COUNT would not have fixed the correctness half; carrying
// the decision does.
//
// The fields are unexported and there is no literal constructor, for the same
// reason testcase.Owner has none: an authority can only come from a resolution
// against a real snapshot. A caller cannot mint one from a string it happens to
// be holding, and RepointSingle / RepointBulk / RepointAll refuse without one --
// so the ownership preflight cannot be bypassed by a non-CLI caller either, which
// is the property REV-158 CODEX-005 put inside the mutating entry points.
type RepointAuthority struct {
	// single is true for a single-TC authority, false for a scope authority.
	single bool

	// --- single-TC ---
	id     string
	owner  *testcase.Owner
	orphan bool

	// --- scope ---
	scopeFolder string
	recursive   bool
	ownerIDs    map[string]bool
	knownIDs    []string
}

// ID returns the bare TC id a single-TC authority proves a decision for.
func (a *RepointAuthority) ID() string { return a.id }

// Owner returns the proven physical owner, or nil for a permitted orphan.
func (a *RepointAuthority) Owner() *testcase.Owner { return a.owner }

// IsOrphan reports whether the authority is the explicitly permitted
// orphan-repair case: nothing owns the id, and repoint may still retarget the
// wiring record that declares it. See AuthoriseSingleRepoint for the policy and
// its three controls.
func (a *RepointAuthority) IsOrphan() bool { return a.orphan }

// AuthoriseSingleRepoint makes the single ownership decision for a one-TC
// repoint, against the snapshot the OPERATION already built.
//
// Outcomes, per ADR-024 s4:
//
//   - UniqueOwner: authorised, carrying the owner.
//   - NotFound: authorised as an ORPHAN REPAIR. This is explicit command policy,
//     not an oversight (REV-160 CODEX-001). ADR-024's matrix cell for the repoint
//     row on NotFound reads "Never overwrite mismatch", which addresses MISMATCH
//     and not ABSENCE, so retargeting wiring whose spec has been removed is not
//     forbidden by it -- and removing that repair capability would take away the
//     only way to fix an orphaned record in place.
//   - Ambiguous or Unprovable: REFUSED. Nothing about the orphan policy relaxes
//     these; only proven absence is permitted.
//
// The orphan policy is bounded by three controls, each enforced elsewhere and
// each covered by a test:
//
//  1. Only NotFound is permitted. Ambiguous and Unprovable refuse here, in
//     Snapshot.RequireOwnership, before any wiring is read.
//  2. The record must declare the identity being repointed. RepointSingle's
//     WiringIdentityMismatchError refuses when the file's body says otherwise, so
//     an orphan repoint can only ever rewrite the record that actually claims
//     that id -- never a foreign-identity one.
//  3. No bulk scope permits an orphan. RepointBulk selects only records whose
//     identity is a proven in-scope owner, and RepointAll refuses the whole
//     operation when any in-scope record has no proven owner (the atomic refusal
//     projectWideRepointRefusal renders). Orphan repair is a SINGLE-TC capability only.
func AuthoriseSingleRepoint(snap *testcase.Snapshot, tcID string) (*RepointAuthority, error) {
	if snap == nil {
		return nil, fmt.Errorf("internal error: repoint authorisation requires the operation's snapshot")
	}
	if oerr := snap.RequireOwnership(tcID); oerr != nil {
		return nil, oerr
	}
	owner, err := snap.RequireOwner(tcID)
	if err != nil {
		// RequireOwnership already refused Ambiguous and Unprovable, so the only
		// error left here is NotFound: the permitted orphan-repair case.
		return &RepointAuthority{single: true, id: tcID, orphan: true}, nil
	}
	return &RepointAuthority{single: true, id: tcID, owner: owner}, nil
}

// AuthoriseScopeRepoint makes the single ownership decision for a folder,
// recursive, or project-wide (--all) repoint, against the snapshot the OPERATION
// already built. scopeFolder "" with recursive true is the whole project.
//
// It carries forward BOTH halves the mutation needs: the in-scope body-identity
// owner set, and every provable identity in the project (which is what lets a
// corrupt wiring filename be attributed to the identity that really owns it
// rather than to a truncating first-"--" split). Both come from the same
// snapshot, so the selection and the refusal cannot disagree with each other.
//
// It replaces the filename-counting AmbiguityCheck, which could not see a renamed
// owner (its filename encodes no id, so it was never counted) and could falsely
// refuse one (its filename-derived id collides with an unrelated spec elsewhere).
// Ownership is a body-identity question and Unprovable candidates must refuse
// (ADR-024 s1/s4).
func AuthoriseScopeRepoint(projectRoot string, snap *testcase.Snapshot, scopeFolder string, recursive bool) (*RepointAuthority, error) {
	if snap == nil {
		return nil, fmt.Errorf("internal error: repoint authorisation requires the operation's snapshot")
	}
	scopeAbs, ferr := testcase.ResolveFolder(projectRoot, scopeFolder)
	if ferr != nil {
		return nil, ferr
	}
	rel, rerr := filepath.Rel(pathsafe.CanonicalRoot(projectRoot), scopeAbs)
	if rerr != nil {
		return nil, fmt.Errorf("resolving repoint scope %q: %w", scopeFolder, rerr)
	}
	scopeRel := filepath.ToSlash(rel)

	if err := snap.RequireFolderOwnership(scopeRel, recursive); err != nil {
		return nil, err
	}
	ids, _ := snap.SelectFolder(scopeRel, recursive)
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return &RepointAuthority{
		scopeFolder: scopeFolder,
		recursive:   recursive,
		ownerIDs:    set,
		knownIDs:    snap.KnownIDs(),
	}, nil
}

// requireSingleAuthority checks that opts carries a single-TC authority for
// exactly tcID. It is the guard that makes the carried decision LOAD-BEARING:
// without it a caller could pass any authority (or none) and RepointSingle would
// go back to trusting the bare id argument it was handed.
func (opts RepointOptions) requireSingleAuthority(tcID string) (*RepointAuthority, error) {
	a := opts.Authority
	if a == nil {
		return nil, fmt.Errorf("internal error: repoint of %q has no carried ownership authority; call link.AuthoriseSingleRepoint against the operation's snapshot first", tcID)
	}
	if !a.single {
		return nil, fmt.Errorf("internal error: repoint of %q was handed a scope authority, not a single-test-case one", tcID)
	}
	if a.id != tcID {
		return nil, fmt.Errorf("internal error: repoint of %q was authorised for %q; the ownership decision does not cover this target", tcID, a.id)
	}
	return a, nil
}

// requireScopeAuthority checks that opts carries a scope authority for exactly
// this scope. Same purpose as requireSingleAuthority: the mutation must run under
// the decision that authorised it, not under a fresh guess.
func (opts RepointOptions) requireScopeAuthority(scopeFolder string, recursive bool) (*RepointAuthority, error) {
	a := opts.Authority
	if a == nil {
		return nil, fmt.Errorf("internal error: repoint of scope %q has no carried ownership authority; call link.AuthoriseScopeRepoint against the operation's snapshot first", scopeFolder)
	}
	if a.single {
		return nil, fmt.Errorf("internal error: repoint of scope %q was handed a single-test-case authority", scopeFolder)
	}
	if a.scopeFolder != scopeFolder || a.recursive != recursive {
		return nil, fmt.Errorf("internal error: repoint of scope %q (recursive=%v) was authorised for %q (recursive=%v)", scopeFolder, recursive, a.scopeFolder, a.recursive)
	}
	return a, nil
}
