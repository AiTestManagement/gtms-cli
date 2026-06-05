package reader

// CON-023 / ENH-146 Phase 3C fix-pass: round-trip tests for the
// (Un)MarshalJSON methods on PipelineEntry and PipelineDetailEntry.
//
// These pin two contracts that are easy to regress silently:
//   1. SelectedFramework=="" marshals as `selected_framework: null`
//      (NOT `""`) on both entry types.
//   2. UnmarshalJSON clears any prior SelectedFramework value when the
//      input JSON has null OR omits the key — important for callers that
//      reuse a struct across multiple decode passes.

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPipelineEntry_Marshal_SelectedFrameworkNullWhenEmpty(t *testing.T) {
	e := PipelineEntry{TestCaseID: "tc-a", Wired: false}
	data, err := json.Marshal(e)
	require.NoError(t, err)

	var top map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &top))
	require.Contains(t, top, "selected_framework")
	assert.Nil(t, top["selected_framework"],
		"empty Go field must serialise as JSON null, not empty string")
}

func TestPipelineEntry_Marshal_SelectedFrameworkStringWhenSet(t *testing.T) {
	e := PipelineEntry{TestCaseID: "tc-a", Wired: true, SelectedFramework: "bats"}
	data, err := json.Marshal(e)
	require.NoError(t, err)
	var top map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &top))
	assert.Equal(t, "bats", top["selected_framework"])
}

func TestPipelineEntry_Unmarshal_NullClearsPriorValue(t *testing.T) {
	// Reused struct already populated with a prior decode.
	e := PipelineEntry{TestCaseID: "tc-a", SelectedFramework: "bats", Wired: true}
	raw := []byte(`{"testcase":"tc-b","wired":false,"selected_framework":null,"frameworks":[]}`)
	require.NoError(t, json.Unmarshal(raw, &e))
	assert.Equal(t, "tc-b", e.TestCaseID)
	assert.False(t, e.Wired)
	assert.Empty(t, e.SelectedFramework,
		"selected_framework:null on reused struct must clear the prior value")
}

func TestPipelineEntry_Unmarshal_MissingKeyClearsPriorValue(t *testing.T) {
	e := PipelineEntry{TestCaseID: "tc-a", SelectedFramework: "bats", Wired: true}
	raw := []byte(`{"testcase":"tc-b","wired":false,"frameworks":[]}`)
	require.NoError(t, json.Unmarshal(raw, &e))
	assert.Empty(t, e.SelectedFramework,
		"missing selected_framework key on reused struct must clear the prior value")
}

func TestPipelineEntry_Unmarshal_StringValueRoundTrips(t *testing.T) {
	e := PipelineEntry{}
	raw := []byte(`{"testcase":"tc-b","wired":true,"selected_framework":"playwright","frameworks":[]}`)
	require.NoError(t, json.Unmarshal(raw, &e))
	assert.Equal(t, "playwright", e.SelectedFramework)
}

func TestPipelineDetailEntry_Marshal_SelectedFrameworkNullWhenEmpty(t *testing.T) {
	d := PipelineDetailEntry{TestCaseID: "tc-a"}
	data, err := json.Marshal(d)
	require.NoError(t, err)
	var top map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &top))
	require.Contains(t, top, "selected_framework")
	assert.Nil(t, top["selected_framework"])
}

func TestPipelineDetailEntry_Marshal_SelectedFrameworkStringWhenSet(t *testing.T) {
	d := PipelineDetailEntry{TestCaseID: "tc-a", SelectedFramework: "bats"}
	data, err := json.Marshal(d)
	require.NoError(t, err)
	var top map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &top))
	assert.Equal(t, "bats", top["selected_framework"])
}

func TestPipelineDetailEntry_Unmarshal_NullClearsPriorValue(t *testing.T) {
	d := PipelineDetailEntry{TestCaseID: "tc-a", SelectedFramework: "bats"}
	raw := []byte(`{"testcase":"tc-b","wired":false,"selected_framework":null,"frameworks":[]}`)
	require.NoError(t, json.Unmarshal(raw, &d))
	assert.Empty(t, d.SelectedFramework,
		"selected_framework:null on reused detail struct must clear the prior value")
}

func TestPipelineDetailEntry_Unmarshal_MissingKeyClearsPriorValue(t *testing.T) {
	d := PipelineDetailEntry{TestCaseID: "tc-a", SelectedFramework: "bats"}
	raw := []byte(`{"testcase":"tc-b","wired":false,"frameworks":[]}`)
	require.NoError(t, json.Unmarshal(raw, &d))
	assert.Empty(t, d.SelectedFramework,
		"missing selected_framework key on reused detail struct must clear the prior value")
}

func TestPipelineDetailEntry_Marshal_TestcaseKey(t *testing.T) {
	d := PipelineDetailEntry{TestCaseID: "tc-a", SelectedFramework: "bats"}
	data, err := json.Marshal(d)
	require.NoError(t, err)
	var top map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &top))
	assert.Contains(t, top, "testcase",
		"detail JSON must emit pinned ENH-146 key `testcase`")
	assert.Equal(t, "tc-a", top["testcase"])
	assert.NotContains(t, top, "test_case_id",
		"detail JSON must not emit legacy key `test_case_id`")
}
