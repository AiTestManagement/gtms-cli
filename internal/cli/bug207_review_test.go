package cli

import (
	"strings"
	"testing"

	"github.com/aitestmanagement/gtms-cli/internal/scaffold"
	"github.com/stretchr/testify/assert"
)

// CLAUDE-001 (REV-249, BUG-207): the trailing "Wire your AI agent in" CTA names
// AGENTS-SNIPPET.md under the active GTMS folder, which is where Init wrote it. In
// a renamed project gtms/AGENTS-SNIPPET.md does not exist, so pointing at it was wrong.
func TestBUG207_CLAUDE001_InitCTAUsesActiveParent(t *testing.T) {
	t.Run("renamed parent", func(t *testing.T) {
		out := captureStderr(t, func() {
			formatInitOutput(t.TempDir(), "Renamed", "org/renamed", "manual",
				&scaffold.Result{Parent: "testing"}, false)
		})
		assert.Contains(t, out, "paste testing/AGENTS-SNIPPET.md",
			"the CTA must name the snippet under the active renamed folder")
		assert.NotContains(t, out, "paste gtms/AGENTS-SNIPPET.md",
			"the CTA must not point at a gtms/ file that does not exist in a renamed project")
	})

	t.Run("default layout", func(t *testing.T) {
		out := captureStderr(t, func() {
			formatInitOutput(t.TempDir(), "Default", "org/default", "manual",
				&scaffold.Result{Parent: "gtms"}, false)
		})
		assert.Contains(t, out, "paste gtms/AGENTS-SNIPPET.md",
			"the default layout keeps the canonical CTA")
	})

	t.Run("empty parent falls back to gtms", func(t *testing.T) {
		out := captureStderr(t, func() {
			formatInitOutput(t.TempDir(), "Fallback", "org/fallback", "manual",
				&scaffold.Result{}, false)
		})
		assert.True(t, strings.Contains(out, "paste gtms/AGENTS-SNIPPET.md"),
			"an unset Parent defaults to gtms")
	})
}
