package adapter

import (
	"context"
	"fmt"

	"github.com/aitestmanagement/gtms-cli/internal/reader"
)

// InvokeBuiltin dispatches to the appropriate reader function based on the command.
// This is the Tier 0 (built-in) adapter invocation path.
// Context is accepted for API consistency but not propagated to reader functions
// (they are fast filesystem reads, not subprocess calls).
func InvokeBuiltin(ctx context.Context, command string, args []string, projectRoot string, specDirs []string, defaultFramework string) (interface{}, error) {
	switch command {
	case "status":
		// Built-in dispatcher has no flag context, so strictFramework=false:
		// it behaves like the no-flag CLI path (config-default fallback).
		// CLI commands handle their own --framework explicitness (ENH-082).
		if len(args) > 0 {
			return reader.PipelineDetail(projectRoot, args[0], defaultFramework, false)
		}
		return reader.PipelineStatus(projectRoot, nil, defaultFramework, false)

	case "gaps":
		return reader.Gaps(projectRoot, nil, defaultFramework, false)

	case "triage":
		testCaseID := ""
		if len(args) > 0 {
			testCaseID = args[0]
		}
		return reader.Triage(projectRoot, testCaseID)

	default:
		return nil, fmt.Errorf("unknown built-in command: %s", command)
	}
}
