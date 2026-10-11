package testcase

// ADR-024 first half: one immutable project-wide snapshot of the test-case
// spec tree, canonical-path physical ownership, and explicit lookup outcomes.
//
// This is the SINGLE identity model every mutating consumer (and direct
// single-TC read) resolves ownership through. Body identity determines
// ownership; a filename only shortlists candidates for relevance. Owners are
// deduplicated by CANONICAL path so a real spec plus an in-tree alias counts as
// one owner. Scope membership uses the LOGICAL project-relative path so a
// spec-file symlink never transfers its target's folder membership.
//
// The four inherited traps (REV-156, do not reintroduce):
//   - CODEX-001: never pre-filter owners by the filename shortlist -- a renamed
//     duplicate owner is invisible to filenames and must be found by body id.
//   - CODEX-002: deduplicate owners by canonical path or an in-tree alias reads
//     as a second owner.
//   - CLAUDE-002: an identity error is never "not found"; a mutating caller must
//     not read it as permission to create or overwrite.
//   - CLAUDE-004: host case-sensitivity comes from the OS resolving the scope
//     directory, never from EqualFold or runtime.GOOS.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
)

// tcIDGrammar is ADR-024's positive, shell-safe, execution-bound TC-ID grammar.
// It admits the established extended form tc-77777777--legacy (alphanumeric runs
// separated by single or double dashes) while rejecting separators, traversal,
// control characters, spaces, and shell metacharacters. An identity failing this
// grammar is Unprovable, and it may never reach a path, adapter, or shell.
var tcIDGrammar = regexp.MustCompile(`^tc-[a-z0-9]+(--?[a-z0-9]+)*$`)

// ValidExecutionID reports whether id satisfies the positive TC-ID grammar. It
// is the boundary check every path/adapter/shell-bound identity must pass
// (ADR-024 s6; tc-b127e260). Quoting is defence in depth, not a substitute.
func ValidExecutionID(id string) bool {
	return tcIDGrammar.MatchString(id)
}

// Outcome is the explicit result of an ownership lookup (ADR-024 s4).
type Outcome int

const (
	// OutcomeNotFound: no candidate proves the requested identity.
	OutcomeNotFound Outcome = iota
	// OutcomeUniqueOwner: exactly one canonical physical owner proves it.
	OutcomeUniqueOwner
	// OutcomeAmbiguous: more than one canonical physical owner proves it.
	OutcomeAmbiguous
	// OutcomeUnprovable: a relevant candidate cannot be read or identified safely.
	OutcomeUnprovable
)

// UnprovableReason classifies why ownership could be neither proved nor
// disproved. Each maps to a repair sentence appropriate to THAT failure -- a
// single generic message would send the user to the wrong repair for four of
// five cases (BUG-185 acceptance).
type UnprovableReason int

const (
	// ReasonUnreadable: the candidate spec could not be read (permissions/encoding).
	ReasonUnreadable UnprovableReason = iota
	// ReasonMalformed: the candidate's YAML frontmatter could not be parsed.
	ReasonMalformed
	// ReasonMissingID: the candidate parsed but declares no test_case_id.
	ReasonMissingID
	// ReasonGrammarInvalid: the candidate's test_case_id fails the positive grammar.
	ReasonGrammarInvalid
)

// repairSentence returns the repair guidance for an Unprovable reason at path.
func repairSentence(reason UnprovableReason, path string) string {
	switch reason {
	case ReasonMissingID:
		return fmt.Sprintf("add a test_case_id to %s, or remove the file", path)
	case ReasonMalformed:
		return fmt.Sprintf("repair the malformed YAML frontmatter in %s, or remove the file", path)
	case ReasonUnreadable:
		return fmt.Sprintf("restore readability (permissions/encoding) of %s, or remove the file", path)
	case ReasonGrammarInvalid:
		return fmt.Sprintf("correct the invalid test_case_id in %s so it matches tc-<hex>[--suffix], or remove the file", path)
	default:
		return fmt.Sprintf("repair or remove %s", path)
	}
}

func schemaRepairSentence(failure *SchemaFailure, path string) string {
	if failure == nil {
		return ""
	}
	path = sanitizeIdentityString(path)
	key := sanitizeIdentityString(failure.Key)
	declaredKind := sanitizeIdentityString(failure.DeclaredKind)
	switch failure.Kind {
	case SchemaFailureNoFrontmatterBlock:
		return fmt.Sprintf("add a leading YAML frontmatter block in %s, or remove the file", path)
	case SchemaFailureMissingSchemaDiscriminator:
		return fmt.Sprintf("add gtms_schema: \"test-case/v2\" in %s, or remove the file", path)
	case SchemaFailureMalformedSchemaDiscriminator, SchemaFailureUnsupportedSchemaVersion:
		return fmt.Sprintf("set gtms_schema to \"test-case/v2\" in %s, or remove the file", path)
	case SchemaFailureWrongConceptType:
		return fmt.Sprintf("set type to Test Case Spec in %s, or remove the file", path)
	case SchemaFailureMissingTestCategory:
		return fmt.Sprintf("set test_type to a non-empty category in %s, or remove the file", path)
	case SchemaFailureDuplicateYAMLKey:
		return fmt.Sprintf("remove duplicate YAML key '%s' in %s, or remove the file", key, path)
	case SchemaFailureWrongTypeRetainedField:
		return fmt.Sprintf("set '%s' to its declared %s in %s, or remove the file", key, declaredKind, path)
	default:
		return repairSentence(ReasonMalformed, path)
	}
}

// Candidate is one directory entry the snapshot walk visited under
// gtms/test/cases/. LogicalRel is where the entry appears (a symlink's own
// path); CanonicalRel/CanonicalAbs is its resolved physical target.
type Candidate struct {
	LogicalRel    string // project-relative, forward-slash (entry path)
	CanonicalRel  string // project-relative, forward-slash (resolved target)
	CanonicalAbs  string // canonical absolute path of the resolved target
	BodyID        string // grammar-valid parsed test_case_id; retained through schema failure
	Unprovable    bool
	Reason        UnprovableReason // meaningful only when Unprovable
	SchemaFailure *SchemaFailure   // strict schema failure, when schema caused Unprovable
	DecodeFailure *SchemaFailure   // normalized decode failure retained for reader diagnostics
	Metadata      Metadata         // normalized fields from the snapshot's single decode
	// Frontmatter is the leading frontmatter region of a parsed candidate file (the
	// bytes up to and including the closing delimiter line), exactly as the
	// enumeration read it. It is set only when BodyID is set. It lets a bulk reader
	// decode a proven owner's display fields from THIS enumeration's read instead of
	// opening every owner file a second time -- which, for an unscoped operation, was
	// a second project-wide pass (ADR-024 5a/5b: at most one per operation). It is
	// retained bytes of one immutable enumeration, not a cache: it is never shared
	// across snapshots and never re-validated.
	//
	// It is a string, not a []byte, so a caller holding a Resolution cannot mutate
	// the snapshot's storage (REV-186 CODEX-001). Aliases of one canonical file
	// share ONE retained value: buildSnapshot keeps the first copy per
	// CanonicalRel and hands the same string to every alias (REV-186 CODEX-002).
	Frontmatter string
}

// IsAlias reports whether the entry is a symlink whose resolved target lies at a
// different logical location (an alias, not the physical owner).
func (c Candidate) IsAlias() bool { return c.CanonicalRel != c.LogicalRel }

// Snapshot is one immutable project-wide enumeration of the spec tree.
type Snapshot struct {
	projectRoot string // canonical
	casesRel    string // project-relative cases dir, forward-slash (e.g. gtms/test/cases)
	candidates  []Candidate
	// dirs holds every directory the walk visited, project-relative and
	// forward-slash, in the REAL on-disk spelling filepath.Walk reports. It is
	// what lets scope membership follow host filesystem identity rather than byte
	// equality (ADR-024 s3; see resolveScopeSpelling).
	dirs []string
}

// NewSnapshot builds one immutable project-wide snapshot of the spec tree.
//
// One operation builds ONE snapshot and passes the resolved owner forward
// (ADR-024 s5, internal/testcase/owner.go). There is deliberately no cache here:
// a cache would make re-resolution cheap rather than unnecessary, and would leave
// every consumer holding a bare id and its own idea of what that id means. It
// would also carry real hazards for the sake of that cheapness -- process-global
// state, an unenforced "reads only" constraint, no nesting guard, and a stale
// window if an adapter writes a spec mid-operation.
//
// A missing cases directory yields an empty snapshot (not an error); an
// out-of-project cases-root symlink or an inaccessible cases root fails closed.
func NewSnapshot(projectRoot string) (*Snapshot, error) {
	return buildSnapshot(projectRoot, pathsafe.CanonicalRoot(projectRoot))
}

// snapshotBuilds counts calls to buildSnapshot. It exists ONLY so tests can prove
// ADR-024's "exactly one project-wide enumeration per operation" property -- in
// particular that a qualified <folder>/<tc-id> target builds ONE snapshot, not one
// for the scoped-owner check and another for the reader (ENH-205). It is never read
// by production code; a single atomic increment per build is not a cache and does not
// change behaviour. Read it through SnapshotBuildsForTest.
var snapshotBuilds int64

// candidateReads counts physical testcase candidate reads. It is test-only
// observability for the one-read admission property, not a cache or policy seam.
var candidateReads int64

// SnapshotBuildsForTest returns the number of buildSnapshot calls so far. Test-only
// observability for the bounded-enumeration property; production must not depend on it.
func SnapshotBuildsForTest() int64 { return atomic.LoadInt64(&snapshotBuilds) }

// buildSnapshot performs the project-wide enumeration.
func buildSnapshot(projectRoot, canonRoot string) (*Snapshot, error) {
	atomic.AddInt64(&snapshotBuilds, 1)
	casesRel := layout.Current().TestCases

	casesRoot, _, err := pathsafe.ResolveUnderRoot(projectRoot, casesRel)
	if err != nil {
		return nil, fmt.Errorf("test cases directory resolves outside the project root: %w", err)
	}

	snap := &Snapshot{projectRoot: canonRoot, casesRel: filepath.ToSlash(casesRel)}

	// F5: only a genuinely ABSENT cases directory is an empty snapshot. A stat
	// failure (permission denied, I/O error) or an existing NON-directory cases
	// root must return an error -- treating either as an empty project would make
	// owners vanish into NotFound, which delete/reset read as proven absence and a
	// licence to clean up. An access failure is never proof a test case is gone.
	info, statErr := os.Stat(casesRoot)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return snap, nil
		}
		return nil, fmt.Errorf("cannot read test cases directory %s: %w", casesRel, statErr)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("test cases path %s exists but is not a directory", casesRel)
	}

	// One retained frontmatter value per canonical physical file: an alias of a file
	// already seen reuses that file's string instead of retaining its own copy
	// (REV-186 CODEX-002).
	frontmatterByCanonical := map[string]string{}
	evidenceByCanonical := map[string]candidateEvidence{}

	walkErr := filepath.Walk(casesRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// A walk error means the enumeration may be incomplete. Fail closed so
			// a destructive caller aborts rather than undercounting.
			return fmt.Errorf("scanning %s: %w", casesRel, err)
		}
		if info.IsDir() {
			// Record the directory in its real on-disk spelling, so a differently
			// cased scope can be matched back to it by filesystem identity.
			snap.dirs = append(snap.dirs, filepath.ToSlash(mustRel(canonRoot, path)))
			return nil
		}
		if IsReservedCompanion(info.Name()) {
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".md") {
			return nil
		}
		cand, ok := classifyCandidate(canonRoot, casesRoot, path, evidenceByCanonical)
		if ok {
			if cand.BodyID != "" {
				if shared, seen := frontmatterByCanonical[cand.CanonicalRel]; seen && shared == cand.Frontmatter {
					cand.Frontmatter = shared
				} else if !seen {
					frontmatterByCanonical[cand.CanonicalRel] = cand.Frontmatter
				}
			}
			snap.candidates = append(snap.candidates, cand)
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return snap, nil
}

// classifyCandidate resolves one walked .md entry to a Candidate. Returns
// ok=false when the entry is out-of-tree or its resolved target is not a
// regular file (an out-of-tree symlink, FIFO, device, or broken link is not
// opened as a spec, per ADR-024 s3).
func classifyCandidate(canonRoot, casesRoot, path string, evidenceByCanonical map[string]candidateEvidence) (Candidate, bool) {
	relToCases, relErr := filepath.Rel(casesRoot, path)
	if relErr != nil {
		return Candidate{}, false
	}
	// Containment + canonicalisation of the entry (resolves an in-tree symlink to
	// its target; rejects an out-of-tree symlink).
	canonAbs, _, resErr := pathsafe.ResolveUnderRoot(casesRoot, relToCases)
	if resErr != nil {
		return Candidate{}, false // out-of-tree candidate: not a spec
	}
	st, statErr := os.Stat(canonAbs)
	if statErr != nil || !st.Mode().IsRegular() {
		return Candidate{}, false // non-regular target: not opened as a spec
	}

	logicalRel := filepath.ToSlash(mustRel(canonRoot, path))
	canonicalRel := filepath.ToSlash(mustRel(canonRoot, canonAbs))

	cand := Candidate{
		LogicalRel:   logicalRel,
		CanonicalRel: canonicalRel,
		CanonicalAbs: canonAbs,
	}

	evidence, seen := evidenceByCanonical[canonicalRel]
	if !seen {
		evidence = readCandidateEvidence(canonAbs)
		evidenceByCanonical[canonicalRel] = evidence
	}
	if !evidence.readable {
		if !isSpecShaped(filepath.Base(path), false) {
			return Candidate{}, false
		}
		cand.Unprovable = true
		cand.Reason = ReasonUnreadable
		return cand, true
	}

	result := evidence.decoded
	cand.Metadata = result.Metadata
	cand.DecodeFailure = result.Failure
	cand.Frontmatter = evidence.frontmatter
	identityValid := result.ParsedIdentity != "" && ValidExecutionID(result.ParsedIdentity)
	failure := result.Failure
	if !identityValid || failure != nil {
		// REV-158 CLAUDE-002: an ID-less, malformed, or unreadable file becomes an
		// Unprovable candidate -- which fails every folder-scoped mutation closed --
		// only when it is structurally a spec. Ordinary documentation in a cases
		// folder is not a candidate at all.
		//
		// REV-159 CODEX-003 / CLAUDE-006: admission asks the CARRIER, not the parse
		// result. Keying the second arm on a successful frontmatter parse made a
		// renamed spec vanish the moment its frontmatter broke -- the one case where
		// admission matters most. See isSpecShaped / hasIdentityCarrier.
		if !identityValid && !isSpecShaped(filepath.Base(path), evidence.hasIdentityCarrier) {
			return Candidate{}, false
		}
		cand.Unprovable = true
		switch {
		case identityValid && failure != nil:
			cand.Reason = ReasonMalformed
			cand.SchemaFailure = failure
		case result.ParsedIdentity != "":
			cand.Reason = ReasonGrammarInvalid
		case failure != nil && failure.Kind == SchemaFailureMissingSchemaDiscriminator:
			cand.Reason = ReasonMissingID
		case failure != nil:
			cand.Reason = ReasonMalformed
			cand.SchemaFailure = failure
		default:
			cand.Reason = ReasonMissingID
		}
		if identityValid {
			cand.BodyID = result.ParsedIdentity
		}
		return cand, true
	}
	cand.BodyID = result.ParsedIdentity
	return cand, true
}

type candidateEvidence struct {
	decoded            DecodeResult
	frontmatter        string
	hasIdentityCarrier bool
	readable           bool
}

func readCandidateEvidence(path string) candidateEvidence {
	atomic.AddInt64(&candidateReads, 1)
	data, err := os.ReadFile(path)
	if err != nil {
		return candidateEvidence{}
	}
	decoded := DecodeCandidate(data)
	hasCarrier := hasIdentityCarrier(data)
	if !hasCarrier && decoded.Failure != nil && decoded.Failure.Kind == SchemaFailureMalformedYAML {
		hasCarrier = hasIdentityCarrierAfterLeadingPreamble(data)
	}
	return candidateEvidence{
		decoded:            decoded,
		frontmatter:        leadingFrontmatterRegion(data),
		hasIdentityCarrier: hasCarrier,
		readable:           true,
	}
}

func leadingFrontmatterRegion(data []byte) string {
	lineEnd := bytes.IndexByte(data, '\n')
	if lineEnd < 0 {
		return ""
	}
	for offset := lineEnd + 1; offset <= len(data); {
		next := bytes.IndexByte(data[offset:], '\n')
		end := len(data)
		if next >= 0 {
			end = offset + next
		}
		if isDelimiterLine(data[offset:end]) {
			if end < len(data) {
				end++
			}
			return string(data[:end])
		}
		if next < 0 {
			break
		}
		offset = end + 1
	}
	return ""
}

// mustRel returns filepath.Rel(base, target), falling back to target on error.
func mustRel(base, target string) string {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return target
	}
	return rel
}

// specFilenameEnvelope matches the filename shape GTMS gives a test-case spec:
// "tc-", at least one id character, an optional slug, and ".md". It is
// deliberately INDEPENDENT of any requested id -- it answers "is this file
// structurally a spec at all", never "does this file belong to the id we are
// looking up". Body identity alone decides ownership (ADR-024 s1), so a renamed
// owner such as tc-deadbeef-renamed.md whose body declares tc-a1b2c3d4 is still
// admitted here and still owns the id it declares.
var specFilenameEnvelope = regexp.MustCompile(`^tc-[a-z0-9].*\.md$`)

// carrierKeyLine matches the identity-carrier key inside a leading frontmatter
// region. Its PRESENCE admits the file; its VALUE is irrelevant here, because
// establishing identity is parseBodyIdentity's job, not admission's (ADR-024 4a).
var carrierKeyLine = regexp.MustCompile(`^\s*test_case_id\s*:`)

// carrierScanLimit is ADR-024 4a's 8 KiB bound on the leading region scanned for
// the identity carrier. It bounds the read for a huge or binary file whose first
// line happens to be a delimiter.
const carrierScanLimit = 8 << 10

// isDelimiterLine reports whether line is a `---` frontmatter delimiter.
//
// ADR-024 4a: the comparison ignores a leading UTF-8 BOM, a trailing CR, and
// trailing spaces or tabs. This repository's spec files are CRLF, so every one of
// them carries "---\r" as its first line -- a byte-exact comparison would admit
// NONE of them, dropping valid renamed owners along with malformed ones.
//
// This tolerates the line ending; it NORMALISES nothing. The stripping applies
// only to this comparison. It does not rewrite the file and it does not touch
// hash semantics -- pipeline.HashFile's own \r handling is a separate rule
// serving a different purpose, and the two must not be conflated. A CRLF spec and
// its LF equivalent are admitted identically and their stored bytes and hashes
// are unchanged.
func isDelimiterLine(line []byte) bool {
	line = bytes.TrimPrefix(line, []byte("\xef\xbb\xbf")) // UTF-8 BOM
	line = bytes.TrimSuffix(line, []byte("\r"))
	line = bytes.TrimRight(line, " \t")
	return bytes.Equal(line, []byte("---"))
}

// hasIdentityCarrier reports whether absPath carries a detectable test_case_id
// key in its LEADING frontmatter region, WITHOUT parsing the document.
//
// ADR-024 4a, exactly: the first line must be a `---` delimiter, and within the
// region running from that line to the next line that is a delimiter, or to
// 8 KiB, or to EOF -- whichever comes first -- some line must match
// `^\s*test_case_id\s*:`. The region need not be terminated.
//
// The no-parse rule is load-bearing in BOTH directions, and each direction has
// already produced a High defect:
//
//   - Requiring a parse means a truncated or malformed frontmatter block admits
//     nothing, so a renamed spec such as login.md vanishes from ownership the
//     moment its YAML breaks, and a folder delete, reset, automate, execute or
//     repoint proceeds over it reporting success (REV-159 CODEX-003/CLAUDE-006).
//     That is Unprovable converted into absence on a destructive path -- the
//     behaviour ADR-024 exists to reject.
//   - Dropping the region bound means a whole-file byte scan admits any document
//     that merely QUOTES the token: a README showing the spec format in a fenced
//     block, or a .bats fixture that seeds one. That file becomes Unprovable and
//     refuses every folder-scoped mutation (REV-158 CLAUDE-002). Files in this
//     repository do carry the literal line outside frontmatter.
//
// A read or open failure is treated as carrier-absent (ADR-024 4a). The caller
// only reaches here for a contained regular file (section 3).
func hasIdentityCarrier(data []byte) bool {
	region := data
	if len(region) > carrierScanLimit {
		region = region[:carrierScanLimit]
	}

	lines := bytes.Split(region, []byte("\n"))
	if len(lines) == 0 || !isDelimiterLine(lines[0]) {
		return false // not a frontmatter carrier at all
	}
	for _, line := range lines[1:] {
		if isDelimiterLine(line) {
			return false // region closed without the carrier
		}
		if carrierKeyLine.Match(line) {
			return true
		}
	}
	// Region ran to 8 KiB or EOF without closing: an unterminated region is
	// explicitly allowed, and the carrier simply was not in it.
	return false
}

func hasIdentityCarrierAfterLeadingPreamble(data []byte) bool {
	for len(data) > 0 {
		if bytes.HasPrefix(data, []byte("\xef\xbb\xbf")) {
			data = data[3:]
			continue
		}
		line, rest, found := bytes.Cut(data, []byte("\n"))
		if len(bytes.TrimSpace(bytes.TrimSuffix(line, []byte("\r")))) != 0 {
			break
		}
		if !found {
			return false
		}
		data = rest
	}
	return hasIdentityCarrier(data)
}

// isSpecShaped reports whether a walked .md file is structurally a test-case
// spec, using only evidence that does not depend on which id is being resolved:
// the spec filename envelope, or a detectable identity carrier in its leading
// frontmatter region.
//
// REV-158 CLAUDE-002: this gates ADMISSION of an UNPROVABLE candidate only. A
// file whose body proves an identity is an owner however it is named, so the
// renamed-owner case ADR-024 s4 expressly permits is untouched -- admission must
// never be keyed on the requested id or renamed owners would stop being owners.
// What this excludes is ordinary documentation: a README.md or NOTES.md sitting
// in a cases folder parses cleanly, declares no identity, and used to become an
// Unprovable candidate that refused every folder-scoped delete, reset, automate,
// execute and link --refresh in that folder. ADR-024's accepted brownfield cost
// is malformed SPECS failing closed, not ordinary documentation disabling bulk
// commands.
//
// Admission and ownership are two independent questions and must not be
// collapsed into one predicate: "is this structurally a spec?" is answerable
// without knowing which id is being looked up; "which id does it own?" is
// answered by its body alone.
//
// The accepted limit (ADR-024 4a, recorded here rather than discovered later): a
// file named outside the envelope whose identity carrier is entirely absent or
// unreadable cannot be admitted, because BOTH available signals are exhausted --
// its name says nothing and no carrier can be detected in its leading region.
// Such a file is invisible to ownership even inside the selected scope. The
// amendment knowingly accepts this residual case; the alternative, admitting
// every in-scope .md, is the README refusal of REV-158 CLAUDE-002, and there is
// no third signal. BUG-185 closes on the carrier-DETECTABLE case only. The
// remainder is ENH-100 create-time uniqueness and ENH-101 read-side detection.
//
// Note the safety property that makes admission-before-relevance acceptable at
// all: every VALID spec has parseable frontmatter, therefore begins with a
// delimiter and carries the key, therefore is admitted by the carrier arm
// whatever its filename.
func isSpecShaped(base string, hasCarrier bool) bool {
	return specFilenameEnvelope.MatchString(base) || hasCarrier
}

// Resolution is the outcome of an ownership lookup, carrying everything a
// consumer needs to act, refuse, or diagnose.
type Resolution struct {
	Outcome            Outcome
	ID                 string
	OwnerLogicalRel    string   // UniqueOwner: the real owner's project-relative path
	OwnerCanonicalAbs  string   // UniqueOwner: canonical absolute path for reading/hashing
	OwnerFrontmatter   string   // UniqueOwner: the owner's frontmatter region as this snapshot read it
	OwnerMetadata      Metadata // UniqueOwner: normalized metadata from the same snapshot decode
	OwnerDecodeFailure *SchemaFailure
	AmbiguousEvidence  []OwnerEvidence
	Owners             []string // Ambiguous: sorted logical paths of every physical owner
	UnprovablePath     string   // Unprovable: candidate logical path
	Reason             UnprovableReason
	SchemaFailure      *SchemaFailure
}

// OwnerEvidence carries the one-read decoder result for one physical owner in
// an Ambiguous resolution. Readers use it without reopening or reparsing specs.
type OwnerEvidence struct {
	LogicalRel    string
	Metadata      Metadata
	DecodeFailure *SchemaFailure
}

// Resolve answers project-wide ownership for a requested bare TC id. Relevance
// for Unprovable candidates is the filename shortlist of id (MatchesSpecFile).
// Precedence: a relevant Unprovable candidate wins (fail closed), then owner
// count decides NotFound / UniqueOwner / Ambiguous.
func (s *Snapshot) Resolve(id string) Resolution {
	return s.resolve(id, func(c Candidate) bool {
		return c.BodyID == id || MatchesSpecFile(filepath.Base(c.LogicalRel), id)
	})
}

// ResolveScopedOwner answers ownership for an id a BULK operation selected by
// scope (ADR-024 5b "Relevance and outcomes in a bulk operation"). A bulk
// operation has no requested id, so section 4a's filename-shortlist arm is
// vacuous and only the scope arm applies: an Unprovable candidate is relevant
// only when it lies inside the selected scope, and those are diagnosed by
// SelectFolder, never here. This therefore decides on owner count alone --
// NotFound / UniqueOwner / Ambiguous (project-wide) -- and never returns
// Unprovable. It exists so an Unprovable file OUTSIDE the scope whose filename
// happens to match an in-scope owner's id cannot turn that owner into a silent
// absence (REV-184 CLAUDE-001). Per-id readers (single-TC forms, triage record
// attribution) keep using Resolve.
func (s *Snapshot) ResolveScopedOwner(id string) Resolution {
	return s.resolve(id, func(Candidate) bool { return false })
}

// resolve is the shared body. relevant reports whether an Unprovable candidate
// is relevant to this operation.
func (s *Snapshot) resolve(id string, relevant func(Candidate) bool) Resolution {
	res := Resolution{ID: id, Outcome: OutcomeNotFound}

	// Owners: candidates whose BODY identity equals id, deduplicated by canonical
	// path. NEVER pre-filtered by filename (REV-156 CODEX-001).
	ownersByCanonical := map[string]string{} // canonicalRel -> logicalRel (owner display)
	frontmatterByCanonical := map[string]string{}
	metadataByCanonical := map[string]Metadata{}
	decodeFailureByCanonical := map[string]*SchemaFailure{}
	for _, c := range s.candidates {
		if c.Unprovable || c.BodyID != id {
			continue
		}
		if _, ok := frontmatterByCanonical[c.CanonicalRel]; !ok {
			frontmatterByCanonical[c.CanonicalRel] = c.Frontmatter
			metadataByCanonical[c.CanonicalRel] = c.Metadata
			decodeFailureByCanonical[c.CanonicalRel] = c.DecodeFailure
		}
		// Prefer a non-alias logical path for display when both exist.
		existing, seen := ownersByCanonical[c.CanonicalRel]
		if !seen || (!c.IsAlias() && existingIsAlias(existing, c.CanonicalRel)) {
			ownersByCanonical[c.CanonicalRel] = c.LogicalRel
		}
	}

	// Relevant Unprovable candidates (fail closed before owner counting).
	var firstUnprovable *Candidate
	for i := range s.candidates {
		c := s.candidates[i]
		if !c.Unprovable {
			continue
		}
		if relevant(c) {
			firstUnprovable = &s.candidates[i]
			break
		}
	}
	if firstUnprovable != nil {
		res.Outcome = OutcomeUnprovable
		res.UnprovablePath = firstUnprovable.LogicalRel
		res.Reason = firstUnprovable.Reason
		res.SchemaFailure = cloneSchemaFailure(firstUnprovable.SchemaFailure)
		return res
	}

	switch len(ownersByCanonical) {
	case 0:
		res.Outcome = OutcomeNotFound
	case 1:
		res.Outcome = OutcomeUniqueOwner
		for canonRel, logRel := range ownersByCanonical {
			res.OwnerLogicalRel = logRel
			res.OwnerCanonicalAbs = filepath.Join(s.projectRoot, filepath.FromSlash(canonRel))
			res.OwnerFrontmatter = frontmatterByCanonical[canonRel]
			res.OwnerMetadata = cloneMetadata(metadataByCanonical[canonRel])
			res.OwnerDecodeFailure = cloneSchemaFailure(decodeFailureByCanonical[canonRel])
		}
	default:
		res.Outcome = OutcomeAmbiguous
		owners := make([]string, 0, len(ownersByCanonical))
		for canonRel, logRel := range ownersByCanonical {
			owners = append(owners, logRel)
			res.AmbiguousEvidence = append(res.AmbiguousEvidence, OwnerEvidence{
				LogicalRel:    logRel,
				Metadata:      cloneMetadata(metadataByCanonical[canonRel]),
				DecodeFailure: cloneSchemaFailure(decodeFailureByCanonical[canonRel]),
			})
		}
		sort.Strings(owners)
		sort.Slice(res.AmbiguousEvidence, func(i, j int) bool {
			return res.AmbiguousEvidence[i].LogicalRel < res.AmbiguousEvidence[j].LogicalRel
		})
		res.Owners = owners
	}
	return res
}

// existingIsAlias reports whether the currently-stored owner display path for a
// canonical target is an alias (differs from the canonical target's own path).
func existingIsAlias(existingLogical, canonicalRel string) bool {
	return existingLogical != canonicalRel
}

// KnownIDs returns every provable body identity in the snapshot, sorted and
// deduplicated. It is the set of identities that DEMONSTRABLY exist in this
// project.
//
// Its purpose is disambiguating a filename that could grammatically belong to
// more than one id -- "tc-x--legacy--bats" reads as either (tc-x, legacy--bats)
// or (tc-x--legacy, bats). Knowing that tc-x--legacy exists settles it. Callers
// use it to decide RELEVANCE for records whose own body cannot be read; it never
// establishes identity for a record that can be read, which stays body-authoritative
// (ADR-024 s1).
func (s *Snapshot) KnownIDs() []string {
	seen := map[string]bool{}
	var ids []string
	for _, c := range s.candidates {
		if c.Unprovable || c.BodyID == "" || seen[c.BodyID] {
			continue
		}
		seen[c.BodyID] = true
		ids = append(ids, c.BodyID)
	}
	sort.Strings(ids)
	return ids
}

// SelectFolder returns the body ids of the REAL physical owners whose logical
// path lies within scopeRel (recursive: any descendant; shallow: a direct
// child), and the Unprovable candidates whose logical path lies in the same
// scope. scopeRel is the project-relative, forward-slash path of the ALREADY
// canonically-resolved scope directory (so a directory alias is resolved and
// host case-sensitivity is decided by the OS before this call). A spec-file
// symlink (IsAlias) never confers its target's membership.
func (s *Snapshot) SelectFolder(scopeRel string, recursive bool) (ids []string, unprovable []Candidate) {
	scopeRel = s.resolveScopeSpelling(scopeRel)
	idSet := map[string]bool{}
	for _, c := range s.candidates {
		if !logicalInScope(c.LogicalRel, scopeRel, recursive) {
			continue
		}
		if c.Unprovable {
			unprovable = append(unprovable, c)
			continue
		}
		if c.IsAlias() {
			continue // a spec-file symlink is not a physical owner of this scope
		}
		idSet[c.BodyID] = true
	}
	for id := range idSet {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, unprovable
}

// resolveScopeSpelling returns scopeRel in the REAL on-disk spelling of the
// directory it names, deciding by FILESYSTEM IDENTITY rather than byte equality
// (ADR-024 s3; REV-159 CLAUDE-002).
//
// The defect this closes. Membership used to be a raw strings.HasPrefix, on the
// reasoning that host case-sensitivity was "already decided by the OS resolution
// of scopeRel". That is true only on Windows. scopeRel reaches SelectFolder
// through ResolveFolder -> pathsafe.ResolveUnderRoot / CanonicalRoot, both ending
// in filepath.EvalSymlinks -- and EvalSymlinks applies the real on-disk case
// (toNorm) only in symlink_windows.go. On darwin it is a plain walkSymlinks that
// preserves whatever the user typed, while candidate LogicalRel values come from
// filepath.Walk and always carry the real on-disk case. So on the macOS default
// case-insensitive filesystem, with a folder named FeatureCase, `gtms delete
// featurecase --dry-run` resolved the folder successfully and then matched NO
// candidate: delete and reset reported nothing to do and exited 0, while
// automate, execute and link --refresh failed with "No test cases found".
//
// Neither plain string equality nor unconditional case folding is sufficient
// (ADR-024 s3 says so in as many words), so this asks the filesystem instead. The
// walk already recorded every directory in its real spelling; os.SameFile decides
// whether the requested scope IS one of them:
//
//   - On a case-INSENSITIVE filesystem, featurecase and FeatureCase are the same
//     inode, SameFile matches, and the real spelling is recovered.
//   - On a case-SENSITIVE filesystem they are different directories (or one does
//     not exist), nothing matches, and scopeRel is returned untouched -- so the
//     two remain distinct scopes, exactly as before.
//
// The result is the same decision the host itself would make, with no EqualFold
// and no runtime.GOOS test (REV-156 CLAUDE-004).
//
// Cost is bounded and per-OPERATION, not per-candidate: an exact hit short-
// circuits before any stat, and only the fallback stats the directory set.
func (s *Snapshot) resolveScopeSpelling(scopeRel string) string {
	scopeRel = strings.TrimSuffix(filepath.ToSlash(scopeRel), "/")
	if scopeRel == "" {
		return scopeRel
	}
	// Fast path: the spelling already matches a directory the walk saw. This is
	// every case-sensitive host, and every correctly-cased request anywhere.
	for _, d := range s.dirs {
		if d == scopeRel {
			return scopeRel
		}
	}
	scopeInfo, err := os.Stat(filepath.Join(s.projectRoot, filepath.FromSlash(scopeRel)))
	if err != nil {
		return scopeRel // absent or unreadable: selects nothing, as before
	}
	for _, d := range s.dirs {
		info, statErr := os.Stat(filepath.Join(s.projectRoot, filepath.FromSlash(d)))
		if statErr == nil && os.SameFile(scopeInfo, info) {
			return d
		}
	}
	return scopeRel
}

// logicalInScope reports whether logicalRel is a member of scopeRel. scopeRel
// must already carry the real on-disk spelling (resolveScopeSpelling), so the
// comparison here is a plain prefix test over two strings the filesystem has
// already agreed name the same directory.
func logicalInScope(logicalRel, scopeRel string, recursive bool) bool {
	if scopeRel == "" {
		return false
	}
	if !strings.HasPrefix(logicalRel, scopeRel+"/") {
		return false
	}
	if recursive {
		return true
	}
	// Shallow: the entry is a DIRECT child of scopeRel.
	rest := logicalRel[len(scopeRel)+1:]
	return !strings.Contains(rest, "/")
}

// canonicalScopeRel resolves a cases-relative folder to its canonical
// project-relative form (following a directory alias to its real directory) and
// reports ok=false for an out-of-tree or absent folder.
func (s *Snapshot) canonicalScopeRel(projectRoot, folder string) (string, bool) {
	abs, err := ResolveFolder(projectRoot, folder)
	if err != nil {
		return "", false
	}
	rel, relErr := filepath.Rel(s.projectRoot, abs)
	if relErr != nil {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// RequireOwnership returns a typed OwnershipError when id is Ambiguous or
// Unprovable, and nil for UniqueOwner or NotFound. It is the shared pre-mutation
// / pre-invocation guard for a single-TC mutating consumer (automate, execute,
// prime, triage): NotFound is left to each command's own existence wording.
func (s *Snapshot) RequireOwnership(id string) error {
	res := s.Resolve(id)
	if res.Outcome == OutcomeAmbiguous || res.Outcome == OutcomeUnprovable {
		return OwnershipErrorFor(res)
	}
	return nil
}

// RequireFolderOwnership refuses a bulk mutating operation over scopeRel (a
// canonical project-relative folder) when any in-scope candidate is Unprovable or
// any selected id is Ambiguous/Unprovable project-wide. scopeRel must already be
// canonically resolved (directory aliases followed; host case applied).
func (s *Snapshot) RequireFolderOwnership(scopeRel string, recursive bool) error {
	ids, unprovable := s.SelectFolder(scopeRel, recursive)
	if len(unprovable) > 0 {
		return UnprovableInScope(scopeRel, unprovable[0])
	}
	for _, id := range ids {
		if err := s.RequireOwnership(id); err != nil {
			return err
		}
	}
	return nil
}

// hasLogicalEntryUnderScope reports whether some candidate with body identity id
// (a real owner OR an in-tree alias to one) has a LOGICAL path directly or
// recursively under scopeRel (a canonical project-relative folder). It answers
// whether a qualified target is reachable within the requested folder, without
// granting mutation authority (that is SelectFolder's stricter, alias-excluding
// job).
func (s *Snapshot) hasLogicalEntryUnderScope(id, scopeRel string) bool {
	// Same host case semantics as SelectFolder: a folder-qualified target must not
	// stop being reachable merely because the user typed a different case on a
	// case-insensitive filesystem (ADR-024 s3).
	prefix := s.resolveScopeSpelling(scopeRel) + "/"
	for _, c := range s.candidates {
		if c.BodyID != id {
			continue
		}
		if strings.HasPrefix(c.LogicalRel, prefix) {
			return true
		}
	}
	return false
}

// OwnershipError is the typed, centrally-sanitised diagnostic a consumer returns
// when ownership is not uniquely proven (ADR-024 s7). Callers render it once;
// they do not reproduce raw formatting.
type OwnershipError struct {
	// ID is the TC id the operation was about. REV-158 CLAUDE-007: it holds a TC
	// id or nothing. A candidate found by SCOPE rather than by id has no id to
	// report -- that is why it is unprovable -- so ID stays empty and Scope
	// carries the folder instead.
	ID            string
	Outcome       Outcome
	Owners        []string         // Ambiguous
	Path          string           // Unprovable
	Scope         string           // Unprovable found by scope: the folder that selected it
	Reason        UnprovableReason // Unprovable
	SchemaFailure *SchemaFailure
}

// UnprovableInScope builds the diagnostic for a candidate that an operation
// selected because it lies in the requested SCOPE, not because its filename or
// body matched a requested id.
//
// REV-158 CLAUDE-007: the four call sites for this used to build the error with
// ID set to the candidate's PATH, so Error() rendered "ownership of test case ID
// 'gtms/test/cases/feat/README.md' cannot be proved from
// gtms/test/cases/feat/README.md" -- labelling a path as a TC id and then
// repeating it, which buries the repair sentence and means a user grepping for a
// TC id finds a path. ADR-024 s7 requires typed diagnostics whose fields carry
// the right facts.
func UnprovableInScope(scopeRel string, c Candidate) *OwnershipError {
	return &OwnershipError{
		ID:            c.BodyID,
		Outcome:       OutcomeUnprovable,
		Path:          c.LogicalRel,
		Scope:         strings.TrimSuffix(filepath.ToSlash(scopeRel), "/"),
		Reason:        c.Reason,
		SchemaFailure: c.SchemaFailure,
	}
}

// Error renders a non-empty, actionable diagnostic with control characters
// sanitised out of the id and paths.
func (e *OwnershipError) Error() string {
	switch e.Outcome {
	case OutcomeAmbiguous:
		display := make([]string, len(e.Owners))
		for i, p := range e.Owners {
			display[i] = sanitizeIdentityString(p)
		}
		return fmt.Sprintf("test case ID '%s' is ambiguous -- it has %d project-wide owners:\n    %s\n    Resolve the duplicate IDs before targeting '%s'.",
			sanitizeIdentityString(e.ID), len(e.Owners), strings.Join(display, "\n    "), sanitizeIdentityString(e.ID))
	case OutcomeUnprovable:
		repair := repairSentence(e.Reason, sanitizeIdentityString(e.Path))
		if e.SchemaFailure != nil {
			repair = schemaRepairSentence(e.SchemaFailure, sanitizeIdentityString(e.Path))
		}
		// A scope-derived candidate has no id: say so, and name the scope that
		// selected it, instead of labelling its path as a TC id (CLAUDE-007). The
		// "cannot be proved from <path>: <repair>" clause is identical in both
		// forms, so the repair sentence reads the same wherever it is surfaced.
		if e.ID == "" {
			scope := e.Scope
			if scope == "" {
				scope = layout.Current().TestCases
			}
			return fmt.Sprintf("ownership of a candidate spec under %s/ cannot be proved from %s: %s.",
				sanitizeIdentityString(scope), sanitizeIdentityString(e.Path), repair)
		}
		return fmt.Sprintf("ownership of test case ID '%s' cannot be proved from %s: %s.",
			sanitizeIdentityString(e.ID), sanitizeIdentityString(e.Path), repair)
	case OutcomeNotFound:
		return fmt.Sprintf("test case '%s' not found under %s/", sanitizeIdentityString(e.ID), layout.Current().TestCases)
	default:
		return fmt.Sprintf("test case '%s' could not be resolved", sanitizeIdentityString(e.ID))
	}
}

// OwnershipErrorFor builds an OwnershipError from a non-unique Resolution, or nil
// for UniqueOwner.
func OwnershipErrorFor(res Resolution) *OwnershipError {
	switch res.Outcome {
	case OutcomeUniqueOwner:
		return nil
	case OutcomeAmbiguous:
		return &OwnershipError{ID: res.ID, Outcome: res.Outcome, Owners: res.Owners}
	case OutcomeUnprovable:
		return &OwnershipError{ID: res.ID, Outcome: res.Outcome, Path: res.UnprovablePath, Reason: res.Reason, SchemaFailure: res.SchemaFailure}
	default:
		return &OwnershipError{ID: res.ID, Outcome: OutcomeNotFound}
	}
}

// SanitizeIdentityString is the shared rendering-boundary sanitiser ADR-024 s7
// requires: "Identity errors carry structured fields and sanitise untrusted IDs
// and paths at their CENTRAL rendering boundary. Callers do not reproduce raw
// formatting."
//
// It is exported so a typed identity diagnostic in another package renders
// through THIS function rather than growing a second copy of the rule -- see
// link.WiringIdentityMismatchError (REV-159 CODEX-005). One implementation is
// the point; a duplicate is how the two drift apart.
func SanitizeIdentityString(s string) string { return sanitizeIdentityString(s) }

// sanitizeIdentityString replaces control characters (C0 + DEL) with '?' so a
// filesystem-derived id or path echoed in a diagnostic cannot manipulate the
// terminal. It does not truncate: a legitimate spec path can be long.
func sanitizeIdentityString(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r == 0x7F {
			b.WriteRune('?')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
