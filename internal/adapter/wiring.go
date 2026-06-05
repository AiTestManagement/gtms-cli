// CON-023 / ENH-145 wiring bridge for automate + link writes.
//
// This file owns two pieces glued to the adapter layer:
//
//   - ResolveCanonicalExecuteAdapter: maps (config, framework) → canonical
//     execute adapter name. Wiring records carry the EXECUTE adapter, not
//     the automate or link operation's own adapter, so a later `gtms
//     execute` reads the wiring and picks the right runner. (CON-023 Q#19.)
//
//   - WriteAutomateWiring: called at the end of a successful `gtms automate`
//     (or the manual-execute error path). Computes testcase-hash and
//     artefact-hash, resolves the canonical execute adapter, and writes a
//     six-field wiring record via internal/wiring. Replaces the legacy
//     pipeline.BuildAutomationRecord call on the automate code path.
package adapter

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/execution"
	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/aitestmanagement/gtms-cli/internal/task"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// ResolveCanonicalExecuteAdapter returns the canonical execute adapter name
// for the given framework, following the algorithm pinned by
// PRP-ENH-145-146-147 §"Task 4 DECISION":
//
//  1. Single-default fast path: if defaults.execute is set, points at an
//     execute adapter, and that adapter's framework matches → return it.
//  2. Framework filter: collect all execute adapters whose framework matches.
//     One match → return it. Multiple matches → prefer the project default
//     if it's in the set; otherwise return lexically-first (deterministic).
//  3. No match → error naming the framework and listing the configured
//     execute adapters.
//
// The third return value is the list of qualifying matches found during
// the framework filter; callers may surface it in a "fell back to X
// because of Y, Z also matched" diagnostic. Empty when the fast path
// succeeded or when no adapters matched.
func ResolveCanonicalExecuteAdapter(cfg *config.Config, framework string) (string, []string, error) {
	if cfg == nil {
		return "", nil, errors.New("config is required to resolve canonical execute adapter")
	}
	if framework == "" {
		return "", nil, errors.New("framework is required to resolve canonical execute adapter")
	}

	executeAdapters := cfg.Adapters["execute"]

	// Step 1: single-default fast path.
	if d := cfg.Defaults["execute"]; d != "" {
		if ac, ok := executeAdapters[d]; ok && ac != nil && ac.Framework == framework {
			return d, nil, nil
		}
	}

	// Step 2: framework filter.
	var matches []string
	for name, ac := range executeAdapters {
		if ac == nil {
			continue
		}
		if ac.Framework == framework {
			matches = append(matches, name)
		}
	}
	sort.Strings(matches)

	switch len(matches) {
	case 0:
		return "", nil, fmt.Errorf(
			"no execute adapter configured for framework %q — add one in gtms.config under adapters.execute with `framework: %s` (configured: %s)",
			framework, framework, describeExecuteAdapters(executeAdapters))
	case 1:
		return matches[0], nil, nil
	default:
		if d := cfg.Defaults["execute"]; d != "" {
			for _, m := range matches {
				if m == d {
					return d, matches, nil
				}
			}
		}
		// Deterministic fallback: lexically-first wins.
		return matches[0], matches, nil
	}
}

// describeExecuteAdapters renders the configured execute adapters as a
// human-readable list "name(framework=fw)" so the error diagnostic shows
// the user exactly what's available and which framework each one declares.
func describeExecuteAdapters(adapters map[string]*config.AdapterConfig) string {
	if len(adapters) == 0 {
		return "<none configured>"
	}
	names := make([]string, 0, len(adapters))
	for name := range adapters {
		names = append(names, name)
	}
	sort.Strings(names)

	out := ""
	for i, name := range names {
		fw := ""
		if ac := adapters[name]; ac != nil {
			fw = ac.Framework
		}
		if fw == "" {
			fw = "<no framework>"
		}
		if i > 0 {
			out += ", "
		}
		out += fmt.Sprintf("%s(framework=%s)", name, fw)
	}
	return out
}

// WriteAutomateWiring is the post-`gtms automate` writer that replaces
// pipeline.BuildAutomationRecord on the wiring code path. It runs after a
// successful automate task (or the manual-execute error path) and writes a
// six-field wiring record to gtms/automation/wiring/.
//
// Inputs:
//
//   - cfg: project config, used to resolve the canonical execute adapter.
//   - tf:  the completed automate task file (carries TC ID + framework).
//   - rc:  the result contract (carries the produced artefact path).
//
// Skip conditions (return nil warnings, no error, no wiring written):
//
//   - tf.Type != "automate"               — only automate writes wiring here
//   - tf.Framework == "manual"            — manual-only TCs have no wiring
//                                           (CON-023 Q#12 / Edge Case 1)
//   - rc.Artefact == ""                   — skeleton / dry-run / trial-run
//                                           produced nothing to wire
//   - rc.Status != "complete"             — failures don't write wiring;
//                                           the user re-runs or triages
//
// Returns warnings the caller should surface to the user. The canonical
// adapter resolver may pick a deterministic lexical fallback when multiple
// execute adapters match the framework; in that case it returns the list
// of candidate matches and WriteAutomateWiring emits a one-line warning
// naming the chosen adapter and the competing matches so the user can
// pin a default in gtms.config if the implicit choice is wrong.
//
// Hash computation is best-effort but the wiring file's six-field
// non-empty invariant is enforced: if a hash cannot be computed (spec
// missing, artefact unreadable), an error is returned and no wiring is
// written.
// isBuiltinAutomateAdapter reports whether the adapter name is one of the
// Tier 0 built-in automate adapters that may write PendingArtefactHash
// (ENH-151). Used to scope the self-skip in WriteAutomateWiring.
func isBuiltinAutomateAdapter(name string) bool {
	return builtinActionAdapters["automate"][name]
}

func WriteAutomateWiring(projectRoot string, cfg *config.Config, tf *task.TaskFile, rc *result.ResultContract) ([]string, error) {
	if tf == nil || rc == nil {
		return nil, errors.New("task file and result contract are required")
	}
	if tf.Type != "automate" {
		return nil, nil
	}
	if tf.Framework == "" {
		return nil, errors.New("task file is missing framework; cannot write wiring without it")
	}
	if tf.Framework == "manual" {
		return nil, nil
	}
	if rc.Artefact == "" {
		return nil, nil
	}
	if rc.Status != "complete" {
		return nil, nil
	}

	// ENH-151: avoid double-writing the wiring that BuiltinAutomate just
	// wrote with PendingArtefactHash. BuiltinAutomate writes wiring
	// internally; the handleSyncResult path then calls WriteAutomateWiring
	// again via buildPipelineRecords. Without this guard, the second write
	// would overwrite pending with a real hash computed from the empty
	// skeleton — defeating the sentinel design.
	//
	// The skip is intentionally narrow: only the built-in Tier 0 automate
	// adapters (agent-automate / manual-automate) ever produce pending
	// wiring, and we additionally require the existing wiring's artefact
	// path to match the current call's artefact. A Tier 1/2 adapter run
	// that targets the same (TC, framework) — possibly at a different
	// artefact path — must write its own wiring; otherwise the wiring
	// would be stuck pointing at the stale built-in skeleton.
	if isBuiltinAutomateAdapter(tf.Adapter) {
		existing, _, findErr := wiring.Find(projectRoot, tf.Target, tf.Framework)
		if findErr == nil && existing != nil &&
			wiring.IsPendingArtefactHash(existing.ArtefactHash) &&
			existing.Artefact == filepath.ToSlash(rc.Artefact) {
			return nil, nil
		}
	}

	// Resolve canonical execute adapter — wiring.adapter is the runner,
	// NOT the automate adapter that produced the artefact.
	executeAdapter, matches, err := ResolveCanonicalExecuteAdapter(cfg, tf.Framework)
	if err != nil {
		return nil, fmt.Errorf("resolving canonical execute adapter for wiring: %w", err)
	}

	var warnings []string
	if w := CanonicalFallbackWarning(executeAdapter, matches, tf.Framework); w != "" {
		warnings = append(warnings, w)
	}

	// Compute testcase-hash from the resolved spec file.
	specPath, err := pipeline.ResolveTestCaseSpec(projectRoot, tf.Target)
	if err != nil {
		return warnings, fmt.Errorf("resolving test case spec for wiring: %w", err)
	}
	testCaseHash, err := pipeline.HashFile(filepath.Join(projectRoot, filepath.FromSlash(specPath)))
	if err != nil {
		return warnings, fmt.Errorf("hashing test case spec for wiring: %w", err)
	}

	// BUG-057: containment check on the adapter-produced artefact path
	// before hashing or writing wiring. A misbehaving or hostile adapter
	// must not be able to plant a wiring record that points outside
	// projectRoot. ResolveUnderRoot normalises absolute-inside-root paths
	// to project-relative slash form; wiring never stores absolute paths.
	absArtefact, storedArtefact, safeErr := pathsafe.ResolveUnderRoot(projectRoot, rc.Artefact)
	if safeErr != nil {
		return warnings, fmt.Errorf("unsafe artefact path for wiring: %w", safeErr)
	}

	// Compute artefact-hash from the produced artefact.
	artefactHash, err := pipeline.HashFile(absArtefact)
	if err != nil {
		return warnings, fmt.Errorf("hashing artefact for wiring: %w", err)
	}

	rec := &wiring.WiringRecord{
		TestCase:     tf.Target,
		TestCaseHash: testCaseHash,
		Framework:    tf.Framework,
		Adapter:      executeAdapter,
		Artefact:     storedArtefact,
		ArtefactHash: artefactHash,
	}
	if _, err := wiring.Write(projectRoot, rec); err != nil {
		return warnings, err
	}
	return warnings, nil
}

// CanonicalFallbackWarning renders a one-line user-facing warning when
// ResolveCanonicalExecuteAdapter picked an execute adapter from a list of
// multiple framework-matching candidates (the deterministic
// lexical-or-default fallback). Returns "" when the resolver took the
// single-default fast path or there was only one match — in those cases
// the choice is unambiguous and no warning is warranted.
//
// Exported so internal/link can emit the same wording for the link path;
// keep the diagnostic copy in one place so future tweaks don't drift
// between the two callers (CON-023 / ENH-145 review-fix).
//
// matches is the candidate list returned by ResolveCanonicalExecuteAdapter:
// empty/len==1 → no warning; len>=2 → fallback was used.
func CanonicalFallbackWarning(chosen string, matches []string, framework string) string {
	if len(matches) < 2 {
		return ""
	}
	var competing []string
	for _, m := range matches {
		if m != chosen {
			competing = append(competing, m)
		}
	}
	sort.Strings(competing)
	return fmt.Sprintf(
		"Multiple execute adapters match framework %q: wiring was written with adapter %q (also matched: %s). Pin a default in gtms.config (defaults.execute) to suppress this warning.",
		framework, chosen, strings.Join(competing, ", "))
}

// WriteExecuteResultsFile writes the per-test results file at
// gtms/execution/*.results.yaml (ADR-020 / CON-016 / ENH-109) after a
// successful execute. CON-023 / ENH-146 retires the legacy
// pipeline.UpdateExecutionResult's automation-record-update half — the
// execute path no longer touches wiring or any legacy record. The
// committed-state guarantee is preserved: every execute task still leaves
// a gtms/execution/*.results.yaml row.
//
// Framework is sourced from rc.Framework (stamped from the wiring record
// at result.Create time on the wiring path, or from the manual result
// template on the manual-execute path). The wiring file itself is never
// read or written here.
func WriteExecuteResultsFile(projectRoot string, tf *task.TaskFile, rc *result.ResultContract) error {
	if tf == nil || rc == nil {
		return errors.New("task file and result contract are required")
	}
	if tf.Type != "execute" {
		return nil
	}

	completedAt := rc.Completed
	if completedAt == "" {
		completedAt = time.Now().UTC().Format(time.RFC3339)
	}

	framework := rc.Framework
	if framework == "" {
		// Fallback: tf.Framework. Execute should have rc.Framework stamped
		// from wiring, but a manual-execute path with parse failure may
		// leave it empty.
		framework = tf.Framework
	}

	rf := &execution.ResultsFile{
		SchemaVersion: "0.1",
		TaskID:        tf.ID,
		Framework:     framework,
		Adapter:       rc.Adapter,
		StartedAt:     tf.Created,
		CompletedAt:   completedAt,
		Artefact:      rc.Artefact,
		Results: []execution.TestResult{
			{
				TCID:    tf.Target,
				Outcome: executionOutcome(rc),
				Message: rc.Summary,
			},
		},
	}
	_, err := execution.Write(projectRoot, rf)
	return err
}

// executionOutcome resolves the per-test outcome for the execution
// results file. Mirrors pipeline.contractOutcome: prefer rc.Result when
// set; lift status: error to outcome "error"; default to "error" as a
// defensive fallback (validation at Update should have rejected a
// status:complete with empty result).
func executionOutcome(rc *result.ResultContract) string {
	if rc.Result != "" {
		return rc.Result
	}
	if rc.Status == "error" {
		return "error"
	}
	return "error"
}
