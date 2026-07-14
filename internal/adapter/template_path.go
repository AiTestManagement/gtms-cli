package adapter

import (
	"path/filepath"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
)

// ResolveTemplatePath returns the absolute path to the role-specific template
// file for the given command and adapter name. ENH-161: template paths are
// derived from the resolved adapter name so switching defaults in gtms.config
// routes to the correct template with zero source edits.
//
// Mapping:
//
//	create + manual-create[-script] -> gtms/test/templates/manual-testcase.template.md
//	create + agent-create[-script]  -> gtms/test/templates/agent-testcase.template.md
//	prime  + manual-prime[-script]  -> gtms/manual/templates/manual-result.template.yaml
//	prime  + agent-prime[-script]   -> gtms/manual/templates/agent-result.template.yaml
func ResolveTemplatePath(projectRoot, command, adapterName string) string {
	// Strip -script suffix for matching (built-in and script adapters
	// share the same template).
	baseName := strings.TrimSuffix(adapterName, "-script")

	switch command {
	case "create":
		role := "manual"
		if strings.HasPrefix(baseName, "agent-") {
			role = "agent"
		}
		return filepath.Join(layout.TestTemplatesDir(projectRoot), role+"-testcase.template.md")
	case "prime":
		role := "manual"
		if strings.HasPrefix(baseName, "agent-") {
			role = "agent"
		}
		return filepath.Join(layout.ManualTemplatesDir(projectRoot), role+"-result.template.yaml")
	default:
		return ""
	}
}
