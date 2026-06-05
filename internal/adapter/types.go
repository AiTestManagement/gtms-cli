package adapter

// AdapterContext holds all context information passed to an adapter invocation.
// For Tier 1: fields are substituted as {variable} in command templates.
// For Tier 2: fields are exported as GTMS_ environment variables.
type AdapterContext struct {
	TaskID          string
	Command         string
	Reference       string
	TestCase        string
	TestCaseContent string // Full content of the test case file (automate command; empty if file not found)
	OutputDir       string
	OutputSubdir    string // Subfolder path under gtms/cases/ with trailing /, empty for root level
	ArtefactFile    string
	TestCaseFile    string // Path to the test case markdown file (automate and execute commands)
	PromptTemplate  string
	Branch          string
	Repo            string
	ProjectRoot     string
	WorkDir         string
	ResultFile      string
	AssembledPrompt string
	PromptFile      string // Path to assembled prompt temp file (.gtms/tmp/{task-id}-prompt.md)
	Focus           string
	Context         string
	ContextFile     string
	Guides          string
	Environment     string // target environment from --env flag
	TestCaseIDs     string // Comma-separated list of pre-generated tc-{8hex} IDs (create command only)
	TestCaseName    string // Optional name for create command (second positional arg)
	TestCaseHash    string // Pre-computed hash of the test case file content (ENH-132: manual prime)
	TCTitle         string // TC frontmatter title snapshot at prime time (ENH-142)
	TCRequirement   string // TC frontmatter requirement snapshot at prime time (ENH-142)
	TCPriority      string // TC frontmatter priority snapshot at prime time (ENH-142)
	TCType          string // TC frontmatter type snapshot at prime time (ENH-142)
	TemplateFile    string // Path to result template file (ENH-132: manual prime)
	OutputFile          string // Path for the single stamped output file (ENH-132: manual prime)
	ResultTemplate      string // Path to the filled manual result file (ENH-133: manual execute)
	ResultValue         string // Parsed result value from manual result file (ENH-133: pass/fail/skip)
	ResultTestCase      string // Parsed testcase from manual result file (ENH-133)
	ResultTestCaseHash  string // Parsed test_case_hash from manual result file (ENH-133)
	Framework           string // Resolved framework for the current command (ENH-151: used by BuiltinAutomate)
	ResultFramework     string // Parsed framework from manual result file (ENH-133)
	ManualExecuteError  error  // Deferred validation error from Go-side parsing (ENH-133)
	Force               bool   // AdapterContext carrier for force flag (not exported to adapters)
}

// InvocationResult holds the output from an adapter invocation.
type InvocationResult struct {
	ExitCode   int
	Stdout     string   // non-file summary content (before first delimiter, after closing tag, or all if no delimiters)
	Stderr     string
	SavedFiles []string // file paths written during streaming (nil if no delimiters found)
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
