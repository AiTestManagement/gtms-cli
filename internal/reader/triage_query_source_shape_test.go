package reader

// ENH-201 source-shape guard for the list-side result join. No behavioural test
// can prove the negative claim that ListTriage does not fall back to a per-test-
// case scan on a large root list: a fixture with one record joins identically
// whether the scan is bulk or per-record. This guard pins the O(records x
// handoffs) regression out structurally.

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSourceShape_ListTriageUsesBulkScanner pins ENH-201 item 7: --list must
// perform ONE canonical bulk terminal-result scan for its scope and join every
// listed record from that single result, never calling scanTerminalResultsForTC
// per test case or per record.
func TestSourceShape_ListTriageUsesBulkScanner(t *testing.T) {
	src, err := os.ReadFile("triage_query.go")
	require.NoError(t, err)
	code := stripGoComments(t, "triage_query.go", string(src))

	// Positive: ListTriage joins through the bulk scanner.
	assert.Contains(t, code, "scanTerminalResults(",
		"ListTriage must join listed records through the single bulk terminal-result scan")

	// Negative: it must not scan per-TC. Isolate the ListTriage function body so
	// ShowTriage's legitimate single-TC scan does not mask a regression here.
	start := strings.Index(code, "func ListTriage(")
	require.GreaterOrEqual(t, start, 0, "ListTriage must exist")
	end := strings.Index(code[start:], "\nfunc ")
	if end < 0 {
		end = len(code) - start
	}
	body := code[start : start+end]
	assert.NotContains(t, body, "scanTerminalResultsForTC(",
		"ListTriage must not call scanTerminalResultsForTC per test case -- that "+
			"reintroduces O(records x handoffs) scanning on a large root list")
}
