package testcase

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	testrequire "github.com/stretchr/testify/require"
)

// bug204ChildEnv gates the in-process child body: when set, the test decodes the
// crafted fixture and returns instead of spawning a grandchild.
const bug204ChildEnv = "GTMS_BUG204_TIMEOUT_CHILD"

// bug204DecodeDeadline is a fixed, CI-tolerant deadline. The current (fixed)
// decoder returns in milliseconds; a regressed decoder would be killed here and
// the test would fail rather than hang the suite.
const bug204DecodeDeadline = 30 * time.Second

// TestBUG204_DecodeCompletesUnderDeadline is the permanent killable
// child-process guard for BUG-204 AC2. It runs only the current implementation:
// a child process decodes the crafted direct-alias fixture, and the parent
// fails (rather than hangs) if that child is not done before the deadline. It
// never builds or runs an old commit; the one-time pre-fix comparison against
// 59d4e7dbe is recorded in PRP-BUG-204.
func TestBUG204_DecodeCompletesUnderDeadline(t *testing.T) {
	if os.Getenv(bug204ChildEnv) == "1" {
		// Child: exercise the heavy key-identity path and return. A hung decoder
		// would never reach the end; the parent's deadline would kill it.
		DecodeCandidate(strictV2Document(nineLevelAliasChain + "  ? *i\n  : refused\n"))
		return
	}

	if testing.Short() {
		t.Skip("child-process timeout guard shells out; skipped in -short")
	}

	ctx, cancel := context.WithTimeout(context.Background(), bug204DecodeDeadline)
	defer cancel()

	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBUG204_DecodeCompletesUnderDeadline$")
	cmd.Env = append(os.Environ(), bug204ChildEnv+"=1")
	err := cmd.Run()

	testrequire.NotEqual(t, context.DeadlineExceeded, ctx.Err(),
		"decode did not finish before the deadline; the child was killed (BUG-204 regression)")
	assert.NoError(t, err, "child decode process exited non-zero")
	// The child has been reaped: Run only returns once the process has exited or
	// been killed, so nothing is left running.
	testrequire.NotNil(t, cmd.ProcessState)
	assert.True(t, cmd.ProcessState.Exited())
}
