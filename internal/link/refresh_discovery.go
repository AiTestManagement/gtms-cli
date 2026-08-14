package link

import (
	"fmt"
	"sort"

	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// RefreshTarget is one wiring record selected for a refresh, or a per-record
// discovery FAILURE. Exactly one of Record / Err is meaningful:
//
//   - Record set, Err nil: a valid, identity-verified record ready for planning.
//   - Err set, Record nil: the file is malformed/unreadable, or its YAML identity
//     disagrees with its filename (a cross-record overwrite vector).
//
// TCID and Framework always come from the FILENAME (the identity the user
// requested), never from the record's YAML. That is the whole point of the
// identity check: a file named tc-A--bats whose YAML declares testcase B is
// reported as a failing target under A (what the preview would have labelled),
// and is NEVER handed on as a selectable record whose write would land on B
// (CODEX-001).
type RefreshTarget struct {
	TCID      string
	Framework string
	Record    *wiring.WiringRecord
	Err       error
}

// PartitionTCIDsByAmbiguity splits a spec-first refresh scope into the bare TC
// IDs that identify exactly one spec and the ones that appear more than once.
// Both slices are sorted and free of repeats, so the caller's behaviour does not
// depend on directory-walk order. ids need not be sorted.
//
// Folder/recursive refresh discovers its scope from spec filenames, and wiring is
// keyed on the BARE id. When two specs in scope carry the same bare ID, a refresh
// cannot tell which one to hash: PlanRefresh resolves the spec through
// pipeline.ResolveTestCaseSpec -> testcase.ResolveSpec, which takes the FIRST
// filepath.Walk match and stops. Refreshing anyway would write a testcase-hash
// derived from an arbitrary source, with nothing in the output to say which spec
// won (CODEX-004).
//
// The ambiguous IDs are therefore excluded from discovery and reported as counted
// failures, while the unambiguous ones refresh normally (REV-129 CLAUDE-003) --
// one collision must not stop an entire folder, or an entire subtree under -r,
// from refreshing. Dry and real callers use the same partition, so their target
// set and failure accounting are identical.
//
// This is a narrow refresh-scope guard, not the tree-wide TC-ID uniqueness owned
// by ENH-100/101: two specs sharing an ID in DIFFERENT folders are still resolved
// first-match by a single-TC refresh.
func PartitionTCIDsByAmbiguity(ids []string) (unique, ambiguous []string) {
	counts := make(map[string]int, len(ids))
	for _, id := range ids {
		counts[id]++
	}
	for id, n := range counts {
		if n > 1 {
			ambiguous = append(ambiguous, id)
		} else {
			unique = append(unique, id)
		}
	}
	sort.Strings(unique)
	sort.Strings(ambiguous)
	return unique, ambiguous
}

// DiscoverRefreshTargets is the single identity-validating, diagnostic-preserving
// discovery path for link --refresh. It replaces the old wiring.Find /
// wiring.FindAllForTC selection, which selected records by filename but returned
// the record's YAML identity (CODEX-001) and silently dropped malformed files
// (CODEX-002). Both the dry-run preview and the real refresh call this, so they
// see exactly the same selected records and the same failure set.
//
// tcIDs is the in-scope set of bare TC IDs (one entry for a single-TC refresh;
// the spec-first folder set for a folder refresh). framework != "" narrows to a
// single framework. Selection is keyed on the FILENAME-derived identity via
// wiring.DiscoverScoped, which applies the scope before opening anything:
//
//   - A file whose YAML identity disagrees with its filename, or that cannot be
//     parsed, is returned as a failing target (Err set) -- never dropped and
//     never exposed as a writable record.
//   - A file whose filename does not parse as {tc}--{framework} has no
//     filename-derived TC ID and can never fall inside a real TC scope, so it is
//     skipped (out of scope, not a failure of this refresh).
//   - An out-of-scope file is never read at all, so its size, contents, or file
//     type cannot affect a refresh that was not about it.
//
// Targets are sorted by (TCID, Framework) for stable, deterministic output. A
// non-nil error is returned only for a directory-level read failure.
func DiscoverRefreshTargets(projectRoot string, tcIDs []string, framework string) ([]RefreshTarget, error) {
	// Scope by the filename BEFORE any file is opened, so a refresh only ever
	// reads the records it was asked about (REV-129 CLAUDE-003 / CODEX-129-002).
	// Passing the requested IDs rather than a (tc, framework) predicate also lets
	// the matcher prefer the longest ID in scope, so a test case whose own ID
	// contains "--" resolves correctly (REV-130 CODEX-130-001). Identity
	// validation is unaffected -- it compares the filename with the file's own
	// YAML, and the filename is known before the read.
	all, err := wiring.DiscoverForScope(projectRoot, tcIDs, framework)
	if err != nil {
		return nil, err
	}

	var targets []RefreshTarget
	for _, dr := range all {
		if dr.Err != nil {
			// Stable, greppable failure label: identifies the wiring as
			// malformed/unreadable (covers unparseable YAML and filename/content
			// identity mismatch alike). A later BATS check greps this token.
			targets = append(targets, RefreshTarget{
				TCID:      dr.TCFromName,
				Framework: dr.FWFromName,
				Err:       fmt.Errorf("malformed or unreadable wiring: %v", dr.Err),
			})
			continue
		}
		targets = append(targets, RefreshTarget{
			TCID:      dr.TCFromName,
			Framework: dr.FWFromName,
			Record:    dr.Record,
		})
	}

	sort.Slice(targets, func(i, j int) bool {
		if targets[i].TCID != targets[j].TCID {
			return targets[i].TCID < targets[j].TCID
		}
		return targets[i].Framework < targets[j].Framework
	})
	return targets, nil
}
