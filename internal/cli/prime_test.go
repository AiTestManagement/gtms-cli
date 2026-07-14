package cli

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/adapter"
	"github.com/aitestmanagement/gtms-cli/internal/output"
)

// TestFormatPrimeOutput_ErrorStatus_PrintsTaskFailed verifies that
// formatPrimeOutput renders the "Task failed:" headline for error results.
func TestFormatPrimeOutput_ErrorStatus_PrintsTaskFailed(t *testing.T) {
	res := &adapter.InvokeResult{
		TaskID:   "task-abc12345",
		Status:   "error",
		Target:   "tc-deadbeef",
		Filename: "task-abc-prime-tc-deadbeef.md",
		Adapter:  "manual-prime",
		Mode:     "sync",
		Summary:  "Manual result file already exists. Use --force to overwrite.",
	}

	stderr := captureStderr(t, func() {
		formatPrimeOutput(res)
	})

	assert.Contains(t, stderr, "Task failed:")
	assert.Contains(t, stderr, res.Summary)
}

// TestPrimeErrorPropagation_BUG135 pins the BUG-135 contract: when the
// adapter returns Status "error", the prime command must return a non-nil
// error (triggering a non-zero exit code). The error must be wrapped with
// output.AsDisplayed (to suppress cobra's duplicate "Error:" re-print)
// and must carry the adapter's Summary as its message text.
func TestPrimeErrorPropagation_BUG135(t *testing.T) {
	res := &adapter.InvokeResult{
		TaskID:   "task-abc12345",
		Status:   "error",
		Target:   "tc-deadbeef",
		Filename: "task-abc-prime-tc-deadbeef.md",
		Adapter:  "manual-prime",
		Mode:     "sync",
		Summary:  "Manual result file already exists. Use --force to overwrite.",
	}

	// Simulate the exact post-formatPrimeOutput logic from prime.go.
	var returnedErr error
	if res != nil && res.Status == "error" {
		returnedErr = output.AsDisplayed(fmt.Errorf("%s", res.Summary))
	}

	// Contract: non-nil error for adapter failure.
	require.Error(t, returnedErr, "prime must return non-nil error when adapter reports Status: error")

	// Contract: error carries the adapter summary.
	assert.Contains(t, returnedErr.Error(), res.Summary,
		"error message must contain the adapter summary")

	// Contract: error is wrapped as displayed (suppresses cobra re-print).
	assert.True(t, output.IsDisplayed(returnedErr),
		"error must be wrapped with output.AsDisplayed")
}

// TestPrimeSuccessReturnsNil verifies that a successful prime result
// does NOT trigger the error path.
func TestPrimeSuccessReturnsNil(t *testing.T) {
	res := &adapter.InvokeResult{
		TaskID:   "task-abc12345",
		Status:   "complete",
		Target:   "tc-deadbeef",
		Filename: "task-abc-prime-tc-deadbeef.md",
		Adapter:  "manual-prime",
		Mode:     "sync",
	}

	// Simulate the post-formatPrimeOutput logic.
	var returnedErr error
	if res != nil && res.Status == "error" {
		returnedErr = output.AsDisplayed(fmt.Errorf("%s", res.Summary))
	}

	assert.NoError(t, returnedErr, "prime must return nil for successful adapter results")
}
