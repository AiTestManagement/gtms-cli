// Package reader implements the built-in local-reader adapter.
// It scans the filesystem for test cases, automation records, and task files
// to provide pipeline visibility without invoking external adapters.
package reader

// PipelineEntry represents the pipeline status of a single test case.
type PipelineEntry struct {
	TestCaseID     string `json:"test_case_id"`
	Slug           string `json:"slug"`
	Title          string `json:"title"`
	CreateStatus   string `json:"create_status"`
	AutomateStatus string `json:"automate_status"`
	ExecuteStatus  string `json:"execute_status"`
	LastResult     string `json:"last_result"`
	LastResultDate string `json:"last_result_date"`
}

// PipelineDetailEntry provides detailed pipeline information for a single test case.
type PipelineDetailEntry struct {
	TestCaseID     string   `json:"test_case_id"`
	Slug           string   `json:"slug"`
	Title          string   `json:"title"`
	Requirement    string   `json:"requirement"`
	CreateStatus   string   `json:"create_status"`
	AutomateStatus string   `json:"automate_status"`
	ExecuteStatus  string   `json:"execute_status"`
	LastResult     string   `json:"last_result"`
	LastResultDate string   `json:"last_result_date"`
	Framework      string   `json:"framework"`
	ArtefactPath   string   `json:"artefact_path"`
	LastRunPath    string   `json:"last_run_path"`
	Tags           []string `json:"tags,omitempty"`
}

// GapReport contains all five categories of coverage gaps.
type GapReport struct {
	NoTests          []GapEntry `json:"no_tests"`
	NoAutomation     []GapEntry `json:"no_automation"`
	NeverExecuted    []GapEntry `json:"never_executed"`
	CurrentlyFailing []GapEntry `json:"currently_failing"`
	SpecButNoRecord  []GapEntry `json:"spec_but_no_record"`
}

// GapEntry represents a single item in a gap category.
type GapEntry struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Since string `json:"since,omitempty"`
}

// TriageInfo holds the current state of a test case for triage decision-making.
type TriageInfo struct {
	TestCaseID       string
	AutomationRecord *automationFrontmatter // current automation state
	LastResult       string                 // pass or fail
	LastRun          string                 // path or URL to results
	FailureHistory   []TriageEntry          // previous triage decisions
}

// TriageEntry represents a single triage decision in the history.
type TriageEntry struct {
	Date     string // ISO 8601
	Category string // automation-wrong, test-wrong, app-wrong
	Summary  string
	Defect   string // optional defect link
}

// TriageResult describes what was done by a triage operation.
type TriageResult struct {
	TestCaseID string   `json:"test_case_id"`
	Category   string   `json:"category"`
	Summary    string   `json:"summary"`
	Defect     string   `json:"defect,omitempty"`
	Actions    []string `json:"actions"`
	NewTaskID  string   `json:"new_task_id,omitempty"`
}

// testCaseFrontmatter represents the YAML frontmatter of a test case file.
type testCaseFrontmatter struct {
	ID          string   `yaml:"test_case_id"`
	Title       string   `yaml:"title"`
	Requirement string   `yaml:"requirement"`
	Priority    string   `yaml:"priority,omitempty"`
	Type        string   `yaml:"type,omitempty"`
	Status      string   `yaml:"status,omitempty"`
	Tags        []string `yaml:"tags,omitempty"`
	Created     string   `yaml:"created,omitempty"`
	SourceFile  string   `yaml:"-"` // populated by scanner, not from YAML
}

// automationFrontmatter represents the YAML frontmatter of an automation record.
type automationFrontmatter struct {
	TestCase        string `yaml:"testcase"`
	Framework       string `yaml:"framework"`
	Status          string `yaml:"status"`
	Artefact        string `yaml:"artefact"`
	Adapter         string `yaml:"adapter"`
	LastDevResult   string `yaml:"last-dev-result"`
	LastFormalResult string `yaml:"last-formal-result"`
	LastFormalRun   string `yaml:"last-formal-run"`
	Attempts        int    `yaml:"attempts"`
	Cycle           int    `yaml:"cycle"`
	Defect          string `yaml:"defect"`
}

// taskFrontmatter represents the YAML frontmatter of a task file.
type taskFrontmatter struct {
	ID      string `yaml:"id"`
	Type    string `yaml:"type"`
	Target  string `yaml:"target"`
	Adapter string `yaml:"adapter"`
	Status  string `yaml:"status"`
	Created string `yaml:"created"`
	Branch  string `yaml:"branch"`
}

// MapReport contains the full traceability map grouped by requirement.
type MapReport struct {
	Groups   []RequirementGroup `json:"groups"`
	Unlinked []MapEntry         `json:"unlinked"`
	Summary  MapSummary         `json:"summary"`
}

// RequirementGroup represents one requirement and all its test cases.
type RequirementGroup struct {
	Requirement string     `json:"requirement"`
	TestCases   []MapEntry `json:"test_cases"`
}

// MapEntry represents one test case in the traceability map.
type MapEntry struct {
	TestCaseID     string `json:"test_case_id"`
	Slug           string `json:"slug"`
	Title          string `json:"title"`
	CreateStatus   string `json:"create_status"`
	AutomateStatus string `json:"automate_status"`
	ExecuteStatus  string `json:"execute_status"`
	LastResult     string `json:"last_result"`
}

// MapSummary provides aggregate statistics for the traceability map.
type MapSummary struct {
	TotalRequirements int `json:"total_requirements"`
	TotalTestCases    int `json:"total_test_cases"`
	Automated         int `json:"automated"`
	Executed          int `json:"executed"`
	UnlinkedCount     int `json:"unlinked_count"`
}
