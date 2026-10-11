package testcase

// ADR-024 s5, "resolve once and pass the owner forward".
//
// Owner is the value that travels. Orchestration proves ownership ONCE and hands
// the resulting Owner to every downstream consumer -- adapter source selection,
// post-fill validation, hashing, wiring writes, status detail, triage
// classification. Those consumers use what they were given. They do not receive a
// bare id, so they have nothing left to resolve.
//
// That "nothing left to resolve" is the whole point, and it is why Owner's fields
// are unexported and it has no exported constructor. An Owner can only come from
// a resolution against a real snapshot. A consumer cannot mint one from a string
// it happens to be holding, which is precisely how a downstream component used to
// end up with its own private notion of identity -- the shape that produced three
// separate defects in three different files (F1, F2, F4).
//
// The rejected alternative was memoising the walk: a process-global cache that
// made repeated resolution CHEAPER while leaving every consumer an independent
// identity implementation. Caching reduces the cost of the defect, not the
// defect. The count of walks is a symptom; the count of identity implementations
// is the disease.

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
)

// ErrOwnerNotFound signals that no candidate proved the requested identity -- the
// NotFound outcome of ADR-024 s4, and ONLY that. It is never returned for an
// Ambiguous or Unprovable lookup, so a mutating caller can never read an identity
// failure as absence-and-permission-to-create (REV-156 CLAUDE-002). Test with
// errors.Is; the concrete error wraps it and carries the command's own wording.
var ErrOwnerNotFound = errors.New("test case owner not found")

// Owner is one proven, uniquely-owned test case: its id, the spec that owns it,
// and where that spec physically lives.
type Owner struct {
	id            string
	logicalRel    string
	canonicalAbs  string
	metadata      Metadata
	decodeFailure *SchemaFailure
}

// ID returns the bare, grammar-valid TC id this Owner proves.
func (o *Owner) ID() string { return o.id }

// SpecRel returns the owning spec's project-relative, forward-slash path. This is
// the form recorded in task files, wiring, and adapter context -- what a human or
// an adapter sees.
func (o *Owner) SpecRel() string { return o.logicalRel }

// SpecAbs returns the owning spec's canonical absolute path, for reading and
// hashing. It is already containment-checked and symlink-resolved, so a consumer
// hashing or reading through it cannot be redirected out of the project.
func (o *Owner) SpecAbs() string { return o.canonicalAbs }

// Metadata returns the normalized testcase fields from the snapshot read that
// proved this owner. Mutable slices and maps are copied before they leave Owner.
func (o *Owner) Metadata() Metadata { return cloneMetadata(o.metadata) }

// DecodeFailure returns normalized decode evidence retained by the snapshot.
func (o *Owner) DecodeFailure() *SchemaFailure { return cloneSchemaFailure(o.decodeFailure) }

// ownerFrom builds an Owner from a UniqueOwner resolution. It is deliberately
// unexported: every Owner in the process originates here, from a resolution that
// actually proved ownership.
func ownerFrom(res Resolution) *Owner {
	return &Owner{
		id:            res.ID,
		logicalRel:    res.OwnerLogicalRel,
		canonicalAbs:  res.OwnerCanonicalAbs,
		metadata:      cloneMetadata(res.OwnerMetadata),
		decodeFailure: cloneSchemaFailure(res.OwnerDecodeFailure),
	}
}

func cloneSchemaFailure(failure *SchemaFailure) *SchemaFailure {
	if failure == nil {
		return nil
	}
	cloned := *failure
	cloned.RetainedFields = append([]RetainedFieldFailure(nil), failure.RetainedFields...)
	return &cloned
}

func cloneMetadata(metadata Metadata) Metadata {
	cloned := metadata
	cloned.Tags = append([]string(nil), metadata.Tags...)
	return cloned
}

// RequireOwner resolves id to its proven unique owner from THIS snapshot.
//
// Returns:
//   - (owner, nil) for UniqueOwner;
//   - (nil, ErrOwnerNotFound-wrapped) when nothing proves the identity;
//   - (nil, *OwnershipError) for Ambiguous or Unprovable.
//
// A mutating caller must treat every non-nil error as a refusal. Only
// ErrOwnerNotFound means "absent", and even that is not by itself permission to
// create -- that judgement belongs to the command.
func (s *Snapshot) RequireOwner(id string) (*Owner, error) {
	res := s.Resolve(id)
	switch res.Outcome {
	case OutcomeUniqueOwner:
		return ownerFrom(res), nil
	case OutcomeNotFound:
		return nil, fmt.Errorf("%w: %s", ErrOwnerNotFound, id)
	default:
		return nil, OwnershipErrorFor(res)
	}
}

// RequireOwnerUnderFolder is RequireOwner for a FOLDER-QUALIFIED target: the
// proven owner must additionally be reachable under the requested folder, either
// as its real file or through an in-tree alias physically located there.
//
// Reachability is not ownership. It answers "did the user name a place this spec
// can be reached from", using the LOGICAL path, while ownership stays a
// project-wide question answered by body identity. Keeping them separate is what
// stops a symlink from granting authority over a spec that lives elsewhere
// (ADR-024 s3).
func (s *Snapshot) RequireOwnerUnderFolder(projectRoot, folder, id string) (*Owner, error) {
	owner, err := s.RequireOwner(id)
	if err != nil {
		return nil, err
	}
	if folder == "" {
		return owner, nil
	}
	scopeRel, ok := s.canonicalScopeRel(projectRoot, folder)
	if !ok || !s.hasLogicalEntryUnderScope(id, scopeRel) {
		return nil, fmt.Errorf("%w: %s/%s under %s/", ErrOwnerNotFound, folder, id, layout.Current().TestCases)
	}
	return owner, nil
}

// ScopeAuthorityError signals that a folder-qualified DESTRUCTIVE target named a
// place the spec is merely REACHABLE from, not the place it lives. It is a typed,
// centrally-sanitised diagnostic (ADR-024 s7); callers render it once.
type ScopeAuthorityError struct {
	ID        string // the bare TC id the user targeted
	Scope     string // the project-relative folder the user named
	OwnerPath string // where the proven owner physically lives
}

func (e *ScopeAuthorityError) Error() string {
	return fmt.Sprintf(
		"test case '%s' is reachable from %s/ through a link, but its spec lives at %s -- "+
			"reaching a spec through a link does not grant authority to change or remove it.\n"+
			"    Target it where it lives, or remove the link itself.",
		sanitizeIdentityString(e.ID), sanitizeIdentityString(e.Scope), sanitizeIdentityString(e.OwnerPath))
}

// RequireMutationAuthorityUnderScope is the DESTRUCTIVE counterpart to
// RequireOwnerUnderFolder, for a folder-qualified delete or reset.
//
// REV-158 CLAUDE-001. Reachability and authority are different questions, and a
// destructive command needs the second one. RequireOwnerUnderFolder answers "did
// the user name a place this spec can be reached from", which is right for a read
// or a lookup and deliberately tolerant of an in-tree alias. A single-TC delete
// plan used only that gate and then removed the owner's CANONICAL file, so with
// beta/tc-22222222-beta.md real and alpha/tc-22222222-linked.md a symlink to it,
// `gtms delete alpha/tc-22222222` exited 0 and deleted the file in BETA, leaving a
// dangling link in alpha. `gtms reset alpha/tc-22222222` reached the same gate.
// That is ADR-024 s3's explicit prohibition -- "removing the link never removes
// the target merely because it was reached through that link" -- and it is
// BUG-185's own failure class, destroying out-of-scope data, arrived at through an
// alias instead of a duplicate id.
//
// Authority therefore requires the proven owner to physically LIVE under the
// requested scope, which is the same rule SelectFolder applies when it excludes
// aliases from folder selection. A directory alias still works: the scope is
// canonically resolved before this comparison, so naming a linked directory
// resolves to the real one (ADR-024 s3). A spec symlink inside the folder it
// points into also still works, because the owner really is there.
//
// scopeRel is a canonical project-relative folder path. An empty scope means the
// target was unqualified and imposes no scope requirement. NotFound is not an
// authority failure -- delete and reset still clean orphaned flat state keyed by
// an exact id whose spec is already gone -- so it returns nil and leaves the
// existence wording to the command.
func (s *Snapshot) RequireMutationAuthorityUnderScope(scopeRel, id string) error {
	res := s.Resolve(id)
	switch res.Outcome {
	case OutcomeAmbiguous, OutcomeUnprovable:
		return OwnershipErrorFor(res)
	case OutcomeNotFound:
		return nil
	}

	// Host case semantics, as SelectFolder (ADR-024 s3; REV-159 CLAUDE-002). Without
	// this, on a case-insensitive filesystem `gtms delete featurecase/tc-x` would
	// refuse a spec that really does live in FeatureCase, reporting it as merely
	// reachable-through-a-link when it is the physical owner in that very folder.
	scopeRel = s.resolveScopeSpelling(scopeRel)
	if scopeRel == "" {
		return nil
	}

	ownerRel, relErr := filepath.Rel(s.projectRoot, res.OwnerCanonicalAbs)
	if relErr != nil {
		return &ScopeAuthorityError{ID: id, Scope: scopeRel, OwnerPath: res.OwnerLogicalRel}
	}
	if !logicalInScope(filepath.ToSlash(ownerRel), scopeRel, true) {
		return &ScopeAuthorityError{ID: id, Scope: scopeRel, OwnerPath: filepath.ToSlash(ownerRel)}
	}
	return nil
}

// ResolveOwner builds one snapshot and resolves a single target -- bare
// ("tc-abc123") or folder-qualified ("folder/tc-abc123") -- to its proven owner.
//
// It is the entry point for a caller that owns a whole operation and needs the
// owner to hand onward. A caller that already holds a snapshot uses RequireOwner
// or RequireOwnerUnderFolder instead of building a second one.
func ResolveOwner(projectRoot, target string) (*Owner, error) {
	snap, err := NewSnapshot(projectRoot)
	if err != nil {
		return nil, err
	}
	folder, id := SplitQualifiedTarget(target)
	return snap.RequireOwnerUnderFolder(projectRoot, folder, id)
}

// SplitQualifiedTarget splits a possibly folder-qualified target into its folder
// and bare id. A bare target yields an empty folder.
func SplitQualifiedTarget(target string) (folder, id string) {
	if idx := strings.LastIndex(target, "/"); idx >= 0 {
		return target[:idx], target[idx+1:]
	}
	return "", target
}

// OwnerAbsFor joins a project root with an owner's project-relative spec path.
// Kept here so consumers do not hand-roll the join with the wrong separator.
func OwnerAbsFor(projectRoot string, owner *Owner) string {
	if owner == nil {
		return ""
	}
	if owner.canonicalAbs != "" {
		return owner.canonicalAbs
	}
	return filepath.Join(projectRoot, filepath.FromSlash(owner.logicalRel))
}
