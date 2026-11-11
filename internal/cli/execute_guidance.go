package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/link"
	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// ENH-222 bulk guidance. The bulk execute loop prints its per-test-case lines
// inline; the GROUPED notices (Draft C refusals, Draft D2 acceptances) and the
// per-test-case "could not start" notice (Draft E) are accumulated here and
// emitted after all per-test-case lines, before the summary. Everything is
// read-only analysis -- no wiring is written here.

// bulkAcceptGroup accumulates one changed script's accepted-and-started tests.
type bulkAcceptGroup struct {
	scriptRel  string
	wiringRels []string
	started    int
}

// bulkAcceptFailure is one accepted test case whose process could not start.
type bulkAcceptFailure struct {
	tcID      string
	scriptRel string
	wiringRel string
	reason    string
}

// bulkRefuseTC is one refused test case in a shared-script refusal group.
type bulkRefuseTC struct {
	tcID      string
	framework string
}

// bulkRefuseGroup accumulates one changed script's refuse-mode refusals.
type bulkRefuseGroup struct {
	scriptRel string
	tcs       []bulkRefuseTC
}

// bulkGuidance holds the ENH-222 grouped-notice state for one bulk run.
type bulkGuidance struct {
	root      string
	folder    string
	framework string // run's --framework (may be "")
	recursive bool

	acceptOrder  []string
	acceptGroups map[string]*bulkAcceptGroup
	couldNotStart []bulkAcceptFailure

	refuseOrder  []string
	refuseGroups map[string]*bulkRefuseGroup

	// specDriftRefusals counts spec-changed (testcase-hash) refusals in the run.
	// It drives ONLY the root-scope run-level hint: a changed spec is confirmed
	// one test case at a time (each spec-changed test case prints its own narrow
	// `gtms link --refresh <tc> --framework <fw>` inline), so no folder-level
	// refresh is offered for spec changes. At root scope there is no folder to
	// name, so the run points the user at gtms status.
	specDriftRefusals int
}

func newBulkGuidance(root, folder, framework string, recursive bool) *bulkGuidance {
	return &bulkGuidance{
		root:         root,
		folder:       folder,
		framework:    framework,
		recursive:    recursive,
		acceptGroups: map[string]*bulkAcceptGroup{},
		refuseGroups: map[string]*bulkRefuseGroup{},
	}
}

// addAccepted records a test case whose script change was accepted. started
// reports whether its process launched (Draft D2 counts started processes; a
// process that started then failed still counts as "ran").
func (g *bulkGuidance) addAccepted(tcID, scriptRel, wiringRel string, started bool, reason string) {
	if !started {
		g.couldNotStart = append(g.couldNotStart, bulkAcceptFailure{tcID: tcID, scriptRel: scriptRel, wiringRel: wiringRel, reason: reason})
		return
	}
	grp, ok := g.acceptGroups[scriptRel]
	if !ok {
		grp = &bulkAcceptGroup{scriptRel: scriptRel}
		g.acceptGroups[scriptRel] = grp
		g.acceptOrder = append(g.acceptOrder, scriptRel)
	}
	grp.wiringRels = append(grp.wiringRels, wiringRel)
	grp.started++
}

// addRefusedScriptChange records a refuse-mode script-change refusal, grouped by
// the script's validated project-relative identity.
func (g *bulkGuidance) addRefusedScriptChange(tcID, framework, scriptRel string) {
	grp, ok := g.refuseGroups[scriptRel]
	if !ok {
		grp = &bulkRefuseGroup{scriptRel: scriptRel}
		g.refuseGroups[scriptRel] = grp
		g.refuseOrder = append(g.refuseOrder, scriptRel)
	}
	grp.tcs = append(grp.tcs, bulkRefuseTC{tcID: tcID, framework: framework})
}

// emit writes the grouped acceptance and refusal notices. Called after the
// per-test-case lines and before the summary.
func (g *bulkGuidance) emit() {
	// Draft D2: grouped automatic acceptances whose tests started.
	for _, scriptRel := range g.acceptOrder {
		grp := g.acceptGroups[scriptRel]
		fmt.Fprintf(os.Stderr, "\n%s changed.\n", filepath.ToSlash(grp.scriptRel))
		noun := "test cases"
		if grp.started == 1 {
			noun = "test case"
		}
		fmt.Fprintf(os.Stderr, "GTMS accepted the change and ran %d %s.\n\n", grp.started, noun)
		fmt.Fprintln(os.Stderr, "Files updated:")
		for _, w := range grp.wiringRels {
			fmt.Fprintf(os.Stderr, "  %s\n", filepath.ToSlash(w))
		}
	}

	// Draft E: accepted but could not start -- reported separately per test case.
	for _, f := range g.couldNotStart {
		fmt.Fprintln(os.Stderr)
		printAcceptedCouldNotStartSingle(f.scriptRel, f.tcID, f.reason, f.wiringRel)
	}

	// Draft C: grouped refuse-mode refusals with a fail-closed safe hint.
	for _, scriptRel := range g.refuseOrder {
		grp := g.refuseGroups[scriptRel]
		noun := "test cases"
		if len(grp.tcs) == 1 {
			noun = "test case"
		}
		fmt.Fprintf(os.Stderr, "\n%d %s were not run because %s changed.\n\n", len(grp.tcs), noun, filepath.ToSlash(grp.scriptRel))
		fmt.Fprintln(os.Stderr, "This project is configured to stop when an automation script changes.")
		fmt.Fprintln(os.Stderr)
		usedBy := projectWideScriptUsers(g.root, grp.scriptRel)
		if usedBy > len(grp.tcs) {
			fmt.Fprintf(os.Stderr, "This script is used by %d test cases across the project; a folder refresh does not cover users outside this run.\n", usedBy)
		}
		fmt.Fprintln(os.Stderr, "Review the changed script. If the change is expected, mark this version as current with:")
		for _, line := range g.scriptChangeRefreshHint(grp) {
			fmt.Fprintf(os.Stderr, "%s\n", line)
		}
	}
}

// scriptChangeRefreshHint applies the ENH-222 Refresh-command safety rule for one
// refuse-mode shared-script group. It returns the hint lines: a single folder
// command when every stale wiring record the folder refresh would touch is a
// script-only change to the SAME script; otherwise narrowed per-test-case
// commands plus the dry-run/inspection route. It fails closed on any record it
// cannot read, validate, resolve or classify.
func (g *bulkGuidance) scriptChangeRefreshHint(grp *bulkRefuseGroup) []string {
	fwSuffix := ""
	if g.framework != "" {
		fwSuffix = " --framework " + g.framework
	}
	recSuffix := ""
	if g.recursive {
		recSuffix = " -r"
	}

	perTC := func() []string {
		var lines []string
		for _, tc := range grp.tcs {
			lines = append(lines, fmt.Sprintf("gtms link --refresh %s --framework %s", tc.tcID, tc.framework))
		}
		// ENH-222 (2026-10-27 delta): never print an empty refresh/dry-run
		// target. A folder-scoped dry-run is offered only when there is a folder
		// to name; at root scope the inspection route is gtms status alone.
		lines = append(lines, "", "Or inspect first:")
		if g.folder != "" {
			lines = append(lines, fmt.Sprintf("gtms link --refresh %s%s%s --dry-run", g.folder, fwSuffix, recSuffix))
		}
		lines = append(lines, "gtms status")
		return lines
	}

	// Root scope has no single folder to name: offer narrowed per-TC commands.
	if g.folder == "" {
		return perTC()
	}

	if g.folderRefreshIsSafe(grp.scriptRel) {
		return []string{fmt.Sprintf("gtms link --refresh %s%s%s", g.folder, fwSuffix, recSuffix)}
	}
	return perTC()
}

// specDriftRunHint emits the run-level guidance when the run refused at least one
// spec-changed test case.
//
// ENH-222 (2026-10-27 owner decision): a changed specification is reviewed and
// confirmed ONE test case at a time. Each spec-changed test case already printed
// its own narrow `gtms link --refresh <tc> --framework <fw>` command inline
// (Draft A). A folder-level refresh for changed specs is NOT offered: it would
// confirm specs the user did not review individually, which contradicts the
// safety rule. Grouped folder hints remain only for safe script-only drift on
// the same script (Draft C). Root scope has no single folder to name, so it
// points the user at gtms status (the tc-ba5d1185 case); the per-test-case
// confirm commands are still printed inline.
func (g *bulkGuidance) specDriftRunHint() {
	if g.specDriftRefusals == 0 {
		return
	}
	if g.folder == "" {
		fmt.Fprintf(os.Stderr, "\n  Hint: changed specs were found across folders. Run gtms status -r to see which test cases changed, then confirm each with gtms link --refresh <tc> --framework <fw>.\n")
	}
}

// folderRefreshIsSafe reports whether `gtms link --refresh <folder>
// [--framework fw] [-r]` would touch ONLY records that are current (a no-op) or
// a script-only change to scriptRel. It fails closed on any discovery or
// classification failure.
func (g *bulkGuidance) folderRefreshIsSafe(scriptRel string) bool {
	owners, err := resolveBulkOwners(g.root, g.folder, g.recursive)
	if err != nil || len(owners) == 0 {
		return false
	}
	ownerByID := map[string]*testcase.Owner{}
	tcIDs := make([]string, 0, len(owners))
	for _, o := range owners {
		ownerByID[o.ID()] = o
		tcIDs = append(tcIDs, o.ID())
	}

	targets, derr := link.DiscoverRefreshTargets(g.root, tcIDs, g.framework)
	if derr != nil {
		return false
	}
	for _, t := range targets {
		if t.Err != nil || t.Record == nil {
			return false // malformed/unreadable/identity-mismatch -> fail closed
		}
		owner := ownerByID[t.TCID]
		if owner == nil {
			return false
		}
		absArtefact, safeArtefact, serr := pathsafe.ResolveUnderRoot(g.root, t.Record.Artefact)
		if serr != nil {
			return false
		}
		_ = absArtefact
		dr := checkWiringDrift(g.root, t.Record, safeArtefact, owner)
		if dr.diag == "" {
			continue // current record -> refresh is a no-op -> safe
		}
		if dr.scriptOnly() && filepath.ToSlash(safeArtefact) == filepath.ToSlash(scriptRel) {
			continue // same-script script-only change -> safe
		}
		return false // spec drift, different script, missing/unreadable, unverified
	}
	return true
}

// projectWideScriptUsers counts the distinct test cases whose wiring records
// resolve to the same project-relative artefact identity as scriptRel.
func projectWideScriptUsers(root, scriptRel string) int {
	dir := layout.WiringDir(root)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	want := filepath.ToSlash(scriptRel)
	seen := map[string]struct{}{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), wiring.FileSuffix) {
			continue
		}
		rec, rerr := wiring.Read(filepath.Join(dir, e.Name()))
		if rerr != nil || rec == nil {
			continue
		}
		_, safe, serr := pathsafe.ResolveUnderRoot(root, rec.Artefact)
		if serr != nil {
			continue
		}
		if filepath.ToSlash(safe) == want {
			seen[rec.TestCase] = struct{}{}
		}
	}
	return len(seen)
}

// printScriptMissingBulkBlock is Draft I2: Draft I's block below a
// `not run (script missing)` line (the line already states the reason).
func printScriptMissingBulkBlock(tcID, framework, scriptRel string) {
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Expected file:")
	fmt.Fprintf(os.Stderr, "%s\n\n", filepath.ToSlash(scriptRel))
	fmt.Fprintln(os.Stderr, "Restore the file, or regenerate it:")
	fmt.Fprintf(os.Stderr, "gtms automate %s --framework %s --force\n", tcID, framework)
}

// printScriptUnreadableBulkBlock is Draft J2.
func printScriptUnreadableBulkBlock(scriptRel string) {
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "File:")
	fmt.Fprintf(os.Stderr, "%s\n\n", filepath.ToSlash(scriptRel))
	fmt.Fprintln(os.Stderr, "Check that the path is a readable file, then retry.")
}

// printChooseFrameworkBulkBlock is Draft L: list the available frameworks and an
// example framework-scoped command.
func printChooseFrameworkBulkBlock(root, tcID string) {
	frameworks := frameworksForTC(root, tcID)
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Available frameworks:")
	for _, fw := range frameworks {
		fmt.Fprintf(os.Stderr, "  %s\n", fw)
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Run again with, for example:")
	example := "<framework>"
	if len(frameworks) > 0 {
		example = frameworks[0]
	}
	fmt.Fprintf(os.Stderr, "gtms execute %s --framework %s\n", tcID, example)
}

// frameworksForTC lists the wired frameworks for a test case, sorted.
func frameworksForTC(root, tcID string) []string {
	sels, err := wiring.FindAllForTCSelections(root, tcID)
	if err != nil {
		return nil
	}
	var fws []string
	for _, s := range sels {
		fws = append(fws, s.Record.Framework)
	}
	sort.Strings(fws)
	return fws
}
