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
//
// ADR-024 5b / REV-184 CODEX-004: the bulk readers dispatched here return a
// typed *reader.IncompleteReadError (and no rows) when any test-case identity is
// Ambiguous or Unprovable. The dispatcher has no stderr channel for per-identity
// diagnostics, so it propagates that error unchanged rather than returning a
// partial result as success; callers can detect it with errors.As.
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
		// ENH-200 / CON-040: triage is a mutation command (--value / --resolve)
		// handled directly by the CLI RunE, not a built-in reader dispatch. The
		// old read-only current-state model (reader.Triage -> TriageInfo) is
		// retired; the supported read surface (show/list) is ENH-201.
		return nil, fmt.Errorf("triage is a mutation command; run 'gtms triage <test-case-id> --value <key>' or '--resolve'")

	default:
		return nil, fmt.Errorf("unknown built-in command: %s", command)
	}
}
