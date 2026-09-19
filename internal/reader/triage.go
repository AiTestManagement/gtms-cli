package reader

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
	"github.com/aitestmanagement/gtms-cli/internal/triage"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// ENH-200 / CON-040:
//
// Triage is a generic classifier. `gtms triage <tc> --value <key>` writes one
// structured YAML record (internal/triage) for the resolved (test case,
// framework) pair, snapshotting the latest terminal execution result.
// `--resolve` marks that record resolved without deleting it. Neither operation
// mutates a spec, wiring, an automation record, or an execution handoff, and no
// value carries behaviour: the old action-bearing category model (task
// creation, gtms/triage-history Markdown appends, ValidTriageCategories) is
// gone.

// ClassifyTriage writes (or reactivates and replaces) the triage record for the
// latest terminal result of one (test case, framework) pair.
//
// values is the resolved permitted-value set from config.ResolveTriageValues.
// The precondition order is deliberate and matches the acceptance specs:
//
//  0. ADR-024: ownership must be uniquely proven (Ambiguous/Unprovable refuse
//     before any hash or write);
//  1. value must be a configured key;
//  2. framework must resolve through wiring (a missing --framework on a
//     multi-framework TC is an ambiguity error naming the candidates);
//  3. the test-case spec must resolve and be readable (its hash is captured);
//  4. a terminal execution result must exist to classify.
//
// Only after every precondition passes is any existing record strict-read (so a
// record this binary cannot understand blocks the overwrite) and the new record
// written atomically.
func ClassifyTriage(projectRoot, testCaseID, value, summary, defect, framework string, values map[string]string) (*triage.Record, error) {
	// 0. ADR-024 (tc-af0ee202): triage classification writes a record attributed to
	// an identity and hashes that identity's spec, so it is a mutating consumer by
	// EFFECT even though it lives in a reader package. Refuse an Ambiguous or
	// Unprovable id BEFORE hashing the spec and before writing any triage record.
	snap, err := testcase.NewSnapshot(projectRoot)
	if err != nil {
		return nil, err
	}
	// ADR-024 s5: keep the resolved owner. Step 3 below hashes THIS spec. It used
	// to prove ownership here, discard the answer, and then ask
	// pipeline.ResolveTestCaseSpec to find the spec again -- a second, independent
	// identity decision on the write path of a record attributed to that identity.
	owner, oerr := snap.RequireOwner(testCaseID)
	if oerr != nil && !errors.Is(oerr, testcase.ErrOwnerNotFound) {
		return nil, oerr
	}

	// 1. Value must be configured.
	if _, ok := values[value]; !ok {
		return nil, fmt.Errorf("'%s' is not a configured triage value. Configured values: %s", value, sortedKeys(values))
	}

	// 2. Resolve framework through wiring (ambiguity names candidates).
	resolvedFramework, err := resolveTriageFramework(projectRoot, testCaseID, framework)
	if err != nil {
		return nil, err
	}
	framework = resolvedFramework

	// 3. Hash the resolved owner's spec. NotFound surfaces here, keeping the
	// existing "could not be found or read" wording for a missing spec.
	if owner == nil {
		return nil, fmt.Errorf("cannot classify '%s': its test-case specification could not be found or read: %w", testCaseID, oerr)
	}
	testcaseHash, err := pipeline.HashFile(owner.SpecAbs())
	if err != nil {
		return nil, fmt.Errorf("cannot classify '%s': its test-case specification could not be read: %w", testCaseID, err)
	}

	// 4. Select the latest terminal result for the pair. scanTerminalResultsForTC
	// loads every wiring record for the TC so the ENH-146 join ladder sees the
	// full sibling set (do not narrow it -- see the function's docstring).
	overlay, oErr := scanTerminalResultsForTC(projectRoot, testCaseID)
	if oErr != nil {
		return nil, fmt.Errorf("scanning terminal results: %w", oErr)
	}
	hit, ok := overlay[overlayKey(testCaseID, framework)]
	if !ok {
		return nil, fmt.Errorf("no execution result is available to classify for '%s' (framework: %s). Run 'gtms execute %s' first.", testCaseID, framework, testCaseID)
	}

	// Guard: if a record already exists, strict-read it first so a record with
	// an unsupported version or an unknown field blocks the overwrite rather
	// than being silently replaced (tc-fffa7eea step 11).
	path, err := triage.Path(projectRoot, testCaseID, framework)
	if err != nil {
		return nil, err
	}
	// BUG-187: Lstat, not Stat, so a symlink or DANGLING link at the target is
	// seen as PRESENT and strict-read (where triage.Read refuses it by name)
	// rather than reported absent -- os.Stat follows a link and reports a dangling
	// one as absent, which would skip this guard and let the writer run.
	//
	// Three outcomes, kept distinct. Only fs.ErrNotExist means "no record here,
	// go ahead and create one". Any OTHER error means GTMS could not determine
	// what is there, and an overwrite guard that cannot see its target must fail
	// closed rather than wave the writer through.
	switch _, statErr := os.Lstat(path); {
	case statErr == nil:
		if _, readErr := triage.Read(projectRoot, path); readErr != nil {
			return nil, readErr
		}
	case !errors.Is(statErr, fs.ErrNotExist):
		return nil, fmt.Errorf("cannot classify '%s' (framework: %s): the existing record could not be inspected: %w",
			testCaseID, framework, statErr)
	}

	rec := &triage.Record{
		Version:      triage.SchemaVersion,
		TestCaseID:   testCaseID,
		Framework:    framework,
		Status:       triage.StatusActive,
		Value:        value,
		Summary:      summary,
		Defect:       defect,
		ClassifiedAt: time.Now().UTC().Format(time.RFC3339),
		Snapshot: triage.Snapshot{
			Task:                 hit.Task,
			Result:               hit.RawResult,    // raw contract vocabulary (skip, not skipped)
			CompletedAt:          hit.RawCompleted, // handoff `completed` verbatim; empty -> omitted
			Artefact:             hit.Artefact,
			ArtefactHash:         hit.ArtefactHash, // handoff hash only; never wiring
			TestcaseHashAtTriage: testcaseHash,
		},
	}
	if _, err := triage.Write(projectRoot, rec); err != nil {
		return nil, err
	}
	return rec, nil
}

// ResolveTriage marks an existing triage record resolved. It depends only on a
// valid test case ID and an existing record: no configuration, spec, wiring,
// automation record, handoff, artefact, or hash is consulted, so a fresh clone
// whose transient handoffs are absent can still close tracked classifications.
//
// With framework set, the exact record is selected. Without it, the sole record
// is resolved; multiple records require --framework, and no record is a clear
// error. Resolution is idempotent: an already-resolved record is returned
// unchanged (alreadyResolved=true) and the file is not rewritten, so repeated
// resolution is byte-for-byte stable.
func ResolveTriage(projectRoot, testCaseID, framework string) (rec *triage.Record, alreadyResolved bool, err error) {
	path, err := selectTriageRecordPath(projectRoot, testCaseID, framework)
	if err != nil {
		return nil, false, err
	}

	rec, err = triage.Read(projectRoot, path)
	if err != nil {
		return nil, false, err
	}

	if rec.Status == triage.StatusResolved {
		// Idempotent: leave the file byte-identical.
		return rec, true, nil
	}

	rec.Status = triage.StatusResolved
	rec.ResolvedAt = time.Now().UTC().Format(time.RFC3339)
	if _, err := triage.Write(projectRoot, rec); err != nil {
		return nil, false, err
	}
	return rec, false, nil
}

// selectTriageRecordPath selects the on-disk path of exactly one triage record
// for (testCaseID, framework) using the shared selection rule, so the write
// surface (--resolve) and the read surface (--show) cannot diverge (ENH-201
// item 13):
//
//   - with framework: the exact record, active or resolved; a missing file is a
//     named no-record error naming the pair.
//   - without framework: the sole record; several records is an ambiguity error
//     naming the candidates (sorted, deterministic); none is a named no-record
//     error naming the test case.
//
// It reads no spec, wiring, or handoff -- selection is independent of execution
// evidence.
func selectTriageRecordPath(projectRoot, testCaseID, framework string) (string, error) {
	if framework != "" {
		path, err := triage.Path(projectRoot, testCaseID, framework)
		if err != nil {
			return "", err
		}
		// BUG-187: Lstat, not Stat. A dangling or symlinked entry EXISTS and must
		// reach triage.Read (which refuses it by name), not be reported as an
		// absent record; only a genuinely absent path yields the no-record error.
		//
		// "Absent" means fs.ErrNotExist and nothing else. A permission or
		// not-a-directory error says GTMS could not look, which is a different
		// fact from there being nothing to find -- reporting it as "no triage
		// record exists" would send the reader hunting for a missing file that is
		// actually sitting there, unreadable.
		if _, statErr := os.Lstat(path); statErr != nil {
			if errors.Is(statErr, fs.ErrNotExist) {
				return "", fmt.Errorf("no triage record exists for '%s' (framework: %s).", testCaseID, framework)
			}
			return "", fmt.Errorf("cannot read the triage record for '%s' (framework: %s): %w",
				testCaseID, framework, statErr)
		}
		return path, nil
	}

	refs, listErr := triage.ListForTC(projectRoot, testCaseID)
	if listErr != nil {
		return "", listErr
	}
	switch len(refs) {
	case 0:
		return "", fmt.Errorf("no triage record exists for '%s'.", testCaseID)
	case 1:
		return refs[0].Path, nil
	default:
		// BUG-187: ListForTC derives these framework names by trimming REPOSITORY
		// FILENAMES, so they are attacker-influencable content, not GTMS-chosen
		// labels. Sanitize each before it reaches a terminal, exactly as the
		// record path is sanitized -- otherwise a crafted
		// tc-x--<ESC>...triage.yaml injects control characters through this
		// diagnostic. Sorting AFTER sanitizing keeps the displayed order stable
		// with what is displayed.
		frameworks := make([]string, 0, len(refs))
		for _, r := range refs {
			frameworks = append(frameworks, pathsafe.SanitizeControlChars(r.Framework))
		}
		sort.Strings(frameworks)
		return "", fmt.Errorf("multiple triage records exist for '%s' (%s). Specify --framework.",
			testCaseID, strings.Join(frameworks, ", "))
	}
}

// resolveTriageFramework resolves the framework for a classification. A given
// framework is returned as-is. When empty, it is auto-detected from the
// candidates for the TC: the wiring records PLUS the manual framework when proven
// manual execution evidence exists (BUG-193 Fix Direction rule 3). Zero
// candidates is a no-wiring error, one is used, and more than one is an ambiguity
// error naming every candidate (including manual) so the caller can pick with
// --framework.
//
// Manual is a candidate only on PROVEN execution evidence (a terminal manual
// execute handoff whose artefact proves out), never on the mere presence of a
// primed manual template -- a primed-only case therefore has no manual candidate
// and refuses like any un-executed pair.
func resolveTriageFramework(projectRoot, testCaseID, framework string) (string, error) {
	if framework != "" {
		return framework, nil
	}
	recs, err := wiring.FindAllForTC(projectRoot, testCaseID)
	if err != nil {
		return "", fmt.Errorf("reading wiring records: %w", err)
	}

	// Build the candidate set: wiring frameworks plus a proven manual claim.
	// Sanitize each (the wiring names are trimmed from repository filenames),
	// dedupe (a manual WIRING record and the manual claim must not double-list
	// "manual"), and sort for deterministic diagnostics.
	seen := make(map[string]bool)
	candidates := make([]string, 0, len(recs)+1)
	add := func(fw string) {
		fw = pathsafe.SanitizeControlChars(fw)
		if fw == "" || seen[fw] {
			return
		}
		seen[fw] = true
		candidates = append(candidates, fw)
	}
	for _, r := range recs {
		add(r.Framework)
	}
	if _, ok := manualClaimForTC(projectRoot, testCaseID); ok {
		add("manual")
	}
	sort.Strings(candidates)

	switch len(candidates) {
	case 0:
		// BUG-193 / REV-170 CLAUDE-002: name both routes to execution evidence.
		// The leading phrase is kept because sibling suites pin it; the manual
		// route is added so a primed-but-unexecuted manual case is not told to
		// automate.
		return "", fmt.Errorf("no wiring record found for '%s' and no manual execution recorded. Automate and execute the test case, or record a manual result with 'gtms execute %s --adapter manual-execute'.", testCaseID, testCaseID)
	case 1:
		return candidates[0], nil
	default:
		return "", fmt.Errorf("multiple frameworks exist for '%s' (%s). Specify --framework.",
			testCaseID, strings.Join(candidates, ", "))
	}
}

// sortedKeys renders the configured value keys as a comma-separated, sorted
// list for error messages.
func sortedKeys(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// insertion sort keeps this dependency-free and the sets are tiny.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return strings.Join(keys, ", ")
}
