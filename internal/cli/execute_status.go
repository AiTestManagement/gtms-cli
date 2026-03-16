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

// newExecuteStatusCmd builds the 'gtms execute status' subcommand.
func newExecuteStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status [target]",
		Short: "Show status of execute tasks",
		Long: `Show the status of execute tasks.

  gtms execute status          — list all execute tasks
  gtms execute status tc-007   — detail for a specific target`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			root := GetProjectRoot()
			cfg := GetConfig()

			if len(args) > 0 {
				return runExecuteStatusDetail(ctx, os.Stdout, root, cfg, strings.ToLower(args[0]))
			}
			return runExecuteStatusList(ctx, os.Stdout, root, cfg)
		},
	}
}

// runExecuteStatusList displays a table of all execute tasks.
// For async in-progress tasks with a status-script, it polls the remote system.
func runExecuteStatusList(ctx context.Context, w io.Writer, projectRoot string, cfg *config.Config) error {
	tasks, err := task.List(projectRoot)
	if err != nil {
		return fmt.Errorf("listing tasks: %w", err)
	}

	// Filter to execute tasks only
	var executeTasks []*task.TaskFile
	for _, tf := range tasks {
		if tf.Type == "execute" {
			executeTasks = append(executeTasks, tf)
		}
	}

	if len(executeTasks) == 0 {
		fmt.Fprintln(w, "No execute tasks found.")
		return nil
	}

	// Check async in-progress tasks for status updates
	for _, tf := range executeTasks {
		if tf.Status == "in-progress" {
			checkAsyncStatus(ctx, projectRoot, cfg, tf, "execute")
		}
	}

	// Re-list after potential status updates
	tasks, err = task.List(projectRoot)
	if err != nil {
		return fmt.Errorf("re-listing tasks: %w", err)
	}

	executeTasks = nil
	for _, tf := range tasks {
		if tf.Type == "execute" {
			executeTasks = append(executeTasks, tf)
		}
	}

	tbl := output.NewTable("TARGET", "STATUS", "TASK", "ADAPTER", "BRANCH")
	for _, tf := range executeTasks {
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

// runExecuteStatusDetail shows detail for a specific execute target.
func runExecuteStatusDetail(ctx context.Context, w io.Writer, projectRoot string, cfg *config.Config, target string) error {
	// Search across all statuses
	tf, err := task.FindByTarget(projectRoot, "execute", target, task.ValidStatuses...)
	if err != nil {
		return fmt.Errorf("finding task: %w", err)
	}

	if tf == nil {
		fmt.Fprintf(w, "No execute task found for target '%s'.\n", target)
		return nil
	}

	// Check async in-progress for status updates
	if tf.Status == "in-progress" {
		checkAsyncStatus(ctx, projectRoot, cfg, tf, "execute")
		// Re-read in case it was updated
		tf2, err := task.FindByTarget(projectRoot, "execute", target, task.ValidStatuses...)
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
	}

	return nil
}

