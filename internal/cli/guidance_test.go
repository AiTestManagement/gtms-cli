package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/aitestmanagement/gtms-cli/internal/adapter"
	"github.com/aitestmanagement/gtms-cli/internal/scaffold"
)

func TestWhatHappenedCreate_WithArtifactPaths(t *testing.T) {
	res := &adapter.InvokeResult{
		Status:        "complete",
		Target:        "login",
		ArtifactCount: 2,
		ArtifactPaths: []string{
			"gtms/test/cases/login/tc-aaa1111-login-happy.md",
			"gtms/test/cases/login/tc-aaa2222-login-error.md",
		},
	}

	result := whatHappenedCreate(res)
	// ENH-096: guidance now shows a count summary, not per-file paths
	assert.Contains(t, result, "2 test cases created in gtms/test/cases/login/")
	// TC ids must NOT appear on stderr (they belong on stdout via the TC list)
	assert.NotContains(t, result, "tc-aaa1111")
	assert.NotContains(t, result, "tc-aaa2222")
}

func TestWhatHappenedCreate_WithSingleArtifactPath(t *testing.T) {
	res := &adapter.InvokeResult{
		Status:        "complete",
		Target:        "login",
		ArtifactCount: 1,
		ArtifactPaths: []string{
			"gtms/test/cases/login/tc-aaa1111-login-happy.md",
		},
	}

	result := whatHappenedCreate(res)
	assert.Contains(t, result, "1 test case created in gtms/test/cases/login/")
	assert.NotContains(t, result, "tc-aaa1111")
}

func TestWhatHappenedCreate_WithArtifactCount(t *testing.T) {
	res := &adapter.InvokeResult{
		Status:        "complete",
		Target:        "sprint-14",
		ArtifactCount: 5,
	}

	result := whatHappenedCreate(res)
	assert.Contains(t, result, "5 test cases created")
	assert.Contains(t, result, "sprint-14")
}

func TestWhatHappenedCreate_Error(t *testing.T) {
	res := &adapter.InvokeResult{
		Status:  "error",
		Summary: "adapter timed out",
	}

	result := whatHappenedCreate(res)
	assert.Contains(t, result, "Create failed")
	assert.Contains(t, result, "adapter timed out")
}

// --- BUG-080: whatHappenedPrime uses manual-aware wording ---

func TestWhatHappenedPrime_Success(t *testing.T) {
	res := &adapter.InvokeResult{
		Status:        "complete",
		Target:        "tc-abc12345",
		ArtifactCount: 1,
	}

	result := whatHappenedPrime(res)
	assert.Contains(t, result, "Manual result template stamped")
	assert.Contains(t, result, "tc-abc12345")
	assert.Contains(t, result, "1 file generated")
	// Must NOT contain automate-stage wording
	assert.NotContains(t, result, "Automation")
	assert.NotContains(t, result, "Automated")
}

func TestWhatHappenedPrime_SuccessNoCount(t *testing.T) {
	res := &adapter.InvokeResult{
		Status: "complete",
		Target: "tc-abc12345",
	}

	result := whatHappenedPrime(res)
	assert.Contains(t, result, "Manual result template stamped for tc-abc12345")
	assert.NotContains(t, result, "file generated")
}

func TestWhatHappenedPrime_Error(t *testing.T) {
	res := &adapter.InvokeResult{
		Status:  "error",
		Summary: "script returned exit code 1",
	}

	result := whatHappenedPrime(res)
	assert.Contains(t, result, "Prime failed")
	assert.Contains(t, result, "script returned exit code 1")
	assert.NotContains(t, result, "Automate failed")
}

// --- ENH-186: whatHappenedInit returns a count summary, not a per-file list ---

func TestWhatHappenedInit_CountSummary(t *testing.T) {
	res := &scaffold.Result{
		FilesCreated: []string{
			"gtms.config",
			"gtms/adapters/manual-create-script.sh",
			"gtms/adapters/agent-create-script.sh",
			".gtms/guidance.yaml",
		},
		DirsCreated: []string{
			"gtms/tasks/pending",
			"gtms/tasks/in-progress",
			"gtms/test/cases",
		},
		GitignoreAction: scaffold.GitignoreCreated,
	}

	result := whatHappenedInit(res)

	// Must contain numeric counts
	assert.Contains(t, result, "4 files")
	assert.Contains(t, result, "3 directories")

	// Must NOT contain any scaffold path (except .gitignore carve-out)
	assert.NotContains(t, result, "gtms.config")
	assert.NotContains(t, result, "manual-create-script.sh")
	assert.NotContains(t, result, "guidance.yaml")
	assert.NotContains(t, result, "gtms/tasks")

	// Gitignore carve-out: exact literal pinned by ENH-108 spec suite
	assert.Contains(t, result, "Created .gitignore")

	// Must be ASCII-only
	for _, b := range []byte(result) {
		assert.Less(t, b, byte(128), "non-ASCII byte in summary: %d", b)
	}
}

func TestWhatHappenedInit_WithSkipped(t *testing.T) {
	res := &scaffold.Result{
		FilesCreated: []string{"gtms.config", "gtms/adapters/manual-create-script.sh"},
		DirsCreated:  []string{"gtms/tasks/pending"},
		FilesSkipped: []string{".vscode/settings.json", ".vscode/extensions.json"},
		GitignoreAction: scaffold.GitignoreUnchanged,
	}

	result := whatHappenedInit(res)

	// Count summary present
	assert.Contains(t, result, "2 files")
	assert.Contains(t, result, "1 directories")

	// Skipped count present
	assert.Contains(t, result, "Skipped 2 files")
	assert.Contains(t, result, "already exist")

	// No paths named
	assert.NotContains(t, result, "settings.json")
	assert.NotContains(t, result, "gtms.config")
}

func TestWhatHappenedInit_GitignoreCarveOut(t *testing.T) {
	// The gitignore action is the one path the summary names (carve-out
	// from the count-only rule), using exact literals pinned by the
	// ENH-108 gitignore-action-reporting spec suite.
	t.Run("Created", func(t *testing.T) {
		res := &scaffold.Result{
			FilesCreated:    []string{"gtms.config"},
			DirsCreated:     []string{"gtms"},
			GitignoreAction: scaffold.GitignoreCreated,
		}
		result := whatHappenedInit(res)
		assert.Contains(t, result, "Created .gitignore")
		assert.NotContains(t, result, "Updated .gitignore")
	})

	t.Run("Appended", func(t *testing.T) {
		res := &scaffold.Result{
			FilesCreated:    []string{"gtms.config"},
			DirsCreated:     []string{"gtms"},
			GitignoreAction: scaffold.GitignoreAppended,
		}
		result := whatHappenedInit(res)
		assert.Contains(t, result, "Updated .gitignore (added .gtms/)")
		assert.NotContains(t, result, "Created .gitignore")
	})

	t.Run("Unchanged", func(t *testing.T) {
		res := &scaffold.Result{
			FilesCreated:    []string{"gtms.config"},
			DirsCreated:     []string{"gtms"},
			GitignoreAction: scaffold.GitignoreUnchanged,
		}
		result := whatHappenedInit(res)
		assert.NotContains(t, result, ".gitignore")
	})
}
