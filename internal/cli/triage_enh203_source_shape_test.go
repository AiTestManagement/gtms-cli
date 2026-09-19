package cli

// ENH-203 source-shape guard. The retired category flags
// (--automation-wrong / --test-wrong / --app-wrong) were removed. This guard
// pins their structural absence from triage.go so a partial reintroduction that
// behavioural tests cannot distinguish -- for example the dead stage-1 branch
// alone, or a hidden registration -- still fails.
//
// It also pins that the positional strategy stays cobra.ArbitraryArgs with
// manual validation (Required Behaviour item 5): a switch to a Cobra range
// check would fail the positive assertion here as well.

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSourceShape_TriageHasNoRetiredFlagTraps(t *testing.T) {
	src, err := os.ReadFile("triage.go")
	require.NoError(t, err)
	// Strip comments so a reworded comment that mentions the history cannot trip
	// the guard: only live code must be free of the traps.
	code := stripGoCommentsCLI(t, "triage.go", string(src))

	// Flag-registration literals are the quoted flag names. The surviving
	// `--value app-wrong` help EXAMPLE is the bare form `app-wrong` inside the
	// Long backtick string and is deliberately not matched here.
	for _, lit := range []string{
		`"automation-wrong"`, `"test-wrong"`, `"app-wrong"`,
	} {
		assert.NotContainsf(t, code, lit,
			"triage.go must not register a retired category flag (found %q)", lit)
	}

	// The retired migration diagnostic and the trap boolean identifiers/branch.
	for _, lit := range []string{
		"were retired",
		"automationWrong", "testWrong", "appWrong",
	} {
		assert.NotContainsf(t, code, lit,
			"triage.go must carry no retired-flag trap remnant (found %q)", lit)
	}

	// Positional strategy frozen (item 5 / ENH-210): a RangeArgs switch would
	// remove this literal and fail here.
	assert.Contains(t, code, "cobra.ArbitraryArgs",
		"triage must keep cobra.ArbitraryArgs with manual positional validation")
}
