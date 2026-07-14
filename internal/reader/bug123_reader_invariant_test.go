package reader

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBUG123_ManualReaderInvariant_DefaultFrameworkManual proves that wiring the
// manual preset's automate default -- which makes config.DefaultFramework return
// "manual" -- does NOT perturb the reader commands manual users live in.
//
// BUG-123 / PRP Key Decision #2: the manual preset gains
// `defaults.automate: manual-automate` (framework: manual) so `gtms automate`
// returns the prime/execute diagnostic rather than the generic empty-adapters
// error. A side effect is that config.DefaultFramework(cfg) now returns "manual"
// (previously always ""), and that value flows into PipelineStatus / Gaps / Map.
//
// defaultFramework is honoured ONLY in strict-framework mode (picker.go:115,
// selectAutomationRecord), and the bare status/gaps/map paths run non-strict.
// So the output must be identical with defaultFramework "" vs "manual". This
// test is the gate for the contingency: if it fails, the manual preset's
// automate default must NOT be wired (see PRP-BUG-123 Key Decision #2).
//
// The same invariance is independently guarded at the CLI layer by the BUG-082
// suite (internal/cli/status_test.go); this test pins it at the reader layer
// against a manual-specific fixture.
func TestBUG123_ManualReaderInvariant_DefaultFrameworkManual(t *testing.T) {
	root := t.TempDir()

	// Manual-only project: TCs with manual result records (no wiring records).
	writeFile(t, root, filepath.Join("gtms/test/cases", "demo", "tc-aaa1111-pass.md"), `---
test_case_id: tc-aaa1111
title: Manual Pass
requirement: REQ-A
---
`)
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-aaa1111", Framework: "manual", Result: "pass"})

	writeFile(t, root, filepath.Join("gtms/test/cases", "demo", "tc-bbb2222-fail.md"), `---
test_case_id: tc-bbb2222
title: Manual Fail
requirement: REQ-A
---
`)
	seedLegacyRecord(t, root, legacyRecord{TC: "tc-bbb2222", Framework: "manual", Result: "fail"})

	// A TC with no records at all (mixed reality the reader must handle).
	writeFile(t, root, filepath.Join("gtms/test/cases", "demo", "tc-ccc3333-none.md"), `---
test_case_id: tc-ccc3333
title: No Records
requirement: REQ-A
---
`)

	// status: identical with defaultFramework "" vs "manual".
	statusEmpty, err := PipelineStatus(root, nil, "", false)
	require.NoError(t, err)
	statusManual, err := PipelineStatus(root, nil, "manual", false)
	require.NoError(t, err)
	assert.True(t, reflect.DeepEqual(statusEmpty, statusManual),
		"status output must be identical with defaultFramework \"\" vs \"manual\"")

	// gaps: identical.
	gapsEmpty, err := Gaps(root, nil, "", false)
	require.NoError(t, err)
	gapsManual, err := Gaps(root, nil, "manual", false)
	require.NoError(t, err)
	assert.True(t, reflect.DeepEqual(gapsEmpty, gapsManual),
		"gaps output must be identical with defaultFramework \"\" vs \"manual\"")

	// map: identical.
	mapEmpty, err := Map(root, nil, "", false)
	require.NoError(t, err)
	mapManual, err := Map(root, nil, "manual", false)
	require.NoError(t, err)
	assert.True(t, reflect.DeepEqual(mapEmpty, mapManual),
		"map output must be identical with defaultFramework \"\" vs \"manual\"")
}
