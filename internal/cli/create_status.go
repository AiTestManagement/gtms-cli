package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/output"
	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/aitestmanagement/gtms-cli/internal/task"
)

// newCreateStatusCmd builds the 'gtms create status' subcommand.
func newCreateStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status [target]",
		Short: "Show status of create tasks",
		Long: `Show the status of create tasks.

  gtms create status              -- list all create tasks
  gtms create status my-feature   -- detail for a specific target
`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			root := GetProjectRoot()
			cfg := GetConfig()

			if len(args) > 0 {
				return runCreateStatusDetail(ctx, os.Stdout, root, cfg, args[0])
			}
			return runCreateStatusList(ctx, os.Stdout, root, cfg)
		},
	}
}

// runCreateStatusList displays a table of all create tasks.
// For async in-progress tasks with a status-script, it polls the remote system.
func runCreateStatusList(ctx context.Context, w io.Writer, projectRoot string, cfg *config.Config) error {
	tasks, err := task.List(projectRoot)
	if err != nil {
		return fmt.Errorf("listing tasks: %w", err)
	}

	// Filter to create tasks only
	var createTasks []*task.TaskFile
	for _, tf := range tasks {
		if tf.Type == "create" {
			createTasks = append(createTasks, tf)
		}
	}

	if len(createTasks) == 0 {
		fmt.Fprintln(w, "No create tasks found.")
		return nil
	}

	// Check async in-progress tasks for status updates
	for _, tf := range createTasks {
		if tf.Status == "in-progress" {
			checkAsyncStatus(ctx, projectRoot, cfg, tf, "create")
		}
	}

	// Re-list after potential status updates
	tasks, err = task.List(projectRoot)
	if err != nil {
		return fmt.Errorf("re-listing tasks: %w", err)
	}

	createTasks = nil
	for _, tf := range tasks {
		if tf.Type == "create" {
			createTasks = append(createTasks, tf)
		}
	}

	tbl := output.NewTable("TARGET", "STATUS", "TASK", "ADAPTER", "BRANCH")
	for _, tf := range createTasks {
		statusLabel := formatTaskStatus(tf.Status)
		tbl.AddRow(
			tf.Target,
			statusLabel,
			tf.ID,
			tf.Adapter,
			tf.Branch,
		)
	}

	tbl.Render(w)
	return nil
}

// runCreateStatusDetail shows detail for a specific create target.
func runCreateStatusDetail(ctx context.Context, w io.Writer, projectRoot string, cfg *config.Config, target string) error {
	// Search across all statuses
	tf, err := task.FindByTarget(projectRoot, "create", target, task.ValidStatuses...)
	if err != nil {
		return fmt.Errorf("finding task: %w", err)
	}

	if tf == nil {
		fmt.Fprintf(w, "No create task found for target '%s'.\n", target)
		return nil
	}

	// Check async in-progress for status updates
	if tf.Status == "in-progress" {
		checkAsyncStatus(ctx, projectRoot, cfg, tf, "create")
		// Re-read in case it was updated
		tf2, err := task.FindByTarget(projectRoot, "create", target, task.ValidStatuses...)
		if err == nil && tf2 != nil {
			tf = tf2
		}
	}

	// Print detail
	fmt.Fprintf(w, "Target:  %s\n", tf.Target)
	fmt.Fprintf(w, "Task:    %s\n", tf.ID)
	fmt.Fprintf(w, "Status:  %s\n", formatTaskStatus(tf.Status))
	fmt.Fprintf(w, "Adapter: %s\n", tf.Adapter)
	fmt.Fprintf(w, "Branch:  %s\n", tf.Branch)
	fmt.Fprintf(w, "Created: %s\n", tf.Created)

	if tf.Error != "" {
		fmt.Fprintf(w, "Error:   %s\n", tf.Error)
	}

	// If there's a result contract, show additional info
	rcPath := result.ResultPath(projectRoot, tf.ID)
	rc, err := result.Read(rcPath)
	if err == nil {
		if rc.Summary != "" {
			fmt.Fprintf(w, "Summary: %s\n", rc.Summary)
		}
		if rc.Artefact != "" {
			fmt.Fprintf(w, "Artefact: %s\n", rc.Artefact)
		}
		if rc.Completed != "" {
			fmt.Fprintf(w, "Completed: %s\n", rc.Completed)
		}

		// ENH-096 Gap 1: list generated TC ids + titles when the task is complete
		if tf.Status == "complete" && rc.Artefact != "" {
			paths := strings.Split(rc.Artefact, ",")
			for i := range paths {
				paths[i] = strings.TrimSpace(paths[i])
			}
			entries := make([]tcInfo, 0, len(paths))
			for _, relPath := range paths {
				id, title := readTCFrontmatter(projectRoot, relPath)
				if id != "" {
					entries = append(entries, tcInfo{id: id, title: title})
				}
			}
			if len(entries) > 0 {
				n := len(entries)
				label := "test case"
				if n > 1 {
					label = "test cases"
				}
				fmt.Fprintf(w, "    Created %d %s:\n", n, label)
				for _, e := range entries {
					fmt.Fprintf(w, "      %s  %s\n", e.id, truncateTitle(e.title))
				}
			}
		}

		// ENH-096 Gap 3: surface adapter-injected warnings from the result contract
		for _, warn := range rc.Warnings {
			fmt.Fprintf(w, "  %s %s\n", output.IconWarning, warn)
		}
	}

	return nil
}

// formatTaskStatus returns a human-readable status label with icon.
func formatTaskStatus(status string) string {
	icon := output.StatusIcon(status)
	switch status {
	case "complete":
		return icon + " Complete"
	case "in-progress":
		return icon + " In Progress"
	case "pending":
		return icon + " Pending"
	case "error":
		return icon + " Error"
	case "in-review":
		return icon + " In Review"
	default:
		return icon + " " + status
	}
}
