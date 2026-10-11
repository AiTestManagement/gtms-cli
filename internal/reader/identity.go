package reader

// ENH-205 / ADR-024 section 5b: the shared identity resolution for a migrated bulk
// read/list operation. One snapshot per operation resolves every in-scope test
// case's ownership, and the operation renders only PROVEN, unique, decodable owners
// while surfacing every affected identity as a diagnostic -- never as absence.
//
// This replaces the identity/enumeration half of the pre-migration scanTestCases
// walk. The snapshot supplies identity, strict schema verdict, and normalized
// display fields from its single read and parse. A legacy display-field failure
// during the migration window is reported and counted exactly as an Unprovable
// candidate at the same path (ADR-024 5b).

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/output"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
)

// IdentityDiagnostics carries every affected identity a bulk operation must
// surface under ADR-024 5b policy (e): an operation with any affected identity
// emits these on stderr and exits non-zero, while its normal output holds only the
// proven entries.
type IdentityDiagnostics struct {
	// ambiguous: an ID with more than one physical owner project-wide, with at
	// least one owner's non-alias path in the selected scope. Owners are the
	// project-wide logical paths (in and out of scope).
	ambiguous []ambiguousDiag
	// pathIssues: in-scope Unprovable candidates AND display-field decode failures,
	// each carrying a pre-rendered, sanitised message and its path for sorting.
	pathIssues []pathIssue
}

type ambiguousDiag struct {
	id       string
	owners   []string
	evidence []testcase.OwnerEvidence
}

type pathIssue struct {
	path    string
	message string
}

// Affected reports whether any identity could not be resolved to a single proven,
// decodable owner in scope.
func (d IdentityDiagnostics) Affected() bool {
	return len(d.ambiguous) > 0 || len(d.pathIssues) > 0
}

// count is the number of affected entries left out of the normal output.
func (d IdentityDiagnostics) count() int { return len(d.ambiguous) + len(d.pathIssues) }

// Render writes the per-identity diagnostics and the closing line to w, in the
// stable order ADR-024 5b point 3 requires: Ambiguous by ID, then Unprovable and
// display-field failures by path. Ambiguous/Unprovable text comes from the central
// testcase.OwnershipError rendering (section 7 sanitisation); display-field text is
// sanitised through the same central function. Returns the number of affected
// entries named.
func (d IdentityDiagnostics) Render(w io.Writer) int {
	amb := append([]ambiguousDiag(nil), d.ambiguous...)
	sort.Slice(amb, func(i, j int) bool { return amb[i].id < amb[j].id })
	for _, a := range amb {
		oe := &testcase.OwnershipError{ID: a.id, Outcome: testcase.OutcomeAmbiguous, Owners: a.owners}
		output.FprintError(w, oe.Error(), "")
	}
	issues := append([]pathIssue(nil), d.pathIssues...)
	sort.Slice(issues, func(i, j int) bool { return issues[i].path < issues[j].path })
	for _, p := range issues {
		output.FprintError(w, p.message, "")
	}
	n := d.count()
	noun := "test cases"
	if n == 1 {
		noun = "test case"
	}
	output.FprintError(w,
		fmt.Sprintf("%d %s could not be read; the output above leaves them out", n, noun),
		"Repair the files named above and rerun.")
	return n
}

// IncompleteReadError is returned by the bulk read entry points that do not hand
// IdentityDiagnostics back to their caller (PipelineStatus, PipelineFolderSummary,
// FolderTriagedCounts, Map, Gaps, GapsFolderSummary, ListTriage) when any identity
// in scope is affected. ADR-024 5b forbids presenting an affected identity as
// absence, so those entry points return no rows and this typed error instead of
// partial rows as success (REV-184 CODEX-004). A caller that can render partial
// output under policy (e) uses the matching ...WithDiagnostics form.
type IncompleteReadError struct {
	Diagnostics IdentityDiagnostics
}

// Error names every affected identity, in the same stable order and with the same
// central sanitisation as Render, on one diagnostic per line.
func (e *IncompleteReadError) Error() string {
	d := e.Diagnostics
	amb := append([]ambiguousDiag(nil), d.ambiguous...)
	sort.Slice(amb, func(i, j int) bool { return amb[i].id < amb[j].id })
	issues := append([]pathIssue(nil), d.pathIssues...)
	sort.Slice(issues, func(i, j int) bool { return issues[i].path < issues[j].path })

	n := d.count()
	noun := "test cases"
	if n == 1 {
		noun = "test case"
	}
	lines := []string{fmt.Sprintf("read incomplete: %d %s could not be read", n, noun)}
	for _, a := range amb {
		oe := &testcase.OwnershipError{ID: a.id, Outcome: testcase.OutcomeAmbiguous, Owners: a.owners}
		lines = append(lines, oe.Error())
	}
	for _, p := range issues {
		lines = append(lines, p.message)
	}
	return strings.Join(lines, "\n")
}

// incompleteReadErr returns an *IncompleteReadError when d is affected, else nil.
func incompleteReadErr(d IdentityDiagnostics) error {
	if !d.Affected() {
		return nil
	}
	return &IncompleteReadError{Diagnostics: d}
}

// scopeRelForScope derives the canonical project-relative scope path and recursion
// flag for SelectFolder from a reader ScopeInfo. A nil scope selects the whole
// cases tree, recursively. SelectFolder itself resolves the on-disk spelling
// (host case-sensitivity), so a plain project-relative path is sufficient here.
func scopeRelForScope(projectRoot string, scope *ScopeInfo) (scopeRel string, recursive bool) {
	if scope == nil {
		rel, err := filepath.Rel(projectRoot, layout.TestCasesDir(projectRoot))
		if err != nil {
			return layout.Current().TestCases, true
		}
		return filepath.ToSlash(rel), true
	}
	return strings.TrimSuffix(filepath.ToSlash(scope.RelPath), "/"), scope.Recursive
}

// resolveScopedSpecs resolves identity for a bulk operation from ONE snapshot and
// the selected scope. It returns the proven, unique, display-decodable owners as
// testCaseFrontmatter (with SourceFile set to the owner's absolute path, ready for
// the existing entry-building), plus the diagnostics for every affected identity.
//
// Relevance is the SCOPE arm only (ADR-024 5b "Relevance and outcomes in a bulk
// operation"): the in-scope Unprovable candidates come from SelectFolder and are
// diagnosed by path; each selected id is then decided on owner count alone
// (Snapshot.ResolveScopedOwner). The per-id filename shortlist of Snapshot.Resolve
// does NOT apply here: with it, an unreadable file OUTSIDE the scope whose name
// matched an in-scope owner's id turned that owner into a silent absence --
// dropped from every row and count with no diagnostic and exit 0 (REV-184
// CLAUDE-001). An out-of-scope Unprovable candidate does not affect a bulk
// operation, so the owner is rendered.
func resolveScopedSpecs(snap *testcase.Snapshot, projectRoot string, scope *ScopeInfo) ([]testCaseFrontmatter, IdentityDiagnostics, error) {
	scopeRel, recursive := scopeRelForScope(projectRoot, scope)
	scopeName := strings.TrimSuffix(scopeRel, "/")

	ids, unprovable := snap.SelectFolder(scopeRel, recursive)

	var diags IdentityDiagnostics
	for _, c := range unprovable {
		diags.addPathIssue(c.LogicalRel, testcase.UnprovableInScope(scopeName, c).Error())
	}

	specs := make([]testCaseFrontmatter, 0, len(ids))
	for _, id := range ids {
		r := snap.ResolveScopedOwner(id)
		switch r.Outcome {
		case testcase.OutcomeUniqueOwner:
			// Schema integration does not move the post-ENH-205 boundary: this
			// bulk read consumes the shared snapshot's one-read, one-parse result.
			// It adds no cases-tree traversal, resolver, or owner decision.
			tc, badField := testCaseFromNormalized(r.OwnerMetadata, r.OwnerDecodeFailure)
			if badField != "" {
				diags.addPathIssue(r.OwnerLogicalRel, renderDisplayFieldError(r.OwnerLogicalRel, badField))
				continue
			}
			// SourceFile drives folder attribution (deriveFolderName) and slug
			// derivation, which expect a path under the RAW project root -- the
			// snapshot's canonical abs can diverge from it (EvalSymlinks, 8.3 names).
			// Use the owner's non-alias logical path joined to the raw root, so the
			// owner is attributed to its real folder (tc-7607ce1a) and folder names
			// resolve as before. Display-field decode already read the canonical abs.
			tc.SourceFile = filepath.Join(projectRoot, filepath.FromSlash(r.OwnerLogicalRel))
			tc.ID = id
			specs = append(specs, *tc)
		case testcase.OutcomeAmbiguous:
			diags.ambiguous = append(diags.ambiguous, ambiguousDiag{id: id, owners: r.Owners, evidence: r.AmbiguousEvidence})
		case testcase.OutcomeUnprovable:
			// ResolveScopedOwner never returns Unprovable today. Should that change,
			// fail closed: diagnose the id rather than drop it silently.
			diags.addUnprovableResolution(r)
		default:
			// NotFound cannot occur for an id SelectFolder returned as a proven
			// in-scope owner of the same snapshot; ignore defensively rather than
			// guess.
		}
	}

	sort.Slice(specs, func(i, j int) bool { return specs[i].ID < specs[j].ID })
	return specs, diags, nil
}

// addPathIssue records a path-keyed diagnostic once per path: the same file can be
// reached both as an in-scope Unprovable candidate and as the Unprovable
// resolution of an id, and ADR-024 5b point 3 names each affected entry once.
func (d *IdentityDiagnostics) addPathIssue(path, message string) {
	for _, p := range d.pathIssues {
		if p.path == path {
			return
		}
	}
	d.pathIssues = append(d.pathIssues, pathIssue{path: path, message: message})
}

// addUnprovableResolution diagnoses an id whose per-id resolution is Unprovable,
// via the central OwnershipError rendering, deduplicated against the path issues
// already recorded (an in-scope Unprovable candidate at the same path).
func (d *IdentityDiagnostics) addUnprovableResolution(r testcase.Resolution) {
	d.addPathIssue(r.UnprovablePath, testcase.OwnershipErrorFor(r).Error())
}

// renderDisplayFieldError builds the sanitised diagnostic for a display-field
// decode failure. It is its own reader-level line (not an OwnershipError and not a
// Resolution outcome, per ADR-024 5b), but path and field are sanitised through the
// same central function section 7 uses.
func renderDisplayFieldError(path, field string) string {
	return fmt.Sprintf("test case %s cannot be read: field '%s' has the wrong type",
		testcase.SanitizeIdentityString(path), testcase.SanitizeIdentityString(field))
}

func testCaseFromNormalized(metadata testcase.Metadata, failure *testcase.SchemaFailure) (*testCaseFrontmatter, string) {
	if failure != nil {
		if failure.Key != "" {
			return nil, failure.Key
		}
		return nil, "frontmatter"
	}
	return &testCaseFrontmatter{
		ID:          strings.ToLower(metadata.TestCaseID),
		Title:       metadata.Title,
		Requirement: metadata.Requirement,
		Priority:    metadata.Priority,
		Type:        metadata.TestType,
		Status:      metadata.Status,
		Tags:        append([]string(nil), metadata.Tags...),
		Created:     metadata.Created,
	}, ""
}
