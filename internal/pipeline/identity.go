// identity.go provides the executed-by precedence resolver introduced by
// ENH-125. The chain is:
//
//   1. CLI flag (--executed-by)
//   2. Environment variable (GTMS_EXECUTED_BY)
//   3. git config user.name from the project repo
//
// An empty result is acceptable — the field stays unset on records and the
// YAML marshaller omits the key via `omitempty`.
package pipeline

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

// ResolveExecutedBy applies the ENH-125 precedence chain to determine the
// identity to record on permanent records. The function never returns an
// error: any failure (no git, no user.name configured, etc.) yields an
// empty string and the caller writes nothing.
//
// projectRoot is used as the working directory for the git invocation so
// repository-local config overrides global config consistently.
func ResolveExecutedBy(ctx context.Context, projectRoot, flag string) string {
	if v := strings.TrimSpace(flag); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("GTMS_EXECUTED_BY")); v != "" {
		return v
	}
	cmd := exec.CommandContext(ctx, "git", "config", "user.name")
	if projectRoot != "" {
		cmd.Dir = projectRoot
	}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
