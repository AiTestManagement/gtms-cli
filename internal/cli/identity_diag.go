package cli

import (
	"fmt"
	"os"

	"github.com/aitestmanagement/gtms-cli/internal/output"
	"github.com/aitestmanagement/gtms-cli/internal/reader"
	"github.com/aitestmanagement/gtms-cli/internal/result"
)

// renderHandoffRefusals writes the BUG-209 refusal diagnostics to stderr in the
// section-10 wording -- the results-directory refusal first, then each refused
// handoff once per path -- so reset/delete name every unsafe entry they left
// untouched. The glyph and indentation follow the CLI's existing error format.
func renderHandoffRefusals(dir *result.RefusedDirError, handoffs []*result.RefusedHandoffError) {
	if dir != nil {
		output.FprintError(os.Stderr, dir.FirstLine(), dir.Remediation())
	}
	seen := make(map[string]bool, len(handoffs))
	for _, h := range handoffs {
		if seen[h.Rel] {
			continue
		}
		seen[h.Rel] = true
		output.FprintError(os.Stderr, h.FirstLine(), h.Remediation())
	}
}

// finishWithIdentityDiagnostics implements ADR-024 5b policy (e) at the CLI
// boundary for a migrated bulk read/list command. The command writes its normal
// (partial) output to stdout first; then it calls this with the operation's
// identity diagnostics. When any identity was affected, the per-identity
// diagnostics and the closing line are written to stderr and a displayed error is
// returned so the process exits non-zero with stdout intact. When nothing was
// affected it returns nil and the command exits 0, unchanged from before ENH-205.
func finishWithIdentityDiagnostics(diags reader.IdentityDiagnostics) error {
	if !diags.Affected() {
		return nil
	}
	diags.Render(os.Stderr)
	return output.AsDisplayed(fmt.Errorf("read incomplete: some test-case identities could not be proved"))
}

// failWithIdentityDiagnostics is the error-path counterpart of
// finishWithIdentityDiagnostics: when a command fails AFTER its read (for
// example, writing the output fails), the identity diagnostics are still
// written to stderr, and the original error is returned unchanged so the exit
// stays non-zero with that error (REV-184 round 7).
func failWithIdentityDiagnostics(diags reader.IdentityDiagnostics, err error) error {
	if diags.Affected() {
		diags.Render(os.Stderr)
	}
	return err
}
