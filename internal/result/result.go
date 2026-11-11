// Package result manages the lifecycle of handoff contracts.
// Handoff contracts are YAML files in .gtms/results/ that track adapter invocation outcomes.
// The file extension is .handoff.yaml (renamed from .result.yaml in ENH-109 to avoid
// collision with the per-test .results.yaml files in gtms/execution/).
package result

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// ResultContract represents the result of an adapter invocation.
//
// CON-023 / ENH-146 added Framework + Git-context fields (and lifted
// ExecutedBy / Environment from the legacy automation record onto the
// result contract). They are stamped by the execute path so the reader's
// overlay join (per ENH-146) has the data it needs without scanning
// legacy records.
//
// GitDirty is a pointer (`*bool`) so the YAML output distinguishes
// "unavailable" (omitted via omitempty when nil) from "clean" (false). A
// plain bool with omitempty would conflate the two -- see the ENH-146
// GOTCHA in PRP-ENH-145-146-147 Task 3.
type ResultContract struct {
	Task         string   `yaml:"task"`
	Command      string   `yaml:"command"`
	Target       string   `yaml:"target"`
	Adapter      string   `yaml:"adapter"`
	Mode         string   `yaml:"mode"`
	Created      string   `yaml:"created"`
	Status       string   `yaml:"status"`           // pending, in-progress, complete, error
	Result       string   `yaml:"result,omitempty"` // pass, fail, skip, error (ENH-130: orthogonal test outcome)
	Artefact     string   `yaml:"artefact,omitempty"`
	ArtefactHash string   `yaml:"artefact-hash,omitempty"`
	Attempts     int      `yaml:"attempts,omitempty"`
	Summary      string   `yaml:"summary,omitempty"`
	Log          string   `yaml:"log,omitempty"`
	NotesSpill   string   `yaml:"notes-spill,omitempty"` // CON-023: lifted from the retired automation record; relative path to full notes when log was truncated (ENH-077/ENH-123)
	Warnings     []string `yaml:"warnings,omitempty"`    // adapter-injectable soft warnings (ENH-096)
	Completed    string   `yaml:"completed,omitempty"`

	// ValidationError carries the gtms create post-write validation violations
	// (BUG-038). The healthy-handoff path writes this key through Update's map
	// merge, which never sees this struct; the field exists so the BUG-191
	// recovery, which marshals this struct wholesale, can keep the key instead
	// of silently dropping it from the durable record (REV-164 CODEX-001).
	ValidationError string `yaml:"validation-error,omitempty"`

	// CON-023 / ENH-146: overlay-join + Git-context fields.
	Framework   string `yaml:"framework,omitempty"`
	GitCommit   string `yaml:"git-commit,omitempty"`
	GitBranch   string `yaml:"git-branch,omitempty"`
	GitDirty    *bool  `yaml:"git-dirty,omitempty"`
	ExecutedBy  string `yaml:"executed_by,omitempty"`
	Environment string `yaml:"environment,omitempty"`
}

// validStatuses is the set of allowed Status values on a ResultContract.
// ENH-130: fail and skipped are retired -- test outcomes live on Result.
var validStatuses = map[string]bool{
	"pending":     true,
	"in-progress": true,
	"complete":    true,
	"error":       true,
}

// validResults is the set of allowed Result values when Result is non-empty.
var validResults = map[string]bool{
	"pass":  true,
	"fail":  true,
	"skip":  true,
	"error": true,
}

// Validate reports whether a ResultContract conforms to the ENH-130 vocabulary
// and required-field rules. Returns nil if valid; an error naming the offending
// field and value otherwise.
//
// Used at both write boundaries (Create / Update) and read boundaries (the
// Tier 2 contract-updated path in invoker.go).
func Validate(rc *ResultContract) error {
	if !validStatuses[rc.Status] {
		return fmt.Errorf("invalid contract status %q: must be one of pending, in-progress, complete, error", rc.Status)
	}
	if rc.Result != "" && !validResults[rc.Result] {
		return fmt.Errorf("invalid contract result %q: must be one of pass, fail, skip, error", rc.Result)
	}
	// Status: complete requires Result to be set.
	if rc.Status == "complete" && rc.Result == "" {
		return fmt.Errorf("contract status is 'complete' but result is empty: a completed adapter run must report a test outcome (pass, fail, skip, or error)")
	}
	// Status: pending / in-progress must NOT have Result set.
	if (rc.Status == "pending" || rc.Status == "in-progress") && rc.Result != "" {
		return fmt.Errorf("contract status is %q but result is %q: result must be empty before the adapter has run", rc.Status, rc.Result)
	}
	return nil
}

// IsTerminalExecuteContract reports whether rc represents a terminal
// (complete or error) EXECUTE handoff. This is the structural guard
// shipped by BUG-130: every consumer that derives pass/skip from
// .gtms/results/ must route through this predicate so the
// command == "execute" dimension lives in exactly one place.
//
// Non-execute commands (create, automate, prime) also write terminal
// handoffs via the shared invoker path, but those must never be read
// as test passes. BUG-124 and BUG-129 were two independent bugs from
// this single root cause; this predicate closes the class.
func IsTerminalExecuteContract(rc *ResultContract) bool {
	if rc == nil {
		return false
	}
	return rc.Command == "execute" && (rc.Status == "complete" || rc.Status == "error")
}

// IsExecuteContract reports whether rc is an EXECUTE handoff, terminal or not.
// It is the BUG-130 structural guard's sibling for consumers that must also
// consider NON-terminal execute handoffs (pending / in-progress) -- e.g.
// isAlreadyPassing, which (REV-263) treats a newer non-terminal execute handoff
// as a non-pass so a run whose durable error write failed cannot fall back to an
// older pass. Keeping the command == "execute" literal here, beside
// IsTerminalExecuteContract, keeps the command dimension in exactly one place.
func IsExecuteContract(rc *ResultContract) bool {
	return rc != nil && rc.Command == "execute"
}

// testCaseScopedCommands is the CLOSED set of commands whose contracts record a
// bare TEST CASE id in `target`. It is a positive allowlist, not a denylist of
// folder-scoped commands, because it gates a DESTRUCTIVE path: anything this set
// does not name is not attributable to a test case and therefore not deletable
// with one.
//
// Adding a command here is a deliberate act that says "this command's `target`
// is a bare TC id". A command added to GTMS and forgotten here loses nothing --
// its contracts simply survive a TC delete, which is the safe direction.
var testCaseScopedCommands = map[string]bool{
	"automate": true,
	"execute":  true,
	"prime":    true,
}

// TargetIsTestCaseScoped reports whether rc's target names a TEST CASE id, so the
// contract may be attributed to that test case for deletion (F1 + the BUG-130
// structural guard: the command dimension lives in exactly one place, never
// inline in a results-dir walker).
//
// ADR-024 s6 mandates positive grammar and destructive paths fail closed, so this
// is an ALLOWLIST of the three TC-scoped commands. The earlier `Command !=
// "create"` denylist inverted that: an empty or missing `command` -- and every
// folder-scoped command added in future -- defaulted to ATTRIBUTABLE, so a
// contract GTMS could not classify was deleted anyway. `create` writes a bare
// FOLDER name in target (no test case exists yet when create runs) and a folder
// can be named character-for-character like a TC id, which is why the command
// dimension has to be decided before the target is compared.
//
// The caller still checks the exact target; this centralises only the command
// dimension.
func TargetIsTestCaseScoped(rc *ResultContract) bool {
	return rc != nil && testCaseScopedCommands[rc.Command]
}

// Create writes a new handoff contract to .gtms/results/{task-id}.handoff.yaml.
// Returns the filepath of the created file.
//
// BUG-209: Create is a thin contract over the shared safe-handoff writer. An
// existing single-link regular file at the path is refused as a collision
// (*RefusedHandoffError "already exists"); a link, hard link or special file is
// refused; a results directory resolving outside the project is refused
// (*RefusedDirError); an absent destination is created with mode 0644 through the
// kernel so the umask filters it (REV-164 / AC12).
func Create(projectRoot string, rc *ResultContract) (string, error) {
	if rc.Task == "" {
		return "", fmt.Errorf("result contract task ID is required")
	}
	if rc.Status == "" {
		rc.Status = "pending"
	}

	// ENH-130: validate before writing.
	if err := Validate(rc); err != nil {
		return "", fmt.Errorf("contract validation failed: %w", err)
	}

	path := ResultPath(projectRoot, rc.Task)

	data, err := yaml.Marshal(rc)
	if err != nil {
		return "", fmt.Errorf("marshalling result contract: %w", err)
	}

	if err := writeHandoff(path, opCreate, data, nil, nil); err != nil {
		return "", err
	}

	return path, nil
}

// Read parses a result contract from the given path.
//
// BUG-209: Read inspects and reads through the same no-follow handle the writers
// use, and applies the section-8 rules: a symlink, dangling symlink, hard link,
// directory or special file is refused (*RefusedHandoffError); a results directory
// resolving outside the project is refused (*RefusedDirError); an absent file is a
// not-found read error (as today); an unreadable regular file (mode 000) is a read
// error (as today), so BUG-191 recovery still runs. It sanitizes the YAML before
// parsing to handle YAML document separators (---) inside block scalar fields.
func Read(path string) (*ResultContract, error) {
	root, absent, err := openResultsDirForPath(path)
	if err != nil {
		return nil, err
	}
	if absent {
		return nil, fmt.Errorf("reading result contract: %w",
			&os.PathError{Op: "open", Path: handoffRel(filepath.Base(path)), Err: os.ErrNotExist})
	}
	defer root.Close()

	data, err := readThroughRoot(root, filepath.Base(path))
	if err != nil {
		if IsRefusal(err) {
			return nil, err
		}
		return nil, fmt.Errorf("reading result contract: %w", err)
	}

	sanitized := sanitizeResultYAML(data)

	var rc ResultContract
	if err := yaml.Unmarshal(sanitized, &rc); err != nil {
		return nil, fmt.Errorf("parsing result contract: %w", err)
	}

	return &rc, nil
}

// Update reads a result contract, applies updates, and writes it back.
// The updates map keys correspond to YAML field names.
//
// BUG-209: Update reads and replaces through one shared, no-follow, atomic,
// metadata-preserving operation, so the bytes merged and the file replaced are
// the same file. The existing mode, uid/gid (Unix) or DACL/owner/group/read-only
// (Windows) are preserved. A link, hard link, special file or directory is
// refused; an absent target is an error; a mode-000 file fails at the read step
// with the destination unchanged (AC11). ENH-130 validates the post-merge
// contract before writing.
func Update(path string, updates map[string]interface{}) error {
	merge := func(data []byte) ([]byte, error) {
		sanitized := sanitizeResultYAML(data)

		var existing map[string]interface{}
		if err := yaml.Unmarshal(sanitized, &existing); err != nil {
			return nil, fmt.Errorf("parsing result contract for update: %w", err)
		}

		// BUG-111 round 3: yaml.Unmarshal on an empty (or all-whitespace) file
		// leaves existing == nil, which would panic on map assignment below. A
		// Tier 2 adapter's heredoc-write can fail partway and truncate the file.
		// Treat the truncated file as a blank slate; Validate catches any
		// status/result mismatch before the write goes through.
		if existing == nil {
			existing = make(map[string]interface{}, len(updates))
		}

		for k, v := range updates {
			existing[k] = v
		}

		// ENH-130: validate the post-merge contract before writing.
		merged, err := yaml.Marshal(existing)
		if err != nil {
			return nil, fmt.Errorf("marshalling merged contract for validation: %w", err)
		}
		var rc ResultContract
		if err := yaml.Unmarshal(merged, &rc); err != nil {
			return nil, fmt.Errorf("parsing merged contract for validation: %w", err)
		}
		if err := Validate(&rc); err != nil {
			return nil, fmt.Errorf("contract validation failed: %w", err)
		}
		return merged, nil
	}

	return writeHandoff(path, opUpdate, nil, merge, nil)
}

// Reconstruct writes a fresh, valid handoff contract to path from a
// ResultContract value, WITHOUT decoding whatever is already on disk.
//
// BUG-191: when a Tier 2 adapter leaves its handoff unusable -- unparseable
// YAML, a deleted file, or a file it made unreadable -- GTMS must still leave a
// readable error handoff behind. Update cannot do this: it reads the existing
// file and decodes it into a map before merging, so it re-hits the very
// read/parse error that triggered recovery. Reconstruct marshals the caller's
// trusted contract and replaces the file outright, without reading it.
//
// BUG-209: Reconstruct is a thin contract over the shared safe-handoff writer, so
// it stages and renames atomically and durably, and it now REFUSES an unsafe leaf
// rather than replacing it: a symlink, dangling symlink, hard link, directory or
// special file is refused (*RefusedHandoffError), and a results directory
// resolving outside the project is refused (*RefusedDirError). A missing target is
// not an error -- recovery must be able to CREATE the handoff (BUG-191
// tc-969e199b) -- and lands with mode 0644 through the kernel (umask filtered). A
// single-link regular file is replaced preserving its permission bits, uid/gid
// (Unix) or DACL/owner/group/read-only (Windows), EXCEPT a mode-000 file, which is
// replaced with owner read/write added (0600) so the recovered handoff is
// readable by its owner (BUG-209 section 5 / AC11). A failed replacement always
// leaves the old handoff in place, never nothing. Callers must still route the
// task to error/ when Reconstruct fails (see recoverUnusableContract).
func Reconstruct(path string, rc *ResultContract) error {
	// ENH-130: never write a contract that would fail validation.
	if err := Validate(rc); err != nil {
		return fmt.Errorf("reconstruct contract validation failed: %w", err)
	}

	data, err := yaml.Marshal(rc)
	if err != nil {
		return fmt.Errorf("marshalling reconstructed contract: %w", err)
	}

	return writeHandoff(path, opReconstruct, data, nil, nil)
}

// ReconstructIfUnchanged is Reconstruct that first proves the handoff is still in the
// captured state. A file replaced or removed since then is refused and left alone, so a
// recovery cannot overwrite a newer writer's file (BUG-209 section 9).
func ReconstructIfUnchanged(path string, rc *ResultContract, prior HandoffState) error {
	if err := Validate(rc); err != nil {
		return fmt.Errorf("reconstruct contract validation failed: %w", err)
	}
	data, err := yaml.Marshal(rc)
	if err != nil {
		return fmt.Errorf("marshalling reconstructed contract: %w", err)
	}
	return writeHandoff(path, opReconstruct, data, nil, &prior)
}

// ResultPath returns the expected path for a result contract given a project root and task ID.
func ResultPath(projectRoot, taskID string) string {
	return filepath.Join(projectRoot, ".gtms", "results", fmt.Sprintf("%s.handoff.yaml", taskID))
}

// sanitizeResultYAML pre-processes result contract YAML to neutralize YAML document
// separators (---) and document end markers (...) that may appear inside block scalar
// fields like log:. Adapter scripts write raw output into the log field via heredocs,
// and that output may contain bare --- lines (e.g. Pester output). The YAML parser
// treats these as document boundaries, silently truncating the parse and losing fields
// that appear after the log block (like completed:).
//
// Strategy: scan lines looking for a block scalar indicator (log: | or log: >).
// Within the block scalar's indented content, replace bare --- and ... with
// safe representations that won't trigger document boundaries.
func sanitizeResultYAML(data []byte) []byte {
	// Strip ANSI escape sequences (e.g. \033[95m) and other control characters
	// that are illegal in YAML. Adapters may capture raw terminal output containing
	// colour codes in the log field; the YAML parser rejects these outright.
	data = stripControlChars(data)

	lines := strings.Split(string(data), "\n")
	inBlockScalar := false
	blockIndent := 0
	var out []string

	for _, line := range lines {
		if !inBlockScalar {
			// Check if this line starts a block scalar for the log field.
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "log:") {
				rest := strings.TrimSpace(trimmed[4:])
				if len(rest) > 0 && (rest[0] == '|' || rest[0] == '>') {
					inBlockScalar = true
					blockIndent = -1 // sentinel: detect from first content line
				}
			}
			out = append(out, line)
			continue
		}

		// Inside the log block scalar.
		// Determine indentation from first non-empty content line.
		if blockIndent == -1 {
			if len(strings.TrimSpace(line)) == 0 {
				out = append(out, line)
				continue
			}
			blockIndent = countLeadingSpaces(line)
			if blockIndent == 0 {
				// No indentation means block scalar is empty/malformed.
				inBlockScalar = false
				out = append(out, line)
				continue
			}
		}

		// Block scalar ends when a non-empty line has less indentation.
		if len(strings.TrimSpace(line)) > 0 && countLeadingSpaces(line) < blockIndent {
			inBlockScalar = false
			out = append(out, line)
			continue
		}

		// Neutralize document separators inside block scalar content.
		stripped := strings.TrimSpace(line)
		if stripped == "---" {
			line = strings.Replace(line, "---", "- - -", 1)
		} else if stripped == "..." {
			line = strings.Replace(line, "...", ". . .", 1)
		}

		out = append(out, line)
	}

	return []byte(strings.Join(out, "\n"))
}

// countLeadingSpaces returns the number of leading space characters in a string.
func countLeadingSpaces(s string) int {
	for i, ch := range s {
		if ch != ' ' {
			return i
		}
	}
	return len(s)
}

// ansiPattern matches ANSI escape sequences: ESC followed by [ then parameters and a command byte.
// Also matches other common escape patterns like ESC(B (charset switch).
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\([A-Z]`)

// stripControlChars removes ANSI escape sequences and YAML-illegal control characters
// from raw bytes. YAML allows TAB (0x09), LF (0x0a), and CR (0x0d) but rejects all
// other C0 control characters. Adapter log output may contain terminal colour codes
// (ESC[...m) that must be stripped before YAML parsing.
func stripControlChars(data []byte) []byte {
	// First strip ANSI escape sequences (multi-byte patterns).
	cleaned := ansiPattern.ReplaceAll(data, nil)

	// Then strip any remaining YAML-illegal control characters (C0 except TAB, LF, CR).
	var buf []byte
	for _, b := range cleaned {
		if b < 0x20 && b != 0x09 && b != 0x0a && b != 0x0d {
			continue // skip illegal control character
		}
		buf = append(buf, b)
	}
	return buf
}
