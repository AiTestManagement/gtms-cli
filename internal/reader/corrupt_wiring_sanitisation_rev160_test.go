package reader

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// REV-160 CLAUDE-007(a) -- the F2 corrupt-wiring delete refusal used to
// interpolate a pre-formatted, repository-derived diagnostic with %s, so control
// and ANSI bytes in a wiring filename reached the terminal verbatim. ADR-024 s7
// requires sanitisation at the CENTRAL rendering boundary, which means the error
// has to still carry FIELDS when it gets there.
//
// Asserted on the typed error directly rather than through a file on disk:
// Windows rejects filenames containing bytes below 0x20, so a fixture-based
// version of this test could only ever run on one platform.
func TestCorruptWiringError_SanitisesEveryRepositoryDerivedField(t *testing.T) {
	e := &CorruptWiringError{
		TCID: "tc-7777\x1b[2J7777",
		Path: "gtms/automation/wiring/tc-77777777\x1b[2J--bats.wiring.yaml",
		Err:  fmt.Errorf("yaml: line 2: found \x07 unexpected character"),
	}

	msg := e.Error()

	for _, b := range []string{"\x1b", "\x07", "\x00", "\x7f"} {
		assert.NotContains(t, msg, b,
			"a control byte from the repository reached the terminal verbatim: %q", msg)
	}
	assert.Contains(t, msg, "tc-7777?[2J7777", "the id must still be identifiable after sanitisation")
	assert.Contains(t, msg, "tc-77777777?[2J--bats.wiring.yaml", "the path must still be identifiable")
	assert.Contains(t, msg, "corrupt or its declared identity disagrees with its filename",
		"the established refusal wording is part of the contract")
}

// The wrapped parse failure stays reachable, so a caller can still inspect it.
func TestCorruptWiringError_UnwrapsTheUnderlyingFailure(t *testing.T) {
	inner := errors.New("boom")
	var e error = &CorruptWiringError{TCID: "tc-11111111", Path: "p", Err: inner}
	assert.True(t, errors.Is(e, inner))
}

// The planner must return the TYPED error, not a flat fmt.Errorf: that is what
// lets runDelete give the refusal the established output.Errorf shape instead of
// falling through to a bare `return err`.
func TestDeleteArtifacts_CorruptWiringReturnsTheTypedError(t *testing.T) {
	root := t.TempDir()
	writeSpec(t, root, "tc-77777777", "gtms/test/cases/checkout/tc-77777777-owner.md")

	wdir := filepath.Join(root, "gtms", "automation", "wiring")
	require.NoError(t, os.MkdirAll(wdir, 0o755))
	corrupt := filepath.Join(wdir, "tc-77777777--bats.wiring.yaml")
	require.NoError(t, os.WriteFile(corrupt, []byte("testcase: tc-99999999\nframework: bats\nadapter: b\nartefact: x\n"), 0o644))

	_, err := DeleteArtifacts(root, nil, "tc-77777777", false, false, true)
	require.Error(t, err)

	var cwe *CorruptWiringError
	require.ErrorAs(t, err, &cwe,
		"the F2 refusal must be typed so the CLI can render it with the standard error shape")
	assert.Equal(t, "tc-77777777", cwe.TCID)
	assert.True(t, strings.HasSuffix(filepath.ToSlash(cwe.Path), "tc-77777777--bats.wiring.yaml"),
		"the offending wiring path must be carried as a field, got %q", cwe.Path)
}
