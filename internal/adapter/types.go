package adapter

// AdapterContext holds all context information passed to an adapter invocation.
// For Tier 1: fields are substituted as {variable} in command templates.
// For Tier 2: fields are exported as GTMS_ environment variables.
type AdapterContext struct {
	TaskID             string
	Command            string
	Reference          string
	TestCase           string
	TestCaseContent    string // Full content of the test case file (automate command; empty if file not found)
	OutputDir          string
	OutputSubdir       string // Subfolder path under gtms/test/cases/ with trailing /, empty for root level
	ArtefactFile       string
	TestCaseFile       string // Path to the test case markdown file (automate and execute commands)
	PromptTemplate     string
	Branch             string
	Repo               string
	ProjectRoot        string
	WorkDir            string
	ResultFile         string
	AssembledPrompt    string
	PromptFile         string // Path to assembled prompt temp file (.gtms/tmp/{task-id}-prompt.md)
	Focus              string
	Context            string
	ContextFile        string
	Guides             string
	Environment        string // target environment from --env flag
	TestCaseIDs        string // Comma-separated list of pre-generated tc-{8hex} IDs (create command only)
	TestCaseName       string // Optional name for create command (second positional arg)
	TestCaseHash       string // Pre-computed hash of the test case file content (ENH-132: manual prime)
	TCTitle            string // TC frontmatter title snapshot at prime time (ENH-142)
	TCRequirement      string // TC frontmatter requirement snapshot at prime time (ENH-142)
	TCPriority         string // TC frontmatter priority snapshot at prime time (ENH-142)
	TCType             string // TC frontmatter type snapshot at prime time (ENH-142)
	TemplateFile       string // ENH-161: role-specific template path. For create, the testcase template (gtms/test/templates/{manual,agent}-testcase.template.md). For prime, the result template (gtms/manual/templates/{manual,agent}-result.template.yaml). Resolved per the resolved adapter name via ResolveTemplatePath. Originally introduced for the manual prime stage in ENH-132; ENH-161 extended it to create and added role-specific routing.
	OutputFile         string // Path for the single stamped output file (ENH-132: manual prime)
	ResultTemplate     string // Path to the filled manual result file (ENH-133: manual execute)
	ResultValue        string // Parsed result value from manual result file (ENH-133: pass/fail/skip)
	ResultTestCase     string // Parsed testcase from manual result file (ENH-133)
	ResultTestCaseHash string // Parsed test_case_hash from manual result file (ENH-133)
	Framework          string // Resolved framework for the current command (ENH-151: used by BuiltinAutomate)
	ResultFramework    string // Parsed framework from manual result file (ENH-133)
	ManualExecuteError error  // Deferred validation error from Go-side parsing (ENH-133)
	Force              bool   // AdapterContext carrier for force flag (not exported to adapters)
	// OutputDirConfigured is true when output-dir was explicitly set in the adapter
	// config (BUG-125). It lets the Tier-0 BuiltinAutomate action prefer the configured
	// ctx.OutputDir over the framework-native default (FrameworkSupport.OutputDir). A
	// plain ctx.OutputDir != "" check cannot signal this: invoker always populates
	// ctx.OutputDir for automate (config value, else the SpecsDir default). Internal
	// carrier, not exported to adapters.
	OutputDirConfigured bool
	// RunDir is the working directory the adapter (Tier 1/2) process runs in (ENH-168).
	// = the WorkDir base, joined with the configured working-dir when set. Both tiers set
	// cmd.Dir from this, replacing the previous Tier-1 ac.WorkDir / Tier-2 ac.ProjectRoot
	// divergence. Equals ProjectRoot when working-dir is unset (no regression). Internal
	// carrier, not exported to adapters; do NOT overload WorkDir / {work_dir} / GTMS_WORK_DIR.
	RunDir string
}

// InvocationResult holds the output from an adapter invocation.
type InvocationResult struct {
	ExitCode   int
	Stdout     string // non-file summary content (before first delimiter, after closing tag, or all if no delimiters)
	Stderr     string
	SavedFiles []string // file paths written during streaming (nil if no delimiters found)

	// ResultOverride lets Tier 0 built-in adapters communicate the test
	// outcome (pass/fail/skip/error) back to handleSyncResult, which would
	// otherwise hardcode `pass` for any exit-0 invocation. ENH-160 surfaced
	// this gap: with `--adapter manual-execute` now routing through Tier 0
	// `BuiltinExecute` (the rename moved the colliding script slot to
	// `manual-execute-script`), the user-authored manual result value
	// from the manual result file must flow through to the pipeline
	// instead of collapsing to `pass`.
	//
	// Tier 1 / Tier 2 adapters communicate their outcome by writing the
	// contract directly (so this field stays empty for them and exit-code
	// fallback continues to apply). Empty value preserves the legacy
	// "exit 0 => pass" default.
	ResultOverride string

	// ArtefactOverride lets Tier 0 built-in adapters communicate an
	// artefact path directly to handleSyncResult, without going through
	// SavedFiles (which also triggers the streaming-summary path and
	// would replace the friendly built-in summary). ENH-160 needs this
	// for Tier 0 BuiltinExecute on the manual path: the filled manual
	// result file is the artefact and must flow through to the handoff
	// contract's artefact field for reader traceability. Pre-ENH-160
	// the Tier 2 manual-execute.sh wrote that field directly; post-
	// rename, the built-in must supply it.
	//
	// Tier 1 / Tier 2 adapters leave this empty -- they write the
	// contract directly or rely on SavedFiles / output-dir scanning.
	ArtefactOverride string
}

// CommandFlags holds the parsed command-line flags for action commands.
type CommandFlags struct {
	Adapter      string
	Framework    string // automate only: test framework (e.g. "playwright")
	ArtefactFile string // execute only: path to automation artefact file
	Focus        string // create only: focus area within source document
	ContextFile  string // create + automate: path to context file
	Context      string // create + automate: content of context file (read at CLI level)
	Environment  string // automate + execute only: target environment (e.g. staging, production)
	ExecutedBy   string // ENH-125: pre-resolved identity for executed_by field (already through the precedence chain)
	Folder       string // create only: the positional folder arg for create
	Reference    string // create only: the --reference flag value
	Name         string // create only: optional name for the test case (second positional arg)
	ArtefactHash string // execute only: SHA-256 hash of the artefact file at invocation time
	Force        bool   // automate: reprocess and overwrite existing output files
}
