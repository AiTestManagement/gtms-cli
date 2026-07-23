package cli

// Source-shape tests for link.go — verify the CLI file is a thin wrapper
// with no business logic (Critical Rule #1).

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSourceShape_LinkCmdNoBusinessLogic(t *testing.T) {
	src, err := os.ReadFile("link.go")
	require.NoError(t, err)
	content := string(src)

	// Must NOT contain direct record/file manipulation or the repoint
	// discovery/selection loop (business logic). CODEX-008: the wiring discovery
	// and scope/adapter selection belong in the core link package, not the Cobra
	// path -- so link.go must not call wiring.DiscoverAll or the per-record
	// mutation primitives directly.
	forbidden := []string{
		"os.MkdirAll",
		"WriteAutomationRecord",
		"ReadAutomationRecord",
		"yaml.Marshal",
		"crypto/rand",
		"wiring.DiscoverAll",
		"link.RepointRecord",
		"link.RepointBatch",
	}
	for _, symbol := range forbidden {
		assert.NotContains(t, content, symbol,
			"link.go must not contain business logic (%s) — that belongs in internal/link/", symbol)
	}

	// Must delegate to the core link package.
	assert.Contains(t, content, "link.LinkRecord",
		"link.go must delegate record creation to link.LinkRecord")
	assert.Contains(t, content, "link.CheckLink",
		"link.go must delegate check validation to link.CheckLink")
	assert.Contains(t, content, "link.RepointBulk",
		"link.go must delegate folder/recursive repoint to link.RepointBulk")
	assert.Contains(t, content, "link.RepointAll",
		"link.go must delegate project-wide repoint to link.RepointAll")
	assert.Contains(t, content, "link.RepointSingle",
		"link.go must delegate single-TC repoint to link.RepointSingle")
}
