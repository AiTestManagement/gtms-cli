package adapter

// BUG-209 section 9, the shared terminal persistence rule (owner decision, after
// REV-278). This file is the only place a terminal handoff write happens. No Tier 1,
// Tier 2, async, recovery or reserved-adapter path reports a pass, moves a task to
// complete/ or writes an execution record unless its final trusted outcome was durably
// persisted.
//
// Exit-path inventory. TestSourceShape_TerminalWritesUseTheSharedRule fails if a direct
// handoff write appears outside this list.
//
//	Pre-invocation writes in invoker.go, still direct, because no outcome exists yet and
//	a failure stops the adapter:
//	  framework from the manual template, cleanup failure, in-progress.
//
//	Terminal writes, all through persistTerminal or persistTerminalDetail:
//	  manual-execute validation error    fails the run, no record
//	  adapter invocation failure         error route, reports the write failure
//	  timeout or cancellation            error route, reports the write failure
//	  create-validation rejection        recovery on an ordinary failure
//	  Tier 2 invalid contract, reserved  canonical contract may be reconstructed
//	  Tier 2 invalid contract, other     fails the run
//	  Tier 2 restore, reserved           canonical contract may be reconstructed
//	  Tier 2 restore, other              canonical contract may be reconstructed
//	  Tier 1 exit 0                      fails the run
//	  Tier 1 non-zero exit               fails the run
//	  ENH-188 rejected run               canonical contract may be reconstructed
//	  rejected multi-file automate       fails the run on a refusal or concurrency error
//	  async validation error, async cap  through PersistTerminal, from internal/cli; a cap
//	                                     on a still-running handoff only warns, with no
//	                                     reconstruct, and a later poll finalises it
//
//	Reconstruct, after an ordinary failure only, against a state captured beforehand:
//	  persistTerminalDetail, and recoverUnusableContract (BUG-191), which refuses first if
//	  the read that sent it there was a refusal.

import (
	"fmt"

	"github.com/aitestmanagement/gtms-cli/internal/result"
	"github.com/aitestmanagement/gtms-cli/internal/task"
)

// persistUpdate and persistReconstruct are the writers used here. They are variables so
// tests can inject a failure. In production they are the result package's writers.
var (
	persistUpdate      = result.Update
	persistReconstruct = result.ReconstructIfUnchanged
)

// blocksReconstruct reports whether a write failure must fail the run without any
// reconstruct: a safety refusal, or a concurrency error, where a reconstruct could
// overwrite the newer writer's file.
func blocksReconstruct(err error) bool {
	return result.IsRefusal(err) || result.IsConcurrentUpdate(err)
}

// persistTerminalDetail applies updates to the handoff. It returns nil errors once they
// are durably written. It reconstructs only after an ordinary failure, only when the
// caller holds a complete canonical contract, and only if the handoff is still the one
// captured before the write. It never reconstructs after a refusal or a concurrency
// error. With no updates there is nothing to persist.
func persistTerminalDetail(path string, updates map[string]interface{}, canonical *result.ResultContract) (updateErr, reconstructErr error) {
	if len(updates) == 0 {
		return nil, nil
	}
	state, stateErr := result.CaptureHandoffState(path)
	if stateErr != nil && result.IsRefusal(stateErr) {
		return stateErr, nil
	}
	updateErr = persistUpdate(path, updates)
	if updateErr == nil || blocksReconstruct(updateErr) || canonical == nil {
		return updateErr, nil
	}
	if stateErr != nil {
		return updateErr, fmt.Errorf("the handoff's state could not be captured: %w", stateErr)
	}
	return updateErr, persistReconstruct(path, canonical, state)
}

// PersistTerminal is persistTerminalDetail as one error: nil means durably persisted.
// Any other result means the outcome did not land, so the caller must not report a pass,
// move the task to complete/ or write an execution record.
func PersistTerminal(path string, updates map[string]interface{}, canonical *result.ResultContract) error {
	updateErr, reconstructErr := persistTerminalDetail(path, updates, canonical)
	switch {
	case updateErr == nil:
		return nil
	case canonical == nil || blocksReconstruct(updateErr):
		return updateErr
	case reconstructErr == nil:
		return nil
	default:
		return fmt.Errorf("could not persist the terminal handoff: update: %v; reconstruct: %w", updateErr, reconstructErr)
	}
}

// persistTerminal is PersistTerminal for this package.
func persistTerminal(path string, updates map[string]interface{}, canonical *result.ResultContract) error {
	return PersistTerminal(path, updates, canonical)
}

// RouteTerminalFailure files a task under error/ after its terminal outcome could not be
// persisted, and returns the failure, combined with any failure of the move. No
// execution record is written. Its caller returns straight after.
func RouteTerminalFailure(projectRoot string, tf *task.TaskFile, cause error) error {
	return failBeforeInvocation(projectRoot, tf, cause)
}

// failAfterInvocation is RouteTerminalFailure inside this package. That the adapter ran
// is added by withPostRunSignal, on every error return from handleSyncResult.
func failAfterInvocation(projectRoot string, tf *task.TaskFile, cause error) error {
	return RouteTerminalFailure(projectRoot, tf, cause)
}

// moveTaskToError moves a task to error/. A failure is returned as a message naming what
// could not be done, because the task stays active and can block the next run.
func moveTaskToError(projectRoot string, tf *task.TaskFile) error {
	if err := task.Move(projectRoot, tf, "error"); err != nil {
		return fmt.Errorf("could not move task to error/: %w", err)
	}
	return nil
}

// failBeforeInvocation routes a task to error/ for a failure, and returns the cause
// combined with any failure of that move. errors.Is and errors.As still see the cause.
func failBeforeInvocation(projectRoot string, tf *task.TaskFile, cause error) error {
	tf.Error = cause.Error()
	if moveErr := moveTaskToError(projectRoot, tf); moveErr != nil {
		return fmt.Errorf("%w (and %v)", cause, moveErr)
	}
	return cause
}

// withPostRunSignal marks any failure after the adapter ran with its real start state,
// so the command can say the run started and then failed. It does not depend on
// script-drift flags. The cause stays reachable through Unwrap, so result.IsRefusal still
// matches.
func withPostRunSignal(invResult *InvocationResult, err error) error {
	if err == nil {
		return nil
	}
	var already *invocationError
	if asInvocationError(err, &already) {
		return err
	}
	return &invocationError{summary: err.Error(), err: err, started: invResult != nil && invResult.Started}
}
