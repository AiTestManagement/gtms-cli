package tapparse

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParse_Pass(t *testing.T) {
	line := Parse("ok 1 test name here")
	assert.Equal(t, Pass, line.Result)
	assert.Equal(t, "test name here", line.Name)
	assert.Empty(t, line.Reason)
}

func TestParse_PassMultiDigitNumber(t *testing.T) {
	line := Parse("ok 42 another test")
	assert.Equal(t, Pass, line.Result)
	assert.Equal(t, "another test", line.Name)
}

func TestParse_Fail(t *testing.T) {
	line := Parse("not ok 2 test name here")
	assert.Equal(t, Fail, line.Result)
	assert.Equal(t, "test name here", line.Name)
	assert.Empty(t, line.Reason)
}

func TestParse_FailMultiDigitNumber(t *testing.T) {
	line := Parse("not ok 99 failing test")
	assert.Equal(t, Fail, line.Result)
	assert.Equal(t, "failing test", line.Name)
}

func TestParse_Skip(t *testing.T) {
	line := Parse("ok 3 test name here # skip jq not available")
	assert.Equal(t, Skip, line.Result)
	assert.Equal(t, "test name here", line.Name)
	assert.Equal(t, "jq not available", line.Reason)
}

func TestParse_SkipUppercase(t *testing.T) {
	line := Parse("ok 4 test name # SKIP reason text")
	assert.Equal(t, Skip, line.Result)
	assert.Equal(t, "test name", line.Name)
	assert.Equal(t, "reason text", line.Reason)
}

func TestParse_SkipMixedCase(t *testing.T) {
	line := Parse("ok 5 test name # Skip PowerShell not available on this host")
	assert.Equal(t, Skip, line.Result)
	assert.Equal(t, "test name", line.Name)
	assert.Equal(t, "PowerShell not available on this host", line.Reason)
}

func TestParse_SkipNoName(t *testing.T) {
	line := Parse("ok 10 # skip")
	assert.Equal(t, Skip, line.Result)
	assert.Empty(t, line.Name)
	assert.Empty(t, line.Reason)
}

func TestParse_SkipEmptyReason(t *testing.T) {
	line := Parse("ok 7 some test # skip")
	assert.Equal(t, Skip, line.Result)
	assert.Equal(t, "some test", line.Name)
	assert.Empty(t, line.Reason)
}

func TestParse_Comment(t *testing.T) {
	line := Parse("# this is a comment")
	assert.Equal(t, Ignored, line.Result)
	assert.Empty(t, line.Name)
}

func TestParse_PlanLine(t *testing.T) {
	line := Parse("1..42")
	assert.Equal(t, Ignored, line.Result)
}

func TestParse_BlankLine(t *testing.T) {
	line := Parse("")
	assert.Equal(t, Ignored, line.Result)
}

func TestParse_WhitespaceOnly(t *testing.T) {
	line := Parse("   ")
	assert.Equal(t, Ignored, line.Result)
}

func TestParse_LeadingWhitespace(t *testing.T) {
	line := Parse("  ok 1 test with indent")
	assert.Equal(t, Pass, line.Result)
	assert.Equal(t, "test with indent", line.Name)
}

func TestParse_PassWithHashInName(t *testing.T) {
	// A test name containing "#" but not "# skip" should be a pass.
	line := Parse("ok 1 test with # comment in name")
	assert.Equal(t, Pass, line.Result)
	assert.Equal(t, "test with # comment in name", line.Name)
}

func TestParse_SkipDirectiveNotAtWordBoundary(t *testing.T) {
	// "# skip" anywhere in the line after the number triggers skip detection.
	// This is consistent with TAP spec and BATS behavior.
	line := Parse("ok 1 my test # skip (jq not available)")
	assert.Equal(t, Skip, line.Result)
	assert.Equal(t, "my test", line.Name)
	assert.Equal(t, "(jq not available)", line.Reason)
}
