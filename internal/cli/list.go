package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/aitestmanagement/gtms-cli/internal/adapter"
	"github.com/aitestmanagement/gtms-cli/internal/config"
	"github.com/aitestmanagement/gtms-cli/internal/output"
)

// effectiveDefault returns the adapter name that resolves as default for a
// command when no --adapter flag is passed. It honours cfg.Defaults[command]
// first and falls back to the resolver's implicit built-in default (e.g.
// prime -> manual-prime). The second return value is false if no default
// exists for the command.
func effectiveDefault(cfg *config.Config, command string) (string, bool) {
	if name, ok := cfg.Defaults[command]; ok && name != "" {
		return name, true
	}
	return adapter.BuiltinCommandDefault(command)
}

// adapterEntry holds one row for the adapters table/JSON output.
// ENH-160: Configured field distinguishes "set in defaults.X but not a no-flag
// runtime default" (execute on manual preset) from a true runtime default.
type adapterEntry struct {
	Command    string `json:"command"`
	Name       string `json:"name"`
	Tier       int    `json:"tier"`
	Tool       string `json:"tool"`
	Framework  string `json:"framework"`
	Mode       string `json:"mode"`
	Default    bool   `json:"default"`
	Configured bool   `json:"configured"`
}

// frameworkEntry holds one row for the frameworks table/JSON output.
type frameworkEntry struct {
	Framework string   `json:"framework"`
	Adapters  []string `json:"adapters"`
}

// commandOrder defines the fixed sort order for command buckets.
var commandOrder = map[string]int{
	"create":   0,
	"automate": 1,
	"execute":  2,
	"prime":    3,
	// built-in reader commands come after pipeline + prime
	"gaps":   4,
	"map":    5,
	"status": 6,
	"triage": 7,
}

// pipelineBuckets lists the three pipeline command buckets that should always
// be rendered (with a hint row if empty) so users never see a silently-omitted
// command. See ENH-086 AC.
var pipelineBuckets = []string{"create", "automate", "execute"}

// emptyBucketHint is the human-readable hint shown in place of an adapter name
// when a pipeline bucket has zero configured adapters. It intentionally names
// both "no adapters" and "gtms init" so users and tests can key off either.
const emptyBucketHint = "(no adapters configured -- run `gtms init --preset bats`)"

// hintTierSentinel marks a row as a placeholder/hint row (no real adapter).
const hintTierSentinel = -1

// builtinAdapters returns all Tier 0 built-in adapter entries: the four reader
// built-ins (gaps, map, status, triage) plus the action built-ins from the
// config package's canonical table (ENH-150: agent-create, manual-create, etc.).
func builtinAdapters() []adapterEntry {
	// Reader built-ins (fixed set, Name is "built-in").
	entries := []adapterEntry{
		{Command: "gaps", Name: "built-in", Tier: 0, Tool: "(built-in)", Framework: "", Mode: "sync", Default: false},
		{Command: "map", Name: "built-in", Tier: 0, Tool: "(built-in)", Framework: "", Mode: "sync", Default: false},
		{Command: "status", Name: "built-in", Tier: 0, Tool: "(built-in)", Framework: "", Mode: "sync", Default: false},
		{Command: "triage", Name: "built-in", Tier: 0, Tool: "(built-in)", Framework: "", Mode: "sync", Default: false},
	}

	// Action built-ins derived from the config package's canonical table.
	for command, names := range config.BuiltinActionAdapterNames() {
		for name := range names {
			entries = append(entries, adapterEntry{
				Command:   command,
				Name:      name,
				Tier:      0,
				Tool:      "(built-in)",
				Framework: "",
				Mode:      "sync",
				Default:   false,
			})
		}
	}

	return entries
}

// deriveTier computes the adapter tier from config fields.
func deriveTier(ac *config.AdapterConfig) int {
	if ac.Command != "" {
		return 1
	}
	if ac.Script != "" {
		return 2
	}
	if ac.Module != "" {
		return 3
	}
	return 0
}

// deriveTool returns the tool string for display.
func deriveTool(ac *config.AdapterConfig) string {
	if ac.Command != "" {
		return ac.Command
	}
	if ac.Script != "" {
		return ac.Script
	}
	if ac.Module != "" {
		return ac.Module
	}
	return "(built-in)"
}

// buildAdapterEntries constructs the sorted list of adapter entries from config + built-ins.
// Empty pipeline buckets (create/automate/execute with zero adapters) are
// represented by a single hint row so the bucket is visibly present rather
// than silently omitted (ENH-086 AC).
func buildAdapterEntries(cfg *config.Config) []adapterEntry {
	var entries []adapterEntry

	for command, adapters := range cfg.Adapters {
		for name, ac := range adapters {
			entries = append(entries, adapterEntry{
				Command:   command,
				Name:      name,
				Tier:      deriveTier(ac),
				Tool:      deriveTool(ac),
				Framework: ac.Framework,
				Mode:      ac.Mode,
			})
		}
	}

	// Emit a hint row for each pipeline bucket that has zero adapters.
	for _, bucket := range pipelineBuckets {
		if len(cfg.Adapters[bucket]) == 0 {
			entries = append(entries, adapterEntry{
				Command:   bucket,
				Name:      emptyBucketHint,
				Tier:      hintTierSentinel,
				Tool:      "",
				Framework: "",
				Mode:      "",
				Default:   false,
			})
		}
	}

	// Append built-in adapters, skipping any whose command:name was already
	// emitted from config (config-defined adapters take precedence over
	// built-ins with the same name -- see resolver.go:55).
	configKeys := make(map[string]bool, len(entries))
	for _, e := range entries {
		if e.Tier != hintTierSentinel {
			configKeys[e.Command+":"+e.Name] = true
		}
	}
	for _, b := range builtinAdapters() {
		if !configKeys[b.Command+":"+b.Name] {
			entries = append(entries, b)
		}
	}

	// Mark the DEFAULT column in a single pass so config-defined and built-in
	// rows are treated symmetrically. For each non-hint entry, check whether
	// its name matches the effective default for its command.
	//
	// ENH-163: Mode 3 execute adapters named in defaults.execute are now true
	// runtime defaults (no-flag `gtms execute` reaches them via the
	// defaults.execute bypass). The ENH-160 Configured=true special case is
	// retired.
	for i := range entries {
		if entries[i].Tier == hintTierSentinel {
			continue
		}
		defaultName, ok := effectiveDefault(cfg, entries[i].Command)
		if !ok || entries[i].Name != defaultName {
			continue
		}
		entries[i].Default = true
	}

	// Sort: by command order, then alphabetical by name
	sort.Slice(entries, func(i, j int) bool {
		oi := commandOrder[entries[i].Command]
		oj := commandOrder[entries[j].Command]
		if oi != oj {
			return oi < oj
		}
		return entries[i].Name < entries[j].Name
	})

	return entries
}

// buildFrameworkEntries groups adapter entries by framework.
// Hint rows (empty-bucket placeholders) are skipped -- they represent absence,
// not real adapters.
func buildFrameworkEntries(entries []adapterEntry) []frameworkEntry {
	groups := make(map[string][]string)

	for _, e := range entries {
		if e.Tier == hintTierSentinel {
			continue
		}
		fw := e.Framework
		if fw == "" {
			fw = "(none)"
		}
		ref := e.Command + ":" + e.Name
		groups[fw] = append(groups[fw], ref)
	}

	var result []frameworkEntry
	for fw, adapters := range groups {
		sort.Strings(adapters)
		result = append(result, frameworkEntry{
			Framework: fw,
			Adapters:  adapters,
		})
	}

	// Sort: named frameworks alphabetical, (none) last
	sort.Slice(result, func(i, j int) bool {
		if result[i].Framework == "(none)" {
			return false
		}
		if result[j].Framework == "(none)" {
			return true
		}
		return result[i].Framework < result[j].Framework
	})

	return result
}

const toolTruncateMax = 45

// truncateTool shortens a tool string for table display.
func truncateTool(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// defaultLabel returns the table label for the DEFAULT column.
// ENH-160: "configured" for execute adapters that are set in defaults but
// require --adapter explicitly (wiring-first design).
func defaultLabel(e adapterEntry) string {
	if e.Default {
		return "*"
	}
	if e.Configured {
		return "configured"
	}
	return ""
}

// renderAdaptersTable writes the adapters table to w.
func renderAdaptersTable(w io.Writer, entries []adapterEntry, showTools bool) {
	if showTools {
		tbl := output.NewTable("COMMAND", "NAME", "TIER", "TOOL", "FRAMEWORK", "MODE", "DEFAULT")
		for _, e := range entries {
			if e.Tier == hintTierSentinel {
				tbl.AddRow(e.Command, e.Name, "", "", "", "", "")
				continue
			}
			fw := e.Framework
			if fw == "" {
				fw = "\u2014"
			}
			tbl.AddRow(
				e.Command,
				e.Name,
				fmt.Sprintf("%d", e.Tier),
				truncateTool(e.Tool, toolTruncateMax),
				fw,
				e.Mode,
				defaultLabel(e),
			)
		}
		tbl.Render(w)
	} else {
		tbl := output.NewTable("COMMAND", "NAME", "TIER", "FRAMEWORK", "MODE", "DEFAULT")
		for _, e := range entries {
			if e.Tier == hintTierSentinel {
				tbl.AddRow(e.Command, e.Name, "", "", "", "")
				continue
			}
			fw := e.Framework
			if fw == "" {
				fw = "\u2014"
			}
			tbl.AddRow(
				e.Command,
				e.Name,
				fmt.Sprintf("%d", e.Tier),
				fw,
				e.Mode,
				defaultLabel(e),
			)
		}
		tbl.Render(w)
	}
}

// renderFrameworksTable writes the frameworks table to w.
func renderFrameworksTable(w io.Writer, entries []frameworkEntry) {
	tbl := output.NewTable("FRAMEWORK", "ADAPTERS")
	for _, e := range entries {
		tbl.AddRow(e.Framework, strings.Join(e.Adapters, ", "))
	}
	tbl.Render(w)
}

func newListCmd() *cobra.Command {
	var jsonOut bool
	var showTools bool

	cmd := &cobra.Command{
		Use:   "list <adapters|frameworks|all>",
		Short: "List configured adapters and frameworks",
		Long: `Show the adapters and frameworks registered for this project: entries from
gtms.config plus the shipped built-in adapters.

Use 'list adapters' for a table of all registered adapters with their tier,
framework, mode, and default status. Use 'list frameworks' to see which
adapters support each test framework.

  gtms list adapters             -- table of all adapters
  gtms list adapters --show-tools -- include the tool/command column
  gtms list adapters --json      -- machine-readable JSON output
  gtms list frameworks           -- adapters grouped by framework
  gtms list all                  -- both views

See USER-GUIDE.md section "gtms list" for details.`,
		// No RunE on parent -- requires a subcommand
	}

	cmd.PersistentFlags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	cmd.PersistentFlags().BoolVar(&showTools, "show-tools", false, "Show the TOOL column (adapter command or script path)")

	cmd.AddCommand(newListAdaptersCmd(&jsonOut, &showTools))
	cmd.AddCommand(newListFrameworksCmd(&jsonOut))
	cmd.AddCommand(newListAllCmd(&jsonOut, &showTools))

	return cmd
}

func newListAdaptersCmd(jsonOut *bool, showTools *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "adapters",
		Short: "List all configured adapters",
		Long: `Render a table of every adapter available to the project -- gtms.config
entries plus the shipped Tier 0 built-ins -- one row per adapter, grouped by
command (create, automate, execute, prime) and then by the built-in readers
(gaps, map, status, triage).

Columns: COMMAND, NAME, TIER, FRAMEWORK, MODE, DEFAULT. A star in the
DEFAULT column marks the adapter that resolves when no --adapter flag is
passed. An em-dash in FRAMEWORK means the adapter did not set one.

Flags:
  --show-tools   Insert a TOOL column containing the tier-1 command
                 template or tier-2 script path ((built-in) for Tier 0
                 rows). The TOOL column is hidden by default because it
                 is often long; pair with --json for full, untruncated
                 values.
  --json         Emit a machine-readable array of adapter records. The
                 "tool" field is always present on every record.

Pipeline commands (create, automate, execute) with no configured adapter
are rendered with a hint row pointing at gtms init --preset bats instead of
being silently omitted.

See USER-GUIDE.md section "What a configured adapter is" for the full adapter model.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := GetConfig()
			entries := buildAdapterEntries(cfg)
			return runListAdapters(os.Stdout, entries, *jsonOut, *showTools)
		},
	}
}

func newListFrameworksCmd(jsonOut *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "frameworks",
		Short: "List adapters grouped by framework",
		Long: `Group registered adapters by their declared framework (bats, playwright,
manual, etc.) so you can see at a glance which frameworks the
project supports and which adapters implement each one.

Adapters that do not declare a framework land under "(none)", rendered
last. Use --json for a machine-readable array of framework records,
each containing the command:name references (for example create:agent-create)
of the adapters that belong to it.

See USER-GUIDE.md section "What a configured adapter is" for the full adapter model.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := GetConfig()
			entries := buildAdapterEntries(cfg)
			fwEntries := buildFrameworkEntries(entries)
			return runListFrameworks(os.Stdout, fwEntries, *jsonOut)
		},
	}
}

func newListAllCmd(jsonOut *bool, showTools *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "all",
		Short: "List adapters and frameworks",
		Long: `Render both the adapters view and the frameworks view in a single
invocation. Plain-text output separates the two with ADAPTERS and
FRAMEWORKS section headers; --json emits a single combined document
with "adapters" and "frameworks" keys.

Flags:
  --show-tools   Insert a TOOL column in the adapters table (same
                 behaviour as 'gtms list adapters --show-tools').
  --json         Emit the combined JSON document.

See USER-GUIDE.md section "What a configured adapter is" for the full adapter model.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := GetConfig()
			entries := buildAdapterEntries(cfg)
			fwEntries := buildFrameworkEntries(entries)
			return runListAll(os.Stdout, entries, fwEntries, *jsonOut, *showTools)
		},
	}
}

// runListAdapters renders the adapters view.
func runListAdapters(w io.Writer, entries []adapterEntry, jsonOut bool, showTools bool) error {
	if jsonOut {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(entries)
	}
	renderAdaptersTable(w, entries, showTools)
	return nil
}

// runListFrameworks renders the frameworks view.
func runListFrameworks(w io.Writer, entries []frameworkEntry, jsonOut bool) error {
	if jsonOut {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(entries)
	}
	renderFrameworksTable(w, entries)
	return nil
}

// listAllJSON is the combined JSON shape for `gtms list all --json`.
type listAllJSON struct {
	Adapters   []adapterEntry   `json:"adapters"`
	Frameworks []frameworkEntry `json:"frameworks"`
}

// runListAll renders both views.
func runListAll(w io.Writer, adapters []adapterEntry, frameworks []frameworkEntry, jsonOut bool, showTools bool) error {
	if jsonOut {
		combined := listAllJSON{
			Adapters:   adapters,
			Frameworks: frameworks,
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(combined)
	}

	fmt.Fprintln(w, "ADAPTERS")
	fmt.Fprintln(w, "========")
	renderAdaptersTable(w, adapters, showTools)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "FRAMEWORKS")
	fmt.Fprintln(w, "==========")
	renderFrameworksTable(w, frameworks)
	return nil
}
