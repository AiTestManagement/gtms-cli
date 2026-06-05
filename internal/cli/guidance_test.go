package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/aitestmanagement/gtms-cli/internal/adapter"
)

func TestWhatHappenedCreate_WithArtifactPaths(t *testing.T) {
	res := &adapter.InvokeResult{
		Status:        "complete",
		Target:        "login",
		ArtifactCount: 2,
		ArtifactPaths: []string{
			"gtms/cases/login/tc-aaa1111-login-happy.md",
			"gtms/cases/login/tc-aaa2222-login-error.md",
		},
	}

	result := whatHappenedCreate(res)
	// ENH-096: guidance now shows a count summary, not per-file paths
	assert.Contains(t, result, "2 test cases created in gtms/cases/login/")
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
			"gtms/cases/login/tc-aaa1111-login-happy.md",
		},
	}

	result := whatHappenedCreate(res)
	assert.Contains(t, result, "1 test case created in gtms/cases/login/")
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
