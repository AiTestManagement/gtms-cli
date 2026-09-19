package cli

// ENH-201 source-shape guards. These pin two negative structural claims no
// behavioural test can establish, and one contract-leak guard:
//   1. the triage CLI does not open-code a scan of the triage directory -- it
//      asks internal/triage's enumerator (ListAll / ListForTC via the reader);
//   2. the mutation JSON serialiser cannot inherit ENH-201's calculated
//      read-only field result_changed_since_triage.

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSourceShape_TriageCLIDoesNotScanTriageDir pins that the triage CLI routes
// enumeration and selection through the reader/triage package rather than
// walking gtms/triage/ itself. A second enumeration path would drift from the
// canonical one (ENH-201 item 1); the CLI must call the shared read functions.
func TestSourceShape_TriageCLIDoesNotScanTriageDir(t *testing.T) {
	src, err := os.ReadFile("triage.go")
	require.NoError(t, err)
	code := stripGoCommentsCLI(t, "triage.go", string(src))

	// Positive: the read surfaces are delegated to the reader package.
	assert.Contains(t, code, "reader.ShowTriage(",
		"triage.go must show records through reader.ShowTriage")
	assert.Contains(t, code, "reader.ListTriage(",
		"triage.go must list records through reader.ListTriage")

	// Negative: the CLI must not walk or address the triage directory itself.
	for _, lit := range []string{
		"layout.TriageDir(",
		".triage.yaml",
		`"triage"`,
	} {
		assert.NotContainsf(t, code, lit,
			"triage.go must not open-code a triage-directory scan (found %q) -- "+
				"enumeration belongs to internal/triage via the reader", lit)
	}
}

// TestSourceShape_MutationJSONExcludesResultChanged pins that the mutation JSON
// contract (tc-b9f8bb7b) cannot inherit result_changed_since_triage. The shared
// record struct triageJSON must not carry it, and only the read wrapper
// triageShowJSON adds it. A shared serialiser is the likely way the field would
// leak into --value / --resolve output.
func TestSourceShape_MutationJSONExcludesResultChanged(t *testing.T) {
	src, err := os.ReadFile("triage.go")
	require.NoError(t, err)
	content := string(src)

	// Isolate the triageJSON struct declaration (the mutation record shape).
	start := strings.Index(content, "type triageJSON struct")
	require.GreaterOrEqual(t, start, 0, "triageJSON struct must exist")
	end := strings.Index(content[start:], "}")
	require.Greater(t, end, 0)
	mutationStruct := content[start : start+end]
	assert.NotContains(t, mutationStruct, "result_changed_since_triage",
		"the mutation record struct triageJSON must not carry result_changed_since_triage")

	// The field lives only on the read wrapper.
	assert.Contains(t, content, "type triageShowJSON struct",
		"the read JSON wrapper triageShowJSON must exist")
	assert.Contains(t, content, `json:"result_changed_since_triage"`,
		"result_changed_since_triage must be declared on the read wrapper")
}
