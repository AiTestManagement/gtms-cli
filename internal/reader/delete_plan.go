package reader

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// canonicalPathKey returns a comparison key for an absolute path. On
// case-insensitive filesystems (Windows) two paths that differ only in case
// name the same physical file, so they must collapse to one identity;
// otherwise a differently-cased alias would slip past the shared-artefact
// reference check and a forced delete could remove a file a surviving test
// case still points to (CODEX-002). The original path is preserved separately
// for display and removal -- this key is used only for matching/dedup.
func canonicalPathKey(abs string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(abs)
	}
	return abs
}

// artefactCand pairs an artefact's original absolute path (used for physical
// removal) with its project-relative display path. Candidates are keyed by
// canonicalPathKey so aliases and case-variants dedupe to one entry.
type artefactCand struct {
	abs string
	rel string
}

// dedupByKey returns paths with duplicates (by canonicalPathKey) removed,
// preserving first-seen order. Defends bulk/folder deletes against duplicate
// frontmatter TC IDs, which would otherwise gather the same physical path more
// than once and fail mid-operation on the second removal (CODEX-005).
func dedupByKey(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	out := paths[:0:0]
	for _, p := range paths {
		k := canonicalPathKey(p)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, p)
	}
	return out
}

// filterOutKeys drops any path whose canonicalPathKey is in exclude, preserving
// order. Used to give preserved artefacts precedence over every GTMS-owned
// deletion category, not just result files (CODEX-001).
func filterOutKeys(paths []string, exclude map[string]bool) []string {
	if len(exclude) == 0 {
		return paths
	}
	out := paths[:0:0]
	for _, p := range paths {
		if exclude[canonicalPathKey(p)] {
			continue
		}
		out = append(out, p)
	}
	return out
}

// deletePlan is the fully-resolved set of filesystem changes for a delete
// operation, computed BEFORE any removal (BUG-173). Planning the whole
// operation up front makes bulk output independent of TC iteration order and
// makes a dry-run preview identical to the subsequent real run: nothing is
// removed while the plan is built, so a shared artefact is classified the same
// way whether or not earlier siblings would have been deleted first.
type deletePlan struct {
	testCasesProcessed int
	specs              []string // absolute paths (spec files; empty when keepSpec)
	records            []string // wiring + manual records for selected TCs
	taskFiles          []string
	resultContracts    []string
	artefactDeletes    []string // canonical abs artefact paths to physically remove
	resultFiles        []string // executed_artefact result files (deduped, minus artefact candidates)
	preserved          []PreservedArtefact
}

// buildDeletePlan resolves the complete deletion plan for a single TC (tcID
// non-empty) or a scope of TCs (tcID empty).
//
// Atomicity (ENH-128 AC #5): every SELECTED test case's declared artefact and
// executed_artefact path is resolved via pathsafe.ResolveUnderRoot during
// planning, REGARDLESS of force. A selected path that escapes the project-owned
// allowlist returns a *PathSafetyError and the whole operation aborts before
// any removal -- even for a plain, preserve-by-default delete.
//
// Referential protection (BUG-173): each referenced artefact is classified
// against project-wide wiring/manual references. Plain delete preserves every
// referenced artefact. A forced delete removes an artefact only when every
// reference to it belongs to the selected deletion set; otherwise the artefact
// is preserved and the surviving external TC IDs are recorded for the warning.
func buildDeletePlan(projectRoot string, scope *ScopeInfo, tcID string, keepSpec, force bool) (*deletePlan, error) {
	// Canonicalise the root the same way pathsafe.ResolveUnderRoot does, so the
	// GTMS-owned paths built by joining under it (specs/records/tasks/contracts)
	// and the display paths from toProjectRel share one identity with artefact
	// paths that go through ResolveUnderRoot. Without this, a Windows 8.3 short
	// root (RUNNER~1) and the long form (runneradmin) diverge: canonical keys
	// stop matching (preserved artefacts leak into a delete category) and
	// filepath.Rel emits a spurious "../../" escape (BUG-173 Windows CI).
	projectRoot = pathsafe.CanonicalRoot(projectRoot)

	// 1. Resolve the selection set of TC IDs. Deduplicate: a folder with two
	//    specs sharing a frontmatter ID would otherwise gather the same physical
	//    paths twice and fail mid-operation on the second removal (CODEX-005).
	var selection []string
	if tcID != "" {
		selection = []string{tcID}
	} else {
		tcs, err := scanTestCases(projectRoot, scope)
		if err != nil {
			return nil, fmt.Errorf("scanning test cases: %w", err)
		}
		for _, tc := range tcs {
			selection = append(selection, tc.ID)
		}
	}
	selection = dedupByKey(selection)
	selectionSet := make(map[string]bool, len(selection))
	for _, id := range selection {
		selectionSet[id] = true
	}

	// 2. Project-wide reference index: canonical artefact key -> set of TC IDs
	//    that reference it. `unreadable` lists records that could not be parsed.
	//    A forced delete must not proceed on an incomplete index: a malformed
	//    surviving record could hide the only remaining reference to a shared
	//    artefact, so force fails closed (CODEX-003). Plain preserve-by-default
	//    delete stays lenient -- it removes no artefacts, so a missed reference
	//    cannot cause loss.
	refIndex, unreadable := buildArtefactReferenceIndex(projectRoot)
	if force && len(unreadable) > 0 {
		return nil, fmt.Errorf(
			"cannot safely honour --force-artefact-deletion: %d wiring/manual record(s) could not be parsed, so shared-artefact protection cannot be guaranteed; fix or remove then retry: %s",
			len(unreadable), strings.Join(unreadable, ", "))
	}

	plan := &deletePlan{}

	// artefactCandidates: canonical key -> {abs, rel}, deduplicated across the
	// whole selection (BUG-073 + BUG-173 batch dedupe; CODEX-002 case-fold).
	artefactCandidates := make(map[string]artefactCand)
	// resultFileSeen dedupes executed_artefact result files across the selection.
	resultFileSeen := make(map[string]bool)
	var resultFileOrder []string

	// 3. Gather per selected TC.
	for _, id := range selection {
		plan.testCasesProcessed++

		if !keepSpec {
			specs, err := findTestCaseSpecs(projectRoot, id)
			if err != nil {
				return nil, fmt.Errorf("finding test case specs for %s: %w", id, err)
			}
			plan.specs = append(plan.specs, specs...)
		}

		records, err := findAutomationRecordFiles(projectRoot, id)
		if err != nil {
			return nil, fmt.Errorf("finding automation records for %s: %w", id, err)
		}
		plan.records = append(plan.records, records...)

		// Atomic precheck: resolve declared artefact paths. A path-safety
		// violation aborts the entire plan before any removal.
		scripts, err := findTestScripts(projectRoot, records)
		if err != nil {
			return nil, err
		}
		for _, abs := range scripts {
			k := canonicalPathKey(abs)
			if _, seen := artefactCandidates[k]; !seen {
				artefactCandidates[k] = artefactCand{abs: abs, rel: toProjectRel(projectRoot, abs)}
			}
		}

		resultFiles, err := findResultFilesFromRecords(projectRoot, records)
		if err != nil {
			return nil, err
		}
		for _, abs := range resultFiles {
			k := canonicalPathKey(abs)
			if !resultFileSeen[k] {
				resultFileSeen[k] = true
				resultFileOrder = append(resultFileOrder, abs)
			}
		}

		tasks, err := findTaskFiles(projectRoot, id)
		if err != nil {
			return nil, fmt.Errorf("finding task files for %s: %w", id, err)
		}
		plan.taskFiles = append(plan.taskFiles, tasks...)

		contracts, err := findResultContracts(projectRoot, id)
		if err != nil {
			return nil, fmt.Errorf("finding result contracts for %s: %w", id, err)
		}
		plan.resultContracts = append(plan.resultContracts, contracts...)
	}

	// 4. Classify each deduped artefact candidate. Iterate in sorted canonical
	//    order so output is deterministic regardless of TC iteration order.
	//    preservedKeys records the canonical key of every artefact that is kept,
	//    so it can be excluded from every GTMS-owned deletion category below.
	keys := make([]string, 0, len(artefactCandidates))
	for k := range artefactCandidates {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	preservedKeys := make(map[string]bool)
	for _, k := range keys {
		cand := artefactCandidates[k]

		var external []string
		for tc := range refIndex[k] {
			if !selectionSet[tc] {
				external = append(external, tc)
			}
		}
		sort.Strings(external)

		switch {
		case !force:
			// Safe default: preserve every referenced artefact.
			plan.preserved = append(plan.preserved, PreservedArtefact{Path: cand.rel})
			preservedKeys[k] = true
		case len(external) > 0:
			// Forced, but a TC outside the deletion set still references it.
			plan.preserved = append(plan.preserved, PreservedArtefact{Path: cand.rel, SurvivingTCs: external})
			preservedKeys[k] = true
		default:
			// Forced and exclusive to the deletion set: delete exactly once.
			plan.artefactDeletes = append(plan.artefactDeletes, cand.abs)
		}
	}

	// 5. Result files: keep the existing "delete executed_artefact output"
	//    behaviour, but EXCLUDE any path that is also an artefact candidate
	//    (BUG-073 cross-category dedupe -- artefact classification wins, so a
	//    preserved artefact is never removed via the result-file backdoor).
	for _, abs := range resultFileOrder {
		if _, isArtefact := artefactCandidates[canonicalPathKey(abs)]; isArtefact {
			continue
		}
		plan.resultFiles = append(plan.resultFiles, abs)
	}

	// 6. Preserved artefacts win over EVERY GTMS-owned category, not only
	//    result files: an artefact wired to a path that is also a selected TC's
	//    spec/record/task/contract must not be removed through that category
	//    while the summary reports it preserved (CODEX-001). Also dedupe each
	//    physical list by canonical key so a duplicate frontmatter ID cannot
	//    schedule the same path for removal twice (CODEX-005).
	plan.specs = dedupByKey(filterOutKeys(plan.specs, preservedKeys))
	plan.records = dedupByKey(filterOutKeys(plan.records, preservedKeys))
	plan.taskFiles = dedupByKey(filterOutKeys(plan.taskFiles, preservedKeys))
	plan.resultContracts = dedupByKey(filterOutKeys(plan.resultContracts, preservedKeys))
	plan.resultFiles = dedupByKey(filterOutKeys(plan.resultFiles, preservedKeys))

	return plan, nil
}

// buildArtefactReferenceIndex scans ALL project wiring records (and manual
// records) and returns a map from an artefact's canonical path key to the set
// of test case IDs that reference it, plus a list of record paths that could
// not be parsed. Keying via canonicalPathKey on the pathsafe-resolved absolute
// path means aliases (`path/f` vs `./path/f`) and, on Windows, case-variants
// collapse to one entry, so a surviving alias reference still protects a shared
// artefact (BUG-173 spec tc-9dcaf5c0; CODEX-002).
//
// A reference whose path fails to resolve is skipped rather than fatal -- it is
// not a deletion target. But a record that cannot be PARSED at all is reported
// in `unreadable`: its artefact reference is invisible to this index, so a
// forced delete relying on the index to prove exclusivity must fail closed
// rather than silently delete a still-shared file (CODEX-003). The caller
// enforces that only for the destructive force path; plain preserve-by-default
// delete ignores `unreadable` because it removes no artefacts.
func buildArtefactReferenceIndex(projectRoot string) (index map[string]map[string]bool, unreadable []string) {
	index = make(map[string]map[string]bool)

	add := func(tc, rawArtefact string) {
		if tc == "" || rawArtefact == "" {
			return
		}
		abs, _, err := pathsafe.ResolveUnderRoot(projectRoot, rawArtefact)
		if err != nil {
			return
		}
		k := canonicalPathKey(abs)
		if index[k] == nil {
			index[k] = make(map[string]bool)
		}
		index[k][tc] = true
	}

	// Wiring records (project-wide). Only two fields matter for sharing: the TC
	// (taken from the filename, which is the authoritative identity) and the
	// artefact path. Hash fields are irrelevant here, so a record that is merely
	// missing them still contributes its reference -- extracting leniently
	// (rather than via the strict six-field Read) keeps hash-optional records
	// visible. A record is reported unreadable ONLY when its artefact reference
	// cannot be determined at all -- broken YAML, a filename that encodes no TC,
	// or an empty artefact field -- because then exclusivity cannot be proven
	// and a forced delete must fail closed (CODEX-003).
	wiringDir := layout.WiringDir(projectRoot)
	if entries, err := os.ReadDir(wiringDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), wiring.FileSuffix) {
				continue
			}
			path := filepath.Join(wiringDir, e.Name())
			tc, _, ok := wiring.ParseFilename(e.Name())
			if !ok {
				unreadable = append(unreadable, path)
				continue
			}
			fields, perr := parseRecordArtefactFields(path)
			if perr != nil || fields.Artefact == "" {
				unreadable = append(unreadable, path)
				continue
			}
			add(tc, fields.Artefact)
		}
	} else if !os.IsNotExist(err) {
		// Whole-directory read failure: the index is entirely unreliable. Name
		// the directory so a forced delete aborts with a clear cause.
		unreadable = append(unreadable, fmt.Sprintf("%s (%v)", wiringDir, err))
	}

	// Manual records also carry an `artefact:` field.
	manualDir := filepath.Join(projectRoot, layout.Current().Manual, "records")
	if entries, err := os.ReadDir(manualDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), "--manual.result.yaml") {
				continue
			}
			path := filepath.Join(manualDir, e.Name())
			fields, perr := parseRecordArtefactFields(path)
			if perr != nil {
				unreadable = append(unreadable, path)
				continue
			}
			if fields.Artefact == "" {
				continue
			}
			tc := strings.TrimSuffix(e.Name(), "--manual.result.yaml")
			add(tc, fields.Artefact)
		}
	}

	sort.Strings(unreadable)
	return index, unreadable
}

// toProjectRel renders an absolute path as a forward-slash project-relative
// path for display. Mirrors the CLI's existing filepath.Rel(projectRoot, ...)
// treatment of DeleteResult.FilesDeleted so preserved and deleted paths read
// identically.
func toProjectRel(projectRoot, abs string) string {
	rel, err := filepath.Rel(projectRoot, abs)
	if err != nil {
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(rel)
}
