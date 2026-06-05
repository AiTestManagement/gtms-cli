package reader

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
	"github.com/aitestmanagement/gtms-cli/internal/wiring"
)

// CON-023 / ENH-146 picker.
//
// For each wiring record we classify hash currency into three tiers:
//
//	current          — both hashes match current content AND artefact present
//	stale            — artefact present; at least one hash differs
//	missing-artefact — artefact path does not resolve on disk
//
// Picker ordering is pinned by ENH-146 §"Decisions Inherited":
//
//	1. current automated
//	2. current manual
//	3. stale automated
//	4. stale manual
//	5. missing-artefact automated
//	6. missing-artefact manual
//	7. lexical tie-break on (framework, artefact path)
//
// The picker always selects something when at least one wiring record
// exists, so `selected_framework` is non-null whenever `wired: true`.

// CurrencyTier is the hash-currency classification of one wiring record.
type CurrencyTier int

const (
	// TierCurrent — both hashes match current content AND artefact present.
	TierCurrent CurrencyTier = iota
	// TierStale — artefact present; at least one hash differs.
	TierStale
	// TierMissingArtefact — artefact path does not resolve on disk.
	// Artefact-hash cannot be computed in this state.
	TierMissingArtefact
)

// Classification carries everything ENH-146 needs to render one
// (testcase, framework) wiring row.
type Classification struct {
	Tier              CurrencyTier
	StaleTestcaseHash bool
	StaleArtefactHash bool // suppressed (false) when MissingArtefact is true
	MissingArtefact   bool
	ArtefactPresent   bool
}

// ClassifyWiring computes the currency tier and drift flags for one
// wiring record against the current content of the TC spec and artefact
// on disk. ENH-146 §"Picker Pattern (Task 7)" pins both the tier
// ordering and the rule that a missing artefact never claims an
// artefact-hash drift.
func ClassifyWiring(projectRoot string, w *wiring.WiringRecord) Classification {
	specStale := false
	if specPath, err := pipeline.ResolveTestCaseSpec(projectRoot, w.TestCase); err == nil {
		if h, hErr := pipeline.HashFile(filepath.Join(projectRoot, filepath.FromSlash(specPath))); hErr == nil {
			specStale = h != w.TestCaseHash
		}
	}

	artefactAbs := pipeline.AbsArtefactPath(projectRoot, w.Artefact)
	if _, statErr := os.Stat(artefactAbs); statErr != nil {
		// Missing artefact dominates. Artefact-hash cannot be computed,
		// so we never report artefact drift in this state.
		return Classification{
			Tier:              TierMissingArtefact,
			StaleTestcaseHash: specStale,
			StaleArtefactHash: false,
			MissingArtefact:   true,
			ArtefactPresent:   false,
		}
	}

	// ENH-151: pending artefact-hash is never stale — pre-bootstrap drift
	// is not a meaningful concept. Skip the hash comparison entirely.
	artefactStale := false
	if !wiring.IsPendingArtefactHash(w.ArtefactHash) {
		if h, err := pipeline.HashFile(artefactAbs); err == nil {
			artefactStale = h != w.ArtefactHash
		}
	}

	tier := TierCurrent
	if specStale || artefactStale {
		tier = TierStale
	}
	return Classification{
		Tier:              tier,
		StaleTestcaseHash: specStale,
		StaleArtefactHash: artefactStale,
		MissingArtefact:   false,
		ArtefactPresent:   true,
	}
}

// pickWiring picks one wiring record from a TC's set per the ENH-146
// rule. defaultFramework + strictFramework are honoured when the caller
// has set them (per-TC strict filter from ENH-082 / status_test).
//
// Returns nil when records is empty OR when strictFramework filters
// everything out.
func pickWiring(projectRoot string, records []*wiring.WiringRecord, classifications []Classification, defaultFramework string, strictFramework bool) (*wiring.WiringRecord, Classification, int) {
	if len(records) == 0 {
		return nil, Classification{}, -1
	}
	if strictFramework && defaultFramework != "" {
		for i, r := range records {
			if r.Framework == defaultFramework {
				return r, classifications[i], i
			}
		}
		return nil, Classification{}, -1
	}

	// Build a ranking key per record. Lower wins.
	type idx struct {
		i   int
		key [3]int // (tier-rank, framework-precedence, lexical-placeholder)
	}
	indices := make([]idx, len(records))
	for i, r := range records {
		c := classifications[i]
		// Tier component.
		tierRank := int(c.Tier) * 2 // 0,2,4 — leave odd slots for manual
		// Framework precedence: non-manual (0) beats manual (1).
		fwRank := 0
		if r.Framework == "manual" {
			fwRank = 1
		}
		indices[i] = idx{i: i, key: [3]int{tierRank, fwRank, 0}}
	}

	sort.SliceStable(indices, func(a, b int) bool {
		ka, kb := indices[a].key, indices[b].key
		if ka[0] != kb[0] {
			return ka[0] < kb[0]
		}
		if ka[1] != kb[1] {
			return ka[1] < kb[1]
		}
		// Lexical tie-break on (framework, artefact path).
		ra, rb := records[indices[a].i], records[indices[b].i]
		if ra.Framework != rb.Framework {
			return ra.Framework < rb.Framework
		}
		return ra.Artefact < rb.Artefact
	})

	// CON-023 / ENH-146 §"Decisions Inherited": framework precedence is
	// the picker tie-breaker after hash currency, and "per-project
	// configurability is rejected for v1." Honouring `defaultFramework`
	// as a top-tier override would re-introduce that configurability via
	// the config-default door. defaultFramework is now only honoured in
	// strict-framework mode (the explicit per-call filter handled above
	// at the top of this function). Anything else flows through the
	// pinned ordering: tier → framework precedence → lexical.
	//
	// If a real need for configurable picker order surfaces, file a
	// separate ENH rather than bringing this override back.
	winner := indices[0].i
	return records[winner], classifications[winner], winner
}

// frameworksByName returns a slice of framework names extracted from the
// supplied wiring records, sorted for deterministic output.
func frameworksByName(records []*wiring.WiringRecord) []string {
	if len(records) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(records))
	for _, r := range records {
		if r.Framework != "" {
			seen[r.Framework] = true
		}
	}
	out := make([]string, 0, len(seen))
	for fw := range seen {
		out = append(out, fw)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

// driftLabel maps a Classification to the ENH-146 wiring_drift JSON
// value: "" (no drift) / "testcase" / "artefact" / "both".
//
// When the artefact is missing, only "testcase" or "" is possible —
// artefact-hash cannot be computed, so it cannot be claimed to mismatch.
func driftLabel(c Classification) string {
	switch {
	case c.MissingArtefact:
		if c.StaleTestcaseHash {
			return "testcase"
		}
		return ""
	case c.StaleTestcaseHash && c.StaleArtefactHash:
		return "both"
	case c.StaleTestcaseHash:
		return "testcase"
	case c.StaleArtefactHash:
		return "artefact"
	}
	return ""
}

// Used only as a sanity import — keeps gofmt happy when picker.go
// stands alone.
var _ = strings.HasPrefix
