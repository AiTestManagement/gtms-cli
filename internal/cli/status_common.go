package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aitestmanagement/gtms-cli/internal/adapter"
	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/output"
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

	// ENH-130: validate script-written contracts at the async read boundary.
	if validateErr := result.Validate(rc); validateErr != nil {
		tf.Error = fmt.Sprintf("adapter wrote invalid contract: %v", validateErr)
		if updateErr := result.Update(resultPath, map[string]interface{}{
			"status":  "error",
			"result":  "",
			"summary": tf.Error,
		}); updateErr != nil {
			output.Warnf(fmt.Sprintf("Recovery contract write failed for %s: %v", tf.ID, updateErr))
		}
		if moveErr := task.Move(projectRoot, tf, "error"); moveErr != nil {
			output.Warnf(fmt.Sprintf("Task move to error/ failed for %s: %v", tf.ID, moveErr))
		}
		return
	}

	// BUG-084: cap oversize log + summary payloads on the result contract
	// before the wiring/results dispatch reads it. Async scripts write the
	// log/summary via heredoc bypassing result.Update, so apply the cap with
	// an explicit result.Update here. Mirrors the sync-Tier-2 producer in
	// internal/adapter/invoker.go.
	logOversize := len(rc.Log) > result.NotesSizeCapBytes
	summaryOversize := len(rc.Summary) > result.SummarySizeCapBytes
	if logOversize || summaryOversize {
		capUpdate := map[string]interface{}{}
		var capErr error
		if logOversize {
			truncated, spill, err := result.ApplyLogCap(projectRoot, tf.ID, rc.Log)
			capUpdate["log"] = truncated
			if spill != "" {
				capUpdate["notes-spill"] = spill
			}
			rc.Log = truncated
			rc.NotesSpill = spill
			capErr = err
		}
		if summaryOversize {
			capped := result.CapSummary(rc.Summary)
			capUpdate["summary"] = capped
			rc.Summary = capped
		}
		if updateErr := result.Update(resultPath, capUpdate); updateErr != nil {
			output.Warnf(fmt.Sprintf("Spill cap write failed for %s: %v", tf.ID, updateErr))
		}
		if capErr != nil {
			output.Warnf(fmt.Sprintf("Log spill write failed for %s: %v", tf.ID, capErr))
		}
	}

	// If complete or error, process the result.
	// ENH-130: task movement decoupled from test outcome.
	if rc.Status == "complete" {
		if moveErr := task.Move(projectRoot, tf, "complete"); moveErr != nil {
			output.Warnf(fmt.Sprintf("Task move to complete/ failed for %s: %v", tf.ID, moveErr))
		}
		// CON-023 / ENH-145 / ENH-146: async finalize goes through the
		// same wiring writer the synchronous path uses. The "execute"
		// case writes only gtms/execution/*.results.yaml — wiring is
		// immutable on the execute path.
		switch command {
		case "automate":
			// ENH-191: WriteAutomateWiring now returns (adapterName, warnings, error).
			wiringAdapter, wireWarns, pErr := adapter.WriteAutomateWiring(projectRoot, GetConfig(), tf, rc)
			for _, w := range wireWarns {
				output.Warnf(w)
			}
			if pErr != nil {
				output.Warnf(fmt.Sprintf("Pipeline record could not be written for %s: %v", tf.ID, pErr))
			}
			// ENH-191 wiring-time report: emit the selected execute adapter under -v.
			if wiringAdapter != "" && IsVerbose() {
				fmt.Fprintf(os.Stderr, "Execute adapter for wiring: %s\n", wiringAdapter)
			}
		case "execute":
			if pErr := adapter.WriteExecuteResultsFile(projectRoot, tf, rc); pErr != nil {
				output.Warnf(fmt.Sprintf("Pipeline record could not be written for %s: %v", tf.ID, pErr))
			}
		}
	} else if rc.Status == "error" {
		tf.Error = rc.Summary
		if moveErr := task.Move(projectRoot, tf, "error"); moveErr != nil {
			output.Warnf(fmt.Sprintf("Task move to error/ failed for %s: %v", tf.ID, moveErr))
		}
	}
}
