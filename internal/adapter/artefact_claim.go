// Artefact ownership: who a file belongs to, whether that is PROVABLE, and how
// that answer changes as an operation runs.
//
// An automation artefact belongs to whichever identity a wiring record CLAIMS it
// for, and a wiring record's identity is its parsed body `testcase` field --
// never its filename, never the artefact's own basename, and never an id-shaped
// prefix of either. Deciding ownership by basename prefix is what made
// `gtms automate <id> --force` delete a renamed owner's artefact, and it cannot
// be made safe: `tcIDGrammar` (internal/testcase/snapshot.go) admits SINGLE
// dashes, so `tc-aaaa-bbbb` is a legitimate id and no parser can tell whether
// "tc-aaaa-bbbb-checkout" is tc-aaaa plus the slug "bbbb-checkout" or
// tc-aaaa-bbbb plus the slug "checkout". This file therefore does no id parsing
// at all: every ownership answer comes from a wiring record's body.
//
// THE CLAIM SET IS OPERATION STATE, NOT A CACHE. ArtefactClaimSet is built once
// per operation, threaded through every invocation that operation makes, and
// UPDATED as that operation changes ownership. It is deliberately not a
// process-global snapshot and not a frozen index: a bulk automate WRITES wiring
// on every iteration, so an index frozen at the start is stale exactly where it
// matters -- item 12 would be judged against the world as it looked before items
// 1..11 claimed their artefacts. It is equally not a per-item rescan, which
// re-reads every wiring record for every test case and reintroduces the
// O(test cases x wiring records) shape this branch exists to remove.
//
// The lifecycle is explicit:
//
//	NewArtefactClaimSet   scan and validate every wiring record ONCE, before the
//	                      operation mutates anything; fail closed if incomplete.
//	Reserve               authorise one planned output path and hold it, so a
//	                      later item in the same operation cannot select it.
//	Commit                the item wrote its wiring: the reservation becomes a
//	                      real claim, replacing that (owner, framework) entry.
//	Release               the item did not complete: the path is free again.
//
// SCOPE OF THE RESERVATION GUARANTEE. An ArtefactClaimSet is not safe for
// concurrent use, and -- more importantly -- it guards ONE OPERATION, not one
// project. Adapter invocations within an operation are sequential
// (ARCHITECTURE.md: one adapter per command invocation per shell), which is what
// makes plain mutation correct here. But ARCHITECTURE.md also supports MULTIPLE
// AGENTS IN MULTIPLE SHELLS, and two concurrent `gtms automate` processes each
// build their own set: both scan before either writes, so neither sees the
// other's reservation, and the collision this reservation catches inside one
// operation is unguarded between two. The on-disk half still applies to whichever
// process runs second in real time, so the window is between one process's scan
// and the other's wiring write. Closing it needs a durable, cross-process lock or
// reservation record, which is deliberately out of scope here (the external
// adapter output-reservation contract noted on cleanupExistingOutputForOwner is
// the same missing primitive). Stated here so the guarantee is not read as
// broader than it is.
package adapter

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// readWiringRecord is the single seam through which claim discovery reads a
// wiring record. It exists so a test can COUNT the reads an operation performs
// and assert the count does not scale with the number of test cases -- the
// O(N x M) shape is invisible to any assertion about results, and elapsed time
// is not a gate in this repository (see the bounded-enumeration guard in
// internal/reader). Production code assigns nothing to it.
var readWiringRecord = wiring.Read

// terminalSafe is the ONE place this package renders repository-derived text
// into a diagnostic, and every Error() below ends with it.
//
// It delegates to testcase.SanitizeIdentityString and adds no rule of its own.
// That helper is identity-NAMED, but its contract is not identity-shaped: it
// documents itself as replacing C0 and DEL "so a filesystem-derived id or PATH
// echoed in a diagnostic cannot manipulate the terminal". Paths are in scope by
// design, not by accident, so using it here is deliberate rather than
// opportunistic -- and routing through one named call keeps that decision
// visible instead of leaving each site to re-argue it.
//
// It is applied to the WHOLE assembled message, not to individual fields. That
// matters: an error wrapped with %w carries the repository-derived path a second
// time inside the cause's own text (wiring.Read renders "parsing wiring record
// <path>: ..."), so sanitising only the struct fields leaves the hostile bytes
// in the message anyway. This call-site class has now been found four times in
// this area (REV-159 CODEX-005, REV-160 CLAUDE-007, REV-160 F1, and the
// ForeignArtefactClaimError hole); sanitising at the boundary where the string
// becomes output is what stops there being a fifth.
func terminalSafe(message string) string {
	return testcase.SanitizeIdentityString(message)
}

// UnprovableArtefactOwnershipError is returned when the wiring records cannot be
// read completely enough to prove who owns the files an operation is about to
// touch.
//
// Inability to parse a record is fine for AUTHORISING (a record that cannot be
// read authorises nothing) and fatal for VETOING (a record that cannot be read
// may be the one that claims the file we are about to delete or overwrite). The
// two are not symmetric, so an incomplete scan is a refusal, not a warning: it
// is the difference between "this file is not claimed" and "I cannot prove this
// file is mine to destroy".
type UnprovableArtefactOwnershipError struct {
	// Path is the wiring record or artefact whose state could not be established.
	Path string
	// Reason is the human-readable failure, already including the cause.
	Reason string
	// Cause is the underlying error, preserved for errors.Is/As.
	Cause error
}

// Error renders through terminalSafe. Reason embeds a repository-derived wiring
// filename AND the wrapped parser's own text, which repeats that filename, so
// the whole message is sanitised rather than any one field.
func (e *UnprovableArtefactOwnershipError) Error() string {
	return terminalSafe(fmt.Sprintf(
		"refusing to automate: artefact ownership cannot be proved because %s -- repair or remove that file, then re-run",
		e.Reason))
}

func (e *UnprovableArtefactOwnershipError) Unwrap() error { return e.Cause }

// ForeignArtefactClaimError is returned when an automate run would write its
// artefact over a file another identity holds -- either through a wiring record
// on disk, or through a reservation made earlier in the SAME operation. Both are
// the same fact to the operator ("that path is not yours"), so both use this one
// error rather than inventing a second vocabulary for the in-flight case.
type ForeignArtefactClaimError struct {
	// Owner is the identity the automate run is for.
	Owner string
	// Claimant is the identity that holds the artefact path.
	Claimant string
	// Artefact is the contested path, project-relative with forward slashes.
	Artefact string
	// InFlight is true when the claimant took the path earlier in this same
	// operation rather than in a wiring record that predates it.
	InFlight bool
}

// Error renders through terminalSafe (ADR-024 s7). All three interpolated values
// are repository-derived: Owner reaches here from the command line, Claimant from
// a file on disk or another item of the same batch, and Artefact is a PATH whose
// work-item directory component comes straight off the filesystem. The path was
// previously rendered raw while the ids either side of it were sanitised, so a
// work-item directory named with ESC and a newline reached the terminal intact
// and could forge a line that read like success.
func (e *ForeignArtefactClaimError) Error() string {
	held := "is already claimed by the wiring record owned by"
	if e.InFlight {
		held = "was already claimed earlier in this run by"
	}
	return terminalSafe(fmt.Sprintf(
		"refusing to write the automate artefact for '%s': %s %s '%s' -- retire or rename that test case's artefact first, then re-run",
		e.Owner, e.Artefact, held, e.Claimant))
}

// OccupiedArtefactPathError is returned when the planned output path is already
// occupied by a file that NO wiring record accounts for.
//
// It is deliberately a different outcome from the preserved-and-reported case,
// and the difference is where the file sits. A stale, unprovable file somewhere
// else in the output directory is preserved and REPORTED: the run still produces
// its authorised artefact, so it succeeds. A stale, unprovable file ON THE
// PLANNED PATH cannot be handled that way -- producing the artefact would mean
// overwriting it -- so that is a collision and the run refuses before anything
// is invoked. Preserving a file and then destroying it would be the worse of
// both behaviours.
type OccupiedArtefactPathError struct {
	// Owner is the identity the automate run is for.
	Owner string
	// Artefact is the occupied path, project-relative with forward slashes.
	Artefact string
}

// Error renders through terminalSafe (ADR-024 s7): the path is repository-derived
// and reaches a terminal.
func (e *OccupiedArtefactPathError) Error() string {
	return terminalSafe(fmt.Sprintf(
		"refusing to write the automate artefact for '%s': %s already exists and no wiring record accounts for it, "+
			"so ownership is unproven and it is preserved rather than overwritten -- %s",
		e.Owner, e.Artefact, artefactRepairOptions))
}

// ContestedArtefactError is returned when one file is claimed by the target AND
// by another identity at the same time. Two wiring records naming one artefact
// is a data-integrity fault that this command cannot adjudicate: removing the
// file serves the target's claim and destroys the other's, and there is nothing
// in the records that says which is right.
//
// So the sweep refuses ATOMICALLY -- it removes nothing at all, not merely
// nothing further -- and the contested file survives byte-identical. Before this
// existed the target-owner check ran first and deleted such a file silently,
// which was the sharpest asymmetry in the design: an UNCLAIMED look-alike was
// preserved and reported, while a file two identities actively claimed was
// destroyed without a word.
type ContestedArtefactError struct {
	// Owner is the identity the sweep is running for.
	Owner string
	// Claimant is the other identity whose wiring also claims the artefact.
	Claimant string
	// Artefact is the contested path, project-relative with forward slashes.
	Artefact string
}

func (e *ContestedArtefactError) Error() string {
	return terminalSafe(fmt.Sprintf(
		"refusing to clean up for '%s': %s is claimed by BOTH '%s' and '%s', so no wiring record proves whose it is -- "+
			"nothing was removed. Repoint or remove one of the two wiring records, then re-run",
		e.Owner, e.Artefact, e.Owner, e.Claimant))
}

// artefactRepairOptions is the one statement of what an operator can do about an
// unprovable artefact, shared by the refusal and the preservation warning so the
// two cannot drift into giving different advice.
const artefactRepairOptions = "restore or reconstruct its wiring record, remove the file by hand once you have checked it, or point this adapter at another output location"

// PreservedArtefactWarning renders the informational diagnostic for a file the
// --force sweep DECLINED to remove. It is a renderer rather than an inline
// Sprintf so its escaping can be tested directly: the path is repository-derived
// and a filename may contain any byte except "/" and NUL, ESC and LF included,
// so an unsanitised "%s" would let a hand-edited name rewrite the terminal or
// forge a line that reads like success. This exact class of defect has already
// been found and fixed three times in this area (REV-159 CODEX-005, REV-160
// CLAUDE-007, REV-160 F1), so every value goes through the CENTRAL sanitiser --
// testcase.SanitizeIdentityString -- and not a second local rule.
//
// holder is the identity whose wiring claims the file, or "" when nothing claims
// it. The message says what was found, that the file was PRESERVED, and what the
// operator can do about it.
func PreservedArtefactWarning(displayPath, target, holder string) string {
	if holder != "" {
		return terminalSafe(fmt.Sprintf(
			"%s was preserved, not removed: it is claimed by the wiring record owned by '%s', so it is that test case's artefact and not '%s's to delete",
			displayPath, holder, target))
	}
	return terminalSafe(fmt.Sprintf(
		"%s was preserved, not removed: no wiring record owned by '%s' authorises removing it, so ownership is unproven -- %s",
		displayPath, target, artefactRepairOptions))
}

// artefactClaim is one wiring record's claim on one file. The (owner, framework)
// pair is the record's on-disk identity -- wiring.Path derives the filename from
// exactly that pair -- so it is the key Commit replaces.
type artefactClaim struct {
	owner     string
	framework string
	// abs is the claimed path, absolute and cleaned.
	abs string
	// info is the claimed file's FileInfo when the file EXISTED at scan time,
	// else nil. It is what makes os.SameFile comparison possible, and it is
	// CAPTURED: os.SameFile compares the stat data already recorded in two
	// FileInfo values, so this keeps identifying the claim after the file is
	// unlinked mid-operation.
	info os.FileInfo
	// pathKey is the canonical PATH identity, also captured at scan time and also
	// immutable: symlinks resolved, "."/".." collapsed, case folded.
	pathKey string
	// reserved marks an entry that an in-flight item holds but has not yet
	// written wiring for.
	reserved bool
}

// ArtefactClaimSet is the operation-scoped, mutable ownership state described in
// the file comment. Build one with NewArtefactClaimSet, thread it through
// CommandFlags, and let Reserve/Commit/Release keep it in step with what the
// operation has actually done.
type ArtefactClaimSet struct {
	projectRoot string
	claims      []artefactClaim
	// unprovable latches a failure that happened AFTER the initial scan and left
	// the set unable to answer correctly. Once set, every query refuses. It is
	// the same fail-closed rule the initial scan follows, applied to the window
	// where the set is being mutated.
	unprovable error
}

// ArtefactReservation is the handle returned by Reserve. Exactly one of Commit
// or Release must be called on it; InvokeWithRoot does that from a single defer
// so no return path can skip it.
type ArtefactReservation struct {
	set       *ArtefactClaimSet
	owner     string
	framework string
	abs       string
}

// NewArtefactClaimSet performs the ONE scan an operation gets: it indexes every
// wiring record under the project's wiring directory by the artefact it claims,
// and FAILS CLOSED.
//
// Identity comes from each record's parsed body `testcase` (ADR-024 s1). A
// record whose filename implies one id while its body declares another belongs
// to the body's id, which is exactly the case this index has to get right.
//
// Any of the following makes ownership unprovable and returns an error:
//   - the wiring directory exists but cannot be listed;
//   - a *.wiring.yaml file cannot be read or parsed;
//   - a claimed artefact path cannot be stat'ed for any reason other than
//     "does not exist".
//
// A MISSING wiring directory is not a failure -- it is a project with no wiring
// at all, where nothing is claimed and nothing can be vetoed.
//
// PRECONDITION: projectRoot is an absolute path. Invoke/InvokeWithRoot supply
// one, and every path this set stores or compares is absolutised against it, so
// a relative root would make every claim's path relative to the PROCESS working
// directory instead. That is a silent miss on a safety check rather than an
// error, so the precondition is stated here and enforced by absolutising both
// sides of every comparison (absoluteCleanPath).
func NewArtefactClaimSet(projectRoot string) (*ArtefactClaimSet, error) {
	set := &ArtefactClaimSet{projectRoot: projectRoot}
	if projectRoot == "" {
		return set, nil
	}
	dir := layout.WiringDir(projectRoot)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return set, nil
		}
		// A wiring path that exists but is NOT a directory holds no records, so
		// the empty index is PROVED rather than assumed -- there is nowhere for a
		// hidden claim to be. Every other listing failure (permissions, I/O) is
		// the opposite: records may exist and one of them may be the claim that
		// would have vetoed this run, so ownership is unprovable.
		if info, statErr := os.Stat(dir); statErr == nil && !info.IsDir() {
			return set, nil
		}
		return nil, &UnprovableArtefactOwnershipError{
			Path:   dir,
			Reason: fmt.Sprintf("the wiring directory %s could not be read: %v", dir, err),
			Cause:  err,
		}
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), wiring.FileSuffix) {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		// Deliberately NOT wiring.Scan / wiring.FindAllForTC: both skip a bad
		// record with a stderr warning, which is precisely the fail-open
		// behaviour that turns "unreadable" into "unclaimed".
		rec, readErr := readWiringRecord(path)
		if readErr != nil {
			return nil, &UnprovableArtefactOwnershipError{
				Path:   path,
				Reason: fmt.Sprintf("the wiring record %s could not be read: %v", path, readErr),
				Cause:  readErr,
			}
		}
		if rec == nil || rec.TestCase == "" || rec.Artefact == "" {
			continue
		}
		claim, claimErr := set.newClaim(rec.TestCase, rec.Framework, rec.Artefact, false)
		if claimErr != nil {
			return nil, claimErr
		}
		set.claims = append(set.claims, claim)
	}
	return set, nil
}

// newClaim resolves one claimed path into a comparable entry, failing closed on
// anything it cannot establish.
func (s *ArtefactClaimSet) newClaim(owner, framework, artefact string, reserved bool) (artefactClaim, error) {
	abs, absErr := s.absolutise(artefact)
	if absErr != nil {
		return artefactClaim{}, &UnprovableArtefactOwnershipError{
			Path:   artefact,
			Reason: fmt.Sprintf("the artefact path %s claimed by %s could not be resolved: %v", artefact, owner, absErr),
			Cause:  absErr,
		}
	}
	info, statErr := os.Stat(abs)
	switch {
	case statErr == nil:
	case errors.Is(statErr, os.ErrNotExist):
		info = nil // claimed but absent: an orphaned claim, not a failure
	default:
		return artefactClaim{}, &UnprovableArtefactOwnershipError{
			Path:   abs,
			Reason: fmt.Sprintf("the artefact %s claimed by %s could not be examined: %v", abs, owner, statErr),
			Cause:  statErr,
		}
	}
	// Identity is resolved HERE, once, while the file is still on disk, and the
	// result is never re-derived from the filesystem afterwards. That ordering is
	// the point: a claim whose file is deleted later -- by an earlier item's own
	// --force sweep, by the operator, by anything -- must go on vetoing, and it
	// can only do so if its identity was captured while there was something to
	// capture. Failing to resolve it is unprovable ownership, never a licence to
	// fall back on comparing strings for inequality.
	pathKey, keyErr := canonicalPathKey(abs)
	if keyErr != nil {
		return artefactClaim{}, &UnprovableArtefactOwnershipError{
			Path:   abs,
			Reason: fmt.Sprintf("the artefact path %s claimed by %s could not be canonicalised: %v", abs, owner, keyErr),
			Cause:  keyErr,
		}
	}
	return artefactClaim{
		owner: owner, framework: framework, abs: abs,
		info: info, pathKey: pathKey, reserved: reserved,
	}, nil
}

// absolutise turns a wiring record's project-relative artefact path into an
// absolute one. Both sides of every comparison go through absoluteCleanPath, so
// a claim and a candidate can never miss each other merely because one was
// spelled relative to the process working directory.
func (s *ArtefactClaimSet) absolutise(artefact string) (string, error) {
	p := filepath.FromSlash(artefact)
	if !filepath.IsAbs(p) {
		p = filepath.Join(s.projectRoot, p)
	}
	return absoluteCleanPath(p)
}

// absoluteCleanPath is the single normalisation both claims and candidates use.
func absoluteCleanPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

// canonicalPathKey returns a deliberately OVER-APPROXIMATING identity key for a
// path, used only where os.SameFile cannot decide (one or both files do not
// exist). It resolves the longest existing prefix through EvalSymlinks -- which
// yields the real on-disk spelling of every directory that exists -- re-appends
// the missing tail, and lowercases the result.
//
// The lowercasing is NOT a substitute for physical-identity comparison; it is
// the fail-closed direction for paths that have no physical identity yet. Two
// distinct non-existent paths that fold together produce a REFUSAL, never a
// deletion, and a claim on a file that does not exist can destroy nothing.
//
// A stat failure other than "does not exist" is returned as an error so the
// caller refuses rather than guesses.
func canonicalPathKey(path string) (string, error) {
	abs, err := absoluteCleanPath(path)
	if err != nil {
		return "", err
	}
	cur := abs
	var tail []string
	for {
		resolved, evalErr := filepath.EvalSymlinks(cur)
		if evalErr == nil {
			return strings.ToLower(filepath.Join(append([]string{resolved}, tail...)...)), nil
		}
		if !errors.Is(evalErr, os.ErrNotExist) {
			return "", evalErr
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return strings.ToLower(filepath.Join(append([]string{cur}, tail...)...)), nil
		}
		tail = append([]string{filepath.Base(cur)}, tail...)
		cur = parent
	}
}

// candidate is one side of a physical-identity comparison, resolved once so a
// caller comparing many claims stats the candidate only once.
type candidate struct {
	abs     string
	info    os.FileInfo
	pathKey string
}

// resolveCandidate absolutises and stats path and computes its fail-closed key.
// A stat failure other than "does not exist" is an error: an unexaminable file
// must never be silently treated as unclaimed.
func resolveCandidate(path string) (candidate, error) {
	abs, absErr := absoluteCleanPath(path)
	if absErr != nil {
		return candidate{}, &UnprovableArtefactOwnershipError{
			Path:   path,
			Reason: fmt.Sprintf("the path %s could not be resolved: %v", path, absErr),
			Cause:  absErr,
		}
	}
	info, statErr := os.Stat(abs)
	if statErr != nil {
		if !errors.Is(statErr, os.ErrNotExist) {
			return candidate{}, &UnprovableArtefactOwnershipError{
				Path:   abs,
				Reason: fmt.Sprintf("the file %s could not be examined: %v", abs, statErr),
				Cause:  statErr,
			}
		}
		info = nil
	}
	pathKey, keyErr := canonicalPathKey(abs)
	if keyErr != nil {
		return candidate{}, &UnprovableArtefactOwnershipError{
			Path:   abs,
			Reason: fmt.Sprintf("the path %s could not be canonicalised: %v", abs, keyErr),
			Cause:  keyErr,
		}
	}
	return candidate{abs: abs, info: info, pathKey: pathKey}, nil
}

// isSame reports whether the candidate and the claim are the SAME PHYSICAL FILE.
//
// When both sides exist the answer is os.SameFile, which is exact on every
// filesystem: it settles case-insensitive spellings (this worktree lives on
// /mnt/c, which is case-insensitive), symlinks, hardlinks, 8.3 short names and
// any other alias, and it does so without a single string comparison.
//
// The CANONICAL KEY is the other half, and it is what makes a claim's identity
// STABLE FOR THE WHOLE OPERATION. Keys are computed when the set is built, so a
// claimed file that is deleted AFTER the scan -- by an earlier item's own
// --force sweep, by the operator, by anything -- keeps vetoing for the rest of
// the operation. Relying on os.SameFile alone would have made such a claim
// silently evaporate the moment its file went away, which is the same
// "unprovable rendered as absent" failure the completeness contract exists to
// stop, arriving by a different route.
//
// Two proofs, either of which is sufficient, and neither of which has to keep
// succeeding once the set is built:
//
//	os.SameFile   compares the stat data held in two FileInfo values, so it is
//	              exact for every alias form -- hardlinks included -- and keeps
//	              working after a claimed path is unlinked, because the claim's
//	              info was captured when the set was built.
//	pathKey       canonical path, captured at scan time. Survives deletion, and
//	              settles symlinks, "."/".." and case folding.
//
// A third comparison on a stored device+inode key was removed as provably
// redundant: it was computed from the same FileInfo, so a non-empty key implied
// a non-nil info on both sides, and on Unix os.SameFile IS dev+ino equality --
// it decided nothing SameFile had not already decided, and on Windows the key
// was always empty. Keeping an independently untestable branch as hypothetical
// forward defence is the dead-code pattern this area has already shed twice.
//
// There is deliberately no branch that concludes "different file" from the paths
// merely differing. Lexical inequality is the fail-open inference this whole
// design exists to remove; the fail-closed direction is that pathKey
// over-approximates (it folds case), and over-approximating costs a refusal,
// never a deletion.
func (c candidate) isSame(claim artefactClaim) bool {
	if c.info != nil && claim.info != nil && os.SameFile(c.info, claim.info) {
		return true
	}
	return c.pathKey != "" && c.pathKey == claim.pathKey
}

// foreignHolder reports the identity that holds the file at path when that
// identity is NOT ownerID -- whether by a wiring record on disk or by a
// reservation made earlier in this operation. A true return is the veto: the
// file is another test case's data and must never be removed or overwritten on
// ownerID's behalf. When several foreign identities hold it the lexically-first
// is named, so the diagnostic is deterministic. An error means ownership could
// not be proved and the caller must refuse.
func (s *ArtefactClaimSet) foreignHolder(ownerID, path string) (holder string, inFlight bool, found bool, err error) {
	if s.unprovable != nil {
		return "", false, false, s.unprovable
	}
	cand, err := resolveCandidate(path)
	if err != nil {
		return "", false, false, err
	}
	type hit struct {
		owner    string
		reserved bool
	}
	var hits []hit
	for _, claim := range s.claims {
		if claim.owner == ownerID || !cand.isSame(claim) {
			continue
		}
		hits = append(hits, hit{owner: claim.owner, reserved: claim.reserved})
	}
	if len(hits) == 0 {
		return "", false, false, nil
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].owner < hits[j].owner })
	return hits[0].owner, hits[0].reserved, true, nil
}

// claimedByOwner reports whether ownerID's OWN wiring claims the file at path.
// This is the positive authority, and the ONLY thing that authorises removing a
// file whose name this identity would not itself produce -- for example the
// legacy artefact a renamed owner wrote before owner-based naming existed.
//
// Reservations are excluded: a path this operation has merely reserved has no
// artefact behind it yet, so there is nothing there to sweep.
func (s *ArtefactClaimSet) claimedByOwner(ownerID, path string) (bool, error) {
	if s.unprovable != nil {
		return false, s.unprovable
	}
	if ownerID == "" {
		return false, nil
	}
	cand, err := resolveCandidate(path)
	if err != nil {
		return false, err
	}
	for _, claim := range s.claims {
		if claim.owner == ownerID && !claim.reserved && cand.isSame(claim) {
			return true, nil
		}
	}
	return false, nil
}

// Reserve authorises one planned output path for ownerID and HOLDS it for the
// rest of the operation, so a later item cannot select the same path. It is the
// in-flight half of the collision check: the on-disk half (a wiring record that
// predates this operation) and this half answer the same question, and a bulk
// run needs both -- item 12's planned path can collide with item 3's artefact,
// which no amount of re-reading the wiring directory would have shown before
// item 3 wrote it.
//
// Returns nil when there is nothing to reserve (no planned path).
func (s *ArtefactClaimSet) Reserve(ownerID, framework, path string) (*ArtefactReservation, error) {
	if s == nil || ownerID == "" || path == "" {
		return nil, nil
	}
	holder, inFlight, found, err := s.foreignHolder(ownerID, path)
	if err != nil {
		return nil, err
	}
	if found {
		return nil, &ForeignArtefactClaimError{
			Owner:    ownerID,
			Claimant: holder,
			Artefact: s.display(path),
			InFlight: inFlight,
		}
	}
	claim, claimErr := s.newClaim(ownerID, framework, path, true)
	if claimErr != nil {
		return nil, claimErr
	}
	s.claims = append(s.claims, claim)
	return &ArtefactReservation{set: s, owner: ownerID, framework: framework, abs: claim.abs}, nil
}

// PlannedPath returns the ABSOLUTE artefact path this reservation authorised --
// the exact value that was collision-checked against every foreign claim and
// then held for the rest of the operation.
//
// It exists so the invocation boundary can HAND that value to the adapter
// context rather than have anything downstream plan the path a second time. Two
// independent computations that happen to agree are not the same guarantee as
// one value carried through: the second would be unchecked, and would drift the
// moment the planner's inputs stopped being identical at both call sites.
//
// A nil reservation means nothing was planned or reserved for this invocation
// (not automate, not Tier 0, or a framework with no planned artefact), and the
// empty string it returns is the carrier BuiltinAutomate refuses to run on.
func (r *ArtefactReservation) PlannedPath() string {
	if r == nil {
		return ""
	}
	return r.abs
}

// display renders a path project-relative with forward slashes for diagnostics.
func (s *ArtefactClaimSet) display(path string) string {
	if abs, err := absoluteCleanPath(path); err == nil {
		if rel, relErr := filepath.Rel(s.projectRoot, abs); relErr == nil {
			return filepath.ToSlash(rel)
		}
	}
	return path
}

// Commit records that the item wrote its wiring: the reservation becomes a real
// claim and REPLACES any previous claim for the same (owner, framework) pair,
// because wiring.Write overwrites exactly that record on disk. Keeping the old
// entry would leave the set asserting a claim the operation has just retired.
//
// The file exists now, so the entry is re-resolved to pick up its FileInfo and
// make later os.SameFile comparisons exact.
func (r *ArtefactReservation) Commit() {
	if r == nil || r.set == nil {
		return
	}
	s := r.set
	kept := s.claims[:0]
	for _, claim := range s.claims {
		if claim.owner == r.owner && claim.framework == r.framework {
			continue // the reservation itself, and any record it replaces
		}
		kept = append(kept, claim)
	}
	s.claims = kept
	claim, err := s.newClaim(r.owner, r.framework, r.abs, false)
	if err != nil {
		// Fail CLOSED, like everything else here. Dropping the claim on a failed
		// re-resolve would leave the set asserting that a path this operation
		// JUST WROTE is unowned, so the next item could reserve and overwrite it.
		// The set is marked unprovable instead, which refuses every subsequent
		// query rather than answering one wrongly.
		s.unprovable = err
		r.set = nil
		return
	}
	s.claims = append(s.claims, claim)
	r.set = nil
}

// Settle disposes of the reservation according to DURABLE STATE, not according
// to whether the invocation happened to return an error.
//
// The two can disagree, and when they do the outer result is the wrong answer.
// BuiltinAutomate writes its wiring record and THEN returns; several steps run
// after that (task.Move, result-contract updates, pipeline records), and any of
// them failing makes the invocation an error even though the wiring record --
// the durable statement of ownership -- is already on disk naming this path.
// Releasing there would free a path the project still claims, and a later
// colliding item would reserve it and overwrite an artefact whose wiring still
// points at it. So the question asked is "does the (owner, framework) wiring
// record now name my path?", and a yes commits regardless of the error.
//
// Read failures are treated as "it might", which keeps the reservation held:
// a held path costs a refusal, a released one costs another identity's data.
func (r *ArtefactReservation) Settle(succeeded bool) {
	if r == nil || r.set == nil {
		return
	}
	if succeeded || r.durablyOwnsPath() {
		r.Commit()
		return
	}
	r.Release()
}

// durablyOwnsPath reports whether the wiring record for this reservation's
// (owner, framework) pair exists on disk and names the reserved path. The pair
// is the record's on-disk identity, so this reads exactly the file the item
// would have written.
func (r *ArtefactReservation) durablyOwnsPath() bool {
	rec, _, err := wiring.Find(r.set.projectRoot, r.owner, r.framework)
	if err != nil {
		return true // cannot prove it did NOT write: keep the path held
	}
	if rec == nil || rec.Artefact == "" {
		return false
	}
	abs, absErr := r.set.absolutise(rec.Artefact)
	if absErr != nil {
		return true
	}
	if abs == r.abs {
		return true
	}
	// Different spelling, possibly the same file. Decide it the same way every
	// other ownership question is decided rather than on the strings.
	cand, candErr := resolveCandidate(abs)
	if candErr != nil {
		return true
	}
	key, keyErr := canonicalPathKey(r.abs)
	if keyErr != nil {
		return true
	}
	return cand.pathKey == key
}

// Release records that the item did not complete, so the path is free again and
// the operation's own earlier view of ownership is restored. Any pre-existing
// wiring record for this (owner, framework) pair is untouched: it was never
// removed, only shadowed by the reservation.
func (r *ArtefactReservation) Release() {
	if r == nil || r.set == nil {
		return
	}
	s := r.set
	kept := s.claims[:0]
	for _, claim := range s.claims {
		if claim.reserved && claim.owner == r.owner && claim.framework == r.framework && claim.abs == r.abs {
			continue
		}
		kept = append(kept, claim)
	}
	s.claims = kept
	r.set = nil
}

// PlanArtefactPath is the ONE definition of where an automate run writes its
// artefact. It is a pure function of the RESOLVED OWNER (ownerID), the source
// spec's project-relative path (specRel, which contributes the descriptive slug
// and the per-work-item subdirectory and NOTHING identity-bearing), and the
// framework's output conventions.
//
// It exists so the planned path can be computed at the invocation boundary --
// before any file is removed, any task state is created, or any adapter runs --
// and then CARRIED to the adapter that writes the file. Nothing recomputes it:
// planTier0AutomateArtefact is its ONE production call site, that single result
// is what enforceArtefactOwnership reserves, and the reserved path is what
// reaches the adapter context -- BuiltinAutomate refuses to run without the
// carried value. TestArtefactPlanning_HasExactlyOneProductionCallSite fails if a
// second planning call is ever added.
//
// outputDir is the ABSOLUTE configured output directory and is honoured only
// when outputDirConfigured is true (BUG-125: an explicit
// adapters.automate.<name>.output-dir wins over the framework-native default);
// otherwise the framework's own project-relative default is used.
func PlanArtefactPath(projectRoot string, support FrameworkSupport, outputDir string, outputDirConfigured bool, subdir, specRel, ownerID string) string {
	if support == nil {
		return ""
	}
	var outDir string
	if outputDirConfigured {
		// Group per work-item by appending the source subdir (trailing slash
		// trimmed; "" leaves the dir unchanged).
		outDir = filepath.Join(outputDir, filepath.FromSlash(strings.TrimRight(subdir, "/")))
	} else {
		outDir = filepath.Join(projectRoot, filepath.FromSlash(support.OutputDir(subdir)))
	}
	return filepath.Join(outDir, DeriveArtefactBasename(specRel, ownerID)+support.Extension())
}

// planTier0AutomateArtefact returns the path a Tier 0 built-in automate run WILL
// write, computed from the carried owner, together with the framework it will be
// wired under, or ok=false when this is not that path.
//
// The gate is the DISPATCH CLASSIFICATION -- command "automate" at tier 0 -- not
// the adapter's name. invokeBuiltinAction dispatches on exactly that pair, and a
// gtms.config entry with no command/script/module resolves to tier 0 under ANY
// name, so a name-based gate would leave every renamed built-in automate adapter
// unguarded.
func planTier0AutomateArtefact(projectRoot string, resolved *ResolvedAdapter, flags CommandFlags) (path, framework string, ok bool) {
	if !isTier0AutomateDispatch(resolved) || flags.Owner == nil {
		return "", "", false
	}
	framework = ResolveFramework(resolved, flags.Framework)
	if framework == "" || framework == "manual" {
		return "", "", false
	}
	support := LookupFrameworkSupport(framework)
	if support == nil {
		return "", "", false
	}
	specRel := flags.Owner.SpecRel()
	outputDir := ""
	configured := resolved.Config != nil && resolved.Config.OutputDir != ""
	if configured {
		outputDir = filepath.Join(projectRoot, resolved.Config.OutputDir)
	}
	planned := PlanArtefactPath(projectRoot, support, outputDir, configured, deriveOutputSubdir(specRel), specRel, flags.Owner.ID())
	if planned == "" {
		return "", "", false
	}
	return planned, framework, true
}

// isTier0AutomateDispatch reports whether this invocation will be dispatched to
// BuiltinAutomate. It mirrors the invoker's own dispatch: tier 0 selects
// invokeBuiltinAction, which switches on resolved.Command.
func isTier0AutomateDispatch(resolved *ResolvedAdapter) bool {
	return resolved != nil && resolved.Tier == 0 && resolved.Command == "automate"
}

// enforceArtefactOwnership is an invocation boundary, and it sits beside
// requireCarriedOwner for the same reason: it must refuse BEFORE any state
// exists. It returns the operation's claim set (built here when the caller did
// not supply one) and the reservation held for this invocation.
//
// For every automate invocation it proves the wiring index is complete, and for
// the Tier 0 path it additionally rejects -- and then reserves -- the planned
// output path. A refusal here means no task file, no result contract, no --force
// sweep and no adapter process ever happened.
//
// A caller that supplies a claim set gets ONE scan for the whole operation; a
// caller that does not gets one scan for its single invocation. Same abstraction,
// same methods, no second code path -- only the scope of the set differs.
//
// Owner-based naming does not make the collision check redundant: an owner's own
// planned name can still land on a LEGACY, filename-derived artefact another
// identity produced before owner-based naming existed and still claims.
func enforceArtefactOwnership(projectRoot string, resolved *ResolvedAdapter, flags CommandFlags) (*ArtefactClaimSet, *ArtefactReservation, error) {
	if resolved == nil || resolved.Command != "automate" {
		return nil, nil, nil
	}
	claims := flags.Claims
	if claims == nil {
		var err error
		if claims, err = NewArtefactClaimSet(projectRoot); err != nil {
			return nil, nil, err
		}
	}
	planned, framework, ok := planTier0AutomateArtefact(projectRoot, resolved, flags)
	if !ok {
		return claims, nil, nil
	}
	// Two refusals, in this order, because the more specific diagnostic must win.
	//
	// First: the planned path is CLAIMED by another identity. That names a
	// claimant, so the operator learns whose artefact it is.
	holder, inFlight, foreign, holderErr := claims.foreignHolder(flags.Owner.ID(), planned)
	if holderErr != nil {
		return nil, nil, holderErr
	}
	if foreign {
		return nil, nil, &ForeignArtefactClaimError{
			Owner:    flags.Owner.ID(),
			Claimant: holder,
			Artefact: claims.display(planned),
			InFlight: inFlight,
		}
	}

	// Second, the R1 exception: a file sitting ON the planned path that NOTHING
	// accounts for. It is a collision rather than something to preserve and
	// report around, because the run cannot produce its artefact without
	// overwriting it -- preserving a file elsewhere and destroying this one would
	// be the worst of both behaviours. Refuse on this same boundary so nothing is
	// invoked and nothing is written. A file the target's OWN wiring claims is
	// not this case: that is an ordinary --force overwrite and the sweep removes
	// it below.
	occupied, occErr := claims.pathOccupiedByStranger(flags.Owner.ID(), planned)
	if occErr != nil {
		return nil, nil, occErr
	}
	if occupied {
		return nil, nil, &OccupiedArtefactPathError{
			Owner:    flags.Owner.ID(),
			Artefact: claims.display(planned),
		}
	}

	reservation, err := claims.Reserve(flags.Owner.ID(), framework, planned)
	if err != nil {
		return nil, nil, err
	}
	return claims, reservation, nil
}

// pathOccupiedByStranger reports whether a file EXISTS at path and no wiring
// record owned by ownerID accounts for it. Existence is the trigger: an absent
// path cannot be overwritten and a claimed-but-absent path has nothing to lose.
func (s *ArtefactClaimSet) pathOccupiedByStranger(ownerID, path string) (bool, error) {
	if s.unprovable != nil {
		return false, s.unprovable
	}
	info, statErr := os.Stat(path)
	if statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return false, nil
		}
		return false, &UnprovableArtefactOwnershipError{
			Path:   path,
			Reason: fmt.Sprintf("the planned artefact path %s could not be examined: %v", path, statErr),
			Cause:  statErr,
		}
	}
	if info.IsDir() {
		return false, nil // a directory in the way is a different failure, raised by the writer
	}
	owned, err := s.claimedByOwner(ownerID, path)
	if err != nil {
		return false, err
	}
	return !owned, nil
}

// IsOwnerDerivedArtefactName reports whether base is a name the owner-derived
// naming rule could have produced for ownerID -- that is, exactly ownerID, or
// ownerID followed by "-" and a slug the sanitiser would have emitted unchanged.
//
// It exists ONLY to scope the preservation reporting. Deciding what to DELETE by
// resemblance is what was retired, and nothing here restores it: this decides
// what is worth MENTIONING, which destroys nothing. Scoping matters because a
// diagnostic that lists every unrelated file in the output directory is noise,
// and noise is how a real warning gets ignored.
//
// Deriving the predicate from the naming rule rather than from a hand-rolled
// prefix test also keeps it honest as that rule changes, and it excludes the
// extended-id sibling for free: "tc-x--legacy-y" leaves the rest "-legacy-y",
// which the sanitiser would not have emitted, so it is not a name tc-x produces.
func IsOwnerDerivedArtefactName(base, ownerID string) bool {
	if ownerID == "" {
		return false
	}
	stem := specFileStem(base)
	if stem == ownerID {
		return true
	}
	rest, ok := strings.CutPrefix(stem, ownerID+"-")
	if !ok || rest == "" {
		return false
	}
	return sanitiseArtefactText(rest) == rest
}
