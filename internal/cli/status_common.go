package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aitestmanagement/gtms-cli/internal/adapter"
	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/output"
	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/aitestmanagement/gtms-cli/internal/task"
	"github.com/aitestmanagement/gtms-cli/internal/testcase"
)

// surfaceAsyncRefusal renders a BUG-209 refusal returned by checkAsyncStatus to
// stderr (section-10 wording) and returns a non-zero, already-displayed error so
// an async status poll reports a refused handoff and exits non-zero instead of
// returning silently (AC29 / tc-d7abf7ae / section 8). A nil or non-refusal error
// returns nil, preserving the prior silent behaviour.
func surfaceAsyncRefusal(err error) error {
	return surfaceAsyncRefusals([]error{err})
}

// surfaceAsyncRefusals reports each distinct refusal once, and returns the displayed
// non-zero error when there was at least one. A list command collects refusals while
// it polls every task, renders its list, and only then calls this, so one unsafe
// handoff neither hides the other tasks nor stops them being polled (section 8,
// CLAUDE-007).
func surfaceAsyncRefusals(errs []error) error {
	seen := map[string]bool{}
	for _, err := range errs {
		if err == nil {
			continue
		}
		var rh *result.RefusedHandoffError
		var rd *result.RefusedDirError
		switch {
		case errors.As(err, &rh):
			if !seen[rh.Rel] {
				seen[rh.Rel] = true
				output.FprintError(os.Stderr, rh.FirstLine(), rh.Remediation())
			}
		case errors.As(err, &rd):
			if !seen[rd.Rel] {
				seen[rd.Rel] = true
				output.FprintError(os.Stderr, rd.FirstLine(), rd.Remediation())
			}
		default:
			// A concurrency error or a failed move: named as it is, with no unsafe-file
			// remediation, because the file itself is not the problem (section 9).
			if !seen[err.Error()] {
				seen[err.Error()] = true
				output.FprintError(os.Stderr, err.Error(), "")
			}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	return output.AsDisplayed(fmt.Errorf("status: the task's handoff could not be safely read or updated"))
}

// listedHandoffRefusals inspects the handoffs of every listed task through one scan,
// whatever the task's state and whether or not it can be polled, so a finished task or
// a task with no status script is not listed as if its handoff were fine (section 8,
// CODEX-003). A refused entry belonging to no listed task is left to the scanning
// commands, because this list only speaks for its own tasks.
func listedHandoffRefusals(projectRoot string, tasks []*task.TaskFile) []error {
	scan := result.OpenScan(projectRoot)
	defer scan.Close()
	if scan.DirRefusal != nil {
		return []error{scan.DirRefusal}
	}
	listed := make(map[string]bool, len(tasks))
	for _, tf := range tasks {
		listed[filepath.Base(result.ResultPath(projectRoot, tf.ID))] = true
	}
	var refusals []error
	for _, ref := range scan.Refusals {
		if listed[ref.Base] {
			refusals = append(refusals, ref.Err)
		}
	}
	return refusals
}

// checkAsyncStatus runs the status-script for an async in-progress task to poll for completion.
// If the task has completed, it processes the result and updates the task file.
// The command parameter specifies which adapter config section to look up (create, automate, execute).
func checkAsyncStatus(ctx context.Context, projectRoot string, cfg *config.Config, tf *task.TaskFile, command string) error {
	if cfg == nil {
		return nil
	}

	// Look up the adapter config to find the status-script
	adapters, ok := cfg.Adapters[command]
	if !ok {
		return nil
	}
	adapterCfg, ok := adapters[tf.Adapter]
	if !ok {
		return nil
	}
	if adapterCfg.StatusScript == "" {
		return nil
	}

	resultPath := result.ResultPath(projectRoot, tf.ID)

	// BUG-209 section 8: refuse before running the status script. The script is handed
	// this path and would write through an unsafe leaf.
	if _, readErr := result.Read(resultPath); result.IsRefusal(readErr) {
		return readErr
	}

	// Build adapter context for the status-script
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
		// The status check failing stays silent, unless the handoff is now refused,
		// which may be why it failed (CLAUDE-006).
		if _, readErr := result.Read(resultPath); result.IsRefusal(readErr) {
			return readErr
		}
		return nil // Status check failed silently
	}

	// Read the updated result contract.
	rc, err := result.Read(resultPath)
	if err != nil {
		// BUG-209 (AC29 / tc-d7abf7ae): an unsafe handoff leaf (e.g. a symlink
		// swapped in between the run and this poll) is refused rather than read
		// through. Surface the refusal so `gtms execute status` reports it and
		// exits non-zero, instead of returning silently. Other read errors stay
		// silent, as before.
		if result.IsRefusal(err) {
			return err
		}
		return nil
	}

	// ENH-130: validate script-written contracts at the async read boundary.
	if validateErr := result.Validate(rc); validateErr != nil {
		tf.Error = fmt.Sprintf("adapter wrote invalid contract: %v", validateErr)
		// BUG-209 section 9: through the shared rule.
		if updateErr := adapter.PersistTerminal(resultPath, map[string]interface{}{
			"status":  "error",
			"result":  "",
			"summary": tf.Error,
		}, nil); updateErr != nil {
			return adapter.RouteTerminalFailure(projectRoot, tf, updateErr)
		}
		if moveErr := task.Move(projectRoot, tf, "error"); moveErr != nil {
			return fmt.Errorf("moving task %s to error/: %w", tf.ID, moveErr)
		}
		return nil
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
		if rc.Status == "complete" || rc.Status == "error" {
			// BUG-209 section 9: the capped document is the final outcome, so nothing is
			// finalised unless it lands. rc is the complete contract to fall back on.
			if updateErr := adapter.PersistTerminal(resultPath, capUpdate, rc); updateErr != nil {
				return adapter.RouteTerminalFailure(projectRoot, tf, updateErr)
			}
		} else if updateErr := adapter.PersistTerminal(resultPath, capUpdate, nil); updateErr != nil {
			// REV-279 CLAUDE-002: the adapter is still running and owns this file, so a
			// failed cap is only a warning. With no canonical contract nothing is
			// reconstructed over its handoff, the task stays in-progress, no record is
			// written, and a later poll retries and finalises the terminal result.
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
			// No record for a task that did not finalise (section 9).
			return fmt.Errorf("moving task %s to complete/: %w", tf.ID, moveErr)
		}
		// CON-023 / ENH-145 / ENH-146: async finalize goes through the
		// same wiring writer the synchronous path uses. The "execute"
		// case writes only gtms/execution/*.results.yaml — wiring is
		// immutable on the execute path.
		switch command {
		case "automate":
			// ADR-024 s5: the async finalise is its own operation in its own process,
			// so it resolves the owner here, ONCE, and hands it to the wiring writer.
			// The writer never sees the bare id. A failure to prove ownership is a
			// refusal to stamp a testcase-hash against an identity we cannot pin down,
			// not a licence to write wiring from whatever the id happens to match now.
			owner, ownErr := testcase.ResolveOwner(projectRoot, tf.Target)
			if ownErr != nil {
				output.Warnf(fmt.Sprintf("Wiring not written for %s: %v", tf.ID, ownErr))
				break
			}
			// ENH-191: WriteAutomateWiring now returns (adapterName, warnings, error).
			wiringAdapter, wireWarns, pErr := adapter.WriteAutomateWiring(projectRoot, GetConfig(), tf, rc, owner)
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
			return fmt.Errorf("moving task %s to error/: %w", tf.ID, moveErr)
		}
	}
	return nil
}
