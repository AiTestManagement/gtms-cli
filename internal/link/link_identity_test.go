package link

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BUG-188 (tc-d1c39672 / REV-156 CLAUDE-002): a wiring file whose parsed body
// identity disagrees with its filename is a data-integrity error, not absence.
// Both normal link and --force must refuse without overwriting it -- --force
// authorises overwriting a VALID record, never a foreign-identity one.
func TestLinkRecord_ForeignIdentityWiring_RefusesEvenWithForce(t *testing.T) {
	root, cfg := commonLinkFixture(t) // spec tc-abc12345, playwright config
	// A spec + artefact for the id we will target.
	createTCSpecFixture(t, root, "tc-11111111")
	require.NoError(t, os.WriteFile(filepath.Join(root, "tests/sample.spec.ts"), []byte("art"), 0o644))

	// Seed a wiring file NAMED tc-11111111--playwright but whose body declares a
	// DIFFERENT test case identity.
	wiringDir := filepath.Join(root, "gtms", "automation", "wiring")
	require.NoError(t, os.MkdirAll(wiringDir, 0o755))
	mismatch := filepath.Join(wiringDir, "tc-11111111--playwright.wiring.yaml")
	body := "testcase: tc-22222222\n" +
		"testcase-hash: 0011223344556677\n" +
		"framework: playwright\n" +
		"adapter: playwright-runner\n" +
		"artefact: tests/sample.spec.ts\n" +
		"artefact-hash: aabbccddeeff0011\n"
	require.NoError(t, os.WriteFile(mismatch, []byte(body), 0o644))
	before, err := os.ReadFile(mismatch)
	require.NoError(t, err)

	// Normal link: refuses, naming the conflicting declared identity.
	err = linkRecordErr(root, cfg, "tc-11111111", "playwright", "tests/sample.spec.ts", false, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tc-22222222", "the foreign declared identity is named")
	assert.NotContains(t, err.Error(), "already exists -- pass --force",
		"a mismatch is an integrity error, not a plain already-exists")

	// --force: same refusal, record byte-identical.
	err = linkRecordErr(root, cfg, "tc-11111111", "playwright", "tests/sample.spec.ts", true, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tc-22222222")

	after, err := os.ReadFile(mismatch)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "the foreign-identity record must never be overwritten")
}
