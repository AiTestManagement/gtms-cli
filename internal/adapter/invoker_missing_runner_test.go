package adapter

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/aitestmanagement/gtms-cli/internal/task"
)

// TestInvokeWithRoot_MissingExecuteAdapter_TypedErrorSurvives pins BUG-171
// item 1: after InvokeWithRoot returns on the missing-execute-adapter path,
// the typed *MissingExecuteAdapterError must still be discoverable via
// errors.As. The invoker used to rebuild the error with fmt.Errorf("%s", ...),
// severing the unwrap chain; this test guards against that regression.
//
// It also asserts the STORED task/result summary strings are unchanged (they
// still carry the internal "Adapter invocation failed: resolving canonical
// execute adapter:" chain), so the presentation fix does not disturb the
// diagnostic lifecycle.
func TestInvokeWithRoot_MissingExecuteAdapter_TypedErrorSurvives(t *testing.T) {
	skipIfShort(t)

	// The fixture configures execute adapters for playwright and cypress only.
	// Requesting a bats automation therefore has no canonical execute adapter.
	root, cfg := setupAutomateTestProject(t)

	resolved := &ResolvedAdapter{
		Command: "automate", Name: "agent-automate",
		Config: &config.AdapterConfig{Mode: "sync"},
		Tier:   0, Mode: "sync",
	}
	flags := CommandFlags{Framework: "bats"}

	_, err := InvokeWithRoot(context.Background(), root, cfg, resolved, "tc-auto", flags)
	require.Error(t, err)

	// Typed identity survives the invoker.
	var mae *MissingExecuteAdapterError
	require.True(t, errors.As(err, &mae),
		"typed *MissingExecuteAdapterError must be discoverable via errors.As on the InvokeWithRoot error; got %q", err.Error())
	assert.Equal(t, "bats", mae.Framework)
	assert.NotNil(t, mae.Configured, "configured inventory should be carried on the typed error")
	assert.Contains(t, mae.Error(), "no execute adapter configured for framework",
		"stable lowercase substring must be retained for lower-level regression pins")

	// The surfaced (== stored) summary keeps the internal chain unchanged.
	const wantPrefix = "Adapter invocation failed: resolving canonical execute adapter:"
	assert.True(t, strings.HasPrefix(err.Error(), wantPrefix),
		"returned error message (== stored summary) should be unchanged; got %q", err.Error())

	// Stored task summary unchanged and task lifecycle moved to error.
	tf, ferr := task.FindByTarget(root, "automate", "tc-auto", "error")
	require.NoError(t, ferr)
	require.NotNil(t, tf, "an automate error task should have been produced")
	assert.Equal(t, "error", tf.Status)
	assert.True(t, strings.HasPrefix(tf.Error, wantPrefix),
		"stored task Error should be unchanged; got %q", tf.Error)

	// Stored result contract unchanged: status error, same summary.
	rc, rerr := result.Read(result.ResultPath(root, tf.ID))
	require.NoError(t, rerr)
	assert.Equal(t, "error", rc.Status)
	assert.True(t, strings.HasPrefix(rc.Summary, wantPrefix),
		"stored result summary should be unchanged; got %q", rc.Summary)
}
