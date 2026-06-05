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
		return fmt.Sprintf("%d %s created in gtms/cases/%s/", len(res.ArtifactPaths), noun, res.Target)
	}
	if res.ArtifactCount > 0 {
		return fmt.Sprintf("%d test cases created in gtms/cases/%s/", res.ArtifactCount, res.Target)
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
func whatHappenedInit(result *scaffold.Result) string {
	var lines []string
	for _, f := range result.FilesCreated {
		lines = append(lines, fmt.Sprintf("Created %s", f))
	}
	for _, d := range result.DirsCreated {
		lines = append(lines, fmt.Sprintf("Created %s/", d))
	}
	for _, f := range result.FilesSkipped {
		lines = append(lines, fmt.Sprintf("Skipped %s (already exists)", f))
	}
	switch result.GitignoreAction {
	case scaffold.GitignoreCreated:
		lines = append(lines, "Created .gitignore")
	case scaffold.GitignoreAppended:
		lines = append(lines, "Updated .gitignore (added .gtms/)")
	}
	return strings.Join(lines, "\n")
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
		return fmt.Sprintf("%d automations created in gtms/cases/%s/", succeeded, folder)
	}
	return fmt.Sprintf("%d automated, %d skipped, %d failed in gtms/cases/%s/",
		succeeded, skipped, failed, folder)
}

// whatHappenedBulkExecute builds the "What happened" text for bulk execute.
func whatHappenedBulkExecute(folder string, passed, skipped, failed, errored int) string {
	if failed == 0 && skipped == 0 && errored == 0 {
		return fmt.Sprintf("All %d tests passed in gtms/cases/%s/", passed, folder)
	}
	return fmt.Sprintf("%d passed, %d failed, %d errored, %d skipped in gtms/cases/%s/",
		passed, failed, errored, skipped, folder)
}
