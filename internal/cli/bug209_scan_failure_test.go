package cli

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/result"
)

// BUG-209 AC31: a failure scanning .gtms/results must not read as "no refusals" to
// the already-passing check. It warns, and the test runs rather than being skipped.
func TestBUG209_BulkScanFailureVetoesAlreadyPassing(t *testing.T) {
	skipIfShort(t)
	root := t.TempDir()
	enh231Spec(t, root, "drift", "tc-11111111")
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-11111111", Framework: "bats", Adapter: "bats-runner", Result: "pass"})

	// Control: with a clean scan the passing test is skipped, so nothing is dispatched.
	before := countHandoffs(t, root)
	out, err := runBulk(t, root, "drift", enh231Config(t, ""), false)
	require.NoError(t, err)
	require.Equal(t, before, countHandoffs(t, root), "control: an already-passing test must be skipped")
	assert.NotContains(t, out, "refused results directory", "control: a clean scan is not a refusal")

	result.InjectScanReadFailureForTest(errors.New("injected list failure"))
	t.Cleanup(func() { result.InjectScanReadFailureForTest(nil) })

	out, _ = runBulk(t, root, "drift", enh231Config(t, ""), false)
	assert.Contains(t, out, "refused results directory .gtms/results: could not be inspected: injected list failure")
	assert.Equal(t, before+1, countHandoffs(t, root), "the scan failure must veto the skip, so the test is dispatched")
}
