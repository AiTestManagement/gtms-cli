package wiring

import (
	"os"
	"path/filepath"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
)

// ExecuteOutcome classifies how SelectForExecute resolved wiring for one test
// case on the execute path (BUG-200).
type ExecuteOutcome int

const (
	// ExecuteFound: exactly one usable wiring record was selected.
	ExecuteFound ExecuteOutcome = iota
	// ExecuteSkip: an ordinary miss that keeps exit 0 - not wired, or no
	// record for the chosen framework.
	ExecuteSkip
	// ExecuteNotRun: a refusal that makes the bulk command exit non-zero but
	// does not stop --fail-fast - the test case is wired to several frameworks
	// and no --framework disambiguated it.
	ExecuteNotRun
	// ExecuteError: a wiring file that APPLIES to this test case (by its
	// canonical filename) is malformed or unreadable. An error on every
	// selection path, counted as errored, with no fallback to a valid record.
	ExecuteError
)

// ExecuteSelection is the classified result of SelectForExecute.
type ExecuteSelection struct {
	Outcome   ExecuteOutcome
	Selection Selection // valid only when Outcome == ExecuteFound
	Reason    string    // short label for a Skip / NotRun / Error line
}

// SelectForExecute resolves wiring for one test case on the execute path
// (BUG-200, narrowed by the 2026-10-25 scope cut).
//
// Explicit framework (framework != "", which also covers an adapter override,
// whose implied framework is known): the selected file is exactly
// tcID--framework.wiring.yaml. A missing file is the ordinary miss
// (ExecuteSkip). A file that exists but is malformed or unreadable is
// ExecuteError, with no fallback. A readable file whose body disagrees with the
// filename is returned as ExecuteFound so the caller's VerifySelection reports
// the identity mismatch (BUG-211).
//
// No-framework discovery (framework == ""): exactly today's generic scanner,
// FindAllForTCSelections. It warns about and drops a malformed or unreadable
// file, and readable bodies decide identity. Discovery does NOT attribute a
// malformed file to a test case by its filename: a filename such as
// tc-x--legacy--bats.wiring.yaml cannot be safely attributed, because test
// case IDs and framework names may both contain "--". BUG-212 owns changing
// discovery. Several readable records for tcID is a refusal (ExecuteNotRun).
func SelectForExecute(projectRoot, tcID, framework string) (ExecuteSelection, error) {
	if err := pathsafe.ValidateFilenameComponent(tcID, "test case ID"); err != nil {
		return ExecuteSelection{}, err
	}

	if framework == "" {
		sels, err := FindAllForTCSelections(projectRoot, tcID)
		if err != nil {
			return ExecuteSelection{}, err
		}
		switch len(sels) {
		case 0:
			return notWired(framework), nil
		case 1:
			return ExecuteSelection{Outcome: ExecuteFound, Selection: sels[0]}, nil
		default:
			// Several frameworks wired and no --framework to disambiguate. This
			// is a refusal (not run), not an ordinary skip (BUG-200 outcome
			// matrix).
			return ExecuteSelection{Outcome: ExecuteNotRun, Reason: "multiple frameworks -- specify --framework"}, nil
		}
	}

	// A framework that cannot be a filename component can name no wiring file:
	// the ordinary miss, as before.
	if err := pathsafe.ValidateFilenameComponent(framework, "framework"); err != nil {
		return notWired(framework), nil
	}
	path := filepath.Join(layout.WiringDir(projectRoot), tcID+"--"+framework+FileSuffix)
	if _, err := os.Lstat(path); err != nil {
		if os.IsNotExist(err) {
			return notWired(framework), nil
		}
		return ExecuteSelection{Outcome: ExecuteError, Reason: "wiring parse error"}, nil
	}
	rec, err := Read(path)
	if err != nil {
		// The exact file the known framework selects is malformed or
		// unreadable: an error, no fallback.
		return ExecuteSelection{Outcome: ExecuteError, Reason: "wiring parse error"}, nil
	}
	// Framework and test case come from the FILENAME, not the body, so a
	// body/filename disagreement is caught by the caller's VerifySelection
	// (BUG-211).
	return ExecuteSelection{Outcome: ExecuteFound, Selection: Selection{
		Record:    rec,
		Path:      path,
		TestCase:  tcID,
		Framework: framework,
	}}, nil
}

// notWired builds the ordinary-miss result for the framework in play.
func notWired(framework string) ExecuteSelection {
	if framework != "" {
		return ExecuteSelection{Outcome: ExecuteSkip, Reason: "no " + framework + " wiring"}
	}
	return ExecuteSelection{Outcome: ExecuteSkip, Reason: "not wired"}
}
