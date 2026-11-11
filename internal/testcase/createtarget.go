package testcase

import (
	"fmt"

	"github.com/aitestmanagement/gtms-cli/internal/layout"
	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
)

// CreateContainmentError is the create-admission-boundary refusal (BUG-214). It
// is returned by ResolveCreateTarget when a create target resolves outside its
// containment boundary -- whether via a `..` escape, a link whose target leaves
// the boundary, a broken link, or an inspection error on an existing component.
//
// Its Error() carries one of the two stable phrases the BUG-214 specs assert on:
//   - "resolves outside the test cases directory" for the default, folder-derived
//     target (no configured output-dir);
//   - "resolves outside the project" for a configured output-dir.
//
// The phrase is a load-bearing substring: the acceptance specs grep stderr for
// it. Keep it exact. The underlying *pathsafe.PathSafetyError is preserved via
// Unwrap so callers can still inspect the cause.
type CreateContainmentError struct {
	// Phrase is the stable containment phrase (one of the two above).
	Phrase string
	// Input is the raw target that was refused (folder arg or output-dir value).
	Input string
	// Cause is the underlying pathsafe resolution failure.
	Cause error
}

func (e *CreateContainmentError) Error() string {
	return fmt.Sprintf("create target %q %s", e.Input, e.Phrase)
}

func (e *CreateContainmentError) Unwrap() error { return e.Cause }

// Stable containment phrases (BUG-214 target contract). Exported-as-constants so
// tests assert against the same strings the error carries.
const (
	// PhraseOutsideTestCases is the refusal phrase for a default (no output-dir)
	// create target that resolves outside the canonical test-case root.
	PhraseOutsideTestCases = "resolves outside the test cases directory"
	// PhraseOutsideProject is the refusal phrase for a configured output-dir that
	// resolves outside the canonical project root.
	PhraseOutsideProject = "resolves outside the project"
)

// ResolveCreateTarget checks a create target for containment at the admission
// boundary and returns the authoritative absolute output directory the caller
// must write into (BUG-214 Owner Decisions 3 and 7).
//
// It never creates anything -- the caller performs MkdirAll on the returned path
// -- and it must be called BEFORE any folder, task file, result contract or
// adapter process is created, so a refusal leaves the project unchanged.
//
// Two modes:
//
//   - configuredOutputDir == "": the default, folder-derived location. Two-stage
//     containment mirrors testcase.ResolveFolder but WITHOUT the must-exist stat,
//     because create legitimately admits an absent leaf below a real in-tree
//     ancestor:
//     1. resolve the cases root beneath the (canonical) project root;
//     2. resolve relativeFolder beneath the canonical cases root.
//     Either stage escaping, a broken link, or an inspection error is refused
//     with PhraseOutsideTestCases. A genuinely-absent leaf under a real in-tree
//     ancestor is returned as a creatable path.
//
//   - configuredOutputDir != "": the configured output-dir. It must resolve
//     inside the canonical project root; an escape (`..`, linked parent), a
//     broken link, or an inspection error is refused with PhraseOutsideProject.
//
// pathsafe.ResolveUnderRoot canonicalises the root (evaluating symlinks) before
// comparing, so a project reached through a link resolves consistently and is
// accepted. The returned path is canonical and absolute; nothing downstream
// recomputes it from the raw input (Decision 7).
func ResolveCreateTarget(projectRoot, configuredOutputDir, relativeFolder string) (string, error) {
	if configuredOutputDir != "" {
		abs, _, err := pathsafe.ResolveUnderRoot(projectRoot, configuredOutputDir)
		if err != nil {
			return "", &CreateContainmentError{
				Phrase: PhraseOutsideProject,
				Input:  configuredOutputDir,
				Cause:  err,
			}
		}
		return abs, nil
	}

	casesRoot, _, err := pathsafe.ResolveUnderRoot(projectRoot, layout.Current().TestCases)
	if err != nil {
		return "", &CreateContainmentError{
			Phrase: PhraseOutsideTestCases,
			Input:  layout.Current().TestCases,
			Cause:  err,
		}
	}

	if relativeFolder == "" {
		return casesRoot, nil
	}

	abs, _, err := pathsafe.ResolveUnderRoot(casesRoot, relativeFolder)
	if err != nil {
		return "", &CreateContainmentError{
			Phrase: PhraseOutsideTestCases,
			Input:  relativeFolder,
			Cause:  err,
		}
	}
	return abs, nil
}
