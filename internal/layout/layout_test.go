package layout

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultFieldValues(t *testing.T) {
	paths := Current()
	assert.Equal(t, "gtms/cases", paths.Cases)
	assert.Equal(t, "gtms/automation", paths.Automation)
	assert.Equal(t, "gtms/tasks", paths.Tasks)
	assert.Equal(t, "gtms/execution", paths.Execution)
	assert.Equal(t, "gtms/scripts", paths.Scripts)
	assert.Equal(t, "gtms/manual", paths.Manual)
	assert.Equal(t, "gtms/schemas", paths.Schemas)
}

func TestCasesDir(t *testing.T) {
	root := "/project"
	assert.Equal(t, filepath.Join(root, "gtms", "cases"), CasesDir(root))
}

func TestAutomationDir(t *testing.T) {
	root := "/project"
	assert.Equal(t, filepath.Join(root, "gtms", "automation"), AutomationDir(root))
}

func TestRecordsDir(t *testing.T) {
	root := "/project"
	assert.Equal(t, filepath.Join(root, "gtms", "automation", "records"), RecordsDir(root))
}

func TestWiringDir(t *testing.T) {
	root := "/project"
	assert.Equal(t, filepath.Join(root, "gtms", "automation", "wiring"), WiringDir(root))
}

func TestWiringDir_WithRenamedParent(t *testing.T) {
	resetLayoutForTest(t)
	InitFromParent("testing")
	root := "/project"
	assert.Equal(t, filepath.Join(root, "testing", "automation", "wiring"), WiringDir(root))
}

func TestSpecsDir(t *testing.T) {
	root := "/project"
	assert.Equal(t, filepath.Join(root, "gtms", "automation", "specs"), SpecsDir(root))
}

func TestTasksDir(t *testing.T) {
	root := "/project"
	assert.Equal(t, filepath.Join(root, "gtms", "tasks"), TasksDir(root))
}

func TestExecutionDir(t *testing.T) {
	root := "/project"
	assert.Equal(t, filepath.Join(root, "gtms", "execution"), ExecutionDir(root))
}

func TestScriptsDir(t *testing.T) {
	root := "/project"
	assert.Equal(t, filepath.Join(root, "gtms", "scripts"), ScriptsDir(root))
}

func TestInitFromParent(t *testing.T) {
	resetLayoutForTest(t)

	InitFromParent("testing")
	paths := Current()
	assert.Equal(t, "testing/cases", paths.Cases)
	assert.Equal(t, "testing/automation", paths.Automation)
	assert.Equal(t, "testing/tasks", paths.Tasks)
	assert.Equal(t, "testing/execution", paths.Execution)
	assert.Equal(t, "testing/scripts", paths.Scripts)
	assert.Equal(t, "testing/manual", paths.Manual)
	assert.Equal(t, "testing/schemas", paths.Schemas)
}

func TestParentDir(t *testing.T) {
	// With default "gtms/cases", ParentDir should return "gtms"
	resetLayoutForTest(t)

	assert.Equal(t, "gtms", ParentDir())

	InitFromParent("testing")
	assert.Equal(t, "testing", ParentDir())
}

func TestInitFromParent_RejectsInvalidInput(t *testing.T) {
	resetLayoutForTest(t)

	cases := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"forward slash", "a/b"},
		{"dot-dot", ".."},
		{"single dot", "."},
		{"embedded dot-dot with slash", "a/../b"},
		{"backslash", "a\\b"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Panics(t, func() { InitFromParent(tc.input) },
				"InitFromParent(%q) should panic", tc.input)
		})
	}
}

func TestAttachmentsDir(t *testing.T) {
	root := "/project"
	assert.Equal(t, filepath.Join(root, "gtms", "execution", "attachments"), AttachmentsDir(root))
}

func TestExecutionLogsDir(t *testing.T) {
	root := "/project"
	assert.Equal(t, filepath.Join(root, "gtms", "execution", "logs"), ExecutionLogsDir(root))
}

func TestAttachmentsDir_WithRenamedParent(t *testing.T) {
	resetLayoutForTest(t)

	InitFromParent("testing")
	root := "/project"
	assert.Equal(t, filepath.Join(root, "testing", "execution", "attachments"), AttachmentsDir(root))
}

func TestExecutionLogsDir_WithRenamedParent(t *testing.T) {
	resetLayoutForTest(t)

	InitFromParent("testing")
	root := "/project"
	assert.Equal(t, filepath.Join(root, "testing", "execution", "logs"), ExecutionLogsDir(root))
}

// ENH-132: Manual and Schemas directory helpers
func TestManualDir(t *testing.T) {
	root := "/project"
	assert.Equal(t, filepath.Join(root, "gtms", "manual"), ManualDir(root))
}

func TestManualRecordsDir(t *testing.T) {
	root := "/project"
	assert.Equal(t, filepath.Join(root, "gtms", "manual", "records"), ManualRecordsDir(root))
}

func TestManualTemplatesDir(t *testing.T) {
	root := "/project"
	assert.Equal(t, filepath.Join(root, "gtms", "manual", "templates"), ManualTemplatesDir(root))
}

func TestSchemasDir(t *testing.T) {
	root := "/project"
	assert.Equal(t, filepath.Join(root, "gtms", "schemas"), SchemasDir(root))
}

func TestManualDir_WithRenamedParent(t *testing.T) {
	resetLayoutForTest(t)

	InitFromParent("testing")
	root := "/project"
	assert.Equal(t, filepath.Join(root, "testing", "manual"), ManualDir(root))
}

func TestManualRecordsDir_WithRenamedParent(t *testing.T) {
	resetLayoutForTest(t)

	InitFromParent("testing")
	root := "/project"
	assert.Equal(t, filepath.Join(root, "testing", "manual", "records"), ManualRecordsDir(root))
}

func TestManualTemplatesDir_WithRenamedParent(t *testing.T) {
	resetLayoutForTest(t)

	InitFromParent("testing")
	root := "/project"
	assert.Equal(t, filepath.Join(root, "testing", "manual", "templates"), ManualTemplatesDir(root))
}

func TestSchemasDir_WithRenamedParent(t *testing.T) {
	resetLayoutForTest(t)

	InitFromParent("testing")
	root := "/project"
	assert.Equal(t, filepath.Join(root, "testing", "schemas"), SchemasDir(root))
}

func TestInitFromParent_AcceptsValidInput(t *testing.T) {
	resetLayoutForTest(t)

	cases := []struct {
		input    string
		wantCase string
	}{
		{"gtms", "gtms/cases"},
		{"testing", "testing/cases"},
		{"my-tests", "my-tests/cases"},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			assert.NotPanics(t, func() { InitFromParent(tc.input) },
				"InitFromParent(%q) should not panic", tc.input)
			assert.Equal(t, tc.wantCase, Current().Cases)
			// Restore for next subtest
			InitFromParent("gtms")
		})
	}
}

func TestInitFromParent_IsReinitializable(t *testing.T) {
	resetLayoutForTest(t)

	InitFromParent("testing")
	assert.Equal(t, "testing/cases", Current().Cases)

	InitFromParent("gtms")
	assert.Equal(t, "gtms/cases", Current().Cases)
}

func TestInitFromParentAndReaders_NoRace(t *testing.T) {
	resetLayoutForTest(t)

	parents := []string{"testing-a", "testing-b", "gtms"}
	var wg sync.WaitGroup

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				_ = Current()
				_ = ParentDir()
				_ = CasesDir("/project")
				_ = RecordsDir("/project")
				_ = ExecutionLogsDir("/project")
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			InitFromParent(parents[i%len(parents)])
		}
	}()

	wg.Wait()

	got := Current().Cases
	assert.Contains(t, []string{"testing-a/cases", "testing-b/cases", "gtms/cases"}, got)
}

func resetLayoutForTest(t *testing.T) {
	t.Helper()
	orig := Current()
	t.Cleanup(func() {
		defaultMu.Lock()
		defaultPaths = orig
		defaultMu.Unlock()
	})
}
