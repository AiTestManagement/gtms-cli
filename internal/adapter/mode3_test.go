package adapter

import "testing"

// TestIsMode3ExecuteAdapterName_AllFourNames proves all four reserved
// Mode 3 names are recognised by the exported predicate. This is
// the dispatch-table test required by tc-ba2f54e2: it verifies the
// name-based predicate independently of the CLI dispatch order, which
// is tested separately.
func TestIsMode3ExecuteAdapterName_AllFourNames(t *testing.T) {
	reserved := []string{
		"manual-execute",
		"agent-execute",
		"manual-execute-script",
		"agent-execute-script",
	}
	for _, name := range reserved {
		if !IsMode3ExecuteAdapterName(name) {
			t.Errorf("IsMode3ExecuteAdapterName(%q) = false, want true", name)
		}
	}
}

func TestIsMode3ExecuteAdapterName_NonReserved(t *testing.T) {
	nonReserved := []string{
		"bats-runner",
		"playwright-runner",
		"remote-winrm-01",
		"manual-automate",
		"agent-create",
		"",
	}
	for _, name := range nonReserved {
		if IsMode3ExecuteAdapterName(name) {
			t.Errorf("IsMode3ExecuteAdapterName(%q) = true, want false", name)
		}
	}
}
