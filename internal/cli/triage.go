package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/output"
	"github.com/aitestmanagement/gtms-cli/internal/pathsafe"
	"github.com/aitestmanagement/gtms-cli/internal/reader"
	targetpkg "github.com/aitestmanagement/gtms-cli/internal/target"
	"github.com/aitestmanagement/gtms-cli/internal/triage"
)

// newTriageCmd builds the 'gtms triage' command (ENH-200 / CON-040).
//
// Four operations: --value <key> classifies the latest execution result for the
// resolved (test case, framework) pair; --resolve marks that record resolved;
// --show reads one record back; --list enumerates records for a scope (ENH-201).
// Cobra uses ArbitraryArgs because --list takes an optional positional (zero or
// one folder or test-case ID, ENH-210) while the other operations require
// exactly one; that mode-specific count is validated manually inside RunE.
func newTriageCmd() *cobra.Command {
	var (
		value           string
		resolveOp       bool
		showOp          bool
		listOp          bool
		includeResolved bool
		recursive       bool
		summary         string
		defect          string
		jsonOut         bool
		frameworkFlag   string
	)

	cmd := &cobra.Command{
		// Keep the <test-case-id> placeholder: TestHelpText_TriageLowercaseUsage
		// and BATS tc-7a3f5ef9 both pin it. The positional is optional under
		// --list, which the Long text explains; the usage line stays as-is
		// rather than breaking a pinned contract for cosmetic accuracy.
		Use:   "triage <test-case-id>",
		Short: "Classify, resolve, show, or list triage records",
		Long: `Classify the latest execution result for a test case, resolve an existing
classification, or read classifications back.
Requires exactly one primary operation: --value, --resolve, --show, or --list.
Under --list, --value is not a second operation: it filters the listed records
by stored key, so 'gtms triage --list --value environment' is one operation.

  gtms triage tc-a1b2c3d4 --value environment --summary "Shared creds expired"
  gtms triage tc-a1b2c3d4 --value app-wrong --defect JIRA-789
  gtms triage tc-a1b2c3d4 --value environment --json
  gtms triage tc-a1b2c3d4 --resolve
  gtms triage tc-a1b2c3d4 --show
  gtms triage --list
  gtms triage --list checkout -r --include-resolved
  gtms triage --list tc-a1b2c3d4
  gtms triage --list --value environment --json

--value keys come from triage.values in gtms.config (built-in defaults apply
when no triage block is configured). --summary and --defect are optional and
valid with --value. --show returns one record (active or resolved). --list
takes an optional [folder|test-case-id] and returns records for a scope: with no
positional the root list covers every record, a folder scopes to that folder's
specs and -r recurses, and a test-case ID lists every framework record for that
test case (without --framework). --include-resolved adds resolved records and
--value filters by stored key. -r applies to folder scopes only. --json prints
machine-readable JSON: a single object for --show, a bare array for --list.`,
		// ArbitraryArgs because the positional count is mode-specific: --list
		// accepts zero or one (folder or test-case ID, ENH-210) while the other
		// operations require exactly one. A Cobra range check would pre-empt the
		// mode-specific diagnostics with generic wording, so the count is
		// validated manually below.
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			displayErr := func(msg, hint string) error {
				output.Errorf(msg, hint)
				return output.AsDisplayed(fmt.Errorf(msg))
			}

			// listMode changes the positional-count rule (0 or 1) and the meaning
			// of the positional (a folder OR a test-case ID, classified below by
			// the shared existence-first classifier, ENH-210). It is known from
			// the flag alone, before operation resolution.
			listMode := listOp

			// Stage 1: positional count.
			if listMode {
				if len(args) > 1 {
					return displayErr(
						"The --list command accepts at most one folder or test-case ID.",
						"Example: gtms triage --list checkout -r")
				}
			} else {
				if len(args) != 1 {
					return displayErr(
						"Exactly one test case ID is required.",
						"Example: gtms triage tc-a1b2c3d4 --value environment")
				}
			}

			// Stage 2: target path-safety and TC-ID validation. In list mode the
			// positional is classified with the shared existence-first classifier
			// (folder OR test-case ID, ENH-210); only the non-list operations take
			// a bare TC ID.
			var target string
			// listRes holds the classification of a list-mode positional; listHasArg
			// says whether one was present. listIsTCTarget is true when the
			// positional classified as a test-case target (bare or folder-qualified),
			// which decides both the -r rejection (stage 4) and dispatch.
			var listRes targetpkg.Result
			listHasArg := false
			listIsTCTarget := false
			if !listMode {
				target = normaliseTarget(strings.ToLower(args[0]))
				if err := validateTargetID(target); err != nil {
					output.Errorf(err.Error(),
						"Use only letters, numbers, dashes, underscores, dots, and forward slashes.")
					return output.AsDisplayed(err)
				}
				if !isTestCaseID(target) {
					return displayErr(
						fmt.Sprintf("Invalid test case ID format: '%s'.", target),
						"Use format like tc-a1b2c3d4.")
				}
			} else if len(args) == 1 {
				res, cerr := targetpkg.Classify(GetProjectRoot(), args[0])
				if cerr != nil {
					output.Errorf(cerr.Error(), "")
					return output.AsDisplayed(cerr)
				}
				listRes = res
				listHasArg = true
				listIsTCTarget = res.Kind == targetpkg.KindTCID
			}

			// Stage 3: exactly one primary operation. In list mode --value is a
			// filter, not the set operation.
			valueSet := cmd.Flags().Changed("value") && !listMode
			ops := make([]string, 0, 4)
			if valueSet {
				ops = append(ops, "--value")
			}
			if resolveOp {
				ops = append(ops, "--resolve")
			}
			if showOp {
				ops = append(ops, "--show")
			}
			if listMode {
				ops = append(ops, "--list")
			}
			switch {
			case len(ops) == 0:
				return displayErr(
					"Exactly one operation is required: --value <key>, --resolve, --show, or --list.",
					"Example: gtms triage tc-a1b2c3d4 --value environment")
			case len(ops) > 1:
				return displayErr(
					fmt.Sprintf("Conflicting operations: %s. Use exactly one.", strings.Join(ops, ", ")),
					"Example: gtms triage tc-a1b2c3d4 --show")
			}

			// Stage 4: companion-flag validation, per selected operation.
			switch {
			case valueSet:
				if recursive {
					return displayErr("-r (--recursive) is not valid with --value.", "It applies only to --list.")
				}
				if includeResolved {
					return displayErr("--include-resolved is not valid with --value.", "It applies only to --list.")
				}
			case resolveOp:
				if summary != "" || defect != "" {
					return displayErr("--summary and --defect are not valid with --resolve.", "They apply only when classifying with --value.")
				}
				if recursive {
					return displayErr("-r (--recursive) is not valid with --resolve.", "It applies only to --list.")
				}
				if includeResolved {
					return displayErr("--include-resolved is not valid with --resolve.", "It applies only to --list.")
				}
			case showOp:
				if summary != "" || defect != "" {
					return displayErr("--summary and --defect are not valid with --show.", "They apply only when classifying with --value.")
				}
				if recursive {
					return displayErr("-r (--recursive) is not valid with --show.", "It applies only to --list.")
				}
				if includeResolved {
					return displayErr("--include-resolved is not valid with --show.", "It applies only to --list.")
				}
			case listMode:
				if cmd.Flags().Changed("framework") {
					return displayErr("--framework is not valid with --list.", "Use --list [folder|test-case-id] with -r, --value <key>, or --include-resolved.")
				}
				if summary != "" || defect != "" {
					return displayErr("--summary and --defect are not valid with --list.", "They apply only when classifying with --value.")
				}
				// -r recurses a folder list scope; it is meaningless for a
				// test-case target, which lists that test case's records
				// regardless of folder depth. Rejected only for a TC target so a
				// folder target keeps -r (decided by classification, not by the
				// mere presence of a positional).
				if listIsTCTarget && recursive {
					return displayErr(
						"-r (--recursive) is not valid with a test-case target.",
						"Recursion applies to folder list scopes; drop -r to list a test case's records.")
				}
			}
			// Framework FORMAT validation where --framework is permitted.
			if !listMode && frameworkFlag != "" && !config.ValidateFramework(frameworkFlag) {
				return displayErr(
					fmt.Sprintf("Invalid framework '%s'. Framework must contain only lowercase letters, digits, and hyphens.", frameworkFlag),
					"Example: --framework playwright")
			}

			root := GetProjectRoot()

			switch {
			case valueSet:
				return runTriageValue(root, target, value, summary, defect, frameworkFlag, jsonOut)
			case resolveOp:
				return runTriageResolve(root, target, frameworkFlag, jsonOut)
			case showOp:
				return runTriageShow(root, target, frameworkFlag, jsonOut)
			default: // listMode
				return runTriageList(root, listHasArg, listRes, value, recursive, includeResolved, jsonOut)
			}
		},
	}

	cmd.Flags().StringVar(&value, "value", "", "Classify the latest result with a configured triage value key (a filter key under --list)")
	cmd.Flags().BoolVar(&resolveOp, "resolve", false, "Mark the existing triage record resolved")
	cmd.Flags().BoolVar(&showOp, "show", false, "Show one triage record for a test case")
	cmd.Flags().BoolVar(&listOp, "list", false, "List triage records (optionally scoped to a folder or a test-case ID)")
	cmd.Flags().BoolVar(&includeResolved, "include-resolved", false, "Include resolved records in --list (active-only by default)")
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "Recurse into subfolders when listing (use with --list)")
	cmd.Flags().StringVar(&summary, "summary", "", "Optional summary of the classification (use with --value)")
	cmd.Flags().StringVar(&defect, "defect", "", "Optional defect ID to record (use with --value)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output the resulting record as JSON")
	cmd.Flags().StringVar(&frameworkFlag, "framework", "", "Framework to triage (required when multiple frameworks exist for a test case)")

	return cmd
}

// runTriageValue resolves the configured values and classifies.
func runTriageValue(root, target, value, summary, defect, framework string, jsonOut bool) error {
	values, err := GetConfig().ResolveTriageValues()
	if err != nil {
		output.Errorf(err.Error(), "")
		return output.AsDisplayed(err)
	}

	rec, err := reader.ClassifyTriage(root, target, value, summary, defect, framework, values)
	if err != nil {
		output.Errorf(err.Error(), "")
		return output.AsDisplayed(err)
	}

	if jsonOut {
		return writeTriageJSON(os.Stdout, rec, values[rec.Value])
	}
	fmt.Fprintf(os.Stdout, "  %s Triaged %s [%s] as %s (result: %s, task: %s)\n",
		output.IconComplete, rec.TestCaseID, rec.Framework, rec.Value, rec.Snapshot.Result, rec.Snapshot.Task)
	return nil
}

// runTriageResolve resolves an existing record. It is deliberately independent
// of configuration: label lookup is best-effort so a config error never blocks
// a resolve.
func runTriageResolve(root, target, framework string, jsonOut bool) error {
	rec, already, err := reader.ResolveTriage(root, target, framework)
	if err != nil {
		output.Errorf(err.Error(), "")
		return output.AsDisplayed(err)
	}

	if jsonOut {
		return writeTriageJSON(os.Stdout, rec, bestEffortLabel(rec.Value))
	}
	if already {
		fmt.Fprintf(os.Stdout, "  %s Triage for %s [%s] was already resolved\n",
			output.IconComplete, rec.TestCaseID, rec.Framework)
		return nil
	}
	fmt.Fprintf(os.Stdout, "  %s Resolved triage for %s [%s]\n",
		output.IconComplete, rec.TestCaseID, rec.Framework)
	return nil
}

// bestEffortLabel resolves the current configured label for a value, returning
// "" on any configuration problem so resolve output never depends on config.
func bestEffortLabel(value string) string {
	cfg := GetConfig()
	if cfg == nil {
		return ""
	}
	values, err := cfg.ResolveTriageValues()
	if err != nil {
		return ""
	}
	return values[value]
}

// triageJSON is the mutation JSON contract (ENH-200). It is the full current
// record plus an optional configuration-derived label. Empty optional fields
// are omitted so scripted consumers can test key presence. It carries none of
// the retired action-oriented keys (actions, new_task_id) and none of ENH-201's
// calculated read fields (result_changed_since_triage).
type triageJSON struct {
	TestCaseID   string         `json:"test_case_id"`
	Framework    string         `json:"framework"`
	Status       string         `json:"status"`
	Value        string         `json:"value"`
	Label        string         `json:"label,omitempty"`
	Summary      string         `json:"summary,omitempty"`
	Defect       string         `json:"defect,omitempty"`
	ClassifiedAt string         `json:"classified_at"`
	ResolvedAt   string         `json:"resolved_at,omitempty"`
	Snapshot     triageJSONSnap `json:"snapshot"`
}

type triageJSONSnap struct {
	Task                 string `json:"task"`
	Result               string `json:"result"`
	CompletedAt          string `json:"completed_at,omitempty"`
	Artefact             string `json:"artefact,omitempty"`
	ArtefactHash         string `json:"artefact_hash,omitempty"`
	TestcaseHashAtTriage string `json:"testcase_hash_at_triage,omitempty"`
}

// buildTriageJSON assembles the shared record shape used by both the mutation
// JSON (writeTriageJSON) and the read JSON (triageShowJSON). label is the
// configured label for the record's value; an empty label is omitted. It
// deliberately carries none of ENH-201's calculated read fields -- the read
// surface adds result_changed_since_triage in its own wrapper so the mutation
// contract (tc-b9f8bb7b) cannot inherit it.
func buildTriageJSON(rec *triage.Record, label string) triageJSON {
	return triageJSON{
		TestCaseID:   rec.TestCaseID,
		Framework:    rec.Framework,
		Status:       rec.Status,
		Value:        rec.Value,
		Label:        label,
		Summary:      rec.Summary,
		Defect:       rec.Defect,
		ClassifiedAt: rec.ClassifiedAt,
		ResolvedAt:   rec.ResolvedAt,
		Snapshot: triageJSONSnap{
			Task:                 rec.Snapshot.Task,
			Result:               rec.Snapshot.Result,
			CompletedAt:          rec.Snapshot.CompletedAt,
			Artefact:             rec.Snapshot.Artefact,
			ArtefactHash:         rec.Snapshot.ArtefactHash,
			TestcaseHashAtTriage: rec.Snapshot.TestcaseHashAtTriage,
		},
	}
}

// writeTriageJSON renders a record as the mutation JSON contract. label is the
// configured label for the record's value; an empty label is omitted.
func writeTriageJSON(w io.Writer, rec *triage.Record, label string) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(buildTriageJSON(rec, label))
}

// triageShowJSON is the ENH-201 read JSON: the full mutation record shape plus
// the nullable result_changed_since_triage comparison. The field is embedded
// with no omitempty so it is ALWAYS present -- true, false, or null -- because
// null ("comparison unavailable") is a distinct, meaningful state a plain
// Boolean cannot express. It appears ONLY on the read surfaces (--show/--list),
// never on mutation output.
type triageShowJSON struct {
	triageJSON
	ResultChangedSinceTriage *bool `json:"result_changed_since_triage"`
}

// runTriageShow reads one record and prints it. Label lookup is best-effort so
// unusable optional value configuration never suppresses a stored record; the
// shared pre-run hook has already enforced that gtms.config itself loaded.
func runTriageShow(root, target, framework string, jsonOut bool) error {
	view, err := reader.ShowTriage(root, target, framework)
	if err != nil {
		output.Errorf(err.Error(), "")
		return output.AsDisplayed(err)
	}
	if jsonOut {
		out := triageShowJSON{
			triageJSON:               buildTriageJSON(view.Record, bestEffortLabel(view.Record.Value)),
			ResultChangedSinceTriage: view.ResultChanged,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	printTriageView(os.Stdout, view)
	return nil
}

// runTriageList lists records for a scope. The positional is classified with the
// shared existence-first classifier (ENH-210):
//   - no positional: the root list (every record, including orphans);
//   - a folder: that folder's specs (with -r for recursion);
//   - a bare test-case ID: every framework record for that test case, queried
//     against the record namespace (an orphan is listable and marked
//     [spec missing]);
//   - a folder-qualified test-case ID: resolved scoped first (identical
//     not-found / ambiguity diagnostics to gtms status), then listed by the
//     bare ID.
//
// Every route renders through renderTriageList, so the JSON array shape and the
// human line shape are identical across scopes.
func runTriageList(root string, hasArg bool, res targetpkg.Result, valueFilter string, recursive, includeResolved, jsonOut bool) error {
	var views []reader.TriageView
	var err error
	switch {
	case !hasArg:
		// Root list enumerates every triage record regardless of spec membership.
		views, err = reader.ListTriage(root, nil, valueFilter, includeResolved)
	case res.Kind == targetpkg.KindTCID:
		if strings.Contains(res.Target, "/") {
			// A folder-qualified target must prove a unique in-folder spec owner
			// before its records are listed (BUG-059 / REV-113 scoped-owner rule).
			if serr := requireScopedSpec(root, res); serr != nil {
				output.Errorf(serr.Error(), "")
				return output.AsDisplayed(serr)
			}
		}
		views, err = reader.ListTriageForTC(root, res.TCID, valueFilter, includeResolved)
	default: // targetpkg.KindFolder
		scope, serr := buildScopeFromArg(root, res.Target, recursive)
		if serr != nil {
			output.Errorf(serr.Error(), "")
			return output.AsDisplayed(serr)
		}
		views, err = reader.ListTriage(root, scope, valueFilter, includeResolved)
	}
	if err != nil {
		output.Errorf(err.Error(), "")
		return output.AsDisplayed(err)
	}
	return renderTriageList(os.Stdout, views, jsonOut)
}

// renderTriageList renders a resolved list of triage views. JSON is a bare array
// (an empty result set is [], never null and never absent); human output is one
// row per record via printTriageListLine, or the empty-result line.
func renderTriageList(w io.Writer, views []reader.TriageView, jsonOut bool) error {
	if jsonOut {
		arr := make([]triageShowJSON, 0, len(views))
		for i := range views {
			arr = append(arr, triageShowJSON{
				triageJSON:               buildTriageJSON(views[i].Record, bestEffortLabel(views[i].Record.Value)),
				ResultChangedSinceTriage: views[i].ResultChanged,
			})
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(arr)
	}

	if len(views) == 0 {
		fmt.Fprintln(w, "  No triage records.")
		return nil
	}
	for i := range views {
		printTriageListLine(w, &views[i])
	}
	return nil
}

// printTriageView renders a single record in human form. The result comparison
// is stated in words: unavailable, changed, or unchanged -- never a bare glyph
// and never a false "unchanged" when no latest result exists to compare.
func printTriageView(w io.Writer, view *reader.TriageView) {
	rec := view.Record
	fmt.Fprintf(w, "  %s %s [%s] %s -- %s\n",
		output.IconComplete, rec.TestCaseID, rec.Framework, rec.Status, rec.Value)
	switch {
	case view.ResultChanged == nil:
		fmt.Fprintf(w, "    Snapshot result: %s (result comparison unavailable -- no latest execution result)\n", rec.Snapshot.Result)
	case *view.ResultChanged:
		fmt.Fprintf(w, "    Snapshot result: %s (latest execution result has changed since triage)\n", rec.Snapshot.Result)
	default:
		fmt.Fprintf(w, "    Snapshot result: %s (unchanged since triage)\n", rec.Snapshot.Result)
	}
	// ENH-209: converge --show with the status detail card by printing the
	// stored summary and defect, each on its own line only when non-empty,
	// indented to the Snapshot result continuation column. Both are persisted
	// record content reaching the terminal at read time, so each is passed
	// through the shared terminal-safe control-character sanitiser before
	// interpolation; JSON continues to expose the original stored strings.
	if rec.Summary != "" {
		fmt.Fprintf(w, "    Summary: %s\n", pathsafe.SanitizeControlChars(rec.Summary))
	}
	if rec.Defect != "" {
		fmt.Fprintf(w, "    Defect:  %s\n", pathsafe.SanitizeControlChars(rec.Defect))
	}
}

// printTriageListLine renders one record as a list row using the stable stored
// value key (never the friendly label), and appends [spec missing] for an
// orphan so a record whose spec is gone is visibly an orphan.
func printTriageListLine(w io.Writer, view *reader.TriageView) {
	rec := view.Record
	line := fmt.Sprintf("  %s %s [%s] %s -- %s",
		output.IconComplete, rec.TestCaseID, rec.Framework, rec.Status, rec.Value)
	// ENH-209: a stored defect is appended as a trailing [<defect>] marker (no
	// column). The defect is persisted content, so it is sanitised for control
	// characters AND its literal brackets are backslash-escaped so a stored
	// value cannot forge a second marker (for example [spec missing]) or break
	// the row onto a second physical line. JSON retains the raw stored value.
	if rec.Defect != "" {
		line += " [" + escapeListMarker(pathsafe.SanitizeControlChars(rec.Defect)) + "]"
	}
	// The orphan marker stays LAST: the defect is record data and [spec missing]
	// is contextual state, so the order is [<defect>] [spec missing] on every
	// list scope (ENH-209).
	if view.SpecMissing {
		line += " [spec missing]"
	}
	fmt.Fprintln(w, line)
}

// escapeListMarker backslash-escapes the literal bracket characters in a value
// destined for a trailing [<value>] list marker, so a stored value containing
// "[" or "]" cannot fabricate or close a marker. It is a rendering-only concern
// kept separate from control-character sanitisation (pathsafe.SanitizeControlChars),
// which the caller applies first; JSON and stored values are never escaped.
func escapeListMarker(s string) string {
	// Escape the backslash FIRST so a stored backslash cannot pair with a
	// following escaped bracket and let the marker close early (REV-166
	// CLAUDE-001): a stored `x\]y` must render as `x\\\]y`, not `x\\]y`, so a
	// reader applying the same convention sees an escaped backslash followed by
	// an escaped bracket, never an escaped backslash then a live `]`.
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "[", `\[`)
	s = strings.ReplaceAll(s, "]", `\]`)
	return s
}
