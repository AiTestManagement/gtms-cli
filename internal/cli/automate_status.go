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

// newAutomateStatusCmd builds the 'gtms automate status' subcommand.
func newAutomateStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status [target]",
		Short: "Show status of automate tasks",
		Long: `Show the status of automate tasks.

  gtms automate status          — list all automate tasks
  gtms automate status tc-a1b2c3d4   — detail for a specific target`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			root := GetProjectRoot()
			cfg := GetConfig()

			if len(args) > 0 {
				return runAutomateStatusDetail(ctx, os.Stdout, root, cfg, strings.ToLower(args[0]))
			}
			return runAutomateStatusList(ctx, os.Stdout, root, cfg)
		},
	}
}

// runAutomateStatusList displays a table of all automate tasks.
// For async in-progress tasks with a status-script, it polls the remote system.
func runAutomateStatusList(ctx context.Context, w io.Writer, projectRoot string, cfg *config.Config) error {
	tasks, err := task.List(projectRoot)
	if err != nil {
		return fmt.Errorf("listing tasks: %w", err)
	}

	// Filter to automate tasks only
	var automateTasks []*task.TaskFile
	for _, tf := range tasks {
		if tf.Type == "automate" {
			automateTasks = append(automateTasks, tf)
		}
	}

	if len(automateTasks) == 0 {
		fmt.Fprintln(w, "No automate tasks found.")
		return nil
	}

	// Check async in-progress tasks for status updates
	for _, tf := range automateTasks {
		if tf.Status == "in-progress" {
			checkAsyncStatus(ctx, projectRoot, cfg, tf, "automate")
		}
	}

	// Re-list after potential status updates
	tasks, err = task.List(projectRoot)
	if err != nil {
		return fmt.Errorf("re-listing tasks: %w", err)
	}

	automateTasks = nil
	for _, tf := range tasks {
		if tf.Type == "automate" {
			automateTasks = append(automateTasks, tf)
		}
	}

	tbl := output.NewTable("TARGET", "STATUS", "TASK", "ADAPTER", "FRAMEWORK", "BRANCH")
	for _, tf := range automateTasks {
		statusLabel := formatTaskStatus(tf.Status)
		tbl.AddRow(
			tf.Target,
			statusLabel,
			tf.ID,
			tf.Adapter,
			tf.Framework,
			tf.Branch,
		)
	}

	tbl.Render(w)
	return nil
}

// runAutomateStatusDetail shows detail for a specific automate target.
func runAutomateStatusDetail(ctx context.Context, w io.Writer, projectRoot string, cfg *config.Config, target string) error {
	// Search across all statuses
	tf, err := task.FindByTarget(projectRoot, "automate", target, task.ValidStatuses...)
	if err != nil {
		return fmt.Errorf("finding task: %w", err)
	}

	if tf == nil {
		fmt.Fprintf(w, "No automate task found for target '%s'.\n", target)
		return nil
	}

	// Check async in-progress for status updates
	if tf.Status == "in-progress" {
		checkAsyncStatus(ctx, projectRoot, cfg, tf, "automate")
		// Re-read in case it was updated
		tf2, err := task.FindByTarget(projectRoot, "automate", target, task.ValidStatuses...)
		if err == nil && tf2 != nil {
			tf = tf2
		}
	}

	// Print detail
	fmt.Fprintf(w, "Target:    %s\n", tf.Target)
	fmt.Fprintf(w, "Task:      %s\n", tf.ID)
	fmt.Fprintf(w, "Status:    %s\n", formatTaskStatus(tf.Status))
	fmt.Fprintf(w, "Adapter:   %s\n", tf.Adapter)
	fmt.Fprintf(w, "Branch:    %s\n", tf.Branch)
	fmt.Fprintf(w, "Created:   %s\n", tf.Created)

	if tf.Framework != "" {
		fmt.Fprintf(w, "Framework: %s\n", tf.Framework)
	}
	if tf.Reference != "" {
		fmt.Fprintf(w, "Reference: %s\n", tf.Reference)
	}
	if tf.Error != "" {
		fmt.Fprintf(w, "Error:     %s\n", tf.Error)
	}

	// If there's a result contract, show additional info
	rcPath := result.ResultPath(projectRoot, tf.ID)
	rc, err := result.Read(rcPath)
	if err == nil {
		if rc.Summary != "" {
			fmt.Fprintf(w, "Summary:   %s\n", rc.Summary)
		}
		if rc.Artefact != "" {
			fmt.Fprintf(w, "Artefact:  %s\n", rc.Artefact)
		}
		if rc.Completed != "" {
			fmt.Fprintf(w, "Completed: %s\n", rc.Completed)
		}
	}

	// ENH-109 / verbose: surface the handoff contract path consulted, so users
	// debugging an automate can find the on-disk artefact without grepping
	// .gtms/. Only printed under --verbose to keep the default view compact.
	if IsVerbose() && rcPath != "" {
		fmt.Fprintf(w, "Handoff:   %s\n", rcPath)
	}

	return nil
}
