package cli

import (
	"fmt"
	"sort"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// shippedFrameworks is the set of framework names that GTMS recognises out of
// the box, regardless of whether the current project has config or records for
// them. Seeding these ensures that --framework bats in a pester-only project
// is "recognised but absent" (silent) rather than "unrecognised near-typo"
// (hint). This is the v1-regression pin: recognised-but-absent must never
// trigger the hint.
var shippedFrameworks = []string{"bats", "playwright", "manual"}

// knownFrameworks builds the recognition set used by frameworkHint. The set is
// the union of:
//  1. Shipped framework names (always recognised).
//  2. Config-adapter frameworks (from gtms.config adapters with a non-empty
//     framework field).
//  3. Wiring-record frameworks (from gtms/automation/wiring/). A framework
//     with records but no config adapter must still be recognised so it does
//     not trigger a false-positive hint.
//
// The "(none)" pseudo-bucket emitted by buildFrameworkEntries is excluded.
func knownFrameworks(cfg *config.Config, projectRoot string) []string {
	set := make(map[string]bool, len(shippedFrameworks))
	for _, fw := range shippedFrameworks {
		set[fw] = true
	}

	// Config-adapter frameworks.
	for _, fe := range buildFrameworkEntries(buildAdapterEntries(cfg)) {
		if fe.Framework != "" && fe.Framework != "(none)" {
			set[fe.Framework] = true
		}
	}

	// Wiring-record frameworks.
	if byTC, err := wiring.Scan(projectRoot); err == nil {
		for _, recs := range byTC {
			for _, r := range recs {
				if r.Framework != "" {
					set[r.Framework] = true
				}
			}
		}
	}

	out := make([]string, 0, len(set))
	for fw := range set {
		out = append(out, fw)
	}
	sort.Strings(out)
	return out
}

// editDistance computes the Levenshtein distance between two strings using the
// standard two-row iterative algorithm. Pure function, no dependencies.
func editDistance(a, b string) int {
	ra := []rune(a)
	rb := []rune(b)
	la := len(ra)
	lb := len(rb)

	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	prev := make([]int, lb+1)
	curr := make([]int, lb+1)

	for j := 0; j <= lb; j++ {
		prev[j] = j
	}

	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			ins := curr[j-1] + 1
			del := prev[j] + 1
			sub := prev[j-1] + cost
			best := ins
			if del < best {
				best = del
			}
			if sub < best {
				best = sub
			}
			curr[j] = best
		}
		prev, curr = curr, prev
	}

	return prev[lb]
}

// frameworkHint returns a non-empty advisory note when name is unrecognised
// but within edit distance <= 2 of a recognised framework, suggesting the
// closest match. Returns "" in all other cases:
//   - name is empty (no --framework flag)
//   - name is recognised (in the known set, even if absent from scope)
//   - name is unrecognised but not a near-typo of any recognised name
//
// The hint is designed for stderr; it never affects stdout, JSON, or the exit
// code. Gate is on RECOGNITION (name not in known), never on scope-match.
func frameworkHint(name string, known []string) string {
	if name == "" {
		return ""
	}

	// Recognised -- no hint even if absent from this scope.
	for _, fw := range known {
		if fw == name {
			return ""
		}
	}

	// Unrecognised -- find the closest known framework by edit distance.
	bestDist := -1
	bestMatch := ""
	for _, fw := range known {
		d := editDistance(name, fw)
		if bestDist < 0 || d < bestDist {
			bestDist = d
			bestMatch = fw
		}
	}

	if bestDist >= 0 && bestDist <= 2 {
		return fmt.Sprintf("Note: no records for framework '%s' in this scope. Did you mean '%s'?",
			sanitizeForError(name), bestMatch)
	}

	return ""
}
