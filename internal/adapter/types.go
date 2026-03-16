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
	OutputSubdir    string // Subfolder path under test-cases/ with trailing /, empty for root level
	SpecFile        string
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
	TestCaseIDs     string // Comma-separated list of pre-generated tc-{7hex} IDs (create command only)
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
	SpecFile     string // execute only: path to automation spec file
	Focus        string // create only: focus area within source document
	ContextFile  string // create only: path to context file
	Context      string // create only: content of context file (read at CLI level)
	Environment  string // automate + execute only: target environment (e.g. staging, production)
	Folder       string // create only: the positional folder arg for create
	Reference    string // create only: the --reference flag value
}
