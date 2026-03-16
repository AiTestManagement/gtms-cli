package cli

import (
	"context"
	"path/filepath"

	"github.com/aitestmanagement/gtms-cli/internal/adapter"
	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/pipeline"
	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/aitestmanagement/gtms-cli/internal/task"
)

// checkAsyncStatus runs the status-script for an async in-progress task to poll for completion.
// If the task has completed, it processes the result and updates the task file.
// The command parameter specifies which adapter config section to look up (create, automate, execute).
func checkAsyncStatus(ctx context.Context, projectRoot string, cfg *config.Config, tf *task.TaskFile, command string) {
	if cfg == nil {
		return
	}

	// Look up the adapter config to find the status-script
	adapters, ok := cfg.Adapters[command]
	if !ok {
		return
	}
	adapterCfg, ok := adapters[tf.Adapter]
	if !ok {
		return
	}
	if adapterCfg.StatusScript == "" {
		return
	}

	// Build adapter context for the status-script
	resultPath := result.ResultPath(projectRoot, tf.ID)
	ac := &adapter.AdapterContext{
		TaskID:      tf.ID,
		Command:     command,
		Reference:   tf.Target,
		TestCase:    tf.Target,
		ProjectRoot: projectRoot,
		WorkDir:     projectRoot,
		ResultFile:  resultPath,
	}

	// Run the status-script as a Tier 2 invocation
	scriptPath := adapterCfg.StatusScript
	if !filepath.IsAbs(scriptPath) {
		scriptPath = filepath.Join(projectRoot, scriptPath)
	}

	_, err := adapter.InvokeTier2(ctx, ac, scriptPath)
	if err != nil {
		return // Status check failed silently
	}

	// Read the updated result contract
	rc, err := result.Read(resultPath)
	if err != nil {
		return
	}

	// If complete or error, process the result
	if rc.Status == "complete" {
		_ = task.Move(projectRoot, tf, "complete")
		// Build pipeline records based on command type
		switch command {
		case "automate":
			_ = pipeline.BuildAutomationRecord(projectRoot, tf, rc)
		case "execute":
			_ = pipeline.UpdateExecutionResult(projectRoot, tf, rc)
		}
	} else if rc.Status == "error" {
		tf.Error = rc.Summary
		_ = task.Move(projectRoot, tf, "failed")
	}
}
