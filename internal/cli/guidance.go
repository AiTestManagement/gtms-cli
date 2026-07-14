package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aitestmanagement/gtms-cli/internal/adapter"
	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/output"
	"github.com/aitestmanagement/gtms-cli/internal/scaffold"
)

// printGuidance prints the guidance block (What happened + Next + footer) to stderr.
// If guidanceEnabled is false, nothing is printed.
func printGuidance(w io.Writer, command string, whatHappened string, projectRoot string, guidanceEnabled bool) {
	if !guidanceEnabled {
		return
	}

	fmt.Fprintln(w)

	// What happened
	output.Dimln(w, "    What happened:")
	for _, line := range strings.Split(strings.TrimRight(whatHappened, "\n"), "\n") {
		output.Dimln(w, "      "+line)
	}

	// Next
	guidance := config.LoadGuidance(projectRoot)
	if body, ok := guidance[command]; ok && strings.TrimSpace(body) != "" {
		fmt.Fprintln(w)
		output.Dimln(w, "    Next:")
		for _, line := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
			output.Dimln(w, "      "+line)
		}
	}

	// Footer
	fmt.Fprintln(w)
	output.Dimln(w, "    To turn off guidance: gtms init --guidance-off")
}

// whatHappenedCreate builds the "What happened" text for the create command.
func whatHappenedCreate(res *adapter.InvokeResult) string {
	if res.Status == "error" {
		return fmt.Sprintf("Create failed: %s", res.Summary)
	}
	if len(res.ArtifactPaths) > 0 {
		// ENH-096: print a count summary instead of per-file paths to avoid
		// leaking TC id substrings onto stderr (the TC list is on stdout).
		noun := "test cases"
		if len(res.ArtifactPaths) == 1 {
			noun = "test case"
		}
		return fmt.Sprintf("%d %s created in gtms/test/cases/%s/", len(res.ArtifactPaths), noun, res.Target)
	}
	if res.ArtifactCount > 0 {
		return fmt.Sprintf("%d test cases created in gtms/test/cases/%s/", res.ArtifactCount, res.Target)
	}
	return fmt.Sprintf("Create task completed for %s", res.Target)
}

// whatHappenedAutomate builds the "What happened" text for the automate command.
func whatHappenedAutomate(res *adapter.InvokeResult) string {
	if res.Status == "error" {
		return fmt.Sprintf("Automate failed: %s", res.Summary)
	}
	msg := fmt.Sprintf("Automation created for %s", res.Target)
	if res.ArtifactCount > 0 {
		msg += fmt.Sprintf(" (%d files generated)", res.ArtifactCount)
	}
	return msg
}

// whatHappenedPrime builds the "What happened" text for the prime command.
// BUG-080: dedicated helper — uses manual-aware wording, not automate wording.
func whatHappenedPrime(res *adapter.InvokeResult) string {
	if res.Status == "error" {
		return fmt.Sprintf("Prime failed: %s", res.Summary)
	}
	msg := fmt.Sprintf("Manual result template stamped for %s", res.Target)
	if res.ArtifactCount > 0 {
		msg += fmt.Sprintf(" (%d file generated)", res.ArtifactCount)
	}
	return msg
}

// whatHappenedExecute builds the "What happened" text for the execute command.
func whatHappenedExecute(res *adapter.InvokeResult) string {
	if res.Status == "error" {
		return fmt.Sprintf("Execute failed: %s", res.Summary)
	}
	return fmt.Sprintf("Execution completed for %s", res.Target)
}

// whatHappenedInit builds the "What happened" text for the init command.
// ENH-186: returns a count-based summary instead of a per-file enumeration,
// with a deliberate carve-out for the .gitignore action line. The gitignore
// action stays here (not in the stdout Created: block) because the ENH-108
// gitignore-action-reporting spec suite pins it to stderr with exact literals.
func whatHappenedInit(result *scaffold.Result) string {
	files := len(result.FilesCreated)
	dirs := len(result.DirsCreated)

	summary := fmt.Sprintf("Created %d files and %d directories.", files, dirs)
	if len(result.FilesSkipped) > 0 {
		summary += fmt.Sprintf(" Skipped %d files (already exist).", len(result.FilesSkipped))
	}

	// Gitignore carve-out: exact literals pinned by ENH-108 spec suite.
	switch result.GitignoreAction {
	case scaffold.GitignoreCreated:
		summary += "\nCreated .gitignore"
	case scaffold.GitignoreAppended:
		summary += "\nUpdated .gitignore (added .gtms/)"
	}

	return summary
}

// guidanceEnabled returns true if guidance is enabled.
// Safe to call when appConfig is nil (init command).
func guidanceEnabled() bool {
	cfg := GetConfig()
	if cfg == nil {
		return true
	}
	return cfg.Guidance
}

// printCommandGuidance is a convenience wrapper for pipeline commands that
// have access to the global config and project root.
func printCommandGuidance(command string, whatHappened string) {
	printGuidance(os.Stderr, command, whatHappened, GetProjectRoot(), guidanceEnabled())
}

// whatHappenedBulkAutomate builds the "What happened" text for bulk automate.
func whatHappenedBulkAutomate(folder string, succeeded, skipped, failed int) string {
	if failed == 0 && skipped == 0 {
		return fmt.Sprintf("%d automations created in gtms/test/cases/%s/", succeeded, folder)
	}
	return fmt.Sprintf("%d automated, %d skipped, %d failed in gtms/test/cases/%s/",
		succeeded, skipped, failed, folder)
}

// whatHappenedBulkExecute builds the "What happened" text for bulk execute.
func whatHappenedBulkExecute(folder string, passed, skipped, failed, errored int) string {
	if failed == 0 && skipped == 0 && errored == 0 {
		return fmt.Sprintf("All %d tests passed in gtms/test/cases/%s/", passed, folder)
	}
	return fmt.Sprintf("%d passed, %d failed, %d errored, %d skipped in gtms/test/cases/%s/",
		passed, failed, errored, skipped, folder)
}
