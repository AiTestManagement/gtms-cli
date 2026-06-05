package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestUpdateTestcaseHash verifies that the test_case_hash line is correctly replaced.
func TestUpdateTestcaseHash(t *testing.T) {
	content := `# yaml-language-server: $schema=../../schemas/manual-result.schema.json
# -- GTMS contract (do not edit) --
test_case_id: tc-12345678
test_case_hash: abcdef0123456789
framework: manual

# -- REQUIRED --
result: pass
`
	newHash := "fedcba9876543210"
	updated := updateTestcaseHash(content, newHash)

	assert.Contains(t, updated, "test_case_hash: fedcba9876543210")
	assert.NotContains(t, updated, "abcdef0123456789")
	// Other fields unchanged
	assert.Contains(t, updated, "test_case_id: tc-12345678")
	assert.Contains(t, updated, "result: pass")
}

// TestStripDriftFields verifies that drift diagnostic fields are removed.
func TestStripDriftFields(t *testing.T) {
	content := `# yaml-language-server: $schema=../../schemas/manual-result.schema.json
test_case_id: tc-12345678
test_case_hash: abcdef0123456789
framework: manual
result: pass
branch: main
drift-detected: true
drift-detected-at: 2026-05-10T14:00:00Z
test_case_hash_at_execute: 1111111111111111
`
	cleaned := stripDriftFields(content)

	assert.NotContains(t, cleaned, "drift-detected")
	assert.NotContains(t, cleaned, "drift-detected-at")
	assert.NotContains(t, cleaned, "test_case_hash_at_execute")
	// Other fields preserved
	assert.Contains(t, cleaned, "test_case_id: tc-12345678")
	assert.Contains(t, cleaned, "test_case_hash: abcdef0123456789")
	assert.Contains(t, cleaned, "result: pass")
	assert.Contains(t, cleaned, "branch: main")
}

// TestStripDriftFields_NoDrift verifies no-op when no drift fields exist.
func TestStripDriftFields_NoDrift(t *testing.T) {
	content := `test_case_id: tc-12345678
test_case_hash: abcdef0123456789
framework: manual
result: pass
`
	cleaned := stripDriftFields(content)
	assert.Contains(t, cleaned, "test_case_id: tc-12345678")
	assert.Contains(t, cleaned, "result: pass")
}

// TestUpdateTestcaseHash_NoMatch verifies graceful no-op when no hash line exists.
func TestUpdateTestcaseHash_NoMatch(t *testing.T) {
	content := `test_case_id: tc-12345678
framework: manual
result: pass
`
	updated := updateTestcaseHash(content, "fedcba9876543210")
	// Should be unchanged since there's no test_case_hash line to match
	assert.Contains(t, updated, "test_case_id: tc-12345678")
	assert.NotContains(t, updated, "fedcba9876543210")
}
