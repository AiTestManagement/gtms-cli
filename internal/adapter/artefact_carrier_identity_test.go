package adapter

// Behavioural guard: the artefact path the adapter WRITES is the same value the
// ownership boundary AUTHORISED -- not a second computation that agrees with it.
//
// The defect: buildAdapterContext re-ran planTier0AutomateArtefact and assigned
// the result to ctx.PlannedArtefact, while enforceArtefactOwnership had planned,
// collision-checked and RESERVED its own separate result. Two values, checked
// once. They matched only because every planner input was identical at both call
// sites, which nothing guaranteed and nothing tested.
//
// The seam that makes this observable is the FrameworkSupport registry, the same
// one TestOwnerDerivedNaming_RenamedOwnerWritesOnlyOwnerDerivedPath uses:
// BuiltinAutomate reaches the filesystem only through support.GenerateSkeleton,
// so the substituted support records WHICH PATH the implementation chose, at the
// moment it chose it.
//
// The discriminating trick is that the substituted support is a DRIFTING planner
// input: its Extension() answers ".plan1.bats" the first time the planner asks
// and ".plan2.bats" every time after. Extension() and OutputDir() are called from
// exactly one place in production -- PlanArtefactPath -- so the count of calls is
// the count of plans. One plan yields plan1 and the reservation holds plan1; a
// second plan yields plan2, and the write lands somewhere the ownership check
// never looked. Equal strings computed twice can no longer masquerade as one
// carried value.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/config"
)

// driftingSupport is a FrameworkSupport whose planner-visible answers CHANGE
// between calls, plus the write recorder from the ownership suite.
type driftingSupport struct {
	inner FrameworkSupport
	// plans counts how many times the planner asked for an extension, which is
	// how many times an artefact path was planned.
	plans int
	// written is the ordered list of paths the adapter was asked to write.
	written []string
}

func (d *driftingSupport) Extension() string {
	d.plans++
	if d.plans == 1 {
		return ".plan1.bats"
	}
	return ".plan2.bats"
}

func (d *driftingSupport) OutputDir(subdir string) string { return d.inner.OutputDir(subdir) }
func (d *driftingSupport) TemplatePath(projectRoot string) string {
	return d.inner.TemplatePath(projectRoot)
}
func (d *driftingSupport) FallbackContent() string { return d.inner.FallbackContent() }

func (d *driftingSupport) GenerateSkeleton(tcID, projectRoot, outPath, templateBody string) error {
	d.written = append(d.written, outPath)
	return d.inner.GenerateSkeleton(tcID, projectRoot, outPath, templateBody)
}

// installDriftingSupport swaps the bats support for the drifting recorder and
// restores the original when the test ends.
func installDriftingSupport(t *testing.T) *driftingSupport {
	t.Helper()
	original := frameworkRegistry["bats"]
	drift := &driftingSupport{inner: original}
	frameworkRegistry["bats"] = drift
	t.Cleanup(func() { frameworkRegistry["bats"] = original })
	return drift
}

// reservedPathFor returns the single path the claim set holds for ownerID under
// framework -- the value Reserve authorised and Commit kept. Reading it from the
// set is what makes this an assertion about the AUTHORISED value rather than
// about a string the test recomputed for itself.
func reservedPathFor(t *testing.T, claims *ArtefactClaimSet, ownerID, framework string) string {
	t.Helper()
	var found []string
	for _, claim := range claims.claims {
		if claim.owner == ownerID && claim.framework == framework {
			found = append(found, claim.abs)
		}
	}
	require.Len(t, found, 1,
		"the operation must hold exactly one artefact claim for %s/%s", ownerID, framework)
	return found[0]
}

// TestPlannedArtefactCarrier_AdapterWritesTheAuthorisedValue proves the adapter
// consumes the reserved path itself.
//
// If a second planning call is reintroduced, the drifting extension makes the
// recomputed path differ from the reserved one and every assertion below fails.
func TestPlannedArtefactCarrier_AdapterWritesTheAuthorisedValue(t *testing.T) {
	skipIfShort(t)

	root, cfg, resolved := ownershipProject(t, "agent-automate", "")
	owner := writeOwnedSpec(t, root, "gtms/test/cases/feat/tc-aaaaaaaa-file.md", "tc-bbbbbbbb")
	drift := installDriftingSupport(t)

	// The operation's claim set is supplied by the test so the reservation the
	// boundary takes survives the invocation and can be read back afterwards.
	claims, err := NewArtefactClaimSet(root)
	require.NoError(t, err)

	flags := automateFlags(owner, false)
	flags.Claims = claims

	res, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-bbbbbbbb", flags)
	require.NoError(t, err)
	require.NotNil(t, res)

	// One plan, therefore one authorised path. Extension() is reached only from
	// PlanArtefactPath in production, so this counts planning calls exactly.
	// assert, not require: when this fails the identity assertions below are the
	// interesting output, and they must still run.
	assert.Equal(t, 1, drift.plans,
		"the artefact path must be planned ONCE per invocation and carried; %d plans means the "+
			"adapter is consuming a path that was not the one checked and reserved", drift.plans)

	authorised := reservedPathFor(t, claims, "tc-bbbbbbbb", "bats")

	// The value that was collision-checked and reserved is the value written.
	assert.Len(t, drift.written, 1, "the adapter must be asked to write exactly once")
	require.NotEmpty(t, drift.written, "the adapter must be asked to write")
	assert.Equal(t, authorised, drift.written[0],
		"the adapter must write the path the ownership boundary authorised, not a recomputation of it")

	// And the drift really would have shown a second plan: the authorised path
	// carries the FIRST answer, so a recomputed path would have carried the
	// second and differed.
	assert.True(t, strings.HasSuffix(authorised, ".plan1.bats"),
		"the reserved path must carry the first planner answer, got %s", authorised)
	recomputed := strings.TrimSuffix(authorised, ".plan1.bats") + ".plan2.bats"
	assert.NotEqual(t, recomputed, drift.written[0],
		"the adapter wrote the path a SECOND planning call would produce, so the reservation "+
			"authorised one path and the adapter used another")

	// The write landed where the reservation said, on disk as well as at the seam.
	assert.FileExists(t, authorised)
	assert.Equal(t, filepath.Join(root, "test", "acceptance", "feat", "tc-bbbbbbbb-tc-aaaaaaaa-file.plan1.bats"),
		authorised, "the fixture's authorised path")
}

// TestPlannedArtefactCarrier_EmptyOnNonAutomatePaths pins the other half of the
// carrier contract: nothing is planned, nothing is reserved, and the carrier is
// EMPTY wherever there is no Tier 0 automate artefact. BuiltinAutomate's refusal
// on an empty carrier depends on that emptiness being real rather than incidental.
func TestPlannedArtefactCarrier_EmptyOnNonAutomatePaths(t *testing.T) {
	skipIfShort(t)

	root, cfg, automateResolved := ownershipProject(t, "agent-automate", "")
	owner := writeOwnedSpec(t, root, "gtms/test/cases/feat/tc-aaaaaaaa-file.md", "tc-bbbbbbbb")

	cases := []struct {
		name     string
		resolved *ResolvedAdapter
		flags    CommandFlags
	}{
		{
			name:     "execute is not an automate path",
			resolved: &ResolvedAdapter{Command: "execute", Name: "bats-runner", Config: automateResolved.Config, Tier: 0, Mode: "sync"},
			flags:    automateFlags(owner, false),
		},
		{
			name: "tier 1 automate plans nothing for the built-in",
			resolved: &ResolvedAdapter{Command: "automate", Name: "shell-automate",
				Config: &config.AdapterConfig{Mode: "sync", Command: "true"}, Tier: 1, Mode: "sync"},
			flags: automateFlags(owner, false),
		},
		{
			name:     "the manual framework has no automate artefact",
			resolved: automateResolved,
			flags:    CommandFlags{Owner: owner, Framework: "manual"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ac := buildAdapterContext(root, "task-carrier", tc.resolved, "tc-bbbbbbbb", tc.flags,
				"feature/test", cfg, root, filepath.Join(root, ".gtms", "results", "task-carrier.handoff.yaml"), "")
			require.NotNil(t, ac)
			assert.Empty(t, ac.PlannedArtefact,
				"a path with no reserved artefact must carry an empty PlannedArtefact")
		})
	}
}
